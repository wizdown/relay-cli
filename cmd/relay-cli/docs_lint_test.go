package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Lint for the documentation's prose: the checks that used to be done by
// reading.
//
// docs_test.go holds the config reference to the code, and docs_pages_test.go
// holds the pages to each other. This file holds the prose to the rules in
// AGENTS.md that a machine can read: a quoted message is one the binary
// prints, a removed key is gone from user pages, the model tables match the
// adapters, every environment variable and worker state is documented, every
// page is on the map, and user pages carry no rationale words. Run them alone
// with `make lint-docs`.

// userPages is every user-facing page, from the ceilings table.
func userPages() []string {
	var out []string
	for page := range userPageCeilings {
		out = append(out, page)
	}
	sort.Strings(out)
	return out
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	return mustRead(t, filepath.Join(repoRoot, rel))
}

// nonTestSource is every non-test Go file in the package, concatenated: the
// strings that can reach a user's terminal or log.
func nonTestSource(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(body)
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	tableRowRe  = regexp.MustCompile("(?m)^\\| `([^|]*)$")
	backtickRe  = regexp.MustCompile("`([^`]+)`")
	placeholder = regexp.MustCompile(`…|\.\.\.|\bN\b|\bX\b|<[a-z]+>|"[^"]*"|\((claude|codex)\)|\b(claude|codex)\b`)
)

// A troubleshooting row whose first cell is a backticked message promises the
// reader will see exactly that text. The code is formatted, so the docs use
// placeholders (…, N, X, <cli>, a quoted name) where a value goes; the literal
// fragments between them are what has to exist. A fragment shorter than ten
// characters is too common to prove anything and is skipped.
func TestDocsQuoteMessagesTheBinaryPrints(t *testing.T) {
	src := nonTestSource(t)
	doc := readRepoFile(t, "docs/troubleshooting.md")

	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.SplitN(strings.TrimPrefix(line, "| "), " | ", 2)
		for _, m := range backtickRe.FindAllStringSubmatch(cells[0], -1) {
			quoted := m[1]
			var checked []string
			found := false
			for _, frag := range placeholder.Split(quoted, -1) {
				frag = strings.TrimSpace(frag)
				if len(frag) < 10 {
					continue
				}
				checked = append(checked, frag)
				if strings.Contains(src, frag) {
					found = true
				}
			}
			if len(checked) > 0 && !found {
				t.Errorf("docs/troubleshooting.md quotes %q, but no non-test Go file prints %s.\n"+
					"Quote what the binary prints, or use a placeholder (…, N, X, <cli>) where a value goes.",
					quoted, quoteAll(checked))
			}
		}
	}
}

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, " or ")
}

// User docs describe what this version accepts. A key that was removed has its
// migration in the error message, not in a page that would otherwise grow a
// table of dead keys. Contributor pages may still name them as history.
func TestDocsCarryNoRemovedKeys(t *testing.T) {
	current := map[string]bool{}
	for _, f := range workerFields() {
		current[f] = true
	}
	for _, rt := range supportedRuntimes() {
		for _, f := range rt.ConfigFields() {
			current[f.Key] = true
		}
	}
	pages := map[string]string{"helpText": helpText, "shortHelp": shortHelp}
	for _, page := range userPages() {
		pages[page] = readRepoFile(t, page)
	}
	for key := range removedKeys {
		if current[key] {
			continue // moved rather than removed: the name is still a live key
		}
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\b`)
		for name, body := range pages {
			if re.MatchString(body) {
				t.Errorf("%s mentions %q, a key this version rejects. User docs describe the present; "+
					"the error message carries the migration.", name, key)
			}
		}
	}
}

// The alias table in docs/configuration.md and the model list in the manual
// are read by someone choosing what to pay for. Both are held to the adapters:
// every alias the code accepts is in both, resolving to the same id, and the
// docs table lists nothing the code does not.
func TestDocsModelTablesMatchTheAdapters(t *testing.T) {
	docs := readRepoFile(t, "docs/configuration.md")
	aliasRowRe := regexp.MustCompile("(?m)^\\| `([a-z0-9.-]+)` \\| `([a-z0-9.-]+)` \\|$")

	known := map[string]string{}
	for _, rt := range supportedRuntimes() {
		for _, f := range rt.ConfigFields() {
			for alias, id := range f.Aliases {
				known[alias] = id
				row := fmt.Sprintf("| `%s` | `%s` |", alias, id)
				if !strings.Contains(docs, row) {
					t.Errorf("runtime %q pins %s → %s, but docs/configuration.md has no row %q", rt.Name(), alias, id, row)
				}
				if !strings.Contains(helpText, alias) || !strings.Contains(helpText, id) {
					t.Errorf("runtime %q pins %s → %s, but the manual (helpText) does not list both", rt.Name(), alias, id)
				}
				if !strings.Contains(shortHelp, alias) {
					t.Errorf("alias %q is missing from shortHelp, which names every required decision", alias)
				}
			}
			for _, id := range f.Enum {
				if !strings.Contains(docs, "`"+id+"`") {
					t.Errorf("runtime %q accepts %s %q, but docs/configuration.md never names it", rt.Name(), f.Key, id)
				}
			}
		}
	}
	for _, m := range aliasRowRe.FindAllStringSubmatch(docs, -1) {
		if id, ok := known[m[1]]; !ok || id != m[2] {
			t.Errorf("docs/configuration.md says %s runs %s, but no adapter pins that", m[1], m[2])
		}
	}
}

// An environment variable the code reads is a switch nobody can find unless a
// page and the manual name it.
func TestDocsNameEveryEnvironmentVariable(t *testing.T) {
	src := nonTestSource(t)
	docs := readRepoFile(t, "docs/configuration.md") + readRepoFile(t, "docs/runtimes.md")
	seen := map[string]bool{}
	for _, v := range regexp.MustCompile(`RELAY_CLI_[A-Z_]+`).FindAllString(src, -1) {
		if seen[v] {
			continue
		}
		seen[v] = true
		if !strings.Contains(docs, v) {
			t.Errorf("the code reads %s, but neither docs/configuration.md nor docs/runtimes.md names it", v)
		}
		if !strings.Contains(helpText, v) {
			t.Errorf("the code reads %s, but the manual (helpText) does not name it", v)
		}
	}
}

// The breaker thresholds and the fixed bounds are numbers a user plans around.
// Each is quoted beside the words that identify it, in the reference and in
// the manual, so a changed constant fails here rather than in a support thread.
func TestDocsQuoteTheBreakerThresholds(t *testing.T) {
	docs := readRepoFile(t, "docs/configuration.md")
	cooldown := fmt.Sprintf("%ds", int(relaunchCooldown.Seconds()))
	for _, want := range []struct{ where, text string }{
		{"docs/configuration.md", fmt.Sprintf("| Probe breaker | %d consecutive", maxProbeFailures)},
		{"docs/configuration.md", fmt.Sprintf("| Budget breaker | %d consecutive", maxBudgetKills)},
		{"docs/configuration.md", fmt.Sprintf("| Attention-stall breaker | %d consecutive", maxAttentionStall)},
		{"docs/configuration.md", "| relaunch cooldown | " + cooldown + ", fixed |"},
		{"docs/configuration.md", fmt.Sprintf("At least `%d` when set", minSecondsPerRun)},
		{"docs/configuration.md", fmt.Sprintf("Minimum `%g`", minPollSeconds)},
		{"helpText", fmt.Sprintf("fleet-wide, minimum %g", minPollSeconds)},
		{"helpText", fmt.Sprintf("after %d consecutive probe failures", maxProbeFailures)},
		{"helpText", fmt.Sprintf("after %d spend", maxBudgetKills)},
		{"helpText", fmt.Sprintf("across %d consecutive", maxAttentionStall)},
		{"helpText", "relaunch cooldown     " + cooldown + ", fixed"},
	} {
		body := helpText
		if want.where != "helpText" {
			body = docs
		}
		if !strings.Contains(body, want.text) {
			t.Errorf("%s does not say %q, so it quotes a threshold the code no longer has", want.where, want.text)
		}
	}
}

// The dashboard's worker states are listed in docs/cli.md so a reader can tell
// "ceiling" from "cooldown". Every state the cards can show is on that list.
func TestDocsListEveryWorkerState(t *testing.T) {
	doc := readRepoFile(t, "docs/cli.md")
	for _, state := range []string{StateIdle, StatePolling, StateRunning, StateCooldown, StateCeiling, StatePaused, StateProbeErr} {
		shown := strings.ReplaceAll(state, "_", " ") // the cards print the state with a space
		if !strings.Contains(doc, state) && !strings.Contains(doc, shown) {
			t.Errorf("worker state %q is not in the state list in docs/cli.md", state)
		}
	}
}

// Every user page is on the map in three places: the readme's Documentation
// table, the page-shape table in AGENTS.md, and the ceilings. A page missing
// from any of them is one a reader cannot find or one that can grow unchecked.
func TestDocsMapCoversEveryPage(t *testing.T) {
	readme := readRepoFile(t, "readme.md")
	agents := readRepoFile(t, "AGENTS.md")

	pages, err := filepath.Glob(filepath.Join(repoRoot, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		rel := "docs/" + filepath.Base(p)
		if !strings.Contains(readme, "]("+rel+")") {
			t.Errorf("%s is not linked from the Documentation table in readme.md", rel)
		}
		if !strings.Contains(agents, "`"+rel+"`") {
			t.Errorf("%s has no row in the page tables in AGENTS.md", rel)
		}
		if _, ok := userPageCeilings[rel]; !ok {
			t.Errorf("%s has no word ceiling in userPageCeilings (docs_pages_test.go)", rel)
		}
	}
	contrib, err := filepath.Glob(filepath.Join(repoRoot, "docs", "contributing", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range contrib {
		rel := "docs/contributing/" + filepath.Base(p)
		if !strings.Contains(agents, "]("+rel+")") {
			t.Errorf("%s is not linked from AGENTS.md", rel)
		}
	}
	for page := range userPageCeilings {
		if _, err := os.Stat(filepath.Join(repoRoot, page)); err != nil {
			t.Errorf("userPageCeilings names %s, which does not exist", page)
		}
	}
}

var (
	rationaleRe   = regexp.MustCompile(`(?i)\b(deliberately|on purpose|not incidental|no longer|previously)\b`)
	fenceRe       = regexp.MustCompile("(?s)```.*?```")
	headingLineRe = regexp.MustCompile(`(?m)^#{1,6} .*$`)
)

// The style rules in AGENTS.md that a machine can hold: a user page states
// what a thing is, not why (so no rationale words); a paragraph carries at most
// one em-dash; and a heading carries none, because the anchor GitHub derives
// would keep a double hyphen where the dash was.
func TestDocsUserPagesFollowTheStyleRules(t *testing.T) {
	texts := map[string]string{"helpText": helpText, "shortHelp": shortHelp}
	for _, page := range userPages() {
		texts[page] = readRepoFile(t, page)
	}
	for name, body := range texts {
		for _, m := range rationaleRe.FindAllString(body, -1) {
			t.Errorf("%s says %q. A user page states what a thing is; the reason goes in docs/contributing/.", name, m)
		}
	}

	files := markdownFiles(t)
	for _, file := range files {
		body := mustRead(t, file)
		for _, h := range headingLineRe.FindAllString(body, -1) {
			if strings.Contains(h, "—") {
				t.Errorf("%s: heading %q has an em-dash, which leaves a double hyphen in its anchor", rel(file), h)
			}
		}
	}

	for _, page := range userPages() {
		prose := fenceRe.ReplaceAllString(readRepoFile(t, page), "")
		for _, para := range strings.Split(prose, "\n\n") {
			if strings.HasPrefix(strings.TrimSpace(para), "|") {
				continue // a table row is not a paragraph
			}
			if n := strings.Count(para, "—"); n > 1 {
				t.Errorf("%s has a paragraph with %d em-dashes; the ceiling is one:\n%s", page, n, strings.TrimSpace(para))
			}
		}
	}
}

// CHANGELOG.md is where a user learns what changed. It has an Unreleased
// section for the next batch, and a heading for the release the docs quote.
func TestDocsChangelogIsCurrent(t *testing.T) {
	log := readRepoFile(t, "CHANGELOG.md")
	if !strings.Contains(log, "## Unreleased") {
		t.Error("CHANGELOG.md has no '## Unreleased' section; the next release has nowhere to be described")
	}
	seen := map[string]bool{}
	for _, page := range userPages() {
		for _, m := range sampleVersionRe.FindAllStringSubmatch(readRepoFile(t, page), -1) {
			seen[m[1]] = true
		}
	}
	for v := range seen {
		if !strings.Contains(log, "## v"+v) {
			t.Errorf("the docs quote relay %s, but CHANGELOG.md has no '## v%s' heading", v, v)
		}
	}
}
