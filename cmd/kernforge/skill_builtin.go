package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Built-in skills live in the binary (go:embed) and are always available through
// LoadSkills even when the user skills directory is empty or seed failed.
// Disk copies under userConfigDir()/skills remain the override channel: when a
// user customizes a skill file (hash marker mismatch), LoadSkills keeps the
// disk version and does not force the embed body.

const (
	builtinHumanizeDocSkillName     = "humanize-doc"
	builtinHumanizeDocEmbedSKILL    = ".kernforge/skills/humanize-doc/SKILL.md"
	builtinHumanizeDocEmbedTells    = ".kernforge/skills/humanize-doc/references/ai-tells.md"
	builtinHumanizeDocSyntheticPath = "builtin:humanize-doc/SKILL.md"
)

// builtinSkillSpecs lists skills that are always merged from the binary.
// Only humanize-doc is treated as a first-class built-in today; other workflow
// skills stay seed-discovered.
var builtinSkillSpecs = []struct {
	Name      string
	EmbedPath string
	// Optional embed paths appended under "## Bundled references" so the skill
	// remains self-contained when supporting files are not on disk yet.
	ReferenceEmbedPaths []struct {
		Title string
		Path  string
	}
}{
	{
		Name:      builtinHumanizeDocSkillName,
		EmbedPath: builtinHumanizeDocEmbedSKILL,
		ReferenceEmbedPaths: []struct {
			Title string
			Path  string
		}{
			{Title: "ai-tells.md", Path: builtinHumanizeDocEmbedTells},
		},
	},
}

func loadEmbeddedBuiltinSkills() ([]Skill, []string) {
	skills := make([]Skill, 0, len(builtinSkillSpecs))
	warnings := make([]string, 0)
	for _, spec := range builtinSkillSpecs {
		skill, warn, err := loadEmbeddedBuiltinSkill(spec.Name, spec.EmbedPath, spec.ReferenceEmbedPaths)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("builtin skill %s: %v", spec.Name, err))
			continue
		}
		warnings = append(warnings, warn...)
		skills = append(skills, skill)
	}
	return skills, warnings
}

func loadEmbeddedBuiltinSkill(name, embedPath string, refs []struct {
	Title string
	Path  string
}) (Skill, []string, error) {
	data, err := bundledBuiltinSkills.ReadFile(embedPath)
	if err != nil {
		return Skill{}, nil, err
	}
	// Prefer the seeded on-disk path so relative read_file instructions resolve
	// after ensureBundledUserAssets. Fall back to a synthetic path when the
	// seed is absent; reference bodies are inlined below for that case.
	diskPath := filepath.Join(userConfigDir(), "skills", name, "SKILL.md")
	path := "builtin:" + name + "/SKILL.md"
	if name == builtinHumanizeDocSkillName {
		path = builtinHumanizeDocSyntheticPath
	}
	if info, statErr := os.Stat(diskPath); statErr == nil && !info.IsDir() {
		path = diskPath
	}

	skill, warnings, err := loadSkillFromBytes(path, data)
	if err != nil {
		return Skill{}, nil, err
	}
	if strings.TrimSpace(skill.Name) == "" {
		skill.Name = name
	}
	skill.Builtin = true
	skill.UserInvocable = true

	// Inline bundled references so the procedure is self-contained even when
	// the user deleted the seeded references/ tree. When the disk tree exists
	// the model can still read those files; the inline copy is authoritative.
	if len(refs) > 0 {
		var b strings.Builder
		b.WriteString(skill.Content)
		b.WriteString("\n\n## Bundled references (built-in copy)\n")
		b.WriteString("The following reference material is shipped inside the kernforge binary. Prefer this copy when the on-disk skill directory is missing files.\n")
		for _, ref := range refs {
			refData, refErr := bundledBuiltinSkills.ReadFile(ref.Path)
			if refErr != nil {
				warnings = append(warnings, fmt.Sprintf("builtin skill %s reference %s: %v", name, ref.Path, refErr))
				continue
			}
			b.WriteString("\n### ")
			b.WriteString(strings.TrimSpace(ref.Title))
			b.WriteString("\n\n")
			b.WriteString(strings.TrimSpace(string(refData)))
			b.WriteString("\n")
		}
		skill.Content = strings.TrimSpace(b.String())
	}
	return skill, warnings, nil
}

// mergeEmbeddedBuiltinSkills inserts binary-resident skills into the catalog.
// User-customized on-disk skills win; otherwise the embed body replaces the
// seeded copy so upgrades always reach the model without a manual reseed.
func mergeEmbeddedBuiltinSkills(order []string, itemsByName map[string]Skill) ([]string, map[string]Skill, []string) {
	if itemsByName == nil {
		itemsByName = map[string]Skill{}
	}
	builtins, warnings := loadEmbeddedBuiltinSkills()
	for _, builtin := range builtins {
		key := normalizeSkillName(builtin.Name)
		if key == "" {
			continue
		}
		if existing, ok := itemsByName[key]; ok {
			if skillPathLooksUserCustomized(existing.Path) {
				// Keep project-local or user-edited skill; do not mark it built-in.
				continue
			}
			// Uncustomized user-global seed: keep disk path for supporting files,
			// replace body with the binary embed so upgrades reach the model.
			builtin.Path = existing.Path
			itemsByName[key] = builtin
			continue
		}
		order = append(order, key)
		itemsByName[key] = builtin
	}
	return order, itemsByName, warnings
}

// skillPathLooksUserCustomized reports whether the on-disk skill should win over
// the binary embed.
//
// Rules:
//   - missing / synthetic paths: not customized (embed may fill in)
//   - project/workspace skill (not under user-global seed dir): always keep disk
//   - user-global seed without marker: treat as uncustomized so embed upgrades
//   - user-global seed with marker: customized when content hash != marker
func skillPathLooksUserCustomized(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "builtin:") {
		return false
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	// Project-local skills (repo .kernforge/skills or skills/) must never be
	// silently replaced by the binary embed, even for known built-in names and
	// even when no hash marker exists.
	if !skillPathIsUserGlobalSeed(path) {
		return true
	}
	marker, err := os.ReadFile(bundledSkillHashPath(path))
	if err != nil {
		// User-global seed without marker: upgrade known built-ins from embed.
		if skillPathIsKnownBuiltinSeed(path) {
			return false
		}
		return true
	}
	recorded := strings.TrimSpace(string(marker))
	if recorded == "" {
		return true
	}
	return recorded != hashBundledSkillContent(existing)
}

// skillPathIsUserGlobalSeed reports whether path lives under
// userConfigDir()/skills, the directory ensureBundledBuiltinSkills seeds.
func skillPathIsUserGlobalSeed(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	seedRoot := filepath.Join(userConfigDir(), "skills")
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = filepath.Clean(path)
	}
	absSeed, err := filepath.Abs(seedRoot)
	if err != nil {
		absSeed = filepath.Clean(seedRoot)
	}
	absPath = filepath.Clean(absPath)
	absSeed = filepath.Clean(absSeed)
	rel, err := filepath.Rel(absSeed, absPath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func skillPathIsKnownBuiltinSeed(path string) bool {
	if !skillPathIsUserGlobalSeed(path) {
		return false
	}
	normalized := strings.ToLower(filepath.ToSlash(path))
	for _, spec := range builtinSkillSpecs {
		// Match .../skills/<name>/SKILL.md under the user seed root.
		if strings.Contains(normalized, "/skills/"+strings.ToLower(spec.Name)+"/skill.md") {
			return true
		}
	}
	return false
}

func embeddedBuiltinSkillBodyForPath(path string) ([]byte, bool) {
	if !skillPathIsKnownBuiltinSeed(path) {
		return nil, false
	}
	normalized := strings.ToLower(filepath.ToSlash(path))
	for _, spec := range builtinSkillSpecs {
		if strings.Contains(normalized, "/skills/"+strings.ToLower(spec.Name)+"/skill.md") {
			data, err := bundledBuiltinSkills.ReadFile(spec.EmbedPath)
			if err != nil {
				return nil, false
			}
			return data, true
		}
	}
	return nil, false
}

// isAlwaysAvailableBuiltinName reports built-ins that are merged from the binary
// and auto-activate by intent. They must not alone force the full skill catalog
// into every system prompt (token cost on non-skill turns).
func isAlwaysAvailableBuiltinName(name string) bool {
	switch normalizeSkillName(name) {
	case builtinHumanizeDocSkillName:
		return true
	default:
		return false
	}
}

// looksLikeHumanizeDocRequest detects explicit polish / anti-slop requests so
// the built-in humanize-doc procedure is activated without requiring $name.
// Intentionally narrow: coding-agent requests often say "edit"/"fix"/"수정" for
// source code, and must not pull the full humanize procedure.
func looksLikeHumanizeDocRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	// Explicit skill name / slash-style invoke. Do not use plain containsWord for
	// "humanize-doc": path segments like humanize-doc-notes.md would false-hit.
	if strings.Contains(lower, "$humanize-doc") || skillNameTokenPresent(lower, "humanize-doc") {
		return true
	}
	// Strong phrases already imply the polish action.
	if containsAny(lower,
		"ai 티 제거",
		"ai티 제거",
		"ai 냄새 제거",
		"ai 문체 제거",
		"ai 말투 제거",
		"ai스러운 문장 고쳐",
		"ai스러운 문장 다듬",
		"사람이 쓴 것처럼",
		"사람처럼 다듬",
		"사람처럼 고쳐",
		"make this sound human",
		"make it sound human",
		"remove ai writing style",
		"remove ai writing",
		"remove ai style",
		"remove ai slop",
		"sound less like ai",
	) {
		return true
	}
	// "humanize" as a bare identifier ("edit the humanize helper") is not enough.
	// Require a request-like particle or document object.
	if containsWord(lower, "humanize") || containsAny(lower, "휴머나이즈", "휴먼나이즈") {
		if containsAny(lower,
			"해줘", "해 줘", "해주세요", "해 주세요", "부탁", "please",
			"this", "document", "문서", "readme", "글", "다듬", "제거",
			"polish", "rewrite", "doc ", " the doc",
		) {
			return true
		}
	}

	// Other topics + polish verbs. Avoid generic coding verbs (edit/fix/수정/make).
	hasTopic := containsAny(lower,
		"ai slop",
		"ai 티",
		"ai티",
		"ai 냄새",
		"ai냄새",
		"ai스러운",
		"에이아이 티",
		"기계 같",
		"로봇 같",
		"상투 말투",
		"ai 문체",
		"ai 말투",
	)
	if !hasTopic {
		return false
	}
	return containsAny(lower,
		"고치", "고쳐", "제거", "없애", "다듬", "바꿔", "바꾸",
		"polish", "rewrite", "remove",
		"해줘", "해 줘", "해주세요", "해 주세요", "부탁",
	)
}

// skillNameTokenPresent reports a bare skill name as its own token, not as a
// prefix of a longer path segment (humanize-doc-notes.md) or identifier.
func skillNameTokenPresent(text, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	start := 0
	for {
		idx := strings.Index(text[start:], name)
		if idx < 0 {
			return false
		}
		pos := start + idx
		beforeOK := pos == 0 || skillNameTokenBoundary(text[pos-1])
		afterPos := pos + len(name)
		afterOK := afterPos >= len(text) || skillNameTokenBoundary(text[afterPos])
		if beforeOK && afterOK {
			return true
		}
		start = pos + 1
		if start >= len(text) {
			return false
		}
	}
}

func skillNameTokenBoundary(b byte) bool {
	// Continue path/identifier segments through '-', '_', alnum, and path seps.
	if b >= 'a' && b <= 'z' {
		return false
	}
	if b >= 'A' && b <= 'Z' {
		return false
	}
	if b >= '0' && b <= '9' {
		return false
	}
	switch b {
	case '_', '-', '/', '\\', '.':
		return false
	default:
		return true
	}
}

// shouldAutoActivateBuiltinSkill reports whether a skill should be injected for
// this user text without an explicit $name token. humanize-doc auto-activates
// by name even when a disk copy is present (as long as it is not disabled).
func shouldAutoActivateBuiltinSkill(skill Skill, userText string) bool {
	if skill.DisableModelInvocation || !skill.UserInvocable {
		return false
	}
	if skill.Enabled {
		// Already injected in full via DefaultPrompt.
		return false
	}
	switch normalizeSkillName(skill.Name) {
	case builtinHumanizeDocSkillName:
		return looksLikeHumanizeDocRequest(userText)
	default:
		return false
	}
}
