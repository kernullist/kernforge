package main

import (
	"strings"
)

// SyntaxHighlighter colors a single line of source code that is known to sit
// inside a fenced code block of a particular language. Implementations must be
// single-pass and must never alter the visible width of the input: every byte
// of the original text has to survive verbatim, with only ANSI SGR escape
// sequences (handled by ansiPattern / visibleLen) inserted around tokens. This
// keeps the terminal width math and CJK width handling unaffected.
type SyntaxHighlighter interface {
	// HighlightLine returns text with ANSI coloring applied. paint must be the
	// UI.paint-style helper so coloring respects the no-color mode and uses the
	// shared 256-color palette. The block-comment depth is threaded through the
	// state pointer so multi-line /* ... */ comments stay colored across lines.
	HighlightLine(text string, state *highlightState, paint func(code, text string) string) string
}

// highlightState carries cross-line lexer context (currently only whether we
// are inside a C/C++/Go block comment). A zero value is a valid fresh state.
type highlightState struct {
	inBlockComment bool
}

// syntax palette codes. These mirror the existing ui.go ANSI-256 tones so the
// highlighter stays inside the established palette; the helpers in ui.go expose
// them as named methods, but the lexers below need the raw codes to pass into
// the injected paint function.
const (
	syntaxKeywordCode = "38;5;212" // pink: keywords / control flow
	syntaxTypeCode    = "38;5;117" // light blue: built-in types
	syntaxStringCode  = "38;5;150" // soft green: string / char literals
	syntaxCommentCode = "38;5;245" // dim gray: comments
	syntaxNumberCode  = "38;5;215" // amber: numeric literals
)

// HighlighterRegistry dispatches highlighting by canonical language name. A nil
// or missing entry means the caller falls back to the flat assistantCode tone.
type HighlighterRegistry struct {
	byLanguage map[string]SyntaxHighlighter
}

// defaultHighlighterRegistry is the registry used by the assistant renderer. It
// is built once at init so lookups are allocation-free.
var defaultHighlighterRegistry = newDefaultHighlighterRegistry()

func newDefaultHighlighterRegistry() *HighlighterRegistry {
	reg := &HighlighterRegistry{byLanguage: make(map[string]SyntaxHighlighter)}
	goHL := &keywordHighlighter{
		keywords:      goKeywords,
		types:         goTypes,
		lineComment:   "//",
		blockComments: true,
		rawStrings:    true,
	}
	cHL := &keywordHighlighter{
		keywords:      cKeywords,
		types:         cTypes,
		lineComment:   "//",
		blockComments: true,
	}
	pyHL := &keywordHighlighter{
		keywords:      pythonKeywords,
		types:         pythonTypes,
		lineComment:   "#",
		blockComments: false,
	}
	reg.register("go", goHL)
	reg.register("c", cHL)
	reg.register("cpp", cHL)
	reg.register("python", pyHL)
	return reg
}

func (r *HighlighterRegistry) register(language string, hl SyntaxHighlighter) {
	if r == nil || hl == nil {
		return
	}
	key := canonicalLanguage(language)
	if key == "" {
		return
	}
	r.byLanguage[key] = hl
}

// Lookup resolves a (possibly aliased) language token to a highlighter. It
// returns nil when the language is unknown so the caller can fall back to the
// monochrome code tone.
func (r *HighlighterRegistry) Lookup(language string) SyntaxHighlighter {
	if r == nil {
		return nil
	}
	key := canonicalLanguage(language)
	if key == "" {
		return nil
	}
	return r.byLanguage[key]
}

// canonicalLanguage normalizes a fence language token (case, common aliases)
// to a registry key. Unknown tokens are lowercased and returned as-is so a
// missing highlighter degrades to the flat tone rather than erroring.
func canonicalLanguage(language string) string {
	lang := strings.ToLower(strings.TrimSpace(language))
	switch lang {
	case "":
		return ""
	case "golang":
		return "go"
	case "c++", "cc", "cxx", "hpp", "hxx", "h", "cppm":
		return "cpp"
	case "py", "python3", "py3":
		return "python"
	default:
		return lang
	}
}

// extractLanguageFromFence parses the language token that follows an opening
// triple-backtick (or triple-tilde) fence, e.g. "```go" -> "go", "~~~ python"
// -> "python". It returns an empty string for a bare fence, a closing fence, or
// a non-fence line. The returned token is the raw (un-canonicalized) word so
// callers can canonicalize once at lookup time.
func extractLanguageFromFence(line string) string {
	trimmed := strings.TrimSpace(line)
	var marker string
	switch {
	case strings.HasPrefix(trimmed, "```"):
		marker = "```"
	case strings.HasPrefix(trimmed, "~~~"):
		marker = "~~~"
	default:
		return ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
	// Skip any extra leading fence characters (e.g. "````go") so longer fences
	// still parse their language.
	rest = strings.TrimLeft(rest, string(marker[0]))
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	// The info string is "<lang> <optional metadata>"; take the first token and
	// trim any trailing punctuation that is never part of a language name.
	fields := strings.Fields(rest)
	token := fields[0]
	token = strings.Trim(token, "{}.,")
	return token
}

// keywordHighlighter is a minimal single-pass lexer parameterized by the
// per-language keyword/type sets and comment syntax. It is intentionally not a
// full parser: it recognizes line comments, block comments (optional), string
// and char literals, numbers, and identifier words that match a keyword/type
// set. Everything else is emitted verbatim.
type keywordHighlighter struct {
	keywords      map[string]struct{}
	types         map[string]struct{}
	lineComment   string // e.g. "//" or "#"; empty disables line comments
	blockComments bool   // recognize /* ... */ across lines
	rawStrings    bool   // recognize Go raw string literals delimited by backticks
}

func (h *keywordHighlighter) HighlightLine(text string, state *highlightState, paint func(code, text string) string) string {
	if text == "" {
		return text
	}
	if state == nil {
		state = &highlightState{}
	}

	var out strings.Builder
	out.Grow(len(text) + 16)
	runes := []rune(text)
	i := 0
	n := len(runes)

	// Continuation of a block comment opened on a previous line.
	if state.inBlockComment {
		end := indexBlockCommentEnd(runes, 0)
		if end < 0 {
			out.WriteString(paint(syntaxCommentCode, string(runes)))
			return out.String()
		}
		out.WriteString(paint(syntaxCommentCode, string(runes[:end])))
		state.inBlockComment = false
		i = end
	}

	for i < n {
		c := runes[i]

		// Line comment.
		if h.lineComment != "" && matchAt(runes, i, h.lineComment) {
			out.WriteString(paint(syntaxCommentCode, string(runes[i:])))
			i = n
			break
		}

		// Block comment start.
		if h.blockComments && matchAt(runes, i, "/*") {
			end := indexBlockCommentEnd(runes, i+2)
			if end < 0 {
				out.WriteString(paint(syntaxCommentCode, string(runes[i:])))
				state.inBlockComment = true
				i = n
				break
			}
			out.WriteString(paint(syntaxCommentCode, string(runes[i:end])))
			i = end
			continue
		}

		// String / char literals.
		if c == '"' || c == '\'' {
			end := scanQuoted(runes, i, c)
			out.WriteString(paint(syntaxStringCode, string(runes[i:end])))
			i = end
			continue
		}
		if h.rawStrings && c == '`' {
			end := scanRaw(runes, i)
			out.WriteString(paint(syntaxStringCode, string(runes[i:end])))
			i = end
			continue
		}

		// Numeric literal (must start at a token boundary).
		if isDigitRune(c) && (i == 0 || !isIdentRune(runes[i-1])) {
			end := scanNumber(runes, i)
			out.WriteString(paint(syntaxNumberCode, string(runes[i:end])))
			i = end
			continue
		}

		// Identifier / keyword / type.
		if isIdentStartRune(c) {
			j := i + 1
			for j < n && isIdentRune(runes[j]) {
				j++
			}
			word := string(runes[i:j])
			if _, ok := h.keywords[word]; ok {
				out.WriteString(paint(syntaxKeywordCode, word))
			} else if _, ok := h.types[word]; ok {
				out.WriteString(paint(syntaxTypeCode, word))
			} else {
				out.WriteString(word)
			}
			i = j
			continue
		}

		out.WriteRune(c)
		i++
	}
	return out.String()
}

// matchAt reports whether the rune slice contains literal at index i.
func matchAt(runes []rune, i int, literal string) bool {
	lr := []rune(literal)
	if i+len(lr) > len(runes) {
		return false
	}
	for k := 0; k < len(lr); k++ {
		if runes[i+k] != lr[k] {
			return false
		}
	}
	return true
}

// indexBlockCommentEnd returns the index just past the closing "*/" starting
// the search at from, or -1 if the comment does not close on this line.
func indexBlockCommentEnd(runes []rune, from int) int {
	for i := from; i+1 < len(runes); i++ {
		if runes[i] == '*' && runes[i+1] == '/' {
			return i + 2
		}
	}
	return -1
}

// scanQuoted returns the index just past a string or char literal opened by the
// quote rune at start. Backslash escapes are honored; an unterminated literal
// consumes to end of line so coloring still covers the visible text.
func scanQuoted(runes []rune, start int, quote rune) int {
	n := len(runes)
	i := start + 1
	for i < n {
		c := runes[i]
		if c == '\\' {
			i += 2
			continue
		}
		if c == quote {
			return i + 1
		}
		i++
	}
	return n
}

// scanRaw returns the index just past a Go raw string literal opened by a
// backtick at start. Raw strings have no escapes and end at the next backtick.
func scanRaw(runes []rune, start int) int {
	n := len(runes)
	for i := start + 1; i < n; i++ {
		if runes[i] == '`' {
			return i + 1
		}
	}
	return n
}

// scanNumber returns the index just past a numeric literal starting at start.
// It accepts hex/binary/octal prefixes, digit separators, a decimal point, an
// exponent, and a trailing type suffix; this is permissive on purpose since the
// goal is coloring, not validation.
func scanNumber(runes []rune, start int) int {
	n := len(runes)
	i := start
	if runes[i] == '0' && i+1 < n && (runes[i+1] == 'x' || runes[i+1] == 'X' || runes[i+1] == 'b' || runes[i+1] == 'B' || runes[i+1] == 'o' || runes[i+1] == 'O') {
		i += 2
	}
	for i < n {
		c := runes[i]
		if isHexDigitRune(c) || c == '.' || c == '_' {
			i++
			continue
		}
		if (c == 'e' || c == 'E' || c == 'p' || c == 'P') && i+1 < n && (runes[i+1] == '+' || runes[i+1] == '-') {
			i += 2
			continue
		}
		break
	}
	// Trailing single-letter type/imaginary suffix (e.g. 1f, 3L, 2i).
	if i < n && isIdentStartRune(runes[i]) && !isIdentStartRune(runes[start]) {
		i++
	}
	return i
}

func isDigitRune(r rune) bool {
	return r >= '0' && r <= '9'
}

func isHexDigitRune(r rune) bool {
	return isDigitRune(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isIdentStartRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isIdentRune(r rune) bool {
	return isIdentStartRune(r) || isDigitRune(r)
}

// renderInlineMarkdown applies conservative inline emphasis to a prose line:
// **bold**, *italic*, and inline `code`. It returns text with ANSI styling
// inserted around the spans and the surrounding markers removed. When no marker
// pairs are present the input is returned with only the base color applied.
//
// baseCode is the prose tone (e.g. mint); plain runs are painted with it and it
// is re-asserted after every styled span so a span's reset never strips the
// surrounding prose color. Styled spans combine baseCode with the emphasis code
// in a single SGR sequence, so each rendered run is one self-contained
// "\x1b[...m...\x1b[0m" pair that ansiPattern strips cleanly, keeping
// visibleLen() and the terminal width math correct.
//
// paint applies the shared SGR styling; bold uses "1", italic uses "3", inline
// code reuses the syntaxStringCode tone so it stands apart from prose.
func renderInlineMarkdown(text string, baseCode string, paint func(code, text string) string) string {
	if text == "" {
		return text
	}
	if !strings.ContainsAny(text, "*`") {
		return paint(baseCode, text)
	}

	var out strings.Builder
	out.Grow(len(text) + 32)
	runes := []rune(text)
	n := len(runes)
	i := 0
	// plainStart marks the beginning of the current unstyled run; it is flushed
	// (painted with the base tone) whenever a styled span begins or the line ends.
	plainStart := 0
	flushPlain := func(upto int) {
		if upto > plainStart {
			out.WriteString(paint(baseCode, string(runes[plainStart:upto])))
		}
	}
	emit := func(code, inner string) {
		out.WriteString(paint(combineCodes(baseCode, code), inner))
	}
	for i < n {
		c := runes[i]
		switch {
		case c == '`':
			if end := matchInlineSpan(runes, i, "`"); end > 0 {
				flushPlain(i)
				emit(syntaxStringCode, string(runes[i+1:end]))
				i = end + 1
				plainStart = i
				continue
			}
		case c == '*' && i+1 < n && runes[i+1] == '*':
			if end := matchInlineSpan(runes, i, "**"); end > 0 {
				flushPlain(i)
				emit("1", string(runes[i+2:end]))
				i = end + 2
				plainStart = i
				continue
			}
		case c == '*':
			if end := matchInlineSpan(runes, i, "*"); end > 0 {
				flushPlain(i)
				emit("3", string(runes[i+1:end]))
				i = end + 1
				plainStart = i
				continue
			}
		}
		i++
	}
	flushPlain(n)
	return out.String()
}

// combineCodes joins a base SGR code with an emphasis code so a styled span
// keeps the prose tone while adding bold/italic/etc. An empty base yields the
// emphasis code alone.
func combineCodes(base, code string) string {
	if base == "" {
		return code
	}
	if code == "" {
		return base
	}
	return base + ";" + code
}

// matchInlineSpan finds the closing marker for an emphasis span that opens with
// marker at index i. It returns the index of the first rune of the closing
// marker, or -1 if there is no non-empty, well-formed closing run on this line.
// The span content must be non-empty and must not start or end with a space
// (so "a * b * c" arithmetic is left alone, matching common Markdown rules).
func matchInlineSpan(runes []rune, i int, marker string) int {
	mr := []rune(marker)
	mlen := len(mr)
	contentStart := i + mlen
	n := len(runes)
	if contentStart >= n {
		return -1
	}
	// Reject a leading space inside the span (e.g. "* not italic").
	if runes[contentStart] == ' ' || runes[contentStart] == '\t' {
		return -1
	}
	for j := contentStart; j < n; j++ {
		if matchAt(runes, j, marker) {
			// For single "*" do not treat a "**" run as a single-star close.
			if marker == "*" && j+1 < n && runes[j+1] == '*' {
				continue
			}
			if j == contentStart {
				return -1
			}
			// Reject a trailing space just before the closing marker.
			if runes[j-1] == ' ' || runes[j-1] == '\t' {
				continue
			}
			return j
		}
	}
	return -1
}

// goKeywords / goTypes / cKeywords / cTypes / pythonKeywords / pythonTypes are
// the per-language token sets used by the lexers. They are intentionally small
// and cover the common surface; unknown identifiers fall through uncolored.
var goKeywords = stringSet(
	"break", "case", "chan", "const", "continue", "default", "defer", "else",
	"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
	"map", "package", "range", "return", "select", "struct", "switch", "type",
	"var", "nil", "true", "false", "iota",
)

var goTypes = stringSet(
	"bool", "byte", "complex64", "complex128", "error", "float32", "float64",
	"int", "int8", "int16", "int32", "int64", "rune", "string", "uint",
	"uint8", "uint16", "uint32", "uint64", "uintptr", "any",
)

var cKeywords = stringSet(
	"alignas", "alignof", "and", "asm", "auto", "break", "case", "catch",
	"class", "const", "constexpr", "continue", "default", "delete", "do",
	"else", "enum", "explicit", "export", "extern", "false", "for", "friend",
	"goto", "if", "inline", "namespace", "new", "noexcept", "nullptr",
	"operator", "or", "override", "private", "protected", "public", "register",
	"return", "sizeof", "static", "static_cast", "struct", "switch", "template",
	"this", "throw", "true", "try", "typedef", "typename", "union", "using",
	"virtual", "volatile", "while",
)

var cTypes = stringSet(
	"bool", "char", "char16_t", "char32_t", "double", "float", "int", "long",
	"short", "signed", "size_t", "ssize_t", "unsigned", "void", "wchar_t",
	"int8_t", "int16_t", "int32_t", "int64_t", "uint8_t", "uint16_t",
	"uint32_t", "uint64_t", "intptr_t", "uintptr_t", "auto",
)

var pythonKeywords = stringSet(
	"and", "as", "assert", "async", "await", "break", "class", "continue",
	"def", "del", "elif", "else", "except", "finally", "for", "from", "global",
	"if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass",
	"raise", "return", "try", "while", "with", "yield", "None", "True",
	"False",
)

var pythonTypes = stringSet(
	"bool", "bytes", "complex", "dict", "float", "frozenset", "int", "list",
	"object", "set", "str", "tuple", "type",
)

func stringSet(items ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		set[item] = struct{}{}
	}
	return set
}
