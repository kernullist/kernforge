package main

import "testing"

// Editable workers concurrently write the shared tree. The safe default is to
// refuse them unless worktree isolation is on or the user explicitly opts in.
func TestParallelEditableWorkersGateRequiresIsolationOrOptIn(t *testing.T) {
	session := &Session{ActiveFeatureID: "feature-1"}

	// Default config: isolation off, no opt-in -> refuse even with an active feature.
	cfg := Config{}
	if parallelEditableWorkersEnabledForSession(cfg, session) {
		t.Fatalf("editable workers must be refused by default in the shared tree")
	}

	// Worktree isolation enabled -> safe path, allowed.
	isolated := Config{WorktreeIsolation: WorktreeIsolationConfig{Enabled: boolPtr(true)}}
	if !parallelEditableWorkersEnabledForSession(isolated, session) {
		t.Fatalf("editable workers must run when worktree isolation is enabled")
	}

	// Explicit opt-in to shared-tree editing with an active feature -> allowed.
	optIn := Config{Parallelism: ParallelismConfig{AllowEditableWorkersInSharedTree: boolPtr(true)}}
	if !parallelEditableWorkersEnabledForSession(optIn, session) {
		t.Fatalf("editable workers must run when the user explicitly opts in")
	}

	// Opt-in but no active feature -> still refuse (nothing to scope to).
	if parallelEditableWorkersEnabledForSession(optIn, &Session{}) {
		t.Fatalf("opt-in without an active feature must not enable editable workers")
	}
}

func TestParallelismWorkerCapsDefaultsAndClamp(t *testing.T) {
	cfg := Config{}
	if got := configParallelismMicroWorkerCap(cfg); got != defaultMicroWorkerCap {
		t.Fatalf("micro cap default = %d, want %d", got, defaultMicroWorkerCap)
	}
	if got := configParallelismReadWorkerCap(cfg); got != defaultReadWorkerCap {
		t.Fatalf("read cap default = %d, want %d", got, defaultReadWorkerCap)
	}
	if got := configParallelismEditWorkerCap(cfg); got != defaultEditWorkerCap {
		t.Fatalf("edit cap default = %d, want %d", got, defaultEditWorkerCap)
	}

	// Configured values are honored.
	cfg.Parallelism = ParallelismConfig{MicroWorkerCap: 5, ReadWorkerCap: 4, EditWorkerCap: 3}
	if got := configParallelismMicroWorkerCap(cfg); got != 5 {
		t.Fatalf("micro cap = %d, want 5", got)
	}
	if got := configParallelismReadWorkerCap(cfg); got != 4 {
		t.Fatalf("read cap = %d, want 4", got)
	}
	if got := configParallelismEditWorkerCap(cfg); got != 3 {
		t.Fatalf("edit cap = %d, want 3", got)
	}

	// Negative falls back to default; oversized is clamped.
	cfg.Parallelism = ParallelismConfig{MicroWorkerCap: -1, EditWorkerCap: 9999}
	if got := configParallelismMicroWorkerCap(cfg); got != defaultMicroWorkerCap {
		t.Fatalf("negative micro cap = %d, want default %d", got, defaultMicroWorkerCap)
	}
	if got := configParallelismEditWorkerCap(cfg); got != maxConfiguredWorkerCap {
		t.Fatalf("oversized edit cap = %d, want clamp %d", got, maxConfiguredWorkerCap)
	}
}

func TestParallelismEditWorkerBudgetDefaultsAndOverride(t *testing.T) {
	// MaxTokens large enough that the /3 and /6 guards do not bind.
	cfg := Config{MaxTokens: 8192}
	if got := configParallelismEditWorkerMaxTurns(cfg); got != defaultEditWorkerMaxTurns {
		t.Fatalf("edit turns default = %d, want %d", got, defaultEditWorkerMaxTurns)
	}
	if got := configParallelismEditWorkerMaxTokens(cfg); got != defaultEditWorkerMaxTokens {
		t.Fatalf("edit tokens default = %d, want %d", got, defaultEditWorkerMaxTokens)
	}
	if got := configParallelismMicroWorkerMaxTokens(cfg); got != defaultMicroWorkerMaxTokens {
		t.Fatalf("micro tokens default = %d, want %d", got, defaultMicroWorkerMaxTokens)
	}

	// Explicit positive values raise the budgets (still bounded by MaxTokens guard).
	cfg.Parallelism = ParallelismConfig{EditWorkerMaxTurns: 8, EditWorkerMaxTokens: 1500, MicroWorkerMaxTokens: 400}
	if got := configParallelismEditWorkerMaxTurns(cfg); got != 8 {
		t.Fatalf("edit turns = %d, want 8", got)
	}
	if got := configParallelismEditWorkerMaxTokens(cfg); got != 1500 {
		t.Fatalf("edit tokens = %d, want 1500", got)
	}
	if got := configParallelismMicroWorkerMaxTokens(cfg); got != 400 {
		t.Fatalf("micro tokens = %d, want 400", got)
	}

	// A tiny MaxTokens keeps the per-turn ceiling from blowing up cost: the
	// guard max(256, MaxTokens/3) still applies as the upper bound.
	small := Config{MaxTokens: 300, Parallelism: ParallelismConfig{EditWorkerMaxTokens: 5000}}
	if got := configParallelismEditWorkerMaxTokens(small); got != 256 {
		t.Fatalf("edit tokens with small MaxTokens = %d, want guard 256", got)
	}
}
