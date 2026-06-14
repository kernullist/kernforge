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
	ID                          string
	Name                        string
	SupportedInAPI              bool
	Visibility                  string
	Priority                    int
	SupportsImageDetailOriginal bool
	SupportsImageInput          bool
	DefaultVerbosity            string
	SupportsReasoningSummaries  bool
	DefaultReasoningEffort      string
	DefaultReasoningSummary     string
	SupportsParallelToolCalls   bool
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

	args := buildCodexCLIArgs(req.Model, c.extraArgs, promptArg, req)
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
			Slug                        string    `json:"slug"`
			ID                          string    `json:"id"`
			Name                        string    `json:"name"`
			DisplayName                 string    `json:"display_name"`
			SupportedInAPI              *bool     `json:"supported_in_api"`
			Visibility                  string    `json:"visibility"`
			Priority                    int       `json:"priority"`
			SupportsImageDetailOriginal bool      `json:"supports_image_detail_original"`
			DefaultVerbosity            string    `json:"default_verbosity"`
			SupportsReasoningSummaries  *bool     `json:"supports_reasoning_summaries"`
			DefaultReasoningLevel       string    `json:"default_reasoning_level"`
			DefaultReasoningSummary     string    `json:"default_reasoning_summary"`
			SupportsParallelToolCalls   *bool     `json:"supports_parallel_tool_calls"`
			InputModalities             *[]string `json:"input_modalities"`
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
		supportsReasoningSummaries := false
		if item.SupportsReasoningSummaries != nil {
			supportsReasoningSummaries = *item.SupportsReasoningSummaries
		}
		supportsParallelToolCalls := false
		if item.SupportsParallelToolCalls != nil {
			supportsParallelToolCalls = *item.SupportsParallelToolCalls
		}
		supportsImageInput := codexInputModalitiesSupportImages(item.InputModalities)
		models = append(models, CodexCLIModelInfo{
			ID:                          id,
			Name:                        name,
			SupportedInAPI:              item.SupportedInAPI == nil || *item.SupportedInAPI,
			Visibility:                  strings.TrimSpace(item.Visibility),
			Priority:                    item.Priority,
			SupportsImageDetailOriginal: item.SupportsImageDetailOriginal,
			SupportsImageInput:          supportsImageInput,
			DefaultVerbosity:            normalizeOpenAICodexVerbosity(item.DefaultVerbosity),
			SupportsReasoningSummaries:  supportsReasoningSummaries,
			DefaultReasoningEffort:      normalizeReasoningEffort(item.DefaultReasoningLevel),
			DefaultReasoningSummary:     normalizeOpenAICodexReasoningSummary(item.DefaultReasoningSummary),
			SupportsParallelToolCalls:   supportsParallelToolCalls,
		})
		registerCodexModelImageDetailSupport(id, item.SupportsImageDetailOriginal)
		registerCodexModelImageInputSupport(id, supportsImageInput)
		registerOpenAICodexDefaultVerbosity(id, item.DefaultVerbosity)
		if item.SupportsReasoningSummaries != nil || strings.TrimSpace(item.DefaultReasoningLevel) != "" || strings.TrimSpace(item.DefaultReasoningSummary) != "" {
			registerOpenAICodexReasoningDefaults(id, supportsReasoningSummaries, item.DefaultReasoningLevel, item.DefaultReasoningSummary)
		}
		registerOpenAICodexParallelToolCallSupport(id, supportsParallelToolCalls)
		seen[strings.ToLower(id)] = true
	}
	return models, nil
}

func buildCodexCLIArgs(model string, extraArgs []string, prompt string, req ChatRequest) []string {
	args := []string{"exec"}
	if modelValue := codexCLIModelFlagValue(model); modelValue != "" {
		args = append(args, "-c", "model="+modelValue)
	}
	// Codex CLI honors reasoning effort via a config override. Other sampling
	// params (max_tokens, temperature, service_tier) are not exposed as Codex CLI
	// options, so they cannot be forwarded through this bridge.
	if effort := normalizeReasoningEffort(req.ReasoningEffort); effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+effort)
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
		role := cliConversationRoleLabel(msg)
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
		text := modelFacingMessageText(msg)
		if strings.TrimSpace(text) != "" {
			b.WriteString(strings.TrimSpace(text))
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
			if isCodexCLIMetadataMarkerAt(lines, j) {
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

// isCodexCLIMetadataMarkerAt reports whether the line at index j is a codex CLI
// frame metadata marker rather than answer content that merely happens to equal
// one of the marker words. The codex CLI text frame format places section
// headers ("user", "exec") as standalone tokens that head a new frame, and emits
// a "tokens used" trailer immediately followed by a numeric count. Anchoring on
// that structure prevents truncating an answer at a content line such as a bare
// "exec" or "tokens used" inside the model output.
func isCodexCLIMetadataMarkerAt(lines []string, j int) bool {
	if j < 0 || j >= len(lines) {
		return false
	}
	marker := strings.TrimSpace(lines[j])
	// Rollout/error trailer lines are unambiguous regardless of context.
	if strings.Contains(lines[j], " ERROR ") && strings.Contains(lines[j], "codex_core::") {
		return true
	}
	switch marker {
	case "tokens used":
		// The "tokens used" trailer is always followed by a token count line
		// (digits, optionally grouped with commas). A bare "tokens used" line in
		// the answer body is not followed by such a count, so it is not split on.
		return codexCLINextNonBlankIsTokenCount(lines, j+1)
	case "user", "exec":
		// Frame headers head a new frame: they are preceded by a frame boundary
		// (blank line, the codex banner / delimiter, a timestamp or rollout error
		// line, or another frame header) OR they directly follow assistant content
		// inside the alternating transcript. The dangerous case the guard removes
		// is a header word embedded in a paragraph; require that the line stand
		// alone as a structural header by confirming a frame boundary on at least
		// one side.
		return codexCLILineIsFrameBoundary(lines, j-1) ||
			codexCLILineIsFrameHeaderToken(lines, j+1) ||
			codexCLINextLineLooksLikeFrameContent(lines, j)
	}
	return false
}

// codexCLINextNonBlankIsTokenCount reports whether the next non-blank line from
// index k is a numeric token count such as "3,785".
func codexCLINextNonBlankIsTokenCount(lines []string, k int) bool {
	for ; k < len(lines); k++ {
		candidate := strings.TrimSpace(lines[k])
		if candidate == "" {
			continue
		}
		return codexCLILooksLikeTokenCount(candidate)
	}
	return false
}

func codexCLILooksLikeTokenCount(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == ',' || r == '.' || r == ' ' {
			continue
		}
		return false
	}
	// Require at least one digit.
	return strings.ContainsAny(s, "0123456789")
}

// codexCLILineIsFrameBoundary reports whether the line at index k marks a frame
// boundary that can precede a section header.
func codexCLILineIsFrameBoundary(lines []string, k int) bool {
	if k < 0 {
		// Start of transcript is a valid boundary.
		return true
	}
	if k >= len(lines) {
		return false
	}
	line := lines[k]
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "----") {
		return true
	}
	if strings.Contains(line, " ERROR ") && strings.Contains(line, "codex_core::") {
		return true
	}
	if strings.HasPrefix(trimmed, "OpenAI Codex") {
		return true
	}
	if codexCLILineStartsWithTimestamp(trimmed) {
		return true
	}
	switch trimmed {
	case "codex", "user", "exec", "thinking":
		return true
	}
	return false
}

// codexCLILineIsFrameHeaderToken reports whether the line at index k is one of
// the bare frame header tokens.
func codexCLILineIsFrameHeaderToken(lines []string, k int) bool {
	if k < 0 || k >= len(lines) {
		return false
	}
	switch strings.TrimSpace(lines[k]) {
	case "codex", "user", "exec", "thinking":
		return true
	}
	return false
}

// codexCLINextLineLooksLikeFrameContent reports whether the line after a header
// at index j begins a new frame's content (a non-empty line that is not itself a
// stray boundary). This preserves the existing transcript fixtures where "exec"
// directly follows assistant content and precedes the exec command output.
func codexCLINextLineLooksLikeFrameContent(lines []string, j int) bool {
	next := j + 1
	if next >= len(lines) {
		return false
	}
	if strings.TrimSpace(lines[next]) == "" {
		return false
	}
	// The previous line must be assistant/frame content (non-empty) for this to
	// be the alternating user/codex/exec transcript rather than a header word that
	// appears at the very start of a paragraph with no surrounding structure.
	prev := j - 1
	if prev < 0 {
		return false
	}
	return strings.TrimSpace(lines[prev]) != ""
}

func codexCLILineStartsWithTimestamp(line string) bool {
	if len(line) < 10 {
		return false
	}
	// Match an ISO-like date prefix YYYY-MM-DD.
	for idx, r := range line[:10] {
		switch idx {
		case 4, 7:
			if r != '-' {
				return false
			}
		default:
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
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
