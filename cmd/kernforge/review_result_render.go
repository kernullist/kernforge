package main

import (
	"fmt"
	"strings"
)

// review_result_render.go centralizes the refined, plain-text (no-ANSI) box-card
// rendering shared by every review reply surface (pre-write final summary,
// natural-language review reply, non-blocking pre-fix reply). The rendered
// strings are reused on the terminal, in MCP responses, and in saved .md
// artifacts, so they must never embed ANSI color codes. The terminal paints
// the assistant block separately; the refinement here is structure only.

const (
	// reviewResultBoxMaxInnerWidth caps the inner content width of the header
	// box so a very long Korean summary does not blow the card out to the full
	// terminal width. Korean glyphs are double-width, so this is a visibleLen
	// budget, not a byte budget.
	reviewResultBoxMaxInnerWidth = 76

	// review box-drawing and severity glyphs. These are plain Unicode glyphs,
	// not ANSI escapes, so they are safe on every surface.
	reviewBoxTopLeft     = "╭" // rounded top-left corner
	reviewBoxTopRight    = "╮" // rounded top-right corner
	reviewBoxBottomLeft  = "╰" // rounded bottom-left corner
	reviewBoxBottomRight = "╯" // rounded bottom-right corner
	reviewBoxHorizontal  = "─" // horizontal line
	reviewBoxVertical    = "│" // vertical line

	reviewSymbolBlocker  = "✗" // heavy ballot X
	reviewSymbolWarning  = "⚠" // warning sign
	reviewSymbolNote     = "●" // black circle
	reviewSymbolApproved = "✓" // check mark
)

// reviewVerdictSymbol maps a review verdict to its header badge symbol.
func reviewVerdictSymbol(verdict string) string {
	switch strings.TrimSpace(strings.ToLower(verdict)) {
	case reviewVerdictApproved:
		return reviewSymbolApproved
	case reviewVerdictApprovedWithWarnings:
		return reviewSymbolWarning
	case reviewVerdictNeedsRevision, reviewVerdictBlocked, reviewVerdictInsufficientEvidence:
		return reviewSymbolBlocker
	default:
		return reviewSymbolNote
	}
}

// reviewSeveritySymbol maps a finding severity to its card header symbol.
func reviewSeveritySymbol(severity string) string {
	switch strings.TrimSpace(strings.ToLower(severity)) {
	case reviewSeverityBlocker:
		return reviewSymbolBlocker
	case reviewSeverityHigh, reviewSeverityMedium:
		return reviewSymbolWarning
	case reviewVerdictApproved:
		return reviewSymbolApproved
	default:
		return reviewSymbolNote
	}
}

// reviewResultBox renders a rounded box with a centered title and the given
// content lines, width-fitted via visibleLen so Korean double-width text
// aligns. The box grows to the widest provided line; callers are responsible
// for capping any line that should be truncated (see reviewHeaderBoxLines).
// The box is returned as a slice of plain text lines (no trailing newline,
// no ANSI).
func reviewResultBox(title string, lines []string) []string {
	title = reviewVisibleInlineText(title)
	inner := 0
	if titleWidth := visibleLen(title); titleWidth > inner {
		inner = titleWidth
	}
	prepared := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " ")
		prepared = append(prepared, line)
		if width := visibleLen(line); width > inner {
			inner = width
		}
	}
	out := make([]string, 0, len(prepared)+2)
	out = append(out, reviewResultBoxTopBorder(title, inner))
	for _, line := range prepared {
		pad := inner - visibleLen(line)
		if pad < 0 {
			pad = 0
		}
		out = append(out, reviewBoxVertical+" "+line+strings.Repeat(" ", pad)+" "+reviewBoxVertical)
	}
	out = append(out, reviewBoxBottomLeft+strings.Repeat(reviewBoxHorizontal, inner+2)+reviewBoxBottomRight)
	return out
}

// reviewResultBoxTopBorder builds the top border with the title centered in the
// horizontal rule, sized to the same inner width (+2 padding) as the body rows.
func reviewResultBoxTopBorder(title string, inner int) string {
	total := inner + 2
	if strings.TrimSpace(title) == "" {
		return reviewBoxTopLeft + strings.Repeat(reviewBoxHorizontal, total) + reviewBoxTopRight
	}
	label := " " + title + " "
	labelWidth := visibleLen(label)
	if labelWidth >= total {
		return reviewBoxTopLeft + label + reviewBoxTopRight
	}
	remaining := total - labelWidth
	left := remaining / 2
	right := remaining - left
	return reviewBoxTopLeft +
		strings.Repeat(reviewBoxHorizontal, left) +
		label +
		strings.Repeat(reviewBoxHorizontal, right) +
		reviewBoxTopRight
}

// reviewHeaderBoxLines composes the refined header box: a verdict badge line
// (symbol + humanized verdict + blocker/warning counts + the next/preview
// decision) and a summary line. nextDecision is the already-localized
// "preview/next" sentence fragment for this surface. The returned lines are
// plain text (no ANSI) and ready to join with "\n".
func reviewHeaderBoxLines(verdict string, blockers int, warnings int, summary string, nextDecision string, korean bool) []string {
	symbol := reviewVerdictSymbol(verdict)
	verdictText := humanizeReviewVerdict(verdict, korean)
	var badge strings.Builder
	badge.WriteString(symbol)
	badge.WriteString(" ")
	badge.WriteString(verdictText)
	if korean {
		fmt.Fprintf(&badge, "    차단 %d", blockers)
		fmt.Fprintf(&badge, "   경고 %d", warnings)
	} else {
		fmt.Fprintf(&badge, "    blockers %d", blockers)
		fmt.Fprintf(&badge, "   warnings %d", warnings)
	}
	if next := reviewVisibleInlineText(nextDecision); next != "" {
		badge.WriteString("   ")
		badge.WriteString(next)
	}
	badgeLine := badge.String()
	lines := []string{badgeLine}
	if summary = reviewVisibleInlineText(summary); summary != "" {
		summaryPrefix := "Summary: "
		if korean {
			summaryPrefix = "요약: "
		}
		// The badge line is never truncated (it carries the verdict and the
		// next/preview decision). Only the summary line is width-capped: it is
		// fit to whichever is wider, the badge line or the configured cap, so
		// the box never grows past the badge for the summary alone.
		summaryCap := reviewResultBoxMaxInnerWidth
		if badgeWidth := visibleLen(badgeLine); badgeWidth > summaryCap {
			summaryCap = badgeWidth
		}
		summaryLine := summaryPrefix + summary
		if visibleLen(summaryLine) > summaryCap {
			summaryLine = truncateDisplayText(summaryLine, summaryCap)
		}
		lines = append(lines, summaryLine)
	}
	title := "Review result"
	if korean {
		title = "검토 결과"
	}
	return reviewResultBox(title, lines)
}

// writeReviewHeaderBox appends the refined header box to the builder.
func writeReviewHeaderBox(b *strings.Builder, verdict string, blockers int, warnings int, summary string, nextDecision string, korean bool) {
	for _, line := range reviewHeaderBoxLines(verdict, blockers, warnings, summary, nextDecision, korean) {
		b.WriteString(line)
		b.WriteString("\n")
	}
}
