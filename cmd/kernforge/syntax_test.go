package main

import (
	"strings"
	"testing"
)

func TestExtractLanguageFromFence(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"go", "```go", "go"},
		{"go with space", "``` go", "go"},
		{"tilde python", "~~~python", "python"},
		{"cpp alias kept raw", "```c++", "c++"},
		{"indented fence", "    ```rust", "rust"},
		{"info string extra metadata", "```python title=example.py", "python"},
		{"longer fence", "````go", "go"},
		{"bare fence", "```", ""},
		{"closing fence", "~~~", ""},
		{"not a fence", "package main", ""},
		{"trailing punctuation", "```{go}", "go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractLanguageFromFence(tc.line); got != tc.want {
				t.Fatalf("extractLanguageFromFence(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestCanonicalLanguageAliases(t *testing.T) {
	cases := map[string]string{
		"Go":      "go",
		"golang":  "go",
		"C++":     "cpp",
		"cxx":     "cpp",
		"hpp":     "cpp",
		"py":      "python",
		"PYTHON3": "python",
		"":        "",
		"unknown": "unknown",
	}
	for in, want := range cases {
		if got := canonicalLanguage(in); got != want {
			t.Fatalf("canonicalLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHighlighterRegistryLookup(t *testing.T) {
	reg := defaultHighlighterRegistry
	for _, lang := range []string{"go", "golang", "c", "cpp", "c++", "python", "py"} {
		if reg.Lookup(lang) == nil {
			t.Fatalf("expected highlighter for %q", lang)
		}
	}
	for _, lang := range []string{"", "rust", "javascript", "haskell"} {
		if reg.Lookup(lang) != nil {
			t.Fatalf("expected no highlighter for %q", lang)
		}
	}
}

// testPaint emulates ui.paint so the highlighter output mirrors real ANSI.
func testPaint(code, text string) string {
	if text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func TestGoHighlighterColorsTokens(t *testing.T) {
	hl := defaultHighlighterRegistry.Lookup("go")
	if hl == nil {
		t.Fatal("missing go highlighter")
	}
	var state highlightState
	line := `func main() { x := 42 // note`
	got := hl.HighlightLine(line, &state, testPaint)

	if !strings.Contains(got, testPaint(syntaxKeywordCode, "func")) {
		t.Fatalf("expected keyword 'func' colored, got %q", got)
	}
	if !strings.Contains(got, testPaint(syntaxNumberCode, "42")) {
		t.Fatalf("expected number '42' colored, got %q", got)
	}
	if !strings.Contains(got, testPaint(syntaxCommentCode, "// note")) {
		t.Fatalf("expected line comment colored, got %q", got)
	}
	if ansiPattern.ReplaceAllString(got, "") != line {
		t.Fatalf("highlight altered visible text: %q -> %q", line, ansiPattern.ReplaceAllString(got, ""))
	}
}

func TestGoHighlighterTypesAndStrings(t *testing.T) {
	hl := defaultHighlighterRegistry.Lookup("go")
	var state highlightState
	line := `var s string = "hi there"`
	got := hl.HighlightLine(line, &state, testPaint)

	if !strings.Contains(got, testPaint(syntaxKeywordCode, "var")) {
		t.Fatalf("expected keyword 'var' colored, got %q", got)
	}
	if !strings.Contains(got, testPaint(syntaxTypeCode, "string")) {
		t.Fatalf("expected type 'string' colored, got %q", got)
	}
	if !strings.Contains(got, testPaint(syntaxStringCode, `"hi there"`)) {
		t.Fatalf("expected string literal colored as a whole, got %q", got)
	}
}

func TestPythonHighlighterColorsTokens(t *testing.T) {
	hl := defaultHighlighterRegistry.Lookup("python")
	if hl == nil {
		t.Fatal("missing python highlighter")
	}
	var state highlightState
	line := `def greet(name: str) -> None:  # hi`
	got := hl.HighlightLine(line, &state, testPaint)

	if !strings.Contains(got, testPaint(syntaxKeywordCode, "def")) {
		t.Fatalf("expected keyword 'def' colored, got %q", got)
	}
	if !strings.Contains(got, testPaint(syntaxTypeCode, "str")) {
		t.Fatalf("expected type 'str' colored, got %q", got)
	}
	if !strings.Contains(got, testPaint(syntaxKeywordCode, "None")) {
		t.Fatalf("expected keyword 'None' colored, got %q", got)
	}
	if !strings.Contains(got, testPaint(syntaxCommentCode, "# hi")) {
		t.Fatalf("expected '#' comment colored, got %q", got)
	}
	// Python must not treat "//" as a comment (it is floor division-ish, not a
	// line comment); a stray "//" should remain plain text.
	plain := hl.HighlightLine(`x = a // b`, &state, testPaint)
	if strings.Contains(plain, testPaint(syntaxCommentCode, "// b")) {
		t.Fatalf("python wrongly treated '//' as comment: %q", plain)
	}
}

func TestStringLiteralBoundaryCases(t *testing.T) {
	hl := defaultHighlighterRegistry.Lookup("go")
	var state highlightState

	// Escaped quote inside the string must not terminate it early.
	escaped := `"a\"b" tail`
	got := hl.HighlightLine(escaped, &state, testPaint)
	if !strings.Contains(got, testPaint(syntaxStringCode, `"a\"b"`)) {
		t.Fatalf("escaped quote ended string early: %q", got)
	}
	if ansiPattern.ReplaceAllString(got, "") != escaped {
		t.Fatalf("escaped string altered text: %q", got)
	}

	// Unterminated string consumes to end of line but keeps the text intact.
	unterminated := `s := "open`
	got2 := hl.HighlightLine(unterminated, &state, testPaint)
	if !strings.Contains(got2, testPaint(syntaxStringCode, `"open`)) {
		t.Fatalf("expected unterminated string colored to EOL, got %q", got2)
	}
	if ansiPattern.ReplaceAllString(got2, "") != unterminated {
		t.Fatalf("unterminated string altered text: %q", got2)
	}

	// A digit inside an identifier must not be lexed as a number.
	ident := `value42 := f(x1)`
	got3 := hl.HighlightLine(ident, &state, testPaint)
	if strings.Contains(got3, testPaint(syntaxNumberCode, "42")) {
		t.Fatalf("digit inside identifier wrongly colored as number: %q", got3)
	}
}

func TestBlockCommentSpansLines(t *testing.T) {
	hl := defaultHighlighterRegistry.Lookup("cpp")
	var state highlightState

	open := hl.HighlightLine(`int x; /* start`, &state, testPaint)
	if !state.inBlockComment {
		t.Fatalf("expected block comment to stay open after %q", open)
	}
	if !strings.Contains(open, testPaint(syntaxCommentCode, "/* start")) {
		t.Fatalf("expected open block comment colored, got %q", open)
	}

	mid := hl.HighlightLine(`still comment`, &state, testPaint)
	if !state.inBlockComment {
		t.Fatalf("expected block comment still open on mid line %q", mid)
	}
	if !strings.Contains(mid, testPaint(syntaxCommentCode, "still comment")) {
		t.Fatalf("expected mid block-comment line fully colored, got %q", mid)
	}

	closeLine := hl.HighlightLine(`end */ int y;`, &state, testPaint)
	if state.inBlockComment {
		t.Fatalf("expected block comment closed after %q", closeLine)
	}
	if !strings.Contains(closeLine, testPaint(syntaxCommentCode, "end */")) {
		t.Fatalf("expected closing block-comment segment colored, got %q", closeLine)
	}
	if !strings.Contains(closeLine, testPaint(syntaxTypeCode, "int")) {
		t.Fatalf("expected code after close colored, got %q", closeLine)
	}
}

func TestUnknownLanguageFallsBackToMonochrome(t *testing.T) {
	ui := UI{color: true}
	ctx := assistantRenderContext{inFence: true, language: "rust"}
	line := `fn main() {}`
	got := ui.renderAssistantLine(assistantLineCode, line, &ctx)

	// Unknown language must render exactly as the flat assistantCode tone.
	if got != ui.assistantCode(line) {
		t.Fatalf("unknown language did not fall back to flat tone: got %q want %q", got, ui.assistantCode(line))
	}
}

func TestKnownLanguageHighlightsInsideFence(t *testing.T) {
	ui := UI{color: true}
	ctx := assistantRenderContext{inFence: true, language: "go"}
	got := ui.renderAssistantLine(assistantLineCode, "func f() {}", &ctx)
	if !strings.Contains(got, testPaint(syntaxKeywordCode, "func")) {
		t.Fatalf("expected go keyword colored inside fence, got %q", got)
	}
}

func TestCodeOutsideFenceUsesFlatTone(t *testing.T) {
	ui := UI{color: true}
	ctx := assistantRenderContext{inFence: false, language: "go"}
	line := "    indented code"
	got := ui.renderAssistantLine(assistantLineCode, line, &ctx)
	if got != ui.assistantCode(line) {
		t.Fatalf("indented (non-fenced) code should use flat tone, got %q", got)
	}
}

func TestInlineMarkdownEmphasis(t *testing.T) {
	ui := UI{color: true}
	got := ui.renderProseLine("a **bold** and *em* and `code` end")

	if !strings.Contains(got, testPaint(combineCodes(proseToneCode, "1"), "bold")) {
		t.Fatalf("expected bold span, got %q", got)
	}
	if !strings.Contains(got, testPaint(combineCodes(proseToneCode, "3"), "em")) {
		t.Fatalf("expected italic span, got %q", got)
	}
	if !strings.Contains(got, testPaint(combineCodes(proseToneCode, syntaxStringCode), "code")) {
		t.Fatalf("expected inline code span, got %q", got)
	}
	// Markers must be consumed; the visible text drops them.
	visible := ansiPattern.ReplaceAllString(got, "")
	if visible != "a bold and em and code end" {
		t.Fatalf("inline markdown left stray markers: %q", visible)
	}
}

func TestInlineMarkdownConservativeBoundaries(t *testing.T) {
	ui := UI{color: true}
	// Arithmetic-looking stars with surrounding spaces must stay literal.
	got := ui.renderProseLine("a * b * c")
	if ansiPattern.ReplaceAllString(got, "") != "a * b * c" {
		t.Fatalf("expected arithmetic stars preserved, got %q", ansiPattern.ReplaceAllString(got, ""))
	}
	// No-color mode keeps the raw markers untouched.
	plain := UI{color: false}.renderProseLine("**keep** `raw`")
	if plain != "**keep** `raw`" {
		t.Fatalf("no-color prose should keep markers, got %q", plain)
	}
}

func TestHighlightedOutputVisibleWidthMatchesRaw(t *testing.T) {
	ui := UI{color: true}
	cases := []struct {
		lang string
		line string
	}{
		{"go", `func f(x int) string { return "ok" } // c`},
		{"python", `def f(x: int) -> str:  # 한국어 주석`},
		{"cpp", `const char* s = "hi"; /* 注釈 */`},
	}
	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			ctx := assistantRenderContext{inFence: true, language: tc.lang}
			highlighted := ui.renderAssistantLine(assistantLineCode, tc.line, &ctx)
			if got, want := visibleLen(highlighted), visibleLen(tc.line); got != want {
				t.Fatalf("visibleLen mismatch for %s: highlighted=%d raw=%d (%q)", tc.lang, got, want, highlighted)
			}
			if stripped := ansiPattern.ReplaceAllString(highlighted, ""); stripped != tc.line {
				t.Fatalf("highlight altered text for %s: %q != %q", tc.lang, stripped, tc.line)
			}
		})
	}

	// Prose with inline markdown and CJK must also preserve visible width.
	prose := "보고 **굵게** 그리고 `코드` 끝"
	rendered := ui.renderProseLine(prose)
	rawVisible := visibleLen(ansiPattern.ReplaceAllString(rendered, ""))
	if got := visibleLen(rendered); got != rawVisible {
		t.Fatalf("prose visibleLen mismatch: rendered=%d raw=%d", got, rawVisible)
	}
}

func TestStreamingThreadsFenceLanguage(t *testing.T) {
	ui := UI{color: true}
	var ctx assistantRenderContext
	prefix := ""

	out := ui.renderAssistantStreamDelta("```go\n", &ctx, &prefix)
	if !ctx.inFence || ctx.language != "go" {
		t.Fatalf("expected fence open with go language, got inFence=%v language=%q", ctx.inFence, ctx.language)
	}
	out += ui.renderAssistantStreamDelta("func f() {}\n", &ctx, &prefix)
	if !strings.Contains(out, testPaint(syntaxKeywordCode, "func")) {
		t.Fatalf("expected streamed go code highlighted, got %q", out)
	}
	out += ui.renderAssistantStreamDelta("```\n", &ctx, &prefix)
	if ctx.inFence || ctx.language != "" {
		t.Fatalf("expected fence closed and language cleared, got inFence=%v language=%q", ctx.inFence, ctx.language)
	}
}

func TestStreamingSplitLineKeepsBlockCommentState(t *testing.T) {
	ui := UI{color: true}
	ctx := assistantRenderContext{inFence: true, language: "cpp"}
	prefix := ""

	// First delta is a partial code line opening a block comment; it must not
	// commit the block-comment state because the line is not yet terminated.
	ui.renderAssistantStreamDelta("int x; /* op", &ctx, &prefix)
	if ctx.blockComment.inBlockComment {
		t.Fatalf("partial line wrongly committed block-comment state")
	}
	// Completing the line must derive the canonical state from the whole line.
	ui.renderAssistantStreamDelta("en\n", &ctx, &prefix)
	if !ctx.blockComment.inBlockComment {
		t.Fatalf("expected block-comment state open after full split line")
	}
}
