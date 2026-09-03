package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeRuntime runs a shell command instead of a CLI, so the loop's own
// behaviour — streaming, timeouts, exit handling — can be tested without
// spending a token.
type fakeRuntime struct {
	script  string
	outcome string
}

func (f *fakeRuntime) Name() string                 { return "fake" }
func (f *fakeRuntime) Check() error                 { return nil }
func (f *fakeRuntime) ConfigFields() []runtimeField { return nil }
func (f *fakeRuntime) BuildCmd(rc *RunContext) ([]string, error) {
	return []string{"/bin/sh", "-c", f.script}, nil
}
func (f *fakeRuntime) ParseLine(line string) []SessionEvent {
	return []SessionEvent{{Type: "raw", Text: line}}
}
func (f *fakeRuntime) ClassifyExit(rc *RunContext, status int, out string) (string, string) {
	if f.outcome != "" {
		return f.outcome, ""
	}
	return defaultOutcome(status), ""
}

func newTestRunner(t *testing.T, rt Runtime, w *Worker) (*WorkerRunner, *Bus) {
	t.Helper()
	root := t.TempDir()
	if w.Name == "" {
		w.Name = "tw"
	}
	if w.MaxSecondsPerRun == 0 {
		w.MaxSecondsPerRun = 30
	}
	if w.RepoDir == "" {
		w.RepoDir = root
	}
	cfg := &Config{RelayDir: root, PollSeconds: defaultPollSeconds, Workers: []*Worker{w}}
	bus := NewBus(false)
	r := NewWorkerRunner(cfg, w, rt, bus, "rules", "")
	os.MkdirAll(r.Dir(), 0o755)
	if err := bus.OpenWorker(w.Name, r.Dir()); err != nil {
		t.Fatal(err)
	}
	return r, bus
}

// The cooldown's only job is to stop a relaunch treadmill after a run that fails
// or is refused instantly.
func TestRelaunchCooldownBlocksAnImmediateRelaunch(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{MaxRunsPerHour: 12})
	r.recordRun()
	if r.withinCeilings() {
		t.Fatal("a run recorded just now must block the next launch")
	}
	if r.Status().State != StateCooldown {
		t.Errorf("state = %q, want %q", r.Status().State, StateCooldown)
	}

	// Same worker, last run an hour ago: allowed, and outside the hourly window.
	os.WriteFile(r.runsFile(), []byte(fmt.Sprintf("%d\n", time.Now().Add(-2*time.Hour).Unix())), 0o644)
	if !r.withinCeilings() {
		t.Error("a run from two hours ago must not block anything")
	}
	if got := r.Status().RunsLastHour; got != 0 {
		t.Errorf("RunsLastHour = %d, want 0", got)
	}
}

// max_runs_per_hour is the only ceiling on how many sessions may start, so it is
// the one that actually caps spend.
func TestRunCeilingCountsARollingHour(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{MaxRunsPerHour: 3})
	now := time.Now()
	var lines []string
	for _, ago := range []time.Duration{90 * time.Minute, 50 * time.Minute, 40 * time.Minute, 30 * time.Minute} {
		lines = append(lines, fmt.Sprintf("%d", now.Add(-ago).Unix()))
	}
	os.WriteFile(r.runsFile(), []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	if r.withinCeilings() {
		t.Fatal("3 runs inside the hour with a ceiling of 3 must not launch")
	}
	if r.Status().State != StateCeiling {
		t.Errorf("state = %q, want %q", r.Status().State, StateCeiling)
	}
	if got := r.Status().RunsLastHour; got != 3 {
		t.Errorf("RunsLastHour = %d, want 3 (the 90-minute-old run is outside the window)", got)
	}
}

func TestZeroCeilingMeansNoCeiling(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{MaxRunsPerHour: 0})
	now := time.Now()
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("%d", now.Add(-time.Duration(i+2)*time.Minute).Unix()))
	}
	os.WriteFile(r.runsFile(), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	if !r.withinCeilings() {
		t.Fatal("max_runs_per_hour 0 is deliberately unbounded")
	}
}

// Two spend-cap kills in a row is a wall, not information: every run restarts
// the same task and stops at the same point.
func TestBudgetKillsPauseTheWorker(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{RuntimeConfig: map[string]string{"max_usd_per_run": "5"}})
	r.noteBudgetKill()
	if _, err := os.Stat(r.pausedFile()); err == nil {
		t.Fatal("one budget kill is information, not a wall")
	}
	r.noteBudgetKill()
	if _, err := os.Stat(r.pausedFile()); err != nil {
		t.Fatal("two consecutive budget kills must pause the worker")
	}
	if !r.Status().Paused {
		t.Error("status should report the pause")
	}
}

// The signature is precise: the SAME task ids across consecutive completed
// cycles. A different set restarts the count, and an empty set clears it.
func TestAttentionStallNeedsTheSameIDsThreeTimes(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{})
	r.noteAttentionStall("22")
	r.noteAttentionStall("22")
	if _, err := os.Stat(r.pausedFile()); err == nil {
		t.Fatal("paused too early")
	}
	r.noteAttentionStall("22")
	if _, err := os.Stat(r.pausedFile()); err != nil {
		t.Fatal("three consecutive completed cycles with the same attention set must pause")
	}
}

func TestAttentionStallResetsOnChange(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{})
	r.noteAttentionStall("22")
	r.noteAttentionStall("23") // a different parent — progress, not a stall
	r.noteAttentionStall("23")
	if _, err := os.Stat(r.pausedFile()); err == nil {
		t.Fatal("a changed attention set must restart the count")
	}
	r.noteAttentionStall("-") // queue cleared
	if _, err := os.Stat(r.stallFile()); err == nil {
		t.Error("an empty attention set must clear the stall record")
	}
}

// The loop must see everything the CLI writes, on both streams, in order.
func TestExecStreamsBothStreams(t *testing.T) {
	r, bus := newTestRunner(t, &fakeRuntime{script: `echo out-one; echo err-one 1>&2; echo out-two`}, &Worker{})
	rc := r.runContext()
	argv, _ := r.rt.BuildCmd(rc)
	summary := &RunSummary{RunID: "run-1"}
	status, timedOut := r.exec(context.Background(), rc, argv, "run-1", summary)
	if status != 0 || timedOut {
		t.Fatalf("status=%d timedOut=%v", status, timedOut)
	}
	var seen []string
	for _, e := range bus.History() {
		if e.Kind == KindSession && e.Session != nil {
			seen = append(seen, e.Session.Text)
		}
	}
	joined := strings.Join(seen, ",")
	for _, want := range []string{"out-one", "err-one", "out-two"} {
		if !strings.Contains(joined, want) {
			t.Errorf("stream lost %q (saw %q)", want, joined)
		}
	}
	// The raw stream is kept per-run because that is what the classifier reads.
	raw, err := os.ReadFile(r.lastRunFile())
	if err != nil || !strings.Contains(string(raw), "out-two") {
		t.Errorf("last-run.out = %q, %v", raw, err)
	}
}

func TestExecNonZeroExitIsReported(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{script: `exit 7`}, &Worker{})
	rc := r.runContext()
	argv, _ := r.rt.BuildCmd(rc)
	status, timedOut := r.exec(context.Background(), rc, argv, "run-1", &RunSummary{})
	if status != 7 || timedOut {
		t.Fatalf("status=%d timedOut=%v, want 7/false", status, timedOut)
	}
}

// A hung session holds both this worker's lock and the task's relay lease until
// it is killed — and its children must die with it, which the bash poller did
// not guarantee.
func TestExecTimeoutKillsTheWholeProcessGroup(t *testing.T) {
	marker := t.TempDir() + "/child-alive"
	script := fmt.Sprintf(`( sleep 30; touch %s ) & sleep 30`, marker)
	r, _ := newTestRunner(t, &fakeRuntime{script: script}, &Worker{MaxSecondsPerRun: 1})
	rc := r.runContext()
	argv, _ := r.rt.BuildCmd(rc)

	start := time.Now()
	status, timedOut := r.exec(context.Background(), rc, argv, "run-1", &RunSummary{})
	elapsed := time.Since(start)

	if !timedOut {
		t.Fatalf("expected a timeout, got status %d after %v", status, elapsed)
	}
	if elapsed > 15*time.Second {
		t.Errorf("timeout took %v — the kill did not land promptly", elapsed)
	}
	// If the backgrounded child outlived the group kill it would create the
	// marker ~30s from now; give it a moment and confirm it never does.
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a child process survived its session's timeout")
	}
}

// The PAUSED file is the documented way a human pauses a worker, and a tick must
// honour it without probing or spending anything.
func TestPausedWorkerSkipsItsTick(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{})
	os.WriteFile(r.pausedFile(), nil, 0o644)
	r.tick(context.Background())
	if s := r.Status(); s.State != StatePaused || !s.Paused {
		t.Fatalf("state = %+v, want paused", s)
	}
	// A paused tick must not have taken the lock, or a resume would deadlock.
	if _, err := os.Stat(r.lockDir()); err == nil {
		t.Error("a paused tick should not hold the cycle lock")
	}
}

// The task a run is on is read off the calls the agent was seen making, and the
// first id it names is the claim. A subtask handed out later in the same run
// says what the agent delegated, not what it is working on.
func TestRunSummaryTakesTheFirstTaskIDItSees(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{})
	summary := &RunSummary{RunID: "run-1"}
	for _, ev := range []SessionEvent{
		{Type: "tool_use", Tool: "Read", Target: "internal/invite/token.go"},
		{Type: "tool_use", Tool: "mcp__relay__claim_task", Target: "task_id=T-413"},
		{Type: "tool_use", Tool: "mcp__relay__create_task", Target: "task_id=T-418"},
	} {
		r.applySessionEvent("run-1", summary, ev)
	}
	if summary.TaskID != "T-413" {
		t.Errorf("TaskID = %q, want %q", summary.TaskID, "T-413")
	}
	if summary.ToolCalls != 3 {
		t.Errorf("ToolCalls = %d, want 3", summary.ToolCalls)
	}
}

// A run whose agent never named a task carries none. Relay owns task state, and
// inventing an id here would be worse than a blank.
func TestRunSummaryWithoutATaskIDStaysEmpty(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{})
	summary := &RunSummary{RunID: "run-1"}
	for _, target := range []string{"", "bash -lc 'go test ./...'", "subtask_id=T-9", "description=do a thing"} {
		r.applySessionEvent("run-1", summary, SessionEvent{Type: "tool_use", Tool: "Bash", Target: target})
	}
	if summary.TaskID != "" {
		t.Errorf("TaskID = %q, want empty", summary.TaskID)
	}
}

// The hourly window rolls rather than resetting on the hour, so the only reset
// time a worker at its ceiling can be shown is when its oldest run ages out.
func TestCeilingResetsWhenTheOldestRunAgesOut(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{MaxRunsPerHour: 3})
	now := time.Now()
	oldest := now.Add(-50 * time.Minute)
	var lines []string
	for _, at := range []time.Time{now.Add(-90 * time.Minute), oldest, now.Add(-20 * time.Minute)} {
		lines = append(lines, fmt.Sprintf("%d", at.Unix()))
	}
	os.WriteFile(r.runsFile(), []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	r.withinCeilings()
	got := r.Status().CeilingResetsAt
	if got == nil {
		t.Fatal("CeilingResetsAt is nil, want the oldest in-window run plus an hour")
	}
	want := oldest.Add(time.Hour)
	if d := got.Sub(want); d > time.Second || d < -time.Second {
		t.Errorf("CeilingResetsAt = %v, want %v (the 90-minute-old run is outside the window)", got, want)
	}
}

// A worker with no ceiling has nothing to free up, so it shows no reset time.
func TestNoCeilingMeansNoResetTime(t *testing.T) {
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{MaxRunsPerHour: 0})
	os.WriteFile(r.runsFile(), []byte(fmt.Sprintf("%d\n", time.Now().Add(-10*time.Minute).Unix())), 0o644)
	r.withinCeilings()
	if got := r.Status().CeilingResetsAt; got != nil {
		t.Errorf("CeilingResetsAt = %v, want nil", got)
	}
}
