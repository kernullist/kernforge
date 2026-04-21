package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordShellBundleSupersedesOlderVerificationBundleForSameEditableLease(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "openai", "gpt-test", "", "default")
	session.TaskGraph = &TaskGraph{
		Nodes: []TaskNode{
			{
				ID:                 "plan-01",
				Title:              "Verify prior driver patch",
				Kind:               "edit",
				Status:             "in_progress",
				EditableLeasePaths: []string{"driver/monitor.inf"},
				LastUpdated:        time.Now(),
			},
			{
				ID:                 "plan-02",
				Title:              "Verify latest driver patch",
				Kind:               "edit",
				Status:             "ready",
				EditableLeasePaths: []string{"driver/monitor.inf"},
				LastUpdated:        time.Now(),
			},
		},
		LastUpdated: time.Now(),
	}
	session.TaskGraph.Normalize()
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}

	managerRoot := filepath.Join(root, "jobs")
	manager := NewBackgroundJobManager(managerRoot, session, store)

	oldJobDir := filepath.Join(managerRoot, session.ID, "job-old")
	if err := os.MkdirAll(oldJobDir, 0o755); err != nil {
		t.Fatalf("MkdirAll old job dir: %v", err)
	}
	oldJob := BackgroundShellJob{
		ID:             "job-old",
		CommandSummary: "go test ./driver/...",
		Status:         "running",
		OwnerNodeID:    "plan-01",
		StatusPath:     filepath.Join(oldJobDir, "status.json"),
		LogPath:        filepath.Join(oldJobDir, "output.log"),
		StartedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	session.UpsertBackgroundJob(oldJob)
	oldBundle := BackgroundShellBundle{
		ID:               "bundle-old",
		Summary:          "go test ./driver/...",
		CommandSummaries: []string{"go test ./driver/..."},
		JobIDs:           []string{oldJob.ID},
		OwnerNodeID:      "plan-01",
		OwnerLeasePaths:  []string{"driver/monitor.inf"},
		Status:           "running",
		LastSummary:      "completed=0 running=1 failed=0 canceled=0 total=1",
		VerificationLike: true,
		StartedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	session.UpsertBackgroundBundle(oldBundle)
	session.AttachBackgroundBundle(oldBundle, []BackgroundShellJob{oldJob})

	newJob := BackgroundShellJob{
		ID:             "job-new",
		CommandSummary: "go test ./... ./driver/...",
		Status:         "running",
		OwnerNodeID:    "plan-02",
		StartedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	session.UpsertBackgroundJob(newJob)
	newBundle, err := manager.RecordShellBundle([]BackgroundShellJob{newJob}, "plan-02", BackgroundShellBundleOptions{
		VerificationLike: true,
	})
	if err != nil {
		t.Fatalf("RecordShellBundle: %v", err)
	}

	updatedOldBundle, ok := session.BackgroundBundle("bundle-old")
	if !ok {
		t.Fatalf("expected superseded bundle to remain in session")
	}
	if updatedOldBundle.Status != "superseded" {
		t.Fatalf("expected old bundle to become superseded, got %#v", updatedOldBundle)
	}
	if updatedOldBundle.SupersededBy != newBundle.ID || updatedOldBundle.PreemptedBy != newBundle.ID {
		t.Fatalf("expected old bundle to point at new bundle %s, got %#v", newBundle.ID, updatedOldBundle)
	}
	if !strings.Contains(strings.ToLower(updatedOldBundle.LifecycleNote), "same editable lease") {
		t.Fatalf("expected same-lease supersede reason, got %#v", updatedOldBundle)
	}
	if len(newBundle.OwnerLeasePaths) != 1 || newBundle.OwnerLeasePaths[0] != "driver/monitor.inf" {
		t.Fatalf("expected new bundle to inherit owner lease paths, got %#v", newBundle)
	}
	oldNode, ok := session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected old owner node in task graph")
	}
	if canonicalTaskNodeStatus(oldNode.Status) != "ready" {
		t.Fatalf("expected old owner node to reopen as ready after supersede refresh, got %#v", oldNode)
	}
	if !strings.Contains(strings.ToLower(oldNode.LifecycleNote), "same editable lease") {
		t.Fatalf("expected owner node note to mention same editable lease, got %#v", oldNode)
	}
}

func TestMarkBundleLifecycleCompletesVerificationLikeOwnerNode(t *testing.T) {
	session := NewSession("C:\\workspace", "openai", "gpt-test", "", "default")
	session.SetSharedPlan([]PlanItem{
		{Step: "Patch driver/monitor.inf", Status: "in_progress"},
		{Step: "Summarize", Status: "pending"},
	})
	node, ok := session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected plan-01 task node")
	}
	node.Kind = "edit"
	node.Status = "in_progress"
	node.EditableLeasePaths = []string{"driver/monitor.inf"}
	node.LastUpdated = time.Now()
	session.TaskGraph.UpsertNode(node)

	bundle := BackgroundShellBundle{
		ID:               "bundle-verify-1",
		Summary:          "go test ./... ./driver/...",
		CommandSummaries: []string{"go test ./... ./driver/..."},
		JobIDs:           []string{"job-verify-1"},
		OwnerNodeID:      "plan-01",
		OwnerLeasePaths:  []string{"driver/monitor.inf"},
		Status:           "running",
		LastSummary:      "completed=0 running=1 failed=0 canceled=0 total=1",
		VerificationLike: true,
		StartedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	session.UpsertBackgroundBundle(bundle)
	session.AttachBackgroundBundle(bundle, nil)

	session.MarkBundleLifecycle("bundle-verify-1", "completed", "completed=1 running=0 failed=0 canceled=0 total=1")

	updatedBundle, ok := session.BackgroundBundle("bundle-verify-1")
	if !ok {
		t.Fatalf("expected updated bundle metadata")
	}
	if updatedBundle.Status != "completed" {
		t.Fatalf("expected bundle to become completed, got %#v", updatedBundle)
	}
	updatedNode, ok := session.TaskGraph.Node("plan-01")
	if !ok {
		t.Fatalf("expected owner node to stay attached")
	}
	if canonicalTaskNodeStatus(updatedNode.Status) != "completed" {
		t.Fatalf("expected verification completion to close owner node, got %#v", updatedNode)
	}
	if len(session.Plan) < 2 || session.Plan[0].Status != "completed" {
		t.Fatalf("expected first plan item to become completed, got %#v", session.Plan)
	}
	if session.Plan[1].Status != "in_progress" {
		t.Fatalf("expected next plan item to advance after verification completes, got %#v", session.Plan)
	}
}
