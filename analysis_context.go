package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultAnalysisContextMaxChars    = 3200
	cachedProjectAnalysisSummaryStart = "[Cached Project Analysis]"
	cachedProjectAnalysisSummaryEnd   = "[/Cached Project Analysis]"
	projectAnalysisFastPathNeedsTools = "NEEDS_TOOLS"
)

type latestAnalysisArtifacts struct {
	Pack         KnowledgePack
	Snapshot     ProjectSnapshot
	Corpus       VectorCorpus
	Index        SemanticIndex
	IndexV2      SemanticIndexV2
	DocsManifest AnalysisDocsManifest
}

type cachedAnalysisFastPathMetadata struct {
	Confidence string
	Sources    []string
}

func (a *Agent) latestProjectAnalysisContext(query string) string {
	if a == nil || a.Session == nil {
		return ""
	}
	artifacts, ok := a.loadLatestProjectAnalysisArtifacts()
	if !ok {
		return ""
	}
	if !a.shouldInjectLatestProjectAnalysisContext(artifacts, query) {
		return ""
	}
	context := renderRelevantProjectAnalysisContext(artifacts, query)
	if strings.TrimSpace(context) == "" {
		return ""
	}
	a.Session.LastAnalysisContextQuery = strings.TrimSpace(query)
	a.Session.LastAnalysisContextRunID = latestAnalysisArtifactsRunID(artifacts)
	return context
}

func (a *Agent) loadLatestProjectAnalysisArtifacts() (latestAnalysisArtifacts, bool) {
	root := ""
	if a != nil {
		root = strings.TrimSpace(a.Workspace.BaseRoot)
		if root == "" && a.Session != nil {
			root = strings.TrimSpace(a.Session.WorkingDir)
		}
	}
	if root == "" {
		return latestAnalysisArtifacts{}, false
	}
	cfg := configProjectAnalysis(a.Config, root)
	latestDir := filepath.Join(cfg.OutputDir, "latest")

	packData, err := os.ReadFile(filepath.Join(latestDir, "knowledge_pack.json"))
	if err != nil {
		return latestAnalysisArtifacts{}, false
	}
	pack := KnowledgePack{}
	if err := json.Unmarshal(packData, &pack); err != nil {
		return latestAnalysisArtifacts{}, false
	}

	artifacts := latestAnalysisArtifacts{Pack: pack}

	if snapshotData, err := os.ReadFile(filepath.Join(latestDir, "snapshot.json")); err == nil {
		_ = json.Unmarshal(snapshotData, &artifacts.Snapshot)
	}
	if corpusData, err := os.ReadFile(filepath.Join(latestDir, "vector_corpus.json")); err == nil {
		_ = json.Unmarshal(corpusData, &artifacts.Corpus)
	}
	if indexData, err := os.ReadFile(filepath.Join(latestDir, "structural_index.json")); err == nil {
		_ = json.Unmarshal(indexData, &artifacts.Index)
	}
	if indexData, err := os.ReadFile(filepath.Join(latestDir, "structural_index_v2.json")); err == nil {
		_ = json.Unmarshal(indexData, &artifacts.IndexV2)
	}
	if manifestData, err := os.ReadFile(filepath.Join(latestDir, "docs_manifest.json")); err == nil {
		if manifest, err := decodeAnalysisDocsManifest(manifestData); err == nil {
			artifacts.DocsManifest = manifest
		}
	} else if manifestData, err := os.ReadFile(filepath.Join(latestDir, "docs", "manifest.json")); err == nil {
		if manifest, err := decodeAnalysisDocsManifest(manifestData); err == nil {
			artifacts.DocsManifest = manifest
		}
	}
	return artifacts, true
}

func (a *Agent) shouldInjectLatestProjectAnalysisContext(artifacts latestAnalysisArtifacts, query string) bool {
	if a == nil || a.Session == nil {
		return false
	}
	currentRunID := latestAnalysisArtifactsRunID(artifacts)
	if len(a.Session.Messages) == 0 {
		return true
	}
	if strings.TrimSpace(a.Session.LastAnalysisContextQuery) == "" {
		return true
	}
	if currentRunID != "" && !strings.EqualFold(strings.TrimSpace(a.Session.LastAnalysisContextRunID), currentRunID) {
		return true
	}
	return analysisQueryMeaningfullyChanged(a.Session.LastAnalysisContextQuery, query)
}

func latestAnalysisArtifactsRunID(artifacts latestAnalysisArtifacts) string {
	if strings.TrimSpace(artifacts.Pack.RunID) != "" {
		return strings.TrimSpace(artifacts.Pack.RunID)
	}
	if strings.TrimSpace(artifacts.Corpus.RunID) != "" {
		return strings.TrimSpace(artifacts.Corpus.RunID)
	}
	return strings.TrimSpace(artifacts.Index.RunID)
}

func analysisQueryMeaningfullyChanged(previous string, current string) bool {
	prev := strings.ToLower(strings.TrimSpace(previous))
	curr := strings.ToLower(strings.TrimSpace(current))
	if curr == "" {
		return false
	}
	if prev == "" {
		return true
	}
	if prev == curr {
		return false
	}

	prevRefs := normalizeAnalysisRefs(extractPersistentMemoryReferences(previous))
	currRefs := normalizeAnalysisRefs(extractPersistentMemoryReferences(current))
	if len(currRefs) > 0 && !analysisStringSetsEqual(prevRefs, currRefs) {
		return true
	}

	prevTokens := filterAnalysisQueryTokens(extractPersistentMemoryTokens(prev))
	currTokens := filterAnalysisQueryTokens(extractPersistentMemoryTokens(curr))
	if len(currTokens) == 0 {
		return false
	}
	if len(currTokens) <= 3 && len(currRefs) == 0 {
		return false
	}

	overlap := analysisTokenOverlap(prevTokens, currTokens)
	newTokens := 0
	prevSet := analysisStringSet(prevTokens)
	for _, token := range currTokens {
		if _, ok := prevSet[token]; !ok {
			newTokens++
		}
	}
	if newTokens >= 2 && overlap <= 0.45 {
		return true
	}
	return overlap < 0.25 && len(currTokens) >= 4
}

func normalizeAnalysisRefs(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.ToLower(filepath.ToSlash(strings.TrimSpace(item)))
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return uniqueStrings(out)
}

func filterAnalysisQueryTokens(items []string) []string {
	stop := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {}, "do": {}, "for": {}, "from": {},
		"how": {}, "i": {}, "if": {}, "in": {}, "into": {}, "is": {}, "it": {}, "me": {}, "my": {}, "now": {}, "of": {},
		"on": {}, "only": {}, "or": {}, "please": {}, "show": {}, "so": {}, "summarize": {}, "tell": {}, "that": {}, "the": {},
		"this": {}, "to": {}, "up": {}, "use": {}, "what": {}, "with": {}, "explain": {},
		"그리고": {}, "그냥": {}, "기능": {}, "다시": {}, "만": {}, "먼저": {}, "설명": {}, "어떻게": {}, "요약": {}, "위주": {},
		"이제": {}, "좀": {}, "코드": {}, "해줘": {}, "흐름": {},
	}
	out := []string{}
	for _, item := range items {
		trimmed := strings.ToLower(strings.TrimSpace(item))
		if trimmed == "" {
			continue
		}
		if _, ok := stop[trimmed]; ok {
			continue
		}
		if len(trimmed) <= 1 {
			continue
		}
		out = append(out, trimmed)
	}
	return uniqueStrings(out)
}

func analysisTokenOverlap(left []string, right []string) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	leftSet := analysisStringSet(left)
	rightSet := analysisStringSet(right)
	intersection := 0
	union := map[string]struct{}{}
	for item := range leftSet {
		union[item] = struct{}{}
		if _, ok := rightSet[item]; ok {
			intersection++
		}
	}
	for item := range rightSet {
		union[item] = struct{}{}
	}
	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

func analysisStringSetsEqual(left []string, right []string) bool {
	leftSet := analysisStringSet(left)
	rightSet := analysisStringSet(right)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for item := range leftSet {
		if _, ok := rightSet[item]; !ok {
			return false
		}
	}
	return true
}

func analysisStringSet(items []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		set[trimmed] = struct{}{}
	}
	return set
}

func renderRelevantProjectAnalysisContext(artifacts latestAnalysisArtifacts, query string) string {
	query = strings.TrimSpace(query)
	if strings.TrimSpace(artifacts.Pack.ProjectSummary) == "" &&
		len(artifacts.Pack.Subsystems) == 0 &&
		len(artifacts.Corpus.Documents) == 0 &&
		len(artifacts.Index.Files) == 0 &&
		len(artifacts.Index.Symbols) == 0 &&
		!hasSemanticIndexV2Data(artifacts.IndexV2) &&
		len(artifacts.DocsManifest.Documents) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("- Source: latest analyze-project artifacts\n")
	if strings.TrimSpace(artifacts.Pack.Goal) != "" {
		fmt.Fprintf(&b, "- Analysis goal: %s\n", strings.TrimSpace(artifacts.Pack.Goal))
	}
	if strings.TrimSpace(artifacts.Pack.PrimaryStartup) != "" {
		fmt.Fprintf(&b, "- Primary startup: %s\n", strings.TrimSpace(artifacts.Pack.PrimaryStartup))
	}
	if strings.TrimSpace(artifacts.Pack.ProjectSummary) != "" {
		fmt.Fprintf(&b, "- Project summary: %s\n", compactProjectAnalysisText(artifacts.Pack.ProjectSummary, 420))
	}

	subsystems := selectRelevantKnowledgeSubsystems(artifacts.Pack, query, 3)
	if len(subsystems) > 0 {
		b.WriteString("\nRelevant subsystems:\n")
		for _, item := range subsystems {
			fmt.Fprintf(&b, "- %s\n", canonicalKnowledgeTitle(item))
			if len(item.Responsibilities) > 0 {
				fmt.Fprintf(&b, "  responsibilities: %s\n", strings.Join(limitStrings(item.Responsibilities, 2), "; "))
			}
			if len(item.EntryPoints) > 0 {
				fmt.Fprintf(&b, "  entry_points: %s\n", strings.Join(limitStrings(item.EntryPoints, 2), "; "))
			}
			if len(item.KeyFiles) > 0 {
				fmt.Fprintf(&b, "  key_files: %s\n", strings.Join(limitStrings(item.KeyFiles, 3), "; "))
			}
			if len(item.Dependencies) > 0 {
				fmt.Fprintf(&b, "  dependencies: %s\n", strings.Join(limitStrings(item.Dependencies, 2), "; "))
			}
		}
	}

	vectorDocs := selectRelevantVectorDocuments(artifacts.Corpus, query, 2)
	if len(vectorDocs) > 0 {
		b.WriteString("\nRelevant vector documents:\n")
		for _, doc := range vectorDocs {
			line := fmt.Sprintf("- %s [%s]", strings.TrimSpace(doc.Title), strings.TrimSpace(doc.Kind))
			if strings.TrimSpace(doc.PathHint) != "" {
				line += " path=" + strings.TrimSpace(doc.PathHint)
			}
			b.WriteString(line + "\n")
			b.WriteString("  " + compactProjectAnalysisText(strings.TrimSpace(doc.Text), 220) + "\n")
		}
	}

	files := selectRelevantIndexedFiles(artifacts.Index, query, 3)
	symbols := selectRelevantSemanticSymbols(artifacts.Index, query, 4)
	if len(files) > 0 || len(symbols) > 0 {
		b.WriteString("\nRelevant structural index hits:\n")
		for _, file := range files {
			line := fmt.Sprintf("- file: %s", strings.TrimSpace(file.Path))
			if file.ImportanceScore > 0 {
				line += fmt.Sprintf(" score=%d", file.ImportanceScore)
			}
			if len(file.Tags) > 0 {
				line += " tags=" + strings.Join(limitStrings(file.Tags, 3), ",")
			}
			b.WriteString(line + "\n")
		}
		for _, symbol := range symbols {
			line := fmt.Sprintf("- symbol: %s (%s)", strings.TrimSpace(symbol.Name), strings.TrimSpace(symbol.Kind))
			if strings.TrimSpace(symbol.File) != "" {
				line += " file=" + strings.TrimSpace(symbol.File)
			}
			if strings.TrimSpace(symbol.Module) != "" {
				line += " module=" + strings.TrimSpace(symbol.Module)
			}
			b.WriteString(line + "\n")
		}
	}

	if v2Text := renderRelevantSemanticIndexV2Context(artifacts.IndexV2, query); strings.TrimSpace(v2Text) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(v2Text))
		b.WriteString("\n")
	}
	if docsText := renderRelevantAnalysisDocsContext(artifacts.DocsManifest, query); strings.TrimSpace(docsText) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(docsText))
		b.WriteString("\n")
	}

	return compactProjectAnalysisText(strings.TrimSpace(b.String()), defaultAnalysisContextMaxChars)
}

func renderRelevantAnalysisDocsContext(manifest AnalysisDocsManifest, query string) string {
	if len(manifest.Documents) == 0 {
		return ""
	}
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	items := []scoredDoc{}
	for _, doc := range manifest.Documents {
		corpus := strings.ToLower(strings.Join(append(append([]string{doc.Name, doc.Title, doc.Kind, doc.Confidence}, doc.SourceAnchors...), append(doc.StaleMarkers, doc.ReuseTargets...)...), " "))
		score := 1
		if lowerQuery != "" && strings.Contains(corpus, lowerQuery) {
			score += 20
		}
		for _, token := range filterAnalysisQueryTokens(extractPersistentMemoryTokens(lowerQuery)) {
			if strings.Contains(corpus, token) {
				score += 4
			}
		}
		if containsAny(corpus, "security", "surface", "fuzz", "verification") {
			score += 2
		}
		items = append(items, scoredDoc{doc: doc, score: score})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].doc.Name < items[j].doc.Name
		}
		return items[i].score > items[j].score
	})
	var b strings.Builder
	b.WriteString("Reusable generated docs:\n")
	for _, item := range limitScoredAnalysisDocs(items, 4) {
		fmt.Fprintf(&b, "- %s path=latest/docs/%s confidence=%s\n", item.doc.Title, item.doc.Path, valueOrDefault(item.doc.Confidence, "unknown"))
		if len(item.doc.SourceAnchors) > 0 {
			fmt.Fprintf(&b, "  anchors: %s\n", strings.Join(limitStrings(item.doc.SourceAnchors, 4), "; "))
		}
		if len(item.doc.StaleMarkers) > 0 {
			fmt.Fprintf(&b, "  stale: %s\n", strings.Join(limitStrings(item.doc.StaleMarkers, 3), "; "))
		}
		if len(item.doc.ReuseTargets) > 0 {
			fmt.Fprintf(&b, "  reuse: %s\n", strings.Join(limitStrings(item.doc.ReuseTargets, 5), ", "))
		}
	}
	return b.String()
}

type scoredDoc struct {
	doc   AnalysisGeneratedDoc
	score int
}

func limitScoredAnalysisDocs(items []scoredDoc, limit int) []scoredDoc {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

func selectRelevantKnowledgeSubsystems(pack KnowledgePack, query string, limit int) []KnowledgeSubsystem {
	if len(pack.Subsystems) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 3
	}
	type scoredSubsystem struct {
		Item  KnowledgeSubsystem
		Score int
		Title string
	}
	loweredQuery := strings.ToLower(strings.TrimSpace(query))
	queryTokens := filterAnalysisQueryTokens(extractPersistentMemoryTokens(loweredQuery))
	queryRefs := normalizeAnalysisRefs(extractPersistentMemoryReferences(query))
	scored := []scoredSubsystem{}
	for _, item := range pack.Subsystems {
		score := scoreKnowledgeSubsystem(item, loweredQuery, queryTokens, queryRefs)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredSubsystem{
			Item:  item,
			Score: score,
			Title: canonicalKnowledgeTitle(item),
		})
	}
	sort.Slice(scored, func(i int, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Title < scored[j].Title
		}
		return scored[i].Score > scored[j].Score
	})
	if len(scored) == 0 {
		if strings.TrimSpace(query) == "" {
			return limitKnowledgeSubsystems(pack.Subsystems, limit)
		}
		return nil
	}
	out := make([]KnowledgeSubsystem, 0, analysisMinInt(limit, len(scored)))
	for _, item := range scored {
		out = append(out, item.Item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func scoreKnowledgeSubsystem(item KnowledgeSubsystem, loweredQuery string, queryTokens []string, queryRefs []string) int {
	haystacks := []string{
		strings.ToLower(canonicalKnowledgeTitle(item)),
		strings.ToLower(strings.Join(item.Responsibilities, " ")),
		strings.ToLower(strings.Join(item.EntryPoints, " ")),
		strings.ToLower(strings.Join(item.KeyFiles, " ")),
		strings.ToLower(strings.Join(item.Dependencies, " ")),
		strings.ToLower(strings.Join(item.EvidenceFiles, " ")),
	}
	score := 0
	if loweredQuery != "" {
		for _, hay := range haystacks {
			if hay == "" {
				continue
			}
			if strings.Contains(hay, loweredQuery) {
				score += 8
			}
		}
	}
	for _, token := range queryTokens {
		for _, hay := range haystacks {
			if hay == "" || token == "" {
				continue
			}
			if strings.Contains(hay, token) {
				score++
			}
		}
	}
	for _, ref := range queryRefs {
		for _, itemRef := range append(append([]string(nil), item.KeyFiles...), item.EvidenceFiles...) {
			lowerItemRef := strings.ToLower(filepath.ToSlash(strings.TrimSpace(itemRef)))
			if lowerItemRef == "" {
				continue
			}
			if strings.Contains(lowerItemRef, ref) || strings.Contains(ref, lowerItemRef) {
				score += 4
			}
		}
	}
	return score
}

func selectRelevantVectorDocuments(corpus VectorCorpus, query string, limit int) []VectorCorpusDocument {
	if len(corpus.Documents) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 2
	}
	type scoredDocument struct {
		Item  VectorCorpusDocument
		Score int
		Title string
	}
	loweredQuery := strings.ToLower(strings.TrimSpace(query))
	queryTokens := filterAnalysisQueryTokens(extractPersistentMemoryTokens(loweredQuery))
	queryRefs := normalizeAnalysisRefs(extractPersistentMemoryReferences(query))
	scored := []scoredDocument{}
	for _, item := range corpus.Documents {
		score := scoreVectorDocument(item, loweredQuery, queryTokens, queryRefs)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredDocument{Item: item, Score: score, Title: item.Title})
	}
	sort.Slice(scored, func(i int, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Title < scored[j].Title
		}
		return scored[i].Score > scored[j].Score
	})
	out := make([]VectorCorpusDocument, 0, analysisMinInt(limit, len(scored)))
	for _, item := range scored {
		out = append(out, item.Item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func scoreVectorDocument(item VectorCorpusDocument, loweredQuery string, queryTokens []string, queryRefs []string) int {
	haystacks := []string{
		strings.ToLower(strings.TrimSpace(item.Title)),
		strings.ToLower(strings.TrimSpace(item.Kind)),
		strings.ToLower(strings.TrimSpace(item.PathHint)),
		strings.ToLower(strings.TrimSpace(item.Text)),
	}
	score := 0
	if loweredQuery != "" {
		for _, hay := range haystacks {
			if hay == "" {
				continue
			}
			if strings.Contains(hay, loweredQuery) {
				score += 6
			}
		}
	}
	for _, token := range queryTokens {
		for _, hay := range haystacks {
			if hay == "" || token == "" {
				continue
			}
			if strings.Contains(hay, token) {
				score++
			}
		}
	}
	for _, ref := range queryRefs {
		for _, hay := range haystacks {
			if hay == "" || ref == "" {
				continue
			}
			if strings.Contains(hay, ref) {
				score += 4
			}
		}
	}
	if item.Kind == "subsystem" {
		score++
	}
	return score
}

func selectRelevantIndexedFiles(index SemanticIndex, query string, limit int) []SemanticIndexedFile {
	if len(index.Files) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 3
	}
	type scoredFile struct {
		Item  SemanticIndexedFile
		Score int
		Path  string
	}
	loweredQuery := strings.ToLower(strings.TrimSpace(query))
	queryTokens := filterAnalysisQueryTokens(extractPersistentMemoryTokens(loweredQuery))
	queryRefs := normalizeAnalysisRefs(extractPersistentMemoryReferences(query))
	scored := []scoredFile{}
	for _, item := range index.Files {
		score := scoreIndexedFile(item, loweredQuery, queryTokens, queryRefs)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredFile{Item: item, Score: score, Path: item.Path})
	}
	sort.Slice(scored, func(i int, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Path < scored[j].Path
		}
		return scored[i].Score > scored[j].Score
	})
	out := make([]SemanticIndexedFile, 0, analysisMinInt(limit, len(scored)))
	for _, item := range scored {
		out = append(out, item.Item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func scoreIndexedFile(item SemanticIndexedFile, loweredQuery string, queryTokens []string, queryRefs []string) int {
	haystacks := []string{
		strings.ToLower(strings.TrimSpace(item.Path)),
		strings.ToLower(strings.TrimSpace(item.Directory)),
		strings.ToLower(strings.Join(item.Tags, " ")),
	}
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
	if item.ImportanceScore > 0 {
		score += analysisMinInt(item.ImportanceScore/20, 3)
	}
	return score
}

func selectRelevantSemanticSymbols(index SemanticIndex, query string, limit int) []SemanticSymbol {
	if len(index.Symbols) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 4
	}
	type scoredSymbol struct {
		Item  SemanticSymbol
		Score int
		Name  string
	}
	loweredQuery := strings.ToLower(strings.TrimSpace(query))
	queryTokens := filterAnalysisQueryTokens(extractPersistentMemoryTokens(loweredQuery))
	queryRefs := normalizeAnalysisRefs(extractPersistentMemoryReferences(query))
	scored := []scoredSymbol{}
	for _, item := range index.Symbols {
		score := scoreSemanticSymbol(item, loweredQuery, queryTokens, queryRefs)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredSymbol{Item: item, Score: score, Name: item.Name})
	}
	sort.Slice(scored, func(i int, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Name < scored[j].Name
		}
		return scored[i].Score > scored[j].Score
	})
	out := make([]SemanticSymbol, 0, analysisMinInt(limit, len(scored)))
	for _, item := range scored {
		out = append(out, item.Item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func scoreSemanticSymbol(item SemanticSymbol, loweredQuery string, queryTokens []string, queryRefs []string) int {
	haystacks := []string{
		strings.ToLower(strings.TrimSpace(item.Name)),
		strings.ToLower(strings.TrimSpace(item.Kind)),
		strings.ToLower(strings.TrimSpace(item.File)),
		strings.ToLower(strings.TrimSpace(item.Container)),
		strings.ToLower(strings.TrimSpace(item.Module)),
		strings.ToLower(strings.Join(item.Tags, " ")),
	}
	score := 0
	if loweredQuery != "" {
		for _, hay := range haystacks {
			if hay != "" && strings.Contains(hay, loweredQuery) {
				score += 5
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

func limitKnowledgeSubsystems(items []KnowledgeSubsystem, limit int) []KnowledgeSubsystem {
	if limit <= 0 || len(items) <= limit {
		return append([]KnowledgeSubsystem(nil), items...)
	}
	return append([]KnowledgeSubsystem(nil), items[:limit]...)
}

func buildSessionAnalysisSummary(run ProjectAnalysisRun) string {
	var b strings.Builder
	b.WriteString(cachedProjectAnalysisSummaryStart)
	b.WriteString("\n")
	if strings.TrimSpace(run.Summary.Goal) != "" {
		fmt.Fprintf(&b, "- Goal: %s\n", strings.TrimSpace(run.Summary.Goal))
	}
	if strings.TrimSpace(run.Summary.Mode) != "" {
		fmt.Fprintf(&b, "- Mode: %s\n", strings.TrimSpace(run.Summary.Mode))
	}
	fmt.Fprintf(&b, "- Run ID: %s\n", strings.TrimSpace(run.Summary.RunID))
	fmt.Fprintf(&b, "- Status: %s\n", strings.TrimSpace(run.Summary.Status))
	if strings.TrimSpace(run.KnowledgePack.PrimaryStartup) != "" {
		fmt.Fprintf(&b, "- Primary startup: %s\n", strings.TrimSpace(run.KnowledgePack.PrimaryStartup))
	}
	if strings.TrimSpace(run.KnowledgePack.ProjectSummary) != "" {
		fmt.Fprintf(&b, "- Summary: %s\n", compactProjectAnalysisText(run.KnowledgePack.ProjectSummary, 420))
	}
	if len(run.KnowledgePack.Subsystems) > 0 {
		names := []string{}
		for _, item := range limitKnowledgeSubsystems(run.KnowledgePack.Subsystems, 4) {
			names = append(names, canonicalKnowledgeTitle(item))
		}
		fmt.Fprintf(&b, "- Key subsystems: %s\n", strings.Join(names, "; "))
	}
	if len(run.KnowledgePack.TopImportantFiles) > 0 {
		fmt.Fprintf(&b, "- Key files: %s\n", strings.Join(limitStrings(run.KnowledgePack.TopImportantFiles, 4), "; "))
	}
	b.WriteString(cachedProjectAnalysisSummaryEnd)
	return strings.TrimSpace(b.String())
}

func mergeSessionSummaryWithAnalysis(summary string, run ProjectAnalysisRun) string {
	trimmed := strings.TrimSpace(summary)
	start := strings.Index(trimmed, cachedProjectAnalysisSummaryStart)
	end := strings.Index(trimmed, cachedProjectAnalysisSummaryEnd)
	if start >= 0 && end >= start {
		end += len(cachedProjectAnalysisSummaryEnd)
		trimmed = strings.TrimSpace(trimmed[:start] + trimmed[end:])
	}
	block := buildSessionAnalysisSummary(run)
	if strings.TrimSpace(trimmed) == "" {
		return block
	}
	return strings.TrimSpace(trimmed) + "\n\n" + block
}

func compactProjectAnalysisText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return strings.TrimSpace(text[:limit-3]) + "..."
}

func buildCachedAnalysisFastPathMetadata(artifacts latestAnalysisArtifacts, query string) cachedAnalysisFastPathMetadata {
	subsystems := selectRelevantKnowledgeSubsystems(artifacts.Pack, query, 3)
	vectorDocs := selectRelevantVectorDocuments(artifacts.Corpus, query, 2)
	files := selectRelevantIndexedFiles(artifacts.Index, query, 3)
	symbols := selectRelevantSemanticSymbols(artifacts.Index, query, 4)
	v2Hits := collectRelevantSemanticIndexV2Hits(artifacts.IndexV2, query)
	sources := []string{}
	if len(subsystems) > 0 {
		sources = append(sources, "knowledge_pack")
	}
	if len(vectorDocs) > 0 {
		sources = append(sources, "vector_corpus")
	}
	if len(files) > 0 || len(symbols) > 0 {
		sources = append(sources, "structural_index")
	}
	if len(v2Hits.Files) > 0 ||
		len(v2Hits.Symbols) > 0 ||
		len(v2Hits.Calls) > 0 ||
		len(v2Hits.Inheritance) > 0 ||
		len(v2Hits.Builds) > 0 ||
		len(v2Hits.Overlays) > 0 ||
		len(v2Hits.References) > 0 ||
		len(v2Hits.Occurrences) > 0 {
		sources = append(sources, "structural_index_v2")
	}
	confidence := "low"
	switch {
	case len(subsystems) > 0 && len(vectorDocs) > 0 && (len(files) > 0 || len(symbols) > 0 || len(v2Hits.Symbols) > 0 || len(v2Hits.Calls) > 0 || len(v2Hits.Overlays) > 0):
		confidence = "high"
	case len(subsystems) > 0 || len(vectorDocs) > 0 || len(files) > 0 || len(symbols) > 0 || len(v2Hits.Files) > 0 || len(v2Hits.Symbols) > 0 || len(v2Hits.Calls) > 0 || len(v2Hits.Overlays) > 0:
		confidence = "medium"
	case strings.TrimSpace(artifacts.Pack.ProjectSummary) != "":
		confidence = "low"
	}
	return cachedAnalysisFastPathMetadata{
		Confidence: confidence,
		Sources:    analysisUniqueStrings(sources),
	}
}

func formatCachedAnalysisFastPathReply(reply string, meta cachedAnalysisFastPathMetadata) string {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return ""
	}
	_ = meta
	return trimmed
}

func (a *Agent) maybeAnswerFromCachedProjectAnalysis(ctx context.Context) (string, bool, error) {
	if a == nil || a.Session == nil || a.Client == nil {
		return "", false, nil
	}
	if !a.shouldTryProjectAnalysisFastPath() {
		return "", false, nil
	}
	artifacts, ok := a.loadLatestProjectAnalysisArtifacts()
	if !ok {
		return "", false, nil
	}
	query := baseUserQueryText(latestUserMessageText(a.Session.Messages))
	meta := buildCachedAnalysisFastPathMetadata(artifacts, query)
	messages := append([]Message(nil), a.Session.Messages...)
	messages = append(messages, Message{
		Role: "user",
		Text: "Fast-path check: Use only the cached project analysis already present in this conversation. Do not use tools and do not assume unseen code. If the cached analysis is sufficient to fully answer the user's latest request, answer now. Otherwise reply exactly NEEDS_TOOLS.",
	})
	resp, err := a.completeModelTurn(ctx, ChatRequest{
		Model:       a.Session.Model,
		System:      a.systemPrompt(),
		Messages:    messages,
		Tools:       nil,
		MaxTokens:   a.Config.MaxTokens,
		Temperature: a.Config.Temperature,
		WorkingDir:  a.Session.WorkingDir,
	})
	if err != nil {
		return "", false, err
	}
	reply := strings.TrimSpace(resp.Message.Text)
	if strings.EqualFold(reply, projectAnalysisFastPathNeedsTools) || reply == "" {
		return "", false, nil
	}
	return formatCachedAnalysisFastPathReply(reply, meta), true, nil
}

func (a *Agent) shouldTryProjectAnalysisFastPath() bool {
	if a == nil || a.Session == nil {
		return false
	}
	lastUser := strings.TrimSpace(latestUserMessageText(a.Session.Messages))
	if lastUser == "" {
		return false
	}
	if !strings.Contains(lastUser, "Relevant project analysis from past analyze-project runs") {
		return false
	}
	baseQuery := baseUserQueryText(lastUser)
	if strings.TrimSpace(baseQuery) == "" {
		baseQuery = lastUser
	}
	if shouldSuppressProjectAnalysisFastPathForIntent(classifyTurnIntent(baseQuery)) {
		return false
	}
	return !looksLikeActionOrToolIntent(baseQuery)
}

func latestUserMessageText(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(messages[i].Role, "user") {
			return messages[i].Text
		}
	}
	return ""
}

func looksLikeActionOrToolIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "/") {
		return true
	}
	return containsAny(lower,
		"add ", "apply ", "build ", "change ", "commit ", "compile ", "create ", "delete ", "edit ", "fix ", "implement ", "modify ", "patch ", "refactor ", "remove ", "rename ", "replace ", "run ", "test ", "update ", "write ",
		"리뷰", "검토", "고쳐", "구현", "만들", "변경", "빌드", "삭제", "수정", "실행", "적용", "작성", "추가", "테스트", "패치",
	)
}

func prefersReadOnlyAnalysisIntent(text string) bool {
	base := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if base == "" {
		return false
	}
	if looksLikeExplicitEditIntent(base) {
		return false
	}
	if strings.Contains(base, "?") {
		return true
	}
	return containsAny(base,
		"analy", "analysis", "diagnos", "explain", "investigat", "why ", "why is", "why does", "reason", "root cause", "document", "summarize",
		"분석", "원인", "이유", "설명", "조사", "문서화", "진단", "왜", "동작할 수 없", "동작하지 않", "안되는", "안 돼", "안되",
	)
}

func looksLikeExplicitEditIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "/") {
		return true
	}
	return containsAny(lower,
		"add ", "apply ", "build ", "change ", "commit ", "compile ", "create ", "delete ", "edit ", "fix ", "implement ", "modify ", "patch ", "refactor ", "remove ", "rename ", "replace ", "run ", "test ", "update ", "write ",
		"고쳐", "구현", "만들", "변경", "빌드", "삭제", "수정", "실행", "적용", "작성", "추가", "테스트", "패치",
	)
}

func looksLikeExplicitGitIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"git add", "git commit", "git push", "git stage", "git stash", "create a pr", "create pr", "open a pr", "open pr", "pull request",
		"stage these changes", "stage the changes", "stage this", "stage it",
		"commit these changes", "commit the changes", "commit this", "commit it",
		"push this branch", "push the branch", "push these changes", "push it",
		"check in these changes", "check in this",
		"커밋해", "커밋해줘", "커밋해 줘", "커밋할", "커밋해도",
		"스테이징해", "스테이징해줘", "스테이징해 줘", "스테이지해", "스테이지해줘", "스테이지해 줘",
		"푸시해", "푸시해줘", "푸시해 줘", "브랜치 푸시",
		"pr 만들어", "pr 열어", "pull request 만들어", "pull request 열어", "풀 리퀘스트 만들어", "풀 리퀘스트 열어",
	)
}

func looksLikeDocumentAuthoringIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	hasDocumentNoun := containsAny(lower,
		"document", "documents", "doc", "markdown", ".md", "report", "reports", "write-up", "writeup", "research", "paper", "papers", "notes", "spec", "specs",
		"문서", "문서들", "마크다운", "보고서", "리서치", "연구", "정리", "초안", "명세", "스펙",
	)
	if !hasDocumentNoun {
		return false
	}
	return containsAny(lower,
		"add ", "author ", "create ", "draft ", "generate ", "prepare ", "revise ", "update ", "write ",
		"작성", "만들", "생성", "업데이트", "정리", "초안", "추가",
	)
}

func baseUserQueryText(text string) string {
	trimmed := strings.TrimSpace(text)
	markers := []string{
		"\n\nRequest mode:",
		"\n\nGit intent:\n",
		"\n\nRelevant persistent memory from past sessions:\n",
		"\n\nRelevant project analysis from past analyze-project runs:\n",
		"\n\nAuto-discovered code context:\n",
	}
	cut := len(trimmed)
	for _, marker := range markers {
		if idx := strings.Index(trimmed, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	return strings.TrimSpace(trimmed[:cut])
}
