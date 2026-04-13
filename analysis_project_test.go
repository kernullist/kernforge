package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type stubAnalysisClient struct {
	workerCalls    int
	reviewerCalls  int
	synthesisCalls int
}

func (c *stubAnalysisClient) Name() string {
	return "stub"
}

func (c *stubAnalysisClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if strings.Contains(req.System, "project analysis sub-agent") {
		c.workerCalls++
		return ChatResponse{
			Message: Message{
				Text: `{"report":{"title":"core","scope_summary":"Core runtime and command handling.","responsibilities":["Owns the main command loop"],"entry_points":["main.go"],"internal_flow":["main.go initializes runtime state and enters the command loop"],"dependencies":["session.go"],"collaboration":["Uses session persistence and configuration state"],"risks":["Large main runtime file"],"unknowns":[],"evidence_files":["main.go"],"narrative":"The shard centers around startup and command dispatch."}}`,
			},
		}, nil
	}
	if strings.Contains(req.System, "conductor reviewing a sub-agent report") {
		c.reviewerCalls++
		return ChatResponse{
			Message: Message{
				Text: `{"decision":{"status":"approved","issues":[],"revision_prompt":""}}`,
			},
		}, nil
	}
	if strings.Contains(req.System, "writing the final Markdown document") {
		c.synthesisCalls++
		return ChatResponse{
			Message: Message{
				Text: "# Project Overview\n\nStub final document.\n",
			},
		}, nil
	}
	return ChatResponse{}, fmt.Errorf("unexpected system prompt")
}

type namedAnalysisClient struct {
	name  string
	calls int
	text  string
}

type fencedAnalysisClient struct{}

func (c *fencedAnalysisClient) Name() string {
	return "fenced"
}

func (c *fencedAnalysisClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if strings.Contains(req.System, "project analysis sub-agent") {
		return ChatResponse{
			Message: Message{
				Text: "```json\n{\n  \"report\": {\n    \"title\": \"runtime\",\n    \"scope_summary\": \"Runtime shard\",\n    \"responsibilities\": [\"Owns runtime\"],\n    \"key_files\": [\"main.go\"],\n    \"entry_points\": [\"main.go: main()\"],\n    \"internal_flow\": [\"main initializes runtime\"],\n    \"dependencies\": [\"config.go\"],\n    \"collaboration\": [\"Uses session state\"],\n    \"risks\": [],\n    \"unknowns\": [],\n    \"evidence_files\": [\"main.go\"],\n    \"narrative\": \"Structured worker output inside a code fence.\"\n  }\n}\n```",
			},
		}, nil
	}
	if strings.Contains(req.System, "conductor reviewing a sub-agent report") {
		return ChatResponse{
			Message: Message{
				Text: "```json\n{\n  \"decision\": {\n    \"status\": \"approved\",\n    \"issues\": [],\n    \"revision_prompt\": \"\"\n  }\n}\n```",
			},
		}, nil
	}
	if strings.Contains(req.System, "writing the final Markdown document") {
		return ChatResponse{
			Message: Message{
				Text: "# Project Overview\n\nFenced response final document.\n",
			},
		}, nil
	}
	return ChatResponse{}, fmt.Errorf("unexpected system prompt")
}

func (c *namedAnalysisClient) Name() string {
	return c.name
}

type draftAnalysisClient struct{}

func (c *draftAnalysisClient) Name() string {
	return "draft"
}

func (c *draftAnalysisClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if strings.Contains(req.System, "project analysis sub-agent") {
		return ChatResponse{
			Message: Message{
				Text: `{"report":{"title":"core","scope_summary":"summary","responsibilities":["boot"],"entry_points":["main.go"],"internal_flow":["main starts"],"dependencies":[],"collaboration":[],"risks":[],"unknowns":[],"evidence_files":["main.go"],"narrative":"ok"}}`,
			},
		}, nil
	}
	if strings.Contains(req.System, "conductor reviewing a sub-agent report") {
		return ChatResponse{
			Message: Message{
				Text: `{"decision":{"status":"needs_revision","issues":["missing depth"],"revision_prompt":"expand details"}}`,
			},
		}, nil
	}
	if strings.Contains(req.System, "writing the final Markdown document") {
		return ChatResponse{
			Message: Message{
				Text: "# Analysis\n\nbody\n",
			},
		}, nil
	}
	return ChatResponse{}, fmt.Errorf("unexpected system prompt")
}

type reviewerFailureAnalysisClient struct{}

func (c *reviewerFailureAnalysisClient) Name() string {
	return "reviewer-failure"
}

func (c *reviewerFailureAnalysisClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if strings.Contains(req.System, "project analysis sub-agent") {
		return ChatResponse{
			Message: Message{
				Text: `{"report":{"title":"core","scope_summary":"summary","responsibilities":["boot"],"entry_points":["main.go"],"internal_flow":["main starts"],"dependencies":[],"collaboration":["coordinates with worker"],"risks":[],"unknowns":[],"evidence_files":["main.go"],"narrative":"ok"}}`,
			},
		}, nil
	}
	if strings.Contains(req.System, "conductor reviewing a sub-agent report") {
		return ChatResponse{}, fmt.Errorf("openai API error: The operation was aborted | code=504")
	}
	if strings.Contains(req.System, "writing the final Markdown document") {
		return ChatResponse{
			Message: Message{
				Text: "# Analysis\n\nbody\n",
			},
		}, nil
	}
	return ChatResponse{}, fmt.Errorf("unexpected system prompt")
}

type failingAnalysisClient struct {
	target string
}

func (c *failingAnalysisClient) Name() string {
	return "failing"
}

func (c *failingAnalysisClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if strings.Contains(req.System, c.target) {
		return ChatResponse{}, fmt.Errorf("openai API error: Provider returned error")
	}
	if strings.Contains(req.System, "project analysis sub-agent") {
		return ChatResponse{
			Message: Message{
				Text: `{"report":{"title":"core","scope_summary":"summary","responsibilities":["boot"],"entry_points":["main.go"],"internal_flow":["main starts"],"dependencies":[],"collaboration":[],"risks":[],"unknowns":[],"evidence_files":["main.go"],"narrative":"ok"}}`,
			},
		}, nil
	}
	if strings.Contains(req.System, "conductor reviewing a sub-agent report") {
		return ChatResponse{
			Message: Message{
				Text: `{"decision":{"status":"approved","issues":[],"revision_prompt":""}}`,
			},
		}, nil
	}
	if strings.Contains(req.System, "writing the final Markdown document") {
		return ChatResponse{
			Message: Message{
				Text: "# Analysis\n\nbody\n",
			},
		}, nil
	}
	return ChatResponse{}, fmt.Errorf("unexpected system prompt")
}

type flakyAnalysisClient struct {
	failuresRemaining int
	calls             int
}

func (c *flakyAnalysisClient) Name() string {
	return "flaky"
}

func (c *flakyAnalysisClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.calls++
	if c.failuresRemaining > 0 {
		c.failuresRemaining--
		return ChatResponse{}, fmt.Errorf("openai API error: Provider returned error")
	}
	return ChatResponse{
		Message: Message{
			Text: `{"report":{"title":"core","scope_summary":"summary","responsibilities":["boot"],"facts":["main.go defines startup"],"inferences":["runtime is centralized"],"entry_points":["main.go"],"internal_flow":["main starts"],"dependencies":[],"collaboration":[],"risks":[],"unknowns":[],"evidence_files":["main.go"],"narrative":"ok"}}`,
		},
	}, nil
}

func (c *namedAnalysisClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.calls++
	return ChatResponse{
		Message: Message{
			Text: c.text,
		},
	}, nil
}

func TestProjectAnalyzerRunCreatesArtifacts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main()\n{\n}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "feature.go"), []byte("package pkg\n\nfunc Feature()\n{\n}\n"), 0o644); err != nil {
		t.Fatalf("write feature.go: %v", err)
	}

	cfg := DefaultConfig(root)
	cfg.Model = "stub-model"
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &stubAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}

	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), "map the project", "")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Summary.AgentCount < 2 || run.Summary.AgentCount > 16 {
		t.Fatalf("unexpected agent count: %d", run.Summary.AgentCount)
	}
	if run.Summary.TotalShards == 0 {
		t.Fatalf("expected shards to be created")
	}
	if strings.TrimSpace(run.Summary.OutputPath) == "" {
		t.Fatalf("expected output path")
	}
	if _, err := os.Stat(run.Summary.OutputPath); err != nil {
		t.Fatalf("expected markdown artifact: %v", err)
	}
	jsonPath := strings.TrimSuffix(run.Summary.OutputPath, ".md") + ".json"
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("expected json artifact: %v", err)
	}
	shardDir := strings.TrimSuffix(run.Summary.OutputPath, ".md") + "_shards"
	entries, err := os.ReadDir(shardDir)
	if err != nil {
		t.Fatalf("expected shard artifact directory: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected shard markdown artifacts")
	}
	if client.workerCalls == 0 {
		t.Fatalf("expected worker calls")
	}
	if len(run.DebugEvents) == 0 {
		t.Fatalf("expected debug events to be recorded")
	}
}

func TestProjectAnalyzerParsesFencedWorkerAndReviewerJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main()\n{\n}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	cfg := DefaultConfig(root)
	cfg.Model = "stub-model"
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &fencedAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}

	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), "map the project", "")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Summary.ApprovedShards == 0 {
		t.Fatalf("expected at least one approved shard")
	}
	if !strings.Contains(run.FinalDocument, "Fenced response final document.") {
		t.Fatalf("expected synthesized final document, got: %s", run.FinalDocument)
	}
	if len(run.Reports) == 0 {
		t.Fatalf("expected reports")
	}
	if got := run.Reports[0].ScopeSummary; got != "Runtime shard" {
		t.Fatalf("expected parsed scope summary, got %q", got)
	}
	if len(run.Reviews) == 0 || run.Reviews[0].Status != "approved" {
		t.Fatalf("expected parsed approved review, got %#v", run.Reviews)
	}
}

func TestDeriveAnalysisGoalScopeMatchesDirectoryHint(t *testing.T) {
	snapshot := ProjectSnapshot{
		Directories: []string{"TavernWorker", "Common", "docs/internal"},
		FilesByDirectory: map[string][]ScannedFile{
			"TavernWorker": {{Path: "TavernWorker/worker.cpp", Directory: "TavernWorker"}},
			"Common":       {{Path: "Common/shared.cpp", Directory: "Common"}},
		},
	}
	scope := deriveAnalysisGoalScope("TavernWorker 디렉토리 안의 코드를 분석하고 주요 탐지, 방어 기능들을 문서화해줘.", snapshot)
	if len(scope.DirectoryPrefixes) != 1 || scope.DirectoryPrefixes[0] != "TavernWorker" {
		t.Fatalf("expected TavernWorker scope, got %#v", scope)
	}
}

func TestProjectAnalyzerRunScopesShardsToRequestedDirectory(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "TavernWorker"),
		filepath.Join(root, "Common"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "TavernWorker", "worker.cpp"), []byte("int Worker()\n{\n    return 1;\n}\n"), 0o644); err != nil {
		t.Fatalf("write worker.cpp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Common", "shared.cpp"), []byte("int Shared()\n{\n    return 2;\n}\n"), 0o644); err != nil {
		t.Fatalf("write shared.cpp: %v", err)
	}

	cfg := DefaultConfig(root)
	cfg.Model = "stub-model"
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &stubAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}

	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), "TavernWorker 디렉토리 안의 코드를 분석하고 주요 탐지, 방어 기능들을 문서화해줘.", "")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(run.Shards) == 0 {
		t.Fatalf("expected scoped shards")
	}
	for _, shard := range run.Shards {
		if !hasPathPrefix(shard.PrimaryFiles, "TavernWorker/") {
			t.Fatalf("expected only TavernWorker shards, got %#v", run.Shards)
		}
	}
	if client.workerCalls != len(run.Shards) {
		t.Fatalf("expected worker calls to match scoped shards, calls=%d shards=%d", client.workerCalls, len(run.Shards))
	}
}

func TestProjectAnalyzerContinuesWhenReviewerFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main()\n{\n}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	cfg := DefaultConfig(root)
	cfg.Model = "stub-model"
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &reviewerFailureAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}

	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), "map the project", "")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Summary.ReviewFailures == 0 {
		t.Fatalf("expected review failures to be tracked")
	}
	if strings.TrimSpace(run.FinalDocument) == "" {
		t.Fatalf("expected final document even when reviewer fails")
	}
	if !strings.Contains(run.FinalDocument, "Draft Analysis") && !strings.Contains(run.FinalDocument, "Analysis With Review Failures") {
		t.Fatalf("expected degraded review banner in final document\n%s", run.FinalDocument)
	}
	found := false
	for _, review := range run.Reviews {
		if review.Status == "review_failed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected at least one review_failed decision: %#v", run.Reviews)
	}
}

func TestExecuteShardWrapsWorkerProviderErrorWithShardAndModel(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	cfg := DefaultConfig(root)
	cfg.Model = "analysis-model"
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	analyzer.workerClient = &failingAnalysisClient{target: "project analysis sub-agent"}
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	shards := analyzer.planShards(snapshot, 2)
	if len(shards) == 0 {
		t.Fatalf("expected shards")
	}
	reuseState := analyzer.buildReuseState(nil, shards)
	_, _, _, err = analyzer.executeShard(context.Background(), snapshot, shards[0], "analyze", nil, reuseState)
	if err == nil {
		t.Fatalf("expected error")
	}
	text := err.Error()
	if !strings.Contains(text, "analysis worker request failed") || !strings.Contains(text, "shard=") || !strings.Contains(text, "model=analysis-model") {
		t.Fatalf("expected wrapped worker error, got %q", text)
	}
}

func TestEstimateAgentCountClampsRange(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	client := &stubAnalysisClient{}
	ws := Workspace{
		BaseRoot: t.TempDir(),
		Root:     t.TempDir(),
	}
	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)

	snapshot := ProjectSnapshot{
		TotalFiles:    5000,
		TotalLines:    1000000,
		Directories:   make([]string, 200),
		ManifestFiles: []string{"go.mod", "package.json", "Cargo.toml"},
	}
	count := analyzer.estimateAgentCount(snapshot)
	if count != 16 {
		t.Fatalf("expected clamp to 16, got %d", count)
	}

	snapshot = ProjectSnapshot{}
	count = analyzer.estimateAgentCount(snapshot)
	if count != 2 {
		t.Fatalf("expected minimum of 2, got %d", count)
	}
}

func TestScanProjectResolvesRelativeImports(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "core"), 0o755); err != nil {
		t.Fatalf("mkdir core: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "feature"), 0o755); err != nil {
		t.Fatalf("mkdir feature: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "core", "shared.h"), []byte("#pragma once\n"), 0o644); err != nil {
		t.Fatalf("write shared.h: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "feature", "mod.cpp"), []byte("#include \"../core/shared.h\"\nint x = 1;\n"), 0o644); err != nil {
		t.Fatalf("write mod.cpp: %v", err)
	}

	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	file := snapshot.FilesByPath["feature/mod.cpp"]
	if len(file.Imports) == 0 {
		t.Fatalf("expected resolved imports, got none")
	}
	if file.Imports[0] != "core/shared.h" {
		t.Fatalf("expected core/shared.h, got %#v", file.Imports)
	}
}

func TestPlanShardsUsesImportClusters(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("a/a.cpp", "#include \"../b/b.h\"\nint a = 1;\n")
	mustWrite("a/a.h", "#pragma once\n")
	mustWrite("b/b.cpp", "#include \"../a/a.h\"\nint b = 2;\n")
	mustWrite("b/b.h", "#pragma once\n")
	mustWrite("c/c.cpp", "int c = 3;\n")

	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	shards := analyzer.planShards(snapshot, 2)
	if len(shards) == 0 {
		t.Fatalf("expected shards")
	}
	foundClusteredShard := false
	for _, shard := range shards {
		hasA := false
		hasB := false
		for _, path := range shard.PrimaryFiles {
			if strings.HasPrefix(path, "a/") {
				hasA = true
			}
			if strings.HasPrefix(path, "b/") {
				hasB = true
			}
		}
		if hasA && hasB {
			foundClusteredShard = true
			break
		}
	}
	if !foundClusteredShard {
		t.Fatalf("expected import-coupled directories to be clustered together")
	}
}

func TestScanProjectExcludesClaudeWorktrees(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude", "worktrees", "mirror"), 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "worktrees", "mirror", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write mirrored main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write real main.go: %v", err)
	}

	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	for _, file := range snapshot.Files {
		if strings.Contains(file.Path, ".claude/worktrees") {
			t.Fatalf("expected .claude/worktrees to be excluded, found %s", file.Path)
		}
	}
}

func TestFindAnalysisDirectoryCandidatesDetectsHiddenAndExternalLikeDirs(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, ".cache"),
		filepath.Join(root, "external"),
		filepath.Join(root, ".git"),
		filepath.Join(root, "src"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	cfg := defaultProjectAnalysisConfig(root)
	candidates, err := findAnalysisDirectoryCandidates(root, cfg)
	if err != nil {
		t.Fatalf("findAnalysisDirectoryCandidates returned error: %v", err)
	}
	got := map[string]string{}
	for _, candidate := range candidates {
		got[candidate.Path] = candidate.Reason
	}
	if got[".cache"] != "hidden" {
		t.Fatalf("expected .cache to be flagged as hidden, got %#v", got)
	}
	if got["external"] != "external_like" {
		t.Fatalf("expected external to be flagged as external_like, got %#v", got)
	}
	if _, ok := got[".git"]; ok {
		t.Fatalf("expected .git to be suppressed by default exclusions, got %#v", got)
	}
	if _, ok := got["src"]; ok {
		t.Fatalf("expected src not to be flagged, got %#v", got)
	}
}

func TestScanProjectHonorsExcludePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "external"), 0o755); err != nil {
		t.Fatalf("mkdir external: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "external", "dep.go"), []byte("package external\n"), 0o644); err != nil {
		t.Fatalf("write external dep.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	cfg := DefaultConfig(root)
	cfg.ProjectAnalysis.ExcludePaths = []string{"external"}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	for _, file := range snapshot.Files {
		if strings.HasPrefix(file.Path, "external/") {
			t.Fatalf("expected external path to be excluded, found %s", file.Path)
		}
	}
	for _, dir := range snapshot.Directories {
		if dir == "external" {
			t.Fatalf("expected external directory to be excluded from directories list")
		}
	}
	if _, ok := snapshot.FilesByPath["main.go"]; !ok {
		t.Fatalf("expected main.go to remain in snapshot")
	}
}

func TestScanProjectExcludesVisualStudioBuildOutputs(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite("Tavern/Common/ETWConsumer.cpp", "int Etw()\n{\n    return 1;\n}\n")
	mustWrite("Tavern/Tavern/x64/Live/Tavern.tlog/link.command.1.tlog", "^link\n")
	mustWrite("Tavern/Tavern/x64/Live/Tavern.lastbuildstate", "state\n")
	mustWrite("Tavern/TavernWorker/x64/Release/TavernWorker.tlog/CL.read.1.tlog", "log\n")

	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}

	for _, file := range snapshot.Files {
		lower := strings.ToLower(file.Path)
		if strings.Contains(lower, "/x64/live/") || strings.Contains(lower, "/x64/release/") {
			t.Fatalf("expected Visual Studio output path to be excluded, found %s", file.Path)
		}
		if strings.HasSuffix(lower, ".tlog") || strings.HasSuffix(lower, ".lastbuildstate") {
			t.Fatalf("expected build artifact file to be excluded, found %s", file.Path)
		}
	}
	if _, ok := snapshot.FilesByPath["Tavern/Common/ETWConsumer.cpp"]; !ok {
		t.Fatalf("expected normal source file to remain included")
	}
}

func TestScanProjectExcludesCommonGeneratedOutputsAcrossToolchains(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite("src/main.cpp", "int main()\n{\n    return 0;\n}\n")
	mustWrite("python/app.py", "print('ok')\n")
	mustWrite("target/debug/build.log", "cargo build output\n")
	mustWrite("module/.venv/Lib/site-packages/pkg.py", "generated\n")
	mustWrite("python/__pycache__/app.cpython-312.pyc", "bytecode\n")
	mustWrite("web/app.tsbuildinfo", "incremental state\n")
	mustWrite("native/CMakeFiles/progress.marks", "1\n")
	mustWrite("native/cmake-build-debug/compile_commands.json", "{ }\n")

	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}

	for _, file := range snapshot.Files {
		lower := strings.ToLower(file.Path)
		if strings.Contains(lower, "/target/debug/") ||
			strings.Contains(lower, "/.venv/") ||
			strings.Contains(lower, "/__pycache__/") ||
			strings.Contains(lower, "/cmakefiles/") ||
			strings.Contains(lower, "/cmake-build-debug/") ||
			strings.HasSuffix(lower, ".tsbuildinfo") ||
			strings.HasSuffix(lower, ".pyc") {
			t.Fatalf("expected generated artifact to be excluded, found %s", file.Path)
		}
	}
	if _, ok := snapshot.FilesByPath["src/main.cpp"]; !ok {
		t.Fatalf("expected native source file to remain included")
	}
	if _, ok := snapshot.FilesByPath["python/app.py"]; !ok {
		t.Fatalf("expected python source file to remain included")
	}
}

func TestMergeSessionSummaryWithAnalysisReplacesPreviousCachedBlock(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:  "run-2",
			Goal:   "map worker architecture",
			Status: "completed",
		},
		KnowledgePack: KnowledgePack{
			ProjectSummary:    "Worker owns telemetry ingestion.",
			PrimaryStartup:    "TavernWorker",
			TopImportantFiles: []string{"TavernWorker/main.cpp"},
			Subsystems: []KnowledgeSubsystem{
				{Title: "Worker Runtime", Group: "Forensic Analysis"},
			},
		},
	}
	initial := "Carry over prior implementation notes.\n\n" + cachedProjectAnalysisSummaryStart + "\n- Goal: stale\n" + cachedProjectAnalysisSummaryEnd
	merged := mergeSessionSummaryWithAnalysis(initial, run)
	if !strings.Contains(merged, "Carry over prior implementation notes.") {
		t.Fatalf("expected non-analysis summary to remain, got %q", merged)
	}
	if strings.Contains(merged, "- Goal: stale") {
		t.Fatalf("expected stale cached analysis block to be replaced, got %q", merged)
	}
	if !strings.Contains(merged, "map worker architecture") || !strings.Contains(merged, "Worker owns telemetry ingestion.") {
		t.Fatalf("expected new cached analysis block, got %q", merged)
	}
}

func TestAnalysisQueryMeaningfullyChangedIgnoresShortFollowUpButDetectsNewTopic(t *testing.T) {
	if analysisQueryMeaningfullyChanged("Explain TavernWorker startup flow.", "Now summarize risks only.") {
		t.Fatalf("expected short follow-up not to trigger reinjection")
	}
	if !analysisQueryMeaningfullyChanged("Explain TavernWorker startup flow.", "Explain Common IPC module architecture in detail.") {
		t.Fatalf("expected materially different topic to trigger reinjection")
	}
}

func TestScanProjectResolvesGoModuleImports(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "core"), 0o755); err != nil {
		t.Fatalf("mkdir internal/core: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "core", "core.go"), []byte("package core\n"), 0o644); err != nil {
		t.Fatalf("write core.go: %v", err)
	}
	mainBody := "package main\n\nimport (\n    \"example.com/demo/internal/core\"\n)\n\nfunc main() {\n    _ = core.Version\n}\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(mainBody), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	if snapshot.ModulePath != "example.com/demo" {
		t.Fatalf("expected module path example.com/demo, got %q", snapshot.ModulePath)
	}
	file := snapshot.FilesByPath["main.go"]
	found := false
	for _, item := range file.Imports {
		if item == "internal/core/core.go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected resolved go import to internal/core/core.go, got %#v", file.Imports)
	}
}

func TestFallbackFinalDocumentIncludesFlowAndIntegrationSections(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:          "C:/repo",
		ModulePath:    "example.com/demo",
		Directories:   []string{"core", "feature"},
		ManifestFiles: []string{"go.mod"},
		TotalFiles:    2,
		TotalLines:    42,
	}
	reports := []WorkerReport{
		{
			Title:            "core",
			ScopeSummary:     "Core runtime.",
			Responsibilities: []string{"Owns bootstrap"},
			KeyFiles:         []string{"main.go", "config.go"},
			EntryPoints:      []string{"main.go"},
			InternalFlow:     []string{"main.go initializes config before dispatch"},
			Dependencies:     []string{"feature/service.go"},
			Collaboration:    []string{"Calls into feature service after bootstrap"},
			Risks:            []string{"Tight coupling in startup path"},
			Unknowns:         []string{"Shutdown sequence not fully traced"},
			Narrative:        "Narrative block.",
		},
	}

	doc := fallbackFinalDocument(snapshot, []AnalysisShard{{ID: "shard-01", Name: "core"}}, reports, "analyze flow")
	for _, expected := range []string{
		"## Shard Index",
		"## Execution Flow And Entry Points",
		"## Dependencies And Integration Points",
		"## Risks And Unknowns",
		"Integration: Calls into feature service after bootstrap",
		"Responsibilities:",
		"Key files:",
		"Internal flow:",
	} {
		if !strings.Contains(doc, expected) {
			t.Fatalf("expected document to contain %q\n%s", expected, doc)
		}
	}
}

func TestBuildShardDocumentsCreatesDetailedShardMarkdown(t *testing.T) {
	shards := []AnalysisShard{
		{
			ID:                 "shard-01",
			Name:               "core",
			PrimaryFiles:       []string{"main.go"},
			ReferenceFiles:     []string{"config.go"},
			CacheStatus:        "miss",
			InvalidationReason: "new",
		},
	}
	reports := []WorkerReport{
		{
			ScopeSummary:     "Core runtime summary.",
			Responsibilities: []string{"Boot the runtime"},
			KeyFiles:         []string{"main.go"},
			EntryPoints:      []string{"main.go"},
			InternalFlow:     []string{"Initialize config then enter loop"},
			Dependencies:     []string{"config.go"},
			Collaboration:    []string{"Interacts with session management"},
			Risks:            []string{"Large command surface"},
			Unknowns:         []string{"Shutdown behavior not traced"},
			EvidenceFiles:    []string{"main.go"},
			Narrative:        "Detailed narrative.",
		},
	}

	docs := buildShardDocuments(ProjectSnapshot{}, shards, reports, "analyze runtime")
	doc := docs["shard-01"]
	for _, expected := range []string{
		"# core",
		"## Primary Files",
		"## Responsibilities",
		"## Key Files",
		"## Internal Flow",
		"## Evidence Files",
	} {
		if !strings.Contains(doc, expected) {
			t.Fatalf("expected shard document to contain %q\n%s", expected, doc)
		}
	}
}

func TestBuildShardDocumentsIncludesInvalidationEvidenceForUnrealNetwork(t *testing.T) {
	snapshot := ProjectSnapshot{
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterCharacter", File: "net.cpp", ServerRPCs: []string{"ServerFire"}, ReplicatedProperties: []string{"Ammo"}},
		},
	}
	shards := []AnalysisShard{
		{
			ID:                 "shard-01",
			Name:               "unreal_network",
			PrimaryFiles:       []string{"net.cpp"},
			CacheStatus:        "miss",
			InvalidationReason: "semantic_dependency_changed",
			InvalidationDiff:   []string{"Replicated property added: AShooterCharacter -> Ammo"},
			InvalidationChanges: []InvalidationChange{
				{Kind: "replicated_property_added", Scope: "unreal_network", Owner: "AShooterCharacter", Subject: "Ammo"},
			},
		},
	}
	reports := []WorkerReport{
		{
			ScopeSummary:  "Network summary.",
			EvidenceFiles: []string{"net.cpp"},
			Narrative:     "Detailed narrative.",
		},
	}
	docs := buildShardDocuments(snapshot, shards, reports, "analyze network")
	doc := docs["shard-01"]
	for _, expected := range []string{
		"Invalidation evidence:",
		"Invalidation diff:",
		"AShooterCharacter server=ServerFire",
		"Replicated property added: AShooterCharacter -> Ammo",
	} {
		if !strings.Contains(doc, expected) {
			t.Fatalf("expected shard document invalidation evidence %q\n%s", expected, doc)
		}
	}
}

func TestBuildInvalidationDiffLinesDetectsUnrealNetworkDelta(t *testing.T) {
	previous := ProjectSnapshot{
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterCharacter", File: "net.cpp", ServerRPCs: []string{"ServerFire"}},
		},
	}
	current := ProjectSnapshot{
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterCharacter", File: "net.cpp", ServerRPCs: []string{"ServerFire"}, ReplicatedProperties: []string{"Ammo"}},
		},
	}
	diff := buildInvalidationDiffLines(previous, current, []string{"unreal_network"}, []string{"net.cpp"}, []string{"net.cpp"}, []string{"semantic_dependency_changed"}, 4)
	if len(diff) == 0 {
		t.Fatalf("expected invalidation diff lines")
	}
	foundAdded := false
	for _, item := range diff {
		if strings.Contains(item, "Replicated property added:") && strings.Contains(item, "Ammo") {
			foundAdded = true
			break
		}
	}
	if !foundAdded {
		t.Fatalf("expected Ammo replication diff in %+v", diff)
	}
}

func TestBuildInvalidationChangesDetectsStructuredUnrealNetworkDelta(t *testing.T) {
	previous := ProjectSnapshot{
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterCharacter", File: "net.cpp", ServerRPCs: []string{"ServerFire"}},
		},
	}
	current := ProjectSnapshot{
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterCharacter", File: "net.cpp", ServerRPCs: []string{"ServerFire"}, ReplicatedProperties: []string{"Ammo"}},
		},
	}
	changes := buildInvalidationChanges(previous, current, []string{"unreal_network"}, []string{"net.cpp"}, []string{"net.cpp"}, 4)
	if len(changes) == 0 {
		t.Fatalf("expected structured invalidation changes")
	}
	found := false
	for _, change := range changes {
		if change.Kind == "replicated_property_added" && change.Owner == "AShooterCharacter" && change.Subject == "Ammo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected replicated_property_added change in %+v", changes)
	}
}

func TestBuildAnalysisExecutionSummaryIncludesTopChangeClasses(t *testing.T) {
	shards := []AnalysisShard{
		{
			ID:                 "shard-01",
			Name:               "unreal_network",
			CacheStatus:        "miss",
			InvalidationReason: "semantic_dependency_changed",
			InvalidationChanges: []InvalidationChange{
				{Kind: "replicated_property_added", Scope: "unreal_network", Owner: "AShooterCharacter", Subject: "Ammo"},
				{Kind: "replicated_property_added", Scope: "unreal_network", Owner: "AShooterCharacter", Subject: "Health"},
			},
		},
		{
			ID:                 "shard-02",
			Name:               "asset_config",
			CacheStatus:        "miss",
			InvalidationReason: "semantic_dependency_changed",
			InvalidationChanges: []InvalidationChange{
				{Kind: "config_binding_added", Scope: "asset_config", Owner: "AShooterHUD", Subject: "MenuClass"},
			},
		},
	}
	summary := buildAnalysisExecutionSummary(shards)
	if len(summary.TopChangeClasses) == 0 {
		t.Fatalf("expected top change classes in execution summary: %+v", summary)
	}
	if !strings.Contains(summary.TopChangeClasses[0], "replicated_property_added") {
		t.Fatalf("expected replicated_property_added to lead top change classes: %+v", summary.TopChangeClasses)
	}
	if len(summary.TopChangeExamples) == 0 || !strings.Contains(summary.TopChangeExamples[0], "Replicated property added") {
		t.Fatalf("expected top change example in execution summary: %+v", summary.TopChangeExamples)
	}
}

func TestSummarizeKnowledgePackIncludesLensAwareExecutiveFocus(t *testing.T) {
	snapshot := ProjectSnapshot{
		AnalysisLenses: []AnalysisLens{
			{Type: "architecture"},
			{Type: "security_boundary"},
			{Type: "runtime_flow"},
		},
	}
	items := []synthesisSection{
		{
			Title:            "network",
			Group:            "Security Control",
			Responsibilities: []string{"handle RPC"},
		},
	}
	execution := AnalysisExecutionSummary{
		TopChangeClasses: []string{"replicated_property_added (2)", "rpc_added (1)"},
	}
	summary := summarizeKnowledgePack(snapshot, items, execution)
	if !strings.Contains(summary, "Executive focus: recent changes are concentrated on authority, replication, or security-sensitive boundaries.") {
		t.Fatalf("expected lens-aware executive focus in summary: %s", summary)
	}
}

func TestPlanShardsSplitsLargeRootIntoSubsystemShards(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	for _, name := range []string{
		"main.go", "agent.go", "session.go", "provider.go", "config.go", "scout.go",
		"verify.go", "verify_policy.go", "verify_classifier.go", "checkpoint.go",
		"hooks.go", "hook_overrides.go",
		"evidence_store.go", "investigation_collectors.go", "simulation_profiles.go",
		"persistent_memory.go", "memory_policy.go", "mcp.go", "skill.go",
		"ui.go", "viewer_windows.go", "preview.go", "selection_diff.go",
		"commands_hooks.go", "commands_investigate.go", "commands_memory.go",
		"input_windows.go", "cancel_windows.go", "storage_atomic.go",
		"README.md", "ROADMAP_kor.md", "release.ps1", "go.mod",
	} {
		write(name)
	}

	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	if len(snapshot.FilesByDirectory[""]) < 12 {
		t.Fatalf("expected enough root files for root subsystem split, got %d", len(snapshot.FilesByDirectory[""]))
	}
	rootSubShards := analyzer.planRootSubsystemShards(snapshot, snapshot.FilesByDirectory[""])
	if len(rootSubShards) < 2 {
		t.Fatalf("expected direct root subsystem split, got %d", len(rootSubShards))
	}
	shards := analyzer.planShards(snapshot, 4)
	names := []string{}
	for _, shard := range shards {
		names = append(names, shard.Name)
	}
	foundRuntime := false
	foundVerification := false
	foundPlatformIO := false
	foundDocsSpecs := false
	foundProjectManifest := false
	foundOpsScripts := false
	for _, name := range names {
		if strings.HasPrefix(name, "runtime") {
			foundRuntime = true
		}
		if strings.HasPrefix(name, "verification") {
			foundVerification = true
		}
		if strings.HasPrefix(name, "platform_io") {
			foundPlatformIO = true
		}
		if strings.HasPrefix(name, "docs_specs") {
			foundDocsSpecs = true
		}
		if strings.HasPrefix(name, "project_manifest") {
			foundProjectManifest = true
		}
		if strings.HasPrefix(name, "ops_scripts") {
			foundOpsScripts = true
		}
	}
	if !foundRuntime || !foundVerification || !foundPlatformIO || !foundDocsSpecs || !foundProjectManifest || !foundOpsScripts {
		t.Fatalf("expected root subsystem shards, got %v", names)
	}
}

func TestPlanShardsUsesSemanticBucketsForUnrealSnapshots(t *testing.T) {
	analyzer := &projectAnalyzer{
		analysisCfg: ProjectAnalysisConfig{
			MaxFilesPerShard: 8,
			MaxLinesPerShard: 2000,
			MaxTotalShards:   16,
		},
	}
	files := []ScannedFile{
		{Path: "Source/ShooterGame/Main.cpp", Directory: "Source/ShooterGame", LineCount: 120, IsEntrypoint: true, ImportanceScore: 15},
		{Path: "ShooterGame.uproject", Directory: "", LineCount: 30, IsManifest: true, ImportanceScore: 10},
		{Path: "Source/ShooterGame/ShooterGame.Build.cs", Directory: "Source/ShooterGame", LineCount: 40, IsManifest: true, ImportanceScore: 14},
		{Path: "Source/ShooterGame/Public/ShooterCharacter.h", Directory: "Source/ShooterGame/Public", LineCount: 220, ImportanceScore: 18},
		{Path: "Source/ShooterGame/Public/ShooterHUD.h", Directory: "Source/ShooterGame/Public", LineCount: 160, ImportanceScore: 13},
		{Path: "Source/ShooterGame/Public/ShooterAbilitySet.h", Directory: "Source/ShooterGame/Public", LineCount: 150, ImportanceScore: 12},
		{Path: "Source/ShooterGame/Private/ShooterSettings.cpp", Directory: "Source/ShooterGame/Private", LineCount: 90, ImportanceScore: 9},
		{Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IntegrityGuard.cpp", Directory: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private", LineCount: 260, ImportanceScore: 17},
		{Path: "Source/ShooterGame/Private/MiscRuntime.cpp", Directory: "Source/ShooterGame/Private", LineCount: 80, ImportanceScore: 4},
	}
	filesByPath := map[string]ScannedFile{}
	filesByDir := map[string][]ScannedFile{}
	for _, file := range files {
		filesByPath[file.Path] = file
		filesByDir[file.Directory] = append(filesByDir[file.Directory], file)
	}
	snapshot := ProjectSnapshot{
		Root:             "C:\\repo",
		GeneratedAt:      time.Now(),
		Files:            files,
		FilesByPath:      filesByPath,
		FilesByDirectory: filesByDir,
		EntrypointFiles: []string{
			"Source/ShooterGame/Main.cpp",
		},
		ManifestFiles: []string{
			"ShooterGame.uproject",
			"Source/ShooterGame/ShooterGame.Build.cs",
		},
		UnrealProjects: []UnrealProject{
			{Name: "ShooterGame", Path: "ShooterGame.uproject", Modules: []string{"ShooterGame"}, Plugins: []string{"CheatGuard"}},
		},
		UnrealModules: []UnrealModule{
			{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs"},
			{Name: "CheatGuardRuntime", Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/CheatGuardRuntime.Build.cs"},
		},
		UnrealTypes: []UnrealReflectedType{
			{Name: "AShooterCharacter", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterCharacter.h", GameplayRole: "character"},
			{Name: "AShooterHUD", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterHUD.h", GameplayRole: "hud"},
			{Name: "UShooterAbilitySet", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterAbilitySet.h"},
			{Name: "UIntegrityGuardSubsystem", Kind: "UCLASS", Module: "CheatGuardRuntime", File: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IntegrityGuard.cpp", GameplayRole: "subsystem"},
		},
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterCharacter", File: "Source/ShooterGame/Public/ShooterCharacter.h", ServerRPCs: []string{"ServerFire"}},
		},
		UnrealAssets: []UnrealAssetReference{
			{OwnerName: "AShooterHUD", File: "Source/ShooterGame/Private/ShooterSettings.cpp", ConfigKeys: []string{"GameDefaultMap"}},
		},
	}
	shards := analyzer.planShards(snapshot, 6)
	names := []string{}
	for _, shard := range shards {
		names = append(names, shard.Name)
	}
	joined := strings.Join(names, ",")
	for _, expected := range []string{
		"startup",
		"build_graph",
		"unreal_network",
		"unreal_ui",
		"unreal_ability",
		"asset_config",
		"integrity_security",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected semantic shard %q in %v", expected, names)
		}
	}
}

func TestPlanShardsSecurityModePrioritizesSecurityBuckets(t *testing.T) {
	analyzer := &projectAnalyzer{
		analysisCfg: ProjectAnalysisConfig{
			MaxFilesPerShard: 8,
			MaxLinesPerShard: 2000,
			MaxTotalShards:   16,
		},
	}
	files := []ScannedFile{
		{Path: "Source/ShooterGame/Main.cpp", Directory: "Source/ShooterGame", LineCount: 120, IsEntrypoint: true, ImportanceScore: 15},
		{Path: "ShooterGame.uproject", Directory: "", LineCount: 30, IsManifest: true, ImportanceScore: 10},
		{Path: "Source/ShooterGame/ShooterGame.Build.cs", Directory: "Source/ShooterGame", LineCount: 40, IsManifest: true, ImportanceScore: 14},
		{Path: "Source/ShooterGame/Public/ShooterCharacter.h", Directory: "Source/ShooterGame/Public", LineCount: 220, ImportanceScore: 18},
		{Path: "Source/ShooterGame/Public/ShooterHUD.h", Directory: "Source/ShooterGame/Public", LineCount: 160, ImportanceScore: 13},
		{Path: "Source/ShooterGame/Public/ShooterAbilitySet.h", Directory: "Source/ShooterGame/Public", LineCount: 150, ImportanceScore: 12},
		{Path: "Source/ShooterGame/Private/ShooterSettings.cpp", Directory: "Source/ShooterGame/Private", LineCount: 90, ImportanceScore: 9},
		{Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IntegrityGuard.cpp", Directory: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private", LineCount: 260, ImportanceScore: 17},
	}
	filesByPath := map[string]ScannedFile{}
	filesByDir := map[string][]ScannedFile{}
	for _, file := range files {
		filesByPath[file.Path] = file
		filesByDir[file.Directory] = append(filesByDir[file.Directory], file)
	}
	snapshot := ProjectSnapshot{
		Root:             "C:\\repo",
		GeneratedAt:      time.Now(),
		AnalysisMode:     "security",
		Files:            files,
		FilesByPath:      filesByPath,
		FilesByDirectory: filesByDir,
		EntrypointFiles:  []string{"Source/ShooterGame/Main.cpp"},
		ManifestFiles: []string{
			"ShooterGame.uproject",
			"Source/ShooterGame/ShooterGame.Build.cs",
		},
		UnrealProjects: []UnrealProject{
			{Name: "ShooterGame", Path: "ShooterGame.uproject", Modules: []string{"ShooterGame"}, Plugins: []string{"CheatGuard"}},
		},
		UnrealModules: []UnrealModule{
			{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs"},
			{Name: "CheatGuardRuntime", Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/CheatGuardRuntime.Build.cs"},
		},
		UnrealTypes: []UnrealReflectedType{
			{Name: "AShooterCharacter", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterCharacter.h", GameplayRole: "character"},
			{Name: "AShooterHUD", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterHUD.h", GameplayRole: "hud"},
			{Name: "UShooterAbilitySet", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterAbilitySet.h"},
			{Name: "UIntegrityGuardSubsystem", Kind: "UCLASS", Module: "CheatGuardRuntime", File: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IntegrityGuard.cpp", GameplayRole: "subsystem"},
		},
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterCharacter", File: "Source/ShooterGame/Public/ShooterCharacter.h", ServerRPCs: []string{"ServerFire"}},
		},
		UnrealAssets: []UnrealAssetReference{
			{OwnerName: "AShooterHUD", File: "Source/ShooterGame/Private/ShooterSettings.cpp", ConfigKeys: []string{"GameDefaultMap"}},
		},
	}

	shards := analyzer.planShards(snapshot, 6)
	if len(shards) < 2 {
		t.Fatalf("expected multiple semantic shards, got %v", shards)
	}
	if !strings.HasPrefix(shards[0].Name, "security_") && !strings.HasPrefix(shards[0].Name, "integrity_security") {
		t.Fatalf("expected security mode to prioritize security shard first, got %v", shards[0].Name)
	}
	if !strings.HasPrefix(shards[1].Name, "unreal_network") &&
		!strings.HasPrefix(shards[1].Name, "startup") &&
		!strings.HasPrefix(shards[1].Name, "integrity_security") {
		t.Fatalf("expected security-adjacent shard near front, got %v", shards[1].Name)
	}
}

func TestPlanShardsSecurityModeSplitsSpecializedSecurityBuckets(t *testing.T) {
	analyzer := &projectAnalyzer{
		analysisCfg: ProjectAnalysisConfig{
			MaxFilesPerShard: 8,
			MaxLinesPerShard: 2000,
			MaxTotalShards:   16,
		},
	}
	files := []ScannedFile{
		{Path: "Source/ShooterGame/Main.cpp", Directory: "Source/ShooterGame", LineCount: 120, IsEntrypoint: true, ImportanceScore: 15},
		{Path: "ShooterGame.uproject", Directory: "", LineCount: 30, IsManifest: true, ImportanceScore: 10},
		{Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/KernelDriverBridge.cpp", Directory: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private", LineCount: 180, ImportanceScore: 16},
		{Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IoctlDispatch.cpp", Directory: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private", LineCount: 170, ImportanceScore: 16},
		{Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/HandlePolicy.cpp", Directory: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private", LineCount: 160, ImportanceScore: 16},
		{Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/MemoryScanner.cpp", Directory: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private", LineCount: 220, ImportanceScore: 17},
		{Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/RpcDispatchPipe.cpp", Directory: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private", LineCount: 150, ImportanceScore: 15},
	}
	filesByPath := map[string]ScannedFile{}
	filesByDir := map[string][]ScannedFile{}
	for _, file := range files {
		filesByPath[file.Path] = file
		filesByDir[file.Directory] = append(filesByDir[file.Directory], file)
	}
	snapshot := ProjectSnapshot{
		Root:             "C:\\repo",
		GeneratedAt:      time.Now(),
		AnalysisMode:     "security",
		Files:            files,
		FilesByPath:      filesByPath,
		FilesByDirectory: filesByDir,
		EntrypointFiles:  []string{"Source/ShooterGame/Main.cpp"},
		ManifestFiles:    []string{"ShooterGame.uproject"},
		UnrealProjects: []UnrealProject{
			{Name: "ShooterGame", Path: "ShooterGame.uproject", Modules: []string{"CheatGuardRuntime"}},
		},
		UnrealModules: []UnrealModule{
			{Name: "CheatGuardRuntime", Path: "Plugins/CheatGuard/Source/CheatGuardRuntime/CheatGuardRuntime.Build.cs"},
		},
		UnrealTypes: []UnrealReflectedType{
			{Name: "UKernelDriverBridge", Kind: "UCLASS", Module: "CheatGuardRuntime", File: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/KernelDriverBridge.cpp"},
			{Name: "UIoctlDispatch", Kind: "UCLASS", Module: "CheatGuardRuntime", File: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IoctlDispatch.cpp"},
			{Name: "UHandlePolicy", Kind: "UCLASS", Module: "CheatGuardRuntime", File: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/HandlePolicy.cpp"},
			{Name: "UMemoryScanner", Kind: "UCLASS", Module: "CheatGuardRuntime", File: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/MemoryScanner.cpp"},
			{Name: "URpcDispatchPipe", Kind: "UCLASS", Module: "CheatGuardRuntime", File: "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/RpcDispatchPipe.cpp"},
		},
	}

	shards := analyzer.planShards(snapshot, 8)
	names := []string{}
	for _, shard := range shards {
		names = append(names, shard.Name)
	}
	joined := strings.Join(names, ",")
	for _, expected := range []string{
		"security_driver",
		"security_ioctl",
		"security_handles",
		"security_memory",
		"security_rpc",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected specialized security shard %q in %v", expected, names)
		}
	}
}

func TestBuildWorkerPromptMentionsTruncatedContextHandling(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "commands_investigate.go"), []byte("package main\n\nfunc handleInvestigateCommand()\n{\n}\n"), 0o644); err != nil {
		t.Fatalf("write commands_investigate.go: %v", err)
	}
	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	shard := AnalysisShard{
		ID:           "shard-01",
		Name:         "commands",
		PrimaryFiles: []string{"commands_investigate.go"},
	}
	prompt := buildWorkerPrompt(snapshot, shard, "goal", "")
	if !strings.Contains(prompt, "snippet-limited or context-truncated") {
		t.Fatalf("expected truncated-context guidance in worker prompt\n%s", prompt)
	}
	if !strings.Contains(prompt, "may include only the first part of the file") {
		t.Fatalf("expected file excerpt note in worker prompt\n%s", prompt)
	}
}

func TestBuildWorkerPromptIncludesSemanticShardFocus(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:           "C:\\repo",
		GeneratedAt:    time.Now(),
		PrimaryStartup: "ShooterGame",
		ProjectEdges: []ProjectEdge{
			{
				Source:     "AShooterCharacter",
				Target:     "ServerFire",
				Type:       "unreal_rpc_server",
				Confidence: "high",
				Evidence:   []string{"Source/ShooterGame/Public/ShooterCharacter.h: UFUNCTION(Server)"},
			},
		},
		UnrealNetwork: []UnrealNetworkSurface{
			{
				TypeName:             "AShooterCharacter",
				File:                 "Source/ShooterGame/Public/ShooterCharacter.h",
				ServerRPCs:           []string{"ServerFire"},
				ReplicatedProperties: []string{"Ammo"},
			},
		},
		Files: []ScannedFile{
			{Path: "Source/ShooterGame/Public/ShooterCharacter.h", Directory: "Source/ShooterGame/Public", LineCount: 100},
		},
		FilesByPath: map[string]ScannedFile{
			"Source/ShooterGame/Public/ShooterCharacter.h": {Path: "Source/ShooterGame/Public/ShooterCharacter.h", Directory: "Source/ShooterGame/Public", LineCount: 100},
		},
		FilesByDirectory: map[string][]ScannedFile{
			"Source/ShooterGame/Public": {{Path: "Source/ShooterGame/Public/ShooterCharacter.h", Directory: "Source/ShooterGame/Public", LineCount: 100}},
		},
	}
	shard := AnalysisShard{
		ID:           "shard-03",
		Name:         "unreal_network",
		PrimaryFiles: []string{"Source/ShooterGame/Public/ShooterCharacter.h"},
	}
	prompt := buildWorkerPrompt(snapshot, shard, "analyze replication and authority", "")
	for _, expected := range []string{
		"Shard intent:",
		"Trace replication, RPC, and authority boundaries",
		"Semantic focus:",
		"Network surfaces:",
		"AShooterCharacter server=ServerFire",
		"Relevant typed project edges:",
		"AShooterCharacter -> ServerFire [unreal_rpc_server, confidence=high]",
		"For network shards, identify authority boundaries",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected semantic worker prompt to contain %q\n%s", expected, prompt)
		}
	}
}

func TestBuildReviewerPromptIncludesSemanticChecklist(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:        "C:\\repo",
		GeneratedAt: time.Now(),
		ProjectEdges: []ProjectEdge{
			{
				Source:     "AShooterCharacter",
				Target:     "ServerFire",
				Type:       "unreal_rpc_server",
				Confidence: "high",
				Evidence:   []string{"Source/ShooterGame/Public/ShooterCharacter.h: UFUNCTION(Server)"},
			},
		},
		UnrealNetwork: []UnrealNetworkSurface{
			{
				TypeName:             "AShooterCharacter",
				File:                 "Source/ShooterGame/Public/ShooterCharacter.h",
				ServerRPCs:           []string{"ServerFire"},
				ReplicatedProperties: []string{"Ammo"},
			},
		},
		Files: []ScannedFile{
			{Path: "Source/ShooterGame/Public/ShooterCharacter.h", Directory: "Source/ShooterGame/Public", LineCount: 100},
		},
		FilesByPath: map[string]ScannedFile{
			"Source/ShooterGame/Public/ShooterCharacter.h": {Path: "Source/ShooterGame/Public/ShooterCharacter.h", Directory: "Source/ShooterGame/Public", LineCount: 100},
		},
		FilesByDirectory: map[string][]ScannedFile{
			"Source/ShooterGame/Public": {{Path: "Source/ShooterGame/Public/ShooterCharacter.h", Directory: "Source/ShooterGame/Public", LineCount: 100}},
		},
	}
	shard := AnalysisShard{
		ID:           "shard-07",
		Name:         "unreal_network",
		PrimaryFiles: []string{"Source/ShooterGame/Public/ShooterCharacter.h"},
	}
	report := WorkerReport{
		Title:        "network",
		ScopeSummary: "Analyzes RPC and replicated state.",
		KeyFiles:     []string{"Source/ShooterGame/Public/ShooterCharacter.h"},
		EvidenceFiles: []string{
			"Source/ShooterGame/Public/ShooterCharacter.h",
		},
	}
	prompt := buildReviewerPrompt(snapshot, shard, report, "analyze replication and authority", WorkerReport{}, false)
	for _, expected := range []string{
		"Shard intent:",
		"Semantic focus:",
		"Semantic review checklist:",
		"Confirm the report separates server/client/multicast RPCs and replicated state.",
		"Reject if authority boundary or replication ownership is missing.",
		"Network surfaces:",
		"AShooterCharacter server=ServerFire",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected semantic reviewer prompt to contain %q\n%s", expected, prompt)
		}
	}
}

func TestNormalizeWorkerReportPreservesFactsAndInferences(t *testing.T) {
	shard := AnalysisShard{
		ID:           "shard-01",
		Name:         "runtime",
		PrimaryFiles: []string{"main.go"},
	}
	report := WorkerReport{
		Facts:         []string{"main.go defines startup", "main.go defines startup"},
		Inferences:    []string{"runtime is centralized", "runtime is centralized"},
		EvidenceFiles: []string{"main.go"},
	}
	normalizeWorkerReport(&report, shard)
	if len(report.Facts) != 1 || report.Facts[0] != "main.go defines startup" {
		t.Fatalf("expected normalized facts, got %#v", report.Facts)
	}
	if len(report.Inferences) != 1 || report.Inferences[0] != "runtime is centralized" {
		t.Fatalf("expected normalized inferences, got %#v", report.Inferences)
	}
}

func TestRunWorkerRetriesProviderErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	cfg := DefaultConfig(root)
	cfg.Model = "analysis-model"
	cfg.ProjectAnalysis.MaxProviderRetries = 2
	cfg.ProjectAnalysis.ProviderRetryDelayMs = 1
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	client := &flakyAnalysisClient{failuresRemaining: 1}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	analyzer.workerClient = client
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	report, err := analyzer.runWorker(context.Background(), snapshot, AnalysisShard{
		ID:           "shard-01",
		Name:         "runtime",
		PrimaryFiles: []string{"main.go"},
	}, "goal", "")
	if err != nil {
		t.Fatalf("runWorker returned error: %v", err)
	}
	if client.calls != 2 {
		t.Fatalf("expected retry to call provider twice, got %d", client.calls)
	}
	if len(report.Facts) == 0 || len(report.Inferences) == 0 {
		t.Fatalf("expected parsed facts and inferences, got %#v", report)
	}
}

func TestGroupedReportsForSynthesisMergesTinyOperationalShards(t *testing.T) {
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "project_manifest"},
		{ID: "shard-02", Name: "ops_scripts"},
		{ID: "shard-03", Name: "runtime"},
	}
	reports := []WorkerReport{
		{Title: "project_manifest", Facts: []string{"module is kernforge"}},
		{Title: "ops_scripts", Facts: []string{"release.ps1 exists"}},
		{Title: "runtime", Facts: []string{"main.go initializes runtime"}},
	}
	grouped := groupedReportsForSynthesis(shards, reports)
	if len(grouped) != 2 {
		t.Fatalf("expected 2 grouped sections, got %d", len(grouped))
	}
	foundOperational := false
	for _, item := range grouped {
		if item.Title == "Operational Metadata And Scripts" {
			foundOperational = true
			if len(item.Facts) != 2 {
				t.Fatalf("expected merged facts for tiny shards, got %#v", item.Facts)
			}
		}
	}
	if !foundOperational {
		t.Fatalf("expected operational grouping, got %#v", grouped)
	}
}

func TestEnsureFinalDocumentInsightsAppendsAppendixWhenMissing(t *testing.T) {
	text := "# Analysis\n\n### Agent Runtime\n\nBody\n"
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "runtime"},
	}
	reports := []WorkerReport{
		{
			Title:         "runtime",
			Facts:         []string{"main.go initializes runtime"},
			Inferences:    []string{"runtime is centralized"},
			EvidenceFiles: []string{"main.go", "agent.go"},
		},
	}
	out := ensureFinalDocumentInsights(text, ProjectSnapshot{}, shards, reports)
	if !strings.Contains(out, "## Evidence And Inference Appendix") {
		t.Fatalf("expected appendix in final document\n%s", out)
	}
	if !strings.Contains(out, "## Evidence Files Appendix") {
		t.Fatalf("expected evidence appendix in final document\n%s", out)
	}
	if !strings.Contains(out, "Facts:") || !strings.Contains(out, "Inferences:") {
		t.Fatalf("expected facts and inferences in appendix\n%s", out)
	}
	if !strings.Contains(out, "Evidence files:") {
		t.Fatalf("expected evidence files in appendix\n%s", out)
	}
	if strings.Contains(out, "Agent Runtime Group") {
		t.Fatalf("expected normalized heading name\n%s", out)
	}
	if !strings.Contains(out, "Agent Runtime") {
		t.Fatalf("expected normalized heading to remain present\n%s", out)
	}
}

func TestEnsureFinalDocumentInsightsUsesCoverageNotGlobalPresence(t *testing.T) {
	text := "# Analysis\n\n### Agent Runtime\n\nFacts:\n- runtime fact\n\nInferences:\n- runtime inference\n\nEvidence files:\n- main.go\n\n### Safety Control Plane\n\nBody only\n"
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "runtime"},
		{ID: "shard-02", Name: "verification"},
	}
	reports := []WorkerReport{
		{
			Title:         "runtime",
			Facts:         []string{"runtime fact"},
			Inferences:    []string{"runtime inference"},
			EvidenceFiles: []string{"main.go"},
		},
		{
			Title:         "verification",
			Facts:         []string{"verification fact"},
			Inferences:    []string{"verification inference"},
			EvidenceFiles: []string{"verify.go"},
		},
	}
	out := ensureFinalDocumentInsights(text, ProjectSnapshot{}, shards, reports)
	if !strings.Contains(out, "### Safety Control Plane: verification") {
		t.Fatalf("expected appendix entry for missing verification coverage\n%s", out)
	}
	if !strings.Contains(out, "verify.go") {
		t.Fatalf("expected evidence appendix for uncovered subsystem\n%s", out)
	}
	if strings.Contains(out, "### Agent Runtime: runtime") {
		t.Fatalf("expected runtime section to be omitted from delta appendix when already covered\n%s", out)
	}
}

func TestGroupedReportsForSynthesisAssignsArchitectureGroups(t *testing.T) {
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "runtime"},
		{ID: "shard-02", Name: "verification"},
		{ID: "shard-03", Name: "project_manifest"},
	}
	reports := []WorkerReport{
		{Title: "runtime"},
		{Title: "verification"},
		{Title: "project_manifest"},
	}
	grouped := groupedReportsForSynthesis(shards, reports)
	if len(grouped) < 2 {
		t.Fatalf("expected grouped sections, got %#v", grouped)
	}
	if grouped[0].Group != "Agent Runtime" {
		t.Fatalf("expected runtime group first, got %#v", grouped)
	}
	foundOperational := false
	for _, item := range grouped {
		if item.Group == "Operational Metadata" {
			foundOperational = true
			break
		}
	}
	if !foundOperational {
		t.Fatalf("expected operational metadata group, got %#v", grouped)
	}
}

func TestSynthesisGroupForShardAdaptsToTavernStyleModules(t *testing.T) {
	if got := synthesisGroupForShard(AnalysisShard{PrimaryFiles: []string{"TavernMaster/TavernMaster.cpp"}}, WorkerReport{}); got != "Security Control" {
		t.Fatalf("expected TavernMaster to map to Security Control, got %q", got)
	}
	if got := synthesisGroupForShard(AnalysisShard{PrimaryFiles: []string{"TavernWorker/PrefetchScanner.cpp"}}, WorkerReport{}); got != "Forensic Analysis" {
		t.Fatalf("expected TavernWorker to map to Forensic Analysis, got %q", got)
	}
	if got := synthesisGroupForShard(AnalysisShard{PrimaryFiles: []string{"External/aws/include/aws/auth/auth.h"}}, WorkerReport{}); got != "External Dependencies" {
		t.Fatalf("expected External to map to External Dependencies, got %q", got)
	}
}

func TestGroupedReportsForSynthesisPushesExternalDependenciesLast(t *testing.T) {
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "misc", PrimaryFiles: []string{"External/aws/include/aws/auth/auth.h"}},
		{ID: "shard-02", Name: "misc", PrimaryFiles: []string{"TavernMaster/TavernMaster.cpp"}},
	}
	reports := []WorkerReport{
		{Title: "aws auth"},
		{Title: "master"},
	}
	grouped := groupedReportsForSynthesis(shards, reports)
	if len(grouped) != 2 {
		t.Fatalf("expected 2 grouped sections, got %#v", grouped)
	}
	if grouped[0].Group != "Security Control" {
		t.Fatalf("expected product code before external dependencies, got %#v", grouped)
	}
	if grouped[1].Group != "External Dependencies" {
		t.Fatalf("expected external dependency section last, got %#v", grouped)
	}
}

func TestFallbackFinalDocumentIncludesEvidenceFilesAndGroupHeadings(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:       "C:\\git\\kernforge",
		TotalFiles: 2,
		TotalLines: 10,
	}
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "runtime", PrimaryFiles: []string{"main.go"}},
	}
	reports := []WorkerReport{
		{
			Title:            "runtime",
			Responsibilities: []string{"boot runtime"},
			Facts:            []string{"main.go initializes runtime"},
			EvidenceFiles:    []string{"main.go", "agent.go"},
		},
	}
	doc := fallbackFinalDocument(snapshot, shards, reports, "goal")
	if !strings.Contains(doc, "### Agent Runtime") {
		t.Fatalf("expected group heading in fallback doc\n%s", doc)
	}
	if !strings.Contains(doc, "Evidence files:") && !strings.Contains(doc, "Evidence files") {
		t.Fatalf("expected evidence files section in fallback doc\n%s", doc)
	}
}

func TestFallbackFinalDocumentIncludesAnalysisExecutionSummary(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:       "C:\\git\\kernforge",
		TotalFiles: 2,
		TotalLines: 10,
	}
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "runtime", PrimaryFiles: []string{"main.go"}, CacheStatus: "reused", InvalidationReason: "cache_hit"},
		{ID: "shard-02", Name: "network", PrimaryFiles: []string{"net.cpp"}, CacheStatus: "miss", InvalidationReason: "semantic_primary_changed"},
	}
	reports := []WorkerReport{
		{Title: "runtime", Responsibilities: []string{"boot runtime"}, EvidenceFiles: []string{"main.go"}},
		{Title: "network", Responsibilities: []string{"handle RPC"}, EvidenceFiles: []string{"net.cpp"}},
	}
	doc := fallbackFinalDocument(snapshot, shards, reports, "goal")
	for _, expected := range []string{
		"## Analysis Execution",
		"Semantic invalidation shards: 1",
		"Primary files kept the same content scope, but their semantic structure changed.",
	} {
		if !strings.Contains(doc, expected) {
			t.Fatalf("expected analysis execution summary to contain %q\n%s", expected, doc)
		}
	}
}

func TestSanitizeFileNamePreservesValidUnicodeBoundaries(t *testing.T) {
	name := sanitizeFileName("이 프로젝트의 전체 구조와 실행 흐름을 아주 자세하게 문서화")
	if strings.ContainsRune(name, utf8.RuneError) {
		t.Fatalf("expected sanitized filename without rune error: %q", name)
	}
	if strings.Contains(name, "�") {
		t.Fatalf("expected sanitized filename without replacement char: %q", name)
	}
	if name == "" {
		t.Fatalf("expected non-empty sanitized filename")
	}
}

func TestExecuteShardUsesDedicatedWorkerAndReviewerClients(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	worker := &namedAnalysisClient{
		name: "worker",
		text: `{"report":{"title":"core","scope_summary":"core","responsibilities":["boot"],"entry_points":["main.go"],"internal_flow":["main starts runtime"],"dependencies":[],"collaboration":[],"risks":[],"unknowns":[],"evidence_files":["main.go"],"narrative":"ok"}}`,
	}
	reviewer := &namedAnalysisClient{
		name: "reviewer",
		text: `{"decision":{"status":"approved","issues":[],"revision_prompt":""}}`,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	analyzer.workerClient = worker
	analyzer.reviewerClient = reviewer
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	shards := analyzer.planShards(snapshot, 2)
	if len(shards) == 0 {
		t.Fatalf("expected shards")
	}
	reuseState := analyzer.buildReuseState(nil, shards)
	_, _, _, err = analyzer.executeShard(context.Background(), snapshot, shards[0], "analyze", nil, reuseState)
	if err != nil {
		t.Fatalf("executeShard returned error: %v", err)
	}
	if worker.calls == 0 {
		t.Fatalf("expected dedicated worker client to be used")
	}
	if reviewer.calls == 0 {
		t.Fatalf("expected dedicated reviewer client to be used")
	}
}

func TestProjectAnalyzerIncrementalReuseSkipsWorkerAndReviewer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	cfg := DefaultConfig(root)
	cfg.Model = "stub-model"
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &stubAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}

	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	firstRun, err := analyzer.Run(context.Background(), "incremental goal", "")
	if err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	if firstRun.Summary.TotalShards == 0 {
		t.Fatalf("expected first run shards")
	}
	firstWorkerCalls := client.workerCalls
	firstReviewerCalls := client.reviewerCalls

	secondRun, err := analyzer.Run(context.Background(), "incremental goal", "")
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if client.workerCalls != firstWorkerCalls {
		t.Fatalf("expected incremental reuse to skip worker calls, before=%d after=%d", firstWorkerCalls, client.workerCalls)
	}
	if client.reviewerCalls != firstReviewerCalls {
		t.Fatalf("expected incremental reuse to skip reviewer calls, before=%d after=%d", firstReviewerCalls, client.reviewerCalls)
	}
	reused := false
	for _, shard := range secondRun.Shards {
		if shard.CacheStatus == "reused" {
			reused = true
			break
		}
	}
	if !reused {
		t.Fatalf("expected reused shard cache status")
	}
}

func TestSelectiveInvalidationWhenDependencyPrimaryChanges(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)

	currentShards := []AnalysisShard{
		{
			ID:                   "shard-01",
			Name:                 "core",
			PrimaryFiles:         []string{"core/a.go"},
			PrimaryFingerprint:   "core-new",
			ReferenceFingerprint: "",
		},
		{
			ID:                   "shard-02",
			Name:                 "feature",
			PrimaryFiles:         []string{"feature/b.go"},
			ReferenceFiles:       []string{"core/a.go"},
			PrimaryFingerprint:   "feature-same",
			ReferenceFingerprint: "feature-ref",
		},
	}
	previousRun := &ProjectAnalysisRun{
		Shards: []AnalysisShard{
			{
				ID:                   "old-01",
				PrimaryFiles:         []string{"core/a.go"},
				PrimaryFingerprint:   "core-old",
				ReferenceFingerprint: "",
			},
			{
				ID:                   "old-02",
				PrimaryFiles:         []string{"feature/b.go"},
				ReferenceFiles:       []string{"core/a.go"},
				PrimaryFingerprint:   "feature-same",
				ReferenceFingerprint: "feature-ref",
			},
		},
		Reports: []WorkerReport{
			{ShardID: "old-01"},
			{ShardID: "old-02"},
		},
		Reviews: []ReviewDecision{
			{Status: "approved"},
			{Status: "approved"},
		},
	}

	reuseState := analyzer.buildReuseState(previousRun, currentShards)
	if _, changed := reuseState.changedPrimaryFiles["core/a.go"]; !changed {
		t.Fatalf("expected changed primary file to be tracked")
	}
	_, _, reason, ok := analyzer.tryReuseShard(previousRun, currentShards[1], reuseState)
	if ok {
		t.Fatalf("expected dependent shard reuse to be denied")
	}
	if reason != "dependency_changed" {
		t.Fatalf("expected dependency_changed, got %q", reason)
	}
}

func TestSelectiveInvalidationWhenPrimarySemanticChanges(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)

	currentShards := []AnalysisShard{
		{
			ID:                         "shard-01",
			Name:                       "feature",
			PrimaryFiles:               []string{"feature/b.go"},
			PrimaryFingerprint:         "feature-same",
			PrimarySemanticFingerprint: "semantic-new",
			ReferenceFingerprint:       "feature-ref",
		},
	}
	previousRun := &ProjectAnalysisRun{
		Shards: []AnalysisShard{
			{
				ID:                         "old-01",
				PrimaryFiles:               []string{"feature/b.go"},
				PrimaryFingerprint:         "feature-same",
				PrimarySemanticFingerprint: "semantic-old",
				ReferenceFingerprint:       "feature-ref",
			},
		},
		Reports: []WorkerReport{
			{ShardID: "old-01"},
		},
		Reviews: []ReviewDecision{
			{Status: "approved"},
		},
	}

	reuseState := analyzer.buildReuseState(previousRun, currentShards)
	if _, changed := reuseState.changedSemanticFiles["feature/b.go"]; !changed {
		t.Fatalf("expected changed semantic file to be tracked")
	}
	_, _, reason, ok := analyzer.tryReuseShard(previousRun, currentShards[0], reuseState)
	if ok {
		t.Fatalf("expected shard reuse to be denied after semantic primary change")
	}
	if reason != "semantic_primary_changed" {
		t.Fatalf("expected semantic_primary_changed, got %q", reason)
	}
}

func TestSelectiveInvalidationWhenDependencySemanticChanges(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)

	currentShards := []AnalysisShard{
		{
			ID:                         "shard-01",
			Name:                       "core",
			PrimaryFiles:               []string{"core/a.go"},
			PrimaryFingerprint:         "core-same",
			PrimarySemanticFingerprint: "core-semantic-new",
		},
		{
			ID:                           "shard-02",
			Name:                         "feature",
			PrimaryFiles:                 []string{"feature/b.go"},
			ReferenceFiles:               []string{"core/a.go"},
			PrimaryFingerprint:           "feature-same",
			PrimarySemanticFingerprint:   "feature-semantic",
			ReferenceFingerprint:         "feature-ref",
			ReferenceSemanticFingerprint: "feature-ref-semantic",
		},
	}
	previousRun := &ProjectAnalysisRun{
		Shards: []AnalysisShard{
			{
				ID:                         "old-01",
				PrimaryFiles:               []string{"core/a.go"},
				PrimaryFingerprint:         "core-same",
				PrimarySemanticFingerprint: "core-semantic-old",
			},
			{
				ID:                           "old-02",
				PrimaryFiles:                 []string{"feature/b.go"},
				ReferenceFiles:               []string{"core/a.go"},
				PrimaryFingerprint:           "feature-same",
				PrimarySemanticFingerprint:   "feature-semantic",
				ReferenceFingerprint:         "feature-ref",
				ReferenceSemanticFingerprint: "feature-ref-semantic",
			},
		},
		Reports: []WorkerReport{
			{ShardID: "old-01"},
			{ShardID: "old-02"},
		},
		Reviews: []ReviewDecision{
			{Status: "approved"},
			{Status: "approved"},
		},
	}

	reuseState := analyzer.buildReuseState(previousRun, currentShards)
	if _, changed := reuseState.changedSemanticFiles["core/a.go"]; !changed {
		t.Fatalf("expected changed semantic dependency to be tracked")
	}
	_, _, reason, ok := analyzer.tryReuseShard(previousRun, currentShards[1], reuseState)
	if ok {
		t.Fatalf("expected dependent shard reuse to be denied after semantic dependency change")
	}
	if reason != "semantic_dependency_changed" {
		t.Fatalf("expected semantic_dependency_changed, got %q", reason)
	}
}

func TestBuildKnowledgePackIncludesAnalysisExecutionSummary(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:        "C:\\repo",
		GeneratedAt: time.Now(),
		AnalysisLenses: []AnalysisLens{
			{Type: "architecture"},
			{Type: "security_boundary"},
		},
	}
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "runtime", PrimaryFiles: []string{"main.go"}, CacheStatus: "reused", InvalidationReason: "cache_hit"},
		{ID: "shard-02", Name: "network", PrimaryFiles: []string{"net.cpp"}, CacheStatus: "miss", InvalidationReason: "semantic_dependency_changed", InvalidationChanges: []InvalidationChange{{Kind: "replicated_property_added", Scope: "unreal_network", Owner: "AShooterCharacter", Subject: "Ammo"}}},
	}
	reports := []WorkerReport{
		{Title: "runtime", Responsibilities: []string{"boot runtime"}, EvidenceFiles: []string{"main.go"}},
		{Title: "network", Responsibilities: []string{"handle RPC"}, EvidenceFiles: []string{"net.cpp"}},
	}
	pack := buildKnowledgePack(snapshot, shards, reports, "goal", "run-1")
	if pack.AnalysisExecution.TotalShards != 2 {
		t.Fatalf("expected total shard count in knowledge pack, got %+v", pack.AnalysisExecution)
	}
	if pack.AnalysisExecution.ReusedShards != 1 || pack.AnalysisExecution.MissedShards != 1 {
		t.Fatalf("expected reused/missed shard counts, got %+v", pack.AnalysisExecution)
	}
	if pack.AnalysisExecution.SemanticRecomputedShards != 1 {
		t.Fatalf("expected semantic invalidation count, got %+v", pack.AnalysisExecution)
	}
	foundSemanticReason := false
	for _, reason := range pack.AnalysisExecution.SemanticInvalidationReasons {
		if reason == "semantic_dependency_changed" {
			foundSemanticReason = true
			break
		}
	}
	if !foundSemanticReason {
		t.Fatalf("expected semantic invalidation reason in knowledge pack, got %+v", pack.AnalysisExecution)
	}
	if len(pack.AnalysisExecution.TopChangeClasses) == 0 || !strings.Contains(strings.Join(pack.AnalysisExecution.TopChangeClasses, ","), "replicated_property_added") {
		t.Fatalf("expected top change classes in analysis execution summary, got %+v", pack.AnalysisExecution)
	}
	digest := buildKnowledgeDigest(pack)
	if !strings.Contains(digest, "## Analysis Execution") || !strings.Contains(digest, "An upstream dependency kept the same file scope, but its semantic context changed.") {
		t.Fatalf("expected digest to include analysis execution summary\n%s", digest)
	}
	if !strings.Contains(digest, "Top change classes:") || !strings.Contains(digest, "replicated_property_added") {
		t.Fatalf("expected digest to include top change classes\n%s", digest)
	}
	if !strings.Contains(pack.ProjectSummary, "Executive focus: recent changes are concentrated on authority, replication, or security-sensitive boundaries.") {
		t.Fatalf("expected project summary to include lens-aware executive focus: %s", pack.ProjectSummary)
	}
}

func TestFallbackFinalDocumentIncludesSubsystemInvalidationReasons(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:       "C:\\git\\kernforge",
		TotalFiles: 2,
		TotalLines: 20,
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterCharacter", File: "net.cpp", ServerRPCs: []string{"ServerFire"}, ReplicatedProperties: []string{"Ammo"}},
		},
	}
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "unreal_network", PrimaryFiles: []string{"net.cpp"}, CacheStatus: "miss", InvalidationReason: "semantic_dependency_changed", InvalidationDiff: []string{"Replicated property added: AShooterCharacter -> Ammo"}, InvalidationChanges: []InvalidationChange{{Kind: "replicated_property_added", Scope: "unreal_network", Owner: "AShooterCharacter", Subject: "Ammo"}}},
	}
	reports := []WorkerReport{
		{
			Title:            "network",
			Responsibilities: []string{"handle RPC"},
			EvidenceFiles:    []string{"net.cpp"},
		},
	}
	doc := fallbackFinalDocument(snapshot, shards, reports, "goal")
	if !strings.Contains(doc, "An upstream dependency kept the same file scope, but RPC, replication, or authority semantics changed.") {
		t.Fatalf("expected subsystem invalidation reason in fallback document\n%s", doc)
	}
	if !strings.Contains(doc, "Invalidation evidence:") || !strings.Contains(doc, "AShooterCharacter server=ServerFire") {
		t.Fatalf("expected subsystem invalidation evidence in fallback document\n%s", doc)
	}
	if !strings.Contains(doc, "Invalidation diff:") || !strings.Contains(doc, "Replicated property added: AShooterCharacter -> Ammo") {
		t.Fatalf("expected subsystem invalidation diff in fallback document\n%s", doc)
	}
}

func TestBuildKnowledgePackIncludesSubsystemInvalidationReasons(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:        "C:\\repo",
		GeneratedAt: time.Now(),
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterCharacter", File: "net.cpp", ServerRPCs: []string{"ServerFire"}, ReplicatedProperties: []string{"Ammo"}},
		},
	}
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "unreal_network", PrimaryFiles: []string{"net.cpp"}, CacheStatus: "miss", InvalidationReason: "semantic_dependency_changed", InvalidationDiff: []string{"Replicated property added: AShooterCharacter -> Ammo"}, InvalidationChanges: []InvalidationChange{{Kind: "replicated_property_added", Scope: "unreal_network", Owner: "AShooterCharacter", Subject: "Ammo"}}},
		{ID: "shard-02", Name: "startup", PrimaryFiles: []string{"main.cpp"}, CacheStatus: "reused", InvalidationReason: "cache_hit"},
	}
	reports := []WorkerReport{
		{Title: "network", Responsibilities: []string{"handle RPC"}, EvidenceFiles: []string{"net.cpp"}},
		{Title: "startup", Responsibilities: []string{"boot runtime"}, EvidenceFiles: []string{"main.cpp"}},
	}
	pack := buildKnowledgePack(snapshot, shards, reports, "goal", "run-1")
	if len(pack.Subsystems) < 2 {
		t.Fatalf("expected subsystems in knowledge pack, got %+v", pack.Subsystems)
	}
	found := false
	for _, subsystem := range pack.Subsystems {
		if subsystem.Title != "network" {
			continue
		}
		found = true
		if len(subsystem.InvalidationReasons) == 0 || subsystem.InvalidationReasons[0] != "semantic_dependency_changed" {
			t.Fatalf("expected subsystem invalidation reason, got %+v", subsystem)
		}
		if len(subsystem.CacheStatuses) == 0 || subsystem.CacheStatuses[0] != "miss" {
			t.Fatalf("expected subsystem cache status, got %+v", subsystem)
		}
		if len(subsystem.InvalidationEvidence) == 0 {
			t.Fatalf("expected subsystem invalidation evidence, got %+v", subsystem)
		}
		if len(subsystem.InvalidationDiff) == 0 {
			t.Fatalf("expected subsystem invalidation diff, got %+v", subsystem)
		}
		if len(subsystem.InvalidationChanges) == 0 {
			t.Fatalf("expected subsystem invalidation changes, got %+v", subsystem)
		}
		if subsystem.InvalidationChanges[0].Kind == "" {
			t.Fatalf("expected structured invalidation change kind, got %+v", subsystem.InvalidationChanges)
		}
	}
	if !found {
		t.Fatalf("expected network subsystem in knowledge pack, got %+v", pack.Subsystems)
	}
	digest := buildKnowledgeDigest(pack)
	if !strings.Contains(digest, "- Invalidation reasons: An upstream dependency kept the same file scope, but RPC, replication, or authority semantics changed.") {
		t.Fatalf("expected subsystem invalidation reason in digest\n%s", digest)
	}
	if !strings.Contains(digest, "- Invalidation evidence:") || !strings.Contains(digest, "AShooterCharacter server=ServerFire") {
		t.Fatalf("expected subsystem invalidation evidence in digest\n%s", digest)
	}
	if !strings.Contains(digest, "- Invalidation diff:") || !strings.Contains(digest, "Replicated property added: AShooterCharacter -> Ammo") {
		t.Fatalf("expected subsystem invalidation diff in digest\n%s", digest)
	}
}

func TestBuildInvalidationDiffLinesDetectsAssetConfigDelta(t *testing.T) {
	previous := ProjectSnapshot{
		UnrealAssets: []UnrealAssetReference{
			{OwnerName: "AShooterHUD", File: "hud.cpp", ConfigKeys: []string{"GameDefaultMap"}},
		},
	}
	current := ProjectSnapshot{
		UnrealAssets: []UnrealAssetReference{
			{OwnerName: "AShooterHUD", File: "hud.cpp", ConfigKeys: []string{"GameDefaultMap", "MenuClass"}},
		},
	}
	diff := buildInvalidationDiffLines(previous, current, []string{"asset_config"}, []string{"hud.cpp"}, []string{"hud.cpp"}, []string{"semantic_dependency_changed"}, 4)
	found := false
	for _, item := range diff {
		if strings.Contains(item, "Config binding added: AShooterHUD -> MenuClass") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected config binding diff in %+v", diff)
	}
}

func TestDescribeAnalysisInvalidationReasonMapsSemanticReason(t *testing.T) {
	got := describeAnalysisInvalidationReason("semantic_dependency_changed")
	want := "An upstream dependency kept the same file scope, but its semantic context changed."
	if got != want {
		t.Fatalf("expected mapped invalidation reason %q, got %q", want, got)
	}
}

func TestDescribeAnalysisInvalidationReasonWithContextMapsUnrealNetworkReason(t *testing.T) {
	got := describeAnalysisInvalidationReasonWithContext("semantic_dependency_changed", []string{"unreal_network"})
	want := "An upstream dependency kept the same file scope, but RPC, replication, or authority semantics changed."
	if got != want {
		t.Fatalf("expected contextual invalidation reason %q, got %q", want, got)
	}
}

func TestBuildReviewerPromptIncludesPreviousReportForDependencyChanged(t *testing.T) {
	snapshot := ProjectSnapshot{
		FilesByPath: map[string]ScannedFile{
			"feature/b.go": {Path: "feature/b.go", LineCount: 10},
		},
		Root: ".",
	}
	shard := AnalysisShard{
		ID:                 "shard-02",
		Name:               "feature",
		PrimaryFiles:       []string{"feature/b.go"},
		CacheStatus:        "miss",
		InvalidationReason: "dependency_changed",
	}
	current := WorkerReport{
		Title:            "feature",
		ScopeSummary:     "new report",
		Responsibilities: []string{"serve requests"},
		InternalFlow:     []string{"flow"},
		EvidenceFiles:    []string{"feature/b.go"},
	}
	previous := WorkerReport{
		Title:            "feature-old",
		ScopeSummary:     "old report",
		Responsibilities: []string{"old ownership"},
	}
	prompt := buildReviewerPrompt(snapshot, shard, current, "goal", previous, true)
	if !strings.Contains(prompt, "Previous approved report for diff-aware review") {
		t.Fatalf("expected previous report block in reviewer prompt\n%s", prompt)
	}
	if !strings.Contains(prompt, "\"old ownership\"") {
		t.Fatalf("expected previous report contents in reviewer prompt\n%s", prompt)
	}
}

func TestRunDowngradesToDraftWhenNoShardApproved(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	cfg := DefaultConfig(root)
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &draftAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), "draft on failed reviews", "")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Summary.Status != "draft" {
		t.Fatalf("expected draft status, got %s", run.Summary.Status)
	}
	if !strings.HasPrefix(run.FinalDocument, "# Draft Analysis") {
		t.Fatalf("expected draft warning prefix\n%s", run.FinalDocument)
	}
}

func TestEnsureFinalDocumentInsightsCompactsExternalDependencyAppendix(t *testing.T) {
	document := "# Project Analysis\n\n## Subsystem Breakdown\n\n### Core Application\n\n#### Core Runtime\n\nResponsibilities:\n- boot\n"
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "runtime", PrimaryFiles: []string{"main.go"}},
		{ID: "shard-02", Name: "aws_auth", PrimaryFiles: []string{"External/aws/auth/Auth.h"}},
		{ID: "shard-03", Name: "zydis", PrimaryFiles: []string{"External/Zydis/Zydis.h"}},
	}
	reports := []WorkerReport{
		{
			Title:            "Core Runtime",
			Responsibilities: []string{"boot"},
			EvidenceFiles:    []string{"main.go"},
			Facts:            []string{"main.go initializes runtime"},
		},
		{
			Title:         "AWS Authentication",
			EvidenceFiles: []string{"External/aws/auth/Auth.h", "External/aws/auth/Credentials.h"},
			Facts:         []string{"aws auth provides credentials"},
			Inferences:    []string{"used for cloud authentication"},
			Dependencies:  []string{"AWS CRT"},
		},
		{
			Title:         "Zydis Decoder",
			EvidenceFiles: []string{"External/Zydis/Zydis.h", "External/Zydis/Decoder.h"},
			Facts:         []string{"zydis decodes instructions"},
			Inferences:    []string{"used for binary analysis"},
			Dependencies:  []string{"Zycore"},
		},
	}
	got := ensureFinalDocumentInsights(document, ProjectSnapshot{}, shards, reports)
	if !strings.Contains(got, "### External Dependencies: Dependency Catalog") {
		t.Fatalf("expected compact external dependency catalog\n%s", got)
	}
	if strings.Contains(got, "### External Dependencies: AWS Authentication") {
		t.Fatalf("expected external appendix entries to be compacted\n%s", got)
	}
}

func TestEnsureFinalDocumentInsightsInjectsPrimaryStartupProject(t *testing.T) {
	document := "# Project Analysis\n\n## 1. Project Overview\n\nOverview text.\n\n## 3. Execution Flow And Entry Points\n\nFlow text.\n"
	snapshot := ProjectSnapshot{
		PrimaryStartup:  "Tavern",
		StartupProjects: []string{"Tavern", "TavernWorker", "TavernDart"},
		SolutionProjects: []SolutionProject{
			{Name: "Tavern", EntryFiles: []string{"Tavern/Tavern.cpp"}},
		},
	}
	got := ensureFinalDocumentInsights(document, snapshot, nil, nil)
	if !strings.Contains(got, "Primary startup project:") {
		t.Fatalf("expected startup project snippet\n%s", got)
	}
	if !strings.Contains(got, "`Tavern`") {
		t.Fatalf("expected Tavern in startup snippet\n%s", got)
	}
	if !strings.Contains(got, "`Tavern/Tavern.cpp`") {
		t.Fatalf("expected startup entry file in snippet\n%s", got)
	}
}

func TestEnsureFinalDocumentInsightsInjectsSecuritySurfaceDecomposition(t *testing.T) {
	document := "# Project Analysis\n\n## 3. Execution Flow And Entry Points\n\nFlow text.\n\n## Subsystem Breakdown\n\nSubsystem text.\n"
	snapshot := ProjectSnapshot{
		AnalysisMode: "security",
	}
	shards := []AnalysisShard{
		{ID: "shard-driver", Name: "security_driver", PrimaryFiles: []string{"driver/DriverEntry.cpp"}},
		{ID: "shard-ioctl", Name: "security_ioctl", PrimaryFiles: []string{"driver/IoctlDispatch.cpp"}},
		{ID: "shard-handles", Name: "security_handles", PrimaryFiles: []string{"agent/HandlePolicy.cpp"}},
		{ID: "shard-memory", Name: "security_memory", PrimaryFiles: []string{"agent/MemoryScanner.cpp"}},
		{ID: "shard-rpc", Name: "security_rpc", PrimaryFiles: []string{"agent/RpcDispatchPipe.cpp"}},
	}
	reports := []WorkerReport{
		{
			Title:            "Driver Security",
			Responsibilities: []string{"Initialize the driver trust boundary and privileged callbacks."},
			Facts:            []string{"DriverEntry registers the process and image monitoring callbacks."},
			EntryPoints:      []string{"DriverEntry"},
			KeyFiles:         []string{"driver/DriverEntry.cpp"},
			Risks:            []string{"Unsigned or weakly validated load paths weaken the privileged boundary."},
		},
		{
			Title:            "IOCTL Security",
			Responsibilities: []string{"Validate device control dispatch paths and IOCTL policy."},
			Facts:            []string{"DeviceIoControl requests converge in the central dispatch table."},
			InternalFlow:     []string{"DispatchDeviceControl -> ValidateIoctl -> ExecuteRequest"},
			EvidenceFiles:    []string{"driver/IoctlDispatch.cpp"},
		},
		{
			Title:            "Handle Security",
			Responsibilities: []string{"Restrict hostile process handle opens and access masks."},
			Inferences:       []string{"OpenProcess and DuplicateHandle checks are the core escalation gate."},
			KeyFiles:         []string{"agent/HandlePolicy.cpp"},
		},
		{
			Title:            "Memory Security",
			Responsibilities: []string{"Scan remote memory regions and guard write-sensitive paths."},
			Facts:            []string{"Remote memory reads are routed through the scanner control loop."},
			KeyFiles:         []string{"agent/MemoryScanner.cpp"},
		},
		{
			Title:            "RPC Security",
			Responsibilities: []string{"Validate IPC and command dispatch before execution."},
			Facts:            []string{"Named pipe commands are decoded before reaching the worker actions."},
			EntryPoints:      []string{"OnPipeMessage"},
			KeyFiles:         []string{"agent/RpcDispatchPipe.cpp"},
		},
	}
	got := ensureFinalDocumentInsights(document, snapshot, shards, reports)
	if !strings.Contains(got, "## Security Surface Decomposition") {
		t.Fatalf("expected security surface section\n%s", got)
	}
	if !strings.Contains(got, "### Driver Surface") || !strings.Contains(got, "### IOCTL Surface") || !strings.Contains(got, "### RPC Surface") {
		t.Fatalf("expected specialized security subsections\n%s", got)
	}
	if !strings.Contains(got, "`driver/IoctlDispatch.cpp`") || !strings.Contains(got, "`agent/RpcDispatchPipe.cpp`") {
		t.Fatalf("expected key security files in decomposition\n%s", got)
	}
	if strings.Index(got, "## Security Surface Decomposition") > strings.Index(got, "## Subsystem Breakdown") {
		t.Fatalf("expected security decomposition before subsystem breakdown\n%s", got)
	}
}

func TestIsVisualStudioCppProjectDetectsSolutionManifest(t *testing.T) {
	snapshot := ProjectSnapshot{
		ManifestFiles: []string{"Tavern.sln", "Tavern/Tavern.vcxproj"},
	}
	if !isVisualStudioCppProject(snapshot) {
		t.Fatalf("expected Visual Studio / C++ project detection")
	}
}

func TestEnrichSolutionMetadataFindsStartupProjectAndEntryFiles(t *testing.T) {
	root := t.TempDir()
	sln := `Microsoft Visual Studio Solution File, Format Version 12.00
Project("{GUID}") = "Tavern", "Tavern/Tavern.vcxproj", "{A}"
Project("{GUID}") = "Common", "Common/Common.vcxproj", "{B}"
EndProject
`
	vcxprojApp := `<Project><PropertyGroup Label="Configuration"><ConfigurationType>Application</ConfigurationType></PropertyGroup></Project>`
	vcxprojLib := `<Project><PropertyGroup Label="Configuration"><ConfigurationType>DynamicLibrary</ConfigurationType></PropertyGroup></Project>`
	if err := os.MkdirAll(filepath.Join(root, "Tavern"), 0o755); err != nil {
		t.Fatalf("mkdir Tavern: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Common"), 0o755); err != nil {
		t.Fatalf("mkdir Common: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tavern.sln"), []byte(sln), 0o644); err != nil {
		t.Fatalf("write sln: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tavern", "Tavern.vcxproj"), []byte(vcxprojApp), 0o644); err != nil {
		t.Fatalf("write app vcxproj: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Common", "Common.vcxproj"), []byte(vcxprojLib), 0o644); err != nil {
		t.Fatalf("write lib vcxproj: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Tavern", "main.cpp"), []byte("int main() { return 0; }\n"), 0o644); err != nil {
		t.Fatalf("write main.cpp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Common", "helper.cpp"), []byte("void helper() {}\n"), 0o644); err != nil {
		t.Fatalf("write helper.cpp: %v", err)
	}

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	if snapshot.PrimaryStartup != "Tavern" {
		t.Fatalf("expected Tavern as primary startup, got %q", snapshot.PrimaryStartup)
	}
	if len(snapshot.SolutionProjects) != 2 {
		t.Fatalf("expected solution projects to be discovered, got %d", len(snapshot.SolutionProjects))
	}
	if !containsString(snapshot.StartupProjects, "Tavern") {
		t.Fatalf("expected Tavern in startup candidates: %#v", snapshot.StartupProjects)
	}
	if !containsString(snapshot.EntrypointFiles, "Tavern/main.cpp") {
		t.Fatalf("expected solution entrypoint file in snapshot: %#v", snapshot.EntrypointFiles)
	}
}

func TestFallbackFinalDocumentIncludesPrimaryStartupProject(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:           "C:\\repo",
		PrimaryStartup: "Tavern",
		TotalFiles:     1,
		TotalLines:     10,
	}
	doc := fallbackFinalDocument(snapshot, nil, nil, "goal")
	if !strings.Contains(doc, "Inferred primary startup project: `Tavern`") {
		t.Fatalf("expected fallback doc to include primary startup project\n%s", doc)
	}
}

func TestInferRuntimeEdgesFindsProjectReferencesAndDynamicLoads(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(path string, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	mustWrite(filepath.Join(root, "Tavern", "Tavern.vcxproj"), `<Project><PropertyGroup Label="Configuration"><ConfigurationType>Application</ConfigurationType></PropertyGroup><ItemGroup><ProjectReference Include="..\TavernMaster\TavernMaster.vcxproj" /></ItemGroup></Project>`)
	mustWrite(filepath.Join(root, "TavernMaster", "TavernMaster.vcxproj"), `<Project><PropertyGroup Label="Configuration"><ConfigurationType>DynamicLibrary</ConfigurationType></PropertyGroup><ItemGroup><ProjectReference Include="..\TavernWorker\TavernWorker.vcxproj" /></ItemGroup></Project>`)
	mustWrite(filepath.Join(root, "TavernCmn", "TavernCmn.vcxproj"), `<Project><PropertyGroup Label="Configuration"><ConfigurationType>DynamicLibrary</ConfigurationType></PropertyGroup></Project>`)
	mustWrite(filepath.Join(root, "TavernUpd", "TavernUpd.vcxproj"), `<Project><PropertyGroup Label="Configuration"><ConfigurationType>DynamicLibrary</ConfigurationType></PropertyGroup></Project>`)
	mustWrite(filepath.Join(root, "TavernCmn", "dllmain.cpp"), `LoadLibrary(L"TavernUpd.bin");`)
	snapshot := ProjectSnapshot{
		Root: root,
		Files: []ScannedFile{
			{Path: "TavernCmn/dllmain.cpp"},
		},
		SolutionProjects: []SolutionProject{
			{Name: "Tavern", Path: "Tavern/Tavern.vcxproj", Directory: "Tavern", ProjectReferences: parseVCXProjProjectReferences(root, "Tavern/Tavern.vcxproj")},
			{Name: "TavernMaster", Path: "TavernMaster/TavernMaster.vcxproj", Directory: "TavernMaster", ProjectReferences: parseVCXProjProjectReferences(root, "TavernMaster/TavernMaster.vcxproj")},
			{Name: "TavernWorker", Path: "TavernWorker/TavernWorker.vcxproj", Directory: "TavernWorker"},
			{Name: "TavernCmn", Path: "TavernCmn/TavernCmn.vcxproj", Directory: "TavernCmn"},
			{Name: "TavernUpd", Path: "TavernUpd/TavernUpd.vcxproj", Directory: "TavernUpd"},
		},
	}
	edges := inferRuntimeEdges(snapshot, snapshot.SolutionProjects)
	foundAppToMaster := false
	foundMasterToWorker := false
	foundCmnToUpd := false
	for _, edge := range edges {
		if edge.Source == "Tavern" && edge.Target == "TavernMaster" {
			if edge.Confidence != "high" {
				t.Fatalf("expected high confidence for project reference edge, got %#v", edge)
			}
			foundAppToMaster = true
		}
		if edge.Source == "TavernMaster" && edge.Target == "TavernWorker" {
			if edge.Confidence != "high" {
				t.Fatalf("expected high confidence for worker edge, got %#v", edge)
			}
			foundMasterToWorker = true
		}
		if edge.Source == "TavernCmn" && edge.Target == "TavernUpd" {
			if edge.Kind != "dynamic_load" || edge.Confidence != "high" {
				t.Fatalf("expected high-confidence dynamic load edge, got %#v", edge)
			}
			foundCmnToUpd = true
		}
	}
	if !foundAppToMaster || !foundMasterToWorker || !foundCmnToUpd {
		t.Fatalf("expected runtime edges, got %#v", edges)
	}
}

func TestHighConfidenceRuntimeEdgesExcludeStringReferencesFromDocument(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:           "C:\\repo",
		PrimaryStartup: "Tavern",
		RuntimeEdges: []RuntimeEdge{
			{Source: "Tavern", Target: "TavernMaster", Kind: "project_reference", Confidence: "high", Evidence: []string{"Tavern/Tavern.vcxproj"}},
			{Source: "TavernWorker", Target: "TavernUpd", Kind: "string_reference", Confidence: "low", Evidence: []string{"TavernWorker/worker.cpp"}},
		},
		TotalFiles: 1,
		TotalLines: 10,
	}
	doc := fallbackFinalDocument(snapshot, nil, nil, "goal")
	if !strings.Contains(doc, "Tavern -> TavernMaster") {
		t.Fatalf("expected high-confidence runtime edge in document\n%s", doc)
	}
	if strings.Contains(doc, "TavernWorker -> TavernUpd") {
		t.Fatalf("expected low-confidence runtime edge to be excluded from document\n%s", doc)
	}
}

func TestNormalizeUnexpectedLocaleArtifacts(t *testing.T) {
	input := "## Project Overview\n\n主要启动链\n\n运行时图\n"
	got := normalizeUnexpectedLocaleArtifacts(input)
	if strings.Contains(got, "主要启动链") || strings.Contains(got, "运行时图") {
		t.Fatalf("expected locale artifacts to be normalized\n%s", got)
	}
	if !strings.Contains(got, "Primary Startup Chain") || !strings.Contains(got, "Runtime Graph") {
		t.Fatalf("expected normalized headings in output\n%s", got)
	}
}

func TestCanonicalProjectNameNormalizesTavernComnAlias(t *testing.T) {
	got := canonicalProjectName("TavernComn", []string{"Tavern", "TavernCmn", "TavernUpd"})
	if got != "TavernCmn" {
		t.Fatalf("expected TavernComn alias to normalize to TavernCmn, got %q", got)
	}
}

func TestBuildOperationalChainUsesCollaborationAndOperationalEvidence(t *testing.T) {
	snapshot := ProjectSnapshot{
		PrimaryStartup: "Tavern",
		SolutionProjects: []SolutionProject{
			{Name: "Tavern", Directory: "Tavern"},
			{Name: "TavernMaster", Directory: "TavernMaster"},
			{Name: "TavernWorker", Directory: "TavernWorker"},
			{Name: "TavernCmn", Directory: "TavernCmn"},
			{Name: "TavernUpd", Directory: "TavernUpd"},
		},
		RuntimeEdges: []RuntimeEdge{
			{Source: "Tavern", Target: "TavernMaster", Kind: "dynamic_load", Confidence: "high", Evidence: []string{"Tavern/TavernWorkerManager.cpp"}},
			{Source: "TavernWorker", Target: "TavernComn", Kind: "string_reference", Confidence: "low", Evidence: []string{"TavernWorker/TavernUpdManager.cpp"}},
			{Source: "TavernCmn", Target: "TavernUpd", Kind: "dynamic_load", Confidence: "high", Evidence: []string{"TavernCmn/dllmain.cpp"}},
		},
	}
	reports := []WorkerReport{
		{
			Title:         "TavernMaster Analysis",
			EvidenceFiles: []string{"TavernMaster/TavernMaster.cpp"},
			Collaboration: []string{"TavernMaster coordinates with TavernWorker for forensic operations."},
			InternalFlow:  []string{"TavernMaster launches worker-side detection orchestration."},
		},
	}
	chain := buildOperationalChain(snapshot, reports)
	got := []string{}
	for _, edge := range chain {
		got = append(got, edge.Source+"->"+edge.Target)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "TavernMaster->TavernWorker") {
		t.Fatalf("expected TavernMaster->TavernWorker in operational chain, got %v", got)
	}
	if !strings.Contains(joined, "TavernWorker->TavernCmn") {
		t.Fatalf("expected TavernWorker->TavernCmn in operational chain, got %v", got)
	}
	if !strings.Contains(joined, "TavernCmn->TavernUpd") {
		t.Fatalf("expected TavernCmn->TavernUpd in operational chain, got %v", got)
	}
}

func TestEstimateShardCountCanExceedConcurrentAgents(t *testing.T) {
	analyzer := &projectAnalyzer{
		analysisCfg: ProjectAnalysisConfig{
			MinAgents:      2,
			MaxAgents:      4,
			MaxTotalShards: 32,
		},
	}
	snapshot := ProjectSnapshot{
		TotalFiles:  2200,
		TotalLines:  260000,
		Directories: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
	}
	concurrency := analyzer.estimateAgentCount(snapshot)
	if concurrency != 4 {
		t.Fatalf("expected concurrent agents capped at 4, got %d", concurrency)
	}
	totalShards := analyzer.estimateShardCount(snapshot, concurrency)
	if totalShards <= concurrency {
		t.Fatalf("expected total shard count to exceed concurrent agents, got shards=%d concurrency=%d", totalShards, concurrency)
	}
}

func TestChooseAnalysisLensesIncludesGoalSpecificLenses(t *testing.T) {
	lenses := chooseAnalysisLenses("analyze runtime flow and named pipe ipc command dispatch", "")
	names := []string{}
	for _, lens := range lenses {
		names = append(names, lens.Type)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "architecture") || !strings.Contains(joined, "runtime_flow") || !strings.Contains(joined, "ipc") {
		t.Fatalf("expected architecture, runtime_flow, and ipc lenses, got %v", names)
	}
}

func TestChooseAnalysisLensesRespectsExplicitSecurityMode(t *testing.T) {
	lenses := chooseAnalysisLenses("map worker startup", "security")
	names := []string{}
	for _, lens := range lenses {
		names = append(names, lens.Type)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "architecture") || !strings.Contains(joined, "security_boundary") {
		t.Fatalf("expected architecture and security_boundary lenses, got %v", names)
	}
}

func TestRefineAnalysisLensesForSnapshotAddsUnrealLenses(t *testing.T) {
	snapshot := ProjectSnapshot{
		UnrealProjects: []UnrealProject{{Name: "ShooterGame", Path: "ShooterGame.uproject"}},
	}
	lenses := refineAnalysisLensesForSnapshot(snapshot, []AnalysisLens{{Type: "architecture"}})
	names := []string{}
	for _, lens := range lenses {
		names = append(names, lens.Type)
	}
	joined := strings.Join(names, ",")
	for _, expected := range []string{"unreal_module", "unreal_gameplay", "unreal_network"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected %s lens in %v", expected, names)
		}
	}
}

func TestScoreFileImportancePrioritizesEntrypointsAndIpcSignals(t *testing.T) {
	analyzer := &projectAnalyzer{}
	snapshot := ProjectSnapshot{
		Files: []ScannedFile{
			{Path: "src/main.cpp", Directory: "src", IsEntrypoint: true, LineCount: 120, Imports: []string{"ipc/pipe.h"}},
			{Path: "External/aws/include/aws/common/command_line_parser.h", Directory: "External/aws/include/aws/common", LineCount: 400},
		},
		FilesByPath: map[string]ScannedFile{
			"src/main.cpp": {Path: "src/main.cpp", Directory: "src", IsEntrypoint: true, LineCount: 120, Imports: []string{"ipc/pipe.h"}},
			"External/aws/include/aws/common/command_line_parser.h": {Path: "External/aws/include/aws/common/command_line_parser.h", Directory: "External/aws/include/aws/common", LineCount: 400},
		},
		FilesByDirectory: map[string][]ScannedFile{
			"src":                             {{Path: "src/main.cpp", Directory: "src", IsEntrypoint: true, LineCount: 120, Imports: []string{"ipc/pipe.h"}}},
			"External/aws/include/aws/common": {{Path: "External/aws/include/aws/common/command_line_parser.h", Directory: "External/aws/include/aws/common", LineCount: 400}},
		},
		ReverseImportGraph: map[string][]string{
			"src/main.cpp": {"src/manager.cpp"},
		},
	}
	analyzer.scoreFileImportance(&snapshot, []AnalysisLens{{Type: "ipc"}, {Type: "runtime_flow"}})
	mainFile := snapshot.FilesByPath["src/main.cpp"]
	externalFile := snapshot.FilesByPath["External/aws/include/aws/common/command_line_parser.h"]
	if mainFile.ImportanceScore <= externalFile.ImportanceScore {
		t.Fatalf("expected main.cpp to outrank external parser file: main=%d external=%d", mainFile.ImportanceScore, externalFile.ImportanceScore)
	}
}

func TestBuildProjectEdgesIncludesDependencyRuntimeAndConfigEdges(t *testing.T) {
	snapshot := ProjectSnapshot{
		PrimaryStartup: "app",
		ManifestFiles:  []string{"go.mod"},
		ImportGraph: map[string][]string{
			"src/main.go": {"src/ipc.go", "External/lib/foo.h"},
		},
		FilesByPath: map[string]ScannedFile{
			"src/main.go":        {Path: "src/main.go", Directory: "src"},
			"src/ipc.go":         {Path: "src/ipc.go", Directory: "src"},
			"External/lib/foo.h": {Path: "External/lib/foo.h", Directory: "External/lib"},
		},
		RuntimeEdges: []RuntimeEdge{
			{Source: "app", Target: "worker", Kind: "process_spawn", Confidence: "high", Evidence: []string{"src/main.go"}},
		},
	}
	edges := buildProjectEdges(snapshot)
	kinds := []string{}
	for _, edge := range edges {
		kinds = append(kinds, edge.Type)
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "dependency_edge") || !strings.Contains(joined, "runtime_edge") || !strings.Contains(joined, "config_edge") || !strings.Contains(joined, "external_edge") {
		t.Fatalf("expected dependency/runtime/config/external edges, got %v", kinds)
	}
}

func TestScanProjectExtractsUnrealMetadata(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("ShooterGame.uproject", `{"Modules":[{"Name":"ShooterGame"}],"Plugins":[{"Name":"GameplayAbilities","Enabled":true}]}`)
	mustWrite("Plugins/CheatGuard/CheatGuard.uplugin", `{"EnabledByDefault":true,"Modules":[{"Name":"CheatGuard"}]}`)
	mustWrite("Source/ShooterGame/ShooterGame.Build.cs", `PublicDependencyModuleNames.AddRange(new string[] { "Core", "Engine", "GameplayAbilities" }); PrivateDependencyModuleNames.AddRange(new string[] { "Slate" });`)
	mustWrite("Source/ShooterGame.Target.cs", `public class ShooterGameTarget : TargetRules { public ShooterGameTarget(TargetInfo Target) : base(Target) { Type = TargetType.Game; ExtraModuleNames.AddRange(new string[] { "ShooterGame" }); } }`)
	mustWrite("Source/ShooterGame/Public/ShooterGameMode.h", `UCLASS(Blueprintable) class SHOOTERGAME_API AShooterGameMode : public AGameModeBase { GENERATED_BODY() public: UPROPERTY(EditAnywhere) int32 MaxBots; UFUNCTION(BlueprintCallable) void StartMatchFlow(); };`)
	mustWrite("Source/ShooterGame/Public/ShooterPlayerController.h", `UCLASS() class SHOOTERGAME_API AShooterPlayerController : public APlayerController { GENERATED_BODY() public: UFUNCTION(BlueprintCallable) void OpenScoreboard(); };`)
	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	snapshot.AnalysisLenses = refineAnalysisLensesForSnapshot(snapshot, chooseAnalysisLenses("analyze unreal gameplay framework", ""))
	if len(snapshot.UnrealProjects) != 1 {
		t.Fatalf("expected 1 unreal project, got %d", len(snapshot.UnrealProjects))
	}
	if snapshot.PrimaryUnrealModule != "ShooterGame" {
		t.Fatalf("expected primary unreal module ShooterGame, got %q", snapshot.PrimaryUnrealModule)
	}
	if len(snapshot.UnrealTargets) != 1 || snapshot.UnrealTargets[0].TargetType != "Game" {
		t.Fatalf("expected one Game target, got %+v", snapshot.UnrealTargets)
	}
	if len(snapshot.UnrealModules) == 0 || snapshot.UnrealModules[0].Name != "ShooterGame" {
		t.Fatalf("expected ShooterGame unreal module, got %+v", snapshot.UnrealModules)
	}
	if len(snapshot.UnrealTypes) < 2 {
		t.Fatalf("expected unreal reflected types, got %+v", snapshot.UnrealTypes)
	}
}

func TestBuildProjectEdgesIncludesUnrealModuleEdges(t *testing.T) {
	snapshot := ProjectSnapshot{
		PrimaryUnrealModule: "ShooterGame",
		ManifestFiles:       []string{"ShooterGame.uproject", "Source/ShooterGame.Target.cs", "Source/ShooterGame/ShooterGame.Build.cs"},
		UnrealProjects: []UnrealProject{
			{Name: "ShooterGame", Path: "ShooterGame.uproject", Modules: []string{"ShooterGame"}, Plugins: []string{"GameplayAbilities"}},
		},
		UnrealTargets: []UnrealTarget{
			{Name: "ShooterGame", Path: "Source/ShooterGame.Target.cs", TargetType: "Game", Modules: []string{"ShooterGame"}},
		},
		UnrealModules: []UnrealModule{
			{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs", PublicDependencies: []string{"Core", "Engine"}, PrivateDependencies: []string{"Slate"}},
		},
		UnrealTypes: []UnrealReflectedType{
			{Name: "AShooterGameMode", Kind: "UCLASS", BaseClass: "AGameModeBase", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterGameMode.h", GameplayRole: "game_mode"},
			{Name: "AShooterPlayerController", Kind: "UCLASS", BaseClass: "APlayerController", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterPlayerController.h", GameplayRole: "player_controller"},
		},
	}
	edges := buildProjectEdges(snapshot)
	joined := []string{}
	for _, edge := range edges {
		joined = append(joined, edge.Type+":"+edge.Source+"->"+edge.Target+":"+edge.Attributes["kind"])
	}
	text := strings.Join(joined, ",")
	for _, expected := range []string{
		"module_edge:ShooterGame->Core:public_dependency",
		"module_edge:ShooterGame->Engine:public_dependency",
		"module_edge:ShooterGame->Slate:private_dependency",
		"reflection_edge:AShooterGameMode->AGameModeBase:UCLASS",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected unreal module edge %q in %s", expected, text)
		}
	}
}

func TestExtractUnrealReflectedTypesDetectsGameplayRoles(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("Source/ShooterGame/ShooterGame.Build.cs", `PublicDependencyModuleNames.AddRange(new string[] { "Core", "Engine" });`)
	mustWrite("Source/ShooterGame/Public/ShooterGameInstance.h", `UCLASS() class UShooterGameInstance : public UGameInstance { GENERATED_BODY() public: UPROPERTY(EditAnywhere) int32 Warmup; UFUNCTION(BlueprintCallable) void BootServices(); UFUNCTION(BlueprintNativeEvent) void NotifyReady(); };`)
	mustWrite("Source/ShooterGame/Public/ShooterWorldSubsystem.h", `UCLASS() class UShooterWorldSubsystem : public UWorldSubsystem { GENERATED_BODY() };`)
	snapshot := ProjectSnapshot{
		Root: root,
		Files: []ScannedFile{
			{Path: "Source/ShooterGame/Public/ShooterGameInstance.h"},
			{Path: "Source/ShooterGame/Public/ShooterWorldSubsystem.h"},
		},
		UnrealModules: []UnrealModule{
			{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs"},
		},
	}
	types := extractUnrealReflectedTypes(snapshot)
	if len(types) < 2 {
		t.Fatalf("expected reflected types, got %+v", types)
	}
	joined := []string{}
	for _, item := range types {
		joined = append(joined, item.Name+":"+item.GameplayRole+":"+item.BaseClass)
	}
	text := strings.Join(joined, ",")
	if !strings.Contains(text, "UShooterGameInstance:game_instance:UGameInstance") {
		t.Fatalf("expected game instance role in %s", text)
	}
	if !strings.Contains(text, "UShooterWorldSubsystem:subsystem:UWorldSubsystem") {
		t.Fatalf("expected subsystem role in %s", text)
	}
	foundBlueprint := false
	foundEvent := false
	for _, item := range types {
		if item.Name == "UShooterGameInstance" {
			foundBlueprint = containsString(item.BlueprintCallableFunctions, "BootServices")
			foundEvent = containsString(item.BlueprintEventFunctions, "NotifyReady")
		}
	}
	if !foundBlueprint || !foundEvent {
		t.Fatalf("expected blueprint callable and event functions in %+v", types)
	}
}

func TestExtractUnrealNetworkSurfacesDetectsRPCsAndReplicatedState(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("Source/ShooterGame/ShooterGame.Build.cs", `PublicDependencyModuleNames.AddRange(new string[] { "Core", "Engine", "NetCore" });`)
	mustWrite("Source/ShooterGame/Public/ShooterCharacter.h", `UCLASS() class SHOOTERGAME_API AShooterCharacter : public ACharacter { GENERATED_BODY() public: UFUNCTION(Server, Reliable) void ServerFire(); UFUNCTION(Client, Reliable) void ClientConfirmShot(); UFUNCTION(NetMulticast, Unreliable) void MulticastPlayFX(); UPROPERTY(Replicated) int32 Ammo; UPROPERTY(ReplicatedUsing=OnRep_Health) float Health; UFUNCTION() void OnRep_Health(); };`)
	mustWrite("Source/ShooterGame/Private/ShooterCharacter.cpp", `void AShooterCharacter::GetLifetimeReplicatedProps(TArray<FLifetimeProperty>& OutLifetimeProps) const { Super::GetLifetimeReplicatedProps(OutLifetimeProps); DOREPLIFETIME(AShooterCharacter, Ammo); DOREPLIFETIME(AShooterCharacter, Health); }`)
	snapshot := ProjectSnapshot{
		Root: root,
		Files: []ScannedFile{
			{Path: "Source/ShooterGame/Public/ShooterCharacter.h"},
			{Path: "Source/ShooterGame/Private/ShooterCharacter.cpp"},
		},
		UnrealModules: []UnrealModule{
			{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs"},
		},
		UnrealTypes: []UnrealReflectedType{
			{Name: "AShooterCharacter", Kind: "UCLASS", BaseClass: "ACharacter", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterCharacter.h", GameplayRole: "character"},
		},
	}
	surfaces := extractUnrealNetworkSurfaces(snapshot)
	if len(surfaces) == 0 {
		t.Fatalf("expected unreal network surfaces")
	}
	item := surfaces[0]
	if !containsString(item.ServerRPCs, "ServerFire") || !containsString(item.ClientRPCs, "ClientConfirmShot") || !containsString(item.MulticastRPCs, "MulticastPlayFX") {
		t.Fatalf("expected RPCs in %+v", item)
	}
	if !containsString(item.ReplicatedProperties, "Ammo") || !containsString(item.RepNotifyProperties, "Health") {
		t.Fatalf("expected replicated state in %+v", item)
	}
	if !item.HasReplicationList {
		t.Fatalf("expected replication list registration in %+v", item)
	}
}

func TestBuildProjectEdgesIncludesUnrealNetworkEdges(t *testing.T) {
	snapshot := ProjectSnapshot{
		UnrealNetwork: []UnrealNetworkSurface{
			{
				TypeName:             "AShooterCharacter",
				File:                 "Source/ShooterGame/Public/ShooterCharacter.h",
				ServerRPCs:           []string{"ServerFire"},
				ClientRPCs:           []string{"ClientConfirmShot"},
				MulticastRPCs:        []string{"MulticastPlayFX"},
				ReplicatedProperties: []string{"Ammo"},
				RepNotifyProperties:  []string{"Health"},
				HasReplicationList:   true,
			},
		},
	}
	edges := buildProjectEdges(snapshot)
	joined := []string{}
	for _, edge := range edges {
		joined = append(joined, edge.Type+":"+edge.Source+"->"+edge.Target+":"+edge.Attributes["kind"])
	}
	text := strings.Join(joined, ",")
	for _, expected := range []string{
		"rpc_edge:AShooterCharacter->ServerFire:server_rpc",
		"rpc_edge:AShooterCharacter->ClientConfirmShot:client_rpc",
		"rpc_edge:AShooterCharacter->MulticastPlayFX:multicast_rpc",
		"gameplay_edge:AShooterCharacter->Ammo:replicated_property",
		"gameplay_edge:AShooterCharacter->Health:rep_notify_property",
		"config_edge:AShooterCharacter->GetLifetimeReplicatedProps:replication_registration",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected unreal network edge %q in %s", expected, text)
		}
	}
}

func TestExtractUnrealAssetReferencesDetectsAssetsAndConfig(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("Source/ShooterGame/ShooterGame.Build.cs", `PublicDependencyModuleNames.AddRange(new string[] { "Core", "Engine" });`)
	mustWrite("Source/ShooterGame/Public/ShooterHUD.h", `UCLASS() class SHOOTERGAME_API AShooterHUD : public AHUD { GENERATED_BODY() public: UPROPERTY(EditDefaultsOnly) TSoftObjectPtr<UTexture2D> CrosshairTexture; };`)
	mustWrite("Source/ShooterGame/Private/ShooterHUD.cpp", `static ConstructorHelpers::FObjectFinder<UTexture2D> CrosshairObj(TEXT("/Game/UI/T_Crosshair")); UObject* WidgetClass = LoadObject<UObject>(nullptr, TEXT("/Game/UI/WBP_Scoreboard.WBP_Scoreboard"));`)
	mustWrite("Config/DefaultGame.ini", "[/Script/ShooterGame.ShooterHUD]\nDefaultCrosshair=/Game/UI/T_Crosshair\nHUDClass=/Game/UI/WBP_Scoreboard.WBP_Scoreboard\n")
	snapshot := ProjectSnapshot{
		Root: root,
		Files: []ScannedFile{
			{Path: "Source/ShooterGame/Public/ShooterHUD.h"},
			{Path: "Source/ShooterGame/Private/ShooterHUD.cpp"},
			{Path: "Config/DefaultGame.ini"},
		},
		UnrealModules: []UnrealModule{
			{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs"},
		},
		UnrealTypes: []UnrealReflectedType{
			{Name: "AShooterHUD", Kind: "UCLASS", BaseClass: "AHUD", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterHUD.h", GameplayRole: "hud"},
		},
	}
	items := extractUnrealAssetReferences(snapshot)
	if len(items) == 0 {
		t.Fatalf("expected unreal asset references")
	}
	joined := []string{}
	for _, item := range items {
		joined = append(joined, firstNonBlankAnalysisString(item.OwnerName, item.File)+":"+strings.Join(item.AssetPaths, "|")+":"+strings.Join(item.ConfigKeys, "|")+":"+strings.Join(item.LoadMethods, "|"))
	}
	text := strings.Join(joined, ",")
	for _, expected := range []string{"/Game/UI/T_Crosshair", "/Game/UI/WBP_Scoreboard.WBP_Scoreboard", "DefaultCrosshair=/Game/UI/T_Crosshair", "constructor_helpers", "runtime_object_load"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected asset/config signal %q in %s", expected, text)
		}
	}
}

func TestBuildProjectEdgesIncludesUnrealAssetAndConfigEdges(t *testing.T) {
	snapshot := ProjectSnapshot{
		UnrealAssets: []UnrealAssetReference{
			{
				OwnerName:   "AShooterHUD",
				Module:      "ShooterGame",
				File:        "Source/ShooterGame/Private/ShooterHUD.cpp",
				AssetPaths:  []string{"/Game/UI/T_Crosshair"},
				ConfigKeys:  []string{"DefaultCrosshair=/Game/UI/T_Crosshair"},
				LoadMethods: []string{"constructor_helpers"},
			},
		},
	}
	edges := buildProjectEdges(snapshot)
	joined := []string{}
	for _, edge := range edges {
		joined = append(joined, edge.Type+":"+edge.Source+"->"+edge.Target+":"+edge.Attributes["kind"])
	}
	text := strings.Join(joined, ",")
	for _, expected := range []string{
		"asset_edge:AShooterHUD->/Game/UI/T_Crosshair:asset_reference",
		"config_edge:AShooterHUD->DefaultCrosshair=/Game/UI/T_Crosshair:config_binding",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected unreal asset/config edge %q in %s", expected, text)
		}
	}
}

func TestBuildProjectEdgesIncludesUnrealFrameworkAssignmentEdges(t *testing.T) {
	snapshot := ProjectSnapshot{
		UnrealTypes: []UnrealReflectedType{
			{
				Name:                  "AShooterGameMode",
				Kind:                  "UCLASS",
				BaseClass:             "AGameModeBase",
				File:                  "Source/ShooterGame/Public/ShooterGameMode.h",
				GameplayRole:          "game_mode",
				DefaultPawnClass:      "AShooterCharacter",
				PlayerControllerClass: "AShooterPlayerController",
				HUDClass:              "AShooterHUD",
			},
		},
	}
	edges := buildProjectEdges(snapshot)
	joined := []string{}
	for _, edge := range edges {
		joined = append(joined, edge.Type+":"+edge.Source+"->"+edge.Target+":"+edge.Attributes["kind"]+":"+edge.Attributes["flow"])
	}
	text := strings.Join(joined, ",")
	for _, expected := range []string{
		"gameplay_edge:AShooterGameMode->AShooterCharacter:framework_assignment:default_pawn_assignment",
		"gameplay_edge:AShooterGameMode->AShooterPlayerController:framework_assignment:player_controller_assignment",
		"gameplay_edge:AShooterGameMode->AShooterHUD:framework_assignment:hud_assignment",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected unreal framework assignment edge %q in %s", expected, text)
		}
	}
}

func TestExtractUnrealProjectSettingsDetectsStartupMapAndGameMode(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("Config/DefaultEngine.ini", "[/Script/EngineSettings.GameMapsSettings]\nGameDefaultMap=/Game/Maps/Lobby\nEditorStartupMap=/Game/Maps/TestMap\nGlobalDefaultGameMode=/Script/ShooterGame.ShooterGameMode\nGameInstanceClass=/Script/ShooterGame.ShooterGameInstance\n")
	mustWrite("Config/DefaultGame.ini", "[/Script/ShooterGame.ShooterGameMode]\nDefaultPawnClass=/Script/ShooterGame.ShooterCharacter\nPlayerControllerClass=/Script/ShooterGame.ShooterPlayerController\nHUDClass=/Script/ShooterGame.ShooterHUD\n")
	snapshot := ProjectSnapshot{
		Root: root,
		Files: []ScannedFile{
			{Path: "Config/DefaultEngine.ini"},
			{Path: "Config/DefaultGame.ini"},
		},
	}
	settings := extractUnrealProjectSettings(snapshot)
	if len(settings) != 2 {
		t.Fatalf("expected 2 unreal settings sources, got %+v", settings)
	}
	joined := []string{}
	for _, item := range settings {
		joined = append(joined, item.SourceFile+":"+item.GameDefaultMap+":"+item.GlobalDefaultGameMode+":"+item.GameInstanceClass+":"+item.DefaultPawnClass+":"+item.PlayerControllerClass+":"+item.HUDClass)
	}
	text := strings.Join(joined, ",")
	for _, expected := range []string{"/Game/Maps/Lobby", "/Script/ShooterGame.ShooterGameMode", "/Script/ShooterGame.ShooterGameInstance", "/Script/ShooterGame.ShooterCharacter", "/Script/ShooterGame.ShooterPlayerController", "/Script/ShooterGame.ShooterHUD"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected unreal setting %q in %s", expected, text)
		}
	}
}

func TestBuildUnrealGameplayFlowLinesIncludesSettingsDrivenStartupFlow(t *testing.T) {
	types := []UnrealReflectedType{
		{Name: "AShooterGameMode", GameplayRole: "game_mode", DefaultPawnClass: "AShooterCharacter", PlayerControllerClass: "AShooterPlayerController", HUDClass: "AShooterHUD"},
	}
	settings := []UnrealProjectSetting{
		{
			SourceFile:            "Config/DefaultEngine.ini",
			GameDefaultMap:        "/Game/Maps/Lobby",
			GlobalDefaultGameMode: "/Script/ShooterGame.ShooterGameMode",
			GameInstanceClass:     "/Script/ShooterGame.ShooterGameInstance",
		},
	}
	lines := buildUnrealGameplayFlowLines(types, settings)
	text := strings.Join(lines, ",")
	for _, expected := range []string{"Startup -> Map=/Game/Maps/Lobby", "Startup -> GameInstance=/Script/ShooterGame.ShooterGameInstance", "MapLoad -> GameMode=/Script/ShooterGame.ShooterGameMode", "AShooterGameMode -> DefaultPawn=AShooterCharacter"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected gameplay flow line %q in %s", expected, text)
		}
	}
}

func TestExtractUnrealReflectedTypesDetectsFrameworkAssignments(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("Source/ShooterGame/ShooterGame.Build.cs", `PublicDependencyModuleNames.AddRange(new string[] { "Core", "Engine" });`)
	mustWrite("Source/ShooterGame/Public/ShooterGameMode.h", `UCLASS() class SHOOTERGAME_API AShooterGameMode : public AGameModeBase { GENERATED_BODY() public: AShooterGameMode() { DefaultPawnClass = AShooterCharacter::StaticClass(); PlayerControllerClass = AShooterPlayerController::StaticClass(); HUDClass = AShooterHUD::StaticClass(); } };`)
	snapshot := ProjectSnapshot{
		Root: root,
		Files: []ScannedFile{
			{Path: "Source/ShooterGame/Public/ShooterGameMode.h"},
		},
		UnrealModules: []UnrealModule{
			{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs"},
		},
	}
	types := extractUnrealReflectedTypes(snapshot)
	if len(types) != 1 {
		t.Fatalf("expected one reflected type, got %+v", types)
	}
	item := types[0]
	if item.DefaultPawnClass != "AShooterCharacter" || item.PlayerControllerClass != "AShooterPlayerController" || item.HUDClass != "AShooterHUD" {
		t.Fatalf("expected framework assignments in %+v", item)
	}
}

func TestCanonicalizeBlueprintAssetClass(t *testing.T) {
	for input, expected := range map[string]string{
		"/Game/UI/WBP_Scoreboard.WBP_Scoreboard":           "WBP_Scoreboard",
		"/Game/UI/WBP_Scoreboard.WBP_Scoreboard_C":         "WBP_Scoreboard",
		"/Script/ShooterGame.ShooterHUD":                   "ShooterHUD",
		"TEXT(\"/Game/Characters/BP_Player.BP_Player_C\")": "BP_Player",
	} {
		if got := canonicalizeBlueprintAssetClass(input); got != expected {
			t.Fatalf("canonicalizeBlueprintAssetClass(%q)=%q, want %q", input, got, expected)
		}
	}
}

func TestExtractUnrealGameplaySystemsDetectsInputUmgAndGAS(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("Source/ShooterGame/ShooterGame.Build.cs", `PublicDependencyModuleNames.AddRange(new string[] { "Core", "Engine", "EnhancedInput", "UMG", "GameplayAbilities" });`)
	mustWrite("Source/ShooterGame/Public/ShooterHUD.h", `UCLASS() class AShooterHUD : public AHUD { GENERATED_BODY() };`)
	mustWrite("Source/ShooterGame/Private/ShooterHUD.cpp", `void AShooterHUD::BuildUI() { CreateWidget<UUserWidget>(GetWorld(), ScoreboardClass); } static ConstructorHelpers::FClassFinder<UUserWidget> Scoreboard(TEXT("/Game/UI/WBP_Scoreboard.WBP_Scoreboard_C"));`)
	mustWrite("Source/ShooterGame/Public/ShooterInputComponent.h", `UCLASS() class UShooterInputComponent : public UActorComponent { GENERATED_BODY() };`)
	mustWrite("Source/ShooterGame/Private/ShooterInputComponent.cpp", `void AShooterPlayerController::SetupInput() { UShooterInputComponent* Comp = nullptr; } void UShooterInputComponent::SetupInput(UEnhancedInputComponent* Input) { Input->BindAction(FireAction, ETriggerEvent::Triggered, this, &UShooterInputComponent::HandleFire); } UInputAction* FireAction = nullptr; UInputMappingContext* IMC_Default = nullptr;`)
	mustWrite("Source/ShooterGame/Public/ShooterAbilityComponent.h", `UCLASS() class UShooterAbilityComponent : public UAbilitySystemComponent { GENERATED_BODY() };`)
	mustWrite("Source/ShooterGame/Private/ShooterAbilityComponent.cpp", `UShooterAttributeSet* Attr = nullptr; UShooterDashAbility* Ability = nullptr; UShooterBuffEffect* Effect = nullptr; void AShooterCharacter::GrantAbilities() { UShooterAbilityComponent* Comp = nullptr; } void UShooterAbilityComponent::InitCombat() { InitAbilityActorInfo(nullptr, nullptr); GiveAbility(FGameplayAbilitySpec()); ApplyGameplayEffectToSelf(nullptr, 1.0f, FGameplayEffectContextHandle()); }`)
	snapshot := ProjectSnapshot{
		Root: root,
		Files: []ScannedFile{
			{Path: "Source/ShooterGame/Public/ShooterHUD.h"},
			{Path: "Source/ShooterGame/Private/ShooterHUD.cpp"},
			{Path: "Source/ShooterGame/Public/ShooterInputComponent.h"},
			{Path: "Source/ShooterGame/Private/ShooterInputComponent.cpp"},
			{Path: "Source/ShooterGame/Public/ShooterAbilityComponent.h"},
			{Path: "Source/ShooterGame/Private/ShooterAbilityComponent.cpp"},
		},
		UnrealModules: []UnrealModule{
			{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs"},
		},
		UnrealTypes: []UnrealReflectedType{
			{Name: "AShooterHUD", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterHUD.h", GameplayRole: "hud"},
			{Name: "UShooterInputComponent", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterInputComponent.h"},
			{Name: "UShooterAbilityComponent", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterAbilityComponent.h"},
			{Name: "AShooterPlayerController", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterPlayerController.h", GameplayRole: "player_controller"},
			{Name: "AShooterCharacter", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterCharacter.h", GameplayRole: "character"},
		},
	}
	snapshot.UnrealAssets = extractUnrealAssetReferences(snapshot)
	systems := extractUnrealGameplaySystems(snapshot)
	if len(systems) < 3 {
		t.Fatalf("expected gameplay systems, got %+v", systems)
	}
	text := []string{}
	for _, item := range systems {
		text = append(text, item.System+":"+firstNonBlankAnalysisString(item.OwnerName, item.File)+":"+strings.Join(item.Signals, "|")+":"+strings.Join(item.Assets, "|")+":"+strings.Join(item.Functions, "|")+":"+strings.Join(item.Actions, "|")+":"+strings.Join(item.Contexts, "|")+":"+strings.Join(item.OwnedBy, "|")+":"+strings.Join(item.Abilities, "|")+":"+strings.Join(item.Effects, "|")+":"+strings.Join(item.Attributes, "|"))
	}
	joined := strings.Join(text, ",")
	for _, expected := range []string{
		"enhanced_input:UShooterInputComponent:input_action|mapping_context|bind_action",
		"umg:AShooterHUD:user_widget|create_widget",
		"gameplay_ability_system:UShooterAbilityComponent:ability_system_component|gameplay_ability|gameplay_effect",
		"WBP_Scoreboard",
		"HandleFire",
		"FireAction",
		"IMC_Default",
		"AShooterPlayerController",
		"UShooterDashAbility",
		"UShooterBuffEffect",
		"UShooterAttributeSet",
		"AShooterCharacter",
		"GiveAbility",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected gameplay system signal %q in %s", expected, joined)
		}
	}
}

func TestBuildProjectEdgesIncludesUnrealGameplaySystemEdges(t *testing.T) {
	snapshot := ProjectSnapshot{
		UnrealAssets: []UnrealAssetReference{
			{
				OwnerName:        "AShooterHUD",
				File:             "Source/ShooterGame/Private/ShooterHUD.cpp",
				CanonicalTargets: []string{"WBP_Scoreboard"},
			},
		},
		UnrealSystems: []UnrealGameplaySystem{
			{
				System:    "enhanced_input",
				OwnerName: "UShooterInputComponent",
				File:      "Source/ShooterGame/Private/ShooterInputComponent.cpp",
				Functions: []string{"HandleFire"},
				Actions:   []string{"IA_Fire"},
				Contexts:  []string{"IMC_Default"},
				Assets:    []string{"IA_Fire"},
			},
		},
	}
	edges := buildProjectEdges(snapshot)
	joined := []string{}
	for _, edge := range edges {
		joined = append(joined, edge.Type+":"+edge.Source+"->"+edge.Target+":"+edge.Attributes["kind"])
	}
	text := strings.Join(joined, ",")
	for _, expected := range []string{
		"gameplay_edge:AShooterHUD->WBP_Scoreboard:blueprint_asset_binding",
		"gameplay_edge:UShooterInputComponent->enhanced_input:gameplay_system",
		"gameplay_edge:UShooterInputComponent->HandleFire:system_function",
		"gameplay_edge:UShooterInputComponent->IA_Fire:input_action",
		"gameplay_edge:UShooterInputComponent->IMC_Default:input_context",
		"asset_edge:UShooterInputComponent->IA_Fire:system_asset",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected gameplay system edge %q in %s", expected, text)
		}
	}
}

func TestPlanShardsDoesNotCollapseToConcurrentAgentLimit(t *testing.T) {
	analyzer := &projectAnalyzer{
		analysisCfg: ProjectAnalysisConfig{
			MaxAgents:        2,
			MaxTotalShards:   16,
			MaxFilesPerShard: 10,
			MaxLinesPerShard: 1000,
		},
	}
	snapshot := ProjectSnapshot{
		FilesByDirectory: map[string][]ScannedFile{
			"mod1": {{Path: "mod1/a.cpp", Directory: "mod1", LineCount: 50}},
			"mod2": {{Path: "mod2/a.cpp", Directory: "mod2", LineCount: 50}},
			"mod3": {{Path: "mod3/a.cpp", Directory: "mod3", LineCount: 50}},
			"mod4": {{Path: "mod4/a.cpp", Directory: "mod4", LineCount: 50}},
			"mod5": {{Path: "mod5/a.cpp", Directory: "mod5", LineCount: 50}},
		},
		FilesByPath: map[string]ScannedFile{
			"mod1/a.cpp": {Path: "mod1/a.cpp", Directory: "mod1", LineCount: 50},
			"mod2/a.cpp": {Path: "mod2/a.cpp", Directory: "mod2", LineCount: 50},
			"mod3/a.cpp": {Path: "mod3/a.cpp", Directory: "mod3", LineCount: 50},
			"mod4/a.cpp": {Path: "mod4/a.cpp", Directory: "mod4", LineCount: 50},
			"mod5/a.cpp": {Path: "mod5/a.cpp", Directory: "mod5", LineCount: 50},
		},
		Directories: []string{"mod1", "mod2", "mod3", "mod4", "mod5"},
	}
	shards := analyzer.planShards(snapshot, 5)
	if len(shards) < 5 {
		t.Fatalf("expected shard planning to preserve more shards than concurrent agent limit, got %d", len(shards))
	}
}

func TestPlanRefinementShardsSplitsLargeImportantCandidate(t *testing.T) {
	analyzer := &projectAnalyzer{
		analysisCfg: ProjectAnalysisConfig{
			MaxFilesPerShard:    12,
			MaxLinesPerShard:    2400,
			MaxRefinementShards: 6,
		},
	}
	files := []ScannedFile{
		{Path: "core/main.cpp", Directory: "core", LineCount: 500, ImportanceScore: 18},
		{Path: "core/pipe.cpp", Directory: "core", LineCount: 420, ImportanceScore: 16},
		{Path: "core/worker.cpp", Directory: "core", LineCount: 380, ImportanceScore: 14},
		{Path: "core/ipc.cpp", Directory: "core", LineCount: 360, ImportanceScore: 13},
		{Path: "core/state.cpp", Directory: "core", LineCount: 340, ImportanceScore: 7},
		{Path: "core/util.cpp", Directory: "core", LineCount: 260, ImportanceScore: 5},
	}
	filesByPath := map[string]ScannedFile{}
	for _, file := range files {
		filesByPath[file.Path] = file
	}
	snapshot := ProjectSnapshot{
		AnalysisLenses: []AnalysisLens{{Type: "ipc"}, {Type: "runtime_flow"}},
		FilesByPath:    filesByPath,
	}
	shards := []AnalysisShard{
		{
			ID:             "shard-01",
			Name:           "core_cluster",
			PrimaryFiles:   filesToPaths(files),
			EstimatedFiles: len(files),
			EstimatedLines: sumLines(files),
		},
	}
	reports := []WorkerReport{
		{
			Title:         "core runtime",
			ScopeSummary:  "IPC manager and runtime control path",
			EntryPoints:   []string{"core/main.cpp"},
			InternalFlow:  []string{"pipe dispatch routes commands into worker control"},
			Collaboration: []string{"manager coordinates worker and service state"},
			Unknowns:      []string{"broad shard hides exact dispatch branches"},
			EvidenceFiles: []string{"core/main.cpp", "core/pipe.cpp"},
		},
	}
	reviews := []ReviewDecision{
		{
			Status: "needs_revision",
			Issues: []string{"scope too broad"},
		},
	}
	refined, replaced := analyzer.planRefinementShards(snapshot, shards, reports, reviews)
	if len(refined) < 2 {
		t.Fatalf("expected refinement shards, got %d", len(refined))
	}
	if _, ok := replaced["shard-01"]; !ok {
		t.Fatalf("expected parent shard to be replaced")
	}
	for _, shard := range refined {
		if shard.ParentShardID != "shard-01" {
			t.Fatalf("expected refined shard to reference parent shard-01, got %s", shard.ParentShardID)
		}
		if shard.RefinementStage < 2 {
			t.Fatalf("expected refined shard stage >= 2, got %d", shard.RefinementStage)
		}
	}
}

func TestProjectAnalyzerRunAppliesStageTwoRefinement(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 8; i++ {
		name := filepath.Join(root, "core", fmt.Sprintf("file_%02d.go", i))
		body := "package core\n\n"
		for j := 0; j < 120; j++ {
			body += fmt.Sprintf("func Fn_%02d_%02d() {}\n", i, j)
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatalf("mkdir core: %v", err)
		}
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	cfg := DefaultConfig(root)
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	cfg.ProjectAnalysis.MaxRefinementShards = 4
	cfg.ProjectAnalysis.MaxFilesPerShard = 16
	cfg.ProjectAnalysis.MaxLinesPerShard = 20000
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), "analyze runtime flow and command dispatch", "")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Summary.RefinedShards == 0 {
		t.Fatalf("expected refinement shards to be planned")
	}
	foundRefined := false
	for _, shard := range run.Shards {
		if shard.ParentShardID != "" {
			foundRefined = true
			break
		}
	}
	if !foundRefined {
		t.Fatalf("expected final run shards to include refined child shards")
	}
}

func TestFallbackFinalDocumentIncludesRuntimeGraph(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:           "C:\\repo",
		PrimaryStartup: "Tavern",
		RuntimeEdges: []RuntimeEdge{
			{Source: "Tavern", Target: "TavernMaster", Kind: "project_reference", Confidence: "high", Evidence: []string{"Tavern/Tavern.vcxproj"}},
		},
		TotalFiles: 1,
		TotalLines: 10,
	}
	doc := fallbackFinalDocument(snapshot, nil, nil, "goal")
	if !strings.Contains(doc, "## Runtime Graph") {
		t.Fatalf("expected runtime graph section\n%s", doc)
	}
	if !strings.Contains(doc, "Tavern -> TavernMaster") {
		t.Fatalf("expected runtime graph edge\n%s", doc)
	}
}

func TestBuildKnowledgePackIncludesStartupAndSubsystems(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:            "C:\\repo",
		GeneratedAt:     time.Now(),
		PrimaryStartup:  "Tavern",
		StartupProjects: []string{"Tavern", "TavernWorker"},
		ManifestFiles:   []string{"Tavern.sln"},
		EntrypointFiles: []string{"Tavern/Tavern.cpp"},
		SolutionProjects: []SolutionProject{
			{Name: "Tavern", EntryFiles: []string{"Tavern/Tavern.cpp"}},
		},
	}
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "runtime", PrimaryFiles: []string{"Tavern/Tavern.cpp"}},
	}
	reports := []WorkerReport{
		{
			Title:            "Core Runtime",
			Responsibilities: []string{"boot client", "load orchestrator"},
			KeyFiles:         []string{"Tavern/Tavern.cpp", "TavernCmn/dllmain.cpp"},
			EvidenceFiles:    []string{"Tavern/Tavern.cpp"},
			EntryPoints:      []string{"Tavern/Tavern.cpp::main"},
			Dependencies:     []string{"TavernCmn", "Common"},
			Risks:            []string{"startup chain is security-sensitive"},
		},
	}
	pack := buildKnowledgePack(snapshot, shards, reports, "goal", "run-1")
	if pack.PrimaryStartup != "Tavern" {
		t.Fatalf("expected primary startup in knowledge pack, got %q", pack.PrimaryStartup)
	}
	if len(pack.Subsystems) != 1 {
		t.Fatalf("expected subsystem in knowledge pack, got %#v", pack.Subsystems)
	}
	if !containsString(pack.StartupEntryFiles, "Tavern/Tavern.cpp") {
		t.Fatalf("expected startup entry file in knowledge pack: %#v", pack.StartupEntryFiles)
	}
	if len(pack.PerformanceLens.Hotspots) == 0 {
		t.Fatalf("expected performance lens hotspots in knowledge pack: %#v", pack.PerformanceLens)
	}
}

func TestFallbackFinalDocumentIncludesUnrealModuleMap(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:                "C:\\repo",
		TotalFiles:          4,
		TotalLines:          100,
		UnrealProjects:      []UnrealProject{{Name: "ShooterGame", Path: "ShooterGame.uproject", Modules: []string{"ShooterGame"}, Plugins: []string{"CheatGuard"}}},
		UnrealTargets:       []UnrealTarget{{Name: "ShooterGame", Path: "Source/ShooterGame.Target.cs", TargetType: "Game", Modules: []string{"ShooterGame"}}},
		UnrealModules:       []UnrealModule{{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs", Kind: "game_module", PublicDependencies: []string{"Core", "Engine"}}},
		PrimaryUnrealModule: "ShooterGame",
	}
	doc := fallbackFinalDocument(snapshot, nil, nil, "analyze unreal gameplay")
	for _, expected := range []string{"## Unreal Module And Target Map", "Primary Unreal module", "ShooterGame.uproject", "Source/ShooterGame.Target.cs"} {
		if !strings.Contains(doc, expected) {
			t.Fatalf("expected %q in fallback document\n%s", expected, doc)
		}
	}
}

func TestFallbackFinalDocumentIncludesUnrealGameplaySystems(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:       "C:\\repo",
		TotalFiles: 4,
		TotalLines: 100,
		UnrealSystems: []UnrealGameplaySystem{
			{System: "enhanced_input", OwnerName: "UShooterInputComponent", File: "Source/ShooterGame/Private/ShooterInputComponent.cpp", Signals: []string{"bind_action"}, Functions: []string{"HandleFire"}, Actions: []string{"IA_Fire"}, Contexts: []string{"IMC_Default"}},
		},
	}
	doc := fallbackFinalDocument(snapshot, nil, nil, "analyze unreal gameplay")
	for _, expected := range []string{"## Unreal Gameplay Systems", "## Unreal Gameplay System Flow Map", "enhanced_input", "UShooterInputComponent", "HandleFire", "IA_Fire", "IMC_Default"} {
		if !strings.Contains(doc, expected) {
			t.Fatalf("expected %q in fallback document\n%s", expected, doc)
		}
	}
}

func TestBuildKnowledgePackIncludesUnrealMetadata(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:                "C:\\repo",
		GeneratedAt:         time.Now(),
		PrimaryUnrealModule: "ShooterGame",
		UnrealProjects:      []UnrealProject{{Name: "ShooterGame", Path: "ShooterGame.uproject"}},
		UnrealPlugins:       []UnrealPlugin{{Name: "CheatGuard", Path: "Plugins/CheatGuard/CheatGuard.uplugin"}},
		UnrealTargets:       []UnrealTarget{{Name: "ShooterGame", Path: "Source/ShooterGame.Target.cs", TargetType: "Game"}},
		UnrealModules:       []UnrealModule{{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs"}},
		UnrealSystems:       []UnrealGameplaySystem{{System: "umg", OwnerName: "AShooterHUD", File: "Source/ShooterGame/Private/ShooterHUD.cpp"}},
	}
	pack := buildKnowledgePack(snapshot, nil, nil, "goal", "run-1")
	if pack.PrimaryUnrealModule != "ShooterGame" {
		t.Fatalf("expected primary unreal module in knowledge pack, got %q", pack.PrimaryUnrealModule)
	}
	if len(pack.UnrealProjects) != 1 || len(pack.UnrealModules) != 1 || len(pack.UnrealTargets) != 1 || len(pack.UnrealSystems) != 1 {
		t.Fatalf("expected unreal metadata in knowledge pack, got %+v", pack)
	}
}

func TestBuildVectorCorpusIncludesProjectSubsystemAndShardDocs(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID: "run-1",
			Goal:  "analyze unreal network security",
		},
		Snapshot: ProjectSnapshot{
			Root:           "C:\\repo",
			GeneratedAt:    time.Now(),
			AnalysisLenses: []AnalysisLens{{Type: "security_boundary"}},
		},
		KnowledgePack: KnowledgePack{
			RunID:          "run-1",
			Goal:           "analyze unreal network security",
			Root:           "C:\\repo",
			ProjectSummary: "Executive focus: recent changes are concentrated on authority, replication, or security-sensitive boundaries.",
			AnalysisExecution: AnalysisExecutionSummary{
				TotalShards:      2,
				TopChangeClasses: []string{"replicated_property_added (2)"},
			},
			Subsystems: []KnowledgeSubsystem{
				{
					Title:                "network",
					Group:                "Security Control",
					Responsibilities:     []string{"handle RPC"},
					InvalidationReasons:  []string{"semantic_dependency_changed"},
					InvalidationEvidence: []string{"AShooterCharacter server=ServerFire client= multicast= replicated=Ammo"},
					EvidenceFiles:        []string{"net.cpp"},
				},
			},
		},
		Shards: []AnalysisShard{
			{ID: "shard-01", Name: "unreal_network"},
		},
		ShardDocuments: map[string]string{
			"shard-01": "# unreal_network\n\nShard text",
		},
	}
	corpus := buildVectorCorpus(run)
	if len(corpus.Documents) < 3 {
		t.Fatalf("expected vector corpus documents, got %+v", corpus.Documents)
	}
	joined := []string{}
	for _, doc := range corpus.Documents {
		joined = append(joined, doc.Kind+":"+doc.Title)
	}
	text := strings.Join(joined, ",")
	for _, expected := range []string{"project_summary:Project Summary", "analysis_execution:Analysis Execution Summary", "subsystem:Security Control: network", "shard:unreal_network"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected vector corpus entry %q in %s", expected, text)
		}
	}
}

func TestBuildVectorIngestionManifestIncludesTargetsAndKinds(t *testing.T) {
	corpus := VectorCorpus{
		RunID:       "run-1",
		Goal:        "analyze unreal network security",
		Root:        "C:\\repo",
		GeneratedAt: time.Now(),
		Documents: []VectorCorpusDocument{
			{ID: "project_summary", Kind: "project_summary", Title: "Project Summary", Text: "summary"},
			{ID: "shard:unreal_network", Kind: "shard", Title: "unreal_network", Text: "shard body"},
		},
	}
	manifest := buildVectorIngestionManifest(corpus)
	if manifest.DocumentCount != 2 {
		t.Fatalf("expected 2 documents, got %+v", manifest)
	}
	if len(manifest.DocumentKinds) != 2 || manifest.DocumentKinds[0] != "project_summary" || manifest.DocumentKinds[1] != "shard" {
		t.Fatalf("expected sorted document kinds, got %+v", manifest.DocumentKinds)
	}
	targets := []string{}
	for _, target := range manifest.Targets {
		targets = append(targets, target.Name+":"+target.Filename)
	}
	joined := strings.Join(targets, ",")
	for _, expected := range []string{
		"records:vector_ingest_records.jsonl",
		"pgvector:vector_pgvector.sql",
		"sqlite:vector_sqlite.sql",
		"qdrant:vector_qdrant.jsonl",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected ingestion target %q in %s", expected, joined)
		}
	}
}

func TestPersistRunWritesKnowledgeArtifacts(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID: "run-1",
			Goal:  "goal",
		},
		FinalDocument: "# doc\n",
		KnowledgePack: KnowledgePack{
			RunID:          "run-1",
			Goal:           "goal",
			Root:           root,
			PrimaryStartup: "Tavern",
			Subsystems: []KnowledgeSubsystem{
				{Title: "Core Runtime", Group: "Core Application"},
			},
		},
		Snapshot: ProjectSnapshot{
			Root:           root,
			GeneratedAt:    time.Now(),
			PrimaryStartup: "Tavern",
			Files: []ScannedFile{
				{Path: "Tavern/Tavern.cpp", Directory: "Tavern", Extension: ".cpp", LineCount: 40, IsEntrypoint: true, ImportanceScore: 12},
			},
		},
		SemanticIndex: SemanticIndex{
			RunID:       "run-1",
			Goal:        "goal",
			Root:        root,
			GeneratedAt: time.Now(),
			Files: []SemanticIndexedFile{
				{Path: "Tavern/Tavern.cpp", IsEntrypoint: true},
			},
			Symbols: []SemanticSymbol{
				{ID: "module:Tavern", Name: "Tavern", Kind: "unreal_module"},
			},
		},
		UnrealGraph: UnrealSemanticGraph{
			RunID:       "run-1",
			Goal:        "goal",
			Root:        root,
			GeneratedAt: time.Now(),
			Nodes: []UnrealSemanticNode{
				{ID: "module:Tavern", Kind: "module", Name: "Tavern"},
			},
		},
	}
	run.SemanticIndexV2 = buildSemanticIndexV2(run.Snapshot, run.Summary.Goal, run.Summary.RunID, run.UnrealGraph)
	run.VectorCorpus = buildVectorCorpus(run)
	run.VectorIngestion = buildVectorIngestionManifest(run.VectorCorpus)
	if _, err := analyzer.persistRun(run); err != nil {
		t.Fatalf("persistRun returned error: %v", err)
	}
	base := filepath.Join(cfg.ProjectAnalysis.OutputDir, "run-1_goal")
	if _, err := os.Stat(base + "_knowledge.json"); err != nil {
		t.Fatalf("expected knowledge json artifact: %v", err)
	}
	if _, err := os.Stat(base + "_knowledge.md"); err != nil {
		t.Fatalf("expected knowledge digest artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "knowledge_pack.json")); err != nil {
		t.Fatalf("expected latest knowledge pack artifact: %v", err)
	}
	if _, err := os.Stat(base + "_performance_lens.json"); err != nil {
		t.Fatalf("expected performance lens json artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "performance_lens.json")); err != nil {
		t.Fatalf("expected latest performance lens artifact: %v", err)
	}
	if _, err := os.Stat(base + "_snapshot.json"); err != nil {
		t.Fatalf("expected snapshot json artifact: %v", err)
	}
	if _, err := os.Stat(base + "_structural_index.json"); err != nil {
		t.Fatalf("expected structural index json artifact: %v", err)
	}
	if _, err := os.Stat(base + "_structural_index_v2.json"); err != nil {
		t.Fatalf("expected structural index v2 json artifact: %v", err)
	}
	if _, err := os.Stat(base + "_unreal_graph.json"); err != nil {
		t.Fatalf("expected unreal graph json artifact: %v", err)
	}
	if _, err := os.Stat(base + "_vector_corpus.json"); err != nil {
		t.Fatalf("expected vector corpus json artifact: %v", err)
	}
	if _, err := os.Stat(base + "_vector_corpus.jsonl"); err != nil {
		t.Fatalf("expected vector corpus jsonl artifact: %v", err)
	}
	if _, err := os.Stat(base + "_vector_ingest_manifest.json"); err != nil {
		t.Fatalf("expected vector ingest manifest artifact: %v", err)
	}
	if _, err := os.Stat(base + "_vector_ingest_records.jsonl"); err != nil {
		t.Fatalf("expected vector ingest records artifact: %v", err)
	}
	if _, err := os.Stat(base + "_vector_pgvector.sql"); err != nil {
		t.Fatalf("expected vector pgvector sql artifact: %v", err)
	}
	if _, err := os.Stat(base + "_vector_sqlite.sql"); err != nil {
		t.Fatalf("expected vector sqlite sql artifact: %v", err)
	}
	if _, err := os.Stat(base + "_vector_qdrant.jsonl"); err != nil {
		t.Fatalf("expected vector qdrant seed artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "snapshot.json")); err != nil {
		t.Fatalf("expected latest snapshot artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "structural_index.json")); err != nil {
		t.Fatalf("expected latest structural index artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "structural_index_v2.json")); err != nil {
		t.Fatalf("expected latest structural index v2 artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "unreal_graph.json")); err != nil {
		t.Fatalf("expected latest unreal graph artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "vector_corpus.json")); err != nil {
		t.Fatalf("expected latest vector corpus artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "vector_corpus.jsonl")); err != nil {
		t.Fatalf("expected latest vector corpus jsonl artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "vector_ingest_manifest.json")); err != nil {
		t.Fatalf("expected latest vector ingest manifest artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "vector_ingest_records.jsonl")); err != nil {
		t.Fatalf("expected latest vector ingest records artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "vector_pgvector.sql")); err != nil {
		t.Fatalf("expected latest vector pgvector sql artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "vector_sqlite.sql")); err != nil {
		t.Fatalf("expected latest vector sqlite sql artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest", "vector_qdrant.jsonl")); err != nil {
		t.Fatalf("expected latest vector qdrant artifact: %v", err)
	}
}

func TestBuildSemanticIndexIncludesFilesSymbolsAndBuildEdges(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:           "C:\\repo",
		GeneratedAt:    time.Now(),
		PrimaryStartup: "ShooterGame",
		Files: []ScannedFile{
			{
				Path:            "Source/ShooterGame/ShooterGame.Build.cs",
				Directory:       "Source/ShooterGame",
				Extension:       ".cs",
				LineCount:       25,
				IsManifest:      true,
				ImportanceScore: 18,
				ImportanceReasons: []string{
					"unreal_module_lens_priority",
				},
				Imports: []string{"Engine", "CoreUObject"},
			},
		},
		UnrealModules: []UnrealModule{
			{
				Name:                "ShooterGame",
				Path:                "Source/ShooterGame/ShooterGame.Build.cs",
				Kind:                "game_module",
				PublicDependencies:  []string{"Core", "Engine"},
				PrivateDependencies: []string{"Slate"},
			},
		},
		UnrealTypes: []UnrealReflectedType{
			{
				Name:         "AShooterGameMode",
				Kind:         "UCLASS",
				BaseClass:    "AGameModeBase",
				Module:       "ShooterGame",
				File:         "Source/ShooterGame/Public/ShooterGameMode.h",
				GameplayRole: "game_mode",
			},
		},
	}
	graph := buildUnrealSemanticGraph(snapshot, "goal", "run-1")
	index := buildSemanticIndex(snapshot, "goal", "run-1", graph)
	if len(index.Files) != 1 {
		t.Fatalf("expected one indexed file, got %+v", index.Files)
	}
	if len(index.Symbols) < 2 {
		t.Fatalf("expected module and type symbols, got %+v", index.Symbols)
	}
	if len(index.References) == 0 {
		t.Fatalf("expected file import references")
	}
	if len(index.BuildEdges) == 0 {
		t.Fatalf("expected build edges")
	}
	if index.UnrealGraph.RunID != "run-1" || len(index.UnrealGraph.Nodes) == 0 {
		t.Fatalf("expected embedded unreal graph, got %+v", index.UnrealGraph)
	}
}

func TestBuildSemanticIndexV2IncludesOccurrencesAndOverlayEdges(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:           "C:\\repo",
		GeneratedAt:    time.Now(),
		PrimaryStartup: "ShooterGame",
		Files: []ScannedFile{
			{
				Path:            "Source/ShooterGame/Public/ShooterGameMode.h",
				Directory:       "Source/ShooterGame/Public",
				Extension:       ".h",
				LineCount:       72,
				ImportanceScore: 22,
				ImportanceReasons: []string{
					"startup_symbol",
					"unreal_network_lens_priority",
				},
			},
			{
				Path:            "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IoctlDispatch.cpp",
				Directory:       "Plugins/CheatGuard/Source/CheatGuardRuntime/Private",
				Extension:       ".cpp",
				LineCount:       110,
				ImportanceScore: 19,
			},
			{
				Path:            "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/MemoryScanner.cpp",
				Directory:       "Plugins/CheatGuard/Source/CheatGuardRuntime/Private",
				Extension:       ".cpp",
				LineCount:       144,
				ImportanceScore: 20,
			},
			{
				Path:            "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/HandlePolicy.cpp",
				Directory:       "Plugins/CheatGuard/Source/CheatGuardRuntime/Private",
				Extension:       ".cpp",
				LineCount:       96,
				ImportanceScore: 18,
			},
			{
				Path:            "Plugins/CheatGuard/Source/CheatGuardRuntime/Private/RpcDispatchPipe.cpp",
				Directory:       "Plugins/CheatGuard/Source/CheatGuardRuntime/Private",
				Extension:       ".cpp",
				LineCount:       102,
				ImportanceScore: 18,
			},
		},
		SolutionProjects: []SolutionProject{
			{
				Name:       "ShooterGame",
				Path:       "ShooterGame.vcxproj",
				Kind:       "vcxproj",
				OutputType: "application",
			},
		},
		UnrealModules: []UnrealModule{
			{
				Name:                "ShooterGame",
				Path:                "Source/ShooterGame/ShooterGame.Build.cs",
				Kind:                "game_module",
				PublicDependencies:  []string{"Core", "Engine"},
				PrivateDependencies: []string{"Slate"},
			},
		},
		UnrealTypes: []UnrealReflectedType{
			{
				Name:         "AShooterGameMode",
				Kind:         "UCLASS",
				BaseClass:    "AGameModeBase",
				Module:       "ShooterGame",
				File:         "Source/ShooterGame/Public/ShooterGameMode.h",
				GameplayRole: "game_mode",
			},
		},
		UnrealNetwork: []UnrealNetworkSurface{
			{
				TypeName:             "AShooterGameMode",
				File:                 "Source/ShooterGame/Public/ShooterGameMode.h",
				ServerRPCs:           []string{"ServerStartMatch"},
				ReplicatedProperties: []string{"MatchState"},
			},
		},
		UnrealAssets: []UnrealAssetReference{
			{
				OwnerName:        "AShooterGameMode",
				File:             "Source/ShooterGame/Public/ShooterGameMode.h",
				CanonicalTargets: []string{"WBP_Lobby"},
				ConfigKeys:       []string{"GameDefaultMap"},
			},
		},
		RuntimeEdges: []RuntimeEdge{
			{
				Source:     "ShooterGame",
				Target:     "ShooterGame",
				Kind:       "dynamic_load",
				Confidence: "high",
				Evidence:   []string{"ShooterGame.vcxproj"},
			},
		},
		ProjectEdges: []ProjectEdge{
			{
				Source:     "ShooterGame",
				Target:     "AShooterGameMode",
				Type:       "security_edge",
				Confidence: "high",
				Evidence:   []string{"Source/ShooterGame/Public/ShooterGameMode.h"},
			},
			{
				Source:     "CheatGuardRuntime",
				Target:     "IoctlDispatch",
				Type:       "device_control_dispatch",
				Confidence: "high",
				Evidence:   []string{"Plugins/CheatGuard/Source/CheatGuardRuntime/Private/IoctlDispatch.cpp"},
			},
			{
				Source:     "CheatGuardRuntime",
				Target:     "MemoryScanner",
				Type:       "remote_memory_scan",
				Confidence: "high",
				Evidence:   []string{"Plugins/CheatGuard/Source/CheatGuardRuntime/Private/MemoryScanner.cpp"},
			},
			{
				Source:     "CheatGuardRuntime",
				Target:     "HandlePolicy",
				Type:       "process_handle_open",
				Confidence: "high",
				Evidence:   []string{"Plugins/CheatGuard/Source/CheatGuardRuntime/Private/HandlePolicy.cpp"},
			},
			{
				Source:     "CheatGuardRuntime",
				Target:     "RpcDispatchPipe",
				Type:       "named_pipe_dispatch",
				Confidence: "high",
				Evidence:   []string{"Plugins/CheatGuard/Source/CheatGuardRuntime/Private/RpcDispatchPipe.cpp"},
			},
		},
	}
	graph := buildUnrealSemanticGraph(snapshot, "goal", "run-1")
	index := buildSemanticIndexV2(snapshot, "goal", "run-1", graph)
	if len(index.Files) != 5 {
		t.Fatalf("expected five v2 file records, got %+v", index.Files)
	}
	if len(index.Symbols) < 5 {
		t.Fatalf("expected richer v2 symbols, got %+v", index.Symbols)
	}
	if len(index.Occurrences) == 0 {
		t.Fatalf("expected symbol occurrences")
	}
	if len(index.CallEdges) == 0 {
		t.Fatalf("expected call edges")
	}
	if len(index.InheritanceEdges) == 0 {
		t.Fatalf("expected inheritance edges")
	}
	if len(index.BuildOwnershipEdges) == 0 {
		t.Fatalf("expected build ownership edges")
	}
	if len(index.GeneratedCodeEdges) == 0 {
		t.Fatalf("expected generated code edges")
	}
	if len(index.OverlayEdges) == 0 {
		t.Fatalf("expected overlay edges")
	}
	overlayText := []string{}
	for _, edge := range index.OverlayEdges {
		overlayText = append(overlayText, edge.Domain+"|"+edge.Type)
	}
	joined := strings.Join(overlayText, ",")
	for _, expected := range []string{"ioctl_surface|issues_ioctl", "memory_surface|accesses_remote_memory", "handle_surface|opens_handle", "rpc_surface|dispatches_rpc"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected v2 overlay %q in %s", expected, joined)
		}
	}
	if len(index.QueryModes) != 5 {
		t.Fatalf("expected default query modes, got %+v", index.QueryModes)
	}
}

func TestBuildUnrealSemanticGraphIncludesCoreEdges(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:        "C:\\repo",
		GeneratedAt: time.Now(),
		UnrealProjects: []UnrealProject{
			{Name: "ShooterGame", Path: "ShooterGame.uproject", Modules: []string{"ShooterGame"}, Plugins: []string{"CheatGuard"}},
		},
		UnrealPlugins: []UnrealPlugin{
			{Name: "CheatGuard", Path: "Plugins/CheatGuard/CheatGuard.uplugin", Modules: []string{"CheatGuardRuntime"}, EnabledByDefault: true},
		},
		UnrealTargets: []UnrealTarget{
			{Name: "ShooterGame", Path: "Source/ShooterGame.Target.cs", TargetType: "Game", Modules: []string{"ShooterGame"}},
		},
		UnrealModules: []UnrealModule{
			{Name: "ShooterGame", Path: "Source/ShooterGame/ShooterGame.Build.cs", PublicDependencies: []string{"Engine"}},
		},
		UnrealTypes: []UnrealReflectedType{
			{Name: "AShooterGameMode", Kind: "UCLASS", Module: "ShooterGame", File: "Source/ShooterGame/Public/ShooterGameMode.h", BaseClass: "AGameModeBase", DefaultPawnClass: "AShooterCharacter"},
		},
		UnrealNetwork: []UnrealNetworkSurface{
			{TypeName: "AShooterGameMode", ServerRPCs: []string{"ServerStartMatch"}, ReplicatedProperties: []string{"MatchState"}},
		},
		UnrealAssets: []UnrealAssetReference{
			{OwnerName: "AShooterGameMode", CanonicalTargets: []string{"WBP_Lobby"}, ConfigKeys: []string{"GameDefaultMap"}},
		},
	}
	graph := buildUnrealSemanticGraph(snapshot, "goal", "run-1")
	if len(graph.Nodes) == 0 || len(graph.Edges) == 0 {
		t.Fatalf("expected graph nodes and edges, got %+v", graph)
	}
	text := []string{}
	for _, edge := range graph.Edges {
		text = append(text, edge.Source+"->"+edge.Type+"->"+edge.Target)
	}
	joined := strings.Join(text, ",")
	for _, expected := range []string{
		"uproject:ShooterGame->declares->module:ShooterGame",
		"uproject:ShooterGame->loads->plugin:CheatGuard",
		"module:ShooterGame->declares->type:AShooterGameMode",
		"type:AShooterGameMode->inherits_from->type:AGameModeBase",
		"type:AShooterGameMode->spawns->type:AShooterCharacter",
		"type:AShooterGameMode->rpc_server->rpc:ServerStartMatch",
		"type:AShooterGameMode->replicates->property:MatchState",
		"type:AShooterGameMode->references_asset->asset:WBP_Lobby",
		"type:AShooterGameMode->configured_by->config:GameDefaultMap",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected graph edge %q in %s", expected, joined)
		}
	}
}

func TestBuildPerformanceLensClassifiesHotspots(t *testing.T) {
	snapshot := ProjectSnapshot{
		PrimaryStartup: "Tavern",
		Files: []ScannedFile{
			{Path: "Common/ETWConsumer.cpp", LineCount: 1032},
			{Path: "TavernWorker/PrefetchScanner.cpp", LineCount: 920},
		},
		SolutionProjects: []SolutionProject{
			{Name: "Tavern", EntryFiles: []string{"Tavern/Tavern.cpp"}},
		},
	}
	items := []synthesisSection{
		{
			Title:            "Monitoring",
			Group:            "Security Control",
			Responsibilities: []string{"monitor file system and ETW events"},
			EntryPoints:      []string{"ETWConsumer::Initialize"},
			KeyFiles:         []string{"Common/ETWConsumer.cpp"},
			InternalFlow:     []string{"initialize ETW -> receive events"},
		},
		{
			Title:            "Worker",
			Group:            "Forensic Analysis",
			Responsibilities: []string{"scan prefetch and decode artifacts"},
			EntryPoints:      []string{"ScannerBase::Scan"},
			KeyFiles:         []string{"TavernWorker/PrefetchScanner.cpp"},
			InternalFlow:     []string{"scan -> decompress -> hash"},
		},
	}
	lens := buildPerformanceLens(snapshot, items)
	if len(lens.Hotspots) < 2 {
		t.Fatalf("expected hotspots, got %#v", lens)
	}
	if !containsString(lens.IOBoundCandidates, "Security Control: Monitoring") {
		t.Fatalf("expected io-bound monitoring candidate: %#v", lens.IOBoundCandidates)
	}
	if !containsString(lens.CPUBoundCandidates, "Forensic Analysis: Worker") {
		t.Fatalf("expected cpu-bound worker candidate: %#v", lens.CPUBoundCandidates)
	}
	if len(lens.Hotspots) == 0 || lens.Hotspots[0].Score < lens.Hotspots[len(lens.Hotspots)-1].Score {
		t.Fatalf("expected hotspots sorted by descending score: %#v", lens.Hotspots)
	}
	if lens.Hotspots[0].Score == 0 {
		t.Fatalf("expected non-zero hotspot score: %#v", lens.Hotspots[0])
	}
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
