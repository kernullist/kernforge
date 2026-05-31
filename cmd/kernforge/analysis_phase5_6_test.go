package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGraphGuidedShardMetadataAddsRequiredPacketsAndFingerprint(t *testing.T) {
	root := t.TempDir()
	writePhase56TestFile(t, root, "driver/ioctl.cpp", strings.Join([]string{
		"NTSTATUS DeviceControl()",
		"{",
		"    ProbeForRead(nullptr, 0, 1);",
		"    return MmCopyVirtualMemory();",
		"}",
	}, "\n"))
	snapshot := phase56Snapshot(root, []ScannedFile{
		{Path: "driver/ioctl.cpp", Directory: "driver", Extension: ".cpp", LineCount: 5, ImportanceScore: 100},
	})
	snapshot.AnalysisMode = "security"
	snapshot.StructuralIndex = StructuralIndex{
		Symbols: []SymbolRecord{{
			ID:            "sym:DeviceControl",
			Name:          "DeviceControl",
			CanonicalName: "DeviceControl",
			Kind:          "function",
			File:          "driver/ioctl.cpp",
			StartLine:     1,
			EndLine:       5,
			Tags:          []string{"ioctl"},
		}},
	}
	index := SemanticIndexV2{
		RunID: "run",
		Root:  root,
		Symbols: []SymbolRecord{{
			ID:            "sym:DeviceControl",
			Name:          "DeviceControl",
			CanonicalName: "DeviceControl",
			Kind:          "function",
			File:          "driver/ioctl.cpp",
			StartLine:     1,
			EndLine:       5,
			Tags:          []string{"ioctl"},
		}},
	}
	shards := buildGraphShardPlan(snapshot, index, "security", 4)
	if len(shards) == 0 {
		t.Fatalf("expected graph shard plan")
	}
	found := false
	for i := range shards {
		shards[i].ID = "shard-01"
		if strings.Contains(shards[i].Name, "security_ioctl") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected security_ioctl graph shard, got %#v", shards)
	}
	overlay := buildSecurityAntiCheatOverlay(snapshot, index)
	graph := buildAnalysisEvidenceGraph(snapshot, index, UnrealSemanticGraph{}, overlay, nil)
	enriched := enrichAnalysisShardsWithGraph(snapshot, index, graph, overlay, shards)
	if len(enriched[0].SeedSymbols) == 0 {
		t.Fatalf("expected seed symbols")
	}
	if strings.TrimSpace(enriched[0].GraphFingerprint) == "" {
		t.Fatalf("expected graph fingerprint")
	}
	if len(enriched[0].RequiredPacketIDs) == 0 {
		t.Fatalf("expected required packet ids")
	}
}

func TestEvidencePacketsClassifyRequiredSupportingAmbiguousAndGap(t *testing.T) {
	root := t.TempDir()
	writePhase56TestFile(t, root, "driver/ioctl.cpp", strings.Join([]string{
		"NTSTATUS DeviceControl()",
		"{",
		"    return STATUS_SUCCESS;",
		"}",
	}, "\n"))
	writePhase56TestFile(t, root, "README.md", "this is a long fallback-only document")
	snapshot := phase56Snapshot(root, []ScannedFile{
		{Path: "driver/ioctl.cpp", Directory: "driver", Extension: ".cpp", LineCount: 4, ImportanceScore: 100},
		{Path: "README.md", Directory: "", Extension: ".md", LineCount: 1, ImportanceScore: 1},
	})
	snapshot.StructuralIndex = StructuralIndex{
		Symbols: []SymbolRecord{{
			ID:            "sym:DeviceControl",
			Name:          "DeviceControl",
			CanonicalName: "DeviceControl",
			Kind:          "function",
			File:          "driver/ioctl.cpp",
			StartLine:     1,
			EndLine:       4,
			Tags:          []string{"ioctl"},
		}},
	}
	shard := AnalysisShard{
		ID:             "shard-01",
		Name:           "security_ioctl",
		PrimaryFiles:   []string{"driver/ioctl.cpp", "README.md"},
		SeedSymbols:    []string{"sym:DeviceControl"},
		ReferenceFiles: nil,
		GraphNeighborhood: &AnalysisGraphNeighborhood{
			EdgeIDs:       []string{"edge-1"},
			EvidenceFiles: []string{"driver/ioctl.cpp"},
		},
	}
	packets := buildEvidencePacketsForShard(snapshot, shard, 4)
	if len(packets) < 2 {
		t.Fatalf("expected symbol and fallback packets, got %d", len(packets))
	}
	if packets[0].Category != analysisEvidencePacketCategoryRequired || !packets[0].Required {
		t.Fatalf("expected first packet to be required, got %#v", packets[0])
	}
	foundGap := false
	for _, packet := range packets {
		if packet.Path == "README.md" && packet.Category == analysisEvidencePacketCategoryGap {
			foundGap = true
		}
	}
	if !foundGap {
		t.Fatalf("expected fallback packet to be classified as gap: %#v", packets)
	}
}

func TestBuildGraphEvidencePacketsAreRequiredBuildContext(t *testing.T) {
	root := t.TempDir()
	writePhase56TestFile(t, root, "build/CompileContext.cpp", strings.Join([]string{
		"int BuildIncludeOwner()",
		"{",
		"    return 0;",
		"}",
	}, "\n"))
	snapshot := phase56Snapshot(root, []ScannedFile{
		{Path: "build/CompileContext.cpp", Directory: "build", Extension: ".cpp", LineCount: 4, ImportanceScore: 100},
	})
	snapshot.StructuralIndex = StructuralIndex{
		Symbols: []SymbolRecord{{
			ID:            "sym:BuildIncludeOwner",
			Name:          "BuildIncludeOwner",
			CanonicalName: "BuildIncludeOwner",
			Kind:          "function",
			File:          "build/CompileContext.cpp",
			StartLine:     1,
			EndLine:       4,
			Tags:          []string{"build"},
		}},
	}
	shard := AnalysisShard{
		ID:           "shard-build",
		Name:         "build_graph",
		PrimaryFiles: []string{"build/CompileContext.cpp"},
	}
	packets := buildEvidencePacketsForShard(snapshot, shard, 2)
	if len(packets) == 0 {
		t.Fatalf("expected build graph evidence packet")
	}
	if !packets[0].Required || packets[0].Category != analysisEvidencePacketCategoryRequired {
		t.Fatalf("expected build graph packet to be required, got %#v", packets[0])
	}
	if packets[0].EvidenceClass != "build_context" {
		t.Fatalf("expected build_context evidence class, got %#v", packets[0])
	}
}

func TestClaimVerifierBlocksUnsupportedHighConfidenceClaim(t *testing.T) {
	run := ProjectAnalysisRun{
		Shards: []AnalysisShard{{
			ID:           "shard-01",
			Name:         "security_ioctl",
			PrimaryFiles: []string{"driver/ioctl.cpp"},
		}},
		Reports: []WorkerReport{{
			ShardID: "shard-01",
			Facts:   []string{"The IOCTL path is fully validated."},
			Claims: []AnalysisClaim{{
				ID:         "claim-1",
				Kind:       "fact",
				Claim:      "The IOCTL path is fully validated.",
				Confidence: "high",
			}},
		}},
	}
	report := verifyAnalysisClaims(ProjectSnapshot{}, run)
	if report.BlockingCount != 1 {
		t.Fatalf("expected blocking unsupported high-confidence claim, got %#v", report)
	}
	applyClaimVerificationToReports(&run, report)
	if len(run.Reports[0].Facts) != 0 {
		t.Fatalf("unsupported exact fact should be removed from facts: %#v", run.Reports[0].Facts)
	}
	if len(run.UnsupportedClaims) != 1 {
		t.Fatalf("expected unsupported claim artifact entry")
	}
}

func TestSecurityOverlayContradictsOnlyPositiveValidationClaims(t *testing.T) {
	root := t.TempDir()
	writePhase56TestFile(t, root, "driver/ioctl.cpp", strings.Join([]string{
		"NTSTATUS DeviceControl()",
		"{",
		"    return MmCopyVirtualMemory();",
		"}",
	}, "\n"))
	snapshot := phase56Snapshot(root, []ScannedFile{
		{Path: "driver/ioctl.cpp", Directory: "driver", Extension: ".cpp", LineCount: 4, ImportanceScore: 100},
	})
	packet := EvidencePacket{
		ID:        "shard-01-packet-01",
		ShardID:   "shard-01",
		Path:      "driver/ioctl.cpp",
		StartLine: 1,
		EndLine:   4,
		Category:  analysisEvidencePacketCategoryRequired,
		Required:  true,
	}
	overlay := buildSecurityAntiCheatOverlay(snapshot, SemanticIndexV2{})
	graph := buildAnalysisEvidenceGraph(snapshot, SemanticIndexV2{}, UnrealSemanticGraph{}, overlay, []EvidencePacket{packet})
	run := ProjectAnalysisRun{
		Shards: []AnalysisShard{{
			ID:           "shard-01",
			Name:         "security_ioctl",
			PrimaryFiles: []string{"driver/ioctl.cpp"},
		}},
		EvidencePackets: []EvidencePacket{packet},
		EvidenceGraph:   graph,
		SecurityOverlay: overlay,
		Reports: []WorkerReport{{
			ShardID: "shard-01",
			Claims: []AnalysisClaim{
				{
					ID:                "claim-missing",
					Kind:              "risk",
					Claim:             "The IOCTL path lacks validation before the privileged sink.",
					SourceAnchors:     []string{"driver/ioctl.cpp:2"},
					EvidencePacketIDs: []string{"shard-01-packet-01"},
					Confidence:        "high",
				},
				{
					ID:                "claim-safe",
					Kind:              "fact",
					Claim:             "The IOCTL path is validated and safe before the privileged sink.",
					SourceAnchors:     []string{"driver/ioctl.cpp:2"},
					EvidencePacketIDs: []string{"shard-01-packet-01"},
					Confidence:        "high",
				},
			},
		}},
	}
	report := verifyAnalysisClaims(snapshot, run)
	if report.BlockingCount != 1 {
		t.Fatalf("expected only positive safety claim to block, got %#v", report)
	}
	for _, result := range report.Results {
		hasBoundaryIssue := false
		for _, issue := range result.Issues {
			if issue.Code == "security_boundary_invariant_violation" {
				hasBoundaryIssue = true
			}
		}
		if result.ClaimID == "claim-missing" && hasBoundaryIssue {
			t.Fatalf("missing-validation risk claim should not be treated as a positive safety contradiction: %#v", result)
		}
		if result.ClaimID == "claim-safe" && !hasBoundaryIssue {
			t.Fatalf("positive safety claim should be contradicted by missing validation candidate: %#v", result)
		}
	}
}

func TestClaimVerifierBlocksLineSymbolAndGraphMismatches(t *testing.T) {
	run := ProjectAnalysisRun{
		Shards: []AnalysisShard{{
			ID:           "shard-01",
			Name:         "security_ioctl",
			PrimaryFiles: []string{"driver/ioctl.cpp"},
		}},
		EvidencePackets: []EvidencePacket{{
			ID:         "shard-01-packet-01",
			ShardID:    "shard-01",
			Kind:       "function",
			Path:       "driver/ioctl.cpp",
			SymbolID:   "sym:DeviceControl",
			SymbolName: "DeviceControl",
			StartLine:  10,
			EndLine:    20,
			Category:   analysisEvidencePacketCategoryRequired,
			Required:   true,
		}},
		SemanticIndexV2: SemanticIndexV2{
			Symbols: []SymbolRecord{{
				ID:            "sym:DeviceControl",
				Name:          "DeviceControl",
				CanonicalName: "DeviceControl",
				Kind:          "function",
				File:          "driver/ioctl.cpp",
				StartLine:     10,
				EndLine:       20,
			}},
		},
		Reports: []WorkerReport{{
			ShardID: "shard-01",
			Claims: []AnalysisClaim{{
				ID:                "claim-1",
				Kind:              "fact",
				Claim:             "DeviceControl routes to a privileged sink.",
				SourceAnchors:     []string{"driver/ioctl.cpp:99#WrongSymbol"},
				EvidencePacketIDs: []string{"shard-01-packet-01"},
				Confidence:        "high",
			}},
		}},
	}
	report := verifyAnalysisClaims(ProjectSnapshot{}, run)
	if report.BlockingCount != 1 {
		t.Fatalf("expected blocking verifier result, got %#v", report)
	}
	codes := map[string]struct{}{}
	for _, issue := range report.Results[0].Issues {
		codes[issue.Code] = struct{}{}
	}
	if _, ok := codes["line_range_mismatch"]; !ok {
		t.Fatalf("expected line_range_mismatch issue: %#v", report.Results[0].Issues)
	}
	if _, ok := codes["symbol_mismatch"]; !ok {
		t.Fatalf("expected symbol_mismatch issue: %#v", report.Results[0].Issues)
	}
}

func TestClaimVerifierDowngradesFlowClaimWithoutGraphEdge(t *testing.T) {
	run := ProjectAnalysisRun{
		Shards: []AnalysisShard{{
			ID:           "shard-01",
			Name:         "security_rpc",
			PrimaryFiles: []string{"rpc.cpp"},
		}},
		EvidencePackets: []EvidencePacket{{
			ID:        "shard-01-packet-01",
			ShardID:   "shard-01",
			Path:      "rpc.cpp",
			StartLine: 1,
			EndLine:   5,
			Category:  analysisEvidencePacketCategoryRequired,
			Required:  true,
		}},
		Reports: []WorkerReport{{
			ShardID: "shard-01",
			Claims: []AnalysisClaim{{
				ID:                "claim-1",
				Kind:              "fact",
				Claim:             "RPC input routes to command dispatch.",
				SourceAnchors:     []string{"rpc.cpp:2"},
				EvidencePacketIDs: []string{"shard-01-packet-01"},
				Confidence:        "high",
			}},
		}},
	}
	report := verifyAnalysisClaims(ProjectSnapshot{}, run)
	if report.DowngradedCount != 1 {
		t.Fatalf("expected graph-edge warning to downgrade high-confidence flow claim: %#v", report)
	}
	if report.Results[0].Status != "downgraded" {
		t.Fatalf("expected downgraded status, got %s", report.Results[0].Status)
	}
}

func TestSecurityOverlayDetectsWindowsIoctlValidationAndCallback(t *testing.T) {
	root := t.TempDir()
	writePhase56TestFile(t, root, "driver/main.cpp", strings.Join([]string{
		"NTSTATUS DriverEntry()",
		"{",
		"    DriverObject->MajorFunction[IRP_MJ_DEVICE_CONTROL] = DeviceControl;",
		"    ObRegisterCallbacks(nullptr, nullptr);",
		"    return STATUS_SUCCESS;",
		"}",
		"NTSTATUS DeviceControl()",
		"{",
		"    ProbeForRead(nullptr, 0, 1);",
		"    return MmCopyVirtualMemory();",
		"}",
	}, "\n"))
	snapshot := phase56Snapshot(root, []ScannedFile{
		{Path: "driver/main.cpp", Directory: "driver", Extension: ".cpp", LineCount: 11, ImportanceScore: 100},
	})
	overlay := buildSecurityAntiCheatOverlay(snapshot, SemanticIndexV2{})
	if !securityOverlayHasEdgeType(overlay, "crosses_user_kernel_boundary") {
		t.Fatalf("expected user/kernel boundary edge: %#v", overlay.Edges)
	}
	if !securityOverlayHasEdgeType(overlay, "validated_before_sink") {
		t.Fatalf("expected validated-before-sink edge: %#v", overlay.Edges)
	}
	if !securityOverlayHasNodeType(overlay, "callback_registration") {
		t.Fatalf("expected callback registration node: %#v", overlay.Nodes)
	}
}

func TestSecurityOverlayDetectsUnrealRPCAndAssetConfig(t *testing.T) {
	snapshot := ProjectSnapshot{
		GeneratedAt: time.Now(),
		UnrealNetwork: []UnrealNetworkSurface{{
			TypeName:             "APlayerPawn",
			File:                 "Source/Game/PlayerPawn.cpp",
			ServerRPCs:           []string{"ServerMove"},
			ReplicatedProperties: []string{"Health"},
		}},
		UnrealAssets: []UnrealAssetReference{{
			OwnerName:        "APlayerPawn",
			File:             "Source/Game/PlayerPawn.cpp",
			AssetPaths:       []string{"/Game/Data/Balance"},
			CanonicalTargets: []string{"Balance"},
			LoadMethods:      []string{"LoadObject"},
		}},
	}
	overlay := buildSecurityAntiCheatOverlay(snapshot, SemanticIndexV2{})
	if !securityOverlayHasEdgeSurface(overlay, "ue_rpc") {
		t.Fatalf("expected ue_rpc overlay edge: %#v", overlay.Edges)
	}
	if !securityOverlayHasEdgeSurface(overlay, "asset_config") {
		t.Fatalf("expected asset_config overlay edge: %#v", overlay.Edges)
	}
}

func TestSymbolGraphFingerprintIgnoresUnrelatedSymbolChange(t *testing.T) {
	root := t.TempDir()
	snapshot := phase56Snapshot(root, []ScannedFile{
		{Path: "a.cpp", Extension: ".cpp", LineCount: 3, ImportanceScore: 50},
		{Path: "b.cpp", Extension: ".cpp", LineCount: 3, ImportanceScore: 50},
	})
	prevIndex := SemanticIndexV2{Symbols: []SymbolRecord{
		{ID: "sym:A", Name: "A", Kind: "function", File: "a.cpp", Signature: "void A()", StartLine: 1, EndLine: 3},
		{ID: "sym:B", Name: "B", Kind: "function", File: "b.cpp", Signature: "void B()", StartLine: 1, EndLine: 3},
	}}
	nextIndex := SemanticIndexV2{Symbols: []SymbolRecord{
		{ID: "sym:A", Name: "A", Kind: "function", File: "a.cpp", Signature: "int A(int changed)", StartLine: 1, EndLine: 3},
		{ID: "sym:B", Name: "B", Kind: "function", File: "b.cpp", Signature: "void B()", StartLine: 1, EndLine: 3},
	}}
	shardB := AnalysisShard{ID: "shard-b", Name: "unrelated", Type: "graph_community", PrimaryFiles: []string{"b.cpp"}, SeedSymbols: []string{"sym:B"}}
	prevOverlay := buildSecurityAntiCheatOverlay(snapshot, prevIndex)
	nextOverlay := buildSecurityAntiCheatOverlay(snapshot, nextIndex)
	prevGraph := buildAnalysisEvidenceGraph(snapshot, prevIndex, UnrealSemanticGraph{}, prevOverlay, nil)
	nextGraph := buildAnalysisEvidenceGraph(snapshot, nextIndex, UnrealSemanticGraph{}, nextOverlay, nil)
	prev := enrichAnalysisShardsWithGraph(snapshot, prevIndex, prevGraph, prevOverlay, []AnalysisShard{shardB})[0]
	next := enrichAnalysisShardsWithGraph(snapshot, nextIndex, nextGraph, nextOverlay, []AnalysisShard{shardB})[0]
	if prev.GraphFingerprint != next.GraphFingerprint {
		t.Fatalf("unrelated symbol change should not alter shard B graph fingerprint: %s != %s", prev.GraphFingerprint, next.GraphFingerprint)
	}
}

func TestPersistRunWritesPhase56Artifacts(t *testing.T) {
	root := t.TempDir()
	analyzer := &projectAnalyzer{
		analysisCfg: defaultProjectAnalysisConfig(root),
	}
	analyzer.analysisCfg.OutputDir = filepath.Join(root, ".kernforge", "analysis")
	run := ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID:       "20260531-010203",
			Goal:        "map graph",
			Mode:        "security",
			Status:      "completed",
			StartedAt:   time.Now(),
			CompletedAt: time.Now(),
			TotalShards: 1,
		},
		Snapshot: ProjectSnapshot{
			Root:        root,
			GeneratedAt: time.Now(),
			FilesByPath: map[string]ScannedFile{},
		},
		Shards: []AnalysisShard{{
			ID:               "shard-01",
			Name:             "security_ioctl",
			PrimaryFiles:     []string{"driver/ioctl.cpp"},
			GraphFingerprint: "graph",
		}},
		Reports: []WorkerReport{{
			ShardID:      "shard-01",
			Title:        "ioctl",
			ScopeSummary: "ioctl",
		}},
		Reviews:       []ReviewDecision{{Status: "approved"}},
		FinalDocument: "# Analysis\n",
		GraphShards: AnalysisGraphShardArtifact{
			Shards: []AnalysisShard{{ID: "shard-01", Name: "security_ioctl"}},
		},
		GraphReuse: AnalysisGraphReuseReport{
			TotalShards: 1,
		},
		EvidenceGraph: AnalysisEvidenceGraph{
			Nodes: []AnalysisGraphNode{{ID: "file:driver/ioctl.cpp", Type: "file"}},
		},
		ClaimVerification: ClaimVerificationReport{
			Status:      "passed",
			TotalClaims: 1,
		},
		UnsupportedClaims: []UnsupportedClaim{{
			ClaimID: "claim-1",
			Claim:   "unsupported",
			Status:  "unsupported",
		}},
		SecurityOverlay: SecurityOverlaySummary{
			Nodes: []SecurityOverlayNode{{ID: "node", Type: "dispatcher"}},
			Edges: []SecurityOverlayEdge{{ID: "edge", SourceID: "node", TargetID: "node", Type: "input_reaches_dispatcher"}},
			Metrics: SecurityOverlayMetrics{
				NodeCount: 1,
				EdgeCount: 1,
			},
		},
	}
	output, err := analyzer.persistRun(run)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(filepath.Base(output), ".md")
	for _, rel := range []string{
		base + "_graph_shards.json",
		base + "_graph_reuse.json",
		base + "_evidence_graph.json",
		base + "_claim_verification.json",
		base + "_unsupported_claims.json",
		base + "_security_overlay.json",
		filepath.Join(base+"_docs", "EVIDENCE_GRAPH.md"),
		filepath.Join(base+"_docs", "SECURITY_OVERLAY.md"),
		filepath.Join(base+"_docs", "UNSUPPORTED_CLAIMS.md"),
		filepath.Join("latest", "graph_shards.json"),
		filepath.Join("latest", "graph_reuse.json"),
		filepath.Join("latest", "evidence_graph.json"),
		filepath.Join("latest", "claim_verification.json"),
		filepath.Join("latest", "unsupported_claims.json"),
		filepath.Join("latest", "security_overlay.json"),
		filepath.Join("latest", "docs", "EVIDENCE_GRAPH.md"),
		filepath.Join("latest", "docs", "SECURITY_OVERLAY.md"),
		filepath.Join("latest", "docs", "UNSUPPORTED_CLAIMS.md"),
	} {
		path := filepath.Join(analyzer.analysisCfg.OutputDir, rel)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected phase 5/6 artifact %s: %v", rel, err)
		}
	}
}

func phase56Snapshot(root string, files []ScannedFile) ProjectSnapshot {
	snapshot := ProjectSnapshot{
		Root:             root,
		GeneratedAt:      time.Now(),
		FilesByPath:      map[string]ScannedFile{},
		FilesByDirectory: map[string][]ScannedFile{},
	}
	for _, file := range files {
		file.Path = filepath.ToSlash(file.Path)
		snapshot.Files = append(snapshot.Files, file)
		snapshot.FilesByPath[file.Path] = file
		snapshot.FilesByDirectory[file.Directory] = append(snapshot.FilesByDirectory[file.Directory], file)
		snapshot.TotalFiles++
		snapshot.TotalLines += file.LineCount
	}
	return snapshot
}

func writePhase56TestFile(t *testing.T, root string, rel string, text string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func securityOverlayHasEdgeType(overlay SecurityOverlaySummary, edgeType string) bool {
	for _, edge := range overlay.Edges {
		if edge.Type == edgeType {
			return true
		}
	}
	return false
}

func securityOverlayHasEdgeSurface(overlay SecurityOverlaySummary, surface string) bool {
	for _, edge := range overlay.Edges {
		if edge.Surface == surface {
			return true
		}
	}
	return false
}

func securityOverlayHasNodeType(overlay SecurityOverlaySummary, nodeType string) bool {
	for _, node := range overlay.Nodes {
		if node.Type == nodeType {
			return true
		}
	}
	return false
}
