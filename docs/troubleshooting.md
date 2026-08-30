# Troubleshooting

Start with `relay check`. It validates the config and tests every credential
without launching anything, so it separates "misconfigured" from "working, but
idle" for free.

Then the worker's own log: `~/.relay/state/<name>/worker.log`, or the archived
`~/.relay/logs/<name>-<timestamp>.log` after a shutdown.

**An idle worker is deliberately silent.** An empty queue must cost nothing, log
noise included, so "no output" is the healthy steady state. The dashboard shows
every poll including the empty ones, which is how you tell "idle" from "wedged".

| Symptom | Where to look |
|---|---|
| Nothing ever launches | `worker.log`. Queue genuinely empty (`relay check`)? Task delegated to *this* agent? `PAUSED` file present? Hourly ceiling hit? |
| `FAIL … HTTP 401` from `check` | Credential revoked, or the wrong `relay_mcp`. Issue a new one in relay and replace the whole URL, secret included. |
| A config key is rejected by name | This version does not accept that key. The error names what to use instead; see [Worker fields](configuration.md#worker-fields). |
| A config key seems to do nothing | Unknown keys at worker level are silently ignored. Check the spelling against [Worker fields](configuration.md#worker-fields). |
| A config change had no effect | `state/` is rebuilt on start — restart `relay run` to apply it. |
| Worker starts and stops instantly | `worker.log` — usually the repo's own hooks, or a denied tool. |
| An agent says a tool isn't available | If relay refused it (`capability_disabled`), grant the capability on the agent in relay. If the *CLI* refused it, the log names it under "the CLI refused these tool calls". |
| `PAUSED — task(s) N have needed this agent's attention` | The attention-stall breaker. See [When a worker keeps relaunching against the same task](configuration.md#when-a-worker-keeps-relaunching-against-the-same-task). |
| `PAUSED — N consecutive runs were killed by the $X cap` | Raise `runtime_config.max_usd_per_run`, or split the task in relay so a run finishes inside the cap. |
| `cycle timed out` repeatedly | Raise `max_seconds_per_run`, or the task is too big for one session — split it. |
| Task ping-pongs between cycles, or a parent never wakes | Claim and lease behaviour is relay's — see the [relay docs](https://relay.bytecurio.com/). |
| `runtime "claude" is unusable` | The CLI is missing from `PATH`, or too old for the flags the adapter needs. The error names which. See [Runtimes](runtimes.md#the-startup-check). |
| `worker … is already running as a bash poller` | Another process holds this worker's lock. The message names the pid; `kill` it, then start again. |
| Dashboard port already in use | It falls forward to the next free port and prints the real URL. `--port` pins one. |

## Reading a run after the fact

Each worker writes two files while it runs, both archived to `~/.relay/logs/` on
shutdown (`--no-archive` skips that):

- **`worker.log`** — the human log: cycle starts, ceilings, breaker messages.
- **`events.ndjson`** — one JSON object per line, which is what lets a run be
  replayed after the fact.

## Removing a worker

Ctrl-C stops the fleet. To take one worker out for good, delete its entry from
the config and its state directory:

```bash
rm -rf ~/.relay/state/<name>
```

Then **revoke its credential in relay** — deleting the config does not invalidate
a credential relay has already issued.

To remove everything: revoke every credential in it, then `rm -rf ~/.relay/` and
`rm $(command -v relay)`. Archived logs can contain anything the agents read, so
delete them deliberately rather than by habit.
