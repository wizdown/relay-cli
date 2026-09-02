# Troubleshooting

Start with `relay check`. It validates the config and tests every credential
without launching anything, so it separates "misconfigured" from "working but
idle".

Then read the worker's log: `~/.relay/state/<name>/worker.log` while it runs,
archived to `~/.relay/logs/<name>-<timestamp>.log` on shutdown. Beside it,
`events.ndjson` holds the same run as one JSON object per line.

An idle worker prints nothing. "No output" is the healthy steady state.

## `relay check` fails

| Message | Fix |
|---|---|
| `FAIL … HTTP 401` | The credential was revoked, or `relay_mcp` is wrong. Issue a new one in Relay and paste the whole URL. |
| `FAIL … HTTP` anything else | The host answered but not as a connector. Usually the URL was truncated on the way into the config. Paste it again. |
| `no config at …` / `has no configuration in it` | Run `relay init`. It will not overwrite an existing file, so move an empty one aside first. |
| `is still the placeholder from …` | Replace `relay_mcp` with the connector URL and `repo_dir` with the directory the agent works in. |
| `is not a key this version accepts` | Every key is checked by name. The error names the key you meant, or where the setting belongs (a runtime's setting inside `runtime_config`; `poll_seconds` at the top level). See [Worker fields](configuration.md#worker-fields). |
| A key is rejected with what to use *instead* | That key was removed in an earlier version. The message is the migration. |
| `runtime_config.model is "…" — it takes one of` | Fix the typo or use a listed alias. If the model is newer than this relay-cli, set `RELAY_CLI_SKIP_MODEL_CHECK=1`. See [Model names](configuration.md#model-names). |

## `relay run` refuses to start

| Message | Fix |
|---|---|
| `runtime "claude" is unusable` / `runtime "codex" is unusable` | The CLI is missing from `PATH`, too old for the flags relay-cli needs, or not signed in. The error says which and names the fix. Install or upgrade [Claude Code](https://claude.com/claude-code) or the [Codex CLI](https://developers.openai.com/codex/cli). |
| `the installed claude is not signed in` | Run `claude auth login` as the user the fleet runs as. |
| `the installed codex is not signed in` | Run `codex login` as that user and sign in with your ChatGPT account. |
| `does not support what relay-cli needs` | The installed CLI's `--help` lacks a flag the adapter uses; the message lists them. Upgrade the CLI, or set `RELAY_CLI_SKIP_RUNTIME_CHECK=1` to try anyway. |
| `could not ask <cli> whether it is signed in` | A warning. The CLI is too old to answer, so the start continues and a real failure shows on the first run. Upgrading the CLI restores the check. |
| `reports it is NOT signed in, but X is set — starting anyway` | A warning. An API key in the environment authenticates runs, and relay-cli cannot check it is valid. If every run fails at once, sign in properly and unset the variable. |
| `is not one runtime "codex" offered when this relay-cli was built` | A warning. The model check was skipped and the name went to the CLI unchecked. If every run fails at once, it was a typo. |
| `no coding CLI found on PATH, and a worker is one` | `relay init` wrote nothing. Install Claude Code or the Codex CLI, sign in, and run `init` again. |
| A worker for the runtime you wanted is commented out | That CLI was not on `PATH` when `relay init` ran. Install and sign it in, then delete the `// ` from those lines. |
| `relay is already running (pid N)` | A fleet is already up. Use it, or `kill` that pid. |

## A run fails, or nothing launches

| Symptom | Fix |
|---|---|
| Nothing ever launches | Check `worker.log`. Is the queue empty (`relay check`)? Is the task delegated to *this* agent? Is there a `PAUSED` file? Is the hourly ceiling reached? |
| A config change had no effect | Restart `relay run`. `state/` is rebuilt on start. |
| `THE CLI IS NOT SIGNED IN` in `worker.log` | The sign-in lapsed after startup. Sign in again and restart. |
| `THE RELAY MCP SERVER DID NOT COME UP` (codex) | The session had no Relay tools. If `relay check` passes, the installed codex cannot connect to a streamable-HTTP MCP server. Upgrade it. |
| `PAUSED — N consecutive runs were cut off by a spend or usage limit` | claude: raise `runtime_config.max_usd_per_run`. codex: the account's plan window is spent, so wait for it to reset, or lower `max_runs_per_hour` or `reasoning_effort`. Then remove the `PAUSED` file. |
| `PAUSED — task(s) N have needed this agent's attention` | The same task came back needing attention on 3 completed runs in a row, usually because its capability was revoked or the parent was re-delegated. Resolve it in Relay, then remove the `PAUSED` file. |
| `cycle timed out` repeatedly | Raise `max_seconds_per_run`, or split the task. |
| Worker starts and stops instantly | Check `worker.log`. Usually the repo's own hooks, or a denied tool. |
| An agent says a tool is not available | If Relay refused it, the refusal names the capability to grant on the agent. If the CLI refused it, `worker.log` lists it under "the CLI refused these tool calls". |
| A codex worker cannot `git push`, or an install hangs | `runtime_config.network_access` is `false`. Set it to `true`. |
| A codex worker reports edits it did not make | `runtime_config.sandbox` is `read-only`. The default `workspace-write` lets it edit `repo_dir`. |
| Logs name a model you did not write | You wrote an alias, and relay-cli pins it to an id. See [Model names](configuration.md#model-names). |

Claim and lease behaviour belongs to Relay, not this CLI. A task that
ping-pongs between cycles, or a parent that never wakes, is answered in the
[Relay docs](https://relay.bytecurio.com/).
