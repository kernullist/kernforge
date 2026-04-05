package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type SemanticIndexedFile struct {
	Path            string   `json:"path"`
	Directory       string   `json:"directory,omitempty"`
	Extension       string   `json:"extension,omitempty"`
	LineCount       int      `json:"line_count,omitempty"`
	IsManifest      bool     `json:"is_manifest,omitempty"`
	IsEntrypoint    bool     `json:"is_entrypoint,omitempty"`
	ImportanceScore int      `json:"importance_score,omitempty"`
	Tags            []string `json:"tags,omitempty"`
}

type SemanticSymbol struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Language   string   `json:"language,omitempty"`
	File       string   `json:"file,omitempty"`
	Container  string   `json:"container,omitempty"`
	Module     string   `json:"module,omitempty"`
	BaseSymbol string   `json:"base_symbol,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

type SemanticReference struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

type SemanticBuildEdge struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Type   string   `json:"type"`
	Files  []string `json:"files,omitempty"`
}

type UnrealSemanticNode struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Module     string            `json:"module,omitempty"`
	File       string            `json:"file,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type UnrealSemanticEdge struct {
	Source     string            `json:"source"`
	Target     string            `json:"target"`
	Type       string            `json:"type"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type UnrealSemanticGraph struct {
	RunID       string               `json:"run_id"`
	Goal        string               `json:"goal"`
	Root        string               `json:"root"`
	GeneratedAt time.Time            `json:"generated_at"`
	Nodes       []UnrealSemanticNode `json:"nodes,omitempty"`
	Edges       []UnrealSemanticEdge `json:"edges,omitempty"`
}

type SemanticIndex struct {
	RunID        string                `json:"run_id"`
	Goal         string                `json:"goal"`
	Root         string                `json:"root"`
	GeneratedAt  time.Time             `json:"generated_at"`
	Files        []SemanticIndexedFile `json:"files,omitempty"`
	Symbols      []SemanticSymbol      `json:"symbols,omitempty"`
	References   []SemanticReference   `json:"references,omitempty"`
	BuildEdges   []SemanticBuildEdge   `json:"build_edges,omitempty"`
	RuntimeEdges []RuntimeEdge         `json:"runtime_edges,omitempty"`
	ProjectEdges []ProjectEdge         `json:"project_edges,omitempty"`
	PrimaryStart string                `json:"primary_startup,omitempty"`
	UnrealGraph  UnrealSemanticGraph   `json:"unreal_graph,omitempty"`
}

func buildSemanticIndex(snapshot ProjectSnapshot, goal string, runID string, unrealGraph UnrealSemanticGraph) SemanticIndex {
	files := make([]SemanticIndexedFile, 0, len(snapshot.Files))
	references := []SemanticReference{}
	buildEdges := []SemanticBuildEdge{}
	symbols := []SemanticSymbol{}
	referenceSeen := map[string]struct{}{}
	buildSeen := map[string]struct{}{}
	symbolSeen := map[string]struct{}{}

	for _, file := range snapshot.Files {
		tags := []string{}
		if file.IsManifest {
			tags = append(tags, "manifest")
		}
		if file.IsEntrypoint {
			tags = append(tags, "entrypoint")
		}
		if file.ImportanceScore > 0 {
			tags = append(tags, limitStrings(file.ImportanceReasons, 4)...)
		}
		files = append(files, SemanticIndexedFile{
			Path:            file.Path,
			Directory:       file.Directory,
			Extension:       file.Extension,
			LineCount:       file.LineCount,
			IsManifest:      file.IsManifest,
			IsEntrypoint:    file.IsEntrypoint,
			ImportanceScore: file.ImportanceScore,
			Tags:            analysisUniqueStrings(tags),
		})
		for _, imported := range analysisUniqueStrings(file.Imports) {
			key := file.Path + "|file_import|" + imported
			if _, ok := referenceSeen[key]; ok {
				continue
			}
			referenceSeen[key] = struct{}{}
			references = append(references, SemanticReference{
				Source: file.Path,
				Target: imported,
				Type:   "file_import",
			})
		}
	}

	for _, project := range snapshot.SolutionProjects {
		id := "project:" + strings.TrimSpace(project.Name)
		if _, ok := symbolSeen[id]; !ok {
			symbolSeen[id] = struct{}{}
			symbols = append(symbols, SemanticSymbol{
				ID:        id,
				Name:      project.Name,
				Kind:      "solution_project",
				File:      project.Path,
				Container: project.Directory,
				Tags:      analysisUniqueStrings(append([]string{project.Kind, project.OutputType}, project.EntryFiles...)),
			})
		}
		for _, ref := range analysisUniqueStrings(project.ProjectReferences) {
			key := project.Name + "|project_reference|" + ref
			if _, ok := buildSeen[key]; ok {
				continue
			}
			buildSeen[key] = struct{}{}
			buildEdges = append(buildEdges, SemanticBuildEdge{
				Source: "project:" + project.Name,
				Target: ref,
				Type:   "project_reference",
				Files:  []string{project.Path},
			})
		}
	}

	for _, module := range snapshot.UnrealModules {
		id := "module:" + strings.TrimSpace(module.Name)
		if _, ok := symbolSeen[id]; !ok {
			symbolSeen[id] = struct{}{}
			tags := []string{module.Kind}
			if strings.TrimSpace(module.Plugin) != "" {
				tags = append(tags, "plugin:"+module.Plugin)
			}
			symbols = append(symbols, SemanticSymbol{
				ID:        id,
				Name:      module.Name,
				Kind:      "unreal_module",
				Language:  "csharp_build",
				File:      module.Path,
				Container: module.Plugin,
				Module:    module.Name,
				Tags:      analysisUniqueStrings(tags),
			})
		}
		for _, dep := range analysisUniqueStrings(append(append([]string{}, module.PublicDependencies...), append(module.PrivateDependencies, module.DynamicallyLoaded...)...)) {
			key := module.Name + "|module_dependency|" + dep
			if _, ok := buildSeen[key]; ok {
				continue
			}
			buildSeen[key] = struct{}{}
			buildEdges = append(buildEdges, SemanticBuildEdge{
				Source: "module:" + module.Name,
				Target: dep,
				Type:   "module_dependency",
				Files:  []string{module.Path},
			})
		}
	}

	for _, item := range snapshot.UnrealTypes {
		id := "type:" + strings.TrimSpace(item.Name)
		if _, ok := symbolSeen[id]; ok {
			continue
		}
		symbolSeen[id] = struct{}{}
		tags := append([]string{}, item.Specifiers...)
		if strings.TrimSpace(item.GameplayRole) != "" {
			tags = append(tags, "role:"+item.GameplayRole)
		}
		symbols = append(symbols, SemanticSymbol{
			ID:         id,
			Name:       item.Name,
			Kind:       strings.ToLower(item.Kind),
			Language:   "cpp",
			File:       item.File,
			Module:     item.Module,
			BaseSymbol: item.BaseClass,
			Tags:       analysisUniqueStrings(tags),
		})
	}

	sort.Slice(files, func(i int, j int) bool {
		return files[i].Path < files[j].Path
	})
	sort.Slice(symbols, func(i int, j int) bool {
		return symbols[i].ID < symbols[j].ID
	})
	sort.Slice(references, func(i int, j int) bool {
		if references[i].Source == references[j].Source {
			if references[i].Type == references[j].Type {
				return references[i].Target < references[j].Target
			}
			return references[i].Type < references[j].Type
		}
		return references[i].Source < references[j].Source
	})
	sort.Slice(buildEdges, func(i int, j int) bool {
		if buildEdges[i].Source == buildEdges[j].Source {
			if buildEdges[i].Type == buildEdges[j].Type {
				return buildEdges[i].Target < buildEdges[j].Target
			}
			return buildEdges[i].Type < buildEdges[j].Type
		}
		return buildEdges[i].Source < buildEdges[j].Source
	})

	return SemanticIndex{
		RunID:        runID,
		Goal:         goal,
		Root:         snapshot.Root,
		GeneratedAt:  snapshot.GeneratedAt,
		Files:        files,
		Symbols:      symbols,
		References:   references,
		BuildEdges:   buildEdges,
		RuntimeEdges: append([]RuntimeEdge(nil), snapshot.RuntimeEdges...),
		ProjectEdges: append([]ProjectEdge(nil), snapshot.ProjectEdges...),
		PrimaryStart: snapshot.PrimaryStartup,
		UnrealGraph:  unrealGraph,
	}
}

func buildUnrealSemanticGraph(snapshot ProjectSnapshot, goal string, runID string) UnrealSemanticGraph {
	graph := UnrealSemanticGraph{
		RunID:       runID,
		Goal:        goal,
		Root:        snapshot.Root,
		GeneratedAt: snapshot.GeneratedAt,
	}
	nodeSeen := map[string]struct{}{}
	edgeSeen := map[string]struct{}{}
	addNode := func(node UnrealSemanticNode) {
		node.ID = strings.TrimSpace(node.ID)
		node.Kind = strings.TrimSpace(node.Kind)
		node.Name = strings.TrimSpace(node.Name)
		if node.ID == "" || node.Kind == "" || node.Name == "" {
			return
		}
		if _, ok := nodeSeen[node.ID]; ok {
			return
		}
		nodeSeen[node.ID] = struct{}{}
		graph.Nodes = append(graph.Nodes, node)
	}
	addEdge := func(edge UnrealSemanticEdge) {
		edge.Source = strings.TrimSpace(edge.Source)
		edge.Target = strings.TrimSpace(edge.Target)
		edge.Type = strings.TrimSpace(edge.Type)
		if edge.Source == "" || edge.Target == "" || edge.Type == "" {
			return
		}
		key := edge.Source + "|" + edge.Type + "|" + edge.Target
		if _, ok := edgeSeen[key]; ok {
			return
		}
		edgeSeen[key] = struct{}{}
		graph.Edges = append(graph.Edges, edge)
	}

	for _, project := range snapshot.UnrealProjects {
		projectID := "uproject:" + project.Name
		addNode(UnrealSemanticNode{
			ID:   projectID,
			Kind: "uproject",
			Name: project.Name,
			File: project.Path,
		})
		for _, module := range analysisUniqueStrings(project.Modules) {
			moduleID := "module:" + module
			addNode(UnrealSemanticNode{
				ID:     moduleID,
				Kind:   "module",
				Name:   module,
				Module: module,
			})
			addEdge(UnrealSemanticEdge{Source: projectID, Target: moduleID, Type: "declares"})
		}
		for _, plugin := range analysisUniqueStrings(project.Plugins) {
			pluginID := "plugin:" + plugin
			addNode(UnrealSemanticNode{
				ID:   pluginID,
				Kind: "plugin",
				Name: plugin,
			})
			addEdge(UnrealSemanticEdge{Source: projectID, Target: pluginID, Type: "loads"})
		}
	}

	for _, plugin := range snapshot.UnrealPlugins {
		pluginID := "plugin:" + plugin.Name
		addNode(UnrealSemanticNode{
			ID:   pluginID,
			Kind: "plugin",
			Name: plugin.Name,
			File: plugin.Path,
			Attributes: map[string]string{
				"enabled_by_default": fmt.Sprintf("%t", plugin.EnabledByDefault),
			},
		})
		for _, module := range analysisUniqueStrings(plugin.Modules) {
			moduleID := "module:" + module
			addNode(UnrealSemanticNode{
				ID:     moduleID,
				Kind:   "module",
				Name:   module,
				Module: module,
			})
			addEdge(UnrealSemanticEdge{Source: pluginID, Target: moduleID, Type: "declares"})
		}
	}

	for _, target := range snapshot.UnrealTargets {
		targetID := "target:" + target.Name
		addNode(UnrealSemanticNode{
			ID:   targetID,
			Kind: "target",
			Name: target.Name,
			File: target.Path,
			Attributes: map[string]string{
				"target_type": target.TargetType,
			},
		})
		for _, module := range analysisUniqueStrings(target.Modules) {
			moduleID := "module:" + module
			addNode(UnrealSemanticNode{
				ID:     moduleID,
				Kind:   "module",
				Name:   module,
				Module: module,
			})
			addEdge(UnrealSemanticEdge{Source: targetID, Target: moduleID, Type: "depends_on"})
		}
	}

	for _, module := range snapshot.UnrealModules {
		moduleID := "module:" + module.Name
		attrs := map[string]string{}
		if strings.TrimSpace(module.Kind) != "" {
			attrs["module_kind"] = module.Kind
		}
		if strings.TrimSpace(module.Plugin) != "" {
			attrs["plugin"] = module.Plugin
		}
		addNode(UnrealSemanticNode{
			ID:         moduleID,
			Kind:       "module",
			Name:       module.Name,
			Module:     module.Name,
			File:       module.Path,
			Attributes: attrs,
		})
		for _, dep := range analysisUniqueStrings(module.PublicDependencies) {
			addEdge(UnrealSemanticEdge{Source: moduleID, Target: "module:" + dep, Type: "depends_on", Attributes: map[string]string{"visibility": "public"}})
		}
		for _, dep := range analysisUniqueStrings(module.PrivateDependencies) {
			addEdge(UnrealSemanticEdge{Source: moduleID, Target: "module:" + dep, Type: "depends_on", Attributes: map[string]string{"visibility": "private"}})
		}
		for _, dep := range analysisUniqueStrings(module.DynamicallyLoaded) {
			addEdge(UnrealSemanticEdge{Source: moduleID, Target: "module:" + dep, Type: "loads"})
		}
	}

	for _, item := range snapshot.UnrealTypes {
		typeID := "type:" + item.Name
		addNode(UnrealSemanticNode{
			ID:     typeID,
			Kind:   strings.ToLower(item.Kind),
			Name:   item.Name,
			Module: item.Module,
			File:   item.File,
			Attributes: map[string]string{
				"base_class":    item.BaseClass,
				"gameplay_role": item.GameplayRole,
			},
		})
		if strings.TrimSpace(item.Module) != "" {
			addEdge(UnrealSemanticEdge{Source: "module:" + item.Module, Target: typeID, Type: "declares"})
		}
		if strings.TrimSpace(item.BaseClass) != "" {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "type:" + item.BaseClass, Type: "inherits_from"})
		}
		if strings.TrimSpace(item.GameInstanceClass) != "" {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "type:" + item.GameInstanceClass, Type: "owns"})
		}
		if strings.TrimSpace(item.GameModeClass) != "" {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "type:" + item.GameModeClass, Type: "owns"})
		}
		if strings.TrimSpace(item.PlayerControllerClass) != "" {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "type:" + item.PlayerControllerClass, Type: "owns"})
		}
		if strings.TrimSpace(item.DefaultPawnClass) != "" {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "type:" + item.DefaultPawnClass, Type: "spawns"})
		}
		if strings.TrimSpace(item.HUDClass) != "" {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "type:" + item.HUDClass, Type: "creates_widget"})
		}
	}

	for _, item := range snapshot.UnrealNetwork {
		typeID := "type:" + item.TypeName
		for _, rpc := range analysisUniqueStrings(item.ServerRPCs) {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "rpc:" + rpc, Type: "rpc_server"})
		}
		for _, rpc := range analysisUniqueStrings(item.ClientRPCs) {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "rpc:" + rpc, Type: "rpc_client"})
		}
		for _, rpc := range analysisUniqueStrings(item.MulticastRPCs) {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "rpc:" + rpc, Type: "rpc_multicast"})
		}
		for _, prop := range analysisUniqueStrings(item.ReplicatedProperties) {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "property:" + prop, Type: "replicates"})
		}
		for _, prop := range analysisUniqueStrings(item.RepNotifyProperties) {
			addEdge(UnrealSemanticEdge{Source: typeID, Target: "property:" + prop, Type: "replicates", Attributes: map[string]string{"notify": "true"}})
		}
	}

	for _, item := range snapshot.UnrealAssets {
		ownerID := "type:" + firstNonBlankAnalysisString(item.OwnerName, item.File)
		for _, asset := range analysisUniqueStrings(item.CanonicalTargets) {
			addNode(UnrealSemanticNode{ID: "asset:" + asset, Kind: "asset", Name: asset})
			addEdge(UnrealSemanticEdge{Source: ownerID, Target: "asset:" + asset, Type: "references_asset"})
		}
		for _, key := range analysisUniqueStrings(item.ConfigKeys) {
			addNode(UnrealSemanticNode{ID: "config:" + key, Kind: "config_key", Name: key})
			addEdge(UnrealSemanticEdge{Source: ownerID, Target: "config:" + key, Type: "configured_by"})
		}
	}

	for _, item := range snapshot.UnrealSystems {
		systemName := firstNonBlankAnalysisString(item.System, item.OwnerName)
		systemID := "system:" + systemName
		addNode(UnrealSemanticNode{
			ID:     systemID,
			Kind:   "system",
			Name:   systemName,
			Module: item.Module,
			File:   item.File,
		})
		if strings.TrimSpace(item.OwnerName) != "" {
			addEdge(UnrealSemanticEdge{Source: "type:" + item.OwnerName, Target: systemID, Type: "registered_in"})
		}
		for _, widget := range analysisUniqueStrings(item.Widgets) {
			addEdge(UnrealSemanticEdge{Source: systemID, Target: "asset:" + widget, Type: "creates_widget"})
		}
		for _, action := range analysisUniqueStrings(item.Actions) {
			addEdge(UnrealSemanticEdge{Source: systemID, Target: "input_action:" + action, Type: "binds_input"})
		}
		for _, ability := range analysisUniqueStrings(item.Abilities) {
			addEdge(UnrealSemanticEdge{Source: systemID, Target: "ability:" + ability, Type: "owns"})
		}
		for _, effect := range analysisUniqueStrings(item.Effects) {
			addEdge(UnrealSemanticEdge{Source: systemID, Target: "effect:" + effect, Type: "owns"})
		}
	}

	for _, item := range snapshot.UnrealSettings {
		sourceID := "settings:" + item.SourceFile
		addNode(UnrealSemanticNode{
			ID:   sourceID,
			Kind: "settings",
			Name: item.SourceFile,
			File: item.SourceFile,
		})
		if strings.TrimSpace(item.GameDefaultMap) != "" {
			addEdge(UnrealSemanticEdge{Source: sourceID, Target: "asset:" + item.GameDefaultMap, Type: "configured_by"})
		}
		if strings.TrimSpace(item.GlobalDefaultGameMode) != "" {
			addEdge(UnrealSemanticEdge{Source: sourceID, Target: "type:" + item.GlobalDefaultGameMode, Type: "configured_by"})
		}
		if strings.TrimSpace(item.GameInstanceClass) != "" {
			addEdge(UnrealSemanticEdge{Source: sourceID, Target: "type:" + item.GameInstanceClass, Type: "configured_by"})
		}
	}

	sort.Slice(graph.Nodes, func(i int, j int) bool {
		return graph.Nodes[i].ID < graph.Nodes[j].ID
	})
	sort.Slice(graph.Edges, func(i int, j int) bool {
		if graph.Edges[i].Source == graph.Edges[j].Source {
			if graph.Edges[i].Type == graph.Edges[j].Type {
				return graph.Edges[i].Target < graph.Edges[j].Target
			}
			return graph.Edges[i].Type < graph.Edges[j].Type
		}
		return graph.Edges[i].Source < graph.Edges[j].Source
	})
	return graph
}
