# Configuration

`~/.relay/config` is one JSON file listing your workers. `//` comments are
allowed and are stripped before parsing. `relay init` writes a starting copy.

Everything relay-cli owns lives beside it, and no flag moves it:

```text
~/.relay/
  config      the worker list — 0600, every relay_mcp in it is a secret
  state/      per-worker runtime state, removed on shutdown
  logs/       archived sessions, written on shutdown
```

## The shape

```jsonc
{
  // Fleet-wide. Optional.
  "poll_seconds": 30,

  "workers": [
    {
      // relay-cli's own fields — same meaning for every runtime
      "name":      "wizhub-claude",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME",
      "repo_dir":  "~/code/wizhub",
      "runtime":   "claude",

      "max_runs_per_hour":   6,
      "max_seconds_per_run": 900,

      // handed to the runtime named above — that CLI's own vocabulary
      "runtime_config": {
        "model":           "sonnet",
        "max_usd_per_run": 5
      }
    }
  ]
}
```

Fields **outside** `runtime_config` are enforced by relay-cli. Fields **inside**
are the named CLI's vocabulary, translated by its adapter — a key that runtime
does not accept is rejected when the config loads.

## More than one worker

A fleet is just more than one entry in `workers`. There is no fleet mode, no
orchestrator binary, and nothing here that says which worker is which.

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

Because a worker is one agent × one directory × one CLI, **the agent you
delegate a task to is the routing decision**: it determines which checkout the
work happens in and which CLI does it. There is no mapping table to keep in
sync.

What an agent is *for* — its instructions, whether it can split work into
subtasks and route them, how many tasks it may hold at once — lives on the relay
agent rather than here, because instructions delivered over MCP reach a session
that is **already running** and stay correct when relay changes. So an
orchestrator and a worker are two relay agents holding different capabilities,
and locally they are two ordinary workers. Set that side up in the
[relay docs](https://relay.bytecurio.com/).

Four things that only matter once there is more than one:

- **Never point two workers at one `relay_mcp`.** Rejected at startup: two
  workers sharing a credential claim against each other as the same agent.
- **Two workers on one repo share a working tree.** Supported, but keep their
  concurrent tasks on separate branches, or give each its own clone.
- **An orchestrator that writes nothing still needs a `repo_dir`.** An empty
  directory of its own is enough; sharing one with a worker means overwriting
  each other.
- **Ceilings are per worker, and a fleet does not share one budget.** A reviewer
  that finishes fast wants tighter numbers than an author.

## Removing a worker

Delete its entry from the config and its state directory, then **revoke its
credential in relay** — deleting the config does not invalidate a credential
relay has already issued.

```bash
rm -rf ~/.relay/state/<name>
```

To remove everything: revoke every credential in the config, then `rm -rf
~/.relay/` and `rm $(command -v relay)`. Archived logs can contain anything the
agents read, so delete them deliberately rather than by habit.

## Polls and runs

Every ceiling here counts **runs**. Nothing counts, limits or bills for polls.

| | **Poll** | **Run** |
|---|---|---|
| What it is | one HTTP request asking relay "do I have a task?" | one headless CLI session, claiming and working exactly one task |
| Runs a model | no — zero tokens | yes; this is the part that costs money |
| How often | every `poll_seconds` | only when a poll comes back with work |
| Bounded by | `poll_seconds` alone | `max_runs_per_hour`, `max_seconds_per_run`, `max_usd_per_run`, a fixed 60s cooldown |

So `"max_runs_per_hour": 6` caps CLI sessions, not polling. Once it is reached
the worker keeps ticking, stops launching, and says so:

```text
2026-02-04T11:41:03Z [wizhub-claude] run ceiling reached (6/6 in the last hour) — not launching
```

## Fleet fields

| Field | Required | What it does | Default |
| --- | --- | --- | --- |
| `poll_seconds` | no | Seconds between polls, for every worker. Minimum `5`, and a lower value is rejected rather than clamped — a free poll is still a request relay must answer. Must be a JSON number. | `30` |

## Worker fields

| Field | Required | What it does | Default |
| --- | --- | --- | --- |
| `name` | **yes** | Unique, and a single path segment — it becomes `~/.relay/state/<name>/`, which is how you pause, tail and find logs for this worker. `<repo>-<runtime>` reads well. | — |
| `relay_mcp` | **yes** | Unique. The `connector_url` relay issued for this agent, secret included, and it must be a full `http(s)` URL. Shown by relay exactly once. | — |
| `repo_dir` | **yes** | The checkout this worker's CLI runs inside, so that repo's `AGENTS.md` / `CLAUDE.md`, skills and tooling load as they would for you. `~` is expanded; the path must be absolute — a relative one would resolve against wherever `relay run` was started from — and the directory must exist at startup. A headless run is autonomous and cannot answer a prompt — point it somewhere you are willing to have rewritten. | — |
| `runtime` | **yes** | Which CLI drives this worker, and which `runtime_config` keys apply. `claude` is the only supported value — see [Runtimes](runtimes.md). | — |
| `max_runs_per_hour` | no | Maximum CLI launches per rolling hour — not polls. The only ceiling on how many sessions start, so it is the one that caps spend. A whole number; `0` means no ceiling. | `12` |
| `max_seconds_per_run` | no | Wall-clock kill for one session, enforced by relay-cli. The only guard that catches a hung run — one spending nothing while holding the worker lock and the task's relay lease. A whole number of seconds, and at least `30` when set: a shorter kill ends every session before it can claim anything, and the run is still paid for. `0` removes it. | `900` |
| `runtime_config` | depends | Settings the named runtime understands. Required when that runtime has a required key, which `claude` does. | — |

## `runtime_config` for `claude`

| Key | Required | What it does | Default |
| --- | --- | --- | --- |
| `model` | **yes** | Passed to the CLI verbatim as `--model`, so it is taken exactly as written: `opus`, `sonnet`, `haiku`, or a pinned id like `claude-opus-5`. Required rather than defaulted, because the CLI's own default moves between versions and an unattended worker should say what it runs. | — |
| `max_usd_per_run` | no | Hard dollar cap inside one run, enforced by the CLI. It does not apply across runs — `max_runs_per_hour` is the only ceiling on how many there are. `0` removes it. | `5` |

Two runs killed by the spend cap in a row pause the worker: retrying unchanged
restarts the same task and hits the same wall at the same point.

## Safeguards

Defaults are already bounded — a worker configured no further than its required
fields is still a safe worker.

| Guard | Default | What it bounds |
|---|---|---|
| `max_runs_per_hour` | `12` | How many CLI sessions may *start*. Everything else bounds a single session. |
| `max_usd_per_run` | `5` | Spend inside one run. claude only. |
| `max_seconds_per_run` | `900` | Wall-clock for one run; the only guard that catches a hang. |
| relaunch cooldown | 60s, fixed | Two launches can't go back-to-back, so a task that fails immediately isn't picked straight back up. |

Set any of the first three to `0` to remove it — deliberately, not by drift.

The rest need no config:

| Guard | Effect |
|---|---|
| Empty queue ⇒ no launch | The idle case costs one HTTP handshake and zero tokens. |
| `mkdir` lock | One cycle per worker at a time. |
| Probe breaker | 10 consecutive probe failures — revoked credential, dead host — self-pause the worker. |
| Budget breaker | 2 consecutive spend-cap kills self-pause. |
| Attention-stall breaker | 3 consecutive *completed* runs that leave the same task needing attention self-pause. |
| One task per cycle | A session ends at a hand-back, not when the model runs out of ideas. |

Each breaker writes a `PAUSED` file naming its own fix. Remove the file to
resume:

```bash
rm ~/.relay/state/<name>/PAUSED
```

### When a worker keeps relaunching against the same task

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

Resolve it in relay, then remove that worker's `PAUSED` file. Only completed
runs count, so a merely slow agent never trips it.

## Gotchas

- **Every key is checked by name, at every level of the file.** `max_runs_per_hr`
  does not quietly do nothing — it fails, and the error names the key it was
  probably meant to be. A key relay-cli does not read is a ceiling you believe is
  in force and is not.
- **Every problem is reported at once**, so fixing a half-written config is one
  sitting rather than a dozen edit-and-rerun rounds.
- **The rolling-hour count resets on restart.** It is
  `~/.relay/state/<name>/runs.log`, and every start clears `state/` — along with
  any `PAUSED` file and every breaker counter.
- **Ceilings are checked before the probe.** A tick goes `PAUSED` → lock →
  ceilings → probe → launch, so a throttled worker is cheaper than an idle one.

## What is validated before anything launches

`relay run` and `relay check` refuse to start rather than failing hourly in a log
nobody reads. Problems with the file are collected and reported together. It
fails if:

- there is no config, or it is empty — run `relay init`
- the JSON is invalid, or `workers` is missing or empty
- **any key is one this version does not accept**, at the top level, in a worker,
  or in `runtime_config`. The error names the key it was likely meant to be, or
  where the setting actually belongs — a runtime's setting written beside
  relay-cli's own fields, or `poll_seconds` written per worker
- an entry is missing a required field, or still holds an `init` placeholder
- a value cannot mean what it says: a field that should be a string is a number,
  `relay_mcp` is not an `http(s)` URL, `repo_dir` is relative or is not a
  directory, `poll_seconds` is below `5`, a ceiling is negative or fractional, a
  `max_seconds_per_run` that is set is below `30`, or a `name` or a
  `runtime_config` value has whitespace around it
- a `name` or `relay_mcp` repeats, or a `name` is not a single path segment
- the named runtime's CLI is unusable on this machine

A key this version has **removed** is rejected by name with what to use instead,
rather than with a spelling suggestion — so a config written against an older
release tells you what changed rather than doing something you did not ask for.

`relay check` then tests each credential against relay, launching nothing:

```text
  wizhub-claude            ok    queue: resume 0 · attention 0 · todo 0
```

A queue of `0` means the credential works and no work is waiting.
`FAIL … HTTP 401` means the URL is wrong or the credential was revoked.
