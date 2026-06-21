package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// daemonStreamEvent is one Server-Sent Event published to connected IDE clients.
// It carries observe-only progress about MCP RPC traffic the daemon proxies. It
// never carries an edit authority: an IDE that watches the stream sees what the
// agent loop did, it does not drive it. The Data map is JSON-encoded as the SSE
// data payload; Event is the SSE event-type line.
type daemonStreamEvent struct {
	// ID is a monotonically increasing per-hub sequence used as the SSE id line,
	// so a client can correlate and (in a future slice) resume with Last-Event-ID.
	ID uint64 `json:"seq"`
	// Event is the SSE "event:" type (for example rpc_request, rpc_response,
	// hello, keepalive). Clients dispatch on it.
	Event string `json:"event"`
	// Data is the JSON-encoded SSE data payload.
	Data map[string]any `json:"data,omitempty"`
}

// daemonStreamSubscriber is a single connected SSE client. ch is buffered so a
// slow or stalled reader is dropped (its channel closed) rather than blocking the
// publisher and back-pressuring the agent loop. Streaming must never stall RPC.
type daemonStreamSubscriber struct {
	ch     chan daemonStreamEvent
	closed atomic.Bool
}

// daemonStreamHub fans out observe-only progress events to connected SSE clients.
// It is safe for concurrent use. The hub holds no edit authority and performs no
// workspace mutation; it only mirrors RPC progress that already happened. A nil
// hub is a valid no-op (publish does nothing), so the streaming surface stays
// fully opt-in and the daemon's existing behavior is unchanged when no client is
// connected.
type daemonStreamHub struct {
	mu          sync.Mutex
	subscribers map[*daemonStreamSubscriber]struct{}
	seq         uint64
	// buffer bounds each subscriber's queue. A subscriber that falls this far
	// behind is dropped to protect the publisher path.
	buffer int
}

// newDaemonStreamHub builds a hub with the given per-subscriber buffer depth. A
// non-positive buffer is clamped to a small default so a subscriber always has
// some slack before being treated as stalled.
func newDaemonStreamHub(buffer int) *daemonStreamHub {
	if buffer <= 0 {
		buffer = 32
	}
	return &daemonStreamHub{
		subscribers: map[*daemonStreamSubscriber]struct{}{},
		buffer:      buffer,
	}
}

// subscribe registers a new subscriber and returns it. The caller must call
// unsubscribe when the client disconnects.
func (h *daemonStreamHub) subscribe() *daemonStreamSubscriber {
	sub := &daemonStreamSubscriber{ch: make(chan daemonStreamEvent, h.buffer)}
	h.mu.Lock()
	h.subscribers[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

// unsubscribe removes a subscriber and closes its channel exactly once. It is
// safe to call multiple times.
func (h *daemonStreamHub) unsubscribe(sub *daemonStreamSubscriber) {
	if sub == nil {
		return
	}
	h.mu.Lock()
	if _, ok := h.subscribers[sub]; ok {
		delete(h.subscribers, sub)
		if sub.closed.CompareAndSwap(false, true) {
			close(sub.ch)
		}
	}
	h.mu.Unlock()
}

// closeAll closes every subscriber channel and clears the registry. It is called
// on daemon shutdown so connected stream readers observe a clean end-of-stream
// (their range/select over the channel returns) instead of a hung socket.
func (h *daemonStreamHub) closeAll() {
	if h == nil {
		return
	}
	h.mu.Lock()
	for sub := range h.subscribers {
		if sub.closed.CompareAndSwap(false, true) {
			close(sub.ch)
		}
	}
	h.subscribers = map[*daemonStreamSubscriber]struct{}{}
	h.mu.Unlock()
}

// subscriberCount reports the number of connected clients. Used by /health to
// expose stream observability without leaking client detail.
func (h *daemonStreamHub) subscriberCount() int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	count := len(h.subscribers)
	h.mu.Unlock()
	return count
}

// publish fans an event out to every connected subscriber. It assigns the event
// sequence id under the lock so ids are strictly increasing. A subscriber whose
// buffer is full is dropped (closed and removed) rather than blocked: the
// publisher path is on the RPC critical path, so it must never wait on a slow
// IDE. A nil hub is a no-op so callers need no guard.
func (h *daemonStreamHub) publish(event string, data map[string]any) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.seq++
	evt := daemonStreamEvent{ID: h.seq, Event: event, Data: data}
	stalled := make([]*daemonStreamSubscriber, 0)
	for sub := range h.subscribers {
		select {
		case sub.ch <- evt:
		default:
			stalled = append(stalled, sub)
		}
	}
	for _, sub := range stalled {
		delete(h.subscribers, sub)
		if sub.closed.CompareAndSwap(false, true) {
			close(sub.ch)
		}
	}
	h.mu.Unlock()
}

// daemonStreamKeepAliveInterval is how often an idle stream emits a keepalive
// comment so intermediaries and the client detect a live connection. It is short
// enough to keep proxies from idling the socket but long enough to stay quiet.
const daemonStreamKeepAliveInterval = 20 * time.Second

// handleStream serves the token-authed GET /stream Server-Sent Events endpoint.
// It is the IDE-facing observe-only channel: it streams MCP RPC progress that the
// daemon already proxied. It does NOT accept edits and does NOT bypass the
// edit/permission/review gates; an IDE uses it to watch, and routes any
// apply/cancel back through the normal /rpc tools/call path that still enforces
// every gate. The endpoint requires the daemon token (query parameter for
// EventSource clients that cannot set headers, or an Authorization: Bearer
// header). It is fully opt-in: when no client connects nothing changes.
func (d *kernforgeDaemonServer) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !d.streamRequestAuthorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if d.stream == nil {
		http.Error(w, "stream is not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sub := d.stream.subscribe()
	defer d.stream.unsubscribe(sub)

	// Announce the connection so a client can confirm the stream is live and
	// learn the daemon identity it is observing. This carries no secret.
	writeDaemonSSEEvent(w, daemonStreamEvent{
		Event: "hello",
		Data: map[string]any{
			"pid":     os.Getpid(),
			"version": currentVersion(),
		},
	})
	flusher.Flush()

	keepAlive := time.NewTicker(daemonStreamKeepAliveInterval)
	defer keepAlive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client disconnected; unsubscribe runs via defer.
			return
		case evt, open := <-sub.ch:
			if !open {
				// Hub dropped us (stalled) or is shutting down.
				return
			}
			if !writeDaemonSSEEvent(w, evt) {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			// SSE comment line keeps the socket warm without a typed event.
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// streamRequestAuthorized validates the daemon token for a stream request. It
// accepts the token via the "token" query parameter (EventSource cannot set
// custom headers) or an "Authorization: Bearer <token>" header. The comparison
// mirrors the other daemon endpoints; an empty configured token never matches.
func (d *kernforgeDaemonServer) streamRequestAuthorized(r *http.Request) bool {
	if strings.TrimSpace(d.token) == "" {
		return false
	}
	if strings.TrimSpace(r.URL.Query().Get("token")) == d.token {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth != "" {
		const prefix = "Bearer "
		if len(auth) > len(prefix) && strings.EqualFold(auth[:len(prefix)], prefix) {
			if strings.TrimSpace(auth[len(prefix):]) == d.token {
				return true
			}
		}
	}
	return false
}

// writeDaemonSSEEvent frames a single event in the SSE wire format and writes it.
// It emits the id, event-type, and a single JSON data line. It returns false if
// the write fails (the client went away). Multi-line data is avoided by encoding
// the payload as compact JSON, which never contains a raw newline.
func writeDaemonSSEEvent(w http.ResponseWriter, evt daemonStreamEvent) bool {
	var builder strings.Builder
	if evt.ID > 0 {
		fmt.Fprintf(&builder, "id: %d\n", evt.ID)
	}
	eventType := strings.TrimSpace(evt.Event)
	if eventType == "" {
		eventType = "message"
	}
	fmt.Fprintf(&builder, "event: %s\n", eventType)
	payload := evt.Data
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte("{}")
	}
	fmt.Fprintf(&builder, "data: %s\n\n", encoded)
	if _, err := fmt.Fprint(w, builder.String()); err != nil {
		return false
	}
	return true
}
