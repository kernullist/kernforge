package main

import "strings"

const (
	analysisEvidencePacketCategoryRequired   = "required"
	analysisEvidencePacketCategorySupporting = "supporting"
	analysisEvidencePacketCategoryAmbiguous  = "ambiguous"
	analysisEvidencePacketCategoryGap        = "gap"
)

func categorizeEvidencePacketsForShard(snapshot ProjectSnapshot, shard AnalysisShard, packets []EvidencePacket) {
	requiredIDs := graphStringSet(shard.RequiredPacketIDs)
	primaryFiles := graphFileSet(shard.PrimaryFiles)
	seedSymbols := graphStringSet(shard.SeedSymbols)
	requiredByClass := graphShardRequiresPrimaryPacket(shard)
	graphEvidenceFiles := map[string]struct{}{}
	graphEdgeIDs := []string{}
	if shard.GraphNeighborhood != nil {
		for _, path := range shard.GraphNeighborhood.EvidenceFiles {
			if strings.TrimSpace(path) != "" {
				graphEvidenceFiles[path] = struct{}{}
			}
		}
		graphEdgeIDs = append(graphEdgeIDs, shard.GraphNeighborhood.EdgeIDs...)
	}
	for i := range packets {
		packet := &packets[i]
		if strings.TrimSpace(packet.Category) == "" {
			packet.Category = analysisEvidencePacketCategorySupporting
		}
		packet.EvidenceClass = graphEvidenceClassForPacket(snapshot, shard, *packet)
		if _, ok := requiredIDs[packet.ID]; ok {
			packet.Category = analysisEvidencePacketCategoryRequired
			packet.Required = true
		}
		if _, ok := seedSymbols[packet.SymbolID]; ok {
			packet.Category = analysisEvidencePacketCategoryRequired
			packet.Required = true
		}
		if requiredByClass {
			if _, ok := primaryFiles[packet.Path]; ok && strings.TrimSpace(packet.SymbolID) != "" && graphPacketMatchesShardClass(*packet, shard.Name) {
				packet.Category = analysisEvidencePacketCategoryRequired
				packet.Required = true
			}
		}
		if packet.Required {
			packet.Confidence = firstNonBlankAnalysisString(packet.Confidence, "high")
			packet.Tags = analysisUniqueStrings(append(packet.Tags, "required_packet"))
		} else if strings.EqualFold(packet.ExtractionMethod, "file_prefix_fallback") {
			packet.Category = analysisEvidencePacketCategoryGap
			packet.EvidenceClass = firstNonBlankAnalysisString(packet.EvidenceClass, "missing_symbol_anchor")
			packet.Tags = analysisUniqueStrings(append(packet.Tags, "gap_packet"))
		} else if containsString(packet.Tags, "context_truncated") || containsString(packet.Tags, "truncated") {
			packet.Category = analysisEvidencePacketCategoryAmbiguous
		}
		if _, ok := graphEvidenceFiles[packet.Path]; ok || packet.Required {
			packet.GraphEdgeIDs = analysisUniqueStrings(append(packet.GraphEdgeIDs, limitStrings(graphEdgeIDs, 8)...))
		}
	}
}

func graphRequiredPacketIDs(packets []EvidencePacket) []string {
	out := []string{}
	for _, packet := range packets {
		if packet.Required || strings.EqualFold(packet.Category, analysisEvidencePacketCategoryRequired) {
			out = append(out, packet.ID)
		}
	}
	return analysisUniqueStrings(out)
}

func graphShardRequiresPrimaryPacket(shard AnalysisShard) bool {
	name := strings.ToLower(strings.TrimSpace(shard.Name))
	return containsAny(name, "startup", "ioctl", "callback", "handle", "memory", "rpc", "asset_config", "build_context", "build_graph", "generated_artifact", "security")
}

func graphPacketMatchesShardClass(packet EvidencePacket, shardName string) bool {
	corpus := strings.ToLower(strings.Join([]string{
		packet.Kind,
		packet.Path,
		packet.SymbolID,
		packet.SymbolName,
		strings.Join(packet.Tags, " "),
		packet.Text,
	}, " "))
	name := strings.ToLower(strings.TrimSpace(shardName))
	switch {
	case containsAny(name, "ioctl"):
		return containsAny(corpus, "ioctl", "devicecontrol", "device_control", "irp", "ctl_code", "deviceiocontrol")
	case containsAny(name, "callback"):
		return containsAny(corpus, "callback", "obregister", "psset", "cmregister", "fltregister", "notifyroutine", "wfp")
	case containsAny(name, "handle", "memory"):
		return containsAny(corpus, "handle", "memory", "openprocess", "duplicatehandle", "mmcopy", "readprocessmemory", "writeprocessmemory", "mdl", "scan")
	case containsAny(name, "rpc"):
		return containsAny(corpus, "rpc", "ipc", "pipe", "alpc", "socket", "server", "client", "command")
	case containsAny(name, "asset_config"):
		return containsAny(corpus, "asset", "config", "loadobject", "loadclass", "tsoftobjectptr", ".ini", "defaultgame", "defaultengine")
	case containsAny(name, "build_context", "build_graph", "generated_artifact"):
		return containsAny(corpus, "build", "generated", "include", "target", "module", "project")
	case containsAny(name, "startup"):
		return containsAny(corpus, "main", "startup", "entry", "driverentry", "initialize", "bootstrap")
	default:
		return true
	}
}

func graphEvidenceClassForPacket(snapshot ProjectSnapshot, shard AnalysisShard, packet EvidencePacket) string {
	_ = snapshot
	name := strings.ToLower(strings.TrimSpace(shard.Name))
	switch {
	case graphPacketMatchesShardClass(packet, shard.Name) && containsAny(name, "ioctl"):
		return "ioctl_surface"
	case graphPacketMatchesShardClass(packet, shard.Name) && containsAny(name, "callback"):
		return "callback_registration"
	case graphPacketMatchesShardClass(packet, shard.Name) && containsAny(name, "handle", "memory"):
		return "handle_memory_surface"
	case graphPacketMatchesShardClass(packet, shard.Name) && containsAny(name, "rpc"):
		return "rpc_authority"
	case graphPacketMatchesShardClass(packet, shard.Name) && containsAny(name, "asset_config"):
		return "asset_config_boundary"
	case graphPacketMatchesShardClass(packet, shard.Name) && containsAny(name, "build_context", "build_graph"):
		return "build_context"
	case graphPacketMatchesShardClass(packet, shard.Name) && containsAny(name, "generated_artifact"):
		return "generated_artifact"
	case graphPacketMatchesShardClass(packet, shard.Name) && containsAny(name, "startup"):
		return "startup_path"
	case strings.TrimSpace(packet.SymbolID) != "":
		return "symbol"
	default:
		return "file_excerpt"
	}
}

func evidenceAnchorMatchesShardSeed(anchor sourceFunctionAnchor, shard AnalysisShard) bool {
	seedSet := graphStringSet(shard.SeedSymbols)
	if _, ok := seedSet[anchor.Symbol.ID]; ok {
		return true
	}
	name := strings.ToLower(firstNonBlankAnalysisString(anchor.Symbol.CanonicalName, anchor.Symbol.Name))
	if name == "" {
		return false
	}
	for _, seed := range shard.SeedSymbols {
		if seed == "" {
			continue
		}
		if strings.Contains(strings.ToLower(seed), name) || strings.Contains(name, strings.ToLower(seed)) {
			return true
		}
	}
	return false
}
