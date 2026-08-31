# Working in Windshift Core tests

## Start here

Read `README.md`, `CONTRIBUTING.md`, `LICENSE`, and the nearest nested
`AGENTS.md` before making changes. More specific instructions under
`internal/`, `tests/`, `frontend/`, `e2e/`, `scripts/`, `.github/`, and `docs/`
supplement this file.

This repository is not a standalone Go module. Tests run against a separate
Windshift Core checkout, normally at `../core`. Standalone language-server
errors caused by the missing production source are expected.

## Scope and licensing

- Keep tests focused on stable, observable behavior in Windshift Core.
- Production code, selectors, interfaces, and dependency-injection points
  belong in the Core repository. Do not copy production source into this one.
- Use AI tools with these Test Materials only for purposes permitted by
  `LICENSE`. Never use them to create or validate a non-AGPL Core
  implementation.
- Do not change `LICENSE` or describe this repository as OSI-approved open
  source without explicit maintainer approval.

## Use the public runner

Use `./test`; the other runner scripts are implementation details.

```bash
./test                                      # policy + fast Go/frontend tests
./test go -run TestName ./internal/package  # focused Go test
./test frontend src/lib/path/file.test.js   # focused frontend test
./test browser tests/feature.spec.ts        # focused browser spec
./test policy                               # repository quality checks
```

Pass `--core PATH` before the command when Core is not at `../core`. Use Bun
1.4 or later for JavaScript, Vitest, Playwright, and one-off scripts. Preserve
committed lockfiles and do not migrate package managers as a side effect.

## Test quality

- Test observable contracts: exact outputs, status codes, errors, persisted
  state, and relevant side effects.
- Use the narrowest layer that covers the risk. Do not repeat the same
  assertion across layers without a distinct purpose.
- Cover meaningful success, boundary, failure, permission, ownership, and
  cross-workspace cases. Security-sensitive changes need an explicit denial
  case.
- Keep tests deterministic and isolated. Do not depend on order, developer
  data, wall-clock timing, unrecorded randomness, retries, or state left by
  another test.
- Use explicit synchronization, fake time, channels, events, or request waits.
  Do not add arbitrary sleeps or larger timeouts to hide a race.
- Register cleanup for every resource and global mutation. Restore environment
  variables, mocks, timers, and DOM state with the matching cleanup mechanism.
- Prefer existing helpers. Add a shared helper only when it removes real
  repetition without hiding the behavior under test.
- Write brief, direct comments. Rework nearby comments when necessary instead
  of layering new explanations onto stale ones.

## Repository hygiene

- Do not commit dependency directories, coverage, browser reports, databases,
  logs, authentication state, or other generated artifacts.
- Treat `core.ref` as an intentional compatibility pin. Update it only when the
  task includes moving the tested Core revision and the fast suite passes
  against that revision.
- Preserve unrelated worktree changes.
- Do not add AI-assistant attribution to commits or pull requests.

## Before completion

Run the narrowest affected test while iterating, then `./test policy` and the
complete affected layer. Run `./test` for changes to shared runners, manifests,
or fixtures. Report the exact commands run and any relevant suite not run.
