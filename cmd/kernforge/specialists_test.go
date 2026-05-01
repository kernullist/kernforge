package main

import (
	"strings"
	"testing"
)

func TestConfiguredSpecialistProfilesMergeBuiltinsAndOverrides(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Specialists.Profiles = []SpecialistSubagentProfile{
		{
			Name:      "planner",
			Model:     "gpt-5.4-mini",
			Provider:  "openai",
			Keywords:  []string{"plan", "sequence", "owner"},
			ReadOnly:  boolPtr(true),
			Prompt:    "Override planner prompt.",
			BaseURL:   "https://api.openai.com/v1",
			APIKey:    "test-key",
			NodeKinds: []string{"task", "edit"},
			Editable:  boolPtr(false),
		},
		{
			Name:           "binary-forensics",
			Description:    "Investigates binary-level drift and symbol mismatches.",
			Keywords:       []string{"pdb", "guid", "hash"},
			NodeKinds:      []string{"inspection"},
			Editable:       boolPtr(true),
			OwnershipPaths: []string{"symbols/**", "*.pdb"},
		},
	}

	profiles := configuredSpecialistProfiles(cfg)
	byName := map[string]SpecialistSubagentProfile{}
	for _, profile := range profiles {
		byName[profile.Name] = profile
	}

	planner, ok := byName["planner"]
	if !ok {
		t.Fatalf("expected planner profile in merged catalog")
	}
	if planner.Provider != "openai" || planner.Model != "gpt-5.4-mini" {
		t.Fatalf("expected planner override to win, got %#v", planner)
	}
	if planner.Prompt != "Override planner prompt." {
		t.Fatalf("expected planner prompt override, got %#v", planner)
	}
	custom, ok := byName["binary-forensics"]
	if !ok {
		t.Fatalf("expected custom specialist profile to be appended")
	}
	if !specialistProfileEditable(custom) {
		t.Fatalf("expected custom profile to preserve editable flag")
	}
	if len(custom.OwnershipPaths) != 2 {
		t.Fatalf("expected ownership paths to be preserved, got %#v", custom.OwnershipPaths)
	}
}

func TestSelectSpecialistForTaskNodePrefersKernelInvestigator(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	node := TaskNode{
		ID:     "plan-01",
		Title:  "Investigate driver verifier failure for anti-cheat .sys package",
		Kind:   "inspection",
		Status: "ready",
	}
	state := &TaskState{
		Goal: "Inspect a Windows kernel anti-cheat regression and identify the next verification step.",
	}

	assignment, ok := selectSpecialistForTaskNode(cfg, node, state, "executor", true)
	if !ok {
		t.Fatalf("expected specialist routing to succeed")
	}
	if assignment.Profile.Name != "kernel-investigator" {
		t.Fatalf("expected kernel-investigator, got %#v", assignment)
	}
	if assignment.Score <= 0 {
		t.Fatalf("expected positive routing score, got %#v", assignment)
	}
}

func TestSelectEditableSpecialistForTaskNodePrefersImplementationOwner(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	node := TaskNode{
		ID:     "plan-02",
		Title:  "Implement specialist ownership routing for edit tools",
		Kind:   "edit",
		Status: "ready",
	}
	state := &TaskState{
		Goal: "Extend editable ownership and worktree routing for specialist nodes.",
	}

	assignment, ok := selectEditableSpecialistForTaskNode(cfg, node, state, "executor-focus")
	if !ok {
		t.Fatalf("expected editable specialist routing to succeed")
	}
	if assignment.Profile.Name != "implementation-owner" {
		t.Fatalf("expected implementation-owner, got %#v", assignment)
	}
	if !specialistProfileEditable(assignment.Profile) {
		t.Fatalf("expected selected profile to be editable")
	}
}

func TestSpecialistBatchRouteLimiterSerializesLocalDuplicateRoute(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "ollama"
	cfg.Model = "llama-local"
	agent := &Agent{Config: cfg}
	client := NewOllamaClient(cfg.BaseURL, cfg.APIKey)
	route := modelRouteForRequest(cfg, client, ChatRequest{Model: cfg.Model})

	limiter := agent.specialistBatchRouteLimiter([]ModelRoute{route, route})
	sem := limiter.semaphores[route.Key]
	if sem == nil || cap(sem) != 1 {
		t.Fatalf("expected duplicate local specialist route to get limit 1 semaphore, got %#v", sem)
	}
}

func TestSpecialistClientInheritsMainBaseURLForSameProviderAfterNormalize(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "lmstudio"
	cfg.Model = "main-local"
	cfg.BaseURL = "http://127.0.0.1:8765/v1/"
	cfg.Specialists.Profiles = []SpecialistSubagentProfile{{
		Name:     "local-reviewer",
		Provider: "lmstudio",
		Model:    "worker-local",
	}}
	normalizeConfigPaths(&cfg)
	if got := cfg.Specialists.Profiles[0].BaseURL; got != "" {
		t.Fatalf("expected empty specialist base URL to remain inheritable, got %q", got)
	}

	agent := &Agent{Config: cfg}
	client, _ := agent.specialistClient(cfg.Specialists.Profiles[0])
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected specialist client to expose route metadata, got %T", client)
	}
	meta := metaProvider.ModelRouteMetadata()
	want := normalizeProviderBaseURL("lmstudio", cfg.BaseURL)
	if meta.BaseURL != want {
		t.Fatalf("expected specialist to inherit main base URL %q, got %q", want, meta.BaseURL)
	}
}

func TestSpecialistClientUsesDifferentProviderDefaultBaseURL(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "ollama"
	cfg.Model = "main-local"
	cfg.BaseURL = "http://127.0.0.1:11435"
	agent := &Agent{Config: cfg}

	client, _ := agent.specialistClient(SpecialistSubagentProfile{
		Name:     "lmstudio-worker",
		Provider: "lmstudio",
		Model:    "worker-local",
	})
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected specialist client to expose route metadata, got %T", client)
	}
	meta := metaProvider.ModelRouteMetadata()
	want := normalizeProviderBaseURL("lmstudio", "")
	if meta.BaseURL != want {
		t.Fatalf("expected different-provider specialist to use provider default base URL %q, got %q", want, meta.BaseURL)
	}
}

func TestSpecialistClientDoesNotLeakMainAPIKeyToDifferentProvider(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "main-cloud"
	cfg.APIKey = "main-key"
	cfg.ProviderKeys = map[string]string{"openrouter": "router-key"}
	agent := &Agent{Config: cfg}

	client, _ := agent.specialistClient(SpecialistSubagentProfile{
		Name:     "router-reviewer",
		Provider: "openrouter",
		Model:    "router-model",
	})
	openAIClient, ok := client.(*OpenAIClient)
	if !ok {
		t.Fatalf("expected openrouter specialist to use OpenAI-compatible client, got %T", client)
	}
	if openAIClient.apiKey != "router-key" {
		t.Fatalf("expected specialist to use provider key, got %q", openAIClient.apiKey)
	}
}

func TestSpecialistBatchRouteLimiterDoesNotForceCloudDuplicateRouteToSerial(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-test"
	cfg.APIKey = "test-key"
	agent := &Agent{Config: cfg}
	client := NewOpenAIClient(cfg.BaseURL, cfg.APIKey)
	route := modelRouteForRequest(cfg, client, ChatRequest{Model: cfg.Model})

	limiter := agent.specialistBatchRouteLimiter([]ModelRoute{route, route, route})
	if _, ok := limiter.semaphores[route.Key]; ok {
		t.Fatalf("expected duplicate cloud specialist route to avoid a semaphore below requested concurrency")
	}
}

func TestSpecialistBatchRouteLimiterHonorsExplicitCloudProviderLimit(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-test"
	cfg.APIKey = "test-key"
	cfg.ModelRoutes.ProviderLimits = map[string]int{"openai": 2}
	agent := &Agent{Config: cfg}
	client := NewOpenAIClient(cfg.BaseURL, cfg.APIKey)
	route := modelRouteForRequest(cfg, client, ChatRequest{Model: cfg.Model})

	limiter := agent.specialistBatchRouteLimiter([]ModelRoute{route, route, route})
	sem := limiter.semaphores[route.Key]
	if sem == nil || cap(sem) != 2 {
		t.Fatalf("expected explicit cloud specialist provider limit to create limit 2 semaphore, got %#v", sem)
	}
}

func TestFormatSpecialistCatalogAlignsDescriptionsAndSeparatesHints(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())

	text := formatSpecialistCatalog(cfg)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("expected formatted specialist catalog output")
	}

	lines := strings.Split(text, "\n")
	generalIndex := strings.Index(text, "General-purpose specialists:")
	domainIndex := strings.Index(text, "Domain-specific specialists:")
	if generalIndex < 0 || domainIndex < 0 {
		t.Fatalf("expected grouped specialist headings, got %q", text)
	}
	if generalIndex >= domainIndex {
		t.Fatalf("expected general-purpose specialists to appear before domain-specific ones, got %q", text)
	}

	plannerIndex := -1
	implementationIndex := -1
	attackIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "planner") && !strings.Contains(line, "implementation-owner") {
			plannerIndex = i
		}
		if strings.Contains(line, "attack-surface-reviewer") {
			attackIndex = i
		}
		if strings.Contains(line, "implementation-owner") {
			implementationIndex = i
		}
	}
	if attackIndex < 0 || implementationIndex < 0 || plannerIndex < 0 {
		t.Fatalf("expected known specialists in output, got %q", text)
	}

	implementationLine := lines[implementationIndex]
	plannerLine := lines[plannerIndex]
	if strings.Contains(plannerLine, "[kinds=") || strings.Contains(implementationLine, "[kinds=") {
		t.Fatalf("expected hints to move to the next line, got %q", text)
	}

	plannerDescCol := strings.Index(plannerLine, "General-purpose planning specialist")
	implementationDescCol := strings.Index(implementationLine, "Owns ordinary product code edits")
	if plannerDescCol <= 0 || implementationDescCol <= 0 {
		t.Fatalf("expected descriptions to be present in formatted output, got %q", text)
	}
	if plannerDescCol != implementationDescCol {
		t.Fatalf("expected description columns to align, got planner=%d implementation=%d in %q", plannerDescCol, implementationDescCol, text)
	}

	if implementationIndex >= attackIndex {
		t.Fatalf("expected general-purpose entries to render before domain-specific ones, got %q", text)
	}

	if attackIndex+1 >= len(lines) {
		t.Fatalf("expected hint line after attack-surface-reviewer entry")
	}
	hintLine := lines[attackIndex+1]
	if !strings.HasPrefix(strings.TrimSpace(hintLine), "[kinds=inspection,summary,verification") {
		t.Fatalf("expected hint line after attack entry, got %q", hintLine)
	}
	if len(hintLine) == len(strings.TrimLeft(hintLine, " ")) {
		t.Fatalf("expected hint line to be indented, got %q", hintLine)
	}
}

func TestFormatSpecialistCatalogWithUIHighlightsNames(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	ui := UI{color: true}

	text := formatSpecialistCatalogWithUI(ui, cfg)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("expected formatted specialist catalog output")
	}
	if !strings.Contains(text, "\x1b[") {
		t.Fatalf("expected ANSI styling in specialist catalog output, got %q", text)
	}

	clean := ansiPattern.ReplaceAllString(text, "")
	lines := strings.Split(clean, "\n")
	implementationIndex := -1
	reviewerIndex := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "implementation-owner") {
			implementationIndex = i
		}
		if strings.HasPrefix(trimmed, "reviewer ") || trimmed == "reviewer" {
			reviewerIndex = i
		}
	}
	if implementationIndex < 0 || reviewerIndex < 0 {
		t.Fatalf("expected general-purpose specialists in output, got %q", clean)
	}

	implementationLine := lines[implementationIndex]
	reviewerLine := lines[reviewerIndex]
	implementationDescCol := strings.Index(implementationLine, "Owns ordinary product code edits")
	reviewerDescCol := strings.Index(reviewerLine, "General-purpose review specialist")
	if implementationDescCol <= 0 || reviewerDescCol <= 0 {
		t.Fatalf("expected descriptions to be present after stripping ANSI, got %q", clean)
	}
	if implementationDescCol != reviewerDescCol {
		t.Fatalf("expected aligned description columns after stripping ANSI, got implementation=%d reviewer=%d in %q", implementationDescCol, reviewerDescCol, clean)
	}
}
