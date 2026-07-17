package main

import (
	"fmt"
	"strings"
)

// detection_design_guards.go encodes hard-won anti-cheat design rules so the
// coding agent, plan-review loop, and review harness reject path-only
// "SetWindowsHook = non-system DLL" heuristics before they ship.

func requestNeedsInjectionDetectionDesignGuard(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"setwindowshook", "set windows hook", "windows hook", "windowshook",
		"setwindowshookex",
		"dll injection", "dll-injection", "dllinjection", "dll_injection",
		"dll 인젝션", "dll인젝션", "디엘엘 인젝션",
		"hook injection", "훅 인젝션", "훅인젝션",
		"image injection", "remote thread", "createremotethread",
		"loadlibrary", "manual map", "manualmap",
		"proxy dll", "proxydll", "proxy-dll",
		"dllinjectionfiltering", "dll injection filtering",
	)
}

// injectionDetectionDesignGuardPrompt is injected into the coding-agent system
// prompt and plan-review prompts when the request is about hook/DLL injection
// defense. Keep it concrete and operational — models ignore abstract advice.
func injectionDetectionDesignGuardPrompt() string {
	var b strings.Builder
	b.WriteString("Detection/injection design guard (mandatory for this request):\n")
	b.WriteString("1. Do NOT treat \"DLL path is outside System32/Windows\" (or \"non-system path image section\") as proof of SetWindowsHook injection.\n")
	b.WriteString("2. Protected game clients legitimately load many non-system DLLs (engine modules, plugins, overlays, GPU stacks, AC modules). Path-only blocking causes severe false positives and is not a SetWindowsHook signature.\n")
	b.WriteString("3. Correct SetWindowsHook detection is behavioral correlation:\n")
	b.WriteString("   - external process installs SetWindowsHookEx (caller PID/path, DLL path, hook type/FilterType, hmod/module base)\n")
	b.WriteString("   - protected process later maps that same DLL (ImageLoad / image section) with module-base and/or name match\n")
	b.WriteString("   - prefer existing ETW/user-mode correlation when the codebase already has it; extend it instead of inventing a path heuristic\n")
	b.WriteString("4. Keep ProxyDllFiltering separate: it is a known hijackable-name/path policy (e.g. game-adjacent version.dll), not a SetWindowsHook detector. Do not reuse ProxyDll path lists as generic injection detection.\n")
	b.WriteString("5. If kernel blocking is required, push correlated or explicitly listed DLL paths/hashes into policy (DETECT/BLOCK for the protected process). Never implement \"any non-system executable mapping => block\".\n")
	b.WriteString("6. Before coding, inspect existing hook/ImageLoad handlers, minifilter placeholders (ProxyDll vs DllInjection), and policy modes. State the threat model and correlation/evidence fields first; reject plans that only check system-directory membership.\n")
	return b.String()
}

func planReviewInjectionDesignGuardPrompt() string {
	var b strings.Builder
	b.WriteString("\nInjection/detection plan checklist (reject the plan if any fail):\n")
	b.WriteString("- Reject plans that detect SetWindowsHook injection solely by non-system/system32 path checks on image maps in the protected process.\n")
	b.WriteString("- Require correlation of hook install (external caller + DLL + hook type + hmod) with protected-process load of that DLL, or an explicit allow/block path/hash policy derived from that correlation.\n")
	b.WriteString("- Require ProxyDll and SetWindowsHook/injection features to stay separate threat models.\n")
	b.WriteString("- Require FP analysis: which legitimate non-system DLLs would the plan block?\n")
	b.WriteString("- Require reuse of existing ETW/minifilter structures when present instead of a greenfield path heuristic.\n")
	return b.String()
}

func reviewPolicyPackDesignGuidance(packs []string, objective string) string {
	needAntiCheat := false
	needKernel := false
	for _, pack := range packs {
		switch strings.ToLower(strings.TrimSpace(pack)) {
		case "anti_cheat_telemetry":
			needAntiCheat = true
		case "windows_kernel_driver":
			needKernel = true
		}
	}
	if requestNeedsInjectionDetectionDesignGuard(objective) {
		needAntiCheat = true
	}
	if !needAntiCheat && !needKernel {
		return ""
	}
	var b strings.Builder
	if needAntiCheat {
		b.WriteString("- anti_cheat_telemetry / injection defense: flag path-only \"non-system DLL load = injection/SetWindowsHook\" designs as false_positive and design defects. Prefer ETW/hook-install ↔ ImageLoad correlation, allow/block lists of specific paths or hashes, and provenance of the injector process. Treat ProxyDll path policy as a different threat model.\n")
	}
	if needKernel {
		b.WriteString("- windows_kernel_driver minifilter/section gates: blocking executable image mapping must be tied to an explicit protected-process policy and specific blocked paths/hashes (or a prior correlation-derived policy). Do not approve \"block every non-system DLL mapping in the target process\" as SetWindowsHook mitigation.\n")
	}
	return strings.TrimSpace(b.String())
}

// textLooksLikeNonSystemPathInjectionHeuristic reports whether text claims that
// non-system / outside-System32 DLL mapping is itself SetWindowsHook or
// generic DLL-injection detection/blocking — without behavioral correlation.
func textLooksLikeNonSystemPathInjectionHeuristic(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	hookOrInjectionTopic := containsAny(lower,
		"setwindowshook", "set windows hook", "windowshook", "setwindowshookex",
		"dll injection", "dll-injection", "dllinjection", "dll_injection",
		"dll 인젝션", "dll인젝션",
		"hook injection", "훅 인젝션",
		"dllinjectionfiltering", "dll injection filtering",
		"image section", "executable mapping", "acquire_for_section",
	)
	if !hookOrInjectionTopic {
		return false
	}
	pathHeuristic := containsAny(lower,
		"non-system", "non system", "not system", "outside system",
		"non_system", "nonsystem",
		"비시스템", "시스템 디렉토리", "시스템 경로", "시스템 디렉토리가 아니",
		"system directory", "system32", "\\windows\\system32",
		"issystemdirectory", "issystempath", "is_system_path", "is_system_directory",
		"system path", "시스템dll", "system dll",
	)
	if !pathHeuristic {
		return false
	}
	// Correlation signals indicate a proper design (or extension of one).
	if textHasInjectionCorrelationSignals(lower) {
		return false
	}
	return true
}

func textHasInjectionCorrelationSignals(lower string) bool {
	// Phrase / long-token matches (safe as substrings).
	if containsAny(lower,
		"modulebase", "module base", "module_base",
		"filtertype", "filter type",
		"imageload", "image load", "image_load",
		"callerpid", "caller pid", "caller_process", "caller process",
		"injector", "correlation", "correlate",
		"wh_event", "whevent", "setwindowshookex event",
		"hook install", "훅 설치",
		"event tracing", "etwconsumer", "etw event",
	) {
		return true
	}
	// Short tokens must use word boundaries: "etw" is a substring of
	// "setwindowshook", and "hmod" can appear inside unrelated identifiers.
	return containsAnyWord(lower, "etw", "hmod")
}

// containsAnyWord reports whether any needle appears in text as a whole word
// (ASCII letter/digit/_ boundaries). Used for short tokens that would false-
// match as substrings (e.g. "etw" inside "setwindowshook").
func containsAnyWord(text string, needles ...string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle == "" {
			continue
		}
		start := 0
		for {
			rel := strings.Index(text[start:], needle)
			if rel < 0 {
				break
			}
			idx := start + rel
			if idx > 0 && isASCIIWordByte(text[idx-1]) {
				start = idx + len(needle)
				continue
			}
			after := idx + len(needle)
			if after < len(text) && isASCIIWordByte(text[after]) {
				start = idx + len(needle)
				continue
			}
			return true
		}
	}
	return false
}

func isASCIIWordByte(b byte) bool {
	if b >= 'a' && b <= 'z' {
		return true
	}
	if b >= 'A' && b <= 'Z' {
		return true
	}
	if b >= '0' && b <= '9' {
		return true
	}
	return b == '_'
}

// reviewEvidenceTextForDesignGuards gathers the free-text surfaces a
// deterministic design check can inspect without inventing files.
func reviewEvidenceTextForDesignGuards(run ReviewRun) string {
	parts := []string{
		run.Objective,
		run.RequestAnalysis.OriginalRequest,
		run.OriginalMainProposal,
		run.ImplementationReply,
		run.Evidence.Text,
		run.Evidence.VerificationSummary,
		run.Result.Summary,
		run.ChangeSet.DiffExcerpt,
		run.ChangeSet.DiffStat,
		strings.Join(run.ChangeSet.ChangedPaths, "\n"),
		strings.Join(run.Evidence.Sources, "\n"),
	}
	return strings.Join(parts, "\n")
}

func deterministicInjectionDesignFindings(run ReviewRun) []ReviewFinding {
	text := reviewEvidenceTextForDesignGuards(run)
	if !textLooksLikeNonSystemPathInjectionHeuristic(text) {
		return nil
	}
	preWrite := strings.EqualFold(strings.TrimSpace(run.Trigger), "pre_write")
	severity := reviewSeverityHigh
	blocks := false
	if preWrite {
		// Stop path-only SetWindowsHook/injection heuristics before they land.
		// Single-model routes skip model review, so this deterministic finding
		// is the main hard gate for the known bad design.
		severity = reviewSeverityBlocker
		blocks = true
	}
	return []ReviewFinding{{
		Source:       "deterministic",
		ReviewerRole: "detection_design_guard",
		Severity:     severity,
		Category:     "false_positive",
		Confidence:   "high",
		Quality:      reviewFindingQualityComplete,
		Title:        "Path-only non-system DLL check is not SetWindowsHook/injection detection",
		Evidence: compactPromptSection(
			"The proposal or evidence treats non-system / outside-System32 DLL image mapping in the protected process as SetWindowsHook or DLL-injection detection/blocking, without hook-install ↔ ImageLoad correlation (caller, DLL, hook type, hmod/module base).",
			500,
		),
		Impact: "Games legitimately load many non-system DLLs; this design causes severe false positives, bricks normal clients, and still misses real hook injection that needs behavioral correlation or an explicit path/hash policy.",
		RequiredFix: strings.Join([]string{
			"Replace the path-only heuristic with either:",
			"(1) SetWindowsHookEx install correlation (external caller + DLL + hook type + hmod) matched to protected-process ImageLoad/module base/name, reusing existing ETW handlers when present; or",
			"(2) an explicit allow/block list of specific DLL paths or hashes pushed as policy for the protected process.",
			"Keep ProxyDllFiltering (known hijackable names next to the game) separate from SetWindowsHook/injection correlation.",
		}, " "),
		TestRecommendation: "Document which legitimate non-system DLLs the old heuristic would have blocked, and show a correlated hook-install + ImageLoad case the new design detects.",
		BlocksGate:         blocks,
	}}
}

func formatInjectionDesignGuardFindingSummary(korean bool) string {
	if korean {
		return "비시스템 경로 DLL 여부만으로 SetWindowsHook/인젝션을 판단하는 설계는 거부됩니다. 훅 설치↔ImageLoad 상관 또는 명시 경로/해시 정책이 필요합니다."
	}
	return "Reject path-only non-system DLL heuristics for SetWindowsHook/injection; require hook-install↔ImageLoad correlation or an explicit path/hash policy."
}

// injectionDesignGuardPromptSection returns the coding-agent system section
// when the latest user request needs it; empty otherwise.
func injectionDesignGuardPromptSection(latestUser string) string {
	if !requestNeedsInjectionDetectionDesignGuard(latestUser) {
		return ""
	}
	return injectionDetectionDesignGuardPrompt()
}

// appendPlanReviewInjectionDesignGuards mutates planner/reviewer system prompts
// when the user request is about injection/hook defense.
func appendPlanReviewInjectionDesignGuards(systemPrompt string, userPrompt string) string {
	if !requestNeedsInjectionDetectionDesignGuard(userPrompt) {
		return systemPrompt
	}
	return strings.TrimSpace(systemPrompt) + "\n\n" + injectionDetectionDesignGuardPrompt() + planReviewInjectionDesignGuardPrompt()
}

// reviewObjectiveNeedsFalsePositiveLens helps force the false_positive lens
// when the objective is clearly detection-shaped even if policy packs lagged.
func reviewObjectiveNeedsFalsePositiveLens(objective string) bool {
	return requestNeedsInjectionDetectionDesignGuard(objective) ||
		containsAny(strings.ToLower(objective),
			"false positive", "false_positive", "오탐",
			"detection", "탐지", "telemetry", "텔레메트리",
		)
}

func explainWhyPathOnlyInjectionIsInvalid(korean bool) string {
	if korean {
		return fmt.Sprintf("%s %s",
			formatInjectionDesignGuardFindingSummary(true),
			"ProxyDll 경로 정책과 혼동하지 마세요.")
	}
	return formatInjectionDesignGuardFindingSummary(false) + " Do not confuse this with ProxyDll path policy."
}
