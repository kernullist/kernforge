package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptAssetsLoadAllBlocks(t *testing.T) {
	want := map[PromptBlockID]bool{
		PromptBlockSystemBase:             true,
		PromptBlockToolPolicy:             true,
		PromptBlockRequestEnvelope:        true,
		PromptBlockEmptyStopRetry:         true,
		PromptBlockRepeatedToolRedirect:   true,
		PromptBlockBlockedTool:            true,
		PromptBlockManualEditHandoffBlock: true,
		PromptBlockVerificationUnresolved: true,
		PromptBlockLengthStopContinue:     true,
	}
	ids := PromptBlockIDs()
	if len(ids) != len(want) {
		t.Fatalf("expected %d prompt assets, got %d: %#v", len(want), len(ids), ids)
	}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("unexpected prompt block id %q", id)
		}
		path, ok := PromptBlockAssetPath(id)
		if !ok || strings.TrimSpace(path) == "" {
			t.Fatalf("missing asset path for %q", id)
		}
		text, err := LoadPromptBlock(id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if !strings.Contains(path, "prompts/") || !strings.HasSuffix(path, ".md") {
			t.Fatalf("prompt block %s should load from prompts/*.md, got %q", id, path)
		}
		if strings.TrimSpace(text) == "" {
			t.Fatalf("prompt block %s is empty", id)
		}
	}
}

func TestRenderPromptBlockMissingRequiredFieldFails(t *testing.T) {
	_, err := RenderPromptBlock(PromptBlockRequestEnvelope, map[string]any{
		"PrimaryClass": "edit",
	})
	if err == nil {
		t.Fatalf("expected missing template fields to fail")
	}
	if !strings.Contains(err.Error(), "ClassesText") {
		t.Fatalf("expected missing ClassesText in error, got %v", err)
	}
}

func TestRenderPromptRequestEnvelopeIncludesMajorFields(t *testing.T) {
	envelope := buildRequestEnvelope("RuntimeManager.cpp 버그를 고쳐줘")
	rendered, err := RenderRequestEnvelopePromptBlock(envelope)
	if err != nil {
		t.Fatalf("render request envelope: %v", err)
	}
	for _, want := range []string{
		"Request envelope:",
		"- Primary class: edit.",
		"- Classes: edit.",
		"- Action boundary: must_edit.",
		"- Allows file mutation: true.",
		"- Allows git mutation: false.",
		"- Requires verification: true.",
		"The latest user request explicitly asks for a fix.",
		"Do not stage, commit, push, or open a PR unless the user explicitly asks",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected request envelope render to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestRenderPromptEmptyStopRetryUsesStaticAsset(t *testing.T) {
	rendered := RenderEmptyStopRetryPrompt(true, "stop", 1)
	for _, want := range []string{
		"Your last reply was empty.",
		"read-only analysis or review request",
		"Do not return an empty message.",
		"Last stop reason: stop.",
		"Empty response count: 1.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected empty-stop prompt to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestRenderPromptVerificationUnresolvedUsesTypedState(t *testing.T) {
	state := NewTurnRuntimeState(buildRequestEnvelope("main.go를 수정해줘"))
	state.Transition(TurnRuntimeNeedFinalGate, "assistant_final_candidate")
	state.UnresolvedVerification = true
	item := RuntimeIntervention{
		Kind:   RuntimeInterventionVerificationUnresolved,
		Reason: "automatic verification was skipped",
		Count:  1,
	}
	rendered := RenderVerificationUnresolvedPrompt(state, item, "", false)
	for _, want := range []string{
		"Verification is still unresolved.",
		"Runtime state: NeedFinalGate.",
		"Reason: automatic verification was skipped.",
		"Unresolved verification: true.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected verification prompt to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestRenderPromptSnapshotStableBlocks(t *testing.T) {
	envelope := buildRequestEnvelope("main.go 버그를 고쳐줘")
	state := NewTurnRuntimeState(envelope)
	state.Transition(TurnRuntimeNeedFinalGate, "assistant_final_candidate")
	state.UnresolvedVerification = true

	sections := []string{}
	for _, item := range []struct {
		title string
		text  string
	}{
		{title: "system_base", text: mustRenderPromptBlockForTest(t, PromptBlockSystemBase, nil)},
		{title: "request_envelope", text: mustRenderPromptBlockForTest(t, PromptBlockRequestEnvelope, NewRequestEnvelopePromptData(envelope))},
		{title: "empty_stop_retry", text: RenderEmptyStopRetryPrompt(false, "stop", 1)},
		{title: "verification_unresolved", text: RenderVerificationUnresolvedPrompt(state, RuntimeIntervention{Kind: RuntimeInterventionVerificationUnresolved, Reason: "verification skipped"}, "", false)},
		{title: "tool_policy", text: mustRenderPromptBlockForTest(t, PromptBlockToolPolicy, nil)},
	} {
		sections = append(sections, "## "+item.title+"\n"+strings.TrimSpace(item.text))
	}
	got := strings.TrimSpace(strings.Join(sections, "\n\n---\n\n"))
	path := filepath.Join("testdata", "prompt_assets", "system_prompt_stable_blocks.golden")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden snapshot %s: %v", path, err)
	}
	want := strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	got = strings.ReplaceAll(got, "\r\n", "\n")
	if got != want {
		t.Fatalf("prompt render snapshot changed\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSystemPromptUsesPromptAssetsAndReportsFallback(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "MCP resources prompt catalog를 보여줘"})
	events := []ProgressEvent{}
	agent := &Agent{
		Config:    DefaultConfig(root),
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		EmitProgressEvent: func(event ProgressEvent) {
			events = append(events, event)
		},
	}
	oldRender := renderPromptBlockForRuntime
	renderPromptBlockForRuntime = func(id PromptBlockID, data any) (string, error) {
		if id == PromptBlockSystemBase {
			return "", assertErrString("missing test asset")
		}
		return RenderPromptBlock(id, data)
	}
	defer func() {
		renderPromptBlockForRuntime = oldRender
	}()

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "You are Kernforge") {
		t.Fatalf("expected fallback system base prompt, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "MCP resource catalog unavailable in this session.") {
		t.Fatalf("expected MCP fallback catalog in prompt, got:\n%s", prompt)
	}
	found := false
	for _, event := range events {
		if event.Kind == progressKindPromptAssembly && event.PromptBlock == string(PromptBlockSystemBase) && strings.Contains(event.Status, "missing test asset") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected prompt assembly fallback event, got %#v", events)
	}
}

func mustRenderPromptBlockForTest(t *testing.T, id PromptBlockID, data any) string {
	t.Helper()
	rendered, err := RenderPromptBlock(id, data)
	if err != nil {
		t.Fatalf("render %s: %v", id, err)
	}
	return rendered
}
