package main

import (
	"strings"
	"testing"
)

func TestPrimeSelfDrivingWorkLoopSeedsPlanForImplementationRequest(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	agent := &Agent{Session: session}

	started := agent.primeSelfDrivingWorkLoop(
		"Proactive Situation Judgment And Suggestions 항목들을 모두 구현하자.",
		TurnIntentEditCode,
		false,
		true,
		false,
	)
	if !started {
		t.Fatalf("expected self-driving loop to start")
	}
	if session.TaskState == nil {
		t.Fatalf("expected task state")
	}
	if session.TaskState.Phase != "execution" {
		t.Fatalf("expected execution phase, got %q", session.TaskState.Phase)
	}
	if !session.TaskState.PlanApproved {
		t.Fatalf("expected approved default plan")
	}
	if len(session.Plan) != 4 {
		t.Fatalf("expected 4 default plan items, got %#v", session.Plan)
	}
	if session.TaskGraph == nil || len(session.TaskGraph.Nodes) != 4 {
		t.Fatalf("expected task graph nodes, got %#v", session.TaskGraph)
	}
	var hasVerification bool
	var hasSummary bool
	for _, node := range session.TaskGraph.Nodes {
		if node.Kind == "verification" {
			hasVerification = true
		}
		if node.Kind == "summary" {
			hasSummary = true
		}
	}
	if !hasVerification || !hasSummary {
		t.Fatalf("expected verification and summary nodes, got %#v", session.TaskGraph.Nodes)
	}
}

func TestPrimeSelfDrivingWorkLoopDoesNotStartForRecentErrorQuestion(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	agent := &Agent{Session: session}

	started := agent.primeSelfDrivingWorkLoop(
		"방금 에러는 왜 난거야?",
		TurnIntentDiagnoseRecentError,
		true,
		false,
		false,
	)
	if started {
		t.Fatalf("did not expect self-driving loop to start")
	}
	if session.TaskState != nil {
		t.Fatalf("did not expect task state, got %#v", session.TaskState)
	}
}

func TestRenderSelfDrivingWorkLoopPromptIncludesLoopGuidance(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	agent := &Agent{Session: session}
	agent.primeSelfDrivingWorkLoop("이제 남은 항목들을 구현하자.", TurnIntentEditCode, false, true, false)

	rendered := renderSelfDrivingWorkLoopPrompt(session.TaskState)
	for _, want := range []string{
		"Self-driving work loop:",
		"inspect -> implement -> verify -> summarize",
		"Do not stop at analysis",
		"Keep the task graph current",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, rendered)
		}
	}
}

func TestFinalizeSelfDrivingWorkLoopMarksRecoveryOnFailedVerification(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	agent := &Agent{Session: session}
	agent.primeSelfDrivingWorkLoop("테스트가 실패하면 고치면서 구현하자.", TurnIntentEditCode, false, true, false)

	finalized := agent.finalizeSelfDrivingWorkLoopOnReturn("Verification still fails.", true)
	if !finalized {
		t.Fatalf("expected finalization hook to handle failed verification")
	}
	if session.TaskState.Phase != "recovery" {
		t.Fatalf("expected recovery phase, got %q", session.TaskState.Phase)
	}
	if !strings.Contains(strings.ToLower(session.TaskState.NextStep), "verification") {
		t.Fatalf("expected verification next step, got %q", session.TaskState.NextStep)
	}
}

func TestFinalizeSelfDrivingWorkLoopCompletesPlanWhenUnblocked(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	agent := &Agent{Session: session}
	agent.primeSelfDrivingWorkLoop("구현하고 검증까지 끝내자.", TurnIntentEditCode, false, true, false)

	finalized := agent.finalizeSelfDrivingWorkLoopOnReturn("Implemented and verified.", false)
	if !finalized {
		t.Fatalf("expected finalization hook to complete unblocked loop")
	}
	if session.TaskState.Phase != "done" {
		t.Fatalf("expected done phase, got %q", session.TaskState.Phase)
	}
	for _, item := range session.Plan {
		if item.Status != "completed" {
			t.Fatalf("expected completed plan item, got %#v", session.Plan)
		}
	}
}
