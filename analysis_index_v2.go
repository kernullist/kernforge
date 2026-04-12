package main

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type FileRecord struct {
	Path            string   `json:"path"`
	Directory       string   `json:"directory,omitempty"`
	Extension       string   `json:"extension,omitempty"`
	Language        string   `json:"language,omitempty"`
	LineCount       int      `json:"line_count,omitempty"`
	IsManifest      bool     `json:"is_manifest,omitempty"`
	IsEntrypoint    bool     `json:"is_entrypoint,omitempty"`
	ImportanceScore int      `json:"importance_score,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	ModuleHints     []string `json:"module_hints,omitempty"`
}

type SymbolRecord struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	CanonicalName     string            `json:"canonical_name,omitempty"`
	Kind              string            `json:"kind"`
	Language          string            `json:"language,omitempty"`
	File              string            `json:"file,omitempty"`
	Module            string            `json:"module,omitempty"`
	ContainerSymbolID string            `json:"container_symbol_id,omitempty"`
	BuildContextID    string            `json:"build_context_id,omitempty"`
	BaseSymbolID      string            `json:"base_symbol_id,omitempty"`
	Tags              []string          `json:"tags,omitempty"`
	Attributes        map[string]string `json:"attributes,omitempty"`
}

type SymbolOccurrence struct {
	SymbolID string `json:"symbol_id"`
	File     string `json:"file"`
	Role     string `json:"role"`
}

type ReferenceRecord struct {
	SourceID   string   `json:"source_id,omitempty"`
	SourceFile string   `json:"source_file,omitempty"`
	TargetID   string   `json:"target_id,omitempty"`
	TargetPath string   `json:"target_path,omitempty"`
	Type       string   `json:"type"`
	Evidence   []string `json:"evidence,omitempty"`
}

type CallEdge struct {
	SourceID string   `json:"source_id"`
	TargetID string   `json:"target_id"`
	Type     string   `json:"type"`
	Evidence []string `json:"evidence,omitempty"`
}

type InheritanceEdge struct {
	SourceID string   `json:"source_id"`
	TargetID string   `json:"target_id"`
	Evidence []string `json:"evidence,omitempty"`
}

type BuildOwnershipEdge struct {
	SourceID string   `json:"source_id"`
	TargetID string   `json:"target_id"`
	Type     string   `json:"type"`
	Evidence []string `json:"evidence,omitempty"`
}

type GeneratedCodeEdge struct {
	SourceFile string   `json:"source_file"`
	TargetID   string   `json:"target_id"`
	Type       string   `json:"type"`
	Evidence   []string `json:"evidence,omitempty"`
}

type OverlayEdge struct {
	SourceID string   `json:"source_id"`
	TargetID string   `json:"target_id"`
	Type     string   `json:"type"`
	Domain   string   `json:"domain"`
	Evidence []string `json:"evidence,omitempty"`
}

type SemanticIndexV2 struct {
	RunID               string               `json:"run_id"`
	Goal                string               `json:"goal"`
	Root                string               `json:"root"`
	GeneratedAt         time.Time            `json:"generated_at"`
	Files               []FileRecord         `json:"files,omitempty"`
	Symbols             []SymbolRecord       `json:"symbols,omitempty"`
	Occurrences         []SymbolOccurrence   `json:"occurrences,omitempty"`
	References          []ReferenceRecord    `json:"references,omitempty"`
	CallEdges           []CallEdge           `json:"call_edges,omitempty"`
	InheritanceEdges    []InheritanceEdge    `json:"inheritance_edges,omitempty"`
	BuildOwnershipEdges []BuildOwnershipEdge `json:"build_ownership_edges,omitempty"`
	GeneratedCodeEdges  []GeneratedCodeEdge  `json:"generated_code_edges,omitempty"`
	OverlayEdges        []OverlayEdge        `json:"overlay_edges,omitempty"`
	PrimaryStartup      string               `json:"primary_startup,omitempty"`
	QueryModes          []string             `json:"query_modes,omitempty"`
}

func analysisLanguageForExtension(ext string) string {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hh", ".inl":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".jsx", ".ts", ".tsx":
		return "typescript"
	case ".json", ".toml", ".yaml", ".yml", ".ini", ".xml":
		return "config"
	case ".md":
		return "documentation"
	default:
		return ""
	}
}

func analysisLanguageForNodeKind(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "uproject", "plugin", "target", "module", "settings":
		return "build_meta"
	case "uclass", "ustruct", "uenum", "uinterface", "type", "rpc", "property", "system":
		return "cpp"
	case "asset", "config_key", "generated_header":
		return "content_meta"
	case "input_action", "ability", "effect":
		return "gameplay_meta"
	default:
		return ""
	}
}

func tagsForNodeKind(kind string, attrs map[string]string) []string {
	tags := []string{strings.TrimSpace(strings.ToLower(kind))}
	for key, value := range attrs {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		tags = append(tags, key+":"+trimmed)
	}
	return analysisUniqueStrings(tags)
}

func buildSyntheticV2Symbol(symbolID string, snapshot ProjectSnapshot) (SymbolRecord, bool) {
	symbolID = strings.TrimSpace(symbolID)
	if symbolID == "" {
		return SymbolRecord{}, false
	}
	switch {
	case strings.HasPrefix(symbolID, "project:"):
		name := strings.TrimPrefix(symbolID, "project:")
		for _, item := range snapshot.SolutionProjects {
			if strings.EqualFold(item.Name, name) {
				return SymbolRecord{
					ID:             symbolID,
					Name:           item.Name,
					CanonicalName:  symbolID,
					Kind:           "solution_project",
					Language:       "build_meta",
					File:           item.Path,
					BuildContextID: symbolID,
					Tags:           analysisUniqueStrings(append([]string{item.Kind, item.OutputType}, item.EntryFiles...)),
				}, true
			}
		}
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "solution_project", Language: "build_meta"}, true
	case strings.HasPrefix(symbolID, "module:"):
		name := strings.TrimPrefix(symbolID, "module:")
		for _, item := range snapshot.UnrealModules {
			if strings.EqualFold(item.Name, name) {
				return SymbolRecord{
					ID:             symbolID,
					Name:           item.Name,
					CanonicalName:  symbolID,
					Kind:           "module",
					Language:       "build_meta",
					File:           item.Path,
					Module:         item.Name,
					BuildContextID: symbolID,
					Tags:           analysisUniqueStrings(append([]string{item.Kind}, item.PublicDependencies...)),
				}, true
			}
		}
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "module", Language: "build_meta", BuildContextID: symbolID}, true
	case strings.HasPrefix(symbolID, "type:"):
		name := strings.TrimPrefix(symbolID, "type:")
		for _, item := range snapshot.UnrealTypes {
			if strings.EqualFold(item.Name, name) {
				baseSymbolID := ""
				if strings.TrimSpace(item.BaseClass) != "" {
					baseSymbolID = "type:" + item.BaseClass
				}
				tags := append([]string{}, item.Specifiers...)
				if strings.TrimSpace(item.GameplayRole) != "" {
					tags = append(tags, "role:"+item.GameplayRole)
				}
				return SymbolRecord{
					ID:            symbolID,
					Name:          item.Name,
					CanonicalName: symbolID,
					Kind:          strings.ToLower(item.Kind),
					Language:      "cpp",
					File:          item.File,
					Module:        item.Module,
					BaseSymbolID:  baseSymbolID,
					Tags:          analysisUniqueStrings(tags),
				}, true
			}
		}
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "type", Language: "cpp"}, true
	case strings.HasPrefix(symbolID, "uproject:"):
		name := strings.TrimPrefix(symbolID, "uproject:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "uproject", Language: "build_meta", BuildContextID: symbolID}, true
	case strings.HasPrefix(symbolID, "plugin:"):
		name := strings.TrimPrefix(symbolID, "plugin:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "plugin", Language: "build_meta", BuildContextID: symbolID}, true
	case strings.HasPrefix(symbolID, "target:"):
		name := strings.TrimPrefix(symbolID, "target:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "target", Language: "build_meta", BuildContextID: symbolID}, true
	case strings.HasPrefix(symbolID, "rpc:"):
		name := strings.TrimPrefix(symbolID, "rpc:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "rpc", Language: "cpp"}, true
	case strings.HasPrefix(symbolID, "property:"):
		name := strings.TrimPrefix(symbolID, "property:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "property", Language: "cpp"}, true
	case strings.HasPrefix(symbolID, "asset:"):
		name := strings.TrimPrefix(symbolID, "asset:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "asset", Language: "content_meta"}, true
	case strings.HasPrefix(symbolID, "config:"):
		name := strings.TrimPrefix(symbolID, "config:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "config_key", Language: "content_meta"}, true
	case strings.HasPrefix(symbolID, "system:"):
		name := strings.TrimPrefix(symbolID, "system:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "system", Language: "cpp"}, true
	case strings.HasPrefix(symbolID, "settings:"):
		name := strings.TrimPrefix(symbolID, "settings:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "settings", Language: "build_meta", File: name}, true
	case strings.HasPrefix(symbolID, "input_action:"):
		name := strings.TrimPrefix(symbolID, "input_action:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "input_action", Language: "gameplay_meta"}, true
	case strings.HasPrefix(symbolID, "ability:"):
		name := strings.TrimPrefix(symbolID, "ability:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "ability", Language: "gameplay_meta"}, true
	case strings.HasPrefix(symbolID, "effect:"):
		name := strings.TrimPrefix(symbolID, "effect:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "effect", Language: "gameplay_meta"}, true
	case strings.HasPrefix(symbolID, "generated_header:"):
		name := strings.TrimPrefix(symbolID, "generated_header:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "generated_header", Language: "cpp_generated", Tags: []string{"unreal_generated"}}, true
	case strings.HasPrefix(symbolID, "entity:"):
		name := strings.TrimPrefix(symbolID, "entity:")
		return SymbolRecord{ID: symbolID, Name: name, CanonicalName: symbolID, Kind: "entity"}, true
	default:
		return SymbolRecord{}, false
	}
}

func semanticIndexV2EntityID(snapshot ProjectSnapshot, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	for _, item := range snapshot.SolutionProjects {
		if strings.EqualFold(item.Name, name) {
			return "project:" + item.Name
		}
	}
	for _, item := range snapshot.UnrealProjects {
		if strings.EqualFold(item.Name, name) {
			return "uproject:" + item.Name
		}
	}
	for _, item := range snapshot.UnrealPlugins {
		if strings.EqualFold(item.Name, name) {
			return "plugin:" + item.Name
		}
	}
	for _, item := range snapshot.UnrealTargets {
		if strings.EqualFold(item.Name, name) {
			return "target:" + item.Name
		}
	}
	for _, item := range snapshot.UnrealModules {
		if strings.EqualFold(item.Name, name) {
			return "module:" + item.Name
		}
	}
	for _, item := range snapshot.UnrealTypes {
		if strings.EqualFold(item.Name, name) {
			return "type:" + item.Name
		}
	}
	return "entity:" + name
}

func projectEdgeSuggestsSecurity(edge ProjectEdge) bool {
	corpus := strings.ToLower(strings.TrimSpace(edge.Type) + " " + strings.Join(edge.Evidence, " "))
	if containsAny(corpus, "security", "integrity", "anti_tamper", "tamper", "rpc_server") {
		return true
	}
	for key, value := range edge.Attributes {
		joined := strings.ToLower(strings.TrimSpace(key) + " " + strings.TrimSpace(value))
		if containsAny(joined, "security", "integrity", "anti_tamper", "tamper") {
			return true
		}
	}
	return false
}

func projectEdgeSuggestsContent(edge ProjectEdge) bool {
	corpus := strings.ToLower(strings.TrimSpace(edge.Type) + " " + strings.Join(edge.Evidence, " "))
	if containsAny(corpus, "config", "asset", "configured_by") {
		return true
	}
	for key, value := range edge.Attributes {
		joined := strings.ToLower(strings.TrimSpace(key) + " " + strings.TrimSpace(value))
		if containsAny(joined, "config", "asset", "map", "game_mode") {
			return true
		}
	}
	return false
}

func buildSemanticIndexV2(snapshot ProjectSnapshot, goal string, runID string, unrealGraph UnrealSemanticGraph) SemanticIndexV2 {
	index := SemanticIndexV2{
		RunID:          runID,
		Goal:           goal,
		Root:           snapshot.Root,
		GeneratedAt:    snapshot.GeneratedAt,
		PrimaryStartup: snapshot.PrimaryStartup,
		QueryModes:     []string{"map", "trace", "impact", "security", "performance"},
	}

	symbolByID := map[string]SymbolRecord{}
	occurrenceSeen := map[string]struct{}{}
	referenceSeen := map[string]struct{}{}
	callSeen := map[string]struct{}{}
	inheritanceSeen := map[string]struct{}{}
	buildSeen := map[string]struct{}{}
	generatedSeen := map[string]struct{}{}
	overlaySeen := map[string]struct{}{}

	addSymbol := func(symbol SymbolRecord) {
		symbol.ID = strings.TrimSpace(symbol.ID)
		symbol.Name = strings.TrimSpace(symbol.Name)
		symbol.Kind = strings.TrimSpace(symbol.Kind)
		if symbol.ID == "" || symbol.Name == "" || symbol.Kind == "" {
			return
		}
		if strings.TrimSpace(symbol.CanonicalName) == "" {
			symbol.CanonicalName = symbol.ID
		}
		symbol.File = strings.TrimSpace(symbol.File)
		symbol.Module = strings.TrimSpace(symbol.Module)
		symbol.ContainerSymbolID = strings.TrimSpace(symbol.ContainerSymbolID)
		symbol.BuildContextID = strings.TrimSpace(symbol.BuildContextID)
		symbol.BaseSymbolID = strings.TrimSpace(symbol.BaseSymbolID)
		symbol.Tags = analysisUniqueStrings(symbol.Tags)
		if len(symbol.Attributes) == 0 {
			symbol.Attributes = nil
		}
		existing, ok := symbolByID[symbol.ID]
		if !ok {
			symbolByID[symbol.ID] = symbol
			return
		}
		if existing.File == "" {
			existing.File = symbol.File
		}
		if existing.Module == "" {
			existing.Module = symbol.Module
		}
		if existing.ContainerSymbolID == "" {
			existing.ContainerSymbolID = symbol.ContainerSymbolID
		}
		if existing.BuildContextID == "" {
			existing.BuildContextID = symbol.BuildContextID
		}
		if existing.BaseSymbolID == "" {
			existing.BaseSymbolID = symbol.BaseSymbolID
		}
		if existing.Language == "" {
			existing.Language = symbol.Language
		}
		existing.Tags = analysisUniqueStrings(append(existing.Tags, symbol.Tags...))
		if len(symbol.Attributes) > 0 {
			if existing.Attributes == nil {
				existing.Attributes = map[string]string{}
			}
			for key, value := range symbol.Attributes {
				if strings.TrimSpace(value) != "" {
					existing.Attributes[key] = value
				}
			}
		}
		symbolByID[symbol.ID] = existing
	}

	updateSymbol := func(symbolID string, fn func(*SymbolRecord)) {
		existing, ok := symbolByID[strings.TrimSpace(symbolID)]
		if !ok {
			return
		}
		fn(&existing)
		existing.Tags = analysisUniqueStrings(existing.Tags)
		if len(existing.Attributes) == 0 {
			existing.Attributes = nil
		}
		symbolByID[symbolID] = existing
	}

	addOccurrence := func(symbolID string, file string, role string) {
		symbolID = strings.TrimSpace(symbolID)
		file = strings.TrimSpace(file)
		role = strings.TrimSpace(role)
		if symbolID == "" || file == "" || role == "" {
			return
		}
		key := symbolID + "|" + file + "|" + role
		if _, ok := occurrenceSeen[key]; ok {
			return
		}
		occurrenceSeen[key] = struct{}{}
		index.Occurrences = append(index.Occurrences, SymbolOccurrence{
			SymbolID: symbolID,
			File:     file,
			Role:     role,
		})
	}

	addReference := func(record ReferenceRecord) {
		record.SourceID = strings.TrimSpace(record.SourceID)
		record.SourceFile = strings.TrimSpace(record.SourceFile)
		record.TargetID = strings.TrimSpace(record.TargetID)
		record.TargetPath = strings.TrimSpace(record.TargetPath)
		record.Type = strings.TrimSpace(record.Type)
		if record.Type == "" {
			return
		}
		key := record.SourceID + "|" + record.SourceFile + "|" + record.Type + "|" + record.TargetID + "|" + record.TargetPath
		if _, ok := referenceSeen[key]; ok {
			return
		}
		referenceSeen[key] = struct{}{}
		record.Evidence = analysisUniqueStrings(record.Evidence)
		index.References = append(index.References, record)
	}

	addCallEdge := func(edge CallEdge) {
		edge.SourceID = strings.TrimSpace(edge.SourceID)
		edge.TargetID = strings.TrimSpace(edge.TargetID)
		edge.Type = strings.TrimSpace(edge.Type)
		if edge.SourceID == "" || edge.TargetID == "" || edge.Type == "" {
			return
		}
		key := edge.SourceID + "|" + edge.Type + "|" + edge.TargetID
		if _, ok := callSeen[key]; ok {
			return
		}
		callSeen[key] = struct{}{}
		edge.Evidence = analysisUniqueStrings(edge.Evidence)
		index.CallEdges = append(index.CallEdges, edge)
	}

	addInheritanceEdge := func(edge InheritanceEdge) {
		edge.SourceID = strings.TrimSpace(edge.SourceID)
		edge.TargetID = strings.TrimSpace(edge.TargetID)
		if edge.SourceID == "" || edge.TargetID == "" {
			return
		}
		key := edge.SourceID + "|" + edge.TargetID
		if _, ok := inheritanceSeen[key]; ok {
			return
		}
		inheritanceSeen[key] = struct{}{}
		edge.Evidence = analysisUniqueStrings(edge.Evidence)
		index.InheritanceEdges = append(index.InheritanceEdges, edge)
	}

	addBuildEdge := func(edge BuildOwnershipEdge) {
		edge.SourceID = strings.TrimSpace(edge.SourceID)
		edge.TargetID = strings.TrimSpace(edge.TargetID)
		edge.Type = strings.TrimSpace(edge.Type)
		if edge.SourceID == "" || edge.TargetID == "" || edge.Type == "" {
			return
		}
		key := edge.SourceID + "|" + edge.Type + "|" + edge.TargetID
		if _, ok := buildSeen[key]; ok {
			return
		}
		buildSeen[key] = struct{}{}
		edge.Evidence = analysisUniqueStrings(edge.Evidence)
		index.BuildOwnershipEdges = append(index.BuildOwnershipEdges, edge)
	}

	addGeneratedEdge := func(edge GeneratedCodeEdge) {
		edge.SourceFile = strings.TrimSpace(edge.SourceFile)
		edge.TargetID = strings.TrimSpace(edge.TargetID)
		edge.Type = strings.TrimSpace(edge.Type)
		if edge.SourceFile == "" || edge.TargetID == "" || edge.Type == "" {
			return
		}
		key := edge.SourceFile + "|" + edge.Type + "|" + edge.TargetID
		if _, ok := generatedSeen[key]; ok {
			return
		}
		generatedSeen[key] = struct{}{}
		edge.Evidence = analysisUniqueStrings(edge.Evidence)
		index.GeneratedCodeEdges = append(index.GeneratedCodeEdges, edge)
	}

	addOverlayEdge := func(edge OverlayEdge) {
		edge.SourceID = strings.TrimSpace(edge.SourceID)
		edge.TargetID = strings.TrimSpace(edge.TargetID)
		edge.Type = strings.TrimSpace(edge.Type)
		edge.Domain = strings.TrimSpace(edge.Domain)
		if edge.SourceID == "" || edge.TargetID == "" || edge.Type == "" || edge.Domain == "" {
			return
		}
		key := edge.Domain + "|" + edge.SourceID + "|" + edge.Type + "|" + edge.TargetID
		if _, ok := overlaySeen[key]; ok {
			return
		}
		overlaySeen[key] = struct{}{}
		edge.Evidence = analysisUniqueStrings(edge.Evidence)
		index.OverlayEdges = append(index.OverlayEdges, edge)
	}

	ensureSymbol := func(symbolID string) {
		if strings.TrimSpace(symbolID) == "" {
			return
		}
		if _, ok := symbolByID[symbolID]; ok {
			return
		}
		if symbol, ok := buildSyntheticV2Symbol(symbolID, snapshot); ok {
			addSymbol(symbol)
		}
	}

	for _, file := range snapshot.Files {
		tags := []string{}
		if file.IsManifest {
			tags = append(tags, "manifest")
		}
		if file.IsEntrypoint {
			tags = append(tags, "entrypoint")
		}
		tags = append(tags, limitStrings(file.ImportanceReasons, 6)...)
		moduleHints := []string{}
		if module := unrealModuleForFile(snapshot, file.Path); strings.TrimSpace(module) != "" {
			moduleHints = append(moduleHints, module)
		}
		index.Files = append(index.Files, FileRecord{
			Path:            file.Path,
			Directory:       file.Directory,
			Extension:       file.Extension,
			Language:        analysisLanguageForExtension(file.Extension),
			LineCount:       file.LineCount,
			IsManifest:      file.IsManifest,
			IsEntrypoint:    file.IsEntrypoint,
			ImportanceScore: file.ImportanceScore,
			Tags:            analysisUniqueStrings(tags),
			ModuleHints:     analysisUniqueStrings(moduleHints),
		})
		for _, imported := range analysisUniqueStrings(file.Imports) {
			addReference(ReferenceRecord{
				SourceFile: file.Path,
				TargetPath: imported,
				Type:       "file_import",
				Evidence:   []string{file.Path},
			})
		}
	}

	for _, project := range snapshot.SolutionProjects {
		symbolID := "project:" + strings.TrimSpace(project.Name)
		addSymbol(SymbolRecord{
			ID:             symbolID,
			Name:           project.Name,
			CanonicalName:  symbolID,
			Kind:           "solution_project",
			Language:       "build_meta",
			File:           project.Path,
			BuildContextID: symbolID,
			Tags:           analysisUniqueStrings(append([]string{project.Kind, project.OutputType}, project.EntryFiles...)),
		})
		if strings.TrimSpace(project.Path) != "" {
			addOccurrence(symbolID, project.Path, "definition")
		}
	}

	for _, node := range unrealGraph.Nodes {
		buildContextID := ""
		switch node.Kind {
		case "uproject", "plugin", "target", "module":
			buildContextID = node.ID
		}
		addSymbol(SymbolRecord{
			ID:             node.ID,
			Name:           node.Name,
			CanonicalName:  node.ID,
			Kind:           node.Kind,
			Language:       analysisLanguageForNodeKind(node.Kind),
			File:           node.File,
			Module:         node.Module,
			BuildContextID: buildContextID,
			Tags:           tagsForNodeKind(node.Kind, node.Attributes),
			Attributes:     cloneStringMap(node.Attributes),
		})
		if strings.TrimSpace(node.File) != "" {
			addOccurrence(node.ID, node.File, "definition")
		}
	}

	for _, edge := range unrealGraph.Edges {
		ensureSymbol(edge.Source)
		ensureSymbol(edge.Target)
		switch edge.Type {
		case "inherits_from":
			addInheritanceEdge(InheritanceEdge{SourceID: edge.Source, TargetID: edge.Target})
			updateSymbol(edge.Source, func(symbol *SymbolRecord) {
				if symbol.BaseSymbolID == "" {
					symbol.BaseSymbolID = edge.Target
				}
			})
		case "declares", "depends_on", "loads", "owns":
			addBuildEdge(BuildOwnershipEdge{SourceID: edge.Source, TargetID: edge.Target, Type: edge.Type})
			if edge.Type == "declares" {
				updateSymbol(edge.Target, func(symbol *SymbolRecord) {
					if symbol.ContainerSymbolID == "" {
						symbol.ContainerSymbolID = edge.Source
					}
					if symbol.BuildContextID == "" {
						symbol.BuildContextID = edge.Source
					}
				})
			}
		case "rpc_server", "rpc_client", "rpc_multicast", "spawns", "creates_widget", "binds_input":
			addCallEdge(CallEdge{SourceID: edge.Source, TargetID: edge.Target, Type: edge.Type})
		case "references_asset", "configured_by", "registered_in":
			addReference(ReferenceRecord{SourceID: edge.Source, TargetID: edge.Target, Type: edge.Type})
		}

		switch edge.Type {
		case "rpc_server", "rpc_client", "rpc_multicast", "replicates":
			addOverlayEdge(OverlayEdge{SourceID: edge.Source, TargetID: edge.Target, Type: edge.Type, Domain: "authority_boundary"})
		case "references_asset", "configured_by":
			addOverlayEdge(OverlayEdge{SourceID: edge.Source, TargetID: edge.Target, Type: edge.Type, Domain: "content_boundary"})
		case "spawns", "creates_widget", "binds_input":
			addOverlayEdge(OverlayEdge{SourceID: edge.Source, TargetID: edge.Target, Type: edge.Type, Domain: "gameplay_surface"})
		}
	}

	for _, project := range snapshot.SolutionProjects {
		sourceID := "project:" + strings.TrimSpace(project.Name)
		for _, ref := range analysisUniqueStrings(project.ProjectReferences) {
			targetName := projectNameFromReference(snapshot.SolutionProjects, ref)
			targetID := "project:" + targetName
			if strings.TrimSpace(targetName) == "" {
				targetID = "entity:" + strings.TrimSpace(filepath.ToSlash(ref))
			}
			ensureSymbol(sourceID)
			ensureSymbol(targetID)
			addBuildEdge(BuildOwnershipEdge{
				SourceID: sourceID,
				TargetID: targetID,
				Type:     "project_reference",
				Evidence: []string{project.Path},
			})
		}
	}

	for _, edge := range snapshot.RuntimeEdges {
		sourceID := semanticIndexV2EntityID(snapshot, edge.Source)
		targetID := semanticIndexV2EntityID(snapshot, edge.Target)
		ensureSymbol(sourceID)
		ensureSymbol(targetID)
		addCallEdge(CallEdge{
			SourceID: sourceID,
			TargetID: targetID,
			Type:     "runtime_" + strings.TrimSpace(edge.Kind),
			Evidence: edge.Evidence,
		})
		if edge.Kind == "dynamic_load" || edge.Kind == "process_spawn" {
			addOverlayEdge(OverlayEdge{
				SourceID: sourceID,
				TargetID: targetID,
				Type:     "runtime_" + strings.TrimSpace(edge.Kind),
				Domain:   "tamper_surface",
				Evidence: edge.Evidence,
			})
		}
	}

	for _, edge := range snapshot.ProjectEdges {
		sourceID := semanticIndexV2EntityID(snapshot, edge.Source)
		targetID := semanticIndexV2EntityID(snapshot, edge.Target)
		ensureSymbol(sourceID)
		ensureSymbol(targetID)
		addReference(ReferenceRecord{
			SourceID: sourceID,
			TargetID: targetID,
			Type:     edge.Type,
			Evidence: edge.Evidence,
		})
		if projectEdgeSuggestsSecurity(edge) {
			addOverlayEdge(OverlayEdge{
				SourceID: sourceID,
				TargetID: targetID,
				Type:     edge.Type,
				Domain:   "security_boundary",
				Evidence: edge.Evidence,
			})
		}
		if projectEdgeSuggestsContent(edge) {
			addOverlayEdge(OverlayEdge{
				SourceID: sourceID,
				TargetID: targetID,
				Type:     edge.Type,
				Domain:   "content_boundary",
				Evidence: edge.Evidence,
			})
		}
	}

	for _, item := range snapshot.UnrealNetwork {
		typeID := "type:" + strings.TrimSpace(item.TypeName)
		if strings.TrimSpace(item.File) == "" {
			continue
		}
		ensureSymbol(typeID)
		addOccurrence(typeID, item.File, "network_surface")
	}

	for _, item := range snapshot.UnrealAssets {
		ownerID := "type:" + firstNonBlankAnalysisString(item.OwnerName, item.File)
		if strings.TrimSpace(item.File) == "" {
			continue
		}
		ensureSymbol(ownerID)
		addOccurrence(ownerID, item.File, "asset_binding")
	}

	for _, item := range snapshot.UnrealSystems {
		systemID := "system:" + firstNonBlankAnalysisString(item.System, item.OwnerName)
		if strings.TrimSpace(item.File) == "" {
			continue
		}
		ensureSymbol(systemID)
		addOccurrence(systemID, item.File, "system_definition")
		if strings.TrimSpace(item.OwnerName) != "" {
			ownerID := "type:" + item.OwnerName
			ensureSymbol(ownerID)
			addOccurrence(ownerID, item.File, "system_owner")
		}
	}

	for _, item := range snapshot.UnrealSettings {
		sourceID := "settings:" + strings.TrimSpace(item.SourceFile)
		if strings.TrimSpace(item.SourceFile) == "" {
			continue
		}
		ensureSymbol(sourceID)
		addOccurrence(sourceID, item.SourceFile, "config_source")
	}

	for _, item := range snapshot.UnrealTypes {
		lowerExt := strings.ToLower(filepath.Ext(item.File))
		if lowerExt != ".h" && lowerExt != ".hpp" {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(item.File), lowerExt)
		if strings.TrimSpace(base) == "" {
			continue
		}
		targetID := "generated_header:" + base + ".generated.h"
		addSymbol(SymbolRecord{
			ID:            targetID,
			Name:          base + ".generated.h",
			CanonicalName: targetID,
			Kind:          "generated_header",
			Language:      "cpp_generated",
			Tags:          []string{"unreal_generated"},
		})
		addGeneratedEdge(GeneratedCodeEdge{
			SourceFile: item.File,
			TargetID:   targetID,
			Type:       "uht_generated_header",
			Evidence:   []string{item.File},
		})
	}

	for _, symbol := range symbolByID {
		index.Symbols = append(index.Symbols, symbol)
	}
	sort.Slice(index.Files, func(i int, j int) bool {
		return index.Files[i].Path < index.Files[j].Path
	})
	sort.Slice(index.Symbols, func(i int, j int) bool {
		return index.Symbols[i].ID < index.Symbols[j].ID
	})
	sort.Slice(index.Occurrences, func(i int, j int) bool {
		if index.Occurrences[i].SymbolID == index.Occurrences[j].SymbolID {
			if index.Occurrences[i].File == index.Occurrences[j].File {
				return index.Occurrences[i].Role < index.Occurrences[j].Role
			}
			return index.Occurrences[i].File < index.Occurrences[j].File
		}
		return index.Occurrences[i].SymbolID < index.Occurrences[j].SymbolID
	})
	sort.Slice(index.References, func(i int, j int) bool {
		left := index.References[i].Type + "|" + index.References[i].SourceID + "|" + index.References[i].SourceFile + "|" + index.References[i].TargetID + "|" + index.References[i].TargetPath
		right := index.References[j].Type + "|" + index.References[j].SourceID + "|" + index.References[j].SourceFile + "|" + index.References[j].TargetID + "|" + index.References[j].TargetPath
		return left < right
	})
	sort.Slice(index.CallEdges, func(i int, j int) bool {
		left := index.CallEdges[i].SourceID + "|" + index.CallEdges[i].Type + "|" + index.CallEdges[i].TargetID
		right := index.CallEdges[j].SourceID + "|" + index.CallEdges[j].Type + "|" + index.CallEdges[j].TargetID
		return left < right
	})
	sort.Slice(index.InheritanceEdges, func(i int, j int) bool {
		left := index.InheritanceEdges[i].SourceID + "|" + index.InheritanceEdges[i].TargetID
		right := index.InheritanceEdges[j].SourceID + "|" + index.InheritanceEdges[j].TargetID
		return left < right
	})
	sort.Slice(index.BuildOwnershipEdges, func(i int, j int) bool {
		left := index.BuildOwnershipEdges[i].SourceID + "|" + index.BuildOwnershipEdges[i].Type + "|" + index.BuildOwnershipEdges[i].TargetID
		right := index.BuildOwnershipEdges[j].SourceID + "|" + index.BuildOwnershipEdges[j].Type + "|" + index.BuildOwnershipEdges[j].TargetID
		return left < right
	})
	sort.Slice(index.GeneratedCodeEdges, func(i int, j int) bool {
		left := index.GeneratedCodeEdges[i].SourceFile + "|" + index.GeneratedCodeEdges[i].Type + "|" + index.GeneratedCodeEdges[i].TargetID
		right := index.GeneratedCodeEdges[j].SourceFile + "|" + index.GeneratedCodeEdges[j].Type + "|" + index.GeneratedCodeEdges[j].TargetID
		return left < right
	})
	sort.Slice(index.OverlayEdges, func(i int, j int) bool {
		left := index.OverlayEdges[i].Domain + "|" + index.OverlayEdges[i].SourceID + "|" + index.OverlayEdges[i].Type + "|" + index.OverlayEdges[i].TargetID
		right := index.OverlayEdges[j].Domain + "|" + index.OverlayEdges[j].SourceID + "|" + index.OverlayEdges[j].Type + "|" + index.OverlayEdges[j].TargetID
		return left < right
	})
	return index
}

func hasSemanticIndexV2Data(index SemanticIndexV2) bool {
	return len(index.Files) > 0 ||
		len(index.Symbols) > 0 ||
		len(index.Occurrences) > 0 ||
		len(index.References) > 0 ||
		len(index.CallEdges) > 0 ||
		len(index.InheritanceEdges) > 0 ||
		len(index.BuildOwnershipEdges) > 0 ||
		len(index.GeneratedCodeEdges) > 0 ||
		len(index.OverlayEdges) > 0
}
