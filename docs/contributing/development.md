# Development

Go 1.22+ and nothing else. One module with no third-party dependencies, so
there is no `go.sum`, no lockfile, and no network needed to build or test.

## Commands

The list is [Commands](../../AGENTS.md#commands), and `make help` prints it.

## The fresh-clone property

A clone passes its tests with no coding CLI installed. It is easy to break
without noticing, because your machine has the CLI.

Three seams make it hold. `dispatch` in `main.go` takes the argument list and
both output streams and returns the exit status, so `cli_test.go` drives every
command line without a process. The other two are package variables:
`checkRuntime` in `config.go`
is stubbed by the parsing tests via `noRuntimeCheck(t)`. `installedRuntimes`
in `init.go` decides which workers `relay init` writes live, and is stubbed
via `withInstalledRuntimes(t, …)`, so an init test describes a machine rather
than the one it runs on. A test that genuinely needs a CLI gates on it being
present.

To check you have not broken it:

```bash
make check-fresh
```

It runs the suite with `PATH` cut to the system directories and wherever `go`
itself lives, so no coding CLI is found however it was installed. Run it before
a PR that touches `config.go`, `init.go` or a runtime, and say in the PR that
you did.

## The git hooks

`make hooks` sets `core.hooksPath` to the tracked `.githooks/` directory, so
an updated hook reaches you with a `git pull`.

**pre-commit** runs, cheapest first:

1. **Credentials**: refuses a staged relay-cli config, or any connector-shaped
   secret in added lines. A secret pushed to a public repo is leaked the
   moment it lands, and rewriting history does not un-leak it.
2. **Build output**: refuses a staged file that is an executable image or over
   1 MiB. A binary committed by accident is pulled by every clone from then
   on. `TestNoBuildOutputIsTracked` is the backstop for a clone without hooks.
3. **gofmt** on staged Go files.
4. **`go test ./...`** when Go changed, and also when only docs changed,
   because the drift tests read the docs. Without `-race`, for speed.

**commit-msg** scans the message for the same connector shapes. A message is
where a failing `check` gets pasted, and that output quotes the credential.

Both hooks call `scripts/scan-secrets.sh`, and so do the `secrets` job in
`ci.yml` and the pull-request text check. It reads files named on the command
line or text on stdin, prints what it found, and exits 1. The shapes and the
allow-list are in `.githooks/lib.sh`, which is the one home for both: four
scanners with four copies of a regular expression is four copies that drift.
Pass `-q` to suppress the value, which is what the pull-request check does so a
public run log does not publish the credential a second time.

The hooks only run where someone ran `make hooks` — the `SessionStart` hook in
`.claude/settings.json` runs it for an agent session — and `--no-verify` skips
them. CI is the backstop for files, and `pr-text.yml` is the backstop for a
pull request title and body.

## CI

`.github/workflows/ci.yml` runs on every pull request. It runs gofmt, `go vet`,
`go test -race`, a build, and a scan for credential-shaped strings across
tracked files, on a machine with no coding CLI installed. That last part is
what a local run cannot prove, since your machine has the CLI.

A `concurrency` group keyed on the pull request means a new push cancels the
run it superseded, so a branch costs about a minute of runner time however many
times it is pushed.

For a branch with no pull request open:

```bash
gh workflow run ci.yml --ref <branch>
gh run watch
```

`.github/workflows/pr-text.yml` runs alongside it, on the pull request title
and body. See [The git hooks](#the-git-hooks).

## Versions

`master` always carries the next version with a `-SNAPSHOT` marker:

```go
version = "0.2.0-SNAPSHOT"
channel = "beta"
```

A PR never touches it. `make release` clears the marker for exactly one
commit, the one the tag points at, and puts it back on the next. A release is
a batch of merges, and a bare `0.2.0` on every commit between two tags would
claim each is the release.

Which unreleased tree a binary came from is the build stamp:

```text
relay 0.2.0-SNAPSHOT (beta) [v0.1.0-4-g1aa22a3]
```

The Makefile stamps `main.build` with `git describe --tags --always --dirty`.
It is empty outside a checkout and suppressed when it would only repeat the
tag, so a released binary prints exactly `relay 0.2.0 (beta)`.

Stay on 0.x. A test fails the build if the version leaves it, because 1.0
claims the interface is settled; see [Versioning](../cli.md#versioning). If
that is the intent, delete that test in the same commit.

## Cutting a release

A release publishes a `relay` binary for macOS on Apple Silicon plus
`SHA256SUMS`, built by `.github/workflows/release.yml` and marked pre-release
while the project is 0.x. Other platforms build (CGO is off, nothing is
platform-specific) and are not published; adding one is a line in `PLATFORMS`
in the `Makefile`. Artifacts are named for the platform a user recognises
(`macos-arm64`), not for `GOOS`. The build is unsigned, so the readme and the
release notes carry the `xattr -c` line.

Everything a user should know about the batch is already under `Unreleased`
in `CHANGELOG.md`; the release moves it under the new version. Then:

```bash
make release VERSION=0.2.0
```

**1. Choose the version.** It is mandatory and nothing guesses it. The number
says whether the batch since the last tag was a fix or a feature, and only
someone reading those commits knows. Run it bare to see them:

```text
error: make release needs a version. It is never guessed.

  last release   v0.1.0
  master says    0.2.0-SNAPSHOT

  commits since:
    1aa22a3 Refuse a config this version cannot fully honour
    970de16 Rewrite the runtimes page, and trim the readme

  a fix, or docs only   make release VERSION=0.2.0
  anything new          make release VERSION=0.3.0
  breaking              (0.x — say so in the release notes; there is
                         no 1.0 until the interface settles)

  The number on master (0.2.0) is the default only if the batch above is
  what it was chosen for. Read it before deciding.
```

**2. It checks first.** Clean tree, on `master`, in sync with
`origin/master`, no such tag locally or on origin, a version no lower than
the one `master` claims, and at least one entry under `Unreleased` in
`CHANGELOG.md`. A release says what changed, even when that is "docs only".

**3. It proves the bumped tree.** The constant is written, the sample
`relay x.y.z` lines in the user docs follow it, and the `Unreleased` entries
in `CHANGELOG.md` move under `## vx.y.z` with an empty `Unreleased` left above
for the next batch. Then `make check` and `make dist` run against that tree,
so the documentation tests check the release itself. If either fails, every
file is put back and nothing is committed.

**4. It asks.** The tag, the commit, the artifacts, the changelog entries and
the diff, then `[y/N]`. Without a terminal it refuses.

**5. Two commits, one tag, one `--atomic` push.**

```text
Release v0.2.0        ← the tag points here
Start 0.2.1-SNAPSHOT  ← master carries the next version again
```

If something merged to `master` while the checks ran, the push is rejected,
nothing is published, and the script prints the undo:

```bash
git tag -d v0.2.0 && git reset --hard origin/master
```

**6. The workflow publishes.** Pushing the tag starts `release.yml`. It
re-checks the tag against the constant, refuses a `-SNAPSHOT` one, runs
gofmt/vet/`test -race`, builds every platform in `PLATFORMS`, and publishes a
pre-release with `SHA256SUMS` and the commits since the previous tag appended
to the notes. `make release` runs `gh run watch` for you.

If publishing fails after the tag is pushed, fix the cause and re-run without
moving the tag:

```bash
gh workflow run release.yml -f tag=v0.2.0
```

Only move or delete a tag that never published. A tag someone may have
downloaded is superseded by a new version, not rewritten.

## Pull requests

Keep the diff to one concern. `make check` passes. Docs updated in the same
commit.

Commit messages and PR bodies explain why, matching the codebase's own
comments. The repo is public and a message outlives the branch: no connector
URL, no internal hostname, no absolute path off your machine, nobody else's
name. Redact rather than omit: "HTTP 401 from the configured endpoint" carries
what the value would. The `commit-msg` hook catches connector shapes in a
message, and `.github/workflows/pr-text.yml` catches them in a PR title and
body, on every edit. A red check there means revoke the credential in relay
before anything else, then edit the text.

`.github/pull_request_template.md` opens with these headings already in place.
A PR summary covers:

- **What changed, and why**: the problem, not just the edit.
- **Anything deleted, and why it was safe**: name what replaced it.
- **Behaviour changes for existing users**, with the error they will see and
  what to do.
- **Verification you actually ran**: which tests, on what, and anything
  checked by hand. Say plainly if something is unverified.
- **What you deliberately left out**, so a reviewer does not have to guess.

Do not claim a check you did not run. An unverified claim in a PR body is
worse than an admitted gap, because it stops anyone else from looking.
