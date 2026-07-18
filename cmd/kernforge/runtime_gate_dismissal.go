package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	runtimeGateDismissalScopeSession   = "session"
	runtimeGateDismissalScopeWorkspace = "workspace"
	runtimeGateDismissalFileName       = "runtime_gate_dismissal.json"
)

// RuntimeGateDismissal records an explicit operator decision to stop a prior
// review from blocking the runtime gate. Artifacts on disk are kept; only gate
// attachment is suppressed until a newer review is recorded or the dismissal is
// restored.
type RuntimeGateDismissal struct {
	ClearedAt   time.Time `json:"cleared_at,omitempty"`
	ReviewRunID string    `json:"review_run_id,omitempty"`
	// ReviewCreatedAt is optional provenance so an older review remains ignored
	// even if its id is missing from a restored session snapshot.
	ReviewCreatedAt time.Time `json:"review_created_at,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	Scope           string    `json:"scope,omitempty"`
	// IgnoreReviewUntilNewer drops the dismissed review from gate attachment.
	// A later review with a different id clears the dismissal automatically.
	IgnoreReviewUntilNewer bool `json:"ignore_review_until_newer,omitempty"`
}

func (d *RuntimeGateDismissal) Normalize() {
	if d == nil {
		return
	}
	d.ReviewRunID = strings.TrimSpace(d.ReviewRunID)
	d.Reason = strings.TrimSpace(d.Reason)
	d.Scope = strings.ToLower(strings.TrimSpace(d.Scope))
	switch d.Scope {
	case runtimeGateDismissalScopeSession, runtimeGateDismissalScopeWorkspace:
	default:
		if d.Scope == "" {
			d.Scope = runtimeGateDismissalScopeWorkspace
		} else {
			d.Scope = runtimeGateDismissalScopeWorkspace
		}
	}
	if !d.IgnoreReviewUntilNewer {
		// Default semantics: always ignore until a newer review replaces it.
		d.IgnoreReviewUntilNewer = true
	}
}

func (d RuntimeGateDismissal) Active() bool {
	d.Normalize()
	return d.IgnoreReviewUntilNewer && (d.ReviewRunID != "" || !d.ClearedAt.IsZero())
}

func runtimeGateDismissalPath(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	return filepath.Join(reviewArtifactRoot(root), "..", runtimeGateDismissalFileName)
}

func resolvedRuntimeGateDismissalPath(root string) string {
	path := runtimeGateDismissalPath(root)
	if path == "" {
		return ""
	}
	cleaned, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return cleaned
}

func loadWorkspaceRuntimeGateDismissal(root string) (*RuntimeGateDismissal, error) {
	path := resolvedRuntimeGateDismissalPath(root)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dismissal RuntimeGateDismissal
	if err := json.Unmarshal(data, &dismissal); err != nil {
		return nil, err
	}
	dismissal.Normalize()
	if !dismissal.Active() {
		return nil, nil
	}
	return &dismissal, nil
}

func saveWorkspaceRuntimeGateDismissal(root string, dismissal *RuntimeGateDismissal) error {
	path := resolvedRuntimeGateDismissalPath(root)
	if path == "" {
		return fmt.Errorf("workspace root is required to persist gate dismissal")
	}
	if dismissal == nil {
		_ = os.Remove(path)
		return nil
	}
	dismissal.Normalize()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(dismissal, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func clearWorkspaceRuntimeGateDismissal(root string) error {
	path := resolvedRuntimeGateDismissalPath(root)
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// effectiveRuntimeGateDismissal returns the session dismissal first, then the
// workspace dismissal. Session scope can temporarily override workspace restore.
func effectiveRuntimeGateDismissal(root string, session *Session) *RuntimeGateDismissal {
	if session != nil && session.RuntimeGateDismissal != nil {
		copyDismissal := *session.RuntimeGateDismissal
		copyDismissal.Normalize()
		if copyDismissal.Active() {
			return &copyDismissal
		}
	}
	dismissal, err := loadWorkspaceRuntimeGateDismissal(root)
	if err != nil || dismissal == nil {
		return nil
	}
	return dismissal
}

func runtimeGateReviewIsDismissed(root string, session *Session, review ReviewRun) bool {
	dismissal := effectiveRuntimeGateDismissal(root, session)
	if dismissal == nil || !dismissal.Active() {
		return false
	}
	reviewID := strings.TrimSpace(review.ID)
	if reviewID != "" && dismissal.ReviewRunID != "" {
		return strings.EqualFold(reviewID, dismissal.ReviewRunID)
	}
	// Fall back to "any review not newer than the clear timestamp".
	if !dismissal.ClearedAt.IsZero() {
		created := review.CreatedAt
		if created.IsZero() {
			// Unknown creation time with an active dismissal for "current latest"
			// still matches when the id was empty.
			return dismissal.ReviewRunID == ""
		}
		return !created.After(dismissal.ClearedAt)
	}
	return false
}

// noteRuntimeGateDismissalAfterReview clears an active dismissal once a newer
// review id is recorded, so /gate clear only drops prior baggage.
func noteRuntimeGateDismissalAfterReview(root string, session *Session, run ReviewRun) {
	newID := strings.TrimSpace(run.ID)
	if newID == "" {
		return
	}
	if session != nil && session.RuntimeGateDismissal != nil {
		session.RuntimeGateDismissal.Normalize()
		if session.RuntimeGateDismissal.Active() &&
			!strings.EqualFold(strings.TrimSpace(session.RuntimeGateDismissal.ReviewRunID), newID) {
			session.RuntimeGateDismissal = nil
		}
	}
	workspace, err := loadWorkspaceRuntimeGateDismissal(root)
	if err != nil || workspace == nil {
		return
	}
	if workspace.Active() && !strings.EqualFold(strings.TrimSpace(workspace.ReviewRunID), newID) {
		_ = clearWorkspaceRuntimeGateDismissal(root)
	}
}

func (rt *runtimeState) handleGateCommand(args string) error {
	if rt == nil {
		return fmt.Errorf("runtime is not ready")
	}
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return rt.printGateCommandStatus()
	}
	switch strings.ToLower(fields[0]) {
	case "status", "show":
		return rt.printGateCommandStatus()
	case "clear", "dismiss", "reset":
		return rt.clearRuntimeGate(fields[1:])
	case "restore", "undo":
		return rt.restoreRuntimeGate(fields[1:])
	case "help", "-h", "--help":
		rt.printGateCommandUsage()
		return nil
	default:
		return fmt.Errorf("unknown /gate argument %q; use /gate status|clear|restore", fields[0])
	}
}

func (rt *runtimeState) printGateCommandUsage() {
	fmt.Fprintln(rt.writer, rt.ui.infoLine(localizedText(rt.cfg,
		"Usage: /gate status | /gate clear [--session] [--reason <text>] | /gate restore",
		"사용법: /gate status | /gate clear [--session] [--reason <텍스트>] | /gate restore")))
	fmt.Fprintln(rt.writer, rt.ui.hintLine(localizedText(rt.cfg,
		"/gate clear stops a previous review from blocking completion/git write. It does not delete review files. A new /review replaces the dismissal automatically.",
		"/gate clear는 이전 리뷰가 완료·git write를 막지 않게 합니다. 리뷰 파일은 삭제하지 않습니다. 새 /review가 실행되면 dismissal은 자동으로 끝납니다.")))
}

func (rt *runtimeState) printGateCommandStatus() error {
	if rt == nil {
		return fmt.Errorf("runtime is not ready")
	}
	ledger := rt.runtimeGateLedgerForStatus(runtimeGateActionFinalAnswer)
	ledger.Normalize()
	fmt.Fprintln(rt.writer, rt.ui.subsection("Runtime Gate"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV(localizedText(rt.cfg, "gate", "게이트"), runtimeGateStatusSummaryLocalized(rt.cfg, ledger)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("review_freshness", runtimeGateReviewFreshnessLabel(ledger)))
	if ledger.ReviewRunID != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("latest_review", ledger.ReviewRunID))
	}
	if len(ledger.Blockers) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("blockers", strings.Join(limitStrings(ledger.Blockers, 3), " | ")))
	}
	if len(ledger.Warnings) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("warnings", strings.Join(limitStrings(ledger.Warnings, 3), " | ")))
	}
	if next := runtimeGatePrimaryNextCommandLine(ledger); next != "" {
		fmt.Fprintln(rt.writer, rt.ui.activityLine("next", next))
	}
	rt.printRuntimeGateRecoveryGuidance(ledger)

	root := rt.runtimeGateWorkspaceRoot()
	dismissal := effectiveRuntimeGateDismissal(root, rt.session)
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.subsection("Gate Dismissal"))
	if dismissal == nil || !dismissal.Active() {
		fmt.Fprintln(rt.writer, rt.ui.hintLine(localizedText(rt.cfg,
			"No active /gate clear. Previous reviews can still block completion/git write when stale.",
			"활성 /gate clear 없음. 이전 리뷰가 stale이면 완료·git write를 계속 막을 수 있습니다.")))
		fmt.Fprintln(rt.writer, rt.ui.activityLine("next", localizedText(rt.cfg,
			"/gate clear - dismiss previous-session gate baggage",
			"/gate clear - 이전 세션 게이트 부담 해제")))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("dismissal", "active"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("scope", dismissal.Scope))
	if dismissal.ReviewRunID != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("dismissed_review", dismissal.ReviewRunID))
	}
	if !dismissal.ClearedAt.IsZero() {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("cleared_at", dismissal.ClearedAt.Format(time.RFC3339)))
	}
	if dismissal.Reason != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("reason", dismissal.Reason))
	}
	fmt.Fprintln(rt.writer, rt.ui.activityLine("next", localizedText(rt.cfg,
		"/gate restore - re-enable the dismissed review for gate checks",
		"/gate restore - 해제한 리뷰를 게이트 검사에 다시 사용")))
	return nil
}

func (rt *runtimeState) clearRuntimeGate(args []string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	scope := runtimeGateDismissalScopeWorkspace
	reason := ""
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--session" || strings.EqualFold(arg, "session"):
			scope = runtimeGateDismissalScopeSession
		case arg == "--workspace" || strings.EqualFold(arg, "workspace"):
			scope = runtimeGateDismissalScopeWorkspace
		case arg == "--reason" || strings.HasPrefix(arg, "--reason="):
			if strings.HasPrefix(arg, "--reason=") {
				reason = strings.TrimSpace(strings.TrimPrefix(arg, "--reason="))
				continue
			}
			if i+1 >= len(args) {
				return fmt.Errorf("usage: /gate clear [--session] [--reason <text>]")
			}
			// Remainder after --reason is free text (may contain spaces).
			reason = strings.TrimSpace(strings.Join(args[i+1:], " "))
			i = len(args)
		case arg == "--help" || arg == "-h" || strings.EqualFold(arg, "help"):
			rt.printGateCommandUsage()
			return nil
		default:
			return fmt.Errorf("unknown /gate clear argument %q; use --session or --reason", arg)
		}
	}

	root := rt.runtimeGateWorkspaceRoot()
	review, hasReview := runtimeGateReviewRun(root, rt.session, nil)
	// If the only thing available is already dismissed, still refresh timestamps.
	if !hasReview {
		if latest, _, ok, err := loadLatestReviewRun(root); err == nil && ok {
			review = latest
			hasReview = strings.TrimSpace(latest.ID) != ""
		}
	}
	if !hasReview && (rt.session.LastReviewRun == nil) &&
		(rt.session.RuntimeGateLedger == nil || len(rt.session.RuntimeGateLedger.Blockers) == 0) {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(localizedText(rt.cfg,
			"No previous review is currently attached to the runtime gate.",
			"현재 런타임 게이트에 붙어 있는 이전 리뷰가 없습니다.")))
		return nil
	}

	dismissal := RuntimeGateDismissal{
		ClearedAt:              time.Now(),
		Reason:                 reason,
		Scope:                  scope,
		IgnoreReviewUntilNewer: true,
	}
	if hasReview {
		dismissal.ReviewRunID = strings.TrimSpace(review.ID)
		dismissal.ReviewCreatedAt = review.CreatedAt
	} else if rt.session.LastReviewRun != nil {
		dismissal.ReviewRunID = strings.TrimSpace(rt.session.LastReviewRun.ID)
		dismissal.ReviewCreatedAt = rt.session.LastReviewRun.CreatedAt
	}
	if dismissal.Reason == "" {
		dismissal.Reason = "operator cleared previous-session runtime gate"
	}
	dismissal.Normalize()

	// Drop session-local pointers that re-surface the dismissed review.
	if rt.session.LastReviewRun != nil &&
		(dismissal.ReviewRunID == "" || strings.EqualFold(rt.session.LastReviewRun.ID, dismissal.ReviewRunID)) {
		rt.session.LastReviewRun = nil
	}
	rt.session.RuntimeGateLedger = nil
	// Harness recovery cards that only restate the old gate should not keep
	// trapping the operator after an explicit clear.
	if rt.session.PendingHarnessBlockedRecovery != nil {
		rt.session.PendingHarnessBlockedRecovery = nil
	}

	switch scope {
	case runtimeGateDismissalScopeSession:
		copyDismissal := dismissal
		rt.session.RuntimeGateDismissal = &copyDismissal
	default:
		rt.session.RuntimeGateDismissal = nil
		if err := saveWorkspaceRuntimeGateDismissal(root, &dismissal); err != nil {
			return err
		}
	}

	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}

	// Refresh ledger after dismissal so the operator sees the new state.
	ledger := rt.runtimeGateLedgerForStatus(runtimeGateActionFinalAnswer)
	fmt.Fprintln(rt.writer, rt.ui.successLine(localizedText(rt.cfg,
		"Cleared previous runtime gate baggage.",
		"이전 런타임 게이트 부담을 해제했습니다.")))
	if dismissal.ReviewRunID != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("dismissed_review", dismissal.ReviewRunID))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("scope", dismissal.Scope))
	fmt.Fprintln(rt.writer, rt.ui.hintLine(localizedText(rt.cfg,
		"Review files were kept. Run /review before claiming completion or write-side git if you want a fresh gate. /gate restore re-enables the dismissed review.",
		"리뷰 파일은 유지됩니다. 완료·git write 전에 다시 게이트하려면 /review를 실행하세요. /gate restore로 해제를 되돌릴 수 있습니다.")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV(localizedText(rt.cfg, "gate", "게이트"), runtimeGateStatusSummaryLocalized(rt.cfg, ledger)))
	if runtimeGateNeedsRecoveryGuidance(ledger) {
		rt.printRuntimeGateRecoveryGuidance(ledger)
	}
	return nil
}

func (rt *runtimeState) restoreRuntimeGate(args []string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	for _, arg := range args {
		switch strings.TrimSpace(arg) {
		case "", "--help", "-h", "help":
			if strings.TrimSpace(arg) != "" {
				rt.printGateCommandUsage()
				return nil
			}
		default:
			return fmt.Errorf("usage: /gate restore")
		}
	}
	root := rt.runtimeGateWorkspaceRoot()
	hadSession := rt.session.RuntimeGateDismissal != nil
	rt.session.RuntimeGateDismissal = nil
	hadWorkspace := false
	if existing, err := loadWorkspaceRuntimeGateDismissal(root); err == nil && existing != nil {
		hadWorkspace = true
		if err := clearWorkspaceRuntimeGateDismissal(root); err != nil {
			return err
		}
	}
	if !hadSession && !hadWorkspace {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(localizedText(rt.cfg,
			"No /gate clear dismissal was active.",
			"활성 /gate clear dismissal이 없습니다.")))
		return nil
	}
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	ledger := rt.runtimeGateLedgerForStatus(runtimeGateActionFinalAnswer)
	fmt.Fprintln(rt.writer, rt.ui.successLine(localizedText(rt.cfg,
		"Restored runtime gate checks against the latest review.",
		"최신 리뷰에 대한 런타임 게이트 검사를 복구했습니다.")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV(localizedText(rt.cfg, "gate", "게이트"), runtimeGateStatusSummaryLocalized(rt.cfg, ledger)))
	if runtimeGateNeedsRecoveryGuidance(ledger) {
		rt.printRuntimeGateRecoveryGuidance(ledger)
	}
	return nil
}

func (rt *runtimeState) runtimeGateWorkspaceRoot() string {
	if rt == nil {
		return ""
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.workspace.Root
	}
	if strings.TrimSpace(root) == "" && rt.session != nil {
		root = rt.session.WorkingDir
	}
	if strings.TrimSpace(root) == "" && rt.session != nil {
		root = sessionBaseWorkingDir(rt.session)
	}
	return strings.TrimSpace(root)
}
