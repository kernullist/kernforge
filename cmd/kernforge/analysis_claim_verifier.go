package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func verifyAnalysisClaims(snapshot ProjectSnapshot, run ProjectAnalysisRun) ClaimVerificationReport {
	report := ClaimVerificationReport{
		GeneratedAt:   time.Now(),
		Status:        "passed",
		Results:       []ClaimVerificationResult{},
		FollowThrough: []string{},
	}
	packetByID := map[string]EvidencePacket{}
	duplicatePacketIDs := map[string]struct{}{}
	for _, packet := range run.EvidencePackets {
		key := strings.ToLower(strings.TrimSpace(packet.ID))
		if key == "" {
			continue
		}
		if _, ok := packetByID[key]; ok {
			duplicatePacketIDs[key] = struct{}{}
			continue
		}
		packetByID[key] = packet
	}
	shardByID := map[string]AnalysisShard{}
	ambiguousShardIDs := map[string]struct{}{}
	for _, shard := range run.Shards {
		key := strings.TrimSpace(shard.ID)
		if key == "" {
			continue
		}
		if _, ok := shardByID[key]; ok {
			ambiguousShardIDs[key] = struct{}{}
			continue
		}
		shardByID[key] = shard
	}
	for packetID := range duplicatePacketIDs {
		report.RunIssues = append(report.RunIssues, ClaimVerificationIssue{
			Code:     "duplicate_packet_id",
			Severity: "blocking",
			Message:  "Evidence packet id appears more than once; verifier kept the first packet and refused to silently overwrite it.",
			Evidence: []string{packetID},
		})
	}
	for shardID := range ambiguousShardIDs {
		report.RunIssues = append(report.RunIssues, ClaimVerificationIssue{
			Code:     "duplicate_shard_id",
			Severity: "blocking",
			Message:  "Shard id appears more than once; verifier kept the first shard scope and marks claims from this id as ambiguous.",
			Evidence: []string{shardID},
		})
	}
	reportShardCounts := map[string]int{}
	for _, worker := range run.Reports {
		key := strings.TrimSpace(worker.ShardID)
		if key == "" {
			continue
		}
		reportShardCounts[key]++
	}
	for shardID, count := range reportShardCounts {
		if count <= 1 {
			continue
		}
		report.RunIssues = append(report.RunIssues, ClaimVerificationIssue{
			Code:     "duplicate_report_shard_id",
			Severity: "blocking",
			Message:  "More than one worker report targets the same shard id; verifier processes both reports but records the identity collision before persistence.",
			Evidence: []string{shardID, fmt.Sprintf("reports=%d", count)},
		})
	}
	for _, worker := range run.Reports {
		if workerReportExcludesClaims(worker) {
			continue
		}
		shard := shardByID[worker.ShardID]
		_, ambiguousScope := ambiguousShardIDs[worker.ShardID]
		for _, claim := range worker.Claims {
			result := verifySingleAnalysisClaim(snapshot, run, shard, worker, claim, packetByID, ambiguousScope)
			report.TotalClaims++
			report.Results = append(report.Results, result)
			switch result.Status {
			case "verified":
				report.VerifiedCount++
				report.VerifiedClaims = append(report.VerifiedClaims, VerifiedClaim{
					ShardID:           result.ShardID,
					ClaimID:           result.ClaimID,
					Kind:              result.Kind,
					Claim:             result.Claim,
					EvidencePacketIDs: result.SupportingPacketIDs,
					SourceAnchors:     result.SourceAnchors,
					GraphEdgeIDs:      result.GraphEdgeIDs,
				})
			case "inference":
				report.InferenceCount++
			case "downgraded":
				report.DowngradedCount++
				report.UnsupportedClaims = append(report.UnsupportedClaims, unsupportedClaimFromVerificationResult(result))
				if strings.EqualFold(result.OriginalConfidence, "high") {
					report.UnsupportedHighConfidenceCount++
				}
			case "unsupported":
				report.UnsupportedCount++
				report.UnsupportedClaims = append(report.UnsupportedClaims, unsupportedClaimFromVerificationResult(result))
				if strings.EqualFold(result.OriginalConfidence, "high") {
					report.UnsupportedHighConfidenceCount++
				}
			case "blocking":
				report.BlockingCount++
				report.UnsupportedClaims = append(report.UnsupportedClaims, unsupportedClaimFromVerificationResult(result))
				if strings.EqualFold(result.OriginalConfidence, "high") {
					report.UnsupportedHighConfidenceCount++
				}
			}
		}
	}
	for _, issue := range report.RunIssues {
		if strings.EqualFold(issue.Severity, "blocking") {
			report.BlockingCount++
		}
	}
	if report.BlockingCount > 0 {
		report.Status = "blocking"
	} else if report.UnsupportedCount+report.DowngradedCount+report.InferenceCount > 0 {
		report.Status = "warnings"
	}
	report.FollowThrough = claimVerificationFollowThrough(report, run.SecurityOverlay)
	return report
}

func verifySingleAnalysisClaim(snapshot ProjectSnapshot, run ProjectAnalysisRun, shard AnalysisShard, worker WorkerReport, claim AnalysisClaim, packetByID map[string]EvidencePacket, ambiguousScope bool) ClaimVerificationResult {
	originalConfidence := normalizeAnalysisClaimConfidence(claim.Confidence)
	result := ClaimVerificationResult{
		ShardID:            firstNonBlankAnalysisString(worker.ShardID, shard.ID),
		ClaimID:            firstNonBlankAnalysisString(claim.ID, analysisGraphStableID("claim", worker.ShardID, claim.Claim)),
		Kind:               normalizeAnalysisClaimKind(claim.Kind),
		Claim:              strings.TrimSpace(claim.Claim),
		OriginalConfidence: originalConfidence,
		FinalConfidence:    originalConfidence,
		Status:             "verified",
		EvidencePacketIDs:  analysisUniqueStrings(claim.EvidencePacketIDs),
		SourceAnchors:      analysisUniqueStrings(claim.SourceAnchors),
	}
	validPackets := []EvidencePacket{}
	if ambiguousScope {
		result.Issues = append(result.Issues, ClaimVerificationIssue{
			Code:     "duplicate_shard_id",
			Severity: "blocking",
			Message:  "Claim belongs to a duplicate shard id, so packet/source scope checks are ambiguous and the claim cannot be promoted.",
			Evidence: []string{result.ShardID},
		})
	}
	for _, packetID := range claim.EvidencePacketIDs {
		packetID = strings.TrimSpace(packetID)
		if packetID == "" {
			continue
		}
		packet, ok := packetByID[strings.ToLower(packetID)]
		if !ok {
			result.Issues = append(result.Issues, ClaimVerificationIssue{
				Code:     "packet_id_not_found",
				Severity: severityForConfidence(originalConfidence, "error"),
				Message:  "Claim references an evidence packet id that does not exist in this run.",
				Evidence: []string{packetID},
			})
			continue
		}
		if strings.TrimSpace(packet.ShardID) != "" && strings.TrimSpace(result.ShardID) != "" && packet.ShardID != result.ShardID {
			result.Issues = append(result.Issues, ClaimVerificationIssue{
				Code:     "packet_shard_scope_mismatch",
				Severity: "blocking",
				Message:  "Claim cites an evidence packet from another shard.",
				Evidence: []string{packet.ID, packet.ShardID, result.ShardID},
			})
		}
		if !ambiguousScope && !packetPathInShardScope(packet.Path, shard) {
			result.Issues = append(result.Issues, ClaimVerificationIssue{
				Code:     "packet_source_scope_mismatch",
				Severity: "blocking",
				Message:  "Claim cites a packet outside the assigned primary/reference scope.",
				Evidence: []string{packet.ID, packet.Path},
			})
		}
		validPackets = append(validPackets, packet)
		result.SupportingPacketIDs = append(result.SupportingPacketIDs, packet.ID)
		result.GraphEdgeIDs = append(result.GraphEdgeIDs, packet.GraphEdgeIDs...)
	}
	if len(result.EvidencePacketIDs) == 0 {
		result.Issues = append(result.Issues, ClaimVerificationIssue{
			Code:     "missing_packet_id",
			Severity: severityForConfidence(originalConfidence, "warning"),
			Message:  "Claim has no evidence_packet_id.",
			Evidence: []string{claim.Claim},
		})
	}
	if len(validPackets) == 0 && len(result.EvidencePacketIDs) > 0 {
		result.Issues = append(result.Issues, ClaimVerificationIssue{
			Code:     "no_valid_packet",
			Severity: severityForConfidence(originalConfidence, "error"),
			Message:  "Claim references packet ids, but none are valid for this shard.",
		})
	}
	result.Issues = append(result.Issues, verifyClaimSourceAnchors(claim, validPackets, shard, run.SemanticIndexV2, ambiguousScope)...)
	result.Issues = append(result.Issues, verifyClaimSymbolAnchors(claim, validPackets, run.SemanticIndexV2)...)
	graphEdgeIDs, graphIssues := verifyClaimGraphEdges(claim, validPackets, run.EvidenceGraph)
	result.GraphEdgeIDs = analysisUniqueStrings(append(result.GraphEdgeIDs, graphEdgeIDs...))
	result.Issues = append(result.Issues, graphIssues...)
	result.Issues = append(result.Issues, verifyClaimFactPackConflicts(snapshot, claim)...)
	result.Issues = append(result.Issues, verifyClaimSecurityOverlay(claim, validPackets, run.SecurityOverlay)...)
	result.GraphEdgeIDs = analysisUniqueStrings(result.GraphEdgeIDs)
	result.SupportingPacketIDs = analysisUniqueStrings(result.SupportingPacketIDs)
	result.Status, result.FinalConfidence = claimVerificationStatus(originalConfidence, result.Issues)
	return result
}

func verifyClaimSourceAnchors(claim AnalysisClaim, packets []EvidencePacket, shard AnalysisShard, index SemanticIndexV2, skipScope bool) []ClaimVerificationIssue {
	_ = index
	issues := []ClaimVerificationIssue{}
	if len(claim.SourceAnchors) == 0 {
		return append(issues, ClaimVerificationIssue{
			Code:     "missing_source_anchor",
			Severity: severityForConfidence(claim.Confidence, "warning"),
			Message:  "Claim has no source anchor.",
		})
	}
	for _, anchor := range claim.SourceAnchors {
		path, line, ok := parseAnalysisClaimSourceAnchor(anchor)
		if !ok || path == "" {
			continue
		}
		if !skipScope && !packetPathInShardScope(path, shard) {
			issues = append(issues, ClaimVerificationIssue{
				Code:     "source_scope_mismatch",
				Severity: "blocking",
				Message:  "Claim source anchor is outside the assigned primary/reference scope.",
				Evidence: []string{anchor},
			})
			continue
		}
		if len(packets) == 0 {
			continue
		}
		pathMatched := false
		lineMatched := line <= 0
		for _, packet := range packets {
			if packet.Path != path {
				continue
			}
			pathMatched = true
			if line <= 0 || packet.StartLine <= 0 || (line >= packet.StartLine && line <= packet.EndLine) {
				lineMatched = true
				break
			}
		}
		if !pathMatched {
			issues = append(issues, ClaimVerificationIssue{
				Code:     "source_packet_mismatch",
				Severity: "warning",
				Message:  "Claim source anchor path does not match any cited packet path; treat the claim as a citation precision issue until the correct packet is attached.",
				Evidence: []string{anchor},
			})
		} else if !lineMatched {
			issues = append(issues, ClaimVerificationIssue{
				Code:     "line_range_mismatch",
				Severity: "blocking",
				Message:  "Claim source anchor line is outside the cited packet line range.",
				Evidence: []string{anchor},
			})
		}
	}
	return issues
}

func verifyClaimSymbolAnchors(claim AnalysisClaim, packets []EvidencePacket, index SemanticIndexV2) []ClaimVerificationIssue {
	citedSymbols := claimCitedSymbols(claim)
	if len(citedSymbols) == 0 {
		return nil
	}
	packetSymbols := map[string]struct{}{}
	packetPaths := map[string]struct{}{}
	for _, packet := range packets {
		if strings.TrimSpace(packet.SymbolName) != "" {
			packetSymbols[strings.ToLower(packet.SymbolName)] = struct{}{}
		}
		if strings.TrimSpace(packet.SymbolID) != "" {
			packetSymbols[strings.ToLower(packet.SymbolID)] = struct{}{}
		}
		packetPaths[packet.Path] = struct{}{}
	}
	for _, symbol := range index.Symbols {
		if _, ok := packetPaths[symbol.File]; !ok {
			continue
		}
		packetSymbols[strings.ToLower(symbol.Name)] = struct{}{}
		packetSymbols[strings.ToLower(symbol.CanonicalName)] = struct{}{}
		packetSymbols[strings.ToLower(symbol.ID)] = struct{}{}
	}
	issues := []ClaimVerificationIssue{}
	for _, cited := range citedSymbols {
		if _, ok := packetSymbols[strings.ToLower(cited)]; !ok {
			issues = append(issues, ClaimVerificationIssue{
				Code:     "symbol_mismatch",
				Severity: "blocking",
				Message:  "Claim cites a symbol that is not present in the cited packets or structural index scope.",
				Evidence: []string{cited},
			})
		}
	}
	return issues
}

func verifyClaimGraphEdges(claim AnalysisClaim, packets []EvidencePacket, graph AnalysisEvidenceGraph) ([]string, []ClaimVerificationIssue) {
	edgeIDs := []string{}
	packetIDs := map[string]struct{}{}
	packetPaths := map[string]struct{}{}
	packetSymbols := map[string]struct{}{}
	for _, packet := range packets {
		packetIDs[packet.ID] = struct{}{}
		packetPaths[packet.Path] = struct{}{}
		if strings.TrimSpace(packet.SymbolID) != "" {
			packetSymbols[analysisGraphSymbolNodeID(packet.SymbolID)] = struct{}{}
		}
		for _, edgeID := range packet.GraphEdgeIDs {
			edgeIDs = append(edgeIDs, edgeID)
		}
	}
	for _, edge := range graph.Edges {
		if graphEdgeTouchesFiles(edge.Evidence, packetPaths) {
			edgeIDs = append(edgeIDs, edge.ID)
			continue
		}
		if _, ok := packetSymbols[edge.SourceID]; ok {
			edgeIDs = append(edgeIDs, edge.ID)
			continue
		}
		if _, ok := packetSymbols[edge.TargetID]; ok {
			edgeIDs = append(edgeIDs, edge.ID)
			continue
		}
		for _, packetID := range edge.PacketIDs {
			if _, ok := packetIDs[packetID]; ok {
				edgeIDs = append(edgeIDs, edge.ID)
			}
		}
	}
	edgeIDs = analysisUniqueStrings(edgeIDs)
	if claimNeedsGraphEdge(claim) && len(edgeIDs) == 0 {
		return nil, []ClaimVerificationIssue{{
			Code:     "graph_edge_unverified",
			Severity: "warning",
			Message:  "Claim describes a flow, boundary, or dependency but no deterministic graph edge supports the cited packets.",
			Evidence: []string{claim.Claim},
		}}
	}
	return edgeIDs, nil
}

func verifyClaimFactPackConflicts(snapshot ProjectSnapshot, claim AnalysisClaim) []ClaimVerificationIssue {
	text := strings.ToLower(claim.Claim)
	issues := []ClaimVerificationIssue{}
	if containsAny(text, "no skipped files", "full source coverage", "all files scanned") && snapshot.CoverageLedger.SkippedFileCount > 0 {
		issues = append(issues, ClaimVerificationIssue{
			Code:     "coverage_fact_conflict",
			Severity: "blocking",
			Message:  "Claim conflicts with deterministic coverage ledger skipped-file data.",
			Evidence: []string{fmt.Sprintf("skipped_files=%d", snapshot.CoverageLedger.SkippedFileCount)},
		})
	}
	if containsAny(text, "not unreal", "no unreal") && len(snapshot.UnrealProjects)+len(snapshot.UnrealModules)+len(snapshot.UnrealTypes) > 0 {
		issues = append(issues, ClaimVerificationIssue{
			Code:     "architecture_fact_conflict",
			Severity: "blocking",
			Message:  "Claim conflicts with deterministic Unreal project facts.",
		})
	}
	if claimAssertsMmGetSystemRoutineAddressOnly(text) && snapshotContainsExportTableParser(snapshot) {
		issues = append(issues, ClaimVerificationIssue{
			Code:     "windows_driver_fact_conflict",
			Severity: "blocking",
			Message:  "Claim says Windows kernel exports are resolved only through MmGetSystemRoutineAddress, but scanned source contains deterministic export-table parsing evidence.",
			Evidence: []string{"GetExportFunctionAddress", "IMAGE_EXPORT_DIRECTORY"},
		})
	}
	return issues
}

func claimAssertsMmGetSystemRoutineAddressOnly(text string) bool {
	if !strings.Contains(text, "mmgetsystemroutineaddress") {
		return false
	}
	if containsAny(text, "not mmgetsystemroutineaddress", "does not use mmgetsystemroutineaddress", "without mmgetsystemroutineaddress") {
		return false
	}
	if containsAny(text, "export table", "getexportfunctionaddress", "image_export_directory") {
		return false
	}
	return containsAny(text,
		"via mmgetsystemroutineaddress",
		"using mmgetsystemroutineaddress",
		"uses mmgetsystemroutineaddress",
		"through mmgetsystemroutineaddress",
		"only mmgetsystemroutineaddress",
		"purely mmgetsystemroutineaddress",
		"resolved by mmgetsystemroutineaddress",
	)
}

func snapshotContainsExportTableParser(snapshot ProjectSnapshot) bool {
	for _, file := range snapshot.Files {
		lowerPath := strings.ToLower(filepath.ToSlash(file.Path))
		if !strings.HasSuffix(lowerPath, ".c") &&
			!strings.HasSuffix(lowerPath, ".cc") &&
			!strings.HasSuffix(lowerPath, ".cpp") &&
			!strings.HasSuffix(lowerPath, ".cxx") &&
			!strings.HasSuffix(lowerPath, ".h") &&
			!strings.HasSuffix(lowerPath, ".hpp") {
			continue
		}
		sourcePath := filepath.FromSlash(file.Path)
		if !filepath.IsAbs(sourcePath) {
			sourcePath = filepath.Join(snapshot.Root, sourcePath)
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			continue
		}
		corpus := strings.ToLower(string(data))
		if strings.Contains(corpus, "getexportfunctionaddress") &&
			containsAny(corpus, "image_export_directory", "addressoffunctions", "addressofnames", "export directory") {
			return true
		}
	}
	return false
}

func verifyClaimSecurityOverlay(claim AnalysisClaim, packets []EvidencePacket, overlay SecurityOverlaySummary) []ClaimVerificationIssue {
	if !claimIsSecurityRelevant(claim) {
		return nil
	}
	issues := []ClaimVerificationIssue{}
	if contradiction, evidence := securityOverlayClaimContradictsBoundary(overlay, claim, packets); contradiction {
		issues = append(issues, ClaimVerificationIssue{
			Code:     "security_boundary_invariant_violation",
			Severity: "blocking",
			Message:  "Claim says the boundary is validated/safe, but the deterministic overlay records a missing validation candidate.",
			Evidence: evidence,
		})
	}
	if len(overlay.Edges) > 0 && len(packets) > 0 && !securityOverlayTouchesClaim(overlay, claim, packets) {
		issues = append(issues, ClaimVerificationIssue{
			Code:     "security_overlay_unlinked",
			Severity: "warning",
			Message:  "Security-relevant claim is not linked to a deterministic security overlay edge.",
			Evidence: []string{claim.Claim},
		})
	}
	return issues
}

func applyClaimVerificationToReports(run *ProjectAnalysisRun, report ClaimVerificationReport) {
	if run == nil {
		return
	}
	resultByKey := map[string]ClaimVerificationResult{}
	for _, result := range report.Results {
		resultByKey[claimVerificationResultKey(result.ShardID, result.ClaimID, result.Claim)] = result
	}
	for i := range run.Reports {
		unknowns := append([]string(nil), run.Reports[i].Unknowns...)
		facts := append([]string(nil), run.Reports[i].Facts...)
		for j := range run.Reports[i].Claims {
			claim := &run.Reports[i].Claims[j]
			key := claimVerificationResultKey(run.Reports[i].ShardID, claim.ID, claim.Claim)
			result, ok := resultByKey[key]
			if !ok {
				continue
			}
			claim.Confidence = result.FinalConfidence
			claim.EvidencePacketIDs = analysisUniqueStrings(result.SupportingPacketIDs)
			if result.Status == "verified" {
				continue
			}
			if result.Status == "inference" {
				if strings.EqualFold(claim.Confidence, "high") {
					claim.Confidence = "medium"
				}
				continue
			}
			unknowns = append(unknowns, fmt.Sprintf("Claim %s withheld from verified facts by deterministic verifier: %s.", firstNonBlankAnalysisString(claim.ID, claim.Claim), result.Status))
			facts = removeExactAnalysisFact(facts, claim.Claim)
			if strings.TrimSpace(claim.VerificationHint) != "" {
				claim.VerificationHint = strings.TrimSpace(claim.VerificationHint + " Deterministic verifier status: " + result.Status + ".")
			} else {
				claim.VerificationHint = "Deterministic verifier status: " + result.Status + "."
			}
		}
		run.Reports[i].Facts = analysisUniqueStrings(facts)
		run.Reports[i].Unknowns = analysisUniqueStrings(unknowns)
	}
	run.ClaimVerification = report
	run.UnsupportedClaims = append([]UnsupportedClaim(nil), report.UnsupportedClaims...)
}

func appendClaimVerificationFinalSections(document string, report ClaimVerificationReport, overlay SecurityOverlaySummary) string {
	var b strings.Builder
	document = strings.TrimSpace(document)
	if document != "" {
		b.WriteString(document)
		b.WriteString("\n\n")
	}
	b.WriteString("## Deterministic Claim Verification\n\n")
	fmt.Fprintf(&b, "- Status: %s\n", firstNonBlankAnalysisString(report.Status, "unknown"))
	fmt.Fprintf(&b, "- Total claims: %d\n", report.TotalClaims)
	fmt.Fprintf(&b, "- Verified: %d\n", report.VerifiedCount)
	fmt.Fprintf(&b, "- Inferences: %d\n", report.InferenceCount)
	fmt.Fprintf(&b, "- Downgraded: %d\n", report.DowngradedCount)
	fmt.Fprintf(&b, "- Unsupported: %d\n", report.UnsupportedCount)
	fmt.Fprintf(&b, "- Blocking: %d\n", report.BlockingCount)
	fmt.Fprintf(&b, "- Unsupported high-confidence: %d\n", report.UnsupportedHighConfidenceCount)
	b.WriteString("\n")
	b.WriteString("## Verified Facts\n\n")
	if len(report.RunIssues) > 0 {
		b.WriteString("Run-level verifier issues:\n")
		for _, issue := range limitClaimVerificationIssues(report.RunIssues, 8) {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", issue.Severity, issue.Code, issue.Message)
		}
		b.WriteString("\n")
	}
	if len(report.VerifiedClaims) == 0 {
		b.WriteString("- No high-confidence verified claim objects were produced by the deterministic verifier.\n")
	} else {
		for _, claim := range limitVerifiedClaims(report.VerifiedClaims, 16) {
			fmt.Fprintf(&b, "- %s", claim.Claim)
			if len(claim.EvidencePacketIDs) > 0 {
				fmt.Fprintf(&b, " (packets: %s)", strings.Join(limitStrings(claim.EvidencePacketIDs, 4), ", "))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n## Inferences\n\n")
	inferences := claimVerificationResultsByStatus(report.Results, "inference")
	if len(inferences) == 0 {
		b.WriteString("- No verifier-demoted inference claims were recorded.\n")
	} else {
		for _, result := range limitClaimVerificationResults(inferences, 16) {
			fmt.Fprintf(&b, "- %s (confidence: %s)\n", result.Claim, result.FinalConfidence)
		}
	}
	b.WriteString("\n## Unsupported Or Downgraded Claims\n\n")
	if len(report.UnsupportedClaims) == 0 {
		b.WriteString("- No unsupported or downgraded claims were recorded.\n")
	} else {
		for _, claim := range limitUnsupportedClaims(report.UnsupportedClaims, 16) {
			fmt.Fprintf(&b, "- [%s] %s", claim.Status, claim.Claim)
			if strings.TrimSpace(claim.Reason) != "" {
				fmt.Fprintf(&b, " -- %s", claim.Reason)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n## Security / Anti-Cheat Overlay\n\n")
	fmt.Fprintf(&b, "- Overlay nodes: %d\n", overlay.Metrics.NodeCount)
	fmt.Fprintf(&b, "- Overlay edges: %d\n", overlay.Metrics.EdgeCount)
	fmt.Fprintf(&b, "- Missing validation candidates: %d\n", overlay.Metrics.MissingValidationCandidates)
	if len(overlay.Metrics.Surfaces) > 0 {
		fmt.Fprintf(&b, "- Surfaces: %s\n", strings.Join(overlay.Metrics.Surfaces, ", "))
	}
	for _, edge := range limitSecurityOverlayEdges(overlay.Edges, 12) {
		fmt.Fprintf(&b, "- `%s` -> `%s` [%s/%s]\n", edge.SourceID, edge.TargetID, edge.Type, firstNonBlankAnalysisString(edge.Surface, "surface"))
	}
	b.WriteString("\n## Verification Follow-Through\n\n")
	if len(report.FollowThrough) == 0 && len(overlay.FollowUp) == 0 {
		b.WriteString("- Run `/verify`, `/docs-refresh`, `/simulate stealth-surface`, and `/fuzz-campaign run` for operational follow-through.\n")
	} else {
		for _, item := range analysisUniqueStrings(append(report.FollowThrough, overlay.FollowUp...)) {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	return strings.TrimSpace(b.String())
}

func claimVerificationStatus(originalConfidence string, issues []ClaimVerificationIssue) (string, string) {
	hasBlocking := false
	hasError := false
	hasWarning := false
	for _, issue := range issues {
		switch strings.ToLower(strings.TrimSpace(issue.Severity)) {
		case "blocking":
			hasBlocking = true
		case "error":
			hasError = true
		case "warning":
			hasWarning = true
		}
	}
	if hasBlocking {
		return "blocking", "low"
	}
	if hasError {
		if strings.EqualFold(originalConfidence, "high") {
			return "unsupported", "low"
		}
		return "unsupported", "low"
	}
	if hasWarning {
		if strings.EqualFold(originalConfidence, "high") {
			return "downgraded", "medium"
		}
		return "inference", firstNonBlankAnalysisString(originalConfidence, "medium")
	}
	return "verified", firstNonBlankAnalysisString(originalConfidence, "medium")
}

func severityForConfidence(confidence string, defaultSeverity string) string {
	if strings.EqualFold(normalizeAnalysisClaimConfidence(confidence), "high") {
		return "blocking"
	}
	return defaultSeverity
}

func parseAnalysisClaimSourceAnchor(anchor string) (string, int, bool) {
	clean := cleanEvidencePath(anchor)
	if clean == "" {
		return "", 0, false
	}
	if hash := strings.Index(clean, "#"); hash > 0 {
		clean = strings.TrimSpace(clean[:hash])
	}
	line := 0
	if index := strings.LastIndex(clean, ":"); index > 1 {
		suffix := strings.TrimSpace(clean[index+1:])
		if evidenceLineSuffixLooksNumeric(suffix) {
			first := suffix
			if dash := strings.Index(first, "-"); dash >= 0 {
				first = first[:dash]
			}
			if parsed, err := strconv.Atoi(first); err == nil {
				line = parsed
				clean = clean[:index]
			}
		}
	}
	return filepathSlashOrEmpty(clean), line, true
}

func packetPathInShardScope(path string, shard AnalysisShard) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if len(shard.PrimaryFiles) == 0 && len(shard.ReferenceFiles) == 0 {
		return true
	}
	allowed := graphFileSet(append(append([]string(nil), shard.PrimaryFiles...), shard.ReferenceFiles...))
	_, ok := allowed[filepathSlashOrEmpty(path)]
	return ok
}

func claimCitedSymbols(claim AnalysisClaim) []string {
	out := []string{}
	for _, anchor := range claim.SourceAnchors {
		anchor = strings.TrimSpace(anchor)
		lower := strings.ToLower(anchor)
		if index := strings.Index(lower, "symbol="); index >= 0 {
			value := strings.Trim(anchor[index+len("symbol="):], "`'\" )]")
			if value != "" {
				out = append(out, value)
			}
		}
		if hash := strings.LastIndex(anchor, "#"); hash > 0 && hash < len(anchor)-1 {
			value := strings.Trim(anchor[hash+1:], "`'\" )]")
			if value != "" && !strings.Contains(value, "/") {
				out = append(out, value)
			}
		}
	}
	return analysisUniqueStrings(out)
}

func claimNeedsGraphEdge(claim AnalysisClaim) bool {
	text := strings.ToLower(strings.Join([]string{claim.Kind, claim.Claim, claim.VerificationHint}, " "))
	return containsAny(text, "->", "calls", "routes", "dispatch", "reaches", "flows", "depends", "boundary", "ioctl", "rpc", "callback", "handle", "memory", "asset", "config", "generated")
}

func claimIsSecurityRelevant(claim AnalysisClaim) bool {
	text := strings.ToLower(strings.Join([]string{claim.Kind, claim.Claim, claim.VerificationHint}, " "))
	return containsAny(text, "security", "anti-cheat", "anticheat", "ioctl", "rpc", "callback", "handle", "memory", "kernel", "driver", "tamper", "integrity", "telemetry", "authority", "replication", "asset", "config")
}

func unsupportedClaimFromVerificationResult(result ClaimVerificationResult) UnsupportedClaim {
	reasons := []string{}
	for _, issue := range result.Issues {
		reasons = append(reasons, issue.Code)
	}
	return UnsupportedClaim{
		ShardID:            result.ShardID,
		ClaimID:            result.ClaimID,
		Kind:               result.Kind,
		Claim:              result.Claim,
		OriginalConfidence: result.OriginalConfidence,
		FinalConfidence:    result.FinalConfidence,
		Status:             result.Status,
		Reason:             strings.Join(analysisUniqueStrings(reasons), ", "),
		EvidencePacketIDs:  result.EvidencePacketIDs,
		SourceAnchors:      result.SourceAnchors,
		Issues:             append([]ClaimVerificationIssue(nil), result.Issues...),
	}
}

func claimVerificationFollowThrough(report ClaimVerificationReport, overlay SecurityOverlaySummary) []string {
	items := []string{"/analyze-dashboard latest", "/docs-refresh", "/verify"}
	if report.BlockingCount > 0 || report.UnsupportedHighConfidenceCount > 0 {
		items = append(items, "Re-run analyze-project or route a focused evidence shard before treating unsupported high-confidence claims as facts.")
	}
	if overlay.Metrics.MissingValidationCandidates > 0 {
		items = append(items, "/simulate stealth-surface", "/fuzz-campaign run")
	}
	return analysisUniqueStrings(items)
}

func claimVerificationResultKey(shardID string, claimID string, claimText string) string {
	return strings.ToLower(strings.TrimSpace(shardID) + "|" + strings.TrimSpace(claimID) + "|" + strings.TrimSpace(claimText))
}

func removeExactAnalysisFact(facts []string, value string) []string {
	out := []string{}
	value = strings.TrimSpace(value)
	for _, fact := range facts {
		if strings.EqualFold(strings.TrimSpace(fact), value) {
			continue
		}
		out = append(out, fact)
	}
	return out
}

func claimVerificationResultsByStatus(results []ClaimVerificationResult, status string) []ClaimVerificationResult {
	out := []ClaimVerificationResult{}
	for _, result := range results {
		if strings.EqualFold(result.Status, status) {
			out = append(out, result)
		}
	}
	return out
}

func limitClaimVerificationResults(items []ClaimVerificationResult, limit int) []ClaimVerificationResult {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitClaimVerificationIssues(items []ClaimVerificationIssue, limit int) []ClaimVerificationIssue {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitVerifiedClaims(items []VerifiedClaim, limit int) []VerifiedClaim {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitUnsupportedClaims(items []UnsupportedClaim, limit int) []UnsupportedClaim {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func limitSecurityOverlayEdges(items []SecurityOverlayEdge, limit int) []SecurityOverlayEdge {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func sortedClaimVerificationIssues(issues []ClaimVerificationIssue) []ClaimVerificationIssue {
	out := append([]ClaimVerificationIssue(nil), issues...)
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Severity == out[j].Severity {
			return out[i].Code < out[j].Code
		}
		return out[i].Severity < out[j].Severity
	})
	return out
}
