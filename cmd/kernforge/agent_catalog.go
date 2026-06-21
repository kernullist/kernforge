package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// agentCatalogDirName is the project-relative directory that holds user-defined
// agent definitions discovered from disk, merged with in-config specialist
// profiles. Each *.md file is one agent.
const agentCatalogDirName = "agents"

// loadAgentCatalogProfiles discovers user-definable agent definitions from
// <projectRoot>/.kernforge/agents/*.md and returns them as specialist profiles.
// Discovery is best-effort: a missing directory, unreadable file, or malformed
// frontmatter is skipped rather than failing config load. Results are sorted by
// file name so the merge order is deterministic.
func loadAgentCatalogProfiles(projectRoot string) []SpecialistSubagentProfile {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		return nil
	}
	dir := filepath.Join(root, userConfigDirName, agentCatalogDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), ".md") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]SpecialistSubagentProfile, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		profile, ok := parseAgentCatalogFile(string(data), name)
		if !ok {
			continue
		}
		out = append(out, profile)
	}
	return out
}

// parseAgentCatalogFile parses one agent markdown file. The supported shape is a
// leading frontmatter block delimited by lines of three dashes:
//
//	---
//	name: my-agent
//	description: one line summary
//	model: gpt-5
//	provider: openai
//	tools: read_file, grep, apply_patch
//	---
//	Free-form prompt body...
//
// The prompt is taken from a "prompt:" frontmatter key when present, otherwise
// from the body after the closing delimiter. When the frontmatter is absent the
// file name (without extension) becomes the agent name and the whole file is the
// prompt. A profile without a usable name is rejected.
func parseAgentCatalogFile(content string, fileName string) (SpecialistSubagentProfile, bool) {
	frontmatter, body := splitAgentCatalogFrontmatter(content)
	profile := SpecialistSubagentProfile{}
	for key, value := range frontmatter {
		switch key {
		case "name":
			profile.Name = value
		case "description":
			profile.Description = value
		case "prompt":
			profile.Prompt = value
		case "model":
			profile.Model = value
		case "provider":
			profile.Provider = value
		case "tools":
			profile.Tools = splitAgentCatalogList(value)
		case "keywords":
			profile.Keywords = splitAgentCatalogList(value)
		case "node_kinds":
			profile.NodeKinds = splitAgentCatalogList(value)
		case "ownership_paths":
			profile.OwnershipPaths = splitAgentCatalogList(value)
		case "read_only":
			if parsed, ok := parseBoolString(value); ok {
				readOnly := parsed
				profile.ReadOnly = &readOnly
			}
		case "editable":
			if parsed, ok := parseBoolString(value); ok {
				editable := parsed
				profile.Editable = &editable
			}
		}
	}
	if strings.TrimSpace(profile.Prompt) == "" {
		profile.Prompt = strings.TrimSpace(body)
	}
	if strings.TrimSpace(profile.Name) == "" {
		base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		profile.Name = strings.TrimSpace(base)
	}
	profile = normalizeSpecialistProfile(profile)
	if strings.TrimSpace(profile.Name) == "" {
		return SpecialistSubagentProfile{}, false
	}
	return profile, true
}

// splitAgentCatalogFrontmatter separates a leading "--- ... ---" frontmatter
// block from the body. When no frontmatter delimiter is present the whole input
// is returned as the body with an empty frontmatter map.
func splitAgentCatalogFrontmatter(content string) (map[string]string, string) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	trimmed := strings.TrimLeft(normalized, "\n")
	if !strings.HasPrefix(trimmed, "---\n") && trimmed != "---" {
		return map[string]string{}, normalized
	}
	rest := strings.TrimPrefix(trimmed, "---\n")
	closeIdx := strings.Index(rest, "\n---")
	if closeIdx < 0 {
		// No closing delimiter; treat the whole thing as body.
		return map[string]string{}, normalized
	}
	block := rest[:closeIdx]
	body := rest[closeIdx+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")
	return parseAgentCatalogFrontmatterBlock(block), body
}

// parseAgentCatalogFrontmatterBlock parses simple "key: value" lines. Keys are
// lower-cased and trimmed. Blank lines and comment lines (starting with #) are
// ignored. A surrounding pair of matching quotes on the value is stripped.
func parseAgentCatalogFrontmatterBlock(block string) map[string]string {
	out := map[string]string{}
	for _, rawLine := range strings.Split(block, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		value := strings.TrimSpace(line[idx+1:])
		value = trimAgentCatalogQuotes(value)
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

// splitAgentCatalogList accepts either a comma-separated value or a YAML inline
// list "[a, b]" and returns the trimmed, non-empty entries.
func splitAgentCatalogList(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = trimAgentCatalogQuotes(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func trimAgentCatalogQuotes(value string) string {
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// mergeAgentCatalogProfiles overlays disk-discovered agent profiles onto the
// in-config specialist profiles. Disk entries that share a normalized name with
// a config entry override the config entry's set fields (config remains the base
// so built-in routing stays intact); new names are appended. Config order is
// preserved; new disk-only agents keep their sorted discovery order.
func mergeAgentCatalogProfiles(configProfiles []SpecialistSubagentProfile, diskProfiles []SpecialistSubagentProfile) []SpecialistSubagentProfile {
	if len(diskProfiles) == 0 {
		return configProfiles
	}
	order := make([]string, 0, len(configProfiles)+len(diskProfiles))
	merged := make(map[string]SpecialistSubagentProfile, len(configProfiles)+len(diskProfiles))
	for _, profile := range configProfiles {
		key := normalizeSpecialistProfileName(profile.Name)
		if key == "" {
			continue
		}
		if _, ok := merged[key]; !ok {
			order = append(order, key)
		}
		merged[key] = normalizeSpecialistProfile(profile)
	}
	for _, disk := range diskProfiles {
		key := normalizeSpecialistProfileName(disk.Name)
		if key == "" {
			continue
		}
		if existing, ok := merged[key]; ok {
			merged[key] = mergeSpecialistProfile(existing, disk)
			continue
		}
		order = append(order, key)
		merged[key] = normalizeSpecialistProfile(disk)
	}
	out := make([]SpecialistSubagentProfile, 0, len(order))
	for _, key := range order {
		if profile, ok := merged[key]; ok {
			out = append(out, profile)
		}
	}
	return out
}
