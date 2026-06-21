package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Default limits for the built-in web tools. These are intentionally
// conservative so a default install can read a URL without exhausting memory
// or hanging on a slow host. Callers may lower (not raise) them per-call.
const (
	webFetchDefaultMaxBytes  = 2 * 1024 * 1024 // 2 MiB readable cap
	webFetchHardMaxBytes     = 8 * 1024 * 1024 // absolute ceiling a caller cannot exceed
	webFetchDefaultTimeout   = 30 * time.Second
	webFetchMaxTimeout       = 120 * time.Second
	webFetchMaxRedirects     = 10
	webFetchMaxOutputChars   = 60000 // trim extracted text so a huge page cannot flood the model
	webSearchDefaultMax      = 5
	webSearchHardMax         = 20
	webSearchDefaultTimeout  = 30 * time.Second
	webToolUserAgentFragment = "kernforge-web/1.0"
)

// WebFetchTool fetches an http(s) URL and returns its readable text. It is
// gated through the PermissionManager under ActionNetwork (plan mode denies it,
// other modes prompt or honor a configured network allowlist), enforces a size
// cap and timeout, follows a bounded number of redirects, and reduces HTML to
// plain text.
type WebFetchTool struct{ ws Workspace }

func NewWebFetchTool(ws Workspace) WebFetchTool { return WebFetchTool{ws: ws} }

// ReadOnlyToolCall reports that web_fetch does not mutate the workspace. Network
// access is still gated separately under ActionNetwork inside Execute, so the
// read-only classification never bypasses the permission/allowlist check.
func (t WebFetchTool) ReadOnlyToolCall() bool {
	return true
}

func (t WebFetchTool) SupportsParallelToolCalls() bool {
	return true
}

func (t WebFetchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "web_fetch",
		Description: "Fetch an http(s) URL and return its readable text content. Follows redirects, enforces a size cap and timeout, and reduces HTML to plain text. Network access is gated by the active permission mode (plan mode denies it) and any configured network allowlist.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "Required. The http(s) URL to fetch.",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "Optional positive cap on bytes read from the response body. Defaults to a safe limit; values above the hard ceiling are clamped.",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional positive request timeout in seconds. Defaults to a safe value; clamped to a maximum.",
				},
				"raw": map[string]any{
					"type":        "boolean",
					"description": "Optional. When true, return the raw response body without HTML-to-text reduction.",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (t WebFetchTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t WebFetchTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	rawURL := strings.TrimSpace(stringValue(args, "url"))
	if rawURL == "" {
		return ToolExecutionResult{}, fmt.Errorf("web_fetch requires a non-empty url")
	}
	parsed, err := validateWebFetchURL(rawURL)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	maxBytes, err := webFetchMaxBytesArg(args)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	timeout, err := webFetchTimeoutArg(args)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	wantRaw := boolValue(args, "raw", false)

	// Gate outbound access before any connection is made. Plan mode denies this;
	// other modes prompt or honor the configured network allowlist (keyed on the
	// host so per-domain rules apply).
	if err := t.ws.EnsureNetworkWithContext(ctx, "web_fetch "+parsed.String()); err != nil {
		return ToolExecutionResult{}, err
	}

	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, finalURL, contentType, status, err := t.doFetch(fetchCtx, parsed, maxBytes)
	if err != nil {
		return ToolExecutionResult{}, err
	}

	text := string(body)
	reducedHTML := false
	if !wantRaw && contentTypeIsHTML(contentType, finalURL) {
		text = htmlToReadableText(text)
		reducedHTML = true
	}
	text = strings.TrimSpace(text)
	truncatedChars := false
	if len(text) > webFetchMaxOutputChars {
		text = text[:webFetchMaxOutputChars]
		truncatedChars = true
	}

	display := text
	if display == "" {
		display = fmt.Sprintf("(no readable content; status %d, content-type %q)", status, strings.TrimSpace(contentType))
	}

	meta := map[string]any{
		"url":             finalURL,
		"status":          status,
		"content_type":    strings.TrimSpace(contentType),
		"bytes":           len(body),
		"reduced_html":    reducedHTML,
		"truncated_chars": truncatedChars,
		"effect":          "network",
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: display, Meta: meta}, nil
}

// doFetch performs the request with a bounded redirect chain and a hard read
// cap. The size cap is enforced on the body stream (not only Content-Length) so
// a server that under-reports or omits the length cannot exceed the limit.
func (t WebFetchTool) doFetch(ctx context.Context, parsed *url.URL, maxBytes int64) ([]byte, string, string, int, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= webFetchMaxRedirects {
				return fmt.Errorf("web_fetch stopped after %d redirects", webFetchMaxRedirects)
			}
			// A redirect must stay on an http(s) scheme; reject attempts to bounce
			// to file://, ftp://, or similar.
			if !schemeIsHTTP(req.URL.Scheme) {
				return fmt.Errorf("web_fetch refused redirect to unsupported scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", "", 0, fmt.Errorf("web_fetch could not build request: %w", err)
	}
	req.Header.Set("User-Agent", webToolUserAgentFragment)
	req.Header.Set("Accept", "text/html,text/plain,application/xhtml+xml,application/json;q=0.9,*/*;q=0.5")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", 0, fmt.Errorf("web_fetch failed: %w", err)
	}
	defer resp.Body.Close()

	// Read at most maxBytes; reading maxBytes+1 lets us detect truncation.
	limited := io.LimitReader(resp.Body, maxBytes+1)
	data, readErr := io.ReadAll(limited)
	if readErr != nil {
		return nil, "", "", resp.StatusCode, fmt.Errorf("web_fetch read error: %w", readErr)
	}
	if int64(len(data)) > maxBytes {
		data = data[:maxBytes]
	}
	finalURL := parsed.String()
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	if resp.StatusCode >= 400 {
		preview := strings.TrimSpace(string(data))
		if len(preview) > 512 {
			preview = preview[:512]
		}
		return data, finalURL, resp.Header.Get("Content-Type"), resp.StatusCode,
			fmt.Errorf("web_fetch got HTTP %d for %s: %s", resp.StatusCode, finalURL, preview)
	}
	return data, finalURL, resp.Header.Get("Content-Type"), resp.StatusCode, nil
}

// validateWebFetchURL parses rawURL and rejects anything that is not an http(s)
// URL with a host. Scheme/host validation here prevents SSRF via non-http
// schemes and gives the network gate a meaningful target string.
func validateWebFetchURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("web_fetch could not parse url %q: %w", rawURL, err)
	}
	if !schemeIsHTTP(parsed.Scheme) {
		return nil, fmt.Errorf("web_fetch only supports http and https urls, got scheme %q", parsed.Scheme)
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("web_fetch url %q has no host", rawURL)
	}
	return parsed, nil
}

func schemeIsHTTP(scheme string) bool {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func webFetchMaxBytesArg(args map[string]any) (int64, error) {
	raw, ok := args["max_bytes"]
	if !ok || raw == nil {
		return webFetchDefaultMaxBytes, nil
	}
	value, ok := numericIntValue(raw)
	if !ok {
		return 0, fmt.Errorf("max_bytes must be an integer")
	}
	if value <= 0 {
		return 0, fmt.Errorf("max_bytes must be a positive integer")
	}
	capped := int64(value)
	if capped > webFetchHardMaxBytes {
		capped = webFetchHardMaxBytes
	}
	return capped, nil
}

func webFetchTimeoutArg(args map[string]any) (time.Duration, error) {
	raw, ok := args["timeout_seconds"]
	if !ok || raw == nil {
		return webFetchDefaultTimeout, nil
	}
	value, ok := numericIntValue(raw)
	if !ok {
		return 0, fmt.Errorf("timeout_seconds must be an integer")
	}
	if value <= 0 {
		return 0, fmt.Errorf("timeout_seconds must be a positive integer")
	}
	timeout := time.Duration(value) * time.Second
	if timeout > webFetchMaxTimeout {
		timeout = webFetchMaxTimeout
	}
	return timeout, nil
}

func contentTypeIsHTML(contentType string, finalURL string) bool {
	lower := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(lower, "text/html") || strings.Contains(lower, "application/xhtml") {
		return true
	}
	// When the server omits a content type, fall back to a URL-suffix guess so a
	// plain ".html" link is still reduced.
	if lower == "" {
		lowerURL := strings.ToLower(finalURL)
		if strings.HasSuffix(lowerURL, ".html") || strings.HasSuffix(lowerURL, ".htm") {
			return true
		}
	}
	return false
}

var (
	htmlScriptStyleRe = regexp.MustCompile(`(?is)<(script|style|head|noscript)[^>]*>.*?</(?:script|style|head|noscript)>`)
	htmlCommentRe     = regexp.MustCompile(`(?is)<!--.*?-->`)
	htmlBlockBreakRe  = regexp.MustCompile(`(?i)</(p|div|section|article|header|footer|li|tr|h[1-6]|ul|ol|table|blockquote)>`)
	htmlLineBreakRe   = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlTagRe         = regexp.MustCompile(`(?s)<[^>]+>`)
	htmlBlankLinesRe  = regexp.MustCompile(`\n{3,}`)
	htmlSpacesRe      = regexp.MustCompile(`[ \t]{2,}`)
)

// htmlToReadableText performs a basic, dependency-free HTML-to-text reduction:
// it drops script/style/head/comment regions, turns common block-closing tags
// into newlines, strips the remaining tags, unescapes entities, and collapses
// excess whitespace. It is intentionally simple (no DOM parse) so it cannot
// hang on malformed markup.
func htmlToReadableText(input string) string {
	text := input
	text = htmlCommentRe.ReplaceAllString(text, "")
	text = htmlScriptStyleRe.ReplaceAllString(text, "")
	text = htmlLineBreakRe.ReplaceAllString(text, "\n")
	text = htmlBlockBreakRe.ReplaceAllString(text, "\n")
	text = htmlTagRe.ReplaceAllString(text, "")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = htmlSpacesRe.ReplaceAllString(text, " ")
	// Trim trailing spaces on each line, then collapse runs of blank lines.
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	text = strings.Join(lines, "\n")
	text = htmlBlankLinesRe.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// WebSearchTool runs a web search through a configured provider. When no
// provider/api_key is configured it returns a clear "not configured" message
// rather than failing, so a default install degrades gracefully. The configured
// path supports the common JSON search backends (Tavily and Brave); other
// providers report that they are unsupported.
type WebSearchTool struct{ ws Workspace }

func NewWebSearchTool(ws Workspace) WebSearchTool { return WebSearchTool{ws: ws} }

// ReadOnlyToolCall reports that web_search does not mutate the workspace. The
// configured-path network access is gated under ActionNetwork inside Execute.
func (t WebSearchTool) ReadOnlyToolCall() bool {
	return true
}

func (t WebSearchTool) SupportsParallelToolCalls() bool {
	return true
}

func (t WebSearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "web_search",
		Description: "Search the web for a query and return result titles, URLs, and snippets. Requires a configured search provider and API key (config: search.provider + search.api_key); without one it reports that web_search is not configured. Network access is gated by the active permission mode and any configured allowlist.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Required. The search query.",
				},
				"max_results": map[string]any{
					"type":        "integer",
					"description": "Optional positive cap on the number of results. Defaults to a small value; clamped to a maximum.",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t WebSearchTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t WebSearchTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	query := strings.TrimSpace(stringValue(args, "query"))
	if query == "" {
		return ToolExecutionResult{}, fmt.Errorf("web_search requires a non-empty query")
	}
	maxResults, err := webSearchMaxResultsArg(args)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if !t.ws.Search.IsConfigured() {
		msg := "web_search is not configured. Set search.provider and search.api_key in the Kernforge config (supported providers: tavily, brave) to enable real web search, or use web_fetch with a known URL."
		meta := map[string]any{
			"configured": false,
			"effect":     "inspect",
		}
		addEffectiveExecutionContextMetadata(meta, t.ws, nil)
		return ToolExecutionResult{DisplayText: msg, Meta: meta}, nil
	}

	provider := strings.ToLower(strings.TrimSpace(t.ws.Search.Provider))
	target := fmt.Sprintf("web_search provider=%s query=%s", provider, query)
	if err := t.ws.EnsureNetworkWithContext(ctx, target); err != nil {
		return ToolExecutionResult{}, err
	}

	searchCtx, cancel := context.WithTimeout(ctx, webSearchDefaultTimeout)
	defer cancel()

	results, err := t.runSearch(searchCtx, provider, query, maxResults)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	display := formatWebSearchResults(query, results)
	meta := map[string]any{
		"configured":   true,
		"provider":     provider,
		"result_count": len(results),
		"effect":       "network",
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: display, Meta: meta}, nil
}

type webSearchResult struct {
	Title   string
	URL     string
	Snippet string
}

func (t WebSearchTool) runSearch(ctx context.Context, provider, query string, maxResults int) ([]webSearchResult, error) {
	switch provider {
	case "tavily":
		return t.searchTavily(ctx, query, maxResults)
	case "brave":
		return t.searchBrave(ctx, query, maxResults)
	default:
		return nil, fmt.Errorf("web_search provider %q is not supported; supported providers: tavily, brave", provider)
	}
}

func (t WebSearchTool) searchTavily(ctx context.Context, query string, maxResults int) ([]webSearchResult, error) {
	endpoint := strings.TrimSpace(t.ws.Search.Endpoint)
	if endpoint == "" {
		endpoint = "https://api.tavily.com/search"
	}
	payload := map[string]any{
		"api_key":     strings.TrimSpace(t.ws.Search.APIKey),
		"query":       query,
		"max_results": maxResults,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("web_search could not encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("web_search could not build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", webToolUserAgentFragment)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("web_search request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, webFetchHardMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("web_search read error: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("web_search provider returned HTTP %d: %s", resp.StatusCode, webSearchErrorPreview(data))
	}
	var decoded struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("web_search could not decode provider response: %w", err)
	}
	out := make([]webSearchResult, 0, len(decoded.Results))
	for _, r := range decoded.Results {
		out = append(out, webSearchResult{
			Title:   strings.TrimSpace(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: strings.TrimSpace(r.Content),
		})
		if len(out) >= maxResults {
			break
		}
	}
	return out, nil
}

func (t WebSearchTool) searchBrave(ctx context.Context, query string, maxResults int) ([]webSearchResult, error) {
	endpoint := strings.TrimSpace(t.ws.Search.Endpoint)
	if endpoint == "" {
		endpoint = "https://api.search.brave.com/res/v1/web/search"
	}
	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("web_search could not parse endpoint: %w", err)
	}
	q := reqURL.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", maxResults))
	reqURL.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("web_search could not build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", strings.TrimSpace(t.ws.Search.APIKey))
	req.Header.Set("User-Agent", webToolUserAgentFragment)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("web_search request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, webFetchHardMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("web_search read error: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("web_search provider returned HTTP %d: %s", resp.StatusCode, webSearchErrorPreview(data))
	}
	var decoded struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("web_search could not decode provider response: %w", err)
	}
	out := make([]webSearchResult, 0, len(decoded.Web.Results))
	for _, r := range decoded.Web.Results {
		out = append(out, webSearchResult{
			Title:   strings.TrimSpace(r.Title),
			URL:     strings.TrimSpace(r.URL),
			Snippet: strings.TrimSpace(r.Description),
		})
		if len(out) >= maxResults {
			break
		}
	}
	return out, nil
}

func webSearchMaxResultsArg(args map[string]any) (int, error) {
	raw, ok := args["max_results"]
	if !ok || raw == nil {
		return webSearchDefaultMax, nil
	}
	value, ok := numericIntValue(raw)
	if !ok {
		return 0, fmt.Errorf("max_results must be an integer")
	}
	if value <= 0 {
		return 0, fmt.Errorf("max_results must be a positive integer")
	}
	if value > webSearchHardMax {
		value = webSearchHardMax
	}
	return value, nil
}

func webSearchErrorPreview(data []byte) string {
	preview := strings.TrimSpace(string(data))
	if len(preview) > 512 {
		preview = preview[:512]
	}
	return preview
}

func formatWebSearchResults(query string, results []webSearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No web search results for %q.", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Web search results for %q:\n", query)
	for i, r := range results {
		title := r.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
