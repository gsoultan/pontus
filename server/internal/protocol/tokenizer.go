package protocol

import (
	"iter"
	"strings"
	"unicode"
	"unicode/utf8"
)

type TokenType int

const (
	TokenUnknown TokenType = iota
	TokenKeyword
	TokenIdentifier
	TokenLiteral
	TokenOperator
	TokenPunctuation
)

type Token struct {
	Type  TokenType
	Value string
}

// Tokenize returns an iterator over tokens in the SQL query.
//
// Allocation-free for ordinary SQL, which matters because this runs on every
// statement: whatever it allocates, the proxy allocates per query. It used to
// cost 15 allocations for a trivial SELECT and 40 for a JOIN — one copy of the
// whole query as []rune, then a strings.Builder result and a ToUpper per token.
//
// Three things keep it at zero now. Tokens are substrings of q rather than
// copies, because a Go string slice shares its backing array. Keywords resolve
// to interned constants through keywordOf instead of being uppercased. And the
// query is walked as UTF-8 in place rather than widened to runes.
//
// A quoted literal containing an escaped quote is the one case that still
// allocates: its value is not any contiguous piece of the input, so it has to be
// built. See BenchmarkTokenize before changing any of this.
func Tokenize(q string) iter.Seq[Token] {
	return func(yield func(Token) bool) {
		for i := 0; i < len(q); {
			r, size := utf8.DecodeRuneInString(q[i:])

			switch {
			case unicode.IsSpace(r):
				i += size

			case unicode.IsLetter(r) || r == '_':
				start := i
				i += size
				for i < len(q) {
					next, w := utf8.DecodeRuneInString(q[i:])
					if !unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_' && next != '.' {
						break
					}
					i += w
				}

				word := q[start:i]
				if keyword, ok := keywordOf(word); ok {
					if !yield(Token{Type: TokenKeyword, Value: keyword}) {
						return
					}
					continue
				}
				if !yield(Token{Type: TokenIdentifier, Value: word}) {
					return
				}

			case r == '\'':
				var value string
				value, i = readQuoted(q, i)
				if !yield(Token{Type: TokenLiteral, Value: value}) {
					return
				}

			case unicode.IsDigit(r):
				start := i
				i += size
				for i < len(q) {
					next, w := utf8.DecodeRuneInString(q[i:])
					if !unicode.IsDigit(next) && next != '.' {
						break
					}
					i += w
				}
				if !yield(Token{Type: TokenLiteral, Value: q[start:i]}) {
					return
				}

			default:
				if !yield(Token{Type: TokenPunctuation, Value: q[i : i+size]}) {
					return
				}
				i += size
			}
		}
	}
}

// readQuoted reads a single-quoted literal starting at the opening quote and
// returns its value and the index just past the closing quote.
//
// The value excludes the quotes and resolves ” to a single quote. Without an
// escape it is a substring and costs nothing; with one it has to be assembled,
// because the value is then not any contiguous piece of the input.
func readQuoted(q string, open int) (string, int) {
	start := open + 1

	for i := start; i < len(q); i++ {
		if q[i] != '\'' {
			continue
		}
		if i+1 < len(q) && q[i+1] == '\'' {
			return unescapeQuoted(q, start), skipQuoted(q, start)
		}
		return q[start:i], i + 1
	}
	// Unterminated: everything after the opening quote is the value, which is
	// what the caller of a malformed statement gets either way.
	return q[start:], len(q)
}

// skipQuoted returns the index just past the closing quote of a literal whose
// value began at start.
func skipQuoted(q string, start int) int {
	for i := start; i < len(q); i++ {
		if q[i] != '\'' {
			continue
		}
		if i+1 < len(q) && q[i+1] == '\'' {
			i++
			continue
		}
		return i + 1
	}
	return len(q)
}

// unescapeQuoted builds the value of a literal that contains an escaped quote.
func unescapeQuoted(q string, start int) string {
	var sb strings.Builder
	for i := start; i < len(q); i++ {
		if q[i] != '\'' {
			sb.WriteByte(q[i])
			continue
		}
		if i+1 < len(q) && q[i+1] == '\'' {
			sb.WriteByte('\'')
			i++
			continue
		}
		break
	}
	return sb.String()
}

// maxKeywordLen bounds the stack buffer keywordOf folds into. The longest word
// in the table is TRANSACTION, at eleven.
const maxKeywordLen = 16

// keywordOf resolves a word to its canonical uppercase keyword without
// allocating.
//
// The returned string is the constant from the table rather than an uppercased
// copy of the input, so a lowercase `select` costs nothing either. Folding into
// a stack array and indexing the map with it relies on the compiler's
// m[string(bytes)] optimisation, which does not copy.
func keywordOf(word string) (string, bool) {
	if len(word) == 0 || len(word) > maxKeywordLen {
		return "", false
	}

	var folded [maxKeywordLen]byte
	for i := 0; i < len(word); i++ {
		c := word[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		folded[i] = c
	}

	keyword, ok := keywords[string(folded[:len(word)])]
	return keyword, ok
}

// keywords maps each keyword to itself, so a lookup yields an interned constant.
var keywords = func() map[string]string {
	table := make(map[string]string, len(keywordList))
	for _, word := range keywordList {
		table[word] = word
	}
	return table
}()

// keywordList is every word ClassifyQuery switches on.
//
// It is not a style question: a word that is missing arrives as
// TokenIdentifier, the switch case never matches, and the branch is silently
// unreachable. That is how `SELECT 1; DROP TABLE users` stayed classified
// read-only and how `INSERT INTO orders` recorded no affected table.
var keywordList = []string{
	// statements
	"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "MERGE",
	"CREATE", "DROP", "ALTER", "GRANT", "REVOKE", "COMMENT",
	"SHOW", "DESCRIBE", "DESC", "EXPLAIN", "ANALYZE", "VACUUM",
	"CALL", "DO", "COPY", "REFRESH", "REINDEX", "CLUSTER",
	// clauses and structure
	"FROM", "WHERE", "JOIN", "ON", "INTO", "TABLE", "VALUES", "SET",
	"GROUP", "BY", "ORDER", "LIMIT", "OFFSET", "HAVING", "WITH", "AS",
	"RETURNING", "USING", "ONLY",
	// transactions
	"BEGIN", "COMMIT", "ROLLBACK", "START", "TRANSACTION", "SAVEPOINT",
	// locking
	"FOR", "SHARE", "SKIP", "LOCKED", "NOWAIT", "LOCK",
	// operators
	"UNION", "INTERSECT", "EXCEPT", "OR", "AND", "NOT", "IN", "EXISTS",
}

// isKeyword reports whether an already-uppercased word is a keyword.
func isKeyword(s string) bool {
	_, ok := keywords[s]
	return ok
}
