package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setTempUserConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

type agentsMDTestFileInfo struct {
	mode os.FileMode
}

func (info agentsMDTestFileInfo) Name() string {
	return "AGENTS.md"
}

func (info agentsMDTestFileInfo) Size() int64 {
	return 0
}

func (info agentsMDTestFileInfo) Mode() os.FileMode {
	return info.mode
}

func (info agentsMDTestFileInfo) ModTime() time.Time {
	return time.Time{}
}

func (info agentsMDTestFileInfo) IsDir() bool {
	return info.mode.IsDir()
}

func (info agentsMDTestFileInfo) Sys() any {
	return nil
}

func TestAgentsMDFileIsRegularMatchesCodexFileOnlyPolicy(t *testing.T) {
	if !agentsMDFileIsRegular(agentsMDTestFileInfo{mode: 0o644}) {
		t.Fatalf("expected regular AGENTS.md file to be readable")
	}
	if agentsMDFileIsRegular(nil) {
		t.Fatalf("nil file info must not be readable")
	}
	if agentsMDFileIsRegular(agentsMDTestFileInfo{mode: os.ModeDir | 0o755}) {
		t.Fatalf("directory AGENTS.md must not be readable")
	}
	if agentsMDFileIsRegular(agentsMDTestFileInfo{mode: os.ModeNamedPipe | 0o644}) {
		t.Fatalf("special AGENTS.md file must not be readable")
	}
}

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

func TestSystemPromptIncludesActivePermissionProfileSnapshot(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", string(ModePlan))
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
			Perms:    NewPermissionManager(ModeBypass, nil),
		},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Permission mode: "+string(ModeBypass)) {
		t.Fatalf("expected live permission mode line, got %q", prompt)
	}
	if !strings.Contains(prompt, "Active permission profile: "+builtInPermissionProfileDangerFullAccess) {
		t.Fatalf("expected live permission profile snapshot in system prompt, got %q", prompt)
	}
}

func TestSystemPromptIncludesProjectAgentsMDInstructions(t *testing.T) {
	setTempUserConfigHome(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git marker: %v", err)
	}
	nested := filepath.Join(root, "pkg", "worker")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root project instruction"), 0o644); err != nil {
		t.Fatalf("write root AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("nested default instruction should lose to override"), 0o644); err != nil {
		t.Fatalf("write nested AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.override.md"), []byte("nested override instruction"), 0o644); err != nil {
		t.Fatalf("write nested AGENTS.override.md: %v", err)
	}
	session := NewSession(nested, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     nested,
		},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "# AGENTS.md instructions for "+nested) {
		t.Fatalf("expected AGENTS.md contextual header, got %q", prompt)
	}
	rootIndex := strings.Index(prompt, "root project instruction")
	nestedIndex := strings.Index(prompt, "nested override instruction")
	if rootIndex < 0 || nestedIndex < 0 || rootIndex > nestedIndex {
		t.Fatalf("expected root AGENTS.md before nested override, got %q", prompt)
	}
	if strings.Contains(prompt, "nested default instruction should lose to override") {
		t.Fatalf("expected AGENTS.override.md to win over AGENTS.md in same directory, got %q", prompt)
	}
}

func TestSystemPromptDiscoversGitRootAgentsMDFromNestedSessionRoot(t *testing.T) {
	setTempUserConfigHome(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git marker: %v", err)
	}
	nested := filepath.Join(root, "pkg", "worker")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("repo root instruction"), 0o644); err != nil {
		t.Fatalf("write root AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("nested instruction"), 0o644); err != nil {
		t.Fatalf("write nested AGENTS.md: %v", err)
	}
	session := NewSession(nested, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: nested,
			Root:     nested,
		},
	}

	prompt := agent.systemPrompt()
	rootIndex := strings.Index(prompt, "repo root instruction")
	nestedIndex := strings.Index(prompt, "nested instruction")
	if rootIndex < 0 || nestedIndex < 0 {
		t.Fatalf("expected repo root and nested AGENTS.md instructions, got %q", prompt)
	}
	if rootIndex > nestedIndex {
		t.Fatalf("expected repo root AGENTS.md before nested AGENTS.md, got %q", prompt)
	}
}

func TestSystemPromptDiscoversAgentsMDThroughSymlinkedWorkingDir(t *testing.T) {
	setTempUserConfigHome(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git marker: %v", err)
	}
	nested := filepath.Join(root, "pkg", "worker")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("real root instruction"), 0o644); err != nil {
		t.Fatalf("write root AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("real nested instruction"), 0o644); err != nil {
		t.Fatalf("write nested AGENTS.md: %v", err)
	}
	link := filepath.Join(t.TempDir(), "worker-link")
	if err := os.Symlink(nested, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	session := NewSession(link, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: link,
			Root:     link,
		},
	}

	prompt := agent.systemPrompt()
	rootIndex := strings.Index(prompt, "real root instruction")
	nestedIndex := strings.Index(prompt, "real nested instruction")
	if rootIndex < 0 || nestedIndex < 0 {
		t.Fatalf("expected symlinked cwd to resolve real repo AGENTS.md chain, got %q", prompt)
	}
	if rootIndex > nestedIndex {
		t.Fatalf("expected real root AGENTS.md before nested AGENTS.md, got %q", prompt)
	}
}

func TestSystemPromptPreservesProjectAgentsMDWhitespaceBetweenFiles(t *testing.T) {
	setTempUserConfigHome(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git marker: %v", err)
	}
	nested := filepath.Join(root, "pkg", "worker")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("root doc\n\n"), 0o644); err != nil {
		t.Fatalf("write root AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("  child doc\n"), 0o644); err != nil {
		t.Fatalf("write nested AGENTS.md: %v", err)
	}
	session := NewSession(nested, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     nested,
		},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "root doc\n\n\n\n  child doc\n") {
		t.Fatalf("expected project AGENTS.md whitespace to be preserved like Codex, got %q", prompt)
	}
}

func TestSystemPromptWithoutProjectRootMarkerUsesCWDOnly(t *testing.T) {
	setTempUserConfigHome(t)
	root := t.TempDir()
	nested := filepath.Join(root, "pkg", "worker")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("unmarked parent instruction"), 0o644); err != nil {
		t.Fatalf("write root AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("unmarked cwd instruction"), 0o644); err != nil {
		t.Fatalf("write nested AGENTS.md: %v", err)
	}
	session := NewSession(nested, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     nested,
		},
	}

	prompt := agent.systemPrompt()
	if strings.Contains(prompt, "unmarked parent instruction") {
		t.Fatalf("expected parent AGENTS.md to be ignored without project root marker, got %q", prompt)
	}
	if !strings.Contains(prompt, "unmarked cwd instruction") {
		t.Fatalf("expected cwd AGENTS.md to be included, got %q", prompt)
	}
}

func TestSystemPromptUsesConfiguredProjectRootMarker(t *testing.T) {
	setTempUserConfigHome(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".kernforge-root"), []byte("marker"), 0o644); err != nil {
		t.Fatalf("write custom root marker: %v", err)
	}
	nested := filepath.Join(root, "pkg", "worker")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("custom marker root instruction"), 0o644); err != nil {
		t.Fatalf("write root AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("custom marker nested instruction"), 0o644); err != nil {
		t.Fatalf("write nested AGENTS.md: %v", err)
	}
	markers := []string{".kernforge-root"}
	session := NewSession(nested, "provider", "model", "", "default")
	agent := &Agent{
		Config: Config{
			ProjectRootMarkers: &markers,
		},
		Session: session,
		Workspace: Workspace{
			BaseRoot: nested,
			Root:     nested,
		},
	}

	prompt := agent.systemPrompt()
	rootIndex := strings.Index(prompt, "custom marker root instruction")
	nestedIndex := strings.Index(prompt, "custom marker nested instruction")
	if rootIndex < 0 || nestedIndex < 0 {
		t.Fatalf("expected custom marker root and nested AGENTS.md instructions, got %q", prompt)
	}
	if rootIndex > nestedIndex {
		t.Fatalf("expected root AGENTS.md before nested AGENTS.md, got %q", prompt)
	}
}

func TestSystemPromptProjectRootMarkersCanDisableParentTraversal(t *testing.T) {
	setTempUserConfigHome(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git marker: %v", err)
	}
	nested := filepath.Join(root, "pkg", "worker")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("disabled parent root instruction"), 0o644); err != nil {
		t.Fatalf("write root AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("disabled parent nested instruction"), 0o644); err != nil {
		t.Fatalf("write nested AGENTS.md: %v", err)
	}
	markers := []string{}
	session := NewSession(nested, "provider", "model", "", "default")
	agent := &Agent{
		Config: Config{
			ProjectRootMarkers: &markers,
		},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     nested,
		},
	}

	prompt := agent.systemPrompt()
	if strings.Contains(prompt, "disabled parent root instruction") {
		t.Fatalf("expected empty project_root_markers to disable parent AGENTS.md traversal, got %q", prompt)
	}
	if !strings.Contains(prompt, "disabled parent nested instruction") {
		t.Fatalf("expected cwd AGENTS.md to remain visible, got %q", prompt)
	}
}

func TestSystemPromptUsesProjectDocFallbackFilenames(t *testing.T) {
	setTempUserConfigHome(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("fallback project instruction"), 0o644); err != nil {
		t.Fatalf("write fallback project doc: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	agent := &Agent{
		Config: Config{
			ProjectDocFallbackFilenames: []string{"CLAUDE.md"},
		},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "fallback project instruction") {
		t.Fatalf("expected configured fallback project doc in prompt, got %q", prompt)
	}
}

func TestSystemPromptCanDisableProjectAgentsMDInstructions(t *testing.T) {
	setTempUserConfigHome(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("disabled project instruction"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	agent := &Agent{
		Config: Config{
			ProjectDocMaxBytes: intPtr(0),
		},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	prompt := agent.systemPrompt()
	if strings.Contains(prompt, "disabled project instruction") || strings.Contains(prompt, "# AGENTS.md instructions for ") {
		t.Fatalf("expected project docs to be disabled, got %q", prompt)
	}
}

func TestSystemPromptIncludesGlobalAgentsMDInstructions(t *testing.T) {
	home := setTempUserConfigHome(t)
	configDir := filepath.Join(home, userConfigDirName)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir user config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "AGENTS.md"), []byte("global user instruction"), 0o644); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "# AGENTS.md instructions for "+root) {
		t.Fatalf("expected AGENTS.md contextual header, got %q", prompt)
	}
	if !strings.Contains(prompt, "global user instruction") {
		t.Fatalf("expected global AGENTS.md instruction in prompt, got %q", prompt)
	}
}

func TestSystemPromptPrefersGlobalAgentsOverride(t *testing.T) {
	home := setTempUserConfigHome(t)
	configDir := filepath.Join(home, userConfigDirName)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir user config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "AGENTS.md"), []byte("global default instruction"), 0o644); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "AGENTS.override.md"), []byte("global override instruction"), 0o644); err != nil {
		t.Fatalf("write global AGENTS.override.md: %v", err)
	}
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "global override instruction") {
		t.Fatalf("expected global AGENTS.override.md in prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "global default instruction") {
		t.Fatalf("expected global AGENTS.override.md to win over AGENTS.md, got %q", prompt)
	}
}

func TestSystemPromptCombinesGlobalAndProjectAgentsMDInstructions(t *testing.T) {
	home := setTempUserConfigHome(t)
	configDir := filepath.Join(home, userConfigDirName)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir user config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "AGENTS.md"), []byte("global user instruction"), 0o644); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("project instruction"), 0o644); err != nil {
		t.Fatalf("write project AGENTS.md: %v", err)
	}
	session := NewSession(root, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	prompt := agent.systemPrompt()
	globalIndex := strings.Index(prompt, "global user instruction")
	separatorIndex := strings.Index(prompt, agentsMDSeparator)
	projectIndex := strings.Index(prompt, "project instruction")
	if globalIndex < 0 || separatorIndex < 0 || projectIndex < 0 {
		t.Fatalf("expected global/project AGENTS.md instructions and separator, got %q", prompt)
	}
	if !(globalIndex < separatorIndex && separatorIndex < projectIndex) {
		t.Fatalf("expected global instructions before project instructions, got %q", prompt)
	}
}

func TestSystemPromptExplainsAgentsMDScopePolicy(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	agent := &Agent{
		Config:  Config{},
		Session: session,
		Workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}

	prompt := agent.systemPrompt()
	for _, want := range []string{
		"AGENTS.md policy:",
		"loaded instructions apply to their directory tree",
		"nested files take precedence",
		"outside the loaded AGENTS.md scope",
		"AGENTS.override.md or AGENTS.md",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected AGENTS.md scope policy %q in system prompt, got %q", want, prompt)
		}
	}
}

func TestSystemPromptUsesExternalUserRequestAfterInternalSteering(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "RuntimeManager.cpp 버그를 찾아서 수정해"})
	session.AddMessage(Message{Role: "user", Text: "Reviewer feedback: revise the final answer before concluding."})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "explicitly asks for a fix") {
		t.Fatalf("expected system prompt to preserve external edit intent, got %q", prompt)
	}
}

func TestSystemPromptTreatsModifiedCodeReviewAsReadOnly(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "수정한 코드에 버그는 없는지 검토해"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "analysis-only") {
		t.Fatalf("expected modified-code review request to be read-only, got %q", prompt)
	}
	if strings.Contains(prompt, "The latest user request explicitly asks for a fix.") {
		t.Fatalf("modified-code review request should not be prompted as an edit request, got %q", prompt)
	}
}

func TestSystemPromptUsesExternalUserRequestAfterGoalSteering(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "Fix the RuntimeManager.cpp bug."})
	session.AddMessage(Message{Role: "user", Text: buildGoalImplementationPrompt(GoalState{
		Objective: "Codex repo와 kernforge 전체 코드를 비교 분석해",
	}, 2)})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "explicitly asks for a fix") {
		t.Fatalf("expected system prompt to preserve external edit intent after goal steering, got %q", prompt)
	}
	if strings.Contains(prompt, "Respond in Korean because the latest user request is written in Korean") {
		t.Fatalf("goal steering should not become the latest external language request, got %q", prompt)
	}
}

func TestSystemPromptPreservesEditIntentForContinuationSteering(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	original := "Codex upstream과 kernforge를 비교해서 turn orchestration을 수정해"
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: original}
	session.TaskState = &TaskState{Goal: original}
	session.AddMessage(Message{Role: "user", Text: original})
	session.AddMessage(Message{Role: "assistant", Text: "provider role 분리를 적용했습니다."})
	session.AddMessage(Message{Role: "user", Text: "좋아 너무 작은 기능까지 먼저 확인하지 말고 전체적인 큰 흐름과 관련된 것들 위주로 먼저 확인하자"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "explicitly asks for a fix") {
		t.Fatalf("expected continuation steering to keep preserved edit intent in system prompt, got %q", prompt)
	}
}

func TestSystemPromptFallsBackToAcceptanceContextWhenOnlyInternalMessagesRemain(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{SourcePrompt: "Fix the RuntimeManager.cpp bug."}
	session.TaskState = &TaskState{Goal: "Fix the RuntimeManager.cpp bug."}
	session.AddMessage(internalUserMessage("Additional turn context for the preceding user request:\nRequest mode: inspect-and-fix."))
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "explicitly asks for a fix") {
		t.Fatalf("expected system prompt to recover preserved edit intent, got %q", prompt)
	}
	if strings.Contains(prompt, "The user prompt may include") {
		t.Fatalf("system prompt should not describe internal context as part of the user prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Separate internal context messages may include") {
		t.Fatalf("expected system prompt to explain separated internal context, got %q", prompt)
	}
}

func TestSystemPromptIncludesEffectiveWorkspaceRoots(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := filepath.Join(baseRoot, "worktrees", "feature")
	session := NewSession(baseRoot, "provider", "model", "", "default")
	session.WorkingDir = activeRoot
	agent := &Agent{
		Config:    Config{},
		Session:   session,
		Workspace: Workspace{BaseRoot: baseRoot, Root: activeRoot},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Workspace root: "+activeRoot) {
		t.Fatalf("expected prompt to show active workspace root, got %q", prompt)
	}
	if !strings.Contains(prompt, "Workspace base root: "+baseRoot) {
		t.Fatalf("expected prompt to show base workspace root, got %q", prompt)
	}
	if !strings.Contains(prompt, "Workspace roots: "+baseRoot+", "+activeRoot) {
		t.Fatalf("expected prompt to show effective workspace roots, got %q", prompt)
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

func TestSystemPromptUsesLatestUserQuestionLanguage(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "SampleKernel 문서를 검토하고 개선점을 찾아줘"})
	agent := &Agent{
		Config:  Config{AutoLocale: boolPtr(false)},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Respond in Korean because the latest user request is written in Korean") {
		t.Fatalf("expected Korean response language policy from question language, got %q", prompt)
	}
}

func TestSystemPromptUsesKoreanLocaleForEnglishLeadingAmbiguousPrompt(t *testing.T) {
	t.Setenv("LANG", "ko_KR.UTF-8")

	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "SampleApp review"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Respond in Korean because the configured/system locale prefers Korean") {
		t.Fatalf("expected Korean response language policy from locale, got %q", prompt)
	}
	if !strings.Contains(prompt, "A leading English code identifier") {
		t.Fatalf("expected leading-English safeguard in language policy, got %q", prompt)
	}
}

func TestSystemPromptKeepsKoreanForEnglishLeadingMixedRequest(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "SampleApp review 시스템을 테스트해보자"})
	agent := &Agent{
		Config:  Config{AutoLocale: boolPtr(false)},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Respond in Korean because the latest user request is written in Korean") {
		t.Fatalf("expected Korean response language policy from mixed Korean request, got %q", prompt)
	}
}

func TestSystemPromptStillUsesEnglishForNaturalEnglishRequest(t *testing.T) {
	t.Setenv("LANG", "ko_KR.UTF-8")

	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "Please review this patch and explain the risk."})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Respond in English because the latest user request is written in English") {
		t.Fatalf("expected English response language policy from natural English request, got %q", prompt)
	}
}

func TestSystemPromptExplicitLanguageOverridesQuestionLanguage(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "SampleKernel 문서를 검토해. Answer in English."})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Always respond in English because the latest user request explicitly asks for English") {
		t.Fatalf("expected explicit English response language policy, got %q", prompt)
	}
}

func TestSystemPromptIncludesWebResearchGuidanceAndRelevantMCPCapabilities(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "Hypervisor 기반 게임핵 탐지 최신 기술들을 리서치하고 설계 문서를 작성해줘"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
		MCP: &MCPManager{
			servers: []*MCPClient{
				{
					config: MCPServerConfig{Name: "web"},
					tools: []MCPToolDescriptor{
						{Name: "search_web", Description: "Search the web for current articles and references"},
						{Name: "echo", Description: "Echo a message"},
					},
					prompts: []MCPPromptDescriptor{
						{Name: "browse_url", Description: "Fetch and summarize a URL"},
					},
				},
			},
		},
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "likely needs current external research") {
		t.Fatalf("expected latest research guidance, got %q", prompt)
	}
	if !strings.Contains(prompt, "Relevant MCP web/research capabilities:") {
		t.Fatalf("expected relevant web MCP catalog, got %q", prompt)
	}
	if !strings.Contains(prompt, "mcp__web__search_web") {
		t.Fatalf("expected namespaced web tool in prompt, got %q", prompt)
	}
	if strings.Contains(prompt, "Echo a message") {
		t.Fatalf("expected non-web MCP tool to be filtered out, got %q", prompt)
	}
}

func TestExplicitWebResearchIntentMatchesCurrentResearchPrompt(t *testing.T) {
	for _, request := range []string{
		"Hypervisor 기반 게임핵 탐지 최신 기술들을 리서치하고 설계 문서를 작성해줘",
		"Research latest hypervisor anti-cheat detection techniques.",
		"최신 안티치트 동향을 조사해줘",
	} {
		if !requestExplicitlyAsksForWebResearch(request) {
			t.Fatalf("expected current research request to count as explicit web research: %q", request)
		}
	}
}

func TestSystemPromptDoesNotSuggestWebResearchForLocalCodeRepair(t *testing.T) {
	root := t.TempDir()
	for _, request := range []string{
		"@SampleApp/SampleWorker/PathConverter.cpp:132-221 검토하고 버그를 수정해",
		"FocusedRuntime 코드를 분석해서 서버 성능이나 히칭에 영향을 줄 수 있는 부분을 검토해줘",
		"Automatic pre-write review found actionable warnings. Revise the proposed edit before writing files.\n\nImplementation rules:\n- This is local code review/repair work. Do not use MCP web/search/browser tools or external web research to satisfy this gate.",
	} {
		session := NewSession(root, "provider", "model", "", "default")
		session.AddMessage(Message{Role: "user", Text: request})
		agent := &Agent{
			Config:  Config{},
			Session: session,
			MCP: &MCPManager{
				servers: []*MCPClient{
					{
						config: MCPServerConfig{Name: "web"},
						tools: []MCPToolDescriptor{
							{Name: "search_web", Description: "Search the web for current articles and references"},
						},
					},
				},
			},
		}

		prompt := agent.systemPrompt()
		if strings.Contains(prompt, "likely needs current external research") ||
			strings.Contains(prompt, "Relevant MCP web/research capabilities:") {
			t.Fatalf("local code repair should not be prompted toward web research for %q, got %q", request, prompt)
		}
		if !strings.Contains(prompt, "For local code review or repair tasks, do not use MCP web/search/browser tools") {
			t.Fatalf("expected local-code web restriction in prompt for %q, got %q", request, prompt)
		}
	}
}

func TestSystemPromptWarnsWhenLatestResearchNeedsWebButNoCapabilityConfigured(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "최신 안티치트 동향을 조사해줘"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "no obvious MCP web-search/browser capability is configured") {
		t.Fatalf("expected missing web capability warning, got %q", prompt)
	}
	if !strings.Contains(prompt, "Do not pretend to have live web results.") {
		t.Fatalf("expected no-fabrication guidance, got %q", prompt)
	}
}

func TestSystemPromptMarksAnalysisOnlyRequestsAsReadOnly(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@SampleApp/Common/ETWConsumer.cpp ETWConsumer가 제대로 동작할 수 없는 로그를 수집했는데 원인을 분석해"})
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

func TestSystemPromptIncludesNarrowPatchPayloadGuidance(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@Sample.cpp 버그를 수정해줘"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	for _, want := range []string{
		"prefer narrow hunks anchored to current file contents",
		"apply the first independent hunk",
		"large tool-call payload",
		"include the required RF hunks as separate narrow hunks",
		"Prefer dedicated workspace tools such as read_file, grep, git_diff, git_status, and list_files",
		"Do not use run_shell with Get-Content",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected narrow patch payload guidance %q in system prompt, got %q", want, prompt)
		}
	}
}

func TestSystemPromptDocumentsScopedMutatingShellContract(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "@Sample.cpp 버그를 수정해줘"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	for _, want := range []string{
		"allow_workspace_writes=true",
		"write_paths",
		"formatter, code generator, or setup command",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected scoped mutating shell contract %q in system prompt, got %q", want, prompt)
		}
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

func TestSystemPromptExplainsDocumentReadConfirmationGuidance(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.AddMessage(Message{Role: "user", Text: "리서치 문서를 markdown 파일로 작성해줘"})
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "prefer edit tools. Do not use run_shell for repo bootstrap") {
		t.Fatalf("expected document edit guidance, got %q", prompt)
	}
	if !strings.Contains(prompt, "Do not use run_shell with Set-Content") {
		t.Fatalf("expected source-edit shell safety guidance, got %q", prompt)
	}
	if !strings.Contains(prompt, "Use list_files on the parent directory before read_file") {
		t.Fatalf("expected document read confirmation guidance, got %q", prompt)
	}
}

func TestSystemPromptIncludesActiveBackgroundBundles(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.BackgroundBundles = []BackgroundShellBundle{{
		ID:               "bundle-1",
		Status:           "running",
		CommandSummaries: []string{"go test ./pkg/...", "ctest --output-on-failure"},
		JobIDs:           []string{"job-1", "job-2"},
		LastSummary:      "completed=1 running=1 failed=0 total=2",
	}}
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Active background shell bundles:") {
		t.Fatalf("expected active background bundle section in system prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "bundle-1 [running] jobs=2") {
		t.Fatalf("expected bundle details in system prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "bundle_id=\"latest\"") {
		t.Fatalf("expected latest bundle polling guidance in system prompt, got %q", prompt)
	}
}

func TestSystemPromptIncludesStructuredTaskGraph(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "provider", "model", "", "default")
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{ID: "plan-01", Title: "Inspect the issue", Kind: "inspection", Status: "completed"},
			{ID: "plan-02", Title: "Verify the fix", Kind: "verification", Status: "ready", MicroWorkerBrief: "Poll the verification bundle before concluding."},
		},
	}
	session.TaskGraph.Normalize()
	agent := &Agent{
		Config:  Config{},
		Session: session,
	}

	prompt := agent.systemPrompt()
	if !strings.Contains(prompt, "Structured task graph:") {
		t.Fatalf("expected structured task graph section, got %q", prompt)
	}
	if !strings.Contains(prompt, "Task graph nodes: 2") {
		t.Fatalf("expected task graph summary in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Verify the fix [ready]") {
		t.Fatalf("expected ready node details in prompt, got %q", prompt)
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

func TestSummarizeToolCompletionForReadFile(t *testing.T) {
	summary := summarizeToolCompletion(Config{AutoLocale: boolPtr(false)}, ToolCall{
		Name:      "read_file",
		Arguments: `{"path":"main.go","start_line":1,"end_line":3}`,
	}, "line1\nline2\nline3\n")

	if summary != "read_file loaded main.go (3 line(s))." {
		t.Fatalf("unexpected read_file summary: %q", summary)
	}
}

func TestSummarizeToolCompletionForRunShellUsesFirstOutputLine(t *testing.T) {
	summary := summarizeToolCompletion(Config{AutoLocale: boolPtr(false)}, ToolCall{
		Name:      "run_shell",
		Arguments: `{"command":"go test ./..."}`,
	}, "\nPASS\nok   kernforge  0.123s\n")

	if summary != "run_shell completed: PASS" {
		t.Fatalf("unexpected run_shell summary: %q", summary)
	}
}

func TestSummarizeToolCompletionForListFiles(t *testing.T) {
	summary := summarizeToolCompletion(Config{AutoLocale: boolPtr(false)}, ToolCall{
		Name:      "list_files",
		Arguments: `{"path":"./anti-cheat-research/analysis"}`,
	}, "analysis/testing.md\n")

	if summary != "list_files returned 1 item(s) from ./anti-cheat-research/analysis." {
		t.Fatalf("unexpected list_files summary: %q", summary)
	}
}

func TestSummarizeToolCompletionForGrepKeepsKoreanArgumentOrder(t *testing.T) {
	t.Setenv("LANG", "ko_KR.UTF-8")
	summary := summarizeToolCompletion(Config{}, ToolCall{
		Name:      "grep",
		Arguments: `{"pattern":"StringToLower"}`,
	}, "one\ntwo\nthree\n")

	if strings.Contains(summary, "%!") {
		t.Fatalf("grep summary should not contain fmt placeholder errors, got %q", summary)
	}
	if !strings.Contains(summary, "StringToLower") || !strings.Contains(summary, "3") {
		t.Fatalf("grep summary should include pattern and count, got %q", summary)
	}
}

func TestSummarizeToolFailureTruncatesError(t *testing.T) {
	err := summarizeToolFailure(Config{AutoLocale: boolPtr(false)}, ToolCall{
		Name: "apply_patch",
	}, assertErrString("search text not found in main.go while trying to update a stale block with old line numbers"))

	if !strings.HasPrefix(err, "apply_patch failed: ") {
		t.Fatalf("expected failure prefix, got %q", err)
	}
	if len(err) > 130 {
		t.Fatalf("expected truncated failure summary, got %q", err)
	}
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func assertErrString(text string) error {
	return errString(text)
}
