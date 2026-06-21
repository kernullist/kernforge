package main

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// updateStateSchemaVersion lets future shape changes migrate cleanly.
const updateStateSchemaVersion = 1

// UpdateState persists the throttle stamp and the most recent self-update
// decision across sessions. It deliberately holds no secrets: only the last
// check time, the version currently advertised as available, and a version the
// operator has dismissed so it is not re-notified.
type UpdateState struct {
	SchemaVersion    int       `json:"schema_version,omitempty"`
	LastCheckTime    time.Time `json:"last_check_time,omitempty"`
	AvailableVersion string    `json:"available_version,omitempty"`
	DismissedVersion string    `json:"dismissed_version,omitempty"`
}

// LoadUpdateState reads the persisted state. A missing or malformed file is not
// an error: it yields a zero-value state so the first run behaves like "never
// checked". Callers may ignore the error and use the returned state directly.
func LoadUpdateState(path string) (UpdateState, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return UpdateState{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return UpdateState{}, nil
		}
		return UpdateState{}, err
	}
	var state UpdateState
	if err := json.Unmarshal(data, &state); err != nil {
		// A corrupt state file must never block a check; treat it as empty.
		return UpdateState{}, err
	}
	return state, nil
}

// SaveUpdateState writes the state through the shared atomic-write helper so a
// crash mid-write cannot corrupt the file other sessions read.
func SaveUpdateState(path string, state UpdateState) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if state.SchemaVersion == 0 {
		state.SchemaVersion = updateStateSchemaVersion
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o644)
}

// pendingUpdateNotice returns the available version and its release URL when
// update-state.json advertises a newer, non-dismissed version. The newer-than
// comparison is re-evaluated against the running binary so a state file written
// by an older build (or after the operator upgraded out of band) never produces
// a stale suggestion. An empty available version yields ("", "", false).
func pendingUpdateNotice(state UpdateState) (version string, available bool) {
	candidate := strings.TrimSpace(state.AvailableVersion)
	if candidate == "" {
		return "", false
	}
	if strings.EqualFold(candidate, strings.TrimSpace(state.DismissedVersion)) {
		return "", false
	}
	if compareSemver(candidate, currentVersion()) <= 0 {
		return "", false
	}
	return candidate, true
}

// pendingUpdateSuggestion reads the persisted update-state and reports a newer,
// non-dismissed version suitable for a proactive suggestion. It is a no-op
// (returns ok=false) when no manifest endpoint is configured, so a build
// without -X main.updateManifestURL never suggests an update. A missing or
// malformed state file is treated as "nothing available".
func pendingUpdateSuggestion() (version string, ok bool) {
	if !updateCheckConfigured() {
		return "", false
	}
	state, _ := LoadUpdateState(updateStatePath())
	return pendingUpdateNotice(state)
}
