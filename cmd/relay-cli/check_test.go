package main

import (
	"bytes"
	"encoding/json"
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
	noRuntimeCheck(t)
	repo := t.TempDir()
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
