package protocol

import "testing"

// The tokenizer and classifier run on every statement, so an allocation here is
// an allocation per query. AGENTS.md vetoes adding one without a benchmark;
// until now there was no benchmark to hold anything to.
//
// Run a comparison with:
//
//	go test ./server/internal/protocol/ -run '^$' -bench . -benchmem -count=10 > new.txt
//	benchstat old.txt new.txt

var benchQueries = map[string]string{
	"select":  "SELECT id, name FROM users WHERE id = 42",
	"join":    "SELECT u.id, o.total FROM users u JOIN orders o ON o.user_id = u.id WHERE u.active AND o.total > 100 ORDER BY o.created_at DESC LIMIT 50",
	"insert":  "INSERT INTO events (kind, payload, created_at) VALUES ('click', '{\"a\":1}', now())",
	"comment": "/* app: checkout */ SELECT total FROM carts WHERE id = $1 -- trailing",
}

// tokenSink keeps the compiler from eliminating the loop.
var tokenSink int

func BenchmarkTokenize(b *testing.B) {
	for name, query := range benchQueries {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				n := 0
				for range Tokenize(query) {
					n++
				}
				tokenSink = n
			}
		})
	}
}

var classifySink QueryInfo

func BenchmarkClassifyQuery(b *testing.B) {
	handler := NewPostgresHandler()

	for name, query := range benchQueries {
		// A simple-query message: the tag, a length, and the statement.
		message := appendTagged(nil, 'Q', append([]byte(query), 0))

		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				classifySink = handler.ClassifyQuery(message)
			}
		})
	}
}
