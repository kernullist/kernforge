package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const implementationPreferenceProfileSchemaVersion = 1

var ErrImplementationPreferenceConflict = errors.New("implementation preference profile revision conflict")

const (
	implementationPreferenceScopeProject = "project"
	implementationPreferenceScopeDomain  = "domain"
	implementationPreferenceScopeGlobal  = "global"
)

type ImplementationPreferenceRule struct {
	ID                       string    `json:"id"`
	Scope                    string    `json:"scope"`
	ScopeKey                 string    `json:"scope_key,omitempty"`
	Criterion                string    `json:"criterion"`
	Preference               string    `json:"preference"`
	PreferenceLabel          string    `json:"preference_label,omitempty"`
	SupportDecisionIDs       []string  `json:"support_decision_ids"`
	ContradictionDecisionIDs []string  `json:"contradiction_decision_ids,omitempty"`
	SupportProjectIDs        []string  `json:"support_project_ids,omitempty"`
	Confidence               float64   `json:"confidence"`
	Enabled                  bool      `json:"enabled"`
	Pinned                   bool      `json:"pinned,omitempty"`
	UserNote                 string    `json:"user_note,omitempty"`
	UserNoteRedacted         bool      `json:"user_note_redacted,omitempty"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type ImplementationPreferenceRuleOverride struct {
	Enabled          bool      `json:"enabled"`
	Pinned           bool      `json:"pinned,omitempty"`
	UserNote         string    `json:"user_note,omitempty"`
	UserNoteRedacted bool      `json:"user_note_redacted,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ImplementationPreferenceProfile struct {
	SchemaVersion int                                             `json:"schema_version"`
	Revision      int                                             `json:"revision"`
	GeneratedAt   time.Time                                       `json:"generated_at"`
	RecordCount   int                                             `json:"record_count"`
	SourceHash    string                                          `json:"source_hash,omitempty"`
	Rules         []ImplementationPreferenceRule                  `json:"rules"`
	Overrides     map[string]ImplementationPreferenceRuleOverride `json:"overrides,omitempty"`
}

type ImplementationPreferenceProfileStore struct {
	Path string
}

type implementationPreferenceCandidate struct {
	Scope       string
	ScopeKey    string
	Criterion   string
	Preference  string
	Label       string
	DecisionIDs []string
	ProjectIDs  []string
}

func NewImplementationPreferenceProfileStore() *ImplementationPreferenceProfileStore {
	return &ImplementationPreferenceProfileStore{
		Path: filepath.Join(userConfigDir(), "decision-rationales", "profiles", "profile.json"),
	}
}

func (s *ImplementationPreferenceProfileStore) Load() (ImplementationPreferenceProfile, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return ImplementationPreferenceProfile{}, nil
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return ImplementationPreferenceProfile{}, nil
		}
		return ImplementationPreferenceProfile{}, err
	}
	var profile ImplementationPreferenceProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return ImplementationPreferenceProfile{}, fmt.Errorf("parse implementation preference profile: %w", err)
	}
	profile = normalizeImplementationPreferenceProfile(profile)
	if err := validateImplementationPreferenceProfile(profile); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	return profile, nil
}

func (s *ImplementationPreferenceProfileStore) Save(profile ImplementationPreferenceProfile) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return fmt.Errorf("implementation preference profile store is not configured")
	}
	if err := ensureImplementationDecisionPrivateDir(filepath.Dir(s.Path)); err != nil {
		return err
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	unlockProcess, err := lockImplementationDecisionFile(s.Path + ".lock")
	if err != nil {
		return err
	}
	defer unlockProcess()
	profile = synchronizeImplementationPreferenceOverrides(profile)
	current, err := loadImplementationPreferenceProfileFile(s.Path)
	if err != nil {
		return err
	}
	if current.Revision > 0 {
		if profile.Revision != current.Revision {
			return fmt.Errorf("%w: profile is revision %d, expected %d", ErrImplementationPreferenceConflict, current.Revision, profile.Revision)
		}
		profile.Revision = current.Revision + 1
	} else {
		profile.Revision = 1
	}
	profile.GeneratedAt = time.Now().UTC()
	return saveImplementationPreferenceProfileFile(s.Path, profile)
}

func (s *ImplementationPreferenceProfileStore) Rebuild(decisions *ImplementationDecisionStore) (ImplementationPreferenceProfile, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return ImplementationPreferenceProfile{}, fmt.Errorf("implementation preference profile store is not configured")
	}
	if decisions == nil {
		return ImplementationPreferenceProfile{}, fmt.Errorf("implementation decision store is required")
	}
	if err := ensureImplementationDecisionPrivateDir(filepath.Dir(s.Path)); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	unlockProcess, err := lockImplementationDecisionFile(s.Path + ".lock")
	if err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	defer unlockProcess()
	records, err := decisions.List(ImplementationDecisionFilter{})
	if err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	previous, err := loadImplementationPreferenceProfileFile(s.Path)
	if err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	profile := BuildImplementationPreferenceProfile(records)
	if previous.Revision > 0 {
		profile.Revision = previous.Revision + 1
	} else {
		profile.Revision = 1
	}
	overrides := cloneImplementationPreferenceOverrides(previous.Overrides)
	for _, rule := range previous.Rules {
		if implementationPreferenceRuleHasOverride(rule) {
			overrides[rule.ID] = implementationPreferenceOverrideFromRule(rule)
		}
	}
	profile.Overrides = overrides
	for index := range profile.Rules {
		if override, ok := overrides[profile.Rules[index].ID]; ok {
			applyImplementationPreferenceOverride(&profile.Rules[index], override)
		}
	}
	if err := saveImplementationPreferenceProfileFile(s.Path, profile); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	return profile, nil
}

func (s *ImplementationPreferenceProfileStore) UpdateRule(ruleID string, expectedRevision int, mutate func(*ImplementationPreferenceRule) error) (ImplementationPreferenceProfile, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return ImplementationPreferenceProfile{}, fmt.Errorf("implementation preference profile store is not configured")
	}
	if strings.TrimSpace(ruleID) == "" || expectedRevision <= 0 || mutate == nil {
		return ImplementationPreferenceProfile{}, fmt.Errorf("rule id, positive expected revision, and mutation callback are required")
	}
	if err := ensureImplementationDecisionPrivateDir(filepath.Dir(s.Path)); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	unlockProcess, err := lockImplementationDecisionFile(s.Path + ".lock")
	if err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	defer unlockProcess()
	profile, err := loadImplementationPreferenceProfileFile(s.Path)
	if err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	if profile.Revision != expectedRevision {
		return ImplementationPreferenceProfile{}, fmt.Errorf("%w: profile is revision %d, expected %d", ErrImplementationPreferenceConflict, profile.Revision, expectedRevision)
	}
	index := -1
	for current := range profile.Rules {
		if profile.Rules[current].ID == strings.TrimSpace(ruleID) {
			index = current
			break
		}
	}
	if index < 0 {
		return ImplementationPreferenceProfile{}, os.ErrNotExist
	}
	if err := mutate(&profile.Rules[index]); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	profile.Rules[index].UpdatedAt = time.Now().UTC()
	if profile.Overrides == nil {
		profile.Overrides = map[string]ImplementationPreferenceRuleOverride{}
	}
	if implementationPreferenceRuleHasOverride(profile.Rules[index]) {
		profile.Overrides[profile.Rules[index].ID] = implementationPreferenceOverrideFromRule(profile.Rules[index])
	} else {
		delete(profile.Overrides, profile.Rules[index].ID)
	}
	profile.Revision++
	profile.GeneratedAt = time.Now().UTC()
	if err := saveImplementationPreferenceProfileFile(s.Path, profile); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	return normalizeImplementationPreferenceProfile(profile), nil
}

func loadImplementationPreferenceProfileFile(path string) (ImplementationPreferenceProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ImplementationPreferenceProfile{}, nil
		}
		return ImplementationPreferenceProfile{}, err
	}
	var profile ImplementationPreferenceProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return ImplementationPreferenceProfile{}, fmt.Errorf("parse implementation preference profile: %w", err)
	}
	profile = normalizeImplementationPreferenceProfile(profile)
	if err := validateImplementationPreferenceProfile(profile); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	return profile, nil
}

func saveImplementationPreferenceProfileFile(path string, profile ImplementationPreferenceProfile) error {
	profile = normalizeImplementationPreferenceProfile(profile)
	if err := validateImplementationPreferenceProfile(profile); err != nil {
		return err
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o600)
}

func BuildImplementationPreferenceProfile(records []ImplementationDecisionRecord) ImplementationPreferenceProfile {
	now := time.Now().UTC()
	profile := ImplementationPreferenceProfile{
		SchemaVersion: implementationPreferenceProfileSchemaVersion,
		GeneratedAt:   now,
		RecordCount:   len(records),
		SourceHash:    implementationPreferenceSourceHash(records),
	}
	candidates := map[string]*implementationPreferenceCandidate{}
	groupDecisions := map[string]map[string][]string{}
	for _, record := range records {
		if record.Status == implementationDecisionStatusDeleted || strings.TrimSpace(record.DecisionKind) == "" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(record.Provenance.Selection), "user") ||
			!strings.EqualFold(strings.TrimSpace(record.Provenance.Rationale), "user") ||
			strings.TrimSpace(record.SelectionReasonRaw) == "" ||
			record.DetectorConfidence < 0.80 {
			continue
		}
		preference, label := implementationDecisionPreference(record)
		if preference == "" {
			continue
		}
		contexts := []struct {
			scope string
			key   string
		}{}
		if strings.TrimSpace(record.ProjectID) != "" {
			contexts = append(contexts, struct {
				scope string
				key   string
			}{implementationPreferenceScopeProject, record.ProjectID})
		}
		for _, domain := range uniqueStrings(record.Domains) {
			if strings.TrimSpace(domain) != "" {
				contexts = append(contexts, struct {
					scope string
					key   string
				}{implementationPreferenceScopeDomain, strings.ToLower(strings.TrimSpace(domain))})
			}
		}
		contexts = append(contexts, struct {
			scope string
			key   string
		}{implementationPreferenceScopeGlobal, "all"})
		criterion := strings.ToLower(strings.TrimSpace(record.DecisionKind))
		for _, context := range contexts {
			groupKey := implementationPreferenceGroupKey(context.scope, context.key, criterion)
			if groupDecisions[groupKey] == nil {
				groupDecisions[groupKey] = map[string][]string{}
			}
			groupDecisions[groupKey][preference] = append(groupDecisions[groupKey][preference], record.ID)
			candidateKey := groupKey + "|" + preference
			candidate := candidates[candidateKey]
			if candidate == nil {
				candidate = &implementationPreferenceCandidate{
					Scope:      context.scope,
					ScopeKey:   context.key,
					Criterion:  criterion,
					Preference: preference,
					Label:      label,
				}
				candidates[candidateKey] = candidate
			}
			candidate.DecisionIDs = append(candidate.DecisionIDs, record.ID)
			if strings.TrimSpace(record.ProjectID) != "" {
				candidate.ProjectIDs = append(candidate.ProjectIDs, record.ProjectID)
			}
		}
	}
	for _, candidate := range candidates {
		candidate.DecisionIDs = sortedUniqueStrings(candidate.DecisionIDs)
		candidate.ProjectIDs = sortedUniqueStrings(candidate.ProjectIDs)
		groupKey := implementationPreferenceGroupKey(candidate.Scope, candidate.ScopeKey, candidate.Criterion)
		var contradictions []string
		for preference, ids := range groupDecisions[groupKey] {
			if preference != candidate.Preference {
				contradictions = append(contradictions, ids...)
			}
		}
		contradictions = sortedUniqueStrings(contradictions)
		if !implementationPreferenceCandidatePromoted(*candidate, len(contradictions)) {
			continue
		}
		rule := ImplementationPreferenceRule{
			ID:                       "preference-" + shortStableID(groupKey+"|"+candidate.Preference),
			Scope:                    candidate.Scope,
			ScopeKey:                 candidate.ScopeKey,
			Criterion:                candidate.Criterion,
			Preference:               candidate.Preference,
			PreferenceLabel:          candidate.Label,
			SupportDecisionIDs:       candidate.DecisionIDs,
			ContradictionDecisionIDs: contradictions,
			SupportProjectIDs:        candidate.ProjectIDs,
			Confidence:               implementationPreferenceConfidence(*candidate, len(contradictions)),
			Enabled:                  true,
			UpdatedAt:                now,
		}
		profile.Rules = append(profile.Rules, rule)
	}
	sort.SliceStable(profile.Rules, func(i, j int) bool {
		left := profile.Rules[i]
		right := profile.Rules[j]
		if left.Scope != right.Scope {
			return implementationPreferenceScopeRank(left.Scope) < implementationPreferenceScopeRank(right.Scope)
		}
		if left.ScopeKey != right.ScopeKey {
			return left.ScopeKey < right.ScopeKey
		}
		if left.Criterion != right.Criterion {
			return left.Criterion < right.Criterion
		}
		return left.Preference < right.Preference
	})
	return profile
}

func implementationPreferenceSourceHash(records []ImplementationDecisionRecord) string {
	entries := make([]string, 0, len(records))
	for _, record := range records {
		if record.Status == implementationDecisionStatusDeleted {
			continue
		}
		entries = append(entries, strings.Join([]string{
			record.ID,
			fmt.Sprintf("%d", record.Revision),
			record.UpdatedAt.UTC().Format(time.RFC3339Nano),
			record.Status,
		}, "\x00"))
	}
	sort.Strings(entries)
	digest := sha256.Sum256([]byte(strings.Join(entries, "\x01")))
	return "journal-" + hex.EncodeToString(digest[:16])
}

func implementationDecisionPreference(record ImplementationDecisionRecord) (string, string) {
	if strings.TrimSpace(record.CustomSelection) != "" {
		label := strings.TrimSpace(record.CustomSelection)
		return "custom:" + shortStableID(strings.ToLower(strings.Join(strings.Fields(label), " "))), label
	}
	for _, option := range record.Options {
		if option.ID == record.SelectedOptionID {
			label := strings.TrimSpace(option.Label)
			return strings.ToLower(strings.TrimSpace(option.ID)) + "@" + shortStableID(strings.ToLower(strings.Join(strings.Fields(label), " "))), label
		}
	}
	return "", ""
}

func implementationPreferenceCandidatePromoted(candidate implementationPreferenceCandidate, contradictionCount int) bool {
	support := len(candidate.DecisionIDs)
	projects := len(candidate.ProjectIDs)
	if contradictionCount > 0 {
		ratio := float64(support) / float64(support+contradictionCount)
		if support <= contradictionCount || ratio < (2.0/3.0) {
			return false
		}
	}
	switch candidate.Scope {
	case implementationPreferenceScopeProject:
		return support >= 2
	case implementationPreferenceScopeDomain:
		return support >= 3 && projects >= 2
	case implementationPreferenceScopeGlobal:
		return support >= 5 && projects >= 3
	default:
		return false
	}
}

func implementationPreferenceConfidence(candidate implementationPreferenceCandidate, contradictionCount int) float64 {
	base := 0.35
	switch candidate.Scope {
	case implementationPreferenceScopeProject:
		base = 0.45
	case implementationPreferenceScopeDomain:
		base = 0.55
	case implementationPreferenceScopeGlobal:
		base = 0.60
	}
	confidence := base + float64(len(candidate.DecisionIDs))*0.07 + float64(len(candidate.ProjectIDs))*0.04 - float64(contradictionCount)*0.08
	if confidence < 0.10 {
		confidence = 0.10
	}
	if confidence > 0.95 {
		confidence = 0.95
	}
	return confidence
}

func implementationPreferenceGroupKey(scope string, scopeKey string, criterion string) string {
	return strings.ToLower(strings.TrimSpace(scope)) + "|" + strings.ToLower(strings.TrimSpace(scopeKey)) + "|" + strings.ToLower(strings.TrimSpace(criterion))
}

func normalizeImplementationPreferenceProfile(profile ImplementationPreferenceProfile) ImplementationPreferenceProfile {
	if profile.SchemaVersion == 0 {
		profile.SchemaVersion = implementationPreferenceProfileSchemaVersion
	}
	if profile.Revision == 0 && (profile.RecordCount > 0 || len(profile.Rules) > 0 || !profile.GeneratedAt.IsZero()) {
		profile.Revision = 1
	}
	if profile.GeneratedAt.IsZero() {
		profile.GeneratedAt = time.Now().UTC()
	}
	profile.SourceHash = strings.TrimSpace(profile.SourceHash)
	normalizedOverrides := map[string]ImplementationPreferenceRuleOverride{}
	for rawID, override := range profile.Overrides {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		override.UserNote = strings.TrimSpace(override.UserNote)
		if redacted, report := redactSensitiveText(override.UserNote); report.Redacted {
			override.UserNote = redacted
			override.UserNoteRedacted = true
		}
		if override.UpdatedAt.IsZero() {
			override.UpdatedAt = profile.GeneratedAt
		}
		normalizedOverrides[id] = override
	}
	if len(normalizedOverrides) == 0 {
		profile.Overrides = nil
	} else {
		profile.Overrides = normalizedOverrides
	}
	for index := range profile.Rules {
		rule := &profile.Rules[index]
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Scope = strings.ToLower(strings.TrimSpace(rule.Scope))
		rule.ScopeKey = strings.TrimSpace(rule.ScopeKey)
		rule.Criterion = strings.ToLower(strings.TrimSpace(rule.Criterion))
		rule.Preference = strings.ToLower(strings.TrimSpace(rule.Preference))
		rule.PreferenceLabel = strings.TrimSpace(rule.PreferenceLabel)
		rule.SupportDecisionIDs = sortedUniqueStrings(rule.SupportDecisionIDs)
		rule.ContradictionDecisionIDs = sortedUniqueStrings(rule.ContradictionDecisionIDs)
		rule.SupportProjectIDs = sortedUniqueStrings(rule.SupportProjectIDs)
		rule.UserNote = strings.TrimSpace(rule.UserNote)
		if redacted, report := redactSensitiveText(rule.UserNote); report.Redacted {
			rule.UserNote = redacted
			rule.UserNoteRedacted = true
		}
		if rule.UpdatedAt.IsZero() {
			rule.UpdatedAt = profile.GeneratedAt
		}
		if override, ok := profile.Overrides[rule.ID]; ok {
			applyImplementationPreferenceOverride(rule, override)
		}
	}
	return profile
}

func validateImplementationPreferenceProfile(profile ImplementationPreferenceProfile) error {
	if profile.SchemaVersion != implementationPreferenceProfileSchemaVersion {
		return fmt.Errorf("unsupported implementation preference profile schema version %d", profile.SchemaVersion)
	}
	if profile.Revision <= 0 {
		return fmt.Errorf("implementation preference profile revision must be positive")
	}
	if profile.SourceHash != "" {
		encoded := strings.TrimPrefix(profile.SourceHash, "journal-")
		if len(profile.SourceHash) != len("journal-")+32 || encoded == profile.SourceHash {
			return fmt.Errorf("implementation preference profile has an invalid source hash")
		}
		if _, err := hex.DecodeString(encoded); err != nil {
			return fmt.Errorf("implementation preference profile has an invalid source hash")
		}
	}
	seen := map[string]bool{}
	for _, rule := range profile.Rules {
		if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Criterion) == "" || strings.TrimSpace(rule.Preference) == "" {
			return fmt.Errorf("implementation preference rule requires id, criterion, and preference")
		}
		if seen[rule.ID] {
			return fmt.Errorf("duplicate implementation preference rule id %q", rule.ID)
		}
		seen[rule.ID] = true
		switch rule.Scope {
		case implementationPreferenceScopeProject, implementationPreferenceScopeDomain:
			if strings.TrimSpace(rule.ScopeKey) == "" {
				return fmt.Errorf("implementation preference rule %s requires a scope key", rule.ID)
			}
		case implementationPreferenceScopeGlobal:
		default:
			return fmt.Errorf("unsupported implementation preference scope %q", rule.Scope)
		}
		if rule.Confidence < 0 || rule.Confidence > 1 {
			return fmt.Errorf("implementation preference rule %s has invalid confidence", rule.ID)
		}
		if len(rule.SupportDecisionIDs) == 0 {
			return fmt.Errorf("implementation preference rule %s has no support decisions", rule.ID)
		}
		if len(rule.UserNote) > 4096 || len(rule.SupportDecisionIDs) > 2048 || len(rule.ContradictionDecisionIDs) > 2048 {
			return fmt.Errorf("implementation preference rule %s exceeds the storage limit", rule.ID)
		}
	}
	for id, override := range profile.Overrides {
		if strings.TrimSpace(id) == "" || len(id) > 200 {
			return fmt.Errorf("implementation preference override has an invalid rule id")
		}
		if len(override.UserNote) > 4096 {
			return fmt.Errorf("implementation preference override %s exceeds the storage limit", id)
		}
		if override.UpdatedAt.IsZero() {
			return fmt.Errorf("implementation preference override %s is missing updated_at", id)
		}
	}
	return nil
}

func synchronizeImplementationPreferenceOverrides(profile ImplementationPreferenceProfile) ImplementationPreferenceProfile {
	profile.Overrides = cloneImplementationPreferenceOverrides(profile.Overrides)
	for _, rule := range profile.Rules {
		if implementationPreferenceRuleHasOverride(rule) {
			profile.Overrides[rule.ID] = implementationPreferenceOverrideFromRule(rule)
		} else {
			delete(profile.Overrides, rule.ID)
		}
	}
	if len(profile.Overrides) == 0 {
		profile.Overrides = nil
	}
	return profile
}

func cloneImplementationPreferenceOverrides(source map[string]ImplementationPreferenceRuleOverride) map[string]ImplementationPreferenceRuleOverride {
	cloned := make(map[string]ImplementationPreferenceRuleOverride, len(source))
	for id, override := range source {
		cloned[id] = override
	}
	return cloned
}

func implementationPreferenceRuleHasOverride(rule ImplementationPreferenceRule) bool {
	return !rule.Enabled || rule.Pinned || strings.TrimSpace(rule.UserNote) != "" || rule.UserNoteRedacted
}

func implementationPreferenceOverrideFromRule(rule ImplementationPreferenceRule) ImplementationPreferenceRuleOverride {
	return ImplementationPreferenceRuleOverride{
		Enabled:          rule.Enabled,
		Pinned:           rule.Pinned,
		UserNote:         rule.UserNote,
		UserNoteRedacted: rule.UserNoteRedacted,
		UpdatedAt:        rule.UpdatedAt,
	}
}

func applyImplementationPreferenceOverride(rule *ImplementationPreferenceRule, override ImplementationPreferenceRuleOverride) {
	if rule == nil {
		return
	}
	rule.Enabled = override.Enabled
	rule.Pinned = override.Pinned
	rule.UserNote = override.UserNote
	rule.UserNoteRedacted = override.UserNoteRedacted
	if !override.UpdatedAt.IsZero() {
		rule.UpdatedAt = override.UpdatedAt
	}
}

func sortedUniqueStrings(items []string) []string {
	out := uniqueStrings(items)
	sort.Strings(out)
	return out
}

func implementationPreferenceScopeRank(scope string) int {
	switch scope {
	case implementationPreferenceScopeProject:
		return 0
	case implementationPreferenceScopeDomain:
		return 1
	case implementationPreferenceScopeGlobal:
		return 2
	default:
		return 3
	}
}
