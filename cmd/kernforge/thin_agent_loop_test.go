package main

import (
	"strings"
	"testing"
)

// Industry-style thin gate: local README gap analysis must never force web research
// or defer local tools. Regression for:
// "@README.md 문서를 읽고 현재 구현에 부족한 부분을 찾아서 알려줘"
func TestLocalReadmeGapAnalysisDoesNotForceWebResearch(t *testing.T) {
	query := "@README.md 문서를 읽고 현재 구현에 부족한 부분을 찾아서 알려줘"
	lower := strings.ToLower(query)

	if !requestLooksLikeLocalWorkspaceInspection(lower) {
		t.Fatalf("expected local workspace inspection for %q", query)
	}
	if requestExplicitlyAsksForWebResearch(lower) {
		t.Fatalf("must not treat local gap analysis as explicit web research")
	}
	if shouldPrioritizeWebResearchInSystemPrompt(lower) {
		t.Fatalf("soft web priority must not fire on bare '현재' local analysis")
	}

	env := buildRequestEnvelope(query)
	if env.RequiresFreshExternalInfo {
		t.Fatalf("RequiresFreshExternalInfo must be false, got %#v", env)
	}

	session := NewSession(t.TempDir(), "scripted", "main-model", "", "full")
	session.AddMessage(Message{Role: "user", Text: query})
	manager := &MCPManager{
		servers: []*MCPClient{{
			config: MCPServerConfig{Name: "web_research"},
			tools: []MCPToolDescriptor{{
				Name:        "search_web",
				Description: "Search the web for current articles and references",
			}},
		}},
	}
	if webResearchIntentForToolTurn(session) != "" {
		t.Fatalf("webResearchIntentForToolTurn must be empty for local analysis")
	}
	if shouldBlockLocalToolCallsBeforeWebResearch([]ToolCall{{
		Name:      "read_file",
		Arguments: `{"path":"README.md"}`,
	}}, session, manager) {
		t.Fatalf("local read_file must not be deferred behind web research")
	}
	if shouldBlockLocalToolCallsBeforeWebResearch([]ToolCall{{
		Name:      "list_files",
		Arguments: `{"path":"."}`,
	}}, session, manager) {
		t.Fatalf("list_files must not be deferred for local analysis")
	}
}

func TestBareCurrentWordsDoNotTriggerWebResearch(t *testing.T) {
	for _, q := range []string{
		"현재 상태를 알려줘",
		"current implementation looks incomplete, explain",
		"지금 코드 구조를 설명해",
		"search for the helper function in this repo",
	} {
		if shouldPrioritizeWebResearchInSystemPrompt(strings.ToLower(q)) {
			t.Fatalf("should not soft-prioritize web for %q", q)
		}
	}
}

func TestExplicitWebResearchStillHardGatesWhenNoLocalAttachment(t *testing.T) {
	query := "Hypervisor 기반 게임핵 탐지 최신 기술들을 리서치하고 설계 문서를 작성해줘"
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: query}
	session.TaskState = &TaskState{Goal: query}
	session.AddMessage(Message{Role: "user", Text: query})
	manager := &MCPManager{
		servers: []*MCPClient{{
			config: MCPServerConfig{Name: "web_research"},
			tools: []MCPToolDescriptor{{
				Name:        "search_web",
				Description: "Search the web",
			}},
		}},
	}
	if got := webResearchIntentForToolTurn(session); got == "" {
		t.Fatalf("explicit research request must keep web intent")
	}
	// Pure external research (no @path) may still defer plain list_files first.
	if !shouldBlockLocalToolCallsBeforeWebResearch([]ToolCall{{
		Name:      "list_files",
		Arguments: `{"path":"."}`,
	}}, session, manager) {
		t.Fatalf("unmet explicit web research may still defer pure local listing")
	}
}

func TestAnalysisOnlyStallRecoveryPrefersAnswerNotRepair(t *testing.T) {
	query := "@README.md 문서를 읽고 현재 구현에 부족한 부분을 찾아서 알려줘"
	if !requestLooksLikeAnalysisOnlyTurn(query) {
		t.Fatalf("expected analysis-only classification")
	}
	recovery := buildStallBlockedRecoveryWithMode(Config{}, nil, harnessRecoveryCauseReadChurn, "stopped", []string{"README.md"}, true)
	if len(recovery.Actions) < 2 {
		t.Fatalf("expected analysis recovery actions, got %#v", recovery.Actions)
	}
	if recovery.Actions[0].Kind != harnessRecoveryActionAnswer {
		t.Fatalf("primary action should be answer, got %#v", recovery.Actions[0])
	}
	for _, action := range recovery.Actions {
		if action.Kind == harnessRecoveryActionRepair {
			t.Fatalf("analysis recovery must not offer repair-first card, got %#v", recovery.Actions)
		}
	}
}

func TestPolicyNotExecutedIsNotRepairEvidence(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "full")
	session.AddMessage(Message{
		Role:     "tool",
		ToolName: "read_file",
		IsError:  true,
		Text:     "NOT_EXECUTED: local tools were deferred until required external research is gathered.",
	})
	evidence := collectRecentRepairEvidence(session, 24)
	if evidence.Detail != "" {
		t.Fatalf("policy NOT_EXECUTED must not become repair evidence, got %q", evidence.Detail)
	}
}

func TestStallContinuePromptForAnalysisDoesNotForceEdits(t *testing.T) {
	session := NewSession(t.TempDir(), "scripted", "main-model", "", "full")
	query := "@README.md 문서를 읽고 현재 구현에 부족한 부분을 찾아서 알려줘"
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: query}
	session.TaskState = &TaskState{Goal: query}
	session.AddMessage(Message{Role: "user", Text: query})
	prompt := buildStallContinueRecoveryPromptForAction(Config{}, session, harnessRecoveryCauseReadChurn, harnessRecoveryActionAnswer)
	if strings.Contains(strings.ToLower(prompt), "first tool call must be write_file") {
		t.Fatalf("analysis continue must not force write_file-first bias: %q", prompt)
	}
	if !strings.Contains(prompt, "분석") && !strings.Contains(strings.ToLower(prompt), "analysis") {
		t.Fatalf("analysis continue should mention analysis/reporting: %q", prompt)
	}
}
