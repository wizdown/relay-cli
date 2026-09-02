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
	withInstalledRuntimes(t, "claude", "codex")
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
		t.Fatalf("the config `relay init` writes does not validate: %v", err)
	}
}

// "not found" has to point at the one command that fixes it.
func TestMissingConfigPointsAtInit(t *testing.T) {
	missing := filepath.Join(t.TempDir(), configFileName)
	err := loadErr(missing)
	if err == nil {
		t.Fatal("want an error when there is no config")
	}
	for _, want := range []string{missing, "relay init"} {
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

// ── unknown keys ─────────────────────────────────────────────────────────────

// workerKeys is what the parser accepts; the struct is what the rest of the
// program reads. A field added to one and not the other is a setting the docs
// describe and the config refuses, so the two are pinned together rather than
// left to a checklist.
func TestWorkerKeysMatchTheStruct(t *testing.T) {
	accepted := map[string]bool{}
	for _, k := range workerKeys {
		accepted[k] = true
	}
	for _, field := range workerFields() {
		if !accepted[field] {
			t.Errorf("Worker has a %q field the config parser would reject — add it to workerKeys in config.go", field)
		}
		delete(accepted, field)
	}
	for k := range accepted {
		t.Errorf("workerKeys accepts %q, which no field on Worker reads — remove it, or add the field", k)
	}
}

// The whole point of the change: a key relay-cli does not read is a setting the
// operator believes is in force. `max_run_per_hour` used to load clean and cap
// nothing.
func TestRejectsUnknownWorkerKey(t *testing.T) {
	noRuntimeCheck(t)
	err := loadErr(write(t, configOf(t, worker(t, `, "max_run_per_hour": 6`))))
	if err == nil {
		t.Fatal("want an error for a misspelled worker key")
	}
	// Refusing is half an error message; naming the key it was meant to be is
	// the other half.
	for _, want := range []string{"max_run_per_hour", "did you mean", "max_runs_per_hour"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is missing %q:\n%v", want, err)
		}
	}
}

// Far enough from anything real that a guess would be noise. It still has to
// fail — it just fails without inventing a suggestion.
func TestRejectsUnknownWorkerKeyWithNoNearMiss(t *testing.T) {
	noRuntimeCheck(t)
	err := loadErr(write(t, configOf(t, worker(t, `, "totally_bogus": true`))))
	if err == nil || !strings.Contains(err.Error(), "totally_bogus") {
		t.Fatalf("want a rejection naming the key, got %v", err)
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("nothing is close to that key; the error should not guess:\n%v", err)
	}
}

// The misplacement the file's own shape invites: a runtime's setting written
// beside relay-cli's fields, where it reads perfectly and is read by nothing.
func TestSaysWhereAMisplacedRuntimeSettingBelongs(t *testing.T) {
	noRuntimeCheck(t)
	err := loadErr(write(t, configOf(t, worker(t, `, "max_usd_per_run": 5`))))
	if err == nil || !strings.Contains(err.Error(), "runtime_config") {
		t.Fatalf("want an error telling the user to move it into runtime_config, got %v", err)
	}
}

// One poll rate for the fleet, so a per-worker one is not a smaller mistake
// than a typo — it is a setting that would never apply.
func TestSaysPollSecondsIsFleetWide(t *testing.T) {
	noRuntimeCheck(t)
	err := loadErr(write(t, configOf(t, worker(t, `, "poll_seconds": 10`))))
	if err == nil || !strings.Contains(err.Error(), "TOP LEVEL") {
		t.Fatalf("want an error saying the poll rate is fleet-wide, got %v", err)
	}
}

func TestRejectsUnknownTopLevelKey(t *testing.T) {
	noRuntimeCheck(t)
	err := loadErr(write(t, `{"poll_secondz":30,"workers":[`+worker(t, "")+`]}`))
	if err == nil {
		t.Fatal("want an error for a misspelled top-level key")
	}
	for _, want := range []string{"poll_secondz", "did you mean", "poll_seconds"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is missing %q:\n%v", want, err)
		}
	}
}

// A worker field written at the top level is the other half of the same
// mistake, and "workers is missing" would be a true but useless answer.
func TestSaysAWorkerFieldAtTopLevelBelongsInAWorker(t *testing.T) {
	noRuntimeCheck(t)
	err := loadErr(write(t, `{"runtime":"claude","workers":[`+worker(t, "")+`]}`))
	if err == nil || !strings.Contains(err.Error(), "belongs inside an entry") {
		t.Fatalf("want an error placing the key inside a worker, got %v", err)
	}
}

// Strictness that rejected a legal config would be worse than the silence it
// replaced, so the full accepted surface is exercised as one file.
func TestAcceptsEveryKeyThisVersionDocuments(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, fmt.Sprintf(`{
	  "poll_seconds": 15,
	  "workers": [{
	    "name": "w",
	    "relay_mcp": "https://relay.example.com/relay/mcp/c/wzh_aaaaaaaa",
	    "repo_dir": %q,
	    "runtime": "claude",
	    "max_runs_per_hour": 6,
	    "max_seconds_per_run": 600,
	    "runtime_config": {"model": "sonnet", "max_usd_per_run": 2.5}
	  }]
	}`, repoHere(t)))
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("a config using every accepted key must load: %v", err)
	}
	w := cfg.Workers[0]
	if cfg.PollSeconds != 15 || w.MaxRunsPerHour != 6 || w.MaxSecondsPerRun != 600 || w.RCFloat("max_usd_per_run") != 2.5 {
		t.Errorf("a value was lost on the way through: %+v poll=%v", w, cfg.PollSeconds)
	}
}

// ── value sanity ─────────────────────────────────────────────────────────────

// Reported as MISSING, someone goes looking for a field that is already there.
func TestRejectsWrongTypedRequiredField(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, configOf(t, strings.Replace(worker(t, ""), `"name": "w"`, `"name": 5`, 1)))
	err := loadErr(p)
	if err == nil || !strings.Contains(err.Error(), "must be a string") {
		t.Fatalf("want a type error naming the field, got %v", err)
	}
	if strings.Contains(err.Error(), `missing "name"`) {
		t.Errorf("a present-but-wrong-typed field is not a missing one:\n%v", err)
	}
}

func TestRejectsEmptyRequiredField(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, configOf(t, strings.Replace(worker(t, ""), `"name": "w"`, `"name": ""`, 1)))
	if err := loadErr(p); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("want an empty-field error, got %v", err)
	}
}

// A name is typed into `tail`, `rm` and a PAUSED path by hand.
func TestRejectsNameWithSurroundingWhitespace(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, configOf(t, strings.Replace(worker(t, ""), `"name": "w"`, `"name": " w "`, 1)))
	if err := loadErr(p); err == nil || !strings.Contains(err.Error(), "whitespace") {
		t.Fatalf("want a whitespace error, got %v", err)
	}
}

// Under `check` a malformed endpoint costs a confusing transport error; under
// `run` it fails on every poll for as long as the fleet is up.
func TestRejectsMalformedEndpoint(t *testing.T) {
	noRuntimeCheck(t)
	for _, bad := range []string{"relay.example.com/c/wzh_a", "wzh_aaaaaaaa", "ftp://relay.example.com/c/wzh_a"} {
		p := write(t, configOf(t, strings.Replace(worker(t, ""),
			`"relay_mcp": "https://x/c/wzh_aaaaaaaa"`, `"relay_mcp": `+fmt.Sprintf("%q", bad), 1)))
		err := loadErr(p)
		if err == nil || !strings.Contains(err.Error(), "http(s) URL") {
			t.Errorf("%q: want a URL-shape error, got %v", bad, err)
		}
		// The value is the credential and never belongs in an error.
		if err != nil && strings.Contains(err.Error(), bad) {
			t.Errorf("%q: the error quotes the endpoint back:\n%v", bad, err)
		}
	}
}

// The same config would drive a different checkout depending on where the
// fleet was started from, and the wrong one would still look like it worked.
func TestRejectsRelativeRepoDir(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"workers":[{"name":"w","relay_mcp":"https://x/c/wzh_a","repo_dir":"./checkout",`+
		`"runtime":"claude","runtime_config":{"model":"sonnet"}}]}`)
	if err := loadErr(p); err == nil || !strings.Contains(err.Error(), "relative path") {
		t.Fatalf("want a relative-path error, got %v", err)
	}
}

// Truncating 6.9 into a ceiling of 6 is a limit nobody chose and nothing would
// ever have mentioned.
func TestRejectsUnsaneCeilings(t *testing.T) {
	noRuntimeCheck(t)
	for _, tc := range []struct{ entry, want string }{
		{`, "max_runs_per_hour": -1`, "cannot be negative"},
		{`, "max_runs_per_hour": 6.5`, "whole runs"},
		{`, "max_seconds_per_run": -30`, "cannot be negative"},
		{`, "max_seconds_per_run": 5`, "minimum"},
		{`, "max_seconds_per_run": "900"`, "must be a JSON number"},
	} {
		err := loadErr(write(t, configOf(t, worker(t, tc.entry))))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error containing %q, got %v", tc.entry, tc.want, err)
		}
	}
}

// 0 is the operator removing a ceiling deliberately, which is not the same as
// setting a nonsensical one.
func TestAcceptsZeroCeilings(t *testing.T) {
	noRuntimeCheck(t)
	cfg, err := LoadConfig(write(t, configOf(t, worker(t, `, "max_runs_per_hour": 0, "max_seconds_per_run": 0`))))
	if err != nil {
		t.Fatalf("0 removes a ceiling deliberately and must be accepted: %v", err)
	}
	if w := cfg.Workers[0]; w.MaxRunsPerHour != 0 || w.MaxSecondsPerRun != 0 {
		t.Errorf("a deliberate 0 was not preserved: %+v", w)
	}
}

// The value is passed to the CLI verbatim, so " sonnet" is rejected inside a
// run that has already been paid for.
func TestRejectsUntrimmedRuntimeConfigValue(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, configOf(t, strings.Replace(worker(t, ""), `"model": "sonnet"`, `"model": " sonnet "`, 1)))
	err := loadErr(p)
	if err == nil || !strings.Contains(err.Error(), "whitespace") {
		t.Fatalf("want a whitespace error, got %v", err)
	}
	if !strings.Contains(err.Error(), `"sonnet"`) {
		t.Errorf("the error should show what to write instead:\n%v", err)
	}
}

// An empty file is a step half done. "not valid JSON" describes it correctly
// and helps nobody, and `relay init` refuses to overwrite it — so the error has
// to say more than "run init".
func TestEmptyConfigSaysWhatToDo(t *testing.T) {
	for _, body := range []string{"", "   \n\n", "// only a comment\n"} {
		err := loadErr(write(t, body))
		if err == nil {
			t.Fatalf("%q: want an error for a config with nothing in it", body)
		}
		for _, want := range []string{"relay init", "overwrite"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q: the error does not mention %q:\n%v", body, want, err)
			}
		}
	}
}
