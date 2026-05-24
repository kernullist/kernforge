package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Agent struct {
	Config                         Config
	Client                         ProviderClient
	ModelRoutes                    *ModelRouteScheduler
	ReviewerClient                 ProviderClient
	ReviewerModel                  string
	AuxReviewerClient              ProviderClient
	AuxReviewerModel               string
	Tools                          *ToolRegistry
	Workspace                      Workspace
	Session                        *Session
	Store                          *SessionStore
	Memory                         MemoryBundle
	Skills                         SkillCatalog
	MCP                            *MCPManager
	LongMem                        *PersistentMemoryStore
	Evidence                       *EvidenceStore
	VerifyHistory                  *VerificationHistoryStore
	FunctionFuzz                   *FunctionFuzzStore
	FuzzCampaigns                  *FuzzCampaignStore
	Hooks                          *HookRuntime
	VerifyChanges                  func(context.Context) (VerificationReport, bool)
	PromptConfirmAutoVerify        func(VerificationPlan) (bool, error)
	PromptResolveAutoVerifyFailure func(VerificationReport) (AutoVerifyFailureResolution, error)
	PromptContinueReviewRepair     func(string) (bool, error)
	UserChangeIsolation            *UserChangeIsolationState
	EmitAssistant                  func(string)
	EmitAssistantPersistent        func(string)
	EmitAssistantDelta             func(string)
	EmitProgress                   func(string)
	EmitProgressEvent              func(ProgressEvent)
	lastEmittedText                string
}

type AutoVerifyFailureResolution string

const (
	AutoVerifyFailureNoAction AutoVerifyFailureResolution = ""
	AutoVerifyFailureDisable  AutoVerifyFailureResolution = "disable"
	AutoVerifyFailureRetry    AutoVerifyFailureResolution = "retry"
)

const (
	repeatedToolCallNudgeThreshold        = 3
	repeatedToolCallRecoveryThreshold     = 4
	repeatedToolCallAbortThreshold        = 5
	repeatedToolFailureNudgeThreshold     = 2
	repeatedToolFailureRecoveryThreshold  = 3
	repeatedToolFailureAbortThreshold     = 4
	repeatedReadFilePathNudgeTurns        = 4
	repeatedReadFilePathRecoveryThreshold = 6
	repeatedReadFilePathAbortTurns        = 8
	maxPreWriteReviewRepairBlocksPerTurn  = 4
	maxPreWriteReviewRepairInspectTools   = 6
	maxPreWriteReviewRepairInspectNudges  = 1
	maxEditTargetMismatchFailuresPerTurn  = 1
	maxEditTargetMismatchReanchorBlocks   = 1
	maxToolBudgetExtensions               = 2
	maxStopHookRevisions                  = 3
	compactPinnedMessagesToKeep           = 6
	compactRetainedMessageTokenBudget     = 64_000
	compactApproxCharsPerToken            = 4
	compactMinRetainedMessageCharBudget   = 8_000
)

var errVerificationFollowupBlocked = errors.New("verification follow-up blocked after verification was declined or skipped")
var errVerificationOutOfScopeFollowupBlocked = errors.New("verification follow-up blocked after verification failed outside the current patch scope")
var errReadOnlyAnalysisToolBlocked = errors.New("read-only analysis blocked a tool that can mutate external state")
var errTurnDisabledToolBlocked = errors.New("turn tool exposure blocked a disabled tool")
var errPreWriteReviewReanchorRequired = errors.New("pre-write blocked proposal requires current file reanchor before next edit")

func (a *Agent) Reply(ctx context.Context, userText string) (string, error) {
	return a.ReplyWithImages(ctx, userText, nil)
}

const (
	reviewRepairConfirmationModeRepair                  = "repair"
	reviewRepairConfirmationModeReviewerGateUnavailable = "reviewer_gate_unavailable"
)

func (a *Agent) ReplyWithImages(ctx context.Context, userText string, extraImages []MessageImage) (string, error) {
	a.lastEmittedText = ""
	a.discardStaleFinalAnswerCandidates()
	startIndex := len(a.Session.Messages)
	confirmedReviewRepair := false
	confirmedReviewerGateRepair := false
	if a.hasPendingReviewRepairConfirmation() {
		pendingMode := a.pendingReviewRepairConfirmationMode()
		switch parseReviewRepairConfirmationInput(userText) {
		case reviewRepairConfirmationYes:
			if a.Session.LastReviewRun == nil || !reviewRunNeedsRepair(*a.Session.LastReviewRun) {
				reply, err := a.finishPendingReviewRepairConfirmation(userText, extraImages, true, formatMissingPendingReviewRepairReply(a.Config))
				return reply, err
			}
			if pendingMode == reviewRepairConfirmationModeReviewerGateUnavailable &&
				!reviewRunHasActionableNonReviewerFindings(*a.Session.LastReviewRun) {
				reply, err := a.finishPendingReviewRepairConfirmation(userText, extraImages, true, formatNoActionableReviewerGateRepairReply(a.Config))
				return reply, err
			}
			a.Session.PendingReviewRepairConfirm = nil
			userText = "계속 수정해"
			if pendingMode == reviewRepairConfirmationModeReviewerGateUnavailable {
				confirmedReviewerGateRepair = true
			} else {
				confirmedReviewRepair = true
			}
		case reviewRepairConfirmationNo:
			reply, err := a.finishPendingReviewRepairConfirmation(userText, extraImages, true, formatCancelledPendingReviewRepairReply(a.Config))
			return reply, err
		default:
			reply, err := a.finishPendingReviewRepairConfirmation(userText, extraImages, false, formatInvalidPendingReviewRepairReply(a.Config))
			return reply, err
		}
	}
	intent := classifyTurnIntent(userText)
	requestMode := resolveAgentRequestMode(userText, intent)
	intent = requestMode.Intent
	readOnlyAnalysis := requestMode.ReadOnlyAnalysis
	explicitEditRequest := requestMode.ExplicitEditRequest
	explicitGitRequest := looksLikeExplicitGitIntent(userText)
	enriched, mentionImages := a.expandMentions(ctx, userText)
	if runtimeContext := strings.TrimSpace(a.assembleConversationRuntimeContext(userText)); runtimeContext != "" {
		enriched += "\n\n" + runtimeContext
	}
	if readOnlyAnalysis {
		enriched += "\n\nRequest mode: analysis-only.\n- Investigate, explain, or document the issue.\n- Do not modify files or call edit tools unless the user explicitly asks for a fix.\n"
	} else if explicitEditRequest {
		enriched += "\n\nRequest mode: inspect-and-fix.\n- Investigate the referenced code and apply the necessary fix directly when needed.\n- Use available inspect tools first, then use edit tools to make the change.\n- Do not ask the user to apply the patch manually unless an edit tool actually failed and you cite that tool error.\n"
	}
	if confirmedReviewRepair {
		enriched += "\n\nPending review repair confirmation:\n- The user selected `y` for the pending pre-write review repair prompt.\n- Continue from the latest review findings and the last edit proposal already stored in this session.\n- Do not run a new review-before-fix pass for this confirmation turn.\n- Do not run package-wide tests unless the user explicitly requests them; use focused verification only.\n"
	}
	if confirmedReviewerGateRepair {
		enriched += "\n\nPending reviewer-gate repair confirmation:\n- The user selected `y` after a pre-write reviewer gate failed or returned weak output.\n- This is not approval to write without review and not approval to bypass the reviewer gate.\n- Continue repairing only the actionable non-reviewer findings and the last edit proposal already stored in this session.\n- Use the normal edit tool path so the pre-write review gate runs again before any file write.\n- Do not run package-wide tests unless the user explicitly requests them; use focused verification only.\n"
	}
	if explicitGitRequest {
		enriched += "\n\nGit intent:\n- The user explicitly asked for a git action such as staging, committing, pushing, or opening a PR.\n- If you perform a git-mutating action, summarize exactly what you are about to do.\n"
	}
	enriched = a.Skills.InjectPromptContext(enriched)
	if a.LongMem != nil {
		memoryPolicy := persistentMemoryPromptPolicyForRequest(userText)
		if memoryContext := a.LongMem.PromptContextDetailsWithPolicy(a.Workspace.BaseRoot, userText, a.Session.ID, memoryPolicy); strings.TrimSpace(memoryContext.Text) != "" {
			enriched += "\n\nRelevant persistent memory from past sessions:\n" + memoryContext.Text
			if message := formatPersistentMemoryProgressMessage(a.Config, memoryContext); message != "" {
				a.emitProgressEvent(ProgressEvent{
					Kind:    progressKindMemoryContext,
					Message: message,
				})
			}
		}
	}
	analysisContext := ""
	if !shouldSuppressProjectAnalysisFastPathForIntent(intent) {
		analysisContext = strings.TrimSpace(a.latestProjectAnalysisContext(userText))
	}
	if analysisContext != "" {
		enriched += "\n\nRelevant project analysis from past analyze-project runs:\n" + analysisContext
	}
	if analysisContext == "" {
		if scout := a.autoScoutContext(userText); scout != "" {
			enriched += scout
		}
	}
	images := appendUniqueImages(nil, mentionImages...)
	images = appendUniqueImages(images, extraImages...)
	a.noteUserConversationEvent(userText, images)
	if a.shouldStartNewExternalAcceptanceContext(userText) {
		a.initializeTaskState(userText)
		contract := buildAcceptanceContract(userText, intent, readOnlyAnalysis, explicitEditRequest, explicitGitRequest)
		a.Session.AcceptanceContract = &contract
		a.Session.LastCodingHarnessReport = nil
		a.Session.LastUserChangeIsolationReport = nil
		a.Session.LastTestImpactReport = nil
		a.Session.LastJobSupervisorReport = nil
	} else if a.Session.TaskState == nil {
		if prompt := a.preservableExternalAcceptancePrompt(); prompt != "" {
			a.initializeTaskState(prompt)
		}
	}
	a.startUserChangeIsolation()
	a.Session.AddMessage(Message{
		Role:   "user",
		Text:   enriched,
		Images: images,
	})
	if err := a.Store.Save(a.Session); err != nil {
		return "", err
	}
	if reply, ok := a.maybeAnswerRecentErrorQuestion(userText); ok {
		reply = a.maybeAppendProactiveSuggestion(reply, userText)
		reply = sanitizeAssistantFinalText(reply)
		if len(a.Session.Messages) > 0 && a.Session.Messages[len(a.Session.Messages)-1].Role == "assistant" {
			a.Session.Messages[len(a.Session.Messages)-1].Text = reply
		} else {
			a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
			a.noteAssistantConversationEvent(reply)
			a.Session.RefreshConversationState()
		}
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		return reply, nil
	}
	ranReviewMode, reviewModeReply, err := a.maybeRunCodexAppReviewMode(ctx, userText, enriched, images)
	if err != nil {
		return "", err
	}
	if ranReviewMode {
		reviewModeReply = sanitizeAssistantFinalText(reviewModeReply)
		a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reviewModeReply})
		if a.LongMem != nil {
			safeStart := startIndex
			if safeStart > len(a.Session.Messages) {
				safeStart = len(a.Session.Messages)
			}
			_ = a.LongMem.CaptureTurn(a.Workspace, a.Session, userText, reviewModeReply, a.Session.Messages[safeStart:])
		}
		if a.Evidence != nil {
			_ = a.Evidence.CaptureVerification(a.Workspace, a.Session)
		}
		a.noteAssistantConversationEvent(reviewModeReply)
		a.Session.RefreshConversationState()
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		return reviewModeReply, nil
	}
	if a.Client == nil {
		return "", fmt.Errorf("no model provider is configured")
	}
	ranReviewBeforeFix, err := a.maybeRunReviewBeforeFix(ctx, userText, images, readOnlyAnalysis, explicitEditRequest)
	if err != nil {
		return "", err
	}
	if ranReviewBeforeFix {
		if reply, ok := a.maybeStopAfterReviewerGateUnavailable(); ok {
			reply = sanitizeAssistantFinalText(reply)
			a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			return reply, nil
		}
	}
	if ranReviewBeforeFix && a.shouldConcludeAfterNonBlockingPreFixReview(userText) {
		reply := a.formatNonBlockingPreFixReviewReply()
		reply = sanitizeAssistantFinalText(reply)
		a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		return reply, nil
	}
	if !ranReviewBeforeFix {
		primedRepair := false
		if confirmedReviewerGateRepair {
			primedRepair = a.maybePrimeRepairFromReviewerGateUnavailable(userText, images, readOnlyAnalysis, explicitEditRequest)
		} else {
			primedRepair = a.maybePrimeRepairFromLastReview(userText, images, readOnlyAnalysis, explicitEditRequest)
		}
		if primedRepair {
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
		}
	}
	reply, err := a.completeLoop(ctx, readOnlyAnalysis, explicitEditRequest, explicitGitRequest)
	if err != nil {
		return "", err
	}
	reply = a.maybeAppendProactiveSuggestion(reply, userText)
	reply = sanitizeAssistantFinalText(reply)
	if len(a.Session.Messages) > 0 && a.Session.Messages[len(a.Session.Messages)-1].Role == "assistant" {
		a.Session.Messages[len(a.Session.Messages)-1].Text = reply
	}
	if a.LongMem != nil {
		// completeLoop() 내에서 Compact()가 호출되어 메시지가 축소되었을 수 있음
		safeStart := startIndex
		if safeStart > len(a.Session.Messages) {
			safeStart = len(a.Session.Messages)
		}
		_ = a.LongMem.CaptureTurn(a.Workspace, a.Session, userText, reply, a.Session.Messages[safeStart:])
	}
	if a.Evidence != nil {
		_ = a.Evidence.CaptureVerification(a.Workspace, a.Session)
	}
	a.noteAssistantConversationEvent(reply)
	a.Session.RefreshConversationState()
	_ = a.Store.Save(a.Session)
	return reply, nil
}

type agentRequestMode struct {
	Intent                TurnIntent
	ReviewOnlyModeRequest bool
	ReadOnlyAnalysis      bool
	ExplicitEditRequest   bool
}

func resolveAgentRequestMode(userText string, intent TurnIntent) agentRequestMode {
	documentArtifactEditRequest := looksLikeDocumentAuthoringIntent(userText) && looksLikeExplicitEditIntent(userText)
	repairActionNegated := hasRepairActionNegation(userText) && !documentArtifactEditRequest
	explicitEditRequest := looksLikeExplicitEditIntent(userText) && !repairActionNegated
	reviewOnlyModeRequest := looksLikeReviewOnlyModeIntent(userText) && !explicitEditRequest
	readOnlyAnalysis := repairActionNegated || prefersReadOnlyAnalysisIntent(userText) || reviewOnlyModeRequest
	if readOnlyAnalysis && intent == TurnIntentEditCode {
		intent = TurnIntentGeneral
	}
	return agentRequestMode{
		Intent:                intent,
		ReviewOnlyModeRequest: reviewOnlyModeRequest,
		ReadOnlyAnalysis:      readOnlyAnalysis,
		ExplicitEditRequest:   explicitEditRequest && !readOnlyAnalysis,
	}
}

type reviewRepairConfirmationDecision int

const (
	reviewRepairConfirmationInvalid reviewRepairConfirmationDecision = iota
	reviewRepairConfirmationYes
	reviewRepairConfirmationNo
)

func (a *Agent) hasPendingReviewRepairConfirmation() bool {
	return a != nil && a.Session != nil && a.Session.PendingReviewRepairConfirm != nil
}

func (a *Agent) pendingReviewRepairConfirmationMode() string {
	if a == nil || a.Session == nil || a.Session.PendingReviewRepairConfirm == nil {
		return ""
	}
	mode := strings.TrimSpace(a.Session.PendingReviewRepairConfirm.Mode)
	if mode == "" {
		return reviewRepairConfirmationModeRepair
	}
	return mode
}

func (a *Agent) shouldStartNewExternalAcceptanceContext(userText string) bool {
	if a == nil || a.Session == nil {
		return true
	}
	text := strings.TrimSpace(baseUserQueryText(userText))
	if text == "" {
		return true
	}
	if looksLikeInternalReviewFeedbackUserMessage(text) {
		return !a.hasPreservableExternalAcceptanceContext()
	}
	return true
}

func (a *Agent) hasPreservableExternalAcceptanceContext() bool {
	return a.preservableExternalAcceptancePrompt() != ""
}

func (a *Agent) preservableExternalAcceptancePrompt() string {
	if a == nil || a.Session == nil {
		return ""
	}
	if a.Session.AcceptanceContract != nil {
		if prompt := strings.TrimSpace(baseUserQueryText(a.Session.AcceptanceContract.SourcePrompt)); prompt != "" && !looksLikeInternalReviewFeedbackUserMessage(prompt) {
			return prompt
		}
	}
	if a.Session.TaskState != nil {
		if goal := strings.TrimSpace(baseUserQueryText(a.Session.TaskState.Goal)); goal != "" && !looksLikeInternalReviewFeedbackUserMessage(goal) {
			return goal
		}
	}
	return latestExternalUserMessageText(a.Session.Messages)
}

func parseReviewRepairConfirmationInput(input string) reviewRepairConfirmationDecision {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "y":
		return reviewRepairConfirmationYes
	case "n":
		return reviewRepairConfirmationNo
	default:
		return reviewRepairConfirmationInvalid
	}
}

func (a *Agent) finishPendingReviewRepairConfirmation(userText string, images []MessageImage, clearPending bool, reply string) (string, error) {
	if a == nil || a.Session == nil {
		return sanitizeAssistantFinalText(reply), nil
	}
	reply = sanitizeAssistantFinalText(reply)
	if clearPending {
		a.Session.PendingReviewRepairConfirm = nil
	}
	a.noteUserConversationEvent(userText, images)
	a.Session.AddMessage(Message{
		Role:   "user",
		Text:   userText,
		Images: images,
	})
	a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
	a.noteAssistantConversationEvent(reply)
	a.Session.RefreshConversationState()
	if a.Store != nil {
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
	}
	return reply, nil
}

func formatInvalidPendingReviewRepairReply(cfg Config) string {
	if localePrefersKorean(cfg) {
		return "계속 수정 여부는 `y` 또는 `n`만 입력해 주세요. `y`는 최신 리뷰 결과 기준으로 계속 수정하고, `n`은 여기서 중단합니다."
	}
	return "Please answer with exactly `y` or `n`. `y` continues from the latest review result, and `n` stops here."
}

func formatCancelledPendingReviewRepairReply(cfg Config) string {
	if localePrefersKorean(cfg) {
		return "알겠습니다. 리뷰 미통과 상태와 마지막 수정안은 세션에 남겨두고, 추가 수정은 진행하지 않겠습니다."
	}
	return "Understood. I kept the failed review result and latest proposal in the session, and I will not continue repairing."
}

func formatMissingPendingReviewRepairReply(cfg Config) string {
	if localePrefersKorean(cfg) {
		return "계속 수정할 최신 리뷰 결과가 더 이상 세션에 남아 있지 않습니다. 확인 상태를 닫고 여기서 멈춥니다."
	}
	return "There is no longer a repairable latest review result in the session. I cleared the confirmation state and stopped here."
}

func formatNoActionableReviewerGateRepairReply(cfg Config) string {
	if localePrefersKorean(cfg) {
		return "이번 중단은 코드 finding 때문이 아니라 필수 리뷰 단계의 모델 route 실패/약한 응답 때문입니다. `primary`가 실패했다면 현재 메인 모델 또는 프로바이더 route 문제이므로 `/model`로 메인 모델을 바꾸거나 LM Studio/Qwen 응답 문제를 먼저 해결하세요. `cross` 또는 전용 reviewer가 실패했다면 `/review models`로 해당 reviewer route를 정상 동작하는 모델로 바꾸세요. 지금 계속해도 수정할 코드 항목이 없으므로 추가 편집은 진행하지 않습니다. route를 복구한 뒤 같은 요청을 다시 실행해 주세요."
	}
	return "This stop was caused by a required review-stage model route failure or weak output, not by a code finding. If `primary` failed, the active main model/provider route is the problem; use `/model` to switch the main model or fix the LM Studio/Qwen response issue first. If `cross` or a dedicated reviewer failed, use `/review models` to switch that reviewer route to a working model. There is no code item to repair right now, so I will not continue editing. Restore the route, then rerun the same request."
}

func (a *Agent) Compact(instructions string) string {
	summary, err := a.CompactWithTrigger(context.Background(), instructions, "manual", "user_requested")
	if err != nil {
		return "compaction blocked: " + err.Error()
	}
	return summary
}

func (a *Agent) CompactWithTrigger(ctx context.Context, instructions string, trigger string, reason string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.discardStaleFinalAnswerCandidates()
	cut := compactCutIndex(a.Session.Messages, 12, 4)
	if cut <= 0 {
		return "conversation is already compact", nil
	}
	trigger = normalizeCompactTrigger(trigger)
	reason = strings.TrimSpace(reason)
	beforeMessages := len(a.Session.Messages)
	beforeChars := a.Session.ApproxChars()
	beforeSummaryChars := len(a.Session.Summary)
	if _, err := a.Workspace.Hook(ctx, HookPreCompact, compactHookPayload(a, HookPreCompact, instructions, trigger, reason, "", beforeMessages, beforeChars, beforeSummaryChars, 0, 0, 0)); err != nil {
		return "", err
	}
	candidates := append([]Message(nil), a.Session.Messages[cut:]...)
	retained, summarizeCandidatePrefix := compactRetainedMessagesWithinBudget(candidates, compactRetainedMessageCharBudget(a.Config.AutoCompactChars))
	olderEnd := cut + summarizeCandidatePrefix
	if olderEnd > len(a.Session.Messages) {
		olderEnd = len(a.Session.Messages)
	}
	older := a.Session.Messages[:olderEnd]
	a.Session.Messages = retained
	summary := summarizeMessages(older, instructions)
	if workingMemory := strings.TrimSpace(renderCompactionWorkingMemory(a.Session)); workingMemory != "" {
		summary = strings.TrimSpace(summary) + "\n\n" + workingMemory
	}
	if strings.TrimSpace(a.Session.Summary) == "" {
		a.Session.Summary = summary
	} else {
		a.Session.Summary = strings.TrimSpace(a.Session.Summary) + "\n\n" + summary
	}
	afterMessages := len(a.Session.Messages)
	afterChars := a.Session.ApproxChars()
	afterSummaryChars := len(a.Session.Summary)
	if a.Store != nil {
		_ = a.Store.Save(a.Session)
	}
	if _, err := a.Workspace.Hook(ctx, HookPostCompact, compactHookPayload(a, HookPostCompact, instructions, trigger, reason, "success", beforeMessages, beforeChars, beforeSummaryChars, afterMessages, afterChars, afterSummaryChars)); err != nil {
		return "", err
	}
	return summary, nil
}

func normalizeCompactTrigger(trigger string) string {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "auto":
		return "auto"
	default:
		return "manual"
	}
}

func compactHookPayload(a *Agent, event HookEvent, instructions string, trigger string, reason string, status string, beforeMessages int, beforeChars int, beforeSummaryChars int, afterMessages int, afterChars int, afterSummaryChars int) HookPayload {
	payload := HookPayload{
		"hook_event_name":      string(event),
		"trigger":              normalizeCompactTrigger(trigger),
		"implementation":       "local",
		"instructions":         strings.TrimSpace(instructions),
		"messages_before":      beforeMessages,
		"approx_chars_before":  beforeChars,
		"summary_chars_before": beforeSummaryChars,
	}
	if strings.TrimSpace(reason) != "" {
		payload["reason"] = strings.TrimSpace(reason)
	}
	if strings.TrimSpace(status) != "" {
		payload["status"] = strings.TrimSpace(status)
		payload["messages_after"] = afterMessages
		payload["approx_chars_after"] = afterChars
		payload["summary_chars_after"] = afterSummaryChars
	}
	if a != nil && a.Session != nil {
		payload["session_id"] = a.Session.ID
		payload["model"] = a.Session.Model
		payload["provider"] = a.Session.Provider
	}
	return payload
}

func (a *Agent) completeLoop(ctx context.Context, readOnlyAnalysis bool, explicitEditRequest bool, explicitGitRequest bool) (string, error) {
	a.refreshBackgroundJobs()
	if reply, ok, err := a.maybeAnswerFromCachedProjectAnalysis(ctx); err != nil {
		_ = a.Store.Save(a.Session)
		return "", err
	} else if ok {
		a.Session.AddMessage(Message{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  reply,
		})
		a.noteAssistantConversationEvent(reply)
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		return reply, nil
	}
	latestUser := latestExternalOrUserMessageText(a.Session.Messages)
	latestUserExplicitWebResearch := requestExplicitlyAsksForWebResearch(strings.ToLower(strings.TrimSpace(baseUserQueryText(latestUser))))
	intent := classifyTurnIntent(latestUser)
	_ = a.primeSelfDrivingWorkLoop(latestUser, intent, readOnlyAnalysis, explicitEditRequest, explicitGitRequest)
	if err := a.maybePrimeInteractivePlan(ctx, readOnlyAnalysis, explicitEditRequest, explicitGitRequest); err != nil {
		return "", err
	}
	localCodeToolPolicyForTurn := !latestUserExplicitWebResearch && shouldUseLocalCodeToolPolicy(a.Session)
	emptyFinalReplies := 0
	unresolvedVerification := false
	finalAnswerNudges := 0
	patchFormatRetries := 0
	lastPatchFormatFailureSignature := ""
	invalidToolArgsRetries := 0
	editTargetMismatchRetries := 0
	editTargetMismatchFailures := 0
	editTargetMismatchRequiresReanchor := false
	editTargetMismatchReanchorBlocks := 0
	preWriteReviewRepairBlocks := 0
	preWriteReviewRepairBlockFingerprints := map[string]int{}
	preWriteReviewRepairInspectTools := 0
	preWriteReviewRepairInspectNudges := 0
	preWriteReviewRequiresReanchor := false
	preWriteReviewReanchorBlocks := 0
	lastToolError := ""
	lastToolErrorCount := 0
	lastToolCallSignature := ""
	lastToolCallSignatureCount := 0
	repeatedToolCallNudges := 0
	repeatedToolCallRecoveryTurns := 0
	lastToolCallSummary := ""
	lastReadFilePath := ""
	lastReadFilePathTurns := 0
	repeatedReadFilePathNudges := 0
	repeatedCachedReadFileNudges := 0
	repeatedReadFilePathRecoveryCount := 0
	lastStopReason := ""
	lastIteration := 0
	lastRecentToolTurns := ""
	consecutiveEditTurns := 0
	postEditFinalAnswerNudges := 0
	autoVerifyInfraFailureCount := 0
	autoVerifyDisablePrompted := false
	manualEditHandoffRetries := 0
	internalToolTranscriptFailureReplyRetries := 0
	toolAvailabilityBlameReplyRetries := 0
	localCodeToolAvailabilityBlameRetries := 0
	abruptReplyRetries := 0
	rawReviewResultReplyRetries := 0
	commentaryOnlyReplies := 0
	finalAnswerReviewRevisions := 0
	stopHookRevisions := 0
	stopHookActive := false
	finalHarnessRevisions := 0
	runtimeGateFinalAnswerRevisions := 0
	runtimeGateGitWriteNudges := 0
	postChangeReviewRevisions := 0
	postChangeReviewExhaustedNudge := false
	lastPostChangeReviewFingerprint := ""
	lastReviewedFinalAnswer := ""
	finalAnswerOnlyCorrection := false
	serverModelWarningEmitted := false
	modelVerificationEmitted := false
	providerTurnState := &ProviderTurnState{}
	attemptedEditTool := false
	successfulEditTool := false
	sawToolResultThisTurn := false
	verificationDeclinedThisTurn := false
	verificationOutOfScopeThisTurn := false
	verificationOutOfScopeFinalOnly := false
	repeatedToolFailureRecoveryTurns := 0
	continuedReplyPrefix := ""
	continuedReplyMessageIndex := -1
	turnStartedAt := time.Now()
	stopHookTurnID := a.newStopLifecycleTurnID(turnStartedAt)
	mcpTurnMetadata := a.mcpTurnMetadataForToolCall(turnStartedAt)
	providerTurnMetadata := providerTurnMetadataFromMCP(mcpTurnMetadata)
	a.noteTurnStartedConversationEvent(turnStartedAt, mcpTurnMetadata)
	markUserInputRequestedDuringTurn := func() {
		if mcpTurnMetadata != nil {
			mcpTurnMetadata[mcpTurnMetadataUserInputRequestedKey] = true
		}
	}
	if userInputRequests := a.ensureUserInputRequestTracker(); userInputRequests != nil {
		var previous func()
		previous = userInputRequests.SetCallback(func() {
			if previous != nil {
				previous()
			}
			markUserInputRequestedDuringTurn()
		})
		defer userInputRequests.SetCallback(previous)
	}
	maxToolIterations := configMaxToolIterations(a.Config)
	toolBudgetLimit := maxToolIterations
	toolBudgetExtensions := 0
	toolLoopLimitRecoveryUsed := false
	turnCount := 0
	disabledTools := map[string]bool{}
	if readOnlyAnalysis {
		disabledTools["apply_patch"] = true
		disabledTools["write_file"] = true
		disabledTools["replace_in_file"] = true
	}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if toolBudgetLimit > 0 && turnCount >= toolBudgetLimit {
			if shouldExtendToolBudget(a.Session.Messages, lastToolErrorCount, lastToolCallSignatureCount, lastReadFilePathTurns, toolBudgetExtensions) {
				extraTurns := nextToolBudgetExtension(maxToolIterations, toolBudgetExtensions)
				if extraTurns > 0 {
					toolBudgetExtensions++
					toolBudgetLimit += extraTurns
					if a.Config.AutoCompactChars > 0 && a.Session.ApproxChars() > a.Config.AutoCompactChars/2 {
						if _, err := a.CompactWithTrigger(ctx, "Auto-compacted to preserve important context while extending the tool budget after sustained progress.", "auto", "tool_budget_extension"); err != nil {
							return "", err
						}
					}
					recoveryRecent := summarizeRecentToolTurns(a.Session.Messages, 4)
					if a.EmitProgress != nil {
						a.EmitProgress(fmt.Sprintf("Recent tool turns show progress. Extending the tool budget by %d more turn(s)...", extraTurns))
					}
					a.Session.AddMessage(internalUserMessage(toolBudgetExtensionGuidance(extraTurns, recoveryRecent)))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					lastRecentToolTurns = recoveryRecent
					continue
				}
			}
			if toolLoopLimitRecoveryUsed {
				break
			}
			toolLoopLimitRecoveryUsed = true
			toolBudgetLimit++
			recoveryRecent := summarizeRecentToolTurns(a.Session.Messages, 3)
			if a.EmitProgress != nil {
				a.EmitProgress("Tool loop limit reached. Asking the model to replan or conclude without more tool churn...")
			}
			a.Session.AddMessage(internalUserMessage(a.recoveryGuidance(ctx, recoveryTriggerToolBudgetExceeded, recoveryInput{
				Summary: lastToolCallSummary,
				Recent:  recoveryRecent,
				Detail:  lastStopReason,
			})))
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			lastRecentToolTurns = recoveryRecent
			continue
		}
		turnCount++
		lastIteration = turnCount
		if a.Config.AutoCompactChars > 0 && a.Session.ApproxChars() > a.Config.AutoCompactChars {
			if _, err := a.CompactWithTrigger(ctx, "Auto-compacted due to context growth.", "auto", "context_growth"); err != nil {
				return "", err
			}
		}
		if err := a.syncTaskExecutorFocus(); err != nil {
			return "", err
		}
		toolExposurePlan := a.buildTurnToolExposurePlan(disabledTools, latestUser, unresolvedVerification, finalAnswerOnlyCorrection, verificationOutOfScopeFinalOnly, latestUserExplicitWebResearch, localCodeToolPolicyForTurn)
		if !toolExposurePlan.SuppressInteractiveWorkers {
			_ = a.maybeRunInteractiveParallelEditableWorkers(ctx, "executor")
			_ = a.maybeRunInteractiveParallelReadOnlyWorkers(ctx, "executor")
			_ = a.maybeRunInteractiveMicroWorkers(ctx, "executor")
		}
		turnDisabledTools := toolExposurePlan.DisabledTools
		generatedDocumentFinalOnly := toolExposurePlan.GeneratedDocumentFinalOnly
		onTextDelta := a.EmitAssistantDelta
		if a.shouldBufferAssistantDeltaForGatedTurn(unresolvedVerification, attemptedEditTool, successfulEditTool) {
			onTextDelta = nil
		}
		systemPrompt := a.systemPrompt()
		if finalAnswerOnlyCorrection {
			systemPrompt += "\n\n" + finalAnswerOnlyHarnessPromptGuidance()
		}
		if generatedDocumentFinalOnly {
			systemPrompt += "\n\n" + generatedDocumentArtifactFinalOnlyPromptGuidance()
		}
		turnReq := ChatRequest{
			Model:        a.Session.Model,
			System:       systemPrompt,
			Messages:     a.Session.Messages,
			Tools:        adaptToolDefinitionsForImageDetailSupport(a.Tools.DefinitionsExcluding(turnDisabledTools), a.Session.Provider, a.Session.Model),
			MaxTokens:    a.Config.MaxTokens,
			Temperature:  a.Config.Temperature,
			WorkingDir:   a.Session.WorkingDir,
			OnTextDelta:  onTextDelta,
			TurnState:    providerTurnState,
			TurnMetadata: providerTurnMetadata,
		}
		resp, err := a.completeModelTurn(ctx, turnReq)
		if err != nil && readOnlyAnalysis && isToolUseUnsupportedError(err) && len(turnReq.Tools) > 0 {
			if a.EmitProgress != nil {
				a.EmitProgress("Current model does not support tool use. Retrying without tools...")
			}
			turnReq.Tools = nil
			turnReq.System = turnReq.System + "\nThe selected model endpoint does not support tool use for this turn. Answer only from the attached context and conversation history. If the evidence is insufficient, say exactly what is missing."
			resp, err = a.completeModelTurn(ctx, turnReq)
		}
		if err != nil {
			_ = a.Store.Save(a.Session)
			if isToolUseUnsupportedError(err) {
				return "", fmt.Errorf("selected model does not support tool use for inspect/edit requests: provider=%s model=%s", strings.TrimSpace(a.Session.Provider), strings.TrimSpace(a.Session.Model))
			}
			return "", err
		}
		a.emitProviderResponseMetadata(turnReq, resp, &serverModelWarningEmitted, &modelVerificationEmitted)
		rawAssistantText := resp.Message.Text
		resp.Message.Text = sanitizeAssistantMessageText(rawAssistantText, len(resp.Message.ToolCalls) > 0)
		resp.Message.ToolCalls = assignFocusedOwnerNodeToToolCalls(resp.Message.ToolCalls, a.Session)
		resp.Message.ToolCalls = stripUnsupportedOwnerNodeIDFromToolCalls(resp.Message.ToolCalls)
		if toolRegistryHasTool(a.Tools, "apply_patch") {
			resp.Message.ToolCalls = rewriteShellApplyPatchToolCalls(resp.Message.ToolCalls)
		}
		if a.shouldPromoteFinalLookingToolPreambleToFinalCandidate(latestUser, resp.Message.ToolCalls, rawAssistantText, attemptedEditTool, successfulEditTool, unresolvedVerification) {
			if finalText := sanitizeAssistantMessageText(rawAssistantText, false); finalText != "" {
				resp.Message.Text = finalText
				resp.Message.ToolCalls = nil
			}
		}
		resp.Message.Phase = assistantMessagePhaseForModelResponse(resp.Message)
		deferEndTurnFollowUpForGeneratedDocument := a.shouldDeferEndTurnFollowUpForGeneratedDocument(latestUser, resp)
		deferEndTurnFollowUpForFinalLookingReply := shouldDeferEndTurnFollowUpForFinalLookingReply(resp)
		deferEndTurnFollowUpForPostWorkReply := a.shouldDeferEndTurnFollowUpForPostWorkReply(latestUser, resp, attemptedEditTool, successfulEditTool, unresolvedVerification)
		if chatResponseRequestsFollowUp(resp) && !deferEndTurnFollowUpForGeneratedDocument && !deferEndTurnFollowUpForFinalLookingReply && !deferEndTurnFollowUpForPostWorkReply && !finalAnswerOnlyCorrection && len(resp.Message.ToolCalls) == 0 && strings.TrimSpace(resp.Message.Text) != "" {
			resp.Message.Phase = messagePhaseCommentary
		}
		if shouldRetryKoreanLocalCodeToolNarration(resp.Message, a.Session, a.Config) {
			a.Session.AddMessage(internalUserMessage(koreanLocalCodeToolNarrationGuidance()))
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			continue
		}
		if shouldRetryPreFixEditWithoutVisibleReviewSummary(resp.Message, a.Session) {
			a.Session.AddMessage(internalUserMessage(preFixVisibleReviewSummaryGuidance(*a.Session.LastReviewRun)))
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			continue
		}
		if onTextDelta != nil && strings.TrimSpace(resp.Message.Text) != "" {
			a.lastEmittedText = strings.TrimSpace(resp.Message.Text)
		}
		lastStopReason = normalizeStopReason(resp.StopReason)
		a.Session.AddMessage(resp.Message)
		if resp.Message.Phase != messagePhaseFinalAnswerCandidate {
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
		}
		if len(resp.Message.ToolCalls) > 0 {
			commentaryOnlyReplies = 0
			if finalAnswerOnlyCorrection {
				if err := a.addToolCallRedirectGuidance(
					resp.Message.ToolCalls,
					"NOT_EXECUTED: pre-final coding harness requires a final-answer-only correction; no tools are available for this retry.",
					finalAnswerOnlyHarnessToolBlockedGuidance(a.Session.LastCodingHarnessReport),
				); err != nil {
					return "", err
				}
				continue
			}
			if !explicitGitRequest && hasMutatingGitToolCalls(resp.Message.ToolCalls) {
				if err := a.addToolCallRedirectGuidance(
					resp.Message.ToolCalls,
					"NOT_EXECUTED: git write actions require an explicit user request; continue without staging, committing, pushing, or opening a PR.",
					"Do not stage, commit, push, or open a PR unless the user explicitly asks for a git action first. Continue with inspection, edits, verification, and a summary instead.",
				); err != nil {
					return "", err
				}
				continue
			}
			if explicitGitRequest && hasMutatingGitToolCalls(resp.Message.ToolCalls) {
				blocked, feedback := a.runtimeGateFeedbackForAction(runtimeGateActionGitWrite)
				if blocked {
					runtimeGateGitWriteNudges++
					if runtimeGateGitWriteNudges > 3 {
						return "", fmt.Errorf("runtime gate still blocks git write after repeated attempts")
					}
					if err := a.addToolCallRedirectGuidance(
						resp.Message.ToolCalls,
						"NOT_EXECUTED: runtime gate blocked this git write action; follow the gate feedback before retrying.",
						feedback,
					); err != nil {
						return "", err
					}
					continue
				}
			}
			if replyBlamesInternalToolTranscriptRecovery(resp.Message.Text) {
				if err := a.addToolCallRedirectGuidance(
					resp.Message.ToolCalls,
					"NOT_EXECUTED: assistant text blamed internal transcript recovery; retry using the next guidance instead of executing this batch.",
					internalToolTranscriptFailureGuidance(explicitEditRequest),
				); err != nil {
					return "", err
				}
				continue
			}
			if toolCallsIncludeImplicitShellApplyPatchBody(resp.Message.ToolCalls) {
				if err := a.addToolCallRedirectGuidance(
					resp.Message.ToolCalls,
					"NOT_EXECUTED: apply_patch verification failed: raw patch body was provided to run_shell without an explicit apply_patch invocation.",
					"The previous shell command was a raw `*** Begin Patch` / `*** End Patch` body. Codex treats this as an implicit apply_patch invocation and does not run it as shell. Re-issue the edit with the `apply_patch` tool, or use a supported `apply_patch <<'PATCH'` heredoc command.",
				); err != nil {
					return "", err
				}
				continue
			}
			if !latestUserExplicitWebResearch &&
				(shouldBlockWebResearchForLocalCodeWork(resp.Message.ToolCalls, a.Session, a.MCP) ||
					(localCodeToolPolicyForTurn && toolCallsIncludeWebResearch(resp.Message.ToolCalls, a.MCP))) {
				a.recordExternalLookupIntents(resp.Message.ToolCalls, "blocked_local_code_context", true)
				if a.EmitProgress != nil {
					a.EmitProgress(formatBlockedLocalCodeWebResearchProgress(a.Config, resp.Message.ToolCalls))
				}
				if err := a.addToolCallRedirectGuidance(
					resp.Message.ToolCalls,
					"NOT_EXECUTED: external research tools are blocked for this local code repair turn unless the user explicitly asks for external research.",
					localCodeWebResearchBlockGuidance(a.Config, a.Session),
				); err != nil {
					return "", err
				}
				continue
			}
			if toolCallsIncludeWebResearch(resp.Message.ToolCalls, a.MCP) {
				a.recordExternalLookupIntents(resp.Message.ToolCalls, "tool_call_declared", false)
			}
			if shouldBlockLocalToolCallsBeforeWebResearch(resp.Message.ToolCalls, a.Session, a.MCP) {
				researchCatalog := ""
				if a.MCP != nil {
					researchCatalog = strings.TrimSpace(compactPromptSection(a.MCP.WebResearchCatalogPrompt(), 700))
				}
				guidance := "This request likely needs current external research. Before using local inspection or edit tools, first use a relevant MCP web/search/browser capability to gather current external sources, then return to local file work."
				if researchCatalog != "" {
					guidance += "\n\nRelevant web/research capabilities:\n" + researchCatalog
				}
				guidance += "\n\nRecommended flow:\n1. Break the topic into a few focused search facets.\n2. Use MCP web/search/browser tools to gather multiple current sources.\n3. Compare recency and source authority.\n4. Then inspect local files or write the requested document."
				if err := a.addToolCallRedirectGuidance(
					resp.Message.ToolCalls,
					"NOT_EXECUTED: local tools were deferred until required external research is gathered.",
					guidance,
				); err != nil {
					return "", err
				}
				continue
			}
			if reply, finalized, err := a.maybeFinalizeGeneratedDocumentArtifactToolCallPreamble(latestUser, resp.Message.ToolCalls, resp.Message.Text, attemptedEditTool, unresolvedVerification); err != nil {
				return "", err
			} else if finalized {
				return reply, nil
			}
			if a.shouldBlockGeneratedDocumentArtifactValidationToolCalls(latestUser, resp.Message.ToolCalls) {
				reason := "NOT_EXECUTED: generated document artifact turns do not run shell or review validation, planning, or additional inspection after the document is written."
				if a.prepareGeneratedDocumentArtifactFinalAfterBlockedToolCalls(latestUser, attemptedEditTool, unresolvedVerification) {
					reply, err := a.finalizeGeneratedDocumentArtifactAfterBlockedToolCalls(resp.Message.ToolCalls, attemptedEditTool, unresolvedVerification, reason)
					if err != nil {
						return "", err
					}
					return reply, nil
				}
				if err := a.addToolCallRedirectGuidance(
					resp.Message.ToolCalls,
					reason,
					generatedDocumentArtifactValidationToolGuidance(),
				); err != nil {
					return "", err
				}
				continue
			}
			if block, targetPath, parentPath := shouldBlockUnconfirmedDocumentReadToolCalls(resp.Message.ToolCalls, a.Session); block {
				if err := a.addToolCallRedirectGuidance(
					resp.Message.ToolCalls,
					"NOT_EXECUTED: document read was deferred until the target path is confirmed by listing the parent directory.",
					fmt.Sprintf("This request is document/report authoring work. Do not guess that generated files already exist and call read_file on them immediately. First use list_files on the parent directory %s to confirm whether %s actually exists. If the parent directory is empty or the file is absent, treat the document as not created yet and create or update it with edit tools instead.", parentPath, targetPath),
				); err != nil {
					return "", err
				}
				continue
			}
			if readOnlyAnalysis && allToolCallsAreEditTools(resp.Message.ToolCalls) {
				if err := a.addToolCallRedirectGuidance(
					resp.Message.ToolCalls,
					"NOT_EXECUTED: this is a read-only analysis turn; edit tools are blocked.",
					"This request is analysis-only. Do not edit files or call edit tools. Investigate the current code and logs, then answer with the root cause or findings.",
				); err != nil {
					return "", err
				}
				continue
			}
			currentSignature := ""
			if shouldTrackRepeatedToolCallSignature(resp.Message.ToolCalls) {
				currentSignature = toolCallSignature(resp.Message.ToolCalls)
			}
			if currentSignature != "" {
				lastToolCallSummary = summarizeToolCalls(resp.Message.ToolCalls)
				if currentSignature == lastToolCallSignature {
					lastToolCallSignatureCount++
				} else {
					lastToolCallSignature = currentSignature
					lastToolCallSignatureCount = 1
					repeatedToolCallNudges = 0
					repeatedToolCallRecoveryTurns = 0
				}
				if lastToolCallSignatureCount >= repeatedToolCallAbortThreshold {
					return "", fmt.Errorf("stopped after repeated identical tool calls")
				}
				if lastToolCallSignatureCount >= repeatedToolCallRecoveryThreshold && repeatedToolCallRecoveryTurns < 1 {
					repeatedToolCallRecoveryTurns++
					a.Session.AddMessage(internalUserMessage(a.recoveryGuidance(ctx, recoveryTriggerRepeatedToolCalls, recoveryInput{
						Summary: lastToolCallSummary,
						Recent:  summarizeRecentToolTurns(a.Session.Messages, 3),
						Detail:  lastToolCallSummary,
					})))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
					continue
				}
				if lastToolCallSignatureCount >= repeatedToolCallNudgeThreshold {
					if repeatedToolCallNudges < 1 {
						repeatedToolCallNudges++
						a.Session.AddMessage(internalUserMessage("You are repeating the same tool call sequence with the same arguments. Do not repeat it again unless the previous tool result explicitly requires it. Use a different next step or provide the final answer now."))
						if err := a.Store.Save(a.Session); err != nil {
							return "", err
						}
						continue
					}
				}
			}
			if readPath, ok := repeatedReadFilePathKey(resp.Message.ToolCalls); ok {
				if readPath == lastReadFilePath {
					lastReadFilePathTurns++
				} else {
					lastReadFilePath = readPath
					lastReadFilePathTurns = 1
					repeatedReadFilePathNudges = 0
					repeatedCachedReadFileNudges = 0
					repeatedReadFilePathRecoveryCount = 0
				}
				if lastReadFilePathTurns >= repeatedReadFilePathAbortTurns {
					return "", fmt.Errorf("stopped after repeatedly reading the same file without making progress: %s", readPath)
				}
				if lastReadFilePathTurns >= repeatedReadFilePathRecoveryThreshold && repeatedReadFilePathRecoveryCount < 1 {
					repeatedReadFilePathRecoveryCount++
					a.Session.AddMessage(internalUserMessage(a.recoveryGuidance(ctx, recoveryTriggerRepeatedReadFile, recoveryInput{
						Path:   readPath,
						Turns:  lastReadFilePathTurns,
						Recent: summarizeRecentToolTurns(a.Session.Messages, 3),
						Detail: readPath,
					})))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
					continue
				}
				if lastReadFilePathTurns >= repeatedReadFilePathNudgeTurns && repeatedReadFilePathNudges < 1 {
					repeatedReadFilePathNudges++
					a.Session.AddMessage(internalUserMessage(fmt.Sprintf("You have read the same file repeatedly across multiple tool turns: %s. Do not keep scanning more ranges from the same file unless a specific missing section is still required. Either explain what you found so far, switch to a different tool or file, or provide the final answer now.", readPath)))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
			} else {
				lastReadFilePath = ""
				lastReadFilePathTurns = 0
				repeatedReadFilePathNudges = 0
				repeatedCachedReadFileNudges = 0
				repeatedReadFilePathRecoveryCount = 0
			}
			preamble := strings.TrimSpace(resp.Message.Text)
			if a.EmitAssistant != nil {
				if preamble != "" {
					if preamble != a.lastEmittedText {
						a.EmitAssistant(preamble)
						a.lastEmittedText = preamble
					}
				} else {
					synth := synthesizeToolPreambleText(resp.Message.ToolCalls)
					if synth != a.lastEmittedText {
						a.EmitAssistant(synth)
						a.lastEmittedText = synth
					}
				}
			}
			if postEditFinalAnswerNudges > 0 && allToolCallsAreEditTools(resp.Message.ToolCalls) {
				a.Session.AddMessage(internalUserMessage("You have already made multiple rounds of edits. Do not call more edit tools unless the previous changes are clearly insufficient. If the requested work is complete, provide the final answer now and summarize what changed."))
				if err := a.Store.Save(a.Session); err != nil {
					return "", err
				}
				continue
			}
		}
		if len(resp.Message.ToolCalls) == 0 {
			lastToolCallSignature = ""
			lastToolCallSignatureCount = 0
			repeatedToolCallNudges = 0
			repeatedToolCallRecoveryTurns = 0
			lastReadFilePath = ""
			lastReadFilePathTurns = 0
			repeatedReadFilePathNudges = 0
			repeatedCachedReadFileNudges = 0
			repeatedReadFilePathRecoveryCount = 0
			if chatResponseRequestsFollowUp(resp) && !deferEndTurnFollowUpForGeneratedDocument && !deferEndTurnFollowUpForFinalLookingReply && !deferEndTurnFollowUpForPostWorkReply && !finalAnswerOnlyCorrection {
				commentaryOnlyReplies = 0
				if err := a.Store.Save(a.Session); err != nil {
					return "", err
				}
				continue
			}
			reply := strings.TrimSpace(resp.Message.Text)
			if resp.Message.Phase == messagePhaseCommentary {
				if a.shouldRouteCommentaryReplyThroughFinalGates(latestUser, reply, attemptedEditTool, successfulEditTool, unresolvedVerification) {
					resp.Message.Phase = messagePhaseFinalAnswerCandidate
					if len(a.Session.Messages) > 0 {
						a.Session.Messages[len(a.Session.Messages)-1].Phase = messagePhaseFinalAnswerCandidate
					}
				} else {
					if reply != "" {
						commentaryOnlyReplies = 0
						if a.EmitAssistant != nil && reply != a.lastEmittedText {
							a.EmitAssistant(reply)
							a.lastEmittedText = reply
						}
						a.finalizeTaskStateOnAcceptedFinalAnswer(reply, unresolvedVerification)
						a.finalizePatchTransactionOnReturn()
						a.finalizeEditLoopOnReturn(reply, unresolvedVerification)
						a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
						if err := a.Store.Save(a.Session); err != nil {
							return "", err
						}
						return reply, nil
					}
					commentaryOnlyReplies++
					if commentaryOnlyReplies >= 3 {
						return "", fmt.Errorf("model produced commentary-only assistant messages without tool calls or final answer")
					}
					a.Session.AddMessage(internalUserMessage("Your last assistant message was commentary/progress, not the final answer. Continue with the next needed tool call, or provide the final answer now. Do not repeat the same commentary-only message."))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
			}
			if reply != "" {
				commentaryOnlyReplies = 0
				if continuedReplyPrefix != "" {
					reply = mergeAssistantContinuation(continuedReplyPrefix, reply)
					continuedReplyPrefix = ""
					if continuedReplyMessageIndex >= 0 && continuedReplyMessageIndex < len(a.Session.Messages) {
						a.Session.Messages[continuedReplyMessageIndex].Text = reply
						a.Session.Messages = a.Session.Messages[:continuedReplyMessageIndex+1]
						continuedReplyMessageIndex = -1
					} else if len(a.Session.Messages) > 0 {
						a.Session.Messages[len(a.Session.Messages)-1].Text = reply
						if err := a.Store.Save(a.Session); err != nil {
							return "", err
						}
					}
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
				}
				if replyBlamesInternalToolTranscriptRecovery(reply) && internalToolTranscriptFailureReplyRetries < 1 {
					internalToolTranscriptFailureReplyRetries++
					a.discardRecentFinalAnswerCandidate(reply)
					a.Session.AddMessage(internalUserMessage(internalToolTranscriptFailureGuidance(explicitEditRequest)))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if verificationDeclinedThisTurn &&
					replyBlamesToolAvailabilityAfterSkippedVerification(reply) &&
					toolAvailabilityBlameReplyRetries < 1 {
					toolAvailabilityBlameReplyRetries++
					a.discardRecentFinalAnswerCandidate(reply)
					a.Session.AddMessage(internalUserMessage(toolAvailabilityAfterSkippedVerificationGuidance(a.Config)))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if localCodeToolPolicyForTurn &&
					toolRegistryHasLocalInspectionTools(a.Tools) &&
					replyBlamesLocalCodeToolAvailability(reply) &&
					localCodeToolAvailabilityBlameRetries < 1 {
					localCodeToolAvailabilityBlameRetries++
					a.discardRecentFinalAnswerCandidate(reply)
					a.Session.AddMessage(internalUserMessage(localCodeToolAvailabilityBlameGuidance(a.Config)))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if explicitEditRequest && !attemptedEditTool && replySuggestsManualEditHandoff(reply) && manualEditHandoffRetries < 1 {
					manualEditHandoffRetries++
					a.discardRecentFinalAnswerCandidate(reply)
					a.Session.AddMessage(internalUserMessage("This request explicitly asks you to inspect and fix the code. Do not hand the patch back to the user. Read the relevant file if needed, then use the available edit tools directly. Only ask the user to edit manually if an edit tool actually failed, and cite that exact tool error."))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if abruptReplyRetries < 1 && replyLooksAbruptlyTruncated(reply) {
					abruptReplyRetries++
					continuedReplyPrefix = reply
					continuedReplyMessageIndex = len(a.Session.Messages) - 1
					a.Session.AddMessage(internalUserMessage("Your last answer appears to have been cut off mid-sentence. Continue exactly from where you stopped and finish the answer. Do not restart from the beginning, do not apologize, and do not repeat the earlier text."))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if rawReviewResultReplyRetries < 1 && replyLooksLikeRawReviewHarnessResult(reply) {
					rawReviewResultReplyRetries++
					a.discardRecentFinalAnswerCandidate(reply)
					a.Session.AddMessage(internalUserMessage("Your last response was a raw internal REVIEW_RESULT block. Do not expose review harness result syntax to the user as the final answer. Provide a normal user-facing final answer that summarizes what changed, verification status, and any remaining blockers or risks."))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if unresolvedVerification && !a.shouldLetGeneratedDocumentArtifactHarnessHandleSkippedVerification(latestUser) && finalAnswerNudges < 1 && !replyMentionsVerificationBlocker(reply) && !replyMentionsVerificationNotRun(reply) {
					finalAnswerNudges++
					a.discardRecentFinalAnswerCandidate(reply)
					a.Session.AddMessage(internalUserMessage("Verification is still unresolved. Continue fixing the issue if possible. If verification was skipped or declined, give a final answer that explicitly says verification was not run and do not describe it as completed."))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				stopVerdict, err := a.runStopHook(ctx, reply, stopHookActive, stopHookTurnID)
				if err != nil {
					return "", err
				}
				if stopHookShouldBlock(stopVerdict) {
					if stopHookRevisions >= maxStopHookRevisions {
						return "", fmt.Errorf("Stop hook kept blocking final answer after %d continuation attempt(s): %s", stopHookRevisions, strings.TrimSpace(stopVerdict.StopMessage))
					}
					stopHookRevisions++
					stopHookActive = true
					finalAnswerOnlyCorrection = false
					a.discardRecentFinalAnswerCandidate(reply)
					a.Session.AddMessage(internalUserMessage(stopHookContinuationGuidance(stopVerdict)))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if verificationOutOfScopeFinalOnly {
					reply = a.ensureOutOfScopeVerificationFinalDisclosure(reply)
					if continuedReplyMessageIndex >= 0 && continuedReplyMessageIndex < len(a.Session.Messages) {
						a.Session.Messages[continuedReplyMessageIndex].Text = reply
					} else if len(a.Session.Messages) > 0 {
						a.Session.Messages[len(a.Session.Messages)-1].Text = reply
					}
					a.acceptRecentFinalAnswerCandidate(reply)
					a.finalizeTaskStateOnAcceptedFinalAnswer(reply, false)
					a.finalizePatchTransactionOnReturn()
					a.finalizeEditLoopOnReturn(reply, false)
					a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					return reply, nil
				}
				harnessApproved, harnessFeedback := a.runPreFinalCodingHarnesses(ctx, reply, attemptedEditTool, unresolvedVerification)
				if !harnessApproved && a.shouldSynthesizeGeneratedDocumentArtifactFinalReply(latestUser, a.Session.LastCodingHarnessReport, unresolvedVerification) {
					a.discardRecentFinalAnswerCandidate(reply)
					reply = a.synthesizeGeneratedDocumentArtifactFinalReply(a.Session.LastCodingHarnessReport)
					synthesizedReport := a.buildCodingHarnessReport(reply, attemptedEditTool, unresolvedVerification)
					a.Session.LastCodingHarnessReport = &synthesizedReport
					a.Session.LastTestImpactReport = &synthesizedReport.TestImpact
					a.Session.LastJobSupervisorReport = &synthesizedReport.JobSupervisor
					if !synthesizedReport.Approved {
						reply = generatedDocumentArtifactHarnessBlockedReply(&synthesizedReport)
					}
					a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
					a.finalizeTaskStateOnAcceptedFinalAnswer(reply, unresolvedVerification)
					a.finalizePatchTransactionOnReturn()
					a.finalizeEditLoopOnReturn(reply, unresolvedVerification)
					a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					return reply, nil
				}
				if !harnessApproved && finalHarnessRevisions >= 2 {
					a.discardRecentFinalAnswerCandidate(reply)
					if a.changesAreGeneratedDocumentArtifactsForTurn(latestUser) {
						reply = generatedDocumentArtifactHarnessBlockedReply(a.Session.LastCodingHarnessReport)
					} else {
						reply = preFinalCodingHarnessBlockedReply(a.Session.LastCodingHarnessReport)
					}
					a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
					a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					return reply, nil
				}
				if !harnessApproved && finalHarnessRevisions < 2 {
					finalHarnessRevisions++
					a.discardRecentFinalAnswerCandidate(reply)
					finalAnswerOnlyCorrection = codingHarnessReportRequiresFinalAnswerOnlyRevision(a.Session.LastCodingHarnessReport)
					nextGuidance := harnessFeedback
					if finalAnswerOnlyCorrection {
						nextGuidance = finalAnswerOnlyHarnessRevisionGuidance(a.Session.LastCodingHarnessReport, harnessFeedback)
					}
					a.Session.AddMessage(internalUserMessage(nextGuidance))
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if reply, finalized, err := a.maybeFinalizeGeneratedDocumentArtifactFinalReply(latestUser, reply, attemptedEditTool, unresolvedVerification); finalized || err != nil {
					return reply, err
				}
				if successfulEditTool && !a.shouldSkipPostChangeReviewForKnownFinalBlocker(reply, unresolvedVerification) {
					needsModelTurn, err := a.runAutomaticPostChangeReviewGate(ctx, latestUser, &lastPostChangeReviewFingerprint, &postChangeReviewRevisions, &postChangeReviewExhaustedNudge)
					if err != nil {
						return "", err
					}
					if needsModelTurn {
						finalAnswerOnlyCorrection = false
						a.discardRecentFinalAnswerCandidate(reply)
						if err := a.Store.Save(a.Session); err != nil {
							return "", err
						}
						continue
					}
					if reply, finalized, err := a.maybeFinalizeGeneratedDocumentArtifactFinalReply(latestUser, reply, attemptedEditTool, unresolvedVerification); finalized || err != nil {
						return reply, err
					}
				}
				if runtimeGateFinalAnswerRevisions < 2 {
					ledger := a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
					if runtimeGateBlocksFinalAnswer(ledger, reply) {
						runtimeGateFinalAnswerRevisions++
						finalAnswerOnlyCorrection = false
						a.discardRecentFinalAnswerCandidate(reply)
						a.Session.AddMessage(internalUserMessage(renderRuntimeGateBlockedFeedback(ledger, runtimeGateActionFinalAnswer)))
						if err := a.Store.Save(a.Session); err != nil {
							return "", err
						}
						continue
					}
				} else {
					a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
				}
				if !a.shouldSkipInteractiveFinalAnswerReviewForGeneratedDocumentArtifact(latestUser, unresolvedVerification) &&
					a.shouldReviewInteractiveFinalAnswer(reply, attemptedEditTool, unresolvedVerification) &&
					finalAnswerReviewRevisions < 2 &&
					!strings.EqualFold(strings.TrimSpace(reply), strings.TrimSpace(lastReviewedFinalAnswer)) {
					approved, reviewText := a.reviewInteractiveFinalAnswer(ctx, reply, unresolvedVerification)
					lastReviewedFinalAnswer = reply
					if !approved {
						finalAnswerReviewRevisions++
						finalAnswerOnlyCorrection = false
						a.discardRecentFinalAnswerCandidate(reply)
						nextText := "Reviewer feedback: the proposed final answer is not ready yet. Revise the work or the answer before concluding."
						if strings.TrimSpace(reviewText) != "" {
							nextText += "\n\n" + strings.TrimSpace(reviewText)
						}
						a.Session.AddMessage(internalUserMessage(nextText))
						if err := a.Store.Save(a.Session); err != nil {
							return "", err
						}
						continue
					}
				}
				a.acceptRecentFinalAnswerCandidate(reply)
				a.finalizeTaskStateOnAcceptedFinalAnswer(reply, unresolvedVerification)
				a.finalizePatchTransactionOnReturn()
				a.finalizeEditLoopOnReturn(reply, unresolvedVerification)
				a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
				if err := a.Store.Save(a.Session); err != nil {
					return "", err
				}
				return reply, nil
			}
			if isTokenLimitStopReason(lastStopReason) {
				return "", fmt.Errorf("model stopped before producing a usable response due to token limit (stop_reason=%s)", lastStopReason)
			}
			emptyFinalReplies++
			if emptyFinalReplies >= 2 {
				return "", formatEmptyModelResponseError(a.Session, lastStopReason, sawToolResultThisTurn)
			}
			if readOnlyAnalysis {
				a.Session.AddMessage(internalUserMessage("Your last reply was empty. This is a read-only analysis or review request. If you need more evidence, use read_file, grep, or list_files on the referenced code first. Then provide a concrete final answer with findings, likely root causes, and file references. Do not return an empty message."))
			} else {
				a.Session.AddMessage(internalUserMessage("Please provide the final answer to the user now. Do not return an empty message."))
			}
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			continue
		}
		emptyFinalReplies = 0
		finalAnswerNudges = 0
		edited := false
		iterationToolError := ""
		iterationHadToolSuccess := false
		deferEditToolsInBatch := shouldDeferEditToolsInToolCallBatch(resp.Message.ToolCalls)
		deferredMixedTools := false
		preWriteBlockedRetryQueued := false
		editMismatchRetryQueued := false
		preWriteForceEditQueued := false
		toolRetryQueued := false
		toolMsgIndexes, saveErr := a.beginToolExecutions(resp.Message.ToolCalls)
		if saveErr != nil {
			return "", saveErr
		}
		parallelBatchExecuted := false
		if !deferEditToolsInBatch &&
			!verificationOutOfScopeFinalOnly &&
			!verificationDeclinedThisTurn &&
			!verificationOutOfScopeThisTurn &&
			!preWriteReviewRequiresReanchor &&
			!editTargetMismatchRequiresReanchor &&
			preWriteReviewRepairBlocks == 0 &&
			a.shouldExecuteToolCallBatchInParallel(resp.Message.ToolCalls, readOnlyAnalysis, turnDisabledTools) {
			outcome, err := a.executeParallelToolCallBatch(ctx, resp.Message.ToolCalls, toolMsgIndexes, mcpTurnMetadata)
			if err != nil {
				return "", err
			}
			sawToolResultThisTurn = true
			iterationHadToolSuccess = outcome.hadSuccess
			iterationToolError = outcome.errorText
			parallelBatchExecuted = true
		}
		if parallelBatchExecuted {
			goto toolLoopCompleted
		}
		for callIndex, call := range resp.Message.ToolCalls {
			if err := ctx.Err(); err != nil {
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex, "NOT_EXECUTED: context canceled before this tool could run.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			toolMsgIndex := -1
			if callIndex >= 0 && callIndex < len(toolMsgIndexes) {
				toolMsgIndex = toolMsgIndexes[callIndex]
			}
			if deferEditToolsInBatch && shouldDeferToolCallInMixedEditBatch(call) {
				result := deferredMixedToolResult(call)
				toolMsg := Message{
					Role:       "tool",
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Text:       result.DisplayText,
					ToolMeta:   result.Meta,
					IsError:    true,
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.noteToolConversationBlockedResult(call, result, nil)
				sawToolResultThisTurn = true
				deferredMixedTools = true
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				continue
			}
			if verificationOutOfScopeFinalOnly {
				result := outOfScopeVerificationFinalOnlyBlockedResult(call)
				toolMsg := Message{
					Role:       "tool",
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Text:       result.DisplayText,
					ToolMeta:   result.Meta,
					IsError:    true,
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.noteToolConversationBlockedResult(call, result, errVerificationOutOfScopeFollowupBlocked)
				a.noteToolExecutionResultDetailed(call, result, errVerificationOutOfScopeFollowupBlocked)
				if a.EmitProgress != nil {
					a.EmitProgress(localizedText(a.Config, "Tool call blocked: verification already failed outside the current patch scope; final answer only.", "도구 호출을 차단했습니다: 검증이 현재 patch scope 밖에서 실패했으므로 최종 답변만 허용합니다."))
				}
				sawToolResultThisTurn = true
				a.Session.AddMessage(internalUserMessage(verificationOutOfScopeFinalOnlyGuidance(a.Config)))
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: automatic verification already failed outside the current patch scope; no further tools are available in this turn, provide the final answer.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				lastToolError = ""
				lastToolErrorCount = 0
				break
			}
			if verificationDeclinedThisTurn && toolCallIsVerificationRetryOrPoll(call, a.Session) {
				result := declinedVerificationFollowupBlockedResult(call)
				toolMsg := Message{
					Role:       "tool",
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Text:       result.DisplayText,
					ToolMeta:   result.Meta,
					IsError:    false,
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.noteToolConversationBlockedResult(call, result, nil)
				a.noteToolExecutionResultDetailed(call, result, nil)
				sawToolResultThisTurn = true
				a.Session.AddMessage(internalUserMessage(verificationFollowupBlockedGuidance(a.Config)))
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: an earlier verification command was already skipped or declined in this turn; do not retry verification until the user explicitly approves it.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				lastToolError = ""
				lastToolErrorCount = 0
				break
			}
			if verificationOutOfScopeThisTurn && toolCallIsVerificationRetryOrPoll(call, a.Session) {
				result := outOfScopeVerificationFollowupBlockedResult(call)
				toolMsg := Message{
					Role:       "tool",
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Text:       result.DisplayText,
					ToolMeta:   result.Meta,
					IsError:    true,
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.noteToolConversationBlockedResult(call, result, errVerificationOutOfScopeFollowupBlocked)
				a.noteToolExecutionResultDetailed(call, result, errVerificationOutOfScopeFollowupBlocked)
				sawToolResultThisTurn = true
				a.Session.AddMessage(internalUserMessage(verificationOutOfScopeFollowupBlockedGuidance(a.Config)))
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: automatic verification already failed outside the current patch scope; do not retry build, test, or verification commands in this turn.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				lastToolError = ""
				lastToolErrorCount = 0
				break
			}
			if preWriteReviewRequiresReanchor && isEditTool(call.Name) {
				preWriteReviewReanchorBlocks++
				result := preWriteReviewReanchorRequiredResult(call)
				toolMsg := Message{
					Role:       "tool",
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Text:       result.DisplayText,
					ToolMeta:   result.Meta,
					IsError:    true,
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.noteToolConversationBlockedResult(call, result, errPreWriteReviewReanchorRequired)
				a.noteToolExecutionResultDetailed(call, result, errPreWriteReviewReanchorRequired)
				sawToolResultThisTurn = true
				a.Session.AddMessage(internalUserMessage(preWriteReviewReanchorRequiredGuidance(a.Config)))
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: a previous edit proposal was blocked before writing; re-read the current file or current diff before issuing another edit.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				lastToolError = ""
				lastToolErrorCount = 0
				preWriteBlockedRetryQueued = true
				break
			}
			if editTargetMismatchRequiresReanchor && isEditTool(call.Name) {
				editTargetMismatchReanchorBlocks++
				result := editTargetMismatchReanchorRequiredResult(call)
				toolMsg := Message{
					Role:       "tool",
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Text:       result.DisplayText,
					ToolMeta:   result.Meta,
					IsError:    true,
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.noteToolConversationBlockedResult(call, result, ErrEditTargetMismatch)
				a.noteToolExecutionResultDetailed(call, result, ErrEditTargetMismatch)
				sawToolResultThisTurn = true
				if editTargetMismatchReanchorBlocks > maxEditTargetMismatchReanchorBlocks {
					a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: a previous edit targeted stale or mismatched file contents and edit retries continued without re-anchoring.")
					reply := formatEditTargetMismatchReanchorLoopLimitReply(a.Config, a.Session)
					a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					return reply, nil
				}
				a.Session.AddMessage(internalUserMessage(editTargetMismatchReanchorRequiredGuidance(a.Config)))
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: a previous edit targeted stale or mismatched file contents; re-anchor on the current file or diff before issuing another edit.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				lastToolError = ""
				lastToolErrorCount = 0
				editMismatchRetryQueued = true
				break
			}
			if preWriteReviewRepairBlocks > 0 && !isEditTool(call.Name) {
				preWriteReviewRepairInspectTools++
				if preWriteReviewRepairInspectTools > maxPreWriteReviewRepairInspectTools {
					toolMsg := Message{
						Role:       "tool",
						ToolCallID: call.ID,
						ToolName:   call.Name,
						Text:       "NOT_EXECUTED: pre-write repair inspection budget was exhausted; retry from the next model turn with an edit tool or final answer.",
						IsError:    true,
					}
					a.setToolExecutionResult(toolMsgIndex, toolMsg)
					sawToolResultThisTurn = true
					if preWriteReviewRepairInspectNudges < maxPreWriteReviewRepairInspectNudges {
						preWriteReviewRepairInspectNudges++
						preWriteReviewRepairInspectTools = 0
						a.Session.AddMessage(internalUserMessage(formatPreWriteReviewRepairForceEditGuidance(a.Config, a.Session, summarizeRecentToolTurns(a.Session.Messages, 4))))
						a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: pre-write repair inspection budget was exhausted; retry from the next model turn with an edit tool or final answer.")
						if saveErr := a.Store.Save(a.Session); saveErr != nil {
							return "", saveErr
						}
						lastToolError = ""
						lastToolErrorCount = 0
						lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
						preWriteForceEditQueued = true
						break
					}
					a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: pre-write repair inspection budget was exhausted; the repair loop is asking the user how to proceed.")
					reply := formatPreWriteReviewRepairInspectionLoopLimitReply(a.Config, a.Session)
					a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					return reply, nil
				}
			}
			if summary := summarizeToolInvocation(a.Config, call); summary != "" {
				a.emitProgressEvent(ProgressEvent{
					Kind:             progressKindToolStarted,
					Message:          summary,
					ToolName:         call.Name,
					ToolCallID:       call.ID,
					ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
				})
			}
			a.noteToolConversationStart(call)
			a.noteToolExecutionStart(call)
			if isEditTool(call.Name) {
				attemptedEditTool = true
				lastPostChangeReviewFingerprint = ""
				postChangeReviewExhaustedNudge = false
			}
			var result ToolExecutionResult
			var err error
			blockedToolResult := false
			if readOnlyAnalysis && !a.toolCallAllowedInReadOnlyAnalysis(call) {
				result = readOnlyAnalysisToolBlockedResult(a.Config, call)
				err = fmt.Errorf("%w: %s", errReadOnlyAnalysisToolBlocked, strings.TrimSpace(call.Name))
				blockedToolResult = true
			} else if turnDisabledTools[strings.TrimSpace(call.Name)] {
				result = turnDisabledToolBlockedResult(a.Config, call)
				err = fmt.Errorf("%w: %s", errTurnDisabledToolBlocked, strings.TrimSpace(call.Name))
				blockedToolResult = true
			} else if isolationErr := a.checkUserChangeIsolationBeforeTool(call); isolationErr != nil {
				err = isolationErr
				result = userChangeIsolationToolResult(call, isolationErr)
			} else {
				patchProbe := a.beginPatchTransactionToolProbe(call)
				toolCtx := contextWithOriginalImageDetailSupport(ctx, canRequestOriginalImageDetail(a.Session.Provider, a.Session.Model))
				toolCtx = contextWithImageInputSupport(toolCtx, canRequestImageInput(a.Session.Provider, a.Session.Model))
				toolCtx = a.contextWithMCPToolInvocationMetadata(toolCtx, mcpTurnMetadata)
				toolCtx = contextWithToolCallHookMetadata(toolCtx, call)
				result, err = a.Tools.ExecuteDetailed(toolCtx, call.Name, call.Arguments)
				result = sanitizeToolExecutionImageDetailForModel(result, a.Session.Provider, a.Session.Model)
				a.finishPatchTransactionToolProbe(patchProbe, call, result, err)
			}
			a.rebaselineUserChangeIsolationFromRead(call, err)
			sawToolResultThisTurn = true
			if err == nil && !blockedToolResult {
				a.noteToolConversationResult(call, result)
			} else {
				a.noteToolConversationFailureResult(call, result, err, blockedToolResult)
			}
			toolMsg := Message{
				Role:             "tool",
				ToolCallID:       call.ID,
				ToolName:         call.Name,
				Text:             toolExecutionModelText(result),
				ToolContentItems: toolExecutionModelContentItems(result),
				ToolMeta:         result.Meta,
			}
			if blockedToolResult {
				toolMsg.IsError = true
			}
			if toolMetaVerificationWasSkipped(result.Meta, result.DisplayText) {
				verificationDeclinedThisTurn = true
			}
			a.setToolExecutionResult(toolMsgIndex, toolMsg)
			a.noteToolExecutionResultDetailed(call, result, err)
			accounting := a.accountGoalProgressAfterTool(call)
			if accounting.StatusChanged && accounting.Goal.Status == goalStatusBudgetLimited {
				a.Session.AddMessage(internalUserMessage(goalBudgetLimitContextMessage(accounting.Goal)))
			}
			if err == nil && preWriteReviewRequiresReanchor && preWriteReviewReanchorTool(call) {
				preWriteReviewRequiresReanchor = false
				preWriteReviewReanchorBlocks = 0
			}
			if err == nil && editTargetMismatchRequiresReanchor && editTargetMismatchReanchorTool(call) {
				editTargetMismatchRequiresReanchor = false
				editTargetMismatchReanchorBlocks = 0
				delete(disabledTools, "replace_in_file")
			}
			if saveErr := a.Store.Save(a.Session); saveErr != nil {
				return "", saveErr
			}
			if err := ctx.Err(); err != nil {
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: context canceled before this tool could run.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && errors.Is(err, ErrEditCanceled) {
				toolMsg.Text = "CANCELED: user canceled the edit preview. No files were changed."
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: an earlier edit was canceled before this tool could run.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && errors.Is(err, ErrWriteDenied) {
				toolMsg.Text = "CANCELED: user declined write approval. No files were changed, and no filesystem permission issue was detected."
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: an earlier write approval was declined before this tool could run.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && errors.Is(err, ErrInvalidEditPayload) {
				toolMsg.IsError = true
				toolMsg.Text = toolExecutionModelTextWithError(result, err)
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: an earlier edit payload was invalid before this tool could run.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && isPreWriteReviewBlockedError(err) {
				preWriteReviewRepairBlocks++
				preWriteReviewRepairInspectTools = 0
				preWriteReviewRepairInspectNudges = 0
				preWriteReviewRequiresReanchor = true
				blockFingerprint := preWriteReviewRepairBlockFingerprint(a.Session, err)
				if blockFingerprint != "" {
					preWriteReviewRepairBlockFingerprints[blockFingerprint]++
				}
				repeatedPreWriteBlock := blockFingerprint != "" && preWriteReviewRepairBlockFingerprints[blockFingerprint] > 1
				if repeatedPreWriteBlock || preWriteReviewRepairBlocks > maxPreWriteReviewRepairBlocksPerTurn {
					toolMsg.IsError = true
					toolMsg.Text = toolExecutionModelTextWithError(result, err)
					if summary := summarizeToolFailure(a.Config, call, err); summary != "" {
						a.emitProgressEvent(ProgressEvent{
							Kind:             progressKindToolFailed,
							Message:          summary,
							ToolName:         call.Name,
							ToolCallID:       call.ID,
							ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
							Status:           firstNonEmptyLine(err.Error()),
						})
					}
					a.setToolExecutionResult(toolMsgIndex, toolMsg)
					a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: repeated pre-write review failures stopped this tool-call batch before this tool could run.")
					reply := formatPreWriteReviewRepairLoopLimitReply(a.Config, a.Session)
					a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					return reply, nil
				}
				toolMsg.IsError = true
				toolMsg.Text = toolExecutionModelTextWithError(result, err)
				if summary := summarizeToolFailure(a.Config, call, err); summary != "" {
					a.emitProgressEvent(ProgressEvent{
						Kind:             progressKindToolFailed,
						Message:          summary,
						ToolName:         call.Name,
						ToolCallID:       call.ID,
						ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
						Status:           firstNonEmptyLine(err.Error()),
					})
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				disabledTools["replace_in_file"] = true
				a.Session.AddMessage(internalUserMessage(formatPreWriteReviewBlockedRetryGuidance(a.Config, a.Session)))
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: an earlier edit in this model response was blocked by pre-write review; retry from the next model turn.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				lastToolError = ""
				lastToolErrorCount = 0
				preWriteBlockedRetryQueued = true
				break
			}
			if err != nil && errors.Is(err, ErrEditTargetMismatch) {
				editTargetMismatchFailures++
				if editTargetMismatchFailures > maxEditTargetMismatchFailuresPerTurn {
					toolMsg.IsError = true
					toolMsg.Text = toolExecutionModelTextWithError(result, err)
					if summary := summarizeToolFailure(a.Config, call, err); summary != "" {
						a.emitProgressEvent(ProgressEvent{
							Kind:             progressKindToolFailed,
							Message:          summary,
							ToolName:         call.Name,
							ToolCallID:       call.ID,
							ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
							Status:           firstNonEmptyLine(err.Error()),
						})
					}
					a.setToolExecutionResult(toolMsgIndex, toolMsg)
					a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: repeated edit target mismatches stopped this tool-call batch before this tool could run.")
					reply := formatEditTargetMismatchLoopLimitReply(a.Config, a.Session)
					a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					return reply, nil
				}
			}
			if err != nil && errors.Is(err, ErrInvalidToolArgumentsJSON) && invalidToolArgsRetries < 1 {
				toolMsg.IsError = true
				toolMsg.Text = toolExecutionModelTextWithError(result, err)
				if summary := summarizeToolFailure(a.Config, call, err); summary != "" {
					a.emitProgressEvent(ProgressEvent{
						Kind:             progressKindToolFailed,
						Message:          summary,
						ToolName:         call.Name,
						ToolCallID:       call.ID,
						ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
						Status:           firstNonEmptyLine(err.Error()),
					})
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				if toolShouldBeDisabledAfterInvalidJSON(call.Name) {
					disabledTools[strings.TrimSpace(call.Name)] = true
				}
				a.Session.AddMessage(internalUserMessage(invalidToolArgumentsGuidance(call.Name)))
				a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: an earlier tool call had invalid JSON arguments; retry from the next model turn.")
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				invalidToolArgsRetries++
				lastToolError = ""
				lastToolErrorCount = 0
				toolRetryQueued = true
				break
			}
			if err != nil && errors.Is(err, ErrEditTargetMismatch) && editTargetMismatchRetries < 1 {
				toolMsg.IsError = true
				toolMsg.Text = toolExecutionModelTextWithError(result, err)
				if summary := summarizeToolFailure(a.Config, call, err); summary != "" {
					a.emitProgressEvent(ProgressEvent{
						Kind:             progressKindToolFailed,
						Message:          summary,
						ToolName:         call.Name,
						ToolCallID:       call.ID,
						ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
						Status:           firstNonEmptyLine(err.Error()),
					})
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				if readOnlyInspectionToolName(call.Name) {
					a.Session.AddMessage(internalUserMessage("The last read-only inspection tool was blocked by editable ownership routing. This is not a stale patch problem. Retry the same local inspection without owner_node_id, or inspect the main workspace path directly with read_file, list_files, grep, git_status, or git_diff. Do not switch to web research, do not create a replacement report from partial evidence, and do not attempt an edit until local evidence reads succeed."))
					a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: a read-only lookup was blocked by editable ownership routing; retry local inspection without owner_node_id from the next model turn.")
				} else {
					disabledTools["replace_in_file"] = true
					a.Session.AddMessage(internalUserMessage("Your last edit targeted stale or mismatched file contents. This is still local code review/repair work. Do not use MCP web/search/browser tools or external web research. Do not repeat or lightly reformat the previous patch text. First read the exact file again from the same path, confirm the current contents, and compare that fresh read with the tool error's expected/current context diagnostics. After that re-anchor, build a cohesive standalone apply_patch against the current workspace state. The patch may include multiple related hunks or files when that is the smallest complete repair for the root cause; do not split only because the previous attempt mismatched. If the resolved path points into a different worktree or administrative worktree directory, correct the path before editing."))
					a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: an earlier edit in this model response targeted stale file contents; retry from the next model turn.")
				}
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				editTargetMismatchRetries++
				editTargetMismatchRequiresReanchor = true
				lastToolError = ""
				lastToolErrorCount = 0
				editMismatchRetryQueued = true
				break
			}
			if err != nil {
				a.noteToolConversationError(call, err, result.DisplayText)
				toolMsg.IsError = true
				toolMsg.Text = toolExecutionModelTextWithError(result, err)
				if errors.Is(err, ErrReviewerGateUnavailable) {
					if summary := summarizeToolFailure(a.Config, call, err); summary != "" {
						a.emitProgressEvent(ProgressEvent{
							Kind:             progressKindToolFailed,
							Message:          summary,
							ToolName:         call.Name,
							ToolCallID:       call.ID,
							ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
							Status:           firstNonEmptyLine(err.Error()),
						})
					}
					a.setToolExecutionResult(toolMsgIndex, toolMsg)
					a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: reviewer gate stopped this tool-call batch before this tool could run.")
					reply := localizedText(a.Config,
						"The pre-write reviewer gate did not produce reliable evidence, so I stopped the edit. No code changes were applied.",
						"쓰기 전 리뷰어 게이트가 신뢰 가능한 근거를 만들지 못해서 편집을 중단했습니다. 코드 수정은 적용하지 않았습니다.",
					)
					if a.Session != nil && a.Session.LastReviewRun != nil {
						reply = formatReviewerGateUnavailableUserDecisionReply(a.Config, a.Session)
						if reviewRunHasActionableNonReviewerFindingsFromSession(a.Session) && a.PromptContinueReviewRepair == nil {
							recordPendingReviewerGateRepairConfirmation(a.Session)
						}
					}
					if a.PromptContinueReviewRepair != nil && a.Session != nil && a.Session.LastReviewRun != nil {
						if !reviewRunHasActionableNonReviewerFindingsFromSession(a.Session) {
							a.Session.PendingReviewRepairConfirm = nil
							a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
							if saveErr := a.Store.Save(a.Session); saveErr != nil {
								return "", saveErr
							}
							return reply, nil
						}
						promptText := formatReviewerGateUnavailableUserDecisionPrompt(a.Config, a.Session)
						markUserInputRequestedDuringTurn()
						continueRepair, promptErr := a.PromptContinueReviewRepair(promptText)
						if promptErr != nil {
							return "", promptErr
						}
						a.Session.PendingReviewRepairConfirm = nil
						if !continueRepair {
							reply = formatCancelledPendingReviewRepairReply(a.Config)
							a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
							if saveErr := a.Store.Save(a.Session); saveErr != nil {
								return "", saveErr
							}
							return reply, nil
						}
						if !a.primeReviewerGateRepairFromLastReview(latestUser) {
							reply = formatNoActionableReviewerGateRepairReply(a.Config)
							a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
							if saveErr := a.Store.Save(a.Session); saveErr != nil {
								return "", saveErr
							}
							return reply, nil
						}
						if saveErr := a.Store.Save(a.Session); saveErr != nil {
							return "", saveErr
						}
						lastToolError = ""
						lastToolErrorCount = 0
						toolRetryQueued = true
						break
					}
					a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					return reply, nil
				}
				currentError := strings.TrimSpace(err.Error())
				if call.Name == "run_shell" {
					currentError = strings.TrimSpace(call.Name + ": " + toolMsg.Text + "\n" + err.Error())
				}
				if currentError != "" {
					iterationToolError = currentError
				}
				if summary := summarizeToolFailure(a.Config, call, err); summary != "" {
					a.emitProgressEvent(ProgressEvent{
						Kind:             progressKindToolFailed,
						Message:          summary,
						ToolName:         call.Name,
						ToolCallID:       call.ID,
						ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
						Status:           firstNonEmptyLine(err.Error()),
					})
				}
				if call.Name == "apply_patch" && errors.Is(err, ErrInvalidPatchFormat) && patchFormatRetries < 2 {
					signature := applyPatchFormatFailureSignature(call.Arguments)
					repeatedSignature := signature != "" && signature == lastPatchFormatFailureSignature
					lastPatchFormatFailureSignature = signature
					patchFormatRetries++
					a.setToolExecutionResult(toolMsgIndex, toolMsg)
					a.Session.AddMessage(internalUserMessage(invalidPatchFormatGuidance(repeatedSignature, err)))
					a.setRemainingToolCallsNotExecuted(resp.Message.ToolCalls, toolMsgIndexes, callIndex+1, "NOT_EXECUTED: an earlier apply_patch call had invalid patch format; retry from the next model turn.")
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					lastToolError = ""
					lastToolErrorCount = 0
					toolRetryQueued = true
					break
				}
			} else {
				iterationHadToolSuccess = true
				if toolResultAttemptedWorkspaceEdit(call.Name, result.Meta) {
					attemptedEditTool = true
				}
				if toolResultRepresentsWorkspaceEdit(call.Name, result.Meta) {
					edited = true
					successfulEditTool = true
					finalAnswerOnlyCorrection = false
					finalHarnessRevisions = 0
					runtimeGateFinalAnswerRevisions = 0
					finalAnswerReviewRevisions = 0
					lastReviewedFinalAnswer = ""
					preWriteReviewRepairBlocks = 0
					preWriteReviewRepairBlockFingerprints = map[string]int{}
					preWriteReviewRepairInspectTools = 0
					preWriteReviewRepairInspectNudges = 0
					preWriteReviewRequiresReanchor = false
					preWriteReviewReanchorBlocks = 0
					lastPostChangeReviewFingerprint = ""
					postChangeReviewExhaustedNudge = false
				}
				if isEditTool(call.Name) {
					if summary := summarizeEditToolResult(call.Name, result.DisplayText); summary != "" {
						a.emitProgressEvent(ProgressEvent{
							Kind:             progressKindToolCompleted,
							Message:          summary,
							ToolName:         call.Name,
							ToolCallID:       call.ID,
							ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
						})
					}
				} else if summary := summarizeToolCompletion(a.Config, call, result.DisplayText); summary != "" {
					a.emitProgressEvent(ProgressEvent{
						Kind:             progressKindToolCompleted,
						Message:          summary,
						ToolName:         call.Name,
						ToolCallID:       call.ID,
						ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
					})
				}
				toolExposurePlan := a.buildTurnToolExposurePlan(disabledTools, latestUser, unresolvedVerification, finalAnswerOnlyCorrection, verificationOutOfScopeFinalOnly, latestUserExplicitWebResearch, localCodeToolPolicyForTurn)
				if !toolExposurePlan.SuppressInteractiveWorkers {
					_ = a.maybeRunInteractiveParallelEditableWorkers(ctx, "tool:"+strings.TrimSpace(call.Name))
					_ = a.maybeRunInteractiveParallelReadOnlyWorkers(ctx, "tool:"+strings.TrimSpace(call.Name))
					_ = a.maybeRunInteractiveMicroWorkers(ctx, "tool:"+strings.TrimSpace(call.Name))
				}
			}
		}
	toolLoopCompleted:
		if deferredMixedTools {
			a.Session.AddMessage(internalUserMessage(mixedToolCallEditDeferralGuidance(a.Config)))
			if saveErr := a.Store.Save(a.Session); saveErr != nil {
				return "", saveErr
			}
			lastToolError = ""
			lastToolErrorCount = 0
			lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
			continue
		}
		if preWriteForceEditQueued || preWriteBlockedRetryQueued || editMismatchRetryQueued || toolRetryQueued {
			continue
		}
		if lastReadFilePath != "" && lastReadFilePathTurns >= 2 && repeatedCachedReadFileNudges < 1 && lastAssistantToolTurnWasCachedReadFile(a.Session.Messages) {
			repeatedCachedReadFileNudges++
			repeatedReadFilePathNudges++
			a.Session.AddMessage(internalUserMessage(fmt.Sprintf("Your latest read_file result for %s came from cached previously-read content. Treat that as confirmation that you already have that context. Do not reread the same chunk again. Either inspect a different file or tool, or give the final answer now.", lastReadFilePath)))
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
			continue
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if iterationHadToolSuccess {
			lastToolError = ""
			lastToolErrorCount = 0
			repeatedToolFailureRecoveryTurns = 0
		} else if iterationToolError != "" {
			if iterationToolError == lastToolError {
				lastToolErrorCount++
			} else {
				lastToolError = iterationToolError
				lastToolErrorCount = 1
				repeatedToolFailureRecoveryTurns = 0
			}
		}
		if lastToolErrorCount >= repeatedToolFailureAbortThreshold && lastToolError != "" {
			return "", fmt.Errorf("stopped after repeated tool failure: %s", lastToolError)
		}
		if lastToolErrorCount >= repeatedToolFailureRecoveryThreshold && lastToolError != "" && repeatedToolFailureRecoveryTurns < 1 {
			repeatedToolFailureRecoveryTurns++
			a.Session.AddMessage(internalUserMessage(a.recoveryGuidance(ctx, recoveryTriggerRepeatedToolError, recoveryInput{
				Detail: lastToolError,
				Recent: summarizeRecentToolTurns(a.Session.Messages, 3),
			})))
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
			continue
		}
		if lastToolErrorCount == repeatedToolFailureNudgeThreshold && lastToolError != "" {
			a.Session.AddMessage(internalUserMessage("The same tool failure repeated. Do not repeat the same failing tool call again unless you materially change the approach or inputs. Read the error carefully, choose a different next step, or provide a final answer if the issue is external."))
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
			continue
		}
		if edited {
			autoVerifyDisabledAfterPrompt := false
			if a.EmitProgress != nil {
				a.EmitProgress(localizedText(a.Config, "Edit applied. Checking follow-up steps...", "편집이 적용되었습니다. 후속 단계를 확인합니다..."))
			}
			if report, ok := a.autoVerifyChanges(ctx); ok {
				a.noteVerificationResult(report)
				autoVerifyRetryAttempted := false
				if a.EmitProgress != nil {
					if report.HasFailures() {
						a.EmitProgress(localizedText(a.Config, "Automatic verification failed. Classifying whether the failure belongs to the current patch...", "자동 검증이 실패했습니다. 실패가 현재 patch에 속하는지 판정합니다..."))
					} else if report.WasSkipped() {
						a.EmitProgress(localizedText(a.Config, "Automatic verification was skipped. Waiting for the model to summarize the unverified change...", "자동 검증이 생략되었습니다. 모델의 미검증 변경 요약을 기다립니다..."))
					} else {
						a.EmitProgress(localizedText(a.Config, "Automatic verification finished. Waiting for the model to summarize the change...", "자동 검증이 완료되었습니다. 모델의 변경 요약을 기다립니다..."))
					}
				}
				verification := strings.TrimSpace(report.RenderDetailed())
				verificationPrefix := localizedText(a.Config, "Automatic verification results:\n", "자동 검증 결과:\n")
				if report.WasSkipped() {
					verificationPrefix = localizedText(a.Config, "Automatic verification was not run:\n", "자동 검증이 실행되지 않았습니다:\n")
				}
				a.Session.AddMessage(internalUserMessage(verificationPrefix + verification))
				unresolvedVerification = report.HasFailures() || report.WasSkipped()
				if report.WasSkipped() {
					verificationDeclinedThisTurn = true
				}
				if report.HasFailures() {
					if report.HasCommandMissingFailure() {
						autoVerifyInfraFailureCount++
					} else {
						autoVerifyInfraFailureCount = 0
					}
					if autoVerifyInfraFailureCount >= 1 && !autoVerifyDisablePrompted && a.PromptResolveAutoVerifyFailure != nil {
						autoVerifyDisablePrompted = true
						markUserInputRequestedDuringTurn()
						resolution, promptErr := a.PromptResolveAutoVerifyFailure(report)
						if promptErr != nil {
							return "", promptErr
						}
						if resolution == AutoVerifyFailureRetry && !autoVerifyRetryAttempted {
							autoVerifyRetryAttempted = true
							a.recordEditLoopRetry("Retry automatic verification after tool-path update.", report.FailureSummary())
							if a.EmitProgress != nil {
								a.EmitProgress(localizedText(a.Config, "Verification tool path updated. Retrying automatic verification...", "검증 도구 경로가 업데이트되었습니다. 자동 검증을 다시 실행합니다..."))
							}
							retriedReport, retriedOK := a.autoVerifyChanges(ctx)
							if retriedOK {
								report = retriedReport
								a.noteVerificationResult(report)
								verification = strings.TrimSpace(report.RenderDetailed())
								a.Session.AddMessage(internalUserMessage(localizedText(a.Config, "Automatic verification results after tool-path update:\n", "도구 경로 업데이트 후 자동 검증 결과:\n") + verification))
								unresolvedVerification = report.HasFailures() || report.WasSkipped()
								if report.WasSkipped() {
									verificationDeclinedThisTurn = true
								}
							}
						}
						if resolution == AutoVerifyFailureDisable {
							unresolvedVerification = false
							autoVerifyInfraFailureCount = 0
							autoVerifyDisabledAfterPrompt = true
							a.recordEditLoopRisk("Automatic verification disabled after tool startup failure.", report.FailureSummary())
							if a.EmitProgress != nil {
								a.EmitProgress(localizedText(a.Config, "Automatic verification was disabled after repeated tool-path verification failures.", "검증 도구 경로 실패가 반복되어 자동 검증을 비활성화했습니다."))
							}
							a.Session.AddMessage(internalUserMessage("Automatic verification has been disabled for this workspace after repeated verification tool startup failures. Do not spend more turns trying to repair the local verification environment unless the user explicitly asks for that. Continue with the task and summarize any unverified risk briefly if needed."))
						}
					}
					if !autoVerifyDisabledAfterPrompt {
						scopeDecision := a.verificationFailureRepairScope(report)
						if !scopeDecision.ShouldRepair {
							unresolvedVerification = false
							verificationOutOfScopeThisTurn = true
							verificationOutOfScopeFinalOnly = true
							a.recordEditLoopRisk("Automatic verification failed outside the current patch scope.", strings.Join([]string{scopeDecision.Reason, scopeDecision.Anchor}, "\n"))
							if a.Session.TaskState != nil {
								a.Session.TaskState.RecordEvent("verification_terminal", strings.TrimSpace(a.Session.TaskState.ExecutorFocusNode), "verify", "Automatic verification failed outside the current patch scope; switching to final-answer-only.", strings.Join([]string{scopeDecision.Reason, scopeDecision.Anchor}, "\n"), "blocked", true)
							}
							a.Session.AddMessage(internalUserMessage(automaticVerificationOutOfScopeMessage(a.Config, report, scopeDecision)))
							if a.EmitProgress != nil {
								a.EmitProgress(localizedText(a.Config, "Verification failure is outside the current patch scope; stopping edit expansion and requiring disclosure.", "검증 실패가 현재 patch scope 밖입니다. 수정 범위 확장을 중단하고 보고하도록 전환합니다."))
							}
						} else {
							failureSummary := strings.TrimSpace(report.FailureSummary())
							repairGuidance := strings.TrimSpace(report.RepairGuidance())
							text := "The latest verification failed within the current patch scope. Investigate the failure and continue repairing only the current patch scope. Do not broaden into unrelated files or project settings unless the failure output directly names them as part of the current patch."
							if failureSummary != "" {
								text += "\n\nLikely failure summary:\n" + failureSummary
							}
							if repairGuidance != "" {
								text += "\n\nSuggested repair strategy:\n" + repairGuidance
							}
							text = a.appendFailureRepairPrompt(text)
							if decision := a.recordEditLoopRetry("Verification failed; continue the repair loop.", strings.Join([]string{failureSummary, repairGuidance}, "\n")); decision != nil {
								if policy := editLoopRetryDecisionPrompt(*decision); policy != "" {
									text += "\n\nRetry loop policy:\n" + policy
								}
							}
							a.Session.AddMessage(internalUserMessage(text))
						}
					}
				}
			} else {
				autoVerifyInfraFailureCount = 0
				if a.EmitProgress != nil {
					a.EmitProgress(localizedText(a.Config, "Edit applied. Waiting for the model to summarize the change...", "편집이 적용되었습니다. 모델의 변경 요약을 기다립니다..."))
				}
				unresolvedVerification = false
			}
			if !unresolvedVerification {
				consecutiveEditTurns++
				if consecutiveEditTurns >= 2 && postEditFinalAnswerNudges < 1 {
					postEditFinalAnswerNudges++
					a.Session.AddMessage(internalUserMessage("You have already completed multiple edit rounds. If there is no specific remaining issue to fix, stop editing and provide the final answer now. Only continue editing if the earlier changes are clearly insufficient."))
				}
			} else {
				consecutiveEditTurns = 0
				postEditFinalAnswerNudges = 0
			}
		} else {
			consecutiveEditTurns = 0
			postEditFinalAnswerNudges = 0
		}
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
	}
	if lastToolErrorCount >= repeatedToolFailureAbortThreshold && lastToolError != "" {
		return "", fmt.Errorf("stopped after repeated tool failure: %s", lastToolError)
	}
	return "", fmt.Errorf("tool loop limit exceeded%s", formatToolLoopDiagnostic(lastToolCallSummary, lastStopReason, lastIteration, maxToolIterations, lastRecentToolTurns))
}

func (a *Agent) emitProgressEvent(event ProgressEvent) {
	if a == nil {
		return
	}
	if a.EmitProgressEvent != nil {
		emitProgressEvent(a.EmitProgressEvent, event)
		return
	}
	if a.EmitProgress != nil {
		if message := formatProgressEventMessage(a.Config, event); strings.TrimSpace(message) != "" {
			a.EmitProgress(message)
		}
	}
}

func (a *Agent) attachProgressEventHandler(req ChatRequest) ChatRequest {
	if req.OnProgressEvent != nil {
		return req
	}
	if a == nil || a.EmitProgressEvent == nil {
		return req
	}
	req.OnProgressEvent = func(event ProgressEvent) {
		a.emitProgressEvent(event)
	}
	return req
}

func (a *Agent) shouldBufferAssistantDeltaForGatedTurn(unresolvedVerification bool, attemptedEditTool bool, successfulEditTool bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if unresolvedVerification {
		return true
	}
	if attemptedEditTool || successfulEditTool {
		return true
	}
	return sessionHasCurrentTurnFinalGateEvidence(a.Session)
}

func assistantMessagePhaseForModelResponse(msg Message) string {
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
		return strings.TrimSpace(msg.Phase)
	}
	if len(msg.ToolCalls) > 0 {
		return messagePhaseCommentary
	}
	if strings.TrimSpace(msg.Text) != "" {
		if strings.TrimSpace(msg.Phase) == messagePhaseCommentary {
			return messagePhaseCommentary
		}
		return messagePhaseFinalAnswerCandidate
	}
	return strings.TrimSpace(msg.Phase)
}

func (a *Agent) acceptRecentFinalAnswerCandidate(reply string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	index := a.findRecentFinalAnswerCandidate(reply)
	if index < 0 {
		return false
	}
	a.Session.Messages[index].Phase = messagePhaseFinalAnswer
	return true
}

func (a *Agent) discardRecentFinalAnswerCandidate(reply string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	index := a.findRecentFinalAnswerCandidate(reply)
	if index < 0 {
		return false
	}
	a.Session.Messages = append(a.Session.Messages[:index], a.Session.Messages[index+1:]...)
	return true
}

func (a *Agent) discardStaleFinalAnswerCandidates() bool {
	if a == nil || a.Session == nil {
		return false
	}
	messages := a.Session.Messages
	if len(messages) == 0 {
		return false
	}
	kept := messages[:0]
	changed := false
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") && strings.TrimSpace(msg.Phase) == messagePhaseFinalAnswerCandidate {
			changed = true
			continue
		}
		kept = append(kept, msg)
	}
	if changed {
		a.Session.Messages = kept
	}
	return changed
}

func (a *Agent) findRecentFinalAnswerCandidate(reply string) int {
	if a == nil || a.Session == nil {
		return -1
	}
	target := strings.TrimSpace(reply)
	if target == "" {
		return -1
	}
	for i := len(a.Session.Messages) - 1; i >= 0; i-- {
		msg := a.Session.Messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		if strings.TrimSpace(msg.Text) != target {
			continue
		}
		phase := strings.TrimSpace(msg.Phase)
		if phase == messagePhaseFinalAnswerCandidate || (phase == "" && i == len(a.Session.Messages)-1) {
			return i
		}
		return -1
	}
	return -1
}

func (a *Agent) shouldFinalizeGeneratedDocumentArtifactReply(request string, reply string, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if strings.TrimSpace(reply) == "" {
		return false
	}
	if unresolvedVerification {
		if a.Session.LastVerification == nil || !a.Session.LastVerification.WasSkipped() || !replyMentionsVerificationNotRun(reply) {
			return false
		}
	}
	if !a.changesAreGeneratedDocumentArtifactsForTurn(request) {
		return false
	}
	report := a.Session.LastCodingHarnessReport
	return report != nil && report.Approved
}

func (a *Agent) shouldSkipInteractiveFinalAnswerReviewForGeneratedDocumentArtifact(request string, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if requestLooksLikeLocalVerificationWork(strings.ToLower(strings.TrimSpace(baseUserQueryText(request)))) {
		return false
	}
	if !a.changesAreGeneratedDocumentArtifactsForTurn(request) {
		return false
	}
	if a.Session.LastVerification != nil && a.Session.LastVerification.HasFailures() && !a.Session.LastVerification.WasSkipped() {
		return false
	}
	if unresolvedVerification && (a.Session.LastVerification == nil || !a.Session.LastVerification.WasSkipped()) {
		return false
	}
	report := a.Session.LastCodingHarnessReport
	if report == nil {
		return false
	}
	copyReport := *report
	copyReport.Normalize()
	if codingHarnessFindingsHaveBlockers(copyReport.ArtifactQuality.Findings) {
		return false
	}
	if copyReport.Approved {
		return true
	}
	return len(copyReport.ArtifactQuality.Artifacts) > 0 &&
		generatedDocumentArtifactFinalReplyFindingsAreAnswerOnly(copyReport.Acceptance.Findings) &&
		generatedDocumentArtifactFinalReplyFindingsAreAnswerOnly(copyReport.DiffReview.Findings) &&
		generatedDocumentArtifactFinalReplyFindingsAreAnswerOnly(copyReport.Outcome.Findings)
}

func (a *Agent) shouldLetGeneratedDocumentArtifactHarnessHandleSkippedVerification(request string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if a.Session.LastVerification == nil || !a.Session.LastVerification.WasSkipped() {
		return false
	}
	return a.changesAreGeneratedDocumentArtifactsForTurn(request)
}

func (a *Agent) prepareGeneratedDocumentArtifactFinalAfterBlockedToolCalls(request string, attemptedEditTool bool, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if !a.changesAreGeneratedDocumentArtifactsForTurn(request) {
		return false
	}
	if a.Session.LastVerification != nil && a.Session.LastVerification.HasFailures() && !a.Session.LastVerification.WasSkipped() {
		return false
	}
	if unresolvedVerification && (a.Session.LastVerification == nil || !a.Session.LastVerification.WasSkipped()) {
		return false
	}

	report := a.Session.LastCodingHarnessReport
	if report == nil || !codingHarnessReportHasArtifactQuality(*report) {
		seedReply := a.generatedDocumentArtifactSeedFinalReply()
		freshReport := a.buildCodingHarnessReport(seedReply, attemptedEditTool, unresolvedVerification)
		a.Session.LastCodingHarnessReport = &freshReport
		a.Session.LastTestImpactReport = &freshReport.TestImpact
		a.Session.LastJobSupervisorReport = &freshReport.JobSupervisor
		report = &freshReport
	}
	copyReport := *report
	copyReport.Normalize()
	if codingHarnessFindingsHaveBlockers(copyReport.ArtifactQuality.Findings) {
		return false
	}
	if copyReport.Approved {
		return true
	}
	return a.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, report, unresolvedVerification)
}

func codingHarnessReportHasArtifactQuality(report CodingHarnessReport) bool {
	report.Normalize()
	return len(report.ArtifactQuality.Artifacts) > 0 || len(report.ArtifactQuality.Findings) > 0
}

func (a *Agent) generatedDocumentArtifactSeedFinalReply() string {
	paths := make([]string, 0)
	if a != nil && a.Session != nil {
		for _, path := range currentTurnPatchTransactionChangedPaths(a.Session) {
			if preWritePathLooksLikeGeneratedDocumentArtifact(path) {
				paths = append(paths, normalizeSessionRelativePath(path))
			}
		}
	}
	paths = normalizeTaskStateList(paths, 8)
	target := "the generated document artifact"
	if len(paths) > 0 {
		quoted := make([]string, 0, len(paths))
		for _, path := range paths {
			quoted = append(quoted, "`"+path+"`")
		}
		target = strings.Join(quoted, ", ")
	}
	cfg := Config{}
	sourcePrompt := ""
	if a != nil {
		cfg = a.Config
		if a.Session != nil {
			sourcePrompt = codingHarnessSourcePrompt(a.Session)
		}
	}
	language, _ := inferResponseLanguageForUserText(sourcePrompt, cfg)
	if language == "ko" {
		return fmt.Sprintf("%s 문서 산출물이 완료되었습니다. 결정적 산출물 품질 검사로 내용을 확인했습니다. 이번 턴은 생성 문서 산출물만 변경했으므로 빌드/테스트 검증은 실행하지 않았습니다.", target)
	}
	return fmt.Sprintf("%s is complete. Deterministic artifact-quality checks validated the document content. Build/test verification was not run because this turn only produced a generated document artifact.", target)
}

func (a *Agent) finalizeAcceptedFinalAnswer(reply string, unresolvedVerification bool) (string, error) {
	if a == nil || a.Session == nil {
		return reply, nil
	}
	a.acceptRecentFinalAnswerCandidate(reply)
	a.finalizeTaskStateOnAcceptedFinalAnswer(reply, unresolvedVerification)
	a.finalizePatchTransactionOnReturn()
	a.finalizeEditLoopOnReturn(reply, unresolvedVerification)
	a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
	if a.Store != nil {
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
	}
	return reply, nil
}

func (a *Agent) maybeFinalizeGeneratedDocumentArtifactFinalReply(request string, reply string, attemptedEditTool bool, unresolvedVerification bool) (string, bool, error) {
	if a == nil || a.Session == nil {
		return "", false, nil
	}
	if a.changesAreGeneratedDocumentArtifactsForTurn(request) && strings.TrimSpace(reply) != "" {
		report := a.buildCodingHarnessReport(reply, attemptedEditTool, unresolvedVerification)
		a.Session.LastCodingHarnessReport = &report
		a.Session.LastTestImpactReport = &report.TestImpact
		a.Session.LastJobSupervisorReport = &report.JobSupervisor
		if report.Approved {
			finalReply, err := a.finalizeAcceptedFinalAnswer(reply, unresolvedVerification)
			return finalReply, true, err
		}
		if !a.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, &report, unresolvedVerification) {
			return "", false, nil
		}
		a.discardRecentFinalAnswerCandidate(reply)
		reply = a.synthesizeGeneratedDocumentArtifactFinalReply(&report)
		report = a.buildCodingHarnessReport(reply, attemptedEditTool, unresolvedVerification)
		a.Session.LastCodingHarnessReport = &report
		a.Session.LastTestImpactReport = &report.TestImpact
		a.Session.LastJobSupervisorReport = &report.JobSupervisor
		if !report.Approved {
			reply = generatedDocumentArtifactHarnessBlockedReply(&report)
		}
		a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
		finalReply, err := a.finalizeAcceptedFinalAnswer(reply, unresolvedVerification)
		return finalReply, true, err
	}
	if a.shouldFinalizeGeneratedDocumentArtifactReply(request, reply, unresolvedVerification) {
		finalReply, err := a.finalizeAcceptedFinalAnswer(reply, unresolvedVerification)
		return finalReply, true, err
	}
	if !a.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, a.Session.LastCodingHarnessReport, unresolvedVerification) {
		return "", false, nil
	}
	a.discardRecentFinalAnswerCandidate(reply)
	reply = a.synthesizeGeneratedDocumentArtifactFinalReply(a.Session.LastCodingHarnessReport)
	report := a.buildCodingHarnessReport(reply, attemptedEditTool, unresolvedVerification)
	a.Session.LastCodingHarnessReport = &report
	a.Session.LastTestImpactReport = &report.TestImpact
	a.Session.LastJobSupervisorReport = &report.JobSupervisor
	if !report.Approved {
		reply = generatedDocumentArtifactHarnessBlockedReply(&report)
	}
	a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
	finalReply, err := a.finalizeAcceptedFinalAnswer(reply, unresolvedVerification)
	return finalReply, true, err
}

func (a *Agent) finalizeGeneratedDocumentArtifactAfterBlockedToolCalls(calls []ToolCall, attemptedEditTool bool, unresolvedVerification bool, reason string) (string, error) {
	if err := a.addToolCallRedirectGuidance(calls, reason, ""); err != nil {
		return "", err
	}
	reply := a.synthesizeGeneratedDocumentArtifactFinalReply(a.Session.LastCodingHarnessReport)
	synthesizedReport := a.buildCodingHarnessReport(reply, attemptedEditTool, unresolvedVerification)
	a.Session.LastCodingHarnessReport = &synthesizedReport
	a.Session.LastTestImpactReport = &synthesizedReport.TestImpact
	a.Session.LastJobSupervisorReport = &synthesizedReport.JobSupervisor
	if !synthesizedReport.Approved {
		reply = generatedDocumentArtifactHarnessBlockedReply(&synthesizedReport)
	}
	a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
	a.finalizeTaskStateOnAcceptedFinalAnswer(reply, unresolvedVerification)
	a.finalizePatchTransactionOnReturn()
	a.finalizeEditLoopOnReturn(reply, unresolvedVerification)
	a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
	if a.Store != nil {
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
	}
	return reply, nil
}

func (a *Agent) maybeFinalizeGeneratedDocumentArtifactToolCallPreamble(request string, calls []ToolCall, reply string, attemptedEditTool bool, unresolvedVerification bool) (string, bool, error) {
	if a == nil || a.Session == nil || len(calls) == 0 {
		return "", false, nil
	}
	if !generatedDocumentArtifactFinalizationOnlyToolCalls(calls) {
		return "", false, nil
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "", false, nil
	}
	if !a.changesAreGeneratedDocumentArtifactsForTurn(request) {
		return "", false, nil
	}
	if a.Session.LastVerification != nil && a.Session.LastVerification.HasFailures() && !a.Session.LastVerification.WasSkipped() {
		return "", false, nil
	}

	report := a.buildCodingHarnessReport(reply, attemptedEditTool, unresolvedVerification)
	a.Session.LastCodingHarnessReport = &report
	a.Session.LastTestImpactReport = &report.TestImpact
	a.Session.LastJobSupervisorReport = &report.JobSupervisor
	if !report.Approved {
		if !a.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, &report, unresolvedVerification) {
			return "", false, nil
		}
		reply = a.synthesizeGeneratedDocumentArtifactFinalReply(&report)
		report = a.buildCodingHarnessReport(reply, attemptedEditTool, unresolvedVerification)
		a.Session.LastCodingHarnessReport = &report
		a.Session.LastTestImpactReport = &report.TestImpact
		a.Session.LastJobSupervisorReport = &report.JobSupervisor
		if !report.Approved {
			reply = generatedDocumentArtifactHarnessBlockedReply(&report)
		}
	}

	reason := "NOT_EXECUTED: generated document artifact turns do not run shell or review validation, planning, or additional inspection after the document is written."
	if err := a.addToolCallRedirectGuidance(calls, reason, ""); err != nil {
		return "", false, err
	}
	a.Session.AddMessage(Message{Role: "assistant", Phase: messagePhaseFinalAnswer, Text: reply})
	a.finalizeTaskStateOnAcceptedFinalAnswer(reply, unresolvedVerification)
	a.finalizePatchTransactionOnReturn()
	a.finalizeEditLoopOnReturn(reply, unresolvedVerification)
	a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
	if a.Store != nil {
		if err := a.Store.Save(a.Session); err != nil {
			return "", false, err
		}
	}
	return reply, true, nil
}

func (a *Agent) shouldRouteCommentaryReplyThroughFinalGates(request string, reply string, attemptedEditTool bool, successfulEditTool bool, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if strings.TrimSpace(reply) == "" {
		return false
	}
	if a.changesAreGeneratedDocumentArtifactsForTurn(request) {
		return true
	}
	if unresolvedVerification {
		return true
	}
	if attemptedEditTool || successfulEditTool {
		return true
	}
	return sessionHasCurrentTurnFinalGateEvidence(a.Session)
}

func (a *Agent) shouldDeferEndTurnFollowUpForGeneratedDocument(request string, resp ChatResponse) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if !chatResponseRequestsFollowUp(resp) {
		return false
	}
	if len(resp.Message.ToolCalls) > 0 || strings.TrimSpace(resp.Message.Text) == "" {
		return false
	}
	if assistantTextLooksLikeInProgress(resp.Message.Text) {
		return false
	}
	return a.changesAreGeneratedDocumentArtifactsForTurn(request)
}

func shouldDeferEndTurnFollowUpForFinalLookingReply(resp ChatResponse) bool {
	if !chatResponseRequestsFollowUp(resp) {
		return false
	}
	if len(resp.Message.ToolCalls) > 0 {
		return false
	}
	if strings.TrimSpace(resp.Message.Text) == "" {
		return false
	}
	if assistantTextLooksLikeInProgress(resp.Message.Text) {
		return false
	}
	switch strings.TrimSpace(resp.Message.Phase) {
	case messagePhaseFinalAnswerCandidate, messagePhaseFinalAnswer:
		return true
	}
	return assistantTextLooksLikeCompletionSummary(resp.Message.Text)
}

func (a *Agent) shouldDeferEndTurnFollowUpForPostWorkReply(request string, resp ChatResponse, attemptedEditTool bool, successfulEditTool bool, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if !chatResponseRequestsFollowUp(resp) {
		return false
	}
	if len(resp.Message.ToolCalls) > 0 || strings.TrimSpace(resp.Message.Text) == "" {
		return false
	}
	if assistantTextLooksLikeInProgress(resp.Message.Text) {
		return false
	}
	if attemptedEditTool || successfulEditTool || sessionHasCurrentTurnFinalGateEvidence(a.Session) {
		return true
	}
	if unresolvedVerification && (replyMentionsVerificationBlocker(resp.Message.Text) || replyMentionsVerificationNotRun(resp.Message.Text)) {
		return true
	}
	return a.changesAreGeneratedDocumentArtifactsForTurn(request)
}

func (a *Agent) shouldPromoteFinalLookingToolPreambleToFinalCandidate(request string, calls []ToolCall, reply string, attemptedEditTool bool, successfulEditTool bool, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil || len(calls) == 0 {
		return false
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return false
	}
	if a.changesAreGeneratedDocumentArtifactsForTurn(request) {
		return false
	}
	if !assistantTextLooksLikeCompletionSummary(reply) {
		return false
	}
	if !attemptedEditTool && !successfulEditTool && assistantTextClaimsWorkspaceMutationCompletion(reply) {
		return false
	}
	if !finalLookingPostCompletionOnlyToolCalls(calls) {
		return false
	}
	if finalLookingToolCallsIncludeVerification(calls, a.Session) && !runtimeGateVerificationPassed(a.Session) {
		return false
	}
	return true
}

func finalLookingPostCompletionOnlyToolCalls(calls []ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			return false
		}
		if isEditTool(name) || toolCallMutatesGitState(call) {
			return false
		}
		if shellToolCallMayWriteWorkspace(call) {
			return false
		}
		if !finalLookingPostCompletionToolName(name) {
			return false
		}
	}
	return true
}

func assistantTextClaimsWorkspaceMutationCompletion(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"what i changed",
		"i changed",
		"changed ",
		"modified ",
		"fixed ",
		"patched ",
		"implemented ",
		"code changes",
		"no further changes",
		"no further edits",
		"수정이 완료",
		"수정 완료",
		"수정했습니다",
		"변경했습니다",
		"변경은 필요",
		"더 이상 변경",
		"더 이상 수정",
		"패치",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func finalLookingPostCompletionToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "read_file",
		"list_files",
		"grep",
		"git_status",
		"git_diff",
		"run_shell",
		"run_shell_background",
		"run_shell_bundle_background",
		"check_shell_job",
		"check_shell_bundle",
		"review",
		"run_review",
		"post_change_review",
		"update_plan",
		"get_goal":
		return true
	default:
		return false
	}
}

func finalLookingToolCallsIncludeVerification(calls []ToolCall, session *Session) bool {
	for _, call := range calls {
		if toolCallIsVerificationRetryOrPoll(call, session) {
			return true
		}
	}
	return false
}

func (a *Agent) shouldBlockGeneratedDocumentArtifactValidationToolCalls(request string, calls []ToolCall) bool {
	if a == nil || a.Session == nil || len(calls) == 0 {
		return false
	}
	if requestLooksLikeLocalVerificationWork(strings.ToLower(strings.TrimSpace(baseUserQueryText(request)))) {
		return false
	}
	if !a.changesAreGeneratedDocumentArtifactsForTurn(request) {
		return false
	}
	artifactApproved := a.generatedDocumentArtifactQualityApproved()
	artifactContentAccepted := a.generatedDocumentArtifactContentQualityAccepted()
	if !artifactApproved && !artifactContentAccepted && a.Session.LastCodingHarnessReport == nil && generatedDocumentArtifactFinalizationOnlyToolCalls(calls) {
		return true
	}
	for _, call := range calls {
		if artifactApproved || artifactContentAccepted {
			return true
		}
		if generatedDocumentArtifactValidationToolCall(call) {
			return true
		}
		if (artifactApproved || artifactContentAccepted) && generatedDocumentArtifactPostCompletionToolCall(call) {
			return true
		}
	}
	return false
}

func (a *Agent) generatedDocumentArtifactQualityApproved() bool {
	if a == nil || a.Session == nil || a.Session.LastCodingHarnessReport == nil {
		return false
	}
	return a.Session.LastCodingHarnessReport.Approved
}

func (a *Agent) generatedDocumentArtifactContentQualityAccepted() bool {
	if a == nil || a.Session == nil || a.Session.LastCodingHarnessReport == nil {
		return false
	}
	report := *a.Session.LastCodingHarnessReport
	report.Normalize()
	if len(report.ArtifactQuality.Artifacts) == 0 {
		return false
	}
	return !codingHarnessFindingsHaveBlockers(report.ArtifactQuality.Findings)
}

func generatedDocumentArtifactValidationToolCall(call ToolCall) bool {
	switch strings.ToLower(strings.TrimSpace(call.Name)) {
	case "run_shell",
		"run_shell_background",
		"run_shell_bundle_background",
		"check_shell_job",
		"check_shell_bundle",
		"review",
		"run_review",
		"post_change_review":
		return true
	default:
		return false
	}
}

func generatedDocumentArtifactPostCompletionToolCall(call ToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	return name != "" && !isEditTool(name)
}

func generatedDocumentArtifactFinalizationOnlyToolCalls(calls []ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			return false
		}
		if isEditTool(name) || toolCallMutatesGitState(call) {
			return false
		}
		if shellToolCallMayWriteWorkspace(call) {
			return false
		}
		if !generatedDocumentArtifactPostCompletionToolCall(call) {
			return false
		}
	}
	return true
}

func shellToolCallMayWriteWorkspace(call ToolCall) bool {
	switch strings.TrimSpace(call.Name) {
	case "run_shell", "run_shell_background":
		return shellMutationClassMayWriteWorkspace(assessShellCommandMutation(toolCallCommandArgument(call)).Class)
	case "run_shell_bundle_background":
		for _, command := range toolCallCommandsArgument(call) {
			if shellMutationClassMayWriteWorkspace(assessShellCommandMutation(command).Class) {
				return true
			}
		}
	}
	return false
}

func shellMutationClassMayWriteWorkspace(class shellMutationClass) bool {
	switch class {
	case shellMutationWorkspaceWrite,
		shellMutationGitMutation,
		shellMutationExternalInstall,
		shellMutationUnsafe,
		shellMutationUnsupported:
		return true
	default:
		return false
	}
}

func generatedDocumentArtifactValidationToolGuidance() string {
	return "This is a generated document artifact turn. Do not run shell, review, or additional inspection tools after the report is written and artifact-quality content checks have accepted it. Deterministic artifact-quality checks are the post-write gate here. If those checks reported blockers, inspect or edit the document artifact itself; otherwise revise only the final answer and conclude."
}

func generatedDocumentArtifactFinalOnlyPromptGuidance() string {
	return "Generated document artifact finalization is answer-only now. The artifact content has already passed deterministic content checks or has an approved artifact harness report. Do not request or mention additional tool use, shell validation, review passes, or source inspection. Provide the final answer only, including an explicit statement when build/test verification was not run."
}

type turnToolExposurePlan struct {
	DisabledTools              map[string]bool
	GeneratedDocumentFinalOnly bool
	SuppressInteractiveWorkers bool
}

func (a *Agent) buildTurnToolExposurePlan(baseDisabled map[string]bool, request string, unresolvedVerification bool, finalAnswerOnlyCorrection bool, verificationOutOfScopeFinalOnly bool, latestUserExplicitWebResearch bool, localCodeToolPolicyForTurn bool) turnToolExposurePlan {
	disabled := cloneDisabledTools(baseDisabled)
	var registry *ToolRegistry
	if a != nil {
		registry = a.Tools
	}
	generatedDocumentFinalOnly := a.shouldUseGeneratedDocumentArtifactFinalOnlyTools(request, unresolvedVerification)
	suppressInteractiveWorkers := a.shouldSuppressInteractiveWorkersForTurn(request)
	if finalAnswerOnlyCorrection || verificationOutOfScopeFinalOnly || generatedDocumentFinalOnly {
		disableAllTools(disabled, registry)
		suppressInteractiveWorkers = true
	}
	if !latestUserExplicitWebResearch && localCodeToolPolicyForTurn {
		disableWebResearchToolsForLocalCodeWork(disabled, registry)
	}
	return turnToolExposurePlan{
		DisabledTools:              disabled,
		GeneratedDocumentFinalOnly: generatedDocumentFinalOnly,
		SuppressInteractiveWorkers: suppressInteractiveWorkers,
	}
}

func (a *Agent) shouldSuppressInteractiveWorkersForTurn(request string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if requestLooksLikeLocalVerificationWork(strings.ToLower(strings.TrimSpace(baseUserQueryText(request)))) {
		return false
	}
	return a.changesAreGeneratedDocumentArtifactsForTurn(request)
}

func (a *Agent) shouldUseGeneratedDocumentArtifactFinalOnlyTools(request string, unresolvedVerification bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if requestLooksLikeLocalVerificationWork(strings.ToLower(strings.TrimSpace(baseUserQueryText(request)))) {
		return false
	}
	if !a.changesAreGeneratedDocumentArtifactsForTurn(request) {
		return false
	}
	if a.Session.LastVerification != nil && a.Session.LastVerification.HasFailures() && !a.Session.LastVerification.WasSkipped() {
		return false
	}
	if unresolvedVerification && (a.Session.LastVerification == nil || !a.Session.LastVerification.WasSkipped()) {
		return false
	}
	report := a.Session.LastCodingHarnessReport
	if report == nil {
		return false
	}
	copyReport := *report
	copyReport.Normalize()
	if len(copyReport.ArtifactQuality.Artifacts) == 0 {
		return false
	}
	if codingHarnessFindingsHaveBlockers(copyReport.ArtifactQuality.Findings) {
		return false
	}
	if copyReport.Approved {
		return true
	}
	return a.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, report, unresolvedVerification)
}

func (a *Agent) changesAreGeneratedDocumentArtifactsForTurn(request string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	requestText := strings.TrimSpace(baseUserQueryText(request))
	requestIsInternalReviewFeedback := looksLikeInternalReviewFeedbackUserMessage(requestText)
	if changedPaths := currentTurnPatchTransactionChangedPaths(a.Session); len(changedPaths) > 0 {
		if changedPathsAreGeneratedDocumentArtifacts(a.Session, request, changedPaths) {
			return true
		}
		if requestIsInternalReviewFeedback && sessionHasDocumentArtifactQualityAcceptedHarness(a.Session) && changedPathsMatchDocumentArtifactQuality(a.Session, changedPaths) {
			return true
		}
		return false
	}
	documentRequestContext := generatedDocumentArtifactRequestContextForTurn(a.Session, request)
	rawActiveChangedPaths := rawActivePatchTransactionChangedPaths(a.Session)
	if len(rawActiveChangedPaths) > 0 {
		if documentRequestContext != "" && changedPathsAreGeneratedDocumentArtifacts(a.Session, request, rawActiveChangedPaths) {
			return true
		}
		if requestIsInternalReviewFeedback && sessionHasDocumentArtifactQualityAcceptedHarness(a.Session) && changedPathsMatchDocumentArtifactQuality(a.Session, rawActiveChangedPaths) {
			return true
		}
	}
	if documentRequestContext == "" &&
		!requestIsInternalReviewFeedback {
		return false
	}
	if sessionHasDocumentArtifactContentAcceptedHarness(a.Session) {
		return true
	}
	if sessionHasApprovedDocumentArtifactOnlyHarness(a.Session) {
		return true
	}
	root := workspaceSnapshotRoot(a.Workspace)
	if strings.TrimSpace(root) == "" {
		root = a.Workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		root = a.Session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return false
	}
	changedPaths := autoReviewChangedPaths(a.Session, root)
	if len(changedPaths) > 0 {
		return changedPathsAreGeneratedDocumentArtifacts(a.Session, request, changedPaths)
	}
	sessionChangedPaths := sessionPatchTransactionChangedPaths(a.Session)
	if len(sessionChangedPaths) > 0 {
		if documentRequestContext != "" && changedPathsAreGeneratedDocumentArtifacts(a.Session, request, sessionChangedPaths) {
			return true
		}
		if requestIsInternalReviewFeedback && sessionHasDocumentArtifactQualityAcceptedHarness(a.Session) && changedPathsMatchDocumentArtifactQuality(a.Session, sessionChangedPaths) {
			return true
		}
		return false
	}
	return sessionHasDocumentArtifactQualityAcceptedHarness(a.Session)
}

func sessionHasDocumentArtifactContentAcceptedHarness(session *Session) bool {
	if session == nil || session.LastCodingHarnessReport == nil {
		return false
	}
	report := *session.LastCodingHarnessReport
	report.Normalize()
	if len(report.ArtifactQuality.Artifacts) == 0 {
		return false
	}
	if codingHarnessFindingsHaveBlockers(report.ArtifactQuality.Findings) {
		return false
	}
	artifactPaths := make(map[string]bool)
	for _, artifact := range report.ArtifactQuality.Artifacts {
		path := normalizeSessionRelativePath(artifact.Path)
		if !preWritePathLooksLikeGeneratedDocumentArtifact(path) {
			return false
		}
		artifactPaths[strings.ToLower(path)] = true
	}
	changedPaths := documentArtifactHarnessChangedPaths(session)
	if len(changedPaths) == 0 {
		return false
	}
	for _, path := range changedPaths {
		normalized := normalizeSessionRelativePath(path)
		if !preWritePathLooksLikeGeneratedDocumentArtifact(normalized) {
			return false
		}
		if len(artifactPaths) > 0 && !artifactPaths[strings.ToLower(normalized)] {
			return false
		}
	}
	return true
}

func sessionHasDocumentArtifactQualityAcceptedHarness(session *Session) bool {
	if session == nil || session.LastCodingHarnessReport == nil {
		return false
	}
	report := *session.LastCodingHarnessReport
	report.Normalize()
	if len(report.ArtifactQuality.Artifacts) == 0 {
		return false
	}
	if codingHarnessFindingsHaveBlockers(report.ArtifactQuality.Findings) {
		return false
	}
	for _, artifact := range report.ArtifactQuality.Artifacts {
		if !artifactQualityDocumentArtifactLooksAccepted(artifact) {
			return false
		}
	}
	return true
}

func artifactQualityDocumentArtifactLooksAccepted(artifact ArtifactQualityCheck) bool {
	path := normalizeSessionRelativePath(artifact.Path)
	if !preWritePathLooksLikeGeneratedDocumentArtifact(path) {
		return false
	}
	if artifact.Size <= 0 && artifact.ContentChars <= 0 {
		return false
	}
	for _, check := range artifact.Checks {
		lower := strings.ToLower(strings.TrimSpace(check))
		if strings.Contains(lower, "does not exist") ||
			strings.Contains(lower, "read failed") ||
			strings.Contains(lower, "path could not be resolved") {
			return false
		}
	}
	return true
}

func sessionHasApprovedDocumentArtifactOnlyHarness(session *Session) bool {
	if session == nil || session.LastCodingHarnessReport == nil {
		return false
	}
	report := session.LastCodingHarnessReport
	if !report.Approved {
		return false
	}
	changedPaths := documentArtifactHarnessChangedPaths(session)
	if len(changedPaths) == 0 {
		return false
	}
	for _, path := range changedPaths {
		if !preWritePathLooksLikeGeneratedDocumentArtifact(path) {
			return false
		}
	}
	artifactPaths := make(map[string]bool)
	for _, artifact := range report.ArtifactQuality.Artifacts {
		path := normalizeSessionRelativePath(artifact.Path)
		if !preWritePathLooksLikeGeneratedDocumentArtifact(path) {
			return false
		}
		artifactPaths[path] = true
	}
	if len(artifactPaths) == 0 {
		return changedPathsLookLikeGeneratedReportArtifacts(changedPaths)
	}
	for _, path := range changedPaths {
		if !artifactPaths[normalizeSessionRelativePath(path)] {
			return false
		}
	}
	return true
}

func documentArtifactHarnessChangedPaths(session *Session) []string {
	changedPaths := currentTurnPatchTransactionChangedPaths(session)
	if len(changedPaths) == 0 {
		rawActiveChangedPaths := rawActivePatchTransactionChangedPaths(session)
		if changedPathsLookLikeGeneratedReportArtifacts(rawActiveChangedPaths) ||
			changedPathsMatchDocumentArtifactQuality(session, rawActiveChangedPaths) {
			return normalizeTaskStateList(rawActiveChangedPaths, 64)
		}
		changedPaths = sessionPatchTransactionChangedPaths(session)
	}
	return normalizeTaskStateList(changedPaths, 64)
}

func rawActivePatchTransactionChangedPaths(session *Session) []string {
	if session == nil || session.ActivePatchTransaction == nil {
		return nil
	}
	return normalizeTaskStateList(session.ActivePatchTransaction.ChangedPaths(), 64)
}

func changedPathsMatchDocumentArtifactQuality(session *Session, changedPaths []string) bool {
	if session == nil || session.LastCodingHarnessReport == nil {
		return false
	}
	report := *session.LastCodingHarnessReport
	report.Normalize()
	if len(report.ArtifactQuality.Artifacts) == 0 {
		return false
	}
	if codingHarnessFindingsHaveBlockers(report.ArtifactQuality.Findings) {
		return false
	}
	artifactPaths := make(map[string]bool)
	for _, artifact := range report.ArtifactQuality.Artifacts {
		path := normalizeSessionRelativePath(artifact.Path)
		if !preWritePathLooksLikeGeneratedDocumentArtifact(path) {
			return false
		}
		artifactPaths[strings.ToLower(path)] = true
	}
	normalizedChangedPaths := normalizeTaskStateList(changedPaths, 64)
	for _, path := range normalizedChangedPaths {
		normalized := normalizeSessionRelativePath(path)
		if !preWritePathLooksLikeGeneratedDocumentArtifact(normalized) {
			return false
		}
		if len(artifactPaths) > 0 && !artifactPaths[strings.ToLower(normalized)] {
			return false
		}
	}
	return len(normalizedChangedPaths) > 0
}

func changedPathsLookLikeGeneratedReportArtifacts(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		normalized := normalizeSessionRelativePath(path)
		if !preWritePathLooksLikeGeneratedDocumentArtifact(normalized) {
			return false
		}
		lower := strings.ToLower(normalized)
		base := strings.ToLower(filepath.Base(lower))
		if !containsAny(lower, "report", "review", "analysis", "finding", "bug") &&
			!containsAny(base, "report", "review", "analysis", "finding", "bug") {
			return false
		}
	}
	return true
}

func (a *Agent) shouldCompleteSharedPlanOnReturn(unresolvedVerification bool) bool {
	if a == nil || a.Session == nil {
		return false
	}
	a.refreshBackgroundJobs()
	if unresolvedVerification {
		return false
	}
	if a.Session.TaskState != nil && len(a.Session.TaskState.PendingChecks) > 0 {
		return false
	}
	return !a.hasRunningBackgroundJobs()
}

func (a *Agent) finalizeTaskStateOnAcceptedFinalAnswer(reply string, unresolvedVerification bool) {
	if a == nil || a.Session == nil || a.Session.TaskState == nil {
		return
	}
	effectiveUnresolvedVerification := unresolvedVerification
	if a.shouldTreatGeneratedDocumentVerificationAsComplete(reply) {
		effectiveUnresolvedVerification = false
		a.Session.TaskState.RemovePendingCheck(verificationPendingCheck)
		a.Session.TaskState.RecordEvent(
			"verification",
			strings.TrimSpace(a.Session.TaskState.ExecutorFocusNode),
			"verify",
			"Code verification not required for generated document artifact.",
			compactPromptSection(reply, 500),
			"skipped",
			false,
		)
	}
	if !a.finalizeSelfDrivingWorkLoopOnReturn(reply, effectiveUnresolvedVerification) && a.shouldCompleteSharedPlanOnReturn(effectiveUnresolvedVerification) {
		a.Session.TaskState.SetPhase("done")
		a.Session.TaskState.SetNextStep("Wait for the next user instruction.")
		a.Session.TaskState.ClearExecutorFocus()
		a.Session.completeSharedPlan()
	}
}

func (a *Agent) shouldTreatGeneratedDocumentVerificationAsComplete(reply string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	if a.Session.LastVerification != nil && a.Session.LastVerification.HasFailures() {
		return false
	}
	if replyClaimsVerificationSuccess(reply) && !sessionHasSuccessfulVerificationEvidence(a.Session) {
		return false
	}
	if a.Session.AcceptanceContract != nil && a.Session.AcceptanceContract.VerificationRequired && !sessionHasSuccessfulVerificationEvidence(a.Session) {
		return false
	}
	if sessionHasApprovedDocumentArtifactOnlyHarness(a.Session) {
		return true
	}
	if a.Session.LastCodingHarnessReport != nil && a.Session.LastCodingHarnessReport.Approved {
		request := codingHarnessSourcePrompt(a.Session)
		return a.changesAreGeneratedDocumentArtifactsForTurn(request)
	}
	return false
}

func replyBlamesInternalToolTranscriptRecovery(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	hasInternalMarker := containsAny(lower,
		"tool result was missing from the saved transcript",
		"missing from saved transcript",
		"saved transcript",
		"도구 실행 파이프라인 오류",
		"도구 파이프라인 오류",
		"툴 실행 파이프라인 오류",
		"툴 파이프라인 오류",
	)
	if !hasInternalMarker {
		return false
	}
	return containsAny(lower,
		"manual patch",
		"apply the patch manually",
		"restart the session",
		"restart",
		"all tools",
		"tool pipeline",
		"tool execution pipeline",
		"cannot read",
		"cannot apply",
		"수동 패치",
		"직접 적용",
		"세션을 다시 시작",
		"모든 도구",
		"파일의 현재 내용을 새로 읽을 수 없음",
		"편집을 적용할 수 없음",
	)
}

func internalToolTranscriptFailureGuidance(explicitEditRequest bool) string {
	guidance := "Your previous answer treated internal transcript-recovery context as a real tool pipeline failure. The phrase `tool result was missing from the saved transcript` is not evidence that all tools are broken. Do not tell the user to restart the session, wait for a pipeline fix, or apply a patch manually based on that phrase."
	guidance += "\n\nInspect the actual latest tool results and continue from concrete evidence. If a tool really failed, cite that specific tool name and exact latest error. If you need file context, call read_file/list_files/grep normally."
	if explicitEditRequest {
		guidance += " This is an edit/fix request, so keep using the available edit tools. If an edit was blocked by review or stale anchors, re-read the file and produce a fresh edit that addresses the blocker."
	}
	return guidance
}

func replyBlamesToolAvailabilityAfterSkippedVerification(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	hasAvailabilityClaim := containsAny(lower,
		"blocked by tool availability",
		"tool availability/session error",
		"tool availability error",
		"requested mcp tools",
		"not exposed in the callable tool namespace",
		"enable/expose",
		"workspace tools are returning results",
		"local inspection/verification tools are currently returning",
		"transcript-recovery errors",
		"cannot currently re-read",
		"도구 가용성",
		"도구를 사용할 수",
		"mcp 도구",
		"노출되어 있지",
		"워크스페이스 도구",
	)
	if !hasAvailabilityClaim {
		return false
	}
	return containsAny(lower,
		"verification",
		"검증",
		"web",
		"mcp",
		"tool",
		"도구",
		"session",
		"세션",
	)
}

func toolAvailabilityAfterSkippedVerificationGuidance(cfg Config) string {
	return localizedText(cfg,
		"Your previous answer converted a skipped or declined verification step into a tool-availability/session failure. Do not do that. If verification was skipped or declined, say that verification was not run and do not ask the user to enable web/MCP tools. Keep verification gaps separate from code findings: do not relabel resolved code-review findings as remaining bugs only because verification is missing. This is local code work; use the latest review, patch, and tool-result evidence. Provide the final answer now unless a concrete available local tool is still necessary.",
		"이전 답변은 생략되었거나 거절된 검증 단계를 도구 가용성/세션 장애로 잘못 해석했습니다. 그렇게 처리하지 마세요. 검증이 생략되었거나 거절되었다면 검증을 실행하지 않았다고만 밝히고, web/MCP 도구를 활성화하라고 요구하지 마세요. 검증 공백과 코드 finding은 분리하세요. 검증 증거가 없다는 이유만으로 해결된 코드 리뷰 finding을 남은 버그처럼 다시 표시하지 마세요. 이 작업은 로컬 코드 작업입니다. 최신 리뷰, 패치, 도구 결과 근거를 기준으로 최종 답변을 작성하세요. 꼭 필요한 경우에만 실제 사용 가능한 로컬 도구를 호출하세요.",
	)
}

func toolRegistryHasLocalInspectionTools(registry *ToolRegistry) bool {
	if registry == nil {
		return false
	}
	for _, def := range registry.Definitions() {
		switch strings.TrimSpace(def.Name) {
		case "read_file", "list_files", "grep", "git_status", "git_diff":
			return true
		}
	}
	return false
}

func replyBlamesLocalCodeToolAvailability(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	hasAvailabilityClaim := containsAny(lower,
		"blocked by tool availability",
		"tool availability/session error",
		"tool availability error",
		"not exposed in the callable tool namespace",
		"enable/expose",
		"local inspection tools are currently unavailable",
		"local inspection/verification tools are currently returning",
		"workspace tools are returning",
		"cannot currently re-read",
		"read-only analysis mode blocked",
		"도구 가용성",
		"도구를 사용할 수",
		"노출되어 있지",
		"워크스페이스 도구",
		"읽기 전용 분석 모드",
		"로컬 파일 분석 도구",
		"로컬 검사 도구",
		"로컬 도구",
		"분석 모드 제한",
	)
	if !hasAvailabilityClaim {
		return false
	}
	hasLocalToolClaim := containsAny(lower,
		"read_file",
		"list_files",
		"grep",
		"git_status",
		"git_diff",
		"local inspection",
		"local file",
		"workspace tool",
		"로컬 파일",
		"로컬 검사",
		"로컬 도구",
	)
	hasWebOrPolicyClaim := containsAny(lower,
		"web",
		"mcp",
		"browser",
		"external research",
		"read-only analysis",
		"웹",
		"외부 연구",
		"외부 리서치",
		"읽기 전용 분석",
	)
	hasBlockedClaim := containsAny(lower,
		"blocked",
		"unavailable",
		"not exposed",
		"cannot",
		"차단",
		"막혀",
		"사용할 수",
		"해제",
		"허용 목록",
	)
	return hasLocalToolClaim && hasWebOrPolicyClaim && hasBlockedClaim
}

func localCodeToolAvailabilityBlameGuidance(cfg Config) string {
	return localizedText(cfg,
		"Your previous answer treated the local-code web-research policy as a general tool outage. Do not ask the user to enable MCP/web tools. For local code review or repair, MCP web/search/browser tools may be blocked by design, but local inspection tools such as read_file, list_files, grep, git_status, and git_diff remain the correct path. Use those tools if more evidence is needed, or answer from already collected local evidence. If a specific local tool call actually failed, cite that exact tool error only.",
		"이전 답변은 로컬 코드 작업의 웹 리서치 차단 정책을 전체 도구 장애처럼 잘못 해석했습니다. 사용자에게 MCP/web 도구를 활성화하라고 요구하지 마세요. 로컬 코드 리뷰/수정에서는 MCP web/search/browser 도구가 의도적으로 차단될 수 있지만 read_file, list_files, grep, git_status, git_diff 같은 로컬 검사 도구는 올바른 경로입니다. 근거가 더 필요하면 해당 로컬 도구를 사용하고, 이미 수집한 근거가 충분하면 그 근거로 답하세요. 특정 로컬 도구 호출이 실제로 실패한 경우에만 그 도구 이름과 정확한 오류를 인용하세요.",
	)
}

func verificationFollowupBlockedGuidance(cfg Config) string {
	return localizedText(cfg,
		"A build, test, or verification command was already skipped or declined in this turn. Do not call run_shell, run_shell_background, run_shell_bundle_background, check_shell_job, or check_shell_bundle for the same verification again unless the user explicitly approves verification. Use the existing code, diff, review, and tool-output evidence and provide the final answer now. State that verification was not run; do not describe it as a tool outage. Keep verification gaps separate from code findings: do not relabel resolved code-review findings as remaining bugs only because verification is missing.",
		"이번 턴에서 빌드/테스트/검증 명령이 이미 생략되었거나 거절되었습니다. 사용자가 명시적으로 검증 실행을 승인하기 전에는 같은 검증을 위해 run_shell, run_shell_background, run_shell_bundle_background, check_shell_job, check_shell_bundle를 다시 호출하지 마세요. 기존 코드, diff, 리뷰, 도구 출력 근거만 사용해 지금 최종 답변을 작성하세요. 검증은 실행하지 않았다고 밝히되, 도구 장애로 표현하지 마세요. 검증 공백과 코드 finding은 분리하세요. 검증 증거가 없다는 이유만으로 해결된 코드 리뷰 finding을 남은 버그처럼 다시 표시하지 마세요.",
	)
}

func verificationOutOfScopeFollowupBlockedGuidance(cfg Config) string {
	return localizedText(cfg,
		"Automatic verification already failed outside the current patch scope. Do not call run_shell, run_shell_background, run_shell_bundle_background, check_shell_job, or check_shell_bundle to rerun or probe build/test/verification in this turn. Use the existing code, diff, review, and verification output evidence and provide the final answer now. Disclose the verification failure as an external or ambient blocker/risk, and do not broaden the repair into unrelated files or project settings unless the user explicitly approves a new verification or scope expansion.",
		"자동 검증이 이미 현재 patch scope 밖의 실패로 판정되었습니다. 이 턴에서는 build/test/verification을 다시 실행하거나 탐색하기 위해 run_shell, run_shell_background, run_shell_bundle_background, check_shell_job, check_shell_bundle를 호출하지 마세요. 기존 코드, diff, 리뷰, 검증 출력 근거만 사용해 지금 최종 답변을 작성하세요. 검증 실패는 외부/환경성 blocker 또는 risk로 명시하고, 사용자가 새 검증이나 범위 확장을 명시적으로 승인하기 전에는 관련 없는 파일이나 프로젝트 설정으로 수리 범위를 넓히지 마세요.",
	)
}

func verificationOutOfScopeFinalOnlyGuidance(cfg Config) string {
	return localizedText(cfg,
		"Automatic verification already failed outside the current patch scope, so this turn is now final-answer-only. Do not request more tools, do not retry verification, and do not broaden the repair. Use the accepted patch/review evidence and disclose the out-of-scope verification blocker or risk in the final answer.",
		"자동 검증이 현재 patch scope 밖의 실패로 판정되었으므로 이 턴은 이제 최종 답변 전용입니다. 추가 도구를 요청하지 말고, 검증을 재시도하지 말고, 수리 범위를 넓히지 마세요. 승인된 패치/리뷰 근거를 기준으로 최종 답변을 작성하고 out-of-scope 검증 blocker 또는 risk만 명시하세요.",
	)
}

func (a *Agent) ensureOutOfScopeVerificationFinalDisclosure(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply != "" && (replyMentionsVerificationBlocker(reply) || replyMentionsVerificationNotRun(reply)) {
		return reply
	}
	summary := ""
	if a != nil && a.Session != nil && a.Session.LastVerification != nil {
		summary = strings.TrimSpace(a.Session.LastVerification.FailureSummary())
		if summary == "" {
			summary = strings.TrimSpace(a.Session.LastVerification.SummaryLine())
		}
	}
	korean := true
	if a != nil {
		korean = localePrefersKorean(a.Config)
	}
	var note string
	if korean {
		note = "검증 참고: 자동 검증은 현재 patch scope 밖의 실패로 종료되어, 이번 수정 범위에서는 추가 수리를 진행하지 않았습니다."
		if summary != "" {
			note += "\n" + compactPromptSection(summary, 500)
		}
	} else {
		note = "Verification note: automatic verification failed outside the current patch scope, so no additional repair was made in this change scope."
		if summary != "" {
			note += "\n" + compactPromptSection(summary, 500)
		}
	}
	return strings.TrimSpace(reply + "\n\n" + note)
}

func disableAllTools(disabled map[string]bool, registry *ToolRegistry) {
	if disabled == nil || registry == nil {
		return
	}
	for _, name := range registry.ToolNames() {
		disabled[name] = true
	}
}

func replySuggestsManualEditHandoff(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"도구 사용에 문제가 있어",
		"직접 패치를 적용해드리지 못",
		"직접 패치를 적용해 드리지 못",
		"직접 수정해주시면",
		"직접 수정해 주시면",
		"직접 고쳐주시면",
		"직접 고쳐 주시면",
		"수동으로 적용",
		"수동 패치",
		"직접 적용하세요",
		"please apply the patch manually",
		"please edit the file manually",
		"please update the code manually",
		"apply the patch manually",
		"edit the file manually",
		"cannot apply the patch directly",
		"could not apply the patch directly",
		"unable to apply the patch directly",
		"cannot modify the file directly",
		"can't modify the file directly",
		"please modify the code yourself",
	)
}

func replyLooksAbruptlyTruncated(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	last := rune(0)
	for _, r := range trimmed {
		last = r
	}
	switch last {
	case '.', '!', '?', ':', ';', ')', ']', '}', '"', '\'', '`':
		return false
	}
	if strings.HasSuffix(trimmed, "```") {
		return false
	}

	lines := strings.Split(trimmed, "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine == "" {
		return false
	}
	if hasUnbalancedContinuationDelimiters(lastLine) {
		return true
	}
	fields := strings.Fields(lastLine)
	if len(fields) > 0 {
		lastToken := strings.ToLower(strings.Trim(fields[len(fields)-1], ".,!?;:()[]{}<>\"'`"))
		switch lastToken {
		case "a", "an", "and", "are", "as", "at", "for", "from", "in", "is", "of", "on", "or", "the", "this", "to", "with",
			"가", "과", "는", "도", "를", "에", "와", "으로", "을", "은", "이", "의":
			return true
		}
	}

	lastLineRunes := []rune(lastLine)
	if len(lastLineRunes) <= 4 && (unicode.IsLetter(last) || unicode.IsDigit(last)) {
		return true
	}
	return false
}

func replyLooksLikeRawReviewHarnessResult(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	first := strings.ToUpper(strings.TrimSpace(lines[0]))
	if first != "REVIEW_RESULT" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "verdict:") || strings.Contains(lower, "findings:")
}

func hasUnbalancedContinuationDelimiters(text string) bool {
	if strings.Count(text, "`")%2 == 1 {
		return true
	}
	if strings.Count(text, "(") > strings.Count(text, ")") {
		return true
	}
	if strings.Count(text, "[") > strings.Count(text, "]") {
		return true
	}
	if strings.Count(text, "{") > strings.Count(text, "}") {
		return true
	}
	return false
}

func mergeAssistantContinuation(prefix string, continuation string) string {
	prefix = strings.TrimSpace(prefix)
	continuation = strings.TrimSpace(continuation)
	if prefix == "" {
		return continuation
	}
	if continuation == "" {
		return prefix
	}
	if needsContinuationSpace(prefix, continuation) {
		return prefix + " " + continuation
	}
	return prefix + continuation
}

func needsContinuationSpace(prefix string, continuation string) bool {
	last := rune(0)
	for _, r := range prefix {
		last = r
	}
	first := rune(0)
	for _, r := range continuation {
		first = r
		break
	}
	if last == 0 || first == 0 {
		return false
	}
	if strings.ContainsRune(".,!?;:)]}\"'`", first) {
		return false
	}
	if isHangulRune(last) && isHangulRune(first) {
		return false
	}
	if unicode.IsLower(last) && unicode.IsUpper(first) {
		return false
	}
	return (unicode.IsLetter(last) || unicode.IsDigit(last)) && (unicode.IsLetter(first) || unicode.IsDigit(first))
}

func isHangulRune(r rune) bool {
	return r >= 0xAC00 && r <= 0xD7A3
}

func sanitizeAssistantMessageText(text string, hasToolCalls bool) string {
	trimmed := strings.TrimSpace(splitAssistantPreambleBoundaries(text))
	if trimmed == "" {
		return ""
	}
	trimmed = stripHiddenAssistantMarkup(trimmed)
	if trimmed == "" {
		return ""
	}
	trimmed = suppressInternalRoutingMarkers(trimmed)
	if trimmed == "" {
		return ""
	}
	if !hasToolCalls {
		return trimmed
	}
	if assistantToolCallTextLooksLikeCompletionSummary(trimmed) {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	kept := make([]string, 0, len(lines))
	sawPreamble := false
	for _, line := range lines {
		current := strings.TrimSpace(line)
		if current == "" {
			continue
		}
		if isAssistantNarrationPreamble(current) {
			sawPreamble = true
			continue
		}
		kept = append(kept, current)
	}
	if len(kept) == 0 && sawPreamble {
		return ""
	}
	return strings.Join(kept, "\n")
}

func assistantTextLooksLikeCompletionSummary(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"final answer",
		"final summary",
		"what was done",
		"what i changed",
		"i have completed",
		"i completed",
		"has been completed",
		"is complete",
		"complete and ready",
		"ready for review",
		"ready for use",
		"no further changes",
		"no further edits",
		"no further action",
		"successfully created",
		"successfully generated",
		"successfully saved",
		"report has been",
		"report is saved",
		"report is complete",
		"report is ready",
		"document has been",
		"document is saved",
		"document is complete",
		"document is ready",
		"has been finalized",
		"has been fully created",
		"has been fully generated",
		"has been fully completed",
		"is now in final form",
		"bug report has been",
		"saved to ",
		"created at ",
		"created in ",
		"generated at ",
		"generated in ",
		"작업 완료",
		"완료되었습니다",
		"완성되었습니다",
		"작성되었습니다",
		"생성되었습니다",
		"저장되었습니다",
		"검증되었습니다",
		"준비되었습니다",
		"준비 완료",
		"최종 요약",
		"수정 내역",
		"보고서 요약",
		"검증 결과",
		"추가 변경 없음",
		"더 이상 변경",
		"더 이상 수정",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	hasArtifactNoun := strings.Contains(normalized, "report") ||
		strings.Contains(normalized, "document") ||
		strings.Contains(normalized, "artifact") ||
		strings.Contains(normalized, "file") ||
		strings.Contains(normalized, "보고서") ||
		strings.Contains(normalized, "문서") ||
		strings.Contains(normalized, "산출물") ||
		strings.Contains(normalized, "파일")
	if hasArtifactNoun {
		for _, marker := range []string{
			"saved to ",
			"saved in ",
			"saved at ",
			"is saved in ",
			"is saved at ",
			"created at ",
			"created in ",
			"generated at ",
			"generated in ",
			"written to ",
			"wrote to ",
			"저장되었습니다",
			"작성되었습니다",
			"생성되었습니다",
		} {
			if strings.Contains(normalized, marker) {
				return true
			}
		}
	}
	return false
}

func assistantTextLooksLikeInProgress(text string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
	if normalized == "" {
		return false
	}
	return containsAny(normalized,
		"still checking",
		"still reviewing",
		"still working",
		"checking the",
		"reviewing the",
		"working on",
		"i am checking",
		"i am reviewing",
		"i'm checking",
		"i'm reviewing",
		"will check",
		"will review",
		"need to inspect",
		"need to review",
		"need to run",
		"need to execute",
		"need to verify",
		"will run",
		"will execute",
		"will verify",
		"not finished",
		"not done yet",
		"continuing",
		"i will run",
		"i will now run",
		"i will verify",
		"i will now verify",
		"i'll run",
		"i'll now run",
		"i'll verify",
		"i'll now verify",
		"going to run",
		"going to verify",
		"next i will",
		"next i'll",
		"verify next",
		"진행 중",
		"확인 중",
		"검토 중",
		"작업 중",
		"수정 중",
		"테스트 중",
		"아직 확인",
		"아직 검토",
		"계속 진행",
		"실행하겠습니다",
		"검증하겠습니다",
		"확인하겠습니다",
		"검증해야",
		"검증할 예정",
	)
}

func assistantToolCallTextLooksLikeCompletionSummary(text string) bool {
	return assistantTextLooksLikeCompletionSummary(text)
}

var hiddenAssistantMarkupPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<oai-mem-citation\b[^>]*>.*?</oai-mem-citation>`),
	regexp.MustCompile(`(?is)<oai-mem-citation\b[^>]*>.*`),
	regexp.MustCompile(`(?is)<proposed_plan\b[^>]*>.*?</proposed_plan>`),
	regexp.MustCompile(`(?is)<proposed_plan\b[^>]*>.*`),
}

func stripHiddenAssistantMarkup(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	for _, pattern := range hiddenAssistantMarkupPatterns {
		trimmed = pattern.ReplaceAllString(trimmed, "")
	}
	return strings.TrimSpace(trimmed)
}

type hiddenAssistantMarkupDeltaFilter struct {
	pending string
	hiding  string
}

func (f *hiddenAssistantMarkupDeltaFilter) Push(delta string) string {
	if f == nil {
		return delta
	}
	if delta == "" {
		return ""
	}
	f.pending += delta
	var out strings.Builder
	for {
		if f.hiding != "" {
			closeTag := "</" + f.hiding + ">"
			if index := strings.Index(f.pending, closeTag); index >= 0 {
				f.pending = f.pending[index+len(closeTag):]
				f.hiding = ""
				continue
			}
			keep := longestSuffixPrefix(f.pending, closeTag, false)
			if keep > 0 {
				f.pending = f.pending[len(f.pending)-keep:]
			} else {
				f.pending = ""
			}
			return out.String()
		}
		index, tagName := earliestHiddenAssistantOpenTag(f.pending)
		if index >= 0 {
			out.WriteString(f.pending[:index])
			openEnd := strings.Index(f.pending[index:], ">")
			if openEnd < 0 {
				f.pending = f.pending[index:]
				return out.String()
			}
			f.pending = f.pending[index+openEnd+1:]
			f.hiding = tagName
			continue
		}
		keep := longestHiddenAssistantOpenSuffix(f.pending)
		if keep > 0 {
			out.WriteString(f.pending[:len(f.pending)-keep])
			f.pending = f.pending[len(f.pending)-keep:]
		} else {
			out.WriteString(f.pending)
			f.pending = ""
		}
		return out.String()
	}
}

func (f *hiddenAssistantMarkupDeltaFilter) Flush() string {
	if f == nil {
		return ""
	}
	if f.hiding != "" {
		f.pending = ""
		f.hiding = ""
		return ""
	}
	out := f.pending
	f.pending = ""
	return out
}

type hiddenAssistantMarkupTag struct {
	Name      string
	OpenStart string
}

var hiddenAssistantMarkupTags = []hiddenAssistantMarkupTag{
	{Name: "oai-mem-citation", OpenStart: "<oai-mem-citation"},
	{Name: "proposed_plan", OpenStart: "<proposed_plan"},
}

func earliestHiddenAssistantOpenTag(text string) (int, string) {
	bestIndex := -1
	bestName := ""
	for _, tag := range hiddenAssistantMarkupTags {
		index := strings.Index(text, tag.OpenStart)
		if index < 0 {
			continue
		}
		if bestIndex < 0 || index < bestIndex {
			bestIndex = index
			bestName = tag.Name
		}
	}
	return bestIndex, bestName
}

func longestHiddenAssistantOpenSuffix(text string) int {
	best := 0
	for _, tag := range hiddenAssistantMarkupTags {
		if n := longestSuffixPrefix(text, tag.OpenStart, true); n > best {
			best = n
		}
	}
	return best
}

func longestSuffixPrefix(text string, prefix string, allowFullPrefix bool) int {
	limit := len(prefix) - 1
	if allowFullPrefix {
		limit = len(prefix)
	}
	if len(text) < limit {
		limit = len(text)
	}
	for n := limit; n > 0; n-- {
		if strings.HasSuffix(text, prefix[:n]) {
			return n
		}
	}
	return 0
}

func suppressInternalRoutingMarkers(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		current := strings.TrimSpace(line)
		if current == "" {
			kept = append(kept, line)
			continue
		}
		lower := strings.ToLower(current)
		if strings.EqualFold(current, projectAnalysisFastPathNeedsTools) {
			continue
		}
		if strings.HasPrefix(lower, "cached analysis fast-path") {
			continue
		}
		if strings.HasPrefix(lower, "fast-path check:") {
			continue
		}
		if strings.Contains(lower, "needs_tools") && len(current) <= 80 {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func isAssistantNarrationPreamble(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.HasPrefix(lower, "let me "):
		return true
	case strings.HasPrefix(lower, "i understand "):
		return true
	case strings.HasPrefix(lower, "now let me "):
		return true
	case strings.HasPrefix(lower, "now i "):
		return true
	case strings.HasPrefix(lower, "the system is "):
		return true
	case strings.HasPrefix(lower, "there's a tool execution deadlock"):
		return true
	case strings.HasPrefix(lower, "there is a tool execution deadlock"):
		return true
	case strings.HasPrefix(lower, "i'm experiencing a tool execution deadlock"):
		return true
	case strings.HasPrefix(lower, "i am experiencing a tool execution deadlock"):
		return true
	case strings.HasPrefix(lower, "i'll "):
		return true
	case strings.HasPrefix(lower, "i will "):
		return true
	case strings.HasPrefix(lower, "i need to "):
		return true
	case strings.HasPrefix(lower, "first, "):
		return true
	case strings.HasPrefix(lower, "이제 "):
		return true
	case strings.HasPrefix(lower, "먼저 "):
		return true
	case strings.HasPrefix(lower, "우선 "):
		return true
	case strings.HasPrefix(lower, "잠깐"):
		return true
	case strings.HasPrefix(lower, "잠시"):
		return true
	case strings.HasPrefix(lower, "다시 "):
		return true
	default:
		return false
	}
}

func (a *Agent) completeModelTurn(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	req = a.attachProviderRequestMetadata(req)
	req = a.attachProgressEventHandler(req)
	maxRetries := configMaxRequestRetries(a.Config)
	totalAttempts := maxRetries + 1
	baseDelay := configRequestRetryDelay(a.Config)
	for attempt := 0; attempt < totalAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ChatResponse{}, err
		}

		attemptCtx, cancel := context.WithTimeout(ctx, configRequestTimeout(a.Config))
		resp, err := a.completeModelTurnOnce(attemptCtx, req)
		cancel()
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return ChatResponse{}, ctx.Err()
		}
		if !shouldRetryProviderError(err) || attempt == totalAttempts-1 {
			a.noteProviderConversationError(err, req, true)
			return ChatResponse{}, err
		}
		a.noteProviderConversationError(err, req, false)
		delay := providerRetryDelay(baseDelay, attempt)
		a.emitProgressEvent(ProgressEvent{
			Kind:    progressKindProviderRetry,
			Message: modelRetryProgressMessage(err, attempt, totalAttempts, delay),
			Model:   req.Model,
			Status:  firstNonEmptyLine(err.Error()),
		})
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ChatResponse{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return ChatResponse{}, context.DeadlineExceeded
}

func modelRetryProgressMessage(err error, attempt int, totalAttempts int, delay time.Duration) string {
	base := "Transient provider error during model request."
	if errors.Is(err, context.DeadlineExceeded) {
		base = "Model request timed out."
	}
	if totalAttempts == 2 && attempt == 0 {
		return base + " Retrying once..."
	}
	if delay <= 0 {
		return fmt.Sprintf("%s Retrying (attempt %d/%d)...", base, attempt+2, totalAttempts)
	}
	return fmt.Sprintf("%s Retrying in %s (attempt %d/%d)...", base, delay.Round(time.Second), attempt+2, totalAttempts)
}

func isToolUseUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "no endpoints found that support tool use") ||
		strings.Contains(lower, "does not support tool use") ||
		strings.Contains(lower, "tool use is not supported")
}

func (a *Agent) completeModelTurnOnce(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if a == nil {
		return ChatResponse{}, fmt.Errorf("no model provider is configured")
	}
	return completeModelTurnOnceWithModelRoutes(ctx, a.modelRouteScheduler(), a.modelRoutePolicy(), a.Config, a.Client, req)
}

func (a *Agent) emitProviderResponseMetadata(req ChatRequest, resp ChatResponse, serverModelWarningEmitted *bool, modelVerificationEmitted *bool) {
	if a == nil {
		return
	}
	requestedModel := strings.TrimSpace(req.Model)
	serverModel := strings.TrimSpace(resp.ServerModel)
	if serverModel != "" &&
		requestedModel != "" &&
		!strings.EqualFold(serverModel, requestedModel) &&
		(serverModelWarningEmitted == nil || !*serverModelWarningEmitted) {
		if serverModelWarningEmitted != nil {
			*serverModelWarningEmitted = true
		}
		a.emitProgressEvent(ProgressEvent{
			Kind:    progressKindModelReroute,
			Model:   requestedModel,
			Status:  serverModel,
			Message: openAICodexServerModelMismatchMessage(requestedModel, serverModel),
		})
	}
	if len(resp.ModelVerifications) == 0 ||
		(modelVerificationEmitted != nil && *modelVerificationEmitted) {
		return
	}
	if modelVerificationEmitted != nil {
		*modelVerificationEmitted = true
	}
	a.emitProgressEvent(ProgressEvent{
		Kind:   progressKindModelVerification,
		Model:  requestedModel,
		Status: strings.Join(resp.ModelVerifications, ", "),
	})
}

func openAICodexServerModelMismatchMessage(requestedModel string, serverModel string) string {
	requestedModel = strings.TrimSpace(requestedModel)
	serverModel = strings.TrimSpace(serverModel)
	if strings.EqualFold(serverModel, "gpt-5.2") && strings.Contains(strings.ToLower(requestedModel), "codex") {
		return "Your account was flagged for potentially high-risk cyber activity and this request was routed to gpt-5.2 as a fallback. To regain access to Codex models, apply for trusted access: https://chatgpt.com/cyber or learn more: https://developers.openai.com/codex/concepts/cyber-safety"
	}
	if requestedModel == "" || serverModel == "" {
		return ""
	}
	return fmt.Sprintf("Server routed request to %s instead of %s.", serverModel, requestedModel)
}

func (a *Agent) attachProviderRequestMetadata(req ChatRequest) ChatRequest {
	if a == nil || a.Session == nil {
		return req
	}
	sessionID := strings.TrimSpace(a.Session.ID)
	if strings.TrimSpace(req.SessionID) == "" {
		req.SessionID = sessionID
	}
	if strings.TrimSpace(req.ThreadID) == "" {
		req.ThreadID = sessionID
	}
	if len(req.TurnMetadata) == 0 {
		req.TurnMetadata = providerTurnMetadataFromMCP(a.mcpTurnMetadataForToolCall(time.Now()))
	} else {
		req.TurnMetadata = cloneStringAnyMap(req.TurnMetadata)
	}
	return req
}

func toolCallSignature(calls []ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, 0, len(calls))
	for _, call := range calls {
		parts = append(parts, strings.TrimSpace(call.Name)+"\x1f"+normalizeToolArguments(call.Arguments))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x1e")
}

func shouldTrackRepeatedToolCallSignature(calls []ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		if strings.TrimSpace(call.Name) != "read_file" {
			return true
		}
	}
	return false
}

func normalizeToolArguments(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return trimmed
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return trimmed
	}
	return string(canonical)
}

func repeatedReadFilePathKey(calls []ToolCall) (string, bool) {
	if len(calls) == 0 {
		return "", false
	}

	key := ""
	for _, call := range calls {
		if strings.TrimSpace(call.Name) != "read_file" {
			return "", false
		}
		path := readFilePathKey(call.Arguments)
		if path == "" {
			return "", false
		}
		if key == "" {
			key = path
			continue
		}
		if key != path {
			return "", false
		}
	}

	return key, key != ""
}

func readFilePathKey(arguments string) string {
	args := map[string]any{}
	if strings.TrimSpace(arguments) != "" {
		_ = json.Unmarshal([]byte(arguments), &args)
	}
	path := strings.TrimSpace(stringValue(args, "path"))
	if path == "" {
		return ""
	}
	key := filepath.ToSlash(filepath.Clean(path))
	if readFilePathKeyUsesCaseInsensitiveMatch(path) {
		key = strings.ToLower(key)
	}
	return key
}

func readFilePathKeyUsesCaseInsensitiveMatch(path string) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	trimmed := strings.TrimSpace(path)
	if len(trimmed) >= 2 && trimmed[1] == ':' {
		return true
	}
	return strings.HasPrefix(trimmed, `\\`) || strings.HasPrefix(trimmed, `//`)
}

func summarizeEditToolResult(name, out string) string {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "--- ") || strings.HasPrefix(trimmed, "+++ ") || strings.HasPrefix(trimmed, "@@") {
			continue
		}
		switch name {
		case "apply_patch":
			return "Patch applied: " + trimmed
		case "write_file":
			return "File updated: " + trimmed
		case "replace_in_file":
			return "Replacement applied: " + trimmed
		default:
			return trimmed
		}
	}
	switch name {
	case "apply_patch":
		return "Patch applied successfully."
	case "write_file":
		return "File updated successfully."
	case "replace_in_file":
		return "Replacement applied successfully."
	default:
		return ""
	}
}

func invalidToolArgumentsGuidance(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "write_file":
		return "Your last write_file call used malformed or truncated JSON arguments. write_file is now disabled for this request. Do not repeat the same write_file payload. If you are editing an existing file, first read the exact file path again and use apply_patch instead of write_file. Only use write_file for creating a new file or fully rewriting a file with one complete valid JSON object."
	case "apply_patch":
		return "Your last apply_patch call used malformed or truncated JSON arguments. Retry with one complete valid JSON object whose patch field contains the full raw patch text. Do not send partial JSON or cut off the patch string."
	case "replace_in_file":
		return "Your last replace_in_file call used malformed or truncated JSON arguments. replace_in_file is now disabled for this request. Re-read the file and retry with one complete valid JSON object. If the change is larger than a tiny exact substitution, use apply_patch instead."
	default:
		return "Your last tool call used malformed or truncated JSON arguments. Retry with one complete valid JSON object only. Do not repeat the same broken payload."
	}
}

func invalidPatchFormatGuidance(repeatedSignature bool, err error) string {
	reason := ""
	if err != nil {
		reason = firstNonEmptyLine(err.Error())
	}
	if repeatedSignature {
		return strings.TrimSpace("Your last apply_patch call repeated the same invalid patch signature. Do not retry the same patch text again.\n" +
			"First read the exact target file again, confirm the current contents and path, then create a fresh patch from that current text.\n" +
			"The patch must start exactly with:\n*** Begin Patch\n" +
			"Then use one or more file sections like *** Update File:, *** Add File:, or *** Delete File:, and end with:\n*** End Patch\n" +
			"For every *** Update File: section, include at least one @@ hunk with context and +/- lines.\n" +
			"Last parser error: " + reason)
	}
	return strings.TrimSpace("Your last apply_patch call used the wrong patch format. Retry using the tool again and make the patch string start exactly with:\n*** Begin Patch\n" +
		"Then use one or more file sections like *** Update File:, *** Add File:, or *** Delete File:, and end with:\n*** End Patch\n" +
		"For every *** Update File: section, include at least one @@ hunk with context and +/- lines. Do not send an update section with no hunks, and do not send prose, JSON, or code fences inside the patch string.\n" +
		"Last parser error: " + reason)
}

func isPreWriteReviewBlockedError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	return strings.Contains(text, "automatic pre-write review blocked this edit before writing") ||
		strings.Contains(text, "automatic pre-write review blocked this edit on actionable warnings before writing") ||
		strings.Contains(text, "자동 쓰기 전 리뷰가 파일 쓰기를 차단했습니다") ||
		strings.Contains(text, "자동 쓰기 전 리뷰가 수정 필요한 경고 때문에 파일 쓰기를 차단했습니다")
}

func formatPreWriteReviewRepairLoopLimitReply(cfg Config, session *Session) string {
	return formatPreWriteReviewRepairUserDecisionReply(
		cfg,
		session,
		"The revised edit still did not pass the pre-write review.",
		"수정안이 아직 쓰기 전 리뷰 모델을 통과하지 못했습니다.",
	)
}

func preWriteReviewRepairBlockFingerprint(session *Session, err error) string {
	if session == nil || session.LastReviewRun == nil {
		return preWriteReviewRepairFallbackFingerprint(err)
	}
	run := *session.LastReviewRun
	run.Gate.BlockingFindings = normalizeTaskStateList(run.Gate.BlockingFindings, 64)
	run.Gate.WarningFindings = normalizeTaskStateList(run.Gate.WarningFindings, 64)
	actionable := preWriteReviewRepairActionableFindingFingerprintParts(run)
	if len(actionable) > 0 {
		parts := []string{
			"actionable",
			strings.TrimSpace(run.Trigger),
			strings.TrimSpace(run.Gate.Verdict),
		}
		parts = append(parts, actionable...)
		return strings.Join(parts, "\n")
	}
	fallback := preWriteReviewRepairVisibleFindingFingerprintParts(run)
	if len(fallback) > 0 {
		parts := []string{
			"visible",
			strings.TrimSpace(run.Trigger),
			strings.TrimSpace(run.Gate.Verdict),
		}
		parts = append(parts, fallback...)
		return strings.Join(parts, "\n")
	}
	return preWriteReviewRepairFallbackFingerprint(err)
}

func preWriteReviewRepairActionableFindingFingerprintParts(run ReviewRun) []string {
	ids := append([]string{}, run.Gate.BlockingFindings...)
	ids = append(ids, run.Gate.WarningFindings...)
	idSet := reviewFindingIDSet(ids)
	parts := make([]string, 0)
	for _, finding := range run.Findings {
		finding.Normalize()
		if !preWriteReviewRepairFindingMatchesGate(finding, idSet) {
			continue
		}
		if !preWriteReviewRepairFindingIsActionable(run, finding) {
			continue
		}
		parts = append(parts, preWriteReviewRepairFindingFingerprintPart(finding))
	}
	sort.Strings(parts)
	return parts
}

func preWriteReviewRepairVisibleFindingFingerprintParts(run ReviewRun) []string {
	ids := append([]string{}, run.Gate.BlockingFindings...)
	ids = append(ids, run.Gate.WarningFindings...)
	idSet := reviewFindingIDSet(ids)
	parts := make([]string, 0)
	for _, finding := range run.Findings {
		finding.Normalize()
		if !preWriteReviewRepairFindingMatchesGate(finding, idSet) {
			continue
		}
		if strings.TrimSpace(firstNonBlankString(finding.ID, finding.Title, finding.RequiredFix, finding.Evidence)) == "" {
			continue
		}
		parts = append(parts, preWriteReviewRepairFindingFingerprintPart(finding))
	}
	sort.Strings(parts)
	return parts
}

func preWriteReviewRepairFindingMatchesGate(finding ReviewFinding, idSet map[string]bool) bool {
	if len(idSet) == 0 {
		return true
	}
	id := strings.TrimSpace(finding.ID)
	if id == "" {
		return false
	}
	return idSet[id]
}

func preWriteReviewRepairFindingIsActionable(run ReviewRun, finding ReviewFinding) bool {
	if strings.EqualFold(strings.TrimSpace(finding.ID), requiredReviewerFailureFindingID) {
		return false
	}
	if preWritePreFixFindingIsConcreteRepairObligation(finding) {
		return true
	}
	return reviewFindingIsActionableNonReviewerFinding(run, finding, nil)
}

func preWriteReviewRepairFindingFingerprintPart(finding ReviewFinding) string {
	return strings.Join([]string{
		strings.TrimSpace(finding.ID),
		strings.TrimSpace(finding.Source),
		strings.TrimSpace(finding.ReviewerRole),
		strings.TrimSpace(finding.Severity),
		strings.TrimSpace(finding.Category),
		compactPromptSection(finding.Title, 180),
		compactPromptSection(finding.RequiredFix, 260),
		compactPromptSection(finding.Evidence, 260),
	}, "|")
}

func preWriteReviewRepairFallbackFingerprint(err error) string {
	if err == nil {
		return ""
	}
	return "error|" + firstNonEmptyLine(err.Error())
}

func formatPreWriteReviewRepairInspectionLoopLimitReply(cfg Config, session *Session) string {
	return formatPreWriteReviewRepairUserDecisionReply(
		cfg,
		session,
		"The pre-write review did not pass, and the repair pass used its local inspection budget without producing a reviewed patch.",
		"쓰기 전 리뷰 모델을 통과하지 못했고, 수리 단계가 검토된 패치를 만들기 전에 로컬 상태 확인 예산을 사용했습니다.",
	)
}

func formatPreWriteReviewRepairUserDecisionReply(cfg Config, session *Session, englishIntro string, koreanIntro string) string {
	recordPendingReviewRepairConfirmation(session)
	korean := localePrefersKorean(cfg)
	var b strings.Builder
	if korean {
		b.WriteString(koreanIntro)
	} else {
		b.WriteString(englishIntro)
	}
	if reviewText := formatLatestPreWriteReviewForUserDecision(cfg, session); reviewText != "" {
		if korean {
			b.WriteString("\n\n[1] 최신 리뷰\n")
		} else {
			b.WriteString("\n\n[1] Latest review\n")
		}
		b.WriteString(reviewText)
	}
	if carried := formatLatestPreWriteCarriedRepairObligationsForUserDecision(cfg, session); carried != "" {
		if korean {
			b.WriteString("\n\n[1a] 계속 유효한 전체 수리 의무\n")
		} else {
			b.WriteString("\n\n[1a] Still-active full repair obligations\n")
		}
		b.WriteString(carried)
	}
	if proposalText := formatLatestEditProposalForUserDecision(cfg, session); proposalText != "" {
		if korean {
			b.WriteString("\n\n[2] 마지막 수정안\n")
		} else {
			b.WriteString("\n\n[2] Latest edit proposal\n")
		}
		b.WriteString(proposalText)
	}
	if korean {
		b.WriteString("\n\n[3] 다음 선택\n이 검토 결과를 기준으로 계속 수정할까요? [y/N]\n`y` 또는 `n`만 입력해 주세요.")
	} else {
		b.WriteString("\n\n[3] Next decision\nShould I keep repairing from this review result? [y/N]\nReply with exactly `y` or `n`.")
	}
	return strings.TrimSpace(b.String())
}

func formatReviewerGateUnavailableUserDecisionReply(cfg Config, session *Session) string {
	return formatReviewerGateUnavailableUserDecisionContent(cfg, session, true)
}

func formatReviewerGateUnavailableUserDecisionPrompt(cfg Config, session *Session) string {
	return formatReviewerGateUnavailableUserDecisionContent(cfg, session, false)
}

func formatReviewerGateUnavailableUserDecisionContent(cfg Config, session *Session, includeInlinePrompt bool) string {
	korean := localePrefersKorean(cfg)
	if session != nil && session.LastReviewRun != nil {
		korean = reviewRunPrefersKorean(cfg, *session.LastReviewRun)
	}
	var lastRun *ReviewRun
	if session != nil {
		lastRun = session.LastReviewRun
	}
	var b strings.Builder
	preWriteGate := lastRun != nil && strings.EqualFold(strings.TrimSpace(lastRun.Trigger), "pre_write")
	if korean {
		if preWriteGate {
			b.WriteString("쓰기 전 리뷰어 게이트: 통과하지 못함")
		} else {
			b.WriteString("리뷰어 게이트: 통과하지 못함")
		}
		b.WriteString("\n- 결과: 코드 수정은 적용하지 않았습니다.")
		b.WriteString("\n- 원인: 필수 리뷰 단계의 모델 route가 실패했거나 `weak` 품질로 판정되었습니다. `primary` 실패는 현재 메인 모델 route 문제이고, `cross` 실패는 전용 reviewer route 문제입니다.")
		b.WriteString("\n- 중요한 점: 이 상태는 쓰기 승인도, 리뷰 우회 승인도 아닙니다.")
		if preWriteGate {
			b.WriteString("\n- 다음 조건: 최신 리뷰 finding과 마지막 수정안을 기준으로 다시 수리한 뒤, 일반 파일 쓰기 경로에서 pre-write review를 다시 통과해야 합니다.")
		} else {
			b.WriteString("\n- 다음 조건: 실패한 리뷰 route를 복구하거나 모델을 바꾼 뒤 같은 요청을 다시 실행해야 합니다.")
		}
	} else {
		if preWriteGate {
			b.WriteString("Pre-write reviewer gate: not approved (did not pass)")
		} else {
			b.WriteString("Reviewer gate: not approved (did not pass)")
		}
		b.WriteString("\n- Result: no code changes were applied.")
		b.WriteString("\n- Cause: a required review-stage model route failed or was classified as `weak` quality. A `primary` failure points at the active main model route; a `cross` failure points at a dedicated reviewer route.")
		b.WriteString("\n- Important: this is not write approval and not approval to bypass review.")
		if preWriteGate {
			b.WriteString("\n- Next condition: repair from the latest review findings and last edit proposal below, then pass the normal pre-write review through the edit tool path.")
		} else {
			b.WriteString("\n- Next condition: restore the failed review route or change model, then rerun the same request.")
		}
	}
	if recoveryOptions := formatReviewerGateRecoveryOptions(korean, lastRun); recoveryOptions != "" {
		b.WriteString("\n\n")
		b.WriteString(recoveryOptions)
	}
	if session != nil && session.LastReviewRun != nil {
		failed := reviewFailedRequiredReviewerRuns(*session.LastReviewRun)
		if len(failed) > 0 {
			if korean {
				b.WriteString("\n\n[0] 실패한 리뷰어")
			} else {
				b.WriteString("\n\n[0] Failed reviewer")
			}
			for _, reviewerRun := range failed {
				role := firstNonBlankString(reviewRoleProgressName(reviewerRun.Role), "reviewer")
				status := valueOrDefault(strings.TrimSpace(reviewerRun.Status), "unknown")
				quality := valueOrDefault(strings.TrimSpace(reviewerRun.ModelQuality), "unknown")
				detail := firstNonBlankString(firstNonEmptyLine(reviewerRun.Error), "reviewer output was too weak")
				fmt.Fprintf(&b, "\n- %s status=%s quality=%s: %s", role, status, quality, detail)
			}
		}
	}
	if reviewText := formatLatestPreWriteReviewForUserDecision(cfg, session); reviewText != "" {
		if korean {
			b.WriteString("\n\n[1] 최신 리뷰")
		} else {
			b.WriteString("\n\n[1] Latest review")
		}
		b.WriteString("\n")
		b.WriteString(reviewText)
	}
	if carried := formatLatestPreWriteCarriedRepairObligationsForUserDecision(cfg, session); carried != "" {
		if korean {
			b.WriteString("\n\n[1a] 계속 유효한 전체 수리 의무")
		} else {
			b.WriteString("\n\n[1a] Still-active full repair obligations")
		}
		b.WriteString("\n")
		b.WriteString(carried)
	}
	if proposalText := formatLatestEditProposalForUserDecision(cfg, session); proposalText != "" {
		if korean {
			b.WriteString("\n\n[2] 마지막 수정안")
		} else {
			b.WriteString("\n\n[2] Latest edit proposal")
		}
		b.WriteString("\n")
		b.WriteString(proposalText)
	}
	if reviewRunHasActionableNonReviewerFindingsFromSession(session) {
		if korean {
			b.WriteString("\n\n[3] 다음 선택\n위의 코드 finding을 기준으로 계속 수리할 수 있습니다.")
			if includeInlinePrompt {
				b.WriteString(" 계속 수리할까요? [y/N]\n`y` 또는 `n`만 입력해 주세요.")
			}
		} else {
			b.WriteString("\n\n[3] Next decision\nI can keep repairing from the code findings above.")
			if includeInlinePrompt {
				b.WriteString(" Should I keep repairing? [y/N]\nReply with exactly `y` or `n`.")
			}
		}
	} else if korean {
		b.WriteString("\n\n[3] 다음 조치\n이번 중단은 코드 finding 때문이 아니라 필수 리뷰 단계의 모델 route 실패/약한 응답 때문입니다.")
		b.WriteString("\n- 지금 계속해도 수정할 코드 항목이 없으므로 추가 편집은 진행하지 않습니다.")
		b.WriteString("\n- `[0] 실패한 리뷰어`에 `primary`가 보이면 현재 메인 모델 route가 문제입니다. `/model`로 메인 모델을 바꾸거나 해당 provider route를 먼저 복구하세요.")
		b.WriteString("\n- `cross` 또는 전용 reviewer가 보이면 `/review models`로 해당 reviewer route를 정상 동작하는 모델로 바꾸세요.")
		b.WriteString("\n- route를 복구한 뒤 같은 요청을 다시 실행하세요.")
		if includeInlinePrompt {
			b.WriteString("\n- 이 상태에서는 `y/N` 선택을 받지 않습니다.")
		}
	} else {
		b.WriteString("\n\n[3] Next step\nThis stop was caused by a required review-stage model route failure or weak output, not by a code finding.")
		b.WriteString("\n- There is no code item to repair right now, so I will not continue editing.")
		b.WriteString("\n- If `[0] Failed reviewer` shows `primary`, the active main model route is the problem. Use `/model` to switch the main model or fix that provider route first.")
		b.WriteString("\n- If it shows `cross` or a dedicated reviewer, use `/review models` to switch that reviewer route to a working model.")
		b.WriteString("\n- Restore the route, then rerun the same request.")
		if includeInlinePrompt {
			b.WriteString("\n- No `y/N` continuation is offered in this state.")
		}
	}
	return strings.TrimSpace(b.String())
}

func recordPendingReviewRepairConfirmation(session *Session) {
	recordPendingReviewRepairConfirmationWithMode(session, reviewRepairConfirmationModeRepair)
}

func recordPendingReviewerGateRepairConfirmation(session *Session) {
	recordPendingReviewRepairConfirmationWithMode(session, reviewRepairConfirmationModeReviewerGateUnavailable)
}

func recordPendingReviewRepairConfirmationWithMode(session *Session, mode string) {
	if session == nil {
		return
	}
	state := &ReviewRepairConfirmationState{
		CreatedAt: time.Now(),
		Mode:      strings.TrimSpace(mode),
	}
	if session.LastReviewRun != nil {
		state.ReviewID = session.LastReviewRun.ID
		state.Verdict = valueOrDefault(session.LastReviewRun.Gate.Verdict, session.LastReviewRun.Result.Verdict)
	}
	session.PendingReviewRepairConfirm = state
	session.UpdatedAt = time.Now()
}

func formatLatestPreWriteReviewForUserDecision(cfg Config, session *Session) string {
	if session == nil || session.LastReviewRun == nil {
		return ""
	}
	run := *session.LastReviewRun
	run.Findings = append([]ReviewFinding(nil), run.Findings...)
	for i := range run.Findings {
		run.Findings[i].Normalize()
	}
	korean := reviewRunPrefersKorean(cfg, run)
	verdict := valueOrDefault(run.Gate.Verdict, run.Result.Verdict)
	blockerCount := len(run.Gate.BlockingFindings)
	warningCount := len(run.Gate.WarningFindings)
	var b strings.Builder
	if korean {
		fmt.Fprintf(&b, "마지막 검토 결과: %s (차단=%d, 경고=%d)", valueOrUnset(verdict), blockerCount, warningCount)
	} else {
		fmt.Fprintf(&b, "Latest review result: %s (blockers=%d, warnings=%d)", valueOrUnset(verdict), blockerCount, warningCount)
	}
	if strings.TrimSpace(run.Result.Summary) != "" {
		if korean {
			fmt.Fprintf(&b, "\n요약: %s", compactPromptSection(run.Result.Summary, 500))
		} else {
			fmt.Fprintf(&b, "\nSummary: %s", compactPromptSection(run.Result.Summary, 500))
		}
	}
	findings := latestReviewDecisionFindings(run)
	if len(findings) > 0 {
		if korean {
			b.WriteString("\n주요 finding:")
		} else {
			b.WriteString("\nKey findings:")
		}
		for _, finding := range findings {
			title := compactPromptSection(finding.Title, 180)
			if title == "" {
				title = compactPromptSection(finding.RequiredFix, 180)
			}
			fmt.Fprintf(&b, "\n- %s [%s/%s]: %s", valueOrUnset(finding.ID), valueOrUnset(finding.Severity), valueOrUnset(finding.Category), title)
			if strings.TrimSpace(finding.RequiredFix) != "" {
				if korean {
					fmt.Fprintf(&b, " -> 조치: %s", compactPromptSection(localizedReviewRequiredFixText(finding.RequiredFix, true), 260))
				} else {
					fmt.Fprintf(&b, " -> fix: %s", compactPromptSection(finding.RequiredFix, 260))
				}
			}
		}
	}
	if len(run.ArtifactRefs) > 0 {
		if korean {
			fmt.Fprintf(&b, "\n보고서: %s", run.ArtifactRefs[len(run.ArtifactRefs)-1])
		} else {
			fmt.Fprintf(&b, "\nReport: %s", run.ArtifactRefs[len(run.ArtifactRefs)-1])
		}
	}
	return strings.TrimSpace(b.String())
}

func formatLatestPreWriteCarriedRepairObligationsForUserDecision(cfg Config, session *Session) string {
	if session == nil || session.LastReviewRun == nil {
		return ""
	}
	run := *session.LastReviewRun
	if len(run.RepairFindings) == 0 {
		return ""
	}
	korean := reviewRunPrefersKorean(cfg, run)
	text := formatPreWriteCarriedRepairObligationsFeedback(run, korean)
	return compactPromptSection(text, 2400)
}

func latestReviewDecisionFindings(run ReviewRun) []ReviewFinding {
	ids := append([]string{}, run.Gate.BlockingFindings...)
	if len(ids) == 0 {
		ids = append(ids, run.Gate.WarningFindings...)
	}
	idSet := reviewFindingIDSet(ids)
	var out []ReviewFinding
	if len(idSet) > 0 {
		for _, finding := range run.Findings {
			finding.Normalize()
			if idSet[finding.ID] {
				out = append(out, finding)
			}
			if len(out) >= 4 {
				return out
			}
		}
	}
	if len(out) == 0 {
		for _, finding := range run.Findings {
			finding.Normalize()
			if reviewFindingBlocksGate(run, finding) || reviewFindingCountsAsWarning(finding) {
				out = append(out, finding)
			}
			if len(out) >= 4 {
				return out
			}
		}
	}
	return out
}

func formatLatestEditProposalForUserDecision(cfg Config, session *Session) string {
	toolName, proposal := latestEditToolProposalForUserDecision(session)
	if proposal == "" {
		return ""
	}
	korean := localePrefersKorean(cfg)
	var b strings.Builder
	if korean {
		fmt.Fprintf(&b, "마지막 수정안(%s):\n", toolName)
	} else {
		fmt.Fprintf(&b, "Latest edit proposal (%s):\n", toolName)
	}
	b.WriteString("```text\n")
	b.WriteString(compactPromptSection(proposal, 4000))
	b.WriteString("\n```")
	return strings.TrimSpace(b.String())
}

func latestEditToolProposalForUserDecision(session *Session) (string, string) {
	toolName, proposal, latestIdx := latestEditToolProposalWithIndex(session)
	if session == nil || strings.TrimSpace(proposal) == "" {
		return toolName, proposal
	}
	if !lastReviewRequiresNonIncludeCodePatch(session) || !proposalLooksIncludeOnly(proposal) {
		return toolName, proposal
	}
	startIdx := latestEditProposalFallbackStart(session, latestIdx)
	for i := latestIdx; i >= startIdx; i-- {
		msg := session.Messages[i]
		for j := len(msg.ToolCalls) - 1; j >= 0; j-- {
			call := msg.ToolCalls[j]
			if !isEditTool(call.Name) {
				continue
			}
			candidate := editToolProposalText(call.Name, call.Arguments)
			if strings.TrimSpace(candidate) == "" || proposalLooksIncludeOnly(candidate) {
				continue
			}
			return call.Name, candidate
		}
	}
	return toolName, proposal
}

func latestEditToolProposal(session *Session) (string, string) {
	toolName, proposal, _ := latestEditToolProposalWithIndex(session)
	return toolName, proposal
}

func latestEditToolProposalWithIndex(session *Session) (string, string, int) {
	if session == nil {
		return "", "", -1
	}
	for i := len(session.Messages) - 1; i >= 0; i-- {
		msg := session.Messages[i]
		for j := len(msg.ToolCalls) - 1; j >= 0; j-- {
			call := msg.ToolCalls[j]
			if !isEditTool(call.Name) {
				continue
			}
			proposal := editToolProposalText(call.Name, call.Arguments)
			if strings.TrimSpace(proposal) != "" {
				return call.Name, proposal, i
			}
		}
	}
	return "", "", -1
}

func latestEditProposalFallbackStart(session *Session, latestIdx int) int {
	if session == nil || latestIdx <= 0 {
		return latestIdx
	}
	for idx := latestIdx - 1; idx >= 0; idx-- {
		if messageLooksLikeCurrentReviewBoundary(session.Messages[idx], session.LastReviewRun) {
			return idx + 1
		}
	}
	return 0
}

func messageLooksLikeCurrentReviewBoundary(msg Message, run *ReviewRun) bool {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return false
	}
	if run != nil {
		if id := strings.TrimSpace(run.ID); id != "" && strings.Contains(text, id) {
			return true
		}
	}
	lower := strings.ToLower(text)
	return containsAny(text,
		"검토 결과",
		"리뷰 결과",
		"최신 리뷰",
		"수정 확인 대상",
		"남은 검토 항목",
		"자동 쓰기 전 리뷰",
	) || containsAny(lower,
		"latest review",
		"review result",
		"review finding",
		"review findings",
		"pre-write review",
		"remaining review",
	)
}

func proposalLooksIncludeOnly(proposal string) bool {
	lines := strings.Split(proposal, "\n")
	changed := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" ||
			strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "@@") {
			continue
		}
		prefix := line[0]
		if prefix != '+' && prefix != '-' {
			continue
		}
		code := strings.TrimSpace(line[1:])
		if code == "" {
			continue
		}
		changed++
		if !strings.HasPrefix(code, "#include") {
			return false
		}
	}
	return changed > 0
}

func lastReviewRequiresNonIncludeCodePatch(session *Session) bool {
	if session == nil || session.LastReviewRun == nil {
		return false
	}
	findings := append([]ReviewFinding(nil), session.LastReviewRun.RepairFindings...)
	if len(findings) == 0 {
		findings = latestReviewDecisionFindings(*session.LastReviewRun)
	}
	for _, finding := range findings {
		if reviewFindingRequiresNonIncludeCodePatch(finding) {
			return true
		}
	}
	return false
}

func reviewFindingRequiresNonIncludeCodePatch(finding ReviewFinding) bool {
	text := strings.ToLower(strings.Join([]string{
		finding.Title,
		finding.Evidence,
		finding.Impact,
		finding.RequiredFix,
		finding.TestRecommendation,
		finding.Symbol,
		finding.Category,
	}, "\n"))
	if reviewFindingIsIncludeFocused(text) {
		return false
	}
	if finding.BlocksGate {
		return true
	}
	return reviewFindingTextHasAny(text, []string{
		"function", "loop", "control flow", "body", "branch", "condition", "hunk", "patch", "rewrite", "resubmit",
		"함수", "루프", "흐름", "본문", "조건", "분기", "수정", "패치", "재작성",
	})
}

func reviewFindingIsIncludeFocused(text string) bool {
	if !strings.Contains(text, "#include") {
		return false
	}
	return !reviewFindingTextHasAny(text, []string{
		"function", "loop", "control flow", "body", "branch", "condition", "rewrite",
		"함수", "루프", "흐름", "본문", "조건", "분기", "재작성",
	})
}

func reviewFindingTextHasAny(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func editToolProposalText(toolName string, arguments string) string {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(arguments)), &decoded); err != nil {
		return strings.TrimSpace(arguments)
	}
	switch strings.TrimSpace(toolName) {
	case "apply_patch":
		return strings.TrimSpace(stringValue(decoded, "patch"))
	case "replace_in_file":
		path := stringValue(decoded, "path")
		search := stringValue(decoded, "search")
		replace := stringValue(decoded, "replace")
		return strings.TrimSpace(fmt.Sprintf("path: %s\nsearch:\n%s\nreplace:\n%s", path, search, replace))
	case "write_file":
		path := stringValue(decoded, "path")
		content := stringValue(decoded, "content")
		return strings.TrimSpace(fmt.Sprintf("path: %s\ncontent:\n%s", path, content))
	default:
		return strings.TrimSpace(arguments)
	}
}

func formatPreWriteReviewRepairForceEditGuidance(cfg Config, session *Session, recent string) string {
	var b strings.Builder
	korean := localePrefersKorean(cfg)
	if session != nil && session.LastReviewRun != nil {
		korean = reviewRunPrefersKorean(cfg, *session.LastReviewRun)
	}
	if korean {
		b.WriteString("pre-write 리뷰가 이미 수정안을 차단했고, 필요한 로컬 상태 확인 예산도 충분히 사용했습니다.\n")
		b.WriteString("다음 응답은 상태 확인 도구 호출이 아니라 반드시 edit tool 호출이어야 합니다.\n")
		b.WriteString("규칙:\n")
		b.WriteString("1. 최신 pre-write blocker는 이전 proposal이 불완전했던 근거입니다. 원래 pre-fix 필수 RF 전체를 계속 수리 대상으로 유지하세요.\n")
		b.WriteString("2. 차단된 이전 patch는 적용되지 않았으므로, 누락분 delta가 아니라 현재 파일에 바로 적용 가능한 완전한 standalone patch를 작성하세요.\n")
		b.WriteString("3. 방금 확인한 현재 파일 내용에 고정된 좁은 apply_patch hunk를 작성하되, 필요한 RF hunk를 같은 proposal 안에 모두 포함하세요.\n")
		b.WriteString("4. 같은 patch, replace_in_file 추측, review artifact 재읽기, run_shell 우회 쓰기는 금지합니다.\n")
		b.WriteString("5. 필요한 문맥이 아직 부족하다고 판단되면, 먼저 한 문장으로 정확한 blocker를 말하고 수정 불가 사유를 보고하세요. 추가 탐색 루프를 시작하지 마세요.")
	} else {
		b.WriteString("The pre-write review already blocked the edit, and enough local state inspection has been spent.\n")
		b.WriteString("The next response must be an edit-tool call, not another inspection-tool call.\n")
		b.WriteString("Rules:\n")
		b.WriteString("1. Treat the latest pre-write blocker as evidence that the previous proposal was incomplete. Keep every original required pre-fix RF in force.\n")
		b.WriteString("2. The previously blocked patch was not applied, so produce a complete standalone patch for the current file instead of a missing-piece delta.\n")
		b.WriteString("3. Use narrow apply_patch hunks anchored to the current file contents just inspected, and include every required RF hunk in the same proposal.\n")
		b.WriteString("4. Do not repeat the same patch, guess with replace_in_file, reread review artifacts, or bypass review with run_shell writes.\n")
		b.WriteString("5. If the context is still insufficient, state the exact blocker in one sentence and report why editing cannot proceed. Do not start another inspection loop.")
	}
	if carried := formatLatestPreWriteCarriedRepairObligationsForUserDecision(cfg, session); carried != "" {
		if korean {
			b.WriteString("\n\n계속 유효한 전체 수리 의무:\n")
		} else {
			b.WriteString("\n\nStill-active full repair obligations:\n")
		}
		b.WriteString(carried)
	}
	recent = strings.TrimSpace(recent)
	if recent != "" {
		if korean {
			b.WriteString("\n\n최근 도구 호출:\n")
		} else {
			b.WriteString("\n\nRecent tool turns:\n")
		}
		b.WriteString(recent)
	}
	return strings.TrimSpace(b.String())
}

func formatPreWriteReviewBlockedRetryGuidance(cfg Config, session *Session) string {
	var b strings.Builder
	korean := localePrefersKorean(cfg)
	if session != nil && session.LastReviewRun != nil {
		korean = reviewRunPrefersKorean(cfg, *session.LastReviewRun)
	}
	if korean {
		b.WriteString("pre-write 리뷰가 이미 수정안을 차단했습니다.\n")
		b.WriteString("다음 턴은 차단된 proposal의 누락분만 보강하는 방식이 아니라, 현재 파일 기준의 완전한 standalone apply_patch를 다시 작성해야 합니다.\n")
		b.WriteString("규칙:\n")
		b.WriteString("1. 최신 pre-write blocker와 원래 pre-fix 필수 RF를 모두 같은 수리 의무로 유지하세요.\n")
		b.WriteString("2. replace_in_file은 이번 복구 경로에서 비활성화되었으니 사용하지 마세요.\n")
		b.WriteString("3. 차단된 proposal은 파일에 쓰이지 않았습니다. 다음 edit tool 전에 read_file, grep, git_diff 중 하나로 현재 대상 파일 또는 현재 diff를 다시 확인하세요.\n")
		b.WriteString("4. 그 다음 현재 상태에 바로 적용 가능한 완전한 standalone apply_patch를 작성하세요. review artifact 재읽기나 run_shell 우회 쓰기로 게이트를 우회하지 마세요.")
	} else {
		b.WriteString("The pre-write review already blocked the edit.\n")
		b.WriteString("On the next turn, do not submit a missing-piece delta for the blocked proposal; produce a complete standalone apply_patch anchored to the current file state.\n")
		b.WriteString("Rules:\n")
		b.WriteString("1. Keep both the latest pre-write blocker and every original required pre-fix RF as active repair obligations.\n")
		b.WriteString("2. replace_in_file is disabled for this recovery path; do not use it.\n")
		b.WriteString("3. The blocked proposal was not written. Before the next edit tool, re-read the current target file or current diff with read_file, grep, or git_diff.\n")
		b.WriteString("4. Then write a complete standalone apply_patch against that current state. Do not reread review artifacts or bypass the gate with run_shell writes.")
	}
	if carried := formatLatestPreWriteCarriedRepairObligationsForUserDecision(cfg, session); carried != "" {
		if korean {
			b.WriteString("\n\n계속 유효한 전체 수리 의무:\n")
		} else {
			b.WriteString("\n\nStill-active full repair obligations:\n")
		}
		b.WriteString(carried)
	}
	return strings.TrimSpace(b.String())
}

func formatEditTargetMismatchLoopLimitReply(cfg Config, session *Session) string {
	korean := localePrefersKorean(cfg)
	lookupMismatch := sessionLastEditTargetMismatchWasLookup(session)
	var b strings.Builder
	if korean && lookupMismatch {
		b.WriteString("읽기 전용 조회 도구에서 editable ownership mismatch가 반복되어, 더 추측하며 진행하지 않고 중단했습니다.")
		b.WriteString("\n\n- 결과: 코드 수정은 적용하지 않았습니다.")
		b.WriteString("\n- 원인: read_file/list_files/grep 같은 조회가 specialist 쓰기 소유권 라우팅에 묶였습니다. 이 문제는 stale patch 문제가 아닙니다.")
		b.WriteString("\n- 다음 조건: owner_node_id 없이 같은 로컬 조회를 다시 실행하거나, main workspace 기준 경로를 직접 조회해야 합니다.")
	} else if !korean && lookupMismatch {
		b.WriteString("Read-only inspection tools repeatedly hit editable ownership routing, so I stopped instead of guessing from partial evidence.")
		b.WriteString("\n\n- Result: no code changes were applied.")
		b.WriteString("\n- Cause: read_file/list_files/grep was constrained by specialist edit ownership. This is not a stale patch problem.")
		b.WriteString("\n- Next condition: retry the local lookup without owner_node_id, or inspect the main workspace path directly.")
	} else if korean {
		b.WriteString("파일 상태를 다시 확인한 뒤에도 edit target mismatch가 반복되어, 더 추측하며 진행하지 않고 중단했습니다.")
		b.WriteString("\n\n- 결과: 코드 수정은 적용하지 않았습니다.")
		b.WriteString("\n- 원인: 마지막 patch가 현재 파일 내용 또는 실제 workspace/root 경로에 고정되지 않았습니다.")
		b.WriteString("\n- 다음 조건: 현재 파일 또는 diff를 다시 확인해 경로와 내용을 고정한 뒤, 현재 상태에 바로 적용 가능한 완전한 standalone apply_patch를 작성해야 합니다. 여러 hunk/파일은 같은 근본 수정에 필요한 경우 허용됩니다.")
	} else {
		b.WriteString("Edit target mismatches repeated after a refresh attempt, so I stopped instead of continuing to guess at the file state.")
		b.WriteString("\n\n- Result: no code changes were applied.")
		b.WriteString("\n- Cause: the latest patch was not anchored to the current file contents or the actual workspace/root path.")
		b.WriteString("\n- Next condition: re-read the current file or diff, lock the path and contents, then produce a complete standalone apply_patch against the current state. Multiple hunks/files are allowed when they are required for the same root repair.")
	}
	if session != nil && session.LastReviewRun != nil {
		reviewText := strings.TrimSpace(formatLatestPreWriteReviewForUserDecision(cfg, session))
		if reviewText == "" {
			reviewText = strings.TrimSpace(formatPreFixVisibleReviewSummary(cfg, *session.LastReviewRun))
		}
		if reviewText := compactPromptSection(reviewText, 1800); strings.TrimSpace(reviewText) != "" {
			if korean {
				b.WriteString("\n\n[1] 최신 리뷰 기준\n")
			} else {
				b.WriteString("\n\n[1] Latest review basis\n")
			}
			b.WriteString(reviewText)
		}
	}
	if proposalText := formatLatestEditProposalForUserDecision(cfg, session); strings.TrimSpace(proposalText) != "" {
		if korean {
			b.WriteString("\n\n[2] 마지막 수정안\n")
		} else {
			b.WriteString("\n\n[2] Latest edit proposal\n")
		}
		b.WriteString(proposalText)
	}
	return strings.TrimSpace(b.String())
}

func sessionLastEditTargetMismatchWasLookup(session *Session) bool {
	if session == nil {
		return false
	}
	for i := len(session.Messages) - 1; i >= 0; i-- {
		msg := session.Messages[i]
		if msg.Role != "tool" || !msg.IsError {
			continue
		}
		text := strings.ToLower(msg.Text)
		if !strings.Contains(text, "outside editable ownership") && !strings.Contains(text, "edit target mismatch") {
			continue
		}
		return readOnlyInspectionToolName(msg.ToolName)
	}
	return false
}

func formatEditTargetMismatchReanchorLoopLimitReply(cfg Config, session *Session) string {
	korean := localePrefersKorean(cfg)
	if session != nil && session.LastReviewRun != nil {
		korean = reviewRunPrefersKorean(cfg, *session.LastReviewRun)
	}
	var b strings.Builder
	if korean {
		b.WriteString("edit target mismatch 이후 현재 파일/경로를 다시 고정하지 않은 edit 재시도가 반복되어 중단했습니다.")
		b.WriteString("\n\n- 결과: 코드 수정은 적용하지 않았습니다.")
		b.WriteString("\n- 원인: 이전 patch는 stale/mismatched 상태였고, 그 뒤 read_file, grep, list_files, git_status, git_diff 같은 재확인 없이 또 edit tool이 호출되었습니다.")
		b.WriteString("\n- 다음 조건: 현재 파일 또는 diff를 먼저 다시 확인해 경로와 내용을 고정한 뒤, 근본 수정에 필요한 완전한 standalone apply_patch를 제출해야 합니다. 여러 hunk/파일은 같은 근본 수정에 필요한 경우 허용됩니다.")
	} else {
		b.WriteString("Edit retries continued after an edit target mismatch without re-anchoring the current file/path state, so I stopped instead of guessing.")
		b.WriteString("\n\n- Result: no code changes were applied.")
		b.WriteString("\n- Cause: the previous patch was stale or mismatched, and another edit tool was issued before read_file, grep, list_files, git_status, or git_diff re-anchored the current workspace state.")
		b.WriteString("\n- Next condition: re-read the current file or diff, lock the path and contents, then submit a complete standalone apply_patch for the root repair. Multiple hunks/files are allowed when required for the same repair.")
	}
	if session != nil && session.LastReviewRun != nil {
		reviewText := strings.TrimSpace(formatLatestPreWriteReviewForUserDecision(cfg, session))
		if reviewText == "" {
			reviewText = strings.TrimSpace(formatPreFixVisibleReviewSummary(cfg, *session.LastReviewRun))
		}
		if reviewText := compactPromptSection(reviewText, 1800); strings.TrimSpace(reviewText) != "" {
			if korean {
				b.WriteString("\n\n[1] 최신 리뷰 기준\n")
			} else {
				b.WriteString("\n\n[1] Latest review basis\n")
			}
			b.WriteString(reviewText)
		}
	}
	if proposalText := formatLatestEditProposalForUserDecision(cfg, session); strings.TrimSpace(proposalText) != "" {
		if korean {
			b.WriteString("\n\n[2] 마지막 수정안\n")
		} else {
			b.WriteString("\n\n[2] Latest edit proposal\n")
		}
		b.WriteString(proposalText)
	}
	return strings.TrimSpace(b.String())
}

func applyPatchFormatFailureSignature(arguments string) string {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(arguments)), &decoded); err != nil {
		return computeReviewFingerprint("apply_patch", normalizeToolArguments(arguments))
	}
	patch := stringValue(decoded, "patch")
	if strings.TrimSpace(patch) == "" {
		return computeReviewFingerprint("apply_patch", normalizeToolArguments(arguments))
	}
	return computeReviewFingerprint("apply_patch", normalizePatchDocumentText(patch))
}

func repeatedToolCallRecoveryGuidance(summary string, recent string) string {
	parts := []string{
		"Recovery mode: the tool loop is still stuck on the same tool call sequence. Do not repeat that sequence again immediately.",
		"Next step requirements:\n1. State the blocker in one sentence.\n2. Choose one materially different next step: inspect a different file or tool, change the tool arguments, or provide the best final answer now.\n3. Only retry the same tool sequence if you can explain exactly what changed.",
	}
	if strings.TrimSpace(summary) != "" {
		parts = append(parts, "Loop signature: "+renderLoopSignature(LoopSignature{
			Kind:          "repeated_tool_calls",
			Signature:     computeReviewFingerprint("tool_calls", summary),
			RepeatCount:   repeatedToolCallRecoveryThreshold,
			RequiredShift: "change tool, arguments, target path, or stop and summarize the blocker",
		}))
		parts = append(parts, "Repeated tool sequence:\n"+summary)
	}
	if strings.TrimSpace(recent) != "" {
		parts = append(parts, "Recent tool turns:\n"+recent)
	}
	return strings.Join(parts, "\n\n")
}

func repeatedReadFilePathRecoveryGuidance(path string, turns int, recent string) string {
	parts := []string{
		fmt.Sprintf("Recovery mode: you have read the same file path across %d tool turns: %s. Treat the existing reads as sufficient unless the file changed.", turns, path),
		"Do not read the same path again immediately. Either inspect a different file or tool, explain the current findings, or provide the best final answer now. Only reread this path if you can name the exact missing section that is still required.",
	}
	if signature := renderLoopSignature(loopSignatureForRepeatedRead(path, turns)); signature != "" {
		parts = append(parts, "Loop signature: "+signature)
	}
	if strings.TrimSpace(recent) != "" {
		parts = append(parts, "Recent tool turns:\n"+recent)
	}
	return strings.Join(parts, "\n\n")
}

func repeatedToolFailureRecoveryGuidance(toolErr string, recent string) string {
	parts := []string{
		"Recovery mode: the same tool failure has happened multiple times. Do not repeat the same failing tool call again with near-identical inputs.",
		"Next step requirements:\n1. State the blocker in one sentence.\n2. Choose a materially different action: use another tool, change the target/path/arguments, or provide the best partial final answer with the blocker.\n3. Only retry the failing tool if you can explain what changed.",
	}
	if signature := renderLoopSignature(loopSignatureForToolFailure(toolErr, repeatedToolFailureRecoveryThreshold)); signature != "" {
		parts = append(parts, "Loop signature: "+signature)
	}
	if strings.TrimSpace(toolErr) != "" {
		parts = append(parts, "Latest tool failure:\n"+sanitizeDiagnosticValue(toolErr))
	}
	if strings.TrimSpace(recent) != "" {
		parts = append(parts, "Recent tool turns:\n"+recent)
	}
	return strings.Join(parts, "\n\n")
}

func toolBudgetExtensionGuidance(extraTurns int, recent string) string {
	parts := []string{
		fmt.Sprintf("Recent tool turns show real progress. The tool budget is extended by %d more turn(s).", extraTurns),
		"Use the extra turns to finish the investigation or fix. Do not spend them repeating the same tool sequence again.",
	}
	if strings.TrimSpace(recent) != "" {
		parts = append(parts, "Recent tool turns:\n"+recent)
	}
	return strings.Join(parts, "\n\n")
}

func toolLoopLimitRecoveryGuidance(summary string, stopReason string, recent string) string {
	parts := []string{
		"The normal tool budget has been exhausted. Do not continue the same tool loop blindly.",
		"Next step requirements:\n1. State the blocker or current finding in one sentence.\n2. Prefer a final or partial answer from the evidence already gathered.\n3. Only make one more tool call if it is materially different and you can explain why it is still necessary.",
	}
	if strings.TrimSpace(summary) != "" {
		parts = append(parts, "Last tool sequence:\n"+summary)
	}
	if strings.TrimSpace(stopReason) != "" {
		parts = append(parts, "Last stop reason:\n"+sanitizeDiagnosticValue(stopReason))
	}
	if strings.TrimSpace(recent) != "" {
		parts = append(parts, "Recent tool turns:\n"+recent)
	}
	return strings.Join(parts, "\n\n")
}

func shouldExtendToolBudget(messages []Message, lastToolErrorCount, lastToolCallSignatureCount, lastReadFilePathTurns, extensionCount int) bool {
	if extensionCount >= maxToolBudgetExtensions {
		return false
	}
	if lastToolErrorCount > 0 {
		return false
	}
	if lastToolCallSignatureCount >= repeatedToolCallNudgeThreshold {
		return false
	}
	if lastReadFilePathTurns >= repeatedReadFilePathNudgeTurns {
		return false
	}
	recentTurns := collectRecentToolTurnSummaries(messages, 4)
	if len(recentTurns) < 2 {
		return false
	}
	if countDistinctStrings(recentTurns) < 2 {
		return false
	}
	return countDistinctRecentToolNames(messages, 4) >= 2
}

func nextToolBudgetExtension(baseBudget, extensionCount int) int {
	if extensionCount >= maxToolBudgetExtensions {
		return 0
	}
	extra := 2
	if baseBudget >= 6 {
		extra = 3
	}
	if baseBudget >= 10 {
		extra = 4
	}
	return extra
}

func toolShouldBeDisabledAfterInvalidJSON(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "write_file", "replace_in_file":
		return true
	default:
		return false
	}
}

func summarizeToolCalls(calls []ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			name = "unknown"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func collectRecentToolTurnSummaries(messages []Message, limit int) []string {
	if limit <= 0 || len(messages) == 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		msg := messages[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		summary := strings.TrimSpace(summarizeToolTurn(messages, i))
		if summary == "" {
			continue
		}
		out = append(out, summary)
	}
	return out
}

func countDistinctStrings(items []string) int {
	if len(items) == 0 {
		return 0
	}
	seen := map[string]struct{}{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		seen[trimmed] = struct{}{}
	}
	return len(seen)
}

func countDistinctRecentToolNames(messages []Message, limit int) int {
	if limit <= 0 || len(messages) == 0 {
		return 0
	}
	seen := map[string]struct{}{}
	countedTurns := 0
	for i := len(messages) - 1; i >= 0 && countedTurns < limit; i-- {
		msg := messages[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		countedTurns++
		for _, call := range msg.ToolCalls {
			name := strings.TrimSpace(call.Name)
			if name == "" {
				continue
			}
			seen[name] = struct{}{}
		}
	}
	return len(seen)
}

func (a *Agent) beginToolExecution(call ToolCall) (int, error) {
	a.Session.AddMessage(Message{
		Role:       "tool",
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Text:       "IN_PROGRESS: " + summarizeToolDiagnosticCall(call),
	})
	if err := a.Store.Save(a.Session); err != nil {
		return -1, err
	}
	return len(a.Session.Messages) - 1, nil
}

func (a *Agent) beginToolExecutions(calls []ToolCall) ([]int, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	indexes := make([]int, 0, len(calls))
	for _, call := range calls {
		a.Session.AddMessage(Message{
			Role:       "tool",
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Text:       "IN_PROGRESS: " + summarizeToolDiagnosticCall(call),
		})
		indexes = append(indexes, len(a.Session.Messages)-1)
	}
	if err := a.Store.Save(a.Session); err != nil {
		return nil, err
	}
	return indexes, nil
}

func (a *Agent) setToolExecutionResult(index int, msg Message) {
	if index >= 0 && index < len(a.Session.Messages) {
		a.Session.Messages[index] = msg
		a.Session.UpdatedAt = time.Now()
		return
	}
	a.Session.AddMessage(msg)
}

func (a *Agent) setRemainingToolCallsNotExecuted(calls []ToolCall, indexes []int, start int, reason string) {
	if a == nil || a.Session == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "NOT_EXECUTED: a previous tool call in this model response required a retry from the next model turn."
	}
	for i := start; i < len(calls); i++ {
		call := calls[i]
		index := -1
		if i >= 0 && i < len(indexes) {
			index = indexes[i]
		}
		result := notExecutedToolResult(call, reason)
		a.setToolExecutionResult(index, Message{
			Role:       "tool",
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Text:       result.DisplayText,
			IsError:    true,
			ToolMeta:   result.Meta,
		})
		a.noteToolConversationBlockedResult(call, result, nil)
	}
}

func (a *Agent) addToolCallRedirectGuidance(calls []ToolCall, reason string, guidance string) error {
	if a == nil || a.Session == nil {
		return nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "NOT_EXECUTED: this tool-call batch was redirected by the runtime before execution."
	}
	for _, call := range calls {
		result := notExecutedToolResult(call, reason)
		a.Session.AddMessage(Message{
			Role:       "tool",
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Text:       result.DisplayText,
			IsError:    true,
			ToolMeta:   result.Meta,
		})
		a.noteToolConversationBlockedResult(call, result, nil)
	}
	if strings.TrimSpace(guidance) != "" {
		a.Session.AddMessage(internalUserMessage(guidance))
	}
	if a.Store != nil {
		return a.Store.Save(a.Session)
	}
	return nil
}

func notExecutedToolResult(call ToolCall, reason string) ToolExecutionResult {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "NOT_EXECUTED: this tool call was redirected by the runtime before execution."
	}
	meta := defaultToolExecutionMeta(call.Name, toolCallArgumentsMap(call))
	meta["status"] = "not_executed"
	meta["reason"] = reason
	meta["success"] = false
	meta["changed_workspace"] = false
	meta["deferred"] = true
	meta["requires_reissue"] = true
	if toolCallIsExecCommandLike(call.Name) {
		meta["command_execution_status"] = "declined"
	}
	if toolCallIsPatchApplyLike(call.Name) {
		meta["patch_apply_status"] = "declined"
	}
	if toolCallIsMCPToolLike(call.Name) {
		meta["mcp_is_error"] = true
	}
	return ToolExecutionResult{
		DisplayText: reason,
		Meta:        meta,
	}
}

func summarizeToolInvocation(cfg Config, call ToolCall) string {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return ""
	}

	args := map[string]any{}
	if strings.TrimSpace(call.Arguments) != "" {
		_ = json.Unmarshal([]byte(call.Arguments), &args)
	}

	switch name {
	case "read_file":
		path := strings.TrimSpace(stringValue(args, "path"))
		if path == "" {
			return localizedText(cfg, "Using read_file...", "read_file 확인 중 ...")
		}
		start := intValue(args, "start_line", 0)
		end := intValue(args, "end_line", 0)
		if start > 0 && end >= start {
			return fmt.Sprintf(localizedText(cfg, "Using read_file on %s:%d-%d...", "read_file 확인 중 ... %s:%d-%d"), path, start, end)
		}
		return fmt.Sprintf(localizedText(cfg, "Using read_file on %s...", "read_file 확인 중 ... %s"), path)
	case "grep":
		pattern := strings.TrimSpace(stringValue(args, "pattern"))
		if len(pattern) > 48 {
			pattern = pattern[:45] + "..."
		}
		if pattern == "" {
			return localizedText(cfg, "Using grep...", "grep 검색 중 ...")
		}
		return fmt.Sprintf(localizedText(cfg, "Using grep for %q...", "grep 검색 중 ... %q"), pattern)
	case "list_files":
		path := strings.TrimSpace(stringValue(args, "path"))
		if path == "" {
			return localizedText(cfg, "Using list_files...", "list_files 확인 중 ...")
		}
		return fmt.Sprintf(localizedText(cfg, "Using list_files in %s...", "list_files 확인 중 ... %s"), path)
	case "run_shell":
		command := strings.TrimSpace(stringValue(args, "command"))
		if len(command) > 72 {
			command = command[:69] + "..."
		}
		if shellToolArgsLookLikeVerification(args) {
			if command == "" {
				return localizedText(cfg, "Requesting verification command approval...", "검증 명령 승인 확인 중 ...")
			}
			return fmt.Sprintf(localizedText(cfg, "Requesting verification command approval: %s", "검증 명령 승인 확인 중 ... %s"), command)
		}
		if command == "" {
			return localizedText(cfg, "Using run_shell...", "shell 실행 중 ...")
		}
		return fmt.Sprintf(localizedText(cfg, "Running shell: %s", "shell 실행 중 ... %s"), command)
	case "run_shell_background":
		command := strings.TrimSpace(stringValue(args, "command"))
		if len(command) > 72 {
			command = command[:69] + "..."
		}
		if shellToolArgsLookLikeVerification(args) {
			if command == "" {
				return localizedText(cfg, "Requesting background verification approval...", "백그라운드 검증 승인 확인 중 ...")
			}
			return fmt.Sprintf(localizedText(cfg, "Requesting background verification approval: %s", "백그라운드 검증 승인 확인 중 ... %s"), command)
		}
		if command == "" {
			return localizedText(cfg, "Starting background shell...", "백그라운드 shell 시작 중 ...")
		}
		return fmt.Sprintf(localizedText(cfg, "Starting background shell: %s", "백그라운드 shell 시작 중 ... %s"), command)
	case "run_shell_bundle_background":
		commands := stringSliceValue(args, "commands")
		if shellBundleArgsLookLikeVerification(args) {
			if len(commands) == 0 {
				return localizedText(cfg, "Requesting background verification bundle approval...", "백그라운드 검증 묶음 승인 확인 중 ...")
			}
			return fmt.Sprintf(localizedText(cfg, "Requesting background verification bundle approval for %d command(s)...", "백그라운드 검증 묶음 %d개 승인 확인 중 ..."), len(commands))
		}
		if len(commands) == 0 {
			return localizedText(cfg, "Starting background shell bundle...", "백그라운드 shell 묶음 시작 중 ...")
		}
		return fmt.Sprintf(localizedText(cfg, "Starting %d background shell command(s)...", "백그라운드 shell %d개 시작 중 ..."), len(commands))
	case "check_shell_job":
		jobID := strings.TrimSpace(stringValue(args, "job_id"))
		if jobID == "" {
			jobID = "latest"
		}
		return fmt.Sprintf(localizedText(cfg, "Checking shell job %s...", "shell job 확인 중 ... %s"), jobID)
	case "check_shell_bundle":
		jobIDs := stringSliceValue(args, "job_ids")
		if len(jobIDs) == 0 {
			return localizedText(cfg, "Checking shell bundle...", "shell 묶음 확인 중 ...")
		}
		return fmt.Sprintf(localizedText(cfg, "Checking %d shell job(s)...", "shell job %d개 확인 중 ..."), len(jobIDs))
	case "cancel_shell_job":
		jobID := strings.TrimSpace(stringValue(args, "job_id"))
		if jobID == "" {
			jobID = "latest"
		}
		return fmt.Sprintf(localizedText(cfg, "Canceling shell job %s...", "shell job 중단 중 ... %s"), jobID)
	case "cancel_shell_bundle":
		bundleID := strings.TrimSpace(stringValue(args, "bundle_id"))
		if bundleID == "" {
			bundleID = "latest"
		}
		return fmt.Sprintf(localizedText(cfg, "Canceling shell bundle %s...", "shell 묶음 중단 중 ... %s"), bundleID)
	case "git_status", "git_diff", "git_add", "git_commit", "git_push", "git_create_pr":
		return fmt.Sprintf(localizedText(cfg, "Using %s...", "%s 실행 중 ..."), name)
	case "apply_patch", "write_file", "replace_in_file":
		return ""
	default:
		if toolCallNameLooksLikeWebResearch(name) {
			intent := webResearchCallIntent(call)
			if intent != "" {
				return fmt.Sprintf(localizedText(cfg, "Web research requested: %s", "웹 리서치 요청: %s"), intent)
			}
			return localizedText(cfg, "Web research requested.", "웹 리서치 요청.")
		}
		return fmt.Sprintf(localizedText(cfg, "Using %s...", "%s 실행 중 ..."), name)
	}
}

func shellToolArgsLookLikeVerification(args map[string]any) bool {
	if boolValue(args, "verification_like", false) {
		return true
	}
	command := strings.TrimSpace(stringValue(args, "command"))
	if command == "" {
		return false
	}
	return assessShellCommandMutation(command).Class == shellMutationVerificationArtifacts
}

func shellBundleArgsLookLikeVerification(args map[string]any) bool {
	if boolValue(args, "verification_like", false) {
		return true
	}
	for _, command := range stringSliceValue(args, "commands") {
		if assessShellCommandMutation(command).Class == shellMutationVerificationArtifacts {
			return true
		}
	}
	return false
}

func summarizeToolCompletion(cfg Config, call ToolCall, out string) string {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return ""
	}

	args := map[string]any{}
	if strings.TrimSpace(call.Arguments) != "" {
		_ = json.Unmarshal([]byte(call.Arguments), &args)
	}

	switch name {
	case "read_file":
		path := strings.TrimSpace(stringValue(args, "path"))
		lineCount := countNonEmptyLines(out)
		if path == "" {
			if lineCount > 0 {
				return fmt.Sprintf(localizedText(cfg, "read_file loaded %d line(s).", "read_file 완료 (%d줄)."), lineCount)
			}
			return localizedText(cfg, "read_file completed.", "read_file 완료.")
		}
		if lineCount > 0 {
			return fmt.Sprintf(localizedText(cfg, "read_file loaded %s (%d line(s)).", "read_file 완료 %s (%d줄)."), path, lineCount)
		}
		return fmt.Sprintf(localizedText(cfg, "read_file loaded %s.", "read_file 완료 %s."), path)
	case "grep":
		pattern := truncateStatusSnippet(strings.TrimSpace(stringValue(args, "pattern")), 48)
		matchCount := countNonEmptyLines(out)
		if pattern == "" {
			return fmt.Sprintf(localizedText(cfg, "grep returned %d line(s).", "grep 완료 (%d줄)."), matchCount)
		}
		return fmt.Sprintf(localizedText(cfg, "grep returned %[1]d line(s) for %[2]q.", "grep 완료 %[2]q (%[1]d줄)."), matchCount, pattern)
	case "list_files":
		path := strings.TrimSpace(stringValue(args, "path"))
		if path == "" {
			path = "."
		}
		itemCount := countNonEmptyLines(out)
		return fmt.Sprintf(localizedText(cfg, "list_files returned %[1]d item(s) from %[2]s.", "list_files 완료 %[2]s (%[1]d개)."), itemCount, path)
	case "run_shell":
		snippet := truncateStatusSnippet(firstNonEmptyLine(out), 80)
		if runShellOutputLooksLikeSkippedVerification(out) {
			if snippet == "" {
				return localizedText(cfg, "Verification command skipped.", "검증 명령 생략됨.")
			}
			return fmt.Sprintf(localizedText(cfg, "Verification command skipped: %s", "검증 명령 생략됨: %s"), snippet)
		}
		if snippet == "" {
			return localizedText(cfg, "run_shell completed with no output.", "shell 완료: 출력 없음.")
		}
		return fmt.Sprintf(localizedText(cfg, "run_shell completed: %s", "shell 완료: %s"), snippet)
	case "run_shell_background":
		snippet := truncateStatusSnippet(firstNonEmptyLine(out), 80)
		if runShellOutputLooksLikeSkippedVerification(out) || strings.Contains(strings.ToLower(out), "no background jobs started") {
			if snippet == "" {
				return localizedText(cfg, "Background verification skipped.", "백그라운드 검증 생략됨.")
			}
			return fmt.Sprintf(localizedText(cfg, "Background verification skipped: %s", "백그라운드 검증 생략됨: %s"), snippet)
		}
		if snippet == "" {
			return localizedText(cfg, "Background shell job started.", "백그라운드 shell 시작됨.")
		}
		return fmt.Sprintf(localizedText(cfg, "Background shell started: %s", "백그라운드 shell 시작: %s"), snippet)
	case "run_shell_bundle_background":
		snippet := truncateStatusSnippet(firstNonEmptyLine(out), 80)
		if runShellOutputLooksLikeSkippedVerification(out) || strings.Contains(strings.ToLower(out), "no background jobs started") {
			if snippet == "" {
				return localizedText(cfg, "Background verification bundle skipped.", "백그라운드 검증 묶음 생략됨.")
			}
			return fmt.Sprintf(localizedText(cfg, "Background verification bundle skipped: %s", "백그라운드 검증 묶음 생략됨: %s"), snippet)
		}
		if snippet == "" {
			return localizedText(cfg, "Background shell bundle started.", "백그라운드 shell 묶음 시작됨.")
		}
		return fmt.Sprintf(localizedText(cfg, "Background shell bundle started: %s", "백그라운드 shell 묶음 시작: %s"), snippet)
	case "check_shell_job":
		snippet := truncateStatusSnippet(firstNonEmptyLine(out), 80)
		if snippet == "" {
			return localizedText(cfg, "Shell job status loaded.", "shell job 상태 확인 완료.")
		}
		return fmt.Sprintf(localizedText(cfg, "Shell job status: %s", "shell job 상태: %s"), snippet)
	case "check_shell_bundle":
		snippet := truncateStatusSnippet(firstNonEmptyLine(out), 80)
		if snippet == "" {
			return localizedText(cfg, "Shell bundle status loaded.", "shell 묶음 상태 확인 완료.")
		}
		return fmt.Sprintf(localizedText(cfg, "Shell bundle status: %s", "shell 묶음 상태: %s"), snippet)
	case "cancel_shell_job":
		return localizedText(cfg, "Background shell job canceled.", "백그라운드 shell job 중단 완료.")
	case "cancel_shell_bundle":
		return localizedText(cfg, "Background shell bundle canceled.", "백그라운드 shell 묶음 중단 완료.")
	case "git_status", "git_diff", "git_add", "git_commit", "git_push", "git_create_pr":
		return fmt.Sprintf(localizedText(cfg, "%s completed.", "%s 완료."), name)
	case "apply_patch", "write_file", "replace_in_file":
		return ""
	default:
		return fmt.Sprintf(localizedText(cfg, "%s completed.", "%s 완료."), name)
	}
}

func summarizeToolFailure(cfg Config, call ToolCall, err error) string {
	name := strings.TrimSpace(call.Name)
	if name == "" || err == nil {
		return ""
	}
	reason := truncateStatusSnippet(firstNonEmptyLine(err.Error()), 96)
	if reason == "" {
		reason = localizedText(cfg, "unknown error", "알 수 없는 오류")
	}
	return fmt.Sprintf(localizedText(cfg, "%s failed: %s", "%s 실패: %s"), name, reason)
}

func countNonEmptyLines(text string) int {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncateStatusSnippet(text string, limit int) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	return truncateDisplayText(trimmed, limit)
}

func normalizeStopReason(reason string) string {
	return strings.ToLower(strings.TrimSpace(reason))
}

func chatResponseRequestsFollowUp(resp ChatResponse) bool {
	return resp.EndTurn != nil && !*resp.EndTurn
}

func formatEmptyModelResponseError(session *Session, stopReason string, afterTool bool) error {
	parts := make([]string, 0, 4)
	if session != nil {
		if provider := strings.TrimSpace(session.Provider); provider != "" {
			parts = append(parts, "provider="+provider)
		}
		if model := strings.TrimSpace(session.Model); model != "" {
			parts = append(parts, "model="+model)
		}
	}
	if normalized := normalizeStopReason(stopReason); normalized != "" {
		parts = append(parts, "stop_reason="+normalized)
	}
	parts = append(parts, fmt.Sprintf("after_tool=%t", afterTool))
	if len(parts) == 0 {
		return fmt.Errorf("model returned an empty response")
	}
	return fmt.Errorf("model returned an empty response (%s)", strings.Join(parts, " "))
}

func isTokenLimitStopReason(reason string) bool {
	switch normalizeStopReason(reason) {
	case "length", "max_tokens":
		return true
	default:
		return false
	}
}

func formatToolLoopDiagnostic(toolSummary, stopReason string, iteration, maxIterations int, recentToolTurns string) string {
	parts := make([]string, 0, 5)
	if strings.TrimSpace(toolSummary) != "" {
		parts = append(parts, "last_tools="+toolSummary)
	}
	if strings.TrimSpace(stopReason) != "" {
		parts = append(parts, "stop_reason="+stopReason)
	}
	if iteration > 0 {
		parts = append(parts, "iteration="+strconv.Itoa(iteration))
	}
	if maxIterations > 0 {
		parts = append(parts, "max_iterations="+strconv.Itoa(maxIterations))
	}
	if strings.TrimSpace(recentToolTurns) != "" {
		parts = append(parts, "recent_turns="+recentToolTurns)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

func summarizeRecentToolTurns(messages []Message, limit int) string {
	if limit <= 0 || len(messages) == 0 {
		return ""
	}
	turns := make([]string, 0, limit)
	for i := len(messages) - 1; i >= 0 && len(turns) < limit; i-- {
		msg := messages[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		turns = append(turns, summarizeToolTurn(messages, i))
	}
	if len(turns) == 0 {
		return ""
	}
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return strings.Join(turns, " | ")
}

func summarizeToolTurn(messages []Message, assistantIndex int) string {
	msg := messages[assistantIndex]
	parts := make([]string, 0, len(msg.ToolCalls))
	toolResults := collectToolResults(messages, assistantIndex, len(msg.ToolCalls))
	for i, call := range msg.ToolCalls {
		name := summarizeToolDiagnosticCall(call)
		status := "pending"
		if i < len(toolResults) && toolResults[i] != "" {
			status = toolResults[i]
		}
		parts = append(parts, sanitizeDiagnosticValue(name+":"+status))
	}
	return strings.Join(parts, ", ")
}

func collectToolResults(messages []Message, assistantIndex, expected int) []string {
	results := make([]string, 0, expected)
	for i := assistantIndex + 1; i < len(messages) && len(results) < expected; i++ {
		msg := messages[i]
		if msg.Role == "tool" {
			results = append(results, summarizeToolResultStatus(msg))
			continue
		}
		break
	}
	return results
}

func summarizeToolResultStatus(msg Message) string {
	text := strings.TrimSpace(msg.Text)
	switch {
	case strings.HasPrefix(text, "IN_PROGRESS:"):
		return "running"
	case strings.HasPrefix(text, "CANCELED:"):
		return "canceled"
	case msg.IsError:
		return "error"
	case isCachedReadFileToolResult(msg):
		return "cached"
	case text == "":
		return "empty"
	default:
		return "ok"
	}
}

func isCachedReadFileToolResult(msg Message) bool {
	if strings.TrimSpace(msg.ToolName) != "read_file" {
		return false
	}
	text := strings.TrimSpace(msg.Text)
	return strings.HasPrefix(text, "NOTE: returning cached content") ||
		strings.HasPrefix(text, "NOTE: returning content from a cached overlapping read_file range.") ||
		strings.HasPrefix(text, "NOTE: returning content assembled from a cached partial overlap plus newly read lines.")
}

func lastAssistantToolTurnWasCachedReadFile(messages []Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		results := collectToolResults(messages, i, len(msg.ToolCalls))
		if len(results) == 0 {
			return false
		}
		hasReadFile := false
		for idx, call := range msg.ToolCalls {
			if strings.TrimSpace(call.Name) != "read_file" {
				return false
			}
			hasReadFile = true
			if idx >= len(results) || results[idx] != "cached" {
				return false
			}
		}
		return hasReadFile
	}
	return false
}

func summarizeToolDiagnosticCall(call ToolCall) string {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return "unknown"
	}

	args := map[string]any{}
	if strings.TrimSpace(call.Arguments) != "" {
		_ = json.Unmarshal([]byte(call.Arguments), &args)
	}

	switch name {
	case "read_file":
		path := strings.TrimSpace(stringValue(args, "path"))
		if path == "" {
			return name
		}
		start := intValue(args, "start_line", 0)
		end := intValue(args, "end_line", 0)
		if start > 0 && end >= start {
			return fmt.Sprintf("%s[%s:%d-%d]", name, path, start, end)
		}
		return fmt.Sprintf("%s[%s]", name, path)
	case "grep":
		pattern := strings.TrimSpace(stringValue(args, "pattern"))
		if pattern == "" {
			return name
		}
		if len(pattern) > 32 {
			pattern = pattern[:29] + "..."
		}
		return fmt.Sprintf("%s[%s]", name, pattern)
	case "list_files":
		path := strings.TrimSpace(stringValue(args, "path"))
		if path == "" {
			return name
		}
		return fmt.Sprintf("%s[%s]", name, path)
	default:
		return name
	}
}

func sanitizeDiagnosticValue(value string) string {
	replacer := strings.NewReplacer(";", ",", "(", "[", ")", "]", "\n", " ", "\r", " ")
	return strings.TrimSpace(replacer.Replace(value))
}

func allToolCallsAreEditTools(calls []ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		if !isEditTool(call.Name) {
			return false
		}
	}
	return true
}

func shouldDeferEditToolsInToolCallBatch(calls []ToolCall) bool {
	return len(calls) > 1 && toolCallsIncludeEditTool(calls)
}

func shouldDeferToolCallInMixedEditBatch(call ToolCall) bool {
	if isEditTool(call.Name) {
		return true
	}
	return !toolCallIsReadOnlyDuringMixedEditBatch(call)
}

func toolCallIsReadOnlyDuringMixedEditBatch(call ToolCall) bool {
	switch inferToolExecutionEffect(call.Name) {
	case "inspect", "plan":
		return true
	}
	switch strings.TrimSpace(call.Name) {
	case "check_shell_job", "check_shell_bundle":
		return true
	case "run_shell":
		assessment := assessShellCommandMutation(toolCallCommandArgument(call))
		return assessment.Class == shellMutationReadOnly
	default:
		return false
	}
}

func deferredMixedToolResult(call ToolCall) ToolExecutionResult {
	args := toolCallArgumentsMap(call)
	meta := defaultToolExecutionMeta(call.Name, args)
	meta["success"] = false
	meta["deferred"] = true
	meta["requires_reissue"] = true
	meta["changed_workspace"] = false
	if isEditTool(call.Name) {
		meta["reason"] = "edit_tool_must_be_single_tool_call"
	} else {
		meta["reason"] = "non_read_only_tool_deferred_until_edit_is_isolated"
	}
	return ToolExecutionResult{
		DisplayText: mixedToolDeferralResultText(call.Name),
		Meta:        meta,
	}
}

func preWriteReviewReanchorTool(call ToolCall) bool {
	switch strings.TrimSpace(call.Name) {
	case "read_file", "grep", "git_diff":
		return true
	default:
		return false
	}
}

func preWriteReviewReanchorRequiredResult(call ToolCall) ToolExecutionResult {
	args := toolCallArgumentsMap(call)
	meta := defaultToolExecutionMeta(call.Name, args)
	meta["success"] = false
	meta["deferred"] = true
	meta["requires_reissue"] = true
	meta["changed_workspace"] = false
	meta["reason"] = "pre_write_blocked_proposal_requires_current_file_reanchor"
	return ToolExecutionResult{
		DisplayText: "NOT_EXECUTED: the previous edit proposal was blocked before writing, so it is not the current file state. Re-read the current target file or current diff, then issue one standalone edit anchored to that fresh evidence. Do not submit a delta against the rejected proposal.",
		Meta:        meta,
	}
}

func preWriteReviewReanchorRequiredGuidance(cfg Config) string {
	return localizedText(cfg,
		"The previous edit proposal was blocked by pre-write review and was not written. Before another edit tool can run, re-anchor on committed workspace state with read_file, grep, or git_diff for the current target. Then submit one complete standalone patch against that current state. Do not treat the rejected proposal as applied, and do not submit a missing-piece delta.",
		"이전 수정안은 pre-write review에서 차단되어 파일에 쓰이지 않았습니다. 다음 edit tool을 실행하기 전에는 read_file, grep, git_diff 중 하나로 현재 대상 파일이나 현재 diff를 다시 확인해야 합니다. 그 뒤 현재 상태에 바로 적용 가능한 완전한 standalone patch 하나를 제출하세요. 거절된 proposal이 적용된 것처럼 다루거나 누락분 delta만 제출하지 마세요.",
	)
}

func editTargetMismatchReanchorTool(call ToolCall) bool {
	switch strings.TrimSpace(call.Name) {
	case "read_file", "grep", "list_files", "git_status", "git_diff":
		return true
	default:
		return false
	}
}

func editTargetMismatchReanchorRequiredResult(call ToolCall) ToolExecutionResult {
	args := toolCallArgumentsMap(call)
	meta := defaultToolExecutionMeta(call.Name, args)
	meta["success"] = false
	meta["deferred"] = true
	meta["requires_reissue"] = true
	meta["changed_workspace"] = false
	meta["reason"] = "edit_target_mismatch_requires_current_context_reanchor"
	return ToolExecutionResult{
		DisplayText: "NOT_EXECUTED: the previous edit targeted stale or mismatched file contents. Re-anchor with read_file, grep, list_files, git_status, or git_diff before another edit. After re-anchoring, submit a complete standalone patch for the root repair; do not assume the skipped edit was applied.",
		Meta:        meta,
	}
}

func editTargetMismatchReanchorRequiredGuidance(cfg Config) string {
	return localizedText(cfg,
		"The previous edit targeted stale or mismatched file contents and was not written. Before another edit tool can run, re-anchor on the current workspace state with read_file, grep, list_files, git_status, or git_diff. After that, submit a complete standalone apply_patch for the root repair. Multiple related hunks or files are allowed when they are needed for one coherent fix; do not split solely because the previous edit mismatched.",
		"이전 수정은 현재 파일 내용 또는 실제 workspace 경로와 맞지 않아 쓰이지 않았습니다. 다음 edit tool을 실행하기 전에는 read_file, grep, list_files, git_status, git_diff 중 하나로 현재 workspace 상태를 다시 고정해야 합니다. 그 뒤 근본 수리를 위한 완전한 standalone apply_patch를 제출하세요. 같은 수정에 필요한 여러 hunk나 파일은 허용되며, 이전 mismatch 때문에 억지로 잘게 쪼개지 마세요.",
	)
}

func mixedToolDeferralResultText(name string) string {
	if isEditTool(name) {
		return "NOT_EXECUTED: edit tools must be issued as the only tool call in an assistant response. Only read-only inspect or plan tool calls from this response are handled in this turn. Re-issue this edit tool by itself in the next assistant response if the edit is still needed. Do not assume this skipped tool operation was applied."
	}
	return "NOT_EXECUTED: non-read-only tool calls cannot run in the same assistant response as an edit tool. Re-issue this tool separately after the edit has been isolated and reviewed, if it is still needed."
}

func mixedToolCallEditDeferralGuidance(cfg Config) string {
	return localizedText(cfg,
		"Some tool calls were not executed because edit tools must be isolated as the only mutation-capable tool call in an assistant response. Use only the completed read-only inspect or plan tool results above, then if an edit is still needed, issue exactly one edit tool call by itself in the next assistant response. Re-issue any deferred non-read-only tool separately after the edit is isolated and reviewed. Do not summarize or verify any skipped tool as if it ran.",
		"일부 tool call은 실행하지 않았습니다. edit tool은 assistant 응답 하나에서 mutation 가능 tool call과 섞이면 안 되며 단독으로 격리되어야 합니다. 위에서 완료된 read-only inspect 또는 plan tool 결과만 반영한 뒤, 수정이 여전히 필요하면 다음 assistant 응답에서 edit tool call 하나만 단독으로 다시 호출하세요. 보류된 non-read-only tool은 수정이 격리되고 리뷰된 뒤 별도로 다시 호출해야 합니다. 건너뛴 tool이 실행된 것처럼 요약하거나 검증하지 마세요.",
	)
}

func hasMutatingGitToolCalls(calls []ToolCall) bool {
	for _, call := range calls {
		if toolCallMutatesGitState(call) {
			return true
		}
	}
	return false
}

func shouldBlockUnconfirmedDocumentReadToolCalls(calls []ToolCall, session *Session) (bool, string, string) {
	if session == nil || !looksLikeDocumentAuthoringIntent(latestExternalOrUserMessageText(session.Messages)) {
		return false, "", ""
	}
	for _, call := range calls {
		if strings.TrimSpace(call.Name) != "read_file" {
			continue
		}
		targetPath := normalizeSessionRelativePath(toolCallPathArgument(call))
		if targetPath == "." || !pathLooksLikeDocumentArtifact(targetPath) {
			continue
		}
		parentPath := normalizeSessionRelativePath(filepath.Dir(targetPath))
		if documentPathConfirmedBySession(session, targetPath) {
			continue
		}
		return true, targetPath, parentPath
	}
	return false, "", ""
}

func shouldBlockLocalToolCallsBeforeWebResearch(calls []ToolCall, session *Session, mcp *MCPManager) bool {
	if session == nil || mcp == nil || !mcp.HasWebResearchCapability() {
		return false
	}
	latestUser := latestExternalOrUserMessageText(session.Messages)
	if !shouldPrioritizeWebResearchInSystemPrompt(strings.ToLower(strings.TrimSpace(latestUser))) {
		return false
	}
	if sessionHasWebResearchToolResult(session, mcp) || toolCallsIncludeWebResearch(calls, mcp) {
		return false
	}
	for _, call := range calls {
		if toolCallAllowedBeforeWebResearch(call, mcp) {
			continue
		}
		return true
	}
	return false
}

func shouldBlockWebResearchForLocalCodeWork(calls []ToolCall, session *Session, mcp *MCPManager) bool {
	if session == nil {
		return false
	}
	latestUser := strings.ToLower(strings.TrimSpace(latestExternalOrUserMessageText(session.Messages)))
	if requestExplicitlyAsksForWebResearch(latestUser) || !shouldUseLocalCodeToolPolicy(session) {
		return false
	}
	return toolCallsIncludeWebResearch(calls, mcp)
}

func formatBlockedLocalCodeWebResearchProgress(cfg Config, calls []ToolCall) string {
	intents := webResearchCallIntents(calls, 4)
	if len(intents) == 0 {
		return localizedText(cfg,
			"Blocked web research tool call during local code review/repair. The model must continue with local source evidence.",
			"로컬 코드 리뷰/수정 중 외부 웹 리서치 도구 호출을 차단했습니다. 모델은 로컬 소스 근거로 계속 진행해야 합니다.",
		)
	}
	return fmt.Sprintf(localizedText(cfg,
		"Blocked web research tool call during local code review/repair. Model wanted to check: %s.",
		"로컬 코드 리뷰/수정 중 외부 웹 리서치 도구 호출을 차단했습니다. 모델이 확인하려던 항목: %s.",
	), strings.Join(intents, " | "))
}

func webResearchCallIntents(calls []ToolCall, limit int) []string {
	if limit <= 0 {
		return nil
	}
	intents := make([]string, 0, minInt(len(calls), limit))
	for _, call := range calls {
		if !toolCallNameLooksLikeWebResearch(call.Name) {
			continue
		}
		intent := webResearchCallIntent(call)
		if intent == "" {
			intent = strings.TrimSpace(call.Name)
		}
		if intent == "" {
			continue
		}
		intents = append(intents, intent)
		if len(intents) >= limit {
			break
		}
	}
	return intents
}

func webResearchCallIntent(call ToolCall) string {
	args := map[string]any{}
	if strings.TrimSpace(call.Arguments) != "" {
		_ = json.Unmarshal([]byte(call.Arguments), &args)
	}
	query := strings.TrimSpace(firstNonBlankString(
		stringValue(args, "query"),
		stringValue(args, "q"),
		stringValue(args, "search_query"),
		stringValue(args, "question"),
	))
	if query != "" {
		return "query=" + compactReviewVisibleInlineText(query, 220)
	}
	url := strings.TrimSpace(firstNonBlankString(
		stringValue(args, "url"),
		stringValue(args, "uri"),
		stringValue(args, "href"),
	))
	if url != "" {
		return "url=" + compactReviewVisibleInlineText(url, 220)
	}
	return strings.TrimSpace(call.Name)
}

func shouldUseLocalCodeToolPolicy(session *Session) bool {
	if session == nil {
		return false
	}
	latestUser := strings.ToLower(strings.TrimSpace(latestExternalOrUserMessageText(session.Messages)))
	if requestExplicitlyAsksForWebResearch(latestUser) {
		return false
	}
	if requestLooksLikeLocalCodeWork(latestUser) {
		return true
	}
	if requestLooksLikeLocalVerificationWork(latestUser) {
		return true
	}
	if latestUserLooksLikeLocalCodeContinuation(latestUser) && sessionHasRecentLocalCodeWorkContext(session) {
		return true
	}
	if latestUserLooksLikeReviewRepairContinuation(latestUser) && reviewRunLooksLikeLocalCodeWork(session.LastReviewRun) {
		return true
	}
	return false
}

func latestUserLooksLikeLocalCodeContinuation(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	return containsAny(trimmed,
		"continue", "keep going", "proceed", "next step", "retry", "rerun", "run verification", "run tests", "run build",
		"계속", "이어", "다음", "재시도", "다시 실행", "검증 실행", "테스트 실행", "빌드 실행", "검증 명령",
	)
}

func latestUserLooksLikeReviewRepairContinuation(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	return containsAny(trimmed,
		"pending review repair confirmation",
		"pending reviewer-gate repair confirmation",
		"continue repairing",
		"continue repair",
		"계속 수정",
	)
}

func sessionHasRecentLocalCodeWorkContext(session *Session) bool {
	if session == nil {
		return false
	}
	const recentMessageScanLimit = 80
	start := len(session.Messages) - recentMessageScanLimit
	if start < 0 {
		start = 0
	}
	for i := len(session.Messages) - 1; i >= start; i-- {
		if messageLooksLikeLocalCodeWorkContext(session.Messages[i]) {
			return true
		}
	}
	return false
}

func messageLooksLikeLocalCodeWorkContext(msg Message) bool {
	if msg.Role == "user" {
		text := strings.ToLower(strings.TrimSpace(baseUserQueryText(msg.Text)))
		return text != "" &&
			!requestExplicitlyAsksForWebResearch(text) &&
			requestLooksLikeLocalCodeWork(text)
	}
	var parts []string
	if strings.TrimSpace(msg.ToolName) != "" {
		parts = append(parts, msg.ToolName)
	}
	if strings.TrimSpace(msg.Text) != "" {
		parts = append(parts, msg.Text)
	}
	for key, value := range msg.ToolMeta {
		parts = append(parts, key, fmt.Sprint(value))
	}
	for _, call := range msg.ToolCalls {
		parts = append(parts, call.Name, call.Arguments)
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join(parts, " ")))
	if text == "" || requestExplicitlyAsksForWebResearch(text) {
		return false
	}
	return requestLooksLikeLocalCodeWork(text)
}

func reviewRunLooksLikeLocalCodeWork(run *ReviewRun) bool {
	if run == nil {
		return false
	}
	if requestLooksLikeLocalCodeWork(strings.ToLower(strings.TrimSpace(run.Objective))) {
		return true
	}
	if requestLooksLikeLocalCodeWork(strings.ToLower(strings.TrimSpace(run.RequestAnalysis.OriginalRequest))) {
		return true
	}
	if len(run.ChangeSet.ChangedPaths) > 0 ||
		len(run.ChangeSet.ModifiedPaths) > 0 ||
		len(run.ChangeSet.AddedPaths) > 0 ||
		len(run.ChangeSet.DeletedPaths) > 0 ||
		len(run.Evidence.ChangedPaths) > 0 ||
		len(run.RequestAnalysis.ScopeDiscovery.CandidateFiles) > 0 {
		return true
	}
	if containsAny(strings.ToLower(strings.TrimSpace(run.Trigger)), "pre_write", "post_change", "before_fix") {
		return true
	}
	return false
}

func cloneDisabledTools(disabled map[string]bool) map[string]bool {
	out := map[string]bool{}
	for name, value := range disabled {
		if strings.TrimSpace(name) == "" || !value {
			continue
		}
		out[name] = true
	}
	return out
}

func disableWebResearchToolsForLocalCodeWork(disabled map[string]bool, registry *ToolRegistry) {
	if disabled == nil || registry == nil {
		return
	}
	for _, def := range registry.Definitions() {
		name := strings.TrimSpace(def.Name)
		if name == "" || !toolCallNameLooksLikeWebResearch(name) {
			continue
		}
		disabled[name] = true
	}
}

func sessionHasWebResearchToolResult(session *Session, mcp *MCPManager) bool {
	if session == nil || mcp == nil {
		return false
	}
	for _, msg := range session.Messages {
		if msg.Role != "tool" || msg.IsError {
			continue
		}
		if mcp.IsWebResearchToolName(strings.TrimSpace(msg.ToolName)) {
			return true
		}
	}
	return false
}

func toolCallsIncludeWebResearch(calls []ToolCall, mcp *MCPManager) bool {
	for _, call := range calls {
		if mcp != nil && mcp.IsWebResearchToolCall(call) {
			return true
		}
		if toolCallNameLooksLikeWebResearch(call.Name) {
			return true
		}
	}
	return false
}

func toolCallNameLooksLikeWebResearch(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "mcp__") &&
		containsAny(lower,
			"web_research",
			"web_search",
			"search_web",
			"browse_web",
			"browser",
			"fetch_url",
			"tavily",
			"serpapi",
			"brave_search",
		) {
		return true
	}
	return containsAny(lower,
		"search_web",
		"web_search",
		"web_research",
		"browse_web",
		"fetch_url",
	)
}

func localCodeWebResearchBlockGuidance(cfg Config, session *Session) string {
	latestUser := ""
	if session != nil {
		latestUser = latestExternalOrUserMessageText(session.Messages)
	}
	language, _ := inferResponseLanguageForUserText(latestUser, cfg)
	if language == "ko" {
		return "이 요청은 로컬 코드 리뷰/수정 작업입니다. 사용자가 외부 웹 리서치를 명시적으로 요청하지 않는 한 MCP web/search/browser 도구를 사용하지 마세요. 한국어로 계속 답변하고, 로컬 소스 근거(read_file, grep, git diff/status)와 이번 턴에 첨부된 리뷰 finding만 사용하세요."
	}
	return "This is a local code review or repair request. Do not use MCP web/search/browser tools unless the user explicitly asks for external web research. Continue in the user's requested language with local source evidence: read_file, grep, git diff/status, and the review findings already attached to this turn."
}

func shouldRetryKoreanLocalCodeToolNarration(message Message, session *Session, cfg Config) bool {
	text := strings.TrimSpace(message.Text)
	if text == "" || len(message.ToolCalls) == 0 || session == nil {
		return false
	}
	latestUser := latestExternalOrUserMessageText(session.Messages)
	language, _ := inferResponseLanguageForUserText(latestUser, cfg)
	if language != "ko" {
		return false
	}
	if !requestLooksLikeLocalCodeWork(strings.ToLower(strings.TrimSpace(latestUser))) {
		return false
	}
	if textContainsHangul(text) {
		return false
	}
	if !textLooksMostlyEnglish(text) {
		return false
	}
	return containsAny(strings.ToLower(text),
		"i see",
		"let me",
		"now",
		"apply",
		"patch",
		"fix",
		"review",
		"file",
		"tool",
	)
}

func koreanLocalCodeToolNarrationGuidance() string {
	return "응답 언어 정책 위반입니다. 최신 사용자 요청은 한국어 로컬 코드 리뷰/수정 작업입니다. 도구 호출 전 진행 설명도 한국어로 작성하세요. 코드 식별자, 경로, API 이름, 명령어는 원문을 유지해도 됩니다."
}

func shouldRetryPreFixEditWithoutVisibleReviewSummary(message Message, session *Session) bool {
	if session == nil || session.LastReviewRun == nil || len(message.ToolCalls) == 0 {
		return false
	}
	run := *session.LastReviewRun
	if !strings.EqualFold(strings.TrimSpace(run.Trigger), reviewBeforeFixTrigger) {
		return false
	}
	if !toolCallsIncludeEditTool(message.ToolCalls) {
		return false
	}
	if len(preFixVisibleReviewSummaryObligations(run, 3)) == 0 {
		return false
	}
	if assistantTextIncludesPreFixReviewSummary(message.Text, run) {
		return false
	}
	return !sessionHasVisiblePreFixReviewSummaryForCurrentRepair(session, run)
}

func toolCallsIncludeEditTool(calls []ToolCall) bool {
	for _, call := range calls {
		if isEditTool(call.Name) {
			return true
		}
	}
	return false
}

func assistantTextIncludesPreFixReviewSummary(text string, run ReviewRun) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	obligations := preFixVisibleReviewSummaryObligations(run, 5)
	if len(obligations) == 0 {
		obligations = preFixVisibleReviewSummaryFindings(run, 5)
	}
	if len(obligations) == 0 && len(run.Findings) > 0 {
		obligations = normalizeReviewFindingCopies(run.Findings)
		if len(obligations) > 5 {
			obligations = obligations[:5]
		}
	}
	hasStructuredID := false
	for _, finding := range obligations {
		id := strings.TrimSpace(finding.ID)
		if id != "" {
			hasStructuredID = true
			if !strings.Contains(trimmed, id) {
				return false
			}
			continue
		}
		if !reviewFindingConcreteTextVisible(trimmed, finding) {
			return false
		}
	}
	if hasStructuredID || len(obligations) > 0 {
		return true
	}
	if strings.Contains(lower, "rf-") {
		return true
	}
	if containsAny(trimmed, "검토 결과", "리뷰 결과") {
		return true
	}
	if containsAny(lower, "review finding", "review findings") {
		return true
	}
	return false
}

func reviewFindingConcreteTextVisible(text string, finding ReviewFinding) bool {
	lower := strings.ToLower(text)
	fields := []string{
		finding.Title,
		finding.RequiredFix,
		finding.Evidence,
		finding.Category,
		finding.Severity,
	}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(field)) {
			return true
		}
	}
	return false
}

func sessionHasVisiblePreFixReviewSummaryForCurrentRepair(session *Session, run ReviewRun) bool {
	if session == nil {
		return false
	}
	for idx := len(session.Messages) - 1; idx >= 0; idx-- {
		message := session.Messages[idx]
		if message.Role == "assistant" {
			if assistantTextIncludesPreFixReviewSummary(message.Text, run) {
				return true
			}
			continue
		}
		if message.Role != "user" {
			continue
		}
		if !userMessageLooksLikePreFixReviewGuidance(message.Text) {
			return false
		}
	}
	return false
}

func userMessageLooksLikePreFixReviewGuidance(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return containsAny(trimmed,
		"수정 전에 리뷰를 완료",
		"파일 쓰기/패치 도구를 호출하기 전에",
		"검토 결과",
		"리뷰 결과",
	) || containsAny(lower,
		"review-first pass completed",
		"before calling file write",
		"before calling patch",
		"latest review findings",
		"review result",
	)
}

func sessionHasVisiblePreFixReviewSummary(session *Session, run ReviewRun) bool {
	if session == nil {
		return false
	}
	for idx := len(session.Messages) - 1; idx >= 0; idx-- {
		message := session.Messages[idx]
		if message.Role != "assistant" {
			continue
		}
		if assistantTextIncludesPreFixReviewSummary(message.Text, run) {
			return true
		}
	}
	return false
}

func preFixVisibleReviewSummaryObligations(run ReviewRun, limit int) []ReviewFinding {
	findings := preFixRepairObligationFindings(run)
	if limit > 0 && len(findings) > limit {
		return findings[:limit]
	}
	return findings
}

func preFixVisibleReviewSummaryFindings(run ReviewRun, limit int) []ReviewFinding {
	findings := preFixRepairObligationFindings(run)
	warnings := preFixReplyWarningFindings(run, 0)
	if len(findings) > 0 && len(warnings) > 0 {
		seen := make(map[string]bool, len(findings))
		for _, finding := range findings {
			if id := strings.TrimSpace(finding.ID); id != "" {
				seen[id] = true
			}
		}
		for _, warning := range warnings {
			if id := strings.TrimSpace(warning.ID); id != "" && seen[id] {
				continue
			}
			findings = append(findings, warning)
			if id := strings.TrimSpace(warning.ID); id != "" {
				seen[id] = true
			}
		}
	} else if len(findings) == 0 {
		findings = warnings
	}
	if limit > 0 && len(findings) > limit {
		return findings[:limit]
	}
	return findings
}

func formatPreFixVisibleReviewSummary(cfg Config, run ReviewRun) string {
	findings := preFixVisibleReviewSummaryFindings(run, 8)
	if len(findings) == 0 {
		return ""
	}
	korean := reviewRunPrefersKorean(cfg, run)
	var b strings.Builder
	if korean {
		b.WriteString("검토 결과:")
	} else {
		b.WriteString("Review findings:")
	}
	for _, finding := range findings {
		finding.Normalize()
		id := valueOrDefault(finding.ID, "RF")
		severity := valueOrDefault(finding.Severity, "unknown")
		category := valueOrDefault(finding.Category, "general")
		title := compactPromptSection(firstNonBlankString(finding.Title, finding.Evidence, finding.Impact, "Review finding"), 180)
		requiredFix := compactPromptSection(strings.TrimSpace(finding.RequiredFix), 220)
		if korean {
			fmt.Fprintf(&b, "\n- %s [%s/%s]: %s", id, severity, category, title)
			if requiredFix != "" {
				fmt.Fprintf(&b, " -> 조치: %s", requiredFix)
			}
		} else {
			fmt.Fprintf(&b, "\n- %s [%s/%s]: %s", id, severity, category, title)
			if requiredFix != "" {
				fmt.Fprintf(&b, " -> Fix: %s", requiredFix)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func preFixVisibleReviewSummaryGuidance(run ReviewRun) string {
	korean := textContainsHangul(run.Objective)
	findings := preFixVisibleReviewSummaryFindings(run, 8)
	var b strings.Builder
	if korean {
		b.WriteString("수정 전 리뷰 finding을 사용자에게 먼저 보여줘야 합니다. 파일 쓰기/패치 도구를 호출하기 전에 한국어로 `검토 결과:` 섹션을 만들고, 아래 RF 항목과 조치 방향을 짧게 요약하세요. 그 다음 필요한 로컬 수정 도구를 호출하세요.")
		if len(findings) > 0 {
			b.WriteString("\n\n반드시 언급할 리뷰 항목:")
		}
	} else {
		b.WriteString("Show the pre-fix review findings to the user before editing. Before calling a file write or patch tool, add a short `Review findings:` section with the RF items and repair direction, then call the needed local edit tool.")
		if len(findings) > 0 {
			b.WriteString("\n\nReview items to mention:")
		}
	}
	for _, finding := range findings {
		finding.Normalize()
		title := valueOrDefault(finding.Title, "Review finding")
		if korean {
			fmt.Fprintf(&b, "\n- %s [%s/%s] %s", valueOrDefault(finding.ID, "RF"), finding.Severity, finding.Category, title)
			if strings.TrimSpace(finding.RequiredFix) != "" {
				fmt.Fprintf(&b, " / 조치: %s", compactPromptSection(finding.RequiredFix, 180))
			}
		} else {
			fmt.Fprintf(&b, "\n- %s [%s/%s] %s", valueOrDefault(finding.ID, "RF"), finding.Severity, finding.Category, title)
			if strings.TrimSpace(finding.RequiredFix) != "" {
				fmt.Fprintf(&b, " / Fix: %s", compactPromptSection(finding.RequiredFix, 180))
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func toolCallAllowedBeforeWebResearch(call ToolCall, mcp *MCPManager) bool {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return true
	}
	if name == "update_plan" {
		return true
	}
	if mcp != nil && mcp.IsWebResearchToolCall(call) {
		return true
	}
	return false
}

func documentPathConfirmedBySession(session *Session, targetPath string) bool {
	if session == nil {
		return false
	}
	normalizedTarget := normalizeSessionRelativePath(targetPath)
	for _, msg := range session.Messages {
		if msg.Role != "tool" || msg.IsError {
			continue
		}
		switch strings.TrimSpace(msg.ToolName) {
		case "read_file", "write_file", "replace_in_file":
			if normalizeSessionRelativePath(toolMetaString(msg.ToolMeta, "path")) == normalizedTarget {
				return true
			}
		case "apply_patch":
			for _, changed := range toolMetaStringSlice(msg.ToolMeta, "changed_paths") {
				if normalizeSessionRelativePath(changed) == normalizedTarget {
					return true
				}
			}
		case "list_files":
			if listFilesOutputConfirmsPath(msg.Text, normalizedTarget) {
				return true
			}
		}
	}
	return false
}

func listFilesOutputConfirmsPath(output string, targetPath string) bool {
	normalizedTarget := strings.TrimSuffix(normalizeSessionRelativePath(targetPath), "/")
	if normalizedTarget == "" || normalizedTarget == "." {
		return false
	}
	for _, line := range strings.Split(output, "\n") {
		normalizedLine := strings.TrimSuffix(normalizeSessionRelativePath(line), "/")
		if strings.EqualFold(normalizedLine, normalizedTarget) {
			return true
		}
	}
	return false
}

func sessionHasListFilesConfirmationForParent(session *Session, parentPath string) bool {
	if session == nil {
		return false
	}
	normalizedParent := normalizeSessionRelativePath(parentPath)
	for _, msg := range session.Messages {
		if msg.Role != "tool" || msg.IsError || strings.TrimSpace(msg.ToolName) != "list_files" {
			continue
		}
		if listFilesCoverageConfirmsParent(
			normalizeSessionRelativePath(toolMetaString(msg.ToolMeta, "path")),
			toolMetaBool(msg.ToolMeta, "recursive"),
			normalizedParent,
		) {
			return true
		}
	}
	return false
}

func toolCallsIncludeListFilesConfirmation(calls []ToolCall, parentPath string) bool {
	normalizedParent := normalizeSessionRelativePath(parentPath)
	for _, call := range calls {
		if strings.TrimSpace(call.Name) != "list_files" {
			continue
		}
		if listFilesCoverageConfirmsParent(
			normalizeSessionRelativePath(toolCallPathArgument(call)),
			toolCallRecursiveArgument(call),
			normalizedParent,
		) {
			return true
		}
	}
	return false
}

func listFilesCoverageConfirmsParent(listedRoot string, recursive bool, parentPath string) bool {
	root := normalizeSessionRelativePath(listedRoot)
	parent := normalizeSessionRelativePath(parentPath)
	if root == parent {
		return true
	}
	if !recursive {
		return false
	}
	if root == "." {
		return true
	}
	return parent == root || strings.HasPrefix(parent, root+"/")
}

func toolCallMutatesGitState(call ToolCall) bool {
	switch strings.TrimSpace(call.Name) {
	case "git_add", "git_commit", "git_push", "git_create_pr":
		return true
	case "run_shell", "run_shell_background":
		return shellCommandMutatesGitState(toolCallCommandArgument(call))
	case "run_shell_bundle_background":
		for _, command := range toolCallCommandsArgument(call) {
			if shellCommandMutatesGitState(command) {
				return true
			}
		}
	}
	return false
}

func toolCallIsVerificationRetryOrPoll(call ToolCall, session *Session) bool {
	switch strings.TrimSpace(call.Name) {
	case "run_shell", "run_shell_background":
		return assessShellCommandMutation(toolCallCommandArgument(call)).Class == shellMutationVerificationArtifacts
	case "run_shell_bundle_background":
		for _, command := range toolCallCommandsArgument(call) {
			if assessShellCommandMutation(command).Class == shellMutationVerificationArtifacts {
				return true
			}
		}
		return false
	case "check_shell_job", "check_shell_bundle":
		return toolCallTargetsVerificationBackgroundWork(call, session)
	default:
		return false
	}
}

func toolCallTargetsVerificationBackgroundWork(call ToolCall, session *Session) bool {
	if session == nil {
		return false
	}
	switch strings.TrimSpace(call.Name) {
	case "check_shell_job":
		jobID := strings.TrimSpace(stringValue(toolCallArgumentsMap(call), "job_id"))
		if jobID == "" || strings.EqualFold(jobID, "latest") {
			session.normalizeBackgroundJobs()
			if len(session.BackgroundJobs) == 0 {
				return true
			}
			jobID = strings.TrimSpace(session.BackgroundJobs[0].ID)
		}
		job, ok := session.BackgroundJob(jobID)
		if !ok {
			return false
		}
		return backgroundJobIsVerificationLike(job)
	case "check_shell_bundle":
		args := toolCallArgumentsMap(call)
		bundleID := strings.TrimSpace(stringValue(args, "bundle_id"))
		if bundleID != "" || len(stringSliceValue(args, "job_ids")) == 0 {
			if bundleID == "" || strings.EqualFold(bundleID, "latest") {
				session.normalizeBackgroundBundles()
				if len(session.BackgroundBundles) == 0 {
					return true
				}
				bundleID = strings.TrimSpace(session.BackgroundBundles[0].ID)
			}
			bundle, ok := session.BackgroundBundle(bundleID)
			return ok && bundle.VerificationLike
		}
		for _, jobID := range normalizeBackgroundCommandList(stringSliceValue(args, "job_ids"), 8) {
			job, ok := session.BackgroundJob(jobID)
			if ok && backgroundJobIsVerificationLike(job) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func backgroundJobIsVerificationLike(job BackgroundShellJob) bool {
	job.Normalize()
	return strings.EqualFold(strings.TrimSpace(job.MutationClass), string(shellMutationVerificationArtifacts))
}

func declinedVerificationFollowupBlockedResult(call ToolCall) ToolExecutionResult {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = "unknown"
	}
	return ToolExecutionResult{
		DisplayText: "NOT_EXECUTED: a build, test, or verification command was already skipped or declined in this turn. Do not retry the verification command or poll background jobs unless the user explicitly approves verification; disclose that verification was not run. Do not relabel resolved code-review findings as remaining bugs only because verification is missing.",
		Meta: map[string]any{
			"tool_name":                name,
			"plan_effect":              "none",
			"result_class":             "verification_skipped",
			"verification_like":        true,
			"verification_status":      string(VerificationSkipped),
			"verification_evidence":    false,
			"verification_approved":    false,
			"verification_declined":    true,
			"command_execution_status": "declined",
			"success":                  false,
		},
	}
}

func outOfScopeVerificationFollowupBlockedResult(call ToolCall) ToolExecutionResult {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = "unknown"
	}
	return ToolExecutionResult{
		DisplayText: "NOT_EXECUTED: automatic verification already failed outside the current patch scope. Do not retry or probe build, test, or verification commands in this turn; disclose the external or ambient verification blocker/risk instead of broadening the repair.",
		Meta: map[string]any{
			"tool_name":                name,
			"plan_effect":              "none",
			"result_class":             "verification_skipped",
			"verification_like":        true,
			"verification_status":      string(VerificationSkipped),
			"verification_evidence":    false,
			"verification_approved":    false,
			"verification_out_scope":   true,
			"command_execution_status": "blocked_out_of_scope",
			"success":                  false,
		},
	}
}

func outOfScopeVerificationFinalOnlyBlockedResult(call ToolCall) ToolExecutionResult {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = "unknown"
	}
	return ToolExecutionResult{
		DisplayText: "NOT_EXECUTED: automatic verification already failed outside the current patch scope, so this turn is final-answer-only. No further tools are available; provide the final answer from the existing patch, review, and verification evidence.",
		Meta: map[string]any{
			"tool_name":                name,
			"plan_effect":              "none",
			"result_class":             "final_answer_only",
			"verification_like":        toolCallIsVerificationRetryOrPoll(call, nil),
			"verification_status":      string(VerificationSkipped),
			"verification_evidence":    false,
			"verification_approved":    false,
			"verification_out_scope":   true,
			"command_execution_status": "blocked_final_answer_only",
			"success":                  false,
		},
	}
}

func toolCallAllowedInReadOnlyAnalysis(call ToolCall) bool {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return false
	}
	if isEditTool(name) {
		return false
	}
	switch name {
	case "read_file", "list_files", "grep", "git_status", "git_diff", "check_shell_job", "check_shell_bundle", "update_plan", "get_goal":
		return true
	default:
		return false
	}
}

type parallelToolCallBatchOutcome struct {
	hadSuccess bool
	errorText  string
}

type parallelToolCallExecution struct {
	call   ToolCall
	result ToolExecutionResult
	err    error
}

func (a *Agent) toolCallAllowedInReadOnlyAnalysis(call ToolCall) bool {
	if toolCallAllowedInReadOnlyAnalysis(call) {
		return true
	}
	if a == nil || a.Tools == nil {
		return false
	}
	return a.Tools.ToolCallReadOnly(call.Name)
}

func (a *Agent) shouldExecuteToolCallBatchInParallel(calls []ToolCall, readOnlyAnalysis bool, disabled map[string]bool) bool {
	if len(calls) < 2 || a == nil || a.Tools == nil {
		return false
	}
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" || disabled[name] || isEditTool(name) {
			return false
		}
		if !toolCallArgumentsAreJSONObject(call.Arguments) {
			return false
		}
		if readOnlyAnalysis && !a.toolCallAllowedInReadOnlyAnalysis(call) {
			return false
		}
		if !a.Tools.ToolCallSupportsParallel(name) {
			return false
		}
	}
	return true
}

func toolCallArgumentsAreJSONObject(args string) bool {
	if strings.TrimSpace(args) == "" {
		return true
	}
	payload := map[string]any{}
	return json.Unmarshal([]byte(args), &payload) == nil
}

func (a *Agent) executeParallelToolCallBatch(ctx context.Context, calls []ToolCall, indexes []int, mcpTurnMetadata map[string]any) (parallelToolCallBatchOutcome, error) {
	outcome := parallelToolCallBatchOutcome{}
	if len(calls) == 0 {
		return outcome, nil
	}
	results := make([]parallelToolCallExecution, len(calls))
	provider := ""
	model := ""
	if a.Session != nil {
		provider = a.Session.Provider
		model = a.Session.Model
	}
	baseToolCtx := a.contextWithMCPToolInvocationMetadata(ctx, mcpTurnMetadata)
	var wg sync.WaitGroup
	for callIndex, call := range calls {
		if summary := summarizeToolInvocation(a.Config, call); summary != "" {
			a.emitProgressEvent(ProgressEvent{
				Kind:             progressKindToolStarted,
				Message:          summary,
				ToolName:         call.Name,
				ToolCallID:       call.ID,
				ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
			})
		}
		a.noteToolConversationStart(call)
		a.noteToolExecutionStart(call)
		results[callIndex].call = call
		wg.Add(1)
		go func(index int, item ToolCall) {
			defer wg.Done()
			toolCtx := contextWithOriginalImageDetailSupport(baseToolCtx, canRequestOriginalImageDetail(provider, model))
			toolCtx = contextWithImageInputSupport(toolCtx, canRequestImageInput(provider, model))
			toolCtx = contextWithToolCallHookMetadata(toolCtx, item)
			result, err := a.Tools.ExecuteDetailed(toolCtx, item.Name, item.Arguments)
			result = sanitizeToolExecutionImageDetailForModel(result, provider, model)
			results[index] = parallelToolCallExecution{
				call:   item,
				result: result,
				err:    err,
			}
		}(callIndex, call)
	}
	wg.Wait()
	for callIndex, executed := range results {
		call := executed.call
		result := executed.result
		err := executed.err
		toolMsgIndex := -1
		if callIndex >= 0 && callIndex < len(indexes) {
			toolMsgIndex = indexes[callIndex]
		}
		if err == nil {
			a.noteToolConversationResult(call, result)
		} else {
			a.noteToolConversationFailureResult(call, result, err, false)
		}
		toolMsg := Message{
			Role:             "tool",
			ToolCallID:       call.ID,
			ToolName:         call.Name,
			Text:             toolExecutionModelText(result),
			ToolContentItems: toolExecutionModelContentItems(result),
			ToolMeta:         result.Meta,
			IsError:          err != nil,
		}
		if err != nil {
			toolMsg.Text = toolExecutionModelTextWithError(result, err)
			if currentError := strings.TrimSpace(err.Error()); currentError != "" {
				outcome.errorText = currentError
			}
			if summary := summarizeToolFailure(a.Config, call, err); summary != "" {
				a.emitProgressEvent(ProgressEvent{
					Kind:             progressKindToolFailed,
					Message:          summary,
					ToolName:         call.Name,
					ToolCallID:       call.ID,
					ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
					Status:           firstNonEmptyLine(err.Error()),
				})
			}
		} else {
			outcome.hadSuccess = true
			if summary := summarizeToolCompletion(a.Config, call, result.DisplayText); summary != "" {
				a.emitProgressEvent(ProgressEvent{
					Kind:             progressKindToolCompleted,
					Message:          summary,
					ToolName:         call.Name,
					ToolCallID:       call.ID,
					ArgumentsPreview: summarizeToolArgumentsPreview(call.Arguments),
				})
			}
		}
		a.setToolExecutionResult(toolMsgIndex, toolMsg)
		a.noteToolExecutionResultDetailed(call, result, err)
		accounting := a.accountGoalProgressAfterTool(call)
		if accounting.StatusChanged && accounting.Goal.Status == goalStatusBudgetLimited {
			a.Session.AddMessage(internalUserMessage(goalBudgetLimitContextMessage(accounting.Goal)))
		}
	}
	if err := a.Store.Save(a.Session); err != nil {
		return outcome, err
	}
	return outcome, nil
}

func readOnlyInspectionToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "read_file", "list_files", "grep", "git_status", "git_diff", "check_shell_job", "check_shell_bundle", "get_goal":
		return true
	default:
		return false
	}
}

func readOnlyAnalysisToolBlockedResult(cfg Config, call ToolCall) ToolExecutionResult {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = "unknown"
	}
	return ToolExecutionResult{
		DisplayText: localizedText(cfg,
			fmt.Sprintf("NOT_EXECUTED: read-only analysis mode blocked tool `%s` because it is not in the read-only inspection allowlist. Use read_file, grep, list_files, git_status, git_diff, or answer from the evidence already collected.", name),
			fmt.Sprintf("NOT_EXECUTED: 읽기 전용 분석 모드에서 `%s` 도구를 차단했습니다. 이 도구는 읽기 전용 검사 허용 목록에 없습니다. read_file, grep, list_files, git_status, git_diff를 사용하거나 이미 수집한 근거로 답하세요.", name),
		),
		Meta: map[string]any{
			"tool_name":                name,
			"plan_effect":              "none",
			"result_class":             "read_only_policy_block",
			"read_only_analysis":       true,
			"read_only_allowed":        false,
			"command_execution_status": "blocked",
			"changed_workspace":        false,
			"success":                  false,
		},
	}
}

func turnDisabledToolBlockedResult(cfg Config, call ToolCall) ToolExecutionResult {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = "unknown"
	}
	return ToolExecutionResult{
		DisplayText: localizedText(cfg,
			fmt.Sprintf("NOT_EXECUTED: tool `%s` is disabled for this turn and was not executed. Use the currently exposed tools or provide the final answer from existing evidence.", name),
			fmt.Sprintf("NOT_EXECUTED: 이번 턴에서 `%s` 도구는 비활성화되어 실행하지 않았습니다. 현재 노출된 도구를 사용하거나 기존 근거로 최종 답변을 작성하세요.", name),
		),
		Meta: map[string]any{
			"tool_name":                name,
			"plan_effect":              "none",
			"result_class":             "turn_tool_exposure_block",
			"turn_tool_disabled":       true,
			"exposed_to_model":         false,
			"command_execution_status": "blocked",
			"changed_workspace":        false,
			"success":                  false,
		},
	}
}

func toolCallCommandArgument(call ToolCall) string {
	args := toolCallArgumentsMap(call)
	return stringValue(args, "command")
}

func toolCallPathArgument(call ToolCall) string {
	args := toolCallArgumentsMap(call)
	return stringValue(args, "path")
}

func toolCallRecursiveArgument(call ToolCall) bool {
	args := toolCallArgumentsMap(call)
	return boolValue(args, "recursive", false)
}

type shellApplyPatchPayload struct {
	patch   string
	workdir string
}

func rewriteShellApplyPatchToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return calls
	}
	out := make([]ToolCall, 0, len(calls))
	changed := false
	for _, call := range calls {
		if strings.TrimSpace(call.Name) != "run_shell" {
			out = append(out, call)
			continue
		}
		args := toolCallArgumentsMap(call)
		payload, ok := extractShellApplyPatchPayload(stringValue(args, "command"))
		if !ok {
			out = append(out, call)
			continue
		}
		nextArgs := map[string]any{"patch": payload.patch}
		if ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id")); ownerNodeID != "" {
			nextArgs["owner_node_id"] = ownerNodeID
		}
		if payload.workdir != "" {
			nextArgs["workdir"] = payload.workdir
		}
		encoded, err := json.Marshal(nextArgs)
		if err != nil {
			out = append(out, call)
			continue
		}
		call.Name = "apply_patch"
		call.Arguments = string(encoded)
		out = append(out, call)
		changed = true
	}
	if !changed {
		return calls
	}
	return out
}

func toolRegistryHasTool(registry *ToolRegistry, name string) bool {
	if registry == nil {
		return false
	}
	_, ok := registry.tools[strings.TrimSpace(name)]
	return ok
}

func extractShellApplyPatchPayload(command string) (shellApplyPatchPayload, bool) {
	normalized := strings.ReplaceAll(command, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = strings.TrimSpace(normalized)
	firstLine, rest, ok := strings.Cut(normalized, "\n")
	if !ok {
		return shellApplyPatchPayload{}, false
	}
	marker, workdir, stripTabs, ok := parseShellApplyPatchStartLine(firstLine)
	if !ok {
		return shellApplyPatchPayload{}, false
	}
	patch, suffix, ok := extractShellHeredocBody(rest, marker, stripTabs)
	if !ok || strings.TrimSpace(suffix) != "" {
		return shellApplyPatchPayload{}, false
	}
	if !strings.Contains(patch, "*** Begin Patch") || !strings.Contains(patch, "*** End Patch") {
		return shellApplyPatchPayload{}, false
	}
	return shellApplyPatchPayload{patch: patch, workdir: workdir}, true
}

func toolCallsIncludeImplicitShellApplyPatchBody(calls []ToolCall) bool {
	for _, call := range calls {
		if strings.TrimSpace(call.Name) != "run_shell" {
			continue
		}
		args := toolCallArgumentsMap(call)
		if shellCommandIsImplicitApplyPatchBody(stringValue(args, "command")) {
			return true
		}
	}
	return false
}

func shellCommandIsImplicitApplyPatchBody(command string) bool {
	normalized := strings.ReplaceAll(command, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = strings.TrimSpace(strings.TrimPrefix(normalized, "\uFEFF"))
	if !strings.HasPrefix(normalized, "*** Begin Patch") {
		return false
	}
	if !strings.HasSuffix(normalized, "*** End Patch") {
		return false
	}
	if normalizePatchDocumentText(normalized) != normalized {
		return false
	}
	_, err := parsePatchDocument(normalized)
	return err == nil
}

func parseShellApplyPatchStartLine(line string) (marker string, workdir string, stripTabs bool, ok bool) {
	remaining := strings.TrimSpace(line)
	command, rest, ok := consumeShellToken(remaining)
	if !ok {
		return "", "", false, false
	}
	if command == "cd" {
		workdir, rest, ok = consumeShellToken(rest)
		if !ok {
			return "", "", false, false
		}
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "&&") {
			return "", "", false, false
		}
		remaining = strings.TrimSpace(strings.TrimPrefix(rest, "&&"))
		command, rest, ok = consumeShellToken(remaining)
		if !ok {
			return "", "", false, false
		}
	}
	if command != "apply_patch" && command != "applypatch" {
		return "", "", false, false
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "<<") {
		return "", "", false, false
	}
	rest = strings.TrimPrefix(rest, "<<")
	if strings.HasPrefix(rest, "-") {
		stripTabs = true
		rest = strings.TrimPrefix(rest, "-")
	}
	marker, rest, ok = consumeShellToken(rest)
	if !ok || marker == "" || strings.TrimSpace(rest) != "" {
		return "", "", false, false
	}
	return marker, workdir, stripTabs, true
}

func consumeShellToken(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", false
	}
	if text[0] == '\'' || text[0] == '"' {
		quote := text[0]
		for i := 1; i < len(text); i++ {
			if text[i] == quote {
				return text[1:i], text[i+1:], true
			}
		}
		return "", "", false
	}
	for i, r := range text {
		if unicode.IsSpace(r) || strings.ContainsRune("&|;<>", r) {
			if i == 0 {
				return "", "", false
			}
			return text[:i], text[i:], true
		}
	}
	return text, "", true
}

func extractShellHeredocBody(text string, marker string, stripTabs bool) (string, string, bool) {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return "", "", false
	}
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmedLine := strings.TrimRight(line, "\n")
		markerCandidate := trimmedLine
		if stripTabs {
			markerCandidate = strings.TrimLeft(markerCandidate, "\t")
		}
		if markerCandidate == marker {
			return text[:offset], text[offset+len(line):], true
		}
		offset += len(line)
	}
	return "", "", false
}

func toolCallArgumentsMap(call ToolCall) map[string]any {
	args := map[string]any{}
	if strings.TrimSpace(call.Arguments) != "" {
		_ = json.Unmarshal([]byte(call.Arguments), &args)
	}
	return args
}

func toolCallSupportsOwnerNodeID(name string) bool {
	switch strings.TrimSpace(name) {
	case "apply_patch", "write_file", "replace_in_file", "run_shell", "run_shell_background", "run_shell_bundle_background":
		return true
	default:
		return false
	}
}

func withToolCallOwnerNodeID(call ToolCall, ownerNodeID string) ToolCall {
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	if ownerNodeID == "" || !toolCallSupportsOwnerNodeID(call.Name) {
		return call
	}
	args := map[string]any{}
	if strings.TrimSpace(call.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return call
		}
	}
	if existing := strings.TrimSpace(stringValue(args, "owner_node_id")); existing != "" {
		return call
	}
	args["owner_node_id"] = ownerNodeID
	encoded, err := json.Marshal(args)
	if err != nil {
		return call
	}
	call.Arguments = string(encoded)
	return call
}

func stripUnsupportedOwnerNodeIDFromToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return calls
	}
	out := make([]ToolCall, len(calls))
	copy(out, calls)
	for index := range out {
		if !toolCallForbidsModelOwnerNodeID(out[index].Name) {
			continue
		}
		if strings.TrimSpace(out[index].Arguments) == "" {
			continue
		}
		args := map[string]any{}
		if err := json.Unmarshal([]byte(out[index].Arguments), &args); err != nil {
			continue
		}
		if _, ok := args["owner_node_id"]; !ok {
			continue
		}
		delete(args, "owner_node_id")
		encoded, err := json.Marshal(args)
		if err != nil {
			continue
		}
		out[index].Arguments = string(encoded)
	}
	return out
}

func toolCallForbidsModelOwnerNodeID(name string) bool {
	switch strings.TrimSpace(name) {
	case "read_file",
		"list_files",
		"grep",
		"git_status",
		"git_diff",
		"check_shell_job",
		"check_shell_bundle",
		"get_goal",
		"update_plan":
		return true
	default:
		return false
	}
}

func assignFocusedOwnerNodeToToolCalls(calls []ToolCall, session *Session) []ToolCall {
	if len(calls) == 0 || session == nil || session.TaskState == nil {
		return calls
	}
	ownerNodeID := strings.TrimSpace(session.TaskState.ExecutorFocusNode)
	if ownerNodeID == "" {
		return calls
	}
	if !sessionOwnerNodeHasConcreteEditRouting(session, ownerNodeID) {
		return calls
	}
	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, withToolCallOwnerNodeID(call, ownerNodeID))
	}
	return out
}

func toolCallCommandsArgument(call ToolCall) []string {
	args := toolCallArgumentsMap(call)
	return stringSliceValue(args, "commands")
}

func normalizeSessionRelativePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "."
	}
	normalized := filepath.ToSlash(filepath.Clean(trimmed))
	if normalized == "" {
		return "."
	}
	return normalized
}

func pathLooksLikeDocumentArtifact(path string) bool {
	lower := strings.ToLower(normalizeSessionRelativePath(path))
	switch strings.ToLower(filepath.Ext(lower)) {
	case ".md", ".markdown", ".txt", ".rst", ".adoc":
		return true
	}
	return containsAny(lower,
		"/analysis/", "/document/", "/documents/", "/docs/", "/legal/", "/notes/", "/report/", "/reports/", "/research/",
	)
}

func (a *Agent) ensureUserInputRequestTracker() *UserInputRequestTracker {
	if a == nil {
		return nil
	}
	tracker := a.Workspace.UserInputRequests
	if tracker == nil {
		tracker = NewUserInputRequestTracker()
		a.Workspace.UserInputRequests = tracker
	}
	if a.Workspace.Perms != nil {
		a.Workspace.Perms.SetUserInputRequestTracker(tracker)
	}
	return tracker
}

func (a *Agent) mcpTurnMetadataForToolCall(turnStartedAt time.Time) map[string]any {
	if a == nil || a.Session == nil {
		return nil
	}
	metadata := map[string]any{}
	if sessionID := strings.TrimSpace(a.Session.ID); sessionID != "" {
		metadata["session_id"] = sessionID
		metadata["thread_id"] = sessionID
		if turnID := mcpTurnMetadataTurnID(sessionID, turnStartedAt); turnID != "" {
			metadata["turn_id"] = turnID
		}
		if traceID := mcpTurnMetadataTraceID(sessionID, turnStartedAt); traceID != "" {
			metadata["trace_id"] = traceID
		}
		metadata["thread_source"] = "user"
	}
	if provider := strings.TrimSpace(a.Session.Provider); provider != "" {
		metadata["provider"] = provider
	}
	if model := strings.TrimSpace(a.Session.Model); model != "" {
		metadata["model"] = model
	}
	if effort := normalizeReasoningEffort(a.Config.ReasoningEffort); effort != "" {
		metadata["reasoning_effort"] = effort
	}
	if serviceTier := normalizeServiceTier(a.Config.ServiceTier); serviceTier != "" {
		metadata["service_tier"] = serviceTier
	}
	if !turnStartedAt.IsZero() {
		metadata["turn_started_at_unix_ms"] = turnStartedAt.UnixMilli()
	}
	if permissionMode := a.activePermissionModeSnapshot(); permissionMode != "" {
		metadata["permission_mode"] = permissionMode
		if profileID := activePermissionProfileIDForModeString(permissionMode); profileID != "" {
			metadata["active_permission_profile_id"] = profileID
			metadata["active_permission_profile"] = activePermissionProfileSnapshotForModeString(permissionMode)
		}
		if sandbox := mcpTurnMetadataSandboxTag(permissionMode); sandbox != "" {
			metadata["sandbox"] = sandbox
		}
	}
	cwd := strings.TrimSpace(workspaceEffectiveActiveRoot(a.Workspace, a.Session))
	if cwd == "" {
		cwd = strings.TrimSpace(a.Session.WorkingDir)
	}
	if cwd != "" {
		metadata["cwd"] = cwd
	}
	workspaceRoot := strings.TrimSpace(workspaceEffectiveBaseRoot(a.Workspace, a.Session))
	if workspaceRoot == "" {
		workspaceRoot = strings.TrimSpace(a.Session.BaseWorkingDir)
	}
	if workspaceRoot == "" {
		workspaceRoot = strings.TrimSpace(a.Session.WorkingDir)
	}
	workspaceRoots := workspaceEffectiveRoots(a.Workspace, a.Session)
	if workspaceRoot != "" {
		metadata["workspace_root"] = workspaceRoot
	}
	if cwd != "" && workspaceRoot != "" && !samePath(cwd, workspaceRoot) {
		metadata["active_workspace_root"] = cwd
	}
	if len(workspaceRoots) > 0 {
		metadata["workspace_roots"] = workspaceRoots
	}
	if workspaces := mcpTurnMetadataWorkspaces(cwd); len(workspaces) > 0 {
		metadata["workspaces"] = workspaces
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func (a *Agent) activePermissionModeSnapshot() string {
	if a != nil && a.Workspace.Perms != nil {
		return string(a.Workspace.Perms.Mode())
	}
	if a != nil && a.Session != nil {
		return strings.TrimSpace(a.Session.PermissionMode)
	}
	return ""
}

func (a *Agent) runHook(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
	if a == nil || a.Hooks == nil {
		return HookVerdict{Allow: true}, nil
	}
	hooks := *a.Hooks
	hooks.Workspace = a.Workspace
	hooks.Session = a.Session
	hooks.Config = a.Config
	hooks.FailClosed = configHooksFailClosed(a.Config)
	hooks.Evidence = a.Evidence
	return hooks.Run(ctx, event, payload)
}

func (a *Agent) subagentStartHookPayload(agentID string, agentType string, model string, turnID string) HookPayload {
	agentID = firstNonBlankString(strings.TrimSpace(agentID), "subagent")
	agentType = firstNonBlankString(strings.TrimSpace(agentType), "specialist")
	sessionID, transcriptPath, cwd, permissionMode, model := a.subagentLifecycleHookMetadata(model)
	if strings.TrimSpace(turnID) == "" {
		turnID = a.newSubagentLifecycleTurnID(agentID)
	}

	return HookPayload{
		"agent_id":        agentID,
		"agent_type":      agentType,
		"cwd":             cwd,
		"hook_event_name": string(HookSubagentStart),
		"model":           model,
		"permission_mode": permissionMode,
		"session_id":      sessionID,
		"transcript_path": nullableHookString(transcriptPath),
		"turn_id":         strings.TrimSpace(turnID),
	}
}

func (a *Agent) subagentStopHookPayload(agentID string, agentType string, model string, lastAssistantMessage string) HookPayload {
	return a.subagentStopHookPayloadWithTurnID(agentID, agentType, model, lastAssistantMessage, "")
}

func (a *Agent) subagentStopHookPayloadWithTurnID(agentID string, agentType string, model string, lastAssistantMessage string, turnID string) HookPayload {
	return a.subagentStopHookPayloadWithTurnIDAndActive(agentID, agentType, model, lastAssistantMessage, turnID, false)
}

func (a *Agent) subagentStopHookPayloadWithTurnIDAndActive(agentID string, agentType string, model string, lastAssistantMessage string, turnID string, stopHookActive bool) HookPayload {
	agentID = firstNonBlankString(strings.TrimSpace(agentID), "subagent")
	agentType = firstNonBlankString(strings.TrimSpace(agentType), "specialist")
	lastAssistantMessage = strings.TrimSpace(lastAssistantMessage)
	sessionID, transcriptPath, cwd, permissionMode, model := a.subagentLifecycleHookMetadata(model)
	if strings.TrimSpace(turnID) == "" {
		turnID = a.newSubagentLifecycleTurnID(agentID)
	}

	return HookPayload{
		"agent_id":               agentID,
		"agent_type":             agentType,
		"agent_transcript_path":  nullableHookString(""),
		"cwd":                    cwd,
		"hook_event_name":        string(HookSubagentStop),
		"last_assistant_message": nullableHookString(lastAssistantMessage),
		"model":                  model,
		"permission_mode":        permissionMode,
		"session_id":             sessionID,
		"stop_hook_active":       stopHookActive,
		"transcript_path":        nullableHookString(transcriptPath),
		"turn_id":                strings.TrimSpace(turnID),
	}
}

func (a *Agent) runSubagentStartHook(ctx context.Context, agentID string, agentType string, model string, turnID string) ([]string, error) {
	verdict, err := a.runHook(ctx, HookSubagentStart, a.subagentStartHookPayload(agentID, agentType, model, turnID))
	if err != nil {
		return nil, err
	}
	return append([]string(nil), verdict.ContextAdds...), nil
}

func (a *Agent) runSubagentStopHook(ctx context.Context, agentID string, agentType string, model string, lastAssistantMessage string) error {
	return a.runSubagentStopHookWithTurnID(ctx, agentID, agentType, model, lastAssistantMessage, "")
}

func (a *Agent) runSubagentStopHookWithTurnID(ctx context.Context, agentID string, agentType string, model string, lastAssistantMessage string, turnID string) error {
	verdict, err := a.runSubagentStopHookVerdictWithTurnID(ctx, agentID, agentType, model, lastAssistantMessage, turnID, false)
	if err != nil {
		return err
	}
	if stopHookShouldBlock(verdict) {
		return fmt.Errorf("%s", subagentStopHookContinuationGuidance(verdict))
	}
	return nil
}

func (a *Agent) runSubagentStopHookVerdictWithTurnID(ctx context.Context, agentID string, agentType string, model string, lastAssistantMessage string, turnID string, stopHookActive bool) (HookVerdict, error) {
	return a.runHook(ctx, HookSubagentStop, a.subagentStopHookPayloadWithTurnIDAndActive(agentID, agentType, model, lastAssistantMessage, turnID, stopHookActive))
}

func (a *Agent) stopHookPayload(lastAssistantMessage string, stopHookActive bool, turnID string) HookPayload {
	lastAssistantMessage = strings.TrimSpace(lastAssistantMessage)
	sessionID, transcriptPath, cwd, permissionMode, model := a.subagentLifecycleHookMetadata("")
	if strings.TrimSpace(turnID) == "" {
		turnID = a.newStopLifecycleTurnID(time.Now())
	}

	return HookPayload{
		"cwd":                    cwd,
		"hook_event_name":        string(HookStop),
		"last_assistant_message": nullableHookString(lastAssistantMessage),
		"model":                  model,
		"permission_mode":        permissionMode,
		"session_id":             sessionID,
		"stop_hook_active":       stopHookActive,
		"transcript_path":        nullableHookString(transcriptPath),
		"turn_id":                strings.TrimSpace(turnID),
	}
}

func (a *Agent) runStopHook(ctx context.Context, lastAssistantMessage string, stopHookActive bool, turnID string) (HookVerdict, error) {
	return a.runHook(ctx, HookStop, a.stopHookPayload(lastAssistantMessage, stopHookActive, turnID))
}

func (a *Agent) subagentLifecycleHookMetadata(model string) (string, string, string, string, string) {
	model = strings.TrimSpace(model)
	sessionID := ""
	transcriptPath := ""
	cwd := ""
	permissionMode := ""
	if a != nil {
		permissionMode = strings.TrimSpace(a.activePermissionModeSnapshot())
		if permissionMode == "" {
			permissionMode = strings.TrimSpace(a.Config.PermissionMode)
		}
		cwd = strings.TrimSpace(workspaceEffectiveActiveRoot(a.Workspace, a.Session))
		if a.Session != nil {
			sessionID = strings.TrimSpace(a.Session.ID)
			if model == "" {
				model = strings.TrimSpace(a.Session.Model)
			}
			if cwd == "" {
				cwd = strings.TrimSpace(a.Session.WorkingDir)
			}
			if a.Store != nil && sessionID != "" {
				transcriptPath = a.Store.sessionPath(sessionID)
			}
		}
	}
	if permissionMode == "" {
		permissionMode = string(ModeDefault)
	}
	return sessionID, transcriptPath, cwd, permissionMode, model
}

func (a *Agent) newStopLifecycleTurnID(startedAt time.Time) string {
	if a == nil || a.Session == nil {
		return ""
	}
	sessionID := strings.TrimSpace(a.Session.ID)
	if sessionID == "" {
		return ""
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return fmt.Sprintf("%s:stop:%d", sessionID, startedAt.UnixNano())
}

func (a *Agent) newSubagentLifecycleTurnID(agentID string) string {
	if a == nil || a.Session == nil {
		return ""
	}
	sessionID := strings.TrimSpace(a.Session.ID)
	if sessionID == "" {
		return ""
	}
	safeAgentID := sanitizeFileName(agentID)
	if safeAgentID == "" {
		safeAgentID = "subagent"
	}
	return fmt.Sprintf("%s:subagent:%s:%d", sessionID, safeAgentID, time.Now().UnixNano())
}

func (a *Agent) codexParentThreadID() string {
	if a == nil || a.Session == nil {
		return ""
	}
	return strings.TrimSpace(a.Session.ID)
}

func (a *Agent) codexSubagentThreadID(agentID string, turnID string) string {
	turnID = strings.TrimSpace(turnID)
	if turnID != "" {
		return turnID
	}
	parentThreadID := a.codexParentThreadID()
	if parentThreadID == "" {
		return ""
	}
	safeAgentID := sanitizeFileName(agentID)
	if safeAgentID == "" {
		safeAgentID = "subagent"
	}
	return fmt.Sprintf("%s:subagent:%s", parentThreadID, safeAgentID)
}

func stopHookShouldBlock(verdict HookVerdict) bool {
	return strings.EqualFold(strings.TrimSpace(verdict.StopDecision), "block")
}

func stopHookContinuationGuidance(verdict HookVerdict) string {
	reason := strings.TrimSpace(verdict.StopMessage)
	if reason == "" {
		reason = strings.TrimSpace(verdict.DenyReason)
	}
	if reason == "" {
		reason = "Stop hook requested continuation before the final answer."
	}
	return "Stop hook requested continuation before the final answer. Address the following feedback, then provide a revised final answer without repeating completed work:\n\n" + reason
}

func subagentStopHookContinuationGuidance(verdict HookVerdict) string {
	reason := strings.TrimSpace(verdict.StopMessage)
	if reason == "" {
		reason = strings.TrimSpace(verdict.DenyReason)
	}
	if reason == "" {
		reason = "SubagentStop hook requested continuation before the worker can finish."
	}
	return "SubagentStop hook requested continuation before the worker can finish. Address the following feedback, then provide a revised worker answer without repeating completed work:\n\n" + reason
}

func appendSubagentHookContextGuidance(prompt string, contexts []string) string {
	if len(contexts) == 0 {
		return prompt
	}
	var parts []string
	for _, context := range contexts {
		if context = strings.TrimSpace(context); context != "" {
			parts = append(parts, context)
		}
	}
	if len(parts) == 0 {
		return prompt
	}
	prompt = strings.TrimSpace(prompt)
	return prompt + "\n\nAdditional SubagentStart hook guidance:\n- " + strings.Join(parts, "\n- ")
}

func nullableHookString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func (a *Agent) contextWithMCPToolInvocationMetadata(ctx context.Context, turnMetadata map[string]any) context.Context {
	ctx = contextWithMCPTurnMetadata(ctx, turnMetadata)
	if a == nil || a.Session == nil {
		return ctx
	}
	return contextWithMCPConversationHistory(ctx, mcpConversationHistorySnapshot(a.Session.Messages))
}

func providerTurnMetadataFromMCP(metadata map[string]any) map[string]any {
	out := cloneStringAnyMap(metadata)
	if len(out) == 0 {
		return nil
	}
	delete(out, "provider")
	delete(out, "model")
	delete(out, "reasoning_effort")
	delete(out, "service_tier")
	if len(out) == 0 {
		return nil
	}
	return out
}

func mcpTurnMetadataTurnID(sessionID string, turnStartedAt time.Time) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || turnStartedAt.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s:%d", sessionID, turnStartedAt.UnixNano())
}

func mcpTurnMetadataTraceID(sessionID string, turnStartedAt time.Time) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || turnStartedAt.IsZero() {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", sessionID, turnStartedAt.UnixNano())))
	return hex.EncodeToString(sum[:16])
}

func mcpTurnMetadataSandboxTag(permissionMode string) string {
	switch strings.ToLower(strings.TrimSpace(permissionMode)) {
	case "":
		return ""
	case "external", "external-sandbox":
		return "external"
	default:
		return "none"
	}
}

const mcpTurnMetadataGitCommandTimeout = 5 * time.Second

func mcpTurnMetadataWorkspaces(cwd string) map[string]any {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	repoRoot := gitRepositoryRootFromFilesystem(cwd)
	if repoRoot == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpTurnMetadataGitCommandTimeout)
	defer cancel()

	workspace := map[string]any{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	setWorkspaceValue := func(key string, value any) {
		mu.Lock()
		workspace[key] = value
		mu.Unlock()
	}
	wg.Add(3)
	go func() {
		defer wg.Done()
		if head, ok := mcpTurnMetadataGitText(ctx, repoRoot, "rev-parse", "HEAD"); ok && head != "" {
			setWorkspaceValue("latest_git_commit_hash", head)
		}
	}()
	go func() {
		defer wg.Done()
		if status, ok := mcpTurnMetadataGitText(ctx, repoRoot, "status", "--porcelain"); ok {
			setWorkspaceValue("has_changes", strings.TrimSpace(status) != "")
		}
	}()
	go func() {
		defer wg.Done()
		if remotes := mcpTurnMetadataRemoteURLs(ctx, repoRoot); len(remotes) > 0 {
			setWorkspaceValue("associated_remote_urls", remotes)
		}
	}()
	wg.Wait()
	if len(workspace) == 0 {
		return nil
	}
	return map[string]any{repoRoot: workspace}
}

func gitRepositoryRootFromFilesystem(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	candidate := filepath.Clean(path)
	if !filepath.IsAbs(candidate) {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return ""
		}
		candidate = abs
	}
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = resolved
	}
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		candidate = filepath.Dir(candidate)
	}
	for {
		if candidate == "" {
			return ""
		}
		if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
			return filepath.Clean(candidate)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return ""
		}
		candidate = parent
	}
}

func mcpTurnMetadataGitText(ctx context.Context, dir string, args ...string) (string, bool) {
	cmd := newGitHelperCommand(ctx, dir, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func mcpTurnMetadataRemoteURLs(ctx context.Context, repoRoot string) map[string]string {
	out, ok := mcpTurnMetadataGitText(ctx, repoRoot, "remote", "-v")
	if !ok {
		return nil
	}
	remotes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 || fields[2] != "(fetch)" {
			continue
		}
		name := strings.TrimSpace(fields[0])
		url := strings.TrimSpace(fields[1])
		if name != "" && url != "" {
			remotes[name] = url
		}
	}
	if len(remotes) == 0 {
		return nil
	}
	return remotes
}

func (a *Agent) systemPrompt() string {
	var b strings.Builder
	latestUser := latestExternalOrUserMessageText(a.Session.Messages)
	lowerLatestUser := strings.ToLower(strings.TrimSpace(latestUser))
	webResearchIntent := shouldPrioritizeWebResearchInSystemPrompt(lowerLatestUser)
	b.WriteString("You are Kernforge, a terminal-based coding agent inspired by Claude Code.\n")
	b.WriteString("Work like a careful senior engineer inside the user's repository.\n")
	b.WriteString("Use tools before making assumptions. Read relevant files before editing them. Keep answers concise and implementation-focused.\n")
	b.WriteString("When code changes are needed, prefer the smallest correct diff and verify with tests or builds when practical.\n")
	b.WriteString("When using edit tools, prefer narrow hunks anchored to current file contents; if a fix would produce a large patch, apply the first independent hunk and continue after rereading instead of generating a large tool-call payload. When a review/pre-write gate explicitly requires all RFs to be addressed, include the required RF hunks as separate narrow hunks instead of one large rewrite.\n")
	b.WriteString("If the user asks a question, answer directly before suggesting extra work.\n")
	if prefersReadOnlyAnalysisIntent(latestUser) {
		b.WriteString("The latest user request is analysis-only. Investigate and explain the issue, but do not modify files or call edit tools unless the user explicitly asks for a fix.\n")
	} else if looksLikeExplicitEditIntent(latestUser) {
		b.WriteString("The latest user request explicitly asks for a fix. Inspect the relevant code and apply the necessary edit directly with the available tools. Do not hand the patch back to the user unless an edit tool actually fails.\n")
	}
	if webResearchIntent {
		if a.MCP != nil && a.MCP.HasWebResearchCapability() {
			b.WriteString("The latest user request likely needs current external research. Prefer relevant MCP web/search/browser tools before relying on memory or local-only context.\n")
			b.WriteString("For external research tasks, first break the topic into 3-6 focused query facets, gather multiple sources, compare recency and source authority, then synthesize the results before writing files.\n")
		} else {
			b.WriteString("The latest user request likely needs current external research, but no obvious MCP web-search/browser capability is configured in this session.\n")
			b.WriteString("Do not pretend to have live web results. If current external evidence is required, explicitly say live web research is unavailable here and offer alternatives such as a no-tools best-effort answer, a smaller scoped task, or a retry with a web-capable MCP setup.\n")
		}
	}
	if !looksLikeExplicitGitIntent(latestUser) {
		b.WriteString("Do not stage, commit, push, or open a PR unless the user explicitly asks for that git action.\n")
	}
	b.WriteString("The user prompt may include an 'Auto-discovered code context' section with best-effort relevant snippets. Use it as a shortcut, but verify with tools if something looks uncertain.\n")
	b.WriteString("The user prompt may include a 'Relevant persistent memory from past sessions' section. Treat it as best-effort historical context and verify it when needed. If you rely on a memory item in your answer, cite its memory id in brackets like [mem-...].\n")
	b.WriteString("The user prompt may include a 'Relevant project analysis from past analyze-project runs' section. Treat it as a cached architecture summary derived from prior workspace analysis. Prefer using it before rereading large code areas, but verify details with tools before making edits or high-risk claims.\n")
	b.WriteString("User messages may include attached images. Use visual details from them when relevant.\n")
	b.WriteString("After successful file edits, the conversation may include an 'Automatic verification results' message, or the localized equivalent '자동 검증 결과', generated by the CLI. Use it to validate or fix your changes.\n")
	workspaceRoot := strings.TrimSpace(workspaceEffectiveActiveRoot(a.Workspace, a.Session))
	if workspaceRoot == "" {
		workspaceRoot = strings.TrimSpace(a.Session.WorkingDir)
	}
	fmt.Fprintf(&b, "Workspace root: %s\n", workspaceRoot)
	baseWorkspaceRoot := strings.TrimSpace(workspaceEffectiveBaseRoot(a.Workspace, a.Session))
	if baseWorkspaceRoot != "" && !samePath(baseWorkspaceRoot, workspaceRoot) {
		fmt.Fprintf(&b, "Workspace base root: %s\n", baseWorkspaceRoot)
	}
	if workspaceRoots := workspaceEffectiveRoots(a.Workspace, a.Session); len(workspaceRoots) > 0 {
		fmt.Fprintf(&b, "Workspace roots: %s\n", strings.Join(workspaceRoots, ", "))
	}
	if agentsMD := strings.TrimSpace(a.agentsMDPromptSection()); agentsMD != "" {
		b.WriteString("\n")
		b.WriteString(agentsMD)
		b.WriteString("\n")
	}
	b.WriteString("AGENTS.md policy: loaded instructions apply to their directory tree, nested files take precedence, and direct system/developer/user instructions override them. Before editing or creating files in a directory outside the loaded AGENTS.md scope, inspect that target path for additional AGENTS.override.md or AGENTS.md instructions and follow the most specific applicable file.\n")
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", a.Session.Provider, a.Session.Model)
	permissionMode := a.activePermissionModeSnapshot()
	if permissionMode == "" {
		permissionMode = a.Session.PermissionMode
	}
	fmt.Fprintf(&b, "Permission mode: %s\n", permissionMode)
	if profileID := activePermissionProfileIDForModeString(permissionMode); profileID != "" {
		fmt.Fprintf(&b, "Active permission profile: %s\n", profileID)
	}
	if strings.TrimSpace(a.Session.Summary) != "" {
		b.WriteString("\nConversation summary:\n")
		b.WriteString(compactPromptSection(a.Session.Summary, 900))
		b.WriteString("\n")
	}
	a.Session.RefreshConversationState()
	if stateText := strings.TrimSpace(renderConversationStatePrompt(a.Session.ConversationState)); stateText != "" {
		b.WriteString("\nActive conversation state:\n")
		b.WriteString(compactPromptSection(stateText, 1100))
		b.WriteString("\n")
	}
	if eventsText := strings.TrimSpace(renderRecentConversationEventsPrompt(recentNonUserConversationEvents(a.Session, 5), 5)); eventsText != "" {
		b.WriteString("\nRecent runtime/session events:\n")
		b.WriteString(compactPromptSection(eventsText, 1600))
		b.WriteString("\n")
	}
	if a.Session.TaskState != nil {
		if stateText := strings.TrimSpace(a.Session.TaskState.RenderPromptSection()); stateText != "" {
			b.WriteString("\nStructured task state:\n")
			b.WriteString(stateText)
			b.WriteString("\n")
		}
		if loopText := strings.TrimSpace(renderSelfDrivingWorkLoopPrompt(a.Session.TaskState)); loopText != "" {
			b.WriteString("\n")
			b.WriteString(loopText)
			b.WriteString("\n")
		}
	}
	if a.Session.TaskGraph != nil {
		if graphText := strings.TrimSpace(a.Session.TaskGraph.RenderPromptSection()); graphText != "" {
			b.WriteString("\nStructured task graph:\n")
			b.WriteString(graphText)
			b.WriteString("\n")
		}
	}
	if activeTx := currentTurnPatchTransaction(a.Session); activeTx != nil {
		if txText := strings.TrimSpace(activeTx.RenderPromptSection()); txText != "" {
			b.WriteString("\nActive patch transaction:\n")
			b.WriteString(txText)
			b.WriteString("\n")
		}
	}
	if activeLoop := promptActiveEditLoop(a.Session); activeLoop != nil {
		if loopText := strings.TrimSpace(activeLoop.RenderPromptSection()); loopText != "" {
			b.WriteString("\nActive apply/verify/retry ledger:\n")
			b.WriteString(loopText)
			b.WriteString("\n")
			if contractText := strings.TrimSpace(renderEditLoopOutcomeContractPrompt(activeLoop)); contractText != "" {
				b.WriteString("Expected final answer outcome contract:\n")
				b.WriteString(contractText)
				b.WriteString("\n")
			}
			b.WriteString("Use this ledger before final answers: connect what changed, worker evidence, verification outcome, retry state, final review, and any remaining risk in one coherent summary.\n")
		}
	}
	if a.Session.LastCodingHarnessReport != nil {
		if harnessText := strings.TrimSpace(a.Session.LastCodingHarnessReport.RenderPromptSection()); harnessText != "" {
			b.WriteString("\nLatest coding harness report:\n")
			b.WriteString(harnessText)
			b.WriteString("\n")
		}
	}
	if a.Session.LastUserChangeIsolationReport != nil {
		if isolationText := strings.TrimSpace(a.Session.LastUserChangeIsolationReport.RenderPromptSection()); isolationText != "" {
			b.WriteString("\nLatest user-change isolation report:\n")
			b.WriteString(isolationText)
			b.WriteString("\n")
		}
	}
	if a.Session.LastTestImpactReport != nil {
		if impactText := strings.TrimSpace(a.Session.LastTestImpactReport.RenderPromptSection()); impactText != "" {
			b.WriteString("\nLatest test impact report:\n")
			b.WriteString(impactText)
			b.WriteString("\n")
		}
	}
	if a.Session.LastJobSupervisorReport != nil {
		if jobText := strings.TrimSpace(a.Session.LastJobSupervisorReport.RenderPromptSection()); jobText != "" {
			b.WriteString("\nLatest job supervisor report:\n")
			b.WriteString(jobText)
			b.WriteString("\n")
		}
	}
	if a.Session.RuntimeGateLedger != nil {
		if ledgerText := strings.TrimSpace(a.Session.RuntimeGateLedger.RenderPromptSection()); ledgerText != "" {
			b.WriteString("\nRuntime gate ledger:\n")
			b.WriteString(ledgerText)
			b.WriteString("\n")
		}
	}
	if len(a.Session.Plan) > 0 {
		b.WriteString("\nCurrent shared plan:\n")
		for _, item := range a.Session.Plan {
			fmt.Fprintf(&b, "- [%s] %s\n", item.Status, item.Step)
		}
	}
	if jobsText := strings.TrimSpace(renderBackgroundJobsPrompt(a.Session.BackgroundJobs, a.Session.WorkingDir)); jobsText != "" {
		b.WriteString("\nActive background shell jobs:\n")
		b.WriteString(jobsText)
		b.WriteString("\n")
	}
	if bundlesText := strings.TrimSpace(renderBackgroundBundlesPrompt(a.Session.BackgroundBundles)); bundlesText != "" {
		b.WriteString("\nActive background shell bundles:\n")
		b.WriteString(bundlesText)
		b.WriteString("\n")
	}
	if a.Session.LastVerification != nil {
		b.WriteString("\nLatest verification summary:\n")
		if a.Session.LastVerification.HasFailures() {
			b.WriteString(compactPromptSection(a.Session.LastVerification.RenderShort(), 500))
		} else {
			b.WriteString(a.Session.LastVerification.SummaryLine())
		}
		b.WriteString("\n")
	}
	if a.Session.AcceptanceContract != nil {
		if contractText := strings.TrimSpace(a.Session.AcceptanceContract.RenderPromptSection()); contractText != "" {
			b.WriteString("\nAcceptance contract for this turn:\n")
			b.WriteString(contractText)
			b.WriteString("\n")
		}
	}
	if a.Session.ActiveFailureRepair != nil {
		if repairText := strings.TrimSpace(a.Session.ActiveFailureRepair.RenderPromptSection()); repairText != "" {
			b.WriteString("\nActive failure repair harness:\n")
			b.WriteString(repairText)
			b.WriteString("\n")
		}
	}
	if combined := strings.TrimSpace(a.Memory.Combined()); combined != "" {
		b.WriteString("\nLoaded memory files:\n")
		b.WriteString(compactPromptSection(combined, 700))
		b.WriteString("\n")
	}
	if shouldIncludeSkillCatalogInSystemPrompt(lowerLatestUser) {
		if catalog := strings.TrimSpace(a.Skills.CatalogPrompt()); catalog != "" {
			b.WriteString("\nAvailable local skills:\n")
			b.WriteString(compactPromptSection(catalog, 1200))
			b.WriteString("\n")
		}
	}
	if defaults := strings.TrimSpace(renderEnabledSkillSummary(a.Skills)); defaults != "" {
		b.WriteString("\nEnabled local skills:\n")
		b.WriteString(defaults)
		b.WriteString("\n")
	}
	if shouldIncludeMCPCatalogInSystemPrompt(lowerLatestUser) {
		if resources := strings.TrimSpace(a.MCP.ResourceCatalogPrompt()); resources != "" {
			b.WriteString("\nAvailable MCP resources:\n")
			b.WriteString(compactPromptSection(resources, 1000))
			b.WriteString("\n")
		}
		if prompts := strings.TrimSpace(a.MCP.PromptCatalogPrompt()); prompts != "" {
			b.WriteString("\nAvailable MCP prompts:\n")
			b.WriteString(compactPromptSection(prompts, 900))
			b.WriteString("\n")
		}
	}
	if webResearchIntent {
		if catalog := strings.TrimSpace(a.MCP.WebResearchCatalogPrompt()); catalog != "" {
			b.WriteString("\nRelevant MCP web/research capabilities:\n")
			b.WriteString(compactPromptSection(catalog, 1200))
			b.WriteString("\n")
		}
	}

	if instruction := responseLanguageInstructionForUserText(latestUser, a.Config); instruction != "" {
		fmt.Fprintf(&b, "\nResponse language policy: %s\n", instruction)
	}

	b.WriteString("\nTool rules:\n")
	b.WriteString("- Prefer read_file, list_files, grep, and git tools to inspect the codebase.\n")
	b.WriteString("- Prefer apply_patch for precise edits to existing files.\n")
	b.WriteString("- Before editing a file, read that exact file path first unless the current contents were already read very recently in this turn.\n")
	b.WriteString("- If read_file returns a NOTE about cached content, treat that as evidence you already have the relevant lines. Do not reread the same range unless the file likely changed or a missing adjacent range is still required.\n")
	b.WriteString("- If grep results include [cached-nearby:inside] or [cached-nearby:N], prefer a narrowly targeted next read around the unmatched nearby lines instead of rereading a large surrounding range.\n")
	b.WriteString("- When using apply_patch, the patch argument must be raw patch text that starts with *** Begin Patch and ends with *** End Patch.\n")
	b.WriteString("- Every *** Update File: section in apply_patch must contain at least one hunk with context and/or +/- lines. Use @@ markers for anchored edits and never send an update file section with no hunks.\n")
	b.WriteString("- Never send JSON, markdown code fences, prose, or pseudo-objects as the apply_patch patch string.\n")
	b.WriteString("- Use replace_in_file only for very small exact substitutions when you have just read the same file path and the exact search text is present exactly as written.\n")
	b.WriteString("- If there is any risk that the file changed, the path is ambiguous, or the replacement spans multiple lines or repeated matches, read the file again and use apply_patch instead of replace_in_file.\n")
	b.WriteString("- If an edit fails because search text or patch context is not found, do not repeat the same edit. Re-read the file from the same path and build a fresh edit.\n")
	b.WriteString("- Use write_file for creating new files or fully rewriting a file when necessary.\n")
	b.WriteString("- Do not use write_file for small edits to existing files. Read the file and use apply_patch instead.\n")
	b.WriteString("- Tool arguments must be complete valid JSON. Never send truncated JSON, partial strings, or unfinished objects.\n")
	b.WriteString("- Use update_plan for multi-step tasks.\n")
	b.WriteString("- Use run_shell for build, test, or local inspection commands when no dedicated workspace tool fits.\n")
	b.WriteString("- Prefer dedicated workspace tools such as read_file, grep, git_diff, git_status, and list_files for code, diff, and git-state inspection. Do not use run_shell with Get-Content or PowerShell pipelines just to print line numbers or file excerpts.\n")
	b.WriteString("- Do not use run_shell with Set-Content, Out-File, .NET file APIs such as WriteAllText, redirection, or inline scripts to modify existing source files; use apply_patch or replace_in_file so edits stay reviewable and encoding-safe.\n")
	b.WriteString("- For scoped mutating shell commands, only use run_shell with allow_workspace_writes=true and write_paths when a formatter, code generator, or setup command is clearly safer than a manual patch.\n")
	b.WriteString("- Use run_shell_background for a single long-running build, test, or verification command that may take multiple minutes.\n")
	b.WriteString("- Use run_shell_bundle_background when multiple independent build, test, or verification commands can run in parallel.\n")
	b.WriteString("- Use check_shell_job to poll a background shell job instead of rerunning the same long command.\n")
	b.WriteString("- Use check_shell_bundle to poll several background shell jobs together when a parallel verification bundle is running.\n")
	b.WriteString("- If a build, test, or verification command is skipped or declined, do not retry the same verification command or poll a background job for it in this turn. Report that verification was not run unless the user explicitly approves running it.\n")
	b.WriteString("- When a background shell bundle already exists, prefer check_shell_bundle with bundle_id=\"latest\" instead of reconstructing the job id list from memory.\n")
	b.WriteString("- When a background job or bundle becomes obsolete after newer edits or a newer verification run, use cancel_shell_job or cancel_shell_bundle instead of leaving stale work running.\n")
	b.WriteString("- Include owner_node_id only when the current focused task-graph node has explicit editable ownership or a concrete worktree lease for the exact work being done. If unsure, omit owner_node_id; the harness will keep the edit inside the active workspace root.\n")
	b.WriteString("- For run_shell on Windows PowerShell, do not use && or ||. Use a single command or PowerShell separators and conditionals only when needed.\n")
	b.WriteString("- For run_shell on Windows PowerShell, do not use Unix shell syntax like find ... -type, chmod, chown, ls -la, or /dev/null redirection, and do not use cmd.exe batch syntax like for /d %x. Rewrite those commands with PowerShell cmdlets, or explicitly invoke cmd /c or bash -lc only when that interpreter is intentionally required.\n")
	b.WriteString("- For run_shell and background shell tools, set the workdir argument when a command should run in a subdirectory. Do not prepend commands with cd unless the command itself truly needs shell-local directory changes.\n")
	b.WriteString("- When the user asks to create or update ordinary source files or documents, prefer edit tools. Do not use run_shell for repo bootstrap, ACL changes, or git init unless the user explicitly asked for setup work.\n")
	b.WriteString("- For document or report authoring tasks, do not assume generated files already exist. Use list_files on the parent directory before read_file. If the directory is empty or the file is absent, treat the document as not created yet and create or update it with edit tools.\n")
	b.WriteString("- For latest/current external research tasks, prefer relevant MCP web/search/browser tools before answering from memory. Gather multiple sources, compare recency and authority, then synthesize.\n")
	b.WriteString("- For local code review or repair tasks, do not use MCP web/search/browser tools unless the user explicitly asks for external web research. Rely on local source evidence and review artifacts first.\n")
	b.WriteString("- When a background job is already running for the same command, prefer polling it instead of starting a duplicate.\n")
	b.WriteString("- Do not use git_add, git_commit, git_push, or git_create_pr unless the user explicitly asks for a git action.\n")
	b.WriteString("- Local skills can be referenced by name with $skill-name.\n")
	b.WriteString("- MCP tool names from servers are prefixed as mcp__server__tool.\n")
	b.WriteString("- Use mcp__resource__server to read a listed MCP resource.\n")
	b.WriteString("- Use mcp__prompt__server to resolve a listed MCP prompt.\n")
	return b.String()
}

func compactPromptSection(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		var b strings.Builder
		for _, r := range text {
			size := utf8.RuneLen(r)
			if size < 0 {
				size = 1
			}
			if b.Len()+size > limit {
				break
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	budget := limit - 3
	end := 0
	for idx, r := range text {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 1
		}
		if idx+size > budget {
			break
		}
		end = idx + size
	}
	if end == 0 {
		return "..."
	}
	truncated := text[:end]
	if lineEnd := strings.LastIndexAny(truncated, "\r\n"); lineEnd > 0 {
		return strings.TrimSpace(truncated[:lineEnd]) + "\n..."
	}
	return strings.TrimSpace(truncated) + "..."
}

func renderEnabledSkillSummary(c SkillCatalog) string {
	if len(c.enabled) == 0 {
		return ""
	}
	lines := make([]string, 0, len(c.enabled))
	for _, skill := range c.enabled {
		summary := strings.TrimSpace(skill.Summary)
		if summary == "" {
			summary = "No summary available."
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", skill.Name, summary))
	}
	return strings.Join(lines, "\n")
}

func shouldIncludeSkillCatalogInSystemPrompt(lowerLatestUser string) bool {
	if strings.TrimSpace(lowerLatestUser) == "" {
		return false
	}
	if explicitSkillPattern.MatchString(lowerLatestUser) {
		return true
	}
	return containsAny(lowerLatestUser, "skill", "skills", "스킬")
}

func shouldIncludeMCPCatalogInSystemPrompt(lowerLatestUser string) bool {
	if strings.TrimSpace(lowerLatestUser) == "" {
		return false
	}
	return containsAny(lowerLatestUser, "mcp", "resource", "resources", "prompt", "prompts", "리소스", "프롬프트")
}

func persistentMemoryPromptPolicyForRequest(userText string) PersistentMemoryPromptPolicy {
	base := strings.ToLower(strings.TrimSpace(baseUserQueryText(userText)))
	policy := PersistentMemoryPromptPolicy{
		IncludeContinuity:   true,
		IncludeQueryMatches: true,
	}
	if base == "" {
		return policy
	}
	if requestExplicitlyAsksForPersistentMemory(base) {
		return policy
	}
	if classifyTurnIntent(base) == TurnIntentContinueLastTask {
		return policy
	}
	if requestLooksLikeFreshExecutionTask(base) {
		policy.IncludeContinuity = false
		if looksLikeReviewArtifactAuthoringRequest(base) {
			policy.IncludeQueryMatches = false
		}
	}
	return policy
}

func requestLooksLikeFreshExecutionTask(lowerLatestUser string) bool {
	lowerLatestUser = strings.ToLower(strings.TrimSpace(lowerLatestUser))
	if lowerLatestUser == "" {
		return false
	}
	if requestLooksLikeLocalCodeWork(lowerLatestUser) ||
		requestLooksLikeLocalVerificationWork(lowerLatestUser) ||
		looksLikeDocumentAuthoringIntent(lowerLatestUser) ||
		looksLikeExplicitGitIntent(lowerLatestUser) ||
		looksLikeExplicitEditIntent(lowerLatestUser) {
		return true
	}
	return false
}

func requestExplicitlyAsksForPersistentMemory(lowerLatestUser string) bool {
	lowerLatestUser = strings.ToLower(strings.TrimSpace(lowerLatestUser))
	if lowerLatestUser == "" {
		return false
	}
	return containsAny(lowerLatestUser,
		"persistent memory", "workspace memory", "project memory", "memory context",
		"memory record", "memory records", "memory search", "/mem", "mem-",
		"remember from", "recall from", "past session", "previous session", "prior session",
		"previous work", "prior work", "past work", "prior context", "previous context",
		"메모리 참고", "메모리에서", "메모리 기록", "메모리 검색", "워크스페이스 메모리",
		"프로젝트 메모리", "기억해", "기억나", "기억하고", "지난 세션", "이전 세션",
		"이전 작업", "지난 작업", "과거 작업",
	)
}

func shouldPrioritizeWebResearchInSystemPrompt(lowerLatestUser string) bool {
	if strings.TrimSpace(lowerLatestUser) == "" {
		return false
	}
	if requestLooksLikeLocalCodeWork(lowerLatestUser) && !requestExplicitlyAsksForWebResearch(lowerLatestUser) {
		return false
	}
	if containsAny(lowerLatestUser,
		"latest", "recent", "current", "today", "now", "news", "trend", "trends",
		"web", "search", "browse", "browser", "citation", "citations",
		"research", "survey", "state of the art", "look up", "find sources",
		"최신", "최근", "현재", "뉴스", "동향", "웹", "검색", "출처", "자료", "리서치", "조사", "논문",
	) {
		return true
	}
	return false
}

func requestLooksLikeLocalCodeWork(lowerLatestUser string) bool {
	lowerLatestUser = strings.ToLower(strings.TrimSpace(lowerLatestUser))
	if lowerLatestUser == "" {
		return false
	}
	if containsAny(lowerLatestUser,
		"automatic pre-write review", "pre-write review", "edit proposal", "proposed edit",
		"review finding", "review findings", "repair guidance", "review gate",
		"자동 쓰기 전 리뷰", "쓰기 전 리뷰", "수정 전 리뷰", "리뷰 finding", "리뷰 경고",
		"수정 지침", "리뷰 게이트",
	) {
		return true
	}
	hasPathOrCodeToken := strings.Contains(lowerLatestUser, "@") ||
		containsAny(lowerLatestUser,
			".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hxx",
			".cs", ".rs", ".py", ".js", ".ts", ".tsx", ".jsx", ".java",
			".kt", ".swift", ".sln", ".vcxproj", "read_file", "apply_patch",
			"git_diff", "source/", "src/", "cmd/", "internal/", "plugins/",
		)
	hasCodeIntent := containsAny(lowerLatestUser,
		"code", "source", "file", "path", "review", "inspect", "audit", "fix", "bug", "patch",
		"코드", "소스", "파일", "경로", "검토", "리뷰", "수정", "버그", "패치",
	)
	hasCodeAnalysisIntent := containsAny(lowerLatestUser, "code", "source code", "코드", "소스") &&
		containsAny(lowerLatestUser, "analyze", "analyse", "analysis", "review", "inspect", "audit", "fix", "bug", "분석", "검토", "리뷰", "수정", "버그")
	hasWorkspaceFileAuditIntent := containsAny(lowerLatestUser, "file", "files", "파일", "파일들") &&
		containsAny(lowerLatestUser, "analyze", "analyse", "analysis", "review", "inspect", "audit", "problem", "problems", "issue", "issues", "bug", "bugs", "분석", "검토", "리뷰", "문제", "문제점", "버그") &&
		containsAny(lowerLatestUser, "document", "report", "write-up", "writeup", "문서", "보고서", "정리")
	return (hasPathOrCodeToken && hasCodeIntent) || hasCodeAnalysisIntent || hasWorkspaceFileAuditIntent
}

func requestLooksLikeLocalVerificationWork(lowerLatestUser string) bool {
	lowerLatestUser = strings.ToLower(strings.TrimSpace(lowerLatestUser))
	if lowerLatestUser == "" {
		return false
	}
	return containsAny(lowerLatestUser,
		"verification command", "verify command", "run verification", "run tests", "run test", "run build", "build command", "test command",
		"verify", "validation", "validate", "smoke test", "test it", "build it",
		"검증 명령", "검증 실행", "검증해", "검증하", "빌드 명령", "빌드 실행", "빌드해", "빌드하", "테스트 명령", "테스트 실행", "테스트해", "테스트하",
	)
}

func requestExplicitlyAsksForWebResearch(lowerLatestUser string) bool {
	lowerLatestUser = strings.ToLower(strings.TrimSpace(lowerLatestUser))
	if lowerLatestUser == "" {
		return false
	}
	if strings.Contains(lowerLatestUser, "this is a local code review or repair request") ||
		strings.Contains(lowerLatestUser, "this is still local code review/repair work") ||
		strings.Contains(lowerLatestUser, "continue with local source evidence") ||
		strings.Contains(lowerLatestUser, "do not use mcp web") ||
		strings.Contains(lowerLatestUser, "do not use mcp web/search/browser") ||
		strings.Contains(lowerLatestUser, "do not use web/search/browser") ||
		strings.Contains(lowerLatestUser, "do not use external web research") ||
		strings.Contains(lowerLatestUser, "외부 웹 리서치를 명시적으로 요청하지 않는 한") ||
		strings.Contains(lowerLatestUser, "mcp web/search/browser 도구를 사용하지") ||
		strings.Contains(lowerLatestUser, "외부 웹 리서치를 사용하지") {
		return false
	}
	return containsAny(lowerLatestUser,
		"web search", "search web", "search the web", "browse web", "web browser", "internet", "online",
		"look up online", "find online sources", "external source", "external sources",
		"웹 검색", "웹에서", "웹 브라우저", "인터넷", "온라인", "외부 자료", "외부 출처",
	)
}

func summarizeMessages(messages []Message, instructions string) string {
	var lines []string
	if strings.TrimSpace(instructions) != "" {
		lines = append(lines, "Focus: "+strings.TrimSpace(instructions))
	}
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			text := summarizeToolTurnForCompact(messages, i)
			if strings.TrimSpace(text) != "" {
				lines = append(lines, fmt.Sprintf("[%s] %s", msg.Role, text))
			}
			i += countFollowingToolMessages(messages, i)
			continue
		}
		if !messageShouldAppearInCompactSummary(msg) {
			continue
		}
		text := compactMessageSummaryText(msg)
		if text == "" && len(msg.Images) > 0 {
			text = fmt.Sprintf("attached %d image(s)", len(msg.Images))
		}
		if text == "" && len(msg.WebSearchCalls) > 0 {
			text = fmt.Sprintf("web search call(s): %d", len(msg.WebSearchCalls))
		}
		if text == "" && len(msg.LocalShellCalls) > 0 {
			text = fmt.Sprintf("local shell call(s): %d", len(msg.LocalShellCalls))
		}
		if text == "" && len(msg.CodexCompactionItems) > 0 {
			text = fmt.Sprintf("codex compaction item(s): %d", len(msg.CodexCompactionItems))
		}
		if text == "" && len(msg.CodexToolOutputItems) > 0 {
			text = fmt.Sprintf("codex tool output item(s): %d", len(msg.CodexToolOutputItems))
		}
		if text == "" && len(msg.ToolCalls) > 0 {
			var names []string
			for _, call := range msg.ToolCalls {
				names = append(names, call.Name)
			}
			text = "tool calls: " + strings.Join(names, ", ")
		}
		if text == "" {
			continue
		}
		limit := 220
		if messageShouldPinForCompact(msg) {
			limit = 420
		}
		if len(text) > limit {
			text = text[:limit] + "..."
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", msg.Role, strings.ReplaceAll(text, "\n", " ")))
	}
	if len(lines) == 0 {
		return "No prior summary available."
	}
	return strings.Join(lines, "\n")
}

func compactCutIndex(messages []Message, keepRecentMessages int, keepRecentToolTurns int) int {
	if keepRecentMessages <= 0 {
		keepRecentMessages = 8
	}
	if len(messages) <= keepRecentMessages {
		return 0
	}
	cut := len(messages) - keepRecentMessages
	if keepRecentToolTurns <= 0 {
		return cut
	}
	toolTurnStarts := make([]int, 0, keepRecentToolTurns)
	for index, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			toolTurnStarts = append(toolTurnStarts, index)
		}
	}
	if len(toolTurnStarts) >= keepRecentToolTurns {
		preserveStart := toolTurnStarts[len(toolTurnStarts)-keepRecentToolTurns]
		if preserveStart < cut {
			cut = preserveStart
		}
	}
	if pinnedStart := compactPinnedStartIndex(messages, compactPinnedMessagesToKeep); pinnedStart >= 0 && pinnedStart < cut {
		cut = pinnedStart
	}
	if cut < 0 {
		return 0
	}
	return cut
}

func compactRetainedMessageCharBudget(autoCompactChars int) int {
	budget := compactRetainedMessageTokenBudget * compactApproxCharsPerToken
	if autoCompactChars <= 0 || autoCompactChars >= budget {
		return budget
	}
	derived := autoCompactChars / 2
	if derived <= 0 {
		return autoCompactChars
	}
	if derived < compactMinRetainedMessageCharBudget {
		if autoCompactChars < compactMinRetainedMessageCharBudget {
			return autoCompactChars
		}
		return compactMinRetainedMessageCharBudget
	}
	return derived
}

func compactRetainedMessagesWithinBudget(messages []Message, maxChars int) ([]Message, int) {
	if len(messages) == 0 {
		return nil, 0
	}
	if maxChars <= 0 {
		return append([]Message(nil), messages...), 0
	}
	remaining := maxChars
	retainedReversed := make([]Message, 0, len(messages))
	firstRetained := len(messages)
	firstRetainedTruncated := false
	for i := len(messages) - 1; i >= 0; i-- {
		if remaining <= 0 {
			break
		}
		msg := messages[i]
		if !messageShouldRetainAfterCompact(msg) {
			continue
		}
		cost := compactMessageRetainedCharCost(msg)
		if cost <= 0 {
			cost = 1
		}
		if cost <= remaining {
			retainedReversed = append(retainedReversed, msg)
			firstRetained = i
			remaining -= cost
			continue
		}
		truncated, ok := compactTruncateMessageToCharBudget(msg, remaining)
		if ok {
			retainedReversed = append(retainedReversed, truncated)
			firstRetained = i
			firstRetainedTruncated = true
		}
		remaining = 0
		break
	}
	if len(retainedReversed) == 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			latest := messages[i]
			if !messageShouldRetainAfterCompact(latest) {
				continue
			}
			if truncated, ok := compactTruncateMessageToCharBudget(latest, maxChars); ok {
				retainedReversed = append(retainedReversed, truncated)
				firstRetained = i
				firstRetainedTruncated = true
			}
			break
		}
	}
	retained := make([]Message, len(retainedReversed))
	for i := range retainedReversed {
		retained[len(retainedReversed)-1-i] = retainedReversed[i]
	}
	retained = compactDropOrphanToolMessages(retained)
	summarizePrefix := firstRetained
	if firstRetainedTruncated && summarizePrefix < len(messages) {
		summarizePrefix++
	}
	if summarizePrefix < 0 {
		summarizePrefix = 0
	}
	if summarizePrefix > len(messages) {
		summarizePrefix = len(messages)
	}
	return retained, summarizePrefix
}

func compactDropOrphanToolMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}
	expected := map[string]bool{}
	expectedSets := newOpenAICodexExpectedOutputSets()
	for _, msg := range messages {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		for _, call := range msg.ToolCalls {
			callID := firstNonEmptyTrimmed(call.ID, call.Name)
			if callID != "" {
				expected[callID] = true
				expectedSets.markToolCall(callID, call)
			}
		}
		for _, call := range msg.LocalShellCalls {
			callID := firstNonEmptyTrimmed(call.CallID, call.ID)
			if callID != "" {
				expected[callID] = true
				expectedSets.markLocalShell(callID)
			}
		}
	}

	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			msg.CodexToolOutputItems = filterOpenAICodexAssistantToolOutputItems(msg.CodexToolOutputItems, expectedSets)
			out = append(out, msg)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			callID := firstNonEmptyTrimmed(msg.ToolCallID, msg.ToolName)
			if callID == "" || !expected[callID] {
				continue
			}
			out = append(out, msg)
			continue
		}
		out = append(out, msg)
	}
	return out
}

func compactMessageRetainedCharCost(msg Message) int {
	total := len(msg.Text) + len(msg.ReasoningContent) + encryptedReasoningApproxChars(len(msg.ReasoningEncryptedContent))
	total += len(msg.ToolCallID) + len(msg.ToolName)
	for _, call := range msg.WebSearchCalls {
		total += len(call.Status)
		total += compactActionCharCost(call.Action)
	}
	for _, call := range msg.LocalShellCalls {
		total += len(call.ID) + len(call.CallID) + len(call.Status)
		total += compactActionCharCost(call.Action)
	}
	for _, item := range msg.CodexCompactionItems {
		total += encryptedReasoningApproxChars(len(item.EncryptedContent))
	}
	for _, item := range msg.CodexToolOutputItems {
		total += len(item.Type) + len(item.CallID) + len(item.Name) + len(item.Status) + len(item.Execution) + len(item.Text)
		for _, content := range item.ToolContentItems {
			total += toolContentItemApproxChars(content)
		}
		for _, tool := range item.Tools {
			total += compactActionCharCost(tool)
		}
	}
	for _, call := range msg.ToolCalls {
		total += len(call.ID) + len(call.Name) + len(call.Arguments)
	}
	for _, item := range msg.ToolContentItems {
		total += toolContentItemApproxChars(item)
	}
	if len(msg.Images) > 0 {
		for _, image := range msg.Images {
			total += messageImageApproxChars("", image)
		}
	}
	if total == 0 && messageHasStructuredHistoryItems(msg) {
		return 1
	}
	return total
}

func compactActionCharCost(action map[string]any) int {
	if len(action) == 0 {
		return 0
	}
	if encoded, err := json.Marshal(action); err == nil {
		return len(encoded)
	}
	total := 0
	for key := range action {
		total += len(key)
	}
	return total
}

func messageHasStructuredHistoryItems(msg Message) bool {
	return len(msg.Images) > 0 ||
		len(msg.WebSearchCalls) > 0 ||
		len(msg.LocalShellCalls) > 0 ||
		len(msg.CodexCompactionItems) > 0 ||
		len(msg.CodexToolOutputItems) > 0 ||
		len(msg.ToolCalls) > 0 ||
		len(msg.ToolContentItems) > 0
}

func messageShouldRetainAfterCompact(msg Message) bool {
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	switch role {
	case "developer", "system":
		return false
	case "user":
		return !messageLooksLikeCompactContextOnly(msg)
	case "assistant", "tool":
		return true
	default:
		return false
	}
}

func messageLooksLikeCompactContextOnly(msg Message) bool {
	if messageHasStructuredHistoryItems(msg) {
		return false
	}
	text := strings.TrimSpace(strings.ReplaceAll(msg.Text, "\r\n", "\n"))
	if text == "" {
		return false
	}
	return compactWrappedSectionOnly(text, "[Conversation Runtime Context]", "[/Conversation Runtime Context]") ||
		compactWrappedSectionOnly(text, "<environment_context>", "</environment_context>") ||
		compactWrappedSectionOnly(text, "<goal_context>", "</goal_context>")
}

func compactWrappedSectionOnly(text string, start string, end string) bool {
	text = strings.TrimSpace(text)
	lowerText := strings.ToLower(text)
	lowerStart := strings.ToLower(strings.TrimSpace(start))
	lowerEnd := strings.ToLower(strings.TrimSpace(end))
	if !strings.HasPrefix(lowerText, lowerStart) || !strings.HasSuffix(lowerText, lowerEnd) {
		return false
	}
	startLen := len(strings.TrimSpace(start))
	endLen := len(strings.TrimSpace(end))
	if len(text) < startLen+endLen {
		return false
	}
	inner := strings.TrimSpace(text[startLen : len(text)-endLen])
	return inner != ""
}

func compactTruncateMessageToCharBudget(msg Message, maxChars int) (Message, bool) {
	if maxChars <= 0 {
		return Message{}, false
	}
	if len(msg.ToolCalls) > 0 {
		return Message{}, false
	}
	remaining := maxChars
	truncated := msg
	if compactConsumeTextBudget(&truncated.Text, &remaining) {
		return truncated, true
	}
	if compactConsumeTextBudget(&truncated.ReasoningContent, &remaining) {
		return truncated, true
	}
	for i := range truncated.ToolContentItems {
		if compactConsumeTextBudget(&truncated.ToolContentItems[i].Text, &remaining) {
			return truncated, true
		}
		if compactConsumeTextBudget(&truncated.ToolContentItems[i].EncryptedContent, &remaining) {
			return truncated, true
		}
	}
	for i := range truncated.CodexToolOutputItems {
		if compactConsumeTextBudget(&truncated.CodexToolOutputItems[i].Text, &remaining) {
			return truncated, true
		}
		for j := range truncated.CodexToolOutputItems[i].ToolContentItems {
			if compactConsumeTextBudget(&truncated.CodexToolOutputItems[i].ToolContentItems[j].Text, &remaining) {
				return truncated, true
			}
			if compactConsumeTextBudget(&truncated.CodexToolOutputItems[i].ToolContentItems[j].EncryptedContent, &remaining) {
				return truncated, true
			}
		}
	}
	if messageHasStructuredHistoryItems(truncated) {
		return truncated, true
	}
	return Message{}, false
}

func compactConsumeTextBudget(text *string, remaining *int) bool {
	if text == nil || remaining == nil {
		return false
	}
	value := strings.TrimSpace(*text)
	if value == "" {
		*text = ""
		return false
	}
	if *remaining <= 0 {
		*text = ""
		return false
	}
	if len(value) <= *remaining {
		*text = value
		*remaining -= len(value)
		return false
	}
	*text = compactPromptSection(value, *remaining)
	*remaining = 0
	return true
}

func countFollowingToolMessages(messages []Message, assistantIndex int) int {
	count := 0
	for i := assistantIndex + 1; i < len(messages); i++ {
		if messages[i].Role != "tool" {
			break
		}
		count++
	}
	return count
}

func summarizeToolTurnForCompact(messages []Message, assistantIndex int) string {
	msg := messages[assistantIndex]
	parts := make([]string, 0, len(msg.ToolCalls))
	toolResults := collectToolMessages(messages, assistantIndex, len(msg.ToolCalls))
	for i, call := range msg.ToolCalls {
		name := summarizeToolDiagnosticCall(call)
		status := "pending"
		if i < len(toolResults) {
			status = summarizeCompactToolResult(toolResults[i])
		}
		parts = append(parts, sanitizeDiagnosticValue(name+":"+status))
	}
	preamble := strings.TrimSpace(msg.Text)
	toolSummary := strings.Join(parts, ", ")
	if preamble == "" {
		return "tool turn: " + toolSummary
	}
	return sanitizeDiagnosticValue(strings.ReplaceAll(preamble, "\n", " ")) + " | tool turn: " + toolSummary
}

func collectToolMessages(messages []Message, assistantIndex, expected int) []Message {
	results := make([]Message, 0, expected)
	for i := assistantIndex + 1; i < len(messages) && len(results) < expected; i++ {
		msg := messages[i]
		if msg.Role == "tool" {
			results = append(results, msg)
			continue
		}
		break
	}
	return results
}

func summarizeCompactToolResult(msg Message) string {
	status := summarizeToolResultStatus(msg)
	if !msg.IsError {
		return status
	}
	detail := compactToolErrorDetail(msg.Text)
	if detail == "" {
		return status
	}
	return status + ":" + sanitizeDiagnosticValue(detail)
}

func compactMessageSummaryText(msg Message) string {
	if msg.Role == "tool" {
		return summarizeCompactToolResult(msg)
	}
	text := strings.TrimSpace(msg.Text)
	if !messageShouldPinForCompact(msg) {
		return text
	}
	return compactPinnedMessageSnippet(text, 6)
}

func messageShouldAppearInCompactSummary(msg Message) bool {
	if messageLooksLikeCompactContextOnly(msg) {
		return false
	}
	role := strings.TrimSpace(strings.ToLower(msg.Role))
	if role != "assistant" {
		return true
	}
	if messageHasStructuredHistoryItems(msg) {
		return true
	}
	return false
}

func messageShouldPinForCompact(msg Message) bool {
	text := strings.ToLower(strings.TrimSpace(msg.Text))
	if msg.Role == "tool" {
		if msg.IsError {
			return true
		}
		if strings.HasPrefix(strings.TrimSpace(msg.Text), "IN_PROGRESS:") {
			return true
		}
		if strings.TrimSpace(msg.ToolName) == "run_shell" && strings.Contains(text, "[run_shell output truncated") {
			return true
		}
		return false
	}
	if msg.Role != "user" {
		return false
	}
	return containsAny(text,
		"automatic verification results",
		"latest verification failed",
		"automatic verification has been disabled",
		"verification tool path updated",
		"recovery mode:",
		"tool budget is extended",
		"tool budget has been exhausted",
		"wrong patch format",
		"malformed or truncated json",
		"stale or mismatched file contents",
	)
}

func compactPinnedStartIndex(messages []Message, keepPinnedMessages int) int {
	if keepPinnedMessages <= 0 || len(messages) == 0 {
		return -1
	}
	indices := make([]int, 0, keepPinnedMessages)
	seen := map[int]struct{}{}
	for idx, msg := range messages {
		if !messageShouldPinForCompact(msg) {
			continue
		}
		start := compactPinnedMessageStart(messages, idx)
		if _, ok := seen[start]; ok {
			continue
		}
		seen[start] = struct{}{}
		indices = append(indices, start)
	}
	if len(indices) == 0 {
		return -1
	}
	if len(indices) <= keepPinnedMessages {
		return indices[0]
	}
	return indices[len(indices)-keepPinnedMessages]
}

func compactPinnedMessageStart(messages []Message, index int) int {
	if index <= 0 || index >= len(messages) {
		return index
	}
	if messages[index].Role != "tool" {
		return index
	}
	for i := index - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && len(messages[i].ToolCalls) > 0 {
			return i
		}
		if messages[i].Role != "tool" {
			break
		}
	}
	return index
}

func compactPinnedMessageSnippet(text string, maxLines int) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	if normalized == "" {
		return ""
	}
	lines := strings.Split(normalized, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		filtered = append(filtered, trimmed)
		if maxLines > 0 && len(filtered) >= maxLines {
			break
		}
	}
	return strings.Join(filtered, " | ")
}

func compactToolErrorDetail(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	if idx := strings.LastIndex(normalized, "ERROR:"); idx >= 0 {
		return firstLine(normalized[idx+len("ERROR:"):])
	}
	return firstLine(normalized)
}

func synthesizeToolPreambleText(calls []ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	switch calls[0].Name {
	case "run_shell":
		return "Let me check the current state first."
	case "run_shell_background":
		return "This may take a while, so I am starting it in the background first."
	case "run_shell_bundle_background":
		return "These can run independently, so I am starting them in parallel in the background."
	case "check_shell_job":
		return "Let me poll the background job and see where it is."
	case "check_shell_bundle":
		return "Let me poll the background job bundle and see how far it has progressed."
	case "cancel_shell_job":
		return "This background job is stale, so I am stopping it before it wastes more time."
	case "cancel_shell_bundle":
		return "This background bundle is stale, so I am stopping it before it wastes more time."
	case "apply_patch":
		return "I prepared a patch. I will show the diff before applying it."
	case "write_file":
		return "I am going to update the file. I will show the change first."
	case "replace_in_file":
		return "This looks like a small targeted edit. I will show the change before applying it."
	default:
		return ""
	}
}

func synthesizeToolPreamble(calls []ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	switch calls[0].Name {
	case "run_shell":
		return "먼저 현재 상태를 확인해볼게요."
	case "run_shell_background":
		return "시간이 걸릴 수 있어서 먼저 백그라운드로 돌려둘게요."
	case "run_shell_bundle_background":
		return "서로 독립적인 작업이라 백그라운드에서 병렬로 먼저 돌려둘게요."
	case "check_shell_job":
		return "백그라운드 작업 상태를 먼저 확인해볼게요."
	case "check_shell_bundle":
		return "백그라운드 작업 묶음 상태를 먼저 확인해볼게요."
	case "cancel_shell_job":
		return "이 background job은 stale해서 먼저 정리할게요."
	case "cancel_shell_bundle":
		return "이 background bundle은 stale해서 먼저 정리할게요."
	case "apply_patch":
		return "수정안을 만들었어요. 적용 전에 diff를 먼저 보여드릴게요."
	case "write_file":
		return "파일 내용을 갱신하려고 해요. 먼저 변경 내용을 보여드릴게요."
	case "replace_in_file":
		return "작은 치환 수정이 필요해 보여요. 적용 전에 변경 내용을 보여드릴게요."
	case "read_file":
		return "관련 파일부터 빠르게 확인해볼게요."
	case "grep":
		return "관련 코드 위치를 먼저 찾아볼게요."
	case "git_status", "git_diff":
		return "현재 변경 상태를 먼저 확인해볼게요."
	default:
		return "다음 단계로 진행해볼게요."
	}
}

var mentionPattern = regexp.MustCompile(`@([^\s]+)`)

func (a *Agent) expandMentions(ctx context.Context, input string) (string, []MessageImage) {
	matches := mentionPattern.FindAllStringSubmatch(input, -1)
	if len(matches) == 0 {
		return input, nil
	}
	var sections []string
	var images []MessageImage
	seen := map[string]bool{}
	replacements := map[string]string{}
	for _, match := range matches {
		raw := strings.Trim(match[1], ".,:;()[]{}<>\"'")
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		if a.MCP != nil {
			mentionCtx := ctx
			if mentionCtx == nil {
				mentionCtx = context.Background()
			}
			if display, content, ok := a.MCP.ResolveMention(mentionCtx, raw); ok {
				replacements["@"+raw] = display
				if len(content) > 6000 {
					content = content[:6000] + "\n... (truncated)"
				}
				sections = append(sections, fmt.Sprintf("Referenced MCP resource: %s\n```\n%s\n```", display, content))
				continue
			}
		}
		if image, display, ok := tryResolveMentionImage(a.Session.WorkingDir, raw); ok {
			replacements["@"+raw] = display
			images = appendUniqueImages(images, image)
			continue
		}
		path, startLine, endLine, ok := a.resolveMention(raw)
		if !ok {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if startLine > 0 {
			content = sliceLines(content, startLine, endLine)
		}
		if len(content) > 6000 {
			content = content[:6000] + "\n... (truncated)"
		}
		label := path
		if startLine > 0 {
			if endLine > startLine {
				label = fmt.Sprintf("%s:%d-%d", path, startLine, endLine)
			} else {
				label = fmt.Sprintf("%s:%d", path, startLine)
			}
		}
		display := relOrAbs(a.Session.WorkingDir, path)
		if startLine > 0 {
			if endLine > startLine {
				display = fmt.Sprintf("%s:%d-%d", display, startLine, endLine)
			} else {
				display = fmt.Sprintf("%s:%d", display, startLine)
			}
		}
		replacements["@"+raw] = display
		sections = append(sections, fmt.Sprintf("Referenced file: %s\n```\n%s\n```", label, content))
	}
	for raw, replacement := range replacements {
		input = strings.ReplaceAll(input, raw, replacement)
	}
	if len(sections) == 0 {
		return input, images
	}
	return input + "\n\nAttached context:\n" + strings.Join(sections, "\n\n"), images
}

var mentionRangePattern = regexp.MustCompile(`^(.*?):(\d+)(?:-(\d+))?$`)

func (a *Agent) resolveMention(raw string) (string, int, int, bool) {
	path := raw
	startLine := 0
	endLine := 0
	if match := mentionRangePattern.FindStringSubmatch(raw); len(match) == 4 {
		path = match[1]
		start, err := strconv.Atoi(match[2])
		if err == nil {
			startLine = start
			endLine = start
			if match[3] != "" {
				if end, err := strconv.Atoi(match[3]); err == nil && end >= start {
					endLine = end
				}
			}
		}
	}
	if startLine == 0 {
		if fullPath, ok := a.resolveMentionPath(raw); ok {
			return fullPath, 0, 0, true
		}
	}
	fullPath, ok := a.resolveMentionPath(path)
	if !ok {
		return "", 0, 0, false
	}
	return fullPath, startLine, endLine, true
}

func (a *Agent) resolveMentionPath(raw string) (string, bool) {
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Session.WorkingDir, path)
	}
	rootAbs, err := filepath.Abs(a.Session.WorkingDir)
	if err != nil {
		return "", false
	}
	targetAbs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", false
	}
	if rel != "." && (strings.HasPrefix(rel, "..") || filepath.IsAbs(rel)) {
		return "", false
	}
	return targetAbs, true
}

func sliceLines(content string, startLine, endLine int) string {
	lines := strings.Split(content, "\n")
	if startLine < 1 {
		startLine = 1
	}
	if endLine < startLine {
		endLine = startLine
	}
	if startLine > len(lines) {
		return ""
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	var out []string
	for i := startLine - 1; i < endLine; i++ {
		out = append(out, fmt.Sprintf("%4d | %s", i+1, strings.TrimSuffix(lines[i], "\r")))
	}
	return strings.Join(out, "\n")
}
