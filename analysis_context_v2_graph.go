package main

import (
	"sort"
	"strings"
)

func expandRelevantSemanticIndexV2Hits(index SemanticIndexV2, hits relevantSemanticIndexV2Hits, query string) relevantSemanticIndexV2Hits {
	seedIDs := map[string]struct{}{}
	seedFiles := map[string]struct{}{}
	for _, item := range hits.Symbols {
		seedIDs[item.ID] = struct{}{}
		if strings.TrimSpace(item.File) != "" {
			seedFiles[item.File] = struct{}{}
		}
	}
	for _, item := range hits.Files {
		seedFiles[item.Path] = struct{}{}
		for _, ctxID := range item.BuildContextIDs {
			if strings.TrimSpace(ctxID) != "" {
				seedIDs[ctxID] = struct{}{}
			}
		}
	}
	for _, item := range hits.Overlays {
		seedIDs[item.SourceID] = struct{}{}
		seedIDs[item.TargetID] = struct{}{}
	}
	for _, item := range hits.Calls {
		seedIDs[item.SourceID] = struct{}{}
		seedIDs[item.TargetID] = struct{}{}
	}
	for _, item := range hits.Builds {
		seedIDs[item.SourceID] = struct{}{}
		seedIDs[item.TargetID] = struct{}{}
	}
	if len(hits.BuildContexts) == 0 {
		hits.BuildContexts = selectRelevantV2BuildContexts(index, query, hits.Mode, 2)
	}
	for _, item := range hits.BuildContexts {
		seedIDs[item.ID] = struct{}{}
		for _, file := range item.Files {
			if strings.TrimSpace(file) != "" {
				seedFiles[file] = struct{}{}
			}
		}
	}
	if len(seedIDs) == 0 {
		for _, item := range selectRelevantV2Symbols(index, query, hits.Mode, 2) {
			seedIDs[item.ID] = struct{}{}
		}
	}

	depth := 1
	switch hits.Mode {
	case "trace", "impact", "security":
		depth = 2
	}
	nodeSet := expandSemanticV2Neighborhood(index, seedIDs, seedFiles, depth)
	hits.BuildContexts = mergeRelevantBuildContexts(index, hits.BuildContexts, nodeSet, seedFiles, 3)
	hits.Symbols = mergeRelevantSymbols(index, hits.Symbols, nodeSet, 6)
	hits.Files = mergeRelevantFiles(index, hits.Files, nodeSet, seedFiles, 4)
	hits.Calls = mergeRelevantCallEdges(index, hits.Calls, nodeSet, seedFiles, 5)
	hits.Inheritance = mergeRelevantInheritanceEdges(index, hits.Inheritance, nodeSet, seedFiles, 4)
	hits.Builds = mergeRelevantBuildEdges(index, hits.Builds, nodeSet, seedFiles, 5)
	hits.Overlays = mergeRelevantOverlayEdges(index, hits.Overlays, nodeSet, seedFiles, 5)
	hits.References = mergeRelevantReferences(index, hits.References, nodeSet, seedFiles, 5)
	hits.Occurrences = mergeRelevantOccurrences(index, hits.Occurrences, nodeSet, seedFiles, 4)
	hits.Paths = buildRelevantSemanticPaths(index, seedIDs, hits.BuildContexts, hits.Mode, 2)
	return hits
}

func selectRelevantV2BuildContexts(index SemanticIndexV2, query string, mode string, limit int) []BuildContextRecord {
	if len(index.BuildContexts) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  BuildContextRecord
		score int
		key   string
	}
	items := []scored{}
	for _, item := range index.BuildContexts {
		haystacks := []string{
			strings.ToLower(strings.TrimSpace(item.ID)),
			strings.ToLower(strings.TrimSpace(item.Name)),
			strings.ToLower(strings.TrimSpace(item.Kind)),
			strings.ToLower(strings.TrimSpace(item.Directory)),
			strings.ToLower(strings.TrimSpace(item.Project)),
			strings.ToLower(strings.TrimSpace(item.Target)),
			strings.ToLower(strings.TrimSpace(item.Module)),
			strings.ToLower(strings.TrimSpace(item.Compiler)),
			strings.ToLower(strings.Join(item.Files, " ")),
			strings.ToLower(strings.Join(item.Defines, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if mode == "trace" || mode == "impact" {
			score += 2
		}
		if score <= 0 {
			continue
		}
		items = append(items, scored{item: item, score: score, key: item.ID})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].key < items[j].key
		}
		return items[i].score > items[j].score
	})
	out := make([]BuildContextRecord, 0, analysisMinInt(limit, len(items)))
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func expandSemanticV2Neighborhood(index SemanticIndexV2, seedIDs map[string]struct{}, seedFiles map[string]struct{}, depth int) map[string]struct{} {
	nodeSet := map[string]struct{}{}
	frontier := map[string]struct{}{}
	for id := range seedIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		nodeSet[id] = struct{}{}
		frontier[id] = struct{}{}
	}
	for step := 0; step < depth; step++ {
		next := map[string]struct{}{}
		visit := func(sourceID string, targetID string, touchesFile bool) {
			if touchesFile {
				if strings.TrimSpace(sourceID) != "" {
					nodeSet[sourceID] = struct{}{}
				}
				if strings.TrimSpace(targetID) != "" {
					nodeSet[targetID] = struct{}{}
				}
			}
			if _, ok := frontier[sourceID]; ok {
				if strings.TrimSpace(targetID) != "" {
					nodeSet[targetID] = struct{}{}
					next[targetID] = struct{}{}
				}
			}
			if _, ok := frontier[targetID]; ok {
				if strings.TrimSpace(sourceID) != "" {
					nodeSet[sourceID] = struct{}{}
					next[sourceID] = struct{}{}
				}
			}
		}
		for _, edge := range index.CallEdges {
			visit(edge.SourceID, edge.TargetID, edgeTouchesFiles(edge.Evidence, seedFiles))
		}
		for _, edge := range index.BuildOwnershipEdges {
			visit(edge.SourceID, edge.TargetID, edgeTouchesFiles(edge.Evidence, seedFiles))
		}
		for _, edge := range index.InheritanceEdges {
			visit(edge.SourceID, edge.TargetID, edgeTouchesFiles(edge.Evidence, seedFiles))
		}
		for _, edge := range index.OverlayEdges {
			visit(edge.SourceID, edge.TargetID, edgeTouchesFiles(edge.Evidence, seedFiles))
		}
		for _, edge := range index.References {
			touchesFile := edgeTouchesFiles(edge.Evidence, seedFiles)
			if _, ok := seedFiles[edge.SourceFile]; ok {
				touchesFile = true
			}
			if _, ok := seedFiles[edge.TargetPath]; ok {
				touchesFile = true
			}
			visit(edge.SourceID, edge.TargetID, touchesFile)
		}
		for _, edge := range index.GeneratedCodeEdges {
			if _, ok := seedFiles[edge.SourceFile]; ok {
				if strings.TrimSpace(edge.TargetID) != "" {
					nodeSet[edge.TargetID] = struct{}{}
					next[edge.TargetID] = struct{}{}
				}
			}
		}
		frontier = next
		if len(frontier) == 0 {
			break
		}
	}
	return nodeSet
}

func mergeRelevantBuildContexts(index SemanticIndexV2, base []BuildContextRecord, nodeSet map[string]struct{}, fileSet map[string]struct{}, limit int) []BuildContextRecord {
	items := append([]BuildContextRecord(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item.ID] = struct{}{}
	}
	for _, item := range index.BuildContexts {
		if _, ok := seen[item.ID]; ok {
			continue
		}
		if _, ok := nodeSet[item.ID]; ok || buildContextTouchesFiles(item, fileSet) {
			items = append(items, item)
			seen[item.ID] = struct{}{}
		}
	}
	sort.Slice(items, func(i int, j int) bool {
		return items[i].ID < items[j].ID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func mergeRelevantSymbols(index SemanticIndexV2, base []SymbolRecord, nodeSet map[string]struct{}, limit int) []SymbolRecord {
	items := append([]SymbolRecord(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item.ID] = struct{}{}
	}
	for _, item := range index.Symbols {
		if _, ok := seen[item.ID]; ok {
			continue
		}
		if _, ok := nodeSet[item.ID]; ok {
			items = append(items, item)
			seen[item.ID] = struct{}{}
		}
	}
	sort.Slice(items, func(i int, j int) bool {
		return items[i].ID < items[j].ID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func mergeRelevantFiles(index SemanticIndexV2, base []FileRecord, nodeSet map[string]struct{}, fileSet map[string]struct{}, limit int) []FileRecord {
	items := append([]FileRecord(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item.Path] = struct{}{}
	}
	for _, item := range index.Files {
		if _, ok := seen[item.Path]; ok {
			continue
		}
		if _, ok := fileSet[item.Path]; ok {
			items = append(items, item)
			seen[item.Path] = struct{}{}
			continue
		}
		for _, ctxID := range item.BuildContextIDs {
			if _, ok := nodeSet[ctxID]; ok {
				items = append(items, item)
				seen[item.Path] = struct{}{}
				break
			}
		}
	}
	sort.Slice(items, func(i int, j int) bool {
		return items[i].Path < items[j].Path
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func mergeRelevantCallEdges(index SemanticIndexV2, base []CallEdge, nodeSet map[string]struct{}, fileSet map[string]struct{}, limit int) []CallEdge {
	items := append([]CallEdge(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item.SourceID+"|"+item.Type+"|"+item.TargetID] = struct{}{}
	}
	for _, item := range index.CallEdges {
		key := item.SourceID + "|" + item.Type + "|" + item.TargetID
		if _, ok := seen[key]; ok {
			continue
		}
		if semanticV2EdgeTouchesNodeSet(item.SourceID, item.TargetID, fileSet, item.Evidence, nodeSet) {
			items = append(items, item)
			seen[key] = struct{}{}
		}
	}
	sort.Slice(items, func(i int, j int) bool {
		left := items[i].SourceID + "|" + items[i].Type + "|" + items[i].TargetID
		right := items[j].SourceID + "|" + items[j].Type + "|" + items[j].TargetID
		return left < right
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func mergeRelevantInheritanceEdges(index SemanticIndexV2, base []InheritanceEdge, nodeSet map[string]struct{}, fileSet map[string]struct{}, limit int) []InheritanceEdge {
	items := append([]InheritanceEdge(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item.SourceID+"|"+item.TargetID] = struct{}{}
	}
	for _, item := range index.InheritanceEdges {
		key := item.SourceID + "|" + item.TargetID
		if _, ok := seen[key]; ok {
			continue
		}
		if semanticV2EdgeTouchesNodeSet(item.SourceID, item.TargetID, fileSet, item.Evidence, nodeSet) {
			items = append(items, item)
			seen[key] = struct{}{}
		}
	}
	sort.Slice(items, func(i int, j int) bool {
		left := items[i].SourceID + "|" + items[i].TargetID
		right := items[j].SourceID + "|" + items[j].TargetID
		return left < right
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func mergeRelevantBuildEdges(index SemanticIndexV2, base []BuildOwnershipEdge, nodeSet map[string]struct{}, fileSet map[string]struct{}, limit int) []BuildOwnershipEdge {
	items := append([]BuildOwnershipEdge(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item.SourceID+"|"+item.Type+"|"+item.TargetID] = struct{}{}
	}
	for _, item := range index.BuildOwnershipEdges {
		key := item.SourceID + "|" + item.Type + "|" + item.TargetID
		if _, ok := seen[key]; ok {
			continue
		}
		if semanticV2EdgeTouchesNodeSet(item.SourceID, item.TargetID, fileSet, item.Evidence, nodeSet) {
			items = append(items, item)
			seen[key] = struct{}{}
		}
	}
	sort.Slice(items, func(i int, j int) bool {
		left := items[i].SourceID + "|" + items[i].Type + "|" + items[i].TargetID
		right := items[j].SourceID + "|" + items[j].Type + "|" + items[j].TargetID
		return left < right
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func mergeRelevantOverlayEdges(index SemanticIndexV2, base []OverlayEdge, nodeSet map[string]struct{}, fileSet map[string]struct{}, limit int) []OverlayEdge {
	items := append([]OverlayEdge(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item.Domain+"|"+item.SourceID+"|"+item.Type+"|"+item.TargetID] = struct{}{}
	}
	for _, item := range index.OverlayEdges {
		key := item.Domain + "|" + item.SourceID + "|" + item.Type + "|" + item.TargetID
		if _, ok := seen[key]; ok {
			continue
		}
		if semanticV2EdgeTouchesNodeSet(item.SourceID, item.TargetID, fileSet, item.Evidence, nodeSet) {
			items = append(items, item)
			seen[key] = struct{}{}
		}
	}
	sort.Slice(items, func(i int, j int) bool {
		left := items[i].Domain + "|" + items[i].SourceID + "|" + items[i].Type + "|" + items[i].TargetID
		right := items[j].Domain + "|" + items[j].SourceID + "|" + items[j].Type + "|" + items[j].TargetID
		return left < right
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func mergeRelevantReferences(index SemanticIndexV2, base []ReferenceRecord, nodeSet map[string]struct{}, fileSet map[string]struct{}, limit int) []ReferenceRecord {
	items := append([]ReferenceRecord(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item.SourceID+"|"+item.SourceFile+"|"+item.Type+"|"+item.TargetID+"|"+item.TargetPath] = struct{}{}
	}
	for _, item := range index.References {
		key := item.SourceID + "|" + item.SourceFile + "|" + item.Type + "|" + item.TargetID + "|" + item.TargetPath
		if _, ok := seen[key]; ok {
			continue
		}
		touchesFile := edgeTouchesFiles(item.Evidence, fileSet)
		if _, ok := fileSet[item.SourceFile]; ok {
			touchesFile = true
		}
		if _, ok := fileSet[item.TargetPath]; ok {
			touchesFile = true
		}
		_, sourceHit := nodeSet[item.SourceID]
		_, targetHit := nodeSet[item.TargetID]
		if sourceHit || targetHit || touchesFile {
			items = append(items, item)
			seen[key] = struct{}{}
		}
	}
	sort.Slice(items, func(i int, j int) bool {
		left := items[i].SourceID + "|" + items[i].SourceFile + "|" + items[i].Type + "|" + items[i].TargetID + "|" + items[i].TargetPath
		right := items[j].SourceID + "|" + items[j].SourceFile + "|" + items[j].Type + "|" + items[j].TargetID + "|" + items[j].TargetPath
		return left < right
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func mergeRelevantOccurrences(index SemanticIndexV2, base []SymbolOccurrence, nodeSet map[string]struct{}, fileSet map[string]struct{}, limit int) []SymbolOccurrence {
	items := append([]SymbolOccurrence(nil), base...)
	seen := map[string]struct{}{}
	for _, item := range items {
		seen[item.SymbolID+"|"+item.Role+"|"+item.File] = struct{}{}
	}
	for _, item := range index.Occurrences {
		key := item.SymbolID + "|" + item.Role + "|" + item.File
		if _, ok := seen[key]; ok {
			continue
		}
		_, symbolHit := nodeSet[item.SymbolID]
		if symbolHit || containsFileKey(fileSet, item.File) {
			items = append(items, item)
			seen[key] = struct{}{}
		}
	}
	sort.Slice(items, func(i int, j int) bool {
		left := items[i].SymbolID + "|" + items[i].Role + "|" + items[i].File
		right := items[j].SymbolID + "|" + items[j].Role + "|" + items[j].File
		return left < right
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func containsFileKey(fileSet map[string]struct{}, path string) bool {
	_, ok := fileSet[path]
	return ok
}

type semanticPathStep struct {
	Next string
	Type string
}

func buildRelevantSemanticPaths(index SemanticIndexV2, seedIDs map[string]struct{}, contexts []BuildContextRecord, mode string, limit int) []SemanticPathV2 {
	if len(seedIDs) == 0 || limit <= 0 {
		return nil
	}
	adjacency := map[string][]semanticPathStep{}
	addStep := func(sourceID string, targetID string, edgeType string) {
		sourceID = strings.TrimSpace(sourceID)
		targetID = strings.TrimSpace(targetID)
		edgeType = strings.TrimSpace(edgeType)
		if sourceID == "" || targetID == "" || edgeType == "" {
			return
		}
		adjacency[sourceID] = append(adjacency[sourceID], semanticPathStep{Next: targetID, Type: edgeType})
	}
	for _, edge := range index.CallEdges {
		addStep(edge.SourceID, edge.TargetID, edge.Type)
	}
	for _, edge := range index.BuildOwnershipEdges {
		addStep(edge.SourceID, edge.TargetID, edge.Type)
	}
	for _, edge := range index.InheritanceEdges {
		addStep(edge.SourceID, edge.TargetID, "inherits_from")
	}
	for _, edge := range index.OverlayEdges {
		addStep(edge.SourceID, edge.TargetID, edge.Type)
	}
	for _, edge := range index.References {
		if strings.TrimSpace(edge.SourceID) != "" && strings.TrimSpace(edge.TargetID) != "" {
			addStep(edge.SourceID, edge.TargetID, edge.Type)
		}
	}

	startIDs := []string{}
	for _, ctx := range contexts {
		startIDs = append(startIDs, ctx.ID)
	}
	if strings.TrimSpace(index.PrimaryStartup) != "" {
		for _, candidate := range []string{
			"project:" + index.PrimaryStartup,
			"target:" + index.PrimaryStartup,
			"module:" + index.PrimaryStartup,
			"type:" + index.PrimaryStartup,
		} {
			startIDs = append(startIDs, candidate)
		}
	}
	startIDs = analysisUniqueStrings(startIDs)
	targetIDs := []string{}
	for id := range seedIDs {
		targetIDs = append(targetIDs, id)
	}
	sort.Strings(targetIDs)

	paths := []SemanticPathV2{}
	seen := map[string]struct{}{}
	for _, startID := range startIDs {
		for _, targetID := range targetIDs {
			if strings.TrimSpace(startID) == "" || strings.TrimSpace(targetID) == "" || startID == targetID {
				continue
			}
			path, ok := shortestSemanticPath(adjacency, startID, targetID, 4)
			if !ok || len(path.Nodes) < 2 {
				continue
			}
			key := strings.Join(path.Nodes, "->")
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			path.Reason = mode + "_graph_expansion"
			path.Score = len(path.Nodes)
			paths = append(paths, path)
			if len(paths) >= limit {
				return paths
			}
		}
	}
	return paths
}

func shortestSemanticPath(adjacency map[string][]semanticPathStep, startID string, targetID string, maxDepth int) (SemanticPathV2, bool) {
	type state struct {
		Node  string
		Nodes []string
		Edges []string
	}
	queue := []state{{Node: startID, Nodes: []string{startID}, Edges: []string{}}}
	seen := map[string]struct{}{startID: struct{}{}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.Node == targetID {
			return SemanticPathV2{Nodes: current.Nodes, Edges: current.Edges}, true
		}
		if len(current.Edges) >= maxDepth {
			continue
		}
		for _, step := range adjacency[current.Node] {
			if _, ok := seen[step.Next]; ok {
				continue
			}
			seen[step.Next] = struct{}{}
			nextNodes := append(append([]string(nil), current.Nodes...), step.Next)
			nextEdges := append(append([]string(nil), current.Edges...), step.Type)
			queue = append(queue, state{Node: step.Next, Nodes: nextNodes, Edges: nextEdges})
		}
	}
	return SemanticPathV2{}, false
}
