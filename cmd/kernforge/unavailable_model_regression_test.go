package main

import (
	"errors"
	"testing"
)

// TestUnavailableModelClassification locks in that a configured model that is
// gone (deprecated/retired/not found) is treated as a permanent configuration
// error -- non-retryable and detectable -- so the agent surfaces a precise fix
// instead of looping on a route that can never succeed. Transient outages stay
// retryable. This is the root cause of the 2h27m cross-reviewer deadlock: the
// configured cross-review model had been decommissioned.
func TestUnavailableModelClassification(t *testing.T) {
	unavailable := []string{
		"model_not_found: the model 'claude-x' does not exist",
		"This model has been deprecated",
		"model is no longer available",
		"unknown model claude-old",
		"no such model: sonnet-legacy",
	}
	for _, m := range unavailable {
		if !providerErrorIndicatesUnavailableModel(errors.New(m)) {
			t.Errorf("must detect unavailable model: %q", m)
		}
		if providerErrorLooksRetryable(0, "", m, "", "") {
			t.Errorf("unavailable model must be non-retryable: %q", m)
		}
	}
	// Transient outages must stay retryable and must NOT be flagged unavailable,
	// so a blip does not get misreported as a permanent model removal.
	transient := []string{
		"service temporarily unavailable",
		"server is overloaded",
		"gateway timeout",
	}
	for _, m := range transient {
		if providerErrorIndicatesUnavailableModel(errors.New(m)) {
			t.Errorf("transient outage must not be flagged as unavailable model: %q", m)
		}
		if !providerErrorLooksRetryable(0, "", m, "", "") {
			t.Errorf("transient outage must stay retryable: %q", m)
		}
	}
}
