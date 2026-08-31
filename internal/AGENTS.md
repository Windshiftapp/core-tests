# Go package tests

These tests mirror Core packages under `internal/`. Shared Go-only test support
also belongs here, primarily under `internal/testutils/`.

- Match the production package path and use the package name that gives the
  test the narrowest necessary access.
- Add `//go:build test` to new white-box tests that rely on test-only production
  hooks.
- Prefer public behavior. Use white-box access only for an invariant that
  cannot be observed reliably through the package API.
- Use table-driven subtests when several inputs exercise one contract. Keep
  unrelated scenarios in separate tests.
- Compare complete meaningful values. Assert collection order when it is part
  of the contract and error categories plus useful context.
- Mock external or nondeterministic boundaries, not the unit's core behavior.
  Prefer cheap real in-process collaborators.
- Database-backed tests should use the production schema through
  `testutils.CreateTestDB`. Direct SQL schemas are limited to repository or
  database-layer invariants that cannot be created through production setup.
- Exercise SQLite and PostgreSQL when behavior can differ by engine.
- Use `t.Cleanup`, `t.Setenv`, contexts, channels, barriers, and injected clocks.
  Use `t.Parallel()` only when all state and dependencies are isolated.

Run a focused test from the repository root:

```bash
./test go -run TestName ./internal/package
```
