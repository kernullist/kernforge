package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutoCheckpointControllerCreatesOnlyOncePerArm(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	manager := &CheckpointManager{
		Root: filepath.Join(t.TempDir(), "checkpoints"),
	}
	controller := &AutoCheckpointController{
		Manager: manager,
	}
	controller.Arm(true, workspace)

	first, err := controller.Prepare("write file.txt")
	if err != nil {
		t.Fatalf("Prepare first: %v", err)
	}
	if first == nil {
		t.Fatal("expected first auto checkpoint to be created")
	}
	second, err := controller.Prepare("replace in file.txt")
	if err != nil {
		t.Fatalf("Prepare second: %v", err)
	}
	if second != nil {
		t.Fatalf("expected second prepare to be skipped, got %#v", second)
	}
	items, err := manager.List(workspace)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly one auto checkpoint, got %d", len(items))
	}
}
