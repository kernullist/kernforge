package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Harness recovery action kinds the operator can take after a pre-final coding
// harness block or a stalled turn. These are user-facing choices, not internal
// model nudges.
const (
	harnessRecoveryActionDisclose        = "disclose"
	harnessRecoveryActionRetryVerify     = "retry_verify"
	harnessRecoveryActionReview          = "review"
	harnessRecoveryActionRepair          = "repair"
	harnessRecoveryActionAnswer          = "answer"
	harnessRecoveryActionWaive           = "waive"
	harnessRecoveryActionModel           = "model"
	harnessRecoveryActionStatus          = "status"
	harnessRecoveryActionDismiss         = "dismiss"
	harnessRecoveryActionContinueEditing = "continue_editing"

	harnessRecoveryCauseHarness             = "harness"
	harnessRecoveryCauseReadChurn           = "read_churn"
	harnessRecoveryCauseNoProgress          = "no_progress"
	harnessRecoveryCauseRepeatedToolCalls   = "repeated_tool_calls"
	harnessRecoveryCauseRepeatedToolFailure = "repeated_tool_failure"
	harnessRecoveryCauseToolLoopLimit       = "tool_loop_limit"
	harnessRecoveryCauseCommentaryOnly      = "commentary_only"
	harnessRecoveryCauseLengthStop          = "length_stop"
	harnessRecoveryCauseContentFilter       = "content_filter"
	harnessRecoveryCauseEmptyStop           = "empty_stop"
	harnessRecoveryCauseFinalGate           = "final_gate"
)

// HarnessBlockedRecovery captures the operator choices available after the
// pre-final coding harness stopped the turn, or after a stall/no-progress stop.
// Stored on the session so /finish, /retry-verify, /continue, numbered choices,
// and status can resume without re-deriving intent.
type HarnessBlockedRecovery struct {
	RecordedAt        time.Time               `json:"recorded_at,omitempty"`
	Cause             string                  `json:"cause,omitempty"`
	CandidateReply    string                  `json:"candidate_reply,omitempty"`
	BlockerTitles     []string                `json:"blocker_titles,omitempty"`
	ExtraBlockers     []string                `json:"extra_blockers,omitempty"`
	Actions           []HarnessRecoveryAction `json:"actions,omitempty"`
	PrimaryCommand    string                  `json:"primary_command,omitempty"`
	AttemptedEditTool bool                    `json:"attempted_edit_tool,omitempty"`
	UnresolvedVerify  bool                    `json:"unresolved_verification,omitempty"`
}

// HarnessRecoveryAction is one concrete next step shown to the operator.
type HarnessRecoveryAction struct {
	ID       string `json:"id,omitempty"`
	Command  string `json:"command,omitempty"`
	ChatHint string `json:"chat_hint,omitempty"`
	TitleEN  string `json:"title_en,omitempty"`
	TitleKO  string `json:"title_ko,omitempty"`
	ReasonEN string `json:"reason_en,omitempty"`
	ReasonKO string `json:"reason_ko,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

func (r *HarnessBlockedRecovery) Normalize() {
	if r == nil {
		return
	}
	r.Cause = strings.TrimSpace(r.Cause)
	if r.Cause == "" {
		r.Cause = harnessRecoveryCauseHarness
	}
	r.CandidateReply = strings.TrimSpace(r.CandidateReply)
	r.BlockerTitles = normalizeTaskStateList(r.BlockerTitles, 12)
	r.ExtraBlockers = normalizeTaskStateList(r.ExtraBlockers, 12)
	r.PrimaryCommand = strings.TrimSpace(r.PrimaryCommand)
	out := make([]HarnessRecoveryAction, 0, len(r.Actions))
	seen := map[string]bool{}
	for _, action := range r.Actions {
		action.ID = strings.TrimSpace(action.ID)
		action.Command = strings.TrimSpace(action.Command)
		action.ChatHint = strings.TrimSpace(action.ChatHint)
		action.Kind = strings.TrimSpace(action.Kind)
		key := action.ID + "|" + action.Command
		if key == "|" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, action)
	}
	r.Actions = out
	if r.PrimaryCommand == "" && len(r.Actions) > 0 {
		r.PrimaryCommand = r.Actions[0].Command
	}
}

func buildHarnessBlockedRecovery(cfg Config, report *CodingHarnessReport, extraBlockers []string, candidateReply string, attemptedEditTool bool, unresolvedVerification bool) HarnessBlockedRecovery {
	recovery := HarnessBlockedRecovery{
		RecordedAt:        time.Now(),
		Cause:             harnessRecoveryCauseHarness,
		CandidateReply:    strings.TrimSpace(candidateReply),
		ExtraBlockers:     append([]string(nil), extraBlockers...),
		AttemptedEditTool: attemptedEditTool,
		UnresolvedVerify:  unresolvedVerification,
	}
	titles := harnessBlockedFindingTitles(report)
	recovery.BlockerTitles = titles
	joined := strings.ToLower(strings.Join(append(append([]string{}, titles...), extraBlockers...), "\n"))

	hasOverclaim := containsAny(joined, "verification claim has no recorded evidence", "검증했다는 주장")
	hasDiscloseGap := containsAny(joined,
		"verification was not run disclosure missing",
		"validation result is missing",
		"changed-file summary is missing",
		"review result is missing",
		"remaining-risk statement is missing",
		"검증 미실행",
	)
	hasStaleOrMissingReview := containsAny(joined, "latest review is stale", "missing review", "review result is missing", "unreviewed")
	hasUnwaivedFindings := containsAny(joined, "unwaived", "blocking findings", "needs_revision", "latest review has")
	hasHardDefect := containsAny(joined,
		"inconsistent bug counts",
		"contradicts the patch transaction",
		"unresolved verification failure",
		"required verification has no outcome",
		"workspace mutation has unknown review scope",
	)

	if hasDiscloseGap || hasOverclaim {
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "finish-disclose",
			Command:  "/finish --disclose",
			ChatHint: localizedText(cfg, `say "finish with verification not run"`, `「검증 미실행으로 끝내줘」라고 입력`),
			TitleEN:  "Finish with honest disclosure",
			TitleKO:  "정직한 공개로 끝내기",
			ReasonEN: "Rewrite the final answer so it does not overclaim verification, then complete the turn.",
			ReasonKO: "검증을 과대주장하지 않도록 최종 답변을 고친 뒤 턴을 완료합니다.",
			Kind:     harnessRecoveryActionDisclose,
		})
	}
	if hasOverclaim || containsAny(joined, "validation result is missing", "verification was not run", "검증 미실행") {
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "retry-verify",
			Command:  "/retry-verify",
			ChatHint: localizedText(cfg, `say "retry verification and finish"`, `「다시 검증하고 마무리해」라고 입력`),
			TitleEN:  "Retry verification, then finish",
			TitleKO:  "검증 다시 실행 후 마무리",
			ReasonEN: "Ask the agent to run a focused verification command and finish with recorded evidence.",
			ReasonKO: "에이전트가 검증을 다시 실행하고, 기록된 근거로 마무리하게 합니다.",
			Kind:     harnessRecoveryActionRetryVerify,
		})
	}
	if hasStaleOrMissingReview || hasUnwaivedFindings {
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "review",
			Command:  "/review",
			ChatHint: localizedText(cfg, "run /review", "/review 실행"),
			TitleEN:  "Refresh review coverage",
			TitleKO:  "리뷰 다시 실행",
			ReasonEN: "Create or refresh a review so the gate is no longer stale or missing.",
			ReasonKO: "리뷰를 새로 만들어 게이트의 stale/missing 상태를 해소합니다.",
			Kind:     harnessRecoveryActionReview,
		})
	}
	if hasUnwaivedFindings {
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "waive",
			Command:  "/review waive <finding-id> --reason <text>",
			ChatHint: localizedText(cfg, "waive a false-positive finding", "오탐 finding을 waive"),
			TitleEN:  "Waive a false-positive finding",
			TitleKO:  "오탐 finding 예외 처리",
			ReasonEN: "Only for confirmed false positives. Requires a finding id and reason.",
			ReasonKO: "확인된 오탐에만 사용합니다. finding id와 사유가 필요합니다.",
			Kind:     harnessRecoveryActionWaive,
		})
	}
	if hasHardDefect || (!hasDiscloseGap && !hasOverclaim && !hasStaleOrMissingReview) {
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "repair",
			Command:  "/continue",
			ChatHint: localizedText(cfg, `say "fix the remaining blockers"`, `「남은 blocker를 수정해」라고 입력`),
			TitleEN:  "Resume repair in chat",
			TitleKO:  "채팅에서 수정 재개",
			ReasonEN: "Continue editing to clear real code or verification defects.",
			ReasonKO: "실제 코드/검증 결함을 고치도록 수정을 이어갑니다.",
			Kind:     harnessRecoveryActionRepair,
		})
	}
	if len(recovery.Actions) == 0 {
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "status",
			Command:  "/status detail",
			ChatHint: localizedText(cfg, "inspect /status detail", "/status detail 확인"),
			TitleEN:  "Inspect gate details",
			TitleKO:  "게이트 상세 확인",
			ReasonEN: "See the full blocker list and current ledger state.",
			ReasonKO: "전체 blocker 목록과 현재 ledger 상태를 확인합니다.",
			Kind:     harnessRecoveryActionRepair,
		})
	}
	recovery.Normalize()
	return recovery
}

func harnessBlockedFindingTitles(report *CodingHarnessReport) []string {
	if report == nil {
		return nil
	}
	copyReport := *report
	copyReport.Normalize()
	titles := make([]string, 0)
	for _, finding := range copyReport.allFindings() {
		if !strings.EqualFold(strings.TrimSpace(finding.Severity), "blocker") {
			continue
		}
		if title := strings.TrimSpace(finding.Title); title != "" {
			titles = append(titles, title)
		}
	}
	return normalizeTaskStateList(titles, 12)
}

func renderHarnessBlockedRecoveryReply(cfg Config, report *CodingHarnessReport, extraBlockers []string, recovery HarnessBlockedRecovery) string {
	korean := localePrefersKorean(cfg)
	recovery.Normalize()
	header := localizedText(cfg,
		"Completion is blocked until you choose a next step.",
		"다음 단계를 고르기 전까지 완료가 차단되어 있습니다.")
	switch recovery.Cause {
	case harnessRecoveryCauseReadChurn:
		header = localizedText(cfg,
			"I stopped after re-reading the same files without progress. Choose how to continue.",
			"같은 파일을 진전 없이 반복해서 읽어 중단했습니다. 이어서 할 일을 고르세요.")
	case harnessRecoveryCauseNoProgress:
		header = localizedText(cfg,
			"No-progress guard stopped this turn. Choose how to continue.",
			"무진행 가드로 이번 턴을 중단했습니다. 이어서 할 일을 고르세요.")
	case harnessRecoveryCauseRepeatedToolCalls:
		header = localizedText(cfg,
			"I stopped after repeating the same tool calls. Choose how to continue.",
			"같은 도구 호출을 반복해서 중단했습니다. 이어서 할 일을 고르세요.")
	case harnessRecoveryCauseRepeatedToolFailure:
		header = localizedText(cfg,
			"I stopped after the same tool failure repeated. Choose how to continue.",
			"같은 도구 실패가 반복되어 중단했습니다. 이어서 할 일을 고르세요.")
	case harnessRecoveryCauseToolLoopLimit:
		header = localizedText(cfg,
			"I stopped after hitting the tool-loop limit. Choose how to continue.",
			"도구 루프 한도에 걸려 중단했습니다. 이어서 할 일을 고르세요.")
	case harnessRecoveryCauseCommentaryOnly:
		header = localizedText(cfg,
			"I stopped after repeated commentary-only replies. Choose how to continue.",
			"진행 설명만 반복되어 중단했습니다. 이어서 할 일을 고르세요.")
	case harnessRecoveryCauseLengthStop:
		header = localizedText(cfg,
			"I stopped after repeated output-length cuts. Choose how to continue.",
			"출력 길이 제한이 반복되어 중단했습니다. 이어서 할 일을 고르세요.")
	case harnessRecoveryCauseContentFilter:
		header = localizedText(cfg,
			"I stopped after the provider content filter blocked replies. Choose how to continue.",
			"제공자 content filter가 답변을 막아 중단했습니다. 이어서 할 일을 고르세요.")
	case harnessRecoveryCauseEmptyStop:
		header = localizedText(cfg,
			"I stopped after repeated empty model replies. Choose how to continue.",
			"빈 모델 응답이 반복되어 중단했습니다. 이어서 할 일을 고르세요.")
	case harnessRecoveryCauseFinalGate:
		header = localizedText(cfg,
			"I stopped because confirmation is still needed before finishing. Choose how to continue.",
			"완료를 마무리하려면 확인이 필요해 중단했습니다. 이어서 할 일을 고르세요.")
	}
	lines := []string{header}
	blockers := make([]string, 0)
	seen := map[string]bool{}
	addBlocker := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" || len(blockers) >= finalHarnessMaxFindings {
			return
		}
		key := strings.ToLower(text)
		if seen[key] {
			return
		}
		seen[key] = true
		blockers = append(blockers, "- "+text)
	}
	if report != nil {
		copyReport := *report
		copyReport.Normalize()
		for _, finding := range copyReport.allFindings() {
			if !strings.EqualFold(strings.TrimSpace(finding.Severity), "blocker") {
				continue
			}
			title := firstNonBlankString(finding.Title, "coding harness blocker")
			if detail := strings.TrimSpace(finding.Detail); detail != "" {
				addBlocker(title + ": " + detail)
			} else {
				addBlocker(title)
			}
		}
	}
	for _, extra := range extraBlockers {
		addBlocker(extra)
	}
	if len(blockers) > 0 {
		lines = append(lines, "", localizedText(cfg, "Remaining blockers:", "남은 차단 항목:"))
		lines = append(lines, blockers...)
	}
	if len(recovery.Actions) > 0 {
		lines = append(lines, "", localizedText(cfg, "Choose a next step:", "다음 단계를 고르세요:"))
		for i, action := range recovery.Actions {
			title := action.TitleEN
			reason := action.ReasonEN
			if korean {
				title = firstNonBlankString(action.TitleKO, action.TitleEN)
				reason = firstNonBlankString(action.ReasonKO, action.ReasonEN)
			}
			lines = append(lines, fmt.Sprintf("%d) %s", i+1, firstNonBlankString(title, action.Command)))
			if reason != "" {
				lines = append(lines, "   "+reason)
			}
		}
		lines = append(lines, "",
			localizedText(cfg,
				"An interactive prompt will ask for a number. You can also type 1, 2, ... on the next turn.",
				"번호 선택 프롬프트가 열립니다. 다음 턴에 1, 2, ...만 입력해도 됩니다."))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func preFinalCodingHarnessBlockedReply(report *CodingHarnessReport, extraBlockers ...string) string {
	return preFinalCodingHarnessBlockedReplyWithRecovery(Config{}, report, HarnessBlockedRecovery{}, extraBlockers...)
}

func preFinalCodingHarnessBlockedReplyWithRecovery(cfg Config, report *CodingHarnessReport, recovery HarnessBlockedRecovery, extraBlockers ...string) string {
	if len(recovery.Actions) == 0 {
		recovery = buildHarnessBlockedRecovery(cfg, report, extraBlockers, recovery.CandidateReply, recovery.AttemptedEditTool, recovery.UnresolvedVerify)
	}
	return renderHarnessBlockedRecoveryReply(cfg, report, extraBlockers, recovery)
}

func (a *Agent) recordHarnessBlockedRecovery(candidateReply string, report *CodingHarnessReport, extraBlockers []string, attemptedEditTool bool, unresolvedVerification bool) HarnessBlockedRecovery {
	recovery := buildHarnessBlockedRecovery(a.Config, report, extraBlockers, candidateReply, attemptedEditTool, unresolvedVerification)
	if a != nil && a.Session != nil {
		a.Session.PendingHarnessBlockedRecovery = &recovery
		if a.Session.LastFinalAnswerCorrection != nil && a.Session.LastFinalAnswerCorrection.Contract != nil {
			a.Session.LastFinalAnswerCorrection.Contract.ManualRecoveryNeeded = true
			if recovery.PrimaryCommand != "" {
				a.Session.LastFinalAnswerCorrection.Contract.NextCommand = recovery.PrimaryCommand
			}
		}
	}
	return recovery
}

func (a *Agent) clearHarnessBlockedRecovery() {
	if a == nil || a.Session == nil {
		return
	}
	a.Session.PendingHarnessBlockedRecovery = nil
}

func harnessBlockedRecoveryPrimaryCommand(session *Session) string {
	if session == nil || session.PendingHarnessBlockedRecovery == nil {
		return ""
	}
	recovery := *session.PendingHarnessBlockedRecovery
	recovery.Normalize()
	return recovery.PrimaryCommand
}

func looksLikeHarnessDiscloseFinishIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"finish with verification not run",
		"finish with disclosure",
		"disclose and finish",
		"검증 미실행으로 끝내",
		"검증 없이 끝내",
		"공개로 끝내",
		"정직하게 끝내",
		"미실행으로 마무리",
	)
}

func looksLikeHarnessRetryVerifyIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"retry verification",
		"retry verify",
		"run verification again",
		"verify and finish",
		"다시 검증",
		"검증하고 마무리",
		"검증 다시",
		"재검증",
	)
}

func looksLikeHarnessRepairContinueIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if looksLikeHarnessDiscloseFinishIntent(text) || looksLikeHarnessRetryVerifyIntent(text) {
		return false
	}
	return containsAny(lower,
		"fix the remaining blockers",
		"fix remaining blockers",
		"continue repair",
		"resume repair",
		"keep repairing",
		"/continue",
		"남은 blocker",
		"blocker를 수정",
		"계속 수정",
		"수정 재개",
	) || strings.EqualFold(strings.TrimSpace(text), "continue") || strings.EqualFold(strings.TrimSpace(text), "계속")
}

// finishHarnessBlockedWithDisclosure rewrites the blocked candidate reply with
// honest disclosures and, when the harness/ledger accept it, finalizes the turn.
func (a *Agent) finishHarnessBlockedWithDisclosure() (string, error) {
	if a == nil || a.Session == nil {
		return "", fmt.Errorf("no active session")
	}
	recovery := a.Session.PendingHarnessBlockedRecovery
	if recovery == nil {
		return "", fmt.Errorf("no blocked harness recovery is pending; run a task that hits the pre-final harness first")
	}
	candidate := strings.TrimSpace(recovery.CandidateReply)
	if candidate == "" {
		candidate = latestAssistantFinalCandidateText(a.Session)
	}
	if candidate == "" {
		return "", fmt.Errorf("no candidate final answer is available to disclose")
	}
	attempted := recovery.AttemptedEditTool
	unresolved := recovery.UnresolvedVerify
	healed := retractVerificationOverclaimForDisclosure(candidate)
	healed = a.completeFinalAnswerDisclosures(healed, attempted)
	if healedSkipped, recheck, ok := a.healSkippedVerificationDisclosure(healed, codingHarnessSourcePrompt(a.Session), attempted, unresolved); ok {
		healed = healedSkipped
		_ = recheck
	}
	if healedDoc, recheck, ok := a.healDocumentArtifactDisclosure(healed, attempted, unresolved); ok {
		healed = healedDoc
		_ = recheck
	}
	if healedOnly, recheck, ok := a.healFinalAnswerOnlyDisclosures(healed, attempted, unresolved); ok {
		healed = healedOnly
		_ = recheck
	}
	report := a.buildCodingHarnessReport(healed, attempted, unresolved)
	a.Session.LastCodingHarnessReport = &report
	a.Session.LastTestImpactReport = &report.TestImpact
	a.Session.LastJobSupervisorReport = &report.JobSupervisor
	ledger := a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
	if !report.Approved {
		recoveryUpdate := a.recordHarnessBlockedRecovery(healed, &report, nonHarnessLedgerBlockers(ledger), attempted, unresolved)
		return preFinalCodingHarnessBlockedReplyWithRecovery(a.Config, &report, recoveryUpdate, nonHarnessLedgerBlockers(ledger)...),
			fmt.Errorf("disclosure finish still blocked by the coding harness")
	}
	if runtimeGateBlocksAction(ledger) {
		// Disclosure fixed the harness, but the ledger still needs operator action
		// (usually a fresh /review). Keep recovery pending with updated actions.
		recoveryUpdate := a.recordHarnessBlockedRecovery(healed, &report, nonHarnessLedgerBlockers(ledger), attempted, unresolved)
		msg := localizedText(a.Config,
			"Disclosure was applied, but the runtime gate is still blocked.\n\n"+renderHarnessBlockedRecoveryReply(a.Config, &report, nonHarnessLedgerBlockers(ledger), recoveryUpdate),
			"공개 문구는 반영했지만 runtime gate가 아직 차단되어 있습니다.\n\n"+renderHarnessBlockedRecoveryReply(a.Config, &report, nonHarnessLedgerBlockers(ledger), recoveryUpdate),
		)
		return msg, fmt.Errorf("disclosure finish still blocked by the runtime gate")
	}
	a.clearHarnessBlockedRecovery()
	a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: healed})
	a.markFinalAnswerCorrectionAccepted()
	a.finalizeTaskStateOnAcceptedFinalAnswer(healed, unresolved)
	a.finalizePatchTransactionOnReturn()
	a.finalizeEditLoopOnReturn(healed, unresolved)
	a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
	if a.Store != nil {
		if err := a.Store.Save(a.Session); err != nil {
			return healed, err
		}
	}
	return healed, nil
}

func retractVerificationOverclaimForDisclosure(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return reply
	}
	if !replyClaimsVerificationSuccess(reply) {
		return reply
	}
	if replyMentionsVerificationNotRun(reply) || replyMentionsVerificationBlocker(reply) {
		return reply
	}
	return reply + "\n\nValidation: verification was not run."
}

func latestAssistantFinalCandidateText(session *Session) string {
	if session == nil {
		return ""
	}
	for i := len(session.Messages) - 1; i >= 0; i-- {
		msg := session.Messages[i]
		if msg.Role != "assistant" || msg.Internal {
			continue
		}
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		// Skip the blocked recovery card itself.
		if strings.Contains(text, "Completion is blocked until you choose a next step.") ||
			strings.Contains(text, "다음 단계를 고르기 전까지 완료가 차단") ||
			strings.Contains(text, "I stopped after re-reading the same files without progress") ||
			strings.Contains(text, "같은 파일을 진전 없이 반복해서 읽어 중단") ||
			strings.Contains(text, "No-progress guard stopped this turn") ||
			strings.Contains(text, "무진행 가드로 이번 턴을 중단") ||
			strings.Contains(text, "I stopped after repeating the same tool calls") ||
			strings.Contains(text, "같은 도구 호출을") ||
			strings.Contains(text, "I stopped after the same tool failure repeated") ||
			strings.Contains(text, "같은 도구 실패가 반복") ||
			strings.Contains(text, "I stopped after hitting the tool-loop limit") ||
			strings.Contains(text, "도구 루프 한도에 걸려") ||
			strings.Contains(text, "I stopped after repeated commentary-only") ||
			strings.Contains(text, "진행 설명만 반복") ||
			strings.Contains(text, "I stopped after the model repeatedly hit the output length") ||
			strings.Contains(text, "출력 길이 제한에 반복") ||
			strings.Contains(text, "I stopped after the provider content filter") ||
			strings.Contains(text, "content filter가 답변을 막아") ||
			strings.Contains(text, "I stopped after the model returned repeated empty") ||
			strings.Contains(text, "빈 응답을 반복해서 반환") ||
			strings.Contains(text, "I stopped because the final gate is still blocked") ||
			strings.Contains(text, "최종 게이트가 여전히 막혀") ||
			strings.Contains(text, "Pre-final coding harness is still blocking completion") ||
			strings.Contains(text, "Choose a next step:") ||
			strings.Contains(text, "다음 단계를 고르세요:") ||
			strings.Contains(text, "What you can do:") ||
			strings.Contains(text, "할 수 있는 일:") {
			continue
		}
		return text
	}
	return ""
}

func retryVerifyRecoveryPrompt(cfg Config) string {
	return localizedText(cfg,
		"The previous turn was blocked because verification evidence was missing or overclaimed. Run a focused verification now (for this workspace: prefer python -m py_compile on changed Python files, or the project test/build command). Then provide the final answer using only recorded evidence. If verification cannot run, explicitly say verification was not run.",
		"이전 턴은 검증 근거가 없거나 과대주장되어 차단되었습니다. 지금 변경된 파일에 대해 focused 검증을 실행하세요(이 워크스페이스에서는 변경된 Python 파일에 python -m py_compile, 또는 프로젝트 테스트/빌드 명령). 그다음 기록된 근거만으로 최종 답변을 작성하세요. 검증을 실행할 수 없으면 검증 미실행을 명시하세요.",
	)
}

func repairBlockedRecoveryPrompt(cfg Config) string {
	return localizedText(cfg,
		"The previous turn was blocked by remaining harness/runtime-gate blockers. Continue repairing the concrete blockers. Do not claim completion until the blockers are cleared or the operator chooses /finish --disclose or /review.",
		"이전 턴은 harness/runtime-gate blocker 때문에 차단되었습니다. 구체적인 blocker를 계속 수정하세요. blocker가 해소되거나 사용자가 /finish --disclose 또는 /review를 고르기 전에는 완료를 주장하지 마세요.",
	)
}

// stallRepairEvidence captures concrete defects already observed in the session
// so stall continue can force an edit instead of another read loop.
type stallRepairEvidence struct {
	Detail string
	Files  []string
	Kind   string // "syntax", "tool_error", or ""
}

func (e stallRepairEvidence) empty() bool {
	return strings.TrimSpace(e.Detail) == "" && len(e.Files) == 0
}

func looksLikeCompileOrSyntaxFailure(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if containsAny(lower, "syntaxerror", "invalid syntax", "indentationerror", "taberror") {
		return true
	}
	if containsAny(lower, "py_compile", "tsc --noemit", "javac ") &&
		containsAny(lower, "error", "failed", "exit status", "traceback") {
		return true
	}
	return false
}

// toolTextIsPolicyNotExecuted reports harness policy deferrals/blocks that are
// NOT concrete code defects. Treating them as repair evidence caused analysis
// turns to show "fix that defect" after NOT_EXECUTED: local tools deferred...
func toolTextIsPolicyNotExecuted(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "not_executed:") {
		return true
	}
	return containsAny(lower,
		"deferred until",
		"blocked for this local",
		"read-only analysis",
		"disabled in plan mode",
		"permission denied",
		"tool exposure blocked",
	)
}

func collectRecentRepairEvidence(session *Session, limit int) stallRepairEvidence {
	if session == nil || limit <= 0 {
		return stallRepairEvidence{}
	}
	files := make([]string, 0, 4)
	seenFiles := map[string]bool{}
	addFile := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		key := strings.ToLower(filepath.ToSlash(path))
		if seenFiles[key] {
			return
		}
		seenFiles[key] = true
		files = append(files, path)
	}
	var detail string
	kind := ""
	for i := len(session.Messages) - 1; i >= 0 && limit > 0; i-- {
		msg := session.Messages[i]
		if msg.Role != "tool" {
			continue
		}
		limit--
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		// Policy blocks are harness routing, not defects to "fix with an edit".
		if toolTextIsPolicyNotExecuted(text) {
			continue
		}
		if msg.IsError || looksLikeCompileOrSyntaxFailure(text) {
			if detail == "" {
				detail = compactPromptSection(text, 420)
			}
			if looksLikeCompileOrSyntaxFailure(text) {
				kind = "syntax"
			} else if kind == "" {
				kind = "tool_error"
			}
			if path := extractPathFromToolErrorText(text); path != "" {
				addFile(path)
			}
			if name := strings.TrimSpace(msg.ToolName); name == "run_shell" || name == "read_file" {
				if path := extractQuotedPathHint(text); path != "" {
					addFile(path)
				}
			}
		}
	}
	if session.PendingHarnessBlockedRecovery != nil {
		for _, path := range session.PendingHarnessBlockedRecovery.BlockerTitles {
			addFile(path)
		}
	}
	return stallRepairEvidence{Detail: detail, Files: normalizeTaskStateList(files, 8), Kind: kind}
}

func extractPathFromToolErrorText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// Common py_compile / python forms: File "merge_mp4_gui.py", line 747
	if idx := strings.Index(strings.ToLower(text), `file "`); idx >= 0 {
		rest := text[idx+6:]
		if end := strings.Index(rest, `"`); end > 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	if idx := strings.Index(strings.ToLower(text), "file '"); idx >= 0 {
		rest := text[idx+6:]
		if end := strings.Index(rest, "'"); end > 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return extractQuotedPathHint(text)
}

func extractQuotedPathHint(text string) string {
	lower := strings.ToLower(text)
	for _, marker := range []string{"py_compile ", "in ", "path=", "path:"} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			rest := strings.TrimSpace(text[idx+len(marker):])
			rest = strings.Trim(rest, "`\"'")
			if end := strings.IndexAny(rest, " \n\r\t:]"); end > 0 {
				rest = rest[:end]
			}
			rest = strings.Trim(rest, "`\"'")
			if strings.Contains(rest, ".") || strings.ContainsAny(rest, `/\`) {
				return rest
			}
		}
	}
	return ""
}

func syntaxFailureForceEditGuidance(cfg Config, evidence stallRepairEvidence) string {
	var b strings.Builder
	b.WriteString(localizedText(cfg,
		"A compile/syntax failure is already on record. Do not keep reading the same file. Your next tool call must be write_file, replace_in_file, or apply_patch that fixes the invalid syntax, then re-run the compile check. Do not ask the user to clarify the product request until that syntax defect is fixed.",
		"컴파일/문법 실패가 이미 기록되어 있습니다. 같은 파일을 계속 읽지 마세요. 다음 도구 호출은 잘못된 문법을 고치는 write_file, replace_in_file, 또는 apply_patch여야 하고, 그다음 컴파일 검사를 다시 실행하세요. 그 문법 결함이 고쳐지기 전에는 사용자에게 제품 요청을 다시 묻지 마세요."))
	if evidence.Detail != "" {
		fmt.Fprintf(&b, "\n\n%s\n%s", localizedText(cfg, "Recorded failure:", "기록된 실패:"), evidence.Detail)
	}
	if len(evidence.Files) > 0 {
		fmt.Fprintf(&b, "\n\n%s\n- %s", localizedText(cfg, "Target file(s):", "대상 파일:"), strings.Join(evidence.Files, "\n- "))
	}
	return strings.TrimSpace(b.String())
}

func stallContinueEditBiasGuidance(cfg Config, evidence stallRepairEvidence, files []string) string {
	var b strings.Builder
	b.WriteString(localizedText(cfg,
		"Operator continue is active. Your FIRST tool call must be write_file, replace_in_file, or apply_patch. Do not call read_file on already-inspected files first. If a compile/syntax failure is recorded, fix that defect immediately.",
		"운영자 continue가 활성화되어 있습니다. 첫 도구 호출은 write_file, replace_in_file, 또는 apply_patch여야 합니다. 이미 검사한 파일에 대해 먼저 read_file을 호출하지 마세요. 컴파일/문법 실패가 기록되어 있으면 그 결함부터 바로 고치세요."))
	if evidence.Detail != "" {
		fmt.Fprintf(&b, "\n\n%s\n%s", localizedText(cfg, "Recorded failure:", "기록된 실패:"), evidence.Detail)
	}
	targets := append([]string{}, evidence.Files...)
	for _, path := range files {
		targets = append(targets, path)
	}
	targets = normalizeTaskStateList(targets, 8)
	if len(targets) > 0 {
		fmt.Fprintf(&b, "\n\n%s\n- %s", localizedText(cfg, "Already-inspected files (edit, do not re-read first):", "이미 검사한 파일(먼저 다시 읽지 말고 수정):"), strings.Join(targets, "\n- "))
	}
	return strings.TrimSpace(b.String())
}

func stallContinueRecoveryPrompt(cfg Config, cause string) string {
	switch strings.TrimSpace(cause) {
	case harnessRecoveryCauseReadChurn:
		return localizedText(cfg,
			"The previous turn stopped because the same files were re-read without progress. Continue the user's original task now. Prefer write_file, replace_in_file, or apply_patch over more reads of already-seen files. If the last shell or compile output showed invalid syntax, fix that concrete defect first with a focused edit, then finish with honest verification status.",
			"이전 턴은 같은 파일을 진전 없이 반복해서 읽어 중단되었습니다. 지금 사용자의 원래 작업을 이어서 진행하세요. 이미 읽은 파일을 다시 읽기보다 write_file, replace_in_file, apply_patch로 수정하세요. 마지막 shell/compile 출력에 문법 문제가 있으면 그 위치부터 focused 수정으로 고치고, 검증 상태는 정직하게 밝히며 마무리하세요.",
		)
	case harnessRecoveryCauseRepeatedToolCalls:
		return localizedText(cfg,
			"The previous turn stopped because the same tool calls repeated without progress. Continue the user's original task now with a different next step. Do not repeat the same tool sequence; edit, verify, or give a final answer based on evidence already gathered.",
			"이전 턴은 같은 도구 호출이 진전 없이 반복되어 중단되었습니다. 지금 다른 다음 단계로 사용자의 원래 작업을 이어가세요. 같은 도구 시퀀스를 반복하지 말고, 이미 모은 근거로 수정·검증하거나 최종 답변을 하세요.",
		)
	case harnessRecoveryCauseRepeatedToolFailure:
		return localizedText(cfg,
			"The previous turn stopped because the same tool failure repeated. Continue the user's original task now with a different approach or inputs. Do not retry the exact failing call unchanged.",
			"이전 턴은 같은 도구 실패가 반복되어 중단되었습니다. 지금 다른 접근이나 입력으로 사용자의 원래 작업을 이어가세요. 실패했던 호출을 그대로 다시 시도하지 마세요.",
		)
	case harnessRecoveryCauseToolLoopLimit:
		return localizedText(cfg,
			"The previous turn stopped after hitting the tool-loop limit. Continue the user's original task now with a short plan: either one focused edit/verification step or a final answer from evidence already gathered. Avoid more exploratory tool churn.",
			"이전 턴은 도구 루프 한도에 걸려 중단되었습니다. 지금 짧은 계획으로 사용자의 원래 작업을 이어가세요: focused 수정/검증 한 단계, 또는 이미 모은 근거로 최종 답변. 탐색용 도구 호출을 더 늘리지 마세요.",
		)
	case harnessRecoveryCauseCommentaryOnly, harnessRecoveryCauseEmptyStop, harnessRecoveryCauseLengthStop, harnessRecoveryCauseContentFilter:
		return localizedText(cfg,
			"The previous turn stopped before producing a usable final answer. Continue the user's original task now. Prefer a concrete tool action or a concise final answer; do not repeat empty or commentary-only replies.",
			"이전 턴은 쓸 만한 최종 답변을 내기 전에 중단되었습니다. 지금 사용자의 원래 작업을 이어가세요. 구체적 도구 행동이나 간결한 최종 답변을 우선하고, 빈 응답이나 진행 설명만 반복하지 마세요.",
		)
	case harnessRecoveryCauseFinalGate:
		return localizedText(cfg,
			"The previous turn stopped because confirmation was still needed before finishing. Continue repairing concrete blockers for the user's original task. Do not claim completion until blockers are cleared or the operator chooses dismiss/review.",
			"이전 턴은 완료 전에 확인이 필요해 중단되었습니다. 사용자의 원래 작업에 대한 구체적 차단 항목을 계속 수정하세요. 차단이 해소되거나 사용자가 무시/리뷰를 고르기 전에는 완료를 주장하지 마세요.",
		)
	default:
		return localizedText(cfg,
			"The previous turn stopped because it made no workspace progress. Continue the user's original task now with a concrete edit or verification step. Do not keep re-reading the same files without changing anything.",
			"이전 턴은 작업공간에 진전이 없어 중단되었습니다. 지금 구체적 수정이나 검증 단계로 사용자의 원래 작업을 이어가세요. 아무것도 바꾸지 않은 채 같은 파일만 다시 읽지 마세요.",
		)
	}
}

func buildStallContinueRecoveryPrompt(cfg Config, session *Session, cause string) string {
	return buildStallContinueRecoveryPromptForAction(cfg, session, cause, harnessRecoveryActionRepair)
}

// buildStallContinueRecoveryPromptForAction biases the continue injection by
// recovery kind. Analysis answer mode must NOT force write_file-first edit bias.
func buildStallContinueRecoveryPromptForAction(cfg Config, session *Session, cause string, actionKind string) string {
	original := preservableSessionAcceptancePrompt(session)
	analysisOnly := requestLooksLikeAnalysisOnlyTurn(original) ||
		actionKind == harnessRecoveryActionAnswer ||
		actionKind == harnessRecoveryActionReview
	evidence := collectRecentRepairEvidence(session, 24)
	files := evidence.Files
	if session != nil && session.PendingHarnessBlockedRecovery != nil {
		files = normalizeTaskStateList(append(append([]string{}, files...), session.PendingHarnessBlockedRecovery.BlockerTitles...), 8)
	}
	var b strings.Builder
	if analysisOnly {
		b.WriteString(localizedText(cfg,
			"The previous turn stopped after unproductive re-reads. Continue the user's original analysis/reporting task. Prefer a final answer from evidence already gathered. If one missing fact remains, inspect a different path once — do not re-read the same file set, and do not start unrelated code repairs.",
			"이전 턴은 진전 없는 반복 읽기 후 중단되었습니다. 사용자의 원래 분석/보고 작업을 이어가세요. 이미 모은 근거로 최종 답변을 우선하세요. 정말 빠진 사실 하나가 있으면 다른 경로를 한 번만 보고, 같은 파일 집합을 다시 읽거나 관련 없는 코드 수정으로 전환하지 마세요.",
		))
	} else {
		b.WriteString(stallContinueRecoveryPrompt(cfg, cause))
		b.WriteString("\n\n")
		b.WriteString(stallContinueEditBiasGuidance(cfg, evidence, files))
	}
	if original != "" {
		b.WriteString("\n\n")
		b.WriteString(localizedText(cfg,
			"Original user request to continue:\n"+original,
			"이어서 수행할 원래 사용자 요청:\n"+original))
	}
	return strings.TrimSpace(b.String())
}

func buildStallBlockedRecovery(cfg Config, cause string, summary string, files []string) HarnessBlockedRecovery {
	return buildStallBlockedRecoveryWithMode(cfg, nil, cause, summary, files, false)
}

// buildStallBlockedRecoveryWithMode builds operator choices after a stall.
// analysisOnly matches industry agents: analysis/read turns offer "answer with
// findings" first, not "continue repairing".
func buildStallBlockedRecoveryWithMode(cfg Config, session *Session, cause string, summary string, files []string, analysisOnly bool) HarnessBlockedRecovery {
	cause = strings.TrimSpace(cause)
	if cause == "" {
		cause = harnessRecoveryCauseNoProgress
	}
	if cause == harnessRecoveryCauseFinalGate {
		return buildFinalGateBlockedRecovery(cfg, session, summary, files)
	}
	recovery := HarnessBlockedRecovery{
		RecordedAt:     time.Now(),
		Cause:          cause,
		CandidateReply: strings.TrimSpace(summary),
		BlockerTitles:  normalizeTaskStateList(files, 8),
	}
	if analysisOnly {
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "answer-findings",
			Command:  "/continue",
			ChatHint: localizedText(cfg, `say "write it up from what you found"`, `「지금까지 찾은 걸로 작성해」라고 입력`),
			TitleEN:  "Write the deliverable now",
			TitleKO:  "지금까지 근거로 문서/답변 작성",
			ReasonEN: "Stop re-reading and write the analysis answer or document from evidence already gathered.",
			ReasonKO: "반복 읽기를 멈추고 이미 모은 근거로 분석 답변 또는 문서를 작성합니다.",
			Kind:     harnessRecoveryActionAnswer,
		})
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "continue-inspect",
			Command:  "/continue",
			ChatHint: localizedText(cfg, `say "keep investigating"`, `「더 조사해」라고 입력`),
			TitleEN:  "Keep investigating",
			TitleKO:  "조사 계속",
			ReasonEN: "Resume local inspection with a different file or tool, not more re-reads of the same set.",
			ReasonKO: "같은 파일을 다시 읽기보다 다른 파일/도구로 조사를 이어갑니다.",
			// Same continue path as answer (no /review harness), but prompt allows one more inspect step.
			Kind: harnessRecoveryActionAnswer,
		})
	} else {
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "continue-repair",
			Command:  "/continue",
			ChatHint: localizedText(cfg, `say "continue fixing"`, `「이어서 수정해」라고 입력`),
			TitleEN:  "Continue repairing now",
			TitleKO:  "지금 이어서 수정",
			ReasonEN: "Resume with focused edits instead of more re-reads.",
			ReasonKO: "반복 읽기 대신 focused 수정으로 재개합니다.",
			Kind:     harnessRecoveryActionRepair,
		})
	}
	recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
		ID:       "switch-model",
		Command:  "/model",
		ChatHint: localizedText(cfg, "open /model", "/model 열기"),
		TitleEN:  "Retry with another model",
		TitleKO:  "다른 모델로 재시도",
		ReasonEN: "Open model routing if the current provider/model is stalling or rate-limited.",
		ReasonKO: "현재 제공자/모델이 멈추거나 rate-limit이면 모델 라우팅을 엽니다.",
		Kind:     harnessRecoveryActionModel,
	})
	recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
		ID:       "status-detail",
		Command:  "/status detail",
		ChatHint: localizedText(cfg, "inspect /status detail", "/status detail 확인"),
		TitleEN:  "Inspect details",
		TitleKO:  "자세히 보기",
		ReasonEN: "See blockers, verification, and ledger state before deciding.",
		ReasonKO: "결정 전에 차단 항목, 검증, 상태 상세를 확인합니다.",
		Kind:     harnessRecoveryActionStatus,
	})
	recovery.Normalize()
	return recovery
}

// buildFinalGateBlockedRecovery builds Everyday-facing choices when completion
// is blocked. Slash commands stay as internal Command fields; titles avoid
// operator jargon.
func buildFinalGateBlockedRecovery(cfg Config, session *Session, summary string, files []string) HarnessBlockedRecovery {
	recovery := HarnessBlockedRecovery{
		RecordedAt:     time.Now(),
		Cause:          harnessRecoveryCauseFinalGate,
		CandidateReply: strings.TrimSpace(summary),
		BlockerTitles:  normalizeTaskStateList(files, 8),
		PrimaryCommand: "/review",
	}
	var ledger RuntimeGateLedger
	if session != nil && session.RuntimeGateLedger != nil {
		ledger = *session.RuntimeGateLedger
		if len(recovery.BlockerTitles) == 0 {
			recovery.BlockerTitles = normalizeTaskStateList(ledger.Blockers, 8)
		}
		if len(recovery.ExtraBlockers) == 0 {
			recovery.ExtraBlockers = normalizeTaskStateList(ledger.Blockers, 6)
		}
	}
	offerClear := runtimeGateShouldOfferClear(session, ledger)

	recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
		ID:       "refresh-review",
		Command:  "/review",
		ChatHint: localizedText(cfg, `say "refresh the review"`, `「리뷰 갱신해」라고 입력`),
		TitleEN:  "Refresh review and continue",
		TitleKO:  "리뷰 갱신하고 계속",
		ReasonEN: "Run a fresh review covering the current changes, then resume.",
		ReasonKO: "현재 변경을 포함한 최신 리뷰를 돌린 뒤 이어갑니다.",
		Kind:     harnessRecoveryActionReview,
	})
	if offerClear {
		recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
			ID:       "dismiss-baggage",
			Command:  "/gate clear",
			ChatHint: localizedText(cfg, `say "ignore for now"`, `「이번만 무시해」라고 입력`),
			TitleEN:  "Dismiss this once and continue",
			TitleKO:  "이번만 무시하고 계속",
			ReasonEN: "Dismiss this session's review for completion/git write. Review files stay on disk.",
			ReasonKO: "이 세션 리뷰만 게이트에서 해제합니다. 리뷰 파일은 유지됩니다.",
			Kind:     harnessRecoveryActionDismiss,
		})
	}
	recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
		ID:       "status-detail",
		Command:  "/status detail",
		ChatHint: localizedText(cfg, "inspect details", "자세히 보기"),
		TitleEN:  "Show details",
		TitleKO:  "자세히 보기",
		ReasonEN: "Inspect why completion is blocked before choosing another option.",
		ReasonKO: "다른 선택 전에 완료가 막힌 이유를 확인합니다.",
		Kind:     harnessRecoveryActionStatus,
	})
	recovery.Actions = append(recovery.Actions, HarnessRecoveryAction{
		ID:       "continue-editing",
		Command:  "/continue",
		ChatHint: localizedText(cfg, `say "keep editing"`, `「편집만 계속」이라고 입력`),
		TitleEN:  "Keep editing for now",
		TitleKO:  "지금은 편집만 계속",
		ReasonEN: "Close this prompt and keep editing. Completion may still be blocked later.",
		ReasonKO: "이 선택을 닫고 편집을 이어갑니다. 완료는 나중에 다시 막힐 수 있습니다.",
		Kind:     harnessRecoveryActionContinueEditing,
	})
	recovery.Normalize()
	return recovery
}

// requestLooksLikeInspectThenDocumentTurn reports "read/inspect then write a
// document/report" work. That is NOT a code-repair turn: stall recovery must
// offer "write the document" / answer paths, never "continue repairing code".
func requestLooksLikeInspectThenDocumentTurn(text string) bool {
	base := strings.TrimSpace(baseUserQueryText(text))
	if base == "" {
		base = strings.TrimSpace(text)
	}
	if base == "" {
		return false
	}
	lower := strings.ToLower(base)
	hasDocDeliverable := containsAny(lower,
		"문서로 작성", "문서 작성", "문서를 작성", "문서를 만들", "문서로 정리", "문서로 남겨",
		"문서를 써", "문서를 써줘", "문서로 써", "문서를 보강", "문서 보강", "문서를 보완", "문서 보완",
		"보고서를 작성", "보고서 작성", "보고서를 만들",
		"write a document", "write a report", "write documentation", "write the doc",
		"create a document", "create a report", "draft a document", "draft a report",
		"improve the document", "update the documentation", "update the document",
		"write it up", "write-up", "writeup",
	)
	if !hasDocDeliverable {
		// "작성해줘"/"보강해줘" alone is ambiguous; require a document noun.
		if containsAny(lower, "작성해", "작성해줘", "작성해 줘", "보강해", "보강해줘", "보완해", "수정해", "write ", "create ", "update ") &&
			containsAny(lower, "문서", "document", "report", "보고서", ".md", "docs/") {
			hasDocDeliverable = true
		}
	}
	if !hasDocDeliverable {
		return false
	}
	// Prefer inspect/gap context so plain "문서를 작성해" without research still
	// counts when @path or 부족/구현 language is present.
	if requestLooksLikeLocalWorkspaceInspection(lower) {
		return true
	}
	return containsAny(lower,
		"읽", "read", "찾", "find", "부족", "gap", "missing", "구현", "implementation",
		"분석", "analyze", "검토", "review", "비교", "compare",
		"문제", "수정", "발견", "fix", "issue", "보강", "보완",
	)
}

// requestLooksLikeAnalysisOnlyTurn reports answer/analysis (or inspect→document)
// work that should not be steered into a code-repair continue card when the
// read loop stalls.
func requestLooksLikeAnalysisOnlyTurn(text string) bool {
	base := strings.TrimSpace(baseUserQueryText(text))
	if base == "" {
		base = strings.TrimSpace(text)
	}
	if base == "" {
		return false
	}
	if looksLikeOperatorRecoveryContinuePrompt(base) {
		return false
	}
	// Inspect-then-document is non-repair deliverable work.
	if requestLooksLikeInspectThenDocumentTurn(base) {
		return true
	}
	lower := strings.ToLower(base)
	// Explicit source-code edits are never analysis-only. Document "작성해줘" is
	// handled above and must not be treated as a code repair verb here.
	if looksLikeImperativeSourceEditCommand(base) ||
		containsAny(lower,
			"수정해", "고쳐", "패치해", "구현해", "적용해",
			"fix it", "patch it", "implement it", "apply the", "write the code",
		) {
		// "만들어줘" / "작성해줘" without document context can still be code work.
		if containsAny(lower, "만들어줘", "작성해줘", "작성해 줘", "create the file") &&
			!containsAny(lower, "문서", "document", "report", "보고서", ".md") {
			return false
		}
		if !containsAny(lower, "만들어줘", "작성해줘", "작성해 줘", "create the file") {
			return false
		}
	}
	// Strong local read+report phrasing (the failure case that was mis-routed to
	// "continue repairing"). Checked before envelope mutation flags because
	// "문서를 읽고 … 알려줘" can trip document-authoring heuristics without being
	// a code edit request.
	if requestLooksLikeLocalWorkspaceInspection(lower) &&
		containsAny(lower,
			"알려", "tell", "explain", "what", "find", "찾", "부족", "gap", "missing",
			"compare", "분석", "검토", "read", "읽", "summar", "요약", "list gaps",
		) {
		return true
	}
	envelope := buildRequestEnvelope(base)
	if envelope.DocumentAuthoring && !envelope.ExplicitEditRequest {
		return true
	}
	if envelope.ExplicitEditRequest && !envelope.DocumentAuthoring {
		return false
	}
	if requestEnvelopeAllowsRepairContinuation(envelope) && !envelope.DocumentAuthoring {
		return false
	}
	if envelope.ReadOnlyAnalysis {
		return true
	}
	switch envelope.Intent {
	case TurnIntentReviewCode, TurnIntentAskProjectKnowledge, TurnIntentPlanOrDesign, TurnIntentExplainCurrentState:
		return true
	}
	return false
}

func (a *Agent) recordStallBlockedRecovery(cause string, summary string, files []string) HarnessBlockedRecovery {
	cfg := Config{}
	analysisOnly := false
	if a != nil {
		cfg = a.Config
		if a.Session != nil {
			analysisOnly = requestLooksLikeAnalysisOnlyTurn(sessionEffectiveUserRequestText(a.Session))
		}
	}
	recovery := buildStallBlockedRecoveryWithMode(cfg, a.Session, cause, summary, files, analysisOnly)
	if a != nil && a.Session != nil {
		a.Session.PendingHarnessBlockedRecovery = &recovery
		if a.Session.LastFinalAnswerCorrection != nil && a.Session.LastFinalAnswerCorrection.Contract != nil {
			a.Session.LastFinalAnswerCorrection.Contract.ManualRecoveryNeeded = true
			if recovery.PrimaryCommand != "" {
				a.Session.LastFinalAnswerCorrection.Contract.NextCommand = recovery.PrimaryCommand
			}
		}
	}
	return recovery
}

// finalizeOperatorStallReply records a pending recovery card and returns the
// user-facing reply that should replace a hard assistant error.
func (a *Agent) finalizeOperatorStallReply(cause string, baseReply string, files []string) string {
	baseReply = strings.TrimSpace(baseReply)
	if baseReply == "" {
		baseReply = operatorStallBaseReply(a.Config, cause, "")
	}
	recovery := a.recordStallBlockedRecovery(cause, baseReply, files)
	card := renderHarnessBlockedRecoveryReply(a.Config, nil, recovery.ExtraBlockers, recovery)
	return strings.TrimSpace(baseReply + "\n\n" + card)
}

func operatorStallBaseReply(cfg Config, cause, detail string) string {
	detail = strings.TrimSpace(detail)
	switch strings.TrimSpace(cause) {
	case harnessRecoveryCauseRepeatedToolCalls:
		return localizedText(cfg,
			"I stopped after repeating the same tool calls without making progress.",
			"같은 도구 호출을 진전 없이 반복해서 중단했습니다.")
	case harnessRecoveryCauseRepeatedToolFailure:
		msg := localizedText(cfg,
			"I stopped after the same tool failure repeated.",
			"같은 도구 실패가 반복되어 중단했습니다.")
		if detail != "" {
			msg += "\n\n" + localizedText(cfg, "Last failure:", "마지막 실패:") + " " + compactPromptSection(detail, 320)
		}
		return msg
	case harnessRecoveryCauseToolLoopLimit:
		msg := localizedText(cfg,
			"I stopped after hitting the tool-loop limit for this turn.",
			"이번 턴의 도구 루프 한도에 걸려 중단했습니다.")
		if detail != "" {
			msg += "\n\n" + compactPromptSection(detail, 400)
		}
		return msg
	case harnessRecoveryCauseCommentaryOnly:
		return localizedText(cfg,
			"I stopped after repeated commentary-only replies with no tool call or final answer.",
			"도구 호출이나 최종 답변 없이 진행 설명만 반복되어 중단했습니다.")
	case harnessRecoveryCauseLengthStop:
		msg := localizedText(cfg,
			"I stopped after the model repeatedly hit the output length limit before producing a usable answer.",
			"모델이 쓸 만한 답변을 내기 전에 출력 길이 제한에 반복해서 걸려 중단했습니다.")
		if detail != "" {
			msg += "\n\n" + detail
		}
		return msg
	case harnessRecoveryCauseContentFilter:
		msg := localizedText(cfg,
			"I stopped after the provider content filter blocked replies.",
			"제공자 content filter가 답변을 막아 중단했습니다.")
		if detail != "" {
			msg += "\n\n" + detail
		}
		return msg
	case harnessRecoveryCauseEmptyStop:
		msg := localizedText(cfg,
			"I stopped after the model returned repeated empty responses.",
			"모델이 빈 응답을 반복해서 반환해 중단했습니다.")
		if detail != "" {
			msg += "\n\n" + compactPromptSection(detail, 400)
		}
		return msg
	case harnessRecoveryCauseFinalGate:
		msg := localizedText(cfg,
			"I stopped because confirmation is still needed before finishing.",
			"완료를 마무리하려면 확인이 필요해 중단했습니다.")
		if detail != "" {
			msg += "\n\n" + compactPromptSection(detail, 400)
		}
		return msg
	case harnessRecoveryCauseNoProgress:
		return localizedText(cfg,
			"No-progress guard stopped this turn. No files were changed.",
			"무진행 가드로 이번 턴을 중단했습니다. 변경된 파일은 없습니다.")
	default:
		msg := localizedText(cfg,
			"I stopped to avoid an unproductive loop.",
			"진전 없는 반복을 피하려고 멈췄습니다.")
		if detail != "" {
			msg += "\n\n" + compactPromptSection(detail, 400)
		}
		return msg
	}
}

func classifyAssistantStallError(err error) (cause string, detail string) {
	if err == nil {
		return "", ""
	}
	text := strings.TrimSpace(err.Error())
	switch {
	case strings.HasPrefix(text, "stopped after repeated identical tool calls"):
		return harnessRecoveryCauseRepeatedToolCalls, text
	case strings.HasPrefix(text, "stopped after repeatedly reading the same file without making progress:"):
		return harnessRecoveryCauseReadChurn, text
	case strings.HasPrefix(text, "stopped after repeatedly cycling read_file"):
		return harnessRecoveryCauseReadChurn, text
	case strings.HasPrefix(text, "stopped after repeated tool failure:"):
		return harnessRecoveryCauseRepeatedToolFailure, strings.TrimSpace(strings.TrimPrefix(text, "stopped after repeated tool failure:"))
	case strings.HasPrefix(text, "tool loop limit exceeded"):
		return harnessRecoveryCauseToolLoopLimit, text
	case strings.Contains(text, "commentary-only assistant messages"):
		return harnessRecoveryCauseCommentaryOnly, text
	case strings.Contains(text, "token limit") || strings.Contains(text, "stop_reason=length"):
		return harnessRecoveryCauseLengthStop, text
	case strings.Contains(text, "content filter blocked"):
		return harnessRecoveryCauseContentFilter, text
	case strings.Contains(text, "empty response") || strings.Contains(text, "empty model response"):
		return harnessRecoveryCauseEmptyStop, text
	case strings.Contains(text, "[final_gate") || strings.HasPrefix(text, "Stop hook kept blocking"):
		return harnessRecoveryCauseFinalGate, text
	default:
		return "", ""
	}
}

func stallRecoveryFilesFromSeen(seen map[string]struct{}) []string {
	files := make([]string, 0, len(seen))
	for p := range seen {
		if p = strings.TrimSpace(p); p != "" {
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return normalizeTaskStateList(files, 8)
}
