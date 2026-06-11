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
		MinConfidence: 0.7,
	}
	session := NewSession(root, "scripted", "model", "", "default")
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", Text: `{"primary_class":"question","action_boundary":"read_only","read_only_analysis":true,"explicit_edit_request":false,"document_authoring":false,"confidence":0.93,"reason":"answer-only comparison request"}`}},
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

	reply, err := agent.Reply(context.Background(), "TavernKernel이 다른 Global Anti-Cheat 대비 부족한 기능들을 정리해서 알려줘.")
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

func requestEnvelopeHasWarning(envelope RequestEnvelope, needle string) bool {
	for _, warning := range envelope.Warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}
