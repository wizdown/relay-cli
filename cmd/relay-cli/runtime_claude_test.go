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
	rc := &RunContext{Worker: &Worker{RuntimeConfig: map[string]string{"max_usd_per_run": "5"}}}
	outcome, expl := (&claudeRuntime{}).ClassifyExit(rc, 1, out)
	if outcome != outcomeBudget {
		t.Fatalf("outcome = %q, want %q — the loop breaks its retry treadmill on this", outcome, outcomeBudget)
	}
	for _, want := range []string{"KILLED BY ITS SPEND CAP", "$5.0012", "max_usd_per_run"} {
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
		Worker: &Worker{Name: "w", Endpoint: "https://r.example/relay/mcp/c/wzh_secretvalue",
			RuntimeConfig: map[string]string{"model": "opus", "max_usd_per_run": "5"}},
		WorkerDir: dir, RepoDir: dir, Prompt: "p", Rules: "r",
		AllowTools: relayAllowedTools,
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

// `auto` is what this harness IS, not a setting. Anything stricter silently
// denies whatever was not pre-allowed and the session stalls with no prompt to
// answer — so with runtime_args gone there is deliberately no way to talk the
// adapter out of it.
func TestBuildCmdAlwaysRunsFullyAutonomous(t *testing.T) {
	dir := t.TempDir()
	rc := &RunContext{
		Worker:    &Worker{Name: "w", RuntimeConfig: map[string]string{"model": "sonnet"}},
		WorkerDir: dir, RepoDir: dir,
	}
	argv, err := (&claudeRuntime{}).BuildCmd(rc)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if n := strings.Count(joined, "--permission-mode"); n != 1 {
		t.Fatalf("--permission-mode appears %d times: %v", n, argv)
	}
	if !strings.Contains(joined, "--permission-mode auto") {
		t.Errorf("a headless run has to be fully autonomous:\n%s", joined)
	}
}

// 0 is the operator deliberately removing the cap, and is spelled by omitting
// the flag rather than by passing --max-budget-usd 0, which the CLI would read
// as a cap of nothing and kill every run instantly.
func TestBuildCmdOmitsAZeroSpendCap(t *testing.T) {
	dir := t.TempDir()
	rc := &RunContext{
		Worker: &Worker{Name: "w",
			RuntimeConfig: map[string]string{"model": "sonnet", "max_usd_per_run": "0"}},
		WorkerDir: dir, RepoDir: dir,
	}
	argv, err := (&claudeRuntime{}).BuildCmd(rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "--max-budget-usd") {
		t.Errorf("a 0 cap must omit the flag entirely: %v", argv)
	}
}

// ── the working-directory summary `relay check` prints ───────────────────────

// A skill is <name>/SKILL.md and nothing else. Counting a directory that merely
// looks like one would be exactly the false reassurance this line exists to
// avoid: the operator reads "2 skills", and the run loads one.
func TestInspectWorkdirCountsOnlyWhatTheCLILoads(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("CLAUDE.md", "# rules\n")
	write(".claude/skills/release-check/SKILL.md", "---\nname: release-check\n---\n")
	write(".claude/skills/notes/skill.md", "lowercase, so not a skill\n")
	write(".claude/agents/reviewer.md", "---\nname: reviewer\n---\n")
	write(".claude/settings.json", `{"hooks":{"PostToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"gofmt -w ."}]}]}}`)

	got := (&claudeRuntime{}).InspectWorkdir(dir)
	for _, want := range []string{"CLAUDE.md", "1 skill", "1 subagent", "1 hook"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "2 skills") {
		t.Errorf("skill.md is not a skill the CLI loads:\n%s", got)
	}
}

// An empty directory is a valid setup, so the empty case describes what the
// agent still arrives with. Reporting it as a problem would push people into
// writing a CLAUDE.md they do not need.
func TestInspectWorkdirTreatsAnEmptyDirectoryAsFine(t *testing.T) {
	got := (&claudeRuntime{}).InspectWorkdir(t.TempDir())
	if !strings.Contains(got, "nothing to load") {
		t.Errorf("an empty working directory should say so plainly, got %q", got)
	}
}

// The CLI ignores a settings file that does not parse, silently, in headless
// mode — so a typo there is invisible unless something says it out loud.
func TestInspectWorkdirNamesASettingsFileThatDoesNotParse(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(`{"hooks":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (&claudeRuntime{}).InspectWorkdir(dir); !strings.Contains(got, "does not parse") {
		t.Errorf("an invalid settings file has to be named, got %q", got)
	}
}

// A headless run denies whatever no rule matches. With relay's surface alone a
// worker can read its task and hand it back, and cannot edit one file — a
// failure that is silent until the run's permission_denials, after it is paid
// for. This is the guard on that.
func TestWorkerAllowlistCarriesTheToolsTheWorkNeeds(t *testing.T) {
	for _, tool := range []string{"Read", "Edit", "Write", "Bash", "Skill"} {
		if !strings.Contains(workerAllowedTools, tool) {
			t.Errorf("%s is missing: a worker without it is silently denied mid-run", tool)
		}
	}
	if !strings.Contains(workerAllowedTools, "mcp__relay__claim_task") {
		t.Error("relay's own surface must still be pre-allowed")
	}
}
