package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestCountAnalysisExplorationPathsSkipsHarnessArtifacts(t *testing.T) {
	seen := map[string]struct{}{
		"README.md":                 {},
		"imwatchingu/cli.py":        {},
		".kernforge/reviews/old.md": {},
		"":                          {},
	}
	if got := countAnalysisExplorationPaths(seen); got != 2 {
		t.Fatalf("countAnalysisExplorationPaths=%d want 2", got)
	}
}

func TestAnalysisExplorationNudgeHasNoHardBudgetClaim(t *testing.T) {
	nudge := analysisExplorationNudgeGuidance(12)
	if !strings.Contains(nudge, "12") {
		t.Fatalf("nudge: %q", nudge)
	}
	lower := strings.ToLower(nudge)
	if strings.Contains(lower, "final-answer only") || strings.Contains(lower, "do not call any more tools") {
		t.Fatalf("nudge must not hard-ban tools: %q", nudge)
	}
	if !strings.Contains(lower, "no hard") && !strings.Contains(nudge, "하드") {
		// English guidance explicitly says there is no hard file-read cap.
		if !strings.Contains(lower, "no hard file-read cap") {
			t.Fatalf("nudge should clarify there is no hard cap: %q", nudge)
		}
	}
}

func TestAnalysisOnlyTurnClassificationForReadmeGap(t *testing.T) {
	q := "@README.md 문서를 읽고 현재 구현에 부족한 부분을 찾아서 알려줘"
	if !requestLooksLikeAnalysisOnlyTurn(q) {
		t.Fatalf("expected analysis-only for %q", q)
	}
}

func TestInspectThenDocumentTurnIsNonRepair(t *testing.T) {
	q := "@README.md 문서를 읽고 현재 구현에 부족한 부분을 찾아서 문서로 작성해줘"
	if !requestLooksLikeInspectThenDocumentTurn(q) {
		t.Fatalf("expected inspect-then-document for %q", q)
	}
	if !requestLooksLikeAnalysisOnlyTurn(q) {
		t.Fatalf("document-after-inspect must not be treated as code-repair turn: %q", q)
	}
	// Recovery card for this request must prefer answer/write, not repair.
	recovery := buildStallBlockedRecoveryWithMode(Config{}, harnessRecoveryCauseReadChurn, "stopped", []string{"README.md"}, true)
	if recovery.Actions[0].Kind != harnessRecoveryActionAnswer {
		t.Fatalf("primary recovery must be answer/write, got %#v", recovery.Actions[0])
	}
	if recovery.Actions[0].Kind == harnessRecoveryActionRepair {
		t.Fatalf("must not offer repair as primary")
	}
}

func TestShouldTrackRepeatedSignatureForReadPlusGrep(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file", Arguments: `{"path":"README.md"}`},
		{Name: "grep", Arguments: `{"pattern":"TODO"}`},
	}
	if !shouldTrackRepeatedToolCallSignature(calls) {
		t.Fatalf("read+grep batches must be signature-tracked")
	}
	if shouldTrackRepeatedToolCallSignature([]ToolCall{
		{Name: "read_file", Arguments: `{"path":"a.go"}`},
		{Name: "read_file", Arguments: `{"path":"b.go"}`},
	}) {
		t.Fatalf("pure read_file batches are owned by multi-path detectors")
	}
}

func TestAnalysisExplorationBlocksRevisitButNotNewPath(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 3; i++ {
		seen[fmt.Sprintf("src/file%d.py", i)] = struct{}{}
	}
	// Re-read only already-seen files.
	calls := []ToolCall{
		{Name: "read_file", Arguments: `{"path":"src/file0.py"}`},
		{Name: "read_file", Arguments: `{"path":"src/file1.py"}`},
	}
	block, reason := analysisExplorationShouldBlockRevisitBatch(seen, calls)
	if !block {
		t.Fatalf("expected block on revisit batch, reason=%q", reason)
	}
	// Brand-new path must stay allowed (no hard path budget).
	callsNew := []ToolCall{
		{Name: "read_file", Arguments: `{"path":"src/brand_new.py"}`},
	}
	block, reason = analysisExplorationShouldBlockRevisitBatch(seen, callsNew)
	if block {
		t.Fatalf("new path must not be blocked, reason=%q", reason)
	}
	// list_files alone is allowed even after many reads.
	block, reason = analysisExplorationShouldBlockRevisitBatch(seen, []ToolCall{
		{Name: "list_files", Arguments: `{"path":"src"}`},
	})
	if block {
		t.Fatalf("list_files must not be treated as revisit-only, reason=%q", reason)
	}
}

func TestAnalysisExplorationNoHardPathForce(t *testing.T) {
	// Many paths already seen, but requesting a new file must still be allowed.
	seen := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		seen[fmt.Sprintf("pkg/mod%d.go", i)] = struct{}{}
	}
	block, reason := analysisExplorationShouldBlockRevisitBatch(seen, []ToolCall{
		{Name: "read_file", Arguments: `{"path":"pkg/extra.go"}`},
	})
	if block {
		t.Fatalf("hard path budget must not exist; blocked new read: %q", reason)
	}
}
