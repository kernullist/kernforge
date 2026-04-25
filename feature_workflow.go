package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	featureStatusDraft        = "draft"
	featureStatusPlanned      = "planned"
	featureStatusImplementing = "implementing"
	featureStatusImplemented  = "implemented"
	featureStatusDone         = "done"
	featureStatusBlocked      = "blocked"

	featureSpecFileName           = "spec.md"
	featurePlanFileName           = "plan.md"
	featureTasksFileName          = "tasks.md"
	featureImplementationFileName = "implementation.md"
	featureManifestFileName       = "feature.json"
)

type FeatureWorkflow struct {
	ID                 string    `json:"id"`
	Request            string    `json:"request"`
	Title              string    `json:"title"`
	Slug               string    `json:"slug"`
	Workspace          string    `json:"workspace"`
	Status             string    `json:"status"`
	Planner            string    `json:"planner"`
	Reviewer           string    `json:"reviewer,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	SpecPath           string    `json:"spec_path,omitempty"`
	PlanPath           string    `json:"plan_path,omitempty"`
	TasksPath          string    `json:"tasks_path,omitempty"`
	ImplementationPath string    `json:"implementation_path,omitempty"`
	PlanReviewed       bool      `json:"plan_reviewed,omitempty"`
	PlanRounds         int       `json:"plan_rounds,omitempty"`
}

type FeatureStore struct {
	root string
}

func NewFeatureStore(workspaceRoot string) *FeatureStore {
	return &FeatureStore{
		root: filepath.Join(workspaceRoot, userConfigDirName, "features"),
	}
}

func (s *FeatureStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *FeatureStore) featureDir(id string) string {
	return filepath.Join(s.root, strings.TrimSpace(id))
}

func (s *FeatureStore) manifestPath(id string) string {
	return filepath.Join(s.featureDir(id), featureManifestFileName)
}

func (s *FeatureStore) artifactPath(id string, name string) string {
	return filepath.Join(s.featureDir(id), name)
}

func (s *FeatureStore) normalize(feature *FeatureWorkflow) {
	if s == nil || feature == nil {
		return
	}
	feature.ID = strings.TrimSpace(feature.ID)
	feature.Request = strings.TrimSpace(feature.Request)
	feature.Title = strings.TrimSpace(feature.Title)
	feature.Slug = strings.TrimSpace(feature.Slug)
	feature.Workspace = strings.TrimSpace(feature.Workspace)
	feature.Status = strings.TrimSpace(feature.Status)
	feature.Planner = strings.TrimSpace(feature.Planner)
	feature.Reviewer = strings.TrimSpace(feature.Reviewer)
	if feature.Status == "" {
		feature.Status = featureStatusDraft
	}
	if feature.Title == "" {
		feature.Title = compactPersistentMemoryText(feature.Request, 72)
	}
	if feature.Slug == "" {
		feature.Slug = sanitizeFeatureWorkflowSlug(feature.Request)
	}
	if feature.SpecPath == "" && feature.ID != "" {
		feature.SpecPath = s.artifactPath(feature.ID, featureSpecFileName)
	}
	if feature.PlanPath == "" && feature.ID != "" {
		feature.PlanPath = s.artifactPath(feature.ID, featurePlanFileName)
	}
	if feature.TasksPath == "" && feature.ID != "" {
		feature.TasksPath = s.artifactPath(feature.ID, featureTasksFileName)
	}
	if feature.ImplementationPath == "" && feature.ID != "" {
		feature.ImplementationPath = s.artifactPath(feature.ID, featureImplementationFileName)
	}
}

func (s *FeatureStore) Save(feature FeatureWorkflow) error {
	if s == nil {
		return fmt.Errorf("feature store is not configured")
	}
	s.normalize(&feature)
	if strings.TrimSpace(feature.ID) == "" {
		return fmt.Errorf("feature id is required")
	}
	if err := os.MkdirAll(s.featureDir(feature.ID), 0o755); err != nil {
		return err
	}
	if feature.CreatedAt.IsZero() {
		feature.CreatedAt = time.Now()
	}
	feature.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(feature, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.manifestPath(feature.ID), append(data, '\n'), 0o644)
}

func (s *FeatureStore) Load(id string) (FeatureWorkflow, error) {
	if s == nil {
		return FeatureWorkflow{}, fmt.Errorf("feature store is not configured")
	}
	path := s.manifestPath(strings.TrimSpace(id))
	data, err := os.ReadFile(path)
	if err != nil {
		return FeatureWorkflow{}, err
	}
	var feature FeatureWorkflow
	if err := json.Unmarshal(data, &feature); err != nil {
		return FeatureWorkflow{}, err
	}
	s.normalize(&feature)
	return feature, nil
}

func (s *FeatureStore) List() ([]FeatureWorkflow, error) {
	if s == nil {
		return nil, fmt.Errorf("feature store is not configured")
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]FeatureWorkflow, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		feature, loadErr := s.Load(entry.Name())
		if loadErr != nil {
			continue
		}
		items = append(items, feature)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (s *FeatureStore) Create(workspace string, request string, planner string, reviewer string) (FeatureWorkflow, error) {
	if s == nil {
		return FeatureWorkflow{}, fmt.Errorf("feature store is not configured")
	}
	now := time.Now()
	id := newFeatureWorkflowID(request, now)
	feature := FeatureWorkflow{
		ID:                 id,
		Request:            strings.TrimSpace(request),
		Title:              compactPersistentMemoryText(strings.TrimSpace(request), 72),
		Slug:               sanitizeFeatureWorkflowSlug(request),
		Workspace:          strings.TrimSpace(workspace),
		Status:             featureStatusDraft,
		Planner:            strings.TrimSpace(planner),
		Reviewer:           strings.TrimSpace(reviewer),
		CreatedAt:          now,
		UpdatedAt:          now,
		SpecPath:           s.artifactPath(id, featureSpecFileName),
		PlanPath:           s.artifactPath(id, featurePlanFileName),
		TasksPath:          s.artifactPath(id, featureTasksFileName),
		ImplementationPath: s.artifactPath(id, featureImplementationFileName),
	}
	if err := s.Save(feature); err != nil {
		return FeatureWorkflow{}, err
	}
	return feature, nil
}

func (s *FeatureStore) WriteArtifact(path string, content string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	text := strings.TrimSpace(content)
	if text != "" {
		text += "\n"
	}
	return atomicWriteFile(path, []byte(text), 0o644)
}

func sanitizeFeatureWorkflowSlug(request string) string {
	text := strings.ToLower(strings.TrimSpace(request))
	if text == "" {
		return "feature"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "feature"
	}
	if len(out) > 48 {
		out = strings.Trim(out[:48], "-")
	}
	if out == "" {
		out = "feature"
	}
	return out
}

func newFeatureWorkflowID(request string, now time.Time) string {
	return fmt.Sprintf("%s-%03d-%s", now.Format("20060102-150405"), now.Nanosecond()/1_000_000, sanitizeFeatureWorkflowSlug(request))
}

func buildNewFeatureSpecPrompt(request string, analysisContext string) string {
	request = strings.TrimSpace(request)
	var b strings.Builder
	b.WriteString("Create a tracked feature specification for the current workspace.\n\n")
	b.WriteString("Feature request:\n")
	b.WriteString(request)
	b.WriteString("\n\n")
	if strings.TrimSpace(analysisContext) != "" {
		b.WriteString("Relevant project analysis context:\n")
		b.WriteString(strings.TrimSpace(analysisContext))
		b.WriteString("\n\n")
	}
	b.WriteString("Output markdown with these sections:\n")
	b.WriteString("1. Summary\n")
	b.WriteString("2. User Value\n")
	b.WriteString("3. Scope\n")
	b.WriteString("4. Command or Workflow Changes\n")
	b.WriteString("5. Acceptance Criteria\n")
	b.WriteString("6. Affected Code Areas\n")
	b.WriteString("7. Verification\n")
	b.WriteString("8. Risks and Rollback Notes\n")
	return b.String()
}

func buildNewFeaturePlanningPrompt(request string, spec string, analysisContext string) string {
	request = strings.TrimSpace(request)
	spec = strings.TrimSpace(spec)
	var b strings.Builder
	b.WriteString("Create an implementation plan for a tracked feature in the current workspace.\n\n")
	b.WriteString("Feature request:\n")
	b.WriteString(request)
	b.WriteString("\n\nFeature specification:\n")
	b.WriteString(spec)
	b.WriteString("\n\n")
	if strings.TrimSpace(analysisContext) != "" {
		b.WriteString("Relevant project analysis context:\n")
		b.WriteString(strings.TrimSpace(analysisContext))
		b.WriteString("\n\n")
	}
	b.WriteString("Requirements:\n")
	b.WriteString("- Output the plan as a numbered list with one actionable step per item.\n")
	b.WriteString("- Include command wiring, state/artifact updates, tests, docs, and verification when relevant.\n")
	b.WriteString("- Prefer concrete file and function targets over generic descriptions.\n")
	return b.String()
}

func buildNewFeatureExecutionPrompt(request string, spec string, plan string) string {
	request = strings.TrimSpace(request)
	spec = strings.TrimSpace(spec)
	plan = strings.TrimSpace(plan)
	var b strings.Builder
	b.WriteString("Implement the tracked feature described below.\n\n")
	b.WriteString("Feature request:\n")
	b.WriteString(request)
	b.WriteString("\n\nFeature specification:\n")
	b.WriteString(spec)
	b.WriteString("\n\nImplementation plan:\n")
	b.WriteString(plan)
	b.WriteString("\n\nExecution requirements:\n")
	b.WriteString("- Inspect the relevant files before editing.\n")
	b.WriteString("- Keep the tracked feature artifacts aligned with the implementation.\n")
	b.WriteString("- Update the shared task list as you make progress.\n")
	b.WriteString("- Run relevant verification before finishing when practical.\n")
	b.WriteString("- If you cannot finish cleanly, explain the blocker and the remaining work.\n")
	return b.String()
}

func newFeatureSystemPromptSpecifier(workspaceRoot string, memoryContext string) string {
	var b strings.Builder
	b.WriteString("You are a senior software architect preparing a tracked feature specification.\n")
	b.WriteString("Write precise, implementation-ready markdown grounded in the current codebase.\n")
	fmt.Fprintf(&b, "Workspace root: %s\n", workspaceRoot)
	if strings.TrimSpace(memoryContext) != "" {
		b.WriteString("\nLoaded memory context:\n")
		b.WriteString(memoryContext)
		b.WriteString("\n")
	}
	return b.String()
}

func renderFeatureTasksMarkdown(feature FeatureWorkflow, items []PlanItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Tasks for %s\n\n", feature.ID)
	fmt.Fprintf(&b, "Feature: %s\n\n", feature.Title)
	if len(items) == 0 {
		b.WriteString("- [ ] No parsed tasks yet\n")
		return b.String()
	}
	for _, item := range items {
		marker := " "
		if strings.EqualFold(item.Status, "completed") {
			marker = "x"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", marker, item.Step)
	}
	return b.String()
}

func (rt *runtimeState) featureStore() *FeatureStore {
	if rt == nil {
		return nil
	}
	return NewFeatureStore(rt.workspace.BaseRoot)
}

func (rt *runtimeState) setActiveFeature(feature FeatureWorkflow) error {
	if rt == nil || rt.session == nil || rt.store == nil {
		return nil
	}
	previous := strings.TrimSpace(rt.session.ActiveFeatureID)
	rt.session.ActiveFeatureID = feature.ID
	switch {
	case previous != "" && !strings.EqualFold(previous, feature.ID):
		rt.session.LastFeatureID = previous
	case strings.TrimSpace(rt.session.LastFeatureID) == "":
		rt.session.LastFeatureID = feature.ID
	}
	return rt.store.Save(rt.session)
}

func (rt *runtimeState) latestFeatureAnalysisContext(query string) string {
	if rt == nil || rt.agent == nil {
		return ""
	}
	artifacts, ok := rt.agent.loadLatestProjectAnalysisArtifacts()
	if !ok {
		return ""
	}
	return strings.TrimSpace(renderRelevantProjectAnalysisContext(artifacts, query))
}

func (rt *runtimeState) resolveFeature(arg string) (FeatureWorkflow, *FeatureStore, error) {
	store := rt.featureStore()
	if store == nil {
		return FeatureWorkflow{}, nil, fmt.Errorf("feature store is not configured")
	}
	targetID := strings.TrimSpace(arg)
	if targetID == "" && rt.session != nil {
		targetID = strings.TrimSpace(rt.session.ActiveFeatureID)
	}
	if targetID == "" {
		items, err := store.List()
		if err != nil {
			return FeatureWorkflow{}, nil, err
		}
		if len(items) > 0 {
			targetID = items[0].ID
		}
	}
	if targetID == "" {
		return FeatureWorkflow{}, nil, fmt.Errorf("no tracked features found. Start one with /new-feature <task>")
	}
	feature, err := resolveFeatureByIDOrPrefix(store, targetID)
	if err != nil {
		return FeatureWorkflow{}, nil, err
	}
	_ = rt.setActiveFeature(feature)
	return feature, store, nil
}

func resolveFeatureByIDOrPrefix(store *FeatureStore, target string) (FeatureWorkflow, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return FeatureWorkflow{}, fmt.Errorf("feature id is required")
	}
	feature, err := store.Load(target)
	if err == nil {
		return feature, nil
	}
	if !os.IsNotExist(err) {
		return FeatureWorkflow{}, err
	}
	items, listErr := store.List()
	if listErr != nil {
		return FeatureWorkflow{}, listErr
	}
	matches := make([]FeatureWorkflow, 0, 1)
	lowerTarget := strings.ToLower(target)
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.ID)), lowerTarget) {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 0:
		return FeatureWorkflow{}, fmt.Errorf("feature not found: %s", target)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, item := range matches {
			ids = append(ids, item.ID)
		}
		return FeatureWorkflow{}, fmt.Errorf("feature id prefix is ambiguous: %s (%s)", target, strings.Join(ids, ", "))
	}
}

func (rt *runtimeState) handleNewFeatureCommand(args string) error {
	if rt.agent == nil || rt.agent.Client == nil {
		return fmt.Errorf("no model provider is configured")
	}
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		if rt.session != nil && strings.TrimSpace(rt.session.ActiveFeatureID) != "" {
			return rt.handleNewFeatureStatusCommand("")
		}
		return fmt.Errorf("usage: /new-feature <task description> | /new-feature [start|list|status|plan|implement|close] ...")
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return fmt.Errorf("usage: /new-feature <task description>")
	}
	subcommand := strings.ToLower(fields[0])
	rest := strings.TrimSpace(trimmed[len(fields[0]):])
	switch subcommand {
	case "start":
		return rt.handleNewFeatureStartCommand(rest)
	case "list":
		return rt.handleNewFeatureListCommand()
	case "status":
		return rt.handleNewFeatureStatusCommand(rest)
	case "plan":
		return rt.handleNewFeaturePlanCommand(rest)
	case "implement":
		return rt.handleNewFeatureImplementCommand(rest)
	case "close":
		return rt.handleNewFeatureCloseCommand(rest)
	default:
		return rt.handleNewFeatureStartCommand(trimmed)
	}
}

func (rt *runtimeState) handleNewFeatureStartCommand(request string) error {
	request = strings.TrimSpace(request)
	if request == "" {
		return fmt.Errorf("usage: /new-feature <task description>")
	}
	store := rt.featureStore()
	plannerLabel := rt.session.Provider + " / " + rt.session.Model
	reviewerLabel := ""
	if rt.cfg.PlanReview != nil {
		reviewerLabel = rt.cfg.PlanReview.Provider + " / " + rt.cfg.PlanReview.Model
	}
	feature, err := store.Create(rt.workspace.BaseRoot, request, plannerLabel, reviewerLabel)
	if err != nil {
		return err
	}
	if err := rt.setActiveFeature(feature); err != nil {
		return err
	}

	fmt.Fprintln(rt.writer, rt.ui.section("New Feature"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("feature_id", feature.ID))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("status", feature.Status))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("planner", feature.Planner))
	if feature.Reviewer != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("reviewer", feature.Reviewer))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("reviewer", "not configured"))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("artifacts", store.featureDir(feature.ID)))
	fmt.Fprintln(rt.writer)

	return rt.generateNewFeatureArtifacts(store, &feature)
}

func (rt *runtimeState) handleNewFeaturePlanCommand(arg string) error {
	feature, store, err := rt.resolveFeature(arg)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("New Feature Plan"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("feature_id", feature.ID))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("status", feature.Status))
	fmt.Fprintln(rt.writer)
	return rt.generateNewFeatureArtifacts(store, &feature)
}

func (rt *runtimeState) generateNewFeatureArtifacts(store *FeatureStore, feature *FeatureWorkflow) error {
	if store == nil || feature == nil {
		return fmt.Errorf("feature workflow is not available")
	}
	feature.Planner = strings.TrimSpace(rt.session.Provider + " / " + rt.session.Model)
	if rt.cfg.PlanReview != nil {
		feature.Reviewer = strings.TrimSpace(rt.cfg.PlanReview.Provider + " / " + rt.cfg.PlanReview.Model)
	} else {
		feature.Reviewer = ""
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopEscapeWatcher := startEscapeWatcher(cancel, rt.shouldHonorRequestCancel, rt.confirmRequestCancel)
	defer stopEscapeWatcher()

	memoryContext := strings.TrimSpace(rt.memory.Combined())
	analysisContext := rt.latestFeatureAnalysisContext(feature.Request)

	fmt.Fprintln(rt.writer, rt.ui.hintLine("Generating feature specification..."))
	specPrompt := rt.appendSimulationPlanningContext(buildNewFeatureSpecPrompt(feature.Request, analysisContext), feature.Request)
	specResp, err := rt.agent.Client.Complete(requestCtx, ChatRequest{
		Model:       rt.session.Model,
		System:      newFeatureSystemPromptSpecifier(rt.session.WorkingDir, memoryContext),
		Messages:    []Message{{Role: "user", Text: specPrompt}},
		MaxTokens:   rt.cfg.MaxTokens,
		Temperature: rt.cfg.Temperature,
	})
	if err != nil {
		if requestCtx.Err() == context.Canceled {
			rt.noteRecentRequestCancel()
			return fmt.Errorf("new-feature specification canceled by user")
		}
		return fmt.Errorf("failed to generate feature specification: %w", err)
	}
	specText := strings.TrimSpace(specResp.Message.Text)
	if specText == "" {
		return fmt.Errorf("feature specification was empty")
	}
	if err := store.WriteArtifact(feature.SpecPath, specText); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Feature Spec"))
	fmt.Fprintln(rt.writer, specText)
	fmt.Fprintln(rt.writer)

	planPrompt := rt.appendSimulationPlanningContext(buildNewFeaturePlanningPrompt(feature.Request, specText, analysisContext), feature.Request+"\n"+specText)
	planText := ""
	planReviewed := false
	planRounds := 1
	if rt.cfg.PlanReview != nil {
		reviewerClient, reviewErr := createReviewerClient(rt.cfg.PlanReview, rt.cfg)
		if reviewErr != nil {
			return fmt.Errorf("failed to create reviewer client: %w", reviewErr)
		}
		result, runErr := RunPlanReview(
			requestCtx,
			rt.agent.Client,
			rt.session.Model,
			reviewerClient,
			rt.cfg.PlanReview.Model,
			planPrompt,
			rt.session.WorkingDir,
			memoryContext,
			rt.cfg.MaxTokens,
			rt.cfg.Temperature,
			func(status string) {
				fmt.Fprintln(rt.writer, rt.ui.hintLine(status))
			},
		)
		if runErr != nil {
			if requestCtx.Err() == context.Canceled {
				rt.noteRecentRequestCancel()
				return fmt.Errorf("new-feature planning canceled by user")
			}
			return runErr
		}
		for i, round := range result.ReviewLog {
			fmt.Fprintln(rt.writer, rt.ui.section(fmt.Sprintf("Round %d - Plan", i+1)))
			fmt.Fprintln(rt.writer, round.Plan)
			fmt.Fprintln(rt.writer)
			fmt.Fprintln(rt.writer, rt.ui.section(fmt.Sprintf("Round %d - Review", i+1)))
			fmt.Fprintln(rt.writer, round.Review)
			fmt.Fprintln(rt.writer)
		}
		planText = strings.TrimSpace(result.FinalPlan)
		planReviewed = true
		planRounds = result.Rounds
	} else {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Generating implementation plan with the active model..."))
		planResp, planErr := rt.agent.Client.Complete(requestCtx, ChatRequest{
			Model:       rt.session.Model,
			System:      planReviewSystemPromptPlanner(rt.session.WorkingDir, memoryContext),
			Messages:    []Message{{Role: "user", Text: planPrompt}},
			MaxTokens:   rt.cfg.MaxTokens,
			Temperature: rt.cfg.Temperature,
		})
		if planErr != nil {
			if requestCtx.Err() == context.Canceled {
				rt.noteRecentRequestCancel()
				return fmt.Errorf("new-feature planning canceled by user")
			}
			return fmt.Errorf("failed to generate feature plan: %w", planErr)
		}
		planText = strings.TrimSpace(planResp.Message.Text)
	}
	if planText == "" {
		return fmt.Errorf("feature plan was empty")
	}
	if err := store.WriteArtifact(feature.PlanPath, planText); err != nil {
		return err
	}
	if err := rt.seedSessionPlanFromText(planText); err != nil {
		return err
	}
	if err := store.WriteArtifact(feature.TasksPath, renderFeatureTasksMarkdown(*feature, parsePlanItemsFromText(planText))); err != nil {
		return err
	}

	feature.Status = featureStatusPlanned
	feature.PlanReviewed = planReviewed
	feature.PlanRounds = planRounds
	if err := store.Save(*feature); err != nil {
		return err
	}
	if err := rt.setActiveFeature(*feature); err != nil {
		return err
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Feature Plan"))
	fmt.Fprintln(rt.writer, planText)
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.statusKV("spec_path", feature.SpecPath))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("plan_path", feature.PlanPath))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("tasks_path", feature.TasksPath))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Next: run /new-feature implement to execute the tracked feature plan."))
	return nil
}

func (rt *runtimeState) handleNewFeatureListCommand() error {
	store := rt.featureStore()
	items, err := store.List()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No tracked features found."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Tracked Features"))
	for _, item := range items {
		active := ""
		if rt.session != nil && strings.EqualFold(rt.session.ActiveFeatureID, item.ID) {
			active = " [active]"
		}
		fmt.Fprintf(rt.writer, "%s  status=%s  %s%s\n", rt.ui.dim(item.ID), item.Status, item.Title, active)
	}
	return nil
}

func (rt *runtimeState) handleNewFeatureStatusCommand(arg string) error {
	feature, _, err := rt.resolveFeature(arg)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Tracked Feature"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("feature_id", feature.ID))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("status", feature.Status))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("title", feature.Title))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("created_at", feature.CreatedAt.Format(time.RFC3339)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("updated_at", feature.UpdatedAt.Format(time.RFC3339)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("spec_path", valueOrUnset(feature.SpecPath)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("plan_path", valueOrUnset(feature.PlanPath)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("tasks_path", valueOrUnset(feature.TasksPath)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("implementation_path", valueOrUnset(feature.ImplementationPath)))
	if strings.TrimSpace(feature.Request) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Request: "+feature.Request))
	}
	if tasks := readFeatureTasksPreview(feature.TasksPath, 8); len(tasks) > 0 {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.section("Tasks"))
		for _, task := range tasks {
			fmt.Fprintln(rt.writer, task)
		}
	}
	if handoff := featureStatusHandoff(feature); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	if campaign, ok := rt.latestFeatureFuzzCampaign(); ok {
		if handoff := featureFuzzHandoff(feature, campaign); strings.TrimSpace(handoff) != "" {
			fmt.Fprintln(rt.writer)
			fmt.Fprintln(rt.writer, handoff)
		}
	}
	return nil
}

func (rt *runtimeState) latestFeatureFuzzCampaign() (FuzzCampaign, bool) {
	if rt == nil || rt.fuzzCampaigns == nil {
		return FuzzCampaign{}, false
	}
	items, err := rt.fuzzCampaigns.ListRecent(workspaceSnapshotRoot(rt.workspace), 1)
	if err != nil || len(items) == 0 {
		return FuzzCampaign{}, false
	}
	campaign := normalizeFuzzCampaign(items[0])
	if strings.TrimSpace(campaign.ManifestPath) != "" {
		if data, err := os.ReadFile(campaign.ManifestPath); err == nil {
			var manifest FuzzCampaign
			if json.Unmarshal(data, &manifest) == nil {
				campaign = normalizeFuzzCampaign(manifest)
			}
		}
	}
	if len(campaign.NativeResults) == 0 && len(campaign.SeedArtifacts) == 0 {
		return FuzzCampaign{}, false
	}
	return campaign, true
}

func readFeatureTasksPreview(path string, limit int) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := make([]string, 0, limit)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "- [") {
			out = append(out, line)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func latestAssistantMessageText(sess *Session) string {
	if sess == nil {
		return ""
	}
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		msg := sess.Messages[i]
		if !strings.EqualFold(msg.Role, "assistant") {
			continue
		}
		if strings.TrimSpace(msg.Text) == "" {
			continue
		}
		return strings.TrimSpace(msg.Text)
	}
	return ""
}

func (rt *runtimeState) handleNewFeatureImplementCommand(arg string) error {
	feature, store, err := rt.resolveFeature(arg)
	if err != nil {
		return err
	}
	specText, err := os.ReadFile(feature.SpecPath)
	if err != nil {
		return fmt.Errorf("feature spec is missing; rerun /new-feature plan %s", feature.ID)
	}
	planText, err := os.ReadFile(feature.PlanPath)
	if err != nil {
		return fmt.Errorf("feature plan is missing; rerun /new-feature plan %s", feature.ID)
	}

	if rt.interactive {
		proceed, confirmErr := rt.confirm("Proceed with this tracked feature implementation?")
		if confirmErr != nil {
			return confirmErr
		}
		if !proceed {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Tracked feature implementation aborted by user."))
			return nil
		}
	}

	if err := rt.ensureTrackedFeatureWorktree(feature); err != nil {
		return err
	}

	feature.Status = featureStatusImplementing
	if err := store.Save(feature); err != nil {
		return err
	}
	if err := rt.setActiveFeature(feature); err != nil {
		return err
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopEscapeWatcher := startEscapeWatcher(cancel, rt.shouldHonorRequestCancel, rt.confirmRequestCancel)
	defer stopEscapeWatcher()

	prompt := buildNewFeatureExecutionPrompt(feature.Request, string(specText), string(planText))
	prompt = rt.appendSimulationPlanningContext(prompt, feature.Request+"\n"+string(planText))
	fmt.Fprintln(rt.writer, rt.ui.section("Tracked Feature Implementation"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("feature_id", feature.ID))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Executing tracked feature plan..."))
	reply, err := rt.runAgentReply(requestCtx, prompt)
	if err != nil {
		if requestCtx.Err() == context.Canceled {
			rt.noteRecentRequestCancel()
			feature.Status = featureStatusPlanned
			_ = store.Save(feature)
			return fmt.Errorf("tracked feature implementation canceled by user")
		}
		feature.Status = featureStatusBlocked
		_ = store.Save(feature)
		return err
	}
	if strings.TrimSpace(reply) == "" {
		reply = latestAssistantMessageText(rt.session)
	}
	if strings.TrimSpace(reply) == "" {
		feature.Status = featureStatusBlocked
		_ = store.Save(feature)
		return fmt.Errorf("tracked feature implementation finished without a final assistant summary")
	}
	if err := store.WriteArtifact(feature.ImplementationPath, reply); err != nil {
		return err
	}
	feature.Status = featureStatusImplemented
	if err := store.Save(feature); err != nil {
		return err
	}
	rt.printAssistant(reply)
	fmt.Fprintln(rt.writer, rt.ui.statusKV("implementation_path", feature.ImplementationPath))
	if handoff := featureStatusHandoff(feature); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleNewFeatureCloseCommand(arg string) error {
	feature, store, err := rt.resolveFeature(arg)
	if err != nil {
		return err
	}
	feature.Status = featureStatusDone
	if err := store.Save(feature); err != nil {
		return err
	}
	if err := rt.setActiveFeature(feature); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Marked tracked feature as done: "+feature.ID))
	if handoff := featureCloseHandoff(feature, rt.session != nil && rt.session.Worktree != nil); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}
