package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	analysisEvidencePacketDefaultLimit  = 12
	analysisEvidencePacketMaxLines      = 140
	analysisEvidencePacketFallbackLines = 80
)

func buildAnalysisRunEvidencePackets(snapshot ProjectSnapshot, shards []AnalysisShard) []EvidencePacket {
	out := []EvidencePacket{}
	seen := map[string]struct{}{}
	for _, shard := range shards {
		for _, packet := range buildEvidencePacketsForShard(snapshot, shard, analysisEvidencePacketDefaultLimit) {
			key := packet.ID
			if key == "" {
				key = packet.ShardID + "|" + packet.Path + "|" + packet.SymbolID + "|" + fmt.Sprintf("%d", packet.StartLine)
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, packet)
		}
	}
	return out
}

func buildEvidencePacketsForShard(snapshot ProjectSnapshot, shard AnalysisShard, limit int) []EvidencePacket {
	if limit <= 0 {
		limit = analysisEvidencePacketDefaultLimit
	}
	paths := analysisEvidencePacketPaths(shard)
	if len(paths) == 0 {
		return nil
	}
	primary := map[string]struct{}{}
	for _, path := range shard.PrimaryFiles {
		primary[path] = struct{}{}
	}
	anchors := collectSourceFunctionAnchorsForPaths(snapshot, paths)
	sort.SliceStable(anchors, func(i int, j int) bool {
		left := evidenceAnchorScore(anchors[i], shard, primary)
		right := evidenceAnchorScore(anchors[j], shard, primary)
		if left == right {
			if anchors[i].Symbol.File == anchors[j].Symbol.File {
				if anchors[i].Symbol.StartLine == anchors[j].Symbol.StartLine {
					return anchors[i].Symbol.CanonicalName < anchors[j].Symbol.CanonicalName
				}
				return anchors[i].Symbol.StartLine < anchors[j].Symbol.StartLine
			}
			_, leftPrimary := primary[anchors[i].Symbol.File]
			_, rightPrimary := primary[anchors[j].Symbol.File]
			if leftPrimary != rightPrimary {
				return leftPrimary
			}
			return anchors[i].Symbol.File < anchors[j].Symbol.File
		}
		return left > right
	})

	packets := []EvidencePacket{}
	coveredPaths := map[string]struct{}{}
	for _, anchor := range anchors {
		if len(packets) >= limit {
			break
		}
		packet, ok := evidencePacketFromAnchor(snapshot, shard, anchor)
		if !ok {
			continue
		}
		packets = append(packets, packet)
		coveredPaths[packet.Path] = struct{}{}
	}
	for _, path := range paths {
		if len(packets) >= limit {
			break
		}
		if _, ok := coveredPaths[path]; ok {
			continue
		}
		packet, ok := fallbackEvidencePacketForFile(snapshot, shard, path)
		if !ok {
			continue
		}
		packets = append(packets, packet)
		coveredPaths[packet.Path] = struct{}{}
	}
	assignEvidencePacketIDs(shard, packets)
	return packets
}

func renderEvidencePacketsForPrompt(snapshot ProjectSnapshot, shard AnalysisShard, limit int) string {
	packets := buildEvidencePacketsForShard(snapshot, shard, limit)
	if len(packets) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Evidence packets:\n")
	b.WriteString("Use these packet IDs for claims.evidence_packet_ids. Treat packet metadata as the citation contract.\n\n")
	for _, packet := range packets {
		fmt.Fprintf(&b, "PACKET %s\n", packet.ID)
		fmt.Fprintf(&b, "- kind: %s\n", packet.Kind)
		fmt.Fprintf(&b, "- path: %s\n", packet.Path)
		if strings.TrimSpace(packet.SymbolName) != "" {
			fmt.Fprintf(&b, "- symbol: %s\n", packet.SymbolName)
		}
		if packet.StartLine > 0 {
			fmt.Fprintf(&b, "- lines: %d-%d\n", packet.StartLine, packet.EndLine)
		}
		if len(packet.Tags) > 0 {
			fmt.Fprintf(&b, "- tags: %s\n", strings.Join(packet.Tags, ", "))
		}
		fmt.Fprintf(&b, "- confidence: %s\n", firstNonBlankAnalysisString(packet.Confidence, "medium"))
		b.WriteString("```\n")
		b.WriteString(packet.Text)
		b.WriteString("\n```\n\n")
	}
	return strings.TrimSpace(b.String())
}

func collectSourceFunctionAnchorsForPaths(snapshot ProjectSnapshot, paths []string) []sourceFunctionAnchor {
	out := []sourceFunctionAnchor{}
	for _, path := range analysisUniqueStrings(paths) {
		file, ok := snapshot.FilesByPath[path]
		if !ok || !analysisSupportsSourceAnchors(file.Extension) {
			continue
		}
		abs := filepath.Join(snapshot.Root, filepath.FromSlash(path))
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

func evidencePacketFromAnchor(snapshot ProjectSnapshot, shard AnalysisShard, anchor sourceFunctionAnchor) (EvidencePacket, bool) {
	startHint := refineEvidenceAnchorStartLine(snapshot, anchor)
	text, startLine, endLine, truncated, ok := readEvidenceLineRange(snapshot, anchor.Symbol.File, startHint, anchor.Symbol.EndLine, analysisEvidencePacketMaxLines)
	if !ok {
		return EvidencePacket{}, false
	}
	tags := append([]string(nil), anchor.Symbol.Tags...)
	if truncated {
		tags = append(tags, "truncated")
	}
	packet := EvidencePacket{
		ShardID:          shard.ID,
		Kind:             firstNonBlankAnalysisString(anchor.Symbol.Kind, "symbol"),
		Path:             anchor.Symbol.File,
		SymbolID:         anchor.Symbol.ID,
		SymbolName:       firstNonBlankAnalysisString(anchor.Symbol.CanonicalName, anchor.Symbol.Name),
		StartLine:        startLine,
		EndLine:          endLine,
		ExtractionMethod: "source_anchor",
		Confidence:       "high",
		Tags:             analysisUniqueStrings(tags),
		Text:             text,
	}
	packet.ContentHash = hashAnalysisText(packet.Text)
	return packet, true
}

func refineEvidenceAnchorStartLine(snapshot ProjectSnapshot, anchor sourceFunctionAnchor) int {
	startLine := anchor.Symbol.StartLine
	if startLine < 1 {
		startLine = 1
	}
	name := strings.TrimSpace(anchor.Symbol.Name)
	if name == "" {
		return startLine
	}
	abs := filepath.Join(snapshot.Root, filepath.FromSlash(anchor.Symbol.File))
	data, err := os.ReadFile(abs)
	if err != nil {
		return startLine
	}
	lines := splitLines(string(data))
	if len(lines) == 0 {
		return startLine
	}
	endLine := anchor.Symbol.EndLine
	if endLine < startLine || endLine > len(lines) {
		endLine = len(lines)
	}
	for index := startLine - 1; index < endLine; index++ {
		line := strings.TrimSpace(lines[index])
		if strings.Contains(line, name) && strings.Contains(line, "(") {
			return index + 1
		}
	}
	return startLine
}

func fallbackEvidencePacketForFile(snapshot ProjectSnapshot, shard AnalysisShard, path string) (EvidencePacket, bool) {
	file, ok := snapshot.FilesByPath[path]
	if !ok {
		return EvidencePacket{}, false
	}
	text, startLine, endLine, truncated, ok := readEvidenceLineRange(snapshot, path, 1, analysisEvidencePacketFallbackLines, analysisEvidencePacketFallbackLines)
	if !ok {
		return EvidencePacket{}, false
	}
	tags := []string{"file_excerpt"}
	if file.IsEntrypoint {
		tags = append(tags, "entrypoint")
	}
	if file.IsManifest {
		tags = append(tags, "manifest")
	}
	if truncated || file.LineCount > endLine {
		tags = append(tags, "context_truncated")
	}
	packet := EvidencePacket{
		ShardID:          shard.ID,
		Kind:             "file_excerpt",
		Path:             path,
		StartLine:        startLine,
		EndLine:          endLine,
		ExtractionMethod: "file_prefix_fallback",
		Confidence:       "medium",
		Tags:             analysisUniqueStrings(tags),
		Text:             text,
	}
	packet.ContentHash = hashAnalysisText(packet.Text)
	return packet, true
}

func readEvidenceLineRange(snapshot ProjectSnapshot, path string, startLine int, endLine int, maxLines int) (string, int, int, bool, bool) {
	if maxLines <= 0 {
		maxLines = analysisEvidencePacketMaxLines
	}
	if startLine < 1 {
		startLine = 1
	}
	if endLine < startLine {
		endLine = startLine
	}
	abs := filepath.Join(snapshot.Root, filepath.FromSlash(path))
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", 0, 0, false, false
	}
	lines := splitLines(string(data))
	if len(lines) == 0 {
		return "", 0, 0, false, false
	}
	if startLine > len(lines) {
		return "", 0, 0, false, false
	}
	truncated := false
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if endLine-startLine+1 > maxLines {
		endLine = startLine + maxLines - 1
		truncated = true
	}
	selected := lines[startLine-1 : endLine]
	return strings.Join(selected, "\n"), startLine, endLine, truncated, true
}

func assignEvidencePacketIDs(shard AnalysisShard, packets []EvidencePacket) {
	prefix := strings.TrimSpace(shard.ID)
	if prefix == "" {
		prefix = "shard"
	}
	for i := range packets {
		packets[i].ID = fmt.Sprintf("%s-packet-%02d", prefix, i+1)
		if strings.TrimSpace(packets[i].ShardID) == "" {
			packets[i].ShardID = shard.ID
		}
	}
}

func analysisEvidencePacketPaths(shard AnalysisShard) []string {
	return analysisUniqueStrings(append(append([]string(nil), shard.PrimaryFiles...), shard.ReferenceFiles...))
}

func evidenceAnchorScore(anchor sourceFunctionAnchor, shard AnalysisShard, primary map[string]struct{}) int {
	score := 0
	if _, ok := primary[anchor.Symbol.File]; ok {
		score += 120
	} else {
		score += 50
	}
	lower := strings.ToLower(strings.Join([]string{
		anchor.Symbol.Name,
		anchor.Symbol.CanonicalName,
		anchor.Symbol.Kind,
		anchor.Symbol.File,
		strings.Join(anchor.Symbol.Tags, " "),
		strings.TrimSpace(anchor.Body),
	}, " "))
	shardName := strings.ToLower(shard.Name + " " + shard.Type + " " + shard.Objective)
	if containsAny(lower, "driverentry", "driver_entry", "dispatch", "devicecontrol", "device_control", "ioctl", "irp", "ctl_code") {
		score += 80
	}
	if containsAny(lower, "obregister", "psset", "cmregister", "fltregister", "callback", "minifilter", "wdf") {
		score += 55
	}
	if containsAny(lower, "probe", "validate", "bounds", "copy", "mmcopy", "mdl", "readprocessmemory", "writeprocessmemory") {
		score += 45
	}
	if containsAny(lower, "rpc", "pipe", "alpc", "command", "decrypt", "decode", "payload") {
		score += 40
	}
	if containsAny(shardName, "ioctl") && containsAny(lower, "ioctl", "devicecontrol", "irp", "ctl_code", "dispatch") {
		score += 60
	}
	if containsAny(shardName, "driver") && containsAny(lower, "driver", "entry", "unload", "start", "initialize", "register") {
		score += 45
	}
	if containsAny(shardName, "security", "surface") && containsAny(lower, "validate", "privilege", "access", "handle", "memory", "tamper", "integrity") {
		score += 35
	}
	if containsString(anchor.Symbol.Tags, "entrypoint") || containsString(anchor.Symbol.Tags, "ioctl") || containsString(anchor.Symbol.Tags, "security") {
		score += 35
	}
	if anchor.Symbol.StartLine > 80 {
		score += 12
	}
	bodyLines := anchor.Symbol.EndLine - anchor.Symbol.StartLine + 1
	if bodyLines > 0 {
		score += analysisMinInt(bodyLines/20, 15)
	}
	return score
}

func attachEvidencePacketsToWorkerReport(snapshot ProjectSnapshot, shard AnalysisShard, report *WorkerReport) {
	if report == nil {
		return
	}
	packets := buildEvidencePacketsForShard(snapshot, shard, analysisEvidencePacketDefaultLimit)
	if len(packets) == 0 || len(report.Claims) == 0 {
		return
	}
	packetByID := map[string]EvidencePacket{}
	packetIDsByPath := map[string][]string{}
	for _, packet := range packets {
		packetByID[strings.ToLower(packet.ID)] = packet
		packetIDsByPath[packet.Path] = append(packetIDsByPath[packet.Path], packet.ID)
	}
	unknowns := append([]string(nil), report.Unknowns...)
	for i := range report.Claims {
		validIDs := []string{}
		for _, id := range report.Claims[i].EvidencePacketIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if packet, ok := packetByID[strings.ToLower(id)]; ok {
				validIDs = append(validIDs, packet.ID)
			} else {
				unknowns = append(unknowns, fmt.Sprintf("Claim %s referenced unknown evidence packet %s.", firstNonBlankAnalysisString(report.Claims[i].ID, report.Claims[i].Claim), id))
			}
		}
		if len(validIDs) == 0 {
			for _, anchor := range report.Claims[i].SourceAnchors {
				path := evidenceAnchorPath(anchor, shard)
				if path == "" {
					continue
				}
				validIDs = append(validIDs, packetIDsByPath[path]...)
			}
		}
		report.Claims[i].EvidencePacketIDs = analysisUniqueStrings(validIDs)
		if len(report.Claims[i].EvidencePacketIDs) == 0 && strings.EqualFold(report.Claims[i].Confidence, "high") {
			report.Claims[i].Confidence = "medium"
			unknowns = append(unknowns, fmt.Sprintf("Claim %s was downgraded because it lacks an evidence_packet_id.", firstNonBlankAnalysisString(report.Claims[i].ID, report.Claims[i].Claim)))
		}
	}
	report.Unknowns = analysisUniqueStrings(unknowns)
}

func evidenceAnchorPath(anchor string, shard AnalysisShard) string {
	clean := cleanEvidencePath(anchor)
	if clean == "" {
		return ""
	}
	candidates := []string{clean}
	if index := strings.LastIndex(clean, ":"); index > 1 {
		suffix := clean[index+1:]
		if evidenceLineSuffixLooksNumeric(suffix) {
			candidates = append(candidates, clean[:index])
		}
	}
	for _, candidate := range candidates {
		if filtered := filterEvidence([]string{candidate}, shard); len(filtered) > 0 {
			return filtered[0]
		}
	}
	return ""
}

func evidenceLineSuffixLooksNumeric(suffix string) bool {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return false
	}
	suffix = strings.ReplaceAll(suffix, "-", "")
	for _, ch := range suffix {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
