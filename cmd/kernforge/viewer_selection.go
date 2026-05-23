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
	unlock := lockFilePath(path)
	defer unlock()
	selections, _, err := loadWorkspaceSelectionsFile(path)
	if err == nil {
		return absolutizeWorkspaceSelections(root, selections), nil
	}
	if os.IsNotExist(err) {
		return nil, nil
	}
	primaryErr := err
	backupPath := workspaceSelectionsBackupPath(path)
	backupSelections, backupData, backupErr := loadWorkspaceSelectionsFile(backupPath)
	if backupErr != nil {
		return nil, primaryErr
	}
	_ = atomicWriteFile(path, backupData, 0o644)
	return absolutizeWorkspaceSelections(root, backupSelections), nil
}

func SyncWorkspaceSelections(root string, sessionSelections []ViewerSelection) error {
	path := filepath.Join(root, ".kernforge", "selections.json")
	unlock := lockFilePath(path)
	defer unlock()
	var existing []ViewerSelection
	if loaded, _, err := loadWorkspaceSelectionsFile(path); err == nil {
		existing = loaded
	} else if !os.IsNotExist(err) {
		if loaded, data, backupErr := loadWorkspaceSelectionsFile(workspaceSelectionsBackupPath(path)); backupErr == nil {
			existing = loaded
			_ = atomicWriteFile(path, data, 0o644)
		}
	}

	merged := make(map[string]ViewerSelection)
	for _, sel := range existing {
		key, sel, ok := prepareWorkspaceSelection(root, sel)
		if !ok {
			continue
		}
		merged[key] = sel
	}

	for _, sel := range sessionSelections {
		key, sel, ok := prepareWorkspaceSelection(root, sel)
		if !ok {
			continue
		}
		merged[key] = sel
	}

	var final []ViewerSelection
	for _, sel := range merged {
		final = append(final, sel)
	}

	if len(final) == 0 {
		_ = os.Remove(path)
		_ = os.Remove(workspaceSelectionsBackupPath(path))
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
	if existingData, err := os.ReadFile(path); err == nil && json.Valid(existingData) {
		_ = atomicWriteFile(workspaceSelectionsBackupPath(path), existingData, 0o644)
	}
	if err := atomicWriteFile(path, out, 0o644); err != nil {
		return err
	}
	_ = atomicWriteFile(workspaceSelectionsBackupPath(path), out, 0o644)
	return nil
}

func prepareWorkspaceSelection(root string, sel ViewerSelection) (string, ViewerSelection, bool) {
	if !sel.HasSelection() {
		return "", ViewerSelection{}, false
	}
	sel.Note = strings.TrimSpace(sel.Note)
	sel.Tags = normalizeSelectionTags(sel.Tags)
	if sel.Note == "" && len(sel.Tags) == 0 {
		return "", ViewerSelection{}, false
	}
	rel := relOrAbs(root, sel.FilePath)
	sel.FilePath = rel
	return fmt.Sprintf("%s:%d-%d", rel, sel.StartLine, sel.EndLine), sel, true
}

func normalizeSelectionTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		if value := strings.TrimSpace(tag); value != "" {
			normalized = append(normalized, value)
		}
	}
	return uniqueStrings(normalized)
}

func workspaceSelectionsBackupPath(path string) string {
	return path + ".bak"
}

func loadWorkspaceSelectionsFile(path string) ([]ViewerSelection, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var selections []ViewerSelection
	if err := json.Unmarshal(data, &selections); err != nil {
		return nil, nil, err
	}
	return selections, data, nil
}

func absolutizeWorkspaceSelections(root string, selections []ViewerSelection) []ViewerSelection {
	out := append([]ViewerSelection(nil), selections...)
	for i, sel := range out {
		if !filepath.IsAbs(sel.FilePath) {
			out[i].FilePath = filepath.Join(root, sel.FilePath)
		}
	}
	return out
}
