package main

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// httpConnKiller tracks dialed connections so a user cancel can forcibly close
// in-flight provider sockets. CloseIdleConnections alone does not abort an
// active hung stream.
type httpConnKiller struct {
	mu    sync.Mutex
	conns map[*trackedConn]struct{}
}

type trackedConn struct {
	net.Conn
	killer *httpConnKiller
	once   sync.Once
}

func (c *trackedConn) Close() error {
	c.unregister()
	return c.Conn.Close()
}

func (c *trackedConn) unregister() {
	c.once.Do(func() {
		if c.killer != nil {
			c.killer.remove(c)
		}
	})
}

func (k *httpConnKiller) add(c *trackedConn) {
	if k == nil || c == nil {
		return
	}
	k.mu.Lock()
	if k.conns == nil {
		k.conns = map[*trackedConn]struct{}{}
	}
	k.conns[c] = struct{}{}
	k.mu.Unlock()
}

func (k *httpConnKiller) remove(c *trackedConn) {
	if k == nil || c == nil {
		return
	}
	k.mu.Lock()
	delete(k.conns, c)
	k.mu.Unlock()
}

func (k *httpConnKiller) KillAll() {
	if k == nil {
		return
	}
	k.mu.Lock()
	conns := make([]*trackedConn, 0, len(k.conns))
	for c := range k.conns {
		conns = append(conns, c)
	}
	k.conns = map[*trackedConn]struct{}{}
	k.mu.Unlock()
	for _, c := range conns {
		if c == nil {
			continue
		}
		c.once.Do(func() {})
		_ = c.Conn.Close()
	}
}

func attachHTTPConnKiller(client *http.Client) *httpConnKiller {
	if client == nil {
		return nil
	}
	killer := &httpConnKiller{conns: map[*trackedConn]struct{}{}}
	base, _ := http.DefaultTransport.(*http.Transport)
	if base == nil {
		base = &http.Transport{}
	} else {
		base = base.Clone()
	}
	if existing, ok := client.Transport.(*http.Transport); ok && existing != nil {
		base = existing.Clone()
	}
	prevDial := base.DialContext
	if prevDial == nil {
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		prevDial = dialer.DialContext
	}
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := prevDial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tracked := &trackedConn{Conn: conn, killer: killer}
		killer.add(tracked)
		return tracked, nil
	}
	client.Transport = base
	return killer
}

type providerForceCloser interface {
	ForceCloseConnections()
}

func forceCloseProviderClient(client ProviderClient) {
	if closer, ok := any(client).(providerForceCloser); ok {
		closer.ForceCloseConnections()
	}
}

func forceCloseHTTPClient(client *http.Client, killer *httpConnKiller) {
	if killer != nil {
		killer.KillAll()
	}
	if client != nil {
		client.CloseIdleConnections()
	}
}
