# Junie Guidelines

All the code generated must follow all the points of these guidelines.

## Code Guidelines
- Must Clean Code and SOLID Principle
- Must Programming by Interface and Must use design pattern 
- One interface one file, Rule of Thumb 7 methods
- Interface Segregation Principle (ISP). Clients should not be forced to depend on methods they do not use. If an interface represents different task areas (e.g., UserLoginAndAdministration), split it into smaller, specific interfaces (e.g., UserLogin, UserAdministration).
- Cohesion: All methods in an interface should be closely related and serve a single, clear purpose.
- One struct one file
- Keep the struct method small. if the logic in one method too big, then composite the logic into small function. separate all the reusable function into the internal or pkg folder.
- No nested if
- Avoid unnecessary IF-THEN statements in loops, you can use strategy pattern, pre filtering, etc.
- Must be Object Oriented
- Must use Modern Go 1.26 syntax
- **Avoid stuttering** — filenames must not have a suffix that matches their parent folder name (e.g., use `backend.go` instead of `backend_service.go` in a `service/` folder). Similarly, symbol names (structs, interfaces) must not repeat the package name (e.g., use `service.Backend` instead of `service.BackendService`).
- **Reliability** — ensure no error, no panic, and no unexpected behavior in production. Avoid panic code and ensure error handling is robust (no raw error codes).
- **Security** — ensure no exploit code, no vulnerable code, and no zero-day vulnerabilities code follow secure coding practices.

## Third party library
- No vulnerabilities 3rd party library and have high performance

## Architecture
- Must use GRPC for internal management

## Performance & Lightweight
- **No memory leaks** — always `Close()` connections and response bodies; `Cancel()` contexts when done; avoid goroutine leaks by using bounded workers or select-with-context; prioritize **low memory code**.
- **Modern Idioms (Go 1.26)** — use `new(val)`, `for i := range n`, `strings.SplitSeq`, iterators (`maps.Keys/Values`, `slices.Collect/Sorted`), `slices` package helpers, `errors.Is/AsType/Join`, `wg.Go(fn)`, `t.Context()`, `b.Loop()`, and `omitzero` JSON tags
- **High performance** — avoid allocations in hot paths; reuse buffers; prefer `sync.Pool` for frequently allocated objects; use `strings.Builder` for string concatenation; profile before optimizing
- **Lightweight** — keep dependencies minimal; prefer stdlib; avoid heavy reflection or codegen where simple code suffices
- **Protobuf Integration** — follow the [Protobuf Best Practices](proto-guidelines.mdc) for all `.proto` files and generated code. Must use `buf` to generate protobuf.
- **PgBouncer Integration** — follow the [PgBouncer Best Practices](pgbouncer-guidelines.mdc) for all database connection pooling and configurations. Use transaction mode for high performance.

## Testing
- Mock interfaces, not concrete types — enables easy unit testing
- Prefer table-driven tests

# React Frontend Guidelines

All frontend code must follow React best practices and established design patterns.

## Tech Stack
- **React 19.2**
- **Mantine v9.1.0**
- **Vite 8**
- **Bun** (Package Manager)
- **Latest TypeScript**

## Core Principles
- **Functional components only** — no class components
- **Hooks for logic** — extract reusable logic into custom hooks (useRouteStatsHistory, etc.)
- **Composition over inheritance** — prefer composition and small, focused components

## Design Patterns
1. **Container/Presentational (Smart/Dumb)** — separate data-fetching containers from presentational components
2. **Custom hooks** — encapsulate stateful logic (API calls, subscriptions, form handling)
3. **Controlled components** — for forms; use single source of truth
4. **Compound components** — when components work together (e.g., RouteForm + RoutingConfig)

## Folder & File Organization
- **Organize by domain** — group by feature/business domain, not by technical type
- Prefer `routes/`, `services/`, `entrypoints/` (domain) over flat `components/`, `hooks/` (type)
- Colocate domain logic: components, hooks, types, and utils for a domain in the same folder

## Structure
```tsx
// ✅ GOOD — focused component, props interface
interface StatusCardProps {
  label: string;
  value: string | number;
}

export function StatusCard({ label, value }: StatusCardProps) {
  return (/* ... */);
}
```

## Components Best Practices
- **One component per file** — one main export per file; name the file after the component (e.g. `StatusCard.tsx`, `RouteForm.tsx`)
- **Avoid God components** — keep components small and focused; if a component handles layout, data, and many sub-features, split it into smaller components
- **Avoid God pages** — pages should orchestrate, not implement; delegate UI and logic to child components and hooks; keep page files thin

## State & Data Fetching
- Use **ConnectRPC** to connect to backend services
- Use **TanStack Query** for server state; Zustand for client-only state
- Keep server and client state separate; avoid duplicating server data in local state
- Use `useQuery` / `useMutation` with proper keys and invalidation

## Routing
- use **Tanstack Router**

## Performance
- **Optimization (Critical)** — parallelize independent operations (`Promise.all()`); defer `await` to point of use; monitor bundle size and use dynamic imports/manual chunks
- **Re-render** — use derived state during render (no `useEffect` sync); defer state reads to event handlers; extract non-primitive defaults to constants outside components
- **Memoization** — use `useMemo`, `useCallback` when passed to children; use strict dependency arrays
- **Lazy Loading** — default to `React.lazy`/`Suspense` for routes and heavy components
- **Virtualization** — virtualize long lists; use stable `key` props (not indices)
- **Reliability** — ensure no UI error, no crashes, handle all edge cases gracefully, and ensure no vulnerable code.

## Hooks Best Practices
- **One hook per file** — one custom hook per file; name the file after the hook (e.g. `useRoutes.ts`, `useGateonStatus.ts`)
- **Avoid God hooks** — keep hooks small and focused; if a hook does many unrelated things, split it or compose smaller hooks
- **Extract reusable logic** — create a custom hook when logic is used in 2+ components or when it combines state + effects
- **One responsibility** — keep each hook focused; compose smaller hooks (e.g. `useLimitStatsHistory` wraps `useLimitStats`)
- **`use` prefix and camelCase** — `useRoutes`, `useGateonStatus`, `useAuth`
- **Colocate by domain** — place domain-specific hooks in the same folder as the feature (e.g. `routes/useRouteStats.ts`); use a shared `hooks/` folder only for cross-cutting concerns (e.g. `useGateon`, `useAuth`)
- **TanStack Query in hooks** — encapsulate `useQuery`/`useMutation` in hooks; use stable `queryKey` arrays (include params); set `enabled: false` for conditional fetching
- **Return stable shapes** — return objects or arrays; avoid changing return shape between renders; destructure what callers need
- **Cleanup in `useEffect`** — clear timers, subscriptions, and listeners in the effect cleanup
- **No conditional hook calls** — always call hooks at the top level; use `enabled` or early returns inside the hook, not before calling it

## Naming Conventions

| Kind | Convention | Example |
|------|------------|---------|
| Components | PascalCase | `StatusCard`, `RouteForm` |
| Hooks | `use` prefix, camelCase | `useRouteStatsHistory`, `useAuth` |
| Props interfaces | `ComponentNameProps` | `StatusCardProps` |
| Event handlers | `handle` or `on` prefix | `handleSubmit`, `onClick` |
| Files | PascalCase for components | `StatusCard.tsx`, `RouteForm.tsx` |
| Constants | SCREAMING_SNAKE or PascalCase | `API_BASE`, `DefaultTimeout` |
| Types/interfaces | PascalCase | `Route`, `ServiceConfig` |
