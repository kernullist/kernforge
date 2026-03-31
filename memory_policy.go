package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type PersistentMemoryPolicy struct {
	AutoPrune           bool `json:"auto_prune,omitempty"`
	MaxRecords          int  `json:"max_records,omitempty"`
	WorkspaceMaxRecords int  `json:"workspace_max_records,omitempty"`
	ProtectRecent       int  `json:"protect_recent,omitempty"`
	KeepDaysDefault     int  `json:"keep_days_default,omitempty"`
	KeepDaysLow         int  `json:"keep_days_low,omitempty"`
	KeepDaysMedium      int  `json:"keep_days_medium,omitempty"`
	KeepDaysHigh        int  `json:"keep_days_high,omitempty"`
	KeepDaysTentative   int  `json:"keep_days_tentative,omitempty"`
	KeepDaysConfirmed   int  `json:"keep_days_confirmed,omitempty"`
}

type PersistentMemoryPruneResult struct {
	Scope         string
	Before        int
	After         int
	Deleted       int
	DeletedIDs    []string
	DeletedReason []string
}

func DefaultPersistentMemoryPolicy() PersistentMemoryPolicy {
	return PersistentMemoryPolicy{
		AutoPrune:           true,
		MaxRecords:          1200,
		WorkspaceMaxRecords: 300,
		ProtectRecent:       12,
		KeepDaysDefault:     180,
		KeepDaysLow:         45,
		KeepDaysMedium:      180,
		KeepDaysHigh:        365,
		KeepDaysTentative:   60,
		KeepDaysConfirmed:   365,
	}
}

func InitMemoryPolicyTemplate() string {
	data, err := json.MarshalIndent(DefaultPersistentMemoryPolicy(), "", "  ")
	if err != nil {
		return "{\n  \"auto_prune\": true\n}\n"
	}
	return string(data) + "\n"
}

func LoadPersistentMemoryPolicy(root string) (PersistentMemoryPolicy, error) {
	policy := DefaultPersistentMemoryPolicy()
	path := filepath.Join(root, userConfigDirName, "memory-policy.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return policy, nil
		}
		return policy, err
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		return policy, fmt.Errorf("parse %s: %w", path, err)
	}
	return policy, nil
}

func (s *PersistentMemoryStore) Prune(workspace string, policy PersistentMemoryPolicy, all bool) (PersistentMemoryPruneResult, error) {
	records, err := s.load()
	if err != nil {
		return PersistentMemoryPruneResult{}, err
	}
	result := PersistentMemoryPruneResult{Scope: "current workspace", Before: len(records)}
	if all {
		result.Scope = "all workspaces"
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	now := time.Now()

	type candidate struct {
		index  int
		record PersistentMemoryRecord
	}
	var filtered []candidate
	for i, record := range records {
		if all || workspaceAffinityScore(workspace, record.Workspace) > 0 {
			filtered = append(filtered, candidate{index: i, record: record})
		}
	}

	protected := map[string]bool{}
	protectRecent := policy.ProtectRecent
	if protectRecent < 0 {
		protectRecent = 0
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].record.CreatedAt.After(filtered[j].record.CreatedAt)
	})
	for i := 0; i < len(filtered) && i < protectRecent; i++ {
		protected[filtered[i].record.ID] = true
	}

	deleteSet := map[string]string{}
	for _, item := range filtered {
		record := item.record
		if protected[record.ID] {
			continue
		}
		maxAgeDays := effectivePersistentMemoryKeepDays(policy, record)
		if maxAgeDays > 0 {
			ageDays := int(now.Sub(record.CreatedAt).Hours() / 24)
			if ageDays > maxAgeDays {
				deleteSet[record.ID] = fmt.Sprintf("older than retention window (%d days)", maxAgeDays)
			}
		}
	}

	limit := policy.MaxRecords
	if limit <= 0 {
		limit = DefaultPersistentMemoryPolicy().MaxRecords
	}
	if !all && policy.WorkspaceMaxRecords > 0 {
		limit = policy.WorkspaceMaxRecords
	}
	if limit > 0 && len(filtered) > limit {
		for i := limit; i < len(filtered); i++ {
			record := filtered[i].record
			if protected[record.ID] {
				continue
			}
			if _, exists := deleteSet[record.ID]; !exists {
				deleteSet[record.ID] = "exceeds retention count limit"
			}
		}
	}

	var kept []PersistentMemoryRecord
	for _, record := range records {
		if reason, ok := deleteSet[record.ID]; ok {
			result.Deleted++
			result.DeletedIDs = append(result.DeletedIDs, record.ID)
			result.DeletedReason = append(result.DeletedReason, reason)
			continue
		}
		kept = append(kept, record)
	}
	result.After = len(kept)
	if result.Deleted > 0 {
		if err := s.save(kept); err != nil {
			return PersistentMemoryPruneResult{}, err
		}
	}
	return result, nil
}

func effectivePersistentMemoryKeepDays(policy PersistentMemoryPolicy, record PersistentMemoryRecord) int {
	switch record.Trust {
	case PersistentMemoryConfirmed:
		if policy.KeepDaysConfirmed > 0 {
			return policy.KeepDaysConfirmed
		}
	case PersistentMemoryTentative:
		if policy.KeepDaysTentative > 0 {
			return policy.KeepDaysTentative
		}
	}
	switch record.Importance {
	case PersistentMemoryHigh:
		if policy.KeepDaysHigh > 0 {
			return policy.KeepDaysHigh
		}
	case PersistentMemoryMedium:
		if policy.KeepDaysMedium > 0 {
			return policy.KeepDaysMedium
		}
	case PersistentMemoryLow:
		if policy.KeepDaysLow > 0 {
			return policy.KeepDaysLow
		}
	}
	return policy.KeepDaysDefault
}
