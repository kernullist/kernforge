package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// updateManifestURL is the remote endpoint that serves the release manifest. It
// is intentionally empty by default so that, unless a build is stamped with
// -X main.updateManifestURL=..., every self-update check is a no-op. Phase 1
// only checks and notifies; it never replaces the running binary.
var updateManifestURL = ""

// updateCheckInterval is the minimum spacing between remote manifest fetches.
// The on-disk update-state.json last_check_time enforces this across sessions.
const updateCheckInterval = time.Hour

// updateCheckHTTPTimeout caps a single manifest fetch so a slow or hung
// endpoint can never delay the REPL or the daemon.
const updateCheckHTTPTimeout = 5 * time.Second

// updateCheckMaxJitter randomizes the throttle window slightly so many clients
// that share a clock do not all fetch at the same instant.
const updateCheckMaxJitter = 5 * time.Minute

// UpdateManifest is the release metadata document published next to a release
// artifact. Only LatestVersion is strictly required for the available-update
// decision; the remaining fields are advisory and surfaced to the operator.
type UpdateManifest struct {
	LatestVersion       string   `json:"latest_version"`
	ReleaseURL          string   `json:"release_url,omitempty"`
	ReleaseDate         string   `json:"release_date,omitempty"`
	SignedSHA256        string   `json:"signed_sha256,omitempty"`
	Notes               string   `json:"notes,omitempty"`
	MinSupportedVersion string   `json:"min_supported_version,omitempty"`
	BreakingChanges     []string `json:"breaking_changes,omitempty"`
}

// UpdateCheckResult is the structured outcome of one check, suitable for both
// scriptable CLI output and the proactive suggestion engine.
type UpdateCheckResult struct {
	CurrentVersion  string         `json:"current_version"`
	LatestVersion   string         `json:"latest_version,omitempty"`
	UpdateAvailable bool           `json:"update_available"`
	BelowMinimum    bool           `json:"below_minimum,omitempty"`
	ManifestURL     string         `json:"manifest_url,omitempty"`
	Checked         bool           `json:"checked"`
	Error           string         `json:"error,omitempty"`
	Manifest        UpdateManifest `json:"manifest,omitempty"`
}

// updateCheckRand guards a non-crypto PRNG used only for throttle jitter.
var (
	updateCheckRandMu sync.Mutex
	updateCheckRand   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func updateCheckJitter() time.Duration {
	updateCheckRandMu.Lock()
	defer updateCheckRandMu.Unlock()
	if updateCheckMaxJitter <= 0 {
		return 0
	}
	return time.Duration(updateCheckRand.Int63n(int64(updateCheckMaxJitter)))
}

// updateCheckConfigured reports whether a manifest endpoint is set. When it is
// not, all check paths must be no-ops.
func updateCheckConfigured() bool {
	return strings.TrimSpace(updateManifestURL) != ""
}

// updateCheckLogf emits a single best-effort diagnostic line for a failed or
// skipped background check. It writes to stderr only when KERNFORGE_DEBUG is
// set so a normal session is never polluted by an unreachable endpoint.
func updateCheckLogf(format string, args ...any) {
	if strings.TrimSpace(os.Getenv("KERNFORGE_DEBUG")) == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "update-check: "+format+"\n", args...)
}

// fetchUpdateManifest performs one short, bounded HTTP GET and decodes the
// manifest. It never blocks longer than updateCheckHTTPTimeout and returns a
// plain error on any failure so callers can degrade gracefully.
func fetchUpdateManifest(ctx context.Context, client *http.Client, manifestURL string) (UpdateManifest, error) {
	manifestURL = strings.TrimSpace(manifestURL)
	if manifestURL == "" {
		return UpdateManifest{}, fmt.Errorf("update manifest URL is empty")
	}
	if client == nil {
		client = &http.Client{Timeout: updateCheckHTTPTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return UpdateManifest{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return UpdateManifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UpdateManifest{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	// Cap the body so a hostile or misconfigured endpoint cannot exhaust memory.
	limited := io.LimitReader(resp.Body, 1<<20)
	data, err := io.ReadAll(limited)
	if err != nil {
		return UpdateManifest{}, err
	}
	manifest, err := decodeUpdateManifest(data)
	if err != nil {
		return UpdateManifest{}, err
	}
	return manifest, nil
}

func decodeUpdateManifest(data []byte) (UpdateManifest, error) {
	var manifest UpdateManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return UpdateManifest{}, err
	}
	if strings.TrimSpace(manifest.LatestVersion) == "" {
		return UpdateManifest{}, fmt.Errorf("manifest missing latest_version")
	}
	return manifest, nil
}

// evaluateUpdateManifest compares a fetched manifest against the running
// version and produces the structured result. It performs no I/O.
func evaluateUpdateManifest(current string, manifest UpdateManifest) UpdateCheckResult {
	result := UpdateCheckResult{
		CurrentVersion: current,
		LatestVersion:  strings.TrimSpace(manifest.LatestVersion),
		ManifestURL:    strings.TrimSpace(updateManifestURL),
		Checked:        true,
		Manifest:       manifest,
	}
	if result.LatestVersion != "" && compareSemver(result.LatestVersion, current) > 0 {
		result.UpdateAvailable = true
	}
	if min := strings.TrimSpace(manifest.MinSupportedVersion); min != "" {
		if compareSemver(current, min) < 0 {
			result.BelowMinimum = true
		}
	}
	return result
}

// RunUpdateCheckNow performs a synchronous, bounded manifest fetch and returns
// the structured result. It is used by the CLI "version --check-update" path.
// It does NOT consult or update the throttle state; it always fetches when a
// manifest URL is configured. When no URL is configured it returns a result
// with Checked=false and UpdateAvailable=false.
func RunUpdateCheckNow(ctx context.Context, client *http.Client) UpdateCheckResult {
	current := currentVersion()
	if !updateCheckConfigured() {
		return UpdateCheckResult{CurrentVersion: current, Checked: false}
	}
	manifest, err := fetchUpdateManifest(ctx, client, updateManifestURL)
	if err != nil {
		return UpdateCheckResult{
			CurrentVersion: current,
			ManifestURL:    strings.TrimSpace(updateManifestURL),
			Checked:        false,
			Error:          err.Error(),
		}
	}
	return evaluateUpdateManifest(current, manifest)
}

// MaybeRunBackgroundUpdateCheck runs a throttled check on a background
// goroutine and never blocks the caller. It returns immediately. When the
// throttle window has not elapsed (per update-state.json) or no manifest URL
// is configured, it does nothing. On a successful fetch that finds a newer,
// non-dismissed version it records available_version into update-state.json so
// the proactive suggestion engine can surface it on a later turn.
//
// Phase 1 scope: this only records availability. It never downloads or
// replaces the binary. The optional done channel is closed when the background
// work finishes; it exists primarily for tests and may be nil.
func MaybeRunBackgroundUpdateCheck(client *http.Client, done chan<- struct{}) {
	closeDone := func() {
		if done != nil {
			close(done)
		}
	}
	if !updateCheckConfigured() {
		closeDone()
		return
	}
	statePath := updateStatePath()
	state, _ := LoadUpdateState(statePath)
	now := time.Now()
	if !updateCheckDue(state, now) {
		closeDone()
		return
	}
	go func() {
		defer closeDone()
		runBackgroundUpdateCheck(statePath, client, now)
	}()
}

// updateCheckDue reports whether enough time has elapsed since the last check.
// A zero last_check_time always allows a check (first run).
func updateCheckDue(state UpdateState, now time.Time) bool {
	last := state.LastCheckTime
	if last.IsZero() {
		return true
	}
	window := updateCheckInterval + updateCheckJitter()
	return now.Sub(last) >= window
}

func runBackgroundUpdateCheck(statePath string, client *http.Client, startedAt time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckHTTPTimeout)
	defer cancel()
	manifest, err := fetchUpdateManifest(ctx, client, updateManifestURL)

	// Reload under the same path so we merge onto the latest persisted state
	// (a concurrent session may have written a dismissal in the meantime).
	state, _ := LoadUpdateState(statePath)
	state.LastCheckTime = startedAt
	if err != nil {
		updateCheckLogf("fetch failed: %v", err)
		// Persist the throttle stamp even on failure so a flapping endpoint
		// cannot turn into a hot retry loop.
		if saveErr := SaveUpdateState(statePath, state); saveErr != nil {
			updateCheckLogf("save state failed: %v", saveErr)
		}
		return
	}
	result := evaluateUpdateManifest(currentVersion(), manifest)
	if result.UpdateAvailable && !strings.EqualFold(strings.TrimSpace(state.DismissedVersion), result.LatestVersion) {
		state.AvailableVersion = result.LatestVersion
	} else {
		// No newer version, or the newer version was already dismissed:
		// clear any stale availability so the suggestion does not linger.
		state.AvailableVersion = ""
	}
	if saveErr := SaveUpdateState(statePath, state); saveErr != nil {
		updateCheckLogf("save state failed: %v", saveErr)
	}
}

// compareSemver compares two dotted version strings. It returns a negative
// number when a < b, zero when they are equal, and a positive number when
// a > b. It tolerates a leading "v" and a "+build"/"-prerelease" suffix, and
// compares the dotted numeric core component-by-component. Non-numeric or
// missing components are treated as zero so "1.2" and "1.2.0" compare equal.
// A pre-release (e.g. "1.2.0-rc1") sorts below the same release core.
func compareSemver(a, b string) int {
	aCore, aPre := splitSemver(a)
	bCore, bPre := splitSemver(b)
	aParts := strings.Split(aCore, ".")
	bParts := strings.Split(bCore, ".")
	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}
	for i := 0; i < maxLen; i++ {
		av := semverComponentValue(aParts, i)
		bv := semverComponentValue(bParts, i)
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	// Equal numeric cores: a present pre-release sorts below an absent one.
	switch {
	case aPre == "" && bPre == "":
		return 0
	case aPre == "" && bPre != "":
		return 1
	case aPre != "" && bPre == "":
		return -1
	default:
		return strings.Compare(aPre, bPre)
	}
}

func splitSemver(value string) (core string, pre string) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	value = strings.TrimPrefix(value, "V")
	// Strip any build metadata; it never affects precedence.
	if idx := strings.IndexByte(value, '+'); idx >= 0 {
		value = value[:idx]
	}
	if idx := strings.IndexByte(value, '-'); idx >= 0 {
		return value[:idx], value[idx+1:]
	}
	return value, ""
}

func semverComponentValue(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	component := strings.TrimSpace(parts[index])
	if component == "" {
		return 0
	}
	value, err := strconv.Atoi(component)
	if err != nil {
		return 0
	}
	if value < 0 {
		return 0
	}
	return value
}

// renderUpdateCheckResultText formats the structured result for the
// non-interactive "version --check-update" CLI path. The output is stable and
// scriptable: a leading status token, then key=value lines.
func renderUpdateCheckResultText(result UpdateCheckResult) string {
	var b strings.Builder
	status := "up-to-date"
	switch {
	case !result.Checked && strings.TrimSpace(result.Error) != "":
		status = "check-failed"
	case !result.Checked:
		status = "not-configured"
	case result.UpdateAvailable:
		status = "update-available"
	}
	fmt.Fprintf(&b, "update_check: %s\n", status)
	fmt.Fprintf(&b, "current_version: %s\n", valueOrDefault(result.CurrentVersion, "unknown"))
	if strings.TrimSpace(result.LatestVersion) != "" {
		fmt.Fprintf(&b, "latest_version: %s\n", result.LatestVersion)
	}
	fmt.Fprintf(&b, "update_available: %s\n", strconv.FormatBool(result.UpdateAvailable))
	if result.BelowMinimum {
		fmt.Fprintf(&b, "below_min_supported_version: true\n")
	}
	if strings.TrimSpace(result.Manifest.ReleaseURL) != "" {
		fmt.Fprintf(&b, "release_url: %s\n", strings.TrimSpace(result.Manifest.ReleaseURL))
	}
	if len(result.Manifest.BreakingChanges) > 0 {
		fmt.Fprintf(&b, "breaking_changes: %s\n", strings.Join(result.Manifest.BreakingChanges, "; "))
	}
	if strings.TrimSpace(result.Error) != "" {
		fmt.Fprintf(&b, "error: %s\n", strings.TrimSpace(result.Error))
	}
	// "--apply" binary replacement is intentionally not implemented in Phase 1.
	fmt.Fprintf(&b, "apply_supported: false\n")
	return b.String()
}
