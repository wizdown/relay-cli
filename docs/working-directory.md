# The working directory

Every worker has a `repo_dir`, and the coding CLI starts inside it. That
directory is the whole of what the agent knows about your world: relay tells it
*what* to do, and this directory decides *what it can do it with*.

`claude` is the only supported runtime, so this page is about preparing a
directory for [Claude Code](https://claude.com/claude-code) — the same
`CLAUDE.md`, skills and subagents you would use yourself.

Read it as a ladder. An empty directory already works; everything after the
first step is optional, and you add a rung only when you want the capability it
gives.

## Step 0 — an empty directory

A worker needs a directory to exist. It does not need anything in it.

```bash
mkdir -p ~/relay/hello
```

```jsonc
"repo_dir": "~/relay/hello"
```

That is a complete, working setup. The agent arrives with:

- **its task**, from relay — the description, the Task Context, and your
  standing instructions for that agent;
- **the ordinary tools** — read, write and edit files, run shell commands,
  search the web;
- **the harness contract** — a few rules about the process it is running in,
  which relay cannot know: one task per session, and stop rather than invent
  work when the queue is empty. Same text for every worker and every runtime,
  compiled into the binary so a downloaded `relay` carries it;
- **nothing else**. It has never seen your codebase and holds no memory of the
  last run.

A `worker-rules.md` in `~/.relay/` replaces that contract — for *every* worker
on the machine, which is what makes it the wrong place for anything about one
agent. That belongs on the relay agent, where it reaches a session already
running.

This is enough for self-contained work: drafting a document, doing research,
or orchestrating subtasks it hands to other agents. An agent that never touches
code can stop reading here.

> Point `repo_dir` somewhere you are willing to have rewritten. The run is
> autonomous, and there is no prompt for it to ask you first.

## Step 1 — point it at a real checkout

Swap the empty directory for the repository the agent should work in.

```jsonc
"repo_dir": "~/code/app"
```

Now the agent can read and change your code — but it still knows nothing about
it beyond what it can find by looking. It will guess your test command.

## Step 2 — tell it about the project: `CLAUDE.md`

`CLAUDE.md` in the working directory is read at the start of every run. It is
the highest-value thing on this page: one file, and every future run inherits
it.

```
~/code/app/
└── CLAUDE.md
```

Write what a new colleague would need on day one:

```markdown
# app

Go service, one module. `make test` before anything is done — it is fast.

- Migrations live in `db/migrations/`. Never edit one that has shipped; add a new one.
- `internal/billing/` is under audit. Don't touch it without saying so in the task.
- Branch as `agent/<short-description>`. Never commit to `master`.
```

Two rules keep it useful:

- **Say what is not obvious from the code.** The commands, the conventions, the
  places that bite. Not a tour of the directory tree — the agent can read that.
- **Keep *who the agent is* out of it.** Its role, its remit, what it may decide
  alone — that belongs on the relay agent's own instructions, because relay
  delivers those to a session already running, and an edit there reaches the
  next run without touching this machine.

A `CLAUDE.md` can pull in other files with `@path/to/file.md`, so shared house
rules do not have to be copied into it.

## Step 3 — give it a procedure: skills

A skill is a named procedure the agent can pull in when it is relevant. Add one
when there is a job with steps you would otherwise re-explain in every task.

```
~/code/app/
├── CLAUDE.md
└── .claude/
    └── skills/
        └── release-check/
            └── SKILL.md
```

```markdown
---
name: release-check
description: Verify a release candidate before tagging. Use when a task asks to cut, tag or verify a release.
---

1. `make test` and `make build` both pass on a clean tree.
2. `CHANGELOG.md` has an entry for the version in `version.go`.
3. No `TODO(release)` markers remain: `grep -rn "TODO(release)" .`

Report each check as pass or fail. Never tag anything yourself.
```

The `description` is what the agent matches against, so write it as *when to use
this*, not as a title. Skills in this directory load exactly as they do in your
own sessions.

## Step 4 — give it a specialist: subagents

A subagent is a separate context the agent can hand a slice of work to. It is
worth adding when a task has a step better done by something with its own
instructions and its own blank slate — a reviewer, an explorer.

```
.claude/agents/reviewer.md
```

```markdown
---
name: reviewer
description: Reviews a diff for correctness bugs. Returns findings, changes nothing.
---

You review Go diffs. Report only defects you can name a failing input for.
Do not edit files.
```

## Step 5 — give it an environment: `.claude/settings.json`

Two parts of this file apply to a worker run:

```jsonc
{
  "env": {
    "APP_ENV": "test"
  },
  "hooks": {
    "PostToolUse": [
      { "matcher": "Edit|Write",
        "hooks": [{ "type": "command", "command": "gofmt -w ." }] }
    ]
  }
}
```

- **`env`** — variables every command in the run sees. The usual reason to reach
  for it is pointing the agent at a test database rather than a real one.
- **`hooks`** — they run, unattended, on every session. That is useful for
  formatting after an edit, and it is worth being deliberate about: a hook here
  fires with nobody watching it.

**`permissions` in this file has no effect on a worker.** A headless run skips
the workspace-trust step, so the directory's own permission rules are ignored.
What the agent is allowed to do is decided by relay-cli, which pre-allows
relay's tools and the ordinary coding tools before the session starts. Rules in
your personal `~/.claude/settings.json` do still apply, and a `deny` rule there
still wins.

## What a worker will not pick up: MCP servers

This is the one thing in the directory that does not load. A worker session runs
with `--strict-mcp-config`, which means:

- an `.mcp.json` in the working directory is **ignored**;
- the MCP servers in your own Claude Code config are **ignored**.

The session's only MCP server is this worker's relay connector. An unattended
agent gets the credentials the operator chose for it deliberately, not whatever
happens to be configured on the machine it runs on. If a task needs a service,
give the agent a script or a CLI it can run instead.

## What comes along from your machine

The isolation above is about MCP servers only. Your personal Claude Code setup
is otherwise still there: skills and subagents in `~/.claude/` are visible to
the worker, and so are the permission rules in `~/.claude/settings.json`.

If you want a worker to behave the same way on someone else's machine, keep what
it depends on in the working directory.

## Checking what landed

Nothing here fails loudly. A `CLAUDE.md` written one directory up, or a skill in
a folder that isn't `<name>/SKILL.md`, produces a worker that starts, runs, costs
money and knows none of it. `relay check` says what each worker would actually
find, before anything launches:

```text
  wizhub-claude            ok    queue: resume 0 · attention 1 · todo 0
    repo /Users/you/code/wizhub   CLAUDE.md · 2 skills · 1 subagent · 1 hook
```

It counts only what the CLI itself loads, so if you wrote a file and it is not
in that line, the agent will not see it either.

## The whole ladder

| You want the agent to… | Add |
|---|---|
| do self-contained work | nothing — an empty directory |
| work on your code | a `repo_dir` that is the checkout |
| know your conventions and commands | `CLAUDE.md` |
| follow a repeatable procedure | `.claude/skills/<name>/SKILL.md` |
| delegate a step to a specialist | `.claude/agents/<name>.md` |
| run with particular env vars, or hooks | `.claude/settings.json` |
| reach another MCP server | not supported — see above |
| follow different harness rules, fleet-wide | `~/.relay/worker-rules.md` — not in `repo_dir`; see [Step 0](#step-0--an-empty-directory) |

Two workers may share one directory, but they share the working tree with it:
keep their tasks on separate branches, or give each its own clone. See
[Configuration](configuration.md#more-than-one-worker) for the fleet side of
that.
