package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initHere runs init into a fresh ~/.relay-shaped directory and returns it.
func initHere(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), relayDirName)
	var out bytes.Buffer
	if err := initConfig(dir, &out); err != nil {
		t.Fatal(err)
	}
	return dir, out.String()
}

// The whole point of init is that what it writes actually works. A template
// that drifts out of the parser's grammar would hand every new user a file that
// fails on their first check, which is the worst possible first five minutes.
//
// The two placeholders are the exception: they are MEANT to fail until they are
// replaced, which TestInitTemplateValidates covers by replacing them.
func TestInitWritesAConfigThePlaceholdersAreTheOnlyProblemWith(t *testing.T) {
	noRuntimeCheck(t)
	dir, _ := initHere(t)

	err := loadErr(filepath.Join(dir, configFileName))
	if err == nil {
		t.Fatal("a freshly written config still has both placeholders and must not validate")
	}
	// Exactly the two decisions relay-cli cannot make for anyone, reported
	// together — a third complaint would mean the template itself is malformed.
	for _, want := range []string{"relay_mcp", "repo_dir", "placeholder", "2 fix"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should point at the placeholders, missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("the generated config does not parse:\n%v", err)
	}
}

// The comments are the documentation for someone who has only the binary, so
// "it parses" is not enough — it has to still be annotated after stripping.
func TestInitTemplateIsAnnotated(t *testing.T) {
	if !strings.Contains(initConfigTemplate, "//") {
		t.Fatal("the generated config has no comments; a user with just the binary " +
			"has nothing else in front of them")
	}
	// Comment-stripping is what makes annotation possible, so prove it on this
	// exact text rather than trusting the general case.
	stripped := string(stripLineComments([]byte(initConfigTemplate)))
	if strings.Contains(stripped, "NEVER COMMIT") {
		t.Error("comments survived stripping")
	}
	if !strings.Contains(stripped, `"relay_mcp"`) {
		t.Error("stripping removed a JSON key — the URL in relay_mcp contains //")
	}
}

// It is short on purpose. The reference is docs/configuration.md; a template
// that grows back into a second copy of the manual is the thing this replaced.
func TestInitTemplatePointsAtTheRealReference(t *testing.T) {
	if !strings.Contains(initConfigTemplate, "docs/configuration.md") {
		t.Error("the template should link the full field reference — it deliberately " +
			"does not repeat it")
	}
}

// Overwriting a config destroys connector secrets relay showed exactly once.
// There is deliberately no --force.
func TestInitRefusesToOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), relayDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, configFileName)
	const existing = `{"workers":[{"name":"mine","relay_mcp":"https://r.example/relay/mcp/c/wzh_secretvalue"}]}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := initConfig(dir, &out)
	if err == nil {
		t.Fatal("init must not overwrite an existing config")
	}
	if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "exactly once") {
		t.Errorf("the refusal should say what overwriting would cost, got: %v", err)
	}

	b, _ := os.ReadFile(path)
	if string(b) != existing {
		t.Fatal("the existing config was modified")
	}
}

// ~/.relay existing as a file is rare, and MkdirAll would report it as a bare
// "not a directory" from somewhere further in.
func TestInitRefusesWhenTheTargetIsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), relayDirName)
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := initConfig(path, &out)
	if err == nil {
		t.Fatal("init must refuse when its target exists as a file")
	}
	if !strings.Contains(err.Error(), "is a file") {
		t.Errorf("the error should say what is actually wrong, got: %v", err)
	}
}

// The file is about to hold a credential. Creating it world-readable and hoping
// the user tightens it later is the wrong default — and so is a directory
// anyone on the machine can list.
func TestInitWritesPrivateModes(t *testing.T) {
	dir, _ := initHere(t)

	info, err := os.Stat(filepath.Join(dir, configFileName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("generated config mode = %04o, want 0600 — it will hold a credential", perm)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("%s mode = %04o, want 0700 — it holds every worker's credential", dir, perm)
	}
}

// init no longer creates a workspace. An empty scratch directory was the one
// place an agent could do the least useful work, and it duplicated a fallback
// that wrote into a directory shutdown deletes.
func TestInitCreatesOnlyTheConfig(t *testing.T) {
	dir, _ := initHere(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != configFileName {
			t.Errorf("init created %q; it should write the config and nothing else — "+
				"state/ and logs/ are made when a fleet runs", e.Name())
		}
	}
}

// Someone who ran init has no readme in front of them, so the output has to
// carry both decisions, the next command, and the warning.
func TestInitOutputExplainsWhatToDoNext(t *testing.T) {
	dir, got := initHere(t)

	for _, want := range []string{
		dir,                      // where the file landed
		"relay_mcp",              // the first placeholder
		"repo_dir",               // the second
		"issue_agent_credential", // how to get a credential
		"relay check",            // verify before spending
		"relay run",              // then start
		"rewritten",              // what pointing repo_dir somewhere costs
		"NEVER COMMIT",           // the one irreversible mistake
	} {
		if !strings.Contains(got, want) {
			t.Errorf("init output does not mention %q:\n%s", want, got)
		}
	}
}

// Both placeholders must be obviously placeholders — and the endpoint one must
// not look like a real secret to the repo's own credential scanners.
func TestInitTemplateUsesObviousPlaceholders(t *testing.T) {
	if !strings.Contains(initConfigTemplate, "wzh_REPLACE_ME") {
		t.Error("the endpoint placeholder should be wzh_REPLACE_ME: it reads as a " +
			"placeholder, and the pre-commit hook and CI scanner allow it by name")
	}
	if !strings.Contains(initConfigTemplate, "relay.example.com") {
		t.Error("the placeholder host should be relay.example.com")
	}
	// Rejected by name in config.go, so the two have to stay in step.
	if !strings.Contains(initConfigTemplate, repoDirPlaceholder) {
		t.Errorf("the template should write %q into repo_dir — that exact string is "+
			"what the validator rejects by name", repoDirPlaceholder)
	}
}
