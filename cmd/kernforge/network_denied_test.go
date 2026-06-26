package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Plan mode denies network/MCP access with the ErrNetworkDenied sentinel so the
// agent can surface a clear, actionable block instead of a silent failure.
func TestEnsureNetworkBlockedInPlanModeWrapsSentinel(t *testing.T) {
	ws := Workspace{Perms: NewPermissionManager(ModePlan, func(string) (bool, error) { return false, nil })}
	err := ws.EnsureNetworkWithContext(context.Background(), "mcp:knlivedbg http://192.168.44.129:8765/mcp")
	if err == nil || !errors.Is(err, ErrNetworkDenied) {
		t.Fatalf("plan-mode network must be denied with ErrNetworkDenied, got %v", err)
	}
}

// Full (bypass) mode allows network/MCP access.
func TestEnsureNetworkAllowedInFullMode(t *testing.T) {
	ws := Workspace{Perms: NewPermissionManager(ModeBypass, nil)}
	if err := ws.EnsureNetworkWithContext(context.Background(), "mcp:x"); err != nil {
		t.Fatalf("full mode must allow network, got %v", err)
	}
}

// The plan-mode reply must be actionable: name the blocked tool, the read-only
// restriction, and the way out (edit/full).
func TestFormatNetworkDeniedReplyPlanModeIsActionable(t *testing.T) {
	r := formatNetworkDeniedReply(Config{}, "mcp__knlivedbg__ti_query", true)
	low := strings.ToLower(r)
	// Either locale: name the read-only plan mode and the edit/full way out.
	hasPlan := strings.Contains(low, "plan mode") || strings.Contains(r, "plan 모드")
	if !hasPlan || !strings.Contains(low, "edit") || !strings.Contains(low, "full") {
		t.Fatalf("plan-mode reply must name plan mode and the edit/full switch, got %q", r)
	}
	if !strings.Contains(r, "ti_query") {
		t.Fatalf("reply should name the blocked tool, got %q", r)
	}
}
