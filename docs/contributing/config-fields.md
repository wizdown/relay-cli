# Changing the config

`~/.relay/config` is the whole user interface of `relay-cli`. A field added here
has to land in two more places, and the cost of missing one is not a broken build
— it is a field that exists and cannot be discovered, or a documented default
that quietly disagrees with the real one.

**Most of this is enforced by tests.** `cmd/relay-cli/docs_test.go` reflects over
the `Worker` struct and over each runtime's declared fields, and fails the build
when a field, a default or a removed key is undocumented. So the loop below is
the order to work in, not a checklist you have to be trusted to complete.

## First: which kind of field is it?

This is the decision that matters, and it is the one the layout is built around.

| | Outside `runtime_config` | Inside `runtime_config` |
|---|---|---|
| Enforced by | relay-cli itself | the CLI, via its adapter |
| Means the same for every runtime | yes | no — it is that CLI's vocabulary |
| Declared in | the `Worker` struct, `config.go` | that runtime's `ConfigFields()` |
| Examples | `max_runs_per_hour`, `max_seconds_per_run` | `model`, `max_usd_per_run` (claude), `sandbox` (codex) |

`max_seconds_per_run` and `max_usd_per_run` read like a matched pair and are
deliberately on opposite sides of that line: the wall-clock kill is relay-cli's
`context.WithTimeout` and works everywhere, while the dollar cap becomes
`--max-budget-usd` and only claude can enforce it — codex has no equivalent at
all, which is why it declares no such key rather than accepting one it would
ignore. The placement is the answer to "who kills this run?".

## Adding a runtime setting

One place. In that adapter's `ConfigFields()`, add a `runtimeField` with its
`Key`, `Kind` (`fieldString`, `fieldNumber` or `fieldBool`), whether it is
`Required`, its `Default` if it has one, an `Enum` if only a fixed set of values
is meaningful, and a `Doc` line. That table is read by the config parser (which
validates against it and rejects unknown keys and values), by `bashAdapterEnv`
(which exports each key as `RUNTIME_<KEY>`), and by the docs test.

An `Enum` is worth setting whenever the CLI would reject a wrong value: refusing
`workspace_write` when the config loads costs a startup error, and discovering it
inside a run costs a run.

Then add a row to that runtime's table in `docs/configuration.md` and use the
value in the adapter's `BuildCmd`. `make check` names anything you missed.

The `Doc` line is not decoration: it is the text printed when someone omits a
required field, so write the sentence you would want in front of you at that
moment.

## Adding a relay-cli field

**1. The code** — `config.go`:

- Add the field to `Worker` with its `json:"…"` tag.
- Add it to `workerKeys` in `config.go`. Unknown keys are refused at every level
  of the file, so a field that is not on that list is a field nobody can set —
  `TestWorkerKeysMatchTheStruct` fails until the two agree.
- Add a `default…` constant if it has a default. Make it a **bound**, never
  "unlimited": the short config has to be the safe one.
- Parse it in `LoadConfig` with a fallback for when the key is absent.
- Validate it if a wrong value would fail late, and **append to `problems`**
  rather than returning early. Every problem in a file is reported at once; a
  parser that stops at the first one turns a half-written config into a dozen
  edit-and-rerun rounds.

**2. The field table** — `docs/configuration.md`:

Add a row to the worker table. Keep the default in the **last** column as
`` `value` `` — a test parses that cell and compares it to the Go constant.

**3. The manual** — `helpText` in `main.go`:

Add it to the `THE CONFIG FILE` block. `relay help` has to stand alone:
someone running a downloaded binary with no checkout has only this.

If the field is **required**, add it to `shortHelp` too — that is the list a bare
`relay` prints, and it exists to name every decision the config will demand. An
optional field does not belong there; it has a bounded default, which is the
whole reason the summary can leave it out.

Then `make check`. If you missed a surface, the failure names it.

Note what is **not** on this list. `relay init` writes a deliberately short
starting config that links `docs/configuration.md` rather than repeating it —
adding every new field there is how it grew into a second copy of the manual that
drifted from the first.

That link, and every doc link the binary prints, is a **full URL** built from
`docsBase` in `main.go`. The config lands in `~/.relay/`, and whoever reads it
may have downloaded a binary and nothing else, so a relative `docs/` path there
points at nothing. `TestUserFacingDocLinksAreFullURLs` fails on one.

## Removing or renaming a field

A removed key is **not** just a deleted field. Dropping it from `workerKeys`
would already make a config carrying it fail — but as an unknown key, offering
the nearest spelling, which for a setting that genuinely moved is a worse answer
than none.

1. Delete it from the `Worker` struct and from `workerKeys`, then add it to
   `removedKeys` in `config.go`, mapped to **what to do instead**, not merely
   "removed". The map value is printed to a person whose fleet just refused to
   start; it should end the problem, not name it — and it wins over the spelling
   suggestion an unknown key would otherwise get.
2. Delete it from the manual and from `docs/configuration.md` — every trace,
   including any sentence explaining what it used to do.

**The error message is the whole migration path**, which is why step 1 carries
the weight. The user documentation describes what this version accepts, not how
it got there: a table of dead keys is a page that grows forever and that nobody
reading a current config needs. Someone still carrying one is not reading the
docs anyway — their fleet has already refused to start, and the message in front
of them names the key and its replacement.

`TestEveryRemovedKeyExplainsItself` fails the build on an empty replacement.

## Changing a default

For a relay-cli field, change the `default…` constant. For a runtime setting,
change `Default` on its `runtimeField`. Either way, update the last column of the
docs table and the inline comment in the manual. Two tests cover this —
`TestHelpQuotesTheRealDefaults` and `TestConfigDocsQuoteTheRealDefaults` — because
a default is what someone sets their spend ceiling against, and docs disagreeing
with the code is worse than no docs.

If you are **loosening** a bound, say why in the commit. These numbers are the
reason a barely-configured worker is a safe worker.

## Making a field required

Required fields are for decisions relay-cli genuinely cannot make for anyone. The
bar is not "important" — it is that **no default is safe**:

- `repo_dir` decides which repo's `CLAUDE.md`, skills and tooling the agent
  inherits, and which checkout it may rewrite.
- `model` has a CLI default, but that default moves between CLI versions, so an
  unchanged config would silently change what a worker costs.

Compare with `max_usd_per_run`: `5` is a safe, bounded, fail-closed answer, so it
defaults. **Default where there is a safe answer, require where there is not.**
Mandatory fields with one obvious answer train people to paste past them, which
cheapens the required-ness of the ones that matter.

Add a placeholder to the init template when a required field has no sensible
starting value, and reject that exact string by name in `config.go` — the way
`repo_dir` and `relay_mcp` both work. "`/path/to/your/repo` is not a directory"
reads like a typo; "still the placeholder from `relay init`" reads like the
step it is.

## What must not go in the config

**Anything about what an agent is *for*.** Its instructions, capabilities, claim
limits and lease TTL live on the relay agent, set with `update_agent`. That is
not a stylistic preference: agent identity delivered over MCP reaches a session
that is **already running**, and stays correct when relay changes. A file here
could do neither.

`system_prompt`, `system_prompt_file`, `min_run_interval_seconds` and
`permission_mode` were removed for exactly this reason and are rejected by name.
So is `codex_mcp_transport`, for a different one: there is no transport left to
choose, because a codex worker is handed relay as a streamable-HTTP MCP server
directly.

`runtime_args` was removed for a related one: raw argv passthrough could silently
override flags the harness depends on — including `--permission-mode`, which is
what makes a headless run work at all — and it bypassed every validation on this
page. A setting worth having is worth declaring.

Before adding a field, check it is really about *when and where a CLI runs*. If
it is about *what the agent should do*, it belongs in relay.

## The tests behind this

In `cmd/relay-cli/docs_test.go`:

| Test | Fails when |
|---|---|
| `TestEveryWorkerFieldIsDocumented` | a `Worker` json tag is missing from the docs table or from `helpText` |
| `TestEveryRuntimeConfigFieldIsDocumented` | a runtime declares a key the docs never mention, or declares one with no `Doc` line |
| `TestConfigDocsQuoteTheRealDefaults` | a docs table default disagrees with the code, for a relay-cli field or a runtime one |
| `TestEveryRemovedKeyExplainsItself` | a removed key has no replacement text |
| `TestConfigDocsExampleValidates` | the complete example in the docs no longer survives the validator |

And in `config_test.go` / `init_test.go`: `TestInitTemplateValidates` proves the
config `init` writes still loads once its placeholders are filled in.

If one of these fails, update the document it names. **Don't relax the test** —
it is the only thing between "we added a field" and "nobody can find out it
exists".
