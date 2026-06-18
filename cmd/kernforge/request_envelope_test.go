package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestEnvelopeClassifiesReviewOnlyKoreanAsReadOnly(t *testing.T) {
	envelope := buildRequestEnvelope("RuntimeManager.cpp 코드 리뷰해줘")
	if envelope.PrimaryClass != RequestClassReview {
		t.Fatalf("expected review primary class, got %#v", envelope)
	}
	if !envelope.ReadOnlyAnalysis {
		t.Fatalf("expected review-only request to be read-only, got %#v", envelope)
	}
	if envelope.AllowsFileMutation || envelope.ExplicitEditRequest {
		t.Fatalf("review-only request must not allow file mutation, got %#v", envelope)
	}
	plan := requestEnvelopeTestAgent(t, requestEnvelopeTestRegistry()).buildTurnToolExposurePlanForEnvelope(nil, envelope, false, false, false, false, false, false)
	if !plan.toolDisabled("apply_patch") {
		t.Fatalf("review-only request must not expose apply_patch")
	}
	if plan.toolDisabled("read_file") {
		t.Fatalf("review-only request should keep read_file available")
	}
}

func TestRequestEnvelopeClassifiesPlanOnlyAsReadOnly(t *testing.T) {
	envelope := buildRequestEnvelope("Codex repo와 비교해서 개선 방향을 먼저 정하자")
	if envelope.PrimaryClass != RequestClassPlan {
		t.Fatalf("expected plan primary class, got %#v", envelope)
	}
	if !envelope.ReadOnlyAnalysis {
		t.Fatalf("plan-only request should be read-only, got %#v", envelope)
	}
	if envelope.AllowsFileMutation || envelope.AllowsGitMutation {
		t.Fatalf("plan-only request must not allow mutations, got %#v", envelope)
	}
}

func TestRequestEnvelopeClassifiesAnswerOnlyComparisonAsReadOnly(t *testing.T) {
	envelope := buildRequestEnvelope("TavernKernel이 다른 Global Anti-Cheat 대비 부족한 기능들을 정리해서 알려줘.")
	if envelope.PrimaryClass != RequestClassQuestion {
		t.Fatalf("expected question primary class, got %#v", envelope)
	}
	if !envelope.ReadOnlyAnalysis {
		t.Fatalf("answer-only comparison request should be read-only, got %#v", envelope)
	}
	if envelope.DocumentAuthoring || envelope.AllowsFileMutation || envelope.ExplicitEditRequest {
		t.Fatalf("answer-only comparison must not allow mutation or document authoring, got %#v", envelope)
	}
	if !strings.EqualFold(envelope.ReviewLifecycleKind, reviewLifecycleKindAnalysis) {
		t.Fatalf("expected analysis lifecycle kind, got %#v", envelope)
	}
	plan := requestEnvelopeTestAgent(t, requestEnvelopeTestRegistry()).buildTurnToolExposurePlanForEnvelope(nil, envelope, false, false, false, false, false, false)
	if !plan.toolDisabled("apply_patch") || !plan.toolDisabled("write_file") {
		t.Fatalf("answer-only comparison request must disable edit tools, got %#v", plan.DisabledTools)
	}
}

func TestRequestEnvelopeTreatsNegatedKoreanEditAsReadOnly(t *testing.T) {
	envelope := buildRequestEnvelope("main.go를 분석만 해. 파일은 수정하지 마")
	if !envelope.ReadOnlyAnalysis {
		t.Fatalf("negated edit request should be read-only, got %#v", envelope)
	}
	if envelope.ExplicitEditRequest || envelope.AllowsFileMutation {
		t.Fatalf("negated edit request must not allow file mutation, got %#v", envelope)
	}
	plan := requestEnvelopeTestAgent(t, requestEnvelopeTestRegistry()).buildTurnToolExposurePlanForEnvelope(nil, envelope, false, false, false, false, false, false)
	if !plan.toolDisabled("apply_patch") || !plan.toolDisabled("write_file") {
		t.Fatalf("negated edit request must disable edit tools, got %#v", plan.DisabledTools)
	}
}

func TestRequestEnvelopeClassifiesExplicitEditRequiresVerification(t *testing.T) {
	for _, request := range []string{
		"RuntimeManager.cpp 버그를 고쳐줘",
		"make the change",
	} {
		envelope := buildRequestEnvelope(request)
		if envelope.PrimaryClass != RequestClassEdit {
			t.Fatalf("expected edit primary class for %q, got %#v", request, envelope)
		}
		if !envelope.ExplicitEditRequest || !envelope.AllowsFileMutation {
			t.Fatalf("explicit edit request %q should allow file mutation, got %#v", request, envelope)
		}
		if !envelope.RequiresVerification {
			t.Fatalf("explicit edit request %q should require verification, got %#v", request, envelope)
		}
		plan := requestEnvelopeTestAgent(t, requestEnvelopeTestRegistry()).buildTurnToolExposurePlanForEnvelope(nil, envelope, false, false, false, false, false, false)
		if plan.toolDisabled("apply_patch") || plan.toolDisabled("write_file") {
			t.Fatalf("explicit edit request %q should expose edit tools, got %#v", request, plan.DisabledTools)
		}
	}
}

// TestRequestEnvelopeClassifiesInsertionEditAsEdit guards the fix for a real
// session where ".env에 gitlab 토큰을 넣어두고 사용하게 하자" was misread as a
// read-only request. The insertion/wiring phrasing is a genuine edit, so the
// envelope must allow file mutation AND repair continuation; otherwise the
// pre-write repair stop wrongly renders a read-only "cannot continue" boundary.
func TestRequestEnvelopeClassifiesInsertionEditAsEdit(t *testing.T) {
	for _, request := range []string{
		".env에 gitlab 토큰을 넣어두고 사용하게 하자",
		"config.json에 키를 집어넣어줘",
	} {
		envelope := buildRequestEnvelope(request)
		if !envelope.ExplicitEditRequest || !envelope.AllowsFileMutation {
			t.Fatalf("insertion edit %q should allow file mutation, got %#v", request, envelope)
		}
		if !requestTextAllowsRepairContinuation(request) {
			t.Fatalf("insertion edit %q should allow repair continuation", request)
		}
	}
}

func TestRequestEnvelopeAllowsDocumentArtifactWrites(t *testing.T) {
	envelope := buildRequestEnvelope("분석 계획 문서를 .kernforge/plans/request.md로 작성해줘")
	if envelope.PrimaryClass != RequestClassDocument {
		t.Fatalf("expected document primary class, got %#v", envelope)
	}
	if !envelope.DocumentAuthoring || !envelope.AllowsFileMutation {
		t.Fatalf("document authoring should allow requested artifact writes, got %#v", envelope)
	}
	if envelope.AllowsGitMutation {
		t.Fatalf("document authoring should not imply git mutation, got %#v", envelope)
	}
}

func TestRequestEnvelopeKeepsDocumentArtifactSummaryWritable(t *testing.T) {
	envelope := buildRequestEnvelope("TavernKernel 부족 기능을 별도 문서로 정리해줘")
	if envelope.PrimaryClass != RequestClassDocument {
		t.Fatalf("expected document primary class, got %#v", envelope)
	}
	if !envelope.DocumentAuthoring || !envelope.AllowsFileMutation {
		t.Fatalf("document artifact summary should allow artifact writes, got %#v", envelope)
	}
	if envelope.ReadOnlyAnalysis {
		t.Fatalf("document artifact summary must not be read-only, got %#v", envelope)
	}
}

func TestRequestEnvelopeRequiresFreshExternalInfoForLatestResearch(t *testing.T) {
	envelope := buildRequestEnvelope("최신 리서치를 조사해서 보강할 부분을 찾아줘")
	if !envelope.RequiresFreshExternalInfo {
		t.Fatalf("latest research request should require fresh external info, got %#v", envelope)
	}
	if !envelope.AllowsWebResearch {
		t.Fatalf("latest research request should allow web research tools, got %#v", envelope)
	}
	if !requestEnvelopeHasClass(envelope, RequestClassResearch) {
		t.Fatalf("latest research request should include research class, got %#v", envelope)
	}
}

func TestRequestEnvelopeGatesGitMutationOnExplicitRequest(t *testing.T) {
	agent := requestEnvelopeTestAgent(t, requestEnvelopeTestRegistry())
	implicit := buildRequestEnvelope("작업 끝내줘")
	if implicit.AllowsGitMutation {
		t.Fatalf("implicit completion request must not allow git mutation, got %#v", implicit)
	}
	implicitPlan := agent.buildTurnToolExposurePlanForEnvelope(nil, implicit, false, false, false, false, false, false)
	if !implicitPlan.toolDisabled("git_commit") || !implicitPlan.toolDisabled("git_push") {
		t.Fatalf("implicit request must not expose git mutation tools, got %#v", implicitPlan.DisabledTools)
	}

	explicit := buildRequestEnvelope("변경사항 커밋하고 push해")
	if !explicit.AllowsGitMutation || !explicit.ExplicitGitRequest {
		t.Fatalf("explicit git request should allow git mutation, got %#v", explicit)
	}
	explicitPlan := agent.buildTurnToolExposurePlanForEnvelope(nil, explicit, false, false, false, false, false, false)
	if explicitPlan.toolDisabled("git_commit") || explicitPlan.toolDisabled("git_push") {
		t.Fatalf("explicit git request should expose git mutation tools, got %#v", explicitPlan.DisabledTools)
	}
}

// TestRequestEnvelopeAllowsRepoBootstrapGitRequests guards the fix for a real
// session where "git 초기화 하자" (and "git init") was not recognized as an
// explicit git request, so AllowsGitMutation stayed false and the tool contract
// blocked git init even under full permission. Repo bootstrap (init/clone) is an
// explicit user-requested git action and must allow git mutation.
func TestRequestEnvelopeAllowsRepoBootstrapGitRequests(t *testing.T) {
	for _, request := range []string{
		"git 초기화 하자",
		"git init",
		"저장소 초기화 해줘",
		"clone the repository",
	} {
		envelope := buildRequestEnvelope(request)
		if !envelope.ExplicitGitRequest || !envelope.AllowsGitMutation {
			t.Fatalf("repo bootstrap request %q should allow git mutation, got %#v", request, envelope)
		}
	}
	// A question about git init stays read-only (no mutation granted).
	q := buildRequestEnvelope("git init이 뭐야?")
	if q.AllowsGitMutation {
		t.Fatalf("a question about git init must not allow git mutation, got %#v", q)
	}
}

func TestRequestEnvelopeKeepsDraftGoalPromptOutOfExecution(t *testing.T) {
	envelope := buildRequestEnvelope("goal 프롬프트를 작성해줘")
	if !envelope.GoalPromptDraftOnly {
		t.Fatalf("expected draft-only goal prompt classification, got %#v", envelope)
	}
	if envelope.AllowsFileMutation || envelope.AllowsGitMutation || envelope.ExplicitEditRequest {
		t.Fatalf("draft-only goal prompt must not allow execution mutations, got %#v", envelope)
	}
	if envelope.PrimaryClass != RequestClassPlan {
		t.Fatalf("draft-only goal prompt should remain planning text work, got %#v", envelope)
	}
	if rendered := envelope.RenderPromptSection(); !strings.Contains(rendered, "Do not call create_goal") {
		t.Fatalf("expected rendered envelope to block goal activation, got %q", rendered)
	}
}

func TestRequestEnvelopeContinuePreservesMutableVerificationContext(t *testing.T) {
	agent := requestEnvelopeTestAgent(t, requestEnvelopeTestRegistry())
	agent.Session.ActivePatchTransaction = &PatchTransaction{
		ID:            "tx",
		Goal:          "main.go 버그를 고쳐줘",
		WorkspaceRoot: agent.Workspace.Root,
		Status:        patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:       "entry",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "main.go",
				Operation: "modify",
			}},
		}},
	}
	envelope := agent.latestRequestEnvelopeFor("계속해")
	if !envelope.AllowsFileMutation {
		t.Fatalf("continuation should preserve mutable context, got %#v", envelope)
	}
	if !envelope.RequiresVerification {
		t.Fatalf("continuation with mutable context should preserve verification requirement, got %#v", envelope)
	}
	if envelope.ReadOnlyAnalysis {
		t.Fatalf("continuation with mutable context should not stay read-only, got %#v", envelope)
	}
}

func TestAgentReplyRendersSessionAwareContinuationEnvelope(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "model", "", "default")
	session.ActivePatchTransaction = &PatchTransaction{
		ID:            "tx",
		Goal:          "main.go 버그를 고쳐줘",
		WorkspaceRoot: root,
		Status:        patchTransactionStatusActive,
		Entries: []PatchTransactionEntry{{
			ID:       "entry",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "main.go",
				Operation: "modify",
			}},
		}},
	}
	session.LastVerification = &VerificationReport{
		Workspace:    root,
		Trigger:      "test",
		Mode:         VerificationAdaptive,
		ChangedPaths: []string{"main.go"},
		Steps: []VerificationStep{{
			Label:  "go test",
			Status: VerificationPassed,
		}},
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: testModificationFinalAnswer("main.go", "passed", "none")}},
		},
	}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     requestEnvelopeTestRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "계속해"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if session.LastRequestEnvelope == nil || !session.LastRequestEnvelope.RequiresVerification || !session.LastRequestEnvelope.AllowsFileMutation {
		t.Fatalf("expected stored continuation envelope to preserve mutable verification context, got %#v", session.LastRequestEnvelope)
	}
	if len(provider.requests) == 0 {
		t.Fatalf("expected provider request")
	}
	found := false
	for _, msg := range provider.requests[0].Messages {
		if !msg.Internal {
			continue
		}
		text := strings.TrimSpace(msg.Text)
		if strings.Contains(text, "Request envelope:") &&
			strings.Contains(text, "- Allows file mutation: true.") &&
			strings.Contains(text, "- Requires verification: true.") {
			found = true
		}
		if strings.Contains(text, "Request envelope:") &&
			strings.Contains(text, "- Requires verification: false.") {
			t.Fatalf("internal continuation envelope rendered stale verification=false context: %q", text)
		}
	}
	if !found {
		t.Fatalf("expected session-aware continuation envelope in provider request, got %#v", provider.requests[0].Messages)
	}
}

func TestAgentReplyStoresRequestEnvelopeAndSeparatesInternalContext(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "model", "", "default")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: "리뷰 결과입니다."}},
		},
	}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     requestEnvelopeTestRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "RuntimeManager.cpp 코드 리뷰해줘")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.TrimSpace(reply) == "" {
		t.Fatalf("expected reply")
	}
	if session.LastRequestEnvelope == nil {
		t.Fatalf("expected session to store latest request envelope")
	}
	if session.LastRequestEnvelope.PrimaryClass != RequestClassReview {
		t.Fatalf("expected stored review envelope, got %#v", session.LastRequestEnvelope)
	}
	if len(provider.requests) == 0 {
		t.Fatalf("expected provider request")
	}
	foundInternalEnvelope := false
	for _, msg := range provider.requests[0].Messages {
		if msg.Role == "user" && !msg.Internal && strings.Contains(msg.Text, "Request envelope:") {
			t.Fatalf("external user text must not include rendered request envelope: %q", msg.Text)
		}
		if msg.Internal && strings.Contains(msg.Text, "Request envelope:") && strings.Contains(msg.Text, "Request mode: analysis-only.") {
			foundInternalEnvelope = true
		}
	}
	if !foundInternalEnvelope {
		t.Fatalf("expected rendered request envelope in internal context, got %#v", provider.requests[0].Messages)
	}
}

func requestEnvelopeHasClass(envelope RequestEnvelope, class RequestClass) bool {
	for _, existing := range envelope.Classes {
		if existing == class {
			return true
		}
	}
	return false
}

func requestEnvelopeTestAgent(t *testing.T, registry *ToolRegistry) *Agent {
	t.Helper()
	root := t.TempDir()
	return &Agent{
		Config:    DefaultConfig(root),
		Tools:     registry,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   NewSession(root, "scripted", "model", "", "default"),
	}
}

func requestEnvelopeTestRegistry() *ToolRegistry {
	return NewToolRegistry(
		&mutableRegistryTool{def: ToolDefinition{Name: "read_file", InputSchema: emptyObjectSchema()}},
		&mutableRegistryTool{def: ToolDefinition{Name: "apply_patch", InputSchema: emptyObjectSchema()}},
		&mutableRegistryTool{def: ToolDefinition{Name: "write_file", InputSchema: emptyObjectSchema()}},
		&mutableRegistryTool{def: ToolDefinition{Name: "replace_in_file", InputSchema: emptyObjectSchema()}},
		&mutableRegistryTool{def: ToolDefinition{Name: "git_commit", InputSchema: emptyObjectSchema()}},
		&mutableRegistryTool{def: ToolDefinition{Name: "git_push", InputSchema: emptyObjectSchema()}},
		&mutableRegistryTool{def: ToolDefinition{Name: "web_search", InputSchema: emptyObjectSchema()}},
	)
}
