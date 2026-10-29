# E2E test screenshots

Per-spec PNGs documenting what each Playwright test does. Each subdirectory
matches a spec file under `e2e/tests/` (without the `.spec.ts` suffix);
each PNG inside is either a per-step shot or an end-of-test shot.

## How they are produced

Two complementary layers, both gated on `E2E_SCREENSHOTS=1`:

1. **Auto end-of-test capture** — Playwright's built-in `screenshot: 'on'`
   takes one PNG when each test finishes; `helpers/screenshot-reporter.ts`
   copies it into `screenshots/<spec>/<test-title>.png`. Every test in
   every spec gets at least one screenshot, no instrumentation required.
2. **Per-step capture inside specs** — call the shared helper at any
   meaningful checkpoint:

   ```ts
   import { shot } from '../helpers/screenshot';

   await page.goto('/portal/foo');
   await shot(page, '01-after-load');
   ```

   Writes to `screenshots/<spec>/<step-name>.png`. Numeric prefixes
   (`01-`, `02-`, …) keep the directory listing in flow order.

The helper waits for any open modal (`[role="dialog"][aria-modal="true"]`)
to reach `opacity: 1` before snapping, so screenshots aren't taken
mid-fade.

## Regenerating

Screenshots are off by default so CI runs aren't slowed down. To capture
them locally:

```sh
cd core-tests
E2E_SCREENSHOTS=1 ./run-e2e.sh
```

Run a single spec:

```sh
E2E_SCREENSHOTS=1 ./run-e2e.sh e2e/tests/portal-passkey.spec.ts
```

Override the output directory if you want them somewhere else:

```sh
E2E_SCREENSHOTS=1 E2E_SCREENSHOTS_DIR=/tmp/foo ./run-e2e.sh ...
```

## Git policy

Screenshots are gitignored — they regenerate on every run and would churn
the diff. Commit them deliberately when you want to attach a flow to a PR
description or pin a baseline image.
