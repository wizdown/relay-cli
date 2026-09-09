---
name: steward
description: Use when driving a pull request on this repo to green after it is opened, or when acting on a CI failure or a review comment on one. Repo-specific rules that override a generic PR-driving posture.
---

# Driving a pull request here

## Where the signal comes from

`ci.yml` runs on every pull request, and `pr-text.yml` runs on the title and
body. A new push cancels the superseded run, so wait for the run on the head
commit rather than the first one to finish. For a branch with no pull request
open, dispatch it: `gh workflow run ci.yml --ref <branch>`.

Reproduce locally before pushing anything:

```bash
make check
make check-fresh
```

Say in the pull request which of the two you ran. `make check-fresh` is the one
that catches a broken fresh-clone seam, and it is the failure most often
mistaken for a runner problem.

## A red pr-text check

Revoke the credential in relay before anything else, then edit the title or
body. The value is not printed in the run log, because the log is public. Find
it in your own text.

## A red ci check

- **A documentation test** names the page it read. Fix that page. Never skip a
  test, relax a pattern or raise a word ceiling to get green. A ceiling comes
  down by cutting a duplicate or moving a rationale to `docs/contributing/`,
  which has no ceiling.
- **`TestNoBuildOutputIsTracked`** means a binary is committed. `git rm --cached`
  it; do not raise the ceiling.
- **A test that fails only in CI** is usually the fresh-clone property, not a
  flake: CI has no coding CLI installed and your machine does. Reproduce it with
  `make check-fresh`. See
  [The fresh-clone property](../../../docs/contributing/development.md#the-fresh-clone-property).

## Never

- **Touch the version constant** in `cmd/relay-cli/main.go`. `master` carries
  `x.y.z-SNAPSHOT` and only `make release` changes it. A pull request that moves
  it is wrong however green it is.
- **Paste output that quotes a connector URL** into a comment. Describe it as
  "HTTP 401 from the configured endpoint".
- **Widen the diff.** One concern per pull request:
  [Pull requests](../../../docs/contributing/development.md#pull-requests).

Before each push, run the `pre-pr` skill. Documentation owed by the fix lands in
the same commit as the fix: the `docs-change` skill.
