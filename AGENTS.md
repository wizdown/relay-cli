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

Where a sentence belongs:

| Question | Where it is answered |
|---|---|
| How do I install and run one worker? | `readme.md`, and nothing else lives there |
| What does this config field do? | `docs/configuration.md`, the only field reference |
| Which runtime should I pick, and what bounds it? | `docs/runtimes.md` |
| What commands and flags exist? What does the dashboard show? | `docs/cli.md` |
| What do I put in `repo_dir`? | `docs/working-directory.md` |
| Why isn't it working? | `docs/troubleshooting.md` |
| How does Relay work: tasks, agents, capabilities, leases? | Not here. Link <https://relay.bytecurio.com/> |
| Why is it built this way? How do I change it? | `docs/contributing/` |

- **User pages state what a thing is and what it defaults to.** The reason it
  works that way goes in `docs/contributing/design.md`, or nowhere. The words
  "deliberately", "on purpose" and "not incidental" do not appear in a user
  page.
- **One home per fact.** State it in full once. Everywhere else, one clause
  and a link.
- **Present tense.** No "no longer accepted", no "previously", no migration
  notes. A removed key explains itself in the error (`removedKeys`) and in
  `CHANGELOG.md`.
- **Delete rather than caveat.** A section that no longer describes the code
  is removed.
- **Short sentences.** One idea each, under 20 words on average, at most one
  em-dash per paragraph. Lead with the fact.
- **Word ceilings are tested.** `TestUserPagesStayShort` in
  `docs_pages_test.go` fails when a user page grows past its ceiling. Cut a
  duplicate or a rationale before raising a ceiling.
- **If you cannot find where a sentence belongs, fix the table above.**

Before you open a PR, walk this table for what you touched:

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
pages stay under their word ceilings. The pre-commit hook runs the suite when
docs change. Nothing checks what a sentence means, so re-read the pages your
change touches.

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
