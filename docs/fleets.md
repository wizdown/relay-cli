# Fleets

A fleet is just more than one worker in `~/.relay/config`. There is no fleet
mode, no orchestrator binary, and nothing here that says which worker is which.

```jsonc
{
  "poll_seconds": 30,
  "workers": [
    {
      "name":      "orchestrator-claude",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME",
      "repo_dir":  "~/relay/orchestrator",
      "runtime":   "claude",
      "runtime_config": { "model": "opus" }
    },
    {
      "name":      "app-claude",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME_2",
      "repo_dir":  "~/code/app",
      "runtime":   "claude",
      "runtime_config": { "model": "sonnet" }
    }
  ]
}
```

## What this repo decides

Only three things, and they are the same three for every worker:

- **Which relay agent it is** — `relay_mcp`, one credential per agent.
- **Which directory it works in** — `repo_dir`.
- **Which CLI and model run, and how hard** — `runtime`, `runtime_config`, and
  the per-worker ceilings.

Because a worker is one agent × one directory × one CLI, **the agent you
delegate a task to is the routing decision**: it determines which checkout the
work happens in and which CLI does it. There is no mapping table to keep in
sync.

## What relay decides

Everything else. What an agent is *for*, its `instructions_md`, whether it can
split work into subtasks and route them, how many tasks it may hold at once, how
long its claim stays live — all of it lives on the relay agent, because
instructions delivered over MCP reach a session that is **already running** and
stay correct when relay changes. A file here could do neither.

So an orchestrator and a worker are two relay agents holding different
capabilities, and locally they are two ordinary workers. Set that side up in the
[relay docs](https://relay.bytecurio.com/).

## Rules that are actually enforced here

- **One credential per worker.** Two workers sharing a `relay_mcp` claim against
  each other as the same agent; this is rejected at startup.
- **Per-worker ceilings.** `max_runs_per_hour`, `max_seconds_per_run` and
  `max_usd_per_run` are per worker, deliberately — a fleet does not share one
  budget. A reviewer that finishes fast wants tighter numbers than an author.
- **Two workers on one checkout is supported**, but keep their concurrent tasks
  on separate branches, or give each its own clone — they share a working tree.
- **An orchestrator that writes nothing still needs a `repo_dir`.** An empty
  directory of its own is enough; sharing one with a worker means overwriting
  each other.

## When a worker keeps relaunching against the same task

Relay can hand a worker a task that needs *its* attention — a subtask asked it
something, or finished. If the agent cannot resolve that (its capability was
revoked, or the parent was re-delegated), the task keeps coming back and each
eligible tick burns a run rediscovering something it cannot fix.

The attention-stall breaker catches that shape — the same task ids across
consecutive *completed* runs — and pauses, naming them:

```text
2026-02-04T11:02:31Z [orchestrator-claude] PAUSED — task(s) 42 have needed this agent's
  attention for 3 consecutive completed runs, and nothing changed.
```

Resolve it in relay, then `rm ~/.relay/state/orchestrator-claude/PAUSED`. Only
completed runs count, so a merely slow agent never trips it.
