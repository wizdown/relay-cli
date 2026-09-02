// Runtime adapters: the only part of relay-cli that knows what a particular
// CLI is called or how it is spelled.
//
// An adapter's ONLY job is to translate the worker contract into one invocation
// of a CLI, and to say afterwards what that run's exit meant. It does not poll,
// does not decide whether to run, does not enforce timeouts or ceilings, and
// does not touch relay — the poll loop owns all of that, identically for every
// runtime.
//
// It also carries no agent IDENTITY. Who an agent is and what it may decide
// alone is relay's per-agent instructions_md, which the agent receives over MCP.
// An adapter passes along the runtime contract and nothing more.
//
// `claude` and `codex` are the runtimes offered today, and both adapters are
// native Go — which is what lets relay run with no jq, no curl and no scripts
// on disk. A native adapter is what buys the live session feed: both CLIs emit
// one JSON object per event, and only Go in this repo can turn those into the
// events the dashboard draws.
//
// A third kind exists in this file and does not run: bashRuntime drives a
// runtimes/<name>.sh through the contract documented on bashAdapterEnv below.
// It is complete and tested, but bashAdaptersEnabled is false, because a bash
// adapter contributes an argv and nothing else — no stream parsing and no
// declared config keys — and an extension point that cannot be supported is a
// promise this repo is not ready to keep. It is kept for the CLI nobody has
// written an adapter for yet.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// The task-agnostic prompt for one cycle. Overridable for debugging, exactly as
// in the bash poller.
var workerPrompt = envOr("WORKER_PROMPT",
	"Poll relay for one available task, claim it, and work it to completion. If no task is available, say so and stop immediately.")

// Every relay agent-surface tool, named explicitly. Headless runs never see an
// approval prompt, so anything not pre-allowed is silently denied — relay access
// must not depend on the model's judgement about an unfamiliar tool.
//
// This lists the WHOLE surface, including the capability-gated fleet verbs
// (create_task, delegate_task_to_agent, answer_task, …). Listing a tool the
// agent was not granted is harmless: relay hides it from tools/list and refuses
// the call anyway, so this allowlist is a floor, never a grant. Omitting one is
// NOT harmless — the agent sees the tool relay offered it, calls it, and the CLI
// denies it with no useful error.
//
// get_subtask_handoff / get_subtask_document are the sharp end of that: relay's
// own playbook tells an orchestrator to read a handoff BEFORE answering or
// reviewing it. Omit them and the supervisor either stalls at the read or
// resolves blind — with the denial visible only in the run's permission_denials,
// after it has been paid for.
const relayAllowedTools = "mcp__relay__get_available_tasks mcp__relay__claim_task mcp__relay__heartbeat mcp__relay__get_task_context mcp__relay__update_task_context mcp__relay__add_comment mcp__relay__ask_question mcp__relay__request_review mcp__relay__release_task mcp__relay__get_task_document mcp__relay__update_task_document mcp__relay__attach_new_document_to_task mcp__relay__request_document_deletion mcp__relay__create_task mcp__relay__link_document_to_task mcp__relay__unlink_document_from_task mcp__relay__delegate_task_to_agent mcp__relay__undelegate_task mcp__relay__list_agents mcp__relay__get_subtask_handoff mcp__relay__get_subtask_document mcp__relay__answer_task mcp__relay__approve_task mcp__relay__request_changes"

// The CLI's own tools a worker needs to do the work, named for the same reason
// the relay tools are: a headless run has no prompt to approve anything on, so
// `--permission-mode auto` denies whatever no rule matches. None of these is
// pre-allowed by default. Without this list a worker can read its task, update
// the Task Context and hand it back — and cannot touch a single file. The
// denial is invisible while it happens: it reaches the operator only in the
// run's permission_denials, after the session has been paid for.
//
// The list is whole-tool rather than pattern-scoped (`Bash`, not `Bash(git *)`)
// on purpose. A worker is already an autonomous session inside a checkout the
// operator chose and accepted might be rewritten; scoping here would only move
// the same silent denial to the first command nobody predicted. What bounds a
// run is repo_dir and the spend ceilings.
//
// Rules in the operator's own ~/.claude/settings.json still apply on top, and a
// deny rule still wins: this is a floor, not a bypass.
const coreAllowedTools = "Read Glob Grep Edit Write NotebookEdit Bash BashOutput KillShell Task TodoWrite Skill WebFetch WebSearch"

// workerAllowedTools is what one run is launched with: relay's surface, plus the
// tools that make it a coding session rather than a bookkeeping one.
const workerAllowedTools = relayAllowedTools + " " + coreAllowedTools

// workdirInspector is implemented by a runtime that can say what a session
// started in a directory would pick up from it. Optional on purpose: what lives
// in a working directory is one CLI's own layout, so only that CLI's adapter can
// read it, and a runtime with nothing to report simply does not implement this.
//
// It is read-only by contract — `relay check` calls it, and check launches
// nothing and spends nothing.
type workdirInspector interface {
	InspectWorkdir(dir string) string
}

// RunContext is what an adapter is given to build one invocation. It mirrors the
// environment a bash adapter is given, field for field — the contract
// runtimes/_template.sh documents.
type RunContext struct {
	Worker     *Worker
	WorkerDir  string // scratch/state dir — generated config goes HERE, not in the repo
	RepoDir    string // the checkout the CLI runs in; the loop has already chosen it
	Prompt     string
	Rules      string
	RulesFile  string
	AllowTools string
}

// runtimeField declares one key a runtime accepts inside a worker's
// "runtime_config".
//
// This table is the single source of truth for three things that used to drift
// apart: what the config parser accepts, what a bash adapter is handed, and what
// the documentation claims. A runtime that grows a setting declares it here and
// all three follow.
type runtimeField struct {
	Key      string
	Kind     fieldKind
	Required bool
	// Default applies when the key is absent and not required. Empty means the
	// setting simply goes unset, which is not the same as zero.
	Default string
	// Enum, when set, is the complete list of values this key accepts. A value
	// outside it is refused when the config loads rather than by the CLI inside
	// a run that has already been paid for — which is the whole reason this
	// table exists.
	Enum []string
	// Doc is one line, used in the error a missing required field produces and
	// in the generated reference table.
	Doc string
}

type fieldKind int

const (
	fieldString fieldKind = iota
	fieldNumber
	// fieldBool is a JSON true/false, canonicalised to "true"/"false". Written
	// as a boolean rather than as a string because that is what someone editing
	// a JSON file expects to write, and "false" quoted is the value that reads
	// as on.
	fieldBool
)

// Runtime is one CLI, wrapped.
type Runtime interface {
	Name() string
	// ConfigFields declares every key this runtime accepts in a worker's
	// "runtime_config", with which are required and what the optional ones
	// default to. The config parser validates against it and the docs are
	// written from it.
	ConfigFields() []runtimeField
	// Check answers: is this CLI usable on this machine? A non-nil error carries
	// a one-line fix hint, and is reported at launch rather than 120 times an
	// hour in a background log.
	Check() error
	// BuildCmd returns the exact argv to run. It must not exec, background, cd or
	// apply a timeout: the loop runs the argv with RepoDir as cwd under the
	// worker's max_seconds_per_run.
	BuildCmd(rc *RunContext) ([]string, error)
	// ParseLine turns one line of the CLI's output into session events. An
	// adapter that cannot parse its CLI returns a single "raw" event, which is
	// still live in the UI — just unstructured.
	ParseLine(line string) []SessionEvent
	// ClassifyExit says what a finished run MEANT. The loop knows a run exited 1;
	// only the adapter knows whether that was a spend cap, a bad model name, or
	// ordinary failure.
	ClassifyExit(rc *RunContext, status int, outPath string) (outcome, explanation string)
}

// Outcomes an adapter may report. Only budgetExhausted changes what the loop
// does; the rest are for the log and the UI.
const (
	outcomeOK      = "ok"
	outcomeTimeout = "timeout"
	outcomeError   = "error"
	outcomeBudget  = "budget_exhausted"
)

// versioned is implemented by adapters that can report which CLI they found.
// Optional: a bash adapter contributes an argv and nothing else, so it has no
// way to answer this.
type versioned interface {
	Version() string
	Path() string
}

// bashAdaptersEnabled gates every runtime that has no adapter compiled in.
//
// The bash-adapter path below is complete and covered by tests; what it is
// missing is verification against a real CLI, which is the whole of what
// "supported" means here. It is also the weaker half of the contract: a script
// gets no stream parsing and declares no config keys, so a worker driven by one
// cannot even be told which model to run. Both compiled-in runtimes are native
// for that reason; flipping this constant is for a third-party CLI, not for
// anything this repo ships.
const bashAdaptersEnabled = false

// supportedRuntimes is every runtime a config may name today. The reference
// tables in docs/configuration.md and the test that guards them are both
// written from this, so a new adapter is documented by existing.
func supportedRuntimes() []Runtime { return []Runtime{claudeAdapter, codexAdapter} }

// ResolveRuntime maps a worker's "runtime" field to an adapter.
//
// To be clear about what "built in" means here, since the word invites the wrong
// reading: what ships inside relay-cli is the ADAPTER — the code that knows how
// to build an argv for a CLI and read what it prints. No CLI is bundled. claude
// is installed separately and found on PATH, and its adapter is asked to prove
// that at startup.
func ResolveRuntime(name, relayDir string) (Runtime, error) {
	for _, rt := range supportedRuntimes() {
		if rt.Name() == name {
			return rt, nil
		}
	}
	if !bashAdaptersEnabled {
		return nil, fmt.Errorf("relay-cli supports two runtimes: claude and codex.\n"+
			"       %q is not offered. Set \"runtime\" to one of those.", name)
	}
	script := filepath.Join(relayDir, "runtimes", name+".sh")
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("relay-cli has no adapter for it: %s does not exist.\n"+
			"       adapters compiled in: claude\n"+
			"       add one by copying a bash adapter into runtimes/ — see\n"+
			"       "+docsBase+"contributing/adapters.md", script)
	}
	return &bashRuntime{name: name, script: script}, nil
}

// ── bash adapters ────────────────────────────────────────────────────────────

// bashRuntime drives a runtimes/<name>.sh through exactly the contract
// _template.sh documents, so an adapter written for the bash poller keeps
// working here with no edit.
//
// It gets no stream parsing: a bash adapter contributes an argv and nothing
// else, so its output reaches the UI as raw lines. Live, in order, and readable
// — just not broken into tool calls the way a native adapter's is.
type bashRuntime struct {
	name   string
	script string
}

func (b *bashRuntime) Name() string { return b.name }

// ConfigFields — a bash adapter declares nothing yet, so any runtime_config key
// on a worker using one is refused rather than passed through unchecked. Giving
// a script a way to declare its own fields is the other half of shipping bash
// adapters, alongside bashAdaptersEnabled.
func (b *bashRuntime) ConfigFields() []runtimeField { return nil }

func (b *bashRuntime) Check() error {
	out, err := exec.Command("bash", "-c", `. "$1"; runtime_check`, "_", b.script).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// BuildCmd sources the adapter and reads back RUNTIME_CMD as a NUL-separated
// list. NUL rather than newline because an argument can contain anything — the
// prompt a codex adapter builds is multi-line by construction.
func (b *bashRuntime) BuildCmd(rc *RunContext) ([]string, error) {
	cmd := exec.Command("bash", "-c",
		`. "$1"; runtime_build_cmd || exit 1; printf '%s\0' "${RUNTIME_CMD[@]}"`, "_", b.script)
	cmd.Env = append(os.Environ(), bashAdapterEnv(rc)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("runtime %q failed to build a command: %s", b.name, Scrub(strings.TrimSpace(stderr.String())))
	}
	parts := strings.Split(strings.TrimSuffix(stdout.String(), "\x00"), "\x00")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("runtime %q produced an empty command", b.name)
	}
	return parts, nil
}

func (b *bashRuntime) ParseLine(line string) []SessionEvent {
	return []SessionEvent{{Type: "raw", Text: line}}
}

// ClassifyExit delegates to the adapter's optional runtime_classify_exit. An
// adapter that does not define one gets the generic report, which is a fine
// place to start.
func (b *bashRuntime) ClassifyExit(rc *RunContext, status int, outPath string) (string, string) {
	outcome := defaultOutcome(status)
	cmd := exec.Command("bash", "-c",
		`. "$1"; declare -F runtime_classify_exit >/dev/null || exit 0
		 RUN_OUTCOME=""; RUN_EXPLANATION=""
		 runtime_classify_exit "$2" "$3"
		 printf '%s\0%s' "$RUN_OUTCOME" "$RUN_EXPLANATION"`,
		"_", b.script, fmt.Sprint(status), outPath)
	cmd.Env = append(os.Environ(), bashAdapterEnv(rc)...)
	out, err := cmd.Output()
	if err != nil {
		return outcome, ""
	}
	parts := strings.SplitN(string(out), "\x00", 2)
	if len(parts) == 2 && parts[0] != "" {
		outcome = parts[0]
	}
	if len(parts) == 2 {
		return outcome, strings.TrimSpace(parts[1])
	}
	return outcome, ""
}

// bashAdapterEnv is the whole bash-adapter contract. It used to be documented
// in runtimes/_template.sh; that file is not shipped while bashAdaptersEnabled
// is false, so the contract lives here rather than in a deleted file.
//
// An adapter is sourced, not executed, and defines two functions:
//
//	runtime_check       is this CLI usable on this machine? Non-zero plus a
//	                    one-line fix hint if not. Called once per worker at
//	                    launch, so a missing CLI fails fast rather than 120
//	                    times an hour in a background log.
//	runtime_build_cmd   populate RUNTIME_CMD with the exact argv. Do NOT exec,
//	                    background, cd or apply a timeout — the loop runs
//	                    RUNTIME_CMD with REPO_DIR as cwd under the worker's
//	                    max_seconds_per_run, so a run and its kill switch behave
//	                    the same whichever runtime produced it.
//	runtime_classify_exit  OPTIONAL. Say what an exit MEANT: the loop knows a
//	                    run exited 1, only the adapter knows whether that was a
//	                    spend cap, a bad model name, or ordinary failure.
//
// The variables below are everything an adapter is given. On top of them, every
// key the adapter declared in ConfigFields arrives as RUNTIME_<KEY> — so a
// runtime's own settings are typed, validated and documented before they reach
// the script, which raw argv passthrough never was.
//
// A headless run can never answer an approval prompt, so whatever the CLI spells
// as "fully autonomous" must be set unconditionally by the adapter.
func bashAdapterEnv(rc *RunContext) []string {
	env := []string{
		"RELAY_CONNECTOR_URL=" + rc.Worker.Endpoint,
		"WORKER_PROMPT=" + rc.Prompt,
		"WORKER_RULES=" + rc.Rules,
		"WORKER_RULES_FILE=" + rc.RulesFile,
		"RELAY_ALLOWED_TOOLS=" + rc.AllowTools,
		"INSTANCE_NAME=" + rc.Worker.Name,
		"WORKER_DIR=" + rc.WorkerDir,
		"REPO_DIR=" + rc.RepoDir,
	}
	keys := make([]string, 0, len(rc.Worker.RuntimeConfig))
	for k := range rc.Worker.RuntimeConfig {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, "RUNTIME_"+strings.ToUpper(k)+"="+rc.Worker.RuntimeConfig[k])
	}
	return env
}

// ── sign-in checks ───────────────────────────────────────────────────────────

// Both CLIs can be asked whether they are signed in without spending anything,
// and both are asked at startup: a signed-out CLI fails every cycle in the same
// second, so refusing to start is the honest answer. The two helpers below are
// what every adapter's sign-in check shares.

// warnOut is where the two warnings below go. A variable so a test can read what
// was printed: a warning nobody can assert on is a warning that silently stops
// being printed.
var warnOut io.Writer = os.Stderr

// envCredentialName returns the first of these environment variables that is
// set, or "" when none is.
//
// This is the escape hatch that keeps the sign-in check from causing the outage
// it exists to prevent. A key exported into the environment authenticates a run
// perfectly well while the CLI's own status says "not logged in" — codex says so
// explicitly about CODEX_API_KEY — and refusing to start a fleet that would have
// worked is worse than not checking at all.
//
// It returns the NAME rather than a bool because standing down silently is its
// own trap: what relay-cli knows is that a key is present, not that it is valid,
// so the one case where a stale variable left over from something else hides a
// genuinely signed-out CLI has to be visible. See warnSignInStandDown.
func envCredentialName(names ...string) string {
	for _, n := range names {
		if os.Getenv(n) != "" {
			return n
		}
	}
	return ""
}

// warnSignInStandDown says why a signed-out CLI is being allowed to start
// anyway, and names the variable responsible.
//
// Validating the key would cost a model call, which is the one thing `check`
// may not spend — so relay-cli trusts its presence. That trust is worth stating
// out loud: an ANTHROPIC_API_KEY or OPENAI_API_KEY exported for something
// unrelated, on a machine whose CLI is genuinely signed out, is exactly the
// combination that would otherwise turn a startup error back into a failure per
// cycle in a log nobody is watching.
func warnSignInStandDown(cli, envName, loginCmd string) {
	fmt.Fprintf(warnOut, "warning: %s reports it is NOT signed in, but %s is set — starting anyway.\n"+
		"         relay-cli cannot tell whether that key is valid without spending a call, so it\n"+
		"         trusts it. If every run fails immediately, the key is wrong or left over from\n"+
		"         something else: run `%s` and unset %s.\n", cli, envName, loginCmd, envName)
}

// warnUnverifiedSignIn is the answer when a CLI cannot be asked at all — an
// older build with no status command, or one that will not run here.
// Unverifiable is not the same as signed out: the start continues, and a real
// failure surfaces on the first run instead.
func warnUnverifiedSignIn(cli string, detail string) {
	fmt.Fprintf(warnOut, "warning: could not ask %s whether it is signed in (%s).\n"+
		"         Continuing anyway; if it is not, the first run will say so in worker.log.\n", cli, detail)
}

func defaultOutcome(status int) string {
	switch status {
	case 0:
		return outcomeOK
	case 124:
		return outcomeTimeout
	}
	return outcomeError
}
