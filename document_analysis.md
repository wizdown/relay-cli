# Documentation review: relay-cli

Scope: every documentation surface in the repository as of this branch.

- `readme.md`, `SECURITY.md`, `AGENTS.md`, `CLAUDE.md`, `worker-rules.md`
- `docs/cli.md`, `docs/configuration.md`, `docs/runtimes.md`, `docs/troubleshooting.md`, `docs/working-directory.md`
- `docs/contributing/adapters.md`, `config-fields.md`, `design.md`, `development.md`
- The text the binary itself prints: `shortHelp` and `helpText` in `cmd/relay-cli/main.go`, and the config template in `cmd/relay-cli/init.go`
- The three doc tests: `docs_test.go`, `docs_pages_test.go`, `doclinks_test.go`

Method: read every page end to end, checked quoted error strings and test names against the Go source, ran `go test ./...` (green), and measured length and sentence shape per file.

## Verdict

The documentation is **complete and accurate but not easy**. A new user has to read far more than they should to reach a working worker, and what they read is written in a rationale-first, aphoristic style that suits a design memo, not a getting-started guide.

Three things stand out:

1. **Volume.** About 21,000 words across 14 markdown files, plus a 250-line manual compiled into the binary. The user-facing pages alone (readme plus `docs/*.md`) are 10,400 words. The readme is 1,430 words and the first install command is on line 42.
2. **Rationale everywhere.** `AGENTS.md` states the right rule ("the readme is for users... rationale goes to `docs/contributing/design.md`"), but nearly every user page still explains *why* a thing is the way it is next to *what* it is. The typical paragraph states a fact, then justifies it, then restates it as a maxim.
3. **Repetition.** The same dozen facts (polls are free, codex has no spend cap, the dashboard is read-only, never commit the config, the credential is shown once, point `repo_dir` somewhere you can lose) appear in four to six files each. The repo's own "one home per fact" rule is not followed.

What is good and should be kept: the drift tests, the doc map in `AGENTS.md`, the `relay check` walkthrough, the troubleshooting table quoting real output, the honest versioning section, and the "ladder" shape of `docs/working-directory.md`.

## Metrics

Prose metrics exclude code blocks, tables and headings. "Longest" is the longest single sentence in words.

| File | Words | Avg words per sentence | Em-dashes | Longest sentence |
|---|---|---|---|---|
| `readme.md` | 1,428 | 16.5 | 26 | 58 |
| `docs/configuration.md` | 2,898 | 23.5 | 51 | 174 |
| `docs/runtimes.md` | 1,650 | 24.4 | 25 | 60 |
| `docs/working-directory.md` | 1,655 | 20.2 | 25 | 72 |
| `docs/cli.md` | 1,287 | 21.8 | 23 | 51 |
| `docs/troubleshooting.md` | 1,252 | (table) | 20 | 28 |
| `SECURITY.md` | 617 | 19.6 | 9 | 47 |
| `worker-rules.md` | 411 | 21.9 | 7 | 57 |
| `AGENTS.md` | 4,349 | 22.5 | 72 | 83 |
| `docs/contributing/development.md` | 1,732 | 19.7 | 24 | 47 |
| `docs/contributing/config-fields.md` | 1,617 | 20.6 | 21 | 60 |
| `docs/contributing/design.md` | 1,110 | 19.8 | 18 | 75 |
| `docs/contributing/adapters.md` | 954 | 17.6 | 17 | 47 |
| `helpText` (in binary) | ~1,900 | | | 250 lines |

Reference points: a good README quickstart is 300 to 600 words. Average sentence length above 20 words is where comprehension drops for skim-readers, and technical docs usually aim for 15. 340 em-dashes across the set is roughly one every 60 words.

How often one fact is stated (files that contain it):

| Fact | Files |
|---|---|
| A poll or `check` "spends nothing" / "costs nothing" | 6 files, 12 times |
| The dashboard binds `127.0.0.1` | 6 files, 10 times |
| Codex has no per-run spend cap | 5 files, 7 times |
| The credential is shown once | 4 files, 5 times |
| Never commit the config | 4 files, 5 times |
| Point `repo_dir` somewhere you are willing to have rewritten | 4 files |
| The dashboard is read-only | 4 files |
| "one worker = one agent × one directory × one CLI" | 4 files |
| Other platforms build because CGO is off | 3 files plus the Makefile |

## Findings

### 1. The getting-started path is buried in the readme

A first-time reader wants: what this is, what they need, install, four commands, done. The readme has that skeleton but pads every step.

- **The intro** spends its second and third paragraphs on the cost model and a beta warning before the reader knows whether the tool is for them. It also assumes the reader already knows what Relay is. One sentence saying what Relay is (a task board where you delegate work to agents) is missing.
- **Prerequisites** is four bullets with nested sub-bullets of rationale ("each CLI can answer that from its own stored credentials, without spending anything"; "Nothing launches, because every worker using it would fail in the same second"). A prerequisite list should be three lines.
- **Install** leads with `gh release download`, then admits `gh` needs a login even for a public repo. The plain download link should come first.
- **Step 1** tells the user to call `onboard_agent` and `issue_agent_credential`, which are MCP tool names, then says "How you invoke them is relay's to document". The reader is handed jargon and sent elsewhere at the one step relay-cli cannot skip. Describe the outcome in plain words ("create an agent, issue it a credential, copy the connector URL") and link Relay's page for the mechanics.
- **Step 2** shows the config, then follows it with five bold bullets, each a paragraph, covering commented-out workers, `repo_dir`, the rewrite warning, ceilings and the codex cap comparison. All of that is already in `docs/configuration.md` and `docs/runtimes.md`. Two of the bullets are also in `helpText` and the init template.
- **Safeguards** and **Versioning** are full sections in a quickstart document. Safeguards is a duplicate of the table in `docs/configuration.md`; Versioning belongs in a short note or in `docs/cli.md` next to `relay version`.

Net: the readme carries roughly 900 words that exist elsewhere. The remaining 500 are the quickstart.

### 2. Rationale is mixed into every user page

`AGENTS.md` has the right rule and `helpText` has the right comment ("a section states what a thing IS and what it DEFAULTS to; the argument for why belongs in docs/"). The pages do not follow it. Examples, one per page:

- `docs/cli.md` opens with "The MCP probe and the config parser are built in, so it needs neither `jq` nor `curl`." Nobody starting from this version knows there was ever a shell poller that needed them. This is history, which the doc rules forbid.
- `docs/configuration.md`, the `repo_dir` row, is 93 words in a table cell and ends with the rewrite warning. A reference row should be one or two sentences; the warning has its own home.
- `docs/runtimes.md`: "It checks capabilities rather than a version number deliberately: the adapter depends on those flags, not on a release, and a version gate would block working installs whenever it guessed high." A user needs "relay-cli checks that the installed CLI supports the flags it uses". The rest is `design.md` material.
- `docs/working-directory.md`, Step 0, spends a paragraph on why `~/.relay/worker-rules.md` is the wrong place for per-agent instructions. The ladder is excellent; this paragraph interrupts rung zero.
- `SECURITY.md`: "the moment to catch that is before the commit rather than after the revocation" and "Rotation is the fix; the whole URL changes." Both are true and both are padding on a page whose reader wants the handling rules and the reporting address.

The recurring word "deliberately" (22 occurrences across the docs) is a reliable marker of a sentence that should move to `docs/contributing/`.

### 3. Sentence style works against skimming

The house style is aphoristic: short declarative maxims, inverted clauses, and a reveal after an em-dash. It reads well once and badly under pressure. Some examples with plainer rewrites:

| Current | Plainer |
|---|---|
| "Polling costs nothing. Every `poll_seconds` a worker asks relay over plain HTTP, **with no model running**, whether it has a task; a CLI session starts only if the answer is yes. An idle worker costs one HTTP handshake and zero tokens." | "A worker polls Relay over HTTP every `poll_seconds`. Polling runs no model and costs nothing. A CLI session starts only when a task is waiting." |
| "With neither CLI installed `init` writes nothing and says so: a worker is a coding CLI, and there is no useful config for a machine with none." | "If neither CLI is installed, `relay init` writes nothing and tells you which to install." |
| "Both placeholders are rejected by name, so an unfinished config fails in `check` rather than inside a run you have already paid for." | "`relay check` rejects a config that still contains either placeholder." |
| "The one bound that is not yours to remove is the floor under poll_seconds: it protects relay rather than you, since an empty poll costs you nothing but relay still has to answer it. A config below the floor is rejected rather than clamped." | "`poll_seconds` cannot go below 5. A lower value is rejected." |
| "Starting is asked for by name rather than being the default, because it launches autonomous sessions that spend money." | "`relay` with no command prints help. Use `relay run` to start." |

Rules that would fix most of it: lead with the fact, one idea per sentence, at most one em-dash per paragraph, and no sentence that only restates the previous one as a maxim.

### 4. The same content lives in three tiers

The config reference exists in `docs/configuration.md`, in `helpText`, and in abbreviated form in `shortHelp`, the init template and the readme. The model list exists in `docs/configuration.md`, `helpText`, `shortHelp` and `AGENTS.md`. The runtime comparison exists in `docs/runtimes.md`, `helpText`, the readme and `AGENTS.md`.

Some duplication is a design choice: `helpText` has to stand alone for someone with only the binary. That is a good reason to keep one full copy in the binary and one in `docs/`. It is not a reason for the readme, `shortHelp`, `AGENTS.md` and the init template to each carry a third or fourth partial copy with its own wording.

### 5. Reference pages contain narrative, and narrative pages contain reference

- `docs/configuration.md` has 12 H2 sections. Five are reference (the field tables, safeguards) and seven are narrative ("Polls and runs", "More than one worker", "Removing a worker", "Gotchas", "What is validated before anything launches", "Model names", "When a worker keeps relaunching"). A reader looking up a field scrolls through essays; a reader with a problem does not think to look in a page called Configuration.
- `docs/runtimes.md` quotes four full error blocks under "The startup check". Each already has a row in `docs/troubleshooting.md`. Quote them once, in troubleshooting.
- `docs/troubleshooting.md` is a single 30-row table with no grouping and cells up to 70 words. Grouped under four headings ("`check` fails", "start refuses", "a run fails", "config rejected") with shorter cells it becomes scannable.
- `docs/cli.md` is the best-shaped user page, but "Help comes in two sizes" is two paragraphs for a two-row table, and "It cannot change anything" repeats the read-only fact that is in the previous section, `SECURITY.md`, `helpText` and the readme.

### 6. Stale or wrong content the tests cannot catch

The drift tests check links, names, defaults and versions. They do not check meaning, and the following slipped through:

| Where | Problem |
|---|---|
| `docs/contributing/config-fields.md`, "The tests behind this" | Names `TestEveryRemovedKeyIsDocumented`, which does not exist. The real test is `TestEveryRemovedKeyExplainsItself`. The row also says a removed key must be "in the docs", which contradicts the page's own rule to delete every trace. |
| `docs/contributing/development.md`, "Cutting a release" | Says the release notes carry an `xattr -d` line. The readme and `release.yml` both use `xattr -c`. |
| `docs/contributing/development.md`, "The fresh-clone property" | Names one seam (`checkRuntime`). `AGENTS.md` names two (`checkRuntime` and `installedRuntimes`), and `init.go` confirms the second exists. |
| `docs/contributing/design.md`, "Directory layout" | Omits `init.go`, `runtime_codex.go`, `redact.go`, `docs_test.go` and `docs_pages_test.go`. The codemap in `AGENTS.md` has them. Two codemaps will keep diverging; keep one. |
| `docs/contributing/design.md`, "The worker rules" | Says only how `claude` receives the contract. Codex receives it in the prompt, which `docs/runtimes.md` explains. |
| `docs/contributing/adapters.md`, "Adding a native adapter" | After the six numbered steps, the page switches without a heading to the bash-adapter contract ("For reference, an adapter is sourced (not executed)..."), including a `RUN_OUTCOME` vocabulary (`budget_exhausted`) that differs from the Go one (`outcomeBudget`) named a few lines earlier. The two contracts need two sections. |
| `helpText`, "GETTING STARTED" | Says "the free demo workspace". The readme says "the free workspace". Pick one. |
| `docs/cli.md`, opening paragraph | References `jq` and `curl`, which only make sense against the removed shell poller. |

### 7. Contributor documentation is larger than the code needs

`AGENTS.md` (4,350 words) plus four contributing pages (5,400 words) is 9,700 words for a single-package Go binary whose readme says public contributions are not open. `AGENTS.md` restates most of `development.md` (commands, hooks, CI, versions, releases), most of `config-fields.md` (the config loop), the runtime table from `docs/runtimes.md`, and the codemap from `design.md`. When they disagree (see finding 6), a contributor cannot tell which is current.

The right shape is the one `CLAUDE.md` already demonstrates: `AGENTS.md` becomes the short list of hard rules plus the doc map and links, and each topic is written once under `docs/contributing/`.

### 8. Gaps

- **No one-line explanation of Relay** for a reader who lands on the repo cold.
- **No `docs/` index.** The readme table is the only map, and its labels differ from the page titles ("Commands & dashboard" vs "Commands and the dashboard").
- **No `CHANGELOG.md`.** Release notes exist only on GitHub Releases, and the docs rules route every "what changed" to release notes. A changelog in the repo makes that route visible.
- **No CONTRIBUTING.md.** Fine while contributions are closed, but the "not open yet" sentence sits at the bottom of the readme under Versioning where nobody looks for it.
- **Linux and Intel Mac users** are told to build from source in the readme; the same "not published yet, but it builds" note is in `AGENTS.md`, `development.md` and the Makefile. State it once in the readme.

## Recommendations

Ordered by impact on a new user. Word counts are targets, not limits.

### P0: rewrite the readme (target 500 to 600 words)

Suggested structure:

```
# relay-cli
One paragraph: Relay is X. relay-cli runs agents on your machine that pick up
tasks you delegate in Relay, do them with Claude Code or Codex, and hand back
the result. Beta, 0.x.

## Requirements
- A Relay workspace (free tier works)
- Claude Code or Codex CLI, installed and signed in
- macOS Apple Silicon binary; other platforms build from source (Go 1.22+)

## Install
Download from the releases page, verify, chmod, xattr -c, move to PATH.
(gh alternative in one line.)

## Quickstart
1. In Relay: create an agent, issue it a credential, copy the connector URL.
2. `relay init`, then replace `relay_mcp` and `repo_dir`.
3. `relay check`
4. `relay run`, then delegate a task in Relay.
One short sample of the run output.

## Stopping and pausing
Ctrl-C. `touch ~/.relay/state/<name>/PAUSED`.

## Documentation
Table linking the five docs pages.

## Security, License
Two lines each, linking SECURITY.md and LICENSE.
```

Move out of the readme: the cost model paragraph (to `configuration.md`, Safeguards), the Safeguards section (already in `configuration.md`), the five bullets after the config sample (already in `configuration.md`, `runtimes.md` and `working-directory.md`), Versioning (to `docs/cli.md` under `relay version`, three sentences), and the sub-bullets under Prerequisites.

### P1: make `docs/configuration.md` a reference (target 1,400 words)

- Keep: the `~/.relay/` layout, the example config, the four field tables, the aliases table, the safeguards tables.
- Shorten every table cell to at most two sentences. Move the `repo_dir` rewrite warning to one place (the field row, one sentence).
- Merge "Polls and runs" into Safeguards as its two-column table plus one sentence.
- Cut "More than one worker" to the second example and its four bullets.
- Cut "Removing a worker" to three lines.
- Merge "Gotchas" and "What is validated before anything launches" into one "Validation" section of six bullets, or move the list to troubleshooting.
- Move "A model this build has not heard of" to a short note under the aliases table, and the quoted error to troubleshooting.

### P1: shorten `docs/runtimes.md` (target 700 words)

- Keep the comparison table and the two "What a run does" lists, trimmed to one sentence per bullet.
- Replace "The startup check" and its four error blocks with one paragraph: what is checked (installed, flags, signed in), what fails the start, what only warns, and the two `RELAY_CLI_SKIP_*` variables. Point at troubleshooting for the messages.
- Drop the "checks capabilities rather than a version" rationale, the "worse than no check" rationale, and the `CODEX_HOME` trade-off paragraph (move the last to `design.md` if it is worth keeping).

### P1: regroup `docs/troubleshooting.md`

Four headings: **`relay check` fails**, **`relay run` refuses to start**, **A run fails or does nothing**, **The config is rejected**. Cap each cell at two sentences: what it means, what to do. Keep the quoted strings, they are the page's best feature.

### P1: trim `docs/working-directory.md` (target 1,100 words)

The ladder is the right shape. Cut Step 0 to the directory, the config line, the four-bullet "arrives with" list and the rewrite warning. Move the `worker-rules.md` override to a two-line note at the end of the page (it is fleet-wide, not per-directory). Cut "Two rules keep it useful" and "What comes along from your machine" to half.

### P1: tidy `docs/cli.md` (target 900 words)

Delete the opening paragraph's second sentence. Collapse "Help comes in two sizes" to its table. Fold "It cannot change anything" into "What the dashboard shows" as one bullet. Move the Versioning explanation here in three sentences.

### P2: deduplicate the contributor tier

- Cut `AGENTS.md` to roughly 1,200 words: what the project is (three lines), the commands block, the hard rules (no credentials, no version bump, docs in the same commit, dashboard read-only, runtime logic in adapters), the doc map table, and links. Everything else already has a home in `docs/contributing/`.
- Keep one codemap. `design.md` "Directory layout" and the `AGENTS.md` table describe the same tree; keep the table in `design.md` and link it.
- Fix the eight stale items in finding 6.
- Split `adapters.md` into "Native adapters" and "The bash bridge (gated off)" sections with a heading between them.
- `config-fields.md` and `development.md` are the right length and shape once the duplicates in `AGENTS.md` are gone.

### P2: adopt a short style rule, and enforce the part a test can hold

Add to the "Documentation rules" section of `AGENTS.md`:

- State what first. Why goes in `docs/contributing/`, or nowhere.
- One idea per sentence, under 20 words on average.
- One em-dash per paragraph at most.
- A fact is stated in full once. Everywhere else, one clause and a link.
- No "deliberately", "on purpose", "not incidental" in a user page.

The repo already enforces doc rules with tests, so add a word ceiling per user page to `docs_pages_test.go` (for example 700 for the readme, 1,500 for `configuration.md`, 1,000 for the rest). A page that grows past it fails the build the same way an undocumented field does. Nothing else in the current suite stops a page from doubling.

### P3: fill the gaps

- Add a one-sentence definition of Relay to the top of the readme.
- Add `docs/README.md` with the five-page index, or make the readme table's labels match the page titles.
- Add `CHANGELOG.md`, filled by `make release` from the same commit list the workflow already appends to the release notes.
- Move "Public contributions are not open yet" to its own short heading near the top of the readme, or into a `CONTRIBUTING.md` of three lines.

## Suggested per-page budgets after the rewrite

| Page | Now | Target |
|---|---|---|
| `readme.md` | 1,430 | 550 |
| `docs/configuration.md` | 2,900 | 1,400 |
| `docs/runtimes.md` | 1,650 | 700 |
| `docs/working-directory.md` | 1,650 | 1,100 |
| `docs/cli.md` | 1,290 | 900 |
| `docs/troubleshooting.md` | 1,250 | 1,000 |
| `SECURITY.md` | 620 | 400 |
| `AGENTS.md` | 4,350 | 1,200 |
| `docs/contributing/*` | 5,400 | 4,000 |
| Total | ~20,500 | ~11,250 |

That halves the reading load without removing a single fact: every cut above is a duplicate, a rationale that moves to `docs/contributing/`, or a sentence restating the one before it.

## Order of work

1. Readme rewrite (P0). One PR, one day. Biggest effect on the first five minutes.
2. `configuration.md`, `runtimes.md`, `troubleshooting.md` (P1). One PR each; the drift tests will name anything a cut breaks.
3. `working-directory.md` and `cli.md` (P1).
4. `AGENTS.md` and `docs/contributing/` deduplication plus the stale fixes (P2).
5. Style rule and the word-ceiling test (P2), so the pages stay short.
6. Gaps (P3).
