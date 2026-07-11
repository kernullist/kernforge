package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testImplementationDecisionToolInput() map[string]any {
	return map[string]any{
		"problem":             "Choose the canonical cross-project decision storage layout",
		"header":              "Decision storage",
		"decision_kind":       "storage_backend",
		"risk_level":          "medium",
		"detector_confidence": 0.92,
		"domains":             []any{"developer_tooling"},
		"languages":           []any{"go"},
		"evidence_refs":       []any{"cmd/kernforge/storage_atomic.go"},
		"options": []any{
			map[string]any{
				"id":          "per-record-json",
				"label":       "Per-record JSON",
				"description": "Isolates writes and keeps records portable.",
				"pros":        []any{"Atomic per-decision updates"},
				"cons":        []any{"Requires an index for fast aggregation"},
				"recommended": true,
			},
			map[string]any{
				"id":          "single-json",
				"label":       "Single JSON document",
				"description": "Keeps all decisions in one simple file.",
				"pros":        []any{"Simple export"},
				"cons":        []any{"Cross-process read-modify-write conflicts"},
				"recommended": false,
			},
		},
	}
}

func TestImplementationDecisionToolDefinitionUsesObjectSchemaForCaptureAndResume(t *testing.T) {
	definitions := NewToolRegistry(NewImplementationDecisionTool(Workspace{})).Definitions()
	if len(definitions) != 1 {
		t.Fatalf("decision tool definition count=%d, want 1", len(definitions))
	}
	schema := definitions[0].InputSchema
	if schema["type"] != "object" {
		t.Fatalf("decision tool schema must be a top-level object: %#v", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("decision tool schema has no object properties: %#v", schema)
	}
	for _, name := range []string{"decision_id", "problem", "decision_kind", "options", "detector_confidence"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("decision tool schema lost %q after registry normalization: %#v", name, schema)
		}
	}
	options, ok := properties["options"].(map[string]any)
	if !ok {
		t.Fatalf("decision tool schema lost the options object: %#v", schema)
	}
	items, ok := options["items"].(map[string]any)
	if !ok {
		t.Fatalf("decision tool schema lost the option item schema: %#v", options)
	}
	optionProperties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("decision tool schema lost the option properties: %#v", items)
	}
	for _, name := range []string{"id", "label", "description", "recommended"} {
		if _, ok := optionProperties[name]; !ok {
			t.Fatalf("decision tool schema lost option field %q: %#v", name, items)
		}
	}
	branches, ok := schema["anyOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("decision tool schema must retain capture and resume branches: %#v", schema)
	}
}

func testImplementationDecisionToolWorkspace(t *testing.T) (Workspace, *Session, *ImplementationDecisionStore) {
	t.Helper()
	root := t.TempDir()
	session := NewSession(root, "test", "test", "", "default")
	decisionStore := &ImplementationDecisionStore{Dir: filepath.Join(root, "global", "records")}
	return Workspace{
		Root:                 root,
		UserInputRequests:    NewUserInputRequestTracker(),
		DecisionStore:        decisionStore,
		DecisionProfileStore: &ImplementationPreferenceProfileStore{Path: filepath.Join(root, "global", "profiles", "profile.json")},
		DecisionSession:      session,
		DecisionSessionStore: NewSessionStore(filepath.Join(root, "sessions")),
	}, session, decisionStore
}

func TestImplementationDecisionToolCapturesChoiceAndPerOptionRationale(t *testing.T) {
	ws, session, decisions := testImplementationDecisionToolWorkspace(t)
	ws.PromptUserChoice = func(question UserQuestion) (UserQuestionResult, error) {
		if !question.AllowCustom || !question.RequireExplicit || len(question.Options) != 2 {
			t.Fatalf("unexpected decision question: %#v", question)
		}
		return UserQuestionResult{Selected: []string{"Single JSON document"}}, nil
	}
	answers := []string{
		"The first version should optimize for the smallest operational surface.",
		"The extra index and file fan-out are premature for the expected volume.",
	}
	ws.PromptUserText = func(question UserTextQuestion) (UserTextResult, error) {
		if len(answers) == 0 {
			t.Fatalf("unexpected extra rationale prompt: %#v", question)
		}
		answer := answers[0]
		answers = answers[1:]
		return UserTextResult{Text: answer}, nil
	}
	result, err := NewImplementationDecisionTool(ws).ExecuteDetailed(context.Background(), testImplementationDecisionToolInput())
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if success, _ := result.Meta["success"].(bool); !success {
		t.Fatalf("decision was not recorded: %#v", result)
	}
	if session.PendingImplementationDecision != nil {
		t.Fatalf("completed decision remained pending: %#v", session.PendingImplementationDecision)
	}
	records, err := decisions.List(ImplementationDecisionFilter{})
	if err != nil || len(records) != 1 {
		t.Fatalf("List: %#v err=%v", records, err)
	}
	record := records[0]
	if record.SelectedOptionID != "single-json" || len(record.RejectedReasons) != 1 || record.RejectedReasons[0].OptionID != "per-record-json" {
		t.Fatalf("choice rationale was not normalized: %#v", record)
	}
	if !strings.Contains(record.SummarySentence, "이 사용자는") || !strings.Contains(record.SummarySentence, "선택하지 않았다") {
		t.Fatalf("summary sentence does not match the journal contract: %q", record.SummarySentence)
	}
}

func TestImplementationDecisionToolAcceptsBoundedLongRationales(t *testing.T) {
	ws, _, decisions := testImplementationDecisionToolWorkspace(t)
	ws.PromptUserChoice = func(question UserQuestion) (UserQuestionResult, error) {
		return UserQuestionResult{Selected: []string{question.Options[0].Label}}, nil
	}
	answers := []string{strings.Repeat("a", 4096), strings.Repeat("b", 4096)}
	ws.PromptUserText = func(question UserTextQuestion) (UserTextResult, error) {
		answer := answers[0]
		answers = answers[1:]
		return UserTextResult{Text: answer}, nil
	}
	result, err := NewImplementationDecisionTool(ws).ExecuteDetailed(context.Background(), testImplementationDecisionToolInput())
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if success, _ := result.Meta["success"].(bool); !success {
		t.Fatalf("long bounded rationales were not recorded: %#v", result)
	}
	records, err := decisions.List(ImplementationDecisionFilter{})
	if err != nil || len(records) != 1 {
		t.Fatalf("List: %#v err=%v", records, err)
	}
	if len(records[0].SummarySentence) <= 8192 || len(records[0].SummarySentence) > implementationDecisionSummaryMaxBytes {
		t.Fatalf("summary length=%d, want 8193..%d", len(records[0].SummarySentence), implementationDecisionSummaryMaxBytes)
	}
}

func TestImplementationDecisionToolPersistsAndResumesPendingChoice(t *testing.T) {
	ws, session, decisions := testImplementationDecisionToolWorkspace(t)
	tool := NewImplementationDecisionTool(ws)
	result, err := tool.ExecuteDetailed(context.Background(), testImplementationDecisionToolInput())
	if err != nil {
		t.Fatalf("headless ExecuteDetailed: %v", err)
	}
	if pending, _ := result.Meta["pending"].(bool); !pending || session.PendingImplementationDecision == nil {
		t.Fatalf("headless decision was not persisted as pending: %#v session=%#v", result, session.PendingImplementationDecision)
	}
	loaded, err := ws.DecisionSessionStore.Load(session.ID)
	if err != nil || loaded.PendingImplementationDecision == nil || loaded.PendingImplementationDecision.DecisionID != session.PendingImplementationDecision.DecisionID {
		t.Fatalf("pending decision did not survive session reload: loaded=%#v err=%v", loaded, err)
	}
	ws.PromptUserChoice = func(question UserQuestion) (UserQuestionResult, error) {
		return UserQuestionResult{Selected: []string{question.Options[0].Label}}, nil
	}
	answers := []string{"Concurrent writers must not overwrite each other.", "One shared document has a larger conflict domain."}
	ws.PromptUserText = func(question UserTextQuestion) (UserTextResult, error) {
		answer := answers[0]
		answers = answers[1:]
		return UserTextResult{Text: answer}, nil
	}
	result, err = NewImplementationDecisionTool(ws).ExecuteDetailed(context.Background(), testImplementationDecisionToolInput())
	if err != nil {
		t.Fatalf("resume ExecuteDetailed: %v", err)
	}
	if success, _ := result.Meta["success"].(bool); !success || session.PendingImplementationDecision != nil {
		t.Fatalf("pending decision did not complete: %#v session=%#v", result, session.PendingImplementationDecision)
	}
	records, err := decisions.List(ImplementationDecisionFilter{})
	if err != nil || len(records) != 1 {
		t.Fatalf("resumed decision was not stored exactly once: %#v err=%v", records, err)
	}
}

func TestImplementationDecisionToolResumesByDecisionIDAfterHistoryIsClearedAndReloaded(t *testing.T) {
	ws, session, decisions := testImplementationDecisionToolWorkspace(t)
	result, err := NewImplementationDecisionTool(ws).ExecuteDetailed(context.Background(), testImplementationDecisionToolInput())
	if err != nil {
		t.Fatalf("headless ExecuteDetailed: %v", err)
	}
	if pending, _ := result.Meta["pending"].(bool); !pending || session.PendingImplementationDecision == nil {
		t.Fatalf("decision was not persisted as pending: result=%#v session=%#v", result, session.PendingImplementationDecision)
	}
	decisionID := session.PendingImplementationDecision.DecisionID
	if prompt := renderPendingImplementationDecisionPrompt(session.PendingImplementationDecision); !strings.Contains(prompt, `"decision_id":"`+decisionID+`"`) {
		t.Fatalf("pending prompt does not expose the explicit resume payload: %q", prompt)
	}

	session.Messages = nil
	session.Summary = "Compacted history intentionally omits the original decision proposal."
	if err := ws.DecisionSessionStore.Save(session); err != nil {
		t.Fatalf("Save cleared session: %v", err)
	}
	loaded, err := ws.DecisionSessionStore.Load(session.ID)
	if err != nil {
		t.Fatalf("Load cleared session: %v", err)
	}
	if len(loaded.Messages) != 0 || loaded.PendingImplementationDecision == nil {
		t.Fatalf("cleared session did not retain only canonical pending state: %#v", loaded)
	}

	resumedWS := ws
	resumedWS.DecisionSession = loaded
	resumedWS.PromptUserChoice = func(question UserQuestion) (UserQuestionResult, error) {
		if len(question.Options) != 2 || question.Question == "" {
			t.Fatalf("pending canonical record was not used for the resumed question: %#v", question)
		}
		return UserQuestionResult{Selected: []string{question.Options[0].Label}}, nil
	}
	answers := []string{
		"Independent records keep the write conflict domain small.",
		"A shared document would couple otherwise unrelated writers.",
	}
	resumedWS.PromptUserText = func(question UserTextQuestion) (UserTextResult, error) {
		if len(answers) == 0 {
			t.Fatalf("unexpected extra rationale prompt: %#v", question)
		}
		answer := answers[0]
		answers = answers[1:]
		return UserTextResult{Text: answer}, nil
	}
	result, err = NewImplementationDecisionTool(resumedWS).ExecuteDetailed(context.Background(), map[string]any{
		"decision_id": decisionID,
	})
	if err != nil {
		t.Fatalf("resume by decision_id: %v", err)
	}
	if success, _ := result.Meta["success"].(bool); !success || loaded.PendingImplementationDecision != nil {
		t.Fatalf("explicit resume did not complete the pending decision: result=%#v pending=%#v", result, loaded.PendingImplementationDecision)
	}
	records, err := decisions.List(ImplementationDecisionFilter{})
	if err != nil || len(records) != 1 || records[0].ID != decisionID {
		t.Fatalf("explicit resume did not store exactly the canonical decision: records=%#v err=%v", records, err)
	}
}

func TestGoalIterationPausesBeforeReviewWhenImplementationDecisionIsPending(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "test", "test", "", "default")
	now := time.Now()
	goal := GoalState{
		ID:        "goal-decision-pause",
		Objective: "Implement a storage backend",
		Status:    goalStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	session.Goals = []GoalState{goal}
	session.ActiveGoalID = goal.ID
	calls := 0
	runtime := &runtimeState{
		cfg:       Config{},
		writer:    io.Discard,
		ui:        NewUI(),
		session:   session,
		store:     NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{Root: root},
		goalReply: func(ctx context.Context, prompt string) (string, error) {
			calls++
			session.PendingImplementationDecision = &PendingImplementationDecision{
				DecisionID: "decision-goal-pending",
				Stage:      implementationDecisionPendingChoice,
				Record: ImplementationDecisionRecord{
					ID:      "decision-goal-pending",
					Problem: "Choose the storage backend",
				},
			}
			return "waiting for the user choice", nil
		},
	}
	updated, done, err := runtime.runGoalIteration(context.Background(), goal)
	if err != nil {
		t.Fatalf("runGoalIteration: %v", err)
	}
	if !done || updated.Status != goalStatusPaused || calls != 1 {
		t.Fatalf("goal did not pause before review: done=%v status=%q calls=%d goal=%#v", done, updated.Status, calls, updated)
	}
	if len(updated.Iterations) != 1 || updated.Iterations[0].Status != goalStatusPaused || !strings.Contains(updated.LastError, "decision-goal-pending") {
		t.Fatalf("paused goal evidence was not persisted: %#v", updated)
	}
}

func TestAgentPreWriteReviewBlocksPendingImplementationDecision(t *testing.T) {
	agent := &Agent{Session: &Session{PendingImplementationDecision: &PendingImplementationDecision{
		DecisionID: "decision-pending",
		Stage:      implementationDecisionPendingRationale,
	}}}
	err := agent.reviewProposedEdit(context.Background(), EditPreview{Preview: "diff"})
	if err == nil || !strings.Contains(err.Error(), "decision-pending") {
		t.Fatalf("pending decision did not fail closed before edit: %v", err)
	}
}

func TestImplementationDecisionCheckpointDefersMixedBatchEdits(t *testing.T) {
	call := ToolCall{Name: "present_implementation_decision"}
	if !toolCallIsReadOnlyDuringMixedEditBatch(call) {
		t.Fatal("decision checkpoint must execute before deferred edits in a mixed batch")
	}
	if !toolMetaImplementationDecisionCheckpoint(map[string]any{"decision_checkpoint": true}) {
		t.Fatal("decision checkpoint metadata was not recognized")
	}
	if !toolMetaImplementationDecisionRequiresUserInput(map[string]any{"decision_checkpoint": true, "pending": true}) {
		t.Fatal("pending decision checkpoint metadata was not recognized")
	}
	calls := []ToolCall{
		{Name: "write_file"},
		{Name: "present_implementation_decision"},
		{Name: "read_file"},
	}
	if index := implementationDecisionToolCallIndex(calls); index != 1 {
		t.Fatalf("decision checkpoint batch index = %d, want 1", index)
	}
	session := &Session{PendingImplementationDecision: &PendingImplementationDecision{DecisionID: "decision-pending"}}
	if !pendingImplementationDecisionBlocksToolCall(session, ToolCall{Name: "apply_patch"}, nil) {
		t.Fatal("pending decision must block edit tools")
	}
	if !pendingImplementationDecisionBlocksToolCall(session, ToolCall{Name: "notebook_edit"}, nil) {
		t.Fatal("pending decision must block notebook edits")
	}
	if !pendingImplementationDecisionBlocksToolCall(session, ToolCall{Name: "run_shell", Arguments: `{"command":"Set-Content -Path x.txt -Value x"}`}, nil) {
		t.Fatal("pending decision must block shell workspace writes")
	}
	if pendingImplementationDecisionBlocksToolCall(session, ToolCall{Name: "read_file", Arguments: `{"path":"x.txt"}`}, nil) {
		t.Fatal("pending decision should still allow read-only inspection")
	}
}

func TestImplementationDecisionToolUsesBaseRootAcrossWorktrees(t *testing.T) {
	baseRoot := t.TempDir()
	session := NewSession(baseRoot, "test", "test", "", "default")
	first := NewImplementationDecisionTool(Workspace{
		BaseRoot:        baseRoot,
		Root:            filepath.Join(baseRoot, ".kernforge", "worktrees", "first"),
		DecisionSession: session,
	})
	second := NewImplementationDecisionTool(Workspace{
		BaseRoot:        baseRoot,
		Root:            filepath.Join(baseRoot, ".kernforge", "worktrees", "second"),
		DecisionSession: session,
	})
	firstRecord, _, err := first.parseRecord(testImplementationDecisionToolInput())
	if err != nil {
		t.Fatalf("first parseRecord: %v", err)
	}
	secondRecord, _, err := second.parseRecord(testImplementationDecisionToolInput())
	if err != nil {
		t.Fatalf("second parseRecord: %v", err)
	}
	firstID, err := implementationDecisionIDForRecord(firstRecord)
	if err != nil {
		t.Fatalf("first implementationDecisionIDForRecord: %v", err)
	}
	secondID, err := implementationDecisionIDForRecord(secondRecord)
	if err != nil {
		t.Fatalf("second implementationDecisionIDForRecord: %v", err)
	}
	if firstRecord.Workspace != baseRoot || secondRecord.Workspace != baseRoot {
		t.Fatalf("workspace identity drifted from base root: first=%q second=%q base=%q", firstRecord.Workspace, secondRecord.Workspace, baseRoot)
	}
	if firstRecord.ProjectID != secondRecord.ProjectID || firstRecord.WorkspaceHash != secondRecord.WorkspaceHash || firstID != secondID {
		t.Fatalf("worktree changed decision identity: first=%#v/%q second=%#v/%q", firstRecord, firstID, secondRecord, secondID)
	}
}

func TestImplementationDecisionToolRedactsPendingBeforeSessionSave(t *testing.T) {
	ws, session, _ := testImplementationDecisionToolWorkspace(t)
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	input := testImplementationDecisionToolInput()
	input["problem"] = "Choose storage for token " + secret
	result, err := NewImplementationDecisionTool(ws).ExecuteDetailed(context.Background(), input)
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if pending, _ := result.Meta["pending"].(bool); !pending || session.PendingImplementationDecision == nil {
		t.Fatalf("decision was not left pending: result=%#v session=%#v", result, session.PendingImplementationDecision)
	}
	if strings.Contains(session.PendingImplementationDecision.Record.Problem, secret) || !session.PendingImplementationDecision.Record.Redaction.Redacted {
		t.Fatalf("pending decision retained unredacted text: %#v", session.PendingImplementationDecision)
	}
	data, err := os.ReadFile(filepath.Join(ws.DecisionSessionStore.Root(), session.ID+".json"))
	if err != nil {
		t.Fatalf("ReadFile pending session: %v", err)
	}
	if strings.Contains(string(data), secret) || !strings.Contains(string(data), "[REDACTED:openai_api_key]") {
		t.Fatalf("pending session redaction failed: %s", data)
	}
}

func TestImplementationDecisionToolRejectsInvalidProposalBeforePendingSave(t *testing.T) {
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "oversized problem",
			mutate: func(input map[string]any) {
				input["problem"] = strings.Repeat("p", 4097)
			},
		},
		{
			name: "oversized option label",
			mutate: func(input map[string]any) {
				input["options"].([]any)[0].(map[string]any)["label"] = strings.Repeat("l", 257)
			},
		},
		{
			name: "secret decision kind",
			mutate: func(input map[string]any) {
				input["decision_kind"] = secret
			},
		},
		{
			name: "secret task fingerprint",
			mutate: func(input map[string]any) {
				input["task_fingerprint"] = secret
			},
		},
		{
			name: "secret evidence fingerprint",
			mutate: func(input map[string]any) {
				input["evidence_fingerprint"] = secret
			},
		},
		{
			name: "secret option id",
			mutate: func(input map[string]any) {
				input["options"].([]any)[0].(map[string]any)["id"] = secret
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ws, session, _ := testImplementationDecisionToolWorkspace(t)
			input := testImplementationDecisionToolInput()
			test.mutate(input)
			if _, err := NewImplementationDecisionTool(ws).ExecuteDetailed(context.Background(), input); err == nil {
				t.Fatal("invalid proposal unexpectedly succeeded")
			}
			if session.PendingImplementationDecision != nil {
				t.Fatalf("invalid proposal poisoned pending state: %#v", session.PendingImplementationDecision)
			}
			if _, err := os.Stat(filepath.Join(ws.DecisionSessionStore.Root(), session.ID+".json")); !os.IsNotExist(err) {
				t.Fatalf("invalid proposal reached session storage: %v", err)
			}
		})
	}
}

type implementationDecisionMutationProbeTool struct {
	calls int
}

func (t *implementationDecisionMutationProbeTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "write_file",
		Description: "Test-only mutation probe.",
		InputSchema: map[string]any{"type": "object"},
	}
}

func (t *implementationDecisionMutationProbeTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	_ = input
	t.calls++
	return "mutated", nil
}

type implementationDecisionUnclassifiedMutationTool struct{}

func (implementationDecisionUnclassifiedMutationTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "external_mutation",
		Description: "Test-only unclassified side effect.",
		InputSchema: map[string]any{"type": "object"},
	}
}

func (implementationDecisionUnclassifiedMutationTool) Execute(context.Context, any) (string, error) {
	return "mutated", nil
}

func TestPendingImplementationDecisionFailsClosedForUnclassifiedMutation(t *testing.T) {
	session := &Session{PendingImplementationDecision: &PendingImplementationDecision{DecisionID: "decision-pending"}}
	registry := NewToolRegistry(implementationDecisionUnclassifiedMutationTool{}, NewAskUserTool(Workspace{}), NewImplementationDecisionTool(Workspace{}))
	if !pendingImplementationDecisionBlocksToolCall(session, ToolCall{Name: "external_mutation"}, registry) {
		t.Fatal("pending decision allowed an unclassified non-read-only tool")
	}
	if pendingImplementationDecisionBlocksToolCall(session, ToolCall{Name: "ask_user"}, registry) {
		t.Fatal("pending decision blocked an explicitly read-only tool")
	}
	if pendingImplementationDecisionBlocksToolCall(session, ToolCall{Name: "present_implementation_decision"}, registry) {
		t.Fatal("pending decision blocked its own resume tool")
	}
}

func TestAgentDecisionBatchIsolationAndHeadlessPendingStop(t *testing.T) {
	ws, session, _ := testImplementationDecisionToolWorkspace(t)
	payload, err := json.Marshal(testImplementationDecisionToolInput())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	probe := &implementationDecisionMutationProbeTool{}
	provider := &scriptedProviderClient{replies: []ChatResponse{
		{Message: Message{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "mutation-first", Name: "write_file", Arguments: `{"path":"should-not-exist"}`},
			{ID: "decision-second", Name: "present_implementation_decision", Arguments: string(payload)},
		}}},
		{Message: Message{Role: "assistant", Text: "This second turn must not be reached."}},
	}}
	agent := &Agent{
		Config:    DefaultConfig(ws.Root),
		Client:    provider,
		Tools:     NewToolRegistry(probe, NewImplementationDecisionTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     ws.DecisionSessionStore,
	}
	reply, err := agent.Reply(context.Background(), "Implement the selected storage layout")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if probe.calls != 0 {
		t.Fatalf("mutation ran before the decision checkpoint: calls=%d", probe.calls)
	}
	if session.PendingImplementationDecision == nil {
		t.Fatal("headless decision was not persisted as pending")
	}
	if !strings.Contains(strings.ToLower(reply), "pending") {
		t.Fatalf("headless reply did not expose pending state: %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("agent continued after requires_user_input: requests=%d", len(provider.requests))
	}
}

func TestAgentRedactsDecisionToolArgumentsBeforePrimaryAndBackupSessionSave(t *testing.T) {
	ws, session, _ := testImplementationDecisionToolWorkspace(t)
	if err := ws.DecisionSessionStore.Save(session); err != nil {
		t.Fatalf("initial session save: %v", err)
	}
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	input := testImplementationDecisionToolInput()
	input["problem"] = "Choose storage for token " + secret
	input["options"].([]any)[0].(map[string]any)["description"] = "Keep " + secret + " out of persisted tool arguments."
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	provider := &scriptedProviderClient{replies: []ChatResponse{{
		Message: Message{Role: "assistant", ToolCalls: []ToolCall{{
			ID:        "decision-secret",
			Name:      "present_implementation_decision",
			Arguments: string(payload),
		}}},
	}}}
	agent := &Agent{
		Config:    DefaultConfig(ws.Root),
		Client:    provider,
		Tools:     NewToolRegistry(NewImplementationDecisionTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     ws.DecisionSessionStore,
	}
	if _, err := agent.Reply(context.Background(), "Implement the selected storage layout"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	primaryPath := filepath.Join(ws.DecisionSessionStore.Root(), session.ID+".json")
	for _, path := range []string{primaryPath, sessionBackupPath(primaryPath)} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("ReadFile %s: %v", path, readErr)
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("session persistence leaked raw decision tool arguments in %s: %s", path, data)
		}
		if !strings.Contains(string(data), "[REDACTED:openai_api_key]") {
			t.Fatalf("session persistence did not retain a redaction marker in %s: %s", path, data)
		}
	}
}

func TestImplementationDecisionIncompleteInterventionRedactsPrimaryAndBackupSessionSave(t *testing.T) {
	ws, session, _ := testImplementationDecisionToolWorkspace(t)
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	input := testImplementationDecisionToolInput()
	input["problem"] = "Choose storage for token " + secret
	input["options"].([]any)[0].(map[string]any)["description"] = "Keep " + secret + " out of persisted runtime interventions."
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	runtimeState := NewTurnRuntimeState(RequestEnvelope{})
	runtimeState.RecordIntervention(RuntimeIntervention{
		Kind:       RuntimeInterventionLengthStop,
		Reason:     "incomplete implementation decision tool call",
		ToolCalls:  []ToolCall{{ID: "decision-incomplete", Name: "present_implementation_decision", Arguments: string(payload)}},
		StopReason: "length",
	})
	session.LastTurnRuntimeState = runtimeState
	if err := ws.DecisionSessionStore.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if len(runtimeState.Interventions) != 1 || len(runtimeState.Interventions[0].ToolCalls) != 1 {
		t.Fatalf("incomplete intervention was not retained: %#v", runtimeState.Interventions)
	}
	arguments := runtimeState.Interventions[0].ToolCalls[0].Arguments
	if strings.Contains(arguments, secret) || !strings.Contains(arguments, "[REDACTED:openai_api_key]") {
		t.Fatalf("runtime intervention retained raw decision arguments: %s", arguments)
	}

	primaryPath := filepath.Join(ws.DecisionSessionStore.Root(), session.ID+".json")
	for _, path := range []string{primaryPath, sessionBackupPath(primaryPath)} {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("ReadFile %s: %v", path, readErr)
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("session persistence leaked raw incomplete intervention arguments in %s: %s", path, data)
		}
		if !strings.Contains(string(data), "[REDACTED:openai_api_key]") {
			t.Fatalf("session persistence did not retain an intervention redaction marker in %s: %s", path, data)
		}
	}
}

func TestAgentPendingDecisionRejectsFalseFinalAnswer(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "scripted", "model", "", "default")
	session.PendingImplementationDecision = &PendingImplementationDecision{
		DecisionID: "decision-awaiting-user",
		Stage:      implementationDecisionPendingChoice,
		Record: ImplementationDecisionRecord{
			ID:      "decision-awaiting-user",
			Problem: "Choose the storage backend",
		},
	}
	provider := &scriptedProviderClient{replies: []ChatResponse{
		{Message: Message{Role: "assistant", Text: "Implementation complete."}},
	}}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
		Store:     store,
	}
	reply, err := agent.Reply(context.Background(), "continue")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "Implementation complete") || !strings.Contains(reply, "decision-awaiting-user") {
		t.Fatalf("pending decision accepted a false final answer: %q", reply)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("pending final-answer guard should stop after one request, got %d", len(provider.requests))
	}
}
