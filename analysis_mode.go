package main

import (
	"fmt"
	"strings"
)

var supportedProjectAnalysisModes = []string{
	"map",
	"trace",
	"impact",
	"security",
	"performance",
}

const defaultProjectAnalysisMode = "map"

func normalizeProjectAnalysisMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "map", "trace", "impact", "security", "performance":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func effectiveProjectAnalysisMode(explicitMode string, goal string) string {
	if mode := normalizeProjectAnalysisMode(explicitMode); mode != "" {
		return mode
	}
	return defaultProjectAnalysisMode
}

func projectAnalysisUsage() string {
	return fmt.Sprintf("usage: /analyze-project [--mode %s] <goal>", strings.Join(supportedProjectAnalysisModes, "|"))
}

func projectAnalysisModeStatus(explicitMode string, goal string) string {
	mode := effectiveProjectAnalysisMode(explicitMode, goal)
	if mode == "" {
		return "default(" + defaultProjectAnalysisMode + ")"
	}
	if normalizeProjectAnalysisMode(explicitMode) != "" {
		return mode
	}
	return "default(" + mode + ")"
}

func analysisGoalArtifactSuffix(goal string, mode string) string {
	parts := []string{}
	if normalizedMode := normalizeProjectAnalysisMode(mode); normalizedMode != "" {
		parts = append(parts, normalizedMode)
	}
	if sanitizedGoal := sanitizeFileName(goal); sanitizedGoal != "" {
		parts = append(parts, sanitizedGoal)
	}
	return strings.Join(parts, "_")
}

func analysisArtifactBaseName(runID string, goal string, mode string) string {
	suffix := analysisGoalArtifactSuffix(goal, mode)
	if strings.TrimSpace(suffix) == "" {
		return strings.TrimSpace(runID)
	}
	if strings.TrimSpace(runID) == "" {
		return suffix
	}
	return strings.TrimSpace(runID) + "_" + suffix
}

func analysisLensesForMode(mode string) []AnalysisLens {
	switch normalizeProjectAnalysisMode(mode) {
	case "trace":
		return []AnalysisLens{
			{
				Type:            "runtime_flow",
				PrioritySignals: []string{"startup", "flow", "trace", "dispatch", "entrypoint"},
				OutputFocus:     []string{"execution chain", "caller/callee path", "ownership transitions"},
			},
		}
	case "impact":
		return []AnalysisLens{
			{
				Type:            "runtime_flow",
				PrioritySignals: []string{"dependency", "impact", "change", "callers", "consumers"},
				OutputFocus:     []string{"blast radius", "upstream/downstream dependencies", "change-sensitive surfaces"},
			},
		}
	case "security":
		return []AnalysisLens{
			{
				Type:            "security_boundary",
				PrioritySignals: []string{"trust", "validation", "ioctl", "driver", "authority", "integrity"},
				OutputFocus:     []string{"trust boundaries", "privileged flows", "tamper-sensitive paths"},
			},
		}
	case "performance":
		return []AnalysisLens{
			{
				Type:            "runtime_flow",
				PrioritySignals: []string{"hot path", "startup", "contention", "allocation", "latency"},
				OutputFocus:     []string{"hot path ownership", "blocking chain", "startup cost"},
			},
		}
	default:
		return nil
	}
}

func projectAnalysisModePromptLabel(mode string) string {
	switch normalizeProjectAnalysisMode(mode) {
	case "map":
		return "architecture map"
	case "trace":
		return "execution trace"
	case "impact":
		return "change impact"
	case "security":
		return "security boundary"
	case "performance":
		return "performance hotspot"
	default:
		return ""
	}
}

func projectAnalysisModePromptRequirements(mode string) []string {
	switch normalizeProjectAnalysisMode(mode) {
	case "map":
		return []string{
			"Prioritize subsystem ownership, module boundaries, and representative entry points.",
			"Prefer stable architectural relationships over low-value implementation trivia.",
		}
	case "trace":
		return []string{
			"Prioritize execution order, caller/callee chains, dispatch paths, and authority transitions.",
			"Prefer concrete step-by-step runtime flow over static file inventory.",
		}
	case "impact":
		return []string{
			"Prioritize upstream/downstream dependencies, blast radius, and symbols or files likely to be affected by change.",
			"Call out which modules, RPC surfaces, or startup paths would need retesting when this area changes.",
		}
	case "security":
		return []string{
			"Prioritize trust boundaries, privileged surfaces, validation paths, tamper-sensitive state, and enforcement points.",
			"Call out kernel, driver, RPC, handle, or remote-memory surfaces when they appear in the assigned files.",
		}
	case "performance":
		return []string{
			"Prioritize startup cost, hot path ownership, blocking calls, allocation or copy pressure, and contention risk.",
			"Call out where the runtime would likely pay latency or throughput cost, even if exact profiling data is unavailable.",
		}
	default:
		return nil
	}
}
