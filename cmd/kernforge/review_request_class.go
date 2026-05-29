package main

import (
	"strings"
)

const (
	reviewRequestClassGeneral          = "general"
	reviewRequestClassReviewOnly       = "review_only"
	reviewRequestClassDocumentArtifact = "document_artifact"
	reviewRequestClassReviewThenModify = "review_then_modify"
	reviewRequestClassModifyThenReview = "modify_then_review"
	reviewRequestClassVerificationOnly = "verification_only"
	reviewRequestClassValidationOnly   = "validation_only"

	reviewRouteModeSingleModel       = "single_model"
	reviewRouteModeCrossModel        = "cross_model"
	reviewRouteModeDeterministicOnly = "deterministic_only"

	reviewLifecycleKindGeneral          = "general"
	reviewLifecycleKindReviewOnly       = "review_only"
	reviewLifecycleKindDocumentArtifact = "document_artifact"
	reviewLifecycleKindImplementation   = "implementation"
	reviewLifecycleKindModifyThenReview = "modify_then_review"
	reviewLifecycleKindFixFromReview    = "fix_from_review"
	reviewLifecycleKindAnalysis         = "analysis"
	reviewLifecycleKindMixedFlow        = "mixed_flow"
	reviewLifecycleKindVerificationOnly = "verification_only"
	reviewLifecycleKindValidationOnly   = "validation_only"
)

type ReviewRequestLifecycle struct {
	RequestClass             string                   `json:"request_class,omitempty"`
	LifecycleKind            string                   `json:"lifecycle_kind,omitempty"`
	MixedFlow                bool                     `json:"mixed_flow,omitempty"`
	SecondaryRequestClasses  []string                 `json:"secondary_request_classes,omitempty"`
	Phase                    string                   `json:"phase,omitempty"`
	RouteMode                string                   `json:"route_mode,omitempty"`
	Reason                   string                   `json:"reason,omitempty"`
	Timeline                 []ReviewLifecyclePhase   `json:"timeline,omitempty"`
	ClassificationConfidence float64                  `json:"classification_confidence,omitempty"`
	ClassificationAmbiguous  bool                     `json:"classification_ambiguous,omitempty"`
	AmbiguityWarnings        []string                 `json:"ambiguity_warnings,omitempty"`
	Contract                 *ReviewLifecycleContract `json:"contract,omitempty"`
	RouteQuality             string                   `json:"route_quality,omitempty"`
	RouteDegradedReasons     []string                 `json:"route_degraded_reasons,omitempty"`
	ReviewGateStatus         string                   `json:"review_gate_status,omitempty"`
	RepairGateStatus         string                   `json:"repair_gate_status,omitempty"`
	DocumentGateStatus       string                   `json:"document_gate_status,omitempty"`
	VerificationGateStatus   string                   `json:"verification_gate_status,omitempty"`
	SecondPassStatus         string                   `json:"second_pass_status,omitempty"`
	CrossReviewTriage        string                   `json:"cross_review_triage,omitempty"`
	RemainingObligations     []string                 `json:"remaining_obligations,omitempty"`
	NextRecommendedCommand   string                   `json:"next_recommended_command,omitempty"`
}

type ReviewLifecycleContract struct {
	RequestClass                   string   `json:"request_class,omitempty"`
	ReadOnly                       bool     `json:"read_only,omitempty"`
	RequiresReviewBeforeModify     bool     `json:"requires_review_before_modify,omitempty"`
	RequiresPostChangeReview       bool     `json:"requires_post_change_review,omitempty"`
	RequiresDocumentGate           bool     `json:"requires_document_gate,omitempty"`
	RequiresVerificationDisclosure bool     `json:"requires_verification_disclosure,omitempty"`
	RequiresValidationDisclosure   bool     `json:"requires_validation_disclosure,omitempty"`
	FinalAnswerRequirements        []string `json:"final_answer_requirements,omitempty"`
	SkipRules                      []string `json:"skip_rules,omitempty"`
}

type ReviewRequestClassDecision struct {
	RequestClass            string   `json:"request_class,omitempty"`
	LifecycleKind           string   `json:"lifecycle_kind,omitempty"`
	MixedFlow               bool     `json:"mixed_flow,omitempty"`
	SecondaryRequestClasses []string `json:"secondary_request_classes,omitempty"`
	Reason                  string   `json:"reason,omitempty"`
	Confidence              float64  `json:"confidence,omitempty"`
	Ambiguous               bool     `json:"ambiguous,omitempty"`
	AmbiguityWarnings       []string `json:"ambiguity_warnings,omitempty"`
	Signals                 []string `json:"signals,omitempty"`
}

func (d *ReviewRequestClassDecision) Normalize() {
	if d == nil {
		return
	}
	d.RequestClass = normalizeReviewRequestClass(d.RequestClass)
	d.LifecycleKind = normalizeReviewLifecycleKind(d.LifecycleKind)
	if d.LifecycleKind == reviewLifecycleKindGeneral {
		d.LifecycleKind = reviewLifecycleKindForRequestClass(d.RequestClass)
	}
	d.SecondaryRequestClasses = normalizeReviewRequestClasses(d.SecondaryRequestClasses, 6)
	if len(d.SecondaryRequestClasses) > 0 || d.LifecycleKind == reviewLifecycleKindMixedFlow {
		d.MixedFlow = true
	}
	d.Reason = strings.TrimSpace(d.Reason)
	if d.Confidence < 0 {
		d.Confidence = 0
	}
	if d.Confidence > 1 {
		d.Confidence = 1
	}
	d.AmbiguityWarnings = normalizeTaskStateList(d.AmbiguityWarnings, 8)
	d.Signals = normalizeTaskStateList(d.Signals, 12)
	if len(d.AmbiguityWarnings) > 0 {
		d.Ambiguous = true
	}
}

func (l *ReviewRequestLifecycle) Normalize() {
	if l == nil {
		return
	}
	l.RequestClass = normalizeReviewRequestClass(l.RequestClass)
	l.LifecycleKind = normalizeReviewLifecycleKind(l.LifecycleKind)
	if l.LifecycleKind == reviewLifecycleKindGeneral {
		l.LifecycleKind = reviewLifecycleKindForRequestClass(l.RequestClass)
	}
	l.SecondaryRequestClasses = normalizeReviewRequestClasses(l.SecondaryRequestClasses, 6)
	if len(l.SecondaryRequestClasses) > 0 || l.LifecycleKind == reviewLifecycleKindMixedFlow {
		l.MixedFlow = true
	}
	l.Phase = strings.TrimSpace(l.Phase)
	l.RouteMode = strings.TrimSpace(l.RouteMode)
	l.Reason = strings.TrimSpace(l.Reason)
	l.Timeline = normalizeReviewLifecycleTimeline(l.Timeline)
	if l.ClassificationConfidence < 0 {
		l.ClassificationConfidence = 0
	}
	if l.ClassificationConfidence > 1 {
		l.ClassificationConfidence = 1
	}
	l.AmbiguityWarnings = normalizeTaskStateList(l.AmbiguityWarnings, 8)
	if len(l.AmbiguityWarnings) > 0 {
		l.ClassificationAmbiguous = true
	}
	if l.Contract != nil {
		l.Contract.Normalize()
	}
	l.RouteQuality = strings.TrimSpace(l.RouteQuality)
	l.RouteDegradedReasons = normalizeTaskStateList(l.RouteDegradedReasons, 8)
	l.ReviewGateStatus = strings.TrimSpace(l.ReviewGateStatus)
	l.RepairGateStatus = strings.TrimSpace(l.RepairGateStatus)
	l.DocumentGateStatus = strings.TrimSpace(l.DocumentGateStatus)
	l.VerificationGateStatus = strings.TrimSpace(l.VerificationGateStatus)
	l.SecondPassStatus = strings.TrimSpace(l.SecondPassStatus)
	l.CrossReviewTriage = strings.TrimSpace(l.CrossReviewTriage)
	l.RemainingObligations = normalizeTaskStateList(l.RemainingObligations, 8)
	l.NextRecommendedCommand = strings.TrimSpace(l.NextRecommendedCommand)
}

func (c *ReviewLifecycleContract) Normalize() {
	if c == nil {
		return
	}
	c.RequestClass = normalizeReviewRequestClass(c.RequestClass)
	c.FinalAnswerRequirements = normalizeTaskStateList(c.FinalAnswerRequirements, 12)
	c.SkipRules = normalizeTaskStateList(c.SkipRules, 8)
}

func normalizeReviewLifecycleKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "-", "_")))
	switch value {
	case reviewLifecycleKindReviewOnly, "review", "code_review", "read_only_review":
		return reviewLifecycleKindReviewOnly
	case reviewLifecycleKindDocumentArtifact, "document", "document_generation", "generated_document", "report_artifact":
		return reviewLifecycleKindDocumentArtifact
	case reviewLifecycleKindImplementation, "implement", "code_development", "development", "edit_code":
		return reviewLifecycleKindImplementation
	case reviewLifecycleKindModifyThenReview, "edit_then_review", "post_change", "pre_write":
		return reviewLifecycleKindModifyThenReview
	case reviewLifecycleKindFixFromReview, "review_then_fix", "review_before_fix", "review_then_modify", "pre_fix":
		return reviewLifecycleKindFixFromReview
	case reviewLifecycleKindAnalysis, "analysis_only", "project_knowledge", "diagnosis", "diagnose":
		return reviewLifecycleKindAnalysis
	case reviewLifecycleKindMixedFlow, "mixed", "mixed_request":
		return reviewLifecycleKindMixedFlow
	case reviewLifecycleKindVerificationOnly, "verify_only", "verification":
		return reviewLifecycleKindVerificationOnly
	case reviewLifecycleKindValidationOnly, "validate_only", "validation":
		return reviewLifecycleKindValidationOnly
	case "", reviewLifecycleKindGeneral:
		return reviewLifecycleKindGeneral
	default:
		return value
	}
}

func reviewLifecycleKindForRequestClass(class string) string {
	switch normalizeReviewRequestClass(class) {
	case reviewRequestClassReviewOnly:
		return reviewLifecycleKindReviewOnly
	case reviewRequestClassDocumentArtifact:
		return reviewLifecycleKindDocumentArtifact
	case reviewRequestClassReviewThenModify:
		return reviewLifecycleKindFixFromReview
	case reviewRequestClassModifyThenReview:
		return reviewLifecycleKindModifyThenReview
	case reviewRequestClassVerificationOnly:
		return reviewLifecycleKindVerificationOnly
	case reviewRequestClassValidationOnly:
		return reviewLifecycleKindValidationOnly
	default:
		return reviewLifecycleKindGeneral
	}
}

func normalizeReviewRequestClasses(values []string, limit int) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		normalized := normalizeReviewRequestClass(value)
		if normalized == "" || normalized == reviewRequestClassGeneral || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func reviewDecisionHasSignal(decision ReviewRequestClassDecision, signal string) bool {
	for _, item := range decision.Signals {
		if strings.EqualFold(strings.TrimSpace(item), signal) {
			return true
		}
	}
	return false
}

func reviewDecisionMixedSourceDocument(decision ReviewRequestClassDecision) bool {
	if !reviewDecisionHasSignal(decision, "document_artifact_intent") {
		return false
	}
	return reviewDecisionHasSignal(decision, "source_modification_intent") ||
		reviewDecisionHasSignal(decision, "review_intent") ||
		reviewDecisionHasSignal(decision, "read_only_analysis")
}

func reviewSecondaryRequestClassesForDecision(decision ReviewRequestClassDecision) []string {
	classes := []string{}
	if reviewDecisionHasSignal(decision, "document_artifact_intent") {
		classes = append(classes, reviewRequestClassDocumentArtifact)
	}
	if reviewDecisionHasSignal(decision, "source_modification_intent") {
		classes = append(classes, reviewRequestClassModifyThenReview)
	}
	if reviewDecisionHasSignal(decision, "review_intent") || reviewDecisionHasSignal(decision, "read_only_analysis") {
		classes = append(classes, reviewRequestClassReviewOnly)
	}
	return normalizeReviewRequestClasses(classes, 6)
}

func reviewLifecycleKindFromDecision(decision ReviewRequestClassDecision, request string, intent TurnIntent, target string, mode string) string {
	lower := strings.ToLower(strings.TrimSpace(baseUserQueryText(request)))
	if lower == "" {
		lower = strings.ToLower(strings.TrimSpace(request))
	}
	if reviewDecisionMixedSourceDocument(decision) {
		return reviewLifecycleKindMixedFlow
	}
	switch normalizeReviewRequestClass(decision.RequestClass) {
	case reviewRequestClassReviewOnly:
		if intent == TurnIntentAskProjectKnowledge ||
			intent == TurnIntentDiagnoseRecentError ||
			(prefersReadOnlyAnalysisIntent(lower) && !hasTurnReviewIntent(lower)) {
			return reviewLifecycleKindAnalysis
		}
		return reviewLifecycleKindReviewOnly
	case reviewRequestClassDocumentArtifact:
		return reviewLifecycleKindDocumentArtifact
	case reviewRequestClassReviewThenModify:
		return reviewLifecycleKindFixFromReview
	case reviewRequestClassModifyThenReview:
		if requestExplicitlyAsksReviewAfterModify(lower) ||
			strings.EqualFold(strings.TrimSpace(mode), "post_change") ||
			strings.EqualFold(strings.TrimSpace(mode), "pre_write") {
			return reviewLifecycleKindModifyThenReview
		}
		return reviewLifecycleKindImplementation
	case reviewRequestClassVerificationOnly:
		return reviewLifecycleKindVerificationOnly
	case reviewRequestClassValidationOnly:
		return reviewLifecycleKindValidationOnly
	default:
		if intent == TurnIntentAskProjectKnowledge ||
			intent == TurnIntentDiagnoseRecentError ||
			prefersReadOnlyAnalysisIntent(lower) ||
			strings.EqualFold(strings.TrimSpace(target), reviewTargetSourceAnalysis) ||
			strings.EqualFold(strings.TrimSpace(target), reviewTargetAnalysis) ||
			strings.EqualFold(strings.TrimSpace(target), reviewTargetAnalysisAlias) {
			return reviewLifecycleKindAnalysis
		}
		return reviewLifecycleKindGeneral
	}
}

func applyReviewLifecycleKindToDecision(decision ReviewRequestClassDecision, request string, intent TurnIntent, target string, mode string) ReviewRequestClassDecision {
	lifecycleKind := normalizeReviewLifecycleKind(decision.LifecycleKind)
	defaultKind := reviewLifecycleKindForRequestClass(decision.RequestClass)
	if lifecycleKind == reviewLifecycleKindGeneral || lifecycleKind == defaultKind {
		decision.LifecycleKind = reviewLifecycleKindFromDecision(decision, request, intent, target, mode)
	}
	if reviewDecisionMixedSourceDocument(decision) {
		decision.MixedFlow = true
		if len(decision.SecondaryRequestClasses) == 0 {
			decision.SecondaryRequestClasses = reviewSecondaryRequestClassesForDecision(decision)
		}
	}
	decision.Normalize()
	return decision
}

func reviewLifecycleKindForRun(run *ReviewRun) string {
	if run == nil {
		return reviewLifecycleKindGeneral
	}
	if run.Lifecycle != nil {
		if kind := normalizeReviewLifecycleKind(run.Lifecycle.LifecycleKind); kind != reviewLifecycleKindGeneral {
			return kind
		}
	}
	if kind := normalizeReviewLifecycleKind(run.RequestAnalysis.LifecycleKind); kind != reviewLifecycleKindGeneral {
		return kind
	}
	class := firstNonBlankString(run.RequestClass, run.RequestAnalysis.RequestClass)
	request := firstNonBlankString(run.RequestAnalysis.OriginalRequest, run.Objective)
	return reviewLifecycleKindFromDecision(ReviewRequestClassDecision{
		RequestClass: class,
		Signals:      run.RequestAnalysis.RequestClassSignals,
	}, request, classifyTurnIntent(request), run.Target, firstNonBlankString(run.Trigger, run.Mode))
}

func reviewMixedFlowForRun(run *ReviewRun) bool {
	if run == nil {
		return false
	}
	if run.RequestAnalysis.MixedFlow {
		return true
	}
	return run.Lifecycle != nil && run.Lifecycle.MixedFlow
}

func reviewSecondaryRequestClassesForRun(run *ReviewRun) []string {
	if run == nil {
		return nil
	}
	classes := normalizeReviewRequestClasses(run.RequestAnalysis.SecondaryRequestClasses, 6)
	if len(classes) > 0 {
		return classes
	}
	if run.Lifecycle != nil {
		return normalizeReviewRequestClasses(run.Lifecycle.SecondaryRequestClasses, 6)
	}
	return nil
}

func requestExplicitlyAsksReviewAfterModify(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"then review", "review after", "post-change review", "self-review", "self review",
		"수정 후 검토", "수정하고 검토", "패치 후 리뷰", "변경 후 리뷰", "변경 후 검토",
	)
}

func normalizeReviewRequestClass(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "-", "_")))
	switch value {
	case reviewRequestClassReviewOnly, "review", "code_review", "read_only_review":
		return reviewRequestClassReviewOnly
	case reviewRequestClassDocumentArtifact, "document", "document_generation", "generated_document", "report_artifact":
		return reviewRequestClassDocumentArtifact
	case reviewRequestClassReviewThenModify, "review_then_fix", "review_before_fix", "pre_fix", "fix_from_review":
		return reviewRequestClassReviewThenModify
	case reviewRequestClassModifyThenReview, "edit_then_review", "post_change", "pre_write", "implementation", "implement":
		return reviewRequestClassModifyThenReview
	case "analysis", "analysis_only", "project_knowledge", "diagnose_recent_error":
		return reviewRequestClassReviewOnly
	case reviewRequestClassVerificationOnly, "verify_only", "verification":
		return reviewRequestClassVerificationOnly
	case reviewRequestClassValidationOnly, "validate_only", "validation":
		return reviewRequestClassValidationOnly
	case "", reviewRequestClassGeneral:
		return reviewRequestClassGeneral
	default:
		return value
	}
}

func classifyReviewRequestClass(rt *runtimeState, root string, opts ReviewHarnessOptions, target string, mode string, discovery ReviewScopeDiscovery) (string, string) {
	decision := classifyReviewRequestClassDecision(rt, root, opts, target, mode, discovery)
	return decision.RequestClass, decision.Reason
}

func classifyReviewRequestClassDecision(rt *runtimeState, root string, opts ReviewHarnessOptions, target string, mode string, discovery ReviewScopeDiscovery) ReviewRequestClassDecision {
	request := strings.TrimSpace(baseUserQueryText(opts.Request))
	lower := strings.ToLower(request)
	trigger := strings.ToLower(strings.TrimSpace(opts.Trigger))
	documentIntent := requestLooksLikeDocumentArtifactIntent(rt, request, opts)
	sourceModification := requestHasSourceModificationIntent(lower, opts.Paths)
	if !documentIntent && strings.EqualFold(mode, reviewModeLiveFix) {
		sourceModification = true
	}
	explicitNoEdit := requestHasExplicitNoEditLanguage(lower)
	reviewIntent := looksLikeReviewInspectionOnlyRequest(lower) || hasTurnReviewIntent(lower)
	reviewFirst := requestLooksLikeReviewThenModify(lower) || requestLooksLikeInspectBugsThenFixConfirmed(lower)
	decision := ReviewRequestClassDecision{
		RequestClass: reviewRequestClassGeneral,
		Reason:       "no specialized request lifecycle was selected",
		Confidence:   0.62,
	}
	if documentIntent {
		decision.Signals = append(decision.Signals, "document_artifact_intent")
	}
	if sourceModification {
		decision.Signals = append(decision.Signals, "source_modification_intent")
	}
	if reviewIntent {
		decision.Signals = append(decision.Signals, "review_intent")
	}
	if explicitNoEdit {
		decision.Signals = append(decision.Signals, "explicit_no_edit")
	}
	if documentIntent && sourceModification {
		if requestExplicitlyOrdersReviewBeforeModification(lower) || requestLooksLikeInspectBugsThenFixConfirmed(lower) {
			decision.RequestClass = reviewRequestClassReviewThenModify
			decision.Reason = "mixed document/report and source-change request; source modification requires review-before-modify lifecycle while document output remains an artifact obligation"
		} else {
			decision.RequestClass = reviewRequestClassModifyThenReview
			decision.Reason = "mixed document/report and source-change request; code behavior is affected so post-change review and verification disclosure are required"
		}
		decision.Confidence = 0.74
		decision.AmbiguityWarnings = append(decision.AmbiguityWarnings, "mixed document artifact and source modification signals; document_artifact was not selected because code behavior may change")
		decision.Normalize()
		return decision
	}
	if documentIntent {
		decision.RequestClass = reviewRequestClassDocumentArtifact
		decision.Reason = "request asks for a generated document/report artifact; artifact-quality gates are primary and source edits are not implied"
		decision.Confidence = 0.86
		if reviewIntent {
			decision.Confidence = 0.78
			decision.AmbiguityWarnings = append(decision.AmbiguityWarnings, "mixed review and document signals; selected document_artifact because only a report/document artifact is requested")
		}
		if explicitNoEdit {
			decision.AmbiguityWarnings = append(decision.AmbiguityWarnings, "explicit no-edit language applies to source code; document artifact creation remains the selected lifecycle")
		}
		decision.Normalize()
		return decision
	}
	if requestLooksLikeValidationOnly(lower) {
		decision.RequestClass = reviewRequestClassValidationOnly
		decision.Reason = "request asks to validate existing work without asking for code edits"
		decision.Confidence = 0.9
		decision.Normalize()
		return decision
	}
	if requestLooksLikeVerificationOnly(lower) {
		decision.RequestClass = reviewRequestClassVerificationOnly
		decision.Reason = "request asks to run or report verification without asking for code edits"
		decision.Confidence = 0.9
		decision.Normalize()
		return decision
	}
	switch trigger {
	case reviewBeforeFixTrigger:
		decision.RequestClass = reviewRequestClassReviewThenModify
		decision.Reason = "pre-fix trigger requires review findings before repair guidance"
		decision.Confidence = 0.94
		decision.Normalize()
		return decision
	case "pre_write", "post_change":
		decision.RequestClass = reviewRequestClassModifyThenReview
		decision.Reason = "code-changing lifecycle requires review after the proposed or applied modification"
		decision.Confidence = 0.92
		decision.Normalize()
		return decision
	case naturalReviewTrigger:
		decision.RequestClass = reviewRequestClassReviewOnly
		decision.Reason = "natural-language review route is read-only unless the user explicitly asks for a fix"
		decision.Confidence = 0.9
		decision.Normalize()
		return decision
	}
	if reviewFirst {
		decision.RequestClass = reviewRequestClassReviewThenModify
		decision.Reason = "request asks to inspect/review first and fix only confirmed findings"
		decision.Confidence = 0.84
		if requestLooksLikeInspectBugsThenFixConfirmed(lower) {
			decision.AmbiguityWarnings = append(decision.AmbiguityWarnings, "bug-inspection plus confirmed-fix language is mixed; selected review_then_modify to avoid silently editing unconfirmed issues")
		}
		decision.Normalize()
		return decision
	}
	if requestLooksLikeModifyThenReview(lower) {
		decision.RequestClass = reviewRequestClassModifyThenReview
		decision.Reason = "request asks for implementation or modification with review afterwards"
		decision.Confidence = 0.86
		decision.Normalize()
		return decision
	}
	if explicitNoEdit && reviewIntent {
		decision.RequestClass = reviewRequestClassReviewOnly
		decision.Reason = "request explicitly asks for read-only review and no edits"
		decision.Confidence = 0.95
		decision.Normalize()
		return decision
	}
	if looksLikeReviewInspectionOnlyRequest(lower) ||
		(hasTurnReviewIntent(lower) && !looksLikeExplicitEditIntent(lower)) {
		decision.RequestClass = reviewRequestClassReviewOnly
		decision.Reason = "request asks for review/inspection only and has no explicit edit command"
		decision.Confidence = 0.86
		decision.Normalize()
		return decision
	}
	if requestLooksLikeReviewOfExistingChange(lower) {
		decision.RequestClass = reviewRequestClassReviewOnly
		decision.Reason = "request asks to review an existing change or provided diff without asking for a new edit"
		decision.Confidence = 0.88
		decision.Normalize()
		return decision
	}
	if looksLikeExplicitEditIntent(lower) || strings.EqualFold(mode, reviewModeLiveFix) {
		decision.RequestClass = reviewRequestClassModifyThenReview
		decision.Reason = "request is an edit/fix lifecycle; post-change review and validation disclosure are required"
		decision.Confidence = 0.82
		decision.Normalize()
		return decision
	}
	if strings.EqualFold(target, reviewTargetAnalysis) && containsAny(lower, "write", "generate", "create", "작성", "생성", "만들") {
		decision.RequestClass = reviewRequestClassDocumentArtifact
		decision.Reason = "analysis/report target is being authored as an artifact"
		decision.Confidence = 0.82
		decision.Normalize()
		return decision
	}
	if len(discovery.CandidateFiles) > 0 && hasTurnReviewIntent(lower) {
		decision.RequestClass = reviewRequestClassReviewOnly
		decision.Reason = "request mentions reviewable files and no modification lifecycle was selected"
		decision.Confidence = 0.78
		decision.Normalize()
		return decision
	}
	decision.Normalize()
	return decision
}

func requestClassLooksLikeDocumentArtifact(rt *runtimeState, request string, opts ReviewHarnessOptions) bool {
	return requestLooksLikeDocumentArtifactIntent(rt, request, opts) &&
		!requestHasSourceModificationIntent(strings.ToLower(strings.TrimSpace(baseUserQueryText(request))), opts.Paths)
}

func requestLooksLikeDocumentArtifactIntent(rt *runtimeState, request string, opts ReviewHarnessOptions) bool {
	if preWriteRequestLooksLikeGeneratedDocumentArtifact(request) || looksLikeReviewArtifactAuthoringRequest(request) || looksLikeDocumentAuthoringIntent(request) {
		return true
	}
	if rt != nil && rt.session != nil {
		if generatedDocumentArtifactRequestContextForTurn(rt.session, request) != "" {
			return true
		}
		if rt.session.AcceptanceContract != nil {
			for _, path := range rt.session.AcceptanceContract.RequiredArtifacts {
				if pathLooksLikeDocumentArtifact(path) {
					return true
				}
			}
		}
	}
	for _, path := range opts.Paths {
		if pathLooksLikeDocumentArtifact(path) && looksLikeDocumentAuthoringIntent(request) {
			return true
		}
	}
	return false
}

func requestHasSourceModificationIntent(lower string, paths []string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" {
		return false
	}
	if requestHasExplicitNoEditLanguage(lower) && !containsAny(lower, "fix only", "fix confirmed", "수정할 항목만", "확정된") {
		return false
	}
	hasSourcePath := false
	for _, path := range paths {
		normalized := strings.ToLower(filepathSlashTrim(path))
		if normalized == "" {
			continue
		}
		if reviewPathIsExecutableSource(normalized) || (!pathLooksLikeDocumentArtifact(normalized) && strings.Contains(normalized, ".")) {
			hasSourcePath = true
			break
		}
	}
	hasSourceSignal := hasSourcePath || containsAny(lower,
		"code", "source", "source code", "implementation", "function", "method", "class", "module", "runtime", "handler",
		".go", ".cpp", ".cc", ".cxx", ".c", ".h", ".hpp", ".cs", ".rs", ".py", ".js", ".ts", ".tsx", ".jsx",
		"코드", "소스", "소스코드", "구현", "함수", "클래스", "모듈", "핸들러",
	)
	hasBugSignal := containsAny(lower,
		"bug", "bugs", "defect", "defects", "regression", "issue", "issues", "crash", "failure", "failing", "fails", "broken",
		"버그", "오류", "에러", "문제", "문제점", "회귀", "깨짐", "실패",
	)
	hasSourceEditVerb := containsAny(lower,
		"address ", "change ", "correct ", "edit ", "fix ", "implement ", "modify ", "patch ", "refactor ", "remove ", "rename ", "replace ",
		"수정", "고쳐", "고치", "해결", "패치", "반영", "구현", "변경", "삭제", "교체",
	)
	if hasSourceEditVerb && (hasSourceSignal || hasBugSignal) {
		return true
	}
	if hasSourceEditVerb && !looksLikeDocumentAuthoringIntent(lower) {
		return true
	}
	return false
}

func filepathSlashTrim(path string) string {
	return strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
}

func requestHasExplicitNoEditLanguage(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" {
		return false
	}
	if hasRepairActionNegation(lower) {
		return true
	}
	return containsAny(lower,
		"read-only", "read only", "review only", "only review", "analysis only", "inspect only",
		"no edit", "no edits", "no file edit", "no code edit", "no changes", "without changes",
		"do not change", "don't change", "dont change",
		"읽기 전용", "리뷰만", "검토만", "분석만", "수정 금지", "변경 금지", "코드 수정 없이", "수정 없이",
	)
}

func requestLooksLikeInspectBugsThenFixConfirmed(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" || hasRepairActionNegation(lower) {
		return false
	}
	hasInspection := containsAny(lower,
		"inspect", "review", "audit", "find", "look for", "check",
		"검토", "리뷰", "점검", "확인", "찾",
	)
	hasBug := containsAny(lower,
		"bug", "bugs", "issue", "issues", "defect", "defects", "problem", "problems",
		"버그", "이슈", "문제", "문제점", "결함",
	)
	hasFix := hasRepairActionIntent(lower)
	hasConfirmedOnly := containsAny(lower,
		"only confirmed", "confirmed issues", "confirmed bugs", "verified issues", "proven issues", "actionable findings",
		"확인된", "확정된", "검증된", "재현된", "근거 있는", "확인한 것만", "확정한 것만",
	)
	return hasInspection && hasBug && hasFix && hasConfirmedOnly
}

func requestExplicitlyOrdersReviewBeforeModification(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" || hasRepairActionNegation(lower) {
		return false
	}
	return containsAny(lower,
		"review then fix",
		"review, then fix",
		"review before fixing",
		"review before modifying",
		"inspect then fix",
		"audit then fix",
		"fix after review",
		"repair after review",
		"검토 후 수정",
		"검토한 뒤 수정",
		"리뷰 후 수정",
		"리뷰한 뒤 수정",
		"검토하고 나서 수정",
		"리뷰하고 나서 수정",
	)
}

func requestLooksLikeValidationOnly(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" {
		return false
	}
	if !containsAny(lower, "validate", "validation", "검산") {
		return false
	}
	if requestLooksLikeImplementationOrSourceEditWork(lower) {
		return false
	}
	return !hasTurnReviewIntent(lower) || containsAny(lower, "validation only", "validate only", "검증만")
}

func requestLooksLikeVerificationOnly(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" || !requestLooksLikeLocalVerificationWork(lower) {
		return false
	}
	return !requestLooksLikeImplementationOrSourceEditWork(lower)
}

func requestLooksLikeReviewThenModify(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" {
		return false
	}
	if looksLikeReviewBeforeFixIntent(lower) {
		return true
	}
	return containsAny(lower,
		"review and fix", "review then fix", "review, then fix", "review before fixing",
		"inspect and fix", "audit and fix", "fix after review", "repair after review",
		"검토하고 수정", "검토 후 수정", "검토해서 수정", "리뷰하고 수정", "리뷰 후 수정", "리뷰해서 수정",
		"검토한 뒤 수정", "리뷰한 뒤 수정",
	)
}

func requestLooksLikeModifyThenReview(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" {
		return false
	}
	if requestLooksLikeReviewThenModify(lower) {
		return false
	}
	if !looksLikeExplicitEditIntent(lower) {
		return false
	}
	return containsAny(lower,
		"then review", "review after", "post-change review", "self-review", "self review",
		"수정 후 검토", "수정하고 검토", "패치 후 리뷰", "변경 후 리뷰", "변경 후 검토",
	) || !hasTurnReviewIntent(lower)
}

func requestLooksLikeReviewOfExistingChange(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	if lower == "" || !hasTurnReviewIntent(lower) {
		return false
	}
	if requestLooksLikeReviewThenModify(lower) {
		return false
	}
	return containsAny(lower,
		"review the modified code",
		"review modified code",
		"review this diff",
		"review the diff",
		"review the change",
		"review changed code",
		"code i changed",
		"방금 수정한 코드",
		"수정한 코드",
		"변경한 코드",
		"변경된 코드",
		"수정된 코드",
		"diff 리뷰",
		"diff 검토",
		"코드 리뷰",
	)
}

func classifyAcceptanceContractRequestClass(userText string, intent TurnIntent, readOnlyAnalysis bool, explicitEditRequest bool) (string, string) {
	decision := classifyAcceptanceContractRequestClassDecision(userText, intent, readOnlyAnalysis, explicitEditRequest)
	return decision.RequestClass, decision.Reason
}

func classifyAcceptanceContractRequestClassDecision(userText string, intent TurnIntent, readOnlyAnalysis bool, explicitEditRequest bool) ReviewRequestClassDecision {
	base := strings.TrimSpace(baseUserQueryText(userText))
	lower := strings.ToLower(base)
	paths := extractContractArtifactPaths(base)
	documentIntent := looksLikeDocumentAuthoringIntent(base) || looksLikeReviewArtifactAuthoringRequest(base)
	for _, path := range extractContractArtifactPaths(base) {
		if pathLooksLikeDocumentArtifact(path) {
			documentIntent = true
			break
		}
	}
	sourceModification := requestHasSourceModificationIntent(lower, paths)
	if explicitEditRequest && !documentIntent {
		sourceModification = true
	}
	if intent == TurnIntentEditCode && !documentIntent {
		sourceModification = true
	}
	decision := ReviewRequestClassDecision{
		RequestClass: reviewRequestClassGeneral,
		Reason:       "acceptance contract did not select a specialized lifecycle",
		Confidence:   0.62,
	}
	if documentIntent {
		decision.Signals = append(decision.Signals, "document_artifact_intent")
	}
	if sourceModification {
		decision.Signals = append(decision.Signals, "source_modification_intent")
	}
	if readOnlyAnalysis {
		decision.Signals = append(decision.Signals, "read_only_analysis")
	}
	if documentIntent && sourceModification {
		if requestExplicitlyOrdersReviewBeforeModification(lower) || requestLooksLikeInspectBugsThenFixConfirmed(lower) {
			decision.RequestClass = reviewRequestClassReviewThenModify
			decision.Reason = "acceptance contract saw mixed document and source-change signals; selected review-before-modify because edits must be limited to confirmed findings"
		} else {
			decision.RequestClass = reviewRequestClassModifyThenReview
			decision.Reason = "acceptance contract saw mixed document and source-change signals; selected modify-then-review because code behavior may change"
		}
		decision.Confidence = 0.74
		decision.AmbiguityWarnings = append(decision.AmbiguityWarnings, "document artifact request also includes source modification signals")
		decision.Normalize()
		return decision
	}
	if documentIntent {
		decision.RequestClass = reviewRequestClassDocumentArtifact
		decision.Reason = "acceptance contract classified the request as document/report artifact authoring"
		decision.Confidence = 0.86
		if hasTurnReviewIntent(lower) {
			decision.Confidence = 0.78
			decision.AmbiguityWarnings = append(decision.AmbiguityWarnings, "mixed review and document signals; selected document_artifact because no source edit was requested")
		}
		decision.Normalize()
		return decision
	}
	if requestLooksLikeValidationOnly(lower) {
		decision.RequestClass = reviewRequestClassValidationOnly
		decision.Reason = "acceptance contract classified the request as validation-only"
		decision.Confidence = 0.9
		decision.Normalize()
		return decision
	}
	if requestLooksLikeVerificationOnly(lower) {
		decision.RequestClass = reviewRequestClassVerificationOnly
		decision.Reason = "acceptance contract classified the request as verification-only"
		decision.Confidence = 0.9
		decision.Normalize()
		return decision
	}
	if requestLooksLikeReviewThenModify(lower) || requestLooksLikeInspectBugsThenFixConfirmed(lower) {
		decision.RequestClass = reviewRequestClassReviewThenModify
		decision.Reason = "acceptance contract classified the request as review-before-modify"
		decision.Confidence = 0.84
		if requestLooksLikeInspectBugsThenFixConfirmed(lower) {
			decision.AmbiguityWarnings = append(decision.AmbiguityWarnings, "bug-inspection plus confirmed-fix language is mixed; selected review_then_modify")
		}
		decision.Normalize()
		return decision
	}
	if requestLooksLikeReviewOfExistingChange(lower) {
		decision.RequestClass = reviewRequestClassReviewOnly
		decision.Reason = "acceptance contract classified the request as review of an existing change"
		decision.Confidence = 0.88
		decision.Normalize()
		return decision
	}
	if requestHasExplicitNoEditLanguage(lower) && hasTurnReviewIntent(lower) {
		decision.RequestClass = reviewRequestClassReviewOnly
		decision.Reason = "acceptance contract classified the request as explicit read-only review"
		decision.Confidence = 0.95
		decision.Normalize()
		return decision
	}
	if explicitEditRequest || intent == TurnIntentEditCode {
		decision.RequestClass = reviewRequestClassModifyThenReview
		decision.Reason = "acceptance contract classified the request as modify-then-review"
		decision.Confidence = 0.82
		decision.Normalize()
		return decision
	}
	if readOnlyAnalysis && (intent == TurnIntentReviewCode || hasTurnReviewIntent(lower)) {
		decision.RequestClass = reviewRequestClassReviewOnly
		decision.Reason = "acceptance contract classified the request as read-only review"
		decision.Confidence = 0.86
		decision.Normalize()
		return decision
	}
	decision.Normalize()
	return decision
}

func buildReviewRequestLifecycle(run *ReviewRun, session *Session) *ReviewRequestLifecycle {
	if run == nil {
		return nil
	}
	class := firstNonBlankString(run.RequestClass, run.RequestAnalysis.RequestClass)
	reason := firstNonBlankString(run.RequestAnalysis.RequestClassReason, "request class selected by review request analysis")
	routeQuality := reviewRouteQualityForRun(*run)
	lifecycle := &ReviewRequestLifecycle{
		RequestClass:             class,
		LifecycleKind:            firstNonBlankString(run.RequestAnalysis.LifecycleKind, reviewLifecycleKindForRun(run)),
		MixedFlow:                run.RequestAnalysis.MixedFlow,
		SecondaryRequestClasses:  run.RequestAnalysis.SecondaryRequestClasses,
		Phase:                    reviewLifecyclePhaseForRun(*run),
		RouteMode:                reviewRouteModeForRun(*run),
		Reason:                   reason,
		Timeline:                 reviewLifecycleTimelineForRun(run, session, nil, nil),
		ClassificationConfidence: run.RequestAnalysis.RequestClassConfidence,
		ClassificationAmbiguous:  run.RequestAnalysis.RequestClassAmbiguous,
		AmbiguityWarnings:        run.RequestAnalysis.AmbiguityWarnings,
		Contract:                 reviewLifecycleContractForClass(class),
		RouteQuality:             routeQuality.Status,
		RouteDegradedReasons:     routeQuality.Reasons,
		ReviewGateStatus:         firstNonBlankString(run.Gate.Verdict, run.Result.Verdict, "unknown"),
		RepairGateStatus:         reviewRepairGateStatus(*run),
		DocumentGateStatus:       reviewDocumentGateStatusForRun(*run, session),
		VerificationGateStatus:   reviewVerificationGateStatusForRun(*run),
		SecondPassStatus:         reviewSecondPassStatusLine(buildReviewSecondPassObservability(*run)),
		CrossReviewTriage:        reviewCrossReviewTriageStatusLine(buildReviewCrossReviewTriageSummary(run.CrossReviewTriage)),
		RemainingObligations:     reviewLifecycleRemainingObligations(*run),
		NextRecommendedCommand:   reviewLifecycleNextCommand(run.Gate.NextCommands),
	}
	lifecycle.Normalize()
	return lifecycle
}

func reviewLifecyclePhaseForRun(run ReviewRun) string {
	if len(run.StateTransitions) > 0 {
		last := run.StateTransitions[len(run.StateTransitions)-1]
		if strings.TrimSpace(last.To) != "" {
			return strings.TrimSpace(last.To)
		}
	}
	if strings.TrimSpace(run.Gate.Action) != "" {
		return reviewActionBoundaryState(run)
	}
	if len(run.Findings) > 0 || strings.TrimSpace(run.Gate.Verdict) != "" {
		return reviewStateGateDecision
	}
	if len(run.Evidence.Sources) > 0 {
		return reviewStateMainReview
	}
	return reviewStateCollectEvidence
}

func reviewRouteModeForRun(run ReviewRun) string {
	if run.SingleModelPolicy.Enabled {
		return reviewRouteModeSingleModel
	}
	if reviewRunHasReviewerRun(run, "cross_reviewer") || strings.TrimSpace(run.CapabilityManifest.CrossReviewModel) == "available" {
		return reviewRouteModeCrossModel
	}
	if len(run.ReviewerRuns) == 0 {
		return reviewRouteModeDeterministicOnly
	}
	return reviewRouteModeSingleModel
}

func reviewRepairGateStatus(run ReviewRun) string {
	if run.RepairPlan.Required || reviewRunNeedsRepair(run) {
		return "required"
	}
	if len(run.RepairFindings) > 0 {
		return "tracked"
	}
	return "not_required"
}

func reviewDocumentGateStatusForRun(run ReviewRun, session *Session) string {
	if normalizeReviewRequestClass(firstNonBlankString(run.RequestClass, run.RequestAnalysis.RequestClass)) != reviewRequestClassDocumentArtifact {
		return "not_applicable"
	}
	if session != nil {
		if sessionHasDocumentArtifactQualityAcceptedHarness(session) ||
			sessionHasDocumentArtifactContentAcceptedHarness(session) ||
			sessionHasApprovedDocumentArtifactOnlyHarness(session) {
			return "accepted"
		}
		if session.LastCodingHarnessReport != nil &&
			codingHarnessFindingsHaveBlockers(session.LastCodingHarnessReport.ArtifactQuality.Findings) {
			return "blocked"
		}
	}
	return "pending"
}

func reviewVerificationGateStatusForRun(run ReviewRun) string {
	if run.Evidence.VerificationFailed {
		return "failed"
	}
	if strings.TrimSpace(run.Evidence.VerificationSummary) != "" {
		return "recorded"
	}
	if run.Evidence.VerificationRequired ||
		(strings.TrimSpace(run.Evidence.VerificationSummary) == "" && reviewRunHasChangeEvidence(run) && run.Target != reviewTargetPlan) {
		return "gap_recorded"
	}
	return "not_required"
}

func reviewLifecycleRemainingObligations(run ReviewRun) []string {
	if run.ObligationLedger.TotalCount == 0 && len(run.ObligationLedger.Items) == 0 {
		return nil
	}
	return normalizeTaskStateList(run.ObligationLedger.Summary, 8)
}

func reviewLifecycleNextCommand(commands []ReviewNextCommand) string {
	if len(commands) == 0 {
		return ""
	}
	return strings.TrimSpace(commands[0].Command)
}

func buildRuntimeGateLifecycle(session *Session, action string, changedPaths []string, review *ReviewRun) *ReviewRequestLifecycle {
	if review != nil {
		return buildReviewRequestLifecycle(review, session)
	}
	class, reason := classifyRuntimeGateRequestClass(session, action, changedPaths)
	lifecycle := &ReviewRequestLifecycle{
		RequestClass:             class,
		LifecycleKind:            runtimeGateLifecycleKind(session, class),
		MixedFlow:                runtimeGateLifecycleMixedFlow(session),
		SecondaryRequestClasses:  runtimeGateLifecycleSecondaryClasses(session),
		Phase:                    "runtime_gate",
		Reason:                   reason,
		Timeline:                 reviewLifecycleTimelineForRuntimeGate(session, action, changedPaths, nil, nil),
		ClassificationConfidence: reviewRuntimeGateRequestClassConfidence(session),
		ClassificationAmbiguous:  reviewRuntimeGateRequestClassAmbiguous(session),
		AmbiguityWarnings:        reviewRuntimeGateRequestClassAmbiguityWarnings(session),
		Contract:                 reviewLifecycleContractForClass(class),
		DocumentGateStatus:       reviewRuntimeGateDocumentStatus(session, class),
		VerificationGateStatus:   reviewRuntimeGateVerificationStatus(session, class, changedPaths),
	}
	lifecycle.Normalize()
	return lifecycle
}

func classifyRuntimeGateRequestClass(session *Session, action string, changedPaths []string) (string, string) {
	if session == nil {
		return reviewRequestClassGeneral, "no session request context was available"
	}
	request := ""
	if session.AcceptanceContract != nil {
		if class := normalizeReviewRequestClass(session.AcceptanceContract.RequestClass); class != reviewRequestClassGeneral {
			return class, firstNonBlankString(session.AcceptanceContract.RequestClassReason, "request class came from the acceptance contract")
		}
		request = session.AcceptanceContract.SourcePrompt
	}
	if request == "" && session.TaskState != nil {
		request = session.TaskState.Goal
	}
	if request == "" {
		request = latestExternalOrUserMessageText(session.Messages)
	}
	if generatedDocumentArtifactGateAcceptedForRequest(session, request, changedPaths) ||
		changedPathsAreGeneratedDocumentArtifacts(session, request, changedPaths) {
		return reviewRequestClassDocumentArtifact, "runtime gate detected generated document artifact paths and accepted artifact-quality context"
	}
	readOnly := prefersReadOnlyAnalysisIntent(request) || looksLikeReviewInspectionOnlyRequest(request)
	class, reason := classifyAcceptanceContractRequestClass(request, classifyTurnIntent(request), readOnly, looksLikeExplicitEditIntent(request))
	if class == reviewRequestClassGeneral && strings.EqualFold(normalizeRuntimeGateAction(action), runtimeGateActionFinalAnswer) {
		return reviewRequestClassGeneral, "final-answer gate has no specialized lifecycle context"
	}
	return class, reason
}

func runtimeGateLifecycleKind(session *Session, class string) string {
	if session != nil && session.AcceptanceContract != nil {
		if kind := normalizeReviewLifecycleKind(session.AcceptanceContract.LifecycleKind); kind != reviewLifecycleKindGeneral {
			return kind
		}
		if request := strings.TrimSpace(session.AcceptanceContract.SourcePrompt); request != "" {
			readOnly := strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "analysis_only") ||
				prefersReadOnlyAnalysisIntent(request) ||
				looksLikeReviewInspectionOnlyRequest(request)
			explicitEdit := strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "edit") ||
				looksLikeExplicitEditIntent(request)
			decision := classifyAcceptanceContractRequestClassDecision(request, classifyTurnIntent(request), readOnly, explicitEdit)
			if normalizedClass := normalizeReviewRequestClass(class); normalizedClass != reviewRequestClassGeneral {
				decision.RequestClass = normalizedClass
			}
			decision = applyReviewLifecycleKindToDecision(decision, request, classifyTurnIntent(request), "", session.AcceptanceContract.Mode)
			if kind := normalizeReviewLifecycleKind(decision.LifecycleKind); kind != reviewLifecycleKindGeneral {
				return kind
			}
		}
	}
	return reviewLifecycleKindForRequestClass(class)
}

func runtimeGateLifecycleMixedFlow(session *Session) bool {
	if session == nil || session.AcceptanceContract == nil {
		return false
	}
	if session.AcceptanceContract.MixedFlow {
		return true
	}
	request := strings.TrimSpace(session.AcceptanceContract.SourcePrompt)
	if request == "" {
		return false
	}
	readOnly := strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "analysis_only") ||
		prefersReadOnlyAnalysisIntent(request) ||
		looksLikeReviewInspectionOnlyRequest(request)
	explicitEdit := strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "edit") ||
		looksLikeExplicitEditIntent(request)
	decision := classifyAcceptanceContractRequestClassDecision(request, classifyTurnIntent(request), readOnly, explicitEdit)
	decision = applyReviewLifecycleKindToDecision(decision, request, classifyTurnIntent(request), "", session.AcceptanceContract.Mode)
	return decision.MixedFlow
}

func runtimeGateLifecycleSecondaryClasses(session *Session) []string {
	if session == nil || session.AcceptanceContract == nil {
		return nil
	}
	classes := normalizeReviewRequestClasses(session.AcceptanceContract.SecondaryRequestClasses, 6)
	if len(classes) > 0 {
		return classes
	}
	request := strings.TrimSpace(session.AcceptanceContract.SourcePrompt)
	if request == "" {
		return nil
	}
	readOnly := strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "analysis_only") ||
		prefersReadOnlyAnalysisIntent(request) ||
		looksLikeReviewInspectionOnlyRequest(request)
	explicitEdit := strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "edit") ||
		looksLikeExplicitEditIntent(request)
	decision := classifyAcceptanceContractRequestClassDecision(request, classifyTurnIntent(request), readOnly, explicitEdit)
	decision = applyReviewLifecycleKindToDecision(decision, request, classifyTurnIntent(request), "", session.AcceptanceContract.Mode)
	return normalizeReviewRequestClasses(decision.SecondaryRequestClasses, 6)
}

func reviewRuntimeGateDocumentStatus(session *Session, class string) string {
	if normalizeReviewRequestClass(class) != reviewRequestClassDocumentArtifact {
		return "not_applicable"
	}
	if session == nil {
		return "unknown"
	}
	if sessionHasDocumentArtifactQualityAcceptedHarness(session) ||
		sessionHasDocumentArtifactContentAcceptedHarness(session) ||
		sessionHasApprovedDocumentArtifactOnlyHarness(session) {
		return "accepted"
	}
	if session.LastCodingHarnessReport != nil &&
		codingHarnessFindingsHaveBlockers(session.LastCodingHarnessReport.ArtifactQuality.Findings) {
		return "blocked"
	}
	return "pending"
}

func reviewRuntimeGateVerificationStatus(session *Session, class string, changedPaths []string) string {
	if normalizeReviewRequestClass(class) == reviewRequestClassDocumentArtifact {
		return "skipped_document_artifact_only"
	}
	if session != nil && session.LastVerification != nil {
		if session.LastVerification.WasSkipped() {
			return "skipped"
		}
		if session.LastVerification.HasFailures() {
			return "failed"
		}
		return "recorded"
	}
	if len(changedPaths) > 0 {
		return "gap_recorded"
	}
	return "not_required"
}

func reviewDocumentArtifactVerificationSkipReason(status string) string {
	switch strings.TrimSpace(status) {
	case "skipped_document_artifact_only":
		return "build/test verification skipped because this is a document-only artifact flow"
	case "skipped":
		return "verification was skipped and must be disclosed if finalizing"
	case "not_required", "not_applicable":
		return "build/test verification not required for this status snapshot"
	default:
		return ""
	}
}

func reviewRuntimeGateRequestClassConfidence(session *Session) float64 {
	if session == nil {
		return 0
	}
	if session.AcceptanceContract != nil && session.AcceptanceContract.RequestClassConfidence > 0 {
		return session.AcceptanceContract.RequestClassConfidence
	}
	request := ""
	readOnly := false
	explicitEdit := false
	intent := TurnIntentGeneral
	if session.AcceptanceContract != nil {
		request = session.AcceptanceContract.SourcePrompt
		readOnly = strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "analysis_only")
		explicitEdit = strings.EqualFold(strings.TrimSpace(session.AcceptanceContract.Mode), "edit")
	}
	if request == "" && session.TaskState != nil {
		request = session.TaskState.Goal
	}
	if request == "" {
		request = latestExternalOrUserMessageText(session.Messages)
	}
	if request == "" {
		return 0
	}
	intent = classifyTurnIntent(request)
	decision := classifyAcceptanceContractRequestClassDecision(request, intent, readOnly || prefersReadOnlyAnalysisIntent(request) || looksLikeReviewInspectionOnlyRequest(request), explicitEdit || looksLikeExplicitEditIntent(request))
	return decision.Confidence
}

func reviewRuntimeGateRequestClassAmbiguous(session *Session) bool {
	if session == nil {
		return false
	}
	if session.AcceptanceContract != nil {
		return session.AcceptanceContract.RequestClassAmbiguous || len(session.AcceptanceContract.AmbiguityWarnings) > 0
	}
	return len(reviewRuntimeGateRequestClassAmbiguityWarnings(session)) > 0
}

func reviewRuntimeGateRequestClassAmbiguityWarnings(session *Session) []string {
	if session == nil {
		return nil
	}
	if session.AcceptanceContract != nil && len(session.AcceptanceContract.AmbiguityWarnings) > 0 {
		return normalizeTaskStateList(session.AcceptanceContract.AmbiguityWarnings, 8)
	}
	request := ""
	if session.AcceptanceContract != nil {
		request = session.AcceptanceContract.SourcePrompt
	}
	if request == "" && session.TaskState != nil {
		request = session.TaskState.Goal
	}
	if request == "" {
		request = latestExternalOrUserMessageText(session.Messages)
	}
	if request == "" {
		return nil
	}
	decision := classifyAcceptanceContractRequestClassDecision(request, classifyTurnIntent(request), prefersReadOnlyAnalysisIntent(request) || looksLikeReviewInspectionOnlyRequest(request), looksLikeExplicitEditIntent(request))
	return normalizeTaskStateList(decision.AmbiguityWarnings, 8)
}

func reviewLifecycleContractForClass(class string) *ReviewLifecycleContract {
	class = normalizeReviewRequestClass(class)
	contract := &ReviewLifecycleContract{
		RequestClass: class,
	}
	switch class {
	case reviewRequestClassReviewOnly:
		contract.ReadOnly = true
		contract.FinalAnswerRequirements = []string{
			"findings first",
			"no file edits",
			"review scope",
			"residual evidence or verification risk",
		}
		contract.SkipRules = []string{
			"do not upgrade to modification without an explicit follow-up edit request",
		}
	case reviewRequestClassDocumentArtifact:
		contract.RequiresDocumentGate = true
		contract.RequiresVerificationDisclosure = true
		contract.FinalAnswerRequirements = []string{
			"artifact path",
			"artifact-quality status",
			"verification limitation or result",
			"remaining limitation",
		}
		contract.SkipRules = []string{
			"document-only output does not require code-review or build loops",
			"source-code changes in the same request use a modification lifecycle instead",
		}
	case reviewRequestClassReviewThenModify:
		contract.RequiresReviewBeforeModify = true
		contract.RequiresPostChangeReview = true
		contract.RequiresVerificationDisclosure = true
		contract.RequiresValidationDisclosure = true
		contract.FinalAnswerRequirements = []string{
			"changed files",
			"review findings and repair mapping",
			"post-change review or self-review result",
			"validation result or explicit gap",
			"residual risk",
		}
	case reviewRequestClassModifyThenReview:
		contract.RequiresPostChangeReview = true
		contract.RequiresVerificationDisclosure = true
		contract.RequiresValidationDisclosure = true
		contract.FinalAnswerRequirements = []string{
			"changed files",
			"post-change review or self-review result",
			"validation result or explicit gap",
			"residual risk",
		}
	case reviewRequestClassVerificationOnly:
		contract.ReadOnly = true
		contract.RequiresVerificationDisclosure = true
		contract.FinalAnswerRequirements = []string{
			"verification command or source",
			"pass/fail/skipped outcome",
			"scope covered",
			"remaining verification gap",
		}
		contract.SkipRules = []string{
			"do not start repair unless verification exposes a user-approved follow-up edit",
		}
	case reviewRequestClassValidationOnly:
		contract.ReadOnly = true
		contract.RequiresValidationDisclosure = true
		contract.FinalAnswerRequirements = []string{
			"validation target",
			"validation decision",
			"evidence checked",
			"remaining limitation",
		}
		contract.SkipRules = []string{
			"do not modify workspace files during validation-only flow",
		}
	default:
		contract.RequestClass = reviewRequestClassGeneral
		contract.FinalAnswerRequirements = []string{
			"answer the latest user request",
			"state verification gaps when relevant",
		}
	}
	contract.Normalize()
	return contract
}
