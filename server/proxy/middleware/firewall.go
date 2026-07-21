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
}

func NewFirewall(config *config.Firewall, handler protocol.Handler) *Firewall {
	return &Firewall{config: config, handler: handler}
}

func (m *Firewall) Handle(ctx context.Context, s *Session, next HandlerFunc) error {
	if m.config == nil || !m.config.Enabled || s.Normalized == "" {
		return next(ctx, s)
	}

	// WAF 2.0: Advanced SQL Injection Detection
	if m.isMalicious(s.Normalized) {
		slog.Warn("Blocked malicious query by WAF 2.0", "client", s.RemoteAddr, "query", s.Normalized)
		observability.DefaultTracker.RecordFirewallViolation("pattern")
		return fmt.Errorf("blocked by firewall (WAF 2.0)")
	}

	for _, word := range m.config.BlockedWords {
		if strings.Contains(strings.ToUpper(s.Normalized), strings.ToUpper(word)) {
			slog.Warn("Blocked query by firewall (word)", "client", s.RemoteAddr, "query", s.Normalized, "word", word)
			observability.DefaultTracker.RecordFirewallViolation("word")
			return fmt.Errorf("blocked by firewall")
		}
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

func (m *Firewall) isMalicious(query string) bool {
	// Structural Firewall: Analysis of Token stream
	tokens := slices.Collect(protocol.Tokenize(query))

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
