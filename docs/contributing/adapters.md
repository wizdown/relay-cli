# Adapters

How a runtime is wired in. `docs/runtimes.md` is the user-facing half; this is
the contract behind it.

## Native adapters

`claude` and `codex` are native adapters: Go, compiled in, each implementing
`Runtime` in `runtime_claude.go` and `runtime_codex.go`. Why native is the
only shape that ships is in
[Design](design.md#why-both-shipped-adapters-are-native).

To add one, copy the shape of `runtime_codex.go`, the more recent of the two:

1. `ConfigFields()`: every setting the CLI takes, typed, defaulted and
   documented in one table. See
   [Adding a runtime setting](config-fields.md#adding-a-runtime-setting).
2. `Check()`: is the CLI installed, does this build accept the flags the
   adapter uses, and is it signed in. Read the CLI's own `--help` rather than
   gating on a version, and ask about sign-in in a way that spends nothing.
   Three answers: signed in passes, signed out fails the start, and a CLI that
   cannot be asked warns and continues. See `warnUnverifiedSignIn` and
   `envCredentialSet` in `runtime.go`.
3. `BuildCmd()`: the exact argv, with whatever spells "fully autonomous" set
   unconditionally. A headless run can never answer an approval prompt.
4. `ParseLine()`: one output line to session events. An adapter that cannot
   parse a line returns a single `raw` event, which is still live in the UI.
5. `ClassifyExit()`: what the exit meant. The loop knows a run exited 1; only
   the adapter knows whether that was a limit, a missing sign-in, or ordinary
   failure. Return `outcomeBudget` for a limit; it is the one outcome the loop
   acts on.
6. Optionally `InspectWorkdir()`, `Version()` and `Path()`, which
   `relay check` and the startup banner use.

Then add it to `supportedRuntimes()`. `ResolveRuntime` reads that list, the
docs test reads it too, and `make check` names the documentation you still
owe.

The argv is not overridable from the config. Raw arguments could silently
replace the flags a headless run depends on (`--strict-mcp-config`, the
allowlist, the spend cap), so every setting a runtime accepts is a declared
key in `ConfigFields()`.

## The bash bridge

`bashRuntime` drives a `runtimes/<name>.sh` through a small shell contract. It
is complete and tested, and switched off by `bashAdaptersEnabled` in
`runtime.go`. While that constant is false, no `runtimes/` directory ships and
only the compiled-in adapters resolve. It is the extension point for a CLI
nobody has written a native adapter for. Keep it compiling and keep its test
passing.

Enabling it means flipping the constant and restoring an adapter script.
"Supported" means verified against a real CLI and given a bound, not merely
that an argv can be built.

A script is sourced, not executed, and defines two functions:

```sh
runtime_check()      # 0 if the CLI is usable here; else print a one-line fix hint
runtime_build_cmd()  # populate RUNTIME_CMD=( … ) with the exact argv to run
```

Optionally a third:

```sh
runtime_classify_exit()  # say what a finished run's exit meant
```

Given the status and the run's output, it sets `RUN_OUTCOME` (`ok`, `timeout`,
`error` or `budget_exhausted`) and a human `RUN_EXPLANATION` for the log. Omit
it and every non-zero exit is reported generically.

The script builds an argv array rather than running the command, so relay-cli
can apply cwd and the timeout uniformly and so an argv can be inspected without
spending a token.

### Environment

The script receives everything as exported variables. The full contract is on
`bashAdapterEnv` in `runtime.go`; this is the summary:

| Variable | From |
| --- | --- |
| `RELAY_CONNECTOR_URL` | the worker's `relay_mcp` |
| `INSTANCE_NAME` | the worker's `name` |
| `REPO_DIR` | `repo_dir`; relay-cli has already `cd`'d there |
| `WORKER_DIR` | `~/.relay/state/<name>/` |
| `RUNTIME_<KEY>` | one per key declared in `ConfigFields()`, e.g. `RUNTIME_MODEL` |
| `WORKER_PROMPT` | the built prompt |
| `WORKER_RULES` / `WORKER_RULES_FILE` | the harness contract, as text and as a path |
| `RELAY_ALLOWED_TOOLS` | Relay's whole agent tool surface, space-separated |

An agent's identity is not there. That is its Relay `instructions_md`, which
reaches it over MCP.

`RELAY_ALLOWED_TOOLS` must list Relay's whole agent surface. A tool missing
there is denied by the CLI, silently, after Relay has already offered it.

## Where runtime-specific behaviour goes

The CLIs differ in shape, not just spelling: `claude` takes an MCP config file
plus a system-prompt flag; `codex` takes TOML overrides on the command line,
has no system-prompt flag, and bounds a run with a sandbox rather than an
allowlist. A flag table in the config cannot express that, and an adapter can.
If you find yourself adding `if runtime == "…"` to the worker loop, it belongs
in an adapter.
