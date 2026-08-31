# Runner internals

Contributors should use [`../test`](../test). The files in this directory are
small helpers used by that command and by CI:

- `check-public-suite.mjs` checks suite boundaries and quality baselines.
- `run-frontend.sh` runs Vitest inside an isolated Core overlay.
- `run-with-core-node.sh` activates the Node.js version required by Core for one
  command.
- `install-mailpit.sh` installs the pinned CI mail server used by browser tests.

These helpers are not separate contributor workflows.
