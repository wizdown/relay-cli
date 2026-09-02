# Changelog

Changes that matter to a user of the binary. Each release also lists its
commits on the GitHub release page.

## Unreleased

- `codex` is a second supported runtime, with `model`, `reasoning_effort`,
  `sandbox`, `network_access` and `web_search` in its `runtime_config`.
- The documentation was rewritten to be shorter. No behaviour changed.

## v0.1.1

- Every documentation link the binary prints is a full URL that opens without
  a checkout.
- `relay init` writes a shorter starting config that links the reference
  instead of repeating it, points its header at `relay check`, and gives each
  worker a name it can keep.
- First-run gaps closed: placeholders are rejected by name, and a missing CLI
  is reported with what to install.

## v0.1.0

- First release: `init`, `check`, `run`, the read-only dashboard, the `claude`
  runtime, and bounded defaults.
