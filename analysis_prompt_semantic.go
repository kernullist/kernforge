package main

import (
	"fmt"
	"sort"
	"strings"
)

func analysisShardIntent(name string) string {
	switch {
	case strings.HasPrefix(name, "startup"):
		return "- Trace bootstrap order, startup ownership, and initial runtime handoff."
	case strings.HasPrefix(name, "build_graph"):
		return "- Map project, target, plugin, and module composition with dependency direction."
	case strings.HasPrefix(name, "unreal_network"):
		return "- Trace replication, RPC, and authority boundaries across gameplay types."
	case strings.HasPrefix(name, "unreal_ui"):
		return "- Map widget ownership, UI composition, and gameplay-to-UI coupling."
	case strings.HasPrefix(name, "unreal_ability"):
		return "- Map ability system ownership, action dispatch, effects, and attribute interactions."
	case strings.HasPrefix(name, "asset_config"):
		return "- Map config-driven startup, asset bindings, and runtime load indirection."
	case strings.HasPrefix(name, "integrity_security"):
		return "- Map trust boundaries, anti-tamper controls, and security-sensitive runtime flow."
	case strings.HasPrefix(name, "unreal_gameplay"):
		return "- Map gameplay framework ownership and subsystem responsibilities."
	default:
		return ""
	}
}

func buildSemanticShardFocus(snapshot ProjectSnapshot, shard AnalysisShard) string {
	fileSet := map[string]struct{}{}
	for _, path := range shard.PrimaryFiles {
		fileSet[path] = struct{}{}
	}
	lines := []string{}
	switch {
	case strings.HasPrefix(shard.Name, "startup"):
		lines = append(lines, promptEdgeLines(snapshot.RuntimeEdges, fileSet, 6, "Relevant runtime edges")...)
		lines = append(lines, promptProjectEdgeLines(snapshot.ProjectEdges, fileSet, 6, "Relevant typed project edges")...)
		if strings.TrimSpace(snapshot.PrimaryStartup) != "" {
			lines = append(lines, fmt.Sprintf("- Primary startup candidate: %s", snapshot.PrimaryStartup))
		}
	case strings.HasPrefix(shard.Name, "build_graph"):
		lines = append(lines, buildPromptList("Unreal projects", collectPromptProjectLines(snapshot.UnrealProjects, fileSet))...)
		lines = append(lines, buildPromptList("Unreal plugins", collectPromptPluginLines(snapshot.UnrealPlugins, fileSet))...)
		lines = append(lines, buildPromptList("Unreal targets", collectPromptTargetLines(snapshot.UnrealTargets, fileSet))...)
		lines = append(lines, buildPromptList("Unreal modules", collectPromptModuleLines(snapshot.UnrealModules, fileSet))...)
	case strings.HasPrefix(shard.Name, "unreal_network"):
		lines = append(lines, buildPromptList("Network surfaces", collectPromptNetworkLines(snapshot.UnrealNetwork, fileSet))...)
		lines = append(lines, promptProjectEdgeLines(snapshot.ProjectEdges, fileSet, 8, "Relevant typed project edges")...)
	case strings.HasPrefix(shard.Name, "unreal_ui"):
		lines = append(lines, buildPromptList("Gameplay systems", collectPromptSystemLines(snapshot.UnrealSystems, fileSet))...)
		lines = append(lines, buildPromptList("Asset bindings", collectPromptAssetLines(snapshot.UnrealAssets, fileSet))...)
	case strings.HasPrefix(shard.Name, "unreal_ability"):
		lines = append(lines, buildPromptList("Gameplay systems", collectPromptSystemLines(snapshot.UnrealSystems, fileSet))...)
		lines = append(lines, buildPromptList("Reflected gameplay types", collectPromptTypeLines(snapshot.UnrealTypes, fileSet))...)
	case strings.HasPrefix(shard.Name, "asset_config"):
		lines = append(lines, buildPromptList("Asset/config bindings", collectPromptAssetLines(snapshot.UnrealAssets, fileSet))...)
		lines = append(lines, buildPromptList("Project settings", collectPromptSettingsLines(snapshot.UnrealSettings, fileSet))...)
	case strings.HasPrefix(shard.Name, "integrity_security"):
		lines = append(lines, promptProjectEdgeLines(snapshot.ProjectEdges, fileSet, 8, "Relevant typed project edges")...)
		lines = append(lines, buildPromptList("Security-oriented gameplay systems", collectPromptSystemLines(snapshot.UnrealSystems, fileSet))...)
	case strings.HasPrefix(shard.Name, "unreal_gameplay"):
		lines = append(lines, buildPromptList("Reflected gameplay types", collectPromptTypeLines(snapshot.UnrealTypes, fileSet))...)
		lines = append(lines, buildPromptList("Gameplay systems", collectPromptSystemLines(snapshot.UnrealSystems, fileSet))...)
		lines = append(lines, buildPromptList("Project settings", collectPromptSettingsLines(snapshot.UnrealSettings, fileSet))...)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func buildSemanticReviewerChecklist(name string) string {
	lines := []string{
		"- Reject claims that are not grounded in the assigned files or semantic focus.",
		"- Reject reports that ignore the shard intent and drift into unrelated subsystem summary.",
	}
	switch {
	case strings.HasPrefix(name, "startup"):
		lines = append(lines,
			"- Confirm the report names bootstrap order, startup ownership, and first runtime handoff.",
			"- Reject if startup edges or manifest/target/module relationships are skipped.",
		)
	case strings.HasPrefix(name, "build_graph"):
		lines = append(lines,
			"- Confirm the report names project, target, plugin, and module dependency direction.",
			"- Reject if composition is described vaguely without concrete build ownership evidence.",
		)
	case strings.HasPrefix(name, "unreal_network"):
		lines = append(lines,
			"- Confirm the report separates server/client/multicast RPCs and replicated state.",
			"- Reject if authority boundary or replication ownership is missing.",
		)
	case strings.HasPrefix(name, "unreal_ui"):
		lines = append(lines,
			"- Confirm the report names widget ownership and gameplay-to-UI coupling.",
			"- Reject if UI is described without concrete widgets, owners, or bindings.",
		)
	case strings.HasPrefix(name, "unreal_ability"):
		lines = append(lines,
			"- Confirm the report names abilities, effects, attributes, or ASC-related ownership when present.",
			"- Reject if action-to-effect flow is hand-wavy or unsupported.",
		)
	case strings.HasPrefix(name, "asset_config"):
		lines = append(lines,
			"- Confirm the report names config-driven startup or asset load points when present.",
			"- Reject if asset/config indirection is omitted despite semantic evidence.",
		)
	case strings.HasPrefix(name, "integrity_security"):
		lines = append(lines,
			"- Confirm the report names trust boundaries, anti-tamper controls, or sensitive runtime state.",
			"- Reject if security-sensitive flow is summarized generically without concrete evidence files.",
		)
	case strings.HasPrefix(name, "unreal_gameplay"):
		lines = append(lines,
			"- Confirm the report names framework ownership such as GameMode, Controller, Pawn, Character, or Subsystem roles.",
			"- Reject if gameplay responsibility is described without type-level evidence.",
		)
	}
	return strings.Join(lines, "\n")
}

func promptEdgeLines(edges []RuntimeEdge, fileSet map[string]struct{}, limit int, header string) []string {
	lines := []string{}
	for _, edge := range edges {
		if !edgeTouchesFiles(edge.Evidence, fileSet) {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s -> %s (%s, confidence=%s)", edge.Source, edge.Target, edge.Kind, edge.Confidence))
		if len(lines) >= limit {
			break
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return append([]string{header + ":"}, lines...)
}

func promptProjectEdgeLines(edges []ProjectEdge, fileSet map[string]struct{}, limit int, header string) []string {
	lines := []string{}
	for _, edge := range edges {
		if !edgeTouchesFiles(edge.Evidence, fileSet) {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s -> %s [%s, confidence=%s]", edge.Source, edge.Target, edge.Type, edge.Confidence))
		if len(lines) >= limit {
			break
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return append([]string{header + ":"}, lines...)
}

func edgeTouchesFiles(evidence []string, fileSet map[string]struct{}) bool {
	for _, item := range evidence {
		for path := range fileSet {
			if strings.Contains(item, path) {
				return true
			}
		}
	}
	return false
}

func buildPromptList(header string, items []string) []string {
	if len(items) == 0 {
		return nil
	}
	return append([]string{header + ":"}, prefixPromptItems(items)...)
}

func prefixPromptItems(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, "- "+item)
	}
	return out
}

func collectPromptProjectLines(items []UnrealProject, fileSet map[string]struct{}) []string {
	lines := []string{}
	for _, item := range items {
		if _, ok := fileSet[item.Path]; !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s path=%s modules=%s plugins=%s", item.Name, item.Path, strings.Join(limitStrings(item.Modules, 4), ", "), strings.Join(limitStrings(item.Plugins, 4), ", ")))
	}
	sort.Strings(lines)
	return lines
}

func collectPromptPluginLines(items []UnrealPlugin, fileSet map[string]struct{}) []string {
	lines := []string{}
	for _, item := range items {
		if _, ok := fileSet[item.Path]; !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s path=%s enabled_by_default=%t modules=%s", item.Name, item.Path, item.EnabledByDefault, strings.Join(limitStrings(item.Modules, 4), ", ")))
	}
	sort.Strings(lines)
	return lines
}

func collectPromptTargetLines(items []UnrealTarget, fileSet map[string]struct{}) []string {
	lines := []string{}
	for _, item := range items {
		if _, ok := fileSet[item.Path]; !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s type=%s path=%s modules=%s", item.Name, item.TargetType, item.Path, strings.Join(limitStrings(item.Modules, 4), ", ")))
	}
	sort.Strings(lines)
	return lines
}

func collectPromptModuleLines(items []UnrealModule, fileSet map[string]struct{}) []string {
	lines := []string{}
	for _, item := range items {
		if _, ok := fileSet[item.Path]; !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s (%s) public=%s private=%s dynamic=%s", item.Name, item.Path, strings.Join(limitStrings(item.PublicDependencies, 4), ", "), strings.Join(limitStrings(item.PrivateDependencies, 4), ", "), strings.Join(limitStrings(item.DynamicallyLoaded, 4), ", ")))
	}
	sort.Strings(lines)
	return lines
}

func collectPromptTypeLines(items []UnrealReflectedType, fileSet map[string]struct{}) []string {
	lines := []string{}
	for _, item := range items {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s kind=%s role=%s base=%s", item.Name, item.Kind, item.GameplayRole, item.BaseClass))
	}
	sort.Strings(lines)
	return lines
}

func collectPromptNetworkLines(items []UnrealNetworkSurface, fileSet map[string]struct{}) []string {
	lines := []string{}
	for _, item := range items {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s server=%s client=%s multicast=%s replicated=%s", firstNonBlankAnalysisString(item.TypeName, item.File), strings.Join(limitStrings(item.ServerRPCs, 4), ", "), strings.Join(limitStrings(item.ClientRPCs, 4), ", "), strings.Join(limitStrings(item.MulticastRPCs, 4), ", "), strings.Join(limitStrings(item.ReplicatedProperties, 4), ", ")))
	}
	sort.Strings(lines)
	return lines
}

func collectPromptAssetLines(items []UnrealAssetReference, fileSet map[string]struct{}) []string {
	lines := []string{}
	for _, item := range items {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s assets=%s config=%s load=%s", firstNonBlankAnalysisString(item.OwnerName, item.File), strings.Join(limitStrings(item.CanonicalTargets, 4), ", "), strings.Join(limitStrings(item.ConfigKeys, 4), ", "), strings.Join(limitStrings(item.LoadMethods, 4), ", ")))
	}
	sort.Strings(lines)
	return lines
}

func collectPromptSystemLines(items []UnrealGameplaySystem, fileSet map[string]struct{}) []string {
	lines := []string{}
	for _, item := range items {
		if _, ok := fileSet[item.File]; !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s owner=%s actions=%s widgets=%s abilities=%s effects=%s", item.System, firstNonBlankAnalysisString(item.OwnerName, item.File), strings.Join(limitStrings(item.Actions, 4), ", "), strings.Join(limitStrings(item.Widgets, 4), ", "), strings.Join(limitStrings(item.Abilities, 4), ", "), strings.Join(limitStrings(item.Effects, 4), ", ")))
	}
	sort.Strings(lines)
	return lines
}

func collectPromptSettingsLines(items []UnrealProjectSetting, fileSet map[string]struct{}) []string {
	lines := []string{}
	for _, item := range items {
		if _, ok := fileSet[item.SourceFile]; !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s map=%s game_mode=%s game_instance=%s", item.SourceFile, item.GameDefaultMap, item.GlobalDefaultGameMode, item.GameInstanceClass))
	}
	sort.Strings(lines)
	return lines
}
