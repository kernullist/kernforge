package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// NotebookEditTool provides cell-aware reading and editing of Jupyter
// notebooks (.ipynb). It parses the notebook JSON, operates on a single cell's
// source by index, and writes the file back while preserving the surrounding
// structure (notebook metadata, nbformat fields, and untouched cells).
type NotebookEditTool struct{ ws Workspace }

func NewNotebookEditTool(ws Workspace) NotebookEditTool { return NotebookEditTool{ws: ws} }

func (t NotebookEditTool) hookWorkspace() Workspace { return t.ws }

func (t NotebookEditTool) SupportsParallelToolCalls() bool {
	return false
}

func (t NotebookEditTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "notebook_edit",
		Description: "Cell-aware reading and editing of a Jupyter notebook (.ipynb). " +
			"Operations: `read` returns a cell's source (or a summary of all cells when no index is given); " +
			"`replace` overwrites a cell's source; `insert` adds a new cell before the given index; " +
			"`delete` removes a cell. Indexes are zero-based.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the .ipynb notebook in the workspace.",
				},
				"operation": map[string]any{
					"type":        "string",
					"enum":        []any{"read", "replace", "insert", "delete"},
					"description": "Cell operation to perform. Defaults to `read`.",
				},
				"cell_index": map[string]any{
					"type":        "integer",
					"description": "Zero-based target cell index. Optional for `read` (summarizes all cells when omitted).",
				},
				"source": map[string]any{
					"type":        "string",
					"description": "New cell source for `replace` and `insert`.",
				},
				"cell_type": map[string]any{
					"type":        "string",
					"enum":        []any{"code", "markdown", "raw"},
					"description": "Cell type for `insert`. Defaults to `code`.",
				},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}
}

func (t NotebookEditTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	operation := strings.ToLower(strings.TrimSpace(stringValue(args, "operation")))
	if operation == "" {
		operation = "read"
	}
	if operation == "read" {
		return t.executeRead(ctx, args)
	}
	return t.executeMutation(ctx, args, operation)
}

func (t NotebookEditTool) executeRead(ctx context.Context, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, _, err := t.resolveReadPath(args)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	nb, err := parseNotebook(data)
	if err != nil {
		return "", err
	}
	if !notebookCellIndexProvided(args) {
		return notebookSummary(nb), nil
	}
	idx := intValue(args, "cell_index", 0)
	if idx < 0 || idx >= len(nb.Cells) {
		return "", fmt.Errorf("notebook_edit: cell_index %d out of range (%d cells)", idx, len(nb.Cells))
	}
	cellType, source := notebookCellTypeAndSource(nb.Cells[idx])
	return fmt.Sprintf("cell %d (%s):\n%s", idx, cellType, source), nil
}

func (t NotebookEditTool) executeMutation(ctx context.Context, args map[string]any, operation string) (string, error) {
	route, err := t.ws.ResolveEditPathWithOptions(EditRoutingRequest{
		Path:              stringValue(args, "path"),
		OwnerNodeID:       stringValue(args, "owner_node_id"),
		ForLookup:         false,
		AllowBaseFallback: true,
	})
	if err != nil {
		return "", err
	}
	path := route.AbsolutePath
	displayPath := route.DisplayPath()
	editRoot := firstNonBlankString(route.WorktreeRoot, route.DisplayRoot, t.ws.Root)
	if err := t.ws.CheckEditBoundary(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	before := string(data)
	nb, err := parseNotebook(data)
	if err != nil {
		return "", err
	}

	idx := intValue(args, "cell_index", -1)
	switch operation {
	case "replace":
		if idx < 0 || idx >= len(nb.Cells) {
			return "", fmt.Errorf("notebook_edit: cell_index %d out of range (%d cells)", idx, len(nb.Cells))
		}
		setNotebookCellSource(nb.Cells[idx], stringValue(args, "source"))
	case "insert":
		insertAt := idx
		if insertAt < 0 {
			insertAt = len(nb.Cells)
		}
		if insertAt > len(nb.Cells) {
			insertAt = len(nb.Cells)
		}
		cellType := strings.ToLower(strings.TrimSpace(stringValue(args, "cell_type")))
		if cellType == "" {
			cellType = "code"
		}
		newCell := newNotebookCell(cellType, stringValue(args, "source"))
		nb.Cells = append(nb.Cells, nil)
		copy(nb.Cells[insertAt+1:], nb.Cells[insertAt:])
		nb.Cells[insertAt] = newCell
	case "delete":
		if idx < 0 || idx >= len(nb.Cells) {
			return "", fmt.Errorf("notebook_edit: cell_index %d out of range (%d cells)", idx, len(nb.Cells))
		}
		nb.Cells = append(nb.Cells[:idx], nb.Cells[idx+1:]...)
	default:
		return "", fmt.Errorf("notebook_edit: unsupported operation %q", operation)
	}

	after, err := marshalNotebook(nb)
	if err != nil {
		return "", err
	}
	if after == before {
		return fmt.Sprintf("no changes to %s; notebook already matches requested edit", displayPath), nil
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	reason := "notebook " + operation + " in " + displayPath
	if _, err := t.ws.Hook(ctx, HookPreEdit, HookPayload{
		"path":          displayPath,
		"absolute_path": path,
		"operation":     "notebook_edit",
		"reason":        reason,
		"file_tags":     hookFileTags(path),
		"owner_node_id": route.OwnerNodeID,
		"worktree_root": route.WorktreeRoot,
		"specialist":    route.Specialist,
	}); err != nil {
		return "", err
	}
	preview := EditPreview{
		Title:     "Update " + displayPath,
		Preview:   buildSelectionAwareEditPreview(t.ws, displayPath, before, after),
		Paths:     []string{displayPath},
		Operation: "notebook_edit",
	}
	if err := t.ws.ReviewProposedEdit(ctx, preview); err != nil {
		return "", err
	}
	if err := t.ws.ConfirmEdit(preview); err != nil {
		return "", err
	}
	if err := t.ws.EnsureWriteWithContext(ctx, path); err != nil {
		return "", err
	}
	if err := t.ws.BeforeEditForRoot(reason, editRoot); err != nil {
		return "", err
	}
	t.ws.Progress("Writing " + displayPath + "...")
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		return "", err
	}
	t.ws.Progress("Saved " + displayPath + ".")
	t.ws.Progress("Running post-edit hooks for " + displayPath + "...")
	if _, err := t.ws.Hook(ctx, HookPostEdit, HookPayload{
		"path":          displayPath,
		"absolute_path": path,
		"operation":     "notebook_edit",
		"reason":        reason,
		"file_tags":     hookFileTags(path),
		"owner_node_id": route.OwnerNodeID,
		"worktree_root": route.WorktreeRoot,
		"specialist":    route.Specialist,
	}); err != nil {
		return "", err
	}
	t.ws.Progress("Post-edit hooks finished for " + displayPath + ".")
	return joinNonEmpty(
		fmt.Sprintf("%s cell in %s (%d cells)", operation, displayPath, len(nb.Cells)),
		buildEditPreview(displayPath, before, after),
	), nil
}

func (t NotebookEditTool) resolveReadPath(args map[string]any) (string, EditRoutingResult, error) {
	route, err := t.ws.ResolveEditPathWithOptions(EditRoutingRequest{
		Path:              stringValue(args, "path"),
		OwnerNodeID:       stringValue(args, "owner_node_id"),
		ForLookup:         true,
		AllowBaseFallback: true,
	})
	if err != nil {
		return "", EditRoutingResult{}, err
	}
	return route.AbsolutePath, route, nil
}

func (t NotebookEditTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	operation := strings.ToLower(strings.TrimSpace(stringValue(args, "operation")))
	if operation == "" {
		operation = "read"
	}
	text, err := t.Execute(ctx, input)
	changedWorkspace := err == nil && operation != "read"
	meta := map[string]any{
		"path":                  strings.TrimSpace(stringValue(args, "path")),
		"operation":             operation,
		"owner_node_id":         strings.TrimSpace(stringValue(args, "owner_node_id")),
		"changed_workspace":     changedWorkspace,
		"requires_verification": changedWorkspace,
	}
	if operation == "read" {
		meta["effect"] = "read"
	} else {
		meta["effect"] = "edit"
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

// notebookDocument keeps the notebook's top-level fields as raw JSON so that
// untouched keys (metadata, nbformat, nbformat_minor) round-trip unchanged.
type notebookDocument struct {
	fields map[string]json.RawMessage
	order  []string
	Cells  []map[string]json.RawMessage
}

func parseNotebook(data []byte) (*notebookDocument, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("notebook_edit: invalid .ipynb JSON: %w", err)
	}
	rawCells, ok := fields["cells"]
	if !ok {
		return nil, fmt.Errorf("notebook_edit: notebook has no `cells` array")
	}
	var cells []map[string]json.RawMessage
	if err := json.Unmarshal(rawCells, &cells); err != nil {
		return nil, fmt.Errorf("notebook_edit: invalid `cells` array: %w", err)
	}
	doc := &notebookDocument{
		fields: fields,
		order:  notebookFieldOrder(data),
		Cells:  cells,
	}
	return doc, nil
}

// notebookFieldOrder preserves the original top-level key order so the rewritten
// file keeps a stable, diff-friendly layout.
func notebookFieldOrder(data []byte) []string {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	tok, err := dec.Token()
	if err != nil {
		return nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil
	}
	var order []string
	depth := 0
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return order
		}
		key, ok := keyTok.(string)
		if !ok {
			return order
		}
		if depth == 0 {
			order = append(order, key)
		}
		// Skip the value for this key without recording nested keys.
		if err := skipNotebookValue(dec); err != nil {
			return order
		}
	}
	return order
}

func skipNotebookValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return nil
	}
	for dec.More() {
		if delim == '{' {
			if _, err := dec.Token(); err != nil { // key
				return err
			}
		}
		if err := skipNotebookValue(dec); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // closing delim
		return err
	}
	return nil
}

func marshalNotebook(doc *notebookDocument) (string, error) {
	cellsJSON, err := json.Marshal(doc.Cells)
	if err != nil {
		return "", err
	}
	doc.fields["cells"] = cellsJSON

	keys := doc.order
	if len(keys) == 0 {
		for k := range doc.fields {
			keys = append(keys, k)
		}
	}
	seen := map[string]bool{}
	var b strings.Builder
	b.WriteString("{\n")
	first := true
	writeField := func(key string) error {
		raw, ok := doc.fields[key]
		if !ok || seen[key] {
			return nil
		}
		seen[key] = true
		if !first {
			b.WriteString(",\n")
		}
		first = false
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return err
		}
		b.WriteString(" ")
		b.Write(keyJSON)
		b.WriteString(": ")
		var pretty strings.Builder
		if err := indentNotebookValue(&pretty, raw, " "); err != nil {
			return err
		}
		b.WriteString(pretty.String())
		return nil
	}
	for _, key := range keys {
		if err := writeField(key); err != nil {
			return "", err
		}
	}
	// Emit any fields that were not in the recorded order.
	for key := range doc.fields {
		if err := writeField(key); err != nil {
			return "", err
		}
	}
	b.WriteString("\n}\n")
	return b.String(), nil
}

func indentNotebookValue(b *strings.Builder, raw json.RawMessage, prefix string) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, prefix, " "); err != nil {
		// Fall back to the compact form if the raw message is not standard JSON.
		b.Write(raw)
		return nil
	}
	b.Write(pretty.Bytes())
	return nil
}

func notebookCellIndexProvided(args map[string]any) bool {
	if _, ok := args["cell_index"]; ok {
		return true
	}
	return false
}

func notebookCellTypeAndSource(cell map[string]json.RawMessage) (string, string) {
	cellType := "code"
	if raw, ok := cell["cell_type"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			cellType = s
		}
	}
	return cellType, notebookSourceToString(cell["source"])
}

func notebookSourceToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var asLines []string
	if err := json.Unmarshal(raw, &asLines); err == nil {
		return strings.Join(asLines, "")
	}
	return ""
}

// setNotebookCellSource stores the source as the canonical nbformat list of
// lines: every line keeps its trailing newline except the final one.
func setNotebookCellSource(cell map[string]json.RawMessage, source string) {
	cell["source"] = notebookSourceLines(source)
}

func notebookSourceLines(source string) json.RawMessage {
	lines := splitNotebookSourceLines(source)
	encoded, err := json.Marshal(lines)
	if err != nil {
		// json.Marshal of []string cannot realistically fail; fall back to an
		// empty array to keep the notebook valid.
		return json.RawMessage("[]")
	}
	return encoded
}

func splitNotebookSourceLines(source string) []string {
	if source == "" {
		return []string{}
	}
	parts := strings.SplitAfter(source, "\n")
	// SplitAfter leaves a trailing empty element when the source ends with a
	// newline; drop it so the stored list matches nbformat conventions.
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func newNotebookCell(cellType, source string) map[string]json.RawMessage {
	cell := map[string]json.RawMessage{
		"cell_type": mustNotebookJSON(cellType),
		"metadata":  json.RawMessage("{}"),
		"source":    notebookSourceLines(source),
	}
	if cellType == "code" {
		cell["execution_count"] = json.RawMessage("null")
		cell["outputs"] = json.RawMessage("[]")
	}
	return cell
}

func mustNotebookJSON(value string) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

func notebookSummary(nb *notebookDocument) string {
	var b strings.Builder
	fmt.Fprintf(&b, "notebook with %d cells\n", len(nb.Cells))
	for i, cell := range nb.Cells {
		cellType, source := notebookCellTypeAndSource(cell)
		preview := strings.ReplaceAll(source, "\n", " ")
		preview = strings.TrimSpace(preview)
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		fmt.Fprintf(&b, "  [%d] %s: %s\n", i, cellType, preview)
	}
	return strings.TrimRight(b.String(), "\n")
}
