package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A help text offering everything this adapter uses.
const fullClaudeHelp = `
  -p, --print                       non-interactive
      --output-format <format>      "text", "json", or "stream-json"
      --verbose
      --append-system-prompt <s>
      --mcp-config <path>
      --strict-mcp-config
      --allowedTools <list>
      --permission-mode <mode>
      --max-budget-usd <n>
      --model <model>
      --name <name>
`

func TestNoMissingFlagsOnACompleteCLI(t *testing.T) {
	if got := missingClaudeFlags([]byte(fullClaudeHelp)); len(got) != 0 {
		t.Fatalf("a complete CLI must pass, got:\n%s", strings.Join(got, "\n"))
	}
}

func TestMissingFlagsAreNamedWithTheirReason(t *testing.T) {
	help := strings.ReplaceAll(fullClaudeHelp, "--max-budget-usd <n>", "")
	help = strings.ReplaceAll(help, "--strict-mcp-config", "")
	got := strings.Join(missingClaudeFlags([]byte(help)), "\n")
	for _, want := range []string{"--max-budget-usd", "spend cap", "--strict-mcp-config"} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--mcp-config ") {
		t.Errorf("--mcp-config is present and must not be reported:\n%s", got)
	}
}

// A CLI can have --output-format and still not offer stream-json, which would
// pass a flag-only check and then produce no live feed at all.
func TestOutputFormatWithoutStreamJSONIsCaught(t *testing.T) {
	help := strings.ReplaceAll(fullClaudeHelp, `"text", "json", or "stream-json"`, `"text" or "json"`)
	got := strings.Join(missingClaudeFlags([]byte(help)), "\n")
	if !strings.Contains(got, "stream-json") {
		t.Fatalf("a CLI without stream-json must be rejected:\n%s", got)
	}
}

// Every flag gated on must be a flag actually used, and vice versa. Otherwise
// the check blocks installs over a flag nobody needs, or waves through one that
// breaks every run.
func TestGatedFlagsMatchTheFlagsActuallyUsed(t *testing.T) {
	dir := t.TempDir()
	// Model and budget set, so every conditional flag is emitted too.
	rc := &RunContext{
		Worker: &Worker{Name: "w", Endpoint: "https://r.example/c/wzh_aaaaaaaaaa",
			RuntimeConfig: map[string]string{"model": "opus", "max_usd_per_run": "5"}},
		WorkerDir: dir, RepoDir: dir, AllowTools: relayAllowedTools,
	}
	argv, err := (&claudeRuntime{}).BuildCmd(rc)
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, a := range argv {
		if strings.HasPrefix(a, "-") {
			used[a] = true
		}
	}
	// The CLI's own short form; --help lists it as "-p, --print".
	aliases := map[string]string{"--print": "-p"}
	// Emitted only when a worker pins a model, so a CLI without it is still
	// perfectly usable by every worker that does not.
	exempt := map[string]bool{"--model": true}

	for _, r := range requiredClaudeFlags {
		if !used[r.flag] && !used[aliases[r.flag]] {
			t.Errorf("gating on %s, but BuildCmd never passes it", r.flag)
		}
	}
	gated := map[string]bool{}
	for _, r := range requiredClaudeFlags {
		gated[r.flag] = true
		if a, ok := aliases[r.flag]; ok {
			gated[a] = true
		}
	}
	for flag := range used {
		if !gated[flag] && !exempt[flag] {
			t.Errorf("BuildCmd passes %s but nothing verifies the CLI supports it", flag)
		}
	}
}

// relay-cli bundles no CLI, and the error has to say so — this is the exact
// misunderstanding the wording is meant to prevent.
func TestMissingCLIFailsWithAnInstallHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := (&claudeRuntime{}).Check()
	if err == nil {
		t.Fatal("a missing claude must fail the check")
	}
	for _, want := range []string{"not found on PATH", "claude.com/claude-code", "does not bundle"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func fakeCLI(t *testing.T, help string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/usr/bin/env bash\ncase \"$1\" in\n  --version) echo '9.9.9 (Claude Code)' ;;\n  --help) cat <<'H'\n" + help + "\nH\n  ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCheckPassesAndCapturesVersion(t *testing.T) {
	t.Setenv("PATH", fakeCLI(t, fullClaudeHelp)+":/usr/bin:/bin")
	rt := &claudeRuntime{}
	if err := rt.Check(); err != nil {
		t.Fatalf("a complete CLI must pass: %v", err)
	}
	if rt.Version() != "9.9.9 (Claude Code)" {
		t.Errorf("version = %q", rt.Version())
	}
	if rt.Path() == "" {
		t.Error("path should record which claude was resolved")
	}
}

func TestOldCLIIsRejectedBeforeAnythingLaunches(t *testing.T) {
	t.Setenv("PATH", fakeCLI(t, "  -p, --print\n      --output-format <format>  \"text\" or \"json\"\n")+":/usr/bin:/bin")
	err := (&claudeRuntime{}).Check()
	if err == nil {
		t.Fatal("a CLI without the required flags must not start a fleet")
	}
	if !strings.Contains(err.Error(), "RELAY_CLI_SKIP_RUNTIME_CHECK") {
		t.Errorf("the error should offer its own bypass: %v", err)
	}
}

// The bypass exists because this gate parses help text, which can change shape.
// It must skip the flag check without skipping the does-it-exist check.
func TestBypassSkipsFlagCheckButNotExistence(t *testing.T) {
	t.Setenv("RELAY_CLI_SKIP_RUNTIME_CHECK", "1")

	t.Setenv("PATH", fakeCLI(t, "  -p, --print\n")+":/usr/bin:/bin")
	if err := (&claudeRuntime{}).Check(); err != nil {
		t.Errorf("the bypass should allow an unverified CLI through: %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	if err := (&claudeRuntime{}).Check(); err == nil {
		t.Error("the bypass must not suppress a missing CLI")
	}
}

// stubCLI puts a fake CLI of the given name on PATH, so a sign-in check can be
// exercised on a machine that has neither CLI installed — and without touching
// the real one's credentials on a machine that does.
func stubCLI(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// The parse above is written from the real output shape, and a stub can only
// prove it reads the stub. This is the drift check: where a claude IS installed,
// its answer must still be one this adapter can read. Skipped rather than failed
// where there is none, because a fresh clone has to pass with no CLI at all.
func TestClaudeSignInCheckStillReadsTheInstalledCLI(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude is not installed here")
	}
	out, err := exec.Command("claude", "auth", "status", "--json").CombinedOutput()
	if err != nil {
		t.Skipf("this build cannot be asked: %v", err)
	}
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal(jsonObject(out), &status) != nil || status.LoggedIn == nil {
		t.Errorf("the installed claude no longer answers in a shape this adapter reads, "+
			"so every fleet gets the cannot-tell warning instead of a check:\n%s", out)
	}
}

// noEnvCredentials makes a sign-in test hermetic. Both checks stand down when
// the environment carries a key, so a test asserting a refusal would pass or
// fail depending on whose machine ran it.
func noEnvCredentials(t *testing.T) {
	t.Helper()
	for _, n := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
		"CODEX_API_KEY", "OPENAI_API_KEY",
	} {
		t.Setenv(n, "")
	}
}

// A signed-out claude fails the start, rather than launching workers that each
// fail in the same second, once a cycle, in a log nobody is watching.
func TestClaudeLoginErrorNamesTheFix(t *testing.T) {
	noEnvCredentials(t)
	stubCLI(t, "claude", `echo '{"loggedIn":false}'`)

	err := claudeLoginError()
	if err == nil {
		t.Fatal("a signed-out claude must fail the check")
	}
	for _, want := range []string{"not signed in", "claude auth login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestClaudeLoginPassesWhenSignedIn(t *testing.T) {
	// The real CLI prints more than this, so the parse has to read the field it
	// needs rather than the whole shape.
	stubCLI(t, "claude", `echo '{"loggedIn":true,"authMethod":"oauth_token","apiProvider":"firstParty"}'`)
	if err := claudeLoginError(); err != nil {
		t.Errorf("a signed-in CLI must pass: %v", err)
	}
}

// captureWarnings redirects the two sign-in warnings so a test can assert on
// them. A warning nobody checks is a warning that silently stops being printed.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := warnOut
	warnOut = &buf
	t.Cleanup(func() { warnOut = prev })
	return &buf
}

// A key in the environment authenticates every run whatever the cached sign-in
// says. Refusing to start a fleet that would have worked is worse than not
// checking, so the check stands down rather than overruling it — and says so,
// because what relay-cli knows is that the variable is SET, not that it is
// valid. A stale one left over from something else, on a machine whose CLI is
// genuinely signed out, is the case this line exists for.
func TestSignInChecksStandDownLoudlyForAnEnvCredential(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		warnings := captureWarnings(t)
		stubCLI(t, "claude", `echo '{"loggedIn":false}'`)
		t.Setenv("ANTHROPIC_API_KEY", "sk-test")

		if err := claudeLoginError(); err != nil {
			t.Fatalf("an API key in the environment is a working credential: %v", err)
		}
		for _, want := range []string{"NOT signed in", "ANTHROPIC_API_KEY", "claude auth login"} {
			if !strings.Contains(warnings.String(), want) {
				t.Errorf("the stand-down must name %q:\n%s", want, warnings)
			}
		}
	})
	t.Run("codex", func(t *testing.T) {
		// codex says outright that a CODEX_API_KEY never becomes a cached login,
		// so its own status reports signed out while every run works.
		warnings := captureWarnings(t)
		stubCLI(t, "codex", `echo 'Not logged in'; exit 1`)
		t.Setenv("CODEX_API_KEY", "sk-test")

		if err := codexLoginError(); err != nil {
			t.Fatalf("an API key in the environment is a working credential: %v", err)
		}
		for _, want := range []string{"NOT signed in", "CODEX_API_KEY", "codex login"} {
			if !strings.Contains(warnings.String(), want) {
				t.Errorf("the stand-down must name %q:\n%s", want, warnings)
			}
		}
	})
}

// A signed-in CLI is the ordinary case and has nothing to say about it. A
// warning printed on every healthy start is one nobody reads on the start that
// matters.
func TestASignedInCLIWarnsAboutNothing(t *testing.T) {
	warnings := captureWarnings(t)
	stubCLI(t, "claude", `echo '{"loggedIn":true,"authMethod":"oauth_token"}'`)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	if err := claudeLoginError(); err != nil {
		t.Fatalf("a signed-in CLI must pass: %v", err)
	}
	if warnings.Len() > 0 {
		t.Errorf("nothing to warn about here:\n%s", warnings)
	}
}

// An older CLI with no way to answer must not fail the start: unverifiable is
// not the same as signed out.
func TestSignInChecksContinueWhenTheCLICannotAnswer(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		stubCLI(t, "claude", `echo "error: unknown command 'auth'" >&2; exit 1`)
		if err := claudeLoginError(); err != nil {
			t.Errorf("a CLI that cannot be asked must not fail the start: %v", err)
		}
	})
	t.Run("codex missing entirely", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if err := codexLoginError(); err != nil {
			t.Errorf("this check answers sign-in, not installation: %v", err)
		}
	})
}

// The point of every check in this file: a runtime that is not usable stops the
// whole start, before a worker is launched. LoadConfig reports it separately
// from problems in the file, because a correct config and an unusable CLI are
// different jobs.
func TestAnUnusableRuntimeStopsTheStart(t *testing.T) {
	prev := checkRuntime
	checkRuntime = func(name, runtime, relayDir string) error {
		return fmt.Errorf("worker %q cannot run: runtime %q is unusable.\n       the installed %s is not signed in.", name, runtime, runtime)
	}
	t.Cleanup(func() { checkRuntime = prev })

	_, err := LoadConfig(write(t, configOf(t, worker(t, ""))))
	if err == nil {
		t.Fatal("a signed-out runtime must stop the start, not launch workers that each fail")
	}
	for _, want := range []string{"the config is valid, but a runtime it names is not usable here", "not signed in"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should say %q:\n%v", want, err)
		}
	}
}

// Both compiled-in runtimes resolve without touching the disk: resolving is
// pure, and the tests that parse configs have to run where no CLI is installed.
func TestBothRuntimesResolve(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		rt, err := ResolveRuntime(name, t.TempDir())
		if err != nil {
			t.Fatalf("%s should resolve: %v", name, err)
		}
		if rt.Name() != name {
			t.Errorf("resolved %q to the %q adapter", name, rt.Name())
		}
	}
}

// Any other name is refused, and the error names the runtimes that do work
// rather than only saying what does not.
func TestUnknownRuntimeNamesTheSupportedOnes(t *testing.T) {
	_, err := ResolveRuntime("aider", t.TempDir())
	if err == nil {
		t.Fatal("only the compiled-in adapters resolve while bash adapters are gated")
	}
	for _, want := range []string{"claude", "codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s: %v", want, err)
		}
	}
}

// The bash-adapter path is shipped disabled, not deleted — a CLI nobody has
// written an adapter for is the reason it is kept. Unreachable code rots
// silently, so this exercises it
// directly: it has to still build an argv the day bashAdaptersEnabled flips.
func TestBashAdapterStillBuildsACommand(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "mycli.sh")
	adapter := "runtime_check() { :; }\n" +
		"runtime_build_cmd() { RUNTIME_CMD=(mycli --prompt \"$WORKER_PROMPT\" --dir \"$REPO_DIR\"); }\n"
	if err := os.WriteFile(script, []byte(adapter), 0o755); err != nil {
		t.Fatal(err)
	}

	rt := &bashRuntime{name: "mycli", script: script}
	if err := rt.Check(); err != nil {
		t.Fatalf("runtime_check should pass: %v", err)
	}
	argv, err := rt.BuildCmd(&RunContext{
		Worker:  &Worker{Name: "w"},
		Prompt:  "do one task",
		RepoDir: "/tmp/repo",
	})
	if err != nil {
		t.Fatalf("BuildCmd: %v", err)
	}
	want := []string{"mycli", "--prompt", "do one task", "--dir", "/tmp/repo"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}

// The rules override is silent by design when absent — but silently discarding
// an edited copy left in the old location is the one case that needs a word.
func TestLoadWorkerRulesWarnsAboutTheOldLocation(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "internal")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "CUSTOM RULES THAT MUST NOT SILENTLY VANISH"
	if err := os.WriteFile(filepath.Join(legacy, "worker-rules.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	rules, _ := loadWorkerRules(root)
	if strings.Contains(rules, custom) {
		t.Error("the old location must not still be read")
	}
	if rules != embeddedRules {
		t.Error("with no rules beside the config, the embedded copy should be used")
	}
}

// The current location is read, and wins over the embedded copy.
func TestLoadWorkerRulesReadsBesideTheConfig(t *testing.T) {
	root := t.TempDir()
	custom := "ONE TASK PER SESSION, AND NOTHING ELSE"
	if err := os.WriteFile(filepath.Join(root, "worker-rules.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, path := loadWorkerRules(root)
	if rules != custom {
		t.Errorf("worker-rules.md beside the config should win, got %q", rules)
	}
	if filepath.Dir(path) != root {
		t.Errorf("the rules path should be the on-disk one, got %q", path)
	}
}
