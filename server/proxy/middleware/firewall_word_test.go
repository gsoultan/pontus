package middleware

import (
	"slices"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

func newWordFirewall(t *testing.T, words ...string) *Firewall {
	t.Helper()
	return NewFirewall(&config.Firewall{Enabled: true, BlockedWords: words}, nil)
}

// Matching blocked words with strings.Contains on the uppercased query blocked
// any statement that merely mentioned an identifier containing the word.
func TestBlockedWordMatchesTokensNotSubstrings(t *testing.T) {
	fw := newWordFirewall(t, "DROP", "TRUNCATE")

	allowed := []string{
		"SELECT dropdown FROM settings",
		"SELECT * FROM backdrop WHERE id = 1",
		"UPDATE forms SET dropdown_label = 'x'",
		"SELECT truncated_at FROM audit_log",
	}
	for _, query := range allowed {
		if word, blocked := fw.blockedWord(slices.Collect(protocol.Tokenize(query)), query); blocked {
			t.Errorf("blockedWord(%q) matched %q; identifiers must not trip a keyword rule", query, word)
		}
	}

	denied := []string{
		"DROP TABLE users",
		"drop table users",
		"TRUNCATE audit_log",
	}
	for _, query := range denied {
		if _, blocked := fw.blockedWord(slices.Collect(protocol.Tokenize(query)), query); !blocked {
			t.Errorf("blockedWord(%q) allowed a statement that must be blocked", query)
		}
	}
}

// A rule containing whitespace is a phrase and still matches as a substring.
func TestBlockedPhraseStillMatches(t *testing.T) {
	fw := newWordFirewall(t, "DROP TABLE")

	if _, blocked := fw.blockedWord(slices.Collect(protocol.Tokenize("DROP TABLE users")), "DROP TABLE users"); !blocked {
		t.Error("phrase rule did not match")
	}
	if _, blocked := fw.blockedWord(slices.Collect(protocol.Tokenize("SELECT dropdown FROM tables")), "SELECT dropdown FROM tables"); blocked {
		t.Error("phrase rule matched an unrelated statement")
	}
}

func TestBlockedWordIsCaseInsensitive(t *testing.T) {
	fw := newWordFirewall(t, "delete")

	for _, query := range []string{"DELETE FROM t", "delete from t", "DeLeTe FROM t"} {
		if _, blocked := fw.blockedWord(slices.Collect(protocol.Tokenize(query)), query); !blocked {
			t.Errorf("blockedWord(%q) should be blocked regardless of case", query)
		}
	}
}

func TestNoBlockedWordsAllowsEverything(t *testing.T) {
	fw := newWordFirewall(t)

	if _, blocked := fw.blockedWord(slices.Collect(protocol.Tokenize("DROP TABLE users")), "DROP TABLE users"); blocked {
		t.Error("no rules configured, nothing should be blocked")
	}
}

func TestBlockedWordIgnoresBlankRules(t *testing.T) {
	fw := newWordFirewall(t, "", "   ", "DROP")

	if _, blocked := fw.blockedWord(slices.Collect(protocol.Tokenize("SELECT 1")), "SELECT 1"); blocked {
		t.Error("a blank rule must not block every query")
	}
	if _, blocked := fw.blockedWord(slices.Collect(protocol.Tokenize("DROP TABLE t")), "DROP TABLE t"); !blocked {
		t.Error("a real rule alongside blank ones must still apply")
	}
}

// Measures the matching cost alone. Handle tokenizes once and shares the
// stream with isMalicious, so tokenization is hoisted out of the loop here to
// match what the query path actually pays per rule set.
func BenchmarkBlockedWord(b *testing.B) {
	fw := NewFirewall(&config.Firewall{
		Enabled:      true,
		BlockedWords: []string{"DROP", "TRUNCATE", "ALTER", "GRANT", "REVOKE"},
	}, nil)
	query := "SELECT id, name, dropdown FROM users JOIN orders ON users.id = orders.user_id WHERE created_at > $1"
	tokens := slices.Collect(protocol.Tokenize(query))

	b.ReportAllocs()
	for b.Loop() {
		fw.blockedWord(tokens, query)
	}
}

// The previous implementation uppercased the whole query once per rule.
func BenchmarkBlockedWordLegacyContains(b *testing.B) {
	words := []string{"DROP", "TRUNCATE", "ALTER", "GRANT", "REVOKE"}
	query := "SELECT id, name, dropdown FROM users JOIN orders ON users.id = orders.user_id WHERE created_at > $1"

	b.ReportAllocs()
	for b.Loop() {
		// No early break: this is the cost when nothing matches, which is the
		// common case. With a break it exits on the first rule only because
		// "dropdown" false-positives against "DROP" — the bug being fixed.
		for _, word := range words {
			_ = strings.Contains(strings.ToUpper(query), strings.ToUpper(word))
		}
	}
}
