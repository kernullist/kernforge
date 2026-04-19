package main

import "strings"

type ToolExecutionPolicyOutcome struct {
	OwnerNodeID          string
	Effect               string
	ResultClass          string
	PlanEffect           string
	RequiresVerification bool
	VerificationLike     bool
	LifecycleNote        string
}

func buildToolExecutionPolicy(call ToolCall, result ToolExecutionResult, session *Session) ToolExecutionPolicyOutcome {
	meta := result.Meta
	effect := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "effect")))
	if effect == "" {
		effect = inferToolExecutionEffect(strings.TrimSpace(call.Name))
	}
	resultClass := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "result_class")))
	verificationLike := toolMetaBool(meta, "verification_like")
	requiresVerification := toolMetaBool(meta, "requires_verification")
	ownerNodeID := resolvePolicyOwnerNode(meta, session)
	planEffect := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "plan_effect")))
	if planEffect == "" {
		planEffect = inferToolPlanEffect(call, effect, meta, verificationLike)
	}
	if resultClass == "" {
		resultClass = inferToolResultClass(call, effect, meta, verificationLike)
	}
	lifecycleNote := firstNonBlankString(
		toolMetaString(meta, "bundle_summary"),
		toolMetaString(meta, "cancel_reason"),
		toolMetaString(meta, "lifecycle_note"),
		toolMetaString(meta, "command_summary"),
		toolMetaString(meta, "path"),
	)
	return ToolExecutionPolicyOutcome{
		OwnerNodeID:          ownerNodeID,
		Effect:               effect,
		ResultClass:          resultClass,
		PlanEffect:           planEffect,
		RequiresVerification: requiresVerification,
		VerificationLike:     verificationLike,
		LifecycleNote:        lifecycleNote,
	}
}

func resolvePolicyOwnerNode(meta map[string]any, session *Session) string {
	ownerNodeID := strings.TrimSpace(toolMetaString(meta, "owner_node_id"))
	if ownerNodeID != "" {
		return ownerNodeID
	}
	if session != nil && session.TaskState != nil {
		focusNodeID := strings.TrimSpace(session.TaskState.ExecutorFocusNode)
		if focusNodeID != "" {
			return focusNodeID
		}
	}
	return ""
}

func inferToolResultClass(call ToolCall, effect string, meta map[string]any, verificationLike bool) string {
	switch strings.TrimSpace(call.Name) {
	case "run_shell_background", "run_shell_bundle_background":
		return "background_start"
	case "check_shell_job", "check_shell_bundle":
		return "background_status"
	case "cancel_shell_job", "cancel_shell_bundle":
		return "background_cancel"
	case "run_shell":
		if verificationLike {
			return "verification"
		}
		return "command_result"
	}
	switch effect {
	case "inspect":
		return "inspection"
	case "edit":
		return "workspace_edit"
	case "plan":
		return "plan_sync"
	case "git_mutation":
		return "git_mutation"
	default:
		return "task"
	}
}

func inferToolPlanEffect(call ToolCall, effect string, meta map[string]any, verificationLike bool) string {
	switch strings.TrimSpace(call.Name) {
	case "run_shell_background", "run_shell_bundle_background", "cancel_shell_job", "cancel_shell_bundle":
		return "progress"
	case "check_shell_job":
		switch strings.TrimSpace(strings.ToLower(toolMetaString(meta, "job_status"))) {
		case "completed":
			return "complete"
		case "failed":
			return "block"
		default:
			return "progress"
		}
	case "check_shell_bundle":
		switch strings.TrimSpace(strings.ToLower(toolMetaString(meta, "bundle_status"))) {
		case "completed":
			return "complete"
		case "failed":
			return "block"
		default:
			return "progress"
		}
	case "run_shell":
		if verificationLike {
			return "complete"
		}
		return "progress"
	}
	switch effect {
	case "edit", "git_mutation":
		return "complete"
	case "inspect", "plan":
		return "progress"
	default:
		return "progress"
	}
}
