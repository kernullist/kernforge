package main

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
)

type SpecialistAssignment struct {
	Profile SpecialistSubagentProfile
	Reason  string
	Score   int
}

func defaultSpecialistProfiles() []SpecialistSubagentProfile {
	return []SpecialistSubagentProfile{
		{
			Name:           "implementation-owner",
			Description:    "Owns ordinary product code edits, refactors, and feature implementation slices when no narrower domain specialist fits.",
			Prompt:         "Act like the concrete implementation owner. Focus on the smallest correct code change, keep related verification tight, and avoid spilling across unrelated files.",
			NodeKinds:      []string{"edit", "task", "verification"},
			Keywords:       []string{"edit", "fix", "implement", "update", "refactor", "feature", "code", "ownership"},
			ReadOnly:       boolPtr(false),
			Editable:       boolPtr(true),
			OwnershipPaths: []string{"**"},
		},
		{
			Name:        "planner",
			Description: "General-purpose planning specialist for decomposition, dependency order, and next-step clarity.",
			Prompt:      "Focus on sequencing, hidden dependencies, and the smallest safe next step.",
			NodeKinds:   []string{"task", "summary", "edit"},
			Keywords:    []string{"plan", "task", "next step", "sequence", "dependency", "implement"},
			ReadOnly:    boolPtr(true),
			Editable:    boolPtr(false),
		},
		{
			Name:        "reviewer",
			Description: "General-purpose review specialist for correctness, regression risk, and verification coverage.",
			Prompt:      "Prioritize correctness, regression risk, and missing validation before polish.",
			NodeKinds:   []string{"verification", "edit", "summary"},
			Keywords:    []string{"verify", "verification", "correctness", "regression", "test", "failure"},
			ReadOnly:    boolPtr(true),
			Editable:    boolPtr(false),
		},
		{
			Name:        "kernel-investigator",
			Description: "Specializes in Windows kernel, driver, service, and verifier evidence.",
			Prompt:      "Think like a Windows kernel investigator. Watch for driver state, symbols, verifier, packaging, and service lifecycle blind spots.",
			NodeKinds:   []string{"inspection", "verification"},
			Keywords:    []string{"kernel", "driver", ".sys", "verifier", "service", "pdb", "symbol", "minifilter", "wdf"},
			ReadOnly:    boolPtr(true),
			Editable:    boolPtr(false),
		},
		{
			Name:           "driver-build-fixer",
			Description:    "Focuses on driver build, packaging, catalog, INF, and signing issues.",
			Prompt:         "Prioritize build graph failures, packaging drift, INF and CAT integrity, and signing readiness.",
			NodeKinds:      []string{"edit", "verification"},
			Keywords:       []string{"build", "msbuild", "link", "sign", "signtool", "inf", "cat", "package", "driver"},
			ReadOnly:       boolPtr(true),
			Editable:       boolPtr(true),
			OwnershipPaths: []string{"driver/**", "drivers/**", "kernel/**", "*.sys", "*.inf", "*.cat", "*.vcxproj", "*.props", "*.targets"},
		},
		{
			Name:           "telemetry-analyst",
			Description:    "Focuses on ETW, WPP, provider schema, manifest, and telemetry drift.",
			Prompt:         "Treat telemetry as an operational contract. Look for schema drift, provider mismatches, missing fields, and consumer breakage.",
			NodeKinds:      []string{"inspection", "verification", "edit"},
			Keywords:       []string{"telemetry", "etw", "wpp", "manifest", "provider", "schema", "trace", "event"},
			ReadOnly:       boolPtr(true),
			Editable:       boolPtr(true),
			OwnershipPaths: []string{"telemetry/**", "etw/**", "trace/**", "providers/**", "*.man", "*.xml"},
		},
		{
			Name:           "unreal-integrity-reviewer",
			Description:    "Focuses on Unreal integrity, replication, gameplay, startup, and asset coupling.",
			Prompt:         "Reason about Unreal module boundaries, UBT and UHT behavior, replication surfaces, startup config, and cooked asset integrity.",
			NodeKinds:      []string{"inspection", "verification", "edit"},
			Keywords:       []string{"unreal", "uproject", "uplugin", "ubt", "uht", "replication", "asset", "pak", "integrity"},
			ReadOnly:       boolPtr(true),
			Editable:       boolPtr(true),
			OwnershipPaths: []string{"Source/**", "Plugins/**", "Config/**", "Content/**", "*.uproject", "*.uplugin"},
		},
		{
			Name:           "memory-inspection-reviewer",
			Description:    "Focuses on memory inspection, scanner quality, false positives, and evidence coverage.",
			Prompt:         "Watch for false positives, stale assumptions, address-space blind spots, and scanner regression coverage gaps.",
			NodeKinds:      []string{"inspection", "verification", "edit"},
			Keywords:       []string{"memory", "scanner", "scan", "signature", "page", "rwx", "handle", "dump"},
			ReadOnly:       boolPtr(true),
			Editable:       boolPtr(true),
			OwnershipPaths: []string{"memory/**", "scanner/**", "signatures/**", "patterns/**", "*.sig", "*.pat"},
		},
		{
			Name:        "attack-surface-reviewer",
			Description: "Focuses on attack surface, tamper paths, trust boundaries, and bypass risk.",
			Prompt:      "Look for tamper surface, privilege boundaries, bypass paths, and forensic blind spots before implementation convenience.",
			NodeKinds:   []string{"inspection", "summary", "verification"},
			Keywords:    []string{"attack", "tamper", "bypass", "surface", "boundary", "hook", "inject", "trust"},
			ReadOnly:    boolPtr(true),
			Editable:    boolPtr(false),
		},
	}
}

func configuredSpecialistProfiles(cfg Config) []SpecialistSubagentProfile {
	base := defaultSpecialistProfiles()
	if len(cfg.Specialists.Profiles) == 0 {
		return normalizeSpecialistProfiles(base)
	}
	order := make([]string, 0, len(base)+len(cfg.Specialists.Profiles))
	merged := make(map[string]SpecialistSubagentProfile, len(base)+len(cfg.Specialists.Profiles))
	for _, profile := range base {
		key := normalizeSpecialistProfileName(profile.Name)
		if key == "" {
			continue
		}
		order = append(order, key)
		merged[key] = normalizeSpecialistProfile(profile)
	}
	for _, overlay := range cfg.Specialists.Profiles {
		key := normalizeSpecialistProfileName(overlay.Name)
		if key == "" {
			continue
		}
		if existing, ok := merged[key]; ok {
			merged[key] = mergeSpecialistProfile(existing, overlay)
			continue
		}
		order = append(order, key)
		merged[key] = normalizeSpecialistProfile(overlay)
	}
	out := make([]SpecialistSubagentProfile, 0, len(order))
	for _, key := range order {
		profile, ok := merged[key]
		if !ok {
			continue
		}
		out = append(out, normalizeSpecialistProfile(profile))
	}
	return out
}

func normalizeSpecialistProfiles(items []SpecialistSubagentProfile) []SpecialistSubagentProfile {
	out := make([]SpecialistSubagentProfile, 0, len(items))
	for _, item := range items {
		item = normalizeSpecialistProfile(item)
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func normalizeSpecialistProfile(profile SpecialistSubagentProfile) SpecialistSubagentProfile {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Description = strings.TrimSpace(profile.Description)
	profile.Prompt = strings.TrimSpace(profile.Prompt)
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.NodeKinds = normalizeTaskStateList(profile.NodeKinds, 16)
	profile.Keywords = normalizeTaskStateList(profile.Keywords, 32)
	profile.OwnershipPaths = normalizeTaskStateList(profile.OwnershipPaths, 32)
	return profile
}

func mergeSpecialistProfile(base SpecialistSubagentProfile, overlay SpecialistSubagentProfile) SpecialistSubagentProfile {
	merged := normalizeSpecialistProfile(base)
	overlay = normalizeSpecialistProfile(overlay)
	if overlay.Name != "" {
		merged.Name = overlay.Name
	}
	if overlay.Description != "" {
		merged.Description = overlay.Description
	}
	if overlay.Prompt != "" {
		merged.Prompt = overlay.Prompt
	}
	if overlay.Provider != "" {
		merged.Provider = overlay.Provider
	}
	if overlay.Model != "" {
		merged.Model = overlay.Model
	}
	if overlay.BaseURL != "" {
		merged.BaseURL = overlay.BaseURL
	}
	if overlay.APIKey != "" {
		merged.APIKey = overlay.APIKey
	}
	if len(overlay.NodeKinds) > 0 {
		merged.NodeKinds = append([]string(nil), overlay.NodeKinds...)
	}
	if len(overlay.Keywords) > 0 {
		merged.Keywords = append([]string(nil), overlay.Keywords...)
	}
	if overlay.ReadOnly != nil {
		value := *overlay.ReadOnly
		merged.ReadOnly = &value
	}
	if overlay.Editable != nil {
		value := *overlay.Editable
		merged.Editable = &value
	}
	if len(overlay.OwnershipPaths) > 0 {
		merged.OwnershipPaths = append([]string(nil), overlay.OwnershipPaths...)
	}
	return normalizeSpecialistProfile(merged)
}

func normalizeSpecialistProfileName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func specialistProfileReadOnly(profile SpecialistSubagentProfile) bool {
	if profile.ReadOnly == nil {
		return true
	}
	return *profile.ReadOnly
}

func specialistProfileEditable(profile SpecialistSubagentProfile) bool {
	if profile.Editable == nil {
		return false
	}
	return *profile.Editable
}

func configuredSpecialistProfileByName(cfg Config, name string) (SpecialistSubagentProfile, bool) {
	target := normalizeSpecialistProfileName(name)
	if target == "" {
		return SpecialistSubagentProfile{}, false
	}
	for _, profile := range configuredSpecialistProfiles(cfg) {
		if normalizeSpecialistProfileName(profile.Name) == target {
			return profile, true
		}
	}
	return SpecialistSubagentProfile{}, false
}

func specialistProfileHint(profile SpecialistSubagentProfile) string {
	parts := make([]string, 0, 4)
	if len(profile.NodeKinds) > 0 {
		parts = append(parts, "kinds="+strings.Join(profile.NodeKinds, ","))
	}
	if len(profile.Keywords) > 0 {
		limit := min(5, len(profile.Keywords))
		parts = append(parts, "keywords="+strings.Join(profile.Keywords[:limit], ","))
	}
	if strings.TrimSpace(profile.Provider) != "" || strings.TrimSpace(profile.Model) != "" {
		parts = append(parts, "model="+strings.TrimSpace(strings.TrimSpace(profile.Provider)+" / "+strings.TrimSpace(profile.Model)))
	}
	if specialistProfileReadOnly(profile) {
		parts = append(parts, "read_only")
	}
	if specialistProfileEditable(profile) {
		parts = append(parts, "editable")
	}
	if len(profile.OwnershipPaths) > 0 {
		limit := min(4, len(profile.OwnershipPaths))
		parts = append(parts, "ownership="+strings.Join(profile.OwnershipPaths[:limit], ","))
	}
	return strings.Join(parts, " | ")
}

func specialistCatalogSummary(cfg Config) []string {
	items := specialistCatalogItems(cfg)
	if len(items) == 0 {
		return []string{"(no specialist profiles configured)"}
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		line := item.Name
		if item.Description != "" {
			line += "  " + item.Description
		}
		if item.Hint != "" {
			hint := item.Hint
			line += "  [" + hint + "]"
		}
		lines = append(lines, line)
	}
	return lines
}

type specialistCatalogItem struct {
	Group       string
	Name        string
	Description string
	Hint        string
}

func specialistCatalogItems(cfg Config) []specialistCatalogItem {
	profiles := configuredSpecialistProfiles(cfg)
	if len(profiles) == 0 {
		return nil
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		return strings.Compare(strings.ToLower(profiles[i].Name), strings.ToLower(profiles[j].Name)) < 0
	})
	items := make([]specialistCatalogItem, 0, len(profiles))
	for _, profile := range profiles {
		items = append(items, specialistCatalogItem{
			Group:       specialistCatalogGroup(profile),
			Name:        profile.Name,
			Description: strings.TrimSpace(profile.Description),
			Hint:        specialistProfileHint(profile),
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		groupI := specialistCatalogGroupOrder(items[i].Group)
		groupJ := specialistCatalogGroupOrder(items[j].Group)
		if groupI != groupJ {
			return groupI < groupJ
		}
		return strings.Compare(strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)) < 0
	})
	return items
}

func formatSpecialistCatalog(cfg Config) string {
	items := specialistCatalogItems(cfg)
	if len(items) == 0 {
		return "(no specialist profiles configured)"
	}
	var buf bytes.Buffer
	currentGroup := ""
	var tw *tabwriter.Writer
	flushGroup := func() {
		if tw != nil {
			_ = tw.Flush()
			tw = nil
		}
	}
	for _, item := range items {
		if item.Group != currentGroup {
			flushGroup()
			if buf.Len() > 0 {
				buf.WriteString("\n\n")
			}
			currentGroup = item.Group
			buf.WriteString(currentGroup)
			buf.WriteString(" specialists:\n")
			tw = tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
		}
		fmt.Fprintf(tw, "%s\t%s\n", item.Name, item.Description)
		if item.Hint != "" {
			fmt.Fprintf(tw, "\t[%s]\n", item.Hint)
		}
	}
	flushGroup()
	return strings.TrimRight(buf.String(), "\n")
}

func formatSpecialistCatalogWithUI(ui UI, cfg Config) string {
	items := specialistCatalogItems(cfg)
	if len(items) == 0 {
		return "(no specialist profiles configured)"
	}
	var buf strings.Builder
	currentGroup := ""
	groupItems := make([]specialistCatalogItem, 0, len(items))
	flushGroup := func() {
		if len(groupItems) == 0 {
			return
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(ui.subsection(currentGroup + " Specialists"))
		buf.WriteString("\n")
		nameWidth := 0
		for _, item := range groupItems {
			if width := visibleLen(item.Name); width > nameWidth {
				nameWidth = width
			}
		}
		for index, item := range groupItems {
			if index > 0 {
				buf.WriteString("\n")
			}
			styledName := ui.bold(ui.accent(item.Name))
			buf.WriteString(padDisplayRight(styledName, nameWidth))
			if strings.TrimSpace(item.Description) != "" {
				buf.WriteString("  ")
				buf.WriteString(item.Description)
			}
			if item.Hint != "" {
				buf.WriteString("\n")
				buf.WriteString(strings.Repeat(" ", nameWidth+2))
				buf.WriteString(ui.dim("[" + item.Hint + "]"))
			}
		}
		groupItems = groupItems[:0]
	}
	for _, item := range items {
		if item.Group != currentGroup {
			flushGroup()
			currentGroup = item.Group
		}
		groupItems = append(groupItems, item)
	}
	flushGroup()
	return strings.TrimRight(buf.String(), "\n")
}

func specialistCatalogGroup(profile SpecialistSubagentProfile) string {
	switch normalizeSpecialistProfileName(profile.Name) {
	case "implementation-owner", "planner", "reviewer":
		return "General-purpose"
	}
	if defaultSpecialistProfileKnown(profile.Name) {
		return "Domain-specific"
	}
	return "Project-specific"
}

func specialistCatalogGroupOrder(group string) int {
	switch strings.TrimSpace(strings.ToLower(group)) {
	case "general-purpose":
		return 0
	case "project-specific":
		return 1
	case "domain-specific":
		return 2
	default:
		return 3
	}
}

func defaultSpecialistProfileKnown(name string) bool {
	target := normalizeSpecialistProfileName(name)
	if target == "" {
		return false
	}
	for _, profile := range defaultSpecialistProfiles() {
		if normalizeSpecialistProfileName(profile.Name) == target {
			return true
		}
	}
	return false
}

func specialistSelectionText(node TaskNode, state *TaskState, trigger string) string {
	parts := []string{
		node.Title,
		node.Kind,
		node.Status,
		node.LifecycleNote,
		node.LastFailureTool,
		node.LastFailure,
		node.MicroWorkerBrief,
		node.ReadOnlyWorkerSummary,
		trigger,
	}
	if state != nil {
		parts = append(parts, state.Goal, state.PlanSummary)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func selectSpecialistForTaskNode(cfg Config, node TaskNode, state *TaskState, trigger string, requireReadOnly bool) (SpecialistAssignment, bool) {
	if !configSpecialistsEnabled(cfg) {
		return SpecialistAssignment{}, false
	}
	text := specialistSelectionText(node, state, trigger)
	best := SpecialistAssignment{}
	for _, profile := range configuredSpecialistProfiles(cfg) {
		if requireReadOnly && !specialistProfileReadOnly(profile) {
			continue
		}
		score := 0
		reasons := make([]string, 0, 3)
		nodeKind := strings.ToLower(strings.TrimSpace(node.Kind))
		for _, kind := range profile.NodeKinds {
			if strings.EqualFold(kind, nodeKind) {
				score += 35
				reasons = append(reasons, "kind="+nodeKind)
				break
			}
		}
		keywordHits := 0
		for _, keyword := range profile.Keywords {
			needle := strings.ToLower(strings.TrimSpace(keyword))
			if needle == "" || !strings.Contains(text, needle) {
				continue
			}
			keywordHits++
		}
		if keywordHits > 0 {
			score += min(keywordHits, 4) * 18
			reasons = append(reasons, fmt.Sprintf("keywords=%d", keywordHits))
		}
		switch normalizeSpecialistProfileName(profile.Name) {
		case "reviewer":
			if strings.EqualFold(node.Kind, "verification") || strings.Contains(text, "retry") || strings.Contains(text, "failure") {
				score += 12
			}
		case "planner":
			if strings.Contains(text, "plan") || strings.Contains(text, "next") || strings.Contains(text, "dependency") {
				score += 10
			}
		}
		if score <= 0 {
			continue
		}
		reason := strings.Join(reasons, ", ")
		if reason == "" {
			reason = "default"
		}
		if score > best.Score {
			best = SpecialistAssignment{
				Profile: profile,
				Reason:  reason,
				Score:   score,
			}
		}
	}
	if best.Score > 0 {
		return best, true
	}
	fallbackName := "planner"
	if strings.EqualFold(strings.TrimSpace(node.Kind), "verification") {
		fallbackName = "reviewer"
	}
	for _, profile := range configuredSpecialistProfiles(cfg) {
		if normalizeSpecialistProfileName(profile.Name) != fallbackName {
			continue
		}
		if requireReadOnly && !specialistProfileReadOnly(profile) {
			continue
		}
		return SpecialistAssignment{
			Profile: profile,
			Reason:  "fallback",
			Score:   1,
		}, true
	}
	return SpecialistAssignment{}, false
}

func selectEditableSpecialistForTaskNode(cfg Config, node TaskNode, state *TaskState, trigger string) (SpecialistAssignment, bool) {
	if !configSpecialistsEnabled(cfg) {
		return SpecialistAssignment{}, false
	}
	text := specialistSelectionText(node, state, trigger)
	best := SpecialistAssignment{}
	for _, profile := range configuredSpecialistProfiles(cfg) {
		if !specialistProfileEditable(profile) {
			continue
		}
		score := 0
		reasons := make([]string, 0, 4)
		nodeKind := strings.ToLower(strings.TrimSpace(node.Kind))
		for _, kind := range profile.NodeKinds {
			if strings.EqualFold(kind, nodeKind) {
				score += 45
				reasons = append(reasons, "kind="+nodeKind)
				break
			}
		}
		keywordHits := 0
		for _, keyword := range profile.Keywords {
			needle := strings.ToLower(strings.TrimSpace(keyword))
			if needle == "" || !strings.Contains(text, needle) {
				continue
			}
			keywordHits++
		}
		if keywordHits > 0 {
			score += min(keywordHits, 5) * 16
			reasons = append(reasons, fmt.Sprintf("keywords=%d", keywordHits))
		}
		switch normalizeSpecialistProfileName(profile.Name) {
		case "implementation-owner":
			if nodeKind == "edit" || strings.Contains(text, "implement") || strings.Contains(text, "ownership") {
				score += 18
			}
		case "driver-build-fixer":
			if strings.Contains(text, "build") || strings.Contains(text, "sign") {
				score += 12
			}
		case "telemetry-analyst":
			if strings.Contains(text, "etw") || strings.Contains(text, "telemetry") {
				score += 12
			}
		case "unreal-integrity-reviewer":
			if strings.Contains(text, "unreal") || strings.Contains(text, "uproject") {
				score += 12
			}
		case "memory-inspection-reviewer":
			if strings.Contains(text, "memory") || strings.Contains(text, "scanner") {
				score += 12
			}
		}
		if score <= 0 {
			continue
		}
		reason := strings.Join(reasons, ", ")
		if reason == "" {
			reason = "editable"
		}
		if score > best.Score {
			best = SpecialistAssignment{
				Profile: profile,
				Reason:  reason,
				Score:   score,
			}
		}
	}
	if best.Score > 0 {
		return best, true
	}
	if profile, ok := configuredSpecialistProfileByName(cfg, "implementation-owner"); ok && specialistProfileEditable(profile) {
		return SpecialistAssignment{
			Profile: profile,
			Reason:  "editable-fallback",
			Score:   1,
		}, true
	}
	return SpecialistAssignment{}, false
}

func (a *Agent) specialistClient(profile SpecialistSubagentProfile) (ProviderClient, string) {
	if a == nil {
		return nil, ""
	}
	cfg := a.Config
	provider := firstNonBlankString(profile.Provider, cfg.Provider)
	if provider == "" && a.Session != nil {
		provider = strings.TrimSpace(a.Session.Provider)
	}
	model := firstNonBlankString(profile.Model, cfg.Model)
	if model == "" && a.Session != nil {
		model = strings.TrimSpace(a.Session.Model)
	}
	if provider == "" || model == "" {
		return a.ensureInteractiveReviewerClient()
	}
	cfg.Provider = provider
	cfg.Model = model
	if strings.TrimSpace(profile.BaseURL) != "" {
		cfg.BaseURL = strings.TrimSpace(profile.BaseURL)
	} else {
		cfg.BaseURL = normalizeProfileBaseURL(provider, cfg.BaseURL)
	}
	if strings.TrimSpace(profile.APIKey) != "" {
		cfg.APIKey = strings.TrimSpace(profile.APIKey)
	} else if cfg.ProviderKeys != nil {
		if key := strings.TrimSpace(cfg.ProviderKeys[strings.ToLower(provider)]); key != "" {
			cfg.APIKey = key
		}
	}
	client, err := NewProviderClient(cfg)
	if err != nil {
		return a.ensureInteractiveReviewerClient()
	}
	return client, cfg.Model
}

func buildSpecialistMicroWorkerSystemPrompt(profile SpecialistSubagentProfile) string {
	lines := []string{
		"You are a specialist micro-worker assisting a terminal coding agent.",
		"Focus on one task-graph node and return a short brief with the most likely risk, next check, and why the node matters.",
		"Keep the answer under 4 short bullets.",
	}
	if strings.TrimSpace(profile.Name) != "" {
		lines = append(lines, "Specialist role: "+profile.Name)
	}
	if strings.TrimSpace(profile.Description) != "" {
		lines = append(lines, profile.Description)
	}
	if strings.TrimSpace(profile.Prompt) != "" {
		lines = append(lines, profile.Prompt)
	}
	return strings.Join(lines, "\n")
}

func buildSpecialistMicroWorkerPrompt(profile SpecialistSubagentProfile, state *TaskState, node TaskNode, trigger string, reason string) string {
	var b strings.Builder
	if strings.TrimSpace(profile.Name) != "" {
		b.WriteString("Assigned specialist: ")
		b.WriteString(profile.Name)
		b.WriteString("\n")
	}
	if strings.TrimSpace(reason) != "" {
		b.WriteString("Routing reason: ")
		b.WriteString(reason)
		b.WriteString("\n\n")
	}
	b.WriteString(buildInteractiveMicroWorkerPrompt(state, node, trigger))
	return b.String()
}

func (rt *runtimeState) handleSpecialistsStatus() error {
	fmt.Fprintln(rt.writer, rt.ui.section("Specialists"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("enabled", fmt.Sprintf("%t", configSpecialistsEnabled(rt.cfg))))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Most normal app, backend, frontend, and tooling work routes through implementation-owner, planner, or reviewer first. Domain specialists engage when task text or file paths strongly match."))
	if catalog := strings.TrimSpace(formatSpecialistCatalogWithUI(rt.ui, rt.cfg)); catalog != "" {
		fmt.Fprintln(rt.writer, catalog)
	}
	if rt == nil || rt.session == nil {
		return nil
	}
	if len(rt.session.SpecialistWorktrees) > 0 {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.subsection("Editable Worktrees"))
		for _, lease := range rt.session.SpecialistWorktrees {
			lease.Normalize()
			line := fmt.Sprintf("- %s  root=%s", lease.Specialist, lease.Root)
			if lease.Branch != "" {
				line += "  branch=" + lease.Branch
			}
			if len(lease.OwnershipPaths) > 0 {
				line += "  ownership=" + strings.Join(lease.OwnershipPaths, ",")
			}
			if len(lease.NodeIDs) > 0 {
				line += "  nodes=" + strings.Join(lease.NodeIDs, ",")
			}
			fmt.Fprintln(rt.writer, line)
		}
	}
	if rt.session.TaskGraph != nil {
		lines := make([]string, 0)
		for _, node := range rt.session.TaskGraph.Nodes {
			if strings.TrimSpace(node.EditableSpecialist) == "" {
				continue
			}
			line := fmt.Sprintf("- %s  %s", node.ID, node.EditableSpecialist)
			if node.EditableWorktreeRoot != "" {
				line += "  root=" + node.EditableWorktreeRoot
			}
			if len(node.EditableOwnershipPaths) > 0 {
				line += "  ownership=" + strings.Join(node.EditableOwnershipPaths, ",")
			}
			if len(node.EditableLeasePaths) > 0 {
				line += "  lease=" + strings.Join(node.EditableLeasePaths, ",")
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			fmt.Fprintln(rt.writer)
			fmt.Fprintln(rt.writer, rt.ui.subsection("Editable Assignments"))
			for _, line := range lines {
				fmt.Fprintln(rt.writer, line)
			}
		}
	}
	return nil
}

func (rt *runtimeState) handleSpecialistsAssign(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("session is not initialized")
	}
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) < 2 {
		return fmt.Errorf("usage: /specialists assign <node-id> <specialist> [glob,glob2]")
	}
	nodeID := strings.TrimSpace(fields[0])
	profileName := strings.TrimSpace(fields[1])
	graph := rt.session.EnsureTaskGraph()
	if graph == nil {
		return fmt.Errorf("task graph is not available")
	}
	node, ok := graph.Node(nodeID)
	if !ok {
		return fmt.Errorf("task node not found: %s", nodeID)
	}
	profile, ok := configuredSpecialistProfileByName(rt.cfg, profileName)
	if !ok {
		return fmt.Errorf("unknown specialist profile: %s", profileName)
	}
	if !specialistProfileEditable(profile) {
		return fmt.Errorf("specialist is not editable: %s", profile.Name)
	}
	rawPatterns := strings.TrimSpace(strings.Join(fields[2:], " "))
	patterns := append([]string(nil), profile.OwnershipPaths...)
	if rawPatterns != "" {
		parts := strings.Split(rawPatterns, ",")
		patterns = normalizeTaskStateList(parts, 32)
	}
	assignment := SpecialistAssignment{
		Profile: profile,
		Reason:  "manual-assign",
		Score:   1,
	}
	lease, err := rt.ensureSpecialistWorktreeLease(node.ID, assignment, patterns, false, true)
	if err != nil {
		return err
	}
	rt.recordEditableAssignment(node.ID, assignment, patterns, lease)
	rt.session.RecordTaskGraphEditableLease(node.ID, patterns, "manual-assign")
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Assigned editable specialist "+profile.Name+" to "+node.ID))
	if lease.Root != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_root", lease.Root))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_branch", valueOrUnset(lease.Branch)))
	}
	if len(patterns) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("ownership", strings.Join(patterns, ",")))
	}
	return nil
}

func (rt *runtimeState) handleSpecialistsCleanup(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("session is not initialized")
	}
	target := normalizeSpecialistProfileName(args)
	if target == "" {
		return fmt.Errorf("usage: /specialists cleanup <specialist|all>")
	}
	leases := append([]SpecialistWorktree(nil), rt.session.SpecialistWorktrees...)
	if len(leases) == 0 {
		return fmt.Errorf("no specialist worktrees are recorded for this session")
	}
	manager := newWorktreeManager(rt.cfg)
	removed := 0
	for _, lease := range leases {
		name := normalizeSpecialistProfileName(lease.Specialist)
		if target != "all" && name != target {
			continue
		}
		sessionRecord := SessionWorktree{
			ID:      lease.Specialist,
			Root:    lease.Root,
			Branch:  lease.Branch,
			Managed: lease.Managed,
			Active:  false,
		}
		if err := manager.Remove(context.Background(), sessionBaseWorkingDir(rt.session), sessionRecord); err != nil {
			return err
		}
		rt.session.RemoveSpecialistWorktree(lease.Specialist)
		rt.clearEditableWorktreeMetadataForSpecialist(lease.Specialist)
		removed++
	}
	if removed == 0 {
		return fmt.Errorf("no specialist worktrees matched: %s", args)
	}
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Removed %d specialist worktree(s)", removed)))
	return nil
}

func (rt *runtimeState) handleSpecialistsCommand(args string) error {
	parts := strings.Fields(strings.TrimSpace(args))
	subcommand := "status"
	if len(parts) > 0 {
		subcommand = strings.ToLower(strings.TrimSpace(parts[0]))
	}
	switch subcommand {
	case "", "status":
		return rt.handleSpecialistsStatus()
	case "assign":
		return rt.handleSpecialistsAssign(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), parts[0])))
	case "cleanup":
		return rt.handleSpecialistsCleanup(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), parts[0])))
	default:
		return fmt.Errorf("usage: /specialists [status|assign <node-id> <specialist> [glob,glob2]|cleanup <specialist|all>]")
	}
}
