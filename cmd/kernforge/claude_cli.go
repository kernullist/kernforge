package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	claudeCLIDefaultExecutable = "claude"
	claudeCLIDefaultModel      = "default"
	claudeCLIPromptArgLimit    = 1024
)

type claudeCLICommandRunner func(ctx context.Context, executable string, args []string, dir string, env []string) ([]byte, error)

type ClaudeCLIClient struct {
	executable string
	extraArgs  []string
	run        claudeCLICommandRunner
}

func NewClaudeCLIClient(executable string, extraArgs []string) *ClaudeCLIClient {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		executable = claudeCLIDefaultExecutable
	}
	cleanArgs := make([]string, 0, len(extraArgs))
	for _, arg := range extraArgs {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			cleanArgs = append(cleanArgs, arg)
		}
	}
	return &ClaudeCLIClient{
		executable: executable,
		extraArgs:  cleanArgs,
		run:        runClaudeCLICommand,
	}
}

func (c *ClaudeCLIClient) Name() string {
	return "anthropic-claude-cli"
}

func (c *ClaudeCLIClient) ModelRouteMetadata() ModelRouteMetadata {
	if c == nil {
		return ModelRouteMetadata{Provider: "anthropic-claude-cli"}
	}
	executable := strings.TrimSpace(c.executable)
	if executable == "" {
		executable = claudeCLIDefaultExecutable
	}
	endpoint := executable
	if len(c.extraArgs) > 0 {
		endpoint += " " + strings.Join(c.extraArgs, " ")
	}
	return ModelRouteMetadata{Provider: "anthropic-claude-cli", BaseURL: endpoint}
}

func (c *ClaudeCLIClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if c == nil {
		return ChatResponse{}, fmt.Errorf("Claude Code CLI client is not configured")
	}
	executable := strings.TrimSpace(c.executable)
	if executable == "" {
		executable = claudeCLIDefaultExecutable
	}
	workingDir := strings.TrimSpace(req.WorkingDir)
	if workingDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workingDir = cwd
		}
	}
	prompt := renderClaudeCLIPrompt(req)
	promptArg, cleanup, err := claudeCLIPromptArgument(workingDir, prompt)
	if err != nil {
		return ChatResponse{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	args := buildClaudeCLIArgs(req.Model, c.extraArgs, promptArg, req)
	env := append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CI=1")
	runner := c.run
	if runner == nil {
		runner = runClaudeCLICommand
	}
	data, err := runner(ctx, executable, args, workingDir, env)
	text := sanitizeClaudeCLIOutput(string(data))
	if req.JSONMode {
		if extracted := lastValidJSONObject(text); strings.TrimSpace(extracted) != "" {
			text = extracted
		}
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ChatResponse{}, ctxErr
		}
		if errors.Is(err, exec.ErrNotFound) {
			return ChatResponse{}, fmt.Errorf("Claude Code CLI executable not found: %s", executable)
		}
		if text != "" {
			return ChatResponse{}, fmt.Errorf("Claude Code CLI command failed: %w\n%s", err, text)
		}
		return ChatResponse{}, fmt.Errorf("Claude Code CLI command failed: %w", err)
	}
	if text == "" {
		return ChatResponse{}, newProviderMessageError("anthropic-claude-cli", "empty Claude Code CLI output", "", "", nil, nil)
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

func runClaudeCLICommand(ctx context.Context, executable string, args []string, dir string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	return cmd.CombinedOutput()
}

func buildClaudeCLIArgs(model string, extraArgs []string, prompt string, req ChatRequest) []string {
	args := []string{}
	if modelValue := claudeCLIModelFlagValue(model); modelValue != "" {
		args = append(args, "--model", modelValue)
	}
	// Forward the request's reasoning effort through the CLI's --effort flag. The
	// Claude Code CLI accepts exactly low/medium/high/xhigh/max, which is the same
	// set normalizeReasoningEffort produces (minus "minimal", which the CLI does
	// not accept and is folded onto "low"). Anything else is dropped so an
	// unsupported value never makes the CLI reject the whole invocation.
	if effort := claudeCLIEffortFlagValue(req.ReasoningEffort); effort != "" {
		args = append(args, "--effort", effort)
	}
	// max_tokens, temperature, and service_tier are NOT exposed by the Claude Code
	// CLI (confirmed against `claude --help`: it has --effort and --fallback-model
	// but no --max-tokens/--temperature/--service-tier). They are intentionally
	// not forwarded here; fabricating those flags would make every Claude CLI
	// invocation fail with an unknown-option error. The CLI uses its own defaults
	// for those knobs.
	for _, arg := range extraArgs {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			args = append(args, arg)
		}
	}
	args = append(args, "-p", prompt)
	return args
}

// claudeCLIEffortFlagValue maps a Kernforge reasoning effort onto the value the
// Claude Code CLI's --effort flag accepts (low, medium, high, xhigh, max). It
// returns "" when the effort is unset or unsupported by the CLI so the flag is
// omitted rather than passed an invalid value. "minimal" has no CLI equivalent,
// so it is folded onto the closest supported level, "low".
func claudeCLIEffortFlagValue(effort string) string {
	switch normalizeReasoningEffort(effort) {
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	case "max":
		return "max"
	default:
		return ""
	}
}

func claudeCLIModelFlagValue(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.EqualFold(model, claudeCLIDefaultModel) {
		return ""
	}
	// Map versioned Anthropic model ids onto the Claude Code CLI's stable family
	// aliases (fable, opus, sonnet, haiku). The CLI accepts either an alias for
	// the latest model in a family or a full model name, so when an id is not a
	// recognized family member it is passed through verbatim (custom or future
	// full names still work). Keep the current 4.6/4.7/4.8/fable ids in sync with
	// the model catalog so a live config model resolves to the right alias.
	switch strings.ToLower(model) {
	case "claude-fable-5", "claude-fable5", "fable", "fable-5", "fable5",
		"claude-mythos-5", "claude-mythos5", "mythos", "mythos-5", "mythos5":
		return "fable"
	case "claude-sonnet-4-6", "claude-sonnet-4.6", "sonnet-4-6", "sonnet-4.6",
		"claude-sonnet-4-7", "claude-sonnet-4.7", "sonnet-4-7", "sonnet-4.7",
		"claude-sonnet-4-5", "claude-sonnet-4.5", "sonnet-4-5", "sonnet-4.5":
		return "sonnet"
	case "claude-opus-4-8", "claude-opus-4.8", "opus-4-8", "opus-4.8",
		"claude-opus-4-7", "claude-opus-4.7", "opus-4-7", "opus-4.7",
		"claude-opus-4-6", "claude-opus-4.6", "opus-4-6", "opus-4.6",
		"claude-opus-4-5", "claude-opus-4.5", "opus-4-5", "opus-4.5":
		return "opus"
	case "claude-haiku-4-5", "claude-haiku-4.5", "haiku-4-5", "haiku-4.5",
		"claude-haiku-3-5", "claude-haiku-3.5", "claude-3-5-haiku-latest", "haiku-3-5", "haiku-3.5":
		return "haiku"
	}
	return model
}

func renderClaudeCLIPrompt(req ChatRequest) string {
	var b strings.Builder
	b.WriteString("# Kernforge request for Claude Code CLI\n\n")
	b.WriteString("You are running as the Claude Code CLI provider behind Kernforge. Use your native local repository access when needed, and return the final assistant answer as plain Markdown. Do not emit Kernforge tool-call JSON.\n")
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
		b.WriteString("Kernforge tool schemas are not forwarded through this provider bridge. If repository inspection or edits are required, use Claude Code CLI's native capabilities and summarize what changed or what blocked the work.\n")
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

func claudeCLIPromptArgument(workingDir string, prompt string) (string, func(), error) {
	if len(prompt) <= claudeCLIPromptArgLimit {
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
	path := filepath.Join(tmpDir, fmt.Sprintf("claude-cli-request-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(prompt+"\n"), 0o600); err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(path)
	}
	return "Read the complete Kernforge request from this file, follow it, and return the final answer: " + path, cleanup, nil
}

func sanitizeClaudeCLIOutput(text string) string {
	text = ansiPattern.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}
