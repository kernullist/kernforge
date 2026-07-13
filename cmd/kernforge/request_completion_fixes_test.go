package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestReplyLooksLikePreActionNarration pins the narration detector: intent-to-act
// phrasing matches, a completion report / plain answer does not.
func TestReplyLooksLikePreActionNarration(t *testing.T) {
	narration := []string{
		"I'll now implement the fix in app.py and update the logic.",
		"Let me go ahead and modify the handler.",
		"먼저 app.py의 버그를 수정하겠습니다.",
		"다음 단계로 로직을 구현하겠습니다.",
	}
	for _, s := range narration {
		if !replyLooksLikePreActionNarration(s) {
			t.Fatalf("expected narration to be detected: %q", s)
		}
	}
	notNarration := []string{
		"",
		"I changed app.py to add the nil check and verified the build passes.",
		"app.py에 nil 체크를 추가했고 빌드가 통과했습니다.",
		"The bug is caused by an off-by-one in the loop bound.",
	}
	for _, s := range notNarration {
		if replyLooksLikePreActionNarration(s) {
			t.Fatalf("expected NON-narration: %q", s)
		}
	}
}

// TestPreActionNarrationBouncedThenModelActs verifies that a text-only plan
// narration on an edit request is not accepted as the final answer: the model is
// re-prompted, then actually calls the edit tool and completes.
func TestPreActionNarrationBouncedThenModelActs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}
	session := NewSession(root, "scripted", "main-model", "", "full")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root, Perms: NewPermissionManager(ModeBypass, nil)}

	patchTool := &sequenceTool{
		name:    "apply_patch",
		errs:    []error{nil},
		outputs: []string{"Applied patch to app.py."},
	}
	provider := &scriptedProviderClient{replies: []ChatResponse{
		{Message: Message{Role: "assistant", Text: "I'll now implement the fix in app.py and apply the change."}},
		toolCallResponse("apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: app.py\n@@\n-x = 1\n+x = 2\n*** End Patch\n"}),
		{Message: Message{Role: "assistant", Text: testModificationFinalAnswer("app.py", "targeted check passed.", "no known remaining blocker.")}},
	}}
	agent := &Agent{
		Config:    Config{AutoLocale: boolPtr(false), PermissionMode: string(ModeBypass)},
		Client:    provider,
		Tools:     NewToolRegistry(patchTool, NewReadFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "Edit app.py to fix the bug and apply the change to the file.")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if patchTool.calls < 1 {
		t.Fatalf("expected the model to actually call the edit tool after the narration nudge, got %d call(s)", patchTool.calls)
	}
	if strings.Contains(reply, "I'll now implement") {
		t.Fatalf("the plan narration must not be accepted as the final answer, got %q", reply)
	}
}

// TestReplyFalselyClaimsEditToolsUnavailable pins the detector used to bounce
// models that invent an analysis-only story after a non-edit tool was blocked.
func TestReplyFalselyClaimsEditToolsUnavailable(t *testing.T) {
	claims := []string{
		"현재 analysis-only 모드라서 편집 도구가 차단되어 있습니다.\n\n대신 작성할 코드를 여기서 보여드리겠습니다.\n\n```python\nprint(1)\n```",
		"Edit tools are blocked in analysis-only mode. Save the following code manually:\n```go\npackage main\n```",
		"I cannot edit files because this is read-only analysis. Please create the file manually.",
	}
	for _, s := range claims {
		if !replyFalselyClaimsEditToolsUnavailable(s) {
			t.Fatalf("expected false analysis-only claim: %q", s)
		}
	}
	honest := []string{
		"Changed files: main.go. Self-review: ok. Validation: not run. Remaining risk: none.",
		"write_file failed with: disk full. I could not create the file.",
		"Plan mode is read-only; switch to /permissions edit to allow changes.",
	}
	for _, s := range honest {
		if replyFalselyClaimsEditToolsUnavailable(s) {
			t.Fatalf("expected NON-claim: %q", s)
		}
	}
}

// TestFalseAnalysisOnlyClaimBouncedThenModelWrites verifies that inventing an
// analysis-only / edit-tools-unavailable excuse (after a blocked shell write
// style failure) is bounced, then the model must use write_file.
func TestFalseAnalysisOnlyClaimBouncedThenModelWrites(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "main-model", "", "full")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root, Perms: NewPermissionManager(ModeBypass, nil)}

	falseClaim := "현재 analysis-only 모드라서 편집 도구가 차단되어 있습니다.\n\n대신 작성할 코드를 여기서 보여드리겠습니다.\n\n```python\nprint('hi')\n```\n\n파일로 저장하시려면 말씀해 주세요."
	provider := &scriptedProviderClient{replies: []ChatResponse{
		{Message: Message{Role: "assistant", Text: falseClaim}},
		toolCallResponse("write_file", map[string]any{"path": "merge_mp4.py", "content": "print('hi')\n"}),
		{Message: Message{Role: "assistant", Text: testModificationFinalAnswer("merge_mp4.py", "verification was not run.", "no known remaining blocker.")}},
	}}
	agent := &Agent{
		Config: Config{
			AutoLocale:     boolPtr(false),
			PermissionMode: string(ModeBypass),
			AutoVerify:     boolPtr(false),
			Review:         ReviewHarnessConfig{AutoAfterChange: boolPtr(true)},
		},
		Client:    provider,
		Tools:     NewToolRegistry(NewWriteFileTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
		PromptConfirmAutoVerify: func(plan VerificationPlan) (bool, error) {
			_ = plan
			return false, nil
		},
	}

	reply, err := agent.Reply(context.Background(), "여러 mp4를 합치는 파이썬 프로그램을 작성해")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.Contains(reply, "analysis-only 모드라서") {
		t.Fatalf("false analysis-only claim must not be accepted as the final answer, got %q", reply)
	}
	data, readErr := os.ReadFile(filepath.Join(root, "merge_mp4.py"))
	if readErr != nil {
		t.Fatalf("expected write_file to create merge_mp4.py after the bounce: %v", readErr)
	}
	if !strings.Contains(string(data), "print('hi')") {
		t.Fatalf("unexpected file contents: %q", string(data))
	}
}

// modelKeyedProviderClient returns a scripted outcome per requested model, so a
// failover test can make the primary keep failing transiently and a fallback
// succeed.
type modelKeyedProviderClient struct {
	mu      sync.Mutex
	byModel map[string]func() (ChatResponse, error)
	seen    []string
}

func (c *modelKeyedProviderClient) Name() string { return "scripted" }

func (c *modelKeyedProviderClient) Complete(_ context.Context, req ChatRequest) (ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, req.Model)
	if fn, ok := c.byModel[req.Model]; ok {
		return fn()
	}
	return ChatResponse{}, fmt.Errorf("connection refused")
}

// TestModelFailoverOnRetryableExhaustedPrimary verifies that when the primary
// model keeps failing with a transient (retryable) transport error and a
// fallback model is configured, the turn fails over to the fallback and
// completes instead of hard-failing. This covers the gap the terminal-only inner
// fallback chain leaves open.
func TestModelFailoverOnRetryableExhaustedPrimary(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "primary-model", "", "full")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	ws := Workspace{BaseRoot: root, Root: root, Perms: NewPermissionManager(ModeBypass, nil)}

	provider := &modelKeyedProviderClient{byModel: map[string]func() (ChatResponse, error){
		"primary-model": func() (ChatResponse, error) {
			return ChatResponse{}, fmt.Errorf("read tcp 127.0.0.1:5000->127.0.0.1:1234: read: connection reset by peer")
		},
		"fallback-model": func() (ChatResponse, error) {
			return ChatResponse{Message: Message{Role: "assistant", Text: "Answered from the fallback model."}}, nil
		},
	}}

	agent := &Agent{
		// MaxRequestRetries -1 => a single attempt per completeModelTurn (fast test).
		Config:    Config{AutoLocale: boolPtr(false), PermissionMode: string(ModeBypass), MaxRequestRetries: -1, FallbackModels: []string{"fallback-model"}},
		Client:    provider,
		Tools:     NewToolRegistry(),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	reply, err := agent.Reply(context.Background(), "What is the answer?")
	if err != nil {
		t.Fatalf("Reply hard-failed instead of failing over to the fallback model: %v", err)
	}
	if !strings.Contains(reply, "fallback model") {
		t.Fatalf("expected the answer to come from the fallback model, got %q", reply)
	}
	provider.mu.Lock()
	seen := append([]string(nil), provider.seen...)
	provider.mu.Unlock()
	sawFallback := false
	for _, m := range seen {
		if m == "fallback-model" {
			sawFallback = true
		}
	}
	if !sawFallback {
		t.Fatalf("expected the fallback model to be tried, models seen: %v", seen)
	}
}
