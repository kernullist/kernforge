package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type decisionDashboardTestClient struct {
	client *http.Client
	origin string
	csrf   string
	proof  string
	token  string
}

func startDecisionDashboardTestServer(t *testing.T) (*DecisionDashboardServer, *ImplementationDecisionStore, *ImplementationPreferenceProfileStore, decisionDashboardTestClient) {
	t.Helper()
	root := t.TempDir()
	decisions := &ImplementationDecisionStore{Dir: filepath.Join(root, "records")}
	profiles := &ImplementationPreferenceProfileStore{Path: filepath.Join(root, "profiles", "profile.json")}
	server := NewDecisionDashboardServer(decisions, profiles, filepath.Join(root, "workspace"))
	launchURL, err := server.LaunchURL()
	if err != nil {
		t.Fatalf("LaunchURL: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	parsed, err := url.Parse(launchURL)
	if err != nil {
		t.Fatalf("parse launch URL: %v", err)
	}
	token := strings.TrimPrefix(parsed.Fragment, "bootstrap=")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return server, decisions, profiles, decisionDashboardTestClient{
		client: &http.Client{Jar: jar, Timeout: 5 * time.Second},
		origin: server.Origin(),
		token:  token,
	}
}

func (c *decisionDashboardTestClient) exchange(t *testing.T) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": c.token})
	request, err := http.NewRequest(http.MethodPost, c.origin+"/api/auth/exchange", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("auth request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", c.origin)
	response, err := c.client.Do(request)
	if err != nil {
		t.Fatalf("auth exchange: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("auth exchange status=%d body=%s", response.StatusCode, data)
	}
	var payload struct {
		CSRF  string `json:"csrf_token"`
		Proof string `json:"request_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode auth exchange: %v", err)
	}
	c.csrf = payload.CSRF
	c.proof = payload.Proof
	cookies := response.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/api/" {
		t.Fatalf("auth cookie flags=%#v", cookies)
	}
	if c.proof == "" {
		t.Fatal("auth exchange did not return the origin-scoped request proof")
	}
}

func (c decisionDashboardTestClient) request(t *testing.T, method string, path string, body any, includeCSRF bool, origin string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, c.origin+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if includeCSRF {
		request.Header.Set("X-CSRF-Token", c.csrf)
	}
	request.Header.Set("X-KernForge-Session", c.proof)
	response, err := c.client.Do(request)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return response
}

func TestDecisionDashboardAuthExchangeIsOneTimeAndCookieProtected(t *testing.T) {
	_, _, _, authenticated := startDecisionDashboardTestServer(t)
	unauthenticated := &http.Client{Timeout: 5 * time.Second}
	response, err := unauthenticated.Get(authenticated.origin + "/api/bootstrap")
	if err != nil {
		t.Fatalf("unauthenticated GET: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API status=%d", response.StatusCode)
	}
	authenticated.exchange(t)
	request, err := http.NewRequest(http.MethodGet, authenticated.origin+"/api/bootstrap", nil)
	if err != nil {
		t.Fatalf("cookie-only request: %v", err)
	}
	response, err = authenticated.client.Do(request)
	if err != nil {
		t.Fatalf("cookie-only API request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cookie-only API request status=%d", response.StatusCode)
	}
	response = authenticated.request(t, http.MethodGet, "/api/bootstrap", nil, false, "")
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated API status=%d", response.StatusCode)
	}
	response = authenticated.request(t, http.MethodGet, "/", nil, false, "")
	response.Body.Close()
	if !strings.Contains(response.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("root response missing CSP: %q", response.Header.Get("Content-Security-Policy"))
	}
	body, _ := json.Marshal(map[string]string{"token": authenticated.token})
	request, _ = http.NewRequest(http.MethodPost, authenticated.origin+"/api/auth/exchange", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", authenticated.origin)
	response, err = unauthenticated.Do(request)
	if err != nil {
		t.Fatalf("second auth exchange: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bootstrap token was reusable: status=%d", response.StatusCode)
	}
}

func TestDecisionDashboardRejectsInvalidHostOriginAndCSRF(t *testing.T) {
	_, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	record, err := decisions.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	request, _ := http.NewRequest(http.MethodGet, client.origin+"/", nil)
	request.Host = "evil.example"
	response, err := client.client.Do(request)
	if err != nil {
		t.Fatalf("invalid Host request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid Host status=%d", response.StatusCode)
	}
	response = client.request(t, http.MethodDelete, "/api/decisions/"+record.ID, map[string]int{"expected_revision": record.Revision}, false, client.origin)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", response.StatusCode)
	}
	response = client.request(t, http.MethodDelete, "/api/decisions/"+record.ID, map[string]int{"expected_revision": record.Revision}, true, "http://evil.example")
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin mutation status=%d", response.StatusCode)
	}
}

func TestDecisionDashboardRevisionConflictAndStrictJSON(t *testing.T) {
	_, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	record, err := decisions.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	patch := map[string]any{"expected_revision": record.Revision, "selection_reason_raw": "first dashboard update"}
	response := client.request(t, http.MethodPatch, "/api/decisions/"+record.ID, patch, true, client.origin)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("first patch status=%d", response.StatusCode)
	}
	response = client.request(t, http.MethodPatch, "/api/decisions/"+record.ID, patch, true, client.origin)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale patch status=%d", response.StatusCode)
	}
	response = client.request(t, http.MethodPatch, "/api/decisions/"+record.ID, map[string]any{"expected_revision": 2, "unknown": true}, true, client.origin)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown JSON field status=%d", response.StatusCode)
	}
}

func TestDecisionDashboardPaginatesWithoutHidingRecords(t *testing.T) {
	_, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	for index := 0; index < 5; index++ {
		record := testImplementationDecisionRecord()
		record.ID = fmt.Sprintf("decision-page-%d", index)
		record.ProjectID = "project-pagination"
		if _, err := decisions.Put(record); err != nil {
			t.Fatalf("Put %d: %v", index, err)
		}
	}
	type pagePayload struct {
		Records    []ImplementationDecisionRecord `json:"records"`
		Total      int                            `json:"total"`
		NextCursor string                         `json:"next_cursor"`
		Truncated  bool                           `json:"truncated"`
	}
	response := client.request(t, http.MethodGet, "/api/decisions?project_id=project-pagination&limit=2", nil, false, "")
	var first pagePayload
	if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || first.Total != 5 || len(first.Records) != 2 || first.NextCursor == "" || !first.Truncated {
		t.Fatalf("unexpected first page status=%d payload=%#v", response.StatusCode, first)
	}
	seen := map[string]bool{}
	for _, record := range first.Records {
		seen[record.ID] = true
	}
	response = client.request(t, http.MethodGet, "/api/decisions?project_id=project-pagination&limit=2&cursor="+url.QueryEscape(first.NextCursor), nil, false, "")
	var second pagePayload
	if err := json.NewDecoder(response.Body).Decode(&second); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || second.Total != 5 || len(second.Records) != 2 || second.NextCursor == "" {
		t.Fatalf("unexpected second page status=%d payload=%#v", response.StatusCode, second)
	}
	for _, record := range second.Records {
		if seen[record.ID] {
			t.Fatalf("pagination repeated decision %s", record.ID)
		}
		seen[record.ID] = true
	}
	response = client.request(t, http.MethodGet, "/api/decisions?project_id=project-pagination&limit=2&cursor="+url.QueryEscape(second.NextCursor), nil, false, "")
	var third pagePayload
	if err := json.NewDecoder(response.Body).Decode(&third); err != nil {
		t.Fatalf("decode third page: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || third.Total != 5 || len(third.Records) != 1 || third.NextCursor != "" || third.Truncated {
		t.Fatalf("unexpected third page status=%d payload=%#v", response.StatusCode, third)
	}
	for _, record := range third.Records {
		seen[record.ID] = true
	}
	if len(seen) != 5 {
		t.Fatalf("pagination exposed %d of 5 decisions", len(seen))
	}
	response = client.request(t, http.MethodGet, "/api/decisions?cursor=not-a-cursor", nil, false, "")
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid cursor status=%d", response.StatusCode)
	}
}

func TestDecisionDashboardPaginationUsesImmutableSnapshot(t *testing.T) {
	_, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	originalIDs := map[string]bool{}
	for index := 0; index < 4; index++ {
		record := testImplementationDecisionRecord()
		record.ID = fmt.Sprintf("decision-snapshot-%d", index)
		record.ProjectID = "project-snapshot"
		stored, err := decisions.Put(record)
		if err != nil {
			t.Fatalf("Put %d: %v", index, err)
		}
		originalIDs[stored.ID] = true
	}
	type pagePayload struct {
		Records    []ImplementationDecisionRecord `json:"records"`
		Total      int                            `json:"total"`
		NextCursor string                         `json:"next_cursor"`
	}
	response := client.request(t, http.MethodGet, "/api/decisions?project_id=project-snapshot&limit=2", nil, false, "")
	var first pagePayload
	if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || first.Total != 4 || len(first.Records) != 2 || first.NextCursor == "" {
		t.Fatalf("unexpected first page status=%d payload=%#v", response.StatusCode, first)
	}
	seen := map[string]bool{}
	for _, record := range first.Records {
		seen[record.ID] = true
	}
	laterID := ""
	for id := range originalIDs {
		if !seen[id] {
			laterID = id
			break
		}
	}
	later, ok, err := decisions.Get(laterID)
	if err != nil || !ok {
		t.Fatalf("Get later-page decision %q: ok=%v err=%v", laterID, ok, err)
	}
	if _, err := decisions.Revise(later.ID, later.Revision, func(record *ImplementationDecisionRecord) error {
		record.SelectionReasonRaw = "revised after the pagination snapshot"
		return nil
	}); err != nil {
		t.Fatalf("Revise later-page decision: %v", err)
	}
	newRecord := testImplementationDecisionRecord()
	newRecord.ID = "decision-snapshot-new"
	newRecord.ProjectID = "project-snapshot"
	if _, err := decisions.Put(newRecord); err != nil {
		t.Fatalf("Put post-snapshot decision: %v", err)
	}
	response = client.request(t, http.MethodGet, "/api/decisions?project_id=another-project&limit=2&cursor="+url.QueryEscape(first.NextCursor), nil, false, "")
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("cursor accepted a changed filter: status=%d", response.StatusCode)
	}
	response = client.request(t, http.MethodGet, "/api/decisions?project_id=project-snapshot&limit=2&cursor="+url.QueryEscape(first.NextCursor), nil, false, "")
	var second pagePayload
	if err := json.NewDecoder(response.Body).Decode(&second); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || second.Total != 4 || len(second.Records) != 2 || second.NextCursor != "" {
		t.Fatalf("unexpected snapshot page status=%d payload=%#v", response.StatusCode, second)
	}
	for _, record := range second.Records {
		if record.ID == newRecord.ID {
			t.Fatalf("snapshot page included post-snapshot decision %s", record.ID)
		}
		if record.ID == laterID && record.SelectionReasonRaw != "revised after the pagination snapshot" {
			t.Fatalf("snapshot membership was stable but did not load the current revision: %#v", record)
		}
		seen[record.ID] = true
	}
	if !seen[laterID] || len(seen) != len(originalIDs) {
		t.Fatalf("snapshot pagination skipped a revised record: later=%q seen=%#v", laterID, seen)
	}
	response = client.request(t, http.MethodGet, "/api/decisions?project_id=project-snapshot&limit=10", nil, false, "")
	var fresh pagePayload
	if err := json.NewDecoder(response.Body).Decode(&fresh); err != nil {
		t.Fatalf("decode fresh page: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || fresh.Total != 5 {
		t.Fatalf("fresh pagination did not include the new capture: status=%d payload=%#v", response.StatusCode, fresh)
	}
}

func TestDecisionDashboardSnapshotCacheIsBoundedAndExpires(t *testing.T) {
	server := NewDecisionDashboardServer(nil, nil, "")
	filterFingerprint := decisionDashboardFilterFingerprint(ImplementationDecisionFilter{})
	records := []ImplementationDecisionRecord{{ID: "decision-snapshot-cache"}}
	created := make([]string, 0, decisionDashboardMaxSnapshots+1)
	for index := 0; index <= decisionDashboardMaxSnapshots; index++ {
		id, err := server.createDecisionSnapshot(records, nil, filterFingerprint)
		if err != nil {
			t.Fatalf("create snapshot %d: %v", index, err)
		}
		created = append(created, id)
	}
	server.mu.Lock()
	if len(server.snapshots) != decisionDashboardMaxSnapshots {
		server.mu.Unlock()
		t.Fatalf("snapshot cache size=%d, want %d", len(server.snapshots), decisionDashboardMaxSnapshots)
	}
	if server.snapshotRecords != decisionDashboardMaxSnapshots || server.snapshotBytes <= 0 {
		server.mu.Unlock()
		t.Fatalf("snapshot cache accounting is invalid: records=%d bytes=%d", server.snapshotRecords, server.snapshotBytes)
	}
	expiredID := created[len(created)-1]
	expired := server.snapshots[expiredID]
	expired.AccessedAt = time.Now().UTC().Add(-decisionDashboardSnapshotTTL - time.Second)
	server.snapshots[expiredID] = expired
	server.pruneDecisionSnapshotsLocked(time.Now().UTC())
	_, stillPresent := server.snapshots[expiredID]
	recordsAfterPrune := server.snapshotRecords
	bytesAfterPrune := server.snapshotBytes
	server.mu.Unlock()
	if stillPresent {
		t.Fatalf("expired snapshot %s was retained", expiredID)
	}
	if recordsAfterPrune != decisionDashboardMaxSnapshots-1 || bytesAfterPrune <= 0 {
		t.Fatalf("snapshot cache accounting was not reduced after expiry: records=%d bytes=%d", recordsAfterPrune, bytesAfterPrune)
	}
}

func TestDecisionDashboardSnapshotCacheEnforcesRecordAndByteBudgets(t *testing.T) {
	t.Run("record budget evicts least recently used snapshot", func(t *testing.T) {
		server := NewDecisionDashboardServer(nil, nil, "")
		server.snapshotLimits.MaxSnapshots = 4
		server.snapshotLimits.MaxRecords = 3
		server.snapshotLimits.MaxBytes = 1 << 20
		filterFingerprint := decisionDashboardFilterFingerprint(ImplementationDecisionFilter{})
		oldID, err := server.createDecisionSnapshot([]ImplementationDecisionRecord{{ID: "decision-budget-old-1"}, {ID: "decision-budget-old-2"}}, nil, filterFingerprint)
		if err != nil {
			t.Fatalf("create old snapshot: %v", err)
		}
		recentID, err := server.createDecisionSnapshot([]ImplementationDecisionRecord{{ID: "decision-budget-recent"}}, nil, filterFingerprint)
		if err != nil {
			t.Fatalf("create recent snapshot: %v", err)
		}
		server.mu.Lock()
		old := server.snapshots[oldID]
		old.AccessedAt = time.Now().UTC().Add(-time.Minute)
		server.snapshots[oldID] = old
		recent := server.snapshots[recentID]
		recent.AccessedAt = time.Now().UTC()
		server.snapshots[recentID] = recent
		server.mu.Unlock()

		newID, err := server.createDecisionSnapshot([]ImplementationDecisionRecord{{ID: "decision-budget-new-1"}, {ID: "decision-budget-new-2"}}, nil, filterFingerprint)
		if err != nil {
			t.Fatalf("create new snapshot: %v", err)
		}
		server.mu.Lock()
		_, oldPresent := server.snapshots[oldID]
		_, recentPresent := server.snapshots[recentID]
		_, newPresent := server.snapshots[newID]
		recordCount := server.snapshotRecords
		server.mu.Unlock()
		if oldPresent || !recentPresent || !newPresent || recordCount != 3 {
			t.Fatalf("record-budget LRU eviction failed: old=%v recent=%v new=%v records=%d", oldPresent, recentPresent, newPresent, recordCount)
		}
	})

	t.Run("byte budget evicts least recently used snapshot", func(t *testing.T) {
		server := NewDecisionDashboardServer(nil, nil, "")
		filterFingerprint := decisionDashboardFilterFingerprint(ImplementationDecisionFilter{})
		firstRecords := []ImplementationDecisionRecord{{ID: "decision-byte-budget-first"}}
		secondRecords := []ImplementationDecisionRecord{{ID: "decision-byte-budget-second"}}
		firstBytes := decisionDashboardEstimateSnapshotBytes([]string{firstRecords[0].ID}, nil)
		secondBytes := decisionDashboardEstimateSnapshotBytes([]string{secondRecords[0].ID}, nil)
		server.snapshotLimits.MaxSnapshots = 4
		server.snapshotLimits.MaxRecords = 10
		server.snapshotLimits.MaxBytes = firstBytes + secondBytes - 1
		firstID, err := server.createDecisionSnapshot(firstRecords, nil, filterFingerprint)
		if err != nil {
			t.Fatalf("create first byte-budget snapshot: %v", err)
		}
		secondID, err := server.createDecisionSnapshot(secondRecords, nil, filterFingerprint)
		if err != nil {
			t.Fatalf("create second byte-budget snapshot: %v", err)
		}
		server.mu.Lock()
		_, firstPresent := server.snapshots[firstID]
		_, secondPresent := server.snapshots[secondID]
		byteCount := server.snapshotBytes
		server.mu.Unlock()
		if firstPresent || !secondPresent || byteCount != secondBytes {
			t.Fatalf("byte-budget LRU eviction failed: first=%v second=%v bytes=%d want=%d", firstPresent, secondPresent, byteCount, secondBytes)
		}
	})

	t.Run("single oversized snapshot fails closed", func(t *testing.T) {
		filterFingerprint := decisionDashboardFilterFingerprint(ImplementationDecisionFilter{})
		recordServer := NewDecisionDashboardServer(nil, nil, "")
		recordServer.snapshotLimits.MaxRecords = 1
		_, err := recordServer.createDecisionSnapshot([]ImplementationDecisionRecord{{ID: "decision-too-many-1"}, {ID: "decision-too-many-2"}}, nil, filterFingerprint)
		if !errors.Is(err, errDecisionDashboardSnapshotTooLarge) {
			t.Fatalf("record budget error=%v, want snapshot-too-large", err)
		}

		byteServer := NewDecisionDashboardServer(nil, nil, "")
		records := []ImplementationDecisionRecord{{ID: "decision-too-many-bytes"}}
		estimatedBytes := decisionDashboardEstimateSnapshotBytes([]string{records[0].ID}, nil)
		byteServer.snapshotLimits.MaxBytes = estimatedBytes - 1
		_, err = byteServer.createDecisionSnapshot(records, nil, filterFingerprint)
		if !errors.Is(err, errDecisionDashboardSnapshotTooLarge) {
			t.Fatalf("byte budget error=%v, want snapshot-too-large", err)
		}
	})
}

func TestDecisionDashboardOversizedSnapshotReturnsRequestEntityTooLarge(t *testing.T) {
	server, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	server.mu.Lock()
	server.snapshotLimits.MaxRecords = 1
	server.mu.Unlock()
	for index := 0; index < 2; index++ {
		record := testImplementationDecisionRecord()
		record.ID = fmt.Sprintf("decision-http-budget-%d", index)
		if _, err := decisions.Put(record); err != nil {
			t.Fatalf("Put %d: %v", index, err)
		}
	}
	response := client.request(t, http.MethodGet, "/api/decisions?limit=1", nil, false, "")
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized snapshot status=%d, want %d", response.StatusCode, http.StatusRequestEntityTooLarge)
	}
	server.mu.Lock()
	snapshotCount := len(server.snapshots)
	recordCount := server.snapshotRecords
	byteCount := server.snapshotBytes
	server.mu.Unlock()
	if snapshotCount != 0 || recordCount != 0 || byteCount != 0 {
		t.Fatalf("oversized snapshot mutated cache: snapshots=%d records=%d bytes=%d", snapshotCount, recordCount, byteCount)
	}
}

func TestDecisionDashboardSnapshotJanitorExpiresEntriesAndStopsWithServer(t *testing.T) {
	root := t.TempDir()
	server := NewDecisionDashboardServer(
		&ImplementationDecisionStore{Dir: filepath.Join(root, "records")},
		&ImplementationPreferenceProfileStore{Path: filepath.Join(root, "profiles", "profile.json")},
		root,
	)
	server.snapshotLimits.TTL = 20 * time.Millisecond
	server.snapshotLimits.JanitorInterval = 5 * time.Millisecond
	if err := server.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Close(ctx)
	})
	filterFingerprint := decisionDashboardFilterFingerprint(ImplementationDecisionFilter{})
	if _, err := server.createDecisionSnapshot([]ImplementationDecisionRecord{{ID: "decision-janitor-expiry"}}, nil, filterFingerprint); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		server.mu.Lock()
		empty := len(server.snapshots) == 0 && server.snapshotRecords == 0 && server.snapshotBytes == 0
		server.mu.Unlock()
		if empty {
			break
		}
		select {
		case <-deadline:
			t.Fatal("snapshot janitor did not expire the cached entry")
		case <-ticker.C:
		}
	}
	server.mu.Lock()
	janitorDone := server.janitorDone
	server.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := server.Close(ctx); err != nil {
		cancel()
		t.Fatalf("Close: %v", err)
	}
	cancel()
	select {
	case <-janitorDone:
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot janitor did not stop with the dashboard server")
	}
}

func TestDecisionDashboardRestoreRejectsActiveRecord(t *testing.T) {
	_, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	record, err := decisions.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	response := client.request(t, http.MethodPost, "/api/decisions/"+record.ID+"/restore", map[string]int{"expected_revision": record.Revision}, true, client.origin)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("active restore status=%d", response.StatusCode)
	}
	current, ok, err := decisions.Get(record.ID)
	if err != nil || !ok || current.Revision != record.Revision || current.Status == implementationDecisionStatusDeleted {
		t.Fatalf("active restore mutated record: ok=%v err=%v current=%#v", ok, err, current)
	}
}

func TestDecisionDashboardReportsDurablyStaleProfile(t *testing.T) {
	_, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	if _, err := decisions.Put(testImplementationDecisionRecord()); err != nil {
		t.Fatalf("Put: %v", err)
	}
	readDirty := func() bool {
		response := client.request(t, http.MethodGet, "/api/bootstrap", nil, false, "")
		defer response.Body.Close()
		var payload struct {
			Profile struct {
				Dirty bool `json:"dirty"`
			} `json:"profile"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatalf("decode bootstrap: %v", err)
		}
		return payload.Profile.Dirty
	}
	if !readDirty() {
		t.Fatal("missing profile was not reported stale after the journal changed")
	}
	response := client.request(t, http.MethodPost, "/api/profiles/rebuild", map[string]any{}, true, client.origin)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("profile rebuild status=%d", response.StatusCode)
	}
	if readDirty() {
		t.Fatal("freshly rebuilt profile was still reported stale")
	}
}

func TestDecisionDashboardRejectsOversizedJSON(t *testing.T) {
	_, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	record, err := decisions.Put(testImplementationDecisionRecord())
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	data := `{"expected_revision":1,"selection_reason_raw":"` + strings.Repeat("x", decisionDashboardMaxJSONBody) + `"}`
	request, _ := http.NewRequest(http.MethodPatch, client.origin+"/api/decisions/"+record.ID, strings.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", client.origin)
	request.Header.Set("X-CSRF-Token", client.csrf)
	request.Header.Set("X-KernForge-Session", client.proof)
	response, err := client.client.Do(request)
	if err != nil {
		t.Fatalf("oversized request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized JSON status=%d", response.StatusCode)
	}
}

func TestDecisionDashboardExportIsPrivacyReducedByDefault(t *testing.T) {
	_, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	record := testImplementationDecisionRecord()
	record.SessionID = "session-private"
	record.EvidenceRefs = []string{`F:\private\source.go:10`}
	if _, err := decisions.Put(record); err != nil {
		t.Fatalf("Put: %v", err)
	}
	response := client.request(t, http.MethodPost, "/api/export", map[string]any{}, true, client.origin)
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export status=%d body=%s", response.StatusCode, data)
	}
	for _, forbidden := range []string{"session-private", `F:\\kernullist\\kernforge`, `F:\\private\\source.go`} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("privacy-reduced export leaked %q: %s", forbidden, data)
		}
	}
}

func TestDecisionDashboardProfileUpdateUsesRevisionCAS(t *testing.T) {
	_, decisions, _, client := startDecisionDashboardTestServer(t)
	client.exchange(t)
	for _, record := range []ImplementationDecisionRecord{
		preferenceDecision("decision-1", "project-a", "tooling", "per-record-json"),
		preferenceDecision("decision-2", "project-a", "tooling", "per-record-json"),
	} {
		if _, err := decisions.Put(record); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	response := client.request(t, http.MethodPost, "/api/profiles/rebuild", map[string]any{}, true, client.origin)
	var profile ImplementationPreferenceProfile
	if err := json.NewDecoder(response.Body).Decode(&profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || len(profile.Rules) != 1 {
		t.Fatalf("rebuild status=%d profile=%#v", response.StatusCode, profile)
	}
	path := "/api/profiles/" + url.PathEscape(profile.Rules[0].ID)
	response = client.request(t, http.MethodPatch, path, map[string]any{"expected_revision": profile.Revision, "pinned": true}, true, client.origin)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("profile patch status=%d", response.StatusCode)
	}
	response = client.request(t, http.MethodPatch, path, map[string]any{"expected_revision": profile.Revision, "enabled": false}, true, client.origin)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale profile patch status=%d", response.StatusCode)
	}
}

func TestDecisionDashboardAssetsAvoidUserHTMLInjectionSinks(t *testing.T) {
	for _, path := range []string{"decision_dashboard_assets/index.html", "decision_dashboard_assets/app.js", "decision_dashboard_assets/app.css"} {
		data, err := decisionDashboardAssets.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		text := string(data)
		if strings.Contains(text, "innerHTML") || strings.Contains(text, "outerHTML") || strings.Contains(text, "insertAdjacentHTML") {
			t.Fatalf("dashboard asset %s contains an unsafe HTML injection sink", path)
		}
		if strings.Contains(text, "fonts.googleapis.com") || strings.Contains(text, "cdn.") {
			t.Fatalf("dashboard asset %s loads a remote asset", path)
		}
	}
}

func TestDecisionDashboardHistoryRenderGuardsCurrentSelection(t *testing.T) {
	data, err := decisionDashboardAssets.ReadFile("decision_dashboard_assets/app.js")
	if err != nil {
		t.Fatalf("ReadFile app.js: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "async function loadHistory(record)")
	if start < 0 {
		t.Fatal("loadHistory function is missing")
	}
	endOffset := strings.Index(text[start:], "async function deleteDecision(record)")
	if endOffset < 0 {
		t.Fatal("loadHistory function boundary is missing")
	}
	historyFunction := text[start : start+endOffset]
	captureIndex := strings.Index(historyFunction, "const generation = state.selectionRequestGeneration;")
	requestIndex := strings.Index(historyFunction, "const payload = await api(")
	guardIndex := strings.Index(historyFunction, "generation !== state.selectionRequestGeneration || state.selectedID !== record.id")
	currentIndex := strings.Index(historyFunction, "const current = state.selectedRecord;")
	renderIndex := strings.Index(historyFunction, "renderInspector(current);")
	if captureIndex < 0 || requestIndex < 0 || guardIndex < 0 || currentIndex < 0 || renderIndex < 0 || captureIndex > requestIndex || guardIndex < requestIndex || guardIndex > currentIndex || currentIndex > renderIndex {
		t.Fatalf("loadHistory must capture selection generation and reject a late response before rendering:\n%s", historyFunction)
	}
}

func TestDecisionDashboardDecisionListCancelsStaleRequests(t *testing.T) {
	data, err := decisionDashboardAssets.ReadFile("decision_dashboard_assets/app.js")
	if err != nil {
		t.Fatalf("ReadFile app.js: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "async function loadDecisions(appendPage = false)")
	if start < 0 {
		t.Fatal("loadDecisions function is missing")
	}
	endOffset := strings.Index(text[start:], "function renderDecisionList()")
	if endOffset < 0 {
		t.Fatal("loadDecisions function boundary is missing")
	}
	loadFunction := text[start : start+endOffset]
	abortIndex := strings.Index(loadFunction, "state.decisionAbortController.abort();")
	controllerIndex := strings.Index(loadFunction, "const controller = new AbortController();")
	signalIndex := strings.Index(loadFunction, "signal: controller.signal")
	abortErrorIndex := strings.Index(loadFunction, `error.name === "AbortError"`)
	throwIndex := strings.Index(loadFunction, "throw error;")
	if abortIndex < 0 || controllerIndex < 0 || signalIndex < 0 || abortErrorIndex < 0 || throwIndex < 0 || abortIndex > controllerIndex || controllerIndex > signalIndex || signalIndex > abortErrorIndex || abortErrorIndex > throwIndex {
		t.Fatalf("loadDecisions must abort the previous request and silently ignore AbortError before rethrowing other failures:\n%s", loadFunction)
	}
}

func TestDecisionCommandReusesServerAndRejectsNoninteractiveMode(t *testing.T) {
	root := t.TempDir()
	var opened []string
	previousOpen := decisionDashboardOpenURL
	decisionDashboardOpenURL = func(target string) error {
		opened = append(opened, target)
		return nil
	}
	t.Cleanup(func() { decisionDashboardOpenURL = previousOpen })
	runtime := &runtimeState{
		writer:               io.Discard,
		ui:                   NewUI(),
		interactive:          true,
		workspace:            Workspace{BaseRoot: root, Root: filepath.Join(root, ".kernforge", "worktrees", "active")},
		decisionStore:        &ImplementationDecisionStore{Dir: filepath.Join(root, "records")},
		decisionProfileStore: &ImplementationPreferenceProfileStore{Path: filepath.Join(root, "profiles", "profile.json")},
	}
	t.Cleanup(runtime.closeExtensions)
	if err := runtime.handleDecisionCommand(""); err != nil {
		t.Fatalf("first /decision: %v", err)
	}
	firstOrigin := runtime.decisionDashboard.Origin()
	if runtime.decisionDashboard.workspace != root {
		t.Fatalf("dashboard workspace=%q, want stable base root %q", runtime.decisionDashboard.workspace, root)
	}
	if err := runtime.handleDecisionCommand(""); err != nil {
		t.Fatalf("second /decision: %v", err)
	}
	if len(opened) != 2 || runtime.decisionDashboard.Origin() != firstOrigin {
		t.Fatalf("dashboard server was not reused: opened=%#v first=%q current=%q", opened, firstOrigin, runtime.decisionDashboard.Origin())
	}
	runtime.interactive = false
	if err := runtime.handleDecisionCommand(""); err == nil {
		t.Fatal("noninteractive /decision must explain that its process lifetime is insufficient")
	}
}

func TestDecisionDashboardCloseStopsListener(t *testing.T) {
	server, _, _, client := startDecisionDashboardTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := server.Close(ctx); err != nil {
		cancel()
		t.Fatalf("Close: %v", err)
	}
	cancel()
	select {
	case <-server.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("dashboard server did not stop")
	}
	response, err := client.client.Get(client.origin + "/")
	if err == nil {
		response.Body.Close()
		t.Fatalf("dashboard listener still accepted requests after Close: status=%d", response.StatusCode)
	}
}
