---
name: pre-pr
description: Use before opening or updating a pull request. The Before you commit checklist as commands, including the two things no hook checks: the version constant and the PR text.
---

# Before the pull request

[Before you commit](../../../AGENTS.md#before-you-commit) is the list. These are
the commands. Run them in this order and paste what they printed into the PR
body.

```bash
make check         # gofmt, vet, the whole suite
make check-fresh   # the suite with no coding CLI on PATH
git diff origin/master -- cmd/relay-cli/main.go   # must show no version change
git status         # must show no ./relay binary, no dist/
```

Then the text of the pull request itself, before it is posted:

```bash
printf '%s\n%s\n' "$title" "$body" | scripts/scan-secrets.sh
```

`.github/workflows/pr-text.yml` runs the same scan on every edit, and a red
check there means revoke the credential in relay before anything else. Catching
it here means there is nothing to revoke.

## What each one is for

- **`make check`** is the gate. It includes the documentation drift tests, so a
  config field added without its docs fails here.
- **`make check-fresh`** proves the fresh-clone property, which a local
  `make check` cannot: your machine has a coding CLI installed. See
  [The fresh-clone property](../../../docs/contributing/development.md#the-fresh-clone-property).
- **The version constant** is `master`'s to carry and `make release`'s to
  change. A PR never touches it. Nothing but this diff catches that.
- **`git status`** because a staged binary is pulled by every clone from then
  on.

## The body

`.github/pull_request_template.md` has the five headings already. Fill in every
one, and under Verification say which of these commands you actually ran, on
what. Do not claim a check you did not run: an unverified claim is worse than
an admitted gap, because it stops anyone else from looking.

Docs still owed? Run the `docs-change` skill first.
