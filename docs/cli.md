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
| `check` | validate the config, probe every credential and report what each `repo_dir` holds, launching nothing and spending nothing |
| `run` | start every worker in the config and open the dashboard |
| `version` | print the version |
| `help` | the full manual — also what a bare invocation prints |

Starting is asked for by name rather than being the default, because it launches
autonomous sessions that spend money.

`version` prints one line, and it is the line to quote in a bug report:

```text
relay 0.1.1 (beta)                                a release
relay 0.1.1-SNAPSHOT (beta) [v0.0.9-4-g1aa22a3]   built from the repo
```

A `-SNAPSHOT` version is a build made between releases; the part in brackets is
the commit it came from. A downloaded release prints neither.

## What `check` answers

```text
relay 0.1.1 (beta) — checking 2 worker(s) from /Users/you/.relay/config
  runtime claude   2.1.250 (Claude Code) /Users/you/.local/bin/claude

  wizhub-claude            ok    queue: resume 0 · attention 1 · todo 0
    repo /Users/you/code/wizhub   CLAUDE.md · 2 skills · 1 subagent · 1 hook
  orchestrator-claude      ok    queue: resume 0 · attention 0 · todo 0
    repo /Users/you/relay/orchestrator   nothing to load — the agent arrives with its task and its tools
```

Two questions, one pass. The queue line proves the credential works and relay is
reachable.

Those three counts are relay's answer to one poll — how much work it is holding
for this agent, in the three buckets the loop acts on:

| | |
|---|---|
| `resume` | a task this agent already holds and can pick back up |
| `attention` | a task it is holding that has moved — a subtask finished, or asked it something |
| `todo` | delegated work it has not started |

**Any one of them above zero launches a session**, and all three at `0` is the
healthy idle reading, not a fault. What puts a task in each bucket — delegation,
leases, hand-backs — is relay's, and is in the
[relay docs](https://relay.bytecurio.com/).

The `repo` line proves the other half: that the `CLAUDE.md`, skills
and subagents someone wrote are where the CLI will look for them — a file one
directory up, or a skill in a folder that isn't `<name>/SKILL.md`, produces a
worker that starts, spends and knows none of it.

"Nothing to load" is a valid answer, not a warning: an empty directory is a
working setup. [The working directory](working-directory.md) is what you can add
to one.

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
relay 0.1.1 (beta) — 1 worker(s) from /Users/you/.relay/config
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
