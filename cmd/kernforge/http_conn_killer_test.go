package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPConnKillerForceClosesActiveConnection(t *testing.T) {
	var accepted atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1)
				_, _ = c.Read(buf)
			}(conn)
		}
	}()

	client := &http.Client{}
	killer := attachHTTPConnKiller(client)
	if killer == nil {
		t.Fatal("expected killer")
	}

	connected := make(chan net.Conn, 1)
	go func() {
		conn, err := client.Transport.(*http.Transport).DialContext(context.Background(), "tcp", ln.Addr().String())
		if err != nil {
			t.Errorf("DialContext: %v", err)
			close(connected)
			return
		}
		connected <- conn
	}()

	var conn net.Conn
	select {
	case conn = <-connected:
		if conn == nil {
			t.Fatal("dial failed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dial timed out")
	}
	defer conn.Close()

	killer.KillAll()

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("KillAll did not unblock connection read")
	}
}

func TestAwaitManagedAgentReplyAbandonsHungProvider(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan managedAgentReplyResult)
	forceClosed := make(chan struct{}, 1)

	started := time.Now()
	done := make(chan struct{})
	var gotAbandoned bool
	go func() {
		_, err, abandoned := awaitManagedAgentReply(requestCtx, context.Background(), resultCh, func() {
			forceClosed <- struct{}{}
		}, 80*time.Millisecond)
		if err != ErrRequestCanceled {
			t.Errorf("expected ErrRequestCanceled, got %v", err)
		}
		gotAbandoned = abandoned
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-forceClosed:
	case <-time.After(time.Second):
		t.Fatal("expected forceClose on cancel")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("await did not abandon")
	}
	if !gotAbandoned {
		t.Fatal("expected abandoned=true")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("abandon took too long: %s", time.Since(started))
	}
}

func TestOpenAICompatibleClientForceClosesTrackedConns(t *testing.T) {
	client := NewOpenAICompatibleClient("openrouter", "https://openrouter.ai/api/v1", "test-key")
	if client.connKiller == nil {
		t.Fatal("expected conn killer on openrouter client")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.WriteHeader(200)
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	started := make(chan struct{})
	go func() {
		close(started)
		resp, err := client.httpClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	client.ForceCloseConnections()
	cancel()
}
