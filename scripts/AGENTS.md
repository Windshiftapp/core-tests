# Test runner maintenance

The contributor-facing interface is `./test`. Files in this directory and the
low-level root runners are internal implementation details.

- Preserve the commands and examples documented by `./test help` and
  `README.md`, or update them together in the same change.
- Keep fast-suite membership sourced from `suite-manifest.json`; do not add a
  second hard-coded list.
- Keep runners compatible with the Bash version shipped by macOS. Avoid Bash 4
  features such as associative arrays and `mapfile`.
- Create isolated temporary directories, resolve the Core path explicitly, and
  clean up every generated binary, database, process, and report on exit.
- Never mutate the source Core checkout as part of an overlay run.
- Preserve the committed npm lockfile and use Bun to launch Vitest and
  Playwright.
- Validate shell changes with `bash -n`, run `./test policy`, and exercise the
  affected public command through `./test`.
