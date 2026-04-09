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
