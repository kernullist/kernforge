package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	structuralIndexSchemaVersion = "structural_index.phase2.v1"
	structuralParserTreeSitter   = "tree-sitter"
	structuralParserFallback     = "heuristic-fallback"
)

type StructuralIndex struct {
	SchemaVersion  string                        `json:"schema_version,omitempty"`
	RunID          string                        `json:"run_id"`
	Goal           string                        `json:"goal,omitempty"`
	Root           string                        `json:"root"`
	GeneratedAt    time.Time                     `json:"generated_at"`
	Files          []FileRecord                  `json:"files,omitempty"`
	Symbols        []SymbolRecord                `json:"symbols,omitempty"`
	References     []ReferenceRecord             `json:"references,omitempty"`
	CallEdges      []CallEdge                    `json:"call_edges,omitempty"`
	Diagnostics    []StructuralIndexDiagnostic   `json:"diagnostics,omitempty"`
	Metrics        StructuralIndexMetrics        `json:"metrics,omitempty"`
	AdapterNotes   []string                      `json:"adapter_notes,omitempty"`
	PacketCoverage AnalysisPacketCoverageMetrics `json:"packet_coverage,omitempty"`
}

type StructuralIndexMetrics struct {
	IndexedFiles        int `json:"indexed_files,omitempty"`
	IndexedSymbols      int `json:"indexed_symbols,omitempty"`
	IndexedReferences   int `json:"indexed_references,omitempty"`
	ParserFailures      int `json:"parser_failures,omitempty"`
	FallbackFiles       int `json:"fallback_files,omitempty"`
	UnsupportedFiles    int `json:"unsupported_files,omitempty"`
	TreeSitterFiles     int `json:"tree_sitter_files,omitempty"`
	SymbolAnchoredFiles int `json:"symbol_anchored_files,omitempty"`
}

type StructuralIndexDiagnostic struct {
	Path     string `json:"path,omitempty"`
	Parser   string `json:"parser,omitempty"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type AnalysisPacketCoverageMetrics struct {
	TotalClaims                     int     `json:"total_claims,omitempty"`
	ClaimsWithPackets               int     `json:"claims_with_packets,omitempty"`
	ClaimPacketCoverageRatio        float64 `json:"claim_packet_coverage_ratio,omitempty"`
	TotalPackets                    int     `json:"total_packets,omitempty"`
	PacketsWithSymbolAnchor         int     `json:"packets_with_symbol_anchor,omitempty"`
	PacketSymbolAnchorCoverageRatio float64 `json:"packet_symbol_anchor_coverage_ratio,omitempty"`
}

type structuralFileExtraction struct {
	Parser       string
	Status       string
	Diagnostic   string
	Symbols      []SymbolRecord
	References   []ReferenceRecord
	CallEdges    []CallEdge
	Diagnostics  []StructuralIndexDiagnostic
	FallbackUsed bool
}

type structuralIndexAdapter interface {
	Name() string
	Supports(ScannedFile) bool
	Extract(ProjectSnapshot, ScannedFile, string) (structuralFileExtraction, error)
}

type heuristicStructuralIndexAdapter struct{}

func buildStructuralIndex(snapshot ProjectSnapshot, goal string, runID string) StructuralIndex {
	index := StructuralIndex{
		SchemaVersion: structuralIndexSchemaVersion,
		RunID:         strings.TrimSpace(runID),
		Goal:          strings.TrimSpace(goal),
		Root:          snapshot.Root,
		GeneratedAt:   snapshot.GeneratedAt,
		AdapterNotes:  structuralIndexAdapterNotes(),
	}
	if index.GeneratedAt.IsZero() {
		index.GeneratedAt = time.Now()
	}

	adapters := []structuralIndexAdapter{}
	if adapter := optionalTreeSitterStructuralIndexAdapter(); adapter != nil {
		adapters = append(adapters, adapter)
	}
	adapters = append(adapters, heuristicStructuralIndexAdapter{})

	symbolSeen := map[string]struct{}{}
	referenceSeen := map[string]struct{}{}
	callSeen := map[string]struct{}{}
	fileRecords := []FileRecord{}

	for _, file := range snapshot.Files {
		record := structuralIndexFileRecord(snapshot, file)
		if !analysisSupportsStructuralIndex(file.Extension) {
			record.ParserStatus = "unsupported"
			record.ParserDiagnostic = "unsupported language or file kind"
			index.Metrics.UnsupportedFiles++
			fileRecords = append(fileRecords, record)
			continue
		}

		abs := filepath.Join(snapshot.Root, filepath.FromSlash(file.Path))
		data, err := os.ReadFile(abs)
		if err != nil {
			record.Parser = structuralParserFallback
			record.ParserStatus = "failed"
			record.ParserDiagnostic = err.Error()
			index.Metrics.ParserFailures++
			index.Diagnostics = append(index.Diagnostics, StructuralIndexDiagnostic{
				Path:     file.Path,
				Parser:   structuralParserFallback,
				Severity: "error",
				Reason:   "read_failed",
				Detail:   err.Error(),
			})
			fileRecords = append(fileRecords, record)
			continue
		}

		extraction, ok := runStructuralIndexAdapters(snapshot, file, string(data), adapters)
		record.Parser = extraction.Parser
		record.ParserStatus = firstNonBlankAnalysisString(extraction.Status, "indexed")
		record.ParserDiagnostic = extraction.Diagnostic
		record.SymbolCount = len(extraction.Symbols)
		record.ReferenceCount = len(extraction.References) + len(extraction.CallEdges)
		if extraction.FallbackUsed {
			index.Metrics.FallbackFiles++
		}
		if strings.EqualFold(extraction.Parser, structuralParserTreeSitter) {
			index.Metrics.TreeSitterFiles++
		}
		if !ok {
			index.Metrics.ParserFailures++
		}
		if len(extraction.Symbols) > 0 {
			index.Metrics.SymbolAnchoredFiles++
		}
		if record.ParserStatus == "indexed" || len(extraction.Symbols) > 0 || len(extraction.References) > 0 || len(extraction.CallEdges) > 0 {
			index.Metrics.IndexedFiles++
		}
		index.Diagnostics = append(index.Diagnostics, extraction.Diagnostics...)
		for _, symbol := range extraction.Symbols {
			addStructuralSymbol(&index, symbol, symbolSeen)
		}
		for _, ref := range extraction.References {
			addStructuralReference(&index, ref, referenceSeen)
		}
		for _, edge := range extraction.CallEdges {
			addStructuralCallEdge(&index, edge, callSeen)
		}
		fileRecords = append(fileRecords, record)
	}

	seedSymbols := map[string]SymbolRecord{}
	for _, symbol := range index.Symbols {
		seedSymbols[symbol.ID] = symbol
	}
	sourceExtraction := collectSourceAnchorsV2(snapshot, seedSymbols)
	for _, edge := range sourceExtraction.Calls {
		addStructuralCallEdge(&index, edge, callSeen)
	}

	index.Files = fileRecords
	index.Metrics.IndexedSymbols = len(index.Symbols)
	index.Metrics.IndexedReferences = len(index.References) + len(index.CallEdges)
	return normalizeStructuralIndex(index)
}

func structuralIndexAdapterNotes() []string {
	notes := []string{
		"Tree-sitter adapter is preferred when built with the kernforge_treesitter tag and cgo is available.",
		"Default Windows builds keep a heuristic fallback so analyze-project remains usable when cgo is disabled.",
	}
	if note := optionalTreeSitterStructuralIndexNote(); strings.TrimSpace(note) != "" {
		notes = append(notes, note)
	}
	return analysisUniqueStrings(notes)
}

func runStructuralIndexAdapters(snapshot ProjectSnapshot, file ScannedFile, text string, adapters []structuralIndexAdapter) (structuralFileExtraction, bool) {
	failures := []StructuralIndexDiagnostic{}
	for _, adapter := range adapters {
		if adapter == nil || !adapter.Supports(file) {
			continue
		}
		extraction, err := adapter.Extract(snapshot, file, text)
		if err != nil {
			failures = append(failures, StructuralIndexDiagnostic{
				Path:     file.Path,
				Parser:   adapter.Name(),
				Severity: "warning",
				Reason:   "adapter_failed",
				Detail:   err.Error(),
			})
			continue
		}
		if adapter.Name() != structuralParserFallback && len(extraction.Symbols) == 0 && len(extraction.References) == 0 && len(extraction.CallEdges) == 0 {
			failures = append(failures, StructuralIndexDiagnostic{
				Path:     file.Path,
				Parser:   adapter.Name(),
				Severity: "warning",
				Reason:   "adapter_empty",
				Detail:   firstNonBlankAnalysisString(extraction.Diagnostic, "adapter produced no structural records"),
			})
			continue
		}
		extraction.Diagnostics = append(failures, extraction.Diagnostics...)
		if adapter.Name() != structuralParserTreeSitter && len(failures) > 0 {
			extraction.FallbackUsed = true
		}
		if adapter.Name() == structuralParserFallback {
			extraction.FallbackUsed = true
		}
		return extraction, true
	}
	return structuralFileExtraction{
		Parser:     structuralParserFallback,
		Status:     "failed",
		Diagnostic: "no structural index adapter accepted the file",
		Diagnostics: append(failures, StructuralIndexDiagnostic{
			Path:     file.Path,
			Parser:   structuralParserFallback,
			Severity: "error",
			Reason:   "no_adapter",
			Detail:   "no structural index adapter accepted the file",
		}),
	}, false
}

func (heuristicStructuralIndexAdapter) Name() string {
	return structuralParserFallback
}

func (heuristicStructuralIndexAdapter) Supports(file ScannedFile) bool {
	return analysisSupportsStructuralIndex(file.Extension)
}

func (heuristicStructuralIndexAdapter) Extract(snapshot ProjectSnapshot, file ScannedFile, text string) (structuralFileExtraction, error) {
	out := structuralFileExtraction{
		Parser:       structuralParserFallback,
		Status:       "indexed",
		FallbackUsed: true,
	}
	switch analysisLanguageForExtension(file.Extension) {
	case "go":
		out.Symbols = append(out.Symbols, structuralSymbolsFromAnchors(extractGoFunctionAnchors(snapshot, file, text), text, structuralParserFallback)...)
		out.Symbols = append(out.Symbols, extractGoTypeStructuralSymbols(snapshot, file, text)...)
	case "cpp":
		out.Symbols = append(out.Symbols, structuralSymbolsFromAnchors(extractCStyleFunctionAnchors(snapshot, file, text), text, structuralParserFallback)...)
		out.Symbols = append(out.Symbols, extractCStyleDeclarationStructuralSymbols(snapshot, file, text)...)
	case "csharp":
		out.Symbols = append(out.Symbols, structuralSymbolsFromAnchors(extractCSharpFunctionAnchors(snapshot, file, text), text, structuralParserFallback)...)
		out.Symbols = append(out.Symbols, extractCStyleDeclarationStructuralSymbols(snapshot, file, text)...)
	default:
		out.Status = "unsupported"
		out.Diagnostic = "unsupported structural language"
		return out, nil
	}
	out.References = append(out.References, structuralFileImportReferences(file)...)
	if len(out.Symbols) == 0 && len(out.References) == 0 {
		out.Status = "fallback_empty"
		out.Diagnostic = "fallback parser found no symbols or references"
	}
	return out, nil
}

func structuralIndexFileRecord(snapshot ProjectSnapshot, file ScannedFile) FileRecord {
	tags := []string{}
	if file.IsManifest {
		tags = append(tags, "manifest")
	}
	if file.IsEntrypoint {
		tags = append(tags, "entrypoint")
	}
	tags = append(tags, limitStrings(file.ImportanceReasons, 6)...)
	moduleHints := []string{}
	if module := unrealModuleForFile(snapshot, file.Path); strings.TrimSpace(module) != "" {
		moduleHints = append(moduleHints, module)
	}
	return FileRecord{
		Path:            file.Path,
		Directory:       file.Directory,
		Extension:       file.Extension,
		Language:        analysisLanguageForExtension(file.Extension),
		LineCount:       file.LineCount,
		IsManifest:      file.IsManifest,
		IsEntrypoint:    file.IsEntrypoint,
		ImportanceScore: file.ImportanceScore,
		Tags:            analysisUniqueStrings(tags),
		ModuleHints:     analysisUniqueStrings(moduleHints),
		BuildContextIDs: buildContextIDsForFile(snapshot, file.Path),
	}
}

func analysisSupportsStructuralIndex(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hh", ".inl", ".cs":
		return true
	default:
		return false
	}
}

func structuralSymbolsFromAnchors(anchors []sourceFunctionAnchor, text string, method string) []SymbolRecord {
	out := []SymbolRecord{}
	for _, anchor := range anchors {
		symbol := anchor.Symbol
		symbol.ExtractionMethod = valueOrDefault(firstNonBlankAnalysisString(method, symbol.ExtractionMethod), "source_anchor")
		if symbol.StartByte == 0 && symbol.StartLine > 0 {
			symbol.StartByte = analysisByteOffsetForLine(text, symbol.StartLine)
		}
		if symbol.EndByte == 0 && symbol.EndLine > 0 {
			symbol.EndByte = analysisByteOffsetForLineEnd(text, symbol.EndLine)
		}
		if symbol.Attributes == nil {
			symbol.Attributes = map[string]string{}
		}
		symbol.Attributes["anchor_source"] = "structural_index"
		out = append(out, symbol)
	}
	return out
}

func extractGoTypeStructuralSymbols(snapshot ProjectSnapshot, file ScannedFile, text string) []SymbolRecord {
	re := regexp.MustCompile(`(?m)^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(struct|interface)\b`)
	matches := re.FindAllStringSubmatchIndex(text, -1)
	out := []SymbolRecord{}
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		name := strings.TrimSpace(text[match[2]:match[3]])
		kind := strings.TrimSpace(text[match[4]:match[5]])
		if name == "" || kind == "" {
			continue
		}
		startLine := analysisLineNumberAt(text, match[0])
		endLine := structuralDeclarationEndLine(text, match[1])
		out = append(out, SymbolRecord{
			ID:               buildSourceAnchorID(kind, name, file.Path),
			Name:             name,
			CanonicalName:    name,
			Kind:             kind,
			Language:         "go",
			File:             file.Path,
			Module:           unrealModuleForFile(snapshot, file.Path),
			Signature:        analysisTrimSignature(text[match[0]:analysisMinInt(match[1]+160, len(text))]),
			StartLine:        startLine,
			EndLine:          endLine,
			StartByte:        match[0],
			EndByte:          analysisByteOffsetForLineEnd(text, endLine),
			ExtractionMethod: structuralParserFallback,
			Tags:             analysisUniqueStrings([]string{"type_declaration", kind}),
		})
	}
	return out
}

func extractCStyleDeclarationStructuralSymbols(snapshot ProjectSnapshot, file ScannedFile, text string) []SymbolRecord {
	masked := analysisMaskCommentsAndStrings(text)
	out := []SymbolRecord{}
	out = append(out, extractCStyleScopeSymbols(snapshot, file, text, masked)...)
	out = append(out, extractCStyleMacroSymbols(file, text)...)
	return out
}

func extractCStyleScopeSymbols(snapshot ProjectSnapshot, file ScannedFile, text string, masked string) []SymbolRecord {
	scopeRe := regexp.MustCompile(`(?m)\b(namespace|class|struct|enum)\s+([A-Za-z_][A-Za-z0-9_:]*)`)
	matches := scopeRe.FindAllStringSubmatchIndex(masked, -1)
	out := []SymbolRecord{}
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		kind := strings.TrimSpace(masked[match[2]:match[3]])
		name := strings.TrimSpace(masked[match[4]:match[5]])
		if kind == "" || name == "" || analysisIgnoredCallToken(name) {
			continue
		}
		startLine := analysisLineNumberAt(text, match[0])
		endLine := structuralDeclarationEndLine(text, match[1])
		canonicalName := analysisNormalizeCStyleQualifiedName(name)
		out = append(out, SymbolRecord{
			ID:               buildSourceAnchorID(kind, canonicalName, file.Path),
			Name:             analysisShortCStyleName(canonicalName),
			CanonicalName:    canonicalName,
			Kind:             kind,
			Language:         analysisLanguageForExtension(file.Extension),
			File:             file.Path,
			Module:           unrealModuleForFile(snapshot, file.Path),
			Signature:        analysisTrimSignature(text[match[0]:analysisMinInt(match[1]+180, len(text))]),
			StartLine:        startLine,
			EndLine:          endLine,
			StartByte:        match[0],
			EndByte:          analysisByteOffsetForLineEnd(text, endLine),
			ExtractionMethod: structuralParserFallback,
			Tags:             analysisUniqueStrings([]string{"declaration", kind}),
		})
	}
	return out
}

func extractCStyleMacroSymbols(file ScannedFile, text string) []SymbolRecord {
	re := regexp.MustCompile(`(?m)^\s*#\s*define\s+([A-Za-z_][A-Za-z0-9_]*)(?:\s*\(([^)]*)\))?`)
	matches := re.FindAllStringSubmatchIndex(text, -1)
	out := []SymbolRecord{}
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		name := strings.TrimSpace(text[match[2]:match[3]])
		if name == "" {
			continue
		}
		startLine := analysisLineNumberAt(text, match[0])
		kind := "macro"
		if len(match) >= 6 && match[4] >= 0 {
			kind = "function_macro"
		}
		out = append(out, SymbolRecord{
			ID:               buildSourceAnchorID(kind, name, file.Path),
			Name:             name,
			CanonicalName:    name,
			Kind:             kind,
			Language:         analysisLanguageForExtension(file.Extension),
			File:             file.Path,
			Signature:        strings.TrimSpace(text[match[0]:match[1]]),
			StartLine:        startLine,
			EndLine:          startLine,
			StartByte:        match[0],
			EndByte:          match[1],
			ExtractionMethod: structuralParserFallback,
			Tags:             []string{"macro"},
		})
	}
	return out
}

func structuralFileImportReferences(file ScannedFile) []ReferenceRecord {
	out := []ReferenceRecord{}
	for _, imported := range analysisUniqueStrings(file.Imports) {
		out = append(out, ReferenceRecord{
			SourceFile: file.Path,
			TargetPath: imported,
			Type:       "file_import",
			Evidence:   []string{file.Path},
		})
	}
	return out
}

func structuralDeclarationEndLine(text string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	if offset > len(text) {
		offset = len(text)
	}
	fragment := text[offset:]
	if open := strings.Index(fragment, "{"); open >= 0 {
		absoluteOpen := offset + open
		if close := analysisMatchClosingBrace(text, absoluteOpen); close > absoluteOpen {
			return analysisLineNumberAt(text, close)
		}
	}
	if semi := strings.Index(fragment, ";"); semi >= 0 {
		return analysisLineNumberAt(text, offset+semi)
	}
	return analysisLineNumberAt(text, offset)
}

func analysisByteOffsetForLine(text string, line int) int {
	if line <= 1 {
		return 0
	}
	currentLine := 1
	for index, ch := range text {
		if currentLine == line {
			return index
		}
		if ch == '\n' {
			currentLine++
		}
	}
	return len(text)
}

func analysisByteOffsetForLineEnd(text string, line int) int {
	if line <= 0 {
		return 0
	}
	start := analysisByteOffsetForLine(text, line)
	if start >= len(text) {
		return len(text)
	}
	if next := strings.IndexByte(text[start:], '\n'); next >= 0 {
		return start + next
	}
	return len(text)
}

func addStructuralSymbol(index *StructuralIndex, symbol SymbolRecord, seen map[string]struct{}) {
	symbol.ID = strings.TrimSpace(symbol.ID)
	symbol.Name = strings.TrimSpace(symbol.Name)
	symbol.Kind = strings.TrimSpace(symbol.Kind)
	if symbol.ID == "" || symbol.Name == "" || symbol.Kind == "" {
		return
	}
	if symbol.CanonicalName == "" {
		symbol.CanonicalName = symbol.Name
	}
	symbol.File = filepath.ToSlash(strings.TrimSpace(symbol.File))
	symbol.Tags = analysisUniqueStrings(symbol.Tags)
	if len(symbol.Attributes) == 0 {
		symbol.Attributes = nil
	}
	key := strings.ToLower(symbol.ID)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	index.Symbols = append(index.Symbols, symbol)
}

func addStructuralReference(index *StructuralIndex, ref ReferenceRecord, seen map[string]struct{}) {
	ref.SourceID = strings.TrimSpace(ref.SourceID)
	ref.SourceFile = filepath.ToSlash(strings.TrimSpace(ref.SourceFile))
	ref.TargetID = strings.TrimSpace(ref.TargetID)
	ref.TargetPath = filepath.ToSlash(strings.TrimSpace(ref.TargetPath))
	ref.Type = strings.TrimSpace(ref.Type)
	if ref.Type == "" {
		return
	}
	key := strings.ToLower(strings.Join([]string{ref.SourceID, ref.SourceFile, ref.Type, ref.TargetID, ref.TargetPath}, "|"))
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	ref.Evidence = analysisUniqueStrings(ref.Evidence)
	index.References = append(index.References, ref)
}

func addStructuralCallEdge(index *StructuralIndex, edge CallEdge, seen map[string]struct{}) {
	edge.SourceID = strings.TrimSpace(edge.SourceID)
	edge.TargetID = strings.TrimSpace(edge.TargetID)
	edge.Type = strings.TrimSpace(edge.Type)
	if edge.SourceID == "" || edge.TargetID == "" || edge.Type == "" {
		return
	}
	key := strings.ToLower(edge.SourceID + "|" + edge.Type + "|" + edge.TargetID)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	edge.Evidence = analysisUniqueStrings(edge.Evidence)
	index.CallEdges = append(index.CallEdges, edge)
}

func normalizeStructuralIndex(index StructuralIndex) StructuralIndex {
	sort.Slice(index.Files, func(i int, j int) bool {
		return index.Files[i].Path < index.Files[j].Path
	})
	sort.Slice(index.Symbols, func(i int, j int) bool {
		return index.Symbols[i].ID < index.Symbols[j].ID
	})
	sort.Slice(index.References, func(i int, j int) bool {
		left := index.References[i].Type + "|" + index.References[i].SourceFile + "|" + index.References[i].SourceID + "|" + index.References[i].TargetPath + "|" + index.References[i].TargetID
		right := index.References[j].Type + "|" + index.References[j].SourceFile + "|" + index.References[j].SourceID + "|" + index.References[j].TargetPath + "|" + index.References[j].TargetID
		return left < right
	})
	sort.Slice(index.CallEdges, func(i int, j int) bool {
		left := index.CallEdges[i].SourceID + "|" + index.CallEdges[i].Type + "|" + index.CallEdges[i].TargetID
		right := index.CallEdges[j].SourceID + "|" + index.CallEdges[j].Type + "|" + index.CallEdges[j].TargetID
		return left < right
	})
	sort.Slice(index.Diagnostics, func(i int, j int) bool {
		left := index.Diagnostics[i].Path + "|" + index.Diagnostics[i].Parser + "|" + index.Diagnostics[i].Reason
		right := index.Diagnostics[j].Path + "|" + index.Diagnostics[j].Parser + "|" + index.Diagnostics[j].Reason
		return left < right
	})
	index.AdapterNotes = analysisUniqueStrings(index.AdapterNotes)
	index.Metrics.IndexedSymbols = len(index.Symbols)
	index.Metrics.IndexedReferences = len(index.References) + len(index.CallEdges)
	return index
}

func hasStructuralIndexData(index StructuralIndex) bool {
	return len(index.Files) > 0 ||
		len(index.Symbols) > 0 ||
		len(index.References) > 0 ||
		len(index.CallEdges) > 0 ||
		len(index.Diagnostics) > 0
}

func structuralSymbolsForPaths(index StructuralIndex, paths []string, limit int) []SymbolRecord {
	if limit <= 0 {
		limit = 16
	}
	pathSet := map[string]struct{}{}
	for _, path := range paths {
		pathSet[filepath.ToSlash(strings.TrimSpace(path))] = struct{}{}
	}
	items := []SymbolRecord{}
	for _, symbol := range index.Symbols {
		if _, ok := pathSet[filepath.ToSlash(symbol.File)]; !ok {
			continue
		}
		if !structuralSymbolKindUsefulForEvidence(symbol.Kind) {
			continue
		}
		items = append(items, symbol)
	}
	sort.SliceStable(items, func(i int, j int) bool {
		left := structuralSymbolPriority(items[i])
		right := structuralSymbolPriority(items[j])
		if left == right {
			if items[i].File == items[j].File {
				return items[i].StartLine < items[j].StartLine
			}
			return items[i].File < items[j].File
		}
		return left > right
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return append([]SymbolRecord(nil), items...)
}

func structuralSymbolKindUsefulForEvidence(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "function", "method", "entrypoint", "ioctl", "callback", "security", "memory", "driver", "struct", "class", "namespace", "enum", "macro", "function_macro", "interface":
		return true
	default:
		return strings.Contains(strings.ToLower(kind), "function") || strings.Contains(strings.ToLower(kind), "method")
	}
}

func structuralSymbolPriority(symbol SymbolRecord) int {
	score := 0
	lower := strings.ToLower(strings.Join([]string{symbol.Name, symbol.CanonicalName, symbol.Kind, strings.Join(symbol.Tags, " ")}, " "))
	if containsAny(lower, "driverentry", "driver_entry", "dispatch", "devicecontrol", "ioctl", "irp", "ctl_code") {
		score += 100
	}
	if containsAny(lower, "validate", "probe", "copy", "memory", "handle", "security") {
		score += 70
	}
	if containsAny(lower, "register", "callback", "initialize", "start") {
		score += 45
	}
	if symbol.StartLine > 0 {
		score += 10
	}
	if strings.TrimSpace(symbol.Signature) != "" {
		score += 5
	}
	return score
}

func sourceFunctionAnchorsFromStructuralIndex(snapshot ProjectSnapshot, paths []string) []sourceFunctionAnchor {
	out := []sourceFunctionAnchor{}
	for _, symbol := range structuralSymbolsForPaths(snapshot.StructuralIndex, paths, 256) {
		if symbol.StartLine <= 0 || symbol.EndLine < symbol.StartLine {
			continue
		}
		body := ""
		if text, _, _, _, ok := readEvidenceLineRange(snapshot, symbol.File, symbol.StartLine, symbol.EndLine, analysisEvidencePacketMaxLines); ok {
			body = text
		}
		out = append(out, sourceFunctionAnchor{
			Symbol: symbol,
			Body:   body,
		})
	}
	return out
}

func computeAnalysisPacketCoverage(reports []WorkerReport, packets []EvidencePacket) AnalysisPacketCoverageMetrics {
	metrics := AnalysisPacketCoverageMetrics{
		TotalPackets: len(packets),
	}
	packetSet := map[string]struct{}{}
	for _, packet := range packets {
		if strings.TrimSpace(packet.ID) != "" {
			packetSet[strings.ToLower(strings.TrimSpace(packet.ID))] = struct{}{}
		}
		if strings.TrimSpace(packet.SymbolID) != "" || strings.TrimSpace(packet.SymbolName) != "" || strings.EqualFold(packet.ExtractionMethod, "structural_symbol") {
			metrics.PacketsWithSymbolAnchor++
		}
	}
	for _, report := range reports {
		for _, claim := range report.Claims {
			metrics.TotalClaims++
			for _, id := range claim.EvidencePacketIDs {
				if _, ok := packetSet[strings.ToLower(strings.TrimSpace(id))]; ok {
					metrics.ClaimsWithPackets++
					break
				}
			}
		}
	}
	if metrics.TotalClaims > 0 {
		metrics.ClaimPacketCoverageRatio = float64(metrics.ClaimsWithPackets) / float64(metrics.TotalClaims)
	}
	if metrics.TotalPackets > 0 {
		metrics.PacketSymbolAnchorCoverageRatio = float64(metrics.PacketsWithSymbolAnchor) / float64(metrics.TotalPackets)
	}
	return metrics
}

func renderStructuralIndexPromptSummary(snapshot ProjectSnapshot, shard AnalysisShard, limit int) string {
	symbols := structuralSymbolsForPaths(snapshot.StructuralIndex, append(append([]string(nil), shard.PrimaryFiles...), shard.ReferenceFiles...), limit)
	if len(symbols) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Structural symbol anchors:\n")
	for _, symbol := range symbols {
		location := symbol.File
		if symbol.StartLine > 0 {
			location += ":" + strconv.Itoa(symbol.StartLine)
		}
		fmt.Fprintf(&b, "- %s [%s] %s", firstNonBlankAnalysisString(symbol.CanonicalName, symbol.Name), symbol.Kind, location)
		if strings.TrimSpace(symbol.Signature) != "" {
			fmt.Fprintf(&b, " sig=%s", compactPromptSection(symbol.Signature, 120))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
