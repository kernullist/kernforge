package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceEnsureWriteRejectsNestedClaudeWorktreeOutsideActiveRoot(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, ".claude", "worktrees", "compassionate-goldberg", "completion.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ws := Workspace{
		BaseRoot: base,
		Root:     base,
	}

	err := ws.EnsureWrite(target)
	if err == nil {
		t.Fatalf("expected nested worktree edit to be rejected")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestWorkspaceEnsureWriteRejectsSymlinkTargetOutsideRoot(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("external\n"), 0o644); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	err := ws.EnsureWrite(link)
	if err == nil {
		t.Fatalf("expected symlink write outside root to be rejected")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestWorkspaceEnsureWriteAllowsInternalDotDotPrefixName(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "..cache", "main.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
	}
	if err := ws.EnsureWrite(target); err != nil {
		t.Fatalf("expected internal dot-dot-prefix path to be allowed, got %v", err)
	}
}

func TestPlanEditProposalUsesLookupRoutingForExistingFileOperations(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	for _, operation := range []string{"replace_in_file", "delete_file"} {
		t.Run(operation, func(t *testing.T) {
			var seen []EditRoutingRequest
			ws := Workspace{
				BaseRoot: root,
				Root:     root,
				ResolveEditTarget: func(req EditRoutingRequest) (EditRoutingResult, error) {
					seen = append(seen, req)
					return EditRoutingResult{
						AbsolutePath: filepath.Join(root, req.Path),
						DisplayRoot:  root,
						OwnerNodeID:  req.OwnerNodeID,
					}, nil
				},
			}
			proposal := EditProposal{
				File:      "main.go",
				Operation: operation,
			}
			if operation == "replace_in_file" {
				proposal.ExactSearch = "package main"
				proposal.Replacement = "package main"
			}
			if _, err := planEditProposal(ws, proposal, "", false); err != nil {
				t.Fatalf("planEditProposal: %v", err)
			}
			if len(seen) != 1 || !seen[0].ForLookup {
				t.Fatalf("expected lookup routing for %s, got %#v", operation, seen)
			}
		})
	}
}

func TestWorkspaceEnsureWriteAllowsActiveNestedClaudeWorktree(t *testing.T) {
	base := t.TempDir()
	activeRoot := filepath.Join(base, ".claude", "worktrees", "compassionate-goldberg")
	target := filepath.Join(activeRoot, "completion.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ws := Workspace{
		BaseRoot: base,
		Root:     activeRoot,
	}

	if err := ws.EnsureWrite(target); err != nil {
		t.Fatalf("expected active worktree edit to be allowed, got %v", err)
	}
}

func TestWorkspaceEnsureWriteRejectsSiblingClaudeWorktree(t *testing.T) {
	base := t.TempDir()
	activeRoot := filepath.Join(base, ".claude", "worktrees", "active")
	siblingTarget := filepath.Join(base, ".claude", "worktrees", "other", "completion.go")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll active root: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(siblingTarget), 0o755); err != nil {
		t.Fatalf("MkdirAll sibling target: %v", err)
	}
	ws := Workspace{
		BaseRoot: base,
		Root:     activeRoot,
	}

	err := ws.EnsureWrite(siblingTarget)
	if err == nil {
		t.Fatalf("expected sibling worktree edit to be rejected")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestSameFilePathUsesOSFileIdentity(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.txt")
	second := filepath.Join(root, "second.txt")
	if err := os.WriteFile(first, []byte("same\n"), 0o644); err != nil {
		t.Fatalf("WriteFile first: %v", err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if !sameFilePath(first, second) {
		t.Fatalf("expected hard-linked paths to resolve to the same file")
	}
}

func TestReplaceInFileReturnsEditTargetMismatchWhenSearchTextMissing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "completion.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewReplaceInFileTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "completion.go",
		"search":  "missing",
		"replace": "found",
	})
	if err == nil {
		t.Fatalf("expected replace failure")
	}
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
}

func TestEditToolDescriptionsBiasTowardEditProposal(t *testing.T) {
	ws := Workspace{}

	proposalDesc := NewApplyEditProposalTool(ws).Definition().Description
	if !strings.Contains(proposalDesc, "structured edit proposal") || !strings.Contains(proposalDesc, "Prefer this") {
		t.Fatalf("expected apply_edit_proposal description to emphasize first-class proposal usage, got %q", proposalDesc)
	}
	if !isEditTool("apply_edit_proposal") || inferToolExecutionEffect("apply_edit_proposal") != "edit" {
		t.Fatalf("expected apply_edit_proposal to be classified as an edit tool")
	}

	patchDesc := NewApplyPatchTool(ws).Definition().Description
	if !strings.Contains(patchDesc, "expert fallback") {
		t.Fatalf("expected apply_patch description to emphasize fallback usage, got %q", patchDesc)
	}
	for _, want := range []string{"payloads narrow", "first independent hunk", "reread"} {
		if !strings.Contains(patchDesc, want) {
			t.Fatalf("expected apply_patch description to contain narrow payload guidance %q, got %q", want, patchDesc)
		}
	}

	replaceDesc := NewReplaceInFileTool(ws).Definition().Description
	if !strings.Contains(replaceDesc, "only for very small single-location substitutions") {
		t.Fatalf("expected replace_in_file description to emphasize narrow usage, got %q", replaceDesc)
	}
}

func TestApplyEditProposalRequiresReviewBeforePreviewAndWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	before := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	events := []string{}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		ReviewEdit: func(ctx context.Context, preview EditPreview) error {
			events = append(events, "review")
			if len(preview.Proposals) != 1 {
				t.Fatalf("expected structured proposal in preview, got %#v", preview.Proposals)
			}
			proposal := preview.Proposals[0]
			if proposal.File != "main.go" || proposal.Operation != "replace_in_file" || proposal.ExactSearch == "" || proposal.PreviewFingerprint == "" {
				t.Fatalf("unexpected proposal: %#v", proposal)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read before approval: %v", err)
			}
			if string(data) != before {
				t.Fatalf("proposal review must run before write, got %q", string(data))
			}
			return nil
		},
		PreviewEdit: func(preview EditPreview) (bool, error) {
			events = append(events, "preview")
			if !strings.Contains(preview.Preview, "Preview for main.go") {
				t.Fatalf("expected edit preview, got %q", preview.Preview)
			}
			return true, nil
		},
	}
	tool := NewApplyEditProposalTool(ws)

	out, err := tool.Execute(context.Background(), map[string]any{
		"file":         "main.go",
		"operation":    "replace_in_file",
		"exact_search": "func main() {}",
		"replacement":  "func main() { println(\"ok\") }",
		"rationale":    "exercise proposal flow",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Join(events, ",") != "review,preview" {
		t.Fatalf("expected review before preview/write, got %#v", events)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after proposal: %v", err)
	}
	if !strings.Contains(string(data), "println(\"ok\")") {
		t.Fatalf("expected proposal write, got %q", string(data))
	}
	if !strings.Contains(out, "applied edit proposal") {
		t.Fatalf("expected proposal output, got %q", out)
	}
}

func TestApplyEditProposalPreservesExactSearchReplacementWhitespace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	before := "package main\n\nfunc main()\n{\n\tprintln(\"old\")\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		ReviewEdit: func(ctx context.Context, preview EditPreview) error {
			if len(preview.Proposals) != 1 {
				t.Fatalf("expected structured proposal in preview, got %#v", preview.Proposals)
			}
			proposal := preview.Proposals[0]
			if proposal.ExactSearch != "\tprintln(\"old\")\n}\n" {
				t.Fatalf("exact_search whitespace changed before review: %q", proposal.ExactSearch)
			}
			if proposal.Replacement != "\tprintln(\"new\")\n}\n" {
				t.Fatalf("replacement whitespace changed before review: %q", proposal.Replacement)
			}
			return nil
		},
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	}
	tool := NewApplyEditProposalTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"file":         "main.go",
		"operation":    "replace_in_file",
		"exact_search": "\tprintln(\"old\")\n}\n",
		"replacement":  "\tprintln(\"new\")\n}\n",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after proposal: %v", err)
	}
	want := "package main\n\nfunc main()\n{\n\tprintln(\"new\")\n}\n"
	if string(data) != want {
		t.Fatalf("replacement should preserve exact whitespace, got %q want %q", string(data), want)
	}
}

func TestApplyEditProposalRejectsMalformedInputWithoutPanic(t *testing.T) {
	tool := NewApplyEditProposalTool(Workspace{})
	if _, err := tool.ExecuteDetailed(context.Background(), "not an object"); err == nil {
		t.Fatalf("expected malformed input error")
	}
}

func TestReadFileRejectsMalformedInputWithoutPanic(t *testing.T) {
	tool := NewReadFileTool(Workspace{})
	if _, err := tool.ExecuteDetailed(context.Background(), "not an object"); err == nil {
		t.Fatalf("expected malformed input error")
	}
}

func TestApplyPatchRejectsMalformedInputWithoutPanic(t *testing.T) {
	tool := NewApplyPatchTool(Workspace{})
	if _, err := tool.ExecuteDetailed(context.Background(), "not an object"); err == nil {
		t.Fatalf("expected malformed input error")
	}
}

func TestApplyPatchAcceptsRawFreeformPayloadLikeCodex(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, Workspace, string) (ToolExecutionResult, error)
	}{
		{
			name: "direct tool",
			run: func(ctx context.Context, ws Workspace, patch string) (ToolExecutionResult, error) {
				return NewApplyPatchTool(ws).ExecuteDetailed(ctx, map[string]any{
					"raw": patch,
				})
			},
		},
		{
			name: "registry",
			run: func(ctx context.Context, ws Workspace, patch string) (ToolExecutionResult, error) {
				return NewToolRegistry(NewApplyPatchTool(ws)).ExecuteDetailed(ctx, "apply_patch", mustJSON(map[string]any{
					"raw": patch,
				}))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "main.go")
			if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			patch := "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+\n+func freeformPatch() {}\n*** End Patch\n"

			result, err := tc.run(context.Background(), Workspace{BaseRoot: root, Root: root}, patch)
			if err != nil {
				t.Fatalf("expected raw freeform patch payload to apply, got %v", err)
			}
			if changed, _ := result.Meta["changed_workspace"].(bool); !changed {
				t.Fatalf("expected changed_workspace metadata, got %#v", result.Meta)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if !strings.Contains(string(data), "func freeformPatch() {}") {
				t.Fatalf("expected raw freeform patch to update file, got %q", string(data))
			}
		})
	}
}

func TestListFilesRejectsMalformedInputWithoutPanic(t *testing.T) {
	tool := NewListFilesTool(Workspace{})
	for _, input := range []any{nil, "not an object", []any{"path"}} {
		if _, err := tool.ExecuteDetailed(context.Background(), input); err == nil {
			t.Fatalf("expected malformed input error for %#v", input)
		}
	}
}

func TestRegisteredToolsRejectMalformedInputWithoutPanic(t *testing.T) {
	root := t.TempDir()
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		UpdatePlan: func(items []PlanItem) {
		},
	}
	registry := buildRegistry(ws, nil)
	for name, tool := range registry.tools {
		for _, input := range []any{nil, "not an object", []any{"path"}} {
			t.Run(name, func(t *testing.T) {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("tool panicked for %#v: %v", input, recovered)
					}
				}()
				var err error
				if detailed, ok := tool.(detailedTool); ok {
					_, err = detailed.ExecuteDetailed(context.Background(), input)
				} else {
					_, err = tool.Execute(context.Background(), input)
				}
				if err == nil {
					t.Fatalf("expected malformed input error for %#v", input)
				}
			})
		}
	}
}

func TestApplyPatchAppendsContextFreeUpdateHunkLikeCodex(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc existing() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})
	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n+func inserted() {}\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("expected context-free hunk to append like Codex, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "package main\n\nfunc existing() {}\nfunc inserted() {}\n" {
		t.Fatalf("unexpected patched content: %q", string(data))
	}
}

func TestApplyPatchAcceptsFirstUpdateHunkWithoutContextMarkerLikeCodex(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc existing() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n package main\n+import \"fmt\"\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("expected first update hunk without @@ to apply, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "package main\nimport \"fmt\"\n\nfunc existing() {}\n" {
		t.Fatalf("unexpected patched content: %q", string(data))
	}
}

func TestApplyPatchAcceptsHeredocEnvelopeLikeCodex(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "<<'EOF'\n*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\nEOF\n",
	})
	if err != nil {
		t.Fatalf("expected heredoc envelope to apply, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "package main\nfunc main() {}\n" {
		t.Fatalf("unexpected patched content: %q", string(data))
	}
}

func TestApplyPatchRejectsEnvironmentIDWhenUnavailableLikeCodex(t *testing.T) {
	root := t.TempDir()
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Environment ID: remote\n*** Add File: hello.txt\n+hello\n*** End Patch\n",
	})
	if !errors.Is(err, ErrInvalidPatchFormat) {
		t.Fatalf("expected invalid patch format for unavailable environment selection, got %v", err)
	}
	if !strings.Contains(err.Error(), "environment selection is unavailable") {
		t.Fatalf("expected unavailable environment message, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "hello.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("environment rejected patch must not write file, stat err=%v", statErr)
	}
}

func TestApplyEditProposalChecksCancellationBeforePlanning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := applyEditProposal(ctx, Workspace{}, EditProposal{
		File:        "main.go",
		Operation:   "write_file",
		Replacement: "package main\n",
	}, "", false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation before planning, got %v", err)
	}
}

func TestApplyPatchRequiresPreviewApprovalBeforeWriting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	previewCalls := 0
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			previewCalls++
			if !strings.Contains(preview.Title, "Apply patch") {
				t.Fatalf("unexpected preview title: %q", preview.Title)
			}
			if !strings.Contains(preview.Preview, "Preview for main.go") {
				t.Fatalf("expected patch preview contents, got %q", preview.Preview)
			}
			return false, nil
		},
	}
	tool := NewApplyPatchTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
	})
	if !errors.Is(err, ErrEditCanceled) {
		t.Fatalf("expected ErrEditCanceled, got %v", err)
	}
	if previewCalls != 1 {
		t.Fatalf("expected one preview confirmation, got %d", previewCalls)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("expected file to remain unchanged after preview rejection, got %q", string(data))
	}
}

func TestApplyPatchPlanningDoesNotPromptWritePermissionBeforeReview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	promptCalls := 0
	perms := NewPermissionManager(ModeDefault, func(question string) (bool, error) {
		promptCalls++
		return true, nil
	})
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Perms:    perms,
		ReviewEdit: func(ctx context.Context, preview EditPreview) error {
			return errors.New("review blocked")
		},
		PreviewEdit: func(preview EditPreview) (bool, error) {
			t.Fatalf("preview must not run after review rejection")
			return true, nil
		},
	}
	tool := NewApplyPatchTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
	})
	if err == nil || !strings.Contains(err.Error(), "review blocked") {
		t.Fatalf("expected review block, got %v", err)
	}
	if promptCalls != 0 {
		t.Fatalf("planning/review path must not request write permission, got %d prompt(s)", promptCalls)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("expected file to remain unchanged, got %q", string(data))
	}
}

func TestApplyPatchPreservesCRLFLineEndings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\r\n\r\nfunc main() {}\r\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n func main() {}\n+func helper() {}\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "func main() {}\r\nfunc helper() {}\r\n") {
		t.Fatalf("expected inserted line to use CRLF, got %q", got)
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("expected no bare LF line endings, got %q", got)
	}
}

func TestApplyPatchPreservesCROnlyLineEndings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.txt")
	original := "one\rtwo\r"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.txt\n@@\n one\n-two\n+TWO\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	if got != "one\rTWO\r" {
		t.Fatalf("expected CR-only endings to be preserved, got %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("expected no LF line endings, got %q", got)
	}
}

func TestApplyPatchHunkHeaderTargetsDuplicateContext(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := strings.Join([]string{
		"func first()",
		"{",
		"    value := 0",
		"    println(value)",
		"}",
		"",
		"func second()",
		"{",
		"    value := 0",
		"    println(value)",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@ -9,2 +9,3 @@\n     value := 0\n+    value++\n     println(value)\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	if strings.Contains(got, "func first()\n{\n    value := 0\n    value++") {
		t.Fatalf("first duplicate block must remain unchanged, got %q", got)
	}
	if !strings.Contains(got, "func second()\n{\n    value := 0\n    value++\n    println(value)") {
		t.Fatalf("expected hunk header to target second duplicate block, got %q", got)
	}
}

func TestApplyPatchHunkHeaderAccountsForPriorLineDelta(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.txt")
	original := "drop\nsame\nsame\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.txt\n@@ -1,1 +0,0 @@\n-drop\n@@ -2,1 +1,1 @@\n-same\n+target\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "target\nsame\n" {
		t.Fatalf("expected second hunk to use adjusted original line number, got %q", string(data))
	}
}

func TestApplyPatchRejectsOutOfOrderHunksBeforeCursor(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.txt")
	original := "a\nb\nc\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			t.Fatalf("preview must not run for out-of-order patch")
			return true, nil
		},
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.txt\n@@ -3,1 +3,1 @@\n-c\n+C\n@@ -1,1 +1,1 @@\n-a\n+A\n*** End Patch\n",
	})
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch for out-of-order hunk, got %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != original {
		t.Fatalf("expected rejected patch to leave file unchanged, got %q", string(data))
	}
}

func TestApplyPatchMismatchReportsExpectedAndCurrentContext(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.txt")
	original := "alpha\nbeta\ncurrent\nomega\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			t.Fatalf("preview must not run for mismatched patch")
			return true, nil
		},
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.txt\n@@\n beta\n-old\n omega\n*** End Patch\n",
	})
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected ErrEditTargetMismatch, got %v", err)
	}
	got := err.Error()
	for _, want := range []string{
		"expected context not found",
		"expected first line: \"beta\"",
		"expected last line: \"omega\"",
		"nearest current context",
		"2: \"beta\"",
		"3: \"current\"",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected mismatch diagnostic to contain %q, got %q", want, got)
		}
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != original {
		t.Fatalf("expected rejected patch to leave file unchanged, got %q", string(data))
	}
}

func TestApplyEditProposalPlanningDoesNotPromptWritePermissionBeforeReview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	promptCalls := 0
	perms := NewPermissionManager(ModeDefault, func(question string) (bool, error) {
		promptCalls++
		return true, nil
	})
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Perms:    perms,
		ReviewEdit: func(ctx context.Context, preview EditPreview) error {
			return errors.New("review blocked")
		},
		PreviewEdit: func(preview EditPreview) (bool, error) {
			t.Fatalf("preview must not run after review rejection")
			return true, nil
		},
	}
	tool := NewApplyEditProposalTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"file":         "main.go",
		"operation":    "replace_in_file",
		"exact_search": "package main\n",
		"replacement":  "package main\n\nfunc main() {}\n",
	})
	if err == nil || !strings.Contains(err.Error(), "review blocked") {
		t.Fatalf("expected review block, got %v", err)
	}
	if promptCalls != 0 {
		t.Fatalf("planning/review path must not request write permission, got %d prompt(s)", promptCalls)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("expected file to remain unchanged, got %q", string(data))
	}
}

func TestApplyEditProposalRejectsExplicitReplaceWithoutExactSearch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyEditProposalTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			t.Fatalf("preview must not run for malformed explicit replace proposal")
			return true, nil
		},
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"file":        "main.go",
		"operation":   "replace_in_file",
		"replacement": "package main\n\nfunc main() {}\n",
	})
	if err == nil {
		t.Fatalf("expected explicit replace without exact_search to fail")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != original {
		t.Fatalf("expected malformed proposal to leave file unchanged, got %q", string(data))
	}
}

func TestApplyPatchMoveMetadataIncludesSourceAndDestination(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old.go")
	if err := os.WriteFile(oldPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: old.go\n*** Move to: new.go\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	changedPaths, _ := result.Meta["changed_paths"].([]string)
	if !slices.Contains(changedPaths, "old.go") || !slices.Contains(changedPaths, "new.go") {
		t.Fatalf("expected changed_paths to include source and destination, got %#v", result.Meta["changed_paths"])
	}
	if count, _ := result.Meta["changed_count"].(int); count != len(changedPaths) {
		t.Fatalf("expected changed_count to match changed_paths length, got %v vs %d", result.Meta["changed_count"], len(changedPaths))
	}
	if moveCount, _ := result.Meta["move_count"].(int); moveCount != 1 {
		t.Fatalf("expected move_count=1, got %#v", result.Meta["move_count"])
	}
	if _, err := os.Stat(filepath.Join(root, "new.go")); err != nil {
		t.Fatalf("expected destination file: %v", err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected source to be removed, stat err=%v", err)
	}
}

func TestApplyPatchWorkdirResolvesMoveDestinationFromEffectiveRoot(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	oldPath := filepath.Join(subdir, "old.go")
	if err := os.WriteFile(oldPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"workdir": "subdir",
		"patch":   "*** Begin Patch\n*** Update File: old.go\n*** Move to: renamed.go\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	changedPaths, _ := result.Meta["changed_paths"].([]string)
	if !slices.Contains(changedPaths, "old.go") || !slices.Contains(changedPaths, "renamed.go") {
		t.Fatalf("expected workdir-relative changed paths, got %#v", changedPaths)
	}
	if _, err := os.Stat(filepath.Join(subdir, "renamed.go")); err != nil {
		t.Fatalf("expected destination under workdir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "renamed.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination must not be resolved at workspace root, stat err=%v", err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected source to be removed from workdir, stat err=%v", err)
	}
}

func TestApplyPatchWorkdirCanCreateMissingDirectoryForAdd(t *testing.T) {
	root := t.TempDir()
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"workdir": "created",
		"patch":   "*** Begin Patch\n*** Add File: nested/main.go\n+package main\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if changedWorkspace, _ := result.Meta["changed_workspace"].(bool); !changedWorkspace {
		t.Fatalf("expected missing workdir add to change workspace, meta=%#v", result.Meta)
	}
	target := filepath.Join(root, "created", "nested", "main.go")
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected add file under missing workdir: %v", err)
	}
	if string(content) != "package main\n" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestApplyPatchUpdateAppendsTrailingNewlineLikeCodex(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "no_newline.txt")
	if err := os.WriteFile(target, []byte("no newline at end"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: no_newline.txt",
			"@@",
			"-no newline at end",
			"+first line",
			"+second line",
			"*** End Patch",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "first line\nsecond line\n" {
		t.Fatalf("expected Codex-style trailing newline, got %q", string(content))
	}
}

func TestApplyPatchAcceptsEndOfFileSentinelLikeCodexGrammar(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.txt")
	if err := os.WriteFile(target, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: main.txt",
			"@@",
			" one",
			"+two",
			"*** End of File",
			"*** End Patch",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("expected EOF sentinel accepted, got %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "one\ntwo\n" {
		t.Fatalf("unexpected patched content: %q", string(content))
	}
}

func TestApplyPatchAddOverwritesExistingRegularFileLikeCodex(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "duplicate.txt")
	if err := os.WriteFile(target, []byte("old content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var preview EditPreview
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(next EditPreview) (bool, error) {
			preview = next
			return true, nil
		},
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Add File: duplicate.txt\n+new content\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "new content\n" {
		t.Fatalf("expected Codex-style add overwrite, got %q", string(content))
	}
	if !strings.Contains(preview.Preview, "-   1 | old content") || !strings.Contains(preview.Preview, "+   1 | new content") {
		t.Fatalf("expected overwrite preview to include old and new content, got %q", preview.Preview)
	}
}

func TestApplyPatchRejectsEmptyPatchLikeCodex(t *testing.T) {
	root := t.TempDir()
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** End Patch\n",
	})
	if err == nil {
		t.Fatalf("expected empty patch to be rejected")
	}
	if !errors.Is(err, ErrInvalidPatchFormat) {
		t.Fatalf("expected ErrInvalidPatchFormat, got %v", err)
	}
	if !strings.Contains(err.Error(), "No files were modified") {
		t.Fatalf("expected Codex-style empty patch error, got %v", err)
	}
}

func TestApplyPatchRejectsEmptyUpdateHunkLikeCodex(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "foo.txt")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: foo.txt\n*** End Patch\n",
	})
	if err == nil {
		t.Fatalf("expected empty update hunk to be rejected")
	}
	if !errors.Is(err, ErrInvalidPatchFormat) {
		t.Fatalf("expected ErrInvalidPatchFormat, got %v", err)
	}
	if !strings.Contains(err.Error(), "patch_format_empty_update") {
		t.Fatalf("expected empty update format error, got %v", err)
	}
}

func TestApplyPatchRejectsDoubleAtEmptyHunkAsInvalidFormat(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "foo.txt")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: foo.txt\n@@\n@@\n old\n+new\n*** End Patch\n",
	})
	if err == nil {
		t.Fatalf("expected double @@ empty hunk to be rejected")
	}
	if !errors.Is(err, ErrInvalidPatchFormat) {
		t.Fatalf("expected ErrInvalidPatchFormat, got %v", err)
	}
	if errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("empty hunk must not be classified as edit target mismatch: %v", err)
	}
	if !strings.Contains(err.Error(), "patch_format_empty_hunk") {
		t.Fatalf("expected empty hunk format error, got %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "old\n" {
		t.Fatalf("invalid patch must not update file, got %q", string(data))
	}
}

func TestApplyPatchErrorMetadataDoesNotClaimWorkspaceChanged(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		ReviewEdit: func(ctx context.Context, preview EditPreview) error {
			return errors.New("review rejected")
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
	})
	if err == nil {
		t.Fatalf("expected review rejection")
	}
	if changed, _ := result.Meta["changed_workspace"].(bool); changed {
		t.Fatalf("failed patch must not report changed_workspace=true")
	}
	if requires, _ := result.Meta["requires_verification"].(bool); requires {
		t.Fatalf("failed patch must not report requires_verification=true")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != "package main\n" {
		t.Fatalf("file must remain unchanged, got %q", string(data))
	}
}

func TestApplyPatchPartialFailureMetadataReportsOnlyChangedPaths(t *testing.T) {
	root := t.TempDir()
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Add File: dir\n+not a directory\n*** Add File: dir/child.txt\n+child\n*** End Patch\n",
	})
	if err == nil {
		t.Fatalf("expected partial patch failure")
	}
	data, readErr := os.ReadFile(filepath.Join(root, "dir"))
	if readErr != nil {
		t.Fatalf("expected first patch operation to have written dir file: %v", readErr)
	}
	if string(data) != "not a directory\n" {
		t.Fatalf("unexpected first file content: %q", string(data))
	}
	if _, statErr := os.Stat(filepath.Join(root, "dir", "child.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("second patch operation should not create child, stat err=%v", statErr)
	}
	if changed, _ := result.Meta["changed_workspace"].(bool); !changed {
		t.Fatalf("partial patch failure must report changed_workspace=true")
	}
	changedPaths, _ := result.Meta["changed_paths"].([]string)
	if !slices.Equal(changedPaths, []string{"dir"}) {
		t.Fatalf("expected exact changed paths [dir], got %#v", result.Meta["changed_paths"])
	}
}

func TestApplyPatchAddRejectsBrokenSymlink(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "missing.txt")
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Add File: link.txt\n+hello\n*** End Patch\n",
	})
	if err == nil {
		t.Fatalf("expected add-file patch over broken symlink to be rejected")
	}
	if !strings.Contains(err.Error(), "cannot add file that already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(external); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("external symlink target must not be created, stat err=%v", statErr)
	}
}

func TestApplyPatchDeleteRejectsExternalSymlinkBeforeReview(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("external\n"), 0o644); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	reviewCalls := 0
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		ReviewEdit: func(ctx context.Context, preview EditPreview) error {
			reviewCalls++
			return nil
		},
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Delete File: link.txt\n*** End Patch\n",
	})
	if !errors.Is(err, ErrEditTargetMismatch) {
		t.Fatalf("expected external symlink delete to be rejected with ErrEditTargetMismatch, got %v", err)
	}
	if reviewCalls != 0 {
		t.Fatalf("expected rejection before review, got %d review calls", reviewCalls)
	}
	data, readErr := os.ReadFile(external)
	if readErr != nil {
		t.Fatalf("ReadFile external: %v", readErr)
	}
	if string(data) != "external\n" {
		t.Fatalf("external target must remain unchanged, got %q", string(data))
	}
}

func TestWorkspaceConfirmEditTreatsPromptCancelAsEditCanceled(t *testing.T) {
	ws := Workspace{
		PreviewEdit: func(preview EditPreview) (bool, error) {
			if !strings.Contains(preview.Preview, "Preview for main.go") {
				t.Fatalf("expected preview contents, got %q", preview.Preview)
			}
			return false, ErrPromptCanceled
		},
	}

	err := ws.ConfirmEdit(EditPreview{
		Title:   "Apply patch",
		Preview: "Preview for main.go\n+ change",
	})
	if !errors.Is(err, ErrEditCanceled) {
		t.Fatalf("expected prompt cancel to be normalized to ErrEditCanceled, got %v", err)
	}
}

func TestWorkspaceConfirmEditTreatsPreviewEOFAsEditCanceled(t *testing.T) {
	ws := Workspace{
		PreviewEdit: func(preview EditPreview) (bool, error) {
			if !strings.Contains(preview.Preview, "Preview for main.go") {
				t.Fatalf("expected preview contents, got %q", preview.Preview)
			}
			return false, io.EOF
		},
	}
	err := ws.ConfirmEdit(EditPreview{
		Title:   "Apply patch",
		Preview: "Preview for main.go",
	})
	if !errors.Is(err, ErrEditCanceled) {
		t.Fatalf("expected preview EOF to be normalized to ErrEditCanceled, got %v", err)
	}
}

func TestApplyPatchAcceptsBareBlankContextLinesInHunk(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.cpp")
	original := "int value()\n{\n\n    return 0;\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.cpp\n@@\n int value()\n {\n\n-    return 0;\n+    return 1;\n }\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("expected bare blank hunk line to be treated as blank context, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(data); got != "int value()\n{\n\n    return 1;\n}\n" {
		t.Fatalf("unexpected patched content: %q", got)
	}
}

func TestApplyPatchRejectsFencedPatchWithProseLikeCodex(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "Here is the patch:\n```patch\n*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n```\nDone.",
	})
	if err == nil {
		t.Fatalf("expected fenced patch with prose to be rejected")
	}
	if !errors.Is(err, ErrInvalidPatchFormat) {
		t.Fatalf("expected invalid patch format, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "package main\n" {
		t.Fatalf("direct apply_patch payload with prose must not update file, got %q", string(data))
	}
}

func TestApplyPatchNormalizesBackslashPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: src\\main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("expected backslash patch path to be normalized, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "func main() {}") {
		t.Fatalf("expected normalized path patch to update file, got %q", string(data))
	}
}

func TestApplyPatchInvalidFormatUsesReasonCode(t *testing.T) {
	_, err := NewApplyPatchTool(Workspace{BaseRoot: t.TempDir(), Root: t.TempDir()}).Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n*** End Patch\n",
	})
	if !errors.Is(err, ErrInvalidPatchFormat) {
		t.Fatalf("expected invalid patch format, got %v", err)
	}
	if !strings.Contains(err.Error(), "patch_format_empty_update") {
		t.Fatalf("expected reason-coded patch error, got %v", err)
	}
}

func TestInvalidPatchFormatGuidanceChangesOnRepeatedSignature(t *testing.T) {
	first := invalidPatchFormatGuidance(false, ErrInvalidPatchFormat)
	repeated := invalidPatchFormatGuidance(true, ErrInvalidPatchFormat)
	if !strings.Contains(first, "Retry using the tool again") {
		t.Fatalf("expected first guidance to allow a format retry, got %q", first)
	}
	if !strings.Contains(repeated, "Do not retry the same patch text again") ||
		!strings.Contains(repeated, "First read the exact target file again") {
		t.Fatalf("expected repeated guidance to force fresh read, got %q", repeated)
	}

	argsA := "{\"patch\":\"```patch\\n*** Begin Patch\\n*** End Patch\\n```\"}"
	argsB := `{"patch":"*** Begin Patch\n*** End Patch"}`
	if applyPatchFormatFailureSignature(argsA) == applyPatchFormatFailureSignature(argsB) {
		t.Fatalf("expected direct apply_patch wrappers/prose to remain distinct from raw patch text")
	}
}

func TestServiceInstallReviewInfersSecurityMode(t *testing.T) {
	mode := inferReviewMode(
		"SampleWorker 서비스 설치/시작 과정에 버그를 찾고 수정해",
		[]string{"SampleApp/SampleApp/SampleWorkerManager.cpp", "SampleApp/SampleWorker/SampleUpdManager.cpp"},
		reviewTargetChange,
		nil,
	)
	if mode != reviewModeSecurityHardening {
		t.Fatalf("expected service install/start review to use security hardening mode, got %q", mode)
	}
}

func TestWriteFileReviewBlocksBeforePreviewAndWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	reviewCalls := 0
	previewCalls := 0
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		ReviewEdit: func(ctx context.Context, preview EditPreview) error {
			_ = ctx
			reviewCalls++
			if !strings.Contains(preview.Preview, "func main") {
				t.Fatalf("expected proposed diff in review preview, got %q", preview.Preview)
			}
			return errors.New("review blocked")
		},
		PreviewEdit: func(preview EditPreview) (bool, error) {
			_ = preview
			previewCalls++
			return true, nil
		},
	}
	tool := NewWriteFileTool(ws)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "main.go",
		"content": "package main\n\nfunc main() {}\n",
	})
	if err == nil || !strings.Contains(err.Error(), "review blocked") {
		t.Fatalf("expected review block, got %v", err)
	}
	if reviewCalls != 1 {
		t.Fatalf("expected one review call, got %d", reviewCalls)
	}
	if previewCalls != 0 {
		t.Fatalf("expected preview to be skipped after review block, got %d", previewCalls)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("expected file to remain unchanged, got %q", string(data))
	}
}

func TestWriteFilePreviewRejectDoesNotPromptOrCreateParent(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "newdir")
	promptCalls := 0
	perms := NewPermissionManager(ModeDefault, func(question string) (bool, error) {
		promptCalls++
		return true, nil
	})
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Perms:    perms,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return false, nil
		},
	}
	tool := NewWriteFileTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    filepath.Join("newdir", "main.go"),
		"content": "package main\n",
	})
	if !errors.Is(err, ErrEditCanceled) {
		t.Fatalf("expected ErrEditCanceled, got %v", err)
	}
	if promptCalls != 0 {
		t.Fatalf("preview rejection must not request write permission, got %d prompt(s)", promptCalls)
	}
	if _, statErr := os.Stat(targetDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("preview rejection must not create parent directory, stat err=%v", statErr)
	}
}

func TestReplaceInFilePreviewRejectDoesNotPromptWritePermission(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	promptCalls := 0
	perms := NewPermissionManager(ModeDefault, func(question string) (bool, error) {
		promptCalls++
		return true, nil
	})
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Perms:    perms,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return false, nil
		},
	}
	tool := NewReplaceInFileTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "main.go",
		"search":  "package main\n",
		"replace": "package main\n\nfunc main() {}\n",
	})
	if !errors.Is(err, ErrEditCanceled) {
		t.Fatalf("expected ErrEditCanceled, got %v", err)
	}
	if promptCalls != 0 {
		t.Fatalf("preview rejection must not request write permission, got %d prompt(s)", promptCalls)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("expected file to remain unchanged, got %q", string(data))
	}
}

func TestToolRegistryExecuteWrapsMalformedJSONAsInvalidToolArguments(t *testing.T) {
	ws := Workspace{}
	registry := NewToolRegistry(NewWriteFileTool(ws))

	_, err := registry.Execute(context.Background(), "write_file", `{"path":"main.go","content":"package main`)
	if !errors.Is(err, ErrInvalidToolArgumentsJSON) {
		t.Fatalf("expected ErrInvalidToolArgumentsJSON, got %v", err)
	}
}

func TestRunShellRejectsWorkspaceMutatingCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "Set-Content test.txt 'hello'",
	})
	if err == nil {
		t.Fatalf("expected mutating shell command to be rejected")
	}
	if !strings.Contains(err.Error(), "manual workspace file writes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShellRejectsInlinePowerShellFileWrites(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	cases := map[string]string{
		"semicolon_set_content": "Get-Content input.txt; Set-Content output.txt 'changed'",
		"dotnet_write_all_text": "$content = 'changed'; [System.IO.File]::WriteAllText('output.txt', $content)",
		"invoke_web_out_file":   "Invoke-WebRequest https://example.test/file.txt -OutFile output.txt",
		"pipeline_out_file":     "Get-Content input.txt | Out-File output.txt",
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]any{
				"command": command,
			})
			if err == nil {
				t.Fatalf("expected inline mutating shell command to be rejected")
			}
			if !strings.Contains(err.Error(), "manual workspace file writes") {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "output.txt")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("expected output.txt to remain absent, stat err=%v", statErr)
			}
		})
	}
}

func TestRunShellRejectsPowerShellStopParsingForms(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	cases := map[string]string{
		"direct":                          "git log --% HEAD --output=codex_poc.txt",
		"powershell_shell":                `powershell -NoProfile -Command "git log --% HEAD --output=codex_poc.txt"`,
		"powershell_slash_command":        `powershell -NoProfile /Command "git log --% HEAD --output=codex_poc.txt"`,
		"powershell_inline_command":       `powershell -NoProfile -Command:"git log --% HEAD --output=codex_poc.txt"`,
		"powershell_inline_slash_command": `powershell -NoProfile /Command:"git log --% HEAD --output=codex_poc.txt"`,
		"pwsh_shell":                      `pwsh -Command "git log --% HEAD --output=codex_poc.txt"`,
		"cmd_native_bridge":               `cmd /c echo --% > codex_poc.txt`,
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]any{
				"command": command,
			})
			if err == nil {
				t.Fatalf("expected PowerShell stop-parsing form to be rejected")
			}
			if !strings.Contains(err.Error(), "unsupported shell syntax") {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "codex_poc.txt")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("expected codex_poc.txt to remain absent, stat err=%v", statErr)
			}
		})
	}
}

func TestRunShellRejectsPowerShellEncodedCommandForms(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	cases := map[string]string{
		"encoded_long":  "powershell -NoProfile -EncodedCommand UwBlAHQALQBDAG8AbgB0AGUAbgB0ACAAYwBvAGQAZQB4AF8AcABvAGMALgB0AHgAdAAgAHgA",
		"encoded_short": `pwsh -enc UwBlAHQALQBDAG8AbgB0AGUAbgB0ACAAYwBvAGQAZQB4AF8AcABvAGMALgB0AHgAdAAgAHgA`,
		"encoded_colon": `powershell /EncodedCommand:UwBlAHQALQBDAG8AbgB0AGUAbgB0ACAAYwBvAGQAZQB4AF8AcABvAGMALgB0AHgAdAAgAHgA`,
		"encoded_alias": `powershell -e UwBlAHQALQBDAG8AbgB0AGUAbgB0ACAAYwBvAGQAZQB4AF8AcABvAGMALgB0AHgAdAAgAHgA`,
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]any{
				"command": command,
			})
			if err == nil {
				t.Fatalf("expected PowerShell EncodedCommand form to be rejected")
			}
			if !strings.Contains(err.Error(), "unsupported shell syntax") {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "codex_poc.txt")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("expected codex_poc.txt to remain absent, stat err=%v", statErr)
			}
		})
	}
}

func TestShellInvocationPreservesPwshAndDisablesProfiles(t *testing.T) {
	name, args := shellInvocation("pwsh", "Write-Output ok")
	if name != "pwsh" {
		t.Fatalf("expected pwsh shell to be preserved, got %q", name)
	}
	if len(args) != 3 || args[0] != "-NoProfile" || args[1] != "-Command" || !strings.Contains(args[2], "Write-Output ok") {
		t.Fatalf("expected pwsh invocation to use -NoProfile -Command, got %#v", args)
	}
}

func TestRunShellRejectsRipgrepExternalCommandOptions(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	cases := map[string]string{
		"pre_split":           "rg --pre ./hook pattern .",
		"pre_equals":          "rg --pre=./hook pattern .",
		"hostname_bin_split":  "rg --hostname-bin ./hostname pattern .",
		"hostname_bin_equals": "rg --hostname-bin=./hostname pattern .",
		"search_zip":          "rg --search-zip pattern .",
		"short_zip":           "rg -z pattern .",
		"quoted_pre":          `rg "--pre" docs`,
		"powershell_pre":      `powershell -NoProfile -Command "rg --pre ./hook pattern ."`,
		"bash_lc_pre":         `bash -lc "rg --pre=./hook pattern ."`,
		"bash_escaped_pre_eq": `bash -lc "rg --pre\=./hook pattern ."`,
		"bash_escaped_pre":    `bash -lc "rg --\pre=./hook pattern ."`,
		"bash_escaped_host":   `bash -lc "rg --hostname\-bin=./hostname pattern ."`,
		"bash_escaped_zip":    `bash -lc "rg -\z pattern ."`,
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]any{
				"command": command,
			})
			if err == nil {
				t.Fatalf("expected unsafe ripgrep command to be rejected")
			}
			if !strings.Contains(err.Error(), "read-only-looking tool form") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunShellRejectsUnsafeGitReadOnlyForms(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	cases := map[string]string{
		"global_c_split":    "git -C . status",
		"global_c_inline":   "git -C. status",
		"global_config":     "git -c core.pager=cat log -n 1",
		"global_config_in":  "git -ccore.pager=cat status",
		"global_paginate":   "git --paginate log -1",
		"global_p":          "git -p log -1",
		"config_env":        "git --config-env=core.pager=PAGER show HEAD",
		"git_dir":           "git --git-dir=.evil-git diff HEAD~1..HEAD",
		"work_tree":         "git --work-tree=. status",
		"exec_path":         "git --exec-path=.git/helpers show HEAD",
		"namespace":         "git --namespace=attacker show HEAD",
		"super_prefix":      "git --super-prefix=attacker/ show HEAD",
		"diff_output":       "git diff --output codex_poc.txt",
		"log_output_equals": "git log --output=codex_poc.txt -n 1",
		"show_output":       "git show --output=codex_poc.txt HEAD",
		"diff_ext":          "git diff --ext-diff HEAD",
		"log_textconv":      "git log --textconv -1",
		"log_exec":          "git log --exec=helper -1",
		"cat_file_filters":  "git cat-file --filters HEAD:a.txt",
		"branch_create":     "git branch feature/test",
		"branch_delete":     "git branch -d feature/test",
		"powershell_git":    `powershell -NoProfile -Command "git --paginate log -1"`,
		"bash_lc_git":       `bash -lc "git --git-dir=.evil-git diff HEAD~1..HEAD"`,
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]any{
				"command": command,
			})
			if err == nil {
				t.Fatalf("expected unsafe git command to be rejected")
			}
			if !strings.Contains(err.Error(), "read-only-looking tool form") {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "codex_poc.txt")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("expected codex_poc.txt to remain absent, stat err=%v", statErr)
			}
		})
	}
}

func TestRunShellRejectsPOSIXShellExpansionForms(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	cases := map[string]string{
		"glob_option_injection": `bash -lc "base64 -i input.txt -* cc06_out"`,
		"glob_path":             `bash -lc "ls *.rs"`,
		"brace_expansion":       `bash -lc "echo -{o,}"`,
		"bracket_glob":          `sh -lc "cat [ab].txt"`,
		"zsh_question_glob":     `zsh -lc "cat file?.txt"`,
		"tilde_expansion":       `bash -lc "echo ~/loot"`,
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]any{
				"command": command,
			})
			if err == nil {
				t.Fatalf("expected shell expansion command to be rejected")
			}
			if !strings.Contains(err.Error(), "read-only-looking tool form") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunShellRejectsManualFileWriteEvenWithScopedWritePaths(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command":                "Set-Content allowed.txt 'hello'",
		"allow_workspace_writes": true,
		"write_paths":            []string{"allowed.txt"},
	})
	if err == nil {
		t.Fatalf("expected manual shell file write to be rejected even with scoped write paths")
	}
	if !strings.Contains(err.Error(), "manual workspace file writes") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "allowed.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected allowed.txt to remain absent, stat err=%v", statErr)
	}
}

func TestRunShellRejectsNestedShellOutputRedirection(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	cases := map[string]string{
		"bash_lc_redirect":       `bash -lc "echo hello > codex_poc.txt"`,
		"bash_lc_quoted_target":  `bash -lc "echo hello > 'codex poc.txt'"`,
		"bash_lc_heredoc_target": "bash -lc \"cat <<'EOF' > codex_poc.txt\nhello\nEOF\"",
		"powershell_redirect":    `powershell -NoProfile -Command "Write-Output hello > codex_poc.txt"`,
		"cmd_redirect":           `cmd /c "echo hello > codex_poc.txt"`,
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]any{
				"command": command,
			})
			if err == nil {
				t.Fatalf("expected nested shell redirection to be rejected")
			}
			if !strings.Contains(err.Error(), "manual workspace file writes") {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "codex_poc.txt")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("expected codex_poc.txt to remain absent, stat err=%v", statErr)
			}
		})
	}
}

func TestRunShellAllowsReadOnlyCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello",
	}); err != nil {
		t.Fatalf("expected read-only shell command to succeed, got %v", err)
	}
}

func TestRunShellPermissionRequestHookCanApproveWithoutPrompt(t *testing.T) {
	root := t.TempDir()
	var permissionPayload HookPayload
	promptCalls := 0
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		Perms: NewPermissionManager(ModeDefault, func(string) (bool, error) {
			promptCalls++
			return false, nil
		}),
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			if event == HookPermissionRequest {
				permissionPayload = payload
				return HookVerdict{
					Allow:              true,
					PermissionDecision: "allow",
				}, nil
			}
			return HookVerdict{Allow: true}, nil
		},
	})

	ctx := contextWithMCPTurnMetadata(context.Background(), map[string]any{
		"turn_id":         "turn-123",
		"cwd":             root,
		"model":           "gpt-test",
		"permission_mode": string(ModeDefault),
	})
	out, err := tool.Execute(ctx, map[string]any{
		"command": "echo hook-approved",
	})
	if err != nil {
		t.Fatalf("expected PermissionRequest allow to approve shell command, got %v", err)
	}
	if !strings.Contains(out, "hook-approved") {
		t.Fatalf("expected command output, got %q", out)
	}
	if promptCalls != 0 {
		t.Fatalf("PermissionRequest allow should bypass user prompt, got %d prompt(s)", promptCalls)
	}
	if got := toolMetaString(permissionPayload, "hook_event_name"); got != string(HookPermissionRequest) {
		t.Fatalf("expected PermissionRequest payload, got %#v", permissionPayload)
	}
	if got := toolMetaString(permissionPayload, "tool_name"); got != "run_shell" {
		t.Fatalf("expected run_shell tool name, got %#v", permissionPayload)
	}
	if got := toolMetaString(permissionPayload, "turn_id"); got != "turn-123" {
		t.Fatalf("expected turn_id from tool context, got %#v", permissionPayload)
	}
	if got := toolMetaString(permissionPayload, "model"); got != "gpt-test" {
		t.Fatalf("expected model from tool context, got %#v", permissionPayload)
	}
	if got := toolMetaString(permissionPayload, "permission_mode"); got != string(ModeDefault) {
		t.Fatalf("expected permission mode from tool context, got %#v", permissionPayload)
	}
	input, ok := permissionPayload["tool_input"].(HookPayload)
	if !ok || toolMetaString(input, "command") != "echo hook-approved" {
		t.Fatalf("expected command in tool_input, got %#v", permissionPayload["tool_input"])
	}
}

func TestRunShellPermissionRequestHookCanDenyBeforePrompt(t *testing.T) {
	root := t.TempDir()
	promptCalls := 0
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		Perms: NewPermissionManager(ModeDefault, func(string) (bool, error) {
			promptCalls++
			return true, nil
		}),
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			_ = payload
			if event == HookPermissionRequest {
				return HookVerdict{
					Allow:              false,
					PermissionDecision: "deny",
					PermissionMessage:  "blocked by PermissionRequest",
				}, nil
			}
			return HookVerdict{Allow: true}, nil
		},
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo denied",
	})
	if err == nil || !strings.Contains(err.Error(), "blocked by PermissionRequest") {
		t.Fatalf("expected hook denial, got %v", err)
	}
	if promptCalls != 0 {
		t.Fatalf("PermissionRequest deny should bypass user prompt, got %d prompt(s)", promptCalls)
	}
}

func TestRunShellRememberedApprovalSkipsPermissionRequestHook(t *testing.T) {
	root := t.TempDir()
	promptCalls := 0
	permissionHookCalls := 0
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		Perms: NewPermissionManager(ModeDefault, func(string) (bool, error) {
			promptCalls++
			return true, nil
		}),
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			_ = payload
			if event == HookPermissionRequest {
				permissionHookCalls++
			}
			return HookVerdict{Allow: true}, nil
		},
	})

	for i := 0; i < 2; i++ {
		if _, err := tool.Execute(context.Background(), map[string]any{
			"command": "echo remembered",
		}); err != nil {
			t.Fatalf("shell run %d: %v", i+1, err)
		}
	}
	if promptCalls != 1 {
		t.Fatalf("expected first shell prompt to be remembered, got %d prompt(s)", promptCalls)
	}
	if permissionHookCalls != 1 {
		t.Fatalf("remembered shell approval should skip PermissionRequest hooks, got %d call(s)", permissionHookCalls)
	}
}

func TestRunShellRememberedShellApprovalDoesNotSkipVerificationConfirmation(t *testing.T) {
	root := t.TempDir()
	shellPromptCalls := 0
	verificationPromptCalls := 0
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		Perms: NewPermissionManager(ModeDefault, func(string) (bool, error) {
			shellPromptCalls++
			return true, nil
		}),
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			verificationPromptCalls++
			if len(plan.Steps) != 1 || !strings.Contains(plan.Steps[0].Command, "go test") {
				t.Fatalf("unexpected verification plan: %#v", plan)
			}
			return false, nil
		},
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			_ = event
			_ = payload
			return HookVerdict{Allow: true}, nil
		},
	})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo remembered",
	}); err != nil {
		t.Fatalf("initial shell approval: %v", err)
	}
	text, err := tool.Execute(context.Background(), map[string]any{
		"command": "go test ./...",
	})
	if err != nil {
		t.Fatalf("declined verification command should be a non-error skip, got %v", err)
	}
	if !strings.Contains(text, "skipped") {
		t.Fatalf("expected skipped verification output, got %q", text)
	}
	if shellPromptCalls != 1 {
		t.Fatalf("expected only the initial shell prompt, got %d", shellPromptCalls)
	}
	if verificationPromptCalls != 1 {
		t.Fatalf("remembered shell approval must not bypass verification confirmation, got %d prompt(s)", verificationPromptCalls)
	}
}

func TestRunShellPreToolUseRewriteExecutesUpdatedCommand(t *testing.T) {
	root := t.TempDir()
	var observed string
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			if event != HookPreToolUse {
				return HookVerdict{Allow: true}, nil
			}
			observed = stringsValueFromAny(payload["command"])
			return HookVerdict{
				Allow:        true,
				UpdatedInput: HookPayload{"command": "echo rewritten"},
			}, nil
		},
	})
	input := map[string]any{"command": "echo original"}

	result, err := tool.ExecuteDetailed(context.Background(), input)
	if err != nil {
		t.Fatalf("run shell with hook rewrite: %v", err)
	}
	if observed != "echo original" {
		t.Fatalf("expected hook to observe original command, got %q", observed)
	}
	if !strings.Contains(result.DisplayText, "rewritten") || strings.Contains(result.DisplayText, "original") {
		t.Fatalf("expected rewritten command output only, got %q", result.DisplayText)
	}
	if got := toolMetaString(result.Meta, "command"); got != "echo rewritten" {
		t.Fatalf("expected rewritten command metadata, got %#v", result.Meta)
	}
	if got := toolMetaString(result.Meta, "original_command"); got != "echo original" {
		t.Fatalf("expected original command metadata, got %#v", result.Meta)
	}
	if rewritten, _ := result.Meta["hook_rewritten"].(bool); !rewritten {
		t.Fatalf("expected hook_rewritten metadata, got %#v", result.Meta)
	}
}

func TestRunShellPreToolUseRewriteRevalidatesUpdatedCommand(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "hook-write.txt")
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			_ = payload
			if event != HookPreToolUse {
				return HookVerdict{Allow: true}, nil
			}
			return HookVerdict{
				Allow:        true,
				UpdatedInput: HookPayload{"command": "echo nope > hook-write.txt"},
			}, nil
		},
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo original",
	})
	if err == nil {
		t.Fatalf("expected rewritten workspace write to be rejected")
	}
	if !strings.Contains(err.Error(), "manual workspace file writes") {
		t.Fatalf("expected workspace write guard error, got %v", err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rewritten unsafe command should not create target, stat err=%v", statErr)
	}
}

func TestApplyPatchPreToolUseRewriteAppliesUpdatedPatch(t *testing.T) {
	root := t.TempDir()
	originalPatch := "*** Begin Patch\n*** Add File: original.txt\n+original\n*** End Patch\n"
	rewrittenPatch := "*** Begin Patch\n*** Add File: rewritten.txt\n+rewritten\n*** End Patch\n"
	var observed string
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			if event != HookPreToolUse {
				return HookVerdict{Allow: true}, nil
			}
			observed = stringsValueFromAny(payload["command"])
			return HookVerdict{
				Allow:        true,
				UpdatedInput: HookPayload{"command": rewrittenPatch},
			}, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": originalPatch,
	})
	if err != nil {
		t.Fatalf("apply patch with hook rewrite: %v", err)
	}
	if observed != strings.TrimSpace(originalPatch) {
		t.Fatalf("expected hook to observe original patch, got %q", observed)
	}
	if !strings.Contains(result.DisplayText, "rewritten.txt") {
		t.Fatalf("expected rewritten patch output, got %q", result.DisplayText)
	}
	if _, err := os.Stat(filepath.Join(root, "original.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original patch target should not exist, stat err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "rewritten.txt"))
	if err != nil {
		t.Fatalf("ReadFile rewritten target: %v", err)
	}
	if strings.TrimSpace(string(data)) != "rewritten" {
		t.Fatalf("unexpected rewritten file content: %q", data)
	}
}

func TestRunShellWorkdirExecutesFromRequestedDirectoryLikeCodex(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	command := `basename "$PWD"`
	if runtime.GOOS == "windows" {
		command = `$PWD.Path | Split-Path -Leaf`
	}
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"command": command,
		"workdir": "subdir",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !strings.Contains(result.DisplayText, "subdir") {
		t.Fatalf("expected command to run from subdir, got %q", result.DisplayText)
	}
	if got := toolMetaString(result.Meta, "work_dir"); !sameFilePath(got, subdir) {
		t.Fatalf("expected work_dir meta to resolve subdir, got %#v", result.Meta)
	}
}

func TestRunShellRejectsWorkdirOutsideActiveRootLikeCodexSandbox(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello",
		"workdir": "..",
	})
	if err == nil {
		t.Fatalf("expected outside workdir to be rejected")
	}
	if !strings.Contains(err.Error(), "outside the active workspace root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShellGuidesGitStatusToDedicatedTool(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	out, err := tool.Execute(context.Background(), map[string]any{
		"command": "git status --short SampleApp/SampleWorker/PathConverter.cpp",
	})
	if err == nil {
		t.Fatalf("expected run_shell git status to be rejected")
	}
	if !strings.Contains(out, "git_status") || !strings.Contains(err.Error(), "dedicated workspace tool") {
		t.Fatalf("expected dedicated tool guidance, out=%q err=%v", out, err)
	}
}

func TestRunShellGuidesGitDiffToDedicatedTool(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	out, err := tool.Execute(context.Background(), map[string]any{
		"command": "git diff -- SampleApp/SampleWorker/PathConverter.cpp",
	})
	if err == nil {
		t.Fatalf("expected run_shell git diff to be rejected")
	}
	if !strings.Contains(out, "git_diff") || !strings.Contains(err.Error(), "dedicated workspace tool") {
		t.Fatalf("expected dedicated tool guidance, out=%q err=%v", out, err)
	}
}

func TestRunShellGuidesGetContentToReadFile(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	out, err := tool.Execute(context.Background(), map[string]any{
		"command": `Get-Content -Path "SampleApp/SampleWorker/PathConverter.cpp" -TotalCount 10`,
	})
	if err == nil {
		t.Fatalf("expected run_shell Get-Content to be rejected")
	}
	if !strings.Contains(out, "read_file") || !strings.Contains(err.Error(), "dedicated workspace tool") {
		t.Fatalf("expected read_file guidance, out=%q err=%v", out, err)
	}
}

func TestRunShellReturnsPromptlyOnContextCancel(t *testing.T) {
	root := t.TempDir()
	command := "sleep 10; echo done"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 10; Write-Output done"
	}
	tool := NewRunShellTool(Workspace{
		BaseRoot:     root,
		Root:         root,
		Shell:        defaultShell(),
		ShellTimeout: 30 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := tool.ExecuteDetailed(ctx, map[string]any{"command": command})
		done <- err
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
		if elapsed := time.Since(start); elapsed > 4*time.Second {
			t.Fatalf("expected prompt cancellation, took %s", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("run_shell did not return promptly after cancellation")
	}
}

func TestRunShellAllowsExternalInstallCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	if _, err := tool.Execute(context.Background(), map[string]any{
		"command": "go install example.com/tool@latest",
	}); err == nil {
		t.Fatalf("expected external install command to reach the shell instead of the workspace-write guard")
	} else if strings.Contains(err.Error(), "run_shell cannot modify workspace files") {
		t.Fatalf("external install command should not be blocked as a workspace write: %v", err)
	}
}

func TestRunShellRejectsWorkspaceMutatingDependencyCommands(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "npm install react",
	})
	if err == nil {
		t.Fatalf("expected workspace-mutating dependency command to be rejected")
	}
	if !strings.Contains(err.Error(), "run_shell cannot modify workspace files") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShellRejectsSymlinkWorkspaceWriteBeforeExternalTargetMutation(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"command": "Set-Content -Path link.txt -Value changed",
	})
	if err == nil {
		t.Fatalf("expected shell write through symlink to be rejected before execution")
	}
	if !strings.Contains(err.Error(), "run_shell cannot perform manual workspace file writes") {
		t.Fatalf("unexpected error: %v", err)
	}
	data, readErr := os.ReadFile(external)
	if readErr != nil {
		t.Fatalf("ReadFile external: %v", readErr)
	}
	if string(data) != "original\n" {
		t.Fatalf("external symlink target must remain unchanged, got %q", string(data))
	}
}

func TestRunShellVerificationCommandReportsWorkspaceMutation(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}
	bin := t.TempDir()
	var fakeGo string
	if runtime.GOOS == "windows" {
		fakeGo = filepath.Join(bin, "go.bat")
		if err := os.WriteFile(fakeGo, []byte("@echo off\r\necho package main> source.go\r\necho func changed() {}>> source.go\r\necho ok\r\n"), 0o755); err != nil {
			t.Fatalf("WriteFile fake go: %v", err)
		}
	} else {
		fakeGo = filepath.Join(bin, "go")
		if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf 'package main\\nfunc changed() {}\\n' > source.go\necho ok\n"), 0o755); err != nil {
			t.Fatalf("WriteFile fake go: %v", err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tool := NewRunShellTool(Workspace{
		BaseRoot:     root,
		Root:         root,
		ShellTimeout: 10 * time.Second,
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"command": "go test ./...",
	})
	if err == nil {
		t.Fatalf("expected verification command workspace mutation to be reported")
	}
	if !strings.Contains(err.Error(), "verification command modified workspace") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunShellVerificationCommandAllowsBuildArtifacts(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	var fakeGo string
	if runtime.GOOS == "windows" {
		fakeGo = filepath.Join(bin, "go.bat")
		if err := os.WriteFile(fakeGo, []byte("@echo off\r\nmkdir x64\\Debug\\SampleWorker.tlog 2>nul\r\necho touched> x64\\Debug\\SampleWorker.tlog\\unsuccessfulbuild\r\necho ok\r\n"), 0o755); err != nil {
			t.Fatalf("WriteFile fake go: %v", err)
		}
	} else {
		fakeGo = filepath.Join(bin, "go")
		if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nmkdir -p x64/Debug/SampleWorker.tlog\nprintf touched > x64/Debug/SampleWorker.tlog/unsuccessfulbuild\necho ok\n"), 0o755); err != nil {
			t.Fatalf("WriteFile fake go: %v", err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tool := NewRunShellTool(Workspace{
		BaseRoot:     root,
		Root:         root,
		ShellTimeout: 10 * time.Second,
	})

	text, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"command": "go test ./...",
	})
	if err != nil {
		t.Fatalf("expected build artifact mutation to be tolerated, got err=%v text=%s", err, text.DisplayText)
	}
	if !strings.Contains(text.DisplayText, "ok") {
		t.Fatalf("expected command output, got %q", text.DisplayText)
	}
}

func TestRunShellVerificationCommandRejectsExternalSymlinkWorkspace(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("external\n"), 0o644); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	tool := NewRunShellTool(Workspace{
		BaseRoot:     root,
		Root:         root,
		ShellTimeout: 10 * time.Second,
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"command": "go test ./...",
	})
	if err == nil {
		t.Fatalf("expected verification command to be rejected before execution")
	}
	if !strings.Contains(err.Error(), "symlinks that resolve outside the active root") {
		t.Fatalf("unexpected error: %v", err)
	}
	data, readErr := os.ReadFile(external)
	if readErr != nil {
		t.Fatalf("ReadFile external: %v", readErr)
	}
	if string(data) != "external\n" {
		t.Fatalf("external target must remain unchanged, got %q", string(data))
	}
}

func TestRunShellSchemaDoesNotExposeWorkspaceWriteBypass(t *testing.T) {
	tool := NewRunShellTool(Workspace{})
	def := tool.Definition()
	schema, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected run_shell properties schema")
	}
	for _, field := range []string{"allow_workspace_writes", "write_paths"} {
		if _, ok := schema[field]; ok {
			t.Fatalf("run_shell schema must not expose %s", field)
		}
	}
}

func TestRunShellRejectsWorkspaceWriteEvenWithDeclaredPaths(t *testing.T) {
	root := t.TempDir()
	original := "package main\nfunc main(){println(\"hello\")}\n"
	if err := os.WriteFile(filepath.Join(root, "allowed.go"), []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile allowed.go: %v", err)
	}
	prepareCalls := 0
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PrepareEdit: func(reason string) error {
			prepareCalls++
			return nil
		},
	})

	command := "gofmt -w allowed.go"
	_, err := tool.Execute(context.Background(), map[string]any{
		"command":                command,
		"allow_workspace_writes": true,
		"write_paths":            []string{"allowed.go"},
	})
	if err == nil {
		t.Fatalf("expected workspace write command to be rejected")
	}
	if !strings.Contains(err.Error(), "run_shell cannot modify workspace files") {
		t.Fatalf("unexpected error: %v", err)
	}
	if prepareCalls != 0 {
		t.Fatalf("expected prepare edit not to run, got %d", prepareCalls)
	}
	data, err := os.ReadFile(filepath.Join(root, "allowed.go"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("expected allowed file to remain unchanged, got %q", string(data))
	}
}

func TestRunShellRejectsWorkspaceWriteBeforePathScopeChecks(t *testing.T) {
	root := t.TempDir()
	original := "package main\nfunc main(){println(\"oops\")}\n"
	if err := os.WriteFile(filepath.Join(root, "blocked.go"), []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile blocked.go: %v", err)
	}
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	command := "gofmt -w blocked.go"
	_, err := tool.Execute(context.Background(), map[string]any{
		"command":                command,
		"allow_workspace_writes": true,
		"write_paths":            []string{"allowed.go"},
	})
	if err == nil {
		t.Fatalf("expected workspace write command to be rejected")
	}
	if !strings.Contains(err.Error(), "run_shell cannot modify workspace files") {
		t.Fatalf("unexpected error: %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(root, "blocked.go"))
	if readErr != nil {
		t.Fatalf("ReadFile blocked.go: %v", readErr)
	}
	if string(data) != original {
		t.Fatalf("expected blocked.go to remain unchanged, got %q", string(data))
	}
}

func TestRunShellRejectsWorkspaceWriteBeforeEditableOwnershipChecks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	repoRoot := t.TempDir()
	ctx := context.Background()

	if _, err := runGitCommand(ctx, repoRoot, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.email", "kernforge-test@example.com"); err != nil {
		t.Fatalf("git config user.email: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "config", "user.name", "Kernforge Test"); err != nil {
		t.Fatalf("git config user.name: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "telemetry"), 0o755); err != nil {
		t.Fatalf("MkdirAll telemetry: %v", err)
	}
	original := "package main\nfunc main(){println(\"oops\")}\n"
	if err := os.WriteFile(filepath.Join(repoRoot, "main.go"), []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "telemetry", "provider.man"), []byte("<manifest/>\n"), 0o644); err != nil {
		t.Fatalf("WriteFile provider.man: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "add", "main.go", "telemetry/provider.man"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runGitCommand(ctx, repoRoot, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	cfg := DefaultConfig(repoRoot)
	cfg.WorktreeIsolation.Enabled = boolPtr(true)
	cfg.WorktreeIsolation.RootDir = filepath.Join(t.TempDir(), "managed")
	session := NewSession(repoRoot, "test", "test-model", "", "default")
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:                     "plan-01",
				Title:                  "Update telemetry assets",
				Kind:                   "edit",
				Status:                 "ready",
				EditableSpecialist:     "telemetry-analyst",
				EditableOwnershipPaths: []string{"telemetry/**", "*.man"},
				LastUpdated:            time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}
	rt := &runtimeState{
		cfg:     cfg,
		session: session,
		workspace: Workspace{
			BaseRoot: repoRoot,
			Root:     repoRoot,
		},
	}
	rt.syncWorkspaceFromSession()
	manager := newWorktreeManager(cfg)
	t.Cleanup(func() {
		for _, lease := range rt.session.SpecialistWorktrees {
			_ = manager.Remove(context.Background(), repoRoot, SessionWorktree{
				Root:    lease.Root,
				Branch:  lease.Branch,
				Managed: true,
			})
		}
	})

	tool := NewRunShellTool(rt.workspace)
	command := "gofmt -w main.go"
	_, err := tool.Execute(context.Background(), map[string]any{
		"command":                command,
		"allow_workspace_writes": true,
		"write_paths":            []string{"main.go"},
		"owner_node_id":          "plan-01",
	})
	if err == nil {
		t.Fatalf("expected workspace write command to be rejected")
	}
	if !strings.Contains(err.Error(), "run_shell cannot modify workspace files") {
		t.Fatalf("unexpected error: %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(repoRoot, "main.go"))
	if readErr != nil {
		t.Fatalf("ReadFile main.go: %v", readErr)
	}
	if string(data) != original {
		t.Fatalf("expected main.go to remain unchanged, got %q", string(data))
	}
}

func TestUserChangeIsolationDetectsSameSizeRestoredMtimeChange(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed.txt")
	protected := filepath.Join(root, "protected.txt")
	if err := os.WriteFile(allowed, []byte("allowed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile allowed: %v", err)
	}
	original := []byte("alpha-1234\n")
	if err := os.WriteFile(protected, original, 0o644); err != nil {
		t.Fatalf("WriteFile protected: %v", err)
	}
	info, err := os.Stat(protected)
	if err != nil {
		t.Fatalf("Stat protected: %v", err)
	}
	before, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	replacement := []byte("bravo-5678\n")
	if len(replacement) != len(original) {
		t.Fatalf("test setup bug: replacement length differs")
	}
	if err := os.WriteFile(protected, replacement, 0o644); err != nil {
		t.Fatalf("rewrite protected: %v", err)
	}
	if err := os.Chtimes(protected, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("restore mtime: %v", err)
	}
	current, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot current: %v", err)
	}
	conflicts := detectUserChangeConflicts(root, before, current, []string{"protected.txt"}, nil)
	if !slices.Contains(conflicts, "protected.txt") {
		t.Fatalf("expected same-size restored-mtime protected write to be detected")
	}
}

func TestAllowedScopeMatchingIsCaseSensitiveOffWindows(t *testing.T) {
	if workspacePathsAreCaseInsensitiveByDefault() {
		t.Skip("default workspace path matching is intentionally case-insensitive on this platform")
	}
	if pathMatchesAnyAllowedScope(filepath.Join("Allowed", "file.txt"), []string{"allowed"}) {
		t.Fatalf("expected case-distinct scope not to match on case-sensitive platforms")
	}
}

func TestAllowedScopeMatchingRejectsSiblingPrefixNames(t *testing.T) {
	scopes := []string{"allowed"}
	if pathMatchesAnyAllowedScope(filepath.Join("allowed2.txt"), scopes) {
		t.Fatalf("allowed scope must not match sibling file with shared prefix")
	}
	if pathMatchesAnyAllowedScope(filepath.Join("allowed_extra", "file.txt"), scopes) {
		t.Fatalf("allowed scope must not match sibling directory with shared prefix")
	}
	if !pathMatchesAnyAllowedScope(filepath.Join("allowed", "file.txt"), scopes) {
		t.Fatalf("allowed scope should match child path")
	}
}

func TestNormalizeAllowedWriteScopesDropsOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	sibling := filepath.Join(parent, "sibling")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll root: %v", err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("MkdirAll sibling: %v", err)
	}
	scopes := normalizeAllowedWriteScopes(root, []string{
		"inside",
		filepath.Join("..", "sibling"),
		sibling,
	})
	if !slices.Contains(scopes, "inside") {
		t.Fatalf("expected inside scope to remain, got %#v", scopes)
	}
	for _, scope := range scopes {
		if strings.HasPrefix(scope, "..") || filepath.IsAbs(scope) || strings.Contains(strings.ToLower(scope), "sibling") {
			t.Fatalf("outside-root scope must not be normalized as allowed: %#v", scopes)
		}
	}
	if pathMatchesAnyAllowedScope(filepath.Join("..", "sibling", "file.txt"), scopes) {
		t.Fatalf("outside-root path must not match allowed scopes %#v", scopes)
	}
}

func TestUserChangeIsolationDetectsDirectoryCreateAndDelete(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "anchor.txt"), []byte("anchor\n"), 0o644); err != nil {
		t.Fatalf("WriteFile anchor: %v", err)
	}
	beforeCreate, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot before create: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatalf("Mkdir empty: %v", err)
	}
	afterCreate, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot after create: %v", err)
	}
	createConflicts := detectUserChangeConflicts(root, beforeCreate, afterCreate, []string{"empty"}, nil)
	if !slices.Contains(createConflicts, "empty") {
		t.Fatalf("expected directory creation conflict, got %#v", createConflicts)
	}

	beforeDelete := afterCreate
	if err := os.Remove(filepath.Join(root, "empty")); err != nil {
		t.Fatalf("Remove empty: %v", err)
	}
	afterDelete, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot after delete: %v", err)
	}
	deleteConflicts := detectUserChangeConflicts(root, beforeDelete, afterDelete, []string{"empty"}, nil)
	if !slices.Contains(deleteConflicts, "empty") {
		t.Fatalf("expected directory deletion conflict, got %#v", deleteConflicts)
	}
}

func TestWorkspaceSnapshotIncludesClaudeWorktreeFiles(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".claude", "worktrees", "compassionate-goldberg", "completion.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	snapshot, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	rel := filepath.Clean(filepath.Join(".claude", "worktrees", "compassionate-goldberg", "completion.go"))
	if _, ok := snapshot[rel]; !ok {
		t.Fatalf("expected snapshot to include %s", rel)
	}
}

func TestWorkspaceSnapshotIncludesRootMetadata(t *testing.T) {
	root := t.TempDir()
	snapshot, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, ok := snapshot["."]; !ok {
		t.Fatalf("expected snapshot to include root metadata")
	}
}

func TestWorkspaceSnapshotWalksSymlinkedRootTarget(t *testing.T) {
	realRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(realRoot, "inside.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatalf("WriteFile inside: %v", err)
	}
	linkRoot := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	snapshot, err := snapshotWorkspaceFiles(linkRoot)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, ok := snapshot[filepath.Clean("inside.txt")]; !ok {
		t.Fatalf("expected snapshot through symlinked root to include real workspace file")
	}
}

func TestWorkspaceSnapshotTracksSymlinkTargetContent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	before, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	if err := os.WriteFile(target, []byte("bravo\n"), 0o644); err != nil {
		t.Fatalf("rewrite target: %v", err)
	}
	current, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot current: %v", err)
	}
	rel := filepath.Clean("link.txt")
	if before[rel] == current[rel] {
		t.Fatalf("expected symlink target content change to alter workspace signature")
	}
}

func TestWorkspaceSnapshotDoesNotHashExternalSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("WriteFile external: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	snapshot, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	signature := snapshot[filepath.Clean("link.txt")]
	if signature.LinkTargetStatus != "outside_root" {
		t.Fatalf("expected outside_root symlink status, got %q", signature.LinkTargetStatus)
	}
	if signature.LinkTargetContentSHA != "" {
		t.Fatalf("external symlink target content must not be hashed")
	}
}

func TestChangedWorkspaceSignaturePathsDoesNotCapEnforcementList(t *testing.T) {
	before := map[string]workspaceFileSignature{}
	current := map[string]workspaceFileSignature{}
	for index := 0; index < 40; index++ {
		path := filepath.Join("scope", fmt.Sprintf("file-%02d.txt", index))
		before[path] = workspaceFileSignature{Size: 1, ContentSHA: "before"}
		current[path] = workspaceFileSignature{Size: 1, ContentSHA: fmt.Sprintf("after-%02d", index)}
	}

	changed := changedWorkspaceSignaturePaths(before, current)
	if len(changed) != 40 {
		t.Fatalf("expected all changed paths, got %d: %#v", len(changed), changed)
	}
	if !slices.Contains(changed, filepath.ToSlash(filepath.Join("scope", "file-39.txt"))) {
		t.Fatalf("expected high-index changed path to remain in enforcement list")
	}
}

func TestNormalizeWorkspaceSignaturePathListSkipsEmptyEntries(t *testing.T) {
	paths := normalizeWorkspaceSignaturePathList([]string{
		"",
		"   ",
		"dir/file.txt",
		"dir/../dir/file.txt",
	})
	if !slices.Equal(paths, []string{"dir/file.txt"}) {
		t.Fatalf("expected only normalized non-empty paths, got %#v", paths)
	}
}

func TestNormalizeUserChangeIsolationPathPreservesCaseOffWindows(t *testing.T) {
	if workspacePathsAreCaseInsensitiveByDefault() {
		t.Skip("default workspace path matching is intentionally case-insensitive on this platform")
	}
	if got := normalizeUserChangeIsolationPath("Foo.go"); got != "Foo.go" {
		t.Fatalf("expected case-sensitive path normalization, got %q", got)
	}
}

func TestWorkspaceSnapshotIncludesGitMetadata(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".git", "config")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	if err := os.WriteFile(target, []byte("[core]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	snapshot, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	rel := filepath.Clean(filepath.Join(".git", "config"))
	if _, ok := snapshot[rel]; !ok {
		t.Fatalf("expected snapshot to include %s", rel)
	}
}

func TestWorkspaceSnapshotIncludesKernforgeMetadata(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, userConfigDirName, "state.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	snapshot, err := snapshotWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	rel := filepath.Clean(filepath.Join(userConfigDirName, "state.json"))
	if _, ok := snapshot[rel]; !ok {
		t.Fatalf("expected snapshot to include %s", rel)
	}
}

func TestRunShellAllowsDescriptorRedirectionWithoutFileWrite(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{BaseRoot: root, Root: root})

	command := "echo hello 2>&1"
	if runtime.GOOS == "windows" {
		command = "Write-Output hello 2>&1"
	}
	if _, err := tool.Execute(context.Background(), map[string]any{
		"command": command,
	}); err != nil {
		t.Fatalf("expected descriptor redirection without file write to succeed, got %v", err)
	}
}

func TestRunShellStreamsProgressFromOutput(t *testing.T) {
	root := t.TempDir()
	var progress []string
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		ReportProgress: func(message string) {
			progress = append(progress, message)
		},
	})

	command := "printf 'alpha\\nomega\\n'"
	if runtime.GOOS == "windows" {
		command = "Write-Output alpha; Write-Output omega"
	}
	out, err := tool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("expected streamed shell command to succeed, got %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "omega") {
		t.Fatalf("expected shell output to be captured, got %q", out)
	}
	found := false
	for _, item := range progress {
		if strings.HasPrefix(item, "run_shell output: ") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected run_shell output progress, got %#v", progress)
	}
}

func TestAssessShellCommandMutationClassifiesVerificationArtifactCommands(t *testing.T) {
	cases := map[string]shellMutationClass{
		"go test ./...":                              shellMutationVerificationArtifacts,
		"go build ./cmd/app":                         shellMutationVerificationArtifacts,
		"cargo test":                                 shellMutationVerificationArtifacts,
		"cargo check":                                shellMutationVerificationArtifacts,
		"pytest":                                     shellMutationVerificationArtifacts,
		"ctest --output-on-failure":                  shellMutationVerificationArtifacts,
		"cmake --build build":                        shellMutationVerificationArtifacts,
		"msbuild demo.sln /m":                        shellMutationVerificationArtifacts,
		"cmd /c msbuild demo.sln /m":                 shellMutationVerificationArtifacts,
		`cmd /s /c msbuild demo.sln /m`:              shellMutationVerificationArtifacts,
		`cmd /c "set CL=/FS && msbuild demo.sln /m"`: shellMutationVerificationArtifacts,
		`"C:\Program Files\Microsoft Visual Studio\2022\Professional\MSBuild\Current\Bin\MSBuild.exe" demo.sln /m`:                                                                             shellMutationVerificationArtifacts,
		`& "C:\Program Files\Microsoft Visual Studio\2022\Professional\MSBuild\Current\Bin\MSBuild.exe" demo.sln /m`:                                                                           shellMutationVerificationArtifacts,
		`powershell -Command "& 'C:\Program Files\Microsoft Visual Studio\2022\Professional\MSBuild\Current\Bin\MSBuild.exe' demo.sln /m"`:                                                     shellMutationVerificationArtifacts,
		`$msbuild = Get-Command MSBuild.exe -ErrorAction SilentlyContinue; if ($null -eq $msbuild) { Write-Error 'MSBuild.exe not found' } else { & $msbuild.Source SampleWorker.vcxproj /m }`: shellMutationVerificationArtifacts,
		"ninja":                shellMutationVerificationArtifacts,
		"cmd /c where msbuild": shellMutationReadOnly,
		`$msbuild = Get-Command MSBuild.exe -ErrorAction SilentlyContinue; if ($msbuild) { $msbuild.Source }`: shellMutationReadOnly,
		"go list ./...":      shellMutationCacheOnly,
		"git commit -m test": shellMutationGitMutation,
		`cd repo ; git init`: shellMutationGitMutation,
		"npm install react":  shellMutationWorkspaceWrite,
		`$content = Get-Content file.txt; [System.IO.File]::WriteAllText("file.txt", $content)`: shellMutationWorkspaceWrite,
		`Get-Content file.txt | Set-Content other.txt`:                                          shellMutationWorkspaceWrite,
		`Invoke-WebRequest https://example.test/file.txt -OutFile file.txt`:                     shellMutationWorkspaceWrite,
		`gofmt -w main.go`:                shellMutationWorkspaceWrite,
		`echo hello > "out file.txt"`:     shellMutationWorkspaceWrite,
		`bash -lc "echo hello > out.txt"`: shellMutationWorkspaceWrite,
		`bash -lc "cat <<'EOF' > out.txt
hello
EOF"`: shellMutationWorkspaceWrite,
		`powershell -NoProfile -Command "Write-Output hello > out.txt"`: shellMutationWorkspaceWrite,
		`cmd /c "echo hello > out.txt"`:                                 shellMutationWorkspaceWrite,
		`rg "Set-Content" docs`:                                         shellMutationReadOnly,
		`rg Set-Content docs`:                                           shellMutationReadOnly,
		`rg "[System.IO.File]::WriteAllText" docs`:                      shellMutationReadOnly,
		`bash -lc "echo '>'"`:                                           shellMutationReadOnly,
		`Get-Command tee`:                                               shellMutationReadOnly,
		`Get-Command copy`:                                              shellMutationReadOnly,
		`git log --% HEAD --output=codex_poc.txt`:                       shellMutationUnsupported,
		`powershell -NoProfile -Command "git log --% HEAD --output=codex_poc.txt"`:                                       shellMutationUnsupported,
		`powershell -NoProfile /Command "git log --% HEAD --output=codex_poc.txt"`:                                       shellMutationUnsupported,
		`powershell -NoProfile -Command:"git log --% HEAD --output=codex_poc.txt"`:                                       shellMutationUnsupported,
		`powershell -NoProfile /Command:"git log --% HEAD --output=codex_poc.txt"`:                                       shellMutationUnsupported,
		`pwsh -Command "git log --% HEAD --output=codex_poc.txt"`:                                                        shellMutationUnsupported,
		`powershell -NoProfile -EncodedCommand UwBlAHQALQBDAG8AbgB0AGUAbgB0ACAAYwBvAGQAZQB4AF8AcABvAGMALgB0AHgAdAAgAHgA`: shellMutationUnsupported,
		`pwsh -enc UwBlAHQALQBDAG8AbgB0AGUAbgB0ACAAYwBvAGQAZQB4AF8AcABvAGMALgB0AHgAdAAgAHgA`:                             shellMutationUnsupported,
		`powershell /EncodedCommand:UwBlAHQALQBDAG8AbgB0AGUAbgB0ACAAYwBvAGQAZQB4AF8AcABvAGMALgB0AHgAdAAgAHgA`:            shellMutationUnsupported,
		`rg "--%" docs`: shellMutationReadOnly,
		`powershell -NoProfile -Command "Write-Output '--%'"`:    shellMutationReadOnly,
		`powershell -NoProfile -Command:"Write-Output '--%'"`:    shellMutationReadOnly,
		`powershell -NoProfile /Command:"Write-Output '--%'"`:    shellMutationReadOnly,
		`powershell -NoProfile -Command:"Set-Content out.txt x"`: shellMutationWorkspaceWrite,
		`powershell -NoProfile /Command:"Set-Content out.txt x"`: shellMutationWorkspaceWrite,
		`rg --pre ./hook pattern .`:                              shellMutationUnsafe,
		`rg --pre=./hook pattern .`:                              shellMutationUnsafe,
		`rg --hostname-bin ./hostname foo .`:                     shellMutationUnsafe,
		`rg --hostname-bin=./hostname foo .`:                     shellMutationUnsafe,
		`rg --search-zip foo .`:                                  shellMutationUnsafe,
		`rg --search-zip=true foo .`:                             shellMutationReadOnly,
		`rg -z foo .`:                                            shellMutationUnsafe,
		`rg -zn foo .`:                                           shellMutationReadOnly,
		`rg -nz foo .`:                                           shellMutationReadOnly,
		`rg "--pre" docs`:                                        shellMutationUnsafe,
		`powershell -Command "rg --pre ./p ."`:                   shellMutationUnsafe,
		`bash -lc "rg --pre=./p foo ."`:                          shellMutationUnsafe,
		`bash -lc "rg --pre\=./p foo ."`:                         shellMutationUnsafe,
		`bash -lc "rg --\pre=./p foo ."`:                         shellMutationUnsafe,
		`bash -lc "rg --hostname\-bin=./hostname foo ."`:         shellMutationUnsafe,
		`bash -lc "rg -\z foo ."`:                                shellMutationUnsafe,
		`bash -lc "rg -\zn foo ."`:                               shellMutationReadOnly,
		"bash -lc \"rg --pr\\\ne=./p foo .\"":                    shellMutationReadOnly,
		"bash -lc \"rg \\\"--pr\\\ne=./p\\\" foo .\"":            shellMutationReadOnly,
		`bash -lc "rg \"--pre\=./p\" foo ."`:                     shellMutationReadOnly,
		`git -C . status`:                                        shellMutationUnsafe,
		`git -C. status`:                                         shellMutationUnsafe,
		`git -c core.pager=cat log -n 1`:                         shellMutationUnsafe,
		`git -ccore.pager=cat status`:                            shellMutationUnsafe,
		`git --paginate log -1`:                                  shellMutationUnsafe,
		`git -p log -1`:                                          shellMutationUnsafe,
		`git --config-env=core.pager=PAGER show HEAD`:            shellMutationUnsafe,
		`git --git-dir=.evil-git diff HEAD~1..HEAD`:              shellMutationUnsafe,
		`git --work-tree=. status`:                               shellMutationUnsafe,
		`git --exec-path=.git/helpers show HEAD`:                 shellMutationUnsafe,
		`git --namespace=attacker show HEAD`:                     shellMutationUnsafe,
		`git --super-prefix=attacker/ show HEAD`:                 shellMutationUnsafe,
		`git log --output=codex_poc.txt -n 1`:                    shellMutationUnsafe,
		`git diff --output codex_poc.txt`:                        shellMutationUnsafe,
		`git show --output=codex_poc.txt HEAD`:                   shellMutationUnsafe,
		`git diff --ext-diff HEAD`:                               shellMutationUnsafe,
		`git log --textconv -1`:                                  shellMutationUnsafe,
		`git log --exec=helper -1`:                               shellMutationUnsafe,
		`git cat-file --filters HEAD:a.txt`:                      shellMutationUnsafe,
		`git branch feature/test`:                                shellMutationUnsafe,
		`git branch -d feature/test`:                             shellMutationUnsafe,
		`powershell -NoProfile -Command "git --paginate log -1"`: shellMutationUnsafe,
		`bash -lc "git --git-dir=.evil-git diff HEAD~1..HEAD"`:   shellMutationUnsafe,
		`bash -lc "base64 -i input.txt -* cc06_out"`:             shellMutationUnsafe,
		`bash -lc "ls *.rs"`:                                     shellMutationUnsafe,
		`bash -lc "echo -{o,}"`:                                  shellMutationUnsafe,
		`sh -lc "cat [ab].txt"`:                                  shellMutationUnsafe,
		`zsh -lc "cat file?.txt"`:                                shellMutationUnsafe,
		`bash -lc "echo ~/loot"`:                                 shellMutationUnsafe,
		`bash -lc "echo '-*' '*.rs' '-{o,}'"`:                    shellMutationReadOnly,
		`bash -lc "echo '~'"`:                                    shellMutationReadOnly,
		`git log -p -1`:                                          shellMutationReadOnly,
		`git show -p HEAD`:                                       shellMutationReadOnly,
		`git branch`:                                             shellMutationReadOnly,
		`git branch --list`:                                      shellMutationReadOnly,
		`git branch --format=%(refname)`:                         shellMutationReadOnly,
		`git diff -p`:                                            shellMutationCacheOnly,
	}

	for command, want := range cases {
		got := assessShellCommandMutation(command)
		if got.Class != want {
			t.Fatalf("unexpected shell mutation class for %q: got %q want %q", command, got.Class, want)
		}
	}
}

func TestAssessShellCommandMutationIgnoresNullSinkFallbackSyntax(t *testing.T) {
	if got := assessShellCommandMutation(`ls -la . 2>/dev/null || echo "ls failed"`); got.Class != shellMutationReadOnly {
		t.Fatalf("expected Unix null sink fallback to stay read-only, got %q", got.Class)
	}
	if got := assessShellCommandMutation(`echo hello > out.txt 2>/dev/null`); got.Class != shellMutationWorkspaceWrite {
		t.Fatalf("expected real file write redirection to remain a workspace write, got %q", got.Class)
	}
}

func TestShellOutputCollectorTracksCarriageReturnProgress(t *testing.T) {
	var progress []string
	collector := newShellOutputCollector(Workspace{
		ReportProgress: func(message string) {
			progress = append(progress, message)
		},
	}, "demo command")

	collector.AppendBytes([]byte("step 1\rstep 2\rfinal\n"))

	if len(progress) == 0 {
		t.Fatalf("expected carriage-return progress updates")
	}
	if !strings.Contains(progress[len(progress)-1], "final") {
		t.Fatalf("expected final progress line to be tracked, got %#v", progress)
	}
	if !strings.Contains(collector.Text(), "final") {
		t.Fatalf("expected collector text to retain final output, got %q", collector.Text())
	}
}

func TestRunShellUsesWorkspaceDefaultTimeout(t *testing.T) {
	root := t.TempDir()
	command := "sleep 0.2"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Milliseconds 200"
	}
	tool := NewRunShellTool(Workspace{
		BaseRoot:     root,
		Root:         root,
		ShellTimeout: 50 * time.Millisecond,
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err == nil {
		t.Fatalf("expected shell command to time out")
	}
	if !strings.Contains(err.Error(), "command timed out after") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunBackgroundShellStartsAndCanBePolled(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)
	checkTool := NewCheckShellJobTool(ws)

	command := "sleep 0.1; echo ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Milliseconds 100; Write-Output ready"
	}
	if _, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	}); err != nil {
		t.Fatalf("run background shell: %v", err)
	}

	jobID := jobs.LatestJobID()
	if jobID == "" {
		t.Fatalf("expected a background job id")
	}

	var status string
	var output string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, err := checkTool.Execute(context.Background(), map[string]any{
			"job_id": jobID,
		})
		if err != nil {
			t.Fatalf("check shell job: %v", err)
		}
		output = result
		if strings.Contains(result, "status: completed") || strings.Contains(result, "status: failed") {
			status = result
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status == "" {
		t.Fatalf("expected background job to finish, last output: %q", output)
	}
	if !strings.Contains(status, "ready") {
		t.Fatalf("expected background job output to contain ready, got %q", status)
	}
}

func TestRunBackgroundShellWorkdirIsRecordedAndUsedForReuse(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)
	command := "sleep 1"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 1"
	}

	first, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command": command,
		"workdir": "subdir",
	})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := toolMetaString(first.Meta, "work_dir"); !sameFilePath(got, subdir) {
		t.Fatalf("expected first work_dir meta to resolve subdir, got %#v", first.Meta)
	}
	jobID := jobs.LatestJobID()
	if jobID == "" {
		t.Fatalf("expected background job")
	}
	job, ok := session.BackgroundJob(jobID)
	if !ok {
		t.Fatalf("expected recorded job %s", jobID)
	}
	if !sameFilePath(job.WorkDir, subdir) {
		t.Fatalf("expected job workdir %q, got %q", subdir, job.WorkDir)
	}

	second, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command": command,
		"workdir": "subdir",
	})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if reused, _ := second.Meta["reused"].(bool); !reused {
		t.Fatalf("expected matching workdir command to reuse background job, meta=%#v", second.Meta)
	}
}

func TestRunShellBundleBackgroundWorkdirIsRecorded(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunShellBundleBackgroundTool(ws)
	command := "sleep 1"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 1"
	}

	result, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"commands": []string{command},
		"workdir":  "subdir",
	})
	if err != nil {
		t.Fatalf("bundle run: %v", err)
	}
	if got := toolMetaString(result.Meta, "work_dir"); !sameFilePath(got, subdir) {
		t.Fatalf("expected bundle work_dir meta to resolve subdir, got %#v", result.Meta)
	}
	jobID := jobs.LatestJobID()
	if jobID == "" {
		t.Fatalf("expected background job")
	}
	job, ok := session.BackgroundJob(jobID)
	if !ok {
		t.Fatalf("expected recorded job %s", jobID)
	}
	if !sameFilePath(job.WorkDir, subdir) {
		t.Fatalf("expected job workdir %q, got %q", subdir, job.WorkDir)
	}
}

func TestDeclinedBackgroundVerificationLeavesNoPollableLatestJob(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			return false, nil
		},
	}
	runTool := NewRunBackgroundShellTool(ws)
	checkTool := NewCheckShellJobTool(ws)

	start, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command":       "go test ./...",
		"owner_node_id": "plan-02",
		"workdir":       ".",
	})
	if err != nil {
		t.Fatalf("declined background verification should be a non-error skip, got %v", err)
	}
	if got := toolMetaString(start.Meta, "verification_status"); got != string(VerificationSkipped) {
		t.Fatalf("expected skipped verification status, got %#v meta=%#v", got, start.Meta)
	}
	if got := toolMetaString(start.Meta, "owner_node_id"); got != "plan-02" {
		t.Fatalf("expected skipped verification owner metadata, got %#v", start.Meta)
	}
	if got := toolMetaString(start.Meta, "work_dir"); !sameFilePath(got, root) {
		t.Fatalf("expected skipped verification work_dir metadata, got %#v", start.Meta)
	}
	if latest := jobs.LatestJobID(); latest != "" {
		t.Fatalf("declined verification must not create a pollable job, got %q", latest)
	}

	poll, err := checkTool.ExecuteDetailed(context.Background(), map[string]any{
		"job_id": "latest",
	})
	if err != nil {
		t.Fatalf("polling latest after a declined background verification should be non-error, got %v", err)
	}
	if got := toolMetaString(poll.Meta, "result_class"); got != "background_unavailable" {
		t.Fatalf("expected background_unavailable result, got %#v meta=%#v", got, poll.Meta)
	}
	if !strings.Contains(poll.DisplayText, "nothing to poll") {
		t.Fatalf("expected no-work guidance, got %q", poll.DisplayText)
	}
	if !strings.Contains(poll.DisplayText, "Do not poll again") {
		t.Fatalf("expected repeated-poll prevention guidance, got %q", poll.DisplayText)
	}
}

func TestDeclinedBackgroundBundleVerificationPreservesRoutingMeta(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			return false, nil
		},
	}
	runTool := NewRunShellBundleBackgroundTool(ws)

	result, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"commands":      []string{"go test ./..."},
		"owner_node_id": "plan-02",
		"workdir":       "subdir",
	})
	if err != nil {
		t.Fatalf("declined background bundle verification should be a non-error skip, got %v", err)
	}
	if got := toolMetaString(result.Meta, "verification_status"); got != string(VerificationSkipped) {
		t.Fatalf("expected skipped verification status, got %#v meta=%#v", got, result.Meta)
	}
	if got := toolMetaString(result.Meta, "owner_node_id"); got != "plan-02" {
		t.Fatalf("expected skipped bundle owner metadata, got %#v", result.Meta)
	}
	if got := toolMetaString(result.Meta, "work_dir"); !sameFilePath(got, subdir) {
		t.Fatalf("expected skipped bundle work_dir metadata, got %#v", result.Meta)
	}
	if commands := toolMetaStringSlice(result.Meta, "commands"); len(commands) != 1 || commands[0] != "go test ./..." {
		t.Fatalf("expected skipped bundle command metadata, got %#v", result.Meta)
	}
	if latest := jobs.LatestBundleID(); latest != "" {
		t.Fatalf("declined verification must not create a pollable bundle, got %q", latest)
	}
}

func TestRunBackgroundShellReusesMatchingRunningJob(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)

	command := "sleep 1; echo ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 1; Write-Output ready"
	}
	first, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("first background shell: %v", err)
	}
	second, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("second background shell: %v", err)
	}
	if !strings.Contains(second, "reusing background shell job") {
		t.Fatalf("expected reuse message, got %q", second)
	}
	snapshot := jobs.Snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("expected one reusable background job, got %d", len(snapshot))
	}
	if !strings.Contains(first, snapshot[0].ID) || !strings.Contains(second, snapshot[0].ID) {
		t.Fatalf("expected both outputs to reference the same job id, got first=%q second=%q", first, second)
	}
}

func TestRunShellBundleBackgroundStartsParallelJobsAndCheckBundleSummarizes(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunShellBundleBackgroundTool(ws)
	checkTool := NewCheckShellBundleTool(ws)

	commands := []string{"go version", "go env GOOS"}
	if runtime.GOOS == "windows" {
		commands = []string{"Write-Output alpha", "Write-Output beta"}
	}
	started, err := runTool.Execute(context.Background(), map[string]any{
		"commands": commands,
	})
	if err != nil {
		t.Fatalf("run shell bundle: %v", err)
	}
	if !strings.Contains(started, "started background shell bundle") {
		t.Fatalf("unexpected bundle start output: %q", started)
	}
	if !strings.Contains(started, "bundle: ") {
		t.Fatalf("expected bundle id in start output, got %q", started)
	}
	snapshot := jobs.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 background jobs, got %d", len(snapshot))
	}
	bundleSnapshot := jobs.SnapshotBundles()
	if len(bundleSnapshot) != 1 {
		t.Fatalf("expected 1 background bundle, got %d", len(bundleSnapshot))
	}

	var summary string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, err := checkTool.Execute(context.Background(), map[string]any{
			"bundle_id": "latest",
		})
		if err != nil {
			t.Fatalf("check shell bundle: %v", err)
		}
		summary = result
		if strings.Contains(strings.ToLower(result), "running=0") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(strings.ToLower(summary), "summary:") {
		t.Fatalf("expected bundle summary, got %q", summary)
	}
	if !strings.Contains(strings.ToLower(summary), "bundle_status:") {
		t.Fatalf("expected bundle status, got %q", summary)
	}
	if !strings.Contains(strings.ToLower(summary), "total=2") {
		t.Fatalf("expected total count in bundle summary, got %q", summary)
	}
}

func TestRunShellBundleBackgroundReusesExistingRunningBundle(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunShellBundleBackgroundTool(ws)
	defer func() {
		for _, bundle := range jobs.SnapshotBundles() {
			_, _, _ = jobs.CancelBundle(bundle.ID, "canceled", "test cleanup", "")
		}
	}()

	commands := []string{"sleep 5; echo alpha", "sleep 5; echo beta"}
	if runtime.GOOS == "windows" {
		commands = []string{"Start-Sleep -Seconds 5; Write-Output alpha", "Start-Sleep -Seconds 5; Write-Output beta"}
	}

	first, err := runTool.Execute(context.Background(), map[string]any{
		"commands": commands,
	})
	if err != nil {
		t.Fatalf("first shell bundle: %v", err)
	}
	second, err := runTool.Execute(context.Background(), map[string]any{
		"commands": commands,
	})
	if err != nil {
		t.Fatalf("second shell bundle: %v", err)
	}

	bundles := jobs.SnapshotBundles()
	if len(bundles) != 1 {
		t.Fatalf("expected one reusable background bundle, got %d", len(bundles))
	}
	if !strings.Contains(first, bundles[0].ID) || !strings.Contains(second, bundles[0].ID) {
		t.Fatalf("expected both outputs to reference the same bundle id, got first=%q second=%q", first, second)
	}
}

func TestRunShellBundleBackgroundExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	root := t.TempDir()
	activeRoot := filepath.Join(root, "worktree")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           activeRoot,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunShellBundleBackgroundTool(ws)

	commands := []string{"go version", "go env GOOS"}
	if runtime.GOOS == "windows" {
		commands = []string{"Write-Output alpha", "Write-Output beta"}
	}
	result, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"commands": commands,
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if strings.TrimSpace(result.DisplayText) == "" {
		t.Fatalf("expected display text in detailed result")
	}
	if toolMetaString(result.Meta, "bundle_id") == "" {
		t.Fatalf("expected bundle id in structured meta, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "bundle_status") == "" {
		t.Fatalf("expected bundle status in structured meta, got %#v", result.Meta)
	}
	jobIDs := toolMetaStringSlice(result.Meta, "bundle_job_ids")
	if len(jobIDs) != 2 {
		t.Fatalf("expected 2 bundle job ids, got %#v", result.Meta)
	}
	if got := toolMetaString(result.Meta, "workspace_root"); !sameFilePath(got, root) {
		t.Fatalf("expected bundle base workspace root %q, got %#v", root, result.Meta)
	}
	if got := toolMetaString(result.Meta, "active_workspace_root"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected bundle active workspace root %q, got %#v", activeRoot, result.Meta)
	}
}

func TestCheckShellJobExecuteDetailedIncludesBundleMeta(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)
	checkTool := NewCheckShellJobTool(ws)

	command := "go version"
	if runtime.GOOS == "windows" {
		command = "Write-Output alpha"
	}
	start, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("run background shell detailed: %v", err)
	}
	jobID := toolMetaString(start.Meta, "job_id")
	if jobID == "" {
		t.Fatalf("expected job id in start meta, got %#v", start.Meta)
	}
	result, err := checkTool.ExecuteDetailed(context.Background(), map[string]any{
		"job_id": jobID,
	})
	if err != nil {
		t.Fatalf("check shell job detailed: %v", err)
	}
	if toolMetaString(result.Meta, "bundle_id") == "" {
		t.Fatalf("expected bundle id in check-shell-job meta, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "job_id") != jobID {
		t.Fatalf("expected stable job id in result meta, got %#v", result.Meta)
	}
}

func TestRunBackgroundShellReuseRunsPostHookAndReportsActualStatus(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	postHooks := 0
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			_ = payload
			if event == HookPostToolUse {
				postHooks++
			}
			return HookVerdict{}, nil
		},
	}
	runTool := NewRunBackgroundShellTool(ws)

	command := "sleep 1; echo ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 1; Write-Output ready"
	}
	first, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("first background shell: %v", err)
	}
	second, err := runTool.Execute(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("second background shell: %v", err)
	}

	if !strings.Contains(first, "status: running") {
		t.Fatalf("expected actual running status in first output, got %q", first)
	}
	if !strings.Contains(first, "status_file:") {
		t.Fatalf("expected explicit status file in first output, got %q", first)
	}
	if !strings.Contains(second, "reusing background shell job") {
		t.Fatalf("expected reuse message, got %q", second)
	}
	if postHooks != 2 {
		t.Fatalf("expected HookPostToolUse for both start and reuse paths, got %d", postHooks)
	}
}

func TestRunBackgroundShellExecuteDetailedCarriesOwnerNodeID(t *testing.T) {
	root := t.TempDir()
	activeRoot := filepath.Join(root, "worktree")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           activeRoot,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)

	command := "go version"
	if runtime.GOOS == "windows" {
		command = "Write-Output alpha"
	}
	result, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command":       command,
		"owner_node_id": "plan-02",
	})
	if err != nil {
		t.Fatalf("run background shell detailed: %v", err)
	}
	if toolMetaString(result.Meta, "owner_node_id") != "plan-02" {
		t.Fatalf("expected owner node id in meta, got %#v", result.Meta)
	}
	if got := toolMetaString(result.Meta, "workspace_root"); !sameFilePath(got, root) {
		t.Fatalf("expected base workspace root %q, got %#v", root, result.Meta)
	}
	if got := toolMetaString(result.Meta, "active_workspace_root"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected active workspace root %q, got %#v", activeRoot, result.Meta)
	}
	if got := toolMetaString(result.Meta, "work_dir"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected shell work_dir %q, got %#v", activeRoot, result.Meta)
	}
	bundles := jobs.SnapshotBundles()
	if len(bundles) != 1 || bundles[0].OwnerNodeID != "plan-02" {
		t.Fatalf("expected owner node id on persisted bundle, got %#v", bundles)
	}
}

func TestRunBackgroundShellPreToolUseRewriteStartsUpdatedCommand(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	var observed string
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			if event != HookPreToolUse {
				return HookVerdict{Allow: true}, nil
			}
			observed = stringsValueFromAny(payload["command"])
			return HookVerdict{
				Allow:        true,
				UpdatedInput: HookPayload{"command": "echo rewritten-bg"},
			}, nil
		},
	}
	runTool := NewRunBackgroundShellTool(ws)

	result, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command": "echo original-bg",
	})
	if err != nil {
		t.Fatalf("run background shell with hook rewrite: %v", err)
	}
	if observed != "echo original-bg" {
		t.Fatalf("expected hook to observe original background command, got %q", observed)
	}
	if !strings.Contains(result.DisplayText, "echo rewritten-bg") {
		t.Fatalf("expected rewritten command in display text, got %q", result.DisplayText)
	}
	jobID := jobs.LatestJobID()
	job, ok := session.BackgroundJob(jobID)
	if !ok {
		t.Fatalf("expected recorded background job %q", jobID)
	}
	if job.Command != "echo rewritten-bg" {
		t.Fatalf("expected rewritten command in job record, got %#v", job)
	}
}

func TestCancelShellBundleStopsRunningJobs(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)
	cancelTool := NewCancelShellBundleTool(ws)

	command := "sleep 5; echo test-ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 5; Write-Output test-ready"
	}
	started, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command": command,
	})
	if err != nil {
		t.Fatalf("run background shell detailed: %v", err)
	}
	bundleID := toolMetaString(started.Meta, "bundle_id")
	if bundleID == "" {
		t.Fatalf("expected bundle id in start meta, got %#v", started.Meta)
	}

	result, err := cancelTool.ExecuteDetailed(context.Background(), map[string]any{
		"bundle_id": bundleID,
		"reason":    "Newer verification replaced this run.",
	})
	if err != nil {
		t.Fatalf("cancel shell bundle detailed: %v", err)
	}
	if toolMetaString(result.Meta, "bundle_status") != "canceled" {
		t.Fatalf("expected canceled bundle status, got %#v", result.Meta)
	}
	snapshotBundles := jobs.SnapshotBundles()
	if len(snapshotBundles) != 1 || snapshotBundles[0].Status != "canceled" {
		t.Fatalf("expected persisted canceled bundle, got %#v", snapshotBundles)
	}
	snapshotJobs := jobs.Snapshot()
	if len(snapshotJobs) != 1 || (snapshotJobs[0].Status != "canceled" && snapshotJobs[0].Status != "preempted") {
		t.Fatalf("expected canceled or preempted job, got %#v", snapshotJobs)
	}
}

func TestMarkBackgroundBundlesStalePreemptsRunningBundle(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "test", "test-model", "", "default")
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jobs := NewBackgroundJobManager(filepath.Join(root, userConfigDirName, "jobs"), session, store)
	ws := Workspace{
		BaseRoot:       root,
		Root:           root,
		Shell:          defaultShell(),
		BackgroundJobs: jobs,
	}
	runTool := NewRunBackgroundShellTool(ws)
	command := "sleep 5; echo test-ready"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 5; Write-Output test-ready"
	}
	started, err := runTool.ExecuteDetailed(context.Background(), map[string]any{
		"command":       command,
		"owner_node_id": "plan-02",
	})
	if err != nil {
		t.Fatalf("run background shell detailed: %v", err)
	}
	bundleID := toolMetaString(started.Meta, "bundle_id")
	jobID := toolMetaString(started.Meta, "job_id")
	if bundleID == "" || jobID == "" {
		t.Fatalf("expected bundle/job ids, got %#v", started.Meta)
	}
	agent := &Agent{
		Config:    Config{},
		Session:   session,
		Workspace: ws,
	}
	agent.markBackgroundBundlesStale("A newer edit invalidated the previous verification.")

	bundle, ok := session.BackgroundBundle(bundleID)
	if !ok || bundle.Status != "stale" {
		t.Fatalf("expected stale bundle after preemption, got %#v", bundle)
	}
	job, ok := session.BackgroundJob(jobID)
	if !ok || job.Status != "preempted" {
		t.Fatalf("expected preempted job after stale invalidation, got %#v", job)
	}
}

func TestApplyPatchDoesNotUpdateBaseRootFallbackOutsideActiveRoot(t *testing.T) {
	base := t.TempDir()
	current := filepath.Join(base, "nested")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(base, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tool := NewApplyPatchTool(Workspace{
		BaseRoot: base,
		Root:     current,
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
	})
	if err == nil {
		t.Fatalf("expected active-root bounded patch to reject base fallback target")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "package main\n" {
		t.Fatalf("base target must remain unchanged, got %q", string(data))
	}
	if _, statErr := os.Stat(filepath.Join(current, "main.go")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("nested target should not be created, stat err=%v", statErr)
	}
}

func TestRunShellVerificationCommandRequiresConfirmation(t *testing.T) {
	root := t.TempDir()
	promptCount := 0
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			promptCount++
			if len(plan.Steps) != 1 || !strings.Contains(plan.Steps[0].Command, "go test") {
				t.Fatalf("unexpected verification plan: %#v", plan)
			}
			return false, nil
		},
	})

	text, err := tool.Execute(context.Background(), map[string]any{
		"command": "go test ./...",
	})
	if err != nil {
		t.Fatalf("declined verification command should be a non-error skip, got %v", err)
	}
	if !strings.Contains(text, "skipped") {
		t.Fatalf("expected skipped message, got %q", text)
	}
	if !strings.Contains(text, "Do not retry") {
		t.Fatalf("expected declined verification retry guidance, got %q", text)
	}
	if promptCount != 1 {
		t.Fatalf("expected one confirmation prompt, got %d", promptCount)
	}
}

func TestRunShellVerificationPermissionRequestAllowBypassesConfirmation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/permhook\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "permhook_test.go"), []byte("package permhook\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile test: %v", err)
	}
	promptCount := 0
	permissionHookCalls := 0
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		Perms: NewPermissionManager(ModeDefault, func(string) (bool, error) {
			return true, nil
		}),
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			_ = plan
			promptCount++
			return false, nil
		},
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			if event == HookPermissionRequest {
				permissionHookCalls++
				if boolValueFromAny(payload["verification"]) {
					if got := toolMetaString(payload, "command"); got != "go test ./..." {
						t.Fatalf("expected verification command in PermissionRequest payload, got %#v", payload)
					}
					return HookVerdict{
						Allow:              true,
						PermissionDecision: "allow",
					}, nil
				}
			}
			return HookVerdict{Allow: true}, nil
		},
	})

	text, err := tool.Execute(context.Background(), map[string]any{
		"command": "go test ./...",
	})
	if err != nil {
		t.Fatalf("expected PermissionRequest allow to bypass verification prompt, got %v", err)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatalf("expected command output or failure text, got %q", text)
	}
	if promptCount != 0 {
		t.Fatalf("PermissionRequest allow should bypass verification prompt, got %d prompt(s)", promptCount)
	}
	if permissionHookCalls == 0 {
		t.Fatalf("expected PermissionRequest hook to run")
	}
}

func TestRunShellDeclinedVerificationDetailedStatusIsSkipped(t *testing.T) {
	root := t.TempDir()
	tool := NewRunShellTool(Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			return false, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"command": "go test ./...",
	})
	if err != nil {
		t.Fatalf("declined verification command should be a non-error skip, got %v", err)
	}
	if got := toolMetaString(result.Meta, "verification_status"); got != string(VerificationSkipped) {
		t.Fatalf("expected skipped verification status, got %#v meta=%#v", got, result.Meta)
	}
	if got := toolMetaString(result.Meta, "command_execution_status"); got != "declined" {
		t.Fatalf("expected declined command execution status, got %#v meta=%#v", got, result.Meta)
	}
	if toolMetaBool(result.Meta, "verification_evidence") {
		t.Fatalf("declined verification must not be evidence, got %#v", result.Meta)
	}
	if !toolMetaBool(result.Meta, "verification_declined") {
		t.Fatalf("expected declined verification metadata, got %#v", result.Meta)
	}
	if !strings.Contains(result.DisplayText, "Do not retry") {
		t.Fatalf("expected declined verification retry guidance, got %q", result.DisplayText)
	}
}

func TestSkippedVerificationOutputIsNotSuccessfulEvidence(t *testing.T) {
	if runShellOutputLooksLikeVerification("verification command skipped because the user declined to run it") {
		t.Fatalf("declined verification output must not be treated as successful verification evidence")
	}
	if !runShellOutputLooksLikeVerification("ok  \t./cmd/kernforge") {
		t.Fatalf("passing verification-like output should still be recognized")
	}
}
