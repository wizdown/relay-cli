package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lines in the shape of a `codex exec --json` run, trimmed to the fields this
// adapter reads.
const (
	cxThread   = `{"type":"thread.started","thread_id":"019ce6ce-65fd-7530-8e6b-9ccce0436091"}`
	cxTurn     = `{"type":"turn.started"}`
	cxCmdStart = `{"type":"item.started","item":{"id":"i1","item_type":"command_execution","command":"bash -lc 'go test ./...'","status":"in_progress"}}`
	cxCmdDone  = `{"type":"item.completed","item":{"id":"i1","item_type":"command_execution","command":"bash -lc 'go test ./...'","aggregated_output":"ok\tpkg\t0.4s","exit_code":0,"status":"completed"}}`
	cxCmdFail  = `{"type":"item.completed","item":{"id":"i2","item_type":"command_execution","command":"bash -lc 'go build'","aggregated_output":"undefined: x","exit_code":2,"status":"completed"}}`
	cxMCPStart = `{"type":"item.started","item":{"id":"i3","item_type":"mcp_tool_call","server":"relay","tool":"claim_task","status":"in_progress"}}`
	cxMCPDone  = `{"type":"item.completed","item":{"id":"i3","item_type":"mcp_tool_call","server":"relay","tool":"claim_task","status":"completed"}}`
	cxMsg      = `{"type":"item.completed","item":{"id":"i4","item_type":"agent_message","text":"Claimed task 23."}}`
	cxReason   = `{"type":"item.completed","item":{"id":"i5","item_type":"reasoning","text":"Reading the failing test first."}}`
	cxUpdated  = `{"type":"item.updated","item":{"id":"i4","item_type":"agent_message","text":"Claimed"}}`
	cxFiles    = `{"type":"item.completed","item":{"id":"i6","item_type":"file_change","status":"completed","changes":[{"path":"/repo/main.go","kind":"modify"},{"path":"/repo/new.go","kind":"add"}]}}`
	cxSearch   = `{"type":"item.completed","item":{"id":"i7","item_type":"web_search","query":"go 1.22 range over int"}}`
	cxDone     = `{"type":"turn.completed","usage":{"input_tokens":8497,"cached_input_tokens":8448,"output_tokens":611}}`
	cxFailed   = `{"type":"turn.failed","error":{"message":"You've hit your usage limit. Try again after 4pm."}}`
	// An older build spells the item discriminator `type` rather than
	// `item_type`; both have to keep parsing or the feed goes silent.
	cxMsgAltKey = `{"type":"item.completed","item":{"id":"i8","type":"agent_message","text":"Done."}}`
)

func parseCodex(t *testing.T, line string) []SessionEvent {
	t.Helper()
	return (&codexRuntime{}).ParseLine(line)
}

func TestCodexParsesTheSessionShape(t *testing.T) {
	if ev := parseCodex(t, cxThread); len(ev) != 1 || ev[0].Type != "init" || ev[0].SessionID == "" {
		t.Errorf("thread.started should open the session: %+v", ev)
	}
	if ev := parseCodex(t, cxMsg); len(ev) != 1 || ev[0].Type != "assistant" || ev[0].Text != "Claimed task 23." {
		t.Errorf("agent message: %+v", ev)
	}
	if ev := parseCodex(t, cxReason); len(ev) != 1 || ev[0].Type != "thinking" {
		t.Errorf("reasoning: %+v", ev)
	}
	if ev := parseCodex(t, cxMsgAltKey); len(ev) != 1 || ev[0].Type != "assistant" {
		t.Errorf("an item spelled with `type` still has to parse: %+v", ev)
	}
	if ev := parseCodex(t, cxSearch); len(ev) != 1 || ev[0].Tool != "WebSearch" || ev[0].Target == "" {
		t.Errorf("web search: %+v", ev)
	}
}

// item.updated repeats the partial text of a message item.completed sends in
// full. Keeping both would double every assistant turn in the ring, on disk and
// on the page.
func TestCodexDropsPartialUpdates(t *testing.T) {
	for _, line := range []string{cxUpdated, cxTurn} {
		if ev := parseCodex(t, line); len(ev) != 0 {
			t.Errorf("%s should be filtered, got %+v", line[:30], ev)
		}
	}
}

// A tool call is narrated when it starts, so a five-minute test run is visible
// while it happens rather than only once it is over.
func TestCodexNarratesToolCallsAsTheyStart(t *testing.T) {
	ev := parseCodex(t, cxCmdStart)
	if len(ev) != 1 || ev[0].Type != "tool_use" || !strings.Contains(ev[0].Target, "go test") {
		t.Fatalf("command start: %+v", ev)
	}
	if ev := parseCodex(t, cxCmdDone); len(ev) != 1 || ev[0].Type != "tool_result" || ev[0].IsError {
		t.Errorf("a command that exited 0 is not an error: %+v", ev)
	}
	fail := parseCodex(t, cxCmdFail)
	if len(fail) != 1 || !fail[0].IsError || !strings.Contains(fail[0].Text, "exit 2") {
		t.Errorf("a non-zero exit has to read as one: %+v", fail)
	}
}

// Relay calls are spelled the way the claude adapter spells them, so a run reads
// the same whichever CLI produced it and the dashboard has one shape to strip.
func TestCodexNamesRelayToolsLikeClaude(t *testing.T) {
	ev := parseCodex(t, cxMCPStart)
	if len(ev) != 1 || ev[0].Tool != "mcp__relay__claim_task" {
		t.Fatalf("mcp call: %+v", ev)
	}
	if done := parseCodex(t, cxMCPDone); len(done) != 1 || !strings.Contains(done[0].Text, "claim_task") {
		t.Errorf("mcp result should name the tool: %+v", done)
	}
}

func TestCodexReportsOneEventPerFileChanged(t *testing.T) {
	ev := parseCodex(t, cxFiles)
	if len(ev) != 2 {
		t.Fatalf("want one event per change, got %+v", ev)
	}
	if ev[0].Tool != "Edit" || ev[0].Target != "/repo/main.go" {
		t.Errorf("modified file: %+v", ev[0])
	}
	if ev[1].Tool != "Write" {
		t.Errorf("an added file is a write: %+v", ev[1])
	}
}

// Codex reports tokens and no cost. A run whose spend line is blank reads as
// free, so the usage has to survive into the result event the cards read.
func TestCodexResultCarriesTokenUsage(t *testing.T) {
	ev := parseCodex(t, cxDone)
	if len(ev) != 1 || ev[0].Type != "result" {
		t.Fatalf("turn.completed: %+v", ev)
	}
	if ev[0].Usage == nil || ev[0].Usage.Total() != 8497+611 {
		t.Fatalf("usage lost: %+v", ev[0].Usage)
	}
	if ev[0].Usage.CachedInput != 8448 {
		t.Errorf("cached input is worth showing: %+v", ev[0].Usage)
	}
}

func TestCodexRawLinesSurvive(t *testing.T) {
	// exec writes its own progress and failures to stderr, and the loop merges
	// both streams. A signed-out CLI says so this way.
	ev := parseCodex(t, "stream error: not logged in")
	if len(ev) != 1 || ev[0].Type != "raw" {
		t.Fatalf("got %+v", ev)
	}
}

func codexRun(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "last-run.out")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A plan limit is the codex equivalent of a spend cap firing: the agent did
// nothing wrong, and retrying unchanged walks into the same wall. It is the one
// outcome the loop acts on, so it has to be classified rather than reported as
// an ordinary failure.
func TestCodexClassifiesAPlanLimitAsBudget(t *testing.T) {
	rc := &RunContext{Worker: &Worker{}}
	outcome, expl := (&codexRuntime{}).ClassifyExit(rc, 1, codexRun(t, cxThread, cxFailed))
	if outcome != outcomeBudget {
		t.Fatalf("outcome = %q, want %q", outcome, outcomeBudget)
	}
	for _, want := range []string{"PLAN LIMIT", "max_runs_per_hour"} {
		if !strings.Contains(expl, want) {
			t.Errorf("explanation missing %q:\n%s", want, expl)
		}
	}
	// There is no per-run cap to raise, and saying there is would send someone
	// looking for a setting that does not exist.
	if strings.Contains(expl, "max_usd_per_run") {
		t.Errorf("codex has no spend cap to name:\n%s", expl)
	}
}

// A run is classified by what FAILED, not by whatever the agent happened to
// write. An assistant turn quoting "rate limit" is a sentence about code; reading
// it as a plan limit would pause a healthy worker after two of them.
func TestCodexDoesNotClassifyOnTheAgentsOwnWords(t *testing.T) {
	chatter := `{"type":"item.completed","item":{"item_type":"agent_message","text":"The handler returns 429 when the rate limit is hit."}}`
	outcome, expl := (&codexRuntime{}).ClassifyExit(
		&RunContext{Worker: &Worker{}}, 1, codexRun(t, cxThread, chatter, cxDone))
	if outcome != outcomeError {
		t.Errorf("outcome = %q, want %q — nothing failed here", outcome, outcomeError)
	}
	if expl != "" {
		t.Errorf("nothing to explain:\n%s", expl)
	}
}

func TestCodexClassifiesSignedOutAndRelayFailures(t *testing.T) {
	rt := &codexRuntime{}
	rc := &RunContext{Worker: &Worker{}}

	_, expl := rt.ClassifyExit(rc, 1, codexRun(t, "stream error: not logged in; run `codex login`"))
	if !strings.Contains(expl, "NOT SIGNED IN") {
		t.Errorf("a signed-out CLI fails every cycle the same way:\n%s", expl)
	}

	_, expl = rt.ClassifyExit(rc, 1, codexRun(t, "ERROR: failed to start MCP server `relay`"))
	if !strings.Contains(expl, "RELAY MCP SERVER") {
		t.Errorf("a session with no relay tools looks healthy and does nothing:\n%s", expl)
	}
}

// exec echoes its own header to stderr, prompt included — and the prompt names
// relay in every other line. A classifier matching the word would report an MCP
// failure for every run that exited non-zero.
func TestCodexDoesNotBlameRelayForTheEchoedPrompt(t *testing.T) {
	header := "workdir: /repo\nmodel: gpt-5.1-codex\nprompt: Poll relay for one available task, claim it, and work it to completion."
	_, expl := (&codexRuntime{}).ClassifyExit(
		&RunContext{Worker: &Worker{}}, 1, codexRun(t, header, cxThread, cxMsg))
	if strings.Contains(expl, "RELAY MCP SERVER") {
		t.Errorf("the echoed prompt is not an MCP failure:\n%s", expl)
	}
}

func TestCodexClassifiesPlainExits(t *testing.T) {
	out := codexRun(t, cxThread, cxMsg, cxDone)
	rc := &RunContext{Worker: &Worker{}}
	for status, want := range map[int]string{0: outcomeOK, 124: outcomeTimeout, 1: outcomeError} {
		if got, _ := (&codexRuntime{}).ClassifyExit(rc, status, out); got != want {
			t.Errorf("status %d → %q, want %q", status, got, want)
		}
	}
}

func codexContext(t *testing.T, rcfg map[string]string) *RunContext {
	t.Helper()
	dir := t.TempDir()
	return &RunContext{
		Worker: &Worker{Name: "w", Endpoint: "https://r.example/relay/mcp/c/wzh_secretvalue",
			RuntimeConfig: rcfg},
		WorkerDir: dir, RepoDir: dir,
		Prompt: "Poll relay for one available task.", Rules: "THE HARNESS CONTRACT",
		AllowTools: relayAllowedTools,
	}
}

func TestCodexBuildCmdStreamsAndIsolates(t *testing.T) {
	rc := codexContext(t, map[string]string{
		"model": "gpt-5.1-codex", "reasoning_effort": "high",
		"sandbox": "workspace-write", "network_access": "true", "web_search": "true",
	})
	argv, err := (&codexRuntime{}).BuildCmd(rc)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"codex exec", "--json", "--ignore-user-config", "--skip-git-repo-check",
		"--ephemeral", "--model gpt-5.1-codex", "--sandbox workspace-write",
		"model_reasoning_effort=high", "sandbox_workspace_write.network_access=true",
		"web_search=live", "approval_policy=never",
		"mcp_servers.relay.url=https://r.example/relay/mcp/c/wzh_secretvalue",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q:\n%s", want, joined)
		}
	}

	// codex exec has no --append-system-prompt, and a contract that does not
	// arrive is a worker that claims two tasks in a session and tells nobody.
	last := argv[len(argv)-1]
	if !strings.HasPrefix(last, "THE HARNESS CONTRACT") || !strings.Contains(last, "Poll relay") {
		t.Errorf("the prompt must carry the harness contract:\n%s", last)
	}
	// Nothing in a prompt may be read as a flag.
	if argv[len(argv)-2] != "--" {
		t.Errorf("the prompt has to be separated from the flags: %v", argv[len(argv)-3:])
	}
}

func TestCodexBuildCmdHonoursTheBoundedSettings(t *testing.T) {
	rc := codexContext(t, map[string]string{
		"model": "gpt-5.1-codex", "reasoning_effort": "low",
		"sandbox": "read-only", "network_access": "false", "web_search": "false",
	})
	argv, err := (&codexRuntime{}).BuildCmd(rc)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"--sandbox read-only", "sandbox_workspace_write.network_access=false", "web_search=disabled",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q:\n%s", want, joined)
		}
	}
}

// The workdir line answers "did what I wrote land?" before the first run, and it
// has to look where codex looks — not where claude does.
func TestCodexInspectWorkdirReadsCodexLayout(t *testing.T) {
	dir := t.TempDir()
	if got := (&codexRuntime{}).InspectWorkdir(dir); !strings.Contains(got, "nothing to load") {
		t.Errorf("an empty directory is a valid setup: %q", got)
	}

	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("rules"), 0o644)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("not codex's"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".codex", "agents"), 0o755)
	os.WriteFile(filepath.Join(dir, ".codex", "agents", "reviewer.toml"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte(""), 0o644)

	got := (&codexRuntime{}).InspectWorkdir(dir)
	for _, want := range []string{"AGENTS.md", "1 agent", ".codex/config.toml"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	if strings.Contains(got, "CLAUDE.md") {
		t.Errorf("codex does not load CLAUDE.md, so reporting it would be a lie: %q", got)
	}
}
