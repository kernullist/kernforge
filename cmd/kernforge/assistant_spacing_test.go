package main

import "testing"

// assistant_spacing_test.go locks in the assistant-body spacing contract that
// formatAssistantText produces. The load-bearing new behavior is that a
// flush-left paragraph following a bullet/numbered list gets a blank line so the
// answer breathes, while a wrapped (indented) list-item continuation stays
// attached to its bullet. The pre-existing spacing rules (headings, fences,
// consecutive same-kind runs, hard-wrapped prose) must not regress.
func TestFormatAssistantTextSpacing(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "flush-left paragraph after a list breathes",
			in:   "- item one\n- item two\nNext paragraph here.",
			want: "- item one\n- item two\n\nNext paragraph here.",
		},
		{
			name: "indented continuation stays attached to its bullet",
			in:   "- item one\n  wrapped continuation of item one",
			want: "- item one\n  wrapped continuation of item one",
		},
		{
			name: "consecutive list items are not spaced apart",
			in:   "- alpha\n- beta\n- gamma",
			want: "- alpha\n- beta\n- gamma",
		},
		{
			name: "ordered-list item followed by a flush-left paragraph breathes",
			in:   "1. first step\n2. second step\nThat completes the flow.",
			want: "1. first step\n2. second step\n\nThat completes the flow.",
		},
		{
			name: "hard-wrapped prose is not over-spaced",
			in:   "This is a single paragraph that the model\nhard-wrapped across two source lines.",
			want: "This is a single paragraph that the model\nhard-wrapped across two source lines.",
		},
		{
			name: "heading after a paragraph is spaced",
			in:   "Some intro text.\n## Section",
			want: "Some intro text.\n\n## Section",
		},
		{
			name: "model-emitted blank line between paragraphs is preserved",
			in:   "First paragraph.\n\nSecond paragraph.",
			want: "First paragraph.\n\nSecond paragraph.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatAssistantText(tc.in)
			if got != tc.want {
				t.Fatalf("formatAssistantText mismatch\n--- in ---\n%q\n--- got ---\n%q\n--- want ---\n%q", tc.in, got, tc.want)
			}
		})
	}
}

// TestShouldInsertAssistantSpacerListToParagraph is a focused unit guard for the
// flush-left gate so a future refactor of formatAssistantText cannot silently
// drop the distinction between a new paragraph and a wrapped list continuation.
func TestShouldInsertAssistantSpacerListToParagraph(t *testing.T) {
	if !shouldInsertAssistantSpacer(assistantLineList, assistantLineParagraph, false, true) {
		t.Fatal("a flush-left paragraph after a list should get a spacer")
	}
	if shouldInsertAssistantSpacer(assistantLineList, assistantLineParagraph, false, false) {
		t.Fatal("an indented continuation after a list must not get a spacer")
	}
	if shouldInsertAssistantSpacer(assistantLineList, assistantLineList, false, true) {
		t.Fatal("consecutive list items must not be spaced apart")
	}
	if shouldInsertAssistantSpacer(assistantLineParagraph, assistantLineParagraph, false, true) {
		t.Fatal("hard-wrapped prose must not be over-spaced")
	}
}
