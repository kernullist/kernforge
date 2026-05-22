package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	reviewLedgerConsistencyOK      = "ok"
	reviewLedgerConsistencyWarning = "warning"
	reviewLedgerConsistencyBlocked = "blocked"

	reviewResumeSanityOK       = "ok"
	reviewResumeSanityConflict = "conflict"
)

type ReviewExternalLookupIntent struct {
	ID               string    `json:"id,omitempty"`
	ToolName         string    `json:"tool_name,omitempty"`
	Intent           string    `json:"intent,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	Status           string    `json:"status,omitempty"`
	Blocked          bool      `json:"blocked"`
	CreatedAt        time.Time `json:"created_at,omitempty"`
	UsedInFindingIDs []string  `json:"used_in_finding_ids,omitempty"`
}

type ReviewArtifactIntegrity struct {
	HashAlgorithm     string            `json:"hash_algorithm,omitempty"`
	EvidenceHash      string            `json:"evidence_hash,omitempty"`
	ProposalHash      string            `json:"proposal_hash,omitempty"`
	CurrentFileHashes map[string]string `json:"current_file_hashes,omitempty"`
	CheckedAt         time.Time         `json:"checked_at,omitempty"`
	Warnings          []string          `json:"warnings,omitempty"`
}

type ReviewLedgerConsistencyCheck struct {
	Status    string    `json:"status,omitempty"`
	Blockers  []string  `json:"blockers,omitempty"`
	Warnings  []string  `json:"warnings,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

type ReviewResumeSanityCheck struct {
	Status                    string    `json:"status,omitempty"`
	LastStableAction          string    `json:"last_stable_action,omitempty"`
	NextState                 string    `json:"next_state,omitempty"`
	ConflictReason            string    `json:"conflict_reason,omitempty"`
	EvidenceHash              string    `json:"evidence_hash,omitempty"`
	ProposalHash              string    `json:"proposal_hash,omitempty"`
	CurrentFileHashMismatches []string  `json:"current_file_hash_mismatches,omitempty"`
	CheckedAt                 time.Time `json:"checked_at,omitempty"`
}

func (a *Agent) recordExternalLookupIntents(calls []ToolCall, reason string, blocked bool) {
	if a == nil || a.Session == nil {
		return
	}
	intents := reviewExternalLookupIntentsFromToolCalls(calls, reason, blocked)
	if len(intents) == 0 {
		return
	}
	a.Session.ExternalLookupIntents = append(a.Session.ExternalLookupIntents, intents...)
	if len(a.Session.ExternalLookupIntents) > 64 {
		a.Session.ExternalLookupIntents = append([]ReviewExternalLookupIntent(nil), a.Session.ExternalLookupIntents[len(a.Session.ExternalLookupIntents)-64:]...)
	}
	for _, intent := range intents {
		a.Session.AppendConversationEvent(ConversationEvent{
			Kind:     conversationEventKindExternalLookup,
			Severity: conversationSeverityInfo,
			Summary:  fmt.Sprintf("external lookup intent %s: %s", valueOrDefault(intent.Status, "declared"), valueOrDefault(intent.Intent, intent.ToolName)),
			Entities: map[string]string{
				"tool":    intent.ToolName,
				"status":  intent.Status,
				"blocked": fmt.Sprintf("%t", intent.Blocked),
				"reason":  intent.Reason,
			},
		})
	}
}

func reviewExternalLookupIntentsFromToolCalls(calls []ToolCall, reason string, blocked bool) []ReviewExternalLookupIntent {
	now := time.Now()
	var out []ReviewExternalLookupIntent
	for _, call := range calls {
		if !toolCallNameLooksLikeWebResearch(call.Name) {
			continue
		}
		intent := strings.TrimSpace(webResearchCallIntent(call))
		if intent == "" {
			intent = strings.TrimSpace(call.Name)
		}
		status := "declared"
		if blocked {
			status = "blocked"
		}
		out = append(out, ReviewExternalLookupIntent{
			ID:        fmt.Sprintf("ELI-%s-%03d", now.Format("20060102-150405.000"), len(out)+1),
			ToolName:  strings.TrimSpace(call.Name),
			Intent:    intent,
			Reason:    strings.TrimSpace(reason),
			Status:    status,
			Blocked:   blocked,
			CreatedAt: now,
		})
	}
	return out
}

func reviewExternalLookupIntentsForRun(rt *runtimeState, run ReviewRun) []ReviewExternalLookupIntent {
	var out []ReviewExternalLookupIntent
	if rt != nil && rt.session != nil {
		out = append(out, rt.session.ExternalLookupIntents...)
	}
	lowerObjective := strings.ToLower(strings.TrimSpace(baseUserQueryText(run.Objective)))
	if requestExplicitlyAsksForWebResearch(lowerObjective) && len(out) == 0 {
		status := "requested"
		blocked := false
		reason := "user_request"
		if !strings.EqualFold(run.CapabilityManifest.WebSearch, "available") {
			status = "unavailable"
			blocked = true
			reason = "web_capability_unavailable"
		}
		out = append(out, ReviewExternalLookupIntent{
			ID:        "ELI-requested-001",
			ToolName:  "web_research",
			Intent:    "external research requested by user",
			Reason:    reason,
			Status:    status,
			Blocked:   blocked,
			CreatedAt: time.Now(),
		})
	}
	if len(out) > 32 {
		out = out[len(out)-32:]
	}
	return append([]ReviewExternalLookupIntent(nil), out...)
}

func mergeReviewExternalLookupIntents(left []ReviewExternalLookupIntent, right []ReviewExternalLookupIntent) []ReviewExternalLookupIntent {
	seen := map[string]bool{}
	var out []ReviewExternalLookupIntent
	add := func(items []ReviewExternalLookupIntent) {
		for _, item := range items {
			key := strings.ToLower(strings.Join([]string{
				strings.TrimSpace(item.ID),
				strings.TrimSpace(item.ToolName),
				strings.TrimSpace(item.Intent),
				strings.TrimSpace(item.Status),
				fmt.Sprintf("%t", item.Blocked),
			}, "|"))
			if key == "||||false" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, item)
		}
	}
	add(left)
	add(right)
	if len(out) > 32 {
		out = out[len(out)-32:]
	}
	return out
}

func buildReviewArtifactIntegrity(root string, run ReviewRun) ReviewArtifactIntegrity {
	integrity := ReviewArtifactIntegrity{
		HashAlgorithm:     "sha256",
		EvidenceHash:      reviewHashString(run.Evidence.Text, strings.Join(run.Evidence.Sources, "\n")),
		CurrentFileHashes: map[string]string{},
		CheckedAt:         time.Now(),
	}
	proposalData, err := json.Marshal(struct {
		EditProposals []EditProposal `json:"edit_proposals,omitempty"`
		DiffExcerpt   string         `json:"diff_excerpt,omitempty"`
		Fingerprint   string         `json:"fingerprint,omitempty"`
	}{
		EditProposals: run.EditProposals,
		DiffExcerpt:   run.ChangeSet.DiffExcerpt,
		Fingerprint:   run.ChangeSet.Fingerprint,
	})
	if err == nil {
		integrity.ProposalHash = reviewHashBytes(proposalData)
	} else {
		integrity.Warnings = append(integrity.Warnings, "proposal hash unavailable: "+err.Error())
	}
	for _, candidate := range reviewIntegrityCandidatePaths(run) {
		key, abs, ok, warning := resolveReviewIntegrityPath(root, candidate)
		if warning != "" {
			integrity.Warnings = append(integrity.Warnings, warning)
		}
		if !ok {
			continue
		}
		hash, err := reviewHashFile(abs)
		if err != nil {
			integrity.Warnings = append(integrity.Warnings, fmt.Sprintf("file hash unavailable for %s: %s", key, firstNonEmptyLine(err.Error())))
			continue
		}
		integrity.CurrentFileHashes[key] = hash
	}
	if len(integrity.CurrentFileHashes) == 0 {
		integrity.CurrentFileHashes = nil
	}
	integrity.Warnings = normalizeTaskStateList(integrity.Warnings, 16)
	return integrity
}

func reviewIntegrityCandidatePaths(run ReviewRun) []string {
	var paths []string
	paths = append(paths, run.ChangeSet.ChangedPaths...)
	paths = append(paths, run.ChangeSet.AddedPaths...)
	paths = append(paths, run.ChangeSet.ModifiedPaths...)
	paths = append(paths, run.Evidence.ChangedPaths...)
	for _, proposal := range run.EditProposals {
		paths = append(paths, proposal.File)
	}
	paths = normalizeCompletionAuditReviewPaths(paths)
	sort.Strings(paths)
	return paths
}

func resolveReviewIntegrityPath(root string, path string) (string, string, bool, string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", false, ""
	}
	path = stripReviewLineRangeSuffix(path)
	root = strings.TrimSpace(root)
	abs := path
	if !filepath.IsAbs(abs) {
		if root == "" {
			return filepathSlash(path), "", false, "file hash unavailable without workspace root: " + filepathSlash(path)
		}
		abs = filepath.Join(root, path)
	}
	abs = filepath.Clean(abs)
	key := filepathSlash(path)
	if root != "" {
		if rel, err := filepath.Rel(root, abs); err == nil {
			relSlash := filepathSlash(rel)
			if strings.HasPrefix(relSlash, "../") || relSlash == ".." {
				return key, abs, false, "file hash skipped outside workspace: " + filepathSlash(abs)
			}
			key = relSlash
		}
	}
	return key, abs, true, ""
}

func stripReviewLineRangeSuffix(path string) string {
	path = strings.TrimSpace(path)
	colon := strings.LastIndex(path, ":")
	if colon < 0 || colon == len(path)-1 {
		return path
	}
	suffix := path[colon+1:]
	dash := strings.Index(suffix, "-")
	if dash >= 0 {
		if dash == 0 || dash == len(suffix)-1 {
			return path
		}
		if !allASCIIDigits(suffix[:dash]) || !allASCIIDigits(suffix[dash+1:]) {
			return path
		}
		return strings.TrimSpace(path[:colon])
	}
	if allASCIIDigits(suffix) {
		return strings.TrimSpace(path[:colon])
	}
	return path
}

func allASCIIDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, ch := range text {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func reviewHashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return reviewHashBytes(data), nil
}

func reviewHashString(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func reviewHashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func buildReviewLedgerConsistency(root string, rt *runtimeState, run ReviewRun) ReviewLedgerConsistencyCheck {
	check := ReviewLedgerConsistencyCheck{
		Status:    reviewLedgerConsistencyOK,
		CheckedAt: time.Now(),
	}
	if run.Freshness.Stale {
		check.Blockers = append(check.Blockers, "latest review is stale: "+valueOrDefault(run.Freshness.StaleReason, "review fingerprint changed"))
	}
	if len(run.Gate.BlockingFindings) > 0 {
		check.Blockers = append(check.Blockers, "unresolved review blockers: "+strings.Join(limitStrings(run.Gate.BlockingFindings, 8), ", "))
	}
	if strings.TrimSpace(run.Evidence.VerificationSummary) == "" && reviewRunHasChangeEvidence(run) && run.Target != reviewTargetPlan {
		if run.Evidence.VerificationRequired {
			check.Blockers = append(check.Blockers, "required verification evidence is missing")
		} else {
			check.Warnings = append(check.Warnings, "changed files have no linked verification evidence")
		}
	}
	if rt != nil && rt.session != nil && !strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") {
		currentPaths := runtimeGateChangedPaths(root, rt.session)
		if missing := reviewUnreviewedChangedPaths(run.ChangeSet.ChangedPaths, currentPaths); len(missing) > 0 {
			check.Blockers = append(check.Blockers, "changed paths are not covered by this review: "+strings.Join(limitStrings(missing, 8), ", "))
		}
	}
	if strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write") && run.ApprovalLedger.ReviewGateApproved {
		if !run.ApprovalLedger.DiffPreviewShown || !run.ApprovalLedger.UserWriteApproved {
			message := "pre-write review approval is not write approval; diff preview and explicit user write approval are still required"
			if strings.EqualFold(strings.TrimSpace(run.ReviewerGatePolicy), reviewReviewerGatePolicyMainOnlyFallback) {
				check.Blockers = append(check.Blockers, "main-review fallback requires diff preview before write")
			} else {
				check.Warnings = append(check.Warnings, message)
			}
		}
	}
	check.Blockers = normalizeTaskStateList(check.Blockers, 16)
	check.Warnings = normalizeTaskStateList(check.Warnings, 16)
	if len(check.Blockers) > 0 {
		check.Status = reviewLedgerConsistencyBlocked
	} else if len(check.Warnings) > 0 {
		check.Status = reviewLedgerConsistencyWarning
	}
	return check
}

func buildReviewResumeSanityCheck(root string, rt *runtimeState, run ReviewRun) ReviewResumeSanityCheck {
	check := ReviewResumeSanityCheck{
		Status:       reviewResumeSanityOK,
		EvidenceHash: run.ArtifactIntegrity.EvidenceHash,
		ProposalHash: run.ArtifactIntegrity.ProposalHash,
		CheckedAt:    time.Now(),
	}
	for i := len(run.ActionEnvelopes) - 1; i >= 0; i-- {
		envelope := run.ActionEnvelopes[i]
		if strings.EqualFold(strings.TrimSpace(envelope.Status), "completed") && strings.TrimSpace(envelope.FailureClass) == "" {
			check.LastStableAction = envelope.ActionID + ":" + envelope.ActionType
			break
		}
	}
	if len(run.StateTransitions) > 0 {
		check.NextState = run.StateTransitions[len(run.StateTransitions)-1].To
	}
	if rt != nil && rt.session != nil {
		latest := strings.ToLower(strings.TrimSpace(baseUserQueryText(latestExternalOrUserMessageText(rt.session.Messages))))
		if reason := reviewResumeRequestConflictReason(latest, run); reason != "" {
			check.Status = reviewResumeSanityConflict
			check.ConflictReason = reason
		}
	}
	check.CurrentFileHashMismatches = reviewCurrentFileHashMismatches(root, run.ArtifactIntegrity)
	if len(check.CurrentFileHashMismatches) > 0 && check.Status == reviewResumeSanityOK {
		check.Status = reviewLedgerConsistencyWarning
	}
	return check
}

func reviewResumeRequestConflictReason(latest string, run ReviewRun) string {
	if latest == "" {
		return ""
	}
	normalized := strings.ToLower(latest)
	if containsAny(normalized, "stop", "pause", "cancel", "do not continue", "중단", "멈춰", "취소") {
		return "latest user request asks to stop or pause the previous proposal"
	}
	if strings.Contains(normalized, "only answer") || strings.Contains(latest, "답변만") {
		return "latest user request narrows the response mode"
	}
	return ""
}

func reviewCurrentFileHashMismatches(root string, integrity ReviewArtifactIntegrity) []string {
	if len(integrity.CurrentFileHashes) == 0 {
		return nil
	}
	var mismatches []string
	for path, recorded := range integrity.CurrentFileHashes {
		_, abs, ok, _ := resolveReviewIntegrityPath(root, path)
		if !ok {
			continue
		}
		current, err := reviewHashFile(abs)
		if err != nil {
			mismatches = append(mismatches, path+": unreadable")
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(recorded), strings.TrimSpace(current)) {
			mismatches = append(mismatches, path)
		}
	}
	sort.Strings(mismatches)
	return mismatches
}
