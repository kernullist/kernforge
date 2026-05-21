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

func TestDefaultSpecialistProfilesAvoidReviewerNames(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	names := map[string]bool{}
	for _, profile := range configuredSpecialistProfiles(cfg) {
		names[normalizeSpecialistProfileName(profile.Name)] = true
	}
	for _, want := range []string{"attack-surface-analyst", "unreal-integrity-analyst", "memory-inspection-analyst"} {
		if !names[want] {
			t.Fatalf("expected renamed domain specialist %q in catalog, got %#v", want, names)
		}
	}
	for _, old := range []string{"reviewer", "attack-surface-reviewer", "unreal-integrity-reviewer", "memory-inspection-reviewer"} {
		if names[old] {
			t.Fatalf("default specialist catalog should not expose %q: %#v", old, names)
		}
	}
}

func TestSpecialistProfileCanonicalizesDomainReviewerAliases(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Specialists.Profiles = []SpecialistSubagentProfile{{
		Name:     "memory-inspection-reviewer",
		Provider: "openai",
		Model:    "gpt-memory",
	}}

	profile, ok := configuredSpecialistProfileByName(cfg, "memory-inspection-analyst")
	if !ok {
		t.Fatalf("expected legacy memory-inspection-reviewer override to map to memory-inspection-analyst")
	}
	if profile.Name != "memory-inspection-analyst" || profile.Model != "gpt-memory" {
		t.Fatalf("unexpected canonicalized profile: %#v", profile)
	}
	if _, ok := configuredSpecialistProfileByName(cfg, "memory-inspection-reviewer"); !ok {
		t.Fatalf("expected old domain specialist name to resolve as an alias")
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

func TestSpecialistClientSkipsImplicitMainModelRoute(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-chat"
	agent := &Agent{Config: cfg}

	client, model := agent.specialistClient(SpecialistSubagentProfile{
		Name: "kernel-investigator",
	})
	if client != nil || model != "" {
		t.Fatalf("expected unconfigured specialist to skip implicit main route, got %T %q", client, model)
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

func TestSpecialistClientUsesSpecialistReasoningEffort(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "low"
	agent := &Agent{Config: cfg}

	client, _ := agent.specialistClient(SpecialistSubagentProfile{
		Name:            "codex-specialist",
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		ReasoningEffort: "high",
	})
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected specialist client to expose route metadata, got %T", client)
	}
	if got := metaProvider.ModelRouteMetadata().ReasoningEffort; got != "high" {
		t.Fatalf("expected specialist reasoning effort high, got %q", got)
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

func TestSpecialistMicroWorkerSystemPromptCompactsProfileText(t *testing.T) {
	longDescription := "description-head " + strings.Repeat("description-body ", 80) + "description-tail"
	longPrompt := "prompt-head " + strings.Repeat("prompt-body ", 180) + "prompt-tail"

	systemPrompt := buildSpecialistMicroWorkerSystemPrompt(SpecialistSubagentProfile{
		Name:        strings.Repeat("role-name-", 30),
		Description: longDescription,
		Prompt:      longPrompt,
	})

	if !strings.Contains(systemPrompt, "Specialist role: role-name-") {
		t.Fatalf("expected compact role line in system prompt, got:\n%s", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "Specialist description:\ndescription-head") {
		t.Fatalf("expected compact description section in system prompt, got:\n%s", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "Specialist guidance:\nprompt-head") {
		t.Fatalf("expected compact guidance section in system prompt, got:\n%s", systemPrompt)
	}
	if strings.Contains(systemPrompt, "description-tail") {
		t.Fatalf("expected long description tail to be omitted, got:\n%s", systemPrompt)
	}
	if strings.Contains(systemPrompt, "prompt-tail") {
		t.Fatalf("expected long prompt tail to be omitted, got:\n%s", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "...") {
		t.Fatalf("expected compacted prompt to include ellipsis, got:\n%s", systemPrompt)
	}
	if len(systemPrompt) > specialistMicroWorkerDescriptionMaxBytes+specialistMicroWorkerPromptMaxBytes+600 {
		t.Fatalf("expected system prompt to stay bounded, got %d bytes", len(systemPrompt))
	}
}

func TestSpecialistEditableWorkerSystemPromptCompactsProfileText(t *testing.T) {
	longDescription := "description-head " + strings.Repeat("description-body ", 80) + "description-tail"
	longPrompt := "prompt-head " + strings.Repeat("prompt-body ", 180) + "prompt-tail"

	systemPrompt := buildSpecialistEditableWorkerSystemPrompt(SpecialistSubagentProfile{
		Name:        strings.Repeat("editable-role-", 30),
		Description: longDescription,
		Prompt:      longPrompt,
	})

	if !strings.Contains(systemPrompt, "Specialist role: editable-role-") {
		t.Fatalf("expected compact role line in editable worker prompt, got:\n%s", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "Specialist description:\ndescription-head") {
		t.Fatalf("expected compact description section in editable worker prompt, got:\n%s", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "Specialist guidance:\nprompt-head") {
		t.Fatalf("expected compact guidance section in editable worker prompt, got:\n%s", systemPrompt)
	}
	if strings.Contains(systemPrompt, "description-tail") {
		t.Fatalf("expected long description tail to be omitted, got:\n%s", systemPrompt)
	}
	if strings.Contains(systemPrompt, "prompt-tail") {
		t.Fatalf("expected long prompt tail to be omitted, got:\n%s", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "...") {
		t.Fatalf("expected compacted editable worker prompt to include ellipsis, got:\n%s", systemPrompt)
	}
	if len(systemPrompt) > specialistPromptDescriptionMaxBytes+specialistPromptGuidanceMaxBytes+700 {
		t.Fatalf("expected editable worker system prompt to stay bounded, got %d bytes", len(systemPrompt))
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
		if strings.Contains(line, "attack-surface-analyst") {
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
		t.Fatalf("expected hint line after attack-surface-analyst entry")
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
	plannerIndex := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "implementation-owner") {
			implementationIndex = i
		}
		if strings.HasPrefix(trimmed, "planner ") || trimmed == "planner" {
			plannerIndex = i
		}
	}
	if implementationIndex < 0 || plannerIndex < 0 {
		t.Fatalf("expected general-purpose specialists in output, got %q", clean)
	}

	implementationLine := lines[implementationIndex]
	plannerLine := lines[plannerIndex]
	implementationDescCol := strings.Index(implementationLine, "Owns ordinary product code edits")
	plannerDescCol := strings.Index(plannerLine, "General-purpose planning specialist")
	if implementationDescCol <= 0 || plannerDescCol <= 0 {
		t.Fatalf("expected descriptions to be present after stripping ANSI, got %q", clean)
	}
	if implementationDescCol != plannerDescCol {
		t.Fatalf("expected aligned description columns after stripping ANSI, got implementation=%d planner=%d in %q", implementationDescCol, plannerDescCol, clean)
	}
}
