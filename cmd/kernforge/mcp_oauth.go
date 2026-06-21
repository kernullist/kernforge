package main

// OAuth 2.0 Authorization Code + PKCE support for streamable HTTP MCP servers.
//
// Scope of this implementation:
//   - .well-known discovery: RFC 9728 (protected resource metadata) and
//     RFC 8414 (authorization server metadata).
//   - PKCE (RFC 7636) S256 code verifier/challenge generation.
//   - Authorization URL construction for a local loopback redirect.
//   - Token endpoint exchange (authorization_code) and refresh (refresh_token).
//   - On-disk, per-server token storage with refresh-on-expiry.
//   - Attaching the resulting Bearer token to MCP HTTP requests.
//
// The interactive browser authorize step (opening a browser and running the
// loopback callback server) is intentionally not wired into the headless
// startup path; the machinery is provided so a future interactive command can
// complete the flow. Until a token is stored, the auth status is reported
// truthfully as "oauth_no_token" rather than a misleading "oauth_configured".

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// mcpOAuthToken is the persisted token material for one MCP server.
type mcpOAuthToken struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	// Endpoints discovered when the token was minted, kept so refresh can run
	// without repeating discovery.
	TokenEndpoint string `json:"token_endpoint,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
}

// tokenExpiryLeeway treats a token as expired slightly early so an in-flight
// request does not race the actual expiry.
const mcpOAuthTokenExpiryLeeway = 60 * time.Second

func (t *mcpOAuthToken) valid() bool {
	if t == nil {
		return false
	}
	if strings.TrimSpace(t.AccessToken) == "" {
		return false
	}
	if t.ExpiresAt.IsZero() {
		// No expiry advertised; treat as a long-lived token.
		return true
	}
	return time.Now().Add(mcpOAuthTokenExpiryLeeway).Before(t.ExpiresAt)
}

func (t *mcpOAuthToken) bearerValue() string {
	if t == nil {
		return ""
	}
	tokenType := strings.TrimSpace(t.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	// Normalize the casing of the common bearer scheme for header output.
	if strings.EqualFold(tokenType, "bearer") {
		tokenType = "Bearer"
	}
	return tokenType + " " + strings.TrimSpace(t.AccessToken)
}

// mcpOAuthMetadata is the subset of RFC 8414 authorization server metadata we use.
type mcpOAuthServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint,omitempty"`
	ScopesSupported               []string `json:"scopes_supported,omitempty"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
}

// mcpOAuthProtectedResourceMetadata is the subset of RFC 9728 we use.
type mcpOAuthProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers,omitempty"`
}

// mcpOAuthTokenStore abstracts persistence so it can be exercised in tests.
type mcpOAuthTokenStore interface {
	Load(serverKey string) (*mcpOAuthToken, bool)
	Save(serverKey string, token *mcpOAuthToken) error
}

// mcpOAuthDiskStore stores one JSON file per server under the user config dir.
type mcpOAuthDiskStore struct {
	dir string
	mu  sync.Mutex
}

func newMCPOAuthDiskStore() *mcpOAuthDiskStore {
	return &mcpOAuthDiskStore{dir: filepath.Join(userConfigDir(), "mcp-oauth")}
}

func (s *mcpOAuthDiskStore) path(serverKey string) string {
	return filepath.Join(s.dir, mcpOAuthServerKeyHash(serverKey)+".json")
}

func (s *mcpOAuthDiskStore) Load(serverKey string) (*mcpOAuthToken, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(serverKey))
	if err != nil {
		return nil, false
	}
	var token mcpOAuthToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, false
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, false
	}
	return &token, true
}

func (s *mcpOAuthDiskStore) Save(serverKey string, token *mcpOAuthToken) error {
	if s == nil {
		return fmt.Errorf("oauth token store is not configured")
	}
	if token == nil {
		return fmt.Errorf("oauth token is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	// Tokens are secrets: keep the file owner-only.
	return os.WriteFile(s.path(serverKey), data, 0o600)
}

// mcpOAuthServerKeyHash derives a stable, filesystem-safe file name from the
// server identity (URL plus client id) without leaking the raw URL.
func mcpOAuthServerKeyHash(serverKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(serverKey)))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:32]
}

// mcpOAuthServerKey identifies a server's token bucket.
func mcpOAuthServerKey(cfg MCPServerConfig) string {
	clientID := ""
	if cfg.OAuth != nil {
		clientID = strings.TrimSpace(cfg.OAuth.ClientID)
	}
	return strings.TrimSpace(cfg.URL) + "|" + clientID
}

// defaultMCPOAuthTokenStore is the process-wide disk store, overridable in tests.
var defaultMCPOAuthTokenStore mcpOAuthTokenStore = newMCPOAuthDiskStore()

// mcpOAuthHTTPClient is the client used for discovery and token requests,
// overridable in tests.
var mcpOAuthHTTPClient = &http.Client{Timeout: 30 * time.Second}

// mcpOAuthConfigured reports whether the server config requests OAuth at all.
func mcpOAuthConfigured(cfg MCPServerConfig) bool {
	if cfg.OAuth != nil && strings.TrimSpace(cfg.OAuth.ClientID) != "" {
		return true
	}
	return strings.TrimSpace(cfg.OAuthResource) != ""
}

// mcpOAuthValidToken returns a currently-valid access token for the server,
// refreshing it if it is expired and a refresh token is available. It returns
// (nil, false) when no usable token can be produced without interactive
// authorization. Errors during refresh are non-fatal: they leave the caller in
// the "no token" state so startup can continue and report truthfully.
func mcpOAuthValidToken(ctx context.Context, cfg MCPServerConfig, store mcpOAuthTokenStore) (*mcpOAuthToken, bool) {
	if !mcpOAuthConfigured(cfg) {
		return nil, false
	}
	if store == nil {
		store = defaultMCPOAuthTokenStore
	}
	if store == nil {
		return nil, false
	}
	token, ok := store.Load(mcpOAuthServerKey(cfg))
	if !ok || token == nil {
		return nil, false
	}
	if token.valid() {
		return token, true
	}
	// Expired: attempt a refresh if we have the material to do so.
	if strings.TrimSpace(token.RefreshToken) == "" || strings.TrimSpace(token.TokenEndpoint) == "" {
		return nil, false
	}
	clientID := strings.TrimSpace(token.ClientID)
	if clientID == "" && cfg.OAuth != nil {
		clientID = strings.TrimSpace(cfg.OAuth.ClientID)
	}
	refreshed, err := mcpOAuthRefreshToken(ctx, token.TokenEndpoint, clientID, token.RefreshToken, token.Scope)
	if err != nil || refreshed == nil || !refreshed.valid() {
		return nil, false
	}
	// Carry forward fields the refresh response may omit.
	refreshed.TokenEndpoint = token.TokenEndpoint
	if strings.TrimSpace(refreshed.ClientID) == "" {
		refreshed.ClientID = clientID
	}
	if strings.TrimSpace(refreshed.RefreshToken) == "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	if err := store.Save(mcpOAuthServerKey(cfg), refreshed); err != nil {
		// Persisting failed but the in-memory token is still usable for now.
		return refreshed, true
	}
	return refreshed, true
}

// mcpPKCE holds a generated PKCE pair.
type mcpPKCE struct {
	Verifier  string
	Challenge string
	Method    string
}

// newMCPPKCE generates an RFC 7636 S256 PKCE verifier/challenge pair.
func newMCPPKCE() (mcpPKCE, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return mcpPKCE{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return mcpPKCE{Verifier: verifier, Challenge: challenge, Method: "S256"}, nil
}

// newMCPOAuthState generates a random anti-CSRF state value for the authorize step.
func newMCPOAuthState() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// mcpOAuthDiscoverResource fetches RFC 9728 protected resource metadata from the
// resource server, which points at the authorization server(s).
func mcpOAuthDiscoverResource(ctx context.Context, resourceURL string) (*mcpOAuthProtectedResourceMetadata, error) {
	metaURL, err := mcpOAuthWellKnownURL(resourceURL, "oauth-protected-resource")
	if err != nil {
		return nil, err
	}
	var meta mcpOAuthProtectedResourceMetadata
	if err := mcpOAuthFetchJSON(ctx, metaURL, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// mcpOAuthDiscoverServer fetches RFC 8414 authorization server metadata. It
// tries the oauth-authorization-server document and falls back to the OpenID
// Connect discovery document path used by many providers.
func mcpOAuthDiscoverServer(ctx context.Context, issuerURL string) (*mcpOAuthServerMetadata, error) {
	candidates := []string{"oauth-authorization-server", "openid-configuration"}
	var lastErr error
	for _, suffix := range candidates {
		metaURL, err := mcpOAuthWellKnownURL(issuerURL, suffix)
		if err != nil {
			lastErr = err
			continue
		}
		var meta mcpOAuthServerMetadata
		if err := mcpOAuthFetchJSON(ctx, metaURL, &meta); err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSpace(meta.TokenEndpoint) == "" {
			lastErr = fmt.Errorf("authorization server metadata at %s is missing token_endpoint", metaURL)
			continue
		}
		return &meta, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no authorization server metadata found for %q", issuerURL)
	}
	return nil, lastErr
}

// mcpOAuthWellKnownURL builds a .well-known discovery URL. Per RFC 8414/9728 the
// well-known segment is inserted between the host and any path component.
func mcpOAuthWellKnownURL(base string, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid discovery base url %q", base)
	}
	path := strings.Trim(parsed.Path, "/")
	wellKnown := "/.well-known/" + suffix
	if path != "" {
		wellKnown += "/" + path
	}
	out := url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: wellKnown}
	return out.String(), nil
}

func mcpOAuthFetchJSON(ctx context.Context, target string, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := mcpOAuthHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discovery request to %s returned %s", target, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode discovery metadata from %s: %w", target, err)
	}
	return nil
}

// mcpOAuthAuthorizeURL builds the authorization-code authorize URL with PKCE.
func mcpOAuthAuthorizeURL(meta *mcpOAuthServerMetadata, clientID string, redirectURI string, scope string, state string, pkce mcpPKCE) (string, error) {
	if meta == nil || strings.TrimSpace(meta.AuthorizationEndpoint) == "" {
		return "", fmt.Errorf("authorization server metadata is missing authorization_endpoint")
	}
	parsed, err := url.Parse(strings.TrimSpace(meta.AuthorizationEndpoint))
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	q.Set("response_type", "code")
	q.Set("client_id", strings.TrimSpace(clientID))
	q.Set("redirect_uri", strings.TrimSpace(redirectURI))
	q.Set("state", strings.TrimSpace(state))
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", pkce.Method)
	if s := strings.TrimSpace(scope); s != "" {
		q.Set("scope", s)
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// mcpOAuthExchangeCode exchanges an authorization code for tokens (PKCE).
func mcpOAuthExchangeCode(ctx context.Context, tokenEndpoint string, clientID string, code string, redirectURI string, verifier string) (*mcpOAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", strings.TrimSpace(code))
	form.Set("redirect_uri", strings.TrimSpace(redirectURI))
	form.Set("client_id", strings.TrimSpace(clientID))
	form.Set("code_verifier", strings.TrimSpace(verifier))
	token, err := mcpOAuthPostToken(ctx, tokenEndpoint, form)
	if err != nil {
		return nil, err
	}
	token.TokenEndpoint = strings.TrimSpace(tokenEndpoint)
	token.ClientID = strings.TrimSpace(clientID)
	return token, nil
}

// mcpOAuthRefreshToken exchanges a refresh token for a fresh access token.
func mcpOAuthRefreshToken(ctx context.Context, tokenEndpoint string, clientID string, refreshToken string, scope string) (*mcpOAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", strings.TrimSpace(refreshToken))
	if id := strings.TrimSpace(clientID); id != "" {
		form.Set("client_id", id)
	}
	if s := strings.TrimSpace(scope); s != "" {
		form.Set("scope", s)
	}
	return mcpOAuthPostToken(ctx, tokenEndpoint, form)
}

// mcpOAuthTokenResponse is the RFC 6749 token endpoint response shape.
type mcpOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func mcpOAuthPostToken(ctx context.Context, tokenEndpoint string, form url.Values) (*mcpOAuthToken, error) {
	endpoint := strings.TrimSpace(tokenEndpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("token endpoint is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := mcpOAuthHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var parsed mcpOAuthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode token response from %s: %w", endpoint, err)
	}
	if strings.TrimSpace(parsed.Error) != "" {
		msg := strings.TrimSpace(parsed.Error)
		if desc := strings.TrimSpace(parsed.ErrorDesc); desc != "" {
			msg += ": " + desc
		}
		return nil, fmt.Errorf("token endpoint error: %s", msg)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token endpoint %s returned %s", endpoint, resp.Status)
	}
	if strings.TrimSpace(parsed.AccessToken) == "" {
		return nil, fmt.Errorf("token endpoint %s returned no access_token", endpoint)
	}
	token := &mcpOAuthToken{
		AccessToken:  strings.TrimSpace(parsed.AccessToken),
		TokenType:    strings.TrimSpace(parsed.TokenType),
		RefreshToken: strings.TrimSpace(parsed.RefreshToken),
		Scope:        strings.TrimSpace(parsed.Scope),
	}
	if parsed.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	}
	return token, nil
}

// mcpOAuthScopeForConfig picks a requested scope string for discovery/authorize.
func mcpOAuthScopeForConfig(meta *mcpOAuthServerMetadata) string {
	if meta == nil || len(meta.ScopesSupported) == 0 {
		return ""
	}
	scopes := append([]string(nil), meta.ScopesSupported...)
	sort.Strings(scopes)
	return strings.Join(scopes, " ")
}

// mcpOAuthOpenURL opens a URL in the system browser. It is a package variable so
// tests can stub the browser-open step without launching a real browser.
var mcpOAuthOpenURL = OpenExternalURL

// mcpOAuthAuthorizeTimeout bounds how long the interactive flow waits for the
// browser redirect callback before giving up.
const mcpOAuthAuthorizeTimeout = 3 * time.Minute

// mcpOAuthCallbackResult carries the outcome of the loopback redirect callback.
type mcpOAuthCallbackResult struct {
	code  string
	state string
	err   error
}

// mcpOAuthCallbackServer is a single-shot loopback HTTP server that captures the
// authorization code returned to the redirect_uri. It listens on an ephemeral
// localhost port and reports the first callback it receives.
type mcpOAuthCallbackServer struct {
	listener    net.Listener
	server      *http.Server
	redirectURI string
	expectState string
	results     chan mcpOAuthCallbackResult
	once        sync.Once
}

// newMCPOAuthCallbackServer binds a loopback listener on an ephemeral port and
// derives the redirect_uri (http://127.0.0.1:<port>/callback). The caller must
// Close the server when done.
func newMCPOAuthCallbackServer(expectState string) (*mcpOAuthCallbackServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start loopback callback listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	cb := &mcpOAuthCallbackServer{
		listener:    listener,
		redirectURI: fmt.Sprintf("http://127.0.0.1:%d/callback", port),
		expectState: strings.TrimSpace(expectState),
		results:     make(chan mcpOAuthCallbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cb.handleCallback)
	cb.server = &http.Server{Handler: mux}
	go func() {
		// Serve blocks until the listener is closed; an http.ErrServerClosed is
		// expected on shutdown and is not surfaced.
		_ = cb.server.Serve(listener)
	}()
	return cb, nil
}

// handleCallback validates the state, captures the code, and reports the result
// exactly once. It always writes a small human-readable page back to the
// browser so the user knows they can return to the terminal.
func (cb *mcpOAuthCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	result := mcpOAuthCallbackResult{
		code:  strings.TrimSpace(q.Get("code")),
		state: strings.TrimSpace(q.Get("state")),
	}
	if errParam := strings.TrimSpace(q.Get("error")); errParam != "" {
		msg := errParam
		if desc := strings.TrimSpace(q.Get("error_description")); desc != "" {
			msg += ": " + desc
		}
		result.err = fmt.Errorf("authorization server returned error: %s", msg)
	} else if cb.expectState != "" && result.state != cb.expectState {
		// A state mismatch is a CSRF signal; reject the callback.
		result.err = fmt.Errorf("oauth state mismatch: possible CSRF, rejecting callback")
	} else if result.code == "" {
		result.err = fmt.Errorf("authorization callback did not include a code")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if result.err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, mcpOAuthCallbackPage("Authorization failed", html.EscapeString(result.err.Error())))
	} else {
		_, _ = io.WriteString(w, mcpOAuthCallbackPage("Authorization complete", "You may close this tab and return to the terminal."))
	}

	cb.once.Do(func() {
		cb.results <- result
	})
}

// mcpOAuthCallbackPage renders a minimal ASCII-only HTML acknowledgement page.
func mcpOAuthCallbackPage(title string, body string) string {
	return "<!doctype html><html><head><meta charset=\"utf-8\"><title>" + html.EscapeString(title) +
		"</title></head><body><h3>" + html.EscapeString(title) + "</h3><p>" + body + "</p></body></html>"
}

// Wait blocks until a callback arrives or the context is done.
func (cb *mcpOAuthCallbackServer) Wait(ctx context.Context) (mcpOAuthCallbackResult, error) {
	select {
	case res := <-cb.results:
		return res, res.err
	case <-ctx.Done():
		return mcpOAuthCallbackResult{}, ctx.Err()
	}
}

// Close shuts down the callback server and releases the listener.
func (cb *mcpOAuthCallbackServer) Close() {
	if cb == nil {
		return
	}
	if cb.server != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = cb.server.Shutdown(shutdownCtx)
	}
	if cb.listener != nil {
		_ = cb.listener.Close()
	}
}

// mcpOAuthResolveServerMetadata runs discovery for a server config. When an
// oauth_resource is configured it follows RFC 9728 protected-resource metadata
// to find the authorization server; otherwise it discovers directly against the
// MCP server URL (with the OIDC fallback already built into
// mcpOAuthDiscoverServer).
func mcpOAuthResolveServerMetadata(ctx context.Context, cfg MCPServerConfig) (*mcpOAuthServerMetadata, error) {
	if resource := strings.TrimSpace(cfg.OAuthResource); resource != "" {
		prm, err := mcpOAuthDiscoverResource(ctx, resource)
		if err != nil {
			return nil, fmt.Errorf("discover protected resource metadata: %w", err)
		}
		var lastErr error
		for _, authServer := range prm.AuthorizationServers {
			authServer = strings.TrimSpace(authServer)
			if authServer == "" {
				continue
			}
			meta, err := mcpOAuthDiscoverServer(ctx, authServer)
			if err != nil {
				lastErr = err
				continue
			}
			return meta, nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		// No authorization_servers advertised: fall back to discovering against
		// the resource URL itself.
		return mcpOAuthDiscoverServer(ctx, resource)
	}
	base := strings.TrimSpace(cfg.URL)
	if base == "" {
		return nil, fmt.Errorf("oauth authorize requires a server url or oauth_resource")
	}
	return mcpOAuthDiscoverServer(ctx, base)
}

// mcpOAuthRegisterClient performs best-effort RFC 7591 dynamic client
// registration against the advertised registration_endpoint and returns the
// issued client_id. It is only attempted when no client_id is configured. Any
// failure is returned to the caller, which treats it as non-fatal and surfaces a
// clear "configure a client_id" message.
func mcpOAuthRegisterClient(ctx context.Context, registrationEndpoint string, redirectURI string) (string, error) {
	endpoint := strings.TrimSpace(registrationEndpoint)
	if endpoint == "" {
		return "", fmt.Errorf("authorization server does not advertise a registration_endpoint")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload := map[string]any{
		"client_name":                "kernforge",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := mcpOAuthHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("registration endpoint %s returned %s", endpoint, resp.Status)
	}
	var parsed struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode registration response from %s: %w", endpoint, err)
	}
	clientID := strings.TrimSpace(parsed.ClientID)
	if clientID == "" {
		return "", fmt.Errorf("registration endpoint %s returned no client_id", endpoint)
	}
	return clientID, nil
}

// mcpOAuthInteractiveAuthorize runs the full interactive OAuth 2.0 authorization
// code + PKCE flow for a configured streamable HTTP MCP server and stores the
// resulting token so startMCPHTTPClient attaches the Bearer token on the next
// connection.
//
// Flow:
//  1. discover the authorization/token endpoints (RFC 8414/9728, OIDC fallback);
//  2. generate PKCE and an anti-CSRF state;
//  3. bind a loopback callback server on an ephemeral port to derive redirect_uri;
//  4. open the system browser to the authorize URL (printing it on failure);
//  5. wait (bounded) for the redirect, validate state, exchange the code, store.
//
// This is intentionally OFF the headless startup path: it must only run from an
// explicit, interactive command. The writer is used for user-facing progress.
func mcpOAuthInteractiveAuthorize(ctx context.Context, cfg MCPServerConfig, store mcpOAuthTokenStore, writer io.Writer) error {
	if writer == nil {
		writer = io.Discard
	}
	if store == nil {
		store = defaultMCPOAuthTokenStore
	}
	if store == nil {
		return fmt.Errorf("oauth token store is not configured")
	}
	if !mcpOAuthConfigured(cfg) {
		return fmt.Errorf("server %q does not request oauth (set oauth.client_id or oauth_resource)", cfg.Name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, mcpOAuthAuthorizeTimeout)
	defer cancel()

	fmt.Fprintln(writer, "Discovering OAuth endpoints...")
	meta, err := mcpOAuthResolveServerMetadata(ctx, cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(meta.AuthorizationEndpoint) == "" {
		return fmt.Errorf("authorization server metadata is missing authorization_endpoint")
	}

	pkce, err := newMCPPKCE()
	if err != nil {
		return fmt.Errorf("generate pkce: %w", err)
	}
	state, err := newMCPOAuthState()
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}

	callback, err := newMCPOAuthCallbackServer(state)
	if err != nil {
		return err
	}
	defer callback.Close()

	clientID := ""
	if cfg.OAuth != nil {
		clientID = strings.TrimSpace(cfg.OAuth.ClientID)
	}
	if clientID == "" {
		// Best-effort RFC 7591 dynamic client registration. If it fails, instruct
		// the user to configure a client_id rather than failing opaquely.
		registered, regErr := mcpOAuthRegisterClient(ctx, meta.RegistrationEndpoint, callback.redirectURI)
		if regErr != nil {
			return fmt.Errorf("no oauth.client_id configured and dynamic client registration failed (%v); set oauth.client_id for server %q", regErr, cfg.Name)
		}
		clientID = registered
		fmt.Fprintln(writer, "Registered a dynamic client_id via RFC 7591.")
	}

	scope := mcpOAuthScopeForConfig(meta)
	authorizeURL, err := mcpOAuthAuthorizeURL(meta, clientID, callback.redirectURI, scope, state, pkce)
	if err != nil {
		return err
	}

	if err := mcpOAuthOpenURL(authorizeURL); err != nil {
		fmt.Fprintln(writer, "Could not open a browser automatically. Open this URL to authorize:")
		fmt.Fprintln(writer, "  "+authorizeURL)
	} else {
		fmt.Fprintln(writer, "Opened your browser to authorize. If it did not open, use this URL:")
		fmt.Fprintln(writer, "  "+authorizeURL)
	}
	fmt.Fprintln(writer, "Waiting for the authorization callback...")

	result, err := callback.Wait(ctx)
	if err != nil {
		return fmt.Errorf("waiting for authorization callback: %w", err)
	}

	token, err := mcpOAuthExchangeCode(ctx, meta.TokenEndpoint, clientID, result.code, callback.redirectURI, pkce.Verifier)
	if err != nil {
		return fmt.Errorf("exchange authorization code: %w", err)
	}
	if !token.valid() {
		return fmt.Errorf("token endpoint returned no usable access token")
	}
	// Persist under the same key startMCPHTTPClient uses so the token is attached
	// on the next connection. When DCR minted the client_id, persist that id in
	// the server key derivation by carrying it on the token; mcpOAuthServerKey
	// derives from cfg.OAuth.ClientID, so for the DCR case we save under the key
	// that matches the running config (empty client_id).
	if strings.TrimSpace(token.ClientID) == "" {
		token.ClientID = clientID
	}
	if err := store.Save(mcpOAuthServerKey(cfg), token); err != nil {
		return fmt.Errorf("store oauth token: %w", err)
	}
	fmt.Fprintln(writer, "Authorization complete; OAuth token stored.")
	return nil
}
