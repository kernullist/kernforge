package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProactiveProviderRateLimitSuggestionIsShownOnce(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "deepseek/deepseek-v4-flash", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindProviderError,
		Severity: conversationSeverityError,
		Summary:  "provider error: 429 Too Many Requests",
		Entities: map[string]string{
			"category": "rate_limit",
			"code":     "429",
			"model":    "deepseek/deepseek-v4-flash",
			"shard":    "BuildCab_refined_03",
		},
	})
	agent := &Agent{
		Workspace: Workspace{BaseRoot: root, Root: root},
		Session:   session,
	}

	reply := agent.maybeAppendProactiveSuggestion("원인은 provider rate limit입니다.", "방금 에러는 왜 난거야?")
	if !strings.Contains(reply, "Suggested next step") {
		t.Fatalf("expected proactive suggestion, got %q", reply)
	}
	if len(session.SuggestionMemory.Records) != 1 {
		t.Fatalf("expected shown suggestion record, got %#v", session.SuggestionMemory)
	}

	again := agent.maybeAppendProactiveSuggestion("다시 설명합니다.", "방금 에러는 왜 난거야?")
	if strings.Contains(again, "Suggested next step") {
		t.Fatalf("expected dedup cooldown to suppress repeat, got %q", again)
	}
}

// After /model switch, a historical rate-limit for the previous model must not
// blame the active route or suggest switching models for an unrelated failure
// (e.g. compile_error verification) under the new model.
func TestProactiveRateLimitSuggestionIgnoresStalePreviousModel(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "z-ai/glm-5.2", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindProviderError,
		Severity: conversationSeverityError,
		Summary:  "provider error: 429 Too Many Requests",
		Entities: map[string]string{
			"category":      "rate_limit",
			"code":          "429",
			"model":         "moonshotai/kimi-k3",
			"session_model": "moonshotai/kimi-k3",
		},
	})
	// Workflow continued under the new model after the operator switched.
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindAssistantReply,
		Severity: conversationSeverityInfo,
		Summary:  "edit proposal under glm-5.2",
	})
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindVerification,
		Severity: conversationSeverityError,
		Summary:  "msbuild compile_error",
	})

	items := BuildProactiveSuggestions(SituationSnapshot{}, ProactiveSources{Session: session})
	for _, item := range items {
		if item.Type == "retry_or_switch_model" {
			t.Fatalf("stale previous-model rate-limit must not produce retry_or_switch_model, got %#v", item)
		}
	}
	if event, ok := latestProviderRateLimitEvent(session); ok {
		t.Fatalf("latestProviderRateLimitEvent must ignore stale previous-model event, got %#v", event)
	}
}

// A rate-limit that was recovered from (tools/assistant continued) must not
// remain the default "Suggested next step" for a later unrelated failure.
func TestProactiveRateLimitSuggestionExpiresAfterRecovery(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "z-ai/glm-5.2", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindProviderError,
		Severity: conversationSeverityError,
		Summary:  "provider error: 429 Too Many Requests",
		Entities: map[string]string{
			"category": "rate_limit",
			"code":     "429",
			"model":    "z-ai/glm-5.2",
		},
	})
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindToolCall,
		Severity: conversationSeverityInfo,
		Summary:  "apply_patch",
	})
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindVerification,
		Severity: conversationSeverityError,
		Summary:  "compile_error outside patch scope",
	})

	if event, ok := latestProviderRateLimitEvent(session); ok {
		t.Fatalf("recovered rate-limit must not stay active, got %#v", event)
	}
	items := BuildProactiveSuggestions(SituationSnapshot{}, ProactiveSources{Session: session})
	for _, item := range items {
		if item.Type == "retry_or_switch_model" {
			t.Fatalf("recovered rate-limit must not produce retry_or_switch_model, got %#v", item)
		}
	}
}

// An unrecovered rate-limit for the active model still surfaces the suggestion.
func TestProactiveRateLimitSuggestionStillSurfacesForActiveModel(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openrouter", "z-ai/glm-5.2", "", "default")
	session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindProviderError,
		Severity: conversationSeverityError,
		Summary:  "provider error: 429 Too Many Requests",
		Entities: map[string]string{
			"category": "rate_limit",
			"code":     "429",
			"model":    "z-ai/glm-5.2",
		},
	})
	event, ok := latestProviderRateLimitEvent(session)
	if !ok {
		t.Fatalf("expected active-model rate-limit event")
	}
	if got := event.Entities["model"]; got != "z-ai/glm-5.2" {
		t.Fatalf("expected glm model on event, got %q", got)
	}
	items := BuildProactiveSuggestions(SituationSnapshot{}, ProactiveSources{Session: session})
	if !hasSuggestionType(items, "retry_or_switch_model") {
		t.Fatalf("expected retry_or_switch_model for unrecovered active-model limit, got %#v", items)
	}
	for _, item := range items {
		if item.Type == "retry_or_switch_model" && !strings.Contains(item.Reason, "z-ai/glm-5.2") {
			t.Fatalf("suggestion reason must name the active model, got %#v", item)
		}
	}
}

func TestVerificationGapSuggestionFromChangedPaths(t *testing.T) {
	snapshot := SituationSnapshot{
		ChangedPaths:        []string{"driver/ioctl_dispatch.cpp", "include/ioctl_dispatch.h"},
		MissingVerification: []string{"high-risk Windows security/kernel/anti-cheat files changed without a covering verification report"},
		RiskLevel:           "high",
	}
	items := BuildProactiveSuggestions(snapshot, ProactiveSources{})
	if !hasSuggestionType(items, "run_verification") {
		t.Fatalf("expected run_verification suggestion, got %#v", items)
	}
	if !hasSuggestionType(items, "checkpoint_or_worktree") {
		t.Fatalf("expected checkpoint suggestion for high-risk paths, got %#v", items)
	}
}

func TestFailedVerificationSuggestionUsesRecoverBrief(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.LastVerification = &VerificationReport{
		GeneratedAt: time.Now(),
		Workspace:   root,
		Steps: []VerificationStep{{
			Label:       "go test",
			Command:     "go test ./...",
			Status:      VerificationFailed,
			FailureKind: "test_failure",
			Output:      "FAIL package",
		}},
	}
	items := BuildProactiveSuggestions(SituationSnapshot{}, ProactiveSources{Session: session})
	for _, item := range items {
		if item.Type == "inspect_failure" {
			if item.Command != "/session recover" {
				t.Fatalf("expected failed verification suggestion to use /session recover, got %#v", item)
			}
			return
		}
	}
	t.Fatalf("expected inspect_failure suggestion, got %#v", items)
}

func TestFuzzCrashWithoutMinimizationSuggestion(t *testing.T) {
	root := t.TempDir()
	store := &FuzzCampaignStore{Path: filepath.Join(root, "campaigns.json")}
	_, err := store.Append(FuzzCampaign{
		ID:        "campaign-1",
		Workspace: root,
		Name:      "crash campaign",
		Status:    "active",
		NativeResults: []FuzzCampaignNativeResult{{
			RunID:      "run-1",
			Target:     "ValidateRequest",
			CrashCount: 1,
		}},
	})
	if err != nil {
		t.Fatalf("Append campaign: %v", err)
	}
	snapshot := BuildSituationSnapshot(ProactiveSources{
		Workspace:     Workspace{BaseRoot: root, Root: root},
		Session:       NewSession(root, "provider", "model", "", "default"),
		FuzzCampaigns: store,
	})
	if !hasSuggestionType(snapshot.SuggestionCandidates, "fuzz_next_step") {
		t.Fatalf("expected fuzz_next_step suggestion, got %#v", snapshot.SuggestionCandidates)
	}
}

func TestDismissedSuggestionSurvivesCompactionWorkingMemory(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	mem := session.ensureSuggestionMemory()
	suggestion := normalizeSuggestion(Suggestion{
		Type:     "refresh_analysis",
		Title:    "stale analysis docs refresh",
		Reason:   "stale marker",
		Command:  "/docs-refresh",
		DedupKey: "docs-refresh:stale",
	})
	mem.recordShown(suggestion)
	if _, ok := mem.mark(suggestion.ID, SuggestionStatusDismissed, "not now"); !ok {
		t.Fatalf("expected suggestion mark to succeed")
	}
	rendered := renderCompactionWorkingMemory(session)
	if !strings.Contains(rendered, "Pending suggestions") || !strings.Contains(rendered, "dismissed") {
		t.Fatalf("expected dismissed suggestion in compaction memory, got %q", rendered)
	}
}

func TestSuggestCommandAcceptsSuggestion(t *testing.T) {
	root := t.TempDir()
	var output bytes.Buffer
	session := NewSession(root, "provider", "model", "", "default")
	mem := session.ensureSuggestionMemory()
	suggestion := normalizeSuggestion(Suggestion{
		Type:      "run_verification",
		Title:     "변경 파일에 맞는 verification 실행",
		Reason:    "changed files need verification",
		Command:   "/verify",
		DedupKey:  "verify:test",
		CreatedAt: time.Now(),
	})
	mem.recordShown(suggestion)
	rt := &runtimeState{
		writer:  &output,
		ui:      NewUI(),
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	if err := rt.handleSuggestCommand("accept " + suggestion.ID); err != nil {
		t.Fatalf("handleSuggestCommand: %v", err)
	}
	if session.SuggestionMemory.Records[0].Status != SuggestionStatusAccepted {
		t.Fatalf("expected accepted status, got %#v", session.SuggestionMemory.Records[0])
	}
	if len(session.ConversationEvents) == 0 {
		t.Fatalf("expected accept event")
	}
}

func TestSuggestionDashboardHTMLIntegratesRelatedDashboards(t *testing.T) {
	mem := &SuggestionMemory{
		SchemaVersion: 1,
		Mode:          SuggestionModeSuggest,
		Records: []SuggestionRecord{
			{
				Suggestion: normalizeSuggestion(Suggestion{
					Type:     "run_verification",
					Title:    "변경 파일에 맞는 verification 실행",
					Reason:   "driver change needs verification",
					Command:  "/verify",
					DedupKey: "verify:driver/ioctl.cpp",
				}),
				Status: SuggestionStatusShown,
			},
		},
	}
	snapshot := SituationSnapshot{
		CreatedAt:           time.Now(),
		CurrentGoal:         "finish driver change",
		RiskLevel:           "high",
		MissingVerification: []string{"driver change needs verification"},
		MissingEvidence:     []string{"latest failure is not captured"},
		StaleDocs:           []string{"SECURITY_SURFACE.md: IOCTL section stale"},
		ChangedPaths:        []string{"driver/ioctl.cpp"},
		SuggestionCandidates: []Suggestion{
			normalizeSuggestion(Suggestion{
				Type:         "run_verification",
				Title:        "변경 파일에 맞는 verification 실행",
				Reason:       "driver change needs verification",
				Command:      "/verify",
				EvidenceRefs: []string{"driver/ioctl.cpp"},
				Risk:         "high",
				DedupKey:     "verify:driver/ioctl.cpp",
			}),
			normalizeSuggestion(Suggestion{
				Type:     "refresh_analysis",
				Title:    "stale analysis docs refresh",
				Reason:   "stale marker",
				Command:  "/docs-refresh",
				DedupKey: "docs-refresh:stale",
			}),
		},
	}

	html := renderSuggestionDashboardHTML(Config{}, snapshot, mem)
	for _, want := range []string{
		"Integrated Signals",
		"Dashboard links",
		"/verify dashboard --html",
		"/evidence dashboard --html",
		"/analyze-dashboard",
		"status=shown",
		"Evidence refs",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected dashboard HTML to contain %q, got %q", want, html)
		}
	}
}

func hasSuggestionType(items []Suggestion, typ string) bool {
	for _, item := range items {
		if item.Type == typ {
			return true
		}
	}
	return false
}
