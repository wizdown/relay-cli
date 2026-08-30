package main

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The self-update loop for the config reference, made executable.
//
// A field used to live in four places — the struct here, the shipped example,
// docs/configuration.md and the manual in helpText — and a checklist asking
// someone to remember all four is a checklist that gets skipped. The example is
// gone and the init template is deliberately a starter rather than a reference,
// so two surfaces are left: the docs, which must be complete, and the manual,
// which must at least name relay-cli's own fields.
//
// If one of these fails, the fix is to update the document it names. Do not
// relax the test: it is the only thing standing between "we added a field" and
// "nobody can find out it exists".

const configDocsPath = "../../docs/configuration.md"

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v\n"+
			"This test guards the documentation of every config field. "+
			"If the file moved, update the path constant in docs_test.go rather "+
			"than deleting the check.", path, err)
	}
	return string(b)
}

// workerFields is every key a worker entry may set, taken from the struct
// rather than from a list someone has to maintain alongside it.
func workerFields() []string {
	var out []string
	rt := reflect.TypeOf(Worker{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		// "-" is Endpoint, which is deliberately never serialized: it carries
		// the secret. It is documented as relay_mcp, which the redacted field
		// already contributes.
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func TestEveryWorkerFieldIsDocumented(t *testing.T) {
	docs := mustRead(t, configDocsPath)

	for _, field := range workerFields() {
		if !strings.Contains(docs, "`"+field+"`") {
			t.Errorf("worker field %q has no row in docs/configuration.md — "+
				"add it to the worker field table", field)
		}
		if !strings.Contains(helpText, field) {
			t.Errorf("worker field %q is not in the manual (helpText in main.go) — "+
				"add it to the THE CONFIG FILE block", field)
		}
	}

	// poll_seconds is fleet-wide rather than per-worker, so the struct walk
	// above never reaches it — and a setting nobody can find is the one thing
	// this file exists to prevent.
	if !strings.Contains(docs, "`poll_seconds`") {
		t.Error("poll_seconds has no row in docs/configuration.md")
	}
}

// Every runtime declares what it accepts. The docs have to carry a table per
// runtime, or "which keys go in runtime_config?" is answerable only by reading
// Go — which is exactly the question the block was introduced to make askable.
func TestEveryRuntimeConfigFieldIsDocumented(t *testing.T) {
	docs := mustRead(t, configDocsPath)

	for _, rt := range supportedRuntimes() {
		if !strings.Contains(docs, rt.Name()) {
			t.Errorf("runtime %q has no section in docs/configuration.md", rt.Name())
		}
		for _, f := range rt.ConfigFields() {
			if !strings.Contains(docs, "`"+f.Key+"`") {
				t.Errorf("runtime %q accepts runtime_config.%s, but no row in "+
					"docs/configuration.md mentions it", rt.Name(), f.Key)
			}
			// The Doc line is what a missing required field tells the user, so
			// an empty one turns a helpful error into a bare key name.
			if strings.TrimSpace(f.Doc) == "" {
				t.Errorf("runtime %q declares %q with no Doc — that text is what "+
					"the config error prints", rt.Name(), f.Key)
			}
		}
	}
}

// A default is what someone sets their spend ceiling against, so the number in
// the docs disagreeing with the number in the code is worse than no docs.
func TestConfigDocsQuoteTheRealDefaults(t *testing.T) {
	docs := mustRead(t, configDocsPath)

	want := map[string]string{
		"poll_seconds":        fmt.Sprintf("%g", defaultPollSeconds),
		"max_runs_per_hour":   fmt.Sprint(defaultMaxRunsPerHour),
		"max_seconds_per_run": fmt.Sprint(defaultMaxSecondsPerRun),
	}
	// Runtime defaults are declared, not hardcoded here, so a runtime that
	// changes one cannot leave the docs behind.
	for _, rt := range supportedRuntimes() {
		for _, f := range rt.ConfigFields() {
			if f.Default != "" {
				want[f.Key] = f.Default
			}
		}
	}

	for field, def := range want {
		got, ok := docsTableDefault(docs, field)
		if !ok {
			t.Errorf("no field row for %q in docs/configuration.md.\n"+
				"Rows are matched as: | `field` | … | `default` |", field)
			continue
		}
		if got != def {
			t.Errorf("docs/configuration.md says %q defaults to %q, the code says %q",
				field, got, def)
		}
	}
}

// docsTableDefault reads the LAST backticked cell of the markdown row for a
// field, which is where the reference table keeps the default:
//
//	| `max_runs_per_hour` | no | Maximum CLI launches per rolling hour | `12` |
func docsTableDefault(docs, field string) (string, bool) {
	re := regexp.MustCompile("(?m)^\\|\\s*`" + regexp.QuoteMeta(field) + "`\\s*\\|.*$")
	row := re.FindString(docs)
	if row == "" {
		return "", false
	}
	cells := strings.Split(strings.Trim(row, "|"), "|")
	last := strings.TrimSpace(cells[len(cells)-1])
	return strings.Trim(last, "`"), true
}

// A removed key is rejected by name, and the error IS its documentation: the
// user docs describe what this version accepts, not how it got here, so the
// replacement text is the only place someone hitting one is told what to do.
// An empty one turns a fleet that refuses to start into a dead end.
func TestEveryRemovedKeyExplainsItself(t *testing.T) {
	for key, replacement := range removedKeys {
		if strings.TrimSpace(replacement) == "" {
			t.Errorf("removed key %q has no replacement text; the error it produces "+
				"would tell someone what is wrong but not what to do", key)
		}
	}
}

// The docs are the reference now that no annotated example ships, so the
// complete example in them has to be a config that actually loads.
func TestConfigDocsExampleValidates(t *testing.T) {
	noRuntimeCheck(t)
	docs := mustRead(t, configDocsPath)

	block := regexp.MustCompile("(?s)```jsonc\n(.*?)```").FindStringSubmatch(docs)
	if block == nil {
		t.Fatal("docs/configuration.md has no ```jsonc example block — the reference " +
			"should show one complete, working config")
	}
	// The example shows both placeholders, because that is what a reader copies
	// and replaces. Fill them in exactly as the reader would, then hold the
	// result to the validator.
	body := strings.ReplaceAll(block[1], repoDirPlaceholder, t.TempDir())
	body = strings.ReplaceAll(body, "~/code/wizhub", t.TempDir())
	body = strings.ReplaceAll(body, endpointPlaceholder, "filledin")

	if _, err := LoadConfig(write(t, body)); err != nil {
		t.Fatalf("the example in docs/configuration.md does not validate: %v", err)
	}
}
