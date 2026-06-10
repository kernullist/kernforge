package main

import (
	"fmt"
	"strings"
	"time"
)

type TurnRuntimeStateName string

const (
	TurnRuntimeNeedModelTurn         TurnRuntimeStateName = "NeedModelTurn"
	TurnRuntimeNeedToolExecution     TurnRuntimeStateName = "NeedToolExecution"
	TurnRuntimeNeedRecoveryModelTurn TurnRuntimeStateName = "NeedRecoveryModelTurn"
	TurnRuntimeNeedFinalGate         TurnRuntimeStateName = "NeedFinalGate"
	TurnRuntimeCompleted             TurnRuntimeStateName = "Completed"
	TurnRuntimeBlocked               TurnRuntimeStateName = "Blocked"
)

type RuntimeInterventionKind string

const (
	RuntimeInterventionBlockedTool            RuntimeInterventionKind = "BlockedTool"
	RuntimeInterventionRepeatedTool           RuntimeInterventionKind = "RepeatedTool"
	RuntimeInterventionEmptyStop              RuntimeInterventionKind = "EmptyStop"
	RuntimeInterventionLengthStop             RuntimeInterventionKind = "LengthStop"
	RuntimeInterventionCommentaryOnly         RuntimeInterventionKind = "CommentaryOnly"
	RuntimeInterventionManualEditHandoff      RuntimeInterventionKind = "ManualEditHandoff"
	RuntimeInterventionVerificationUnresolved RuntimeInterventionKind = "VerificationUnresolved"
	RuntimeInterventionFinalLooksPremature    RuntimeInterventionKind = "FinalLooksPremature"
)

type RuntimeIntervention struct {
	Kind       RuntimeInterventionKind `json:"kind"`
	Reason     string                  `json:"reason,omitempty"`
	Guidance   string                  `json:"guidance,omitempty"`
	ToolCalls  []ToolCall              `json:"tool_calls,omitempty"`
	StopReason string                  `json:"stop_reason,omitempty"`
	Count      int                     `json:"count,omitempty"`
	Iteration  int                     `json:"iteration,omitempty"`
	Resolved   bool                    `json:"resolved,omitempty"`
	CreatedAt  time.Time               `json:"created_at,omitempty"`
	ResolvedAt time.Time               `json:"resolved_at,omitempty"`
}

type TurnRuntimeCounters struct {
	EmptyFinalReplies                 int `json:"empty_final_replies,omitempty"`
	FinalAnswerNudges                 int `json:"final_answer_nudges,omitempty"`
	RepeatedToolCallNudges            int `json:"repeated_tool_call_nudges,omitempty"`
	RepeatedToolCallRecoveryTurns     int `json:"repeated_tool_call_recovery_turns,omitempty"`
	RepeatedReadFilePathNudges        int `json:"repeated_read_file_path_nudges,omitempty"`
	RepeatedCachedReadFileNudges      int `json:"repeated_cached_read_file_nudges,omitempty"`
	RepeatedReadFilePathRecoveryCount int `json:"repeated_read_file_path_recovery_count,omitempty"`
	ManualEditHandoffRetries          int `json:"manual_edit_handoff_retries,omitempty"`
	CommentaryOnlyReplies             int `json:"commentary_only_replies,omitempty"`
	LengthStopReplies                 int `json:"length_stop_replies,omitempty"`
	FinalAnswerReviewRevisions        int `json:"final_answer_review_revisions,omitempty"`
	RuntimeGateFinalAnswerRevisions   int `json:"runtime_gate_final_answer_revisions,omitempty"`
}

type TurnRuntimeState struct {
	State                     TurnRuntimeStateName  `json:"state"`
	RequestEnvelope           RequestEnvelope       `json:"request_envelope,omitempty"`
	Counters                  TurnRuntimeCounters   `json:"counters,omitempty"`
	Interventions             []RuntimeIntervention `json:"interventions,omitempty"`
	UnresolvedVerification    bool                  `json:"unresolved_verification,omitempty"`
	FinalAnswerOnlyCorrection bool                  `json:"final_answer_only_correction,omitempty"`
	LastTransitionReason      string                `json:"last_transition_reason,omitempty"`
	UpdatedAt                 time.Time             `json:"updated_at,omitempty"`
}

type TurnRuntimeFinalContext struct {
	AttemptedEditTool              bool
	ExplicitEditRequest            bool
	GeneratedDocumentHarnessOwnsIt bool
}

type TurnRuntimeFinalReadiness struct {
	Ready        bool
	BlockedBy    []RuntimeIntervention
	Reason       string
	Guidance     string
	RuntimeState TurnRuntimeStateName
}

func (r TurnRuntimeFinalReadiness) BlockedOnlyBy(kind RuntimeInterventionKind) bool {
	if r.Ready || len(r.BlockedBy) == 0 {
		return false
	}
	kind = normalizeRuntimeInterventionKind(kind)
	for _, item := range r.BlockedBy {
		if item.Kind != kind {
			return false
		}
	}
	return true
}

func NewTurnRuntimeState(envelope RequestEnvelope) *TurnRuntimeState {
	state := &TurnRuntimeState{
		State:           TurnRuntimeNeedModelTurn,
		RequestEnvelope: envelope,
		UpdatedAt:       time.Now(),
	}
	return state
}

func (s *TurnRuntimeState) Transition(next TurnRuntimeStateName, reason string) {
	if s == nil {
		return
	}
	if next == "" {
		next = TurnRuntimeNeedModelTurn
	}
	s.State = next
	s.LastTransitionReason = strings.TrimSpace(reason)
	s.UpdatedAt = time.Now()
}

func (s *TurnRuntimeState) RecordIntervention(item RuntimeIntervention) RuntimeIntervention {
	if s == nil {
		return item
	}
	item.Kind = normalizeRuntimeInterventionKind(item.Kind)
	item.Reason = strings.TrimSpace(item.Reason)
	item.Guidance = strings.TrimSpace(item.Guidance)
	item.StopReason = normalizeStopReason(item.StopReason)
	item.ToolCalls = cloneRuntimeInterventionToolCalls(item.ToolCalls)
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	s.Interventions = append(s.Interventions, item)
	s.Transition(runtimeInterventionNextState(item.Kind), string(item.Kind))
	return item
}

func (s *TurnRuntimeState) RecordInterventionKind(kind RuntimeInterventionKind, reason string, guidance string, calls []ToolCall) RuntimeIntervention {
	item := RuntimeIntervention{
		Kind:      kind,
		Reason:    reason,
		Guidance:  guidance,
		ToolCalls: calls,
	}
	return s.RecordIntervention(item)
}

func (s *TurnRuntimeState) RecordRepeatedTool(kind RuntimeInterventionKind, reason string, guidance string, calls []ToolCall, count int) RuntimeIntervention {
	item := RuntimeIntervention{
		Kind:      kind,
		Reason:    reason,
		Guidance:  guidance,
		ToolCalls: calls,
		Count:     count,
	}
	return s.RecordIntervention(item)
}

func (s *TurnRuntimeState) RecordStopIntervention(kind RuntimeInterventionKind, stopReason string, reason string, guidance string) RuntimeIntervention {
	item := RuntimeIntervention{
		Kind:       kind,
		Reason:     reason,
		Guidance:   guidance,
		StopReason: stopReason,
	}
	return s.RecordIntervention(item)
}

func (s *TurnRuntimeState) MarkRecoveryModelTurnStarted() {
	if s == nil {
		return
	}
	for i := range s.Interventions {
		if s.Interventions[i].Resolved {
			continue
		}
		if runtimeInterventionResolvedByRecoveryTurn(s.Interventions[i].Kind) {
			s.Interventions[i].Resolved = true
			s.Interventions[i].ResolvedAt = time.Now()
		}
	}
	s.Transition(TurnRuntimeNeedModelTurn, "recovery_model_turn_started")
}

func (s *TurnRuntimeState) ResolveFinalAnswerInterventions(reply string, ctx TurnRuntimeFinalContext) {
	if s == nil {
		return
	}
	for i := range s.Interventions {
		if s.Interventions[i].Resolved {
			continue
		}
		switch s.Interventions[i].Kind {
		case RuntimeInterventionVerificationUnresolved:
			if ctx.GeneratedDocumentHarnessOwnsIt || replyMentionsVerificationBlocker(reply) || replyMentionsVerificationNotRun(reply) {
				s.resolveInterventionAt(i)
			}
		case RuntimeInterventionManualEditHandoff:
			if ctx.AttemptedEditTool || !replySuggestsManualEditHandoff(reply) {
				s.resolveInterventionAt(i)
			}
		case RuntimeInterventionFinalLooksPremature:
			if strings.TrimSpace(reply) != "" {
				s.resolveInterventionAt(i)
			}
		}
	}
}

func (s *TurnRuntimeState) FinalAnswerReadiness(reply string, ctx TurnRuntimeFinalContext) TurnRuntimeFinalReadiness {
	readiness := TurnRuntimeFinalReadiness{
		Ready:        true,
		RuntimeState: TurnRuntimeNeedFinalGate,
	}
	if s == nil {
		return readiness
	}
	s.ResolveFinalAnswerInterventions(reply, ctx)
	blocked := make([]RuntimeIntervention, 0)
	for _, item := range s.Interventions {
		if item.Resolved {
			continue
		}
		if runtimeInterventionBlocksFinalAnswer(item.Kind) {
			blocked = append(blocked, item)
		}
	}
	if len(blocked) == 0 {
		return readiness
	}
	readiness.Ready = false
	readiness.BlockedBy = blocked
	readiness.RuntimeState = s.State
	readiness.Reason = runtimeFinalReadinessReason(blocked)
	readiness.Guidance = runtimeFinalReadinessGuidance(blocked)
	return readiness
}

func (s *TurnRuntimeState) HasUnresolvedIntervention(kind RuntimeInterventionKind) bool {
	if s == nil {
		return false
	}
	kind = normalizeRuntimeInterventionKind(kind)
	for _, item := range s.Interventions {
		if item.Kind == kind && !item.Resolved {
			return true
		}
	}
	return false
}

func (s *TurnRuntimeState) LastIntervention() RuntimeIntervention {
	if s == nil || len(s.Interventions) == 0 {
		return RuntimeIntervention{}
	}
	return s.Interventions[len(s.Interventions)-1]
}

func (s *TurnRuntimeState) resolveInterventionAt(index int) {
	if s == nil || index < 0 || index >= len(s.Interventions) || s.Interventions[index].Resolved {
		return
	}
	s.Interventions[index].Resolved = true
	s.Interventions[index].ResolvedAt = time.Now()
	s.UpdatedAt = time.Now()
}

func runtimeInterventionNextState(kind RuntimeInterventionKind) TurnRuntimeStateName {
	switch normalizeRuntimeInterventionKind(kind) {
	case RuntimeInterventionBlockedTool,
		RuntimeInterventionRepeatedTool,
		RuntimeInterventionEmptyStop,
		RuntimeInterventionLengthStop,
		RuntimeInterventionCommentaryOnly,
		RuntimeInterventionManualEditHandoff,
		RuntimeInterventionVerificationUnresolved,
		RuntimeInterventionFinalLooksPremature:
		return TurnRuntimeNeedRecoveryModelTurn
	default:
		return TurnRuntimeNeedModelTurn
	}
}

func runtimeInterventionResolvedByRecoveryTurn(kind RuntimeInterventionKind) bool {
	switch normalizeRuntimeInterventionKind(kind) {
	case RuntimeInterventionBlockedTool,
		RuntimeInterventionRepeatedTool,
		RuntimeInterventionEmptyStop,
		RuntimeInterventionLengthStop,
		RuntimeInterventionCommentaryOnly,
		RuntimeInterventionFinalLooksPremature:
		return true
	default:
		return false
	}
}

func runtimeInterventionBlocksFinalAnswer(kind RuntimeInterventionKind) bool {
	switch normalizeRuntimeInterventionKind(kind) {
	case RuntimeInterventionVerificationUnresolved,
		RuntimeInterventionManualEditHandoff,
		RuntimeInterventionFinalLooksPremature:
		return true
	default:
		return false
	}
}

func runtimeFinalReadinessReason(items []RuntimeIntervention) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Kind == "" {
			continue
		}
		reason := strings.TrimSpace(item.Reason)
		if reason == "" {
			reason = string(item.Kind)
		}
		parts = append(parts, fmt.Sprintf("%s: %s", item.Kind, reason))
	}
	return strings.Join(parts, "; ")
}

func runtimeFinalReadinessGuidance(items []RuntimeIntervention) string {
	for _, item := range items {
		if guidance := strings.TrimSpace(item.Guidance); guidance != "" {
			return guidance
		}
		switch normalizeRuntimeInterventionKind(item.Kind) {
		case RuntimeInterventionVerificationUnresolved:
			return RenderVerificationUnresolvedPrompt(nil, item, "", false)
		case RuntimeInterventionManualEditHandoff:
			return "This request explicitly asks for an edit. Do not hand the patch back to the user unless an edit tool failed and you cite the exact failure."
		case RuntimeInterventionFinalLooksPremature:
			return "The previous answer looked final while runtime recovery was still unresolved. Continue the recovery step before concluding."
		}
	}
	return "Runtime state still has unresolved interventions. Resolve them before providing the final answer."
}

func ensureRuntimeVerificationNotRunDisclosure(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" || replyMentionsVerificationNotRun(reply) || replyMentionsVerificationBlocker(reply) {
		return reply
	}
	return reply + "\n\nValidation: verification was not run."
}

func normalizeRuntimeInterventionKind(kind RuntimeInterventionKind) RuntimeInterventionKind {
	switch kind {
	case RuntimeInterventionBlockedTool,
		RuntimeInterventionRepeatedTool,
		RuntimeInterventionEmptyStop,
		RuntimeInterventionLengthStop,
		RuntimeInterventionCommentaryOnly,
		RuntimeInterventionManualEditHandoff,
		RuntimeInterventionVerificationUnresolved,
		RuntimeInterventionFinalLooksPremature:
		return kind
	default:
		return RuntimeInterventionBlockedTool
	}
}

func cloneRuntimeInterventionToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, len(calls))
	copy(out, calls)
	return out
}

func (a *Agent) emitRuntimeInterventionProgress(state *TurnRuntimeState, item RuntimeIntervention) {
	if a == nil || item.Kind == "" {
		return
	}
	runtimeState := ""
	if state != nil {
		runtimeState = string(state.State)
	}
	a.emitProgressEvent(ProgressEvent{
		Kind:                progressKindRuntimeIntervention,
		RuntimeState:        runtimeState,
		RuntimeIntervention: string(item.Kind),
		Status:              firstNonEmptyLine(item.Reason),
	})
}
