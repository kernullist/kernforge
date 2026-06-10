package main

import (
	"testing"
	"time"
)

func TestRecoveryPolicyEmptyStopDecision(t *testing.T) {
	retry := DecideRecovery(RecoveryPolicyInput{
		Kind:        RecoveryKindEmptyStop,
		StopReason:  "stop",
		Attempt:     1,
		MaxAttempts: 2,
	})
	if retry.Kind != RecoveryKindEmptyStop || retry.Action != RecoveryActionRetry || !retry.Retryable {
		t.Fatalf("expected retryable empty-stop decision, got %#v", retry)
	}

	blocked := DecideRecovery(RecoveryPolicyInput{
		Kind:        RecoveryKindEmptyStop,
		StopReason:  "stop",
		Attempt:     2,
		MaxAttempts: 2,
	})
	if blocked.Kind != RecoveryKindEmptyStop || blocked.Action != RecoveryActionBlock || blocked.Retryable {
		t.Fatalf("expected blocked empty-stop decision at cap, got %#v", blocked)
	}
	if blocked.Guidance == "" {
		t.Fatalf("expected actionable blocked guidance")
	}
}

func TestRecoveryPolicyLengthStopDecisionRequestsContinuationOrCompaction(t *testing.T) {
	continuation := DecideRecovery(RecoveryPolicyInput{
		Kind:       RecoveryKindLengthStop,
		StopReason: "length",
	})
	if continuation.Action != RecoveryActionContinue || !continuation.Retryable {
		t.Fatalf("expected length stop to request continuation, got %#v", continuation)
	}

	compaction := DecideRecovery(RecoveryPolicyInput{
		Kind:       RecoveryKindLengthStop,
		StopReason: "max_tokens",
		CanCompact: true,
	})
	if compaction.Action != RecoveryActionCompact || !compaction.Retryable {
		t.Fatalf("expected length stop with compaction room to compact, got %#v", compaction)
	}
}

func TestRecoveryPolicyRetryableProviderErrorBackoff(t *testing.T) {
	decision := DecideRecovery(RecoveryPolicyInput{
		ProviderError: &ProviderAPIError{
			Provider:   "openai",
			StatusCode: 503,
			Message:    "service unavailable",
		},
		BaseDelay: 10 * time.Millisecond,
		MaxDelay:  30 * time.Second,
	})
	if decision.Kind != RecoveryKindRetryableProviderError || decision.Action != RecoveryActionBackoff || !decision.Retryable {
		t.Fatalf("expected retryable provider backoff, got %#v", decision)
	}
	if decision.Backoff <= 0 {
		t.Fatalf("expected positive backoff, got %#v", decision)
	}

	blocked := DecideRecovery(RecoveryPolicyInput{
		ProviderError: &ProviderAPIError{
			Provider:   "openai",
			StatusCode: 429,
			Message:    "rate limit",
		},
		RetryAfter: 60 * time.Second,
		MaxDelay:   30 * time.Second,
	})
	if blocked.Kind != RecoveryKindBlocked || blocked.Action != RecoveryActionBlock {
		t.Fatalf("expected max-delay fail-fast block, got %#v", blocked)
	}
}

func TestRecoveryPolicyContextOverflowClassifiesProviderError(t *testing.T) {
	decision := DecideRecovery(RecoveryPolicyInput{
		ProviderError: &ProviderAPIError{
			Provider: "openai",
			Code:     "context_length_exceeded",
			Message:  "context window exceeded",
		},
		CanCompact: true,
	})
	if decision.Kind != RecoveryKindContextOverflow || decision.Action != RecoveryActionCompact || !decision.Retryable {
		t.Fatalf("expected context overflow compaction decision, got %#v", decision)
	}
}

func TestRecoveryPolicyUsageLimitChoosesModelFallbackWhenAvailable(t *testing.T) {
	decision := DecideRecovery(RecoveryPolicyInput{
		ProviderError: &ProviderAPIError{
			Provider: "openai",
			Code:     "usage_not_included",
			Message:  "usage not included",
		},
		CanPromoteModel: true,
	})
	if decision.Kind != RecoveryKindBlocked || decision.Action != RecoveryActionPromoteModel || !decision.Retryable {
		t.Fatalf("expected usage-limit model fallback, got %#v", decision)
	}

	blocked := DecideRecovery(RecoveryPolicyInput{
		ProviderError: &ProviderAPIError{
			Provider: "openai",
			Code:     "insufficient_quota",
			Message:  "quota exceeded",
		},
	})
	if blocked.Action != RecoveryActionBlock || blocked.Guidance == "" {
		t.Fatalf("expected actionable usage-limit block without fallback, got %#v", blocked)
	}
}

func TestContextMaintenanceInputOverflowChoosesCompactPromoteOrBlock(t *testing.T) {
	compact := DecideContextMaintenance(ContextMaintenanceInput{
		Trigger:     RecoveryKindContextOverflow,
		ApproxChars: 120000,
		Threshold:   45000,
		CanCompact:  true,
	})
	if compact.Action != ContextMaintenanceCompact {
		t.Fatalf("expected compact decision, got %#v", compact)
	}

	promote := DecideContextMaintenance(ContextMaintenanceInput{
		Trigger:         RecoveryKindContextOverflow,
		ApproxChars:     120000,
		Threshold:       45000,
		CanPromoteModel: true,
	})
	if promote.Action != ContextMaintenancePromoteModel {
		t.Fatalf("expected promote-model decision, got %#v", promote)
	}

	block := DecideContextMaintenance(ContextMaintenanceInput{
		Trigger:     RecoveryKindContextOverflow,
		ApproxChars: 120000,
		Threshold:   45000,
	})
	if block.Action != ContextMaintenanceBlock {
		t.Fatalf("expected block decision, got %#v", block)
	}
}

func TestContextMaintenanceCompactionThresholdDecision(t *testing.T) {
	decision := DecideContextMaintenance(ContextMaintenanceInput{
		Trigger:     RecoveryKindCompaction,
		ApproxChars: 46000,
		Threshold:   45000,
		CanCompact:  true,
	})
	if decision.Action != ContextMaintenanceCompact {
		t.Fatalf("expected proactive compaction decision, got %#v", decision)
	}

	none := DecideContextMaintenance(ContextMaintenanceInput{
		Trigger:     RecoveryKindCompaction,
		ApproxChars: 44000,
		Threshold:   45000,
		CanCompact:  true,
	})
	if none.Action != ContextMaintenanceNone {
		t.Fatalf("expected no maintenance below threshold, got %#v", none)
	}
}
