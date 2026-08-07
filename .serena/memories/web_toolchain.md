# Web toolchain (dashboard)

Current pins: **TypeScript 7.0.2 · Vite 8.2.1 · Mantine 9.5.1 · React 19.2.8 ·
TanStack Router/Query/Form · oxlint 1.77**.

## The linter is oxlint, not ESLint — and that is forced, not preference

`typescript-eslint` **cannot run under TypeScript 7**. It hard-fails at import time:

```
typescript-eslint does not support TS 7.0.
```

No published channel fixes this (`latest` 8.66.0, `canary` 8.66.1-alpha.8, `rc-v8`
all refuse). Upstream tracking is typescript-eslint#10940, targeting TS >= 7.1.

The documented workaround — run typescript-eslint against the TS 6 API side by
side — **is not available to us**: it needs npm-style *nested* overrides, and
`bun install` prints `warn: Bun currently does not support nested "overrides"`
and ignores them. Verified empirically, not assumed.

ESLint also cannot parse `.tsx` at all without the typescript-eslint parser, so
"keep ESLint minus typescript-eslint" is not an option either.

**Therefore:** the ESLint stack (`eslint`, `@eslint/js`, `typescript-eslint`,
`eslint-plugin-react-hooks`, `eslint-plugin-react-refresh`, `globals`) was
removed and `eslint.config.js` deleted. `bun run lint` runs `oxlint`, configured
in `.oxlintrc.json`. oxlint parses TS natively in Rust, so the TS version is
irrelevant to it, and it carries the react-hooks rules we relied on.

Do not "restore ESLint" without first checking that typescript-eslint supports
the pinned TS major — otherwise lint breaks on the next install.

`react/react-in-jsx-scope` is off (modern JSX transform) and the `react-perf`
plugin is off (it flags every inline prop object in Mantine-style code).

## Build order matters

`package.json` runs `tsc -b && vite build` — typecheck **before** bundling. It
used to be the reverse, which is how a `web/dist` got produced from a tree that
did not typecheck. See `mem:build_and_release` for why `web/dist` must exist
before any Go build (`web/ui.go` does `//go:embed all:dist`).

## Dev proxy prefix

`vite.config.ts` proxies `/api.proto.service.ManagementService`. The `.service`
segment is load-bearing — see `mem:control_plane`. It was missing and every
dev-mode RPC fell through to the SPA handler.
