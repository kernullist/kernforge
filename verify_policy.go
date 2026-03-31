package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type VerificationPolicy struct {
	DisableDefaults bool                        `json:"disable_defaults,omitempty"`
	Defaults        []VerificationPolicyDefault `json:"defaults,omitempty"`
	Steps           []VerificationPolicyStep    `json:"steps,omitempty"`
}

type VerificationPolicyDefault struct {
	Match             string   `json:"match"`
	Priority          int      `json:"priority,omitempty"`
	When              []string `json:"when,omitempty"`
	OnlyIf            []string `json:"only_if,omitempty"`
	ExcludeWhen       []string `json:"exclude_when,omitempty"`
	ContinueOnFailure *bool    `json:"continue_on_failure,omitempty"`
	StopOnFailure     *bool    `json:"stop_on_failure,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

type VerificationPolicyStep struct {
	Label             string   `json:"label"`
	Command           string   `json:"command"`
	Stage             string   `json:"stage,omitempty"`
	Mode              string   `json:"mode,omitempty"`
	Priority          int      `json:"priority,omitempty"`
	When              []string `json:"when,omitempty"`
	OnlyIf            []string `json:"only_if,omitempty"`
	ExcludeWhen       []string `json:"exclude_when,omitempty"`
	ContinueOnFailure *bool    `json:"continue_on_failure,omitempty"`
	StopOnFailure     *bool    `json:"stop_on_failure,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

func InitVerifyPolicyTemplate() string {
	sample := VerificationPolicy{
		Defaults: []VerificationPolicyDefault{
			{
				Match:             "go vet workspace",
				Priority:          250,
				OnlyIf:            []string{"file:go.mod"},
				ContinueOnFailure: boolPtr(false),
				StopOnFailure:     boolPtr(true),
				Tags:              []string{"static-analysis"},
			},
		},
		Steps: []VerificationPolicyStep{
			{
				Label:             "go test ./integration/...",
				Command:           "go test ./integration/...",
				Stage:             "workspace",
				Priority:          150,
				When:              []string{"internal/auth/*.go", "cmd/**/*.go"},
				OnlyIf:            []string{"mode:adaptive"},
				ContinueOnFailure: boolPtr(true),
				StopOnFailure:     boolPtr(false),
				Tags:              []string{"integration", "auth"},
			},
		},
	}
	data, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		return "{\n  \"steps\": []\n}\n"
	}
	return string(data) + "\n"
}

func LoadVerificationPolicy(root string) (VerificationPolicy, error) {
	path := filepath.Join(root, userConfigDirName, "verify.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return VerificationPolicy{}, nil
		}
		return VerificationPolicy{}, err
	}
	var policy VerificationPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return VerificationPolicy{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return policy, nil
}

func applyVerificationPolicy(root string, steps []VerificationStep, changed []string, mode VerificationMode, policy VerificationPolicy) ([]VerificationStep, string) {
	if policy.DisableDefaults {
		steps = nil
	}
	steps = append([]VerificationStep(nil), steps...)
	for i := range steps {
		for _, item := range policy.Defaults {
			if verificationDefaultMatches(root, item, steps[i], changed, mode) {
				steps[i].PlannerPriority += item.Priority
				if item.ContinueOnFailure != nil {
					steps[i].ContinueOnFailure = *item.ContinueOnFailure
				}
				if item.StopOnFailure != nil {
					steps[i].StopOnFailure = *item.StopOnFailure
				}
				steps[i].Tags = uniqueStrings(append(steps[i].Tags, item.Tags...))
			}
		}
	}

	var added []string
	for _, item := range policy.Steps {
		if !verificationPolicyStepEnabled(root, item, changed, mode) {
			continue
		}
		if verificationStepExists(steps, item) {
			continue
		}
		stage := strings.TrimSpace(item.Stage)
		if stage == "" {
			stage = "workspace"
		}
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = strings.TrimSpace(item.Command)
		}
		steps = append(steps, VerificationStep{
			Label:             label,
			Command:           strings.TrimSpace(item.Command),
			Scope:             stage,
			Stage:             stage,
			Tags:              append([]string(nil), item.Tags...),
			ContinueOnFailure: item.ContinueOnFailure != nil && *item.ContinueOnFailure,
			StopOnFailure:     item.StopOnFailure != nil && *item.StopOnFailure,
			Status:            VerificationPending,
			PlannerPriority:   item.Priority,
		})
		added = append(added, label)
	}

	note := ""
	if policy.DisableDefaults {
		note = joinSentence(note, "Verify policy disabled the default verification steps.")
	}
	if len(added) > 0 {
		note = joinSentence(note, "Verify policy added custom steps: "+strings.Join(added, ", "))
	}
	return steps, note
}

func verificationDefaultMatches(root string, item VerificationPolicyDefault, step VerificationStep, changed []string, mode VerificationMode) bool {
	match := strings.TrimSpace(item.Match)
	if match == "" {
		return false
	}
	if !verificationPatternsMatch(item.When, changed) {
		return false
	}
	if !verificationConditionsAllMatch(root, item.OnlyIf, changed, mode) {
		return false
	}
	if verificationConditionsAnyMatch(root, item.ExcludeWhen, changed, mode) {
		return false
	}
	key := verificationHistoryKey(step)
	return strings.EqualFold(match, key) ||
		strings.EqualFold(match, step.Command) ||
		strings.EqualFold(match, step.Label)
}

func verificationPolicyStepEnabled(root string, step VerificationPolicyStep, changed []string, mode VerificationMode) bool {
	if strings.TrimSpace(step.Command) == "" {
		return false
	}
	modeText := strings.ToLower(strings.TrimSpace(step.Mode))
	if modeText != "" && modeText != "any" && modeText != strings.ToLower(string(mode)) {
		return false
	}
	if !verificationPatternsMatch(step.When, changed) {
		return false
	}
	if !verificationConditionsAllMatch(root, step.OnlyIf, changed, mode) {
		return false
	}
	if verificationConditionsAnyMatch(root, step.ExcludeWhen, changed, mode) {
		return false
	}
	return true
}

func verificationConditionsAllMatch(root string, conditions []string, changed []string, mode VerificationMode) bool {
	if len(conditions) == 0 {
		return true
	}
	for _, condition := range conditions {
		if !matchVerificationCondition(root, condition, changed, mode) {
			return false
		}
	}
	return true
}

func verificationConditionsAnyMatch(root string, conditions []string, changed []string, mode VerificationMode) bool {
	if len(conditions) == 0 {
		return false
	}
	for _, condition := range conditions {
		if matchVerificationCondition(root, condition, changed, mode) {
			return true
		}
	}
	return false
}

func matchVerificationCondition(root, condition string, changed []string, mode VerificationMode) bool {
	trimmed := strings.TrimSpace(condition)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "file:"):
		target := strings.TrimSpace(trimmed[len("file:"):])
		if target == "" {
			return false
		}
		return exists(filepath.Join(root, filepath.FromSlash(target)))
	case strings.HasPrefix(lower, "script:"):
		script := strings.TrimSpace(trimmed[len("script:"):])
		return hasScript(packageScripts(filepath.Join(root, "package.json")), script)
	case strings.HasPrefix(lower, "mode:"):
		want := strings.TrimSpace(trimmed[len("mode:"):])
		return strings.EqualFold(want, string(mode))
	case strings.HasPrefix(lower, "changed:"):
		pattern := strings.TrimSpace(trimmed[len("changed:"):])
		return verificationPatternsMatch([]string{pattern}, changed)
	default:
		return false
	}
}

func verificationPatternsMatch(patterns []string, changed []string) bool {
	if len(patterns) == 0 {
		return true
	}
	if len(changed) == 0 {
		return false
	}
	for _, rawPath := range changed {
		path := filepath.ToSlash(strings.TrimSpace(rawPath))
		base := filepath.Base(path)
		for _, pattern := range patterns {
			if matchVerificationPattern(pattern, path) || matchVerificationPattern(pattern, base) {
				return true
			}
		}
	}
	return false
}

func matchVerificationPattern(pattern, value string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	value = filepath.ToSlash(strings.TrimSpace(value))
	if pattern == "" || value == "" {
		return false
	}
	regex := regexp.QuoteMeta(pattern)
	regex = strings.ReplaceAll(regex, `\*\*`, `.*`)
	regex = strings.ReplaceAll(regex, `\*`, `[^/]*`)
	regex = strings.ReplaceAll(regex, `\?`, `.`)
	ok, err := regexp.MatchString("^"+regex+"$", value)
	return err == nil && ok
}

func verificationStepExists(steps []VerificationStep, item VerificationPolicyStep) bool {
	label := strings.TrimSpace(item.Label)
	command := strings.TrimSpace(item.Command)
	for _, step := range steps {
		if command != "" && strings.EqualFold(step.Command, command) {
			return true
		}
		if label != "" && strings.EqualFold(step.Label, label) {
			return true
		}
	}
	return false
}
