package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The whole point of init is that what it writes actually works. A template
// that drifts out of the parser's grammar would hand every new user a file that
// fails on their first check, which is the worst possible first five minutes.
func TestInitWritesAConfigThatValidates(t *testing.T) {
	noRuntimeCheck(t)
	dir := t.TempDir()
	path := filepath.Join(dir, ".worker-config")

	var out bytes.Buffer
	if err := initConfig(path, &out); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("the config init generates does not survive the validator: %v", err)
	}
	if len(cfg.Workers) != 1 {
		t.Fatalf("want exactly one worker to start from, got %d", len(cfg.Workers))
	}
	if w := cfg.Workers[0]; w.Runtime != "claude" {
		t.Errorf("the generated worker should use the supported runtime, got %q", w.Runtime)
	}
}

// The comments are the documentation for someone who has only the binary, so
// "it parses" is not enough — it has to still be annotated after stripping.
func TestInitTemplateIsAnnotated(t *testing.T) {
	if !strings.Contains(initConfigTemplate, "//") {
		t.Fatal("the generated config has no comments; it is the only reference a " +
			"user with just the binary has")
	}
	// Comment-stripping is what makes annotation possible, so prove it on this
	// exact text rather than trusting the general case.
	stripped := string(stripLineComments([]byte(initConfigTemplate)))
	if strings.Contains(stripped, "NEVER COMMIT") {
		t.Error("comments survived stripping")
	}
	if !strings.Contains(stripped, `"mcp_endpoint"`) {
		t.Error("stripping removed a JSON key — the URL in mcp_endpoint contains //")
	}
}

// Overwriting a .worker-config destroys connector secrets relay showed exactly
// once. There is deliberately no --force.
func TestInitRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".worker-config")
	const existing = `{"relay_workers":[{"name":"mine","mcp_endpoint":"https://r.example/relay/mcp/c/wzh_secretvalue"}]}`
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := initConfig(path, &out)
	if err == nil {
		t.Fatal("init must not overwrite an existing config")
	}
	if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "--config") {
		t.Errorf("the refusal should say why and offer a way through, got: %v", err)
	}

	b, _ := os.ReadFile(path)
	if string(b) != existing {
		t.Fatal("the existing config was modified")
	}
}

// The file is about to hold a credential. Creating it world-readable and hoping
// the user tightens it later is the wrong default.
func TestInitWritesPrivateMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".worker-config")
	var out bytes.Buffer
	if err := initConfig(path, &out); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("generated config mode = %04o, want 0600 — it will hold a credential", perm)
	}
}

// Someone who ran init has no readme to turn to, so the output has to carry the
// next steps and the warning.
func TestInitOutputExplainsWhatToDoNext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".worker-config")
	var out bytes.Buffer
	if err := initConfig(path, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		dir,                      // where the file landed, absolute
		"issue_agent_credential", // how to get a credential
		"relay-cli check",        // verify before spending
		"relay-cli run",          // then start
		"NEVER COMMIT",           // the one irreversible mistake
	} {
		if !strings.Contains(got, want) {
			t.Errorf("init output does not mention %q:\n%s", want, got)
		}
	}
}

// The placeholder must be obviously a placeholder — and must not look like a
// real secret to the repo's own credential scanners.
func TestInitTemplateUsesAnObviousPlaceholder(t *testing.T) {
	if !strings.Contains(initConfigTemplate, "wzh_REPLACE_ME") {
		t.Error("the endpoint placeholder should be wzh_REPLACE_ME: it reads as a " +
			"placeholder, and the pre-commit hook and CI scanner allow it by name")
	}
	if !strings.Contains(initConfigTemplate, "relay.example.com") {
		t.Error("the placeholder host should be relay.example.com")
	}
}

// The workspace is the reason init makes a directory rather than a file: the
// generated worker has to have somewhere to run that is not the directory
// holding every worker's credentials.
func TestInitCreatesTheWorkspaceBesideTheConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), homeDirName)

	var out bytes.Buffer
	if err := initConfig(dir, &out); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		filepath.Join(dir, defaultConfigName),
		filepath.Join(dir, workspaceDirName),
		filepath.Join(dir, ".gitignore"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("init did not create %s: %v", p, err)
		}
	}

	// This directory is routinely created inside a repo, and one `git add -A`
	// would publish a live credential.
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); strings.TrimSpace(string(b)) != "*" {
		t.Errorf("the generated .gitignore should ignore everything here, got %q", b)
	}
}

// A repo_dir that is written but wrong is worse than one left commented out:
// validation would reject it, or the CLI would run somewhere unexpected.
func TestInitPointsRepoDirAtTheWorkspace(t *testing.T) {
	noRuntimeCheck(t)
	dir := filepath.Join(t.TempDir(), homeDirName)

	var out bytes.Buffer
	if err := initConfig(dir, &out); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(dir, defaultConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), workspacePlaceholder) {
		t.Fatalf("the workspace placeholder survived into the written config:\n%s", body)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("the generated config does not validate: %v", err)
	}
	if got, want := cfg.Workers[0].RepoDir, filepath.Join(dir, workspaceDirName); got != want {
		t.Errorf("repo_dir = %q, want the workspace init created, %q", got, want)
	}
}

// --config names a place, and both ways of saying it mean the same place.
func TestInitAcceptsAFilePathToo(t *testing.T) {
	dir := t.TempDir()

	var out bytes.Buffer
	if err := initConfig(filepath.Join(dir, defaultConfigName), &out); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, workspaceDirName)); err != nil {
		t.Errorf("the workspace should be created beside the config: %v", err)
	}
}
