package main

import (
	"sort"
	"strings"
)

type semanticShardSignals struct {
	StartupPaths  map[string]struct{}
	BuildPaths    map[string]struct{}
	GameplayPaths map[string]struct{}
	NetworkPaths  map[string]struct{}
	UIPaths       map[string]struct{}
	AbilityPaths  map[string]struct{}
	AssetPaths    map[string]struct{}
	SecurityPaths map[string]struct{}
}

func (a *projectAnalyzer) planSemanticShards(snapshot ProjectSnapshot, desiredShards int) []AnalysisShard {
	if !hasSemanticShardSignals(snapshot) {
		return nil
	}
	signals := collectSemanticShardSignals(snapshot)
	orderedFiles := append([]ScannedFile(nil), snapshot.Files...)
	sort.Slice(orderedFiles, func(i int, j int) bool {
		if orderedFiles[i].ImportanceScore == orderedFiles[j].ImportanceScore {
			return orderedFiles[i].Path < orderedFiles[j].Path
		}
		return orderedFiles[i].ImportanceScore > orderedFiles[j].ImportanceScore
	})

	buckets := map[string][]ScannedFile{
		"startup":            {},
		"build_graph":        {},
		"unreal_network":     {},
		"unreal_ui":          {},
		"unreal_ability":     {},
		"asset_config":       {},
		"integrity_security": {},
		"unreal_gameplay":    {},
	}
	assigned := map[string]struct{}{}
	for _, file := range orderedFiles {
		bucket := classifySemanticShardFile(file, signals)
		if bucket == "" {
			continue
		}
		if _, ok := assigned[file.Path]; ok {
			continue
		}
		assigned[file.Path] = struct{}{}
		buckets[bucket] = append(buckets[bucket], file)
	}

	nonEmptyBuckets := 0
	assignedFiles := 0
	for _, files := range buckets {
		if len(files) > 0 {
			nonEmptyBuckets++
			assignedFiles += len(files)
		}
	}
	if nonEmptyBuckets < 2 || assignedFiles < 4 {
		return nil
	}

	shards := []AnalysisShard{}
	orderedBuckets := []string{
		"startup",
		"build_graph",
		"unreal_network",
		"unreal_ui",
		"unreal_ability",
		"asset_config",
		"integrity_security",
		"unreal_gameplay",
	}
	for _, name := range orderedBuckets {
		files := buckets[name]
		if len(files) == 0 {
			continue
		}
		sort.Slice(files, func(i int, j int) bool {
			if files[i].ImportanceScore == files[j].ImportanceScore {
				return files[i].Path < files[j].Path
			}
			return files[i].ImportanceScore > files[j].ImportanceScore
		})
		chunks := chunkFiles(files, a.analysisCfg.MaxFilesPerShard, a.analysisCfg.MaxLinesPerShard)
		for chunkIndex, chunk := range chunks {
			shards = append(shards, AnalysisShard{
				Name:           shardName(name, chunkIndex, len(chunks)),
				PrimaryFiles:   filesToPaths(chunk),
				EstimatedFiles: len(chunk),
				EstimatedLines: sumLines(chunk),
			})
		}
	}

	remaining := filterSnapshotForUnassignedFiles(snapshot, assigned)
	if len(remaining.Files) > 0 {
		clusterTarget := desiredShards - len(shards)
		if clusterTarget < 1 {
			clusterTarget = 1
		}
		clusters := a.planDirectoryClusters(remaining, clusterTarget)
		if len(clusters) > 0 {
			for _, cluster := range clusters {
				fileChunks := a.collectClusterFileChunks(remaining, cluster)
				for chunkIndex, files := range fileChunks {
					shards = append(shards, AnalysisShard{
						Name:           shardName(clusterName(cluster), chunkIndex, len(fileChunks)),
						PrimaryFiles:   filesToPaths(files),
						EstimatedFiles: len(files),
						EstimatedLines: sumLines(files),
					})
				}
			}
		} else {
			for _, shard := range a.planNonRootDirectoryShards(remaining) {
				shards = append(shards, shard)
			}
		}
	}

	return shards
}

func hasSemanticShardSignals(snapshot ProjectSnapshot) bool {
	return len(snapshot.UnrealProjects) > 0 ||
		len(snapshot.UnrealPlugins) > 0 ||
		len(snapshot.UnrealTargets) > 0 ||
		len(snapshot.UnrealModules) > 0 ||
		len(snapshot.UnrealTypes) > 0 ||
		len(snapshot.UnrealNetwork) > 0 ||
		len(snapshot.UnrealAssets) > 0 ||
		len(snapshot.UnrealSystems) > 0 ||
		len(snapshot.UnrealSettings) > 0
}

func collectSemanticShardSignals(snapshot ProjectSnapshot) semanticShardSignals {
	signals := semanticShardSignals{
		StartupPaths:  map[string]struct{}{},
		BuildPaths:    map[string]struct{}{},
		GameplayPaths: map[string]struct{}{},
		NetworkPaths:  map[string]struct{}{},
		UIPaths:       map[string]struct{}{},
		AbilityPaths:  map[string]struct{}{},
		AssetPaths:    map[string]struct{}{},
		SecurityPaths: map[string]struct{}{},
	}
	for _, path := range analysisUniqueStrings(append([]string{}, snapshot.EntrypointFiles...)) {
		signals.StartupPaths[path] = struct{}{}
	}
	for _, path := range startupProjectEntryFiles(snapshot) {
		signals.StartupPaths[path] = struct{}{}
	}
	for _, path := range analysisUniqueStrings(snapshot.ManifestFiles) {
		signals.BuildPaths[path] = struct{}{}
	}
	for _, item := range snapshot.UnrealProjects {
		signals.BuildPaths[item.Path] = struct{}{}
	}
	for _, item := range snapshot.UnrealPlugins {
		signals.BuildPaths[item.Path] = struct{}{}
		if containsAny(strings.ToLower(item.Name), "anti", "cheat", "guard", "integrity", "tamper") {
			signals.SecurityPaths[item.Path] = struct{}{}
		}
	}
	for _, item := range snapshot.UnrealTargets {
		signals.BuildPaths[item.Path] = struct{}{}
	}
	for _, item := range snapshot.UnrealModules {
		signals.BuildPaths[item.Path] = struct{}{}
		if containsAny(strings.ToLower(item.Name), "anti", "cheat", "guard", "integrity", "tamper", "scan", "memory", "telemetry") {
			signals.SecurityPaths[item.Path] = struct{}{}
		}
	}
	for _, item := range snapshot.UnrealTypes {
		lowerRole := strings.ToLower(item.GameplayRole)
		lowerName := strings.ToLower(item.Name)
		lowerFile := strings.ToLower(item.File)
		switch lowerRole {
		case "game_instance", "game_mode", "game_state", "player_controller", "player_state", "pawn", "character", "subsystem":
			signals.GameplayPaths[item.File] = struct{}{}
		case "hud":
			signals.UIPaths[item.File] = struct{}{}
		}
		if containsAny(lowerName, "widget", "hud", "umg") || containsAny(lowerFile, "widget", "hud", "ui", "umg") {
			signals.UIPaths[item.File] = struct{}{}
		}
		if containsAny(lowerName, "ability", "attributeset", "effect") || containsAny(lowerFile, "ability", "attributeset", "effect") {
			signals.AbilityPaths[item.File] = struct{}{}
		}
		if containsAny(lowerName, "anti", "cheat", "guard", "integrity", "tamper", "scanner") || containsAny(lowerFile, "anti", "cheat", "guard", "integrity", "tamper", "scanner", "memory", "telemetry") {
			signals.SecurityPaths[item.File] = struct{}{}
		}
	}
	for _, item := range snapshot.UnrealNetwork {
		if strings.TrimSpace(item.File) != "" {
			signals.NetworkPaths[item.File] = struct{}{}
		}
	}
	for _, item := range snapshot.UnrealAssets {
		if strings.TrimSpace(item.File) != "" {
			signals.AssetPaths[item.File] = struct{}{}
		}
	}
	for _, item := range snapshot.UnrealSystems {
		lowerSystem := strings.ToLower(item.System)
		lowerFile := strings.ToLower(item.File)
		if len(item.Widgets) > 0 || containsAny(lowerSystem, "widget", "ui", "umg") || containsAny(lowerFile, "widget", "ui", "umg", "hud") {
			signals.UIPaths[item.File] = struct{}{}
		}
		if len(item.Abilities) > 0 || len(item.Effects) > 0 || len(item.Attributes) > 0 || containsAny(lowerSystem, "ability", "effect") {
			signals.AbilityPaths[item.File] = struct{}{}
		}
		if len(item.Actions) > 0 || len(item.Contexts) > 0 || len(item.Targets) > 0 || containsAny(lowerSystem, "input", "gameplay", "subsystem") {
			signals.GameplayPaths[item.File] = struct{}{}
		}
		if containsAny(lowerFile, "anti", "cheat", "guard", "integrity", "tamper", "scanner", "telemetry") {
			signals.SecurityPaths[item.File] = struct{}{}
		}
	}
	for _, item := range snapshot.UnrealSettings {
		if strings.TrimSpace(item.SourceFile) != "" {
			signals.AssetPaths[item.SourceFile] = struct{}{}
			signals.StartupPaths[item.SourceFile] = struct{}{}
		}
	}
	return signals
}

func classifySemanticShardFile(file ScannedFile, signals semanticShardSignals) string {
	if _, ok := signals.StartupPaths[file.Path]; ok {
		return "startup"
	}
	if _, ok := signals.BuildPaths[file.Path]; ok {
		return "build_graph"
	}
	if _, ok := signals.NetworkPaths[file.Path]; ok {
		return "unreal_network"
	}
	if _, ok := signals.UIPaths[file.Path]; ok {
		return "unreal_ui"
	}
	if _, ok := signals.AbilityPaths[file.Path]; ok {
		return "unreal_ability"
	}
	if _, ok := signals.AssetPaths[file.Path]; ok {
		return "asset_config"
	}
	if _, ok := signals.SecurityPaths[file.Path]; ok {
		return "integrity_security"
	}
	if _, ok := signals.GameplayPaths[file.Path]; ok {
		return "unreal_gameplay"
	}
	lower := strings.ToLower(file.Path)
	switch {
	case file.IsEntrypoint || containsAny(lower, "main", "bootstrap", "startup", "gameinstance", "gamemode"):
		return "startup"
	case file.IsManifest || containsAny(lower, ".uproject", ".uplugin", ".build.cs", ".target.cs") || (strings.Contains(lower, "/config/") && strings.HasSuffix(lower, ".ini")):
		return "build_graph"
	case containsAny(lower, "replication", "replicated", "rpc", "net"):
		return "unreal_network"
	case containsAny(lower, "widget", "hud", "ui", "umg"):
		return "unreal_ui"
	case containsAny(lower, "ability", "attributeset", "effect"):
		return "unreal_ability"
	case strings.HasSuffix(lower, ".ini") || containsAny(lower, "asset", "content", "defaultengine", "defaultgame"):
		return "asset_config"
	case containsAny(lower, "anti", "cheat", "guard", "integrity", "tamper", "scanner", "memory", "telemetry"):
		return "integrity_security"
	case containsAny(lower, "playercontroller", "character", "pawn", "subsystem", "gameplay"):
		return "unreal_gameplay"
	default:
		return ""
	}
}

func filterSnapshotForUnassignedFiles(snapshot ProjectSnapshot, assigned map[string]struct{}) ProjectSnapshot {
	filtered := snapshot
	filtered.Files = nil
	filtered.FilesByPath = map[string]ScannedFile{}
	filtered.FilesByDirectory = map[string][]ScannedFile{}
	filtered.Directories = nil
	filtered.TotalFiles = 0
	filtered.TotalLines = 0
	for _, file := range snapshot.Files {
		if _, ok := assigned[file.Path]; ok {
			continue
		}
		filtered.Files = append(filtered.Files, file)
		filtered.FilesByPath[file.Path] = file
		filtered.FilesByDirectory[file.Directory] = append(filtered.FilesByDirectory[file.Directory], file)
		filtered.TotalFiles++
		filtered.TotalLines += file.LineCount
	}
	for dir := range filtered.FilesByDirectory {
		filtered.Directories = append(filtered.Directories, dir)
	}
	sort.Strings(filtered.Directories)
	return filtered
}
