package main

import "testing"

func TestSessionSelectionStackAddActivateRemove(t *testing.T) {
	sess := NewSession("F:/repo", "openai", "gpt", "", "default")
	s1 := ViewerSelection{FilePath: "main.go", StartLine: 1, EndLine: 3}
	s2 := ViewerSelection{FilePath: "provider.go", StartLine: 10, EndLine: 12}

	sess.AddSelection(s1)
	sess.AddSelection(s2)

	if len(sess.Selections) != 2 {
		t.Fatalf("expected 2 selections, got %#v", sess.Selections)
	}
	current := sess.CurrentSelection()
	if current == nil || current.FilePath != "provider.go" {
		t.Fatalf("expected most recent selection to be active, got %#v", current)
	}

	if !sess.SetActiveSelection(0) {
		t.Fatal("expected SetActiveSelection to succeed")
	}
	current = sess.CurrentSelection()
	if current == nil || current.FilePath != "main.go" {
		t.Fatalf("expected main.go to be active, got %#v", current)
	}

	if !sess.RemoveSelection(0) {
		t.Fatal("expected RemoveSelection to succeed")
	}
	if len(sess.Selections) != 1 {
		t.Fatalf("expected one selection left, got %#v", sess.Selections)
	}
	current = sess.CurrentSelection()
	if current == nil || current.FilePath != "provider.go" {
		t.Fatalf("expected remaining selection to become active, got %#v", current)
	}
}

func TestSessionSelectionMetadataCanBeStored(t *testing.T) {
	sess := NewSession("F:/repo", "openai", "gpt", "", "default")
	sess.AddSelection(ViewerSelection{FilePath: "main.go", StartLine: 4, EndLine: 8})
	if current := sess.CurrentSelection(); current == nil {
		t.Fatal("expected current selection")
	}
	sess.Selections[sess.ActiveSelection].Note = "important auth branch"
	sess.Selections[sess.ActiveSelection].SetTags("auth,critical")
	current := sess.CurrentSelection()
	if current == nil || current.Note != "important auth branch" {
		t.Fatalf("expected note to persist, got %#v", current)
	}
	if len(current.Tags) != 2 {
		t.Fatalf("expected tags to persist, got %#v", current)
	}
}
