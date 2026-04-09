package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePlanItemsFromTextHandlesNumberedAndFallback(t *testing.T) {
	items := parsePlanItemsFromText("1. Add command routing\n2. Update help text\n3. Add tests")
	if len(items) != 3 {
		t.Fatalf("expected 3 plan items, got %#v", items)
	}
	if items[0].Step != "Add command routing" || items[0].Status != "pending" {
		t.Fatalf("unexpected first plan item: %#v", items[0])
	}

	fallback := parsePlanItemsFromText("single paragraph without numbering")
	if len(fallback) != 1 {
		t.Fatalf("expected fallback plan item, got %#v", fallback)
	}
	if fallback[0].Step != "single paragraph without numbering" {
		t.Fatalf("unexpected fallback step: %#v", fallback[0])
	}
}

func TestHandleNewFeatureCommandCreatesTrackedArtifacts(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(root)
	session := NewSession(root, "openai", "gpt-5.4", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}

	client := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "# Summary\n\nAdd a tracked feature workflow.\n\n## Acceptance Criteria\n- /new-feature creates spec, plan, and tasks artifacts.\n",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "1. Add /new-feature command routing\n2. Update help and completion coverage\n3. Add workflow tests",
				},
				StopReason: "stop",
			},
		},
	}

	var output bytes.Buffer
	rt := &runtimeState{
		cfg: Config{
			Provider:    "openai",
			Model:       "gpt-5.4",
			MaxTokens:   1024,
			Temperature: 0.1,
		},
		writer:      &output,
		ui:          NewUI(),
		store:       store,
		session:     session,
		agent:       &Agent{},
		interactive: false,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	rt.agent = &Agent{
		Config:    rt.cfg,
		Client:    client,
		Tools:     NewToolRegistry(),
		Workspace: rt.workspace,
		Session:   session,
		Store:     store,
	}

	if err := rt.handleNewFeatureCommand("add a feature workflow command"); err != nil {
		t.Fatalf("handleNewFeatureCommand: %v", err)
	}
	if strings.TrimSpace(rt.session.ActiveFeatureID) == "" {
		t.Fatalf("expected active feature id to be set")
	}
	if len(rt.session.Plan) != 3 {
		t.Fatalf("expected seeded session plan, got %#v", rt.session.Plan)
	}
	if rt.session.Plan[0].Step != "Add /new-feature command routing" {
		t.Fatalf("unexpected first seeded plan step: %#v", rt.session.Plan[0])
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected spec and plan requests, got %d", len(client.requests))
	}
	if got := client.requests[1].Messages[0].Text; !strings.Contains(got, "Feature specification:") {
		t.Fatalf("expected planning prompt to include specification, got %q", got)
	}
	featureStore := NewFeatureStore(root)
	feature, err := featureStore.Load(rt.session.ActiveFeatureID)
	if err != nil {
		t.Fatalf("load feature: %v", err)
	}
	if feature.Status != featureStatusPlanned {
		t.Fatalf("expected planned status, got %#v", feature)
	}
	for _, path := range []string{feature.SpecPath, feature.PlanPath, feature.TasksPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", path, err)
		}
	}
	if _, err := os.Stat(feature.ImplementationPath); !os.IsNotExist(err) {
		t.Fatalf("implementation artifact should not exist yet, err=%v", err)
	}
	if !strings.Contains(output.String(), "Next: run /new-feature implement") {
		t.Fatalf("expected next-step guidance, got %q", output.String())
	}
}

func TestHandleNewFeatureImplementUsesTrackedArtifacts(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(root)
	session := NewSession(root, "openai", "gpt-5.4", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}

	client := &scriptedProviderClient{
		replies: []ChatResponse{
			{
				Message: Message{
					Role: "assistant",
					Text: "# Summary\n\nTracked feature spec.\n",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "1. Add artifact store\n2. Wire subcommands\n3. Add tests",
				},
				StopReason: "stop",
			},
			{
				Message: Message{
					Role: "assistant",
					Text: "implemented tracked feature workflow",
				},
				StopReason: "stop",
			},
		},
	}

	var output bytes.Buffer
	rt := &runtimeState{
		cfg: Config{
			Provider:    "openai",
			Model:       "gpt-5.4",
			MaxTokens:   1024,
			Temperature: 0.1,
		},
		writer:      &output,
		ui:          NewUI(),
		store:       store,
		session:     session,
		agent:       &Agent{},
		interactive: false,
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	rt.agent = &Agent{
		Config:    rt.cfg,
		Client:    client,
		Tools:     NewToolRegistry(),
		Workspace: rt.workspace,
		Session:   session,
		Store:     store,
	}

	if err := rt.handleNewFeatureCommand("add tracked feature workflow"); err != nil {
		t.Fatalf("create tracked feature: %v", err)
	}
	featureID := rt.session.ActiveFeatureID
	if err := rt.handleNewFeatureCommand("implement"); err != nil {
		t.Fatalf("implement tracked feature: %v", err)
	}

	feature, err := NewFeatureStore(root).Load(featureID)
	if err != nil {
		t.Fatalf("load feature: %v", err)
	}
	if feature.Status != featureStatusImplemented {
		t.Fatalf("expected implemented status, got %#v", feature)
	}
	data, err := os.ReadFile(feature.ImplementationPath)
	if err != nil {
		t.Fatalf("read implementation artifact: %v", err)
	}
	if !strings.Contains(string(data), "implemented tracked feature workflow") {
		t.Fatalf("unexpected implementation artifact: %q", string(data))
	}
	if got := client.requests[2].Messages[0].Text; !strings.Contains(got, "Feature specification:") || !strings.Contains(got, "Implementation plan:") {
		t.Fatalf("expected execution prompt to include tracked artifacts, got %q", got)
	}
	if !strings.Contains(output.String(), feature.ImplementationPath) {
		t.Fatalf("expected implementation path in output, got %q", output.String())
	}
	if _, err := os.Stat(filepath.Dir(feature.SpecPath)); err != nil {
		t.Fatalf("expected feature directory to exist: %v", err)
	}
}

func TestResolveFeatureByIDOrPrefixMatchesUniquePrefix(t *testing.T) {
	root := t.TempDir()
	store := NewFeatureStore(root)
	first, err := store.Create(root, "alpha workflow", "openai / gpt-5.4", "")
	if err != nil {
		t.Fatalf("create first feature: %v", err)
	}
	second, err := store.Create(root, "beta workflow", "openai / gpt-5.4", "")
	if err != nil {
		t.Fatalf("create second feature: %v", err)
	}

	prefix := second.ID
	index := strings.Index(prefix, "-beta-workflow")
	if index < 0 {
		t.Fatalf("expected beta slug in feature id: %s", second.ID)
	}
	prefix = prefix[:index+len("-beta")]
	got, err := resolveFeatureByIDOrPrefix(store, prefix)
	if err != nil {
		t.Fatalf("resolve by prefix: %v", err)
	}
	if got.ID != second.ID {
		t.Fatalf("unexpected feature match: got %s want %s", got.ID, second.ID)
	}

	if _, err := resolveFeatureByIDOrPrefix(store, first.ID[:8]); err == nil {
		t.Fatalf("expected ambiguous short prefix to fail")
	}
}

func TestLatestAssistantMessageTextReturnsLatestNonEmptyAssistantReply(t *testing.T) {
	sess := &Session{
		Messages: []Message{
			{Role: "assistant", Text: "first"},
			{Role: "tool", Text: "tool output"},
			{Role: "assistant", Text: ""},
			{Role: "assistant", Text: "final summary"},
		},
	}
	if got := latestAssistantMessageText(sess); got != "final summary" {
		t.Fatalf("unexpected latest assistant text: %q", got)
	}
}

func TestSetActiveFeaturePreservesPreviousFeatureAsLast(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(root)
	session := NewSession(root, "openai", "gpt-5.4", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}
	rt := &runtimeState{
		store:   store,
		session: session,
	}

	first := FeatureWorkflow{ID: "feature-one"}
	second := FeatureWorkflow{ID: "feature-two"}
	if err := rt.setActiveFeature(first); err != nil {
		t.Fatalf("set first active feature: %v", err)
	}
	if err := rt.setActiveFeature(second); err != nil {
		t.Fatalf("set second active feature: %v", err)
	}
	if rt.session.ActiveFeatureID != "feature-two" {
		t.Fatalf("unexpected active feature: %q", rt.session.ActiveFeatureID)
	}
	if rt.session.LastFeatureID != "feature-one" {
		t.Fatalf("unexpected last feature: %q", rt.session.LastFeatureID)
	}
}
