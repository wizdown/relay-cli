// Runtime adapter: Anthropic Claude Code CLI (headless `claude -p`), native.
//
// One thing differs from the bash adapter it replaces, and it is the reason
// relay-cli exists: the output format. `--output-format json` prints ONE
// object at the very end of a run, so a fifteen-minute session showed nothing at
// all until it finished. This uses `--output-format stream-json --verbose`,
// which emits one JSON object per event as it happens — session init, each
// assistant turn, each tool call with its argument, each tool result, then the
// same final result envelope as before.
//
// The classifier is unaffected by that change: the result event still arrives,
// last, carrying terminal_reason and permission_denials.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// claudeRuntime adapts the Claude Code CLI. It contains no part of that CLI:
// `claude` is a separate program the operator installs, found on PATH at
// startup. What lives here is only the knowledge of how to invoke it and how to
// read what it prints.
type claudeRuntime struct {
	once    sync.Once
	err     error
	path    string
	version string
}

// One instance, so the capability probe below runs once per process rather than
// once per worker that names this runtime.
var claudeAdapter = &claudeRuntime{}

func (c *claudeRuntime) Name() string { return "claude" }

// ConfigFields — what a worker may set in "runtime_config" when its runtime is
// claude.
//
// model is REQUIRED, and deliberately so. The CLI has its own default and
// tracking it would be one less thing to write, but that default is not stable
// across CLI versions: an unchanged config would quietly change both what a
// worker costs and how it behaves the next time someone upgraded claude. For an
// unattended process that spends money on its own, the config has to say what it
// will actually run.
func (c *claudeRuntime) ConfigFields() []runtimeField {
	return []runtimeField{
		{
			Key: "model", Kind: fieldString, Required: true,
			Doc: "which model to run: opus, sonnet or haiku, or a full id like claude-opus-5 to pin one",
		},
		{
			Key: "max_usd_per_run", Kind: fieldNumber, Default: "5",
			Doc: "hard dollar cap INSIDE one run, enforced by the CLI. 0 removes it",
		},
	}
}

// requiredFlags is what this adapter actually depends on, each with the reason
// it is not optional. Checked against the installed CLI's own --help, because
// that tests the real requirement: a version number would only be a proxy for
// it, and one this adapter would have to guess the mapping for.
var requiredClaudeFlags = []struct{ flag, why string }{
	{"--print", "running headless, with no terminal to answer prompts"},
	{"--output-format", "asking for stream-json instead of one blob at the end"},
	{"--verbose", "in print mode the CLI only streams with it"},
	{"--append-system-prompt", "delivering the relay runtime contract"},
	{"--mcp-config", "pointing the session at this worker's relay connector"},
	{"--strict-mcp-config", "keeping the operator's personal MCP servers out of an unattended run"},
	{"--allowedTools", "pre-allowing relay's tools, since a headless run cannot approve one"},
	{"--permission-mode", "running fully autonomously"},
	{"--max-budget-usd", "the per-run spend cap"},
	{"--name", "labelling the session so it is identifiable in Claude Code's own history"},
}

// Check verifies that the CLI is installed AND that this build of it accepts
// every flag the adapter uses.
//
// The second half matters as much as the first. Without it, a CLI missing one
// flag fails inside a background log, once per cycle, after the worker has
// already launched — spending a run's worth of setup to rediscover the same
// thing every time. Here it is one line, once, before anything starts.
func (c *claudeRuntime) Check() error {
	c.once.Do(c.probe)
	return c.err
}

// Version is the installed CLI's version, for diagnostics. Empty if unknown.
func (c *claudeRuntime) Version() string { return c.version }

// Path is where the CLI was found, so "which claude did it use?" has an answer.
func (c *claudeRuntime) Path() string { return c.path }

func (c *claudeRuntime) probe() {
	path, err := exec.LookPath("claude")
	if err != nil {
		c.err = fmt.Errorf("claude not found on PATH — install Claude Code (https://claude.com/claude-code).\n" +
			"       relay-cli does not bundle any CLI; each one is installed separately.")
		return
	}
	c.path = path
	c.version = claudeVersion()

	if os.Getenv("RELAY_CLI_SKIP_RUNTIME_CHECK") != "" {
		return
	}

	help, err := exec.Command("claude", "--help").CombinedOutput()
	if err != nil {
		// Unverifiable is not the same as unusable. A CLI whose --help cannot be
		// read might still run perfectly, and refusing to start over that would be
		// this check causing the outage it exists to prevent.
		fmt.Fprintf(os.Stderr, "warning: could not run `claude --help` to verify this install supports the flags relay-cli needs (%v).\n"+
			"         Continuing anyway; a missing flag will surface on the first run.\n", err)
		return
	}

	missing := missingClaudeFlags(help)
	if len(missing) > 0 {
		c.err = fmt.Errorf("the installed claude (%s at %s) does not support what relay-cli needs.\n"+
			"       Its --help does not offer:\n%s\n"+
			"       Upgrade Claude Code (https://claude.com/claude-code), then try again.\n"+
			"       To run anyway, set RELAY_CLI_SKIP_RUNTIME_CHECK=1 — each session will\n"+
			"       then fail on the missing flag instead of failing here, once.",
			orDefault(c.version, "version unknown"), path, strings.Join(missing, "\n"))
	}
}

// missingClaudeFlags reports which of this adapter's requirements the installed
// CLI's --help does not mention, already formatted for the error message.
func missingClaudeFlags(help []byte) []string {
	var missing []string
	for _, r := range requiredClaudeFlags {
		if !bytes.Contains(help, []byte(r.flag)) {
			missing = append(missing, fmt.Sprintf("         %-28s %s", r.flag, r.why))
		}
	}
	// The VALUE matters as much as the flag: --output-format long predates
	// stream-json being one of its choices, and the streaming is what this whole
	// binary is for. A CLI with the flag but not the value would pass a
	// flag-only check and then produce no live feed at all.
	if bytes.Contains(help, []byte("--output-format")) && !bytes.Contains(help, []byte("stream-json")) {
		missing = append(missing, fmt.Sprintf("         %-28s %s", "--output-format stream-json", "the live session feed — the reason relay-cli exists"))
	}
	return missing
}

// claudeVersion is best effort: it is shown in the startup banner and in error
// messages, and never gated on. The CLI prints e.g. "2.1.250 (Claude Code)".
func claudeVersion() string {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// InspectWorkdir reports what a session started in dir would load from it, for
// the one line `relay check` prints under each worker.
//
// It exists because the working directory is the half of a worker's setup
// nothing else can confirm. A config typo is caught when the config loads and a
// dead credential is caught by the probe, but a CLAUDE.md that was written one
// directory up, or a skill whose file is named skill.md, produces a worker that
// starts, runs, costs money and quietly knows none of it. This turns "did what I
// wrote land?" into something the tool answers before the first run.
//
// Nothing here is a requirement: an empty directory is a valid setup, so the
// empty case reports what the agent still arrives with rather than a warning.
// Every read is a stat or a small file, and none of it is fatal — this is a
// description, never a validation.
func (c *claudeRuntime) InspectWorkdir(dir string) string {
	var parts []string

	if hasFileNamed(dir, "CLAUDE.md") {
		parts = append(parts, "CLAUDE.md")
	}
	if n := countSkills(filepath.Join(dir, ".claude", "skills")); n > 0 {
		parts = append(parts, plural(n, "skill", "skills"))
	}
	if n := countFilesWithSuffix(filepath.Join(dir, ".claude", "agents"), ".md"); n > 0 {
		parts = append(parts, plural(n, "subagent", "subagents"))
	}
	if s := describeSettings(filepath.Join(dir, ".claude", "settings.json")); s != "" {
		parts = append(parts, s)
	}

	if len(parts) == 0 {
		return "nothing to load — the agent arrives with its task and its tools"
	}
	return strings.Join(parts, " · ")
}

// countSkills counts <dir>/<name>/SKILL.md, which is the only shape the CLI
// loads. A directory holding a "skill.md" or a bare README is not a skill, and
// counting it would be the reassurance this line exists to avoid giving.
func countSkills(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if hasFileNamed(filepath.Join(dir, e.Name()), "SKILL.md") {
			n++
		}
	}
	return n
}

// hasFileNamed matches the name exactly, which os.Stat does not: macOS and
// Windows are case-insensitive, so a Stat for SKILL.md happily finds skill.md —
// a file the CLI itself will not load. Reporting it would mean this line
// disagrees with the run on the one machine most operators are using.
func hasFileNamed(dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == name && !e.IsDir() {
			return true
		}
	}
	return false
}

// countFilesWithSuffix counts the plain files in dir with a given extension —
// claude's subagents are .md, codex's agents are .toml. Shared because the
// question is the same one; what differs is only where each CLI looks.
func countFilesWithSuffix(dir, suffix string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			n++
		}
	}
	return n
}

// describeSettings reports the part of a settings file that changes a run.
//
// Hooks are called out by count because they execute, unattended, on every
// session — the one thing in that file worth seeing before a fleet starts. A
// file that does not parse is named as such rather than skipped: the CLI ignores
// an invalid settings file silently in headless mode, so a typo there is
// otherwise invisible.
func describeSettings(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []json.RawMessage `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return "settings.json (does not parse — the CLI ignores it silently)"
	}
	n := 0
	for _, matchers := range s.Hooks {
		for _, m := range matchers {
			n += len(m.Hooks)
		}
	}
	if n > 0 {
		return plural(n, "hook", "hooks")
	}
	return "settings.json"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func (c *claudeRuntime) BuildCmd(rc *RunContext) ([]string, error) {
	// The connector URL is a secret, so the generated MCP config lives in the
	// worker's state dir (gitignored), never in the repo the worker is editing.
	mcpPath := rc.WorkerDir + "/mcp.json"
	cfg, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"relay": map[string]any{"type": "http", "url": rc.Worker.Endpoint},
		},
	})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(mcpPath, cfg, 0o600); err != nil {
		return nil, err
	}

	// Only the runtime contract rides here. WHO this agent is — its house rules,
	// its branch naming, what it may decide alone — is relay's instructions_md,
	// delivered on the agent's own MCP surface and repeated in every
	// get_task_context, so an owner's edit reaches a session already running.
	argv := []string{"claude", "-p", rc.Prompt,
		// This layers ON TOP of Claude Code's own system prompt — it replaces
		// nothing, so the model keeps its normal engineering behaviour.
		"--append-system-prompt", rc.Rules,
		"--mcp-config", mcpPath,
		// An unattended run gets relay and nothing else: whatever MCP servers the
		// human's personal config carries are not in scope here.
		"--strict-mcp-config",
		// Headless mode can never answer an approval prompt, so a tool that is
		// not pre-allowed is silently denied. Naming every relay tool explicitly
		// means relay access never rides on the model's judgement about an
		// unfamiliar tool.
		"--allowedTools", rc.AllowTools,
		// The live feed. --verbose is not optional here: in print mode the CLI
		// only streams with it.
		"--output-format", "stream-json", "--verbose",
		"--name", rc.Worker.Name,
	}

	// `auto` is the only mode a headless run can work in — anything stricter
	// silently denies whatever was not pre-allowed and the session stalls with no
	// prompt to answer. So it is not a config field; it is what this harness IS,
	// and there is no longer any way to talk it out of it.
	argv = append(argv, "--permission-mode", "auto")
	argv = append(argv, "--model", rc.Worker.RCString("model"))
	// 0 is the operator deliberately removing the cap, and is spelled by omitting
	// the flag rather than by passing --max-budget-usd 0.
	if cap := rc.Worker.RCFloat("max_usd_per_run"); cap > 0 {
		argv = append(argv, "--max-budget-usd", strconv.FormatFloat(cap, 'f', -1, 64))
	}
	return argv, nil
}

// ── stream parsing ───────────────────────────────────────────────────────────

// claudeLine is the subset of the stream schema this adapter reads. Every field
// the CLI emits that is not here is ignored by construction, so a new event type
// in a future version is silently skipped rather than breaking the feed.
type claudeLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`

	// system/init
	Model      string `json:"model"`
	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`

	// system/task_summary
	Detail *string `json:"detail"`

	// assistant / user
	Message struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	// result
	TotalCostUSD float64 `json:"total_cost_usd"`
	NumTurns     int     `json:"num_turns"`
	Result       string  `json:"result"`
	IsError      bool    `json:"is_error"`
}

type claudeBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Content  json.RawMessage `json:"content"`
	IsError  bool            `json:"is_error"`
}

// ParseLine turns one stream line into the events worth showing.
//
// Most of what the CLI emits is not: thinking_tokens ticks arrive several times
// a second and say nothing a human wants, and rate_limit_event repeats unchanged
// all run. Filtering them here rather than in the UI keeps them out of the ring
// buffer and off the disk record too.
func (c *claudeRuntime) ParseLine(line string) []SessionEvent {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if !strings.HasPrefix(line, "{") {
		// Not stream JSON: the CLI's own stderr, or a wrapper's noise. Still worth
		// showing — an authentication failure arrives this way.
		return []SessionEvent{{Type: "raw", Text: line}}
	}
	var l claudeLine
	if json.Unmarshal([]byte(line), &l) != nil {
		return []SessionEvent{{Type: "raw", Text: line}}
	}

	switch l.Type {
	case "system":
		switch l.Subtype {
		case "init":
			ev := SessionEvent{Type: "init", SessionID: l.SessionID, Model: l.Model}
			// Which MCP servers came up is the single most useful thing in this
			// event. A relay that is `needs-auth` or `failed` produces a run that
			// looks healthy, does nothing, and explains itself only in prose at the
			// very end — the exact failure sitting in this repo's archived logs.
			var parts []string
			for _, s := range l.MCPServers {
				parts = append(parts, s.Name+": "+s.Status)
			}
			ev.Text = strings.Join(parts, ", ")
			return []SessionEvent{ev}
		case "task_summary":
			if l.Detail != nil && *l.Detail != "" {
				return []SessionEvent{{Type: "status", Text: *l.Detail}}
			}
		}
		return nil

	case "assistant":
		return blocksToEvents(l.Message.Content)

	case "user":
		return blocksToEvents(l.Message.Content)

	case "result":
		return []SessionEvent{{
			Type:      "result",
			SessionID: l.SessionID,
			CostUSD:   l.TotalCostUSD,
			NumTurns:  l.NumTurns,
			IsError:   l.IsError,
			Text:      l.Result,
		}}
	}
	return nil
}

func blocksToEvents(raw json.RawMessage) []SessionEvent {
	if len(raw) == 0 {
		return nil
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []SessionEvent
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				out = append(out, SessionEvent{Type: "assistant", Text: b.Text})
			}
		case "thinking":
			if strings.TrimSpace(b.Thinking) != "" {
				out = append(out, SessionEvent{Type: "thinking", Text: b.Thinking})
			}
		case "tool_use":
			out = append(out, SessionEvent{Type: "tool_use", Tool: b.Name, Target: toolTarget(b.Input)})
		case "tool_result":
			out = append(out, SessionEvent{Type: "tool_result", Text: flattenContent(b.Content), IsError: b.IsError})
		}
	}
	return out
}

// toolTarget picks the one argument that says what a tool call is DOING. A tool
// call rendered as its name alone ("Edit", "mcp__relay__claim_task") is nearly
// content-free; with its target it becomes a readable narration of the run.
func toolTarget(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	// Bare-value keys: the value alone reads better than "file_path=…".
	for _, k := range []string{"file_path", "path", "command", "url", "notebook_path"} {
		if v, ok := in[k]; ok {
			if s := scalar(v); s != "" {
				return s
			}
		}
	}
	// Labelled keys: the value needs its name to mean anything.
	for _, k := range []string{"task_id", "subtask_id", "agent_id", "id", "pattern", "query", "description", "title", "prompt"} {
		if v, ok := in[k]; ok {
			if s := scalar(v); s != "" {
				return k + "=" + s
			}
		}
	}
	return ""
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return oneLine(t, 300)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

// flattenContent renders a tool result, which the CLI sends either as a plain
// string or as a list of content blocks.
func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

// ── exit classification ──────────────────────────────────────────────────────

// ClassifyExit reads the run's own result envelope, which carries exactly what
// the loop cannot know on its own. Two fields are worth acting on:
//
//	terminal_reason: "budget_exhausted"  the run hit --max-budget-usd.
//	  Distinguishing this from ordinary failure matters because the remedy is the
//	  opposite one — the agent did nothing wrong, and retrying unchanged just
//	  spends the cap again.
//
//	permission_denials  tools the run tried to call and was refused, which is the
//	  signature of an allowlist that has fallen behind relay's agent surface. It
//	  is silent by construction (a headless run has no prompt to fail), so
//	  surfacing it is the difference between a five-minute fix and an afternoon
//	  wondering why an orchestrator does nothing.
func (c *claudeRuntime) ClassifyExit(rc *RunContext, status int, outPath string) (string, string) {
	outcome := defaultOutcome(status)

	fh, err := os.Open(outPath)
	if err != nil {
		return outcome, ""
	}
	defer fh.Close()

	// The result envelope is the last line of the stream carrying type "result".
	var last string
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"type":"result"`) {
			last = sc.Text()
		}
	}
	if last == "" {
		return outcome, ""
	}

	var res struct {
		TerminalReason   string  `json:"terminal_reason"`
		TotalCostUSD     float64 `json:"total_cost_usd"`
		PermissionDenial []struct {
			ToolName string `json:"tool_name"`
			Alt      string `json:"toolName"`
		} `json:"permission_denials"`
	}
	if json.Unmarshal([]byte(last), &res) != nil {
		return outcome, ""
	}

	var explanation string
	seen := map[string]bool{}
	var denied []string
	for _, d := range res.PermissionDenial {
		name := d.ToolName
		if name == "" {
			name = d.Alt
		}
		if name != "" && !seen[name] {
			seen[name] = true
			denied = append(denied, name)
		}
	}
	if len(denied) > 0 {
		explanation = fmt.Sprintf(`the CLI refused these tool calls: %s
  Anything here means this worker's allowlist is behind what the session can
  reach (workerAllowedTools in runtime.go) — a relay tool means it is behind
  relay's agent surface. The agent could see the tool and called it; its own
  CLI, not relay, said no.`, strings.Join(denied, ", "))
	}

	if res.TerminalReason == "budget_exhausted" {
		cap := rc.Worker.RCString("max_usd_per_run")
		if cap == "" {
			cap = "?"
		}
		return outcomeBudget, fmt.Sprintf(`RUN KILLED BY ITS SPEND CAP — it stopped mid-task at $%.4f of a $%s max_usd_per_run.
  Nothing was wrong with the agent or the task: the session was cut off partway.
  It did not finish, and it did not hand the task back, so relay still shows the
  task held until the claim's lease lapses and re-offers it.
  Left alone, the next run starts the same task from its Task Context and walks
  into the same wall at the same point. Either raise max_usd_per_run for this
  worker in %s, or split the task into smaller ones in relay.`, res.TotalCostUSD, cap, displayConfigPath())
	}

	return outcome, explanation
}
