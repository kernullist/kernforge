package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type TaskGraph struct {
	Nodes       []TaskNode `json:"nodes,omitempty"`
	LastUpdated time.Time  `json:"last_updated,omitempty"`
}

type TaskNode struct {
	ID                    string    `json:"id"`
	Title                 string    `json:"title"`
	Kind                  string    `json:"kind,omitempty"`
	Status                string    `json:"status,omitempty"`
	DependsOn             []string  `json:"depends_on,omitempty"`
	LinkedBundleIDs       []string  `json:"linked_bundle_ids,omitempty"`
	LinkedJobIDs          []string  `json:"linked_job_ids,omitempty"`
	MicroWorkerBrief      string    `json:"micro_worker_brief,omitempty"`
	ReadOnlyWorkerTool    string    `json:"read_only_worker_tool,omitempty"`
	ReadOnlyWorkerSummary string    `json:"read_only_worker_summary,omitempty"`
	ReadOnlyWorkerAt      time.Time `json:"read_only_worker_at,omitempty"`
	RetryBudget           int       `json:"retry_budget,omitempty"`
	RetryUsed             int       `json:"retry_used,omitempty"`
	LastFailureTool       string    `json:"last_failure_tool,omitempty"`
	LastFailure           string    `json:"last_failure,omitempty"`
	LifecycleNote         string    `json:"lifecycle_note,omitempty"`
	LastUpdated           time.Time `json:"last_updated,omitempty"`
}

func newTaskGraphFromPlan(items []PlanItem) *TaskGraph {
	if len(items) == 0 {
		return nil
	}
	graph := &TaskGraph{
		Nodes:       make([]TaskNode, 0, len(items)),
		LastUpdated: time.Now(),
	}
	previousID := ""
	for index, item := range items {
		nodeID := fmt.Sprintf("plan-%02d", index+1)
		node := TaskNode{
			ID:          nodeID,
			Title:       strings.TrimSpace(item.Step),
			Kind:        inferTaskNodeKind(item.Step),
			Status:      canonicalTaskNodeStatus(item.Status),
			LastUpdated: time.Now(),
		}
		if previousID != "" {
			node.DependsOn = []string{previousID}
		}
		graph.Nodes = append(graph.Nodes, node)
		previousID = nodeID
	}
	graph.Normalize()
	graph.SyncPlanStatuses(items)
	return graph
}

func inferTaskNodeKind(step string) string {
	lower := strings.ToLower(strings.TrimSpace(step))
	switch {
	case strings.Contains(lower, "verification"), strings.Contains(lower, "verify"), strings.Contains(lower, "test"), strings.Contains(lower, "build"):
		return "verification"
	case strings.Contains(lower, "inspect"), strings.Contains(lower, "read"), strings.Contains(lower, "investigate"), strings.Contains(lower, "analyze"):
		return "inspection"
	case strings.Contains(lower, "edit"), strings.Contains(lower, "patch"), strings.Contains(lower, "fix"), strings.Contains(lower, "update"):
		return "edit"
	case strings.Contains(lower, "summar"), strings.Contains(lower, "report"), strings.Contains(lower, "explain"):
		return "summary"
	default:
		return "task"
	}
}

func defaultTaskNodeRetryBudget(kind string) int {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "verification":
		return 3
	case "inspection", "edit", "task":
		return 2
	case "summary", "background_bundle":
		return 1
	default:
		return 2
	}
}

func canonicalTaskNodeStatus(status string) string {
	normalized := strings.TrimSpace(strings.ToLower(status))
	switch normalized {
	case "ready", "in_progress", "completed", "failed", "stale", "superseded", "blocked", "canceled", "preempted":
		return normalized
	case "pending":
		return "pending"
	default:
		if normalized == "" {
			return "pending"
		}
		return normalized
	}
}

func (g *TaskGraph) Normalize() {
	if g == nil {
		return
	}
	normalized := make([]TaskNode, 0, len(g.Nodes))
	for _, node := range g.Nodes {
		node.Normalize()
		if strings.TrimSpace(node.ID) == "" || strings.TrimSpace(node.Title) == "" {
			continue
		}
		normalized = append(normalized, node)
	}
	g.Nodes = normalized
	if g.LastUpdated.IsZero() {
		g.LastUpdated = time.Now()
	}
}

func (n *TaskNode) Normalize() {
	n.ID = strings.TrimSpace(n.ID)
	n.Title = strings.Join(strings.Fields(strings.TrimSpace(n.Title)), " ")
	n.Kind = strings.TrimSpace(strings.ToLower(n.Kind))
	n.Status = canonicalTaskNodeStatus(n.Status)
	n.DependsOn = normalizeTaskStateList(n.DependsOn, 16)
	n.LinkedBundleIDs = normalizeTaskStateList(n.LinkedBundleIDs, 16)
	n.LinkedJobIDs = normalizeTaskStateList(n.LinkedJobIDs, 32)
	n.MicroWorkerBrief = strings.TrimSpace(n.MicroWorkerBrief)
	n.ReadOnlyWorkerTool = strings.TrimSpace(n.ReadOnlyWorkerTool)
	n.ReadOnlyWorkerSummary = strings.TrimSpace(n.ReadOnlyWorkerSummary)
	if n.RetryBudget <= 0 {
		n.RetryBudget = defaultTaskNodeRetryBudget(n.Kind)
	}
	if n.RetryUsed < 0 {
		n.RetryUsed = 0
	}
	if n.RetryUsed > n.RetryBudget && n.RetryBudget > 0 {
		n.RetryUsed = n.RetryBudget
	}
	n.LastFailureTool = strings.TrimSpace(n.LastFailureTool)
	n.LastFailure = strings.TrimSpace(n.LastFailure)
	n.LifecycleNote = strings.TrimSpace(n.LifecycleNote)
	if n.LastUpdated.IsZero() {
		n.LastUpdated = time.Now()
	}
}

func (g *TaskGraph) Touch() {
	if g == nil {
		return
	}
	g.LastUpdated = time.Now()
}

func (g *TaskGraph) SyncPlanStatuses(items []PlanItem) {
	if g == nil {
		return
	}
	previous := make(map[string]TaskNode, len(g.Nodes))
	for _, node := range g.Nodes {
		previous[strings.TrimSpace(node.ID)] = node
	}
	for index := range g.Nodes {
		if index >= len(items) {
			g.Nodes[index].Normalize()
			continue
		}
		prev, hasPrev := previous[strings.TrimSpace(g.Nodes[index].ID)]
		g.Nodes[index].Title = strings.TrimSpace(items[index].Step)
		g.Nodes[index].Status = canonicalTaskNodeStatus(items[index].Status)
		g.Nodes[index].Kind = inferTaskNodeKind(items[index].Step)
		if hasPrev {
			g.Nodes[index] = mergeTaskNodeRuntimeState(g.Nodes[index], prev)
		}
		g.Nodes[index].LastUpdated = time.Now()
	}
	g.refreshReadyNodes()
	g.Touch()
}

func (g *TaskGraph) refreshReadyNodes() {
	if g == nil {
		return
	}
	completed := make(map[string]struct{}, len(g.Nodes))
	for _, node := range g.Nodes {
		switch canonicalTaskNodeStatus(node.Status) {
		case "completed":
			completed[node.ID] = struct{}{}
		}
	}
	for index := range g.Nodes {
		if !isPrimaryTaskNode(g.Nodes[index]) {
			continue
		}
		status := canonicalTaskNodeStatus(g.Nodes[index].Status)
		if status != "pending" && status != "ready" {
			continue
		}
		depsReady := true
		for _, dep := range g.Nodes[index].DependsOn {
			if _, ok := completed[dep]; !ok {
				depsReady = false
				break
			}
		}
		if depsReady {
			g.Nodes[index].Status = "ready"
		} else if status == "ready" && !depsReady {
			g.Nodes[index].Status = "pending"
		}
	}
}

func mergeTaskNodeRuntimeState(current TaskNode, previous TaskNode) TaskNode {
	current.Normalize()
	previous.Normalize()
	if current.MicroWorkerBrief == "" {
		current.MicroWorkerBrief = previous.MicroWorkerBrief
	}
	if current.ReadOnlyWorkerTool == "" {
		current.ReadOnlyWorkerTool = previous.ReadOnlyWorkerTool
	}
	if current.ReadOnlyWorkerSummary == "" {
		current.ReadOnlyWorkerSummary = previous.ReadOnlyWorkerSummary
	}
	if current.ReadOnlyWorkerAt.IsZero() {
		current.ReadOnlyWorkerAt = previous.ReadOnlyWorkerAt
	}
	if current.RetryBudget <= 0 {
		current.RetryBudget = previous.RetryBudget
	}
	if current.RetryUsed == 0 && previous.RetryUsed > 0 {
		current.RetryUsed = previous.RetryUsed
	}
	if current.LastFailureTool == "" {
		current.LastFailureTool = previous.LastFailureTool
	}
	if current.LastFailure == "" {
		current.LastFailure = previous.LastFailure
	}
	if current.LifecycleNote == "" {
		current.LifecycleNote = previous.LifecycleNote
	}
	if len(current.LinkedBundleIDs) == 0 {
		current.LinkedBundleIDs = append([]string(nil), previous.LinkedBundleIDs...)
	}
	if len(current.LinkedJobIDs) == 0 {
		current.LinkedJobIDs = append([]string(nil), previous.LinkedJobIDs...)
	}
	switch previous.Status {
	case "blocked", "failed":
		if current.Status != "completed" {
			current.Status = previous.Status
		}
	}
	return current
}

func isPrimaryTaskNode(node TaskNode) bool {
	return !strings.EqualFold(strings.TrimSpace(node.Kind), "background_bundle") &&
		!strings.HasPrefix(strings.TrimSpace(node.ID), "bundle:")
}

func (g *TaskGraph) UpsertNode(node TaskNode) {
	if g == nil {
		return
	}
	node.Normalize()
	if node.ID == "" || node.Title == "" {
		return
	}
	for index := range g.Nodes {
		if strings.EqualFold(g.Nodes[index].ID, node.ID) {
			if node.MicroWorkerBrief == "" {
				node.MicroWorkerBrief = g.Nodes[index].MicroWorkerBrief
			}
			if node.ReadOnlyWorkerTool == "" {
				node.ReadOnlyWorkerTool = g.Nodes[index].ReadOnlyWorkerTool
			}
			if node.ReadOnlyWorkerSummary == "" {
				node.ReadOnlyWorkerSummary = g.Nodes[index].ReadOnlyWorkerSummary
			}
			if node.ReadOnlyWorkerAt.IsZero() {
				node.ReadOnlyWorkerAt = g.Nodes[index].ReadOnlyWorkerAt
			}
			if node.RetryBudget <= 0 {
				node.RetryBudget = g.Nodes[index].RetryBudget
			}
			if node.RetryUsed == 0 && g.Nodes[index].RetryUsed > 0 {
				node.RetryUsed = g.Nodes[index].RetryUsed
			}
			if node.LastFailureTool == "" {
				node.LastFailureTool = g.Nodes[index].LastFailureTool
			}
			if node.LastFailure == "" {
				node.LastFailure = g.Nodes[index].LastFailure
			}
			if node.LifecycleNote == "" {
				node.LifecycleNote = g.Nodes[index].LifecycleNote
			}
			if len(node.DependsOn) == 0 {
				node.DependsOn = g.Nodes[index].DependsOn
			}
			g.Nodes[index] = node
			g.refreshReadyNodes()
			g.Touch()
			return
		}
	}
	g.Nodes = append(g.Nodes, node)
	g.refreshReadyNodes()
	g.Touch()
}

func (g *TaskGraph) Node(nodeID string) (TaskNode, bool) {
	if g == nil {
		return TaskNode{}, false
	}
	for _, node := range g.Nodes {
		if strings.EqualFold(node.ID, strings.TrimSpace(nodeID)) {
			return node, true
		}
	}
	return TaskNode{}, false
}

func (g *TaskGraph) ReadyNodes(limit int) []TaskNode {
	if g == nil {
		return nil
	}
	out := make([]TaskNode, 0, len(g.Nodes))
	for _, node := range g.Nodes {
		if canonicalTaskNodeStatus(node.Status) == "ready" {
			out = append(out, node)
		}
	}
	if limit > 0 && len(out) > limit {
		return append([]TaskNode(nil), out[:limit]...)
	}
	return append([]TaskNode(nil), out...)
}

func (g *TaskGraph) ActiveNodes(limit int) []TaskNode {
	if g == nil {
		return nil
	}
	out := make([]TaskNode, 0, len(g.Nodes))
	for _, node := range g.Nodes {
		status := canonicalTaskNodeStatus(node.Status)
		if status == "in_progress" || status == "ready" {
			out = append(out, node)
		}
	}
	if limit > 0 && len(out) > limit {
		return append([]TaskNode(nil), out[:limit]...)
	}
	return append([]TaskNode(nil), out...)
}

func (g *TaskGraph) RecordMicroWorkerBrief(nodeID string, brief string) {
	if g == nil {
		return
	}
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return
	}
	for index := range g.Nodes {
		if !strings.EqualFold(g.Nodes[index].ID, strings.TrimSpace(nodeID)) {
			continue
		}
		g.Nodes[index].MicroWorkerBrief = brief
		g.Nodes[index].LastUpdated = time.Now()
		g.Touch()
		return
	}
}

func (g *TaskGraph) RecordReadOnlyWorkerEvidence(nodeID string, toolName string, summary string) {
	if g == nil {
		return
	}
	toolName = strings.TrimSpace(toolName)
	summary = strings.TrimSpace(summary)
	if toolName == "" && summary == "" {
		return
	}
	for index := range g.Nodes {
		if !strings.EqualFold(g.Nodes[index].ID, strings.TrimSpace(nodeID)) {
			continue
		}
		g.Nodes[index].ReadOnlyWorkerTool = toolName
		g.Nodes[index].ReadOnlyWorkerSummary = summary
		g.Nodes[index].ReadOnlyWorkerAt = time.Now()
		g.Nodes[index].LastUpdated = time.Now()
		g.Touch()
		return
	}
}

func (g *TaskGraph) RecordNodeFailure(nodeID string, toolName string, detail string) bool {
	if g == nil {
		return false
	}
	trimmedID := strings.TrimSpace(nodeID)
	if trimmedID == "" {
		return false
	}
	for index := range g.Nodes {
		if !strings.EqualFold(g.Nodes[index].ID, trimmedID) {
			continue
		}
		before := g.Nodes[index]
		node := before
		if node.RetryBudget <= 0 {
			node.RetryBudget = defaultTaskNodeRetryBudget(node.Kind)
		}
		if node.RetryUsed < node.RetryBudget {
			node.RetryUsed++
		}
		node.LastFailureTool = strings.TrimSpace(toolName)
		node.LastFailure = truncateStatusSnippet(firstNonEmptyLine(detail), 180)
		if node.RetryBudget > 0 && node.RetryUsed >= node.RetryBudget && canonicalTaskNodeStatus(node.Status) != "completed" {
			node.Status = "blocked"
			node.LifecycleNote = fmt.Sprintf("Retry budget exhausted for %s after %s", node.Title, firstNonBlankString(node.LastFailure, "a repeated failure"))
		}
		node.LastUpdated = time.Now()
		node.Normalize()
		g.Nodes[index] = node
		g.refreshReadyNodes()
		g.Touch()
		return before.Status != node.Status ||
			before.RetryUsed != node.RetryUsed ||
			before.LastFailure != node.LastFailure ||
			before.LastFailureTool != node.LastFailureTool ||
			before.LifecycleNote != node.LifecycleNote
	}
	return false
}

func (g *TaskGraph) SetNodeLifecycle(nodeID string, status string, note string) {
	if g == nil {
		return
	}
	changedBundleIDs := []string{}
	for index := range g.Nodes {
		if !strings.EqualFold(g.Nodes[index].ID, strings.TrimSpace(nodeID)) {
			continue
		}
		g.Nodes[index].Status = canonicalTaskNodeStatus(status)
		switch g.Nodes[index].Status {
		case "in_progress", "completed", "ready":
			g.Nodes[index].RetryUsed = 0
			g.Nodes[index].LastFailureTool = ""
			g.Nodes[index].LastFailure = ""
		}
		if strings.TrimSpace(note) != "" {
			g.Nodes[index].LifecycleNote = strings.TrimSpace(note)
		}
		g.Nodes[index].LastUpdated = time.Now()
		changedBundleIDs = append(changedBundleIDs, g.Nodes[index].LinkedBundleIDs...)
		g.refreshReadyNodes()
		g.Touch()
		break
	}
	if len(changedBundleIDs) == 0 && strings.HasPrefix(strings.TrimSpace(nodeID), "bundle:") {
		changedBundleIDs = append(changedBundleIDs, strings.TrimPrefix(strings.TrimSpace(nodeID), "bundle:"))
	}
	if len(changedBundleIDs) > 0 {
		g.propagateBundleLifecycle(changedBundleIDs, status, note)
	}
}

func (g *TaskGraph) propagateBundleLifecycle(bundleIDs []string, status string, note string) {
	if g == nil || len(bundleIDs) == 0 {
		return
	}
	targets := make(map[string]struct{}, len(bundleIDs))
	for _, bundleID := range bundleIDs {
		trimmedID := strings.TrimSpace(bundleID)
		if trimmedID != "" {
			targets[trimmedID] = struct{}{}
		}
	}
	if len(targets) == 0 {
		return
	}
	for index := range g.Nodes {
		if !isPrimaryTaskNode(g.Nodes[index]) {
			continue
		}
		matches := false
		for _, bundleID := range g.Nodes[index].LinkedBundleIDs {
			if _, ok := targets[strings.TrimSpace(bundleID)]; ok {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if strings.TrimSpace(note) != "" {
			g.Nodes[index].LifecycleNote = strings.TrimSpace(note)
		}
		switch canonicalTaskNodeStatus(status) {
		case "failed":
			if g.Nodes[index].Status != "completed" {
				g.Nodes[index].Status = "blocked"
			}
		case "running":
			if g.Nodes[index].Status == "pending" || g.Nodes[index].Status == "ready" {
				g.Nodes[index].Status = "in_progress"
			}
		case "stale", "superseded", "canceled", "preempted":
			if g.Nodes[index].Status != "completed" {
				g.Nodes[index].Status = "pending"
			}
		}
		g.Nodes[index].LastUpdated = time.Now()
	}
	g.refreshReadyNodes()
	g.Touch()
}

func (g *TaskGraph) RenderPromptSection() string {
	if g == nil {
		return ""
	}
	g.Normalize()
	if len(g.Nodes) == 0 {
		return ""
	}
	ready := g.ReadyNodes(3)
	active := g.ActiveNodes(4)
	completed := 0
	blocked := 0
	staleBundles := 0
	for _, node := range g.Nodes {
		switch canonicalTaskNodeStatus(node.Status) {
		case "completed":
			completed++
		case "blocked":
			blocked++
		case "stale", "superseded", "canceled", "preempted":
			if !isPrimaryTaskNode(node) {
				staleBundles++
			}
		}
	}
	lines := []string{
		fmt.Sprintf("- Task graph nodes: %d", len(g.Nodes)),
		fmt.Sprintf("- Task graph completed: %d", completed),
	}
	if blocked > 0 {
		lines = append(lines, fmt.Sprintf("- Task graph blocked: %d", blocked))
	}
	if staleBundles > 0 {
		lines = append(lines, fmt.Sprintf("- Stale background bundles: %d", staleBundles))
	}
	if len(ready) > 0 {
		items := make([]string, 0, len(ready))
		for _, node := range ready {
			items = append(items, fmt.Sprintf("%s (%s)", node.Title, node.Kind))
		}
		lines = append(lines, "- Ready nodes: "+strings.Join(items, " | "))
	}
	if len(active) > 0 {
		items := make([]string, 0, len(active))
		for _, node := range active {
			item := fmt.Sprintf("%s [%s]", node.Title, node.Status)
			if node.MicroWorkerBrief != "" {
				item += " | brief=" + compactPromptSection(node.MicroWorkerBrief, 120)
			}
			if node.ReadOnlyWorkerSummary != "" {
				item += " | worker=" + compactPromptSection(firstNonBlankString(node.ReadOnlyWorkerTool, "read_only")+": "+node.ReadOnlyWorkerSummary, 120)
			}
			if node.RetryBudget > 0 && node.RetryUsed > 0 {
				item += fmt.Sprintf(" | retries=%d/%d", node.RetryUsed, node.RetryBudget)
			}
			items = append(items, item)
		}
		lines = append(lines, "- Active nodes: "+strings.Join(items, " | "))
	}
	return strings.Join(lines, "\n")
}

func (g *TaskGraph) RenderExportSection() string {
	if g == nil || len(g.Nodes) == 0 {
		return ""
	}
	lines := make([]string, 0, len(g.Nodes))
	for _, node := range g.Nodes {
		line := fmt.Sprintf("- %s [%s] %s", node.ID, node.Status, node.Title)
		if len(node.LinkedBundleIDs) > 0 {
			line += " | bundles=" + strings.Join(node.LinkedBundleIDs, ", ")
		}
		if len(node.LinkedJobIDs) > 0 {
			line += " | jobs=" + strings.Join(node.LinkedJobIDs, ", ")
		}
		if node.MicroWorkerBrief != "" {
			line += " | brief=" + compactPromptSection(node.MicroWorkerBrief, 120)
		}
		if node.ReadOnlyWorkerSummary != "" {
			line += " | worker=" + compactPromptSection(firstNonBlankString(node.ReadOnlyWorkerTool, "read_only")+": "+node.ReadOnlyWorkerSummary, 120)
		}
		if node.RetryBudget > 0 && node.RetryUsed > 0 {
			line += fmt.Sprintf(" | retries=%d/%d", node.RetryUsed, node.RetryBudget)
		}
		if node.LastFailure != "" {
			line += " | last_failure=" + compactPromptSection(node.LastFailure, 100)
		}
		if node.LifecycleNote != "" {
			line += " | note=" + compactPromptSection(node.LifecycleNote, 120)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (g *TaskGraph) LinkBundle(bundle BackgroundShellBundle, jobs []BackgroundShellJob) string {
	if g == nil {
		return ""
	}
	bundle.Normalize()
	if bundle.ID == "" {
		return ""
	}
	node := TaskNode{
		ID:              "bundle:" + bundle.ID,
		Title:           firstNonBlankString(bundle.Summary, summarizeBackgroundBundleCommands(bundle.CommandSummaries), bundle.ID),
		Kind:            "background_bundle",
		Status:          taskNodeStatusForBundle(bundle.Status),
		LinkedBundleIDs: []string{bundle.ID},
		LinkedJobIDs:    append([]string(nil), bundle.JobIDs...),
		LifecycleNote:   bundle.LastSummary,
		LastUpdated:     time.Now(),
	}
	if len(node.LinkedJobIDs) == 0 && len(jobs) > 0 {
		ids := make([]string, 0, len(jobs))
		for _, job := range jobs {
			if strings.TrimSpace(job.ID) != "" {
				ids = append(ids, job.ID)
			}
		}
		node.LinkedJobIDs = normalizeTaskStateList(ids, 32)
	}
	if ownerID := g.resolveBundleOwnerNode(bundle, node.LinkedJobIDs); ownerID != "" {
		node.DependsOn = normalizeTaskStateList(append(node.DependsOn, ownerID), 8)
	}
	g.UpsertNode(node)
	return node.ID
}

func (g *TaskGraph) resolveBundleOwnerNode(bundle BackgroundShellBundle, linkedJobIDs []string) string {
	if g == nil {
		return ""
	}
	if ownerID := strings.TrimSpace(bundle.OwnerNodeID); ownerID != "" {
		if node, ok := g.Node(ownerID); ok && isPrimaryTaskNode(node) {
			g.attachBundleToPlanNode(ownerID, bundle, linkedJobIDs)
			return ownerID
		}
	}
	return g.attachBundleToBestPlanNode(bundle, linkedJobIDs)
}

func (g *TaskGraph) attachBundleToBestPlanNode(bundle BackgroundShellBundle, linkedJobIDs []string) string {
	if g == nil {
		return ""
	}
	targetIndex := -1
	bestScore := -1
	for index, node := range g.Nodes {
		if !isPrimaryTaskNode(node) {
			continue
		}
		score := scorePlanNodeForBundleOwnership(node, bundle)
		if score <= bestScore {
			continue
		}
		bestScore = score
		targetIndex = index
	}
	if targetIndex < 0 {
		return ""
	}
	return g.attachBundleToPlanNode(g.Nodes[targetIndex].ID, bundle, linkedJobIDs)
}

func (g *TaskGraph) attachBundleToPlanNode(nodeID string, bundle BackgroundShellBundle, linkedJobIDs []string) string {
	if g == nil {
		return ""
	}
	targetIndex := -1
	for index := range g.Nodes {
		if strings.EqualFold(g.Nodes[index].ID, strings.TrimSpace(nodeID)) {
			targetIndex = index
			break
		}
	}
	if targetIndex < 0 {
		return ""
	}
	node := g.Nodes[targetIndex]
	node.LinkedBundleIDs = normalizeTaskStateList(append(node.LinkedBundleIDs, bundle.ID), 16)
	node.LinkedJobIDs = normalizeTaskStateList(append(node.LinkedJobIDs, linkedJobIDs...), 32)
	if strings.TrimSpace(bundle.LastSummary) != "" {
		node.LifecycleNote = bundle.LastSummary
	}
	switch canonicalTaskNodeStatus(bundle.Status) {
	case "running":
		if node.Status == "pending" || node.Status == "ready" {
			node.Status = "in_progress"
		}
	case "failed":
		if node.Status != "completed" {
			node.Status = "blocked"
		}
	case "completed":
		if node.Status == "ready" {
			node.Status = "in_progress"
		}
	}
	node.LastUpdated = time.Now()
	g.Nodes[targetIndex] = node
	g.refreshReadyNodes()
	g.Touch()
	return node.ID
}

func scorePlanNodeForBundleOwnership(node TaskNode, bundle BackgroundShellBundle) int {
	status := canonicalTaskNodeStatus(node.Status)
	if status == "completed" || status == "failed" {
		return -1
	}
	score := 0
	switch status {
	case "in_progress":
		score += 100
	case "ready":
		score += 70
	case "pending":
		score += 25
	}
	switch strings.TrimSpace(strings.ToLower(node.Kind)) {
	case "verification":
		score += 40
	case "task":
		score += 20
	case "inspection", "edit", "summary":
		score += 10
	}
	commandText := strings.ToLower(strings.Join(bundle.CommandSummaries, " "))
	titleText := strings.ToLower(strings.TrimSpace(node.Title))
	if strings.Contains(commandText, "test") || strings.Contains(commandText, "build") || strings.Contains(commandText, "verify") {
		if strings.Contains(titleText, "test") || strings.Contains(titleText, "build") || strings.Contains(titleText, "verify") {
			score += 25
		}
	}
	return score
}

func taskNodeStatusForBundle(status string) string {
	switch canonicalTaskNodeStatus(status) {
	case "completed", "failed", "stale", "superseded", "canceled", "preempted":
		return canonicalTaskNodeStatus(status)
	default:
		return "in_progress"
	}
}

func firstNonBlankString(items ...string) string {
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *Session) EnsureTaskGraph() *TaskGraph {
	if s == nil {
		return nil
	}
	if s.TaskGraph == nil {
		if len(s.Plan) > 0 {
			s.TaskGraph = newTaskGraphFromPlan(s.Plan)
		} else {
			s.TaskGraph = &TaskGraph{}
		}
	}
	s.TaskGraph.Normalize()
	return s.TaskGraph
}

func (s *Session) syncTaskGraphFromPlan() {
	if s == nil {
		return
	}
	if len(s.Plan) == 0 {
		if s.TaskGraph == nil {
			return
		}
		preserved := append([]TaskNode(nil), s.TaskGraph.Nodes...)
		if len(preserved) == 0 {
			s.TaskGraph = nil
			return
		}
		s.TaskGraph = &TaskGraph{
			Nodes:       preserved,
			LastUpdated: time.Now(),
		}
		s.TaskGraph.Normalize()
		return
	}
	if s.TaskGraph == nil || len(s.TaskGraph.Nodes) == 0 {
		s.TaskGraph = newTaskGraphFromPlan(s.Plan)
		return
	}
	graph := newTaskGraphFromPlan(s.Plan)
	for _, node := range s.TaskGraph.Nodes {
		switch {
		case strings.HasPrefix(strings.TrimSpace(node.ID), "bundle:"):
			graph.UpsertNode(node)
		case strings.HasPrefix(strings.TrimSpace(node.ID), "plan-"):
			if existing, ok := graph.Node(node.ID); ok {
				graph.UpsertNode(mergeTaskNodeRuntimeState(existing, node))
			}
		}
	}
	s.TaskGraph = graph
}

func (s *Session) SetSharedPlan(items []PlanItem) {
	if s == nil {
		return
	}
	s.Plan = append([]PlanItem(nil), items...)
	s.syncTaskGraphFromPlan()
}

func (s *Session) ClearSharedPlan() {
	if s == nil {
		return
	}
	s.Plan = nil
	s.syncTaskGraphFromPlan()
}

func (s *Session) normalizeTaskGraph() {
	if s == nil || s.TaskGraph == nil {
		return
	}
	s.TaskGraph.Normalize()
	if len(s.Plan) > 0 {
		s.TaskGraph.SyncPlanStatuses(s.Plan)
	}
}

func (s *Session) normalizeTaskStateArtifacts() {
	if s == nil {
		return
	}
	s.normalizeTaskState()
	s.normalizeTaskGraph()
}

func (s *Session) AttachBackgroundBundle(bundle BackgroundShellBundle, jobs []BackgroundShellJob) string {
	if s == nil {
		return ""
	}
	graph := s.EnsureTaskGraph()
	if graph == nil {
		return ""
	}
	return graph.LinkBundle(bundle, jobs)
}

func (s *Session) MarkBundleLifecycle(bundleID string, status string, note string) {
	if s == nil || s.TaskGraph == nil {
		return
	}
	s.TaskGraph.SetNodeLifecycle("bundle:"+strings.TrimSpace(bundleID), status, note)
}

func (s *Session) ReadyTaskGraphNodes(limit int) []TaskNode {
	if s == nil || s.TaskGraph == nil {
		return nil
	}
	return s.TaskGraph.ReadyNodes(limit)
}

func (s *Session) ActiveTaskGraphNodes(limit int) []TaskNode {
	if s == nil || s.TaskGraph == nil {
		return nil
	}
	return s.TaskGraph.ActiveNodes(limit)
}

func (s *Session) TaskGraphMicroWorkerCandidates(limit int) []TaskNode {
	if s == nil || s.TaskGraph == nil {
		return nil
	}
	nodes := append([]TaskNode(nil), s.TaskGraph.Nodes...)
	slices.SortStableFunc(nodes, func(a, b TaskNode) int {
		scoreA := scoreTaskNodeForMicroWorker(a)
		scoreB := scoreTaskNodeForMicroWorker(b)
		if scoreA == scoreB {
			return strings.Compare(a.ID, b.ID)
		}
		if scoreA > scoreB {
			return -1
		}
		return 1
	})
	filtered := make([]TaskNode, 0, len(nodes))
	for _, node := range nodes {
		if !isPrimaryTaskNode(node) {
			continue
		}
		status := canonicalTaskNodeStatus(node.Status)
		if node.MicroWorkerBrief != "" || status == "completed" || status == "failed" || status == "stale" || status == "superseded" {
			continue
		}
		filtered = append(filtered, node)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func scoreTaskNodeForMicroWorker(node TaskNode) int {
	status := canonicalTaskNodeStatus(node.Status)
	kind := strings.TrimSpace(strings.ToLower(node.Kind))
	score := 0
	switch status {
	case "blocked":
		score += 120
	case "in_progress":
		score += 90
	case "ready":
		score += 70
	case "pending":
		score += 30
	default:
		score -= 100
	}
	switch kind {
	case "verification":
		score += 40
	case "background_bundle":
		score += 25
	case "edit":
		score += 20
	case "inspection":
		score += 15
	case "summary":
		score += 5
	}
	if len(node.LinkedBundleIDs) > 0 {
		score += 10
	}
	if strings.TrimSpace(node.LifecycleNote) != "" {
		score += 5
	}
	if strings.TrimSpace(node.ReadOnlyWorkerSummary) != "" {
		score += 6
	}
	return score
}

func (s *Session) RecordTaskGraphMicroWorkerBrief(nodeID string, brief string) {
	if s == nil || s.TaskGraph == nil {
		return
	}
	s.TaskGraph.RecordMicroWorkerBrief(nodeID, brief)
}

func (s *Session) RecordTaskGraphReadOnlyWorkerEvidence(nodeID string, toolName string, summary string) {
	if s == nil || s.TaskGraph == nil {
		return
	}
	s.TaskGraph.RecordReadOnlyWorkerEvidence(nodeID, toolName, summary)
}

func (s *Session) RecordPlanNodeFailure(nodeID string, toolName string, detail string) bool {
	if s == nil || s.TaskGraph == nil {
		return false
	}
	before, ok := s.TaskGraph.Node(nodeID)
	if !ok {
		return false
	}
	if !s.TaskGraph.RecordNodeFailure(nodeID, toolName, detail) {
		return false
	}
	after, ok := s.TaskGraph.Node(nodeID)
	if !ok {
		return true
	}
	if canonicalTaskNodeStatus(before.Status) != "blocked" && canonicalTaskNodeStatus(after.Status) == "blocked" {
		if index, ok := planNodeIndex(nodeID); ok && index >= 0 && index < len(s.Plan) {
			current := strings.TrimSpace(strings.ToLower(s.Plan[index].Status))
			if current != "completed" && current != "pending" {
				s.Plan[index].Status = "pending"
			}
		}
		s.syncTaskStatePlanCursor()
	}
	return true
}

func bundleNodeIDs(graph *TaskGraph) []string {
	if graph == nil {
		return nil
	}
	out := make([]string, 0)
	for _, node := range graph.Nodes {
		if strings.HasPrefix(node.ID, "bundle:") {
			out = append(out, node.ID)
		}
	}
	slices.Sort(out)
	return out
}
