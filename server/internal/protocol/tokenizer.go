package protocol

import (
	"iter"
	"strings"
	"unicode"
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
func Tokenize(q string) iter.Seq[Token] {
	return func(yield func(Token) bool) {
		var sb strings.Builder
		runes := []rune(q)
		n := len(runes)

		for i := 0; i < n; i++ {
			r := runes[i]

			if unicode.IsSpace(r) {
				continue
			}

			// Keywords and Identifiers
			if unicode.IsLetter(r) || r == '_' {
				sb.Reset()
				sb.WriteRune(r)
				i++
				for i < n && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_' || runes[i] == '.') {
					sb.WriteRune(runes[i])
					i++
				}
				i--
				val := sb.String()
				upper := strings.ToUpper(val)
				if isKeyword(upper) {
					if !yield(Token{Type: TokenKeyword, Value: upper}) {
						return
					}
				} else {
					if !yield(Token{Type: TokenIdentifier, Value: val}) {
						return
					}
				}
				continue
			}

			// Literals (Strings)
			if r == '\'' {
				sb.Reset()
				i++
				for i < n {
					if runes[i] == '\'' {
						if i+1 < n && runes[i+1] == '\'' { // Escaped quote
							sb.WriteRune('\'')
							i += 2
							continue
						}
						break
					}
					sb.WriteRune(runes[i])
					i++
				}
				if !yield(Token{Type: TokenLiteral, Value: sb.String()}) {
					return
				}
				continue
			}

			// Literals (Numbers)
			if unicode.IsDigit(r) {
				sb.Reset()
				sb.WriteRune(r)
				i++
				for i < n && (unicode.IsDigit(runes[i]) || runes[i] == '.') {
					sb.WriteRune(runes[i])
					i++
				}
				i--
				if !yield(Token{Type: TokenLiteral, Value: sb.String()}) {
					return
				}
				continue
			}

			// Operators and Punctuation
			if !yield(Token{Type: TokenPunctuation, Value: string(r)}) {
				return
			}
		}
	}
}

// isKeyword decides whether a word is emitted as TokenKeyword.
//
// Every word ClassifyQuery switches on has to be in here. It is
// not a style question: a word that is missing arrives as TokenIdentifier, the
// switch case never matches, and the branch is silently unreachable. That is how
// `SELECT 1; DROP TABLE users` stayed classified read-only and how `INSERT INTO
// orders` recorded no affected table.
func isKeyword(s string) bool {
	switch s {
	case
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
		"UNION", "INTERSECT", "EXCEPT", "OR", "AND", "NOT", "IN", "EXISTS":
		return true
	}
	return false
}
