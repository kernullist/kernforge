package main

import "testing"

// A declined prompt with a feedback hook captures the user's reason, which is
// then consumed exactly once.
func TestPermissionDeclineFeedbackCapturedOnce(t *testing.T) {
	perms := NewPermissionManager(ModeDefault, func(string) (bool, error) { return false, nil })
	perms.SetDeclineFeedbackHook(func(Action, string) string { return "use the existing helper instead" })

	allowed, err := perms.Allow(ActionWrite, "write main.go")
	if err != nil || allowed {
		t.Fatalf("expected decline, allowed=%v err=%v", allowed, err)
	}
	if fb := perms.ConsumeDeclineFeedback(); fb != "use the existing helper instead" {
		t.Fatalf("expected captured feedback, got %q", fb)
	}
	if fb := perms.ConsumeDeclineFeedback(); fb != "" {
		t.Fatalf("feedback must clear after the first consume, got %q", fb)
	}
}

// An approval clears any stale feedback left by an earlier decline, so it never
// attaches to an unrelated, later-approved action.
func TestPermissionApprovalClearsStaleFeedback(t *testing.T) {
	allow := false
	perms := NewPermissionManager(ModeDefault, func(string) (bool, error) { return allow, nil })
	perms.SetDeclineFeedbackHook(func(Action, string) string { return "reason" })

	if a, _ := perms.Allow(ActionWrite, "x"); a {
		t.Fatalf("first call should decline")
	}
	allow = true
	if a, _ := perms.Allow(ActionWrite, "y"); !a {
		t.Fatalf("second call should approve")
	}
	if fb := perms.ConsumeDeclineFeedback(); fb != "" {
		t.Fatalf("approval must clear stale feedback, got %q", fb)
	}
}

// With no hook installed, behavior is unchanged: a decline captures nothing.
func TestPermissionNoHookKeepsOriginalBehavior(t *testing.T) {
	perms := NewPermissionManager(ModeDefault, func(string) (bool, error) { return false, nil })
	if a, _ := perms.Allow(ActionWrite, "x"); a {
		t.Fatalf("should decline")
	}
	if fb := perms.ConsumeDeclineFeedback(); fb != "" {
		t.Fatalf("no hook must not capture feedback, got %q", fb)
	}
}

// A hook that returns empty text (the user pressed Enter) records nothing.
func TestPermissionEmptyFeedbackRecordsNothing(t *testing.T) {
	perms := NewPermissionManager(ModeDefault, func(string) (bool, error) { return false, nil })
	perms.SetDeclineFeedbackHook(func(Action, string) string { return "   " })
	if a, _ := perms.Allow(ActionWrite, "x"); a {
		t.Fatalf("should decline")
	}
	if fb := perms.ConsumeDeclineFeedback(); fb != "" {
		t.Fatalf("blank feedback must be trimmed to empty, got %q", fb)
	}
}
