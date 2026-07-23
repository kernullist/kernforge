package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkillsFindsWorkspaceSkillsAndEnabledDefaults(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	skillDir := filepath.Join(dir, ".kernforge", "skills", "checks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "# Checks\n\nRun tests and report failures before editing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	catalog, warnings := LoadSkills(dir, nil, []string{"checks"})

	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	skill, ok := catalog.Lookup("checks")
	if !ok {
		t.Fatalf("expected checks skill in catalog, got %#v", catalog.Items())
	}
	if !strings.EqualFold(skill.Path, filepath.Join(skillDir, "SKILL.md")) {
		t.Fatalf("expected workspace checks skill path, got %q", skill.Path)
	}
	if catalog.EnabledCount() != 1 {
		t.Fatalf("expected 1 enabled skill, got %d", catalog.EnabledCount())
	}
	prompt := catalog.DefaultPrompt()
	if !strings.Contains(prompt, "Run tests and report failures") {
		t.Fatalf("expected enabled skill content in default prompt, got %q", prompt)
	}
}

func TestSkillCatalogInjectsExplicitSkillContext(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	skillDir := filepath.Join(dir, "skills", "unit-planner")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "# unit-planner\n\nBreak the work into ordered steps.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	catalog, warnings := LoadSkills(dir, nil, nil)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	enriched := catalog.InjectPromptContext("please use $unit-planner for this task")

	if strings.Contains(enriched, "$unit-planner") {
		t.Fatalf("expected explicit skill token to be normalized, got %q", enriched)
	}
	if !strings.Contains(enriched, "Activated skills for this request:") {
		t.Fatalf("expected activated skills section, got %q", enriched)
	}
	if !strings.Contains(enriched, "Break the work into ordered steps.") {
		t.Fatalf("expected injected skill body, got %q", enriched)
	}
}

func TestLoadSkillParsesYAMLFrontmatter(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	skillDir := filepath.Join(dir, ".kernforge", "skills", "raw-folder")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	// Frontmatter name/description must win over the folder name and the first
	// prose line. allowed-tools must parse into a list. Body must drop the
	// frontmatter block.
	content := "---\n" +
		"name: memory-auditor\n" +
		"description: \"Audit persistent memory entries for stale claims before reuse.\"\n" +
		"allowed-tools: Read, Grep\n" +
		"---\n" +
		"# Heading That Should Not Win\n\n" +
		"This prose line should not become the summary.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	catalog, warnings := LoadSkills(dir, nil, nil)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	skill, ok := catalog.Lookup("memory-auditor")
	if !ok {
		t.Fatalf("expected frontmatter name to be used, items=%#v", catalog.Items())
	}
	if skill.Summary != "Audit persistent memory entries for stale claims before reuse." {
		t.Fatalf("expected description as summary, got %q", skill.Summary)
	}
	if len(skill.AllowedTools) != 2 || skill.AllowedTools[0] != "Read" || skill.AllowedTools[1] != "Grep" {
		t.Fatalf("expected allowed-tools [Read Grep], got %#v", skill.AllowedTools)
	}
	if strings.Contains(skill.Content, "name: memory-auditor") {
		t.Fatalf("expected frontmatter to be stripped from content, got %q", skill.Content)
	}
	if !strings.Contains(skill.Content, "# Heading That Should Not Win") {
		t.Fatalf("expected markdown body to be retained, got %q", skill.Content)
	}
}

func TestLoadSkillWithoutFrontmatterFallsBackToHeading(t *testing.T) {
	// Files without a frontmatter block must keep the legacy behavior: name
	// from the first heading and summary from the first prose line.
	dir := t.TempDir()
	isolateUserConfigDir(t)
	skillDir := filepath.Join(dir, ".kernforge", "skills", "legacy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "# legacy-skill\n\nDo the legacy thing carefully.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	catalog, warnings := LoadSkills(dir, nil, nil)
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	skill, ok := catalog.Lookup("legacy-skill")
	if !ok {
		t.Fatalf("expected heading name fallback, items=%#v", catalog.Items())
	}
	if skill.Summary != "Do the legacy thing carefully." {
		t.Fatalf("expected first prose line summary, got %q", skill.Summary)
	}
	if len(skill.AllowedTools) != 0 {
		t.Fatalf("expected no allowed-tools, got %#v", skill.AllowedTools)
	}
}

func TestParseSkillFrontmatterUnterminatedTreatedAsBody(t *testing.T) {
	// An opening "---" with no closing fence is not valid frontmatter and must
	// be returned as body so a horizontal-rule style document is not mangled.
	front, body := parseSkillFrontmatter("---\nname: broken\nstill body\n")
	if len(front) != 0 {
		t.Fatalf("expected no frontmatter keys, got %#v", front)
	}
	if !strings.Contains(body, "name: broken") {
		t.Fatalf("expected full text as body, got %q", body)
	}
}

func TestSkillCatalogSelectableCount(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	for _, name := range []string{"alpha", "beta"} {
		skillDir := filepath.Join(dir, ".kernforge", "skills", name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatalf("mkdir skill dir: %v", err)
		}
		content := "# " + name + "\n\nSummary for " + name + ".\n"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatalf("write SKILL.md: %v", err)
		}
	}
	// Only "alpha" is enabled by default; "beta" stays selectable on demand.
	// (Ancestor traversal of the temp dir can pick up unrelated global skills,
	// so assert relative selectability of our two skills, not an absolute count.)
	catalog, _ := LoadSkills(dir, nil, []string{"alpha"})
	alpha, ok := catalog.Lookup("alpha")
	if !ok || !alpha.Enabled {
		t.Fatalf("expected alpha to be enabled, got %#v ok=%v", alpha, ok)
	}
	beta, ok := catalog.Lookup("beta")
	if !ok || beta.Enabled {
		t.Fatalf("expected beta to be selectable (not enabled), got %#v ok=%v", beta, ok)
	}
	if catalog.SelectableCount() < 1 {
		t.Fatalf("expected at least 1 selectable skill, got %d", catalog.SelectableCount())
	}
}

func TestShouldIncludeSkillCatalogAutoAvailable(t *testing.T) {
	dir := t.TempDir()
	isolateUserConfigDir(t)
	skillDir := filepath.Join(dir, ".kernforge", "skills", "auto")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: auto\ndescription: Auto available skill.\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	catalog, _ := LoadSkills(dir, nil, nil)
	// Auto-availability: catalog is offered even when the prompt never mentions
	// "skill" and uses no literal "$name" token.
	if !shouldIncludeSkillCatalogInSystemPrompt("please refactor this parser", catalog) {
		t.Fatalf("expected catalog to be auto-available when selectable skills exist")
	}
	// With no skills at all, keep the keyword/token gate behavior.
	empty := SkillCatalog{}
	if shouldIncludeSkillCatalogInSystemPrompt("please refactor this parser", empty) {
		t.Fatalf("expected no catalog injection without skills and without keyword")
	}
	if !shouldIncludeSkillCatalogInSystemPrompt("show me the skill list", empty) {
		t.Fatalf("expected keyword gate to still trigger injection")
	}
}

func TestInitSkillTemplateIncludesSkillName(t *testing.T) {
	text := InitSkillTemplate("planner")
	if !strings.Contains(text, "# planner") {
		t.Fatalf("expected heading to include skill name, got %q", text)
	}
	if !strings.Contains(text, "## Workflow") {
		t.Fatalf("expected workflow section, got %q", text)
	}
}

func TestDefaultSkillSearchPathsExcludeLegacyLocations(t *testing.T) {
	paths := defaultSkillSearchPaths(filepath.Join("workspace", "repo"))
	for _, path := range paths {
		lower := strings.ToLower(filepath.ToSlash(path))
		if strings.Contains(lower, ".imcli") {
			t.Fatalf("unexpected legacy skill path: %s", path)
		}
	}
}

func TestSkillSearchProjectDirsStopsAtProjectRoot(t *testing.T) {
	root := t.TempDir()
	// Simulate a repo root with go.mod and a nested cwd under the user-like tree.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	nested := filepath.Join(root, "cmd", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	// A fake "home" skill dir above the repo must not be walked into.
	dirs := skillSearchProjectDirs(nested)
	if len(dirs) == 0 {
		t.Fatal("expected at least cwd")
	}
	if dirs[0] != nested && filepath.Clean(dirs[0]) != filepath.Clean(nested) {
		// Abs may differ in clean form.
		absNested, _ := filepath.Abs(nested)
		if filepath.Clean(dirs[0]) != filepath.Clean(absNested) {
			t.Fatalf("nearest dir should be cwd, got %q want %q", dirs[0], absNested)
		}
	}
	foundRoot := false
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			foundRoot = true
		}
		// Must not climb to parent of repo root.
		if filepath.Clean(dir) == filepath.Clean(filepath.Dir(root)) {
			t.Fatalf("must stop at project root, climbed to %q", dir)
		}
	}
	if !foundRoot {
		t.Fatalf("expected project root with go.mod in search dirs, got %#v", dirs)
	}
}

func isolateUserConfigDir(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir isolated home: %v", err)
	}
	// Env vars alone are unreliable on some Windows Go runtimes (UserHomeDir
	// can ignore mid-process USERPROFILE changes). Force the config base.
	prev := userConfigDirOverride
	userConfigDirOverride = home
	t.Cleanup(func() {
		userConfigDirOverride = prev
	})
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := userConfigDir(); !strings.HasPrefix(got, home) {
		t.Fatalf("userConfigDir isolation failed: got %q want under %q", got, home)
	}
}

func TestLoadSkillsIncludesBuiltinHumanizeDocWithoutSeed(t *testing.T) {
	isolateUserConfigDir(t)
	// Empty user skills dir, empty workspace: humanize-doc must still resolve
	// from the binary embed.
	catalog, warnings := LoadSkills(t.TempDir(), nil, nil)
	for _, warn := range warnings {
		if strings.Contains(strings.ToLower(warn), "humanize-doc") {
			t.Fatalf("unexpected humanize-doc load warning: %s", warn)
		}
	}
	skill, ok := catalog.Lookup("humanize-doc")
	if !ok {
		t.Fatalf("expected built-in humanize-doc in catalog, got %#v", catalog.Items())
	}
	if !skill.Builtin {
		t.Fatalf("expected humanize-doc.Builtin=true, got %#v", skill)
	}
	if !strings.Contains(skill.Content, "AI 문체 제거") && !strings.Contains(skill.Content, "Humanize") {
		t.Fatalf("expected humanize-doc body from embed, got %q", skill.Content)
	}
	if !strings.Contains(skill.Content, "Bundled references (built-in copy)") {
		t.Fatalf("expected inlined ai-tells reference in built-in body")
	}
	if !strings.Contains(skill.Content, "Binary contrasts") {
		t.Fatalf("expected no-ai-slop pattern table in built-in body")
	}
	catalogText := catalog.CatalogPrompt()
	if !strings.Contains(catalogText, "humanize-doc (built-in)") {
		t.Fatalf("catalog should label humanize-doc as built-in, got:\n%s", catalogText)
	}
}

func TestLoadSkillsKeepsUserCustomizedHumanizeDoc(t *testing.T) {
	isolateUserConfigDir(t)
	// Place the customized skill under the workspace cwd so ancestor walks from
	// TempDir cannot pick a real home-directory humanize-doc first.
	root := t.TempDir()
	path := filepath.Join(root, ".kernforge", "skills", "humanize-doc", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	custom := []byte("---\nname: humanize-doc\ndescription: custom humanize\n---\n\n# custom humanize body only\n")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatalf("write custom: %v", err)
	}
	// Marker for a different hash so the file is treated as user-customized.
	if err := os.WriteFile(path+bundledSkillHashSuffix, []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	catalog, _ := LoadSkills(root, nil, nil)
	skill, ok := catalog.Lookup("humanize-doc")
	if !ok {
		t.Fatal("expected humanize-doc present")
	}
	if skill.Builtin {
		t.Fatalf("customized disk skill must not be replaced by built-in, got %#v", skill)
	}
	if !strings.Contains(skill.Content, "custom humanize body only") {
		t.Fatalf("expected customized body preserved, got %q", skill.Content)
	}
	if !strings.EqualFold(skill.Path, path) {
		t.Fatalf("expected customized workspace skill path %q, got %q", path, skill.Path)
	}
}

func TestInjectPromptContextAutoActivatesBuiltinHumanizeDoc(t *testing.T) {
	isolateUserConfigDir(t)
	catalog, _ := LoadSkills(t.TempDir(), nil, nil)

	skill, ok := catalog.Lookup("humanize-doc")
	if !ok || !skill.Builtin {
		t.Fatalf("precondition: humanize-doc must be built-in, got ok=%v %#v", ok, skill)
	}

	enriched := catalog.InjectPromptContext("이 README에서 AI 티 제거해줘")
	if !strings.Contains(enriched, "Activated skills for this request:") {
		t.Fatalf("expected auto-activated skill section, got %q", enriched)
	}
	if !strings.Contains(enriched, "### humanize-doc") {
		t.Fatalf("expected humanize-doc section, got %q", enriched)
	}
	if !strings.Contains(enriched, "Origin: shipped inside the kernforge binary") {
		t.Fatalf("expected built-in origin note, got %q", enriched)
	}

	// Ordinary code edit must not auto-activate humanize-doc.
	plain := catalog.InjectPromptContext("main.go 버그를 고쳐줘")
	if strings.Contains(plain, "Activated skills for this request:") {
		t.Fatalf("code-edit request must not auto-activate skills, got %q", plain)
	}
}

func TestInjectPromptContextForRequestIgnoresEnrichedContextFalsePositives(t *testing.T) {
	isolateUserConfigDir(t)
	catalog, _ := LoadSkills(t.TempDir(), nil, nil)
	// Enriched message mentions humanize-doc in internal guidance, but the
	// external request is a plain code fix — must not auto-activate.
	request := "main.go 버그를 고쳐줘"
	message := request + "\n\nRequest mode: document-authoring.\n- If the user asks to remove AI tone, use $humanize-doc after the draft exists.\n"
	out := catalog.InjectPromptContextForRequest(request, message)
	if strings.Contains(out, "Activated skills for this request:") {
		t.Fatalf("enriched envelope mention must not auto-activate humanize-doc, got %q", out)
	}
	// Real humanize request still activates even when the message is enriched.
	humanizeReq := "AI 티 제거해줘"
	humanizeMsg := humanizeReq + "\n\nRequest envelope:\n- Allows file mutation: true.\n"
	out = catalog.InjectPromptContextForRequest(humanizeReq, humanizeMsg)
	if !strings.Contains(out, "### humanize-doc") {
		t.Fatalf("real humanize request must still activate, got %q", out)
	}
}

func TestLoadSkillsDoesNotOverwriteProjectLocalHumanizeDoc(t *testing.T) {
	isolateUserConfigDir(t)
	root := t.TempDir()
	path := filepath.Join(root, ".kernforge", "skills", "humanize-doc", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Project skill with no hash marker must still win over the binary embed.
	projectBody := []byte("---\nname: humanize-doc\ndescription: project humanize\n---\n\n# project-local humanize procedure\n")
	if err := os.WriteFile(path, projectBody, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	catalog, _ := LoadSkills(root, nil, nil)
	skill, ok := catalog.Lookup("humanize-doc")
	if !ok {
		t.Fatal("expected humanize-doc")
	}
	if skill.Builtin {
		t.Fatalf("project-local skill without marker must not be replaced by built-in, got %#v", skill)
	}
	if !strings.Contains(skill.Content, "project-local humanize procedure") {
		t.Fatalf("expected project body, got %q", skill.Content)
	}
}

func TestSelectableCountIgnoresAlwaysAvailableBuiltinAlone(t *testing.T) {
	isolateUserConfigDir(t)
	// Empty workspace: only binary humanize-doc is present.
	catalog, _ := LoadSkills(t.TempDir(), nil, nil)
	if _, ok := catalog.Lookup("humanize-doc"); !ok {
		t.Fatal("expected built-in humanize-doc")
	}
	if catalog.SelectableCount() != 0 {
		t.Fatalf("always-available builtin alone must not force SelectableCount>0, got %d", catalog.SelectableCount())
	}
	// Without other selectable skills, ordinary code turns must not inject catalog.
	if shouldIncludeSkillCatalogInSystemPrompt("please refactor this parser", catalog) {
		t.Fatalf("humanize-doc alone must not force skill catalog into every turn")
	}
	// Explicit skill keyword still surfaces the catalog (and built-in entry).
	if !shouldIncludeSkillCatalogInSystemPrompt("show available skills", catalog) {
		t.Fatalf("skill keyword must still include catalog")
	}
}

func TestLooksLikeHumanizeDocRequest(t *testing.T) {
	positives := []string{
		"AI 티 제거해줘",
		"이 문서를 humanize 해줘",
		"$humanize-doc README.md",
		"humanize-doc 로 다듬어",
		"make this sound human",
		"remove ai slop from the report",
		"로봇 같은 말투 다듬어줘",
	}
	for _, q := range positives {
		if !looksLikeHumanizeDocRequest(q) {
			t.Fatalf("expected humanize intent for %q", q)
		}
	}
	negatives := []string{
		"main.go 버그를 고쳐줘",
		"AI 모델 API를 연동해줘",
		"문서를 작성해줘",
		"README 내용을 설명해줘",
		"AI detector false positive를 수정해",
		"edit the humanize helper function",
		"fix AI telemetry path",
		"path/to/humanize-doc-notes.md 읽어줘",
	}
	for _, q := range negatives {
		if looksLikeHumanizeDocRequest(q) {
			t.Fatalf("did not expect humanize intent for %q", q)
		}
	}
}
