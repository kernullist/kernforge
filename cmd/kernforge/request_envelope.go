package main

import (
	"fmt"
	"strings"
)

type RequestClass string

const (
	RequestClassQuestion  RequestClass = "question"
	RequestClassReview    RequestClass = "review"
	RequestClassPlan      RequestClass = "plan"
	RequestClassEdit      RequestClass = "edit"
	RequestClassDocument  RequestClass = "document"
	RequestClassResearch  RequestClass = "research"
	RequestClassGit       RequestClass = "git"
	RequestClassAmbiguous RequestClass = "ambiguous"
)

type ActionBoundary string

const (
	ActionBoundaryReadOnly ActionBoundary = "read_only"
	ActionBoundaryMayEdit  ActionBoundary = "may_edit"
	ActionBoundaryMustEdit ActionBoundary = "must_edit"
	ActionBoundaryMayGit   ActionBoundary = "may_git"
	ActionBoundaryNoCommit ActionBoundary = "no_commit"
)

type RequestEvidence struct {
	Source string `json:"source,omitempty"`
	Signal string `json:"signal,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type RequestEnvelope struct {
	ExternalUserText          string            `json:"external_user_text,omitempty"`
	Intent                    TurnIntent        `json:"intent,omitempty"`
	Classes                   []RequestClass    `json:"classes,omitempty"`
	PrimaryClass              RequestClass      `json:"primary_class,omitempty"`
	Boundary                  ActionBoundary    `json:"boundary,omitempty"`
	AllowsToolExecution       bool              `json:"allows_tool_execution,omitempty"`
	AllowsFileMutation        bool              `json:"allows_file_mutation,omitempty"`
	AllowsGitMutation         bool              `json:"allows_git_mutation,omitempty"`
	AllowsWebResearch         bool              `json:"allows_web_research,omitempty"`
	RequiresFreshExternalInfo bool              `json:"requires_fresh_external_info,omitempty"`
	RequiresVerification      bool              `json:"requires_verification,omitempty"`
	ReadOnlyAnalysis          bool              `json:"read_only_analysis,omitempty"`
	ExplicitEditRequest       bool              `json:"explicit_edit_request,omitempty"`
	ExplicitGitRequest        bool              `json:"explicit_git_request,omitempty"`
	DocumentAuthoring         bool              `json:"document_authoring,omitempty"`
	GoalPromptDraftOnly       bool              `json:"goal_prompt_draft_only,omitempty"`
	ReviewOnlyModeRequest     bool              `json:"review_only_mode_request,omitempty"`
	Confidence                float64           `json:"confidence,omitempty"`
	Evidence                  []RequestEvidence `json:"evidence,omitempty"`
	Warnings                  []string          `json:"warnings,omitempty"`
	ReviewRequestClass        string            `json:"review_request_class,omitempty"`
	ReviewLifecycleKind       string            `json:"review_lifecycle_kind,omitempty"`
	ReviewRequestClassReason  string            `json:"review_request_class_reason,omitempty"`
}

func buildRequestEnvelope(userText string) RequestEnvelope {
	base := strings.TrimSpace(baseUserQueryText(userText))
	if base == "" {
		base = strings.TrimSpace(userText)
	}
	intent := classifyTurnIntent(base)
	mode := classifyAgentRequestModeHeuristics(base, intent)
	intent = mode.Intent
	lower := strings.ToLower(strings.TrimSpace(base))
	documentAuthoring := looksLikeDocumentAuthoringIntent(base) || looksLikeReviewArtifactAuthoringRequest(base)
	goalPromptDraftOnly := looksLikeGoalPromptDraftOnlyRequest(base)
	explicitGit := looksLikeExplicitGitIntent(base)
	if requestLooksLikeGitOnlyMutation(base) {
		mode.ExplicitEditRequest = false
		mode.ReadOnlyAnalysis = false
		if mode.Intent == TurnIntentEditCode {
			mode.Intent = TurnIntentRunCommand
			intent = mode.Intent
		}
	}
	freshExternal := shouldPrioritizeWebResearchInSystemPrompt(lower)
	explicitWebResearch := requestExplicitlyAsksForWebResearch(lower)
	reviewDecision := classifyAcceptanceContractRequestClassDecision(base, intent, mode.ReadOnlyAnalysis, mode.ExplicitEditRequest)
	reviewDecision = applyReviewLifecycleKindToDecision(reviewDecision, base, intent, "", acceptanceContractMode(intent, mode.ReadOnlyAnalysis, mode.ExplicitEditRequest))
	reviewDecision.Normalize()
	// When the review classifier resolves a document deliverable and there is no
	// imperative source-edit command, treat the request as document authoring so
	// it never renders as a must_edit code-edit request. This covers document
	// phrasing the authoring-intent heuristic misses (for example save-to-file).
	if normalizeReviewRequestClass(reviewDecision.RequestClass) == reviewRequestClassDocumentArtifact && !looksLikeImperativeSourceEditCommand(base) {
		documentAuthoring = true
		mode.ExplicitEditRequest = false
	}
	envelope := RequestEnvelope{
		ExternalUserText:          base,
		Intent:                    intent,
		AllowsToolExecution:       true,
		AllowsWebResearch:         explicitWebResearch || freshExternal,
		RequiresFreshExternalInfo: freshExternal,
		ReadOnlyAnalysis:          mode.ReadOnlyAnalysis,
		ExplicitEditRequest:       mode.ExplicitEditRequest,
		ExplicitGitRequest:        explicitGit,
		DocumentAuthoring:         documentAuthoring,
		GoalPromptDraftOnly:       goalPromptDraftOnly,
		ReviewOnlyModeRequest:     mode.ReviewOnlyModeRequest,
		Confidence:                reviewDecision.Confidence,
		ReviewRequestClass:        reviewDecision.RequestClass,
		ReviewLifecycleKind:       reviewDecision.LifecycleKind,
		ReviewRequestClassReason:  reviewDecision.Reason,
	}
	envelope.Evidence = append(envelope.Evidence, RequestEvidence{Source: "turn_intent", Signal: string(intent)})
	if mode.ReadOnlyAnalysis {
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{Source: "agent_request_mode", Signal: "read_only_analysis"})
	}
	if mode.ExplicitEditRequest {
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{Source: "agent_request_mode", Signal: "explicit_edit_request"})
	}
	if mode.ReviewOnlyModeRequest {
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{Source: "agent_request_mode", Signal: "review_only_mode_request"})
	}
	if explicitGit {
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{Source: "analysis_context", Signal: "explicit_git_request"})
	}
	if documentAuthoring {
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{Source: "analysis_context", Signal: "document_authoring"})
	}
	if goalPromptDraftOnly {
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{Source: "analysis_context", Signal: "goal_prompt_draft_only"})
	}
	if freshExternal {
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{Source: "web_research", Signal: "fresh_external_info_required"})
	}
	for _, signal := range reviewDecision.Signals {
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{Source: "review_request_class", Signal: signal})
	}
	envelope.Warnings = append(envelope.Warnings, reviewDecision.AmbiguityWarnings...)
	envelope.Classes = requestEnvelopeClasses(envelope, reviewDecision)
	envelope.PrimaryClass = requestEnvelopePrimaryClass(envelope, reviewDecision)
	envelope.applyPolicy()
	envelope.Normalize()
	return envelope
}

func classifyAgentRequestModeHeuristics(userText string, intent TurnIntent) agentRequestMode {
	documentAuthoring := looksLikeDocumentAuthoringIntent(userText) || looksLikeReviewArtifactAuthoringRequest(userText)
	imperativeSourceEdit := looksLikeImperativeSourceEditCommand(userText)
	documentArtifactEditRequest := documentAuthoring && imperativeSourceEdit
	// A document-authoring request without an imperative source-edit command is a
	// writable artifact request whose deliverable is the document, not a source
	// edit. Such a request must not be treated as an explicit source-edit request
	// and must not be swallowed into read-only analysis even when source edits are
	// negated (the negation targets source, not the document deliverable).
	documentAuthoringOnly := documentAuthoring && !imperativeSourceEdit
	repairActionNegated := hasRepairActionNegation(userText) && !documentArtifactEditRequest
	explicitEditRequest := looksLikeExplicitEditIntent(userText) && !repairActionNegated && !documentAuthoringOnly
	reviewOnlyModeRequest := looksLikeReviewOnlyModeIntent(userText) && !explicitEditRequest && !documentAuthoringOnly
	readOnlyAnalysis := !documentAuthoringOnly && (repairActionNegated || intent == TurnIntentReviewCode || prefersReadOnlyAnalysisIntent(userText) || reviewOnlyModeRequest || looksLikePlanOrDirectionOnlyRequest(userText))
	if readOnlyAnalysis && intent == TurnIntentEditCode {
		intent = TurnIntentGeneral
	}
	return agentRequestMode{
		Intent:                intent,
		ReviewOnlyModeRequest: reviewOnlyModeRequest,
		ReadOnlyAnalysis:      readOnlyAnalysis,
		ExplicitEditRequest:   explicitEditRequest && !readOnlyAnalysis,
	}
}

func requestEnvelopeClasses(envelope RequestEnvelope, decision ReviewRequestClassDecision) []RequestClass {
	classes := make([]RequestClass, 0, 6)
	if envelope.GoalPromptDraftOnly {
		classes = appendRequestClass(classes, RequestClassPlan)
	}
	if envelope.DocumentAuthoring || normalizeReviewRequestClass(decision.RequestClass) == reviewRequestClassDocumentArtifact {
		classes = appendRequestClass(classes, RequestClassDocument)
	}
	if envelope.ExplicitGitRequest {
		classes = appendRequestClass(classes, RequestClassGit)
	}
	if envelope.RequiresFreshExternalInfo {
		classes = appendRequestClass(classes, RequestClassResearch)
	}
	if envelope.ExplicitEditRequest || requestEnvelopeReviewClassMutates(decision.RequestClass) {
		classes = appendRequestClass(classes, RequestClassEdit)
	}
	if envelope.ReadOnlyAnalysis && (envelope.Intent == TurnIntentReviewCode || normalizeReviewRequestClass(decision.RequestClass) == reviewRequestClassReviewOnly) {
		classes = appendRequestClass(classes, RequestClassReview)
	}
	if envelope.Intent == TurnIntentPlanOrDesign || looksLikePlanOrDirectionOnlyRequest(envelope.ExternalUserText) {
		classes = appendRequestClass(classes, RequestClassPlan)
	}
	if len(classes) == 0 {
		if strings.Contains(envelope.ExternalUserText, "?") || envelope.ReadOnlyAnalysis {
			classes = appendRequestClass(classes, RequestClassQuestion)
		} else {
			classes = appendRequestClass(classes, RequestClassQuestion)
		}
	}
	if decision.Ambiguous {
		classes = appendRequestClass(classes, RequestClassAmbiguous)
	}
	return classes
}

func requestEnvelopePrimaryClass(envelope RequestEnvelope, decision ReviewRequestClassDecision) RequestClass {
	if envelope.GoalPromptDraftOnly {
		return RequestClassPlan
	}
	if envelope.DocumentAuthoring || normalizeReviewRequestClass(decision.RequestClass) == reviewRequestClassDocumentArtifact {
		return RequestClassDocument
	}
	if envelope.ExplicitEditRequest || requestEnvelopeReviewClassMutates(decision.RequestClass) {
		return RequestClassEdit
	}
	if envelope.ExplicitGitRequest {
		return RequestClassGit
	}
	if envelope.RequiresFreshExternalInfo {
		return RequestClassResearch
	}
	if envelope.Intent == TurnIntentReviewCode || normalizeReviewRequestClass(decision.RequestClass) == reviewRequestClassReviewOnly {
		return RequestClassReview
	}
	if envelope.Intent == TurnIntentPlanOrDesign || looksLikePlanOrDirectionOnlyRequest(envelope.ExternalUserText) {
		return RequestClassPlan
	}
	return RequestClassQuestion
}

func requestEnvelopeReviewClassMutates(class string) bool {
	switch normalizeReviewRequestClass(class) {
	case reviewRequestClassReviewThenModify, reviewRequestClassModifyThenReview:
		return true
	default:
		return false
	}
}

func requestEnvelopeReviewDecision(envelope RequestEnvelope) ReviewRequestClassDecision {
	envelope.Normalize()
	decision := ReviewRequestClassDecision{
		RequestClass:            envelope.ReviewRequestClass,
		LifecycleKind:           envelope.ReviewLifecycleKind,
		Reason:                  envelope.ReviewRequestClassReason,
		Confidence:              envelope.Confidence,
		AmbiguityWarnings:       append([]string(nil), envelope.Warnings...),
		SecondaryRequestClasses: nil,
	}
	for _, item := range envelope.Evidence {
		if strings.EqualFold(strings.TrimSpace(item.Source), "review_request_class") {
			decision.Signals = append(decision.Signals, strings.TrimSpace(item.Signal))
		}
	}
	decision.Normalize()
	return decision
}

func requestEnvelopeAllowsRepairContinuation(envelope RequestEnvelope) bool {
	envelope.Normalize()
	if envelope.AllowsFileMutation || envelope.ExplicitEditRequest || envelope.DocumentAuthoring {
		return true
	}
	if requestEnvelopeReviewClassMutates(envelope.ReviewRequestClass) {
		return true
	}
	return false
}

func requestTextAllowsRepairContinuation(text string) bool {
	base := strings.TrimSpace(baseUserQueryText(text))
	if base == "" {
		base = strings.TrimSpace(text)
	}
	if base == "" {
		return false
	}
	return requestEnvelopeAllowsRepairContinuation(buildRequestEnvelope(base))
}

func requestTextIsReadOnlyAnalysisBoundary(text string) bool {
	base := strings.TrimSpace(baseUserQueryText(text))
	if base == "" {
		base = strings.TrimSpace(text)
	}
	if base == "" {
		return false
	}
	envelope := buildRequestEnvelope(base)
	envelope.Normalize()
	return envelope.ReadOnlyAnalysis && !requestEnvelopeAllowsRepairContinuation(envelope)
}

func requestLooksLikeGitOnlyMutation(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(text)))
	if lower == "" || !looksLikeExplicitGitIntent(lower) {
		return false
	}
	if looksLikeDocumentAuthoringIntent(lower) {
		return false
	}
	return !containsAny(lower,
		"fix ", "patch ", "implement ", "modify ", "edit ", "refactor ", "repair ",
		"fix하고", "fix하고", "fix 후", "patch하고", "implement하고",
		"수정", "고쳐", "고치", "구현", "패치", "반영", "리팩터", "리팩토", "수리",
	)
}

func appendRequestClass(classes []RequestClass, class RequestClass) []RequestClass {
	if class == "" {
		return classes
	}
	for _, existing := range classes {
		if existing == class {
			return classes
		}
	}
	return append(classes, class)
}

func (e *RequestEnvelope) applyPolicy() {
	if e == nil {
		return
	}
	e.AllowsGitMutation = e.ExplicitGitRequest
	e.AllowsFileMutation = e.ExplicitEditRequest || e.DocumentAuthoring || requestEnvelopeReviewClassMutates(e.ReviewRequestClass)
	if e.GoalPromptDraftOnly {
		e.AllowsFileMutation = false
		e.AllowsGitMutation = false
		e.ReadOnlyAnalysis = true
		e.ExplicitEditRequest = false
	}
	if e.ReadOnlyAnalysis && !e.DocumentAuthoring && !e.ExplicitGitRequest {
		e.AllowsFileMutation = false
	}
	if e.Confidence > 0 && e.Confidence < 0.55 && !e.ExplicitEditRequest && !e.DocumentAuthoring && !e.ExplicitGitRequest {
		e.AllowsFileMutation = false
		e.AllowsGitMutation = false
		e.ReadOnlyAnalysis = true
		e.Warnings = append(e.Warnings, "low confidence request classification; mutation defaults to read-only")
	}
	e.RequiresVerification = e.AllowsFileMutation || promptExplicitlyRequiresVerification(e.ExternalUserText)
	if e.AllowsGitMutation {
		e.Boundary = ActionBoundaryMayGit
	} else if e.AllowsFileMutation {
		// must_edit is the strongest mutating boundary. Grant it only when the
		// explicit edit request is corroborated by a real source-edit signal (an
		// imperative source-edit command, or a source-edit verb co-occurring with a
		// file target) and the request is not git-only / run-command. Otherwise an
		// over-matched edit signal degrades to the softer may_edit boundary.
		if e.ExplicitEditRequest && !e.DocumentAuthoring && e.boundaryHasCorroboratedSourceEdit() {
			e.Boundary = ActionBoundaryMustEdit
		} else {
			e.Boundary = ActionBoundaryMayEdit
		}
	} else if !e.AllowsGitMutation {
		e.Boundary = ActionBoundaryReadOnly
	}
	if !e.AllowsGitMutation && e.Boundary == "" {
		e.Boundary = ActionBoundaryNoCommit
	}
}

// boundaryHasCorroboratedSourceEdit reports whether the request carries a real
// source-edit signal strong enough to justify the must_edit boundary: an
// imperative source-edit command, or a source-edit verb co-occurring with a file
// target. Git-only and run-command intents never corroborate a source edit.
func (e *RequestEnvelope) boundaryHasCorroboratedSourceEdit() bool {
	if e == nil {
		return false
	}
	if e.Intent == TurnIntentRunCommand || e.Intent == TurnIntentReviewCode {
		return false
	}
	base := strings.ToLower(strings.TrimSpace(baseUserQueryText(e.ExternalUserText)))
	if base == "" {
		return false
	}
	if requestLooksLikeGitOnlyMutation(base) {
		return false
	}
	if looksLikeImperativeSourceEditCommand(base) {
		return true
	}
	hasSourceEditVerb := containsWord(base,
		"fix", "edit", "modify", "patch", "refactor", "implement", "replace", "rename", "change", "update",
	) || containsAny(base,
		"고쳐", "고치", "수정해", "수정하", "구현해", "패치해", "변경해", "변경하", "교체해", "리팩터", "리팩토",
	)
	if !hasSourceEditVerb {
		return false
	}
	hasFileTarget := containsAny(base,
		".go", ".c", ".cpp", ".h", ".hpp", ".py", ".js", ".ts", ".rs", ".java", ".cs",
		"file", "함수", "function", "method", "메서드", "메소드", "class", "클래스",
		"코드", "소스", "source", "줄", "line ", "라인",
	)
	return hasFileTarget
}

func (e *RequestEnvelope) Normalize() {
	if e == nil {
		return
	}
	e.ExternalUserText = strings.TrimSpace(e.ExternalUserText)
	e.Classes = normalizeRequestClasses(e.Classes)
	if e.PrimaryClass == "" && len(e.Classes) > 0 {
		e.PrimaryClass = e.Classes[0]
	}
	if e.Boundary == "" {
		e.Boundary = ActionBoundaryReadOnly
	}
	if e.Confidence < 0 {
		e.Confidence = 0
	}
	if e.Confidence > 1 {
		e.Confidence = 1
	}
	e.Warnings = normalizeTaskStateList(e.Warnings, 8)
	e.Evidence = normalizeRequestEvidence(e.Evidence)
	e.ReviewRequestClass = normalizeReviewRequestClass(e.ReviewRequestClass)
	e.ReviewLifecycleKind = normalizeReviewLifecycleKind(e.ReviewLifecycleKind)
	e.ReviewRequestClassReason = strings.TrimSpace(e.ReviewRequestClassReason)
}

func normalizeRequestClasses(classes []RequestClass) []RequestClass {
	out := make([]RequestClass, 0, len(classes))
	seen := map[RequestClass]bool{}
	for _, class := range classes {
		class = RequestClass(strings.TrimSpace(string(class)))
		if class == "" || seen[class] {
			continue
		}
		seen[class] = true
		out = append(out, class)
	}
	return out
}

func normalizeRequestEvidence(items []RequestEvidence) []RequestEvidence {
	out := make([]RequestEvidence, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item.Source = strings.TrimSpace(item.Source)
		item.Signal = strings.TrimSpace(item.Signal)
		item.Detail = strings.TrimSpace(item.Detail)
		if item.Source == "" && item.Signal == "" && item.Detail == "" {
			continue
		}
		key := item.Source + "\x00" + item.Signal + "\x00" + item.Detail
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func (e RequestEnvelope) RenderPromptSection() string {
	if rendered, err := RenderRequestEnvelopePromptBlock(e); err == nil && strings.TrimSpace(rendered) != "" {
		return rendered
	}
	return e.renderPromptSectionFallback()
}

func (e RequestEnvelope) renderPromptSectionFallback() string {
	e.Normalize()
	var b strings.Builder
	b.WriteString("Request envelope:\n")
	if e.PrimaryClass != "" {
		fmt.Fprintf(&b, "- Primary class: %s.\n", e.PrimaryClass)
	}
	if len(e.Classes) > 0 {
		parts := make([]string, 0, len(e.Classes))
		for _, class := range e.Classes {
			parts = append(parts, string(class))
		}
		fmt.Fprintf(&b, "- Classes: %s.\n", strings.Join(parts, ", "))
	}
	if e.Boundary != "" {
		fmt.Fprintf(&b, "- Action boundary: %s.\n", e.Boundary)
	}
	fmt.Fprintf(&b, "- Allows file mutation: %t.\n", e.AllowsFileMutation)
	fmt.Fprintf(&b, "- Allows git mutation: %t.\n", e.AllowsGitMutation)
	fmt.Fprintf(&b, "- Requires verification: %t.\n", e.RequiresVerification)
	if e.RequiresFreshExternalInfo {
		b.WriteString("- Requires fresh external information: true.\n")
	}
	if e.ReviewRequestClass != "" {
		fmt.Fprintf(&b, "- Review request class: %s.\n", e.ReviewRequestClass)
	}
	if e.Confidence > 0 {
		fmt.Fprintf(&b, "- Classification confidence: %.2f.\n", e.Confidence)
	}
	if len(e.Warnings) > 0 {
		fmt.Fprintf(&b, "- Classification warnings: %s.\n", strings.Join(e.Warnings, " | "))
	}
	if e.ReadOnlyAnalysis {
		b.WriteString("\nRequest mode: analysis-only.\n")
		b.WriteString("- Investigate, explain, or document the issue.\n")
		b.WriteString("- Do not modify files or call edit tools unless the user explicitly asks for a fix.\n")
	} else if e.DocumentAuthoring {
		b.WriteString("\nRequest mode: document-authoring.\n")
		b.WriteString("- Produce the requested document or report as the deliverable.\n")
		b.WriteString("- You may create or update the target document file (for example a .md file) using the available file tools.\n")
		b.WriteString("- Do not modify, fix, or refactor source code; describe needed changes in the document instead unless the user gives an explicit source-edit command.\n")
	} else if e.ExplicitEditRequest {
		b.WriteString("\nRequest mode: inspect-and-fix.\n")
		b.WriteString("- Investigate the referenced code and apply the necessary fix directly when needed.\n")
		b.WriteString("- Use available inspect tools first, then use edit tools to make the change.\n")
		b.WriteString("- Do not ask the user to apply the patch manually unless an edit tool actually failed and you cite that tool error.\n")
	}
	if e.GoalPromptDraftOnly {
		b.WriteString("\nGoal prompt draft mode:\n")
		b.WriteString("- The user asked to draft goal prompt text, not to activate or run a goal.\n")
		b.WriteString("- Do not call create_goal or update_goal unless the user explicitly asks for goal activation or execution.\n")
	}
	if e.ExplicitGitRequest {
		b.WriteString("\nGit intent:\n")
		b.WriteString("- The user explicitly asked for a git action such as staging, committing, pushing, or opening a PR.\n")
		b.WriteString("- If you perform a git-mutating action, summarize exactly what you are about to do.\n")
	}
	if e.RequiresFreshExternalInfo {
		b.WriteString("\nFresh external information:\n")
		b.WriteString("- This request likely needs current external evidence before relying on memory or local-only context.\n")
	}
	return strings.TrimSpace(b.String())
}

func (e RequestEnvelope) agentRequestMode() agentRequestMode {
	return agentRequestMode{
		Intent:                e.Intent,
		ReviewOnlyModeRequest: e.ReviewOnlyModeRequest,
		ReadOnlyAnalysis:      e.ReadOnlyAnalysis,
		ExplicitEditRequest:   e.ExplicitEditRequest,
	}
}

func (a *Agent) latestRequestEnvelopeFor(request string) RequestEnvelope {
	base := strings.TrimSpace(baseUserQueryText(request))
	if base == "" {
		base = strings.TrimSpace(request)
	}
	if a != nil && a.Session != nil && a.Session.LastRequestEnvelope != nil {
		current := *a.Session.LastRequestEnvelope
		current.Normalize()
		if strings.EqualFold(strings.TrimSpace(baseUserQueryText(current.ExternalUserText)), base) {
			a.applySessionRequestEnvelopeContext(&current)
			current.Normalize()
			return current
		}
	}
	envelope := buildRequestEnvelope(base)
	a.applySessionRequestEnvelopeContext(&envelope)
	envelope.Normalize()
	return envelope
}

func (a *Agent) rememberRequestEnvelope(envelope RequestEnvelope) {
	if a == nil || a.Session == nil {
		return
	}
	envelope.Normalize()
	a.Session.LastRequestEnvelope = &envelope
}

func (a *Agent) applySessionRequestEnvelopeContext(envelope *RequestEnvelope) {
	if a == nil || a.Session == nil || envelope == nil {
		return
	}
	if envelope.Intent != TurnIntentContinueLastTask {
		return
	}
	contract := a.Session.AcceptanceContract
	if requestEnvelopeContractAllowsFileMutation(contract) {
		envelope.AllowsFileMutation = true
		envelope.RequiresVerification = true
		envelope.ReadOnlyAnalysis = false
		envelope.Boundary = ActionBoundaryMayEdit
		envelope.Classes = appendRequestClass(envelope.Classes, RequestClassEdit)
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{
			Source: "acceptance_contract",
			Signal: "preserved_mutable_context",
			Detail: normalizeReviewRequestClass(contract.RequestClass),
		})
		return
	}
	if contract != nil && requestEnvelopeContractIsReadOnly(contract) {
		return
	}
	// A prior successful file-mutation transaction must NOT re-enable mutation on a
	// NEW request that itself classifies as read-only. Ambiguity falls toward
	// read-only: do not inherit write capability from history when the fresh
	// envelope carries no edit/document/git signal of its own.
	if envelope.ReadOnlyAnalysis && !envelope.ExplicitEditRequest && !envelope.DocumentAuthoring && !envelope.ExplicitGitRequest && !requestEnvelopeReviewClassMutates(envelope.ReviewRequestClass) {
		return
	}
	if requestEnvelopeSessionHasMutablePatchContext(a.Session) {
		envelope.AllowsFileMutation = true
		envelope.RequiresVerification = true
		envelope.ReadOnlyAnalysis = false
		envelope.Boundary = ActionBoundaryMayEdit
		envelope.Classes = appendRequestClass(envelope.Classes, RequestClassEdit)
		envelope.Evidence = append(envelope.Evidence, RequestEvidence{
			Source: "patch_transaction",
			Signal: "preserved_mutable_context",
			Detail: "previous_file_mutation",
		})
	}
}

func requestEnvelopeContractAllowsFileMutation(contract *AcceptanceContract) bool {
	if contract == nil {
		return false
	}
	mode := strings.TrimSpace(contract.Mode)
	class := normalizeReviewRequestClass(contract.RequestClass)
	if strings.EqualFold(mode, "analysis_only") || class == reviewRequestClassReviewOnly {
		return false
	}
	return strings.EqualFold(mode, "inspect_and_fix") ||
		class == reviewRequestClassDocumentArtifact ||
		class == reviewRequestClassReviewThenModify ||
		class == reviewRequestClassModifyThenReview
}

func requestEnvelopeContractIsReadOnly(contract *AcceptanceContract) bool {
	if contract == nil {
		return false
	}
	mode := strings.TrimSpace(contract.Mode)
	class := normalizeReviewRequestClass(contract.RequestClass)
	return strings.EqualFold(mode, "analysis_only") || class == reviewRequestClassReviewOnly
}

func requestEnvelopeSessionHasMutablePatchContext(session *Session) bool {
	if session == nil {
		return false
	}
	if requestEnvelopePatchTransactionHasFileMutation(session.ActivePatchTransaction) {
		return true
	}
	for i := range session.PatchTransactions {
		tx := session.PatchTransactions[i]
		if requestEnvelopePatchTransactionHasFileMutation(&tx) {
			return true
		}
	}
	return false
}

func requestEnvelopePatchTransactionHasFileMutation(tx *PatchTransaction) bool {
	if tx == nil {
		return false
	}
	tx.Normalize()
	for _, entry := range tx.Entries {
		if !requestEnvelopePatchEntrySucceeded(entry) {
			continue
		}
		if requestEnvelopePatchEntryHasFileMutation(entry) {
			return true
		}
	}
	return false
}

func requestEnvelopePatchEntrySucceeded(entry PatchTransactionEntry) bool {
	status := strings.TrimSpace(strings.ToLower(entry.Status))
	return status == "" || status == "success" || status == "succeeded" || status == "ok"
}

func requestEnvelopePatchEntryHasFileMutation(entry PatchTransactionEntry) bool {
	toolName := strings.TrimSpace(strings.ToLower(entry.ToolName))
	if toolName == "apply_patch" || toolName == "write_file" || toolName == "replace_in_file" || toolName == "apply_edit_proposal" {
		return true
	}
	for _, change := range entry.Paths {
		op := strings.TrimSpace(strings.ToLower(change.Operation))
		if op == "" || op == "write_file" || op == "create" || op == "modify" || op == "update" || op == "delete" || op == "remove" || op == "rename" || op == "unknown" {
			return true
		}
	}
	return false
}

func disableRequestEnvelopeForbiddenTools(disabled map[string]bool, registry *ToolRegistry, envelope RequestEnvelope, mcp *MCPManager) {
	if disabled == nil {
		return
	}
	envelope.Normalize()
	if !envelope.AllowsFileMutation {
		disabled["apply_patch"] = true
		disabled["write_file"] = true
		disabled["replace_in_file"] = true
		disabled["apply_edit_proposal"] = true
	}
	if !envelope.AllowsGitMutation {
		disabled["git_add"] = true
		disabled["git_commit"] = true
		disabled["git_push"] = true
		disabled["git_create_pr"] = true
	}
	// A goal-prompt-draft-only turn forces read-only and the prompt instructs the
	// model not to mutate goals. Match exposure to that policy instead of relying
	// on a prompt-only guard.
	if envelope.GoalPromptDraftOnly {
		disabled["create_goal"] = true
		disabled["update_goal"] = true
	}
	if !envelope.AllowsWebResearch {
		disableWebResearchToolsForLocalCodeWork(disabled, registry, mcp)
	}
}
