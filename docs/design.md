# Design

## Why the probe exists

The obvious version of this design — wake the CLI on a schedule and let it check
whether there's work — is the expensive one, because **the empty queue is the
common case and it's the case that pays full price.**

Before a CLI can call `get_available_tasks` it has to load its whole context:

| loaded every cycle | ≈ tokens |
|---|---|
| relay's agent-surface MCP tool schemas | **45,000** (24 tools, 166,279 bytes of tool definitions as served — measured 2026-08-28) |
| the CLI's own system prompt + built-in tools | ~10,000 |
| the worker rules | ~400 |

That's ~55k tokens of context, twice per cycle (decide → tool result →
conclude), to learn there is nothing to do. At a 30-second tick that is roughly
**6.6M input tokens an hour, per worker, while idle** — and it scales with the
number of workers, not with the amount of work.

Re-measure that first row rather than trusting it. It was 84,344 bytes when this
was written and roughly doubled inside a fortnight, because relay's fan-out
tools arrived with descriptions that teach a whole workflow. The number moves in
one direction, and always in the probe's favour.

So the probe asks the same question over plain HTTP: MCP JSON-RPC straight at the
connector endpoint, no model anywhere. An idle worker costs one short HTTP
handshake per tick and not a single token. A CLI is launched only once the queue
comes back non-empty, which means **the cost of the system tracks the work you
actually filed.**

The probe sits *below* the runtime adapters, so every runtime — including ones
you add later — inherits the gate for free.

## How a cycle runs

Each worker ticks every `poll_seconds`. A tick does this, in order:

1. **`PAUSED` file** — present? do nothing this tick.
2. **Lock** — take the worker's `mkdir` mutex. One cycle per worker at a time; a
   lapsed relay lease would otherwise re-offer a task while its session is still
   running.
3. **Ceilings** — hourly run ceiling, relaunch cooldown. If either forbids
   launching, stop here. The probe is skipped, because there is no point asking a
   question you can't act on — which is why a throttled worker is *cheaper* than
   an idle one, never more expensive.
4. **Probe** — one MCP call, no model. Three buckets come back: `resume`,
   `attention`, `todo`.
5. **Launch** — only if some bucket is non-empty. One headless CLI session,
   inside `repo_dir`, under the wall-clock timeout and the spend cap.

The worker owns the working directory and the timeout for every runtime, so a
stuck session can never hold its lock — or the task's relay lease — open
indefinitely.

## Who owns what

**Relay owns the task state machine entirely**: `todo → in_progress →
in_review/blocked → done`, with a `claim_seq` fencing token on every mutating
call. It also owns the agent roster, the delegation graph, and the attempt cap
that forces a repeatedly-failing task to Blocked instead of letting it loop.

**Nothing in this repo tracks task state.** A worker decides one thing only: when
it is safe and worthwhile to start another cycle.

That boundary is why the config here is so small. Anything about *what an agent
is for* is on the agent in relay; anything about *when and where a CLI runs* is
here.

## The worker rules

`worker-rules.md` is the **runtime contract** — what is true of this harness and
only this harness: one task per session, no memory across turns,
carry `claim_seq`, and end the session still holding a task whose subtasks are
still working.

It is runtime-neutral and the same text for every CLI. `claude` gets it via
`--append-system-prompt`, layered on top of its own system prompt rather than
replacing it.

The binary embeds a copy so a downloaded `relay` works on a machine holding
nothing but itself, and prefers a `worker-rules.md` sitting in `~/.relay/` when
there is one, so editing the rules takes effect without rebuilding.

**The relay workflow is deliberately not in it.** Relay serves that as its MCP
server's `instructions` at connect, and each agent's own `instructions_md`
arrives with it and is repeated in every `get_task_context` — so it reaches an
agent mid-session and stays correct when relay changes. A copy in this repo would
only be a second, staler source of truth. Resist growing the rules file back into
a copy of the playbook.

## Repo context

A worker is one relay agent identity × one repo × one CLI. Set `repo_dir` and the
CLI starts inside that checkout, so the repo's `AGENTS.md` / `CLAUDE.md`, skills
and tooling load exactly as they would for you.

That makes delegation the routing decision: the relay agent you hand a task to
determines which repo it runs in and which CLI does the work. There's no mapping
table to keep in sync, and a per-repo credential's queue can only ever contain
that repo's tasks.

`repo_dir` is required: it decides which repo's `AGENTS.md` / `CLAUDE.md`, skills
and tooling the agent inherits, which is what the field is *for*. There is no
default because there is no safe one — an agent pointed somewhere arbitrary is an
agent working without any of it.

## Directory layout

```text
relay-cli/                       # the repository
  readme.md
  worker-rules.md                # the runtime contract (relay owns the workflow)
  docs/                          # this documentation
  Makefile
  cmd/relay-cli/                 # the binary: runs the fleet, serves the dashboard
    main.go                      # commands, flags, supervisor, startup checks, archiving
    config.go                    # config parse + validation, problems accumulated
    probe.go                     # MCP JSON-RPC over net/http — the token-free gate
    worker.go                    # the poll loop: ceilings, breakers, timeouts
    runtime.go                   # adapter interface + the gated bash-adapter bridge
    runtime_claude.go            # native claude adapter, stream-json parsing
    events.go                    # event bus → worker.log, events.ndjson, SSE
    server.go                    # /api/snapshot, /api/stream, embedded page
    ui/index.html                # the dashboard, one self-contained file
    assets/worker-rules.md       # embedded copy; the on-disk one wins when present
```

And in `~/.relay/`, which is not this repository and is the only place relay-cli
keeps anything:

```text
~/.relay/
  config
  worker-rules.md                # optional: overrides the embedded copy
  logs/                          # archived on shutdown
  state/                         # created at start, removed on shutdown
    <name>/
      mcp.json                   # generated, holds the connector secret (0600)
      lock/                      # mkdir mutex, present only mid-cycle
      runs.log                   # launch timestamps, backing the hourly ceiling
      last-run.out               # the last run's output, read by the exit classifier
      probe-failures             # consecutive probe failures, for the breaker
      budget-kills               # consecutive spend-cap kills, for the breaker
      attention-stall            # "<task ids> <count>", for the stall breaker
      PAUSED                     # kill switch, if present
      worker.log                 # the human log
      events.ndjson              # the structured record, one JSON object per line
    relay-cli.pid
```
