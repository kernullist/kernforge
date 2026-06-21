package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mcpOAuthTokenStoreStub is an in-memory token store for tests.
type mcpOAuthTokenStoreStub struct {
	token *mcpOAuthToken
}

func (s mcpOAuthTokenStoreStub) Load(string) (*mcpOAuthToken, bool) {
	if s.token == nil {
		return nil, false
	}
	clone := *s.token
	return &clone, true
}

func (s mcpOAuthTokenStoreStub) Save(string, *mcpOAuthToken) error {
	return nil
}

// mcpOAuthMutableStore records saves so refresh persistence can be asserted.
type mcpOAuthMutableStore struct {
	token *mcpOAuthToken
	saved *mcpOAuthToken
}

func (s *mcpOAuthMutableStore) Load(string) (*mcpOAuthToken, bool) {
	if s.token == nil {
		return nil, false
	}
	clone := *s.token
	return &clone, true
}

func (s *mcpOAuthMutableStore) Save(_ string, token *mcpOAuthToken) error {
	clone := *token
	s.saved = &clone
	return nil
}

func TestNewMCPPKCEProducesValidS256Pair(t *testing.T) {
	pkce, err := newMCPPKCE()
	if err != nil {
		t.Fatalf("newMCPPKCE: %v", err)
	}
	if pkce.Method != "S256" {
		t.Fatalf("expected S256 method, got %q", pkce.Method)
	}
	if strings.TrimSpace(pkce.Verifier) == "" || strings.TrimSpace(pkce.Challenge) == "" {
		t.Fatalf("expected non-empty verifier and challenge, got %#v", pkce)
	}
	// The challenge must be base64url(SHA256(verifier)).
	sum := sha256.Sum256([]byte(pkce.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pkce.Challenge != want {
		t.Fatalf("challenge does not match S256 of verifier: got %q want %q", pkce.Challenge, want)
	}
}

func TestMCPOAuthWellKnownURL(t *testing.T) {
	got, err := mcpOAuthWellKnownURL("https://auth.example.com", "oauth-authorization-server")
	if err != nil {
		t.Fatalf("mcpOAuthWellKnownURL: %v", err)
	}
	if got != "https://auth.example.com/.well-known/oauth-authorization-server" {
		t.Fatalf("unexpected well-known URL: %q", got)
	}
	// A path component must be appended after the well-known segment per RFC 8414.
	got, err = mcpOAuthWellKnownURL("https://auth.example.com/tenant1", "oauth-protected-resource")
	if err != nil {
		t.Fatalf("mcpOAuthWellKnownURL: %v", err)
	}
	if got != "https://auth.example.com/.well-known/oauth-protected-resource/tenant1" {
		t.Fatalf("unexpected tenant well-known URL: %q", got)
	}
}

func TestMCPOAuthDiscoverServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://auth.example.com",
			"authorization_endpoint": "https://auth.example.com/authorize",
			"token_endpoint":         "https://auth.example.com/token",
		})
	}))
	defer srv.Close()

	meta, err := mcpOAuthDiscoverServer(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("mcpOAuthDiscoverServer: %v", err)
	}
	if meta.TokenEndpoint != "https://auth.example.com/token" {
		t.Fatalf("unexpected token endpoint: %q", meta.TokenEndpoint)
	}
}

func TestMCPOAuthAuthorizeURLIncludesPKCE(t *testing.T) {
	meta := &mcpOAuthServerMetadata{AuthorizationEndpoint: "https://auth.example.com/authorize"}
	pkce := mcpPKCE{Verifier: "verifier", Challenge: "challenge", Method: "S256"}
	raw, err := mcpOAuthAuthorizeURL(meta, "client-id", "http://127.0.0.1:9999/callback", "openid profile", "state123", pkce)
	if err != nil {
		t.Fatalf("mcpOAuthAuthorizeURL: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	q := parsed.Query()
	if q.Get("response_type") != "code" {
		t.Fatalf("expected response_type=code, got %q", q.Get("response_type"))
	}
	if q.Get("code_challenge") != "challenge" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("expected PKCE challenge params, got %v", q)
	}
	if q.Get("client_id") != "client-id" || q.Get("state") != "state123" {
		t.Fatalf("expected client_id/state params, got %v", q)
	}
}

func TestMCPOAuthExchangeCodeAndRefresh(t *testing.T) {
	var lastGrant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		lastGrant = r.Form.Get("grant_type")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-" + lastGrant,
			"token_type":    "Bearer",
			"refresh_token": "refresh-next",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	token, err := mcpOAuthExchangeCode(context.Background(), srv.URL, "client", "code", "http://127.0.0.1/callback", "verifier")
	if err != nil {
		t.Fatalf("mcpOAuthExchangeCode: %v", err)
	}
	if lastGrant != "authorization_code" {
		t.Fatalf("expected authorization_code grant, got %q", lastGrant)
	}
	if token.AccessToken != "access-authorization_code" || token.RefreshToken != "refresh-next" {
		t.Fatalf("unexpected token: %#v", token)
	}
	if token.TokenEndpoint != srv.URL || token.ClientID != "client" {
		t.Fatalf("expected endpoint/client recorded on token, got %#v", token)
	}
	if !token.valid() {
		t.Fatalf("expected freshly minted token to be valid")
	}

	refreshed, err := mcpOAuthRefreshToken(context.Background(), srv.URL, "client", "refresh-next", "")
	if err != nil {
		t.Fatalf("mcpOAuthRefreshToken: %v", err)
	}
	if lastGrant != "refresh_token" {
		t.Fatalf("expected refresh_token grant, got %q", lastGrant)
	}
	if refreshed.AccessToken != "access-refresh_token" {
		t.Fatalf("unexpected refreshed token: %#v", refreshed)
	}
}

func TestMCPOAuthValidTokenRefreshesExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh-access",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	store := &mcpOAuthMutableStore{
		token: &mcpOAuthToken{
			AccessToken:   "stale",
			RefreshToken:  "refresh-token",
			TokenEndpoint: srv.URL,
			ClientID:      "client",
			ExpiresAt:     time.Now().Add(-time.Minute), // already expired
		},
	}
	cfg := MCPServerConfig{
		URL:   "https://example.com/mcp",
		OAuth: &MCPServerOAuthConfig{ClientID: "client"},
	}
	token, ok := mcpOAuthValidToken(context.Background(), cfg, store)
	if !ok {
		t.Fatalf("expected refreshed token to be returned")
	}
	if token.AccessToken != "fresh-access" {
		t.Fatalf("expected fresh access token, got %#v", token)
	}
	// Refreshed token must be persisted and carry forward the prior refresh token.
	if store.saved == nil || store.saved.AccessToken != "fresh-access" {
		t.Fatalf("expected refreshed token to be saved, got %#v", store.saved)
	}
	if store.saved.RefreshToken != "refresh-token" {
		t.Fatalf("expected prior refresh token carried forward, got %#v", store.saved)
	}
}

func TestMCPOAuthValidTokenNoStoredToken(t *testing.T) {
	cfg := MCPServerConfig{
		URL:   "https://example.com/mcp",
		OAuth: &MCPServerOAuthConfig{ClientID: "client"},
	}
	if _, ok := mcpOAuthValidToken(context.Background(), cfg, mcpOAuthTokenStoreStub{}); ok {
		t.Fatalf("expected no token when store is empty")
	}
}

// TestMCPOAuthInteractiveAuthorizeStoresAndAttachesToken exercises the wired
// interactive flow end to end against a hermetic fake authorization server. The
// browser-open step is stubbed: instead of launching a real browser it parses
// the authorize URL, extracts the loopback redirect_uri and state, and drives
// the callback with a synthetic authorization code. It then asserts a token is
// stored under the server key and that the auth-status reporter sees it.
func TestMCPOAuthInteractiveAuthorizeStoresAndAttachesToken(t *testing.T) {
	var sawCode, sawVerifier, sawRedirect string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/.well-known/oauth-authorization-server"):
			base := "http://" + r.Host
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 base,
				"authorization_endpoint": base + "/authorize",
				"token_endpoint":         base + "/token",
				"scopes_supported":       []string{"profile", "openid"},
			})
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			sawCode = r.Form.Get("code")
			sawVerifier = r.Form.Get("code_verifier")
			sawRedirect = r.Form.Get("redirect_uri")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "interactive-access",
				"token_type":    "Bearer",
				"refresh_token": "interactive-refresh",
				"expires_in":    3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer authSrv.Close()

	// Stub the browser-open step: hit the loopback callback with a synthetic code.
	prevOpen := mcpOAuthOpenURL
	mcpOAuthOpenURL = func(authorizeURL string) error {
		parsed, err := url.Parse(authorizeURL)
		if err != nil {
			return err
		}
		q := parsed.Query()
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		cb, err := url.Parse(redirect)
		if err != nil {
			return err
		}
		cbq := cb.Query()
		cbq.Set("code", "synthetic-code")
		cbq.Set("state", state)
		cb.RawQuery = cbq.Encode()
		go func() {
			resp, err := http.Get(cb.String())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	defer func() { mcpOAuthOpenURL = prevOpen }()

	cfg := MCPServerConfig{
		Name:  "fake",
		URL:   authSrv.URL + "/mcp",
		OAuth: &MCPServerOAuthConfig{ClientID: "client-id"},
	}
	store := &mcpOAuthMutableStore{}

	if err := mcpOAuthInteractiveAuthorize(context.Background(), cfg, store, nil); err != nil {
		t.Fatalf("mcpOAuthInteractiveAuthorize: %v", err)
	}
	if sawCode != "synthetic-code" {
		t.Fatalf("token endpoint did not receive the synthetic code, got %q", sawCode)
	}
	if strings.TrimSpace(sawVerifier) == "" {
		t.Fatalf("token endpoint did not receive a code_verifier (PKCE not wired)")
	}
	if !strings.HasPrefix(sawRedirect, "http://127.0.0.1:") {
		t.Fatalf("token endpoint did not receive a loopback redirect_uri, got %q", sawRedirect)
	}
	if store.saved == nil || store.saved.AccessToken != "interactive-access" {
		t.Fatalf("expected interactive token to be stored, got %#v", store.saved)
	}

	// The stored token must be attached: prime the store to load it back and
	// confirm the honest auth-status reporter sees an authorized server.
	store.token = store.saved
	status := mcpHTTPAuthStatusWithStore(cfg, map[string]string{}, func(string) string { return "" }, store)
	if status != "oauth_configured" {
		t.Fatalf("expected oauth_configured after authorize, got %q", status)
	}
}

// TestMCPOAuthCallbackServerRejectsStateMismatch confirms the loopback callback
// handler enforces the anti-CSRF state.
func TestMCPOAuthCallbackServerRejectsStateMismatch(t *testing.T) {
	cb, err := newMCPOAuthCallbackServer("expected-state")
	if err != nil {
		t.Fatalf("newMCPOAuthCallbackServer: %v", err)
	}
	defer cb.Close()

	target := cb.redirectURI + "?code=abc&state=wrong-state"
	resp, err := http.Get(target)
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on state mismatch, got %d", resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, waitErr := cb.Wait(ctx); waitErr == nil {
		t.Fatalf("expected a state-mismatch error from Wait")
	}
}

// TestMCPOAuthResolveServerMetadataViaResource exercises the RFC 9728
// protected-resource discovery path that points at an authorization server.
func TestMCPOAuthResolveServerMetadataViaResource(t *testing.T) {
	var authBase string
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	authBase = srv.URL
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              authBase,
			"authorization_servers": []string{authBase},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 authBase,
			"authorization_endpoint": authBase + "/authorize",
			"token_endpoint":         authBase + "/token",
		})
	})

	cfg := MCPServerConfig{
		Name:          "res",
		URL:           "https://example.com/mcp",
		OAuthResource: srv.URL,
	}
	meta, err := mcpOAuthResolveServerMetadata(context.Background(), cfg)
	if err != nil {
		t.Fatalf("mcpOAuthResolveServerMetadata: %v", err)
	}
	if meta.TokenEndpoint != authBase+"/token" {
		t.Fatalf("unexpected token endpoint via resource discovery: %q", meta.TokenEndpoint)
	}
}

func TestNegotiateMCPProtocolVersion(t *testing.T) {
	cases := []struct {
		client string
		server string
		want   string
	}{
		{mcpClientProtocolVersion, "2025-06-18", "2025-06-18"},
		{mcpClientProtocolVersion, "2024-11-05", "2024-11-05"}, // older but supported server
		{mcpClientProtocolVersion, "", mcpClientProtocolVersion},
		{mcpClientProtocolVersion, "2099-01-01", mcpClientProtocolVersion}, // unknown -> fall back
	}
	for _, tc := range cases {
		if got := negotiateMCPProtocolVersion(tc.client, tc.server); got != tc.want {
			t.Fatalf("negotiate(%q,%q)=%q want %q", tc.client, tc.server, got, tc.want)
		}
	}
}
