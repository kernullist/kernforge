package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type blockingCloser struct {
	block chan struct{}
}

func (b *blockingCloser) Close() error {
	<-b.block
	return nil
}

type hangingReader struct {
	closed chan struct{}
}

func (h *hangingReader) Read(p []byte) (int, error) {
	<-h.closed
	return 0, io.EOF
}

func (h *hangingReader) Close() error {
	select {
	case <-h.closed:
	default:
		close(h.closed)
	}
	return nil
}

func TestCloseHTTPBodyWithTimeoutDoesNotHangForever(t *testing.T) {
	hangForever := make(chan struct{})
	body := &blockingCloser{block: hangForever}
	started := time.Now()
	closeHTTPBodyWithTimeout(body, 50*time.Millisecond)
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("closeHTTPBodyWithTimeout took too long: %s", time.Since(started))
	}
}

func TestContextReadCloserReturnsPromptlyOnCancel(t *testing.T) {
	inner := &hangingReader{closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	body := newContextReadCloser(ctx, inner)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 32)
		_, err := body.Read(buf)
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancel error from blocked read")
		}
	case <-time.After(time.Second):
		t.Fatal("contextReadCloser did not return after cancel")
	}
}

func TestArmHTTPResponseCancelUnblocksBlockedBodyRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	stop := armHTTPResponseCancel(ctx, resp)
	defer stop()
	body := newContextReadCloser(ctx, resp.Body)
	defer closeHTTPBodyWithTimeout(body, httpBodyCloseTimeout)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		_, readErr := body.Read(buf)
		done <- readErr
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected read to fail after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked body read did not unblock after cancel")
	}
}

func TestCloseHTTPBodyWithTimeoutClosesNormalBody(t *testing.T) {
	body := io.NopCloser(strings.NewReader("ok"))
	closeHTTPBodyWithTimeout(body, 100*time.Millisecond)
}
