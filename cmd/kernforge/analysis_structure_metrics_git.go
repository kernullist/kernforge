package main

// Git-history structure metrics: change hotspots (churn x size) and temporal
// change coupling (files that change together without a static import edge).
//
// All git access is bounded (commit window + count caps + context timeout) and
// degrades gracefully: any failure returns ok=false and the pure graph metrics
// still stand on their own.

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const structureGitCommitMarker = "\x1fKF\x1f"

type structureGitCommit struct {
	Hash  string
	Date  string
	Files []string
}

// structureCollectGitHistory runs a single bounded `git log --name-only` and
// maps changed paths back to scanned snapshot files. Returns ok=false when the
// workspace is not a usable repository.
func structureCollectGitHistory(ctx context.Context, snapshot ProjectSnapshot, gitRoot string) ([]structureGitCommit, bool) {
	gitRoot = strings.TrimSpace(gitRoot)
	if gitRoot == "" {
		return nil, false
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	format := "--pretty=format:" + structureGitCommitMarker + "%H|%cI"
	stdout, _, err := runGitHelperSplit(timeoutCtx, gitRoot,
		"log",
		"--no-merges",
		"--since=90 days ago",
		"--max-count=800",
		"--name-only",
		format,
	)
	if err != nil {
		return nil, false
	}
	prefix := structureGitPathPrefix(gitRoot, snapshot.Root)
	commits := []structureGitCommit{}
	var current *structureGitCommit
	flush := func() {
		if current != nil && len(current.Files) > 0 {
			commits = append(commits, *current)
		}
		current = nil
	}
	for _, rawLine := range strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if strings.HasPrefix(line, structureGitCommitMarker) {
			flush()
			header := strings.TrimPrefix(line, structureGitCommitMarker)
			hash := header
			date := ""
			if idx := strings.Index(header, "|"); idx >= 0 {
				hash = header[:idx]
				date = header[idx+1:]
			}
			current = &structureGitCommit{Hash: strings.TrimSpace(hash), Date: strings.TrimSpace(date)}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || current == nil {
			continue
		}
		mapped, ok := structureMapGitPath(snapshot, prefix, trimmed)
		if !ok {
			continue
		}
		current.Files = append(current.Files, mapped)
	}
	flush()
	if len(commits) == 0 {
		return nil, false
	}
	return commits, true
}

func structureGitPathPrefix(gitRoot string, snapshotRoot string) string {
	rel, err := filepath.Rel(gitRoot, snapshotRoot)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return ""
	}
	return strings.Trim(rel, "/")
}

func structureMapGitPath(snapshot ProjectSnapshot, prefix string, gitPath string) (string, bool) {
	clean := strings.Trim(strings.TrimSpace(gitPath), "\"")
	clean = filepath.ToSlash(clean)
	if clean == "" {
		return "", false
	}
	if prefix != "" {
		if !strings.HasPrefix(clean, prefix+"/") {
			return "", false
		}
		clean = strings.TrimPrefix(clean, prefix+"/")
	}
	if _, ok := snapshot.FilesByPath[clean]; ok {
		return clean, true
	}
	return "", false
}

func structureHotspots(snapshot ProjectSnapshot, commits []structureGitCommit, inCycleFiles map[string]struct{}) []StructureHotspot {
	commitCount := map[string]int{}
	lastDate := map[string]string{}
	for _, commit := range commits {
		for _, file := range commit.Files {
			commitCount[file]++
			if _, ok := lastDate[file]; !ok {
				lastDate[file] = commit.Date
			}
		}
	}
	hotspots := []StructureHotspot{}
	for path, count := range commitCount {
		if count < 2 {
			continue
		}
		lineCount := 0
		if file, ok := snapshot.FilesByPath[path]; ok {
			lineCount = file.LineCount
		}
		_, inCycle := inCycleFiles[path]
		hotspots = append(hotspots, StructureHotspot{
			Path:        analysisDocSlashPath(path),
			Commits:     count,
			LineCount:   lineCount,
			Score:       count * structureSizeBucket(lineCount),
			LastChanged: structureShortDate(lastDate[path]),
			InCycle:     inCycle,
		})
	}
	sort.Slice(hotspots, func(i int, j int) bool {
		if hotspots[i].Score != hotspots[j].Score {
			return hotspots[i].Score > hotspots[j].Score
		}
		if hotspots[i].Commits != hotspots[j].Commits {
			return hotspots[i].Commits > hotspots[j].Commits
		}
		return hotspots[i].Path < hotspots[j].Path
	})
	if len(hotspots) > 25 {
		hotspots = hotspots[:25]
	}
	return hotspots
}

func structureChangeCoupling(commits []structureGitCommit, fileGraph map[string][]string) []ChangeCouplingEdge {
	staticPairs := map[string]struct{}{}
	for source, targets := range fileGraph {
		for _, target := range targets {
			staticPairs[structurePairKey(source, target)] = struct{}{}
		}
	}
	pairCount := map[string]int{}
	pairEndpoints := map[string][2]string{}
	for _, commit := range commits {
		files := analysisUniqueStrings(commit.Files)
		if len(files) < 2 || len(files) > structureMetricsGitMaxCommitSize {
			continue
		}
		sort.Strings(files)
		for i := 0; i < len(files); i++ {
			for j := i + 1; j < len(files); j++ {
				key := structurePairKey(files[i], files[j])
				if _, ok := staticPairs[key]; ok {
					continue
				}
				pairCount[key]++
				if _, ok := pairEndpoints[key]; !ok {
					pairEndpoints[key] = [2]string{files[i], files[j]}
				}
			}
		}
	}
	edges := []ChangeCouplingEdge{}
	for key, count := range pairCount {
		if count < structureMetricsChangeCoupleMin {
			continue
		}
		endpoints := pairEndpoints[key]
		edges = append(edges, ChangeCouplingEdge{
			Source:        analysisDocSlashPath(endpoints[0]),
			Target:        analysisDocSlashPath(endpoints[1]),
			SharedChanges: count,
			Confidence:    structureChangeCouplingConfidence(count),
		})
	}
	sort.Slice(edges, func(i int, j int) bool {
		if edges[i].SharedChanges != edges[j].SharedChanges {
			return edges[i].SharedChanges > edges[j].SharedChanges
		}
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		return edges[i].Target < edges[j].Target
	})
	if len(edges) > 25 {
		edges = edges[:25]
	}
	return edges
}

func structureSizeBucket(lines int) int {
	switch {
	case lines < 100:
		return 1
	case lines < 300:
		return 2
	case lines < 800:
		return 3
	case lines < 2000:
		return 4
	default:
		return 5
	}
}

func structurePairKey(left string, right string) string {
	if left <= right {
		return left + "\x00" + right
	}
	return right + "\x00" + left
}

func structureChangeCouplingConfidence(count int) string {
	switch {
	case count >= 6:
		return "high"
	case count >= 4:
		return "medium"
	default:
		return "low"
	}
}

func structureShortDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}
