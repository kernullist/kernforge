package main

import "testing"

// TestRequestDocVsEditClassification locks in the document-vs-edit routing fix.
// "analyze this code and write it up as a document" must author a document, not
// route to a code-edit / review-then-modify lifecycle. The do-not-break cases
// keep their existing mutating / read-only behavior.
func TestRequestDocVsEditClassification(t *testing.T) {
	cases := []struct {
		name             string
		request          string
		wantPrimary      RequestClass
		wantReviewClass  string
		wantExplicitEdit bool
		wantMutating     bool
		forbidMustEdit   bool
	}{
		{
			name:             "korean analyze and write document",
			request:          "이 코드를 분석해서 문서로 작성해줘",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			name:             "korean driver doc describing fixes",
			request:          "이 드라이버 코드를 분석해서 수정이 필요한 부분을 문서로 정리해줘",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			name:             "english analyze and document fixes",
			request:          "analyze this code and write a document describing what needs to be fixed",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			name:             "korean refactor plan report",
			request:          "이 코드 분석하고 리팩터링 방안을 보고서로 작성해줘",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			name:             "korean save analysis to md file",
			request:          "이 코드 분석 결과를 ANALYSIS.md 파일로 저장해줘",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			// Obligation form: "고쳐야 할" describes document content, not a command.
			name:             "korean obligation form fixes to document",
			request:          "이 코드에서 고쳐야 할 점들을 문서로 정리해줘",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			name:             "korean obligation form fixes to report",
			request:          "이 드라이버에서 수정해야 할 부분을 보고서로 만들어줘",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			name:             "korean recommendation included in report",
			request:          "이 드라이버 분석 보고서에 수정 권고사항 포함해서 작성해줘",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			// Verb elided; the explicit "markdown으로" sink carries the intent.
			name:             "korean direction to markdown sink no verb",
			request:          "이 코드 분석해서 개선 방향을 markdown으로",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			name:             "english write up issues as report",
			request:          "write up the issues you find in this driver as a report",
			wantPrimary:      RequestClassDocument,
			wantReviewClass:  reviewRequestClassDocumentArtifact,
			wantExplicitEdit: false,
			wantMutating:     true,
			forbidMustEdit:   true,
		},
		{
			name:             "korean fix this bug stays edit",
			request:          "이 버그를 고쳐줘",
			wantPrimary:      RequestClassEdit,
			wantExplicitEdit: true,
			wantMutating:     true,
		},
		{
			name:            "korean review only stays read only",
			request:         "이 코드 리뷰만 해줘",
			wantPrimary:     RequestClassReview,
			wantReviewClass: reviewRequestClassReviewOnly,
			wantMutating:    false,
		},
		{
			name:             "korean genuine review then fix confirmed bugs",
			request:          "이 코드 리뷰하고 확인된 버그만 고쳐줘",
			wantReviewClass:  reviewRequestClassReviewThenModify,
			wantExplicitEdit: true,
			wantMutating:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := buildRequestEnvelope(tc.request)
			env.Normalize()
			if tc.wantPrimary != "" && env.PrimaryClass != tc.wantPrimary {
				t.Errorf("PrimaryClass = %q, want %q", env.PrimaryClass, tc.wantPrimary)
			}
			if tc.wantReviewClass != "" {
				if got := normalizeReviewRequestClass(env.ReviewRequestClass); got != tc.wantReviewClass {
					t.Errorf("ReviewRequestClass = %q, want %q", got, tc.wantReviewClass)
				}
			}
			if env.ExplicitEditRequest != tc.wantExplicitEdit {
				t.Errorf("ExplicitEditRequest = %t, want %t", env.ExplicitEditRequest, tc.wantExplicitEdit)
			}
			if env.AllowsFileMutation != tc.wantMutating {
				t.Errorf("AllowsFileMutation = %t, want %t", env.AllowsFileMutation, tc.wantMutating)
			}
			if tc.forbidMustEdit && env.Boundary == ActionBoundaryMustEdit {
				t.Errorf("Boundary = must_edit, want non-must_edit for document authoring")
			}
		})
	}
}

func TestRequestHasSourceModificationIntentDocContentGuard(t *testing.T) {
	if requestHasSourceModificationIntent("이 드라이버 코드를 분석해서 수정이 필요한 부분을 문서로 정리해줘", nil) {
		t.Errorf("doc-content modification phrasing must not be source-modification intent")
	}
	if !requestHasSourceModificationIntent("이 버그를 고쳐줘", nil) {
		t.Errorf("imperative fix command must stay source-modification intent")
	}
}
