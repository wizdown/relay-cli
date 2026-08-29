# Configuration

Everything here describes `.worker-config`, the JSON file listing your workers.
[`.worker-config.example`](../.worker-config.example) is annotated field by field
and is the reference; this page is the summary.

The file is one JSON object with one key, `relay_workers`, an array of worker
objects. **`//` comments are allowed** and are stripped before parsing.

## Two clocks: polls are free, runs are not

Almost every question about this config comes down to one distinction, so it is
worth fixing the vocabulary before the field tables:

| | **Poll** (a *tick*) | **Run** (a *cycle*, a *launch*) |
|---|---|---|
| What it is | One short HTTP request asking relay "do I have a task?" | One headless CLI session that claims and works exactly one task |
| Runs a model | **No.** No CLI is started, no tokens are read or written. | Yes — this is the part that costs money. |
| How often | Every `poll_frequency_seconds` — except on ticks where a run ceiling already forbids launching, when the poll is skipped as pointless | **Only when a poll comes back with work** in any of its three buckets |
| Governed by | `poll_frequency_seconds` — and nothing else | `max_runs_per_hour`, `run_timeout_seconds`, `max_budget_usd`, and a fixed 60s relaunch cooldown |

So `"max_runs_per_hour": 6` means **at most 6 CLI sessions may be launched per
rolling hour**. It is not a limit on polling: a worker set to `30` keeps ticking
every 30 seconds regardless, and 120 empty ticks an hour still cost zero tokens.
Lowering the ceiling doesn't make the worker check less often — it makes it *act*
less often. Once the ceiling is reached the worker won't launch, and doesn't even
bother asking relay, logging this on each tick instead:

```text
2026-02-04T11:41:03Z [wizhub-claude] run ceiling reached (6/6 in the last hour) — not launching
```

**Every ceiling on this page counts runs. Nothing counts, limits, or bills for
polls.**

## Where things live

`relay-cli init` creates one directory and puts everything in it:

```text
relay-cli-workers/
  .worker-config     the worker list — 0600, every endpoint in it is a secret
  agent-workspace/   the directory the generated worker's CLI runs in
  .gitignore         ignores this whole directory, itself included
  live-workers/      per-worker runtime state, removed on shutdown
  logs/              archived sessions, written on shutdown
```

**The config's directory is the poller root.** `live-workers/` and `logs/` are
created beside the config rather than beside the binary, which is what lets one
downloaded binary serve several fleets from different directories.

`--config` accepts either the config file or the directory holding it. Given no
`--config`, `check` and `run` look for `.worker-config` in the current
directory, and then for `relay-cli-workers/.worker-config` below it — so
`init`, `check` and `run` all work from one place with no flags.

The workspace is a directory of its own because the poller root holds the
credentials of *every* worker in the fleet, and an agent's working directory is
the one place its tools are pointed by default. That is hygiene rather than
containment: a headless run is not jailed to `repo_dir`, and the boundary that
actually holds is relay's — one credential per agent, scoped to the work that
agent may be handed.

## Required fields

| Field | Description |
| --- | --- |
| `name` | Unique across all workers. Becomes the worker's state directory, `live-workers/<name>/`, so keep it filesystem-safe (no `/`, no spaces). It's also how you pause, tail and find archived logs for this one worker, so name it after the identity — `<repo>-<runtime>` reads well. |
| `mcp_endpoint` | Unique across all workers. The `connector_url` from `issue_agent_credential`, secret included. |

## Optional fields

| Field | Default | When to set it |
| --- | --- | --- |
| `runtime` | `claude` | The CLI that drives this worker. `claude` is the only supported value today; anything else resolves to `runtimes/<runtime>.sh` beside your config. See [Runtimes](runtimes.md). |
| `repo_dir` | the worker's state dir | The directory the CLI runs inside — `init` writes it pointing at the `agent-workspace/` it created, so a new worker has somewhere to work. Give a second worker its own: two agents in one directory overwrite each other. Otherwise, the checkout the CLI runs inside — so the repo's own `AGENTS.md` / `CLAUDE.md`, skills and tooling load exactly as they would for you. `~` is expanded. Must exist at startup. Omit it only for workers whose tasks are self-contained instructions rather than repo work. |
| `model` | the CLI's default | Which model the CLI runs. Passed straight through, so the accepted values are the runtime's — see [Models](#models). |
| `poll_frequency_seconds` | `30` | Seconds the worker sleeps between **polls**. An empty poll is one short HTTP handshake and zero tokens, so 30–60s is cheap and there's little reason to go below ~15s. **The minimum is `5`** — a config below it is rejected at startup, because a poll that costs you nothing is still a request relay has to answer. This is the *only* field that affects polling, and it never limits how many polls happen. Must be a JSON number (`30`, not `"30"`). |
| `max_runs_per_hour` | `12` | Maximum **CLI launches** per rolling hour — *not* polls. Once the ceiling is hit the worker stops launching for the rest of that rolling hour, even if relay has work waiting, and says so in its log on every tick. `0` means no ceiling. |
| `max_budget_usd` | `5` | Hard dollar cap **inside one CLI run**. Does not apply across runs. `0` removes it. claude only. |
| `run_timeout_seconds` | `900` | Wall-clock kill for **one CLI session**, enforced by relay-cli rather than the CLI itself. |
| `runtime_args` | none | Raw extra flags appended to the CLI invocation. An escape hatch; prefer a config field. |

## Models

`model` is handed to the CLI verbatim, so use that runtime's own spelling. A
wrong value fails inside the run, not at startup.

For **claude**: `opus`, `sonnet` or `haiku` — the alias always points at the
latest model in that family. A full model id (e.g. `claude-opus-5`) also works
and pins an exact version.

Omit `model` entirely to take whatever the CLI defaults to. That's the right
choice unless you have a reason to pick: the default tracks the CLI's own
recommendation and needs no maintenance here when the lineup changes. `claude
--help` is the authority; if a worker dies immediately with an unknown-model
error, check there before assuming it's a poller problem.

Choosing is the usual trade: the largest model for tasks whose briefs are
open-ended or whose blast radius is wide, the smallest for mechanical,
well-specified work where the win is turnaround rather than judgement. Pair a
bigger model with a tighter `max_runs_per_hour` and a lower `max_budget_usd`.

## Safeguards

The defaults are already bounded, which is the point — **a worker you configure
with two fields is a safe worker**:

| Guard | Default | What it bounds |
|---|---|---|
| `max_runs_per_hour` | `12` | How many CLI sessions may *start*. The only ceiling on how many, so it is the one that actually caps spend. Everything else bounds a single session. |
| `max_budget_usd` | `5` | Spend inside one run (claude). |
| `run_timeout_seconds` | `900` | Wall-clock for one run. A session that hangs holds both the worker's lock and the task's relay lease until it is killed. |
| relaunch cooldown | 60s, fixed | Two launches can't go back-to-back. Without it, a task that fails immediately, or a lease that lapses mid-session, is re-offered on the very next poll and picked straight back up. |

Set any of the first three to `0` to remove it — that's a deliberate choice, not
a default you can drift into.

The rest you get for free, no config:

| Guard | Effect |
|---|---|
| Empty queue ⇒ no launch | The idle case, which is most of them, costs one HTTP handshake and zero tokens. Polling is never the expense. |
| `mkdir` lock | One cycle per worker at a time. A lapsed relay lease would otherwise re-offer a task while its session is still running. |
| Probe breaker | `MAX_PROBE_FAILURES` (default 10) consecutive probe failures — revoked credential, dead host — self-pause the worker instead of failing twice a minute forever. |
| Budget breaker | A run killed by its spend cap is explained in full; `MAX_BUDGET_KILLS` (default 2) in a row self-pause. Retrying unchanged just spends the cap again at the same point. |
| Attention-stall breaker | `MAX_ATTENTION_STALLS` (default 3) consecutive *completed* runs that left the same task in `attention` self-pause. See [When a fan-out gets stuck](fleets.md#when-a-fan-out-gets-stuck). |
| One task per cycle | A session ends at a hand-back, not when the model runs out of ideas. |
| Attempt cap | Relay-side: a task re-claimed too many times is forced to Blocked and escalated instead of looping. |

## Things that will bite you otherwise

- **Unknown keys are silently ignored.** Every optional field is read with a
  fallback, so `"max_runs_per_hr": 6` is not an error — it just does nothing.
  Check spelling against the tables above. The one exception is the five
  **removed** keys (`system_prompt`, `system_prompt_file`,
  `min_run_interval_seconds`, `permission_mode`, `codex_mcp_transport`), which
  are rejected by name with their replacement, because silently ignoring those
  would change what the worker does.
- **`max_budget_usd` reaches claude only.** A runtime without a per-run spend cap
  has `max_runs_per_hour` and `run_timeout_seconds` as its whole budget.
- **`runtime_args` is word-split**, so an argument containing a space can't be
  expressed.
- **`max_runs_per_hour` history resets on restart.** The rolling-hour count is
  literally `live-workers/<name>/runs.log` — one unix timestamp appended per CLI
  launch, nothing appended for a poll. Every start clears `live-workers/`
  wholesale, so restarting a worker gives it a fresh hour's allowance. A restart
  also clears any `PAUSED` file and every breaker's counter.
- **Ceilings are checked before the probe, not after.** A tick runs in this
  order: `PAUSED` file → lock → ceilings → probe → launch. So when a run ceiling
  or the relaunch cooldown is in force, the probe is skipped for that tick —
  there's no point asking a question you can't act on. A throttled worker is
  therefore *cheaper* than an idle one, never more expensive.
- **Two workers on one repo share a working tree.** Supported and useful, but
  keep their concurrent tasks on separate branches — or give each its own clone.

## What is checked before anything launches

`relay-cli run` and `relay-cli check` perform the same validation, and both
refuse to start rather than failing 120 times an hour in a log nobody is reading.
It fails if:

- no config is found — with no `--config`, both `.worker-config` here and
  `relay-cli-workers/.worker-config` are tried, and the error names both — or
  it isn't valid JSON once comments are stripped
- there's no non-empty `relay_workers` array
- an entry is missing `name` or `mcp_endpoint`, or sets `poll_frequency_seconds`
  to anything but a positive JSON number, or sets it below the `5` second
  minimum
- an entry still uses one of the five removed keys
- a `name` or `mcp_endpoint` repeats
- a `name` is not a single path segment
- a `runtime` has no adapter **or that CLI isn't usable on this machine**
- a `repo_dir` doesn't exist

`relay-cli check` then goes one step further and tests each credential against
relay. It launches nothing and spends nothing:

```bash
relay-cli check
```

```text
  wizhub-claude            ok    queue: resume 0 · attention 0 · todo 0
```

A queue of `0` means the credential works and there is simply no work waiting.
`FAIL … HTTP 401` means the URL is wrong or the credential was revoked.
