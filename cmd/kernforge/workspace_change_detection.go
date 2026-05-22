package main

import (
	"strings"
)

func toolMetaBoolValue(meta map[string]any, key string) (bool, bool) {
	if len(meta) == 0 {
		return false, false
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.EqualFold(trimmed, "true") {
			return true, true
		}
		if strings.EqualFold(trimmed, "false") {
			return false, true
		}
	}
	return false, false
}

func toolResultAttemptedWorkspaceEdit(toolName string, meta map[string]any) bool {
	if isEditTool(toolName) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(toolMetaString(meta, "effect")), "edit") {
		return true
	}
	return strings.EqualFold(toolMetaString(meta, "mutation_class"), string(shellMutationWorkspaceWrite))
}

func toolResultRepresentsWorkspaceEdit(toolName string, meta map[string]any) bool {
	if toolMetaBool(meta, "turn_diff_invalidated") {
		return true
	}
	if strings.TrimSpace(toolMetaString(meta, "unified_diff")) != "" {
		return true
	}
	if changed, ok := toolMetaBoolValue(meta, "changed_workspace"); ok {
		return changed
	}
	if isEditTool(toolName) && len(meta) == 0 {
		return true
	}
	if !toolResultAttemptedWorkspaceEdit(toolName, meta) {
		return false
	}
	if toolMetaInt(meta, "changed_count") > 0 {
		return true
	}
	paths := toolMetaStringSlice(meta, "changed_paths")
	if len(paths) == 0 {
		return false
	}
	return true
}

func changedWorkspacePathMeta(path string, changed bool) ([]string, int) {
	if !changed {
		return nil, 0
	}
	paths := normalizeTaskStateList([]string{path}, 8)
	return paths, len(paths)
}
