package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ViewerSelection struct {
	FilePath  string   `json:"file_path"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
	Note      string   `json:"note,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

func (v ViewerSelection) HasSelection() bool {
	return strings.TrimSpace(v.FilePath) != "" && v.StartLine > 0 && v.EndLine >= v.StartLine
}

func createViewerResultPath() (string, error) {
	dir := filepath.Join(os.TempDir(), "kernforge-viewer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, "selection-*.json")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func readViewerSelection(path string) (ViewerSelection, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ViewerSelection{}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return ViewerSelection{}, fmt.Errorf("viewer closed without a selection")
	}
	var selection ViewerSelection
	if err := json.Unmarshal(data, &selection); err != nil {
		return ViewerSelection{}, err
	}
	return selection, nil
}

func formatSelectionPrompt(path string, startLine, endLine int) string {
	if endLine <= startLine {
		return fmt.Sprintf("@%s:%d ", path, startLine)
	}
	return fmt.Sprintf("@%s:%d-%d ", path, startLine, endLine)
}

func (v ViewerSelection) RelativePrompt(root string) string {
	if !v.HasSelection() {
		return ""
	}
	path := v.FilePath
	if filepath.IsAbs(path) {
		path = relOrAbs(root, path)
	}
	return strings.TrimSpace(formatSelectionPrompt(path, v.StartLine, v.EndLine))
}

func (v ViewerSelection) Summary(root string) string {
	if !v.HasSelection() {
		return "(no selection)"
	}
	base := v.RelativePrompt(root)
	if len(v.Tags) > 0 {
		base += " tags=" + strings.Join(v.Tags, ",")
	}
	if strings.TrimSpace(v.Note) != "" {
		base += " note=" + compactPersistentMemoryText(v.Note, 60)
	}
	return base
}

func (v *ViewerSelection) SetTags(raw string) {
	var tags []string
	for _, item := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(item); value != "" {
			tags = append(tags, value)
		}
	}
	v.Tags = uniqueStrings(tags)
}

func loadSelectionPreview(root string, selection ViewerSelection) (string, error) {
	if !selection.HasSelection() {
		return "", fmt.Errorf("no selection")
	}
	path := selection.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := sliceLines(string(data), selection.StartLine, selection.EndLine)
	if len(content) > 3000 {
		content = content[:3000] + "\n... (truncated)"
	}
	return content, nil
}

func buildSelectionContextPrompt(root string, selections []ViewerSelection) string {
	var refs []string
	for _, selection := range selections {
		if !selection.HasSelection() {
			continue
		}
		ref := selection.RelativePrompt(root)
		if strings.TrimSpace(selection.Note) != "" {
			ref += " note=\"" + strings.TrimSpace(selection.Note) + "\""
		}
		if len(selection.Tags) > 0 {
			ref += " tags=" + strings.Join(selection.Tags, ",")
		}
		refs = append(refs, ref)
	}
	return strings.Join(refs, " ")
}

func LoadWorkspaceSelections(root string) ([]ViewerSelection, error) {
	path := filepath.Join(root, ".kernforge", "selections.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var selections []ViewerSelection
	if err := json.Unmarshal(data, &selections); err != nil {
		return nil, err
	}
	for i, sel := range selections {
		if !filepath.IsAbs(sel.FilePath) {
			selections[i].FilePath = filepath.Join(root, sel.FilePath)
		}
	}
	return selections, nil
}

func SyncWorkspaceSelections(root string, sessionSelections []ViewerSelection) error {
	path := filepath.Join(root, ".kernforge", "selections.json")
	var existing []ViewerSelection
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &existing)
	}

	merged := make(map[string]ViewerSelection)
	for _, sel := range existing {
		rel := sel.FilePath
		if filepath.IsAbs(rel) {
			rel = relOrAbs(root, rel)
		}
		key := fmt.Sprintf("%s:%d-%d", rel, sel.StartLine, sel.EndLine)
		merged[key] = sel
	}

	for _, sel := range sessionSelections {
		rel := relOrAbs(root, sel.FilePath)
		key := fmt.Sprintf("%s:%d-%d", rel, sel.StartLine, sel.EndLine)
		if strings.TrimSpace(sel.Note) != "" || len(sel.Tags) > 0 {
			sel.FilePath = rel
			merged[key] = sel
		} else {
			delete(merged, key)
		}
	}

	var final []ViewerSelection
	for _, sel := range merged {
		final = append(final, sel)
	}

	if len(final) == 0 {
		os.Remove(path)
		return nil
	}

	sort.Slice(final, func(i, j int) bool {
		if final[i].FilePath == final[j].FilePath {
			return final[i].StartLine < final[j].StartLine
		}
		return final[i].FilePath < final[j].FilePath
	})

	if err := os.MkdirAll(filepath.Join(root, ".kernforge"), 0755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(final, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0644)
}
