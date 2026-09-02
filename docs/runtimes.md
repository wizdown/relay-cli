# Runtimes

A runtime is the coding CLI that drives a worker. Set `runtime` on every
worker, plus that CLI's settings in `runtime_config`. relay-cli bundles no CLI:
install it, sign in, and relay-cli finds it on `PATH` at startup.

## Which runtimes exist

| `runtime` | CLI | Sign in with | Per-run spend cap | Reports |
|---|---|---|---|---|
| `claude` | [Claude Code](https://claude.com/claude-code) | `claude auth login` | yes: `max_usd_per_run` | dollars |
| `codex` | [Codex CLI](https://developers.openai.com/codex/cli) | `codex login` | **no** | tokens |

Any other value is rejected when the config loads. The settings each accepts
are in Configuration: [claude](configuration.md#runtime_config-for-claude) and
[codex](configuration.md#runtime_config-for-codex).

**Read the spend column before choosing.** A claude run is cut off at a dollar
figure by the CLI itself. A codex run has no such cap, so a codex worker is
bounded by `max_seconds_per_run`, `max_runs_per_hour`, `reasoning_effort` and
the plan limits of the signed-in account.

## The startup check

`relay run` and `relay check` verify every runtime the config names before
anything launches:

1. The CLI is on `PATH`.
2. Its `--help` offers the flags the adapter needs.
3. It is signed in, asked with `claude auth status --json` or
   `codex login status`. Both read stored credentials and spend nothing.

A failure stops the start and names the fix. Two cases warn instead of
failing:

- A CLI too old to answer the sign-in question.
- A credential in the environment (`ANTHROPIC_API_KEY`, `CODEX_API_KEY`, or a
  third-party provider). It authenticates a run whatever the stored sign-in
  says, and relay-cli cannot tell whether it is valid.

`RELAY_CLI_SKIP_RUNTIME_CHECK=1` skips the flag, sign-in and model checks.
`RELAY_CLI_SKIP_MODEL_CHECK=1` skips only the
[model check](configuration.md#model-names). Neither skips the check that the
CLI exists. The messages each check prints are in
[Troubleshooting](troubleshooting.md#relay-run-refuses-to-start).

## What a claude run does

- Runs with `--strict-mcp-config`: the session sees this worker's Relay
  connector and no other MCP server, including an `.mcp.json` in the working
  directory.
- Pre-allows every Relay tool and the ordinary coding tools. Anything else is
  denied without a prompt, and each refusal is listed in `worker.log` under
  "the CLI refused these tool calls".
- Passes `model` and `max_usd_per_run` as flags. No other argv is configurable.
- Appends the harness rules to the CLI's system prompt with
  `--append-system-prompt`. See
  [Harness rules](configuration.md#harness-rules).
- Reads the result envelope, so a spend-cap kill is reported in words rather
  than as an exit code.

## What a codex run does

- Runs with `--ignore-user-config`: your `~/.codex/config.toml` is not read.
  A `.codex/config.toml` inside `repo_dir` still applies.
- Is bounded by `runtime_config.sandbox` and `network_access`, not by a tool
  allowlist. The defaults (`workspace-write`, network on) let it edit the
  checkout and push a branch.
- Receives the harness rules at the top of the prompt, because `codex exec`
  has no system-prompt flag.
- Runs with `--ephemeral`, so no session recording is left behind. relay-cli
  keeps its own record in `~/.relay/logs/`.
- Reports tokens, not dollars.
- Receives the connector URL as a `-c` override on the command line, so
  another user on the same machine can read it in `ps`. A claude worker
  writes it to a `0600` file instead. The reason is in
  [Design](contributing/design.md#the-codex-connector-trade).

Adding a runtime is a contributor topic:
[Adapters](contributing/adapters.md).
