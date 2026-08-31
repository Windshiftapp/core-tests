# GitHub workflow changes

- Keep workflow permissions at the narrowest required level.
- Pin third-party actions to full commit SHAs.
- Use the same `./test` commands documented for contributors.
- Test pull requests against the Core revision in `core.ref`; use Core `main`
  only in the scheduled compatibility workflow.
- Do not place credentials, tokens, repository secrets, or developer-specific
  paths in workflows or fixtures.
- Preserve failure artifact collection for browser runs without uploading
  artifacts from successful runs.
- Validate every workflow file as YAML after editing it.
