// The dashboard's HTTP surface: three routes, no mutation.
//
// relay-cli is read-only on purpose. There is no route here that can pause a
// worker, launch a run, or change a ceiling — not a hidden one, not a disabled
// button, none. A page served on a developer's machine that can spend money is a
// different thing to reason about than a page that can only show what already
// happened, and this version is deliberately the second one. Pausing still works
// the way the readme documents: touch the worker's PAUSED file, and the next
// tick picks it up.
//
// It also binds to loopback only, and no flag changes that. Session output can
// contain anything the agent read — repo contents, task text, whatever was in
// the files it opened.
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

//go:embed ui/index.html
var uiFS embed.FS

// Snapshot is the whole visible state of the fleet at one moment: enough to
// draw the page from nothing, which is what a fresh load and a reconnect both
// need.
type Snapshot struct {
	Version    string    `json:"version"`
	StartedAt  time.Time `json:"started_at"`
	Now        time.Time `json:"now"`
	ConfigPath string    `json:"config_path"`
	RelayDir   string    `json:"relay_dir"`
	// Fleet-wide, so it belongs here rather than repeated on every worker.
	PollSeconds float64        `json:"poll_seconds"`
	Workers     []WorkerStatus `json:"workers"`
	Events      []Event        `json:"events,omitempty"`
}

type Server struct {
	sup *Supervisor
	mux *http.ServeMux
}

func NewServer(sup *Supervisor) *Server {
	s := &Server{sup: sup, mux: http.NewServeMux()}
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/snapshot", s.handleSnapshot)
	s.mux.HandleFunc("/api/stream", s.handleStream)
	return s
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, "ui missing from binary", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page is self-contained by construction; saying so means a stray
	// external reference fails loudly in development instead of silently
	// reaching the network on someone else's machine.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src data:")
	w.Write(page)
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snap := Snapshot{
		Version:     version,
		StartedAt:   s.sup.startedAt,
		Now:         time.Now().UTC(),
		ConfigPath:  s.sup.cfg.Path,
		RelayDir:    s.sup.cfg.RelayDir,
		PollSeconds: s.sup.cfg.PollSeconds,
		Workers:     s.sup.Statuses(),
	}
	// The dashboard's card refresh asks for state alone; a full load asks for the history too.
	if r.URL.Query().Get("events") != "0" {
		snap.Events = s.sup.bus.History()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(snap)
}

// handleStream is the live feed. SSE rather than a WebSocket: the traffic is
// one-way, it is a few lines of net/http, and the browser reconnects on its own
// — which pairs exactly with the bus dropping a subscriber that falls behind.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, unsubscribe := s.sup.bus.Subscribe()
	defer unsubscribe()

	// A comment frame keeps proxies and sleeping laptops from silently dropping
	// an idle connection — an idle fleet can go minutes between events.
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return // dropped for falling behind; the browser will reconnect
			}
			line, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// Listen binds loopback on the requested port, falling forward to the next free
// one rather than refusing to start. A dashboard that will not open because
// something else holds the port is a worse outcome than a dashboard on 7718.
func Listen(port int) (net.Listener, error) {
	var lastErr error
	for p := port; p < port+20; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return ln, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("no free port in %d–%d: %v", port, port+19, lastErr)
}
