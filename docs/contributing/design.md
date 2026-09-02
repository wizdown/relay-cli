# Design

Why relay-cli is built the way it is. The user pages say what it does; the
reasons live here.

## Why the probe exists

The obvious design, waking the CLI on a schedule and letting it check for
work, pays full price for the common case: an empty queue. Before a CLI can
call `get_available_tasks` it loads its whole context:

| Loaded every cycle | ≈ tokens |
|---|---|
| Relay's agent-surface MCP tool schemas | **45,000** (24 tools, 166,279 bytes as served, measured 2026-08-28) |
| the CLI's own system prompt and built-in tools | ~10,000 |
| the worker rules | ~400 |

That is ~55k tokens, twice per cycle, to learn there is nothing to do. At a
30-second tick it is roughly 6.6M input tokens an hour per idle worker, and it
scales with workers, not with work. The first row was 84,344 bytes when first
measured and doubled within a fortnight; re-measure it rather than trust it.

So the probe asks the same question over plain HTTP: MCP JSON-RPC at the
connector endpoint, no model anywhere. A CLI launches only when the queue
comes back non-empty, so the cost of the system tracks the work filed. The
probe sits below the runtime adapters, so every runtime inherits it.

## How a cycle runs

Each worker ticks every `poll_seconds`:

1. **`PAUSED` file** present? Do nothing this tick.
2. **Lock**: take the worker's `mkdir` mutex. One cycle per worker at a time,
   so a lapsed Relay lease cannot re-offer a task whose session is still
   running.
3. **Ceilings**: hourly run ceiling and relaunch cooldown. If either forbids a
   launch, stop here without probing. A throttled worker is cheaper than an
   idle one.
4. **Probe**: one MCP call. Three buckets come back: `resume`, `attention`,
   `todo`.
5. **Launch**, only if a bucket is non-empty: one headless session inside
   `repo_dir`, under the wall-clock timeout and the spend cap.

The worker owns the working directory and the timeout for every runtime, so a
stuck session can never hold its lock, or the task's lease, indefinitely.

## Who owns what

Relay owns the task state machine (`todo → in_progress → in_review/blocked →
done`), the `claim_seq` fencing token on every mutating call, the agent
roster, the delegation graph, and the attempt cap that forces a repeatedly
failing task to Blocked. Nothing in this repo tracks task state. A worker
decides one thing: when it is safe and worthwhile to start another cycle.

That boundary is why the config is small. Anything about *what an agent is
for* is on the Relay agent; anything about *when and where a CLI runs* is
here. `system_prompt`, `permission_mode` and three other keys were removed for
crossing that line, and are rejected by name.

`repo_dir` is required because it decides which repo's `CLAUDE.md`, skills
and tooling the agent inherits. There is no safe default. `model` is required
because each CLI's own default moves between versions, and an unattended
process that spends money should say what it runs. Aliases are pinned to one
id for the same reason: in the CLIs a bare `sonnet` means "the latest Sonnet",
which is a config that changes itself.

## The worker rules

`worker-rules.md` is the runtime contract: what is true of this harness and
only this harness. One task per session, no memory across turns, carry
`claim_seq`, and end the session still holding a task whose subtasks are
working. It is the same text for every runtime. `claude` receives it through
`--append-system-prompt`; `codex exec` has no such flag, so it rides at the
top of the prompt instead, because a contract that silently fails to arrive is
a worker that claims two tasks and tells nobody.

The binary embeds a copy so a downloaded `relay` works alone, and prefers
`~/.relay/worker-rules.md` when one exists. The Relay workflow is deliberately
not in it: Relay serves that as its MCP server's `instructions`, and each
agent's `instructions_md` is repeated in every `get_task_context`, so it
reaches an agent mid-session and stays correct when Relay changes.

## The startup check

`Check()` proves a CLI is installed, accepts the flags the adapter uses, and
is signed in. It reads the CLI's `--help` rather than gating on a version
number: the adapter depends on flags, not on a release, and a version gate
blocks working installs whenever it guesses high. Sign-in is asked from stored
credentials (`claude auth status --json`, `codex login status`) because that
spends nothing, which is what makes it a startup check rather than something
the first paid run discovers.

Two cases warn instead of failing, because a check that refuses a fleet which
would have worked is worse than no check: a CLI too old to be asked, and a
credential in the environment, which authenticates a run whatever the stored
sign-in says. Both warn out loud, since a stale key hiding a signed-out CLI is
exactly what the check exists to catch. A healthy start stays silent.

`relay init` asks a weaker question, `Installed()`: is the CLI on `PATH` at
all? That decides whether a runtime's worker is written live or commented
out, and it is not `Check()` on purpose: a signed-out CLI is installed, and
telling someone to install what they have is the wrong fix.

## The codex connector trade

A codex worker receives its connector URL as a `-c` config override on the
command line, visible in `ps` to other users on the machine. The isolated
alternative is a private `CODEX_HOME`, and that breaks a ChatGPT sign-in:
`auth.json` lives there and its refresh tokens are single-use, so a copy stops
working the moment either side refreshes. Working sign-in beat the smaller
exposure. Everything else about that URL still goes through `Scrub`.

## Why both shipped adapters are native

Only a native adapter can parse its CLI's event stream into session events,
which is what the dashboard shows, and only a native one can declare
`ConfigFields()`, without which a worker cannot be told which model to run.
The bash bridge exists for a CLI nobody here has written an adapter for. It
gives that CLI an argv and nothing else, so it is the fallback, not the
pattern.

## Directory layout

```text
relay-cli/
  readme.md
  CHANGELOG.md
  worker-rules.md                the runtime contract, editable copy
  docs/                          user documentation
  docs/contributing/             this directory
  scripts/release.sh             everything `make release` does
  .githooks/                     pre-commit and commit-msg, sharing lib.sh
  .github/workflows/             ci.yml (manual) and release.yml (tags)
  cmd/relay-cli/                 one Go package
    main.go                      commands, flags, supervisor, startup checks, log
                                 archiving, the version constant, shortHelp, helpText
    init.go                      `relay init` and the starting config it writes
    config.go                    parse, defaults, validation; problems accumulated
    probe.go                     MCP JSON-RPC over net/http, the token-free gate
    worker.go                    the poll loop: ceilings, breakers, locking, timeouts
    runtime.go                   the Runtime interface, runtimeField, the bash bridge
    runtime_claude.go            native claude adapter
    runtime_codex.go             native codex adapter
    events.go                    event bus → worker.log, events.ndjson, SSE
    server.go                    /api/snapshot, /api/stream, the embedded page. Read-only
    redact.go                    Scrub and RedactURL
    awake.go                     --keep-awake: the macOS power assertion
    ui/index.html                the dashboard, one file
    assets/worker-rules.md       the embedded copy of the contract
    docs_test.go                 the config reference is held to the code
    docs_pages_test.go           links resolve, cli.md is complete, versions exist, pages stay short
    doclinks_test.go             every doc link the binary prints is a full URL
    docs_lint_test.go            the prose is held to the rules in AGENTS.md
```

And `~/.relay/`, the only place relay-cli keeps anything:

```text
~/.relay/
  config
  worker-rules.md                optional override
  logs/                          archived on shutdown
  state/                         created at start, removed on shutdown
    <name>/
      mcp.json                   generated, holds the connector secret (0600)
      lock/                      mkdir mutex, present only mid-cycle
      runs.log                   launch timestamps, backing the hourly ceiling
      last-run.out               the last run's output, read by the exit classifier
      probe-failures             consecutive probe failures
      budget-kills               consecutive spend-cap kills
      attention-stall            "<task ids> <count>"
      PAUSED                     kill switch, if present
      worker.log                 the human log
      events.ndjson              one JSON object per line
    relay-cli.pid
```
