package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuiltinRootCausePatternPackLoads(t *testing.T) {
	pack := loadBuiltinRootCausePatternPack()
	if len(pack.Patterns) < 20 {
		t.Fatalf("expected at least 20 builtin patterns, got %d", len(pack.Patterns))
	}
	found := false
	for _, pattern := range pack.Patterns {
		if pattern.ID == "win_service_stop_event_not_signaled" {
			found = true
			if len(pattern.CodeSignals) == 0 || len(pattern.VerificationProbes) == 0 {
				t.Fatalf("expected service stop pattern to include signals and probes: %#v", pattern)
			}
		}
	}
	if !found {
		t.Fatalf("expected service stop pattern in builtin pack")
	}
}

func TestBuiltinRootCausePatternsMeetMinimumQuality(t *testing.T) {
	pack := loadBuiltinRootCausePatternPack()
	for _, pattern := range pack.Patterns {
		if pattern.ID == "" || pattern.Title == "" {
			t.Fatalf("pattern missing id/title: %#v", pattern)
		}
		if len(pattern.ProjectTypes) == 0 {
			t.Fatalf("pattern %s missing project types", pattern.ID)
		}
		if len(pattern.Symptoms) == 0 || len(pattern.RootCauses) == 0 {
			t.Fatalf("pattern %s missing symptom or root cause examples", pattern.ID)
		}
		if len(pattern.CodeSignals) == 0 || len(pattern.StateVariables) == 0 {
			t.Fatalf("pattern %s missing code or state signals", pattern.ID)
		}
		if len(pattern.OutOfRangeCases) == 0 || len(pattern.VerificationProbes) == 0 {
			t.Fatalf("pattern %s missing out-of-range cases or probes", pattern.ID)
		}
	}
}

func TestInferRootCauseProjectTypesDetectsWindowsServiceFromSource(t *testing.T) {
	root := t.TempDir()
	source := "void ServiceMain() { RegisterServiceCtrlHandlerExW(nullptr, nullptr, nullptr); SetServiceStatus(nullptr, nullptr); }\n"
	if err := os.WriteFile(filepath.Join(root, "service.cpp"), []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	snapshot := rootCauseTestSnapshot([]ScannedFile{{Path: "service.cpp", Directory: ".", Extension: ".cpp", LineCount: 1}})
	snapshot.Root = root
	types := inferRootCauseProjectTypes(snapshot, "sc stop leaves my service running")
	if !containsString(types, "windows_user_service") {
		t.Fatalf("expected windows service type, got %#v", types)
	}
}

func TestRootCausePatternMatchFindsServiceStopPattern(t *testing.T) {
	root := t.TempDir()
	source := strings.Join([]string{
		"void Handler(DWORD control)",
		"{",
		"    if (control == SERVICE_CONTROL_STOP)",
		"    {",
		"        SetServiceStatus(nullptr, nullptr);",
		"    }",
		"}",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "service_control.cpp"), []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	snapshot := rootCauseTestSnapshot([]ScannedFile{{Path: "service_control.cpp", Directory: ".", Extension: ".cpp", LineCount: 7}})
	snapshot.Root = root
	snapshot.ProjectTypes = inferRootCauseProjectTypes(snapshot, "sc stop does not stop the service")
	matches := matchRootCausePatterns(snapshot, "sc stop does not stop the service", 8)
	if len(matches) == 0 {
		t.Fatalf("expected pattern matches")
	}
	if matches[0].PatternID != "win_service_stop_event_not_signaled" && matches[0].PatternID != "win_service_status_never_reaches_stopped" {
		t.Fatalf("expected service stop pattern first, got %#v", matches[0])
	}
	if len(matches[0].MatchedFiles) == 0 {
		t.Fatalf("expected matched service file, got %#v", matches[0])
	}
}

func TestRootCausePatternMatchRejectsProjectTypeOnlyHit(t *testing.T) {
	snapshot := rootCauseTestSnapshot(nil)
	snapshot.ProjectTypes = []string{"windows_user_service"}
	pack := RootCausePatternPack{Patterns: []RootCausePattern{{
		ID:           "service_type_only",
		Title:        "Service type only prior",
		ProjectTypes: []string{"windows_user_service"},
		Symptoms:     []string{"service stop hangs"},
		RootCauses:   []string{"stop event is not signaled"},
		CodeSignals:  []string{"SERVICE_CONTROL_STOP"},
	}}}
	matches := matchRootCausePatternsFromPack(snapshot, "document artifact is missing after generation", pack, 8)
	if len(matches) != 0 {
		t.Fatalf("expected project-type-only hit to be rejected, got %#v", matches)
	}
}

func TestLoadRootCausePatternPackIncludesLocalWorkspacePacks(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, ".kernforge", "root_cause", "pattern_packs")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir pack dir: %v", err)
	}
	localPack := RootCausePatternPack{Patterns: []RootCausePattern{{
		ID:                 "local_artifact_missing",
		Title:              "Local artifact finalization skips write",
		ProjectTypes:       []string{"go_cli_agent"},
		Symptoms:           []string{"requested artifact file is missing"},
		RootCauses:         []string{"writeArtifact is skipped when finalization_enabled=false"},
		CodeSignals:        []string{"writeArtifact"},
		StateVariables:     []string{"finalization_enabled"},
		OutOfRangeCases:    []string{"finalization_enabled=false"},
		VerificationProbes: []string{"assert writeArtifact runs before final response"},
	}}}
	data, _ := json.Marshal(localPack)
	if err := os.WriteFile(filepath.Join(packDir, "local.json"), data, 0o644); err != nil {
		t.Fatalf("write local pack: %v", err)
	}
	pack, diagnostics := loadRootCausePatternPackWithDiagnostics(root, nil)
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
	found := false
	for _, pattern := range pack.Patterns {
		if pattern.ID == "local_artifact_missing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected local pattern pack to be loaded")
	}
}

func TestRootCausePatternPackInputPathsIncludesDefaultAndExplicitPacks(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, ".kernforge", "root_cause", "pattern_packs")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir default pack dir: %v", err)
	}
	defaultPath := filepath.Join(packDir, "default.json")
	explicitPath := filepath.Join(root, "explicit.json")
	for _, path := range []string{defaultPath, explicitPath} {
		if err := os.WriteFile(path, []byte(`{"patterns":[]}`), 0o644); err != nil {
			t.Fatalf("write pack %s: %v", path, err)
		}
	}
	paths := rootCausePatternPackInputPaths(root, []string{explicitPath})
	if !containsString(paths, defaultPath) {
		t.Fatalf("expected default local pack to remain loaded with explicit packs, got %#v", paths)
	}
	if !containsString(paths, explicitPath) {
		t.Fatalf("expected explicit pack, got %#v", paths)
	}
}

func TestRootCausePatternPackValidationPreservesLocalDuplicates(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, ".kernforge", "root_cause", "pattern_packs")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir pack dir: %v", err)
	}
	pack := RootCausePatternPack{Patterns: []RootCausePattern{
		{ID: "duplicate_local", Title: "First duplicate", ProjectTypes: []string{"go_cli_agent"}, RootCauses: []string{"first root cause"}, CodeSignals: []string{"firstSignal"}},
		{ID: "duplicate_local", Title: "Second duplicate", ProjectTypes: []string{"go_cli_agent"}, RootCauses: []string{"second root cause"}, CodeSignals: []string{"secondSignal"}},
	}}
	data, _ := json.Marshal(pack)
	if err := os.WriteFile(filepath.Join(packDir, "duplicates.json"), data, 0o644); err != nil {
		t.Fatalf("write duplicate pack: %v", err)
	}
	loaded, diagnostics := loadRootCausePatternPackForValidation(root, nil)
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", diagnostics)
	}
	validation := validateRootCausePatternPack(loaded)
	foundDuplicate := false
	for _, issue := range validation.Errors {
		if issue.PatternID == "duplicate_local" && strings.Contains(issue.Message, "duplicate") {
			foundDuplicate = true
		}
	}
	if !foundDuplicate {
		t.Fatalf("expected duplicate local pattern id to be reported, got %#v", validation.Errors)
	}
}

func TestRootCausePlannerPromptIncludesKnownPatterns(t *testing.T) {
	snapshot := rootCauseTestSnapshot([]ScannedFile{{Path: "service.cpp", Directory: ".", Extension: ".cpp", LineCount: 10}})
	snapshot.ProjectTypes = []string{"windows_user_service"}
	snapshot.RootCause.PatternMatches = []RootCausePatternMatch{{
		PatternID:      "win_service_stop_event_not_signaled",
		Title:          "Windows service stop handler does not signal worker shutdown",
		ProjectTypes:   []string{"windows_user_service"},
		Score:          88,
		Confidence:     "high",
		MatchedSignals: []string{"SERVICE_CONTROL_STOP"},
		MatchedFiles:   []string{"service.cpp"},
	}}
	prompt := buildRootCausePlannerPrompt(snapshot, "sc stop does not stop the service")
	if !strings.Contains(prompt, "Known root-cause pattern priors") || !strings.Contains(prompt, "win_service_stop_event_not_signaled") {
		t.Fatalf("expected known pattern priors in planner prompt, got %s", prompt)
	}
}

func TestRootCauseGitHubIssueQueryAddsSafeFilters(t *testing.T) {
	query := rootCauseGitHubIssueQuery(`"SERVICE_CONTROL_STOP"`)
	for _, needle := range []string{"is:issue", "state:closed", "bug"} {
		if !strings.Contains(query, needle) {
			t.Fatalf("expected query to contain %q, got %q", needle, query)
		}
	}
}

func TestSearchRootCauseGitHubIssuesRejectsPullRequestQueries(t *testing.T) {
	_, err := searchRootCauseGitHubIssues(context.Background(), nil, rootCauseGitHubSearchConfig{
		Queries: []string{`repo:example/service is:pr bug`},
		Limit:   5,
	})
	if err == nil {
		t.Fatalf("expected pull request query rejection")
	}
	if !strings.Contains(err.Error(), "only supports issues") {
		t.Fatalf("expected issue-only error, got %v", err)
	}
}

func TestSearchRootCauseGitHubIssuesParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Fatalf("expected GitHub accept header, got %q", got)
		}
		_, _ = w.Write([]byte(`{
  "total_count": 1,
  "items": [
    {
      "number": 42,
      "title": "Fix service stop pending forever",
      "body": "Root cause was SERVICE_CONTROL_STOP not signaling stopEvent. Added regression test.",
      "state": "closed",
      "html_url": "https://github.com/example/service/issues/42",
      "labels": [{"name": "bug"}],
      "repository": {"full_name": "example/service"},
      "created_at": "2026-01-01T00:00:00Z",
      "updated_at": "2026-01-02T00:00:00Z",
      "closed_at": "2026-01-02T00:00:00Z"
    }
  ]
}`))
	}))
	defer server.Close()

	corpus, err := searchRootCauseGitHubIssues(context.Background(), server.Client(), rootCauseGitHubSearchConfig{
		APIURL:       server.URL,
		Queries:      []string{`"SERVICE_CONTROL_STOP"`},
		ProjectTypes: []string{"windows_user_service"},
		Limit:        5,
	})
	if err != nil {
		t.Fatalf("searchRootCauseGitHubIssues returned error: %v", err)
	}
	if len(corpus.Items) != 1 {
		t.Fatalf("expected one item, got %#v", corpus)
	}
	if corpus.Items[0].Repository != "example/service" || corpus.Items[0].Score == 0 {
		t.Fatalf("unexpected parsed issue: %#v", corpus.Items[0])
	}
	if len(corpus.ExecutedQueries) != 1 || len(corpus.QueryResults) != 1 {
		t.Fatalf("expected reproducibility query metadata, got %#v", corpus)
	}
	if corpus.QueryResults[0].Query != `"SERVICE_CONTROL_STOP"` || !strings.Contains(corpus.QueryResults[0].ExecutedQuery, "is:issue") {
		t.Fatalf("expected original and executed query metadata, got %#v", corpus.QueryResults[0])
	}
	if corpus.Items[0].Quality != "promotable" {
		t.Fatalf("expected promotable issue quality, got %#v", corpus.Items[0])
	}
}

func TestParseRootCauseGitHubSearchResponseSkipsPullRequests(t *testing.T) {
	items, err := parseRootCauseGitHubSearchResponse([]byte(`{
  "items": [
    {
      "number": 7,
      "title": "Fix implementation in PR",
      "body": "This is a pull request result from the issue search endpoint.",
      "state": "closed",
      "html_url": "https://github.com/example/service/pull/7",
      "labels": [{"name": "bug"}],
      "repository": {"full_name": "example/service"},
      "pull_request": {"url": "https://api.github.com/repos/example/service/pulls/7"}
    },
    {
      "number": 42,
      "title": "Fix service stop pending forever",
      "body": "Root cause was SERVICE_CONTROL_STOP not signaling stopEvent.",
      "state": "closed",
      "html_url": "https://github.com/example/service/issues/42",
      "labels": [{"name": "bug"}],
      "repository": {"full_name": "example/service"}
    }
  ]
}`))
	if err != nil {
		t.Fatalf("parseRootCauseGitHubSearchResponse returned error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected only the issue item, got %#v", items)
	}
	if items[0].Number != 42 {
		t.Fatalf("expected issue #42, got %#v", items[0])
	}
}

func TestNormalizeGitHubIssuesToRootCausePatternPack(t *testing.T) {
	corpus := RootCauseGitHubIssueCorpus{
		ProjectTypes: []string{"windows_user_service"},
		Items: []RootCauseGitHubIssue{{
			Repository: "example/service",
			Number:     42,
			Title:      "Fix service stop pending forever",
			Body:       "Root cause was SERVICE_CONTROL_STOP not signaling stopEvent. The fix updates SetServiceStatus to SERVICE_STOPPED.",
			State:      "closed",
			HTMLURL:    "https://github.com/example/service/issues/42",
			Labels:     []string{"bug"},
			Score:      72,
		}},
	}
	pack := normalizeGitHubIssuesToRootCausePatternPack(corpus, nil)
	if len(pack.Patterns) != 1 {
		t.Fatalf("expected one pattern, got %#v", pack)
	}
	pattern := pack.Patterns[0]
	if !containsString(pattern.ProjectTypes, "windows_user_service") {
		t.Fatalf("expected project type, got %#v", pattern.ProjectTypes)
	}
	if len(pattern.Sources) != 1 || pattern.Sources[0].Type != "github_issue" {
		t.Fatalf("expected github source, got %#v", pattern.Sources)
	}
	data, err := json.Marshal(pattern)
	if err != nil || !strings.Contains(string(data), "SERVICE_CONTROL_STOP") {
		t.Fatalf("expected code signal in pattern JSON, data=%s err=%v", string(data), err)
	}
}

func TestNormalizeGitHubIssuesInfersProjectTypeWhenMissing(t *testing.T) {
	corpus := RootCauseGitHubIssueCorpus{
		Items: []RootCauseGitHubIssue{{
			Repository: "example/service",
			Number:     43,
			Title:      "sc stop leaves service stuck in STOP_PENDING",
			Body:       "The fix handles SERVICE_CONTROL_STOP by signaling stopEvent and calling SetServiceStatus with SERVICE_STOPPED.",
			State:      "closed",
			HTMLURL:    "https://github.com/example/service/issues/43",
			Labels:     []string{"bug"},
			Score:      72,
		}},
	}
	pack := normalizeGitHubIssuesToRootCausePatternPack(corpus, nil)
	if len(pack.Patterns) != 1 {
		t.Fatalf("expected one pattern, got %#v", pack)
	}
	if !containsString(pack.Patterns[0].ProjectTypes, "windows_user_service") {
		t.Fatalf("expected inferred windows service type, got %#v", pack.Patterns[0].ProjectTypes)
	}
}

func TestNormalizeGitHubIssuesSkipsRejectedQualityIssues(t *testing.T) {
	corpus := RootCauseGitHubIssueCorpus{
		Items: []RootCauseGitHubIssue{{
			Repository: "example/service",
			Number:     45,
			Title:      "Question about service behavior",
			Body:       "How should this work?",
			State:      "open",
			HTMLURL:    "https://github.com/example/service/issues/45",
			Score:      1,
		}},
	}
	pack := normalizeGitHubIssuesToRootCausePatternPack(corpus, []string{"windows_user_service"})
	if len(pack.Patterns) != 0 {
		t.Fatalf("expected rejected issue to be skipped, got %#v", pack.Patterns)
	}
}

func TestValidateRootCausePatternPackFindsQualityIssues(t *testing.T) {
	pack := RootCausePatternPack{Patterns: []RootCausePattern{
		{
			ID:           "dup",
			Title:        "First",
			ProjectTypes: []string{"go_cli_agent"},
			RootCauses:   []string{"Review linked fix to confirm the exact root cause."},
			CodeSignals:  []string{"handler"},
		},
		{
			ID:    "dup",
			Title: "Second",
		},
	}}
	validation := validateRootCausePatternPack(pack)
	if len(validation.Errors) == 0 {
		t.Fatalf("expected validation errors, got %#v", validation)
	}
	if len(validation.Warnings) == 0 {
		t.Fatalf("expected validation warnings, got %#v", validation)
	}
}

func TestRootCauseGitHubPatternIDIsASCIIForUnicodeTitle(t *testing.T) {
	id := rootCauseGitHubPatternID(RootCauseGitHubIssue{
		Repository: "example/service",
		Number:     44,
		Title:      "서비스 종료가 가끔 멈춤",
	})
	if !utf8.ValidString(id) {
		t.Fatalf("expected valid UTF-8 id, got %q", id)
	}
	for _, r := range id {
		if r > 127 {
			t.Fatalf("expected ASCII-only id, got %q", id)
		}
	}
	if !strings.HasPrefix(id, "github_example_service_44_") {
		t.Fatalf("expected stable github id prefix, got %q", id)
	}
}

func TestMarkRootCausePatternMatchUsageRequiresExplicitPatternID(t *testing.T) {
	matches := []RootCausePatternMatch{{
		PatternID:      "win_service_stop_event_not_signaled",
		Title:          "Windows service stop handler does not signal worker shutdown",
		MatchedSignals: []string{"SERVICE_CONTROL_STOP"},
		RootCauses:     []string{"stop event is not signaled"},
	}}
	report := WorkerReport{
		RootCauseCandidates: []RootCauseCandidate{{
			Title:          "SERVICE_CONTROL_STOP does not signal stop event",
			CandidateChain: []string{"SERVICE_CONTROL_STOP is received", "stop event is not signaled"},
		}},
	}
	reviews := []ReviewDecision{{
		Status:              "approved",
		SymptomPossible:     "yes",
		SymptomCausality:    []string{"SERVICE_CONTROL_STOP can leave the process running when the stop event is not signaled"},
		CausalChainComplete: true,
	}}
	out := markRootCausePatternMatchUsage(matches, []WorkerReport{report}, reviews, nil)
	if len(out) != 1 {
		t.Fatalf("expected one pattern match, got %#v", out)
	}
	if out[0].UsedByWorker || out[0].AcceptedByReviewer {
		t.Fatalf("expected no usage without explicit pattern id, got %#v", out[0])
	}

	report.RootCauseCandidates[0].PatternIDs = []string{"win_service_stop_event_not_signaled"}
	out = markRootCausePatternMatchUsage(matches, []WorkerReport{report}, reviews, nil)
	if !out[0].UsedByWorker || !out[0].AcceptedByReviewer {
		t.Fatalf("expected explicit pattern id usage, got %#v", out[0])
	}
}

func TestRootCauseCandidateAuditSplitsExplicitAndInferredPatternIDs(t *testing.T) {
	matches := []RootCausePatternMatch{{
		PatternID:      "win_service_stop_event_not_signaled",
		Title:          "Windows service stop handler does not signal worker shutdown",
		MatchedSignals: []string{"SERVICE_CONTROL_STOP"},
		RootCauses:     []string{"stop event is not signaled"},
	}}
	candidate := RootCauseCandidate{
		Title:          "SERVICE_CONTROL_STOP does not signal stop event",
		CandidateChain: []string{"SERVICE_CONTROL_STOP is received", "stop event is not signaled"},
	}
	explicit := rootCauseExplicitPatternIDsForCandidate(candidate, matches)
	inferred := rootCauseInferredRelatedPatternIDsForCandidate(candidate, matches)
	if len(explicit) != 0 {
		t.Fatalf("expected no explicit pattern ids, got %#v", explicit)
	}
	if len(inferred) != 1 || inferred[0] != "win_service_stop_event_not_signaled" {
		t.Fatalf("expected inferred related pattern id, got %#v", inferred)
	}
	candidate.PatternIDs = []string{"win_service_stop_event_not_signaled"}
	explicit = rootCauseExplicitPatternIDsForCandidate(candidate, matches)
	inferred = rootCauseInferredRelatedPatternIDsForCandidate(candidate, matches)
	if len(explicit) != 1 || explicit[0] != "win_service_stop_event_not_signaled" {
		t.Fatalf("expected explicit pattern id, got %#v", explicit)
	}
	if len(inferred) != 0 {
		t.Fatalf("expected explicit id to be excluded from inferred ids, got %#v", inferred)
	}
}
