package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	priorReviewArtifactConsentUse  = "use"
	priorReviewArtifactConsentSkip = "skip"

	// priorReviewArtifactSessionStartMargin absorbs filesystem timestamp
	// granularity (on Windows an mtime can round to tens of ms below a high-res
	// wall clock). Only artifacts whose mtime predates the session start by more
	// than this margin are treated as prior-session, so a review artifact written
	// moments after the session began is never misclassified as prior and its
	// current-turn findings stay freely readable. A genuine prior session is many
	// seconds-to-hours older, so this margin does not lose real prior artifacts.
	priorReviewArtifactSessionStartMargin = 5 * time.Second
)

// pathIsReviewArtifact reports whether a read targets a file under the harness's
// .kernforge/reviews tree (per-run review reports, original_main_proposal.md,
// latest.json, ...).
func pathIsReviewArtifact(path string) bool {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"))
	norm = strings.TrimPrefix(norm, "./")
	if norm == "" {
		return false
	}
	return strings.HasPrefix(norm, ".kernforge/reviews/") ||
		strings.Contains(norm, "/.kernforge/reviews/")
}

// priorSessionReviewArtifactReadPaths returns the read_file targets in this tool
// batch that point at review artifacts produced by a PRIOR session -- ones whose
// file mtime predates this session's creation. Current-session review artifacts
// (for example the pre-write review just produced this turn) are written after
// the session started, so they are intentionally not gated. A path that cannot
// be stat-ed is not gated either: a missing/unreadable read fails on its own and
// must not be silently swallowed by the consent gate.
func priorSessionReviewArtifactReadPaths(root string, session *Session, toolCalls []ToolCall) []string {
	if session == nil || session.CreatedAt.IsZero() {
		return nil
	}
	batchPaths, ok := readFileBatchPaths(toolCalls)
	if !ok {
		return nil
	}
	var gated []string
	for _, p := range batchPaths {
		if !pathIsReviewArtifact(p) {
			continue
		}
		abs := p
		if !filepath.IsAbs(abs) && strings.TrimSpace(root) != "" {
			abs = filepath.Join(root, filepath.FromSlash(p))
		}
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if info.ModTime().Before(session.CreatedAt.Add(-priorReviewArtifactSessionStartMargin)) {
			gated = append(gated, p)
		}
	}
	return uniqueStrings(gated)
}

// priorReviewArtifactConsentSkipGuidance is the synthetic guidance injected when
// the user has declined (or non-interactive mode defaults to declining) use of
// prior-session review artifacts. It steers the model back to the current
// workspace so it stops fixating on stale cross-session findings.
func priorReviewArtifactConsentSkipGuidance(cfg Config) string {
	return localizedText(cfg,
		"Skipping prior-session review artifacts (.kernforge/reviews/...): they may be stale and were not produced by this session. Proceed using only the current workspace code and this session's context; do not re-read those artifacts.",
		"이전 세션의 리뷰 산출물(.kernforge/reviews/...)은 stale일 수 있고 이번 세션이 만든 것이 아니므로 사용하지 않습니다. 현재 워크스페이스 코드와 이번 세션 컨텍스트만으로 진행하고, 해당 산출물을 다시 읽지 마세요.")
}
