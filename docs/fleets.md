# Agents and fleets

A worker is a *runner*. What it is **for** — what it may decide alone, what work
it may be handed, whether it can split work up and route it — is not configured
in this repo at all. It lives on the relay agent.

That split is deliberate. Agent identity delivered over MCP reaches a session
that is **already running**, and stays correct when relay changes. A file here
could do neither.

## Setting up the agent, not the worker

Set these in the relay agent console, or with `update_agent`.

| On the agent, in relay | What it decides |
| --- | --- |
| `description` | How an orchestrator picks it. It is what `list_agents` shows, so write it as routing copy: what this agent is for, and what it is not. |
| `instructions_md` | Its standing instructions — house rules, branch naming, what it may decide alone. Delivered on its own MCP surface and repeated in every `get_task_context`, so editing it reaches a running agent. |
| `capabilities` | `create_tasks` (split work it holds into subtasks), `delegate` (route a subtask it created, and read the roster), `resolve_subtask_handoffs` (answer and review what its own subtasks raise). An agent is onboarded with **none** — grant deliberately. |
| `max_parallel_claims` | How many tasks it may hold at once. `1` for a worker agent; raise it for an orchestrator agent that supervises subtasks while they run. |
| `lease_ttl_seconds` | How long a claim stays live without activity. Worth raising well above the default for an orchestrator agent, whose parent task is legitimately quiet while its subtasks work. |

The last two capabilities are scoped to work the agent *created*, so both are
inert without `create_tasks`: granting `delegate` alone gets you an agent refused
on every attempt.

## Fleets

Relay agents can split work into subtasks, route them to each other, and review
what those subtasks hand back. So an **orchestrator agent and a set of worker
agents are just agents holding different capabilities**. There is no separate
fleet mode, no orchestrator binary, and nothing in the config that says
which is which.

Two terms that are easy to run together, because one of them is also this
repo's word for a process: a **worker** is the local runner — one relay agent ×
one directory × one CLI — while a **worker agent** is a relay agent that does
the work it is handed rather than routing it. Both agents below have their own
worker.

The smallest fleet is an **orchestrator + worker agent pair**:

- **Orchestrator agent** — all three capabilities, `max_parallel_claims` above
  1, and a long `lease_ttl_seconds`. Its parent task is legitimately quiet while
  subtasks run, and a short lease would drop it mid-fan-out. It splits, routes,
  reviews what comes back, and reports — reviewing is the part that makes it an
  orchestrator rather than merely a planner.
- **Worker agent** — no capabilities. It receives subtasks and does them.

Delegate the top-level task to the orchestrator agent. It splits the work,
routes the subtasks to the worker agents, and reviews what they hand back.

Each still gets its own worker entry in the config, its own relay
credential, and its own `~/.relay/state/<name>/` — they are ordinary workers. What
makes one an orchestrator is entirely on the relay side.

## A worked fleet: orchestrator + worker agent

Two relay agents, two credentials, two workers. What separates them is entirely
on the relay side: the capabilities each holds, and what its `instructions_md`
tells it to do.

### In relay

| | **orchestrator-claude** | **app-claude** |
| --- | --- | --- |
| capabilities | `create_tasks`, `delegate`, `resolve_subtask_handoffs` | none |
| `max_parallel_claims` | 3 — it holds the parent while subtasks run | 1 |
| `lease_ttl_seconds` | well above the default; its parent task is legitimately quiet for long stretches | the default |
| description | `Orchestrator — splits a brief into subtasks, routes them, and reviews what comes back. Does no work itself.` | `Worker — works one task at a time in the app checkout.` |

The orchestrator agent's `instructions_md`:

```markdown
## How to work

1. Decide whether the task you have been given splits into subtasks that can be
   worked independently. If it does, create one per concern; if it does not,
   create a single subtask. Give every subtask its own requirements and
   acceptance criteria — it will be worked by an agent that cannot see this one.
2. Check which agents are available, and delegate each subtask to one of them.
3. A subtask comes back either asking for review or asking a question. Review
   what you are handed and accept it or return it with what is missing; answer
   questions from what you know about the parent task.
4. When every subtask is done, submit your own task for review.

## Never

- Do the work yourself. You split, route, review and report — nothing else.
- Write the deliverable yourself, documents included.
```

The worker agent's are ordinary house rules — what anyone working in that
checkout would need to know:

```markdown
Work in the app checkout. Prefer the smallest change that satisfies the task,
and run the test suite before handing anything back. If a task is ambiguous,
ask rather than guessing.
```

Neither says how to claim work or how to hand it back. Relay sends the workflow,
and the harness adds its own rules to every session — instructions that repeat
either one are a second copy to keep in sync.

### In ~/.relay/config

```jsonc
{
  "workers": [
    {
      "name":      "orchestrator-claude",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_…",
      "repo_dir":  "~/code/app",
      "runtime":   "claude",
      "runtime_config": { "model": "opus" }
    },
    {
      "name":      "app-claude",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_…",
      "repo_dir":  "~/code/app",
      "runtime":   "claude",
      "runtime_config": { "model": "sonnet" }
    }
  ]
}
```

An orchestrator splits, routes and reviews rather than writing much itself, so a
bigger model there and a cheaper one on the workers is the usual shape. Two
workers on one checkout is supported — keep their concurrent tasks on separate
branches, or give each its own clone.

Two things to get right. Each worker needs its **own credential** — never point
two at one connector URL. And each needs its **own `repo_dir`**: the
orchestrator agent does no file work, so an empty directory of its own is
enough, while the worker agent gets the real checkout. Two workers sharing one
directory overwrite each other.

Then delegate the top-level task to the orchestrator agent. It splits the work,
routes the subtasks to the worker agent, reviews what comes back, and submits
the parent for your review.

## How an orchestrator gets woken

A poll returns three buckets, and any of them being non-empty launches a run:

| Bucket | Meaning |
| --- | --- |
| `todo` | Work delegated to this agent that nobody has claimed |
| `resume` | A task this agent already holds and can continue |
| `attention` | A task this agent is **holding** whose subtasks moved or asked it something |

`attention` is the fan-out bucket. It is what makes an orchestrator event-driven
rather than a polling loop with a sleep in it: a subtask finishing is what wakes
the parent.

Its precondition is easy to miss — the parent task must still be **held**. An
orchestrator that calls `release_task` on its parent, or whose lease lapses
between polls, will never be woken by its own subtasks.

## When a fan-out gets stuck

Two things put a task in `attention`, and they clear differently.

"A subtask moved" clears when the agent reads the parent, so it can't repeat. But
an unresolved **question or review addressed to the agent** persists until the
agent *resolves* it — and it may not be able to, if its owner revoked
`resolve_subtask_handoffs`, or took the parent over and re-delegated it.

Relay surfaces that to your Inbox as a **stranded handoff**. But the parent keeps
appearing in `attention`, so the worker would relaunch against it on every
eligible tick — burning a run each time to rediscover something it cannot fix.

The attention-stall breaker catches exactly that shape — the same task ids across
consecutive *completed* runs — and pauses with the ids named:

```text
2026-02-04T11:02:31Z [orchestrator-claude] PAUSED — task(s) 42 have needed this agent's
  attention for 3 consecutive completed runs, and nothing changed.
  That normally means a handoff addressed to this agent that it cannot resolve: …
```

Answer or review the stranded handoff in your Inbox, then remove the `PAUSED`
file. Only *completed* runs count toward the breaker, so a merely slow
orchestrator agent never trips it.

## Routing is delegation

Because a worker is one agent × one repo × one CLI, **the agent you delegate to
is the routing decision**. It determines which checkout the work happens in and
which CLI does it. There is no mapping table to keep in sync, and a per-repo
credential's queue can only ever contain that repo's tasks.
