# Configuration

`~/.relay/config` is one JSON file listing your workers. `//` comments are
allowed and are stripped before parsing. `relay init` writes a starting copy.

It writes one worker per coding CLI it finds on `PATH`, and a worker for each
one it does not find **commented out**, above a line naming the CLI to install.
So the file that lands is one that runs here — a worker naming a runtime this
machine has no CLI for refuses the start — and the other runtime is still in
front of you, four characters a line away from being on. With neither CLI
installed `init` writes nothing and says so: a worker is a coding CLI, and there
is no useful config for a machine with none.

Everything relay-cli owns lives beside it, and no flag moves it:

```text
~/.relay/
  config           the worker list — 0600, every relay_mcp in it is a secret
  state/           per-worker runtime state, removed on shutdown
  logs/            archived sessions, written on shutdown
  worker-rules.md  optional — replaces the harness contract every worker is given
```

`relay init` writes `config` and nothing else. `worker-rules.md` is one you
create yourself, and most fleets never do — what the contract says, and when
replacing it is the right move, is in
[The working directory](working-directory.md#step-0--an-empty-directory).

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
        "model":           "sonnet",           // pinned to claude-sonnet-5
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
      "name":      "app-codex",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME_2",
      "repo_dir":  "~/code/app",
      "runtime":   "codex",
      "runtime_config": { "model": "terra" }
    }
  ]
}
```

Because a worker is one agent × one directory × one CLI, **the agent you
delegate a task to is the routing decision**: it determines which checkout the
work happens in and which CLI does it. There is no mapping table to keep in
sync.

What an agent is *for* — its instructions, whether it can split work into
subtasks, how many tasks it may hold — lives on the relay agent, not here: those
reach a session that is **already running**. So an orchestrator and a worker are
two relay agents with different capabilities, and locally two ordinary workers.
Set that side up in the [relay docs](https://relay.bytecurio.com/).

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
| Bounded by | `poll_seconds` alone | `max_runs_per_hour`, `max_seconds_per_run`, `max_usd_per_run` (claude), a fixed 60s cooldown |

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
| `repo_dir` | **yes** | The directory this worker's CLI runs inside, so its `CLAUDE.md`, skills and tooling load as they would for you — see [The working directory](working-directory.md). An empty directory is valid; a checkout is what makes the worker able to change code. `~` is expanded; the path must be absolute — a relative one would resolve against wherever `relay run` was started from — and the directory must exist at startup. A headless run is autonomous and cannot answer a prompt — point it somewhere you are willing to have rewritten. | — |
| `runtime` | **yes** | Which CLI drives this worker, and which `runtime_config` keys apply. `claude` or `codex` — see [Runtimes](runtimes.md). | — |
| `max_runs_per_hour` | no | Maximum CLI launches per rolling hour — not polls. The only ceiling on how many sessions start, so it is the one that caps spend. A whole number; `0` means no ceiling. | `12` |
| `max_seconds_per_run` | no | Wall-clock kill for one session, enforced by relay-cli. The only guard that catches a hung run — one spending nothing while holding the worker lock and the task's relay lease. A whole number of seconds, and at least `30` when set: a shorter kill ends every session before it can claim anything, and the run is still paid for. `0` removes it. | `900` |
| `runtime_config` | depends | Settings the named runtime understands. Required when that runtime has a required key, which both `claude` and `codex` do. | — |

## `runtime_config` for `claude`

| Key | Required | What it does | Default |
| --- | --- | --- | --- |
| `model` | **yes** | Which model to run. Write `opus`, `sonnet` or `haiku` — each is **pinned** to an id (see [Aliases are pinned, not tracked](#aliases-are-pinned-not-tracked)), and the ids `claude-opus-5`, `claude-sonnet-5` and `claude-haiku-4-5` are accepted too. Anything else is refused when the config loads: the CLI takes whatever it is handed, so a typo would fail inside a session you have already paid for. Required rather than defaulted, because the CLI's own default moves between versions. | — |
| `max_usd_per_run` | no | Hard dollar cap inside one run, enforced by the CLI. It does not apply across runs — `max_runs_per_hour` is the only ceiling on how many there are. `0` removes it. | `5` |

Two runs killed by the spend cap in a row pause the worker: retrying unchanged
restarts the same task and hits the same wall at the same point.

## `runtime_config` for `codex`

| Key | Required | What it does | Default |
| --- | --- | --- | --- |
| `model` | **yes** | Which model to run. Write `sol`, `terra` or `luna` — each is **pinned** to an id (see [Aliases are pinned, not tracked](#aliases-are-pinned-not-tracked)), and the ids `gpt-5.6-sol`, `gpt-5.6-terra` and `gpt-5.6-luna` are accepted too. Anything else is refused when the config loads, for the same reason it is on claude. | — |
| `reasoning_effort` | no | How hard the model thinks before acting: `minimal`, `low`, `medium`, `high` or `xhigh`. The largest influence on what a run costs, and since codex enforces no spend cap it is the dial you have. | `medium` |
| `sandbox` | no | What a run may write. `read-only` writes nothing, `workspace-write` writes inside `repo_dir` and temporary directories, `danger-full-access` writes anywhere you can. Relay tools are unaffected — a `read-only` worker can still update a Task Context. | `workspace-write` |
| `network_access` | no | Whether commands the agent runs may reach the network. With it off, `git push`, dependency installs and anything else that talks to a server fail inside the run. | `true` |
| `web_search` | no | Whether the agent may search the web, as a claude worker can. | `true` |

**A codex worker has no per-run spend cap**, because the CLI has none: nothing
can cut a run off at a dollar figure. What bounds it is `max_seconds_per_run`,
`max_runs_per_hour`, `reasoning_effort`, and the plan limits of the account the
CLI is signed in as. A run stopped by those plan limits is reported the way a
spend cap is, and two in a row pause the worker.

Codex reports tokens rather than dollars, so that is what the dashboard and the
logs show for a codex worker.

## Model names

`model` is required for both runtimes and checked against a list for both. Two
things about that list are worth knowing before you write one.

### Aliases are pinned, not tracked

Write the short name. Both CLIs read a bare `sonnet` or `opus` as *the latest
model in that family*, so a worker configured that way in the CLI runs something
different the week after a new one ships — from a config nobody edited. relay-cli
resolves each short name to one pinned id instead, and hands the CLI that id:

| Write | It runs |
| --- | --- |
| `opus` | `claude-opus-5` |
| `sonnet` | `claude-sonnet-5` |
| `haiku` | `claude-haiku-4-5` |
| `sol` | `gpt-5.6-sol` |
| `terra` | `gpt-5.6-terra` |
| `luna` | `gpt-5.6-luna` |

What a short name means changes only when a release of relay-cli changes it, and
`relay check` and the dashboard show the resolved id. The codex tier names are
relay-cli's own: `codex --model` takes a full slug and nothing else.

### A model this build has not heard of

The model lists above are a **snapshot**. Neither CLI has a command that
enumerates its models, so relay-cli cannot ask yours what it accepts — the names
come from each vendor's own reference as they stood when this version was built.
A model released since then is valid in the CLI and unknown here:

```text
relay: ~/.relay/config needs 1 fix(es):
  worker "app-codex": runtime_config.model is "gpt-5.7-sol" — it takes one of: gpt-5.6-sol, gpt-5.6-terra, gpt-5.6-luna
      or one of these, which relay-cli pins to the id beside it: sol (gpt-5.6-sol), terra (gpt-5.6-terra), luna (gpt-5.6-luna)
      That is the list runtime "codex" offered when this relay-cli was built. If
      "gpt-5.7-sol" is newer than this build, set RELAY_CLI_SKIP_MODEL_CHECK=1 to run it anyway.
```

Set `RELAY_CLI_SKIP_MODEL_CHECK=1` and the name is passed to the CLI unchecked.
It is not silent: the start names which worker was let through, and with what —
past that point a typo fails once a cycle instead of once at startup.
`RELAY_CLI_SKIP_RUNTIME_CHECK=1` covers this too.

It applies to `model` on both runtimes and to nothing else. `sandbox` and
`reasoning_effort` are printed in codex's own `--help` and move only when the
CLI does, so a value outside those sets is simply wrong and no variable excuses
it.

## Safeguards

Defaults are already bounded — a worker configured no further than its required
fields is still a safe worker.

| Guard | Default | What it bounds |
|---|---|---|
| `max_runs_per_hour` | `12` | How many CLI sessions may *start*. Everything else bounds a single session. |
| `max_usd_per_run` | `5` | Spend inside one run. **claude only** — codex has no equivalent, so a codex worker is bounded by the row above and the row below. |
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
