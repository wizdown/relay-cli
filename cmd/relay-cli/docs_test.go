package main

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The self-update loop for .worker-config, made executable.
//
// A worker field lives in four places: this package (the struct, its default and
// its validation), .worker-config.example, docs/configuration.md, and the
// manual in helpText. A checklist asking someone to remember all four is a
// checklist that gets skipped — so these tests reflect over the struct and fail
// the build when a field, a default or a removed key is undocumented.
//
// If one of these fails, the fix is to update the document it names. Do not
// relax the test: it is the only thing standing between "we added a field" and
// "nobody can find out it exists".

const (
	exampleConfigPath = "../../.worker-config.example"
	configDocsPath    = "../../docs/configuration.md"
)

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v\n"+
			"This test guards the documentation of every .worker-config field. "+
			"If the file moved, update the path constant in docs_test.go rather "+
			"than deleting the check.", path, err)
	}
	return string(b)
}

// workerFields is every field a .worker-config entry may set, taken from the
// struct rather than from a list someone has to maintain alongside it.
func workerFields() []string {
	var out []string
	rt := reflect.TypeOf(Worker{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		// "-" is Endpoint, which is deliberately never serialized: it carries the
		// secret. It is documented as mcp_endpoint, which the redacted field
		// already contributes.
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func TestEveryConfigFieldIsDocumented(t *testing.T) {
	example := mustRead(t, exampleConfigPath)
	docs := mustRead(t, configDocsPath)

	surfaces := []struct {
		what string
		body string
		fix  string
	}{
		{".worker-config.example", example, "annotate the field where a reader will be editing it"},
		{"docs/configuration.md", docs, "add a row to the required or optional field table"},
		{"the manual (helpText in main.go)", helpText, "add it to the THE CONFIG FILE block"},
		// The template `relay-cli init` writes is the only reference a user
		// with just the binary ever sees, so an undocumented field there is
		// invisible to exactly the people who cannot look it up.
		{"the init template (init.go)", initConfigTemplate, "add the field, annotated, to initConfigTemplate"},
	}

	for _, field := range workerFields() {
		for _, s := range surfaces {
			if !strings.Contains(s.body, field) {
				t.Errorf("worker field %q is not documented in %s — %s", field, s.what, s.fix)
			}
		}
	}
}

// A default is what someone sets their spend ceiling against, so the number in
// the docs disagreeing with the number in the code is worse than no docs.
// helpText is covered by TestHelpQuotesTheRealDefaults; this is the field table.
func TestConfigDocsQuoteTheRealDefaults(t *testing.T) {
	docs := mustRead(t, configDocsPath)

	for _, tc := range []struct{ field, want string }{
		{"runtime", "claude"},
		{"poll_frequency_seconds", fmt.Sprintf("%g", defaultPollSeconds)},
		{"max_runs_per_hour", fmt.Sprint(defaultMaxRunsPerHour)},
		{"max_budget_usd", fmt.Sprintf("%g", defaultMaxBudgetUSD)},
		{"run_timeout_seconds", fmt.Sprint(defaultRunTimeoutSecs)},
	} {
		got, ok := docsTableDefault(docs, tc.field)
		if !ok {
			t.Errorf("no optional-field row for %q in docs/configuration.md.\n"+
				"Rows are matched as: | `field` | `default` | …", tc.field)
			continue
		}
		if got != tc.want {
			t.Errorf("docs/configuration.md says %q defaults to %q, the code says %q",
				tc.field, got, tc.want)
		}
	}
}

// docsTableDefault reads the second column of the markdown row for a field:
//
//	| `poll_frequency_seconds` | `30` | Seconds the worker sleeps … |
func docsTableDefault(docs, field string) (string, bool) {
	re := regexp.MustCompile("(?m)^\\|\\s*`" + regexp.QuoteMeta(field) + "`\\s*\\|\\s*`?([^`|]*)`?\\s*\\|")
	m := re.FindStringSubmatch(docs)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// A removed key is rejected by name with its replacement, which is the whole
// reason it is worth listing: a config still carrying one would otherwise change
// what the worker does. Someone hitting that error should be able to search the
// docs for the key and find out what happened to it.
func TestEveryRemovedKeyIsDocumented(t *testing.T) {
	docs := mustRead(t, configDocsPath)
	for key, replacement := range removedKeys {
		if !strings.Contains(docs, key) {
			t.Errorf("removed key %q is rejected by the parser but not mentioned in "+
				"docs/configuration.md — add it to the removed-keys list", key)
		}
		if strings.TrimSpace(replacement) == "" {
			t.Errorf("removed key %q has no replacement text; the error it produces "+
				"would tell someone what is wrong but not what to do", key)
		}
	}
}

// The example is the file people copy. A field that exists but is not shown in a
// working worker is a field found only by reading Go.
func TestExampleShowsEveryOptionalField(t *testing.T) {
	example := mustRead(t, exampleConfigPath)
	// Inside the JSON, not merely mentioned in a comment.
	for _, field := range workerFields() {
		if !strings.Contains(example, `"`+field+`"`) {
			t.Errorf("worker field %q never appears as a JSON key in "+
				".worker-config.example — show it on one of the example workers", field)
		}
	}
}

// Same rule for the generated config, and it matters more: this one is written
// onto the machine of someone who has no checkout to read.
func TestInitTemplateSetsEveryField(t *testing.T) {
	for _, field := range workerFields() {
		if !strings.Contains(initConfigTemplate, `"`+field+`"`) {
			t.Errorf("worker field %q is never set in the config `relay-cli init` "+
				"writes — a field a standalone user cannot see is a field they cannot use", field)
		}
	}
}
