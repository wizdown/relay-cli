// relay-probe in Go: ask relay a question over plain HTTP, with no model
// involved.
//
// This is the piece that makes an idle worker free. It speaks MCP JSON-RPC to
// the connector endpoint directly, so the poll loop can answer "is there work?"
// every tick without booting a CLI — booting one costs tens of thousands of
// tokens of context before it can even ask.
//
// The probe speaks MCP over plain HTTP with no dependencies: this is
// why relay-cli needs neither curl nor jq on PATH.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultProtocolVersion = "2025-11-25"

// QueueState is the answer to one poll: how much work relay is holding for this
// agent, in the three buckets the loop acts on.
//
// THREE buckets, worked in that order. `attention` is relay's token-free wake
// for an orchestrator: a task this agent is HOLDING whose subtasks moved or
// asked it something. It exists precisely because such a parent is in neither of
// the other two — its lease is live, so it is not `resume`, and it is
// In-Progress, so it is not `todo`. A worker that reads only resume+todo never
// wakes a supervisor until its lease lapses, which is the whole fan-out loop
// stalled behind a timeout.
type QueueState struct {
	Resume       int      `json:"resume"`
	Attention    int      `json:"attention"`
	Todo         int      `json:"todo"`
	AttentionIDs []string `json:"attention_ids"`
}

// Total is what the loop gates on: any bucket non-empty means launch.
func (q QueueState) Total() int { return q.Resume + q.Attention + q.Todo }

// AttentionKey is the stall detector's fingerprint — the same task ids in
// `attention` across consecutive completed cycles. Sorted and joined so the
// comparison is order-independent, and "-" for none so an empty value is never
// mistaken for a missing file.
func (q QueueState) AttentionKey() string {
	if len(q.AttentionIDs) == 0 {
		return "-"
	}
	ids := append([]string(nil), q.AttentionIDs...)
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// Prober holds one worker's connector URL and an HTTP client. One per worker:
// the URL embeds that agent's secret.
type Prober struct {
	endpoint string
	client   *http.Client
	protocol string
}

func NewProber(endpoint string) *Prober {
	return &Prober{
		endpoint: endpoint,
		protocol: envOr("RELAY_MCP_PROTOCOL_VERSION", defaultProtocolVersion),
		client: &http.Client{
			Timeout: time.Duration(envSeconds("RELAY_PROBE_MAX_TIME", 60)) * time.Second,
		},
	}
}

// GetAvailableTasks runs one full MCP exchange: initialize, initialized,
// tools/call, then close the session. Every error it returns has already been
// scrubbed of the credential.
func (p *Prober) GetAvailableTasks(ctx context.Context) (QueueState, error) {
	var q QueueState

	// 1. initialize — mints the session id every later request must carry.
	initBody := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": p.protocol,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "relay-cli", "version": version},
		},
	}
	// Only the header matters here: the session id every later request carries.
	_, hdr, err := p.post(ctx, "", initBody)
	if err != nil {
		return q, fmt.Errorf("initialize failed: %w", err)
	}
	session := hdr.Get("Mcp-Session-Id")
	if session == "" {
		return q, errors.New("initialize returned no Mcp-Session-Id")
	}
	defer p.closeSession(session)

	// 2. notifications/initialized — the handshake the SDK expects before any
	// tool call. Its reply is not interesting; its absence breaks the next step.
	if err := p.notify(ctx, session, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	}); err != nil {
		return q, fmt.Errorf("notifications/initialized failed: %w", err)
	}

	// 3. tools/call.
	callBody := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "get_available_tasks", "arguments": map[string]any{}},
	}
	payload, _, err := p.post(ctx, session, callBody)
	if err != nil {
		return q, fmt.Errorf("get_available_tasks failed: %w", err)
	}

	var env struct {
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				ResumeTotal    int `json:"resume_total"`
				AttentionTotal int `json:"attention_total"`
				TodoTotal      int `json:"todo_total"`
				Attention      []struct {
					ID json.RawMessage `json:"id"`
				} `json:"attention"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return q, fmt.Errorf("unparseable response: %s", Scrub(truncate(string(payload), 400)))
	}
	if len(env.Error) > 0 || env.Result.IsError {
		detail := string(env.Error)
		if detail == "" {
			detail = truncate(string(payload), 400)
		}
		return q, fmt.Errorf("get_available_tasks returned an error: %s", Scrub(detail))
	}

	sc := env.Result.StructuredContent
	q.Resume, q.Attention, q.Todo = sc.ResumeTotal, sc.AttentionTotal, sc.TodoTotal
	for _, a := range sc.Attention {
		// Relay may send an id as a number or a string; both render the same
		// here, and the value is only ever compared to itself.
		id := strings.Trim(string(a.ID), `"`)
		if id != "" {
			q.AttentionIDs = append(q.AttentionIDs, id)
		}
	}
	return q, nil
}

// post sends one JSON-RPC *request* and returns the unwrapped payload. A request
// is answered inline, so anything but 200 is an error; so is a transport
// failure. The distinction the caller cares about — probe failure versus an
// honestly empty queue — is preserved by returning an error only for the former.
func (p *Prober) post(ctx context.Context, session string, body any) ([]byte, http.Header, error) {
	raw, hdr, code, err := p.send(ctx, session, body)
	if err != nil {
		return nil, hdr, err
	}
	if code != http.StatusOK {
		return nil, hdr, fmt.Errorf("HTTP %d: %s", code, Scrub(truncate(string(raw), 400)))
	}
	return unwrapSSE(raw), hdr, nil
}

// notify sends a JSON-RPC *notification*, which by definition has no reply: the
// Streamable HTTP transport has the server acknowledge one with 202 Accepted and
// an empty body. So any 2xx is a success here.
//
// Holding a notification to the 200 a request must return fails every poll
// against a spec-correct relay, with the uninformative "HTTP 202:" that an empty
// body leaves behind. The shell probe this replaced discarded this status
// entirely; this keeps that behaviour without also swallowing a real 4xx/5xx.
func (p *Prober) notify(ctx context.Context, session string, body any) error {
	raw, _, code, err := p.send(ctx, session, body)
	if err != nil {
		return err
	}
	if code < 200 || code > 299 {
		return fmt.Errorf("HTTP %d: %s", code, Scrub(truncate(string(raw), 400)))
	}
	return nil
}

// send performs the exchange and reports the status code without judging it —
// what counts as success differs between a request and a notification.
func (p *Prober) send(ctx context.Context, session string, body any) ([]byte, http.Header, int, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("bad endpoint: %v", Scrub(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
		req.Header.Set("Mcp-Protocol-Version", p.protocol)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, 0, errors.New(Scrub(err.Error()))
	}
	defer resp.Body.Close()

	// Bounded read: a misconfigured endpoint answering with something enormous
	// must not become this process's memory problem.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.Header, resp.StatusCode, errors.New(Scrub(err.Error()))
	}
	return raw, resp.Header, resp.StatusCode, nil
}

// closeSession is best-effort by design: the exchange has already produced its
// answer, and a relay that will not close a session is not a reason to report
// this poll as failed.
func (p *Prober) closeSession(session string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", session)
	req.Header.Set("Mcp-Protocol-Version", p.protocol)
	if resp, err := p.client.Do(req); err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// unwrapSSE pulls the payload out of a Streamable-HTTP reply. The server answers
// as SSE, so the JSON arrives in a `data:` frame; a plain JSON body is passed
// through untouched.
func unwrapSSE(raw []byte) []byte {
	if !bytes.Contains(raw, []byte("data: ")) {
		return raw
	}
	var last []byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data: ")) {
			last = bytes.TrimPrefix(line, []byte("data: "))
		}
	}
	if last == nil {
		return raw
	}
	return bytes.TrimRight(last, "\r")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envSeconds(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
