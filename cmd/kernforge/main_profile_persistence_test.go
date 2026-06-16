package main

import (
	"io"
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

func TestSaveUserConfigDoesNotLetSessionPermissionOverwriteActiveConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := t.TempDir()
	cfg := DefaultConfig(workspace)
	cfg.PermissionMode = "default"
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig initial: %v", err)
	}

	session := NewSession(workspace, cfg.Provider, cfg.Model, cfg.BaseURL, "bypassPermissions")
	rt := &runtimeState{
		cfg:     cfg,
		session: session,
	}
	rt.cfg.MaxToolIterations = 37
	if err := rt.saveUserConfig(); err != nil {
		t.Fatalf("saveUserConfig: %v", err)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.PermissionMode != "plan" {
		t.Fatalf("expected global permission mode to migrate default -> plan (read-only), got %q", loaded.PermissionMode)
	}
	if loaded.MaxToolIterations != 37 {
		t.Fatalf("expected unrelated setting to persist, got %d", loaded.MaxToolIterations)
	}

	rt.session.PermissionMode = "acceptEdits"
	rt.cfg.MaxToolIterations = 41
	if err := rt.saveUserConfigReplacingReviewRoleModels(); err != nil {
		t.Fatalf("saveUserConfigReplacingReviewRoleModels: %v", err)
	}

	loaded, err = LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig after role replacement save: %v", err)
	}
	if loaded.PermissionMode != "plan" {
		t.Fatalf("expected role replacement save to keep global permission mode plan (migrated from default), got %q", loaded.PermissionMode)
	}
	if loaded.MaxToolIterations != 41 {
		t.Fatalf("expected role replacement save to persist unrelated setting, got %d", loaded.MaxToolIterations)
	}
}

func TestReloadRuntimeConfigUsesLivePermissionSnapshotOverStaleSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := t.TempDir()
	cfg := DefaultConfig(workspace)
	cfg.PermissionMode = " " + string(ModeDefault) + " "
	cfg.MCPServers = nil
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig initial: %v", err)
	}

	session := NewSession(workspace, cfg.Provider, cfg.Model, cfg.BaseURL, string(ModeBypass))
	rt := newReloadPermissionTestRuntime(workspace, cfg, session, ModeDefault)

	loaded := cfg
	loaded.PermissionMode = string(ModeAcceptEdits)
	if err := SaveUserConfig(loaded); err != nil {
		t.Fatalf("SaveUserConfig reloaded: %v", err)
	}

	if err := rt.reloadRuntimeConfig(); err != nil {
		t.Fatalf("reloadRuntimeConfig: %v", err)
	}
	if rt.cfg.PermissionMode != "edit" {
		t.Fatalf("expected loaded config permission %q, got %q", "edit", rt.cfg.PermissionMode)
	}
	if rt.session.PermissionMode != string(ModeAcceptEdits) {
		t.Fatalf("expected stale session permission to adopt loaded config, got %q", rt.session.PermissionMode)
	}
	if rt.perms.Mode() != ModeAcceptEdits {
		t.Fatalf("expected live permission manager to adopt loaded config, got %q", rt.perms.Mode())
	}
}

func TestPermissionsCommandRejectsInvalidModeWithoutChangingState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := t.TempDir()
	cfg := DefaultConfig(workspace)
	cfg.PermissionMode = string(ModePlan)
	cfg.MCPServers = nil
	session := NewSession(workspace, cfg.Provider, cfg.Model, cfg.BaseURL, string(ModePlan))
	rt := newReloadPermissionTestRuntime(workspace, cfg, session, ModePlan)

	_, err := rt.handleCommand(Command{Name: "permissions", Args: "full-access"})
	if err == nil {
		t.Fatalf("expected invalid permission mode to fail")
	}
	if rt.perms.Mode() != ModePlan {
		t.Fatalf("permission manager changed after invalid mode, got %q", rt.perms.Mode())
	}
	if rt.session.PermissionMode != string(ModePlan) {
		t.Fatalf("session permission changed after invalid mode, got %q", rt.session.PermissionMode)
	}
	if rt.cfg.PermissionMode != string(ModePlan) {
		t.Fatalf("config permission changed after invalid mode, got %q", rt.cfg.PermissionMode)
	}
}

func TestReloadRuntimeConfigPreservesLivePermissionOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := t.TempDir()
	cfg := DefaultConfig(workspace)
	cfg.PermissionMode = string(ModeDefault)
	cfg.MCPServers = nil
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig initial: %v", err)
	}

	session := NewSession(workspace, cfg.Provider, cfg.Model, cfg.BaseURL, string(ModeDefault))
	rt := newReloadPermissionTestRuntime(workspace, cfg, session, ModeBypass)

	loaded := cfg
	loaded.PermissionMode = string(ModeAcceptEdits)
	if err := SaveUserConfig(loaded); err != nil {
		t.Fatalf("SaveUserConfig reloaded: %v", err)
	}

	if err := rt.reloadRuntimeConfig(); err != nil {
		t.Fatalf("reloadRuntimeConfig: %v", err)
	}
	if rt.cfg.PermissionMode != "edit" {
		t.Fatalf("expected loaded config permission %q, got %q", "edit", rt.cfg.PermissionMode)
	}
	if rt.session.PermissionMode != string(ModeBypass) {
		t.Fatalf("expected live permission override to be persisted to session, got %q", rt.session.PermissionMode)
	}
	if rt.perms.Mode() != ModeBypass {
		t.Fatalf("expected live permission override to survive reload, got %q", rt.perms.Mode())
	}
}

func newReloadPermissionTestRuntime(workspace string, cfg Config, session *Session, mode Mode) *runtimeState {
	store := NewSessionStore(cfg.SessionDir)
	perms := NewPermissionManager(mode, nil)
	return &runtimeState{
		cfg:     cfg,
		writer:  io.Discard,
		ui:      NewUI(),
		store:   store,
		session: session,
		perms:   perms,
		workspace: Workspace{
			BaseRoot: workspace,
			Root:     workspace,
			Perms:    perms,
		},
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
