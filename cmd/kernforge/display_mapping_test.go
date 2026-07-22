package main

import (
	"strings"
	"testing"
)

func TestSoftenAssistantDisplayTextRewritesChecklistLabels(t *testing.T) {
	raw := strings.TrimSpace(`
Changed files: main.go.
Self-review: no code blocker found.
Validation: verification was not run.
Remaining risk: none known.
`)
	en := softenAssistantDisplayText(Config{AutoLocale: boolPtr(false)}, raw)
	for _, want := range []string{
		"Updated: main.go.",
		"Checked:",
		"Verification was not run.",
		"No known remaining risks.",
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("english soften missing %q in:\n%s", want, en)
		}
	}
	if strings.Contains(en, "Validation:") || strings.Contains(en, "Remaining risk:") {
		t.Fatalf("english soften kept rigid labels:\n%s", en)
	}

	ko := softenAssistantDisplayText(Config{AutoLocale: boolPtr(true)}, raw)
	for _, want := range []string{
		"변경:",
		"검증은 아직 실행하지 않았습니다.",
		"남은 위험은 없습니다.",
	} {
		if !strings.Contains(ko, want) {
			t.Fatalf("korean soften missing %q in:\n%s", want, ko)
		}
	}
}

func TestSoftenAssistantDisplayTextLeavesOrdinaryProse(t *testing.T) {
	raw := "I updated the invite path and rechecked the kick flow."
	got := softenAssistantDisplayText(Config{}, raw)
	if got != raw {
		t.Fatalf("ordinary prose must stay unchanged, got %q", got)
	}
}
