# Runtimes

A **runtime** is which coding CLI drives a worker. It is required on every
worker, alongside whatever that CLI needs in `runtime_config` — see
[Configuration](configuration.md#worker-fields).

relay-cli contains no coding CLI. What it contains is an *adapter*: the code that
builds an argv and reads what the CLI prints. The CLI itself is a separate
program you install, resolved on `PATH` at startup and named in the banner, so
"which `claude` is this using?" has an answer before the first run.

## Which runtimes exist

| `runtime` | Support | CLI you install | Per-run spend cap |
|---|---|---|---|
| `claude` | **Supported** | [Claude Code](https://claude.com/claude-code) | yes — `max_usd_per_run` |
| `codex` | **Supported** | [Codex CLI](https://developers.openai.com/codex/cli) | **no** — the CLI has none |

Any other value is refused when the config loads: a runtime you cannot use should
cost a startup error, not a session you have already paid for.

What each accepts in `runtime_config` is in Configuration —
[claude](configuration.md#runtime_config-for-claude) takes `model` and
`max_usd_per_run`, [codex](configuration.md#runtime_config-for-codex) takes
`model`, `reasoning_effort`, `sandbox`, `network_access` and `web_search`. Every
one of those keys has a declared set of values or a type, checked when the config
loads — neither CLI rejects a bad setting until it is already inside a run you
have paid for. `model` is the sharp end of that, and it works the same for both:
a list this build knows, short [aliases pinned to one id each](configuration.md#aliases-are-pinned-not-tracked),
and an [escape hatch](configuration.md#a-model-this-build-has-not-heard-of) for a
model newer than your relay-cli.

**Read the spend column before choosing.** A claude run can be cut off at a
dollar figure by the CLI itself. A codex run cannot — there is no such flag — so
a codex worker is bounded by `max_seconds_per_run`, `max_runs_per_hour`, its
`reasoning_effort`, and the plan limits of the account it is signed in as. Two
runs stopped by those plan limits in a row pause the worker, the same way two
spend-cap kills do.

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

The check goes further than "is it there". Each adapter depends on specific
flags, so it reads the installed CLI's own `--help` and names every one that is
missing, with what relay-cli needed it for:

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

`RELAY_CLI_SKIP_RUNTIME_CHECK=1` skips the flag and sign-in checks — not the
does-it-exist check — if a future CLI reshapes its help text or its status
output. It also covers the model-name check on either runtime's config, which is
the same problem one layer up: a list this build knows and a CLI that has moved
past it.
That one has its own switch,
[`RELAY_CLI_SKIP_MODEL_CHECK`](configuration.md#a-model-this-build-has-not-heard-of).

### The CLI is not signed in

A worker launches the CLI as you, so it authenticates the way your own sessions
do — `claude auth login`, or `codex login` with your ChatGPT account. relay-cli
never writes, moves or copies those credentials, and it sets no API key.

**A signed-out CLI stops the start.** Both can be asked from their own stored
credentials without spending anything (`claude auth status --json`,
`codex login status`), so this is a startup error rather than something every
cycle rediscovers: a fleet whose CLI is signed out would launch a session per
worker per cycle, fail in the same second each time, and say so only in
`worker.log`.

```text
error: the config is valid, but a runtime it names is not usable here:
  worker "app-codex" cannot run: runtime "codex" is unusable.
       the installed codex is not signed in (Not logged in).
       Run `codex login` once as this user and sign in with your ChatGPT
       account; workers then run as you, with no API key to configure.
       relay-cli never writes or moves those credentials.
```

Only workers that name that runtime are affected: a fleet of claude workers does
not care whether codex is signed in, and the check is only run for a runtime some
worker asks for.

Two things deliberately do **not** fail the start, because a check that refuses a
fleet which would have worked is worse than no check:

- **A CLI too old to be asked.** It warns and continues —
  `could not ask claude whether it is signed in (…)`.
- **A credential in the environment.** `ANTHROPIC_API_KEY`, `CODEX_API_KEY` or a
  third-party provider authenticates a run whatever the stored sign-in says, so
  the check stands down for that CLI. codex is explicit that a `CODEX_API_KEY`
  never becomes a cached login.

  It stands down **out loud**, because what relay-cli knows is that the variable
  is set, not that it is valid — checking that would cost the model call `check`
  refuses to spend:

  ```text
  warning: codex reports it is NOT signed in, but OPENAI_API_KEY is set — starting anyway.
           relay-cli cannot tell whether that key is valid without spending a call, so it
           trusts it. If every run fails immediately, the key is wrong or left over from
           something else: run `codex login` and unset OPENAI_API_KEY.
  ```

  If you sign in with a subscription, none of this applies: there is no key, the
  stored sign-in is the whole answer, and a healthy start says nothing.

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

## What a codex run does

- **Your own codex config does not load.** The session runs with
  `--ignore-user-config`, so `~/.codex/config.toml` — your models, your MCP
  servers, your defaults — is not read. This worker's relay connector is passed
  in directly and is the only MCP server the session has. A `.codex/config.toml`
  **inside the working directory** is a project config and still applies;
  `relay check` names it if one is there.
- **The sandbox is what bounds a run, not an allowlist.** codex has no per-tool
  approval to pre-grant in a headless run: what it may write is
  `runtime_config.sandbox`, and whether the commands it runs may reach the
  network is `network_access`. The defaults — `workspace-write` and network on —
  let a worker edit its checkout and push a branch, and nothing outside it.
- **The harness contract arrives in the prompt.** `codex exec` has no
  "append to the system prompt" flag, so the contract is the first thing in the
  prompt instead. It is the same runtime-neutral text a claude worker gets; see
  [The working directory](working-directory.md#step-0--an-empty-directory).
- **Sessions are not recorded.** Runs use `--ephemeral`, so a fleet does not
  leave a session recording behind every cycle. relay-cli keeps its own record
  in `~/.relay/logs/`.
- **Tokens, not dollars.** codex reports token usage and no cost, so that is
  what the dashboard and the logs show for a codex worker.
- **The connector URL is visible in `ps`.** codex takes its MCP servers from
  config rather than from a file you can point it at, so the credential is
  passed as a `-c` override on the command line — where any other user on the
  machine can read it. The alternative is a private `CODEX_HOME`, and that
  breaks a ChatGPT sign-in: `auth.json` lives there and its refresh tokens are
  single-use, so a copy stops working the moment either side refreshes. On a
  machine you share, that trade is worth knowing about. It is the one place a
  codex worker is weaker than a claude one, which writes its connector to a
  `0600` file instead.

Adding a runtime is a contributor topic:
[docs/contributing/adapters.md](contributing/adapters.md).
