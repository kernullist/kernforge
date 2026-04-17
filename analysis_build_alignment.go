package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
}

type BuildContextRecord struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Directory     string   `json:"directory,omitempty"`
	Project       string   `json:"project,omitempty"`
	Target        string   `json:"target,omitempty"`
	Module        string   `json:"module,omitempty"`
	Plugin        string   `json:"plugin,omitempty"`
	Compiler      string   `json:"compiler,omitempty"`
	Files         []string `json:"files,omitempty"`
	IncludePaths  []string `json:"include_paths,omitempty"`
	Defines       []string `json:"defines,omitempty"`
	ForceIncludes []string `json:"force_includes,omitempty"`
	Source        string   `json:"source,omitempty"`
}

type SemanticPathV2 struct {
	Reason string   `json:"reason,omitempty"`
	Nodes  []string `json:"nodes,omitempty"`
	Edges  []string `json:"edges,omitempty"`
	Score  int      `json:"score,omitempty"`
}

func enrichBuildAlignment(snapshot *ProjectSnapshot) {
	compileCommands := discoverCompileCommands(snapshot.Root)
	contexts := buildSnapshotBuildContexts(*snapshot, compileCommands)
	snapshot.CompileCommands = compileCommands
	snapshot.BuildContexts = contexts
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

func buildSnapshotBuildContexts(snapshot ProjectSnapshot, compileCommands []CompilationCommandRecord) []BuildContextRecord {
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
		contextByID[ctx.ID] = existing
	}

	for _, project := range snapshot.SolutionProjects {
		id := "buildctx:project:" + strings.TrimSpace(project.Name)
		files := append([]string{}, project.EntryFiles...)
		if strings.TrimSpace(project.Path) != "" {
			files = append(files, project.Path)
		}
		mergeContext(BuildContextRecord{
			ID:        id,
			Name:      project.Name,
			Kind:      "solution_project",
			Directory: project.Directory,
			Project:   project.Name,
			Files:     files,
			Source:    project.Path,
		})
	}
	for _, target := range snapshot.UnrealTargets {
		id := "buildctx:target:" + strings.TrimSpace(target.Name)
		mergeContext(BuildContextRecord{
			ID:        id,
			Name:      target.Name,
			Kind:      "unreal_target",
			Directory: filepath.ToSlash(filepath.Dir(target.Path)),
			Target:    target.Name,
			Files:     []string{target.Path},
			Source:    target.Path,
		})
	}
	for _, module := range snapshot.UnrealModules {
		id := "buildctx:module:" + strings.TrimSpace(module.Name)
		files := []string{}
		if strings.TrimSpace(module.Path) != "" {
			files = append(files, module.Path)
		}
		mergeContext(BuildContextRecord{
			ID:        id,
			Name:      module.Name,
			Kind:      "unreal_module",
			Directory: filepath.ToSlash(filepath.Dir(module.Path)),
			Module:    module.Name,
			Plugin:    module.Plugin,
			Files:     files,
			Source:    module.Path,
		})
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
