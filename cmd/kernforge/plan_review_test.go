package main

import "testing"

func TestCreateReviewerClientInheritsMainBaseURLForSameProvider(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "lmstudio"
	cfg.Model = "main-local"
	cfg.BaseURL = "http://127.0.0.1:8765/v1/"

	client, err := createReviewerClient(&ReviewModelConfig{
		Provider: "lmstudio",
		Model:    "review-local",
	}, cfg)
	if err != nil {
		t.Fatalf("createReviewerClient: %v", err)
	}
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected reviewer client to expose route metadata, got %T", client)
	}
	meta := metaProvider.ModelRouteMetadata()
	want := normalizeProviderBaseURL("lmstudio", cfg.BaseURL)
	if meta.BaseURL != want {
		t.Fatalf("expected reviewer to inherit main base URL %q, got %q", want, meta.BaseURL)
	}
}

func TestNormalizeConfigPathsPreservesReviewRoleEmptyBaseURLForInheritance(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "lmstudio"
	cfg.Model = "main-local"
	cfg.BaseURL = "http://127.0.0.1:8765/v1/"
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider: "lmstudio",
			Model:    "review-local",
		},
	}

	normalizeConfigPaths(&cfg)
	roleCfg := cfg.Review.RoleModels["primary_reviewer"]
	if roleCfg.BaseURL != "" {
		t.Fatalf("expected empty review role base URL to remain inheritable, got %q", roleCfg.BaseURL)
	}

	client, err := createReviewerClient(&roleCfg, cfg)
	if err != nil {
		t.Fatalf("createReviewerClient: %v", err)
	}
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected reviewer client to expose route metadata, got %T", client)
	}
	meta := metaProvider.ModelRouteMetadata()
	want := normalizeProviderBaseURL("lmstudio", cfg.BaseURL)
	if meta.BaseURL != want {
		t.Fatalf("expected normalized reviewer to inherit main base URL %q, got %q", want, meta.BaseURL)
	}
}

func TestCreateReviewerClientUsesReviewerBaseURLOverride(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "lmstudio"
	cfg.Model = "main-local"
	cfg.BaseURL = "http://127.0.0.1:8765/v1/"
	override := "http://127.0.0.1:8766/v1/"

	client, err := createReviewerClient(&ReviewModelConfig{
		Provider: "lmstudio",
		Model:    "review-local",
		BaseURL:  override,
	}, cfg)
	if err != nil {
		t.Fatalf("createReviewerClient: %v", err)
	}
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected reviewer client to expose route metadata, got %T", client)
	}
	meta := metaProvider.ModelRouteMetadata()
	want := normalizeProviderBaseURL("lmstudio", override)
	if meta.BaseURL != want {
		t.Fatalf("expected reviewer base URL override %q, got %q", want, meta.BaseURL)
	}
}

func TestCreateReviewerClientUsesReviewerReasoningEffort(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "low"

	client, err := createReviewerClient(&ReviewModelConfig{
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		ReasoningEffort: "high",
	}, cfg)
	if err != nil {
		t.Fatalf("createReviewerClient: %v", err)
	}
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected reviewer client to expose route metadata, got %T", client)
	}
	if got := metaProvider.ModelRouteMetadata().ReasoningEffort; got != "high" {
		t.Fatalf("expected reviewer reasoning effort high, got %q", got)
	}
}

func TestModelRequestPolicyFromAgentUsesAgentScheduler(t *testing.T) {
	scheduler := NewModelRouteScheduler()
	agent := &Agent{
		Config:      DefaultConfig(t.TempDir()),
		ModelRoutes: scheduler,
	}

	policy := modelRequestPolicyFromAgent(agent)
	if policy.ModelRoutes != scheduler {
		t.Fatalf("expected plan-review policy to use the agent scheduler")
	}
}
