# Development

Go 1.22+ and nothing else. One module with no third-party dependencies, so there
is no `go.sum`, no lockfile, and no network needed to build or test.

## Commands

From the repository root:

```bash
make check    # gofmt + vet + test — the pre-PR command
make test     # tests only
make fmt      # gofmt -w
make build    # build cmd/relay-cli/relay-cli
make hooks    # one-time per clone: install the pre-commit hook
```

Everything runs from the repository root; there is no second module to enter.

## The fresh-clone property

**A clone passes its tests with no coding CLI installed.** Requiring someone to
install Claude Code before they can test a JSON parser is a poor first five
minutes, and it is easy to break without noticing — *your* machine has the CLI.

The seam is `checkRuntime` in `config.go`, a package variable the parsing tests
stub via `noRuntimeCheck(t)`. If you add a test that genuinely needs a CLI on
`PATH`, gate it on the CLI being present rather than making the suite depend on
it.

To check you haven't broken it:

```bash
env PATH="/usr/bin:/bin:$(dirname $(command -v go))" go test ./...
```

## The pre-commit hook

```bash
make hooks
```

This sets `core.hooksPath` to the tracked `.githooks/` directory, so an updated
hook reaches you with a `git pull` rather than needing a reinstall.

It runs, cheapest first:

1. **Credentials** — refuses a staged relay-cli config, or any connector-shaped
   secret in added lines. First because it is the only check here guarding a
   mistake you cannot take back: a secret pushed to a public repo is leaked the
   moment it lands, and rewriting history does not un-leak it.
2. **gofmt** on staged Go files.
3. **`go test ./...`** when Go changed — and also when only docs changed,
   because the drift tests read those docs. Without `-race`, for speed; CI runs
   the race detector.

It is a fast local loop, not a guarantee: it only runs where someone ran `make
hooks`, and `git commit --no-verify` skips it. The manual CI workflow is the
backstop.

## CI

`.github/workflows/ci.yml` is **manual-dispatch only** — it does not run on push
or pull request. Everything it does runs locally with nothing but Go, and runner
time is a cost this repo does not spend per commit.

```bash
gh workflow run ci.yml --ref <branch>
```

It exists for the one check that is awkward by hand: whether the suite still
passes on a machine with **no coding CLI installed**. It also runs `-race` and a
scan for credential-shaped strings across tracked files.

## Cutting a release

Releases publish a `relay-cli` binary for **macOS on Apple Silicon**, with a
checksum, so a user can download one file and run it without a Go toolchain.
`.github/workflows/release.yml` builds it.

Other platforms are deliberately not published yet. They all build — CGO is off
and nothing here is platform-specific — so adding one is a line in
`PLATFORMS` in the root `Makefile`; the release picks it up.

Artifacts are named for the platform a user recognises (`macos-arm64`), not for
`GOOS` (`darwin-arm64`). The build is unsigned and unnotarised, so Gatekeeper
quarantines it on first run; the release notes carry the `xattr -d` line.

**The version constant in `main.go` is the single source of truth.** The binary
prints it, the artifacts are named from it, and the workflow refuses to publish
a tag that disagrees with it.

**1. Confirm the version.** Every PR already bumps the constant — see [Version
on every PR](../AGENTS.md#version-on-every-pr) — so a release tags what is
on `master` rather than bumping again. Read it in
`cmd/relay-cli/main.go`:

```go
version = "0.3.0"
channel = "beta"
```

Bump it here only if `master` somehow carries an unreleased change that missed
its bump; that is a gap in the PR rule, not a release step.

Stay on `0.x`. A test fails the build if the version leaves it, because 1.0
would be a claim that the interface is settled — see
[Versioning](../readme.md#versioning). If that is genuinely the intent, delete
that test in the same commit, deliberately.

**2. Verify the build locally.** From the repository root:

```bash
make version      # prints what the workflow will compare the tag against
make dist         # every platform in PLATFORMS + dist/SHA256SUMS
```

`make version` failing, or printing nothing, means the constant could not be
read — fix that before tagging, or the release publishes artifacts named
`relay-cli--macos-arm64`, with the version missing.

**3. Merge the bump to `master`.** Releases are cut from merged code.

**4. Tag and push.** The tag must be `v` + the constant:

```bash
git checkout master && git pull
git tag v0.3.0
git push origin v0.3.0
```

Pushing the tag is what starts the release — it is the only workflow here that
runs without being asked, because pushing a version tag *is* the request.

**5. Watch it.** The workflow re-checks the tag against the constant, runs
gofmt/vet/`test -race`, builds every platform in `PLATFORMS`, and publishes a
GitHub Release with `SHA256SUMS` attached.

```bash
gh run watch
```

Releases are marked **pre-release** while the project is `0.x`.

**If publishing fails** after the tag is already pushed, fix the cause and re-run
without moving the tag:

```bash
gh workflow run release.yml -f tag=v0.3.0
```

Only move or delete a tag that never published. A tag someone may already have
downloaded should be superseded by a new version, not rewritten.

## Pull requests

Keep the diff to one concern. `make check` passes. Docs updated in the same
commit — not a follow-up, which is how documentation ends up describing a
version that no longer exists.

Commit messages and PR bodies here explain **why**, matching the codebase's own
comments. A reviewer can read the diff; what they cannot read is the reason.

A PR summary should cover:

- **What changed, and why** — the problem, not just the edit.
- **Anything deleted, and why it was safe** — name what replaced it. If a file
  was unreachable or superseded, say how you established that.
- **Behaviour changes for existing users**, especially anything that makes an
  existing config, adapter or checkout stop working. Say what error
  they will see and what they should do.
- **Verification you actually ran.** Not "tests pass" — which tests, on what,
  and anything you checked by hand. Say plainly if something is unverified.
- **What you deliberately left out**, so a reviewer doesn't have to guess whether
  you missed it or chose it.

Do not claim a check you did not run. An unverified claim in a PR body is worse
than an admitted gap, because it stops anyone else from looking.
