package middleware

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/pkg/observability"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

type Firewall struct {
	config  *config.Firewall
	handler protocol.Handler

	// blockedTokens holds single-word rules, uppercased once at construction
	// and matched against the token stream. blockedPhrases holds rules that
	// contain whitespace, which have to be matched as substrings.
	blockedTokens  map[string]string
	blockedPhrases [][2]string // {uppercased, original}
}

func NewFirewall(config *config.Firewall, handler protocol.Handler) *Firewall {
	f := &Firewall{config: config, handler: handler}
	if config == nil {
		return f
	}

	// Uppercasing every rule against every query allocated a full copy of the
	// query per rule per request. Normalize the rules once instead.
	f.blockedTokens = make(map[string]string, len(config.BlockedWords))
	for _, word := range config.BlockedWords {
		trimmed := strings.TrimSpace(word)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		if strings.ContainsAny(trimmed, " \t") {
			f.blockedPhrases = append(f.blockedPhrases, [2]string{upper, word})
			continue
		}
		f.blockedTokens[upper] = word
	}
	return f
}

func (m *Firewall) Handle(ctx context.Context, s *Session, next HandlerFunc) error {
	if m.config == nil || !m.config.Enabled || s.Normalized == "" {
		return next(ctx, s)
	}

	// Tokenize once and share the stream. Both the structural checks and the
	// blocked-word rules need it, and the query path should not pay for two
	// passes over the same statement.
	tokens := slices.Collect(protocol.Tokenize(s.Normalized))

	// WAF 2.0: Advanced SQL Injection Detection
	if m.isMalicious(tokens) {
		slog.Warn("Blocked malicious query by WAF 2.0", "client", s.RemoteAddr, "query", s.Normalized)
		observability.DefaultTracker.RecordFirewallViolation("pattern")
		return fmt.Errorf("blocked by firewall (WAF 2.0)")
	}

	if word, blocked := m.blockedWord(tokens, s.Normalized); blocked {
		slog.Warn("Blocked query by firewall (word)", "client", s.RemoteAddr, "query", s.Normalized, "word", word)
		observability.DefaultTracker.RecordFirewallViolation("word")
		return fmt.Errorf("blocked by firewall")
	}

	// WAF 3.0: Dynamic Data Masking
	if len(m.config.MaskingRules) > 0 {
		rewritten, err := m.handler.RewriteQuery(s.Data, m.config.MaskingRules)
		if err == nil && !bytes.Equal(s.Data, rewritten) {
			slog.Debug("Query rewritten by WAF 3.0 (Masking)", "client", s.RemoteAddr)
			s.Data = rewritten
			// Re-classify and normalize since the query changed
			s.Normalized = m.handler.NormalizeQuery(s.Data)
			s.QueryInfo = m.handler.ClassifyQuery(s.Data)
		}
	}

	return next(ctx, s)
}

func (m *Firewall) isMalicious(tokens []protocol.Token) bool {
	isDelete := false
	hasWhere := false

	for i, t := range tokens {
		// Detect DELETE without WHERE
		if t.Type == protocol.TokenKeyword && t.Value == "DELETE" {
			isDelete = true
		}
		if t.Type == protocol.TokenKeyword && t.Value == "WHERE" {
			hasWhere = true
		}

		// Detect Tautology: OR 1=1 or 'a'='a'
		if t.Type == protocol.TokenKeyword && (t.Value == "OR" || t.Value == "AND") {
			if i+3 < len(tokens) && tokens[i+2].Value == "=" {
				if tokens[i+1].Value == tokens[i+3].Value {
					return true
				}
			}
		}

		// Detect UNION-based exfiltration
		if t.Type == protocol.TokenKeyword && t.Value == "UNION" {
			if i+1 < len(tokens) && tokens[i+1].Value == "SELECT" {
				return true
			}
		}

		// Detect administrative functions or sensitive tables access
		if t.Type == protocol.TokenIdentifier {
			val := strings.ToLower(t.Value)
			if val == "pg_shadow" || val == "pg_authid" || val == "mysql.user" {
				return true
			}
		}
	}

	if isDelete && !hasWhere {
		slog.Warn("Blocked DELETE statement without WHERE clause")
		return true
	}

	return false
}

// blockedWord reports whether a query trips a blocked-word rule.
//
// Single-word rules are matched against the token stream, not as substrings of
// the query text. Matching "DROP" with strings.Contains blocked any query
// mentioning a column named `dropdown` or a table named `backdrop` — a WAF
// that blocks legitimate traffic gets turned off, which is the real failure.
// Quoted string literals are skipped so a rule cannot be tripped by data.
func (m *Firewall) blockedWord(tokens []protocol.Token, query string) (string, bool) {
	for _, phrase := range m.blockedPhrases {
		if strings.Contains(strings.ToUpper(query), phrase[0]) {
			return phrase[1], true
		}
	}

	if len(m.blockedTokens) == 0 {
		return "", false
	}

	for _, token := range tokens {
		switch token.Type {
		case protocol.TokenKeyword:
			// Tokenize already emits keywords uppercased, so this is a plain
			// map hit with no per-token allocation.
			if original, ok := m.blockedTokens[token.Value]; ok {
				return original, true
			}
		case protocol.TokenIdentifier:
			// Identifiers keep their original case. Compare with EqualFold
			// rather than uppercasing, which would allocate a string per token
			// on the query path.
			for upper, original := range m.blockedTokens {
				if strings.EqualFold(token.Value, upper) {
					return original, true
				}
			}
		}
	}
	return "", false
}
