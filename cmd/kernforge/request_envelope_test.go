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
