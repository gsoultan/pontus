package protocol

import (
	"testing"
	"testing/quick"
)

func tokensOf(q string) []Token {
	var out []Token
	for tok := range Tokenize(q) {
		out = append(out, tok)
	}
	return out
}

func values(q string) []string {
	var out []string
	for tok := range Tokenize(q) {
		out = append(out, tok.Value)
	}
	return out
}

// A literal's value excludes its quotes and resolves ” to one quote. Nothing
// covered this, and the allocation-free rewrite changed how it is read: without
// an escape the value is now a substring, with one it is still assembled.
func TestTokenizeReadsQuotedLiterals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  []string
	}{
		{"plain", "SELECT 'abc'", []string{"SELECT", "abc"}},
		{"empty", "SELECT ''", []string{"SELECT", ""}},
		{"escaped quote", "SELECT 'it''s'", []string{"SELECT", "it's"}},
		{"only an escaped quote", "SELECT ''''", []string{"SELECT", "'"}},
		{"escape then more", "SELECT 'a''b' FROM t", []string{"SELECT", "a'b", "FROM", "t"}},
		{"two literals", "SELECT 'a', 'b'", []string{"SELECT", "a", ",", "b"}},
		{"two literals with escapes", "SELECT 'a''a', 'b''b'", []string{"SELECT", "a'a", ",", "b'b"}},
		{"unterminated", "SELECT 'abc", []string{"SELECT", "abc"}},
		// The token after a literal is what a mis-counted closing quote breaks,
		// and a swallowed FROM is how a write gets classified as a read.
		{"clause after literal", "DELETE FROM t WHERE name = 'x' RETURNING id",
			[]string{"DELETE", "FROM", "t", "WHERE", "name", "=", "x", "RETURNING", "id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := values(tc.query)
			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("token %d = %q, want %q (all: %q)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

// Keywords come back uppercase whatever case they were written in, because
// ClassifyQuery switches on the uppercase form.
func TestTokenizeNormalisesKeywordCase(t *testing.T) {
	for _, query := range []string{"select * from t", "SELECT * FROM t", "SeLeCt * FrOm t"} {
		got := tokensOf(query)
		if got[0].Type != TokenKeyword || got[0].Value != "SELECT" {
			t.Errorf("%q: first token = %v/%q, want keyword SELECT", query, got[0].Type, got[0].Value)
		}
	}

	// An identifier keeps the case it was written in: it names a real object.
	got := tokensOf("SELECT MyColumn FROM MyTable")
	if got[1].Value != "MyColumn" {
		t.Errorf("identifier = %q, want MyColumn", got[1].Value)
	}
	if got[1].Type != TokenIdentifier {
		t.Errorf("MyColumn is a %v, want an identifier", got[1].Type)
	}
}

// A word long enough to overrun the fold buffer must be an identifier rather
// than a panic.
func TestTokenizeHandlesWordsPastTheKeywordBuffer(t *testing.T) {
	long := "a_very_long_identifier_well_past_the_keyword_buffer"
	got := tokensOf("SELECT " + long)

	if got[1].Value != long {
		t.Errorf("identifier = %q, want %q", got[1].Value, long)
	}
	if got[1].Type != TokenIdentifier {
		t.Errorf("long word is a %v, want an identifier", got[1].Type)
	}
}

// Identifiers may be UTF-8, and the rewrite walks bytes rather than runes.
func TestTokenizeHandlesMultiByteIdentifiers(t *testing.T) {
	got := values("SELECT café FROM naïve")
	want := []string{"SELECT", "café", "FROM", "naïve"}

	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("token %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Whatever the input, tokenizing must terminate and never index out of range.
// The rewrite advances by rune width in several places, and a path that fails
// to advance is an infinite loop on the query hot path.
func TestTokenizeTerminatesOnAnyInput(t *testing.T) {
	if err := quick.Check(func(q string) bool {
		for range Tokenize(q) {
		}
		return true
	}, &quick.Config{MaxCount: 2000}); err != nil {
		t.Error(err)
	}
}

// Every token is a piece of the input or an interned keyword, so tokenizing
// must not allocate. This is the property the rewrite exists for, and a
// benchmark records the number while this fails the build if it regresses.
func TestTokenizeDoesNotAllocate(t *testing.T) {
	for name, query := range benchQueries {
		t.Run(name, func(t *testing.T) {
			got := testing.AllocsPerRun(100, func() {
				for range Tokenize(query) {
				}
			})
			if got != 0 {
				t.Errorf("Tokenize allocated %.0f times per call; it runs on every statement", got)
			}
		})
	}

	// The one documented exception, so the claim above stays honest.
	escaped := testing.AllocsPerRun(100, func() {
		for range Tokenize("SELECT 'it''s'") {
		}
	})
	if escaped == 0 {
		t.Log("an escaped quote no longer allocates; the comment on Tokenize can be simplified")
	}
}

func TestIsKeywordMatchesTheTable(t *testing.T) {
	for _, word := range keywordList {
		if !isKeyword(word) {
			t.Errorf("isKeyword(%q) = false, but it is in the table", word)
		}
		if got, ok := keywordOf(word); !ok || got != word {
			t.Errorf("keywordOf(%q) = %q/%v, want the word itself", word, got, ok)
		}
	}
	for _, word := range []string{"users", "orders", "", "SELECTED"} {
		if isKeyword(word) {
			t.Errorf("isKeyword(%q) = true", word)
		}
	}
}
