# Changelog

Changes that matter to a user of the binary. Each release also lists its
commits on the GitHub release page.

## Unreleased

## v0.3.1

- A worker no longer launches a session when Relay is withholding its claimable
  work. Relay reports how much it is holding for an agent at its parallel-claim
  limit while offering none of it; a worker used to launch against that count
  and the session had nothing to take. Such a worker now shows `at limit` with
  the number withheld, and still launches for a task needing its attention.
- `relay check` marks a withheld queue `at limit, none claimable`, and the
  dashboard labels those polls rather than counting them as work waiting.

## v0.3.0

- The dashboard has two new views in its sidebar. **Fleet board** gives each
  worker a row with the task it claimed, the tool call it is in, and its spend,
  tokens and elapsed time against the caps that bound them. **Spend ledger**
  totals the last hour by worker and by task: cost per run, turns, tool calls,
  cache share, the outcome mix, and spend per five minutes.
- A run now carries the task id its agent was seen claiming, and a worker at
  its hourly ceiling shows when the next slot frees. Both are visible in
  `/api/snapshot` as `task_id` and `ceiling_resets_at`.

## v0.2.1

- `relay run --keep-awake` holds off macOS system sleep for as long as the
  fleet runs, while the Mac is on AC power. Closing the lid still sleeps it.
  On a machine that cannot hold the assertion the flag warns and the fleet
  runs.
- The documentation and `relay help` were rewritten to be shorter, and tests
  now hold the prose to the code. No behaviour changed.

## v0.2.0

- `codex` is a second supported runtime, with `model`, `reasoning_effort`,
  `sandbox`, `network_access` and `web_search` in its `runtime_config`.

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
