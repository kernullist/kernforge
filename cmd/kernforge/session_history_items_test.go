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
		}},
	})

	approx := session.ApproxChars()
	minAdded := len("sealed-reasoning") + len("completed") + len("call_shell") +
		len("context_compaction") + len("sealed-context") + len("sealed-tool")
	if approx-baseline < minAdded {
		t.Fatalf("expected approx chars to count structured history items, baseline=%d approx=%d minAdded=%d", baseline, approx, minAdded)
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
