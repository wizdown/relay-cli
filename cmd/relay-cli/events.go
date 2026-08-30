// The event bus: one stream of everything every worker does, fanned out to the
// browser, to disk, and to the human-readable worker log.
//
// Three sinks, because they answer different questions:
//
//	worker.log      what happened, in prose, for someone reading an archived log
//	                after the fact. Same line format as the bash poller's, so
//	                `tail -f` and everything in logs/ still look the same.
//	events.ndjson   the structured record, one JSON object per line, for replay
//	                and for anything that wants to parse a run later.
//	subscribers     live SSE feeds. In-memory, lossy on purpose (see Publish).
//
// Every string that enters this file is scrubbed of credentials on the way in,
// once, rather than at each of the dozens of call sites that produce one.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Event kinds.
const (
	KindLog        = "log"         // a human line, also written to worker.log
	KindPoll       = "poll"        // one probe result — including the empty ones
	KindCycleStart = "cycle_start" // a CLI session is starting
	KindCycleEnd   = "cycle_end"   // it finished, with its classification
	KindSession    = "session"     // one parsed event from inside a running session
)

// Worker states, as shown on the cards.
const (
	StateStarting = "starting"
	StateIdle     = "idle"
	StatePolling  = "polling"
	StateRunning  = "running"
	StateCooldown = "cooldown"
	StateCeiling  = "ceiling"
	StatePaused   = "paused"
	StateProbeErr = "probe_failing"
	StateStopped  = "stopped"
)

// SessionEvent is one thing that happened inside a CLI run.
//
// This is the whole reason relay-cli exists. The bash poller ran claude with
// --output-format json, which prints ONE object at the very end: for fifteen
// minutes you saw nothing, then a wall of JSON. The runtime adapters now stream
// events, and these are what a stream line becomes.
type SessionEvent struct {
	Type      string  `json:"type"` // init | assistant | thinking | tool_use | tool_result | result | raw
	Text      string  `json:"text,omitempty"`
	Tool      string  `json:"tool,omitempty"`
	Target    string  `json:"target,omitempty"` // the interesting argument, already summarised
	IsError   bool    `json:"is_error,omitempty"`
	Model     string  `json:"model,omitempty"`
	SessionID string  `json:"session_id,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
	NumTurns  int     `json:"num_turns,omitempty"`
}

// CycleInfo describes one CLI run, at its start and again at its end.
type CycleInfo struct {
	Runtime     string      `json:"runtime"`
	Cwd         string      `json:"cwd,omitempty"`
	Queue       *QueueState `json:"queue,omitempty"`
	Status      int         `json:"status"`
	Outcome     string      `json:"outcome,omitempty"` // ok | timeout | error | budget_exhausted
	Explanation string      `json:"explanation,omitempty"`
	CostUSD     float64     `json:"cost_usd,omitempty"`
	DurationMS  int64       `json:"duration_ms,omitempty"`
	NumTurns    int         `json:"num_turns,omitempty"`
	Result      string      `json:"result,omitempty"`
}

// Event is one item on the bus.
type Event struct {
	Seq    uint64    `json:"seq"`
	Time   time.Time `json:"time"`
	Worker string    `json:"worker"`
	Kind   string    `json:"kind"`
	RunID  string    `json:"run_id,omitempty"`

	Text  string `json:"text,omitempty"`
	Level string `json:"level,omitempty"` // info | warn | error

	Poll    *QueueState   `json:"poll,omitempty"`
	Error   string        `json:"error,omitempty"`
	Cycle   *CycleInfo    `json:"cycle,omitempty"`
	Session *SessionEvent `json:"session,omitempty"`
}

// ringSize is how much history one worker keeps in memory for a browser that
// connects late or refreshes. The full record is on disk in events.ndjson; this
// only bounds what a long-running fleet costs in RAM.
const ringSize = 1500

// maxEventText bounds one event. An agent can produce a very large tool result,
// and neither the ring nor an SSE frame should carry all of it.
const maxEventText = 4000

type workerSink struct {
	ring    []Event
	logFile *bufio.Writer
	logFH   *os.File
	ndjson  *bufio.Writer
	ndFH    *os.File
}

// Bus fans events out. Its mutex covers everything: the sequence counter, the
// per-worker rings, the file writers and the subscriber set. Event volume is low
// (a busy fleet produces a few per second), so one lock is the right trade
// against the bugs a finer-grained scheme would buy.
type Bus struct {
	mu      sync.Mutex
	seq     uint64
	sinks   map[string]*workerSink
	subs    map[int]chan Event
	nextSub int
	echo    bool // also print human lines to stdout
}

func NewBus(echo bool) *Bus {
	return &Bus{sinks: map[string]*workerSink{}, subs: map[int]chan Event{}, echo: echo}
}

// OpenWorker creates the two files a worker writes. Called before the worker's
// loop starts, so a browser attaching immediately still finds the files present
// (and empty) rather than missing.
func (b *Bus) OpenWorker(name, dir string) error {
	logFH, err := os.OpenFile(dir+"/worker.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	ndFH, err := os.OpenFile(dir+"/events.ndjson", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logFH.Close()
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sinks[name] = &workerSink{
		logFH: logFH, logFile: bufio.NewWriter(logFH),
		ndFH: ndFH, ndjson: bufio.NewWriter(ndFH),
	}
	return nil
}

// CloseWorker flushes and closes a worker's files.
func (b *Bus) CloseWorker(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sinks[name]
	if s == nil {
		return
	}
	s.logFile.Flush()
	s.ndjson.Flush()
	s.logFH.Close()
	s.ndFH.Close()
	delete(b.sinks, name)
}

// Publish stamps, scrubs, records and fans out one event.
func (b *Bus) Publish(e Event) {
	e.Time = time.Now().UTC()
	e.Text = Scrub(truncate(e.Text, maxEventText))
	e.Error = Scrub(truncate(e.Error, maxEventText))
	if e.Session != nil {
		s := *e.Session
		s.Text = Scrub(truncate(s.Text, maxEventText))
		s.Target = Scrub(truncate(s.Target, 400))
		e.Session = &s
	}
	if e.Cycle != nil {
		c := *e.Cycle
		c.Explanation = Scrub(truncate(c.Explanation, maxEventText))
		c.Result = Scrub(truncate(c.Result, maxEventText))
		e.Cycle = &c
	}

	b.mu.Lock()
	b.seq++
	e.Seq = b.seq
	sink := b.sinks[e.Worker]
	if sink != nil {
		sink.ring = append(sink.ring, e)
		if len(sink.ring) > ringSize {
			sink.ring = sink.ring[len(sink.ring)-ringSize:]
		}
		if line, err := json.Marshal(e); err == nil {
			sink.ndjson.Write(line)
			sink.ndjson.WriteByte('\n')
			sink.ndjson.Flush()
		}
		if text := humanLine(e); text != "" {
			stamped := fmt.Sprintf("%s [%s] %s\n", e.Time.Format("2006-01-02T15:04:05Z"), e.Worker, text)
			sink.logFile.WriteString(stamped)
			sink.logFile.Flush()
			if b.echo {
				os.Stdout.WriteString(stamped)
			}
		}
	}
	for id, ch := range b.subs {
		select {
		case ch <- e:
		default:
			// A browser that cannot keep up is dropped rather than allowed to
			// block a worker's loop. It reconnects on its own and re-snapshots,
			// so the cost of this is a gap no one sees.
			close(ch)
			delete(b.subs, id)
		}
	}
	b.mu.Unlock()
}

// Subscribe returns a channel of future events and a function to stop.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	id := b.nextSub
	b.nextSub++
	b.subs[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if existing, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(existing)
		}
		b.mu.Unlock()
	}
}

// History returns the retained events for every worker, in bus order.
func (b *Bus) History() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Event
	for _, s := range b.sinks {
		out = append(out, s.ring...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// Flush writes anything buffered. Called on shutdown, before logs are archived.
func (b *Bus) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.sinks {
		s.logFile.Flush()
		s.ndjson.Flush()
	}
}

// humanLine renders an event for worker.log, or returns "" for events that
// belong only in the structured record.
//
// What lands here is a judgement about what a person reading an archived log
// six hours later needs. Empty polls do not: the bash poller kept them silent so
// an idle worker cost nothing, log noise included, and that stays true — the UI
// shows every poll from the event stream instead.
func humanLine(e Event) string {
	switch e.Kind {
	case KindLog:
		return e.Text
	case KindPoll:
		// A successful poll — including the empty one that is the common case —
		// stays out of worker.log. The bash poller kept it silent so an idle
		// worker cost nothing, log noise included, and that is still true: the
		// dashboard shows every poll from the event stream instead.
		//
		// A FAILED poll is the opposite: it is the whole reason someone opens an
		// archived log. One line, carrying the consecutive count, because a single
		// failure and the tenth in a row mean different things.
		if e.Error != "" {
			return e.Text + ": " + e.Error
		}
		return ""
	case KindCycleStart:
		if e.Cycle == nil {
			return ""
		}
		q := e.Cycle.Queue
		if q == nil {
			q = &QueueState{}
		}
		return fmt.Sprintf("cycle start: runtime=%s resume=%d attention=%d todo=%d cwd=%s",
			e.Cycle.Runtime, q.Resume, q.Attention, q.Todo, e.Cycle.Cwd)
	case KindSession:
		return sessionLine(e.Session)
	}
	return ""
}

// sessionLine is the readable rendering of one in-session event. Indented, so an
// archived log reads as a worker's narration with each run's activity nested
// under its cycle-start line.
func sessionLine(s *SessionEvent) string {
	if s == nil {
		return ""
	}
	switch s.Type {
	case "init":
		return fmt.Sprintf("  · session %s started (model %s)", s.SessionID, s.Model)
	case "assistant":
		return "  · " + oneLine(s.Text, 400)
	case "tool_use":
		if s.Target != "" {
			return fmt.Sprintf("  → %s  %s", s.Tool, oneLine(s.Target, 200))
		}
		return "  → " + s.Tool
	case "tool_result":
		if s.IsError {
			return "  ✗ " + oneLine(s.Text, 200)
		}
		return ""
	case "raw":
		return "  | " + oneLine(s.Text, 400)
	}
	return ""
}

// oneLine flattens and clips text for a log line. Multi-line assistant prose in
// the middle of a log destroys its scannability; the full text is in
// events.ndjson for anyone who wants it.
func oneLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ⏎ "))
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return truncate(s, n)
}
