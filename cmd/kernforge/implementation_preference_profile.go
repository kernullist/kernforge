package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	implementationPreferenceProfileSchemaVersion = 1
	implementationPreferenceProfileMaxBytes      = 64 << 20
	implementationPreferenceEvidenceMaxItems     = 2048
	implementationPreferenceRulesMaxItems        = 4096
	implementationPreferenceOverridesMaxItems    = 4096
)

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
	return loadImplementationPreferenceProfileFile(s.Path)
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
		if !implementationPreferenceProfileSourceEquivalent(current, profile) {
			return fmt.Errorf("implementation preference profile source-derived fields cannot be changed by Save")
		}
		profile.Revision = current.Revision + 1
	} else {
		profile.Revision = 1
	}
	profile.GeneratedAt = time.Now().UTC()
	return saveImplementationPreferenceProfileFile(s.Path, profile)
}

func (s *ImplementationPreferenceProfileStore) Rebuild(decisions *ImplementationDecisionStore) (ImplementationPreferenceProfile, error) {
	return s.RebuildContext(context.Background(), decisions)
}

func (s *ImplementationPreferenceProfileStore) RebuildContext(ctx context.Context, decisions *ImplementationDecisionStore) (ImplementationPreferenceProfile, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return ImplementationPreferenceProfile{}, fmt.Errorf("implementation preference profile store is not configured")
	}
	if decisions == nil {
		return ImplementationPreferenceProfile{}, fmt.Errorf("implementation decision store is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	if err := ensureImplementationDecisionPrivateDir(filepath.Dir(s.Path)); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	unlock, err := lockFilePathContext(ctx, s.Path)
	if err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	defer unlock()
	unlockProcess, err := lockImplementationDecisionFileContext(ctx, s.Path+".lock")
	if err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	defer unlockProcess()
	if err := ctx.Err(); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
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
	profile.Overrides = implementationPreferenceBoundedOverrides(profile.Rules, profile.Overrides, implementationPreferenceOverridesMaxItems)
	if err := ctx.Err(); err != nil {
		return ImplementationPreferenceProfile{}, err
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
	originalRule := profile.Rules[index]
	originalRule.SupportDecisionIDs = append([]string(nil), originalRule.SupportDecisionIDs...)
	originalRule.ContradictionDecisionIDs = append([]string(nil), originalRule.ContradictionDecisionIDs...)
	originalRule.SupportProjectIDs = append([]string(nil), originalRule.SupportProjectIDs...)
	if err := mutate(&profile.Rules[index]); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	if !implementationPreferenceRuleSourceEquivalent(originalRule, profile.Rules[index]) {
		return ImplementationPreferenceProfile{}, fmt.Errorf("implementation preference source-derived rule fields cannot be changed")
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
	profile.Overrides = implementationPreferenceBoundedOverrides(profile.Rules, profile.Overrides, implementationPreferenceOverridesMaxItems)
	profile.Revision++
	profile.GeneratedAt = time.Now().UTC()
	if err := saveImplementationPreferenceProfileFile(s.Path, profile); err != nil {
		return ImplementationPreferenceProfile{}, err
	}
	return normalizeImplementationPreferenceProfile(profile), nil
}

func loadImplementationPreferenceProfileFile(path string) (ImplementationPreferenceProfile, error) {
	data, err := readImplementationDecisionFile(path, implementationPreferenceProfileMaxBytes)
	if err != nil {
		if os.IsNotExist(err) {
			return ImplementationPreferenceProfile{}, nil
		}
		return ImplementationPreferenceProfile{}, err
	}
	var profile ImplementationPreferenceProfile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profile); err != nil {
		return ImplementationPreferenceProfile{}, fmt.Errorf("parse implementation preference profile: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ImplementationPreferenceProfile{}, fmt.Errorf("parse implementation preference profile: trailing data")
	}
	if err := validateStoredImplementationPreferenceProfile(profile); err != nil {
		return ImplementationPreferenceProfile{}, err
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
	data = append(data, '\n')
	if len(data) > implementationPreferenceProfileMaxBytes {
		return fmt.Errorf("implementation preference profile exceeds the storage limit")
	}
	if err := ensureImplementationDecisionPathNoLinks(path); err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o600)
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
			SupportDecisionIDs:       implementationPreferenceBoundedStrings(candidate.DecisionIDs, implementationPreferenceEvidenceMaxItems),
			ContradictionDecisionIDs: implementationPreferenceBoundedStrings(contradictions, implementationPreferenceEvidenceMaxItems),
			SupportProjectIDs:        implementationPreferenceBoundedStrings(candidate.ProjectIDs, implementationPreferenceEvidenceMaxItems),
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
		data, err := json.Marshal(record)
		if err != nil {
			data = []byte(fmt.Sprintf("%#v", record))
		}
		recordDigest := sha256.Sum256(data)
		entries = append(entries, record.ID+"\x00"+hex.EncodeToString(recordDigest[:]))
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
		if redacted, report := redactSensitiveText(rule.PreferenceLabel); report.Redacted {
			rule.PreferenceLabel = redacted
		}
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
	if profile.RecordCount < 0 || len(profile.Rules) > implementationPreferenceRulesMaxItems || len(profile.Overrides) > implementationPreferenceOverridesMaxItems {
		return fmt.Errorf("implementation preference profile exceeds the storage limit")
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
		if len(rule.ID) > 200 || len(rule.ScopeKey) > 1024 || len(rule.Criterion) > 256 || len(rule.Preference) > 256 || len(rule.PreferenceLabel) > 4096 {
			return fmt.Errorf("implementation preference rule %s exceeds the storage limit", rule.ID)
		}
		for _, value := range []string{rule.ID, rule.ScopeKey, rule.Criterion, rule.Preference} {
			if _, report := redactSensitiveText(value); report.Redacted {
				return fmt.Errorf("implementation preference rule %s contains sensitive-looking identifier text", rule.ID)
			}
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
		if len(rule.UserNote) > 4096 || len(rule.SupportDecisionIDs) > implementationPreferenceEvidenceMaxItems || len(rule.ContradictionDecisionIDs) > implementationPreferenceEvidenceMaxItems || len(rule.SupportProjectIDs) > implementationPreferenceEvidenceMaxItems {
			return fmt.Errorf("implementation preference rule %s exceeds the storage limit", rule.ID)
		}
		for _, decisionID := range append(append([]string{}, rule.SupportDecisionIDs...), rule.ContradictionDecisionIDs...) {
			if !validImplementationDecisionID(decisionID) {
				return fmt.Errorf("implementation preference rule %s has an invalid decision id", rule.ID)
			}
			if _, report := redactSensitiveText(decisionID); report.Redacted {
				return fmt.Errorf("implementation preference rule %s contains sensitive-looking decision id", rule.ID)
			}
		}
		for _, projectID := range rule.SupportProjectIDs {
			if len(projectID) > 1024 {
				return fmt.Errorf("implementation preference rule %s exceeds the storage limit", rule.ID)
			}
			if _, report := redactSensitiveText(projectID); report.Redacted {
				return fmt.Errorf("implementation preference rule %s contains sensitive-looking project id", rule.ID)
			}
		}
	}
	for id, override := range profile.Overrides {
		if strings.TrimSpace(id) == "" || len(id) > 200 {
			return fmt.Errorf("implementation preference override has an invalid rule id")
		}
		if _, report := redactSensitiveText(id); report.Redacted {
			return fmt.Errorf("implementation preference override has a sensitive-looking rule id")
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

func validateStoredImplementationPreferenceProfile(profile ImplementationPreferenceProfile) error {
	if profile.SchemaVersion != implementationPreferenceProfileSchemaVersion {
		return fmt.Errorf("unsupported implementation preference profile schema version %d", profile.SchemaVersion)
	}
	if profile.Revision <= 0 || profile.GeneratedAt.IsZero() {
		return fmt.Errorf("stored implementation preference profile is missing required lifecycle metadata")
	}
	for _, rule := range profile.Rules {
		if rule.UpdatedAt.IsZero() {
			return fmt.Errorf("stored implementation preference rule is missing updated_at")
		}
	}
	seenOverrides := map[string]bool{}
	for rawID, override := range profile.Overrides {
		id := strings.TrimSpace(rawID)
		if id == "" || id != rawID || seenOverrides[id] || override.UpdatedAt.IsZero() {
			return fmt.Errorf("stored implementation preference override has invalid lifecycle metadata")
		}
		seenOverrides[id] = true
	}
	return nil
}

func implementationPreferenceBoundedStrings(items []string, limit int) []string {
	if len(items) > limit {
		items = items[:limit]
	}
	return append([]string(nil), items...)
}

func implementationPreferenceRuleSourceEquivalent(left ImplementationPreferenceRule, right ImplementationPreferenceRule) bool {
	left.Enabled = false
	left.Pinned = false
	left.UserNote = ""
	left.UserNoteRedacted = false
	left.UpdatedAt = time.Time{}
	right.Enabled = false
	right.Pinned = false
	right.UserNote = ""
	right.UserNoteRedacted = false
	right.UpdatedAt = time.Time{}
	return reflect.DeepEqual(left, right)
}

func implementationPreferenceProfileSourceEquivalent(left ImplementationPreferenceProfile, right ImplementationPreferenceProfile) bool {
	if left.SchemaVersion != right.SchemaVersion || left.RecordCount != right.RecordCount || left.SourceHash != right.SourceHash || len(left.Rules) != len(right.Rules) {
		return false
	}
	for index := range left.Rules {
		if !implementationPreferenceRuleSourceEquivalent(left.Rules[index], right.Rules[index]) {
			return false
		}
	}
	return true
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
	profile.Overrides = implementationPreferenceBoundedOverrides(profile.Rules, profile.Overrides, implementationPreferenceOverridesMaxItems)
	return profile
}

func implementationPreferenceBoundedOverrides(rules []ImplementationPreferenceRule, source map[string]ImplementationPreferenceRuleOverride, limit int) map[string]ImplementationPreferenceRuleOverride {
	if limit <= 0 || len(source) == 0 {
		return nil
	}
	result := make(map[string]ImplementationPreferenceRuleOverride, min(limit, len(source)))
	active := map[string]bool{}
	for _, rule := range rules {
		if len(result) >= limit {
			break
		}
		override, ok := source[rule.ID]
		if !ok || !implementationPreferenceRuleHasOverride(rule) {
			continue
		}
		result[rule.ID] = override
		active[rule.ID] = true
	}
	type overrideEntry struct {
		id       string
		override ImplementationPreferenceRuleOverride
	}
	orphans := make([]overrideEntry, 0, len(source))
	for id, override := range source {
		if active[id] || !implementationPreferenceOverrideHasEffect(override) {
			continue
		}
		orphans = append(orphans, overrideEntry{id: id, override: override})
	}
	sort.SliceStable(orphans, func(i, j int) bool {
		if !orphans[i].override.UpdatedAt.Equal(orphans[j].override.UpdatedAt) {
			return orphans[i].override.UpdatedAt.After(orphans[j].override.UpdatedAt)
		}
		return orphans[i].id < orphans[j].id
	})
	for _, entry := range orphans {
		if len(result) >= limit {
			break
		}
		result[entry.id] = entry.override
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func implementationPreferenceOverrideHasEffect(override ImplementationPreferenceRuleOverride) bool {
	return !override.Enabled || override.Pinned || strings.TrimSpace(override.UserNote) != "" || override.UserNoteRedacted
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
