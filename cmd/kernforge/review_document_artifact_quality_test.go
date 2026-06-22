package main

import (
	"strings"
	"testing"
)

// TestSummarizeProposedEditDiffTreatsDocumentAsDocument locks Fix D: a markdown
// document that embeds fenced code blocks must NOT be summarized as code. The
// per-file line counts stay, but "new imports"/"new definitions" extracted from
// the embedded code fences must not appear - otherwise the reviewer is told a
// prose design document is code with those symbols.
func TestSummarizeProposedEditDiffTreatsDocumentAsDocument(t *testing.T) {
	before := "# Design\n"
	after := strings.Join([]string{
		"# Design",
		"",
		"```python",
		"import pseudo_forge",
		"class VulnLensPlugin:",
		"    def run(self):",
		"        return None",
		"```",
	}, "\n")

	summary := summarizeProposedEditDiff(buildEditPreview("VULNLENS_DESIGN.md", before, after))
	if summary == "" {
		t.Fatal("expected a non-empty change summary")
	}
	if !strings.Contains(summary, "VULNLENS_DESIGN.md: +") {
		t.Fatalf("summary should still report per-file line deltas, got:\n%s", summary)
	}
	for _, banned := range []string{"new imports", "new definitions", "pseudo_forge", "VulnLensPlugin"} {
		if strings.Contains(summary, banned) {
			t.Fatalf("document summary must not surface embedded code as %q, got:\n%s", banned, summary)
		}
	}

	// A real code file with the same lines must still surface them.
	codeSummary := summarizeProposedEditDiff(buildEditPreview("plugin.py", "import json\n", "import json\nimport pseudo_forge\nclass VulnLensPlugin:\n    pass\n"))
	if !strings.Contains(codeSummary, "new imports") || !strings.Contains(codeSummary, "pseudo_forge") {
		t.Fatalf("code-file summary must still extract imports, got:\n%s", codeSummary)
	}
}

func TestReviewFindingIsDocsOnlyDescribedSecurity(t *testing.T) {
	docRun := ReviewRun{ChangeSet: ReviewChangeSet{ChangedPaths: []string{"DESIGN.md"}}}
	codeRun := ReviewRun{ChangeSet: ReviewChangeSet{ChangedPaths: []string{"driver.c"}}}
	security := ReviewFinding{Category: "security"}
	bypass := ReviewFinding{Category: "bypass_surface"}
	secret := ReviewFinding{Category: "credential_leak"}

	if !reviewFindingIsDocsOnlyDescribedSecurity(docRun, security) {
		t.Fatal("docs-only security finding should be a described-security carve-out")
	}
	if !reviewFindingIsDocsOnlyDescribedSecurity(docRun, bypass) {
		t.Fatal("docs-only bypass_surface finding should be a described-security carve-out")
	}
	if reviewFindingIsDocsOnlyDescribedSecurity(docRun, secret) {
		t.Fatal("a credential leak committed in a document must NOT be carved out")
	}
	if reviewFindingIsDocsOnlyDescribedSecurity(codeRun, security) {
		t.Fatal("a code change must keep security findings blocking")
	}
}

func docArtifactSecurityFinding() ReviewFinding {
	return ReviewFinding{
		ID:           "RF-001",
		Source:       "model",
		ReviewerRole: "primary_reviewer",
		Severity:     reviewSeverityMedium,
		Category:     "security",
		Confidence:   "medium",
		Quality:      reviewFindingQualityComplete,
		Title:        "Described cloud-LLM consent inconsistency",
		Evidence:     "Data-flow auto-runs Tier 3 but the security section requires user approval.",
		Impact:       "A future implementer could send code to an external LLM without consent.",
		RequiredFix:  "Make the architecture and the security policy agree in the document.",
	}
}

// TestDocsOnlySecurityFindingDoesNotBlockPreWriteGate locks Fix A: a model
// security warning raised against a docs-only design-document write must surface
// as a warning, not get promoted into a pre-write blocker that hard-blocks the
// write. The same finding against a code change must still block.
func TestDocsOnlySecurityFindingDoesNotBlockPreWriteGate(t *testing.T) {
	docRun := ReviewRun{
		Trigger:   "pre_write",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"VULNLENS_DESIGN.md"}},
		Findings:  []ReviewFinding{docArtifactSecurityFinding()},
	}
	gate := evaluateReviewGate(docRun)
	if len(gate.BlockingFindings) != 0 {
		t.Fatalf("docs-only security finding must not block the write, got blockers %v", gate.BlockingFindings)
	}
	if gate.Verdict != reviewVerdictApprovedWithWarnings {
		t.Fatalf("docs-only security finding should approve with warnings, got %q", gate.Verdict)
	}

	codeRun := ReviewRun{
		Trigger:   "pre_write",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"driver.c"}},
		Findings:  []ReviewFinding{docArtifactSecurityFinding()},
	}
	codeGate := evaluateReviewGate(codeRun)
	if len(codeGate.BlockingFindings) == 0 {
		t.Fatalf("a code change with the same security finding must still block")
	}
}

// TestDocsOnlyCredentialLeakStillBlocks ensures the carve-out is narrow: a real
// secret committed in a document still hard-blocks.
func TestDocsOnlyCredentialLeakStillBlocks(t *testing.T) {
	finding := docArtifactSecurityFinding()
	finding.Category = "credential_leak"
	finding.Title = "Hardcoded API key in document"
	run := ReviewRun{
		Trigger:   "pre_write",
		ChangeSet: ReviewChangeSet{ChangedPaths: []string{"NOTES.md"}},
		Findings:  []ReviewFinding{finding},
	}
	gate := evaluateReviewGate(run)
	if len(gate.BlockingFindings) == 0 {
		t.Fatalf("a credential leak in a document must still block, got verdict %q", gate.Verdict)
	}
}
