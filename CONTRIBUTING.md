# Contributing

Thank you for helping improve the Windshift Core test suite.

## Before you start

Keep the Core and test repositories beside each other, or pass the Core path
with `./test --core PATH`. Run `./test` once to verify your local setup.

If a test needs a production selector, interface, or dependency-injection
point, make that change in the Core repository. Keep production code out of
this repository. Do not update `core.ref` as an incidental part of a test
change; it is the revision used by CI and should move deliberately.

## Choose the smallest useful test

Start at the narrowest layer that proves the behavior:

- Use a Go test for backend logic, handlers, persistence, and HTTP contracts.
- Use a frontend test for rendered behavior and user interaction that does not
  require a browser process.
- Use a Playwright test for browser integration and complete user workflows.

Avoid repeating the same assertion at several layers unless each test protects
a different risk.

## Write a durable test

- Test observable results rather than implementation details.
- Give the test a name that describes the scenario and expected outcome.
- Create all state needed by the test and clean it up afterward.
- Do not depend on test order, existing data, wall-clock timing, or retries.
- Cover denial and ownership cases for security-sensitive behavior.
- In Playwright, use `getByTestId(...)` or a stable `#id` and wait for the
  specific state or event that proves the page is ready.

Browser setup and cleanup may use the HTTP API. The assertion itself should be
made through the browser-visible workflow.

## Run your change

Run the focused test while working:

```bash
./test go -run TestName ./internal/package
./test frontend src/lib/path/example.test.js
./test browser tests/example.spec.ts
```

Before opening a pull request, run:

```bash
./test
```

If your change affects browser behavior, also run the relevant browser spec.
Use `./test all` when the change has broad cross-layer impact.

New fast-lane tests must be added to [`suite-manifest.json`](suite-manifest.json).
Tests that are useful but expensive can remain in the complete suite.

Before submitting, also check that you have not added dependency directories,
coverage, browser reports, databases, logs, authentication state, or real
credentials. In the pull request, list the exact test commands you ran and any
relevant suite you did not run.
