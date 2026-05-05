package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type mcpCodeReviewContext struct {
	Text         string
	Sources      []string
	ChangedPaths []string
	Warnings     []string
}

func (s *kernforgeMCPServer) toolReviewCode(ctx context.Context, args map[string]any) (string, error) {
	request := strings.TrimSpace(stringValue(args, "request"))
	if request == "" {
		request = "Review the current code changes."
	}
	maxContextChars := mcpReviewMaxContextChars(args, 60000)
	reviewCtx, err := s.buildMCPCodeReviewContext(ctx, args, maxContextChars)
	if err != nil {
		return "", err
	}
	if !mcpReviewHasReviewableContext(reviewCtx) {
		return s.renderEmptyMCPCodeReviewContext(request, reviewCtx), nil
	}
	if err := s.ensureProviderReady(); err != nil {
		return "", err
	}
	prompt := buildMCPCodeReviewPrompt(request, s.rt.workspace.Root, s.rt.cfg, reviewCtx)
	resp, err := s.rt.agent.completeModelTurn(ctx, ChatRequest{
		Model:           s.rt.cfg.Model,
		System:          mcpCodeReviewSystemPrompt(),
		Messages:        []Message{{Role: "user", Text: prompt}},
		MaxTokens:       s.rt.cfg.MaxTokens,
		Temperature:     0.1,
		ReasoningEffort: s.rt.cfg.ReasoningEffort,
		WorkingDir:      s.rt.workspace.Root,
	})
	if err != nil {
		return "", err
	}
	review := strings.TrimSpace(resp.Message.Text)
	if review == "" {
		review = "(main model returned an empty review)"
	}
	var b strings.Builder
	b.WriteString("KernForge main-model code review\n\n")
	b.WriteString("- Workspace: ")
	b.WriteString(filepath.ToSlash(s.rt.workspace.Root))
	b.WriteString("\n")
	b.WriteString("- Model: ")
	b.WriteString(formatProviderModelEffortLabel(s.rt.cfg.Provider, s.rt.cfg.Model, s.rt.cfg.ReasoningEffort))
	b.WriteString("\n")
	if len(reviewCtx.Sources) > 0 {
		b.WriteString("- Context: ")
		b.WriteString(strings.Join(reviewCtx.Sources, ", "))
		b.WriteString("\n")
	}
	if len(reviewCtx.ChangedPaths) > 0 {
		b.WriteString("- Changed paths: ")
		b.WriteString(strings.Join(limitStrings(reviewCtx.ChangedPaths, 16), ", "))
		if len(reviewCtx.ChangedPaths) > 16 {
			fmt.Fprintf(&b, " (+%d more)", len(reviewCtx.ChangedPaths)-16)
		}
		b.WriteString("\n")
	}
	for _, warning := range reviewCtx.Warnings {
		b.WriteString("- Warning: ")
		b.WriteString(warning)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(review)
	return mcpLimitText(b.String(), mcpMaxChars(args, 40000)), nil
}

func (s *kernforgeMCPServer) buildMCPCodeReviewContext(ctx context.Context, args map[string]any, maxChars int) (mcpCodeReviewContext, error) {
	var out mcpCodeReviewContext
	paths := s.mcpReviewWorkspacePaths(stringSliceValue(args, "paths"))
	providedDiff := strings.TrimSpace(stringValue(args, "diff"))
	providedCode := strings.TrimSpace(stringValue(args, "code"))
	if providedDiff != "" {
		mcpReviewAppendSection(&out, "Provided unified diff", providedDiff, "provided_diff", maxChars)
	}
	if providedCode != "" {
		mcpReviewAppendSection(&out, "Provided code", providedCode, "provided_code", maxChars)
	}
	includeGit := boolValue(args, "include_git_diff", providedDiff == "" && providedCode == "")
	if includeGit {
		gitCtx := s.collectMCPGitReviewContext(ctx, paths, maxChars-len(out.Text))
		out.Sources = append(out.Sources, gitCtx.Sources...)
		out.ChangedPaths = append(out.ChangedPaths, gitCtx.ChangedPaths...)
		out.Warnings = append(out.Warnings, gitCtx.Warnings...)
		if strings.TrimSpace(gitCtx.Text) != "" {
			if strings.TrimSpace(out.Text) != "" {
				out.Text += "\n\n"
			}
			out.Text += gitCtx.Text
		}
	}
	includeFiles := boolValue(args, "include_file_contents", false)
	if includeFiles || (strings.TrimSpace(out.Text) == "" && len(paths) > 0) {
		fileCtx := s.collectMCPFileReviewContext(paths, maxChars-len(out.Text))
		out.Sources = append(out.Sources, fileCtx.Sources...)
		out.ChangedPaths = append(out.ChangedPaths, fileCtx.ChangedPaths...)
		out.Warnings = append(out.Warnings, fileCtx.Warnings...)
		if strings.TrimSpace(fileCtx.Text) != "" {
			if strings.TrimSpace(out.Text) != "" {
				out.Text += "\n\n"
			}
			out.Text += fileCtx.Text
		}
	}
	out.Sources = analysisUniqueStrings(out.Sources)
	out.ChangedPaths = analysisUniqueStrings(out.ChangedPaths)
	out.Warnings = analysisUniqueStrings(out.Warnings)
	if len(out.Text) > maxChars && maxChars > 0 {
		out.Text = mcpLimitText(out.Text, maxChars)
	}
	return out, nil
}

func (s *kernforgeMCPServer) mcpReviewWorkspacePaths(paths []string) []string {
	cleaned := mcpReviewCleanPaths(paths)
	if len(cleaned) == 0 {
		return nil
	}
	out := make([]string, 0, len(cleaned))
	for _, path := range cleaned {
		resolved, err := s.rt.workspace.Resolve(path)
		if err != nil {
			out = append(out, path)
			continue
		}
		rel := filepath.ToSlash(relOrAbs(s.rt.workspace.Root, resolved))
		if rel == "" {
			rel = path
		}
		out = append(out, rel)
	}
	return mcpReviewCleanPaths(out)
}

func (s *kernforgeMCPServer) collectMCPGitReviewContext(ctx context.Context, paths []string, maxChars int) mcpCodeReviewContext {
	var out mcpCodeReviewContext
	if maxChars <= 0 {
		out.Warnings = append(out.Warnings, "review context budget was exhausted before git diff collection")
		return out
	}
	status, err := runMCPReviewGit(ctx, s.rt.workspace.Root, "status", "--short", "--branch")
	if err != nil {
		out.Warnings = append(out.Warnings, "git status unavailable: "+firstNonEmptyLine(status+"\n"+err.Error()))
		return out
	}
	changed := parseMCPReviewGitStatusPaths(status)
	if len(paths) > 0 {
		changed = filterMCPReviewPaths(changed, paths)
	}
	out.ChangedPaths = append(out.ChangedPaths, changed...)
	if len(changed) == 0 {
		out.Warnings = append(out.Warnings, "git status found no changed paths for review")
		return out
	}
	mcpReviewAppendSection(&out, "Git status", status, "git_status", maxChars)
	pathArgs, pathErr := s.mcpReviewGitPathArgs(paths)
	if pathErr != nil {
		out.Warnings = append(out.Warnings, pathErr.Error())
		return out
	}
	unstagedArgs := append([]string{"diff", "--no-ext-diff"}, pathArgs...)
	unstaged, unstagedErr := runMCPReviewGit(ctx, s.rt.workspace.Root, unstagedArgs...)
	if unstagedErr != nil {
		out.Warnings = append(out.Warnings, "git diff unavailable: "+firstNonEmptyLine(unstaged+"\n"+unstagedErr.Error()))
	} else {
		mcpReviewAppendSection(&out, "Unstaged git diff", unstaged, "git_diff", maxChars)
	}
	stagedArgs := append([]string{"diff", "--no-ext-diff", "--staged"}, pathArgs...)
	staged, stagedErr := runMCPReviewGit(ctx, s.rt.workspace.Root, stagedArgs...)
	if stagedErr != nil {
		out.Warnings = append(out.Warnings, "git diff --staged unavailable: "+firstNonEmptyLine(staged+"\n"+stagedErr.Error()))
	} else {
		mcpReviewAppendSection(&out, "Staged git diff", staged, "git_diff_staged", maxChars)
	}
	untracked := parseMCPReviewUntrackedPaths(status)
	if len(paths) > 0 {
		untracked = filterMCPReviewPaths(untracked, paths)
	}
	if len(untracked) > 0 {
		fileCtx := s.collectMCPFileReviewContext(limitStrings(untracked, 8), maxChars-len(out.Text))
		out.Sources = append(out.Sources, fileCtx.Sources...)
		out.ChangedPaths = append(out.ChangedPaths, fileCtx.ChangedPaths...)
		out.Warnings = append(out.Warnings, fileCtx.Warnings...)
		if strings.TrimSpace(fileCtx.Text) != "" {
			if strings.TrimSpace(out.Text) != "" {
				out.Text += "\n\n"
			}
			out.Text += fileCtx.Text
		}
	}
	return out
}

func (s *kernforgeMCPServer) collectMCPFileReviewContext(paths []string, maxChars int) mcpCodeReviewContext {
	var out mcpCodeReviewContext
	if maxChars <= 0 || len(paths) == 0 {
		return out
	}
	for _, path := range paths {
		if len(out.Text) >= maxChars {
			out.Warnings = append(out.Warnings, "file excerpt context was truncated")
			break
		}
		if shouldSkipMCPReviewFile(path) {
			continue
		}
		resolved, err := s.rt.workspace.Resolve(path)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("skipped %s: %v", path, err))
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("skipped %s: %v", path, err))
			continue
		}
		if info.IsDir() {
			continue
		}
		if info.Size() > 256*1024 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("skipped %s: file is too large for review excerpt", filepath.ToSlash(path)))
			continue
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("skipped %s: %v", path, err))
			continue
		}
		if !isText(data) {
			out.Warnings = append(out.Warnings, fmt.Sprintf("skipped %s: binary or non-text file", filepath.ToSlash(path)))
			continue
		}
		rel := filepath.ToSlash(relOrAbs(s.rt.workspace.Root, resolved))
		out.ChangedPaths = append(out.ChangedPaths, rel)
		mcpReviewAppendSection(&out, "File excerpt: "+rel, string(data), "file_excerpt", maxChars)
	}
	return out
}

func (s *kernforgeMCPServer) mcpReviewGitPathArgs(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := []string{"--"}
	for _, path := range paths {
		resolved, err := s.rt.workspace.Resolve(path)
		if err != nil {
			return nil, fmt.Errorf("invalid review path %s: %w", path, err)
		}
		out = append(out, filepath.ToSlash(relOrAbs(s.rt.workspace.Root, resolved)))
	}
	return out, nil
}

func runMCPReviewGit(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mcpReviewAppendSection(ctx *mcpCodeReviewContext, title string, body string, source string, maxChars int) {
	body = strings.TrimSpace(body)
	if body == "" || body == "(no diff)" {
		return
	}
	section := "## " + strings.TrimSpace(title) + "\n\n```text\n" + body + "\n```"
	if maxChars > 0 && len(ctx.Text)+len(section)+2 > maxChars {
		remaining := maxChars - len(ctx.Text) - len("## "+strings.TrimSpace(title)+"\n\n```text\n\n```\n\n... (truncated)")
		if remaining <= 256 {
			ctx.Warnings = append(ctx.Warnings, strings.TrimSpace(title)+" omitted because review context budget was exhausted")
			return
		}
		if remaining > len(body) {
			remaining = len(body)
		}
		body = strings.TrimSpace(body[:remaining])
		section = "## " + strings.TrimSpace(title) + "\n\n```text\n" + body + "\n```\n\n... (truncated)"
		ctx.Warnings = append(ctx.Warnings, strings.TrimSpace(title)+" was truncated to fit the review context budget")
	}
	if strings.TrimSpace(ctx.Text) != "" {
		ctx.Text += "\n\n"
	}
	ctx.Text += section
	if strings.TrimSpace(source) != "" {
		ctx.Sources = append(ctx.Sources, source)
	}
}

func buildMCPCodeReviewPrompt(request string, workspaceRoot string, cfg Config, reviewCtx mcpCodeReviewContext) string {
	var b strings.Builder
	b.WriteString("Review request:\n")
	b.WriteString(strings.TrimSpace(request))
	b.WriteString("\n\nWorkspace:\n")
	b.WriteString(filepath.ToSlash(workspaceRoot))
	b.WriteString("\n\nMain model route:\n")
	b.WriteString(formatProviderModelEffortLabel(cfg.Provider, cfg.Model, cfg.ReasoningEffort))
	if len(reviewCtx.ChangedPaths) > 0 {
		b.WriteString("\n\nChanged paths:\n")
		for _, path := range limitStrings(reviewCtx.ChangedPaths, 64) {
			b.WriteString("- ")
			b.WriteString(filepath.ToSlash(path))
			b.WriteString("\n")
		}
	}
	if len(reviewCtx.Warnings) > 0 {
		b.WriteString("\nContext warnings:\n")
		for _, warning := range reviewCtx.Warnings {
			b.WriteString("- ")
			b.WriteString(warning)
			b.WriteString("\n")
		}
	}
	b.WriteString("\nReview context:\n")
	b.WriteString(reviewCtx.Text)
	return b.String()
}

func mcpCodeReviewSystemPrompt() string {
	return strings.TrimSpace(`
You are KernForge's configured main model performing a read-only code review for an MCP client.

The MCP client may be Codex, Claude, or another coding agent that just wrote or changed code. Review only the provided diff, code, file excerpts, and metadata. Do not ask to edit files, do not claim you ran tests, and do not invent source context that is absent.

Output findings first, ordered by severity. For each finding include severity, path or symbol, the concrete risk, and a precise fix direction. Prioritize correctness, security boundaries, stability, data loss, race conditions, API contract regressions, and missing tests. If there are no blocking findings, say so clearly and list residual risks or test gaps. Answer in the same language as the review request unless it explicitly asks otherwise.
`)
}

func (s *kernforgeMCPServer) renderEmptyMCPCodeReviewContext(request string, reviewCtx mcpCodeReviewContext) string {
	var b strings.Builder
	b.WriteString("KernForge main-model code review\n\n")
	b.WriteString("No reviewable code context was found, so the main model was not called.\n\n")
	b.WriteString("Request: ")
	b.WriteString(request)
	b.WriteString("\n")
	b.WriteString("Workspace: ")
	b.WriteString(filepath.ToSlash(s.rt.workspace.Root))
	b.WriteString("\n\n")
	b.WriteString("Pass one of these inputs:\n")
	b.WriteString("- diff: a unified diff from the MCP client\n")
	b.WriteString("- code: a code excerpt to review\n")
	b.WriteString("- paths: changed files to include as excerpts\n")
	b.WriteString("- or leave git changes in the workspace so KernForge can collect git diff\n")
	if len(reviewCtx.Warnings) > 0 {
		b.WriteString("\nWarnings:\n")
		for _, warning := range reviewCtx.Warnings {
			b.WriteString("- ")
			b.WriteString(warning)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func mcpReviewMaxContextChars(args map[string]any, fallback int) int {
	value := intValue(args, "max_context_chars", fallback)
	if value <= 0 {
		value = fallback
	}
	if value < 4000 {
		return 4000
	}
	if value > 200000 {
		return 200000
	}
	return value
}

func mcpReviewHasReviewableContext(reviewCtx mcpCodeReviewContext) bool {
	for _, source := range reviewCtx.Sources {
		switch strings.TrimSpace(source) {
		case "provided_diff",
			"provided_code",
			"git_diff",
			"git_diff_staged",
			"file_excerpt":
			return true
		}
	}
	return false
}

func mcpReviewCleanPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func parseMCPReviewGitStatusPaths(status string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(status, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "## ") {
			continue
		}
		if len(line) < 3 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if path != "" {
			out = append(out, filepath.ToSlash(path))
		}
	}
	return analysisUniqueStrings(out)
}

func parseMCPReviewUntrackedPaths(status string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(status, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "?? ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(trimmed, "?? "))
		if path != "" && !shouldSkipMCPReviewFile(path) {
			out = append(out, filepath.ToSlash(path))
		}
	}
	return analysisUniqueStrings(out)
}

func filterMCPReviewPaths(paths []string, allowed []string) []string {
	if len(allowed) == 0 {
		return paths
	}
	allowed = mcpReviewCleanPaths(allowed)
	var out []string
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		for _, prefix := range allowed {
			if path == prefix || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
				out = append(out, path)
				break
			}
		}
	}
	return analysisUniqueStrings(out)
}

func shouldSkipMCPReviewFile(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if normalized == "" {
		return true
	}
	if strings.HasPrefix(normalized, ".git/") ||
		strings.HasPrefix(normalized, ".kernforge/") ||
		strings.HasPrefix(normalized, "release/") {
		return true
	}
	switch filepath.Ext(normalized) {
	case ".exe", ".dll", ".sys", ".pdb", ".obj", ".lib", ".bin", ".zip", ".7z", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf":
		return true
	default:
		return false
	}
}
