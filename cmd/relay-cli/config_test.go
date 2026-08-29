package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The stripper's one real hazard: an mcp_endpoint is a URL, and "http://host"
// contains the comment marker. Truncating there would turn a credential into
// "http:" and fail much later, somewhere far less obvious.
func TestStripLineCommentsKeepsURLs(t *testing.T) {
	in := `{
  // a comment
  "mcp_endpoint": "http://localhost:8080/relay/mcp/c/wzh_abc", // trailing
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
	p := filepath.Join(dir, ".worker-config")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// noRuntimeCheck stubs out the "is this CLI installed?" half of validation for
// tests that are about parsing. Without it, `go test ./...` on a fresh clone
// would require Claude Code to be installed to test a JSON parser. The real
// check keeps its own coverage in TestRejectsUnknownRuntime and in
// runtime_claude_check_test.go, neither of which stubs it.
func noRuntimeCheck(t *testing.T) {
	t.Helper()
	prev := checkRuntime
	checkRuntime = func(name, runtime, pollerRoot string) error { return nil }
	t.Cleanup(func() { checkRuntime = prev })
}

func TestDefaultsMatchTheBashPoller(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"relay_workers":[{"name":"w1","mcp_endpoint":"https://r.example/relay/mcp/c/wzh_secret_value"}]}`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.Workers[0]
	if w.Runtime != "claude" || w.PollSeconds != 30 || w.MaxRunsPerHour != 12 ||
		w.MaxBudgetUSD != 5 || w.RunTimeoutSecs != 900 {
		t.Errorf("defaults drifted: %+v", w)
	}
	if w.RepoDir != "" {
		t.Errorf("repo_dir should default to empty (worker state dir), got %q", w.RepoDir)
	}
}

// "30" as a string is the classic version of this mistake, and reached the bash
// loop as a broken sleep.
func TestRejectsStringPollFrequency(t *testing.T) {
	p := write(t, `{"relay_workers":[{"name":"w","mcp_endpoint":"https://x/c/wzh_aaaaaaaa","poll_frequency_seconds":"30"}]}`)
	_, err := LoadConfig(p)
	if err == nil || !strings.Contains(err.Error(), "positive JSON number") {
		t.Fatalf("want a poll_frequency_seconds error, got %v", err)
	}
}

func TestRejectsNonPositivePollFrequency(t *testing.T) {
	p := write(t, `{"relay_workers":[{"name":"w","mcp_endpoint":"https://x/c/wzh_aaaaaaaa","poll_frequency_seconds":0}]}`)
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("want an error for poll_frequency_seconds: 0")
	}
}

// The floor exists to protect relay, not the worker, so it is enforced rather
// than advised: a config below it does not start at all. A misplaced decimal
// point is the realistic way someone arranges a flood by accident.
func TestRejectsPollFrequencyBelowTheMinimum(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"relay_workers":[{"name":"w","mcp_endpoint":"https://x/c/wzh_aaaaaaaa","poll_frequency_seconds":0.5}]}`)
	_, err := LoadConfig(p)
	if err == nil {
		t.Fatal("want an error for a poll_frequency_seconds below the minimum")
	}
	// The message has to name the floor and the worker: "invalid config" would
	// leave someone guessing which worker and which number.
	for _, want := range []string{"minimum", `"w"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

// The floor itself is a legal value. Rejecting it too would make the documented
// minimum a number nobody can actually set.
func TestAcceptsPollFrequencyAtTheMinimum(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, fmt.Sprintf(`{"relay_workers":[{"name":"w","mcp_endpoint":"https://x/c/wzh_aaaaaaaa","poll_frequency_seconds":%g}]}`, minPollSeconds))
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("%gs is the floor and must be accepted: %v", minPollSeconds, err)
	}
	if got := cfg.Workers[0].PollSeconds; got != minPollSeconds {
		t.Errorf("PollSeconds = %v, want %v", got, minPollSeconds)
	}
}

// A config still carrying system_prompt_file would otherwise launch an agent
// with no standing instructions at all and look fine doing it.
func TestRejectsRemovedKeysByName(t *testing.T) {
	for key, hint := range map[string]string{
		"system_prompt_file":       "instructions_md",
		"min_run_interval_seconds": "60s relaunch cooldown",
		"permission_mode":          "fully autonomous",
	} {
		p := write(t, `{"relay_workers":[{"name":"w","mcp_endpoint":"https://x/c/wzh_aaaaaaaa","`+key+`":"v"}]}`)
		err := LoadConfig1(p)
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), hint) {
			t.Errorf("%s: want a rejection naming the key and its replacement, got %v", key, err)
		}
	}
}

func LoadConfig1(p string) error { _, err := LoadConfig(p); return err }

func TestRejectsMissingRequiredFields(t *testing.T) {
	for _, body := range []string{
		`{"relay_workers":[{"mcp_endpoint":"https://x/c/wzh_aaaaaaaa"}]}`,
		`{"relay_workers":[{"name":"w"}]}`,
	} {
		if err := LoadConfig1(write(t, body)); err == nil {
			t.Errorf("want an error for %s", body)
		}
	}
}

func TestRejectsDuplicates(t *testing.T) {
	dupName := `{"relay_workers":[
	  {"name":"w","mcp_endpoint":"https://x/c/wzh_aaaaaaaa"},
	  {"name":"w","mcp_endpoint":"https://x/c/wzh_bbbbbbbb"}]}`
	if err := LoadConfig1(write(t, dupName)); err == nil || !strings.Contains(err.Error(), "duplicate worker name") {
		t.Errorf("want a duplicate-name error, got %v", err)
	}
	dupEndpoint := `{"relay_workers":[
	  {"name":"a","mcp_endpoint":"https://x/c/wzh_aaaaaaaa"},
	  {"name":"b","mcp_endpoint":"https://x/c/wzh_aaaaaaaa"}]}`
	if err := LoadConfig1(write(t, dupEndpoint)); err == nil || !strings.Contains(err.Error(), "duplicate mcp_endpoint") {
		t.Errorf("want a duplicate-endpoint error, got %v", err)
	}
}

// A name becomes live-workers/<name>/. The bash poller only asked this in a
// comment, and a worker called "a/b" silently wrote its state elsewhere.
func TestRejectsUnsafeName(t *testing.T) {
	p := write(t, `{"relay_workers":[{"name":"a/b","mcp_endpoint":"https://x/c/wzh_aaaaaaaa"}]}`)
	if err := LoadConfig1(p); err == nil || !strings.Contains(err.Error(), "filesystem-safe") {
		t.Fatalf("want a name-safety error, got %v", err)
	}
}

func TestRejectsMissingRepoDir(t *testing.T) {
	noRuntimeCheck(t)
	p := write(t, `{"relay_workers":[{"name":"w","mcp_endpoint":"https://x/c/wzh_aaaaaaaa","repo_dir":"/nope/definitely/not/here"}]}`)
	if err := LoadConfig1(p); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want a repo_dir error, got %v", err)
	}
}

// An unsupported runtime is refused at load, before anything launches, and the
// error names the worker as well as the runtime — a fleet config has several,
// and "unknown runtime" alone does not say which one to fix.
func TestRejectsUnknownRuntime(t *testing.T) {
	p := write(t, `{"relay_workers":[{"name":"w","mcp_endpoint":"https://x/c/wzh_aaaaaaaa","runtime":"nope"}]}`)
	err := LoadConfig1(p)
	if err == nil {
		t.Fatal("want an unknown-runtime error")
	}
	for _, want := range []string{`worker "w"`, "claude"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q, got %v", want, err)
		}
	}
}

// The shipped example must survive its own validator, comments and all. Only
// its repo_dir paths are fictional, so they are dropped first.
func TestShippedExampleValidates(t *testing.T) {
	noRuntimeCheck(t)
	src, err := os.ReadFile("../../.worker-config.example")
	if err != nil {
		t.Skip("example not present")
	}
	body := strings.ReplaceAll(string(src), `"repo_dir": "~/code/wizhub",`, "")
	if err := LoadConfig1(write(t, body)); err != nil {
		t.Fatalf("the shipped .worker-config.example does not validate: %v", err)
	}
}

// A directory means "the config in there". Someone who ran init knows the
// directory it made; making them remember the filename inside it is friction
// for nothing.
func TestConfigPathAcceptsADirectory(t *testing.T) {
	noRuntimeCheck(t)
	dir := filepath.Join(t.TempDir(), homeDirName)
	var out strings.Builder
	if err := initConfig(dir, &out); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("--config <directory> should find the config inside it: %v", err)
	}
	if want := filepath.Join(dir, defaultConfigName); cfg.Path != want {
		t.Errorf("loaded %q, want %q", cfg.Path, want)
	}
}

// init, check and run are meant to be run from one directory with no flags, so
// the lookup has to reach what init created without being told.
func TestFindsTheConfigInitCreated(t *testing.T) {
	noRuntimeCheck(t)
	dir := t.TempDir()
	chdir(t, dir)

	var out strings.Builder
	if err := initConfig(homeDirName, &out); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(defaultConfigName)
	if err != nil {
		t.Fatalf("a bare check/run should find relay-cli-workers/.worker-config: %v", err)
	}
	if !strings.HasSuffix(cfg.Path, filepath.Join(homeDirName, defaultConfigName)) {
		t.Errorf("loaded %q, want the config under %s/", cfg.Path, homeDirName)
	}
}

// "not found" has to say where it looked, or the reader cannot tell a missing
// file from a file in the wrong place.
func TestMissingConfigNamesEveryPlaceItLooked(t *testing.T) {
	chdir(t, t.TempDir())

	_, err := LoadConfig(defaultConfigName)
	if err == nil {
		t.Fatal("want an error when there is no config anywhere")
	}
	for _, want := range []string{defaultConfigName, homeDirName, "relay-cli init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%v", want, err)
		}
	}
}

// chdir moves into dir for one test. The config lookup is relative to the
// working directory by design, so proving it needs one.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatal(err)
		}
	})
}
