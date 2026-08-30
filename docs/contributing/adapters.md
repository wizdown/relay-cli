# Adapters

How a runtime is wired in. `docs/runtimes.md` is the user-facing half — which
CLIs are offered and what is checked at startup; this is the contract behind it.

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

That argv is not overridable from the config, deliberately: raw arguments could
silently replace the flags a headless run depends on — `--strict-mcp-config`,
the allowlist, the spend cap. Every setting a runtime accepts is a declared key
in `ConfigFields()` instead.

### The adapter contract

The adapter receives everything it needs as exported environment variables. The
full contract is documented on `bashAdapterEnv` in `cmd/relay-cli/runtime.go`;
this is the summary:

| Variable | From |
| --- | --- |
| `RELAY_CONNECTOR_URL` | the worker's `relay_mcp` |
| `INSTANCE_NAME` | the worker's `name` |
| `REPO_DIR` | `repo_dir` — relay-cli has already `cd`'d there |
| `WORKER_DIR` | `~/.relay/state/<name>/` |
| `RUNTIME_<KEY>` | one per key the adapter declared in `ConfigFields()`, e.g. `RUNTIME_MODEL` |
| `WORKER_PROMPT` | the built prompt |
| `WORKER_RULES` / `WORKER_RULES_FILE` | the harness contract, as text and as a path |
| `RELAY_ALLOWED_TOOLS` | relay's whole agent tool surface, space-separated |

Note what is *not* there: an agent's identity. That is its relay
`instructions_md`, which reaches it over MCP. See the [relay docs](https://relay.bytecurio.com/).

`RELAY_ALLOWED_TOOLS` must list relay's whole agent surface. A tool missing there
is denied by the CLI, silently, after relay has already offered it to the agent.

### Why shell functions rather than a table of flags

The CLIs differ in shape, not just spelling. `claude` takes an MCP config file
plus a system-prompt flag; codex takes TOML config overrides, has no
system-prompt flag, and may need an `mcp-remote` stdio bridge depending on the
build. A flag table in the config can't express that; fifteen lines of
shell can.

Runtime-specific behaviour belongs in the adapter. If you find yourself adding an
`if [ "$RUNTIME" = … ]` to the worker loop, it belongs in an adapter instead.
