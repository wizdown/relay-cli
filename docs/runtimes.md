# Runtimes

A **runtime** is which coding CLI drives a worker. It is a required per-worker
field:

```json
{ "name": "wizhub-claude", "relay_mcp": "…", "repo_dir": "~/code/wizhub", "runtime": "claude", "runtime_config": { "model": "sonnet" } }
```

| `runtime` | Support | CLI you install |
|---|---|---|
| `claude` | **Supported** | [Claude Code](https://claude.com/claude-code) |
| `codex` | **Coming soon** — refused at config load | — |
| anything else | Not offered | — |

`relay-cli` contains no coding CLI. What it contains is an *adapter*: the code
that builds an argv and parses what the CLI prints. The CLI itself is a separate
program you install, found on `PATH` at startup.

`codex` is refused when the config **loads**, by name — a runtime you cannot use
should cost a startup error, not a session you have already paid for. It has no
per-run dollar cap yet, and a runtime that cannot be bounded is not one this
ships quietly.

## What is checked before anything launches

A failure stops the whole start:

```text
error: worker "wizhub-claude" cannot run: runtime "claude" is unusable.
       claude not found on PATH — install Claude Code (https://claude.com/claude-code).
```

For `claude` the check goes further than "is it there". The adapter depends on
specific flags — streaming output, the MCP config, `--strict-mcp-config`, the
spend cap — so it reads the installed CLI's own `--help` and names any that is
missing:

```text
error: worker "w" cannot run: runtime "claude" is unusable.
       the installed claude (1.0.3 (Claude Code) at /usr/local/bin/claude) does not support what this poller needs.
       Its --help does not offer:
         --strict-mcp-config          keeping your personal MCP servers out of an unattended run
         --max-budget-usd             the per-run spend cap
         --output-format stream-json  the live session feed
       Upgrade Claude Code (https://claude.com/claude-code), then try again.
```

It checks **capabilities rather than a version number** deliberately: the adapter
depends on those flags, not on a release, and a version gate would block working
installs whenever it guessed high.

`RELAY_CLI_SKIP_RUNTIME_CHECK=1` bypasses the flag check (not the
does-it-exist check) if a future CLI reshapes its help text. Each session then
fails on the missing flag instead, once per run.

## How the claude adapter runs a session

`claude -p` with `--strict-mcp-config`, so an unattended run sees relay and
nothing else from your personal MCP config.

Headless mode can never answer an approval prompt, so any tool not pre-allowed is
silently denied. The run uses `--permission-mode auto`, and every relay tool is
additionally named in `--allowedTools` — relay access never rides on the model's
judgement about an unfamiliar tool.

The argv is deliberately not overridable: raw arguments could silently replace
the flags a headless run depends on. Every setting a runtime accepts is a
declared key in `runtime_config`.

It takes `runtime_config.model` (required) and `runtime_config.max_usd_per_run`
— see [Configuration](configuration.md#runtime_config-for-claude) — and reads the
result envelope so a budget kill or a permission denial is reported in plain
words rather than as a bare exit code.

Adding a runtime is a contributor topic:
[docs/contributing/adapters.md](contributing/adapters.md).
