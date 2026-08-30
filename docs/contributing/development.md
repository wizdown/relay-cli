# Development

Go 1.22+ and nothing else. One module with no third-party dependencies, so there
is no `go.sum`, no lockfile, and no network needed to build or test.

## Commands

From the repository root:

```bash
make check    # gofmt + vet + test — the pre-PR command
make test     # tests only
make fmt      # gofmt -w
make build    # build ./relay
make hooks    # one-time per clone: install the pre-commit hook

make release VERSION=x.y.z   # cut a release — see below
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

## Versions

`master` always carries the **next** version with a `-SNAPSHOT` marker on it:

```go
version = "0.2.0-SNAPSHOT"
channel = "beta"
```

**A PR never touches it.** `make release` clears the marker for exactly one
commit — the one the tag points at — and puts it back on the next. So the
constant is either a release, or an admission that this tree is not one.

That matters because a release is a **batch** of merges. Dozens of commits share
one version number between two tags, and a bare `0.2.0` on all of them would
claim each is the release. `-SNAPSHOT` says otherwise in a form a user can read
out of `relay version` and paste into an issue.

*Which* unreleased tree is a separate question, answered by the build stamp:

```text
relay 0.2.0-SNAPSHOT (beta) [v0.1.0-4-g1aa22a3]
```

The Makefile stamps `main.build` with `git describe --tags --always --dirty`.
It is empty when built outside a checkout — a source tarball — and suppressed
when it would only repeat the tag, so a **released** binary prints exactly
`relay 0.2.0 (beta)`, which is what the docs show.

Stay on `0.x`. A test fails the build if the version leaves it, because 1.0 would
be a claim that the interface is settled — see
[Versioning](../../readme.md#versioning). If that is genuinely the intent, delete
that test in the same commit, deliberately.

## Cutting a release

Releases publish a `relay` binary for **macOS on Apple Silicon**, with a
checksum, so a user can download one file and run it without a Go toolchain.
`.github/workflows/release.yml` builds it.

Other platforms are deliberately not published yet. They all build — CGO is off
and nothing here is platform-specific — so adding one is a line in
`PLATFORMS` in the root `Makefile`; the release picks it up.

Artifacts are named for the platform a user recognises (`macos-arm64`), not for
`GOOS` (`darwin-arm64`). The build is unsigned and unnotarised, so Gatekeeper
quarantines it on first run; the release notes carry the `xattr -d` line.

One command does all of it:

```bash
make release VERSION=0.2.0
```

**1. Choose the version.** It is mandatory, and nothing guesses it — not the
Makefile, not an agent asked to cut a release. The number says whether the batch
since the last tag was a fix or a feature, and only someone reading those commits
knows. Run it bare to see them:

```bash
make release
```

That refuses, and prints the last tag, every commit since it, and the candidates:

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

The number already on `master` is a suggestion, not a default: it was chosen when
the *last* release closed, before anyone knew what this batch would hold.

**2. It checks before it touches anything.** Clean tree, on `master`, in sync
with `origin/master`, and no such tag either locally or on origin. A version
lower than the one `master` already claims is refused outright — a published
number means one tree, forever.

**3. It proves the tree it is about to publish.** The constant is written
*first*, then `make check` and `make dist` run against the bumped tree — so the
documentation tests are checking the release rather than the commit before it. If
either fails, the constant is put back and nothing is committed.

This is also what "the docs are up to date" means here: `docs_pages_test.go`
fails if a link is broken, if `docs/cli.md` has fallen behind the commands and
flags the binary actually has, or if a page quotes a version that does not exist.
A release cannot go out over any of those.

**4. It asks.** The tag, the commit, the artifacts and the diff, then `[y/N]`.
There is no unattended path: run without a terminal and it refuses.

**5. Two commits, one tag, one push.**

```text
Release v0.2.0        ← the tag points here; constant is 0.2.0
Start 0.2.1-SNAPSHOT  ← master carries the next version again
```

Both commits and the tag go up in a single `git push --atomic`. A branch push
that lands without its tag would leave `master` claiming a release nobody
published; atomic makes that state unreachable. If something merged to `master`
while the checks were running, the push is rejected, **nothing is published**,
and the script prints the two lines that undo it:

```bash
git tag -d v0.2.0 && git reset --hard origin/master
```

**6. The workflow publishes.** Pushing the tag is what starts it — the only
workflow here that runs without being asked, because pushing a version tag *is*
the request. It re-checks the tag against the constant, refuses a `-SNAPSHOT`
one, runs gofmt/vet/`test -race`, builds every platform in `PLATFORMS`, and
publishes a **pre-release** with `SHA256SUMS` attached and the commits since the
previous tag appended to the notes. `make release` runs `gh run watch` for you.

**If publishing fails** after the tag is already pushed, fix the cause and re-run
without moving the tag:

```bash
gh workflow run release.yml -f tag=v0.2.0
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
