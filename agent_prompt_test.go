package main

import (
	"strings"
	"testing"
)

func TestSystemPromptOmitsHeavyCatalogsByDefaultAndSummarizesEnabledSkills(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Skills: SkillCatalog{
			enabled: []Skill{
				{
					Name:    "MemoryOps",
					Summary: "Summarize memory workflow.",
					Content: "### MemoryOps\nSource: /skill\nVery long skill body that should not appear in the system prompt by default.",
				},
			},
		},
		MCP: &MCPManager{
			servers: []*MCPClient{
				{
					config: MCPServerConfig{Name: "docs"},
					resources: []MCPResourceDescriptor{
						{URI: "mcp://docs/index", Name: "Docs Index", Description: "Indexed docs"},
					},
					prompts: []MCPPromptDescriptor{
						{Name: "lookup", Description: "Lookup docs"},
					},
				},
			},
		},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Enabled local skills:") {
		t.Fatalf("expected enabled skill summary in system prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "MemoryOps: Summarize memory workflow.") {
		t.Fatalf("expected enabled skill summary text, got %q", prompt)
	}
	if strings.Contains(prompt, "Very long skill body") || strings.Contains(prompt, "Source: /skill") {
		t.Fatalf("expected full enabled skill body to be omitted, got %q", prompt)
	}
	if strings.Contains(prompt, "Available local skills:") {
		t.Fatalf("expected skill catalog to be omitted by default, got %q", prompt)
	}
	if strings.Contains(prompt, "Available MCP resources:") || strings.Contains(prompt, "Available MCP prompts:") {
		t.Fatalf("expected MCP catalogs to be omitted by default, got %q", prompt)
	}
}

func TestSystemPromptIncludesSkillAndMCPCatalogsWhenUserAsks(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "Show available skills and MCP resources."})
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Skills: SkillCatalog{
			items: []Skill{
				{Name: "MemoryOps", Summary: "Summarize memory workflow."},
			},
		},
		MCP: &MCPManager{
			servers: []*MCPClient{
				{
					config: MCPServerConfig{Name: "docs"},
					resources: []MCPResourceDescriptor{
						{URI: "mcp://docs/index", Name: "Docs Index", Description: "Indexed docs"},
					},
					prompts: []MCPPromptDescriptor{
						{Name: "lookup", Description: "Lookup docs"},
					},
				},
			},
		},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Available local skills:") {
		t.Fatalf("expected skill catalog in system prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Available MCP resources:") || !strings.Contains(prompt, "Available MCP prompts:") {
		t.Fatalf("expected MCP catalogs in system prompt, got %q", prompt)
	}
}

func TestSystemPromptMarksAnalysisOnlyRequestsAsReadOnly(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@Tavern/Common/ETWConsumer.cpp ETWConsumer가 제대로 동작할 수 없는 로그를 수집했는데 원인을 분석해"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "The latest user request is analysis-only.") {
		t.Fatalf("expected analysis-only guard in system prompt, got %q", prompt)
	}
}

func TestSystemPromptMarksExplicitFixRequestsAsToolDriven(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "The latest user request explicitly asks for a fix.") {
		t.Fatalf("expected explicit fix guard in system prompt, got %q", prompt)
	}
}

func TestSystemPromptForbidsGitMutationsWithoutExplicitRequest(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@VAllocAnalyzer.cpp 코드에 버그가 있는지 검토하고 수정해줘"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Do not stage, commit, push, or open a PR unless the user explicitly asks") {
		t.Fatalf("expected git mutation guard in system prompt, got %q", prompt)
	}
}

func TestSystemPromptExplainsCachedReadAndGrepHints(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "큰 cpp 파일 수정 이어서 진행해줘"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "If read_file returns a NOTE about cached content") {
		t.Fatalf("expected cached read_file guidance in system prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "If grep results include [cached-nearby:inside] or [cached-nearby:N]") {
		t.Fatalf("expected cached-nearby grep guidance in system prompt, got %q", prompt)
	}
}

func TestLooksLikeExplicitGitIntentIgnoresCodeOnlyCommitMentions(t *testing.T) {
	if looksLikeExplicitGitIntent("fix commit parsing in the hook engine") {
		t.Fatalf("expected code-only commit mention to stay false")
	}
	if looksLikeExplicitGitIntent("investigate push retry logic in provider.go") {
		t.Fatalf("expected code-only push mention to stay false")
	}
	if !looksLikeExplicitGitIntent("commit these changes after the fix") {
		t.Fatalf("expected explicit commit request to be true")
	}
}
