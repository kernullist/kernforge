package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadChurnAbortsWhenRotatingHarnessArtifacts guards the fix for a real
// session where the model fixated on a prior run's .kernforge/reviews/* files.
// Each distinct artifact path used to count as a new investigative path and reset
// the no-progress counter, so the guard never tripped and the turn burned ~10
// expensive model turns before another guard caught it. After the fix, reading
// the harness's own .kernforge artifacts is not progress, so the cumulative
// read-churn guard trips even while the model keeps "discovering" fresh artifact
// paths.
func TestReadChurnAbortsWhenRotatingHarnessArtifacts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("print('app')\n"), 0o644); err != nil {
		t.Fatalf("WriteFile app.py: %v", err)
	}
	reviewsDir := filepath.Join(root, ".kernforge", "reviews")
	if err := os.MkdirAll(reviewsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// One real source read (genuine progress), then a long run that keeps reading
	// DISTINCT .kernforge artifact paths. Distinct paths defeat the single-path
	// and identical-signature detectors; only the cumulative read-churn counter
	// can stop this, and only if artifact reads are not treated as progress.
	var replies []ChatResponse
	replies = append(replies, toolCallResponse("read_file", map[string]any{"path": "app.py"}))
	for i := 0; i < 30; i++ {
		name := filepath.Join(".kernforge", "reviews", "review-"+string(rune('a'+i%26))+"-"+string(rune('0'+i%10))+".json")
		rel := strings.ReplaceAll(name, "\\", "/")
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("WriteFile artifact: %v", err)
		}
		replies = append(replies, toolCallResponse("read_file", map[string]any{"path": rel}))
	}

	provider := &scriptedProviderClient{replies: replies}
	ws := Workspace{BaseRoot: root, Root: root}
	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	agent := &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), ".env에 gitlab 토큰을 넣어두고 사용하게 하자")
	if err != nil {
		t.Fatalf("read-churn guard should escalate to the user, not error, got: %v", err)
	}
	if strings.TrimSpace(reply) == "" {
		t.Fatalf("expected a read-churn escalation reply, got empty")
	}
	if provider.index >= len(replies) {
		t.Fatalf("guard did not stop early while rotating .kernforge artifacts: consumed %d/%d replies", provider.index, len(replies))
	}
}

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

	reply, err := agent.Reply(context.Background(), "rotate reads over the file set")
	if err != nil {
		t.Fatalf("read-churn guard should escalate to the user, not error, got: %v", err)
	}
	// The turn must STOP (bounded loop) as a clarification escalation naming the
	// churned files, rather than a bare error or an endless loop.
	if strings.TrimSpace(reply) == "" || !strings.Contains(reply, "a.txt") {
		t.Fatalf("expected a read-churn escalation reply naming the churned files, got: %q", reply)
	}
	// The guard must trip well before the script is exhausted; otherwise the
	// interleaved failing tool was still masking the loop.
	if provider.index >= len(replies) {
		t.Fatalf("guard did not stop early: consumed %d/%d replies", provider.index, len(replies))
	}
}
