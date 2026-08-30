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
| A config key is rejected by name, or seems to do nothing | This version does not accept that key — a rejection names what to use instead, and an unknown key at worker level is ignored silently. Check it against [Worker fields](configuration.md#worker-fields). |
| A config change had no effect | `state/` is rebuilt on start — restart `relay run` to apply it. |
| Worker starts and stops instantly | `worker.log` — usually the repo's own hooks, or a denied tool. |
| An agent says a tool isn't available | If relay refused it, the refusal names the capability; grant it on the agent in relay. If the *CLI* refused it, the log names it under "the CLI refused these tool calls". |
| `PAUSED — task(s) N have needed this agent's attention` | The attention-stall breaker. See [When a worker keeps relaunching against the same task](configuration.md#when-a-worker-keeps-relaunching-against-the-same-task). |
| `PAUSED — N consecutive runs were killed by the $X cap` | Raise `runtime_config.max_usd_per_run`, or split the task in relay so a run finishes inside the cap. |
| `cycle timed out` repeatedly | Raise `max_seconds_per_run`, or the task is too big for one session — split it. |
| `runtime "claude" is unusable` | The CLI is missing from `PATH`, or too old for the flags the adapter needs. The error names which. See [Runtimes](runtimes.md#the-startup-check). |
| `relay is already running (pid N)` | A fleet is already up — two would double-claim. `kill` that pid, or use the one that is running. |

Claim and lease behaviour is relay's rather than this CLI's: a task that
ping-pongs between cycles, or a parent that never wakes, is answered in the
[relay docs](https://relay.bytecurio.com/).
