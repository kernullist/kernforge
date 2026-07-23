package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// GoalSlice is one independently reviewable unit of work in a goal DAG.
// Runner v2 (PR3) executes ready slices; PR2 only models, parses, and persists.
type GoalSlice struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Outcome      string   `json:"outcome,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	LikelyFiles  []string `json:"likely_files,omitempty"`
	Acceptance   []string `json:"acceptance,omitempty"`
	Validation   string   `json:"validation,omitempty"`
	Docs         string   `json:"docs,omitempty"`
	Risk         string   `json:"risk,omitempty"` // low|medium|high
	DependsOn    []string `json:"depends_on,omitempty"`
	Status       string   `json:"status,omitempty"`
	CriterionIDs []string `json:"criterion_ids,omitempty"`
	ArtifactRefs []string `json:"artifact_refs,omitempty"`
	Order        int      `json:"order,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// GoalSlicePlan is the durable slice DAG attached to a GoalState.
type GoalSlicePlan struct {
	ObjectiveRestated string      `json:"objective_restated,omitempty"`
	NonGoals          []string    `json:"non_goals,omitempty"`
	RecommendedFirst  string      `json:"recommended_first,omitempty"`
	OpenQuestions     []string    `json:"open_questions,omitempty"`
	Slices            []GoalSlice `json:"slices,omitempty"`
	CompiledFrom      string      `json:"compiled_from,omitempty"`
}

const (
	goalSliceStatusPending  = "pending"
	goalSliceStatusReady    = "ready"
	goalSliceStatusRunning  = "running"
	goalSliceStatusComplete = "complete"
	goalSliceStatusBlocked  = "blocked"
	goalSliceStatusSkipped  = "skipped"

	goalSliceRiskLow    = "low"
	goalSliceRiskMedium = "medium"
	goalSliceRiskHigh   = "high"
)

var (
	goalSliceNumberedHeading = regexp.MustCompile(`(?i)^\s*(\d+)[.)]\s+(.+?)\s*$`)
	goalSliceFieldLine       = regexp.MustCompile(`(?i)^\s*(Outcome|Scope|Likely files|Acceptance|Validation|Docs|Risk|Depends on)\s*:\s*(.*)$`)
	goalSliceSectionHeading  = regexp.MustCompile(`(?i)^\s*(Objective|Non-goals|Current facts|Slices|Recommended first slice|Open questions|KernForge execution plan|Draft status)\s*$`)
)

func (s *GoalSlice) Normalize() {
	if s == nil {
		return
	}
	s.ID = strings.TrimSpace(s.ID)
	s.Name = strings.TrimSpace(s.Name)
	s.Outcome = strings.TrimSpace(s.Outcome)
	s.Scope = strings.TrimSpace(s.Scope)
	s.Validation = strings.TrimSpace(s.Validation)
	s.Docs = strings.TrimSpace(s.Docs)
	s.Error = strings.TrimSpace(s.Error)
	s.Risk = canonicalGoalSliceRisk(s.Risk)
	s.Status = canonicalGoalSliceStatus(s.Status)
	s.LikelyFiles = uniqueStrings(normalizeTaskStateList(s.LikelyFiles, 24))
	s.Acceptance = normalizeTaskStateList(s.Acceptance, 12)
	s.DependsOn = uniqueStrings(normalizeTaskStateList(s.DependsOn, 16))
	s.CriterionIDs = uniqueStrings(normalizeTaskStateList(s.CriterionIDs, 16))
	s.ArtifactRefs = uniqueStrings(s.ArtifactRefs)
	if s.Order < 0 {
		s.Order = 0
	}
	if s.ID == "" && s.Name != "" {
		s.ID = goalSliceIDFromName(s.Name, s.Order)
	}
	if s.Name == "" && s.ID != "" {
		s.Name = s.ID
	}
	if s.Status == "" {
		s.Status = goalSliceStatusPending
	}
}

func (p *GoalSlicePlan) Normalize() {
	if p == nil {
		return
	}
	p.ObjectiveRestated = strings.TrimSpace(p.ObjectiveRestated)
	p.RecommendedFirst = strings.TrimSpace(p.RecommendedFirst)
	p.CompiledFrom = strings.TrimSpace(p.CompiledFrom)
	p.NonGoals = normalizeTaskStateList(p.NonGoals, 12)
	p.OpenQuestions = normalizeTaskStateList(p.OpenQuestions, 12)
	out := make([]GoalSlice, 0, len(p.Slices))
	seenID := map[string]bool{}
	for i := range p.Slices {
		slice := p.Slices[i]
		if slice.Order == 0 {
			slice.Order = i + 1
		}
		slice.Normalize()
		if strings.TrimSpace(slice.Name) == "" && strings.TrimSpace(slice.ID) == "" {
			continue
		}
		if slice.ID == "" {
			slice.ID = goalSliceIDFromName(slice.Name, slice.Order)
		}
		// Deduplicate IDs deterministically.
		base := slice.ID
		suffix := 2
		for seenID[strings.ToLower(slice.ID)] {
			slice.ID = fmt.Sprintf("%s-%d", base, suffix)
			suffix++
		}
		seenID[strings.ToLower(slice.ID)] = true
		out = append(out, slice)
		if len(out) >= 16 {
			break
		}
	}
	// Default sequential edges when DependsOn is empty: slice N depends on N-1.
	applyDefaultSequentialDependencies(out)
	// Drop depends_on edges that point at unknown IDs.
	known := map[string]bool{}
	for _, s := range out {
		known[strings.ToLower(s.ID)] = true
	}
	for i := range out {
		deps := make([]string, 0, len(out[i].DependsOn))
		for _, dep := range out[i].DependsOn {
			if known[strings.ToLower(dep)] && !strings.EqualFold(dep, out[i].ID) {
				deps = append(deps, dep)
			}
		}
		out[i].DependsOn = uniqueStrings(deps)
	}
	p.Slices = out
	if cycle := goalSlicePlanCycle(p.Slices); cycle != "" {
		// Break cycles by clearing all depends_on and restoring sequential defaults.
		for i := range p.Slices {
			p.Slices[i].DependsOn = nil
		}
		applyDefaultSequentialDependencies(p.Slices)
		p.OpenQuestions = append(p.OpenQuestions, "Slice dependency cycle detected and linearized: "+cycle)
		p.OpenQuestions = normalizeTaskStateList(p.OpenQuestions, 12)
	}
}

func applyDefaultSequentialDependencies(slices []GoalSlice) {
	// Only auto-wire when no slice already declares depends_on.
	for _, s := range slices {
		if len(s.DependsOn) > 0 {
			return
		}
	}
	for i := 1; i < len(slices); i++ {
		slices[i].DependsOn = []string{slices[i-1].ID}
	}
}

func canonicalGoalSliceStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case goalSliceStatusPending, goalSliceStatusReady, goalSliceStatusRunning,
		goalSliceStatusComplete, goalSliceStatusBlocked, goalSliceStatusSkipped:
		return strings.ToLower(strings.TrimSpace(value))
	case "done", "completed", "approved":
		return goalSliceStatusComplete
	case "in_progress", "active":
		return goalSliceStatusRunning
	case "todo", "open", "":
		return goalSliceStatusPending
	default:
		return goalSliceStatusPending
	}
}

func canonicalGoalSliceRisk(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case goalSliceRiskLow, goalSliceRiskMedium, goalSliceRiskHigh:
		return strings.ToLower(strings.TrimSpace(value))
	case "critical":
		return goalSliceRiskHigh
	default:
		return goalSliceRiskMedium
	}
}

func goalSliceIDFromName(name string, order int) string {
	name = strings.TrimSpace(name)
	if name == "" {
		if order > 0 {
			return fmt.Sprintf("slice-%02d", order)
		}
		return "slice"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		if order > 0 {
			return fmt.Sprintf("slice-%02d", order)
		}
		return "slice"
	}
	if len(id) > 48 {
		id = id[:48]
		id = strings.Trim(id, "-")
	}
	return id
}

// ReadySlices returns slices whose dependencies are all complete and that are
// still pending (or already marked ready). Order is stable by Order then ID.
func (p GoalSlicePlan) ReadySlices() []GoalSlice {
	p.Normalize()
	done := map[string]bool{}
	for _, s := range p.Slices {
		if s.Status == goalSliceStatusComplete || s.Status == goalSliceStatusSkipped {
			done[strings.ToLower(s.ID)] = true
		}
	}
	ready := make([]GoalSlice, 0, len(p.Slices))
	for _, s := range p.Slices {
		switch s.Status {
		case goalSliceStatusComplete, goalSliceStatusSkipped, goalSliceStatusRunning, goalSliceStatusBlocked:
			continue
		}
		ok := true
		for _, dep := range s.DependsOn {
			if !done[strings.ToLower(dep)] {
				ok = false
				break
			}
		}
		if ok {
			if s.Status == goalSliceStatusPending {
				s.Status = goalSliceStatusReady
			}
			ready = append(ready, s)
		}
	}
	return ready
}

// TopoOrder returns slice IDs in a valid topological order, or empty on cycle.
func (p GoalSlicePlan) TopoOrder() []string {
	p.Normalize()
	return goalSliceTopoOrder(p.Slices)
}

func goalSliceTopoOrder(slices []GoalSlice) []string {
	if len(slices) == 0 {
		return nil
	}
	index := map[string]int{}
	for i, s := range slices {
		index[strings.ToLower(s.ID)] = i
	}
	indegree := make([]int, len(slices))
	edges := make([][]int, len(slices))
	for i, s := range slices {
		for _, dep := range s.DependsOn {
			j, ok := index[strings.ToLower(dep)]
			if !ok {
				continue
			}
			edges[j] = append(edges[j], i)
			indegree[i]++
		}
	}
	queue := make([]int, 0, len(slices))
	for i := range slices {
		if indegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	out := make([]string, 0, len(slices))
	for len(queue) > 0 {
		// Stable: pick lowest Order then lowest index.
		best := 0
		for k := 1; k < len(queue); k++ {
			a, b := queue[k], queue[best]
			if slices[a].Order < slices[b].Order || (slices[a].Order == slices[b].Order && a < b) {
				best = k
			}
		}
		i := queue[best]
		queue = append(queue[:best], queue[best+1:]...)
		out = append(out, slices[i].ID)
		for _, j := range edges[i] {
			indegree[j]--
			if indegree[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	if len(out) != len(slices) {
		return nil
	}
	return out
}

func goalSlicePlanCycle(slices []GoalSlice) string {
	if goalSliceTopoOrder(slices) != nil {
		return ""
	}
	ids := make([]string, 0, len(slices))
	for _, s := range slices {
		ids = append(ids, s.ID)
	}
	return strings.Join(ids, " -> ")
}

// PlanItemsFromSlices projects the DAG into the legacy flat PlanItem list used
// by existing session/UI surfaces.
func PlanItemsFromSlices(plan GoalSlicePlan) []PlanItem {
	plan.Normalize()
	order := plan.TopoOrder()
	if len(order) == 0 {
		// Fall back to declaration order when topo fails (should be rare after Normalize).
		order = make([]string, 0, len(plan.Slices))
		for _, s := range plan.Slices {
			order = append(order, s.ID)
		}
	}
	byID := map[string]GoalSlice{}
	for _, s := range plan.Slices {
		byID[strings.ToLower(s.ID)] = s
	}
	items := make([]PlanItem, 0, len(order))
	for _, id := range order {
		s, ok := byID[strings.ToLower(id)]
		if !ok {
			continue
		}
		step := s.Name
		if outcome := strings.TrimSpace(s.Outcome); outcome != "" {
			step = s.Name + ": " + compactPromptSection(outcome, 160)
		}
		status := "pending"
		switch s.Status {
		case goalSliceStatusComplete:
			status = "completed"
		case goalSliceStatusRunning, goalSliceStatusReady:
			status = "in_progress"
		case goalSliceStatusBlocked:
			status = "blocked"
		case goalSliceStatusSkipped:
			status = "skipped"
		}
		items = append(items, PlanItem{Step: step, Status: status})
	}
	return items
}

// parseGoalSlicePlanFromText parses goal-to-slice-planner style output (or a
// close subset) into a GoalSlicePlan. Returns ok=false when no slices found.
func parseGoalSlicePlanFromText(text string) (GoalSlicePlan, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		return GoalSlicePlan{}, false
	}
	plan := GoalSlicePlan{CompiledFrom: "parseGoalSlicePlanFromText"}
	lines := strings.Split(text, "\n")
	section := ""
	var current *GoalSlice
	flush := func() {
		if current == nil {
			return
		}
		current.Normalize()
		if current.Name != "" || current.ID != "" {
			plan.Slices = append(plan.Slices, *current)
		}
		current = nil
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Section headings (with optional trailing colon or markdown #).
		heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		heading = strings.TrimSuffix(heading, ":")
		if m := goalSliceSectionHeading.FindStringSubmatch(heading); len(m) == 2 {
			flush()
			section = strings.ToLower(strings.TrimSpace(m[1]))
			continue
		}
		// Numbered slice under Slices (or free-form if no section yet / slices).
		if section == "" || section == "slices" {
			if m := goalSliceNumberedHeading.FindStringSubmatch(trimmed); len(m) == 3 {
				flush()
				order, _ := strconv.Atoi(m[1])
				name := strings.TrimSpace(m[2])
				// Avoid treating "KernForge execution plan" numbered steps as slices
				// when they appear after slices were already collected under a different
				// section — only accept numbered items in slices section or before any
				// known section if they look like slice entries (have following fields).
				current = &GoalSlice{
					Name:   name,
					Order:  order,
					Status: goalSliceStatusPending,
					Risk:   goalSliceRiskMedium,
				}
				section = "slices"
				continue
			}
		}
		if current != nil {
			if m := goalSliceFieldLine.FindStringSubmatch(trimmed); len(m) == 3 {
				key := strings.ToLower(strings.TrimSpace(m[1]))
				val := strings.TrimSpace(m[2])
				switch key {
				case "outcome":
					current.Outcome = val
				case "scope":
					current.Scope = val
				case "likely files":
					current.LikelyFiles = splitGoalSliceList(val)
				case "acceptance":
					if val != "" {
						current.Acceptance = append(current.Acceptance, val)
					}
				case "validation":
					current.Validation = val
				case "docs":
					current.Docs = val
				case "risk":
					current.Risk = val
				case "depends on":
					current.DependsOn = splitGoalSliceList(val)
				}
				continue
			}
			// Continuation bullets under the current slice field context.
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
				item := strings.TrimSpace(trimmed[2:])
				if item != "" && len(current.Acceptance) > 0 && current.Validation == "" && current.Docs == "" {
					// Prefer appending as extra acceptance when mid-slice.
					current.Acceptance = append(current.Acceptance, item)
				}
				continue
			}
		}
		// Top-level section content.
		switch section {
		case "objective":
			item := strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* ")
			if plan.ObjectiveRestated == "" {
				plan.ObjectiveRestated = item
			} else {
				plan.ObjectiveRestated += " " + item
			}
		case "non-goals":
			item := strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* ")
			if item != "" && !strings.EqualFold(item, "none") {
				plan.NonGoals = append(plan.NonGoals, item)
			}
		case "recommended first slice":
			item := strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* ")
			if plan.RecommendedFirst == "" {
				plan.RecommendedFirst = item
			}
		case "open questions":
			item := strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* ")
			if item != "" && !strings.EqualFold(item, "none") {
				plan.OpenQuestions = append(plan.OpenQuestions, item)
			}
		}
	}
	flush()

	// Reject "plans" that are only a flat numbered list with no slice fields —
	// those belong to the legacy PlanItem parser.
	if !goalSlicePlanHasStructuredFields(plan) {
		return GoalSlicePlan{}, false
	}
	plan.Normalize()
	if len(plan.Slices) == 0 {
		return GoalSlicePlan{}, false
	}
	return plan, true
}

func goalSlicePlanHasStructuredFields(plan GoalSlicePlan) bool {
	for _, s := range plan.Slices {
		if s.Outcome != "" || s.Scope != "" || s.Validation != "" || s.Docs != "" ||
			len(s.LikelyFiles) > 0 || len(s.Acceptance) > 0 || len(s.DependsOn) > 0 {
			return true
		}
		// Risk other than default medium after normalize is weak; check raw name length.
		if s.Risk != "" && !strings.EqualFold(s.Risk, goalSliceRiskMedium) {
			return true
		}
	}
	// Multiple named slices with section Objective is enough structure.
	if plan.ObjectiveRestated != "" && len(plan.Slices) >= 2 {
		return true
	}
	return false
}

func splitGoalSliceList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") || strings.EqualFold(value, "n/a") {
		return nil
	}
	// Split on commas or semicolons; keep path-like tokens intact.
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "`\"'")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// renderGoalSlicePlanMarkdown is the durable human-editable slice artifact body.
func renderGoalSlicePlanMarkdown(goal GoalState, plan GoalSlicePlan) string {
	plan.Normalize()
	var b strings.Builder
	fmt.Fprintf(&b, "# Goal Slice Plan\n\n")
	fmt.Fprintf(&b, "Goal ID: %s\n", valueOrUnset(goal.ID))
	fmt.Fprintf(&b, "Status: %s\n\n", valueOrUnset(goal.Status))
	fmt.Fprintf(&b, "## Objective\n\n%s\n\n", firstNonBlankString(plan.ObjectiveRestated, goal.Objective))
	if len(plan.NonGoals) > 0 {
		b.WriteString("## Non-goals\n\n")
		for _, item := range plan.NonGoals {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Slices\n\n")
	if len(plan.Slices) == 0 {
		b.WriteString("_No slices._\n\n")
	}
	for i, s := range plan.Slices {
		fmt.Fprintf(&b, "%d. %s\n", i+1, firstNonBlankString(s.Name, s.ID))
		fmt.Fprintf(&b, "   ID: %s\n", s.ID)
		fmt.Fprintf(&b, "   Status: %s\n", s.Status)
		if s.Outcome != "" {
			fmt.Fprintf(&b, "   Outcome: %s\n", s.Outcome)
		}
		if s.Scope != "" {
			fmt.Fprintf(&b, "   Scope: %s\n", s.Scope)
		}
		if len(s.LikelyFiles) > 0 {
			fmt.Fprintf(&b, "   Likely files: %s\n", strings.Join(s.LikelyFiles, ", "))
		}
		if len(s.Acceptance) > 0 {
			fmt.Fprintf(&b, "   Acceptance: %s\n", strings.Join(s.Acceptance, "; "))
		}
		if s.Validation != "" {
			fmt.Fprintf(&b, "   Validation: %s\n", s.Validation)
		}
		if s.Docs != "" {
			fmt.Fprintf(&b, "   Docs: %s\n", s.Docs)
		}
		fmt.Fprintf(&b, "   Risk: %s\n", s.Risk)
		if len(s.DependsOn) > 0 {
			fmt.Fprintf(&b, "   Depends on: %s\n", strings.Join(s.DependsOn, ", "))
		}
		b.WriteString("\n")
	}
	if plan.RecommendedFirst != "" {
		fmt.Fprintf(&b, "## Recommended first slice\n\n- %s\n\n", plan.RecommendedFirst)
	}
	if order := plan.TopoOrder(); len(order) > 0 {
		b.WriteString("## Execution order\n\n")
		for i, id := range order {
			fmt.Fprintf(&b, "%d. %s\n", i+1, id)
		}
		b.WriteString("\n")
	}
	if ready := plan.ReadySlices(); len(ready) > 0 {
		b.WriteString("## Ready now\n\n")
		for _, s := range ready {
			fmt.Fprintf(&b, "- %s (%s)\n", s.ID, s.Name)
		}
		b.WriteString("\n")
	}
	if len(plan.OpenQuestions) > 0 {
		b.WriteString("## Open questions\n\n")
		for _, q := range plan.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", q)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// fallbackGoalSlicePlan builds a single-slice plan from the objective so every
// goal can carry a SlicePlan even when the model returns only a flat list.
func fallbackGoalSlicePlan(goal GoalState) GoalSlicePlan {
	objective := strings.TrimSpace(goal.Objective)
	acceptance := []string{}
	if goal.AcceptanceSpec != nil {
		acceptance = goal.AcceptanceSpec.PrimaryCriteriaTexts()
	}
	if len(acceptance) == 0 {
		acceptance = append([]string(nil), goal.UserCriteria...)
	}
	if len(acceptance) == 0 {
		acceptance = []string{"Workspace demonstrates the objective with concrete evidence."}
	}
	plan := GoalSlicePlan{
		ObjectiveRestated: firstNonBlankString(objective, "(empty objective)"),
		CompiledFrom:      "fallbackGoalSlicePlan",
		Slices: []GoalSlice{{
			ID:         "slice-01",
			Name:       "Deliver objective",
			Outcome:    objective,
			Scope:      "Changes required to satisfy the objective; no unrelated refactors.",
			Acceptance: acceptance,
			Validation: "Targeted tests or manual evidence for the changed scope.",
			Docs:       "Update docs only when the objective requires it.",
			Risk:       goalSliceRiskMedium,
			Status:     goalSliceStatusPending,
			Order:      1,
		}},
	}
	if goal.AcceptanceSpec != nil {
		plan.NonGoals = append([]string(nil), goal.AcceptanceSpec.NonGoals...)
	}
	plan.Normalize()
	return plan
}
