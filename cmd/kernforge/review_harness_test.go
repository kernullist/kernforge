package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func indexStringContaining(items []string, needle string) int {
	for i, item := range items {
		if strings.Contains(item, needle) {
			return i
		}
	}
	return -1
}

func TestReviewHarnessNoModelWritesTypedArtifact(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int main(){return 0;}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "main.cpp")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int main(){return 1;}\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:        "explicit_command",
		Target:         "change",
		Request:        "review current change",
		NoModel:        true,
		IncludeGitDiff: true,
	})
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if run.SchemaVersion != reviewSchemaVersion {
		t.Fatalf("unexpected schema version: %s", run.SchemaVersion)
	}
	if run.Target != reviewTargetChange || run.ChangeSet.Fingerprint == "" {
		t.Fatalf("review did not collect change set: %#v", run.ChangeSet)
	}
	if run.ArtifactRefs == nil || len(run.ArtifactRefs) < 2 {
		t.Fatalf("expected artifacts, got %#v", run.ArtifactRefs)
	}
	if _, err := os.Stat(filepath.Join(root, ".kernforge", "reviews", "latest.json")); err != nil {
		t.Fatalf("latest review json missing: %v", err)
	}
}

func TestSelectionReviewEvidenceUsesFullRangeNotPreviewTruncation(t *testing.T) {
	root := t.TempDir()
	var lines []string
	for i := 1; i <= 120; i++ {
		lines = append(lines, fmt.Sprintf("// line %03d %s", i, strings.Repeat("x", 48)))
	}
	lines[119] = "// line 120 tail sentinel review must keep this line"
	path := filepath.Join(root, "main.cpp")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	session := NewSession(root, "", "", "", "default")
	session.AddSelection(ViewerSelection{
		FilePath:  path,
		StartLine: 1,
		EndLine:   120,
	})
	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   session,
	}
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:         naturalReviewTrigger,
		Target:          reviewTargetSelection,
		Request:         "@main.cpp:1-120 리뷰해줘",
		NoModel:         true,
		IncludeGitDiff:  false,
		MaxContextChars: 60000,
	})
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if strings.Contains(run.Evidence.Text, "... (truncated)") {
		t.Fatalf("selection review evidence should not use UI preview truncation: %q", run.Evidence.Text)
	}
	if !strings.Contains(run.Evidence.Text, "line 120 tail sentinel") {
		t.Fatalf("selection review evidence lost the selected tail line: %q", run.Evidence.Text)
	}
	for _, finding := range run.Findings {
		if strings.Contains(finding.Title, "Changed files have no latest verification evidence") {
			t.Fatalf("read-only selection review should not be treated as a changed-file verification gap: %#v", finding)
		}
	}
	for _, cmd := range run.Gate.NextCommands {
		if cmd.ID == "verify" {
			t.Fatalf("read-only selection review should not recommend /verify --full: %#v", run.Gate.NextCommands)
		}
	}
}

func TestPostChangeReviewRunsAfterEditWhenEnabled(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int main()\n{\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "main.cpp")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "main.cpp"), []byte("int main()\n{\n    return 1;\n}\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	session := NewSession(root, "", "", "", "default")
	agent := &Agent{
		Config:    DefaultConfig(root),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}
	reviewed, needsRevision, feedback, fingerprint, err := agent.maybeRunPostChangeReview(context.Background(), "implement the change", "")
	if err != nil {
		t.Fatalf("post-change review: %v", err)
	}
	if !reviewed || needsRevision || fingerprint == "" {
		t.Fatalf("expected non-blocking automatic review, reviewed=%t needs=%t fingerprint=%q feedback=%q", reviewed, needsRevision, fingerprint, feedback)
	}
	if session.LastReviewRun == nil {
		t.Fatalf("expected session last review run")
	}
	if !session.LastReviewRun.AutoTriggered || session.LastReviewRun.Trigger != "post_change" {
		t.Fatalf("expected post_change auto review, got %#v", session.LastReviewRun)
	}
	if session.LastReviewRun.Target != reviewTargetChange {
		t.Fatalf("expected change target, got %#v", session.LastReviewRun)
	}
	if !strings.Contains(feedback, "Automatic post-change review completed") {
		t.Fatalf("expected user-visible review summary, got %q", feedback)
	}
	reviewedAgain, _, _, againFingerprint, err := agent.maybeRunPostChangeReview(context.Background(), "implement the change", fingerprint)
	if err != nil {
		t.Fatalf("repeat post-change review: %v", err)
	}
	if reviewedAgain || againFingerprint != fingerprint {
		t.Fatalf("expected same fingerprint to skip repeat review, reviewed=%t fingerprint=%q", reviewedAgain, againFingerprint)
	}
}

func TestPostChangeReviewSkipsAfterFailedEditAttemptWithoutSuccessfulChange(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "main.go")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")

	provider := &sideEffectProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{
				"path":    "main.go",
				"content": "package main\n\n// agent edit\n",
			}),
			{Message: Message{Role: "assistant", Text: "I stopped because main.go changed outside the agent."}},
		},
		beforeReturn: []func(){
			func() {
				if err := os.WriteFile(path, []byte("package main\n\n// user edit\n"), 0o644); err != nil {
					panic(err)
				}
			},
			nil,
		},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "model"
	cfg.Review.AutoAfterChange = boolPtr(true)
	session := NewSession(root, "scripted", "model", "", "default")
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-tx-prior",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "entry-1",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "main.go",
				Operation: "update",
				After: HarnessFileFingerprint{
					Path:   "main.go",
					Kind:   "file",
					Exists: true,
				},
			}},
		}},
	}}
	ws := Workspace{BaseRoot: root, Root: root}
	var progress []string
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}

	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "changed outside") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	for _, item := range progress {
		if strings.Contains(item, "automatic post-change review") || strings.Contains(item, "Automatic post-change review") {
			t.Fatalf("post-change review should not run after a failed edit attempt with no successful current change, progress=%#v", progress)
		}
	}
	if session.LastReviewRun != nil && strings.EqualFold(session.LastReviewRun.Trigger, "post_change") {
		t.Fatalf("post-change review should not be recorded after failed edit attempt, got %#v", session.LastReviewRun)
	}
}

func TestAutomaticReviewFeedbackKeepsModelOnInlineFindings(t *testing.T) {
	run := ReviewRun{
		ID:            "review-blocker",
		MachineStatus: "blocked",
		Result: ReviewResult{
			Verdict: "needs_revision",
			Summary: "review found blockers",
		},
		Gate: GateDecision{
			Verdict: reviewVerdictNeedsRevision,
		},
		ArtifactRefs: []string{
			".kernforge/reviews/review-blocker/review.md",
			".kernforge/reviews/review-blocker/review.json",
		},
		Findings: []ReviewFinding{{
			ID:                 "RF-001",
			Severity:           reviewSeverityHigh,
			Category:           "correctness",
			Title:              "Bad bounds check",
			Path:               "main.cpp",
			Evidence:           "index is used before validation",
			RequiredFix:        "validate index before use",
			TestRecommendation: "add an out-of-range index test",
			BlocksGate:         true,
		}},
	}
	post := formatPostChangeReviewFeedback(run, true)
	preWrite := formatPreWriteReviewFeedback(run)
	for _, feedback := range []string{post, preWrite} {
		for _, banned := range []string{".kernforge", "review.md", "review.json", "\nReport:", "Review ID", "Next Commands"} {
			if strings.Contains(feedback, banned) {
				t.Fatalf("automatic review feedback should not leak artifact/report text %q in %q", banned, feedback)
			}
		}
		for _, required := range []string{"Bad bounds check", "validate index before use", "add an out-of-range index test", "Do not read review artifact files"} {
			if !strings.Contains(feedback, required) {
				t.Fatalf("expected automatic review feedback to contain %q, got %q", required, feedback)
			}
		}
	}
}

func TestDistinctReviewModelProgressIsExplicit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{{
			Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: approved_with_warnings",
				"summary: review found a minor issue",
				"findings:",
				"- severity: medium",
				"  title: Missing focused test",
				"  category: test_gap",
				"  evidence: no test evidence was supplied",
				"  required_fix: run a focused test",
			}, "\n")},
		}},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(false)
	session := NewSession(root, "scripted", "main-model", "", "default")
	var progress []string
	agent := &Agent{
		Config:         cfg,
		Client:         &scriptedProviderClient{},
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             "explicit_command",
		Target:              reviewTargetChange,
		Request:             "review main.go",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if run.Result.WarningCount == 0 {
		t.Fatalf("expected warning finding from reviewer, got %#v", run.Result)
	}
	for _, needle := range []string{
		"Review model request: primary -> scripted / reviewer-model (main: scripted / main-model).",
		"Review model result: primary completed",
		"Review gate result:",
	} {
		if indexStringContaining(progress, needle) < 0 {
			t.Fatalf("expected progress to contain %q, got %#v", needle, progress)
		}
	}
}

func TestReviewModelRetriesOmittedFindingOutput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() { println(\"ok\") }\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	reviewer := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: 버퍼 검증 문제",
				"findings:",
				"- severity: high",
				"  category: correctness",
				"  title: 입력 검증이 누락됨...",
				"  evidence: main.go의 호출 흐름 일부가 생략되었습니다",
				"  impact: 잘못된 입력에서 크래시가 날 수 있습니다",
				"  required_fix: 검증을 추가하세요",
			}, "\n")}},
			{Message: Message{Role: "assistant", Text: strings.Join([]string{
				"REVIEW_RESULT",
				"verdict: needs_revision",
				"summary: 구체적인 수정 필요 finding이 있습니다",
				"findings:",
				"- severity: high",
				"  category: correctness",
				"  title: 리뷰 범위가 실제 입력 경계를 포함하지 않음",
				"  path: main.go",
				"  symbol: main",
				"  evidence: 제공된 파일은 외부 입력 없이 동작하지만 리뷰 요청은 버그 탐지를 요구하므로 실제 입력 검증 대상이 증거에 포함되지 않았습니다.",
				"  impact: 증거가 좁으면 잘못된 수정을 적용할 수 있습니다.",
				"  required_fix: 버그가 의심되는 함수의 실제 입력 경계 코드까지 선택해서 다시 리뷰하십시오.",
				"  test_recommendation: 문제 함수의 정상 입력과 비정상 입력을 포함하는 focused test를 추가하십시오.",
			}, "\n")}},
		},
	}
	cfg := DefaultConfig(root)
	cfg.Provider = "scripted"
	cfg.Model = "main-model"
	cfg.AutoLocale = boolPtr(true)
	session := NewSession(root, "scripted", "main-model", "", "default")
	var progress []string
	agent := &Agent{
		Config:         cfg,
		Client:         &scriptedProviderClient{},
		ReviewerClient: reviewer,
		ReviewerModel:  "reviewer-model",
		Workspace:      Workspace{BaseRoot: root, Root: root},
		Session:        session,
		Store:          NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}
	rt := agent.reviewHarnessRuntime(root)

	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:             naturalReviewTrigger,
		Target:              reviewTargetChange,
		Request:             "@main.go 리뷰해줘",
		Paths:               []string{path},
		IncludeFileContents: true,
	})
	if err != nil {
		t.Fatalf("runReviewHarness: %v", err)
	}
	if len(reviewer.requests) != 2 {
		t.Fatalf("expected strict retry request, got %d requests", len(reviewer.requests))
	}
	var modelFindings []ReviewFinding
	for _, finding := range run.Findings {
		if finding.Source == "model" {
			modelFindings = append(modelFindings, finding)
		}
	}
	if len(modelFindings) != 1 {
		t.Fatalf("expected retry model finding only, got %#v", run.Findings)
	}
	if reviewFindingIsOmittedOutputPlaceholder(modelFindings[0]) {
		t.Fatalf("omitted placeholder should be replaced by retry finding: %#v", modelFindings[0])
	}
	if run.Result.ModelQuality != reviewModelQualityUsable {
		t.Fatalf("expected usable retry quality, got %#v", run.Result)
	}
	if !strings.Contains(reviewer.requests[1].Messages[0].Text, "이전 리뷰 출력은") {
		t.Fatalf("retry prompt should explain the rejected output, got %q", reviewer.requests[1].Messages[0].Text)
	}
	if indexStringContaining(progress, "엄격 리뷰로 재시도합니다") < 0 {
		t.Fatalf("expected retry progress message, got %#v", progress)
	}
}

func TestAgentRunsPreWriteReviewBeforePreviewAndWrite(t *testing.T) {
	root := t.TempDir()
	runTestGit(t, root, "init")
	path := filepath.Join(root, "main.go")
	before := "package main\n\nfunc main() {}\n"
	after := "package main\n\nfunc main() { println(\"ok\") }\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runTestGit(t, root, "add", "main.go")
	runTestGit(t, root, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-m", "init")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("write_file", map[string]any{"path": "main.go", "content": after}),
			{Message: Message{Role: "assistant", Text: "main.go updated."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root}
	var progress []string
	agent := &Agent{
		Config:  DefaultConfig(root),
		Client:  provider,
		Session: session,
		Store:   store,
		EmitProgress: func(message string) {
			progress = append(progress, message)
		},
	}
	var events []string
	ws.ReviewEdit = func(ctx context.Context, preview EditPreview) error {
		events = append(events, "review")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read before pre-write review: %v", err)
		}
		if string(data) != before {
			t.Fatalf("expected file to remain unchanged during pre-write review, got %q", string(data))
		}
		return agent.reviewProposedEdit(ctx, preview)
	}
	ws.PreviewEdit = func(preview EditPreview) (bool, error) {
		events = append(events, "preview")
		if session.LastReviewRun == nil || session.LastReviewRun.Trigger != "pre_write" {
			t.Fatalf("expected pre-write review before diff preview, got %#v", session.LastReviewRun)
		}
		return true, nil
	}
	agent.Workspace = ws
	agent.Tools = NewToolRegistry(NewWriteFileTool(ws))
	reply, err := agent.Reply(context.Background(), "update main.go")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "updated") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if strings.Join(events, ",") != "review,preview" {
		t.Fatalf("expected pre-write review before preview, got %#v", events)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after write: %v", err)
	}
	if string(data) != after {
		t.Fatalf("expected write after pre-write review, got %q", string(data))
	}
	reviewProgress := indexStringContaining(progress, "자동 쓰기 전 리뷰가 완료")
	previewProgress := indexStringContaining(progress, "Edit applied. Checking follow-up steps")
	if reviewProgress < 0 {
		t.Fatalf("expected user-visible pre-write review progress, got %#v", progress)
	}
	if previewProgress < 0 {
		t.Fatalf("expected post-review follow-up progress, got %#v", progress)
	}
	if reviewProgress > previewProgress {
		t.Fatalf("expected review progress before follow-up checks, got %#v", progress)
	}
}

func TestPostChangeReviewCanBeDisabled(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Review.AutoAfterChange = boolPtr(false)
	agent := &Agent{
		Config:    cfg,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   NewSession(root, "", "", "", "default"),
	}
	reviewed, _, _, _, err := agent.maybeRunPostChangeReview(context.Background(), "implement", "")
	if err != nil {
		t.Fatalf("disabled post-change review: %v", err)
	}
	if reviewed {
		t.Fatalf("expected disabled auto review to skip")
	}
}

func TestReviewHarnessRedactsSensitiveEvidence(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{BaseRoot: root, Root: root},
		session:   NewSession(root, "", "", "", "default"),
	}
	run, err := runReviewHarness(context.Background(), rt, ReviewHarnessOptions{
		Trigger:      "explicit_mcp",
		Target:       "change",
		Request:      "review supplied diff with token sk-abcdefghijklmnopqrstuvwxyz123456",
		ProvidedDiff: "diff --git a/.env b/.env\n+OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456",
		NoModel:      true,
	})
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if !run.Redaction.Redacted {
		t.Fatalf("expected redaction report: %#v", run.Redaction)
	}
	data, err := os.ReadFile(filepath.Join(root, ".kernforge", "reviews", "latest.json"))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if strings.Contains(string(data), "sk-abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("secret leaked into review artifact: %s", string(data))
	}
	if !strings.Contains(string(data), "[REDACTED:") {
		t.Fatalf("redaction marker missing from artifact: %s", string(data))
	}
}

func TestReviewModelParserKeepsMultipleStructuredFindings(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"findings:",
		"- severity: high",
		"  title: Missing bounds check",
		"  category: security",
		"  path: driver/ioctl.cpp",
		"  evidence: IOCTL length is trusted",
		"  impact: kernel read may exceed the user buffer",
		"  required_fix: validate the length before copy",
		"  test_recommendation: add short-buffer IOCTL test",
		"- severity: medium",
		"  title: Missing regression test",
		"  category: test_gap",
		"  evidence: no verification evidence was supplied",
		"  required_fix: run /verify --full",
	}, "\n")
	findings, quality := parseModelReviewFindings(raw, "security_reviewer")
	if quality != reviewModelQualityUsable {
		t.Fatalf("unexpected quality: %s", quality)
	}
	if len(findings) != 2 {
		t.Fatalf("expected two findings, got %#v", findings)
	}
	if findings[0].Title != "Missing bounds check" || findings[1].Title != "Missing regression test" {
		t.Fatalf("parser merged or lost findings: %#v", findings)
	}
	if findings[0].TestRecommendation != "add short-buffer IOCTL test" {
		t.Fatalf("test recommendation was not parsed: %#v", findings[0])
	}
}

func TestReviewModelParserKeepsLongKoreanFallbackFindingText(t *testing.T) {
	raw := `- high: "JPEG 데이터가 잘린 상태로 호출자 버퍼에 기록될 수 있습니다. 이후 성공 경로에서 이 값을 정상 JPEG로 취급하면 호출자는 손상된 데이터를 저장하거나 전송하게 됩니다. 성공 처리는 전체 JPEG가 복사된 경우에만 허용해야 합니다."`
	findings, quality := parseModelReviewFindings(raw, "primary_reviewer")
	if quality != reviewModelQualityUsable || len(findings) != 1 {
		t.Fatalf("expected one usable finding, quality=%s findings=%#v", quality, findings)
	}
	title := findings[0].Title
	if strings.Contains(title, "...") || strings.ContainsRune(title, '\uFFFD') || !utf8.ValidString(title) {
		t.Fatalf("fallback finding title should not be truncated or split: %q", title)
	}
	if strings.HasPrefix(title, "\"") || strings.HasSuffix(title, "\"") {
		t.Fatalf("fallback finding title should trim wrapper quotes: %q", title)
	}
	if !strings.Contains(title, "성공 처리는 전체 JPEG가 복사된 경우에만 허용해야 합니다") {
		t.Fatalf("fallback finding title lost the important tail text: %q", title)
	}
}

func TestReviewModelParserRejectsOmittedFindingText(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"verdict: needs_revision",
		"summary: omitted output should be rejected",
		"findings:",
		"- severity: high",
		"  category: correctness",
		"  title: ...",
		"  evidence: omitted for brevity",
		"  impact: ...",
		"  required_fix: ...",
	}, "\n")

	findings, quality := parseModelReviewFindings(raw, "primary_reviewer")
	if quality != reviewModelQualityWeak || len(findings) != 1 {
		t.Fatalf("expected one weak placeholder finding, quality=%s findings=%#v", quality, findings)
	}
	finding := findings[0]
	if finding.Title != "Reviewer output omitted part of a finding" ||
		finding.Category != "evidence_gap" ||
		finding.Severity != reviewSeverityMedium ||
		finding.BlocksGate {
		t.Fatalf("omitted finding should become non-blocking quality finding, got %#v", finding)
	}
	for _, value := range []string{finding.Title, finding.Evidence, finding.Impact, finding.RequiredFix} {
		if strings.Contains(value, "...") || strings.Contains(value, "…") || strings.ContainsRune(value, '\uFFFD') {
			t.Fatalf("placeholder should not render omitted text markers, got %#v", finding)
		}
	}
}

func TestReviewModelParserSalvagesOmittedFindingText(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"verdict: needs_revision",
		"summary: one field contains an omission marker",
		"findings:",
		"- severity: high",
		"  category: correctness",
		"  title: Buffer validation is missing...",
		"  evidence: DataCount is trusted before indexing",
		"  impact: out-of-bounds read",
		"  required_fix: validate DataCount",
	}, "\n")

	findings, quality := parseModelReviewFindings(raw, "primary_reviewer")
	if quality != reviewModelQualityUsable || len(findings) != 1 {
		t.Fatalf("expected one salvaged finding, quality=%s findings=%#v", quality, findings)
	}
	finding := findings[0]
	if reviewFindingIsOmittedOutputPlaceholder(finding) ||
		finding.Title != "Buffer validation is missing" ||
		finding.Quality != reviewFindingQualityPartial ||
		finding.Severity != reviewSeverityMedium ||
		finding.BlocksGate {
		t.Fatalf("expected non-blocking salvaged finding, got %#v", finding)
	}
	for _, value := range []string{finding.Title, finding.Evidence, finding.Impact, finding.RequiredFix} {
		if strings.Contains(value, "...") || strings.Contains(value, "…") || strings.ContainsRune(value, '\uFFFD') {
			t.Fatalf("salvaged finding should not render omission markers, got %#v", finding)
		}
	}
}

func TestReviewModelParserKeepsUnstructuredNoBlockingSummary(t *testing.T) {
	raw := "No blocking findings. Add a regression test for the new branch."
	findings, quality := parseModelReviewFindings(raw, "primary_reviewer")
	if quality != reviewModelQualityUsable || len(findings) != 1 {
		t.Fatalf("expected one usable info finding, quality=%s findings=%#v", quality, findings)
	}
	finding := findings[0]
	if finding.Severity != reviewSeverityInfo ||
		finding.Title != raw ||
		finding.BlocksGate {
		t.Fatalf("unexpected unstructured approval finding: %#v", finding)
	}
}

func TestReviewModelParserLocalizesAndDeduplicatesOmittedFindings(t *testing.T) {
	raw := strings.Join([]string{
		"REVIEW_RESULT",
		"verdict: needs_revision",
		"summary: omitted output should be rejected",
		"findings:",
		"- severity: high",
		"  category: correctness",
		"  title: ...",
		"  evidence: omitted for brevity",
		"  impact: ...",
		"  required_fix: ...",
		"- severity: high",
		"  category: security",
		"  title: ...",
		"  evidence: omitted for brevity",
		"  impact: ...",
		"  required_fix: ...",
	}, "\n")

	findings, quality := parseModelReviewFindingsForLanguage(raw, "primary_reviewer", true)
	if quality != reviewModelQualityWeak || len(findings) != 1 {
		t.Fatalf("expected one localized weak placeholder finding, quality=%s findings=%#v", quality, findings)
	}
	finding := findings[0]
	for _, value := range []string{finding.Title, finding.Evidence, finding.Impact, finding.RequiredFix} {
		if !textContainsHangul(value) {
			t.Fatalf("expected Korean placeholder fields, got %#v", finding)
		}
		if strings.Contains(value, "...") || strings.Contains(value, "…") {
			t.Fatalf("placeholder should not include omitted markers, got %#v", finding)
		}
	}
	if finding.Title != "리뷰어 출력이 finding 일부를 생략함" {
		t.Fatalf("unexpected localized placeholder title: %#v", finding)
	}
}

func TestCompactPromptSectionDoesNotSplitKoreanRune(t *testing.T) {
	got := compactPromptSection("가나다라마바사아자차카타파하", 19)
	if !utf8.ValidString(got) || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("compactPromptSection split a multibyte rune: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated string to keep ellipsis, got %q", got)
	}
}

func TestSecurityHighFindingBlocksGate(t *testing.T) {
	run := ReviewRun{
		PolicyPacks: []string{"windows_kernel_driver"},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Severity:    reviewSeverityHigh,
			Category:    "security",
			Title:       "Bypassable trust boundary",
			Evidence:    "security-sensitive path",
			RequiredFix: "tighten the boundary",
		}},
	}
	gate := evaluateReviewGate(run)
	if gate.Verdict != reviewVerdictNeedsRevision {
		t.Fatalf("high security finding should block, got %#v", gate)
	}
	if len(gate.BlockingFindings) != 1 || gate.BlockingFindings[0] != "RF-001" {
		t.Fatalf("expected high security finding as blocker, got %#v", gate.BlockingFindings)
	}
	run.Gate = gate
	rendered := renderReviewRunMarkdown(run)
	if strings.Contains(rendered, "## Warnings") {
		t.Fatalf("policy-blocking security finding should not be duplicated as warning: %s", rendered)
	}
}

func TestSecuritySensitiveFallbackModelsProduceGuidance(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	run := ReviewRun{
		Mode:        reviewModeSecurityHardening,
		Flow:        "security_review",
		PolicyPacks: []string{"windows_kernel_driver", "anti_cheat_telemetry"},
		Objective:   "kernel anti-cheat false positive review",
	}
	plan := planReviewModels(cfg, run)
	if !stringSliceContainsCI(plan.MissingRoles, "security_reviewer") {
		t.Fatalf("expected missing dedicated security role, got %#v", plan)
	}
	if !stringSliceContainsCI(plan.MissingRoles, "false_positive_reviewer") {
		t.Fatalf("expected missing dedicated false-positive role, got %#v", plan)
	}
	if len(plan.AssignedModels) == 0 {
		t.Fatalf("fallback model should still be assigned for execution: %#v", plan)
	}
	gate := evaluateReviewGate(ReviewRun{ModelPlan: plan})
	if len(gate.NextCommands) < 2 {
		t.Fatalf("expected model setup next commands, got %#v", gate.NextCommands)
	}
}

func TestSecurityServiceReviewDoesNotRequireFalsePositiveReviewer(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	run := ReviewRun{
		Mode:        reviewModeSecurityHardening,
		Flow:        "security_review",
		PolicyPacks: []string{"windows_kernel_driver"},
		Objective:   "TavernWorker service install/start bug fix",
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"Tavern/TavernWorker/TavernKernelManager.cpp"},
		},
	}
	plan := planReviewModels(cfg, run)
	if !stringSliceContainsCI(plan.RequiredRoles, "security_reviewer") {
		t.Fatalf("expected security reviewer, got %#v", plan)
	}
	if stringSliceContainsCI(plan.RequiredRoles, "false_positive_reviewer") ||
		stringSliceContainsCI(plan.MissingRoles, "false_positive_reviewer") {
		t.Fatalf("service install review should not require false-positive reviewer, got %#v", plan)
	}
}

func TestBaseSecurityPolicyDoesNotForceSecurityReviewer(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	run := ReviewRun{
		Mode:        reviewModeGeneralChange,
		Flow:        "selection_review",
		PolicyPacks: []string{"base_correctness", "base_security", "base_stability"},
		Objective:   "@src/foo.cpp 리뷰해줘",
	}
	if reviewRunSecuritySensitive(run) {
		t.Fatalf("base_security alone should not make a generic review security-sensitive")
	}
	plan := planReviewModels(cfg, run)
	if stringSliceContainsCI(plan.OptionalRoles, "security_reviewer") ||
		stringSliceContainsCI(plan.MissingRoles, "security_reviewer") {
		t.Fatalf("generic review should not recommend security reviewer from base_security alone: %#v", plan)
	}
}

func TestApprovedWarningsRecommendRepairWhenActionable(t *testing.T) {
	run := ReviewRun{
		Mode:        reviewModeGeneralChange,
		Flow:        "selection_review",
		PolicyPacks: []string{"base_correctness", "base_security"},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Source:      "model",
			Severity:    reviewSeverityMedium,
			Category:    "stability",
			Title:       "Handle leak on failure path",
			Path:        "TaverDartManager.cpp",
			Evidence:    "CreateEvent can succeed before a later failure.",
			Impact:      "Repeated failures can leak handles.",
			RequiredFix: "Close acquired handles on every failure path.",
			Quality:     reviewFindingQualityComplete,
		}},
	}
	gate := evaluateReviewGate(run)
	if gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("expected approved_with_warnings, got %#v", gate)
	}
	if !reviewNextCommandsContainID(gate.NextCommands, "repair-warnings") {
		t.Fatalf("expected actionable warning repair command, got %#v", gate.NextCommands)
	}
	if !reviewNextCommandsContainID(gate.NextCommands, "completion-audit") {
		t.Fatalf("expected completion audit to remain available, got %#v", gate.NextCommands)
	}
}

func reviewNextCommandsContainID(commands []ReviewNextCommand, id string) bool {
	for _, command := range commands {
		if strings.EqualFold(command.ID, id) {
			return true
		}
	}
	return false
}

func TestReviewModelsCommandShortFormConfiguresRole(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		cfg:     cfg,
		session: &Session{Provider: "openai", Model: "gpt-main", PermissionMode: "default"},
	}

	if err := rt.handleReviewModelsCommand("security openai gpt-5.4"); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	roleCfg := rt.cfg.Review.RoleModels["security_reviewer"]
	if roleCfg.Provider != "openai" || roleCfg.Model != "gpt-5.4" {
		t.Fatalf("unexpected security reviewer config: %#v", roleCfg)
	}
	if !strings.Contains(out.String(), "Review security set") {
		t.Fatalf("expected success output, got %q", out.String())
	}
}

func TestReviewModelsCommandShortFormPersistsRole(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "deepseek"
	cfg.Model = "deepseek-v4-pro"
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		cfg:     cfg,
		session: &Session{Provider: "deepseek", Model: "deepseek-v4-pro", PermissionMode: "default"},
	}

	if err := rt.handleReviewModelsCommand("primary deepseek deepseek-v4-pro low"); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	roleCfg := loaded.Review.RoleModels["primary_reviewer"]
	if roleCfg.Provider != "deepseek" || roleCfg.Model != "deepseek-v4-pro" || roleCfg.ReasoningEffort != "low" {
		t.Fatalf("expected review primary model to persist, got %#v", loaded.Review.RoleModels)
	}
}

func TestReviewModelsClearPersistsLastRoleRemoval(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	cfg.Review.RoleModels = map[string]ReviewModelConfig{
		"primary_reviewer": {
			Provider:        "codex-cli",
			Model:           "gpt-5.5",
			ReasoningEffort: "low",
		},
	}
	if err := SaveUserConfig(cfg); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		cfg:     cfg,
		session: &Session{Provider: "openai", Model: "gpt-main", PermissionMode: "default"},
	}

	if err := rt.handleReviewModelsCommand("clear primary"); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(loaded.Review.RoleModels) != 0 {
		t.Fatalf("expected /review models clear to persist last role removal, got %#v", loaded.Review.RoleModels)
	}
}

func TestReviewModelsStatusExplainsRolesAndSettings(t *testing.T) {
	var out bytes.Buffer
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
		cfg:    cfg,
	}

	if err := rt.handleReviewModelsCommand("status"); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	rendered := out.String()
	for _, needle := range []string{
		"Automatic Review",
		"after_change",
		"review code-changing agent edits by default",
		"Reviewer Roles",
		"security",
		"security boundary",
		"follows main: openai-api / gpt-main",
		"Direct form: /review models security openai-api gpt-5.4",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected status output to contain %q, got %q", needle, rendered)
		}
	}
	for _, internal := range []string{"auto_after_change", "security_reviewer"} {
		if strings.Contains(rendered, internal) {
			t.Fatalf("status output should hide internal key %q, got %q", internal, rendered)
		}
	}
}

func TestReviewModelsCommandInteractiveUsesNumberedChoices(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	cfg.APIKey = "test-key"
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("2\n3\n1\n")),
		writer:      &out,
		ui:          UI{},
		interactive: true,
		cfg:         cfg,
		session:     &Session{Provider: "openai", Model: "gpt-main", PermissionMode: "default"},
	}

	if err := rt.handleReviewModelsCommand(""); err != nil {
		t.Fatalf("handleReviewModelsCommand: %v", err)
	}
	roleCfg := rt.cfg.Review.RoleModels["security_reviewer"]
	if roleCfg.Provider != "openai" || roleCfg.Model != "gpt-5.4" {
		t.Fatalf("unexpected interactive security reviewer config: %#v", roleCfg)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Review Model Role") || !strings.Contains(rendered, "2. security") {
		t.Fatalf("expected numbered role choices, got %q", rendered)
	}
}

func TestReviewModelPromptFollowsKoreanObjectiveLanguage(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.AutoLocale = boolPtr(false)
	run := ReviewRun{
		ID:        "review-1",
		Objective: "@Tavern/TavernWorker/Txr.cpp:20-57 리뷰해줘",
	}

	system := reviewModelSystemPrompt(cfg, run, "primary_reviewer")
	if !strings.Contains(system, "human-readable narrative fields in Korean") {
		t.Fatalf("expected Korean narrative guidance in system prompt, got %q", system)
	}
	if !strings.Contains(system, "Never use ellipses") {
		t.Fatalf("expected no-ellipsis guidance in system prompt, got %q", system)
	}
	prompt := buildReviewModelPrompt(cfg, run, "primary_reviewer")
	if !strings.Contains(prompt, "Write narrative field values in Korean") {
		t.Fatalf("expected Korean narrative guidance in review prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Do not use ellipses") {
		t.Fatalf("expected no-ellipsis guidance in review prompt, got %q", prompt)
	}
}

func TestPrintReviewRunNeedsRevisionUsesWarnAndKoreanLabels(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
		cfg:    Config{AutoLocale: boolPtr(false)},
	}
	run := ReviewRun{
		ID:        "review-1",
		Objective: "@Tavern/TavernWorker/Txr.cpp:20-57 리뷰해줘",
		Target:    reviewTargetSelection,
		Mode:      reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict:          reviewVerdictNeedsRevision,
			BlockingFindings: []string{"RF-001"},
		},
		Findings: []ReviewFinding{{
			ID:          "RF-001",
			Severity:    reviewSeverityHigh,
			Title:       "범위 검사가 누락됨",
			RequiredFix: "읽기 전에 버퍼 크기를 검증하세요.",
			BlocksGate:  true,
		}},
	}

	rt.printReviewRun(run)
	rendered := out.String()
	if strings.Contains(rendered, "ERROR") {
		t.Fatalf("needs_revision review should not be rendered as ERROR, got %q", rendered)
	}
	for _, needle := range []string{"WARN", "리뷰 review-1: needs_revision", "- 대상: selection", "수정: 읽기 전에 버퍼 크기를 검증하세요."} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected review output to contain %q, got %q", needle, rendered)
		}
	}
}

func TestPrintReviewRunApprovedWithWarningsShowsWarningFindings(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
		cfg:    Config{AutoLocale: boolPtr(false)},
	}
	run := ReviewRun{
		ID:        "review-1",
		Objective: "@Tavern/TavernMaster/TavernMaster.cpp:869-996 리뷰해줘",
		Target:    reviewTargetSelection,
		Mode:      reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001", "RF-002"},
		},
		Findings: []ReviewFinding{
			{
				ID:                 "RF-001",
				Severity:           reviewSeverityMedium,
				Title:              "상태 복구 경로가 불명확함",
				Evidence:           "실패 분기에서 복구 상태가 기록되지 않습니다.",
				Impact:             "호출자가 성공/실패 상태를 잘못 판단할 수 있습니다.",
				RequiredFix:        "실패 시 상태를 명시적으로 복구하세요.",
				TestRecommendation: "복구 경로 회귀 테스트",
			},
			{
				ID:          "RF-002",
				Severity:    reviewSeverityHigh,
				Title:       "동시 실행 경계가 검증되지 않음",
				RequiredFix: "공유 상태 접근 전후의 동기화 경계를 검증하세요.",
			},
		},
	}

	rt.printReviewRun(run)
	rendered := out.String()
	if strings.Contains(rendered, "ERROR") {
		t.Fatalf("approved_with_warnings review should not be rendered as ERROR, got %q", rendered)
	}
	for _, needle := range []string{
		"WARN",
		"리뷰 review-1: approved_with_warnings",
		"경고:",
		"[RF-001] medium: 상태 복구 경로가 불명확함",
		"근거: 실패 분기에서 복구 상태가 기록되지 않습니다.",
		"영향: 호출자가 성공/실패 상태를 잘못 판단할 수 있습니다.",
		"권장 조치: 실패 시 상태를 명시적으로 복구하세요.",
		"테스트: 복구 경로 회귀 테스트",
		"[RF-002] high: 동시 실행 경계가 검증되지 않음",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected review output to contain %q, got %q", needle, rendered)
		}
	}
}

func TestPrintReviewRunExplainsNextCommands(t *testing.T) {
	run := ReviewRun{
		ID:        "review-next",
		Objective: "@Tavern/TavernMaster/CaptureHelper.cpp:45-145 리뷰해줘",
		Target:    reviewTargetSelection,
		Mode:      reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict: reviewVerdictNeedsRevision,
			NextCommands: []ReviewNextCommand{
				{
					ID:         "verify",
					Command:    "/verify --full",
					Reason:     "changed files have no latest verification evidence",
					Safety:     "safe_local",
					When:       "before completion or git write",
					ClientHint: "Run verification, then repeat /review.",
				},
				{
					ID:         "repair",
					Command:    "/continuity continue from review",
					Reason:     "blocking findings need a focused repair pass",
					Safety:     "safe_local",
					When:       "after reading review findings",
					ClientHint: "Use the repair prompt in the review artifact.",
				},
			},
		},
	}

	rendered := renderReviewCLIResult(Config{AutoLocale: boolPtr(false)}, run)
	for _, needle := range []string{
		"다음 명령:",
		"- /verify --full\n  이유: 변경된 파일에 대한 최신 빌드/테스트 근거가 없습니다.",
		"  시점: 완료 선언 또는 git write 전에",
		"  안전성: safe_local",
		"  실행 방법: `/verify --full`로 검증을 실행한 뒤 `/review`를 다시 실행해 최신 근거를 붙이세요.",
		"- /continuity continue from review\n  이유: 차단 finding이 있어서 위 RF 항목을 기준으로 수정 작업을 이어가야 합니다.",
		"  실행 방법: 이 명령을 실행하거나 자연어로 `수정해줘`라고 이어가면 최신 리뷰 finding을 기준으로 repair 흐름을 시작합니다.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected next-command output to contain %q, got %q", needle, rendered)
		}
	}
}

func TestReviewMarkdownKeepsLongFindingTextAndExplainsNextCommands(t *testing.T) {
	longTitle := "JPEG 데이터가 잘린 상태로 호출자 버퍼에 기록될 수 있습니다. 이후 성공 경로에서 이 값을 정상 JPEG로 취급하면 호출자는 손상된 데이터를 저장하거나 전송하게 됩니다. 성공 처리는 전체 JPEG가 복사된 경우에만 허용해야 합니다."
	run := ReviewRun{
		ID:            "review-report",
		SchemaVersion: reviewSchemaVersion,
		Objective:     "@Tavern/TavernMaster/DirectCapture.cpp:252-413 리뷰해줘",
		Target:        reviewTargetSelection,
		Mode:          reviewModeGeneralChange,
		Gate: GateDecision{
			Verdict:         reviewVerdictApprovedWithWarnings,
			WarningFindings: []string{"RF-001"},
			NextCommands: []ReviewNextCommand{{
				ID:         "verify",
				Command:    "/verify --full",
				Reason:     "changed files have no latest verification evidence",
				When:       "before completion or git write",
				Safety:     "safe_local",
				ClientHint: "Run verification, then repeat /review.",
			}},
		},
		Findings: []ReviewFinding{{
			ID:                 "RF-001",
			Severity:           reviewSeverityMedium,
			Category:           "correctness",
			Title:              longTitle,
			Evidence:           longTitle,
			Impact:             "Caller can persist or transmit corrupt image data.",
			RequiredFix:        "Return failure when the caller buffer is too small, or define an explicit size-query contract.",
			TestRecommendation: "Cover exact-size, one-byte-short, and zero-size output buffers.",
		}},
	}

	rendered := renderReviewRunMarkdown(run)
	if strings.Contains(rendered, "호출자는 손상된 데이터를 저장하거나 전...") ||
		strings.ContainsRune(rendered, '\uFFFD') {
		t.Fatalf("review markdown should not contain truncated or split finding text: %q", rendered)
	}
	for _, needle := range []string{
		longTitle,
		"- Required fix: Return failure when the caller buffer is too small",
		"## Next Commands",
		"- `/verify --full`",
		"  - Why: changed files have no latest verification evidence",
		"  - Action: Run verification, then repeat /review.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected markdown report to contain %q, got %q", needle, rendered)
		}
	}
}

func TestLowSeverityFindingCountsAsGateWarningAndIsRendered(t *testing.T) {
	run := ReviewRun{
		ID:        "review-low",
		Objective: "@Tavern/TavernMaster/CaptureHelper.cpp:45-145 리뷰해줘",
		Target:    reviewTargetSelection,
		Mode:      reviewModeGeneralChange,
		Findings: []ReviewFinding{
			{
				ID:          "RF-001",
				Severity:    reviewSeverityLow,
				Title:       "정리 가능한 중복 검증 경로",
				RequiredFix: "중복 검증 경로를 하나로 합치십시오.",
			},
			{
				ID:       "RF-002",
				Severity: reviewSeverityInfo,
				Title:    "참고용 기록",
			},
		},
	}
	run.Gate = evaluateReviewGate(run)
	run.finalizeStatus(false)

	if run.Gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("low finding should keep the gate in approved_with_warnings, got %#v", run.Gate)
	}
	if len(run.Gate.WarningFindings) != 1 || run.Gate.WarningFindings[0] != "RF-001" {
		t.Fatalf("expected exactly one warning finding, got %#v", run.Gate.WarningFindings)
	}
	if run.Result.WarningCount != len(run.Gate.WarningFindings) {
		t.Fatalf("result and gate warning counts diverged: result=%d gate=%d", run.Result.WarningCount, len(run.Gate.WarningFindings))
	}
	rendered := renderReviewCLIResult(Config{AutoLocale: boolPtr(false)}, run)
	for _, needle := range []string{
		"발견: 2 blocker=0 warning=1",
		"경고:",
		"[RF-001] low: 정리 가능한 중복 검증 경로",
		"권장 조치: 중복 검증 경로를 하나로 합치십시오.",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected low warning output to contain %q, got %q", needle, rendered)
		}
	}
	if strings.Contains(rendered, "참고용 기록") {
		t.Fatalf("info findings should stay out of concise warning output, got %q", rendered)
	}
}

func TestReviewFinalizeStatusResetsCounts(t *testing.T) {
	run := ReviewRun{
		Gate: GateDecision{Verdict: reviewVerdictNeedsRevision},
		Findings: []ReviewFinding{{
			ID:         "RF-001",
			Severity:   reviewSeverityBlocker,
			Title:      "blocker",
			BlocksGate: true,
		}},
	}
	run.finalizeStatus(false)
	run.finalizeStatus(false)
	if run.Result.BlockingCount != 1 || run.Result.WarningCount != 0 {
		t.Fatalf("finalizeStatus should not accumulate counts, got %#v", run.Result)
	}
}

func TestCompletionAuditFlagsUnreviewedChangedFilesAsStale(t *testing.T) {
	artifact := &CompletionAuditArtifact{ChangedFiles: []string{"cmd/kernforge/review_harness.go", "cmd/kernforge/new_file.go"}}
	review := ReviewRun{
		SchemaVersion: reviewSchemaVersion,
		ChangeSet: ReviewChangeSet{
			ChangedPaths: []string{"cmd/kernforge/review_harness.go"},
		},
	}
	reason := completionAuditReviewFreshnessIssue(t.TempDir(), review, artifact)
	if !strings.Contains(reason, "cmd/kernforge/new_file.go") {
		t.Fatalf("expected stale reason for unreviewed file, got %q", reason)
	}
}

func stringSliceContainsCI(items []string, want string) bool {
	for _, item := range items {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}
