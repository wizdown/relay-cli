# Troubleshooting

Start with `relay check`. It validates the config and tests every credential
without launching anything, so it separates "misconfigured" from "working, but
idle" for free.

Then the worker's own log — `~/.relay/state/<name>/worker.log` while it runs,
archived to `~/.relay/logs/<name>-<timestamp>.log` on the next start or on
shutdown. Beside it, `events.ndjson` is one JSON object per line, which is what
lets a run be replayed after the fact; it archives as
`<name>-<timestamp>.events.ndjson`.

**An idle worker is deliberately silent.** An empty queue must cost nothing, log
noise included, so "no output" is the healthy steady state.

| Symptom | Where to look |
|---|---|
| Nothing ever launches | `worker.log`. Queue genuinely empty (`relay check`)? Task delegated to *this* agent? `PAUSED` file present? Hourly ceiling hit? |
| `FAIL … HTTP 401` from `check` | Credential revoked, or the wrong `relay_mcp`. Issue a new one in relay and replace the whole URL, secret included. |
| `FAIL … HTTP` anything else from `check` | The host answered but not as a connector, so the host is right and the rest of the URL is not — usually a `connector_url` truncated on the way to the config. Paste the whole thing again. |
| `Not logged in · Please run /login` in `worker.log` | Claude Code is installed but not signed in. A worker launches it as you, and `relay check` proves the CLI exists and is new enough, not that it can authenticate — that would cost a model call. Run `claude` once yourself, log in, then `relay run` again. |
| `the installed codex is not signed in` | codex can be asked this for free, so it fails the check rather than the first run. Run `codex login` once as this user and sign in with your ChatGPT account. relay-cli sets no API key and never copies those credentials. |
| `THE CLI IS NOT SIGNED IN` in `worker.log` | The same thing found mid-run — the sign-in lapsed or expired after startup. `codex login` again. |
| `PAUSED — N consecutive runs were cut off by a spend or usage limit` | For claude, raise `runtime_config.max_usd_per_run`. For codex there is no per-run cap: the account's plan window is spent, so wait for it to reset, lower `max_runs_per_hour`, or lower `reasoning_effort`. The explanation logged with each run says which it was. |
| `THE RELAY MCP SERVER DID NOT COME UP` (codex) | The session had no relay tools. Run `relay check` — it tests the same credential over plain HTTP and spends nothing. If that passes, the installed codex could not connect: it has to support streamable-HTTP MCP servers, so upgrade it. |
| A codex worker cannot `git push`, or an install hangs | `runtime_config.network_access` is `false`, so commands the agent runs have no network. Set it to `true`, or give the worker a task that does not need one. |
| A codex worker reports edits it did not make | `runtime_config.sandbox` is `read-only`, so writes fail inside the run. `workspace-write` is the default and lets it edit `repo_dir`. |
| `is not a key this version accepts` | Every key is checked by name. The error suggests the one you meant, or says where the setting belongs — a runtime's setting goes inside `runtime_config`, and `poll_seconds` at the top level. Full list: [Worker fields](configuration.md#worker-fields). |
| A key is rejected with what to use *instead* | That key was removed in an earlier version; the message is the migration. The docs only describe what this version accepts. |
| `no config at …` / `has no configuration in it` | Nothing has been set up here yet — run `relay init`. It refuses to overwrite an existing file, so move an empty one aside first. |
| `still the placeholder from relay init` | The config was written but not filled in. Replace `relay_mcp` with the whole `connector_url` from relay, and `repo_dir` with the checkout this agent works in. |
| A config change had no effect | `state/` is rebuilt on start — restart `relay run` to apply it. |
| Worker starts and stops instantly | `worker.log` — usually the repo's own hooks, or a denied tool. |
| An agent says a tool isn't available | If relay refused it, the refusal names the capability; grant it on the agent in relay. If the *CLI* refused it, the log names it under "the CLI refused these tool calls". |
| `PAUSED — task(s) N have needed this agent's attention` | The attention-stall breaker. See [When a worker keeps relaunching against the same task](configuration.md#when-a-worker-keeps-relaunching-against-the-same-task). |
| `cycle timed out` repeatedly | Raise `max_seconds_per_run`, or the task is too big for one session — split it. |
| `runtime "claude" is unusable` / `runtime "codex" is unusable` | The CLI is missing from `PATH`, too old for the flags the adapter needs, or (codex) not signed in. The error names which. See [Runtimes](runtimes.md#the-startup-check). |
| `relay is already running (pid N)` | A fleet is already up — two would double-claim. `kill` that pid, or use the one that is running. |

Claim and lease behaviour is relay's rather than this CLI's: a task that
ping-pongs between cycles, or a parent that never wakes, is answered in the
[relay docs](https://relay.bytecurio.com/).
