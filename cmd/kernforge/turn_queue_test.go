package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestTurnQueueClassifiesKinds(t *testing.T) {
	active := buildRequestEnvelope("kernforge request handling을 고쳐줘")
	cases := []struct {
		name string
		text string
		want QueueKind
	}{
		{name: "steering", text: "아니 먼저 recovery policy부터 고쳐", want: QueueKindUserSteer},
		{name: "follow-up", text: "그 테스트도 추가해줘", want: QueueKindFollowUp},
		{name: "aside", text: "참고로 oh-my-pi 쪽 구현도 봐", want: QueueKindAside},
		{name: "maintenance", text: "계속 진행해", want: QueueKindMaintenance},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := ClassifyTurnQueueKind(active, tc.text)
			if got != tc.want {
				t.Fatalf("ClassifyTurnQueueKind(%q) = %s (%s), want %s", tc.text, got, reason, tc.want)
			}
		})
	}
}

func TestTurnQueueFollowUpAndSteeringStaySeparateSlots(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "queue 작업을 진행해"})
	active := buildRequestEnvelope("queue 작업을 진행해")

	followUp := session.EnqueueTurnInput(active, "그 테스트도 같이 넣어줘", nil)
	steering := session.EnqueueTurnInput(active, "아니 recovery 쪽을 먼저 해", nil)

	if len(session.Messages) != 1 {
		t.Fatalf("queued input polluted message history: %#v", session.Messages)
	}
	if len(session.TurnQueue) != 2 {
		t.Fatalf("expected two queued items, got %#v", session.TurnQueue)
	}
	if followUp.Kind != QueueKindFollowUp || steering.Kind != QueueKindUserSteer {
		t.Fatalf("expected separate follow-up and steering slots, got %#v %#v", followUp, steering)
	}
	first, ok := session.PopTurnQueue()
	if !ok || first.Kind != QueueKindFollowUp {
		t.Fatalf("expected follow-up to pop first, got %#v ok=%v", first, ok)
	}
	second, ok := session.PopTurnQueue()
	if !ok || second.Kind != QueueKindUserSteer {
		t.Fatalf("expected steering to pop second, got %#v ok=%v", second, ok)
	}
	if session.HasTurnQueue() {
		t.Fatalf("expected queue to be empty after pops")
	}

	store := NewSessionStore(filepath.Join(root, "sessions"))
	session.EnqueueTurnInput(active, "계속 진행해", nil)
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.TurnQueue) != 1 || loaded.TurnQueue[0].Kind != QueueKindMaintenance {
		t.Fatalf("expected queued maintenance item to persist, got %#v", loaded.TurnQueue)
	}
}

func TestTurnQueueAgentConcurrentReplyDoesNotPolluteActiveRequest(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "active request"})
	agent := &Agent{
		Config: Config{
			AutoLocale: boolPtr(false),
		},
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	agent.turnMu.Lock()
	defer agent.turnMu.Unlock()

	reply, err := agent.Reply(context.Background(), "아니 recovery 먼저 처리해")
	if err != nil {
		t.Fatalf("Reply while active: %v", err)
	}
	if !strings.Contains(reply, "Queued this input") {
		t.Fatalf("expected queued-input acknowledgement, got %q", reply)
	}
	if len(session.Messages) != 1 {
		t.Fatalf("queued concurrent reply polluted active request history: %#v", session.Messages)
	}
	if len(session.TurnQueue) != 1 || session.TurnQueue[0].Kind != QueueKindUserSteer {
		t.Fatalf("expected queued steering item, got %#v", session.TurnQueue)
	}
}
