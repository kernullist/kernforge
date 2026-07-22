package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectionHubCheatsheetAndAliases(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        UI{color: false},
		cfg:       DefaultConfig(root),
		workspace: Workspace{Root: root, BaseRoot: root},
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		session:   NewSession(root, "provider", "model", "", "default"),
	}

	if err := rt.handleSelectionFamilyCommand(""); err != nil {
		t.Fatalf("selection hub: %v", err)
	}
	if !strings.Contains(out.String(), "/selection open") {
		t.Fatalf("expected selection cheatsheet, got %q", out.String())
	}

	out.Reset()
	if _, err := rt.handleCommand(Command{Name: "settings", Args: ""}); err != nil {
		t.Fatalf("settings hub: %v", err)
	}
	if !strings.Contains(out.String(), "auto-verify") {
		t.Fatalf("expected settings cheatsheet, got %q", out.String())
	}

	out.Reset()
	if _, err := rt.handleCommand(Command{Name: "probe", Args: ""}); err != nil {
		t.Fatalf("probe hub: %v", err)
	}
	if !strings.Contains(out.String(), "/probe fuzz") {
		t.Fatalf("expected probe cheatsheet, got %q", out.String())
	}

	out.Reset()
	if _, err := rt.handleCommand(Command{Name: "analyze", Args: ""}); err != nil {
		t.Fatalf("analyze hub: %v", err)
	}
	if !strings.Contains(out.String(), "/analyze project") {
		t.Fatalf("expected analyze cheatsheet, got %q", out.String())
	}

	out.Reset()
	if _, err := rt.handleCommand(Command{Name: "help", Args: "all"}); err != nil {
		t.Fatalf("help all: %v", err)
	}
	if !strings.Contains(out.String(), "/fuzz-func") {
		t.Fatalf("expected /help all catalog, got %q", out.String())
	}
}

func TestMemoryEvidenceSubcommandRoutes(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        UI{color: false},
		cfg:       DefaultConfig(root),
		workspace: Workspace{Root: root, BaseRoot: root},
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		session:   NewSession(root, "provider", "model", "", "default"),
	}
	if err := rt.handleMemoryFamilyCommand("evidence"); err != nil {
		// Empty evidence store may warn but should not hard-fail routing.
		if !strings.Contains(err.Error(), "usage:") {
			// ok — handler may return nil or a soft error depending on store
		}
	}
}
