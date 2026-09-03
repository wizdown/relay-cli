# Plan: a `relay update` command

A self-update command for `relay-cli`: check GitHub for a newer release,
download it, verify it, and replace the running binary in place.

This is a plan, not an implementation. It records the design decisions and the
constraints this repo puts on them, so the build is a matter of typing rather
than discovering.

## What it does

Five steps. Each one has a wrinkle specific to how this repo ships.

1. **Find the newest release.** `GET /repos/wizdown/relay-cli/releases`, take
   the first entry that is not a draft.
2. **Compare** its tag against `baseVersion()` with a real semver comparison.
3. **Download** the matching asset and `SHA256SUMS` into the directory the
   running binary lives in.
4. **Verify** the sha256, then smoke-test the downloaded file by running
   `<tmp> version` and checking the output names the version just fetched.
5. **Replace** with `os.Rename`. It is atomic, and it is legal on Unix while
   the binary is running, because the running process holds the open inode.

The download lands next to the target rather than in `TMPDIR` because
`os.Rename` cannot cross a filesystem boundary, and on macOS `/tmp` and
`/usr/local/bin` are routinely on different mounts.

## Four things that will bite

### `releases/latest` does not work here

`release.yml` publishes with `--prerelease`, and every release so far carries
that flag. Both the REST `releases/latest` endpoint and the web page of the
same name skip prereleases, so for this repo that endpoint returns 404 today.

The command must list releases and pick the newest itself. Sorting by
`published_at` is not enough on its own: a re-published tag can land out of
order, so compare parsed tags and use the list order only to break a tie.

Related, and worth fixing in its own commit: the install link in `readme.md`
points at `releases/latest`, which is a dead page for the same reason.

### The binary is usually not writable by the user running it

The readme installs with `sudo mv` into `/usr/local/bin/relay`, so the file
ends up owned by root. A normal-user `relay update` then fails with `EACCES`
on the rename, after a 10 MB download has already happened.

Probe the **directory** for writability before fetching anything, by creating
and removing a temp file in it. Report the fix in the message rather than the
errno.

### The asset name is built in the Makefile

`dist` maps `darwin` to `macos` in shell, so the published asset is
`relay-<version>-macos-arm64`. Nothing in the Go code knows that mapping.

Match assets by platform suffix against a small table in `update.go`, and fail
clearly when no asset matches the running platform. Only `darwin/arm64` is
published: a user who built from source on Linux must be told there is no
published build for their platform and to build from source, not left with a
silent no-op.

### A snapshot build is newer than the newest release

`master` carries `0.2.2-SNAPSHOT`, which sorts above the latest release. An
unguarded update would quietly downgrade a contributor's working binary to a
release. Refuse when the version constant carries the marker, unless `--force`
is passed.

## One thing that gets better

`xattr -c` becomes unnecessary. Gatekeeper quarantine is applied by the
application that performs the download, so a file fetched by Go's HTTP client
never carries the attribute. Self-update sidesteps the unsigned-binary dance
the readme currently walks a user through.

## Command surface

```bash
relay update           # check, download, verify, replace
relay update --check   # report what is available; write nothing
relay update --force   # update even from a -SNAPSHOT build
```

`--version x.y.z` for pinning or downgrading is deliberately left out. It is a
second feature with its own questions (may it cross a minor? what happens to a
config the older binary rejects?) and it does not have to ship with the first.

There is no `--skip-verify`. The published builds are unsigned and
unnotarised, so the checksum is the entire integrity story and cannot be
optional.

## Design decisions

| Decision | Why |
|---|---|
| GitHub API base is an unexported package var | Tests point it at an `httptest` server. Not a `RELAY_CLI_*` variable: the docs lint would then require it documented, and it is a test seam, not a user knob. |
| Hand-written semver compare | `go.mod` has no requires and stays that way. String comparison gets `0.2.10` against `0.2.9` wrong. |
| No update nudge in `run` or `check` | A separate feature. It would add a network call to commands documented as launching nothing. |
| Output goes through `Scrub` | Hard rule 6 is unconditional, whether or not the strings can carry a secret. |
| Keep the replaced binary until the new one proves out | The smoke test runs before the rename, and the old file is restored if the rename half-fails. |

Windows is the one platform where the rename does not work, because a running
executable is locked. It is already untested and unpublished, so the first
version refuses there with a message pointing at a manual download.

## Files this touches

| File | What |
|---|---|
| `cmd/relay-cli/update.go` | new; one job per file |
| `cmd/relay-cli/update_test.go` | new; `httptest`, because the suite runs with no network |
| `cmd/relay-cli/main.go` | the `commands` slice, the dispatch switch, `shortHelp`, `helpText` |
| `cmd/relay-cli/docs_pages_test.go` | add `updateFlags` to the flag set list, or the coverage test cannot see the new flags |
| `docs/cli.md` | a command table row, a flag table, a line under Versioning |
| `docs/troubleshooting.md` | one row per new message, quoted verbatim |
| `readme.md` | an Upgrading line |
| `CHANGELOG.md` | under Unreleased |
| `docs/contributing/design.md` | a codemap row for `update.go` |

### The documentation budget is the tight part

`TestUserPagesStayShort` fails the build on an overrun, and
[AGENTS.md](AGENTS.md) says to cut a duplicate before raising a ceiling.

| Page | Now | Ceiling | Headroom |
|---|---|---|---|
| `docs/cli.md` | 911 | 1000 | 89 words |
| `docs/troubleshooting.md` | 1063 | 1100 | 37 words |
| `readme.md` | 642 | 700 | 58 words |

Thirty-seven words will not cover five error rows. Expect either a real
trimming pass over `docs/troubleshooting.md` or a justified ceiling raise, and
treat that as part of the work rather than a surprise at the end.

## Tests

An `httptest` server serves a fake release list, asset bytes, and a
`SHA256SUMS` file. The cases:

- a newer release, an equal one, and an older one
- a `-SNAPSHOT` build refuses, and `--force` proceeds
- no asset matches the running platform
- a checksum mismatch leaves the existing binary byte-identical
- an unwritable directory fails before any download starts
- `--check` writes nothing at all

Make the post-download smoke test an injectable function so the tests do not
need to produce a real executable for the host platform.

## Size

Roughly 400 lines in `update.go` and 300 in its test. The documentation loop is
the slower half, because of the word ceilings above.
