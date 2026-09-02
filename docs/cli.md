# Commands and the dashboard

`relay-cli` is one static binary. It runs the workers and serves a local
dashboard.

## Commands

```bash
relay                 # one-screen summary
relay init            # write ~/.relay/config (never overwrites an existing one)
relay check           # validate the config and test every credential
relay run             # start every worker and open the dashboard
relay version         # print the version
relay help            # the full manual
```

| Command | |
|---|---|
| `init` | Write a starting config: one worker per coding CLI found on `PATH`, the rest commented out. Takes no flags. |
| `check` | Validate the config, probe every credential, and report what each `repo_dir` holds. Launches nothing and spends nothing. |
| `run` | Start every worker and open the dashboard. |
| `version` | Print the version. |
| `help` | The full manual: every field, default and safeguard. `relay` and `relay -h` print a one-screen summary instead. |

Every command reads `~/.relay/config`. There is no `--config` flag.

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
relay 0.2.0 (beta) — checking 2 worker(s) from /Users/you/.relay/config
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

Any count above zero launches a session on the next poll. All three at `0` is
healthy and idle.

The `repo` line lists what the CLI will load from `repo_dir`, in that
runtime's own layout (`CLAUDE.md` and `.claude/` for claude, `AGENTS.md` and
`.codex/` for codex). "Nothing to load" is valid. If you wrote a file and it
is not listed, the agent will not see it. See
[The working directory](working-directory.md).

## Startup and shutdown

```text
relay 0.2.0 (beta) — 1 worker(s) from /Users/you/.relay/config
  runtime claude   2.1.250 (Claude Code) /Users/you/.local/bin/claude
  wizhub-claude            runtime claude   poll 30s  runs/h 6  repo /Users/you/code/wizhub

dashboard: http://127.0.0.1:7717/
stop with Ctrl-C (workers stop, logs are archived to logs/)
```

The banner names each CLI it resolved and where. Workers run in the foreground
of this one process.

Ctrl-C stops every worker, archives each log to
`~/.relay/logs/<name>-<timestamp>.log`, and deletes `~/.relay/state/`,
including the generated MCP configs that hold connector secrets.

Every start rebuilds `state/`, which also clears any `PAUSED` file.
**Restart `relay run` to apply a config change.**

## What the dashboard shows

The page reads each CLI's event stream (`--output-format stream-json` for
claude, `--json` for codex), so a session appears line by line:

```text
14:22:08  wizhub-claude   poll  resume 0 · attention 0 · todo 1
14:22:08  wizhub-claude   ▶ run started   claude · /Users/you/code/wizhub
14:22:10  wizhub-claude   session 9f31c8a2 · claude-opus-5   mcp: relay: connected
14:22:13  wizhub-claude   → relay:claim_task   task_id=42
14:22:31  wizhub-claude   → Edit   src/handlers.go
14:23:02  wizhub-claude   ■ run ok   status 0 · $0.31 · 7 turns · 54.1s
14:24:40  app-codex       ▶ run started   codex · /Users/you/code/app
14:24:44  app-codex       → relay:claim_task
14:25:19  app-codex       → Bash   bash -lc 'go test ./...'
14:26:02  app-codex       ■ run ok   status 0 · 41.2k tok · 82.0s
```

- **Worker cards**: state (`idle · polling · running · cooldown · ceiling ·
  paused · probe failing`), the last poll's three counts, runs used against
  the hourly ceiling, cost or tokens so far, and a countdown to the next poll.
- **Every poll**, including empty ones. Consecutive empty polls collapse to one
  line.
- **The live session**: each tool call with its target, and the result with its
  cost or token usage. For claude, the session line shows which MCP servers
  came up.
- **The effective config**, with every default resolved.

A claude run shows dollars, as the CLI reports them. A codex run shows tokens,
because that CLI reports no cost.

The dashboard is read-only. It binds `127.0.0.1` only, no flag changes that,
and connector secrets are redacted before anything reaches the page. Pausing a
worker is a file: `touch ~/.relay/state/<name>/PAUSED`.

## Versioning

`relay version` prints one line. Quote it in bug reports:

```text
relay 0.2.0 (beta)                                a release
relay 0.2.0-SNAPSHOT (beta) [v0.0.9-4-g1aa22a3]   built from the repo, at that commit
```

relay-cli stays on 0.x until the interface settles. A release may change
configuration, defaults or the worker contract. Changes are listed in
[CHANGELOG.md](../CHANGELOG.md) and in the release notes. A removed config key
is rejected by name with its replacement, so an old config fails at
`relay check` with the fix in the message.
