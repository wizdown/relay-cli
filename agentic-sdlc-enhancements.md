# Agentic SDLC enhancements

An executable plan for making this repo easier for a coding agent to work in
without breaking a rule. It is the result of reading `AGENTS.md`, `CLAUDE.md`,
`docs/contributing/`, the hooks, the workflows and the tests as an agent would,
and noting where an agent still has to be trusted rather than checked.

Each item says why, what to add, and what "done" looks like. Items are grouped
by priority and sequenced into pull requests at the end. Nothing here changes
the binary.

## What already works

Keep these as they are. They are the reason the rest of this plan is short.

- **`AGENTS.md` is the single source of truth**, and `CLAUDE.md` only points at
  it. Both Claude Code and Codex read the same rules.
- **The docs are held to the code by tests**, not by review. A missing field,
  a broken link, a stale default or an over-long page fails `make check`.
- **The rules an agent breaks most often are enforced**: no credentials (hooks
  and CI), no build output (hook and test), no version bump (test), help on
  stdout (test).
- **`make check` is one command, needs only Go, and runs in about twenty
  seconds.** An agent can verify every change locally.
- **The contributor pages are procedures**, not tours. `config-fields.md` and
  `adapters.md` are already the shape a skill wants.

## The gaps

Where an agent is still on trust, in the order they bite:

| Gap | Where it is admitted | What happens today |
|---|---|---|
| The git hooks are opt-in per clone | [The git hooks](docs/contributing/development.md#the-git-hooks) | A fresh clone, which is every agent session, has `core.hooksPath` unset. The credential and build-output checks do not run until someone types `make hooks`. |
| Nothing scans a PR title or body for a credential | [Hard rules](AGENTS.md#hard-rules), rule 1 | The one place a pasted `relay check` failure lands unscanned is the one place that is world-readable the moment it is posted. |
| The credential pattern lives in three places | `.githooks/lib.sh`, `ci.yml` | `lib.sh` says two copies drift. `ci.yml` carries a third, inline. |
| No pull request template | none | The PR summary format is prose in `development.md`. An agent that opens a PR has to remember it. |
| The fresh-clone check is a command to copy | [The fresh-clone property](docs/contributing/development.md#the-fresh-clone-property) | It is not a `make` target, so nothing runs it and no agent is told to. |
| The repo ships none of what it teaches | [The working directory](docs/working-directory.md) | Steps 3 to 5 tell a user to give their agent skills, subagents and settings. This repo has no `.claude/` directory. |
| CI is manual only | [CI](docs/contributing/development.md#ci) | An agent driving a PR gets no signal on push. It has to know to dispatch the workflow, and needs `gh` or the Actions API to do it. |
| "Nothing checks what a sentence means" | [Documentation is part of the change](AGENTS.md#documentation-is-part-of-the-change) | Re-reading the touched pages is the one step left to the author. |

## Priority 1: close the admitted gaps

Small, mechanical, and each one removes a rule that is currently on trust.

### 1. One scanner, sourced everywhere

**Why.** `lib.sh` holds the connector pattern so the two hooks cannot
disagree, and `ci.yml` disagrees with it anyway by carrying its own copy. A
PR-body scan (item 2) would be a fourth.

**What.** Add `scripts/scan-secrets.sh`: sources `.githooks/lib.sh`, reads
stdin or the files named on the command line, prints any connector-shaped
string that is not a placeholder, exits 1 if it found one. Then:

- `.githooks/pre-commit` and `.githooks/commit-msg` pipe through it.
- The `secrets` job in `ci.yml` runs `git ls-files -z | xargs -0 scripts/scan-secrets.sh`
  instead of its inline pipeline.
- `development.md` names the script under [The git hooks](docs/contributing/development.md#the-git-hooks).

**Done when** `grep -rn 'wzh_' .github .githooks scripts` finds the pattern in
`lib.sh` and nowhere else, and both hooks and the CI job still refuse a
fixture secret.

### 2. Scan the PR title and body

**Why.** Rule 1 in `AGENTS.md` says "nothing scans a PR body, so read yours
before opening it". That is the sentence this item deletes.

**What.** Add `.github/workflows/pr-text.yml` on `pull_request` with types
`opened`, `edited`, `reopened` and `synchronize`. One job, no Go, a few
seconds of runner time. It passes `github.event.pull_request.title` and
`.body` through `env`, never interpolated into the script, and pipes them
into `scripts/scan-secrets.sh`. A hit fails the check with the same "revoke it
in relay before anything else" message the hooks print.

Update rule 1 in `AGENTS.md` and [Pull requests](docs/contributing/development.md#pull-requests)
to say what now scans a PR body, and that a red check means revoke first.

**Done when** a draft PR whose body contains a fixture secret shows a failing
check, and the wording "nothing scans a PR body" is gone from the repo.

### 3. Install the hooks at session start

**Why.** Every agent session is a fresh clone. This one had `core.hooksPath`
unset, so the pre-commit credential check would not have run.

**What.** Add `.claude/settings.json` with a `SessionStart` hook that runs
`make hooks`. It is idempotent and prints one line. In the same file, add a
permissions allowlist for the read-only and check commands an agent runs
constantly (`make check`, `make test`, `make lint-docs`, `make build`,
`go test`, `go vet`, `gofmt -l`, `git status`, `git diff`, `git log`) and a
deny list for `~/.relay/**` and `.relay/**`, so no session reads a live
config by accident.

Codex has no equivalent hook. Its `.codex/config.toml` is out of scope until
someone runs a codex worker against this repo; note that in the file's
comment rather than guessing at its syntax.

**Done when** a fresh clone opened in Claude Code shows
`git config core.hooksPath` as `.githooks` without anyone typing `make hooks`.

### 4. A pull request template

**Why.** The system prompt of every Claude Code session tells it to look for a
PR template and fill in its sections. The repo has a PR format and no
template, so the format is applied from memory.

**What.** Add `.github/pull_request_template.md` with the five headings from
[Pull requests](docs/contributing/development.md#pull-requests): what changed
and why; anything deleted and why it was safe; behaviour changes for existing
users; verification actually run; what was left out. One line under each
saying what goes there, and one line at the top: no connector URL, no
hostname, no absolute path. Link `development.md` instead of restating it.

**Done when** the template exists, links resolve (the doc tests walk every
`.md` file, including this one), and `development.md` points at it.

### 5. `make check-fresh`

**Why.** The fresh-clone property is the property CI exists to prove, and the
local check for it is a command to copy out of a page.

**What.** Add a `check-fresh` target that runs `go test ./...` with `PATH`
reduced to `/usr/bin:/bin` and the Go toolchain's directory, exactly as
`development.md` shows. List it in `make help`, in the commands block of
`AGENTS.md` and `development.md`, and have the fresh-clone section call it
instead of quoting the command.

**Done when** `make check-fresh` passes on a machine with a coding CLI
installed and the copied command is gone from `development.md`.

## Priority 2: give the agent the procedures it keeps needing

`docs/working-directory.md` tells a user what a skill, a subagent and a
settings file do for an agent. This repo should carry the same for itself.

The one rule for every file under `.claude/`: **it links, it does not
restate.** `AGENTS.md` says two files with overlapping instructions become two
files that disagree. A skill here is a trigger, an ordered list of commands to
run, and a link to the contributing page that holds the reasons. The doc tests
already walk `.md` files under `.claude/`, so a link that rots fails the build.

### 6. Skills

Add `.claude/skills/<name>/SKILL.md` for the procedures an agent is asked for
repeatedly. Each has frontmatter (`name`, `description` that names the trigger)
and a body under 60 lines.

| Skill | Triggers on | Body |
|---|---|---|
| `config-field` | adding, renaming, removing or defaulting a config field | the loop in [Changing the config](docs/contributing/config-fields.md), as steps with `make lint-docs` between them, and the test names that fail when a step is missed |
| `add-runtime` | a new coding CLI, a new adapter | the six methods in [Adapters](docs/contributing/adapters.md), then `supportedRuntimes()`, then `make check` to be told which docs are owed |
| `docs-change` | any change a user notices | walk the "If you changed…" table in [Documentation is part of the change](AGENTS.md#documentation-is-part-of-the-change); end by re-reading each touched page against [How a sentence reads](AGENTS.md#how-a-sentence-reads) |
| `pre-pr` | before opening or updating a PR | [Before you commit](AGENTS.md#before-you-commit) as commands: `make check`, `make check-fresh`, `git diff origin/master -- cmd/relay-cli/main.go` shows no version change, `git status` shows no binary, and the PR title and body piped through `scripts/scan-secrets.sh` |
| `release` | "cut a release", "tag", "bump" | run `make release` bare, show the output, and stop. The version is the user's to choose. Link [Cutting a release](docs/contributing/development.md#cutting-a-release) |
| `steward` | driving a PR to green after it is opened | CI is manual: run `make check` and `make check-fresh` locally, dispatch `ci.yml` on the branch if the session can, and say in the PR which of the two it ran. Never touch the version constant. Never skip a doc test to get green; fix the page it names |

`steward` is the name the Claude Code PR-driving harness looks for under
`.claude/skills/`, so an agent watching a PR here reads it before acting on a
CI event.

**Done when** each skill exists, every link in it resolves, and a test in
`repo_test.go` asserts that every `SKILL.md` has a `name` and a `description`
and that `.claude/settings.json` parses. That test is the backstop for a
skill that is added without a trigger.

### 7. A docs-reviewer subagent

**Why.** The tests hold the docs to the code; nothing holds a sentence to the
style. `AGENTS.md` leaves that to "re-read the pages your change touches".

**What.** Add `.claude/agents/docs-reviewer.md`: read-only tools, invoked on
the pages a diff touched. It checks each changed paragraph against
[Who reads what](AGENTS.md#who-reads-what) and
[How a sentence reads](AGENTS.md#how-a-sentence-reads): is the fact in the
right tier, is it stated once, does it lead with the fact, is there a
rationale word on a user page. It reports, it does not edit. The `docs-change`
skill invokes it as its last step.

**Done when** the agent exists and running it on a deliberately bad paragraph
(a "deliberately" in `docs/cli.md`) names the sentence and the rule.

## Priority 3: decisions for the owner

These change a stated preference, so they are proposals, each with the fact
that argues for it. Decide, then do or delete.

### 8. Run CI on pull requests

**The preference.** `ci.yml` is manual because "runner time is a cost this
project does not want to spend on every commit".

**The fact.** GitHub-hosted standard runners are free for public repositories.
The whole workflow runs in about a minute.

**The proposal.** Add `pull_request` to the `on:` block, with a `concurrency`
group keyed on the PR so a new push cancels the last run, and keep
`workflow_dispatch` for the clean-machine second opinion on any branch. An
agent driving a PR then gets a signal on every push, and the fresh-clone
property is proved on every PR instead of when someone remembers.

If the answer is no, item 6's `steward` skill already tells the agent to
dispatch the workflow itself, and this item closes.

### 9. Trim `AGENTS.md` to what only it says

**Why.** The commands block appears verbatim in `AGENTS.md` and
`development.md`, and the hook description is in both. The file is the first
thing loaded in every session; every duplicated line is paid for twice, and
the rule against two homes for one fact applies to it too.

**What.** Keep the commands block in `AGENTS.md`, since an agent needs it
before it reads anything else, and cut it from `development.md` to one line
and a link. Move the paragraph on the hooks the same way. Add one short
section, "For agents", that names `.claude/skills/`, `make check-fresh`, and
the PR-text scan. Under 20 lines net removed; the point is one home, not a
shorter file.

**Done when** `make check` passes and no paragraph in `AGENTS.md` is a copy of
one in `docs/contributing/`.

## Sequencing

One concern per PR, in the order that lets each one be verified by the one
before it:

| PR | Items | Verifies with |
|---|---|---|
| A | this plan | `make check` (links, headings) |
| B | 1, 2, 4 | a draft PR with a fixture secret in its body goes red; `ci.yml` still refuses a fixture in a file |
| C | 3, 5 | a fresh clone in Claude Code has the hooks installed; `make check-fresh` passes |
| D | 6, 7 and their test | `make check`; each skill invoked once by hand |
| E | 9 | `make check` |
| F | 8, if accepted | the next PR runs CI without being asked |

B before C because C's `pre-pr` step pipes into the scanner B adds. D after C
because the `pre-pr` and `steward` skills call `make check-fresh`.

## Out of scope

- **Anything in the binary.** No config field, no flag, no adapter.
- **Codex configuration.** Codex reads `AGENTS.md` and gets everything that
  matters; a `.codex/` directory waits for a codex worker on this repo.
- **Automating the release.** The version is never guessed, including by a
  workflow.
