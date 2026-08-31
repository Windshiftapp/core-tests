# Playwright tests

Browser tests must prove a browser-visible Core workflow. API calls are for
setup and cleanup, not a substitute for UI assertions.

- Put specs under `e2e/tests/`, reusable setup under `e2e/fixtures/` or
  `e2e/helpers/`, and repeated UI interactions in a focused page object.
- Create application state through production HTTP APIs and use unique test
  data. Each test must run alone and in arbitrary suite order.
- Select interactive elements with `getByTestId(...)` or a stable exact `#id`.
  Do not select by visible text, translated labels, role names, CSS classes, or
  DOM structure. Add a stable selector in Core when one is missing.
- Use Playwright's web-first locators and `expect` assertions. Wait for the
  specific UI state, request, response, URL, or event that proves readiness.
- Do not add `waitForTimeout`, arbitrary sleeps, retries, `test.only`, or serial
  suites. Fix synchronization and isolation instead.
- Keep the decisive user action and assertions visible in the spec. Helpers
  should not hide the contract under test.
- For mutations, verify resulting values and reload or revisit when persistence
  is part of the contract.
- Preserve traces, reports, screenshots, server logs, and error context on
  failure. Screenshots are diagnostic artifacts, not primary assertions.

Run a focused spec from the repository root:

```bash
./test browser tests/feature.spec.ts
```

In a restricted macOS environment, launch the browser outside the sandbox on
the first attempt; Chromium cannot register its required Mach ports inside the
sandbox.
