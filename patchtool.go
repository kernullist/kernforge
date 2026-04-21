package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type ApplyPatchTool struct{ ws Workspace }

func NewApplyPatchTool(ws Workspace) ApplyPatchTool { return ApplyPatchTool{ws: ws} }

func (t ApplyPatchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "apply_patch",
		Description: "Apply a precise patch to one or more existing files using a Begin/End Patch format. This is the default edit tool for most code changes after reading the current file contents.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patch":         map[string]any{"type": "string"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"patch"},
		},
	}
}

func (t ApplyPatchTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t ApplyPatchTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args := input.(map[string]any)
	patchText := stringValue(args, "patch")
	if strings.TrimSpace(patchText) == "" {
		return ToolExecutionResult{}, fmt.Errorf("patch is required")
	}
	doc, err := parsePatchDocument(patchText)
	if err != nil {
		return ToolExecutionResult{}, fmt.Errorf("%w: %v", ErrInvalidPatchFormat, err)
	}
	text, err := applyPatchDocument(ctx, t.ws, doc, strings.TrimSpace(stringValue(args, "owner_node_id")))
	return ToolExecutionResult{
		DisplayText: text,
		Meta:        buildApplyPatchMeta(t.ws.Root, doc),
	}, err
}

func buildApplyPatchMeta(root string, doc patchDocument) map[string]any {
	changedPaths := make([]string, 0, len(doc.ops))
	added := 0
	updated := 0
	deleted := 0
	moved := 0
	for _, op := range doc.ops {
		path := relOrAbs(root, op.path)
		if strings.TrimSpace(path) != "" {
			changedPaths = append(changedPaths, path)
		}
		switch strings.TrimSpace(strings.ToLower(op.kind)) {
		case "add":
			added++
		case "delete":
			deleted++
		case "update":
			updated++
			if strings.TrimSpace(op.moveTo) != "" {
				moved++
			}
		}
	}
	return map[string]any{
		"changed_paths":         normalizeTaskStateList(changedPaths, 32),
		"changed_count":         len(changedPaths),
		"patch_operation_count": len(doc.ops),
		"add_count":             added,
		"update_count":          updated,
		"delete_count":          deleted,
		"move_count":            moved,
		"changed_workspace":     len(changedPaths) > 0,
		"requires_verification": len(changedPaths) > 0,
		"effect":                "edit",
	}
}

type patchDocument struct {
	ops []patchOperation
}

type plannedPatchChange struct {
	kind        string
	srcPath     string
	destPath    string
	displayRoot string
	before      string
	after       string
	summary     string
}

type patchOperation struct {
	kind     string
	path     string
	moveTo   string
	addLines []string
	hunks    []patchHunk
}

type patchHunk struct {
	header string
	lines  []patchLine
}

type patchLine struct {
	kind byte
	text string
}

func parsePatchDocument(text string) (patchDocument, error) {
	text = strings.TrimPrefix(text, "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	index := 0

	next := func() string {
		if index >= len(lines) {
			return ""
		}
		return lines[index]
	}
	advance := func() string {
		line := next()
		index++
		return line
	}

	for strings.TrimSpace(next()) == "" {
		advance()
	}
	if advance() != "*** Begin Patch" {
		return patchDocument{}, fmt.Errorf("patch must start with *** Begin Patch")
	}

	var ops []patchOperation
	for index < len(lines) {
		line := next()
		if strings.TrimSpace(line) == "" {
			advance()
			continue
		}
		if line == "*** End Patch" {
			advance()
			return patchDocument{ops: ops}, nil
		}
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			op, err := parseAddFileOp(lines, &index)
			if err != nil {
				return patchDocument{}, err
			}
			ops = append(ops, op)
		case strings.HasPrefix(line, "*** Delete File: "):
			op := patchOperation{
				kind: "delete",
				path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: ")),
			}
			index++
			ops = append(ops, op)
		case strings.HasPrefix(line, "*** Update File: "):
			op, err := parseUpdateFileOp(lines, &index)
			if err != nil {
				return patchDocument{}, err
			}
			ops = append(ops, op)
		default:
			return patchDocument{}, fmt.Errorf("unexpected patch line: %s", line)
		}
	}
	return patchDocument{}, fmt.Errorf("patch is missing *** End Patch")
}

func parseAddFileOp(lines []string, index *int) (patchOperation, error) {
	line := lines[*index]
	path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
	*index++
	var addLines []string
	for *index < len(lines) {
		current := lines[*index]
		if strings.HasPrefix(current, "*** ") {
			break
		}
		if !strings.HasPrefix(current, "+") {
			return patchOperation{}, fmt.Errorf("add file patch lines must start with +: %s", current)
		}
		addLines = append(addLines, strings.TrimPrefix(current, "+"))
		*index++
	}
	return patchOperation{kind: "add", path: path, addLines: addLines}, nil
}

func parseUpdateFileOp(lines []string, index *int) (patchOperation, error) {
	line := lines[*index]
	op := patchOperation{
		kind: "update",
		path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: ")),
	}
	*index++
	if *index < len(lines) && strings.HasPrefix(lines[*index], "*** Move to: ") {
		op.moveTo = strings.TrimSpace(strings.TrimPrefix(lines[*index], "*** Move to: "))
		*index++
	}
	for *index < len(lines) {
		current := lines[*index]
		if strings.TrimSpace(current) == "" {
			*index++
			continue
		}
		if strings.HasPrefix(current, "*** ") {
			break
		}
		if !strings.HasPrefix(current, "@@") {
			return patchOperation{}, fmt.Errorf("update patch hunk must start with @@: %s", current)
		}
		hunk := patchHunk{header: current}
		*index++
		for *index < len(lines) {
			current = lines[*index]
			if strings.HasPrefix(current, "*** ") || strings.HasPrefix(current, "@@") {
				break
			}
			if current == "*** End of File" {
				*index++
				continue
			}
			if current == "" {
				return patchOperation{}, fmt.Errorf("patch lines inside hunks must start with space, +, or -")
			}
			prefix := current[0]
			if prefix != ' ' && prefix != '+' && prefix != '-' {
				return patchOperation{}, fmt.Errorf("invalid patch line in hunk: %s", current)
			}
			hunk.lines = append(hunk.lines, patchLine{kind: prefix, text: current[1:]})
			*index++
		}
		op.hunks = append(op.hunks, hunk)
	}
	if len(op.hunks) == 0 && op.moveTo == "" {
		return patchOperation{}, fmt.Errorf("update patch for %s has no hunks", op.path)
	}
	return op, nil
}

func applyPatchDocument(ctx context.Context, ws Workspace, doc patchDocument, ownerNodeID string) (string, error) {
	planned, err := planPatchDocument(ws, doc, ownerNodeID)
	if err != nil {
		return "", err
	}
	if len(planned) == 0 {
		return "No patch operations to apply.", nil
	}
	var previewBlocks []string
	for _, change := range planned {
		target := relOrAbs(change.displayRoot, change.destPath)
		if change.kind == "delete" {
			target = relOrAbs(change.displayRoot, change.srcPath)
		}
		previewBlocks = append(previewBlocks, buildSelectionAwareEditPreview(ws, target, change.before, change.after))
	}
	if err := ws.ConfirmEdit(EditPreview{
		Title:   fmt.Sprintf("Apply patch to %d file(s)", len(planned)),
		Preview: strings.Join(previewBlocks, "\n\n"),
	}); err != nil {
		return "", err
	}
	if err := ensurePlannedPatchWrites(ws, planned); err != nil {
		return "", err
	}
	editRoot := ws.Root
	if len(planned) > 0 {
		editRoot = firstNonBlankString(planned[0].displayRoot, ws.Root)
	}
	if err := ws.BeforeEditForRoot(fmt.Sprintf("apply patch (%d file(s))", len(planned)), editRoot); err != nil {
		return "", err
	}

	var summaries []string
	var previews []string
	for _, change := range planned {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		switch change.kind {
		case "add":
			ws.Progress("Writing " + relOrAbs(change.displayRoot, change.destPath) + "...")
			if err := ensureParentDir(change.destPath); err != nil {
				return "", err
			}
			if err := os.WriteFile(change.destPath, []byte(change.after), 0o644); err != nil {
				return "", err
			}
			ws.Progress("Saved " + relOrAbs(change.displayRoot, change.destPath) + ".")
			summaries = append(summaries, change.summary)
			previews = append(previews, buildEditPreview(relOrAbs(change.displayRoot, change.destPath), "", change.after))
		case "delete":
			ws.Progress("Deleting " + relOrAbs(change.displayRoot, change.srcPath) + "...")
			if err := os.Remove(change.srcPath); err != nil {
				return "", err
			}
			ws.Progress("Deleted " + relOrAbs(change.displayRoot, change.srcPath) + ".")
			summaries = append(summaries, change.summary)
			previews = append(previews, buildEditPreview(relOrAbs(change.displayRoot, change.srcPath), change.before, ""))
		case "update":
			ws.Progress("Writing " + relOrAbs(change.displayRoot, change.destPath) + "...")
			if err := ensureParentDir(change.destPath); err != nil {
				return "", err
			}
			if err := os.WriteFile(change.destPath, []byte(change.after), 0o644); err != nil {
				return "", err
			}
			if change.destPath != change.srcPath {
				if err := os.Remove(change.srcPath); err != nil {
					return "", err
				}
			}
			ws.Progress("Saved " + relOrAbs(change.displayRoot, change.destPath) + ".")
			summaries = append(summaries, change.summary)
			previews = append(previews, buildEditPreview(relOrAbs(change.displayRoot, change.destPath), change.before, change.after))
		default:
			return "", fmt.Errorf("unsupported patch operation: %s", change.kind)
		}
	}
	return joinNonEmpty(strings.Join(summaries, "\n"), strings.Join(previews, "\n\n")), nil
}

func ensurePlannedPatchWrites(ws Workspace, planned []plannedPatchChange) error {
	for _, change := range planned {
		switch change.kind {
		case "add":
			if err := ws.EnsureWrite(change.destPath); err != nil {
				return err
			}
		case "delete":
			if err := ws.EnsureWrite(change.srcPath); err != nil {
				return err
			}
		case "update":
			if err := ws.EnsureWrite(change.srcPath); err != nil {
				return err
			}
			if change.destPath != change.srcPath {
				if err := ws.EnsureWrite(change.destPath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func planPatchDocument(ws Workspace, doc patchDocument, ownerNodeID string) ([]plannedPatchChange, error) {
	var planned []plannedPatchChange
	for _, op := range doc.ops {
		switch op.kind {
		case "add":
			route, err := ws.ResolveEditPath(op.path, ownerNodeID, false)
			if err != nil {
				return nil, err
			}
			path := route.AbsolutePath
			if _, err := os.Stat(path); err == nil {
				return nil, fmt.Errorf("cannot add file that already exists: %s", op.path)
			}
			content := strings.Join(op.addLines, "\n")
			if len(op.addLines) > 0 {
				content += "\n"
			}
			planned = append(planned, plannedPatchChange{
				kind:        "add",
				destPath:    path,
				displayRoot: firstNonBlankString(route.DisplayRoot, ws.Root),
				after:       content,
				summary:     "added " + route.DisplayPath(),
			})
		case "delete":
			route, err := ws.ResolveEditPath(op.path, ownerNodeID, true)
			if err != nil {
				return nil, err
			}
			path := route.AbsolutePath
			original, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			planned = append(planned, plannedPatchChange{
				kind:        "delete",
				srcPath:     path,
				displayRoot: firstNonBlankString(route.DisplayRoot, ws.Root),
				before:      string(original),
				summary:     "deleted " + route.DisplayPath(),
			})
		case "update":
			srcRoute, err := ws.ResolveEditPath(op.path, ownerNodeID, true)
			if err != nil {
				return nil, err
			}
			srcPath := srcRoute.AbsolutePath
			destPath := srcPath
			displayRoot := firstNonBlankString(srcRoute.DisplayRoot, ws.Root)
			if strings.TrimSpace(op.moveTo) != "" {
				destRoute, err := ws.ResolveEditPath(op.moveTo, ownerNodeID, false)
				if err != nil {
					return nil, err
				}
				destPath = destRoute.AbsolutePath
				displayRoot = firstNonBlankString(destRoute.DisplayRoot, displayRoot, ws.Root)
			}
			original, err := os.ReadFile(srcPath)
			if err != nil {
				return nil, err
			}
			updated, err := applyPatchHunks(string(original), op.hunks)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", op.path, err)
			}
			summary := "updated " + relOrAbs(displayRoot, srcPath)
			if destPath != srcPath {
				summary = fmt.Sprintf("updated %s -> %s", relOrAbs(displayRoot, srcPath), relOrAbs(displayRoot, destPath))
			}
			planned = append(planned, plannedPatchChange{
				kind:        "update",
				srcPath:     srcPath,
				destPath:    destPath,
				displayRoot: displayRoot,
				before:      string(original),
				after:       updated,
				summary:     summary,
			})
		default:
			return nil, fmt.Errorf("unsupported patch operation: %s", op.kind)
		}
	}
	return planned, nil
}

func applyPatchHunks(content string, hunks []patchHunk) (string, error) {
	hasTrailingNewline := strings.HasSuffix(content, "\n")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	oldLines := []string{}
	if content != "" {
		oldLines = strings.Split(content, "\n")
	}

	cursor := 0
	for _, hunk := range hunks {
		var oldChunk []string
		var newChunk []string
		for _, line := range hunk.lines {
			if line.kind == ' ' || line.kind == '-' {
				oldChunk = append(oldChunk, line.text)
			}
			if line.kind == ' ' || line.kind == '+' {
				newChunk = append(newChunk, line.text)
			}
		}

		applyAt, err := locatePatchChunk(oldLines, oldChunk, cursor)
		if err != nil {
			return "", fmt.Errorf("failed to apply hunk %s: %w", hunk.header, err)
		}
		if len(oldChunk) == 0 {
			oldLines = append(append([]string{}, oldLines[:applyAt]...), append(newChunk, oldLines[applyAt:]...)...)
			cursor = applyAt + len(newChunk)
			continue
		}
		updated := make([]string, 0, len(oldLines)-len(oldChunk)+len(newChunk))
		updated = append(updated, oldLines[:applyAt]...)
		updated = append(updated, newChunk...)
		updated = append(updated, oldLines[applyAt+len(oldChunk):]...)
		oldLines = updated
		cursor = applyAt + len(newChunk)
	}

	result := strings.Join(oldLines, "\n")
	if hasTrailingNewline && result != "" {
		result += "\n"
	}
	return result, nil
}

func locatePatchChunk(lines, oldChunk []string, cursor int) (int, error) {
	if len(oldChunk) == 0 {
		if cursor < 0 {
			cursor = 0
		}
		if cursor > len(lines) {
			cursor = len(lines)
		}
		return cursor, nil
	}
	if cursor < 0 {
		cursor = 0
	}
	if idx := findChunkAtOrAfter(lines, oldChunk, cursor); idx >= 0 {
		return idx, nil
	}
	if idx := findChunkAtOrAfter(lines, oldChunk, 0); idx >= 0 {
		return idx, nil
	}
	return -1, fmt.Errorf("%w: expected context not found", ErrEditTargetMismatch)
}

func findChunkAtOrAfter(lines, chunk []string, start int) int {
	if len(chunk) == 0 {
		return start
	}
	limit := len(lines) - len(chunk)
	for i := start; i <= limit; i++ {
		match := true
		for j := 0; j < len(chunk); j++ {
			if lines[i+j] != chunk[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
