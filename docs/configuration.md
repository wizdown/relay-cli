# Configuration

`~/.relay/config` is one JSON file listing your workers. `//` comments are
allowed. `relay init` writes a starting copy: one worker per coding CLI found
on `PATH`, plus a commented-out worker for each CLI it did not find. With
neither CLI installed it writes nothing and says which to install.

Everything relay-cli owns lives beside the config, and no flag moves it:

```text
~/.relay/
  config           the worker list. 0600; every relay_mcp in it is a secret
  state/           per-worker runtime state. Rebuilt on start, removed on shutdown
  logs/            archived sessions
  worker-rules.md  optional; see Harness rules below
```

## Example

```jsonc
{
  "poll_seconds": 30,                 // fleet-wide, optional

  "workers": [
    {
      // relay-cli's own fields, with the same meaning for every runtime
      "name":      "wizhub-claude",
      "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_REPLACE_ME",
      "repo_dir":  "/path/to/your/repo",
      "runtime":   "claude",
      "max_runs_per_hour":   6,
      "max_seconds_per_run": 900,

      // settings of the runtime named above
      "runtime_config": {
        "model":           "sonnet",
        "max_usd_per_run": 5
      }
    }
  ]
}
```

Fields outside `runtime_config` are enforced by relay-cli. Fields inside
belong to the named CLI, and a key that CLI does not accept is rejected when
the config loads.

## Fleet fields

| Field | Required | What it does | Default |
| --- | --- | --- | --- |
| `poll_seconds` | no | Seconds between polls, for every worker. Minimum `5`; a lower value is rejected. | `30` |

## Worker fields

| Field | Required | What it does | Default |
| --- | --- | --- | --- |
| `name` | **yes** | Unique, one path segment. Names `~/.relay/state/<name>/`, the log and the dashboard row. `<repo>-<runtime>` reads well. | — |
| `relay_mcp` | **yes** | The `connector_url` Relay issued for this agent, as a full `http(s)` URL. Unique per worker; two workers must never share one. | — |
| `repo_dir` | **yes** | Absolute path (`~` is expanded) of an existing directory the CLI runs in. Its `CLAUDE.md` or `AGENTS.md`, skills and tooling load as they would for you; see [The working directory](working-directory.md). An empty directory is valid. A run cannot ask before changing files, so choose a checkout you are willing to have rewritten. | — |
| `runtime` | **yes** | `claude` or `codex`. See [Runtimes](runtimes.md). | — |
| `max_runs_per_hour` | no | CLI launches per rolling hour, not polls. The ceiling that caps spend. `0` removes it. | `12` |
| `max_seconds_per_run` | no | Wall-clock kill for one session, and the only guard that catches a hung run. At least `30` when set; `0` removes it. | `900` |
| `runtime_config` | depends | Settings of the named runtime. Required for both `claude` and `codex`, because each requires `model`. | — |

## `runtime_config` for `claude`

| Key | Required | What it does | Default |
| --- | --- | --- | --- |
| `model` | **yes** | `opus`, `sonnet` or `haiku`, or the full ids `claude-opus-5`, `claude-sonnet-5`, `claude-haiku-4-5`. See [Model names](#model-names). | — |
| `max_usd_per_run` | no | Dollar cap inside one run, enforced by the CLI. `0` removes it. | `5` |

Two runs in a row killed by the spend cap pause the worker.

## `runtime_config` for `codex`

| Key | Required | What it does | Default |
| --- | --- | --- | --- |
| `model` | **yes** | `sol`, `terra` or `luna`, or the full ids `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`. See [Model names](#model-names). | — |
| `reasoning_effort` | no | `minimal`, `low`, `medium`, `high` or `xhigh`. The largest influence on what a run costs. | `medium` |
| `sandbox` | no | What a run may write: `read-only`, `workspace-write` (inside `repo_dir` and temp directories) or `danger-full-access`. Relay tools are unaffected. | `workspace-write` |
| `network_access` | no | Whether commands the agent runs may reach the network. Off, `git push` and dependency installs fail. | `true` |
| `web_search` | no | Whether the agent may search the web. | `true` |

**Codex has no per-run spend cap**, because the CLI has none. A codex worker is
bounded by `max_seconds_per_run`, `max_runs_per_hour`, `reasoning_effort` and
the plan limits of the signed-in account. Two runs in a row stopped by those
plan limits pause the worker. Codex reports tokens rather than dollars, and
that is what the dashboard and logs show.

## Model names

`model` is required on both runtimes. Write the short name and relay-cli pins
it to one id, so what a worker runs changes only when a relay-cli release
changes it. The full ids are accepted too. Logs and the dashboard show the
resolved id.

| Write | It runs |
| --- | --- |
| `opus` | `claude-opus-5` |
| `sonnet` | `claude-sonnet-5` |
| `haiku` | `claude-haiku-4-5` |
| `sol` | `gpt-5.6-sol` |
| `terra` | `gpt-5.6-terra` |
| `luna` | `gpt-5.6-luna` |

Any other name is rejected when the config loads. The lists are what each
vendor offered when this version was built. For a model newer than that, set
`RELAY_CLI_SKIP_MODEL_CHECK=1`: the name is passed to the CLI unchecked, and
the start prints a warning naming the worker. This applies to `model` only.

## Safeguards

A worker configured no further than its required fields is bounded.

| Guard | Default | What it bounds |
|---|---|---|
| `max_runs_per_hour` | `12` | How many CLI sessions may start. |
| `max_usd_per_run` | `5` | Spend inside one run. claude only. |
| `max_seconds_per_run` | `900` | Wall-clock for one run. |
| relaunch cooldown | 60s, fixed | No two launches back to back. |

Set any of the first three to `0` to remove it.

Every ceiling counts runs, not polls. A poll is one HTTP request and runs no
model. A worker at its ceiling keeps polling and logs why it is not launching:

```text
2026-02-04T11:41:03Z [wizhub-claude] run ceiling reached (6/6 in the last hour) — not launching
```

Guards that need no config:

| Guard | Effect |
|---|---|
| Empty queue | No launch. An idle worker costs one HTTP request per poll. |
| `mkdir` lock | One cycle per worker at a time. |
| Probe breaker | 10 consecutive probe failures (revoked credential, dead host) pause the worker. |
| Budget breaker | 2 consecutive spend-cap or plan-limit kills pause the worker. |
| Attention-stall breaker | 3 consecutive completed runs that leave the same task needing this agent's attention pause the worker. Resolve the task in Relay. |
| One task per cycle | A session ends at a hand-back. |

Each breaker writes `~/.relay/state/<name>/PAUSED` naming its own fix. Remove
the file to resume. Every start clears `state/`, including `PAUSED` files,
breaker counters and the rolling-hour count.

## More than one worker

A fleet is more entries in `workers`. There is no fleet mode.

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

The agent you delegate a task to decides which worker runs it, so which
checkout and which CLI. What an agent is for (its instructions, capabilities
and claim limits) is set on the Relay agent, not here; see the
[Relay docs](https://relay.bytecurio.com/).

- Never point two workers at one `relay_mcp`. It is rejected at startup.
- Two workers on one `repo_dir` share a working tree. Keep their tasks on
  separate branches, or give each its own clone.
- An orchestrator that writes nothing still needs its own `repo_dir`. An empty
  directory is enough.
- Ceilings are per worker. There is no fleet-wide budget.

## Removing a worker

Delete its entry, remove `~/.relay/state/<name>`, and revoke its credential in
Relay. Deleting the config does not invalidate a credential. To remove
everything, revoke every credential, then `rm -rf ~/.relay/` and the binary.
Archived logs can contain anything the agents read.

## Harness rules

Every session receives a short set of rules about the process it runs in: one
task per session, no memory across runs, carry `claim_seq` on every mutating
call, and end the session while subtasks are still working rather than
releasing the task. The text is compiled into the binary. A
`~/.relay/worker-rules.md` replaces it for every worker on this machine.
Instructions for one agent belong on the Relay agent instead, where they reach
a session already running.

## Validation

`relay run` and `relay check` collect every problem in the file and report
them together. The config is rejected when:

- there is no config, or `workers` is missing or empty
- a key is one this version does not accept, at any level. The error names the
  key you probably meant, or where the setting belongs. A key this version
  removed is rejected with what to use instead.
- a required field is missing, or still holds an `init` placeholder
- a value is the wrong type, `relay_mcp` is not an `http(s)` URL, `repo_dir` is
  relative or not a directory, `poll_seconds` is below `5`, a ceiling is
  negative or fractional, or a set `max_seconds_per_run` is below `30`
- a `name` or `relay_mcp` repeats
- a runtime the config names is not usable on this machine; see
  [The startup check](runtimes.md#the-startup-check)

`relay check` then probes each credential over HTTP without launching
anything:

```text
  wizhub-claude            ok    queue: resume 0 · attention 0 · todo 0
```

`ok` with a queue of `0` means the credential works and no work is waiting.
`FAIL … HTTP 401` means the URL is wrong or the credential was revoked.
