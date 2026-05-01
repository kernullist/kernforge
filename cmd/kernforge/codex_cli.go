package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	codexCLIDefaultExecutable = "codex"
	codexCLIDefaultModel      = "default"
	codexCLIPromptArgLimit    = 1024
)

type codexCLICommandRunner func(ctx context.Context, executable string, args []string, dir string, env []string) ([]byte, error)

type CodexCLIModelInfo struct {
	ID             string
	Name           string
	SupportedInAPI bool
	Visibility     string
	Priority       int
}

type CodexCLIClient struct {
	executable string
	extraArgs  []string
	run        codexCLICommandRunner
}

func NewCodexCLIClient(executable string, extraArgs []string) *CodexCLIClient {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		executable = codexCLIDefaultExecutable
	}
	cleanArgs := make([]string, 0, len(extraArgs))
	for _, arg := range extraArgs {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			cleanArgs = append(cleanArgs, arg)
		}
	}
	return &CodexCLIClient{
		executable: executable,
		extraArgs:  cleanArgs,
		run:        runCodexCLICommand,
	}
}

func (c *CodexCLIClient) Name() string {
	return "codex-cli"
}

func (c *CodexCLIClient) ModelRouteMetadata() ModelRouteMetadata {
	if c == nil {
		return ModelRouteMetadata{Provider: "codex-cli"}
	}
	executable := strings.TrimSpace(c.executable)
	if executable == "" {
		executable = codexCLIDefaultExecutable
	}
	endpoint := executable
	if len(c.extraArgs) > 0 {
		endpoint += " " + strings.Join(c.extraArgs, " ")
	}
	return ModelRouteMetadata{Provider: "codex-cli", BaseURL: endpoint}
}

func (c *CodexCLIClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if c == nil {
		return ChatResponse{}, fmt.Errorf("codex CLI client is not configured")
	}
	executable := strings.TrimSpace(c.executable)
	if executable == "" {
		executable = codexCLIDefaultExecutable
	}
	workingDir := strings.TrimSpace(req.WorkingDir)
	if workingDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workingDir = cwd
		}
	}
	prompt := renderCodexCLIPrompt(req)
	promptArg, cleanup, err := codexCLIPromptArgument(workingDir, prompt)
	if err != nil {
		return ChatResponse{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	args := buildCodexCLIArgs(req.Model, c.extraArgs, promptArg)
	env := append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	runner := c.run
	if runner == nil {
		runner = runCodexCLICommand
	}
	data, err := runner(ctx, executable, args, workingDir, env)
	text := sanitizeCodexCLIOutput(string(data))
	if extracted := extractCodexCLIFinalOutput(text, req.JSONMode); strings.TrimSpace(extracted) != "" {
		text = extracted
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ChatResponse{}, ctxErr
		}
		if errors.Is(err, exec.ErrNotFound) {
			return ChatResponse{}, fmt.Errorf("codex CLI executable not found: %s", executable)
		}
		if text != "" {
			return ChatResponse{}, fmt.Errorf("codex CLI command failed: %w\n%s", err, text)
		}
		return ChatResponse{}, fmt.Errorf("codex CLI command failed: %w", err)
	}
	if text == "" {
		return ChatResponse{}, newProviderMessageError("codex-cli", "empty Codex CLI output", "", "", nil, nil)
	}
	if req.OnTextDelta != nil {
		req.OnTextDelta(text)
	}
	return ChatResponse{
		Message: Message{
			Role: "assistant",
			Text: text,
		},
		StopReason: "stop",
	}, nil
}

func runCodexCLICommand(ctx context.Context, executable string, args []string, dir string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	return cmd.CombinedOutput()
}

func FetchCodexCLIModels(ctx context.Context, executable string, dir string) ([]CodexCLIModelInfo, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		executable = codexCLIDefaultExecutable
	}
	env := append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	data, err := runCodexCLICommand(ctx, executable, []string{"debug", "models"}, dir, env)
	text := sanitizeCodexCLIOutput(string(data))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("codex CLI executable not found: %s", executable)
		}
		if text != "" {
			return nil, fmt.Errorf("codex CLI model discovery failed: %w\n%s", err, text)
		}
		return nil, fmt.Errorf("codex CLI model discovery failed: %w", err)
	}
	return parseCodexCLIModelsJSON([]byte(text))
}

func parseCodexCLIModelsJSON(data []byte) ([]CodexCLIModelInfo, error) {
	text := sanitizeCodexCLIOutput(string(data))
	if text == "" {
		return nil, fmt.Errorf("empty Codex CLI model list")
	}
	payload := text
	if !looksLikeStandaloneJSONOutput(payload) {
		if candidate := lastValidJSONObject(payload); candidate != "" {
			payload = candidate
		}
	}
	var decoded struct {
		Models []struct {
			Slug           string `json:"slug"`
			ID             string `json:"id"`
			Name           string `json:"name"`
			DisplayName    string `json:"display_name"`
			SupportedInAPI *bool  `json:"supported_in_api"`
			Visibility     string `json:"visibility"`
			Priority       int    `json:"priority"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return nil, fmt.Errorf("parse Codex CLI model list: %w", err)
	}
	models := make([]CodexCLIModelInfo, 0, len(decoded.Models))
	seen := make(map[string]bool)
	for _, item := range decoded.Models {
		id := strings.TrimSpace(item.Slug)
		if id == "" {
			id = strings.TrimSpace(item.ID)
		}
		if id == "" {
			id = strings.TrimSpace(item.Name)
		}
		if id == "" || seen[strings.ToLower(id)] {
			continue
		}
		if item.SupportedInAPI != nil && !*item.SupportedInAPI {
			continue
		}
		visibility := strings.ToLower(strings.TrimSpace(item.Visibility))
		if visibility == "hide" || visibility == "hidden" {
			continue
		}
		name := strings.TrimSpace(item.DisplayName)
		if name == "" {
			name = strings.TrimSpace(item.Name)
		}
		if name == "" {
			name = id
		}
		models = append(models, CodexCLIModelInfo{
			ID:             id,
			Name:           name,
			SupportedInAPI: item.SupportedInAPI == nil || *item.SupportedInAPI,
			Visibility:     strings.TrimSpace(item.Visibility),
			Priority:       item.Priority,
		})
		seen[strings.ToLower(id)] = true
	}
	return models, nil
}

func buildCodexCLIArgs(model string, extraArgs []string, prompt string) []string {
	args := []string{"exec"}
	if modelValue := codexCLIModelFlagValue(model); modelValue != "" {
		args = append(args, "-c", "model="+modelValue)
	}
	for _, arg := range extraArgs {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			args = append(args, arg)
		}
	}
	args = append(args, prompt)
	return args
}

func codexCLIModelFlagValue(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.EqualFold(model, codexCLIDefaultModel) {
		return ""
	}
	return model
}

func renderCodexCLIPrompt(req ChatRequest) string {
	var b strings.Builder
	b.WriteString("# Kernforge request for Codex CLI\n\n")
	b.WriteString("You are running as the Codex CLI provider behind Kernforge. Use your native local repository access when needed, and return the final assistant answer as plain Markdown. Do not emit Kernforge tool-call JSON.\n")
	if req.JSONMode {
		b.WriteString("The upstream request requires machine-readable JSON. Your final assistant answer must be only the requested JSON object, with no Markdown fences or prose around it.\n")
	}
	if strings.TrimSpace(req.System) != "" {
		b.WriteString("\n## System\n\n")
		b.WriteString(strings.TrimSpace(req.System))
		b.WriteString("\n")
	}
	if len(req.Tools) > 0 {
		b.WriteString("\n## Tooling Note\n\n")
		b.WriteString("Kernforge tool schemas are not forwarded through this provider bridge. If repository inspection or edits are required, use Codex CLI's native capabilities and summarize what changed or what blocked the work.\n")
	}
	if len(req.Messages) > 0 {
		b.WriteString("\n## Conversation\n")
	}
	for _, msg := range req.Messages {
		role := strings.ToUpper(strings.TrimSpace(msg.Role))
		if role == "" {
			role = "MESSAGE"
		}
		b.WriteString("\n### ")
		b.WriteString(role)
		if strings.TrimSpace(msg.ToolName) != "" {
			b.WriteString(" ")
			b.WriteString(strings.TrimSpace(msg.ToolName))
		}
		if strings.TrimSpace(msg.ToolCallID) != "" {
			b.WriteString(" ")
			b.WriteString(strings.TrimSpace(msg.ToolCallID))
		}
		if msg.IsError {
			b.WriteString(" ERROR")
		}
		b.WriteString("\n\n")
		if strings.TrimSpace(msg.Text) != "" {
			b.WriteString(strings.TrimSpace(msg.Text))
			b.WriteString("\n")
		}
		if len(msg.Images) > 0 {
			b.WriteString("\nImages:\n")
			for _, image := range msg.Images {
				if strings.TrimSpace(image.Path) != "" {
					b.WriteString("- ")
					b.WriteString(strings.TrimSpace(image.Path))
					b.WriteString("\n")
				}
			}
		}
		if len(msg.ToolCalls) > 0 {
			b.WriteString("\nPrior Kernforge tool calls:\n")
			for _, call := range msg.ToolCalls {
				b.WriteString("- ")
				b.WriteString(strings.TrimSpace(call.Name))
				if strings.TrimSpace(call.ID) != "" {
					b.WriteString(" id=")
					b.WriteString(strings.TrimSpace(call.ID))
				}
				if strings.TrimSpace(call.Arguments) != "" {
					b.WriteString(" args=")
					b.WriteString(strings.TrimSpace(call.Arguments))
				}
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func codexCLIPromptArgument(workingDir string, prompt string) (string, func(), error) {
	if len(prompt) <= codexCLIPromptArgLimit {
		return prompt, nil, nil
	}
	baseDir := strings.TrimSpace(workingDir)
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	tmpDir := filepath.Join(baseDir, userConfigDirName, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", nil, err
	}
	path := filepath.Join(tmpDir, fmt.Sprintf("codex-cli-request-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(prompt+"\n"), 0o600); err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(path)
	}
	return "Read the complete Kernforge request from this file, follow it, and return the final answer: " + path, cleanup, nil
}

func sanitizeCodexCLIOutput(text string) string {
	text = ansiPattern.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func extractCodexCLIFinalOutput(text string, jsonMode bool) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if block := lastCodexAssistantBlock(text); block != "" {
		if jsonMode {
			if candidate := lastValidJSONObject(block); candidate != "" {
				return candidate
			}
		}
		return block
	}
	if jsonMode && looksLikeStandaloneJSONOutput(text) {
		if candidate := lastValidJSONObject(text); candidate != "" {
			return candidate
		}
	}
	return text
}

func looksLikeStandaloneJSONOutput(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return false
	}
	if strings.Contains(trimmed, "Kernforge request for Codex CLI") {
		return false
	}
	if strings.Contains(trimmed, "\nuser\n") || strings.Contains(trimmed, "\ncodex\n") || strings.Contains(trimmed, "\nexec\n") {
		return false
	}
	return true
}

func lastCodexAssistantBlock(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	last := ""
	for i, line := range lines {
		if strings.TrimSpace(line) != "codex" {
			continue
		}
		start := i + 1
		end := len(lines)
		for j := start; j < len(lines); j++ {
			marker := strings.TrimSpace(lines[j])
			if isCodexCLIMetadataMarker(marker) {
				end = j
				break
			}
		}
		block := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if block != "" {
			last = block
		}
	}
	return last
}

func isCodexCLIMetadataMarker(line string) bool {
	switch strings.TrimSpace(line) {
	case "user", "exec", "tokens used":
		return true
	}
	if strings.Contains(line, " ERROR ") && strings.Contains(line, "codex_core::") {
		return true
	}
	return false
}

func lastValidJSONObject(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	last := ""
	start := -1
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			if depth > 0 {
				inString = true
			}
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				candidate := strings.TrimSpace(text[start : i+1])
				var obj map[string]any
				if err := json.Unmarshal([]byte(candidate), &obj); err == nil && len(obj) > 0 {
					last = candidate
				}
				start = -1
			}
		}
	}
	return last
}
