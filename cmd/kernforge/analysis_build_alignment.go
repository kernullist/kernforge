package main

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type BuildContextDiagnostic struct {
	Path     string `json:"path,omitempty"`
	Adapter  string `json:"adapter,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type CompilationCommandRecord struct {
	File           string   `json:"file"`
	Directory      string   `json:"directory,omitempty"`
	Command        string   `json:"command,omitempty"`
	Arguments      []string `json:"arguments,omitempty"`
	Output         string   `json:"output,omitempty"`
	Compiler       string   `json:"compiler,omitempty"`
	IncludePaths   []string `json:"include_paths,omitempty"`
	Defines        []string `json:"defines,omitempty"`
	ForceIncludes  []string `json:"force_includes,omitempty"`
	BuildContextID string   `json:"build_context_id,omitempty"`
	Source         string   `json:"source,omitempty"`
	SourceAdapter  string   `json:"source_adapter,omitempty"`
	Confidence     string   `json:"confidence,omitempty"`
}

type BuildContextRecord struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Kind          string                   `json:"kind"`
	Directory     string                   `json:"directory,omitempty"`
	Project       string                   `json:"project,omitempty"`
	Target        string                   `json:"target,omitempty"`
	Module        string                   `json:"module,omitempty"`
	Plugin        string                   `json:"plugin,omitempty"`
	Compiler      string                   `json:"compiler,omitempty"`
	Files         []string                 `json:"files,omitempty"`
	IncludePaths  []string                 `json:"include_paths,omitempty"`
	Defines       []string                 `json:"defines,omitempty"`
	ForceIncludes []string                 `json:"force_includes,omitempty"`
	Source        string                   `json:"source,omitempty"`
	SourceAdapter string                   `json:"source_adapter,omitempty"`
	Confidence    string                   `json:"confidence,omitempty"`
	Diagnostics   []BuildContextDiagnostic `json:"diagnostics,omitempty"`
}

type SemanticPathV2 struct {
	Reason string   `json:"reason,omitempty"`
	Nodes  []string `json:"nodes,omitempty"`
	Edges  []string `json:"edges,omitempty"`
	Score  int      `json:"score,omitempty"`
}

func enrichBuildAlignment(snapshot *ProjectSnapshot) {
	compileCommands := discoverCompileCommands(snapshot.Root)
	msbuildContexts, diagnostics := discoverMSBuildBuildContexts(*snapshot)
	contexts := buildSnapshotBuildContexts(*snapshot, compileCommands, msbuildContexts)
	snapshot.CompileCommands = compileCommands
	snapshot.BuildContexts = contexts
	snapshot.BuildDiagnostics = append(snapshot.BuildDiagnostics, diagnostics...)
}

func discoverCompileCommands(root string) []CompilationCommandRecord {
	type compileCommandEntry struct {
		Directory string   `json:"directory"`
		File      string   `json:"file"`
		Command   string   `json:"command"`
		Arguments []string `json:"arguments"`
		Output    string   `json:"output"`
	}

	commandFiles := []string{}
	seenFiles := map[string]struct{}{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := strings.ToLower(strings.TrimSpace(d.Name()))
			if name == ".git" || name == ".svn" || name == ".hg" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(strings.TrimSpace(d.Name()), "compile_commands.json") {
			return nil
		}
		normalized := filepath.Clean(path)
		if _, ok := seenFiles[normalized]; ok {
			return nil
		}
		seenFiles[normalized] = struct{}{}
		commandFiles = append(commandFiles, normalized)
		return nil
	})
	sort.Strings(commandFiles)

	records := []CompilationCommandRecord{}
	recordSeen := map[string]struct{}{}
	for _, commandFile := range commandFiles {
		data, err := os.ReadFile(commandFile)
		if err != nil {
			continue
		}
		entries := []compileCommandEntry{}
		if err := json.Unmarshal(data, &entries); err != nil {
			continue
		}
		for _, entry := range entries {
			normalizedFile := normalizeCompileCommandFile(root, entry.Directory, entry.File)
			if strings.TrimSpace(normalizedFile) == "" {
				continue
			}
			args := append([]string(nil), entry.Arguments...)
			if len(args) == 0 && strings.TrimSpace(entry.Command) != "" {
				args = splitAnalysisCommandLine(entry.Command)
			}
			directory := normalizeCompileCommandDirectory(root, entry.Directory)
			compiler := ""
			if len(args) > 0 {
				compiler = filepath.Base(strings.TrimSpace(args[0]))
			}
			includes, defines, forceIncludes := extractCompileCommandFlags(args)
			key := normalizedFile + "|" + directory + "|" + strings.Join(args, "\x00")
			if _, ok := recordSeen[key]; ok {
				continue
			}
			recordSeen[key] = struct{}{}
			records = append(records, CompilationCommandRecord{
				File:          normalizedFile,
				Directory:     directory,
				Command:       strings.TrimSpace(entry.Command),
				Arguments:     analysisUniqueStrings(args),
				Output:        strings.TrimSpace(entry.Output),
				Compiler:      strings.TrimSpace(compiler),
				IncludePaths:  includes,
				Defines:       defines,
				ForceIncludes: forceIncludes,
				Source:        filepath.ToSlash(relOrAbs(root, commandFile)),
				SourceAdapter: "compile_commands",
				Confidence:    "high",
			})
		}
	}
	sort.Slice(records, func(i int, j int) bool {
		if records[i].File == records[j].File {
			if records[i].Directory == records[j].Directory {
				return records[i].Source < records[j].Source
			}
			return records[i].Directory < records[j].Directory
		}
		return records[i].File < records[j].File
	})
	return records
}

type msbuildExtraction struct {
	Files         []string
	IncludePaths  []string
	Defines       []string
	ForceIncludes []string
	Imports       []string
	Diagnostics   []BuildContextDiagnostic
}

func discoverMSBuildBuildContexts(snapshot ProjectSnapshot) ([]BuildContextRecord, []BuildContextDiagnostic) {
	paths := []string{}
	projectByPath := map[string]SolutionProject{}
	for _, project := range snapshot.SolutionProjects {
		if strings.HasSuffix(strings.ToLower(project.Path), ".vcxproj") {
			paths = append(paths, project.Path)
			projectByPath[strings.ToLower(filepath.ToSlash(project.Path))] = project
		}
	}
	for _, path := range snapshot.ManifestFiles {
		if strings.HasSuffix(strings.ToLower(path), ".vcxproj") {
			paths = append(paths, path)
		}
	}
	for _, file := range snapshot.Files {
		switch strings.ToLower(strings.TrimSpace(file.Extension)) {
		case ".props", ".targets":
			paths = append(paths, file.Path)
		}
	}
	paths = analysisUniqueStrings(paths)
	sort.Strings(paths)

	contexts := []BuildContextRecord{}
	diagnostics := []BuildContextDiagnostic{}
	for _, path := range paths {
		project := projectByPath[strings.ToLower(filepath.ToSlash(path))]
		ctx, extraDiagnostics, ok := buildMSBuildContext(snapshot, path, project)
		diagnostics = append(diagnostics, extraDiagnostics...)
		if ok {
			contexts = append(contexts, ctx)
		}
	}
	return contexts, diagnostics
}

func buildMSBuildContext(snapshot ProjectSnapshot, relPath string, project SolutionProject) (BuildContextRecord, []BuildContextDiagnostic, bool) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return BuildContextRecord{}, nil, false
	}
	extraction, ok := extractMSBuildMetadata(snapshot.Root, relPath)
	diagnostics := append([]BuildContextDiagnostic(nil), extraction.Diagnostics...)
	if !ok {
		diagnostics = append(diagnostics, BuildContextDiagnostic{
			Path:     relPath,
			Adapter:  "msbuild",
			Severity: "warning",
			Reason:   "parse_failed",
			Detail:   "MSBuild XML metadata could not be parsed",
		})
		return BuildContextRecord{}, diagnostics, false
	}
	for _, imported := range extraction.Imports {
		importedPath, resolved := normalizeMSBuildPath(snapshot.Root, relPath, imported, true)
		if !resolved {
			diagnostics = append(diagnostics, BuildContextDiagnostic{
				Path:     relPath,
				Adapter:  "msbuild",
				Severity: "info",
				Reason:   "unresolved_import",
				Detail:   imported,
			})
			continue
		}
		ext := strings.ToLower(filepath.Ext(importedPath))
		if ext != ".props" && ext != ".targets" {
			continue
		}
		importedExtraction, importedOK := extractMSBuildMetadata(snapshot.Root, importedPath)
		if !importedOK {
			diagnostics = append(diagnostics, BuildContextDiagnostic{
				Path:     relPath,
				Adapter:  "msbuild",
				Severity: "warning",
				Reason:   "import_parse_failed",
				Detail:   importedPath,
			})
			continue
		}
		extraction.IncludePaths = append(extraction.IncludePaths, importedExtraction.IncludePaths...)
		extraction.Defines = append(extraction.Defines, importedExtraction.Defines...)
		extraction.ForceIncludes = append(extraction.ForceIncludes, importedExtraction.ForceIncludes...)
		diagnostics = append(diagnostics, importedExtraction.Diagnostics...)
	}

	kind := "msbuild_project"
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == ".props" {
		kind = "msbuild_props"
	}
	if ext == ".targets" {
		kind = "msbuild_targets"
	}
	name := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	if strings.TrimSpace(project.Name) != "" {
		name = project.Name
	}
	files := analysisUniqueStrings(extraction.Files)
	confidence := "medium"
	if kind == "msbuild_project" && len(files) > 0 {
		confidence = "high"
	}
	ctx := BuildContextRecord{
		ID:            msbuildContextID(relPath, name),
		Name:          name + " MSBuild context",
		Kind:          kind,
		Directory:     filepath.ToSlash(filepath.Dir(relPath)),
		Project:       strings.TrimSpace(project.Name),
		Files:         files,
		IncludePaths:  analysisUniqueStrings(extraction.IncludePaths),
		Defines:       analysisUniqueStrings(extraction.Defines),
		ForceIncludes: analysisUniqueStrings(extraction.ForceIncludes),
		Source:        relPath,
		SourceAdapter: "msbuild",
		Confidence:    confidence,
		Diagnostics:   normalizeBuildContextDiagnostics(diagnostics),
	}
	if ctx.Directory == "." {
		ctx.Directory = ""
	}
	return ctx, diagnostics, true
}

func msbuildContextID(relPath string, name string) string {
	stem := strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(relPath)), filepath.Ext(relPath))
	id := sanitizeFileName(stem)
	if id == "" {
		id = sanitizeFileName(name)
	}
	if id == "" {
		id = "unknown"
	}
	return "buildctx:msbuild:" + id
}

func extractMSBuildMetadata(root string, relPath string) (msbuildExtraction, bool) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return msbuildExtraction{}, false
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	out := msbuildExtraction{}
	current := ""
	var content strings.Builder
	flush := func(name string) {
		value := strings.TrimSpace(content.String())
		content.Reset()
		switch name {
		case "AdditionalIncludeDirectories":
			out.IncludePaths = append(out.IncludePaths, normalizeMSBuildPathList(root, relPath, value, true)...)
		case "PreprocessorDefinitions":
			out.Defines = append(out.Defines, splitMSBuildList(value, false)...)
		case "ForcedIncludeFiles":
			out.ForceIncludes = append(out.ForceIncludes, normalizeMSBuildPathList(root, relPath, value, false)...)
		}
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err != io.EOF {
				out.Diagnostics = append(out.Diagnostics, BuildContextDiagnostic{
					Path:     relPath,
					Adapter:  "msbuild",
					Severity: "warning",
					Reason:   "parse_error",
					Detail:   err.Error(),
				})
				return out, false
			}
			break
		}
		switch typed := token.(type) {
		case xml.StartElement:
			name := typed.Name.Local
			switch name {
			case "ClCompile":
				if include := xmlAttrValue(typed.Attr, "Include"); include != "" {
					if path, ok := normalizeMSBuildPath(root, relPath, include, false); ok {
						out.Files = append(out.Files, path)
					}
				}
			case "Import":
				if project := xmlAttrValue(typed.Attr, "Project"); project != "" {
					out.Imports = append(out.Imports, project)
				}
			case "AdditionalIncludeDirectories", "PreprocessorDefinitions", "ForcedIncludeFiles":
				current = name
				content.Reset()
			}
		case xml.CharData:
			if current != "" {
				content.Write([]byte(typed))
			}
		case xml.EndElement:
			if current != "" && typed.Name.Local == current {
				flush(current)
				current = ""
			}
		}
	}
	out.Files = analysisUniqueStrings(out.Files)
	out.IncludePaths = analysisUniqueStrings(out.IncludePaths)
	out.Defines = analysisUniqueStrings(out.Defines)
	out.ForceIncludes = analysisUniqueStrings(out.ForceIncludes)
	out.Imports = analysisUniqueStrings(out.Imports)
	return out, true
}

func normalizeCompileCommandDirectory(root string, directory string) string {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" {
		return ""
	}
	abs := trimmed
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, trimmed)
	}
	return filepath.ToSlash(relOrAbs(root, filepath.Clean(abs)))
}

func normalizeCompileCommandFile(root string, directory string, file string) string {
	trimmed := strings.TrimSpace(file)
	if trimmed == "" {
		return ""
	}
	abs := trimmed
	if !filepath.IsAbs(abs) {
		baseDir := directory
		if strings.TrimSpace(baseDir) == "" {
			baseDir = root
		}
		abs = filepath.Join(baseDir, trimmed)
	}
	cleaned := filepath.Clean(abs)
	normalized := filepath.ToSlash(relOrAbs(root, cleaned))
	if strings.HasPrefix(normalized, "..") {
		return ""
	}
	return normalized
}

func splitAnalysisCommandLine(command string) []string {
	out := []string{}
	var current strings.Builder
	inQuote := false
	var quote rune
	flush := func() {
		token := strings.TrimSpace(current.String())
		current.Reset()
		if token != "" {
			out = append(out, token)
		}
	}
	for _, r := range command {
		switch {
		case inQuote && r == quote:
			inQuote = false
		case !inQuote && (r == '"' || r == '\''):
			inQuote = true
			quote = r
		case !inQuote && (r == ' ' || r == '\t' || r == '\r' || r == '\n'):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return out
}

func extractCompileCommandFlags(args []string) ([]string, []string, []string) {
	includes := []string{}
	defines := []string{}
	forceIncludes := []string{}
	for index := 0; index < len(args); index++ {
		token := strings.TrimSpace(args[index])
		if token == "" {
			continue
		}
		switch {
		case token == "-I" || token == "/I" || token == "-isystem":
			if index+1 < len(args) {
				includes = append(includes, strings.TrimSpace(args[index+1]))
				index++
			}
		case strings.HasPrefix(token, "-I") && len(token) > 2:
			includes = append(includes, strings.TrimSpace(token[2:]))
		case strings.HasPrefix(token, "/I") && len(token) > 2:
			includes = append(includes, strings.TrimSpace(token[2:]))
		case token == "-include" || token == "/FI":
			if index+1 < len(args) {
				forceIncludes = append(forceIncludes, strings.TrimSpace(args[index+1]))
				index++
			}
		case strings.HasPrefix(token, "/FI") && len(token) > 3:
			forceIncludes = append(forceIncludes, strings.TrimSpace(token[3:]))
		case strings.HasPrefix(token, "-D") && len(token) > 2:
			defines = append(defines, strings.TrimSpace(token[2:]))
		case strings.HasPrefix(token, "/D") && len(token) > 2:
			defines = append(defines, strings.TrimSpace(token[2:]))
		}
	}
	return analysisUniqueStrings(includes), analysisUniqueStrings(defines), analysisUniqueStrings(forceIncludes)
}

func xmlAttrValue(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if strings.EqualFold(attr.Name.Local, name) {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

func splitMSBuildList(value string, normalizePathLike bool) []string {
	parts := strings.Split(value, ";")
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "%(") || strings.Contains(part, "%(") {
			continue
		}
		if !normalizePathLike && strings.Contains(part, "$(") {
			continue
		}
		out = append(out, part)
	}
	return analysisUniqueStrings(out)
}

func normalizeMSBuildPathList(root string, ownerRelPath string, value string, allowDirectories bool) []string {
	out := []string{}
	for _, item := range splitMSBuildList(value, true) {
		if path, ok := normalizeMSBuildPath(root, ownerRelPath, item, allowDirectories); ok {
			out = append(out, path)
		}
	}
	return analysisUniqueStrings(out)
}

func normalizeMSBuildPath(root string, ownerRelPath string, raw string, allowDirectories bool) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.Contains(value, "%(") {
		return "", false
	}
	ownerAbs := filepath.Join(root, filepath.FromSlash(ownerRelPath))
	ownerDir := filepath.Dir(ownerAbs)
	value = strings.ReplaceAll(value, "\\", string(filepath.Separator))
	value = strings.ReplaceAll(value, "$(ProjectDir)", ownerDir+string(filepath.Separator))
	value = strings.ReplaceAll(value, "$(MSBuildProjectDirectory)", ownerDir)
	value = strings.ReplaceAll(value, "$(SolutionDir)", filepath.Clean(root)+string(filepath.Separator))
	value = strings.ReplaceAll(value, "$(Configuration)", "")
	value = strings.ReplaceAll(value, "$(Platform)", "")
	if strings.Contains(value, "$(") {
		return "", false
	}
	if strings.Contains(value, "*") || strings.Contains(value, "?") {
		return "", false
	}
	cleaned := value
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(ownerDir, cleaned)
	}
	cleaned = filepath.Clean(cleaned)
	rel := filepath.ToSlash(relOrAbs(root, cleaned))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	if !allowDirectories {
		if strings.TrimSpace(filepath.Ext(rel)) == "" {
			return "", false
		}
	}
	return rel, true
}

func normalizeBuildContextDiagnostics(items []BuildContextDiagnostic) []BuildContextDiagnostic {
	out := []BuildContextDiagnostic{}
	seen := map[string]struct{}{}
	for _, item := range items {
		item.Path = filepath.ToSlash(strings.TrimSpace(item.Path))
		item.Adapter = strings.TrimSpace(item.Adapter)
		item.Severity = strings.TrimSpace(item.Severity)
		item.Reason = strings.TrimSpace(item.Reason)
		item.Detail = strings.TrimSpace(item.Detail)
		if item.Adapter == "" {
			item.Adapter = "build"
		}
		if item.Severity == "" {
			item.Severity = "info"
		}
		if item.Reason == "" {
			continue
		}
		key := strings.ToLower(item.Path + "|" + item.Adapter + "|" + item.Severity + "|" + item.Reason + "|" + item.Detail)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		left := out[i].Path + "|" + out[i].Reason + "|" + out[i].Detail
		right := out[j].Path + "|" + out[j].Reason + "|" + out[j].Detail
		return left < right
	})
	return out
}

func strongestAnalysisConfidence(left string, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if runtimeEdgeConfidenceRank(right) > runtimeEdgeConfidenceRank(left) {
		return right
	}
	return left
}

func buildSnapshotBuildContexts(snapshot ProjectSnapshot, compileCommands []CompilationCommandRecord, msbuildContexts []BuildContextRecord) []BuildContextRecord {
	contextByID := map[string]BuildContextRecord{}
	projectDirMap := map[string]string{}
	for _, project := range snapshot.SolutionProjects {
		projectDirMap[strings.ToLower(filepath.ToSlash(strings.TrimSpace(project.Directory)))] = project.Name
	}

	mergeContext := func(ctx BuildContextRecord) {
		ctx.ID = strings.TrimSpace(ctx.ID)
		ctx.Name = strings.TrimSpace(ctx.Name)
		ctx.Kind = strings.TrimSpace(ctx.Kind)
		if ctx.ID == "" || ctx.Name == "" || ctx.Kind == "" {
			return
		}
		ctx.Directory = filepath.ToSlash(strings.TrimSpace(ctx.Directory))
		ctx.Project = strings.TrimSpace(ctx.Project)
		ctx.Target = strings.TrimSpace(ctx.Target)
		ctx.Module = strings.TrimSpace(ctx.Module)
		ctx.Plugin = strings.TrimSpace(ctx.Plugin)
		ctx.Compiler = strings.TrimSpace(ctx.Compiler)
		ctx.Source = strings.TrimSpace(ctx.Source)
		ctx.Files = analysisUniqueStrings(ctx.Files)
		ctx.IncludePaths = analysisUniqueStrings(ctx.IncludePaths)
		ctx.Defines = analysisUniqueStrings(ctx.Defines)
		ctx.ForceIncludes = analysisUniqueStrings(ctx.ForceIncludes)
		ctx.SourceAdapter = strings.TrimSpace(ctx.SourceAdapter)
		ctx.Confidence = strings.TrimSpace(ctx.Confidence)
		ctx.Diagnostics = normalizeBuildContextDiagnostics(ctx.Diagnostics)
		existing, ok := contextByID[ctx.ID]
		if !ok {
			contextByID[ctx.ID] = ctx
			return
		}
		existing.Directory = firstNonBlankAnalysisString(existing.Directory, ctx.Directory)
		existing.Project = firstNonBlankAnalysisString(existing.Project, ctx.Project)
		existing.Target = firstNonBlankAnalysisString(existing.Target, ctx.Target)
		existing.Module = firstNonBlankAnalysisString(existing.Module, ctx.Module)
		existing.Plugin = firstNonBlankAnalysisString(existing.Plugin, ctx.Plugin)
		existing.Compiler = firstNonBlankAnalysisString(existing.Compiler, ctx.Compiler)
		existing.Source = firstNonBlankAnalysisString(existing.Source, ctx.Source)
		existing.Files = analysisUniqueStrings(append(existing.Files, ctx.Files...))
		existing.IncludePaths = analysisUniqueStrings(append(existing.IncludePaths, ctx.IncludePaths...))
		existing.Defines = analysisUniqueStrings(append(existing.Defines, ctx.Defines...))
		existing.ForceIncludes = analysisUniqueStrings(append(existing.ForceIncludes, ctx.ForceIncludes...))
		existing.SourceAdapter = firstNonBlankAnalysisString(existing.SourceAdapter, ctx.SourceAdapter)
		existing.Confidence = strongestAnalysisConfidence(existing.Confidence, ctx.Confidence)
		existing.Diagnostics = normalizeBuildContextDiagnostics(append(existing.Diagnostics, ctx.Diagnostics...))
		contextByID[ctx.ID] = existing
	}

	for _, project := range snapshot.SolutionProjects {
		id := "buildctx:project:" + strings.TrimSpace(project.Name)
		files := append([]string{}, project.EntryFiles...)
		if strings.TrimSpace(project.Path) != "" {
			files = append(files, project.Path)
		}
		mergeContext(BuildContextRecord{
			ID:            id,
			Name:          project.Name,
			Kind:          "solution_project",
			Directory:     project.Directory,
			Project:       project.Name,
			Files:         files,
			Source:        project.Path,
			SourceAdapter: "solution",
			Confidence:    "medium",
		})
	}
	for _, target := range snapshot.UnrealTargets {
		id := "buildctx:target:" + strings.TrimSpace(target.Name)
		mergeContext(BuildContextRecord{
			ID:            id,
			Name:          target.Name,
			Kind:          "unreal_target",
			Directory:     filepath.ToSlash(filepath.Dir(target.Path)),
			Target:        target.Name,
			Files:         []string{target.Path},
			Source:        target.Path,
			SourceAdapter: "unreal_target",
			Confidence:    "high",
		})
	}
	for _, module := range snapshot.UnrealModules {
		id := "buildctx:module:" + strings.TrimSpace(module.Name)
		files := []string{}
		if strings.TrimSpace(module.Path) != "" {
			files = append(files, module.Path)
		}
		mergeContext(BuildContextRecord{
			ID:            id,
			Name:          module.Name,
			Kind:          "unreal_module",
			Directory:     filepath.ToSlash(filepath.Dir(module.Path)),
			Module:        module.Name,
			Plugin:        module.Plugin,
			Files:         files,
			Source:        module.Path,
			SourceAdapter: "unreal_build_cs",
			Confidence:    "high",
		})
	}
	for _, ctx := range msbuildContexts {
		mergeContext(ctx)
	}

	for index := range compileCommands {
		record := &compileCommands[index]
		moduleName := unrealModuleForFile(snapshot, record.File)
		projectName := projectNameForFile(record.File, projectDirMap)
		ctx := BuildContextRecord{
			Kind:          "compile_command",
			Directory:     record.Directory,
			Compiler:      record.Compiler,
			Files:         []string{record.File},
			IncludePaths:  append([]string{}, record.IncludePaths...),
			Defines:       append([]string{}, record.Defines...),
			ForceIncludes: append([]string{}, record.ForceIncludes...),
			Source:        record.Source,
			SourceAdapter: firstNonBlankAnalysisString(record.SourceAdapter, "compile_commands"),
			Confidence:    firstNonBlankAnalysisString(record.Confidence, "high"),
		}
		switch {
		case strings.TrimSpace(moduleName) != "":
			ctx.ID = "buildctx:compile:module:" + moduleName
			ctx.Name = moduleName + " compile context"
			ctx.Module = moduleName
		case strings.TrimSpace(projectName) != "":
			ctx.ID = "buildctx:compile:project:" + projectName
			ctx.Name = projectName + " compile context"
			ctx.Project = projectName
		default:
			dirName := strings.TrimSpace(filepath.Base(strings.TrimSpace(record.Directory)))
			if dirName == "" || dirName == "." {
				dirName = strings.TrimSpace(filepath.Base(strings.TrimSpace(filepath.Dir(record.File))))
			}
			ctx.ID = "buildctx:compile:dir:" + sanitizeFileName(firstNonBlankAnalysisString(dirName, record.Directory))
			ctx.Name = firstNonBlankAnalysisString(dirName, record.Directory) + " compile context"
		}
		record.BuildContextID = ctx.ID
		mergeContext(ctx)
	}

	contexts := make([]BuildContextRecord, 0, len(contextByID))
	for _, ctx := range contextByID {
		contexts = append(contexts, ctx)
	}
	sort.Slice(contexts, func(i int, j int) bool {
		return contexts[i].ID < contexts[j].ID
	})
	return contexts
}

func buildContextIDsForFile(snapshot ProjectSnapshot, path string) []string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return nil
	}
	moduleName := unrealModuleForFile(snapshot, path)
	projectDirMap := map[string]string{}
	for _, project := range snapshot.SolutionProjects {
		projectDirMap[strings.ToLower(filepath.ToSlash(strings.TrimSpace(project.Directory)))] = project.Name
	}
	projectName := projectNameForFile(path, projectDirMap)
	out := []string{}
	for _, ctx := range snapshot.BuildContexts {
		if buildContextContainsFile(ctx, path) {
			out = append(out, ctx.ID)
			continue
		}
		if strings.TrimSpace(moduleName) != "" && strings.EqualFold(strings.TrimSpace(ctx.Module), strings.TrimSpace(moduleName)) {
			out = append(out, ctx.ID)
			continue
		}
		if strings.TrimSpace(projectName) != "" && strings.EqualFold(strings.TrimSpace(ctx.Project), strings.TrimSpace(projectName)) {
			out = append(out, ctx.ID)
			continue
		}
	}
	sort.Strings(out)
	return analysisUniqueStrings(out)
}

func buildContextContainsFile(ctx BuildContextRecord, path string) bool {
	lowerPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	for _, item := range ctx.Files {
		lowerItem := strings.ToLower(filepath.ToSlash(strings.TrimSpace(item)))
		if lowerItem == "" {
			continue
		}
		if lowerItem == lowerPath {
			return true
		}
	}
	dir := strings.ToLower(filepath.ToSlash(strings.TrimSpace(ctx.Directory)))
	if dir == "" || dir == "." {
		return false
	}
	return strings.HasPrefix(lowerPath, strings.TrimSuffix(dir, "/")+"/")
}
