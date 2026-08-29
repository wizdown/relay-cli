# Runtimes

A **runtime** is which coding CLI drives a worker. It is a per-worker field:

```json
{ "name": "wizhub-claude", "mcp_endpoint": "…", "runtime": "claude" }
```

## Adapters ship; CLIs do not

`relay-cli` contains no coding CLI. What it contains is an *adapter* — the code
that builds an argv and parses what the CLI prints. The CLI itself is a separate
program you install, found on `PATH` at startup.

| `runtime` | Support | Adapter | CLI you install |
|---|---|---|---|
| `claude` | **Supported** | compiled into the binary | [Claude Code](https://claude.com/claude-code) |
| `codex` | **Coming soon** | written, not enabled | — |
| anything else | Not offered | — | — |

**`claude` is the only runtime offered today.** It is the one with a compiled-in
adapter, a per-run spend cap, and the streamed session feed the dashboard is
built around. `"runtime"` defaults to it, so most configs never set the field.

`codex` is **coming soon, and refused until it arrives.** The adapter machinery
for it exists in the binary and is tested, but it is unverified against current
codex builds and has no per-run dollar cap — and a runtime that cannot be bounded
is not one this ships quietly. A worker asking for it is stopped when the config
loads, by name:

```text
error: worker "wizhub-codex" asks for runtime "codex", but claude is the only
       supported runtime today. Codex support is coming soon.
       Set "runtime": "claude" — it is also the default, so the field can be dropped.
```

That refusal is at **load**, not mid-run: a runtime you cannot use should cost
you a startup error, not a session you have already paid for.

## Every runtime is checked before anything launches

A failure stops the whole start:

```text
error: worker "wizhub-claude" cannot run: runtime "claude" is unusable.
       claude not found on PATH — install Claude Code (https://claude.com/claude-code).
       relay-cli does not bundle any CLI; each one is installed separately.
```

For `claude` the check goes further than "is it there". The adapter depends on
specific flags — streaming output, the MCP config, `--strict-mcp-config`, the
spend cap — so it reads the installed CLI's own `--help` and confirms each one is
offered:

```text
error: worker "w" cannot run: runtime "claude" is unusable.
       the installed claude (1.0.3 (Claude Code) at /usr/local/bin/claude) does not support what this poller needs.
       Its --help does not offer:
         --strict-mcp-config          keeping the operator's personal MCP servers out of an unattended run
         --max-budget-usd             the per-run spend cap
         --output-format stream-json  the live session feed — the reason relay-cli exists
       Upgrade Claude Code (https://claude.com/claude-code), then try again.
```

It checks **capabilities rather than a version number** deliberately. The adapter
does not depend on a version, it depends on those flags; a version gate would
have to encode a guess about which release introduced each one, and would block
working installs when it guessed high. Reading `--help` tests the actual
requirement.

`RELAY_CLI_SKIP_RUNTIME_CHECK=1` bypasses the flag check (not the
does-it-exist check) if a future CLI reshapes its help text — each session then
fails on the missing flag instead, once per run.

## The claude adapter

`claude -p` with `--strict-mcp-config`, so an unattended run sees relay and
nothing else from your personal MCP config.

Headless mode can never answer an approval prompt, so any tool not pre-allowed is
silently denied. The run uses `--permission-mode auto` — which biases toward
acting when nobody is present to approve, unlike `acceptEdits`, which only covers
file edits — and every relay tool is additionally named in `--allowedTools`, so
relay access never rides on the model's judgement about an unfamiliar tool.

Pass `--permission-mode` yourself via `runtime_args` and the adapter emits none
of its own.

It supports `max_budget_usd`, and reads its result envelope to report a budget
kill or a permission denial in plain words rather than as a bare exit code.

## The extension point, and where it stands

A second adapter kind exists in the binary and does not run. `bashRuntime` drives
a `runtimes/<name>.sh` through a small shell contract, and it is complete, tested
and switched off by `bashAdaptersEnabled` in `runtime.go`. It is not dead code —
it is the half of codex support that is already written.

While that constant is false, no `runtimes/` directory ships and no runtime but
`claude` resolves. Enabling one means two things: flipping the constant, and
restoring an adapter for it. Both are deliberate, because "supported" here means
verified against a real CLI and given a spend bound — not merely that an argv can
be built.

For reference, an adapter is sourced (not executed) and defines two functions:

```sh
runtime_check()      # 0 if the CLI is usable here; else print a one-line fix hint
runtime_build_cmd()  # populate RUNTIME_CMD=( … ) with the exact argv to run
```

Optionally a third:

```sh
runtime_classify_exit()  # say what a finished run's exit MEANT
```

relay-cli knows a run exited 1; only the adapter knows whether that was a spend
cap, a bad model name, or ordinary failure. Given the status and the run's
output, it sets `RUN_OUTCOME` (`ok` · `timeout` · `error` · `budget_exhausted`)
and a human `RUN_EXPLANATION` for the log. Omit it and every non-zero exit is
reported generically.

An adapter builds an argv array instead of running the command itself, so
relay-cli can apply cwd and the timeout uniformly, and so you can inspect what a
runtime *would* run without spending a token.

### The adapter contract

The adapter receives everything it needs as exported environment variables. The
full contract is documented on `bashAdapterEnv` in `cmd/relay-cli/runtime.go`;
this is the summary:

| Variable | From |
| --- | --- |
| `RELAY_CONNECTOR_URL` | the worker's `mcp_endpoint` |
| `INSTANCE_NAME` | the worker's `name` |
| `REPO_DIR` | `repo_dir` — relay-cli has already `cd`'d there |
| `WORKER_DIR` | `live-workers/<name>/` |
| `WORKER_MODEL` | `model` |
| `MAX_BUDGET_USD` | `max_budget_usd` (`0` disables) |
| `RUNTIME_ARGS` | `runtime_args` |
| `WORKER_PROMPT` | the built prompt |
| `WORKER_RULES` / `WORKER_RULES_FILE` | the harness contract, as text and as a path |
| `RELAY_ALLOWED_TOOLS` | relay's whole agent tool surface, space-separated |

Note what is *not* there: an agent's identity. That is its relay
`instructions_md`, which reaches it over MCP. See [Agents and fleets](fleets.md).

`RELAY_ALLOWED_TOOLS` must list relay's whole agent surface. A tool missing there
is denied by the CLI, silently, after relay has already offered it to the agent.

### Why shell functions rather than a table of flags

The CLIs differ in shape, not just spelling. `claude` takes an MCP config file
plus a system-prompt flag; codex takes TOML config overrides, has no
system-prompt flag, and may need an `mcp-remote` stdio bridge depending on the
build. A flag table in `.worker-config` can't express that; fifteen lines of
shell can.

Runtime-specific behaviour belongs in the adapter. If you find yourself adding an
`if [ "$RUNTIME" = … ]` to the worker loop, it belongs in an adapter instead.
