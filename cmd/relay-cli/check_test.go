package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeConfigFor builds a config pointing at the given endpoints, one worker
// each, and stubs the runtime check: `check` is about reaching relay, not about
// whether a coding CLI happens to be installed on this machine.
func writeConfigFor(t *testing.T, endpoints ...string) string {
	t.Helper()
	return writeConfigForRepo(t, t.TempDir(), endpoints...)
}

// writeConfigForRepo is the same, for a test that needs to put something in the
// working directory and then read what check says about it.
func writeConfigForRepo(t *testing.T, repo string, endpoints ...string) string {
	t.Helper()
	noRuntimeCheck(t)
	var workers []string
	for i, ep := range endpoints {
		b, _ := json.Marshal(ep)
		workers = append(workers, `{"name":"w`+string(rune('1'+i))+`","relay_mcp":`+string(b)+
			`,"repo_dir":"`+repo+`","runtime":"claude","runtime_config":{"model":"sonnet"}}`)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, configFileName)
	body := `{"workers":[` + strings.Join(workers, ",") + `]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckReportsAHealthyFleet(t *testing.T) {
	relay := mcpStub(t, threeBuckets, true)
	defer relay.Close()

	var out bytes.Buffer
	if err := check(writeConfigFor(t, relay.URL+"/c/wzh_aaaaaaaa"), 5*time.Second, &out); err != nil {
		t.Fatalf("check on a reachable relay should succeed, got %v", err)
	}
	got := out.String()
	for _, want := range []string{"w1", "ok", "resume 1", "attention 2", "todo 3", "nothing was spent"} {
		if !strings.Contains(got, want) {
			t.Errorf("check output missing %q:\n%s", want, got)
		}
	}
}

// A backlog nothing will pick up is the confusing case check has to name. The
// counts alone read as work about to start, and this is where someone looks
// first when a fleet sits idle with tasks filed against it.
func TestCheckNamesAWithheldQueue(t *testing.T) {
	relay := mcpStub(t, atLimitQueue, true)
	defer relay.Close()

	var out bytes.Buffer
	if err := check(writeConfigFor(t, relay.URL+"/c/wzh_aaaaaaaa"), 5*time.Second, &out); err != nil {
		t.Fatalf("an at-limit queue is a healthy fleet, not a failure: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "at limit, none claimable") {
		t.Errorf("check does not say the queue is withheld:\n%s", got)
	}
	if !strings.Contains(got, "todo 3") {
		t.Errorf("check stopped reporting the real counts:\n%s", got)
	}
}

// An ordinary queue says nothing about limits, or the note stops meaning
// anything on the fleets it is there to explain.
func TestCheckSaysNothingAboutLimitsWhenThereIsNone(t *testing.T) {
	relay := mcpStub(t, threeBuckets, true)
	defer relay.Close()

	var out bytes.Buffer
	if err := check(writeConfigFor(t, relay.URL+"/c/wzh_aaaaaaaa"), 5*time.Second, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "at limit") {
		t.Errorf("check called an ordinary queue withheld:\n%s", out.String())
	}
}

// The failure this command exists for. A revoked credential and an empty queue
// are indistinguishable from the outside until something asks, and asking must
// not cost a run.
func TestCheckFailsOnARevokedCredential(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer relay.Close()

	var out bytes.Buffer
	err := check(writeConfigFor(t, relay.URL+"/c/wzh_aaaaaaaa"), 5*time.Second, &out)
	if err == nil {
		t.Fatal("check should fail when relay answers 401")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "issue_agent_credential") {
		t.Errorf("the 401 error should name the cause and the fix, got: %v", err)
	}
	if !strings.Contains(out.String(), "FAIL") {
		t.Errorf("the failing worker should be listed by name:\n%s", out.String())
	}
}

// One dead worker must not hide the healthy ones: the whole point of checking a
// fleet at once is to see every answer in a single pass.
func TestCheckReportsEveryWorkerEvenWhenOneFails(t *testing.T) {
	ok := mcpStub(t, threeBuckets, true)
	defer ok.Close()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer dead.Close()

	var out bytes.Buffer
	err := check(writeConfigFor(t, ok.URL+"/c/wzh_aaaaaaaa", dead.URL+"/c/wzh_bbbbbbbb"), 5*time.Second, &out)
	if err == nil {
		t.Fatal("a fleet with one unreachable worker should not report success")
	}
	got := out.String()
	if !strings.Contains(got, "w1") || !strings.Contains(got, "w2") {
		t.Errorf("both workers should appear:\n%s", got)
	}
	if !strings.Contains(got, "ok") || !strings.Contains(got, "FAIL") {
		t.Errorf("want one ok and one FAIL:\n%s", got)
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("the summary should count the failures, got: %v", err)
	}
}

// check prints a probe's error straight to a terminal, and a probe error can
// quote the URL it failed on — which is the credential.
func TestCheckNeverPrintsTheSecret(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer relay.Close()

	const secret = "wzh_supersecretvalue"
	var out bytes.Buffer
	err := check(writeConfigFor(t, relay.URL+"/c/"+secret), 5*time.Second, &out)
	if err == nil {
		t.Fatal("expected a failure to report")
	}
	if strings.Contains(out.String(), secret) || strings.Contains(err.Error(), secret) {
		t.Errorf("the connector secret reached the output:\n%s\n%v", out.String(), err)
	}
}

// The two failures have different fixes, so the summary must not offer the
// credential advice for what is actually an unreachable host.
func TestCheckDistinguishesUnreachableFromUnauthorized(t *testing.T) {
	var out bytes.Buffer
	// A port nothing is listening on: a connection error, not a 401.
	err := check(writeConfigFor(t, "http://127.0.0.1:1/c/wzh_aaaaaaaa"), 2*time.Second, &out)
	if err == nil {
		t.Fatal("expected a failure")
	}
	if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "issue_agent_credential") {
		t.Errorf("an unreachable host should not be reported as a credential problem: %v", err)
	}
	if !strings.Contains(err.Error(), "could not be reached") {
		t.Errorf("want the unreachable-host hint, got: %v", err)
	}
}

// A config that cannot be loaded must fail before any network call: check is
// also the way to validate a config, and the parse error is the useful answer.
func TestCheckSurfacesConfigErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, configFileName)
	os.WriteFile(p, []byte(`{"workers":[{"name":"a/b","relay_mcp":"https://x/c/wzh_aaaaaaaa",`+
		`"repo_dir":"`+t.TempDir()+`","runtime":"claude","runtime_config":{"model":"sonnet"}}]}`), 0o600)

	var out bytes.Buffer
	err := check(p, time.Second, &out)
	if err == nil || !strings.Contains(err.Error(), "filesystem-safe") {
		t.Fatalf("want the config validation error, got %v", err)
	}
}

// Neither command may get as far as the network — or as far as launching a
// session — with a config nobody has filled in yet. `relay init` writes two
// placeholders, and both commands stop on them by name.
func TestCommandsRefuseTheUntouchedInitConfig(t *testing.T) {
	noRuntimeCheck(t)
	withInstalledRuntimes(t, "claude", "codex")
	dir := filepath.Join(t.TempDir(), relayDirName)
	if err := initConfig(dir, io.Discard); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, configFileName)

	var out bytes.Buffer
	checkErr := check(path, time.Second, &out)
	// port 0 and no-open: run must fail on the config long before it would
	// listen on anything or start a worker.
	runErr := run(path, 0, true, true, true, false)

	for name, err := range map[string]error{"check": checkErr, "run": runErr} {
		if err == nil {
			t.Fatalf("%s accepted a config still holding the init placeholders", name)
		}
		for _, want := range []string{"relay_mcp", "placeholder", "issue_agent_credential"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the error is missing %q:\n%v", name, want, err)
			}
		}
	}
	if out.Len() > 0 {
		t.Errorf("check printed a report for a config it refused:\n%s", out.String())
	}
}

// Every command that reads the config has to say the same thing when there
// isn't one: run init. Finding out by way of a JSON parse error, or by a fleet
// that starts with no workers, is how a first five minutes goes wrong.
func TestCommandsWithNoConfigPointAtInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)

	var out bytes.Buffer
	for name, err := range map[string]error{
		"check": check(path, time.Second, &out),
		"run":   run(path, 0, true, true, true, false),
	} {
		if err == nil {
			t.Fatalf("%s ran with no config at all", name)
		}
		if !strings.Contains(err.Error(), "relay init") {
			t.Errorf("%s: the error does not name the command that fixes it:\n%v", name, err)
		}
	}
}

// The probe proves relay is reachable; this line proves the other half of a
// worker's setup. A CLAUDE.md written one directory up, or a skill in a folder
// the CLI does not read, produces a worker that starts, spends and knows none of
// it — and nothing else in this tool would ever say so.
func TestCheckReportsWhatTheWorkingDirectoryHolds(t *testing.T) {
	relay := mcpStub(t, threeBuckets, true)
	defer relay.Close()

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".claude", "skills", "release-check"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"CLAUDE.md": "# rules\n",
		filepath.Join(".claude", "skills", "release-check", "SKILL.md"): "---\nname: release-check\n---\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := check(writeConfigForRepo(t, repo, relay.URL+"/c/wzh_aaaaaaaa"), 5*time.Second, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{repo, "CLAUDE.md", "1 skill"} {
		if !strings.Contains(got, want) {
			t.Errorf("check output missing %q:\n%s", want, got)
		}
	}
}

// An empty working directory is a valid setup — check has to describe it, not
// fail it. Anything else pushes people into writing files they do not need.
func TestCheckAcceptsAnEmptyWorkingDirectory(t *testing.T) {
	relay := mcpStub(t, threeBuckets, true)
	defer relay.Close()

	var out bytes.Buffer
	if err := check(writeConfigFor(t, relay.URL+"/c/wzh_aaaaaaaa"), 5*time.Second, &out); err != nil {
		t.Fatalf("an empty repo_dir is fine, got %v", err)
	}
	if !strings.Contains(out.String(), "nothing to load") {
		t.Errorf("check should say the directory is empty:\n%s", out.String())
	}
}

// A signed-out CLI must not hide what relay said. check probes every
// credential first and reports both, so a config with one wrong URL and one
// CLI to sign in to is fixed in one sitting rather than two.
func TestCheckProbesCredentialsEvenWhenARuntimeIsUnusable(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer relay.Close()

	path := writeConfigFor(t, relay.URL+"/c/wzh_aaaaaaaa")
	prev := checkRuntime
	checkRuntime = func(name, runtime, relayDir string) error {
		return fmt.Errorf("worker %q cannot run: runtime %q is unusable.\n       the installed %s is not signed in.", name, runtime, runtime)
	}
	t.Cleanup(func() { checkRuntime = prev })

	var out bytes.Buffer
	err := check(path, 5*time.Second, &out)
	if err == nil {
		t.Fatal("check passed with a 401 and a signed-out CLI")
	}
	if !strings.Contains(out.String(), "FAIL") {
		t.Errorf("the probe result was not reported:\n%s", out.String())
	}
	for _, want := range []string{"401", "issue_agent_credential", "not usable here", "not signed in"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is missing %q:\n%v", want, err)
		}
	}
}

// A healthy relay and a signed-out CLI is still a failure — run would refuse —
// but the ok line is printed so the reader knows which half is done.
func TestCheckFailsOnAnUnusableRuntimeAlone(t *testing.T) {
	relay := mcpStub(t, threeBuckets, true)
	defer relay.Close()

	path := writeConfigFor(t, relay.URL+"/c/wzh_aaaaaaaa")
	prev := checkRuntime
	checkRuntime = func(name, runtime, relayDir string) error {
		return fmt.Errorf("worker %q cannot run: runtime %q is unusable.", name, runtime)
	}
	t.Cleanup(func() { checkRuntime = prev })

	var out bytes.Buffer
	err := check(path, 5*time.Second, &out)
	if err == nil || !strings.Contains(err.Error(), "not usable here") {
		t.Fatalf("want the runtime failure, got %v", err)
	}
	if !strings.Contains(out.String(), "ok    queue") {
		t.Errorf("the healthy probe was not reported:\n%s", out.String())
	}
	if strings.Contains(out.String(), "ready") {
		t.Errorf("check said the fleet is ready with a runtime it cannot run:\n%s", out.String())
	}
}
