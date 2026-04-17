package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type sourceAnchorExtraction struct {
	Symbols     []SymbolRecord
	Occurrences []SymbolOccurrence
	Calls       []CallEdge
	Overlays    []OverlayEdge
	Builds      []BuildOwnershipEdge
}

type sourceFunctionAnchor struct {
	Symbol SymbolRecord
	Body   string
}

type analysisCStyleScope struct {
	Kind  string
	Name  string
	Open  int
	Start int
	End   int
}

type analysisCStyleFunctionHeader struct {
	Start     int
	OpenBrace int
	FullName  string
	IsFriend  bool
}

func collectSourceAnchorsV2(snapshot ProjectSnapshot, existingSymbols map[string]SymbolRecord) sourceAnchorExtraction {
	extraction := sourceAnchorExtraction{}
	anchors := collectSourceFunctionAnchors(snapshot)
	if len(anchors) == 0 {
		return extraction
	}

	symbolLookup := buildAnchorSymbolLookup(existingSymbols, anchors)
	callSeen := map[string]struct{}{}
	overlaySeen := map[string]struct{}{}
	buildSeen := map[string]struct{}{}
	occurrenceSeen := map[string]struct{}{}

	for _, anchor := range anchors {
		extraction.Symbols = append(extraction.Symbols, anchor.Symbol)
		definitionKey := anchor.Symbol.ID + "|" + anchor.Symbol.File + "|definition"
		if _, ok := occurrenceSeen[definitionKey]; !ok {
			occurrenceSeen[definitionKey] = struct{}{}
			extraction.Occurrences = append(extraction.Occurrences, SymbolOccurrence{
				SymbolID: anchor.Symbol.ID,
				File:     anchor.Symbol.File,
				Role:     "definition",
			})
		}

		for _, ctxID := range buildContextIDsForFile(snapshot, anchor.Symbol.File) {
			key := ctxID + "|compiles_symbol|" + anchor.Symbol.ID
			if _, ok := buildSeen[key]; ok {
				continue
			}
			buildSeen[key] = struct{}{}
			extraction.Builds = append(extraction.Builds, BuildOwnershipEdge{
				SourceID: ctxID,
				TargetID: anchor.Symbol.ID,
				Type:     "compiles_symbol",
				Evidence: []string{anchor.Symbol.File},
			})
		}

		for _, item := range sourceAnchorOverlays(anchor.Symbol) {
			key := item.Domain + "|" + item.SourceID + "|" + item.Type + "|" + item.TargetID
			if _, ok := overlaySeen[key]; ok {
				continue
			}
			overlaySeen[key] = struct{}{}
			extraction.Overlays = append(extraction.Overlays, item)
		}

		for _, call := range collectAnchorCallEdges(anchor, symbolLookup) {
			key := call.SourceID + "|" + call.Type + "|" + call.TargetID
			if _, ok := callSeen[key]; ok {
				continue
			}
			callSeen[key] = struct{}{}
			extraction.Calls = append(extraction.Calls, call)
		}
	}

	sort.Slice(extraction.Symbols, func(i int, j int) bool {
		return extraction.Symbols[i].ID < extraction.Symbols[j].ID
	})
	sort.Slice(extraction.Occurrences, func(i int, j int) bool {
		if extraction.Occurrences[i].SymbolID == extraction.Occurrences[j].SymbolID {
			if extraction.Occurrences[i].File == extraction.Occurrences[j].File {
				return extraction.Occurrences[i].Role < extraction.Occurrences[j].Role
			}
			return extraction.Occurrences[i].File < extraction.Occurrences[j].File
		}
		return extraction.Occurrences[i].SymbolID < extraction.Occurrences[j].SymbolID
	})
	sort.Slice(extraction.Calls, func(i int, j int) bool {
		left := extraction.Calls[i].SourceID + "|" + extraction.Calls[i].Type + "|" + extraction.Calls[i].TargetID
		right := extraction.Calls[j].SourceID + "|" + extraction.Calls[j].Type + "|" + extraction.Calls[j].TargetID
		return left < right
	})
	sort.Slice(extraction.Overlays, func(i int, j int) bool {
		left := extraction.Overlays[i].Domain + "|" + extraction.Overlays[i].SourceID + "|" + extraction.Overlays[i].Type + "|" + extraction.Overlays[i].TargetID
		right := extraction.Overlays[j].Domain + "|" + extraction.Overlays[j].SourceID + "|" + extraction.Overlays[j].Type + "|" + extraction.Overlays[j].TargetID
		return left < right
	})
	sort.Slice(extraction.Builds, func(i int, j int) bool {
		left := extraction.Builds[i].SourceID + "|" + extraction.Builds[i].Type + "|" + extraction.Builds[i].TargetID
		right := extraction.Builds[j].SourceID + "|" + extraction.Builds[j].Type + "|" + extraction.Builds[j].TargetID
		return left < right
	})
	return extraction
}

func collectSourceFunctionAnchors(snapshot ProjectSnapshot) []sourceFunctionAnchor {
	out := []sourceFunctionAnchor{}
	for _, file := range snapshot.Files {
		if !analysisSupportsSourceAnchors(file.Extension) {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		text := string(data)
		switch analysisLanguageForExtension(file.Extension) {
		case "go":
			out = append(out, extractGoFunctionAnchors(snapshot, file, text)...)
		case "cpp":
			out = append(out, extractCStyleFunctionAnchors(snapshot, file, text)...)
		case "csharp":
			out = append(out, extractCSharpFunctionAnchors(snapshot, file, text)...)
		}
	}
	return out
}

func analysisSupportsSourceAnchors(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hh", ".cs":
		return true
	default:
		return false
	}
}

func extractGoFunctionAnchors(snapshot ProjectSnapshot, file ScannedFile, text string) []sourceFunctionAnchor {
	re := regexp.MustCompile(`(?m)^func\s*(\(([^)]*)\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	matches := re.FindAllStringSubmatchIndex(text, -1)
	out := []sourceFunctionAnchor{}
	for _, match := range matches {
		if len(match) < 8 {
			continue
		}
		rawReceiver := ""
		if match[4] >= 0 && match[5] >= 0 {
			rawReceiver = strings.TrimSpace(text[match[4]:match[5]])
		}
		name := strings.TrimSpace(text[match[6]:match[7]])
		if name == "" {
			continue
		}
		openIndex := strings.Index(text[match[1]:], "{")
		if openIndex < 0 {
			continue
		}
		openBrace := match[1] + openIndex
		closeBrace := analysisMatchClosingBrace(text, openBrace)
		if closeBrace <= openBrace {
			continue
		}
		body := text[openBrace : closeBrace+1]
		receiverType := normalizeGoReceiverType(rawReceiver)
		canonicalName := name
		containerID := ""
		if receiverType != "" {
			canonicalName = receiverType + "." + name
			containerID = "type:" + receiverType
		}
		startLine := analysisLineNumberAt(text, match[0])
		endLine := analysisLineNumberAt(text, closeBrace)
		tags, kind := classifySourceAnchorKind(file.Path, name, canonicalName, body, file.IsEntrypoint)
		buildContextID := firstSliceValue(buildContextIDsForFile(snapshot, file.Path))
		symbolID := buildSourceAnchorID(kind, canonicalName, file.Path)
		out = append(out, sourceFunctionAnchor{
			Symbol: SymbolRecord{
				ID:                symbolID,
				Name:              name,
				CanonicalName:     canonicalName,
				Kind:              kind,
				Language:          "go",
				File:              file.Path,
				ContainerSymbolID: containerID,
				BuildContextID:    buildContextID,
				Module:            unrealModuleForFile(snapshot, file.Path),
				Signature:         analysisTrimSignature(text[match[0]:openBrace]),
				StartLine:         startLine,
				EndLine:           endLine,
				Tags:              tags,
				Attributes: map[string]string{
					"line_start": strconv.Itoa(startLine),
					"line_end":   strconv.Itoa(endLine),
				},
			},
			Body: body,
		})
	}
	return out
}

func extractCStyleFunctionAnchors(snapshot ProjectSnapshot, file ScannedFile, text string) []sourceFunctionAnchor {
	masked := analysisMaskCommentsAndStrings(text)
	out := collectCStyleFunctionAnchorsInRange(snapshot, file, text, masked, 0, len(masked), "", "")
	return dedupeSourceFunctionAnchors(out)
}

func extractCSharpFunctionAnchors(snapshot ProjectSnapshot, file ScannedFile, text string) []sourceFunctionAnchor {
	if analysisIsBuildMetadataSourceFile(file.Path) {
		return nil
	}
	return extractCStyleFunctionAnchors(snapshot, file, text)
}

func analysisIsBuildMetadataSourceFile(path string) bool {
	normalized := strings.ToLower(strings.TrimSpace(filepath.ToSlash(path)))
	return strings.HasSuffix(normalized, ".build.cs") || strings.HasSuffix(normalized, ".target.cs")
}

func collectCStyleFunctionAnchorsInRange(snapshot ProjectSnapshot, file ScannedFile, text string, masked string, start int, end int, namespacePrefix string, containerPrefix string) []sourceFunctionAnchor {
	if start < 0 {
		start = 0
	}
	if end > len(masked) {
		end = len(masked)
	}
	if start >= end {
		return nil
	}

	scopes := collectCStyleScopes(masked, start, end)
	out := []sourceFunctionAnchor{}
	for _, scope := range scopes {
		nextNamespace := namespacePrefix
		nextContainer := containerPrefix
		switch scope.Kind {
		case "namespace":
			nextNamespace = analysisJoinScopePrefix(namespacePrefix, scope.Name)
		case "class", "struct":
			baseContainer := containerPrefix
			if strings.TrimSpace(baseContainer) == "" {
				baseContainer = namespacePrefix
			}
			nextContainer = analysisJoinScopePrefix(baseContainer, scope.Name)
		}
		out = append(out, collectCStyleFunctionAnchorsInRange(snapshot, file, text, masked, scope.Start, scope.End, nextNamespace, nextContainer)...)
	}

	for _, header := range collectCStyleFunctionHeaders(masked, start, end, scopes) {
		absoluteStart := header.Start
		fullName := analysisNormalizeCStyleQualifiedName(header.FullName)
		if analysisIgnoredCallToken(fullName) {
			continue
		}
		openBrace := header.OpenBrace
		closeBrace := analysisMatchClosingBrace(masked, openBrace)
		if closeBrace <= openBrace || closeBrace >= end {
			continue
		}
		body := text[openBrace : closeBrace+1]
		shortName := analysisShortCStyleName(fullName)
		if shortName == "" || analysisIgnoredCallToken(shortName) {
			continue
		}
		effectiveContainer := containerPrefix
		if header.IsFriend {
			effectiveContainer = ""
		}
		canonicalName, containerID := qualifyCStyleSymbolName(fullName, namespacePrefix, effectiveContainer)
		startLine := analysisLineNumberAt(text, absoluteStart)
		endLine := analysisLineNumberAt(text, closeBrace)
		language := analysisLanguageForExtension(file.Extension)
		tags, kind := classifySourceAnchorKind(file.Path, shortName, canonicalName, body, file.IsEntrypoint)
		buildContextID := firstSliceValue(buildContextIDsForFile(snapshot, file.Path))
		symbolID := buildSourceAnchorID(kind, canonicalName, file.Path)
		out = append(out, sourceFunctionAnchor{
			Symbol: SymbolRecord{
				ID:                symbolID,
				Name:              shortName,
				CanonicalName:     canonicalName,
				Kind:              kind,
				Language:          language,
				File:              file.Path,
				ContainerSymbolID: containerID,
				BuildContextID:    buildContextID,
				Module:            unrealModuleForFile(snapshot, file.Path),
				Signature:         analysisTrimSignature(text[absoluteStart:openBrace]),
				StartLine:         startLine,
				EndLine:           endLine,
				Tags:              tags,
				Attributes: map[string]string{
					"line_start": strconv.Itoa(startLine),
					"line_end":   strconv.Itoa(endLine),
				},
			},
			Body: body,
		})
	}
	return out
}

func collectCStyleFunctionHeaders(masked string, start int, end int, scopes []analysisCStyleScope) []analysisCStyleFunctionHeader {
	scopeByOpen := map[int]analysisCStyleScope{}
	for _, scope := range scopes {
		scopeByOpen[scope.Open] = scope
	}

	headers := []analysisCStyleFunctionHeader{}
	depth := 0
	for index := start; index < end; index++ {
		if scope, ok := scopeByOpen[index]; ok && depth == 0 {
			index = scope.End
			continue
		}

		switch masked[index] {
		case '{':
			if depth == 0 {
				header, ok := analysisParseCStyleFunctionHeader(masked, start, index)
				if ok {
					headers = append(headers, header)
				}
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return headers
}

func analysisParseCStyleFunctionHeader(masked string, rangeStart int, openBrace int) (analysisCStyleFunctionHeader, bool) {
	headerEnd := analysisPreviousSignificantIndex(masked, rangeStart, openBrace)
	if headerEnd < rangeStart {
		return analysisCStyleFunctionHeader{}, false
	}
	headerEnd++

	candidateClose := -1
	parenDepth := 0
	for index := headerEnd - 1; index >= rangeStart; index-- {
		switch masked[index] {
		case ')':
			if parenDepth == 0 {
				candidateClose = index
			}
			parenDepth++
		case '(':
			if parenDepth == 0 {
				continue
			}
			parenDepth--
			if parenDepth == 0 && candidateClose >= 0 {
				header, ok := analysisBuildCStyleFunctionHeader(masked, rangeStart, headerEnd, index, openBrace)
				if ok {
					return header, true
				}
				candidateClose = -1
			}
		case ';', '{', '}':
			if parenDepth == 0 && candidateClose < 0 {
				return analysisCStyleFunctionHeader{}, false
			}
		}
	}
	return analysisCStyleFunctionHeader{}, false
}

func analysisBuildCStyleFunctionHeader(masked string, rangeStart int, headerEnd int, openParen int, openBrace int) (analysisCStyleFunctionHeader, bool) {
	headerStart := analysisFindCStyleHeaderStart(masked, rangeStart, openParen)
	stem := masked[headerStart:openParen]
	nameStartOffset, nameEndOffset, fullName, ok := analysisExtractCStyleFunctionName(stem, headerStart, rangeStart)
	if !ok {
		return analysisCStyleFunctionHeader{}, false
	}
	nameStart := headerStart + nameStartOffset
	nameEnd := headerStart + nameEndOffset
	fullName = analysisNormalizeCStyleQualifiedName(fullName)
	if fullName == "" {
		return analysisCStyleFunctionHeader{}, false
	}

	headerPrefix := strings.TrimSpace(masked[headerStart:nameStart])
	if !analysisIsPlausibleCStyleFunctionHeader(headerPrefix, fullName) {
		return analysisCStyleFunctionHeader{}, false
	}
	if nameEnd <= nameStart {
		return analysisCStyleFunctionHeader{}, false
	}

	return analysisCStyleFunctionHeader{
		Start:     headerStart,
		OpenBrace: openBrace,
		FullName:  fullName,
		IsFriend:  analysisHeaderHasKeyword(headerPrefix, "friend"),
	}, true
}

func analysisFindCStyleHeaderStart(masked string, rangeStart int, nameStart int) int {
	for index := nameStart - 1; index >= rangeStart; index-- {
		switch masked[index] {
		case ';', '{', '}':
			return index + 1
		}
	}
	return rangeStart
}

func analysisExtractCStyleFunctionName(stem string, absoluteStart int, rangeStart int) (int, int, string, bool) {
	if opStart, opEnd, ok := analysisFindCStyleOperatorName(stem); ok {
		return opStart, opEnd, strings.TrimSpace(stem[opStart:opEnd]), true
	}

	nameEnd := analysisPreviousSignificantIndex(stem, 0, len(stem))
	if nameEnd < 0 {
		return 0, 0, "", false
	}
	nameEnd++
	nameStart := analysisFindCStyleNameStart(stem, 0, nameEnd)
	if absoluteStart+nameStart < rangeStart || nameStart >= nameEnd {
		return 0, 0, "", false
	}
	return nameStart, nameEnd, strings.TrimSpace(stem[nameStart:nameEnd]), true
}

func analysisFindCStyleOperatorName(stem string) (int, int, bool) {
	stem = strings.TrimRight(stem, " \t\r\n")
	operatorPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?s)((?:[A-Za-z_][A-Za-z0-9_:<>\s]*::\s*)*operator\s*\(\))\s*$`),
		regexp.MustCompile(`(?s)((?:[A-Za-z_][A-Za-z0-9_:<>\s]*::\s*)*operator\s*\[\])\s*$`),
		regexp.MustCompile(`(?s)((?:[A-Za-z_][A-Za-z0-9_:<>\s]*::\s*)*operator\s*(?:==|!=|<=|>=|<=>|<<=?|>>=?|->\*?|[+\-*/%&|^=!<>]+))\s*$`),
		regexp.MustCompile(`(?s)((?:[A-Za-z_][A-Za-z0-9_:<>\s]*::\s*)*operator\s+(?:new|delete|co_await))\s*$`),
		regexp.MustCompile(`(?s)((?:[A-Za-z_][A-Za-z0-9_:<>\s]*::\s*)*operator\s+(?:(?:const|volatile|signed|unsigned|short|long)\s+)*[A-Za-z_][A-Za-z0-9_:<>\s*&]+)\s*$`),
	}
	for _, re := range operatorPatterns {
		match := re.FindStringSubmatchIndex(stem)
		if len(match) >= 4 {
			return match[2], match[3], true
		}
	}
	return 0, 0, false
}

func analysisFindCStyleNameStart(masked string, rangeStart int, nameEnd int) int {
	angleDepth := 0
	for index := nameEnd - 1; index >= rangeStart; index-- {
		switch masked[index] {
		case '>':
			angleDepth++
			continue
		case '<':
			if angleDepth > 0 {
				angleDepth--
				continue
			}
		}
		if angleDepth > 0 {
			continue
		}
		if analysisIsCStyleNameChar(masked[index]) {
			continue
		}
		return index + 1
	}
	return rangeStart
}

func analysisIsCStyleNameChar(ch byte) bool {
	if ch >= 'a' && ch <= 'z' {
		return true
	}
	if ch >= 'A' && ch <= 'Z' {
		return true
	}
	if ch >= '0' && ch <= '9' {
		return true
	}
	switch ch {
	case '_', ':', '~':
		return true
	default:
		return false
	}
}

func analysisIsPlausibleCStyleFunctionHeader(prefix string, fullName string) bool {
	prefix = strings.TrimSpace(prefix)
	fullName = strings.TrimSpace(fullName)
	if fullName == "" {
		return false
	}
	if analysisIgnoredCallToken(fullName) || analysisIgnoredCallToken(analysisShortCStyleName(fullName)) {
		return false
	}
	lowerPrefix := strings.ToLower(prefix)
	for _, blocked := range []string{"namespace", "class", "struct", "enum", "union", "typedef", "using"} {
		if strings.HasPrefix(lowerPrefix, blocked+" ") || lowerPrefix == blocked {
			return false
		}
	}
	if strings.HasPrefix(lowerPrefix, "if ") || lowerPrefix == "if" {
		return false
	}
	if strings.HasPrefix(lowerPrefix, "for ") || lowerPrefix == "for" {
		return false
	}
	if strings.HasPrefix(lowerPrefix, "while ") || lowerPrefix == "while" {
		return false
	}
	if strings.HasPrefix(lowerPrefix, "switch ") || lowerPrefix == "switch" {
		return false
	}
	if strings.HasPrefix(lowerPrefix, "catch ") || lowerPrefix == "catch" {
		return false
	}

	prev := analysisPreviousSignificantIndex(prefix, 0, len(prefix))
	if prev >= 0 {
		switch prefix[prev] {
		case ',', '=', '[', '.':
			return false
		case ':':
			return analysisEndsWithAccessSpecifier(prefix)
		}
	}
	return true
}

func analysisHeaderHasKeyword(text string, keyword string) bool {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return false
	}
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_')
	})
	for _, field := range fields {
		if field == keyword {
			return true
		}
	}
	return false
}

func analysisEndsWithAccessSpecifier(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{"public:", "private:", "protected:"} {
		if strings.HasSuffix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func analysisPreviousSignificantIndex(text string, start int, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	for index := end - 1; index >= start; index-- {
		switch text[index] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return index
		}
	}
	return -1
}

func analysisMaskCommentsAndStrings(text string) string {
	bytes := []byte(text)
	state := "code"
	for index := 0; index < len(bytes); index++ {
		switch state {
		case "code":
			if index+1 < len(bytes) && bytes[index] == '/' && bytes[index+1] == '/' {
				bytes[index] = ' '
				bytes[index+1] = ' '
				state = "line_comment"
				index++
				continue
			}
			if index+1 < len(bytes) && bytes[index] == '/' && bytes[index+1] == '*' {
				bytes[index] = ' '
				bytes[index+1] = ' '
				state = "block_comment"
				index++
				continue
			}
			if bytes[index] == '"' {
				bytes[index] = ' '
				state = "double_quote"
				continue
			}
			if bytes[index] == '\'' {
				bytes[index] = ' '
				state = "single_quote"
				continue
			}
		case "line_comment":
			if bytes[index] == '\n' {
				state = "code"
				continue
			}
			bytes[index] = ' '
		case "block_comment":
			if index+1 < len(bytes) && bytes[index] == '*' && bytes[index+1] == '/' {
				bytes[index] = ' '
				bytes[index+1] = ' '
				state = "code"
				index++
				continue
			}
			if bytes[index] != '\n' && bytes[index] != '\r' && bytes[index] != '\t' {
				bytes[index] = ' '
			}
		case "double_quote":
			if bytes[index] == '\\' {
				if bytes[index] != '\n' && bytes[index] != '\r' {
					bytes[index] = ' '
				}
				if index+1 < len(bytes) {
					if bytes[index+1] != '\n' && bytes[index+1] != '\r' {
						bytes[index+1] = ' '
					}
					index++
				}
				continue
			}
			if bytes[index] == '"' {
				bytes[index] = ' '
				state = "code"
				continue
			}
			if bytes[index] != '\n' && bytes[index] != '\r' && bytes[index] != '\t' {
				bytes[index] = ' '
			}
		case "single_quote":
			if bytes[index] == '\\' {
				if bytes[index] != '\n' && bytes[index] != '\r' {
					bytes[index] = ' '
				}
				if index+1 < len(bytes) {
					if bytes[index+1] != '\n' && bytes[index+1] != '\r' {
						bytes[index+1] = ' '
					}
					index++
				}
				continue
			}
			if bytes[index] == '\'' {
				bytes[index] = ' '
				state = "code"
				continue
			}
			if bytes[index] != '\n' && bytes[index] != '\r' && bytes[index] != '\t' {
				bytes[index] = ' '
			}
		}
	}
	return string(bytes)
}

func collectCStyleScopes(masked string, start int, end int) []analysisCStyleScope {
	re := regexp.MustCompile(`(?m)\b(namespace|class|struct)\b([^;{]*)\{`)
	matches := re.FindAllStringSubmatchIndex(masked[start:end], -1)
	out := []analysisCStyleScope{}
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		absoluteStart := start + match[0]
		keyword := strings.TrimSpace(masked[start+match[2] : start+match[3]])
		header := strings.TrimSpace(masked[start+match[4] : start+match[5]])
		name := analysisExtractCStyleScopeName(keyword, header)
		if name == "" {
			continue
		}
		if strings.EqualFold(keyword, "class") && absoluteStart >= 5 && strings.TrimSpace(masked[absoluteStart-5:absoluteStart]) == "enum" {
			continue
		}
		openBrace := start + match[1] - 1
		if openBrace < absoluteStart || openBrace >= len(masked) || masked[openBrace] != '{' {
			continue
		}
		closeBrace := analysisMatchClosingBrace(masked, openBrace)
		if closeBrace <= openBrace || closeBrace >= end {
			continue
		}
		out = append(out, analysisCStyleScope{
			Kind:  keyword,
			Name:  name,
			Open:  openBrace,
			Start: openBrace + 1,
			End:   closeBrace,
		})
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].Start == out[j].Start {
			return out[i].End > out[j].End
		}
		return out[i].Start < out[j].Start
	})
	direct := []analysisCStyleScope{}
	for _, scope := range out {
		if len(direct) > 0 {
			parent := direct[len(direct)-1]
			if scope.Start >= parent.Start && scope.End <= parent.End {
				continue
			}
		}
		direct = append(direct, scope)
	}
	return direct
}

func analysisExtractCStyleScopeName(keyword string, header string) string {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	if keyword == "namespace" {
		return analysisExtractNamespaceScopeName(header)
	}

	header = analysisTrimCStyleScopeHeaderBeforeInheritance(header)
	tokens := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_:\.]*`).FindAllString(header, -1)
	for index := len(tokens) - 1; index >= 0; index-- {
		token := strings.TrimSpace(tokens[index])
		if token == "" || analysisIgnoredScopeToken(token) {
			continue
		}
		if analysisLooksLikeScopeMacro(token) && index > 0 {
			continue
		}
		return token
	}
	return ""
}

func analysisExtractNamespaceScopeName(header string) string {
	header = strings.TrimSpace(header)
	for analysisHeaderHasKeyword(header, "inline") {
		lower := strings.ToLower(strings.TrimSpace(header))
		if !strings.HasPrefix(lower, "inline ") {
			break
		}
		header = strings.TrimSpace(header[len("inline "):])
	}
	tokens := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_:\.]*`).FindAllString(header, -1)
	for _, token := range tokens {
		if analysisIgnoredScopeToken(token) {
			continue
		}
		return strings.TrimSpace(token)
	}
	return ""
}

func analysisTrimCStyleScopeHeaderBeforeInheritance(header string) string {
	angleDepth := 0
	parenDepth := 0
	bracketDepth := 0
	for index := 0; index < len(header); index++ {
		switch header[index] {
		case '<':
			angleDepth++
		case '>':
			if angleDepth > 0 {
				angleDepth--
			}
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case ':':
			if angleDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				prevColon := index > 0 && header[index-1] == ':'
				nextColon := index+1 < len(header) && header[index+1] == ':'
				if !prevColon && !nextColon {
					return strings.TrimSpace(header[:index])
				}
			}
		}
	}
	return strings.TrimSpace(header)
}

func analysisIgnoredScopeToken(token string) bool {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "", "class", "struct", "namespace", "final", "sealed", "alignas", "__declspec", "declspec", "inline":
		return true
	default:
		return false
	}
}

func analysisLooksLikeScopeMacro(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if strings.HasSuffix(token, "_API") || strings.HasSuffix(token, "_EXPORT") {
		return true
	}
	hasLetter := false
	for index := 0; index < len(token); index++ {
		ch := token[index]
		if ch >= 'a' && ch <= 'z' {
			return false
		}
		if ch >= 'A' && ch <= 'Z' {
			hasLetter = true
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '_' {
			continue
		}
		return false
	}
	return hasLetter
}

func analysisIndexInScopes(index int, scopes []analysisCStyleScope) bool {
	for _, scope := range scopes {
		if index >= scope.Start && index < scope.End {
			return true
		}
	}
	return false
}

func analysisJoinScopePrefix(prefix string, name string) string {
	prefix = strings.TrimSpace(prefix)
	name = strings.TrimSpace(name)
	if prefix == "" {
		return name
	}
	if name == "" {
		return prefix
	}
	return prefix + "::" + name
}

func analysisShortCStyleName(fullName string) string {
	fullName = analysisNormalizeCStyleQualifiedName(fullName)
	if strings.Contains(fullName, "::") {
		parts := strings.Split(fullName, "::")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return fullName
}

func qualifyCStyleSymbolName(fullName string, namespacePrefix string, containerPrefix string) (string, string) {
	fullName = analysisNormalizeCStyleQualifiedName(fullName)
	namespacePrefix = strings.TrimSpace(namespacePrefix)
	containerPrefix = strings.TrimSpace(containerPrefix)
	canonicalName := fullName
	if strings.Contains(fullName, "::") {
		if namespacePrefix != "" && !strings.HasPrefix(fullName, namespacePrefix+"::") {
			if containerPrefix != "" {
				shortContainer := analysisShortCStyleContainer(containerPrefix)
				if shortContainer != "" && strings.HasPrefix(fullName, shortContainer+"::") {
					containerNamespace := strings.TrimSuffix(containerPrefix, "::"+shortContainer)
					if strings.TrimSpace(containerNamespace) != "" {
						canonicalName = containerNamespace + "::" + fullName
					}
				} else {
					canonicalName = namespacePrefix + "::" + fullName
				}
			} else {
				canonicalName = namespacePrefix + "::" + fullName
			}
		}
	} else if containerPrefix != "" {
		canonicalName = containerPrefix + "::" + fullName
	} else if namespacePrefix != "" {
		canonicalName = namespacePrefix + "::" + fullName
	}

	container := analysisInferCStyleContainer(canonicalName, namespacePrefix, containerPrefix)
	if container != "" {
		return canonicalName, "type:" + container
	}
	return canonicalName, ""
}

func analysisShortCStyleContainer(scope string) string {
	parts := analysisSplitScopeParts(scope)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func analysisInferCStyleContainer(canonicalName string, namespacePrefix string, containerPrefix string) string {
	if strings.TrimSpace(containerPrefix) != "" {
		return strings.TrimSpace(containerPrefix)
	}

	canonicalParts := analysisSplitScopeParts(canonicalName)
	namespaceParts := analysisSplitScopeParts(namespacePrefix)
	if len(canonicalParts) <= 1 {
		return ""
	}
	if len(namespaceParts) > 0 && len(canonicalParts) >= len(namespaceParts) {
		canonicalNamespace := strings.Join(canonicalParts[:len(namespaceParts)], "::")
		if strings.EqualFold(canonicalNamespace, strings.Join(namespaceParts, "::")) {
			if len(canonicalParts) == len(namespaceParts)+1 {
				return ""
			}
			return strings.Join(canonicalParts[:len(canonicalParts)-1], "::")
		}
	}
	return strings.Join(canonicalParts[:len(canonicalParts)-1], "::")
}

func analysisSplitScopeParts(scope string) []string {
	rawParts := strings.Split(strings.TrimSpace(scope), "::")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	return parts
}

func analysisNormalizeCStyleQualifiedName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var builder strings.Builder
	angleDepth := 0
	for index := 0; index < len(name); index++ {
		ch := name[index]
		switch ch {
		case '<':
			angleDepth++
			continue
		case '>':
			if angleDepth > 0 {
				angleDepth--
				continue
			}
		}
		if angleDepth > 0 {
			continue
		}
		switch ch {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			builder.WriteByte(ch)
		}
	}
	return strings.Trim(strings.TrimSpace(builder.String()), ":")
}

func dedupeSourceFunctionAnchors(items []sourceFunctionAnchor) []sourceFunctionAnchor {
	out := []sourceFunctionAnchor{}
	seen := map[string]struct{}{}
	for _, item := range items {
		key := item.Symbol.ID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		return out[i].Symbol.ID < out[j].Symbol.ID
	})
	return out
}

func analysisTrimSignature(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " "))
	return strings.Join(strings.Fields(trimmed), " ")
}

func normalizeGoReceiverType(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	candidate := fields[len(fields)-1]
	candidate = strings.TrimPrefix(candidate, "*")
	candidate = strings.TrimPrefix(candidate, "[]")
	return strings.TrimSpace(candidate)
}

func analysisLineNumberAt(text string, index int) int {
	if index <= 0 {
		return 1
	}
	if index > len(text) {
		index = len(text)
	}
	return strings.Count(text[:index], "\n") + 1
}

func analysisMatchClosingBrace(text string, openBrace int) int {
	if openBrace < 0 || openBrace >= len(text) || text[openBrace] != '{' {
		return -1
	}
	depth := 0
	for index := openBrace; index < len(text); index++ {
		switch text[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func classifySourceAnchorKind(path string, shortName string, canonicalName string, body string, entrypoint bool) ([]string, string) {
	corpus := strings.ToLower(strings.TrimSpace(path + " " + shortName + " " + canonicalName + " " + body))
	tags := []string{}
	kind := "function"
	if entrypoint || containsAny(strings.ToLower(shortName), "main", "winmain", "driverentry", "dllmain") {
		tags = append(tags, "entrypoint")
	}
	switch {
	case containsAny(corpus, "irp_mj_device_control", "deviceiocontrol", "ioctl_", "ctl_code", "io_stack_location", "method_buffered", "irp->associatedirp", "irp_sp"):
		kind = "ioctl_handler"
		tags = append(tags, "ioctl_surface", "security_surface")
	case containsAny(corpus, "openprocess", "ntopenprocess", "zwopenprocess", "duplicatehandle", "obregistercallbacks", "process_vm_read", "process_vm_write", "process_dup_handle"):
		kind = "handle_path"
		tags = append(tags, "handle_surface", "security_surface")
	case containsAny(corpus, "readprocessmemory", "writeprocessmemory", "mmcopyvirtualmemory", "zwreadvirtualmemory", "zwwritevirtualmemory", "kestackattachprocess", "scanmemory", "patternscan", "virtualqueryex"):
		kind = "memory_path"
		tags = append(tags, "memory_surface", "security_surface")
	case containsAny(corpus, "namedpipe", "createpipe", "rpc", "grpc", "dispatchrequest", "dispatchcommand", "ipc", "socket"):
		kind = "rpc_handler"
		tags = append(tags, "rpc_surface", "security_surface")
	}
	if containsAny(corpus, "integrity", "tamper", "hook", "patch", "signature", "attestation", "authority", "trust") {
		tags = append(tags, "tamper_surface", "security_boundary")
	}
	if strings.Contains(canonicalName, "::") || strings.Contains(canonicalName, ".") {
		tags = append(tags, "member_function")
	}
	if kind == "function" && containsAny(strings.ToLower(shortName), "dispatch", "handler", "process", "validate", "scan", "protect") {
		tags = append(tags, "control_surface")
	}
	return analysisUniqueStrings(tags), kind
}

func buildSourceAnchorID(kind string, canonicalName string, file string) string {
	prefix := "func"
	switch strings.TrimSpace(kind) {
	case "ioctl_handler":
		prefix = "ioctl"
	case "handle_path":
		prefix = "handle"
	case "memory_path":
		prefix = "memory"
	case "rpc_handler":
		prefix = "rpc_handler"
	}
	return prefix + ":" + strings.TrimSpace(canonicalName) + "@" + strings.TrimSpace(file)
}

func sourceAnchorOverlays(symbol SymbolRecord) []OverlayEdge {
	domainByTag := map[string]struct {
		domain string
		typ    string
	}{
		"ioctl_surface":     {domain: "ioctl_surface", typ: "issues_ioctl"},
		"handle_surface":    {domain: "handle_surface", typ: "opens_handle"},
		"memory_surface":    {domain: "memory_surface", typ: "touches_memory"},
		"rpc_surface":       {domain: "rpc_surface", typ: "dispatches_rpc"},
		"tamper_surface":    {domain: "tamper_surface", typ: "touches_tamper_surface"},
		"security_boundary": {domain: "security_boundary", typ: "crosses_trust_boundary"},
	}
	out := []OverlayEdge{}
	for _, tag := range symbol.Tags {
		if item, ok := domainByTag[strings.TrimSpace(tag)]; ok {
			out = append(out, OverlayEdge{
				SourceID: symbol.ID,
				TargetID: "entity:" + item.domain,
				Type:     item.typ,
				Domain:   item.domain,
				Evidence: []string{symbol.File},
			})
		}
	}
	return out
}

type anchorSymbolLookup struct {
	byShortName map[string][]SymbolRecord
	byFullName  map[string][]SymbolRecord
}

func buildAnchorSymbolLookup(existingSymbols map[string]SymbolRecord, anchors []sourceFunctionAnchor) anchorSymbolLookup {
	lookup := anchorSymbolLookup{
		byShortName: map[string][]SymbolRecord{},
		byFullName:  map[string][]SymbolRecord{},
	}
	add := func(symbol SymbolRecord) {
		shortKey := strings.ToLower(strings.TrimSpace(symbol.Name))
		fullKey := strings.ToLower(strings.TrimSpace(symbol.CanonicalName))
		if shortKey != "" {
			lookup.byShortName[shortKey] = append(lookup.byShortName[shortKey], symbol)
		}
		if fullKey != "" {
			lookup.byFullName[fullKey] = append(lookup.byFullName[fullKey], symbol)
		}
	}
	for _, symbol := range existingSymbols {
		add(symbol)
	}
	for _, anchor := range anchors {
		add(anchor.Symbol)
	}
	return lookup
}

func collectAnchorCallEdges(anchor sourceFunctionAnchor, lookup anchorSymbolLookup) []CallEdge {
	callRe := regexp.MustCompile(`(?m)\b([A-Za-z_][A-Za-z0-9_:<>,~]*)\s*\(|((?:[A-Za-z_][A-Za-z0-9_:<>,~]*::\s*)?operator\s*(?:\(\)|\[\]|==|!=|<=|>=|<=>|<<=?|>>=?|->\*?|[+\-*/%&|^=!<>]+|(?:new|delete|co_await)|(?:(?:const|volatile|signed|unsigned|short|long)\s+)*[A-Za-z_][A-Za-z0-9_:<>\s*&]+))\s*\(`)
	matches := callRe.FindAllStringSubmatch(anchor.Body, -1)
	out := []CallEdge{}
	seen := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		token := strings.TrimSpace(match[1])
		if token == "" && len(match) > 2 {
			token = strings.TrimSpace(match[2])
		}
		token = analysisNormalizeCStyleQualifiedName(token)
		if analysisIgnoredCallToken(token) {
			continue
		}
		target, ok := resolveAnchorCallTarget(token, anchor.Symbol, lookup)
		if !ok || target.ID == anchor.Symbol.ID {
			continue
		}
		key := anchor.Symbol.ID + "|calls|" + target.ID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		callType := "calls"
		switch strings.TrimSpace(target.Kind) {
		case "rpc", "rpc_handler":
			callType = "dispatches_rpc"
		case "ioctl_handler":
			callType = "dispatches_ioctl"
		}
		out = append(out, CallEdge{
			SourceID: anchor.Symbol.ID,
			TargetID: target.ID,
			Type:     callType,
			Evidence: []string{anchor.Symbol.File},
		})
	}
	return out
}

func resolveAnchorCallTarget(token string, source SymbolRecord, lookup anchorSymbolLookup) (SymbolRecord, bool) {
	candidates := []SymbolRecord{}
	fullKey := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(token, ".", "::")))
	candidates = append(candidates, lookup.byFullName[fullKey]...)
	if strings.Contains(token, "::") {
		short := token[strings.LastIndex(token, "::")+2:]
		candidates = append(candidates, lookup.byShortName[strings.ToLower(strings.TrimSpace(short))]...)
	} else {
		candidates = append(candidates, lookup.byShortName[strings.ToLower(strings.TrimSpace(token))]...)
	}
	if len(candidates) == 0 {
		return SymbolRecord{}, false
	}
	type scoredCandidate struct {
		symbol SymbolRecord
		score  int
	}
	scored := []scoredCandidate{}
	for _, candidate := range candidates {
		score := 0
		if strings.EqualFold(candidate.File, source.File) {
			score += 4
		}
		if strings.EqualFold(candidate.BuildContextID, source.BuildContextID) && strings.TrimSpace(candidate.BuildContextID) != "" {
			score += 3
		}
		if strings.EqualFold(candidate.ContainerSymbolID, source.ContainerSymbolID) && strings.TrimSpace(candidate.ContainerSymbolID) != "" {
			score += 2
		}
		if strings.EqualFold(candidate.Module, source.Module) && strings.TrimSpace(candidate.Module) != "" {
			score++
		}
		if candidate.Kind == "function" || candidate.Kind == "method" || strings.HasSuffix(candidate.Kind, "_handler") || strings.HasSuffix(candidate.Kind, "_path") {
			score++
		}
		scored = append(scored, scoredCandidate{symbol: candidate, score: score})
	}
	sort.Slice(scored, func(i int, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].symbol.ID < scored[j].symbol.ID
		}
		return scored[i].score > scored[j].score
	})
	return scored[0].symbol, true
}

func analysisIgnoredCallToken(token string) bool {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "", "if", "for", "while", "switch", "return", "sizeof", "alignof", "decltype", "noexcept", "requires", "catch", "new", "delete", "append", "make", "panic", "static_cast", "dynamic_cast", "reinterpret_cast", "const_cast":
		return true
	default:
		return false
	}
}
