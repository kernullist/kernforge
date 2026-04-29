package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	patchTransactionStatusActive     = "active"
	patchTransactionStatusCommitted  = "committed"
	patchTransactionStatusSuperseded = "superseded"
	finalHarnessMaxFindings          = 8
)

type PatchTransaction struct {
	ID            string                  `json:"id,omitempty"`
	Goal          string                  `json:"goal,omitempty"`
	WorkspaceRoot string                  `json:"workspace_root,omitempty"`
	Status        string                  `json:"status,omitempty"`
	StartedAt     time.Time               `json:"started_at,omitempty"`
	UpdatedAt     time.Time               `json:"updated_at,omitempty"`
	CompletedAt   time.Time               `json:"completed_at,omitempty"`
	Entries       []PatchTransactionEntry `json:"entries,omitempty"`
	Warnings      []string                `json:"warnings,omitempty"`
}

type PatchTransactionEntry struct {
	ID          string            `json:"id,omitempty"`
	ToolName    string            `json:"tool_name,omitempty"`
	OwnerNodeID string            `json:"owner_node_id,omitempty"`
	Command     string            `json:"command,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	Status      string            `json:"status,omitempty"`
	Error       string            `json:"error,omitempty"`
	StartedAt   time.Time         `json:"started_at,omitempty"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
	Paths       []PatchPathChange `json:"paths,omitempty"`
}

type PatchPathChange struct {
	Path      string                 `json:"path,omitempty"`
	Operation string                 `json:"operation,omitempty"`
	Before    HarnessFileFingerprint `json:"before,omitempty"`
	After     HarnessFileFingerprint `json:"after,omitempty"`
}

type HarnessFileFingerprint struct {
	Path       string `json:"path,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Exists     bool   `json:"exists,omitempty"`
	Size       int64  `json:"size,omitempty"`
	ModTime    int64  `json:"mod_time,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	ReadFailed string `json:"read_failed,omitempty"`
}

type PatchTransactionProbe struct {
	TransactionID string
	EntryID       string
	ToolName      string
	OwnerNodeID   string
	Command       string
	Reason        string
	Root          string
	Scopes        []string
	Before        map[string]HarnessFileFingerprint
	StartedAt     time.Time
	SnapshotError string
}

type CodingHarnessReport struct {
	GeneratedAt           time.Time                   `json:"generated_at,omitempty"`
	Approved              bool                        `json:"approved,omitempty"`
	Findings              []CodingHarnessFinding      `json:"findings,omitempty"`
	Acceptance            AcceptanceContractReport    `json:"acceptance,omitempty"`
	ArtifactQuality       ArtifactQualityReport       `json:"artifact_quality,omitempty"`
	ScenarioReplay        ScenarioReplayReport        `json:"scenario_replay,omitempty"`
	SubagentOrchestration SubagentOrchestrationReport `json:"subagent_orchestration,omitempty"`
	TestImpact            TestImpactReport            `json:"test_impact,omitempty"`
	JobSupervisor         JobSupervisorReport         `json:"job_supervisor,omitempty"`
	DiffReview            DiffAwareSelfReviewReport   `json:"diff_review,omitempty"`
	Outcome               OutcomeInvariantReport      `json:"outcome,omitempty"`
}

type CodingHarnessFinding struct {
	Severity string `json:"severity,omitempty"`
	Title    string `json:"title,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type DiffAwareSelfReviewReport struct {
	ChangedPaths []string               `json:"changed_paths,omitempty"`
	Findings     []CodingHarnessFinding `json:"findings,omitempty"`
}

type OutcomeInvariantReport struct {
	Checks   []string               `json:"checks,omitempty"`
	Findings []CodingHarnessFinding `json:"findings,omitempty"`
}

type AcceptanceContract struct {
	ID                   string    `json:"id,omitempty"`
	SourcePrompt         string    `json:"source_prompt,omitempty"`
	Mode                 string    `json:"mode,omitempty"`
	ExpectedBehaviors    []string  `json:"expected_behaviors,omitempty"`
	NonGoals             []string  `json:"non_goals,omitempty"`
	ChangedSurfaces      []string  `json:"changed_surfaces,omitempty"`
	RequiredArtifacts    []string  `json:"required_artifacts,omitempty"`
	VerificationRequired bool      `json:"verification_required,omitempty"`
	VerificationNotes    []string  `json:"verification_notes,omitempty"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

type AcceptanceContractReport struct {
	ContractID string                 `json:"contract_id,omitempty"`
	Checks     []string               `json:"checks,omitempty"`
	Findings   []CodingHarnessFinding `json:"findings,omitempty"`
}

func (s *Session) ensurePatchTransaction(goal string, root string) *PatchTransaction {
	if s == nil {
		return nil
	}
	root = strings.TrimSpace(root)
	if root == "" {
		root = strings.TrimSpace(s.WorkingDir)
	}
	now := time.Now()
	if s.ActivePatchTransaction != nil {
		s.ActivePatchTransaction.Normalize()
		if strings.EqualFold(s.ActivePatchTransaction.Status, patchTransactionStatusActive) {
			s.ActivePatchTransaction.UpdatedAt = now
			return s.ActivePatchTransaction
		}
		s.archiveActivePatchTransaction()
	}
	id := fmt.Sprintf("patch-tx-%s", now.Format("20060102-150405.000"))
	tx := &PatchTransaction{
		ID:            id,
		Goal:          strings.Join(strings.Fields(strings.TrimSpace(baseUserQueryText(goal))), " "),
		WorkspaceRoot: root,
		Status:        patchTransactionStatusActive,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	s.ActivePatchTransaction = tx
	return tx
}

func (s *Session) archiveActivePatchTransaction() {
	if s == nil || s.ActivePatchTransaction == nil {
		return
	}
	tx := *s.ActivePatchTransaction
	tx.Normalize()
	if strings.TrimSpace(tx.ID) != "" {
		s.PatchTransactions = append([]PatchTransaction{tx}, s.PatchTransactions...)
		if len(s.PatchTransactions) > 6 {
			s.PatchTransactions = s.PatchTransactions[:6]
		}
	}
	s.ActivePatchTransaction = nil
}

func (s *Session) finalizeActivePatchTransaction(status string) {
	if s == nil || s.ActivePatchTransaction == nil {
		return
	}
	now := time.Now()
	tx := s.ActivePatchTransaction
	tx.Normalize()
	tx.Status = strings.TrimSpace(strings.ToLower(status))
	if tx.Status == "" {
		tx.Status = patchTransactionStatusCommitted
	}
	tx.CompletedAt = now
	tx.UpdatedAt = now
	s.archiveActivePatchTransaction()
}

func (s *Session) normalizeCodingHarnessState() {
	if s == nil {
		return
	}
	if s.AcceptanceContract != nil {
		s.AcceptanceContract.Normalize()
		if strings.TrimSpace(s.AcceptanceContract.ID) == "" {
			s.AcceptanceContract = nil
		}
	}
	if s.ActivePatchTransaction != nil {
		s.ActivePatchTransaction.Normalize()
		if strings.TrimSpace(s.ActivePatchTransaction.ID) == "" {
			s.ActivePatchTransaction = nil
		}
	}
	filtered := make([]PatchTransaction, 0, len(s.PatchTransactions))
	for _, tx := range s.PatchTransactions {
		tx.Normalize()
		if strings.TrimSpace(tx.ID) != "" {
			filtered = append(filtered, tx)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})
	if len(filtered) > 6 {
		filtered = filtered[:6]
	}
	s.PatchTransactions = filtered
	if s.LastCodingHarnessReport != nil {
		s.LastCodingHarnessReport.Normalize()
	}
	if s.LastUserChangeIsolationReport != nil {
		s.LastUserChangeIsolationReport.Normalize()
	}
	if s.LastTestImpactReport != nil {
		s.LastTestImpactReport.Normalize()
	}
	if s.LastJobSupervisorReport != nil {
		s.LastJobSupervisorReport.Normalize()
	}
}

func buildAcceptanceContract(userText string, intent TurnIntent, readOnlyAnalysis bool, explicitEditRequest bool, explicitGitRequest bool) AcceptanceContract {
	now := time.Now()
	base := strings.TrimSpace(baseUserQueryText(userText))
	if base == "" {
		base = strings.TrimSpace(userText)
	}
	contract := AcceptanceContract{
		ID:           fmt.Sprintf("accept-%s", now.Format("20060102-150405.000")),
		SourcePrompt: strings.Join(strings.Fields(base), " "),
		Mode:         acceptanceContractMode(intent, readOnlyAnalysis, explicitEditRequest),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if contract.SourcePrompt != "" {
		contract.ExpectedBehaviors = append(contract.ExpectedBehaviors, "Satisfy the latest user request: "+compactPromptSection(contract.SourcePrompt, 220))
	}
	contract.RequiredArtifacts = extractContractArtifactPaths(base)
	contract.ChangedSurfaces = inferContractChangedSurfaces(base, contract.RequiredArtifacts)
	contract.VerificationRequired = promptExplicitlyRequiresVerification(base)
	contract.VerificationNotes = acceptanceVerificationNotes(base, contract.VerificationRequired, explicitEditRequest, intent)
	if readOnlyAnalysis {
		contract.NonGoals = append(contract.NonGoals, "Do not modify workspace files unless the user changes the request.")
	}
	if !explicitGitRequest {
		contract.NonGoals = append(contract.NonGoals, "Do not stage, commit, push, or open a PR.")
	}
	if len(contract.RequiredArtifacts) > 0 {
		contract.ExpectedBehaviors = append(contract.ExpectedBehaviors, "Create or update the requested artifact path(s): "+strings.Join(contract.RequiredArtifacts, ", "))
	}
	if contract.VerificationRequired {
		contract.ExpectedBehaviors = append(contract.ExpectedBehaviors, "Provide recorded verification evidence or explicitly report the verification blocker.")
	}
	contract.Normalize()
	return contract
}

func acceptanceContractMode(intent TurnIntent, readOnlyAnalysis bool, explicitEditRequest bool) string {
	if readOnlyAnalysis {
		return "analysis_only"
	}
	if explicitEditRequest || intent == TurnIntentEditCode {
		return "inspect_and_fix"
	}
	switch intent {
	case TurnIntentRunCommand:
		return "command"
	case TurnIntentPlanOrDesign:
		return "plan_or_design"
	case TurnIntentAskProjectKnowledge:
		return "project_knowledge"
	case TurnIntentDiagnoseRecentError:
		return "diagnose_recent_error"
	case TurnIntentContinueLastTask:
		return "continue"
	default:
		return "general"
	}
}

func extractContractArtifactPaths(text string) []string {
	paths := make([]string, 0)
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if !lineLooksLikeArtifactRequest(lower) {
			continue
		}
		for _, token := range pathTokensFromLine(line) {
			if pathTokenLooksVerifiable(token) {
				paths = append(paths, token)
			}
		}
	}
	return normalizeTaskStateList(paths, 16)
}

func lineLooksLikeArtifactRequest(lower string) bool {
	if lineLooksLikeNegativeArtifactClaim(lower) {
		return false
	}
	if containsAny(lower,
		"create", "generate", "write", "save", "add",
		"만들", "생성", "작성", "저장", "추가",
	) {
		return true
	}
	if containsAny(lower, "update", "updated", "업데이트", "수정") &&
		containsAny(lower, "artifact", "file", "doc", "document", "report", "산출물", "파일", "문서", "리포트", "보고서") {
		return true
	}
	return false
}

func inferContractChangedSurfaces(text string, artifacts []string) []string {
	lower := strings.ToLower(strings.TrimSpace(text))
	surfaces := make([]string, 0)
	for _, path := range artifacts {
		surfaces = append(surfaces, path)
	}
	surfaceHints := map[string][]string{
		"source":  {"code", "source", "function", "bug", "fix", "구현", "수정", "버그", "코드"},
		"tests":   {"test", "tests", "테스트", "검증"},
		"docs":    {"doc", "docs", "document", "report", "문서", "리포트", "보고서"},
		"config":  {"config", "setting", "설정", "프로파일"},
		"command": {"run", "execute", "command", "실행", "커맨드", "명령"},
	}
	for surface, hints := range surfaceHints {
		for _, hint := range hints {
			if strings.Contains(lower, hint) {
				surfaces = append(surfaces, surface)
				break
			}
		}
	}
	return normalizeTaskStateList(surfaces, 16)
}

func promptExplicitlyRequiresVerification(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if containsAny(lower, "when practical", "if practical", "when relevant", "if relevant", "가능하면", "실용적이면") {
		return false
	}
	return containsAny(lower,
		"run tests", "run the tests", "execute tests", "test it", "test the change",
		"verify the", "verify this", "verify it", "verify with", "please verify", "run verification", "perform verification",
		"run build", "build it", "build the project", "compile it",
		"테스트 실행", "테스트까지", "테스트도", "검증해", "검증해줘", "검증까지", "검증도", "검증 실행", "빌드 실행", "빌드까지", "컴파일",
	)
}

func acceptanceVerificationNotes(text string, required bool, explicitEditRequest bool, intent TurnIntent) []string {
	notes := make([]string, 0)
	if required {
		notes = append(notes, "Verification is explicitly requested by the user.")
	} else if explicitEditRequest || intent == TurnIntentEditCode {
		notes = append(notes, "Verification is expected when practical after code edits; otherwise report that it was not run.")
	}
	if strings.TrimSpace(text) != "" && containsAny(strings.ToLower(text), "완벽", "끝까지", "꼼꼼", "production", "prod") {
		notes = append(notes, "The prompt asks for a high-confidence finish; prefer concrete verification evidence.")
	}
	return normalizeTaskStateList(notes, 8)
}

func (c AcceptanceContract) RenderPromptSection() string {
	c.Normalize()
	if strings.TrimSpace(c.ID) == "" {
		return ""
	}
	lines := []string{
		"- Contract: " + c.ID,
	}
	if c.Mode != "" {
		lines = append(lines, "- Mode: "+c.Mode)
	}
	if len(c.ExpectedBehaviors) > 0 {
		lines = append(lines, "- Expected: "+strings.Join(c.ExpectedBehaviors, " | "))
	}
	if len(c.RequiredArtifacts) > 0 {
		lines = append(lines, "- Required artifacts: "+strings.Join(c.RequiredArtifacts, ", "))
	}
	if len(c.ChangedSurfaces) > 0 {
		lines = append(lines, "- Surfaces: "+strings.Join(c.ChangedSurfaces, ", "))
	}
	if c.VerificationRequired {
		lines = append(lines, "- Verification: required")
	} else if len(c.VerificationNotes) > 0 {
		lines = append(lines, "- Verification: expected when practical")
	}
	if len(c.VerificationNotes) > 0 {
		lines = append(lines, "- Verification notes: "+strings.Join(c.VerificationNotes, " | "))
	}
	if len(c.NonGoals) > 0 {
		lines = append(lines, "- Non-goals: "+strings.Join(c.NonGoals, " | "))
	}
	return strings.Join(lines, "\n")
}

func (t *PatchTransaction) Normalize() {
	if t == nil {
		return
	}
	t.ID = strings.TrimSpace(t.ID)
	t.Goal = strings.Join(strings.Fields(strings.TrimSpace(t.Goal)), " ")
	t.WorkspaceRoot = strings.TrimSpace(t.WorkspaceRoot)
	t.Status = strings.TrimSpace(strings.ToLower(t.Status))
	if t.Status == "" {
		t.Status = patchTransactionStatusActive
	}
	t.Warnings = normalizeTaskStateList(t.Warnings, 8)
	for i := range t.Entries {
		t.Entries[i].Normalize()
	}
	filtered := make([]PatchTransactionEntry, 0, len(t.Entries))
	for _, entry := range t.Entries {
		if strings.TrimSpace(entry.ID) != "" || strings.TrimSpace(entry.ToolName) != "" {
			filtered = append(filtered, entry)
		}
	}
	t.Entries = filtered
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.StartedAt
	}
}

func (e *PatchTransactionEntry) Normalize() {
	if e == nil {
		return
	}
	e.ID = strings.TrimSpace(e.ID)
	e.ToolName = strings.TrimSpace(e.ToolName)
	e.OwnerNodeID = strings.TrimSpace(e.OwnerNodeID)
	e.Command = strings.TrimSpace(e.Command)
	e.Reason = strings.TrimSpace(e.Reason)
	e.Status = strings.TrimSpace(strings.ToLower(e.Status))
	e.Error = strings.TrimSpace(e.Error)
	for i := range e.Paths {
		e.Paths[i].Normalize()
	}
	e.Paths = normalizePatchPathChanges(e.Paths)
}

func (c *PatchPathChange) Normalize() {
	if c == nil {
		return
	}
	c.Path = normalizeSessionRelativePath(c.Path)
	c.Operation = strings.TrimSpace(strings.ToLower(c.Operation))
	c.Before.Normalize()
	c.After.Normalize()
}

func (f *HarnessFileFingerprint) Normalize() {
	if f == nil {
		return
	}
	f.Path = normalizeSessionRelativePath(f.Path)
	f.Kind = strings.TrimSpace(strings.ToLower(f.Kind))
	f.SHA256 = strings.TrimSpace(f.SHA256)
	f.ReadFailed = strings.TrimSpace(f.ReadFailed)
}

func (r *CodingHarnessReport) Normalize() {
	if r == nil {
		return
	}
	r.Findings = normalizeCodingHarnessFindings(r.Findings)
	r.Acceptance.Findings = normalizeCodingHarnessFindings(r.Acceptance.Findings)
	r.Acceptance.Checks = normalizeTaskStateList(r.Acceptance.Checks, 16)
	r.ArtifactQuality.Normalize()
	r.ScenarioReplay.Normalize()
	r.SubagentOrchestration.Normalize()
	r.TestImpact.Normalize()
	r.JobSupervisor.Normalize()
	r.DiffReview.Findings = normalizeCodingHarnessFindings(r.DiffReview.Findings)
	r.DiffReview.ChangedPaths = normalizeTaskStateList(r.DiffReview.ChangedPaths, 32)
	r.Outcome.Findings = normalizeCodingHarnessFindings(r.Outcome.Findings)
	r.Outcome.Checks = normalizeTaskStateList(r.Outcome.Checks, 16)
	r.Approved = !codingHarnessFindingsHaveBlockers(r.Findings) &&
		!codingHarnessFindingsHaveBlockers(r.Acceptance.Findings) &&
		!codingHarnessFindingsHaveBlockers(r.ArtifactQuality.Findings) &&
		!codingHarnessFindingsHaveBlockers(r.ScenarioReplay.Findings) &&
		!codingHarnessFindingsHaveBlockers(r.SubagentOrchestration.Findings) &&
		!codingHarnessFindingsHaveBlockers(r.TestImpact.Findings) &&
		!codingHarnessFindingsHaveBlockers(r.JobSupervisor.Findings) &&
		!codingHarnessFindingsHaveBlockers(r.DiffReview.Findings) &&
		!codingHarnessFindingsHaveBlockers(r.Outcome.Findings)
}

func (c *AcceptanceContract) Normalize() {
	if c == nil {
		return
	}
	c.ID = strings.TrimSpace(c.ID)
	c.SourcePrompt = strings.Join(strings.Fields(strings.TrimSpace(c.SourcePrompt)), " ")
	c.Mode = strings.TrimSpace(strings.ToLower(c.Mode))
	c.ExpectedBehaviors = normalizeTaskStateList(c.ExpectedBehaviors, 8)
	c.NonGoals = normalizeTaskStateList(c.NonGoals, 8)
	c.ChangedSurfaces = normalizeTaskStateList(c.ChangedSurfaces, 16)
	c.RequiredArtifacts = normalizeTaskStateList(c.RequiredArtifacts, 16)
	c.VerificationNotes = normalizeTaskStateList(c.VerificationNotes, 8)
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
}

func (t PatchTransaction) ChangedPaths() []string {
	paths := make([]string, 0)
	for _, entry := range t.Entries {
		if strings.EqualFold(strings.TrimSpace(entry.Status), "failed") {
			continue
		}
		for _, change := range entry.Paths {
			if strings.TrimSpace(change.Path) != "" {
				paths = append(paths, change.Path)
			}
		}
	}
	return normalizeTaskStateList(paths, 64)
}

func (t PatchTransaction) RenderPromptSection() string {
	t.Normalize()
	if strings.TrimSpace(t.ID) == "" {
		return ""
	}
	changed := t.ChangedPaths()
	lines := []string{
		fmt.Sprintf("- Patch transaction: %s [%s]", t.ID, t.Status),
	}
	if len(changed) > 0 {
		lines = append(lines, "- Changed paths: "+strings.Join(changed, ", "))
	}
	if len(t.Warnings) > 0 {
		lines = append(lines, "- Warnings: "+strings.Join(t.Warnings, " | "))
	}
	recent := t.Entries
	if len(recent) > 4 {
		recent = recent[len(recent)-4:]
	}
	for _, entry := range recent {
		item := fmt.Sprintf("- %s [%s]", entry.ToolName, entry.Status)
		if len(entry.Paths) > 0 {
			item += ": " + strings.Join(patchChangePaths(entry.Paths), ", ")
		}
		if entry.Error != "" {
			item += " | error=" + compactPromptSection(entry.Error, 160)
		}
		lines = append(lines, item)
	}
	return strings.Join(lines, "\n")
}

func (r CodingHarnessReport) RenderPromptSection() string {
	r.Normalize()
	lines := make([]string, 0, 12)
	status := "approved"
	if !r.Approved {
		status = "needs_revision"
	}
	lines = append(lines, "- Coding harness: "+status)
	if len(r.DiffReview.ChangedPaths) > 0 {
		lines = append(lines, "- Diff-aware changed paths: "+strings.Join(r.DiffReview.ChangedPaths, ", "))
	}
	for _, finding := range r.allFindings() {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", finding.Severity, finding.Title, compactPromptSection(finding.Detail, 220)))
		if len(lines) >= finalHarnessMaxFindings+2 {
			break
		}
	}
	if len(r.ArtifactQuality.Artifacts) > 0 {
		lines = append(lines, fmt.Sprintf("- Artifact quality: checked=%d findings=%d", len(r.ArtifactQuality.Artifacts), len(r.ArtifactQuality.Findings)))
	}
	if len(r.ScenarioReplay.Scenarios) > 0 {
		lines = append(lines, fmt.Sprintf("- Scenario replay: scenarios=%d findings=%d", len(r.ScenarioReplay.Scenarios), len(r.ScenarioReplay.Findings)))
	}
	if r.SubagentOrchestration.WorkerEvidenceCount > 0 || r.SubagentOrchestration.ReviewFailures > 0 {
		lines = append(lines, fmt.Sprintf("- Subagent orchestration: workers=%d causal=%d review_failures=%d", r.SubagentOrchestration.WorkerEvidenceCount, r.SubagentOrchestration.CausalEvidenceCount, r.SubagentOrchestration.ReviewFailures))
	}
	if len(r.TestImpact.RecommendedCommands) > 0 {
		lines = append(lines, "- Test impact: "+r.TestImpact.Confidence+" | "+strings.Join(r.TestImpact.RecommendedCommands, " | "))
	}
	if r.JobSupervisor.Total > 0 {
		lines = append(lines, fmt.Sprintf("- Job supervisor: total=%d running=%d failed=%d stale=%d", r.JobSupervisor.Total, r.JobSupervisor.Running, r.JobSupervisor.Failed, r.JobSupervisor.Stale))
	}
	if r.JobSupervisor.BundleTotal > 0 {
		lines = append(lines, fmt.Sprintf("- Job supervisor bundles: total=%d running=%d failed=%d stale=%d", r.JobSupervisor.BundleTotal, r.JobSupervisor.BundleRunning, r.JobSupervisor.BundleFailed, r.JobSupervisor.BundleStale))
	}
	if r.Acceptance.ContractID != "" {
		lines = append(lines, "- Acceptance contract: "+r.Acceptance.ContractID)
	}
	if len(r.Acceptance.Checks) > 0 {
		lines = append(lines, "- Acceptance checks: "+strings.Join(r.Acceptance.Checks, " | "))
	}
	if len(r.Outcome.Checks) > 0 {
		lines = append(lines, "- Outcome checks: "+strings.Join(r.Outcome.Checks, " | "))
	}
	return strings.Join(lines, "\n")
}

func (r CodingHarnessReport) BlockingFeedback() string {
	r.Normalize()
	blockers := make([]string, 0)
	for _, finding := range r.allFindings() {
		if !strings.EqualFold(strings.TrimSpace(finding.Severity), "blocker") {
			continue
		}
		blockers = append(blockers, fmt.Sprintf("- %s: %s", finding.Title, finding.Detail))
		if len(blockers) >= finalHarnessMaxFindings {
			break
		}
	}
	if len(blockers) == 0 {
		return ""
	}
	return "Pre-final coding harness found issues that must be resolved before concluding:\n" + strings.Join(blockers, "\n") + "\n\nRevise the work or the final answer so it matches the actual workspace state. Do not claim verification, created artifacts, or no remaining blockers unless the harness evidence supports that."
}

func (r CodingHarnessReport) allFindings() []CodingHarnessFinding {
	out := make([]CodingHarnessFinding, 0)
	out = append(out, r.Findings...)
	out = append(out, r.Acceptance.Findings...)
	out = append(out, r.ArtifactQuality.Findings...)
	out = append(out, r.ScenarioReplay.Findings...)
	out = append(out, r.SubagentOrchestration.Findings...)
	out = append(out, r.TestImpact.Findings...)
	out = append(out, r.JobSupervisor.Findings...)
	out = append(out, r.DiffReview.Findings...)
	out = append(out, r.Outcome.Findings...)
	return out
}

func (a *Agent) beginPatchTransactionToolProbe(call ToolCall) *PatchTransactionProbe {
	if a == nil || a.Session == nil {
		return nil
	}
	scopes := patchTransactionCandidateScopes(a.Workspace, call)
	if len(scopes) == 0 {
		return nil
	}
	goal := latestUserMessageText(a.Session.Messages)
	tx := a.Session.ensurePatchTransaction(goal, a.Workspace.Root)
	if tx == nil {
		return nil
	}
	now := time.Now()
	entryID := fmt.Sprintf("%s-%03d", tx.ID, len(tx.Entries)+1)
	before, snapshotErr := snapshotHarnessScopes(a.Workspace.Root, scopes)
	probe := &PatchTransactionProbe{
		TransactionID: tx.ID,
		EntryID:       entryID,
		ToolName:      strings.TrimSpace(call.Name),
		OwnerNodeID:   strings.TrimSpace(stringValue(toolCallArgumentsMap(call), "owner_node_id")),
		Command:       strings.TrimSpace(stringValue(toolCallArgumentsMap(call), "command")),
		Reason:        summarizeToolDiagnosticCall(call),
		Root:          a.Workspace.Root,
		Scopes:        append([]string(nil), scopes...),
		Before:        before,
		StartedAt:     now,
	}
	if snapshotErr != nil {
		probe.SnapshotError = snapshotErr.Error()
		tx.Warnings = appendTaskStateItem(tx.Warnings, "Could not capture pre-edit fingerprint for "+strings.TrimSpace(call.Name)+": "+snapshotErr.Error(), 8)
	}
	tx.UpdatedAt = now
	return probe
}

func (a *Agent) finishPatchTransactionToolProbe(probe *PatchTransactionProbe, call ToolCall, result ToolExecutionResult, execErr error) {
	if a == nil || a.Session == nil || probe == nil {
		return
	}
	tx := a.Session.ActivePatchTransaction
	if tx == nil || !strings.EqualFold(strings.TrimSpace(tx.ID), strings.TrimSpace(probe.TransactionID)) {
		return
	}
	scopes := append([]string(nil), probe.Scopes...)
	scopes = append(scopes, resolveHarnessScopes(a.Workspace, strings.TrimSpace(probe.OwnerNodeID), toolMetaStringSlice(result.Meta, "changed_paths"), false)...)
	scopes = uniqueStrings(scopes)
	after, snapshotErr := snapshotHarnessScopes(firstNonBlankString(probe.Root, a.Workspace.Root), scopes)
	changes := diffHarnessSnapshots(probe.Before, after)
	entry := PatchTransactionEntry{
		ID:          probe.EntryID,
		ToolName:    strings.TrimSpace(call.Name),
		OwnerNodeID: strings.TrimSpace(probe.OwnerNodeID),
		Command:     strings.TrimSpace(probe.Command),
		Reason:      strings.TrimSpace(probe.Reason),
		Status:      "success",
		StartedAt:   probe.StartedAt,
		CompletedAt: time.Now(),
		Paths:       changes,
	}
	if execErr != nil {
		entry.Status = "failed"
		entry.Error = execErr.Error()
	}
	if probe.SnapshotError != "" {
		entry.Error = joinSentence(entry.Error, "pre-snapshot: "+probe.SnapshotError)
	}
	if snapshotErr != nil {
		entry.Error = joinSentence(entry.Error, "post-snapshot: "+snapshotErr.Error())
		tx.Warnings = appendTaskStateItem(tx.Warnings, "Could not capture post-edit fingerprint for "+entry.ToolName+": "+snapshotErr.Error(), 8)
	}
	tx.Entries = append(tx.Entries, entry)
	tx.UpdatedAt = entry.CompletedAt
	if result.Meta != nil {
		result.Meta["patch_transaction_id"] = tx.ID
		result.Meta["patch_transaction_entry_id"] = entry.ID
		if len(changes) > 0 {
			result.Meta["changed_paths"] = normalizeTaskStateList(append(toolMetaStringSlice(result.Meta, "changed_paths"), patchChangePaths(changes)...), 32)
			result.Meta["changed_workspace"] = true
			result.Meta["requires_verification"] = true
		}
	}
	if len(changes) > 0 {
		a.markAgentTouchedPaths(patchChangePaths(changes))
	}
	if a.Session.TaskState != nil {
		status := "ok"
		if execErr != nil {
			status = "error"
		}
		a.Session.TaskState.RecordEvent("patch_transaction", entry.OwnerNodeID, entry.ToolName, "Recorded patch transaction entry.", strings.Join(patchChangePaths(changes), ", "), status, false)
	}
}

func (a *Agent) recordPatchTransactionFromToolMetaIfNeeded(call ToolCall, result ToolExecutionResult, execErr error) {
	if a == nil || a.Session == nil || execErr != nil || result.Meta == nil {
		return
	}
	if strings.TrimSpace(toolMetaString(result.Meta, "patch_transaction_entry_id")) != "" {
		return
	}
	effect := strings.TrimSpace(strings.ToLower(toolMetaString(result.Meta, "effect")))
	if effect != "edit" && !toolMetaBool(result.Meta, "changed_workspace") {
		return
	}
	paths := toolMetaStringSlice(result.Meta, "changed_paths")
	if len(paths) == 0 {
		return
	}
	scopes := resolveHarnessScopes(a.Workspace, strings.TrimSpace(toolMetaString(result.Meta, "owner_node_id")), paths, false)
	tx := a.Session.ensurePatchTransaction(latestUserMessageText(a.Session.Messages), a.Workspace.Root)
	if tx == nil {
		return
	}
	now := time.Now()
	after, _ := snapshotHarnessScopes(a.Workspace.Root, scopes)
	entry := PatchTransactionEntry{
		ID:          fmt.Sprintf("%s-%03d", tx.ID, len(tx.Entries)+1),
		ToolName:    strings.TrimSpace(call.Name),
		OwnerNodeID: strings.TrimSpace(toolMetaString(result.Meta, "owner_node_id")),
		Reason:      "metadata-only patch transaction entry",
		Status:      "success",
		StartedAt:   now,
		CompletedAt: now,
	}
	for _, path := range normalizeTaskStateList(paths, 32) {
		fingerprint, ok := after[normalizeSessionRelativePath(path)]
		if !ok {
			fingerprint = HarnessFileFingerprint{Path: normalizeSessionRelativePath(path)}
		}
		entry.Paths = append(entry.Paths, PatchPathChange{
			Path:      normalizeSessionRelativePath(path),
			Operation: "unknown",
			After:     fingerprint,
		})
	}
	entry.Paths = normalizePatchPathChanges(entry.Paths)
	tx.Entries = append(tx.Entries, entry)
	tx.UpdatedAt = now
	result.Meta["patch_transaction_id"] = tx.ID
	result.Meta["patch_transaction_entry_id"] = entry.ID
	a.markAgentTouchedPaths(paths)
}

func (a *Agent) finalizePatchTransactionOnReturn() {
	if a == nil || a.Session == nil || a.Session.ActivePatchTransaction == nil {
		return
	}
	a.Session.finalizeActivePatchTransaction(patchTransactionStatusCommitted)
}

func (a *Agent) runPreFinalCodingHarnesses(ctx context.Context, reply string, attemptedEditTool bool, unresolvedVerification bool) (bool, string) {
	_ = ctx
	if a == nil || a.Session == nil {
		return true, ""
	}
	report := a.buildCodingHarnessReport(reply, attemptedEditTool, unresolvedVerification)
	a.Session.LastCodingHarnessReport = &report
	a.Session.LastTestImpactReport = &report.TestImpact
	a.Session.LastJobSupervisorReport = &report.JobSupervisor
	if report.Approved {
		return true, ""
	}
	return false, report.BlockingFeedback()
}

func (a *Agent) buildCodingHarnessReport(reply string, attemptedEditTool bool, unresolvedVerification bool) CodingHarnessReport {
	report := CodingHarnessReport{
		GeneratedAt: time.Now(),
		Approved:    true,
	}
	if a == nil || a.Session == nil {
		return report
	}
	report.Acceptance = a.buildAcceptanceContractReport(reply)
	report.ArtifactQuality = a.buildArtifactQualityReport(reply)
	report.ScenarioReplay = a.buildScenarioReplayReport(reply)
	report.SubagentOrchestration = a.buildSubagentOrchestrationReport(reply)
	report.TestImpact = a.buildTestImpactReport()
	report.JobSupervisor = a.buildJobSupervisorReport(reply)
	report.DiffReview = a.buildDiffAwareSelfReviewReport(reply, attemptedEditTool)
	report.Outcome = a.buildOutcomeInvariantReport(reply, unresolvedVerification)
	report.Normalize()
	return report
}

func (a *Agent) buildAcceptanceContractReport(reply string) AcceptanceContractReport {
	report := AcceptanceContractReport{}
	if a == nil || a.Session == nil || a.Session.AcceptanceContract == nil {
		return report
	}
	contract := *a.Session.AcceptanceContract
	contract.Normalize()
	report.ContractID = contract.ID
	for _, artifact := range contract.RequiredArtifacts {
		abs, rel, ok := resolveClaimedArtifactPath(a.Workspace.Root, artifact)
		if !ok {
			continue
		}
		report.Checks = append(report.Checks, "required artifact exists: "+rel)
		info, err := os.Stat(abs)
		if err != nil {
			report.Findings = append(report.Findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "Required artifact is missing",
				Detail:   fmt.Sprintf("The acceptance contract requires %s, but it does not exist in the workspace.", rel),
			})
			continue
		}
		if !info.IsDir() && info.Size() == 0 {
			report.Findings = append(report.Findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "Required artifact is empty",
				Detail:   fmt.Sprintf("The acceptance contract requires %s, but the file is empty.", rel),
			})
		}
	}
	if strings.EqualFold(contract.Mode, "analysis_only") && len(sessionPatchTransactionChangedPaths(a.Session)) > 0 {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Analysis-only contract was modified",
			Detail:   "The acceptance contract is analysis-only, but the patch transaction recorded workspace changes.",
		})
	}
	if contract.VerificationRequired && !sessionHasSuccessfulVerificationEvidence(a.Session) && !replyMentionsVerificationNotRun(reply) && !replyMentionsVerificationBlocker(reply) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Required verification has no outcome",
			Detail:   "The user explicitly requested verification/testing/build evidence, but the session has no successful verification result and the final answer does not report a verification blocker.",
		})
	}
	report.Checks = normalizeTaskStateList(report.Checks, 16)
	report.Findings = normalizeCodingHarnessFindings(report.Findings)
	return report
}

func (a *Agent) buildDiffAwareSelfReviewReport(reply string, attemptedEditTool bool) DiffAwareSelfReviewReport {
	report := DiffAwareSelfReviewReport{}
	changed := sessionPatchTransactionChangedPaths(a.Session)
	report.ChangedPaths = changed
	lowerReply := strings.ToLower(strings.TrimSpace(reply))
	if len(changed) > 0 && replyClaimsNoFileChanges(lowerReply) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Final answer contradicts the patch transaction",
			Detail:   "The final answer says no files changed, but the patch transaction recorded changes in: " + strings.Join(changed, ", "),
		})
	}
	if len(changed) > 0 && containsAny(lowerReply, "verified", "tests passed", "test passed", "검증 통과", "테스트 통과") && !replyMentionsVerificationNotRun(reply) && !sessionHasSuccessfulVerificationEvidence(a.Session) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Verification claim has no recorded evidence",
			Detail:   "The final answer claims verification passed, but the session has no successful verification report or verification-like shell result.",
		})
	}
	if attemptedEditTool && len(changed) == 0 {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "warning",
			Title:    "Edit tool produced no fingerprinted diff",
			Detail:   "An edit tool was attempted, but the patch transaction did not record a changed file fingerprint. Confirm this was intentional before claiming a code change.",
		})
	}
	report.Findings = normalizeCodingHarnessFindings(report.Findings)
	return report
}

func (a *Agent) buildOutcomeInvariantReport(reply string, unresolvedVerification bool) OutcomeInvariantReport {
	report := OutcomeInvariantReport{}
	if a == nil || a.Session == nil {
		return report
	}
	a.refreshBackgroundJobs()
	if a.hasRunningBackgroundJobs() {
		severity := "blocker"
		detail := "A background shell job or bundle is still running. Poll or cancel it before giving the final answer."
		if replyMentionsBackgroundStillRunning(reply) {
			severity = "warning"
			detail = "A background shell job or bundle is still running, and the final answer explicitly reports that state."
		}
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: severity,
			Title:    "Background work is still running",
			Detail:   detail,
		})
	}
	if (unresolvedVerification || (a.Session.LastVerification != nil && a.Session.LastVerification.HasFailures())) && !replyMentionsVerificationBlocker(reply) && !replyMentionsVerificationNotRun(reply) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Unresolved verification failure",
			Detail:   "The latest verification still has failures, but the final answer does not clearly state the blocker.",
		})
	}
	if hasPendingVerificationCheck(a.Session) && !sessionHasSuccessfulVerificationEvidence(a.Session) && !replyMentionsVerificationNotRun(reply) && !replyMentionsVerificationBlocker(reply) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "warning",
			Title:    "Verification pending check is unresolved",
			Detail:   "The task state still recommends verification after edits. The final answer should avoid claiming verification passed unless a focused build/test was recorded.",
		})
	}
	for _, path := range extractClaimedArtifactPaths(reply) {
		abs, rel, ok := resolveClaimedArtifactPath(a.Workspace.Root, path)
		if !ok {
			continue
		}
		report.Checks = append(report.Checks, "artifact exists: "+rel)
		info, err := os.Stat(abs)
		if err != nil {
			report.Findings = append(report.Findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "Claimed artifact is missing",
				Detail:   fmt.Sprintf("The final answer appears to claim %s was created or updated, but it does not exist in the workspace.", rel),
			})
			continue
		}
		if !info.IsDir() && info.Size() == 0 {
			report.Findings = append(report.Findings, CodingHarnessFinding{
				Severity: "blocker",
				Title:    "Claimed artifact is empty",
				Detail:   fmt.Sprintf("The final answer appears to claim %s was created or updated, but the file is empty.", rel),
			})
		}
	}
	report.Checks = normalizeTaskStateList(report.Checks, 16)
	report.Findings = normalizeCodingHarnessFindings(report.Findings)
	return report
}

func patchTransactionCandidateScopes(ws Workspace, call ToolCall) []string {
	name := strings.TrimSpace(call.Name)
	args := toolCallArgumentsMap(call)
	ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id"))
	specs := make([]harnessPathSpec, 0)
	switch name {
	case "write_file":
		if path := strings.TrimSpace(stringValue(args, "path")); path != "" {
			specs = append(specs, harnessPathSpec{Path: path, MustExist: false})
		}
	case "replace_in_file":
		if path := strings.TrimSpace(stringValue(args, "path")); path != "" {
			specs = append(specs, harnessPathSpec{Path: path, MustExist: true})
		}
	case "apply_patch":
		doc, err := parsePatchDocument(stringValue(args, "patch"))
		if err != nil {
			return nil
		}
		for _, op := range doc.ops {
			switch strings.TrimSpace(strings.ToLower(op.kind)) {
			case "add":
				specs = append(specs, harnessPathSpec{Path: op.path, MustExist: false})
			case "delete":
				specs = append(specs, harnessPathSpec{Path: op.path, MustExist: true})
			case "update":
				specs = append(specs, harnessPathSpec{Path: op.path, MustExist: true})
				if strings.TrimSpace(op.moveTo) != "" {
					specs = append(specs, harnessPathSpec{Path: op.moveTo, MustExist: false})
				}
			}
		}
	case "run_shell":
		command := strings.TrimSpace(stringValue(args, "command"))
		assessment := assessShellCommandMutation(command)
		if assessment.Class != shellMutationWorkspaceWrite || !boolValue(args, "allow_workspace_writes", false) {
			return nil
		}
		for _, path := range stringSliceValue(args, "write_paths") {
			specs = append(specs, harnessPathSpec{Path: path, MustExist: false})
		}
	default:
		return nil
	}
	return resolveHarnessPathSpecs(ws, ownerNodeID, specs)
}

type harnessPathSpec struct {
	Path      string
	MustExist bool
}

func resolveHarnessPathSpecs(ws Workspace, ownerNodeID string, specs []harnessPathSpec) []string {
	out := make([]string, 0, len(specs))
	for _, spec := range specs {
		trimmed := strings.TrimSpace(spec.Path)
		if trimmed == "" {
			continue
		}
		route, err := ws.ResolveEditPath(trimmed, ownerNodeID, spec.MustExist)
		if err == nil && strings.TrimSpace(route.AbsolutePath) != "" {
			out = append(out, route.AbsolutePath)
			continue
		}
		if filepath.IsAbs(trimmed) {
			out = append(out, filepath.Clean(trimmed))
			continue
		}
		root := firstNonBlankString(ws.Root, ws.BaseRoot)
		if root != "" {
			out = append(out, filepath.Join(root, trimmed))
		}
	}
	return uniqueStrings(out)
}

func resolveHarnessScopes(ws Workspace, ownerNodeID string, paths []string, mustExist bool) []string {
	specs := make([]harnessPathSpec, 0, len(paths))
	for _, path := range paths {
		specs = append(specs, harnessPathSpec{Path: path, MustExist: mustExist})
	}
	return resolveHarnessPathSpecs(ws, ownerNodeID, specs)
}

func snapshotHarnessScopes(root string, scopes []string) (map[string]HarnessFileFingerprint, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	out := map[string]HarnessFileFingerprint{}
	var firstErr error
	for _, scope := range uniqueStrings(scopes) {
		trimmed := strings.TrimSpace(scope)
		if trimmed == "" {
			continue
		}
		abs := trimmed
		if !filepath.IsAbs(abs) && root != "" {
			abs = filepath.Join(root, abs)
		}
		abs = filepath.Clean(abs)
		if err := snapshotHarnessScope(root, abs, out); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return out, firstErr
}

func snapshotHarnessScope(root string, abs string, out map[string]HarnessFileFingerprint) error {
	info, err := os.Stat(abs)
	rel := harnessRelPath(root, abs)
	if err != nil {
		if os.IsNotExist(err) {
			out[rel] = HarnessFileFingerprint{Path: rel, Kind: "missing", Exists: false}
			return nil
		}
		out[rel] = HarnessFileFingerprint{Path: rel, Kind: "unknown", Exists: false, ReadFailed: err.Error()}
		return err
	}
	if !info.IsDir() {
		fp, fpErr := fingerprintHarnessFile(root, abs, info)
		out[rel] = fp
		return fpErr
	}
	out[rel] = HarnessFileFingerprint{Path: rel, Kind: "dir", Exists: true, ModTime: info.ModTime().UnixNano()}
	return filepath.WalkDir(abs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == abs {
			return nil
		}
		if d.IsDir() {
			name := strings.ToLower(strings.TrimSpace(d.Name()))
			if name == ".git" || name == ".claude" || name == userConfigDirName {
				return filepath.SkipDir
			}
			return nil
		}
		info, infoErr := d.Info()
		relPath := harnessRelPath(root, path)
		if infoErr != nil {
			out[relPath] = HarnessFileFingerprint{Path: relPath, Kind: "file", Exists: true, ReadFailed: infoErr.Error()}
			return infoErr
		}
		fp, fpErr := fingerprintHarnessFile(root, path, info)
		out[relPath] = fp
		return fpErr
	})
}

func fingerprintHarnessFile(root string, abs string, info os.FileInfo) (HarnessFileFingerprint, error) {
	rel := harnessRelPath(root, abs)
	fp := HarnessFileFingerprint{
		Path:    rel,
		Kind:    "file",
		Exists:  true,
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
	}
	file, err := os.Open(abs)
	if err != nil {
		fp.ReadFailed = err.Error()
		return fp, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		fp.ReadFailed = err.Error()
		return fp, err
	}
	fp.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return fp, nil
}

func diffHarnessSnapshots(before map[string]HarnessFileFingerprint, after map[string]HarnessFileFingerprint) []PatchPathChange {
	paths := map[string]bool{}
	for path := range before {
		paths[path] = true
	}
	for path := range after {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	changes := make([]PatchPathChange, 0)
	for _, path := range ordered {
		left := before[path]
		right := after[path]
		left.Normalize()
		right.Normalize()
		if harnessFingerprintsEqual(left, right) {
			continue
		}
		op := "update"
		if !left.Exists && right.Exists {
			op = "create"
		} else if left.Exists && !right.Exists {
			op = "delete"
		}
		changes = append(changes, PatchPathChange{
			Path:      normalizeSessionRelativePath(path),
			Operation: op,
			Before:    left,
			After:     right,
		})
	}
	return normalizePatchPathChanges(changes)
}

func harnessFingerprintsEqual(left HarnessFileFingerprint, right HarnessFileFingerprint) bool {
	left.Normalize()
	right.Normalize()
	if left.Path != right.Path || left.Kind != right.Kind || left.Exists != right.Exists || left.Size != right.Size {
		return false
	}
	if left.SHA256 != "" || right.SHA256 != "" {
		return left.SHA256 == right.SHA256
	}
	return left.ModTime == right.ModTime && left.ReadFailed == right.ReadFailed
}

func harnessRelPath(root string, abs string) string {
	root = strings.TrimSpace(root)
	abs = filepath.Clean(strings.TrimSpace(abs))
	if root != "" {
		if rel, err := filepath.Rel(root, abs); err == nil {
			return normalizeSessionRelativePath(rel)
		}
	}
	return filepath.ToSlash(abs)
}

func normalizePatchPathChanges(changes []PatchPathChange) []PatchPathChange {
	if len(changes) == 0 {
		return nil
	}
	byPath := map[string]PatchPathChange{}
	for _, change := range changes {
		change.Normalize()
		if strings.TrimSpace(change.Path) == "" {
			continue
		}
		byPath[change.Path] = change
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]PatchPathChange, 0, len(paths))
	for _, path := range paths {
		out = append(out, byPath[path])
	}
	return out
}

func patchChangePaths(changes []PatchPathChange) []string {
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		if strings.TrimSpace(change.Path) != "" {
			paths = append(paths, change.Path)
		}
	}
	return normalizeTaskStateList(paths, 64)
}

func sessionPatchTransactionChangedPaths(sess *Session) []string {
	if sess == nil {
		return nil
	}
	paths := make([]string, 0)
	if sess.ActivePatchTransaction != nil {
		paths = append(paths, sess.ActivePatchTransaction.ChangedPaths()...)
	}
	if len(sess.PatchTransactions) > 0 {
		paths = append(paths, sess.PatchTransactions[0].ChangedPaths()...)
	}
	return normalizeTaskStateList(paths, 64)
}

func hasPendingVerificationCheck(sess *Session) bool {
	if sess == nil || sess.TaskState == nil {
		return false
	}
	for _, check := range sess.TaskState.PendingChecks {
		if strings.EqualFold(strings.Join(strings.Fields(check), " "), verificationPendingCheck) {
			return true
		}
	}
	return false
}

func sessionHasSuccessfulVerificationEvidence(sess *Session) bool {
	if sess == nil {
		return false
	}
	if sess.LastVerification != nil && !sess.LastVerification.HasFailures() {
		return true
	}
	for _, msg := range sess.Messages {
		if msg.Role != "tool" || msg.IsError {
			continue
		}
		if toolMetaBool(msg.ToolMeta, "verification_like") && toolMetaBoolDefault(msg.ToolMeta, "success", true) {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(msg.ToolName), "run_shell") && runShellOutputLooksLikeVerification(msg.Text) && toolMetaBoolDefault(msg.ToolMeta, "success", true) {
			return true
		}
	}
	return false
}

func replyClaimsNoFileChanges(lowerReply string) bool {
	return containsAny(lowerReply,
		"no files were changed",
		"no file changes",
		"did not change files",
		"didn't change files",
		"파일을 변경하지 않았",
		"파일 변경은 없",
		"수정하지 않았",
	)
}

func replyMentionsVerificationNotRun(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	return containsAny(lower,
		"not run",
		"not executed",
		"did not run",
		"was not run",
		"unverified",
		"verification was disabled",
		"verification disabled",
		"toolchain is unavailable",
		"toolchain unavailable",
		"검증하지 않았",
		"검증은 실행하지 않았",
		"검증이 비활성화",
		"검증은 비활성화",
		"테스트는 실행하지 않았",
		"테스트하지 않았",
		"미검증",
	)
}

func replyMentionsVerificationBlocker(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	return containsAny(lower,
		"verification failed",
		"verification is still failing",
		"verification still failing",
		"tests failed",
		"tests are still failing",
		"still failing",
		"build failed",
		"blocker",
		"blocked",
		"검증 실패",
		"검증이 아직 실패",
		"테스트 실패",
		"테스트가 아직 실패",
		"빌드 실패",
		"블로커",
		"막혀",
	)
}

func replyMentionsBackgroundStillRunning(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if containsAny(lower,
		"still running",
		"still in progress",
		"job is running",
		"job is still running",
		"running job",
		"아직 실행 중",
		"계속 실행 중",
	) {
		return true
	}
	return containsAny(lower, "background", "job", "bundle", "백그라운드", "작업", "묶음") &&
		containsAny(lower, "running", "in progress", "실행 중", "진행 중")
}

func extractClaimedArtifactPaths(reply string) []string {
	paths := make([]string, 0)
	normalized := strings.ReplaceAll(reply, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if !lineLooksLikeArtifactClaim(lower) {
			continue
		}
		for _, token := range pathTokensFromLine(line) {
			if pathTokenLooksVerifiable(token) {
				paths = append(paths, token)
			}
		}
	}
	return normalizeTaskStateList(paths, 32)
}

func lineLooksLikeArtifactClaim(lower string) bool {
	if lineLooksLikeNegativeArtifactClaim(lower) {
		return false
	}
	return containsAny(lower,
		"created", "generated", "wrote", "saved", "added", "updated",
		"생성", "작성", "저장", "추가", "업데이트", "수정",
	)
}

func lineLooksLikeNegativeArtifactClaim(lower string) bool {
	return containsAny(lower,
		"did not create",
		"didn't create",
		"not created",
		"was not created",
		"did not generate",
		"didn't generate",
		"not generated",
		"was not generated",
		"did not write",
		"didn't write",
		"not written",
		"was not written",
		"생성하지 않았",
		"생성하지 않",
		"작성하지 않았",
		"작성하지 않",
		"저장하지 않았",
		"저장하지 않",
	)
}

func pathTokensFromLine(line string) []string {
	fields := strings.FieldsFunc(line, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("`'\"()[]{}<>,;", r)
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.TrimSpace(strings.Trim(field, ".:"))
		token = strings.TrimLeft(token, "@")
		if token == "" {
			continue
		}
		if idx := strings.LastIndex(token, ":"); idx > 1 && harnessAllDigits(token[idx+1:]) {
			token = token[:idx]
		}
		out = append(out, token)
	}
	return out
}

func pathTokenLooksVerifiable(token string) bool {
	token = strings.TrimSpace(strings.Trim(token, "."))
	if token == "" || strings.HasPrefix(strings.ToLower(token), "http://") || strings.HasPrefix(strings.ToLower(token), "https://") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(token))
	if ext == "" || len(ext) > 10 {
		return false
	}
	if pathLooksLikeDocumentArtifact(token) || isCodeLikePath(token) {
		return true
	}
	switch ext {
	case ".json", ".yaml", ".yml", ".toml", ".xml", ".html", ".css", ".csv", ".tsv", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".pdf", ".docx", ".pptx", ".xlsx":
		return true
	default:
		return false
	}
}

func resolveClaimedArtifactPath(root string, raw string) (string, string, bool) {
	root = strings.TrimSpace(root)
	raw = strings.TrimSpace(strings.Trim(raw, "`'\""))
	if raw == "" || root == "" {
		return "", "", false
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, filepath.FromSlash(raw))
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", "", false
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", "", false
	}
	return abs, normalizeSessionRelativePath(rel), true
}

func normalizeCodingHarnessFindings(findings []CodingHarnessFinding) []CodingHarnessFinding {
	if len(findings) == 0 {
		return nil
	}
	out := make([]CodingHarnessFinding, 0, len(findings))
	seen := map[string]bool{}
	for _, finding := range findings {
		finding.Severity = strings.TrimSpace(strings.ToLower(finding.Severity))
		if finding.Severity == "" {
			finding.Severity = "warning"
		}
		finding.Title = strings.TrimSpace(finding.Title)
		finding.Detail = strings.TrimSpace(finding.Detail)
		key := finding.Severity + "\x00" + finding.Title + "\x00" + finding.Detail
		if finding.Title == "" && finding.Detail == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, finding)
		if len(out) >= finalHarnessMaxFindings {
			break
		}
	}
	return out
}

func codingHarnessFindingsHaveBlockers(findings []CodingHarnessFinding) bool {
	for _, finding := range findings {
		if strings.EqualFold(strings.TrimSpace(finding.Severity), "blocker") {
			return true
		}
	}
	return false
}

func toolMetaBoolDefault(meta map[string]any, key string, def bool) bool {
	if meta == nil {
		return def
	}
	value, ok := meta[key]
	if !ok {
		return def
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.TrimSpace(strings.ToLower(typed)) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		default:
			return def
		}
	default:
		return def
	}
}

func harnessAllDigits(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
