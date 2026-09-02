# The working directory

Every worker has a `repo_dir`, and the coding CLI starts inside it. Relay tells
the agent *what* to do; this directory decides *what it can do it with*.

The steps below are written for Claude Code. A `codex` worker reads the same
directory in its own layout; see [What codex loads](#what-codex-loads).
An empty directory already works, and every later step is optional.

## Step 0: an empty directory

```bash
mkdir -p ~/relay/hello
```

```jsonc
"repo_dir": "~/relay/hello"
```

That is a complete setup. The agent arrives with its task from Relay, the
ordinary tools (read, write and edit files, run commands, search the web), and
the [harness rules](configuration.md#harness-rules). It has never seen your
code and remembers nothing from the last run.

This is enough for self-contained work: drafting a document, research, or
orchestrating subtasks handed to other agents.

> A run is autonomous and cannot ask before changing files. Point `repo_dir`
> somewhere you are willing to have rewritten.

## Step 1: a real checkout

```jsonc
"repo_dir": "~/code/app"
```

Now the agent can read and change your code. It still knows nothing about the
project beyond what it finds by looking, and it will guess your test command.

## Step 2: `CLAUDE.md`

`CLAUDE.md` in the working directory is read at the start of every run. One
file, and every future run inherits it.

```markdown
# app

Go service, one module. `make test` before anything is done; it is fast.

- Migrations live in `db/migrations/`. Never edit one that has shipped; add a new one.
- `internal/billing/` is under audit. Don't touch it without saying so in the task.
- Branch as `agent/<short-description>`. Never commit to `master`.
```

Write what a new colleague needs on day one: commands, conventions, the places
that bite. Not a tour of the tree, which the agent can read itself. Keep *who
the agent is* out of it; its role and remit belong on the Relay agent's
instructions, which reach a session already running. A `CLAUDE.md` can pull
in other files with `@path/to/file.md`.

## Step 3: skills

A skill is a named procedure the agent pulls in when relevant. Add one for a
job with steps you would otherwise re-explain in every task.

```
~/code/app/
├── CLAUDE.md
└── .claude/skills/release-check/SKILL.md
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

The `description` is what the agent matches against, so write it as *when to
use this*.

## Step 4: subagents

A subagent is a separate context the agent can hand a slice of work to, such as
a reviewer or an explorer.

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

## Step 5: `.claude/settings.json`

Two parts of this file apply to a worker run:

```jsonc
{
  "env": { "APP_ENV": "test" },
  "hooks": {
    "PostToolUse": [
      { "matcher": "Edit|Write",
        "hooks": [{ "type": "command", "command": "gofmt -w ." }] }
    ]
  }
}
```

- **`env`**: variables every command in the run sees, such as a test database.
- **`hooks`**: run unattended on every session. Useful for formatting after an
  edit; be deliberate about anything else.

**`permissions` in this file has no effect on a worker.** A headless run skips
the workspace-trust step, so relay-cli pre-allows Relay's tools and the
ordinary coding tools instead. Your personal `~/.claude/settings.json` still
applies, and a `deny` rule there still wins.

## What codex loads

| Step | claude | codex |
|---|---|---|
| Tell it about the project | `CLAUDE.md` | `AGENTS.md` |
| Give it a procedure | `.claude/skills/<name>/SKILL.md` | not supported |
| Give it a specialist | `.claude/agents/<name>.md` | `.codex/agents/<name>.toml` |
| Give it an environment | `.claude/settings.json` | `.codex/config.toml` |

A project `.codex/config.toml` applies even though your own
`~/.codex/config.toml` does not. What a codex worker may write is
`runtime_config.sandbox` and `network_access`, not a rules file; see
[Runtimes](runtimes.md#what-a-codex-run-does).

## What does not load: MCP servers

An `.mcp.json` in the working directory and the MCP servers in your own CLI
config are both ignored. The session's only MCP server is this worker's Relay
connector. If a task needs a service, give the agent a script or a CLI it can
run instead.

## What comes along from your machine

A claude worker still sees the skills, subagents and permission rules in
`~/.claude/`. A codex worker sees nothing from `~/.codex/` except your
sign-in. To make a worker behave the same on another machine, keep what it
depends on in the working directory.

## Checking what landed

Nothing here fails loudly. A `CLAUDE.md` one directory up, or a skill outside
`<name>/SKILL.md`, produces a worker that runs and knows none of it.
`relay check` prints what each worker will actually find:

```text
  wizhub-claude            ok    queue: resume 0 · attention 1 · todo 0
    repo /Users/you/code/wizhub   CLAUDE.md · 2 skills · 1 subagent · 1 hook
```

If you wrote a file and it is not on that line, the agent will not see it.

## The whole ladder

| You want the agent to… | Add |
|---|---|
| do self-contained work | nothing: an empty directory |
| work on your code | a `repo_dir` that is the checkout |
| know your conventions and commands | `CLAUDE.md` |
| follow a repeatable procedure | `.claude/skills/<name>/SKILL.md` |
| delegate a step to a specialist | `.claude/agents/<name>.md` |
| run with particular env vars, or hooks | `.claude/settings.json` |
| reach another MCP server | not supported |
| follow different harness rules, fleet-wide | `~/.relay/worker-rules.md`; see [Harness rules](configuration.md#harness-rules) |

Two workers may share one directory, but they share the working tree. Keep
their tasks on separate branches, or give each its own clone. See
[More than one worker](configuration.md#more-than-one-worker).
