package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadChurnAbortsDespiteInterleavedFailingTool is the D-B regression. The
// observed 2h27m hang rotated read_file over the same file set while a failing
// git_status/git_diff (exit 128/129 on a non-repo) was retried between rereads.
// The interleaved non-read batch used to reset BOTH the single-path detector and
// the cumulative read-churn counter, so neither ever tripped and the loop spun
// for hours. After D-B, only a genuine workspace mutation resets the cumulative
// no-new-path read-churn run, so the interleaved failing tool can no longer mask
// the rotating reread and the turn aborts in finite steps.
func TestReadChurnAbortsDespiteInterleavedFailingTool(t *testing.T) {
	root := t.TempDir() // not a git repo: git_status will fail every time.
	files := []string{"a.txt", "b.txt", "c.txt"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte("content of "+name+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	// Build a long alternating script: read one of the rotating files, then a
	// failing git_status, repeated well past the abort threshold. The loop must
	// stop before consuming the whole script.
	// Vary read ranges and alternate between two distinct failing tools so the
	// generic identical-signature repeated-tool detector cannot catch this; only
	// the cumulative read-churn no-progress counter can. This mirrors the real
	// run, which interleaved git_status AND git_diff and re-read varied ranges.
	gitTools := []string{"git_status", "git_diff"}
	var replies []ChatResponse
	for i := 0; i < 60; i++ {
		name := files[i%len(files)]
		offset := (i % 3) + 1 // vary the read so its signature keeps changing
		replies = append(replies, toolCallResponse("read_file", map[string]any{"path": name, "offset": offset}))
		replies = append(replies, toolCallResponse(gitTools[i%len(gitTools)], map[string]any{}))
	}

	provider := &scriptedProviderClient{replies: replies}
	ws := Workspace{BaseRoot: root, Root: root}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws), NewGitStatusTool(ws), NewGitDiffTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	_, err := agent.Reply(context.Background(), "rotate reads over the file set")
	if err == nil {
		t.Fatalf("expected the rotating-reread guard to stop the turn, got nil error")
	}
	if !strings.Contains(err.Error(), "re-reading the same") {
		t.Fatalf("expected read-churn abort error, got: %v", err)
	}
	// The guard must trip well before the script is exhausted; otherwise the
	// interleaved failing tool was still masking the loop.
	if provider.index >= len(replies) {
		t.Fatalf("guard did not stop early: consumed %d/%d replies", provider.index, len(replies))
	}
}
