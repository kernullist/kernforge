package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCommandSpecsCoverPublicSlashCommands(t *testing.T) {
	specs := commandSpecs()
	if len(specs) != len(slashCommands) {
		t.Fatalf("commandSpecs count = %d, slashCommands count = %d", len(specs), len(slashCommands))
	}

	seen := map[string]CommandSpec{}
	advertised := 0
	for _, spec := range specs {
		if strings.TrimSpace(spec.Canonical) == "" {
			t.Fatalf("command spec has empty canonical name: %#v", spec)
		}
		switch spec.Visibility {
		case CommandVisibilityPublic:
			advertised++
		case CommandVisibilityHidden:
			// Folded aliases and expert commands stay dispatchable but off the default /help map.
		default:
			t.Fatalf("%s visibility = %q, want public or hidden", spec.Canonical, spec.Visibility)
		}
		if strings.TrimSpace(spec.Family) == "" {
			t.Fatalf("%s has empty family", spec.Canonical)
		}
		if strings.TrimSpace(spec.HelpTopic) == "" {
			t.Fatalf("%s has empty help topic", spec.Canonical)
		}
		if strings.TrimSpace(spec.Completion) == "" {
			t.Fatalf("%s has empty completion key", spec.Canonical)
		}
		if strings.TrimSpace(spec.Handler) == "" {
			t.Fatalf("%s has empty handler mapping", spec.Canonical)
		}
		seen[spec.Canonical] = spec
	}
	if advertised != len(advertisedSlashCommands()) {
		t.Fatalf("advertised command count = %d, want %d", advertised, len(advertisedSlashCommands()))
	}
	for _, command := range advertisedSlashCommands() {
		spec, ok := seen[command]
		if !ok {
			t.Fatalf("missing advertised command %q in slashCommands/specs", command)
		}
		if spec.Visibility != CommandVisibilityPublic {
			t.Fatalf("%s should be public, got %q", command, spec.Visibility)
		}
	}

	for _, command := range slashCommands {
		spec, ok := seen[command]
		if !ok {
			t.Fatalf("missing command spec for slash command %q", command)
		}
		if spec.Canonical != command {
			t.Fatalf("spec canonical mismatch for %q: %#v", command, spec)
		}
	}
}

func TestCommandSpecsHaveHelpCoverage(t *testing.T) {
	overview := HelpText()
	full := HelpTextAll()
	for _, spec := range commandSpecs() {
		if _, ok := HelpDetail(spec.HelpTopic); ok {
			continue
		}
		needle := "/" + spec.Canonical
		if strings.Contains(overview, needle) || strings.Contains(full, needle) {
			continue
		}
		t.Fatalf("%s has no detail help topic %q and is missing from /help and /help all", spec.Canonical, spec.HelpTopic)
	}
}

func TestHelpTextIsShortEverydayMap(t *testing.T) {
	text := HelpText()
	if !strings.Contains(text, "Everyday:") || !strings.Contains(text, "Hubs") {
		t.Fatalf("expected layered /help overview, got:\n%s", text)
	}
	if strings.Contains(text, "/fuzz-func") || strings.Contains(text, "/use-selection") {
		t.Fatalf("default /help should not advertise expert aliases, got:\n%s", text)
	}
	if !strings.Contains(HelpTextAll(), "/fuzz-func") {
		t.Fatalf("expected /help all to include expert catalog")
	}
}

func TestCompletionPrefersAdvertisedCommands(t *testing.T) {
	matches := completionSlashCommandMatches("", slashCommands)
	if len(matches) != len(advertisedSlashCommands()) {
		t.Fatalf("empty prefix matches = %d, want %d advertised", len(matches), len(advertisedSlashCommands()))
	}
	hidden := completionSlashCommandMatches("fuzz-f", slashCommands)
	if len(hidden) != 1 || hidden[0] != "fuzz-func" {
		t.Fatalf("expected hidden prefix match fuzz-func, got %#v", hidden)
	}
	// Shared prefixes must still surface aliases (reload/resume) alongside review.
	shared := completionSlashCommandMatches("re", slashCommands)
	joined := strings.Join(shared, ",")
	if !strings.Contains(joined, "review") || !strings.Contains(joined, "reload") || !strings.Contains(joined, "resume") {
		t.Fatalf("expected /re to include review+reload+resume, got %#v", shared)
	}
	if shared[0] != "review" {
		t.Fatalf("expected advertised review first for /re, got %#v", shared)
	}
}

func TestProbeHubCompletesFuzzFuncArgs(t *testing.T) {
	rt := &runtimeState{}
	suggestions, replaceIndex, ok := rt.slashArgumentSuggestions("probe", []string{"fuzz"}, true)
	if !ok || replaceIndex != 1 || len(suggestions) == 0 {
		t.Fatalf("expected /probe fuzz completions, got ok=%v idx=%d suggestions=%#v", ok, replaceIndex, suggestions)
	}
}

var removedLegacySlashCommands = []string{
	"review-pr",
	"review-selection",
	"review-selections",
	"do-plan-review",
	"set-plan-review",
	"profile-review",
	"set-analysis-models",
	"set-specialist-model",
	"suggest-dashboard-html",
	"session-dashboard-html",
	"sessions",
	"events",
	"continuity",
	"completion-audit",
	"recover",
	"jobs",
	"handoff",
	"tasks",
	"verify-dashboard",
	"verify-dashboard-html",
	"investigate-dashboard",
	"investigate-dashboard-html",
	"simulate-dashboard",
	"simulate-dashboard-html",
	"evidence-search",
	"evidence-show",
	"evidence-dashboard",
	"evidence-dashboard-html",
	"mem",
	"mem-search",
	"mem-show",
	"mem-promote",
	"mem-demote",
	"mem-confirm",
	"mem-tentative",
	"mem-dashboard",
	"mem-dashboard-html",
	"mem-prune",
	"mem-stats",
	"override-add",
	"override-clear",
	"checkpoint-auto",
	"checkpoint-diff",
	"checkpoints",
	"rollback",
	"detect-verification-tools",
	"set-msbuild-path",
	"clear-msbuild-path",
	"set-cmake-path",
	"clear-cmake-path",
	"set-ctest-path",
	"clear-ctest-path",
	"set-ninja-path",
	"clear-ninja-path",
}

func TestRemovedLegacySlashCommandsAreNotPublicMetadata(t *testing.T) {
	public := map[string]bool{}
	for _, command := range slashCommands {
		public[command] = true
	}

	for _, command := range removedLegacySlashCommands {
		if public[command] {
			t.Fatalf("removed legacy command %q is still exposed as public slash command", command)
		}
		if _, ok := slashCommandDescriptions[command]; ok {
			t.Fatalf("removed legacy command %q still has top-level completion description", command)
		}
		if _, ok := slashSubcommandDescriptions[command]; ok {
			t.Fatalf("removed legacy command %q still has subcommand completion metadata", command)
		}
	}
}

func TestRemovedLegacySlashCommandsAreNotDispatched(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	rt := &runtimeState{
		writer:      &output,
		ui:          UI{color: false},
		cfg:         DefaultConfig(root),
		workspace:   Workspace{Root: root, BaseRoot: root},
		store:       NewSessionStore(filepath.Join(root, "sessions")),
		session:     NewSession(root, "openai", "gpt-main", "", "default"),
		checkpoints: &CheckpointManager{Root: filepath.Join(root, "checkpoints")},
	}

	for _, command := range []string{"mem", "checkpoints", "set-analysis-models", "set-specialist-model", "verify-dashboard-html"} {
		output.Reset()
		if handled, err := rt.handleCommand(Command{Name: command}); err == nil || handled {
			t.Fatalf("expected removed legacy command %q to be rejected, handled=%t err=%v", command, handled, err)
		}
		if strings.Contains(output.String(), "deprecated; use") {
			t.Fatalf("removed legacy command %q still emitted deprecation guidance: %q", command, output.String())
		}
	}
}

func TestModelHubScriptableAnalysisAndTaskOwnerRoutes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	var output bytes.Buffer
	rt := &runtimeState{
		writer:    &output,
		ui:        UI{color: false},
		cfg:       cfg,
		workspace: Workspace{Root: root, BaseRoot: root},
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		session:   NewSession(root, "openai", "gpt-main", "", "default"),
	}

	if _, err := rt.handleCommand(Command{Name: "model", Args: "analysis-worker deepseek deepseek-reasoner high"}); err != nil {
		t.Fatalf("configure analysis worker through /model: %v", err)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile == nil {
		t.Fatalf("expected analysis worker profile to be configured")
	}
	if got := rt.cfg.ProjectAnalysis.WorkerProfile.Provider; got != "deepseek" {
		t.Fatalf("analysis worker provider = %q", got)
	}
	if got := rt.cfg.ProjectAnalysis.WorkerProfile.Model; got != "deepseek-reasoner" {
		t.Fatalf("analysis worker model = %q", got)
	}
	if got := rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort; got != "high" {
		t.Fatalf("analysis worker reasoning effort = %q", got)
	}
	if _, err := rt.handleCommand(Command{Name: "model", Args: "analysis-reviewer openai gpt-analysis"}); err != nil {
		t.Fatalf("configure analysis reviewer through /model: %v", err)
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile == nil {
		t.Fatalf("expected analysis reviewer profile to be configured")
	}
	if _, err := rt.handleCommand(Command{Name: "model", Args: "analysis-worker 0"}); err != nil {
		t.Fatalf("reset analysis worker through /model: %v", err)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile != nil {
		t.Fatalf("expected analysis worker profile to be reset")
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile == nil {
		t.Fatalf("analysis reviewer profile should remain configured after worker reset")
	}
	if _, err := rt.handleCommand(Command{Name: "model", Args: "analysis reviewer 0"}); err != nil {
		t.Fatalf("reset analysis reviewer through /model alias: %v", err)
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile != nil {
		t.Fatalf("expected analysis reviewer profile to be reset")
	}
	if _, err := rt.handleCommand(Command{Name: "model", Args: "analysis clear"}); err != nil {
		t.Fatalf("clear analysis routes through /model: %v", err)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile != nil || rt.cfg.ProjectAnalysis.ReviewerProfile != nil {
		t.Fatalf("expected analysis routes to be cleared")
	}
	if _, err := rt.handleCommand(Command{Name: "model", Args: "task-owner planner deepseek deepseek-reasoner medium"}); err != nil {
		t.Fatalf("configure task owner through /model: %v", err)
	}
	profile, ok := configuredSpecialistProfileByName(rt.cfg, "planner")
	if !ok {
		t.Fatalf("expected planner task owner profile")
	}
	if got := profile.Provider; got != "deepseek" {
		t.Fatalf("task owner provider = %q", got)
	}
	if got := profile.Model; got != "deepseek-reasoner" {
		t.Fatalf("task owner model = %q", got)
	}
	if got := profile.ReasoningEffort; got != "medium" {
		t.Fatalf("task owner reasoning effort = %q", got)
	}
}

func TestRemovedLegacySlashCommandsDoNotHaveDirectHandleCommandCases(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	text := string(content)
	start := strings.Index(text, "func (rt *runtimeState) handleCommand(cmd Command) (bool, error) {")
	end := strings.Index(text, "func (rt *runtimeState) handleLocaleAutoCommand(args string) error {")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("could not isolate handleCommand body")
	}
	body := text[start:end]
	for _, command := range removedLegacySlashCommands {
		if strings.Contains(body, "case \""+command+"\"") {
			t.Fatalf("removed legacy command %q still has a direct handleCommand case", command)
		}
	}
}

func TestMCPToolsDocumentedInMcpSkills(t *testing.T) {
	server := newKernforgeMCPServer(&runtimeState{})
	docPath := filepath.Join("..", "..", "MCP-SKILLS.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}

	documented := documentedMCPToolNames(string(content))
	for name := range server.tools {
		if !documented[name] {
			t.Fatalf("MCP-SKILLS.md is missing registered tool %q", name)
		}
	}

	koreanGuidePath := filepath.Join("..", "..", "MCP_SERVER_MODE_kor.md")
	koreanGuide, err := os.ReadFile(koreanGuidePath)
	if err != nil {
		t.Fatalf("read %s: %v", koreanGuidePath, err)
	}
	koreanGuideText := string(koreanGuide)
	for name := range server.tools {
		if !strings.Contains(koreanGuideText, name) {
			t.Fatalf("MCP_SERVER_MODE_kor.md is missing registered tool %q", name)
		}
	}
}

func documentedMCPToolNames(content string) map[string]bool {
	documented := map[string]bool{}
	toolListLine := regexp.MustCompile(`(?m)^- Tools:\s*(.+)$`).FindStringSubmatch(content)
	if len(toolListLine) != 2 {
		return documented
	}
	for _, match := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(toolListLine[1], -1) {
		if len(match) == 2 {
			documented[strings.TrimSpace(match[1])] = true
		}
	}
	return documented
}
