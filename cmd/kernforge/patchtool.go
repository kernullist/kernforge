package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ApplyPatchTool struct{ ws Workspace }

func NewApplyPatchTool(ws Workspace) ApplyPatchTool { return ApplyPatchTool{ws: ws} }

func (t ApplyPatchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "apply_patch",
		Description: "Apply a precise patch to one or more existing files using a Begin/End Patch format. Use this as an expert fallback for complex hunk-level edits after reading current file contents; prefer apply_edit_proposal for simple reviewed edit intent. Keep payloads narrow and anchored to current file contents; split large fixes into the first independent hunk, reread, then continue. Every update file section must contain at least one @@ hunk.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patch":         map[string]any{"type": "string"},
				"owner_node_id": map[string]any{"type": "string"},
				"workdir":       map[string]any{"type": "string"},
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
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	patchText := stringValue(args, "patch")
	if strings.TrimSpace(patchText) == "" {
		if rawPatchText := stringValue(args, "raw"); strings.TrimSpace(rawPatchText) != "" {
			patchText = rawPatchText
			args["patch"] = patchText
		}
	}
	if strings.TrimSpace(patchText) == "" {
		return ToolExecutionResult{}, fmt.Errorf("patch is required")
	}
	ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id"))
	ws, err := t.workspaceForWorkdir(stringValue(args, "workdir"))
	if err != nil {
		return ToolExecutionResult{}, err
	}
	originalPatchText := patchText
	hookPayload := HookPayload{
		"tool_name":     "apply_patch",
		"tool_kind":     "edit",
		"command":       originalPatchText,
		"patch":         originalPatchText,
		"file_tags":     []string{},
		"owner_node_id": ownerNodeID,
		"work_dir":      ws.Root,
	}
	addEffectiveExecutionContextMetadata(hookPayload, ws, nil)
	verdict, err := ws.Hook(ctx, HookPreToolUse, hookPayload)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if updatedPatchText, changed, updateErr := hookUpdatedPatchText(verdict, "apply_patch", patchText); updateErr != nil {
		return ToolExecutionResult{}, updateErr
	} else if changed {
		patchText = updatedPatchText
		args["patch"] = patchText
	}
	doc, err := parsePatchDocument(patchText)
	if err != nil {
		return ToolExecutionResult{}, fmt.Errorf("%w: %v", ErrInvalidPatchFormat, err)
	}
	if len(doc.ops) == 0 {
		return ToolExecutionResult{}, fmt.Errorf("%w: No files were modified.", ErrInvalidPatchFormat)
	}
	text, changedWorkspace, changedPaths, unifiedDiff, err := applyPatchDocument(ctx, ws, doc, ownerNodeID)
	meta := map[string]any{
		"changed_workspace":     false,
		"requires_verification": false,
	}
	if err == nil {
		meta = buildApplyPatchMeta(ws.Root, doc)
		normalizedChangedPaths := normalizeTaskStateList(changedPaths, 64)
		meta["changed_paths"] = normalizedChangedPaths
		meta["changed_count"] = len(normalizedChangedPaths)
		meta["changed_workspace"] = changedWorkspace
		meta["requires_verification"] = changedWorkspace
		if strings.TrimSpace(unifiedDiff) != "" {
			meta["unified_diff"] = unifiedDiff
		}
	} else if changedWorkspace {
		meta = map[string]any{
			"changed_workspace":     true,
			"requires_verification": true,
			"changed_paths":         normalizeWorkspaceSignaturePathList(changedPaths),
		}
		if strings.TrimSpace(unifiedDiff) != "" {
			meta["unified_diff"] = unifiedDiff
		}
	}
	if ownerNodeID != "" {
		meta["owner_node_id"] = ownerNodeID
	}
	addEffectiveExecutionContextMetadata(meta, ws, nil)
	return ToolExecutionResult{
		DisplayText: text,
		Meta:        meta,
	}, err
}

func (t ApplyPatchTool) workspaceForWorkdir(workdir string) (Workspace, error) {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return t.ws, nil
	}
	root := firstNonBlankString(t.ws.Root, t.ws.BaseRoot)
	resolved, err := t.ws.resolveAgainstRoot(root, filepath.FromSlash(workdir))
	if err != nil {
		return Workspace{}, err
	}
	resolved, err = t.ws.ensureWithinBaseRoot(workdir, resolved)
	if err != nil {
		return Workspace{}, err
	}
	next := t.ws
	next.Root = resolved
	return next, nil
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
				if movePath := relOrAbs(root, op.moveTo); strings.TrimSpace(movePath) != "" {
					changedPaths = append(changedPaths, movePath)
				}
			}
		}
	}
	changedPaths = normalizeTaskStateList(changedPaths, 64)
	return map[string]any{
		"changed_paths":         changedPaths,
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
	text = normalizePatchDocumentText(text)
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
		return patchDocument{}, fmt.Errorf("patch_format_missing_begin: patch must start with *** Begin Patch")
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
			for index < len(lines) {
				if strings.TrimSpace(advance()) != "" {
					return patchDocument{}, fmt.Errorf("patch_format_trailing_content: patch must end with *** End Patch")
				}
			}
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
				path: normalizePatchDocumentPath(strings.TrimPrefix(line, "*** Delete File: ")),
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
			return patchDocument{}, fmt.Errorf("patch_format_unexpected_line: unexpected patch line: %s", line)
		}
	}
	return patchDocument{}, fmt.Errorf("patch_format_missing_end: patch is missing *** End Patch")
}

func normalizePatchDocumentText(text string) string {
	text = strings.TrimPrefix(text, "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSpace(text)
}

func normalizePatchDocumentPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`\"'")
	path = strings.ReplaceAll(path, "\\", "/")
	return path
}

func parseAddFileOp(lines []string, index *int) (patchOperation, error) {
	line := lines[*index]
	path := normalizePatchDocumentPath(strings.TrimPrefix(line, "*** Add File: "))
	*index++
	var addLines []string
	for *index < len(lines) {
		current := lines[*index]
		if strings.HasPrefix(current, "*** ") {
			break
		}
		if !strings.HasPrefix(current, "+") {
			return patchOperation{}, fmt.Errorf("patch_format_invalid_add_line: add file patch lines must start with +: %s", current)
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
		path: normalizePatchDocumentPath(strings.TrimPrefix(line, "*** Update File: ")),
	}
	*index++
	if *index < len(lines) && strings.HasPrefix(lines[*index], "*** Move to: ") {
		op.moveTo = normalizePatchDocumentPath(strings.TrimPrefix(lines[*index], "*** Move to: "))
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
			return patchOperation{}, fmt.Errorf("patch_format_missing_hunk: update patch hunk must start with @@: %s", current)
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
				hunk.lines = append(hunk.lines, patchLine{kind: ' ', text: ""})
				*index++
				continue
			}
			prefix := current[0]
			if prefix != ' ' && prefix != '+' && prefix != '-' {
				return patchOperation{}, fmt.Errorf("patch_format_invalid_hunk_line: invalid patch line in hunk: %s", current)
			}
			hunk.lines = append(hunk.lines, patchLine{kind: prefix, text: current[1:]})
			*index++
		}
		op.hunks = append(op.hunks, hunk)
	}
	if len(op.hunks) == 0 && op.moveTo == "" {
		return patchOperation{}, fmt.Errorf("patch_format_empty_update: update patch for %s has no hunks", op.path)
	}
	return op, nil
}

func applyPatchDocument(ctx context.Context, ws Workspace, doc patchDocument, ownerNodeID string) (string, bool, []string, string, error) {
	planned, err := planPatchDocument(ws, doc, ownerNodeID)
	if err != nil {
		return "", false, nil, "", err
	}
	if len(planned) == 0 {
		return "No patch operations to apply.", false, nil, "", nil
	}
	var previewBlocks []string
	var previewPaths []string
	var proposals []EditProposal
	for _, change := range planned {
		target := relOrAbs(change.displayRoot, change.destPath)
		if change.kind == "delete" {
			target = relOrAbs(change.displayRoot, change.srcPath)
		}
		expectedPreview := buildSelectionAwareEditPreview(ws, target, change.before, change.after)
		previewPaths = append(previewPaths, target)
		previewBlocks = append(previewBlocks, expectedPreview)
		proposal := EditProposal{
			File:         filepath.ToSlash(target),
			Operation:    "apply_patch",
			Rationale:    change.summary,
			Risk:         editProposalRiskForOperation("apply_patch", len(planned)),
			AfterExcerpt: buildSelectionAwareAfterExcerpt(ws, target, change.before, change.after, 12000),
		}
		proposals = append(proposals, bindEditProposalPreview(proposal, "apply_patch", target, expectedPreview))
	}
	preview := EditPreview{
		Title:     fmt.Sprintf("Apply patch to %d file(s)", len(planned)),
		Preview:   strings.Join(previewBlocks, "\n\n"),
		Paths:     normalizeTaskStateList(previewPaths, 64),
		Operation: "apply_patch",
		Proposals: normalizeEditProposals(proposals),
	}
	if err := ws.ReviewProposedEdit(ctx, preview); err != nil {
		return "", false, nil, "", err
	}
	if err := ws.ConfirmEdit(preview); err != nil {
		return "", false, nil, "", err
	}
	if err := ensurePlannedPatchWrites(ctx, ws, planned); err != nil {
		return "", false, nil, "", err
	}
	editRoot := ws.Root
	if len(planned) > 0 {
		editRoot = firstNonBlankString(planned[0].displayRoot, ws.Root)
	}
	if err := ws.BeforeEditForRoot(fmt.Sprintf("apply patch (%d file(s))", len(planned)), editRoot); err != nil {
		return "", false, nil, "", err
	}

	var summaries []string
	var previews []string
	var unifiedDiffs []string
	mutated := false
	var changedPaths []string
	for _, change := range planned {
		select {
		case <-ctx.Done():
			return "", mutated, changedPaths, strings.Join(unifiedDiffs, "\n"), ctx.Err()
		default:
		}
		switch change.kind {
		case "add":
			ws.Progress("Writing " + relOrAbs(change.displayRoot, change.destPath) + "...")
			if err := ensureParentDir(change.destPath); err != nil {
				return "", mutated, changedPaths, strings.Join(unifiedDiffs, "\n"), err
			}
			if err := os.WriteFile(change.destPath, []byte(change.after), 0o644); err != nil {
				return "", mutated, changedPaths, strings.Join(unifiedDiffs, "\n"), err
			}
			mutated = true
			changedPaths = append(changedPaths, relOrAbs(change.displayRoot, change.destPath))
			ws.Progress("Saved " + relOrAbs(change.displayRoot, change.destPath) + ".")
			summaries = append(summaries, change.summary)
			previews = append(previews, buildEditPreview(relOrAbs(change.displayRoot, change.destPath), "", change.after))
			if diff := buildUnifiedDiff(relOrAbs(change.displayRoot, change.destPath), "", change.after); strings.TrimSpace(diff) != "" {
				unifiedDiffs = append(unifiedDiffs, diff)
			}
		case "delete":
			ws.Progress("Deleting " + relOrAbs(change.displayRoot, change.srcPath) + "...")
			if err := os.Remove(change.srcPath); err != nil {
				return "", mutated, changedPaths, strings.Join(unifiedDiffs, "\n"), err
			}
			mutated = true
			changedPaths = append(changedPaths, relOrAbs(change.displayRoot, change.srcPath))
			ws.Progress("Deleted " + relOrAbs(change.displayRoot, change.srcPath) + ".")
			summaries = append(summaries, change.summary)
			previews = append(previews, buildEditPreview(relOrAbs(change.displayRoot, change.srcPath), change.before, ""))
			if diff := buildUnifiedDiff(relOrAbs(change.displayRoot, change.srcPath), change.before, ""); strings.TrimSpace(diff) != "" {
				unifiedDiffs = append(unifiedDiffs, diff)
			}
		case "update":
			ws.Progress("Writing " + relOrAbs(change.displayRoot, change.destPath) + "...")
			if err := ensureParentDir(change.destPath); err != nil {
				return "", mutated, changedPaths, strings.Join(unifiedDiffs, "\n"), err
			}
			if err := os.WriteFile(change.destPath, []byte(change.after), 0o644); err != nil {
				return "", mutated, changedPaths, strings.Join(unifiedDiffs, "\n"), err
			}
			mutated = true
			changedPaths = append(changedPaths, relOrAbs(change.displayRoot, change.destPath))
			if diff := buildUnifiedDiff(relOrAbs(change.displayRoot, change.destPath), change.before, change.after); strings.TrimSpace(diff) != "" {
				unifiedDiffs = append(unifiedDiffs, diff)
			}
			if change.destPath != change.srcPath {
				if err := os.Remove(change.srcPath); err != nil {
					return "", mutated, changedPaths, strings.Join(unifiedDiffs, "\n"), err
				}
				mutated = true
				changedPaths = append(changedPaths, relOrAbs(change.displayRoot, change.srcPath))
			}
			ws.Progress("Saved " + relOrAbs(change.displayRoot, change.destPath) + ".")
			summaries = append(summaries, change.summary)
			previews = append(previews, buildEditPreview(relOrAbs(change.displayRoot, change.destPath), change.before, change.after))
		default:
			return "", mutated, changedPaths, strings.Join(unifiedDiffs, "\n"), fmt.Errorf("unsupported patch operation: %s", change.kind)
		}
	}
	return joinNonEmpty(strings.Join(summaries, "\n"), strings.Join(previews, "\n\n")), mutated, changedPaths, strings.Join(unifiedDiffs, "\n"), nil
}

func ensurePlannedPatchWrites(ctx context.Context, ws Workspace, planned []plannedPatchChange) error {
	for _, change := range planned {
		switch change.kind {
		case "add":
			if err := ws.EnsureWriteWithContext(ctx, change.destPath); err != nil {
				return err
			}
		case "delete":
			if err := ws.EnsureWriteWithContext(ctx, change.srcPath); err != nil {
				return err
			}
		case "update":
			if err := ws.EnsureWriteWithContext(ctx, change.srcPath); err != nil {
				return err
			}
			if change.destPath != change.srcPath {
				if err := ws.EnsureWriteWithContext(ctx, change.destPath); err != nil {
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
			if err := ws.CheckEditBoundary(path); err != nil {
				return nil, err
			}
			before := ""
			if info, err := os.Lstat(path); err == nil {
				if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
					return nil, fmt.Errorf("cannot add file that already exists: %s", op.path)
				}
				existing, err := os.ReadFile(path)
				if err != nil {
					return nil, err
				}
				before = string(existing)
			} else if !os.IsNotExist(err) {
				return nil, err
			}
			content := strings.Join(op.addLines, "\n")
			if len(op.addLines) > 0 {
				content += "\n"
			}
			planned = append(planned, plannedPatchChange{
				kind:        "add",
				destPath:    path,
				displayRoot: firstNonBlankString(route.DisplayRoot, ws.Root),
				before:      before,
				after:       content,
				summary:     "added " + route.DisplayPath(),
			})
		case "delete":
			route, err := ws.ResolveEditPath(op.path, ownerNodeID, false)
			if err != nil {
				return nil, err
			}
			path := route.AbsolutePath
			if err := ws.CheckEditBoundary(path); err != nil {
				return nil, err
			}
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
			srcRoute, err := ws.ResolveEditPath(op.path, ownerNodeID, false)
			if err != nil {
				return nil, err
			}
			srcPath := srcRoute.AbsolutePath
			destPath := srcPath
			displayRoot := firstNonBlankString(srcRoute.DisplayRoot, ws.Root)
			if err := ws.CheckEditBoundary(srcPath); err != nil {
				return nil, err
			}
			if strings.TrimSpace(op.moveTo) != "" {
				destRoute, err := ws.ResolveEditPath(op.moveTo, ownerNodeID, false)
				if err != nil {
					return nil, err
				}
				destPath = destRoute.AbsolutePath
				displayRoot = firstNonBlankString(destRoute.DisplayRoot, displayRoot, ws.Root)
				if destPath != srcPath {
					if err := ws.CheckEditBoundary(destPath); err != nil {
						return nil, err
					}
				}
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
	lineEnding := detectPatchLineEnding(content)
	content = normalizePatchLineEndings(content)
	content = strings.TrimSuffix(content, "\n")
	oldLines := []string{}
	if content != "" {
		oldLines = strings.Split(content, "\n")
	}

	cursor := 0
	lineDelta := 0
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
		if len(oldChunk) == 0 {
			return "", fmt.Errorf("%w: update hunk has no context or deletion lines", ErrEditTargetMismatch)
		}

		applyAt, err := locatePatchChunk(oldLines, oldChunk, cursor, hunk.header, lineDelta)
		if err != nil {
			return "", fmt.Errorf("failed to apply hunk %s: %w", hunk.header, err)
		}
		updated := make([]string, 0, len(oldLines)-len(oldChunk)+len(newChunk))
		updated = append(updated, oldLines[:applyAt]...)
		updated = append(updated, newChunk...)
		updated = append(updated, oldLines[applyAt+len(oldChunk):]...)
		oldLines = updated
		cursor = applyAt + len(newChunk)
		lineDelta += len(newChunk) - len(oldChunk)
	}

	result := strings.Join(oldLines, lineEnding)
	if result != "" {
		result += lineEnding
	}
	return result, nil
}

func normalizePatchLineEndings(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return content
}

func detectPatchLineEnding(content string) string {
	if content == "" {
		return "\n"
	}
	crlfCount := strings.Count(content, "\r\n")
	withoutCRLF := strings.ReplaceAll(content, "\r\n", "")
	lfCount := strings.Count(withoutCRLF, "\n")
	crCount := strings.Count(withoutCRLF, "\r")
	if crlfCount > 0 && crlfCount >= lfCount && crlfCount >= crCount {
		return "\r\n"
	}
	if crCount > 0 && crCount > lfCount {
		return "\r"
	}
	return "\n"
}

func locatePatchChunk(lines, oldChunk []string, cursor int, header string, lineDelta int) (int, error) {
	if len(oldChunk) == 0 {
		return -1, fmt.Errorf("%w: expected context is empty", ErrEditTargetMismatch)
	}
	if cursor < 0 {
		cursor = 0
	}
	if oldStart, ok := parsePatchHunkOldStart(header); ok {
		expectedIndex := oldStart - 1 + lineDelta
		if expectedIndex >= cursor && chunkMatchesAt(lines, oldChunk, expectedIndex) {
			return expectedIndex, nil
		}
	}
	matches := findChunkMatchesAtOrAfter(lines, oldChunk, cursor)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return -1, fmt.Errorf("%w: expected context is ambiguous (%d matches)%s", ErrEditTargetMismatch, len(matches), formatPatchMismatchDiagnostic(lines, oldChunk, cursor, header, lineDelta, matches))
	}
	return -1, fmt.Errorf("%w: expected context not found%s", ErrEditTargetMismatch, formatPatchMismatchDiagnostic(lines, oldChunk, cursor, header, lineDelta, nil))
}

func formatPatchMismatchDiagnostic(lines, oldChunk []string, cursor int, header string, lineDelta int, matches []int) string {
	var b strings.Builder
	trimmedHeader := strings.TrimSpace(header)
	if trimmedHeader != "" {
		fmt.Fprintf(&b, "\nhunk header: %s", trimmedHeader)
	}
	if cursor < 0 {
		cursor = 0
	}
	fmt.Fprintf(&b, "\nsearch started at current line: %d", cursor+1)
	if oldStart, ok := parsePatchHunkOldStart(header); ok {
		fmt.Fprintf(&b, "\nhunk header resolves to current line: %d", oldStart+lineDelta)
	}
	firstLine := ""
	if first, ok := firstPatchDiagnosticLine(oldChunk); ok {
		firstLine = first
		fmt.Fprintf(&b, "\nexpected first line: %s", quotePatchDiagnosticLine(first))
	}
	if last, ok := lastPatchDiagnosticLine(oldChunk); ok && last != firstLine {
		fmt.Fprintf(&b, "\nexpected last line: %s", quotePatchDiagnosticLine(last))
	}

	candidates := patchMismatchCandidateStarts(lines, oldChunk, cursor, header, lineDelta, matches)
	if len(candidates) == 0 {
		return b.String()
	}
	b.WriteString("\nnearest current context:")
	contextLineCount := len(oldChunk)
	if contextLineCount > 5 {
		contextLineCount = 5
	}
	if contextLineCount < 1 {
		contextLineCount = 1
	}
	for _, start := range candidates {
		if start < 0 {
			start = 0
		}
		if start >= len(lines) {
			continue
		}
		fmt.Fprintf(&b, "\n  candidate line %d:", start+1)
		end := start + contextLineCount
		if end > len(lines) {
			end = len(lines)
		}
		for i := start; i < end; i++ {
			fmt.Fprintf(&b, "\n    %d: %s", i+1, quotePatchDiagnosticLine(lines[i]))
		}
	}
	return b.String()
}

func firstPatchDiagnosticLine(lines []string) (string, bool) {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return line, true
		}
	}
	if len(lines) > 0 {
		return lines[0], true
	}
	return "", false
}

func lastPatchDiagnosticLine(lines []string) (string, bool) {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i], true
		}
	}
	if len(lines) > 0 {
		return lines[len(lines)-1], true
	}
	return "", false
}

func patchMismatchCandidateStarts(lines, oldChunk []string, cursor int, header string, lineDelta int, matches []int) []int {
	const maxCandidates = 3
	candidates := make([]int, 0, maxCandidates)
	addCandidate := func(index int) {
		if index < 0 || index >= len(lines) {
			return
		}
		for _, existing := range candidates {
			if existing == index {
				return
			}
		}
		if len(candidates) < maxCandidates {
			candidates = append(candidates, index)
		}
	}
	for _, match := range matches {
		addCandidate(match)
	}
	if len(candidates) >= maxCandidates {
		return candidates
	}
	if oldStart, ok := parsePatchHunkOldStart(header); ok {
		addCandidate(oldStart - 1 + lineDelta)
	}
	if len(candidates) >= maxCandidates {
		return candidates
	}
	anchor, ok := firstPatchDiagnosticLine(oldChunk)
	if !ok {
		return candidates
	}
	if cursor < 0 {
		cursor = 0
	}
	for i := cursor; i < len(lines) && len(candidates) < maxCandidates; i++ {
		if lines[i] == anchor {
			addCandidate(i)
		}
	}
	return candidates
}

func quotePatchDiagnosticLine(line string) string {
	const maxLen = 160
	if len(line) > maxLen {
		line = line[:maxLen] + "...(truncated)"
	}
	return strconv.Quote(line)
}

func parsePatchHunkOldStart(header string) (int, bool) {
	for _, field := range strings.Fields(header) {
		if !strings.HasPrefix(field, "-") {
			continue
		}
		spec := strings.TrimPrefix(field, "-")
		if comma := strings.Index(spec, ","); comma >= 0 {
			spec = spec[:comma]
		}
		start, err := strconv.Atoi(spec)
		if err == nil && start > 0 {
			return start, true
		}
	}
	return 0, false
}

func findChunkMatchesAtOrAfter(lines, chunk []string, start int) []int {
	if len(chunk) == 0 {
		return []int{start}
	}
	limit := len(lines) - len(chunk)
	matches := []int{}
	for i := start; i <= limit; i++ {
		if chunkMatchesAt(lines, chunk, i) {
			matches = append(matches, i)
		}
	}
	return matches
}

func chunkMatchesAt(lines, chunk []string, index int) bool {
	if index < 0 || index+len(chunk) > len(lines) {
		return false
	}
	for j := 0; j < len(chunk); j++ {
		if lines[index+j] != chunk[j] {
			return false
		}
	}
	return true
}
