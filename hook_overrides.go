package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type HookOverrideRecord struct {
	ID        string    `json:"id"`
	RuleID    string    `json:"rule_id"`
	Workspace string    `json:"workspace"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type HookOverrideStore struct {
	Path string
}

func NewHookOverrideStore() *HookOverrideStore {
	return &HookOverrideStore{
		Path: filepath.Join(userConfigDir(), "hook-overrides.json"),
	}
}

func (s *HookOverrideStore) List(workspace string) ([]HookOverrideRecord, error) {
	records, err := s.load()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	workspace = normalizePersistentMemoryWorkspace(workspace)
	var out []HookOverrideRecord
	for _, record := range records {
		if !record.ExpiresAt.IsZero() && now.After(record.ExpiresAt) {
			continue
		}
		if workspace != "" && workspaceAffinityScore(workspace, record.Workspace) == 0 {
			continue
		}
		out = append(out, normalizeHookOverrideRecord(record))
	}
	return out, nil
}

func (s *HookOverrideStore) Append(record HookOverrideRecord) (HookOverrideRecord, error) {
	if s == nil {
		return HookOverrideRecord{}, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	record = normalizeHookOverrideRecord(record)
	records, err := s.load()
	if err != nil {
		return HookOverrideRecord{}, err
	}
	records = append(records, record)
	if err := s.save(records); err != nil {
		return HookOverrideRecord{}, err
	}
	return record, nil
}

func (s *HookOverrideStore) Remove(idOrRule string, workspace string, all bool) ([]HookOverrideRecord, error) {
	if s == nil {
		return nil, nil
	}
	unlock := lockFilePath(s.Path)
	defer unlock()
	records, err := s.load()
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(idOrRule)
	workspace = normalizePersistentMemoryWorkspace(workspace)
	var kept []HookOverrideRecord
	var removed []HookOverrideRecord
	for _, record := range records {
		matchWorkspace := workspace == "" || workspaceAffinityScore(workspace, record.Workspace) > 0
		matchID := strings.EqualFold(record.ID, query)
		matchRule := strings.EqualFold(record.RuleID, query)
		if all {
			if matchWorkspace {
				removed = append(removed, record)
				continue
			}
		} else if matchWorkspace && (matchID || matchRule) {
			removed = append(removed, record)
			continue
		}
		kept = append(kept, record)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	return removed, s.save(kept)
}

func (s *HookOverrideStore) IsActive(ruleID, workspace string, at time.Time) bool {
	active, err := s.ActiveRuleIDs(workspace, at)
	if err != nil {
		return false
	}
	return active[strings.ToLower(strings.TrimSpace(ruleID))]
}

func (s *HookOverrideStore) ActiveRuleIDs(workspace string, at time.Time) (map[string]bool, error) {
	if s == nil {
		return nil, nil
	}
	records, err := s.List(workspace)
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.RuleID) != "" && (record.ExpiresAt.IsZero() || !at.After(record.ExpiresAt)) {
			active[strings.ToLower(strings.TrimSpace(record.RuleID))] = true
		}
	}
	return active, nil
}

func normalizeHookOverrideRecord(record HookOverrideRecord) HookOverrideRecord {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	if strings.TrimSpace(record.ID) == "" {
		record.ID = fmt.Sprintf("ovr-%s-%03d", record.CreatedAt.Format("20060102-150405"), record.CreatedAt.Nanosecond()/1_000_000)
	}
	record.RuleID = strings.TrimSpace(record.RuleID)
	record.Workspace = normalizePersistentMemoryWorkspace(record.Workspace)
	record.Reason = strings.TrimSpace(record.Reason)
	return record
}

func (s *HookOverrideStore) load() ([]HookOverrideRecord, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var records []HookOverrideRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *HookOverrideStore) save(records []HookOverrideRecord) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return nil
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.Path, data, 0o644)
}
