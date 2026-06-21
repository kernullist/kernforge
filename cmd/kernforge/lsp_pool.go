package main

// LSPServerPool manages per-language Language Server Protocol child processes
// for read-only code navigation (definition, references, hover). It is opt-in
// (see Config.LSP and lsp.enabled) and degrades gracefully: when a server is not
// configured, cannot be resolved, crashes, or a request exceeds the per-request
// timeout, callers receive a clear error instead of a hung turn.
//
// The pool reuses the existing LSP-style JSON-RPC framing helpers
// (writeRPCMessage/readRPCMessage in mcp.go) and the minimal-environment helper
// (defaultMCPEnvVarNames). Each server is launched lazily per (language,
// workspaceRoot), cached, and restarted on the next request if its transport has
// failed. An idle reaper shuts down servers that have not been used recently.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Default bounds for the LSP pool. Every request is bounded so a stuck server
// can never hang the turn; idle servers are reaped to bound resource use.
const (
	lspDefaultRequestTimeout = 5 * time.Second
	lspDefaultIdleTimeout    = 5 * time.Minute
	lspInitializeTimeout     = 10 * time.Second
	lspShutdownTimeout       = 2 * time.Second
	lspReaperInterval        = 30 * time.Second
	lspMaxStderrCapture      = 4096
)

// Sentinel errors so the tool layer can distinguish "no server available" (fall
// back to grep) from a genuine protocol error.
var (
	errLSPDisabled        = errors.New("lsp navigation is not enabled")
	errLSPServerNotFound  = errors.New("no lsp server configured for language")
	errLSPBinaryRejected  = errors.New("lsp server binary not permitted by allowlist")
	errLSPUnsupportedLang = errors.New("unsupported language for lsp navigation")
)

// lspLanguageSpec maps a logical language id to its default server binary and
// the LSP languageId used in textDocument/didOpen.
type lspLanguageSpec struct {
	language   string
	binary     string
	languageID string
}

// supportedLSPLanguages lists the languages this slice knows how to launch by
// default. Go (gopls) is the primary, fully exercised path; C/C++ (clangd) is
// supported with the same framing/handshake. Additional languages can be added
// by extending this table plus the extension map below.
var supportedLSPLanguages = []lspLanguageSpec{
	{language: "go", binary: "gopls", languageID: "go"},
	{language: "c", binary: "clangd", languageID: "c"},
	{language: "cpp", binary: "clangd", languageID: "cpp"},
}

// lspExtensionLanguage maps a file extension (lowercase, with dot) to a logical
// language id. Used to pick a server and the didOpen languageId from a path.
var lspExtensionLanguage = map[string]string{
	".go":  "go",
	".c":   "c",
	".h":   "c",
	".cc":  "cpp",
	".cpp": "cpp",
	".cxx": "cpp",
	".hpp": "cpp",
	".hh":  "cpp",
	".hxx": "cpp",
	".ipp": "cpp",
	".inl": "cpp",
	".cu":  "cpp",
	".m":   "c",
	".mm":  "cpp",
}

func lspLanguageSpecFor(language string) (lspLanguageSpec, bool) {
	language = strings.ToLower(strings.TrimSpace(language))
	for _, spec := range supportedLSPLanguages {
		if spec.language == language {
			return spec, true
		}
	}
	return lspLanguageSpec{}, false
}

// lspLanguageForPath resolves the logical language id for a file path by its
// extension. The boolean reports whether the extension is recognized.
func lspLanguageForPath(path string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	language, ok := lspExtensionLanguage[ext]
	return language, ok
}

// LSPPoolConfig is the resolved, validated configuration the pool runs with. It
// is derived from Config.LSP by NewLSPServerPool.
type LSPPoolConfig struct {
	// ServerPaths maps a logical language id to an explicit server binary path.
	// When empty for a language, the default binary name is resolved from PATH.
	ServerPaths map[string]string
	// ServerArgs maps a logical language id to extra args appended after the
	// default server arguments.
	ServerArgs map[string][]string
	// Allowlist, when non-empty, restricts which resolved binary base names are
	// permitted to launch. Comparison is case-insensitive on the base name (with
	// any .exe suffix stripped) so "gopls" matches "gopls.exe".
	Allowlist []string
	// RequestTimeout bounds a single navigation request.
	RequestTimeout time.Duration
	// IdleTimeout bounds how long an unused server stays alive.
	IdleTimeout time.Duration
}

func (c LSPPoolConfig) requestTimeout() time.Duration {
	if c.RequestTimeout > 0 {
		return c.RequestTimeout
	}
	return lspDefaultRequestTimeout
}

func (c LSPPoolConfig) idleTimeout() time.Duration {
	if c.IdleTimeout > 0 {
		return c.IdleTimeout
	}
	return lspDefaultIdleTimeout
}

// allowBinary reports whether a resolved binary path is permitted by the
// allowlist. An empty allowlist permits any resolved binary (PATH/explicit-path
// resolution already constrains it). Matching is on the base name so an operator
// can allow "clangd" without pinning an absolute path.
func (c LSPPoolConfig) allowBinary(resolvedPath string) bool {
	if len(c.Allowlist) == 0 {
		return true
	}
	want := lspBinaryKey(resolvedPath)
	for _, entry := range c.Allowlist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// Allow either an exact path match or a base-name match.
		if sameCleanPathForOS(entry, resolvedPath) {
			return true
		}
		if lspBinaryKey(entry) == want {
			return true
		}
	}
	return false
}

func lspBinaryKey(path string) string {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(strings.TrimSpace(path))))
	base = strings.TrimSuffix(base, ".exe")
	return base
}

// LSPLocation is a resolved source location returned by definition/references.
type LSPLocation struct {
	Path      string
	StartLine int // 1-based
	StartCol  int // 1-based
	EndLine   int // 1-based
	EndCol    int // 1-based
}

// LSPServerPool owns the cached per-(language, root) clients and the idle
// reaper. The zero value is not usable; construct via NewLSPServerPool.
type LSPServerPool struct {
	cfg LSPPoolConfig

	mu      sync.Mutex
	clients map[string]*lspClient
	closed  bool

	reaperOnce sync.Once
	reaperStop chan struct{}

	// nowFn and startFn are injection points for hermetic tests. Production code
	// leaves them nil and the pool uses time.Now and the real process launcher.
	nowFn   func() time.Time
	startFn func(spec lspLanguageSpec, binary string, args []string, root string) (*lspClient, error)
}

// NewLSPServerPool builds a pool from the resolved configuration. It does not
// launch any server; servers start lazily on first use.
func NewLSPServerPool(cfg LSPPoolConfig) *LSPServerPool {
	return &LSPServerPool{
		cfg:        cfg,
		clients:    map[string]*lspClient{},
		reaperStop: make(chan struct{}),
	}
}

func (p *LSPServerPool) now() time.Time {
	if p != nil && p.nowFn != nil {
		return p.nowFn()
	}
	return time.Now()
}

func (p *LSPServerPool) requestTimeout() time.Duration {
	return p.cfg.requestTimeout()
}

func clientCacheKey(language string, root string) string {
	return strings.ToLower(strings.TrimSpace(language)) + "\x00" + strings.ToLower(filepath.Clean(strings.TrimSpace(root)))
}

// client returns a live client for (language, root), launching one if needed and
// restarting it if a cached instance has failed. The caller holds no lock.
func (p *LSPServerPool) client(language string, root string) (*lspClient, error) {
	if p == nil {
		return nil, errLSPDisabled
	}
	spec, ok := lspLanguageSpecFor(language)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errLSPUnsupportedLang, language)
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("lsp navigation requires a workspace root")
	}
	key := clientCacheKey(language, root)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errLSPDisabled
	}
	if existing := p.clients[key]; existing != nil {
		if !existing.failed() {
			existing.touch(p.now())
			p.mu.Unlock()
			return existing, nil
		}
		// Drop the failed client and relaunch below.
		delete(p.clients, key)
		go existing.Close()
	}
	p.mu.Unlock()

	binary, args, err := p.resolveServerCommand(spec)
	if err != nil {
		return nil, err
	}

	client, err := p.launch(spec, binary, args, root)
	if err != nil {
		return nil, err
	}
	client.touch(p.now())

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		go client.Close()
		return nil, errLSPDisabled
	}
	// A concurrent caller may have launched the same key; keep the first winner.
	if existing := p.clients[key]; existing != nil && !existing.failed() {
		p.mu.Unlock()
		go client.Close()
		existing.touch(p.now())
		return existing, nil
	}
	p.clients[key] = client
	p.mu.Unlock()
	p.ensureReaper()
	return client, nil
}

// resolveServerCommand resolves the server binary (explicit config path or PATH)
// and validates it against the allowlist. The returned args include the default
// server args plus any configured extras.
func (p *LSPServerPool) resolveServerCommand(spec lspLanguageSpec) (string, []string, error) {
	configured := strings.TrimSpace(p.cfg.ServerPaths[spec.language])
	var resolved string
	if configured != "" {
		// An explicit path must exist and be a regular file. We do not search
		// PATH when an operator pinned a path, so a typo fails closed.
		info, err := os.Stat(configured)
		if err != nil {
			return "", nil, fmt.Errorf("%w: configured lsp server for %s not found: %v", errLSPServerNotFound, spec.language, err)
		}
		if info.IsDir() {
			return "", nil, fmt.Errorf("%w: configured lsp server for %s is a directory: %s", errLSPServerNotFound, spec.language, configured)
		}
		resolved = configured
	} else {
		found, err := exec.LookPath(spec.binary)
		if err != nil {
			return "", nil, fmt.Errorf("%w: %s (binary %q not on PATH)", errLSPServerNotFound, spec.language, spec.binary)
		}
		resolved = found
	}
	if !p.cfg.allowBinary(resolved) {
		return "", nil, fmt.Errorf("%w: %s", errLSPBinaryRejected, resolved)
	}
	args := defaultLSPServerArgs(spec)
	args = append(args, p.cfg.ServerArgs[spec.language]...)
	return resolved, args, nil
}

// defaultLSPServerArgs returns the baseline arguments for a server. gopls and
// clangd both speak stdio LSP by default with no special flags; we keep this
// explicit so future per-server tuning has a single home.
func defaultLSPServerArgs(spec lspLanguageSpec) []string {
	switch spec.language {
	case "go":
		// gopls defaults to stdio; no args needed.
		return nil
	case "c", "cpp":
		// clangd reads stdio by default; keep background indexing off the request
		// path is not configurable here, but a low log level keeps stderr small.
		return []string{"--log=error"}
	default:
		return nil
	}
}

func (p *LSPServerPool) launch(spec lspLanguageSpec, binary string, args []string, root string) (*lspClient, error) {
	if p.startFn != nil {
		return p.startFn(spec, binary, args, root)
	}
	return startLSPClient(spec, binary, args, root)
}

// Definition/References/Hover perform a single bounded navigation request. The
// caller pins the server workspace root explicitly (the validated workspace
// root) so the LSP root never depends on model-supplied path components. Every
// request is bounded by the configured request timeout; on any failure the
// caller may fall back to grep.
func (p *LSPServerPool) Definition(ctx context.Context, language string, root string, path string, line int, col int) ([]LSPLocation, error) {
	client, err := p.client(language, root)
	if err != nil {
		return nil, err
	}
	return client.definition(ctx, p.requestTimeout(), path, line, col)
}

func (p *LSPServerPool) References(ctx context.Context, language string, root string, path string, line int, col int) ([]LSPLocation, error) {
	client, err := p.client(language, root)
	if err != nil {
		return nil, err
	}
	return client.references(ctx, p.requestTimeout(), path, line, col)
}

func (p *LSPServerPool) Hover(ctx context.Context, language string, root string, path string, line int, col int) (string, error) {
	client, err := p.client(language, root)
	if err != nil {
		return "", err
	}
	return client.hover(ctx, p.requestTimeout(), path, line, col)
}

func (p *LSPServerPool) ensureReaper() {
	p.reaperOnce.Do(func() {
		go p.reapLoop()
	})
}

func (p *LSPServerPool) reapLoop() {
	ticker := time.NewTicker(lspReaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.reaperStop:
			return
		case <-ticker.C:
			p.reapIdle()
		}
	}
}

func (p *LSPServerPool) reapIdle() {
	idle := p.cfg.idleTimeout()
	now := p.now()
	var stale []*lspClient
	p.mu.Lock()
	for key, client := range p.clients {
		if client.failed() || now.Sub(client.lastUsed()) >= idle {
			stale = append(stale, client)
			delete(p.clients, key)
		}
	}
	p.mu.Unlock()
	for _, client := range stale {
		client.Close()
	}
}

// Close shuts down every cached server and stops the reaper. It is safe to call
// multiple times and from the session teardown hook.
func (p *LSPServerPool) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	clients := make([]*lspClient, 0, len(p.clients))
	for key, client := range p.clients {
		clients = append(clients, client)
		delete(p.clients, key)
	}
	p.mu.Unlock()
	close(p.reaperStop)
	for _, client := range clients {
		client.Close()
	}
}

// lspClient is a minimal LSP JSON-RPC client over a single server process. It
// mirrors the MCP stdio client (read loop + pending-id correlation) but is
// scoped to the navigation methods this tool needs.
type lspClient struct {
	spec lspLanguageSpec
	root string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	writeMu sync.Mutex

	pendingMu sync.Mutex
	nextID    int64
	pending   map[int64]chan lspRPCResult
	readErr   error

	readLoopOnce sync.Once

	openMu     sync.Mutex
	openedDocs map[string]int // path -> document version already sent via didOpen

	stderrMu  sync.Mutex
	stderrBuf strings.Builder

	usedMu   sync.Mutex
	usedAt   time.Time
	failedAt bool
}

type lspRPCResult struct {
	payload map[string]any
	err     error
}

// startLSPClient launches the server process, performs the initialize/
// initialized handshake, and returns a ready client. On any handshake failure it
// tears the process down so no orphan survives.
func startLSPClient(spec lspLanguageSpec, binary string, args []string, root string) (*lspClient, error) {
	cmd := exec.Command(binary, args...)
	cmd.Dir = root
	cmd.Env = buildLSPProcessEnv()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	client := &lspClient{
		spec:       spec,
		root:       root,
		cmd:        cmd,
		stdin:      stdin,
		stdout:     bufio.NewReader(stdout),
		pending:    map[int64]chan lspRPCResult{},
		openedDocs: map[string]int{},
	}
	go client.captureStderr(stderr)
	client.ensureReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), lspInitializeTimeout)
	defer cancel()
	if err := client.initialize(ctx, root); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

// buildLSPProcessEnv runs the server with a minimal, allowlisted environment
// (the same core var set MCP servers get). The server still inherits PATH so it
// can find toolchains, but secrets in unrelated env vars are not forwarded.
func buildLSPProcessEnv() []string {
	env := make([]string, 0, 16)
	for _, name := range defaultMCPEnvVarNames() {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func (c *lspClient) captureStderr(r io.Reader) {
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.stderrMu.Lock()
			if c.stderrBuf.Len() < lspMaxStderrCapture {
				c.stderrBuf.Write(buf[:n])
			}
			c.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (c *lspClient) stderrSummary() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return strings.TrimSpace(c.stderrBuf.String())
}

func (c *lspClient) ensureReadLoop() {
	c.readLoopOnce.Do(func() {
		go c.readLoop()
	})
}

func (c *lspClient) readLoop() {
	for {
		msg, err := readRPCMessage(c.stdout)
		if err != nil {
			c.failPending(err)
			return
		}
		// Server-initiated requests and notifications carry a method; we do not
		// answer them in this read-only navigation slice (apart from the ones the
		// handshake handles), so they are ignored.
		if method, _ := msg["method"].(string); method != "" {
			continue
		}
		id, ok := rpcMessageID(msg["id"])
		if !ok {
			continue
		}
		result := lspRPCResult{}
		if rawErr, ok := msg["error"]; ok && rawErr != nil {
			result.err = fmt.Errorf("rpc error: %s", formatRPCError(rawErr))
		} else if payload, ok := msg["result"].(map[string]any); ok {
			result.payload = payload
		} else if raw, ok := msg["result"]; ok && raw != nil {
			// definition/references return a JSON array (or a bare value) at the
			// result key. Wrap it under a synthesized "result" entry so the
			// location parser can recover it; an object result took the branch
			// above.
			result.payload = map[string]any{"result": raw}
		} else {
			// result may legitimately be null (e.g. no definition found); carry an
			// empty payload so the caller sees "no result".
			result.payload = map[string]any{}
		}
		c.deliver(id, result)
	}
}

func (c *lspClient) deliver(id int64, result lspRPCResult) {
	c.pendingMu.Lock()
	ch := c.pending[id]
	if ch != nil {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	if ch != nil {
		ch <- result
	}
}

func (c *lspClient) failPending(err error) {
	c.pendingMu.Lock()
	c.readErr = err
	pending := c.pending
	c.pending = map[int64]chan lspRPCResult{}
	c.pendingMu.Unlock()
	c.markFailed()
	for _, ch := range pending {
		ch <- lspRPCResult{err: err}
	}
}

func (c *lspClient) registerPending(ch chan lspRPCResult) (int64, error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.readErr != nil {
		return 0, c.readErr
	}
	c.nextID++
	id := c.nextID
	if c.pending == nil {
		c.pending = map[int64]chan lspRPCResult{}
	}
	c.pending[id] = ch
	return id, nil
}

func (c *lspClient) unregisterPending(id int64) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	delete(c.pending, id)
}

// call sends a request and waits for the matching response or ctx cancellation.
// On timeout/cancellation it marks the client failed so the pool relaunches it.
func (c *lspClient) call(ctx context.Context, method string, params any) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan lspRPCResult, 1)
	id, err := c.registerPending(ch)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	c.writeMu.Lock()
	err = writeRPCMessage(c.stdin, payload)
	c.writeMu.Unlock()
	if err != nil {
		c.unregisterPending(id)
		c.markFailed()
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	select {
	case out := <-ch:
		if out.err != nil {
			if stderr := c.stderrSummary(); stderr != "" {
				return nil, fmt.Errorf("%w (%s)", out.err, lspTrimStderr(stderr))
			}
			return nil, out.err
		}
		return out.payload, nil
	case <-ctx.Done():
		c.unregisterPending(id)
		// A timed-out server is treated as unhealthy: future requests relaunch it.
		c.markFailed()
		return nil, ctx.Err()
	}
}

func (c *lspClient) notify(method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	c.writeMu.Lock()
	err := writeRPCMessage(c.stdin, payload)
	c.writeMu.Unlock()
	if err != nil {
		c.markFailed()
	}
	return err
}

func (c *lspClient) initialize(ctx context.Context, root string) error {
	rootURI := lspPathToURI(root)
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"rootPath":  root,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"definition": map[string]any{},
				"references": map[string]any{},
				"hover": map[string]any{
					"contentFormat": []any{"markdown", "plaintext"},
				},
				"synchronization": map[string]any{
					"didSave": false,
				},
			},
		},
		"workspaceFolders": []any{
			map[string]any{
				"uri":  rootURI,
				"name": filepath.Base(root),
			},
		},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return err
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		return err
	}
	return nil
}

// ensureDocOpen sends textDocument/didOpen once per path. Servers like gopls
// require the document to be open before definition/hover queries resolve.
func (c *lspClient) ensureDocOpen(path string) error {
	clean := filepath.Clean(path)
	c.openMu.Lock()
	if _, ok := c.openedDocs[clean]; ok {
		c.openMu.Unlock()
		return nil
	}
	c.openMu.Unlock()

	data, err := os.ReadFile(clean)
	if err != nil {
		return err
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        lspPathToURI(clean),
			"languageId": c.spec.languageID,
			"version":    1,
			"text":       string(data),
		},
	}
	if err := c.notify("textDocument/didOpen", params); err != nil {
		return err
	}
	c.openMu.Lock()
	c.openedDocs[clean] = 1
	c.openMu.Unlock()
	return nil
}

func (c *lspClient) definition(ctx context.Context, timeout time.Duration, path string, line int, col int) ([]LSPLocation, error) {
	callCtx, cancel := lspRequestContext(ctx, timeout)
	defer cancel()
	if err := c.ensureDocOpen(path); err != nil {
		return nil, err
	}
	params := lspPositionParams(path, line, col)
	result, err := c.call(callCtx, "textDocument/definition", params)
	if err != nil {
		return nil, err
	}
	return parseLSPLocations(result), nil
}

func (c *lspClient) references(ctx context.Context, timeout time.Duration, path string, line int, col int) ([]LSPLocation, error) {
	callCtx, cancel := lspRequestContext(ctx, timeout)
	defer cancel()
	if err := c.ensureDocOpen(path); err != nil {
		return nil, err
	}
	params := lspPositionParams(path, line, col)
	params["context"] = map[string]any{"includeDeclaration": true}
	result, err := c.call(callCtx, "textDocument/references", params)
	if err != nil {
		return nil, err
	}
	return parseLSPLocations(result), nil
}

func (c *lspClient) hover(ctx context.Context, timeout time.Duration, path string, line int, col int) (string, error) {
	callCtx, cancel := lspRequestContext(ctx, timeout)
	defer cancel()
	if err := c.ensureDocOpen(path); err != nil {
		return "", err
	}
	params := lspPositionParams(path, line, col)
	result, err := c.call(callCtx, "textDocument/hover", params)
	if err != nil {
		return "", err
	}
	return parseLSPHover(result), nil
}

// lspRequestContext derives a child context bounded by the per-request timeout.
// If the parent already carries a deadline, the tighter of the two wins.
func lspRequestContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		timeout = lspDefaultRequestTimeout
	}
	return context.WithTimeout(parent, timeout)
}

func lspPositionParams(path string, line int, col int) map[string]any {
	// LSP positions are zero-based; the tool layer accepts and validates 1-based
	// line/col input, so convert here.
	zeroLine := line - 1
	if zeroLine < 0 {
		zeroLine = 0
	}
	zeroCol := col - 1
	if zeroCol < 0 {
		zeroCol = 0
	}
	return map[string]any{
		"textDocument": map[string]any{"uri": lspPathToURI(filepath.Clean(path))},
		"position":     map[string]any{"line": zeroLine, "character": zeroCol},
	}
}

func (c *lspClient) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), lspShutdownTimeout)
	defer cancel()
	// Best-effort graceful shutdown; ignore errors since Close kills the process
	// afterward regardless.
	_, _ = c.call(ctx, "shutdown", nil)
	_ = c.notify("exit", nil)
}

// Close performs a best-effort graceful shutdown then kills the process. It is
// safe to call multiple times.
func (c *lspClient) Close() {
	if c == nil {
		return
	}
	if !c.failed() {
		c.shutdown()
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		// Reap the process so it does not linger as a zombie.
		go func(cmd *exec.Cmd) {
			_ = cmd.Wait()
		}(c.cmd)
	}
	c.markFailed()
}

func (c *lspClient) markFailed() {
	c.usedMu.Lock()
	c.failedAt = true
	c.usedMu.Unlock()
}

func (c *lspClient) failed() bool {
	c.usedMu.Lock()
	defer c.usedMu.Unlock()
	return c.failedAt
}

func (c *lspClient) touch(now time.Time) {
	c.usedMu.Lock()
	c.usedAt = now
	c.usedMu.Unlock()
}

func (c *lspClient) lastUsed() time.Time {
	c.usedMu.Lock()
	defer c.usedMu.Unlock()
	return c.usedAt
}

// lspPathToURI converts an absolute filesystem path to a file:// URI in a way
// that works for Windows drive paths and POSIX paths alike.
func lspPathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") {
		// Windows drive path such as C:/foo -> /C:/foo
		slashed = "/" + slashed
	}
	return "file://" + lspEscapeURIPath(slashed)
}

// lspURIToPath converts a file:// URI returned by the server back to a local
// path. Non-file URIs are returned unchanged so the caller can still display
// them.
func lspURIToPath(uri string) string {
	const prefix = "file://"
	if !strings.HasPrefix(uri, prefix) {
		return uri
	}
	rest := strings.TrimPrefix(uri, prefix)
	// Drop an optional authority (file://host/path -> /path); we only handle the
	// localhost/empty-authority form servers emit.
	if strings.HasPrefix(rest, "/") {
		// keep
	} else if idx := strings.Index(rest, "/"); idx >= 0 {
		rest = rest[idx:]
	}
	decoded := lspUnescapeURIPath(rest)
	// On Windows the form is /C:/foo; strip the leading slash before the drive.
	if len(decoded) >= 3 && decoded[0] == '/' && decoded[2] == ':' {
		decoded = decoded[1:]
	}
	return filepath.FromSlash(decoded)
}

// lspEscapeURIPath percent-encodes the characters that matter for file URIs
// while leaving the path separators and drive colon intact. A full url.PathEscape
// would encode the slashes, so we escape conservatively by segment.
func lspEscapeURIPath(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r == '/' || r == ':' || r == '-' || r == '_' || r == '.' || r == '~':
			b.WriteRune(r)
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			for _, by := range []byte(string(r)) {
				b.WriteString(fmt.Sprintf("%%%02X", by))
			}
		}
	}
	return b.String()
}

func lspUnescapeURIPath(path string) string {
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] == '%' && i+2 < len(path) {
			var value int
			_, err := fmt.Sscanf(path[i+1:i+3], "%02X", &value)
			if err == nil {
				b.WriteByte(byte(value))
				i += 2
				continue
			}
		}
		b.WriteByte(path[i])
	}
	return b.String()
}

// parseLSPLocations normalizes the several shapes textDocument/definition and
// textDocument/references can return: a single Location, an array of Location,
// or an array of LocationLink. The read loop wraps a bare array under the
// "result" key when present; here we accept the common encodings.
func parseLSPLocations(result map[string]any) []LSPLocation {
	if result == nil {
		return nil
	}
	// Some read paths deliver an array result under a synthesized key.
	if raw, ok := result["result"]; ok {
		if locs := parseLSPLocationValue(raw); len(locs) > 0 {
			return locs
		}
	}
	// A single Location object arrives as the result map itself.
	if loc, ok := parseSingleLSPLocation(result); ok {
		return []LSPLocation{loc}
	}
	return nil
}

// parseLSPLocationValue handles the array/single forms of a location value.
func parseLSPLocationValue(raw any) []LSPLocation {
	switch typed := raw.(type) {
	case []any:
		out := make([]LSPLocation, 0, len(typed))
		for _, item := range typed {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if loc, ok := parseSingleLSPLocation(obj); ok {
				out = append(out, loc)
			}
		}
		return out
	case map[string]any:
		if loc, ok := parseSingleLSPLocation(typed); ok {
			return []LSPLocation{loc}
		}
	}
	return nil
}

// parseSingleLSPLocation accepts both Location ({uri, range}) and LocationLink
// ({targetUri, targetRange/targetSelectionRange}).
func parseSingleLSPLocation(obj map[string]any) (LSPLocation, bool) {
	uri := stringValue(obj, "uri")
	rangeObj, _ := obj["range"].(map[string]any)
	if uri == "" {
		uri = stringValue(obj, "targetUri")
		if r, ok := obj["targetSelectionRange"].(map[string]any); ok {
			rangeObj = r
		} else if r, ok := obj["targetRange"].(map[string]any); ok {
			rangeObj = r
		}
	}
	if uri == "" {
		return LSPLocation{}, false
	}
	loc := LSPLocation{Path: lspURIToPath(uri)}
	if rangeObj != nil {
		start, _ := rangeObj["start"].(map[string]any)
		end, _ := rangeObj["end"].(map[string]any)
		loc.StartLine, loc.StartCol = lspPositionFrom(start)
		loc.EndLine, loc.EndCol = lspPositionFrom(end)
	}
	return loc, true
}

func lspPositionFrom(pos map[string]any) (int, int) {
	if pos == nil {
		return 0, 0
	}
	line := intValue(pos, "line", -1)
	character := intValue(pos, "character", -1)
	// Convert zero-based LSP positions back to 1-based for display. A missing
	// field stays 0 to signal "unknown".
	if line >= 0 {
		line++
	} else {
		line = 0
	}
	if character >= 0 {
		character++
	} else {
		character = 0
	}
	return line, character
}

// parseLSPHover extracts readable text from a textDocument/hover result. The
// contents field can be a MarkupContent object, a string, or an array of
// strings / MarkedString objects.
func parseLSPHover(result map[string]any) string {
	if result == nil {
		return ""
	}
	contents, ok := result["contents"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(lspHoverContents(contents))
}

func lspHoverContents(contents any) string {
	switch typed := contents.(type) {
	case string:
		return typed
	case map[string]any:
		// MarkupContent {kind, value} or MarkedString {language, value}.
		if value := stringValue(typed, "value"); value != "" {
			return value
		}
		return ""
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(lspHoverContents(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

func lspTrimStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	const max = 400
	if len(stderr) > max {
		return stderr[:max] + "..."
	}
	return stderr
}
