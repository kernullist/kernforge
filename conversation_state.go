package main

import (
	"fmt"
	"strings"
	"time"
)

type ConversationState struct {
	SchemaVersion      int       `json:"schema_version"`
	UpdatedAt          time.Time `json:"updated_at"`
	LastUserGoal       string    `json:"last_user_goal,omitempty"`
	CurrentWorkflow    string    `json:"current_workflow,omitempty"`
	LastCommand        string    `json:"last_command,omitempty"`
	LastResult         string    `json:"last_result,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
	LastErrorEventID   string    `json:"last_error_event_id,omitempty"`
	PendingNextStep    string    `json:"pending_next_step,omitempty"`
	ActiveFeatureID    string    `json:"active_feature_id,omitempty"`
	LastAnalysisRunID  string    `json:"last_analysis_run_id,omitempty"`
	LastAnalysisOutput string    `json:"last_analysis_output,omitempty"`
	LastVerification   string    `json:"last_verification,omitempty"`
	Provider           string    `json:"provider,omitempty"`
	Model              string    `json:"model,omitempty"`
	OpenArtifacts      []string  `json:"open_artifacts,omitempty"`
	RunningJobs        []string  `json:"running_jobs,omitempty"`
}

func (s ConversationState) ApproxChars() int {
	total := len(s.LastUserGoal) + len(s.CurrentWorkflow) + len(s.LastCommand) + len(s.LastResult) + len(s.LastError)
	total += len(s.LastErrorEventID) + len(s.PendingNextStep) + len(s.ActiveFeatureID)
	total += len(s.LastAnalysisRunID) + len(s.LastAnalysisOutput) + len(s.LastVerification)
	total += len(s.Provider) + len(s.Model)
	for _, item := range s.OpenArtifacts {
		total += len(item)
	}
	for _, item := range s.RunningJobs {
		total += len(item)
	}
	return total
}

func (s *Session) RefreshConversationState() {
	if s == nil {
		return
	}
	state := ConversationState{
		SchemaVersion:      1,
		UpdatedAt:          time.Now(),
		Provider:           strings.TrimSpace(s.Provider),
		Model:              strings.TrimSpace(s.Model),
		ActiveFeatureID:    strings.TrimSpace(s.ActiveFeatureID),
		OpenArtifacts:      conversationOpenArtifacts(s),
		RunningJobs:        conversationRunningJobs(s),
		LastAnalysisRunID:  conversationLastAnalysisRunID(s),
		LastAnalysisOutput: conversationLastAnalysisOutput(s),
		LastVerification:   conversationLastVerification(s),
	}
	if s.ConversationState != nil {
		state.LastUserGoal = strings.TrimSpace(s.ConversationState.LastUserGoal)
		state.CurrentWorkflow = strings.TrimSpace(s.ConversationState.CurrentWorkflow)
		state.LastCommand = strings.TrimSpace(s.ConversationState.LastCommand)
		state.LastResult = strings.TrimSpace(s.ConversationState.LastResult)
		state.LastError = strings.TrimSpace(s.ConversationState.LastError)
		state.LastErrorEventID = strings.TrimSpace(s.ConversationState.LastErrorEventID)
		state.PendingNextStep = strings.TrimSpace(s.ConversationState.PendingNextStep)
	}
	for i := len(s.ConversationEvents) - 1; i >= 0; i-- {
		event := s.ConversationEvents[i]
		switch event.Kind {
		case conversationEventKindUserMessage:
			if state.LastUserGoal == "" {
				state.LastUserGoal = compactPromptSection(baseUserQueryText(event.Summary), 240)
			}
			if state.LastCommand == "" && strings.HasPrefix(strings.TrimSpace(baseUserQueryText(event.Raw)), "/") {
				state.LastCommand = compactPromptSection(baseUserQueryText(event.Raw), 220)
			}
		case conversationEventKindToolCall:
			if state.LastCommand == "" {
				state.LastCommand = conversationCommandFromEvent(event)
			}
			if state.CurrentWorkflow == "" {
				state.CurrentWorkflow = strings.TrimSpace(event.Entities["tool"])
			}
		case conversationEventKindToolResult:
			if state.LastResult == "" {
				state.LastResult = compactPromptSection(event.Summary, 260)
			}
			if state.LastCommand == "" {
				state.LastCommand = conversationCommandFromEvent(event)
			}
		case conversationEventKindAssistantReply:
			if state.LastResult == "" {
				state.LastResult = compactPromptSection(event.Summary, 260)
			}
		case conversationEventKindProviderError, conversationEventKindToolError, conversationEventKindCommandError:
			if state.LastError == "" {
				state.LastError = compactPromptSection(event.Summary, 360)
				state.LastErrorEventID = strings.TrimSpace(event.ID)
			}
		case conversationEventKindHandoff:
			if state.PendingNextStep == "" {
				state.PendingNextStep = compactPromptSection(event.Summary, 260)
			}
		case conversationEventKindVerification:
			if state.LastResult == "" {
				state.LastResult = compactPromptSection(event.Summary, 260)
			}
		}
	}
	s.ConversationState = &state
}

func renderConversationStatePrompt(state *ConversationState) string {
	if state == nil {
		return ""
	}
	lines := []string{}
	add := func(label string, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", label, value))
		}
	}
	add("last user goal", compactPromptSection(state.LastUserGoal, 220))
	add("current workflow", state.CurrentWorkflow)
	add("last command", compactPromptSection(state.LastCommand, 220))
	add("last result", compactPromptSection(state.LastResult, 220))
	add("last error", compactPromptSection(state.LastError, 280))
	add("pending next step", compactPromptSection(state.PendingNextStep, 220))
	add("active feature", state.ActiveFeatureID)
	add("latest analysis", strings.TrimSpace(strings.Join([]string{state.LastAnalysisRunID, state.LastAnalysisOutput}, " ")))
	add("latest verification", compactPromptSection(state.LastVerification, 220))
	add("provider/model", strings.TrimSpace(strings.Join([]string{state.Provider, state.Model}, " / ")))
	if len(state.OpenArtifacts) > 0 {
		add("open artifacts", strings.Join(state.OpenArtifacts, ", "))
	}
	if len(state.RunningJobs) > 0 {
		add("running jobs", strings.Join(state.RunningJobs, ", "))
	}
	return strings.Join(lines, "\n")
}

func conversationOpenArtifacts(s *Session) []string {
	if s == nil {
		return nil
	}
	out := []string{}
	if s.LastSelection != nil && strings.TrimSpace(s.LastSelection.FilePath) != "" {
		out = append(out, strings.TrimSpace(s.LastSelection.FilePath))
	}
	if s.LastAnalysis != nil && strings.TrimSpace(s.LastAnalysis.OutputPath) != "" {
		out = append(out, strings.TrimSpace(s.LastAnalysis.OutputPath))
	}
	return uniqueStrings(out)
}

func conversationRunningJobs(s *Session) []string {
	if s == nil {
		return nil
	}
	out := []string{}
	for _, job := range s.BackgroundJobs {
		status := strings.ToLower(strings.TrimSpace(job.Status))
		if status == "running" || status == "started" || status == "pending" {
			out = append(out, strings.TrimSpace(job.ID))
		}
	}
	for _, bundle := range s.BackgroundBundles {
		status := strings.ToLower(strings.TrimSpace(bundle.Status))
		if status == "running" || status == "started" || status == "pending" {
			out = append(out, strings.TrimSpace(bundle.ID))
		}
	}
	return uniqueStrings(out)
}

func conversationLastAnalysisRunID(s *Session) string {
	if s == nil || s.LastAnalysis == nil {
		return ""
	}
	return strings.TrimSpace(s.LastAnalysis.RunID)
}

func conversationLastAnalysisOutput(s *Session) string {
	if s == nil || s.LastAnalysis == nil {
		return ""
	}
	return strings.TrimSpace(s.LastAnalysis.OutputPath)
}

func conversationLastVerification(s *Session) string {
	if s == nil || s.LastVerification == nil {
		return ""
	}
	if s.LastVerification.HasFailures() {
		return compactPromptSection(s.LastVerification.FailureSummary(), 260)
	}
	return s.LastVerification.SummaryLine()
}

func conversationCommandFromEvent(event ConversationEvent) string {
	tool := strings.TrimSpace(event.Entities["tool"])
	command := strings.TrimSpace(event.Entities["command"])
	path := strings.TrimSpace(event.Entities["path"])
	switch {
	case tool != "" && command != "":
		return tool + " " + command
	case tool != "" && path != "":
		return tool + " " + path
	case tool != "":
		return tool
	default:
		return compactPromptSection(event.Summary, 220)
	}
}
