package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionApproxCharsCountsCodexStructuredHistoryItems(t *testing.T) {
	session := NewSession("C:\\workspace", "openai-codex", "gpt-5", "", "default")
	baseline := session.ApproxChars()
	session.AddMessage(Message{
		Role:                      "assistant",
		ReasoningEncryptedContent: "sealed-reasoning",
		WebSearchCalls: []MessageWebSearchCall{{
			ID:     "ws_1",
			Status: "completed",
			Action: map[string]any{
				"type":  "search",
				"query": "codex",
			},
		}},
		LocalShellCalls: []MessageLocalShellCall{{
			ID:     "ls_1",
			CallID: "call_shell",
			Status: "completed",
			Action: map[string]any{
				"type":    "exec",
				"command": []any{"echo", "hi"},
			},
		}},
		CodexCompactionItems: []MessageCodexCompactionItem{{
			Type:             "context_compaction",
			EncryptedContent: "sealed-context",
		}},
		ToolContentItems: []ToolContentItem{{
			Type:             "text",
			Text:             "tool text",
			EncryptedContent: "sealed-tool",
		}, {
			Type:     "input_image",
			ImageURL: "data:image/png;base64," + strings.Repeat("A", 2048),
			Detail:   imageDetailHigh,
		}},
	})

	approx := session.ApproxChars()
	minAdded := len("ws_1") + len("completed") + len("call_shell") + len("sealed-tool") + codexResizedImageBytesEstimate
	if approx-baseline < minAdded {
		t.Fatalf("expected approx chars to count structured history items, baseline=%d approx=%d minAdded=%d", baseline, approx, minAdded)
	}
}

func TestSessionAndCompactCostsUseCodexEncryptedReasoningEstimate(t *testing.T) {
	reasoning := strings.Repeat("R", 1200)
	compaction := strings.Repeat("C", 1600)
	msg := Message{
		Role:                      "assistant",
		ReasoningEncryptedContent: reasoning,
		CodexCompactionItems: []MessageCodexCompactionItem{{
			Type:             "context_compaction",
			EncryptedContent: compaction,
		}},
	}
	expected := encryptedReasoningApproxChars(len(reasoning)) + encryptedReasoningApproxChars(len(compaction))
	if got := compactMessageRetainedCharCost(msg); got != expected {
		t.Fatalf("compact encrypted reasoning cost = %d, want %d", got, expected)
	}

	session := NewSession("C:\\workspace", "openai-codex", "gpt-5", "", "default")
	baseline := session.ApproxChars()
	session.AddMessage(msg)
	if got := session.ApproxChars() - baseline; got != expected {
		t.Fatalf("session encrypted reasoning cost = %d, want %d", got, expected)
	}
}

func TestCompactMessageRetainedCharCostUsesCodexImageEstimate(t *testing.T) {
	payload := strings.Repeat("B", 12000)
	imageURL := "data:image/png;base64," + payload
	msg := Message{
		Role: "tool",
		ToolContentItems: []ToolContentItem{{
			Type:     "input_image",
			ImageURL: imageURL,
			Detail:   imageDetailHigh,
		}},
	}

	got := compactMessageRetainedCharCost(msg)
	raw := len("input_image") + len(imageDetailHigh) + len(imageURL)
	expected := len("input_image") + len(imageDetailHigh) + len(imageURL) - len(payload) + codexResizedImageBytesEstimate
	if got != expected {
		t.Fatalf("compact char cost = %d, want %d", got, expected)
	}
	if got >= raw {
		t.Fatalf("compact char cost should discount inline image payload, got=%d raw=%d", got, raw)
	}
}

func TestCompactAndSessionCostsCountMessageImages(t *testing.T) {
	image := MessageImage{
		Path:      "shot.png",
		MediaType: "image/png",
		Detail:    imageDetailHigh,
	}
	msg := Message{
		Role:   "user",
		Images: []MessageImage{image},
	}
	expected := messageImageApproxChars("", image)
	if got := compactMessageRetainedCharCost(msg); got != expected {
		t.Fatalf("compact image char cost = %d, want %d", got, expected)
	}

	session := NewSession("C:\\workspace", "openai-codex", "gpt-5", "", "default")
	baseline := session.ApproxChars()
	session.AddMessage(msg)
	if got := session.ApproxChars() - baseline; got < expected {
		t.Fatalf("session image char cost = %d, want at least %d", got, expected)
	}
}

func TestSessionApproxCharsUsesOriginalImagePatchEstimate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, largePNGForTest(t, 96, 64), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	image := MessageImage{
		Path:      "shot.png",
		MediaType: "image/png",
		Detail:    imageDetailOriginal,
	}
	session := NewSession(dir, "openai-codex", "gpt-5", "", "default")
	baseline := session.ApproxChars()
	session.AddMessage(Message{
		Role:   "user",
		Images: []MessageImage{image},
	})
	expected := len(image.Path) + len(image.MediaType) + len(image.Detail) + 3*2*codexApproxBytesPerToken
	if got := session.ApproxChars() - baseline; got != expected {
		t.Fatalf("session original image char cost = %d, want %d", got, expected)
	}
}

func TestSessionExportTextIncludesCodexStructuredHistoryItems(t *testing.T) {
	session := NewSession("C:\\workspace", "openai-codex", "gpt-5", "", "default")
	session.AddMessage(Message{
		Role: "assistant",
		WebSearchCalls: []MessageWebSearchCall{{
			ID:     "ws_123",
			Status: "completed",
			Action: map[string]any{
				"type":  "search",
				"query": "codex hosted tools",
			},
		}},
		LocalShellCalls: []MessageLocalShellCall{{
			CallID: "call_shell",
			Status: "completed",
			Action: map[string]any{
				"type": "exec",
			},
		}},
		CodexCompactionItems: []MessageCodexCompactionItem{{
			Type:             "context_compaction",
			EncryptedContent: "sealed-context",
		}},
	})

	exported := session.ExportText()
	for _, want := range []string{"web_search: id=ws_123 completed", "codex hosted tools", "local_shell_call: call_shell completed", "codex_context_compaction", "encrypted content present"} {
		if !strings.Contains(exported, want) {
			t.Fatalf("expected exported session to include %q, got:\n%s", want, exported)
		}
	}
}
