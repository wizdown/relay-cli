# Configuration

Everything here describes `~/.relay/config`, the JSON file listing your workers.
This page is the reference: `relay-cli init` writes a short starting config that
points back here rather than repeating it.

The file is one JSON object with a `workers` array. **`//` comments are allowed**
and are stripped before parsing.

```bash
relay-cli init
```

## Two clocks: polls are free, runs are not

Almost every question about this config comes down to one distinction, so it is
worth fixing the vocabulary before the field tables:

| | **Poll** (a *tick*) | **Run** (a *cycle*, a *launch*) |
|---|---|---|
| What it is | One short HTTP request asking relay "do I have a task?" | One headless CLI session that claims and works exactly one task |
| Runs a model | **No.** No CLI is started, no tokens are read or written. | Yes — this is the part that costs money. |
| How often | Every `poll_seconds` — except on ticks where a run ceiling already forbids launching, when the poll is skipped as pointless | **Only when a poll comes back with work** in any of its three buckets |
| Governed by | `poll_seconds` — and nothing else | `max_runs_per_hour`, `max_seconds_per_run`, the runtime's own spend cap, and a fixed 60s relaunch cooldown |

So `"max_runs_per_hour": 6` means **at most 6 CLI sessions may be launched per
rolling hour**. It is not a limit on polling: a fleet set to `30` keeps ticking
every 30 seconds regardless, and 120 empty ticks an hour still cost zero tokens.
Lowering the ceiling doesn't make a worker check less often — it makes it *act*
less often. Once the ceiling is reached the worker won't launch, and doesn't even
bother asking relay, logging this on each tick instead:

```text
2026-02-04T11:41:03Z [wizhub-claude] run ceiling reached (6/6 in the last hour) — not launching
```

**Every ceiling on this page counts runs. Nothing counts, limits, or bills for
polls.**

## Where things live

Everything relay-cli owns is in one place, and no flag moves it:

```text
~/.relay/
  config      the worker list — 0600, every relay_mcp in it is a secret
  state/      per-worker runtime state, removed on shutdown
  logs/       archived sessions, written on shutdown
```

`init`, `check` and `run` all read the same path, from any directory. A worker is
a relay agent *identity* holding a credential relay issued to you, which is
user-scoped the way `~/.aws` and `~/.kube` are — and a fleet routinely spans
several checkouts, so there is no one repo it would belong beside.

## The shape

Two levels. `poll_seconds` is fleet-wide because the floor under it protects
relay rather than you, and how hard a fleet leans on one relay server is a
property of the fleet. Everything else is per worker.

```jsonc
{
  // Fleet-wide. Optional, default 30, minimum 5.
  "poll_seconds": 30,

  "workers": [
    {
      // ── relay-cli's own fields: same meaning for every runtime ──────────
      "name":      "wizhub-claude",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME",
      "repo_dir":  "~/code/wizhub",
      "runtime":   "claude",

      "max_runs_per_hour":   6,
      "max_seconds_per_run": 900,

      // ── handed to the runtime named above; keys vary per runtime ────────
      "runtime_config": {
        "model":           "sonnet",
        "max_usd_per_run": 5
      }
    },

    {
      // A second worker on the same checkout is supported and useful — but
      // keep their concurrent tasks on separate branches, or give each its own
      // clone. What each is FOR is its relay description and instructions_md,
      // not anything here.
      "name":      "wizhub-reviewer",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME_2",
      "repo_dir":  "~/code/wizhub",
      "runtime":   "claude",

      // A reviewer claims less often and finishes faster than an author, so
      // both of these are tighter than the defaults. Bounds are per worker
      // precisely so a fleet does not share one budget.
      "max_runs_per_hour":   4,
      "max_seconds_per_run": 600,

      "runtime_config": {
        "model":           "haiku",
        "max_usd_per_run": 3
      }
    }
  ]
}
```

The fields outside `runtime_config` are enforced by relay-cli and mean the same
thing whichever CLI is driving. The ones inside are that CLI's own vocabulary,
translated by its adapter.

**What is *not* here:** an agent's identity — what it is for, its house rules,
what it may decide alone — lives in relay as that agent's `instructions_md`,
along with its capabilities and claim limits. Set those with `update_agent` or
in the relay agent console. They reach a session that is already running; a file
here would not.

## Fleet fields

| Field | Required | What it does | Default |
| --- | --- | --- | --- |
| `poll_seconds` | no | Seconds between **polls**, for every worker. An empty poll is one HTTP handshake and zero tokens, so 30–60s is cheap and there is little reason to go below ~15s. **The minimum is `5`**, and a config below it is rejected rather than clamped — a poll that costs you nothing is still a request relay has to answer. Must be a JSON number (`30`, not `"30"`). | `30` |

## Worker fields

| Field | Required | What it does | Default |
| --- | --- | --- | --- |
| `name` | **yes** | Unique. Becomes `~/.relay/state/<name>/`, so it must be a single path segment — no `/`, no spaces. It is also how you pause, tail and find archived logs for this one worker, so name it after the identity: `<repo>-<runtime>` reads well. | — |
| `relay_mcp` | **yes** | Unique. The `connector_url` from `issue_agent_credential`, secret included. Shown by relay exactly once. | — |
| `repo_dir` | **yes** | The checkout this worker's CLI runs inside, so that repo's own `AGENTS.md` / `CLAUDE.md`, skills and tooling load exactly as they would for you. That is what the field is *for*, which is why it has no default: an agent pointed somewhere arbitrary is an agent working without any of it. `~` is expanded, and the directory must exist at startup. Point it at a checkout you are willing to have rewritten — a headless run is fully autonomous and cannot answer an approval prompt. | — |
| `runtime` | **yes** | Which CLI drives this worker, and which `runtime_config` keys apply. `claude` is the only supported value today; see [Runtimes](runtimes.md). | — |
| `max_runs_per_hour` | no | Maximum **CLI launches** per rolling hour — *not* polls. This is the only ceiling on how many sessions may start, so it is the one that actually caps what you spend. Once it is hit the worker keeps ticking but stops launching, and says so in its log. `0` means no ceiling. | `12` |
| `max_seconds_per_run` | no | Wall-clock kill for **one session**, enforced by relay-cli rather than by the CLI. It is the only thing that catches a *hung* session — one that spends nothing while holding both the worker's lock and the task's relay lease. `0` removes it. | `900` |
| `runtime_config` | depends | Settings the runtime understands, in its own vocabulary — see the table for your runtime below. Required whenever that runtime has a required key, which for `claude` it does (`model`). A key the runtime does not accept is **rejected when the config loads**, not ignored. | — |

## `runtime_config`: claude

| Key | Required | What it does | Default |
| --- | --- | --- | --- |
| `model` | **yes** | Which model to run, passed to the CLI verbatim as `--model`. See [Models](#models). | — |
| `max_usd_per_run` | no | Hard dollar cap **inside one run**, enforced by the CLI itself. It does not apply across runs — `max_runs_per_hour` is the only ceiling on how many there are, so the two bound different things and you want both. `0` removes the cap. | `5` |

A run killed by this cap is explained in full in the worker's log, and two in a
row pause the worker: retrying unchanged restarts the same task and walks into
the same wall at the same point.

## Models

For claude, `model` takes `opus`, `sonnet` or `haiku` — each alias always points
at the latest model in that family — or a full model id such as `claude-opus-5`
to pin an exact version. It is handed to the CLI verbatim, so a wrong value fails
inside the run rather than at startup; `claude --help` is the authority on what
is currently accepted.

It is **required rather than defaulted**, which is deliberate. The CLI has its
own default and tracking it would be one less thing to write, but that default is
not stable across CLI versions: an unchanged config would quietly change both
what a worker costs and how it behaves the next time you upgraded `claude`. For
an unattended process that spends money on its own, the config should say what it
actually runs.

Choosing is the usual trade: the largest model for tasks whose briefs are
open-ended or whose blast radius is wide, the smallest for mechanical,
well-specified work where the win is turnaround rather than judgement.

| | |
|---|---|
| `opus` | open-ended briefs, wide blast radius, work you would review closely |
| `sonnet` | the usual choice — capable, and cheaper per run |
| `haiku` | mechanical, well-specified work where turnaround is the win |

Pair a bigger model with a tighter `max_runs_per_hour` and a lower
`max_usd_per_run`.

## Safeguards

The defaults are already bounded, which is the point — **a worker you configure
past its required fields is still a safe worker**:

| Guard | Default | What it bounds |
|---|---|---|
| `max_runs_per_hour` | `12` | How many CLI sessions may *start*. The only ceiling on how many, so it is the one that actually caps spend. Everything else bounds a single session. |
| `max_usd_per_run` | `5` | Spend inside one run. claude only — a runtime with no spend cap has the two relay-cli ceilings as its whole budget. |
| `max_seconds_per_run` | `900` | Wall-clock for one run, and the only guard that catches a hang. |
| relaunch cooldown | 60s, fixed | Two launches can't go back-to-back. Without it, a task that fails immediately, or a lease that lapses mid-session, is re-offered on the very next poll and picked straight back up. |

Set any of the first three to `0` to remove it — that is a deliberate choice, not
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

## Keys this version no longer accepts

These are rejected **by name**, with their replacement, rather than ignored —
silently dropping any of them would change what a worker does:

| Key | What happened to it |
| --- | --- |
| `mcp_endpoint` | renamed to `relay_mcp` |
| `poll_frequency_seconds` | renamed to `poll_seconds`, and moved to the top level: one poll rate for the fleet |
| `run_timeout_seconds` | renamed to `max_seconds_per_run` |
| `max_budget_usd` | moved into `runtime_config` and renamed to `max_usd_per_run`: only some runtimes can enforce a spend cap |
| `model` | moved into `runtime_config`: it is spelled in the runtime's own vocabulary, not relay-cli's |
| `runtime_args` | removed. Raw argv could silently override the flags this harness depends on — including `--permission-mode`, which is what makes a headless run work at all. Every setting a runtime accepts is now a declared key in `runtime_config`. |
| `system_prompt`, `system_prompt_file` | agent identity lives in relay now: set the agent's `instructions_md`, which reaches a *running* agent as a local file never could |
| `min_run_interval_seconds` | replaced by a fixed 60s relaunch cooldown. To make a worker act less often, lower `max_runs_per_hour` |
| `permission_mode` | a headless run is always fully autonomous — there is no prompt it could answer |
| `codex_mcp_transport` | export `CODEX_MCP_TRANSPORT=mcp-remote` before launching instead |

## Things that will bite you otherwise

- **Unknown keys at worker level are silently ignored**, so `"max_runs_per_hr": 6`
  is not an error — it just does nothing. Check spelling against the tables
  above. Inside `runtime_config` the opposite is true: an unknown key is
  rejected, because that block is the one place a setting is spelled in a CLI's
  own vocabulary, so a key the runtime does not know is either a typo or a
  setting meant for a different runtime.
- **Every problem is reported at once.** A half-written config produces one list,
  not one error per run, so fixing it is one sitting rather than a dozen
  edit-and-rerun rounds.
- **`max_runs_per_hour` history resets on restart.** The rolling-hour count is
  literally `~/.relay/state/<name>/runs.log` — one unix timestamp appended per CLI
  launch, nothing appended for a poll. Every start clears `state/` wholesale, so
  restarting a worker gives it a fresh hour's allowance. A restart also clears
  any `PAUSED` file and every breaker's counter.
- **Ceilings are checked before the probe, not after.** A tick runs in this
  order: `PAUSED` file → lock → ceilings → probe → launch. So when a run ceiling
  or the relaunch cooldown is in force, the probe is skipped for that tick —
  there's no point asking a question you can't act on. A throttled worker is
  therefore *cheaper* than an idle one, never more expensive.
- **Two workers on one repo share a working tree.** Supported and useful, but
  keep their concurrent tasks on separate branches — or give each its own clone.
- **Never point two workers at one `relay_mcp`.** One credential per agent is
  relay's boundary; two workers sharing one claim against each other as the same
  agent. This is rejected at startup.

## What is checked before anything launches

`relay-cli run` and `relay-cli check` perform the same validation, and both
refuse to start rather than failing 120 times an hour in a log nobody is reading.
Problems with the *file* are collected and reported together; a runtime whose CLI
is missing is reported separately, because a config that is correct and a machine
that is missing a CLI are different jobs.

It fails if:

- there is no config at `~/.relay/config` — the error says so and names `init`
- it isn't valid JSON once comments are stripped
- there's no non-empty `workers` array
- `poll_seconds` isn't a JSON number, or is below the `5` second minimum
- an entry is missing `name`, `relay_mcp`, `repo_dir` or `runtime`
- an entry's `relay_mcp` or `repo_dir` is still the placeholder `init` wrote — both
  are reported together, not one run at a time
- an entry's `repo_dir` isn't a directory
- an entry still uses a key from the removed list above
- a `name` or a `relay_mcp` repeats
- a `name` is not a single path segment
- `runtime_config` is missing a key its runtime requires, carries one that
  runtime does not accept, or gives one the wrong type
- a `runtime` has no adapter **or that CLI isn't usable on this machine**

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
