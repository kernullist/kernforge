package main

import (
	"fmt"
	"sort"
	"strings"
)

type relevantSemanticIndexV2Hits struct {
	Mode        string
	Files       []FileRecord
	Symbols     []SymbolRecord
	Calls       []CallEdge
	Inheritance []InheritanceEdge
	Builds      []BuildOwnershipEdge
	Overlays    []OverlayEdge
	References  []ReferenceRecord
	Occurrences []SymbolOccurrence
}

func classifyProjectAnalysisQueryMode(query string) string {
	lower := strings.ToLower(strings.TrimSpace(query))
	switch {
	case containsAny(lower,
		"security", "anti-cheat", "anti cheat", "tamper", "integrity", "trust boundary", "authority", "ioctl", "driver", "rpc validation", "handle", "memory read", "memory write",
		"보안", "안티치트", "안티 치트", "무결성", "탬퍼", "신뢰 경계", "권한", "드라이버", "핸들", "메모리 읽", "메모리 쓰"):
		return "security"
	case containsAny(lower,
		"impact", "affected", "affect", "blast radius", "dependency", "what breaks", "if i change",
		"영향", "영향도", "파급", "의존성", "변경하면", "바꾸면", "어디가 깨"):
		return "impact"
	case containsAny(lower,
		"trace", "flow", "path", "caller", "callee", "call chain", "execution chain", "startup flow",
		"트레이스", "흐름", "경로", "호출", "콜체인", "실행 순서"):
		return "trace"
	case containsAny(lower,
		"performance", "perf", "hotspot", "latency", "cpu", "memory pressure", "contention", "slow", "bottleneck",
		"성능", "병목", "핫스팟", "지연", "cpu", "메모리 압박", "경합"):
		return "performance"
	default:
		return "map"
	}
}

func renderRelevantSemanticIndexV2Context(index SemanticIndexV2, query string) string {
	if !hasSemanticIndexV2Data(index) {
		return ""
	}
	hits := collectRelevantSemanticIndexV2Hits(index, query)
	if len(hits.Files) == 0 &&
		len(hits.Symbols) == 0 &&
		len(hits.Calls) == 0 &&
		len(hits.Inheritance) == 0 &&
		len(hits.Builds) == 0 &&
		len(hits.Overlays) == 0 &&
		len(hits.References) == 0 &&
		len(hits.Occurrences) == 0 {
		return ""
	}
	nameByID := semanticIndexV2NameMap(index)

	var b strings.Builder
	b.WriteString("\nRelevant structural index v2 hits:\n")
	fmt.Fprintf(&b, "- query_mode: %s\n", hits.Mode)
	for _, item := range hits.Files {
		line := fmt.Sprintf("- file_v2: %s", strings.TrimSpace(item.Path))
		if item.ImportanceScore > 0 {
			line += fmt.Sprintf(" score=%d", item.ImportanceScore)
		}
		if len(item.ModuleHints) > 0 {
			line += " modules=" + strings.Join(limitStrings(item.ModuleHints, 3), ",")
		}
		if len(item.Tags) > 0 {
			line += " tags=" + strings.Join(limitStrings(item.Tags, 3), ",")
		}
		b.WriteString(line + "\n")
	}
	for _, item := range hits.Symbols {
		line := fmt.Sprintf("- symbol_v2: %s (%s)", strings.TrimSpace(item.Name), strings.TrimSpace(item.Kind))
		if strings.TrimSpace(item.File) != "" {
			line += " file=" + strings.TrimSpace(item.File)
		}
		if strings.TrimSpace(item.Module) != "" {
			line += " module=" + strings.TrimSpace(item.Module)
		}
		if strings.TrimSpace(item.ContainerSymbolID) != "" {
			line += " container=" + semanticIndexV2EntityDisplay(nameByID, item.ContainerSymbolID)
		}
		b.WriteString(line + "\n")
	}
	for _, item := range hits.Overlays {
		line := fmt.Sprintf("- overlay_v2: %s %s -> %s [%s]",
			strings.TrimSpace(item.Domain),
			semanticIndexV2EntityDisplay(nameByID, item.SourceID),
			semanticIndexV2EntityDisplay(nameByID, item.TargetID),
			strings.TrimSpace(item.Type),
		)
		if len(item.Evidence) > 0 {
			line += " evidence=" + strings.Join(limitStrings(item.Evidence, 2), ", ")
		}
		b.WriteString(line + "\n")
	}
	for _, item := range hits.Calls {
		line := fmt.Sprintf("- call_v2: %s -> %s [%s]",
			semanticIndexV2EntityDisplay(nameByID, item.SourceID),
			semanticIndexV2EntityDisplay(nameByID, item.TargetID),
			strings.TrimSpace(item.Type),
		)
		if len(item.Evidence) > 0 {
			line += " evidence=" + strings.Join(limitStrings(item.Evidence, 2), ", ")
		}
		b.WriteString(line + "\n")
	}
	for _, item := range hits.Inheritance {
		b.WriteString(fmt.Sprintf("- inheritance_v2: %s -> %s\n",
			semanticIndexV2EntityDisplay(nameByID, item.SourceID),
			semanticIndexV2EntityDisplay(nameByID, item.TargetID),
		))
	}
	for _, item := range hits.Builds {
		line := fmt.Sprintf("- build_v2: %s -> %s [%s]",
			semanticIndexV2EntityDisplay(nameByID, item.SourceID),
			semanticIndexV2EntityDisplay(nameByID, item.TargetID),
			strings.TrimSpace(item.Type),
		)
		if len(item.Evidence) > 0 {
			line += " evidence=" + strings.Join(limitStrings(item.Evidence, 2), ", ")
		}
		b.WriteString(line + "\n")
	}
	for _, item := range hits.References {
		line := fmt.Sprintf("- ref_v2: %s -> %s [%s]",
			semanticIndexV2EntityDisplay(nameByID, firstNonBlankAnalysisString(item.SourceID, item.SourceFile)),
			semanticIndexV2EntityDisplay(nameByID, firstNonBlankAnalysisString(item.TargetID, item.TargetPath)),
			strings.TrimSpace(item.Type),
		)
		if len(item.Evidence) > 0 {
			line += " evidence=" + strings.Join(limitStrings(item.Evidence, 2), ", ")
		}
		b.WriteString(line + "\n")
	}
	for _, item := range hits.Occurrences {
		b.WriteString(fmt.Sprintf("- occurrence_v2: %s role=%s file=%s\n",
			semanticIndexV2EntityDisplay(nameByID, item.SymbolID),
			strings.TrimSpace(item.Role),
			strings.TrimSpace(item.File),
		))
	}
	return strings.TrimSpace(b.String())
}

func collectRelevantSemanticIndexV2Hits(index SemanticIndexV2, query string) relevantSemanticIndexV2Hits {
	mode := classifyProjectAnalysisQueryMode(query)
	hits := relevantSemanticIndexV2Hits{Mode: mode}
	switch mode {
	case "security":
		hits.Files = selectRelevantV2Files(index, query, mode, 2)
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 4)
		hits.Overlays = selectRelevantV2OverlayEdges(index, query, mode, 4)
		hits.Calls = selectRelevantV2CallEdges(index, query, mode, 2)
		hits.References = selectRelevantV2References(index, query, mode, 2)
	case "trace":
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 4)
		hits.Calls = selectRelevantV2CallEdges(index, query, mode, 4)
		hits.Inheritance = selectRelevantV2InheritanceEdges(index, query, mode, 3)
		hits.Builds = selectRelevantV2BuildEdges(index, query, mode, 4)
		hits.Occurrences = selectRelevantV2Occurrences(index, query, mode, 2)
	case "impact":
		hits.Files = selectRelevantV2Files(index, query, mode, 3)
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 3)
		hits.Builds = selectRelevantV2BuildEdges(index, query, mode, 3)
		hits.References = selectRelevantV2References(index, query, mode, 4)
		hits.Occurrences = selectRelevantV2Occurrences(index, query, mode, 4)
	case "performance":
		hits.Files = selectRelevantV2Files(index, query, mode, 4)
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 3)
		hits.Calls = selectRelevantV2CallEdges(index, query, mode, 2)
	default:
		hits.Files = selectRelevantV2Files(index, query, mode, 3)
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 4)
		hits.Builds = selectRelevantV2BuildEdges(index, query, mode, 3)
		hits.References = selectRelevantV2References(index, query, mode, 2)
	}
	return hits
}

func semanticIndexV2NameMap(index SemanticIndexV2) map[string]string {
	out := map[string]string{}
	for _, item := range index.Symbols {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Name) == "" {
			continue
		}
		out[item.ID] = item.Name
	}
	return out
}

func semanticIndexV2EntityDisplay(nameByID map[string]string, id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	if name, ok := nameByID[trimmed]; ok && strings.TrimSpace(name) != "" {
		return name
	}
	if parts := strings.SplitN(trimmed, ":", 2); len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		return strings.TrimSpace(parts[1])
	}
	return trimmed
}

func analysisV2BaseScore(haystacks []string, loweredQuery string, queryTokens []string, queryRefs []string) int {
	score := 0
	if loweredQuery != "" {
		for _, hay := range haystacks {
			if hay != "" && strings.Contains(hay, loweredQuery) {
				score += 6
			}
		}
	}
	for _, token := range queryTokens {
		for _, hay := range haystacks {
			if hay != "" && token != "" && strings.Contains(hay, token) {
				score++
			}
		}
	}
	for _, ref := range queryRefs {
		for _, hay := range haystacks {
			if hay != "" && ref != "" && strings.Contains(hay, ref) {
				score += 4
			}
		}
	}
	return score
}

func analysisV2QueryInputs(query string) (string, []string, []string) {
	loweredQuery := strings.ToLower(strings.TrimSpace(query))
	queryTokens := filterAnalysisQueryTokens(extractPersistentMemoryTokens(loweredQuery))
	queryRefs := normalizeAnalysisRefs(extractPersistentMemoryReferences(query))
	return loweredQuery, queryTokens, queryRefs
}

func selectRelevantV2Files(index SemanticIndexV2, query string, mode string, limit int) []FileRecord {
	if len(index.Files) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  FileRecord
		score int
		path  string
	}
	items := []scored{}
	for _, item := range index.Files {
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(item.Path)),
			strings.ToLower(strings.TrimSpace(item.Directory)),
			strings.ToLower(strings.TrimSpace(item.Language)),
			strings.ToLower(strings.Join(item.Tags, " ")),
			strings.ToLower(strings.Join(item.ModuleHints, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		score += analysisMinInt(item.ImportanceScore/20, 4)
		if mode == "performance" && (item.IsEntrypoint || containsAny(strings.Join(item.Tags, " "), "startup", "entrypoint")) {
			score += 4
		}
		if score <= 0 {
			continue
		}
		items = append(items, scored{item: item, score: score, path: item.Path})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].path < items[j].path
		}
		return items[i].score > items[j].score
	})
	out := make([]FileRecord, 0, analysisMinInt(limit, len(items)))
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantV2Symbols(index SemanticIndexV2, query string, mode string, limit int) []SymbolRecord {
	if len(index.Symbols) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  SymbolRecord
		score int
		name  string
	}
	items := []scored{}
	for _, item := range index.Symbols {
		attrText := []string{}
		for key, value := range item.Attributes {
			attrText = append(attrText, key+"="+value)
		}
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(item.Name)),
			strings.ToLower(strings.TrimSpace(item.CanonicalName)),
			strings.ToLower(strings.TrimSpace(item.Kind)),
			strings.ToLower(strings.TrimSpace(item.File)),
			strings.ToLower(strings.TrimSpace(item.Module)),
			strings.ToLower(strings.TrimSpace(item.ContainerSymbolID)),
			strings.ToLower(strings.Join(item.Tags, " ")),
			strings.ToLower(strings.Join(attrText, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if mode == "security" && containsAny(strings.ToLower(strings.Join(item.Tags, " ")), "authority", "role:game_mode", "role:player_controller", "unreal_generated") {
			score += 2
		}
		if score <= 0 {
			continue
		}
		items = append(items, scored{item: item, score: score, name: item.Name})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].name < items[j].name
		}
		return items[i].score > items[j].score
	})
	out := make([]SymbolRecord, 0, analysisMinInt(limit, len(items)))
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantV2CallEdges(index SemanticIndexV2, query string, mode string, limit int) []CallEdge {
	if len(index.CallEdges) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  CallEdge
		score int
		key   string
	}
	items := []scored{}
	for _, item := range index.CallEdges {
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(item.SourceID)),
			strings.ToLower(strings.TrimSpace(item.TargetID)),
			strings.ToLower(strings.TrimSpace(item.Type)),
			strings.ToLower(strings.Join(item.Evidence, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if mode == "trace" {
			score += 4
		}
		if mode == "security" && containsAny(strings.ToLower(item.Type), "rpc", "runtime_dynamic_load", "runtime_process_spawn") {
			score += 3
		}
		if score <= 0 {
			continue
		}
		key := item.SourceID + "|" + item.Type + "|" + item.TargetID
		items = append(items, scored{item: item, score: score, key: key})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].key < items[j].key
		}
		return items[i].score > items[j].score
	})
	out := make([]CallEdge, 0, analysisMinInt(limit, len(items)))
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantV2InheritanceEdges(index SemanticIndexV2, query string, mode string, limit int) []InheritanceEdge {
	if len(index.InheritanceEdges) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  InheritanceEdge
		score int
		key   string
	}
	items := []scored{}
	for _, item := range index.InheritanceEdges {
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(item.SourceID)),
			strings.ToLower(strings.TrimSpace(item.TargetID)),
			strings.ToLower(strings.Join(item.Evidence, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if mode == "trace" || mode == "map" {
			score += 2
		}
		if score <= 0 {
			continue
		}
		key := item.SourceID + "|" + item.TargetID
		items = append(items, scored{item: item, score: score, key: key})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].key < items[j].key
		}
		return items[i].score > items[j].score
	})
	out := make([]InheritanceEdge, 0, analysisMinInt(limit, len(items)))
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantV2BuildEdges(index SemanticIndexV2, query string, mode string, limit int) []BuildOwnershipEdge {
	if len(index.BuildOwnershipEdges) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  BuildOwnershipEdge
		score int
		key   string
	}
	items := []scored{}
	for _, item := range index.BuildOwnershipEdges {
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(item.SourceID)),
			strings.ToLower(strings.TrimSpace(item.TargetID)),
			strings.ToLower(strings.TrimSpace(item.Type)),
			strings.ToLower(strings.Join(item.Evidence, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if mode == "map" || mode == "trace" || mode == "impact" {
			score += 3
		}
		if score <= 0 {
			continue
		}
		key := item.SourceID + "|" + item.Type + "|" + item.TargetID
		items = append(items, scored{item: item, score: score, key: key})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].key < items[j].key
		}
		return items[i].score > items[j].score
	})
	out := make([]BuildOwnershipEdge, 0, analysisMinInt(limit, len(items)))
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantV2OverlayEdges(index SemanticIndexV2, query string, mode string, limit int) []OverlayEdge {
	if len(index.OverlayEdges) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  OverlayEdge
		score int
		key   string
	}
	items := []scored{}
	for _, item := range index.OverlayEdges {
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(item.SourceID)),
			strings.ToLower(strings.TrimSpace(item.TargetID)),
			strings.ToLower(strings.TrimSpace(item.Type)),
			strings.ToLower(strings.TrimSpace(item.Domain)),
			strings.ToLower(strings.Join(item.Evidence, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if mode == "security" {
			score += 5
		}
		if mode == "impact" {
			score += 1
		}
		if score <= 0 {
			continue
		}
		key := item.Domain + "|" + item.SourceID + "|" + item.Type + "|" + item.TargetID
		items = append(items, scored{item: item, score: score, key: key})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].key < items[j].key
		}
		return items[i].score > items[j].score
	})
	out := make([]OverlayEdge, 0, analysisMinInt(limit, len(items)))
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantV2References(index SemanticIndexV2, query string, mode string, limit int) []ReferenceRecord {
	if len(index.References) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  ReferenceRecord
		score int
		key   string
	}
	items := []scored{}
	for _, item := range index.References {
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(item.SourceID)),
			strings.ToLower(strings.TrimSpace(item.SourceFile)),
			strings.ToLower(strings.TrimSpace(item.TargetID)),
			strings.ToLower(strings.TrimSpace(item.TargetPath)),
			strings.ToLower(strings.TrimSpace(item.Type)),
			strings.ToLower(strings.Join(item.Evidence, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if mode == "impact" {
			score += 3
		}
		if score <= 0 {
			continue
		}
		key := item.SourceID + "|" + item.SourceFile + "|" + item.Type + "|" + item.TargetID + "|" + item.TargetPath
		items = append(items, scored{item: item, score: score, key: key})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].key < items[j].key
		}
		return items[i].score > items[j].score
	})
	out := make([]ReferenceRecord, 0, analysisMinInt(limit, len(items)))
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantV2Occurrences(index SemanticIndexV2, query string, mode string, limit int) []SymbolOccurrence {
	if len(index.Occurrences) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  SymbolOccurrence
		score int
		key   string
	}
	items := []scored{}
	for _, item := range index.Occurrences {
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(item.SymbolID)),
			strings.ToLower(strings.TrimSpace(item.File)),
			strings.ToLower(strings.TrimSpace(item.Role)),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if mode == "impact" {
			score += 4
		}
		if score <= 0 {
			continue
		}
		key := item.SymbolID + "|" + item.Role + "|" + item.File
		items = append(items, scored{item: item, score: score, key: key})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].key < items[j].key
		}
		return items[i].score > items[j].score
	})
	out := make([]SymbolOccurrence, 0, analysisMinInt(limit, len(items)))
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}
