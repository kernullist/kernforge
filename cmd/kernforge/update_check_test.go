package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a    string
		b    string
		want int // sign: -1, 0, +1
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.2.0", "1.2", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0-rc1", "1.0.0-rc2", -1},
		{"1.0.0+build5", "1.0.0+build9", 0},
		{"1.2.3", "dev", 1},
		{"", "", 0},
	}
	for _, tc := range cases {
		got := compareSemver(tc.a, tc.b)
		gotSign := sign(got)
		if gotSign != tc.want {
			t.Errorf("compareSemver(%q,%q) sign = %d, want %d (raw %d)", tc.a, tc.b, gotSign, tc.want, got)
		}
	}
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	default:
		return 0
	}
}

func TestDecodeUpdateManifest(t *testing.T) {
	manifest, err := decodeUpdateManifest([]byte(`{"latest_version":"9.9.9","release_url":"https://example.test/r","breaking_changes":["ioctl abi"]}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if manifest.LatestVersion != "9.9.9" {
		t.Fatalf("latest_version = %q", manifest.LatestVersion)
	}
	if len(manifest.BreakingChanges) != 1 {
		t.Fatalf("breaking_changes = %v", manifest.BreakingChanges)
	}
	if _, err := decodeUpdateManifest([]byte(`{"release_url":"x"}`)); err == nil {
		t.Fatal("manifest without latest_version should error")
	}
	if _, err := decodeUpdateManifest([]byte(`{not json`)); err == nil {
		t.Fatal("malformed JSON should error")
	}
}

func TestRunUpdateCheckNowAgainstServer(t *testing.T) {
	restore := withUpdateManifestURL(t, "")
	defer restore()
	restoreVer := withAppVersion(t, "1.0.0")
	defer restoreVer()

	current := currentVersion()

	cases := []struct {
		name            string
		body            string
		status          int
		wantChecked     bool
		wantAvailable   bool
		wantErrContains string
	}{
		{
			name:          "newer",
			body:          `{"latest_version":"999.0.0"}`,
			status:        http.StatusOK,
			wantChecked:   true,
			wantAvailable: true,
		},
		{
			name:          "equal",
			body:          `{"latest_version":"` + current + `"}`,
			status:        http.StatusOK,
			wantChecked:   true,
			wantAvailable: false,
		},
		{
			name:          "older",
			body:          `{"latest_version":"0.0.1"}`,
			status:        http.StatusOK,
			wantChecked:   true,
			wantAvailable: false,
		},
		{
			name:            "malformed",
			body:            `{not valid json`,
			status:          http.StatusOK,
			wantChecked:     false,
			wantAvailable:   false,
			wantErrContains: "",
		},
		{
			name:            "server_error",
			body:            `oops`,
			status:          http.StatusInternalServerError,
			wantChecked:     false,
			wantAvailable:   false,
			wantErrContains: "status 500",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			updateManifestURL = server.URL

			result := RunUpdateCheckNow(context.Background(), &http.Client{Timeout: 2 * time.Second})
			if result.Checked != tc.wantChecked {
				t.Fatalf("Checked = %v, want %v (err=%q)", result.Checked, tc.wantChecked, result.Error)
			}
			if result.UpdateAvailable != tc.wantAvailable {
				t.Fatalf("UpdateAvailable = %v, want %v", result.UpdateAvailable, tc.wantAvailable)
			}
			if tc.wantErrContains != "" && !strings.Contains(result.Error, tc.wantErrContains) {
				t.Fatalf("Error = %q, want substring %q", result.Error, tc.wantErrContains)
			}
		})
	}
}

func TestRunUpdateCheckNowUnreachable(t *testing.T) {
	restore := withUpdateManifestURL(t, "")
	defer restore()

	// Point at a closed listener so Do() fails fast.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := server.URL
	server.Close()
	updateManifestURL = closedURL

	result := RunUpdateCheckNow(context.Background(), &http.Client{Timeout: 1 * time.Second})
	if result.Checked {
		t.Fatal("unreachable endpoint should not be Checked")
	}
	if result.UpdateAvailable {
		t.Fatal("unreachable endpoint must not report an update")
	}
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("unreachable endpoint should record an error")
	}
}

func TestRunUpdateCheckNowNoopWhenURLEmpty(t *testing.T) {
	restore := withUpdateManifestURL(t, "")
	defer restore()
	updateManifestURL = ""

	result := RunUpdateCheckNow(context.Background(), nil)
	if result.Checked {
		t.Fatal("empty manifest URL must be a no-op (Checked=false)")
	}
	if result.UpdateAvailable {
		t.Fatal("empty manifest URL must never report an update")
	}
	text := renderUpdateCheckResultText(result)
	if !strings.Contains(text, "update_check: not-configured") {
		t.Fatalf("expected not-configured status, got:\n%s", text)
	}
	if !strings.Contains(text, "apply_supported: false") {
		t.Fatalf("expected apply_supported: false, got:\n%s", text)
	}
}

func TestEvaluateUpdateManifestBelowMinimum(t *testing.T) {
	restore := withUpdateManifestURL(t, "https://example.test/m")
	defer restore()
	result := evaluateUpdateManifest("1.0.0", UpdateManifest{
		LatestVersion:       "2.0.0",
		MinSupportedVersion: "1.5.0",
	})
	if !result.UpdateAvailable {
		t.Fatal("2.0.0 > 1.0.0 should be available")
	}
	if !result.BelowMinimum {
		t.Fatal("1.0.0 < min 1.5.0 should set BelowMinimum")
	}
}

func TestUpdateCheckDueThrottle(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	if !updateCheckDue(UpdateState{}, now) {
		t.Fatal("zero last_check_time should always be due")
	}
	recent := UpdateState{LastCheckTime: now.Add(-1 * time.Minute)}
	if updateCheckDue(recent, now) {
		t.Fatal("a check one minute ago should NOT be due (interval is 1h)")
	}
	old := UpdateState{LastCheckTime: now.Add(-2 * time.Hour)}
	if !updateCheckDue(old, now) {
		t.Fatal("a check two hours ago should be due")
	}
}

func TestMaybeRunBackgroundUpdateCheckRecordsAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	restore := withUpdateManifestURL(t, "")
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"latest_version":"999.0.0","release_url":"https://example.test/r"}`))
	}))
	defer server.Close()
	updateManifestURL = server.URL

	done := make(chan struct{})
	MaybeRunBackgroundUpdateCheck(&http.Client{Timeout: 2 * time.Second}, done)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("background update check did not finish in time")
	}

	state, err := LoadUpdateState(updateStatePath())
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.AvailableVersion != "999.0.0" {
		t.Fatalf("AvailableVersion = %q, want 999.0.0", state.AvailableVersion)
	}
	if state.LastCheckTime.IsZero() {
		t.Fatal("LastCheckTime should be stamped after a check")
	}
}

func TestMaybeRunBackgroundUpdateCheckThrottled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	restore := withUpdateManifestURL(t, "")
	defer restore()

	// A recent check must short-circuit before any HTTP call.
	if err := SaveUpdateState(updateStatePath(), UpdateState{LastCheckTime: time.Now()}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	hit := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- struct{}{}:
		default:
		}
		_, _ = w.Write([]byte(`{"latest_version":"999.0.0"}`))
	}))
	defer server.Close()
	updateManifestURL = server.URL

	done := make(chan struct{})
	MaybeRunBackgroundUpdateCheck(&http.Client{Timeout: 2 * time.Second}, done)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("throttled call should return promptly")
	}
	select {
	case <-hit:
		t.Fatal("server must not be hit when the throttle window has not elapsed")
	default:
	}
}

func TestUpdateStateLoadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-state.json")

	// Missing file is not an error and yields a zero state.
	state, err := LoadUpdateState(path)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if !state.LastCheckTime.IsZero() || state.AvailableVersion != "" {
		t.Fatalf("missing file should yield zero state, got %+v", state)
	}

	now := time.Now().Truncate(time.Second).UTC()
	want := UpdateState{LastCheckTime: now, AvailableVersion: "2.0.0", DismissedVersion: "1.5.0"}
	if err := SaveUpdateState(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadUpdateState(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !got.LastCheckTime.Equal(now) || got.AvailableVersion != "2.0.0" || got.DismissedVersion != "1.5.0" {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
	if got.SchemaVersion != updateStateSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", got.SchemaVersion, updateStateSchemaVersion)
	}
}

func TestPendingUpdateNotice(t *testing.T) {
	restoreVer := withAppVersion(t, "1.0.0")
	defer restoreVer()
	// Newer, non-dismissed -> surfaced.
	if v, ok := pendingUpdateNotice(UpdateState{AvailableVersion: "999.0.0"}); !ok || v != "999.0.0" {
		t.Fatalf("newer available should surface, got (%q,%v)", v, ok)
	}
	// Dismissed exactly -> suppressed.
	if _, ok := pendingUpdateNotice(UpdateState{AvailableVersion: "999.0.0", DismissedVersion: "999.0.0"}); ok {
		t.Fatal("dismissed version must be suppressed")
	}
	// Not newer than current -> suppressed.
	if _, ok := pendingUpdateNotice(UpdateState{AvailableVersion: "0.0.1"}); ok {
		t.Fatal("older available version must be suppressed")
	}
	// Empty -> suppressed.
	if _, ok := pendingUpdateNotice(UpdateState{}); ok {
		t.Fatal("empty available version must be suppressed")
	}
}

func TestProactiveSuggestionsEmitUpdateAvailableOnlyWhenAppropriate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// No manifest URL configured: never suggest, even with state on disk.
	noURL := withUpdateManifestURL(t, "")
	if err := SaveUpdateState(updateStatePath(), UpdateState{AvailableVersion: "999.0.0"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if hasUpdateSuggestion(BuildProactiveSuggestions(SituationSnapshot{}, ProactiveSources{})) {
		t.Fatal("update suggestion must not appear when no manifest URL is configured")
	}
	noURL()

	// Configured + newer non-dismissed available: suggested.
	restore := withUpdateManifestURL(t, "https://example.test/m")
	defer restore()
	if err := SaveUpdateState(updateStatePath(), UpdateState{AvailableVersion: "999.0.0"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	suggestions := BuildProactiveSuggestions(SituationSnapshot{}, ProactiveSources{})
	if !hasUpdateSuggestion(suggestions) {
		t.Fatal("update suggestion expected when a newer non-dismissed version is available")
	}

	// Dismissed: suppressed.
	if err := SaveUpdateState(updateStatePath(), UpdateState{AvailableVersion: "999.0.0", DismissedVersion: "999.0.0"}); err != nil {
		t.Fatalf("seed dismissed state: %v", err)
	}
	if hasUpdateSuggestion(BuildProactiveSuggestions(SituationSnapshot{}, ProactiveSources{})) {
		t.Fatal("dismissed update version must not be suggested")
	}
}

func TestUpdateCheckFlagParsing(t *testing.T) {
	if !kernforgeCLIVersionRequest([]string{"version", "--check-update"}) {
		t.Fatal("`version --check-update` should be a version request")
	}
	if !kernforgeCLIUpdateCheckRequest([]string{"version", "--check-update"}) {
		t.Fatal("`--check-update` should be detected")
	}
	if !kernforgeCLIUpdateCheckRequest([]string{"version", "-check-update"}) {
		t.Fatal("`-check-update` should be detected")
	}
	if kernforgeCLIUpdateCheckRequest([]string{"version"}) {
		t.Fatal("plain `version` must not request an update check")
	}
	if !versionCommandRequestsUpdateCheck("check-update") {
		t.Fatal("REPL /version check-update should be detected")
	}
	if versionCommandRequestsUpdateCheck("") {
		t.Fatal("REPL /version with no args must not request an update check")
	}
}

func hasUpdateSuggestion(items []Suggestion) bool {
	for _, item := range items {
		if item.Type == "update_available" {
			return true
		}
	}
	return false
}

// withUpdateManifestURL saves and restores the package-level updateManifestURL
// so tests can mutate it without leaking state across cases. It sets the URL to
// the provided initial value and returns a restore func.
func withUpdateManifestURL(t *testing.T, initial string) func() {
	t.Helper()
	previous := updateManifestURL
	updateManifestURL = initial
	return func() {
		updateManifestURL = previous
	}
}

// withAppVersion pins currentVersion() to a known semver for the duration of a
// test by overriding the appVersion ldflag var. Under test there is no embedded
// PE FileVersion, so appVersion is the source currentVersion() returns.
func withAppVersion(t *testing.T, version string) func() {
	t.Helper()
	previous := appVersion
	appVersion = version
	return func() {
		appVersion = previous
	}
}
