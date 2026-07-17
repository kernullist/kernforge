package main

import (
	"strings"
	"testing"
)

func TestRequestNeedsInjectionDetectionDesignGuard(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"@TavernKernelFileFilter.cpp 에 SetWindowsHook으로 DLL 인젝션 탐지하고 차단 구현", true},
		{"implement DllInjectionFiltering for protected game clients", true},
		{"Proxy DLL path policy next to the game", true},
		{"refactor logging format in the worker", false},
		{"update README only", false},
	}
	for _, tc := range cases {
		if got := requestNeedsInjectionDetectionDesignGuard(tc.text); got != tc.want {
			t.Fatalf("requestNeedsInjectionDetectionDesignGuard(%q)=%v want %v", tc.text, got, tc.want)
		}
	}
}

func TestTextLooksLikeNonSystemPathInjectionHeuristic(t *testing.T) {
	bad := `SetWindowsHook 기반 DLL 인젝션은 보호 대상 프로세스가 비시스템 경로의 DLL을 image section으로 매핑할 때 발생합니다. 시스템 디렉토리가 아니면 차단합니다.`
	if !textLooksLikeNonSystemPathInjectionHeuristic(bad) {
		t.Fatalf("expected bad path-only SetWindowsHook plan to be flagged")
	}

	good := `OnETWEventSetWindowsHook records callerPid, dllPath, FilterType, hmod; OnETWEventImageLoad correlates moduleBase and file name into the game process.`
	if textLooksLikeNonSystemPathInjectionHeuristic(good) {
		t.Fatalf("correlated ETW design must not be flagged as path-only heuristic")
	}

	proxyOnly := `ProxyDllFiltering blocks known hijackable DLL paths next to the game when IsProxyDllBlockedPath matches.`
	if textLooksLikeNonSystemPathInjectionHeuristic(proxyOnly) {
		t.Fatalf("ProxyDll path policy alone without SetWindowsHook claim must not match injection path heuristic")
	}
}

func TestInjectionDesignGuardPromptSectionInjected(t *testing.T) {
	section := injectionDesignGuardPromptSection("SetWindowsHookEx DLL injection detect and block")
	if !strings.Contains(section, "Detection/injection design guard") {
		t.Fatalf("expected design guard section, got %q", section)
	}
	if !strings.Contains(section, "non-system") {
		t.Fatalf("expected non-system anti-pattern wording, got %q", section)
	}
	if injectionDesignGuardPromptSection("fix a typo in comments") != "" {
		t.Fatalf("unrelated request must not inject the injection design guard")
	}
}

func TestPlanReviewInjectionDesignGuardsAppended(t *testing.T) {
	base := "You are a planner."
	out := appendPlanReviewInjectionDesignGuards(base, "implement SetWindowsHook detection")
	if !strings.Contains(out, base) || !strings.Contains(out, "Injection/detection plan checklist") {
		t.Fatalf("expected checklist appended, got %q", out)
	}
	if got := appendPlanReviewInjectionDesignGuards(base, "rename a variable"); got != base {
		t.Fatalf("unrelated plan prompt must stay unchanged, got %q", got)
	}
}

func TestDeterministicInjectionDesignFindingsBlocksPreWrite(t *testing.T) {
	run := ReviewRun{
		Trigger:  "pre_write",
		Objective: "SetWindowsHook DLL 인젝션 탐지 및 차단 구현",
		OriginalMainProposal: `기존 ProxyDllFiltering 패턴과 일관되게 구현.
비시스템 경로의 DLL을 image section으로 매핑할 때 SetWindowsHook 인젝션으로 보고 차단한다.`,
		Evidence: ReviewEvidencePack{
			Sources: []string{"TavernKernelFileFilter.cpp"},
			Text:    "Proposed: block non-system DLL image maps in the protected process as SetWindowsHook injection.",
		},
	}
	findings := deterministicInjectionDesignFindings(run)
	if len(findings) != 1 {
		t.Fatalf("expected one design finding, got %#v", findings)
	}
	if findings[0].Severity != reviewSeverityBlocker || !findings[0].BlocksGate {
		t.Fatalf("pre-write path-only injection heuristic must block, got %#v", findings[0])
	}
	if findings[0].Category != "false_positive" {
		t.Fatalf("expected false_positive category, got %#v", findings[0])
	}
}

func TestDeterministicInjectionDesignFindingsAllowsCorrelatedDesign(t *testing.T) {
	run := ReviewRun{
		Trigger:   "pre_write",
		Objective: "SetWindowsHook DLL injection detect and block",
		OriginalMainProposal: `Extend OnETWEventSetWindowsHook + OnETWEventImageLoad correlation (callerPid, dllPath, FilterType, hmod/moduleBase).
Push correlated DLL paths into DllInjectionFiltering policy for DETECT/BLOCK.`,
		Evidence: ReviewEvidencePack{
			Sources: []string{"TavernWorkerCore.cpp"},
			Text:    "Correlate SetWindowsHookEx install with ImageLoad moduleBase match, then block listed paths.",
		},
	}
	if findings := deterministicInjectionDesignFindings(run); len(findings) != 0 {
		t.Fatalf("correlated design must not raise path-only finding, got %#v", findings)
	}
}

func TestReviewPolicyPacksIncludeAntiCheatForHookRequest(t *testing.T) {
	packs := reviewPolicyPacksFor(reviewTargetChange, reviewModeLiveFix, []string{"TavernKernel/FileFilter.cpp"},
		"SetWindowsHook으로 보호 대상 게임 클라이언트에 DLL 인젝션하는것을 탐지하고 차단")
	if !stringSliceContainsCI(packs, "anti_cheat_telemetry") {
		t.Fatalf("expected anti_cheat_telemetry pack for SetWindowsHook request, got %#v", packs)
	}
}

func TestReviewLensesForceFalsePositiveForInjectionObjective(t *testing.T) {
	run := ReviewRun{
		Objective: "Implement SetWindowsHookEx injection detection and policy block",
		Flow:      "change_review",
	}
	required, _ := reviewLensesForRun(run)
	if !stringSliceContainsCI(required, "false_positive") {
		t.Fatalf("injection objective must require false_positive lens, got %#v", required)
	}
}

func TestReviewPolicyPackDesignGuidanceMentionsPathHeuristic(t *testing.T) {
	text := reviewPolicyPackDesignGuidance([]string{"anti_cheat_telemetry", "windows_kernel_driver"}, "SetWindowsHook block")
	if !strings.Contains(text, "non-system") && !strings.Contains(text, "path-only") {
		t.Fatalf("expected path-only / non-system guard wording, got %q", text)
	}
}

func TestAgentSystemPromptIncludesInjectionDesignGuard(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "test-model", "", "default")
	session.AddMessage(Message{
		Role: "user",
		Text: "TavernKernelFileFilter.cpp 에 SetWindowsHook DLL 인젝션 탐지 및 차단 구조를 구현해줘",
	})
	agent := &Agent{
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Config:    DefaultConfig(root),
	}
	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Detection/injection design guard") {
		t.Fatalf("system prompt must include injection design guard for SetWindowsHook request, got length=%d", len(prompt))
	}
	if !strings.Contains(prompt, "ProxyDllFiltering") {
		t.Fatalf("system prompt guard must separate ProxyDll from SetWindowsHook, got %q", prompt)
	}
}
