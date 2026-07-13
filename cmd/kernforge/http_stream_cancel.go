package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const httpBodyCloseTimeout = 2 * time.Second

// closeHTTPBodyWithTimeout closes an HTTP response body but does not wait forever.
// After cancel, some transports (especially HTTP/2) can block in Body.Close while
// finishing stream cleanup; that left the interactive UI stuck on "Canceling...".
func closeHTTPBodyWithTimeout(body io.Closer, timeout time.Duration) {
	if body == nil {
		return
	}
	if timeout <= 0 {
		timeout = httpBodyCloseTimeout
	}
	done := make(chan struct{})
	go func() {
		_ = body.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

// contextReadCloser wraps a response body so a blocked Read returns promptly when
// ctx is canceled, even if the underlying transport is slow to deliver EOF after
// Close.
type contextReadCloser struct {
	ctx    context.Context
	inner  io.ReadCloser
	closed atomic.Bool
}

func newContextReadCloser(ctx context.Context, inner io.ReadCloser) io.ReadCloser {
	if ctx == nil {
		ctx = context.Background()
	}
	if inner == nil {
		return io.NopCloser(strings.NewReader(""))
	}
	return &contextReadCloser{ctx: ctx, inner: inner}
}

func (r *contextReadCloser) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		_ = r.Close()
		return 0, err
	}
	type result struct {
		n   int
		err error
	}
	// Copy into a private buffer so an abandoned Read goroutine cannot race on p
	// after we return early due to cancel.
	buf := make([]byte, len(p))
	ch := make(chan result, 1)
	go func() {
		n, err := r.inner.Read(buf)
		ch <- result{n, err}
	}()
	select {
	case <-r.ctx.Done():
		_ = r.Close()
		// Do not wait forever for the stuck Read; drain in the background.
		go func() { <-ch }()
		return 0, r.ctx.Err()
	case res := <-ch:
		copy(p, buf[:res.n])
		if res.err != nil {
			if ctxErr := r.ctx.Err(); ctxErr != nil {
				return res.n, ctxErr
			}
		}
		return res.n, res.err
	}
}

func (r *contextReadCloser) Close() error {
	if r == nil || r.inner == nil {
		return nil
	}
	if r.closed.Swap(true) {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- r.inner.Close()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(httpBodyCloseTimeout):
		return r.ctx.Err()
	}
}

// armHTTPResponseCancel closes the response body when ctx is canceled so a
// blocked stream reader can unblock. Prefer wrapping the body with
// newContextReadCloser for the actual Read path.
func armHTTPResponseCancel(ctx context.Context, resp *http.Response) func() {
	if ctx == nil || resp == nil || resp.Body == nil {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() { close(done) })
	}
	go func() {
		select {
		case <-ctx.Done():
			closeHTTPBodyWithTimeout(resp.Body, httpBodyCloseTimeout)
		case <-done:
		}
	}()
	return stop
}
