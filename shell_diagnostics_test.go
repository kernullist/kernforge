package main

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRunShellCompatibilityGuidanceRejectsUnixFindSyntaxOnWindowsPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell compatibility guidance only applies on Windows")
	}

	guidance := runShellCompatibilityGuidance("powershell", `cd "C:/git/docs" ; find anti-cheat-research -type d -exec chmod 700 {} \;`)
	if !strings.Contains(guidance, "Unix shell syntax") {
		t.Fatalf("expected Unix syntax guidance, got %q", guidance)
	}
}

func TestRunShellCompatibilityGuidanceRejectsCmdBatchLoopOnWindowsPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell compatibility guidance only applies on Windows")
	}

	guidance := runShellCompatibilityGuidance("powershell", `cd "C:/git/docs" ; for /d %d in (anti-cheat-research\*) do @echo %d`)
	if !strings.Contains(guidance, "cmd.exe batch syntax") {
		t.Fatalf("expected cmd batch guidance, got %q", guidance)
	}
}

func TestRunShellCompatibilityGuidanceRejectsUnixLsFallbackSyntaxOnWindowsPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell compatibility guidance only applies on Windows")
	}

	guidance := runShellCompatibilityGuidance("powershell", `ls -la . 2>/dev/null || echo "ls failed"`)
	if !strings.Contains(guidance, "Unix-style shell patterns") {
		t.Fatalf("expected Unix-style fallback guidance, got %q", guidance)
	}
	if !strings.Contains(guidance, "Get-ChildItem -Force") {
		t.Fatalf("expected PowerShell ls rewrite guidance, got %q", guidance)
	}
}

func TestRunShellFailureGuidanceFlagsDubiousOwnership(t *testing.T) {
	guidance := runShellFailureGuidance(
		"powershell",
		"git init",
		"fatal: detected dubious ownership in repository at 'C:/git/docs/anti-cheat-research'\nTo add an exception for this directory, call:\n\n\tgit config --global --add safe.directory C:/git/docs/anti-cheat-research",
		errors.New("command failed [git init]: exit status 128"),
	)
	if !strings.Contains(guidance, "different user") {
		t.Fatalf("expected dubious ownership guidance, got %q", guidance)
	}
}

func TestRunShellFailureGuidanceFlagsACLPermissionLoop(t *testing.T) {
	guidance := runShellFailureGuidance(
		"powershell",
		"Set-Acl demo $acl",
		"Set-Acl : Attempted to perform an unauthorized operation.",
		errors.New("command failed [Set-Acl demo $acl]: exit status 1"),
	)
	if !strings.Contains(guidance, "does not have permission") {
		t.Fatalf("expected ACL permission guidance, got %q", guidance)
	}
}

func TestRunShellFailureGuidanceFlagsNestedPowerShellQuoting(t *testing.T) {
	command := `powershell -Command "Import-Module Microsoft.PowerShell.Security; Get-ChildItem | ForEach-Object { $acl = Get-Acl $_.FullName; $acl.SetAccessRuleProtection($true, $false) }"`
	output := "At line:1 char:178\n+ ... Object {  = Get-Acl .FullName; .SetAccessRuleProtection(True, False); ...\n+                                                                 ~\n+ Missing argument in parameter list."

	guidance := runShellFailureGuidance("powershell", command, output, errors.New("command failed"))
	if !strings.Contains(guidance, "nested PowerShell -Command string") {
		t.Fatalf("expected nested PowerShell quoting guidance, got %q", guidance)
	}
}

func TestSystemPromptIncludesWindowsShellCompatibilityGuidance(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "do not use Unix shell syntax like find ... -type, chmod, chown") {
		t.Fatalf("expected Windows shell compatibility guidance, got %q", prompt)
	}
	if !strings.Contains(prompt, "Do not use run_shell for repo bootstrap, ACL changes, or git init") {
		t.Fatalf("expected ordinary-file edit guidance, got %q", prompt)
	}
}
