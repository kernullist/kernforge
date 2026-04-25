package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveUserConfigDoesNotLetStaleSessionOverwriteActiveModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := t.TempDir()
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openrouter"
	cfg.Model = "deepseek/deepseek-v4-pro"
	cfg.BaseURL = normalizeOpenRouterBaseURL("")
	cfg.ProviderKeys = map[string]string{"openrouter": "test-key"}
	cfg.Profiles = []Profile{
		{
			Name:     "openrouter / deepseek/deepseek-v4-pro",
			Provider: "openrouter",
			Model:    "deepseek/deepseek-v4-pro",
			BaseURL:  normalizeOpenRouterBaseURL(""),
		},
		{
			Name:     "openrouter / z-ai/glm-5.1",
			Provider: "openrouter",
			Model:    "z-ai/glm-5.1",
			BaseURL:  normalizeOpenRouterBaseURL(""),
		},
	}
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig initial: %v", err)
	}

	staleSession := NewSession(workspace, "openrouter", "z-ai/glm-5.1", normalizeOpenRouterBaseURL(""), "default")
	rt := &runtimeState{
		cfg:     cfg,
		session: staleSession,
	}
	rt.cfg.MaxToolIterations = 23
	if err := rt.saveUserConfig(); err != nil {
		t.Fatalf("saveUserConfig: %v", err)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Provider != "openrouter" || loaded.Model != "deepseek/deepseek-v4-pro" {
		t.Fatalf("expected active model to stay deepseek, got %s / %s", loaded.Provider, loaded.Model)
	}
	if loaded.MaxToolIterations != 23 {
		t.Fatalf("expected non-model setting to persist, got %d", loaded.MaxToolIterations)
	}
	if len(loaded.Profiles) != 2 {
		t.Fatalf("expected saved profiles to remain, got %#v", loaded.Profiles)
	}
}

func TestActivateProviderPersistsNewActiveModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := t.TempDir()
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openrouter"
	cfg.Model = "z-ai/glm-5.1"
	cfg.BaseURL = normalizeOpenRouterBaseURL("")
	cfg.ProviderKeys = map[string]string{"openrouter": "test-key"}
	cfg.SessionDir = filepath.Join(home, ".kernforge", "sessions")
	session := NewSession(workspace, cfg.Provider, cfg.Model, cfg.BaseURL, cfg.PermissionMode)
	store := NewSessionStore(cfg.SessionDir)

	rt := &runtimeState{
		cfg:     cfg,
		session: session,
		store:   store,
	}
	if err := os.MkdirAll(cfg.SessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll session dir: %v", err)
	}
	if err := rt.activateProvider("openrouter", "deepseek/deepseek-v4-pro", normalizeOpenRouterBaseURL("")); err != nil {
		t.Fatalf("activateProvider: %v", err)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Provider != "openrouter" || loaded.Model != "deepseek/deepseek-v4-pro" {
		t.Fatalf("expected activated model to persist, got %s / %s", loaded.Provider, loaded.Model)
	}
	if loaded.ActiveProfileKey == "" {
		t.Fatalf("expected activated model to persist active profile key")
	}
	if len(loaded.Profiles) == 0 || loaded.Profiles[0].Model != "deepseek/deepseek-v4-pro" {
		t.Fatalf("expected activated model profile first, got %#v", loaded.Profiles)
	}
}
