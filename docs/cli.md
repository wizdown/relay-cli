# Commands and the dashboard

`relay-cli` is one static binary: the workers and a local dashboard.

## Commands

```bash
relay                 # one-screen summary. So do -h and --help
relay init            # write ~/.relay/config (never overwrites an existing one)
relay check           # validate the config, test every credential, verify every runtime
relay run             # start every worker and open the dashboard
relay version         # print the version
relay help            # the full manual
relay help <command>  # one command's usage. So does relay <command> --help
```

| Command | |
|---|---|
| `init` | Write a starting config: one worker per coding CLI found on `PATH`, the rest commented out. Takes no flags. |
| `check` | Validate the config, probe every credential, verify every runtime, and report what each `repo_dir` holds. Launches nothing and spends nothing. |
| `run` | Start every worker and open the dashboard. |
| `version` | Print the version. |
| `help` | The full manual: every field, default and safeguard. With a command name, that command's usage and flags. |

Every command reads `~/.relay/config`. There is no `--config` flag.

## Exit status

| Status | Meaning |
|---|---|
| `0` | Done. For `check`: every credential answered and every runtime is usable. |
| `1` | The config, a runtime or a credential failed. The message names the fix. |
| `2` | The command line was wrong: an unknown command, flag or argument. |

Errors go to stderr with an `error:` prefix; everything else to stdout.

## Flags

| Flag (`run`) | Default | |
|---|---|---|
| `--port` | `7717` | Dashboard port on `127.0.0.1`. If it is taken, the next free port is used and printed. |
| `--no-open` | off | Do not open a browser. |
| `--quiet` | off | Do not echo worker logs to stdout. The dashboard and `worker.log` are unaffected. |
| `--no-archive` | off | Do not archive logs to `~/.relay/logs/` on shutdown. |
| `--keep-awake` | off | macOS: hold off system sleep for as long as the fleet runs, while the Mac is on AC power. Closing the lid still sleeps it. On any other machine it warns and the fleet runs. |

| Flag (`check`) | Default | |
|---|---|---|
| `--timeout` | `15` | Seconds to wait for each credential probe. |

## What `check` reports

```text
relay 0.4.1 (beta) — checking 2 worker(s) from /Users/you/.relay/config
  runtime claude   2.1.250 (Claude Code) /Users/you/.local/bin/claude

  wizhub-claude            ok    queue: resume 0 · attention 1 · todo 0
    repo /Users/you/code/wizhub   CLAUDE.md · 2 skills · 1 subagent · 1 hook
  orchestrator-claude      ok    queue: resume 0 · attention 0 · todo 0
    repo /Users/you/relay/orchestrator   nothing to load — the agent arrives with its task and its tools
```

The queue line proves the credential works. Its three counts are what Relay
is holding for this agent:

| | |
|---|---|
| `resume` | a task this agent holds and can pick back up |
| `attention` | a task it holds that has moved: a subtask finished, or asked it something |
| `todo` | delegated work it has not started |

Work it can act on launches a session on the next poll; all three at `0`
is idle.

The `repo` line lists what the CLI will load from `repo_dir`, in that
runtime's own layout. "Nothing to load" is valid. If you wrote a file and it is
not listed, the agent will not see it. See
[The working directory](working-directory.md).

A runtime that is missing or signed out is reported after the probes, so it
does not hide what Relay said. Either failure exits `1`.

## Startup and shutdown

```text
relay 0.4.1 (beta) — 1 worker(s) from /Users/you/.relay/config
  runtime claude   2.1.250 (Claude Code) /Users/you/.local/bin/claude
  polling every 30s, 120s when idle
  wizhub-claude            runtime claude   runs/h 6  repo /Users/you/code/wizhub

dashboard: http://127.0.0.1:7717/
stop with Ctrl-C (workers stop, logs are archived to logs/)
```

The banner names each CLI it resolved and where. Workers run in the foreground
of this one process. Ctrl-C stops every worker, archives each log to
`~/.relay/logs/<name>-<timestamp>.log`, and deletes `~/.relay/state/`,
including the generated MCP configs that hold connector secrets.

A config edit takes effect on restart. Every start rebuilds `state/`.

## What the dashboard shows

The page reads each CLI's event stream, so a session appears line by line:

```text
14:22:08  wizhub-claude   ▶ run started   claude · /Users/you/code/wizhub
14:22:13  wizhub-claude   → relay:open_task    task_id=42
14:22:31  wizhub-claude   → Edit   src/handlers.go
14:23:02  wizhub-claude   ■ run ok   status 0 · $0.31 · 7 turns · 54.1s
14:26:02  app-codex       ■ run ok   status 0 · 41.2k tok · 82.0s
```

- **Worker cards**: state (`idle · polling · running · cooldown · ceiling ·
  at limit · paused · owner paused · probe failing`), the last poll's counts,
  runs against the hourly ceiling, cost or tokens so far, and a countdown to the
  next poll.
- **The fleet board**: a row per worker with its claimed task, current tool
  call, and spend, tokens and time against their caps.
- **The spend ledger**: the last hour by worker and by task: cost per run,
  turns, tools, cache share, outcomes, and spend per five minutes.
- **Every poll**, including empty ones. Quiet polls collapse to one line,
  labelled `queue empty` or `at claim limit`, and each change of poll rate is
  logged.
- **The live session**: each tool call with its target, and each result with
  its cost or tokens. For claude, the session line lists the MCP servers that
  came up.
- **The effective config**, with every default resolved.

A claude run shows dollars and a codex run tokens; see
[Runtimes](runtimes.md).

The dashboard is read-only. It binds `127.0.0.1` only, no flag changes that,
and connector secrets are redacted before anything reaches the page. Pausing a
worker is a file: `touch ~/.relay/state/<name>/PAUSED`.

## The dashboard API

The page is drawn from two read-only routes on the same port.

| Route | |
|---|---|
| `GET /api/snapshot` | The whole state in one document: version, poll rates, every worker with its state, recent runs, `task_id` and `ceiling_resets_at`, and recent events. `?events=0` omits the events. |
| `GET /api/stream` | Server-sent events, one per poll, run start, run end and session line. |

## Versioning

`relay version` prints one line. Quote it in bug reports:

```text
relay 0.4.1 (beta)                                a release
relay 0.4.1-SNAPSHOT (beta) [v0.0.9-4-g1aa22a3]   built from the repo, at that commit
```

relay-cli stays on 0.x until the interface settles. A release may change
configuration, defaults or the worker contract. Changes are listed in
[CHANGELOG.md](../CHANGELOG.md). An old config fails at `relay check`; see
[Validation](configuration.md#validation).
