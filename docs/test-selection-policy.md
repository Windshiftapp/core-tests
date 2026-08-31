# Test selection policy

The suite is a compatibility contract for Windshift Core. Go, Vitest, and
Playwright tests follow the same durability standard.

## Scope

A test belongs in this repository when all of these are true:

- It protects behavior available in the AGPL Core product.
- It asserts an observable, intentionally supported contract.
- It uses only synthetic local data and production setup paths.
- It has no dependency on live third-party services or real credentials.

Useful Core coverage belongs in the complete suite even when it is too
expensive for every pull request. Go sleeps and frontend real timers fail the
policy check. Reviewed Playwright selector exceptions are recorded in
`e2e/stable-selector-exceptions.json` and may not grow unnoticed.

## Fast-lane promotion

A test is promoted to the fast pull-request lane only when all of these are
also true:

- It is isolated and passes alone and in arbitrary suite order.
- It passes on the first attempt; retries and pass-on-retry are failures.
- It synchronizes on state, requests, events, or fake time rather than sleeps.
- It uses stable test IDs or element IDs without a selector-policy exception.
- Its maintenance value is higher than its coupling to frequently changing UI
  structure or implementation details.

Before admission to the fast lane, run the focused test three consecutive
times against the pinned revision in `core.ref`, then run `./test`. Add the test
to `suite-manifest.json` and explain the contract it protects in the pull
request.

## CI contract

Pull requests run policy checks and fast Go, frontend, and Playwright lanes
against the pinned Core revision. Scheduled compatibility runs execute all Go,
frontend, and Playwright tests against Core `main`. Complete-suite failures are
drift and stabilization work, not a reason to delete useful tests or make every
pull request unreliable. Update `core.ref` deliberately after the fast suite
passes.
