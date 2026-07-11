package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	decisionDashboardMaxJSONBody               = 64 << 10
	decisionDashboardSnapshotTTL               = 15 * time.Minute
	decisionDashboardSnapshotJanitorInterval   = time.Minute
	decisionDashboardMaxSnapshots              = 4
	decisionDashboardMaxSnapshotRecords        = 100000
	decisionDashboardMaxSnapshotEstimatedBytes = 64 << 20
	decisionDashboardSnapshotBaseBytes         = 1024
	decisionDashboardSnapshotRecordOverhead    = 256
	decisionDashboardSnapshotReadIssueOverhead = 512
)

var decisionDashboardOpenURL = OpenExternalURL

var (
	errDecisionDashboardSnapshotTooLarge = errors.New("decision dashboard snapshot exceeds the cache budget")
	errDecisionDashboardNoChanges        = errors.New("decision dashboard patch does not change the record")
)

type DecisionDashboardServer struct {
	mu                         sync.Mutex
	profileRebuildMu           sync.Mutex
	profileRebuildRun          bool
	profileRebuildReq          bool
	profileStatusGen           uint64
	profileRebuildCtx          context.Context
	profileRebuildCancel       context.CancelFunc
	profileRebuildWG           sync.WaitGroup
	closing                    bool
	decisions                  *ImplementationDecisionStore
	profiles                   *ImplementationPreferenceProfileStore
	workspace                  string
	listener                   net.Listener
	server                     *http.Server
	host                       string
	origin                     string
	cookieName                 string
	sessionToken               string
	requestToken               string
	csrfToken                  string
	bootstrapToken             string
	profileDirty               bool
	profileError               string
	snapshots                  map[string]decisionDashboardSnapshot
	snapshotRecords            int
	snapshotBytes              int64
	snapshotLimits             decisionDashboardSnapshotLimits
	janitorCancel              context.CancelFunc
	janitorDone                chan struct{}
	done                       chan struct{}
	closeOnce                  sync.Once
	bootstrapAfterDecisionRead func()
}

type decisionDashboardDecisionPatch struct {
	ExpectedRevision   int                                `json:"expected_revision"`
	SelectedOptionID   *string                            `json:"selected_option_id,omitempty"`
	CustomSelection    *string                            `json:"custom_selection,omitempty"`
	SelectionReasonRaw *string                            `json:"selection_reason_raw,omitempty"`
	RejectedReasons    *[]ImplementationDecisionRejection `json:"rejected_reasons,omitempty"`
	Tags               *[]string                          `json:"tags,omitempty"`
	Domains            *[]string                          `json:"domains,omitempty"`
	DecisionKind       *string                            `json:"decision_kind,omitempty"`
	RiskLevel          *string                            `json:"risk_level,omitempty"`
	ProjectAlias       *string                            `json:"project_alias,omitempty"`
}

type decisionDashboardRevisionRequest struct {
	ExpectedRevision int `json:"expected_revision"`
}

type decisionDashboardProfilePatch struct {
	ExpectedRevision int     `json:"expected_revision"`
	Enabled          *bool   `json:"enabled,omitempty"`
	Pinned           *bool   `json:"pinned,omitempty"`
	UserNote         *string `json:"user_note,omitempty"`
}

type decisionDashboardExportRequest struct {
	ProjectID              string `json:"project_id,omitempty"`
	Domain                 string `json:"domain,omitempty"`
	DecisionKind           string `json:"decision_kind,omitempty"`
	Status                 string `json:"status,omitempty"`
	Query                  string `json:"query,omitempty"`
	IncludeDeleted         bool   `json:"include_deleted,omitempty"`
	IncludePrivateMetadata bool   `json:"include_private_metadata,omitempty"`
}

type decisionDashboardCursor struct {
	SnapshotID        string `json:"snapshot_id"`
	Offset            int    `json:"offset"`
	FilterFingerprint string `json:"filter_fingerprint"`
}

type decisionDashboardSnapshot struct {
	RecordIDs         []string
	Issues            []ImplementationDecisionReadIssue
	FilterFingerprint string
	AccessedAt        time.Time
	EstimatedBytes    int64
}

type decisionDashboardSnapshotLimits struct {
	MaxSnapshots    int
	MaxRecords      int
	MaxBytes        int64
	TTL             time.Duration
	JanitorInterval time.Duration
}

func NewDecisionDashboardServer(decisions *ImplementationDecisionStore, profiles *ImplementationPreferenceProfileStore, workspace string) *DecisionDashboardServer {
	return &DecisionDashboardServer{
		decisions: decisions,
		profiles:  profiles,
		workspace: strings.TrimSpace(workspace),
		snapshots: map[string]decisionDashboardSnapshot{},
		snapshotLimits: decisionDashboardSnapshotLimits{
			MaxSnapshots:    decisionDashboardMaxSnapshots,
			MaxRecords:      decisionDashboardMaxSnapshotRecords,
			MaxBytes:        decisionDashboardMaxSnapshotEstimatedBytes,
			TTL:             decisionDashboardSnapshotTTL,
			JanitorInterval: decisionDashboardSnapshotJanitorInterval,
		},
		done: make(chan struct{}),
	}
}

func (s *DecisionDashboardServer) Start() error {
	if s == nil {
		return fmt.Errorf("decision dashboard server is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return fmt.Errorf("decision dashboard server is closed")
	}
	if s.server != nil {
		return nil
	}
	if s.decisions == nil || s.profiles == nil {
		return fmt.Errorf("decision dashboard stores are not configured")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	sessionToken, err := decisionDashboardRandomToken(32)
	if err != nil {
		_ = listener.Close()
		return err
	}
	csrfToken, err := decisionDashboardRandomToken(32)
	if err != nil {
		_ = listener.Close()
		return err
	}
	requestToken, err := decisionDashboardRandomToken(32)
	if err != nil {
		_ = listener.Close()
		return err
	}
	instanceID, err := decisionDashboardRandomToken(8)
	if err != nil {
		_ = listener.Close()
		return err
	}
	s.listener = listener
	s.host = listener.Addr().String()
	s.origin = "http://" + s.host
	s.cookieName = "kf_decision_" + instanceID
	s.sessionToken = sessionToken
	s.requestToken = requestToken
	s.csrfToken = csrfToken
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/exchange", s.handleAuthExchange)
	mux.HandleFunc("/api/", s.handleAPI)
	mux.HandleFunc("/", s.handleAsset)
	s.server = &http.Server{
		Handler:           s.securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	janitorContext, janitorCancel := context.WithCancel(context.Background())
	profileRebuildContext, profileRebuildCancel := context.WithCancel(context.Background())
	janitorDone := make(chan struct{})
	s.janitorCancel = janitorCancel
	s.janitorDone = janitorDone
	s.profileRebuildCtx = profileRebuildContext
	s.profileRebuildCancel = profileRebuildCancel
	janitorInterval := s.snapshotLimits.JanitorInterval
	if janitorInterval <= 0 {
		janitorInterval = decisionDashboardSnapshotJanitorInterval
	}
	server := s.server
	go s.runDecisionSnapshotJanitor(janitorContext, janitorDone, janitorInterval)
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The command surface reports startup failures synchronously. Runtime
			// serve failures are observed through Done and a failed browser request.
		}
		janitorCancel()
		profileRebuildCancel()
		close(s.done)
	}()
	return nil
}

func (s *DecisionDashboardServer) SetWorkspace(workspace string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.workspace = strings.TrimSpace(workspace)
	s.mu.Unlock()
}

func (s *DecisionDashboardServer) Origin() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.origin
}

func (s *DecisionDashboardServer) LaunchURL() (string, error) {
	if s == nil {
		return "", fmt.Errorf("decision dashboard server is nil")
	}
	if err := s.Start(); err != nil {
		return "", err
	}
	token, err := decisionDashboardRandomToken(32)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.bootstrapToken = token
	origin := s.origin
	s.mu.Unlock()
	return origin + "/#bootstrap=" + token, nil
}

func (s *DecisionDashboardServer) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		server := s.server
		janitorCancel := s.janitorCancel
		janitorDone := s.janitorDone
		profileRebuildCancel := s.profileRebuildCancel
		s.mu.Unlock()
		if janitorCancel != nil {
			janitorCancel()
		}
		if profileRebuildCancel != nil {
			profileRebuildCancel()
		}
		if server != nil {
			closeErr = server.Shutdown(ctx)
		}
		if janitorDone != nil {
			select {
			case <-janitorDone:
			case <-ctx.Done():
				if closeErr == nil {
					closeErr = ctx.Err()
				}
			}
		}
		profileRebuildDone := make(chan struct{})
		go func() {
			s.profileRebuildWG.Wait()
			close(profileRebuildDone)
		}()
		select {
		case <-profileRebuildDone:
		case <-ctx.Done():
			if closeErr == nil {
				closeErr = ctx.Err()
			}
		}
	})
	return closeErr
}

func (s *DecisionDashboardServer) Done() <-chan struct{} {
	if s == nil || s.done == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return s.done
}

func (s *DecisionDashboardServer) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		host := s.host
		s.mu.Unlock()
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.Host != host {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
			http.Error(w, "cross-site request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *DecisionDashboardServer) handleAuthExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.validMutationOrigin(r) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	var request struct {
		Token string `json:"token"`
	}
	if err := decisionDashboardDecodeJSON(w, r, 4096, &request); err != nil {
		decisionDashboardWriteError(w, err, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	valid := s.bootstrapToken != "" && decisionDashboardSecureEqual(s.bootstrapToken, strings.TrimSpace(request.Token))
	if valid {
		s.bootstrapToken = ""
	}
	cookieName := s.cookieName
	sessionToken := s.sessionToken
	requestToken := s.requestToken
	csrfToken := s.csrfToken
	s.mu.Unlock()
	if !valid {
		http.Error(w, "invalid or expired bootstrap token", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sessionToken,
		Path:     "/api/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	decisionDashboardWriteJSON(w, http.StatusOK, map[string]any{
		"csrf_token":    csrfToken,
		"request_token": requestToken,
	})
}

func (s *DecisionDashboardServer) handleAPI(w http.ResponseWriter, r *http.Request) {
	mutation := r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete || r.Method == http.MethodPut
	if !s.authorized(r, false) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if mutation && !s.authorized(r, true) {
		http.Error(w, "invalid origin or CSRF token", http.StatusForbidden)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case path == "bootstrap":
		s.handleBootstrap(w, r)
	case path == "decisions":
		s.handleDecisions(w, r)
	case strings.HasPrefix(path, "decisions/"):
		s.handleDecision(w, r, strings.TrimPrefix(path, "decisions/"))
	case path == "profiles":
		s.handleProfiles(w, r)
	case path == "profiles/rebuild":
		s.handleProfileRebuild(w, r)
	case strings.HasPrefix(path, "profiles/"):
		s.handleProfile(w, r, strings.TrimPrefix(path, "profiles/"))
	case path == "export":
		s.handleExport(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *DecisionDashboardServer) authorized(r *http.Request, mutation bool) bool {
	s.mu.Lock()
	cookieName := s.cookieName
	sessionToken := s.sessionToken
	requestToken := s.requestToken
	csrfToken := s.csrfToken
	s.mu.Unlock()
	cookie, err := r.Cookie(cookieName)
	if err != nil || !decisionDashboardSecureEqual(cookie.Value, sessionToken) {
		return false
	}
	if !decisionDashboardSecureEqual(r.Header.Get("X-KernForge-Session"), requestToken) {
		return false
	}
	if !mutation {
		return true
	}
	return s.validMutationOrigin(r) && decisionDashboardSecureEqual(r.Header.Get("X-CSRF-Token"), csrfToken)
}

func (s *DecisionDashboardServer) validMutationOrigin(r *http.Request) bool {
	s.mu.Lock()
	origin := s.origin
	s.mu.Unlock()
	return strings.TrimSpace(r.Header.Get("Origin")) == origin
}

func (s *DecisionDashboardServer) handleAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	asset := ""
	contentType := ""
	switch r.URL.Path {
	case "/", "/index.html":
		asset = "decision_dashboard_assets/index.html"
		contentType = "text/html; charset=utf-8"
	case "/app.css":
		asset = "decision_dashboard_assets/app.css"
		contentType = "text/css; charset=utf-8"
	case "/app.js":
		asset = "decision_dashboard_assets/app.js"
		contentType = "text/javascript; charset=utf-8"
	case "/favicon.ico":
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		http.NotFound(w, r)
		return
	}
	data, err := decisionDashboardAssets.ReadFile(asset)
	if err != nil {
		http.Error(w, "asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

func (s *DecisionDashboardServer) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	profileStatusGeneration := s.profileStatusGen
	afterDecisionRead := s.bootstrapAfterDecisionRead
	s.mu.Unlock()
	records, issues, err := s.decisions.ListWithIssues(ImplementationDecisionFilter{IncludeDeleted: true})
	if err != nil {
		decisionDashboardWriteStoreError(w, err)
		return
	}
	if afterDecisionRead != nil {
		afterDecisionRead()
	}
	profile, profileErr := s.profiles.Load()
	projects := map[string]string{}
	domainSet := map[string]bool{}
	kindSet := map[string]bool{}
	activeCount := 0
	activeRecords := make([]ImplementationDecisionRecord, 0, len(records))
	for _, record := range records {
		if record.ProjectID != "" {
			if _, exists := projects[record.ProjectID]; !exists {
				// ListWithIssues returns newest records first. Keep the first alias
				// so an older decision cannot overwrite a recently edited alias.
				projects[record.ProjectID] = valueOrDefault(record.ProjectAlias, record.ProjectID)
			}
		}
		for _, domain := range record.Domains {
			domainSet[domain] = true
		}
		if record.DecisionKind != "" {
			kindSet[record.DecisionKind] = true
		}
		if record.Status != implementationDecisionStatusDeleted {
			activeCount++
			activeRecords = append(activeRecords, record)
		}
	}
	type projectItem struct {
		ID    string `json:"id"`
		Alias string `json:"alias"`
	}
	projectItems := make([]projectItem, 0, len(projects))
	for id, alias := range projects {
		projectItems = append(projectItems, projectItem{ID: id, Alias: alias})
	}
	sort.Slice(projectItems, func(i, j int) bool {
		leftAlias := strings.ToLower(projectItems[i].Alias)
		rightAlias := strings.ToLower(projectItems[j].Alias)
		if leftAlias != rightAlias {
			return leftAlias < rightAlias
		}
		return projectItems[i].ID < projectItems[j].ID
	})
	issueItems := make([]map[string]string, 0, len(issues))
	for _, issue := range issues {
		issueItems = append(issueItems, map[string]string{"file": filepath.Base(issue.Path), "error": issue.Error})
	}
	s.mu.Lock()
	workspace := s.workspace
	csrfToken := s.csrfToken
	profileDirty := s.profileDirty
	profileStatusError := s.profileError
	profileRebuilding := s.profileRebuildRun
	profileStatusStable := profileStatusGeneration == s.profileStatusGen
	s.mu.Unlock()
	if profileErr != nil {
		profileDirty = true
		profileStatusError = profileErr.Error()
	}
	currentSourceHash := implementationPreferenceSourceHash(activeRecords)
	if profileErr == nil {
		profileMatchesJournal := (profile.Revision == 0 && len(activeRecords) == 0) ||
			(profile.Revision > 0 && profile.SourceHash == currentSourceHash)
		if profileMatchesJournal && len(issues) == 0 && profileStatusStable {
			// Canonical persisted state wins over an earlier in-memory rebuild error.
			// Another dashboard or process may have repaired the profile since then.
			profileDirty = false
			profileStatusError = ""
		} else if (profile.Revision > 0 || len(activeRecords) > 0) && profile.SourceHash != currentSourceHash {
			profileDirty = true
			if profileStatusError == "" {
				profileStatusError = "The decision journal changed after the preference profile was generated."
			}
		}
	}
	projectID, projectAlias, _ := implementationDecisionProjectIdentity(workspace)
	payload := map[string]any{
		"csrf_token": csrfToken,
		"workspace": map[string]string{
			"path":          workspace,
			"project_id":    projectID,
			"project_alias": projectAlias,
		},
		"counts": map[string]int{
			"active":        activeCount,
			"deleted":       len(records) - activeCount,
			"profile_rules": len(profile.Rules),
			"issues":        len(issues),
		},
		"projects": projectItems,
		"domains":  decisionDashboardSortedKeys(domainSet),
		"kinds":    decisionDashboardSortedKeys(kindSet),
		"issues":   issueItems,
		"profile": map[string]any{
			"revision":   profile.Revision,
			"dirty":      profileDirty,
			"error":      profileStatusError,
			"rebuilding": profileRebuilding,
		},
	}
	decisionDashboardWriteJSON(w, http.StatusOK, payload)
}

func (s *DecisionDashboardServer) handleDecisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	filter := ImplementationDecisionFilter{
		ProjectID:      strings.TrimSpace(r.URL.Query().Get("project_id")),
		Domain:         strings.TrimSpace(r.URL.Query().Get("domain")),
		DecisionKind:   strings.TrimSpace(r.URL.Query().Get("decision_kind")),
		Status:         strings.TrimSpace(r.URL.Query().Get("status")),
		Query:          strings.TrimSpace(r.URL.Query().Get("q")),
		IncludeDeleted: decisionDashboardParseBool(r.URL.Query().Get("include_deleted")),
	}
	if len(filter.Query) > 512 {
		http.Error(w, "query is too long", http.StatusBadRequest)
		return
	}
	filterFingerprint := decisionDashboardFilterFingerprint(filter)
	if rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor")); rawCursor != "" {
		s.handleDecisionSnapshotPage(w, r, rawCursor, limit, filterFingerprint)
		return
	}
	records, issues, err := s.decisions.ListWithIssues(filter)
	if err != nil {
		decisionDashboardWriteStoreError(w, err)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	total := len(records)
	nextCursor := ""
	if len(records) > limit {
		if r.Context().Err() != nil {
			return
		}
		snapshotID, snapshotErr := s.createDecisionSnapshot(records, issues, filterFingerprint)
		if snapshotErr != nil {
			decisionDashboardWriteStoreError(w, snapshotErr)
			return
		}
		if r.Context().Err() != nil {
			s.deleteDecisionSnapshot(snapshotID)
			return
		}
		records = append([]ImplementationDecisionRecord(nil), records[:limit]...)
		nextCursor = decisionDashboardEncodeCursor(snapshotID, limit, filterFingerprint)
	}
	decisionDashboardWriteDecisionPage(w, records, issues, total, nextCursor)
}

func (s *DecisionDashboardServer) handleDecisionSnapshotPage(w http.ResponseWriter, r *http.Request, rawCursor string, limit int, filterFingerprint string) {
	if r.Context().Err() != nil {
		return
	}
	cursor, err := decisionDashboardDecodeCursor(rawCursor)
	if err != nil {
		decisionDashboardWriteError(w, err, http.StatusBadRequest)
		return
	}
	recordIDs, issues, total, nextOffset, ok := s.readDecisionSnapshotPage(cursor.SnapshotID, cursor.FilterFingerprint, filterFingerprint, cursor.Offset, limit)
	if !ok {
		decisionDashboardWriteError(w, fmt.Errorf("decision cursor is invalid or expired"), http.StatusBadRequest)
		return
	}
	records := make([]ImplementationDecisionRecord, 0, len(recordIDs))
	for _, recordID := range recordIDs {
		if r.Context().Err() != nil {
			return
		}
		record, found, readErr := s.decisions.Get(recordID)
		if readErr != nil {
			decisionDashboardWriteStoreError(w, readErr)
			return
		}
		if !found {
			decisionDashboardWriteError(w, fmt.Errorf("decision snapshot record %s is no longer available; refresh the journal", recordID), http.StatusConflict)
			return
		}
		records = append(records, record)
	}
	if r.Context().Err() != nil {
		return
	}
	nextCursor := ""
	if nextOffset > 0 {
		nextCursor = decisionDashboardEncodeCursor(cursor.SnapshotID, nextOffset, cursor.FilterFingerprint)
	} else {
		s.deleteDecisionSnapshot(cursor.SnapshotID)
	}
	decisionDashboardWriteDecisionPage(w, records, issues, total, nextCursor)
}

func decisionDashboardWriteDecisionPage(w http.ResponseWriter, records []ImplementationDecisionRecord, issues []ImplementationDecisionReadIssue, total int, nextCursor string) {
	issueItems := make([]map[string]string, 0, len(issues))
	for _, issue := range issues {
		issueItems = append(issueItems, map[string]string{"file": filepath.Base(issue.Path), "error": issue.Error})
	}
	decisionDashboardWriteJSON(w, http.StatusOK, map[string]any{
		"records":     records,
		"issues":      issueItems,
		"total":       total,
		"next_cursor": nextCursor,
		"truncated":   nextCursor != "",
	})
}

func (s *DecisionDashboardServer) handleDecision(w http.ResponseWriter, r *http.Request, tail string) {
	tail = strings.Trim(tail, "/")
	if strings.HasSuffix(tail, "/history") {
		id := strings.TrimSuffix(tail, "/history")
		s.handleDecisionHistory(w, r, id)
		return
	}
	if strings.HasSuffix(tail, "/restore") {
		id := strings.TrimSuffix(tail, "/restore")
		s.handleDecisionRestore(w, r, id)
		return
	}
	id := tail
	if !validImplementationDecisionID(id) {
		http.Error(w, "invalid decision id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		record, ok, err := s.decisions.Get(id)
		if err != nil {
			decisionDashboardWriteStoreError(w, err)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		decisionDashboardWriteJSON(w, http.StatusOK, record)
	case http.MethodPatch:
		var request decisionDashboardDecisionPatch
		if err := decisionDashboardDecodeJSON(w, r, decisionDashboardMaxJSONBody, &request); err != nil {
			decisionDashboardWriteError(w, err, http.StatusBadRequest)
			return
		}
		if err := validateDecisionDashboardPatch(request); err != nil {
			decisionDashboardWriteError(w, err, http.StatusBadRequest)
			return
		}
		updated, err := s.decisions.Revise(id, request.ExpectedRevision, func(record *ImplementationDecisionRecord) error {
			return applyDecisionDashboardPatch(record, request)
		})
		if err != nil {
			decisionDashboardWriteStoreError(w, err)
			return
		}
		s.scheduleProfileRebuild()
		decisionDashboardWriteJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		var request decisionDashboardRevisionRequest
		if err := decisionDashboardDecodeJSON(w, r, 4096, &request); err != nil {
			decisionDashboardWriteError(w, err, http.StatusBadRequest)
			return
		}
		updated, err := s.decisions.SoftDelete(id, request.ExpectedRevision)
		if err != nil {
			decisionDashboardWriteStoreError(w, err)
			return
		}
		s.scheduleProfileRebuild()
		decisionDashboardWriteJSON(w, http.StatusOK, updated)
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *DecisionDashboardServer) handleDecisionHistory(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validImplementationDecisionID(id) {
		http.Error(w, "invalid decision id", http.StatusBadRequest)
		return
	}
	records, err := s.decisions.History(id)
	if err != nil {
		decisionDashboardWriteStoreError(w, err)
		return
	}
	decisionDashboardWriteJSON(w, http.StatusOK, map[string]any{"records": records})
}

func (s *DecisionDashboardServer) handleDecisionRestore(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !validImplementationDecisionID(id) {
		http.Error(w, "invalid decision id", http.StatusBadRequest)
		return
	}
	var request decisionDashboardRevisionRequest
	if err := decisionDashboardDecodeJSON(w, r, 4096, &request); err != nil {
		decisionDashboardWriteError(w, err, http.StatusBadRequest)
		return
	}
	updated, err := s.decisions.Revise(id, request.ExpectedRevision, func(record *ImplementationDecisionRecord) error {
		if record.Status != implementationDecisionStatusDeleted {
			return fmt.Errorf("%w: decision %s is not deleted", ErrImplementationDecisionConflict, id)
		}
		record.Status = implementationDecisionStatusCorrected
		record.DeletedAt = nil
		return nil
	})
	if err != nil {
		decisionDashboardWriteStoreError(w, err)
		return
	}
	s.scheduleProfileRebuild()
	decisionDashboardWriteJSON(w, http.StatusOK, updated)
}

func (s *DecisionDashboardServer) scheduleProfileRebuild() {
	if s == nil || s.profiles == nil || s.decisions == nil {
		return
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.profileDirty = true
	s.profileStatusGen++
	if s.profileRebuildRun {
		s.profileRebuildReq = true
		s.mu.Unlock()
		return
	}
	s.profileRebuildRun = true
	s.profileRebuildReq = false
	rebuildContext := s.profileRebuildCtx
	if rebuildContext == nil {
		rebuildContext = context.Background()
	}
	s.profileRebuildWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.profileRebuildWG.Done()
		s.runScheduledProfileRebuilds(rebuildContext)
	}()
}

func (s *DecisionDashboardServer) runScheduledProfileRebuilds(ctx context.Context) {
	for {
		_, _ = s.rebuildProfileContext(ctx)
		s.mu.Lock()
		if s.closing || ctx.Err() != nil {
			s.profileRebuildRun = false
			s.profileRebuildReq = false
			s.mu.Unlock()
			return
		}
		if s.profileRebuildReq {
			s.profileRebuildReq = false
			s.mu.Unlock()
			continue
		}
		s.profileRebuildRun = false
		s.mu.Unlock()
		return
	}
}

func (s *DecisionDashboardServer) rebuildProfile() (ImplementationPreferenceProfile, error) {
	return s.rebuildProfileContext(context.Background())
}

func (s *DecisionDashboardServer) rebuildProfileContext(ctx context.Context) (ImplementationPreferenceProfile, error) {
	if s == nil || s.profiles == nil || s.decisions == nil {
		return ImplementationPreferenceProfile{}, fmt.Errorf("decision dashboard profile stores are not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The profile store serializes file writes, but the dashboard status update
	// happens after that file lock is released. Keep the rebuild and its status
	// publication in one order so an older failure cannot overwrite a newer
	// successful rebuild (or vice versa).
	unlock, err := lockDecisionDashboardMutexContext(ctx, &s.profileRebuildMu)
	if err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	defer unlock()
	profile, err := s.profiles.RebuildContext(ctx, s.decisions)
	s.mu.Lock()
	if !s.closing {
		s.profileDirty = err != nil
		s.profileError = decisionDashboardErrorText(err)
		s.profileStatusGen++
	}
	s.mu.Unlock()
	return profile, err
}

func lockDecisionDashboardMutexContext(ctx context.Context, mutex *sync.Mutex) (func(), error) {
	if mutex == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if mutex.TryLock() {
			return mutex.Unlock, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func validateDecisionDashboardPatch(request decisionDashboardDecisionPatch) error {
	if request.ExpectedRevision <= 0 {
		return fmt.Errorf("expected_revision must be positive")
	}
	if request.SelectedOptionID == nil &&
		request.CustomSelection == nil &&
		request.SelectionReasonRaw == nil &&
		request.RejectedReasons == nil &&
		request.Tags == nil &&
		request.Domains == nil &&
		request.DecisionKind == nil &&
		request.RiskLevel == nil &&
		request.ProjectAlias == nil {
		return fmt.Errorf("decision patch requires at least one changed field")
	}
	if request.SelectedOptionID != nil && request.CustomSelection != nil &&
		strings.TrimSpace(*request.SelectedOptionID) != "" && strings.TrimSpace(*request.CustomSelection) != "" {
		return fmt.Errorf("decision patch cannot select a listed and custom option together")
	}
	if request.DecisionKind != nil {
		kind := strings.TrimSpace(*request.DecisionKind)
		if kind == "" || len(kind) > 128 {
			return fmt.Errorf("decision_kind must contain 1 to 128 bytes")
		}
	}
	if request.RiskLevel != nil {
		switch strings.ToLower(strings.TrimSpace(*request.RiskLevel)) {
		case "", "low", "medium", "high", "critical":
		default:
			return fmt.Errorf("risk_level must be empty, low, medium, high, or critical")
		}
	}
	return nil
}

func applyDecisionDashboardPatch(record *ImplementationDecisionRecord, request decisionDashboardDecisionPatch) error {
	if record == nil {
		return fmt.Errorf("decision record is required")
	}
	changed := false
	summaryInputsChanged := false
	if request.SelectedOptionID != nil {
		selected := strings.TrimSpace(*request.SelectedOptionID)
		if selected != record.SelectedOptionID {
			record.SelectedOptionID = selected
			if selected != "" {
				record.CustomSelection = ""
			}
			record.Provenance.Selection = "user"
			changed = true
			summaryInputsChanged = true
		}
	}
	if request.CustomSelection != nil {
		custom := strings.TrimSpace(*request.CustomSelection)
		if custom != record.CustomSelection {
			record.CustomSelection = custom
			if custom != "" {
				record.SelectedOptionID = ""
			}
			record.Provenance.Selection = "user"
			changed = true
			summaryInputsChanged = true
		}
	}
	if request.SelectionReasonRaw != nil {
		reason := strings.TrimSpace(*request.SelectionReasonRaw)
		if reason != record.SelectionReasonRaw {
			record.SelectionReasonRaw = reason
			record.Provenance.Rationale = "user"
			changed = true
			summaryInputsChanged = true
		}
	}
	if request.RejectedReasons != nil {
		if !decisionDashboardRejectionsEqual(record.RejectedReasons, *request.RejectedReasons) {
			record.RejectedReasons = append([]ImplementationDecisionRejection(nil), (*request.RejectedReasons)...)
			for index := range record.RejectedReasons {
				record.RejectedReasons[index].OptionID = strings.TrimSpace(record.RejectedReasons[index].OptionID)
				record.RejectedReasons[index].Reason = strings.TrimSpace(record.RejectedReasons[index].Reason)
				record.RejectedReasons[index].Source = "user"
			}
			record.Provenance.Rationale = "user"
			changed = true
			summaryInputsChanged = true
		}
	}
	if request.Tags != nil {
		tags := decisionDashboardNormalizedList(*request.Tags)
		if !decisionDashboardStringListsEqual(record.Tags, tags) {
			record.Tags = tags
			changed = true
		}
	}
	if request.Domains != nil {
		domains := decisionDashboardNormalizedList(*request.Domains)
		if !decisionDashboardStringListsEqual(record.Domains, domains) {
			record.Domains = domains
			changed = true
		}
	}
	if request.DecisionKind != nil {
		kind := strings.ToLower(strings.TrimSpace(*request.DecisionKind))
		if kind != record.DecisionKind {
			record.DecisionKind = kind
			changed = true
		}
	}
	if request.RiskLevel != nil {
		risk := strings.ToLower(strings.TrimSpace(*request.RiskLevel))
		if risk != record.RiskLevel {
			record.RiskLevel = risk
			changed = true
		}
	}
	if request.ProjectAlias != nil {
		alias := strings.TrimSpace(*request.ProjectAlias)
		if alias != record.ProjectAlias {
			record.ProjectAlias = alias
			changed = true
		}
	}
	if !changed {
		return errDecisionDashboardNoChanges
	}
	if summaryInputsChanged {
		// A curated summary survives metadata-only edits. Selection or rationale
		// edits invalidate it and let store normalization rebuild the sentence.
		record.SummarySentence = ""
		record.Provenance.Summary = "runtime"
	}
	return nil
}

func decisionDashboardRejectionsEqual(left []ImplementationDecisionRejection, right []ImplementationDecisionRejection) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index].OptionID) != strings.TrimSpace(right[index].OptionID) ||
			strings.TrimSpace(left[index].Reason) != strings.TrimSpace(right[index].Reason) {
			return false
		}
	}
	return true
}

func decisionDashboardNormalizedList(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		key := strings.ToLower(item)
		if item == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func decisionDashboardStringListsEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index]) != strings.TrimSpace(right[index]) {
			return false
		}
	}
	return true
}

func (s *DecisionDashboardServer) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.TrimSpace(r.URL.Query().Get("action")) == "rebuild" {
		profile, err := s.rebuildProfile()
		if err != nil {
			decisionDashboardWriteStoreError(w, err)
			return
		}
		decisionDashboardWriteJSON(w, http.StatusOK, profile)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	profile, err := s.profiles.Load()
	if err != nil {
		decisionDashboardWriteStoreError(w, err)
		return
	}
	decisionDashboardWriteJSON(w, http.StatusOK, profile)
}

func (s *DecisionDashboardServer) handleProfileRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct{}
	if err := decisionDashboardDecodeJSON(w, r, 4096, &request); err != nil {
		decisionDashboardWriteError(w, err, http.StatusBadRequest)
		return
	}
	profile, err := s.rebuildProfile()
	if err != nil {
		decisionDashboardWriteStoreError(w, err)
		return
	}
	decisionDashboardWriteJSON(w, http.StatusOK, profile)
}

func (s *DecisionDashboardServer) handleProfile(w http.ResponseWriter, r *http.Request, ruleID string) {
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", http.MethodPatch)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ruleID = strings.TrimSpace(strings.Trim(ruleID, "/"))
	if ruleID == "" || len(ruleID) > 200 {
		http.Error(w, "invalid profile rule id", http.StatusBadRequest)
		return
	}
	var request decisionDashboardProfilePatch
	if err := decisionDashboardDecodeJSON(w, r, 16<<10, &request); err != nil {
		decisionDashboardWriteError(w, err, http.StatusBadRequest)
		return
	}
	if request.ExpectedRevision <= 0 || (request.Enabled == nil && request.Pinned == nil && request.UserNote == nil) {
		decisionDashboardWriteError(w, fmt.Errorf("profile patch requires a positive expected_revision and at least one changed field"), http.StatusBadRequest)
		return
	}
	profile, err := s.profiles.UpdateRule(ruleID, request.ExpectedRevision, func(rule *ImplementationPreferenceRule) error {
		if request.Enabled != nil {
			rule.Enabled = *request.Enabled
		}
		if request.Pinned != nil {
			rule.Pinned = *request.Pinned
		}
		if request.UserNote != nil {
			rule.UserNote = strings.TrimSpace(*request.UserNote)
		}
		return nil
	})
	if err != nil {
		decisionDashboardWriteStoreError(w, err)
		return
	}
	decisionDashboardWriteJSON(w, http.StatusOK, profile)
}

func (s *DecisionDashboardServer) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request decisionDashboardExportRequest
	if err := decisionDashboardDecodeJSON(w, r, 16<<10, &request); err != nil {
		decisionDashboardWriteError(w, err, http.StatusBadRequest)
		return
	}
	data, err := s.decisions.Export(ImplementationDecisionFilter{
		ProjectID:              strings.TrimSpace(request.ProjectID),
		Domain:                 strings.TrimSpace(request.Domain),
		DecisionKind:           strings.TrimSpace(request.DecisionKind),
		Status:                 strings.TrimSpace(request.Status),
		Query:                  strings.TrimSpace(request.Query),
		IncludeDeleted:         request.IncludeDeleted,
		IncludePrivateMetadata: request.IncludePrivateMetadata,
	})
	if err != nil {
		decisionDashboardWriteStoreError(w, err)
		return
	}
	filename := "kernforge-decisions-" + time.Now().UTC().Format("20060102-150405") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func decisionDashboardDecodeJSON(w http.ResponseWriter, r *http.Request, limit int64, destination any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("Content-Type must be application/json")
	}
	if limit <= 0 {
		limit = decisionDashboardMaxJSONBody
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("request body contains trailing data")
	}
	return nil
}

func decisionDashboardWriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decisionDashboardWriteError(w http.ResponseWriter, err error, status int) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		status = http.StatusRequestEntityTooLarge
	}
	decisionDashboardWriteJSON(w, status, map[string]string{"error": err.Error()})
}

func decisionDashboardWriteStoreError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, errDecisionDashboardSnapshotTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, errDecisionDashboardNoChanges), errors.Is(err, ErrImplementationDecisionInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrImplementationDecisionConflict), errors.Is(err, ErrImplementationPreferenceConflict):
		status = http.StatusConflict
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
	default:
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "invalid") || strings.Contains(lower, "required") || strings.Contains(lower, "exceeds") || strings.Contains(lower, "unsupported") || strings.Contains(lower, "must") {
			status = http.StatusBadRequest
		}
	}
	decisionDashboardWriteError(w, err, status)
}

func (s *DecisionDashboardServer) createDecisionSnapshot(records []ImplementationDecisionRecord, issues []ImplementationDecisionReadIssue, filterFingerprint string) (string, error) {
	snapshotID, err := decisionDashboardRandomToken(16)
	if err != nil {
		return "", err
	}
	recordIDs := make([]string, len(records))
	for index, record := range records {
		recordID := strings.TrimSpace(record.ID)
		if !validImplementationDecisionID(recordID) {
			return "", fmt.Errorf("invalid implementation decision id %q in snapshot", recordID)
		}
		recordIDs[index] = recordID
	}
	now := time.Now().UTC()
	snapshot := decisionDashboardSnapshot{
		RecordIDs:         recordIDs,
		Issues:            append([]ImplementationDecisionReadIssue(nil), issues...),
		FilterFingerprint: strings.TrimSpace(filterFingerprint),
		AccessedAt:        now,
		EstimatedBytes:    decisionDashboardEstimateSnapshotBytes(recordIDs, issues),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	limits := s.normalizedSnapshotLimitsLocked()
	if len(snapshot.RecordIDs) > limits.MaxRecords || snapshot.EstimatedBytes > limits.MaxBytes {
		return "", fmt.Errorf("%w: %d records and %d estimated bytes exceed the per-snapshot limit", errDecisionDashboardSnapshotTooLarge, len(snapshot.RecordIDs), snapshot.EstimatedBytes)
	}
	if s.snapshots == nil {
		s.snapshots = map[string]decisionDashboardSnapshot{}
	}
	s.pruneDecisionSnapshotsLocked(now)
	for len(s.snapshots) >= limits.MaxSnapshots ||
		s.snapshotRecords+len(snapshot.RecordIDs) > limits.MaxRecords ||
		s.snapshotBytes+snapshot.EstimatedBytes > limits.MaxBytes {
		if !s.evictOldestDecisionSnapshotLocked() {
			return "", fmt.Errorf("%w: the cache cannot admit %d records and %d estimated bytes", errDecisionDashboardSnapshotTooLarge, len(snapshot.RecordIDs), snapshot.EstimatedBytes)
		}
	}
	s.snapshots[snapshotID] = snapshot
	s.snapshotRecords += len(snapshot.RecordIDs)
	s.snapshotBytes += snapshot.EstimatedBytes
	return snapshotID, nil
}

func (s *DecisionDashboardServer) readDecisionSnapshotPage(snapshotID string, cursorFilterFingerprint string, requestFilterFingerprint string, offset int, limit int) ([]string, []ImplementationDecisionReadIssue, int, int, bool) {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneDecisionSnapshotsLocked(now)
	snapshot, ok := s.snapshots[snapshotID]
	if !ok ||
		snapshot.FilterFingerprint != strings.TrimSpace(cursorFilterFingerprint) ||
		snapshot.FilterFingerprint != strings.TrimSpace(requestFilterFingerprint) ||
		offset <= 0 || offset >= len(snapshot.RecordIDs) || limit <= 0 {
		return nil, nil, 0, 0, false
	}
	end := offset + limit
	if end > len(snapshot.RecordIDs) {
		end = len(snapshot.RecordIDs)
	}
	recordIDs := append([]string(nil), snapshot.RecordIDs[offset:end]...)
	issues := append([]ImplementationDecisionReadIssue(nil), snapshot.Issues...)
	nextOffset := 0
	if end < len(snapshot.RecordIDs) {
		nextOffset = end
	}
	snapshot.AccessedAt = now
	s.snapshots[snapshotID] = snapshot
	return recordIDs, issues, len(snapshot.RecordIDs), nextOffset, true
}

func (s *DecisionDashboardServer) deleteDecisionSnapshot(snapshotID string) {
	s.mu.Lock()
	s.deleteDecisionSnapshotLocked(strings.TrimSpace(snapshotID))
	s.mu.Unlock()
}

func (s *DecisionDashboardServer) pruneDecisionSnapshotsLocked(now time.Time) {
	limits := s.normalizedSnapshotLimitsLocked()
	for id, snapshot := range s.snapshots {
		if snapshot.AccessedAt.IsZero() || now.Sub(snapshot.AccessedAt) > limits.TTL {
			s.deleteDecisionSnapshotLocked(id)
		}
	}
}

func (s *DecisionDashboardServer) evictOldestDecisionSnapshotLocked() bool {
	oldestID := ""
	var oldest time.Time
	for id, snapshot := range s.snapshots {
		if oldestID == "" || snapshot.AccessedAt.Before(oldest) {
			oldestID = id
			oldest = snapshot.AccessedAt
		}
	}
	if oldestID != "" {
		return s.deleteDecisionSnapshotLocked(oldestID)
	}
	return false
}

func (s *DecisionDashboardServer) deleteDecisionSnapshotLocked(snapshotID string) bool {
	snapshot, ok := s.snapshots[snapshotID]
	if !ok {
		return false
	}
	delete(s.snapshots, snapshotID)
	s.snapshotRecords -= len(snapshot.RecordIDs)
	s.snapshotBytes -= snapshot.EstimatedBytes
	if s.snapshotRecords < 0 {
		s.snapshotRecords = 0
	}
	if s.snapshotBytes < 0 {
		s.snapshotBytes = 0
	}
	return true
}

func (s *DecisionDashboardServer) normalizedSnapshotLimitsLocked() decisionDashboardSnapshotLimits {
	limits := s.snapshotLimits
	if limits.MaxSnapshots <= 0 {
		limits.MaxSnapshots = decisionDashboardMaxSnapshots
	}
	if limits.MaxRecords <= 0 {
		limits.MaxRecords = decisionDashboardMaxSnapshotRecords
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = decisionDashboardMaxSnapshotEstimatedBytes
	}
	if limits.TTL <= 0 {
		limits.TTL = decisionDashboardSnapshotTTL
	}
	if limits.JanitorInterval <= 0 {
		limits.JanitorInterval = decisionDashboardSnapshotJanitorInterval
	}
	return limits
}

func (s *DecisionDashboardServer) runDecisionSnapshotJanitor(ctx context.Context, done chan<- struct{}, interval time.Duration) {
	defer close(done)
	if interval <= 0 {
		interval = decisionDashboardSnapshotJanitorInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.mu.Lock()
			s.pruneDecisionSnapshotsLocked(now.UTC())
			s.mu.Unlock()
		}
	}
}

func decisionDashboardEstimateSnapshotBytes(recordIDs []string, issues []ImplementationDecisionReadIssue) int64 {
	const maxInt64 = int64(^uint64(0) >> 1)
	total := int64(decisionDashboardSnapshotBaseBytes)
	add := func(value int64) {
		if value <= 0 || total == maxInt64 {
			return
		}
		if value > maxInt64-total {
			total = maxInt64
			return
		}
		total += value
	}
	addString := func(value string) {
		length := int64(len(value))
		add(length)
		add(length)
	}
	for _, recordID := range recordIDs {
		add(decisionDashboardSnapshotRecordOverhead)
		addString(recordID)
	}
	for _, issue := range issues {
		add(decisionDashboardSnapshotReadIssueOverhead)
		addString(issue.Path)
		addString(issue.Error)
	}
	return total
}

func decisionDashboardEncodeCursor(snapshotID string, offset int, filterFingerprint string) string {
	payload, _ := json.Marshal(decisionDashboardCursor{
		SnapshotID:        strings.TrimSpace(snapshotID),
		Offset:            offset,
		FilterFingerprint: strings.TrimSpace(filterFingerprint),
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decisionDashboardDecodeCursor(raw string) (decisionDashboardCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 {
		return decisionDashboardCursor{}, fmt.Errorf("invalid decision cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return decisionDashboardCursor{}, fmt.Errorf("invalid decision cursor")
	}
	var cursor decisionDashboardCursor
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return decisionDashboardCursor{}, fmt.Errorf("invalid decision cursor")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return decisionDashboardCursor{}, fmt.Errorf("invalid decision cursor")
	}
	cursor.SnapshotID = strings.TrimSpace(cursor.SnapshotID)
	cursor.FilterFingerprint = strings.TrimSpace(cursor.FilterFingerprint)
	if len(cursor.SnapshotID) != 32 || cursor.Offset <= 0 || len(cursor.FilterFingerprint) != 32 {
		return decisionDashboardCursor{}, fmt.Errorf("invalid decision cursor")
	}
	if _, err := hex.DecodeString(cursor.SnapshotID); err != nil {
		return decisionDashboardCursor{}, fmt.Errorf("invalid decision cursor")
	}
	if _, err := hex.DecodeString(cursor.FilterFingerprint); err != nil {
		return decisionDashboardCursor{}, fmt.Errorf("invalid decision cursor")
	}
	return cursor, nil
}

func decisionDashboardFilterFingerprint(filter ImplementationDecisionFilter) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(filter.ProjectID)),
		strings.ToLower(strings.TrimSpace(filter.Domain)),
		strings.ToLower(strings.TrimSpace(filter.DecisionKind)),
		strings.ToLower(strings.TrimSpace(filter.Status)),
		strings.ToLower(strings.TrimSpace(filter.Query)),
		strconv.FormatBool(filter.IncludeDeleted),
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:16])
}

func decisionDashboardRandomToken(byteCount int) (string, error) {
	if byteCount <= 0 {
		byteCount = 32
	}
	data := make([]byte, byteCount)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func decisionDashboardSecureEqual(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if len(left) == 0 || len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func decisionDashboardParseBool(value string) bool {
	parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
	return parsed
}

func decisionDashboardSortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func decisionDashboardErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
