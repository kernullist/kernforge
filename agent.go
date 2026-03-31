package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Agent struct {
	Config        Config
	Client        ProviderClient
	Tools         *ToolRegistry
	Workspace     Workspace
	Session       *Session
	Store         *SessionStore
	Memory        MemoryBundle
	Skills        SkillCatalog
	MCP           *MCPManager
	LongMem       *PersistentMemoryStore
	VerifyHistory *VerificationHistoryStore
	VerifyChanges func(context.Context) (VerificationReport, bool)
	EmitAssistant func(string)
}

func (a *Agent) Reply(ctx context.Context, userText string) (string, error) {
	return a.ReplyWithImages(ctx, userText, nil)
}

func (a *Agent) ReplyWithImages(ctx context.Context, userText string, extraImages []MessageImage) (string, error) {
	if a.Client == nil {
		return "", fmt.Errorf("no model provider is configured")
	}
	startIndex := len(a.Session.Messages)
	enriched, mentionImages := a.expandMentions(ctx, userText)
	enriched = a.Skills.InjectPromptContext(enriched)
	if memoryContext := strings.TrimSpace(a.LongMem.RelevantContext(a.Workspace.BaseRoot, userText, a.Session.ID)); memoryContext != "" {
		enriched += "\n\nRelevant persistent memory from past sessions:\n" + memoryContext
	}
	if scout := a.autoScoutContext(userText); scout != "" {
		enriched += scout
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
	emptyFinalReplies := 0
	unresolvedVerification := false
	finalAnswerNudges := 0
	patchFormatRetries := 0
	lastToolError := ""
	lastToolErrorCount := 0
	for iterations := 0; iterations < configMaxToolIterations(a.Config); iterations++ {
		if a.Config.AutoCompactChars > 0 && a.Session.ApproxChars() > a.Config.AutoCompactChars {
			a.Compact("Auto-compacted due to context growth.")
		}
		resp, err := a.Client.Complete(ctx, ChatRequest{
			Model:       a.Session.Model,
			System:      a.systemPrompt(),
			Messages:    a.Session.Messages,
			Tools:       a.Tools.Definitions(),
			MaxTokens:   a.Config.MaxTokens,
			Temperature: a.Config.Temperature,
			WorkingDir:  a.Session.WorkingDir,
		})
		if err != nil {
			return "", err
		}
		a.Session.AddMessage(resp.Message)
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
		if len(resp.Message.ToolCalls) > 0 {
			preamble := strings.TrimSpace(resp.Message.Text)
			if a.EmitAssistant != nil {
				if preamble != "" {
					a.EmitAssistant(preamble)
				} else {
					a.EmitAssistant(synthesizeToolPreambleText(resp.Message.ToolCalls))
				}
			}
		}
		if len(resp.Message.ToolCalls) == 0 {
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
				return reply, nil
			}
			emptyFinalReplies++
			if emptyFinalReplies >= 2 {
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
		for _, call := range resp.Message.ToolCalls {
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
					if currentError == lastToolError {
						lastToolErrorCount++
					} else {
						lastToolError = currentError
						lastToolErrorCount = 1
					}
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
			} else if isEditTool(call.Name) {
				edited = true
				lastToolError = ""
				lastToolErrorCount = 0
			}
			a.Session.AddMessage(toolMsg)
		}
		if lastToolErrorCount >= 2 && lastToolError != "" {
			return "", fmt.Errorf("stopped after repeated tool failure: %s", lastToolError)
		}
		if edited {
			if report, ok := a.autoVerifyChanges(ctx); ok {
				verification := strings.TrimSpace(report.RenderDetailed())
				a.Session.AddMessage(Message{
					Role: "user",
					Text: "Automatic verification results:\n" + verification,
				})
				unresolvedVerification = report.HasFailures()
				if report.HasFailures() {
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
			} else {
				unresolvedVerification = false
			}
		}
		if err := a.Store.Save(a.Session); err != nil {
			return "", err
		}
	}
	if lastToolError != "" {
		return "", fmt.Errorf("stopped after repeated tool failure: %s", lastToolError)
	}
	return "", fmt.Errorf("tool loop limit exceeded")
}

func (a *Agent) systemPrompt() string {
	var b strings.Builder
	b.WriteString("You are Kernforge, a terminal-based coding agent inspired by Claude Code.\n")
	b.WriteString("Work like a careful senior engineer inside the user's repository.\n")
	b.WriteString("Use tools before making assumptions. Read relevant files before editing them. Keep answers concise and implementation-focused.\n")
	b.WriteString("When code changes are needed, prefer the smallest correct diff and verify with tests or builds when practical.\n")
	b.WriteString("If the user asks a question, answer directly before suggesting extra work.\n")
	b.WriteString("The user prompt may include an 'Auto-discovered code context' section with best-effort relevant snippets. Use it as a shortcut, but verify with tools if something looks uncertain.\n")
	b.WriteString("The user prompt may include a 'Relevant persistent memory from past sessions' section. Treat it as best-effort historical context and verify it when needed. If you rely on a memory item in your answer, cite its memory id in brackets like [mem-...].\n")
	b.WriteString("User messages may include attached images. Use visual details from them when relevant.\n")
	b.WriteString("After successful file edits, the conversation may include an 'Automatic verification results' message generated by the CLI. Use it to validate or fix your changes.\n")
	fmt.Fprintf(&b, "Workspace root: %s\n", a.Session.WorkingDir)
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", a.Session.Provider, a.Session.Model)
	fmt.Fprintf(&b, "Permission mode: %s\n", a.Session.PermissionMode)
	if strings.TrimSpace(a.Session.Summary) != "" {
		b.WriteString("\nConversation summary:\n")
		b.WriteString(a.Session.Summary)
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
		b.WriteString(a.Session.LastVerification.RenderShort())
		b.WriteString("\n")
	}
	if combined := strings.TrimSpace(a.Memory.Combined()); combined != "" {
		b.WriteString("\nLoaded memory files:\n")
		b.WriteString(combined)
		b.WriteString("\n")
	}
	if catalog := strings.TrimSpace(a.Skills.CatalogPrompt()); catalog != "" {
		b.WriteString("\nAvailable local skills:\n")
		b.WriteString(catalog)
		b.WriteString("\n")
	}
	if defaults := strings.TrimSpace(a.Skills.DefaultPrompt()); defaults != "" {
		b.WriteString("\nEnabled local skills:\n")
		b.WriteString(defaults)
		b.WriteString("\n")
	}
	if resources := strings.TrimSpace(a.MCP.ResourceCatalogPrompt()); resources != "" {
		b.WriteString("\nAvailable MCP resources:\n")
		b.WriteString(resources)
		b.WriteString("\n")
	}
	if prompts := strings.TrimSpace(a.MCP.PromptCatalogPrompt()); prompts != "" {
		b.WriteString("\nAvailable MCP prompts:\n")
		b.WriteString(prompts)
		b.WriteString("\n")
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
	b.WriteString("- When using apply_patch, the patch argument must be raw patch text that starts with *** Begin Patch and ends with *** End Patch.\n")
	b.WriteString("- Never send JSON, markdown code fences, prose, or pseudo-objects as the apply_patch patch string.\n")
	b.WriteString("- Use replace_in_file only for very small exact substitutions.\n")
	b.WriteString("- Use write_file for creating new files or fully rewriting a file when necessary.\n")
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
