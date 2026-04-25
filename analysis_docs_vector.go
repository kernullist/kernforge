package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func buildAnalysisDocsVectorDocuments(run ProjectAnalysisRun) []VectorCorpusDocument {
	docsRun := run
	docsRun.VectorCorpus = VectorCorpus{}
	docs := buildAnalysisDocs(docsRun)
	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)

	out := []VectorCorpusDocument{}
	for _, name := range names {
		body := strings.TrimSpace(docs[name])
		if body == "" {
			continue
		}
		docMeta := analysisDocsVectorMetadata(docsRun, name, AnalysisDocSection{})
		out = append(out, VectorCorpusDocument{
			ID:       "generated_doc:" + sanitizeFileName(name),
			Kind:     "generated_doc",
			Title:    analysisDocTitle(name),
			Text:     body,
			PathHint: name,
			Metadata: docMeta,
		})
		knownSections := analysisDocsVectorSectionByTitle(docsRun, name)
		for _, section := range splitAnalysisMarkdownSections(body) {
			section.Title = strings.TrimSpace(section.Title)
			section.Text = strings.TrimSpace(section.Text)
			if section.Title == "" || section.Text == "" {
				continue
			}
			metadataSection := knownSections[strings.ToLower(section.Title)]
			if strings.TrimSpace(metadataSection.Title) == "" {
				metadataSection = AnalysisDocSection{
					ID:            "generated." + sanitizeFileName(section.Title),
					Title:         section.Title,
					SourceAnchors: analysisDocSourceAnchors(docsRun, name),
					Confidence:    analysisDocConfidence(docsRun, name),
					StaleMarkers:  analysisDocStaleMarkers(docsRun, name),
					ReuseTargets:  analysisDocReuseTargets(name),
				}
			}
			out = append(out, VectorCorpusDocument{
				ID:       "generated_doc_section:" + sanitizeFileName(name) + ":" + sanitizeFileName(section.Title),
				Kind:     "generated_doc_section",
				Title:    analysisDocTitle(name) + " / " + section.Title,
				Text:     section.Text,
				PathHint: firstNonBlankAnalysisString(firstSliceValue(metadataSection.SourceAnchors), name),
				Metadata: analysisDocsVectorMetadata(docsRun, name, metadataSection),
			})
		}
	}
	return out
}

func analysisDocsVectorMetadata(run ProjectAnalysisRun, name string, section AnalysisDocSection) map[string]string {
	anchors := analysisDocSourceAnchors(run, name)
	confidence := analysisDocConfidence(run, name)
	staleMarkers := analysisDocStaleMarkers(run, name)
	reuseTargets := analysisDocReuseTargets(name)
	if len(section.SourceAnchors) > 0 {
		anchors = section.SourceAnchors
	}
	if strings.TrimSpace(section.Confidence) != "" {
		confidence = section.Confidence
	}
	if len(section.StaleMarkers) > 0 {
		staleMarkers = section.StaleMarkers
	}
	if len(section.ReuseTargets) > 0 {
		reuseTargets = section.ReuseTargets
	}
	metadata := map[string]string{
		"source":         "generated_docs",
		"doc_name":       name,
		"doc_path":       name,
		"doc_kind":       analysisDocKind(name),
		"confidence":     confidence,
		"source_anchors": strings.Join(analysisUniqueStrings(anchors), ";"),
		"reuse_targets":  strings.Join(analysisUniqueStrings(reuseTargets), ";"),
	}
	if len(staleMarkers) > 0 {
		metadata["stale_markers"] = strings.Join(analysisUniqueStrings(staleMarkers), ";")
	}
	if strings.TrimSpace(section.ID) != "" {
		metadata["section_id"] = section.ID
	}
	if strings.TrimSpace(section.Title) != "" {
		metadata["section_title"] = section.Title
	}
	return metadata
}

func analysisDocsVectorSectionByTitle(run ProjectAnalysisRun, name string) map[string]AnalysisDocSection {
	out := map[string]AnalysisDocSection{}
	for _, section := range analysisDocSections(run, name) {
		title := strings.ToLower(strings.TrimSpace(section.Title))
		if title == "" {
			continue
		}
		out[title] = section
	}
	return out
}

type analysisMarkdownSection struct {
	Title string
	Text  string
}

func splitAnalysisMarkdownSections(markdown string) []analysisMarkdownSection {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	out := []analysisMarkdownSection{}
	currentTitle := ""
	currentLines := []string{}
	flush := func() {
		if strings.TrimSpace(currentTitle) == "" {
			currentLines = nil
			return
		}
		out = append(out, analysisMarkdownSection{
			Title: currentTitle,
			Text:  strings.TrimSpace(strings.Join(currentLines, "\n")),
		})
		currentLines = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") {
			flush()
			currentTitle = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			currentLines = []string{fmt.Sprintf("## %s", currentTitle)}
			continue
		}
		if strings.TrimSpace(currentTitle) != "" {
			currentLines = append(currentLines, line)
		}
	}
	flush()
	return out
}

func writeVectorCorpusArtifactSet(corpus VectorCorpus, ingestion VectorIngestionManifest, dir string, prefix string) error {
	if len(corpus.Documents) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if ingestion.DocumentCount == 0 {
		ingestion = buildVectorIngestionManifest(corpus)
	}
	name := func(suffix string) string {
		if strings.TrimSpace(prefix) == "" {
			return filepath.Join(dir, suffix)
		}
		return filepath.Join(dir, prefix+"_"+suffix)
	}
	corpusData, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(name("vector_corpus.json"), corpusData, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(name("vector_corpus.jsonl"), []byte(buildVectorCorpusJSONL(corpus)), 0o644); err != nil {
		return err
	}
	manifestData, err := json.MarshalIndent(ingestion, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(name("vector_ingest_manifest.json"), manifestData, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(name("vector_ingest_records.jsonl"), []byte(buildVectorIngestionRecordsJSONL(corpus)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(name("vector_pgvector.sql"), []byte(buildVectorPGVectorSQL(corpus)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(name("vector_sqlite.sql"), []byte(buildVectorSQLiteSQL(corpus)), 0o644); err != nil {
		return err
	}
	return os.WriteFile(name("vector_qdrant.jsonl"), []byte(buildVectorQdrantSeedJSONL(corpus)), 0o644)
}
