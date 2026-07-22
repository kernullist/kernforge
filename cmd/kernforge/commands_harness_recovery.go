package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (rt *runtimeState) handleFinishCommand(args string) error {
	if rt == nil || rt.agent == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	fields := strings.Fields(strings.TrimSpace(args))
	disclose := false
	for _, field := range fields {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "--disclose", "disclose", "--disclosure", "disclosure":
			disclose = true
		case "--help", "help", "-h":
			fmt.Fprintln(rt.writer, rt.ui.infoLine(localizedText(rt.cfg,
				"Usage: /finish --disclose  Finish a harness-blocked turn with honest verification disclosure.",
				"사용법: /finish --disclose  harness로 차단된 턴을 검증 미실행 공개로 끝냅니다.")))
			return nil
		default:
			return fmt.Errorf("unknown /finish argument %q; use /finish --disclose", field)
		}
	}
	if !disclose {
		return fmt.Errorf("usage: /finish --disclose")
	}
	reply, err := rt.executeHarnessRecoveryAction(context.Background(), HarnessRecoveryAction{
		ID:      "finish-disclose",
		Command: "/finish --disclose",
		Kind:    harnessRecoveryActionDisclose,
	})
	if text := strings.TrimSpace(reply); text != "" {
		fmt.Fprintln(rt.writer, text)
	}
	return err
}

func (rt *runtimeState) handleRetryVerifyCommand(args string) error {
	if rt == nil || rt.agent == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	if strings.TrimSpace(args) != "" {
		return fmt.Errorf("usage: /retry-verify")
	}
	if rt.session.PendingHarnessBlockedRecovery == nil {
		return fmt.Errorf("no blocked harness recovery is pending; run a task that hits the pre-final harness first")
	}
	reply, err := rt.executeHarnessRecoveryAction(context.Background(), HarnessRecoveryAction{
		ID:      "retry-verify",
		Command: "/retry-verify",
		Kind:    harnessRecoveryActionRetryVerify,
	})
	if text := strings.TrimSpace(reply); text != "" {
		fmt.Fprintln(rt.writer, text)
	}
	return err
}

func (rt *runtimeState) handleContinueCommand(args string) error {
	if rt == nil || rt.agent == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	if strings.TrimSpace(args) != "" {
		return fmt.Errorf("usage: /continue")
	}
	recovery := rt.session.PendingHarnessBlockedRecovery
	if recovery == nil {
		return fmt.Errorf("no blocked harness recovery is pending; /continue resumes the primary next step after a harness block")
	}
	copyRecovery := *recovery
	copyRecovery.Normalize()
	action := harnessRecoveryPrimaryAction(copyRecovery)
	reply, err := rt.executeHarnessRecoveryAction(context.Background(), action)
	if text := strings.TrimSpace(reply); text != "" {
		fmt.Fprintln(rt.writer, text)
	}
	return err
}

func harnessRecoveryPrimaryAction(recovery HarnessBlockedRecovery) HarnessRecoveryAction {
	recovery.Normalize()
	primary := strings.TrimSpace(recovery.PrimaryCommand)
	for _, action := range recovery.Actions {
		if strings.TrimSpace(action.Command) == primary || (primary == "" && action.ID != "") {
			return action
		}
	}
	if len(recovery.Actions) > 0 {
		return recovery.Actions[0]
	}
	return HarnessRecoveryAction{
		ID:      "repair",
		Command: "/continue",
		Kind:    harnessRecoveryActionRepair,
	}
}

func operatorStatusNextCommandLine(session *Session, ledger RuntimeGateLedger) string {
	if primary := harnessBlockedRecoveryPrimaryCommand(session); primary != "" {
		reason := "harness blocked recovery"
		if session != nil && session.PendingHarnessBlockedRecovery != nil && len(session.PendingHarnessBlockedRecovery.Actions) > 0 {
			action := session.PendingHarnessBlockedRecovery.Actions[0]
			if title := strings.TrimSpace(action.TitleEN); title != "" {
				reason = title
			}
		}
		return primary + " - " + reason
	}
	return runtimeGatePrimaryNextCommandLine(ledger)
}

// maybeHandlePendingHarnessRecoveryInput runs a pending recovery action when the
// user answers with a choice number or an explicit recovery intent, instead of
// sending that short answer into a new model turn.
func (rt *runtimeState) maybeHandlePendingHarnessRecoveryInput(ctx context.Context, input string) (bool, string, error) {
	if rt == nil || rt.session == nil || rt.session.PendingHarnessBlockedRecovery == nil || rt.offeringHarnessRecovery {
		return false, "", nil
	}
	recovery := *rt.session.PendingHarnessBlockedRecovery
	recovery.Normalize()
	action, ok := matchHarnessRecoveryInput(rt.cfg, recovery, input)
	if !ok {
		return false, "", nil
	}
	rt.offeringHarnessRecovery = true
	defer func() { rt.offeringHarnessRecovery = false }()
	reply, err := rt.executeHarnessRecoveryAction(ctx, action)
	return true, reply, err
}

// offerHarnessBlockedRecoveryChoice presents the pending recovery actions with
// the shared numbered-choice prompt and executes the selected action.
func (rt *runtimeState) offerHarnessBlockedRecoveryChoice(ctx context.Context) (string, bool, error) {
	if rt == nil || !rt.interactive || rt.session == nil || rt.session.PendingHarnessBlockedRecovery == nil || rt.offeringHarnessRecovery {
		return "", false, nil
	}
	recovery := *rt.session.PendingHarnessBlockedRecovery
	recovery.Normalize()
	if len(recovery.Actions) == 0 {
		return "", false, nil
	}
	result, err := rt.promptUserChoice(harnessBlockedRecoveryUserQuestion(rt.cfg, recovery))
	if err != nil {
		return "", false, err
	}
	if result.Canceled {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(localizedText(rt.cfg,
			"Choice canceled. Type a number (1, 2, ...) or a recovery command when ready.",
			"선택을 취소했습니다. 준비가 되면 번호(1, 2, ...) 또는 복구 명령을 입력하세요.")))
		return "", true, nil
	}
	action, ok := resolveHarnessRecoveryAction(recovery, result)
	if !ok {
		fmt.Fprintln(rt.writer, rt.ui.warnLine(localizedText(rt.cfg,
			"Could not map that answer to a recovery action.",
			"입력한 답을 복구 선택지에 연결하지 못했습니다.")))
		return "", true, nil
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine(localizedText(rt.cfg,
		"Selected: "+harnessRecoveryActionTitle(rt.cfg, action),
		"선택: "+harnessRecoveryActionTitle(rt.cfg, action))))
	rt.offeringHarnessRecovery = true
	reply, execErr := rt.executeHarnessRecoveryAction(ctx, action)
	rt.offeringHarnessRecovery = false
	if execErr == nil && rt.session != nil && rt.session.PendingHarnessBlockedRecovery != nil &&
		(action.Kind == harnessRecoveryActionReview ||
			action.Kind == harnessRecoveryActionWaive ||
			action.Kind == harnessRecoveryActionModel ||
			action.Kind == harnessRecoveryActionStatus ||
			action.Kind == harnessRecoveryActionDismiss) {
		// Keep the operator in the choice loop when the selected action did not
		// clear the pending recovery by itself.
		followUp, offered, offerErr := rt.offerHarnessBlockedRecoveryChoice(ctx)
		if offered {
			if strings.TrimSpace(followUp) != "" {
				if strings.TrimSpace(reply) != "" {
					reply = strings.TrimSpace(reply) + "\n\n" + strings.TrimSpace(followUp)
				} else {
					reply = followUp
				}
			}
			if offerErr != nil {
				execErr = offerErr
			}
		}
	}
	return reply, true, execErr
}

func (rt *runtimeState) afterAgentReplyMaybeOfferHarnessRecovery(ctx context.Context) {
	if rt == nil {
		return
	}
	followUp, offered, err := rt.offerHarnessBlockedRecoveryChoice(ctx)
	if !offered {
		return
	}
	if err != nil {
		rt.printCommandExecutionError("harness-recovery", err)
	}
	if text := strings.TrimSpace(followUp); text != "" {
		rt.printAssistant(text)
	}
}

// maybeOfferStallRecoveryFromAssistantError synthesizes a pending stall recovery
// card when Reply still returned a classifiable hard error, then offers the same
// interactive choices as soft stall replies.
func (rt *runtimeState) maybeOfferStallRecoveryFromAssistantError(ctx context.Context, err error) {
	if rt == nil || !rt.interactive || err == nil || rt.session == nil {
		return
	}
	cause, detail := classifyAssistantStallError(err)
	if cause == "" {
		return
	}
	if rt.session.PendingHarnessBlockedRecovery == nil {
		base := operatorStallBaseReply(rt.cfg, cause, detail)
		recovery := buildStallBlockedRecoveryWithMode(rt.cfg, rt.session, cause, base, nil, false)
		rt.session.PendingHarnessBlockedRecovery = &recovery
		card := renderHarnessBlockedRecoveryReply(rt.cfg, nil, nil, recovery)
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, strings.TrimSpace(base+"\n\n"+card))
		if rt.store != nil {
			_ = rt.store.Save(rt.session)
		}
	}
	rt.afterAgentReplyMaybeOfferHarnessRecovery(ctx)
}

func harnessBlockedRecoveryUserQuestion(cfg Config, recovery HarnessBlockedRecovery) UserQuestion {
	recovery.Normalize()
	options := make([]UserQuestionOption, 0, len(recovery.Actions))
	for i, action := range recovery.Actions {
		title := harnessRecoveryActionTitle(cfg, action)
		reason := harnessRecoveryActionReason(cfg, action)
		label := firstNonBlankString(action.ID, fmt.Sprintf("option-%d", i+1))
		options = append(options, UserQuestionOption{
			Label:       label,
			Description: strings.TrimSpace(title + " — " + reason),
			Recommended: i == 0,
		})
	}
	question := localizedText(cfg,
		"Choose how to continue. Enter accepts the recommended option.",
		"이어서 진행할 방법을 고르세요. Enter를 누르면 권장 옵션이 선택됩니다.")
	header := localizedText(cfg, "Completion blocked", "완료 차단")
	switch recovery.Cause {
	case harnessRecoveryCauseReadChurn:
		header = localizedText(cfg, "Read loop stopped", "반복 읽기 중단")
	case harnessRecoveryCauseNoProgress:
		header = localizedText(cfg, "No-progress stop", "무진행 중단")
	case harnessRecoveryCauseRepeatedToolCalls:
		header = localizedText(cfg, "Repeated tool calls", "동일 도구 반복")
	case harnessRecoveryCauseRepeatedToolFailure:
		header = localizedText(cfg, "Repeated tool failure", "동일 도구 실패")
	case harnessRecoveryCauseToolLoopLimit:
		header = localizedText(cfg, "Tool-loop limit", "도구 루프 한도")
	case harnessRecoveryCauseCommentaryOnly:
		header = localizedText(cfg, "Commentary-only stop", "진행 설명만 반복")
	case harnessRecoveryCauseLengthStop:
		header = localizedText(cfg, "Output length stop", "출력 길이 중단")
	case harnessRecoveryCauseContentFilter:
		header = localizedText(cfg, "Content filter stop", "Content filter 중단")
	case harnessRecoveryCauseEmptyStop:
		header = localizedText(cfg, "Empty reply stop", "빈 응답 중단")
	case harnessRecoveryCauseFinalGate:
		header = localizedText(cfg, "Confirmation needed before finishing", "완료 전 확인 필요")
	}
	return UserQuestion{
		Header:          header,
		Question:        question,
		Options:         options,
		RequireExplicit: false,
	}
}

func harnessRecoveryActionTitle(cfg Config, action HarnessRecoveryAction) string {
	if localePrefersKorean(cfg) {
		return firstNonBlankString(action.TitleKO, action.TitleEN, action.Command, action.ID)
	}
	return firstNonBlankString(action.TitleEN, action.TitleKO, action.Command, action.ID)
}

func harnessRecoveryActionReason(cfg Config, action HarnessRecoveryAction) string {
	if localePrefersKorean(cfg) {
		return firstNonBlankString(action.ReasonKO, action.ReasonEN)
	}
	return firstNonBlankString(action.ReasonEN, action.ReasonKO)
}

func resolveHarnessRecoveryAction(recovery HarnessBlockedRecovery, result UserQuestionResult) (HarnessRecoveryAction, bool) {
	recovery.Normalize()
	if len(result.Selected) > 0 {
		want := strings.TrimSpace(result.Selected[0])
		for _, action := range recovery.Actions {
			if action.ID == want || action.Command == want || action.TitleEN == want || action.TitleKO == want {
				return action, true
			}
		}
	}
	if custom := strings.TrimSpace(result.Custom); custom != "" {
		if action, ok := matchHarnessRecoveryInput(Config{}, recovery, custom); ok {
			return action, true
		}
	}
	return HarnessRecoveryAction{}, false
}

func matchHarnessRecoveryInput(cfg Config, recovery HarnessBlockedRecovery, input string) (HarnessRecoveryAction, bool) {
	recovery.Normalize()
	text := strings.TrimSpace(input)
	if text == "" || len(recovery.Actions) == 0 {
		return HarnessRecoveryAction{}, false
	}
	if n, err := strconv.Atoi(text); err == nil {
		if n >= 1 && n <= len(recovery.Actions) {
			return recovery.Actions[n-1], true
		}
		return HarnessRecoveryAction{}, false
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "/finish") && strings.Contains(lower, "disclose") {
		return findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionDisclose)
	}
	if lower == "/retry-verify" || strings.HasPrefix(lower, "/retry-verify ") {
		return findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionRetryVerify)
	}
	if lower == "/continue" {
		return harnessRecoveryPrimaryAction(recovery), true
	}
	if lower == "/review" || strings.HasPrefix(lower, "/review ") {
		if action, ok := findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionReview); ok {
			return action, true
		}
	}
	if lower == "/gate clear" || strings.HasPrefix(lower, "/gate clear") ||
		looksLikeHarnessDismissIntent(text) {
		if action, ok := findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionDismiss); ok {
			return action, true
		}
	}
	if looksLikeHarnessContinueEditingIntent(text) {
		if action, ok := findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionContinueEditing); ok {
			return action, true
		}
	}
	if lower == "/model" || strings.HasPrefix(lower, "/model ") {
		return findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionModel)
	}
	if lower == "/status" || strings.HasPrefix(lower, "/status ") {
		return findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionStatus)
	}
	if looksLikeHarnessDiscloseFinishIntent(text) {
		return findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionDisclose)
	}
	if looksLikeHarnessRetryVerifyIntent(text) {
		return findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionRetryVerify)
	}
	if looksLikeHarnessRepairContinueIntent(text) {
		if action, ok := findHarnessRecoveryActionByKind(recovery, harnessRecoveryActionRepair); ok {
			return action, true
		}
		return harnessRecoveryPrimaryAction(recovery), true
	}
	_ = cfg
	return HarnessRecoveryAction{}, false
}

func looksLikeHarnessDismissIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"dismiss", "ignore for now", "ignore this once", "clear the gate",
		"이번만 무시", "무시하고 계속", "무시해", "게이트 해제",
	)
}

func looksLikeHarnessContinueEditingIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"keep editing", "continue editing", "edit only",
		"편집만 계속", "편집 계속", "지금은 편집",
	)
}

func findHarnessRecoveryActionByKind(recovery HarnessBlockedRecovery, kind string) (HarnessRecoveryAction, bool) {
	for _, action := range recovery.Actions {
		if action.Kind == kind {
			return action, true
		}
	}
	return HarnessRecoveryAction{}, false
}

func (rt *runtimeState) executeHarnessRecoveryAction(ctx context.Context, action HarnessRecoveryAction) (string, error) {
	if rt == nil || rt.agent == nil || rt.session == nil {
		return "", fmt.Errorf("no active session")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	kind := strings.TrimSpace(action.Kind)
	switch kind {
	case harnessRecoveryActionDisclose:
		reply, err := rt.agent.finishHarnessBlockedWithDisclosure()
		if err != nil && strings.TrimSpace(reply) != "" {
			_ = rt.store.Save(rt.session)
			return reply, nil
		}
		if err != nil {
			return reply, err
		}
		_ = rt.store.Save(rt.session)
		return reply, nil
	case harnessRecoveryActionRetryVerify:
		if rt.session.PendingHarnessBlockedRecovery == nil {
			return "", fmt.Errorf("no blocked harness recovery is pending")
		}
		fmt.Fprintln(rt.writer, rt.ui.infoLine(localizedText(rt.cfg,
			"Retrying verification, then finishing with recorded evidence...",
			"검증을 다시 실행한 뒤 기록된 근거로 마무리합니다...")))
		return rt.runAgentReplyWithImagesManagedCancel(ctx, retryVerifyRecoveryPrompt(rt.cfg), nil, false)
	case harnessRecoveryActionReview:
		err := rt.handleReviewCommandWithContext(ctx, "")
		if err != nil {
			return "", err
		}
		rt.refreshPendingHarnessBlockedRecoveryAfterAction()
		if rt.session.PendingHarnessBlockedRecovery == nil {
			return localizedText(rt.cfg,
				"Review finished and the harness block is clear.",
				"리뷰를 마쳤고 harness 차단이 해제되었습니다."), nil
		}
		return localizedText(rt.cfg,
			"Review finished. Completion is still blocked — choose the next option.",
			"리뷰를 마쳤습니다. 완료가 여전히 차단되어 있으니 다음 옵션을 고르세요."), nil
	case harnessRecoveryActionWaive:
		reply, err := rt.executeHarnessRecoveryWaive(ctx)
		if err != nil {
			return reply, err
		}
		rt.refreshPendingHarnessBlockedRecoveryAfterAction()
		return reply, nil
	case harnessRecoveryActionModel:
		if err := rt.handleModelCommand(""); err != nil {
			return "", err
		}
		// Model picker may clear or change route; keep recovery so the operator can
		// still choose continue after switching.
		return localizedText(rt.cfg,
			"Model routing updated. Choose continue when you want the agent to resume.",
			"모델 라우팅을 갱신했습니다. 에이전트를 다시 돌리려면 continue를 선택하세요."), nil
	case harnessRecoveryActionStatus:
		detailStatus := true
		_ = detailStatus
		fmt.Fprintln(rt.writer, rt.ui.section("Status"))
		rt.printStatusOverview(runtimeGateActionFinalAnswer)
		rt.printRuntimeGateStatusDetail(runtimeGateActionFinalAnswer)
		return localizedText(rt.cfg,
			"Status details printed above. Choose another recovery option when ready.",
			"위쪽에 상태 상세를 출력했습니다. 준비가 되면 다른 복구 옵션을 고르세요."), nil
	case harnessRecoveryActionDismiss:
		if err := rt.clearRuntimeGate(nil); err != nil {
			return "", err
		}
		rt.refreshPendingHarnessBlockedRecoveryAfterAction()
		if rt.session.PendingHarnessBlockedRecovery == nil {
			return localizedText(rt.cfg,
				"Previous review baggage dismissed. Completion can continue.",
				"이전 리뷰 부담을 해제했습니다. 완료를 이어갈 수 있습니다."), nil
		}
		return localizedText(rt.cfg,
			"Dismiss applied. Completion is still blocked — choose the next option.",
			"무시를 적용했습니다. 완료가 여전히 막혀 있으니 다음 옵션을 고르세요."), nil
	case harnessRecoveryActionContinueEditing:
		rt.agent.clearHarnessBlockedRecovery()
		if rt.store != nil {
			_ = rt.store.Save(rt.session)
		}
		return localizedText(rt.cfg,
			"Okay — keep editing. Completion may still need confirmation later.",
			"알겠습니다. 편집을 이어가세요. 완료는 나중에 다시 확인이 필요할 수 있습니다."), nil
	case harnessRecoveryActionAnswer:
		cause := ""
		if rt.session.PendingHarnessBlockedRecovery != nil {
			cause = rt.session.PendingHarnessBlockedRecovery.Cause
		}
		// Analysis/reporting resume: never force write_file-first edit bias.
		rt.session.StallContinueEditBias = false
		prompt := buildStallContinueRecoveryPromptForAction(rt.cfg, rt.session, cause, harnessRecoveryActionAnswer)
		fmt.Fprintln(rt.writer, rt.ui.infoLine(localizedText(rt.cfg,
			"Resuming with an answer from evidence already gathered...",
			"이미 모은 근거로 답변을 이어갑니다...")))
		return rt.runAgentReplyWithImagesManagedCancel(ctx, prompt, nil, false)
	case harnessRecoveryActionRepair, "":
		cause := ""
		if rt.session.PendingHarnessBlockedRecovery != nil {
			cause = rt.session.PendingHarnessBlockedRecovery.Cause
		}
		prompt := repairBlockedRecoveryPrompt(rt.cfg)
		infoEN := "Resuming repair for remaining blockers..."
		infoKO := "남은 blocker 수정을 재개합니다..."
		if cause != "" && cause != harnessRecoveryCauseHarness {
			// If the stalled turn was analysis-only, keep the thin-agent path even
			// when the operator picks a generic continue (legacy primary /continue).
			kind := harnessRecoveryActionRepair
			if requestLooksLikeAnalysisOnlyTurn(preservableSessionAcceptancePrompt(rt.session)) {
				kind = harnessRecoveryActionAnswer
				rt.session.StallContinueEditBias = false
				infoEN = "Resuming the original analysis task..."
				infoKO = "원래 분석 작업을 재개합니다..."
			} else {
				rt.session.StallContinueEditBias = true
				infoEN = "Resuming the original task with focused edits..."
				infoKO = "원래 작업을 focused 수정으로 재개합니다..."
			}
			prompt = buildStallContinueRecoveryPromptForAction(rt.cfg, rt.session, cause, kind)
		}
		fmt.Fprintln(rt.writer, rt.ui.infoLine(localizedText(rt.cfg, infoEN, infoKO)))
		return rt.runAgentReplyWithImagesManagedCancel(ctx, prompt, nil, false)
	default:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(action.Command)), "/review") {
			err := rt.handleReviewCommandWithContext(ctx, strings.TrimSpace(strings.TrimPrefix(action.Command, "/review")))
			if err != nil {
				return "", err
			}
			return localizedText(rt.cfg, "Review command finished.", "리뷰 명령을 마쳤습니다."), nil
		}
		return "", fmt.Errorf("unsupported harness recovery action %q", firstNonBlankString(kind, action.ID, action.Command))
	}
}

func (rt *runtimeState) executeHarnessRecoveryWaive(ctx context.Context) (string, error) {
	_ = ctx
	if rt == nil {
		return "", fmt.Errorf("no active session")
	}
	findingID := ""
	reason := ""
	if rt.interactive {
		idResult, err := rt.promptUserText(UserTextQuestion{
			Header:      localizedText(rt.cfg, "Waive finding", "finding 예외 처리"),
			Question:    localizedText(rt.cfg, "Finding id to waive", "예외 처리할 finding id"),
			Placeholder: "RF-1",
			Required:    true,
			MaxLength:   128,
		})
		if err != nil {
			return "", err
		}
		if idResult.Canceled || strings.TrimSpace(idResult.Text) == "" {
			return localizedText(rt.cfg, "Waive canceled.", "예외 처리를 취소했습니다."), nil
		}
		findingID = strings.TrimSpace(idResult.Text)
		reasonResult, err := rt.promptUserText(UserTextQuestion{
			Header:      localizedText(rt.cfg, "Waive finding", "finding 예외 처리"),
			Question:    localizedText(rt.cfg, "Reason for the waiver", "예외 처리 사유"),
			Placeholder: localizedText(rt.cfg, "confirmed false positive", "확인된 오탐"),
			Required:    true,
			MaxLength:   1024,
		})
		if err != nil {
			return "", err
		}
		if reasonResult.Canceled || strings.TrimSpace(reasonResult.Text) == "" {
			return localizedText(rt.cfg, "Waive canceled.", "예외 처리를 취소했습니다."), nil
		}
		reason = strings.TrimSpace(reasonResult.Text)
	} else {
		return "", fmt.Errorf("usage: /review waive <finding-id> --reason <text>")
	}
	if err := rt.handleReviewWaiverCommand(fmt.Sprintf("waive %s --reason %s", findingID, quoteCommandArg(reason))); err != nil {
		return "", err
	}
	return localizedText(rt.cfg,
		"Waiver recorded for "+findingID+".",
		findingID+"에 대한 예외 처리를 기록했습니다."), nil
}

func quoteCommandArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\"'") {
		return value
	}
	return strconv.Quote(value)
}

func (rt *runtimeState) refreshPendingHarnessBlockedRecoveryAfterAction() {
	if rt == nil || rt.session == nil || rt.agent == nil {
		return
	}
	ledger := rt.agent.refreshRuntimeGateLedger(runtimeGateActionFinalAnswer)
	report := rt.session.LastCodingHarnessReport
	if (report == nil || report.Approved) && !runtimeGateBlocksAction(ledger) {
		rt.agent.clearHarnessBlockedRecovery()
		_ = rt.store.Save(rt.session)
		return
	}
	prev := rt.session.PendingHarnessBlockedRecovery
	candidate := ""
	attempted := false
	unresolved := false
	files := []string(nil)
	if prev != nil {
		candidate = prev.CandidateReply
		attempted = prev.AttemptedEditTool
		unresolved = prev.UnresolvedVerify
		files = append([]string(nil), prev.BlockerTitles...)
	}
	if prev != nil && prev.Cause == harnessRecoveryCauseFinalGate {
		updated := buildFinalGateBlockedRecovery(rt.cfg, rt.session, candidate, files)
		rt.session.PendingHarnessBlockedRecovery = &updated
		_ = rt.store.Save(rt.session)
		return
	}
	updated := buildHarnessBlockedRecovery(rt.cfg, report, nonHarnessLedgerBlockers(ledger), candidate, attempted, unresolved)
	rt.session.PendingHarnessBlockedRecovery = &updated
	_ = rt.store.Save(rt.session)
}
