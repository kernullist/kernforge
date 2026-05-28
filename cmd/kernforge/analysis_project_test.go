package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

type stubAnalysisClient struct {
	workerCalls    int
	reviewerCalls  int
	synthesisCalls int
}

func TestAnalysisRequestWithCodexSubagentLabelsWorkerAndReviewer(t *testing.T) {
	workerReq := analysisRequestWithCodexSubagent("worker", ChatRequest{})
	if workerReq.CodexSubagent != openAICodexSubagentCollabSpawn {
		t.Fatalf("worker CodexSubagent = %q, want %q", workerReq.CodexSubagent, openAICodexSubagentCollabSpawn)
	}
	repairReq := analysisRequestWithCodexSubagent("worker-repair", ChatRequest{})
	if repairReq.CodexSubagent != openAICodexSubagentCollabSpawn {
		t.Fatalf("worker-repair CodexSubagent = %q, want %q", repairReq.CodexSubagent, openAICodexSubagentCollabSpawn)
	}
	reviewerReq := analysisRequestWithCodexSubagent("reviewer", ChatRequest{})
	if reviewerReq.CodexSubagent != openAICodexSubagentReview {
		t.Fatalf("reviewer CodexSubagent = %q, want %q", reviewerReq.CodexSubagent, openAICodexSubagentReview)
	}
	presetReq := analysisRequestWithCodexSubagent("worker", ChatRequest{CodexSubagent: "custom"})
	if presetReq.CodexSubagent != "custom" {
		t.Fatalf("explicit CodexSubagent should be preserved, got %q", presetReq.CodexSubagent)
	}
	mainReq := analysisRequestWithCodexSubagent("synthesis", ChatRequest{})
	if mainReq.CodexSubagent != "" {
		t.Fatalf("main analysis stages should not be marked as subagents, got %q", mainReq.CodexSubagent)
	}
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

func TestBuildWorkerRevisionPromptFromReviewPreservesStructuredFeedback(t *testing.T) {
	review := ReviewDecision{
		Status: "needs_revision",
		Issues: []string{
			"missing depth",
		},
		SymptomCausality: []string{
			"show how the invalid state reaches the user-visible symptom",
		},
		RequiredRuntimeObservation: []string{
			"trace retry_count before the failed transition",
		},
		DisqualifyingEvidence: []string{
			"candidate is invalid if retry_count is never persisted",
		},
		EvidenceRequests: []RootCauseEvidenceRequest{
			{
				Request:         "Inspect the persistence shard for retry_count writes.",
				TargetSignals:   []string{"retry_count write path"},
				TargetFiles:     []string{"state/store.go"},
				Reason:          "The worker report needs cross-shard evidence.",
				RequiredToProve: "retry_count can survive restart",
			},
		},
	}

	prompt := buildWorkerRevisionPromptFromReview(review)
	required := []string{
		"Reviewer issues:",
		"missing depth",
		"Symptom causality:",
		"user-visible symptom",
		"Required runtime observation:",
		"trace retry_count",
		"Disqualifying evidence:",
		"candidate is invalid",
		"Evidence requests:",
		"Inspect the persistence shard",
		"retry_count write path",
		"state/store.go",
	}
	for _, item := range required {
		if !strings.Contains(prompt, item) {
			t.Fatalf("expected revision prompt to contain %q:\n%s", item, prompt)
		}
	}
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

type singleRouteAnalysisClient struct {
	mu                 sync.Mutex
	active             int
	maxActive          int
	concurrentFailures int
}

func (c *singleRouteAnalysisClient) Name() string {
	return "single-route"
}

func (c *singleRouteAnalysisClient) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	c.mu.Lock()
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	overlapped := c.active > 1
	if overlapped {
		c.concurrentFailures++
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return ChatResponse{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	if overlapped {
		return ChatResponse{}, fmt.Errorf("single provider/model route received concurrent analysis request")
	}
	if strings.Contains(req.System, "project analysis sub-agent") {
		return ChatResponse{
			Message: Message{
				Text: `{"report":{"title":"core","scope_summary":"summary","responsibilities":["boot"],"facts":["file participates in runtime"],"inferences":["runtime is analyzable"],"entry_points":["main.go"],"internal_flow":["main starts"],"dependencies":[],"collaboration":[],"risks":[],"unknowns":[],"evidence_files":["main.go"],"narrative":"ok"}}`,
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
	if run.Summary.AgentCount < 1 || run.Summary.AgentCount > 16 {
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

func TestProjectAnalyzerSkipsImplicitReviewerInSingleModelMode(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	cfg := DefaultConfig(root)
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-chat"
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &stubAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}

	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), "map the project", "map")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if client.workerCalls == 0 {
		t.Fatalf("expected worker calls")
	}
	if client.reviewerCalls != 0 {
		t.Fatalf("expected implicit reviewer to be skipped, got %d calls", client.reviewerCalls)
	}
	if len(run.Reviews) == 0 || run.Reviews[0].ClaimCoverageStatus != "review_skipped_single_model" {
		t.Fatalf("expected skipped review decision, got %#v", run.Reviews)
	}
	if !strings.Contains(run.ReviewerProfile, "skipped in single-model mode") {
		t.Fatalf("expected reviewer profile to disclose skip, got %q", run.ReviewerProfile)
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
		Directories: []string{"SampleWorker", "Common", "docs/internal"},
		FilesByDirectory: map[string][]ScannedFile{
			"SampleWorker": {{Path: "SampleWorker/worker.cpp", Directory: "SampleWorker"}},
			"Common":       {{Path: "Common/shared.cpp", Directory: "Common"}},
		},
	}
	scope := deriveAnalysisGoalScope("SampleWorker 디렉토리 안의 코드를 분석하고 주요 탐지, 방어 기능들을 문서화해줘.", snapshot)
	if len(scope.DirectoryPrefixes) != 1 || scope.DirectoryPrefixes[0] != "SampleWorker" {
		t.Fatalf("expected SampleWorker scope, got %#v", scope)
	}
}

func TestProjectAnalyzerRunScopesShardsToRequestedDirectory(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "SampleWorker"),
		filepath.Join(root, "Common"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "SampleWorker", "worker.cpp"), []byte("int Worker()\n{\n    return 1;\n}\n"), 0o644); err != nil {
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
	run, err := analyzer.Run(context.Background(), "SampleWorker 디렉토리 안의 코드를 분석하고 주요 탐지, 방어 기능들을 문서화해줘.", "")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(run.Shards) == 0 {
		t.Fatalf("expected scoped shards")
	}
	for _, shard := range run.Shards {
		if !hasPathPrefix(shard.PrimaryFiles, "SampleWorker/") {
			t.Fatalf("expected only SampleWorker shards, got %#v", run.Shards)
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
	cfg.ProjectAnalysis.MaxProviderRetries = -1
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
	if run.Summary.ReviewProviderFailures == 0 {
		t.Fatalf("expected reviewer provider failures to be tracked separately")
	}
	if run.Summary.ReviewQualityIssues != 0 {
		t.Fatalf("expected no review quality issues, got %d", run.Summary.ReviewQualityIssues)
	}
	if strings.TrimSpace(run.FinalDocument) == "" {
		t.Fatalf("expected final document even when reviewer fails")
	}
	if !strings.Contains(run.FinalDocument, "Draft Analysis") && !strings.Contains(run.FinalDocument, "Analysis With Provider Failures") {
		t.Fatalf("expected degraded review banner in final document\n%s", run.FinalDocument)
	}
	if !strings.Contains(run.FinalDocument, "Provider failures:") {
		t.Fatalf("expected provider failure details in final document\n%s", run.FinalDocument)
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

func TestExecuteShardSoftFailsWorkerProviderErrorWithShardAndModel(t *testing.T) {
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
	report, review, _, err := analyzer.executeShard(context.Background(), snapshot, shards[0], "analyze", nil, reuseState)
	if err != nil {
		t.Fatalf("expected worker provider failure to soft-fail, got %v", err)
	}
	if review.Status != "review_failed" {
		t.Fatalf("expected review_failed, got %#v", review)
	}
	text := strings.Join(append(review.Issues, report.Raw), "\n")
	if !strings.Contains(text, "analysis worker request failed") || !strings.Contains(text, "shard=") || !strings.Contains(text, "model=analysis-model") {
		t.Fatalf("expected wrapped worker error to be preserved, got %q", text)
	}
	if !strings.Contains(report.ScopeSummary, "low-confidence placeholder") {
		t.Fatalf("expected low-confidence placeholder report, got %#v", report)
	}
}

func TestExecuteShardsReportsUserVisibleProgress(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "feature.go"), []byte("package pkg\n\nfunc Feature() {}\n"), 0o644); err != nil {
		t.Fatalf("write feature.go: %v", err)
	}

	cfg := DefaultConfig(root)
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	var mu sync.Mutex
	statuses := []string{}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, func(status string) {
		mu.Lock()
		defer mu.Unlock()
		statuses = append(statuses, status)
	}, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	shards := []AnalysisShard{
		{
			ID:           "shard-01",
			Name:         "runtime",
			PrimaryFiles: []string{"main.go"},
		},
		{
			ID:           "shard-02",
			Name:         "pkg_feature",
			PrimaryFiles: []string{"pkg/feature.go"},
		},
	}

	_, _, err = analyzer.executeShards(context.Background(), snapshot, shards, "analyze", nil, analysisReuseState{}, 1)
	if err != nil {
		t.Fatalf("executeShards returned error: %v", err)
	}

	mu.Lock()
	joined := strings.Join(statuses, "\n")
	mu.Unlock()
	for _, expected := range []string{
		"Shard wave 1/2 started",
		"Shard 1/2 completed: runtime",
		"Shard wave 1/2 completed",
		"Shard 2/2 completed: pkg_feature",
		"Shard wave 2/2 completed",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected progress status to contain %q\n%s", expected, joined)
		}
	}
}

func TestFormatProgressEventMessageIncludesAnalysisStageAndShard(t *testing.T) {
	message := formatProgressEventMessage(Config{}, ProgressEvent{
		Kind:     progressKindModelRequestWait,
		Provider: "deepseek",
		Model:    "deepseek-chat",
		Stage:    "worker",
		Shard:    "runtime",
		Elapsed:  5 * time.Second,
	})
	if !strings.HasPrefix(message, "worker runtime:") {
		t.Fatalf("expected stage and shard prefix, got %q", message)
	}
	if !strings.Contains(message, "DeepSeek / deepseek-chat") {
		t.Fatalf("expected provider/model target, got %q", message)
	}
}

func TestFormatProgressEventMessageUsesProviderDisplayLabel(t *testing.T) {
	message := formatProgressEventMessage(Config{}, ProgressEvent{
		Kind:     progressKindModelRequestStart,
		Provider: "openai-codex",
		Model:    "gpt-5.5",
	})
	if !strings.Contains(message, "openai-codex-subscription / gpt-5.5") {
		t.Fatalf("expected display provider label, got %q", message)
	}
}

func TestFormatProgressEventMessageDoesNotPrefixToolMessageWithStage(t *testing.T) {
	message := formatProgressEventMessage(Config{}, ProgressEvent{
		Kind:     progressKindToolStarted,
		ToolName: "run_shell",
		Stage:    "workspace",
		Shard:    "runtime",
	})
	if strings.HasPrefix(message, "workspace runtime:") {
		t.Fatalf("expected non-model tool progress to avoid stage/shard prefix, got %q", message)
	}
	if !strings.Contains(message, "run_shell") {
		t.Fatalf("expected tool progress message, got %q", message)
	}
}

func TestProjectAnalyzerSerializesDefaultLocalModelRoute(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 6; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir pkg%d: %v", i, err)
		}
		body := fmt.Sprintf("package pkg%d\n\nfunc Run%d() int {\n\treturn %d\n}\n", i, i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.go", i)), []byte(body), 0o644); err != nil {
			t.Fatalf("write file%d.go: %v", i, err)
		}
	}

	cfg := DefaultConfig(root)
	cfg.Provider = "ollama"
	cfg.Model = "one-local-model"
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &singleRouteAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), "map the project", "map")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Summary.TotalShards < 2 {
		t.Fatalf("expected multiple shards for concurrency guard, got %d", run.Summary.TotalShards)
	}
	if run.Summary.AgentCount != 1 {
		t.Fatalf("expected default local model route to serialize, got agent count %d", run.Summary.AgentCount)
	}
	if client.concurrentFailures != 0 || client.maxActive != 1 {
		t.Fatalf("expected no overlapping provider calls, failures=%d max_active=%d", client.concurrentFailures, client.maxActive)
	}
	if run.Summary.ReviewProviderFailures != 0 {
		t.Fatalf("expected no provider failures after serialization, got %d", run.Summary.ReviewProviderFailures)
	}
}

func TestProjectAnalyzerDoesNotForceCloudSharedRouteToSerial(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = "openai"
	cfg.Model = "one-cloud-model"
	cfg.ProjectAnalysis.MinAgents = 8
	cfg.ProjectAnalysis.MaxAgents = 8
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, nil, ws, nil, nil)
	got := analyzer.effectiveShardConcurrency(8, 8, "map")
	if got != 4 {
		t.Fatalf("expected shared cloud route to follow default route limit 4 instead of serializing, got %d", got)
	}
}

func TestProjectAnalyzerCapsOpenRouterSharedRouteToProviderLimit(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = "openrouter"
	cfg.Model = "deepseek/deepseek-v4-pro"
	cfg.ProjectAnalysis.MinAgents = 8
	cfg.ProjectAnalysis.MaxAgents = 8
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, nil, ws, nil, nil)
	got := analyzer.effectiveShardConcurrency(8, 8, "map")
	if got != 2 {
		t.Fatalf("expected shared OpenRouter route to follow default provider limit 2, got %d", got)
	}
}

func TestProjectAnalyzerCapsExplicitAgentConfigToLocalRouteLimit(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 6; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir pkg%d: %v", i, err)
		}
		body := fmt.Sprintf("package pkg%d\n\nfunc Run%d() int {\n\treturn %d\n}\n", i, i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.go", i)), []byte(body), 0o644); err != nil {
			t.Fatalf("write file%d.go: %v", i, err)
		}
	}

	cfg := DefaultConfig(root)
	cfg.Provider = "ollama"
	cfg.Model = "one-local-model"
	cfg.ProjectAnalysis.MinAgents = 4
	cfg.ProjectAnalysis.MaxAgents = 4
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &singleRouteAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), "map the project", "map")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Summary.TotalShards < 2 {
		t.Fatalf("expected multiple shards for concurrency guard, got %d", run.Summary.TotalShards)
	}
	if run.Summary.AgentCount != 1 {
		t.Fatalf("expected local route limit to cap explicit agent config, got agent count %d", run.Summary.AgentCount)
	}
	if client.concurrentFailures != 0 || client.maxActive != 1 {
		t.Fatalf("expected no overlapping provider calls, failures=%d max_active=%d", client.concurrentFailures, client.maxActive)
	}
}

func TestProjectAnalyzerSerializesDefaultLocalModelRouteForRootCause(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 6; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir pkg%d: %v", i, err)
		}
		body := fmt.Sprintf("package pkg%d\n\nfunc Check%d(value int) bool {\n\treturn value == %d\n}\n", i, i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.go", i)), []byte(body), 0o644); err != nil {
			t.Fatalf("write file%d.go: %v", i, err)
		}
	}

	cfg := DefaultConfig(root)
	cfg.Provider = "ollama"
	cfg.Model = "one-local-model"
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	client := &singleRouteAnalysisClient{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	run, err := analyzer.Run(context.Background(), buildRootCauseGoal("Check fails for value 7"), "root-cause")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if run.Summary.TotalShards < 2 {
		t.Fatalf("expected multiple root-cause shards for concurrency guard, got %d", run.Summary.TotalShards)
	}
	if run.Summary.AgentCount != 1 {
		t.Fatalf("expected default local model root-cause route to serialize, got agent count %d", run.Summary.AgentCount)
	}
	if client.concurrentFailures != 0 || client.maxActive != 1 {
		t.Fatalf("expected no overlapping provider calls, failures=%d max_active=%d", client.concurrentFailures, client.maxActive)
	}
}

func TestCreateProviderClientFromProfileInheritsMainBaseURLForSameProvider(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "lmstudio"
	cfg.Model = "main-local"
	cfg.BaseURL = "http://127.0.0.1:8765/v1/"

	client, err := createProviderClientFromProfile(Profile{
		Provider: "lmstudio",
		Model:    "worker-local",
	}, cfg)
	if err != nil {
		t.Fatalf("createProviderClientFromProfile: %v", err)
	}
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected analysis role client to expose route metadata, got %T", client)
	}
	meta := metaProvider.ModelRouteMetadata()
	want := normalizeProviderBaseURL("lmstudio", cfg.BaseURL)
	if meta.BaseURL != want {
		t.Fatalf("expected analysis role client to inherit main base URL %q, got %q", want, meta.BaseURL)
	}
}

func TestNormalizeConfigPathsPreservesAnalysisRoleEmptyBaseURLForInheritance(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "lmstudio"
	cfg.Model = "main-local"
	cfg.BaseURL = "http://127.0.0.1:8765/v1/"
	cfg.ProjectAnalysis.WorkerProfile = &Profile{
		Provider: "lmstudio",
		Model:    "worker-local",
	}

	normalizeConfigPaths(&cfg)
	if cfg.ProjectAnalysis.WorkerProfile == nil {
		t.Fatalf("expected worker profile")
	}
	if cfg.ProjectAnalysis.WorkerProfile.BaseURL != "" {
		t.Fatalf("expected empty worker base URL to remain inheritable, got %q", cfg.ProjectAnalysis.WorkerProfile.BaseURL)
	}

	client, err := createProviderClientFromProfile(*cfg.ProjectAnalysis.WorkerProfile, cfg)
	if err != nil {
		t.Fatalf("createProviderClientFromProfile: %v", err)
	}
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected analysis role client to expose route metadata, got %T", client)
	}
	meta := metaProvider.ModelRouteMetadata()
	want := normalizeProviderBaseURL("lmstudio", cfg.BaseURL)
	if meta.BaseURL != want {
		t.Fatalf("expected normalized analysis role to inherit main base URL %q, got %q", want, meta.BaseURL)
	}
}

func TestCreateProviderClientFromProfileUsesProfileBaseURLOverride(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "lmstudio"
	cfg.Model = "main-local"
	cfg.BaseURL = "http://127.0.0.1:8765/v1/"
	override := "http://127.0.0.1:8766/v1/"

	client, err := createProviderClientFromProfile(Profile{
		Provider: "lmstudio",
		Model:    "worker-local",
		BaseURL:  override,
	}, cfg)
	if err != nil {
		t.Fatalf("createProviderClientFromProfile: %v", err)
	}
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected analysis role client to expose route metadata, got %T", client)
	}
	meta := metaProvider.ModelRouteMetadata()
	want := normalizeProviderBaseURL("lmstudio", override)
	if meta.BaseURL != want {
		t.Fatalf("expected analysis role client base URL override %q, got %q", want, meta.BaseURL)
	}
}

func TestCreateProviderClientFromProfileUsesProfileReasoningEffort(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai-codex"
	cfg.Model = "gpt-5.5"
	cfg.ReasoningEffort = "low"

	client, err := createProviderClientFromProfile(Profile{
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		ReasoningEffort: "high",
	}, cfg)
	if err != nil {
		t.Fatalf("createProviderClientFromProfile: %v", err)
	}
	metaProvider, ok := client.(modelRouteMetadataProvider)
	if !ok {
		t.Fatalf("expected analysis role client to expose route metadata, got %T", client)
	}
	if got := metaProvider.ModelRouteMetadata().ReasoningEffort; got != "high" {
		t.Fatalf("expected analysis role reasoning effort high, got %q", got)
	}
}

func TestAnalysisRouteForProfileMatchesInheritedBaseURL(t *testing.T) {
	route := analysisRouteForProfile(&Profile{
		Provider: "lmstudio",
		Model:    "worker-local",
	}, "lmstudio", "main-local", "http://127.0.0.1:8765/v1/")
	want := normalizeModelRouteBaseURL("lmstudio", "http://127.0.0.1:8765/v1/")
	if route.BaseURL != want {
		t.Fatalf("expected same-provider analysis route to inherit base URL %q, got %q", want, route.BaseURL)
	}
	if !strings.Contains(route.Label, want) {
		t.Fatalf("expected route label to include inherited base URL %q, got %q", want, route.Label)
	}
}

func TestConfigProjectAnalysisAllowsSingleAgentCap(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.ProjectAnalysis.MaxAgents = 1

	analysisCfg := configProjectAnalysis(cfg, root)
	if analysisCfg.MinAgents != 1 || analysisCfg.MaxAgents != 1 {
		t.Fatalf("expected max_agents=1 to be respected, got min=%d max=%d", analysisCfg.MinAgents, analysisCfg.MaxAgents)
	}
}

func TestRootCauseProjectAnalysisConfigPreservesExplicitSingleAgentCap(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.ProjectAnalysis.MaxAgents = 1

	analysisCfg := rootCauseProjectAnalysisConfig(configProjectAnalysis(cfg, root))
	if analysisCfg.MinAgents != 1 || analysisCfg.MaxAgents != 1 {
		t.Fatalf("expected root-cause limits to preserve explicit max_agents=1, got min=%d max=%d", analysisCfg.MinAgents, analysisCfg.MaxAgents)
	}
	if analysisCfg.MaxTotalShards != 8 || analysisCfg.MaxRefinementShards != 8 {
		t.Fatalf("expected root-cause shard caps to stay at 8, got total=%d refine=%d", analysisCfg.MaxTotalShards, analysisCfg.MaxRefinementShards)
	}
}

func TestConfigProjectAnalysisAllowsDisablingProviderRetries(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.ProjectAnalysis.MaxProviderRetries = -1

	analysisCfg := configProjectAnalysis(cfg, root)
	if analysisCfg.MaxProviderRetries != -1 {
		t.Fatalf("expected max_provider_retries=-1 to disable retries, got %d", analysisCfg.MaxProviderRetries)
	}
}

func TestAdaptiveProjectAnalysisShardSizingUsesLocalModelDefaults(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = "lmstudio"
	cfg.Model = "qwen/qwen3.6-27b"
	cfg.MaxTokens = 4096
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot := ProjectSnapshot{
		TotalFiles:  163,
		TotalLines:  45122,
		Directories: []string{"Source", "Public", "Private"},
	}

	notes := analyzer.applyAdaptiveAnalysisShardSizing(snapshot)
	if len(notes) == 0 {
		t.Fatalf("expected adaptive sizing note")
	}
	if analyzer.analysisCfg.MaxLinesPerShard != 5000 {
		t.Fatalf("expected qwen 27b local max_lines_per_shard=5000, got %d", analyzer.analysisCfg.MaxLinesPerShard)
	}
	if analyzer.analysisCfg.MaxFilesPerShard != 50 {
		t.Fatalf("expected qwen 27b local max_files_per_shard=50, got %d", analyzer.analysisCfg.MaxFilesPerShard)
	}
	shards := analyzer.estimateShardCount(snapshot, 1)
	if shards < 10 {
		t.Fatalf("expected shard estimate to reflect adaptive line cap, got %d", shards)
	}
}

func TestAdaptiveProjectAnalysisShardSizingPreservesExplicitShardLimits(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = "lmstudio"
	cfg.Model = "qwen/qwen3.6-27b"
	cfg.ProjectAnalysis.MaxLinesPerShard = 12000
	cfg.ProjectAnalysis.MaxFilesPerShard = 90
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot := ProjectSnapshot{
		TotalFiles: 120,
		TotalLines: 48000,
	}

	_ = analyzer.applyAdaptiveAnalysisShardSizing(snapshot)
	if analyzer.analysisCfg.MaxLinesPerShard != 12000 {
		t.Fatalf("expected explicit max_lines_per_shard to be preserved, got %d", analyzer.analysisCfg.MaxLinesPerShard)
	}
	if analyzer.analysisCfg.MaxFilesPerShard != 90 {
		t.Fatalf("expected explicit max_files_per_shard to be preserved, got %d", analyzer.analysisCfg.MaxFilesPerShard)
	}
}

func TestAnalysisShouldRetryWithSmallerShardsRecognizesProviderTimeouts(t *testing.T) {
	if !analysisShouldRetryWithSmallerShards(context.DeadlineExceeded) {
		t.Fatalf("expected wrapped deadline exceeded errors to trigger smaller-shard retry")
	}
	if !analysisShouldRetryWithSmallerShards(errors.New("context deadline exceeded")) {
		t.Fatalf("expected plain deadline exceeded text to trigger smaller-shard retry")
	}
	if analysisShouldRetryWithSmallerShards(context.Canceled) {
		t.Fatalf("expected user cancellation to skip automatic retry")
	}
	if !analysisShouldRetryWithSmallerShards(&ProviderAPIError{StatusCode: 503, Message: "service unavailable"}) {
		t.Fatalf("expected provider 5xx errors to trigger smaller-shard retry")
	}
	if analysisShouldRetryWithSmallerShards(&ProviderAPIError{StatusCode: 429, Message: "rate limit"}) {
		t.Fatalf("expected rate limits to skip smaller-shard retry")
	}
	if analysisShouldRetryWithSmallerShards(errors.New("invalid prompt template")) {
		t.Fatalf("expected non-transient provider errors to skip automatic retry")
	}
}

func TestAnalysisProviderFailureRecoveryConfigShrinksShards(t *testing.T) {
	cfg := ProjectAnalysisConfig{
		MaxLinesPerShard: 5000,
		MaxFilesPerShard: 50,
		MaxTotalShards:   8,
	}
	snapshot := ProjectSnapshot{
		TotalFiles: 163,
		TotalLines: 45122,
	}

	next, note, ok := analysisProviderFailureRecoveryConfig(cfg, snapshot)
	if !ok {
		t.Fatalf("expected recovery config to change shard sizing")
	}
	if next.MaxLinesPerShard != 2500 {
		t.Fatalf("expected max_lines_per_shard to shrink to 2500, got %d", next.MaxLinesPerShard)
	}
	if next.MaxFilesPerShard != 25 {
		t.Fatalf("expected max_files_per_shard to shrink to 25, got %d", next.MaxFilesPerShard)
	}
	if next.MaxTotalShards <= cfg.MaxTotalShards {
		t.Fatalf("expected max_total_shards to grow past %d, got %d", cfg.MaxTotalShards, next.MaxTotalShards)
	}
	for _, want := range []string{
		"max_lines_per_shard=5000->2500",
		"max_files_per_shard=50->25",
		"max_total_shards=8->",
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("expected recovery note to contain %q, got %q", want, note)
		}
	}
}

func TestCompleteAnalysisRequestWithRetryUsesRequestTimeout(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.RequestTimeoutSecs = 1
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	client := &blockingProviderClient{started: make(chan struct{})}
	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	analyzer.analysisCfg.MaxProviderRetries = 0

	start := time.Now()
	_, err := analyzer.completeAnalysisRequestWithRetry(context.Background(), client, "worker", "shard-01", "model", ChatRequest{
		Model: "model",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected request timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("expected timeout wrapper to return promptly, elapsed=%s", elapsed)
	}
}

func TestCompleteAnalysisRequestWithRetryWritesRuntimeErrorLog(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.ProjectAnalysis.ProviderRetryDelayMs = 1
	ws := Workspace{BaseRoot: root, Root: root}
	client := &flakyAnalysisClient{failuresRemaining: 1}
	analyzer := newProjectAnalyzer(cfg, client, ws, nil, nil)
	analyzer.analysisCfg.MaxProviderRetries = 1
	analyzer.analysisCfg.ProviderRetryDelayMs = 1

	_, err := analyzer.completeAnalysisRequestWithRetry(context.Background(), client, "worker", "runtime", "qwen/qwen3.6-27b", ChatRequest{
		Model: "qwen/qwen3.6-27b",
	})
	if err != nil {
		t.Fatalf("completeAnalysisRequestWithRetry: %v", err)
	}
	data, err := os.ReadFile(runtimeErrorLogPath(root))
	if err != nil {
		t.Fatalf("read runtime error log: %v", err)
	}
	text := string(data)
	for _, want := range []string{"provider_error", "worker", "runtime", "qwen/qwen3.6-27b", `"attempt":"1"`, `"final":"false"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected runtime error log to contain %q, got %q", want, text)
		}
	}
}

func TestSynthesizeFinalDocumentFallsBackOnProviderError(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Model = "analysis-model"
	cfg.ProjectAnalysis.ProviderRetryDelayMs = 1
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &failingAnalysisClient{target: "writing the final Markdown document"}, ws, nil, nil)
	snapshot := ProjectSnapshot{
		Root:       root,
		TotalFiles: 1,
		TotalLines: 4,
	}
	shards := []AnalysisShard{{ID: "shard-01", Name: "runtime", PrimaryFiles: []string{"main.go"}}}
	reports := []WorkerReport{{
		ShardID:          "shard-01",
		Title:            "runtime",
		ScopeSummary:     "Runtime summary.",
		Responsibilities: []string{"Run main flow"},
		KeyFiles:         []string{"main.go"},
		EvidenceFiles:    []string{"main.go"},
		Narrative:        "Runtime narrative.",
	}}
	doc, err := analyzer.synthesizeFinalDocument(context.Background(), snapshot, shards, reports, "map runtime")
	if err != nil {
		t.Fatalf("expected synthesis provider failure to fall back, got %v", err)
	}
	if !strings.Contains(doc, "Analysis With Provider Failures") || !strings.Contains(doc, "Runtime narrative") {
		t.Fatalf("expected fallback document with provider failure banner, got\n%s", doc)
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

func TestScanProjectWithExplicitRootDoesNotRescanParentExcludedCandidates(t *testing.T) {
	root := t.TempDir()
	writeAnalysisTestFile(t, filepath.Join(root, "external", "ignored.go"), "package external\n")
	writeAnalysisTestFile(t, filepath.Join(root, "src", "driver", "dispatch.go"), "package driver\nfunc Dispatch() {}\n")

	ws, paths, err := prepareExplicitAnalysisWorkspace(Workspace{BaseRoot: root, Root: root}, []string{"src/driver"})
	if err != nil {
		t.Fatalf("prepareExplicitAnalysisWorkspace returned error: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected explicit path to be consumed by scan root narrowing, got %#v", paths)
	}
	cfg := Config{}
	analyzer := newProjectAnalyzer(cfg, nil, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	for _, file := range snapshot.Files {
		if strings.Contains(file.Path, "external") || strings.Contains(file.Path, "ignored.go") {
			t.Fatalf("expected parent external directory to stay out of scoped scan, got %#v", snapshot.Files)
		}
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0].Path != "dispatch.go" {
		t.Fatalf("expected only scoped target file, got %#v", snapshot.Files)
	}
	candidates, err := findAnalysisDirectoryCandidates(ws.Root, analyzer.analysisCfg)
	if err != nil {
		t.Fatalf("findAnalysisDirectoryCandidates returned error: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected parent external candidate not to be rediscovered from scoped root, got %#v", candidates)
	}
}

func writeAnalysisTestFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
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

	mustWrite("SampleApp/Common/ETWConsumer.cpp", "int Etw()\n{\n    return 1;\n}\n")
	mustWrite("SampleApp/SampleApp/x64/Live/SampleApp.tlog/link.command.1.tlog", "^link\n")
	mustWrite("SampleApp/SampleApp/x64/Live/SampleApp.lastbuildstate", "state\n")
	mustWrite("SampleApp/SampleWorker/x64/Release/SampleWorker.tlog/CL.read.1.tlog", "log\n")

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
	if _, ok := snapshot.FilesByPath["SampleApp/Common/ETWConsumer.cpp"]; !ok {
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

func TestScanProjectBuildAlignmentCapturesCompileCommands(t *testing.T) {
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

	mustWrite("Source/GuardRuntime/GuardRuntime.Build.cs", `public class GuardRuntime : ModuleRules { public GuardRuntime(ReadOnlyTargetRules Target) : base(Target) {} }`)
	mustWrite("Source/GuardRuntime/Private/IoctlDispatch.cpp", `bool ValidateRequest() { return true; }
int GuardDispatch() { if (ValidateRequest()) { DeviceIoControl(0, 0, 0, 0, 0, 0, 0, 0); } return 0; }
`)
	mustWrite("native/cmake-build-debug/compile_commands.json", `[
  {
    "directory": "`+filepath.ToSlash(root)+`",
    "file": "Source/GuardRuntime/Private/IoctlDispatch.cpp",
    "arguments": ["clang++", "-I", "Source/GuardRuntime/Public", "-DGUARD_BUILD", "-include", "pch.h", "-c", "Source/GuardRuntime/Private/IoctlDispatch.cpp"]
  }
]`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}

	if _, ok := snapshot.FilesByPath["native/cmake-build-debug/compile_commands.json"]; ok {
		t.Fatalf("expected compile_commands.json to stay excluded from scanned files")
	}
	if len(snapshot.CompileCommands) != 1 {
		t.Fatalf("expected compile command metadata, got %+v", snapshot.CompileCommands)
	}
	command := snapshot.CompileCommands[0]
	if command.File != "Source/GuardRuntime/Private/IoctlDispatch.cpp" {
		t.Fatalf("unexpected compile command file: %+v", command)
	}
	if !containsString(snapshot.CompileCommands[0].Defines, "GUARD_BUILD") {
		t.Fatalf("expected define extraction, got %+v", command)
	}
	if !containsString(snapshot.CompileCommands[0].ForceIncludes, "pch.h") {
		t.Fatalf("expected force include extraction, got %+v", command)
	}
	foundContext := false
	for _, ctx := range snapshot.BuildContexts {
		if ctx.Module == "GuardRuntime" && strings.Contains(ctx.ID, "buildctx:compile:module:GuardRuntime") {
			foundContext = true
			break
		}
	}
	if !foundContext {
		t.Fatalf("expected compile build context, got %+v", snapshot.BuildContexts)
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
			PrimaryStartup:    "SampleWorker",
			TopImportantFiles: []string{"SampleWorker/main.cpp"},
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
	if analysisQueryMeaningfullyChanged("Explain SampleWorker startup flow.", "Now summarize risks only.") {
		t.Fatalf("expected short follow-up not to trigger reinjection")
	}
	if !analysisQueryMeaningfullyChanged("Explain SampleWorker startup flow.", "Explain Common IPC module architecture in detail.") {
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
	if !strings.Contains(prompt, "context-truncated/source-limited") {
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

func TestBuildWorkerPromptIncludesBaselineMapContext(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:         "C:\\repo",
		AnalysisMode: "trace",
		BaselineMap: AnalysisBaselineMap{
			RunID:          "run-map",
			Goal:           "map SampleKernel architecture",
			ArtifactPath:   "C:/repo/.kernforge/analysis/run-map_map.json",
			ProjectSummary: "SampleKernel separates driver dispatch, worker scanning, and reporting.",
			Subsystems:     []string{"Driver Dispatch", "Worker Scanner"},
			SourceAnchors:  []string{"SampleKernel/SampleKernel/DriverDispatch.cpp", "SampleKernel/SampleKernel/WorkerScan.cpp"},
		},
		FilesByPath: map[string]ScannedFile{
			"SampleKernel/SampleKernel/DriverDispatch.cpp": {Path: "SampleKernel/SampleKernel/DriverDispatch.cpp", Directory: "SampleKernel/SampleKernel"},
		},
	}
	shard := AnalysisShard{
		ID:           "shard-01",
		Name:         "runtime_flow",
		PrimaryFiles: []string{"SampleKernel/SampleKernel/DriverDispatch.cpp"},
	}
	prompt := buildWorkerPrompt(snapshot, shard, "trace driver dispatch", "")
	for _, expected := range []string{
		"Baseline architecture map:",
		"Baseline run: run-map",
		"SampleKernel separates driver dispatch",
		"Relevant anchors: SampleKernel/SampleKernel/DriverDispatch.cpp",
		"Use this map as prior structure only",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected baseline prompt to contain %q\n%s", expected, prompt)
		}
	}
}

func TestLoadBaselineMapForTraceUsesPreviousMapRun(t *testing.T) {
	outputDir := t.TempDir()
	completedAt := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	mapRun := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:       "run-map",
			Goal:        "map SampleKernel architecture",
			Mode:        "map",
			Status:      "completed",
			CompletedAt: completedAt,
			OutputPath:  filepath.Join(outputDir, "run-map_map_samplekernel_architecture.md"),
		},
		Snapshot: ProjectSnapshot{
			Root: "C:\\repo",
			Files: []ScannedFile{
				{Path: "SampleKernel/SampleKernel/DriverDispatch.cpp", Directory: "SampleKernel/SampleKernel", ImportanceScore: 10},
			},
		},
		KnowledgePack: KnowledgePack{
			RunID:          "run-map",
			Goal:           "map SampleKernel architecture",
			AnalysisMode:   "map",
			ProjectSummary: "SampleKernel dispatches requests through the driver boundary.",
			TopImportantFiles: []string{
				"SampleKernel/SampleKernel/DriverDispatch.cpp",
			},
			Subsystems: []KnowledgeSubsystem{
				{
					Title:         "Driver Dispatch",
					KeyFiles:      []string{"SampleKernel/SampleKernel/DriverDispatch.cpp"},
					EvidenceFiles: []string{"SampleKernel/SampleKernel/Ioctl.cpp"},
				},
			},
		},
	}
	data, err := json.MarshalIndent(mapRun, "", "  ")
	if err != nil {
		t.Fatalf("marshal map run: %v", err)
	}
	mapPath := filepath.Join(outputDir, "20260425-100000_map_samplekernel_architecture.json")
	if err := os.WriteFile(mapPath, data, 0o644); err != nil {
		t.Fatalf("write map run: %v", err)
	}
	analyzer := &projectAnalyzer{
		analysisCfg: ProjectAnalysisConfig{OutputDir: outputDir},
	}
	baseline, ok := analyzer.loadBaselineMapForMode("trace", AnalysisGoalScope{DirectoryPrefixes: []string{"SampleKernel/SampleKernel"}})
	if !ok {
		t.Fatalf("expected baseline map to load")
	}
	if baseline.RunID != "run-map" {
		t.Fatalf("expected run-map baseline, got %q", baseline.RunID)
	}
	if !strings.Contains(baseline.ProjectSummary, "driver boundary") {
		t.Fatalf("unexpected baseline summary: %q", baseline.ProjectSummary)
	}
	if len(baseline.SourceAnchors) == 0 || baseline.SourceAnchors[0] != "SampleKernel/SampleKernel/DriverDispatch.cpp" {
		t.Fatalf("unexpected baseline anchors: %#v", baseline.SourceAnchors)
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

func TestAnalysisStructuredMaxTokensAddsKimiHeadroom(t *testing.T) {
	if got := analysisStructuredMaxTokens("opencode-go/kimi-k2.6", 4096); got != 8192 {
		t.Fatalf("expected kimi budget headroom, got %d", got)
	}
	if got := analysisStructuredMaxTokens("openrouter/z-ai/glm-5.1", 4096); got != 4096 {
		t.Fatalf("expected non-kimi budget to remain unchanged, got %d", got)
	}
	if got := analysisStructuredMaxTokens("opencode-go/kimi-k2.6", 12000); got != 12000 {
		t.Fatalf("expected explicit larger budget to be preserved, got %d", got)
	}
}

func TestFallbackWorkerReportDoesNotInjectRawIntoNarrative(t *testing.T) {
	report := fallbackWorkerReport(AnalysisShard{
		ID:           "shard-01",
		Name:         "runtime",
		PrimaryFiles: []string{"main.go"},
	}, `{"report":{"title":"runtime"`)
	if strings.TrimSpace(report.Narrative) != "" {
		t.Fatalf("expected malformed raw output to stay out of narrative, got %q", report.Narrative)
	}
	if !strings.Contains(report.Raw, `"report"`) {
		t.Fatalf("expected raw output to be preserved, got %q", report.Raw)
	}
	if len(report.Unknowns) == 0 {
		t.Fatalf("expected unknown marker for malformed worker output")
	}
}

func TestParseWorkerReportRejectsSchemaPlaceholder(t *testing.T) {
	_, ok := parseWorkerReportPayload(`{
  "report": {
    "title": "string",
    "scope_summary": "string",
    "responsibilities": ["string"],
    "facts": ["string"],
    "inferences": ["string"],
    "key_files": ["string"],
    "entry_points": ["string"],
    "internal_flow": ["string"],
    "dependencies": ["string"],
    "collaboration": ["string"],
    "risks": ["string"],
    "unknowns": ["string"],
    "evidence_files": ["string"],
    "narrative": "string"
  }
}`, AnalysisShard{ID: "shard-01", Name: "runtime"})
	if ok {
		t.Fatalf("expected schema placeholder worker report to be rejected")
	}
}

func TestParseWorkerReportRejectsEmptyJSONPayload(t *testing.T) {
	shard := AnalysisShard{
		ID:           "shard-01",
		Name:         "runtime",
		PrimaryFiles: []string{"main.go"},
	}
	for _, raw := range []string{`{}`, `{"report":{}}`} {
		if report, ok := parseWorkerReportPayload(raw, shard); ok {
			t.Fatalf("expected empty worker report %s to be rejected, got %+v", raw, report)
		}
	}
}

func TestNormalizeWorkerReportCanonicalizesEvidencePaths(t *testing.T) {
	shard := AnalysisShard{
		ID: "shard-01",
		PrimaryFiles: []string{
			"SampleKernel/SampleKernelPolicy.h",
			"Common/UserCommon.h",
		},
		ReferenceFiles: []string{
			"SampleKernel/SampleKernelPolicy.cpp",
			"SampleKernel/SampleKernelCore.cpp",
		},
	}
	report := WorkerReport{
		KeyFiles: []string{
			"SampleKernelPolicy.h",
			"SampleKernelPolicy.cpp",
			"NotInScope.cpp",
		},
		EvidenceFiles: []string{
			"UserCommon.h",
			"SampleKernelCore.cpp (allowed reference)",
			"Other.cpp",
		},
	}
	normalizeWorkerReport(&report, shard)
	wantKey := []string{
		"SampleKernel/SampleKernelPolicy.h",
		"SampleKernel/SampleKernelPolicy.cpp",
	}
	wantEvidence := []string{
		"Common/UserCommon.h",
		"SampleKernel/SampleKernelCore.cpp",
	}
	if !reflect.DeepEqual(report.KeyFiles, wantKey) {
		t.Fatalf("key files = %#v, want %#v", report.KeyFiles, wantKey)
	}
	if !reflect.DeepEqual(report.EvidenceFiles, wantEvidence) {
		t.Fatalf("evidence files = %#v, want %#v", report.EvidenceFiles, wantEvidence)
	}
}

func TestNormalizeWorkerReportFiltersMetadataReferencedFilesFromKeyFiles(t *testing.T) {
	shard := AnalysisShard{
		ID:           "shard-01",
		PrimaryFiles: []string{"SampleKernelTestConsole/SampleKernelTestConsole.vcxproj.filters"},
	}
	report := WorkerReport{
		KeyFiles: []string{
			"SampleKernelTestConsole/SampleKernelTestConsole.vcxproj.filters",
			"SampleKernelTestConsole/SampleKernelTestConsole.cpp",
			"SampleKernelTestConsole/SampleKernelManager.cpp",
			"SampleKernelTestConsole/SampleKernelManager.h",
		},
		EvidenceFiles: []string{
			"SampleKernelTestConsole/SampleKernelTestConsole.vcxproj.filters (inspected)",
			"SampleKernelTestConsole/SampleKernelManager.cpp (referenced by filter metadata)",
		},
	}
	normalizeWorkerReport(&report, shard)
	want := []string{"SampleKernelTestConsole/SampleKernelTestConsole.vcxproj.filters"}
	if !reflect.DeepEqual(report.KeyFiles, want) {
		t.Fatalf("key files = %#v, want %#v", report.KeyFiles, want)
	}
	if !reflect.DeepEqual(report.EvidenceFiles, want) {
		t.Fatalf("evidence files = %#v, want %#v", report.EvidenceFiles, want)
	}
}

func TestBuildReviewerPromptOmitsRawWorkerPayload(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root: "C:\\repo",
		FilesByPath: map[string]ScannedFile{
			"core.cpp": {Path: "core.cpp", LineCount: 1},
		},
	}
	shard := AnalysisShard{
		ID:           "shard-01",
		Name:         "core",
		PrimaryFiles: []string{"core.cpp"},
	}
	report := WorkerReport{
		ShardID:          "shard-01",
		Title:            "core",
		ScopeSummary:     "summary",
		Responsibilities: []string{"owns core"},
		KeyFiles:         []string{"core.cpp"},
		EntryPoints:      []string{"Init"},
		InternalFlow:     []string{"Init -> Run"},
		EvidenceFiles:    []string{"core.cpp"},
		Raw:              `{"report":{"evidence_files":["out-of-scope.cpp"]}}`,
	}
	prompt := buildReviewerPrompt(snapshot, shard, report, "goal", WorkerReport{}, false)
	if strings.Contains(prompt, `"raw"`) || strings.Contains(prompt, "out-of-scope.cpp") {
		t.Fatalf("reviewer prompt should not include raw worker payload\n%s", prompt)
	}
}

func TestBuildReviewerPromptOmitsPreviousRawWorkerPayload(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root: "C:\\repo",
		FilesByPath: map[string]ScannedFile{
			"core.cpp": {Path: "core.cpp", LineCount: 1},
		},
	}
	shard := AnalysisShard{
		ID:                 "shard-01",
		Name:               "core",
		PrimaryFiles:       []string{"core.cpp"},
		InvalidationReason: "dependency_changed",
	}
	current := WorkerReport{
		ShardID:          "shard-01",
		Title:            "core",
		ScopeSummary:     "summary",
		Responsibilities: []string{"owns core"},
		KeyFiles:         []string{"core.cpp"},
		EntryPoints:      []string{"Init"},
		InternalFlow:     []string{"Init -> Run"},
		EvidenceFiles:    []string{"core.cpp"},
	}
	previous := current
	previous.Raw = `{"report":{"evidence_files":["stale-out-of-scope.cpp"]}}`
	prompt := buildReviewerPrompt(snapshot, shard, current, "goal", previous, true)
	if strings.Contains(prompt, `"raw"`) || strings.Contains(prompt, "stale-out-of-scope.cpp") {
		t.Fatalf("reviewer prompt should not include previous raw worker payload\n%s", prompt)
	}
}

func TestBuildArchitectureFactPackCapturesDriverFacts(t *testing.T) {
	run := sampleDriverProjectStructureQARun()
	pack := buildArchitectureFactPack(run.Snapshot, run.SemanticIndexV2, run.UnrealGraph, run.Summary.Goal)
	if !architectureFactPackHasData(pack) {
		t.Fatalf("expected architecture facts")
	}
	if !containsStringCI(pack.DomainHints, "windows_driver") {
		t.Fatalf("expected windows driver domain hint, got %+v", pack.DomainHints)
	}
	rootRows := []string{}
	for _, dir := range pack.TopLevelDirectories {
		rootRows = append(rootRows, dir.Path)
	}
	for _, want := range []string{"Common/", "SampleKernel/", "SampleKernelTestConsole/"} {
		if !containsString(rootRows, want) {
			t.Fatalf("expected root directory %s in %+v", want, rootRows)
		}
	}
	for _, bad := range []string{"SampleKernel/BuildCab/", "SampleKernel/BuildCab/SampleKernel.inf"} {
		if containsString(rootRows, bad) {
			t.Fatalf("did not expect nested/file path as top-level directory: %+v", rootRows)
		}
	}
	if !containsString(pack.TopLevelNonDirectoryExclusions, "SampleKernel/BuildCab/SampleKernel.inf") {
		t.Fatalf("expected nested/file exclusion, got %+v", pack.TopLevelNonDirectoryExclusions)
	}
	if !architectureAnchorRoleHasLocation(pack.CriticalAnchors, "object_callback_registration", "SampleKernel/SampleKernelObjectFilter.cpp:106") {
		t.Fatalf("expected exact object callback registration anchor, got %+v", pack.CriticalAnchors)
	}
	if !architectureFlowContains(pack.FlowFacts, "REQUIRED device-control command spine", "DecryptIoctlData", "IsValidCommand") {
		t.Fatalf("expected required IOCTL command spine, got %+v", pack.FlowFacts)
	}
}

func TestArchitectureFactPackInjectedIntoPrompts(t *testing.T) {
	run := sampleDriverProjectStructureQARun()
	run.Snapshot.ArchitectureFacts = buildArchitectureFactPack(run.Snapshot, run.SemanticIndexV2, run.UnrealGraph, run.Summary.Goal)
	shard := AnalysisShard{
		ID:             "shard-ioctl",
		Name:           "security_ioctl",
		PrimaryFiles:   []string{"SampleKernel/SampleKernelCore.cpp"},
		ReferenceFiles: []string{"SampleKernel/SampleKernelAPI.cpp"},
	}
	report := WorkerReport{
		ShardID:          shard.ID,
		Title:            "security_ioctl",
		ScopeSummary:     "IOCTL dispatch",
		Responsibilities: []string{"Own kernel IOCTL dispatch"},
		KeyFiles:         []string{"SampleKernel/SampleKernelCore.cpp"},
		EntryPoints:      []string{"SampleKernelCore::DeviceIoControlIrpHandleRoutine"},
		InternalFlow:     []string{"device control -> decrypt -> command validation"},
		EvidenceFiles:    []string{"SampleKernel/SampleKernelCore.cpp"},
	}
	workerPrompt := buildWorkerPrompt(run.Snapshot, shard, run.Summary.Goal, "")
	reviewerPrompt := buildReviewerPrompt(run.Snapshot, shard, report, run.Summary.Goal, WorkerReport{}, false)
	synthesisPrompt := buildSynthesisPrompt(run.Snapshot, []AnalysisShard{shard}, []WorkerReport{report}, run.Summary.Goal)
	for name, text := range map[string]string{
		"worker":    workerPrompt,
		"reviewer":  reviewerPrompt,
		"synthesis": synthesisPrompt,
	} {
		if !strings.Contains(text, "Deterministic architecture fact pack") {
			t.Fatalf("%s prompt missing architecture fact pack\n%s", name, text)
		}
		if !strings.Contains(text, "SampleKernelCore::DeviceIoControlIrpHandleRoutine") {
			t.Fatalf("%s prompt missing exact IOCTL anchor\n%s", name, text)
		}
	}
	if !strings.Contains(synthesisPrompt, "Closed top-level directory set") {
		t.Fatalf("synthesis prompt should include closed root set\n%s", synthesisPrompt)
	}
}

func architectureAnchorRoleHasLocation(items []ArchitectureAnchorFact, role string, location string) bool {
	for _, item := range items {
		if strings.EqualFold(item.Role, role) && strings.EqualFold(item.Location, location) {
			return true
		}
	}
	return false
}

func architectureFlowContains(items []ArchitectureFlowFact, name string, tokens ...string) bool {
	for _, item := range items {
		text := strings.ToLower(strings.Join(append([]string{item.Name, item.Summary}, item.Steps...), " "))
		if !strings.Contains(text, strings.ToLower(name)) {
			continue
		}
		missing := false
		for _, token := range tokens {
			if !strings.Contains(text, strings.ToLower(token)) {
				missing = true
				break
			}
		}
		if !missing {
			return true
		}
	}
	return false
}

func TestAnalysisPromptExcerptTruncatesByRune(t *testing.T) {
	excerpt := analysisPromptExcerpt("가나다라마", 3)
	if !strings.HasPrefix(excerpt, "가나다") || !strings.Contains(excerpt, "truncated") {
		t.Fatalf("unexpected excerpt: %q", excerpt)
	}
}

func TestBuildSynthesisPromptCompactsLargeReportCorpus(t *testing.T) {
	longText := strings.Repeat("source-backed subsystem detail with runtime flow and verification anchors. ", 120)
	snapshot := ProjectSnapshot{
		Root:       "F:/large",
		TotalFiles: 3000,
		TotalLines: 900000,
		Files: []ScannedFile{
			{Path: "src/main.cpp", ImportanceScore: 100, ImportanceReasons: []string{"entrypoint"}},
		},
		FilesByPath: map[string]ScannedFile{},
	}
	shards := make([]AnalysisShard, 0, 96)
	reports := make([]WorkerReport, 0, 96)
	for i := 0; i < 96; i++ {
		file := fmt.Sprintf("src/subsystem_%02d.cpp", i)
		shards = append(shards, AnalysisShard{
			ID:               fmt.Sprintf("shard-%02d", i),
			Name:             fmt.Sprintf("subsystem_%02d", i),
			PrimaryFiles:     []string{file},
			ReferenceFiles:   []string{fmt.Sprintf("include/subsystem_%02d.h", i)},
			InvalidationDiff: []string{longText},
		})
		reports = append(reports, WorkerReport{
			ShardID:          fmt.Sprintf("shard-%02d", i),
			Title:            fmt.Sprintf("Subsystem %02d", i),
			ScopeSummary:     longText,
			Responsibilities: []string{longText, longText},
			Facts:            []string{longText, longText, longText},
			Inferences:       []string{longText, longText},
			Claims: []AnalysisClaim{
				{Claim: longText, SourceAnchors: []string{file}, Confidence: "medium", DisprovesWhen: longText, VerificationHint: longText},
			},
			KeyFiles:      []string{file},
			EntryPoints:   []string{fmt.Sprintf("Subsystem%02d::Start", i), longText},
			InternalFlow:  []string{longText, longText},
			Dependencies:  []string{longText},
			Collaboration: []string{longText},
			Risks:         []string{longText},
			Unknowns:      []string{longText},
			EvidenceFiles: []string{file},
			Narrative:     longText,
		})
	}

	prompt := buildSynthesisPrompt(snapshot, shards, reports, "프로젝트 구조를 분석해서 문서로 작성해")
	if got := len([]rune(prompt)); got > analysisSynthesisPromptMaxRunes {
		t.Fatalf("expected synthesis prompt to stay within budget, got %d > %d", got, analysisSynthesisPromptMaxRunes)
	}
	if !strings.Contains(prompt, "Prompt budget note") {
		t.Fatalf("expected compact prompt to disclose prompt-budget compaction\n%s", prompt)
	}
	if !strings.Contains(prompt, "Full shard reports, structured JSON, docs, and vector artifacts are still written locally") {
		t.Fatalf("expected omitted-section summary to preserve artifact guidance\n%s", prompt)
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

func TestSynthesisGroupForShardAdaptsToSampleAppStyleModules(t *testing.T) {
	if got := synthesisGroupForShard(AnalysisShard{PrimaryFiles: []string{"SampleMaster/SampleMaster.cpp"}}, WorkerReport{}); got != "Security Control" {
		t.Fatalf("expected SampleMaster to map to Security Control, got %q", got)
	}
	if got := synthesisGroupForShard(AnalysisShard{PrimaryFiles: []string{"SampleWorker/PrefetchScanner.cpp"}}, WorkerReport{}); got != "Forensic Analysis" {
		t.Fatalf("expected SampleWorker to map to Forensic Analysis, got %q", got)
	}
	if got := synthesisGroupForShard(AnalysisShard{PrimaryFiles: []string{"External/aws/include/aws/auth/auth.h"}}, WorkerReport{}); got != "External Dependencies" {
		t.Fatalf("expected External to map to External Dependencies, got %q", got)
	}
}

func TestGroupedReportsForSynthesisPushesExternalDependenciesLast(t *testing.T) {
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "misc", PrimaryFiles: []string{"External/aws/include/aws/auth/auth.h"}},
		{ID: "shard-02", Name: "misc", PrimaryFiles: []string{"SampleMaster/SampleMaster.cpp"}},
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

func TestBuildAnalysisDocsCreatesOperationalDocumentSet(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:          "run-docs",
			Goal:           "map driver security surface",
			Mode:           "security",
			TotalShards:    2,
			ApprovedShards: 2,
		},
		FinalDocument: "# Driver Security Report\n\nSolution startup candidate:\n- `launcher.exe`\n\nDriver dispatch owns the user/kernel boundary.\n",
		Snapshot: ProjectSnapshot{
			Root:            "C:\\repo",
			AnalysisMode:    "security",
			TotalFiles:      12,
			TotalLines:      3400,
			EntrypointFiles: []string{"driver/dispatch.cpp"},
			ManifestFiles:   []string{"driver/guard.vcxproj"},
			RuntimeEdges: []RuntimeEdge{
				{Source: "launcher.exe", Target: "guard.sys", Kind: "dynamic_load", Confidence: "high", Evidence: []string{"driver/guard.vcxproj"}},
			},
			ProjectEdges: []ProjectEdge{
				{Source: "user-mode client", Target: "kernel driver", Type: "trust_boundary", Confidence: "high", Evidence: []string{"driver/dispatch.cpp"}, Attributes: map[string]string{"kind": "ioctl", "flow": "user_to_kernel"}},
			},
			BuildContexts: []BuildContextRecord{
				{ID: "buildctx:driver", Name: "driver", Kind: "compile", Module: "guard", Files: []string{"driver/dispatch.cpp"}},
			},
			CompileCommands: []CompilationCommandRecord{
				{File: "driver/dispatch.cpp", Compiler: "clang-cl.exe", Source: "compile_commands.json"},
			},
		},
		KnowledgePack: KnowledgePack{
			RunID:          "run-docs",
			Goal:           "map driver security surface",
			AnalysisMode:   "security",
			Root:           "C:\\repo",
			ProjectSummary: "Driver dispatch owns the user/kernel boundary.",
			HighRiskFiles:  []string{"driver/dispatch.cpp"},
			ProjectEdges: []ProjectEdge{
				{Source: "telemetry parser", Target: "evidence store", Type: "security", Confidence: "medium", Evidence: []string{"telemetry/parser.cpp"}, Attributes: map[string]string{"kind": "telemetry"}},
			},
			Subsystems: []KnowledgeSubsystem{
				{
					Title:                "Driver Dispatch",
					Group:                "Security Surface",
					Responsibilities:     []string{"Validate IOCTL buffers"},
					EntryPoints:          []string{"DispatchIoctl"},
					KeyFiles:             []string{"driver/dispatch.cpp"},
					Risks:                []string{"Input size can diverge from copy size"},
					EvidenceFiles:        []string{"driver/dispatch.cpp"},
					CacheStatuses:        []string{"miss"},
					InvalidationReasons:  []string{"semantic_primary_changed"},
					InvalidationEvidence: []string{"driver/dispatch.cpp"},
				},
			},
			AnalysisExecution: AnalysisExecutionSummary{
				TotalShards:         2,
				MissedShards:        1,
				TopChangeClasses:    []string{"ioctl_surface_changed (1)"},
				InvalidationReasons: []string{"semantic_primary_changed"},
			},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{
					ID:             "func:DispatchIoctl@driver/dispatch.cpp",
					Name:           "DispatchIoctl",
					Kind:           "function",
					File:           "driver/dispatch.cpp",
					Signature:      "NTSTATUS DispatchIoctl(...)",
					StartLine:      42,
					BuildContextID: "buildctx:driver",
					Tags:           []string{"ioctl", "dispatch"},
				},
			},
			CallEdges: []CallEdge{
				{SourceID: "func:DispatchIoctl@driver/dispatch.cpp", TargetID: "func:ValidateIoctl@driver/dispatch.cpp", Type: "calls"},
			},
		},
	}

	docs := buildAnalysisDocs(run)
	for _, name := range analysisGeneratedDocNames() {
		if strings.TrimSpace(docs[name]) == "" {
			t.Fatalf("expected generated doc %s", name)
		}
	}
	if !strings.Contains(docs["DEVELOPER_OVERVIEW.md"], "Where To Start By Task") {
		t.Fatalf("expected developer overview guidance\n%s", docs["DEVELOPER_OVERVIEW.md"])
	}
	if !strings.Contains(docs["FOLDER_MAP.md"], "Folder Summary") {
		t.Fatalf("expected folder map summary\n%s", docs["FOLDER_MAP.md"])
	}
	if !strings.Contains(docs["MODULES.md"], "Module Inventory") {
		t.Fatalf("expected modules inventory\n%s", docs["MODULES.md"])
	}
	if !strings.Contains(docs["FINAL_REPORT.md"], "Solution startup candidate:") || !strings.Contains(docs["FINAL_REPORT.md"], "Driver Security Report") {
		t.Fatalf("expected final report doc to preserve assistant-facing report\n%s", docs["FINAL_REPORT.md"])
	}
	if !strings.Contains(docs["INDEX.md"], "FINAL_REPORT.md") {
		t.Fatalf("expected docs index to link final report\n%s", docs["INDEX.md"])
	}
	if !strings.Contains(docs["FUZZ_TARGETS.md"], `/fuzz-func DispatchIoctl --file "driver/dispatch.cpp"`) {
		t.Fatalf("expected fuzz target suggestion\n%s", docs["FUZZ_TARGETS.md"])
	}
	if !strings.Contains(docs["FUZZ_TARGETS.md"], "Priority score:") || !strings.Contains(docs["FUZZ_TARGETS.md"], "Harness readiness:") {
		t.Fatalf("expected enriched fuzz target catalog\n%s", docs["FUZZ_TARGETS.md"])
	}
	if !strings.Contains(docs["SECURITY_SURFACE.md"], "DispatchIoctl") {
		t.Fatalf("expected security surface to include indexed IOCTL symbol\n%s", docs["SECURITY_SURFACE.md"])
	}
	if !strings.Contains(docs["SECURITY_SURFACE.md"], "Source anchors:") || !strings.Contains(docs["SECURITY_SURFACE.md"], "Confidence:") {
		t.Fatalf("expected security surface doc metadata\n%s", docs["SECURITY_SURFACE.md"])
	}
	if !strings.Contains(docs["VERIFICATION_MATRIX.md"], "Driver or IOCTL") {
		t.Fatalf("expected verification matrix to include driver row\n%s", docs["VERIFICATION_MATRIX.md"])
	}
}

func TestWriteAnalysisDocsPersistsManifest(t *testing.T) {
	dir := t.TempDir()
	completedAt := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	run := ProjectAnalysisRun{
		Summary:  ProjectAnalysisSummary{RunID: "run-docs", Goal: "map runtime", Mode: "map", Status: "completed", CompletedAt: completedAt, TotalShards: 1, ApprovedShards: 1},
		Snapshot: ProjectSnapshot{Root: dir, TotalFiles: 1, TotalLines: 10},
		KnowledgePack: KnowledgePack{
			RunID: "run-docs",
			Goal:  "map runtime",
			Root:  dir,
			Subsystems: []KnowledgeSubsystem{
				{Title: "Runtime", Responsibilities: []string{"Run commands"}},
			},
		},
	}
	manifest, err := writeAnalysisDocs(run, filepath.Join(dir, "docs"))
	if err != nil {
		t.Fatalf("writeAnalysisDocs returned error: %v", err)
	}
	if manifest.DocumentCount != len(analysisGeneratedDocNames()) {
		t.Fatalf("expected %d generated docs, got %+v", len(analysisGeneratedDocNames()), manifest)
	}
	if !manifest.GeneratedAt.Equal(completedAt) {
		t.Fatalf("expected deterministic generated_at %s, got %s", completedAt, manifest.GeneratedAt)
	}
	if len(manifest.ReuseTargets) == 0 {
		t.Fatalf("expected manifest reuse targets")
	}
	if manifest.SchemaVersion != analysisDocsManifestSchemaVersion {
		t.Fatalf("expected schema version %q, got %+v", analysisDocsManifestSchemaVersion, manifest)
	}
	if manifest.MinReaderVersion != analysisDocsManifestMinReaderVersion {
		t.Fatalf("expected min reader version %q, got %+v", analysisDocsManifestMinReaderVersion, manifest)
	}
	if manifest.CompatibilityPolicy != analysisDocsManifestCompatPolicy {
		t.Fatalf("expected compatibility policy %q, got %+v", analysisDocsManifestCompatPolicy, manifest)
	}
	for _, doc := range manifest.Documents {
		if strings.TrimSpace(doc.Confidence) == "" {
			t.Fatalf("expected doc confidence in %+v", doc)
		}
		if doc.Name == "ARCHITECTURE.md" && len(doc.ReuseTargets) == 0 {
			t.Fatalf("expected doc reuse targets in %+v", doc)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "manifest.json")); err != nil {
		t.Fatalf("expected manifest on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "ARCHITECTURE.md")); err != nil {
		t.Fatalf("expected architecture doc on disk: %v", err)
	}
}

func TestDecodeAnalysisDocsManifestBackfillsLegacySchema(t *testing.T) {
	data := []byte(`{
		"run_id": "legacy-run",
		"document_count": 1,
		"documents": [
			{"name": "VERIFICATION_MATRIX.md"}
		],
		"verification_matrix": [
			{"change_area": "Driver or IOCTL", "required_verification": "driver build"}
		]
	}`)
	manifest, err := decodeAnalysisDocsManifest(data)
	if err != nil {
		t.Fatalf("decodeAnalysisDocsManifest returned error: %v", err)
	}
	if manifest.SchemaVersion != analysisDocsManifestLegacySchema {
		t.Fatalf("expected legacy schema marker, got %+v", manifest)
	}
	if manifest.MinReaderVersion != analysisDocsManifestMinReaderVersion {
		t.Fatalf("expected min reader default, got %+v", manifest)
	}
	if manifest.Documents[0].Path != "VERIFICATION_MATRIX.md" {
		t.Fatalf("expected legacy document path backfill, got %+v", manifest.Documents[0])
	}
	if len(manifest.Documents[0].ReuseTargets) == 0 {
		t.Fatalf("expected legacy document reuse target backfill, got %+v", manifest.Documents[0])
	}
}

func TestAnalysisDocsIncludeTrustAndDataFlowGraphSections(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{RunID: "run-graph", Goal: "map security graph", Mode: "security", Status: "completed"},
		Snapshot: ProjectSnapshot{
			Root:       "C:\\repo",
			TotalFiles: 3,
			TotalLines: 120,
			ProjectEdges: []ProjectEdge{
				{Source: "user-mode client", Target: "kernel driver", Type: "trust_boundary", Confidence: "high", Evidence: []string{"driver/dispatch.cpp"}, Attributes: map[string]string{"kind": "ioctl", "flow": "user_to_kernel"}},
				{Source: "telemetry parser", Target: "evidence store", Type: "dependency_edge", Confidence: "medium", Evidence: []string{"telemetry/parser.cpp"}, Attributes: map[string]string{"kind": "parser", "flow": "decoded_event"}},
			},
		},
		KnowledgePack: KnowledgePack{
			RunID: "run-graph",
			Goal:  "map security graph",
			Root:  "C:\\repo",
			AnalysisExecution: AnalysisExecutionSummary{
				TopChangeClasses: []string{"trust_boundary_edge_added (1)", "config_binding_added (1)"},
			},
		},
	}
	architecture := buildAnalysisArchitectureDoc(run)
	for _, want := range []string{
		"## Project Edges",
		"## Trust Boundary Graph",
		"## Data Flow Graph",
		"```mermaid",
		"user_to_kernel",
		"decoded_event",
		"_Section metadata:",
		"stale/invalidation=trust_boundary_edge_added (1)",
	} {
		if !strings.Contains(architecture, want) {
			t.Fatalf("expected architecture doc to contain %q\n%s", want, architecture)
		}
	}
	security := buildAnalysisSecuritySurfaceDoc(run)
	for _, want := range []string{
		"## Trust Boundary Graph",
		"## Attack And Data Flow View",
		"`/fuzz-campaign run`",
	} {
		if !strings.Contains(security, want) {
			t.Fatalf("expected security doc to contain %q\n%s", want, security)
		}
	}
	sections := analysisDocSections(run, "ARCHITECTURE.md")
	sectionText := []string{}
	for _, section := range sections {
		sectionText = append(sectionText, section.ID+":"+section.Title)
		if section.ID == "architecture.trust_boundary_graph" && !containsString(section.StaleMarkers, "trust_boundary_edge_added (1)") {
			t.Fatalf("expected trust boundary graph stale marker in %+v", section)
		}
		if section.ID == "architecture.data_flow_graph" && !containsString(section.StaleMarkers, "config_binding_added (1)") {
			t.Fatalf("expected data flow graph stale marker in %+v", section)
		}
	}
	joined := strings.Join(sectionText, ",")
	for _, want := range []string{"architecture.trust_boundary_graph:Trust Boundary Graph", "architecture.data_flow_graph:Data Flow Graph"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected graph section metadata %q in %s", want, joined)
		}
	}
}

func TestAnalysisFuzzTargetsUseCampaignCoverageGapFeedback(t *testing.T) {
	root := t.TempDir()
	manifest := AnalysisDocsManifest{
		RunID: "analysis-seed",
		FuzzTargets: []AnalysisFuzzTargetCatalogEntry{
			{
				Name:             "DispatchIoctl",
				File:             "driver/dispatch.cpp",
				SymbolID:         "func:DispatchIoctl@driver/dispatch.cpp",
				SourceAnchor:     "driver/dispatch.cpp:42",
				PriorityScore:    60,
				SuggestedCommand: "/fuzz-func DispatchIoctl --file driver/dispatch.cpp",
			},
		},
	}
	if _, err := createFuzzCampaignFromWorkspace(root, "coverage feedback", manifest); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID: "run-coverage-feedback",
			Goal:  "rank fuzz targets",
			Mode:  "surface",
		},
		Snapshot: ProjectSnapshot{
			Root: root,
		},
		KnowledgePack: KnowledgePack{
			Root: root,
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{
					ID:        "func:DispatchIoctl@driver/dispatch.cpp",
					Name:      "DispatchIoctl",
					Kind:      "function",
					File:      "driver/dispatch.cpp",
					Signature: "NTSTATUS DispatchIoctl(void *buffer, size_t length)",
					StartLine: 42,
					Tags:      []string{"ioctl", "dispatch"},
				},
			},
		},
	}

	targets := analysisFuzzTargetCatalog(run)
	if len(targets) == 0 {
		t.Fatalf("expected fuzz target catalog")
	}
	if targets[0].CoverageGapScore == 0 || !containsAny(strings.Join(targets[0].CoverageFeedback, " "), "coverage gap") {
		t.Fatalf("expected coverage gap feedback on target, got %#v", targets[0])
	}
	doc := buildAnalysisFuzzTargetsDoc(run)
	if !strings.Contains(doc, "Coverage Feedback") || !strings.Contains(doc, "coverage gap from") {
		t.Fatalf("expected FUZZ_TARGETS.md to expose coverage feedback\n%s", doc)
	}
}

func TestBuildAnalysisDashboardHTMLIncludesCoreSections(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:          "run-dashboard",
			Goal:           "map driver security surface",
			Mode:           "security",
			Status:         "completed",
			TotalShards:    2,
			ApprovedShards: 2,
		},
		Snapshot: ProjectSnapshot{
			Root:            "C:\\repo",
			AnalysisMode:    "security",
			TotalFiles:      12,
			TotalLines:      3400,
			EntrypointFiles: []string{"driver/dispatch.cpp"},
			ManifestFiles:   []string{"driver/guard.vcxproj"},
			RuntimeEdges: []RuntimeEdge{
				{Source: "launcher.exe", Target: "guard.sys", Kind: "dynamic_load", Confidence: "high", Evidence: []string{"driver/guard.vcxproj"}},
			},
			ProjectEdges: []ProjectEdge{
				{Source: "user-mode client", Target: "kernel driver", Type: "trust_boundary", Confidence: "high", Evidence: []string{"driver/dispatch.cpp"}, Attributes: map[string]string{"kind": "ioctl", "flow": "user_to_kernel"}},
			},
			BuildContexts: []BuildContextRecord{
				{ID: "buildctx:driver", Name: "driver", Kind: "compile", Module: "guard", Files: []string{"driver/dispatch.cpp"}},
			},
			CompileCommands: []CompilationCommandRecord{
				{File: "driver/dispatch.cpp", Compiler: "clang-cl.exe", Source: "compile_commands.json"},
			},
		},
		Shards: []AnalysisShard{
			{ID: "driver", Name: "Driver", CacheStatus: "reused"},
			{ID: "security", Name: "Security", CacheStatus: "miss"},
		},
		KnowledgePack: KnowledgePack{
			RunID:          "run-dashboard",
			Goal:           "map driver security surface",
			AnalysisMode:   "security",
			Root:           "C:\\repo",
			ProjectSummary: "Driver dispatch owns the user/kernel boundary.",
			HighRiskFiles:  []string{"driver/dispatch.cpp"},
			ProjectEdges: []ProjectEdge{
				{Source: "telemetry parser", Target: "evidence store", Type: "security", Confidence: "medium", Evidence: []string{"telemetry/parser.cpp"}, Attributes: map[string]string{"kind": "telemetry"}},
			},
			Subsystems: []KnowledgeSubsystem{
				{
					Title:               "Driver Dispatch",
					Group:               "Security Surface",
					Responsibilities:    []string{"Validate IOCTL buffers"},
					EntryPoints:         []string{"DispatchIoctl"},
					KeyFiles:            []string{"driver/dispatch.cpp"},
					Risks:               []string{"Input size can diverge from copy size"},
					CacheStatuses:       []string{"miss"},
					InvalidationReasons: []string{"semantic_primary_changed"},
					InvalidationDiff:    []string{"security_signal_added: DispatchIoctl -> user_buffer_probe"},
					InvalidationChanges: []InvalidationChange{{Kind: "security_signal_added", Scope: "integrity_security", Owner: "DispatchIoctl", Subject: "user_buffer_probe", Source: "driver/dispatch.cpp"}},
				},
			},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{
					ID:             "func:DispatchIoctl@driver/dispatch.cpp",
					Name:           "DispatchIoctl",
					Kind:           "function",
					File:           "driver/dispatch.cpp",
					Signature:      "NTSTATUS DispatchIoctl(...)",
					StartLine:      42,
					BuildContextID: "buildctx:driver",
					Tags:           []string{"ioctl", "dispatch"},
				},
			},
		},
	}

	html := buildAnalysisDashboardHTML(run, "docs")
	for _, want := range []string{"Project Analysis Dashboard", "Structure At A Glance", "Project Summary", "Module Map", "Functional Areas", "Runtime Chain", "Driver Dispatch", "Validate IOCTL buffers", "SECURITY_SURFACE.md", "Document Portal", "Developer Docs", "developer_docs", "developer document", `data-query="developer_docs"`, "Source Anchors", "Evidence And Memory Drilldown", "Stale Section Diff", "Trust Boundary Graph", "Attack Flow View", "user_to_kernel", "launcher.exe", "security_signal_added", "DispatchIoctl", `docs/SECURITY_SURFACE.md#trust-boundary-graph`, `/fuzz-func DispatchIoctl --file &quot;driver/dispatch.cpp&quot;`, "/evidence-search kind:analysis_docs", "Verification Matrix", "Stale And Invalidation Markers"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected dashboard HTML to contain %q\n%s", want, html)
		}
	}
}

func TestAnalysisDashboardUsesQuestionLanguageAndConsistentDocsHref(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:       "run-dashboard-ko",
			Goal:        "프로젝트 구조를 분석해서 문서로 작성해",
			Mode:        "map",
			Status:      "completed",
			TotalShards: 1,
		},
		Snapshot: ProjectSnapshot{
			Root:           "C:\\repo",
			PrimaryStartup: "SampleKernelTestConsole",
			TotalFiles:     2,
			TotalLines:     100,
			SolutionProjects: []SolutionProject{
				{Name: "SampleKernel", OutputType: "driver", EntryFiles: []string{"SampleKernel/SampleKernel.cpp"}},
				{Name: "SampleKernelTestConsole", OutputType: "application", EntryFiles: []string{"SampleKernelTestConsole/SampleKernelTestConsole.cpp"}, StartupCandidate: true},
			},
		},
		KnowledgePack: KnowledgePack{
			Goal:              "프로젝트 구조를 분석해서 문서로 작성해",
			TopImportantFiles: []string{"BuildCab/SampleKernel.inf"},
			AnalysisExecution: AnalysisExecutionSummary{
				InvalidationReasons: []string{"no_previous_run"},
			},
		},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{
				{Name: "DriverEntry", Kind: "function", File: "SampleKernel/SampleKernel.cpp", Tags: []string{"driver"}},
				{Name: "DeviceIoControlIrpHandleRoutine", Kind: "function", File: "SampleKernel/SampleKernelCore.cpp", Tags: []string{"ioctl"}},
			},
		},
	}

	html := buildAnalysisDashboardHTML(run, "run-dashboard-ko_docs")
	for _, want := range []string{
		`<html lang="ko">`,
		"프로젝트 분석 대시보드",
		"구조 한눈에",
		"모듈 맵",
		"기능 영역",
		"실행 흐름",
		"생성 문서",
		"문서 포털",
		"Startup 후보",
		"SampleKernelTestConsole",
		"DriverEntry",
		"DeviceIoControlIrpHandleRoutine",
		"run-dashboard-ko_docs/INDEX.md",
		"baseline:none",
		"loaded of",
		"shown /",
		"Loading document portal",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected localized dashboard HTML to contain %q\n%s", want, html)
		}
	}
	if strings.Contains(html, `href="docs/`) {
		t.Fatalf("expected all dashboard doc links to use supplied docsHref\n%s", html)
	}
}

func TestAnalysisDashboardHonorsExplicitEnglishLanguageRequest(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:  "run-dashboard-explicit-en",
			Goal:   "영어로 답변하고 문서를 생성해. 프로젝트 구조를 분석해.",
			Mode:   "map",
			Status: "completed",
		},
		Snapshot: ProjectSnapshot{
			Root:           "C:\\repo",
			PrimaryStartup: "SampleApp",
			TotalFiles:     2,
			TotalLines:     100,
		},
		KnowledgePack: KnowledgePack{
			Goal:           "영어로 답변하고 문서를 생성해. 프로젝트 구조를 분석해.",
			ProjectSummary: "SampleApp is the startup application.",
		},
	}

	html := buildAnalysisDashboardHTML(run, "docs")
	for _, want := range []string{
		`<html lang="en">`,
		"Project Analysis Dashboard",
		"Structure At A Glance",
		"Module Map",
		"Functional Areas",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected explicit English dashboard HTML to contain %q\n%s", want, html)
		}
	}
	for _, unwanted := range []string{
		`<html lang="ko">`,
		"프로젝트 분석 대시보드",
		"구조 한눈에",
		"모듈 맵",
	} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("expected explicit English dashboard HTML to avoid %q\n%s", unwanted, html)
		}
	}
}

func TestAnalysisDashboardPortalJSONIsScriptSafe(t *testing.T) {
	item := analysisDashboardNewPortalItem(
		"document",
		`bad </script><script>alert("x")</script>`,
		"detail",
		"source.cpp",
		"docs/INDEX.md",
		[]string{`reuse </script>`},
	)

	got := analysisDashboardPortalJSON([]analysisDashboardPortalItem{item})
	if strings.Contains(strings.ToLower(got), "</script>") {
		t.Fatalf("expected portal JSON to be safe inside a script tag, got %s", got)
	}
	var decoded []analysisDashboardPortalItem
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("expected valid JSON, got %v from %s", err, got)
	}
	if len(decoded) != 1 || decoded[0].Title != item.Title {
		t.Fatalf("expected portal item roundtrip, got %#v", decoded)
	}
}

func TestAnalysisDashboardUsesInlineMarkdownViewerForDocLinks(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:  "run-dashboard-viewer",
			Goal:   "map project docs",
			Mode:   "map",
			Status: "completed",
		},
		FinalDocument: "# SampleApp Client Project Map\n\nSolution startup candidate:\n- `SampleApp`\n\nAuxiliary executable projects:\n- `SampleAppControl`\n",
		Snapshot: ProjectSnapshot{
			Root:       "C:\\repo",
			TotalFiles: 2,
			TotalLines: 80,
		},
		KnowledgePack: KnowledgePack{
			RunID:          "run-dashboard-viewer",
			Goal:           "map project docs",
			Root:           "C:\\repo",
			ProjectSummary: "Runtime dispatch and verification docs are linked from the dashboard.",
		},
	}

	html := buildAnalysisDashboardHTML(run, "docs")
	for _, want := range []string{
		`id="markdown-viewer"`,
		`const markdownDocs = `,
		`data-doc-href="docs/INDEX.md"`,
		`data-doc-href="docs/FINAL_REPORT.md"`,
		`data-doc-href="docs/VERIFICATION_MATRIX.md"`,
		`document.addEventListener('click'`,
		`openMarkdownDoc(href)`,
		`portalItemSearchText`,
		`sanitizeMarkdownHref`,
		`id="reader-toggle"`,
		`setReaderMode`,
		`body.reader-mode`,
		`SampleAppControl`,
		`# Project Documentation Index`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected dashboard markdown viewer HTML to contain %q\n%s", want, html)
		}
	}
	if strings.Contains(html, `target="_blank"`) {
		t.Fatalf("expected dashboard document links to stay in the inline viewer\n%s", html)
	}
}

func TestAnalysisDashboardUsesDarkThemeUXTokens(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:  "run-dashboard-dark",
			Goal:   "map project docs",
			Mode:   "security",
			Status: "completed",
		},
		Snapshot: ProjectSnapshot{
			Root:       "C:\\repo",
			TotalFiles: 3,
			TotalLines: 300,
		},
		KnowledgePack: KnowledgePack{
			RunID: "run-dashboard-dark",
			Goal:  "map project docs",
			Root:  "C:\\repo",
		},
	}

	html := buildAnalysisDashboardHTML(run, "docs")
	for _, want := range []string{
		`color-scheme: dark;`,
		`--bg: #030407;`,
		`--accent: #7aa2ff;`,
		`position: sticky;`,
		`backdrop-filter: blur(18px);`,
		`document-workspace`,
		`.doc-link.active-doc`,
		`.portal-filter.active`,
		`.reader-toggle`,
		`body.reader-mode .markdown-viewer-panel`,
		`background: linear-gradient(180deg, #0d0f15, #07080c);`,
		`::-webkit-scrollbar-thumb`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected dark dashboard UX token %q\n%s", want, html)
		}
	}
	for _, oldLightTheme := range []string{
		`--bg: #f5f7fb;`,
		`--panel: #ffffff;`,
		`background: #fbfcfe;`,
		`--accent: #35d6b7;`,
		`rgba(53, 214, 183`,
		`#c9fff2`,
		`#c8fff3`,
	} {
		if strings.Contains(html, oldLightTheme) {
			t.Fatalf("expected old or green dashboard token %q to be removed\n%s", oldLightTheme, html)
		}
	}
}

func TestAnalysisDashboardStructureHelpersPreserveUTF8AndLocalizedFallbacks(t *testing.T) {
	input := strings.Repeat("프로젝트구조", 12)
	got := analysisDashboardBriefText(input, 11)
	if !utf8.ValidString(got) {
		t.Fatalf("expected UTF-8-safe dashboard summary, got %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated text to keep ellipsis, got %q", got)
	}

	card := analysisDashboardCardLine("의존성", "", "없음")
	if !strings.Contains(card, ">없음<") {
		t.Fatalf("expected localized fallback in card line\n%s", card)
	}
	if strings.Contains(card, ">none<") {
		t.Fatalf("expected Korean card fallback not to render English none\n%s", card)
	}
}

func TestAnalysisDashboardMarkdownDocsJSONIsScriptSafe(t *testing.T) {
	payload := `bad </script><script>alert("x")</script>`
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:  "run-dashboard-script-safe",
			Goal:   payload,
			Mode:   "map",
			Status: "completed",
		},
		FinalDocument: "# Final Report\n\n" + payload + "\n",
		Snapshot: ProjectSnapshot{
			Root:       "C:\\repo",
			TotalFiles: 1,
			TotalLines: 10,
		},
		KnowledgePack: KnowledgePack{
			RunID:          "run-dashboard-script-safe",
			Goal:           payload,
			Root:           "C:\\repo",
			ProjectSummary: payload,
		},
	}

	got := analysisDashboardDocsJSON(run, "docs")
	if strings.Contains(strings.ToLower(got), "</script>") {
		t.Fatalf("expected markdown docs JSON to be safe inside a script tag, got %s", got)
	}
	if !strings.Contains(got, `\u003c/script\u003e`) {
		t.Fatalf("expected markdown docs JSON to HTML-escape script delimiters, got %s", got)
	}
	var decoded []analysisDashboardDocContent
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("expected valid markdown docs JSON, got %v from %s", err, got)
	}
	foundPayload := false
	for _, doc := range decoded {
		if strings.Contains(doc.Markdown, payload) {
			foundPayload = true
			break
		}
	}
	if !foundPayload {
		t.Fatalf("expected markdown docs JSON to preserve document content after decode, got %#v", decoded)
	}

	html := buildAnalysisDashboardHTML(run, "docs")
	if strings.Contains(strings.ToLower(html), `</script><script>alert`) {
		t.Fatalf("expected dashboard HTML to keep embedded markdown script-safe\n%s", html)
	}
}

func TestAnalysisDashboardStaleDiffLinksGraphSections(t *testing.T) {
	run := ProjectAnalysisRun{
		Summary:  ProjectAnalysisSummary{RunID: "run-stale-graph", Goal: "map graph diffs", Mode: "security", Status: "completed"},
		Snapshot: ProjectSnapshot{Root: "C:\\repo", TotalFiles: 2, TotalLines: 80},
		KnowledgePack: KnowledgePack{
			RunID: "run-stale-graph",
			Goal:  "map graph diffs",
			Root:  "C:\\repo",
			Subsystems: []KnowledgeSubsystem{
				{
					Title: "Driver Dispatch",
					Group: "Security Surface",
					InvalidationChanges: []InvalidationChange{
						{Kind: "trust_boundary_edge_added", Scope: "integrity_security", Owner: "user-mode client", Subject: "kernel driver", After: "ioctl", Source: "driver/dispatch.cpp"},
					},
				},
				{
					Title: "Runtime Config",
					Group: "Architecture",
					InvalidationChanges: []InvalidationChange{
						{Kind: "config_binding_added", Scope: "runtime_config", Owner: "config loader", Subject: "GuardProfile", Source: "config/guard.ini"},
					},
				},
			},
		},
	}
	html := buildAnalysisDashboardHTML(run, "docs")
	for _, want := range []string{
		`docs/SECURITY_SURFACE.md#trust-boundary-graph`,
		`SECURITY_SURFACE.md / Trust Boundary Graph`,
		`docs/ARCHITECTURE.md#data-flow-graph`,
		`ARCHITECTURE.md / Data Flow Graph`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected stale diff graph link %q\n%s", want, html)
		}
	}
}

func TestWriteAnalysisDashboardPersistsHTML(t *testing.T) {
	dir := t.TempDir()
	run := ProjectAnalysisRun{
		Summary:       ProjectAnalysisSummary{RunID: "run-dashboard", Goal: "map runtime", Mode: "map", Status: "completed"},
		Snapshot:      ProjectSnapshot{Root: dir, TotalFiles: 1, TotalLines: 10},
		KnowledgePack: KnowledgePack{RunID: "run-dashboard", Goal: "map runtime", Root: dir},
	}
	outputPath := filepath.Join(dir, "dashboard.html")
	if err := writeAnalysisDashboard(run, outputPath, "docs"); err != nil {
		t.Fatalf("writeAnalysisDashboard returned error: %v", err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected dashboard on disk: %v", err)
	}
	if !strings.Contains(string(data), "Project Analysis Dashboard") {
		t.Fatalf("expected dashboard title in persisted HTML")
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

func TestAnnotateAnalysisShardAddsContractAndNormalizesClaims(t *testing.T) {
	snapshot := ProjectSnapshot{
		AnalysisMode: "security",
		FilesByPath: map[string]ScannedFile{
			"driver.cpp": {Path: "driver.cpp", LineCount: 40, IsEntrypoint: true},
		},
	}
	shards := annotateAnalysisShards(snapshot, []AnalysisShard{
		{
			ID:             "shard-01",
			Name:           "security_driver",
			PrimaryFiles:   []string{"driver.cpp"},
			EstimatedFiles: 1,
			EstimatedLines: 40,
		},
	}, "analyze security boundaries")
	if len(shards) != 1 {
		t.Fatalf("expected one shard")
	}
	if shards[0].Type != "security_surface" {
		t.Fatalf("expected security_surface type, got %q", shards[0].Type)
	}
	if len(shards[0].RequiredEvidence) == 0 || len(shards[0].SuccessCriteria) == 0 {
		t.Fatalf("expected shard contract evidence and criteria: %+v", shards[0])
	}

	report := WorkerReport{
		Facts:         []string{"driver.cpp validates the privileged command path."},
		Inferences:    []string{"The shard owns a trust-boundary decision."},
		EvidenceFiles: []string{"driver.cpp"},
	}
	normalizeWorkerReport(&report, shards[0])
	if len(report.Claims) == 0 {
		t.Fatalf("expected derived claims")
	}
	if got := report.Claims[0].SourceAnchors; len(got) == 0 || got[0] != "driver.cpp" {
		t.Fatalf("expected claim source anchor to stay inside shard scope, got %+v", got)
	}

	report = WorkerReport{
		Claims: []AnalysisClaim{
			{
				Claim:         "Out-of-scope claim must not be silently re-anchored.",
				SourceAnchors: []string{"other.cpp"},
				Confidence:    "high",
			},
		},
		EvidenceFiles: []string{"driver.cpp"},
	}
	normalizeWorkerReport(&report, shards[0])
	if len(report.Claims) != 1 {
		t.Fatalf("expected one explicit claim")
	}
	if len(report.Claims[0].SourceAnchors) != 0 {
		t.Fatalf("expected out-of-scope explicit claim anchors to stay unsupported, got %+v", report.Claims[0].SourceAnchors)
	}
	if analysisReportHasSupportedClaims(report) {
		t.Fatalf("expected unsupported explicit claim to fail claim coverage")
	}
}

func TestBuildAnalysisModeScorecardPlansCoverageGapShard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := ProjectSnapshot{
		Root:         root,
		AnalysisMode: "trace",
		FilesByPath: map[string]ScannedFile{
			"main.go": {Path: "main.go", LineCount: 2, IsEntrypoint: true},
		},
	}
	shard := AnalysisShard{
		ID:             "shard-01",
		Name:           "runtime",
		PrimaryFiles:   []string{"main.go"},
		EstimatedFiles: 1,
		EstimatedLines: 2,
	}
	annotateAnalysisShardContract(snapshot, &shard, "trace runtime")
	report := WorkerReport{
		ShardID:          shard.ID,
		Title:            "runtime",
		ScopeSummary:     "runtime",
		Responsibilities: []string{"Owns runtime startup."},
		EvidenceFiles:    []string{"main.go"},
		KeyFiles:         []string{"main.go"},
	}
	normalizeWorkerReport(&report, shard)
	review := ReviewDecision{Status: "approved", ClaimCoverageStatus: "supported"}
	scorecard := buildAnalysisModeScorecard(snapshot, []AnalysisShard{shard}, []WorkerReport{report}, []ReviewDecision{review}, "trace runtime", "trace")
	if len(scorecard.CoverageGaps) == 0 {
		t.Fatalf("expected a mode evidence gap")
	}
	analyzer := &projectAnalyzer{
		analysisCfg: defaultProjectAnalysisConfig(root),
		workspace:   Workspace{Root: root, BaseRoot: root},
	}
	gapShards := analyzer.planCoverageGapShards(snapshot, []AnalysisShard{shard}, []WorkerReport{report}, []ReviewDecision{review}, scorecard, 2)
	if len(gapShards) == 0 {
		t.Fatalf("expected gap-filling shard")
	}
	if gapShards[0].Type != "gap_filling" || gapShards[0].CoverageGapID == "" {
		t.Fatalf("expected gap shard contract, got %+v", gapShards[0])
	}

	providerFailedReview := ReviewDecision{Status: "review_failed", FailureKind: analysisReviewIssueProvider}
	scorecard = buildAnalysisModeScorecard(snapshot, []AnalysisShard{shard}, []WorkerReport{report}, []ReviewDecision{providerFailedReview}, "trace runtime", "trace")
	gapShards = analyzer.planCoverageGapShards(snapshot, []AnalysisShard{shard}, []WorkerReport{report}, []ReviewDecision{providerFailedReview}, scorecard, 2)
	if len(gapShards) != 0 {
		t.Fatalf("expected provider-failed shard to be skipped by gap filler, got %+v", gapShards)
	}
}

func TestRuntimeFeedbackAdjustsShardPlanner(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 2; i++ {
		err := appendRuntimeErrorLogEntry(root, RuntimeErrorLogEntry{
			Time:     time.Now().UTC(),
			Kind:     conversationEventKindProviderError,
			Severity: conversationSeverityError,
			Summary:  "analysis provider error: timeout while calling model",
			Raw:      "context deadline exceeded",
			Entities: map[string]string{
				"model":   "qwen/qwen3.6-27b",
				"attempt": fmt.Sprintf("%d", i+1),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := appendRuntimeErrorLogEntry(root, RuntimeErrorLogEntry{
		Time:     time.Now().Add(-48 * time.Hour).UTC(),
		Kind:     conversationEventKindProviderError,
		Severity: conversationSeverityError,
		Summary:  "analysis provider error: old timeout should not affect planner",
		Raw:      "context deadline exceeded",
		Entities: map[string]string{
			"model": "qwen/qwen3.6-27b",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := appendRuntimeErrorLogEntry(root, RuntimeErrorLogEntry{
		Time:     time.Now().UTC(),
		Kind:     conversationEventKindProviderError,
		Severity: conversationSeverityError,
		Summary:  "analysis provider error: different model timeout should not affect planner",
		Raw:      "context deadline exceeded",
		Entities: map[string]string{
			"model": "other/model",
		},
	}); err != nil {
		t.Fatal(err)
	}
	feedback := analysisRuntimeFeedbackFromLog(root, "qwen/qwen3.6-27b")
	if feedback.RecentTimeoutCount != 2 {
		t.Fatalf("expected timeout feedback, got %+v", feedback)
	}
	analyzer := &projectAnalyzer{analysisCfg: defaultProjectAnalysisConfig(root)}
	beforeLines := analyzer.analysisCfg.MaxLinesPerShard
	notes := analyzer.applyRuntimeFeedbackToAnalysisConfig(feedback)
	if len(notes) == 0 {
		t.Fatalf("expected planner adjustment")
	}
	if analyzer.analysisCfg.MaxLinesPerShard >= beforeLines {
		t.Fatalf("expected smaller shard line limit, before=%d after=%d", beforeLines, analyzer.analysisCfg.MaxLinesPerShard)
	}
}

func TestRefineSemanticInvalidationClassifiesSecurityContract(t *testing.T) {
	shard := AnalysisShard{
		ID:                 "shard-01",
		Name:               "security_ioctl",
		PrimaryFiles:       []string{"driver/ioctl.cpp"},
		InvalidationReason: "semantic_primary_changed",
		InvalidationChanges: []InvalidationChange{
			{Kind: "validation_rule_changed", Subject: "IOCTL command validation", Source: "driver/ioctl.cpp"},
		},
	}
	refineSemanticInvalidation(&shard)
	if shard.InvalidationReason != "semantic_security_contract_changed" {
		t.Fatalf("expected refined semantic security reason, got %+v", shard)
	}
	if shard.InvalidationClass != "security_contract" {
		t.Fatalf("expected security contract class, got %q", shard.InvalidationClass)
	}
}

func TestPersistRunWritesPreflightAndModeScorecard(t *testing.T) {
	root := t.TempDir()
	statuses := []string{}
	analyzer := &projectAnalyzer{
		analysisCfg: defaultProjectAnalysisConfig(root),
		onStatus: func(status string) {
			statuses = append(statuses, status)
		},
	}
	analyzer.analysisCfg.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:       "20260506-010203",
			Goal:        "map project",
			Mode:        "map",
			Status:      "completed",
			StartedAt:   time.Now(),
			TotalShards: 1,
		},
		Preflight: AnalysisPreflight{
			GeneratedAt:      time.Now(),
			Intent:           "map project",
			EffectiveMode:    "map",
			RecommendedDepth: "standard",
		},
		Snapshot: ProjectSnapshot{
			Root:        root,
			GeneratedAt: time.Now(),
			FilesByPath: map[string]ScannedFile{},
		},
		Shards: []AnalysisShard{{ID: "shard-01", Name: "core", PrimaryFiles: []string{"main.go"}}},
		Reports: []WorkerReport{{
			ShardID:          "shard-01",
			Title:            "core",
			ScopeSummary:     "core",
			Responsibilities: []string{"core"},
			EvidenceFiles:    []string{"main.go"},
		}},
		Reviews:       []ReviewDecision{{Status: "approved"}},
		ModeScorecard: AnalysisModeScorecard{GeneratedAt: time.Now(), Mode: "map", Status: "pass", Score: 100},
		FinalDocument: "# Project Analysis\n",
	}
	output, err := analyzer.persistRun(run)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(filepath.Base(output), ".md")
	for _, path := range []string{
		filepath.Join(analyzer.analysisCfg.OutputDir, base+"_analysis_preflight.json"),
		filepath.Join(analyzer.analysisCfg.OutputDir, base+"_mode_scorecard.json"),
		filepath.Join(analyzer.analysisCfg.OutputDir, "latest", "analysis_preflight.json"),
		filepath.Join(analyzer.analysisCfg.OutputDir, "latest", "mode_scorecard.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", path, err)
		}
	}
	joinedStatuses := strings.Join(statuses, "\n")
	for _, expected := range []string{
		"Writing primary analysis report",
		"Writing structured analysis JSON artifacts",
		"Writing analysis docs and dashboard",
		"Refreshing latest analysis artifact set",
	} {
		if !strings.Contains(joinedStatuses, expected) {
			t.Fatalf("expected persist status %q\n%s", expected, joinedStatuses)
		}
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
	if run.Summary.ReviewQualityIssues == 0 {
		t.Fatalf("expected draft review quality issues to be tracked")
	}
	if !strings.Contains(run.FinalDocument, "Reviewer quality issues:") {
		t.Fatalf("expected draft review quality details\n%s", run.FinalDocument)
	}
}

func TestRenderAnalysisReviewIssueBannerSeparatesProviderAndQuality(t *testing.T) {
	providerOnly := renderAnalysisReviewIssueBanner(ProjectAnalysisSummary{
		ReviewFailures:         2,
		ReviewProviderFailures: 2,
	})
	if !strings.Contains(providerOnly, "Analysis With Provider Failures") || !strings.Contains(providerOnly, "Provider failures: 2") {
		t.Fatalf("expected provider-specific banner, got:\n%s", providerOnly)
	}
	if strings.Contains(providerOnly, "Reviewer quality issues:") {
		t.Fatalf("provider-only banner should not include quality issue text:\n%s", providerOnly)
	}

	qualityOnly := renderAnalysisReviewIssueBanner(ProjectAnalysisSummary{
		ReviewFailures:      1,
		ReviewQualityIssues: 1,
	})
	if !strings.Contains(qualityOnly, "Analysis With Reviewer Quality Issues") || !strings.Contains(qualityOnly, "Reviewer quality issues: 1") {
		t.Fatalf("expected quality-specific banner, got:\n%s", qualityOnly)
	}

	mixed := renderAnalysisReviewIssueBanner(ProjectAnalysisSummary{
		ReviewFailures:         3,
		ReviewProviderFailures: 2,
		ReviewQualityIssues:    1,
	})
	for _, needle := range []string{
		"Analysis With Review Issues",
		"Provider failures: 2",
		"Reviewer quality issues: 1",
	} {
		if !strings.Contains(mixed, needle) {
			t.Fatalf("expected mixed banner to include %q, got:\n%s", needle, mixed)
		}
	}
}

func TestRenderAnalysisReviewIssueDetailsSplitsWorkerProviderFailures(t *testing.T) {
	details := renderAnalysisReviewIssueDetailsForReviews(1, 0, []ReviewDecision{{
		Status:      "review_failed",
		FailureKind: analysisReviewIssueProvider,
		Issues: []string{
			"Worker request failed: analysis worker request failed for shard=core model=opencode/gpt-5.4-mini: opencode API error (401 Unauthorized): Insufficient balance | request={large}",
		},
		Raw: "analysis worker request failed for shard=core model=opencode/gpt-5.4-mini: opencode API error (401 Unauthorized): Insufficient balance | request={large}",
	}})
	for _, needle := range []string{
		"Provider failures: 1",
		"Provider failure split: worker=1.",
		"opencode API error (401 Unauthorized): Insufficient balance",
	} {
		if !strings.Contains(details, needle) {
			t.Fatalf("expected details to include %q, got:\n%s", needle, details)
		}
	}
	if strings.Contains(details, "request={") {
		t.Fatalf("provider failure example should omit request payload:\n%s", details)
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
		PrimaryStartup:  "SampleApp",
		StartupProjects: []string{"SampleApp", "SampleWorker", "SampleAppDart"},
		SolutionProjects: []SolutionProject{
			{Name: "SampleApp", EntryFiles: []string{"SampleApp/SampleApp.cpp"}},
		},
	}
	got := ensureFinalDocumentInsights(document, snapshot, nil, nil)
	if !strings.Contains(got, "Solution startup candidate:") {
		t.Fatalf("expected startup project snippet\n%s", got)
	}
	if !strings.Contains(got, "`SampleApp`") {
		t.Fatalf("expected SampleApp in startup snippet\n%s", got)
	}
	if !strings.Contains(got, "`SampleApp/SampleApp.cpp`") {
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
		ManifestFiles: []string{"SampleApp.sln", "SampleApp/SampleApp.vcxproj"},
	}
	if !isVisualStudioCppProject(snapshot) {
		t.Fatalf("expected Visual Studio / C++ project detection")
	}
}

func TestEnrichSolutionMetadataFindsStartupProjectAndEntryFiles(t *testing.T) {
	root := t.TempDir()
	sln := `Microsoft Visual Studio Solution File, Format Version 12.00
Project("{GUID}") = "SampleApp", "SampleApp/SampleApp.vcxproj", "{A}"
Project("{GUID}") = "Common", "Common/Common.vcxproj", "{B}"
EndProject
`
	vcxprojApp := `<Project><PropertyGroup Label="Configuration"><ConfigurationType>Application</ConfigurationType></PropertyGroup></Project>`
	vcxprojLib := `<Project><PropertyGroup Label="Configuration"><ConfigurationType>DynamicLibrary</ConfigurationType></PropertyGroup></Project>`
	if err := os.MkdirAll(filepath.Join(root, "SampleApp"), 0o755); err != nil {
		t.Fatalf("mkdir SampleApp: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Common"), 0o755); err != nil {
		t.Fatalf("mkdir Common: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "SampleApp.sln"), []byte(sln), 0o644); err != nil {
		t.Fatalf("write sln: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "SampleApp", "SampleApp.vcxproj"), []byte(vcxprojApp), 0o644); err != nil {
		t.Fatalf("write app vcxproj: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Common", "Common.vcxproj"), []byte(vcxprojLib), 0o644); err != nil {
		t.Fatalf("write lib vcxproj: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "SampleApp", "main.cpp"), []byte("int main() { return 0; }\n"), 0o644); err != nil {
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
	if snapshot.PrimaryStartup != "SampleApp" {
		t.Fatalf("expected SampleApp as primary startup, got %q", snapshot.PrimaryStartup)
	}
	if len(snapshot.SolutionProjects) != 2 {
		t.Fatalf("expected solution projects to be discovered, got %d", len(snapshot.SolutionProjects))
	}
	if !containsString(snapshot.StartupProjects, "SampleApp") {
		t.Fatalf("expected SampleApp in startup candidates: %#v", snapshot.StartupProjects)
	}
	if !containsString(snapshot.EntrypointFiles, "SampleApp/main.cpp") {
		t.Fatalf("expected solution entrypoint file in snapshot: %#v", snapshot.EntrypointFiles)
	}
}

func TestEnrichSolutionMetadataIgnoresSolutionProjectsOutsideAnalysisRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "Scoped")
	sibling := filepath.Join(parent, "AOutside")
	if err := os.MkdirAll(filepath.Join(root, "Local"), 0o755); err != nil {
		t.Fatalf("mkdir Local: %v", err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}
	sln := `Microsoft Visual Studio Solution File, Format Version 12.00
Project("{GUID}") = "AOutside", "..\AOutside\AOutside.vcxproj", "{A}"
Project("{GUID}") = "Local", "Local\Local.vcxproj", "{B}"
EndProject
`
	vcxprojApp := `<Project><PropertyGroup Label="Configuration"><ConfigurationType>Application</ConfigurationType></PropertyGroup></Project>`
	if err := os.WriteFile(filepath.Join(root, "Scoped.sln"), []byte(sln), 0o644); err != nil {
		t.Fatalf("write sln: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Local", "Local.vcxproj"), []byte(vcxprojApp), 0o644); err != nil {
		t.Fatalf("write local vcxproj: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Local", "main.cpp"), []byte("int main() { return 0; }\n"), 0o644); err != nil {
		t.Fatalf("write local main: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "AOutside.vcxproj"), []byte(vcxprojApp), 0o644); err != nil {
		t.Fatalf("write outside vcxproj: %v", err)
	}

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: parent, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	if snapshot.PrimaryStartup != "Local" {
		t.Fatalf("expected Local as primary startup, got %q with projects %#v", snapshot.PrimaryStartup, snapshot.SolutionProjects)
	}
	if len(snapshot.SolutionProjects) != 1 || snapshot.SolutionProjects[0].Name != "Local" {
		t.Fatalf("expected only in-root solution project, got %#v", snapshot.SolutionProjects)
	}
	for _, project := range snapshot.SolutionProjects {
		if strings.Contains(project.Path, "..") || strings.Contains(project.Path, "AOutside") {
			t.Fatalf("expected outside project to be ignored, got %#v", snapshot.SolutionProjects)
		}
	}
}

func TestFallbackFinalDocumentIncludesPrimaryStartupProject(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:           "C:\\repo",
		PrimaryStartup: "SampleApp",
		TotalFiles:     1,
		TotalLines:     10,
	}
	doc := fallbackFinalDocument(snapshot, nil, nil, "goal")
	if !strings.Contains(doc, "Solution startup candidate: `SampleApp`") {
		t.Fatalf("expected fallback doc to include primary startup project\n%s", doc)
	}
}

func TestBuildWorkerAndSynthesisPromptsFollowKoreanGoalLanguage(t *testing.T) {
	snapshot := ProjectSnapshot{Root: "C:\\repo"}
	shard := AnalysisShard{ID: "shard-01", Name: "core", PrimaryFiles: []string{"core.cpp"}}
	worker := buildWorkerPrompt(snapshot, shard, "프로젝트 구조를 분석해서 문서로 작성해", "")
	if !strings.Contains(worker, "Response language: Korean") {
		t.Fatalf("expected Korean worker language guidance\n%s", worker)
	}
	report := WorkerReport{
		ShardID:          "shard-01",
		Title:            "Core",
		ScopeSummary:     "summary",
		Responsibilities: []string{"owns core"},
		EvidenceFiles:    []string{"core.cpp"},
	}
	synthesis := buildSynthesisPrompt(snapshot, []AnalysisShard{shard}, []WorkerReport{report}, "프로젝트 구조를 분석해서 문서로 작성해")
	if !strings.Contains(synthesis, "final Markdown document in Korean") {
		t.Fatalf("expected Korean synthesis language guidance\n%s", synthesis)
	}
}

func TestBuildWorkerAndSynthesisPromptsHonorExplicitEnglishInKoreanGoal(t *testing.T) {
	snapshot := ProjectSnapshot{Root: "C:\\repo"}
	shard := AnalysisShard{ID: "shard-01", Name: "core", PrimaryFiles: []string{"core.cpp"}}
	goal := "영어로 답변하고 문서를 생성해. 프로젝트 구조를 분석해."
	worker := buildWorkerPrompt(snapshot, shard, goal, "")
	if !strings.Contains(worker, "Response language: English") {
		t.Fatalf("expected explicit English worker language guidance\n%s", worker)
	}
	if strings.Contains(worker, "Response language: Korean") {
		t.Fatalf("expected explicit English to override Korean goal text\n%s", worker)
	}
	report := WorkerReport{
		ShardID:          "shard-01",
		Title:            "Core",
		ScopeSummary:     "summary",
		Responsibilities: []string{"owns core"},
		EvidenceFiles:    []string{"core.cpp"},
	}
	synthesis := buildSynthesisPrompt(snapshot, []AnalysisShard{shard}, []WorkerReport{report}, goal)
	if !strings.Contains(synthesis, "final Markdown document in English") {
		t.Fatalf("expected explicit English synthesis language guidance\n%s", synthesis)
	}
	if strings.Contains(synthesis, "final Markdown document in Korean") {
		t.Fatalf("expected explicit English synthesis guidance to avoid Korean output\n%s", synthesis)
	}
}

func TestDriverPromptsSeparateInitializationAndRuntimeRegistration(t *testing.T) {
	snapshot := ProjectSnapshot{Root: "C:\\repo"}
	shard := AnalysisShard{ID: "shard-driver", Name: "security_driver", PrimaryFiles: []string{"driver/Driver.cpp"}}
	worker := buildWorkerPrompt(snapshot, shard, "map driver structure", "")
	if !strings.Contains(worker, "load/state-initialization flow") || !strings.Contains(worker, "runtime callback/filter registration flow") {
		t.Fatalf("expected driver worker prompt to separate initialization and runtime registration\n%s", worker)
	}
	system := synthesisSystemPrompt()
	if !strings.Contains(system, "keep initialization/state setup separate from runtime callback/filter registration") {
		t.Fatalf("expected synthesis prompt to guard driver initialization/register flow\n%s", system)
	}
	if !strings.Contains(system, "Do not place request-origin/open validation inside the DeviceIoControl command handler") {
		t.Fatalf("expected synthesis prompt to separate request-origin validation from DeviceIoControl command handling\n%s", system)
	}
	if !strings.Contains(system, "describe that as a subsystem instead of labeling the entire project as only a minifilter driver") {
		t.Fatalf("expected synthesis prompt to avoid over-classifying WDM drivers as minifilter-only\n%s", system)
	}
	ioctlWorker := buildWorkerPrompt(snapshot, AnalysisShard{ID: "shard-ioctl", Name: "security_ioctl", PrimaryFiles: []string{"driver/Ioctl.cpp"}}, "map driver ioctl flow", "")
	if !strings.Contains(ioctlWorker, "Keep create/open request-origin validation separate") {
		t.Fatalf("expected IOCTL worker prompt to separate create/open validation\n%s", ioctlWorker)
	}
}

func TestSynthesisPromptIncludesClosedTopLevelAndDriverFacts(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:           "C:\\repo",
		PrimaryStartup: "DriverConsole",
		Directories:    []string{"Driver", "Common", "DriverConsole", "BuildCab"},
		Files: []ScannedFile{
			{Path: "Driver/DriverEntry.cpp", Directory: "Driver", IsEntrypoint: true},
			{Path: "Common/UserCommon.h", Directory: "Common"},
			{Path: "DriverConsole/main.cpp", Directory: "DriverConsole", IsEntrypoint: true},
		},
		SolutionProjects: []SolutionProject{
			{Name: "Driver", Path: "Driver/Driver.vcxproj", Directory: "Driver", OutputType: "driver", EntryFiles: []string{"Driver/DriverEntry.cpp"}},
			{Name: "DriverConsole", Path: "DriverConsole/DriverConsole.vcxproj", Directory: "DriverConsole", OutputType: "application", EntryFiles: []string{"DriverConsole/main.cpp"}, StartupCandidate: true},
		},
	}
	report := WorkerReport{ShardID: "driver", Title: "Driver", ScopeSummary: "summary", Responsibilities: []string{"driver"}, EvidenceFiles: []string{"Driver/DriverEntry.cpp"}}
	prompt := buildSynthesisPrompt(snapshot, []AnalysisShard{{ID: "driver", Name: "security_driver"}}, []WorkerReport{report}, "map")
	for _, needle := range []string{
		"Top-level directory facts:",
		"Closed set for top-level directory maps: BuildCab/, Common/, Driver/, DriverConsole/",
		"Do not list headers, source files, project files, INF files, or nested folders as top-level directories.",
		"Driver architecture facts:",
		"Solution startup candidate is DriverConsole",
		"Kernel/runtime driver entry files: Driver/DriverEntry.cpp",
		"Keep these separate from user-mode startup files.",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected synthesis prompt to include %q\n%s", needle, prompt)
		}
	}
}

func TestSynthesisPromptIncludesWorkerEvidenceGuardrails(t *testing.T) {
	snapshot := ProjectSnapshot{Root: "C:\\repo"}
	reports := []WorkerReport{
		{
			Title:        "Startup",
			ScopeSummary: "main() calls CreateService; the manager also declares public methods for StartService and DeviceIoControl.",
			Facts:        []string{"main() visibly calls CreateService only.", "Declared public methods include StartService and ControlOperation."},
		},
		{
			Title:        "Object Filter",
			ScopeSummary: "GuardObjectFilter::Initialize and StartObjectFilter are present.",
			Facts:        []string{"GuardObjectFilter::Initialize sets state.", "StartObjectFilter calls ObRegisterCallbacks."},
		},
	}
	prompt := buildSynthesisPrompt(snapshot, nil, reports, "map")
	for _, needle := range []string{
		"Synthesis guardrails from worker evidence:",
		"declared public methods and available lifecycle operations belong in an Available operations/API section",
		"Object/handle filter guardrail:",
		"Initialize as state setup and Start/Register/ObRegisterCallbacks as callback registration",
	} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("expected synthesis prompt to include %q\n%s", needle, prompt)
		}
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
	mustWrite(filepath.Join(root, "SampleApp", "SampleApp.vcxproj"), `<Project><PropertyGroup Label="Configuration"><ConfigurationType>Application</ConfigurationType></PropertyGroup><ItemGroup><ProjectReference Include="..\SampleMaster\SampleMaster.vcxproj" /></ItemGroup></Project>`)
	mustWrite(filepath.Join(root, "SampleMaster", "SampleMaster.vcxproj"), `<Project><PropertyGroup Label="Configuration"><ConfigurationType>DynamicLibrary</ConfigurationType></PropertyGroup><ItemGroup><ProjectReference Include="..\SampleWorker\SampleWorker.vcxproj" /></ItemGroup></Project>`)
	mustWrite(filepath.Join(root, "SampleAppCmn", "SampleAppCmn.vcxproj"), `<Project><PropertyGroup Label="Configuration"><ConfigurationType>DynamicLibrary</ConfigurationType></PropertyGroup></Project>`)
	mustWrite(filepath.Join(root, "SampleUpd", "SampleUpd.vcxproj"), `<Project><PropertyGroup Label="Configuration"><ConfigurationType>DynamicLibrary</ConfigurationType></PropertyGroup></Project>`)
	mustWrite(filepath.Join(root, "SampleAppCmn", "dllmain.cpp"), `LoadLibrary(L"SampleUpd.bin");`)
	snapshot := ProjectSnapshot{
		Root: root,
		Files: []ScannedFile{
			{Path: "SampleAppCmn/dllmain.cpp"},
		},
		SolutionProjects: []SolutionProject{
			{Name: "SampleApp", Path: "SampleApp/SampleApp.vcxproj", Directory: "SampleApp", ProjectReferences: parseVCXProjProjectReferences(root, "SampleApp/SampleApp.vcxproj")},
			{Name: "SampleMaster", Path: "SampleMaster/SampleMaster.vcxproj", Directory: "SampleMaster", ProjectReferences: parseVCXProjProjectReferences(root, "SampleMaster/SampleMaster.vcxproj")},
			{Name: "SampleWorker", Path: "SampleWorker/SampleWorker.vcxproj", Directory: "SampleWorker"},
			{Name: "SampleAppCmn", Path: "SampleAppCmn/SampleAppCmn.vcxproj", Directory: "SampleAppCmn"},
			{Name: "SampleUpd", Path: "SampleUpd/SampleUpd.vcxproj", Directory: "SampleUpd"},
		},
	}
	edges := inferRuntimeEdges(snapshot, snapshot.SolutionProjects)
	foundAppToMaster := false
	foundMasterToWorker := false
	foundCmnToUpd := false
	for _, edge := range edges {
		if edge.Source == "SampleApp" && edge.Target == "SampleMaster" {
			if edge.Confidence != "high" {
				t.Fatalf("expected high confidence for project reference edge, got %#v", edge)
			}
			foundAppToMaster = true
		}
		if edge.Source == "SampleMaster" && edge.Target == "SampleWorker" {
			if edge.Confidence != "high" {
				t.Fatalf("expected high confidence for worker edge, got %#v", edge)
			}
			foundMasterToWorker = true
		}
		if edge.Source == "SampleAppCmn" && edge.Target == "SampleUpd" {
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
		PrimaryStartup: "SampleApp",
		RuntimeEdges: []RuntimeEdge{
			{Source: "SampleApp", Target: "SampleMaster", Kind: "project_reference", Confidence: "high", Evidence: []string{"SampleApp/SampleApp.vcxproj"}},
			{Source: "SampleWorker", Target: "SampleUpd", Kind: "string_reference", Confidence: "low", Evidence: []string{"SampleWorker/worker.cpp"}},
		},
		TotalFiles: 1,
		TotalLines: 10,
	}
	doc := fallbackFinalDocument(snapshot, nil, nil, "goal")
	if !strings.Contains(doc, "SampleApp -> SampleMaster") {
		t.Fatalf("expected high-confidence runtime edge in document\n%s", doc)
	}
	if strings.Contains(doc, "SampleWorker -> SampleUpd") {
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

func TestCanonicalProjectNameNormalizesSampleAppComnAlias(t *testing.T) {
	got := canonicalProjectName("SampleAppComn", []string{"SampleApp", "SampleAppCmn", "SampleUpd"})
	if got != "SampleAppCmn" {
		t.Fatalf("expected SampleAppComn alias to normalize to SampleAppCmn, got %q", got)
	}
}

func TestBuildOperationalChainUsesCollaborationAndOperationalEvidence(t *testing.T) {
	snapshot := ProjectSnapshot{
		PrimaryStartup: "SampleApp",
		SolutionProjects: []SolutionProject{
			{Name: "SampleApp", Directory: "SampleApp"},
			{Name: "SampleMaster", Directory: "SampleMaster"},
			{Name: "SampleWorker", Directory: "SampleWorker"},
			{Name: "SampleAppCmn", Directory: "SampleAppCmn"},
			{Name: "SampleUpd", Directory: "SampleUpd"},
		},
		RuntimeEdges: []RuntimeEdge{
			{Source: "SampleApp", Target: "SampleMaster", Kind: "dynamic_load", Confidence: "high", Evidence: []string{"SampleApp/SampleWorkerManager.cpp"}},
			{Source: "SampleWorker", Target: "SampleAppComn", Kind: "string_reference", Confidence: "low", Evidence: []string{"SampleWorker/SampleUpdManager.cpp"}},
			{Source: "SampleAppCmn", Target: "SampleUpd", Kind: "dynamic_load", Confidence: "high", Evidence: []string{"SampleAppCmn/dllmain.cpp"}},
		},
	}
	reports := []WorkerReport{
		{
			Title:         "SampleMaster Analysis",
			EvidenceFiles: []string{"SampleMaster/SampleMaster.cpp"},
			Collaboration: []string{"SampleMaster coordinates with SampleWorker for forensic operations."},
			InternalFlow:  []string{"SampleMaster launches worker-side detection orchestration."},
		},
	}
	chain := buildOperationalChain(snapshot, reports)
	got := []string{}
	for _, edge := range chain {
		got = append(got, edge.Source+"->"+edge.Target)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "SampleMaster->SampleWorker") {
		t.Fatalf("expected SampleMaster->SampleWorker in operational chain, got %v", got)
	}
	if !strings.Contains(joined, "SampleWorker->SampleAppCmn") {
		t.Fatalf("expected SampleWorker->SampleAppCmn in operational chain, got %v", got)
	}
	if !strings.Contains(joined, "SampleAppCmn->SampleUpd") {
		t.Fatalf("expected SampleAppCmn->SampleUpd in operational chain, got %v", got)
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
		PrimaryStartup: "SampleApp",
		RuntimeEdges: []RuntimeEdge{
			{Source: "SampleApp", Target: "SampleMaster", Kind: "project_reference", Confidence: "high", Evidence: []string{"SampleApp/SampleApp.vcxproj"}},
		},
		TotalFiles: 1,
		TotalLines: 10,
	}
	doc := fallbackFinalDocument(snapshot, nil, nil, "goal")
	if !strings.Contains(doc, "## Runtime Graph") {
		t.Fatalf("expected runtime graph section\n%s", doc)
	}
	if !strings.Contains(doc, "SampleApp -> SampleMaster") {
		t.Fatalf("expected runtime graph edge\n%s", doc)
	}
}

func TestBuildKnowledgePackIncludesStartupAndSubsystems(t *testing.T) {
	snapshot := ProjectSnapshot{
		Root:            "C:\\repo",
		GeneratedAt:     time.Now(),
		PrimaryStartup:  "SampleApp",
		StartupProjects: []string{"SampleApp", "SampleWorker"},
		ManifestFiles:   []string{"SampleApp.sln"},
		EntrypointFiles: []string{"SampleApp/SampleApp.cpp"},
		SolutionProjects: []SolutionProject{
			{Name: "SampleApp", EntryFiles: []string{"SampleApp/SampleApp.cpp"}},
		},
	}
	shards := []AnalysisShard{
		{ID: "shard-01", Name: "runtime", PrimaryFiles: []string{"SampleApp/SampleApp.cpp"}},
	}
	reports := []WorkerReport{
		{
			Title:            "Core Runtime",
			Responsibilities: []string{"boot client", "load orchestrator"},
			KeyFiles:         []string{"SampleApp/SampleApp.cpp", "SampleAppCmn/dllmain.cpp"},
			EvidenceFiles:    []string{"SampleApp/SampleApp.cpp"},
			EntryPoints:      []string{"SampleApp/SampleApp.cpp::main"},
			Dependencies:     []string{"SampleAppCmn", "Common"},
			Risks:            []string{"startup chain is security-sensitive"},
		},
	}
	pack := buildKnowledgePack(snapshot, shards, reports, "goal", "run-1")
	if pack.PrimaryStartup != "SampleApp" {
		t.Fatalf("expected primary startup in knowledge pack, got %q", pack.PrimaryStartup)
	}
	if len(pack.Subsystems) != 1 {
		t.Fatalf("expected subsystem in knowledge pack, got %#v", pack.Subsystems)
	}
	if !containsString(pack.StartupEntryFiles, "SampleApp/SampleApp.cpp") {
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
	for _, expected := range []string{
		"project_summary:Project Summary",
		"analysis_execution:Analysis Execution Summary",
		"subsystem:Security Control: network",
		"shard:unreal_network",
		"generated_doc:Security Surface",
		"generated_doc_section:Security Surface / Document Metadata",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected vector corpus entry %q in %s", expected, text)
		}
	}
	foundGeneratedDocMetadata := false
	for _, doc := range corpus.Documents {
		if doc.Kind == "generated_doc_section" && strings.Contains(doc.Title, "Security Surface") {
			if doc.Metadata["source"] != "generated_docs" {
				t.Fatalf("expected generated doc metadata source, got %+v", doc.Metadata)
			}
			if !strings.Contains(doc.Metadata["reuse_targets"], "verification_planner") {
				t.Fatalf("expected generated doc reuse target metadata, got %+v", doc.Metadata)
			}
			foundGeneratedDocMetadata = true
			break
		}
	}
	if !foundGeneratedDocMetadata {
		t.Fatalf("expected generated doc section metadata in vector corpus")
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
			PrimaryStartup: "SampleApp",
			Subsystems: []KnowledgeSubsystem{
				{Title: "Core Runtime", Group: "Core Application"},
			},
		},
		Snapshot: ProjectSnapshot{
			Root:           root,
			GeneratedAt:    time.Now(),
			PrimaryStartup: "SampleApp",
			Files: []ScannedFile{
				{Path: "SampleApp/SampleApp.cpp", Directory: "SampleApp", Extension: ".cpp", LineCount: 40, IsEntrypoint: true, ImportanceScore: 12},
			},
		},
		SemanticIndex: SemanticIndex{
			RunID:       "run-1",
			Goal:        "goal",
			Root:        root,
			GeneratedAt: time.Now(),
			Files: []SemanticIndexedFile{
				{Path: "SampleApp/SampleApp.cpp", IsEntrypoint: true},
			},
			Symbols: []SemanticSymbol{
				{ID: "module:SampleApp", Name: "SampleApp", Kind: "unreal_module"},
			},
		},
		UnrealGraph: UnrealSemanticGraph{
			RunID:       "run-1",
			Goal:        "goal",
			Root:        root,
			GeneratedAt: time.Now(),
			Nodes: []UnrealSemanticNode{
				{ID: "module:SampleApp", Kind: "module", Name: "SampleApp"},
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

func TestPersistRunReplacesLatestArtifacts(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.ProjectAnalysis.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)

	run1 := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID: "run-1",
			Goal:  "goal",
		},
		FinalDocument: "# run 1\n",
		KnowledgePack: KnowledgePack{
			RunID: "run-1",
			Goal:  "goal",
			Root:  root,
		},
		Snapshot: ProjectSnapshot{
			Root:        root,
			GeneratedAt: time.Now(),
		},
		UnrealGraph: UnrealSemanticGraph{
			RunID: "run-1",
			Nodes: []UnrealSemanticNode{
				{ID: "module:Game", Kind: "module", Name: "Game"},
			},
		},
		VectorCorpus: VectorCorpus{
			RunID: "run-1",
			Documents: []VectorCorpusDocument{
				{ID: "doc-1", Title: "Doc", Text: "indexed text"},
			},
		},
	}
	run1.VectorIngestion = buildVectorIngestionManifest(run1.VectorCorpus)
	if _, err := analyzer.persistRun(run1); err != nil {
		t.Fatalf("persistRun run1 returned error: %v", err)
	}
	latestDir := filepath.Join(cfg.ProjectAnalysis.OutputDir, "latest")
	if _, err := os.Stat(filepath.Join(latestDir, "unreal_graph.json")); err != nil {
		t.Fatalf("expected run1 latest unreal graph: %v", err)
	}
	if _, err := os.Stat(filepath.Join(latestDir, "vector_corpus.json")); err != nil {
		t.Fatalf("expected run1 latest vector corpus: %v", err)
	}

	run2 := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID: "run-2",
			Goal:  "goal",
		},
		FinalDocument: "# run 2\n",
		KnowledgePack: KnowledgePack{
			RunID: "run-2",
			Goal:  "goal",
			Root:  root,
		},
		Snapshot: ProjectSnapshot{
			Root:        root,
			GeneratedAt: time.Now(),
		},
	}
	if _, err := analyzer.persistRun(run2); err != nil {
		t.Fatalf("persistRun run2 returned error: %v", err)
	}
	for _, stale := range []string{"unreal_graph.json", "vector_corpus.json", "vector_corpus.jsonl", "vector_ingest_manifest.json"} {
		if _, err := os.Stat(filepath.Join(latestDir, stale)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected stale latest artifact %s to be removed, stat err=%v", stale, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(latestDir, "run.json"))
	if err != nil {
		t.Fatalf("expected latest run json: %v", err)
	}
	if !strings.Contains(string(data), `"run_id": "run-2"`) {
		t.Fatalf("expected latest run json to be replaced by run-2, got %s", string(data))
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
		PrimaryStartup: "SampleApp",
		Files: []ScannedFile{
			{Path: "Common/ETWConsumer.cpp", LineCount: 1032},
			{Path: "SampleWorker/PrefetchScanner.cpp", LineCount: 920},
		},
		SolutionProjects: []SolutionProject{
			{Name: "SampleApp", EntryFiles: []string{"SampleApp/SampleApp.cpp"}},
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
			KeyFiles:         []string{"SampleWorker/PrefetchScanner.cpp"},
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

func TestBuildSemanticIndexV2IncludesSourceAnchorsAndBuildContexts(t *testing.T) {
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

	mustWrite("Source/GuardRuntime/GuardRuntime.Build.cs", `public class GuardRuntime : ModuleRules { public GuardRuntime(ReadOnlyTargetRules Target) : base(Target) {} }`)
	mustWrite("Source/GuardRuntime/Private/IoctlDispatch.cpp", `bool ValidateRequest() { return true; }
int GuardDispatch() { if (ValidateRequest()) { DeviceIoControl(0, 0, 0, 0, 0, 0, 0, 0); } return 0; }
bool ScanRemoteMemory() { return ReadProcessMemory(0, 0, 0, 0, 0); }
`)
	mustWrite("native/cmake-build-debug/compile_commands.json", `[
  {
    "directory": "`+filepath.ToSlash(root)+`",
    "file": "Source/GuardRuntime/Private/IoctlDispatch.cpp",
    "arguments": ["clang++", "-I", "Source/GuardRuntime/Public", "-DGUARD_BUILD", "-c", "Source/GuardRuntime/Private/IoctlDispatch.cpp"]
  }
]`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "goal", "run-1")
	index := buildSemanticIndexV2(snapshot, "goal", "run-1", graph)

	if len(index.BuildContexts) == 0 {
		t.Fatalf("expected build contexts in v2 index, got %+v", index)
	}
	foundBuildContext := false
	foundIoctlHandler := false
	foundMemoryPath := false
	for _, symbol := range index.Symbols {
		if symbol.Kind == "build_context" && strings.Contains(symbol.ID, "buildctx:compile:module:GuardRuntime") {
			foundBuildContext = true
		}
		if symbol.Name == "GuardDispatch" && symbol.Kind == "ioctl_handler" {
			foundIoctlHandler = true
		}
		if symbol.Name == "ScanRemoteMemory" && symbol.Kind == "memory_path" {
			foundMemoryPath = true
		}
	}
	if !foundBuildContext {
		t.Fatalf("expected build context symbol in %+v", index.Symbols)
	}
	if !foundIoctlHandler {
		t.Fatalf("expected ioctl handler symbol in %+v", index.Symbols)
	}
	if !foundMemoryPath {
		t.Fatalf("expected memory path symbol in %+v", index.Symbols)
	}
	foundCall := false
	foundCompileOwnership := false
	for _, edge := range index.CallEdges {
		if strings.Contains(edge.SourceID, "GuardDispatch") && strings.Contains(edge.TargetID, "ValidateRequest") {
			foundCall = true
			break
		}
	}
	for _, edge := range index.BuildOwnershipEdges {
		if strings.Contains(edge.SourceID, "buildctx:compile:module:GuardRuntime") && strings.Contains(edge.TargetID, "GuardDispatch") && edge.Type == "compiles_symbol" {
			foundCompileOwnership = true
			break
		}
	}
	if !foundCall {
		t.Fatalf("expected function-level call edge in %+v", index.CallEdges)
	}
	if !foundCompileOwnership {
		t.Fatalf("expected build-context ownership edge in %+v", index.BuildOwnershipEdges)
	}
}

func TestBuildSemanticIndexV2CapturesDriverRegistrationEdges(t *testing.T) {
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

	mustWrite("Driver/Driver.cpp", `
void MyCreate() {}
void MyDeviceControl() {}
void MyUnload() {}
void MyProcessNotify() {}
void DriverEntry()
{
    DriverObject->MajorFunction[IRP_MJ_CREATE] = MyCreate;
    DriverObject->MajorFunction[IRP_MJ_DEVICE_CONTROL] = MyDeviceControl;
    DriverObject->DriverUnload = MyUnload;
    PsSetCreateProcessNotifyRoutineEx(MyProcessNotify, FALSE);
}
`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "map driver", "run-1")
	index := buildSemanticIndexV2(snapshot, "map driver", "run-1", graph)
	for _, want := range []struct {
		source string
		target string
		typ    string
	}{
		{source: "DriverEntry", target: "MyDeviceControl", typ: "registers_irp_dispatch"},
		{source: "DriverEntry", target: "MyUnload", typ: "registers_unload_callback"},
		{source: "DriverEntry", target: "MyProcessNotify", typ: "registers_process_notify_callback"},
	} {
		if !testCallEdgeContains(index.CallEdges, want.source, want.target, want.typ) {
			t.Fatalf("expected %s -> %s (%s), got %+v", want.source, want.target, want.typ, index.CallEdges)
		}
	}
	facts := buildArchitectureFactPack(snapshot, index, graph, "map driver")
	if !architectureFlowContains(facts.FlowFacts, "registered callback and dispatch edges", "MyDeviceControl", "MyProcessNotify") {
		t.Fatalf("expected registration flow fact, got %+v", facts.FlowFacts)
	}
}

func TestBuildSemanticIndexV2CapturesDriverRegistrationTables(t *testing.T) {
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

	mustWrite("Driver/Callbacks.cpp", `
void ObjectPreCallback() {}
void ObjectPostCallback() {}
void PreCreate() {}
void PostCreate() {}

OB_OPERATION_REGISTRATION gObjectOperations[] = {
    { PsProcessType, OB_OPERATION_HANDLE_CREATE | OB_OPERATION_HANDLE_DUPLICATE, ObjectPreCallback, ObjectPostCallback },
};

const FLT_OPERATION_REGISTRATION gFileOperations[] = {
    { IRP_MJ_CREATE, 0, PreCreate, PostCreate },
    { IRP_MJ_OPERATION_END }
};
`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "map driver", "run-1")
	index := buildSemanticIndexV2(snapshot, "map driver", "run-1", graph)
	for _, want := range []struct {
		source string
		target string
		typ    string
	}{
		{source: "gObjectOperations", target: "ObjectPreCallback", typ: "registers_object_callback"},
		{source: "gObjectOperations", target: "ObjectPostCallback", typ: "registers_object_callback"},
		{source: "gFileOperations", target: "PreCreate", typ: "registers_file_filter_callback"},
		{source: "gFileOperations", target: "PostCreate", typ: "registers_file_filter_callback"},
	} {
		if !testCallEdgeContains(index.CallEdges, want.source, want.target, want.typ) {
			t.Fatalf("expected %s -> %s (%s), got %+v", want.source, want.target, want.typ, index.CallEdges)
		}
	}
	if !testSymbolContains(index.Symbols, "gObjectOperations", "object_callback_table") {
		t.Fatalf("expected object callback table symbol, got %+v", index.Symbols)
	}
	if !testSymbolContains(index.Symbols, "gFileOperations", "file_filter_callback_table") {
		t.Fatalf("expected file filter callback table symbol, got %+v", index.Symbols)
	}
	facts := buildArchitectureFactPack(snapshot, index, graph, "map driver")
	if !architectureFlowContains(facts.FlowFacts, "registered callback and dispatch edges", "ObjectPreCallback", "PreCreate") {
		t.Fatalf("expected registration table flow fact, got %+v", facts.FlowFacts)
	}
}

func TestBuildSemanticIndexV2CapturesAliasedAndMacroRegistrationTables(t *testing.T) {
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

	mustWrite("Driver/MacroCallbacks.cpp", `
#define DECLARE_OBJECT_CALLBACK_TABLE(name, type, pre, post) int name
#define DECLARE_MINIFILTER_OPERATION_TABLE(name, major, pre, post) int name
#define BUILD_OBJECT_TABLE(prefix) \
    DECLARE_OBJECT_CALLBACK_TABLE(prefix##ObjectOps, PsProcessType, prefix##PreOperation, prefix##PostOperation)
#define BUILD_FILE_TABLE(prefix) \
    DECLARE_MINIFILTER_OPERATION_TABLE(prefix##FileOps, IRP_MJ_CREATE, prefix##FilePre, prefix##FilePost)
#define NESTED_FILTER_TABLE(prefix) BUILD_FILE_TABLE(prefix)
#define VARARG_OBJECT_TABLE(prefix, ...) DECLARE_OBJECT_CALLBACK_TABLE(prefix##VarOps, PsProcessType, __VA_ARGS__)
#ifdef USE_OBJECT_VARIANT
#define CONDITIONAL_TABLE(prefix) DECLARE_OBJECT_CALLBACK_TABLE(prefix##ConditionalOps, PsProcessType, prefix##ConditionalPre, prefix##ConditionalPost)
#else
#define CONDITIONAL_TABLE(prefix) DECLARE_MINIFILTER_OPERATION_TABLE(prefix##ConditionalFileOps, IRP_MJ_CREATE, prefix##ConditionalFilePre, prefix##ConditionalFilePost)
#endif

void AliasObjectPre() {}
void AliasObjectPost() {}
void AliasFilePre() {}
void AliasFilePost() {}
void MacroObjectPre() {}
void MacroObjectPost() {}
void MacroFilePre() {}
void MacroFilePost() {}
void GenericRegistrationCallback() {}
void GuardPreOperation() {}
void GuardPostOperation() {}
void GuardFilePre() {}
void GuardFilePost() {}
void VarPre() {}
void VarPost() {}
void MaybeConditionalPre() {}
void MaybeConditionalPost() {}
void MaybeConditionalFilePre() {}
void MaybeConditionalFilePost() {}

typedef OB_OPERATION_REGISTRATION MY_OB_OPERATION_REGISTRATION;
using MY_FLT_OPERATION_REGISTRATION = FLT_OPERATION_REGISTRATION;

MY_OB_OPERATION_REGISTRATION gAliasObjectOperations[] = {
    { PsProcessType, OB_OPERATION_HANDLE_CREATE, AliasObjectPre, AliasObjectPost },
};

MY_FLT_OPERATION_REGISTRATION gAliasFileOperations[] = {
    { IRP_MJ_CREATE, 0, AliasFilePre, AliasFilePost },
    { IRP_MJ_OPERATION_END }
};

CUSTOM_CALLBACK_REGISTRATION gGenericRegistration = {
    GenericRegistrationCallback,
    NULL,
};

DECLARE_OBJECT_CALLBACK_TABLE(gMacroObjectOperations, PsProcessType, MacroObjectPre, MacroObjectPost);
DECLARE_MINIFILTER_OPERATION_TABLE(gMacroFileOperations, IRP_MJ_CREATE, MacroFilePre, MacroFilePost);
BUILD_OBJECT_TABLE(Guard);
NESTED_FILTER_TABLE(Guard);
VARARG_OBJECT_TABLE(Var, VarPre, VarPost);
CONDITIONAL_TABLE(Maybe);
`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "map driver", "run-1")
	index := buildSemanticIndexV2(snapshot, "map driver", "run-1", graph)
	for _, want := range []struct {
		source string
		target string
		typ    string
	}{
		{source: "gAliasObjectOperations", target: "AliasObjectPre", typ: "registers_object_callback"},
		{source: "gAliasFileOperations", target: "AliasFilePre", typ: "registers_file_filter_callback"},
		{source: "gMacroObjectOperations", target: "MacroObjectPre", typ: "registers_object_callback"},
		{source: "gMacroFileOperations", target: "MacroFilePre", typ: "registers_file_filter_callback"},
		{source: "Guard", target: "GuardPreOperation", typ: "registers_object_callback"},
		{source: "Guard", target: "GuardFilePre", typ: "registers_file_filter_callback"},
		{source: "Var", target: "VarPre", typ: "registers_object_callback"},
		{source: "Maybe", target: "MaybeConditionalPre", typ: "registers_object_callback"},
		{source: "Maybe", target: "MaybeConditionalFilePre", typ: "registers_file_filter_callback"},
		{source: "gGenericRegistration", target: "GenericRegistrationCallback", typ: "registers_callback_table_entry"},
	} {
		if !testCallEdgeContains(index.CallEdges, want.source, want.target, want.typ) {
			t.Fatalf("expected %s -> %s (%s), got %+v", want.source, want.target, want.typ, index.CallEdges)
		}
	}
	if !testSymbolContains(index.Symbols, "gAliasObjectOperations", "object_callback_table") {
		t.Fatalf("expected aliased object callback table symbol, got %+v", index.Symbols)
	}
	if !testSymbolContains(index.Symbols, "gAliasFileOperations", "file_filter_callback_table") {
		t.Fatalf("expected aliased file filter table symbol, got %+v", index.Symbols)
	}
	facts := buildArchitectureFactPack(snapshot, index, graph, "map driver")
	if !architectureFlowContains(facts.FlowFacts, "registered callback and dispatch edges", "GuardPreOperation", "MaybeConditionalPre") {
		t.Fatalf("expected macro registration flow fact, got %+v", facts.FlowFacts)
	}
}

func TestBuildSemanticIndexV2ExpandsIncludedRegistrationMacros(t *testing.T) {
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

	mustWrite("Driver/RegistrationBase.h", `
#define INCLUDED_DECLARE_OBJECT(name, type, pre, post) int name
#define INCLUDED_DECLARE_FILE(name, major, pre, post) int name
#define INCLUDED_FILE_TABLE(prefix) INCLUDED_DECLARE_FILE(prefix##FileOps, IRP_MJ_CREATE, prefix##FilePre, prefix##FilePost)
typedef OB_OPERATION_REGISTRATION INCLUDED_OB_REGISTRATION;
`)
	mustWrite("Driver/RegistrationLevel4.h", `
#define INCLUDED_DEEP_OBJECT(prefix) INCLUDED_DECLARE_OBJECT(prefix##DeepOps, PsProcessType, prefix##DeepPre, prefix##DeepPost)
`)
	mustWrite("Driver/RegistrationLevel3.h", `
#include "RegistrationLevel4.h"
`)
	mustWrite("Driver/RegistrationLevel2.h", `
#include "RegistrationLevel3.h"
`)
	mustWrite("Driver/RegistrationMacros.h", `
#include "RegistrationBase.h"
#include "RegistrationLevel2.h"
#define INCLUDED_OBJECT_TABLE(prefix) \
    INCLUDED_DECLARE_OBJECT(prefix##ObjectOps, PsProcessType, prefix##Pre, prefix##Post)
#define INCLUDED_NESTED_FILE_TABLE(prefix) INCLUDED_FILE_TABLE(prefix)
`)
	mustWrite("Driver/IncludedUse.cpp", `
#include "RegistrationMacros.h"

void IncludedPre() {}
void IncludedPost() {}
void IncludedFilePre() {}
void IncludedFilePost() {}
void IncludedAliasPre() {}
void IncludedAliasPost() {}
void IncludedDeepPre() {}
void IncludedDeepPost() {}

INCLUDED_OB_REGISTRATION gIncludedAliasOperations[] = {
    { PsProcessType, OB_OPERATION_HANDLE_CREATE, IncludedAliasPre, IncludedAliasPost },
};

INCLUDED_OBJECT_TABLE(Included);
INCLUDED_NESTED_FILE_TABLE(Included);
INCLUDED_DEEP_OBJECT(Included);
`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "map driver", "run-1")
	index := buildSemanticIndexV2(snapshot, "map driver", "run-1", graph)
	for _, want := range []struct {
		source string
		target string
		typ    string
	}{
		{source: "gIncludedAliasOperations", target: "IncludedAliasPre", typ: "registers_object_callback"},
		{source: "Included", target: "IncludedPre", typ: "registers_object_callback"},
		{source: "Included", target: "IncludedFilePre", typ: "registers_file_filter_callback"},
		{source: "Included", target: "IncludedDeepPre", typ: "registers_object_callback"},
	} {
		if !testCallEdgeContains(index.CallEdges, want.source, want.target, want.typ) {
			t.Fatalf("expected %s -> %s (%s), got %+v", want.source, want.target, want.typ, index.CallEdges)
		}
	}
}

func testCallEdgeContains(edges []CallEdge, source string, target string, typ string) bool {
	for _, edge := range edges {
		if strings.Contains(edge.SourceID, source) &&
			strings.Contains(edge.TargetID, target) &&
			edge.Type == typ {
			return true
		}
	}
	return false
}

func testSymbolContains(symbols []SymbolRecord, name string, kind string) bool {
	for _, symbol := range symbols {
		if strings.Contains(symbol.Name, name) && symbol.Kind == kind {
			return true
		}
	}
	return false
}

func TestBuildSemanticIndexV2QualifiesInlineScopedMethods(t *testing.T) {
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

	mustWrite("Source/GuardRuntime/GuardRuntime.Build.cs", `public class GuardRuntime : ModuleRules { public GuardRuntime(ReadOnlyTargetRules Target) : base(Target) {} }`)
	mustWrite("Source/GuardRuntime/Public/InlineScanner.h", `namespace Guard
{
class Scanner
{
public:
    bool Validate()
    {
        return true;
    }

    bool Dispatch()
    {
        return Validate();
    }
};

bool GlobalCheck()
{
    return true;
}
}
`)
	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "goal", "run-1")
	index := buildSemanticIndexV2(snapshot, "goal", "run-1", graph)

	foundValidate := false
	foundDispatch := false
	foundGlobal := false
	for _, symbol := range index.Symbols {
		switch symbol.CanonicalName {
		case "Guard::Scanner::Validate":
			foundValidate = symbol.ContainerSymbolID == "type:Guard::Scanner"
		case "Guard::Scanner::Dispatch":
			foundDispatch = symbol.ContainerSymbolID == "type:Guard::Scanner"
		case "Guard::GlobalCheck":
			foundGlobal = symbol.ContainerSymbolID == ""
		}
	}
	if !foundValidate || !foundDispatch || !foundGlobal {
		t.Fatalf("expected qualified inline symbols, got %+v", index.Symbols)
	}

	foundScopedCall := false
	for _, edge := range index.CallEdges {
		if strings.Contains(edge.SourceID, "Guard::Scanner::Dispatch") && strings.Contains(edge.TargetID, "Guard::Scanner::Validate") {
			foundScopedCall = true
			break
		}
	}
	if !foundScopedCall {
		t.Fatalf("expected same-container inline call edge, got %+v", index.CallEdges)
	}
}

func TestBuildSemanticIndexV2CapturesTemplatedOutOfLineMethods(t *testing.T) {
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

	mustWrite("Source/GuardRuntime/GuardRuntime.Build.cs", `public class GuardRuntime : ModuleRules { public GuardRuntime(ReadOnlyTargetRules Target) : base(Target) {} }`)
	mustWrite("Source/GuardRuntime/Public/TemplateScanner.h", `namespace Guard
{
template <typename TValue>
class Scanner
{
public:
    bool Validate(
        int pid,
        const TValue* value
    ) const;

    bool Dispatch();
};

template <typename TValue>
bool Scanner<TValue>::Validate(
    int pid,
    const TValue* value
) const
{
    return pid > 0 && value != nullptr;
}

template <typename TValue>
bool Scanner<TValue>::Dispatch()
{
    return Scanner<TValue>::Validate(
        7,
        nullptr
    );
}
}
`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "goal", "run-1")
	index := buildSemanticIndexV2(snapshot, "goal", "run-1", graph)

	foundValidate := false
	foundDispatch := false
	for _, symbol := range index.Symbols {
		switch symbol.CanonicalName {
		case "Guard::Scanner::Validate":
			foundValidate = symbol.ContainerSymbolID == "type:Guard::Scanner"
		case "Guard::Scanner::Dispatch":
			foundDispatch = symbol.ContainerSymbolID == "type:Guard::Scanner"
		}
	}
	if !foundValidate || !foundDispatch {
		t.Fatalf("expected templated out-of-line symbols, got %+v", index.Symbols)
	}

	foundTemplatedCall := false
	for _, edge := range index.CallEdges {
		if strings.Contains(edge.SourceID, "Guard::Scanner::Dispatch") && strings.Contains(edge.TargetID, "Guard::Scanner::Validate") {
			foundTemplatedCall = true
			break
		}
	}
	if !foundTemplatedCall {
		t.Fatalf("expected templated same-type call edge, got %+v", index.CallEdges)
	}
}

func TestBuildSemanticIndexV2CapturesAttributedOperatorMethods(t *testing.T) {
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

	mustWrite("Source/GuardRuntime/GuardRuntime.Build.cs", `public class GuardRuntime : ModuleRules { public GuardRuntime(ReadOnlyTargetRules Target) : base(Target) {} }`)
	mustWrite("Source/GuardRuntime/Public/OperatorScanner.h", `namespace Guard
{
class Scanner
{
public:
    __declspec(noinline) bool Validate(
        int pid
    ) const
    {
        return pid > 0;
    }

    [[nodiscard]] FORCEINLINE bool operator()(
        int pid
    ) const
    {
        return Validate(pid);
    }
};
}
`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "goal", "run-1")
	index := buildSemanticIndexV2(snapshot, "goal", "run-1", graph)

	foundValidate := false
	foundOperator := false
	for _, symbol := range index.Symbols {
		switch symbol.CanonicalName {
		case "Guard::Scanner::Validate":
			foundValidate = symbol.ContainerSymbolID == "type:Guard::Scanner"
		case "Guard::Scanner::operator()":
			foundOperator = symbol.ContainerSymbolID == "type:Guard::Scanner"
		}
	}
	if !foundValidate || !foundOperator {
		t.Fatalf("expected attributed operator symbols, got %+v", index.Symbols)
	}

	foundOperatorCall := false
	for _, edge := range index.CallEdges {
		if strings.Contains(edge.SourceID, "operator()") && strings.Contains(edge.TargetID, "Guard::Scanner::Validate") {
			foundOperatorCall = true
			break
		}
	}
	if !foundOperatorCall {
		t.Fatalf("expected operator-to-validate call edge, got %+v", index.CallEdges)
	}
}

func TestBuildSemanticIndexV2CapturesRequiresConversionOperators(t *testing.T) {
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

	mustWrite("Source/GuardRuntime/GuardRuntime.Build.cs", `public class GuardRuntime : ModuleRules { public GuardRuntime(ReadOnlyTargetRules Target) : base(Target) {} }`)
	mustWrite("Source/GuardRuntime/Public/RequiresScanner.h", `namespace Guard
{
template <typename TValue>
class Scanner
{
public:
    explicit operator bool() const
        requires (sizeof(TValue) >= 4)
    {
        return true;
    }

    bool Dispatch() const
        requires (sizeof(TValue) >= 4)
    {
        return this->operator bool();
    }
};
}
`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "goal", "run-1")
	index := buildSemanticIndexV2(snapshot, "goal", "run-1", graph)

	foundDispatch := false
	foundConversion := false
	for _, symbol := range index.Symbols {
		switch symbol.CanonicalName {
		case "Guard::Scanner::Dispatch":
			foundDispatch = symbol.ContainerSymbolID == "type:Guard::Scanner"
		case "Guard::Scanner::operatorbool":
			foundConversion = symbol.ContainerSymbolID == "type:Guard::Scanner"
		}
	}
	if !foundDispatch || !foundConversion {
		t.Fatalf("expected requires conversion symbols, got %+v", index.Symbols)
	}

	foundConversionCall := false
	for _, edge := range index.CallEdges {
		if strings.Contains(edge.SourceID, "Guard::Scanner::Dispatch") && strings.Contains(edge.TargetID, "Guard::Scanner::operatorbool") {
			foundConversionCall = true
			break
		}
	}
	if !foundConversionCall {
		t.Fatalf("expected dispatch-to-conversion call edge, got %+v", index.CallEdges)
	}
}

func TestBuildSemanticIndexV2CapturesMacroWrappedScopedAndFriendMethods(t *testing.T) {
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

	mustWrite("Source/GuardRuntime/GuardRuntime.Build.cs", `public class GuardRuntime : ModuleRules { public GuardRuntime(ReadOnlyTargetRules Target) : base(Target) {} }`)
	mustWrite("Source/GuardRuntime/Public/MacroScanner.h", `#define SHOOTERGAME_API
#define FORCEINLINE inline

namespace Guard
{
class SHOOTERGAME_API Scanner final
{
public:
    GENERATED_BODY()

    constexpr bool Validate(
        int pid
    ) const
    {
        return pid > 0;
    }

    FORCEINLINE decltype(auto) Access(
        int pid
    ) const
    {
        return Validate(pid);
    }

    consteval static int BuildEpoch()
    {
        return 7;
    }

    friend constexpr bool Inspect(const Scanner& value)
    {
        return value.Validate(7);
    }
};
}
`)

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)
	snapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject returned error: %v", err)
	}
	graph := buildUnrealSemanticGraph(snapshot, "goal", "run-1")
	index := buildSemanticIndexV2(snapshot, "goal", "run-1", graph)

	foundValidate := false
	foundAccess := false
	foundBuildEpoch := false
	foundFriendInspect := false
	for _, symbol := range index.Symbols {
		switch symbol.CanonicalName {
		case "Guard::Scanner::Validate":
			foundValidate = symbol.ContainerSymbolID == "type:Guard::Scanner"
		case "Guard::Scanner::Access":
			foundAccess = symbol.ContainerSymbolID == "type:Guard::Scanner"
		case "Guard::Scanner::BuildEpoch":
			foundBuildEpoch = symbol.ContainerSymbolID == "type:Guard::Scanner"
		case "Guard::Inspect":
			foundFriendInspect = symbol.ContainerSymbolID == ""
		}
	}
	if !foundValidate || !foundAccess || !foundBuildEpoch || !foundFriendInspect {
		t.Fatalf("expected macro-wrapped scoped symbols, got %+v", index.Symbols)
	}

	foundAccessCall := false
	foundFriendCall := false
	for _, edge := range index.CallEdges {
		if strings.Contains(edge.SourceID, "Guard::Scanner::Access") && strings.Contains(edge.TargetID, "Guard::Scanner::Validate") {
			foundAccessCall = true
		}
		if strings.Contains(edge.SourceID, "Guard::Inspect") && strings.Contains(edge.TargetID, "Guard::Scanner::Validate") {
			foundFriendCall = true
		}
	}
	if !foundAccessCall || !foundFriendCall {
		t.Fatalf("expected macro-wrapped call edges, got %+v", index.CallEdges)
	}
}

func TestComputeSemanticFingerprintV2ChangesWithSourceAnchorNeighborhood(t *testing.T) {
	root := t.TempDir()
	writeMain := func(body string) {
		path := filepath.Join(root, "main.go")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write main.go: %v", err)
		}
	}

	cfg := DefaultConfig(root)
	ws := Workspace{BaseRoot: root, Root: root}
	analyzer := newProjectAnalyzer(cfg, &stubAnalysisClient{}, ws, nil, nil)

	writeMain("package main\nfunc helper() {}\nfunc entry() { helper() }\n")
	firstSnapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject first pass: %v", err)
	}
	firstSnapshot.ProjectEdges = buildProjectEdges(firstSnapshot)
	analyzer.cachedUnrealGraph = buildUnrealSemanticGraph(firstSnapshot, "goal", "run-1")
	analyzer.cachedSemanticIndexV2 = buildSemanticIndexV2(firstSnapshot, "goal", "run-1", analyzer.cachedUnrealGraph)
	firstFingerprint := analyzer.computeSemanticFingerprint(firstSnapshot, []string{"main.go"})

	writeMain("package main\nfunc helper() {}\nfunc validate() {}\nfunc entry() { validate() }\n")
	secondSnapshot, err := analyzer.scanProject()
	if err != nil {
		t.Fatalf("scanProject second pass: %v", err)
	}
	secondSnapshot.ProjectEdges = buildProjectEdges(secondSnapshot)
	analyzer.cachedUnrealGraph = buildUnrealSemanticGraph(secondSnapshot, "goal", "run-2")
	analyzer.cachedSemanticIndexV2 = buildSemanticIndexV2(secondSnapshot, "goal", "run-2", analyzer.cachedUnrealGraph)
	secondFingerprint := analyzer.computeSemanticFingerprint(secondSnapshot, []string{"main.go"})

	if firstFingerprint == secondFingerprint {
		t.Fatalf("expected semantic fingerprint to change with source-anchor neighborhood updates")
	}
}
