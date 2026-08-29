package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lines captured from a real `claude -p --output-format stream-json --verbose`
// run, trimmed to the fields this adapter reads.
const (
	lineInit     = `{"type":"system","subtype":"init","cwd":"/repo","session_id":"f5bb6db3-d345-48ab-929b-b2b9b278463f","model":"claude-opus-5","tools":["Read"],"mcp_servers":[{"name":"relay","status":"connected"}],"uuid":"u1"}`
	lineInitBad  = `{"type":"system","subtype":"init","session_id":"s","model":"claude-opus-5","mcp_servers":[{"name":"relay","status":"needs-auth"}]}`
	lineThinkTok = `{"type":"system","subtype":"thinking_tokens","estimated_tokens":48,"session_id":"s"}`
	lineRateLim  = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"},"session_id":"s"}`
	lineSummary  = `{"type":"system","subtype":"task_summary","detail":"Reading sample.txt","session_id":"s"}`
	lineSummary0 = `{"type":"system","subtype":"task_summary","detail":null,"session_id":"s"}`
	lineToolUse  = `{"type":"assistant","message":{"model":"claude-opus-5","content":[{"type":"tool_use","id":"t1","name":"mcp__relay__claim_task","input":{"task_id":23}}]},"session_id":"s"}`
	lineEdit     = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/repo/index.html","old_string":"a","new_string":"b"}}]},"session_id":"s"}`
	lineText     = `{"type":"assistant","message":{"content":[{"type":"text","text":"Claimed task 23."}]},"session_id":"s"}`
	lineThinking = `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me look at the file."}]},"session_id":"s"}`
	lineResult   = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t2","type":"tool_result","content":"1\thello\n"}]},"session_id":"s"}`
	lineFinal    = `{"duration_api_ms":11362,"num_turns":3,"session_id":"s","total_cost_usd":0.098919,"permission_denials":[],"terminal_reason":"completed","subtype":"success","result":"Task 23 complete.","type":"result","duration_ms":58042}`
)

func parseOne(t *testing.T, line string) []SessionEvent {
	t.Helper()
	return (&claudeRuntime{}).ParseLine(line)
}

func TestParseInitReportsModelAndMCPStatus(t *testing.T) {
	ev := parseOne(t, lineInit)
	if len(ev) != 1 || ev[0].Type != "init" {
		t.Fatalf("got %+v", ev)
	}
	if ev[0].Model != "claude-opus-5" || !strings.Contains(ev[0].Text, "relay: connected") {
		t.Errorf("init lost the fields that matter: %+v", ev[0])
	}
	// A relay that is needs-auth produces a run that looks healthy and does
	// nothing — the exact failure sitting in this repo's archived logs. It has to
	// survive into the event.
	bad := parseOne(t, lineInitBad)
	if !strings.Contains(bad[0].Text, "needs-auth") {
		t.Errorf("mcp status dropped: %+v", bad[0])
	}
}

// thinking_tokens arrives several times a second and says nothing a human wants.
func TestParseDropsNoise(t *testing.T) {
	for _, line := range []string{lineThinkTok, lineRateLim, lineSummary0} {
		if ev := parseOne(t, line); len(ev) != 0 {
			t.Errorf("%s should be filtered, got %+v", line[:40], ev)
		}
	}
}

func TestParseToolUseCarriesItsTarget(t *testing.T) {
	ev := parseOne(t, lineToolUse)
	if len(ev) != 1 || ev[0].Type != "tool_use" {
		t.Fatalf("got %+v", ev)
	}
	if ev[0].Tool != "mcp__relay__claim_task" || ev[0].Target != "task_id=23" {
		t.Errorf("a tool call without its target is nearly content-free: %+v", ev[0])
	}
	ed := parseOne(t, lineEdit)
	if ed[0].Target != "/repo/index.html" {
		t.Errorf("file_path should read bare, got %q", ed[0].Target)
	}
}

func TestParseTextThinkingAndToolResult(t *testing.T) {
	if ev := parseOne(t, lineText); len(ev) != 1 || ev[0].Type != "assistant" || ev[0].Text != "Claimed task 23." {
		t.Errorf("assistant text: %+v", ev)
	}
	if ev := parseOne(t, lineThinking); len(ev) != 1 || ev[0].Type != "thinking" {
		t.Errorf("thinking: %+v", ev)
	}
	if ev := parseOne(t, lineResult); len(ev) != 1 || ev[0].Type != "tool_result" || !strings.Contains(ev[0].Text, "hello") {
		t.Errorf("tool_result: %+v", ev)
	}
	if ev := parseOne(t, lineSummary); len(ev) != 1 || ev[0].Type != "status" || ev[0].Text != "Reading sample.txt" {
		t.Errorf("task_summary: %+v", ev)
	}
}

func TestParseFinalResult(t *testing.T) {
	ev := parseOne(t, lineFinal)
	if len(ev) != 1 || ev[0].Type != "result" {
		t.Fatalf("got %+v", ev)
	}
	if ev[0].CostUSD != 0.098919 || ev[0].NumTurns != 3 || ev[0].Text != "Task 23 complete." {
		t.Errorf("result lost fields: %+v", ev[0])
	}
}

// A CLI's own stderr — an authentication failure arrives this way — must still
// reach the feed.
func TestParseNonJSONBecomesRaw(t *testing.T) {
	ev := parseOne(t, "error: not logged in")
	if len(ev) != 1 || ev[0].Type != "raw" || ev[0].Text != "error: not logged in" {
		t.Fatalf("got %+v", ev)
	}
}

func writeRun(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "last-run.out")
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	return p
}

func TestClassifyBudgetExhausted(t *testing.T) {
	out := writeRun(t, lineInit, `{"type":"result","terminal_reason":"budget_exhausted","total_cost_usd":5.0012,"permission_denials":[]}`)
	rc := &RunContext{Worker: &Worker{}, MaxBudget: "5"}
	outcome, expl := (&claudeRuntime{}).ClassifyExit(rc, 1, out)
	if outcome != outcomeBudget {
		t.Fatalf("outcome = %q, want %q — the loop breaks its retry treadmill on this", outcome, outcomeBudget)
	}
	for _, want := range []string{"KILLED BY ITS SPEND CAP", "$5.0012", "max_budget_usd"} {
		if !strings.Contains(expl, want) {
			t.Errorf("explanation missing %q:\n%s", want, expl)
		}
	}
}

// Denials are silent by construction — a headless run has no prompt to fail — so
// surfacing them is the difference between a five-minute fix and an afternoon.
func TestClassifyReportsPermissionDenials(t *testing.T) {
	out := writeRun(t, `{"type":"result","terminal_reason":"completed","permission_denials":[{"tool_name":"mcp__relay__get_subtask_handoff"},{"tool_name":"mcp__relay__get_subtask_handoff"}]}`)
	rc := &RunContext{Worker: &Worker{}}
	outcome, expl := (&claudeRuntime{}).ClassifyExit(rc, 0, out)
	if outcome != outcomeOK {
		t.Errorf("outcome = %q", outcome)
	}
	if strings.Count(expl, "get_subtask_handoff") != 1 {
		t.Errorf("denials should be deduplicated:\n%s", expl)
	}
}

func TestClassifyPlainExits(t *testing.T) {
	out := writeRun(t, lineFinal)
	rc := &RunContext{Worker: &Worker{}}
	for status, want := range map[int]string{0: outcomeOK, 124: outcomeTimeout, 1: outcomeError} {
		if got, _ := (&claudeRuntime{}).ClassifyExit(rc, status, out); got != want {
			t.Errorf("status %d → %q, want %q", status, got, want)
		}
	}
}

func TestBuildCmdStreamsAndBounds(t *testing.T) {
	dir := t.TempDir()
	rc := &RunContext{
		Worker:    &Worker{Name: "w", Endpoint: "https://r.example/relay/mcp/c/wzh_secretvalue", Model: "opus"},
		WorkerDir: dir, RepoDir: dir, Prompt: "p", Rules: "r",
		MaxBudget: "5", AllowTools: relayAllowedTools,
	}
	argv, err := (&claudeRuntime{}).BuildCmd(rc)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	// --verbose is not optional: in print mode the CLI only streams with it, and
	// without streaming there is nothing to watch.
	for _, want := range []string{
		"--output-format stream-json", "--verbose", "--strict-mcp-config",
		"--permission-mode auto", "--model opus", "--max-budget-usd 5",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "wzh_secretvalue") {
		t.Error("the credential must ride in the mcp config file, never in argv (it is visible in ps)")
	}

	// The generated MCP config holds the secret, so it lives in the worker's
	// gitignored state dir, owner-readable only.
	info, err := os.Stat(filepath.Join(dir, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mcp.json mode = %v, want 0600", info.Mode().Perm())
	}
	body, _ := os.ReadFile(filepath.Join(dir, "mcp.json"))
	if !strings.Contains(string(body), "wzh_secretvalue") {
		t.Errorf("mcp.json should carry the endpoint: %s", body)
	}
}

// Emitting our flag only when yours is absent keeps a duplicate
// --permission-mode out of the argv entirely, rather than leaving which one wins
// to argument order.
func TestBuildCmdDefersToRuntimeArgsPermissionMode(t *testing.T) {
	dir := t.TempDir()
	rc := &RunContext{
		Worker: &Worker{Name: "w"}, WorkerDir: dir, RepoDir: dir,
		ExtraArgs: "--permission-mode plan",
	}
	argv, err := (&claudeRuntime{}).BuildCmd(rc)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(argv, " "), "--permission-mode"); n != 1 {
		t.Fatalf("--permission-mode appears %d times: %v", n, argv)
	}
	if !strings.Contains(strings.Join(argv, " "), "--permission-mode plan") {
		t.Error("the operator's own flag should be the one that survives")
	}
}
