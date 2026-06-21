package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// bypassNetworkWorkspace returns a Workspace whose permission gate auto-approves
// network access, so a test exercising fetch/search mechanics is not blocked by
// the ActionNetwork gate. Gate behavior itself is covered separately.
func bypassNetworkWorkspace() Workspace {
	return Workspace{Perms: NewPermissionManager(ModeBypass, nil)}
}

func TestBuildRegistryContainsWebTools(t *testing.T) {
	root := t.TempDir()
	ws := Workspace{BaseRoot: root, Root: root}
	registry := buildRegistry(ws, nil)
	for _, want := range []string{"web_fetch", "web_search"} {
		if _, ok := registry.tools[want]; !ok {
			t.Fatalf("buildRegistry missing %q tool; have %v", want, registry.ToolNames())
		}
	}
}

func TestWebFetchSizeCapTruncates(t *testing.T) {
	// Server returns a body larger than the requested cap. The tool must read at
	// most max_bytes and flag truncation in metadata.
	big := strings.Repeat("A", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, big)
	}))
	defer srv.Close()

	tool := NewWebFetchTool(bypassNetworkWorkspace())
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"url":       srv.URL,
		"max_bytes": 1000,
	})
	if err != nil {
		t.Fatalf("web_fetch error: %v", err)
	}
	bytesRead, _ := result.Meta["bytes"].(int)
	if bytesRead != 1000 {
		t.Fatalf("expected exactly 1000 bytes read, got %d", bytesRead)
	}
	if len(result.DisplayText) > 1000 {
		t.Fatalf("display text exceeded cap: %d chars", len(result.DisplayText))
	}
}

func TestWebFetchTimeout(t *testing.T) {
	// Server delays beyond the configured timeout; the tool must return an error
	// rather than hang.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		fmt.Fprint(w, "late")
	}))
	defer srv.Close()

	tool := NewWebFetchTool(bypassNetworkWorkspace())
	// timeout_seconds has a 1s floor (must be a positive integer), so drive the
	// timeout through the parent context instead to keep the test fast.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := tool.ExecuteDetailed(ctx, map[string]any{
		"url": srv.URL,
	})
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
}

func TestWebFetchTimeoutSecondsArgClamps(t *testing.T) {
	// A configured timeout_seconds is honored and produces an error before the
	// slow server responds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		fmt.Fprint(w, "late")
	}))
	defer srv.Close()

	tool := NewWebFetchTool(bypassNetworkWorkspace())
	start := time.Now()
	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"url":             srv.URL,
		"timeout_seconds": 1,
	})
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
		t.Fatalf("timeout_seconds=1 did not bound the request, took %s", elapsed)
	}
}

func TestWebFetchHTMLToText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>x</title><style>.a{color:red}</style></head><body><h1>Hello</h1><p>World &amp; friends</p><script>alert(1)</script></body></html>`)
	}))
	defer srv.Close()

	tool := NewWebFetchTool(bypassNetworkWorkspace())
	out, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("web_fetch error: %v", err)
	}
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "World & friends") {
		t.Fatalf("expected readable text, got %q", out)
	}
	if strings.Contains(out, "alert(1)") || strings.Contains(out, "color:red") || strings.Contains(out, "<h1>") {
		t.Fatalf("html-to-text did not strip script/style/tags: %q", out)
	}
}

func TestWebFetchRawSkipsHTMLReduction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<h1>raw</h1>")
	}))
	defer srv.Close()

	tool := NewWebFetchTool(bypassNetworkWorkspace())
	out, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL, "raw": true})
	if err != nil {
		t.Fatalf("web_fetch error: %v", err)
	}
	if !strings.Contains(out, "<h1>raw</h1>") {
		t.Fatalf("raw mode should preserve tags, got %q", out)
	}
}

func TestWebFetchRejectsNonHTTPURL(t *testing.T) {
	tool := NewWebFetchTool(bypassNetworkWorkspace())
	_, err := tool.Execute(context.Background(), map[string]any{"url": "file:///etc/passwd"})
	if err == nil || !strings.Contains(err.Error(), "http") {
		t.Fatalf("expected non-http url rejection, got %v", err)
	}
}

func TestWebFetchHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	tool := NewWebFetchTool(bypassNetworkWorkspace())
	_, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}

func TestWebFetchPlanModeDeniesNetwork(t *testing.T) {
	// Plan mode must deny ActionNetwork, so web_fetch fails before contacting any
	// server. This proves the tool honors the network gate.
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	ws := Workspace{Perms: NewPermissionManager(ModePlan, nil)}
	tool := NewWebFetchTool(ws)
	_, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err == nil {
		t.Fatalf("expected plan-mode network denial, got nil")
	}
	if hit {
		t.Fatalf("web_fetch contacted the server despite plan-mode denial")
	}
}

func TestWebFetchHonorsNetworkAllowlistDeny(t *testing.T) {
	// A configured network deny rule matching the target must block the fetch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	perms := NewPermissionManager(ModeDefault, func(string) (bool, error) {
		t.Fatalf("deny rule must not prompt")
		return false, nil
	})
	if err := perms.ApplyConfigRules(PermissionRulesConfig{
		Network: map[string]string{`web_fetch`: "deny"},
	}); err != nil {
		t.Fatalf("ApplyConfigRules: %v", err)
	}
	tool := NewWebFetchTool(Workspace{Perms: perms})
	_, err := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected network deny rule to block, got %v", err)
	}
}

func TestWebSearchNotConfigured(t *testing.T) {
	tool := NewWebSearchTool(bypassNetworkWorkspace())
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{"query": "anything"})
	if err != nil {
		t.Fatalf("web_search must not error when unconfigured, got %v", err)
	}
	if !strings.Contains(strings.ToLower(result.DisplayText), "not configured") {
		t.Fatalf("expected not-configured message, got %q", result.DisplayText)
	}
	if configured, _ := result.Meta["configured"].(bool); configured {
		t.Fatalf("unconfigured search must report configured=false")
	}
}

func TestWebSearchConfiguredTavily(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["api_key"] != "secret" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"title":"Result One","url":"https://a.example","content":"snippet one"},{"title":"Result Two","url":"https://b.example","content":"snippet two"}]}`)
	}))
	defer srv.Close()

	ws := bypassNetworkWorkspace()
	ws.Search = SearchConfig{Provider: "tavily", APIKey: "secret", Endpoint: srv.URL}
	tool := NewWebSearchTool(ws)
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{"query": "go testing", "max_results": 2})
	if err != nil {
		t.Fatalf("web_search error: %v", err)
	}
	if !strings.Contains(result.DisplayText, "Result One") || !strings.Contains(result.DisplayText, "https://b.example") {
		t.Fatalf("expected formatted results, got %q", result.DisplayText)
	}
	if count, _ := result.Meta["result_count"].(int); count != 2 {
		t.Fatalf("expected 2 results, got %d", count)
	}
}

func TestWebSearchUnsupportedProvider(t *testing.T) {
	ws := bypassNetworkWorkspace()
	ws.Search = SearchConfig{Provider: "made-up", APIKey: "key"}
	tool := NewWebSearchTool(ws)
	_, err := tool.Execute(context.Background(), map[string]any{"query": "q"})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected unsupported-provider error, got %v", err)
	}
}

func TestHTMLToReadableText(t *testing.T) {
	in := "<div>One</div><div>Two</div><br>Three&nbsp;four<script>x()</script>"
	out := htmlToReadableText(in)
	if strings.Contains(out, "x()") {
		t.Fatalf("script not stripped: %q", out)
	}
	for _, want := range []string{"One", "Two", "Three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}
