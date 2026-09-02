// Runtime adapter: OpenAI Codex CLI (headless `codex exec`), native.
//
// Same shape as the claude adapter, and native for the same reason: `codex exec
// --json` prints one JSON object per event as it happens, and turning those
// into live session events is what relay-cli is for. A bash adapter could build
// this argv, but it would contribute an argv and nothing else — no stream, and
// no declared config keys, so not even a model.
//
// Two things differ from claude, and both are user-visible enough that the docs
// say them out loud rather than leaving them to be discovered:
//
//	No per-run spend cap. codex has no --max-budget-usd, and its token usage
//	arrives only in the final turn.completed — too late to kill a run on. A codex
//	worker is bounded by max_seconds_per_run and max_runs_per_hour, and by the
//	plan limits of the account it signs in as. Nothing here pretends otherwise.
//
//	The connector URL is passed as a -c override, so it is visible in `ps` to
//	anyone with an account on this machine. claude gets a 0600 file instead. The
//	alternative for codex is a private CODEX_HOME, and that breaks a ChatGPT
//	sign-in: auth.json lives under CODEX_HOME, and its refresh tokens are
//	single-use, so a copy is invalidated the moment either side refreshes.
//	Working sign-in beat the smaller exposure; see docs/runtimes.md.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// codexRuntime adapts the Codex CLI. It contains no part of that CLI: `codex`
// is a separate program the operator installs and signs in to, found on PATH at
// startup.
type codexRuntime struct {
	once    sync.Once
	err     error
	path    string
	version string
}

// One instance, so the capability probe below runs once per process rather than
// once per worker that names this runtime.
var codexAdapter = &codexRuntime{}

func (c *codexRuntime) Name() string { return "codex" }

// ConfigFields — what a worker may set in "runtime_config" when its runtime is
// codex.
//
// model is REQUIRED for the same reason it is for claude: the CLI's own default
// moves between versions, and an unattended process that spends money should say
// what it runs.
//
// The rest are the settings that change what a run costs or what it may touch.
// Everything else about a codex run — that it is non-interactive, that it never
// asks for approval, that it loads relay and not the operator's own MCP servers
// — is what this harness IS, not something to configure.
func (c *codexRuntime) ConfigFields() []runtimeField {
	return []runtimeField{
		{
			Key: "model", Kind: fieldString, Required: true,
			Doc: "which model to run, passed to the CLI verbatim — `codex --help` is the authority on what it accepts",
		},
		{
			Key: "reasoning_effort", Kind: fieldString, Default: "medium",
			Enum: []string{"minimal", "low", "medium", "high", "xhigh"},
			Doc:  "how hard the model thinks before acting. The biggest dial on what a run costs, and codex has no spend cap",
		},
		{
			Key: "sandbox", Kind: fieldString, Default: "workspace-write",
			Enum: []string{"read-only", "workspace-write", "danger-full-access"},
			Doc:  "what a run may write: read-only, workspace-write (repo_dir and temp files), or danger-full-access",
		},
		{
			Key: "network_access", Kind: fieldBool, Default: "true",
			Doc: "whether commands the agent runs may reach the network — off, git push and dependency installs fail",
		},
		{
			Key: "web_search", Kind: fieldBool, Default: "true",
			Doc: "whether the agent may search the web, as a claude worker can",
		},
	}
}

// requiredCodexFlags is what this adapter actually depends on, each with the
// reason it is not optional. Checked against the installed CLI's own --help for
// the same reason the claude adapter is: it tests the real requirement rather
// than a version number this adapter would have to guess the mapping for.
var requiredCodexFlags = []struct{ flag, why string }{
	{"--json", "the live session feed — the reason relay-cli exists"},
	{"--model", "running the model the config names rather than the CLI's default"},
	{"--sandbox", "bounding what an unattended run may write"},
	{"--skip-git-repo-check", "repo_dir does not have to be a git checkout"},
	{"--ignore-user-config", "keeping the operator's personal MCP servers and settings out of an unattended run"},
	{"--ephemeral", "not leaving a session recording behind for every cycle, all day"},
	{"--config", "delivering this worker's relay connector, its reasoning effort and its sandbox"},
}

// Check verifies that the CLI is installed, that this build accepts every flag
// the adapter uses, and that it is signed in.
//
// The sign-in check is the one thing this adapter can do that the claude one
// cannot. `codex login status` reads a local file and spends nothing, so the
// prerequisite that used to be untestable — "did you sign the CLI in?" — is
// answered by `relay check`, before a fleet starts and fails every cycle.
func (c *codexRuntime) Check() error {
	c.once.Do(c.probe)
	return c.err
}

// Version is the installed CLI's version, for diagnostics. Empty if unknown.
func (c *codexRuntime) Version() string { return c.version }

// Path is where the CLI was found, so "which codex did it use?" has an answer.
func (c *codexRuntime) Path() string { return c.path }

func (c *codexRuntime) probe() {
	path, err := exec.LookPath("codex")
	if err != nil {
		c.err = fmt.Errorf("codex not found on PATH — install the Codex CLI (https://developers.openai.com/codex/cli).\n" +
			"       relay-cli does not bundle any CLI; each one is installed separately.")
		return
	}
	c.path = path
	c.version = codexVersion()

	if os.Getenv("RELAY_CLI_SKIP_RUNTIME_CHECK") != "" {
		return
	}

	help, err := exec.Command("codex", "exec", "--help").CombinedOutput()
	if err != nil {
		// Unverifiable is not the same as unusable. A CLI whose --help cannot be
		// read might still run perfectly, and refusing to start over that would be
		// this check causing the outage it exists to prevent.
		fmt.Fprintf(os.Stderr, "warning: could not run `codex exec --help` to verify this install supports the flags relay-cli needs (%v).\n"+
			"         Continuing anyway; a missing flag will surface on the first run.\n", err)
		return
	}
	// Both help surfaces, because codex splits its options between them: a flag
	// this adapter passes to `exec` may be documented on the global help instead,
	// and refusing to start over where a CLI prints a flag would be this check
	// causing the outage it exists to prevent.
	if global, err := exec.Command("codex", "--help").CombinedOutput(); err == nil {
		help = append(help, global...)
	}

	if missing := missingCodexFlags(help); len(missing) > 0 {
		c.err = fmt.Errorf("the installed codex (%s at %s) does not support what relay-cli needs.\n"+
			"       Its --help does not offer:\n%s\n"+
			"       Upgrade the Codex CLI (https://developers.openai.com/codex/cli), then try again.\n"+
			"       To run anyway, set RELAY_CLI_SKIP_RUNTIME_CHECK=1 — each session will\n"+
			"       then fail on the missing flag instead of failing here, once.",
			orDefault(c.version, "version unknown"), path, strings.Join(missing, "\n"))
		return
	}

	c.err = codexLoginError()
}

// missingCodexFlags reports which of this adapter's requirements the installed
// CLI's help does not mention, already formatted for the error message.
func missingCodexFlags(help []byte) []string {
	var missing []string
	for _, r := range requiredCodexFlags {
		if !bytes.Contains(help, []byte(r.flag)) {
			missing = append(missing, fmt.Sprintf("         %-24s %s", r.flag, r.why))
		}
	}
	return missing
}

// codexLoginError asks the CLI whether it is signed in.
//
// A worker launches codex as the operator, so it authenticates the way their own
// sessions do. Signed out, every cycle starts a session that fails immediately
// and costs the setup — so a fleet with a signed-out codex does not start at
// all, on one local file read.
//
// Three answers, not two. Signed in passes; signed out fails the start; and
// "cannot tell" — an older build with no `login status`, or one that will not
// run here — warns and continues, because unverifiable is not the same as
// unusable.
func codexLoginError() error {
	out, err := exec.Command("codex", "login", "status").CombinedOutput()
	if err == nil {
		return nil
	}
	if _, ok := err.(*exec.ExitError); !ok {
		warnUnverifiedSignIn("codex", err.Error())
		return nil
	}
	// `login status` reports the CACHED sign-in only, and codex says outright
	// that a CODEX_API_KEY passed to exec never becomes one. A key in the
	// environment authenticates every run perfectly well, so refusing to start
	// over the cache being empty would be this check causing the outage it exists
	// to prevent.
	if envCredentialSet("CODEX_API_KEY", "OPENAI_API_KEY") {
		return nil
	}
	return fmt.Errorf("the installed codex is not signed in (%s).\n"+
		"       Run `codex login` once as this user and sign in with your ChatGPT\n"+
		"       account; workers then run as you, with no API key to configure.\n"+
		"       relay-cli never writes or moves those credentials.",
		oneLine(strings.TrimSpace(string(out)), 120))
}

// codexVersion is best effort: it is shown in the startup banner and in error
// messages, and never gated on.
func codexVersion() string {
	out, err := exec.Command("codex", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// InspectWorkdir reports what a codex session started in dir would load from it,
// for the one line `relay check` prints under each worker. Same job as the
// claude adapter's, in codex's own layout — which is the reason this is a
// per-runtime method rather than one shared directory walk.
func (c *codexRuntime) InspectWorkdir(dir string) string {
	var parts []string

	if hasFileNamed(dir, "AGENTS.md") {
		parts = append(parts, "AGENTS.md")
	}
	if n := countFilesWithSuffix(filepath.Join(dir, ".codex", "agents"), ".toml"); n > 0 {
		parts = append(parts, plural(n, "agent", "agents"))
	}
	// Called out because it is the one thing here that is NOT the operator's own
	// choice made twice: a run is launched with --ignore-user-config, which drops
	// ~/.codex/config.toml, and a project config in the checkout still applies.
	if hasFileNamed(filepath.Join(dir, ".codex"), "config.toml") {
		parts = append(parts, ".codex/config.toml (project config — still applied)")
	}

	if len(parts) == 0 {
		return "nothing to load — the agent arrives with its task and its tools"
	}
	return strings.Join(parts, " · ")
}

func (c *codexRuntime) BuildCmd(rc *RunContext) ([]string, error) {
	argv := []string{"codex", "exec",
		// The live feed, one JSON object per event.
		"--json",
		// repo_dir is a checkout the operator chose; it does not have to be a git
		// one, and a worker refusing to start over that would be relay-cli
		// deciding something it does not own.
		"--skip-git-repo-check",
		// The --strict-mcp-config of this CLI: ~/.codex/config.toml is not read,
		// so an unattended run gets relay and nothing else — not the operator's
		// personal MCP servers, not their own model or sandbox defaults.
		"--ignore-user-config",
		// One task per session, and the record already lives in ~/.relay/logs.
		// Without this a fleet leaves a session recording behind every cycle, all
		// day, in a directory nothing here prunes.
		"--ephemeral",
		"--model", rc.Worker.RCString("model"),
		"--sandbox", rc.Worker.RCString("sandbox"),
	}

	for _, kv := range codexConfigOverrides(rc) {
		argv = append(argv, "--config", kv)
	}

	// The harness contract rides in the prompt rather than in a system prompt.
	// `codex exec` has no --append-system-prompt, and its config key for the same
	// job has not reliably applied in non-interactive runs — a contract that
	// silently does not arrive is a worker that claims two tasks in a session and
	// tells nobody. The one channel exec guarantees is the prompt itself.
	//
	// `--` first: a prompt begins with the contract, and nothing in it may be
	// read as a flag.
	return append(argv, "--", rc.Rules+"\n\n---\n\n"+rc.Prompt), nil
}

// codexConfigOverrides is everything this adapter sets through -c, which is
// where codex keeps what claude spells as flags.
//
// Values are passed unquoted. Codex parses a -c value as TOML and falls back to
// treating it as a string, so `true` arrives as a boolean and a URL — which is
// not valid TOML — arrives as the string it is. Quoting the URL would be the way
// to get quotes into it on a build that does not parse it.
func codexConfigOverrides(rc *RunContext) []string {
	kv := []string{
		// A headless run has no terminal to approve anything on. exec forces this
		// itself; setting it means the argv says what the run is rather than
		// relying on that.
		"approval_policy=never",
		"model_reasoning_effort=" + rc.Worker.RCString("reasoning_effort"),
		"sandbox_workspace_write.network_access=" + strconv.FormatBool(rc.Worker.RCBool("network_access")),
		// The relay connector. This is the credential, and it is why this argv
		// never reaches a log unscrubbed.
		"mcp_servers.relay.url=" + rc.Worker.Endpoint,
		// Streamable-HTTP MCP servers were gated behind an experiment that has
		// since moved under [features]. Both spellings are sent because an
		// override codex does not know is ignored, while a missing one is a
		// worker that starts, runs, reaches no relay tools and blames the model.
		"experimental_use_rmcp_client=true",
		"features.rmcp_client=true",
	}
	if rc.Worker.RCBool("web_search") {
		kv = append(kv, "web_search=live")
	} else {
		kv = append(kv, "web_search=disabled")
	}
	return kv
}

// ── stream parsing ───────────────────────────────────────────────────────────

// codexLine is the subset of the exec event schema this adapter reads. Anything
// the CLI emits that is not here is ignored by construction, so a new event type
// in a future version is skipped rather than breaking the feed.
type codexLine struct {
	Type     string     `json:"type"`
	ThreadID string     `json:"thread_id"`
	Item     *codexItem `json:"item"`
	Usage    *struct {
		InputTokens       int `json:"input_tokens"`
		CachedInputTokens int `json:"cached_input_tokens"`
		OutputTokens      int `json:"output_tokens"`
	} `json:"usage"`
	// turn.failed carries a nested error; the top-level `error` event carries a
	// bare message.
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Message string `json:"message"`
}

type codexItem struct {
	ID   string `json:"id"`
	Type string `json:"item_type"`
	// Older builds spell the discriminator `type`; both are read so one schema
	// change does not silence the whole feed.
	AltType string `json:"type"`

	Text    string `json:"text"`
	Message string `json:"message"`

	// command_execution
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         *int   `json:"exit_code"`

	// file_change
	Changes []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`

	// mcp_tool_call
	Server string `json:"server"`
	Tool   string `json:"tool"`

	// web_search
	Query string `json:"query"`

	Status string `json:"status"`
}

func (i *codexItem) kind() string {
	if i.Type != "" {
		return i.Type
	}
	return i.AltType
}

// ParseLine turns one exec event into the events worth showing.
//
// item.updated is deliberately dropped: it carries the partial text of a message
// that item.completed repeats in full, so keeping both would double every
// assistant turn in the ring, on disk and on the page.
func (c *codexRuntime) ParseLine(line string) []SessionEvent {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if !strings.HasPrefix(line, "{") {
		// Not an event: the CLI's own stderr, which exec uses for progress and
		// for the reason a run could not start. Still worth showing — a signed-out
		// CLI says so here.
		return []SessionEvent{{Type: "raw", Text: line}}
	}
	var l codexLine
	if json.Unmarshal([]byte(line), &l) != nil {
		return []SessionEvent{{Type: "raw", Text: line}}
	}

	switch l.Type {
	case "thread.started":
		return []SessionEvent{{Type: "init", SessionID: l.ThreadID}}

	case "item.started":
		// Only the long-running items, so the feed narrates work while it happens
		// rather than after it. Their results arrive on item.completed.
		if l.Item == nil {
			return nil
		}
		switch l.Item.kind() {
		case "command_execution":
			return []SessionEvent{{Type: "tool_use", Tool: "Bash", Target: oneLine(l.Item.Command, 300)}}
		case "mcp_tool_call":
			return []SessionEvent{{Type: "tool_use", Tool: codexMCPToolName(l.Item)}}
		}
		return nil

	case "item.completed":
		return codexItemEvents(l.Item)

	case "turn.completed":
		ev := SessionEvent{Type: "result"}
		if l.Usage != nil {
			ev.Usage = &TokenUsage{
				Input:       l.Usage.InputTokens,
				CachedInput: l.Usage.CachedInputTokens,
				Output:      l.Usage.OutputTokens,
			}
		}
		return []SessionEvent{ev}

	case "turn.failed", "error":
		return []SessionEvent{{Type: "result", IsError: true, Text: codexErrorText(&l)}}
	}
	return nil
}

func codexItemEvents(item *codexItem) []SessionEvent {
	if item == nil {
		return nil
	}
	switch item.kind() {
	case "agent_message":
		if strings.TrimSpace(item.Text) == "" {
			return nil
		}
		return []SessionEvent{{Type: "assistant", Text: item.Text}}

	case "reasoning":
		if strings.TrimSpace(item.Text) == "" {
			return nil
		}
		return []SessionEvent{{Type: "thinking", Text: item.Text}}

	case "command_execution":
		failed := (item.ExitCode != nil && *item.ExitCode != 0) || item.Status == "failed"
		text := item.AggregatedOutput
		if failed {
			text = fmt.Sprintf("exit %d · %s", *item.ExitCode, text)
		}
		return []SessionEvent{{Type: "tool_result", Text: text, IsError: failed}}

	case "file_change":
		var out []SessionEvent
		for _, ch := range item.Changes {
			out = append(out, SessionEvent{Type: "tool_use", Tool: codexChangeTool(ch.Kind), Target: ch.Path})
		}
		return out

	case "mcp_tool_call":
		failed := item.Status == "failed" || item.Status == "error"
		return []SessionEvent{{
			Type:    "tool_result",
			Text:    strings.TrimSpace(codexMCPToolName(item) + " " + orDefault(item.Status, "completed")),
			IsError: failed,
		}}

	case "web_search":
		return []SessionEvent{{Type: "tool_use", Tool: "WebSearch", Target: oneLine(item.Query, 300)}}

	case "todo_list":
		return nil

	case "error":
		return []SessionEvent{{Type: "tool_result", Text: orDefault(item.Message, item.Text), IsError: true}}
	}
	return nil
}

// codexMCPToolName spells an MCP call the way the claude adapter does, so a run
// reads the same whichever CLI produced it and shortTool in the dashboard has
// one shape to strip.
func codexMCPToolName(item *codexItem) string {
	if item.Server == "" {
		return item.Tool
	}
	return "mcp__" + item.Server + "__" + item.Tool
}

func codexChangeTool(kind string) string {
	switch kind {
	case "add", "create":
		return "Write"
	case "delete", "remove":
		return "Delete"
	}
	return "Edit"
}

func codexErrorText(l *codexLine) string {
	if l.Error != nil && l.Error.Message != "" {
		return l.Error.Message
	}
	return l.Message
}

// ── exit classification ──────────────────────────────────────────────────────

// ClassifyExit reads the run's own events for what the loop cannot know: why it
// stopped.
//
// Three failures are worth naming, because each has a different remedy and all
// three look like "exited 1" from outside:
//
//	a plan limit    the account's usage window is spent. The agent did nothing
//	                wrong and retrying spends nothing but time, so this is
//	                reported as budget_exhausted — the one outcome the loop acts
//	                on, and two in a row pause the worker.
//
//	signed out      the CLI has no credentials. Every cycle will fail the same
//	                way in the same second until someone runs `codex login`.
//
//	relay unreached the MCP server did not come up, which is the failure that
//	                otherwise looks healthy: a session that starts, finds no
//	                tools, and explains itself in prose nobody reads.
func (c *codexRuntime) ClassifyExit(rc *RunContext, status int, outPath string) (string, string) {
	outcome := defaultOutcome(status)
	if status == 0 {
		return outcome, ""
	}

	fh, err := os.Open(outPath)
	if err != nil {
		return outcome, ""
	}
	defer fh.Close()

	blob := strings.ToLower(codexFailureText(fh))

	switch {
	case containsAny(blob, "usage limit", "rate limit", "quota", "429"):
		return outcomeBudget, fmt.Sprintf(`RUN STOPPED BY A PLAN LIMIT — the account this codex is signed in as has spent its usage window.
  Nothing was wrong with the agent or the task: the session was cut off partway.
  It did not finish and it did not hand the task back, so relay shows the task
  held until the claim's lease lapses and re-offers it.
  codex has no per-run spend cap for relay-cli to lower, so the ways out are to
  wait for the window to reset, to lower max_runs_per_hour for this worker in %s,
  or to run it on an account with more headroom.`, displayConfigPath())

	case containsAny(blob, "not logged in", "please log in", "unauthorized", "401"):
		return outcomeError, `THE CLI IS NOT SIGNED IN — every cycle will fail this way until it is.
  Run ` + "`codex login`" + ` once as the user this fleet runs as. relay-cli never writes
  or moves those credentials, and it does not set an API key: a worker signs in
  as you do.`

	// Specific phrases only. exec echoes a header — the model, the sandbox, the
	// prompt — to stderr, and the prompt names relay throughout, so matching the
	// word alone would report an MCP failure for every non-zero exit.
	case containsAny(blob, "mcp server", "mcp client", "failed to start mcp", "mcp_servers"):
		return outcomeError, `THE RELAY MCP SERVER DID NOT COME UP — the session had no relay tools to call.
  Check this worker's relay_mcp with ` + "`relay check`" + `, which tests the same
  credential over plain HTTP and spends nothing. If that passes, the CLI could
  not reach it: this build has to support streamable-HTTP MCP servers.`
	}

	return outcome, ""
}

// codexFailureText collects the parts of a run that can say why it stopped, and
// only those: the CLI's own stderr, the turn.failed and error events, and error
// items. Reading the whole stream instead would classify a run by whatever the
// agent happened to write — an assistant message quoting "rate limit" is not a
// plan limit, and treating it as one pauses a healthy worker.
func codexFailureText(r io.Reader) string {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		if !strings.HasPrefix(text, "{") {
			out = append(out, text)
			continue
		}
		var l codexLine
		if json.Unmarshal([]byte(text), &l) != nil {
			continue
		}
		switch {
		case l.Type == "turn.failed" || l.Type == "error":
			out = append(out, codexErrorText(&l))
		case l.Item != nil && l.Item.kind() == "error":
			out = append(out, orDefault(l.Item.Message, l.Item.Text))
		}
	}
	return strings.Join(out, "\n")
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
