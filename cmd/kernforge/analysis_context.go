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
	deepAnalysisContextMaxChars       = 12000
	deepAnalysisAnswerPackMaxChars    = 10000
	cachedProjectAnalysisSummaryStart = "[Cached Project Analysis]"
	cachedProjectAnalysisSummaryEnd   = "[/Cached Project Analysis]"
	projectAnalysisFastPathNeedsTools = "NEEDS_TOOLS"
)

type latestAnalysisArtifacts struct {
	Pack              KnowledgePack
	Snapshot          ProjectSnapshot
	RunSummary        ProjectAnalysisSummary
	ClaimVerification ClaimVerificationReport
	Corpus            VectorCorpus
	Structural        StructuralIndex
	Index             SemanticIndex
	IndexV2           SemanticIndexV2
	UnrealGraph       UnrealSemanticGraph
	DocsManifest      AnalysisDocsManifest
}

type cachedAnalysisFastPathMetadata struct {
	Confidence string
	Sources    []string
}

func (a *Agent) latestProjectAnalysisContext(query string) string {
	context, _ := a.latestProjectAnalysisContextWithProgress(query)
	return context
}

func (a *Agent) latestProjectAnalysisContextWithProgress(query string) (string, string) {
	if a == nil || a.Session == nil {
		return "", ""
	}
	artifacts, ok := a.loadLatestProjectAnalysisArtifacts()
	if !ok {
		return "", ""
	}
	if !a.shouldInjectLatestProjectAnalysisContext(artifacts, query) {
		return "", ""
	}
	root := strings.TrimSpace(a.Workspace.BaseRoot)
	if root == "" {
		root = strings.TrimSpace(a.Session.WorkingDir)
	}
	freshness := evaluateLatestAnalysisFreshness(a.Config, root, artifacts, query)
	progress := formatProjectAnalysisContextProgressMessage(a.Config, artifacts, query, freshness)
	if !analysisFreshnessAllowsContext(freshness) {
		return "", progress
	}
	context := renderRelevantProjectAnalysisContext(artifacts, query)
	if strings.TrimSpace(context) == "" {
		return "", ""
	}
	if freshnessBlock := analysisFreshnessPromptBlock(freshness); freshnessBlock != "" {
		context = freshnessBlock + "\n\n" + context
	}
	a.Session.LastAnalysisContextQuery = strings.TrimSpace(query)
	a.Session.LastAnalysisContextRunID = latestAnalysisArtifactsRunID(artifacts)
	return context, progress
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
	if runData, err := os.ReadFile(filepath.Join(latestDir, "run.json")); err == nil {
		var runMeta struct {
			Summary           ProjectAnalysisSummary  `json:"summary"`
			ClaimVerification ClaimVerificationReport `json:"claim_verification,omitempty"`
		}
		if json.Unmarshal(runData, &runMeta) == nil {
			artifacts.RunSummary = runMeta.Summary
			artifacts.ClaimVerification = runMeta.ClaimVerification
		}
	}

	if snapshotData, err := os.ReadFile(filepath.Join(latestDir, "snapshot.json")); err == nil {
		_ = json.Unmarshal(snapshotData, &artifacts.Snapshot)
	}
	if factsData, err := os.ReadFile(filepath.Join(latestDir, "architecture_facts.json")); err == nil {
		facts := ArchitectureFactPack{}
		if json.Unmarshal(factsData, &facts) == nil {
			artifacts.Snapshot.ArchitectureFacts = facts
			if !architectureFactPackHasData(artifacts.Pack.ArchitectureFacts) {
				artifacts.Pack.ArchitectureFacts = facts
			}
		}
	}
	if corpusData, err := os.ReadFile(filepath.Join(latestDir, "vector_corpus.json")); err == nil {
		_ = json.Unmarshal(corpusData, &artifacts.Corpus)
	}
	if indexData, err := os.ReadFile(filepath.Join(latestDir, "structural_index.json")); err == nil {
		_ = json.Unmarshal(indexData, &artifacts.Structural)
		if strings.TrimSpace(artifacts.Structural.SchemaVersion) == "" {
			_ = json.Unmarshal(indexData, &artifacts.Index)
		}
	}
	if indexData, err := os.ReadFile(filepath.Join(latestDir, "semantic_index.json")); err == nil {
		_ = json.Unmarshal(indexData, &artifacts.Index)
	}
	if indexData, err := os.ReadFile(filepath.Join(latestDir, "structural_index_v2.json")); err == nil {
		_ = json.Unmarshal(indexData, &artifacts.IndexV2)
	}
	if graphData, err := os.ReadFile(filepath.Join(latestDir, "unreal_graph.json")); err == nil {
		_ = json.Unmarshal(graphData, &artifacts.UnrealGraph)
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

func formatProjectAnalysisContextProgressMessage(cfg Config, artifacts latestAnalysisArtifacts, query string, freshness analysisFreshnessReport) string {
	runID := latestAnalysisArtifactsRunID(artifacts)
	meta := buildCachedAnalysisFastPathMetadata(artifacts, query)
	sources := limitStrings(meta.Sources, 4)
	files := analysisContextProgressFiles(artifacts, query, 3)
	parts := []string{}
	parts = append(parts, formatAnalysisFreshnessProgressParts(freshness)...)
	if strings.TrimSpace(runID) != "" {
		parts = append(parts, "run="+strings.TrimSpace(runID))
	}
	if strings.TrimSpace(meta.Confidence) != "" {
		parts = append(parts, "confidence="+strings.TrimSpace(meta.Confidence))
	}
	if len(sources) > 0 {
		parts = append(parts, "sources="+strings.Join(sources, ","))
	}
	if len(files) > 0 {
		parts = append(parts, "files="+strings.Join(files, "; "))
	}
	if len(parts) == 0 {
		return localizedText(cfg, "Using latest analyze-project artifacts.", "최신 analyze-project 산출물을 참고합니다.")
	}
	return fmt.Sprintf(
		localizedText(cfg, "Using latest analyze-project artifacts: %s", "최신 analyze-project 산출물 참고: %s"),
		strings.Join(parts, " | "),
	)
}

func analysisContextProgressFiles(artifacts latestAnalysisArtifacts, query string, limit int) []string {
	if limit <= 0 {
		limit = 3
	}
	out := []string{}
	for _, subsystem := range selectRelevantKnowledgeSubsystems(artifacts.Pack, query, 3) {
		out = append(out, subsystem.KeyFiles...)
		out = append(out, subsystem.EvidenceFiles...)
	}
	for _, doc := range selectRelevantVectorDocuments(artifacts.Corpus, query, 2) {
		if strings.TrimSpace(doc.PathHint) != "" {
			out = append(out, doc.PathHint)
		}
	}
	for _, file := range selectRelevantIndexedFiles(artifacts.Index, query, 3) {
		out = append(out, file.Path)
	}
	for _, symbol := range selectRelevantSemanticSymbols(artifacts.Index, query, 4) {
		out = append(out, symbol.File)
	}
	for _, symbol := range selectRelevantStructuralSymbols(artifacts.Structural, query, 4) {
		out = append(out, symbol.File)
	}
	v2Hits := collectRelevantSemanticIndexV2Hits(artifacts.IndexV2, query)
	for _, file := range v2Hits.Files {
		out = append(out, file.Path)
	}
	for _, symbol := range v2Hits.Symbols {
		out = append(out, symbol.File)
	}
	for _, edge := range v2Hits.Overlays {
		out = append(out, edge.Evidence...)
	}
	for _, doc := range limitScoredAnalysisDocs(scoreRelevantAnalysisDocs(artifacts.DocsManifest, query), 2) {
		out = append(out, doc.doc.SourceAnchors...)
	}
	cleaned := []string{}
	for _, item := range out {
		clean := analysisContextProgressFilePath(item)
		if clean == "" {
			continue
		}
		cleaned = append(cleaned, clean)
	}
	return limitStrings(analysisUniqueStrings(cleaned), limit)
}

func analysisContextProgressFilePath(item string) string {
	path, _, ok := parseAnalysisClaimSourceAnchor(item)
	if ok {
		item = path
	}
	clean := cleanEvidencePath(item)
	if clean == "" || !analysisDocPathLooksLikeFile(clean) {
		return ""
	}
	return clean
}

func scoreRelevantAnalysisDocs(manifest AnalysisDocsManifest, query string) []scoredDoc {
	if len(manifest.Documents) == 0 {
		return nil
	}
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	tokens := filterAnalysisQueryTokens(extractPersistentMemoryTokens(lowerQuery))
	items := []scoredDoc{}
	for _, doc := range manifest.Documents {
		corpus := strings.ToLower(strings.Join(append(append([]string{doc.Name, doc.Title, doc.Kind, doc.Confidence}, doc.SourceAnchors...), append(doc.StaleMarkers, doc.ReuseTargets...)...), " "))
		score := 1
		if lowerQuery != "" && strings.Contains(corpus, lowerQuery) {
			score += 20
		}
		for _, token := range tokens {
			if strings.Contains(corpus, token) {
				score += 4
			}
		}
		if containsAny(corpus, "security", "surface", "fuzz", "verification") {
			score += 2
		}
		items = append(items, scoredDoc{doc: doc, score: score})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].doc.Name < items[j].doc.Name
		}
		return items[i].score > items[j].score
	})
	return items
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
	if projectAnalysisQAIntentNeedsAnswerPack(classifyProjectAnalysisQAIntent(query)) && a.lastSessionMessageIsUserWithoutAssistantReply() {
		return true
	}
	return analysisQueryMeaningfullyChanged(a.Session.LastAnalysisContextQuery, query)
}

func (a *Agent) lastSessionMessageIsUserWithoutAssistantReply() bool {
	if a == nil || a.Session == nil || len(a.Session.Messages) == 0 {
		return false
	}
	for i := len(a.Session.Messages) - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(a.Session.Messages[i].Role))
		switch role {
		case "assistant":
			return false
		case "user":
			return true
		}
	}
	return false
}

func latestAnalysisArtifactsRunID(artifacts latestAnalysisArtifacts) string {
	if strings.TrimSpace(artifacts.Pack.RunID) != "" {
		return strings.TrimSpace(artifacts.Pack.RunID)
	}
	if strings.TrimSpace(artifacts.Corpus.RunID) != "" {
		return strings.TrimSpace(artifacts.Corpus.RunID)
	}
	if strings.TrimSpace(artifacts.Structural.RunID) != "" {
		return strings.TrimSpace(artifacts.Structural.RunID)
	}
	if strings.TrimSpace(artifacts.Index.RunID) != "" {
		return strings.TrimSpace(artifacts.Index.RunID)
	}
	if strings.TrimSpace(artifacts.IndexV2.RunID) != "" {
		return strings.TrimSpace(artifacts.IndexV2.RunID)
	}
	if strings.TrimSpace(artifacts.UnrealGraph.RunID) != "" {
		return strings.TrimSpace(artifacts.UnrealGraph.RunID)
	}
	return strings.TrimSpace(artifacts.DocsManifest.RunID)
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
		!hasStructuralIndexData(artifacts.Structural) &&
		len(artifacts.Index.Files) == 0 &&
		len(artifacts.Index.Symbols) == 0 &&
		!hasSemanticIndexV2Data(artifacts.IndexV2) &&
		len(artifacts.UnrealGraph.Nodes) == 0 &&
		len(artifacts.UnrealGraph.Edges) == 0 &&
		!architectureFactPackHasData(artifacts.Snapshot.ArchitectureFacts) &&
		!architectureFactPackHasData(artifacts.Pack.ArchitectureFacts) &&
		len(artifacts.DocsManifest.Documents) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("- Source: latest analyze-project artifacts\n")
	qaIntent := classifyProjectAnalysisQAIntent(query)
	if projectAnalysisQAIntentNeedsAnswerPack(qaIntent) {
		answerPack := buildProjectStructureAnswerPack(artifacts, query)
		if packText := renderProjectStructureAnswerPack(answerPack, deepAnalysisAnswerPackMaxChars); strings.TrimSpace(packText) != "" {
			b.WriteString(strings.TrimSpace(packText))
			b.WriteString("\n")
		}
	}
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
	structuralSymbols := selectRelevantStructuralSymbols(artifacts.Structural, query, 4)
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
	if len(structuralSymbols) > 0 {
		b.WriteString("\nRelevant Phase 2 structural symbols:\n")
		for _, symbol := range structuralSymbols {
			line := fmt.Sprintf("- symbol: %s (%s)", firstNonBlankAnalysisString(symbol.CanonicalName, symbol.Name), strings.TrimSpace(symbol.Kind))
			if strings.TrimSpace(symbol.File) != "" {
				line += " file=" + strings.TrimSpace(symbol.File)
				if symbol.StartLine > 0 {
					line += fmt.Sprintf(":%d", symbol.StartLine)
				}
			}
			if strings.TrimSpace(symbol.ExtractionMethod) != "" {
				line += " parser=" + strings.TrimSpace(symbol.ExtractionMethod)
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

	limit := defaultAnalysisContextMaxChars
	if projectAnalysisQAIntentNeedsAnswerPack(qaIntent) {
		limit = deepAnalysisContextMaxChars
	}
	return compactProjectAnalysisText(strings.TrimSpace(b.String()), limit)
}

func renderRelevantAnalysisDocsContext(manifest AnalysisDocsManifest, query string) string {
	if len(manifest.Documents) == 0 {
		return ""
	}
	items := scoreRelevantAnalysisDocs(manifest, query)
	var b strings.Builder
	b.WriteString("Reusable generated docs:\n")
	for _, item := range limitScoredAnalysisDocs(items, 4) {
		fmt.Fprintf(&b, "- %s path=latest/docs/%s confidence=%s\n", item.doc.Title, item.doc.Path, valueOrDefault(item.doc.Confidence, "unknown"))
		if len(item.doc.SourceAnchors) > 0 {
			fmt.Fprintf(&b, "  anchors: %s\n", strings.Join(limitStrings(item.doc.SourceAnchors, 4), "; "))
		}
		if markers := analysisRealStaleMarkers(item.doc.StaleMarkers); len(markers) > 0 {
			fmt.Fprintf(&b, "  stale: %s\n", strings.Join(limitStrings(markers, 3), "; "))
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

func selectRelevantStructuralSymbols(index StructuralIndex, query string, limit int) []SymbolRecord {
	if len(index.Symbols) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 4
	}
	type scoredSymbol struct {
		Item  SymbolRecord
		Score int
		Name  string
	}
	loweredQuery := strings.ToLower(strings.TrimSpace(query))
	queryTokens := filterAnalysisQueryTokens(extractPersistentMemoryTokens(loweredQuery))
	queryRefs := normalizeAnalysisRefs(extractPersistentMemoryReferences(query))
	scored := []scoredSymbol{}
	for _, item := range index.Symbols {
		score := scoreStructuralSymbolForQuery(item, loweredQuery, queryTokens, queryRefs)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredSymbol{Item: item, Score: score, Name: firstNonBlankAnalysisString(item.CanonicalName, item.Name)})
	}
	sort.Slice(scored, func(i int, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Name < scored[j].Name
		}
		return scored[i].Score > scored[j].Score
	})
	out := make([]SymbolRecord, 0, analysisMinInt(limit, len(scored)))
	for _, item := range scored {
		out = append(out, item.Item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func scoreStructuralSymbolForQuery(item SymbolRecord, loweredQuery string, queryTokens []string, queryRefs []string) int {
	haystacks := []string{
		strings.ToLower(strings.TrimSpace(item.Name)),
		strings.ToLower(strings.TrimSpace(item.CanonicalName)),
		strings.ToLower(strings.TrimSpace(item.Kind)),
		strings.ToLower(strings.TrimSpace(item.File)),
		strings.ToLower(strings.TrimSpace(item.Module)),
		strings.ToLower(strings.TrimSpace(item.Signature)),
		strings.ToLower(strings.Join(item.Tags, " ")),
	}
	score := structuralSymbolPriority(item) / 20
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
	if projectAnalysisQAIntentNeedsAnswerPack(classifyProjectAnalysisQAIntent(query)) {
		pack := buildProjectStructureAnswerPack(artifacts, query)
		sources := []string{}
		if len(pack.RelevantDocs) > 0 {
			sources = append(sources, "generated_docs")
		}
		if len(pack.GraphViews) > 0 || len(pack.Symbols) > 0 || len(pack.Files) > 0 {
			sources = append(sources, "structure_answer_pack")
		}
		if hasSemanticIndexV2Data(artifacts.IndexV2) {
			sources = append(sources, "structural_index_v2")
		}
		if hasStructuralIndexData(artifacts.Structural) {
			sources = append(sources, "structural_index")
		}
		if len(pack.SecurityOverlays) > 0 {
			sources = append(sources, "security_overlay")
		}
		if len(pack.UnrealEdges) > 0 {
			sources = append(sources, "unreal_graph")
		}
		if len(pack.VerificationEntries) > 0 || len(pack.FuzzTargets) > 0 {
			sources = append(sources, "verification_or_fuzz")
		}
		return cachedAnalysisFastPathMetadata{
			Confidence: pack.Confidence,
			Sources:    analysisUniqueStrings(sources),
		}
	}
	subsystems := selectRelevantKnowledgeSubsystems(artifacts.Pack, query, 3)
	vectorDocs := selectRelevantVectorDocuments(artifacts.Corpus, query, 2)
	files := selectRelevantIndexedFiles(artifacts.Index, query, 3)
	symbols := selectRelevantSemanticSymbols(artifacts.Index, query, 4)
	structuralSymbols := selectRelevantStructuralSymbols(artifacts.Structural, query, 4)
	v2Hits := collectRelevantSemanticIndexV2Hits(artifacts.IndexV2, query)
	sources := []string{}
	if len(subsystems) > 0 {
		sources = append(sources, "knowledge_pack")
	}
	if len(vectorDocs) > 0 {
		sources = append(sources, "vector_corpus")
	}
	if len(files) > 0 || len(symbols) > 0 || len(structuralSymbols) > 0 {
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
	case len(subsystems) > 0 && len(vectorDocs) > 0 && (len(files) > 0 || len(symbols) > 0 || len(structuralSymbols) > 0 || len(v2Hits.Symbols) > 0 || len(v2Hits.Calls) > 0 || len(v2Hits.Overlays) > 0):
		confidence = "high"
	case len(subsystems) > 0 || len(vectorDocs) > 0 || len(files) > 0 || len(symbols) > 0 || len(structuralSymbols) > 0 || len(v2Hits.Files) > 0 || len(v2Hits.Symbols) > 0 || len(v2Hits.Calls) > 0 || len(v2Hits.Overlays) > 0:
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
	query := baseUserQueryText(latestExternalOrUserMessageText(a.Session.Messages))
	root := strings.TrimSpace(a.Workspace.BaseRoot)
	if root == "" {
		root = strings.TrimSpace(a.Session.WorkingDir)
	}
	freshness := evaluateLatestAnalysisFreshness(a.Config, root, artifacts, query)
	if !strings.EqualFold(strings.TrimSpace(freshness.Status), analysisFreshnessFresh) {
		return "", false, nil
	}
	meta := buildCachedAnalysisFastPathMetadata(artifacts, query)
	messages := append([]Message(nil), a.Session.Messages...)
	fastPathInstruction := "Fast-path check: Use only the cached project analysis already present in this conversation. Do not use tools and do not assume unseen code. If the cached analysis is sufficient to fully answer the user's latest request, answer now. Otherwise reply exactly NEEDS_TOOLS."
	if projectAnalysisQAIntentNeedsAnswerPack(classifyProjectAnalysisQAIntent(query)) {
		fastPathInstruction = "Fast-path check: Use only the latest cached project analysis and Project structure answer pack already present in this conversation. Do not use tools and do not assume unseen code. Prefer the latest project analysis over persistent memory; do not cite older memory as a stale caveat unless the answer pack or latest docs report the same marker. For deep structure questions, treat a medium/high confidence Project structure answer pack with source anchors, priority docs, graph views, and domain-specific critical anchors as sufficient for a grounded architecture answer. Respect domain_hints: for windows_driver, describe it as a Windows kernel/WDM .sys driver, not a DLL, unless source artifacts explicitly say DLL; if a file/minifilter subsystem exists, describe it as a subsystem unless build evidence says the whole driver is minifilter-only; describe dynamic kernel API resolver/wrapper modules as resolver/wrapper layers when that is what the anchors show. Separate user-mode IOCTL/control-client wrappers from kernel-side IRP/IOCTL dispatch and validation. Treat the domain flow map as a constrained architecture map, not permission to invent direct call chains; include every relevant Domain-specific flow map spine and every Required driver answer fact in the answer. Keep IRP create/open request-origin validation, IRP_MJ_DEVICE_CONTROL command dispatch, process notify callbacks, object callbacks, and Finalize/Unload teardown paths separate unless explicit call-edge evidence connects them. Do not place runtime filter start/registration symbols in DriverEntry/Core Initialize unless direct evidence says so; initialization symbols prepare state, while start/register symbols usually belong to runtime control or subsystem activation paths. Do not place request-origin validation symbols inside the DeviceIoControl command spine unless call-edge evidence says so; keep control-open validation separate from command-payload validation. Include both the device-control branch spine and REQUIRED device-control command spine when explaining IOCTL flow; do not stop at DeviceIoControl handler -> command dispatch if decrypt/shape/command-validation anchors are present. Spell out exact command spine symbols for payload decrypt/unpack, command validation, and requestor/control-process checks when the answer pack provides them. Use exact slash-separated folder paths and treat root folders as siblings. For top-level directory tables, copy the CLOSED SET or exact top-level directory table from Required driver answer facts and do not add extra rows. Never list paths from 'Never list these paths as top-level directory rows' as top-level directory rows. Do not nest one root folder under another unless the path explicitly says so. Do not invent root directories from source/header files; paths ending in .h, .hpp, .cpp, .c, .cc, .vcxproj, .sln, or .inf are files, not top-level folders. When IRP_MJ_DEVICE_CONTROL reaches DeviceIoControl, describe it as a branch of the IRP router. Use exact symbol names and exact file:line anchors; never replace known line numbers with ellipsis and never relabel helper/accessor anchors as lifecycle functions. Control PID/accessor symbols are not Finalize/Unload lifecycle functions. Cover structure layers, execution or dependency flow, key source anchors, impact or verification points, stale caveats when real markers are present, and next docs or files to read. If no real stale markers are present, say the cached analysis did not report stale markers. Reply exactly NEEDS_TOOLS only when the pack is absent, marked current_source_needed, or lacks source anchors/priority docs needed for the user's question."
	}
	messages = append(messages, internalUserMessage(fastPathInstruction))
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
	if projectAnalysisFastPathReplyNeedsTools(reply) || reply == "" {
		return "", false, nil
	}
	if projectAnalysisQAIntentNeedsAnswerPack(classifyProjectAnalysisQAIntent(query)) {
		pack := firstArchitectureFactPack(artifacts.Snapshot.ArchitectureFacts, artifacts.Pack.ArchitectureFacts)
		evaluation := evaluateArchitectureAnswerAgainstFacts(reply, pack)
		if architectureAnswerHasBlockingViolations(evaluation) {
			return "", false, nil
		}
	}
	return formatCachedAnalysisFastPathReply(reply, meta), true, nil
}

func projectAnalysisFastPathReplyNeedsTools(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}
	if strings.EqualFold(trimmed, projectAnalysisFastPathNeedsTools) {
		return true
	}
	firstLine := trimmed
	if idx := strings.IndexAny(firstLine, "\r\n"); idx >= 0 {
		firstLine = strings.TrimSpace(firstLine[:idx])
	}
	if strings.EqualFold(firstLine, projectAnalysisFastPathNeedsTools) {
		return true
	}
	upper := strings.ToUpper(trimmed)
	return strings.HasPrefix(upper, projectAnalysisFastPathNeedsTools+" ") ||
		strings.HasPrefix(upper, projectAnalysisFastPathNeedsTools+":") ||
		strings.HasPrefix(upper, projectAnalysisFastPathNeedsTools+".")
}

func (a *Agent) shouldTryProjectAnalysisFastPath() bool {
	if a == nil || a.Session == nil {
		return false
	}
	lastUser := strings.TrimSpace(latestExternalUserMessageRawText(a.Session.Messages))
	if lastUser == "" {
		return false
	}
	if !strings.Contains(lastUser, "Relevant project analysis from past analyze-project runs") &&
		!latestTurnInternalContextContains(a.Session.Messages, "Relevant project analysis from past analyze-project runs") {
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

func latestTurnInternalContextContains(messages []Message, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		if messageIsInternalUserGuidance(msg) {
			if strings.Contains(msg.Text, needle) {
				return true
			}
			continue
		}
		return false
	}
	return false
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
	// Slash commands are intercepted upstream by ParseCommand/handleCommand and
	// never reach this natural-language pipeline. A leading "/" here is just a
	// stray path-like token, so do not blanket-assume action/edit intent for it.
	if looksLikeExecutionFlowQuestion(lower) {
		return false
	}
	return containsAny(lower,
		"add ", "apply ", "build ", "change ", "commit ", "compile ", "create ", "delete ", "draft ", "edit ", "fix ", "generate ", "implement ", "modify ", "patch ", "prepare ", "refactor ", "remove ", "rename ", "replace ", "run ", "test ", "update ", "write ",
		"리뷰", "검토", "고쳐", "구현", "만들", "변경", "빌드", "삭제", "생성", "수정", "실행", "적용", "작성", "저장", "추가", "테스트", "패치",
	)
}

func looksLikeExecutionFlowQuestion(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		lower = strings.ToLower(strings.TrimSpace(text))
	}
	return containsAny(lower, "실행 흐름", "실행 경로", "실행 구조", "실행 순서", "runtime flow", "execution flow", "execution path", "startup flow", "request flow") &&
		containsAny(lower, "설명", "분석", "구조", "어떻게", "trace", "flow", "경로")
}

func looksLikeAnswerDeliveryRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		lower = strings.ToLower(strings.TrimSpace(text))
	}
	return containsAny(lower,
		"tell me", "explain", "describe", "walk me through", "overview of", "summarize", "summary", "compare", "list ", "list the", "show me", "what ", "which ", "how ",
		"알려줘", "알려 줘", "말해줘", "말해 줘", "설명해", "설명해줘", "설명해 줘", "정리해", "정리해서", "요약해", "요약해줘", "비교해", "나열해",
	)
}

func looksLikeDocumentArtifactOutputRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		lower = strings.ToLower(strings.TrimSpace(text))
	}
	if lower == "" {
		return false
	}
	if looksLikeGoalPromptFileArtifactRequest(lower) {
		return true
	}
	if containsAny(lower,
		".md", ".markdown", "markdown file", "markdown으로", "md로", "in markdown", "as markdown", "as a file", "save to file", "write to file", "create file", "report document",
		"파일", "파일로", "파일에", "저장", "별도 문서", "별도 보고서", "문서로", "문서에", "보고서로", "보고서에", "마크다운으로",
	) {
		return true
	}
	hasArtifactNoun := containsAny(lower,
		"document", "documents", "markdown", "report", "reports", "write-up", "writeup", "notes", "spec", "specs",
		"문서", "문서들", "마크다운", "보고서", "명세", "스펙",
	)
	hasOutputVerb := containsAny(lower,
		"author ", "create ", "draft ", "generate ", "prepare ", "save ", "write ",
		"작성", "만들", "생성", "저장", "초안",
	)
	return hasArtifactNoun && hasOutputVerb
}

func looksLikeAnswerOnlyKnowledgeRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		lower = strings.ToLower(strings.TrimSpace(text))
	}
	if lower == "" || strings.HasPrefix(lower, "/") {
		return false
	}
	if looksLikeDocumentArtifactOutputRequest(lower) || looksLikeExplicitGitIntent(lower) {
		return false
	}
	// Use whole-word matching for ASCII edit verbs so "explain the prefix matching"
	// or "explain the run loop" is not mistaken for an edit request, and require KO
	// imperative forms rather than bare nouns ("변경"/"추가"/"수정") so analysis-only
	// questions like "최신 커널 변경사항 알려줘" stay answer-only.
	if containsWord(lower,
		"apply", "commit", "delete", "edit", "fix", "implement", "modify", "patch", "refactor", "remove", "replace", "update",
	) {
		return false
	}
	if containsAny(lower,
		"적용해", "커밋해", "고쳐", "고치", "구현해", "삭제해",
		"수정해", "수정하", "변경해", "변경하", "추가해", "추가하", "패치해", "패치하", "반영해", "반영하",
	) {
		return false
	}
	if !looksLikeAnswerDeliveryRequest(lower) {
		return false
	}
	return containsAny(lower,
		"architecture", "capability", "capabilities", "compare", "difference", "features", "flow", "gap", "gaps", "limitation", "limitations", "missing", "structure", "weakness",
		"구조", "기능", "대비", "비교", "부족", "설계", "아키텍처", "역량", "제약", "차이", "한계", "흐름", "무엇", "뭐가", "어떤", "왜",
	)
}

func prefersReadOnlyAnalysisIntent(text string) bool {
	base := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if base == "" {
		return false
	}
	if looksLikeReviewInspectionOnlyRequest(base) {
		return true
	}
	if looksLikeExplicitEditIntent(base) {
		return false
	}
	if looksLikeAnswerOnlyKnowledgeRequest(base) {
		return true
	}
	if strings.Contains(base, "?") {
		return true
	}
	return containsAny(base,
		"analy", "analysis", "diagnos", "explain", "investigat", "why ", "why is", "why does", "reason", "root cause", "document", "summarize",
		"분석", "원인", "이유", "설명", "조사", "문서화", "진단", "왜", "동작할 수 없", "동작하지 않", "안되는", "안 돼", "안되",
	)
}

// looksLikeStateQueryQuestion reports whether the request is asking whether
// something is ALREADY in a given state ("구현되어 있어?", "반영됐어?",
// "적용돼 있나?") rather than ordering a change. Korean passive/perfective
// forms (V되다 / V돼다 / V됐다) embed an edit-verb stem, so the substring-based
// Hangul keyword matcher would otherwise read such a status question as an
// edit command. A genuine imperative edit order ("구현해줘", "수정해") is
// detected first and excluded, so only true status questions stay read-only.
func looksLikeStateQueryQuestion(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	// A real imperative source-edit command is never a state query, even when it
	// also references the current state ("안 돼 있으면 구현해줘").
	if looksLikeImperativeSourceEditCommand(lower) {
		return false
	}
	// Passive/perfective state markers describe an existing state, so a request
	// built around them is asking about status, not commanding a mutation.
	return containsAny(lower,
		"되어 있", "되어있", "돼 있", "돼있", "된 상태", "되었", "됐어", "됐나", "됐는지",
		"되어 있나", "되어있나", "되어 있는지", "돼 있나", "돼 있는지",
		"구현돼", "구현되", "반영돼", "반영되", "적용돼", "적용되",
	)
}

func looksLikeExplicitEditIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	// Slash commands are dispatched upstream and never reach this pipeline; a
	// leading "/" here is a stray token, not a guaranteed edit command. Fall
	// through to the verb checks (least-privilege default) instead of forcing edit.
	if looksLikeGoalPromptDraftOnlyRequest(lower) {
		return false
	}
	if looksLikeExecutionFlowQuestion(lower) {
		return false
	}
	if looksLikeStateQueryQuestion(lower) {
		return false
	}
	if looksLikePlanOrDirectionOnlyRequest(lower) {
		return false
	}
	// Descriptive questions ("what is the latest update on the schedule?",
	// "explain how the patch tuesday cycle works") often contain noun-form verbs
	// like update/patch/change. They are edit intent only when an imperative
	// source-edit command is actually present; otherwise an answer-delivery
	// question must stay read-only (least-privilege default).
	if looksLikeAnswerDeliveryRequest(lower) && !looksLikeImperativeSourceEditCommand(lower) {
		return false
	}
	// Execution verbs (run/build/test/compile and KO equivalents) describe
	// running code, not editing source. They must NOT imply an edit on their own;
	// only a real source-mutation verb or imperative form counts as edit intent.
	// "fix the failing test" still matches the repair verb "fix" below, so a
	// genuine repair request stays editable.
	// "commit" is a git verb handled by looksLikeExplicitGitIntent, not a source
	// edit, so it is intentionally absent here.
	if containsWord(lower,
		"add", "apply", "change", "create", "delete", "draft", "edit",
		"fix", "generate", "implement", "modify", "patch", "prepare", "refactor",
		"remove", "rename", "replace", "update", "write",
	) {
		return true
	}
	if containsAny(lower, "make the change", "make this change", "make changes") {
		return true
	}
	// KO imperative source-edit forms only (bare nouns such as "변경/추가/수정"
	// describing content must not flip an answer-only question to edit intent).
	// The insertion/wiring forms (넣어두/넣어줘/집어넣/써넣) cover requests that ask
	// to put a value into a config or source file and wire it up (for example
	// ".env에 토큰을 넣어두고 사용하게 하자"), which are genuine edits the bare-noun
	// list misses.
	return containsAny(lower,
		"고쳐", "구현해", "구현하", "만들", "삭제", "생성", "적용", "작성", "저장",
		"수정해", "수정하", "변경해", "변경하", "추가해", "추가하", "패치해", "패치하",
		"넣어두", "넣어줘", "넣어 줘", "넣어서", "집어넣", "써넣",
	)
}

// looksLikeImperativeSourceEditCommand returns true only when the request
// carries a command-form repair/implement directive aimed at SOURCE code, not
// a document. Bare authoring verbs (write/create/draft/generate and their KO
// equivalents) are intentionally excluded. This is the single shared notion consulted by the
// request envelope, the review request class default, and the boundary policy
// so they all agree on what counts as an imperative source-edit order.
func looksLikeImperativeSourceEditCommand(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	if hasRepairActionNegation(lower) {
		return false
	}
	// Edit term only describing document content is not an imperative command.
	if looksLikeModificationDescribedAsDocumentContent(lower) {
		return false
	}
	return containsAny(lower,
		"fix it", "fix this", "fix the bug", "fix the", "fix main", "fix ", "please fix", "go fix",
		"implement it", "implement the", "make the change", "make this change", "make changes", "make the fix",
		"apply the fix", "patch it", "patch the", "modify it", "modify the", "edit the",
		"refactor it", "refactor the", "replace the", "rename the", "change the", "correct the",
		"고쳐줘", "고쳐 줘", "고쳐주", "고쳐", "고치", "수정해줘", "수정해 줘", "수정해", "수정하",
		"구현해줘", "구현해 줘", "구현해", "패치해", "반영해", "변경해", "교체해", "삭제해", "해결해",
		"리팩터링해줘", "리팩토링해줘",
	)
}

// hasGitActionNegationOrQuestion gates git-mutation intent: a request that
// forbids git ("do not commit", "커밋하지 마", "커밋 없이") or merely asks about
// git ("git push가 뭐야?", "what does git push do?") must never grant git
// mutation. Mirrors hasRepairActionNegation for the git surface.
func hasGitActionNegationOrQuestion(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" {
		return false
	}
	if containsAny(lower,
		"커밋하지 마", "커밋 하지 마", "커밋하지마", "커밋하지 말", "커밋 하지 말", "커밋은 하지", "커밋 없이", "커밋하지는 말",
		"푸시하지 마", "푸시 하지 마", "푸시하지마", "푸시하지 말", "푸시 하지 말", "푸시 없이",
		"스테이징하지 마", "스테이지하지 마",
		"do not commit", "don't commit", "dont commit", "no commit", "without committing", "without commit",
		"do not push", "don't push", "dont push", "no push", "without pushing",
		"do not stage", "don't stage", "without staging",
		"do not create a pr", "don't create a pr", "without a pr", "without creating a pr",
	) {
		return true
	}
	// Answer-only / question phrasing about git ("what does git push do?",
	// "git push가 뭐야?", "explain git commit") asks rather than orders.
	if containsAny(lower,
		"뭐야", "뭔가요", "무엇", "뭐가", "어떻게", "차이", "설명", "알려줘", "알려 줘", "의미",
		"what does", "what is", "what are", "how does", "how do", "explain", "describe", "difference between", "tell me about",
	) {
		return true
	}
	return false
}

func looksLikeExplicitGitIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	if hasGitActionNegationOrQuestion(lower) {
		return false
	}
	if containsAny(lower,
		"git add", "git commit", "git push", "git stage", "git stash", "create a pr", "create pr", "open a pr", "open pr", "pull request",
		// Repo bootstrap (init/clone) is an explicit, user-requested git action even
		// though it is not in the commit/push verb set; recognize it so a plain
		// "git init" / "저장소 초기화" is not blocked as an unrequested git mutation.
		"git init", "git clone", "initialize a git", "initialize the git", "init a git", "set up a git repo", "set up git",
		"clone the repo", "clone this repo", "clone the repository",
		"git 초기화", "깃 초기화", "저장소 초기화", "레포 초기화", "리포 초기화", "리포지토리 초기화", "repo 초기화", "repository 초기화",
		"git 저장소 초기화", "git 저장소 만들", "깃 저장소 만들", "저장소 복제", "레포 복제", "리포 복제",
		"stage these changes", "stage the changes", "stage this", "stage it", "stage everything", "stage all",
		"commit these changes", "commit the changes", "commit the staged changes", "commit this", "commit it", "commit everything", "commit all",
		"push this branch", "push the branch", "push these changes", "push it",
		"check in these changes", "check in this",
		"commit해", "commit하고", "push해", "push하고",
		"커밋해", "커밋해줘", "커밋해 줘", "커밋하자", "커밋하죠", "커밋 진행", "커밋할", "커밋해도", "커밋하고",
		"스테이징해", "스테이징해줘", "스테이징해 줘", "스테이지해", "스테이지해줘", "스테이지해 줘",
		"푸시해", "푸시해줘", "푸시해 줘", "푸시하고", "브랜치 푸시",
		"pr 만들어", "pr 열어", "pull request 만들어", "pull request 열어", "풀 리퀘스트 만들어", "풀 리퀘스트 열어",
	) {
		return true
	}
	// Branch operations (rename/switch/create/use a branch) are explicit git
	// mutations even though they are not in the commit/push verb set. A plain
	// "브랜치를 main으로 써야 해" / "rename the branch to main" must not be blocked
	// as an unrequested git action.
	if looksLikeBranchMutationIntent(lower) {
		return true
	}
	// Two or more distinct git verbs together (e.g. "commit and push", "stage and
	// commit") are unambiguously a git request even without an explicit object.
	gitVerbs := []string{"commit", "push", "stage", "amend", "rebase", "cherry-pick", "squash"}
	verbHits := 0
	for _, v := range gitVerbs {
		if requestTextHasWord(lower, v) {
			verbHits++
		}
	}
	if verbHits >= 2 {
		return true
	}
	if verbHits == 0 {
		return false
	}
	// A single bare whole-word git verb (no embedding inside "multistage" etc.)
	// requires a git object/context word (EN or KO) so plain "push the button"
	// does not match.
	if containsWord(lower, "git", "branch", "changes", "staged", "pr", "remote", "origin", "head", "everything") {
		return true
	}
	return containsAny(lower, "변경사항", "변경분", "변경 사항", "브랜치", "스테이징", "원격")
}

// looksLikeBranchMutationIntent reports whether the text asks to change the git
// branch (rename, switch, create, or "use branch X"). It requires a branch noun
// plus an action/target cue so that questions like "what branch is this?" (which
// are filtered earlier as questions) or passing mentions do not match.
func looksLikeBranchMutationIntent(lower string) bool {
	// Direct branch CLI verbs are unambiguous git mutations.
	if containsAny(lower, "git branch", "git checkout", "git switch", "checkout -b", "switch -c") {
		return true
	}
	if containsAny(lower, "branch", "브랜치") {
		return containsAny(lower,
			// English branch actions / targets
			"rename", "switch", "checkout", "create", "to main", "to master", "to a new",
			"default branch", "set the branch", "set branch",
			// Korean branch actions / targets
			"변경", "바꿔", "바꾸", "만들", "생성", "전환", "이름", "사용",
			"main으로", "메인으로", "master로", "마스터로", "main 으로", "메인 으로",
		)
	}
	// "switch to <branch>" / "check out <branch>" without the literal word
	// "branch" but with a branch-looking target. containsWord avoids matching
	// "main" inside "domain".
	if containsAny(lower, "switch to ", "switch back to ", "check out ", "checkout ") &&
		containsWord(lower, "main", "master", "develop", "trunk") {
		return true
	}
	return false
}

func looksLikeDocumentAuthoringIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	if looksLikeGoalPromptDraftOnlyRequest(lower) {
		return false
	}
	if looksLikeGoalPromptFileArtifactRequest(lower) {
		return true
	}
	hasDocumentNoun := containsAny(lower,
		"document", "documents", "doc", "markdown", ".md", "report", "reports", "write-up", "writeup", "research", "paper", "papers", "notes", "spec", "specs",
		"문서", "문서들", "마크다운", "보고서", "리서치", "연구", "초안", "명세", "스펙",
	)
	if !hasDocumentNoun {
		return false
	}
	if looksLikeAnswerDeliveryRequest(lower) && !looksLikeDocumentArtifactOutputRequest(lower) {
		return false
	}
	// An explicit document-output sink ("...문서로", "...markdown으로", ".md")
	// makes the document the deliverable even when the authoring verb is elided.
	if looksLikeDocumentArtifactOutputRequest(lower) {
		return true
	}
	return containsAny(lower,
		"add ", "author ", "create ", "draft ", "generate ", "prepare ", "revise ", "update ", "write ",
		"작성", "만들", "생성", "업데이트", "정리", "초안", "추가",
	)
}

func looksLikeGoalPromptDraftOnlyRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" {
		return false
	}
	if !looksLikeGoalPromptAuthoringRequest(lower) {
		return false
	}
	return !looksLikeExplicitGoalActivationOrArtifactRequest(lower)
}

func looksLikeGoalPromptFileArtifactRequest(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" || !looksLikeGoalPromptAuthoringRequest(lower) {
		return false
	}
	return containsAny(lower,
		"save to file", "save as", "write to file", "create file", "as a file", "markdown file",
		"파일로 저장", "파일에 저장", "파일을 만들어", "파일 만들어", "파일 생성", "파일에 써",
		".md 파일", ".md로 저장", "markdown 파일", "마크다운 파일", "저장해", "저장하",
	)
}

func looksLikeGoalPromptAuthoringRequest(lower string) bool {
	return containsAny(lower,
		"goal prompt", "goal 프롬프트", "goal프롬프트", "목표 프롬프트", "목표프롬프트",
		"goal 초안", "goal초안", "목표 초안", "목표초안", "goal 문안", "goal문안",
		"goal markdown", "goal 마크다운", "goal마크다운",
	)
}

func looksLikeExplicitGoalActivationOrArtifactRequest(lower string) bool {
	return containsAny(lower,
		"/goal", "goal run", "run goal",
		"-goal", "-goal-file", "--run", "--until-complete", "--file", "@goal", "@ goal",
		"active goal", "activate goal", "execute goal", "save to file", "save as", "write to file", "create file",
		"goal을 실행", "goal 실행", "goal을 시작", "goal 시작", "goal로 실행", "내장 goal", "goal 기능을 켜",
		"파일로 저장", "파일에 저장", "파일을 만들어", "파일 만들어", "파일 생성", "파일에 써",
		".md 파일", ".md로 저장", "markdown file", "마크다운 파일", "저장해", "저장하",
	)
}

func baseUserQueryText(text string) string {
	trimmed := strings.TrimSpace(text)
	markers := []string{
		"\n\nRequest mode:",
		"\n\nRequest envelope:",
		"\n\nGit intent:\n",
		"\n\nActivated skills for this request:\n",
		"\n\nAttached context:\n",
		"\n\nPending review repair confirmation:\n",
		"\n\nPending reviewer-gate repair confirmation:\n",
		"\n\nRelevant persistent memory from past sessions:\n",
		"\n\nRelevant project analysis from past analyze-project runs:\n",
		"\n\nAuto-discovered code context:\n",
		"\n\n[Conversation Runtime Context]\n",
	}
	cut := len(trimmed)
	for _, marker := range markers {
		if idx := strings.Index(trimmed, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	return strings.TrimSpace(trimmed[:cut])
}
