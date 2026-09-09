---
name: release
description: Use when asked to cut a release, tag a version, or bump the version. The version is mandatory and is never guessed, including by you.
---

# Cutting a release

The whole flow is
[Cutting a release](../../../docs/contributing/development.md#cutting-a-release).

## If you were given a version

```bash
make release VERSION=x.y.z
```

It checks the tree, writes the version, proves the bumped tree with `make check`
and `make dist`, shows you the tag, the artifacts and the diff, and asks. Answer
nothing on its behalf. Without a terminal it refuses, which is the correct
outcome for an unattended session.

## If you were not

Run it bare, show the output, and stop:

```bash
make release
```

It prints the last tag, what `master` claims, the commits since, and the two
candidate numbers. Hand that to whoever asked and use the number they give back.

Recommend one, with the reason from the commit list. Do not pass a version the
user did not choose. The number already on `master` is a suggestion made before
anyone knew what the batch would hold, so it is not an answer either.

## What not to do

- Do not edit the version constant in `cmd/relay-cli/main.go` by hand. `master`
  carries `x.y.z-SNAPSHOT`, and only `make release` clears the marker.
- Do not type a version into a docs page. `make release` rewrites the sample
  output lines, and a test fails on a page that quotes a version ahead of the
  constant.
- Do not move or delete a tag that published. It is superseded by a new
  version, not rewritten.
- Check `CHANGELOG.md` has something under Unreleased first. A release says
  what changed, even when that is "docs only", and the script refuses without
  it.
