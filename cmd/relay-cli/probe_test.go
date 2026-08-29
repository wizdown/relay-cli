package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mcpStub is a relay that answers the three-step handshake the way the real one
// does: a session id on initialize, then the tool result as an SSE data frame.
func mcpStub(t *testing.T, toolReply string, asSSE bool) *httptest.Server {
	t.Helper()
	var sawInitialized bool
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(200)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			Method string `json:"method"`
		}
		json.Unmarshal(body, &msg)

		switch msg.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-123")
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25"}}`))
		case "notifications/initialized":
			sawInitialized = true
			// A notification has no reply: the real relay acknowledges it with
			// 202 Accepted and an empty body, so the stub must too.
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			if !sawInitialized {
				t.Error("tools/call arrived before notifications/initialized")
			}
			if r.Header.Get("Mcp-Session-Id") != "sess-123" {
				t.Errorf("tools/call did not carry the session id: %q", r.Header.Get("Mcp-Session-Id"))
			}
			if asSSE {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: message\ndata: " + toolReply + "\n\n"))
			} else {
				w.Write([]byte(toolReply))
			}
		default:
			w.WriteHeader(400)
		}
	}))
}

const threeBuckets = `{"jsonrpc":"2.0","id":2,"result":{"structuredContent":{"resume_total":1,"attention_total":2,"todo_total":3,"attention":[{"id":23},{"id":"9"}]}}}`

func TestProbeReadsAllThreeBuckets(t *testing.T) {
	for _, sse := range []bool{true, false} {
		srv := mcpStub(t, threeBuckets, sse)
		q, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
		srv.Close()
		if err != nil {
			t.Fatalf("sse=%v: %v", sse, err)
		}
		if q.Resume != 1 || q.Attention != 2 || q.Todo != 3 {
			t.Errorf("sse=%v: buckets = %+v", sse, q)
		}
		if q.Total() != 6 {
			t.Errorf("sse=%v: Total() = %d, want 6", sse, q.Total())
		}
		// Sorted and joined, so the stall detector compares like with like
		// whatever order relay listed them in.
		if got := q.AttentionKey(); got != "23,9" {
			t.Errorf("sse=%v: AttentionKey() = %q", sse, got)
		}
	}
}

func TestProbeEmptyQueueIsNotAnError(t *testing.T) {
	srv := mcpStub(t, `{"jsonrpc":"2.0","id":2,"result":{"structuredContent":{"resume_total":0,"attention_total":0,"todo_total":0,"attention":[]}}}`, true)
	defer srv.Close()
	q, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
	if err != nil {
		t.Fatalf("an empty queue must not read as a probe failure: %v", err)
	}
	if q.Total() != 0 || q.AttentionKey() != "-" {
		t.Errorf("got %+v / %q", q, q.AttentionKey())
	}
}

func TestProbeToolErrorIsAFailure(t *testing.T) {
	srv := mcpStub(t, `{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"credential revoked"}}`, true)
	defer srv.Close()
	if _, err := NewProber(srv.URL).GetAvailableTasks(context.Background()); err == nil {
		t.Fatal("a tool-level error must fail the probe so the breaker can count it")
	}
}

func TestProbeHTTPErrorIsAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", 401)
	}))
	defer srv.Close()
	_, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want an HTTP 401 failure, got %v", err)
	}
}

// The 202 Accepted that every spec-correct relay returns for the handshake
// notification is a success, not a probe failure — holding it to the 200 a
// request must return failed every poll, and the empty body left nothing behind
// but "HTTP 202:" to debug with.
func TestProbeAcceptsNotificationAccepted(t *testing.T) {
	srv := mcpStub(t, threeBuckets, true)
	defer srv.Close()
	if _, err := NewProber(srv.URL).GetAvailableTasks(context.Background()); err != nil {
		t.Fatalf("202 on notifications/initialized must not fail the poll: %v", err)
	}
}

// A notification is still an exchange with the relay, so a genuine failure
// status on it has to fail the poll rather than being waved through with the 202.
func TestProbeNotificationErrorIsAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			Method string `json:"method"`
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &msg)
		if msg.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "sess-123")
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
			return
		}
		http.Error(w, "session expired", 401)
	}))
	defer srv.Close()
	_, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want an HTTP 401 failure from the notification, got %v", err)
	}
}

// A probe failure is reported to a log and to a browser, so the credential must
// not survive the trip — including when the server echoes the URL back.
func TestProbeErrorsAreScrubbed(t *testing.T) {
	InstallSecrets([]*Worker{{Endpoint: "https://relay.example/relay/mcp/c/wzh_supersecretvalue"}})
	defer InstallSecrets(nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such connector: https://relay.example/relay/mcp/c/wzh_supersecretvalue", 404)
	}))
	defer srv.Close()

	_, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "wzh_supersecretvalue") {
		t.Fatalf("the credential leaked into a probe error: %v", err)
	}
}
