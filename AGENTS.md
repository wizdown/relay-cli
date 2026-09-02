# Working in this repo

`relay-cli` is a worker that connects to a Relay MCP server, claims one task,
does it, and posts the result back. Relay owns the task state machine and the
agent roster; nothing here tracks task state. One Go binary, one module, one
package: a poller that drives a local coding CLI inside a checkout. It is beta
and stays 0.x until the interface settles.

## Commands

Run from the repository root:

```bash
make check    # gofmt + vet + test. Run before any PR
make test     # tests only
make fmt      # gofmt -w .
make build    # build ./relay
make hooks    # once per clone: install the git hooks

make release VERSION=x.y.z   # cut a release; see docs/contributing/development.md
```

Go 1.22+, no other dependencies, no network. A fresh clone passes its tests
with no coding CLI installed; keep it that way. See
[The fresh-clone property](docs/contributing/development.md#the-fresh-clone-property).

## Hard rules

1. **No credentials, anywhere.** Every `relay_mcp` is a live secret. Not in a
   file, a test, a commit message, a PR title or body, or a release note. Use
   `relay.example.com` and `wzh_REPLACE_ME`; describe a failure as "HTTP 401
   from the configured endpoint". The hooks and `ci.yml` scan files and commit
   messages; nothing scans a PR body, so read yours before opening it.
2. **Do not touch the version constant.** `master` carries the next version
   with a `-SNAPSHOT` marker, and only `make release` changes it.
3. **Documentation lands in the same commit as the change.** See
   [Documentation rules](#documentation-rules).
4. **The dashboard is read-only.** No route may start a run, pause a worker or
   edit a ceiling.
5. **Runtime-specific behaviour lives in an adapter**, never in the worker
   loop. `ResolveRuntime` is the one place a runtime name is a string.
6. **Everything printed, logged, served or returned goes through `Scrub`.**
7. **A doc link the binary prints is a full URL** built from `docsBase`. The
   default branch is `master`; `blob/main/` is a 404. Both are tested.
8. **A new ceiling defaults to a bound**, never to unlimited.
9. **Agent identity is Relay's.** Never add a config field that duplicates
   `instructions_md`, capabilities or claim limits. Five such keys were removed
   and are rejected by name.
10. **`worker-rules.md` carries only what this harness adds.** Relay serves its
    own workflow; do not copy it here.
11. **Comments explain why.** The reason a ceiling exists is more useful than
    its type.

## The code

One package, `cmd/relay-cli/`, one job per file. The codemap is
[Directory layout](docs/contributing/design.md#directory-layout).

Four config fields are required (`name`, `relay_mcp`, `repo_dir`, `runtime`)
plus `runtime_config.model`. Fields outside `runtime_config` are enforced by
relay-cli; fields inside are one CLI's vocabulary, declared by that adapter's
`ConfigFields()`. Adding, renaming, removing or defaulting a field is a loop
across code, docs and `helpText`, and tests name what you missed:
[Changing the config](docs/contributing/config-fields.md).

Both shipped runtimes are native adapters. `claude` has a per-run spend cap
the CLI enforces; `codex` has none, and every page comparing them says so.
`model` is required on both and pinned to one id. The bash bridge is complete,
tested and gated off by `bashAdaptersEnabled`; keep it compiling. The
contract is [Adapters](docs/contributing/adapters.md), and the reasons behind
the startup check and the codex trades are in
[Design](docs/contributing/design.md).

## Documentation rules

### Who reads what

Two readers, two tiers, and a sentence belongs to exactly one:

| Reader | Wants | Tier |
|---|---|---|
| A user | to get a worker running, look up a field, or fix a symptom | `readme.md`, `docs/*.md`, `helpText` |
| A contributor | to change the code without breaking a rule | this file, `docs/contributing/` |

Where a sentence belongs:

| Question | Where it is answered |
|---|---|
| How do I install and run one worker? | `readme.md`, and nothing else lives there |
| What does this config field do? | `docs/configuration.md`, the only field reference |
| Which runtime should I pick, and what bounds it? | `docs/runtimes.md` |
| What commands and flags exist? What does the dashboard show? | `docs/cli.md` |
| What do I put in `repo_dir`? | `docs/working-directory.md` |
| Why isn't it working? | `docs/troubleshooting.md` |
| What changed since the last release? | `CHANGELOG.md`, under Unreleased |
| How does Relay work: tasks, agents, capabilities, leases? | Not here. Link <https://relay.bytecurio.com/> |
| Why is it built this way? | `docs/contributing/design.md` |
| How do I build, test, release, or change the config or an adapter? | the other `docs/contributing/` pages |

If you cannot find where a sentence belongs, fix this table rather than
dropping the sentence into the nearest page.

### What a user page says

- **What a thing is and what it defaults to.** The reason it works that way
  goes in `docs/contributing/design.md`, or nowhere. The words "deliberately",
  "on purpose" and "not incidental" do not appear in a user page.
- **One home per fact.** State it in full once. Everywhere else, one clause
  and a link. Before writing a paragraph, grep for the fact; if it exists,
  link it.
- **Present tense.** No "no longer accepted", no "previously", no migration
  notes. A removed key explains itself in the error (`removedKeys`) and in
  `CHANGELOG.md`.
- **Delete rather than caveat.** A section that no longer describes the code
  is removed, not annotated.
- **Do not explain Relay.** Anything configured on the Relay agent is Relay's
  to document. Say what this CLI does with it and link out.

### How a sentence reads

- Lead with the fact. "`poll_seconds` cannot go below 5", not "The one bound
  that is not yours to remove is the floor under `poll_seconds`".
- One idea per sentence, under 20 words on average. A sentence that restates
  the previous one as a maxim is deleted.
- At most one em-dash per paragraph. Prefer a full stop.
- Name the command, flag or file the reader has to type, and nothing they do
  not. Describe the rest in words.
- Bold the first words of a bullet, never a whole sentence. Use a table for
  parallel facts (fields, flags, symptoms) and prose for a line of argument.
- Headings are plain words. Punctuation is stripped from the anchor, so
  `## Step 0: an empty directory` links as `#step-0-an-empty-directory`;
  an em-dash in a heading leaves a double hyphen behind.

Before and after, from the pages as they were:

| Was | Is |
|---|---|
| "Both placeholders are rejected by name, so an unfinished config fails in `check` rather than inside a run you have already paid for." | "`relay check` rejects a config that still contains either placeholder." |
| "Starting is asked for by name rather than being the default, because it launches autonomous sessions that spend money." | "`relay` with no command prints help. Use `relay run` to start." |

### The shape of each page

| Page | Shape | Ceiling |
|---|---|---|
| `readme.md` | quickstart: what it is, requirements, install, four steps, stop, the doc table | 700 words |
| `docs/configuration.md` | reference: layout, one example, field tables, safeguards, short sections after | 1,700 |
| `docs/runtimes.md` | one comparison table, the startup check, what each run does | 700 |
| `docs/cli.md` | commands, flags, sample output, what the dashboard shows, versioning | 1,000 |
| `docs/working-directory.md` | a ladder: each step adds one thing and shows it | 1,150 |
| `docs/troubleshooting.md` | tables grouped by where the reader is, one row per message | 1,100 |
| `docs/contributing/*` | the reasons and the procedures, as long as they need to be | none |

The ceilings are in `TestUserPagesStayShort` in `docs_pages_test.go`, and a
page past its ceiling fails the build. Cut a duplicate or move a rationale
before raising one. A new user page gets a row in this table, a row in the
readme's Documentation table, and a ceiling in the test, in the same commit.

### Quoting output and linking

- **Quote what the binary prints**, verbatim. A troubleshooting row starts
  with the string the reader will see, and that string exists in a non-test
  Go file. Placeholders are `relay.example.com`, `wzh_REPLACE_ME` and
  `/path/to/your/repo`; the config example in `docs/configuration.md` must
  load once those are filled in, and a test loads it.
- **Sample output quotes one version** across every page, never ahead of the
  constant. `make release` rewrites it. Do not type a version by hand.
- **Links between markdown files are relative** and point at a file or a
  heading that exists; a test walks them. **Links the binary prints are full
  URLs** from `docsBase`, because the reader may hold only the binary.
- **Link the Relay product** at <https://relay.bytecurio.com/>; never quote a
  connector URL.

### Documentation is part of the change

A behaviour change and its documentation land in the same commit. Walk this
table for what you touched:

| If you changed… | Update in the same commit |
|---|---|
| a config field or its default | the tables in `docs/configuration.md`, the `THE CONFIG FILE` block in `helpText`, and `config-fields.md` if the loop moved |
| a field you removed | delete every mention from `docs/configuration.md` and `helpText`; add it to `removedKeys` with what to use instead |
| a command or a flag | `shortHelp` and `helpText`, `docs/cli.md`, and the readme if it appears there |
| a safeguard, ceiling or breaker | the safeguards tables in `docs/configuration.md` |
| what the dashboard shows or serves | `docs/cli.md` |
| a runtime's support status or its startup check | `docs/runtimes.md` |
| what a session picks up from `repo_dir` | `docs/working-directory.md` |
| the adapter contract | `docs/contributing/adapters.md` |
| an error message a user can hit | `docs/troubleshooting.md`, quoting what they see |
| where files live under `~/.relay/` | `docs/configuration.md` |
| the release, CI or hook flow | `docs/contributing/development.md` |
| a file's job or name | `docs/contributing/design.md` |
| anything a user notices | `CHANGELOG.md`, under Unreleased |

What the tests enforce: every field, default and `runtime_config` key is in
`docs/configuration.md` and `helpText`; every command and flag is in
`shortHelp`, `helpText` and `docs/cli.md`; every link between markdown files
resolves; sample output quotes one version, never ahead of the constant; user
pages stay under their ceilings. The pre-commit hook runs the suite when docs
change. Nothing checks what a sentence means, so re-read the pages your change
touches before you open the PR.

## Before you commit

`make hooks` once per clone. Then:

1. No credentials in the diff or the message.
2. `make check` passes.
3. A config change followed [the loop](docs/contributing/config-fields.md).
4. A new flag or command is in `shortHelp` and `helpText`.
5. Docs updated in the same commit.
6. The version constant is untouched.

PR summary format:
[Pull requests](docs/contributing/development.md#pull-requests).

## Versions and releases

`master` carries the next version as `x.y.z-SNAPSHOT`; `make release` clears
the marker for exactly the commit it tags. **The version is mandatory and
never guessed, including by you.** Asked to cut a release without one, run
`make release` bare, show its output (last tag, commits since, candidates) to
whoever asked, and use the number they give back. Recommend one, but do not
pass a version the user did not choose.

CI (`ci.yml`) is manual only: `gh workflow run ci.yml --ref <branch>`. It
proves the suite passes with no coding CLI installed. Releases publish a
macOS Apple Silicon binary and `SHA256SUMS` as a pre-release. The whole flow is
[Cutting a release](docs/contributing/development.md#cutting-a-release).

## Where to read more

| | |
|---|---|
| [development.md](docs/contributing/development.md) | build, test, hooks, CI, releases, PR format |
| [config-fields.md](docs/contributing/config-fields.md) | the self-update loop for the config |
| [design.md](docs/contributing/design.md) | the codemap, the probe, who owns what, the trades |
| [adapters.md](docs/contributing/adapters.md) | the adapter contract |
