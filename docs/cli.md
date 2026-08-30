# Commands and the dashboard

`relay-cli` is one static binary: it runs the fleet *and* serves a local page
showing each worker's state, every poll, and the live event stream of whatever
session is running. The MCP probe and the config parser are built in, so it
needs neither `jq` nor `curl`.

## Commands

```bash
relay                 # print the full manual
relay init            # write ~/.relay/config (never overwrites an existing one)
relay check           # validate the config and test every credential
relay run             # start every worker and open the dashboard
relay version         # print the version
```

| Command | |
|---|---|
| `init` | write a starting config. Takes no flags. |
| `check` | validate the config and probe every credential, launching nothing and spending nothing |
| `run` | start every worker in the config and open the dashboard |
| `version` | print the version |
| `help` | the full manual — also what a bare invocation prints |

Starting is asked for by name rather than being the default, because it launches
autonomous sessions that spend money.

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

**There is no `--config`.** Every command reads `~/.relay/config` from any
directory, with `state/` and `logs/` beside it.

## What the dashboard shows

It runs the CLI with `--output-format stream-json`, so a session arrives line by
line while it happens:

```text
14:22:08  wizhub-claude   poll  resume 0 · attention 0 · todo 1
14:22:08  wizhub-claude   ▶ run started   claude · /Users/you/code/wizhub
14:22:10  wizhub-claude   session 9f31c8a2 · claude-opus-5   mcp: relay: connected
14:22:13  wizhub-claude   → relay:claim_task   task_id=42
14:22:31  wizhub-claude   → Edit   src/handlers.go
14:23:02  wizhub-claude   ■ run ok   status 0 · $0.31 · 7 turns · 54.1s
```

- **Worker cards** — state (`idle · polling · running · cooldown · ceiling ·
  paused · probe failing`), the last poll's three buckets, runs used against the
  hourly ceiling, cost so far, and a countdown to the next poll.
- **Every poll, including the empty ones** — which is how you tell "idle" from
  "wedged". Consecutive empty polls collapse to one line.
- **The live session** — each tool call with its target, and the result envelope
  with its cost. Which MCP servers came up is on the session line: a relay that
  is `needs-auth` produces a run that looks healthy and does nothing.
- **The effective config**, every default resolved — the one question reading the
  config file cannot answer.

## It cannot change anything

No route pauses a worker, starts a run, or edits a ceiling. A page that can spend
money is a different thing to reason about than one that only shows what already
happened. Pausing stays a file:

```bash
touch ~/.relay/state/<name>/PAUSED
```

It binds `127.0.0.1` only, and no flag changes that. Session output can contain
anything the agent read; connector secrets are redacted server-side, so the raw
endpoint is never in an HTTP response.

## Startup and shutdown

```text
relay 0.0.1 — 1 worker(s) from /Users/you/.relay/config
  runtime claude   2.1.250 (Claude Code) /Users/you/.local/bin/claude
  wizhub-claude            runtime claude   poll 30s  runs/h 6  repo /Users/you/code/wizhub

dashboard: http://127.0.0.1:7717/
stop with Ctrl-C (workers stop, logs are archived to logs/)
```

The banner names which CLI it resolved and where, so "which `claude` is this
using?" has an answer before the first run.

Workers run in the foreground of that one process — nothing to `disown`, nothing
to find again later. Ctrl-C stops every worker, archives each log to
`logs/<name>-<timestamp>.log`, and deletes `state/`, which is what removes the
generated MCP configs holding your connector secrets.

Starting always begins fresh: the previous run's logs are archived and `state/`
is recreated, which also clears any `PAUSED` file. **Re-running is how you apply
a config change.**
