package main

import (
	"fmt"
	"strings"
	"time"
)

type AutoCheckpointController struct {
	Manager   *CheckpointManager
	Workspace string
	Enabled   bool
	Pending   bool
	Last      *CheckpointMetadata
}

func (c *AutoCheckpointController) Arm(enabled bool, workspace string) {
	c.Enabled = enabled
	c.Pending = enabled
	c.Workspace = strings.TrimSpace(workspace)
	c.Last = nil
}

func (c *AutoCheckpointController) Clear() {
	c.Pending = false
	c.Enabled = false
}

func (c *AutoCheckpointController) Prepare(reason string) (*CheckpointMetadata, error) {
	return c.PrepareAtWorkspace(reason, "")
}

func (c *AutoCheckpointController) PrepareAtWorkspace(reason string, workspace string) (*CheckpointMetadata, error) {
	if c == nil || c.Manager == nil || !c.Enabled || !c.Pending {
		return nil, nil
	}
	targetWorkspace := strings.TrimSpace(workspace)
	if targetWorkspace == "" {
		targetWorkspace = c.Workspace
	}
	name := "auto-before-edit-" + time.Now().Format("20060102-150405")
	if strings.TrimSpace(reason) != "" {
		name += " " + compactPersistentMemoryText(reason, 80)
	}
	meta, err := c.Manager.Create(targetWorkspace, name)
	if err != nil {
		return nil, err
	}
	c.Pending = false
	c.Last = &meta
	return &meta, nil
}

func formatAutoCheckpointMessage(meta *CheckpointMetadata) string {
	if meta == nil {
		return ""
	}
	return fmt.Sprintf("Created automatic checkpoint %s (%s)", meta.ID, meta.Name)
}
