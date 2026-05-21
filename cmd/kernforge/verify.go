package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type VerificationStatus string
type VerificationMode string

const (
	VerificationPending VerificationStatus = "pending"
	VerificationPassed  VerificationStatus = "passed"
	VerificationFailed  VerificationStatus = "failed"
	VerificationSkipped VerificationStatus = "skipped"

	VerificationAdaptive VerificationMode = "adaptive"
	VerificationFull     VerificationMode = "full"

	defaultAdaptiveFullRegressionInterval = 5
)

type VerificationStep struct {
	Label             string             `json:"label"`
	Command           string             `json:"command"`
	ResolvedCommand   string             `json:"resolved_command,omitempty"`
	Informational     bool               `json:"informational,omitempty"`
	Scope             string             `json:"scope,omitempty"`
	Stage             string             `json:"stage,omitempty"`
	Tags              []string           `json:"tags,omitempty"`
	ContinueOnFailure bool               `json:"continue_on_failure,omitempty"`
	StopOnFailure     bool               `json:"stop_on_failure,omitempty"`
	Status            VerificationStatus `json:"status"`
	FailureKind       string             `json:"failure_kind,omitempty"`
	Hint              string             `json:"hint,omitempty"`
	Output            string             `json:"output,omitempty"`
	DurationMs        int64              `json:"duration_ms,omitempty"`
	PlannerPriority   int                `json:"-"`
}

type VerificationReport struct {
	GeneratedAt  time.Time          `json:"generated_at"`
	Trigger      string             `json:"trigger"`
	Mode         VerificationMode   `json:"mode,omitempty"`
	Decision     string             `json:"decision,omitempty"`
	Workspace    string             `json:"workspace"`
	ChangedPaths []string           `json:"changed_paths,omitempty"`
	Steps        []VerificationStep `json:"steps"`
}

type VerificationPlan struct {
	Mode         VerificationMode
	ChangedPaths []string
	Steps        []VerificationStep
	PlannerNote  string
}

func isEditTool(name string) bool {
	switch name {
	case "apply_edit_proposal", "apply_patch", "write_file", "replace_in_file":
		return true
	default:
		return false
	}
}

func (a *Agent) autoVerifyChanges(ctx context.Context) (VerificationReport, bool) {
	if !configAutoVerify(a.Config) {
		return VerificationReport{}, false
	}
	changed := collectAutomaticVerificationChangedPaths(a.Config, a.Workspace.Root, a.Session)
	if len(changed) == 0 {
		return VerificationReport{}, false
	}
	if a.VerifyChanges != nil {
		if ok, err := a.confirmAutomaticVerification(VerificationPlan{
			Mode:         VerificationAdaptive,
			ChangedPaths: append([]string(nil), changed...),
			Steps: []VerificationStep{{
				Label:   "configured verification",
				Command: "configured verification callback",
				Status:  VerificationPending,
			}},
		}); err != nil {
			report := skippedVerificationReportForPlan(a.Workspace.Root, "automatic", VerificationPlan{
				Mode:         VerificationAdaptive,
				ChangedPaths: append([]string(nil), changed...),
				Steps: []VerificationStep{{
					Label:   "configured verification",
					Command: "configured verification callback",
					Status:  VerificationPending,
				}},
			}, "Automatic verification confirmation failed: "+err.Error())
			a.recordVerificationReport(report)
			return report, true
		} else if !ok {
			report := skippedVerificationReportForPlan(a.Workspace.Root, "automatic", VerificationPlan{
				Mode:         VerificationAdaptive,
				ChangedPaths: append([]string(nil), changed...),
				Steps: []VerificationStep{{
					Label:   "configured verification",
					Command: "configured verification callback",
					Status:  VerificationPending,
				}},
			}, "Automatic verification was declined by the user.")
			a.recordVerificationReport(report)
			return report, true
		}
		report, ok := a.VerifyChanges(ctx)
		if ok && a.Session != nil {
			if report.GeneratedAt.IsZero() {
				report.GeneratedAt = time.Now()
			}
			if strings.TrimSpace(report.Trigger) == "" {
				report.Trigger = "automatic"
			}
			if strings.TrimSpace(report.Workspace) == "" {
				report.Workspace = a.Workspace.Root
			}
			if len(report.ChangedPaths) == 0 {
				report.ChangedPaths = append([]string(nil), changed...)
			}
			a.recordVerificationReport(report)
		}
		return report, ok
	}
	report, ok := runRecommendedVerification(ctx, a.Workspace, a.Session, a.VerifyHistory, "automatic", changed, a.confirmAutomaticVerification)
	if !ok {
		return VerificationReport{}, false
	}
	a.recordVerificationReport(report)
	return report, true
}

func (a *Agent) recordVerificationReport(report VerificationReport) {
	if a == nil || a.Session == nil {
		return
	}
	a.Session.LastVerification = &report
	if a.Store != nil {
		_ = a.Store.Save(a.Session)
	}
	if a.VerifyHistory != nil {
		_ = a.VerifyHistory.Append(a.Session.ID, a.Workspace.Root, report)
	}
}

func (a *Agent) confirmAutomaticVerification(plan VerificationPlan) (bool, error) {
	if a.PromptConfirmAutoVerify == nil {
		return true, nil
	}
	return a.PromptConfirmAutoVerify(plan)
}

func runRecommendedVerification(ctx context.Context, ws Workspace, sess *Session, history *VerificationHistoryStore, trigger string, changed []string, confirm func(VerificationPlan) (bool, error)) (VerificationReport, bool) {
	if len(changed) == 0 {
		changed = collectVerificationChangedPaths(ws.Root, sess)
	}
	tuning := VerificationTuning{}
	if history != nil {
		if loaded, err := history.PlannerTuning(ws.Root); err == nil {
			tuning = loaded
		}
	}
	plan := buildVerificationPlanWithTuning(ws.Root, changed, VerificationAdaptive, tuning)
	if len(plan.Steps) == 0 {
		return VerificationReport{}, false
	}
	if verdict, err := ws.Hook(ctx, HookPreVerification, HookPayload{
		"trigger":       trigger,
		"mode":          string(plan.Mode),
		"changed_files": append([]string(nil), changed...),
	}); err != nil {
		return VerificationReport{}, false
	} else {
		if len(verdict.VerificationAdds) > 0 {
			for _, step := range verdict.VerificationAdds {
				if !verificationStepExists(plan.Steps, VerificationPolicyStep{
					Label:   step.Label,
					Command: step.Command,
					Stage:   step.Stage,
				}) {
					plan.Steps = append(plan.Steps, step)
				}
			}
			plan.PlannerNote = joinSentence(plan.PlannerNote, fmt.Sprintf("Hook engine added %d verification step(s).", len(verdict.VerificationAdds)))
		}
		if len(verdict.ContextAdds) > 0 {
			plan.PlannerNote = joinSentence(plan.PlannerNote, "Hook review context: "+strings.Join(verdict.ContextAdds, " | "))
		}
	}
	if confirm != nil {
		ok, err := confirm(plan)
		if err != nil {
			return skippedVerificationReportForPlan(ws.Root, trigger, plan, "Verification confirmation failed: "+err.Error()), true
		}
		if !ok {
			return skippedVerificationReportForPlan(ws.Root, trigger, plan, "Verification was declined by the user."), true
		}
	}
	report := executeVerificationSteps(ctx, ws, trigger, plan)
	_, _ = ws.Hook(ctx, HookPostVerification, HookPayload{
		"trigger":       trigger,
		"mode":          string(report.Mode),
		"changed_files": append([]string(nil), changed...),
		"output":        report.SummaryLine(),
		"error":         report.FailureSummary(),
	})
	return report, true
}

func skippedVerificationReportForPlan(root string, trigger string, plan VerificationPlan, decision string) VerificationReport {
	report := VerificationReport{
		GeneratedAt:  time.Now(),
		Trigger:      trigger,
		Mode:         plan.Mode,
		Decision:     joinSentence(plan.PlannerNote, decision),
		Workspace:    root,
		ChangedPaths: append([]string(nil), plan.ChangedPaths...),
		Steps:        append([]VerificationStep(nil), plan.Steps...),
	}
	if len(report.Steps) == 0 {
		report.Steps = []VerificationStep{{
			Label:  "verification",
			Status: VerificationSkipped,
		}}
	}
	for i := range report.Steps {
		report.Steps[i].Status = VerificationSkipped
		if strings.TrimSpace(report.Steps[i].Output) == "" {
			report.Steps[i].Output = firstNonBlankString(decision, "Verification was skipped.")
		}
	}
	if strings.TrimSpace(report.Decision) == "" {
		report.Decision = "Verification was skipped."
	}
	return report
}

func buildVerificationPlan(root string, changed []string, mode VerificationMode) VerificationPlan {
	return buildVerificationPlanWithTuning(root, changed, mode, VerificationTuning{})
}

func buildVerificationPlanWithTuning(root string, changed []string, mode VerificationMode, tuning VerificationTuning) VerificationPlan {
	steps := buildVerificationSteps(root, changed, mode)
	var cadenceNote string
	steps, cadenceNote = applyAdaptiveFullRegressionCadence(steps, mode, tuning)
	securitySteps, securityNote := buildSecurityVerificationSteps(root, changed, mode)
	if len(securitySteps) > 0 {
		steps = append(securitySteps, steps...)
	}
	docSteps, docNote := buildAnalysisDocsVerificationSteps(root, changed)
	if len(docSteps) > 0 {
		steps = append(docSteps, steps...)
	}
	fuzzSteps, fuzzNote := buildFuzzCampaignVerificationSteps(root, changed)
	if len(fuzzSteps) > 0 {
		steps = append(fuzzSteps, steps...)
	}
	adversarialSteps, adversarialNote := buildRecentAdversarialVerificationSteps(root)
	if len(adversarialSteps) > 0 {
		steps = append(adversarialSteps, steps...)
	}
	policy, policyErr := LoadVerificationPolicy(root)
	steps, policyNote := applyVerificationPolicy(root, steps, changed, mode, policy)
	steps, reorderNote := reorderVerificationSteps(steps, changed, tuning)
	note := joinSentence(securityNote, docNote)
	note = joinSentence(note, fuzzNote)
	note = joinSentence(note, adversarialNote)
	note = joinSentence(note, policyNote)
	note = joinSentence(note, cadenceNote)
	note = joinSentence(note, renderSecurityVerificationSummary(changed))
	note = joinSentence(note, reorderNote)
	if policyErr != nil {
		note = joinSentence("Verify policy error: "+policyErr.Error(), note)
	}
	return VerificationPlan{
		Mode:         mode,
		ChangedPaths: append([]string(nil), changed...),
		Steps:        steps,
		PlannerNote:  note,
	}
}

func applyAdaptiveFullRegressionCadence(steps []VerificationStep, mode VerificationMode, tuning VerificationTuning) ([]VerificationStep, string) {
	if mode != VerificationAdaptive || len(steps) == 0 {
		return steps, ""
	}
	if !hasTargetedVerificationStep(steps) {
		return steps, ""
	}
	interval := defaultAdaptiveFullRegressionInterval
	nextCycle := tuning.AdaptiveRuns + 1
	if nextCycle <= 0 {
		nextCycle = 1
	}
	if nextCycle%interval == 0 {
		return steps, fmt.Sprintf("Full regression cadence: running workspace regression checks on adaptive cycle %d/%d.", nextCycle, interval)
	}
	filtered, skipped := omitWorkspaceRegressionSteps(steps)
	if skipped == 0 || len(filtered) == 0 {
		return steps, ""
	}
	return filtered, fmt.Sprintf("Full regression cadence: skipped %d workspace regression check(s) until cycle %d; current adaptive cycle is %d/%d.", skipped, interval, nextCycle%interval, interval)
}

func omitWorkspaceRegressionSteps(steps []VerificationStep) ([]VerificationStep, int) {
	filtered := make([]VerificationStep, 0, len(steps))
	skipped := 0
	for _, step := range steps {
		if verificationStepIsWorkspaceRegression(step) {
			skipped++
			continue
		}
		filtered = append(filtered, step)
	}
	return filtered, skipped
}

func verificationStepIsWorkspaceRegression(step VerificationStep) bool {
	if !strings.EqualFold(strings.TrimSpace(step.Stage), "workspace") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(step.Scope), "workspace") {
		return false
	}
	command := strings.ToLower(strings.TrimSpace(step.Command))
	label := strings.ToLower(strings.TrimSpace(step.Label))
	if command == "" {
		command = label
	}
	switch {
	case command == "go test ./..." || label == "go test ./...":
		return true
	case command == "go vet ./..." || label == "go vet ./...":
		return true
	default:
		return false
	}
}

func buildFuzzCampaignVerificationSteps(root string, changed []string) ([]VerificationStep, string) {
	campaigns := loadWorkspaceFuzzCampaignManifests(root, 8)
	if len(campaigns) == 0 {
		return nil, ""
	}
	steps := []VerificationStep{}
	for _, campaign := range campaigns {
		for _, result := range campaign.NativeResults {
			if !fuzzCampaignNativeResultNeedsVerification(result) {
				continue
			}
			if !fuzzCampaignNativeResultMatchesChanged(result, changed) {
				continue
			}
			finding := fuzzCampaignFindingForNativeResult(campaign, result)
			label := "fuzz evidence regression: " + strings.ToLower(firstNonBlankString(result.Target, finding.ID, result.RunID))
			detailParts := []string{}
			detailParts = appendVerificationEvidencePart(detailParts, "campaign", campaign.ID)
			detailParts = appendVerificationEvidencePart(detailParts, "run", result.RunID)
			if strings.TrimSpace(finding.ID) != "" {
				detailParts = appendVerificationEvidencePart(detailParts, "finding", finding.ID)
			}
			detailParts = appendVerificationEvidencePart(detailParts, "outcome", result.Outcome)
			detailParts = appendVerificationEvidencePart(detailParts, "crashes", fmt.Sprintf("%d", result.CrashCount))
			if strings.TrimSpace(result.ReportPath) != "" {
				detailParts = appendVerificationEvidencePart(detailParts, "report", filepath.ToSlash(result.ReportPath))
			}
			if strings.TrimSpace(result.CrashFingerprint) != "" {
				detailParts = appendVerificationEvidencePart(detailParts, "fingerprint", result.CrashFingerprint)
			}
			if strings.TrimSpace(result.CrashDir) != "" {
				detailParts = appendVerificationEvidencePart(detailParts, "crash_dir", filepath.ToSlash(result.CrashDir))
			}
			if strings.TrimSpace(result.MinimizeCommand) != "" {
				detailParts = appendVerificationEvidencePart(detailParts, "minimize", result.MinimizeCommand)
			}
			detail := "Native fuzz evidence requires verification: " + strings.Join(detailParts, " ")
			steps = append(steps, VerificationStep{
				Label:           label,
				Informational:   true,
				Scope:           firstNonBlankString(filepath.ToSlash(result.TargetFile), finding.SourceAnchor, campaign.ID),
				Stage:           "targeted",
				Tags:            []string{"fuzz", "fuzz_native_result", "fuzz_finding", "evidence"},
				Status:          VerificationPending,
				Output:          detail,
				PlannerPriority: 80,
			})
			if len(steps) >= 6 {
				return uniqueVerificationSteps(steps), fmt.Sprintf("Fuzz campaign native results added %d planner step(s).", len(steps))
			}
		}
	}
	if len(steps) == 0 {
		return nil, ""
	}
	return uniqueVerificationSteps(steps), fmt.Sprintf("Fuzz campaign native results added %d planner step(s).", len(steps))
}

func loadWorkspaceFuzzCampaignManifests(root string, limit int) []FuzzCampaign {
	root = normalizePersistentMemoryWorkspace(root)
	if strings.TrimSpace(root) == "" {
		return nil
	}
	base := filepath.Join(root, ".kernforge", "fuzz")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i int, j int) bool {
		return entries[i].Name() > entries[j].Name()
	})
	out := []FuzzCampaign{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(base, entry.Name(), "manifest.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var campaign FuzzCampaign
		if err := json.Unmarshal(data, &campaign); err != nil {
			continue
		}
		campaign = normalizeFuzzCampaign(campaign)
		if len(campaign.NativeResults) == 0 && len(campaign.SeedArtifacts) == 0 && len(campaign.CoverageGaps) == 0 && len(campaign.SeedTargets) == 0 {
			continue
		}
		out = append(out, campaign)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func fuzzCampaignNativeResultNeedsVerification(result FuzzCampaignNativeResult) bool {
	outcome := strings.ToLower(strings.TrimSpace(result.Outcome))
	status := strings.ToLower(strings.TrimSpace(result.Status))
	return result.CrashCount > 0 || len(result.ArtifactIDs) > 0 || outcome == "failed" || status == "failed" || status == "blocked"
}

func fuzzCampaignNativeResultMatchesChanged(result FuzzCampaignNativeResult, changed []string) bool {
	if len(changed) == 0 {
		return true
	}
	candidates := []string{
		result.TargetFile,
		result.ReportPath,
		result.CrashDir,
		result.BuildLogPath,
		result.RunLogPath,
	}
	for _, rawChanged := range changed {
		changedPath := normalizeVerificationComparablePath(rawChanged)
		if changedPath == "" {
			continue
		}
		for _, rawCandidate := range candidates {
			candidate := normalizeVerificationComparablePath(rawCandidate)
			if candidate == "" {
				continue
			}
			if strings.Contains(candidate, changedPath) || strings.Contains(changedPath, candidate) || filepath.Base(candidate) == filepath.Base(changedPath) {
				return true
			}
		}
	}
	return false
}

func normalizeVerificationComparablePath(path string) string {
	path = strings.TrimSpace(filepath.ToSlash(path))
	if path == "" {
		return ""
	}
	path = strings.TrimPrefix(path, "./")
	return strings.ToLower(path)
}

func buildVerificationSteps(root string, changed []string, mode VerificationMode) []VerificationStep {
	if exists(filepath.Join(root, "go.mod")) {
		return buildGoVerificationSteps(root, changed, mode)
	}
	if exists(filepath.Join(root, "Cargo.toml")) {
		return []VerificationStep{
			{
				Label:   "cargo check",
				Command: "cargo check",
				Scope:   "workspace",
				Stage:   "workspace",
				Status:  VerificationPending,
			},
			{
				Label:   "cargo test",
				Command: "cargo test",
				Scope:   "workspace",
				Stage:   "workspace",
				Status:  VerificationPending,
			},
		}
	}
	if scripts := packageScripts(filepath.Join(root, "package.json")); len(scripts) > 0 {
		return buildNodeVerificationSteps(scripts, mode)
	}
	if steps := buildCppVerificationSteps(root, changed, mode); len(steps) > 0 {
		return steps
	}
	return nil
}

func buildAnalysisDocsVerificationSteps(root string, changed []string) ([]VerificationStep, string) {
	manifest, ok := loadLatestAnalysisDocsManifest(root)
	if !ok || len(manifest.VerificationMatrix) == 0 {
		return nil, ""
	}
	matches := analysisVerificationMatrixMatches(manifest, changed)
	if len(matches) == 0 {
		return nil, "Generated verification matrix loaded from latest analyze-project docs."
	}
	steps := make([]VerificationStep, 0, len(matches))
	for _, item := range matches {
		label := "analysis docs verification: " + strings.ToLower(strings.TrimSpace(item.ChangeArea))
		detail := "Generated VERIFICATION_MATRIX.md recommends: " + verificationDisplaySafeText(item.RequiredVerification)
		if strings.TrimSpace(item.OptionalVerification) != "" {
			detail += " Optional: " + verificationDisplaySafeText(item.OptionalVerification) + "."
		}
		scope := "targeted"
		if len(item.SourceAnchors) > 0 {
			scope = strings.Join(limitStrings(item.SourceAnchors, 3), ",")
		}
		steps = append(steps, VerificationStep{
			Label:           label,
			Informational:   true,
			Scope:           scope,
			Stage:           "targeted",
			Tags:            []string{"analysis_docs", "verification_matrix"},
			Status:          VerificationPending,
			Output:          detail,
			PlannerPriority: 35,
		})
	}
	return uniqueVerificationSteps(steps), fmt.Sprintf("Generated verification matrix added %d planner step(s).", len(steps))
}

func loadLatestAnalysisDocsManifest(root string) (AnalysisDocsManifest, bool) {
	cfg := configProjectAnalysis(Config{}, root)
	paths := []string{
		filepath.Join(cfg.OutputDir, "latest", "docs_manifest.json"),
		filepath.Join(cfg.OutputDir, "latest", "docs", "manifest.json"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		manifest, err := decodeAnalysisDocsManifest(data)
		if err != nil {
			continue
		}
		if len(manifest.Documents) > 0 || len(manifest.VerificationMatrix) > 0 || len(manifest.FuzzTargets) > 0 {
			return manifest, true
		}
	}
	return AnalysisDocsManifest{}, false
}

func appendVerificationEvidencePart(parts []string, key string, value string) []string {
	value = verificationDisplaySafeText(value)
	if value == "" {
		return parts
	}
	return append(parts, key+"="+value)
}

func verificationDisplaySafeText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			b.WriteRune(' ')
		case r < 0x20:
			continue
		default:
			b.WriteRune(r)
		}
	}
	value = strings.Join(strings.Fields(b.String()), " ")
	runes := []rune(value)
	if len(runes) > 500 {
		value = string(runes[:500]) + "..."
	}
	return strings.TrimSpace(value)
}

func analysisVerificationMatrixMatches(manifest AnalysisDocsManifest, changed []string) []AnalysisVerificationMatrixEntry {
	out := []AnalysisVerificationMatrixEntry{}
	seen := map[string]struct{}{}
	for _, item := range manifest.VerificationMatrix {
		if !analysisVerificationMatrixEntryMatches(item, changed) {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item.ChangeArea) + "|" + strings.TrimSpace(item.RequiredVerification))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i int, j int) bool {
		return out[i].ChangeArea < out[j].ChangeArea
	})
	return out
}

func analysisVerificationMatrixEntryMatches(item AnalysisVerificationMatrixEntry, changed []string) bool {
	area := strings.ToLower(strings.TrimSpace(item.ChangeArea))
	if area == "" {
		return false
	}
	if len(changed) == 0 {
		return area == "general source change"
	}
	for _, raw := range changed {
		path := strings.ToLower(filepath.ToSlash(strings.TrimSpace(raw)))
		switch {
		case area == "general source change":
			return true
		case strings.Contains(area, "security") && containsAny(path, "security", "guard", "driver", "ioctl", "rpc", "telemetry", "memory", "scan", "anti"):
			return true
		case strings.Contains(area, "driver") && containsAny(path, "driver", ".sys", "ioctl", "irp", "device"):
			return true
		case strings.Contains(area, "ioctl") && containsAny(path, "ioctl", "irp", "device"):
			return true
		case strings.Contains(area, "unreal") && containsAny(path, ".uproject", ".uplugin", ".build.cs", "source/", "rpc", "replication"):
			return true
		case strings.Contains(area, "build") && containsAny(path, "cmakelists", ".vcxproj", ".sln", "compile_commands", ".build.cs"):
			return true
		}
	}
	return false
}

func buildCppVerificationSteps(root string, changed []string, mode VerificationMode) []VerificationStep {
	if projects := detectChangedVCXProjFiles(root, changed); len(projects) > 0 {
		steps := make([]VerificationStep, 0, len(projects)+1)
		for _, project := range projects {
			command, label := msbuildProjectVerificationCommand(root, project)
			steps = append(steps, VerificationStep{
				Label:           label,
				Command:         command,
				Scope:           project,
				Stage:           "targeted",
				Tags:            []string{"cpp", "msbuild", "project"},
				Status:          VerificationPending,
				PlannerPriority: 70,
			})
		}
		if mode != VerificationFull {
			return uniqueVerificationSteps(steps)
		}
		if solution := detectSolutionFile(root); solution != "" {
			steps = append(steps, VerificationStep{
				Label:   "msbuild " + solution,
				Command: "msbuild " + quoteVerificationCommandArg(solution) + " /m",
				Scope:   "workspace",
				Stage:   "workspace",
				Status:  VerificationPending,
			})
		}
		return uniqueVerificationSteps(steps)
	}
	if buildDir := detectCMakeBuildDir(root); buildDir != "" {
		quotedBuildDir := quoteVerificationCommandArg(buildDir)
		steps := []VerificationStep{{
			Label:   "cmake --build " + buildDir,
			Command: "cmake --build " + quotedBuildDir + " --parallel",
			Scope:   "workspace",
			Stage:   "workspace",
			Status:  VerificationPending,
		}}
		if mode == VerificationFull && hasCTestMetadata(root, buildDir) {
			steps = append(steps, VerificationStep{
				Label:   "ctest --test-dir " + buildDir,
				Command: "ctest --test-dir " + quotedBuildDir + " --output-on-failure",
				Scope:   "workspace",
				Stage:   "workspace",
				Status:  VerificationPending,
			})
		}
		return steps
	}
	if solution := detectSolutionFile(root); solution != "" {
		return []VerificationStep{{
			Label:   "msbuild " + solution,
			Command: "msbuild " + quoteVerificationCommandArg(solution) + " /m",
			Scope:   "workspace",
			Stage:   "workspace",
			Status:  VerificationPending,
		}}
	}
	if project := detectVCXProjFile(root); project != "" {
		command, label := msbuildProjectVerificationCommand(root, project)
		return []VerificationStep{{
			Label:   label,
			Command: command,
			Scope:   "workspace",
			Stage:   "workspace",
			Status:  VerificationPending,
		}}
	}
	return nil
}

type msbuildProjectConfiguration struct {
	Configuration string
	Platform      string
}

func msbuildProjectVerificationCommand(root string, project string) (string, string) {
	command := "msbuild " + quoteVerificationCommandArg(project) + " /m"
	label := "msbuild " + project
	if cfg := selectMSBuildProjectConfiguration(root, project); cfg.Configuration != "" && cfg.Platform != "" {
		command += " /p:Configuration=" + quoteMSBuildPropertyValue(cfg.Configuration)
		command += " /p:Platform=" + quoteMSBuildPropertyValue(cfg.Platform)
		label += " " + cfg.Configuration + "|" + cfg.Platform
	}
	return command, label
}

func selectMSBuildProjectConfiguration(root string, project string) msbuildProjectConfiguration {
	configs := readMSBuildProjectConfigurations(root, project)
	if len(configs) == 0 {
		return msbuildProjectConfiguration{}
	}
	preferred := []msbuildProjectConfiguration{
		{Configuration: "Debug", Platform: "x64"},
		{Configuration: "Release", Platform: "x64"},
		{Configuration: "Debug", Platform: "ARM64"},
		{Configuration: "Release", Platform: "ARM64"},
		{Configuration: "Debug", Platform: "Win32"},
		{Configuration: "Release", Platform: "Win32"},
	}
	for _, want := range preferred {
		for _, cfg := range configs {
			if strings.EqualFold(cfg.Configuration, want.Configuration) && strings.EqualFold(cfg.Platform, want.Platform) {
				return cfg
			}
		}
	}
	for _, cfg := range configs {
		if strings.EqualFold(cfg.Platform, "x64") {
			return cfg
		}
	}
	return configs[0]
}

func readMSBuildProjectConfigurations(root string, project string) []msbuildProjectConfiguration {
	projectPath := project
	if !filepath.IsAbs(projectPath) {
		projectPath = filepath.Join(root, filepath.FromSlash(project))
	}
	file, err := os.Open(projectPath)
	if err != nil {
		return nil
	}
	defer file.Close()

	decoder := xml.NewDecoder(file)
	var configs []msbuildProjectConfiguration
	seen := map[string]bool{}
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "ProjectConfiguration" {
			continue
		}
		var item struct {
			Include       string `xml:"Include,attr"`
			Configuration string `xml:"Configuration"`
			Platform      string `xml:"Platform"`
		}
		if err := decoder.DecodeElement(&item, &start); err != nil {
			continue
		}
		configuration := strings.TrimSpace(item.Configuration)
		platform := strings.TrimSpace(item.Platform)
		if (configuration == "" || platform == "") && strings.Contains(item.Include, "|") {
			parts := strings.SplitN(item.Include, "|", 2)
			if configuration == "" {
				configuration = strings.TrimSpace(parts[0])
			}
			if platform == "" {
				platform = strings.TrimSpace(parts[1])
			}
		}
		if configuration == "" || platform == "" {
			continue
		}
		key := strings.ToLower(configuration + "|" + platform)
		if seen[key] {
			continue
		}
		seen[key] = true
		configs = append(configs, msbuildProjectConfiguration{
			Configuration: configuration,
			Platform:      platform,
		})
	}
	return configs
}

func quoteMSBuildPropertyValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\"'") {
		return quoteVerificationCommandArg(value)
	}
	return value
}

func detectChangedVCXProjFiles(root string, changed []string) []string {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range changed {
		rel := strings.TrimSpace(raw)
		if rel == "" || !isCppLikeSourcePath(rel) {
			continue
		}
		abs := rel
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(rootAbs, rel)
		}
		project := nearestVCXProjForPath(rootAbs, abs)
		if project == "" {
			continue
		}
		key := strings.ToLower(filepath.ToSlash(project))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, project)
		if len(out) >= 3 {
			break
		}
	}
	sort.Strings(out)
	return out
}

func nearestVCXProjForPath(rootAbs string, absPath string) string {
	absPath, err := filepath.Abs(absPath)
	if err != nil {
		return ""
	}
	dir := absPath
	if ext := filepath.Ext(dir); ext != "" {
		dir = filepath.Dir(dir)
	}
	for {
		rel, err := filepath.Rel(rootAbs, dir)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return ""
		}
		matches, _ := filepath.Glob(filepath.Join(dir, "*.vcxproj"))
		if len(matches) > 0 {
			sort.Strings(matches)
			projectRel, err := filepath.Rel(rootAbs, matches[0])
			if err != nil {
				return filepath.ToSlash(matches[0])
			}
			return filepath.ToSlash(projectRel)
		}
		if samePath(dir, rootAbs) {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isCppLikeSourcePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	switch ext {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".inl", ".ixx":
		return true
	default:
		return false
	}
}

func buildGoVerificationSteps(root string, changed []string, mode VerificationMode) []VerificationStep {
	packages := deriveGoVerificationPackages(root, changed)
	if mode == VerificationFull {
		packages = []string{"./..."}
	}
	var steps []VerificationStep
	for _, pkg := range packages {
		command := "go test " + pkg
		label := command
		scope := pkg
		stage := "targeted"
		if pkg == "./..." {
			label = "go test ./..."
			scope = "workspace"
			stage = "workspace"
		}
		steps = append(steps, VerificationStep{
			Label:   label,
			Command: command,
			Scope:   scope,
			Stage:   stage,
			Status:  VerificationPending,
		})
	}
	if len(steps) == 0 {
		steps = append(steps, VerificationStep{
			Label:   "go test ./...",
			Command: "go test ./...",
			Scope:   "workspace",
			Stage:   "workspace",
			Status:  VerificationPending,
		})
	}
	steps = append(steps, VerificationStep{
		Label:   "go vet ./...",
		Command: "go vet ./...",
		Scope:   "workspace",
		Stage:   "workspace",
		Status:  VerificationPending,
	})
	return steps
}

func buildNodeVerificationSteps(scripts map[string]string, mode VerificationMode) []VerificationStep {
	var steps []VerificationStep
	_ = mode
	if hasScript(scripts, "typecheck") {
		steps = append(steps, VerificationStep{
			Label:   "npm run typecheck",
			Command: "npm run typecheck",
			Scope:   "workspace",
			Stage:   "workspace",
			Status:  VerificationPending,
		})
	}
	if hasScript(scripts, "lint") {
		steps = append(steps, VerificationStep{
			Label:   "npm run lint",
			Command: "npm run lint",
			Scope:   "workspace",
			Stage:   "workspace",
			Status:  VerificationPending,
		})
	}
	if hasScript(scripts, "test") {
		steps = append(steps, VerificationStep{
			Label:   "npm test",
			Command: "npm test -- --runInBand",
			Scope:   "workspace",
			Stage:   "workspace",
			Status:  VerificationPending,
		})
	}
	return steps
}

func reorderVerificationSteps(steps []VerificationStep, changed []string, tuning VerificationTuning) ([]VerificationStep, string) {
	if len(steps) <= 1 {
		return steps, ""
	}
	reordered := append([]VerificationStep(nil), steps...)
	original := append([]VerificationStep(nil), reordered...)
	sort.SliceStable(reordered, func(i, j int) bool {
		leftStage := reordered[i].Stage
		rightStage := reordered[j].Stage
		if leftStage != rightStage {
			if leftStage == "targeted" {
				return true
			}
			if rightStage == "targeted" {
				return false
			}
		}
		leftScore := verificationCombinedScore(reordered[i], changed, tuning)
		rightScore := verificationCombinedScore(reordered[j], changed, tuning)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return reordered[i].Label < reordered[j].Label
	})
	orderChanged := false
	for i := range reordered {
		if reordered[i].Label != original[i].Label {
			orderChanged = true
			break
		}
	}
	if !orderChanged {
		return reordered, ""
	}
	var promoted []string
	for i := range reordered {
		if reordered[i].Label != original[i].Label && verificationCombinedScore(reordered[i], changed, tuning) > 0 {
			promoted = append(promoted, reordered[i].Label)
		}
		if len(promoted) >= 2 {
			break
		}
	}
	note := ""
	if len(promoted) > 0 {
		note = "Planner prioritized checks using file-pattern and history signals: " + strings.Join(promoted, ", ")
	}
	return reordered, note
}

func verificationHistoryKey(step VerificationStep) string {
	command := strings.TrimSpace(strings.ToLower(step.Command))
	switch {
	case strings.HasPrefix(command, "go test ./") && step.Stage == "targeted":
		return "go test targeted"
	case command == "go test ./...":
		return "go test workspace"
	case command == "go vet ./...":
		return "go vet workspace"
	case command == "npm run typecheck":
		return "npm run typecheck"
	case command == "npm run lint":
		return "npm run lint"
	case command == "npm test -- --runinband":
		return "npm test"
	case command == "cargo check":
		return "cargo check"
	case command == "cargo test":
		return "cargo test"
	default:
		return command
	}
}

func verificationTuningScore(step VerificationStep, tuning VerificationTuning) int {
	key := verificationHistoryKey(step)
	if key == "" {
		return 0
	}
	runs := tuning.RunCounts[key]
	fails := tuning.FailureCounts[key]
	if runs == 0 {
		return 0
	}
	return fails*100 + runs
}

func verificationCombinedScore(step VerificationStep, changed []string, tuning VerificationTuning) int {
	return verificationTuningScore(step, tuning) + verificationPatternScore(step, changed) + step.PlannerPriority
}

func verificationPatternScore(step VerificationStep, changed []string) int {
	if len(changed) == 0 {
		return 0
	}
	command := strings.ToLower(step.Command + " " + step.Label)
	score := 0
	for _, raw := range changed {
		path := strings.ToLower(strings.TrimSpace(raw))
		if path == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(path))
		isGo := strings.HasSuffix(path, ".go")
		isGoTest := strings.HasSuffix(path, "_test.go") || strings.Contains(path, "/testdata/")
		isTs := strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")
		isJs := strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".jsx")
		isNodeTest := strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.Contains(path, "/__tests__/")
		isLintConfig := strings.Contains(base, "eslint") || base == ".eslintrc" || strings.HasPrefix(base, ".eslintrc.")
		isRust := strings.HasSuffix(path, ".rs")
		isRustTest := strings.Contains(path, "/tests/") || strings.HasSuffix(base, "_test.rs")

		switch {
		case strings.Contains(command, "go test"):
			if isGoTest {
				score += 45
			} else if isGo {
				score += 15
			}
		case strings.Contains(command, "go vet"):
			if isGo && !isGoTest {
				score += 25
			}
		case strings.Contains(command, "typecheck"):
			if isTs {
				score += 45
			} else if isJs {
				score += 10
			}
		case strings.Contains(command, "lint"):
			if isLintConfig {
				score += 60
			} else if isTs || isJs {
				score += 30
			}
		case strings.Contains(command, "npm test"):
			if isNodeTest {
				score += 55
			} else if isTs || isJs {
				score += 15
			}
		case strings.Contains(command, "cargo check"):
			if isRust && !isRustTest {
				score += 25
			}
		case strings.Contains(command, "cargo test"):
			if isRustTest {
				score += 50
			} else if isRust {
				score += 15
			}
		}
	}
	return score
}

func deriveGoVerificationPackages(root string, changed []string) []string {
	if len(changed) == 0 {
		return []string{"./..."}
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return []string{"./..."}
	}
	pkgs := map[string]bool{}
	for _, raw := range changed {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(rootAbs, path)
		}
		abs, err = filepath.Abs(abs)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(rel))
		if ext != ".go" {
			continue
		}
		dir := filepath.Dir(rel)
		pkg := "."
		if dir != "." {
			pkg = "./" + filepath.ToSlash(dir) + "/..."
		}
		pkgs[pkg] = true
	}
	var ordered []string
	for pkg := range pkgs {
		if pkg != "./..." {
			ordered = append(ordered, pkg)
		}
	}
	sort.Strings(ordered)
	if len(ordered) > 3 {
		ordered = ordered[:3]
	}
	ordered = append(ordered, "./...")
	return uniqueStrings(ordered)
}

func collectVerificationChangedPaths(root string, sess *Session) []string {
	paths := map[string]bool{}
	for _, path := range collectSessionChangedPaths(sess) {
		paths[path] = true
	}
	for _, path := range collectGitChangedPaths(root) {
		paths[path] = true
	}
	var out []string
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func collectAutomaticVerificationChangedPaths(cfg Config, root string, sess *Session) []string {
	patchChanged := currentTurnPatchTransactionChangedPaths(sess)
	if len(patchChanged) > 0 {
		return filterCodeLikePaths(patchChanged)
	}
	sessionChanged := collectRecentSessionChangedPaths(sess)
	if len(sessionChanged) > 0 {
		return filterCodeLikePaths(sessionChanged)
	}
	return filterCodeLikePaths(collectGitChangedPaths(root))
}

func filterCodeLikePaths(paths []string) []string {
	var out []string
	for _, path := range paths {
		if isCodeLikePath(path) {
			out = append(out, path)
		}
	}
	return uniqueStrings(out)
}

func isCodeLikePath(path string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(path))
	if trimmed == "" {
		return false
	}
	switch filepath.Base(trimmed) {
	case "go.mod", "go.sum", "cargo.toml", "cargo.lock", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "cmakelists.txt", "makefile", "gnu makefile", "makefile.win":
		return true
	}
	switch filepath.Ext(trimmed) {
	case ".go", ".rs", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ixx", ".cppm",
		".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs",
		".py", ".rb", ".java", ".kt", ".kts", ".swift", ".cs", ".php", ".lua",
		".sh", ".bash", ".ps1", ".bat", ".cmd",
		".vcxproj", ".vcproj", ".sln", ".props", ".targets",
		".cmake":
		return true
	}
	return false
}

func collectSessionChangedPaths(sess *Session) []string {
	return collectSessionChangedPathsInRange(sess, 0)
}

func collectRecentSessionChangedPaths(sess *Session) []string {
	if sess == nil {
		return nil
	}
	start := 0
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == "user" {
			start = i + 1
			break
		}
	}
	return collectSessionChangedPathsInRange(sess, start)
}

func collectSessionChangedPathsInRange(sess *Session, start int) []string {
	if sess == nil {
		return nil
	}
	if start < 0 {
		start = 0
	}
	if start > len(sess.Messages) {
		return nil
	}
	var out []string
	for _, msg := range sess.Messages[start:] {
		if msg.Role != "tool" {
			continue
		}
		if !messageRepresentsWorkspaceEdit(msg) {
			continue
		}
		if metaPaths := toolMetaStringSlice(msg.ToolMeta, "changed_paths"); len(metaPaths) > 0 {
			out = append(out, metaPaths...)
			continue
		}
		switch msg.ToolName {
		case "write_file":
			out = append(out, collectVerificationPathsFromToolText(msg.Text, "to ")...)
		case "replace_in_file":
			out = append(out, collectVerificationPathsFromToolText(msg.Text, "updated ")...)
		case "apply_patch":
			out = append(out, collectVerificationPatchPaths(msg.Text)...)
		}
	}
	return uniqueStrings(out)
}

func messageRepresentsWorkspaceEdit(msg Message) bool {
	if isEditTool(msg.ToolName) {
		return true
	}
	if toolMetaBool(msg.ToolMeta, "changed_workspace") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(toolMetaString(msg.ToolMeta, "effect")), "edit")
}

func collectVerificationPathsFromToolText(text, marker string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		idx := strings.Index(strings.ToLower(trimmed), strings.ToLower(marker))
		if idx < 0 {
			continue
		}
		value := strings.TrimSpace(trimmed[idx+len(marker):])
		if value == "" {
			continue
		}
		if cut := strings.Index(value, " "); cut >= 0 {
			value = strings.TrimSpace(value[:cut])
		}
		out = append(out, strings.Trim(value, `"'`))
	}
	return out
}

func collectVerificationPatchPaths(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, prefix := range []string{"added ", "deleted ", "updated "} {
			if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
				value := strings.TrimSpace(trimmed[len(prefix):])
				if idx := strings.Index(value, " -> "); idx >= 0 {
					value = value[:idx]
				}
				out = append(out, strings.Trim(value, `"'`))
			}
		}
	}
	return out
}

func collectGitChangedPaths(root string) []string {
	if !exists(filepath.Join(root, ".git")) {
		return nil
	}
	var out []string
	for _, args := range [][]string{
		{"-c", "core.quotePath=false", "status", "--short"},
	} {
		cmd := newGitHelperCommand(context.Background(), root, args...)
		data, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(string(data), "\r\n"), "\n") {
			path, ok := parseGitStatusShortPath(line)
			if !ok {
				continue
			}
			out = append(out, path)
		}
	}
	return uniqueStrings(out)
}

func parseGitStatusShortPath(line string) (string, bool) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" || len(line) < 4 {
		return "", false
	}
	path := strings.TrimSpace(line[3:])
	if idx := strings.Index(path, " -> "); idx >= 0 {
		path = strings.TrimSpace(path[idx+4:])
	}
	if path == "" {
		return "", false
	}
	return filepath.ToSlash(path), true
}

func executeVerificationSteps(ctx context.Context, ws Workspace, trigger string, plan VerificationPlan) VerificationReport {
	report := VerificationReport{
		GeneratedAt:  time.Now(),
		Trigger:      trigger,
		Mode:         plan.Mode,
		Decision:     plan.PlannerNote,
		Workspace:    ws.Root,
		ChangedPaths: append([]string(nil), plan.ChangedPaths...),
		Steps:        append([]VerificationStep(nil), plan.Steps...),
	}
	for i := range report.Steps {
		step := &report.Steps[i]
		if step.Informational {
			step.Status = VerificationSkipped
			if strings.TrimSpace(step.Output) == "" {
				step.Output = "Informational verification evidence only; no command was executed."
			}
			continue
		}
		resolvedCommand := resolveVerificationCommandPath(ws, step.Command)
		if strings.TrimSpace(resolvedCommand) != "" && strings.TrimSpace(resolvedCommand) != strings.TrimSpace(step.Command) {
			step.ResolvedCommand = resolvedCommand
		}
		if err := ws.EnsureShell(resolvedCommand); err != nil {
			step.Status = VerificationSkipped
			step.Output = "Permission denied: " + err.Error()
			continue
		}
		start := time.Now()
		runCtx, cancel := context.WithTimeout(ctx, ws.defaultShellTimeout())
		name, args := shellInvocation(ws.Shell, resolvedCommand)
		cmd := exec.CommandContext(runCtx, name, args...)
		cmd.Dir = ws.Root
		out, err := cmd.CombinedOutput()
		cancel()
		step.DurationMs = time.Since(start).Milliseconds()
		text := strings.TrimSpace(string(out))
		if len(text) > 5000 {
			text = text[:5000] + "\n... (truncated)"
		}
		step.Output = text
		if runCtx.Err() == context.DeadlineExceeded {
			step.Status = VerificationFailed
			if step.Output == "" {
				step.Output = "command timed out"
			}
		} else if err != nil {
			step.Status = VerificationFailed
			if step.Output == "" {
				step.Output = err.Error()
			}
		} else {
			step.Status = VerificationPassed
			if step.Output == "" {
				step.Output = "(no output)"
			}
		}
		if step.Status == VerificationFailed {
			step.FailureKind, step.Hint = classifyVerificationFailure(*step)
		}
		if step.Status == VerificationFailed {
			if step.StopOnFailure {
				for j := i + 1; j < len(report.Steps); j++ {
					report.Steps[j].Status = VerificationSkipped
					report.Steps[j].Output = "Skipped because policy required stopping after this failure."
				}
				report.Decision = joinSentence(report.Decision, fmt.Sprintf("Verification stopped after %s failed because policy marked it as stop_on_failure.", step.Label))
				break
			}
			if step.ContinueOnFailure {
				report.Decision = joinSentence(report.Decision, fmt.Sprintf("Verification continued after %s failed because policy marked it as continue_on_failure.", step.Label))
			} else {
				for j := i + 1; j < len(report.Steps); j++ {
					report.Steps[j].Status = VerificationSkipped
					report.Steps[j].Output = "Skipped because a previous verification step failed."
				}
				if step.Stage == "targeted" {
					report.Decision = joinSentence(report.Decision, "Adaptive verification stopped after a targeted failure to keep feedback local.")
				} else {
					report.Decision = joinSentence(report.Decision, "Verification stopped after the first failing workspace-level step.")
				}
				break
			}
		}
	}
	for i := range report.Steps {
		if report.Steps[i].Status == "" {
			report.Steps[i].Status = VerificationSkipped
			if report.Steps[i].Output == "" {
				report.Steps[i].Output = "Skipped."
			}
		}
	}
	if strings.TrimSpace(report.Decision) == "" {
		if report.Mode == VerificationAdaptive && hasTargetedVerificationStep(report.Steps) {
			report.Decision = adaptiveVerificationSuccessDecision(report.Steps)
		} else {
			report.Decision = "Verification completed all planned steps."
		}
	} else if !report.HasFailures() && report.HasPassedStep() {
		if report.Mode == VerificationAdaptive && hasTargetedVerificationStep(report.Steps) {
			report.Decision = joinSentence(report.Decision, adaptiveVerificationSuccessDecision(report.Steps))
		} else {
			report.Decision = joinSentence(report.Decision, "Verification completed all planned steps.")
		}
	}
	return report
}

func adaptiveVerificationSuccessDecision(steps []VerificationStep) string {
	if hasWorkspaceVerificationStep(steps) {
		return "Adaptive verification expanded from targeted checks to workspace-level checks after targeted checks passed."
	}
	return "Adaptive verification completed targeted checks; workspace regression remains on the scheduled full cadence."
}

func hasTargetedVerificationStep(steps []VerificationStep) bool {
	for _, step := range steps {
		if step.Stage == "targeted" {
			return true
		}
	}
	return false
}

func hasWorkspaceVerificationStep(steps []VerificationStep) bool {
	for _, step := range steps {
		if step.Stage == "workspace" {
			return true
		}
	}
	return false
}

func joinSentence(prefix, suffix string) string {
	prefix = strings.TrimSpace(prefix)
	suffix = strings.TrimSpace(suffix)
	switch {
	case prefix == "":
		return suffix
	case suffix == "":
		return prefix
	default:
		return prefix + " " + suffix
	}
}

func (r VerificationReport) SummaryLine() string {
	passed := 0
	failed := 0
	skipped := 0
	for _, step := range r.Steps {
		switch step.Status {
		case VerificationPassed:
			passed++
		case VerificationFailed:
			failed++
		case VerificationSkipped:
			skipped++
		}
	}
	return fmt.Sprintf("Verification: passed=%d failed=%d skipped=%d", passed, failed, skipped)
}

func (r VerificationReport) HasFailures() bool {
	for _, step := range r.Steps {
		if step.Status == VerificationFailed {
			return true
		}
	}
	return false
}

func (r VerificationReport) HasPassedStep() bool {
	for _, step := range r.Steps {
		if step.Status == VerificationPassed {
			return true
		}
	}
	return false
}

func (r VerificationReport) WasSkipped() bool {
	if len(r.Steps) == 0 {
		return false
	}
	for _, step := range r.Steps {
		if step.Status != VerificationSkipped {
			return false
		}
	}
	return true
}

func (r VerificationReport) FirstFailure() *VerificationStep {
	for i := range r.Steps {
		if r.Steps[i].Status == VerificationFailed {
			return &r.Steps[i]
		}
	}
	return nil
}

func (r VerificationReport) FailureSummary() string {
	var lines []string
	for _, step := range r.Steps {
		if step.Status != VerificationFailed {
			continue
		}
		line := fmt.Sprintf("- %s", step.Label)
		if strings.TrimSpace(step.ResolvedCommand) != "" && strings.TrimSpace(step.ResolvedCommand) != strings.TrimSpace(step.Command) {
			line += " (cmd: " + step.ResolvedCommand + ")"
		}
		if strings.TrimSpace(step.FailureKind) != "" {
			line += " [" + step.FailureKind + "]"
		}
		if strings.TrimSpace(step.Hint) != "" {
			line += ": " + step.Hint
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (r VerificationReport) SecurityCategories() []string {
	var out []string
	text := strings.TrimSpace(r.Decision)
	for _, token := range strings.Fields(text) {
		lower := strings.ToLower(strings.TrimSpace(token))
		if !strings.HasPrefix(lower, "security_categories=") {
			continue
		}
		value := strings.TrimPrefix(lower, "security_categories=")
		for _, item := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return uniqueStrings(out)
}

func (r VerificationReport) VerificationTags() []string {
	var out []string
	for _, step := range r.Steps {
		out = append(out, step.Tags...)
	}
	return uniqueStrings(out)
}

func (r VerificationReport) VerificationArtifacts() []string {
	var out []string
	for _, step := range r.Steps {
		scope := strings.TrimSpace(step.Scope)
		if scope == "" || strings.EqualFold(scope, "workspace") || strings.EqualFold(scope, "targeted") {
			continue
		}
		if strings.Contains(scope, ",") {
			for _, item := range strings.Split(scope, ",") {
				if trimmed := strings.TrimSpace(item); trimmed != "" {
					out = append(out, trimmed)
				}
			}
			continue
		}
		out = append(out, scope)
	}
	return uniqueStrings(out)
}

func (r VerificationReport) FailureKinds() []string {
	var out []string
	for _, step := range r.Steps {
		if step.Status != VerificationFailed {
			continue
		}
		if trimmed := strings.TrimSpace(step.FailureKind); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return uniqueStrings(out)
}

func (r VerificationReport) HasCommandMissingFailure() bool {
	for _, step := range r.Steps {
		if step.Status != VerificationFailed {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(step.FailureKind), "command_not_found") {
			return true
		}
	}
	return false
}

func (r VerificationReport) MissingCommandTool() string {
	for _, step := range r.Steps {
		if step.Status != VerificationFailed {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(step.FailureKind), "command_not_found") {
			continue
		}
		command := strings.ToLower(strings.TrimSpace(step.Command))
		switch {
		case strings.HasPrefix(command, "msbuild "):
			return "msbuild"
		case command == "msbuild":
			return "msbuild"
		case strings.HasPrefix(command, "cmake "):
			return "cmake"
		case command == "cmake":
			return "cmake"
		case strings.HasPrefix(command, "ctest "):
			return "ctest"
		case command == "ctest":
			return "ctest"
		case strings.HasPrefix(command, "ninja "):
			return "ninja"
		case command == "ninja":
			return "ninja"
		}
	}
	return ""
}

func (r VerificationReport) RepairGuidance() string {
	step := r.FirstFailure()
	if step == nil {
		return ""
	}
	return repairStrategyForFailure(*step)
}

func (r VerificationReport) RenderDetailed() string {
	var parts []string
	parts = append(parts, r.SummaryLine())
	if len(r.ChangedPaths) > 0 {
		parts = append(parts, "Changed paths: "+strings.Join(r.ChangedPaths, ", "))
	}
	if strings.TrimSpace(string(r.Mode)) != "" {
		parts = append(parts, "Verification mode: "+string(r.Mode))
	}
	if strings.TrimSpace(r.Decision) != "" {
		parts = append(parts, "Verification decision:\n"+r.Decision)
	}
	if guidance := strings.TrimSpace(r.RepairGuidance()); guidance != "" {
		parts = append(parts, "Suggested repair strategy:\n"+guidance)
	}
	for _, step := range r.Steps {
		header := fmt.Sprintf("[%s] %s", step.Status, step.Label)
		if step.Status == VerificationFailed && strings.TrimSpace(step.FailureKind) != "" {
			header += " [" + step.FailureKind + "]"
		}
		var bodyLines []string
		if strings.TrimSpace(step.Command) != "" {
			bodyLines = append(bodyLines, "Command: "+step.Command)
		}
		if strings.TrimSpace(step.ResolvedCommand) != "" && strings.TrimSpace(step.ResolvedCommand) != strings.TrimSpace(step.Command) {
			bodyLines = append(bodyLines, "Resolved command: "+step.ResolvedCommand)
		}
		body := step.Output
		if strings.TrimSpace(body) == "" {
			body = "(no output)"
		}
		if step.Status == VerificationFailed && strings.TrimSpace(step.Hint) != "" {
			body = "Hint: " + step.Hint + "\n\n" + body
		}
		bodyLines = append(bodyLines, body)
		body = strings.Join(bodyLines, "\n")
		parts = append(parts, header+"\n"+body)
	}
	return strings.Join(parts, "\n\n")
}

func (r VerificationReport) RenderTerminal(ui UI) string {
	var lines []string
	lines = append(lines, ui.subsection("Summary"))
	lines = append(lines, ui.statusKV("result", r.SummaryLine()))
	if strings.TrimSpace(string(r.Mode)) != "" {
		lines = append(lines, ui.statusKV("mode", string(r.Mode)))
	}
	if strings.TrimSpace(r.Trigger) != "" {
		lines = append(lines, ui.statusKV("trigger", r.Trigger))
	}
	if len(r.ChangedPaths) > 0 {
		lines = append(lines, ui.statusKV("changed_paths", strings.Join(r.ChangedPaths, ", ")))
	}

	if strings.TrimSpace(r.Decision) != "" {
		lines = append(lines, "")
		lines = append(lines, ui.subsection("Decision"))
		lines = append(lines, indentBlock(truncateVerificationBlock(r.Decision, 10), "  "))
	}
	if guidance := strings.TrimSpace(r.RepairGuidance()); guidance != "" {
		lines = append(lines, "")
		lines = append(lines, ui.subsection("Repair"))
		lines = append(lines, indentBlock(truncateVerificationBlock(guidance, 10), "  "))
	}

	lines = append(lines, "")
	lines = append(lines, ui.subsection("Steps"))
	for i, step := range r.Steps {
		lines = append(lines, ui.verificationStepLine(i, step))

		var meta []string
		if strings.TrimSpace(step.Stage) != "" {
			meta = append(meta, "stage="+strings.TrimSpace(step.Stage))
		}
		if strings.TrimSpace(step.Scope) != "" {
			meta = append(meta, "scope="+strings.TrimSpace(step.Scope))
		}
		if step.DurationMs > 0 {
			meta = append(meta, fmt.Sprintf("duration=%dms", step.DurationMs))
		}
		if len(meta) > 0 {
			lines = append(lines, ui.progressLine(ui.dim(strings.Join(meta, "  "))))
		}
		if strings.TrimSpace(step.Command) != "" {
			lines = append(lines, ui.progressLine(ui.dim("cmd: "+step.Command)))
		}
		if strings.TrimSpace(step.ResolvedCommand) != "" && strings.TrimSpace(step.ResolvedCommand) != strings.TrimSpace(step.Command) {
			lines = append(lines, ui.progressLine(ui.dim("resolved cmd: "+step.ResolvedCommand)))
		}
		if step.Status == VerificationFailed && strings.TrimSpace(step.Hint) != "" {
			lines = append(lines, ui.progressLine(ui.warn("hint: "+strings.TrimSpace(step.Hint))))
		}

		output := strings.TrimSpace(step.Output)
		if output == "" {
			continue
		}
		maxLines := 4
		if step.Status == VerificationFailed {
			maxLines = 14
		}
		lines = append(lines, indentBlock(truncateVerificationBlock(output, maxLines), "    "))
	}
	return strings.Join(lines, "\n")
}

func (r VerificationReport) RenderShort() string {
	var lines []string
	lines = append(lines, r.SummaryLine())
	for _, step := range r.Steps {
		line := fmt.Sprintf("[%s] %s", step.Status, step.Label)
		if strings.TrimSpace(step.ResolvedCommand) != "" && strings.TrimSpace(step.ResolvedCommand) != strings.TrimSpace(step.Command) {
			line += " (cmd: " + step.ResolvedCommand + ")"
		}
		if step.Status == VerificationFailed && strings.TrimSpace(step.FailureKind) != "" {
			line += " [" + step.FailureKind + "]"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func truncateVerificationBlock(text string, maxLines int) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(strings.TrimSpace(normalized), "\n")
	if maxLines <= 0 || len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	trimmed := append([]string(nil), lines[:maxLines]...)
	trimmed = append(trimmed, fmt.Sprintf("... (%d more line(s))", len(lines)-maxLines))
	return strings.Join(trimmed, "\n")
}

func indentBlock(text string, prefix string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func classifyVerificationFailure(step VerificationStep) (string, string) {
	output := strings.ToLower(strings.TrimSpace(step.Output))
	command := strings.ToLower(step.Command + " " + step.Label + " " + step.ResolvedCommand)
	switch {
	case strings.Contains(output, "timed out"):
		return "timeout", "The verification command timed out. Reduce the scope, fix hanging behavior, or rerun after addressing long-running work."
	case missingVerificationCommandName(step) != "":
		return "command_not_found", "A required verification tool could not be started. Check PATH, install the missing build/test tool, or disable automatic verification for this workspace."
	case strings.Contains(command, "typecheck"):
		return "typecheck_error", "Fix the reported type errors before retrying verification."
	case strings.Contains(command, "lint") || strings.Contains(command, "vet"):
		return "lint_error", "Address the reported lint or static analysis issues, then rerun verification."
	case strings.Contains(command, "go test"):
		if looksLikeGoCompileFailure(output) {
			return "compile_error", "The code does not compile. Fix the reported compile errors first."
		}
		return "test_failure", "The tests are failing. Fix the behavior or adjust the affected code and tests."
	case strings.Contains(command, "cargo check"):
		return "compile_error", "Rust compilation failed. Fix the reported compiler errors first."
	case strings.Contains(command, "cargo test"):
		if strings.Contains(output, "error[") || strings.Contains(output, "could not compile") {
			return "compile_error", "Rust compilation failed before tests could run. Fix the compiler errors first."
		}
		return "test_failure", "The Rust tests are failing. Fix the failing behavior or tests."
	case strings.Contains(command, "ctest"):
		return "test_failure", "The C++ test suite is failing. Fix the reported failures before finishing."
	case strings.Contains(command, "cmake --build"), strings.Contains(command, "msbuild"), strings.Contains(command, "ninja"):
		return "compile_error", "The C++ build failed. Fix the first compiler or linker error before retrying verification."
	case strings.Contains(command, "npm test"), strings.Contains(command, "pnpm test"), strings.Contains(command, "yarn test"):
		return "test_failure", "The test suite is failing. Fix the reported test failures before finishing."
	default:
		if looksLikeGoCompileFailure(output) {
			return "compile_error", "The code does not compile. Fix the reported compile errors first."
		}
		return "verification_failure", "Review the failing verification output and address the first concrete error."
	}
}

func repairStrategyForFailure(step VerificationStep) string {
	switch step.FailureKind {
	case "command_not_found":
		return "The verification command could not start because a required tool was not found. Fix PATH, install the missing toolchain component, open the workspace in a developer shell, or disable automatic verification if the environment is intentionally incomplete."
	case "compile_error":
		return "Fix the compiler or build errors before anything else. Start with the first reported error, keep the change set minimal, and rerun verification after the code builds again."
	case "typecheck_error":
		return "Fix the reported type errors first. Avoid broader refactors until the typechecker passes, then rerun verification."
	case "lint_error":
		return "Address the lint or static-analysis issues with the smallest safe edits. Preserve runtime behavior while satisfying the reported rule violations."
	case "test_failure":
		return "The code appears to build, but behavior is still wrong. Read the failing test output carefully, identify the broken expectation, and fix the implementation or test setup with the narrowest possible change."
	case "timeout":
		return "The verification timed out. Look for hangs, infinite loops, very slow setup, or an over-broad verification scope. Prefer a targeted fix and rerun a narrower check first."
	default:
		return "Start from the first concrete verification error and fix the smallest blocking issue before rerunning verification."
	}
}

func looksLikeGoCompileFailure(output string) bool {
	for _, needle := range []string{
		"build failed",
		"undefined:",
		"cannot use ",
		"not enough arguments in call",
		"too many arguments in call",
		"syntax error:",
		"declared and not used",
		"missing return",
		"no required module provides package",
		"cannot find package",
		"imported and not used",
		"assignment mismatch:",
		"cannot refer to unexported",
		"expected ",
	} {
		if strings.Contains(output, needle) {
			return true
		}
	}
	return false
}

func missingVerificationCommandName(step VerificationStep) string {
	output := strings.ToLower(strings.TrimSpace(step.Output))
	if output == "" {
		return ""
	}
	primary := verificationPrimaryExecutable(step)
	if primary == "" {
		return ""
	}
	if strings.Contains(output, "exec: \"") && strings.Contains(output, "executable file not found") {
		if quoted := firstQuotedValue(output); executableNameMatches(quoted, primary) {
			return primary
		}
		return ""
	}
	if strings.Contains(output, "is not recognized as the name of a cmdlet") ||
		strings.Contains(output, "is not recognized as an internal or external command") ||
		strings.Contains(output, "command not found") ||
		strings.Contains(output, "the system cannot find the file specified") ||
		strings.Contains(output, "createprocess failed") {
		if missingTerm := missingCommandTerm(output); executableNameMatches(missingTerm, primary) {
			return primary
		}
		first := firstOutputToken(output)
		if executableNameMatches(first, primary) {
			return primary
		}
	}
	return ""
}

func verificationPrimaryExecutable(step VerificationStep) string {
	for _, command := range []string{step.ResolvedCommand, step.Command} {
		if token := firstCommandExecutableToken(command); token != "" {
			return token
		}
	}
	return ""
}

func firstCommandExecutableToken(command string) string {
	trimmed := strings.TrimSpace(command)
	for strings.HasPrefix(trimmed, "&") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "&"))
	}
	if trimmed == "" {
		return ""
	}
	if trimmed[0] == '"' || trimmed[0] == '\'' {
		quote := trimmed[0]
		for i := 1; i < len(trimmed); i++ {
			if trimmed[i] == quote {
				return strings.TrimSpace(trimmed[1:i])
			}
		}
		return strings.Trim(trimmed, `"'`)
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], `"'`)
}

func missingCommandTerm(output string) string {
	if quoted := firstQuotedValue(output); quoted != "" {
		return quoted
	}
	if idx := strings.Index(output, ":"); idx > 0 {
		return strings.TrimSpace(output[:idx])
	}
	return ""
}

func firstQuotedValue(text string) string {
	for _, quote := range []byte{'"', '\''} {
		start := strings.IndexByte(text, quote)
		if start < 0 {
			continue
		}
		end := strings.IndexByte(text[start+1:], quote)
		if end < 0 {
			continue
		}
		return strings.TrimSpace(text[start+1 : start+1+end])
	}
	return ""
}

func firstOutputToken(output string) string {
	line := strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n"))
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if idx := strings.Index(line, ":"); idx > 0 {
		line = strings.TrimSpace(line[:idx])
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], `"'`)
}

func executableNameMatches(observed string, expected string) bool {
	observed = normalizeExecutableName(observed)
	expected = normalizeExecutableName(expected)
	if observed == "" || expected == "" {
		return false
	}
	return observed == expected
}

func normalizeExecutableName(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Trim(value, `"'`)))
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "/", `\`)
	if idx := strings.LastIndex(value, `\`); idx >= 0 {
		value = value[idx+1:]
	}
	return strings.TrimSuffix(value, ".exe")
}

func resolveVerificationCommandPath(ws Workspace, command string) string {
	resolved := strings.TrimSpace(command)
	if resolved == "" || len(ws.VerificationToolPaths) == 0 {
		return command
	}
	if path := strings.TrimSpace(ws.VerificationToolPaths["msbuild"]); path != "" {
		resolved = replaceLeadingVerificationTool(resolved, "msbuild", path, ws.Shell)
	}
	if path := strings.TrimSpace(ws.VerificationToolPaths["cmake"]); path != "" {
		resolved = replaceLeadingVerificationTool(resolved, "cmake", path, ws.Shell)
	}
	if path := strings.TrimSpace(ws.VerificationToolPaths["ctest"]); path != "" {
		resolved = replaceLeadingVerificationTool(resolved, "ctest", path, ws.Shell)
	}
	if path := strings.TrimSpace(ws.VerificationToolPaths["ninja"]); path != "" {
		resolved = replaceLeadingVerificationTool(resolved, "ninja", path, ws.Shell)
	}
	return resolved
}

func replaceLeadingVerificationTool(command, toolName, toolPath string, shell string) string {
	trimmed := strings.TrimSpace(command)
	lowerTool := strings.ToLower(strings.TrimSpace(toolName))
	lowerCommand := strings.ToLower(trimmed)
	if lowerTool == "" || strings.TrimSpace(toolPath) == "" {
		return command
	}
	if lowerCommand != lowerTool && !strings.HasPrefix(lowerCommand, lowerTool+" ") {
		return command
	}
	rest := strings.TrimSpace(trimmed[len(toolName):])
	resolved := quoteVerificationCommandArg(strings.TrimSpace(toolPath))
	if verificationShellRequiresCallOperatorForQuotedExe(shell) {
		resolved = "& " + resolved
	}
	if rest == "" {
		return resolved
	}
	return resolved + " " + rest
}

func verificationShellRequiresCallOperatorForQuotedExe(shell string) bool {
	base := strings.ToLower(strings.TrimSpace(shell))
	switch base {
	case "cmd", "cmd.exe", "bash", "sh":
		return false
	}
	if strings.Contains(base, "powershell") || strings.Contains(base, "pwsh") {
		return true
	}
	return runtime.GOOS == "windows"
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasNpmTestScript(path string) bool {
	return hasScript(packageScripts(path), "test")
}

func packageScripts(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var payload struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	return payload.Scripts
}

func hasScript(scripts map[string]string, name string) bool {
	if scripts == nil {
		return false
	}
	_, ok := scripts[name]
	return ok
}

func detectCMakeBuildDir(root string) string {
	if !exists(filepath.Join(root, "CMakeLists.txt")) {
		return ""
	}
	for _, candidate := range []string{"build", ".build", filepath.Join("out", "build")} {
		abs := filepath.Join(root, candidate)
		if exists(filepath.Join(abs, "CMakeCache.txt")) || exists(filepath.Join(abs, "CMakeFiles")) {
			return filepath.ToSlash(candidate)
		}
	}
	return ""
}

func hasCTestMetadata(root, buildDir string) bool {
	return exists(filepath.Join(root, buildDir, "CTestTestfile.cmake")) ||
		exists(filepath.Join(root, buildDir, "DartConfiguration.tcl"))
}

func detectSolutionFile(root string) string {
	files := findWorkspaceFilesWithExt(root, ".sln", 3)
	if len(files) == 0 {
		return ""
	}
	return files[0]
}

func detectVCXProjFile(root string) string {
	files := findWorkspaceFilesWithExt(root, ".vcxproj", 3)
	if len(files) == 0 {
		return ""
	}
	return files[0]
}

func findWorkspaceFilesWithExt(root, ext string, maxDepth int) []string {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	var matches []string
	_ = filepath.WalkDir(rootAbs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			name := strings.ToLower(d.Name())
			if name == ".git" || name == "node_modules" || name == ".build" {
				return filepath.SkipDir
			}
			if depthFromRoot(rootAbs, path) > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ext) {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return nil
		}
		matches = append(matches, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(matches)
	return matches
}

func depthFromRoot(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

func quoteVerificationCommandArg(value string) string {
	escaped := strings.ReplaceAll(value, `"`, `\"`)
	return `"` + escaped + `"`
}

func normalizeVerificationOverridePath(raw string) string {
	value := strings.TrimSpace(raw)
	value = strings.Trim(value, `"'`)
	value = strings.TrimPrefix(value, "@")
	if match := mentionRangePattern.FindStringSubmatch(value); len(match) == 4 {
		return strings.TrimSpace(match[1])
	}
	return value
}

func joinNonEmpty(parts ...string) string {
	var buf bytes.Buffer
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(part)
	}
	return buf.String()
}
