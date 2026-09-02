# Development

Go 1.22+ and nothing else. One module with no third-party dependencies, so
there is no `go.sum`, no lockfile, and no network needed to build or test.

## Commands

From the repository root:

```bash
make check    # gofmt + vet + test. The pre-PR command
make test     # tests only
make lint-docs  # only the tests that hold the docs to the code
make fmt      # gofmt -w
make build    # build ./relay
make hooks    # once per clone: install the git hooks

make release VERSION=x.y.z   # cut a release; see below
```

## The fresh-clone property

A clone passes its tests with no coding CLI installed. It is easy to break
without noticing, because your machine has the CLI.

Two seams make it hold, both package variables. `checkRuntime` in `config.go`
is stubbed by the parsing tests via `noRuntimeCheck(t)`. `installedRuntimes`
in `init.go` decides which workers `relay init` writes live, and is stubbed
via `withInstalledRuntimes(t, …)`, so an init test describes a machine rather
than the one it runs on. A test that genuinely needs a CLI gates on it being
present.

To check you have not broken it:

```bash
env PATH="/usr/bin:/bin:$(dirname $(command -v go))" go test ./...
```

## The git hooks

`make hooks` sets `core.hooksPath` to the tracked `.githooks/` directory, so
an updated hook reaches you with a `git pull`.

**pre-commit** runs, cheapest first:

1. **Credentials**: refuses a staged relay-cli config, or any connector-shaped
   secret in added lines. A secret pushed to a public repo is leaked the
   moment it lands, and rewriting history does not un-leak it.
2. **gofmt** on staged Go files.
3. **`go test ./...`** when Go changed, and also when only docs changed,
   because the drift tests read the docs. Without `-race`, for speed.

**commit-msg** scans the message for the same connector shapes, with the same
allow-list from `.githooks/lib.sh`. A message is where a failing `check` gets
pasted, and that output quotes the credential.

The hooks only run where someone ran `make hooks`, and `--no-verify` skips
them. CI is the backstop for files; nothing is a backstop for a PR body.

## CI

`.github/workflows/ci.yml` is manual-dispatch only. Everything it does runs
locally with nothing but Go, and runner time is a cost this repo does not
spend per commit.

```bash
gh workflow run ci.yml --ref <branch>
gh run watch
```

It runs gofmt, `go vet`, `go test -race`, a build, and a scan for
credential-shaped strings across tracked files, on a machine with no coding
CLI installed.

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

Before cutting, move the `Unreleased` entries in `CHANGELOG.md` under the new
version. Then:

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
```

The number already on `master` is a suggestion, chosen before anyone knew
what the batch would hold.

**2. It checks first.** Clean tree, on `master`, in sync with
`origin/master`, no such tag locally or on origin, and a version no lower than
the one `master` claims.

**3. It proves the bumped tree.** The constant is written, then `make check`
and `make dist` run against it, so the documentation tests check the release.
If either fails, the constant is put back and nothing is committed.

**4. It asks.** The tag, the commit, the artifacts and the diff, then
`[y/N]`. Without a terminal it refuses.

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
message; a PR title and body are checked by you or by nobody.

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
