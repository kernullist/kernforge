package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (rt *runtimeState) handleRootCausePatternsCommand(args string) error {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		rt.printRootCausePatternsUsage()
		return nil
	}
	switch strings.ToLower(fields[0]) {
	case "list":
		return rt.handleRootCausePatternsList(fields[1:])
	case "match":
		return rt.handleRootCausePatternsMatch(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), fields[0])))
	case "github-search":
		return rt.handleRootCausePatternsGitHubSearch(fields[1:])
	case "normalize":
		return rt.handleRootCausePatternsNormalize(fields[1:])
	case "validate":
		return rt.handleRootCausePatternsValidate(fields[1:])
	default:
		return fmt.Errorf("unknown /root-cause-patterns subcommand %q", fields[0])
	}
}

func (rt *runtimeState) printRootCausePatternsUsage() {
	fmt.Fprintln(rt.writer, rt.ui.section("Root Cause Patterns"))
	fmt.Fprintln(rt.writer, rt.ui.highlightCommands("/root-cause-patterns list [--type windows_kernel_driver|unreal5_game_server|web_backend|...] [--json]"))
	fmt.Fprintln(rt.writer, rt.ui.highlightCommands("/root-cause-patterns match <problem symptom> [--json]"))
	fmt.Fprintln(rt.writer, rt.ui.highlightCommands("/root-cause-patterns github-search [--type <project_type>] [--limit 20] [--out .kernforge/root_cause/github_issues.json] [query words...]"))
	fmt.Fprintln(rt.writer, rt.ui.highlightCommands("/root-cause-patterns normalize --in .kernforge/root_cause/github_issues.json --out .kernforge/root_cause/pattern_pack.json [--type <project_type>]"))
	fmt.Fprintln(rt.writer, rt.ui.highlightCommands("/root-cause-patterns validate [--in <pattern_pack.json>] [--json]"))
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Pattern packs are search priors only. /find-root-cause still requires current source evidence and reviewer causality validation."))
}

func (rt *runtimeState) handleRootCausePatternsList(args []string) error {
	fs := flag.NewFlagSet("root-cause-patterns list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var projectType string
	var jsonOut bool
	fs.StringVar(&projectType, "type", "", "project type filter")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pack, diagnostics := loadRootCausePatternPackWithDiagnostics(rt.workspace.Root, nil)
	filterTypes := normalizeRootCauseProjectTypes([]string{projectType})
	filtered := pack
	if len(filterTypes) > 0 {
		filtered.Patterns = nil
		for _, pattern := range pack.Patterns {
			if rootCauseProjectTypeOverlaps(filterTypes, pattern.ProjectTypes) {
				filtered.Patterns = append(filtered.Patterns, pattern)
			}
		}
	}
	if jsonOut {
		data, _ := json.MarshalIndent(filtered, "", "  ")
		fmt.Fprintln(rt.writer, string(data))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Root Cause Pattern Pack"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("patterns", fmt.Sprintf("%d", len(filtered.Patterns))))
	for _, diagnostic := range diagnostics {
		fmt.Fprintln(rt.writer, rt.ui.hintLine(diagnostic))
	}
	if strings.TrimSpace(projectType) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("type", projectType))
	}
	for _, pattern := range filtered.Patterns {
		fmt.Fprintf(rt.writer, "- %s [%s] %s\n", pattern.ID, strings.Join(pattern.ProjectTypes, ","), pattern.Title)
		if len(pattern.CodeSignals) > 0 {
			fmt.Fprintf(rt.writer, "  signals: %s\n", strings.Join(limitStrings(pattern.CodeSignals, 6), ", "))
		}
	}
	return nil
}

func (rt *runtimeState) handleRootCausePatternsMatch(args string) error {
	fs := flag.NewFlagSet("root-cause-patterns match", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var jsonOut bool
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fields := strings.Fields(args)
	if err := fs.Parse(fields); err != nil {
		return err
	}
	problem := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if problem == "" {
		return fmt.Errorf("usage: /root-cause-patterns match <problem symptom>")
	}
	analyzer := newProjectAnalyzer(rt.cfg, nil, rt.workspace, nil, nil)
	analyzer.analysisCfg = configProjectAnalysis(rt.cfg, rt.workspace.BaseRoot)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		return err
	}
	snapshot.ProjectTypes = inferRootCauseProjectTypes(snapshot, problem)
	pack, diagnostics := loadRootCausePatternPackWithDiagnostics(rt.workspace.Root, nil)
	matches := matchRootCausePatternsFromPack(snapshot, problem, pack, 12)
	if jsonOut {
		payload := struct {
			ProjectTypes []string                `json:"project_types"`
			Matches      []RootCausePatternMatch `json:"matches"`
		}{ProjectTypes: snapshot.ProjectTypes, Matches: matches}
		data, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(rt.writer, string(data))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Root Cause Pattern Match"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("project_types", strings.Join(snapshot.ProjectTypes, ", ")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("matches", fmt.Sprintf("%d", len(matches))))
	for _, diagnostic := range diagnostics {
		fmt.Fprintln(rt.writer, rt.ui.hintLine(diagnostic))
	}
	for _, match := range matches {
		fmt.Fprintf(rt.writer, "- %s score=%d confidence=%s\n", match.PatternID, match.Score, match.Confidence)
		if strings.TrimSpace(match.Title) != "" {
			fmt.Fprintf(rt.writer, "  title: %s\n", match.Title)
		}
		if len(match.MatchedSignals) > 0 {
			fmt.Fprintf(rt.writer, "  signals: %s\n", strings.Join(limitStrings(match.MatchedSignals, 6), ", "))
		}
		if len(match.MatchedFiles) > 0 {
			fmt.Fprintf(rt.writer, "  files: %s\n", strings.Join(limitStrings(match.MatchedFiles, 6), ", "))
		}
	}
	return nil
}

func (rt *runtimeState) handleRootCausePatternsGitHubSearch(args []string) error {
	fs := flag.NewFlagSet("root-cause-patterns github-search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var projectType string
	var outPath string
	var queryFlag string
	var tokenEnv string
	var apiURL string
	var limit int
	var dryRun bool
	fs.StringVar(&projectType, "type", "", "project type")
	fs.StringVar(&outPath, "out", "", "output JSON path")
	fs.StringVar(&queryFlag, "query", "", "GitHub issue search query")
	fs.StringVar(&tokenEnv, "token-env", "GITHUB_TOKEN", "environment variable containing GitHub token")
	fs.StringVar(&apiURL, "api-url", "https://api.github.com", "GitHub API base URL")
	fs.IntVar(&limit, "limit", 20, "per-query result limit")
	fs.BoolVar(&dryRun, "dry-run", false, "print generated queries without network")
	if err := fs.Parse(args); err != nil {
		return err
	}
	projectTypes := normalizeRootCauseProjectTypes([]string{projectType})
	query := strings.TrimSpace(queryFlag)
	if query == "" {
		query = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	queries := []string{}
	if query != "" {
		queries = append(queries, query)
	} else {
		queries = rootCauseGitHubDefaultQueries(projectTypes)
	}
	if len(queries) == 0 {
		return fmt.Errorf("provide --query or --type so default GitHub searches can be generated")
	}
	if dryRun {
		fmt.Fprintln(rt.writer, rt.ui.section("Root Cause GitHub Search"))
		for _, item := range queries {
			if rootCauseGitHubQueryRequestsPullRequests(item) {
				return fmt.Errorf("github root-cause issue search only supports issues; remove pull-request qualifier from query %q", item)
			}
			fmt.Fprintln(rt.writer, rootCauseGitHubIssueQuery(item))
		}
		return nil
	}
	token := ""
	if strings.TrimSpace(tokenEnv) != "" {
		token = os.Getenv(strings.TrimSpace(tokenEnv))
	}
	corpus, err := searchRootCauseGitHubIssues(context.Background(), &http.Client{Timeout: 30 * time.Second}, rootCauseGitHubSearchConfig{
		APIURL:       apiURL,
		Token:        token,
		Queries:      queries,
		ProjectTypes: projectTypes,
		Limit:        limit,
	})
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(corpus, "", "  ")
	if strings.TrimSpace(outPath) != "" {
		resolved, err := resolveRootCausePatternOutputPath(rt.workspace.Root, outPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(resolved, append(data, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Saved %d GitHub issue records.", len(corpus.Items))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("output", resolved))
		return nil
	}
	fmt.Fprintln(rt.writer, string(data))
	return nil
}

func (rt *runtimeState) handleRootCausePatternsNormalize(args []string) error {
	fs := flag.NewFlagSet("root-cause-patterns normalize", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var inPath string
	var outPath string
	var projectType string
	fs.StringVar(&inPath, "in", "", "input GitHub issue corpus JSON")
	fs.StringVar(&outPath, "out", "", "output pattern pack JSON")
	fs.StringVar(&projectType, "type", "", "project type to assign")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(inPath) == "" {
		return fmt.Errorf("normalize requires --in <github_issues.json>")
	}
	resolvedIn, err := resolveRootCausePatternOutputPath(rt.workspace.Root, inPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(resolvedIn)
	if err != nil {
		return err
	}
	corpus := RootCauseGitHubIssueCorpus{}
	if err := json.Unmarshal(data, &corpus); err != nil {
		return err
	}
	pack := normalizeGitHubIssuesToRootCausePatternPack(corpus, normalizeRootCauseProjectTypes([]string{projectType}))
	outData, _ := json.MarshalIndent(pack, "", "  ")
	if strings.TrimSpace(outPath) != "" {
		resolvedOut, err := resolveRootCausePatternOutputPath(rt.workspace.Root, outPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(resolvedOut), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(resolvedOut, append(outData, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Saved %d provisional pattern(s).", len(pack.Patterns))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("output", resolvedOut))
		return nil
	}
	fmt.Fprintln(rt.writer, string(outData))
	return nil
}

func (rt *runtimeState) handleRootCausePatternsValidate(args []string) error {
	fs := flag.NewFlagSet("root-cause-patterns validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var inPath string
	var jsonOut bool
	fs.StringVar(&inPath, "in", "", "pattern pack JSON to validate")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pack := RootCausePatternPack{}
	diagnostics := []string{}
	if strings.TrimSpace(inPath) != "" {
		resolvedIn, err := resolveRootCausePatternOutputPath(rt.workspace.Root, inPath)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(resolvedIn)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &pack); err != nil {
			return err
		}
	} else {
		pack, diagnostics = loadRootCausePatternPackForValidation(rt.workspace.Root, nil)
	}
	validation := validateRootCausePatternPack(pack)
	if jsonOut {
		payload := struct {
			Validation  RootCausePatternPackValidation `json:"validation"`
			Diagnostics []string                       `json:"diagnostics,omitempty"`
		}{Validation: validation, Diagnostics: diagnostics}
		data, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(rt.writer, string(data))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Root Cause Pattern Pack Validation"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("patterns", fmt.Sprintf("%d", validation.Patterns)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("with_sources", fmt.Sprintf("%d", validation.SourceBacked)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("errors", fmt.Sprintf("%d", len(validation.Errors))))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("warnings", fmt.Sprintf("%d", len(validation.Warnings))))
	if validation.Promotable > 0 || validation.Provisional > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("promotable", fmt.Sprintf("%d", validation.Promotable)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("provisional", fmt.Sprintf("%d", validation.Provisional)))
	}
	keys := []string{}
	for key := range validation.ProjectTypes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintln(rt.writer, rt.ui.statusKV(key, fmt.Sprintf("%d", validation.ProjectTypes[key])))
	}
	for _, diagnostic := range diagnostics {
		fmt.Fprintln(rt.writer, rt.ui.hintLine(diagnostic))
	}
	if len(validation.Errors) > 0 {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, "Errors:")
		for _, issue := range limitRootCausePatternValidationIssues(validation.Errors, 20) {
			fmt.Fprintf(rt.writer, "- %s %s: %s\n", issue.PatternID, issue.Field, issue.Message)
		}
	}
	if len(validation.Warnings) > 0 {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, "Warnings:")
		for _, issue := range limitRootCausePatternValidationIssues(validation.Warnings, 20) {
			fmt.Fprintf(rt.writer, "- %s %s: %s\n", issue.PatternID, issue.Field, issue.Message)
		}
	}
	return nil
}

func limitRootCausePatternValidationIssues(items []RootCausePatternValidationIssue, limit int) []RootCausePatternValidationIssue {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func resolveRootCausePatternOutputPath(root string, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	if strings.TrimSpace(root) == "" {
		return filepath.Abs(raw)
	}
	return filepath.Clean(filepath.Join(root, raw)), nil
}
