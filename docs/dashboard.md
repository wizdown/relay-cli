# The dashboard

`relay-cli` is one static binary with no dependencies: it runs the whole fleet
*and* serves a local page showing each worker's state, every poll result, and the
live event stream of whatever CLI session is running right now. The MCP probe and
the config parser are built in, so it needs neither `jq` nor `curl`.

## Commands

```bash
relay                 # prints the full manual — commands, flags, config, safeguards
relay check           # validate the config and test every credential; launches nothing
relay run             # reads ~/.relay/config, opens the dashboard
relay run --port 7717 --no-open
```

| Command | |
|---|---|
| `run` | start every worker in the config and open the dashboard |
| `check` | validate the config and probe every worker's credential, spending nothing |
| `version` | print the version |
| `help` | the full manual (also what a bare invocation prints) |

Starting is asked for by name rather than being the bare default, because it is
not a neutral one: it launches autonomous sessions that spend money. A bare
`relay-cli` prints the manual instead, which is also what makes the binary
usable by someone — or something — that has never seen this page.

## Flags

| Flag (for `run`) | Default | |
|---|---|---|
| `--port` | `7717` | loopback port; falls forward to the next free one rather than refusing to start |
| `--no-open` | off | don't open a browser (servers, containers, CI) |
| `--quiet` | off | don't echo worker logs to stdout |
| `--no-archive` | off | don't archive logs to `logs/` on shutdown |

| Flag (for `check`) | Default | |
|---|---|---|
| `--timeout` | `15` | seconds to wait for each credential probe |

`init` takes no flags at all.

**There is no `--config`.** Every command reads `~/.relay/config`, from any
directory, and `state/` and `logs/` are created beside it. A worker is a relay
agent identity holding a credential issued to you, and a fleet routinely spans
several checkouts — so one user-scoped location means "which config is this
actually running?" is a question nobody has to ask.

`relay help` prints all of this, so the binary explains itself with nothing
else installed.

## Why it exists

The shell poller this replaced ran `claude` with `--output-format json`, which
prints **one** object at the very end of a run. For fifteen minutes you saw
nothing; then a wall of JSON. There was no way to watch an agent work.

`relay-cli` runs the CLI with `--output-format stream-json`, so a session
arrives as it happens and the page reads as a narration of the run:

```text
14:22:08  wizhub-claude   poll  resume 0 · attention 0 · todo 1
14:22:08  wizhub-claude   ▶ run started   claude · /Users/you/code/wizhub
14:22:10  wizhub-claude   session 9f31c8a2 · claude-opus-5   mcp: relay: connected
14:22:11  wizhub-claude   → relay:get_available_tasks
14:22:13  wizhub-claude   → relay:claim_task   task_id=42
14:22:14  wizhub-claude   ← Claimed task 42.
14:22:31  wizhub-claude   → Edit   src/handlers.go
14:23:02  wizhub-claude   result  $0.31 · 7 turns
14:23:02  wizhub-claude   ■ run ok   status 0 · $0.31 · 7 turns · 54.1s
```

## What the page shows

- **Worker cards** — state (`idle · polling · running · cooldown · ceiling ·
  paused · probe failing`), the three buckets from the last poll, runs used
  against the hourly ceiling, cost so far, and a countdown to the next poll.
- **Every poll**, including the empty ones — which is how you tell "idle" from
  "wedged". Consecutive empty polls collapse to one line so they never bury a
  run. They still stay out of `worker.log`, where an idle worker costs nothing.
- **The live session** — each tool call with its target, assistant turns, and the
  final result envelope with its cost. Which MCP servers came up is called out on
  the session line: a relay that is `needs-auth` produces a run that looks healthy
  and does nothing, and that is worth seeing in the first second rather than the
  last.
- **Configuration** — the *effective* config with every default resolved, which
  is the one question reading the config cannot answer.

## It cannot change anything

There is no route in the server that pauses a worker, starts a run, or edits a
ceiling — not a hidden one, not a disabled button. A page that can spend money is
a different thing to reason about than a page that can only show what already
happened, and this version is deliberately the second one.

Pausing works the way it always has:

```bash
touch ~/.relay/state/<name>/PAUSED
```

The dashboard shows a paused worker as paused, and cannot clear it for you.

It binds `127.0.0.1` only, and no flag changes that. Session output can contain
anything the agent read. Connector secrets are redacted **server-side** — the raw
endpoint is never in an HTTP response, and every log line, event and error is
scrubbed on the way out.

## Startup and shutdown

```text
relay 0.1.0 — 1 worker(s) from /Users/you/.relay/config
  runtime claude   2.1.250 (Claude Code) /Users/you/.local/bin/claude
  wizhub-claude            runtime claude   poll 30s  runs/h 6  repo /Users/you/code/wizhub

dashboard: http://127.0.0.1:7717/
stop with Ctrl-C (workers stop, logs are archived to logs/)
```

The banner names which CLI it resolved and where it found it, so "which `claude`
is this using?" has an answer before the first run rather than after a surprising
one.

Workers run in the foreground of that one process, so there is nothing to
`disown` and nothing to find again later. Ctrl-C stops every worker, archives
each log to `logs/<name>-<timestamp>.log`, and deletes `state/` — which is
also what removes the generated MCP configs holding your connector secrets.

Starting always begins fresh: a previous run's logs are archived and
`state/` is recreated, which is also what clears a `PAUSED` file. That
means re-running is how you apply a config change.

## Building it

```bash
make          # gofmt + vet + test, then build ./relay
make dist     # release artifacts + SHA256SUMS
```

Needs Go 1.22+ to build, and nothing at all to run — beyond the coding CLI the
workers drive, which you install separately.
