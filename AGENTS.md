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
make build    # build ./relay
make hooks    # one-time: install the pre-commit hook
```

Go 1.22+, no other dependencies, no network needed. **A fresh clone passes its
tests with no coding CLI installed** — that is a property to protect, not an
accident. To verify you haven't broken it:

```bash
env PATH="/usr/bin:/bin:$(dirname $(command -v go))" go test ./...
```

## Codemap

`cmd/relay-cli/` is one Go package. Each file has one job:

| File | Owns |
|---|---|
| `main.go` | commands (`run`, `check`, `version`, `help`), flags, the supervisor, startup checks, log archiving, and `helpText` — the full manual |
| `init.go` | `relay init` and the short starting config it writes |
| `config.go` | config parse, defaults, and every validation done before launch — problems are accumulated and reported together |
| `probe.go` | MCP JSON-RPC over `net/http` — the token-free gate, no model anywhere |
| `worker.go` | the poll loop: ceilings, the three circuit breakers, locking, timeouts |
| `runtime.go` | the `Runtime` interface, `runtimeField` (what each runtime accepts in `runtime_config`), and the bash-adapter bridge |
| `runtime_claude.go` | the native claude adapter: argv, `stream-json` parsing, exit classification |
| `events.go` | the event bus → `worker.log`, `events.ndjson`, SSE |
| `server.go` | `/api/snapshot`, `/api/stream`, and the embedded page. **Read-only by design** |
| `redact.go` | `Scrub` / `RedactURL`. Everything user-facing goes through these |

Outside the binary:

| Path | |
|---|---|
| `worker-rules.md` | the harness contract given to every CLI — the editable copy; `cmd/relay-cli/assets/` holds the one compiled in |
| `docs/` | user documentation, and the contributor detail behind this file |

## Running it

```bash
relay init     # creates ~/.relay/config (never overwrites an existing one)
relay check    # validate config + test every credential — launches nothing, spends nothing
relay run      # start the fleet, open the dashboard on 127.0.0.1:7717
```

A bare `relay` prints the whole manual — starting is asked for by name
because it launches autonomous sessions that spend money.

Every command reads `~/.relay/config`. **One location, and no flag moves it** —
`state/` and `logs/` sit beside it. Don't add a path flag back without a reason
that survives "which config is this actually running?". **Always `check` first.**
Ctrl-C stops everything and archives logs.

## Making a config

```bash
relay init
```

Four fields per worker are required — `name`, `relay_mcp`, `repo_dir`,
`runtime` — because each is a decision relay-cli cannot make for anyone, plus
whatever the named runtime requires inside `runtime_config` (`model`, for
claude). Everything else is already bounded (12 runs/hour, $5 per run, a
15-minute kill, a 30-second fleet poll with a fixed 5-second floor).

The split is the thing to preserve: fields **outside** `runtime_config` are
enforced by relay-cli and mean the same for every runtime; fields **inside** are
one CLI's own vocabulary, declared by that adapter's `ConfigFields()`. A new
runtime setting is added there and nowhere else — the parser, the bash-adapter
environment and the docs test all read that one table.

**Never commit a config.** Every `relay_mcp` is a live relay credential with the
secret embedded in the URL. It is gitignored by name at any depth, and the
pre-commit hook refuses it. If you need one in a test or a doc, use
`relay.example.com` and `wzh_REPLACE_ME`. That rule is about example *connector
URLs*; prose should still link the product itself, at
<https://relay.bytecurio.com/>.

You cannot create a credential from here — it comes from relay
(`issue_agent_credential`) and is shown exactly once. If you don't have one,
`relay check` is still the right way to prove a config parses.

## Runtime defaults

| | |
|---|---|
| `claude` | **The only supported runtime.** Adapter compiled in. `runtime_config.model` is REQUIRED — `opus`, `sonnet`, `haiku`, or a pinned id like `claude-opus-5` — because the CLI's own default moves between versions and an unattended worker should say what it runs. Also takes `max_usd_per_run`. |
| `codex` | **Coming soon, not offered.** Refused at config load. Don't document it as usable, and don't add a `codex` branch anywhere outside `ResolveRuntime`. |

No CLI is bundled. The adapter ships; the CLI is installed separately and found
on `PATH`, and the runtime proves itself at startup.

The bash-adapter path (`bashRuntime`) is **complete but gated off** by
`bashAdaptersEnabled` in `runtime.go`, and is the half of codex support that
already exists. Keep it compiling and keep its test passing — it is not dead
code, it is unreleased code. Shipping codex means flipping that constant and
adding a `runtimes/` directory back; nothing else should need to change.

## Before you commit

`make hooks` once per clone, and the hook does most of this for you.

1. **No credentials.** No config file, no real `wzh_` secret.
2. `make check` passes.
3. If you changed a config field → [the config loop](docs/changing-the-config.md).
4. If you added a flag or command, `helpText` documents it — a test enforces it.
5. Docs updated in the same commit, not a follow-up.
6. **The version constant is bumped** — [Version on every PR](#version-on-every-pr).

Details and the PR summary format: **[docs/development.md](docs/development.md)**.

## Version on every PR

**Every PR bumps `version` in `cmd/relay-cli/main.go`.** One PR, one
bump. That constant is the single source of truth — the binary prints it, the
release artifacts are named from it, and the tag must match it — so a merge
that moves the code without moving the constant leaves `--version` lying about
what is installed.

Read the constant as `MAJOR.MINOR.PATCH` and apply exactly one of these:

1. **Major** — leave it alone. Increment it only when the PR was explicitly
   asked to, and **never decrement it**. Everything here is `0.x`, and a test
   fails the build if it leaves `0.` — see [Versioning](readme.md#versioning).
2. **Minor** — increment for any new feature, and reset patch to `0`
   (`0.1.4` → `0.2.0`).
3. **Patch** — increment for a bug fix (`0.1.4` → `0.1.5`). Docs-only,
   refactor and chore PRs take a patch too: the rule is one bump per PR, never
   none.

A PR carrying both a feature and a fix is a minor bump — the largest change in
it wins. If `master` moved ahead of your branch, rebase and re-apply the bump
from the constant on `master`, not from yours.

Releases no longer bump anything; they tag the constant already on `master`.
See [docs/development.md](docs/development.md).

## Conventions

- **Comments explain *why*.** This codebase leans on rationale over description;
  match it. The reason a ceiling exists is more useful than restating its type.
- **New ceilings default to a bound, never to unlimited.** The short config has
  to be the safe one.
- **Everything printed, logged, served or returned goes through `Scrub`.** A
  probe error can quote the URL it failed on, and that URL is the credential.
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
| [docs/development.md](docs/development.md) | build, test, hooks, releases, PR format |
| [docs/changing-the-config.md](docs/changing-the-config.md) | the self-update loop for the config |
| [docs/design.md](docs/design.md) | why the probe exists, what relay owns |
| [docs/runtimes.md](docs/runtimes.md) | which runtimes are offered, and the adapter contract |
| [docs/configuration.md](docs/configuration.md) | every config field |
