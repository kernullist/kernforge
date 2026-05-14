package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	VerifyChanges                  func(context.Context) (VerificationReport, bool)
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
	maxPreWriteReviewRepairBlocksPerTurn  = 1
	maxPreWriteReviewRepairInspectTools   = 6
	maxPreWriteReviewRepairInspectNudges  = 1
	maxEditTargetMismatchFailuresPerTurn  = 1
	maxToolBudgetExtensions               = 2
	compactPinnedMessagesToKeep           = 6
)

func (a *Agent) Reply(ctx context.Context, userText string) (string, error) {
	return a.ReplyWithImages(ctx, userText, nil)
}

const (
	reviewRepairConfirmationModeRepair                  = "repair"
	reviewRepairConfirmationModeReviewerGateUnavailable = "reviewer_gate_unavailable"
)

func (a *Agent) ReplyWithImages(ctx context.Context, userText string, extraImages []MessageImage) (string, error) {
	a.lastEmittedText = ""
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
	a.noteUserConversationEvent(userText)
	readOnlyAnalysis := prefersReadOnlyAnalysisIntent(userText)
	explicitEditRequest := looksLikeExplicitEditIntent(userText)
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
	if memoryContext := a.LongMem.PromptContextDetails(a.Workspace.BaseRoot, userText, a.Session.ID); strings.TrimSpace(memoryContext.Text) != "" {
		enriched += "\n\nRelevant persistent memory from past sessions:\n" + memoryContext.Text
		if message := formatPersistentMemoryProgressMessage(a.Config, memoryContext); message != "" {
			a.emitProgressEvent(ProgressEvent{
				Kind:    progressKindMemoryContext,
				Message: message,
			})
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
	a.initializeTaskState(userText)
	contract := buildAcceptanceContract(userText, intent, readOnlyAnalysis, explicitEditRequest, explicitGitRequest)
	a.Session.AcceptanceContract = &contract
	a.Session.LastCodingHarnessReport = nil
	a.Session.LastUserChangeIsolationReport = nil
	a.Session.LastTestImpactReport = nil
	a.Session.LastJobSupervisorReport = nil
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
		if len(a.Session.Messages) > 0 && a.Session.Messages[len(a.Session.Messages)-1].Role == "assistant" {
			a.Session.Messages[len(a.Session.Messages)-1].Text = reply
		}
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		return reply, nil
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
			a.Session.AddMessage(Message{Role: "assistant", Text: reply})
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			return reply, nil
		}
	}
	if ranReviewBeforeFix && a.shouldConcludeAfterNonBlockingPreFixReview(userText) {
		reply := a.formatNonBlockingPreFixReviewReply()
		a.Session.AddMessage(Message{Role: "assistant", Text: reply})
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
		return strings.TrimSpace(reply), nil
	}
	reply = strings.TrimSpace(reply)
	if clearPending {
		a.Session.PendingReviewRepairConfirm = nil
	}
	a.noteUserConversationEvent(userText)
	a.Session.AddMessage(Message{
		Role:   "user",
		Text:   userText,
		Images: images,
	})
	a.Session.AddMessage(Message{Role: "assistant", Text: reply})
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
	cut := compactCutIndex(a.Session.Messages, 12, 4)
	if cut <= 0 {
		return "conversation is already compact"
	}
	older := a.Session.Messages[:cut]
	a.Session.Messages = append([]Message(nil), a.Session.Messages[cut:]...)
	summary := summarizeMessages(older, instructions)
	if workingMemory := strings.TrimSpace(renderCompactionWorkingMemory(a.Session)); workingMemory != "" {
		summary = strings.TrimSpace(summary) + "\n\n" + workingMemory
	}
	if strings.TrimSpace(a.Session.Summary) == "" {
		a.Session.Summary = summary
	} else {
		a.Session.Summary = strings.TrimSpace(a.Session.Summary) + "\n\n" + summary
	}
	_ = a.Store.Save(a.Session)
	return summary
}

func (a *Agent) completeLoop(ctx context.Context, readOnlyAnalysis bool, explicitEditRequest bool, explicitGitRequest bool) (string, error) {
	a.refreshBackgroundJobs()
	if reply, ok, err := a.maybeAnswerFromCachedProjectAnalysis(ctx); err != nil {
		_ = a.Store.Save(a.Session)
		return "", err
	} else if ok {
		a.Session.AddMessage(Message{
			Role: "assistant",
			Text: reply,
		})
		a.noteAssistantConversationEvent(reply)
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		return reply, nil
	}
	latestUser := latestUserMessageText(a.Session.Messages)
	intent := classifyTurnIntent(latestUser)
	_ = a.primeSelfDrivingWorkLoop(latestUser, intent, readOnlyAnalysis, explicitEditRequest, explicitGitRequest)
	if err := a.maybePrimeInteractivePlan(ctx, readOnlyAnalysis, explicitEditRequest, explicitGitRequest); err != nil {
		return "", err
	}
	emptyFinalReplies := 0
	unresolvedVerification := false
	finalAnswerNudges := 0
	patchFormatRetries := 0
	lastPatchFormatFailureSignature := ""
	invalidToolArgsRetries := 0
	editTargetMismatchRetries := 0
	editTargetMismatchFailures := 0
	preWriteReviewRepairBlocks := 0
	preWriteReviewRepairInspectTools := 0
	preWriteReviewRepairInspectNudges := 0
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
	abruptReplyRetries := 0
	finalAnswerReviewRevisions := 0
	finalHarnessRevisions := 0
	runtimeGateFinalAnswerRevisions := 0
	runtimeGateGitWriteNudges := 0
	postChangeReviewRevisions := 0
	postChangeReviewExhaustedNudge := false
	lastPostChangeReviewFingerprint := ""
	lastReviewedFinalAnswer := ""
	attemptedEditTool := false
	successfulEditTool := false
	sawToolResultThisTurn := false
	repeatedToolFailureRecoveryTurns := 0
	continuedReplyPrefix := ""
	continuedReplyMessageIndex := -1
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
						a.Compact("Auto-compacted to preserve important context while extending the tool budget after sustained progress.")
					}
					recoveryRecent := summarizeRecentToolTurns(a.Session.Messages, 4)
					if a.EmitProgress != nil {
						a.EmitProgress(fmt.Sprintf("Recent tool turns show progress. Extending the tool budget by %d more turn(s)...", extraTurns))
					}
					a.Session.AddMessage(Message{
						Role: "user",
						Text: toolBudgetExtensionGuidance(extraTurns, recoveryRecent),
					})
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
			a.Session.AddMessage(Message{
				Role: "user",
				Text: a.recoveryGuidance(ctx, recoveryTriggerToolBudgetExceeded, recoveryInput{
					Summary: lastToolCallSummary,
					Recent:  recoveryRecent,
					Detail:  lastStopReason,
				}),
			})
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			lastRecentToolTurns = recoveryRecent
			continue
		}
		turnCount++
		lastIteration = turnCount
		if a.Config.AutoCompactChars > 0 && a.Session.ApproxChars() > a.Config.AutoCompactChars {
			a.Compact("Auto-compacted due to context growth.")
		}
		if err := a.syncTaskExecutorFocus(); err != nil {
			return "", err
		}
		_ = a.maybeRunInteractiveParallelEditableWorkers(ctx, "executor")
		_ = a.maybeRunInteractiveParallelReadOnlyWorkers(ctx, "executor")
		_ = a.maybeRunInteractiveMicroWorkers(ctx, "executor")
		turnDisabledTools := cloneDisabledTools(disabledTools)
		if shouldUseLocalCodeToolPolicy(a.Session) {
			disableWebResearchToolsForLocalCodeWork(turnDisabledTools, a.Tools)
		}
		turnReq := ChatRequest{
			Model:       a.Session.Model,
			System:      a.systemPrompt(),
			Messages:    a.Session.Messages,
			Tools:       a.Tools.DefinitionsExcluding(turnDisabledTools),
			MaxTokens:   a.Config.MaxTokens,
			Temperature: a.Config.Temperature,
			WorkingDir:  a.Session.WorkingDir,
			OnTextDelta: a.EmitAssistantDelta,
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
		resp.Message.Text = sanitizeAssistantMessageText(resp.Message.Text, len(resp.Message.ToolCalls) > 0)
		resp.Message.ToolCalls = assignFocusedOwnerNodeToToolCalls(resp.Message.ToolCalls, a.Session)
		if shouldRetryKoreanLocalCodeToolNarration(resp.Message, a.Session, a.Config) {
			a.Session.AddMessage(Message{
				Role: "user",
				Text: koreanLocalCodeToolNarrationGuidance(),
			})
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			continue
		}
		if shouldRetryPreFixEditWithoutVisibleReviewSummary(resp.Message, a.Session) {
			a.Session.AddMessage(Message{
				Role: "user",
				Text: preFixVisibleReviewSummaryGuidance(*a.Session.LastReviewRun),
			})
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			continue
		}
		if a.EmitAssistantDelta != nil && strings.TrimSpace(resp.Message.Text) != "" {
			a.lastEmittedText = strings.TrimSpace(resp.Message.Text)
		}
		lastStopReason = normalizeStopReason(resp.StopReason)
		a.Session.AddMessage(resp.Message)
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		if len(resp.Message.ToolCalls) > 0 {
			if !explicitGitRequest && hasMutatingGitToolCalls(resp.Message.ToolCalls) {
				a.Session.AddMessage(Message{
					Role: "user",
					Text: "Do not stage, commit, push, or open a PR unless the user explicitly asks for a git action first. Continue with inspection, edits, verification, and a summary instead.",
				})
				if err := a.Store.Save(a.Session); err != nil {
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
					a.Session.AddMessage(Message{
						Role: "user",
						Text: feedback,
					})
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
			}
			if replyBlamesInternalToolTranscriptRecovery(resp.Message.Text) {
				a.Session.AddMessage(Message{
					Role: "user",
					Text: internalToolTranscriptFailureGuidance(explicitEditRequest),
				})
				if err := a.Store.Save(a.Session); err != nil {
					return "", err
				}
				continue
			}
			if shouldBlockWebResearchForLocalCodeWork(resp.Message.ToolCalls, a.Session, a.MCP) {
				a.recordExternalLookupIntents(resp.Message.ToolCalls, "blocked_local_code_context", true)
				if a.EmitProgress != nil {
					a.EmitProgress(formatBlockedLocalCodeWebResearchProgress(a.Config, resp.Message.ToolCalls))
				}
				a.Session.AddMessage(Message{
					Role: "user",
					Text: localCodeWebResearchBlockGuidance(a.Config, a.Session),
				})
				if err := a.Store.Save(a.Session); err != nil {
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
				a.Session.AddMessage(Message{
					Role: "user",
					Text: guidance,
				})
				if err := a.Store.Save(a.Session); err != nil {
					return "", err
				}
				continue
			}
			if block, targetPath, parentPath := shouldBlockUnconfirmedDocumentReadToolCalls(resp.Message.ToolCalls, a.Session); block {
				a.Session.AddMessage(Message{
					Role: "user",
					Text: fmt.Sprintf("This request is document/report authoring work. Do not guess that generated files already exist and call read_file on them immediately. First use list_files on the parent directory %s to confirm whether %s actually exists. If the parent directory is empty or the file is absent, treat the document as not created yet and create or update it with edit tools instead.", parentPath, targetPath),
				})
				if err := a.Store.Save(a.Session); err != nil {
					return "", err
				}
				continue
			}
			if readOnlyAnalysis && allToolCallsAreEditTools(resp.Message.ToolCalls) {
				a.Session.AddMessage(Message{
					Role: "user",
					Text: "This request is analysis-only. Do not edit files or call edit tools. Investigate the current code and logs, then answer with the root cause or findings.",
				})
				if err := a.Store.Save(a.Session); err != nil {
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
					a.Session.AddMessage(Message{
						Role: "user",
						Text: a.recoveryGuidance(ctx, recoveryTriggerRepeatedToolCalls, recoveryInput{
							Summary: lastToolCallSummary,
							Recent:  summarizeRecentToolTurns(a.Session.Messages, 3),
							Detail:  lastToolCallSummary,
						}),
					})
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
					continue
				}
				if lastToolCallSignatureCount >= repeatedToolCallNudgeThreshold {
					if repeatedToolCallNudges < 1 {
						repeatedToolCallNudges++
						a.Session.AddMessage(Message{
							Role: "user",
							Text: "You are repeating the same tool call sequence with the same arguments. Do not repeat it again unless the previous tool result explicitly requires it. Use a different next step or provide the final answer now.",
						})
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
					a.Session.AddMessage(Message{
						Role: "user",
						Text: a.recoveryGuidance(ctx, recoveryTriggerRepeatedReadFile, recoveryInput{
							Path:   readPath,
							Turns:  lastReadFilePathTurns,
							Recent: summarizeRecentToolTurns(a.Session.Messages, 3),
							Detail: readPath,
						}),
					})
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
					continue
				}
				if lastReadFilePathTurns >= repeatedReadFilePathNudgeTurns && repeatedReadFilePathNudges < 1 {
					repeatedReadFilePathNudges++
					a.Session.AddMessage(Message{
						Role: "user",
						Text: fmt.Sprintf("You have read the same file repeatedly across multiple tool turns: %s. Do not keep scanning more ranges from the same file unless a specific missing section is still required. Either explain what you found so far, switch to a different tool or file, or provide the final answer now.", readPath),
					})
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
				a.Session.AddMessage(Message{
					Role: "user",
					Text: "You have already made multiple rounds of edits. Do not call more edit tools unless the previous changes are clearly insufficient. If the requested work is complete, provide the final answer now and summarize what changed.",
				})
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
			reply := strings.TrimSpace(resp.Message.Text)
			if reply != "" {
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
					a.Session.AddMessage(Message{
						Role: "user",
						Text: internalToolTranscriptFailureGuidance(explicitEditRequest),
					})
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if explicitEditRequest && !attemptedEditTool && replySuggestsManualEditHandoff(reply) && manualEditHandoffRetries < 1 {
					manualEditHandoffRetries++
					a.Session.AddMessage(Message{
						Role: "user",
						Text: "This request explicitly asks you to inspect and fix the code. Do not hand the patch back to the user. Read the relevant file if needed, then use the available edit tools directly. Only ask the user to edit manually if an edit tool actually failed, and cite that exact tool error.",
					})
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if abruptReplyRetries < 1 && replyLooksAbruptlyTruncated(reply) {
					abruptReplyRetries++
					continuedReplyPrefix = reply
					continuedReplyMessageIndex = len(a.Session.Messages) - 1
					a.Session.AddMessage(Message{
						Role: "user",
						Text: "Your last answer appears to have been cut off mid-sentence. Continue exactly from where you stopped and finish the answer. Do not restart from the beginning, do not apologize, and do not repeat the earlier text.",
					})
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if unresolvedVerification && finalAnswerNudges < 1 {
					finalAnswerNudges++
					a.Session.AddMessage(Message{
						Role: "user",
						Text: "Verification is still failing. Continue fixing the issue if possible. If you cannot fully fix it, give a final answer that explicitly explains the blocker and references the failing verification results.",
					})
					if err := a.Store.Save(a.Session); err != nil {
						return "", err
					}
					continue
				}
				if finalHarnessRevisions < 2 {
					approved, harnessFeedback := a.runPreFinalCodingHarnesses(ctx, reply, attemptedEditTool, unresolvedVerification)
					if !approved {
						finalHarnessRevisions++
						a.Session.AddMessage(Message{
							Role: "user",
							Text: harnessFeedback,
						})
						if err := a.Store.Save(a.Session); err != nil {
							return "", err
						}
						continue
					}
				}
				if successfulEditTool && !a.shouldSkipPostChangeReviewForKnownFinalBlocker(reply, unresolvedVerification) {
					needsModelTurn, err := a.runAutomaticPostChangeReviewGate(ctx, latestUser, &lastPostChangeReviewFingerprint, &postChangeReviewRevisions, &postChangeReviewExhaustedNudge)
					if err != nil {
						return "", err
					}
					if needsModelTurn {
						continue
					}
				}
				if runtimeGateFinalAnswerRevisions < 2 {
					ledger := a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
					if runtimeGateBlocksFinalAnswer(ledger, reply) {
						runtimeGateFinalAnswerRevisions++
						a.Session.AddMessage(Message{
							Role: "user",
							Text: renderRuntimeGateBlockedFeedback(ledger, runtimeGateActionFinalAnswer),
						})
						if err := a.Store.Save(a.Session); err != nil {
							return "", err
						}
						continue
					}
				} else {
					a.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
				}
				if a.shouldReviewInteractiveFinalAnswer(reply, attemptedEditTool, unresolvedVerification) &&
					finalAnswerReviewRevisions < 2 &&
					!strings.EqualFold(strings.TrimSpace(reply), strings.TrimSpace(lastReviewedFinalAnswer)) {
					approved, reviewText := a.reviewInteractiveFinalAnswer(ctx, reply, unresolvedVerification)
					lastReviewedFinalAnswer = reply
					if !approved {
						finalAnswerReviewRevisions++
						nextText := "Reviewer feedback: the proposed final answer is not ready yet. Revise the work or the answer before concluding."
						if strings.TrimSpace(reviewText) != "" {
							nextText += "\n\n" + strings.TrimSpace(reviewText)
						}
						a.Session.AddMessage(Message{
							Role: "user",
							Text: nextText,
						})
						if err := a.Store.Save(a.Session); err != nil {
							return "", err
						}
						continue
					}
				}
				if a.Session.TaskState != nil {
					if !a.finalizeSelfDrivingWorkLoopOnReturn(reply, unresolvedVerification) && a.shouldCompleteSharedPlanOnReturn(unresolvedVerification) {
						a.Session.TaskState.SetPhase("done")
						a.Session.TaskState.SetNextStep("Wait for the next user instruction.")
						a.Session.TaskState.ClearExecutorFocus()
						a.Session.completeSharedPlan()
					}
				}
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
				a.Session.AddMessage(Message{
					Role: "user",
					Text: "Your last reply was empty. This is a read-only analysis or review request. If you need more evidence, use read_file, grep, or list_files on the referenced code first. Then provide a concrete final answer with findings, likely root causes, and file references. Do not return an empty message.",
				})
			} else {
				a.Session.AddMessage(Message{
					Role: "user",
					Text: "Please provide the final answer to the user now. Do not return an empty message.",
				})
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
		toolMsgIndexes, saveErr := a.beginToolExecutions(resp.Message.ToolCalls)
		if saveErr != nil {
			return "", saveErr
		}
		for callIndex, call := range resp.Message.ToolCalls {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if isEditTool(call.Name) {
				attemptedEditTool = true
				lastPostChangeReviewFingerprint = ""
				postChangeReviewExhaustedNudge = false
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
			toolMsgIndex := -1
			if callIndex >= 0 && callIndex < len(toolMsgIndexes) {
				toolMsgIndex = toolMsgIndexes[callIndex]
			}
			var result ToolExecutionResult
			var err error
			if isolationErr := a.checkUserChangeIsolationBeforeTool(call); isolationErr != nil {
				err = isolationErr
				result = userChangeIsolationToolResult(call, isolationErr)
			} else {
				patchProbe := a.beginPatchTransactionToolProbe(call)
				result, err = a.Tools.ExecuteDetailed(ctx, call.Name, call.Arguments)
				a.finishPatchTransactionToolProbe(patchProbe, call, result, err)
			}
			a.rebaselineUserChangeIsolationFromRead(call, err)
			sawToolResultThisTurn = true
			if err == nil {
				a.noteToolConversationResult(call, result)
			}
			toolMsg := Message{
				Role:       "tool",
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Text:       result.DisplayText,
				ToolMeta:   result.Meta,
			}
			a.setToolExecutionResult(toolMsgIndex, toolMsg)
			a.noteToolExecutionResultDetailed(call, result, err)
			if saveErr := a.Store.Save(a.Session); saveErr != nil {
				return "", saveErr
			}
			if err != nil && errors.Is(err, ErrEditCanceled) {
				toolMsg.Text = "CANCELED: user canceled the edit preview. No files were changed."
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && errors.Is(err, ErrWriteDenied) {
				toolMsg.Text = "CANCELED: user declined write approval. No files were changed, and no filesystem permission issue was detected."
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && errors.Is(err, ErrInvalidEditPayload) {
				toolMsg.IsError = true
				if result.DisplayText == "" {
					toolMsg.Text = err.Error()
				} else {
					toolMsg.Text = result.DisplayText + "\n\nERROR: " + err.Error()
				}
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && isPreWriteReviewBlockedError(err) {
				preWriteReviewRepairBlocks++
				preWriteReviewRepairInspectTools = 0
				preWriteReviewRepairInspectNudges = 0
				if preWriteReviewRepairBlocks > maxPreWriteReviewRepairBlocksPerTurn {
					toolMsg.IsError = true
					if result.DisplayText == "" {
						toolMsg.Text = err.Error()
					} else {
						toolMsg.Text = result.DisplayText + "\n\nERROR: " + err.Error()
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
					a.setToolExecutionResult(toolMsgIndex, toolMsg)
					reply := formatPreWriteReviewRepairLoopLimitReply(a.Config, a.Session)
					a.Session.AddMessage(Message{Role: "assistant", Text: reply})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					return reply, nil
				}
			}
			if preWriteReviewRepairBlocks > 0 && err != nil && !isPreWriteReviewBlockedError(err) && !isEditTool(call.Name) {
				preWriteReviewRepairInspectTools++
				if preWriteReviewRepairInspectTools > maxPreWriteReviewRepairInspectTools {
					if preWriteReviewRepairInspectNudges < maxPreWriteReviewRepairInspectNudges {
						preWriteReviewRepairInspectNudges++
						preWriteReviewRepairInspectTools = 0
						a.Session.AddMessage(Message{
							Role: "user",
							Text: formatPreWriteReviewRepairForceEditGuidance(a.Config, summarizeRecentToolTurns(a.Session.Messages, 4)),
						})
						if saveErr := a.Store.Save(a.Session); saveErr != nil {
							return "", saveErr
						}
						lastToolError = ""
						lastToolErrorCount = 0
						lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
						continue
					}
					toolMsg.IsError = true
					if result.DisplayText == "" {
						toolMsg.Text = err.Error()
					} else {
						toolMsg.Text = result.DisplayText + "\n\nERROR: " + err.Error()
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
					a.setToolExecutionResult(toolMsgIndex, toolMsg)
					reply := formatPreWriteReviewRepairInspectionLoopLimitReply(a.Config, a.Session)
					a.Session.AddMessage(Message{Role: "assistant", Text: reply})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					return reply, nil
				}
			}
			if err != nil && errors.Is(err, ErrEditTargetMismatch) {
				editTargetMismatchFailures++
				if editTargetMismatchFailures > maxEditTargetMismatchFailuresPerTurn {
					toolMsg.IsError = true
					if result.DisplayText == "" {
						toolMsg.Text = err.Error()
					} else {
						toolMsg.Text = result.DisplayText + "\n\nERROR: " + err.Error()
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
					a.setToolExecutionResult(toolMsgIndex, toolMsg)
					reply := formatEditTargetMismatchLoopLimitReply(a.Config)
					a.Session.AddMessage(Message{Role: "assistant", Text: reply})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					return reply, nil
				}
			}
			if err != nil && errors.Is(err, ErrInvalidToolArgumentsJSON) && invalidToolArgsRetries < 1 {
				toolMsg.IsError = true
				if result.DisplayText == "" {
					toolMsg.Text = err.Error()
				} else {
					toolMsg.Text = result.DisplayText + "\n\nERROR: " + err.Error()
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
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				if toolShouldBeDisabledAfterInvalidJSON(call.Name) {
					disabledTools[strings.TrimSpace(call.Name)] = true
				}
				a.Session.AddMessage(Message{
					Role: "user",
					Text: invalidToolArgumentsGuidance(call.Name),
				})
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				invalidToolArgsRetries++
				lastToolError = ""
				lastToolErrorCount = 0
				continue
			}
			if err != nil && errors.Is(err, ErrEditTargetMismatch) && editTargetMismatchRetries < 1 {
				toolMsg.IsError = true
				if result.DisplayText == "" {
					toolMsg.Text = err.Error()
				} else {
					toolMsg.Text = result.DisplayText + "\n\nERROR: " + err.Error()
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
				a.setToolExecutionResult(toolMsgIndex, toolMsg)
				a.Session.AddMessage(Message{
					Role: "user",
					Text: "Your last edit targeted stale or mismatched file contents. This is still local code review/repair work. Do not use MCP web/search/browser tools or external web research. Do not repeat the same edit immediately. First read the exact file again from the same path, confirm the current contents, and then build a new edit against that fresh text. If the resolved path points into a different worktree or administrative worktree directory, correct the path before editing.",
				})
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				editTargetMismatchRetries++
				lastToolError = ""
				lastToolErrorCount = 0
				continue
			}
			if err != nil {
				a.noteToolConversationError(call.Name, err, toolMsg.Text)
				toolMsg.IsError = true
				if result.DisplayText == "" {
					toolMsg.Text = err.Error()
				} else {
					toolMsg.Text = result.DisplayText + "\n\nERROR: " + err.Error()
				}
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
					reply := localizedText(a.Config,
						"The pre-write reviewer gate did not produce reliable evidence, so I stopped the edit. No code changes were applied.",
						"쓰기 전 리뷰어 게이트가 신뢰 가능한 근거를 만들지 못해서 편집을 중단했습니다. 코드 수정은 적용하지 않았습니다.",
					)
					if a.Session != nil && a.Session.LastReviewRun != nil {
						reply = formatReviewerGateUnavailableUserDecisionReply(a.Config, a.Session)
					}
					if a.PromptContinueReviewRepair != nil && a.Session != nil && a.Session.LastReviewRun != nil {
						if !reviewRunHasActionableNonReviewerFindingsFromSession(a.Session) {
							a.Session.PendingReviewRepairConfirm = nil
							a.Session.AddMessage(Message{Role: "assistant", Text: reply})
							if saveErr := a.Store.Save(a.Session); saveErr != nil {
								return "", saveErr
							}
							return reply, nil
						}
						promptText := formatReviewerGateUnavailableUserDecisionPrompt(a.Config, a.Session)
						continueRepair, promptErr := a.PromptContinueReviewRepair(promptText)
						if promptErr != nil {
							return "", promptErr
						}
						a.Session.PendingReviewRepairConfirm = nil
						if !continueRepair {
							reply = formatCancelledPendingReviewRepairReply(a.Config)
							a.Session.AddMessage(Message{Role: "assistant", Text: reply})
							if saveErr := a.Store.Save(a.Session); saveErr != nil {
								return "", saveErr
							}
							return reply, nil
						}
						if !a.primeReviewerGateRepairFromLastReview(latestUser) {
							reply = formatNoActionableReviewerGateRepairReply(a.Config)
							a.Session.AddMessage(Message{Role: "assistant", Text: reply})
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
						continue
					}
					a.Session.AddMessage(Message{Role: "assistant", Text: reply})
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
					a.Session.AddMessage(Message{
						Role: "user",
						Text: invalidPatchFormatGuidance(repeatedSignature, err),
					})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					lastToolError = ""
					lastToolErrorCount = 0
					continue
				}
			} else {
				iterationHadToolSuccess = true
				editLike := isEditTool(call.Name) ||
					toolMetaBool(result.Meta, "changed_workspace") ||
					strings.EqualFold(strings.TrimSpace(toolMetaString(result.Meta, "effect")), "edit")
				if editLike {
					edited = true
					attemptedEditTool = true
					successfulEditTool = true
					preWriteReviewRepairBlocks = 0
					preWriteReviewRepairInspectTools = 0
					preWriteReviewRepairInspectNudges = 0
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
				_ = a.maybeRunInteractiveParallelEditableWorkers(ctx, "tool:"+strings.TrimSpace(call.Name))
				_ = a.maybeRunInteractiveParallelReadOnlyWorkers(ctx, "tool:"+strings.TrimSpace(call.Name))
				_ = a.maybeRunInteractiveMicroWorkers(ctx, "tool:"+strings.TrimSpace(call.Name))
			}
		}
		if preWriteReviewRepairBlocks > 0 && iterationHadToolSuccess {
			for _, call := range resp.Message.ToolCalls {
				if isEditTool(call.Name) {
					continue
				}
				preWriteReviewRepairInspectTools++
				if preWriteReviewRepairInspectTools > maxPreWriteReviewRepairInspectTools {
					if preWriteReviewRepairInspectNudges < maxPreWriteReviewRepairInspectNudges {
						preWriteReviewRepairInspectNudges++
						preWriteReviewRepairInspectTools = 0
						a.Session.AddMessage(Message{
							Role: "user",
							Text: formatPreWriteReviewRepairForceEditGuidance(a.Config, summarizeRecentToolTurns(a.Session.Messages, 4)),
						})
						if saveErr := a.Store.Save(a.Session); saveErr != nil {
							return "", saveErr
						}
						lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
						continue
					}
					reply := formatPreWriteReviewRepairInspectionLoopLimitReply(a.Config, a.Session)
					a.Session.AddMessage(Message{Role: "assistant", Text: reply})
					if saveErr := a.Store.Save(a.Session); saveErr != nil {
						return "", saveErr
					}
					return reply, nil
				}
			}
		}
		if lastReadFilePath != "" && lastReadFilePathTurns >= 2 && repeatedCachedReadFileNudges < 1 && lastAssistantToolTurnWasCachedReadFile(a.Session.Messages) {
			repeatedCachedReadFileNudges++
			repeatedReadFilePathNudges++
			a.Session.AddMessage(Message{
				Role: "user",
				Text: fmt.Sprintf("Your latest read_file result for %s came from cached previously-read content. Treat that as confirmation that you already have that context. Do not reread the same chunk again. Either inspect a different file or tool, or give the final answer now.", lastReadFilePath),
			})
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
			a.Session.AddMessage(Message{
				Role: "user",
				Text: a.recoveryGuidance(ctx, recoveryTriggerRepeatedToolError, recoveryInput{
					Detail: lastToolError,
					Recent: summarizeRecentToolTurns(a.Session.Messages, 3),
				}),
			})
			if err := a.Store.Save(a.Session); err != nil {
				return "", err
			}
			lastRecentToolTurns = summarizeRecentToolTurns(a.Session.Messages, 3)
			continue
		}
		if lastToolErrorCount == repeatedToolFailureNudgeThreshold && lastToolError != "" {
			a.Session.AddMessage(Message{
				Role: "user",
				Text: "The same tool failure repeated. Do not repeat the same failing tool call again unless you materially change the approach or inputs. Read the error carefully, choose a different next step, or provide a final answer if the issue is external.",
			})
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
						a.EmitProgress(localizedText(a.Config, "Automatic verification failed. Asking the model to continue fixing the issue...", "자동 검증이 실패했습니다. 모델에 수정을 계속 요청합니다..."))
					} else {
						a.EmitProgress(localizedText(a.Config, "Automatic verification finished. Waiting for the model to summarize the change...", "자동 검증이 완료되었습니다. 모델의 변경 요약을 기다립니다..."))
					}
				}
				verification := strings.TrimSpace(report.RenderDetailed())
				a.Session.AddMessage(Message{
					Role: "user",
					Text: localizedText(a.Config, "Automatic verification results:\n", "자동 검증 결과:\n") + verification,
				})
				unresolvedVerification = report.HasFailures()
				if report.HasFailures() {
					if report.HasCommandMissingFailure() {
						autoVerifyInfraFailureCount++
					} else {
						autoVerifyInfraFailureCount = 0
					}
					if autoVerifyInfraFailureCount >= 1 && !autoVerifyDisablePrompted && a.PromptResolveAutoVerifyFailure != nil {
						autoVerifyDisablePrompted = true
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
								a.Session.AddMessage(Message{
									Role: "user",
									Text: localizedText(a.Config, "Automatic verification results after tool-path update:\n", "도구 경로 업데이트 후 자동 검증 결과:\n") + verification,
								})
								unresolvedVerification = report.HasFailures()
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
							a.Session.AddMessage(Message{
								Role: "user",
								Text: "Automatic verification has been disabled for this workspace after repeated verification tool startup failures. Do not spend more turns trying to repair the local verification environment unless the user explicitly asks for that. Continue with the task and summarize any unverified risk briefly if needed.",
							})
						}
					}
					if !autoVerifyDisabledAfterPrompt {
						failureSummary := strings.TrimSpace(report.FailureSummary())
						repairGuidance := strings.TrimSpace(report.RepairGuidance())
						text := "The latest verification failed. Investigate the failure and continue working if you can. Prefer fixing the problem over stopping early."
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
						a.Session.AddMessage(Message{
							Role: "user",
							Text: text,
						})
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
					a.Session.AddMessage(Message{
						Role: "user",
						Text: "You have already completed multiple edit rounds. If there is no specific remaining issue to fix, stop editing and provide the final answer now. Only continue editing if the earlier changes are clearly insufficient.",
					})
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
	trimmed = suppressInternalRoutingMarkers(trimmed)
	if trimmed == "" {
		return ""
	}
	if !hasToolCalls {
		return trimmed
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
	case strings.HasPrefix(lower, "now let me "):
		return true
	case strings.HasPrefix(lower, "now i "):
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
	return strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
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
	if reviewRunHasActionableNonReviewerFindingsFromSession(session) {
		recordPendingReviewerGateRepairConfirmation(session)
	}
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
	var b strings.Builder
	if korean {
		b.WriteString("쓰기 전 리뷰어 게이트: 통과하지 못함")
		b.WriteString("\n- 결과: 코드 수정은 적용하지 않았습니다.")
		b.WriteString("\n- 원인: 필수 리뷰 단계의 모델 route가 실패했거나 `weak` 품질로 판정되었습니다. `primary` 실패는 현재 메인 모델 route 문제이고, `cross` 실패는 전용 reviewer route 문제입니다.")
		b.WriteString("\n- 중요한 점: 이 상태는 쓰기 승인도, 리뷰 우회 승인도 아닙니다.")
		b.WriteString("\n- 다음 조건: 최신 리뷰 finding과 마지막 수정안을 기준으로 다시 수리한 뒤, 일반 파일 쓰기 경로에서 pre-write review를 다시 통과해야 합니다.")
	} else {
		b.WriteString("Pre-write reviewer gate: not approved (did not pass)")
		b.WriteString("\n- Result: no code changes were applied.")
		b.WriteString("\n- Cause: a required review-stage model route failed or was classified as `weak` quality. A `primary` failure points at the active main model route; a `cross` failure points at a dedicated reviewer route.")
		b.WriteString("\n- Important: this is not write approval and not approval to bypass review.")
		b.WriteString("\n- Next condition: repair from the latest review findings and last edit proposal below, then pass the normal pre-write review through the edit tool path.")
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
		b.WriteString("\n- `[0] 실패한 리뷰어`에 `primary`가 보이면 현재 메인 모델 route가 문제입니다. `/model`로 메인 모델을 바꾸거나 LM Studio/Qwen 응답 문제를 먼저 해결하세요.")
		b.WriteString("\n- `cross` 또는 전용 reviewer가 보이면 `/review models`로 해당 reviewer route를 정상 동작하는 모델로 바꾸세요.")
		b.WriteString("\n- route를 복구한 뒤 같은 요청을 다시 실행하세요.")
		if includeInlinePrompt {
			b.WriteString("\n- 이 상태에서는 `y/N` 선택을 받지 않습니다.")
		}
	} else {
		b.WriteString("\n\n[3] Next step\nThis stop was caused by a required review-stage model route failure or weak output, not by a code finding.")
		b.WriteString("\n- There is no code item to repair right now, so I will not continue editing.")
		b.WriteString("\n- If `[0] Failed reviewer` shows `primary`, the active main model route is the problem. Use `/model` to switch the main model or fix the LM Studio/Qwen response issue first.")
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
	toolName, proposal := latestEditToolProposal(session)
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

func latestEditToolProposal(session *Session) (string, string) {
	if session == nil {
		return "", ""
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
				return call.Name, proposal
			}
		}
	}
	return "", ""
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

func formatPreWriteReviewRepairForceEditGuidance(cfg Config, recent string) string {
	var b strings.Builder
	korean := localePrefersKorean(cfg)
	if korean {
		b.WriteString("pre-write 리뷰가 이미 수정안을 차단했고, 필요한 로컬 상태 확인 예산도 충분히 사용했습니다.\n")
		b.WriteString("다음 응답은 상태 확인 도구 호출이 아니라 반드시 edit tool 호출이어야 합니다.\n")
		b.WriteString("규칙:\n")
		b.WriteString("1. 최신 pre-write finding 중 unresolved 차단 항목만 고치세요.\n")
		b.WriteString("2. 방금 확인한 현재 파일 내용에 고정된 좁은 apply_patch hunk를 작성하세요.\n")
		b.WriteString("3. 같은 patch, replace_in_file 추측, review artifact 재읽기, run_shell 우회 쓰기는 금지합니다.\n")
		b.WriteString("4. 필요한 문맥이 아직 부족하다고 판단되면, 먼저 한 문장으로 정확한 blocker를 말하고 수정 불가 사유를 보고하세요. 추가 탐색 루프를 시작하지 마세요.")
	} else {
		b.WriteString("The pre-write review already blocked the edit, and enough local state inspection has been spent.\n")
		b.WriteString("The next response must be an edit-tool call, not another inspection-tool call.\n")
		b.WriteString("Rules:\n")
		b.WriteString("1. Fix only the latest unresolved blocking pre-write finding.\n")
		b.WriteString("2. Use a narrow apply_patch hunk anchored to the current file contents just inspected.\n")
		b.WriteString("3. Do not repeat the same patch, guess with replace_in_file, reread review artifacts, or bypass review with run_shell writes.\n")
		b.WriteString("4. If the context is still insufficient, state the exact blocker in one sentence and report why editing cannot proceed. Do not start another inspection loop.")
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

func formatEditTargetMismatchLoopLimitReply(cfg Config) string {
	return localizedText(cfg,
		"Edit target mismatches repeated after a refresh attempt, so I stopped instead of continuing to guess at the file state. The file should be re-read and edited with a narrower, current-context patch.",
		"파일 상태를 다시 확인한 뒤에도 edit target mismatch가 반복되어, 더 추측하며 진행하지 않고 중단했습니다. 파일을 다시 읽고 현재 내용 기준의 더 좁은 패치로 수정해야 합니다.",
	)
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
		if command == "" {
			return localizedText(cfg, "Using run_shell...", "shell 실행 중 ...")
		}
		return fmt.Sprintf(localizedText(cfg, "Running shell: %s", "shell 실행 중 ... %s"), command)
	case "run_shell_background":
		command := strings.TrimSpace(stringValue(args, "command"))
		if len(command) > 72 {
			command = command[:69] + "..."
		}
		if command == "" {
			return localizedText(cfg, "Starting background shell...", "백그라운드 shell 시작 중 ...")
		}
		return fmt.Sprintf(localizedText(cfg, "Starting background shell: %s", "백그라운드 shell 시작 중 ... %s"), command)
	case "run_shell_bundle_background":
		commands := stringSliceValue(args, "commands")
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
		if snippet == "" {
			return localizedText(cfg, "run_shell completed with no output.", "shell 완료: 출력 없음.")
		}
		return fmt.Sprintf(localizedText(cfg, "run_shell completed: %s", "shell 완료: %s"), snippet)
	case "run_shell_background":
		snippet := truncateStatusSnippet(firstNonEmptyLine(out), 80)
		if snippet == "" {
			return localizedText(cfg, "Background shell job started.", "백그라운드 shell 시작됨.")
		}
		return fmt.Sprintf(localizedText(cfg, "Background shell started: %s", "백그라운드 shell 시작: %s"), snippet)
	case "run_shell_bundle_background":
		snippet := truncateStatusSnippet(firstNonEmptyLine(out), 80)
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

func hasMutatingGitToolCalls(calls []ToolCall) bool {
	for _, call := range calls {
		if toolCallMutatesGitState(call) {
			return true
		}
	}
	return false
}

func shouldBlockUnconfirmedDocumentReadToolCalls(calls []ToolCall, session *Session) (bool, string, string) {
	if session == nil || !looksLikeDocumentAuthoringIntent(latestUserMessageText(session.Messages)) {
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
		if sessionHasListFilesConfirmationForParent(session, parentPath) || toolCallsIncludeListFilesConfirmation(calls, parentPath) {
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
	latestUser := baseUserQueryText(latestUserMessageText(session.Messages))
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
	latestUser := strings.ToLower(strings.TrimSpace(baseUserQueryText(latestUserMessageText(session.Messages))))
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
	latestUser := strings.ToLower(strings.TrimSpace(baseUserQueryText(latestUserMessageText(session.Messages))))
	if requestExplicitlyAsksForWebResearch(latestUser) {
		return false
	}
	if requestLooksLikeLocalCodeWork(latestUser) {
		return true
	}
	if reviewRunLooksLikeLocalCodeWork(session.LastReviewRun) {
		return true
	}
	const recentUserScanLimit = 12
	scanned := 0
	for i := len(session.Messages) - 1; i >= 0 && scanned < recentUserScanLimit; i-- {
		msg := session.Messages[i]
		if msg.Role != "user" {
			continue
		}
		scanned++
		text := strings.ToLower(strings.TrimSpace(baseUserQueryText(msg.Text)))
		if text == "" || requestExplicitlyAsksForWebResearch(text) {
			continue
		}
		if requestLooksLikeLocalCodeWork(text) {
			return true
		}
	}
	return false
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
		latestUser = baseUserQueryText(latestUserMessageText(session.Messages))
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
	latestUser := baseUserQueryText(latestUserMessageText(session.Messages))
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
	return !sessionHasVisiblePreFixReviewSummary(session, run)
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
	hasStructuredID := false
	for _, finding := range preFixVisibleReviewSummaryObligations(run, 5) {
		id := strings.TrimSpace(finding.ID)
		if id == "" {
			continue
		}
		hasStructuredID = true
		if strings.Contains(trimmed, id) {
			return true
		}
	}
	if hasStructuredID {
		return false
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

func assignFocusedOwnerNodeToToolCalls(calls []ToolCall, session *Session) []ToolCall {
	if len(calls) == 0 || session == nil || session.TaskState == nil {
		return calls
	}
	ownerNodeID := strings.TrimSpace(session.TaskState.ExecutorFocusNode)
	if ownerNodeID == "" {
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

func (a *Agent) systemPrompt() string {
	var b strings.Builder
	latestUser := baseUserQueryText(latestUserMessageText(a.Session.Messages))
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
	fmt.Fprintf(&b, "Workspace root: %s\n", a.Session.WorkingDir)
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", a.Session.Provider, a.Session.Model)
	fmt.Fprintf(&b, "Permission mode: %s\n", a.Session.PermissionMode)
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
	if a.Session.ActivePatchTransaction != nil {
		if txText := strings.TrimSpace(a.Session.ActivePatchTransaction.RenderPromptSection()); txText != "" {
			b.WriteString("\nActive patch transaction:\n")
			b.WriteString(txText)
			b.WriteString("\n")
		}
	}
	if a.Session.ActiveEditLoop != nil {
		if loopText := strings.TrimSpace(a.Session.ActiveEditLoop.RenderPromptSection()); loopText != "" {
			b.WriteString("\nActive apply/verify/retry ledger:\n")
			b.WriteString(loopText)
			b.WriteString("\n")
			if contractText := strings.TrimSpace(renderEditLoopOutcomeContractPrompt(a.Session.ActiveEditLoop)); contractText != "" {
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
	b.WriteString("- Every *** Update File: section in apply_patch must contain at least one @@ hunk with context and +/- lines. Never send an update file section with no hunks.\n")
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
	b.WriteString("- Use run_shell_background for a single long-running build, test, or verification command that may take multiple minutes.\n")
	b.WriteString("- Use run_shell_bundle_background when multiple independent build, test, or verification commands can run in parallel.\n")
	b.WriteString("- Use check_shell_job to poll a background shell job instead of rerunning the same long command.\n")
	b.WriteString("- Use check_shell_bundle to poll several background shell jobs together when a parallel verification bundle is running.\n")
	b.WriteString("- When a background shell bundle already exists, prefer check_shell_bundle with bundle_id=\"latest\" instead of reconstructing the job id list from memory.\n")
	b.WriteString("- When a background job or bundle becomes obsolete after newer edits or a newer verification run, use cancel_shell_job or cancel_shell_bundle instead of leaving stale work running.\n")
	b.WriteString("- When the current focused task-graph node owns an edit, formatter run, code generator run, or long-running verification command, include owner_node_id in the tool arguments so specialist ownership routing and worktree isolation stay attached to that node.\n")
	b.WriteString("- For run_shell on Windows PowerShell, do not use && or ||. Use a single command or PowerShell separators and conditionals only when needed.\n")
	b.WriteString("- For run_shell on Windows PowerShell, do not use Unix shell syntax like find ... -type, chmod, chown, ls -la, or /dev/null redirection, and do not use cmd.exe batch syntax like for /d %x. Rewrite those commands with PowerShell cmdlets, or explicitly invoke cmd /c or bash -lc only when that interpreter is intentionally required.\n")
	b.WriteString("- For run_shell, the working directory is already set to the workspace root. Do not prepend commands with cd unless changing into a subdirectory is truly necessary.\n")
	b.WriteString("- When the user asks to create or update ordinary source files or documents, prefer edit tools. Do not use run_shell for repo bootstrap, ACL changes, or git init unless the user explicitly asked for setup work.\n")
	b.WriteString("- For document or report authoring tasks, do not assume generated files already exist. Use list_files on the parent directory before read_file. If the directory is empty or the file is absent, treat the document as not created yet and create or update it with edit tools.\n")
	b.WriteString("- For latest/current external research tasks, prefer relevant MCP web/search/browser tools before answering from memory. Gather multiple sources, compare recency and authority, then synthesize.\n")
	b.WriteString("- For local code review or repair tasks, do not use MCP web/search/browser tools unless the user explicitly asks for external web research. Rely on local source evidence and review artifacts first.\n")
	b.WriteString("- For scoped mutating shell commands, only use run_shell with allow_workspace_writes=true and write_paths when a formatter, code generator, or setup command is clearly safer than a manual patch.\n")
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
	return strings.TrimSpace(text[:end]) + "..."
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
	return (hasPathOrCodeToken && hasCodeIntent) || hasCodeAnalysisIntent
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
		text := compactMessageSummaryText(msg)
		if text == "" && len(msg.Images) > 0 {
			text = fmt.Sprintf("attached %d image(s)", len(msg.Images))
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
