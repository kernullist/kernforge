package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func buildSecurityAntiCheatOverlay(snapshot ProjectSnapshot, index SemanticIndexV2) SecurityOverlaySummary {
	overlay := SecurityOverlaySummary{
		GeneratedAt: time.Now(),
	}
	nodeSeen := map[string]struct{}{}
	edgeSeen := map[string]struct{}{}
	surfaces := []string{}
	addNode := func(node SecurityOverlayNode) string {
		node.ID = strings.TrimSpace(node.ID)
		node.Type = strings.TrimSpace(node.Type)
		if node.ID == "" {
			node.ID = analysisGraphStableID("secnode", node.Type, node.Path, node.Label)
		}
		if node.Type == "" {
			node.Type = "security_overlay"
		}
		if _, ok := nodeSeen[node.ID]; ok {
			return node.ID
		}
		node.Confidence = firstNonBlankAnalysisString(node.Confidence, "medium")
		node.Evidence = analysisUniqueStrings(node.Evidence)
		node.Tags = analysisUniqueStrings(node.Tags)
		overlay.Nodes = append(overlay.Nodes, node)
		nodeSeen[node.ID] = struct{}{}
		return node.ID
	}
	addEdge := func(edge SecurityOverlayEdge) {
		edge.ID = strings.TrimSpace(edge.ID)
		if edge.ID == "" {
			edge.ID = analysisGraphStableID("secedge", edge.SourceID, edge.TargetID, edge.Type, edge.Surface)
		}
		edge.Type = strings.TrimSpace(edge.Type)
		edge.SourceID = strings.TrimSpace(edge.SourceID)
		edge.TargetID = strings.TrimSpace(edge.TargetID)
		if edge.SourceID == "" || edge.TargetID == "" || edge.Type == "" {
			return
		}
		if _, ok := edgeSeen[edge.ID]; ok {
			return
		}
		edge.Confidence = firstNonBlankAnalysisString(edge.Confidence, "medium")
		edge.Evidence = analysisUniqueStrings(edge.Evidence)
		overlay.Edges = append(overlay.Edges, edge)
		edgeSeen[edge.ID] = struct{}{}
		if strings.TrimSpace(edge.Surface) != "" {
			surfaces = append(surfaces, edge.Surface)
		}
		if strings.EqualFold(edge.ValidationState, "missing_candidate") {
			overlay.Metrics.MissingValidationCandidates++
			overlay.Metrics.BlockingIssueCount++
		}
	}
	for _, file := range snapshot.Files {
		text := securityOverlayFileCorpus(snapshot, file.Path)
		pathCorpus := strings.ToLower(file.Path + "\n" + text)
		hasIOCTL := containsAny(pathCorpus, "deviceiocontrol", "irp_mj_device_control", "ioctl", "ctl_code", "io_control_code")
		hasDriver := containsAny(pathCorpus, "driverentry", "driver_entry", "wdfdrivercreate", "driverobject", "driver unload", "driverunload")
		hasCallback := containsAny(pathCorpus, "obregistercallbacks", "psset", "notifyroutine", "cmregistercallback", "fltregisterfilter", "fwpm", "registercallback")
		hasHandle := containsAny(pathCorpus, "openprocess", "duplicatehandle", "obopenobjectbypointer", "accessmask", "handle")
		hasMemory := containsAny(pathCorpus, "mmcopyvirtualmemory", "zwreadvirtualmemory", "zwwritevirtualmemory", "readprocessmemory", "writeprocessmemory", "keattachprocess", "mdl", "probeandlock", "scan")
		hasRPC := containsAny(pathCorpus, "rpc", "namedpipe", "named pipe", "createfilew", "alpc", "socket", "bind(", "listen(", "command dispatcher", "commandhandler")
		hasTelemetry := containsAny(pathCorpus, "eventwrite", "etw", "telemetry", "traceevent", "writefile", "logger", "logevent")
		hasValidation := containsAny(pathCorpus, "probeforread", "probeforwrite", "inputbuffersize", "outputbuffersize", "sizeof(", "validate", "bounds", "length", "range", "access check", "seaccesscheck", "requestorMode")
		hasPrivilegedSink := hasHandle || hasMemory || containsAny(pathCorpus, "zw", "nt", "iomanager", "kernel", "privileged")
		if hasDriver {
			runtime := addNode(SecurityOverlayNode{
				ID:       analysisGraphStableID("runtime_activation", file.Path),
				Type:     "runtime_activation",
				Label:    "driver runtime activation",
				Path:     file.Path,
				Evidence: securityOverlayEvidence(file.Path, text, "driverentry", "driver_entry", "wdfdrivercreate"),
				Tags:     []string{"windows_driver"},
			})
			dispatcher := addNode(SecurityOverlayNode{
				ID:       analysisGraphStableID("dispatcher", file.Path, "driver"),
				Type:     "dispatcher",
				Label:    "driver dispatch table",
				Path:     file.Path,
				Evidence: securityOverlayEvidence(file.Path, text, "driverobject", "majorfunction", "dispatch"),
				Tags:     []string{"driver_dispatch"},
			})
			addEdge(SecurityOverlayEdge{
				SourceID:          runtime,
				TargetID:          dispatcher,
				Type:              "activates_runtime_filter",
				Surface:           "windows_driver",
				Evidence:          securityOverlayEvidence(file.Path, text, "driverentry", "majorfunction", "wdfdrivercreate"),
				RequiredInvariant: "driver runtime activation must be separated from request dispatch.",
			})
		}
		if hasIOCTL || hasRPC {
			surface := "ioctl"
			if hasRPC && !hasIOCTL {
				surface = "rpc"
			}
			input := addNode(SecurityOverlayNode{
				ID:       analysisGraphStableID("untrusted_input", file.Path, surface),
				Type:     "untrusted_input",
				Label:    surface + " input",
				Path:     file.Path,
				Evidence: securityOverlayEvidence(file.Path, text, "deviceiocontrol", "irp_mj_device_control", "ioctl", "rpc", "named pipe", "socket"),
				Tags:     []string{surface},
			})
			dispatcher := addNode(SecurityOverlayNode{
				ID:       analysisGraphStableID("dispatcher", file.Path, surface),
				Type:     "dispatcher",
				Label:    surface + " dispatcher",
				Path:     file.Path,
				Evidence: securityOverlayEvidence(file.Path, text, "switch", "dispatch", "iocode", "ioctl", "command"),
				Tags:     []string{surface, "dispatcher"},
			})
			edgeType := "input_reaches_dispatcher"
			if hasIOCTL {
				addEdge(SecurityOverlayEdge{
					SourceID:          input,
					TargetID:          dispatcher,
					Type:              "crosses_user_kernel_boundary",
					Surface:           "ioctl",
					Evidence:          securityOverlayEvidence(file.Path, text, "irp_mj_device_control", "deviceiocontrol", "ioctl"),
					RequiredInvariant: "user controlled IOCTL input must be validated before privileged kernel sinks.",
				})
			}
			if hasRPC {
				edgeType = "crosses_client_server_boundary"
			}
			addEdge(SecurityOverlayEdge{
				SourceID:          input,
				TargetID:          dispatcher,
				Type:              edgeType,
				Surface:           surface,
				Evidence:          securityOverlayEvidence(file.Path, text, "dispatch", "command", "ioctl", "rpc"),
				RequiredInvariant: "untrusted command input must route through explicit validation.",
			})
			if hasValidation {
				validation := addNode(SecurityOverlayNode{
					ID:       analysisGraphStableID("validation_gate", file.Path, surface),
					Type:     "validation_gate",
					Label:    surface + " validation",
					Path:     file.Path,
					Evidence: securityOverlayEvidence(file.Path, text, "probeforread", "probeforwrite", "validate", "inputbuffersize", "outputbuffersize", "bounds", "length"),
					Tags:     []string{surface, "validation"},
				})
				addEdge(SecurityOverlayEdge{
					SourceID:          dispatcher,
					TargetID:          validation,
					Type:              "handler_requires_validation",
					Surface:           surface,
					Evidence:          securityOverlayEvidence(file.Path, text, "validate", "inputbuffersize", "outputbuffersize", "probeforread", "probeforwrite"),
					RequiredInvariant: "dispatcher must validate payload shape before privileged sinks.",
					ValidationState:   "present",
				})
				if hasPrivilegedSink {
					sink := securityOverlayPrivilegedSinkNode(addNode, file.Path, text, surface)
					addEdge(SecurityOverlayEdge{
						SourceID:          validation,
						TargetID:          sink,
						Type:              "validated_before_sink",
						Surface:           surface,
						Evidence:          securityOverlayEvidence(file.Path, text, "mmcopy", "openprocess", "zwread", "writeprocessmemory", "duplicatehandle", "scan"),
						RequiredInvariant: "validation must dominate privileged sink reachability.",
						ValidationState:   "present",
					})
				}
			} else if hasPrivilegedSink {
				sink := securityOverlayPrivilegedSinkNode(addNode, file.Path, text, surface)
				addEdge(SecurityOverlayEdge{
					SourceID:          dispatcher,
					TargetID:          sink,
					Type:              "missing_validation_candidate",
					Surface:           surface,
					Evidence:          securityOverlayEvidence(file.Path, text, "mmcopy", "openprocess", "zwread", "writeprocessmemory", "duplicatehandle", "scan"),
					RequiredInvariant: "privileged sink must not be reachable from untrusted input without validation evidence.",
					ValidationState:   "missing_candidate",
				})
			}
		}
		if hasCallback {
			registration := addNode(SecurityOverlayNode{
				ID:       analysisGraphStableID("callback_registration", file.Path),
				Type:     "callback_registration",
				Label:    "callback registration",
				Path:     file.Path,
				Evidence: securityOverlayEvidence(file.Path, text, "obregistercallbacks", "psset", "notifyroutine", "fltregisterfilter", "cmregistercallback"),
				Tags:     []string{"callback"},
			})
			runtime := addNode(SecurityOverlayNode{
				ID:       analysisGraphStableID("runtime_activation", file.Path, "callback"),
				Type:     "runtime_activation",
				Label:    "callback runtime activation",
				Path:     file.Path,
				Evidence: securityOverlayEvidence(file.Path, text, "start", "register", "initialize", "fltstartfiltering"),
				Tags:     []string{"callback"},
			})
			addEdge(SecurityOverlayEdge{
				SourceID:          runtime,
				TargetID:          registration,
				Type:              "registers_callback",
				Surface:           "callback",
				Evidence:          securityOverlayEvidence(file.Path, text, "obregistercallbacks", "psset", "notifyroutine", "fltregisterfilter", "cmregistercallback"),
				RequiredInvariant: "callback registration must be treated as runtime activation, not just initialization.",
				ValidationState:   "not_applicable",
			})
		}
		if hasTelemetry {
			source := addNode(SecurityOverlayNode{
				ID:       analysisGraphStableID("dispatcher", file.Path, "telemetry_source"),
				Type:     "dispatcher",
				Label:    "telemetry producer",
				Path:     file.Path,
				Evidence: securityOverlayEvidence(file.Path, text, "telemetry", "eventwrite", "traceevent", "log"),
				Tags:     []string{"telemetry"},
			})
			sink := addNode(SecurityOverlayNode{
				ID:       analysisGraphStableID("telemetry_sink", file.Path),
				Type:     "telemetry_sink",
				Label:    "telemetry output",
				Path:     file.Path,
				Evidence: securityOverlayEvidence(file.Path, text, "eventwrite", "etw", "writefile", "logger"),
				Tags:     []string{"telemetry"},
			})
			addEdge(SecurityOverlayEdge{
				SourceID:          source,
				TargetID:          sink,
				Type:              "writes_telemetry",
				Surface:           "telemetry",
				Evidence:          securityOverlayEvidence(file.Path, text, "eventwrite", "etw", "writefile", "logger"),
				RequiredInvariant: "telemetry output must not leak sensitive input or tamper state.",
			})
		}
	}
	for _, symbol := range index.Symbols {
		corpus := strings.ToLower(strings.Join([]string{symbol.ID, symbol.Name, symbol.CanonicalName, symbol.Kind, symbol.File, strings.Join(symbol.Tags, " ")}, " "))
		if !containsAny(corpus, "ioctl", "callback", "obregister", "psset", "fltregister", "rpc", "replicated", "authority", "tamper", "integrity", "telemetry") {
			continue
		}
		nodeType := "tamper_sensitive_state"
		if containsAny(corpus, "ioctl", "rpc", "dispatch") {
			nodeType = "dispatcher"
		} else if containsAny(corpus, "callback", "obregister", "psset", "fltregister") {
			nodeType = "callback_registration"
		} else if containsAny(corpus, "telemetry") {
			nodeType = "telemetry_sink"
		}
		addNode(SecurityOverlayNode{
			ID:         analysisGraphStableID(nodeType, symbol.ID),
			Type:       nodeType,
			Label:      firstNonBlankAnalysisString(symbol.CanonicalName, symbol.Name),
			Path:       symbol.File,
			SymbolID:   symbol.ID,
			Confidence: "medium",
			Evidence:   []string{graphSourceAnchor(symbol.File, symbol.StartLine)},
			Tags:       append([]string{symbol.Kind}, symbol.Tags...),
		})
	}
	for _, item := range snapshot.UnrealNetwork {
		if strings.TrimSpace(item.File) == "" {
			continue
		}
		typeName := firstNonBlankAnalysisString(item.TypeName, item.File)
		authority := addNode(SecurityOverlayNode{
			ID:       analysisGraphStableID("authority_boundary", item.File, typeName),
			Type:     "authority_boundary",
			Label:    typeName + " RPC authority",
			Path:     item.File,
			Evidence: []string{item.File},
			Tags:     []string{"unreal", "rpc", "authority"},
		})
		state := addNode(SecurityOverlayNode{
			ID:       analysisGraphStableID("tamper_sensitive_state", item.File, typeName),
			Type:     "tamper_sensitive_state",
			Label:    typeName + " replicated state",
			Path:     item.File,
			Evidence: []string{item.File},
			Tags:     []string{"unreal", "replication"},
		})
		addEdge(SecurityOverlayEdge{
			SourceID:          authority,
			TargetID:          state,
			Type:              "crosses_client_server_boundary",
			Surface:           "ue_rpc",
			Evidence:          []string{item.File},
			RequiredInvariant: "server/client RPC authority must protect replicated sensitive state.",
			ValidationState:   "requires_review",
		})
	}
	for _, item := range snapshot.UnrealAssets {
		if strings.TrimSpace(item.File) == "" {
			continue
		}
		boundary := addNode(SecurityOverlayNode{
			ID:       analysisGraphStableID("asset_config_boundary", item.File, item.OwnerName),
			Type:     "asset_config_boundary",
			Label:    firstNonBlankAnalysisString(item.OwnerName, item.File),
			Path:     item.File,
			Evidence: []string{item.File},
			Tags:     []string{"unreal", "asset", "config"},
		})
		runtime := addNode(SecurityOverlayNode{
			ID:       analysisGraphStableID("runtime_activation", item.File, "asset_load"),
			Type:     "runtime_activation",
			Label:    "asset/config runtime load",
			Path:     item.File,
			Evidence: []string{item.File},
			Tags:     []string{"unreal", "asset_load"},
		})
		addEdge(SecurityOverlayEdge{
			SourceID:          boundary,
			TargetID:          runtime,
			Type:              "activates_runtime_filter",
			Surface:           "asset_config",
			Evidence:          []string{item.File},
			RequiredInvariant: "asset/config references are trust-boundary inputs to runtime load paths.",
			ValidationState:   "requires_review",
		})
	}
	for _, item := range snapshot.UnrealSettings {
		if strings.TrimSpace(item.SourceFile) == "" {
			continue
		}
		boundary := addNode(SecurityOverlayNode{
			ID:       analysisGraphStableID("asset_config_boundary", item.SourceFile, "settings"),
			Type:     "asset_config_boundary",
			Label:    "Unreal startup settings",
			Path:     item.SourceFile,
			Evidence: []string{item.SourceFile},
			Tags:     []string{"unreal", "settings", "config"},
		})
		runtime := addNode(SecurityOverlayNode{
			ID:       analysisGraphStableID("runtime_activation", item.SourceFile, "startup_settings"),
			Type:     "runtime_activation",
			Label:    "configured startup runtime",
			Path:     item.SourceFile,
			Evidence: []string{item.SourceFile},
			Tags:     []string{"unreal", "startup"},
		})
		addEdge(SecurityOverlayEdge{
			SourceID:          boundary,
			TargetID:          runtime,
			Type:              "activates_runtime_filter",
			Surface:           "asset_config",
			Evidence:          []string{item.SourceFile},
			RequiredInvariant: "startup maps, game modes, pawns, controllers, and HUD config must be treated as trust-boundary data.",
			ValidationState:   "requires_review",
		})
	}
	overlay.Metrics.NodeCount = len(overlay.Nodes)
	overlay.Metrics.EdgeCount = len(overlay.Edges)
	overlay.Metrics.Surfaces = analysisUniqueStrings(surfaces)
	sort.SliceStable(overlay.Nodes, func(i int, j int) bool {
		return overlay.Nodes[i].ID < overlay.Nodes[j].ID
	})
	sort.SliceStable(overlay.Edges, func(i int, j int) bool {
		return overlay.Edges[i].ID < overlay.Edges[j].ID
	})
	overlay.FollowUp = securityOverlayFollowUp(overlay)
	return overlay
}

func securityOverlayPrivilegedSinkNode(addNode func(SecurityOverlayNode) string, path string, text string, surface string) string {
	return addNode(SecurityOverlayNode{
		ID:       analysisGraphStableID("privileged_sink", path, surface),
		Type:     "privileged_sink",
		Label:    surface + " privileged sink",
		Path:     path,
		Evidence: securityOverlayEvidence(path, text, "mmcopy", "openprocess", "duplicatehandle", "zwread", "writeprocessmemory", "keattachprocess", "mdl", "scan"),
		Tags:     []string{surface, "privileged_sink"},
	})
}

func securityOverlayFileCorpus(snapshot ProjectSnapshot, path string) string {
	abs := filepath.Join(snapshot.Root, filepath.FromSlash(path))
	data, err := os.ReadFile(abs)
	if err != nil {
		return path
	}
	return string(data)
}

func securityOverlayEvidence(path string, text string, needles ...string) []string {
	lines := splitLines(text)
	lowerNeedles := []string{}
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" {
			lowerNeedles = append(lowerNeedles, needle)
		}
	}
	for index, line := range lines {
		lower := strings.ToLower(line)
		for _, needle := range lowerNeedles {
			if strings.Contains(lower, needle) {
				return []string{fmt.Sprintf("%s:%d", filepathSlashOrEmpty(path), index+1)}
			}
		}
	}
	if strings.TrimSpace(path) != "" {
		return []string{filepathSlashOrEmpty(path)}
	}
	return nil
}

func securityOverlayFollowUp(overlay SecurityOverlaySummary) []string {
	items := []string{}
	if overlay.Metrics.MissingValidationCandidates > 0 {
		items = append(items, "Review missing_validation_candidate edges before treating IOCTL/RPC paths as safe.")
	}
	if len(overlay.Metrics.Surfaces) == 0 {
		items = append(items, "No deterministic security overlay surfaces were detected; confirm scan scope and parser coverage.")
	}
	if containsString(overlay.Metrics.Surfaces, "ue_rpc") {
		items = append(items, "Verify server/client RPC authority and replicated state invariants with targeted gameplay tests.")
	}
	if containsString(overlay.Metrics.Surfaces, "asset_config") {
		items = append(items, "Verify asset/config trust-boundary assumptions with docs-refresh and runtime configuration checks.")
	}
	return analysisUniqueStrings(items)
}

func securityOverlayTouchesClaim(overlay SecurityOverlaySummary, claim AnalysisClaim, packets []EvidencePacket) bool {
	packetPaths := map[string]struct{}{}
	for _, packet := range packets {
		if strings.TrimSpace(packet.Path) != "" {
			packetPaths[packet.Path] = struct{}{}
		}
	}
	for _, edge := range overlay.Edges {
		if graphEdgeTouchesFiles(edge.Evidence, packetPaths) {
			return true
		}
	}
	for _, anchor := range claim.SourceAnchors {
		path, _, ok := parseAnalysisClaimSourceAnchor(anchor)
		if !ok || path == "" {
			continue
		}
		for _, edge := range overlay.Edges {
			if graphEdgeTouchesFiles(edge.Evidence, map[string]struct{}{path: {}}) {
				return true
			}
		}
	}
	return false
}

func securityOverlayClaimContradictsBoundary(overlay SecurityOverlaySummary, claim AnalysisClaim, packets []EvidencePacket) (bool, []string) {
	text := strings.ToLower(strings.Join([]string{claim.Kind, claim.Claim, claim.VerificationHint}, " "))
	if !securityOverlayClaimAssertsBoundarySafe(text) {
		return false, nil
	}
	packetPaths := map[string]struct{}{}
	for _, packet := range packets {
		packetPaths[packet.Path] = struct{}{}
	}
	evidence := []string{}
	for _, edge := range overlay.Edges {
		if !strings.EqualFold(edge.ValidationState, "missing_candidate") {
			continue
		}
		if graphEdgeTouchesFiles(edge.Evidence, packetPaths) {
			evidence = append(evidence, edge.Evidence...)
		}
	}
	return len(evidence) > 0, analysisUniqueStrings(evidence)
}

func securityOverlayClaimAssertsBoundarySafe(text string) bool {
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))
	if text == "" {
		return false
	}
	if containsAny(text,
		"missing validation",
		"validation missing",
		"lacks validation",
		"lack validation",
		"without validation",
		"without validating",
		"unvalidated",
		"not validated",
		"no validation",
		"no validator",
		"missing_candidate",
		"requires review",
		"requires validation",
		"unsafe",
		"risk",
		"gap",
	) {
		return false
	}
	return containsAny(text,
		"safe",
		"validated",
		"validation present",
		"validation gate",
		"checked",
		"sanitized",
		"authorized",
		"access checked",
		"guards",
		"guarded",
		"enforces validation",
	)
}
