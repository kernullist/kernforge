package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type SessionDashboardSnapshot struct {
	GeneratedAt         time.Time
	SessionID           string
	SessionName         string
	Workspace           string
	BaseRoot            string
	WorkspaceRoots      []string
	Branch              string
	Provider            string
	Model               string
	PermissionMode      string
	ApproxChars         int
	MessageCount        int
	SummaryChars        int
	PlanItems           int
	TaskCounts          []NamedCount
	OpenTasks           []TaskNode
	ChangedFiles        []string
	AutomationSummary   automationRuntimeSummary
	Automations         []SessionAutomation
	RecentEvents        []ConversationEvent
	ArtifactRefs        []string
	LastVerification    string
	VerificationFailure string
	RuntimeGateLedger   RuntimeGateLedger
	BackgroundJobs      []BackgroundShellJob
	BackgroundBundles   []BackgroundShellBundle
}

func (rt *runtimeState) handleSessionDashboardHTMLCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = sessionBaseWorkingDir(rt.session)
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	snapshot := rt.buildSessionDashboardSnapshot(root, time.Now())
	outDir := filepath.Join(root, ".kernforge", "session_dashboard")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	outputPath := filepath.Join(outDir, "latest.html")
	if err := os.WriteFile(outputPath, []byte(renderSessionDashboardHTML(snapshot)), 0o644); err != nil {
		return err
	}
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindDashboard,
		Severity: conversationSeverityInfo,
		Summary:  "session dashboard generated",
		ArtifactRefs: []string{
			outputPath,
		},
		Entities: map[string]string{
			"dashboard": "session",
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	if rt.interactive {
		if err := OpenExternalURL(outputPath); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Generated HTML session dashboard but could not open it automatically: "+err.Error()))
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated session dashboard: "+outputPath))
	return nil
}

func (rt *runtimeState) buildSessionDashboardSnapshot(root string, now time.Time) SessionDashboardSnapshot {
	sess := rt.session
	snapshot := SessionDashboardSnapshot{
		GeneratedAt:    now,
		SessionID:      sess.ID,
		SessionName:    sess.Name,
		Workspace:      sess.WorkingDir,
		BaseRoot:       root,
		WorkspaceRoots: workspaceEffectiveRoots(rt.workspace, sess),
		Branch:         delegationGitBranch(root),
		Provider:       sess.Provider,
		Model:          sess.Model,
		PermissionMode: sess.PermissionMode,
		ApproxChars:    sess.ApproxChars(),
		MessageCount:   len(sess.Messages),
		SummaryChars:   len(sess.Summary),
		PlanItems:      len(sess.Plan),
		ChangedFiles:   reviewCurrentChangedPaths(root),
	}
	ledger := buildRuntimeGateLedger(root, sess, runtimeGateActionFinalAnswer)
	snapshot.RuntimeGateLedger = ledger
	sess.RuntimeGateLedger = &ledger
	if sess.TaskGraph != nil {
		sess.TaskGraph.Normalize()
		snapshot.TaskCounts = sessionDashboardTaskCounts(sess.TaskGraph)
		snapshot.OpenTasks = sessionDashboardOpenTasks(sess.TaskGraph, 20)
	}
	sess.normalizeAutomations()
	snapshot.AutomationSummary = summarizeAutomations(sess.Automations, now)
	snapshot.Automations = append([]SessionAutomation(nil), sess.Automations...)
	snapshot.RecentEvents = sessionDashboardRecentEvents(sess.ConversationEvents, 16)
	snapshot.ArtifactRefs = delegationArtifactRefs(sess.ConversationEvents, 16)
	if sess.LastVerification != nil {
		snapshot.LastVerification = sess.LastVerification.SummaryLine()
		if sess.LastVerification.HasFailures() {
			snapshot.VerificationFailure = sess.LastVerification.FailureSummary()
		}
	}
	snapshot.BackgroundJobs = sessionDashboardRecentBackgroundJobs(sess.BackgroundJobs, 12)
	snapshot.BackgroundBundles = sessionDashboardRecentBackgroundBundles(sess.BackgroundBundles, 12)
	return snapshot
}

func sessionDashboardTaskCounts(graph *TaskGraph) []NamedCount {
	if graph == nil {
		return nil
	}
	counts := map[string]int{}
	for _, node := range graph.Nodes {
		counts[canonicalTaskNodeStatus(node.Status)]++
	}
	return sortNamedCounts(counts)
}

func sessionDashboardOpenTasks(graph *TaskGraph, limit int) []TaskNode {
	if graph == nil {
		return nil
	}
	out := make([]TaskNode, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if sessionDashboardTerminalTaskStatus(node.Status) {
			continue
		}
		node.Normalize()
		out = append(out, node)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := sessionDashboardTaskStatusRank(out[i].Status)
		right := sessionDashboardTaskStatusRank(out[j].Status)
		if left != right {
			return left < right
		}
		return strings.Compare(strings.ToLower(out[i].ID), strings.ToLower(out[j].ID)) < 0
	})
	if limit > 0 && len(out) > limit {
		return append([]TaskNode(nil), out[:limit]...)
	}
	return append([]TaskNode(nil), out...)
}

func sessionDashboardTerminalTaskStatus(status string) bool {
	switch canonicalTaskNodeStatus(status) {
	case "completed", "canceled", "superseded", "preempted":
		return true
	default:
		return false
	}
}

func sessionDashboardTaskStatusRank(status string) int {
	switch canonicalTaskNodeStatus(status) {
	case "blocked", "failed":
		return 0
	case "in_progress":
		return 1
	case "ready":
		return 2
	case "pending":
		return 3
	default:
		return 4
	}
}

func sessionDashboardRecentEvents(events []ConversationEvent, limit int) []ConversationEvent {
	if limit <= 0 || len(events) == 0 {
		return nil
	}
	start := len(events) - limit
	if start < 0 {
		start = 0
	}
	out := append([]ConversationEvent(nil), events[start:]...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Time.After(out[j].Time)
	})
	return out
}

func sessionDashboardRecentBackgroundJobs(jobs []BackgroundShellJob, limit int) []BackgroundShellJob {
	out := append([]BackgroundShellJob(nil), jobs...)
	for index := range out {
		out[index].Normalize()
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if limit > 0 && len(out) > limit {
		return append([]BackgroundShellJob(nil), out[:limit]...)
	}
	return out
}

func sessionDashboardRecentBackgroundBundles(bundles []BackgroundShellBundle, limit int) []BackgroundShellBundle {
	out := append([]BackgroundShellBundle(nil), bundles...)
	for index := range out {
		out[index].Normalize()
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if limit > 0 && len(out) > limit {
		return append([]BackgroundShellBundle(nil), out[:limit]...)
	}
	return out
}

func renderSessionDashboardHTML(snapshot SessionDashboardSnapshot) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Kernforge Session Dashboard</title>
<style>
body { margin: 0; font-family: Segoe UI, Arial, sans-serif; background: #191a1f; color: #eceff4; }
main { max-width: 1220px; margin: 0 auto; padding: 30px 20px 44px; }
h1 { margin: 0 0 6px; font-size: 28px; font-weight: 700; }
h2 { margin: 0 0 10px; font-size: 16px; color: #d8dee9; text-transform: uppercase; letter-spacing: 0; }
h3 { margin: 0; font-size: 15px; }
section { margin-top: 22px; }
table { width: 100%%; border-collapse: collapse; }
th, td { text-align: left; vertical-align: top; border-bottom: 1px solid #3a3d46; padding: 9px 8px; font-size: 13px; }
th { color: #aeb6c2; font-weight: 600; }
code { display: inline-block; padding: 5px 7px; border-radius: 6px; background: #111217; color: #b9d9ff; }
pre { white-space: pre-wrap; background: #111217; border: 1px solid #3a3d46; border-radius: 8px; padding: 12px; overflow: auto; }
.subtle, .meta { color: #aeb6c2; font-size: 13px; }
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(230px, 1fr)); gap: 12px; margin-top: 16px; }
.wide-grid { display: grid; grid-template-columns: minmax(0, 1.25fr) minmax(320px, 0.75fr); gap: 14px; }
.card { border: 1px solid #3a3d46; border-radius: 8px; padding: 15px; background: #252831; }
.metric { font-size: 25px; font-weight: 700; margin-top: 6px; }
.row { display: flex; justify-content: space-between; gap: 10px; align-items: baseline; }
.chips { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
.chip { border: 1px solid #4b5360; border-radius: 999px; color: #d7e7ff; padding: 4px 9px; font-size: 12px; background: #191a1f; }
.badge { border-radius: 999px; padding: 3px 8px; font-size: 12px; background: #3a3d46; color: #eceff4; }
.status-blocked, .status-failed, .severity-error { color: #ffb4a8; }
.status-in-progress, .severity-warn { color: #ffd27d; }
.status-ready, .status-active, .status-fresh { color: #a7f3d0; }
.status-needs-review, .status-stale, .status-missing { color: #ffd27d; }
.empty { color: #aeb6c2; border: 1px dashed #4b5360; border-radius: 8px; padding: 14px; }
.mono-list { margin: 0; padding-left: 18px; }
@media (max-width: 820px) { .wide-grid { grid-template-columns: 1fr; } main { padding: 24px 14px 34px; } }
</style>
</head>
<body>
<main>
<h1>Kernforge Session Dashboard</h1>
<div class="subtle">session=%s name=%s generated=%s branch=%s provider=%s model=%s</div>
<section>
<div class="grid">%s</div>
<div class="chips">%s</div>
</section>
<section class="wide-grid">
<div>
<h2>Open Task Graph</h2>
%s
</div>
<div>
<h2>Automation Runtime</h2>
%s
</div>
</section>
<section class="wide-grid">
<div>
<h2>Recent Thread Events</h2>
%s
</div>
<div>
<h2>Workspace Signals</h2>
%s
</div>
</section>
<section class="wide-grid">
<div>
<h2>Background Work</h2>
%s
</div>
<div>
<h2>Artifact Refs</h2>
%s
</div>
</section>
</main>
</body>
</html>
`,
		htmlEscape(valueOrDefault(snapshot.SessionID, "unset")),
		htmlEscape(valueOrDefault(snapshot.SessionName, "unnamed")),
		htmlEscape(snapshot.GeneratedAt.Format(time.RFC3339)),
		htmlEscape(valueOrDefault(snapshot.Branch, "unknown")),
		htmlEscape(valueOrUnset(snapshot.Provider)),
		htmlEscape(valueOrUnset(snapshot.Model)),
		renderSessionDashboardMetricCards(snapshot),
		renderSessionDashboardCommandChips(),
		renderSessionDashboardTaskTable(snapshot.OpenTasks, snapshot.TaskCounts),
		renderSessionDashboardAutomationTable(snapshot.Automations, snapshot.AutomationSummary, snapshot.GeneratedAt),
		renderSessionDashboardEvents(snapshot.RecentEvents),
		renderSessionDashboardWorkspaceSignals(snapshot),
		renderSessionDashboardBackground(snapshot.BackgroundJobs, snapshot.BackgroundBundles),
		renderSessionDashboardArtifactRefs(snapshot.ArtifactRefs),
	)
}

func renderSessionDashboardMetricCards(snapshot SessionDashboardSnapshot) string {
	cards := []string{
		sessionDashboardMetricCard("Messages", fmt.Sprintf("%d", snapshot.MessageCount), fmt.Sprintf("context chars=%d summary chars=%d", snapshot.ApproxChars, snapshot.SummaryChars)),
		sessionDashboardMetricCard("Plan Items", fmt.Sprintf("%d", snapshot.PlanItems), "open task graph nodes="+fmt.Sprintf("%d", len(snapshot.OpenTasks))),
		sessionDashboardMetricCard("Changed Files", fmt.Sprintf("%d", len(snapshot.ChangedFiles)), strings.Join(limitStrings(snapshot.ChangedFiles, 3), ", ")),
		sessionDashboardMetricCard("Automations", fmt.Sprintf("%d", snapshot.AutomationSummary.Total), automationSummaryLine(snapshot.AutomationSummary)),
		sessionDashboardMetricCard("Runtime Gate", valueOrDefault(snapshot.RuntimeGateLedger.Status, "unknown"), runtimeGateStatusSummary(snapshot.RuntimeGateLedger)),
		sessionDashboardMetricCard("Verification", valueOrDefault(snapshot.LastVerification, "not recorded"), firstNonBlankString(snapshot.VerificationFailure, "latest verification has no recorded failure")),
	}
	return strings.Join(cards, "\n")
}

func sessionDashboardMetricCard(title string, value string, detail string) string {
	return fmt.Sprintf(`<article class="card"><div class="subtle">%s</div><div class="metric">%s</div><div class="subtle">%s</div></article>`,
		htmlEscape(title),
		htmlEscape(valueOrDefault(value, "unset")),
		htmlEscape(valueOrDefault(detail, "none")),
	)
}

func renderSessionDashboardCommandChips() string {
	commands := []string{
		"/status",
		"/review",
		"/tasks",
		"/automation digest",
		"/automation run-due",
		"/verify-dashboard-html",
		"/suggest-dashboard-html",
		"/handoff",
	}
	chips := make([]string, 0, len(commands))
	for _, command := range commands {
		chips = append(chips, `<span class="chip">`+htmlEscape(command)+`</span>`)
	}
	return strings.Join(chips, "")
}

func renderSessionDashboardTaskTable(tasks []TaskNode, counts []NamedCount) string {
	var b strings.Builder
	if len(counts) > 0 {
		b.WriteString(`<div class="chips">`)
		for _, item := range counts {
			b.WriteString(fmt.Sprintf(`<span class="chip">%s=%d</span>`, htmlEscape(item.Name), item.Count))
		}
		b.WriteString(`</div>`)
	}
	if len(tasks) == 0 {
		b.WriteString(`<div class="empty">No open task graph nodes.</div>`)
		return b.String()
	}
	b.WriteString(`<table><thead><tr><th>ID</th><th>Status</th><th>Kind</th><th>Title</th><th>Owner / evidence</th></tr></thead><tbody>`)
	for _, task := range tasks {
		owner := sessionDashboardTaskOwnerSummary(task)
		b.WriteString(fmt.Sprintf(`<tr><td><code>%s</code></td><td class="%s">%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			htmlEscape(task.ID),
			htmlEscape("status-"+sessionDashboardCSSClass(task.Status)),
			htmlEscape(valueOrDefault(task.Status, "pending")),
			htmlEscape(valueOrDefault(task.Kind, "task")),
			htmlEscape(task.Title),
			htmlEscape(valueOrDefault(owner, "unassigned")),
		))
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func sessionDashboardTaskOwnerSummary(task TaskNode) string {
	parts := []string{}
	if task.AssignedSpecialist != "" {
		parts = append(parts, "specialist="+task.AssignedSpecialist)
	}
	if task.EditableSpecialist != "" {
		parts = append(parts, "editable="+task.EditableSpecialist)
	}
	if len(task.EditableLeasePaths) > 0 {
		parts = append(parts, "lease="+strings.Join(limitStrings(task.EditableLeasePaths, 3), ","))
	}
	if task.EditableWorkerSummary != "" {
		parts = append(parts, "edit_worker="+compactPromptSection(task.EditableWorkerSummary, 100))
	}
	if task.ReadOnlyWorkerSummary != "" {
		parts = append(parts, "worker="+compactPromptSection(firstNonBlankString(task.ReadOnlyWorkerTool, "read_only")+": "+task.ReadOnlyWorkerSummary, 100))
	}
	if task.RetryBudget > 0 && task.RetryUsed > 0 {
		parts = append(parts, fmt.Sprintf("retries=%d/%d", task.RetryUsed, task.RetryBudget))
	}
	if task.LastFailure != "" {
		parts = append(parts, "failure="+compactPromptSection(task.LastFailure, 100))
	}
	if task.LifecycleNote != "" {
		parts = append(parts, "note="+compactPromptSection(task.LifecycleNote, 100))
	}
	return strings.Join(parts, " | ")
}

func renderSessionDashboardAutomationTable(items []SessionAutomation, summary automationRuntimeSummary, now time.Time) string {
	var b strings.Builder
	b.WriteString(`<div class="chips">`)
	for _, chip := range []string{
		fmt.Sprintf("total=%d", summary.Total),
		fmt.Sprintf("scheduled=%d", summary.Scheduled),
		fmt.Sprintf("due=%d", summary.Due),
		fmt.Sprintf("failed=%d", summary.Failed),
		fmt.Sprintf("paused=%d", summary.Paused),
	} {
		b.WriteString(`<span class="chip">` + htmlEscape(chip) + `</span>`)
	}
	b.WriteString(`</div>`)
	if len(items) == 0 {
		b.WriteString(`<div class="empty">No automations configured.</div>`)
		return b.String()
	}
	b.WriteString(`<table><thead><tr><th>ID</th><th>Status</th><th>Schedule</th><th>Command</th><th>Result</th></tr></thead><tbody>`)
	for _, item := range items {
		status := item.Status
		if automationIsDue(item, now) {
			status = status + " due"
		}
		result := item.LastResult
		if item.NextRunHint != "" {
			result = strings.TrimSpace(strings.Join([]string{item.NextRunHint, result}, " | "))
		}
		b.WriteString(fmt.Sprintf(`<tr><td><code>%s</code></td><td class="%s">%s</td><td>%s</td><td><code>%s</code></td><td>%s</td></tr>`,
			htmlEscape(item.ID),
			htmlEscape("status-"+sessionDashboardCSSClass(item.Status)),
			htmlEscape(status),
			htmlEscape(valueOrDefault(item.Schedule, "manual-recurring")),
			htmlEscape(item.Command),
			htmlEscape(valueOrDefault(compactPromptSection(result, 160), "none")),
		))
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func renderSessionDashboardEvents(events []ConversationEvent) string {
	if len(events) == 0 {
		return `<div class="empty">No recent conversation events.</div>`
	}
	var b strings.Builder
	b.WriteString(`<table><thead><tr><th>Time</th><th>Severity</th><th>Kind</th><th>Summary</th><th>Entities</th></tr></thead><tbody>`)
	for _, event := range events {
		b.WriteString(fmt.Sprintf(`<tr><td>%s</td><td class="%s">%s</td><td>%s</td><td>%s%s</td><td>%s</td></tr>`,
			htmlEscape(sessionDashboardFormatTime(event.Time)),
			htmlEscape("severity-"+sessionDashboardCSSClass(event.Severity)),
			htmlEscape(valueOrDefault(event.Severity, "info")),
			htmlEscape(event.Kind),
			htmlEscape(compactPromptSection(event.Summary, 180)),
			renderSessionDashboardEventRefs(event.ArtifactRefs),
			htmlEscape(sessionDashboardEntitiesSummary(event.Entities)),
		))
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func renderSessionDashboardEventRefs(refs []string) string {
	refs = limitStrings(refs, 3)
	if len(refs) == 0 {
		return ""
	}
	chips := make([]string, 0, len(refs))
	for _, ref := range refs {
		chips = append(chips, `<span class="chip">`+htmlEscape(ref)+`</span>`)
	}
	return `<div class="chips">` + strings.Join(chips, "") + `</div>`
}

func sessionDashboardEntitiesSummary(entities map[string]string) string {
	if len(entities) == 0 {
		return ""
	}
	keys := make([]string, 0, len(entities))
	for key := range entities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+entities[key])
	}
	return compactPromptSection(strings.Join(parts, ", "), 160)
}

func renderSessionDashboardWorkspaceSignals(snapshot SessionDashboardSnapshot) string {
	var b strings.Builder
	b.WriteString(`<article class="card">`)
	b.WriteString(`<div class="row"><h3>Workspace</h3><span class="badge">` + htmlEscape(valueOrDefault(snapshot.Branch, "unknown")) + `</span></div>`)
	b.WriteString(`<p class="subtle">` + htmlEscape(valueOrDefault(snapshot.Workspace, snapshot.BaseRoot)) + `</p>`)
	if len(snapshot.WorkspaceRoots) > 0 {
		escapedRoots := make([]string, 0, len(snapshot.WorkspaceRoots))
		for _, root := range snapshot.WorkspaceRoots {
			escapedRoots = append(escapedRoots, htmlEscape(root))
		}
		b.WriteString(`<p><strong>Workspace roots</strong><br>` + strings.Join(escapedRoots, `<br>`) + `</p>`)
	}
	if snapshot.LastVerification != "" {
		b.WriteString(`<p><strong>Verification</strong><br>` + htmlEscape(snapshot.LastVerification) + `</p>`)
	}
	if snapshot.VerificationFailure != "" {
		b.WriteString(`<pre>` + htmlEscape(snapshot.VerificationFailure) + `</pre>`)
	}
	b.WriteString(`</article>`)
	b.WriteString(renderSessionDashboardRuntimeGate(snapshot.RuntimeGateLedger))
	b.WriteString(`<section><h2>Changed Files</h2>`)
	if len(snapshot.ChangedFiles) == 0 {
		b.WriteString(`<div class="empty">No changed files detected.</div>`)
	} else {
		b.WriteString(`<ul class="mono-list">`)
		for _, path := range limitStrings(snapshot.ChangedFiles, 16) {
			b.WriteString(`<li><code>` + htmlEscape(path) + `</code></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}

func renderSessionDashboardRuntimeGate(ledger RuntimeGateLedger) string {
	ledger.Normalize()
	if strings.TrimSpace(ledger.ID) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<article class="card">`)
	b.WriteString(`<div class="row"><h3>Runtime Gate</h3><span class="badge status-` + htmlEscape(sessionDashboardCSSClass(ledger.Status)) + `">` + htmlEscape(valueOrDefault(ledger.Status, "unknown")) + `</span></div>`)
	b.WriteString(`<p class="subtle">` + htmlEscape(runtimeGateStatusSummary(ledger)) + `</p>`)
	b.WriteString(`<div class="chips">`)
	b.WriteString(`<span class="chip">review_freshness=` + htmlEscape(runtimeGateReviewFreshnessLabel(ledger)) + `</span>`)
	b.WriteString(`<span class="chip">blockers=` + fmt.Sprintf("%d", len(ledger.Blockers)) + `</span>`)
	b.WriteString(`<span class="chip">warnings=` + fmt.Sprintf("%d", len(ledger.Warnings)) + `</span>`)
	if len(ledger.Waivers) > 0 {
		b.WriteString(`<span class="chip">waivers=` + fmt.Sprintf("%d", len(ledger.Waivers)) + `</span>`)
	}
	b.WriteString(`</div>`)
	if ledger.ReviewRunID != "" {
		b.WriteString(`<p><strong>Latest review</strong><br><code>` + htmlEscape(ledger.ReviewRunID) + `</code></p>`)
	}
	if len(ledger.StaleReasons) > 0 {
		b.WriteString(`<pre>` + htmlEscape(strings.Join(limitStrings(ledger.StaleReasons, 3), "\n")) + `</pre>`)
	}
	if len(ledger.Blockers) > 0 {
		b.WriteString(`<pre>` + htmlEscape(strings.Join(limitStrings(ledger.Blockers, 3), "\n")) + `</pre>`)
	}
	if next := runtimeGatePrimaryNextCommandLine(ledger); next != "" {
		b.WriteString(`<p><strong>Next command</strong><br><code>` + htmlEscape(next) + `</code></p>`)
	}
	b.WriteString(`</article>`)
	return b.String()
}

func renderSessionDashboardBackground(jobs []BackgroundShellJob, bundles []BackgroundShellBundle) string {
	var b strings.Builder
	if len(bundles) == 0 && len(jobs) == 0 {
		return `<div class="empty">No background jobs or bundles recorded.</div>`
	}
	if len(bundles) > 0 {
		b.WriteString(`<h3>Bundles</h3><table><thead><tr><th>ID</th><th>Status</th><th>Owner</th><th>Summary</th></tr></thead><tbody>`)
		for _, bundle := range bundles {
			summary := firstNonBlankString(bundle.LastSummary, strings.Join(limitStrings(bundle.CommandSummaries, 3), " | "))
			b.WriteString(fmt.Sprintf(`<tr><td><code>%s</code></td><td class="%s">%s</td><td>%s</td><td>%s</td></tr>`,
				htmlEscape(bundle.ID),
				htmlEscape("status-"+sessionDashboardCSSClass(bundle.Status)),
				htmlEscape(valueOrDefault(bundle.Status, "unknown")),
				htmlEscape(valueOrDefault(bundle.OwnerNodeID, "none")),
				htmlEscape(valueOrDefault(compactPromptSection(summary, 160), "none")),
			))
		}
		b.WriteString(`</tbody></table>`)
	}
	if len(jobs) > 0 {
		b.WriteString(`<h3>Jobs</h3><table><thead><tr><th>ID</th><th>Status</th><th>Owner</th><th>Command</th></tr></thead><tbody>`)
		for _, job := range jobs {
			b.WriteString(fmt.Sprintf(`<tr><td><code>%s</code></td><td class="%s">%s</td><td>%s</td><td>%s</td></tr>`,
				htmlEscape(job.ID),
				htmlEscape("status-"+sessionDashboardCSSClass(job.Status)),
				htmlEscape(valueOrDefault(job.Status, "unknown")),
				htmlEscape(valueOrDefault(job.OwnerNodeID, "none")),
				htmlEscape(compactPromptSection(firstNonBlankString(job.CommandSummary, job.Command), 160)),
			))
		}
		b.WriteString(`</tbody></table>`)
	}
	return b.String()
}

func renderSessionDashboardArtifactRefs(refs []string) string {
	if len(refs) == 0 {
		return `<div class="empty">No artifact refs recorded.</div>`
	}
	var b strings.Builder
	b.WriteString(`<ul class="mono-list">`)
	for _, ref := range limitStrings(refs, 16) {
		b.WriteString(`<li><code>` + htmlEscape(ref) + `</code></li>`)
	}
	b.WriteString(`</ul>`)
	return b.String()
}

func sessionDashboardFormatTime(value time.Time) string {
	if value.IsZero() {
		return "unset"
	}
	return value.Format("2006-01-02 15:04:05")
}

func sessionDashboardCSSClass(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unset"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '_' || r == '-' || unicode.IsSpace(r):
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "unset"
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unset"
	}
	return out
}
