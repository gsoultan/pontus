# Always-Optimize Loop (Pontus)

Binding on every task regardless of size. Authoritative copy lives in `CLAUDE.md`; this is
the memory-resident summary. Reading the codebase blindly, or re-deriving context already
written down, is a process violation.

## Discover — before opening any file

| Source | Answers | How |
| :--- | :--- | :--- |
| graphify | *Where is this? What does it touch?* | `rtk graphify query "<term>"` · `graphify explain "<node>"` · `graphify path "<A>" "<B>"` |
| Serena | *What did we already decide?* | `mem:core`, then only the referenced memories |
| Obsidian | *What happened before?* | `~/Documents/ObsidianVault/Pontus` |
| skills.sh | *Have I solved this shape before?* | `rtk npx skills list` → load matches; `rtk npx skills add <skill>` |

If `graphify-out/graph.json` exists, a question about the codebase is a graphify query
**first**. Prefer an existing memory or vault note over re-reading source.

## Work — token ladder, always on

| Level | Purpose | Tool |
| :--- | :--- | :--- |
| 0 | Locate (always first) | `rtk graphify query "<term>"` |
| 1 | Symbols & dependencies | `rtk smart <file>` |
| 2 | Filtered content | `rtk read -l aggr <file>` |
| 3 | Compressed content | `sqz_read_file` / `sqz compress <file>` |
| 4 | Full read (last resort) | `Read` / `rtk read <file>` |

Prefix supported shell commands with `rtk`. Prefer `sqz_read_file`/`sqz_grep`/`sqz_list_dir`
over `Read`/`Grep`/`ls` above ~2 KB or on any second read. Budget a Level 4 read with
`tkn -c -m gpt-4 <file>`; keep single reads under ~2,000 tokens. No raw `cat`/`ls`/`grep`/`find`.

## Verify

`buf generate` → `bun run --cwd web build` (before any Go step — `//go:embed all:dist`) →
`gofmt -l .` → `go vet ./...` → `go test -race ./...` → `bun run --cwd web lint` →
`go build ./cmd/...`, plus the **Proof** column in `AGENTS.md` for the surface touched.
Details: `mem:build_and_release`.

## Persist — the step most easily skipped, most expensive to lose

- `rtk graphify update .`
- `rtk graphify export obsidian --dir ~/Documents/ObsidianVault/Pontus`
- Write durable decisions here as memories, one topic per file, linked from `mem:core`.
  Still true next month → memory. Only mattered this session → not.
- Add a skill rather than repeating a manual workaround a third time.
- `rtk gain` / `sqz gain` after exploration-heavy tasks.
