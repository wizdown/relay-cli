# Changing `.worker-config`

`.worker-config` is the whole user interface of `relay-cli`. A field added
here has to land in four places, and the cost of missing one is not a broken
build — it is a field that exists and cannot be discovered, or a documented
default that quietly disagrees with the real one.

**Most of this is enforced by tests.** `cmd/relay-cli/docs_test.go`
reflects over the `Worker` struct and fails the build when a field, a default or
a removed key is undocumented. So the loop below is the order to work in, not a
checklist you have to be trusted to complete.

## Adding a field

**1. The code** — `config.go`:

- Add the field to `Worker` with its `json:"…"` tag.
- Add a `default…` constant if it has a default. Make it a **bound**, never
  "unlimited": the short config has to be the safe one.
- Parse it in `LoadConfig` with a fallback. Unknown keys are ignored by design,
  so a typo does nothing — that is exactly why the documentation matters.
- Validate it if a wrong value would fail late. A bad value should stop the
  start, not surface 120 times an hour in a log nobody reads.

**2. The example** — `.worker-config.example`:

Show it on a worker, with a comment saying what it defaults to and when you'd
change it. This is the file people copy, so a field that is only *mentioned* has
still not been shown. Comments are stripped before parsing, so write for a human
mid-edit.

**3. The field table** — `docs/configuration.md`:

Add a row to the required or optional table. Keep the default in the second
column as `` `value` `` — a test parses that column and compares it to the Go
constant.

**4. The manual** — `helpText` in `main.go`:

Add it to the `THE CONFIG FILE` block. `relay-cli help` has to stand alone:
someone running a downloaded binary with no checkout has only this.

Then `make check`. If you missed a surface, the failure names it.

## Removing or renaming a field

A removed key is **not** just a deleted field. A config still carrying it would
otherwise be silently ignored — and for anything that changes what a worker
does, silence is the wrong answer.

1. Add it to `removedKeys` in `config.go`, mapped to **what to do instead**, not
   merely "removed". The map value is printed to a person whose fleet just
   refused to start; it should end the problem, not name it.
2. Delete it from the example and the manual.
3. In `docs/configuration.md`, move it to the removed-keys list under
   *Things that will bite you otherwise*, so someone hitting the error can search
   for the key and find out what happened.

`TestEveryRemovedKeyIsDocumented` checks both the mention and that the
replacement text is non-empty.

## Changing a default

Change the `default…` constant, then update the second column of the docs table
and the inline comment in the manual. Two tests cover this —
`TestHelpQuotesTheRealDefaults` and `TestConfigDocsQuoteTheRealDefaults` — because
a default is what someone sets their spend ceiling against, and docs disagreeing
with the code is worse than no docs.

If you are **loosening** a bound, say why in the commit. These numbers are the
reason a two-field worker is a safe worker.

## What must not go in `.worker-config`

**Anything about what an agent is *for*.** Its instructions, capabilities, claim
limits and lease TTL live on the relay agent, set with `update_agent`. That is
not a stylistic preference: agent identity delivered over MCP reaches a session
that is **already running**, and stays correct when relay changes. A file here
could do neither.

Five keys were removed for exactly this reason and are now rejected by name:
`system_prompt`, `system_prompt_file`, `min_run_interval_seconds`,
`permission_mode`, `codex_mcp_transport`.

Before adding a field, check it is really about *when and where a CLI runs*. If
it is about *what the agent should do*, it belongs in relay.

## The tests behind this

In `cmd/relay-cli/docs_test.go`:

| Test | Fails when |
|---|---|
| `TestEveryConfigFieldIsDocumented` | a `Worker` json tag is missing from the example, the docs table, or `helpText` |
| `TestExampleShowsEveryOptionalField` | a field is mentioned but never appears as a JSON key in the example |
| `TestConfigDocsQuoteTheRealDefaults` | the docs table's default disagrees with the Go constant |
| `TestEveryRemovedKeyIsDocumented` | a removed key isn't in the docs, or has no replacement text |
| `TestShippedExampleValidates` | the shipped example no longer survives its own validator |

If one of these fails, update the document it names. **Don't relax the test** —
it is the only thing between "we added a field" and "nobody can find out it
exists".
