# Troubleshooting

Start with `relay check`. It validates the config and tests every
credential without launching anything, so it separates "misconfigured" from
"working, but idle" for free.

Then the worker's own log: `~/.relay/state/<name>/worker.log`, or the archived
`logs/<name>-<timestamp>.log` after a shutdown.

**An idle worker is deliberately silent.** An empty queue must cost nothing, log
noise included, so "no output" is the healthy steady state rather than a symptom.
The dashboard shows every poll including the empty ones, which is how you tell
"idle" from "wedged".

| Symptom | Where to look |
|---|---|
| Nothing ever launches | `worker.log`. Queue genuinely empty (`relay check`)? Task delegated to *this* agent? `PAUSED` file present? Hourly ceiling hit? |
| `FAIL … HTTP 401` from `check` | Credential revoked, or the wrong `relay_mcp`. Issue a new one with `issue_agent_credential` and replace the whole URL, secret included. |
| A config key is rejected by name | That field was removed; the message names its replacement. Agent identity moved to the agent's `instructions_md` in relay. |
| A config key seems to do nothing | Unknown keys are silently ignored — every optional field is read with a fallback. Check the spelling against [Configuration](configuration.md#optional-fields). |
| Worker starts and stops instantly | `worker.log` — usually the repo's own hooks, or a denied tool. |
| An agent says a tool isn't available | If relay refused it (`capability_disabled`), grant the capability with `update_agent`. If the *CLI* refused it, the log names it under "the CLI refused these tool calls" and `RELAY_ALLOWED_TOOLS` is behind relay's surface. |
| Orchestrator never wakes when a subtask finishes | Its parent task must still be *held* for `attention` to fire. Check it did not `release_task`, and that its `lease_ttl_seconds` is long enough to stay live between polls. See [Agents and fleets](fleets.md#how-an-orchestrator-gets-woken). |
| `PAUSED — task(s) N have needed this agent's attention` | A stranded handoff. See [When a fan-out gets stuck](fleets.md#when-a-fan-out-gets-stuck). |
| `PAUSED — N consecutive runs were killed by the $X cap` | Raise `runtime_config.max_usd_per_run`, or split the task in relay so a run can finish inside the cap. |
| Task ping-pongs between cycles | The relay lease is shorter than a run; raise the agent's `lease_ttl_seconds`. |
| `cycle timed out` repeatedly | Raise `max_seconds_per_run`, or the task is too big for one session — split it. |
| Task went Blocked as "stuck" | Relay's attempt cap. Read the activity log, fix the brief, return it to the agent. |
| `runtime "claude" is unusable` | The CLI is missing from `PATH`, or too old for the flags the adapter needs. The error names which flags. See [Runtimes](runtimes.md#every-runtime-is-checked-before-anything-launches). |
| `worker … is already running as a bash poller` | A worker left over from the retired shell poller. The message names the pid; `kill` it. |
| Dashboard port already in use | It falls forward to the next free port and prints the real URL. Use `--port` to pin one. |

## Removing a worker

Ctrl-C stops the fleet. To take one worker out for good, delete its entry from
the config and its state directory:

```bash
rm -rf ~/.relay/state/<name>
```

Then **revoke its credential in relay** — `revoke_agent_credential`, or the
agent's page in your workspace. Deleting the config does not invalidate a
credential relay has already issued.

To remove everything: `rm -rf ~/.relay/` (revoke every credential in
it first), and `rm $(command -v relay)`. Archived sessions under `logs/`
can contain anything the agents read, so delete them deliberately rather than by
habit.

## Reading a run after the fact

Each worker writes two files while it runs, and both are archived to `logs/` on
shutdown:

- **`worker.log`** — the human log: cycle starts, ceilings, breaker messages.
- **`events.ndjson`** — the structured record, one JSON object per line. This is
  what lets a run be replayed after the fact, which the prose log cannot do.

`ARCHIVE_LOGS` behaviour is `--no-archive`: pass it to `run` to skip the archive
on shutdown.
