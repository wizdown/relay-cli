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
// `claude` is the only runtime offered today, and its adapter is native Go —
// which is what lets relay-cli run with no jq, no curl and no scripts on disk.
//
// A second kind exists in this file and does not run: bashRuntime drives a
// runtimes/<name>.sh through the contract documented on bashAdapterEnv below.
// It is complete and tested, but bashAdaptersEnabled is false, because nothing
// but claude has been verified against a current CLI and an extension point
// that cannot be supported is a promise this repo is not ready to keep. Codex
// support is the reason it is kept rather than deleted.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	MaxBudget  string // per-run spend cap, or "" when removed or unsupported
	ExtraArgs  string
	AllowTools string
}

// Runtime is one CLI, wrapped.
type Runtime interface {
	Name() string
	// Check answers: is this CLI usable on this machine? A non-nil error carries
	// a one-line fix hint, and is reported at launch rather than 120 times an
	// hour in a background log.
	Check() error
	// BuildCmd returns the exact argv to run. It must not exec, background, cd or
	// apply a timeout: the loop runs the argv with RepoDir as cwd under the
	// worker's run_timeout_seconds.
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

// bashAdaptersEnabled gates every runtime that is not claude.
//
// The bash-adapter path below is complete and covered by tests; what it is
// missing is verification against a real CLI, which is the whole of what
// "supported" means here. Flipping this to true is one half of shipping codex
// support — the other is restoring runtimes/ with an adapter in it.
const bashAdaptersEnabled = false

// ResolveRuntime maps a worker's "runtime" field to an adapter.
//
// To be clear about what "built in" means here, since the word invites the wrong
// reading: what ships inside relay-cli is the ADAPTER — the code that knows how
// to build an argv for a CLI and read what it prints. No CLI is bundled. claude
// is installed separately and found on PATH, and its adapter is asked to prove
// that at startup.
func ResolveRuntime(name, pollerRoot string) (Runtime, error) {
	if name == "claude" {
		return claudeAdapter, nil
	}
	if !bashAdaptersEnabled {
		// Naming codex specifically matters: "unknown runtime" would read as a
		// typo, and someone who deliberately wrote "codex" would go looking for
		// the spelling mistake rather than learning that the runtime is simply
		// not offered yet.
		if name == "codex" {
			return nil, fmt.Errorf("claude is the only supported runtime today, and codex support is coming soon.\n" +
				"       Set \"runtime\": \"claude\" — it is also the default, so the field can be dropped.")
		}
		return nil, fmt.Errorf("relay-cli supports one runtime: claude.\n"+
			"       %q is not offered. Set \"runtime\": \"claude\" — it is also the\n"+
			"       default, so the field can be dropped.", name)
	}
	script := filepath.Join(pollerRoot, "runtimes", name+".sh")
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("relay-cli has no adapter for it: %s does not exist.\n"+
			"       adapters compiled in: claude\n"+
			"       add one by copying a bash adapter into runtimes/ — see docs/runtimes.md", script)
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
//	                    run_timeout_seconds, so a run and its kill switch behave
//	                    the same whichever runtime produced it.
//	runtime_classify_exit  OPTIONAL. Say what an exit MEANT: the loop knows a
//	                    run exited 1, only the adapter knows whether that was a
//	                    spend cap, a bad model name, or ordinary failure.
//
// The variables below are everything an adapter is given. MAX_BUDGET_USD is
// empty when the operator removed the cap or the runtime has none; a headless
// run can never answer an approval prompt, so whatever the CLI spells as
// "fully autonomous" must be set unconditionally by the adapter.
func bashAdapterEnv(rc *RunContext) []string {
	return []string{
		"RELAY_CONNECTOR_URL=" + rc.Worker.Endpoint,
		"WORKER_PROMPT=" + rc.Prompt,
		"WORKER_RULES=" + rc.Rules,
		"WORKER_RULES_FILE=" + rc.RulesFile,
		"RELAY_ALLOWED_TOOLS=" + rc.AllowTools,
		"INSTANCE_NAME=" + rc.Worker.Name,
		"WORKER_DIR=" + rc.WorkerDir,
		"REPO_DIR=" + rc.RepoDir,
		"WORKER_MODEL=" + rc.Worker.Model,
		"MAX_BUDGET_USD=" + rc.MaxBudget,
		"RUNTIME_ARGS=" + rc.ExtraArgs,
	}
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

// splitArgs word-splits raw extra flags the way the shell did. An escape hatch,
// with the same limitation the bash poller documented: an argument containing a
// space cannot be expressed.
func splitArgs(s string) []string {
	return strings.Fields(s)
}
