# Frontend tests

Frontend unit tests use Vitest, Testing Library, and the production frontend
from the paired Core checkout.

- Exercise rendered behavior and user interactions, not Svelte component
  internals.
- Await asynchronous UI state explicitly. Assert the visible result and any
  important call or state transition.
- Prefer accessible queries in unit tests. Do not couple assertions to Tailwind
  classes, DOM nesting, or other presentation details.
- Restore mocks, fake timers, globals, and DOM state with Vitest cleanup hooks.
- Use fake timers or injected time instead of real `setTimeout` synchronization.
- Do not add a new frontend dependency unless the task explicitly requires it.
  Preserve the Core frontend lockfile used by the overlay.
- Format and lint changed JavaScript or TypeScript with Core's Biome
  configuration. Do not run Biome on Svelte files.

Run a focused test from the repository root:

```bash
./test frontend src/lib/path/file.test.js
```
