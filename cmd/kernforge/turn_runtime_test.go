package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestTurnRuntimeBlockedToolCallMovesToRecoveryModelTurn(t *testing.T) {
	state := NewTurnRuntimeState(buildRequestEnvelope("작업 끝내줘"))
	item := state.RecordInterventionKind(
		RuntimeInterventionBlockedTool,
		"NOT_EXECUTED: git write actions require an explicit user request.",
		"Continue without git writes.",
		[]ToolCall{{ID: "call_git", Name: "git_commit", Arguments: `{}`}},
	)

	if state.State != TurnRuntimeNeedRecoveryModelTurn {
		t.Fatalf("expected blocked tool to require recovery model turn, got %s", state.State)
	}
	if item.Kind != RuntimeInterventionBlockedTool || state.LastIntervention().Kind != RuntimeInterventionBlockedTool {
		t.Fatalf("expected BlockedTool intervention, got %#v", state.Interventions)
	}
}

func TestTurnRuntimeRepeatedReadLoopCreatesRepeatedToolIntervention(t *testing.T) {
	state := NewTurnRuntimeState(buildRequestEnvelope("main.go를 계속 읽어줘"))
	call := ToolCall{ID: "call_read", Name: "read_file", Arguments: `{"path":"main.go"}`}

	state.RecordRepeatedTool(
		RuntimeInterventionRepeatedTool,
		"read_file repeated the same path",
		"Use a different file or provide the final answer.",
		[]ToolCall{call},
		repeatedReadFilePathNudgeTurns,
	)

	if state.State != TurnRuntimeNeedRecoveryModelTurn {
		t.Fatalf("expected repeated read to require recovery, got %s", state.State)
	}
	item := state.LastIntervention()
	if item.Kind != RuntimeInterventionRepeatedTool || item.Count != repeatedReadFilePathNudgeTurns {
		t.Fatalf("expected RepeatedTool intervention with count, got %#v", item)
	}
	if len(item.ToolCalls) != 1 || item.ToolCalls[0].Name != "read_file" {
		t.Fatalf("expected repeated read tool call evidence, got %#v", item.ToolCalls)
	}
}

func TestTurnRuntimeEmptyModelResponseUsesEmptyStopRecovery(t *testing.T) {
	state := NewTurnRuntimeState(buildRequestEnvelope("코드를 분석해줘"))
	state.RecordStopIntervention(
		RuntimeInterventionEmptyStop,
		"",
		"model returned an empty response",
		"Do not return an empty message.",
	)

	if state.State != TurnRuntimeNeedRecoveryModelTurn {
		t.Fatalf("empty stop should not complete the turn, got %s", state.State)
	}
	if !state.HasUnresolvedIntervention(RuntimeInterventionEmptyStop) {
		t.Fatalf("expected unresolved EmptyStop intervention, got %#v", state.Interventions)
	}

	state.MarkRecoveryModelTurnStarted()
	if state.State != TurnRuntimeNeedModelTurn {
		t.Fatalf("expected recovery model turn to move back to model turn, got %s", state.State)
	}
	if state.HasUnresolvedIntervention(RuntimeInterventionEmptyStop) {
		t.Fatalf("expected EmptyStop to resolve when recovery turn starts, got %#v", state.Interventions)
	}
}

func TestTurnRuntimeFinalGateBlocksUnresolvedVerification(t *testing.T) {
	state := NewTurnRuntimeState(buildRequestEnvelope("main.go를 수정해줘"))
	state.Transition(TurnRuntimeNeedFinalGate, "assistant_final_candidate")
	state.RecordInterventionKind(
		RuntimeInterventionVerificationUnresolved,
		"final-looking answer omitted unresolved verification status",
		"Disclose that verification was not run.",
		nil,
	)

	notReady := state.FinalAnswerReadiness("수정 완료했습니다. 모든 작업이 끝났습니다.", TurnRuntimeFinalContext{})
	if notReady.Ready {
		t.Fatalf("expected unresolved verification to block final readiness")
	}
	if len(notReady.BlockedBy) != 1 || notReady.BlockedBy[0].Kind != RuntimeInterventionVerificationUnresolved {
		t.Fatalf("expected verification blocker, got %#v", notReady)
	}

	ready := state.FinalAnswerReadiness("수정 완료했습니다. Validation: verification was not run.", TurnRuntimeFinalContext{})
	if !ready.Ready {
		t.Fatalf("verification disclosure should make final answer ready, got %#v", ready)
	}
}

func TestTurnRuntimeManualEditHandoffBlocksExplicitEditCompletion(t *testing.T) {
	state := NewTurnRuntimeState(buildRequestEnvelope("main.go 버그를 수정해줘"))
	state.Transition(TurnRuntimeNeedFinalGate, "assistant_final_candidate")
	state.RecordInterventionKind(
		RuntimeInterventionManualEditHandoff,
		"final-looking answer handed an explicit edit request back to the user",
		"Use edit tools directly.",
		nil,
	)

	readiness := state.FinalAnswerReadiness("아래 패치를 직접 수정해 주시면 됩니다.", TurnRuntimeFinalContext{
		ExplicitEditRequest: true,
	})
	if readiness.Ready {
		t.Fatalf("manual edit handoff should not pass final readiness for explicit edit requests")
	}

	readiness = state.FinalAnswerReadiness("수정 도구로 직접 변경했습니다.", TurnRuntimeFinalContext{
		AttemptedEditTool:   true,
		ExplicitEditRequest: true,
	})
	if !readiness.Ready {
		t.Fatalf("attempted edit tool should resolve manual handoff blocker, got %#v", readiness)
	}
}

func TestTurnRuntimeInterventionListFeedsFinalAnswerReadiness(t *testing.T) {
	state := NewTurnRuntimeState(buildRequestEnvelope("수정하고 검증해줘"))
	state.RecordInterventionKind(RuntimeInterventionVerificationUnresolved, "verification unresolved", "", nil)
	state.RecordInterventionKind(RuntimeInterventionManualEditHandoff, "manual edit handoff", "", nil)

	readiness := state.FinalAnswerReadiness("직접 수정해 주시면 됩니다. 완료했습니다.", TurnRuntimeFinalContext{
		ExplicitEditRequest: true,
	})
	if readiness.Ready {
		t.Fatalf("expected unresolved interventions to block readiness")
	}
	if len(readiness.BlockedBy) != 2 {
		t.Fatalf("expected both unresolved blockers in readiness, got %#v", readiness.BlockedBy)
	}
	if !strings.Contains(readiness.Reason, string(RuntimeInterventionVerificationUnresolved)) ||
		!strings.Contains(readiness.Reason, string(RuntimeInterventionManualEditHandoff)) {
		t.Fatalf("readiness reason should name intervention kinds, got %q", readiness.Reason)
	}
}

func TestTurnRuntimeCompleteLoopRecordsBlockedToolInterventionProgress(t *testing.T) {
	root := t.TempDir()
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
				ID:        "call_git",
				Name:      "git_commit",
				Arguments: `{"message":"unexpected"}`,
			}}}},
			{Message: Message{Role: "assistant", Text: "커밋하지 않고 작업 상태를 요약했습니다."}},
		},
	}
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: "작업 끝내줘"}}
	events := []ProgressEvent{}
	agent := &Agent{
		Config:            DefaultConfig(root),
		Client:            provider,
		Tools:             NewToolRegistry(&toolContractRecordingTool{name: "git_commit"}),
		Workspace:         Workspace{BaseRoot: root, Root: root},
		Session:           session,
		Store:             NewSessionStore(filepath.Join(root, "sessions")),
		EmitProgressEvent: func(event ProgressEvent) { events = append(events, event) },
	}

	reply, err := agent.completeLoop(context.Background(), false, false, false)
	if err != nil {
		t.Fatalf("completeLoop: %v", err)
	}
	if !strings.Contains(reply, "커밋하지") {
		t.Fatalf("unexpected final reply: %q", reply)
	}
	if session.LastTurnRuntimeState == nil || session.LastTurnRuntimeState.State != TurnRuntimeCompleted {
		t.Fatalf("expected completed runtime state, got %#v", session.LastTurnRuntimeState)
	}
	if !turnRuntimeSessionHasIntervention(session, RuntimeInterventionBlockedTool) {
		t.Fatalf("expected BlockedTool intervention in session runtime state, got %#v", session.LastTurnRuntimeState)
	}
	foundProgress := false
	for _, event := range events {
		if event.Kind == progressKindRuntimeIntervention &&
			event.RuntimeIntervention == string(RuntimeInterventionBlockedTool) &&
			strings.Contains(event.Status, "git write actions require an explicit user request") {
			foundProgress = true
			break
		}
	}
	if !foundProgress {
		t.Fatalf("expected runtime intervention progress event with reason, got %#v", events)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected recovery model turn after blocked tool call, got %d request(s)", len(provider.requests))
	}
}

func turnRuntimeSessionHasIntervention(session *Session, kind RuntimeInterventionKind) bool {
	if session == nil || session.LastTurnRuntimeState == nil {
		return false
	}
	for _, item := range session.LastTurnRuntimeState.Interventions {
		if item.Kind == kind {
			return true
		}
	}
	return false
}
