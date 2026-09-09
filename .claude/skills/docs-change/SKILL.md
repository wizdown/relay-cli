---
name: docs-change
description: Use after any change a user notices, before opening a PR. Walks the "If you changed" table so the documentation lands in the same commit, then reviews the prose the tests cannot read.
---

# Documentation for a change

Rule 3: documentation lands in the same commit as the change. The table is
[Documentation is part of the change](../../../AGENTS.md#documentation-is-part-of-the-change).

## 1. Walk the table

Read your diff, find every row that matches it, and update what the row names.
The rows nobody remembers unaided: an error message a user can hit goes in
`docs/troubleshooting.md` quoting what they see; anything a user notices goes
in `CHANGELOG.md` under Unreleased; a file's job or name goes in
`docs/contributing/design.md`.

## 2. Check where each new sentence belongs

[Who reads what](../../../AGENTS.md#who-reads-what) has two tiers and a table of
questions. A sentence belongs to exactly one page. If you cannot find which,
fix that table rather than dropping the sentence into the nearest page.

One home per fact: grep for it before writing a paragraph, and if it exists,
write one clause and a link.

## 3. Let the tests read what they can

```bash
make lint-docs
```

Every link resolves, every field and default is documented, every quoted
message is one the binary prints, no user page names a removed key, no user
page carries a rationale word, and no page is over its ceiling. A failure names
the page. Fix the page, not the test.

## 4. Read what the tests cannot

Nothing checks what a sentence means. Run the `docs-reviewer` subagent on the
pages the diff touched. It reads each changed paragraph against
[Who reads what](../../../AGENTS.md#who-reads-what) and
[How a sentence reads](../../../AGENTS.md#how-a-sentence-reads) and reports; it
does not edit. Apply what it finds, or say why not.

A ceiling is not raised to fit a new paragraph. Cut a duplicate or move a
rationale to `docs/contributing/`, which has no ceiling.
