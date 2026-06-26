package main

import (
	"context"
	"strings"
	"testing"
)

func TestParseUserChoiceAnswer(t *testing.T) {
	q := UserQuestion{Options: []UserQuestionOption{{Label: "A"}, {Label: "B"}, {Label: "C"}}}
	if r, ok := parseUserChoiceAnswer(q, "2"); !ok || len(r.Selected) != 1 || r.Selected[0] != "B" {
		t.Fatalf("single number: ok=%v %#v", ok, r)
	}
	if _, ok := parseUserChoiceAnswer(q, "9"); ok {
		t.Fatalf("out-of-range number must be rejected")
	}
	if _, ok := parseUserChoiceAnswer(q, "1,2"); ok {
		t.Fatalf("multiple selection must be rejected when Multiple=false")
	}
	qm := q
	qm.Multiple = true
	if r, ok := parseUserChoiceAnswer(qm, "1,3"); !ok || len(r.Selected) != 2 || r.Selected[1] != "C" {
		t.Fatalf("multiple: ok=%v %#v", ok, r)
	}
	if _, ok := parseUserChoiceAnswer(q, "freeform"); ok {
		t.Fatalf("custom must be rejected when AllowCustom=false")
	}
	qc := q
	qc.AllowCustom = true
	if r, ok := parseUserChoiceAnswer(qc, "freeform"); !ok || r.Custom != "freeform" {
		t.Fatalf("custom: ok=%v %#v", ok, r)
	}
}

func TestAskUserToolExecute(t *testing.T) {
	ws := Workspace{PromptUserChoice: func(q UserQuestion) (UserQuestionResult, error) {
		return UserQuestionResult{Selected: []string{q.Options[0].Label}}, nil
	}}
	r, err := NewAskUserTool(ws).ExecuteDetailed(context.Background(), map[string]any{
		"question": "Pick one",
		"options":  []any{map[string]any{"label": "Xx"}, map[string]any{"label": "Yy"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(r.ModelText, "Xx") {
		t.Fatalf("answer must carry the chosen label, got %q", r.ModelText)
	}

	// Headless (nil callback) reports unavailable instead of blocking.
	r2, err := NewAskUserTool(Workspace{}).ExecuteDetailed(context.Background(), map[string]any{
		"question": "Q", "options": []any{"Xx"},
	})
	if err != nil {
		t.Fatalf("headless must not error: %v", err)
	}
	if ok, _ := r2.Meta["success"].(bool); ok {
		t.Fatalf("headless ask_user must report unavailable")
	}
}
