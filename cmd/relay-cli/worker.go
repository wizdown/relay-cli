// The single-worker loop: every poll_seconds, ask relay — over plain
// HTTP, with no model running — whether this worker has a task. Only if it does,
// launch one headless CLI session that claims and works exactly one task, then
// go idle.
//
// The gate is the point. Booting a CLI just to ASK costs tens of thousands of
// tokens of context before the first tool call, and the empty queue is the
// common case — so the check has to happen outside the model. probe.go does it
// over plain HTTP.
//
// Runtime-agnostic: WHICH CLI runs is decided by the adapter, which contributes
// only an argv. Polling, gating, cwd, timeouts and every spend ceiling live
// here, so each runtime gets identical behaviour for free.
//
// This file is the single-worker loop itself: the gate, the ceilings, all three
// circuit breakers, and the exact text each one pauses with.
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Fixed, not configurable. Its only job is to stop a relaunch treadmill after a
// run that fails or is refused instantly — the task is re-offered on the very
// next poll and picked straight back up. That is a constant of the harness, not
// a policy anyone tunes: too low and it does nothing, too high and it is just a
// slower run ceiling. To make a worker ACT less often, lower max_runs_per_hour,
// which is the knob that actually caps spend.
const relaunchCooldown = 60 * time.Second

// Circuit breakers. Each counts a specific kind of fruitless cycle and
// self-pauses once it is clearly not going to stop on its own. A worker that
// cannot make progress should say so once and go quiet, not burn its whole run
// ceiling proving it every hour.
var (
	maxProbeFailures  = envSeconds("MAX_PROBE_FAILURES", 10)
	maxBudgetKills    = envSeconds("MAX_BUDGET_KILLS", 2)
	maxAttentionStall = envSeconds("MAX_ATTENTION_STALLS", 3)
)

// RunSummary is one CLI session, as the UI shows it.
type RunSummary struct {
	RunID     string     `json:"run_id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
	Model     string     `json:"model,omitempty"`
	Status    int        `json:"status"`
	Outcome   string     `json:"outcome,omitempty"`
	CostUSD   float64    `json:"cost_usd"`
	Usage     TokenUsage `json:"usage"`
	NumTurns  int        `json:"num_turns"`
	ToolCalls int        `json:"tool_calls"`
	Queue     QueueState `json:"queue"`
	// TaskID is which task this run is on, as observed here. Relay owns task
	// state; this is only the id the agent was seen passing to a tool call, kept
	// so the fleet board and the ledger can say what a run was working on. A run
	// whose agent never named one carries none, and that is not an error.
	TaskID string `json:"task_id,omitempty"`
}

// WorkerStatus is everything the dashboard shows about one worker. It is a
// value, copied under lock, so a slow HTTP client can never hold up a loop.
type WorkerStatus struct {
	Worker *Worker `json:"config"`

	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	Paused bool   `json:"paused"`

	LastPoll      *QueueState `json:"last_poll,omitempty"`
	LastPollAt    *time.Time  `json:"last_poll_at,omitempty"`
	LastPollError string      `json:"last_poll_error,omitempty"`
	NextPollAt    *time.Time  `json:"next_poll_at,omitempty"`
	ProbeFailures int         `json:"probe_failures"`

	RunsLastHour int `json:"runs_last_hour"`
	// CeilingResetsAt is when the oldest run in the window ages out and the
	// hourly ceiling frees a slot. The window rolls rather than resetting on the
	// hour, so a worker sitting at its ceiling has no reset time to show without
	// it. Unset when the worker has no ceiling or no runs in the last hour.
	CeilingResetsAt *time.Time `json:"ceiling_resets_at,omitempty"`

	TotalCostUSD float64 `json:"total_cost_usd"`
	// TotalTokens is the same running total for a runtime that reports tokens
	// rather than dollars. One of the two is always zero for a given worker.
	TotalTokens int `json:"total_tokens"`

	CurrentRun *RunSummary  `json:"current_run,omitempty"`
	Runs       []RunSummary `json:"runs,omitempty"`
}

// WorkerRunner owns one worker's loop and its state directory.
type WorkerRunner struct {
	cfg *Config
	w   *Worker
	rt  Runtime
	bus *Bus

	dir       string
	prober    *Prober
	rules     string
	rulesFile string

	mu     sync.Mutex
	status WorkerStatus
	runs   []RunSummary
}

func NewWorkerRunner(cfg *Config, w *Worker, rt Runtime, bus *Bus, rules, rulesFile string) *WorkerRunner {
	dir := filepath.Join(cfg.RelayDir, stateDirName, w.Name)
	return &WorkerRunner{
		cfg: cfg, w: w, rt: rt, bus: bus,
		dir: dir, prober: NewProber(w.Endpoint), rules: rules, rulesFile: rulesFile,
		status: WorkerStatus{Worker: w, State: StateStarting},
	}
}

func (r *WorkerRunner) Dir() string { return r.dir }

// Status returns a copy for the API.
func (r *WorkerRunner) Status() WorkerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.status
	s.Runs = append([]RunSummary(nil), r.runs...)
	return s
}

// ── file-backed state ───────────────────────────────────────────────────────
//
// A worker's whole runtime state is these files, and they are plain enough to
// read and edit by hand on purpose. The PAUSED file in particular is the
// documented way a human pauses a worker, so its name and meaning are interface,
// not implementation.

func (r *WorkerRunner) path(name string) string { return filepath.Join(r.dir, name) }

func (r *WorkerRunner) pausedFile() string     { return r.path("PAUSED") }
func (r *WorkerRunner) runsFile() string       { return r.path("runs.log") }
func (r *WorkerRunner) probeFailFile() string  { return r.path("probe-failures") }
func (r *WorkerRunner) budgetKillFile() string { return r.path("budget-kills") }
func (r *WorkerRunner) stallFile() string      { return r.path("attention-stall") }
func (r *WorkerRunner) lastRunFile() string    { return r.path("last-run.out") }
func (r *WorkerRunner) lockDir() string        { return r.path("lock") }

func (r *WorkerRunner) log(format string, args ...any) {
	r.bus.Publish(Event{Worker: r.w.Name, Kind: KindLog, Level: "info", Text: fmt.Sprintf(format, args...)})
}

func (r *WorkerRunner) warn(format string, args ...any) {
	r.bus.Publish(Event{Worker: r.w.Name, Kind: KindLog, Level: "warn", Text: fmt.Sprintf(format, args...)})
}

// setState records the worker's state for the cards. It publishes nothing.
//
// State is a property of a worker, not an event in its history, and treating it
// as the latter produced pure noise: idle→polling→idle every tick, and a
// cooldown detail carrying a live countdown that changed every second. The
// transitions worth reading — a ceiling reached, a breaker tripping, a cycle
// starting — each already log a line that says what happened AND why. The UI
// reads state from the status snapshot it polls for the countdown anyway.
func (r *WorkerRunner) setState(state, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.State = state
	r.status.Detail = detail
}

func readCounter(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func writeCounter(path string, n int) {
	os.WriteFile(path, []byte(strconv.Itoa(n)+"\n"), 0o644)
}

// ── the loop ─────────────────────────────────────────────────────────────────

func (r *WorkerRunner) Run(ctx context.Context) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		r.warn("cannot create state dir: %v", err)
		return
	}
	// Created up front rather than on first write, so a browser attaching
	// immediately finds the worker present-and-quiet instead of missing.
	os.WriteFile(r.runsFile(), nil, 0o644)

	r.log("starting: runtime=%s, poll every %gs, timeout %ds, repo %s",
		r.w.Runtime, r.cfg.PollSeconds, r.w.MaxSecondsPerRun, r.w.RepoDir)

	interval := time.Duration(r.cfg.PollSeconds * float64(time.Second))
	for {
		r.tick(ctx)
		if ctx.Err() != nil {
			break
		}
		next := time.Now().Add(interval).UTC()
		r.mu.Lock()
		r.status.NextPollAt = &next
		r.mu.Unlock()

		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
		if ctx.Err() != nil {
			break
		}
	}
	r.setState(StateStopped, "")
	r.log("shutting down")
}

func (r *WorkerRunner) tick(ctx context.Context) {
	if _, err := os.Stat(r.pausedFile()); err == nil {
		r.mu.Lock()
		r.status.Paused = true
		r.mu.Unlock()
		r.setState(StatePaused, "PAUSED file present — remove it to resume")
		return
	}
	r.mu.Lock()
	r.status.Paused = false
	r.mu.Unlock()

	// The lock directory is how the retired bash poller stopped a slow cycle from
	// overlapping the next tick. One goroutine per worker already guarantees that
	// here — this is kept for the case it also covered: a second relay-cli process,
	// or a leftover bash worker, running the same worker at the same time.
	if err := os.Mkdir(r.lockDir(), 0o755); err != nil {
		r.log("previous cycle still running, skipping this tick")
		return
	}
	defer os.Remove(r.lockDir())

	if !r.withinCeilings() {
		return
	}

	r.setState(StatePolling, "")
	pollCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	queue, err := r.prober.GetAvailableTasks(pollCtx)
	cancel()

	if err != nil {
		n := readCounter(r.probeFailFile()) + 1
		writeCounter(r.probeFailFile(), n)
		now := time.Now().UTC()
		r.mu.Lock()
		r.status.ProbeFailures = n
		r.status.LastPollError = Scrub(err.Error())
		r.status.LastPollAt = &now
		r.mu.Unlock()
		// One event, carrying the count. The count is the part that matters: a
		// single failure is a blip, the tenth in a row is a revoked credential.
		r.bus.Publish(Event{Worker: r.w.Name, Kind: KindPoll, Level: "warn",
			Text: fmt.Sprintf("probe failed (%d consecutive)", n), Error: err.Error()})

		// A revoked credential or a dead host would otherwise fail forever, twice
		// a minute. Trip the breaker and make a human look.
		if maxProbeFailures > 0 && n >= maxProbeFailures {
			r.selfPause(fmt.Sprintf("%d consecutive probe failures — fix the endpoint/credential, then remove the PAUSED file.\n  last error: %s", n, Scrub(err.Error())))
			return
		}
		r.setState(StateProbeErr, fmt.Sprintf("%d consecutive probe failures", n))
		return
	}

	writeCounter(r.probeFailFile(), 0)
	now := time.Now().UTC()
	r.mu.Lock()
	r.status.ProbeFailures = 0
	r.status.LastPollError = ""
	r.status.LastPoll = &queue
	r.status.LastPollAt = &now
	r.mu.Unlock()

	// Every poll is published, including the empty ones — that is what makes the
	// dashboard show a worker as alive rather than merely not-broken. It stays
	// out of worker.log, where the bash poller deliberately kept it silent so an
	// idle worker cost nothing, log noise included.
	q := queue
	r.bus.Publish(Event{Worker: r.w.Name, Kind: KindPoll, Poll: &q})

	if queue.Total() > 0 {
		r.runCycle(ctx, queue)
	}
	r.setState(StateIdle, "")
}

// withinCeilings evaluates every bound before anything is spent.
func (r *WorkerRunner) withinCeilings() bool {
	runs := r.readRuns()
	now := time.Now()

	if len(runs) > 0 {
		since := now.Sub(runs[len(runs)-1])
		if since < relaunchCooldown {
			r.setState(StateCooldown, fmt.Sprintf("relaunch cooldown, %ds left", int((relaunchCooldown-since).Seconds())))
			return false
		}
	}

	recent := 0
	cutoff := now.Add(-time.Hour)
	var oldest time.Time
	for _, t := range runs {
		if t.After(cutoff) {
			recent++
			if oldest.IsZero() || t.Before(oldest) {
				oldest = t
			}
		}
	}
	r.mu.Lock()
	r.status.RunsLastHour = recent
	r.status.CeilingResetsAt = nil
	if r.w.MaxRunsPerHour > 0 && !oldest.IsZero() {
		at := oldest.Add(time.Hour).UTC()
		r.status.CeilingResetsAt = &at
	}
	r.mu.Unlock()

	if r.w.MaxRunsPerHour > 0 && recent >= r.w.MaxRunsPerHour {
		r.log("run ceiling reached (%d/%d in the last hour) — not launching", recent, r.w.MaxRunsPerHour)
		r.setState(StateCeiling, fmt.Sprintf("%d/%d runs this hour", recent, r.w.MaxRunsPerHour))
		return false
	}
	return true
}

func (r *WorkerRunner) readRuns() []time.Time {
	fh, err := os.Open(r.runsFile())
	if err != nil {
		return nil
	}
	defer fh.Close()
	var out []time.Time
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		if n, err := strconv.ParseInt(strings.TrimSpace(sc.Text()), 10, 64); err == nil {
			out = append(out, time.Unix(n, 0))
		}
	}
	return out
}

func (r *WorkerRunner) recordRun() {
	fh, err := os.OpenFile(r.runsFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer fh.Close()
	fmt.Fprintf(fh, "%d\n", time.Now().Unix())
}

// runContext — the CLI starts inside repo_dir, so that directory's CLAUDE.md,
// skills and tooling load exactly as they would for a human there.
// That is what the field is FOR, which is why it is required: an agent pointed
// somewhere arbitrary is an agent working without any of it.
func (r *WorkerRunner) runContext() *RunContext {
	return &RunContext{
		Worker:     r.w,
		WorkerDir:  r.dir,
		RepoDir:    r.w.RepoDir,
		Prompt:     workerPrompt,
		Rules:      r.rules,
		RulesFile:  r.rulesFile,
		AllowTools: workerAllowedTools,
	}
}

// ── one cycle ────────────────────────────────────────────────────────────────

func (r *WorkerRunner) runCycle(ctx context.Context, queue QueueState) {
	rc := r.runContext()
	argv, err := r.rt.BuildCmd(rc)
	if err != nil {
		r.warn("runtime %q failed to build a command — skipping cycle: %v", r.w.Runtime, err)
		return
	}

	r.recordRun()
	runID := fmt.Sprintf("%s-%d", r.w.Name, time.Now().UnixNano())
	summary := RunSummary{RunID: runID, StartedAt: time.Now().UTC(), Queue: queue}

	r.mu.Lock()
	r.status.CurrentRun = &summary
	r.status.RunsLastHour++
	r.mu.Unlock()
	r.setState(StateRunning, "")

	q := queue
	r.bus.Publish(Event{Worker: r.w.Name, Kind: KindCycleStart, RunID: runID,
		Cycle: &CycleInfo{Runtime: r.w.Runtime, Cwd: rc.RepoDir, Queue: &q}})

	status, timedOut := r.exec(ctx, rc, argv, runID, &summary)
	if timedOut {
		status = 124
	}

	outcome, explanation := r.rt.ClassifyExit(rc, status, r.lastRunFile())
	if explanation != "" {
		r.warn("%s", explanation)
	}

	switch status {
	case 0:
		r.log("cycle complete")
	case 124:
		r.log("cycle timed out after %ds — relay's lease will re-offer the task", r.w.MaxSecondsPerRun)
	default:
		r.log("cycle exited with status %d", status)
	}

	ended := time.Now().UTC()
	summary.EndedAt = &ended
	summary.Status = status
	summary.Outcome = outcome

	r.mu.Lock()
	r.status.CurrentRun = nil
	r.status.TotalCostUSD += summary.CostUSD
	r.status.TotalTokens += summary.Usage.Total()
	r.runs = append(r.runs, summary)
	// Bounded: a worker running for weeks must not accumulate an unbounded run
	// list in memory. events.ndjson keeps the full record.
	if len(r.runs) > 200 {
		r.runs = r.runs[len(r.runs)-200:]
	}
	r.mu.Unlock()

	r.bus.Publish(Event{Worker: r.w.Name, Kind: KindCycleEnd, RunID: runID,
		Cycle: &CycleInfo{
			Runtime: r.w.Runtime, Status: status, Outcome: outcome, Explanation: explanation,
			CostUSD: summary.CostUSD, Usage: usageOrNil(summary.Usage), NumTurns: summary.NumTurns,
			DurationMS: ended.Sub(summary.StartedAt).Milliseconds(),
		}})

	if outcome == outcomeBudget {
		r.noteBudgetKill()
	} else {
		writeCounter(r.budgetKillFile(), 0)
	}

	// Only a cycle that ran to completion can be said to have changed nothing.
	if status == 0 {
		r.noteAttentionStall(queue.AttentionKey())
	}
}

// exec runs the argv with the worker's repo as cwd, under its own wall-clock
// kill, streaming every line to the adapter's parser as it arrives.
//
// The timeout is enforced here rather than by the CLI: a hung session holds both
// this worker's lock and the task's relay lease until it is killed.
func (r *WorkerRunner) exec(ctx context.Context, rc *RunContext, argv []string, runID string, summary *RunSummary) (int, bool) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(r.w.MaxSecondsPerRun)*time.Second)
	defer cancel()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = rc.RepoDir
	// Its own process group, so the kill below reaches the session's children
	// too. The bash poller signalled only its direct child, which left a CLI's
	// subprocesses behind on a timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.warn("cannot open stdout: %v", err)
		return 1, false
	}
	// Two pipes rather than one shared fd: Go owns both, so Wait can close them
	// if a child outlives its parent. Both are merged into one ordered stream
	// below, which is what the bash poller's `2>&1 | tee` did.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.warn("cannot open stderr: %v", err)
		return 1, false
	}

	// The raw stream is kept per-run, exactly as the bash poller's last-run.out
	// was, because that is what the exit classifier reads afterwards.
	rawFH, err := os.Create(r.lastRunFile())
	if err != nil {
		r.warn("cannot open last-run.out: %v", err)
		return 1, false
	}
	defer rawFH.Close()
	raw := bufio.NewWriter(rawFH)
	defer raw.Flush()

	if err := cmd.Start(); err != nil {
		r.warn("cannot start %s: %v", argv[0], err)
		return 1, false
	}

	// Watchdog: SIGTERM the whole group at the deadline, SIGKILL it if that is
	// ignored. Nothing from this run may outlive the worker's timeout.
	finished := make(chan struct{})
	timedOut := make(chan struct{}, 1)
	go func() {
		select {
		case <-finished:
			return
		case <-runCtx.Done():
			if runCtx.Err() == context.DeadlineExceeded {
				select {
				case timedOut <- struct{}{}:
				default:
				}
			}
			signalGroup(cmd, syscall.SIGTERM)
			select {
			case <-finished:
			case <-time.After(10 * time.Second):
				signalGroup(cmd, syscall.SIGKILL)
			}
		}
	}()

	var wg sync.WaitGroup
	lines := make(chan string, 512)
	scan := func(rd io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(rd)
		// Stream lines are single JSON objects and can be large — a tool result
		// carrying a whole file arrives as one line.
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	go func() { wg.Wait(); close(lines) }()

	for line := range lines {
		raw.WriteString(line)
		raw.WriteByte('\n')
		for _, ev := range r.rt.ParseLine(line) {
			r.applySessionEvent(runID, summary, ev)
		}
	}
	raw.Flush()

	waitErr := cmd.Wait()
	close(finished)

	status := 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			status = ee.ExitCode()
			if status < 0 {
				status = 1 // killed by signal
			}
		} else {
			status = 1
		}
	}
	select {
	case <-timedOut:
		return status, true
	default:
	}
	return status, false
}

// signalGroup signals the whole process group, falling back to the process
// itself if the group is not available.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		cmd.Process.Signal(sig)
	}
}

// applySessionEvent publishes one in-session event and folds what it says into
// the run summary the cards read.
func (r *WorkerRunner) applySessionEvent(runID string, summary *RunSummary, ev SessionEvent) {
	switch ev.Type {
	case "init":
		summary.SessionID = ev.SessionID
		summary.Model = ev.Model
	case "tool_use":
		summary.ToolCalls++
		// First id wins: that is the claim. A subtask id passed later in the
		// same run says what the agent handed out, not what it is working on.
		if summary.TaskID == "" {
			summary.TaskID = taskIDFromTarget(ev.Target)
		}
	case "result":
		summary.CostUSD = ev.CostUSD
		summary.NumTurns = ev.NumTurns
		if ev.Usage != nil {
			summary.Usage = *ev.Usage
		}
	}
	r.mu.Lock()
	if r.status.CurrentRun != nil && r.status.CurrentRun.RunID == runID {
		copySummary := *summary
		r.status.CurrentRun = &copySummary
	}
	r.mu.Unlock()

	e := ev
	r.bus.Publish(Event{Worker: r.w.Name, Kind: KindSession, RunID: runID, Session: &e})
}

// taskIDFromTarget reads a task id out of a tool target that names one.
//
// An adapter renders a labelled tool argument as "key=value", and `task_id` is
// the one label that says which task a run is on. Nothing here validates the id
// or asks relay about it: an id this worker never saw an agent pass simply does
// not appear.
func taskIDFromTarget(target string) string {
	id, ok := strings.CutPrefix(target, "task_id=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(id)
}

// ── circuit breakers ─────────────────────────────────────────────────────────

// selfPause stops this worker at its next tick, with the reason and the way back
// in the log. The same mechanism a human uses (the PAUSED file), so stopping,
// restarting and `rm` all behave exactly as the readme documents.
func (r *WorkerRunner) selfPause(reason string) {
	os.WriteFile(r.pausedFile(), nil, 0o644)
	r.mu.Lock()
	r.status.Paused = true
	r.mu.Unlock()
	r.warn("PAUSED — %s\n  Resume with: rm %s\n  (relaunching relay-cli also clears it, along with this worker's run history.)",
		reason, r.pausedFile())
	r.setState(StatePaused, reason)
}

// usageOrNil keeps an all-zero usage out of the event stream, so a claude run —
// which reports dollars and no tokens — carries no empty token object.
func usageOrNil(u TokenUsage) *TokenUsage {
	if u.Total() == 0 {
		return nil
	}
	return &u
}

// noteBudgetKill — a limit that fires once is information; twice in a row is a
// wall. The first is always explained in full (the adapter wrote the text, and
// it is the adapter that knows whether the limit was a spend cap or a plan
// window); this only decides when explaining again has stopped being useful.
func (r *WorkerRunner) noteBudgetKill() {
	n := readCounter(r.budgetKillFile()) + 1
	writeCounter(r.budgetKillFile(), n)
	if maxBudgetKills > 0 && n >= maxBudgetKills {
		r.selfPause(fmt.Sprintf(`%d consecutive runs were cut off by a spend or usage limit.
  This worker will not make progress by trying again — every run restarts the
  same task and stops at the same point. Which limit it was, and how to raise it,
  is in the explanation logged with each of those runs; the other way out is to
  split the task in relay so a run can finish inside it, or to change this
  worker's ceilings in %s.`, n, displayConfigPath()))
	}
}

// noteAttentionStall — the fan-out failure mode worth breaking on.
//
// A parent sits in `attention` for two reasons. "A subtask moved" clears the
// moment the agent READS the parent, so it cannot repeat. An unresolved question
// or review addressed to the agent persists until the agent RESOLVES it — and if
// it cannot (its owner revoked resolve_subtask_handoffs, or the parent was taken
// over and re-delegated), nothing it does will ever clear it. Relay surfaces that
// to the human inbox as a stranded handoff, but the parent keeps appearing here,
// so this worker would relaunch a full CLI session against it on every eligible
// tick until its run ceiling ran out, every hour, indefinitely.
//
// The signature is precise: the same task ids in `attention` across consecutive
// cycles that COMPLETED. Requiring completion is what keeps a slow orchestrator
// out of it — a run that timed out did not get its chance to resolve anything,
// and the run ceiling is already that case's bound.
func (r *WorkerRunner) noteAttentionStall(ids string) {
	if ids == "-" {
		os.Remove(r.stallFile())
		return
	}
	prevIDs, prevN := "", 0
	if b, err := os.ReadFile(r.stallFile()); err == nil {
		parts := strings.Fields(string(b))
		if len(parts) == 2 {
			prevIDs = parts[0]
			prevN, _ = strconv.Atoi(parts[1])
		}
	}
	if prevIDs != ids {
		os.WriteFile(r.stallFile(), []byte(ids+" 1\n"), 0o644)
		return
	}
	n := prevN + 1
	os.WriteFile(r.stallFile(), []byte(fmt.Sprintf("%s %d\n", ids, n)), 0o644)
	if maxAttentionStall > 0 && n >= maxAttentionStall {
		r.selfPause(fmt.Sprintf(`task(s) %s have needed this agent's attention for %d consecutive completed runs, and nothing changed.
  That normally means a handoff addressed to this agent that it cannot resolve:
  check in relay that it still holds the parent task and still has the
  resolve_subtask_handoffs capability. Your Inbox should also be showing the
  handoff as stranded. Answer or review it there and this clears.`, ids, n))
	}
}
