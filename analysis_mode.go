package main

import (
	"fmt"
	"strings"
)

var supportedProjectAnalysisModes = []string{
	"map",
	"trace",
	"impact",
	"surface",
	"security",
	"performance",
}

const defaultProjectAnalysisMode = "map"

func normalizeProjectAnalysisMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "map", "trace", "impact", "surface", "security", "performance":
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
	return fmt.Sprintf("usage: /analyze-project [--path <dir>] [--mode %s] [goal]", strings.Join(supportedProjectAnalysisModes, "|"))
}

func defaultProjectAnalysisGoal(mode string, paths []string) string {
	scope := "the project"
	cleanPaths := []string{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" {
			cleanPaths = append(cleanPaths, path)
		}
	}
	if len(cleanPaths) > 0 {
		scope = strings.Join(cleanPaths, ", ")
	}
	switch normalizeProjectAnalysisMode(mode) {
	case "trace":
		return "trace the primary runtime flows, dispatch paths, caller/callee chains, and ownership transitions in " + scope
	case "impact":
		return "analyze change impact, dependencies, blast radius, retest targets, and stale documentation risks in " + scope
	case "surface":
		return "inventory exposed IOCTL, RPC, parser, handle, memory-copy, telemetry decoder, network, and fuzzable surfaces in " + scope
	case "security":
		return "analyze trust boundaries, privileged paths, validation, tamper-sensitive state, and enforcement points in " + scope
	case "performance":
		return "map startup cost, hot paths, blocking chains, allocation or copy pressure, contention, and profiling priorities in " + scope
	default:
		return "map the architecture, subsystems, ownership, module boundaries, entry points, documentation, dashboard, and reusable knowledge base for " + scope
	}
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
	case "surface", "security":
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
	case "surface":
		return "security surface"
	case "security":
		return "security boundary"
	case "performance":
		return "performance hotspot"
	default:
		return ""
	}
}

func projectAnalysisModeCompletionDescription(mode string) string {
	switch normalizeProjectAnalysisMode(mode) {
	case "map":
		return "Build the default architecture map: subsystems, ownership, module boundaries, entry points, docs, dashboard, and reusable knowledge base."
	case "trace":
		return "Follow one runtime or request flow through callers, callees, dispatch points, ownership transitions, and source anchors."
	case "impact":
		return "Estimate change blast radius: upstream/downstream dependencies, affected files, retest targets, and stale documentation risks."
	case "surface":
		return "Inventory exposed entry surfaces: IOCTL, RPC, parsers, handles, memory-copy paths, telemetry decoders, network inputs, and fuzz targets."
	case "security":
		return "Analyze trust boundaries, validation, privileged paths, tamper-sensitive state, enforcement points, and driver/IOCTL/handle/RPC risks."
	case "performance":
		return "Map performance risk: startup cost, hot paths, blocking chains, allocation/copy pressure, contention, and profiling order."
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
	case "surface":
		return []string{
			"Prioritize concrete exposed surfaces: IOCTL, RPC, parser, handle, memory-copy, telemetry decoder, and network entry points.",
			"Attach source anchors and confidence for each surface so fuzzing and verification can reuse the result directly.",
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
