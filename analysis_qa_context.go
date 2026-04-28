package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type ProjectAnalysisQAIntent string

const (
	projectAnalysisQAIntentGeneral         ProjectAnalysisQAIntent = "general"
	projectAnalysisQAIntentDeepMap         ProjectAnalysisQAIntent = "deep_map"
	projectAnalysisQAIntentFlowTrace       ProjectAnalysisQAIntent = "flow_trace"
	projectAnalysisQAIntentModuleDrilldown ProjectAnalysisQAIntent = "module_drilldown"
	projectAnalysisQAIntentImpact          ProjectAnalysisQAIntent = "impact"
	projectAnalysisQAIntentSecuritySurface ProjectAnalysisQAIntent = "security_surface"
	projectAnalysisQAIntentUnrealStructure ProjectAnalysisQAIntent = "unreal_structure"
	projectAnalysisQAIntentBuildArtifact   ProjectAnalysisQAIntent = "build_artifact"
	projectAnalysisQAIntentVerification    ProjectAnalysisQAIntent = "verification"
)

type ProjectStructureAnswerPack struct {
	Intent              ProjectAnalysisQAIntent
	Confidence          string
	Summary             string
	DomainHints         []string
	ArchitectureFacts   ArchitectureFactPack
	CriticalAnchors     []ProjectStructureCriticalAnchor
	DomainFlows         []string
	RelevantDocs        []ProjectStructureDocHit
	Modules             []DeveloperModuleRecord
	Folders             []DeveloperFolderRecord
	Files               []FileRecord
	Symbols             []SymbolRecord
	CallPaths           []SemanticPathV2
	BuildContexts       []BuildContextRecord
	GraphViews          []ProjectStructureGraphView
	UnrealEdges         []UnrealSemanticEdge
	SecurityOverlays    []OverlayEdge
	VerificationEntries []AnalysisVerificationMatrixEntry
	FuzzTargets         []AnalysisFuzzTargetCatalogEntry
	StaleMarkers        []string
	SourceAnchors       []string
	SuggestedReads      []string
	CurrentSourceNeeded bool
}

type ProjectStructureCriticalAnchor struct {
	Role             string
	Name             string
	Kind             string
	File             string
	Line             int
	Tags             []string
	Why              string
	VerificationHint string
	Score            int
	KernelSide       bool
	UserModeSide     bool
}

type ProjectStructureDocHit struct {
	DocName       string
	Title         string
	SectionID     string
	SectionTitle  string
	Path          string
	Text          string
	SourceAnchors []string
	StaleMarkers  []string
	ReuseTargets  []string
	QueryIntents  []string
	EntityRefs    []string
	GraphRefs     []string
	Priority      int
	Confidence    string
	Score         int
}

type ProjectStructureGraphView struct {
	Title             string
	Kind              string
	Nodes             []string
	Edges             []analysisGraphEdgeView
	SourceAnchors     []string
	Evidence          []string
	Confidence        string
	RecommendedDocs   []string
	VerificationHints []string
}

func classifyProjectAnalysisQAIntent(query string) ProjectAnalysisQAIntent {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return projectAnalysisQAIntentGeneral
	}
	if containsAny(lower,
		"전체 구조", "전체 아키텍처", "프로젝트 구조", "프로젝트 전체", "overall structure", "project structure", "architecture overview") &&
		!containsAny(lower,
			"security surface", "trust boundary", "ioctl", "handle", "memory read", "memory write", "tamper", "fuzz",
			"unreal", "ue5", "uclass", "replication", "blueprint", "build", "artifact", "compile", "package", "signing", "verify", "verification", "regression",
			"보안", "보안 표면", "신뢰 경계", "핸들", "메모리 읽", "메모리 쓰", "탬퍼", "퍼즈",
			"언리얼", "리플리케이션", "블루프린트", "빌드", "아티팩트", "컴파일", "패키징", "서명", "검증", "테스트", "회귀") {
		return projectAnalysisQAIntentDeepMap
	}
	switch {
	case containsAny(lower,
		"unreal", "ue5", "uclass", "ustruct", "ufunction", "uproperty", "replication", "replicated", "blueprint", "gameplay ability", "gas", "asset", "config coupling",
		"언리얼", "리플리케이션", "블루프린트", "어빌리티", "에셋", "설정 결합"):
		return projectAnalysisQAIntentUnrealStructure
	case containsAny(lower,
		"security", "surface", "anti-cheat", "anti cheat", "tamper", "integrity", "trust boundary", "authority", "ioctl", "rpc validation", "handle", "memory read", "memory write",
		"보안", "표면", "안티치트", "안티 치트", "무결성", "탬퍼", "신뢰 경계", "권한", "핸들", "메모리 읽", "메모리 쓰"):
		return projectAnalysisQAIntentSecuritySurface
	case containsAny(lower,
		"impact", "affected", "affect", "blast radius", "dependency", "what breaks", "if i change", "if we change",
		"영향", "영향도", "파급", "의존성", "변경하면", "바꾸면", "어디가 깨", "어디까지"):
		return projectAnalysisQAIntentImpact
	case containsAny(lower,
		"verify", "verification", "test plan", "regression", "evidence", "checklist",
		"검증", "테스트", "회귀", "증거", "체크리스트"):
		return projectAnalysisQAIntentVerification
	case containsAny(lower,
		"build", "artifact", "compile", "package", "signing", "target", "generated", "compile_commands",
		"빌드", "아티팩트", "컴파일", "패키징", "서명", "타겟", "생성물"):
		return projectAnalysisQAIntentBuildArtifact
	case containsAny(lower,
		"trace", "flow", "path", "caller", "callee", "call chain", "execution chain", "startup", "dispatch", "request path",
		"트레이스", "흐름", "경로", "호출", "콜체인", "실행 순서", "초기화", "디스패치"):
		return projectAnalysisQAIntentFlowTrace
	case containsAny(lower,
		"module", "folder", "directory", "component", "boundary", "ownership", "subsystem",
		"모듈", "폴더", "디렉토리", "컴포넌트", "경계", "오너십", "서브시스템"):
		return projectAnalysisQAIntentModuleDrilldown
	case containsAny(lower,
		"deep", "detail", "detailed", "architecture", "structure", "project map", "codebase map", "overall", "end to end",
		"깊", "자세", "상세", "아키텍처", "구조", "전체", "큰 그림", "끝까지"):
		return projectAnalysisQAIntentDeepMap
	default:
		return projectAnalysisQAIntentGeneral
	}
}

func projectAnalysisQAIntentMode(intent ProjectAnalysisQAIntent) string {
	switch intent {
	case projectAnalysisQAIntentFlowTrace:
		return "trace"
	case projectAnalysisQAIntentImpact:
		return "impact"
	case projectAnalysisQAIntentSecuritySurface, projectAnalysisQAIntentUnrealStructure:
		return "security"
	case projectAnalysisQAIntentBuildArtifact, projectAnalysisQAIntentModuleDrilldown, projectAnalysisQAIntentDeepMap:
		return "map"
	default:
		return "map"
	}
}

func projectAnalysisQAIntentNeedsAnswerPack(intent ProjectAnalysisQAIntent) bool {
	switch intent {
	case projectAnalysisQAIntentDeepMap,
		projectAnalysisQAIntentFlowTrace,
		projectAnalysisQAIntentModuleDrilldown,
		projectAnalysisQAIntentImpact,
		projectAnalysisQAIntentSecuritySurface,
		projectAnalysisQAIntentUnrealStructure,
		projectAnalysisQAIntentBuildArtifact,
		projectAnalysisQAIntentVerification:
		return true
	default:
		return false
	}
}

func buildProjectStructureAnswerPack(artifacts latestAnalysisArtifacts, query string) ProjectStructureAnswerPack {
	intent := classifyProjectAnalysisQAIntent(query)
	run := projectAnalysisRunFromLatestArtifacts(artifacts)
	hits := collectRelevantSemanticIndexV2HitsForQA(artifacts.IndexV2, query, intent)
	docLimit := projectAnalysisDocLimitForIntent(intent)
	modLimit := projectAnalysisModuleLimitForIntent(intent)
	folderLimit := projectAnalysisFolderLimitForIntent(intent)
	pack := ProjectStructureAnswerPack{
		Intent:            intent,
		Summary:           firstNonBlankAnalysisString(artifacts.Pack.ProjectSummary, artifacts.Pack.Goal),
		DomainHints:       projectStructureDomainHints(run),
		ArchitectureFacts: firstArchitectureFactPack(artifacts.Snapshot.ArchitectureFacts, artifacts.Pack.ArchitectureFacts),
		RelevantDocs:      selectProjectStructureDocHits(artifacts.Corpus, artifacts.DocsManifest, query, intent, docLimit),
		Modules:           limitDeveloperModuleRecords(buildDeveloperModuleRecords(run), modLimit),
		Folders:           limitDeveloperFolderRecords(buildDeveloperFolderRecords(run), folderLimit),
		Files:             hits.Files,
		Symbols:           hits.Symbols,
		CallPaths:         hits.Paths,
		BuildContexts: append(append([]BuildContextRecord(nil), hits.BuildContexts...),
			selectRelevantBuildContextsFromManifest(run.Snapshot.BuildContexts, query, intent, 4)...),
		GraphViews:          buildProjectStructureGraphViews(run, hits, intent),
		UnrealEdges:         selectRelevantUnrealEdges(artifacts.UnrealGraph, query, intent, projectAnalysisUnrealEdgeLimitForIntent(intent)),
		SecurityOverlays:    hits.Overlays,
		VerificationEntries: selectRelevantVerificationEntries(artifacts.DocsManifest.VerificationMatrix, query, intent, projectAnalysisVerificationLimitForIntent(intent)),
		FuzzTargets:         selectRelevantFuzzTargets(artifacts.DocsManifest.FuzzTargets, query, intent, projectAnalysisFuzzLimitForIntent(intent)),
	}
	pack.CriticalAnchors = selectProjectStructureCriticalAnchors(run, hits, intent, 18)
	pack.DomainFlows = projectStructureDomainFlows(pack)
	pack.SourceAnchors = projectStructureAnswerPackSourceAnchors(pack)
	pack.StaleMarkers = projectStructureAnswerPackStaleMarkers(pack)
	pack.SuggestedReads = projectStructureAnswerPackSuggestedReads(pack)
	pack.Confidence = projectStructureAnswerPackConfidence(pack)
	pack.CurrentSourceNeeded = projectStructureAnswerPackNeedsCurrentSource(pack)
	return pack
}

func projectAnalysisRunFromLatestArtifacts(artifacts latestAnalysisArtifacts) ProjectAnalysisRun {
	return ProjectAnalysisRun{
		Summary: ProjectAnalysisSummary{
			RunID: latestAnalysisArtifactsRunID(artifacts),
			Goal:  firstNonBlankProjectStructureString(artifacts.Pack.Goal, artifacts.Corpus.Goal, artifacts.Index.Goal, artifacts.IndexV2.Goal),
			Mode:  projectAnalysisModeFromArtifacts(artifacts),
		},
		Snapshot:        artifacts.Snapshot,
		KnowledgePack:   artifacts.Pack,
		SemanticIndex:   artifacts.Index,
		SemanticIndexV2: artifacts.IndexV2,
		UnrealGraph:     artifacts.UnrealGraph,
		VectorCorpus:    artifacts.Corpus,
	}
}

func projectAnalysisModeFromArtifacts(artifacts latestAnalysisArtifacts) string {
	if strings.TrimSpace(artifacts.DocsManifest.Mode) != "" {
		return artifacts.DocsManifest.Mode
	}
	if strings.TrimSpace(artifacts.Snapshot.AnalysisMode) != "" {
		return artifacts.Snapshot.AnalysisMode
	}
	return "map"
}

func renderProjectStructureAnswerPack(pack ProjectStructureAnswerPack, maxChars int) string {
	if !projectAnalysisQAIntentNeedsAnswerPack(pack.Intent) {
		return ""
	}
	if len(pack.RelevantDocs) == 0 &&
		len(pack.Modules) == 0 &&
		len(pack.Folders) == 0 &&
		len(pack.Files) == 0 &&
		len(pack.Symbols) == 0 &&
		!architectureFactPackHasData(pack.ArchitectureFacts) &&
		len(pack.CriticalAnchors) == 0 &&
		len(pack.DomainFlows) == 0 &&
		len(pack.GraphViews) == 0 &&
		len(pack.CallPaths) == 0 &&
		len(pack.BuildContexts) == 0 &&
		len(pack.UnrealEdges) == 0 &&
		len(pack.SecurityOverlays) == 0 &&
		len(pack.VerificationEntries) == 0 &&
		len(pack.FuzzTargets) == 0 {
		return ""
	}
	var b strings.Builder
	securityFirst := pack.Intent == projectAnalysisQAIntentSecuritySurface || pack.Intent == projectAnalysisQAIntentImpact
	b.WriteString("Project structure answer pack:\n")
	fmt.Fprintf(&b, "- intent: %s\n", pack.Intent)
	fmt.Fprintf(&b, "- confidence: %s\n", firstNonBlankAnalysisString(pack.Confidence, "low"))
	if len(pack.DomainHints) > 0 {
		fmt.Fprintf(&b, "- domain_hints: %s\n", strings.Join(limitStrings(pack.DomainHints, 5), ", "))
	}
	if factText := renderArchitectureFactPackForPrompt(pack.ArchitectureFacts, AnalysisShard{}, 4200); strings.TrimSpace(factText) != "" {
		b.WriteString("\n")
		b.WriteString(factText)
		b.WriteString("\n")
	}
	if strings.TrimSpace(pack.Summary) != "" {
		fmt.Fprintf(&b, "- analysis_summary: %s\n", compactProjectAnalysisText(pack.Summary, 520))
	}
	if pack.CurrentSourceNeeded {
		b.WriteString("- current_source_needed: true\n")
	}
	if len(pack.SuggestedReads) > 0 {
		fmt.Fprintf(&b, "- suggested_reads: %s\n", strings.Join(limitStrings(pack.SuggestedReads, 8), "; "))
	}
	b.WriteString("\nAnswer contract: cover the structure layers, every relevant Domain-specific flow map spine, key source anchors, impact or verification points, stale caveats when markers are present, and next docs/files to read. If stale markers are absent, say the cached analysis did not report stale markers.\n")
	if analysisContainsStringCI(pack.DomainHints, "windows_driver") {
		b.WriteString("Driver terminology rule: describe this as a Windows kernel/WDM .sys driver, not a DLL, unless a source artifact explicitly says DLL.\n")
		b.WriteString("IOCTL ownership rule: separate user-mode control/client wrappers from kernel-side IRP/IOCTL dispatch and validation.\n")
		if !securityFirst {
			b.WriteString("Windows driver evidence rule: use the flow map as constrained architecture evidence; keep create/open validation, DeviceIoControl command dispatch, process notify, object callbacks, and teardown separate.\n")
			b.WriteString("Driver flow guardrail: do not place runtime filter start/registration functions in DriverEntry/Core Initialize unless direct evidence says so. Keep request-origin validation outside the DeviceIoControl command spine unless a call edge proves otherwise.\n")
			b.WriteString("Driver structure completeness rule: include both the device-control branch spine and the REQUIRED device-control command spine; spell out exact command symbols.\n")
			b.WriteString("Folder placement rule: root folders are siblings. Do not nest one root folder under another unless the path explicitly says so. Do not turn *.h/*.cpp/*.vcxproj/*.sln entries into root folders. Do not add *.h/*.cpp/*.vcxproj/*.sln entries as root directories; mention files only in file/source sections.\n")
			b.WriteString("Anchor labeling rule: use exact symbol names and exact file:line anchors; never replace known line numbers with ellipsis. Control PID/accessor symbols are not Finalize/Unload lifecycle functions.\n")
		}
	}
	if len(pack.StaleMarkers) > 0 {
		fmt.Fprintf(&b, "\nStale or invalidation markers: %s\n", strings.Join(limitStrings(pack.StaleMarkers, 8), "; "))
	}
	if !securityFirst {
		if facts := projectStructureDriverRequiredFacts(pack); len(facts) > 0 {
			b.WriteString("\nRequired driver answer facts:\n")
			for _, fact := range facts {
				fmt.Fprintf(&b, "- %s\n", fact)
			}
		}
	}
	if !securityFirst && analysisContainsStringCI(pack.DomainHints, "windows_driver") && len(pack.Folders) > 0 {
		b.WriteString("\nRoot folder map (exact sibling paths):\n")
		for _, folder := range limitDeveloperFolderRecords(projectStructureRootFolders(pack.Folders), 6) {
			fmt.Fprintf(&b, "- %s/ responsibility=%s\n",
				strings.TrimSuffix(folder.Path, "/"),
				compactProjectAnalysisText(folder.Responsibility, 130))
		}
	}
	if securityFirst && len(pack.SecurityOverlays) > 0 {
		b.WriteString("\nSecurity overlays:\n")
		for _, edge := range limitOverlayEdges(pack.SecurityOverlays, 8) {
			fmt.Fprintf(&b, "- %s: %s -> %s [%s] evidence=%s\n",
				edge.Domain,
				edge.SourceID,
				edge.TargetID,
				edge.Type,
				strings.Join(limitStrings(edge.Evidence, 3), "; "))
		}
	}
	if securityFirst && (len(pack.VerificationEntries) > 0 || len(pack.FuzzTargets) > 0) {
		b.WriteString("\nVerification and fuzz follow-through:\n")
		for _, entry := range limitVerificationEntries(pack.VerificationEntries, 6) {
			fmt.Fprintf(&b, "- verify %s: %s optional=%s\n", entry.ChangeArea, entry.RequiredVerification, entry.OptionalVerification)
		}
		for _, target := range limitAnalysisFuzzTargetCatalog(pack.FuzzTargets, 5) {
			fmt.Fprintf(&b, "- fuzz_target %s file=%s surface=%s score=%d\n", target.Name, target.File, target.InputSurfaceKind, target.PriorityScore)
		}
	}
	if len(pack.CriticalAnchors) > 0 {
		b.WriteString("\nVerified critical source anchors (role -> exact symbol -> file:line):\n")
		for _, anchor := range limitProjectStructureCriticalAnchors(pack.CriticalAnchors, 18) {
			location := projectStructureCriticalAnchorLocation(anchor)
			side := "source"
			if anchor.KernelSide {
				side = "kernel"
			} else if anchor.UserModeSide {
				side = "user"
			}
			fmt.Fprintf(&b, "- %s: %s file=%s side=%s kind=%s\n",
				anchor.Role,
				firstNonBlankProjectStructureString(anchor.Name, anchor.Kind),
				location,
				side,
				firstNonBlankAnalysisString(anchor.Kind, "symbol"))
		}
	}
	if !securityFirst && len(pack.DomainFlows) > 0 {
		b.WriteString("\nDomain-specific flow map:\n")
		for _, flow := range limitStrings(pack.DomainFlows, 10) {
			fmt.Fprintf(&b, "- %s\n", flow)
		}
	}
	if len(pack.SourceAnchors) > 0 {
		note := "Source anchors summary"
		if analysisContainsStringCI(pack.DomainHints, "windows_driver") {
			note += " (unlabeled; prefer verified critical anchors section for exact labels)"
		}
		fmt.Fprintf(&b, "\n%s: %s\n", note, strings.Join(limitStrings(pack.SourceAnchors, 8), "; "))
	}
	if len(pack.RelevantDocs) > 0 {
		b.WriteString("\nPriority docs and sections:\n")
		for _, doc := range limitProjectStructureDocHits(pack.RelevantDocs, 6) {
			label := firstNonBlankProjectStructureString(doc.SectionTitle, doc.Title, doc.DocName)
			fmt.Fprintf(&b, "- %s path=latest/docs/%s", label, firstNonBlankAnalysisString(doc.Path, doc.DocName))
			if strings.TrimSpace(doc.SectionID) != "" {
				fmt.Fprintf(&b, " section=%s", doc.SectionID)
			}
			b.WriteString("\n")
		}
	}
	if securityFirst && len(pack.DomainFlows) > 0 {
		b.WriteString("\nDomain-specific flow map:\n")
		for _, flow := range limitStrings(pack.DomainFlows, 10) {
			fmt.Fprintf(&b, "- %s\n", flow)
		}
	}
	if len(pack.GraphViews) > 0 {
		b.WriteString("\nGraph views:\n")
		for _, view := range limitProjectStructureGraphViews(pack.GraphViews, 3) {
			fmt.Fprintf(&b, "- %s kind=%s confidence=%s\n", view.Title, view.Kind, firstNonBlankAnalysisString(view.Confidence, "medium"))
			for _, edge := range limitAnalysisGraphEdgeViews(view.Edges, 2) {
				fmt.Fprintf(&b, "  edge: %s -> %s [%s] evidence=%s\n",
					edge.Source,
					edge.Target,
					firstNonBlankAnalysisString(edge.Flow, edge.Type),
					compactProjectAnalysisText(edge.Evidence, 80))
			}
			if len(view.SourceAnchors) > 0 {
				fmt.Fprintf(&b, "  anchors: %s\n", strings.Join(limitStrings(view.SourceAnchors, 3), "; "))
			}
		}
	}
	if !securityFirst && len(pack.SecurityOverlays) > 0 {
		b.WriteString("\nSecurity overlays:\n")
		for _, edge := range limitOverlayEdges(pack.SecurityOverlays, 8) {
			fmt.Fprintf(&b, "- %s: %s -> %s [%s] evidence=%s\n",
				edge.Domain,
				edge.SourceID,
				edge.TargetID,
				edge.Type,
				strings.Join(limitStrings(edge.Evidence, 3), "; "))
		}
	}
	if !securityFirst && (len(pack.VerificationEntries) > 0 || len(pack.FuzzTargets) > 0) {
		b.WriteString("\nVerification and fuzz follow-through:\n")
		for _, entry := range limitVerificationEntries(pack.VerificationEntries, 6) {
			fmt.Fprintf(&b, "- verify %s: %s optional=%s\n", entry.ChangeArea, entry.RequiredVerification, entry.OptionalVerification)
		}
		for _, target := range limitAnalysisFuzzTargetCatalog(pack.FuzzTargets, 5) {
			fmt.Fprintf(&b, "- fuzz_target %s file=%s surface=%s score=%d\n", target.Name, target.File, target.InputSurfaceKind, target.PriorityScore)
		}
	}
	if len(pack.CriticalAnchors) > 0 {
		b.WriteString("\nCritical anchor verification hints:\n")
		for _, anchor := range limitProjectStructureCriticalAnchors(pack.CriticalAnchors, 12) {
			fmt.Fprintf(&b, "- %s: why=%s", anchor.Role, compactProjectAnalysisText(anchor.Why, 100))
			if strings.TrimSpace(anchor.VerificationHint) != "" {
				fmt.Fprintf(&b, " verify=%s", compactProjectAnalysisText(anchor.VerificationHint, 100))
			}
			b.WriteString("\n")
		}
	}
	if len(pack.SourceAnchors) > 0 {
		header := "Additional Source anchors"
		if analysisContainsStringCI(pack.DomainHints, "windows_driver") {
			header += " (unlabeled; do not use these to rename file:line anchors)"
		}
		fmt.Fprintf(&b, "\n%s: %s\n", header, strings.Join(limitStrings(pack.SourceAnchors, 18), "; "))
	}
	if len(pack.UnrealEdges) > 0 {
		b.WriteString("\nUnreal semantic edges:\n")
		for _, edge := range limitUnrealSemanticEdges(pack.UnrealEdges, 8) {
			fmt.Fprintf(&b, "- %s -> %s [%s]\n", edge.Source, edge.Target, edge.Type)
		}
	}
	if len(pack.Modules) > 0 {
		b.WriteString("\nModule structure:\n")
		for _, module := range limitDeveloperModuleRecords(pack.Modules, 8) {
			fmt.Fprintf(&b, "- %s kind=%s root=%s responsibility=%s\n",
				module.Name,
				firstNonBlankAnalysisString(module.Kind, "module"),
				firstNonBlankAnalysisString(module.Root, "."),
				compactProjectAnalysisText(module.Responsibility, 180))
			if len(module.Entrypoints) > 0 {
				fmt.Fprintf(&b, "  entrypoints: %s\n", strings.Join(limitStrings(module.Entrypoints, 4), "; "))
			}
			if len(module.Dependencies) > 0 {
				fmt.Fprintf(&b, "  dependencies: %s\n", strings.Join(limitStrings(module.Dependencies, 5), "; "))
			}
			if len(module.SourceAnchors) > 0 {
				fmt.Fprintf(&b, "  anchors: %s\n", strings.Join(limitStrings(module.SourceAnchors, 5), "; "))
			}
		}
	}
	if len(pack.Folders) > 0 {
		b.WriteString("\nFolder structure:\n")
		for _, folder := range limitDeveloperFolderRecords(pack.Folders, 8) {
			fmt.Fprintf(&b, "- %s responsibility=%s confidence=%s\n",
				folder.Path,
				compactProjectAnalysisText(folder.Responsibility, 180),
				firstNonBlankAnalysisString(folder.Confidence, "medium"))
			if len(folder.KeyFiles) > 0 {
				fmt.Fprintf(&b, "  key_files: %s\n", strings.Join(limitStrings(folder.KeyFiles, 4), "; "))
			}
			if len(folder.RiskSignals) > 0 {
				fmt.Fprintf(&b, "  risk: %s\n", strings.Join(limitStrings(folder.RiskSignals, 4), "; "))
			}
		}
	}
	if len(pack.CallPaths) > 0 {
		b.WriteString("\nCall or ownership paths:\n")
		for _, path := range limitSemanticPaths(pack.CallPaths, 3) {
			fmt.Fprintf(&b, "- %s reason=%s score=%d\n", strings.Join(path.Nodes, " -> "), path.Reason, path.Score)
		}
	}
	if len(pack.Symbols) > 0 || len(pack.Files) > 0 || len(pack.BuildContexts) > 0 {
		b.WriteString("\nIndexed anchors:\n")
		for _, file := range limitFileRecords(pack.Files, 8) {
			fmt.Fprintf(&b, "- file: %s score=%d modules=%s\n", file.Path, file.ImportanceScore, strings.Join(limitStrings(file.ModuleHints, 3), ","))
		}
		for _, symbol := range limitSymbolRecords(pack.Symbols, 8) {
			location := symbol.File
			if symbol.StartLine > 0 {
				location = fmt.Sprintf("%s:%d", location, symbol.StartLine)
			}
			fmt.Fprintf(&b, "- symbol: %s (%s) file=%s ctx=%s\n",
				firstNonBlankProjectStructureString(symbol.CanonicalName, symbol.Name, symbol.ID),
				symbol.Kind,
				location,
				symbol.BuildContextID)
		}
		for _, ctx := range limitBuildContexts(pack.BuildContexts, 5) {
			fmt.Fprintf(&b, "- build_context: %s kind=%s module=%s dir=%s files=%d\n",
				firstNonBlankAnalysisString(ctx.Name, ctx.ID),
				ctx.Kind,
				ctx.Module,
				ctx.Directory,
				len(ctx.Files))
		}
	}
	if len(pack.RelevantDocs) > 0 {
		b.WriteString("\nDocs and sections to use first:\n")
		for _, doc := range limitProjectStructureDocHits(pack.RelevantDocs, 10) {
			label := firstNonBlankProjectStructureString(doc.SectionTitle, doc.Title, doc.DocName)
			fmt.Fprintf(&b, "- %s path=latest/docs/%s", label, firstNonBlankAnalysisString(doc.Path, doc.DocName))
			if strings.TrimSpace(doc.SectionID) != "" {
				fmt.Fprintf(&b, " section=%s", doc.SectionID)
			}
			if strings.TrimSpace(doc.Confidence) != "" {
				fmt.Fprintf(&b, " confidence=%s", doc.Confidence)
			}
			b.WriteString("\n")
			if len(doc.SourceAnchors) > 0 {
				fmt.Fprintf(&b, "  anchors: %s\n", strings.Join(limitStrings(doc.SourceAnchors, 5), "; "))
			}
			if len(doc.StaleMarkers) > 0 {
				fmt.Fprintf(&b, "  stale: %s\n", strings.Join(limitStrings(doc.StaleMarkers, 3), "; "))
			}
			if strings.TrimSpace(doc.Text) != "" {
				fmt.Fprintf(&b, "  excerpt: %s\n", compactProjectAnalysisText(doc.Text, 220))
			}
		}
	}
	return compactProjectAnalysisText(strings.TrimSpace(b.String()), maxChars)
}

func collectRelevantSemanticIndexV2HitsForQA(index SemanticIndexV2, query string, intent ProjectAnalysisQAIntent) relevantSemanticIndexV2Hits {
	if !hasSemanticIndexV2Data(index) {
		return relevantSemanticIndexV2Hits{Mode: projectAnalysisQAIntentMode(intent)}
	}
	mode := projectAnalysisQAIntentMode(intent)
	hits := relevantSemanticIndexV2Hits{Mode: mode}
	switch intent {
	case projectAnalysisQAIntentSecuritySurface:
		hits.Files = selectRelevantV2Files(index, query, mode, 6)
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 10)
		hits.Overlays = selectRelevantV2OverlayEdges(index, query, mode, 12)
		hits.Calls = selectRelevantV2CallEdges(index, query, mode, 8)
		hits.References = selectRelevantV2References(index, query, mode, 8)
	case projectAnalysisQAIntentFlowTrace:
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 10)
		hits.Calls = selectRelevantV2CallEdges(index, query, mode, 12)
		hits.Inheritance = selectRelevantV2InheritanceEdges(index, query, mode, 6)
		hits.Builds = selectRelevantV2BuildEdges(index, query, mode, 8)
		hits.Occurrences = selectRelevantV2Occurrences(index, query, mode, 8)
	case projectAnalysisQAIntentImpact:
		hits.Files = selectRelevantV2Files(index, query, mode, 8)
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 8)
		hits.Builds = selectRelevantV2BuildEdges(index, query, mode, 8)
		hits.References = selectRelevantV2References(index, query, mode, 12)
		hits.Occurrences = selectRelevantV2Occurrences(index, query, mode, 12)
		hits.Overlays = selectRelevantV2OverlayEdges(index, query, mode, 8)
	case projectAnalysisQAIntentBuildArtifact:
		hits.Files = selectRelevantV2Files(index, query, mode, 8)
		hits.BuildContexts = selectRelevantV2BuildContexts(index, query, mode, 8)
		hits.Builds = selectRelevantV2BuildEdges(index, query, mode, 12)
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 6)
	case projectAnalysisQAIntentUnrealStructure:
		hits.Files = selectRelevantV2Files(index, query, mode, 8)
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 10)
		hits.BuildContexts = selectRelevantV2BuildContexts(index, query, mode, 8)
		hits.Overlays = selectRelevantV2OverlayEdges(index, query, mode, 10)
		hits.Calls = selectRelevantV2CallEdges(index, query, mode, 8)
	default:
		hits.Files = selectRelevantV2Files(index, query, mode, 8)
		hits.Symbols = selectRelevantV2Symbols(index, query, mode, 10)
		hits.BuildContexts = selectRelevantV2BuildContexts(index, query, mode, 8)
		hits.Builds = selectRelevantV2BuildEdges(index, query, mode, 10)
		hits.References = selectRelevantV2References(index, query, mode, 8)
		hits.Calls = selectRelevantV2CallEdges(index, query, mode, 8)
	}
	return expandRelevantSemanticIndexV2Hits(index, hits, query)
}

func selectProjectStructureDocHits(corpus VectorCorpus, manifest AnalysisDocsManifest, query string, intent ProjectAnalysisQAIntent, limit int) []ProjectStructureDocHit {
	if limit <= 0 {
		return nil
	}
	docByName := map[string]AnalysisGeneratedDoc{}
	for _, doc := range manifest.Documents {
		docByName[strings.ToUpper(strings.TrimSpace(doc.Name))] = doc
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	items := []ProjectStructureDocHit{}
	for _, doc := range manifest.Documents {
		score := scoreAnalysisDocForQA(doc.Name, doc.Title, doc.Kind, doc.SourceAnchors, doc.StaleMarkers, doc.ReuseTargets, doc.QueryIntents, doc.Priority, loweredQuery, queryTokens, queryRefs, intent)
		if score > 0 {
			items = append(items, ProjectStructureDocHit{
				DocName:       doc.Name,
				Title:         doc.Title,
				Path:          doc.Path,
				SourceAnchors: analysisDocSlashPaths(doc.SourceAnchors),
				StaleMarkers:  doc.StaleMarkers,
				ReuseTargets:  doc.ReuseTargets,
				QueryIntents:  doc.QueryIntents,
				Priority:      doc.Priority,
				Confidence:    doc.Confidence,
				Score:         score,
			})
		}
		for _, section := range doc.Sections {
			score := scoreAnalysisDocForQA(doc.Name, doc.Title+" "+section.Title, doc.Kind, section.SourceAnchors, section.StaleMarkers, section.ReuseTargets, section.QueryIntents, section.Priority, loweredQuery, queryTokens, queryRefs, intent)
			if score <= 0 {
				continue
			}
			items = append(items, ProjectStructureDocHit{
				DocName:       doc.Name,
				Title:         doc.Title,
				SectionID:     section.ID,
				SectionTitle:  section.Title,
				Path:          doc.Path,
				SourceAnchors: analysisDocSlashPaths(section.SourceAnchors),
				StaleMarkers:  section.StaleMarkers,
				ReuseTargets:  section.ReuseTargets,
				QueryIntents:  section.QueryIntents,
				EntityRefs:    section.EntityRefs,
				GraphRefs:     section.GraphRefs,
				Priority:      section.Priority,
				Confidence:    firstNonBlankAnalysisString(section.Confidence, doc.Confidence),
				Score:         score + 2,
			})
		}
	}
	for _, doc := range corpus.Documents {
		score := scoreVectorDocumentForQA(doc, loweredQuery, queryTokens, queryRefs, intent)
		if score <= 0 {
			continue
		}
		docName := firstNonBlankProjectStructureString(doc.Metadata["doc_name"], doc.Metadata["doc_path"], doc.PathHint)
		metaDoc := docByName[strings.ToUpper(strings.TrimSpace(docName))]
		items = append(items, ProjectStructureDocHit{
			DocName:       firstNonBlankAnalysisString(docName, doc.PathHint),
			Title:         firstNonBlankAnalysisString(metaDoc.Title, doc.Title),
			SectionID:     doc.Metadata["section_id"],
			SectionTitle:  doc.Metadata["section_title"],
			Path:          firstNonBlankProjectStructureString(metaDoc.Path, doc.Metadata["doc_path"], doc.PathHint),
			Text:          doc.Text,
			SourceAnchors: splitAnalysisMetadataList(doc.Metadata["source_anchors"]),
			StaleMarkers:  splitAnalysisMetadataList(doc.Metadata["stale_markers"]),
			ReuseTargets:  splitAnalysisMetadataList(doc.Metadata["reuse_targets"]),
			QueryIntents:  splitAnalysisMetadataList(doc.Metadata["query_intents"]),
			EntityRefs:    splitAnalysisMetadataList(doc.Metadata["entity_refs"]),
			GraphRefs:     splitAnalysisMetadataList(doc.Metadata["graph_refs"]),
			Priority:      parseAnalysisMetadataInt(doc.Metadata["priority"]),
			Confidence:    firstNonBlankAnalysisString(doc.Metadata["confidence"], metaDoc.Confidence),
			Score:         score + 3,
		})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].Score == items[j].Score {
			return projectStructureDocHitKey(items[i]) < projectStructureDocHitKey(items[j])
		}
		return items[i].Score > items[j].Score
	})
	items = uniqueProjectStructureDocHits(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func scoreAnalysisDocForQA(name string, title string, kind string, anchors []string, stale []string, reuse []string, intents []string, priority int, loweredQuery string, queryTokens []string, queryRefs []string, intent ProjectAnalysisQAIntent) int {
	haystacks := []string{
		strings.ToLower(name),
		strings.ToLower(title),
		strings.ToLower(kind),
		strings.ToLower(strings.Join(anchors, " ")),
		strings.ToLower(strings.Join(stale, " ")),
		strings.ToLower(strings.Join(reuse, " ")),
		strings.ToLower(strings.Join(intents, " ")),
	}
	score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
	if score == 0 && projectAnalysisQAIntentNeedsAnswerPack(intent) {
		score = 1
	}
	score += priority
	if analysisContainsStringCI(intents, string(intent)) {
		score += 12
	}
	for _, preferred := range preferredAnalysisDocsForIntent(intent) {
		if strings.EqualFold(name, preferred) {
			score += 10
		}
	}
	return score
}

func scoreVectorDocumentForQA(item VectorCorpusDocument, loweredQuery string, queryTokens []string, queryRefs []string, intent ProjectAnalysisQAIntent) int {
	score := scoreVectorDocument(item, loweredQuery, queryTokens, queryRefs)
	docName := firstNonBlankProjectStructureString(item.Metadata["doc_name"], item.Metadata["doc_path"], item.PathHint)
	queryIntents := splitAnalysisMetadataList(item.Metadata["query_intents"])
	if analysisContainsStringCI(queryIntents, string(intent)) {
		score += 12
	}
	for _, preferred := range preferredAnalysisDocsForIntent(intent) {
		if strings.EqualFold(docName, preferred) {
			score += 10
		}
	}
	if item.Kind == "generated_doc_section" {
		score += 3
	}
	score += parseAnalysisMetadataInt(item.Metadata["priority"])
	return score
}

func preferredAnalysisDocsForIntent(intent ProjectAnalysisQAIntent) []string {
	switch intent {
	case projectAnalysisQAIntentDeepMap:
		return []string{"DEVELOPER_OVERVIEW.md", "MODULES.md", "STRUCTURE_DIAGRAMS.md", "CODE_STRUCTURE_REFERENCE.md", "ARCHITECTURE.md"}
	case projectAnalysisQAIntentFlowTrace:
		return []string{"STRUCTURE_DIAGRAMS.md", "CODE_STRUCTURE_REFERENCE.md", "API_AND_ENTRYPOINTS.md", "ARCHITECTURE.md", "VERIFICATION_MATRIX.md"}
	case projectAnalysisQAIntentModuleDrilldown:
		return []string{"MODULES.md", "FOLDER_MAP.md", "CODE_STRUCTURE_REFERENCE.md", "BUILD_AND_ARTIFACTS.md"}
	case projectAnalysisQAIntentImpact:
		return []string{"CODE_STRUCTURE_REFERENCE.md", "BUILD_AND_ARTIFACTS.md", "VERIFICATION_MATRIX.md", "STRUCTURE_DIAGRAMS.md"}
	case projectAnalysisQAIntentSecuritySurface:
		return []string{"SECURITY_SURFACE.md", "API_AND_ENTRYPOINTS.md", "FUZZ_TARGETS.md", "VERIFICATION_MATRIX.md", "STRUCTURE_DIAGRAMS.md"}
	case projectAnalysisQAIntentUnrealStructure:
		return []string{"MODULES.md", "STRUCTURE_DIAGRAMS.md", "SECURITY_SURFACE.md", "BUILD_AND_ARTIFACTS.md", "CODE_STRUCTURE_REFERENCE.md"}
	case projectAnalysisQAIntentBuildArtifact:
		return []string{"BUILD_AND_ARTIFACTS.md", "MODULES.md", "CODE_STRUCTURE_REFERENCE.md", "STRUCTURE_DIAGRAMS.md"}
	case projectAnalysisQAIntentVerification:
		return []string{"VERIFICATION_MATRIX.md", "FUZZ_TARGETS.md", "SECURITY_SURFACE.md", "BUILD_AND_ARTIFACTS.md"}
	default:
		return []string{"DEVELOPER_OVERVIEW.md", "ARCHITECTURE.md", "INDEX.md"}
	}
}

func buildProjectStructureGraphViews(run ProjectAnalysisRun, hits relevantSemanticIndexV2Hits, intent ProjectAnalysisQAIntent) []ProjectStructureGraphView {
	modules := buildDeveloperModuleRecords(run)
	views := []ProjectStructureGraphView{}
	add := func(view ProjectStructureGraphView) {
		if len(view.Edges) == 0 && len(view.Nodes) == 0 {
			return
		}
		view.SourceAnchors = analysisUniqueStrings(analysisDocSlashPaths(view.SourceAnchors))
		view.Evidence = analysisUniqueStrings(analysisDocSlashPaths(view.Evidence))
		view.RecommendedDocs = analysisUniqueStrings(view.RecommendedDocs)
		view.VerificationHints = analysisUniqueStrings(view.VerificationHints)
		view.Confidence = firstNonBlankAnalysisString(view.Confidence, "medium")
		views = append(views, view)
	}
	switch intent {
	case projectAnalysisQAIntentFlowTrace:
		add(projectStructureGraphViewFromEdges("Runtime Flow", "runtime", developerRuntimeGraphViews(run), runtimeEdgeAnchors(run.Snapshot.RuntimeEdges), []string{"STRUCTURE_DIAGRAMS.md", "API_AND_ENTRYPOINTS.md"}, []string{"Run the verification matrix for touched runtime paths."}))
		add(projectStructureGraphViewFromSemanticHits("Call Path Neighborhood", "call_path", hits, run, []string{"CODE_STRUCTURE_REFERENCE.md"}, []string{"Retest callers and callees on the returned path."}))
	case projectAnalysisQAIntentImpact:
		add(projectStructureGraphViewFromSemanticHits("Impact Blast Radius", "impact", hits, run, []string{"CODE_STRUCTURE_REFERENCE.md", "VERIFICATION_MATRIX.md"}, []string{"Run required checks for affected change areas."}))
		add(projectStructureGraphViewFromEdges("Build Ownership Flow", "build", developerBuildArtifactViews(run), developerBuildArtifactAnchors(run), []string{"BUILD_AND_ARTIFACTS.md"}, []string{"Rebuild affected build contexts."}))
	case projectAnalysisQAIntentSecuritySurface:
		add(projectStructureGraphViewFromEdges("Security Boundary Flow", "security", analysisGraphTrustBoundaryViews(run), analysisGraphSourceAnchors(run), []string{"SECURITY_SURFACE.md", "VERIFICATION_MATRIX.md", "FUZZ_TARGETS.md"}, []string{"Review validation, fuzz target, and evidence hooks before edits."}))
		add(projectStructureGraphViewFromSemanticHits("Security Overlay Neighborhood", "security_overlay", hits, run, []string{"SECURITY_SURFACE.md"}, []string{"Verify privileged and input-facing edges."}))
	case projectAnalysisQAIntentUnrealStructure:
		add(projectStructureGraphViewFromUnrealGraph("Unreal Reflection And Replication Flow", run.UnrealGraph, 18))
		add(projectStructureGraphViewFromEdges("Build Ownership Flow", "build", developerBuildArtifactViews(run), developerBuildArtifactAnchors(run), []string{"BUILD_AND_ARTIFACTS.md", "MODULES.md"}, []string{"Run UBT build for affected target or module."}))
	case projectAnalysisQAIntentBuildArtifact:
		add(projectStructureGraphViewFromEdges("Build Ownership Flow", "build", developerBuildArtifactViews(run), developerBuildArtifactAnchors(run), []string{"BUILD_AND_ARTIFACTS.md"}, []string{"Rebuild affected targets or translation units."}))
	default:
		add(projectStructureGraphViewFromEdges("Module Dependency Graph", "module", developerModuleGraphViews(buildDeveloperStructureGraph(run, modules)), analysisDeveloperDocSourceAnchors(run, "MODULES.md"), []string{"MODULES.md", "STRUCTURE_DIAGRAMS.md"}, []string{"Use module verification notes before changing boundaries."}))
		add(projectStructureGraphViewFromEdges("Folder And Module Map", "folder_module", developerFolderModuleViews(run, modules), analysisDeveloperDocSourceAnchors(run, "FOLDER_MAP.md"), []string{"FOLDER_MAP.md", "MODULES.md"}, []string{"Retest related folder-local tests."}))
		add(projectStructureGraphViewFromEdges("Runtime Flow", "runtime", developerRuntimeGraphViews(run), runtimeEdgeAnchors(run.Snapshot.RuntimeEdges), []string{"STRUCTURE_DIAGRAMS.md", "API_AND_ENTRYPOINTS.md"}, []string{"Trace current source before high-risk runtime edits."}))
		add(projectStructureGraphViewFromEdges("Build Ownership Flow", "build", developerBuildArtifactViews(run), developerBuildArtifactAnchors(run), []string{"BUILD_AND_ARTIFACTS.md"}, []string{"Rebuild affected build contexts."}))
	}
	return views
}

func projectStructureGraphViewFromEdges(title string, kind string, edges []analysisGraphEdgeView, anchors []string, docs []string, hints []string) ProjectStructureGraphView {
	nodes := []string{}
	evidence := []string{}
	for _, edge := range edges {
		nodes = append(nodes, edge.Source, edge.Target)
		evidence = append(evidence, splitAnalysisMetadataList(strings.ReplaceAll(edge.Evidence, ", ", ";"))...)
	}
	return ProjectStructureGraphView{
		Title:             title,
		Kind:              kind,
		Nodes:             analysisUniqueStrings(nodes),
		Edges:             edges,
		SourceAnchors:     anchors,
		Evidence:          evidence,
		RecommendedDocs:   docs,
		VerificationHints: hints,
		Confidence:        "medium",
	}
}

func projectStructureGraphViewFromSemanticHits(title string, kind string, hits relevantSemanticIndexV2Hits, run ProjectAnalysisRun, docs []string, hints []string) ProjectStructureGraphView {
	nameByID := semanticIndexV2NameMap(run.SemanticIndexV2)
	edges := []analysisGraphEdgeView{}
	evidence := []string{}
	addEdge := func(source string, target string, edgeType string, edgeEvidence []string, class string) {
		if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
			return
		}
		edges = append(edges, analysisGraphEdgeView{
			Source:     semanticIndexV2EntityDisplay(nameByID, source),
			Target:     semanticIndexV2EntityDisplay(nameByID, target),
			Type:       edgeType,
			Class:      class,
			Flow:       firstNonBlankAnalysisString(edgeType, class),
			Confidence: "medium",
			Evidence:   strings.Join(limitStrings(analysisDocSlashPaths(edgeEvidence), 3), ", "),
			Next:       "/analyze-dashboard",
		})
		evidence = append(evidence, edgeEvidence...)
	}
	for _, edge := range hits.Calls {
		addEdge(edge.SourceID, edge.TargetID, edge.Type, edge.Evidence, "runtime")
	}
	for _, edge := range hits.Builds {
		addEdge(edge.SourceID, edge.TargetID, edge.Type, edge.Evidence, "build")
	}
	for _, edge := range hits.Overlays {
		addEdge(edge.SourceID, edge.TargetID, edge.Type, edge.Evidence, "security")
	}
	for _, edge := range hits.References {
		addEdge(firstNonBlankAnalysisString(edge.SourceID, edge.SourceFile), firstNonBlankAnalysisString(edge.TargetID, edge.TargetPath), edge.Type, edge.Evidence, "reference")
	}
	nodes := []string{}
	for _, edge := range edges {
		nodes = append(nodes, edge.Source, edge.Target)
	}
	return ProjectStructureGraphView{
		Title:             title,
		Kind:              kind,
		Nodes:             analysisUniqueStrings(nodes),
		Edges:             limitAnalysisGraphEdgeViews(edges, 20),
		SourceAnchors:     analysisUniqueStrings(analysisDocSlashPaths(evidence)),
		Evidence:          analysisUniqueStrings(analysisDocSlashPaths(evidence)),
		RecommendedDocs:   docs,
		VerificationHints: hints,
		Confidence:        "medium",
	}
}

func projectStructureGraphViewFromUnrealGraph(title string, graph UnrealSemanticGraph, limit int) ProjectStructureGraphView {
	if limit <= 0 || len(graph.Edges) == 0 {
		return ProjectStructureGraphView{}
	}
	edges := []analysisGraphEdgeView{}
	anchors := []string{}
	for _, edge := range graph.Edges {
		source := edge.Source
		target := edge.Target
		edgeType := edge.Type
		evidence := []string{}
		for key, value := range edge.Attributes {
			if containsAny(strings.ToLower(key), "file", "source", "path") {
				evidence = append(evidence, value)
			}
		}
		anchors = append(anchors, evidence...)
		edges = append(edges, analysisGraphEdgeView{
			Source:     source,
			Target:     target,
			Type:       edgeType,
			Class:      "unreal",
			Flow:       firstNonBlankAnalysisString(edgeType, "unreal"),
			Confidence: "medium",
			Evidence:   strings.Join(limitStrings(analysisDocSlashPaths(evidence), 3), ", "),
			Next:       "/analyze-dashboard",
		})
		if len(edges) >= limit {
			break
		}
	}
	nodes := []string{}
	for _, edge := range edges {
		nodes = append(nodes, edge.Source, edge.Target)
	}
	return ProjectStructureGraphView{
		Title:             title,
		Kind:              "unreal",
		Nodes:             analysisUniqueStrings(nodes),
		Edges:             edges,
		SourceAnchors:     analysisUniqueStrings(analysisDocSlashPaths(anchors)),
		Evidence:          analysisUniqueStrings(analysisDocSlashPaths(anchors)),
		RecommendedDocs:   []string{"STRUCTURE_DIAGRAMS.md", "MODULES.md", "SECURITY_SURFACE.md"},
		VerificationHints: []string{"Run affected UBT target and replication/integrity regression checks."},
		Confidence:        "medium",
	}
}

func selectRelevantUnrealEdges(graph UnrealSemanticGraph, query string, intent ProjectAnalysisQAIntent, limit int) []UnrealSemanticEdge {
	if len(graph.Edges) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  UnrealSemanticEdge
		score int
		key   string
	}
	items := []scored{}
	for _, edge := range graph.Edges {
		attr := mapValues(edge.Attributes)
		haystacks := []string{
			strings.ToLower(edge.Source),
			strings.ToLower(edge.Target),
			strings.ToLower(edge.Type),
			strings.ToLower(strings.Join(attr, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if intent == projectAnalysisQAIntentUnrealStructure {
			score += 5
		}
		if score <= 0 {
			continue
		}
		items = append(items, scored{item: edge, score: score, key: edge.Source + "|" + edge.Type + "|" + edge.Target})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].score == items[j].score {
			return items[i].key < items[j].key
		}
		return items[i].score > items[j].score
	})
	out := []UnrealSemanticEdge{}
	for _, item := range items {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantVerificationEntries(items []AnalysisVerificationMatrixEntry, query string, intent ProjectAnalysisQAIntent, limit int) []AnalysisVerificationMatrixEntry {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  AnalysisVerificationMatrixEntry
		score int
		key   string
	}
	scoredItems := []scored{}
	for _, item := range items {
		haystacks := []string{
			strings.ToLower(item.ChangeArea),
			strings.ToLower(item.RequiredVerification),
			strings.ToLower(item.OptionalVerification),
			strings.ToLower(item.EvidenceHook),
			strings.ToLower(strings.Join(item.SourceAnchors, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if intent == projectAnalysisQAIntentVerification || intent == projectAnalysisQAIntentImpact || intent == projectAnalysisQAIntentSecuritySurface {
			score += 5
		}
		if score <= 0 {
			continue
		}
		scoredItems = append(scoredItems, scored{item: item, score: score, key: item.ChangeArea})
	}
	sort.Slice(scoredItems, func(i int, j int) bool {
		if scoredItems[i].score == scoredItems[j].score {
			return scoredItems[i].key < scoredItems[j].key
		}
		return scoredItems[i].score > scoredItems[j].score
	})
	out := []AnalysisVerificationMatrixEntry{}
	for _, item := range scoredItems {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantFuzzTargets(items []AnalysisFuzzTargetCatalogEntry, query string, intent ProjectAnalysisQAIntent, limit int) []AnalysisFuzzTargetCatalogEntry {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	loweredQuery, queryTokens, queryRefs := analysisV2QueryInputs(query)
	type scored struct {
		item  AnalysisFuzzTargetCatalogEntry
		score int
		key   string
	}
	scoredItems := []scored{}
	for _, item := range items {
		haystacks := []string{
			strings.ToLower(item.Name),
			strings.ToLower(item.File),
			strings.ToLower(item.Signature),
			strings.ToLower(item.InputSurfaceKind),
			strings.ToLower(item.BuildContext),
			strings.ToLower(strings.Join(item.PriorityReasons, " ")),
			strings.ToLower(strings.Join(item.ParameterStrategies, " ")),
		}
		score := analysisV2BaseScore(haystacks, loweredQuery, queryTokens, queryRefs)
		if intent == projectAnalysisQAIntentSecuritySurface || intent == projectAnalysisQAIntentVerification {
			score += 3
		}
		score += analysisMinInt(item.PriorityScore/25, 4)
		if score <= 0 {
			continue
		}
		scoredItems = append(scoredItems, scored{item: item, score: score, key: item.Name + "|" + item.File})
	}
	sort.Slice(scoredItems, func(i int, j int) bool {
		if scoredItems[i].score == scoredItems[j].score {
			return scoredItems[i].key < scoredItems[j].key
		}
		return scoredItems[i].score > scoredItems[j].score
	})
	out := []AnalysisFuzzTargetCatalogEntry{}
	for _, item := range scoredItems {
		out = append(out, item.item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func selectRelevantBuildContextsFromManifest(items []BuildContextRecord, query string, intent ProjectAnalysisQAIntent, limit int) []BuildContextRecord {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	if intent != projectAnalysisQAIntentBuildArtifact && intent != projectAnalysisQAIntentUnrealStructure && intent != projectAnalysisQAIntentImpact {
		return nil
	}
	return selectRelevantV2BuildContexts(SemanticIndexV2{BuildContexts: items}, query, projectAnalysisQAIntentMode(intent), limit)
}

func projectStructureDomainHints(run ProjectAnalysisRun) []string {
	textParts := []string{
		run.KnowledgePack.ProjectSummary,
		run.KnowledgePack.Goal,
		run.Snapshot.ModulePath,
		run.Snapshot.PrimaryStartup,
	}
	textParts = append(textParts, run.KnowledgePack.ArchitectureGroups...)
	textParts = append(textParts, run.KnowledgePack.TopImportantFiles...)
	textParts = append(textParts, run.KnowledgePack.HighRiskFiles...)
	for _, file := range run.SemanticIndexV2.Files {
		textParts = append(textParts, file.Path, file.Language)
		textParts = append(textParts, file.Tags...)
	}
	for _, ctx := range run.Snapshot.BuildContexts {
		textParts = append(textParts, ctx.Name, ctx.Kind, ctx.Project, ctx.Target, ctx.Module, ctx.Source)
		textParts = append(textParts, ctx.Files...)
	}
	for _, symbol := range run.SemanticIndexV2.Symbols {
		textParts = append(textParts, symbol.Name, symbol.CanonicalName, symbol.Kind, symbol.File)
		textParts = append(textParts, symbol.Tags...)
	}
	corpus := strings.ToLower(strings.Join(textParts, " "))
	out := []string{}
	if containsAny(corpus,
		"driverentry", ".sys", ".inf", "wdm", "wdk", "ntoskrnl", "fltmgr", "flt", "ioctl", "irp_mj_device_control",
		"obregistercallbacks", "pssetcreateprocessnotifyroutine", "mmcopyvirtualmemory", "zwopenprocess",
		"kernel driver", "windows driver", "kernel-mode", "kernel mode", "커널", "드라이버") {
		out = append(out, "windows_driver")
	}
	if containsAny(corpus, "ioctl", "deviceiocontrol", "irp", "control device") {
		out = append(out, "ioctl_control_surface")
	}
	if containsAny(corpus, "obregistercallbacks", "precallback", "handle_surface", "zwopenprocess") {
		out = append(out, "object_handle_filtering")
	}
	if containsAny(corpus, "pssetcreateprocessnotifyroutine", "processmonitor", "process_notify", "process monitor") {
		out = append(out, "process_monitoring")
	}
	if containsAny(corpus, "mmcopyvirtualmemory", "readprocessvirtualmemory", "memory_surface") {
		out = append(out, "memory_inspection")
	}
	if containsAny(corpus, "uclass", "ufunction", "unreal", ".uproject", ".build.cs") {
		out = append(out, "unreal")
	}
	return analysisUniqueStrings(out)
}

func selectProjectStructureCriticalAnchors(run ProjectAnalysisRun, hits relevantSemanticIndexV2Hits, intent ProjectAnalysisQAIntent, limit int) []ProjectStructureCriticalAnchor {
	if limit <= 0 {
		return nil
	}
	domainHints := projectStructureDomainHints(run)
	if len(domainHints) == 0 && len(run.SemanticIndexV2.Symbols) == 0 {
		return nil
	}
	symbols := append([]SymbolRecord(nil), run.SemanticIndexV2.Symbols...)
	symbols = append(symbols, hits.Symbols...)
	symbols = uniqueSymbolRecordsForCriticalAnchors(symbols)
	items := []ProjectStructureCriticalAnchor{}
	for _, symbol := range symbols {
		anchor, ok := projectStructureCriticalAnchorForSymbol(symbol, domainHints, intent)
		if !ok {
			continue
		}
		items = append(items, anchor)
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].Score == items[j].Score {
			return projectStructureCriticalAnchorKey(items[i]) < projectStructureCriticalAnchorKey(items[j])
		}
		return items[i].Score > items[j].Score
	})
	items = uniqueProjectStructureCriticalAnchors(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func projectStructureCriticalAnchorForSymbol(symbol SymbolRecord, domainHints []string, intent ProjectAnalysisQAIntent) (ProjectStructureCriticalAnchor, bool) {
	name := firstNonBlankProjectStructureString(symbol.CanonicalName, symbol.Name, symbol.ID)
	lowerName := strings.ToLower(name)
	lowerKind := strings.ToLower(symbol.Kind)
	lowerFile := strings.ToLower(filepathSlashOrEmpty(symbol.File))
	lowerTags := strings.ToLower(strings.Join(symbol.Tags, " "))
	corpus := strings.Join([]string{lowerName, lowerKind, lowerFile, lowerTags}, " ")
	role := ""
	why := ""
	verify := ""
	score := 0
	kernelSide := analysisContainsStringCI(domainHints, "windows_driver") && !containsAny(lowerFile, "testconsole", "manager.cpp", "manager.h")
	userSide := containsAny(lowerFile, "testconsole", "manager.cpp", "manager.h")
	switch {
	case strings.EqualFold(symbol.Name, "DriverEntry") || strings.EqualFold(symbol.CanonicalName, "DriverEntry") || containsAny(corpus, "driverentry"):
		role = "driver_entry"
		why = "driver load entrypoint; root of the kernel initialization path"
		verify = "Check DriverEntry failure cleanup and handoff into core initialization."
		score = 120
		kernelSide = true
	case containsAny(corpus, "core::initialize", "drivercore::initialize", "initialize") && containsAny(corpus, "core", "driver"):
		role = "core_initialization"
		why = "initializes global driver state, device object, and downstream kernel subsystems"
		verify = "Check initialization order, partial-failure unwind, and matching Finalize/Unload cleanup."
		score = 124
		kernelSide = true
	case containsAny(corpus, "createcontroldevice", "device object", "dosdevicename"):
		role = "control_device_creation"
		why = "creates the user/kernel control channel used by DeviceIoControl"
		verify = "Check device ACL, symbolic link exposure, and cleanup on initialization failure."
		score = 104
		kernelSide = true
	case containsAny(corpus, "unloadroutine"):
		role = "driver_unload_entry"
		why = "driver unload entry that invokes core and subsystem teardown"
		verify = "Verify unload gating, device cleanup, callback unregister ordering, and API/logger finalization."
		score = 114
		kernelSide = true
	case containsAny(corpus, "core::finalize", "drivercore::finalize") || (kernelSide && containsAny(corpus, "finalize", "cleanup") && containsAny(corpus, "core", "driver", "unload")):
		role = "teardown_cleanup"
		why = "core teardown path that finalizes process monitor, symbol manager, object filter, file filter, policy, and device objects"
		verify = "Check teardown ordering, double-finalize behavior, and cleanup after partial initialization failure."
		score = 115
		kernelSide = true
	case containsAny(corpus, "defaultirphandleroutine"):
		role = "kernel_irp_router"
		why = "routes IRP major functions such as create, cleanup, and device-control before handing off to specialized handlers"
		verify = "Verify IRP_MJ_CREATE, IRP_MJ_CLEANUP, and IRP_MJ_DEVICE_CONTROL state transitions, control PID lifecycle, and unload gating."
		score = 122
		kernelSide = true
	case containsAny(corpus, "deviceiocontrolirphandleroutine"):
		role = "kernel_ioctl_dispatch"
		why = "kernel-side IRP_MJ_DEVICE_CONTROL dispatcher; primary encrypted command payload surface"
		verify = "Fuzz IOCTL codes and buffers; verify length checks, decryption, command validation, control PID checks, and command-specific failure cleanup."
		score = 126
		kernelSide = true
	case (containsAny(corpus, "deviceiocontrol", "ioctl_handler", "irp handler", "irp_mj_device_control") || lowerKind == "ioctl_handler") &&
		!containsAny(corpus, "validaterequestor", "validaterequester", "validatecaller", "isvalidcommand", "validatecommand", "decryptioctldata", "decryptioctl", "getcontrolpid", "controlpid"):
		role = "kernel_ioctl_dispatch"
		why = "kernel-side IOCTL dispatcher; primary user-to-kernel input surface"
		verify = "Fuzz IOCTL codes and buffers; verify length checks, command validation, decryption, and caller validation."
		score = 116
		if containsAny(corpus, "deviceiocontrol", "irp_mj_device_control") {
			score = 123
		}
		kernelSide = true
	case containsAny(corpus, "validaterequestor", "validaterequester", "validaterequestorigin", "validatecaller", "validateclient", "isrequestor", "iscallertrusted"):
		role = "request_origin_validation"
		why = "validates the user-mode request origin for the control channel; keep this separate from command-payload validation unless call edges prove otherwise"
		verify = "Test spoofed IRP_MJ_CREATE callers, stale control PID, service restart, and requests from non-controller processes."
		score = 121
		kernelSide = true
	case containsAny(corpus, "isvalidcommand", "validatecommand", "validcommand", "validateioctlcommand"):
		role = "ioctl_command_validation"
		why = "validates IOCTL command identity before dispatching command-specific behavior"
		verify = "Fuzz invalid command IDs, boundary command values, and command/state mismatch cases."
		score = 119
		kernelSide = true
	case containsAny(corpus, "decryptioctldata", "decryptioctl", "decryptrequest", "decryptpayload", "unpackioctl", "decodeioctl"):
		role = "ioctl_payload_decryption"
		why = "decrypts or unwraps IOCTL payload data before policy or control operations consume it"
		verify = "Test malformed encrypted payloads, replay cases, wrong keys, and length mismatch handling."
		score = 116
		kernelSide = true
	case containsAny(corpus, "getcontrolpid", "controlpid", "controlprocess", "requestorpid", "requesterpid", "callerpid", "requestorprocess"):
		role = "request_validation"
		why = "provides control process identity used by caller validation"
		verify = "Verify control PID lifecycle, reset, and lookup behavior across service restart."
		score = 108
		kernelSide = true
	case containsAny(corpus, "objectfilter::initialize", "object_filter::initialize", "handlefilter::initialize", "obcallback::initialize") ||
		(containsAny(corpus, "initialize") && containsAny(corpus, "objectfilter", "handlefilter", "obcallback")):
		role = "object_filter_initialization"
		why = "initializes object-filter state during core initialization; this is not the callback registration path"
		verify = "Verify object-filter state setup, partial initialization failure handling, and matching Finalize cleanup before callbacks are started."
		score = 114
		kernelSide = true
	case containsAny(corpus, "processobjectprecallback", "threadobjectprecallback"):
		role = "object_pre_callback"
		why = "enforces process/thread handle access policy through object callbacks"
		verify = "Exercise protected and allowed process pairs; verify desired access rewriting and callback IRQL assumptions."
		score = 116
		kernelSide = true
	case containsAny(corpus, "startobjectfilter", "startfilter", "enableobjectfilter", "registerobjectcallback", "registerobjectcallbacks") &&
		!containsAny(corpus, "obregistercallbacks"):
		role = "object_callback_registration"
		why = "registers object callbacks that enforce process/thread handle access policy"
		verify = "Exercise protected and allowed process pairs; verify Ob callback registration altitude and unregister ordering."
		score = 120
		kernelSide = true
	case containsAny(corpus, "obregistercallbacks"):
		role = "object_callback_registration"
		why = "wraps or invokes object callback registration; prefer higher-level filter start anchors when present"
		verify = "Verify Ob callback registration altitude, wrapper failure propagation, and unregister ordering."
		score = 104
		kernelSide = true
	case containsAny(corpus, "objectfilter"):
		role = "object_callback_registration"
		why = "object callback subsystem that enforces process/thread handle access policy"
		verify = "Exercise protected and allowed process pairs; verify Ob callback registration altitude and unregister ordering."
		score = 96
		kernelSide = true
	case containsAny(corpus, "pssetcreateprocessnotifyroutine", "setcreateprocessnotifyroutine"):
		role = "process_notify_api_wrapper"
		why = "dynamic API wrapper used by the process monitor to register or remove create-process notifications"
		verify = "Verify wrapper failure propagation and remove=true unregister behavior across teardown."
		score = 106
		kernelSide = true
	case containsAny(corpus, "processmonitor::initialize", "process_monitor::initialize") ||
		(containsAny(corpus, "initialize") && containsAny(corpus, "processmonitor", "process_monitor")):
		role = "process_monitor_initialization"
		why = "initializes process monitor state before process notifications are started"
		verify = "Verify process table setup, lock initialization, and cleanup if later subsystem initialization fails."
		score = 113
		kernelSide = true
	case containsAny(corpus, "startprocessmonitor", "processnotifyroutine", "pssetcreateprocessnotifyroutine", "processmonitor", "insertprocessinfo", "removeprocessinfo"):
		role = "process_monitor"
		why = "tracks process lifecycle and process identity data used by policy decisions"
		verify = "Stress process create/exit races, AVL table locking, and teardown while callbacks are active."
		score = 102
		if containsAny(corpus, "processmonitor::startprocessmonitor", "process_monitor::startprocessmonitor", "startprocessmonitor") {
			score = 120
			why = "starts process lifecycle monitoring by registering create-process notifications and maintaining process identity state"
		} else if containsAny(corpus, "processmonitor::processnotifyroutine", "processnotifyroutineex", "processnotifyroutine") {
			score = 116
		} else if containsAny(corpus, "startprocessmonitor", "processnotifyroutine", "pssetcreateprocessnotifyroutine") {
			score = 112
		}
		kernelSide = true
	case containsAny(corpus, "filefilter::initialize", "file_filter::initialize", "minifilter::initialize") ||
		(containsAny(corpus, "initialize") && containsAny(corpus, "filefilter", "file_filter", "minifilter", "fltregisterfilter")):
		role = "file_minifilter"
		why = "initializes the file-system minifilter registration surface"
		verify = "Verify FltRegisterFilter/FltStartFiltering ordering, callback IRQL assumptions, and unload cleanup."
		score = 112
		kernelSide = true
	case containsAny(corpus, "filefilter", "flt", "minifilter"):
		role = "file_minifilter"
		why = "file-system minifilter surface that can affect file access policy"
		verify = "Verify FltRegisterFilter/FltStartFiltering ordering, callback IRQL assumptions, and unload cleanup."
		score = 94
		kernelSide = true
	case containsAny(corpus, "kernelapi", "kernel_api", "apiresolver", "api_resolver", "mmgetsystemroutineaddress", "getexportfunctionaddress", "getapi", "isvalidfunction", "mmcopyvirtualmemory", "zwopenprocess", "zwqueryvirtualmemory"):
		role = "dynamic_kernel_api_resolver"
		why = "resolves and wraps kernel APIs used by handle, memory, and callback paths"
		verify = "Test missing/invalid routine resolution, Windows version drift, and wrapper failure propagation."
		score = 116
		switch {
		case containsAny(corpus, "api::initialize", "kernelapi::initialize", "apiresolver::initialize", "api_resolver::initialize") ||
			(containsAny(corpus, "initialize") && containsAny(corpus, "kernelapi", "apiresolver", "api_resolver")):
			score = 126
			why = "initializes the dynamic kernel API table by resolving Ob/Ps/Zw/Mm routines"
		case containsAny(corpus, "getexportfunctionaddress"):
			score = 124
			why = "walks the kernel image export table to resolve dynamic kernel API addresses"
		case containsAny(corpus, "getapi", "isvalidfunction", "mmgetsystemroutineaddress"):
			score = 120
		case containsAny(corpus, "_getenclosingsectionheader"):
			score = 100
			why = "helper used by export-table walking; not the whole API resolver layer"
		}
		kernelSide = true
	case containsAny(corpus, "kernelpolicy", "driverpolicy", "policy", "isprotectedprocess", "iswhitelistedprocess", "addallowedprocesspair"):
		role = "policy_engine"
		why = "owns protection/allow policy decisions consumed by enforcement callbacks"
		verify = "Test policy mutation, normalization of process paths, and concurrent lookup/update behavior."
		score = 94
		kernelSide = true
	case userSide && containsAny(corpus, "addprotectiontargetprocesspath", "addwhitelistedprocesspath", "controloperation", "createdevicehandle", "deviceiocontrol"):
		role = "user_mode_ioctl_client"
		why = "user-mode wrapper that constructs IOCTL requests; separate from the kernel-side dispatcher"
		verify = "Validate user-mode buffer construction against kernel-side length, command, and encryption expectations."
		score = 82
	}
	if role == "" {
		if intent == projectAnalysisQAIntentSecuritySurface && containsAny(lowerTags, "security_surface", "ioctl_surface", "handle_surface", "memory_surface", "control_surface") {
			role = "security_surface_symbol"
			why = "security-tagged symbol selected by the structural index"
			verify = "Review validation and regression coverage for this security-tagged path."
			score = 70
		} else {
			return ProjectStructureCriticalAnchor{}, false
		}
	}
	if intent == projectAnalysisQAIntentDeepMap || intent == projectAnalysisQAIntentFlowTrace {
		score += 6
	}
	if intent == projectAnalysisQAIntentSecuritySurface && containsAny(role, "ioctl", "validation", "filter", "policy", "api", "security") {
		score += 10
	}
	if kernelSide {
		score += 8
	}
	if userSide {
		score -= 4
	}
	if strings.TrimSpace(symbol.File) != "" {
		score += 2
	}
	if symbol.StartLine > 0 {
		score += 4
	}
	if lowerKind == "entity" || strings.TrimSpace(symbol.File) == "" {
		score -= 8
	}
	return ProjectStructureCriticalAnchor{
		Role:             role,
		Name:             name,
		Kind:             symbol.Kind,
		File:             filepathSlashOrEmpty(symbol.File),
		Line:             symbol.StartLine,
		Tags:             append([]string(nil), symbol.Tags...),
		Why:              why,
		VerificationHint: verify,
		Score:            score,
		KernelSide:       kernelSide,
		UserModeSide:     userSide,
	}, true
}

func projectStructureDomainFlows(pack ProjectStructureAnswerPack) []string {
	flows := []string{}
	if analysisContainsStringCI(pack.DomainHints, "windows_driver") {
		entry := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "driver_entry")
		init := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "core_initialization")
		irpRouter := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "kernel_irp_router")
		ioctl := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "kernel_ioctl_dispatch")
		originValidation := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "request_origin_validation")
		commandValidation := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "ioctl_command_validation")
		payloadDecrypt := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "ioctl_payload_decryption")
		requestValidation := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "request_validation")
		controlDevice := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "control_device_creation")
		objectInit := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "object_filter_initialization")
		objectRegistration := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "object_callback_registration")
		objectPreCallback := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "object_pre_callback")
		processMonitorInit := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "process_monitor_initialization")
		processMonitor := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "process_monitor")
		processNotifyWrapper := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "process_notify_api_wrapper")
		api := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "dynamic_kernel_api_resolver")
		fileFilter := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "file_minifilter")
		unload := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "driver_unload_entry")
		teardown := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "teardown_cleanup")
		if entry != "" || init != "" {
			objectInitLabel := objectInit
			if objectInitLabel != "" {
				objectInitLabel += " (object filter state initialization, not callback registration)"
			}
			flows = append(flows, "load/init spine: "+strings.Join(nonEmptyStrings(entry, init), " -> ")+"; core initialization coordinates: "+strings.Join(nonEmptyStrings(api, controlDevice, "policy initialization", fileFilter, objectInitLabel, processMonitorInit, processMonitor), "; "))
		}
		if irpRouter != "" {
			flows = append(flows, "IRP router spine: "+strings.Join(nonEmptyStrings(irpRouter, "branches by MajorFunction: IRP_MJ_CREATE, IRP_MJ_CLEANUP, IRP_MJ_DEVICE_CONTROL"), " -> "))
		}
		if irpRouter != "" || originValidation != "" {
			flows = append(flows, "control-open validation spine: "+strings.Join(nonEmptyStrings(irpRouter, "IRP_MJ_CREATE validates request origin", originValidation, "control PID established; IRP_MJ_CLEANUP clears control PID"), " -> "))
		}
		if irpRouter != "" || ioctl != "" {
			flows = append(flows, "device-control branch spine: "+strings.Join(nonEmptyStrings(irpRouter, "IRP_MJ_DEVICE_CONTROL branch", ioctl), " -> "))
		}
		if ioctl != "" || commandValidation != "" || payloadDecrypt != "" {
			requestorRule := ""
			if requestValidation != "" {
				requestorRule = "requestor/control-process checks use accessor or request identity state from " + requestValidation + "; keep this separate from control-open request-origin validation"
			} else if originValidation != "" {
				requestorRule = "keep request-origin validation separate from command-payload validation unless call-edge evidence connects them"
			}
			flows = append(flows, "REQUIRED device-control command spine: "+strings.Join(nonEmptyStrings(ioctl, payloadDecrypt, "per-command size/shape checks", commandValidation, "command-specific policy/object/memory handlers", requestorRule), " -> "))
		}
		if objectRegistration != "" || objectPreCallback != "" {
			flows = append(flows, "object handle enforcement spine: "+strings.Join(nonEmptyStrings(ioctl, "control operation can start/stop object filtering", objectRegistration, "Ob process/thread pre-operation callbacks", objectPreCallback, "policy decisions", "desired-access rewrite or allow"), " -> "))
			flows = append(flows, "teardown rule: Finalize/Unload stops and unregisters filters; do not describe Finalize as the runtime enforcement path.")
		}
		if processMonitor != "" {
			flows = append(flows, "process monitor spine: "+strings.Join(nonEmptyStrings(processMonitor, processNotifyWrapper, "registers/removes create-process notify callback", "process notify routine updates process identity table"), " -> "))
		}
		if api != "" {
			flows = append(flows, "kernel API dependency: "+api+" resolves/wraps Ob/Ps/Zw/Mm routines used by IOCTL, object callback, process monitor, file, and memory paths")
		}
		if unload != "" || teardown != "" {
			flows = append(flows, "teardown spine: "+strings.Join(nonEmptyStrings(unload, teardown, "subsystem finalizers unregister callbacks/filters and delete device objects"), " -> "))
		}
		flows = append(flows, "terminology: describe this as a Windows kernel/WDM .sys driver, not a DLL, unless a source artifact explicitly says DLL")
		flows = append(flows, "IOCTL analysis rule: separate user-mode control/client wrappers from kernel-side IRP/IOCTL dispatch and validation")
	}
	return analysisUniqueStrings(flows)
}

func projectStructureDriverRequiredFacts(pack ProjectStructureAnswerPack) []string {
	if !analysisContainsStringCI(pack.DomainHints, "windows_driver") {
		return nil
	}
	facts := []string{}
	roots := []string{}
	rootFolders := projectStructureRootFolders(pack.Folders)
	for _, folder := range rootFolders {
		path := strings.Trim(strings.ReplaceAll(filepathSlashOrEmpty(folder.Path), "\\", "/"), "/")
		if path == "" || path == "." || projectStructurePathLooksLikeFile(path) {
			continue
		}
		roots = append(roots, path+"/")
	}
	if len(roots) > 0 {
		facts = append(facts, "Authoritative top-level directories: "+strings.Join(limitStrings(analysisUniqueStrings(roots), 8), ", ")+". CLOSED SET: for a top-level directory table, use exactly these directory rows and no extra rows. Do not add nested folders, root files, headers, source files, project files, or inferred paths.")
		facts = append(facts, "Copy this exact top-level directory table if needed: "+projectStructureClosedRootTableFact(rootFolders))
	}
	if exclusions := projectStructureTopLevelTableExclusions(pack); len(exclusions) > 0 {
		facts = append(facts, "Never list these paths as top-level directory rows: "+strings.Join(limitStrings(projectStructurePrioritizedTopLevelExclusions(exclusions), 8), ", ")+". Mention files only in file/source sections and nested folders only under their parent.")
	}
	for _, flow := range pack.DomainFlows {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(flow)), "required device-control command spine:") {
			facts = append(facts, "Copy this exact IOCTL command spine with symbol names: "+flow)
			break
		}
	}
	if anchor := projectStructureFirstCriticalAnchorFlowLabel(pack.CriticalAnchors, "object_callback_registration"); anchor != "" {
		facts = append(facts, "Runtime filter start/registration anchor: "+anchor+". If cited, copy this exact file:line and do not replace it with ellipsis.")
	}
	return analysisUniqueStrings(facts)
}

func projectStructureClosedRootTableFact(folders []DeveloperFolderRecord) string {
	rows := []string{"| Directory | Role |", "|---|---|"}
	for _, folder := range limitDeveloperFolderRecords(folders, 8) {
		path := strings.Trim(strings.ReplaceAll(filepathSlashOrEmpty(folder.Path), "\\", "/"), "/")
		if path == "" || path == "." || projectStructurePathLooksLikeFile(path) {
			continue
		}
		rows = append(rows, fmt.Sprintf("| `%s/` | %s |", path, compactProjectAnalysisText(folder.Responsibility, 120)))
	}
	return strings.Join(rows, " ")
}

func projectStructureTopLevelTableExclusions(pack ProjectStructureAnswerPack) []string {
	rootSet := map[string]struct{}{}
	for _, folder := range projectStructureRootFolders(pack.Folders) {
		path := strings.Trim(strings.ReplaceAll(filepathSlashOrEmpty(folder.Path), "\\", "/"), "/")
		if path != "" && path != "." {
			rootSet[strings.ToLower(path)] = struct{}{}
		}
	}
	exclusions := []string{}
	add := func(path string) {
		path = strings.Trim(strings.ReplaceAll(filepathSlashOrEmpty(path), "\\", "/"), "/")
		if idx := strings.LastIndex(path, ":"); idx > 1 && allDigits(path[idx+1:]) {
			path = path[:idx]
		}
		if path == "" || path == "." {
			return
		}
		lower := strings.ToLower(path)
		if _, ok := rootSet[lower]; ok {
			return
		}
		if projectStructurePathLooksLikeFile(path) || strings.Contains(path, "/") {
			exclusions = append(exclusions, path)
		}
	}
	for _, folder := range pack.Folders {
		add(folder.Path)
		for _, file := range folder.KeyFiles {
			add(file)
		}
		for _, anchor := range folder.SourceAnchors {
			add(anchor)
		}
	}
	for _, file := range pack.Files {
		add(file.Path)
	}
	for _, anchor := range pack.SourceAnchors {
		add(anchor)
	}
	for _, symbol := range pack.Symbols {
		add(symbol.File)
	}
	for _, anchor := range pack.CriticalAnchors {
		add(anchor.File)
	}
	return analysisUniqueStrings(exclusions)
}

func projectStructurePrioritizedTopLevelExclusions(items []string) []string {
	out := analysisUniqueStrings(items)
	sort.SliceStable(out, func(i int, j int) bool {
		left := projectStructureTopLevelExclusionPriority(out[i])
		right := projectStructureTopLevelExclusionPriority(out[j])
		if left != right {
			return left > right
		}
		if strings.Count(out[i], "/") != strings.Count(out[j], "/") {
			return strings.Count(out[i], "/") > strings.Count(out[j], "/")
		}
		return out[i] < out[j]
	})
	return out
}

func projectStructureTopLevelExclusionPriority(path string) int {
	lower := strings.ToLower(strings.TrimSpace(path))
	score := 0
	if strings.Contains(lower, "/") && !projectStructurePathLooksLikeFile(lower) {
		score += 8
	}
	if projectStructurePathLooksLikeFile(lower) {
		score += 5
	}
	if containsAny(lower, "build", "batch", "cab", "package", "sign", "deploy") {
		score += 3
	}
	if containsAny(lower, "object", "filter", "core", "ioctl", "driver") {
		score += 2
	}
	return score
}

func projectStructureRootFolders(folders []DeveloperFolderRecord) []DeveloperFolderRecord {
	roots := map[string]DeveloperFolderRecord{}
	exact := map[string]bool{}
	for _, folder := range folders {
		path := strings.Trim(strings.ReplaceAll(filepathSlashOrEmpty(folder.Path), "\\", "/"), "/")
		if path == "" || path == "." || projectStructurePathLooksLikeFile(path) {
			continue
		}
		root := path
		if idx := strings.Index(root, "/"); idx >= 0 {
			root = root[:idx]
		}
		if root == "" || root == "." {
			continue
		}
		current, ok := roots[root]
		if !ok {
			current = DeveloperFolderRecord{
				Path:       root,
				Confidence: firstNonBlankAnalysisString(folder.Confidence, "medium"),
			}
		}
		if strings.EqualFold(path, root) {
			folder.Path = root
			roots[root] = folder
			exact[root] = true
			continue
		}
		if exact[root] {
			continue
		}
		current.Responsibility = chooseFolderResponsibility(current.Responsibility, folder.Responsibility)
		current.KeyFiles = append(current.KeyFiles, folder.KeyFiles...)
		current.SourceAnchors = append(current.SourceAnchors, folder.SourceAnchors...)
		current.RiskSignals = append(current.RiskSignals, folder.RiskSignals...)
		current.Subsystems = append(current.Subsystems, folder.Subsystems...)
		roots[root] = current
	}
	out := make([]DeveloperFolderRecord, 0, len(roots))
	for _, folder := range roots {
		folder.KeyFiles = analysisUniqueStrings(folder.KeyFiles)
		folder.SourceAnchors = analysisUniqueStrings(folder.SourceAnchors)
		folder.RiskSignals = analysisUniqueStrings(folder.RiskSignals)
		folder.Subsystems = analysisUniqueStrings(folder.Subsystems)
		folder.Responsibility = firstNonBlankAnalysisString(folder.Responsibility, "top-level source area")
		out = append(out, folder)
	}
	sort.Slice(out, func(i int, j int) bool {
		if len(out[i].RiskSignals) != len(out[j].RiskSignals) {
			return len(out[i].RiskSignals) > len(out[j].RiskSignals)
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func projectStructurePathLooksLikeFile(path string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(strings.ReplaceAll(path, "\\", "/"))))
	if base == "" || base == "." {
		return false
	}
	switch filepath.Ext(base) {
	case ".h", ".hh", ".hpp", ".hxx", ".c", ".cc", ".cpp", ".cxx", ".vcxproj", ".sln", ".inf", ".filters", ".props", ".targets":
		return true
	default:
		return false
	}
}

func projectStructureFirstCriticalAnchorName(items []ProjectStructureCriticalAnchor, role string) string {
	anchor, ok := projectStructureFirstCriticalAnchor(items, role)
	if !ok {
		return ""
	}
	return anchor.Name
}

func projectStructureFirstCriticalAnchorFlowLabel(items []ProjectStructureCriticalAnchor, role string) string {
	anchor, ok := projectStructureFirstCriticalAnchor(items, role)
	if !ok {
		return ""
	}
	name := strings.TrimSpace(anchor.Name)
	if name == "" {
		name = strings.TrimSpace(anchor.Kind)
	}
	location := projectStructureCriticalAnchorLocation(anchor)
	if name == "" {
		return location
	}
	if location == "" || location == "none" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, location)
}

func projectStructureFirstCriticalAnchor(items []ProjectStructureCriticalAnchor, role string) (ProjectStructureCriticalAnchor, bool) {
	for _, item := range items {
		if strings.EqualFold(item.Role, role) {
			return item, true
		}
	}
	return ProjectStructureCriticalAnchor{}, false
}

func projectStructureCriticalAnchorLocation(anchor ProjectStructureCriticalAnchor) string {
	location := strings.TrimSpace(anchor.File)
	if anchor.Line > 0 {
		if location == "" {
			return fmt.Sprintf("line %d", anchor.Line)
		}
		return fmt.Sprintf("%s:%d", location, anchor.Line)
	}
	if location == "" {
		return "none"
	}
	return location
}

func uniqueSymbolRecordsForCriticalAnchors(items []SymbolRecord) []SymbolRecord {
	seen := map[string]struct{}{}
	out := []SymbolRecord{}
	for _, item := range items {
		key := strings.ToLower(strings.Join([]string{item.ID, item.CanonicalName, item.Name, item.File, strconv.Itoa(item.StartLine)}, "|"))
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func uniqueProjectStructureCriticalAnchors(items []ProjectStructureCriticalAnchor) []ProjectStructureCriticalAnchor {
	seenRole := map[string]int{}
	out := []ProjectStructureCriticalAnchor{}
	for _, item := range items {
		key := projectStructureCriticalAnchorKey(item)
		if key == "" {
			continue
		}
		roleKey := strings.ToLower(strings.TrimSpace(item.Role))
		if index, ok := seenRole[roleKey]; ok {
			if projectStructureCriticalAnchorBetter(item, out[index]) {
				out[index] = item
			}
			continue
		}
		seenRole[roleKey] = len(out)
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		if out[i].Score == out[j].Score {
			return projectStructureCriticalAnchorKey(out[i]) < projectStructureCriticalAnchorKey(out[j])
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func projectStructureCriticalAnchorBetter(candidate ProjectStructureCriticalAnchor, existing ProjectStructureCriticalAnchor) bool {
	if candidate.Score != existing.Score {
		return candidate.Score > existing.Score
	}
	candidateConcrete := 0
	existingConcrete := 0
	if strings.TrimSpace(candidate.File) != "" {
		candidateConcrete += 2
	}
	if candidate.Line > 0 {
		candidateConcrete += 2
	}
	if !strings.EqualFold(strings.TrimSpace(candidate.Kind), "entity") {
		candidateConcrete++
	}
	if strings.TrimSpace(existing.File) != "" {
		existingConcrete += 2
	}
	if existing.Line > 0 {
		existingConcrete += 2
	}
	if !strings.EqualFold(strings.TrimSpace(existing.Kind), "entity") {
		existingConcrete++
	}
	if candidateConcrete != existingConcrete {
		return candidateConcrete > existingConcrete
	}
	return projectStructureCriticalAnchorKey(candidate) < projectStructureCriticalAnchorKey(existing)
}

func projectStructureCriticalAnchorKey(item ProjectStructureCriticalAnchor) string {
	return strings.ToLower(strings.Join([]string{item.Role, item.Name, item.File, strconv.Itoa(item.Line)}, "|"))
}

func filepathSlashOrEmpty(path string) string {
	return filepath.ToSlash(strings.TrimSpace(path))
}

func nonEmptyStrings(items ...string) []string {
	out := []string{}
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, strings.TrimSpace(item))
		}
	}
	return out
}

func projectStructureAnswerPackSourceAnchors(pack ProjectStructureAnswerPack) []string {
	items := []string{}
	for _, anchor := range pack.CriticalAnchors {
		if strings.TrimSpace(anchor.File) != "" {
			if anchor.Line > 0 {
				items = append(items, fmt.Sprintf("%s:%d", anchor.File, anchor.Line))
			} else {
				items = append(items, anchor.File)
			}
		}
	}
	for _, anchor := range pack.ArchitectureFacts.CriticalAnchors {
		if strings.TrimSpace(anchor.Location) != "" && anchor.Location != "none" {
			items = append(items, anchor.Location)
		} else if strings.TrimSpace(anchor.File) != "" {
			items = append(items, anchor.File)
		}
	}
	for _, flow := range pack.ArchitectureFacts.FlowFacts {
		items = append(items, flow.Evidence...)
	}
	for _, boundary := range pack.ArchitectureFacts.BoundaryFacts {
		items = append(items, boundary.Evidence...)
	}
	for _, doc := range pack.RelevantDocs {
		items = append(items, doc.SourceAnchors...)
	}
	for _, module := range pack.Modules {
		items = append(items, module.SourceAnchors...)
		items = append(items, module.PublicFiles...)
		items = append(items, module.Entrypoints...)
	}
	for _, folder := range pack.Folders {
		items = append(items, folder.SourceAnchors...)
		items = append(items, folder.KeyFiles...)
	}
	for _, file := range pack.Files {
		items = append(items, file.Path)
	}
	for _, symbol := range pack.Symbols {
		if strings.TrimSpace(symbol.File) != "" {
			if symbol.StartLine > 0 {
				items = append(items, fmt.Sprintf("%s:%d", symbol.File, symbol.StartLine))
			} else {
				items = append(items, symbol.File)
			}
		}
	}
	for _, view := range pack.GraphViews {
		items = append(items, view.SourceAnchors...)
		items = append(items, view.Evidence...)
	}
	for _, edge := range pack.SecurityOverlays {
		items = append(items, edge.Evidence...)
	}
	for _, entry := range pack.VerificationEntries {
		items = append(items, entry.SourceAnchors...)
	}
	for _, target := range pack.FuzzTargets {
		items = append(items, target.SourceAnchor, target.File)
	}
	return analysisUniqueStrings(analysisDocSlashPaths(items))
}

func projectStructureAnswerPackStaleMarkers(pack ProjectStructureAnswerPack) []string {
	items := []string{}
	for _, doc := range pack.RelevantDocs {
		items = append(items, doc.StaleMarkers...)
	}
	return analysisRealStaleMarkers(items)
}

func projectStructureAnswerPackSuggestedReads(pack ProjectStructureAnswerPack) []string {
	items := []string{}
	for _, doc := range pack.RelevantDocs {
		if strings.TrimSpace(doc.Path) != "" {
			items = append(items, "latest/docs/"+doc.Path)
		}
	}
	for _, view := range pack.GraphViews {
		for _, doc := range view.RecommendedDocs {
			items = append(items, "latest/docs/"+doc)
		}
	}
	items = append(items, pack.SourceAnchors...)
	return analysisUniqueStrings(items)
}

func projectStructureAnswerPackConfidence(pack ProjectStructureAnswerPack) string {
	coverage := 0
	if len(pack.RelevantDocs) > 0 {
		coverage++
	}
	if len(pack.GraphViews) > 0 {
		coverage++
	}
	if len(pack.SourceAnchors) >= 5 {
		coverage++
	}
	if len(pack.Symbols) > 0 || len(pack.Files) > 0 {
		coverage++
	}
	if len(pack.CriticalAnchors) > 0 {
		coverage++
	}
	if len(pack.VerificationEntries) > 0 || len(pack.FuzzTargets) > 0 {
		coverage++
	}
	switch {
	case coverage >= 4:
		return "high"
	case coverage >= 2:
		return "medium"
	default:
		return "low"
	}
}

func projectStructureAnswerPackNeedsCurrentSource(pack ProjectStructureAnswerPack) bool {
	if len(pack.StaleMarkers) > 0 && (pack.Intent == projectAnalysisQAIntentImpact || pack.Intent == projectAnalysisQAIntentSecuritySurface || pack.Intent == projectAnalysisQAIntentFlowTrace) {
		return true
	}

	hasArchitectureFacts := architectureFactPackHasData(pack.ArchitectureFacts)
	hasCriticalAnchors := len(pack.CriticalAnchors) > 0 || len(pack.ArchitectureFacts.CriticalAnchors) > 0
	hasStructuralContext := len(pack.RelevantDocs) > 0 || len(pack.GraphViews) > 0 || len(pack.Modules) > 0 || len(pack.Folders) > 0
	hasSourceAnchors := len(pack.SourceAnchors) >= 3 || hasCriticalAnchors
	if hasArchitectureFacts && hasCriticalAnchors && hasStructuralContext && hasSourceAnchors {
		return false
	}

	if len(pack.SourceAnchors) < 3 && !hasCriticalAnchors {
		return true
	}
	if !hasArchitectureFacts && len(pack.RelevantDocs) == 0 && len(pack.GraphViews) == 0 {
		return true
	}
	return false
}

func analysisRealStaleMarkers(items []string) []string {
	out := []string{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if lower == "no_previous_run" || lower == "no previous run" || lower == "new_primary_scope" || lower == "new primary scope" || lower == "none" || lower == "no stale markers" {
			continue
		}
		out = append(out, trimmed)
	}
	return analysisUniqueStrings(out)
}

func splitAnalysisMetadataList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ';' || r == ',' || r == '\n' || r == '\t'
	})
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return analysisUniqueStrings(out)
}

func firstNonBlankProjectStructureString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func parseAnalysisMetadataInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return n
}

func projectStructureDocHitKey(item ProjectStructureDocHit) string {
	return strings.Join([]string{item.DocName, item.SectionID, item.SectionTitle, item.Path}, "|")
}

func uniqueProjectStructureDocHits(items []ProjectStructureDocHit) []ProjectStructureDocHit {
	seen := map[string]int{}
	out := []ProjectStructureDocHit{}
	for _, item := range items {
		key := projectStructureDocHitKey(item)
		if strings.TrimSpace(key) == "" {
			continue
		}
		if index, ok := seen[key]; ok {
			out[index] = mergeProjectStructureDocHit(out[index], item)
			continue
		}
		seen[key] = len(out)
		out = append(out, item)
	}
	return out
}

func mergeProjectStructureDocHit(primary ProjectStructureDocHit, extra ProjectStructureDocHit) ProjectStructureDocHit {
	primary.DocName = firstNonBlankAnalysisString(primary.DocName, extra.DocName)
	primary.Title = firstNonBlankAnalysisString(primary.Title, extra.Title)
	primary.SectionID = firstNonBlankAnalysisString(primary.SectionID, extra.SectionID)
	primary.SectionTitle = firstNonBlankAnalysisString(primary.SectionTitle, extra.SectionTitle)
	primary.Path = firstNonBlankAnalysisString(primary.Path, extra.Path)
	if strings.TrimSpace(primary.Text) == "" {
		primary.Text = extra.Text
	}
	primary.SourceAnchors = analysisUniqueStrings(append(primary.SourceAnchors, extra.SourceAnchors...))
	primary.StaleMarkers = analysisUniqueStrings(append(primary.StaleMarkers, extra.StaleMarkers...))
	primary.ReuseTargets = analysisUniqueStrings(append(primary.ReuseTargets, extra.ReuseTargets...))
	primary.QueryIntents = analysisUniqueStrings(append(primary.QueryIntents, extra.QueryIntents...))
	primary.EntityRefs = analysisUniqueStrings(append(primary.EntityRefs, extra.EntityRefs...))
	primary.GraphRefs = analysisUniqueStrings(append(primary.GraphRefs, extra.GraphRefs...))
	if extra.Priority > primary.Priority {
		primary.Priority = extra.Priority
	}
	primary.Confidence = firstNonBlankAnalysisString(primary.Confidence, extra.Confidence)
	if extra.Score > primary.Score {
		primary.Score = extra.Score
	}
	return primary
}

func projectAnalysisDocLimitForIntent(intent ProjectAnalysisQAIntent) int {
	switch intent {
	case projectAnalysisQAIntentDeepMap:
		return 14
	case projectAnalysisQAIntentFlowTrace, projectAnalysisQAIntentImpact, projectAnalysisQAIntentSecuritySurface, projectAnalysisQAIntentUnrealStructure:
		return 12
	default:
		return 8
	}
}

func projectAnalysisModuleLimitForIntent(intent ProjectAnalysisQAIntent) int {
	switch intent {
	case projectAnalysisQAIntentDeepMap, projectAnalysisQAIntentModuleDrilldown, projectAnalysisQAIntentUnrealStructure:
		return 12
	default:
		return 6
	}
}

func projectAnalysisFolderLimitForIntent(intent ProjectAnalysisQAIntent) int {
	switch intent {
	case projectAnalysisQAIntentDeepMap, projectAnalysisQAIntentModuleDrilldown:
		return 12
	default:
		return 6
	}
}

func projectAnalysisVerificationLimitForIntent(intent ProjectAnalysisQAIntent) int {
	switch intent {
	case projectAnalysisQAIntentImpact, projectAnalysisQAIntentSecuritySurface, projectAnalysisQAIntentVerification:
		return 8
	default:
		return 4
	}
}

func projectAnalysisFuzzLimitForIntent(intent ProjectAnalysisQAIntent) int {
	if intent == projectAnalysisQAIntentSecuritySurface || intent == projectAnalysisQAIntentVerification {
		return 8
	}
	return 3
}

func projectAnalysisUnrealEdgeLimitForIntent(intent ProjectAnalysisQAIntent) int {
	if intent == projectAnalysisQAIntentUnrealStructure {
		return 16
	}
	return 6
}

func limitProjectStructureDocHits(items []ProjectStructureDocHit, limit int) []ProjectStructureDocHit {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]ProjectStructureDocHit(nil), items...)
	}
	return append([]ProjectStructureDocHit(nil), items[:limit]...)
}

func limitProjectStructureGraphViews(items []ProjectStructureGraphView, limit int) []ProjectStructureGraphView {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]ProjectStructureGraphView(nil), items...)
	}
	return append([]ProjectStructureGraphView(nil), items[:limit]...)
}

func limitProjectStructureCriticalAnchors(items []ProjectStructureCriticalAnchor, limit int) []ProjectStructureCriticalAnchor {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]ProjectStructureCriticalAnchor(nil), items...)
	}
	return append([]ProjectStructureCriticalAnchor(nil), items[:limit]...)
}

func limitAnalysisGraphEdgeViews(items []analysisGraphEdgeView, limit int) []analysisGraphEdgeView {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]analysisGraphEdgeView(nil), items...)
	}
	return append([]analysisGraphEdgeView(nil), items[:limit]...)
}

func limitFileRecords(items []FileRecord, limit int) []FileRecord {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]FileRecord(nil), items...)
	}
	return append([]FileRecord(nil), items[:limit]...)
}

func limitSemanticPaths(items []SemanticPathV2, limit int) []SemanticPathV2 {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]SemanticPathV2(nil), items...)
	}
	return append([]SemanticPathV2(nil), items[:limit]...)
}

func limitOverlayEdges(items []OverlayEdge, limit int) []OverlayEdge {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]OverlayEdge(nil), items...)
	}
	return append([]OverlayEdge(nil), items[:limit]...)
}

func limitUnrealSemanticEdges(items []UnrealSemanticEdge, limit int) []UnrealSemanticEdge {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]UnrealSemanticEdge(nil), items...)
	}
	return append([]UnrealSemanticEdge(nil), items[:limit]...)
}

func limitVerificationEntries(items []AnalysisVerificationMatrixEntry, limit int) []AnalysisVerificationMatrixEntry {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]AnalysisVerificationMatrixEntry(nil), items...)
	}
	return append([]AnalysisVerificationMatrixEntry(nil), items[:limit]...)
}
