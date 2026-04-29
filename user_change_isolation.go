package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type UserChangeIsolationState struct {
	Root          string
	Baseline      map[string]workspaceFileSignature
	AgentTouched  map[string]bool
	StartedAt     time.Time
	SnapshotError string
}

type UserChangeIsolationReport struct {
	GeneratedAt     time.Time              `json:"generated_at,omitempty"`
	ConflictedPaths []string               `json:"conflicted_paths,omitempty"`
	Warnings        []string               `json:"warnings,omitempty"`
	Findings        []CodingHarnessFinding `json:"findings,omitempty"`
}

func (a *Agent) startUserChangeIsolation() {
	if a == nil || a.Session == nil {
		return
	}
	root := firstNonBlankString(a.Workspace.Root, a.Workspace.BaseRoot, a.Session.WorkingDir)
	state := &UserChangeIsolationState{
		Root:         root,
		AgentTouched: map[string]bool{},
		StartedAt:    time.Now(),
	}
	if strings.TrimSpace(root) != "" {
		baseline, err := snapshotWorkspaceFiles(root)
		if err != nil {
			state.SnapshotError = err.Error()
			a.Session.LastUserChangeIsolationReport = &UserChangeIsolationReport{
				GeneratedAt: time.Now(),
				Warnings: []string{
					"Could not capture the turn-start workspace baseline: " + err.Error(),
				},
			}
		} else {
			state.Baseline = baseline
		}
	}
	a.UserChangeIsolation = state
}

func (a *Agent) checkUserChangeIsolationBeforeTool(call ToolCall) error {
	if a == nil || a.Session == nil {
		return nil
	}
	scopes := patchTransactionCandidateScopes(a.Workspace, call)
	if len(scopes) == 0 {
		return nil
	}
	if a.UserChangeIsolation == nil {
		a.startUserChangeIsolation()
	}
	state := a.UserChangeIsolation
	if state == nil || len(state.Baseline) == 0 || strings.TrimSpace(state.Root) == "" {
		return nil
	}
	current, err := snapshotWorkspaceFiles(state.Root)
	if err != nil {
		report := UserChangeIsolationReport{
			GeneratedAt: time.Now(),
			Warnings: []string{
				"Could not refresh workspace snapshot before " + strings.TrimSpace(call.Name) + ": " + err.Error(),
			},
		}
		a.Session.LastUserChangeIsolationReport = &report
		return nil
	}
	conflicts := detectUserChangeConflicts(state.Root, state.Baseline, current, scopes, state.AgentTouched)
	if len(conflicts) == 0 {
		return nil
	}
	report := UserChangeIsolationReport{
		GeneratedAt:     time.Now(),
		ConflictedPaths: conflicts,
		Findings: []CodingHarnessFinding{{
			Severity: "blocker",
			Title:    "Target changed outside this agent",
			Detail:   "The target path changed after the turn started and before this edit: " + strings.Join(conflicts, ", "),
		}},
	}
	report.Normalize()
	a.Session.LastUserChangeIsolationReport = &report
	if a.Session.TaskState != nil {
		a.Session.TaskState.RecordEvent("user_change_conflict", strings.TrimSpace(a.Session.TaskState.ExecutorFocusNode), call.Name, "Blocked edit because the target changed outside this agent.", strings.Join(conflicts, ", "), "blocked", true)
	}
	return fmt.Errorf("%w: target path changed outside this agent since the turn started: %s. Re-read the file, preserve the user's latest changes, and build a fresh merge-aware edit instead of overwriting it", ErrUserChangeConflict, strings.Join(conflicts, ", "))
}

func userChangeIsolationToolResult(call ToolCall, err error) ToolExecutionResult {
	args := toolCallArgumentsMap(call)
	meta := defaultToolExecutionMeta(call.Name, args)
	meta["success"] = false
	meta["error"] = err.Error()
	meta["error_kind"] = "user_change_conflict"
	meta["blocked_by"] = "user_change_isolation"
	return ToolExecutionResult{
		DisplayText: "BLOCKED: " + err.Error(),
		Meta:        meta,
	}
}

func detectUserChangeConflicts(root string, baseline map[string]workspaceFileSignature, current map[string]workspaceFileSignature, scopes []string, agentTouched map[string]bool) []string {
	if len(baseline) == 0 || len(scopes) == 0 {
		return nil
	}
	allowedScopes := normalizeAllowedWriteScopes(root, scopes)
	seen := map[string]bool{}
	for path := range baseline {
		seen[filepath.Clean(path)] = true
	}
	for path := range current {
		seen[filepath.Clean(path)] = true
	}
	conflicts := make([]string, 0)
	for path := range seen {
		clean := filepath.Clean(path)
		if !pathMatchesAnyAllowedScope(clean, allowedScopes) {
			continue
		}
		key := normalizeUserChangeIsolationPath(clean)
		if agentTouched[key] {
			continue
		}
		left, leftOK := baseline[clean]
		right, rightOK := current[clean]
		if leftOK && rightOK && left == right {
			continue
		}
		conflicts = append(conflicts, filepath.ToSlash(clean))
	}
	sort.Strings(conflicts)
	return normalizeTaskStateList(conflicts, 32)
}

func (a *Agent) markAgentTouchedPaths(paths []string) {
	if a == nil || a.UserChangeIsolation == nil || len(paths) == 0 {
		return
	}
	root := strings.TrimSpace(a.UserChangeIsolation.Root)
	if a.UserChangeIsolation.AgentTouched == nil {
		a.UserChangeIsolation.AgentTouched = map[string]bool{}
	}
	for _, raw := range paths {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		abs := trimmed
		if !filepath.IsAbs(abs) && root != "" {
			abs = filepath.Join(root, filepath.FromSlash(trimmed))
		}
		rel := trimmed
		if root != "" {
			if computed, err := filepath.Rel(root, abs); err == nil {
				rel = computed
			}
		}
		a.UserChangeIsolation.AgentTouched[normalizeUserChangeIsolationPath(rel)] = true
	}
}

func normalizeUserChangeIsolationPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(trimmed)))
}

func (r *UserChangeIsolationReport) Normalize() {
	if r == nil {
		return
	}
	r.ConflictedPaths = normalizeTaskStateList(r.ConflictedPaths, 32)
	r.Warnings = normalizeTaskStateList(r.Warnings, 8)
	r.Findings = normalizeCodingHarnessFindings(r.Findings)
}

func (r UserChangeIsolationReport) RenderPromptSection() string {
	r.Normalize()
	lines := make([]string, 0, 4)
	if len(r.ConflictedPaths) > 0 {
		lines = append(lines, "- Conflicted paths: "+strings.Join(r.ConflictedPaths, ", "))
	}
	for _, finding := range r.Findings {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", finding.Severity, finding.Title, compactPromptSection(finding.Detail, 220)))
	}
	if len(r.Warnings) > 0 {
		lines = append(lines, "- Warnings: "+strings.Join(r.Warnings, " | "))
	}
	return strings.Join(lines, "\n")
}
