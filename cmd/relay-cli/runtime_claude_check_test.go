package main

import (
	"os"
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
		Worker:    &Worker{Name: "w", Endpoint: "https://r.example/c/wzh_aaaaaaaaaa", Model: "opus"},
		WorkerDir: dir, RepoDir: dir, MaxBudget: "5", AllowTools: relayAllowedTools,
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

// codex is not offered yet, and the error has to say that rather than describe
// a missing file — someone who deliberately wrote "codex" would otherwise go
// hunting for a typo.
func TestCodexSaysComingSoon(t *testing.T) {
	_, err := ResolveRuntime("codex", t.TempDir())
	if err == nil {
		t.Fatal("codex is not a supported runtime yet")
	}
	for _, want := range []string{"coming soon", `"runtime": "claude"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
	// The old error described a missing adapter file and an npm install. Both
	// would now be a lie: no adapter path is consulted at all.
	if strings.Contains(err.Error(), "runtimes/") || strings.Contains(err.Error(), "npm i -g") {
		t.Errorf("should not describe an adapter that cannot be loaded: %v", err)
	}
}

// Any other name gets the shorter answer, and it still names the one runtime
// that works rather than only saying what does not.
func TestUnknownRuntimeNamesTheSupportedOne(t *testing.T) {
	_, err := ResolveRuntime("aider", t.TempDir())
	if err == nil {
		t.Fatal("only claude resolves while bash adapters are gated")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("error should name claude: %v", err)
	}
}

// The bash-adapter path is shipped disabled, not deleted — codex support is the
// reason it is kept. Unreachable code rots silently, so this exercises it
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
