package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// deferredMCPHTTPServer is a minimal hermetic streamable_http MCP server used by
// the deferred-schema tests. It serves a fixed tool set, counts tools/list calls
// (to assert cache coherency), and can inject a per-call delay so the bounded
// ToolSearch timeout path can be exercised.
type deferredMCPHTTPServer struct {
	tools        []map[string]any
	listCalls    int64
	listDelayNS  atomic.Int64
	callRecorder func(name string, args map[string]any)
	mu           sync.Mutex
	lastCallTool string
	lastCallArgs map[string]any
}

func (d *deferredMCPHTTPServer) setListDelay(delay time.Duration) {
	d.listDelayNS.Store(int64(delay))
}

func (d *deferredMCPHTTPServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var msg map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		method, _ := msg["method"].(string)
		if strings.HasPrefix(method, "notifications/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "deferred", "version": "1.0.0"},
				},
			})
		case "tools/list":
			atomic.AddInt64(&d.listCalls, 1)
			if delay := time.Duration(d.listDelayNS.Load()); delay > 0 {
				time.Sleep(delay)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  map[string]any{"tools": d.tools},
			})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			name, _ := params["name"].(string)
			args, _ := params["arguments"].(map[string]any)
			d.mu.Lock()
			d.lastCallTool = name
			d.lastCallArgs = args
			d.mu.Unlock()
			if d.callRecorder != nil {
				d.callRecorder(name, args)
			}
			message := ""
			if args != nil {
				message, _ = args["message"].(string)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result": map[string]any{
					"structuredContent": map[string]any{"echoed": message},
				},
			})
		case "resources/list", "prompts/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"result":  map[string]any{},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg["id"],
				"error":   map[string]any{"message": "unsupported method: " + method},
			})
		}
	}
}

func deferredTestToolSet() []map[string]any {
	return []map[string]any{
		{
			"name":        "echo",
			"description": "Echo a message",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"message": map[string]any{"type": "string"}},
			},
		},
		{
			"name":        "search_docs",
			"description": "Search documentation",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
			},
		},
	}
}

func startDeferredMCPManager(t *testing.T, srv *deferredMCPHTTPServer) *MCPManager {
	t.Helper()
	httpSrv := httptest.NewServer(srv.handler(t))
	t.Cleanup(httpSrv.Close)
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name:             "deferred",
		URL:              httpSrv.URL,
		DeferToolSchemas: true,
	}})
	t.Cleanup(manager.Close)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	return manager
}

// mcpToolByName finds the MCPTool wrapper with the given namespaced name.
func mcpToolByName(t *testing.T, manager *MCPManager, namespaced string) MCPTool {
	t.Helper()
	for _, tool := range manager.Tools() {
		if mcpTool, ok := tool.(MCPTool); ok && mcpTool.namespaced == namespaced {
			return mcpTool
		}
	}
	t.Fatalf("tool %q not found", namespaced)
	return MCPTool{}
}

// TestDeferredToolDefinitionIsLightweightStub verifies that a deferred tool that
// is never used costs only name+description in the exposure plan: its model
// definition carries an empty-object input schema with a fetch-on-demand note,
// and no tools/list call is made beyond the single startup fetch.
func TestDeferredToolDefinitionIsLightweightStub(t *testing.T) {
	srv := &deferredMCPHTTPServer{tools: deferredTestToolSet()}
	manager := startDeferredMCPManager(t, srv)

	startupListCalls := atomic.LoadInt64(&srv.listCalls)
	if startupListCalls != 1 {
		t.Fatalf("expected exactly one startup tools/list, got %d", startupListCalls)
	}

	tool := mcpToolByName(t, manager, "mcp__deferred__echo")
	def := tool.Definition()
	if !strings.Contains(def.Description, "schema fetched on demand") {
		t.Fatalf("expected deferred stub note in description, got %q", def.Description)
	}
	props, _ := def.InputSchema["properties"].(map[string]any)
	if len(props) != 0 {
		t.Fatalf("expected empty stub schema properties, got %#v", props)
	}
	// Reading the definition for an unused deferred tool must not trigger any
	// additional tools/list round trips.
	if got := atomic.LoadInt64(&srv.listCalls); got != startupListCalls {
		t.Fatalf("deferred stub definition must not fetch schema; tools/list calls %d", got)
	}
}

// TestToolSearchRPCContract verifies the in-process ToolSearch contract: it
// returns matching tools with their full input schema, honors the tool name
// filter, and reports namespaced model-facing names.
func TestToolSearchRPCContract(t *testing.T) {
	srv := &deferredMCPHTTPServer{tools: deferredTestToolSet()}
	manager := startDeferredMCPManager(t, srv)

	hits, err := manager.ToolSearch(context.Background(), "", "search")
	if err != nil {
		t.Fatalf("ToolSearch: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 filtered hit, got %d: %#v", len(hits), hits)
	}
	hit := hits[0]
	if hit.Name != "mcp__deferred__search_docs" {
		t.Fatalf("unexpected namespaced name %q", hit.Name)
	}
	if hit.Server != "deferred" {
		t.Fatalf("unexpected server %q", hit.Server)
	}
	props, _ := hit.InputSchema["properties"].(map[string]any)
	if _, ok := props["query"]; !ok {
		t.Fatalf("expected full input schema with query property, got %#v", hit.InputSchema)
	}

	// Empty filter returns all tools, deterministically ordered by namespaced name.
	all, err := manager.ToolSearch(context.Background(), "", "")
	if err != nil {
		t.Fatalf("ToolSearch all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(all))
	}
	if all[0].Name >= all[1].Name {
		t.Fatalf("expected deterministic name ordering, got %q then %q", all[0].Name, all[1].Name)
	}
}

// TestToolSearchCacheCoherency verifies that the input schema fetched by one
// ToolSearch call is reflected in the tool definition without re-fetching, and
// that subsequent ToolSearch calls keep returning a coherent schema.
func TestToolSearchCacheCoherency(t *testing.T) {
	srv := &deferredMCPHTTPServer{tools: deferredTestToolSet()}
	manager := startDeferredMCPManager(t, srv)

	// One startup fetch already happened.
	if got := atomic.LoadInt64(&srv.listCalls); got != 1 {
		t.Fatalf("expected 1 startup tools/list, got %d", got)
	}

	if _, err := manager.ToolSearch(context.Background(), "deferred", "echo"); err != nil {
		t.Fatalf("first ToolSearch: %v", err)
	}
	afterFirst := atomic.LoadInt64(&srv.listCalls)
	if afterFirst != 2 {
		t.Fatalf("expected one ToolSearch fetch, got %d total list calls", afterFirst)
	}

	// After ToolSearch the deferred stub renders the full cached schema.
	tool := mcpToolByName(t, manager, "mcp__deferred__echo")
	def := tool.Definition()
	props, _ := def.InputSchema["properties"].(map[string]any)
	if _, ok := props["message"]; !ok {
		t.Fatalf("expected cached full schema after ToolSearch, got %#v", def.InputSchema)
	}
	if strings.Contains(def.Description, "schema fetched on demand") {
		t.Fatalf("cached tool should not carry the deferred note: %q", def.Description)
	}

	// A repeated ToolSearch is allowed to refetch the live list, but the cached
	// schema must stay coherent (same property set).
	hits, err := manager.ToolSearch(context.Background(), "deferred", "echo")
	if err != nil {
		t.Fatalf("second ToolSearch: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	props2, _ := hits[0].InputSchema["properties"].(map[string]any)
	if _, ok := props2["message"]; !ok {
		t.Fatalf("expected coherent schema on refetch, got %#v", hits[0].InputSchema)
	}
}

// TestDeferredAndEagerEquivalentExecution verifies that calling a tool produces
// the same result whether the server is eager or deferred. The deferred path
// transparently populates the schema before routing.
func TestDeferredAndEagerEquivalentExecution(t *testing.T) {
	run := func(t *testing.T, defer_ bool) string {
		t.Helper()
		srv := &deferredMCPHTTPServer{tools: deferredTestToolSet()}
		httpSrv := httptest.NewServer(srv.handler(t))
		t.Cleanup(httpSrv.Close)
		dir := t.TempDir()
		manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
			Name:             "deferred",
			URL:              httpSrv.URL,
			DeferToolSchemas: defer_,
		}})
		t.Cleanup(manager.Close)
		if len(warnings) != 0 {
			t.Fatalf("warnings: %v", warnings)
		}
		tool := mcpToolByName(t, manager, "mcp__deferred__echo")
		out, err := tool.Execute(context.Background(), map[string]any{"message": "hello"})
		if err != nil {
			t.Fatalf("Execute (defer=%v): %v", defer_, err)
		}
		return out
	}
	eager := run(t, false)
	deferred := run(t, true)
	if eager != deferred {
		t.Fatalf("eager and deferred outputs differ: eager=%q deferred=%q", eager, deferred)
	}
	if eager != `{"echoed":"hello"}` {
		t.Fatalf("unexpected echo output: %q", eager)
	}
}

// TestToolSearchTimeoutGracefulFallback verifies that when the live tools/list
// hangs past the bounded timeout, a deferred tool call still succeeds by falling
// back to the schema captured at startup rather than hanging the turn.
func TestToolSearchTimeoutGracefulFallback(t *testing.T) {
	srv := &deferredMCPHTTPServer{
		tools: deferredTestToolSet(),
	}
	httpSrv := httptest.NewServer(srv.handler(t))
	t.Cleanup(httpSrv.Close)
	dir := t.TempDir()
	// Connect with deferral OFF first so startup captures the schema quickly,
	// then we flip the descriptor to deferred to exercise the on-demand path
	// against a now-slow tools/list.
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name: "deferred",
		URL:  httpSrv.URL,
	}})
	t.Cleanup(manager.Close)
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	// Make the slow delay apply to the on-demand fetch (startup already done).
	srv.setListDelay(mcpToolSearchTimeout + 2*time.Second)
	if len(manager.servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(manager.servers))
	}
	server := manager.servers[0]
	for i := range server.tools {
		server.tools[i].Deferred = true
	}

	tool := mcpToolByName(t, manager, "mcp__deferred__echo")
	start := time.Now()
	out, err := tool.Execute(context.Background(), map[string]any{"message": "hi"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("expected graceful fallback execution, got error: %v", err)
	}
	if out != `{"echoed":"hi"}` {
		t.Fatalf("unexpected output after fallback: %q", out)
	}
	// The schema fetch must be bounded; total time should not approach the full
	// listDelay (timeout + 2s). Allow generous slack for the actual tools/call.
	if elapsed >= mcpToolSearchTimeout+2*time.Second {
		t.Fatalf("call took too long (%s); timeout fallback did not bound the fetch", elapsed)
	}
}

// TestToolSearchTimeoutSurfacesErrorWithoutFallback verifies that when the live
// tools/list times out and there is no locally captured schema to fall back to,
// the deferred tool call surfaces a clear error instead of hanging.
func TestToolSearchTimeoutSurfacesErrorWithoutFallback(t *testing.T) {
	srv := &deferredMCPHTTPServer{
		tools: []map[string]any{
			{"name": "echo", "description": "Echo a message"},
		},
	}
	httpSrv := httptest.NewServer(srv.handler(t))
	t.Cleanup(httpSrv.Close)
	dir := t.TempDir()
	manager, warnings := LoadMCPManager(Workspace{BaseRoot: dir, Root: dir}, []MCPServerConfig{{
		Name: "deferred",
		URL:  httpSrv.URL,
	}})
	t.Cleanup(manager.Close)
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	server := manager.servers[0]
	// Force the deferred path and clear the locally captured schema so the only
	// way to obtain one is the (now-slow) on-demand fetch.
	for i := range server.tools {
		server.tools[i].Deferred = true
		server.tools[i].InputSchema = nil
	}
	srv.setListDelay(mcpToolSearchTimeout + 2*time.Second)

	tool := mcpToolByName(t, manager, "mcp__deferred__echo")
	start := time.Now()
	_, err := tool.Execute(context.Background(), map[string]any{"message": "hi"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected a clear error when schema fetch times out with no fallback")
	}
	if !strings.Contains(err.Error(), "tool schema fetch failed") {
		t.Fatalf("expected a tool-schema-fetch error, got %v", err)
	}
	if elapsed >= mcpToolSearchTimeout+2*time.Second {
		t.Fatalf("error path took too long (%s); timeout did not bound the fetch", elapsed)
	}
}

// TestShouldDeferMCPToolSchemasHeuristic verifies the deferral decision: explicit
// flag always defers; the latency heuristic only defers when both the handshake
// is slow and the tool surface is large; default is OFF.
func TestShouldDeferMCPToolSchemasHeuristic(t *testing.T) {
	slow := mcpDeferToolSchemaLatencyThreshold + time.Second
	fast := time.Millisecond
	cases := []struct {
		name     string
		cfg      MCPServerConfig
		latency  time.Duration
		count    int
		expected bool
	}{
		{"explicit flag", MCPServerConfig{DeferToolSchemas: true}, fast, 1, true},
		{"slow and large", MCPServerConfig{}, slow, mcpDeferToolSchemaHeuristicMinTools, true},
		{"slow but small", MCPServerConfig{}, slow, mcpDeferToolSchemaHeuristicMinTools - 1, false},
		{"large but fast", MCPServerConfig{}, fast, mcpDeferToolSchemaHeuristicMinTools + 5, false},
		{"default off", MCPServerConfig{}, fast, 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDeferMCPToolSchemas(tc.cfg, tc.latency, tc.count); got != tc.expected {
				t.Fatalf("shouldDeferMCPToolSchemas = %v, want %v", got, tc.expected)
			}
		})
	}
}

// TestToolSearchBuiltinToolRegistered verifies the built-in tool_search tool is
// registered when an MCP server is present and absent otherwise, and that it
// returns a discoverable, schema-bearing result.
func TestToolSearchBuiltinToolRegistered(t *testing.T) {
	srv := &deferredMCPHTTPServer{tools: deferredTestToolSet()}
	manager := startDeferredMCPManager(t, srv)
	dir := t.TempDir()
	ws := Workspace{BaseRoot: dir, Root: dir}

	withMCP := buildRegistry(ws, manager)
	if !sliceContainsString(withMCP.ToolNames(), "tool_search") {
		t.Fatalf("expected tool_search to be registered when MCP servers exist")
	}
	withoutMCP := buildRegistry(ws, nil)
	if sliceContainsString(withoutMCP.ToolNames(), "tool_search") {
		t.Fatalf("did not expect tool_search without MCP servers")
	}

	out, err := withMCP.Execute(context.Background(), "tool_search", `{"tool_name_filter":"search"}`)
	if err != nil {
		t.Fatalf("tool_search execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal tool_search output: %v", err)
	}
	count, _ := payload["count"].(float64)
	if int(count) != 1 {
		t.Fatalf("expected 1 matching tool, got %v (%s)", count, out)
	}
}
