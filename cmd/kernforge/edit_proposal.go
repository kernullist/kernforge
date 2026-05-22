package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ApplyEditProposalTool struct{ ws Workspace }

const editProposalExpectedPreviewLimit = 12000

func NewApplyEditProposalTool(ws Workspace) ApplyEditProposalTool {
	return ApplyEditProposalTool{ws: ws}
}

func (t ApplyEditProposalTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "apply_edit_proposal",
		Description: "Apply a structured edit proposal after runtime preview, automatic review, and user approval. Prefer this for exact-search replacements, simple add/write/delete edits, and reviewed edit intent; use apply_patch only for complex hunk-level fallback.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file":          map[string]any{"type": "string"},
				"operation":     map[string]any{"type": "string", "description": "replace_in_file, write_file, add_file, delete_file, or modify"},
				"exact_search":  map[string]any{"type": "string"},
				"replacement":   map[string]any{"type": "string"},
				"content":       map[string]any{"type": "string"},
				"all":           map[string]any{"type": "boolean"},
				"rationale":     map[string]any{"type": "string"},
				"risk":          map[string]any{"type": "string"},
				"owner_node_id": map[string]any{"type": "string"},
				"anchor_before": map[string]any{"type": "string"},
				"replace_range": map[string]any{"type": "string"},
			},
			"required": []string{"file"},
		},
	}
}

func (t ApplyEditProposalTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t ApplyEditProposalTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	proposal := editProposalFromToolInput(args)
	ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id"))
	all := boolValue(args, "all", false)
	text, meta, err := applyEditProposal(ctx, t.ws, proposal, ownerNodeID, all)
	return ToolExecutionResult{
		DisplayText: text,
		Meta:        meta,
	}, err
}

func editProposalFingerprintTargetForPaths(paths []string) string {
	normalized := normalizeEditProposalPathList(paths, 0)
	if len(normalized) == 0 {
		return ""
	}
	if len(normalized) == 1 {
		return normalized[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "paths:%d:", len(normalized))
	for _, path := range normalized {
		fmt.Fprintf(&b, "%d:", len(path))
		b.WriteString(path)
		b.WriteByte(';')
	}
	return b.String()
}

func editProposalsFromPreview(preview EditPreview) []EditProposal {
	paths := normalizeEditProposalPathList(preview.Paths, 0)
	if len(paths) == 0 {
		paths = []string{""}
	}
	operation := strings.TrimSpace(preview.Operation)
	if operation == "" {
		operation = inferEditProposalOperation(preview)
	}
	expected := compactEditProposalExpectedPreview(preview.Preview)
	expectedComplete := editProposalExpectedPreviewIsComplete(preview.Preview)
	fingerprint := computeReviewFingerprint(operation, editProposalFingerprintTargetForPaths(paths), preview.Preview)
	if len(paths) > 1 {
		return normalizeEditProposals([]EditProposal{{
			Files:                     paths,
			Operation:                 operation,
			Rationale:                 strings.TrimSpace(preview.Title),
			Risk:                      editProposalRiskForOperation(operation, len(paths)),
			ExpectedPreview:           expected,
			ExpectedComplete:          boolPointer(expectedComplete),
			PreviewFingerprint:        fingerprint,
			trustedPreviewFingerprint: fingerprint,
		}})
	}
	var proposals []EditProposal
	for _, path := range paths {
		proposals = append(proposals, EditProposal{
			File:                      filepath.ToSlash(strings.TrimSpace(path)),
			Operation:                 operation,
			Rationale:                 strings.TrimSpace(preview.Title),
			Risk:                      editProposalRiskForOperation(operation, len(paths)),
			ExpectedPreview:           expected,
			ExpectedComplete:          boolPointer(expectedComplete),
			PreviewFingerprint:        fingerprint,
			trustedPreviewFingerprint: fingerprint,
		})
	}
	return normalizeEditProposals(proposals)
}

func bindEditProposalPreview(proposal EditProposal, operation string, fingerprintTarget string, previewText string) EditProposal {
	fingerprint := computeReviewFingerprint(operation, fingerprintTarget, previewText)
	proposal.ExpectedPreview = compactEditProposalExpectedPreview(previewText)
	proposal.ExpectedComplete = boolPointer(editProposalExpectedPreviewIsComplete(previewText))
	proposal.PreviewFingerprint = fingerprint
	proposal.trustedPreviewFingerprint = fingerprint
	return proposal
}

func compactEditProposalExpectedPreview(previewText string) string {
	return compactEditProposalPayloadForEvidence(previewText, editProposalExpectedPreviewLimit)
}

func editProposalExpectedPreviewIsComplete(previewText string) bool {
	if strings.TrimSpace(previewText) == "" {
		return true
	}
	return editProposalExpectedPreviewLimit <= 0 || len(previewText) <= editProposalExpectedPreviewLimit
}

func boolPointer(value bool) *bool {
	return &value
}

func editProposalsForPreview(preview EditPreview) []EditProposal {
	if len(preview.Proposals) > 0 {
		return normalizeEditProposals(preview.Proposals)
	}
	return editProposalsFromPreview(preview)
}

func normalizeEditProposals(proposals []EditProposal) []EditProposal {
	var out []EditProposal
	for _, proposal := range proposals {
		proposal.File = filepath.ToSlash(strings.TrimSpace(proposal.File))
		proposal.Files = normalizeEditProposalFiles(proposal.Files)
		proposal.Operation = strings.TrimSpace(proposal.Operation)
		proposal.ReplaceRange = strings.TrimSpace(proposal.ReplaceRange)
		proposal.Rationale = strings.TrimSpace(proposal.Rationale)
		proposal.Risk = strings.TrimSpace(proposal.Risk)
		proposal.PreviewFingerprint = strings.TrimSpace(proposal.PreviewFingerprint)
		if proposal.File == "" &&
			proposal.Operation == "" &&
			len(proposal.Files) == 0 &&
			strings.TrimSpace(proposal.ExpectedPreview) == "" &&
			strings.TrimSpace(proposal.AfterExcerpt) == "" &&
			strings.TrimSpace(proposal.AnchorBefore) == "" &&
			proposal.ReplaceRange == "" &&
			proposal.ExactSearch == "" &&
			proposal.Replacement == "" {
			continue
		}
		if proposal.Operation == "" {
			proposal.Operation = "modify"
		}
		out = append(out, proposal)
	}
	return out
}

func normalizeEditProposalFiles(files []string) []string {
	return normalizeEditProposalPathList(files, 0)
}

func normalizeEditProposalPathList(files []string, limit int) []string {
	normalized := make([]string, 0, len(files))
	for _, file := range files {
		trimmed := strings.Join(strings.Fields(filepath.ToSlash(strings.TrimSpace(file))), " ")
		if trimmed == "" {
			continue
		}
		duplicate := false
		for _, existing := range normalized {
			if strings.EqualFold(existing, trimmed) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	if limit > 0 && len(normalized) > limit {
		normalized = normalized[len(normalized)-limit:]
	}
	return normalized
}

func renderEditProposalsForEvidence(proposals []EditProposal) string {
	return renderEditProposalsForEvidenceWithOptions(proposals, false)
}

func renderEditProposalsForEvidenceWithoutAfterExcerpt(proposals []EditProposal) string {
	return renderEditProposalsForEvidenceWithOptions(proposals, true)
}

func renderEditProposalsForEvidenceWithOptions(proposals []EditProposal, omitAfterExcerpt bool) string {
	proposals = normalizeEditProposals(proposals)
	if len(proposals) == 0 {
		return ""
	}
	var b strings.Builder
	for i, proposal := range limitEditProposalsForEvidence(proposals, 16) {
		fmt.Fprintf(&b, "proposal_%d:\n", i+1)
		if proposal.File != "" {
			fmt.Fprintf(&b, "  file: %s\n", reviewEvidenceScalar(proposal.File))
		}
		if len(proposal.Files) > 0 {
			displayFiles := normalizeEditProposalPathList(proposal.Files, 64)
			if len(proposal.Files) > len(displayFiles) {
				displayFiles = append([]string{fmt.Sprintf("... %d older path(s) omitted", len(proposal.Files)-len(displayFiles))}, displayFiles...)
			}
			fmt.Fprintf(&b, "  files: %s\n", reviewEvidenceScalar(strings.Join(displayFiles, ", ")))
		}
		fmt.Fprintf(&b, "  operation: %s\n", reviewEvidenceScalar(proposal.Operation))
		if proposal.Rationale != "" {
			fmt.Fprintf(&b, "  rationale: %s\n", reviewEvidenceScalar(proposal.Rationale))
		}
		if proposal.Risk != "" {
			fmt.Fprintf(&b, "  risk: %s\n", reviewEvidenceScalar(proposal.Risk))
		}
		if proposal.AnchorBefore != "" {
			fmt.Fprintf(&b, "  anchor_before:\n%s\n", indentBlock(compactEditProposalPayloadForEvidence(proposal.AnchorBefore, 1600), "    "))
		}
		if proposal.ReplaceRange != "" {
			fmt.Fprintf(&b, "  replace_range: %s\n", reviewEvidenceScalar(proposal.ReplaceRange))
		}
		if proposal.ExactSearch != "" {
			fmt.Fprintf(&b, "  exact_search:\n%s\n", indentBlock(compactEditProposalPayloadForEvidence(proposal.ExactSearch, 1600), "    "))
		}
		if proposal.Replacement != "" {
			fmt.Fprintf(&b, "  replacement:\n%s\n", indentBlock(compactEditProposalPayloadForEvidence(proposal.Replacement, 1600), "    "))
		}
		if proposal.AfterExcerpt != "" && !omitAfterExcerpt {
			fmt.Fprintf(&b, "  after_excerpt:\n%s\n", indentBlock(compactEditProposalPayloadForEvidence(proposal.AfterExcerpt, 8000), "    "))
		}
		if proposal.PreviewFingerprint != "" {
			fmt.Fprintf(&b, "  preview_fingerprint: %s\n", reviewEvidenceScalar(proposal.PreviewFingerprint))
		}
		if proposal.ExpectedComplete != nil {
			if *proposal.ExpectedComplete {
				fmt.Fprintf(&b, "  expected_preview_status: complete\n")
			} else {
				fmt.Fprintf(&b, "  expected_preview_status: compacted_for_display\n")
				fmt.Fprintf(&b, "  expected_preview_note: use provided_diff, after_excerpt, and preview_fingerprint as the authoritative edit evidence\n")
			}
		}
		if proposal.ExpectedPreview != "" {
			fmt.Fprintf(&b, "  expected_preview:\n%s\n", indentBlock(compactEditProposalPayloadForEvidence(proposal.ExpectedPreview, 4000), "    "))
		} else if proposal.PreviewFingerprint != "" {
			fmt.Fprintf(&b, "  expected_preview: shared by preview_fingerprint\n")
		}
	}
	if len(proposals) > 16 {
		fmt.Fprintf(&b, "additional_proposals: %d\n", len(proposals)-16)
	}
	return strings.TrimSpace(b.String())
}

func reviewEvidenceScalar(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range text {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, "\\x%02X", r)
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func compactEditProposalPayloadForEvidence(text string, limit int) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return compactPromptSectionPreserveHeadTail(text, limit)
}

func limitEditProposalsForEvidence(proposals []EditProposal, limit int) []EditProposal {
	if limit <= 0 || len(proposals) <= limit {
		return proposals
	}
	return proposals[:limit]
}

func renderReviewRepairFindingsForEvidence(findings []ReviewFinding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("The proposed edit must address each item below, or the review must report the item as still unresolved.\n")
	for i, finding := range limitReviewFindings(findings, 12) {
		finding.Normalize()
		fmt.Fprintf(&b, "repair_%d:\n", i+1)
		fmt.Fprintf(&b, "  id: %s\n", reviewEvidenceScalar(valueOrDefault(finding.ID, fmt.Sprintf("RF-%03d", i+1))))
		fmt.Fprintf(&b, "  severity: %s\n", reviewEvidenceScalar(valueOrDefault(finding.Severity, reviewSeverityMedium)))
		fmt.Fprintf(&b, "  category: %s\n", reviewEvidenceScalar(valueOrDefault(finding.Category, "correctness")))
		if strings.TrimSpace(finding.Path) != "" {
			fmt.Fprintf(&b, "  path: %s\n", reviewEvidenceScalar(finding.Path))
		}
		if strings.TrimSpace(finding.Symbol) != "" {
			fmt.Fprintf(&b, "  symbol: %s\n", reviewEvidenceScalar(finding.Symbol))
		}
		if strings.TrimSpace(finding.Title) != "" {
			fmt.Fprintf(&b, "  title: %s\n", reviewEvidenceScalar(compactPromptSection(finding.Title, 220)))
		}
		if strings.TrimSpace(finding.Evidence) != "" {
			fmt.Fprintf(&b, "  evidence: %s\n", reviewEvidenceScalar(compactPromptSection(finding.Evidence, 320)))
		}
		if strings.TrimSpace(finding.RequiredFix) != "" {
			fmt.Fprintf(&b, "  required_fix: %s\n", reviewEvidenceScalar(compactPromptSection(finding.RequiredFix, 320)))
		}
	}
	if len(findings) > 12 {
		fmt.Fprintf(&b, "additional_repair_findings: %d\n", len(findings)-12)
	}
	return strings.TrimSpace(b.String())
}

func inferEditProposalOperation(preview EditPreview) string {
	text := strings.ToLower(strings.TrimSpace(preview.Operation + " " + preview.Title + " " + preview.Preview))
	switch {
	case strings.Contains(text, "delete file") || strings.Contains(text, "deleted file"):
		return "delete"
	case strings.Contains(text, "add file") || strings.Contains(text, "new file"):
		return "add"
	case strings.Contains(text, "rename") || strings.Contains(text, "move"):
		return "move"
	case strings.Contains(text, "apply patch"):
		return "patch"
	case strings.Contains(text, "write file"):
		return "write"
	default:
		return "modify"
	}
}

func editProposalRiskForOperation(operation string, pathCount int) string {
	operation = canonicalEditProposalOperationAlias(operation)
	switch {
	case operation == "delete_file" || operation == "move":
		return "destructive"
	case pathCount > 8:
		return "multi_file"
	case operation == "add_file" || operation == "patch" || operation == "modify" || operation == "write_file" || operation == "replace_in_file":
		return "previewed_change"
	default:
		return "unknown"
	}
}

func editProposalFromToolInput(args map[string]any) EditProposal {
	content := stringValue(args, "content")
	replacement := stringValue(args, "replacement")
	if replacement == "" {
		replacement = content
	}
	return EditProposal{
		File:         stringValue(args, "file"),
		Operation:    stringValue(args, "operation"),
		AnchorBefore: stringValue(args, "anchor_before"),
		ReplaceRange: stringValue(args, "replace_range"),
		ExactSearch:  stringValue(args, "exact_search"),
		Replacement:  replacement,
		Rationale:    stringValue(args, "rationale"),
		Risk:         stringValue(args, "risk"),
	}
}

type plannedEditProposal struct {
	Proposal     EditProposal
	Operation    string
	AbsolutePath string
	DisplayPath  string
	DisplayRoot  string
	Before       string
	After        string
	BeforeExists bool
	Count        int
}

func applyEditProposal(ctx context.Context, ws Workspace, proposal EditProposal, ownerNodeID string, all bool) (string, map[string]any, error) {
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	default:
	}
	planned, err := planEditProposal(ws, proposal, ownerNodeID, all)
	meta := editProposalToolMeta(proposal, planned, false)
	if err != nil {
		return "", meta, err
	}
	select {
	case <-ctx.Done():
		return "", meta, ctx.Err()
	default:
	}
	if _, err := ws.Hook(ctx, HookPreEdit, HookPayload{
		"path":               planned.DisplayPath,
		"absolute_path":      planned.AbsolutePath,
		"operation":          "apply_edit_proposal",
		"proposal_operation": planned.Operation,
		"reason":             editProposalReason(planned),
		"file_tags":          hookFileTags(planned.AbsolutePath),
		"owner_node_id":      ownerNodeID,
		"worktree_root":      planned.DisplayRoot,
	}); err != nil {
		return "", meta, err
	}
	preview := editProposalPreview(ws, planned)
	if err := ws.ReviewProposedEdit(ctx, preview); err != nil {
		return "", meta, err
	}
	if err := ws.ConfirmEdit(preview); err != nil {
		return "", meta, err
	}
	if err := ws.EnsureWrite(planned.AbsolutePath); err != nil {
		return "", meta, err
	}
	if err := ws.BeforeEditForRoot(editProposalReason(planned), planned.DisplayRoot); err != nil {
		return "", meta, err
	}
	switch planned.Operation {
	case "delete_file":
		ws.Progress("Deleting " + planned.DisplayPath + "...")
		if err := os.Remove(planned.AbsolutePath); err != nil {
			if editProposalMutationChangedOnDisk(planned) {
				meta = editProposalObservedMutationMeta(proposal, planned)
			}
			return "", meta, err
		}
		ws.Progress("Deleted " + planned.DisplayPath + ".")
	default:
		ws.Progress("Writing " + planned.DisplayPath + "...")
		if err := ensureParentDir(planned.AbsolutePath); err != nil {
			return "", meta, err
		}
		if err := os.WriteFile(planned.AbsolutePath, []byte(planned.After), 0o644); err != nil {
			if editProposalMutationChangedOnDisk(planned) {
				meta = editProposalObservedMutationMeta(proposal, planned)
			}
			return "", meta, err
		}
		ws.Progress("Saved " + planned.DisplayPath + ".")
	}
	if _, err := ws.Hook(ctx, HookPostEdit, HookPayload{
		"path":               planned.DisplayPath,
		"absolute_path":      planned.AbsolutePath,
		"operation":          "apply_edit_proposal",
		"proposal_operation": planned.Operation,
		"reason":             editProposalReason(planned),
		"file_tags":          hookFileTags(planned.AbsolutePath),
		"owner_node_id":      ownerNodeID,
		"worktree_root":      planned.DisplayRoot,
	}); err != nil {
		if editProposalMutationChangedOnDisk(planned) {
			meta = editProposalObservedMutationMeta(proposal, planned)
		}
		return "", meta, err
	}
	meta = editProposalToolMeta(proposal, planned, true)
	return joinNonEmpty(
		editProposalResultLine(planned),
		buildEditPreview(planned.DisplayPath, planned.Before, planned.After),
	), meta, nil
}

func planEditProposal(ws Workspace, proposal EditProposal, ownerNodeID string, all bool) (plannedEditProposal, error) {
	proposals := normalizeEditProposals([]EditProposal{proposal})
	if len(proposals) == 0 || strings.TrimSpace(proposals[0].File) == "" {
		return plannedEditProposal{}, fmt.Errorf("edit proposal file is required")
	}
	proposal = proposals[0]
	operation := normalizeEditProposalOperation(proposal.Operation, proposal)
	forLookup := operation != "add_file" && operation != "write_file"
	route, err := ws.ResolveEditPath(proposal.File, ownerNodeID, forLookup)
	if err != nil {
		return plannedEditProposal{}, err
	}
	path := route.AbsolutePath
	displayRoot := firstNonBlankString(route.DisplayRoot, route.WorktreeRoot, ws.Root)
	displayPath := route.DisplayPath()
	if err := ws.CheckEditBoundary(path); err != nil {
		return plannedEditProposal{}, err
	}
	beforeBytes, readErr := os.ReadFile(path)
	before := string(beforeBytes)
	if readErr != nil && !os.IsNotExist(readErr) {
		return plannedEditProposal{}, readErr
	}
	planned := plannedEditProposal{
		Proposal:     proposal,
		Operation:    operation,
		AbsolutePath: path,
		DisplayPath:  displayPath,
		DisplayRoot:  displayRoot,
		Before:       before,
		BeforeExists: readErr == nil,
	}
	switch operation {
	case "replace_in_file":
		if readErr != nil {
			return plannedEditProposal{}, readErr
		}
		if proposal.ExactSearch == "" {
			return plannedEditProposal{}, fmt.Errorf("edit proposal exact_search is required for replace_in_file")
		}
		count := strings.Count(before, proposal.ExactSearch)
		if count == 0 {
			return plannedEditProposal{}, fmt.Errorf("%w: exact_search text not found in %s", ErrEditTargetMismatch, displayPath)
		}
		if !all && count > 1 {
			return plannedEditProposal{}, fmt.Errorf("exact_search appears %d times; set all=true or provide a narrower exact_search", count)
		}
		if all {
			planned.After = strings.ReplaceAll(before, proposal.ExactSearch, proposal.Replacement)
		} else {
			planned.After = strings.Replace(before, proposal.ExactSearch, proposal.Replacement, 1)
		}
		planned.Count = count
		if suspiciousReplacePayload(path, proposal.ExactSearch, proposal.Replacement, before, planned.After) {
			return plannedEditProposal{}, fmt.Errorf("%w: edit proposal replacement looks like a malformed serialized payload; provide real replacement text", ErrInvalidEditPayload)
		}
	case "add_file":
		if readErr == nil {
			return plannedEditProposal{}, fmt.Errorf("cannot add file that already exists: %s", displayPath)
		}
		if proposal.Replacement == "" {
			return plannedEditProposal{}, fmt.Errorf("edit proposal content is required for add_file")
		}
		planned.After = proposal.Replacement
	case "write_file":
		if proposal.Replacement == "" {
			return plannedEditProposal{}, fmt.Errorf("edit proposal content is required for write_file")
		}
		planned.After = proposal.Replacement
		if suspiciousRewritePayload(path, before, planned.After) {
			return plannedEditProposal{}, fmt.Errorf("%w: edit proposal content looks like a malformed serialized payload; provide real file text", ErrInvalidEditPayload)
		}
	case "delete_file":
		if readErr != nil {
			return plannedEditProposal{}, readErr
		}
		planned.After = ""
	default:
		return plannedEditProposal{}, fmt.Errorf("unsupported edit proposal operation %q; use apply_patch for complex hunk-level fallback", proposal.Operation)
	}
	return planned, nil
}

func editProposalMutationCommitted(planned plannedEditProposal) bool {
	if strings.TrimSpace(planned.AbsolutePath) == "" {
		return false
	}
	afterExists := planned.Operation != "delete_file"
	if planned.BeforeExists == afterExists && planned.Before == planned.After {
		return false
	}
	if !afterExists {
		_, err := os.Stat(planned.AbsolutePath)
		return errors.Is(err, os.ErrNotExist)
	}
	data, err := os.ReadFile(planned.AbsolutePath)
	return err == nil && string(data) == planned.After
}

func editProposalMutationChangedOnDisk(planned plannedEditProposal) bool {
	if strings.TrimSpace(planned.AbsolutePath) == "" {
		return false
	}
	data, err := os.ReadFile(planned.AbsolutePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return planned.BeforeExists
		}
		return false
	}
	return !planned.BeforeExists || string(data) != planned.Before
}

func editProposalObservedMutationMeta(proposal EditProposal, planned plannedEditProposal) map[string]any {
	meta := editProposalToolMeta(proposal, planned, true)
	if !editProposalMutationCommitted(planned) {
		delete(meta, "unified_diff")
		meta["turn_diff_invalidated"] = true
		meta["unified_diff_unavailable_reason"] = "workspace changed but final contents did not match the planned edit after tool failure"
	}
	return meta
}

func normalizeEditProposalOperation(raw string, proposal EditProposal) string {
	operation := canonicalEditProposalOperationAlias(raw)
	switch operation {
	case "replace", "replace_in_file", "exact_replace", "search_replace":
		if strings.TrimSpace(proposal.ExactSearch) != "" {
			return "replace_in_file"
		}
		return "unsupported"
	case "", "modify":
		if strings.TrimSpace(proposal.ExactSearch) != "" {
			return "replace_in_file"
		}
		return "unsupported"
	case "write", "write_file", "overwrite":
		return "write_file"
	case "add", "add_file", "create", "create_file":
		return "add_file"
	case "delete", "delete_file", "remove":
		return "delete_file"
	default:
		return operation
	}
}

func canonicalEditProposalOperationAlias(raw string) string {
	operation := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(raw, "-", "_")))
	switch operation {
	case "write", "overwrite":
		return "write_file"
	case "add", "create", "create_file":
		return "add_file"
	case "delete", "remove":
		return "delete_file"
	default:
		return operation
	}
}

func editProposalPreview(ws Workspace, planned plannedEditProposal) EditPreview {
	proposal := planned.Proposal
	proposal.Operation = planned.Operation
	proposal.File = planned.DisplayPath
	expectedPreview := buildSelectionAwareEditPreview(ws, planned.DisplayPath, planned.Before, planned.After)
	proposal = bindEditProposalPreview(proposal, planned.Operation, planned.DisplayPath, expectedPreview)
	proposal.AfterExcerpt = buildSelectionAwareAfterExcerpt(ws, planned.DisplayPath, planned.Before, planned.After, 12000)
	return EditPreview{
		Title:     "Apply edit proposal to " + planned.DisplayPath,
		Preview:   expectedPreview,
		Paths:     []string{planned.DisplayPath},
		Operation: "apply_edit_proposal",
		Proposals: []EditProposal{proposal},
	}
}

func editProposalToolMeta(proposal EditProposal, planned plannedEditProposal, changed bool) map[string]any {
	path := strings.TrimSpace(proposal.File)
	if planned.DisplayPath != "" {
		path = planned.DisplayPath
	}
	paths := []string{}
	if path != "" {
		paths = []string{path}
	}
	meta := map[string]any{
		"path":                  path,
		"changed_paths":         normalizeTaskStateList(paths, 8),
		"changed_count":         len(paths),
		"proposal_operation":    firstNonBlankString(planned.Operation, normalizeEditProposalOperation(proposal.Operation, proposal)),
		"changed_workspace":     changed,
		"requires_verification": changed,
		"effect":                "edit",
	}
	if changed && strings.TrimSpace(planned.DisplayPath) != "" {
		if unifiedDiff := buildUnifiedDiff(planned.DisplayPath, planned.Before, planned.After); strings.TrimSpace(unifiedDiff) != "" {
			meta["unified_diff"] = unifiedDiff
		}
	}
	return meta
}

func editProposalReason(planned plannedEditProposal) string {
	return strings.TrimSpace("apply edit proposal " + planned.Operation + " " + planned.DisplayPath)
}

func editProposalResultLine(planned plannedEditProposal) string {
	switch planned.Operation {
	case "replace_in_file":
		return fmt.Sprintf("applied edit proposal to %s (%d replacement(s))", planned.DisplayPath, planned.Count)
	case "add_file":
		return "applied edit proposal to add " + planned.DisplayPath
	case "write_file":
		return "applied edit proposal to write " + planned.DisplayPath
	case "delete_file":
		return "applied edit proposal to delete " + planned.DisplayPath
	default:
		return "applied edit proposal to " + planned.DisplayPath
	}
}
