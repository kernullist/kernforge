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
)

type Agent struct {
	Config                         Config
	Client                         ProviderClient
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
	VerifyChanges                  func(context.Context) (VerificationReport, bool)
	PromptResolveAutoVerifyFailure func(VerificationReport) (AutoVerifyFailureResolution, error)
	EmitAssistant                  func(string)
	EmitAssistantDelta             func(string)
	EmitProgress                   func(string)
	lastEmittedText                string
}

type AutoVerifyFailureResolution string

const (
	AutoVerifyFailureNoAction AutoVerifyFailureResolution = ""
	AutoVerifyFailureDisable  AutoVerifyFailureResolution = "disable"
	AutoVerifyFailureRetry    AutoVerifyFailureResolution = "retry"
)

const (
	repeatedToolFailureNudgeThreshold = 2
	repeatedToolFailureAbortThreshold = 3
)

func (a *Agent) Reply(ctx context.Context, userText string) (string, error) {
	return a.ReplyWithImages(ctx, userText, nil)
}

func (a *Agent) ReplyWithImages(ctx context.Context, userText string, extraImages []MessageImage) (string, error) {
	if a.Client == nil {
		return "", fmt.Errorf("no model provider is configured")
	}
	a.lastEmittedText = ""
	startIndex := len(a.Session.Messages)
	enriched, mentionImages := a.expandMentions(ctx, userText)
	enriched = a.Skills.InjectPromptContext(enriched)
	if memoryContext := strings.TrimSpace(a.LongMem.RelevantContext(a.Workspace.BaseRoot, userText, a.Session.ID)); memoryContext != "" {
		enriched += "\n\nRelevant persistent memory from past sessions:\n" + memoryContext
	}
	analysisContext := strings.TrimSpace(a.latestProjectAnalysisContext(userText))
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
	a.Session.AddMessage(Message{
		Role:   "user",
		Text:   enriched,
		Images: images,
	})
	if err := a.Store.Save(a.Session); err != nil {
		return "", err
	}
	reply, err := a.completeLoop(ctx)
	if err != nil {
		return "", err
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
	return reply, nil
}

func (a *Agent) Compact(instructions string) string {
	if len(a.Session.Messages) <= 8 {
		return "conversation is already compact"
	}
	cut := len(a.Session.Messages) - 8
	older := a.Session.Messages[:cut]
	a.Session.Messages = append([]Message(nil), a.Session.Messages[cut:]...)
	summary := summarizeMessages(older, instructions)
	if strings.TrimSpace(a.Session.Summary) == "" {
		a.Session.Summary = summary
	} else {
		a.Session.Summary = strings.TrimSpace(a.Session.Summary) + "\n\n" + summary
	}
	_ = a.Store.Save(a.Session)
	return summary
}

func (a *Agent) completeLoop(ctx context.Context) (string, error) {
	if reply, ok, err := a.maybeAnswerFromCachedProjectAnalysis(ctx); err != nil {
		return "", err
	} else if ok {
		a.Session.AddMessage(Message{
			Role: "assistant",
			Text: reply,
		})
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		return reply, nil
	}
	emptyFinalReplies := 0
	unresolvedVerification := false
	finalAnswerNudges := 0
	patchFormatRetries := 0
	invalidToolArgsRetries := 0
	editTargetMismatchRetries := 0
	lastToolError := ""
	lastToolErrorCount := 0
	lastToolCallSignature := ""
	lastToolCallSignatureCount := 0
	repeatedToolCallNudges := 0
	lastToolCallSummary := ""
	lastStopReason := ""
	lastIteration := 0
	lastRecentToolTurns := ""
	consecutiveEditTurns := 0
	postEditFinalAnswerNudges := 0
	autoVerifyInfraFailureCount := 0
	autoVerifyDisablePrompted := false
	disabledTools := map[string]bool{}
	for iterations := 0; iterations < configMaxToolIterations(a.Config); iterations++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		lastIteration = iterations + 1
		if a.Config.AutoCompactChars > 0 && a.Session.ApproxChars() > a.Config.AutoCompactChars {
			a.Compact("Auto-compacted due to context growth.")
		}
		resp, err := a.completeModelTurn(ctx, ChatRequest{
			Model:       a.Session.Model,
			System:      a.systemPrompt(),
			Messages:    a.Session.Messages,
			Tools:       a.Tools.DefinitionsExcluding(disabledTools),
			MaxTokens:   a.Config.MaxTokens,
			Temperature: a.Config.Temperature,
			WorkingDir:  a.Session.WorkingDir,
			OnTextDelta: a.EmitAssistantDelta,
		})
		if err != nil {
			return "", err
		}
		resp.Message.Text = sanitizeAssistantMessageText(resp.Message.Text, len(resp.Message.ToolCalls) > 0)
		if a.EmitAssistantDelta != nil && strings.TrimSpace(resp.Message.Text) != "" {
			a.lastEmittedText = strings.TrimSpace(resp.Message.Text)
		}
		lastStopReason = normalizeStopReason(resp.StopReason)
		a.Session.AddMessage(resp.Message)
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		if len(resp.Message.ToolCalls) > 0 {
			currentSignature := toolCallSignature(resp.Message.ToolCalls)
			if currentSignature != "" {
				lastToolCallSummary = summarizeToolCalls(resp.Message.ToolCalls)
				if currentSignature == lastToolCallSignature {
					lastToolCallSignatureCount++
				} else {
					lastToolCallSignature = currentSignature
					lastToolCallSignatureCount = 1
					repeatedToolCallNudges = 0
				}
				if lastToolCallSignatureCount >= 3 {
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
					return "", fmt.Errorf("stopped after repeated identical tool calls")
				}
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
			reply := strings.TrimSpace(resp.Message.Text)
			if reply != "" {
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
				if reply == a.lastEmittedText {
					return "", nil
				}
				return reply, nil
			}
			if isTokenLimitStopReason(lastStopReason) {
				return "", fmt.Errorf("model stopped before producing a usable response due to token limit (stop_reason=%s)", lastStopReason)
			}
			emptyFinalReplies++
			if emptyFinalReplies >= 2 {
				if lastStopReason != "" {
					return "", fmt.Errorf("model returned an empty response (stop_reason=%s)", lastStopReason)
				}
				return "", fmt.Errorf("model returned an empty response")
			}
			a.Session.AddMessage(Message{
				Role: "user",
				Text: "Please provide the final answer to the user now. Do not return an empty message.",
			})
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
		for _, call := range resp.Message.ToolCalls {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			out, err := a.Tools.Execute(ctx, call.Name, call.Arguments)
			toolMsg := Message{
				Role:       "tool",
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Text:       out,
			}
			if err != nil && errors.Is(err, ErrEditCanceled) {
				toolMsg.Text = "CANCELED: user canceled the edit preview. No files were changed."
				a.Session.AddMessage(toolMsg)
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && errors.Is(err, ErrWriteDenied) {
				toolMsg.Text = "CANCELED: user declined write approval. No files were changed, and no filesystem permission issue was detected."
				a.Session.AddMessage(toolMsg)
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && errors.Is(err, ErrInvalidEditPayload) {
				toolMsg.IsError = true
				if out == "" {
					toolMsg.Text = err.Error()
				} else {
					toolMsg.Text = out + "\n\nERROR: " + err.Error()
				}
				a.Session.AddMessage(toolMsg)
				if saveErr := a.Store.Save(a.Session); saveErr != nil {
					return "", saveErr
				}
				return "", err
			}
			if err != nil && errors.Is(err, ErrInvalidToolArgumentsJSON) && invalidToolArgsRetries < 1 {
				toolMsg.IsError = true
				if out == "" {
					toolMsg.Text = err.Error()
				} else {
					toolMsg.Text = out + "\n\nERROR: " + err.Error()
				}
				a.Session.AddMessage(toolMsg)
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
				if out == "" {
					toolMsg.Text = err.Error()
				} else {
					toolMsg.Text = out + "\n\nERROR: " + err.Error()
				}
				a.Session.AddMessage(toolMsg)
				a.Session.AddMessage(Message{
					Role: "user",
					Text: "Your last edit targeted stale or mismatched file contents. Do not repeat the same edit immediately. First read the exact file again from the same path, confirm the current contents, and then build a new edit against that fresh text. If the resolved path points into a different worktree or administrative worktree directory, correct the path before editing.",
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
				toolMsg.IsError = true
				if out == "" {
					toolMsg.Text = err.Error()
				} else {
					toolMsg.Text = out + "\n\nERROR: " + err.Error()
				}
				currentError := strings.TrimSpace(err.Error())
				if call.Name == "run_shell" {
					currentError = strings.TrimSpace(call.Name + ": " + toolMsg.Text + "\n" + err.Error())
				}
				if currentError != "" {
					iterationToolError = currentError
				}
				if call.Name == "apply_patch" && errors.Is(err, ErrInvalidPatchFormat) && patchFormatRetries < 1 {
					patchFormatRetries++
					a.Session.AddMessage(toolMsg)
					a.Session.AddMessage(Message{
						Role: "user",
						Text: "Your last apply_patch call used the wrong patch format. Retry using the tool again and make the patch string start exactly with:\n*** Begin Patch\nThen use one or more file sections like *** Update File:, *** Add File:, or *** Delete File:, and end with:\n*** End Patch\nDo not send prose, JSON, or code fences inside the patch string.",
					})
					lastToolError = ""
					lastToolErrorCount = 0
					continue
				}
			} else {
				iterationHadToolSuccess = true
				if isEditTool(call.Name) {
					edited = true
					if a.EmitProgress != nil {
						if summary := summarizeEditToolResult(call.Name, out); summary != "" {
							a.EmitProgress(summary)
						}
					}
				}
			}
			a.Session.AddMessage(toolMsg)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if iterationHadToolSuccess {
			lastToolError = ""
			lastToolErrorCount = 0
		} else if iterationToolError != "" {
			if iterationToolError == lastToolError {
				lastToolErrorCount++
			} else {
				lastToolError = iterationToolError
				lastToolErrorCount = 1
			}
		}
		if lastToolErrorCount >= repeatedToolFailureAbortThreshold && lastToolError != "" {
			return "", fmt.Errorf("stopped after repeated tool failure: %s", lastToolError)
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
				a.EmitProgress("Edit applied. Checking follow-up steps...")
			}
			if report, ok := a.autoVerifyChanges(ctx); ok {
				autoVerifyRetryAttempted := false
				if a.EmitProgress != nil {
					if report.HasFailures() {
						a.EmitProgress("Automatic verification failed. Asking the model to continue fixing the issue...")
					} else {
						a.EmitProgress("Automatic verification finished. Waiting for the model to summarize the change...")
					}
				}
				verification := strings.TrimSpace(report.RenderDetailed())
				a.Session.AddMessage(Message{
					Role: "user",
					Text: "Automatic verification results:\n" + verification,
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
							if a.EmitProgress != nil {
								a.EmitProgress("Verification tool path updated. Retrying automatic verification...")
							}
							retriedReport, retriedOK := a.autoVerifyChanges(ctx)
							if retriedOK {
								report = retriedReport
								verification = strings.TrimSpace(report.RenderDetailed())
								a.Session.AddMessage(Message{
									Role: "user",
									Text: "Automatic verification results after tool-path update:\n" + verification,
								})
								unresolvedVerification = report.HasFailures()
							}
						}
						if resolution == AutoVerifyFailureDisable {
							unresolvedVerification = false
							autoVerifyInfraFailureCount = 0
							autoVerifyDisabledAfterPrompt = true
							if a.EmitProgress != nil {
								a.EmitProgress("Automatic verification was disabled after repeated tool-path verification failures.")
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
						a.Session.AddMessage(Message{
							Role: "user",
							Text: text,
						})
					}
				}
			} else {
				autoVerifyInfraFailureCount = 0
				if a.EmitProgress != nil {
					a.EmitProgress("Edit applied. Waiting for the model to summarize the change...")
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
	return "", fmt.Errorf("tool loop limit exceeded%s", formatToolLoopDiagnostic(lastToolCallSummary, lastStopReason, lastIteration, configMaxToolIterations(a.Config), lastRecentToolTurns))
}

func sanitizeAssistantMessageText(text string, hasToolCalls bool) string {
	trimmed := strings.TrimSpace(splitAssistantPreambleBoundaries(text))
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
	default:
		return false
	}
}

func (a *Agent) completeModelTurn(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	attempts := 2
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ChatResponse{}, err
		}

		attemptCtx, cancel := context.WithTimeout(ctx, configRequestTimeout(a.Config))
		resp, err := a.completeModelTurnOnce(attemptCtx, req)
		cancel()
		if err == nil {
			return resp, nil
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return ChatResponse{}, err
		}
		if ctx.Err() != nil {
			return ChatResponse{}, ctx.Err()
		}
		if attempt == attempts-1 {
			return ChatResponse{}, err
		}
		if a.EmitProgress != nil {
			a.EmitProgress("Model request timed out. Retrying once...")
		}
	}
	return ChatResponse{}, context.DeadlineExceeded
}

func (a *Agent) completeModelTurnOnce(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	type result struct {
		resp ChatResponse
		err  error
	}

	done := make(chan result, 1)
	go func() {
		resp, err := a.Client.Complete(ctx, req)
		done <- result{resp: resp, err: err}
	}()

	select {
	case <-ctx.Done():
		return ChatResponse{}, ctx.Err()
	case out := <-done:
		return out.resp, out.err
	}
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

func normalizeStopReason(reason string) string {
	return strings.ToLower(strings.TrimSpace(reason))
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
		name := strings.TrimSpace(call.Name)
		if name == "" {
			name = "unknown"
		}
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
	case strings.HasPrefix(text, "CANCELED:"):
		return "canceled"
	case msg.IsError:
		return "error"
	case text == "":
		return "empty"
	default:
		return "ok"
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

func (a *Agent) systemPrompt() string {
	var b strings.Builder
	latestUser := baseUserQueryText(latestUserMessageText(a.Session.Messages))
	lowerLatestUser := strings.ToLower(strings.TrimSpace(latestUser))
	b.WriteString("You are Kernforge, a terminal-based coding agent inspired by Claude Code.\n")
	b.WriteString("Work like a careful senior engineer inside the user's repository.\n")
	b.WriteString("Use tools before making assumptions. Read relevant files before editing them. Keep answers concise and implementation-focused.\n")
	b.WriteString("When code changes are needed, prefer the smallest correct diff and verify with tests or builds when practical.\n")
	b.WriteString("If the user asks a question, answer directly before suggesting extra work.\n")
	b.WriteString("The user prompt may include an 'Auto-discovered code context' section with best-effort relevant snippets. Use it as a shortcut, but verify with tools if something looks uncertain.\n")
	b.WriteString("The user prompt may include a 'Relevant persistent memory from past sessions' section. Treat it as best-effort historical context and verify it when needed. If you rely on a memory item in your answer, cite its memory id in brackets like [mem-...].\n")
	b.WriteString("The user prompt may include a 'Relevant project analysis from past analyze-project runs' section. Treat it as a cached architecture summary derived from prior workspace analysis. Prefer using it before rereading large code areas, but verify details with tools before making edits or high-risk claims.\n")
	b.WriteString("User messages may include attached images. Use visual details from them when relevant.\n")
	b.WriteString("After successful file edits, the conversation may include an 'Automatic verification results' message generated by the CLI. Use it to validate or fix your changes.\n")
	fmt.Fprintf(&b, "Workspace root: %s\n", a.Session.WorkingDir)
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", a.Session.Provider, a.Session.Model)
	fmt.Fprintf(&b, "Permission mode: %s\n", a.Session.PermissionMode)
	if strings.TrimSpace(a.Session.Summary) != "" {
		b.WriteString("\nConversation summary:\n")
		b.WriteString(compactPromptSection(a.Session.Summary, 900))
		b.WriteString("\n")
	}
	if len(a.Session.Plan) > 0 {
		b.WriteString("\nCurrent shared plan:\n")
		for _, item := range a.Session.Plan {
			fmt.Fprintf(&b, "- [%s] %s\n", item.Status, item.Step)
		}
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

	if configAutoLocale(a.Config) {
		locale := getSystemLocale()
		if locale != "" {
			fmt.Fprintf(&b, "\nAlways respond in the following locale language: %s\n", locale)
		}
	}

	b.WriteString("\nTool rules:\n")
	b.WriteString("- Prefer read_file, list_files, grep, and git tools to inspect the codebase.\n")
	b.WriteString("- Prefer apply_patch for precise edits to existing files.\n")
	b.WriteString("- Before editing a file, read that exact file path first unless the current contents were already read very recently in this turn.\n")
	b.WriteString("- When using apply_patch, the patch argument must be raw patch text that starts with *** Begin Patch and ends with *** End Patch.\n")
	b.WriteString("- Never send JSON, markdown code fences, prose, or pseudo-objects as the apply_patch patch string.\n")
	b.WriteString("- Use replace_in_file only for very small exact substitutions when you have just read the same file path and the exact search text is present exactly as written.\n")
	b.WriteString("- If there is any risk that the file changed, the path is ambiguous, or the replacement spans multiple lines or repeated matches, read the file again and use apply_patch instead of replace_in_file.\n")
	b.WriteString("- If an edit fails because search text or patch context is not found, do not repeat the same edit. Re-read the file from the same path and build a fresh edit.\n")
	b.WriteString("- Use write_file for creating new files or fully rewriting a file when necessary.\n")
	b.WriteString("- Do not use write_file for small edits to existing files. Read the file and use apply_patch instead.\n")
	b.WriteString("- Tool arguments must be complete valid JSON. Never send truncated JSON, partial strings, or unfinished objects.\n")
	b.WriteString("- Use update_plan for multi-step tasks.\n")
	b.WriteString("- Use run_shell for build, test, or local inspection commands.\n")
	b.WriteString("- For run_shell on Windows PowerShell, do not use &&. Use a single command or PowerShell separators like ; only when needed.\n")
	b.WriteString("- For run_shell, the working directory is already set to the workspace root. Do not prepend commands with cd unless changing into a subdirectory is truly necessary.\n")
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
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
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

func summarizeMessages(messages []Message, instructions string) string {
	var lines []string
	if strings.TrimSpace(instructions) != "" {
		lines = append(lines, "Focus: "+strings.TrimSpace(instructions))
	}
	for _, msg := range messages {
		text := strings.TrimSpace(msg.Text)
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
		if len(text) > 220 {
			text = text[:220] + "..."
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", msg.Role, strings.ReplaceAll(text, "\n", " ")))
	}
	if len(lines) == 0 {
		return "No prior summary available."
	}
	return strings.Join(lines, "\n")
}

func synthesizeToolPreambleText(calls []ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	switch calls[0].Name {
	case "run_shell":
		return "Let me check the current state first."
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
