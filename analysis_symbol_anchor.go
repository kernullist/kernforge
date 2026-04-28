package main

import (
	"fmt"
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

const (
	registrationIncludeClosureMaxDepth = 6
	registrationIncludeClosureMaxFiles = 128
)

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

	registrationTables := collectFileLevelRegistrationTableAnchors(snapshot, symbolLookup)
	for _, symbol := range registrationTables.Symbols {
		extraction.Symbols = append(extraction.Symbols, symbol)
		definitionKey := symbol.ID + "|" + symbol.File + "|definition"
		if _, ok := occurrenceSeen[definitionKey]; !ok {
			occurrenceSeen[definitionKey] = struct{}{}
			extraction.Occurrences = append(extraction.Occurrences, SymbolOccurrence{
				SymbolID: symbol.ID,
				File:     symbol.File,
				Role:     "definition",
			})
		}
		for _, ctxID := range buildContextIDsForFile(snapshot, symbol.File) {
			key := ctxID + "|compiles_symbol|" + symbol.ID
			if _, ok := buildSeen[key]; ok {
				continue
			}
			buildSeen[key] = struct{}{}
			extraction.Builds = append(extraction.Builds, BuildOwnershipEdge{
				SourceID: ctxID,
				TargetID: symbol.ID,
				Type:     "compiles_symbol",
				Evidence: []string{symbol.File},
			})
		}
	}
	for _, edge := range registrationTables.Calls {
		key := edge.SourceID + "|" + edge.Type + "|" + edge.TargetID
		if _, ok := callSeen[key]; ok {
			continue
		}
		callSeen[key] = struct{}{}
		extraction.Calls = append(extraction.Calls, edge)
	}
	for _, edge := range registrationTables.Overlays {
		key := edge.Domain + "|" + edge.SourceID + "|" + edge.Type + "|" + edge.TargetID
		if _, ok := overlaySeen[key]; ok {
			continue
		}
		overlaySeen[key] = struct{}{}
		extraction.Overlays = append(extraction.Overlays, edge)
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
		"ioctl_surface":       {domain: "ioctl_surface", typ: "issues_ioctl"},
		"handle_surface":      {domain: "handle_surface", typ: "opens_handle"},
		"memory_surface":      {domain: "memory_surface", typ: "touches_memory"},
		"rpc_surface":         {domain: "rpc_surface", typ: "dispatches_rpc"},
		"callback_surface":    {domain: "callback_surface", typ: "registers_callback"},
		"file_filter_surface": {domain: "file_filter_surface", typ: "registers_file_filter"},
		"process_surface":     {domain: "process_surface", typ: "registers_process_callback"},
		"tamper_surface":      {domain: "tamper_surface", typ: "touches_tamper_surface"},
		"security_boundary":   {domain: "security_boundary", typ: "crosses_trust_boundary"},
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

func collectFileLevelRegistrationTableAnchors(snapshot ProjectSnapshot, lookup anchorSymbolLookup) sourceAnchorExtraction {
	out := sourceAnchorExtraction{}
	for _, file := range snapshot.Files {
		if !analysisSupportsSourceAnchors(file.Extension) || analysisLanguageForExtension(file.Extension) != "cpp" {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		text := string(data)
		masked := analysisMaskCommentsAndStrings(text)
		aliases := collectRegistrationTableTypeAliasesForFile(snapshot, file, masked)
		seenTables := map[string]struct{}{}
		for _, table := range collectRegistrationTableDeclarations(masked, aliases) {
			key := strconv.Itoa(table.Start) + "|" + table.TableName
			if _, ok := seenTables[key]; ok {
				continue
			}
			seenTables[key] = struct{}{}
			includeWithoutTargets := table.SourceKind == "explicit" || table.SourceKind == "alias"
			out = appendRegistrationTableAnchor(out, snapshot, file, text, masked, table, lookup, includeWithoutTargets)
		}
		definitions := collectRegistrationMacroDefinitionsForFile(snapshot, file, masked)
		out = appendRegistrationMacroAnchors(out, snapshot, file, text, masked, definitions, lookup)
	}
	return out
}

type registrationTableDeclaration struct {
	TableType  string
	SourceType string
	TableName  string
	Start      int
	OpenBrace  int
	CloseBrace int
	SourceKind string
}

func collectRegistrationTableDeclarations(masked string, knownAliases map[string]string) []registrationTableDeclaration {
	aliases := map[string]string{}
	for key, value := range knownAliases {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			aliases[key] = value
		}
	}
	for key, value := range collectRegistrationTableTypeAliases(masked) {
		aliases[key] = value
	}
	types := []string{"OB_OPERATION_REGISTRATION", "FLT_OPERATION_REGISTRATION"}
	for alias := range aliases {
		types = append(types, alias)
	}
	sort.Slice(types, func(i int, j int) bool {
		if len(types[i]) == len(types[j]) {
			return types[i] < types[j]
		}
		return len(types[i]) > len(types[j])
	})
	alternatives := []string{}
	for _, item := range analysisUniqueStrings(types) {
		alternatives = append(alternatives, regexp.QuoteMeta(item))
	}
	out := []registrationTableDeclaration{}
	if len(alternatives) > 0 {
		tableRe := regexp.MustCompile(`(?is)\b(?:static\s+|extern\s+|const\s+|CONST\s+|volatile\s+|PAGED\s+|__declspec\s*\([^)]*\)\s*)*(` + strings.Join(alternatives, "|") + `)\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\[[^\]]*\])?\s*=\s*\{`)
		for _, match := range tableRe.FindAllStringSubmatchIndex(masked, -1) {
			if len(match) < 6 || match[2] < 0 || match[4] < 0 {
				continue
			}
			sourceType := strings.TrimSpace(masked[match[2]:match[3]])
			tableName := strings.TrimSpace(masked[match[4]:match[5]])
			tableType := firstNonBlankString(aliases[sourceType], sourceType)
			openBrace := match[1] - 1
			closeBrace := analysisMatchClosingBrace(masked, openBrace)
			if tableType == "" || tableName == "" || closeBrace <= openBrace {
				continue
			}
			sourceKind := "explicit"
			if !strings.EqualFold(tableType, sourceType) {
				sourceKind = "alias"
			}
			out = append(out, registrationTableDeclaration{
				TableType:  tableType,
				SourceType: sourceType,
				TableName:  tableName,
				Start:      match[0],
				OpenBrace:  openBrace,
				CloseBrace: closeBrace,
				SourceKind: sourceKind,
			})
		}
	}

	genericRe := regexp.MustCompile(`(?is)\b(?:static\s+|extern\s+|const\s+|CONST\s+|volatile\s+|PAGED\s+|__declspec\s*\([^)]*\)\s*)*([A-Za-z_][A-Za-z0-9_]*(?:REGISTRATION|Registration|CALLBACKS|Callbacks|CALLBACK|Callback|OPERATIONS|Operations|OPERATION|Operation|FILTER|Filter)[A-Za-z0-9_]*)\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\[[^\]]*\])?\s*=\s*\{`)
	for _, match := range genericRe.FindAllStringSubmatchIndex(masked, -1) {
		if len(match) < 6 || match[2] < 0 || match[4] < 0 {
			continue
		}
		tableType := strings.TrimSpace(masked[match[2]:match[3]])
		tableName := strings.TrimSpace(masked[match[4]:match[5]])
		openBrace := match[1] - 1
		closeBrace := analysisMatchClosingBrace(masked, openBrace)
		if tableType == "" || tableName == "" || closeBrace <= openBrace {
			continue
		}
		if !registrationDescriptorLooksRelevant(tableType + " " + tableName + " " + masked[openBrace:closeBrace+1]) {
			continue
		}
		out = append(out, registrationTableDeclaration{
			TableType:  tableType,
			SourceType: tableType,
			TableName:  tableName,
			Start:      match[0],
			OpenBrace:  openBrace,
			CloseBrace: closeBrace,
			SourceKind: "generic_initializer",
		})
	}
	return out
}

func collectRegistrationTableTypeAliases(masked string) map[string]string {
	aliases := map[string]string{}
	typedefRe := regexp.MustCompile(`(?is)\btypedef\s+((?:OB|FLT)_OPERATION_REGISTRATION)\s+([A-Za-z_][A-Za-z0-9_]*)\s*;`)
	for _, match := range typedefRe.FindAllStringSubmatch(masked, -1) {
		if len(match) >= 3 {
			aliases[strings.TrimSpace(match[2])] = strings.TrimSpace(match[1])
		}
	}
	usingRe := regexp.MustCompile(`(?is)\busing\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*((?:OB|FLT)_OPERATION_REGISTRATION)\s*;`)
	for _, match := range usingRe.FindAllStringSubmatch(masked, -1) {
		if len(match) >= 3 {
			aliases[strings.TrimSpace(match[1])] = strings.TrimSpace(match[2])
		}
	}
	return aliases
}

func collectRegistrationTableTypeAliasesForFile(snapshot ProjectSnapshot, file ScannedFile, masked string) map[string]string {
	out := map[string]string{}
	for _, includePath := range registrationIncludeClosure(snapshot, file, registrationIncludeClosureMaxDepth) {
		includeMasked, ok := readMaskedAnalysisSourceFile(snapshot, includePath)
		if !ok {
			continue
		}
		for key, value := range collectRegistrationTableTypeAliases(includeMasked) {
			out[key] = value
		}
	}
	for key, value := range collectRegistrationTableTypeAliases(masked) {
		out[key] = value
	}
	return out
}

type registrationMacroDefinition struct {
	Name   string
	Params []string
	Body   string
}

func collectRegistrationMacroDefinitions(masked string) map[string][]registrationMacroDefinition {
	out := map[string][]registrationMacroDefinition{}
	defineRe := regexp.MustCompile(`(?is)^#\s*define\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*(.*)$`)
	for _, logicalLine := range analysisLogicalPreprocessorLines(masked) {
		line := strings.TrimSpace(logicalLine)
		match := defineRe.FindStringSubmatch(line)
		if len(match) < 4 {
			continue
		}
		name := strings.TrimSpace(match[1])
		params := []string{}
		for _, param := range splitCStyleCallArguments(match[2]) {
			param = strings.TrimSpace(param)
			if param != "" {
				params = append(params, param)
			}
		}
		body := strings.TrimSpace(strings.ReplaceAll(match[3], "\\\n", " "))
		body = strings.TrimSpace(strings.TrimSuffix(body, "\\"))
		if name == "" || len(params) == 0 || body == "" {
			continue
		}
		out[name] = append(out[name], registrationMacroDefinition{Name: name, Params: params, Body: body})
	}
	return out
}

func collectRegistrationMacroDefinitionsForFile(snapshot ProjectSnapshot, file ScannedFile, masked string) map[string][]registrationMacroDefinition {
	out := map[string][]registrationMacroDefinition{}
	for _, includePath := range registrationIncludeClosure(snapshot, file, registrationIncludeClosureMaxDepth) {
		includeMasked, ok := readMaskedAnalysisSourceFile(snapshot, includePath)
		if !ok {
			continue
		}
		for key, value := range collectRegistrationMacroDefinitions(includeMasked) {
			out[key] = append(out[key], value...)
		}
	}
	for key, value := range collectRegistrationMacroDefinitions(masked) {
		out[key] = value
	}
	return out
}

func registrationIncludeClosure(snapshot ProjectSnapshot, file ScannedFile, maxDepth int) []string {
	if maxDepth <= 0 {
		return nil
	}
	fileByPath := registrationSnapshotFilesByPath(snapshot)
	importGraph := snapshot.ImportGraph
	if len(importGraph) == 0 {
		importGraph = map[string][]string{}
		for _, item := range snapshot.Files {
			importGraph[item.Path] = append([]string(nil), item.Imports...)
		}
	}
	type queueItem struct {
		path  string
		depth int
	}
	queue := []queueItem{}
	for _, dep := range append([]string{}, file.Imports...) {
		queue = append(queue, queueItem{path: dep, depth: 1})
	}
	for _, dep := range importGraph[file.Path] {
		queue = append(queue, queueItem{path: dep, depth: 1})
	}
	seen := map[string]struct{}{strings.TrimSpace(file.Path): {}}
	out := []string{}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		path := strings.TrimSpace(filepath.ToSlash(item.path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		depFile, ok := fileByPath[path]
		if !ok || !analysisSupportsSourceAnchors(depFile.Extension) || analysisLanguageForExtension(depFile.Extension) != "cpp" {
			continue
		}
		out = append(out, path)
		if len(out) >= registrationIncludeClosureMaxFiles {
			break
		}
		if item.depth >= maxDepth {
			continue
		}
		for _, next := range append([]string{}, depFile.Imports...) {
			queue = append(queue, queueItem{path: next, depth: item.depth + 1})
		}
		for _, next := range importGraph[path] {
			queue = append(queue, queueItem{path: next, depth: item.depth + 1})
		}
	}
	return analysisUniqueStrings(out)
}

func registrationSnapshotFilesByPath(snapshot ProjectSnapshot) map[string]ScannedFile {
	if len(snapshot.FilesByPath) > 0 {
		return snapshot.FilesByPath
	}
	out := map[string]ScannedFile{}
	for _, file := range snapshot.Files {
		out[file.Path] = file
	}
	return out
}

func readMaskedAnalysisSourceFile(snapshot ProjectSnapshot, path string) (string, bool) {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return "", false
	}
	abs := filepath.Join(snapshot.Root, filepath.FromSlash(path))
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", false
	}
	return analysisMaskCommentsAndStrings(string(data)), true
}

func analysisLogicalPreprocessorLines(text string) []string {
	lines := strings.Split(text, "\n")
	out := []string{}
	for index := 0; index < len(lines); index++ {
		line := strings.TrimRight(lines[index], " \t\r")
		if !strings.HasSuffix(line, "\\") {
			out = append(out, lines[index])
			continue
		}
		var b strings.Builder
		for {
			b.WriteString(strings.TrimSuffix(strings.TrimRight(lines[index], " \t\r"), "\\"))
			b.WriteString("\n")
			if index+1 >= len(lines) {
				break
			}
			index++
			next := strings.TrimRight(lines[index], " \t\r")
			if !strings.HasSuffix(next, "\\") {
				b.WriteString(next)
				break
			}
		}
		out = append(out, b.String())
	}
	return out
}

func appendRegistrationTableAnchor(out sourceAnchorExtraction, snapshot ProjectSnapshot, file ScannedFile, text string, masked string, table registrationTableDeclaration, lookup anchorSymbolLookup, includeWithoutTargets bool) sourceAnchorExtraction {
	descriptor := table.TableType + " " + table.TableName + " " + masked[table.OpenBrace:table.CloseBrace+1]
	tableKind, edgeType, tags := classifyRegistrationTable(descriptor)
	startLine := analysisLineNumberAt(text, table.Start)
	endLine := analysisLineNumberAt(text, table.CloseBrace)
	symbol := SymbolRecord{
		ID:             buildRegistrationTableAnchorID(tableKind, table.TableName, file.Path),
		Name:           table.TableName,
		CanonicalName:  table.TableName,
		Kind:           tableKind,
		Language:       analysisLanguageForExtension(file.Extension),
		File:           file.Path,
		Module:         unrealModuleForFile(snapshot, file.Path),
		BuildContextID: firstSliceValue(buildContextIDsForFile(snapshot, file.Path)),
		Signature:      analysisTrimSignature(text[table.Start:table.OpenBrace]),
		StartLine:      startLine,
		EndLine:        endLine,
		Tags:           tags,
		Attributes: map[string]string{
			"line_start":  strconv.Itoa(startLine),
			"line_end":    strconv.Itoa(endLine),
			"table_type":  table.TableType,
			"source_type": table.SourceType,
			"source_kind": table.SourceKind,
		},
	}
	calls := registrationCallbackEdgesFromText(symbol, masked[table.OpenBrace:table.CloseBrace+1], table.TableName, table.TableType, edgeType, lookup)
	if len(calls) == 0 && !includeWithoutTargets {
		return out
	}
	out.Symbols = append(out.Symbols, symbol)
	out.Overlays = append(out.Overlays, sourceAnchorOverlays(symbol)...)
	out.Calls = append(out.Calls, calls...)
	return out
}

func appendRegistrationMacroAnchors(out sourceAnchorExtraction, snapshot ProjectSnapshot, file ScannedFile, text string, masked string, definitions map[string][]registrationMacroDefinition, lookup anchorSymbolLookup) sourceAnchorExtraction {
	macroRe := regexp.MustCompile(`(?is)\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	matches := macroRe.FindAllStringSubmatchIndex(masked, -1)
	seen := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 4 || match[2] < 0 {
			continue
		}
		if analysisIndexInPreprocessorDirective(masked, match[0]) {
			continue
		}
		macroName := strings.TrimSpace(masked[match[2]:match[3]])
		if !registrationMacroNameLooksRelevant(macroName) {
			continue
		}
		openParen := match[1] - 1
		closeParen := analysisMatchClosingParen(masked, openParen)
		if closeParen <= openParen {
			continue
		}
		args := masked[openParen+1 : closeParen]
		if !registrationDescriptorLooksRelevant(macroName + " " + args) {
			continue
		}
		argList := splitCStyleCallArguments(args)
		expandedVariants := expandRegistrationMacroInvocationVariants(macroName, argList, definitions, 0)
		if len(expandedVariants) == 0 {
			expandedVariants = []string{""}
		}
		for variantIndex, expanded := range expandedVariants {
			descriptor := strings.TrimSpace(macroName + " " + args + " " + expanded)
			tableName := registrationMacroAnchorName(macroName, descriptor, lookup)
			if tableName == "" {
				tableName = fmt.Sprintf("%s:%d", macroName, analysisLineNumberAt(text, match[0]))
			}
			tableKind, edgeType, tags := classifyRegistrationTable(descriptor)
			startLine := analysisLineNumberAt(text, match[0])
			symbolID := buildRegistrationMacroAnchorID(tableKind, macroName, tableName, file.Path, startLine)
			if len(expandedVariants) > 1 {
				symbolID += fmt.Sprintf("#%08x", stableHash32(descriptor))
			}
			symbol := SymbolRecord{
				ID:             symbolID,
				Name:           tableName,
				CanonicalName:  macroName + "(" + tableName + ")",
				Kind:           tableKind,
				Language:       analysisLanguageForExtension(file.Extension),
				File:           file.Path,
				Module:         unrealModuleForFile(snapshot, file.Path),
				BuildContextID: firstSliceValue(buildContextIDsForFile(snapshot, file.Path)),
				Signature:      analysisTrimSignature(text[match[0] : closeParen+1]),
				StartLine:      startLine,
				EndLine:        analysisLineNumberAt(text, closeParen),
				Tags:           tags,
				Attributes: map[string]string{
					"line_start":    strconv.Itoa(startLine),
					"line_end":      strconv.Itoa(analysisLineNumberAt(text, closeParen)),
					"macro_name":    macroName,
					"source_kind":   "macro_invocation",
					"variant_index": strconv.Itoa(variantIndex),
				},
			}
			calls := registrationCallbackEdgesFromText(symbol, descriptor, tableName, macroName, edgeType, lookup)
			if len(calls) == 0 {
				continue
			}
			key := symbol.ID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out.Symbols = append(out.Symbols, symbol)
			out.Overlays = append(out.Overlays, sourceAnchorOverlays(symbol)...)
			out.Calls = append(out.Calls, calls...)
		}
	}
	return out
}

func analysisIndexInPreprocessorDirective(text string, index int) bool {
	if index < 0 {
		return false
	}
	index = minInt(index, len(text))
	lineStart := strings.LastIndex(text[:index], "\n")
	if lineStart < 0 {
		lineStart = 0
	} else {
		lineStart++
	}
	for {
		lineEnd := strings.Index(text[lineStart:], "\n")
		if lineEnd < 0 {
			lineEnd = len(text)
		} else {
			lineEnd += lineStart
		}
		line := strings.TrimSpace(text[lineStart:lineEnd])
		if strings.HasPrefix(line, "#") {
			return true
		}
		if lineStart == 0 {
			return false
		}
		prevEnd := lineStart - 1
		prevStart := strings.LastIndex(text[:prevEnd], "\n")
		if prevStart < 0 {
			prevStart = 0
		} else {
			prevStart++
		}
		prevLine := strings.TrimRight(text[prevStart:prevEnd], " \t\r")
		if !strings.HasSuffix(prevLine, "\\") {
			return false
		}
		lineStart = prevStart
	}
}

func expandRegistrationMacroInvocation(name string, args []string, definitions map[string][]registrationMacroDefinition, depth int) string {
	return strings.Join(expandRegistrationMacroInvocationVariants(name, args, definitions, depth), " ")
}

func expandRegistrationMacroInvocationVariants(name string, args []string, definitions map[string][]registrationMacroDefinition, depth int) []string {
	if depth > 4 || len(definitions) == 0 {
		return nil
	}
	defs := definitions[strings.TrimSpace(name)]
	if len(defs) == 0 {
		return nil
	}
	out := []string{}
	for _, def := range defs {
		if expanded := expandRegistrationMacroDefinitionVariant(def, args, definitions, depth); strings.TrimSpace(expanded) != "" {
			out = append(out, expanded)
		}
	}
	return analysisUniqueStrings(out)
}

func expandRegistrationMacroDefinitionVariant(def registrationMacroDefinition, args []string, definitions map[string][]registrationMacroDefinition, depth int) string {
	replacements := map[string]string{}
	variadicValue := ""
	for index, param := range def.Params {
		param = strings.TrimSpace(param)
		if param == "" {
			continue
		}
		if param == "..." {
			if index < len(args) {
				variadicValue = strings.Join(args[index:], ", ")
			}
			replacements["__VA_ARGS__"] = variadicValue
			continue
		}
		if strings.HasSuffix(param, "...") {
			name := strings.TrimSuffix(param, "...")
			name = strings.TrimSpace(name)
			if index < len(args) {
				variadicValue = strings.Join(args[index:], ", ")
			}
			replacements[name] = variadicValue
			replacements["__VA_ARGS__"] = variadicValue
			continue
		}
		if index >= len(args) {
			continue
		}
		replacements[param] = strings.TrimSpace(args[index])
	}
	body := def.Body
	for param, value := range replacements {
		paramRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(param) + `\b`)
		body = paramRe.ReplaceAllString(body, value)
	}
	body = normalizeRegistrationTokenPaste(body)
	nested := expandRegistrationNestedMacros(body, definitions, depth+1)
	if strings.TrimSpace(nested) != "" {
		body += " " + nested
	}
	return body
}

func expandRegistrationNestedMacros(text string, definitions map[string][]registrationMacroDefinition, depth int) string {
	if depth > 4 {
		return ""
	}
	callRe := regexp.MustCompile(`(?is)\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	matches := callRe.FindAllStringSubmatchIndex(text, -1)
	out := []string{}
	for _, match := range matches {
		if len(match) < 4 || match[2] < 0 {
			continue
		}
		name := strings.TrimSpace(text[match[2]:match[3]])
		if len(definitions[name]) == 0 {
			continue
		}
		openParen := match[1] - 1
		closeParen := analysisMatchClosingParen(text, openParen)
		if closeParen <= openParen {
			continue
		}
		for _, expanded := range expandRegistrationMacroInvocationVariants(name, splitCStyleCallArguments(text[openParen+1:closeParen]), definitions, depth+1) {
			if strings.TrimSpace(expanded) != "" {
				out = append(out, expanded)
			}
		}
	}
	return strings.Join(out, " ")
}

func normalizeRegistrationTokenPaste(text string) string {
	pasteRe := regexp.MustCompile(`\s*##\s*`)
	for strings.Contains(text, "##") {
		next := pasteRe.ReplaceAllString(text, "")
		if next == text {
			break
		}
		text = next
	}
	return text
}

func registrationCallbackEdgesFromText(symbol SymbolRecord, body string, tableName string, tableType string, edgeType string, lookup anchorSymbolLookup) []CallEdge {
	tokenRe := regexp.MustCompile(`(?:[A-Za-z_][A-Za-z0-9_]*::)*[A-Za-z_][A-Za-z0-9_]*`)
	out := []CallEdge{}
	seenTargets := map[string]struct{}{}
	for _, token := range tokenRe.FindAllString(body, -1) {
		token = analysisNormalizeCStyleQualifiedName(strings.TrimPrefix(strings.TrimSpace(token), "&"))
		if registrationTableIgnoredToken(token, tableName, tableType) {
			continue
		}
		target, ok := resolveAnchorCallTarget(token, symbol, lookup)
		if !ok || target.ID == symbol.ID {
			continue
		}
		key := edgeType + "|" + target.ID
		if _, exists := seenTargets[key]; exists {
			continue
		}
		seenTargets[key] = struct{}{}
		out = append(out, CallEdge{
			SourceID: symbol.ID,
			TargetID: target.ID,
			Type:     edgeType,
			Evidence: []string{symbol.File},
		})
	}
	return out
}

func classifyRegistrationTable(descriptor string) (string, string, []string) {
	upper := strings.ToUpper(strings.TrimSpace(descriptor))
	switch {
	case strings.Contains(upper, "OB_OPERATION_REGISTRATION") ||
		strings.Contains(upper, "OB_OPERATION_") ||
		strings.Contains(upper, "PSPROCESSTYPE") ||
		strings.Contains(upper, "PSTHREADTYPE") ||
		strings.Contains(upper, "OBJECT") ||
		strings.Contains(upper, "HANDLE"):
		return "object_callback_table", "registers_object_callback", []string{"registration_table", "callback_surface", "handle_surface", "security_boundary"}
	case strings.Contains(upper, "FLT_OPERATION_REGISTRATION") ||
		strings.Contains(upper, "IRP_MJ_") ||
		strings.Contains(upper, "MINIFILTER") ||
		strings.Contains(upper, "FILTER"):
		return "file_filter_callback_table", "registers_file_filter_callback", []string{"registration_table", "callback_surface", "file_filter_surface", "security_boundary"}
	case strings.Contains(upper, "PROCESS") && strings.Contains(upper, "NOTIFY"):
		return "notify_callback_table", "registers_process_notify_callback", []string{"registration_table", "callback_surface", "process_surface", "security_boundary"}
	default:
		return "registration_table", "registers_callback_table_entry", []string{"registration_table", "callback_surface"}
	}
}

func buildRegistrationTableAnchorID(kind string, tableName string, file string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "registration_table"
	}
	return kind + ":" + strings.TrimSpace(tableName) + "@" + strings.TrimSpace(file)
}

func buildRegistrationMacroAnchorID(kind string, macroName string, tableName string, file string, line int) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "registration_macro"
	}
	name := firstNonBlankString(strings.TrimSpace(tableName), strings.TrimSpace(macroName))
	return kind + ":" + name + "@" + strings.TrimSpace(file) + ":" + strconv.Itoa(line)
}

func registrationDescriptorLooksRelevant(descriptor string) bool {
	lower := strings.ToLower(strings.TrimSpace(descriptor))
	return containsAny(lower,
		"ob_operation_registration",
		"flt_operation_registration",
		"ob_operation_",
		"irp_mj_",
		"psprocesstype",
		"psthreadtype",
		"object",
		"handle",
		"callback",
		"callbacks",
		"registration",
		"register",
		"table",
		"operation",
		"operations",
		"file",
		"minifilter",
		"filter",
		"notify",
	)
}

func registrationMacroNameLooksRelevant(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || analysisIgnoredCallToken(trimmed) {
		return false
	}
	lower := strings.ToLower(trimmed)
	if !registrationDescriptorLooksRelevant(trimmed) {
		return false
	}
	macroLike := trimmed == strings.ToUpper(trimmed) ||
		strings.Contains(trimmed, "_") ||
		strings.HasPrefix(lower, "declare") ||
		strings.HasPrefix(lower, "define") ||
		strings.HasPrefix(lower, "init") ||
		strings.HasPrefix(lower, "make") ||
		strings.HasPrefix(lower, "register")
	if !macroLike {
		return false
	}
	return !containsAny(lower, "sizeof", "offsetof")
}

func registrationMacroAnchorName(macroName string, args string, lookup anchorSymbolLookup) string {
	tokenRe := regexp.MustCompile(`(?:[A-Za-z_][A-Za-z0-9_]*::)*[A-Za-z_][A-Za-z0-9_]*`)
	for _, arg := range splitCStyleCallArguments(args) {
		for _, token := range tokenRe.FindAllString(arg, -1) {
			token = analysisNormalizeCStyleQualifiedName(strings.TrimSpace(token))
			if registrationTableIgnoredToken(token, macroName, macroName) {
				continue
			}
			if _, ok := resolveAnchorCallTarget(token, SymbolRecord{}, lookup); ok {
				continue
			}
			return token
		}
	}
	return ""
}

func registrationTableIgnoredToken(token string, tableName string, tableType string) bool {
	normalized := strings.TrimSpace(token)
	if normalized == "" || analysisIgnoredCallToken(normalized) {
		return true
	}
	shortName := normalized
	if strings.Contains(shortName, "::") {
		shortName = shortName[strings.LastIndex(shortName, "::")+2:]
	}
	lower := strings.ToLower(shortName)
	upper := strings.ToUpper(shortName)
	if strings.EqualFold(shortName, tableName) || strings.EqualFold(shortName, tableType) {
		return true
	}
	switch lower {
	case "null", "nullptr", "true", "false", "const", "static", "extern", "volatile", "sizeof", "array_size", "psprocesstype", "psthreadtype", "exdesktopobjecttype":
		return true
	}
	switch upper {
	case "NULL", "TRUE", "FALSE", "OB_OPERATION_REGISTRATION", "FLT_OPERATION_REGISTRATION", "IRP_MJ_OPERATION_END", "FLT_REGISTRATION", "OB_CALLBACK_REGISTRATION":
		return true
	}
	prefixes := []string{
		"IRP_MJ_",
		"FLTFL_",
		"OB_OPERATION_",
		"FLT_OPERATION_",
		"FLT_REGISTRATION_",
		"FLTFL_OPERATION_REGISTRATION_",
		"FLTFL_REGISTRATION_",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(upper, strings.ToUpper(prefix)) {
			return true
		}
	}
	return false
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
	addEdge := func(target SymbolRecord, edgeType string) {
		if target.ID == anchor.Symbol.ID {
			return
		}
		key := anchor.Symbol.ID + "|" + edgeType + "|" + target.ID
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, CallEdge{
			SourceID: anchor.Symbol.ID,
			TargetID: target.ID,
			Type:     edgeType,
			Evidence: []string{anchor.Symbol.File},
		})
	}
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
		callType := "calls"
		switch strings.TrimSpace(target.Kind) {
		case "rpc", "rpc_handler":
			callType = "dispatches_rpc"
		case "ioctl_handler":
			callType = "dispatches_ioctl"
		}
		addEdge(target, callType)
	}
	for _, edge := range collectAnchorRegistrationEdges(anchor, lookup) {
		target := SymbolRecord{ID: edge.TargetID}
		addEdge(target, edge.Type)
	}
	return out
}

func collectAnchorRegistrationEdges(anchor sourceFunctionAnchor, lookup anchorSymbolLookup) []CallEdge {
	body := anchor.Body
	out := []CallEdge{}
	seen := map[string]struct{}{}
	add := func(token string, edgeType string) {
		token = analysisNormalizeCStyleQualifiedName(strings.TrimPrefix(strings.TrimSpace(token), "&"))
		if token == "" || analysisIgnoredCallToken(token) {
			return
		}
		target, ok := resolveAnchorCallTarget(token, anchor.Symbol, lookup)
		if !ok || target.ID == anchor.Symbol.ID {
			return
		}
		key := anchor.Symbol.ID + "|" + edgeType + "|" + target.ID
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, CallEdge{
			SourceID: anchor.Symbol.ID,
			TargetID: target.ID,
			Type:     edgeType,
			Evidence: []string{anchor.Symbol.File},
		})
	}

	namePattern := `(?:[A-Za-z_][A-Za-z0-9_]*::)*~?[A-Za-z_][A-Za-z0-9_]*`
	majorFunctionRe := regexp.MustCompile(`(?is)\bMajorFunction\s*\[\s*IRP_MJ_[A-Z0-9_]+\s*\]\s*=\s*&?\s*(` + namePattern + `)`)
	for _, match := range majorFunctionRe.FindAllStringSubmatch(body, -1) {
		if len(match) >= 2 {
			add(match[1], "registers_irp_dispatch")
		}
	}

	callbackAssignmentRe := regexp.MustCompile(`(?is)(?:\.|->|\b)(PreOperation|PostOperation|DriverUnload|UnloadCallback|InstanceSetup|InstanceQueryTeardown|InstanceTeardownStart|InstanceTeardownComplete|GenerateFileNameCallback|NormalizeNameComponentCallback)\s*=\s*&?\s*(` + namePattern + `)`)
	for _, match := range callbackAssignmentRe.FindAllStringSubmatch(body, -1) {
		if len(match) < 3 {
			continue
		}
		add(match[2], callbackAssignmentEdgeType(match[1]))
	}

	for _, call := range collectRegistrationCallArguments(body) {
		edgeType := registrationCallEdgeType(call.Name)
		if edgeType == "" {
			continue
		}
		for _, arg := range limitStrings(call.Arguments, 2) {
			token := firstResolvableCallbackToken(arg)
			if token != "" {
				add(token, edgeType)
			}
		}
	}
	return out
}

type registrationCallArguments struct {
	Name      string
	Arguments []string
}

func collectRegistrationCallArguments(body string) []registrationCallArguments {
	namePattern := `(?:[A-Za-z_][A-Za-z0-9_]*::)*[A-Za-z_][A-Za-z0-9_]*`
	callRe := regexp.MustCompile(`(?is)\b(` + namePattern + `)\s*\(`)
	matches := callRe.FindAllStringSubmatchIndex(body, -1)
	out := []registrationCallArguments{}
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		name := strings.TrimSpace(body[match[2]:match[3]])
		if registrationCallEdgeType(name) == "" {
			continue
		}
		open := match[1] - 1
		close := analysisMatchClosingParen(body, open)
		if close <= open {
			continue
		}
		args := splitCStyleCallArguments(body[open+1 : close])
		out = append(out, registrationCallArguments{Name: name, Arguments: args})
	}
	return out
}

func analysisMatchClosingParen(text string, open int) int {
	if open < 0 || open >= len(text) || text[open] != '(' {
		return -1
	}
	depth := 0
	inString := false
	inRune := false
	escaped := false
	for index := open; index < len(text); index++ {
		ch := text[index]
		if inString || inRune {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if inString && ch == '"' {
				inString = false
			}
			if inRune && ch == '\'' {
				inRune = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '\'':
			inRune = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func splitCStyleCallArguments(args string) []string {
	out := []string{}
	start := 0
	depthParen := 0
	depthBrace := 0
	depthBracket := 0
	inString := false
	inRune := false
	escaped := false
	for index := 0; index < len(args); index++ {
		ch := args[index]
		if inString || inRune {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if inString && ch == '"' {
				inString = false
			}
			if inRune && ch == '\'' {
				inRune = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '\'':
			inRune = true
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '{':
			depthBrace++
		case '}':
			if depthBrace > 0 {
				depthBrace--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		case ',':
			if depthParen == 0 && depthBrace == 0 && depthBracket == 0 {
				out = append(out, strings.TrimSpace(args[start:index]))
				start = index + 1
			}
		}
	}
	tail := strings.TrimSpace(args[start:])
	if tail != "" {
		out = append(out, tail)
	}
	return out
}

func callbackAssignmentEdgeType(field string) string {
	lower := strings.ToLower(strings.TrimSpace(field))
	switch {
	case strings.Contains(lower, "driverunload") || strings.Contains(lower, "unloadcallback"):
		return "registers_unload_callback"
	case strings.Contains(lower, "preoperation") || strings.Contains(lower, "postoperation"):
		return "assigns_operation_callback"
	case strings.Contains(lower, "instance"):
		return "assigns_filter_instance_callback"
	default:
		return "assigns_callback"
	}
}

func registrationCallEdgeType(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(lower, "pssetcreateprocessnotifyroutine"):
		return "registers_process_notify_callback"
	case strings.Contains(lower, "pssetcreatethreadnotifyroutine"):
		return "registers_thread_notify_callback"
	case strings.Contains(lower, "pssetloadimagenotifyroutine"):
		return "registers_image_notify_callback"
	case strings.Contains(lower, "cmregistercallback") || strings.Contains(lower, "cmregistercallbackex"):
		return "registers_registry_callback"
	case strings.Contains(lower, "setnotifyroutine"):
		return "registers_notify_callback"
	default:
		return ""
	}
}

func firstResolvableCallbackToken(arg string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return ""
	}
	arg = strings.TrimPrefix(arg, "&")
	if strings.EqualFold(arg, "NULL") || strings.EqualFold(arg, "nullptr") || arg == "0" || strings.EqualFold(arg, "FALSE") || strings.EqualFold(arg, "TRUE") {
		return ""
	}
	tokenRe := regexp.MustCompile(`(?:[A-Za-z_][A-Za-z0-9_]*::)*~?[A-Za-z_][A-Za-z0-9_]*`)
	for _, token := range tokenRe.FindAllString(arg, -1) {
		if analysisIgnoredCallToken(token) {
			continue
		}
		lower := strings.ToLower(token)
		if lower == "null" || lower == "nullptr" || lower == "false" || lower == "true" {
			continue
		}
		return token
	}
	return ""
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
