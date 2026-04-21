package main

import (
	"regexp"
	"runtime"
	"strings"
)

var (
	windowsCmdBatchLoopPattern = regexp.MustCompile(`(?i)(^|[;&(])\s*for\s+/[a-z]+\s+%`)
	unixFindSyntaxPattern      = regexp.MustCompile(`(?i)\bfind\b[^|\r\n]*\s-(type|name|path|exec|maxdepth|mindepth)\b`)
	unixLsFlagsPattern         = regexp.MustCompile(`(?i)(^|[;&|()])\s*ls(?:\.exe)?\s+-[alhrts]{1,6}\b`)
)

func shellUsesWindowsPowerShell(shell string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	base := strings.ToLower(strings.TrimSpace(shell))
	if base == "" {
		return true
	}
	return strings.Contains(base, "powershell") || strings.Contains(base, "pwsh")
}

func shellUsesLegacyWindowsPowerShell(shell string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	base := strings.ToLower(strings.TrimSpace(shell))
	if base == "" {
		return true
	}
	return strings.Contains(base, "powershell") && !strings.Contains(base, "pwsh")
}

func runShellCompatibilityGuidance(shell, command string) string {
	if !shellUsesWindowsPowerShell(shell) {
		return ""
	}

	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return ""
	}

	tokens := splitShellCommandWords(lower)
	if shellCommandHasPrefixTokens(tokens,
		[]string{"cmd"},
		[]string{"cmd.exe"},
		[]string{"bash"},
		[]string{"sh"},
		[]string{"wsl"},
		[]string{"powershell"},
		[]string{"powershell.exe"},
		[]string{"pwsh"},
		[]string{"pwsh.exe"},
	) {
		return ""
	}

	switch {
	case unixFindSyntaxPattern.MatchString(lower) || strings.Contains(lower, " chmod ") || strings.HasPrefix(lower, "chmod ") || strings.Contains(lower, " chown ") || strings.HasPrefix(lower, "chown "):
		return "Shell diagnosis: this command uses Unix shell syntax, but run_shell is using Windows PowerShell. Do not retry the same command unchanged. Rewrite it with PowerShell cmdlets such as Get-ChildItem and ForEach-Object, or explicitly invoke bash -lc only if Bash is truly the intended interpreter."
	case unixLsFlagsPattern.MatchString(lower) || strings.Contains(lower, "/dev/null") ||
		(shellUsesLegacyWindowsPowerShell(shell) && (strings.Contains(command, "||") || strings.Contains(command, "&&"))):
		return "Shell diagnosis: this command uses Unix-style shell patterns that do not match Windows PowerShell. Do not retry the same command unchanged. Rewrite ls -la with Get-ChildItem -Force, prefer -ErrorAction SilentlyContinue or 2>$null instead of /dev/null, and use PowerShell conditionals rather than || or && in Windows PowerShell."
	case windowsCmdBatchLoopPattern.MatchString(lower):
		return "Shell diagnosis: this command uses cmd.exe batch syntax, but run_shell is using PowerShell. Do not retry the same command unchanged. Rewrite it with PowerShell cmdlets, or wrap the batch snippet in cmd /c if cmd.exe is actually required."
	default:
		return ""
	}
}

func runShellFailureGuidance(shell, command, output string, err error) string {
	parts := []string{command}
	if strings.TrimSpace(output) != "" {
		parts = append(parts, output)
	}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		parts = append(parts, err.Error())
	}
	lower := strings.ToLower(strings.Join(parts, "\n"))

	switch {
	case looksLikeNestedPowerShellCommandQuoting(command, lower):
		return "Shell diagnosis: a nested PowerShell -Command string likely let the outer shell expand $variables before the inner command ran. Do not retry the same inline command unchanged. Use -File, a script file, or single-quoted outer command text when nested PowerShell is truly necessary."
	case strings.Contains(lower, "detected dubious ownership") || strings.Contains(lower, "safe.directory") ||
		(strings.Contains(lower, "could not lock config file") && strings.Contains(lower, "permission denied")):
		return "Shell diagnosis: this directory is owned by a different user or protected by restrictive ACLs. Do not keep retrying git init/add/commit or ACL changes in the same path. Work inside the active writable workspace, or ask the user before changing ownership, ACLs, or Git safe.directory settings."
	case strings.Contains(lower, "attempted to perform an unauthorized operation") ||
		strings.Contains(lower, "unauthorizedaccessexception") ||
		strings.Contains(lower, "access is denied"):
		return "Shell diagnosis: the shell does not have permission to change ACLs or protected files in this path. Do not repeat the same Get-Acl/Set-Acl/icacls/chmod sequence unchanged. Skip ACL hardening unless the user explicitly asked for it and the session has the required privileges."
	case strings.Contains(lower, "get-acl") && strings.Contains(lower, "could not autoload matching module"),
		strings.Contains(lower, "set-acl") && strings.Contains(lower, "could not autoload matching module"):
		return "Shell diagnosis: this PowerShell session could not load the ACL cmdlets required by the script. Do not keep retrying the same ACL script unchanged. Import the needed module explicitly only if ACL work is actually required; otherwise continue without ACL setup."
	default:
		return ""
	}
}

func appendRunShellGuidance(text, guidance string) string {
	text = strings.TrimSpace(text)
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return text
	}
	if text == "" {
		return guidance
	}
	if strings.Contains(text, guidance) {
		return text
	}
	return text + "\n\n" + guidance
}

func looksLikeNestedPowerShellCommandQuoting(command, lower string) bool {
	commandLower := strings.ToLower(strings.TrimSpace(command))
	if !containsAny(commandLower, "powershell -command", "pwsh -command") {
		return false
	}
	if !strings.Contains(command, "$") {
		return false
	}
	if strings.Contains(lower, "= get-acl .fullname") || strings.Contains(lower, ".setaccessruleprotection(") {
		return true
	}
	return strings.Contains(lower, "missing argument in parameter list") ||
		strings.Contains(lower, "an expression was expected after '('")
}
