package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmptyStopRecordsRecoveryDecisionAndDoesNotSucceedSilently(t *testing.T) {
	root := t.TempDir()
	finalReply := "빈 응답 이후 최종 답변입니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message:    Message{Role: "assistant"},
				StopReason: "stop",
			},
			{
				Message:    Message{Role: "assistant", Text: finalReply},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "상태를 확인해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected final reply after empty-stop recovery, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected empty response to trigger another model request, got %d", len(provider.requests))
	}
	if session.LastRecoveryDecision == nil || session.LastRecoveryDecision.Kind != RecoveryKindEmptyStop {
		t.Fatalf("expected empty-stop recovery decision, got %#v", session.LastRecoveryDecision)
	}
	if !scriptedRequestsContainText(provider.requests[1:2], "Please provide the final answer") {
		t.Fatalf("expected empty-stop guidance in retry request, got %#v", provider.requests[1].Messages)
	}
}

func TestEmptyStopCleansOrphanToolMessagesBeforeRetry(t *testing.T) {
	root := t.TempDir()
	finalReply := "정리 후 최종 답변입니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message:    Message{Role: "assistant"},
				StopReason: "stop",
			},
			{
				Message:    Message{Role: "assistant", Text: finalReply},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "tool", ToolCallID: "orphan", Text: "orphan tool result"})
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	if _, err := agent.Reply(context.Background(), "상태를 확인해"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	for _, msg := range session.Messages {
		if strings.EqualFold(msg.Role, "tool") && msg.ToolCallID == "orphan" {
			t.Fatalf("orphan tool message survived empty-stop cleanup: %#v", session.Messages)
		}
	}
	if session.LastProviderStateResetReason != "empty_stop_orphan_tool_cleanup" {
		t.Fatalf("expected orphan cleanup to reset provider state, got %q", session.LastProviderStateResetReason)
	}
}

func TestRecoveryPolicyLengthStopContinuesAgentTurn(t *testing.T) {
	root := t.TempDir()
	finalReply := "길이 제한 이후 이어서 완료했습니다."
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message:    Message{Role: "assistant"},
				StopReason: "length",
			},
			{
				Message:    Message{Role: "assistant", Text: finalReply},
				StopReason: "stop",
			},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config: Config{
			Model:      "model",
			AutoLocale: boolPtr(false),
		},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "긴 답변을 작성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply != finalReply {
		t.Fatalf("expected final reply after length-stop continuation, got %q", reply)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected length stop to trigger continuation request, got %d", len(provider.requests))
	}
	if session.LastRecoveryDecision == nil || session.LastRecoveryDecision.Kind != RecoveryKindLengthStop {
		t.Fatalf("expected length-stop recovery decision, got %#v", session.LastRecoveryDecision)
	}
	if session.LastContextMaintenanceDecision == nil || session.LastContextMaintenanceDecision.Trigger != RecoveryKindLengthStop {
		t.Fatalf("expected length-stop context decision, got %#v", session.LastContextMaintenanceDecision)
	}
}
