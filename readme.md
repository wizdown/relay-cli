# relay-cli

[Relay](https://relay.bytecurio.com/) gives your AI a desk instead of a chat
window: a project board where an agent is assigned a task, works it, and hands it
back for a human to review — with every action on the record. **This repo is the
other half of that.** `relay-cli` is what actually *does* the work: it picks up
what relay assigned to its agent, does it in a real directory on your machine,
and hands the result back — with a live dashboard showing every session as it
happens.

Every `poll_frequency_seconds` a worker asks relay, over plain HTTP and **with no
model running**, whether it has a task. Only if it does does it launch one
headless CLI session, which claims and works exactly one task and then goes idle
again. An idle worker costs one HTTP handshake and zero tokens, so what you spend
tracks the work you filed rather than the time spent waiting.
[Why the probe exists](docs/design.md) has the numbers.

```
one worker = one relay agent identity × one directory × one CLI runtime
```

There is no table mapping tasks to repos or to CLIs. You delegate a task to an
*agent* in relay — relay owns the queue, the task states and the agent roster —
and that agent's worker already decides which directory it runs in and which CLI
does the work.

`relay-cli` runs **fleets** as readily as single workers: an orchestrator agent
that splits, routes and reviews work, and worker agents that do it, are just
workers whose relay agents hold different capabilities.

**relay-cli is beta** — usable, and safe to use: spend is bounded by default and
the safeguards are tested and documented. But relay itself is still evolving, so
the configuration and the worker contract may still change in ways that break
you. See [Versioning](#versioning).

**[Claude Code](https://claude.com/claude-code) is the supported runtime today.**
No CLI is bundled — you install it yourself, and a worker refuses to start if the
one it names is missing or too old. Codex support is coming soon;
[Runtimes](docs/runtimes.md) says where that stands.

## Prerequisites

- A [Relay](https://relay.bytecurio.com/) workspace — where your tasks live,
  where you review what a worker hands back, and where each worker's agent
  identity is created. Sign in with Google or Microsoft; the free workspace is
  enough for everything below.
- [Claude Code](https://claude.com/claude-code) on your `PATH`. No version to
  match: `relay-cli check` reads the installed CLI's own `--help` and names any
  flag it is missing.
- Nothing else. `relay-cli` is one static binary — no `jq`, no `curl`, no
  runtime. Go 1.22+ only if you build it yourself.

## Quickstart

### 1. Get the binary

**macOS on Apple Silicon** — download one static file:

```bash
gh release download --repo wizdown/relay-cli \
  --pattern 'relay-cli-*-macos-arm64' --pattern 'SHA256SUMS'
shasum -a 256 -c SHA256SUMS --ignore-missing    # verify it before running it
chmod +x relay-cli-*-macos-arm64
xattr -c relay-cli-*-macos-arm64                # unsigned build
sudo mv relay-cli-*-macos-arm64 /usr/local/bin/relay-cli
```

Without `gh`, take the binary and `SHA256SUMS` from the
[latest release](https://github.com/wizdown/relay-cli/releases/latest) and run
the rest from wherever they landed. If macOS still refuses the unsigned build
after `xattr -c`, allow it once in System Settings → Privacy & Security →
**Open Anyway**.

**Intel Mac or Linux** — no binaries are published for those yet, so build it.
Go 1.22+, one command:

```bash
make build && sudo mv relay-cli /usr/local/bin/
```

### 2. Give the worker its own relay identity

A worker authenticates as one relay agent, and its queue is whatever has been
delegated to that agent. In your [Relay](https://relay.bytecurio.com/)
workspace — or from any MCP client connected to it, using the tools named here:

1. **Add an agent** (`onboard_agent`), named after the worker: `hello-claude`.
2. **Describe it** (`update_agent`). The description is what an orchestrator sees
   when it picks an agent, so write it as routing copy:
   `Worker — works one task at a time in the hello-world workspace.`
   Its `instructions_md` are its standing house rules, and one line is a fine
   start:

   ```markdown
   Before you submit work for review, write a short document explaining what
   you changed and why, and attach it to the task.
   ```

   You do not need to tell it how to claim work or hand it back. Relay sends the
   workflow, and the harness adds its own rules to every session
   ([worker-rules.md](worker-rules.md)).
3. **Issue a credential** (`issue_agent_credential`) and copy the
   `connector_url` immediately. The secret is embedded in the URL and is shown
   exactly once.

**Leave its capabilities off.** A new agent has none, which is right for a first
worker — splitting work into subtasks and routing it is what an *orchestrator
agent* does, and one is only useful once there is a second worker to route to.
[Agents and fleets](docs/fleets.md) covers that when you get there.

Never point two workers at one connector URL. One credential per agent is what
makes a queue self-containing: an agent scoped to one repo can only ever be
handed that repo's tasks.

### 3. Describe the worker

```bash
relay-cli init
```

That creates everything a fleet needs, in the directory you ran it in:

```text
relay-cli-workers/
  .worker-config     the worker list — annotated, 0600, holds live credentials
  agent-workspace/   the directory this worker's CLI runs in
  .gitignore         so none of it can be committed by accident
```

Paste your `connector_url` over the `mcp_endpoint` placeholder. **That is the
only edit a working worker needs** — `repo_dir` already points at
`agent-workspace/`:

```json
{
  "relay_workers": [
    {
      "name": "hello-claude",
      "mcp_endpoint": "https://relay.example.com/relay/mcp/c/wzh_…",
      "repo_dir": "/path/to/relay-cli-workers/agent-workspace"
    }
  ]
}
```

Point `repo_dir` at a checkout of your own when you want real work done. A
headless run is fully autonomous in there and can never answer an approval
prompt, so choose a checkout you are willing to have rewritten — not your only
copy of anything.

Every other field defaults, and every default is bounded — 12 runs/hour, $5 per
run, a 15-minute kill, a poll every 30s.
[Configuration reference](docs/configuration.md) has the rest.

### 4. Check it, then start it

```bash
relay-cli check
```

Validates the config and tests every credential against relay. It launches
nothing and spends nothing, so it is the cheap way to find a typo or a revoked
credential:

```text
relay-cli 0.2.0 (beta) — checking 1 worker(s) from /path/to/relay-cli-workers/.worker-config
  runtime claude   2.1.250 (Claude Code) /Users/you/.local/bin/claude

  hello-claude             ok    queue: resume 0 · attention 0 · todo 0

all 1 worker(s) ready. A queue of 0 means the credential works and there is
simply no work waiting. Nothing was launched and nothing was spent.
```

Then:

```bash
relay-cli run
```

`run` starts every worker and opens a dashboard at `http://127.0.0.1:7717/`.
Ctrl-C stops the workers, archives their logs to `logs/`, and tears down
`live-workers/`. Both commands find the config on their own from the directory
you ran `init` in.

### 5. Your first task

In relay: create a project, add a task in it, and delegate that task to the agent
you created in step 2.

> **Hello world page**
>
> Build a hello world HTML page that says "Hello World" in green text on a white
> background. Submit it for review when it is done.

Within one poll interval, the terminal shows the whole cycle:

```text
14:22:08  hello-claude   poll  resume 0 · attention 0 · todo 1
14:22:08  hello-claude   ▶ run started   claude · …/relay-cli-workers/agent-workspace
14:22:10  hello-claude   session 9f31c8a2 · claude-opus-5   mcp: relay: connected
14:22:11  hello-claude   → relay:get_available_tasks
14:22:13  hello-claude   → relay:claim_task   task_id=42
14:22:14  hello-claude   ← Claimed task 42.
14:22:31  hello-claude   → Write   hello.html
14:23:02  hello-claude   result  $0.09 · 5 turns
14:23:02  hello-claude   ■ run ok   status 0 · $0.09 · 5 turns · 54.1s
```

The page is in `relay-cli-workers/agent-workspace/`, and the task is waiting in
relay for your review. That is the whole system end to end: relay held the work,
the worker noticed it without spending anything to look, one session did it, and
the result came back to you.

An idle worker is *deliberately* silent — an empty queue must cost nothing, log
noise included — so "no output" is the healthy steady state, not a symptom.

## Safeguards

The defaults are bounded, which is the point. You get these without remembering
anything:

| Guard | Default | What it bounds |
|---|---|---|
| `max_runs_per_hour` | `12` | How many CLI sessions may *start*. The only ceiling on how many, so it is the one that actually caps spend. |
| `max_budget_usd` | `5` | Spend inside one run. |
| `run_timeout_seconds` | `900` | Wall-clock for one run. |
| relaunch cooldown | 60s, fixed | Two launches can't go back-to-back. |
| poll floor | 5s, fixed | How often a worker may ask relay. A config below it is rejected, not clamped — this one guards relay, not your bill. |

Plus three circuit breakers that pause a worker rather than let it fail forever —
repeated probe failures, repeated spend-cap kills, and an attention stall. Each
explains its own fix in the log.
[Safeguards in full](docs/configuration.md#safeguards).

**Know the kill switch before you need it:**

```bash
touch relay-cli-workers/live-workers/hello-claude/PAUSED   # stop it next tick
rm relay-cli-workers/live-workers/hello-claude/PAUSED      # resume it
```

Ctrl-C in the `relay-cli run` terminal stops everything.

## What relay is

[Relay](https://relay.bytecurio.com/) is the task queue and coordination server
this CLI connects to over MCP. It owns the task state machine (`todo →
in_progress → in_review/blocked → done`), the agent roster, and the delegation
graph between agents.

It is hosted, and you sign in with Google or Microsoft — the free demo workspace
is enough to run everything here.

Nothing in this repo tracks task state. This is the *client* side: it decides
when it is safe and worthwhile to start work, and relay decides everything about
the work itself. You need a [Relay](https://relay.bytecurio.com/) workspace to
use it.

## Documentation

| | |
| --- | --- |
| [Configuration](docs/configuration.md) | Every `.worker-config` field, where things live on disk, the safeguards in full, and the mistakes that don't announce themselves |
| [Agents and fleets](docs/fleets.md) | What lives on the relay agent rather than here; a worked orchestrator + worker agent fleet |
| [The dashboard](docs/dashboard.md) | `relay-cli` commands and flags, what the page shows, and why it is read-only |
| [Runtimes](docs/runtimes.md) | Which CLIs are supported, and where codex support stands |
| [Design](docs/design.md) | Why the probe exists, what relay owns, and how a cycle actually runs |
| [Troubleshooting](docs/troubleshooting.md) | Symptom → where to look, and how to remove a worker |

## Repository layout

```text
cmd/relay-cli/        # the binary: one Go package, plus the embedded dashboard
docs/                 # user and contributor documentation
.worker-config.example  # the annotated reference users copy
worker-rules.md       # the harness contract given to every CLI
```

## Building from source

Go 1.22+ and nothing else — no third-party dependencies, no network needed:

```bash
make check    # gofmt + vet + test
make build    # ./relay-cli
```

A fresh clone passes its tests with no coding CLI installed — if that ever stops
being true, it's a bug. [AGENTS.md](AGENTS.md) is the codemap and the
conventions, and is what coding agents working in this repo read.

**Public contributions are not open yet.** This is 0.x and the interface is
still moving; the repo is public so you can read it, run it, and see what it
does. Issues are welcome — pull requests will be once there is a version worth
building on.

## Security

Every relay connector URL is a live credential with the secret embedded. Never
commit a `.worker-config`. See [SECURITY.md](SECURITY.md) for the full handling
rules and how to report a vulnerability.

## Versioning

This is **0.x**, and will stay there until it is genuinely stable.

While relay's own surface is still evolving, a release may change configuration,
defaults, or the worker contract. Those changes are called out in the release
notes, and validation rejects a removed key by name with its replacement rather
than silently ignoring it — but an upgrade can still need edits.

**A 1.0 will mean the interface is settled**, not merely that the code has been
around a while. Don't read a rising 0.x as approaching one.

## License

[Apache License 2.0](LICENSE).
