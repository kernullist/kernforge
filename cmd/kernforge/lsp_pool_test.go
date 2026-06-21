package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockLSPServer is an in-process language server speaking the LSP JSON-RPC
// framing (Content-Length headers + JSON body) over an io.Pipe pair. It is the
// hermetic stand-in for gopls/clangd: no real binary, no network, deterministic
// responses. Tests construct one, wire it to an lspClient, and assert on the
// request/response mapping, crash recovery, and timeouts.
type mockLSPServer struct {
	// handler maps an LSP method to a canned result. Returning a nil result with
	// ok=false makes the server omit a response (used to force a timeout).
	handler func(method string, params map[string]any) (result any, respond bool)

	mu       sync.Mutex
	requests []string
	closed   bool
}

func (m *mockLSPServer) recordRequest(method string) {
	m.mu.Lock()
	m.requests = append(m.requests, method)
	m.mu.Unlock()
}

func (m *mockLSPServer) seenRequests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.requests...)
}

// serve reads framed requests from r and writes framed responses to w until the
// reader closes. Notifications (no id) get recorded but never answered.
func (m *mockLSPServer) serve(r io.Reader, w io.Writer) {
	reader := bufio.NewReader(r)
	for {
		msg, err := readRPCMessage(reader)
		if err != nil {
			return
		}
		method, _ := msg["method"].(string)
		params, _ := msg["params"].(map[string]any)
		if method != "" {
			m.recordRequest(method)
		}
		id, hasID := rpcMessageID(msg["id"])
		if !hasID {
			// Notification: nothing to answer.
			continue
		}
		var result any = map[string]any{}
		respond := true
		if m.handler != nil {
			result, respond = m.handler(method, params)
		}
		if !respond {
			// Simulate a server that never replies to this request.
			continue
		}
		_ = writeRPCMessage(w, map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  result,
		})
	}
}

// newMockLSPClient wires an lspClient to a mockLSPServer over two pipes and
// returns the client plus a cleanup function. The client is NOT initialized;
// callers drive the handshake explicitly (or rely on the pool to do it).
func newMockLSPClient(t *testing.T, server *mockLSPServer, spec lspLanguageSpec, root string) (*lspClient, func()) {
	t.Helper()
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()

	client := &lspClient{
		spec:       spec,
		root:       root,
		stdin:      clientToServerW,
		stdout:     bufio.NewReader(serverToClientR),
		pending:    map[int64]chan lspRPCResult{},
		openedDocs: map[string]int{},
	}
	go server.serve(clientToServerR, serverToClientW)
	client.ensureReadLoop()

	cleanup := func() {
		_ = clientToServerW.Close()
		_ = serverToClientW.Close()
	}
	return client, cleanup
}

// defaultMockHandler answers the handshake and navigation methods with canned
// data so a full request flow can be exercised.
func defaultMockHandler(defURI string) func(method string, params map[string]any) (any, bool) {
	return func(method string, params map[string]any) (any, bool) {
		switch method {
		case "initialize":
			return map[string]any{
				"capabilities": map[string]any{
					"definitionProvider": true,
					"referencesProvider": true,
					"hoverProvider":      true,
				},
			}, true
		case "textDocument/definition":
			return []any{
				map[string]any{
					"uri": defURI,
					"range": map[string]any{
						"start": map[string]any{"line": 9, "character": 5},
						"end":   map[string]any{"line": 9, "character": 12},
					},
				},
			}, true
		case "textDocument/references":
			return []any{
				map[string]any{
					"uri": defURI,
					"range": map[string]any{
						"start": map[string]any{"line": 9, "character": 5},
						"end":   map[string]any{"line": 9, "character": 12},
					},
				},
				map[string]any{
					"uri": defURI,
					"range": map[string]any{
						"start": map[string]any{"line": 20, "character": 1},
						"end":   map[string]any{"line": 20, "character": 8},
					},
				},
			}, true
		case "textDocument/hover":
			return map[string]any{
				"contents": map[string]any{
					"kind":  "markdown",
					"value": "func Foo() error",
				},
			}, true
		case "shutdown":
			return nil, true
		default:
			return map[string]any{}, true
		}
	}
}

func TestLSPClientInitializeHandshake(t *testing.T) {
	server := &mockLSPServer{handler: func(method string, params map[string]any) (any, bool) {
		if method == "initialize" {
			// Assert the handshake carries a rootUri.
			if _, ok := params["rootUri"].(string); !ok {
				t.Errorf("initialize missing rootUri: %v", params)
			}
			return map[string]any{"capabilities": map[string]any{}}, true
		}
		return map[string]any{}, true
	}}
	client, cleanup := newMockLSPClient(t, server, lspLanguageSpec{language: "go", languageID: "go"}, t.TempDir())
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.initialize(ctx, client.root); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	// The "initialized" notification is written after the initialize response is
	// received; the server records it asynchronously, so poll briefly for it.
	deadline := time.Now().Add(time.Second)
	var requests []string
	for time.Now().Before(deadline) {
		requests = server.seenRequests()
		if len(requests) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(requests) < 2 || requests[0] != "initialize" || requests[1] != "initialized" {
		t.Fatalf("expected initialize then initialized, got %v", requests)
	}
}

func TestLSPClientDefinitionMapping(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "main.go")
	writeTempFile(t, srcPath, "package main\n\nfunc Foo() error { return nil }\n")
	defURI := lspPathToURI(srcPath)

	server := &mockLSPServer{handler: defaultMockHandler(defURI)}
	client, cleanup := newMockLSPClient(t, server, lspLanguageSpec{language: "go", languageID: "go"}, root)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.initialize(ctx, root); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	locs, err := client.definition(context.Background(), time.Second, srcPath, 3, 6)
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d (%v)", len(locs), locs)
	}
	// Zero-based 9 -> 1-based 10; character 5 -> 6.
	if locs[0].StartLine != 10 || locs[0].StartCol != 6 {
		t.Fatalf("unexpected location %+v", locs[0])
	}
	if !sameCleanPathForOS(locs[0].Path, srcPath) {
		t.Fatalf("expected path %s, got %s", srcPath, locs[0].Path)
	}
	// didOpen must have been sent before the definition query.
	requests := server.seenRequests()
	if !lspTestContains(requests, "textDocument/didOpen") {
		t.Fatalf("expected didOpen before definition, got %v", requests)
	}
	if !lspTestContains(requests, "textDocument/definition") {
		t.Fatalf("expected definition request, got %v", requests)
	}
}

func TestLSPClientReferencesMapping(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "main.go")
	writeTempFile(t, srcPath, "package main\n")
	defURI := lspPathToURI(srcPath)

	server := &mockLSPServer{handler: defaultMockHandler(defURI)}
	client, cleanup := newMockLSPClient(t, server, lspLanguageSpec{language: "go", languageID: "go"}, root)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.initialize(ctx, root); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	locs, err := client.references(context.Background(), time.Second, srcPath, 1, 1)
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("expected 2 references, got %d (%v)", len(locs), locs)
	}
}

func TestLSPClientHoverMapping(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "main.go")
	writeTempFile(t, srcPath, "package main\n")
	defURI := lspPathToURI(srcPath)

	server := &mockLSPServer{handler: defaultMockHandler(defURI)}
	client, cleanup := newMockLSPClient(t, server, lspLanguageSpec{language: "go", languageID: "go"}, root)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.initialize(ctx, root); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	text, err := client.hover(context.Background(), time.Second, srcPath, 1, 1)
	if err != nil {
		t.Fatalf("hover: %v", err)
	}
	if !strings.Contains(text, "func Foo() error") {
		t.Fatalf("unexpected hover text %q", text)
	}
}

func TestLSPClientRequestTimeout(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "main.go")
	writeTempFile(t, srcPath, "package main\n")

	// Handler answers initialize but never answers definition -> the client must
	// time out rather than hang, and mark itself failed for relaunch.
	server := &mockLSPServer{handler: func(method string, params map[string]any) (any, bool) {
		switch method {
		case "initialize":
			return map[string]any{"capabilities": map[string]any{}}, true
		case "textDocument/definition":
			return nil, false // never respond
		default:
			return map[string]any{}, true
		}
	}}
	client, cleanup := newMockLSPClient(t, server, lspLanguageSpec{language: "go", languageID: "go"}, root)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.initialize(ctx, root); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	_, err := client.definition(context.Background(), 150*time.Millisecond, srcPath, 1, 1)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if !client.failed() {
		t.Fatal("expected client to be marked failed after timeout")
	}
}

func TestLSPClientCrashFailsPending(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "main.go")
	writeTempFile(t, srcPath, "package main\n")

	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()
	client := &lspClient{
		spec:       lspLanguageSpec{language: "go", languageID: "go"},
		root:       root,
		stdin:      clientToServerW,
		stdout:     bufio.NewReader(serverToClientR),
		pending:    map[int64]chan lspRPCResult{},
		openedDocs: map[string]int{},
	}
	// A server that answers initialize, then crashes (closes the pipe) on the
	// next request, simulating a server process dying mid-request.
	go func() {
		reader := bufio.NewReader(clientToServerR)
		for {
			msg, err := readRPCMessage(reader)
			if err != nil {
				return
			}
			method, _ := msg["method"].(string)
			id, hasID := rpcMessageID(msg["id"])
			if method == "initialize" && hasID {
				_ = writeRPCMessage(serverToClientW, map[string]any{
					"jsonrpc": "2.0", "id": id, "result": map[string]any{"capabilities": map[string]any{}},
				})
				continue
			}
			if method == "textDocument/definition" {
				// Crash: drop the connection without responding.
				_ = serverToClientW.Close()
				return
			}
		}
	}()
	client.ensureReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.initialize(ctx, root); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	_, err := client.definition(context.Background(), 2*time.Second, srcPath, 1, 1)
	if err == nil {
		t.Fatal("expected error after server crash, got nil")
	}
	if !client.failed() {
		t.Fatal("expected client marked failed after crash")
	}
	_ = clientToServerW.Close()
}

func TestLSPPoolRestartsAfterFailure(t *testing.T) {
	root := t.TempDir()
	srcPath := filepath.Join(root, "main.go")
	writeTempFile(t, srcPath, "package main\n")
	defURI := lspPathToURI(srcPath)

	var launches int
	var mu sync.Mutex
	pool := NewLSPServerPool(LSPPoolConfig{RequestTimeout: time.Second})
	pool.startFn = func(spec lspLanguageSpec, binary string, args []string, r string) (*lspClient, error) {
		mu.Lock()
		launches++
		mu.Unlock()
		server := &mockLSPServer{handler: defaultMockHandler(defURI)}
		client, _ := newMockLSPClient(t, server, spec, r)
		// Drive the handshake as startLSPClient would.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.initialize(ctx, r); err != nil {
			return nil, err
		}
		return client, nil
	}
	defer pool.Close()

	// First request launches a server.
	if _, err := pool.Definition(context.Background(), "go", root, srcPath, 1, 1); err != nil {
		t.Fatalf("first definition: %v", err)
	}
	mu.Lock()
	first := launches
	mu.Unlock()
	if first != 1 {
		t.Fatalf("expected 1 launch, got %d", first)
	}

	// Force the cached client to fail; the next request must relaunch.
	key := clientCacheKey("go", root)
	pool.mu.Lock()
	cached := pool.clients[key]
	pool.mu.Unlock()
	if cached == nil {
		t.Fatal("expected a cached client")
	}
	cached.markFailed()

	if _, err := pool.Definition(context.Background(), "go", root, srcPath, 1, 1); err != nil {
		t.Fatalf("second definition: %v", err)
	}
	mu.Lock()
	second := launches
	mu.Unlock()
	if second != 2 {
		t.Fatalf("expected relaunch (2 launches), got %d", second)
	}
}

func TestLSPPoolReapsIdleServer(t *testing.T) {
	root := t.TempDir()
	defURI := lspPathToURI(filepath.Join(root, "main.go"))
	writeTempFile(t, filepath.Join(root, "main.go"), "package main\n")

	pool := NewLSPServerPool(LSPPoolConfig{RequestTimeout: time.Second, IdleTimeout: time.Minute})
	base := time.Now()
	pool.nowFn = func() time.Time { return base }
	pool.startFn = func(spec lspLanguageSpec, binary string, args []string, r string) (*lspClient, error) {
		server := &mockLSPServer{handler: defaultMockHandler(defURI)}
		client, _ := newMockLSPClient(t, server, spec, r)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.initialize(ctx, r)
		return client, nil
	}
	defer pool.Close()

	if _, err := pool.client("go", root); err != nil {
		t.Fatalf("client: %v", err)
	}
	pool.mu.Lock()
	count := len(pool.clients)
	pool.mu.Unlock()
	if count != 1 {
		t.Fatalf("expected 1 cached client, got %d", count)
	}
	// Advance virtual time past the idle window and run the reaper directly.
	pool.nowFn = func() time.Time { return base.Add(2 * time.Minute) }
	pool.reapIdle()
	pool.mu.Lock()
	count = len(pool.clients)
	pool.mu.Unlock()
	if count != 0 {
		t.Fatalf("expected idle client reaped, still have %d", count)
	}
}

func TestLSPPoolBinaryAllowlist(t *testing.T) {
	cases := []struct {
		name      string
		allowlist []string
		binary    string
		want      bool
	}{
		{name: "empty allows", allowlist: nil, binary: "/usr/bin/gopls", want: true},
		{name: "base name match", allowlist: []string{"gopls"}, binary: "/opt/go/bin/gopls", want: true},
		{name: "exe suffix tolerant", allowlist: []string{"gopls"}, binary: `C:\tools\gopls.exe`, want: true},
		{name: "reject unlisted", allowlist: []string{"gopls"}, binary: "/usr/bin/clangd", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := LSPPoolConfig{Allowlist: tc.allowlist}
			if got := cfg.allowBinary(tc.binary); got != tc.want {
				t.Fatalf("allowBinary(%q) = %v, want %v", tc.binary, got, tc.want)
			}
		})
	}
}

func TestLSPLanguageForPath(t *testing.T) {
	cases := map[string]string{
		"x.go":  "go",
		"x.c":   "c",
		"x.h":   "c",
		"x.cpp": "cpp",
		"x.hpp": "cpp",
	}
	for path, want := range cases {
		got, ok := lspLanguageForPath(path)
		if !ok || got != want {
			t.Fatalf("lspLanguageForPath(%q) = %q,%v want %q", path, got, ok, want)
		}
	}
	if _, ok := lspLanguageForPath("notes.txt"); ok {
		t.Fatal("expected .txt to be unmapped")
	}
}

func TestLSPPathURIRoundTrip(t *testing.T) {
	cases := []string{
		"/home/user/project/main.go",
		`C:\Users\dev\proj\main.go`,
		"/tmp/with space/file.c",
	}
	for _, path := range cases {
		uri := lspPathToURI(path)
		if !strings.HasPrefix(uri, "file://") {
			t.Fatalf("uri %q missing file:// scheme", uri)
		}
		back := lspURIToPath(uri)
		abs, _ := filepath.Abs(path)
		if !sameCleanPathForOS(back, abs) {
			t.Fatalf("round trip mismatch: %q -> %q -> %q (want %q)", path, uri, back, abs)
		}
	}
}

func lspTestContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func writeTempFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
