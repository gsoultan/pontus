# Pontus Security Invariants

Pontus sits between untrusted SQL clients and production databases, and its dashboard
renders traffic captured from those clients. Two trust boundaries, both hostile.

## Boundary 1 — the wire (`proxy_addr`)

Everything arriving here is attacker-controlled: the SQL text, the startup parameters, the
username, the length prefixes.

- **WAF 2.0** (`middleware/firewall.go`) analyses the *token stream*, not the raw string:
  tautologies (`OR 1=1`, `'a'='a'`), `UNION SELECT`, `DELETE` with no `WHERE`, and reads of
  `pg_shadow` / `pg_authid` / `mysql.user`. Structural analysis is the right approach — keep
  new rules on the token stream, never on `strings.Contains`.
- **WAF 3.0** rewrites queries for dynamic data masking via `Handler.RewriteQuery`, then
  re-normalizes and re-classifies. A rewrite that skips re-classification is a bypass.
- **Rate limiting** is global + per-tenant, priced by query cost from the token stream.
- **Exfiltration guard** — `fwConfig.MaxResponseSizeMB` bounds a response in `proxyResponse`.
- **TLS** — `server/proxy/tls.go` *can* build a client-facing config, but nothing wires one:
  only `cfg.BackendTLS` is used, the proxy listener is a plain `net.Listen`, and there is no
  Postgres `SSLRequest` handling at all. **Client traffic is plaintext today and a default
  `sslmode=prefer` client hangs.** `InsecureSkipVerify` is config-exposed, so it must stay off
  by default and be documented as a development-only knob. See `mem:findings` A3, A4.

Non-negotiables: a fast path may *cheapen* a check but never *skip* it; a length prefix from
a client is bounded before it is trusted; anything that reuses a response across sessions
(cache, in-flight collapser, shadow) must be namespaced by identity.

## Boundary 2 — the management plane (`mgmt_addr`)

Serves the ConnectRPC API, `/metrics`, and the dashboard on one mux.

- `middleware/auth.go` — `/Login` open; static `admin_token` accepted; otherwise HS256 JWT
  with a role claim. Non-admins are limited to a read-only allowlist.
- **Every new RPC is admin-only unless it is deliberately added to the allowlist.**
- Passwords are bcrypt (`pkg/auth`), default cost. Login compares hash, never plaintext.
- The dashboard renders captured SQL and log lines — always escaped, never as HTML or
  markdown. That is stored XSS otherwise, sourced from the very clients Pontus exists to
  defend against.
- No permission may be enforced only in the UI; no token in `localStorage`.

## Boundary 3 — the agent (`agent_addr`)

`agent_token` is mandatory (`pool.NewServer` errors without an agent address). The agent can
manage database services and install packages, so an agent RPC must never build a shell
command from a request field, and must never report healthy on an error path.

## Current violations

Twelve security defects are open as of the 2026-08-06 review (`mem:findings` B1–B12). The two
that matter most, because neither is guessable from the design:

1. **The WAF can be bypassed with one unbalanced quote.** The firewall inspects the *normalized*
   query while the backend receives the *raw* bytes, and normalization silently drops everything
   after an unterminated `'`. Any check that reads a different string than the one that gets
   executed is a bypass by construction.
2. **Blocked queries are still recorded and later replayed.** `TrackSessionState` and
   `TrackPreparedStatement` run *before* the middleware chain, so WAF-rejected SQL is stored in
   the session and written verbatim to a fresh backend on the next switch, unchecked.

Then: hardcoded JWT secret fallback, auth that no-ops when `admin_token` is empty, a default
`admin`/`admin123` credential, `jwt.Parse` without `WithValidMethods`, wildcard CORS, and an
unauthenticated SSRF primitive in `ValidateBackend`.

Rules going forward: **no secret gets a default value** — fail closed at startup. **Inspect the
exact bytes you execute**, never a lossy derivative. **Record session state after the chain
admits the query, never before.**
