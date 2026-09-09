package main

import (
	"context"
	"encoding/json"
	"errors"
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

// atLimitQueue is relay's answer to an agent holding its parallel-claim limit:
// both claimable buckets withheld, their counts still honest, and the flag that
// is the only way to tell that apart from a queue that is merely capped.
const atLimitQueue = `{"jsonrpc":"2.0","id":2,"result":{"structuredContent":` +
	`{"resume_total":1,"attention_total":2,"todo_total":3,"at_limit":true,"attention":[{"id":23}]}}}`

func TestProbeReadsAtLimit(t *testing.T) {
	srv := mcpStub(t, atLimitQueue, true)
	defer srv.Close()
	q, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !q.AtLimit {
		t.Fatal("at_limit was not read off the response")
	}
	// Total is unchanged: relay still reports everything it holds.
	if q.Total() != 6 {
		t.Errorf("Total() = %d, want 6 — the flag must not change what is counted", q.Total())
	}
	// Actionable is the part that matters. `attention` needs no free slot, so it
	// survives; both claimable buckets are a launch that could not claim anything.
	if q.Actionable() != 2 {
		t.Errorf("Actionable() = %d, want 2 (attention only)", q.Actionable())
	}
	if q.Withheld() != 4 {
		t.Errorf("Withheld() = %d, want 4 (resume + todo)", q.Withheld())
	}
}

// A relay that never sends the field is one that never withholds, so the absence
// has to read as "everything I was given is mine to take" — not as an at-limit
// worker that would then never launch again.
func TestProbeWithoutAtLimitOffersEverything(t *testing.T) {
	srv := mcpStub(t, threeBuckets, true)
	defer srv.Close()
	q, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if q.AtLimit {
		t.Error("at_limit defaulted to true when the field was absent")
	}
	if q.Actionable() != q.Total() || q.Actionable() != 6 {
		t.Errorf("Actionable() = %d, want 6 — an older relay withholds nothing", q.Actionable())
	}
	if q.Withheld() != 0 {
		t.Errorf("Withheld() = %d, want 0", q.Withheld())
	}
}

// At the limit with nothing needing attention there is nothing a session could
// do, however long the backlog: this is the case the gate exists for.
func TestQueueStateAtLimitWithNoAttentionIsNotActionable(t *testing.T) {
	q := QueueState{Resume: 2, Attention: 0, Todo: 9, AtLimit: true}
	if q.Actionable() != 0 {
		t.Errorf("Actionable() = %d, want 0 — every claimable row here is one relay would refuse", q.Actionable())
	}
	if q.Total() != 11 {
		t.Errorf("Total() = %d, want 11 — withheld work is still work, and the cards say so", q.Total())
	}
	if q.Withheld() != 11 {
		t.Errorf("Withheld() = %d, want 11", q.Withheld())
	}
}

// refusingRelay answers the very first exchange — the initialize a poll starts
// with — the way relay's principal gate refuses a credential it will not serve:
// a status and one of its error envelopes, before any tool call happens.
func refusingRelay(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const pausedEnvelope = `{"errorCode":"relay_agent_paused",` +
	`"errorDescription":"This agent is paused by its owner, so its credential is not being served."}`

// The status and the code both survive the trip, because the loop branches on
// them: flattening them into a string is what made a pause read as a broken
// credential.
func TestProbeSurfacesTheStatusAndErrorCode(t *testing.T) {
	srv := refusingRelay(t, http.StatusForbidden, pausedEnvelope)
	_, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
	if err == nil {
		t.Fatal("a refused credential must still be an error")
	}
	var pe *ProbeError
	if !errors.As(err, &pe) {
		t.Fatalf("want a *ProbeError the caller can read, got %T: %v", err, err)
	}
	if pe.Status != http.StatusForbidden || pe.Code != "relay_agent_paused" {
		t.Errorf("got status %d code %q, want 403 / relay_agent_paused", pe.Status, pe.Code)
	}
	if !strings.Contains(pe.Detail, "paused by its owner") {
		t.Errorf("the envelope's description was dropped: %q", pe.Detail)
	}
	if !AgentPaused(err) {
		t.Error("AgentPaused did not recognise relay's pause refusal")
	}
	if AgentNotFound(err) {
		t.Error("a pause was read as a deleted agent")
	}
}

// The deleted-agent refusal is the opposite case, and the two must never be
// confused: one waits for a resume, the other trips the breaker.
func TestProbeTellsAPauseFromADeletedAgent(t *testing.T) {
	srv := refusingRelay(t, http.StatusForbidden,
		`{"errorCode":"relay_agent_not_found","errorDescription":"No such agent."}`)
	_, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
	if !AgentNotFound(err) {
		t.Fatalf("AgentNotFound did not recognise the refusal: %v", err)
	}
	if AgentPaused(err) {
		t.Error("a deleted agent was read as a pause, so its credential would never trip the breaker")
	}
}

// The check is the status AND the code. A relay too old to send either still
// answers 403 for reasons that are genuine failures, and an unrelated host can
// answer anything at all — neither may be waved through as a pause.
func TestProbeDoesNotInventAPause(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"403 with no envelope", http.StatusForbidden, "forbidden"},
		{"403 with another code", http.StatusForbidden, `{"errorCode":"forbidden"}`},
		{"the code on the wrong status", http.StatusUnauthorized, pausedEnvelope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := refusingRelay(t, tc.status, tc.body)
			_, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
			if err == nil {
				t.Fatal("want an error")
			}
			if AgentPaused(err) {
				t.Errorf("%v was read as an owner pause", err)
			}
		})
	}
}

// A refusal is printed to a log and served to a browser like any other probe
// error, so the credential must not ride along in the description relay echoes.
func TestProbeRefusalsAreScrubbed(t *testing.T) {
	InstallSecrets([]*Worker{{Endpoint: "https://relay.example/relay/mcp/c/wzh_supersecretvalue"}})
	defer InstallSecrets(nil)

	srv := refusingRelay(t, http.StatusForbidden,
		`{"errorCode":"relay_agent_paused","errorDescription":"paused: https://relay.example/relay/mcp/c/wzh_supersecretvalue"}`)
	_, err := NewProber(srv.URL).GetAvailableTasks(context.Background())
	if !AgentPaused(err) {
		t.Fatalf("want the pause refusal, got %v", err)
	}
	if strings.Contains(err.Error(), "wzh_supersecretvalue") {
		t.Fatalf("the credential leaked into a refusal: %v", err)
	}
}
