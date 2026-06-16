package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRequestSemanticClassificationResponseAcceptsFencedJSON(t *testing.T) {
	parsed, err := parseRequestSemanticClassificationResponse("```json\n{\"primary_class\":\"question\",\"action_boundary\":\"read_only\",\"read_only_analysis\":true,\"confidence\":0.88,\"reason\":\"asks for an answer\"}\n```")
	if err != nil {
		t.Fatalf("parse semantic classification: %v", err)
	}
	if parsed.PrimaryClass != string(RequestClassQuestion) || parsed.ActionBoundary != string(ActionBoundaryReadOnly) {
		t.Fatalf("unexpected parsed classification: %#v", parsed)
	}
	if !semanticBoolTrue(parsed.ReadOnlyAnalysis) {
		t.Fatalf("expected read_only_analysis=true, got %#v", parsed)
	}
}

func TestSemanticClassifierEnabledNarrowsAnswerOnlyRequest(t *testing.T) {
	envelope := buildRequestEnvelope("TavernKernel 부족 기능을 정리해줘")
	classification := RequestSemanticClassification{
		PrimaryClass:      string(RequestClassQuestion),
		ActionBoundary:    string(ActionBoundaryReadOnly),
		ReadOnlyAnalysis:  boolPtr(true),
		DocumentAuthoring: boolPtr(false),
		Confidence:        0.91,
		Reason:            "The user asks for an answer, not a file artifact.",
	}

	got := applySemanticRequestClassification(envelope, classification, RequestSemanticClassifierConfig{Mode: RequestSemanticClassifierModeEnabled})
	if !got.ReadOnlyAnalysis || got.AllowsFileMutation || got.DocumentAuthoring {
		t.Fatalf("expected semantic classifier to narrow to read-only answer, got %#v", got)
	}
	if got.PrimaryClass != RequestClassQuestion {
		t.Fatalf("expected question primary class, got %#v", got)
	}
}

func TestSemanticClassifierDoesNotWidenMutationBoundary(t *testing.T) {
	envelope := buildRequestEnvelope("main.go를 분석만 해. 파일은 수정하지 마")
	classification := RequestSemanticClassification{
		PrimaryClass:        string(RequestClassEdit),
		ActionBoundary:      string(ActionBoundaryMustEdit),
		ReadOnlyAnalysis:    boolPtr(false),
		ExplicitEditRequest: boolPtr(true),
		Confidence:          0.99,
		Reason:              "Incorrectly tries to widen permission.",
	}

	got := applySemanticRequestClassification(envelope, classification, RequestSemanticClassifierConfig{Mode: RequestSemanticClassifierModeEnabled})
	if !got.ReadOnlyAnalysis || got.AllowsFileMutation || got.ExplicitEditRequest {
		t.Fatalf("semantic classifier must not widen a deterministic no-edit boundary, got %#v", got)
	}
}

func TestSemanticClassifierPromotesLowConfidenceDocumentArtifact(t *testing.T) {
	envelope := buildRequestEnvelope("TavernKernel no fusoku kino wo betsu no Markdown bunsho ni matomete kudasai")
	classification := RequestSemanticClassification{
		Intent:            string(TurnIntentPlanOrDesign),
		PrimaryClass:      string(RequestClassDocument),
		ActionBoundary:    string(ActionBoundaryMayEdit),
		ReadOnlyAnalysis:  boolPtr(false),
		DocumentAuthoring: boolPtr(true),
		Confidence:        0.94,
		Reason:            "The user explicitly asks for a separate Markdown document artifact.",
	}

	got := applySemanticRequestClassification(envelope, classification, RequestSemanticClassifierConfig{Mode: RequestSemanticClassifierModeEnabled})
	if !got.DocumentAuthoring || !got.AllowsFileMutation || got.ReadOnlyAnalysis {
		t.Fatalf("expected semantic classifier to promote low-confidence document artifact, got %#v", got)
	}
	if got.PrimaryClass != RequestClassDocument || got.ReviewRequestClass != reviewRequestClassDocumentArtifact {
		t.Fatalf("expected document artifact request class, got %#v", got)
	}
}

func TestSemanticClassifierDoesNotPromoteDocumentOverConfidentAnswerOnly(t *testing.T) {
	envelope := buildRequestEnvelope("TavernKernel이 다른 Global Anti-Cheat 대비 부족한 기능들을 정리해서 알려줘.")
	classification := RequestSemanticClassification{
		PrimaryClass:      string(RequestClassDocument),
		ActionBoundary:    string(ActionBoundaryMayEdit),
		ReadOnlyAnalysis:  boolPtr(false),
		DocumentAuthoring: boolPtr(true),
		Confidence:        0.99,
		Reason:            "Incorrectly treats an answer-only request as an artifact.",
	}

	got := applySemanticRequestClassification(envelope, classification, RequestSemanticClassifierConfig{Mode: RequestSemanticClassifierModeEnabled})
	if got.DocumentAuthoring || got.AllowsFileMutation || !got.ReadOnlyAnalysis {
		t.Fatalf("semantic classifier must not promote a confident answer-only request to document authoring, got %#v", got)
	}
}

func TestSemanticClassifierNarrowsMutationFalsePositiveAtHighConfidence(t *testing.T) {
	// Simulate a heuristic mutation false positive: the deterministic envelope
	// claims an edit, but the request is really a status question (not a clear
	// imperative). A high-confidence read-only verdict must override it.
	envelope := buildRequestEnvelope("정책 전부 받아지도록 처리되는 흐름")
	envelope.ReadOnlyAnalysis = false
	envelope.ExplicitEditRequest = true
	envelope.AllowsFileMutation = true
	classification := RequestSemanticClassification{
		PrimaryClass:     string(RequestClassQuestion),
		ActionBoundary:   string(ActionBoundaryReadOnly),
		ReadOnlyAnalysis: boolPtr(true),
		Confidence:       0.9,
		Reason:           "It asks about the current state, not an edit order.",
	}
	got := applySemanticRequestClassification(envelope, classification, RequestSemanticClassifierConfig{Mode: RequestSemanticClassifierModeEnabled})
	if !got.ReadOnlyAnalysis || got.AllowsFileMutation || got.ExplicitEditRequest {
		t.Fatalf("high-confidence narrowing must override a heuristic mutation false positive, got %#v", got)
	}
}

func TestSemanticClassifierDoesNotNarrowImperativeEdit(t *testing.T) {
	// An unambiguous imperative edit must never be silenced, even by a confident
	// (mistaken) read-only verdict.
	envelope := buildRequestEnvelope("정책 다 받게 구현해줘")
	classification := RequestSemanticClassification{
		PrimaryClass:     string(RequestClassQuestion),
		ActionBoundary:   string(ActionBoundaryReadOnly),
		ReadOnlyAnalysis: boolPtr(true),
		Confidence:       0.99,
		Reason:           "Wrongly tries to silence an explicit implement request.",
	}
	got := applySemanticRequestClassification(envelope, classification, RequestSemanticClassifierConfig{Mode: RequestSemanticClassifierModeEnabled})
	if got.ReadOnlyAnalysis || !got.AllowsFileMutation {
		t.Fatalf("an imperative edit must not be narrowed to read-only, got %#v", got)
	}
}

func TestSemanticClassifierDoesNotNarrowMutationBelowOverrideConfidence(t *testing.T) {
	// Above the base threshold but below the mutation-override bar: a mutation
	// signal must NOT be flipped to read-only on a merely-acceptable verdict.
	envelope := buildRequestEnvelope("정책 전부 받아지도록 처리되는 흐름")
	envelope.ReadOnlyAnalysis = false
	envelope.ExplicitEditRequest = true
	envelope.AllowsFileMutation = true
	classification := RequestSemanticClassification{
		PrimaryClass:     string(RequestClassQuestion),
		ActionBoundary:   string(ActionBoundaryReadOnly),
		ReadOnlyAnalysis: boolPtr(true),
		Confidence:       0.80,
		Reason:           "Not confident enough to override a mutation signal.",
	}
	got := applySemanticRequestClassification(envelope, classification, RequestSemanticClassifierConfig{Mode: RequestSemanticClassifierModeEnabled})
	if got.ReadOnlyAnalysis || !got.AllowsFileMutation {
		t.Fatalf("a mutation signal must not be narrowed below the override confidence, got %#v", got)
	}
}

func TestRequestSemanticClassifierCanSkip(t *testing.T) {
	// Clear imperative edit -> skip (must never be narrowed; LLM adds no value).
	if !requestSemanticClassifierCanSkip(buildRequestEnvelope("구현해줘")) {
		t.Errorf("a clear imperative edit should skip the LLM")
	}
	// Already read-only question with no mutation -> skip (already least-privilege).
	if !requestSemanticClassifierCanSkip(buildRequestEnvelope("이거 구현돼 있나?")) {
		t.Errorf("a read-only question with no mutation should skip the LLM")
	}
	// Ambiguous mutation claim that is not a clear imperative -> run the LLM.
	if requestSemanticClassifierCanSkip(buildRequestEnvelope("update the policy list")) {
		t.Errorf("an ambiguous mutation claim must run the LLM, got skip")
	}
}

func TestSemanticClassifierShadowDoesNotChangeEnvelope(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	classification := RequestSemanticClassification{
		PrimaryClass:     string(RequestClassQuestion),
		ActionBoundary:   string(ActionBoundaryReadOnly),
		ReadOnlyAnalysis: boolPtr(true),
		Confidence:       0.95,
		Reason:           "Shadow result should not mutate.",
	}

	got := applySemanticRequestClassification(envelope, classification, RequestSemanticClassifierConfig{Mode: RequestSemanticClassifierModeShadow})
	if got.ReadOnlyAnalysis || !got.AllowsFileMutation || !got.ExplicitEditRequest {
		t.Fatalf("shadow semantic classifier must not change deterministic edit envelope, got %#v", got)
	}
	if !requestEnvelopeHasWarning(got, "shadow mode") {
		t.Fatalf("expected shadow warning, got %#v", got.Warnings)
	}
}

func TestAgentSemanticClassifierUsesModelJSONBeforeMainTurn(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.RequestRuntime.SemanticClassifier = RequestSemanticClassifierConfig{
		Mode:          RequestSemanticClassifierModeEnabled,
		MinConfidence: floatPtr(0.7),
	}
	session := NewSession(root, "scripted", "model", "", "default")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: `{"primary_class":"question","action_boundary":"read_only","read_only_analysis":true,"explicit_edit_request":false,"document_authoring":false,"confidence":0.93,"reason":"asks for an answer, not an edit"}`}},
			{Message: Message{Role: "assistant", Text: "부족한 기능을 답변으로 정리했습니다. Files edited: none."}},
		},
	}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	// Ambiguous request: the heuristic reads "갱신" as a mutation, so the fast
	// path does NOT skip it and the classifier runs. A high-confidence read-only
	// verdict then overrides the mutation false positive (the new narrow path).
	reply, err := agent.Reply(context.Background(), "TavernKernel 부족 기능 정리 문서를 갱신")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !strings.Contains(reply, "부족한 기능") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected semantic classifier request and main request, got %d", len(provider.requests))
	}
	if !provider.requests[0].JSONMode || !strings.Contains(provider.requests[0].System, "Classify") {
		t.Fatalf("first request should be semantic classifier JSON request, got %#v", provider.requests[0])
	}
	if session.LastRequestEnvelope == nil || !session.LastRequestEnvelope.ReadOnlyAnalysis || session.LastRequestEnvelope.AllowsFileMutation {
		t.Fatalf("expected stored semantic-refined read-only envelope, got %#v", session.LastRequestEnvelope)
	}
}

func TestAgentSemanticClassifierHoldsDocumentPromotionUntilCalibrated(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.RequestRuntime.SemanticClassifier = RequestSemanticClassifierConfig{
		Mode:          RequestSemanticClassifierModeEnabled,
		MinConfidence: floatPtr(0.7),
	}
	session := NewSession(root, "scripted", "model", "", "default")
	provider := &scriptedProviderClient{replies: []ChatResponse{{
		Message: Message{Role: "assistant", Text: `{"primary_class":"document","action_boundary":"may_edit","read_only_analysis":false,"explicit_edit_request":false,"document_authoring":true,"confidence":0.94,"reason":"asks for a separate Markdown artifact"}`},
	}}}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	envelope := buildRequestEnvelope("TavernKernel no fusoku kino wo betsu no Markdown bunsho ni matomete kudasai")
	got := agent.maybeRefineRequestEnvelopeWithSemanticClassifier(context.Background(), envelope)
	if got.DocumentAuthoring || got.AllowsFileMutation {
		t.Fatalf("uncalibrated semantic classifier must not promote document mutation, got %#v", got)
	}
	if !requestEnvelopeHasWarning(got, "promotion held in shadow") {
		t.Fatalf("expected promotion-held warning, got %#v", got.Warnings)
	}
	if session.LastSemanticRequestEnvelope == nil || !session.LastSemanticRequestEnvelope.DocumentAuthoring {
		t.Fatalf("expected shadow candidate to be retained for calibration, got %#v", session.LastSemanticRequestEnvelope)
	}
}

func TestAgentSemanticClassifierAllowsCalibratedDocumentPromotion(t *testing.T) {
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.RequestRuntime.SemanticClassifier = RequestSemanticClassifierConfig{
		Mode:          RequestSemanticClassifierModeEnabled,
		MinConfidence: floatPtr(0.7),
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.RequestRuntimeShadowStats = &RequestRuntimeShadowStats{
		SemanticObserved:      3,
		SemanticRiskyDiverged: 0,
	}
	provider := &scriptedProviderClient{replies: []ChatResponse{{
		Message: Message{Role: "assistant", Text: `{"primary_class":"document","action_boundary":"may_edit","read_only_analysis":false,"explicit_edit_request":false,"document_authoring":true,"confidence":0.94,"reason":"asks for a separate Markdown artifact"}`},
	}}}
	agent := &Agent{
		Config:    cfg,
		Client:    provider,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	envelope := buildRequestEnvelope("TavernKernel no fusoku kino wo betsu no Markdown bunsho ni matomete kudasai")
	got := agent.maybeRefineRequestEnvelopeWithSemanticClassifier(context.Background(), envelope)
	if !got.DocumentAuthoring || !got.AllowsFileMutation || got.ReadOnlyAnalysis {
		t.Fatalf("calibrated semantic classifier should allow document promotion, got %#v", got)
	}
}

func requestEnvelopeHasWarning(envelope RequestEnvelope, needle string) bool {
	for _, warning := range envelope.Warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}
