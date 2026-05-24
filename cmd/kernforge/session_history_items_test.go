package main

import (
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
	minAdded := len("sealed-reasoning") + len("completed") + len("call_shell") +
		len("context_compaction") + len("sealed-context") + len("sealed-tool") +
		codexResizedImageBytesEstimate
	if approx-baseline < minAdded {
		t.Fatalf("expected approx chars to count structured history items, baseline=%d approx=%d minAdded=%d", baseline, approx, minAdded)
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
	expected := messageImageApproxChars(image)
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

func TestSessionExportTextIncludesCodexStructuredHistoryItems(t *testing.T) {
	session := NewSession("C:\\workspace", "openai-codex", "gpt-5", "", "default")
	session.AddMessage(Message{
		Role: "assistant",
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
	for _, want := range []string{"local_shell_call: call_shell completed", "codex_context_compaction", "encrypted content present"} {
		if !strings.Contains(exported, want) {
			t.Fatalf("expected exported session to include %q, got:\n%s", want, exported)
		}
	}
}
