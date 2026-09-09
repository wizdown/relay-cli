# Changelog

Changes that matter to a user of the binary. Each release also lists its
commits on the GitHub release page.

## Unreleased

## v0.4.2

- An agent paused in Relay is a worker state, not a probe failure. The worker
  shows `owner paused`, launches nothing, and keeps polling, so resuming the
  agent in Relay brings it back with nothing to do on this machine. It used to
  count toward the probe breaker, which after ten polls wrote a local `PAUSED`
  file that outlived the pause.
- `relay check` reports a paused agent as `paused` and passes. It used to print
  `FAIL` and name the credential.
- The probe breaker names a removed agent when Relay says the agent is gone,
  instead of pointing at the endpoint and the credential.

## v0.4.1

- Claude workers pre-allow relay's tools by server prefix (`mcp__relay`)
  instead of a named list. Relay renamed six of its agent verbs; every name a
  list missed was denied mid-run, and the denial reached the operator only in
  the run's `permission_denials`. Upgrade before pointing a Claude worker at a
  Relay serving the 18-verb agent surface.
- The harness rules name the tools Relay serves today: `open_task` to open or
  re-read a task, and `hand_back` with `outcome: release` to give unfinished
  work back.

## v0.4.0

- `relay run --help`, `relay check --help` and `relay init --help` print that
  command's usage on stdout and exit `0`. `relay help <command>` prints the
  same. `relay -h` and `relay --help` both print the one-screen summary.
- A wrong flag or a stray argument is one line naming the fix, and exits `2`:
  `"relay run" does not take --bogus`, `--port takes a number, not "abc"`.
  `relay version` and `relay help` refuse arguments they do not take instead
  of ignoring them. Exit statuses are documented in `docs/cli.md`.
- `relay check` probes every credential before it reports a missing or
  signed-out CLI, and reports both, so a bad `relay_mcp` is no longer hidden
  behind a CLI that needs `claude auth login`. Its banner still names each
  CLI it found.
- Error messages for a config that needs fixes, an existing config at
  `relay init`, a missing CLI and a signed-out CLI are shorter: what is wrong
  and what to type, without the reasoning.
- The help uses plain ASCII rulers, so it reads the same pasted into a bug
  report.
- `docs/cli.md` documents the two dashboard routes, `/api/snapshot` and
  `/api/stream`.
- A worker's poll rate now adapts. It polls every `poll_seconds` while it has
  work and for five minutes after its last task; each quiet poll after that
  doubles the wait, up to the new fleet-wide `idle_poll_seconds`. Work puts it
  straight back on `poll_seconds`. An idle worker makes about a third of the
  requests it used to, and picks up a task up to two minutes later.
- `idle_poll_seconds` defaults to `120`, and is at least twice `poll_seconds`
  and at most `3600`. Because the wait doubles, anything under twice the fast
  rate is a slowdown the backoff cannot take a single step of, and is rejected
  with the minimum for the `poll_seconds` you set.
- Every change of poll rate is one line, in `worker.log` and in the dashboard
  feed: `nothing to act on for 5m0s — slowing to one poll every 60s`, and
  `work again — back to one poll every 30s` when a task arrives. The worker
  card names the wait a backed-off worker has cooled to, beside its countdown,
  and the effective config shows both rates.

## v0.3.2

- A worker card no longer says `withheld` when nothing is being withheld. The
  label followed `at_limit` alone, so an agent at its parallel-claim limit with
  an empty backlog was tagged as holding work back. It now shows the count —
  `2 withheld` — and only when there is something to show.

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
