package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		{Type: "tool_use", Tool: "mcp__relay__open_task", Target: "task_id=T-413"},
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

// queueStub is a relay whose get_available_tasks answers with a fixed
// structuredContent, so a whole tick can be driven without a live server.
func queueStub(t *testing.T, structured string) *httptest.Server {
	t.Helper()
	return mcpStub(t, `{"jsonrpc":"2.0","id":2,"result":{"structuredContent":`+structured+`}}`, true)
}

// tickAgainst runs one tick with the worker's probe pointed at a stub queue.
func tickAgainst(t *testing.T, structured string) *WorkerRunner {
	t.Helper()
	r, _ := tickResultAgainst(t, structured)
	return r
}

// tickResultAgainst is tickAgainst plus what the tick told the poll ladder.
func tickResultAgainst(t *testing.T, structured string) (*WorkerRunner, tickResult) {
	t.Helper()
	srv := queueStub(t, structured)
	defer srv.Close()
	// `true` exits 0 without spending anything; a launch shows up as a recorded
	// run either way, which is what these tests are counting.
	r, _ := newTestRunner(t, &fakeRuntime{script: "true"}, &Worker{})
	r.prober = NewProber(srv.URL)
	return r, r.tick(context.Background())
}

// The gate this whole change is about. Relay reports what it is holding even
// when it is withholding all of it, so a worker that gates on the counts alone
// launches a full CLI session — the most expensive thing it can do — against a
// queue whose every claimable row relay has already said it will refuse.
func TestAtClaimLimitDoesNotLaunchARun(t *testing.T) {
	r := tickAgainst(t, `{"resume_total":2,"attention_total":0,"todo_total":5,"at_limit":true,"attention":[]}`)

	if got := len(r.readRuns()); got != 0 {
		t.Fatalf("%d run(s) launched while every claimable row was withheld — the counts are not the gate", got)
	}
	s := r.Status()
	if s.State != StateAtLimit {
		t.Errorf("state = %q, want %q — an idle worker with a backlog has to say why", s.State, StateAtLimit)
	}
	if !strings.Contains(s.Detail, "7 task(s) withheld") {
		t.Errorf("detail = %q, want the withheld count", s.Detail)
	}
}

// The deadlock the gate must not cause. A task needing attention is already the
// agent's own: it is worked with get_task_context and needs no free slot, and it
// is how an orchestrator at its ceiling keeps its fan-out moving. Withholding a
// launch for one would strand the whole fan-out until a lease lapsed.
func TestAtClaimLimitStillLaunchesForAttention(t *testing.T) {
	r := tickAgainst(t, `{"resume_total":2,"attention_total":1,"todo_total":5,"at_limit":true,"attention":[{"id":7}]}`)

	if got := len(r.readRuns()); got != 1 {
		t.Fatalf("%d run(s) launched for a task needing attention, want 1 — its fan-out stalls otherwise", got)
	}
	if s := r.Status(); s.State != StateIdle {
		t.Errorf("state after a run = %q, want %q", s.State, StateIdle)
	}
}

// A relay that does not send the flag withholds nothing, so every count it
// reports is work this worker may take. Reading the absence as "at limit" would
// silently stop the fleet against an older server.
func TestQueueWithoutAtLimitStillLaunches(t *testing.T) {
	r := tickAgainst(t, `{"resume_total":0,"attention_total":0,"todo_total":1,"attention":[]}`)

	if got := len(r.readRuns()); got != 1 {
		t.Fatalf("%d run(s) launched for an ordinary todo queue, want 1", got)
	}
}

// An empty queue is still an empty queue: no run, and no at-limit reason on a
// card that has nothing to explain.
func TestEmptyQueueLaunchesNothingAndSaysNothing(t *testing.T) {
	r := tickAgainst(t, `{"resume_total":0,"attention_total":0,"todo_total":0,"attention":[]}`)

	if got := len(r.readRuns()); got != 0 {
		t.Fatalf("%d run(s) launched on an empty queue", got)
	}
	s := r.Status()
	if s.State != StateIdle || s.Detail != "" {
		t.Errorf("state = %q detail = %q, want a plain idle", s.State, s.Detail)
	}
}

// ── the poll ladder ─────────────────────────────────────────────────────────

// The whole adaptive rule, as the loop applies it. A worker is fast while it
// has work and for the warm window after it, then doubles its wait on each
// quiet poll until it reaches the idle rate and stays there.
func TestPollIntervalWarmsThenCools(t *testing.T) {
	const (
		base = 30 * time.Second
		idle = 300 * time.Second
	)
	for _, tc := range []struct {
		name      string
		cur       time.Duration
		sinceWork time.Duration
		want      time.Duration
	}{
		{"work just now stays fast", base, 0, base},
		{"inside the warm window stays fast", base, pollWarmWindow - time.Second, base},
		{"backed off, then work, is fast again", 240 * time.Second, time.Second, base},
		{"first quiet poll past the window doubles", base, pollWarmWindow, 60 * time.Second},
		{"and doubles again", 60 * time.Second, time.Hour, 120 * time.Second},
		{"and again", 120 * time.Second, time.Hour, 240 * time.Second},
		{"clamped at the idle rate", 240 * time.Second, time.Hour, idle},
		{"and stays there", idle, time.Hour, idle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextPollInterval(base, idle, tc.cur, tc.sinceWork); got != tc.want {
				t.Errorf("nextPollInterval(cur=%s, sinceWork=%s) = %s, want %s", tc.cur, tc.sinceWork, got, tc.want)
			}
		})
	}
}

// The rule is total: an idle rate no slower than the base one returns the base
// rate however long the worker has been quiet. The config refuses such a pair
// now, so this holds the function rather than a setting anyone can write.
func TestEqualRatesNeverBackOff(t *testing.T) {
	const base = 30 * time.Second
	for _, since := range []time.Duration{0, pollWarmWindow, 24 * time.Hour} {
		if got := nextPollInterval(base, base, base, since); got != base {
			t.Errorf("quiet for %s: interval = %s, want %s — equal rates mean one rate", since, got, base)
		}
	}
}

// Only a poll that actually happened says anything about how busy this agent
// is. A tick that was paused, locked out, inside a ceiling or unable to reach
// relay leaves the ladder where it was — which is also what stops a dead
// endpoint being retried at the fast rate by a worker that had backed off.
func TestOnlyAPollThatHappenedMovesTheLadder(t *testing.T) {
	work := `{"resume_total":0,"attention_total":0,"todo_total":1,"attention":[]}`
	empty := `{"resume_total":0,"attention_total":0,"todo_total":0,"attention":[]}`
	withheld := `{"resume_total":2,"attention_total":0,"todo_total":5,"at_limit":true,"attention":[]}`

	if _, res := tickResultAgainst(t, work); res != tickWorked {
		t.Errorf("a poll with work returned %v, want tickWorked", res)
	}
	if _, res := tickResultAgainst(t, empty); res != tickQuiet {
		t.Errorf("an empty poll returned %v, want tickQuiet", res)
	}
	// A worker parked at its claim limit polls just as pointlessly as one with
	// an empty queue, and cools for the same reason.
	if _, res := tickResultAgainst(t, withheld); res != tickQuiet {
		t.Errorf("a withheld poll returned %v, want tickQuiet", res)
	}

	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{})
	os.WriteFile(r.pausedFile(), nil, 0o644)
	if res := r.tick(context.Background()); res != tickHold {
		t.Errorf("a paused tick returned %v, want tickHold", res)
	}

	// A probe that cannot reach relay learned nothing about the queue.
	f, _ := newTestRunner(t, &fakeRuntime{}, &Worker{Name: "unreachable"})
	f.prober = NewProber("http://127.0.0.1:1")
	if res := f.tick(context.Background()); res != tickHold {
		t.Errorf("a failed probe returned %v, want tickHold", res)
	}
}

// Both directions are announced, because both are things a reader watching the
// dashboard has to be able to explain. A fleet whose polls quietly thinned out
// looks stopped; one that speeds back up with no line looks like it did so for
// no reason.
func TestBothDirectionsOfTheRateChangeAreLogged(t *testing.T) {
	const (
		base = 30 * time.Second
		idle = 300 * time.Second
	)

	slower := pollRateNote(base, 60*time.Second, 5*time.Minute)
	for _, want := range []string{"nothing to act on", "5m0s", "60s"} {
		if !strings.Contains(slower, want) {
			t.Errorf("slowing down logged %q, which does not say %q", slower, want)
		}
	}

	faster := pollRateNote(idle, base, 0)
	for _, want := range []string{"work again", "30s"} {
		if !strings.Contains(faster, want) {
			t.Errorf("speeding up logged %q, which does not say %q", faster, want)
		}
	}

	// The common tick changes nothing, and a line every poll would bury the two
	// above in the noise they exist to cut through.
	if note := pollRateNote(idle, idle, time.Hour); note != "" {
		t.Errorf("an unchanged rate logged %q, want silence", note)
	}
}

// ── an owner pause is a state, not a failure ────────────────────────────────

// pausedRelay refuses every exchange the way relay's principal gate refuses a
// paused agent's credential: 403 with the code, from the initialize a poll
// starts with.
func pausedRelay(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"errorCode":"relay_agent_paused","errorDescription":"This agent is paused by its owner."}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pausedRunner(t *testing.T) *WorkerRunner {
	t.Helper()
	r, _ := newTestRunner(t, &fakeRuntime{script: "true"}, &Worker{})
	r.prober = NewProber(pausedRelay(t).URL)
	return r
}

// The whole point. A pause is relay answering, so the breaker must not count it:
// counted, the tenth poll wrote a local PAUSED file that outlives the pause, and
// resuming the agent in the console left the worker down until someone deleted
// that file by hand.
func TestOwnerPauseNeverTripsTheProbeBreaker(t *testing.T) {
	r := pausedRunner(t)
	for i := 0; i < maxProbeFailures+2; i++ {
		if res := r.tick(context.Background()); res != tickQuiet {
			t.Fatalf("poll %d returned %v, want tickQuiet — a pause is polled through, not held", i+1, res)
		}
	}
	if _, err := os.Stat(r.pausedFile()); err == nil {
		t.Fatal("a pause in relay wrote a local PAUSED file, which outlives the pause and needs a human to remove")
	}
	s := r.Status()
	if s.ProbeFailures != 0 {
		t.Errorf("probe_failures = %d, want 0 — a refusal that names the pause proves the credential works", s.ProbeFailures)
	}
	if s.State != StateOwnerPaused {
		t.Errorf("state = %q, want %q", s.State, StateOwnerPaused)
	}
	if !strings.Contains(s.Detail, "paused by owner") {
		t.Errorf("detail = %q, want it to name the owner's pause", s.Detail)
	}
	if s.LastPollError != "" {
		t.Errorf("last_poll_error = %q, want empty — the dashboard would read it as a broken endpoint", s.LastPollError)
	}
	if len(r.readRuns()) != 0 {
		t.Error("a paused agent launched a run")
	}
}

// A pause that logged every poll would write the same line into worker.log a few
// thousand times overnight. Once on the way in, once on the way out.
func TestOwnerPauseIsAnnouncedOnceAndSoIsTheResume(t *testing.T) {
	r, bus := newTestRunner(t, &fakeRuntime{script: "true"}, &Worker{})
	r.prober = NewProber(pausedRelay(t).URL)
	for i := 0; i < 3; i++ {
		r.tick(context.Background())
	}

	// Now relay serves the credential again, with an empty queue.
	free := queueStub(t, `{"resume_total":0,"attention_total":0,"todo_total":0,"attention":[]}`)
	defer free.Close()
	r.prober = NewProber(free.URL)
	r.tick(context.Background())

	var pauses, resumes int
	for _, e := range bus.History() {
		if e.Kind != KindLog {
			continue
		}
		if strings.Contains(e.Text, "paused by owner") {
			pauses++
		}
		if strings.Contains(e.Text, "resumed by its owner") {
			resumes++
		}
	}
	if pauses != 1 {
		t.Errorf("logged the pause %d times over 3 polls, want 1", pauses)
	}
	if resumes != 1 {
		t.Errorf("logged the resume %d times, want 1 — a fleet coming back is the line a watcher waits for", resumes)
	}
}

// The credential is fine, so the worker picks straight back up: no restart, no
// file to delete, and the very next poll launches.
func TestAResumeIsPickedUpWithNoHumanAction(t *testing.T) {
	r := pausedRunner(t)
	r.tick(context.Background())

	free := queueStub(t, `{"resume_total":0,"attention_total":0,"todo_total":1,"attention":[]}`)
	defer free.Close()
	r.prober = NewProber(free.URL)
	if res := r.tick(context.Background()); res != tickWorked {
		t.Fatalf("the poll after a resume returned %v, want tickWorked", res)
	}
	if got := len(r.readRuns()); got != 1 {
		t.Fatalf("%d run(s) after the resume, want 1", got)
	}
	if s := r.Status(); s.State == StateOwnerPaused {
		t.Error("the worker is still showing the pause after relay resumed it")
	}
}

// The opposite refusal. A deleted agent's credential is never served again, so
// it counts like any other probe failure and trips the breaker — and the pause
// wired above must not swallow it.
func TestADeletedAgentStillTripsTheProbeBreaker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"errorCode":"relay_agent_not_found","errorDescription":"No such agent."}`))
	}))
	defer srv.Close()
	r, _ := newTestRunner(t, &fakeRuntime{}, &Worker{})
	r.prober = NewProber(srv.URL)
	for i := 0; i < maxProbeFailures; i++ {
		r.tick(context.Background())
	}
	if _, err := os.Stat(r.pausedFile()); err != nil {
		t.Fatal("a removed agent did not trip the probe breaker, so the worker polls a dead credential forever")
	}
	if d := r.Status().Detail; !strings.Contains(d, "this agent is not in relay") {
		t.Errorf("the breaker says %q, which sends the operator after a credential that is not the problem", d)
	}
}
