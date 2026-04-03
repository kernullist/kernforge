package main

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type HookEvent string

const (
	HookUserPromptSubmit HookEvent = "UserPromptSubmit"
	HookPreToolUse       HookEvent = "PreToolUse"
	HookPostToolUse      HookEvent = "PostToolUse"
	HookPreEdit          HookEvent = "PreEdit"
	HookPostEdit         HookEvent = "PostEdit"
	HookPreVerification  HookEvent = "PreVerification"
	HookPostVerification HookEvent = "PostVerification"
	HookPreGitPush       HookEvent = "PreGitPush"
	HookPreCreatePR      HookEvent = "PreCreatePR"
)

type HookPayload map[string]any

type HookAction struct {
	Type              string   `json:"type"`
	Message           string   `json:"message,omitempty"`
	Label             string   `json:"label,omitempty"`
	Command           string   `json:"command,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	Scope             string   `json:"scope,omitempty"`
	Stage             string   `json:"stage,omitempty"`
	ContinueOnFailure *bool    `json:"continue_on_failure,omitempty"`
	StopOnFailure     *bool    `json:"stop_on_failure,omitempty"`
}

type HookMatch struct {
	ToolNames               []string `json:"tool_names,omitempty"`
	Paths                   []string `json:"paths,omitempty"`
	CommandsRegex           []string `json:"commands_regex,omitempty"`
	FileTags                []string `json:"file_tags,omitempty"`
	RiskTags                []string `json:"risk_tags,omitempty"`
	Branches                []string `json:"branches,omitempty"`
	ChangedFiles            []string `json:"changed_files,omitempty"`
	ContainsText            []string `json:"contains_text,omitempty"`
	EvidenceCategories      []string `json:"evidence_categories,omitempty"`
	EvidenceSubjects        []string `json:"evidence_subjects,omitempty"`
	EvidenceTags            []string `json:"evidence_tags,omitempty"`
	EvidenceOutcomes        []string `json:"evidence_outcomes,omitempty"`
	EvidenceSeverities      []string `json:"evidence_severities,omitempty"`
	SignalClasses           []string `json:"signal_classes,omitempty"`
	MinEvidenceRiskScore    *int     `json:"min_evidence_risk_score,omitempty"`
	MinEvidenceMatches      *int     `json:"min_evidence_matches,omitempty"`
	MaxEvidenceAgeHours     *int     `json:"max_evidence_age_hours,omitempty"`
	HasRecentFailedEvidence *bool    `json:"has_recent_failed_evidence,omitempty"`
	Interactive             *bool    `json:"interactive,omitempty"`
	Providers               []string `json:"providers,omitempty"`
	Models                  []string `json:"models,omitempty"`
}

type HookRule struct {
	ID       string      `json:"id"`
	Enabled  *bool       `json:"enabled,omitempty"`
	Priority int         `json:"priority,omitempty"`
	Events   []HookEvent `json:"events"`
	Match    HookMatch   `json:"match"`
	Action   HookAction  `json:"action"`
	Stop     bool        `json:"stop,omitempty"`
}

type HookFile struct {
	Enabled     *bool      `json:"enabled,omitempty"`
	StopOnMatch bool       `json:"stop_on_match,omitempty"`
	Rules       []HookRule `json:"rules"`
}

type HookNotice struct {
	RuleID  string
	Message string
}

type HookVerdict struct {
	Allow            bool
	Warns            []HookNotice
	DenyReason       string
	AskMessage       string
	ContextAdds      []string
	CheckpointNotes  []string
	VerificationAdds []VerificationStep
	MatchedRuleIDs   []string
}

type HookEngine struct {
	Enabled     bool
	StopOnMatch bool
	Rules       []HookRule
}

type HookRuntime struct {
	Engine           *HookEngine
	Ask              func(string) (bool, error)
	Print            func(string)
	CreateCheckpoint func(string) (CheckpointMetadata, error)
	Overrides        *HookOverrideStore
	FailClosed       bool
	Workspace        Workspace
	Session          *Session
	Config           Config
	Evidence         *EvidenceStore
}

func (rt *HookRuntime) Run(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
	if rt == nil || rt.Engine == nil || !rt.Engine.Enabled {
		return HookVerdict{Allow: true}, nil
	}
	payload = rt.enrichPayload(event, payload)
	engine := rt.effectiveEngine()
	verdict, err := engine.Evaluate(ctx, event, payload)
	if err != nil {
		if rt.FailClosed {
			return HookVerdict{}, fmt.Errorf("hook evaluation failed: %w", err)
		}
		if rt.Print != nil {
			rt.Print("Hook warning: " + err.Error())
		}
		return HookVerdict{Allow: true}, nil
	}
	for _, notice := range verdict.Warns {
		if rt.Print != nil {
			rt.Print(fmt.Sprintf("Hook warning [%s] %s", notice.RuleID, notice.Message))
		}
	}
	for _, note := range verdict.CheckpointNotes {
		if rt.CreateCheckpoint == nil {
			return HookVerdict{}, fmt.Errorf("hook checkpoint requested but checkpoint manager is unavailable")
		}
		meta, cpErr := rt.CreateCheckpoint(note)
		if cpErr != nil {
			return HookVerdict{}, fmt.Errorf("hook checkpoint failed: %w", cpErr)
		}
		summary := fmt.Sprintf("Auto-checkpoint created: %s (%s)", meta.ID, meta.Name)
		if rt.Print != nil {
			rt.Print(summary)
		}
		verdict.ContextAdds = append(verdict.ContextAdds, summary)
	}
	if verdict.AskMessage != "" {
		if rt.Ask == nil {
			return HookVerdict{}, fmt.Errorf("hook confirmation required but interactive confirmation is unavailable")
		}
		ok, askErr := rt.Ask("Hook confirmation: " + verdict.AskMessage)
		if askErr != nil {
			return HookVerdict{}, askErr
		}
		if !ok {
			return HookVerdict{}, fmt.Errorf("hook denied: %s", verdict.AskMessage)
		}
	}
	if verdict.DenyReason != "" {
		return HookVerdict{}, fmt.Errorf("hook denied: %s", verdict.DenyReason)
	}
	if !verdict.Allow {
		return HookVerdict{}, fmt.Errorf("hook denied")
	}
	return verdict, nil
}

func (rt *HookRuntime) effectiveEngine() *HookEngine {
	if rt == nil || rt.Engine == nil {
		return nil
	}
	if rt.Overrides == nil {
		return rt.Engine
	}
	now := time.Now()
	active, err := rt.Overrides.ActiveRuleIDs(rt.Workspace.BaseRoot, now)
	if err != nil {
		if rt.Print != nil {
			rt.Print("Hook warning: failed to read active overrides: " + err.Error())
		}
		return rt.Engine
	}
	engine := &HookEngine{
		Enabled:     rt.Engine.Enabled,
		StopOnMatch: rt.Engine.StopOnMatch,
	}
	for _, rule := range rt.Engine.Rules {
		if active[strings.ToLower(strings.TrimSpace(rule.ID))] {
			continue
		}
		engine.Rules = append(engine.Rules, rule)
	}
	return engine
}

func (rt *HookRuntime) enrichPayload(event HookEvent, payload HookPayload) HookPayload {
	if payload == nil {
		payload = HookPayload{}
	}
	payload["event"] = string(event)
	payload["timestamp"] = time.Now().Format(time.RFC3339)
	payload["interactive"] = rt.Workspace.Perms != nil && rt.Workspace.Perms.prompt != nil
	if rt.Session != nil {
		payload["session_id"] = rt.Session.ID
		payload["provider"] = rt.Session.Provider
		payload["model"] = rt.Session.Model
	}
	payload["workspace_root"] = rt.Workspace.BaseRoot
	payload["cwd"] = rt.Workspace.Root
	if rt.Evidence != nil {
		if failed, err := rt.Evidence.RecentFailures(rt.Workspace.BaseRoot, 12); err == nil && len(failed) > 0 {
			payload["has_recent_failed_evidence"] = true
			var categories []string
			var outcomes []string
			var subjects []string
			var tags []string
			var severities []string
			var signalClasses []string
			maxRiskScore := 0
			for _, record := range failed {
				if strings.TrimSpace(record.Category) != "" {
					categories = append(categories, record.Category)
				}
				if strings.TrimSpace(record.Outcome) != "" {
					outcomes = append(outcomes, record.Outcome)
				}
				if strings.TrimSpace(record.Subject) != "" {
					subjects = append(subjects, record.Subject)
				}
				if strings.TrimSpace(record.Severity) != "" {
					severities = append(severities, record.Severity)
				}
				if strings.TrimSpace(record.SignalClass) != "" {
					signalClasses = append(signalClasses, record.SignalClass)
				}
				if record.RiskScore > maxRiskScore {
					maxRiskScore = record.RiskScore
				}
				tags = append(tags, record.Tags...)
			}
			payload["recent_failed_evidence_categories"] = uniqueStrings(categories)
			payload["recent_failed_evidence_tags"] = uniqueStrings(tags)
			payload["recent_failed_evidence_outcomes"] = uniqueStrings(outcomes)
			payload["recent_failed_evidence_subjects"] = uniqueStrings(subjects)
			payload["recent_failed_evidence_severities"] = uniqueStrings(severities)
			payload["recent_failed_evidence_signal_classes"] = uniqueStrings(signalClasses)
			payload["recent_failed_evidence_max_risk_score"] = maxRiskScore
			payload["recent_failed_evidence_count"] = len(failed)
			var recordsPayload []map[string]any
			for _, record := range failed {
				recordsPayload = append(recordsPayload, map[string]any{
					"id":           record.ID,
					"kind":         record.Kind,
					"category":     record.Category,
					"subject":      record.Subject,
					"outcome":      record.Outcome,
					"severity":     record.Severity,
					"signal_class": record.SignalClass,
					"risk_score":   record.RiskScore,
					"tags":         append([]string(nil), record.Tags...),
					"created_at":   record.CreatedAt,
				})
			}
			payload["recent_failed_evidence_records"] = recordsPayload
		}
	}
	if selection := rt.Workspace.Selection(); selection != nil && selection.HasSelection() {
		payload["selection"] = map[string]any{
			"file":       relOrAbs(rt.Workspace.Root, selection.FilePath),
			"start_line": selection.StartLine,
			"end_line":   selection.EndLine,
			"tags":       append([]string(nil), selection.Tags...),
		}
	}
	return payload
}

func (e *HookEngine) Evaluate(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
	_ = ctx
	verdict := HookVerdict{Allow: true}
	if e == nil || !e.Enabled {
		return verdict, nil
	}
	rules := append([]HookRule(nil), e.Rules...)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}
		return rules[i].ID < rules[j].ID
	})
	for _, rule := range rules {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		if !hookRuleHasEvent(rule, event) {
			continue
		}
		matched, err := hookRuleMatches(rule, payload)
		if err != nil {
			return HookVerdict{}, err
		}
		if !matched {
			continue
		}
		verdict.MatchedRuleIDs = append(verdict.MatchedRuleIDs, rule.ID)
		switch strings.ToLower(strings.TrimSpace(rule.Action.Type)) {
		case "", "allow":
		case "warn":
			verdict.Warns = append(verdict.Warns, HookNotice{RuleID: rule.ID, Message: hookActionMessage(rule, "Hook rule matched.")})
		case "ask":
			if verdict.AskMessage == "" {
				verdict.AskMessage = hookActionMessage(rule, "Continue?")
			}
		case "deny":
			verdict.Allow = false
			verdict.DenyReason = hookActionMessage(rule, "Blocked by hook policy.")
		case "append_context", "append_review_context":
			message := strings.TrimSpace(rule.Action.Message)
			if message != "" {
				verdict.ContextAdds = append(verdict.ContextAdds, message)
			}
		case "create_checkpoint":
			note := strings.TrimSpace(rule.Action.Message)
			if note == "" {
				note = fmt.Sprintf("Hook checkpoint for %s", rule.ID)
			}
			verdict.CheckpointNotes = append(verdict.CheckpointNotes, note)
		case "add_verification_step":
			command := strings.TrimSpace(rule.Action.Command)
			if command == "" {
				return HookVerdict{}, fmt.Errorf("hook add_verification_step requires command")
			}
			label := strings.TrimSpace(rule.Action.Label)
			if label == "" {
				label = command
			}
			scope := strings.TrimSpace(rule.Action.Scope)
			if scope == "" {
				scope = "workspace"
			}
			stage := strings.TrimSpace(rule.Action.Stage)
			if stage == "" {
				stage = scope
			}
			step := VerificationStep{
				Label:             label,
				Command:           command,
				Scope:             scope,
				Stage:             stage,
				Tags:              append([]string(nil), rule.Action.Tags...),
				ContinueOnFailure: rule.Action.ContinueOnFailure != nil && *rule.Action.ContinueOnFailure,
				StopOnFailure:     rule.Action.StopOnFailure != nil && *rule.Action.StopOnFailure,
				Status:            VerificationPending,
			}
			verdict.VerificationAdds = append(verdict.VerificationAdds, step)
		default:
			return HookVerdict{}, fmt.Errorf("unsupported hook action: %s", rule.Action.Type)
		}
		if rule.Stop || e.StopOnMatch || verdict.DenyReason != "" {
			break
		}
	}
	return verdict, nil
}

func hookRuleHasEvent(rule HookRule, event HookEvent) bool {
	for _, item := range rule.Events {
		if item == event {
			return true
		}
	}
	return false
}

func hookActionMessage(rule HookRule, fallback string) string {
	if strings.TrimSpace(rule.Action.Message) != "" {
		return strings.TrimSpace(rule.Action.Message)
	}
	return fallback
}

func hookRuleMatches(rule HookRule, payload HookPayload) (bool, error) {
	if rule.Match.Interactive != nil {
		if boolValueFromAny(payload["interactive"]) != *rule.Match.Interactive {
			return false, nil
		}
	}
	if !hookMatchStringList(rule.Match.ToolNames, stringsValueFromAny(payload["tool_name"])) {
		return false, nil
	}
	if !hookMatchStringList(rule.Match.Providers, stringsValueFromAny(payload["provider"])) {
		return false, nil
	}
	if !hookMatchStringList(rule.Match.Models, stringsValueFromAny(payload["model"])) {
		return false, nil
	}
	if !hookMatchPatternList(rule.Match.Paths, collectPayloadPaths(payload)) {
		return false, nil
	}
	if !hookMatchPatternList(rule.Match.Branches, collectPayloadBranches(payload)) {
		return false, nil
	}
	if !hookMatchPatternList(rule.Match.ChangedFiles, stringSliceFromAny(payload["changed_files"])) {
		return false, nil
	}
	if !hookMatchTagList(rule.Match.FileTags, stringSliceFromAny(payload["file_tags"])) {
		return false, nil
	}
	if !hookMatchTagList(rule.Match.RiskTags, stringSliceFromAny(payload["risk_tags"])) {
		return false, nil
	}
	if !hookMatchTagList(rule.Match.EvidenceCategories, stringSliceFromAny(payload["recent_failed_evidence_categories"])) {
		return false, nil
	}
	if !hookMatchPatternList(rule.Match.EvidenceSubjects, stringSliceFromAny(payload["recent_failed_evidence_subjects"])) {
		return false, nil
	}
	if !hookMatchTagList(rule.Match.EvidenceTags, stringSliceFromAny(payload["recent_failed_evidence_tags"])) {
		return false, nil
	}
	if !hookMatchTagList(rule.Match.EvidenceOutcomes, stringSliceFromAny(payload["recent_failed_evidence_outcomes"])) {
		return false, nil
	}
	if !hookMatchTagList(rule.Match.EvidenceSeverities, stringSliceFromAny(payload["recent_failed_evidence_severities"])) {
		return false, nil
	}
	if !hookMatchTagList(rule.Match.SignalClasses, stringSliceFromAny(payload["recent_failed_evidence_signal_classes"])) {
		return false, nil
	}
	if rule.Match.MinEvidenceRiskScore != nil && intValueFromAny(payload["recent_failed_evidence_max_risk_score"]) < *rule.Match.MinEvidenceRiskScore {
		return false, nil
	}
	if rule.Match.MinEvidenceMatches != nil {
		if hookMatchingEvidenceCount(rule.Match, payload) < *rule.Match.MinEvidenceMatches {
			return false, nil
		}
	}
	if rule.Match.MaxEvidenceAgeHours != nil {
		if hookMatchingEvidenceCount(rule.Match, payload) == 0 {
			return false, nil
		}
	}
	if rule.Match.HasRecentFailedEvidence != nil {
		if boolValueFromAny(payload["has_recent_failed_evidence"]) != *rule.Match.HasRecentFailedEvidence {
			return false, nil
		}
	}
	if !hookMatchContains(rule.Match.ContainsText, collectPayloadText(payload)) {
		return false, nil
	}
	if !hookMatchRegex(rule.Match.CommandsRegex, stringsValueFromAny(payload["command"])) {
		return false, nil
	}
	return true, nil
}

func hookMatchingEvidenceCount(match HookMatch, payload HookPayload) int {
	rawRecords, ok := payload["recent_failed_evidence_records"]
	if !ok {
		return intValueFromAny(payload["recent_failed_evidence_count"])
	}
	records, ok := rawRecords.([]map[string]any)
	if !ok {
		return intValueFromAny(payload["recent_failed_evidence_count"])
	}
	count := 0
	for _, record := range records {
		if hookMatchSingleEvidenceRecord(match, record) {
			count++
		}
	}
	return count
}

func hookMatchSingleEvidenceRecord(match HookMatch, record map[string]any) bool {
	if !hookMatchStringList(match.EvidenceCategories, stringsValueFromAny(record["category"])) {
		return false
	}
	if !hookMatchPatternList(match.EvidenceSubjects, []string{stringsValueFromAny(record["subject"])}) {
		return false
	}
	if !hookMatchTagList(match.EvidenceTags, stringSliceFromAny(record["tags"])) {
		return false
	}
	if !hookMatchStringList(match.EvidenceOutcomes, stringsValueFromAny(record["outcome"])) {
		return false
	}
	if !hookMatchStringList(match.EvidenceSeverities, stringsValueFromAny(record["severity"])) {
		return false
	}
	if !hookMatchStringList(match.SignalClasses, stringsValueFromAny(record["signal_class"])) {
		return false
	}
	if match.MinEvidenceRiskScore != nil && intValueFromAny(record["risk_score"]) < *match.MinEvidenceRiskScore {
		return false
	}
	if match.MaxEvidenceAgeHours != nil {
		createdAt, ok := timeValueFromAny(record["created_at"])
		if !ok {
			return false
		}
		maxAge := time.Duration(*match.MaxEvidenceAgeHours) * time.Hour
		if maxAge < 0 || time.Since(createdAt) > maxAge {
			return false
		}
	}
	return true
}

func hookMatchStringList(patterns []string, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, item := range patterns {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return true
		}
	}
	return false
}

func hookMatchTagList(expected, actual []string) bool {
	if len(expected) == 0 {
		return true
	}
	if len(actual) == 0 {
		return false
	}
	for _, want := range expected {
		for _, got := range actual {
			if strings.EqualFold(strings.TrimSpace(want), strings.TrimSpace(got)) {
				return true
			}
		}
	}
	return false
}

func hookMatchContains(needles []string, haystack string) bool {
	if len(needles) == 0 {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(haystack))
	if lower == "" {
		return false
	}
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(needle))) {
			return true
		}
	}
	return false
}

func hookMatchRegex(patterns []string, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false
		}
		if re.MatchString(value) {
			return true
		}
	}
	return false
}

func hookMatchPatternList(patterns, values []string) bool {
	if len(patterns) == 0 {
		return true
	}
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		normalized := filepathToSlash(value)
		base := normalized
		if idx := strings.LastIndex(normalized, "/"); idx >= 0 {
			base = normalized[idx+1:]
		}
		for _, pattern := range patterns {
			for _, expanded := range expandGlobPattern(pattern) {
				if globMatch(expanded, normalized) || globMatch(expanded, base) {
					return true
				}
			}
			if globMatch(pattern, normalized) || globMatch(pattern, base) {
				return true
			}
		}
	}
	return false
}

func expandGlobPattern(pattern string) []string {
	pattern = filepathToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return nil
	}
	out := []string{pattern}
	if strings.Contains(pattern, "/**/") {
		out = append(out, strings.ReplaceAll(pattern, "/**/", "/"))
	}
	return uniqueStrings(out)
}

func globMatch(pattern, value string) bool {
	pattern = filepathToSlash(strings.TrimSpace(pattern))
	value = filepathToSlash(strings.TrimSpace(value))
	if pattern == "" {
		return false
	}
	reText := regexp.QuoteMeta(pattern)
	reText = strings.ReplaceAll(reText, "\\*\\*", ".*")
	reText = strings.ReplaceAll(reText, "\\*", "[^/]*")
	reText = strings.ReplaceAll(reText, "\\?", ".")
	re, err := regexp.Compile("^" + reText + "$")
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func filepathToSlash(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	return strings.TrimSpace(value)
}

func boolValueFromAny(value any) bool {
	b, _ := value.(bool)
	return b
}

func intValueFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func timeValueFromAny(value any) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		return v, !v.IsZero()
	case string:
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
		if err != nil {
			return time.Time{}, false
		}
		return parsed, !parsed.IsZero()
	default:
		return time.Time{}, false
	}
}

func stringsValueFromAny(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func stringSliceFromAny(value any) []string {
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...)
	case []any:
		var out []string
		for _, item := range items {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func collectPayloadPaths(payload HookPayload) []string {
	var out []string
	for _, key := range []string{"path", "absolute_path"} {
		if text := stringsValueFromAny(payload[key]); text != "" {
			out = append(out, text)
		}
	}
	out = append(out, stringSliceFromAny(payload["changed_files"])...)
	return uniqueStrings(out)
}

func collectPayloadBranches(payload HookPayload) []string {
	if branch := stringsValueFromAny(payload["branch"]); branch != "" {
		return []string{branch}
	}
	return nil
}

func collectPayloadText(payload HookPayload) string {
	var parts []string
	for _, key := range []string{"user_text", "command", "output", "error", "path", "branch"} {
		if text := stringsValueFromAny(payload[key]); text != "" {
			parts = append(parts, text)
		}
	}
	for _, key := range []string{"recent_failed_evidence_subjects"} {
		for _, item := range stringSliceFromAny(payload[key]) {
			parts = append(parts, item)
		}
	}
	return strings.Join(parts, "\n")
}
