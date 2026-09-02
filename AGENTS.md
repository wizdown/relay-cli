# Working in this repo

`relay-cli` is a worker that connects to a **relay** MCP server, claims one task,
does it, and posts the result back. Relay owns the task state machine and the
agent roster; nothing here tracks task state.

One Go binary, one module, one package: a poller that drives a local coding CLI
inside a repo checkout. It is **beta**.

Everything is **0.x** and stays there until the interface settles. Don't bump to
1.x — a test enforces this.

## Commands

Run from the repository root:

```bash
make check    # gofmt + vet + test — run this before any PR
make test     # tests only
make fmt      # gofmt -w .
make build    # build ./relay
make dist     # release artifacts + SHA256SUMS
make version  # print the version constant the release compares a tag against
make hooks    # one-time per clone: install the git hooks

make release VERSION=x.y.z   # cut a release — see Cutting a release below
```

Go 1.22+, no other dependencies, no network needed. **A fresh clone passes its
tests with no coding CLI installed** — that is a property to protect, not an
accident. The seam is `checkRuntime` in `config.go`, a package variable the
parsing tests stub via `noRuntimeCheck(t)`. A test that genuinely needs a CLI
gates on it being present. To verify you haven't broken it:

```bash
env PATH="/usr/bin:/bin:$(dirname $(command -v go))" go test ./...
```

Some tests read the docs. `docs_test.go` reflects over the `Worker` struct and
each runtime's `ConfigFields()` and fails when a field, default or removed key is
undocumented; `docs_pages_test.go` walks every markdown file in the repo and
fails on a broken link, a command or flag missing from `docs/cli.md`, or sample
output quoting a version that does not exist. So a docs-only change still runs
the suite — and the pre-commit hook knows it.

## Codemap

`cmd/relay-cli/` is one Go package. Each file has one job:

| File | Owns |
|---|---|
| `main.go` | commands (`run`, `check`, `version`, `help`), flags, the supervisor, startup checks, log archiving, the `version` constant, and `helpText` — the full manual |
| `init.go` | `relay init` and the short starting config it writes |
| `config.go` | config parse, defaults, and every validation done before launch — problems are accumulated and reported together |
| `probe.go` | MCP JSON-RPC over `net/http` — the token-free gate, no model anywhere |
| `worker.go` | the poll loop: ceilings, the three circuit breakers, locking, timeouts |
| `runtime.go` | the `Runtime` interface, `runtimeField` (what each runtime accepts in `runtime_config`), and the bash-adapter bridge |
| `runtime_claude.go` | the native claude adapter: argv, `stream-json` parsing, exit classification |
| `runtime_codex.go` | the native codex adapter: `codex exec` argv, `--json` event parsing, exit classification, and the sign-in check |
| `events.go` | the event bus → `worker.log`, `events.ndjson`, SSE |
| `server.go` | `/api/snapshot`, `/api/stream`, and the embedded page. **Read-only by design** |
| `redact.go` | `Scrub` / `RedactURL`. Everything user-facing goes through these |
| `docs_test.go` | the drift tests that keep the config reference honest |
| `docs_pages_test.go` | the same idea one level up: links resolve, `docs/cli.md` names every command and flag, sample output quotes a version that exists |

Outside the binary:

| Path | |
|---|---|
| `worker-rules.md` | the harness contract given to every CLI — the editable copy; `cmd/relay-cli/assets/` holds the one compiled in |
| `docs/` | user documentation — keep it about what the CLI does |
| `docs/contributing/` | contributor detail behind this file |
| `scripts/release.sh` | everything `make release` does — the checks, the two commits, the tag, the one atomic push |
| `.githooks/` | `pre-commit` (scans the diff) and `commit-msg` (scans the message), sharing the connector shapes in `lib.sh`; installed by `make hooks` |
| `.github/workflows/` | `ci.yml` (manual only) and `release.yml` (tags) |

## Running it

```bash
relay init     # creates ~/.relay/config (never overwrites an existing one)
relay check    # validate config + test every credential — launches nothing, spends nothing
relay run      # start the fleet, open the dashboard on 127.0.0.1:7717
```

A bare `relay` prints the whole manual — starting is asked for by name because it
launches autonomous sessions that spend money.

Every command reads `~/.relay/config`. **One location, and no flag moves it** —
`state/` and `logs/` sit beside it. Don't add a path flag back without a reason
that survives "which config is this actually running?". **Always `check` first.**
Ctrl-C stops everything and archives logs. `state/` is rebuilt on every start, so
**restarting is how a config change is applied**.

## The worker config

```bash
relay init
```

Four fields per worker are required — `name`, `relay_mcp`, `repo_dir`,
`runtime` — because each is a decision relay-cli cannot make for anyone, plus
whatever the named runtime requires inside `runtime_config` (`model`, for both
claude and codex). Everything else is already bounded: 12 runs/hour, $5 per run
(claude only), a 15-minute kill, a 30-second fleet poll with a fixed 5-second
floor. The full
reference for users is **[docs/configuration.md](docs/configuration.md)**.

The split is the thing to preserve: fields **outside** `runtime_config` are
enforced by relay-cli and mean the same for every runtime; fields **inside** are
one CLI's own vocabulary, declared by that adapter's `ConfigFields()`. A new
runtime setting is added there and nowhere else — the parser, the bash-adapter
environment and the docs test all read that one table.

**Changing a config field is a loop, not an edit.** Adding, renaming, removing
one or changing a default touches the struct or `ConfigFields()`, `workerKeys`
in `config.go` (every key is checked by name at every level of the file, so a
field missing from that list is one nobody can set — a test fails until the two
agree), the field table in `docs/configuration.md` (default in the **last**
column, backticked — a test parses that cell), and the `THE CONFIG FILE` block
in `helpText`. A **removed**
key is deleted from the docs entirely and added to `removedKeys` mapped to *what
to do instead* — that error message is the whole migration path, and the user
docs stay a description of what this version accepts. Step by step:
**[docs/contributing/config-fields.md](docs/contributing/config-fields.md)**.

**Never commit a config.** Every `relay_mcp` is a live relay credential with the
secret embedded in the URL. It is gitignored by name at any depth, the pre-commit
hook refuses it, and `ci.yml` scans tracked files for connector-shaped strings.
In tests and docs use `relay.example.com` and `wzh_REPLACE_ME`. That rule is
about example *connector URLs*; prose should still link the product itself, at
<https://relay.bytecurio.com/>.

**The same rule covers what you write around the code** — commit messages, PR
titles and bodies, release notes. Pasting a failing `check` into a message is the
natural thing to do, and its output quotes the connector URL, which *is* the
credential. This repo is public, so that is the version of the mistake you cannot
take back: a pushed message is world-readable at once, `master` refuses a
force-push, and squashing does not help — a pull request's original commits stay
fetchable at `refs/pull/N/head` for good. Redact the URL and describe the shape;
"HTTP 401 from the configured endpoint" tells a reviewer everything the value
would. The `commit-msg` hook scans a message for the same connector shapes, but
**nothing can scan a PR title, a PR body or a release note** — those are read by
you or by nobody. Public and permanent applies to the rest of what a message
carries too: no internal hostnames, no absolute paths off your machine, nobody
else's name or address.

You cannot create a credential from here — it comes from relay
(`issue_agent_credential`) and is shown exactly once. Without one, `relay check`
is still the right way to prove a config parses.

## Runtime defaults

| | |
|---|---|
| `claude` | **Supported**, adapter compiled in and native. `runtime_config.model` is REQUIRED — `opus`, `sonnet`, `haiku`, or a pinned id like `claude-opus-5` — because the CLI's own default moves between versions and an unattended worker should say what it runs. Also takes `max_usd_per_run`, which the CLI enforces itself. |
| `codex` | **Supported**, adapter compiled in and native. Takes `model` (REQUIRED, same reason), `reasoning_effort`, `sandbox`, `network_access`, `web_search`. **It has no per-run spend cap** — none exists in the CLI — so `max_usd_per_run` is claude's alone and a codex worker is bounded by `max_seconds_per_run`, `max_runs_per_hour` and its account's plan limits. Say that plainly wherever the runtimes are compared; it is the one thing someone choosing between them needs. |

No CLI is bundled. The adapter ships; the CLI is installed separately, found on
`PATH`, and proves itself at startup by its `--help` rather than by a version.
codex also proves it is signed in, because `codex login status` costs nothing —
claude cannot be asked that without spending a model call.

Two codex-specific trades are deliberate and documented in
[docs/runtimes.md](docs/runtimes.md); don't quietly reverse either:

- **The harness contract rides in the prompt**, not in a system prompt, because
  `codex exec` has no flag for one and its config key for the job has not
  reliably applied in non-interactive runs. A contract that silently does not
  arrive is a worker that claims two tasks in a session and tells nobody.
- **The connector URL is a `-c` override**, so it is visible in `ps`. The
  isolated alternative is a private `CODEX_HOME`, which breaks a ChatGPT sign-in:
  `auth.json` lives there and its refresh tokens are single-use. Working sign-in
  beat the smaller exposure. Everything else about that URL still goes through
  `Scrub`.

Both shipped adapters are native for the same reason: only a native adapter can
parse a CLI's event stream into session events, and only a native one can declare
`ConfigFields()`. The bash-adapter path (`bashRuntime`) is **complete but gated
off** by `bashAdaptersEnabled` in `runtime.go` — it is the extension point for a
CLI nobody here has written an adapter for, not the pattern a shipped runtime
follows. Keep it compiling and keep its test passing: it is not dead code, it is
unreleased code. The contract is
**[docs/contributing/adapters.md](docs/contributing/adapters.md)**.

## Documentation rules

Docs drift because nobody decides where a sentence belongs. Here it is decided:

| Question | Where it is answered |
|---|---|
| How do I install and run one worker? | `readme.md` — and nothing else lives there |
| Which runtime should I pick, and what bounds it? | `docs/runtimes.md` — the comparison lives there, not in the readme |
| What does this config field do? | `docs/configuration.md`, the only field reference |
| What commands and flags exist? | `docs/cli.md` |
| How do I run several workers? | `docs/configuration.md` — the CLI side only |
| What do I put in `repo_dir` — CLAUDE.md, skills, subagents, settings? | `docs/working-directory.md` |
| Why isn't it working? | `docs/troubleshooting.md` |
| How does relay work — tasks, agents, capabilities, delegation, leases? | **Not here.** Link <https://relay.bytecurio.com/> |
| Why is it built this way? How do I change it? | `docs/contributing/` |

- **The readme is for users, not contributors.** It gets someone from nothing to
  a working worker; rationale goes to `docs/contributing/design.md`.
- **Don't explain the relay product.** Anything configured on the relay agent —
  `instructions_md`, capabilities, `max_parallel_claims`, `lease_ttl_seconds`,
  task states — is relay's to document. Say what this CLI does and link out.
- **One home per fact.** If a table exists in two files, one of them is wrong and
  nobody knows which. Link instead of restating.
- **Document the present tense.** The docs describe what this version does — not
  what it used to do, what a field was renamed from, or what the old shell poller
  did differently. No "no longer accepted" tables, no "previously this was…", no
  migration notes. A user reading them has the current binary; the only thing
  history costs them is a longer page. Where a change genuinely needs to reach
  someone, it goes in the **error message** (`removedKeys` names the replacement)
  or the **release notes** — surfaces that find them, which a doc section they
  will never scroll to does not. Rationale that explains why the current design
  is what it is belongs in `docs/contributing/`, and may say what was tried.

### Documentation is part of the change, not a follow-up

**A behaviour change and its documentation land in the same commit.** A
follow-up commit is how documentation ends up describing a version that no
longer exists, and a docs PR nobody writes is indistinguishable from a lie in
the readme. If a change is too big to document in the same commit, it is too big
for one commit.

Before you open a PR, walk this table for what you touched. Every row is a place
a reader will look and be wrong:

| If you changed… | Update, in the same commit |
|---|---|
| a config field or its default | the tables in `docs/configuration.md`, the `THE CONFIG FILE` block in `helpText`, and `docs/contributing/config-fields.md` if the loop itself moved |
| a field you **removed** | delete every mention from `docs/configuration.md` and `helpText`; add it to `removedKeys` with what to use instead — the error carries the migration, the docs do not |
| a command or a flag | `helpText`, `docs/cli.md`, and the quickstart in `readme.md` if it appears there |
| a safeguard, ceiling or breaker | the safeguards tables in `docs/configuration.md` **and** the summary list under `## Safeguards` in `readme.md` |
| what the dashboard shows or serves | `docs/cli.md` |
| a runtime's support status or its startup check | `docs/runtimes.md`, and the runtime table in this file |
| what a session picks up from `repo_dir`, or what it is allowed to do there | `docs/working-directory.md`, and the claude-run list in `docs/runtimes.md` |
| the adapter contract or its environment | `docs/contributing/adapters.md` |
| an error message a user can hit | `docs/troubleshooting.md` — the symptom row should quote what they actually see |
| where files live under `~/.relay/` | `docs/configuration.md`, and any command that names a path |
| the release, CI or hook flow | `docs/contributing/development.md` and this file |
| a file's job, or a file's name | the codemap above, and every doc that links it |

Some of that is enforced and the rest is not, so know which is which:

- `docs_test.go` fails the build when a worker field, a `runtime_config` key, a
  default or a removed key is missing from `docs/configuration.md`, and when the
  `jsonc` example there no longer validates.
- `TestHelpQuotesTheRealDefaults` and the flag/command tests hold `helpText` to
  the code, and `docs_pages_test.go` holds `docs/cli.md` to the same command and
  flag lists — a flag now has to be documented in both places or the build fails.
- `docs_pages_test.go` also fails when a **link** between two markdown files
  points at a file or a heading anchor that does not exist, and when **sample
  output** quotes a version the code has never been: every page must show the
  same number, and never one ahead of the version constant.
- The pre-commit hook runs the suite when **docs** change, not only Go — because
  those tests read the docs.
- **Nothing checks what a sentence means.** Links resolve, names match, versions
  exist — and a page can still describe behaviour that changed last week. The
  readme's prose, the troubleshooting rows and every explanation are only as
  current as the last person who read them. Re-read the pages your change
  touches; a green `make check` means the docs are consistent, not that they are
  right.

Two more rules that keep this cheap:

- **Delete rather than caveat.** A section that no longer describes the code is
  removed, not annotated with "note: this changed" — the page says what is true
  now, and nothing about the version it used to describe.
- **If you cannot find where a sentence belongs, the doc map is wrong.** Fix the
  map in this file rather than dropping the sentence into whichever page is
  nearest — that is precisely how a page becomes 300 lines nobody reads.

## Before you commit

`make hooks` once per clone, and the hooks do most of this for you: pre-commit
refuses a staged config or a connector-shaped secret, runs gofmt on staged Go
files, and runs the tests when Go *or* docs changed; commit-msg refuses the same
secret shapes in the message.

1. **No credentials.** No config file, no real `wzh_` secret — in the diff *and*
   in the commit message. Same for a PR title and body, which no hook can see.
2. `make check` passes.
3. If you changed a config field → [the config loop](docs/contributing/config-fields.md).
4. If you added a flag or command, `helpText` documents it — a test enforces it.
5. Docs updated in the same commit — walk [the table above](#documentation-is-part-of-the-change-not-a-follow-up).
6. **Do not touch the version constant** — [Versions](#versions-and-the-snapshot-marker).

PR summary format: **[docs/contributing/development.md](docs/contributing/development.md)**.

## Versions and the SNAPSHOT marker

**A PR does not bump the version.** `master` carries the *next* version with a
`-SNAPSHOT` marker on it — `0.2.0-SNAPSHOT` — and only `make release` ever
changes it. Leave it alone; a bump in a feature PR will be a merge conflict with
the next release and nothing else.

The marker is what keeps the constant honest. A release is a batch of merges, so
between two of them dozens of commits share one version number: printing a bare
`0.1.0` on all of them would claim each is the release. `-SNAPSHOT` says
*unreleased* in a form a user can read in `relay version` and paste into a bug
report. Which unreleased tree it is comes from the build stamp beside it
(`[v0.1.0-4-g1aa22a3]`, `git describe`, stamped by the Makefile) — empty outside
a checkout, and suppressed at a release tag, so a published binary prints exactly
what the docs show.

Everything stays **0.x** until the interface settles; a test fails the build if
the version leaves it — see [Versioning](readme.md#versioning).

## Cutting a release

```bash
make release VERSION=0.2.0
```

**The version is mandatory and is never guessed — including by you.** The number
says whether the batch since the last tag was a fix or a feature, and that is a
judgement about what the changes mean to a user. If you are asked to cut a
release without one:

```bash
make release            # refuses, and prints what is needed to choose
```

It shows the last tag, every commit since it, and the patch and minor candidates.
**Show that to whoever asked and use the number they give back.** Recommend one
by all means — say which you would pick and why — but do not pass a version the
user did not choose, and do not fall back to the number already on `master`: that
was chosen before anyone knew what the batch would contain.

What the command does, once it has a version:

1. refuses unless `master` is clean, current, and has no such tag already —
   locally *or* on origin
2. writes the version, then runs `make check` and `make dist` **on the bumped
   tree**, so the documentation tests are checking the release. If either fails
   it puts the constant back and commits nothing
3. asks for confirmation, showing the tag and the artifacts
4. commits `Release v0.2.0`, tags it, commits `Start 0.2.1-SNAPSHOT`
5. pushes both commits and the tag in **one `--atomic` push**

Step 5 is one push on purpose: a branch that lands without its tag leaves
`master` claiming a release nobody published. If the push is rejected — usually
because something merged while the checks ran — nothing was published, and the
script says how to undo and retry.

Pushing the tag is what publishes. `release.yml` re-checks the tag against the
constant, refuses a `-SNAPSHOT` one, runs gofmt/vet/`test -race`, builds every
platform in `PLATFORMS`, and publishes a **pre-release** with `SHA256SUMS`
attached and the commits since the last tag appended to the notes.

**If publishing fails** after the tag is pushed, fix the cause and re-run without
moving the tag:

```bash
gh workflow run release.yml -f tag=v0.2.0
```

Only move or delete a tag that never published. A tag someone may already have
downloaded is superseded by a new version, not rewritten. Full walkthrough:
[docs/contributing/development.md](docs/contributing/development.md).

## CI: manual only

`.github/workflows/ci.yml` does **not** run on push or pull request. Everything
it does runs locally with nothing but Go, and runner time is a cost this repo
does not spend per commit. Trigger it when you want a clean-machine second
opinion — in particular that the suite still passes with **no coding CLI
installed**, which is easy to break on a machine that has one:

```bash
gh workflow run ci.yml --ref <branch>
gh run watch
```

It runs gofmt, `go vet`, `go test -race`, a build, and a scan for
credential-shaped strings across tracked files.

## What a release publishes

A `relay` binary for **macOS on Apple Silicon** plus `SHA256SUMS`, built by
`.github/workflows/release.yml`. Other platforms build fine — CGO is off and
nothing here is platform-specific — they are simply not published; adding one is
a line in `PLATFORMS` in the `Makefile`. Artifacts are named for the platform a
user recognises (`macos-arm64`), not for `GOOS`. Releases are marked
**pre-release** while the project is `0.x`.

## Conventions

- **Comments explain *why*.** This codebase leans on rationale over description;
  match it. The reason a ceiling exists is more useful than restating its type.
- **New ceilings default to a bound, never to unlimited.** The short config has
  to be the safe one.
- **Everything printed, logged, served or returned goes through `Scrub`.** A
  probe error can quote the URL it failed on, and that URL is the credential.
- **A doc link the binary prints is a full URL**, built from `docsBase` in
  `main.go` — the reader may hold nothing but the binary, and the config `init`
  writes lands in `~/.relay/`, so a relative `docs/` path points at nothing. The
  branch in it is `master`; a link to `blob/main/` is a 404. Both are tested.
- **The dashboard is read-only.** No route may start a run, pause a worker or
  edit a ceiling. Adding one changes what the page *is* — don't, without asking.
- **Runtime-specific behaviour belongs in an adapter.** If you're adding
  `if runtime == "…"` to the worker loop, it goes in the adapter instead —
  `runtime_claude.go`, or the bash bridge for a runtime that isn't compiled in.
  `ResolveRuntime` is the one place a runtime name is allowed to be a string.
- **Agent identity is relay's, not this repo's.** What an agent is for, its
  capabilities and its claim limits live on the relay agent (`instructions_md`),
  because that reaches a session already running. Never add a config field here
  that duplicates it — five such keys were removed and are rejected by name.
- **Don't grow `worker-rules.md` into a copy of relay's workflow.** It carries
  only what this harness adds. Relay serves the workflow itself.

## Where to read more

| | |
|---|---|
| [docs/contributing/development.md](docs/contributing/development.md) | build, test, hooks, releases, PR format |
| [docs/contributing/config-fields.md](docs/contributing/config-fields.md) | the self-update loop for the config |
| [docs/contributing/design.md](docs/contributing/design.md) | why the probe exists, what relay owns |
| [docs/contributing/adapters.md](docs/contributing/adapters.md) | the adapter contract, and where codex stands |
| [docs/configuration.md](docs/configuration.md) | every config field, for users |
| [docs/working-directory.md](docs/working-directory.md) | what a worker session picks up from `repo_dir`, for users |
