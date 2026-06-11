package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeBoundaryFilesUseRequestEnvelopeAsSourceOfTruth(t *testing.T) {
	root := filepath.Join("cmd", "kernforge")
	if _, err := os.Stat(root); err != nil {
		root = "."
	}
	files := []string{
		"final_gate.go",
		"interactive_orchestration.go",
		"mcp_review.go",
		"request_runtime_shadow.go",
		"review_harness_collect.go",
		"review_harness_gate.go",
		"review_operator_status.go",
		"tool_contract.go",
		"turn_queue.go",
		"turn_runtime.go",
	}
	for _, name := range files {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, forbidden := range []string{
			"prefersReadOnlyAnalysisIntent(",
			"looksLikeExplicitEditIntent(",
			"looksLikeDocumentAuthoringIntent(",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s directly calls %s; route request boundary decisions through RequestEnvelope helpers instead", name, forbidden)
			}
		}
	}
}
