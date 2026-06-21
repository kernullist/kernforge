package main

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestStreamDaemon builds a minimal daemon server wired with a stream hub and
// an httptest server exposing /health and /stream. It avoids the heavy runtime
// construction (no MCP server, no scheduler) because the stream surface only
// needs the token and the hub.
func newTestStreamDaemon(t *testing.T) (*kernforgeDaemonServer, *httptest.Server) {
	t.Helper()
	daemon := &kernforgeDaemonServer{
		token:  "stream-token",
		stream: newDaemonStreamHub(8),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", daemon.handleHealth)
	mux.HandleFunc("/stream", daemon.handleStream)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Cleanup(daemon.stream.closeAll)
	return daemon, srv
}

func TestDaemonStreamRequiresToken(t *testing.T) {
	_, srv := newTestStreamDaemon(t)

	resp, err := http.Get(srv.URL + "/stream")
	if err != nil {
		t.Fatalf("get stream without token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	bad, err := http.Get(srv.URL + "/stream?token=wrong")
	if err != nil {
		t.Fatalf("get stream with wrong token: %v", err)
	}
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", bad.StatusCode)
	}
}

func TestDaemonStreamAuthorizesViaHeader(t *testing.T) {
	daemon, srv := newTestStreamDaemon(t)
	if !daemon.streamRequestAuthorized(httptestRequestWithBearer(srv.URL, "stream-token")) {
		t.Fatalf("expected bearer header to authorize")
	}
	if daemon.streamRequestAuthorized(httptestRequestWithBearer(srv.URL, "nope")) {
		t.Fatalf("expected wrong bearer header to be rejected")
	}
	// An empty configured token must never match.
	empty := &kernforgeDaemonServer{token: ""}
	if empty.streamRequestAuthorized(httptestRequestWithBearer(srv.URL, "")) {
		t.Fatalf("expected empty token never to authorize")
	}
}

func httptestRequestWithBearer(url string, token string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, url+"/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestDaemonStreamFramesHelloAndPublishedEvent(t *testing.T) {
	daemon, srv := newTestStreamDaemon(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream?token=stream-token", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 stream, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected event-stream content type, got %q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	hello := readSSEEvent(t, reader)
	if hello.event != "hello" {
		t.Fatalf("expected hello event first, got %q", hello.event)
	}

	// Wait until the server has registered our subscriber, then publish.
	waitForStreamSubscribers(t, daemon, 1)
	daemon.publishRPCObservation("rpc_response", kernforgeDaemonRPCRequest{
		Workspace: `C:\ws`,
		Message: map[string]any{
			"id":     float64(7),
			"method": "tools/call",
			"params": map[string]any{"name": "kernforge_status"},
		},
	}, map[string]any{"result": map[string]any{"isError": false}}, true, nil)

	got := readSSEEvent(t, reader)
	if got.event != "rpc_response" {
		t.Fatalf("expected rpc_response event, got %q", got.event)
	}
	if !strings.Contains(got.data, `"tool":"kernforge_status"`) {
		t.Fatalf("expected tool name in data, got %q", got.data)
	}
	if !strings.Contains(got.data, `"method":"tools/call"`) {
		t.Fatalf("expected method in data, got %q", got.data)
	}
	if !strings.Contains(got.data, `"respond":true`) {
		t.Fatalf("expected respond flag in data, got %q", got.data)
	}
	if got.id == "" {
		t.Fatalf("expected an SSE id line, got none")
	}
}

func TestDaemonStreamDropsSubscriberOnClientDisconnect(t *testing.T) {
	daemon, srv := newTestStreamDaemon(t)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream?token=stream-token", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	reader := bufio.NewReader(resp.Body)
	_ = readSSEEvent(t, reader) // hello
	waitForStreamSubscribers(t, daemon, 1)

	// Client disconnects; the server must unsubscribe.
	cancel()
	resp.Body.Close()
	waitForStreamSubscribers(t, daemon, 0)
}

func TestDaemonStreamHubDropsStalledSubscriber(t *testing.T) {
	hub := newDaemonStreamHub(2)
	sub := hub.subscribe()
	if hub.subscriberCount() != 1 {
		t.Fatalf("expected one subscriber, got %d", hub.subscriberCount())
	}
	// Fill the buffer (2) plus one overflow event; the overflow drops the sub.
	hub.publish("a", nil)
	hub.publish("b", nil)
	hub.publish("c", nil)
	if hub.subscriberCount() != 0 {
		t.Fatalf("expected stalled subscriber to be dropped, count=%d", hub.subscriberCount())
	}
	if !sub.closed.Load() {
		t.Fatalf("expected dropped subscriber channel to be closed")
	}
}

func TestDaemonStreamHubNilIsNoOp(t *testing.T) {
	var hub *daemonStreamHub
	// None of these must panic on a nil hub.
	hub.publish("x", map[string]any{"k": "v"})
	if hub.subscriberCount() != 0 {
		t.Fatalf("expected nil hub to report zero subscribers")
	}
	hub.closeAll()
}

func TestMCPResponseIsErrorExtraction(t *testing.T) {
	if _, ok := mcpResponseIsError(nil); ok {
		t.Fatalf("nil response should report ok=false")
	}
	if _, ok := mcpResponseIsError(map[string]any{"error": map[string]any{}}); ok {
		t.Fatalf("error response without result should report ok=false")
	}
	isErr, ok := mcpResponseIsError(map[string]any{"result": map[string]any{"isError": true}})
	if !ok || !isErr {
		t.Fatalf("expected isError=true ok=true, got %v %v", isErr, ok)
	}
	isErr, ok = mcpResponseIsError(map[string]any{"result": map[string]any{}})
	if !ok || isErr {
		t.Fatalf("expected isError=false ok=true, got %v %v", isErr, ok)
	}
}

type sseEvent struct {
	id    string
	event string
	data  string
}

// readSSEEvent reads one SSE event (terminated by a blank line) from the reader.
// It tolerates and skips comment (": ...") keepalive lines.
func readSSEEvent(t *testing.T, reader *bufio.Reader) sseEvent {
	t.Helper()
	var evt sseEvent
	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out reading SSE event")
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE line: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if evt.event != "" || evt.data != "" {
				return evt
			}
			// blank separator before any field; keep reading.
		case strings.HasPrefix(line, ": "):
			// keepalive comment, ignore.
		case strings.HasPrefix(line, "id: "):
			evt.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			evt.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			evt.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func waitForStreamSubscribers(t *testing.T, daemon *kernforgeDaemonServer, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if daemon.stream.subscriberCount() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d stream subscribers, have %d", want, daemon.stream.subscriberCount())
}
