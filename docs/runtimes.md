# Runtimes

A **runtime** is which coding CLI drives a worker. It is required on every
worker, alongside whatever that CLI needs in `runtime_config` — see
[Configuration](configuration.md#worker-fields).

relay-cli contains no coding CLI. What it contains is an *adapter*: the code that
builds an argv and reads what the CLI prints. The CLI itself is a separate
program you install, resolved on `PATH` at startup and named in the banner, so
"which `claude` is this using?" has an answer before the first run.

## Which runtimes exist

| `runtime` | Support | CLI you install |
|---|---|---|
| `claude` | **Supported** | [Claude Code](https://claude.com/claude-code) |
| `codex` | **Coming soon** — refused when the config loads | — |

`codex` is refused by name, and so is any other value: a runtime you cannot use
should cost a startup error, not a session you have already paid for.

The keys `claude` accepts in `runtime_config` — `model` and `max_usd_per_run` —
are in [Configuration](configuration.md#runtime_config-for-claude).

## The startup check

`relay run` and `relay check` both prove the named CLI is usable on this machine
before anything launches. A failure stops the whole start.

### The CLI is not installed

```text
error: the config is valid, but a runtime it names is not usable here:
  worker "wizhub-claude" cannot run: runtime "claude" is unusable.
       claude not found on PATH — install Claude Code (https://claude.com/claude-code).
       relay-cli does not bundle any CLI; each one is installed separately.
```

### The CLI is too old

For `claude` the check goes further than "is it there". The adapter depends on
specific flags, so it reads the installed CLI's own `--help` and names every one
that is missing, with what relay-cli needed it for:

```text
error: the config is valid, but a runtime it names is not usable here:
  worker "wizhub-claude" cannot run: runtime "claude" is unusable.
       the installed claude (1.0.3 (Claude Code) at /usr/local/bin/claude) does not support what relay-cli needs.
       Its --help does not offer:
         --strict-mcp-config          keeping the operator's personal MCP servers out of an unattended run
         --max-budget-usd             the per-run spend cap
         --output-format stream-json  the live session feed — the reason relay-cli exists
       Upgrade Claude Code (https://claude.com/claude-code), then try again.
       To run anyway, set RELAY_CLI_SKIP_RUNTIME_CHECK=1 — each session will
       then fail on the missing flag instead of failing here, once.
```

It checks **capabilities rather than a version number** deliberately: the adapter
depends on those flags, not on a release, and a version gate would block working
installs whenever it guessed high.

`RELAY_CLI_SKIP_RUNTIME_CHECK=1` skips the flag check — not the
does-it-exist check — if a future CLI reshapes its help text.

### `--help` cannot be read

Unverifiable is not the same as unusable. If `claude --help` cannot be run at
all, the start continues with a warning rather than refusing over it:

```text
warning: could not run `claude --help` to verify this install supports the flags relay-cli needs (…).
         Continuing anyway; a missing flag will surface on the first run.
```

## What a claude run does

- **Your own MCP servers do not load.** The session runs with
  `--strict-mcp-config`, so it sees this worker's relay connector and nothing
  else — not your personal MCP config, and not an `.mcp.json` in the working
  directory. What the session *does* pick up from that directory is in
  [The working directory](working-directory.md).
- **Anything not pre-allowed is denied, silently.** A headless run has no
  terminal to answer an approval prompt on, so the session is launched naming
  every relay tool and the ordinary coding tools — reading and editing files,
  running commands, searching the web. Neither relay access nor the ability to
  do the work rides on the model's judgement about an unfamiliar tool. A refusal
  is named in `worker.log`, under "the CLI refused these tool calls"; see
  [Troubleshooting](troubleshooting.md).
- **`model` and `max_usd_per_run` become flags**, and nothing else does. Every
  setting a runtime accepts is a declared `runtime_config` key.
- **The harness contract is appended to the CLI's own system prompt**, via
  `--append-system-prompt`, so it layers on top rather than replacing it. It is
  runtime-neutral — the same text whichever CLI runs — and what it says, plus
  how to replace it, is in
  [The working directory](working-directory.md#step-0--an-empty-directory).
- **A finished run is reported in words, not an exit code.** The adapter reads
  the result envelope, so a spend-cap kill says it was cut off mid-task and what
  to change, rather than surfacing as a bare non-zero exit.

Adding a runtime is a contributor topic:
[docs/contributing/adapters.md](contributing/adapters.md).
