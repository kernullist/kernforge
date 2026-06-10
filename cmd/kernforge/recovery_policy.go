package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type RecoveryKind string

const (
	RecoveryKindEmptyStop              RecoveryKind = "empty_stop"
	RecoveryKindCommentaryOnly         RecoveryKind = "commentary_only"
	RecoveryKindLengthStop             RecoveryKind = "length_stop"
	RecoveryKindRetryableProviderError RecoveryKind = "retryable_provider_error"
	RecoveryKindContextOverflow        RecoveryKind = "context_overflow"
	RecoveryKindCompaction             RecoveryKind = "compaction"
	RecoveryKindBlocked                RecoveryKind = "blocked"
)

type RecoveryAction string

const (
	RecoveryActionNone         RecoveryAction = "none"
	RecoveryActionRetry        RecoveryAction = "retry"
	RecoveryActionContinue     RecoveryAction = "continue"
	RecoveryActionBackoff      RecoveryAction = "backoff"
	RecoveryActionCompact      RecoveryAction = "compact"
	RecoveryActionPromoteModel RecoveryAction = "promote_model"
	RecoveryActionHandoff      RecoveryAction = "handoff"
	RecoveryActionBlock        RecoveryAction = "block"
)

type ContextMaintenanceAction string

const (
	ContextMaintenanceNone         ContextMaintenanceAction = "none"
	ContextMaintenanceCompact      ContextMaintenanceAction = "compact"
	ContextMaintenancePromoteModel ContextMaintenanceAction = "promote_model"
	ContextMaintenanceHandoff      ContextMaintenanceAction = "handoff"
	ContextMaintenanceBlock        ContextMaintenanceAction = "block"
)

type RecoveryDecision struct {
	Kind        RecoveryKind   `json:"kind"`
	Action      RecoveryAction `json:"action"`
	Reason      string         `json:"reason,omitempty"`
	Guidance    string         `json:"guidance,omitempty"`
	StopReason  string         `json:"stop_reason,omitempty"`
	Retryable   bool           `json:"retryable,omitempty"`
	Attempt     int            `json:"attempt,omitempty"`
	MaxAttempts int            `json:"max_attempts,omitempty"`
	Backoff     time.Duration  `json:"backoff,omitempty"`
	MaxDelay    time.Duration  `json:"max_delay,omitempty"`
	CreatedAt   time.Time      `json:"created_at,omitempty"`
}

type RecoveryPolicyInput struct {
	Kind             RecoveryKind
	StopReason       string
	Attempt          int
	MaxAttempts      int
	ProviderError    error
	ApproxChars      int
	Threshold        int
	CanCompact       bool
	CanPromoteModel  bool
	CanHandoff       bool
	BaseDelay        time.Duration
	MaxDelay         time.Duration
	AssistantText    string
	HasToolCalls     bool
	RetryAfter       time.Duration
	RetryAfterSource string
}

type ContextMaintenanceDecision struct {
	Action      ContextMaintenanceAction `json:"action"`
	Trigger     RecoveryKind             `json:"trigger,omitempty"`
	Reason      string                   `json:"reason,omitempty"`
	Guidance    string                   `json:"guidance,omitempty"`
	ApproxChars int                      `json:"approx_chars,omitempty"`
	Threshold   int                      `json:"threshold,omitempty"`
	CreatedAt   time.Time                `json:"created_at,omitempty"`
}

type ContextMaintenanceInput struct {
	Trigger         RecoveryKind
	ApproxChars     int
	Threshold       int
	CanCompact      bool
	CanPromoteModel bool
	CanHandoff      bool
	Reason          string
}

type recoveryTrigger string

const (
	recoveryTriggerRepeatedToolCalls  recoveryTrigger = "repeated_tool_calls"
	recoveryTriggerRepeatedReadFile   recoveryTrigger = "repeated_read_file"
	recoveryTriggerRepeatedToolError  recoveryTrigger = "repeated_tool_error"
	recoveryTriggerToolBudgetExceeded recoveryTrigger = "tool_budget_exceeded"
)

type recoveryInput struct {
	Summary string
	Recent  string
	Detail  string
	Path    string
	Turns   int
}

func DecideRecovery(input RecoveryPolicyInput) RecoveryDecision {
	kind := input.Kind
	if kind == "" && input.ProviderError != nil {
		kind = classifyProviderErrorRecoveryKind(input.ProviderError)
	}
	if kind == "" {
		kind = RecoveryKindBlocked
	}
	maxAttempts := input.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 2
	}
	maxDelay := input.MaxDelay
	if maxDelay <= 0 {
		maxDelay = maxProviderRetryDelay
	}
	decision := RecoveryDecision{
		Kind:        kind,
		StopReason:  strings.TrimSpace(input.StopReason),
		Attempt:     input.Attempt,
		MaxAttempts: maxAttempts,
		MaxDelay:    maxDelay,
		CreatedAt:   time.Now(),
	}
	switch kind {
	case RecoveryKindEmptyStop:
		if input.Attempt >= maxAttempts {
			decision.Action = RecoveryActionBlock
			decision.Reason = "model returned repeated empty responses"
			decision.Guidance = "The model returned no assistant text and no tool calls after the retry cap. Retry with a smaller request, switch model/provider, or inspect provider logs."
			return decision
		}
		decision.Action = RecoveryActionRetry
		decision.Retryable = true
		decision.Reason = "model returned no assistant text and no tool calls"
		decision.Guidance = "Ask for the next needed tool call or a concrete final answer; do not accept an empty turn as success."
		return decision
	case RecoveryKindCommentaryOnly:
		if input.Attempt >= maxAttempts {
			decision.Action = RecoveryActionBlock
			decision.Reason = "model repeated commentary-only assistant messages"
			decision.Guidance = "The model kept returning progress text without tool calls or a final answer. Retry with a stronger instruction or switch model/provider."
			return decision
		}
		decision.Action = RecoveryActionContinue
		decision.Retryable = true
		decision.Reason = "model returned commentary-only text"
		decision.Guidance = "Your last assistant message was commentary/progress, not the final answer. Continue with the next required tool call or provide the final answer; do not repeat progress-only commentary."
		return decision
	case RecoveryKindLengthStop:
		if input.CanCompact {
			decision.Action = RecoveryActionCompact
			decision.Retryable = true
			decision.Reason = "model stopped at output length limit and context can be compacted"
			decision.Guidance = RenderLengthStopContinuePrompt(input.StopReason)
			return decision
		}
		decision.Action = RecoveryActionContinue
		decision.Retryable = true
		decision.Reason = "model stopped at output length limit"
		decision.Guidance = RenderLengthStopContinuePrompt(input.StopReason)
		return decision
	case RecoveryKindContextOverflow:
		ctxDecision := DecideContextMaintenance(ContextMaintenanceInput{
			Trigger:         RecoveryKindContextOverflow,
			ApproxChars:     input.ApproxChars,
			Threshold:       input.Threshold,
			CanCompact:      input.CanCompact,
			CanPromoteModel: input.CanPromoteModel,
			CanHandoff:      input.CanHandoff,
		})
		decision.Action = recoveryActionForContextMaintenance(ctxDecision.Action)
		decision.Retryable = decision.Action != RecoveryActionBlock
		decision.Reason = ctxDecision.Reason
		decision.Guidance = ctxDecision.Guidance
		return decision
	case RecoveryKindCompaction:
		decision.Action = RecoveryActionCompact
		decision.Retryable = true
		decision.Reason = "conversation crossed the proactive compaction threshold"
		decision.Guidance = "Compact conversation history before the next model request and reset provider state."
		return decision
	case RecoveryKindRetryableProviderError:
		baseDelay := input.BaseDelay
		if baseDelay <= 0 {
			baseDelay = 1500 * time.Millisecond
		}
		delay := input.RetryAfter
		if delay <= 0 {
			delay = providerRetryDelay(baseDelay, input.Attempt)
		}
		if delay > maxDelay {
			decision.Kind = RecoveryKindBlocked
			decision.Action = RecoveryActionBlock
			decision.Reason = "retry delay exceeded max provider backoff"
			decision.Backoff = delay
			decision.Guidance = "Provider retry delay is too large for an interactive turn. Wait for the limit to reset, switch model/provider, or reduce request size."
			return decision
		}
		decision.Action = RecoveryActionBackoff
		decision.Retryable = true
		decision.Backoff = delay
		decision.Reason = "provider error is retryable"
		if input.RetryAfterSource != "" {
			decision.Reason += " via " + strings.TrimSpace(input.RetryAfterSource)
		}
		decision.Guidance = "Retry after backoff. If retries are exhausted, switch model/provider or reduce the request."
		return decision
	case RecoveryKindBlocked:
		if providerErrorLooksLikeUsageLimit(input.ProviderError) {
			if input.CanPromoteModel {
				decision.Action = RecoveryActionPromoteModel
				decision.Retryable = true
				decision.Reason = "provider usage limit can fall back to another model route"
				decision.Guidance = "Switch to an available fallback model or provider route before retrying."
				return decision
			}
			if input.CanHandoff {
				decision.Action = RecoveryActionHandoff
				decision.Reason = "provider usage limit requires user handoff"
				decision.Guidance = "Ask the user to change model/provider, wait for quota reset, or provide a smaller request."
				return decision
			}
			decision.Action = RecoveryActionBlock
			decision.Reason = "provider usage limit has no fallback route"
			decision.Guidance = "Usage limits blocked the turn. Wait for quota reset, switch model/provider, or reduce the request."
			return decision
		}
		if input.CanPromoteModel {
			decision.Action = RecoveryActionPromoteModel
			decision.Retryable = true
			decision.Reason = "provider failure can fall back to another model route"
			decision.Guidance = "Retry through a fallback model or provider route."
			return decision
		}
		decision.Action = RecoveryActionBlock
		decision.Reason = "recovery policy blocked the turn"
		decision.Guidance = "The turn cannot continue safely without user action or a provider/model change."
		return decision
	default:
		decision.Kind = RecoveryKindBlocked
		decision.Action = RecoveryActionBlock
		decision.Reason = "recovery policy blocked the turn"
		decision.Guidance = "The turn cannot continue safely without user action or a provider/model change."
		return decision
	}
}

func DecideContextMaintenance(input ContextMaintenanceInput) ContextMaintenanceDecision {
	decision := ContextMaintenanceDecision{
		Trigger:     input.Trigger,
		ApproxChars: input.ApproxChars,
		Threshold:   input.Threshold,
		CreatedAt:   time.Now(),
	}
	reason := strings.TrimSpace(input.Reason)
	switch input.Trigger {
	case RecoveryKindCompaction:
		if input.CanCompact && input.Threshold > 0 && input.ApproxChars > input.Threshold {
			decision.Action = ContextMaintenanceCompact
			decision.Reason = firstNonEmptyTrimmed(reason, "conversation crossed auto-compaction threshold")
			decision.Guidance = "Compact history before issuing another provider request."
			return decision
		}
		decision.Action = ContextMaintenanceNone
		decision.Reason = firstNonEmptyTrimmed(reason, "conversation is below auto-compaction threshold")
		return decision
	case RecoveryKindContextOverflow:
		if input.CanCompact {
			decision.Action = ContextMaintenanceCompact
			decision.Reason = firstNonEmptyTrimmed(reason, "input exceeded provider context and can be compacted")
			decision.Guidance = "Compact history, reset provider state, then retry the request."
			return decision
		}
		if input.CanPromoteModel {
			decision.Action = ContextMaintenancePromoteModel
			decision.Reason = firstNonEmptyTrimmed(reason, "input exceeded provider context and a larger model route is available")
			decision.Guidance = "Promote to a larger context model route before retrying."
			return decision
		}
		if input.CanHandoff {
			decision.Action = ContextMaintenanceHandoff
			decision.Reason = firstNonEmptyTrimmed(reason, "input exceeded provider context and should be handed off")
			decision.Guidance = "Hand off with the current summary and ask the user to resume in a fresh session."
			return decision
		}
		decision.Action = ContextMaintenanceBlock
		decision.Reason = firstNonEmptyTrimmed(reason, "input exceeded provider context and no recovery route is available")
		decision.Guidance = "Reduce the request, compact manually, or switch to a model with a larger context window."
		return decision
	case RecoveryKindLengthStop:
		if input.CanCompact && input.Threshold > 0 && input.ApproxChars > input.Threshold/2 {
			decision.Action = ContextMaintenanceCompact
			decision.Reason = firstNonEmptyTrimmed(reason, "output stopped at length while context is large")
			decision.Guidance = "Compact before asking the model to continue."
			return decision
		}
		decision.Action = ContextMaintenanceNone
		decision.Reason = firstNonEmptyTrimmed(reason, "output stopped at length but continuation can fit")
		decision.Guidance = "Ask the model to continue exactly where it stopped."
		return decision
	default:
		decision.Action = ContextMaintenanceNone
		decision.Reason = firstNonEmptyTrimmed(reason, "no context maintenance required")
		return decision
	}
}

func classifyProviderErrorRecoveryKind(err error) RecoveryKind {
	if err == nil {
		return RecoveryKindBlocked
	}
	if providerErrorLooksLikeContextOverflow(err) {
		return RecoveryKindContextOverflow
	}
	apiErr := &ProviderAPIError{}
	if errors.As(err, &apiErr) && apiErr.Retryable() {
		return RecoveryKindRetryableProviderError
	}
	if providerErrorLooksRetryable(0, "", err.Error(), "", "") {
		return RecoveryKindRetryableProviderError
	}
	return RecoveryKindBlocked
}

func providerErrorLooksLikeContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	apiErr := &ProviderAPIError{}
	if errors.As(err, &apiErr) {
		text := strings.ToLower(strings.Join([]string{
			apiErr.Message,
			apiErr.ErrorType,
			apiErr.Code,
			apiErr.RawBody,
		}, " "))
		return containsAny(text, "context_length_exceeded", "context window exceeded", "maximum context", "context length")
	}
	text := strings.ToLower(err.Error())
	return containsAny(text, "context_length_exceeded", "context window exceeded", "maximum context", "context length")
}

func providerErrorLooksLikeUsageLimit(err error) bool {
	if err == nil {
		return false
	}
	apiErr := &ProviderAPIError{}
	if errors.As(err, &apiErr) {
		text := strings.ToLower(strings.Join([]string{
			apiErr.Message,
			apiErr.ErrorType,
			apiErr.Code,
			apiErr.RawBody,
			apiErr.RateLimitReachedType,
		}, " "))
		return containsAny(text, "insufficient_quota", "usage_not_included", "quota exceeded", "usage not included", "billing", "hard limit")
	}
	text := strings.ToLower(err.Error())
	return containsAny(text, "insufficient_quota", "usage_not_included", "quota exceeded", "usage not included", "billing", "hard limit")
}

func recoveryActionForContextMaintenance(action ContextMaintenanceAction) RecoveryAction {
	switch action {
	case ContextMaintenanceCompact:
		return RecoveryActionCompact
	case ContextMaintenancePromoteModel:
		return RecoveryActionPromoteModel
	case ContextMaintenanceHandoff:
		return RecoveryActionHandoff
	case ContextMaintenanceBlock:
		return RecoveryActionBlock
	default:
		return RecoveryActionNone
	}
}

func (a *Agent) recordRecoveryDecision(decision RecoveryDecision) RecoveryDecision {
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = time.Now()
	}
	if a != nil && a.Session != nil {
		a.Session.LastRecoveryDecision = &decision
	}
	return decision
}

func (a *Agent) recordContextMaintenanceDecision(decision ContextMaintenanceDecision) ContextMaintenanceDecision {
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = time.Now()
	}
	if a != nil && a.Session != nil {
		a.Session.LastContextMaintenanceDecision = &decision
	}
	return decision
}

func (a *Agent) resetProviderStateAfterHistoryMutation(reason string) {
	if a == nil || a.Session == nil {
		return
	}
	a.Session.ResetProviderState(reason)
}

func (a *Agent) recoveryGuidance(ctx context.Context, trigger recoveryTrigger, input recoveryInput) string {
	reason := string(trigger)
	fallback := recoveryFallbackText(trigger, input)
	return a.buildRecoveryGuidance(ctx, reason, fallback, input.Recent, input.Detail)
}

func recoveryFallbackText(trigger recoveryTrigger, input recoveryInput) string {
	switch trigger {
	case recoveryTriggerRepeatedToolCalls:
		return repeatedToolCallRecoveryGuidance(input.Summary, input.Recent)
	case recoveryTriggerRepeatedReadFile:
		return repeatedReadFilePathRecoveryGuidance(input.Path, input.Turns, input.Recent)
	case recoveryTriggerRepeatedToolError:
		return repeatedToolFailureRecoveryGuidance(input.Detail, input.Recent)
	case recoveryTriggerToolBudgetExceeded:
		return toolLoopLimitRecoveryGuidance(input.Summary, input.Detail, input.Recent)
	default:
		return fmt.Sprintf("Recovery mode: %s", input.Detail)
	}
}
