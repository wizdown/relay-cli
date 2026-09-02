# relay-cli

[Relay](https://relay.bytecurio.com/) is a task board where you delegate work
to agents. `relay-cli` runs those agents on your machine. Each worker polls
Relay for tasks delegated to one agent, runs a headless coding CLI
([Claude Code](https://claude.com/claude-code) or the
[Codex CLI](https://developers.openai.com/codex/cli)) in a directory you
choose, and hands the result back to Relay for review. A local dashboard shows
every run as it happens.

Polling runs no model and costs nothing. A CLI session starts only when a task
is waiting. Spend is bounded by default: 12 runs per hour, $5 per run on
claude, and a 15-minute kill on either runtime.

**Beta, 0.x.** Configuration may change between releases. See
[Versioning](docs/cli.md#versioning).

## Requirements

- A Relay workspace. Sign in with Google or Microsoft; the free tier is enough.
- Claude Code or the Codex CLI, installed and signed in (`claude auth login` or
  `codex login`). relay-cli bundles no CLI and launches yours as you.
- macOS on Apple Silicon for the published binary. Other platforms build from
  source with Go 1.22+. Windows is untested.

## Install

Download `relay-<version>-macos-arm64` and `SHA256SUMS` from the
[latest release](https://github.com/wizdown/relay-cli/releases/latest), then:

```bash
shasum -a 256 -c SHA256SUMS --ignore-missing
chmod +x relay-*-macos-arm64
xattr -c relay-*-macos-arm64                    # the build is unsigned
sudo mv relay-*-macos-arm64 /usr/local/bin/relay
```

If macOS still refuses to run it, allow it once under System Settings →
Privacy & Security → **Open Anyway**. With `gh` signed in,
`gh release download --repo wizdown/relay-cli --pattern 'relay-*-macos-arm64' --pattern SHA256SUMS`
fetches both files.

From source:

```bash
git clone https://github.com/wizdown/relay-cli.git && cd relay-cli
make build && sudo mv relay /usr/local/bin/
```

## Quickstart

**1. Create an agent in Relay.** In your workspace, add an agent, issue it a
credential, and copy the connector URL. The URL contains the secret and is
shown once. Leave the agent's capabilities off for now. The
[Relay docs](https://relay.bytecurio.com/) cover the steps.

**2. Write the config.**

```bash
relay init
```

This writes `~/.relay/config` with one worker per coding CLI found on your
`PATH`. Replace the two placeholders in each worker:

```jsonc
"relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME",  // the connector URL
"repo_dir":  "/path/to/your/repo",                                    // where the agent works
```

`repo_dir` can be an empty directory. A run is autonomous and cannot ask you
before changing files, so point it at a checkout you are willing to have
rewritten.

**3. Check, then run.**

```bash
relay check   # validates the config and tests every credential. Spends nothing
relay run     # starts every worker and opens http://127.0.0.1:7717/
```

**4. Delegate a task** to the agent in Relay. Within one poll interval the
terminal shows the run:

```text
14:22:08  worker-claude   poll  resume 0 · attention 0 · todo 1
14:22:08  worker-claude   ▶ run started   claude · ~/code/scratch
14:22:11  worker-claude   → relay:claim_task   task_id=42
14:23:02  worker-claude   ■ run ok   status 0 · $0.09 · 5 turns · 54.1s
```

The result is in `repo_dir` and the task is waiting in Relay for review. An
idle worker prints nothing.

## Stopping and pausing

Ctrl-C stops every worker and archives logs to `~/.relay/logs/`. To pause one
worker without stopping the rest:

```bash
touch ~/.relay/state/worker-claude/PAUSED   # pause
rm ~/.relay/state/worker-claude/PAUSED      # resume
```

## Documentation

| | |
| --- | --- |
| [Configuration](docs/configuration.md) | Every config field and default, safeguards, running several workers |
| [The working directory](docs/working-directory.md) | What `repo_dir` gives the agent: `CLAUDE.md`, skills, subagents, settings |
| [Commands and the dashboard](docs/cli.md) | Commands, flags, what the dashboard shows, versioning |
| [Runtimes](docs/runtimes.md) | Claude Code and Codex: what each run does and what bounds it |
| [Troubleshooting](docs/troubleshooting.md) | Symptom → fix |

`relay help` prints the same reference from the binary.

## Contributing

The repo is public so you can read it, run it, and file issues. Pull requests
are not open yet. Contributor notes are in [AGENTS.md](AGENTS.md) and
[docs/contributing/](docs/contributing/).

## Security

Every connector URL is a live credential. Never commit `~/.relay/config`. See
[SECURITY.md](SECURITY.md) for handling rules and how to report a
vulnerability.

## License

[Apache License 2.0](LICENSE).
