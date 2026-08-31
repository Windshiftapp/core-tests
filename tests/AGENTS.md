# Black-box HTTP tests

Tests in this directory exercise a running Core server through its public HTTP
surface.

- Create application state through production HTTP APIs using the shared
  helpers in `helpers.go`, `fixtures.go`, and `matrix_*.go`.
- Do not use direct database writes for ordinary setup. They bypass
  authorization, defaults, validation, history, cache invalidation, and
  generated fields.
- Use unique data and make every test independent of execution order.
- Assert exact status codes, response bodies, persisted outcomes, and relevant
  side effects. A no-error assertion alone is not sufficient.
- Permission-sensitive routes need explicit allowed and denied cases. Keep
  `MatrixRoutes` or a documented `RouteClassificationExemption` in sync with
  route coverage.
- Direct SQL is reserved for narrowly scoped repository, corruption, or
  malformed-data behavior the production API intentionally cannot create.
  Explain that exception beside the fixture.

Run a focused test from the repository root:

```bash
./test go -run TestName ./tests
```
