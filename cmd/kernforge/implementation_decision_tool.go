package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	implementationDecisionPendingChoice    = "choice"
	implementationDecisionPendingRationale = "rationale"
	implementationDecisionCustomOptionID   = "__custom__"
)

// UserTextQuestion is a bounded free-text prompt used after a structured choice.
type UserTextQuestion struct {
	Question    string
	Header      string
	Placeholder string
	Required    bool
	MaxLength   int
}

// UserTextResult carries one free-text answer from the interactive runtime.
type UserTextResult struct {
	Text     string
	Canceled bool
}

// PendingImplementationDecision keeps only resumable prompt state in a session.
// Rationale text is not persisted here; completed rationale is written directly
// to the private global decision journal.
type PendingImplementationDecision struct {
	DecisionID       string                       `json:"decision_id"`
	Stage            string                       `json:"stage"`
	Record           ImplementationDecisionRecord `json:"record"`
	SelectedOptionID string                       `json:"selected_option_id,omitempty"`
	CustomSelection  string                       `json:"custom_selection,omitempty"`
}

func (pending *PendingImplementationDecision) ApproxChars() int {
	if pending == nil {
		return 0
	}
	total := len(pending.DecisionID) + len(pending.Stage) + len(pending.Record.Problem) + len(pending.SelectedOptionID) + len(pending.CustomSelection)
	for _, option := range pending.Record.Options {
		total += len(option.ID) + len(option.Label) + len(option.Description)
	}
	return total
}

func (session *Session) normalizePendingImplementationDecision() {
	if session == nil || session.PendingImplementationDecision == nil {
		return
	}
	pending := session.PendingImplementationDecision
	pending.DecisionID = strings.TrimSpace(pending.DecisionID)
	pending.Stage = strings.ToLower(strings.TrimSpace(pending.Stage))
	pending.SelectedOptionID = strings.TrimSpace(pending.SelectedOptionID)
	pending.CustomSelection = strings.TrimSpace(pending.CustomSelection)
	if pending.Record.ID == "" {
		pending.Record.ID = pending.DecisionID
	}
	if pending.DecisionID == "" {
		pending.DecisionID = pending.Record.ID
	}
	if pending.Stage != implementationDecisionPendingChoice && pending.Stage != implementationDecisionPendingRationale {
		pending.Stage = implementationDecisionPendingChoice
	}
	if pending.Stage == implementationDecisionPendingRationale && !pendingImplementationDecisionSelectionIsValid(pending) {
		pending.Stage = implementationDecisionPendingChoice
		pending.SelectedOptionID = ""
		pending.CustomSelection = ""
	}
	if pending.Stage == implementationDecisionPendingChoice {
		pending.SelectedOptionID = ""
		pending.CustomSelection = ""
	}
	redactPendingImplementationDecision(pending)
}

func pendingImplementationDecisionSelectionIsValid(pending *PendingImplementationDecision) bool {
	if pending == nil {
		return false
	}
	selectedID := strings.TrimSpace(pending.SelectedOptionID)
	custom := strings.TrimSpace(pending.CustomSelection)
	if (selectedID == "") == (custom == "") {
		return false
	}
	if custom != "" {
		return len(custom) <= 1024
	}
	for _, option := range pending.Record.Options {
		if option.ID == selectedID {
			return true
		}
	}
	return false
}

// ImplementationDecisionTool captures a material implementation fork before
// workspace mutation. It writes the completed choice to the global journal.
type ImplementationDecisionTool struct {
	ws Workspace
}

func NewImplementationDecisionTool(ws Workspace) ImplementationDecisionTool {
	return ImplementationDecisionTool{ws: ws}
}

func (t ImplementationDecisionTool) Definition() ToolDefinition {
	captureProperties := map[string]any{
		"decision_id":   map[string]any{"type": "string", "description": "The exact pending decision id to resume. The persisted pending record is canonical."},
		"problem":       map[string]any{"type": "string", "description": "The concrete implementation problem that has multiple viable approaches."},
		"header":        map[string]any{"type": "string", "description": "A short heading for the decision prompt."},
		"decision_kind": map[string]any{"type": "string", "description": "A reusable criterion such as storage_backend, api_shape, concurrency_model, compatibility_strategy, or rollout_strategy."},
		"risk_level":    map[string]any{"type": "string", "enum": []any{"low", "medium", "high", "critical"}},
		"options": map[string]any{
			"type":     "array",
			"minItems": 2,
			"maxItems": 4,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":          map[string]any{"type": "string", "description": "Stable semantic option id."},
					"label":       map[string]any{"type": "string", "description": "Short user-facing option label."},
					"description": map[string]any{"type": "string", "description": "Objective one-line tradeoff summary."},
					"pros":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"cons":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"recommended": map[string]any{"type": "boolean", "description": "Exactly one option must be recommended."},
				},
				"required": []any{"id", "label", "description", "recommended"},
			},
		},
		"domains":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"languages":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"evidence_refs":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Relevant repository-relative files or symbols already inspected."},
		"detector_confidence":  map[string]any{"type": "number", "minimum": 0, "maximum": 1, "description": "Confidence that this is a material fork rather than a trivial choice."},
		"task_fingerprint":     map[string]any{"type": "string", "description": "Optional stable fingerprint for idempotent retries."},
		"evidence_fingerprint": map[string]any{"type": "string", "description": "Optional stable fingerprint of the evidence supporting the fork."},
	}
	return ToolDefinition{
		Name:        "present_implementation_decision",
		Description: "Present a material implementation fork to the user, require an explicit choice (including Other), ask why the selected approach was chosen and why every listed alternative was rejected, then store the rationale in the cross-project decision journal. Use only after inspecting the relevant code and before the first edit, when 2-4 genuinely viable approaches have meaningful tradeoffs. Do not use for trivial syntax, naming, formatting, or choices already fixed by the user's requirements. Call this tool alone; continue implementation only after it returns a recorded decision. When the system prompt reports a pending decision, resume it with only that decision_id instead of reconstructing the original proposal.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": captureProperties,
			"anyOf": []any{
				map[string]any{
					"required": []any{"decision_id"},
				},
				map[string]any{
					"required": []any{"problem", "decision_kind", "options", "detector_confidence"},
				},
			},
			"additionalProperties": false,
		},
	}
}

func (t ImplementationDecisionTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t ImplementationDecisionTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ToolExecutionResult{}, err
	}
	args, err := requireToolInputObject(input, "present_implementation_decision")
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if t.ws.DecisionStore == nil {
		return implementationDecisionUnavailableResult("the decision journal is not configured"), nil
	}
	if t.ws.DecisionSession != nil {
		t.ws.DecisionSession.normalizePendingImplementationDecision()
	}
	resumeID := strings.TrimSpace(stringValue(args, "decision_id"))
	var (
		record     ImplementationDecisionRecord
		header     string
		decisionID string
	)
	if resumeID != "" {
		if !validImplementationDecisionID(resumeID) {
			return ToolExecutionResult{}, fmt.Errorf("invalid pending implementation decision id %q", resumeID)
		}
		current := (*PendingImplementationDecision)(nil)
		if t.ws.DecisionSession != nil {
			current = t.ws.DecisionSession.PendingImplementationDecision
		}
		if current == nil {
			if existing, ok, getErr := t.ws.DecisionStore.Get(resumeID); getErr != nil {
				return ToolExecutionResult{}, getErr
			} else if ok {
				return implementationDecisionRecordedResult(existing, ""), nil
			}
			return ToolExecutionResult{}, fmt.Errorf("implementation decision %s is not pending in this session", resumeID)
		}
		if current.DecisionID != resumeID {
			return ToolExecutionResult{}, fmt.Errorf("implementation decision %s is still pending; cannot resume %s", current.DecisionID, resumeID)
		}
		record = current.Record
		record.ID = current.DecisionID
		header = strings.TrimSpace(stringValue(args, "header"))
		if len(header) > 256 {
			return ToolExecutionResult{}, fmt.Errorf("implementation decision header exceeds 256 bytes")
		}
		if err := validateImplementationDecisionProposal(record); err != nil {
			return ToolExecutionResult{}, fmt.Errorf("pending implementation decision %s is invalid: %w", resumeID, err)
		}
		decisionID = resumeID
	} else {
		record, header, err = t.parseRecord(args)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		if err := validateImplementationDecisionProposal(record); err != nil {
			return ToolExecutionResult{}, err
		}
		decisionID, err = implementationDecisionIDForRecord(record)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		record.ID = decisionID
	}
	if t.ws.DecisionSession != nil && t.ws.DecisionSession.PendingImplementationDecision != nil {
		current := t.ws.DecisionSession.PendingImplementationDecision
		if current.DecisionID != decisionID {
			return ToolExecutionResult{}, fmt.Errorf("implementation decision %s is still pending; cannot present %s", current.DecisionID, decisionID)
		}
	}

	if existing, ok, getErr := t.ws.DecisionStore.Get(decisionID); getErr != nil {
		return ToolExecutionResult{}, getErr
	} else if ok {
		if clearErr := t.clearPending(decisionID); clearErr != nil {
			return ToolExecutionResult{}, clearErr
		}
		return implementationDecisionRecordedResult(existing, ""), nil
	}

	pending, err := t.preparePending(record)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if t.ws.UserInputRequests != nil {
		t.ws.UserInputRequests.MarkRequested()
	}
	if t.ws.PromptUserChoice == nil || t.ws.PromptUserText == nil {
		if err := t.savePending(pending); err != nil {
			return ToolExecutionResult{}, err
		}
		return implementationDecisionUnavailableResult("no interactive user is attached; the decision remains pending"), nil
	}

	if pending.Stage == implementationDecisionPendingChoice {
		choice, promptErr := t.ws.PromptUserChoice(implementationDecisionUserQuestion(pending.Record, header))
		if promptErr != nil {
			return ToolExecutionResult{}, promptErr
		}
		if choice.Canceled {
			if err := t.savePending(pending); err != nil {
				return ToolExecutionResult{}, err
			}
			return implementationDecisionPendingResult(decisionID, "the user canceled the approach selection"), nil
		}
		selectedID, custom, selectErr := implementationDecisionSelection(pending.Record.Options, choice)
		if selectErr != nil {
			return ToolExecutionResult{}, selectErr
		}
		pending.SelectedOptionID = selectedID
		pending.CustomSelection = custom
		pending.Stage = implementationDecisionPendingRationale
		if err := t.savePending(pending); err != nil {
			return ToolExecutionResult{}, err
		}
	}

	selectedLabel := implementationDecisionSelectedLabel(pending.Record.Options, pending.SelectedOptionID, pending.CustomSelection)
	selectedReason, canceled, err := t.promptRequiredText(UserTextQuestion{
		Header:      "Selection rationale",
		Question:    fmt.Sprintf("Why did you choose %q?", selectedLabel),
		Placeholder: "Describe the criterion or tradeoff that mattered most.",
		Required:    true,
		MaxLength:   4096,
	})
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if canceled {
		return implementationDecisionPendingResult(decisionID, "the user canceled rationale capture"), nil
	}

	rejections := make([]ImplementationDecisionRejection, 0, len(pending.Record.Options))
	for _, option := range pending.Record.Options {
		if option.ID == pending.SelectedOptionID {
			continue
		}
		reason, wasCanceled, promptErr := t.promptRequiredText(UserTextQuestion{
			Header:      "Alternative rationale",
			Question:    fmt.Sprintf("Why did you not choose %q?", option.Label),
			Placeholder: "Describe the drawback or mismatch that ruled it out.",
			Required:    true,
			MaxLength:   4096,
		})
		if promptErr != nil {
			return ToolExecutionResult{}, promptErr
		}
		if wasCanceled {
			return implementationDecisionPendingResult(decisionID, "the user canceled alternative rationale capture"), nil
		}
		rejections = append(rejections, ImplementationDecisionRejection{
			OptionID: option.ID,
			Reason:   reason,
			Source:   "user",
		})
	}

	record = pending.Record
	record.SelectedOptionID = pending.SelectedOptionID
	record.CustomSelection = pending.CustomSelection
	record.SelectionReasonRaw = selectedReason
	record.RejectedReasons = rejections
	record.Status = implementationDecisionStatusCompleted
	record.Provenance.Selection = "user"
	record.Provenance.Rationale = "user"
	record.Provenance.Summary = "runtime"
	record.SummarySentence = renderImplementationDecisionSummary(record)
	stored, err := t.ws.DecisionStore.Put(record)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if err := t.clearPending(decisionID); err != nil {
		return ToolExecutionResult{}, err
	}
	profileWarning := ""
	if t.ws.DecisionProfileStore != nil {
		if _, rebuildErr := t.ws.DecisionProfileStore.Rebuild(t.ws.DecisionStore); rebuildErr != nil {
			profileWarning = rebuildErr.Error()
		}
	}
	return implementationDecisionRecordedResult(stored, profileWarning), nil
}

func (t ImplementationDecisionTool) parseRecord(input any) (ImplementationDecisionRecord, string, error) {
	args, err := requireToolInputObject(input, "present_implementation_decision")
	if err != nil {
		return ImplementationDecisionRecord{}, "", err
	}
	problem := strings.TrimSpace(stringValue(args, "problem"))
	decisionKind := strings.ToLower(strings.TrimSpace(stringValue(args, "decision_kind")))
	if problem == "" || decisionKind == "" {
		return ImplementationDecisionRecord{}, "", fmt.Errorf("present_implementation_decision requires non-empty problem and decision_kind")
	}
	options, recommended, err := parseImplementationDecisionToolOptions(args["options"])
	if err != nil {
		return ImplementationDecisionRecord{}, "", err
	}
	confidence, ok := implementationDecisionFloat(args["detector_confidence"])
	if !ok || math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
		return ImplementationDecisionRecord{}, "", fmt.Errorf("present_implementation_decision requires detector_confidence between 0 and 1")
	}
	if confidence < 0.65 {
		return ImplementationDecisionRecord{}, "", fmt.Errorf("present_implementation_decision is reserved for material forks; detector_confidence %.2f is below 0.65", confidence)
	}
	workspace := strings.TrimSpace(firstNonBlankString(t.ws.BaseRoot, t.ws.Root))
	projectID, projectAlias, workspaceHash := implementationDecisionProjectIdentity(workspace)
	session := t.ws.DecisionSession
	record := ImplementationDecisionRecord{
		ProjectID:           projectID,
		ProjectAlias:        projectAlias,
		Workspace:           workspace,
		WorkspaceHash:       workspaceHash,
		Domains:             implementationDecisionStringList(args["domains"], 32),
		Languages:           implementationDecisionStringList(args["languages"], 32),
		DecisionKind:        decisionKind,
		RiskLevel:           strings.ToLower(strings.TrimSpace(stringValue(args, "risk_level"))),
		Problem:             problem,
		Options:             options,
		RecommendedOptionID: recommended,
		EvidenceRefs:        implementationDecisionStringList(args["evidence_refs"], 64),
		DetectorConfidence:  confidence,
		Status:              implementationDecisionStatusCompleted,
		Provenance: ImplementationDecisionProvenance{
			Options: "model",
		},
	}
	if session != nil {
		record.SessionID = strings.TrimSpace(session.ID)
		record.FeatureID = strings.TrimSpace(session.ActiveFeatureID)
		record.GoalID = strings.TrimSpace(session.ActiveGoalID)
		if session.ActiveEditLoop != nil {
			record.EditLoopID = strings.TrimSpace(session.ActiveEditLoop.ID)
		}
	}
	record.TaskFingerprint = strings.TrimSpace(stringValue(args, "task_fingerprint"))
	if record.TaskFingerprint == "" {
		record.TaskFingerprint = "task-" + shortStableID(record.SessionID+"\x00"+record.Problem)
	}
	record.EvidenceFingerprint = strings.TrimSpace(stringValue(args, "evidence_fingerprint"))
	if record.EvidenceFingerprint == "" {
		record.EvidenceFingerprint = implementationDecisionOptionsFingerprint(options, record.EvidenceRefs)
	}
	header := strings.TrimSpace(stringValue(args, "header"))
	if len(header) > 256 {
		return ImplementationDecisionRecord{}, "", fmt.Errorf("implementation decision header exceeds 256 bytes")
	}
	return record, header, nil
}

func validateImplementationDecisionProposal(record ImplementationDecisionRecord) error {
	if strings.TrimSpace(record.Problem) == "" || strings.TrimSpace(record.DecisionKind) == "" {
		return fmt.Errorf("implementation decision requires non-empty problem and decision_kind")
	}
	if len(record.Problem) > 4096 {
		return fmt.Errorf("implementation decision problem exceeds 4096 bytes")
	}
	if len(record.DecisionKind) > 128 {
		return fmt.Errorf("implementation decision kind exceeds 128 bytes")
	}
	if record.RiskLevel != "" {
		switch record.RiskLevel {
		case "low", "medium", "high", "critical":
		default:
			return fmt.Errorf("implementation decision risk_level %q is invalid", record.RiskLevel)
		}
	}
	if len(record.Domains) > 32 || len(record.Languages) > 32 || len(record.EvidenceRefs) > 64 {
		return fmt.Errorf("implementation decision proposal list exceeds the storage limit")
	}
	if math.IsNaN(record.DetectorConfidence) || math.IsInf(record.DetectorConfidence, 0) || record.DetectorConfidence < 0.65 || record.DetectorConfidence > 1 {
		return fmt.Errorf("implementation decision detector_confidence must be finite and between 0.65 and 1")
	}
	if len(record.Options) < 2 || len(record.Options) > 4 {
		return fmt.Errorf("implementation decision requires 2 to 4 options")
	}
	for _, value := range append(append([]string{}, record.Domains...), record.Languages...) {
		if len(value) > 512 {
			return fmt.Errorf("implementation decision proposal list item exceeds the storage limit")
		}
	}
	for _, value := range record.EvidenceRefs {
		if len(value) > 2048 {
			return fmt.Errorf("implementation decision evidence reference exceeds the storage limit")
		}
	}
	for label, value := range map[string]string{
		"decision_kind":        record.DecisionKind,
		"task_fingerprint":     record.TaskFingerprint,
		"evidence_fingerprint": record.EvidenceFingerprint,
	} {
		if len(value) > 1024 {
			return fmt.Errorf("implementation decision %s exceeds the storage limit", label)
		}
		if implementationDecisionIdentifierContainsSensitiveText(value) {
			return fmt.Errorf("implementation decision %s must not contain sensitive material", label)
		}
	}
	seenIDs := map[string]bool{}
	seenLabels := map[string]bool{}
	recommended := ""
	for _, option := range record.Options {
		if len(option.ID) > 128 || len(option.Label) > 256 || len(option.Description) > 2048 || len(option.Pros) > 16 || len(option.Cons) > 16 {
			return fmt.Errorf("implementation decision option exceeds the storage limit")
		}
		if !validImplementationDecisionOptionID(option.ID) || strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Description) == "" {
			return fmt.Errorf("implementation decision options require a valid id, label, and objective description")
		}
		if implementationDecisionIdentifierContainsSensitiveText(option.ID) {
			return fmt.Errorf("implementation decision option id must not contain sensitive material")
		}
		labelKey := strings.ToLower(strings.TrimSpace(option.Label))
		if seenIDs[option.ID] || seenLabels[labelKey] {
			return fmt.Errorf("implementation decision option ids and labels must be unique")
		}
		seenIDs[option.ID] = true
		seenLabels[labelKey] = true
		if option.Recommended {
			if recommended != "" {
				return fmt.Errorf("implementation decision requires exactly one recommended option")
			}
			recommended = option.ID
		}
		for _, value := range append(append([]string{}, option.Pros...), option.Cons...) {
			if len(value) > 1024 {
				return fmt.Errorf("implementation decision option detail exceeds the storage limit")
			}
		}
	}
	if recommended == "" || record.RecommendedOptionID != recommended {
		return fmt.Errorf("implementation decision requires exactly one consistent recommended option")
	}
	return nil
}

func implementationDecisionIdentifierContainsSensitiveText(value string) bool {
	_, report := redactSensitiveText(strings.TrimSpace(value))
	return report.Redacted
}

func (t ImplementationDecisionTool) preparePending(record ImplementationDecisionRecord) (*PendingImplementationDecision, error) {
	session := t.ws.DecisionSession
	if session != nil && session.PendingImplementationDecision != nil {
		current := session.PendingImplementationDecision
		if current.DecisionID != record.ID {
			return nil, fmt.Errorf("implementation decision %s is still pending; complete it before presenting %s", current.DecisionID, record.ID)
		}
		if current.Stage != implementationDecisionPendingChoice && current.Stage != implementationDecisionPendingRationale {
			return nil, fmt.Errorf("implementation decision %s has invalid pending stage %q", current.DecisionID, current.Stage)
		}
		return current, nil
	}
	pending := &PendingImplementationDecision{
		DecisionID: record.ID,
		Stage:      implementationDecisionPendingChoice,
		Record:     record,
	}
	if err := t.savePending(pending); err != nil {
		return nil, err
	}
	return pending, nil
}

func (t ImplementationDecisionTool) savePending(pending *PendingImplementationDecision) error {
	if t.ws.DecisionSession == nil {
		return nil
	}
	redactPendingImplementationDecision(pending)
	t.ws.DecisionSession.PendingImplementationDecision = pending
	if t.ws.DecisionSessionStore == nil {
		return nil
	}
	return t.ws.DecisionSessionStore.Save(t.ws.DecisionSession)
}

func redactPendingImplementationDecision(pending *PendingImplementationDecision) {
	if pending == nil {
		return
	}
	record := pending.Record
	identifierReport := ReviewRedactionReport{Status: "clean"}
	var currentReport ReviewRedactionReport
	pending.DecisionID, currentReport = redactImplementationDecisionStableIdentifier(pending.DecisionID, "decision-redacted")
	identifierReport = mergeReviewRedactionReports(identifierReport, currentReport)
	if !validImplementationDecisionID(pending.DecisionID) {
		seed := strings.Join([]string{
			pending.DecisionID,
			pending.Record.ID,
			pending.Record.Problem,
			pending.Record.TaskFingerprint,
			pending.Record.EvidenceFingerprint,
		}, "\x00")
		digest := sha256.Sum256([]byte(seed))
		pending.DecisionID = "decision-recovered-" + hex.EncodeToString(digest[:8])
	}
	record.ID = pending.DecisionID
	for _, field := range []*string{
		&record.SessionID,
		&record.FeatureID,
		&record.GoalID,
		&record.EditLoopID,
		&record.ProjectID,
		&record.WorkspaceHash,
		&record.TaskFingerprint,
		&record.EvidenceFingerprint,
		&record.DecisionKind,
	} {
		*field, currentReport = redactSensitiveText(*field)
		identifierReport = mergeReviewRedactionReports(identifierReport, currentReport)
	}
	if _, report := redactSensitiveText(record.RiskLevel); report.Redacted {
		identifierReport = mergeReviewRedactionReports(identifierReport, report)
		record.RiskLevel = ""
	}
	if math.IsNaN(record.DetectorConfidence) || math.IsInf(record.DetectorConfidence, 0) {
		record.DetectorConfidence = 0
	}
	optionIDMap := make(map[string]string, len(record.Options))
	for index := range record.Options {
		originalID := record.Options[index].ID
		redactedID, report := redactImplementationDecisionStableIdentifier(originalID, fmt.Sprintf("option-redacted-%d", index+1))
		identifierReport = mergeReviewRedactionReports(identifierReport, report)
		record.Options[index].ID = redactedID
		if !validImplementationDecisionSource(record.Options[index].Source, true) {
			record.Options[index].Source = "model"
		}
		optionIDMap[originalID] = redactedID
	}
	if redactedID, ok := optionIDMap[record.RecommendedOptionID]; ok {
		record.RecommendedOptionID = redactedID
	} else {
		record.RecommendedOptionID, currentReport = redactImplementationDecisionStableIdentifier(record.RecommendedOptionID, "option-redacted-recommended")
		identifierReport = mergeReviewRedactionReports(identifierReport, currentReport)
	}
	if redactedID, ok := optionIDMap[pending.SelectedOptionID]; ok {
		pending.SelectedOptionID = redactedID
	}
	record.CustomSelection = pending.CustomSelection
	record = redactImplementationDecisionRecord(record)
	record.Redaction.Redacted = record.Redaction.Redacted || identifierReport.Redacted
	record.Redaction.Patterns = uniqueStrings(append(record.Redaction.Patterns, identifierReport.Patterns...))
	pending.CustomSelection = record.CustomSelection
	record.SelectedOptionID = ""
	record.CustomSelection = ""
	record.SelectionReasonRaw = ""
	record.RejectedReasons = nil
	record.SummarySentence = ""
	record.Status = implementationDecisionStatusCompleted
	record.DeletedAt = nil
	record.Provenance.Options = "model"
	record.Provenance.Selection = ""
	record.Provenance.Rationale = ""
	record.Provenance.Summary = ""
	pending.Record = record
}

func redactImplementationDecisionStableIdentifier(value string, prefix string) (string, ReviewRedactionReport) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ReviewRedactionReport{Status: "clean"}
	}
	_, report := redactSensitiveText(value)
	if !report.Redacted {
		return value, report
	}
	prefix = strings.Trim(strings.TrimSpace(prefix), "-_.")
	if prefix == "" {
		prefix = "redacted"
	}
	digest := sha256.Sum256([]byte(value))
	return prefix + "-" + hex.EncodeToString(digest[:8]), report
}

func (t ImplementationDecisionTool) clearPending(decisionID string) error {
	session := t.ws.DecisionSession
	if session == nil || session.PendingImplementationDecision == nil {
		return nil
	}
	if decisionID != "" && session.PendingImplementationDecision.DecisionID != decisionID {
		return nil
	}
	session.PendingImplementationDecision = nil
	if t.ws.DecisionSessionStore == nil {
		return nil
	}
	return t.ws.DecisionSessionStore.Save(session)
}

func (t ImplementationDecisionTool) promptRequiredText(question UserTextQuestion) (string, bool, error) {
	result, err := t.ws.PromptUserText(question)
	if err != nil {
		return "", false, err
	}
	if result.Canceled {
		return "", true, nil
	}
	text := strings.TrimSpace(result.Text)
	if question.Required && text == "" {
		return "", false, fmt.Errorf("a rationale answer is required")
	}
	if question.MaxLength > 0 && len(text) > question.MaxLength {
		return "", false, fmt.Errorf("rationale exceeds %d bytes", question.MaxLength)
	}
	return text, false, nil
}

func parseImplementationDecisionToolOptions(raw any) ([]ImplementationDecisionOption, string, error) {
	items, ok := raw.([]any)
	if !ok || len(items) < 2 || len(items) > 4 {
		return nil, "", fmt.Errorf("present_implementation_decision requires 2 to 4 options")
	}
	options := make([]ImplementationDecisionOption, 0, len(items))
	seenIDs := map[string]bool{}
	seenLabels := map[string]bool{}
	recommended := ""
	for index, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("implementation decision option %d must be an object", index+1)
		}
		id := strings.TrimSpace(stringValue(object, "id"))
		label := strings.TrimSpace(stringValue(object, "label"))
		description := strings.TrimSpace(stringValue(object, "description"))
		if !validImplementationDecisionOptionID(id) || label == "" || description == "" {
			return nil, "", fmt.Errorf("implementation decision option %d requires a valid id, label, and objective description", index+1)
		}
		labelKey := strings.ToLower(label)
		if seenIDs[id] || seenLabels[labelKey] {
			return nil, "", fmt.Errorf("implementation decision option ids and labels must be unique")
		}
		seenIDs[id] = true
		seenLabels[labelKey] = true
		isRecommended, _ := object["recommended"].(bool)
		if isRecommended {
			if recommended != "" {
				return nil, "", fmt.Errorf("implementation decision requires exactly one recommended option")
			}
			recommended = id
		}
		options = append(options, ImplementationDecisionOption{
			ID:          id,
			Label:       label,
			Description: description,
			Pros:        implementationDecisionStringList(object["pros"], 16),
			Cons:        implementationDecisionStringList(object["cons"], 16),
			Recommended: isRecommended,
			Order:       index + 1,
			Source:      "model",
		})
	}
	if recommended == "" {
		return nil, "", fmt.Errorf("implementation decision requires exactly one recommended option")
	}
	return options, recommended, nil
}

func validImplementationDecisionOptionID(id string) bool {
	if id == "" || len(id) > 128 || id == implementationDecisionCustomOptionID {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func implementationDecisionUserQuestion(record ImplementationDecisionRecord, header string) UserQuestion {
	options := make([]UserQuestionOption, 0, len(record.Options))
	for _, option := range record.Options {
		description := option.Description
		if len(option.Pros) > 0 {
			description += " | Pros: " + strings.Join(option.Pros, ", ")
		}
		if len(option.Cons) > 0 {
			description += " | Cons: " + strings.Join(option.Cons, ", ")
		}
		options = append(options, UserQuestionOption{
			Label:       option.Label,
			Description: description,
			Recommended: option.Recommended,
		})
	}
	if header == "" {
		header = "Implementation decision"
	}
	return UserQuestion{
		Question:        record.Problem,
		Header:          header,
		Options:         options,
		AllowCustom:     true,
		RequireExplicit: true,
	}
}

func implementationDecisionSelection(options []ImplementationDecisionOption, result UserQuestionResult) (string, string, error) {
	if custom := strings.TrimSpace(result.Custom); custom != "" {
		if len(result.Selected) > 0 {
			return "", "", fmt.Errorf("implementation decision cannot contain both a listed and custom selection")
		}
		if len(custom) > 1024 {
			return "", "", fmt.Errorf("custom implementation decision exceeds 1024 bytes")
		}
		for _, option := range options {
			if strings.EqualFold(custom, strings.TrimSpace(option.Label)) {
				return option.ID, "", nil
			}
		}
		return "", custom, nil
	}
	if len(result.Selected) != 1 {
		return "", "", fmt.Errorf("implementation decision requires exactly one selected option")
	}
	label := strings.TrimSpace(result.Selected[0])
	for _, option := range options {
		if label == option.Label {
			return option.ID, "", nil
		}
	}
	return "", "", fmt.Errorf("selected implementation decision option %q is not present", label)
}

func implementationDecisionSelectedLabel(options []ImplementationDecisionOption, selectedID string, custom string) string {
	if strings.TrimSpace(custom) != "" {
		return strings.TrimSpace(custom)
	}
	for _, option := range options {
		if option.ID == selectedID {
			return option.Label
		}
	}
	return selectedID
}

func implementationDecisionStringList(raw any, limit int) []string {
	items, _ := raw.([]any)
	values := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		values = append(values, value)
		if limit > 0 && len(values) >= limit {
			break
		}
	}
	return uniqueStrings(values)
}

func implementationDecisionFloat(raw any) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}

func implementationDecisionProjectIdentity(root string) (string, string, string) {
	clean := strings.TrimSpace(root)
	if clean != "" {
		if absolute, err := filepath.Abs(clean); err == nil {
			clean = absolute
		}
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			clean = resolved
		}
		clean = filepath.Clean(clean)
	}
	canonical := filepath.ToSlash(clean)
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	digest := sha256.Sum256([]byte(canonical))
	hash := hex.EncodeToString(digest[:])
	alias := filepath.Base(clean)
	if alias == "." || alias == string(filepath.Separator) {
		alias = "workspace"
	}
	return "project-" + hash[:16], alias, hash
}

func implementationDecisionOptionsFingerprint(options []ImplementationDecisionOption, refs []string) string {
	parts := make([]string, 0, len(options)+len(refs))
	for _, option := range options {
		parts = append(parts, strings.ToLower(strings.TrimSpace(option.ID))+"="+strings.ToLower(strings.TrimSpace(option.Label)))
	}
	sort.Strings(parts)
	normalizedRefs := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref = strings.TrimSpace(ref); ref != "" {
			normalizedRefs = append(normalizedRefs, ref)
		}
	}
	sort.Strings(normalizedRefs)
	parts = append(parts, normalizedRefs...)
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "evidence-" + hex.EncodeToString(digest[:16])
}

func implementationDecisionUnavailableResult(reason string) ToolExecutionResult {
	message := "Implementation decision capture is unavailable: " + strings.TrimSpace(reason) + "."
	return ToolExecutionResult{
		DisplayText: message,
		ModelText:   message + " Do not edit until the user completes the pending decision in an interactive session.",
		Meta: map[string]any{
			"success":             false,
			"decision_checkpoint": true,
			"requires_user_input": true,
			"pending":             true,
		},
	}
}

func implementationDecisionPendingResult(decisionID string, reason string) ToolExecutionResult {
	message := fmt.Sprintf("Implementation decision %s remains pending: %s.", decisionID, strings.TrimSpace(reason))
	return ToolExecutionResult{
		DisplayText: message,
		ModelText:   message + " Stop before editing and wait for the user to resume the decision.",
		Meta: map[string]any{
			"success":             false,
			"decision_checkpoint": true,
			"decision_id":         decisionID,
			"requires_user_input": true,
			"pending":             true,
		},
	}
}

func implementationDecisionRecordedResult(record ImplementationDecisionRecord, profileWarning string) ToolExecutionResult {
	selected := implementationDecisionSelectedLabel(record.Options, record.SelectedOptionID, record.CustomSelection)
	message := fmt.Sprintf("Implementation decision recorded: %s (id=%s).", selected, record.ID)
	meta := map[string]any{
		"success":             true,
		"decision_checkpoint": true,
		"decision_id":         record.ID,
		"revision":            record.Revision,
		"selected_option_id":  record.SelectedOptionID,
		"custom_selection":    record.CustomSelection,
		"summary":             record.SummarySentence,
		"output_bounded":      true,
	}
	if strings.TrimSpace(profileWarning) != "" {
		meta["profile_rebuild_warning"] = profileWarning
		message += " Preference profile rebuild warning: " + profileWarning
	}
	return ToolExecutionResult{
		DisplayText: message,
		ModelText:   message + " Continue implementation using this choice. Do not ask the same decision again. Rationale: " + record.SummarySentence,
		Meta:        meta,
	}
}

func toolMetaImplementationDecisionCheckpoint(meta map[string]any) bool {
	if len(meta) == 0 {
		return false
	}
	checkpoint, _ := meta["decision_checkpoint"].(bool)
	return checkpoint
}

func toolMetaImplementationDecisionRequiresUserInput(meta map[string]any) bool {
	if !toolMetaImplementationDecisionCheckpoint(meta) {
		return false
	}
	pending, _ := meta["pending"].(bool)
	requiresInput, _ := meta["requires_user_input"].(bool)
	return pending || requiresInput
}

func implementationDecisionToolCallIndex(calls []ToolCall) int {
	for index, call := range calls {
		if strings.TrimSpace(call.Name) == "present_implementation_decision" {
			return index
		}
	}
	return -1
}

func pendingImplementationDecisionBlocksToolCall(session *Session, call ToolCall, registry *ToolRegistry) bool {
	if session == nil || session.PendingImplementationDecision == nil {
		return false
	}
	if strings.TrimSpace(call.Name) == "present_implementation_decision" {
		return false
	}
	if registry != nil {
		// While a decision is pending, fail closed for every registered tool
		// that does not explicitly declare itself read-only. This covers custom
		// and MCP mutations whose names are not part of the built-in edit list.
		return !registry.ToolCallReadOnly(call.Name)
	}
	return toolContractCallMutatesWorkspace(call, registry)
}

func renderPendingImplementationDecisionPrompt(pending *PendingImplementationDecision) string {
	if pending == nil {
		return ""
	}
	selected := implementationDecisionSelectedLabel(pending.Record.Options, pending.SelectedOptionID, pending.CustomSelection)
	var b strings.Builder
	fmt.Fprintf(&b, "- Decision ID: %s\n", pending.DecisionID)
	fmt.Fprintf(&b, "- Stage: %s\n", pending.Stage)
	fmt.Fprintf(&b, "- Problem: %s\n", compactPromptSection(pending.Record.Problem, 400))
	if selected != "" {
		fmt.Fprintf(&b, "- Selected approach: %s\n", compactPromptSection(selected, 160))
	}
	fmt.Fprintf(&b, "- Resume by calling present_implementation_decision with only `{\"decision_id\":%q}` before any edit. The persisted pending record is canonical; do not reconstruct the original proposal.\n", pending.DecisionID)
	return strings.TrimSpace(b.String())
}

func sanitizeImplementationDecisionMessageForPersistence(message Message) Message {
	if len(message.ToolCalls) > 0 {
		calls := append([]ToolCall(nil), message.ToolCalls...)
		changed := false
		for index := range calls {
			redacted := sanitizeImplementationDecisionToolCallForPersistence(calls[index])
			if redacted.Arguments == calls[index].Arguments {
				continue
			}
			calls[index] = redacted
			changed = true
		}
		if changed {
			message.ToolCalls = calls
		}
	}
	if strings.TrimSpace(message.ToolName) == "present_implementation_decision" {
		message.Text, _ = redactSensitiveText(message.Text)
		message.SourceText, _ = redactSensitiveText(message.SourceText)
		message.ReasoningContent, _ = redactSensitiveText(message.ReasoningContent)
		if len(message.ToolContentItems) > 0 {
			items := append([]ToolContentItem(nil), message.ToolContentItems...)
			for index := range items {
				items[index].Text, _ = redactSensitiveText(items[index].Text)
			}
			message.ToolContentItems = items
		}
		if len(message.ToolMeta) > 0 {
			if redacted, ok := redactImplementationDecisionArgumentValue(message.ToolMeta).(map[string]any); ok {
				message.ToolMeta = redacted
			}
		}
	}
	return message
}

func sanitizeImplementationDecisionSessionForPersistence(session *Session) {
	if session == nil {
		return
	}
	session.normalizePendingImplementationDecision()
	for index := range session.Messages {
		session.Messages[index] = sanitizeImplementationDecisionMessageForPersistence(session.Messages[index])
	}
	if session.LastTurnRuntimeState != nil {
		for index := range session.LastTurnRuntimeState.Interventions {
			intervention := &session.LastTurnRuntimeState.Interventions[index]
			hasDecisionCall := false
			for callIndex := range intervention.ToolCalls {
				if strings.TrimSpace(intervention.ToolCalls[callIndex].Name) == "present_implementation_decision" {
					hasDecisionCall = true
				}
				intervention.ToolCalls[callIndex] = sanitizeImplementationDecisionToolCallForPersistence(intervention.ToolCalls[callIndex])
			}
			if hasDecisionCall {
				intervention.Reason, _ = redactSensitiveText(intervention.Reason)
				intervention.Guidance, _ = redactSensitiveText(intervention.Guidance)
				intervention.StopReason, _ = redactSensitiveText(intervention.StopReason)
			}
		}
	}
	for index := range session.ConversationEvents {
		event := &session.ConversationEvents[index]
		if !strings.EqualFold(strings.TrimSpace(event.Entities["tool"]), "present_implementation_decision") {
			continue
		}
		event.Summary, _ = redactSensitiveText(event.Summary)
		event.Raw, _ = redactSensitiveText(event.Raw)
		for key, value := range event.Entities {
			event.Entities[key], _ = redactSensitiveText(value)
		}
		if len(event.Metadata) > 0 {
			if redacted, ok := redactImplementationDecisionArgumentValue(event.Metadata).(map[string]any); ok {
				event.Metadata = redacted
			}
		}
	}
}

func sanitizeImplementationDecisionSessionBytesForPersistence(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	raw := string(data)
	if !strings.Contains(raw, "present_implementation_decision") && !strings.Contains(raw, "pending_implementation_decision") {
		return data
	}
	redacted, report := redactSensitiveText(raw)
	if !report.Redacted {
		return data
	}
	candidate := []byte(redacted)
	if !json.Valid(candidate) {
		return data
	}
	return candidate
}

func sanitizeImplementationDecisionToolCallForPersistence(call ToolCall) ToolCall {
	if strings.TrimSpace(call.Name) == "present_implementation_decision" {
		call.Arguments = redactImplementationDecisionArgumentsForPersistence(call.Arguments)
	}
	return call
}

func redactImplementationDecisionArgumentsForPersistence(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	var payload any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		redacted, _ := redactSensitiveText(raw)
		return redacted
	}
	payload = redactImplementationDecisionArgumentValue(payload)
	data, err := json.Marshal(payload)
	if err != nil {
		redacted, _ := redactSensitiveText(raw)
		return redacted
	}
	return string(data)
}

func redactImplementationDecisionArgumentValue(value any) any {
	switch current := value.(type) {
	case string:
		redacted, _ := redactSensitiveText(current)
		return redacted
	case []any:
		redacted := make([]any, len(current))
		for index := range current {
			redacted[index] = redactImplementationDecisionArgumentValue(current[index])
		}
		return redacted
	case map[string]any:
		redacted := make(map[string]any, len(current))
		for key, item := range current {
			redactedKey, _ := redactSensitiveText(key)
			redacted[redactedKey] = redactImplementationDecisionArgumentValue(item)
		}
		return redacted
	default:
		return value
	}
}
