# Windshift Core tests

This repository contains the Go, frontend, and browser tests for Windshift
Core. The tests run against a separate Core checkout, so you can change either
repository without copying files between them.

## Getting started

Place this repository beside a Core checkout:

```text
workspace/
├── core/
└── core-tests/
```

You will need:

- the Go version declared in `core/go.mod`;
- Bun 1.4 or later;
- the Node.js version declared in `core/.nvmrc`; and
- Chromium for browser tests. The browser runner installs the matching
  Playwright browser when needed.

From this repository, run:

```bash
./test
```

This runs the policy checks and the fast Go and frontend suites. To use a Core
checkout somewhere else, pass its path before the command:

```bash
./test --core ../my-core-worktree quick
```

CI uses the Core commit recorded in [`core.ref`](core.ref). To reproduce that
exact pairing locally, check out that commit in your Core repository first.

## Running tests

| Command | What it runs |
| --- | --- |
| `./test` | Policy checks and fast Go/frontend tests |
| `./test go` | All Go tests |
| `./test frontend` | All frontend tests |
| `./test browser` | The fast browser suite |
| `./test browser --all` | Every browser spec |
| `./test policy` | Repository and test-quality checks |
| `./test all` | The complete Go, frontend, and browser suite |

You can pass normal Go, Vitest, or Playwright arguments for focused runs:

```bash
./test go -run TestResolveWebAuthnRPID ./internal/config
./test frontend src/lib/utils/dateFormatter.test.js
./test browser tests/items.spec.ts
```

Run `./test help` for the full command reference.

## Where tests belong

- Go package tests mirror Core under `internal/`.
- Black-box HTTP tests live under `tests/`.
- Frontend unit tests live under `frontend/` and use Vitest.
- Browser tests live under `e2e/tests/` and use Playwright.

Tests should be deterministic, isolated, and focused on observable behavior.
Avoid arbitrary sleeps, retries, external credentials, and developer-specific
data. Browser tests should use stable test IDs or element IDs and should assert
the result visible to the user.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the contribution workflow and
[the test policy](docs/test-selection-policy.md) for the suite's quality rules.

## License

The tests are available under the custom
[Windshift Test Suite License 1.0](LICENSE). It permits testing and contributing
to Windshift Core and AGPL implementations, but does not permit using the tests
with AI systems to create or validate a non-AGPL reimplementation. Because of
that restriction, this is a source-available license rather than an
OSI-approved open-source license.
