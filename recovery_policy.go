package main

import (
	"context"
	"fmt"
)

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
