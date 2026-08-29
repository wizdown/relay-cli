package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The stripper's one real hazard: relay_mcp is a URL, and "http://host"
// contains the comment marker. Truncating there would turn a credential into
// "http:" and fail much later, somewhere far less obvious.
func TestStripLineCommentsKeepsURLs(t *testing.T) {
	in := `{
  // a comment
  "relay_mcp": "http://localhost:8080/relay/mcp/c/wzh_abc", // trailing
  "escaped": "a \" // not a comment",
  "runtime": "claude"
}`
	got := string(stripLineComments([]byte(in)))
	for _, want := range []string{
		`"http://localhost:8080/relay/mcp/c/wzh_abc"`,
		`"a \" // not a comment"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stripped output lost %s\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "// a comment") || strings.Contains(got, "// trailing") {
		t.Errorf("comments survived:\n%s", got)
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, configFileName)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func loadErr(p string) error { _, err := LoadConfig(p); return err }

// repoHere gives a real directory for repo_dir, which is required and must
// exist. Tests that are about some OTHER field should not have to think about
// it.
func repoHere(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// worker builds one valid worker entry, with the given lines merged in. Every
// test below is about one deviation from valid, and spelling out four required
// fields each time would bury which deviation is being tested.
func worker(t *testing.T, extra string) string {
	t.Helper()
	return fmt.Sprintf(`{
	  "name": "w",
	  "relay_mcp": "https://x/c/wzh_aaaaaaaa",
	  "repo_dir": %q,
	  "runtime": "claude",
	  "runtime_config": {"model": "sonnet"}
	  %s
	}`, repoHere(t), extra)
}

func configOf(t *testing.T, workers ...string) string {
	t.Helper()
	return `{"workers":[` + strings.Join(workers, ",") + `]}`
}

// noRuntimeCheck stubs out the "is this CLI installed?" half of validation for
// tests that are about parsing. Without it, `go test ./...` on a fresh clone
// would require Claude Code to be installed to test a JSON parser. The real
// check keeps its own coverage in TestRejectsUnknownRuntime and in
// runtime_claude_check_test.go, neither of which stubs it.
func noRuntimeCheck(t *testing.T) {
	t.Helper()
	prev := checkRuntime
	checkRuntime = func(name, runtime, relayDir string) error { return nil }
	t.Cleanup(func() { checkRuntime = prev })
}

func TestDefaults(t *testing.T) {
	noRuntimeCheck(t)
	cfg, err := LoadConfig(write(t, configOf(t, worker(t, ""))))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollSeconds != 30 {
		t.Errorf("poll_seconds = %v, want 30", cfg.PollSeconds)
	}
	w := cfg.Workers[0]
	if w.MaxRunsPerHour != 12 || w.MaxSecondsPerRun != 900 {
		t.Errorf("relay-cli defaults drifted: %+v", w)
	}
	// The optional runtime setting resolves to the runtime's own default, so
	// nothing downstream has to know a default exists.
	if got := w.RCFloat("max_usd_per_run"); got != 5 {
		t.Errorf("max_usd_per_run = %v, want 5", got)
	}
}

// "30" as a string is the classic version of this mistake, and would reach the
// loop as a broken interval.
func TestRejectsStringPollSeconds(t *testing.T) {
	p := write(t, `{"poll_seconds":"30","workers":[`+worker(t, "")+`]}`)
	if err := loadErr(p); err == nil || !strings.Contains(err.Error(), "must be a JSON number") {
		t.Fatalf("want a poll_seconds type error, got %v", err)
	}
}

// The floor exists to protect relay, not the worker, so it is enforced rather
// than advised: a config below it does not start at all. A misplaced decimal
// point is the realistic way someone arranges a flood by accident.
func TestRejectsPollSecondsBelowTheMinimum(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"poll_seconds":0.5,"workers":[`+worker(t, "")+`]}`)
	err := loadErr(p)
	if err == nil {
		t.Fatal("want an error for a poll_seconds below the minimum")
	}
	if !strings.Contains(err.Error(), "minimum") {
		t.Errorf("error does not name the floor:\n%v", err)
	}
}

// The floor itself is a legal value. Rejecting it too would make the documented
// minimum a number nobody can actually set.
func TestAcceptsPollSecondsAtTheMinimum(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, fmt.Sprintf(`{"poll_seconds":%g,"workers":[`+worker(t, "")+`]}`, minPollSeconds))
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("%gs is the floor and must be accepted: %v", minPollSeconds, err)
	}
	if cfg.PollSeconds != minPollSeconds {
		t.Errorf("PollSeconds = %v, want %v", cfg.PollSeconds, minPollSeconds)
	}
}

// A config still carrying system_prompt_file would otherwise launch an agent
// with no standing instructions at all and look fine doing it. The renamed keys
// matter for the same reason: an ignored "model" is a worker running something
// nobody chose.
func TestRejectsRemovedKeysByName(t *testing.T) {
	for key, hint := range map[string]string{
		"system_prompt_file":       "instructions_md",
		"min_run_interval_seconds": "60s relaunch cooldown",
		"permission_mode":          "fully autonomous",
		"runtime_args":             "runtime_config",
		"mcp_endpoint":             "relay_mcp",
		"model":                    "runtime_config",
		"max_budget_usd":           "max_usd_per_run",
		"run_timeout_seconds":      "max_seconds_per_run",
		"poll_frequency_seconds":   "poll_seconds",
	} {
		p := write(t, configOf(t, worker(t, `, "`+key+`": "v"`)))
		err := loadErr(p)
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), hint) {
			t.Errorf("%s: want a rejection naming the key and its replacement, got %v", key, err)
		}
	}
}

// Four fields are required because each is a decision relay-cli cannot make for
// anyone. The error has to name every one that is missing, not the first.
func TestRejectsMissingRequiredFields(t *testing.T) {
	err := loadErr(write(t, `{"workers":[{}]}`))
	if err == nil {
		t.Fatal("want an error for a worker with no fields at all")
	}
	for _, want := range []string{"name", "relay_mcp", "repo_dir", "runtime"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name the missing %q:\n%v", want, err)
		}
	}
}

// Reporting one problem per run turns a half-written config into a dozen
// edit-rerun rounds. This is the property that stops that, so it is asserted
// directly rather than left to follow from the code's shape.
func TestReportsEveryProblemAtOnce(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"workers":[
	  {"relay_mcp":"https://x/c/wzh_a","repo_dir":"/nope/not/here","runtime":"claude","runtime_config":{"model":"sonnet"}},
	  {"name":"b","repo_dir":"/nope/not/here","runtime":"claude","runtime_config":{}}
	]}`)
	err := loadErr(p)
	if err == nil {
		t.Fatal("want an error")
	}
	// Worker one is missing a name and has a bad repo; worker two is missing a
	// credential, has a bad repo, and is missing the model its runtime requires.
	for _, want := range []string{`worker #1`, `worker "b"`, "name", "relay_mcp", "not a directory", "model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the combined report is missing %q:\n%v", want, err)
		}
	}
}

func TestRejectsDuplicates(t *testing.T) {
	noRuntimeCheck(t)
	repo := repoHere(t)
	entry := func(name, secret string) string {
		return fmt.Sprintf(`{"name":%q,"relay_mcp":"https://x/c/%s","repo_dir":%q,"runtime":"claude","runtime_config":{"model":"sonnet"}}`,
			name, secret, repo)
	}

	err := loadErr(write(t, configOf(t, entry("w", "wzh_a"), entry("w", "wzh_b"))))
	if err == nil || !strings.Contains(err.Error(), "duplicate name") {
		t.Errorf("want a duplicate-name error, got %v", err)
	}

	// One credential per agent is relay's boundary; two workers sharing one
	// claim against each other as the same agent.
	err = loadErr(write(t, configOf(t, entry("a", "wzh_same"), entry("b", "wzh_same"))))
	if err == nil || !strings.Contains(err.Error(), "duplicate relay_mcp") {
		t.Errorf("want a duplicate-credential error, got %v", err)
	}
}

// A name becomes state/<name>/. The bash poller only asked this in a comment,
// and a worker called "a/b" silently wrote its state elsewhere.
func TestRejectsUnsafeName(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, configOf(t, strings.Replace(worker(t, ""), `"name": "w"`, `"name": "a/b"`, 1)))
	if err := loadErr(p); err == nil || !strings.Contains(err.Error(), "filesystem-safe") {
		t.Fatalf("want a name-safety error, got %v", err)
	}
}

func TestRejectsMissingRepoDir(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"workers":[{"name":"w","relay_mcp":"https://x/c/wzh_a","repo_dir":"/nope/definitely/not/here","runtime":"claude","runtime_config":{"model":"sonnet"}}]}`)
	if err := loadErr(p); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want a repo_dir error, got %v", err)
	}
}

// The placeholder is a step the user has not done yet, not a typo, and
// "/path/to/your/repo is not a directory" reads like the latter.
func TestRejectsRepoDirPlaceholderByName(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"workers":[{"name":"w","relay_mcp":"https://x/c/wzh_a","repo_dir":"`+repoDirPlaceholder+`","runtime":"claude","runtime_config":{"model":"sonnet"}}]}`)
	err := loadErr(p)
	if err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("want a placeholder error, got %v", err)
	}
	if !strings.Contains(err.Error(), "rewritten") {
		t.Errorf("the message should warn what pointing this somewhere costs:\n%v", err)
	}
}

// Both placeholders are rejected in the SAME pass, so a fresh config reports
// both at once. Reporting one, then the other on the next run, is exactly the
// grind the accumulating validator exists to avoid.
func TestRejectsBothInitPlaceholdersTogether(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"workers":[{"name":"w","relay_mcp":"https://relay.example.com/c/wzh_`+
		endpointPlaceholder+`","repo_dir":"`+repoDirPlaceholder+`","runtime":"claude",`+
		`"runtime_config":{"model":"sonnet"}}]}`)
	err := loadErr(p)
	if err == nil {
		t.Fatal("want an error for a config with both placeholders")
	}
	for _, want := range []string{"relay_mcp", "repo_dir", "2 fix"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the report is missing %q:\n%v", want, err)
		}
	}
	// The endpoint placeholder has to be caught here rather than by a probe:
	// spending a DNS timeout to report that relay.example.com does not resolve
	// tells the user nothing about what they actually have to do.
	if !strings.Contains(err.Error(), "issue_agent_credential") {
		t.Errorf("the error should say how to get a real credential:\n%v", err)
	}
}

// An unsupported runtime is refused at load, before anything launches, and the
// error names the worker as well as the runtime — a fleet config has several,
// and "unknown runtime" alone does not say which one to fix.
func TestRejectsUnknownRuntime(t *testing.T) {
	p := write(t, `{"workers":[{"name":"w","relay_mcp":"https://x/c/wzh_a","repo_dir":"`+repoHere(t)+`","runtime":"nope"}]}`)
	err := loadErr(p)
	if err == nil {
		t.Fatal("want an unknown-runtime error")
	}
	for _, want := range []string{`worker "w"`, "claude"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q, got %v", want, err)
		}
	}
}

// ── runtime_config ───────────────────────────────────────────────────────────

// model is required for claude on purpose: the CLI's own default moves between
// versions, so an unchanged config would quietly change what a worker costs.
func TestRejectsMissingRequiredRuntimeField(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"workers":[{"name":"w","relay_mcp":"https://x/c/wzh_a","repo_dir":"`+repoHere(t)+`","runtime":"claude"}]}`)
	err := loadErr(p)
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("want a missing-model error, got %v", err)
	}
}

// This block is the one place a setting is spelled in a CLI's own vocabulary,
// so an unknown key is either a typo or a setting meant for another runtime.
// Ignoring it silently means a fleet runs for a week without the cap someone
// thought they set.
func TestRejectsUnknownRuntimeConfigKey(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"workers":[{"name":"w","relay_mcp":"https://x/c/wzh_a","repo_dir":"`+repoHere(t)+`","runtime":"claude",
	  "runtime_config":{"model":"sonnet","max_budget_usd":5}}]}`)
	err := loadErr(p)
	if err == nil || !strings.Contains(err.Error(), "max_budget_usd") {
		t.Fatalf("want an unknown-key error, got %v", err)
	}
	// Naming what it DOES take turns a rejection into a correction.
	if !strings.Contains(err.Error(), "max_usd_per_run") {
		t.Errorf("the error should list the keys the runtime accepts:\n%v", err)
	}
}

func TestRejectsWrongTypedRuntimeConfigValue(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"workers":[{"name":"w","relay_mcp":"https://x/c/wzh_a","repo_dir":"`+repoHere(t)+`","runtime":"claude",
	  "runtime_config":{"model":"sonnet","max_usd_per_run":"5"}}]}`)
	if err := loadErr(p); err == nil || !strings.Contains(err.Error(), "max_usd_per_run") {
		t.Fatalf("want a type error for a string spend cap, got %v", err)
	}
}

// 0 is the operator deliberately removing the cap, which is not the same as
// leaving it unset — so it has to survive validation.
func TestAcceptsZeroSpendCap(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"workers":[{"name":"w","relay_mcp":"https://x/c/wzh_a","repo_dir":"`+repoHere(t)+`","runtime":"claude",
	  "runtime_config":{"model":"sonnet","max_usd_per_run":0}}]}`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("0 removes the cap deliberately and must be accepted: %v", err)
	}
	if got := cfg.Workers[0].RCFloat("max_usd_per_run"); got != 0 {
		t.Errorf("max_usd_per_run = %v, want 0", got)
	}
}

// ── discovery ────────────────────────────────────────────────────────────────

// What `init` writes has to survive the validator that `check` runs, comments
// and all — otherwise the first thing a new user does fails. Only the two
// placeholders are fictional, so they are filled in first, exactly as the
// printed instructions tell someone to.
func TestInitTemplateValidates(t *testing.T) {
	noRuntimeCheck(t)
	dir := filepath.Join(t.TempDir(), relayDirName)
	var out strings.Builder
	if err := initConfig(dir, &out); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(dir, configFileName))
	if err != nil {
		t.Fatal(err)
	}
	filled := strings.ReplaceAll(string(body), repoDirPlaceholder, repoHere(t))
	filled = strings.ReplaceAll(filled, "wzh_REPLACE_ME", "wzh_realsecret")

	if err := loadErr(write(t, filled)); err != nil {
		t.Fatalf("the config `relay-cli init` writes does not validate: %v", err)
	}
}

// "not found" has to point at the one command that fixes it.
func TestMissingConfigPointsAtInit(t *testing.T) {
	missing := filepath.Join(t.TempDir(), configFileName)
	err := loadErr(missing)
	if err == nil {
		t.Fatal("want an error when there is no config")
	}
	for _, want := range []string{missing, "relay-cli init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%v", want, err)
		}
	}
}

// There is exactly one location, and the rest of the tool derives its state and
// log directories from it. A change here relocates everyone's fleet, so it is
// pinned rather than assumed.
func TestRelayHomeIsUnderTheUsersHome(t *testing.T) {
	dir, err := RelayHome()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".relay"); dir != want {
		t.Errorf("RelayHome() = %q, want %q", dir, want)
	}
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "config"); path != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", path, want)
	}
}
