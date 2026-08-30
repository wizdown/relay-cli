# relay-cli

`relay-cli` spins up agents on your machine that work on tasks you create in
[Relay](https://relay.bytecurio.com/) and delegate to them. Each one polls relay
for its delegated work, launches a headless coding CLI in a directory on your
machine to do it, and hands the result back for review — with a live dashboard
while it runs.

```
one worker = one relay agent identity × one directory × one CLI runtime
```

Polling costs nothing. Every `poll_seconds` a worker asks relay over plain HTTP,
**with no model running**, whether it has a task; a CLI session starts only if
the answer is yes. An idle worker costs one HTTP handshake and zero tokens.

**Beta, and 0.x.** Spend is bounded by default, but configuration and the worker
contract may still change between releases — see [Versioning](#versioning).
[Claude Code](https://claude.com/claude-code) is the only supported runtime
today; no CLI is bundled.

## Prerequisites

- A [Relay](https://relay.bytecurio.com/) workspace, where your tasks and agents
  live. Sign in with Google or Microsoft; the free workspace is enough.
- [Claude Code](https://claude.com/claude-code) on your `PATH`.
- Nothing else — one static binary. Go 1.22+ only to build it yourself.

## Install

**macOS, Apple Silicon:**

```bash
gh release download --repo wizdown/relay-cli \
  --pattern 'relay-*-macos-arm64' --pattern 'SHA256SUMS'
shasum -a 256 -c SHA256SUMS --ignore-missing    # verify before running
chmod +x relay-*-macos-arm64
xattr -c relay-*-macos-arm64                    # unsigned build
sudo mv relay-*-macos-arm64 /usr/local/bin/relay
```

Without `gh`, download both files from the
[latest release](https://github.com/wizdown/relay-cli/releases/latest). If
macOS still refuses the binary, allow it once in System Settings → Privacy &
Security → **Open Anyway**.

**Intel Mac or Linux** — no binaries published yet; build it with Go 1.22+:

```bash
make build && sudo mv relay /usr/local/bin/
```

## Quickstart

### 1. Create the agent in relay

A worker authenticates as one relay agent and works whatever is delegated to
that agent. In your [Relay](https://relay.bytecurio.com/) workspace: add an
agent, give it a description and instructions, then issue a credential and copy
the `connector_url` — the secret is in the URL and is shown **once**.

Never point two workers at one connector URL. What an agent is *for* — its
instructions, capabilities and claim limits — is configured in relay, not here.

### 2. Write the config

```bash
relay init
```

That writes `~/.relay/config`. Replace its two placeholders:

```jsonc
{
  "workers": [
    {
      "name":      "hello-claude",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_…",  // ← paste yours
      "repo_dir":  "~/code/scratch",                               // ← choose one
      "runtime":   "claude",
      "runtime_config": { "model": "sonnet" }
    }
  ]
}
```

- **`repo_dir` is what the agent gets** — that directory's `CLAUDE.md`, skills
  and tooling. An empty one is a valid start:
  [The working directory](docs/working-directory.md) is the ladder from there.
- **Point it somewhere you are willing to have rewritten** — a headless run is
  autonomous and can never answer an approval prompt.
- **Everything else defaults, and every default is bounded** — 12 runs/hour, $5
  per run, a 15-minute kill, a poll every 30s.
  [Configuration](docs/configuration.md) has the rest.

### 3. Check it, then run it

```bash
relay check
```

Validates the config and tests every credential. It launches nothing and spends
nothing, so it is the cheap way to find a typo or a revoked credential:

```text
relay 0.1.0 (beta) — checking 1 worker(s) from /Users/you/.relay/config
  runtime claude   2.1.250 (Claude Code) /Users/you/.local/bin/claude

  hello-claude             ok    queue: resume 0 · attention 0 · todo 0
    repo /Users/you/code/scratch   nothing to load — the agent arrives with its task and its tools
```

The second line is what that directory gives the agent. "Nothing to load" is a
valid answer — see [The working directory](docs/working-directory.md) for what
you can add to it, and what each thing buys you.

```bash
relay run
```

Starts every worker and opens a dashboard at `http://127.0.0.1:7717/`. Ctrl-C
stops the workers, archives logs to `~/.relay/logs/`, and clears
`~/.relay/state/`.

### 4. Delegate a task

Create a task in relay and delegate it to the agent from step 1. Within one poll
interval the terminal shows the whole cycle:

```text
14:22:08  hello-claude   poll  resume 0 · attention 0 · todo 1
14:22:08  hello-claude   ▶ run started   claude · ~/code/scratch
14:22:11  hello-claude   → relay:claim_task   task_id=42
14:22:31  hello-claude   → Write   hello.html
14:23:02  hello-claude   ■ run ok   status 0 · $0.09 · 5 turns · 54.1s
```

The result is in your `repo_dir`, and the task is waiting in relay for review.
An idle worker is *deliberately* silent — "no output" is the healthy steady
state, not a symptom.

## Safeguards

Defaults are bounded without configuring anything: **12 runs/hour** (the ceiling
that actually caps spend), **$5 per run**, a **15-minute** wall-clock kill, a
fixed **60s** relaunch cooldown, and three circuit breakers that pause a worker
rather than let it fail forever. [Safeguards in
full](docs/configuration.md#safeguards).

The kill switch, worth knowing before you need it:

```bash
touch ~/.relay/state/hello-claude/PAUSED   # stop it next tick
rm ~/.relay/state/hello-claude/PAUSED      # resume it
```

## Documentation

| | |
| --- | --- |
| [Configuration](docs/configuration.md) | Every config field, per runtime, with defaults — and what changes with more than one worker |
| [The working directory](docs/working-directory.md) | What a `repo_dir` gives the agent: `CLAUDE.md`, skills, subagents, settings |
| [Commands & dashboard](docs/cli.md) | Commands, flags, and what the page shows |
| [Runtimes](docs/runtimes.md) | Supported CLIs, and where codex stands |
| [Troubleshooting](docs/troubleshooting.md) | Symptom → where to look |

## Versioning

This is **0.x** and stays there until the interface settles. A release may
change configuration, defaults or the worker contract; changes are called out in
the release notes, and a removed config key is rejected by name with its
replacement rather than silently ignored. A 1.0 will mean the interface is
settled, not that time has passed.

**Public contributions are not open yet.** The repo is public so you can read
it, run it, and see what it does. Issues are welcome; pull requests will be once
there is a version worth building on.

## Security

Every relay connector URL is a live credential with the secret embedded. Never
commit a relay-cli config. See [SECURITY.md](SECURITY.md) for the handling rules
and how to report a vulnerability.

## License

[Apache License 2.0](LICENSE).
