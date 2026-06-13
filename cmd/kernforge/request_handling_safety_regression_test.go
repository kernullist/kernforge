package main

import "testing"

// TestRequestHandlingSafetyRegression locks the corrected request-classification
// behavior for the four defect clusters. The guiding principle: ambiguity falls
// toward read-only / least-privilege. A request that should be read-only must
// never yield AllowsFileMutation / AllowsGitMutation / must_edit.
//
// Defect families covered:
//   F1        run/build/test-only requests are run_command, not source edits.
//   F2,F16    substring / word-boundary false positives stay read-only.
//   F3        git negation / git questions never allow git mutation.
//   F10       no-change negation keeps file mutation off.
//   F5        git-only English is a git intent, not a source must_edit.
//   DO-NOT-BREAK invariants stay green.
func TestRequestHandlingSafetyRegression(t *testing.T) {
	cases := []struct {
		name             string
		request          string
		wantIntent       TurnIntent
		wantFileMutation bool
		wantGitMutation  bool
		wantReadOnly     bool   // when true, ReadOnlyAnalysis must hold
		forbidMustEdit   bool   // when true, Boundary must not be must_edit
		wantPrimary      RequestClass
		wantReviewClass  string
		wantExplicitEdit *bool // optional explicit-edit assertion
	}{
		// F1: run / build / test only -> run_command, never an edit.
		{name: "ko run tests", request: "테스트 실행해줘", wantIntent: TurnIntentRunCommand, wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},
		{name: "en run the tests", request: "run the tests", wantIntent: TurnIntentRunCommand, wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},
		{name: "ko build", request: "빌드해줘", wantIntent: TurnIntentRunCommand, wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},
		{name: "en build the project", request: "build the project", wantIntent: TurnIntentRunCommand, wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},
		{name: "en go test", request: "go test ./...", wantIntent: TurnIntentRunCommand, wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},

		// F2,F16: substring / word-boundary false positives stay read-only.
		{name: "en explain prefix matching", request: "explain the prefix matching logic", wantFileMutation: false, wantGitMutation: false, wantReadOnly: true, forbidMustEdit: true},
		{name: "en describe suffix computed", request: "describe how the suffix is computed", wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},
		{name: "en explain run loop", request: "explain the run loop", wantFileMutation: false, wantGitMutation: false, wantReadOnly: true, forbidMustEdit: true},
		{name: "ko latest kernel changes", request: "최신 커널 변경사항 알려줘", wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},
		{name: "ko analyze only this change", request: "이 변경을 분석만 해줘", wantFileMutation: false, wantGitMutation: false, wantReadOnly: true, forbidMustEdit: true},
		{name: "ko explain multistage pipeline", request: "multistage pipeline 설명해줘", wantFileMutation: false, wantGitMutation: false, wantReadOnly: true, forbidMustEdit: true},

		// F2 English noun-form verbs (update/patch/change) in descriptive
		// questions must stay read-only; they are edit intent only with an
		// imperative source-edit command.
		{name: "en latest update question", request: "what is the latest update on the schedule?", wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},
		{name: "en patch tuesday cycle", request: "explain how the patch tuesday cycle works", wantFileMutation: false, wantGitMutation: false, wantReadOnly: true, forbidMustEdit: true},
		{name: "en describe update mechanism", request: "describe the update mechanism", wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},
		{name: "en what changes were made", request: "what changes were made in the last release?", wantFileMutation: false, wantGitMutation: false, forbidMustEdit: true},
		// Imperative source-edit with the same verbs stays editable.
		{name: "en update the handler", request: "update the handler to use the new API", wantFileMutation: true, wantPrimary: RequestClassEdit, wantExplicitEdit: boolPtr(true)},
		{name: "en patch the overflow", request: "patch the buffer overflow in main.c", wantFileMutation: true, wantPrimary: RequestClassEdit, wantExplicitEdit: boolPtr(true)},

		// F3: git negation / git questions never allow git mutation.
		{name: "ko do not commit", request: "커밋하지 마", wantGitMutation: false, wantFileMutation: false, forbidMustEdit: true},
		{name: "en do not commit", request: "do not commit", wantGitMutation: false, wantFileMutation: false, forbidMustEdit: true},
		{name: "en what does git push do", request: "what does git push do?", wantGitMutation: false, wantFileMutation: false, forbidMustEdit: true},
		{name: "ko what is git push", request: "git push가 뭐야?", wantGitMutation: false, wantFileMutation: false, forbidMustEdit: true},

		// F10: no-change negation keeps file mutation off.
		{name: "en do not change just explain", request: "do not change the code, just explain", wantFileMutation: false, wantGitMutation: false, wantReadOnly: true, forbidMustEdit: true},
		{name: "ko do not touch code explain only", request: "코드는 건드리지 말고 설명만", wantFileMutation: false, wantGitMutation: false, wantReadOnly: true, forbidMustEdit: true},

		// F5: git-only English is a git intent, not a source must_edit.
		{name: "en commit staged and push", request: "commit the staged changes and push", wantFileMutation: false, wantGitMutation: true, forbidMustEdit: true, wantPrimary: RequestClassGit},

		// DO-NOT-BREAK invariants.
		{name: "ko fix this bug", request: "이 버그를 고쳐줘", wantFileMutation: true, wantPrimary: RequestClassEdit, wantExplicitEdit: boolPtr(true)},
		{name: "en fix the failing test", request: "fix the failing test", wantFileMutation: true, wantPrimary: RequestClassEdit, wantExplicitEdit: boolPtr(true)},
		{name: "ko implement", request: "구현해줘", wantFileMutation: true, wantPrimary: RequestClassEdit, wantExplicitEdit: boolPtr(true)},
		{name: "ko review only", request: "이 코드 리뷰만 해줘", wantFileMutation: false, wantReadOnly: true, wantPrimary: RequestClassReview, wantReviewClass: reviewRequestClassReviewOnly},
		{name: "ko commit please", request: "커밋해줘", wantGitMutation: true, wantFileMutation: false, wantPrimary: RequestClassGit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := buildRequestEnvelope(tc.request)
			env.Normalize()
			if tc.wantIntent != "" && env.Intent != tc.wantIntent {
				t.Errorf("Intent = %q, want %q", env.Intent, tc.wantIntent)
			}
			if env.AllowsFileMutation != tc.wantFileMutation {
				t.Errorf("AllowsFileMutation = %t, want %t", env.AllowsFileMutation, tc.wantFileMutation)
			}
			if env.AllowsGitMutation != tc.wantGitMutation {
				t.Errorf("AllowsGitMutation = %t, want %t", env.AllowsGitMutation, tc.wantGitMutation)
			}
			if tc.wantReadOnly && !env.ReadOnlyAnalysis {
				t.Errorf("ReadOnlyAnalysis = false, want true")
			}
			if tc.forbidMustEdit && env.Boundary == ActionBoundaryMustEdit {
				t.Errorf("Boundary = must_edit, want non-must_edit for read-only/git-only request")
			}
			if tc.wantPrimary != "" && env.PrimaryClass != tc.wantPrimary {
				t.Errorf("PrimaryClass = %q, want %q", env.PrimaryClass, tc.wantPrimary)
			}
			if tc.wantReviewClass != "" {
				if got := normalizeReviewRequestClass(env.ReviewRequestClass); got != tc.wantReviewClass {
					t.Errorf("ReviewRequestClass = %q, want %q", got, tc.wantReviewClass)
				}
			}
			if tc.wantExplicitEdit != nil && env.ExplicitEditRequest != *tc.wantExplicitEdit {
				t.Errorf("ExplicitEditRequest = %t, want %t", env.ExplicitEditRequest, *tc.wantExplicitEdit)
			}
		})
	}
}

// TestRequestHandlingSafetyFinalGateRecovery covers F6: a non-mutating envelope
// (PrimaryClass question, file mutation not allowed) that nonetheless produced
// changed files must drive the final gate to NeedsRecovery, never Ready. The
// unexpected workspace edit on a read-only turn is a recovery condition.
func TestRequestHandlingSafetyFinalGateRecovery(t *testing.T) {
	env := buildRequestEnvelope("explain the run loop")
	env.Normalize()
	if env.PrimaryClass != RequestClassQuestion {
		t.Fatalf("setup: PrimaryClass = %q, want question", env.PrimaryClass)
	}
	if env.AllowsFileMutation {
		t.Fatalf("setup: AllowsFileMutation = true, want false")
	}
	input := FinalGateInput{
		RequestEnvelope: env,
		ChangedFiles:    []string{"cmd/kernforge/foo.go"},
	}
	decision := DecideFinalGate(input)
	if decision.State != FinalGateNeedsRecovery {
		t.Errorf("State = %q, want %q", decision.State, FinalGateNeedsRecovery)
	}
	if decision.Ready {
		t.Errorf("Ready = true, want false for non-mutating envelope with changed files")
	}
}
