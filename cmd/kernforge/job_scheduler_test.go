package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

func TestParseScheduleInterval(t *testing.T) {
	cases := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"30m", 30 * time.Minute, false},
		{"6h", 6 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"45s", 45 * time.Second, false},
		{"5s", 0, true},   // below the minimum cadence floor
		{"0m", 0, true},   // non-positive
		{"-3m", 0, true},  // negative
		{"10", 0, true},   // missing unit
		{"m", 0, true},    // missing value
		{"10x", 0, true},  // unknown unit
		{"abch", 0, true}, // non-integer
		{"", 0, true},     // empty
	}
	for _, tc := range cases {
		got, err := parseScheduleInterval(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseScheduleInterval(%q) expected error, got %v", tc.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseScheduleInterval(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseScheduleInterval(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestNextScheduleRunInterval(t *testing.T) {
	now := mustTime(t, "2026-06-21T10:00:00Z")

	// First run with no LastRun anchors off now.
	job := JobDefinition{Interval: "30m"}
	next, err := nextScheduleRun(job, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := now.Add(30 * time.Minute); !next.Equal(want) {
		t.Fatalf("interval next run = %v, want %v", next, want)
	}

	// A recent LastRun advances one interval from LastRun.
	job.LastRun = now.Add(-10 * time.Minute)
	next, err = nextScheduleRun(job, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := job.LastRun.Add(30 * time.Minute); !next.Equal(want) {
		t.Fatalf("interval next run from recent last = %v, want %v", next, want)
	}

	// A far-past LastRun must not produce a catch-up burst; it advances from now.
	job.LastRun = now.Add(-100 * time.Hour)
	next, err = nextScheduleRun(job, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := now.Add(30 * time.Minute); !next.Equal(want) {
		t.Fatalf("interval next run from far-past last = %v, want %v", next, want)
	}
}

func TestNextCronRun(t *testing.T) {
	// 2026-06-21 is a Sunday.
	cases := []struct {
		name string
		expr string
		now  string
		want string
	}{
		{"every-minute", "* * * * *", "2026-06-21T10:00:30Z", "2026-06-21T10:01:00Z"},
		{"top-of-hour", "0 * * * *", "2026-06-21T10:15:00Z", "2026-06-21T11:00:00Z"},
		{"daily-at-0230", "30 2 * * *", "2026-06-21T10:00:00Z", "2026-06-22T02:30:00Z"},
		{"step-every-15", "*/15 * * * *", "2026-06-21T10:07:00Z", "2026-06-21T10:15:00Z"},
		{"list-minutes", "0,30 * * * *", "2026-06-21T10:10:00Z", "2026-06-21T10:30:00Z"},
		{"range-hours", "0 9-17 * * *", "2026-06-21T18:00:00Z", "2026-06-22T09:00:00Z"},
		{"dow-monday", "0 0 * * 1", "2026-06-21T10:00:00Z", "2026-06-22T00:00:00Z"},
		{"dow-sunday-7", "0 12 * * 7", "2026-06-21T13:00:00Z", "2026-06-28T12:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := mustTime(t, tc.now)
			got, err := nextCronRun(tc.expr, now)
			if err != nil {
				t.Fatalf("nextCronRun(%q) unexpected error: %v", tc.expr, err)
			}
			want := mustTime(t, tc.want)
			if !got.Equal(want) {
				t.Fatalf("nextCronRun(%q) = %v, want %v", tc.expr, got.UTC(), want)
			}
		})
	}
}

func TestNextCronRunDayOfMonthOrDayOfWeekOr(t *testing.T) {
	// When both DOM and DOW are restricted, a match on EITHER fires. DOM=15 OR
	// DOW=1 (Monday). From a Sunday the 21st, the next match is Monday the 22nd
	// (DOW), which is sooner than the 15th of next month.
	now := mustTime(t, "2026-06-21T10:00:00Z")
	got, err := nextCronRun("0 0 15 * 1", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := mustTime(t, "2026-06-22T00:00:00Z")
	if !got.Equal(want) {
		t.Fatalf("DOM-or-DOW next = %v, want %v", got.UTC(), want)
	}
}

func TestParseCronExpressionRejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		"* * * *",     // too few fields
		"* * * * * *", // too many fields
		"60 * * * *",  // minute out of range
		"* 24 * * *",  // hour out of range
		"* * 0 * *",   // day-of-month below min
		"* * * 13 *",  // month out of range
		"* * * * 8",   // day-of-week out of range
		"*/0 * * * *", // zero step
		"5-2 * * * *", // inverted range
		"a * * * *",   // non-numeric
		"0 0 30 2 *",  // impossible date (no Feb 30) within a year
	}
	for _, expr := range bad {
		if _, err := nextCronRun(expr, mustTime(t, "2026-06-21T10:00:00Z")); err == nil {
			t.Errorf("nextCronRun(%q) expected error, got nil", expr)
		}
	}
}

func newTestScheduler(t *testing.T, now *time.Time, runFn ScheduleRunFunc) *DaemonScheduler {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "schedule.json")
	return NewDaemonScheduler(statePath, time.Minute, func() time.Time { return *now }, runFn)
}

func TestSchedulerAddListRemove(t *testing.T) {
	now := mustTime(t, "2026-06-21T10:00:00Z")
	s := newTestScheduler(t, &now, nil)

	added, err := s.Add(JobDefinition{Name: "nightly", Objective: "run verify", Type: scheduleJobTypeVerify, Interval: "6h"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.ID == "" {
		t.Fatalf("Add should assign an id")
	}
	if !added.Enabled {
		t.Fatalf("added job should be enabled")
	}
	if want := now.Add(6 * time.Hour); !added.NextRun.Equal(want) {
		t.Fatalf("added NextRun = %v, want %v", added.NextRun, want)
	}

	// Duplicate name is rejected.
	if _, err := s.Add(JobDefinition{Name: "nightly", Objective: "x", Type: scheduleJobTypeGoal, Interval: "1h"}); err == nil {
		t.Fatalf("expected duplicate name to be rejected")
	}

	jobs := s.Jobs()
	if len(jobs) != 1 || jobs[0].Name != "nightly" {
		t.Fatalf("expected one job named nightly, got %+v", jobs)
	}

	removed, err := s.Remove("nightly")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !removed {
		t.Fatalf("expected removal to report true")
	}
	if got := s.Jobs(); len(got) != 0 {
		t.Fatalf("expected empty registry after remove, got %+v", got)
	}

	// Removing a missing job reports false without error.
	removed, err = s.Remove("nightly")
	if err != nil {
		t.Fatalf("Remove missing: %v", err)
	}
	if removed {
		t.Fatalf("expected removal of missing job to report false")
	}
}

func TestSchedulerPersistenceRoundTripAcrossRestart(t *testing.T) {
	now := mustTime(t, "2026-06-21T10:00:00Z")
	statePath := filepath.Join(t.TempDir(), "schedule.json")

	first := NewDaemonScheduler(statePath, time.Minute, func() time.Time { return now }, nil)
	if _, err := first.Add(JobDefinition{Name: "verify-loop", Objective: "verify build", Type: scheduleJobTypeVerify, Interval: "30m", Budgets: ScheduleBudgets{TokenBudget: 1000, MaxIterations: 3}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := first.Add(JobDefinition{Name: "cron-job", Objective: "nightly goal", Type: scheduleJobTypeGoal, Cron: "0 3 * * *"}); err != nil {
		t.Fatalf("Add cron: %v", err)
	}

	// Simulate a daemon restart: a fresh scheduler over the same state path.
	restarted := NewDaemonScheduler(statePath, time.Minute, func() time.Time { return now }, nil)
	if err := restarted.Load(); err != nil {
		t.Fatalf("Load after restart: %v", err)
	}
	jobs := restarted.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs after restart, got %d", len(jobs))
	}
	byName := map[string]JobDefinition{}
	for _, job := range jobs {
		byName[job.Name] = job
	}
	verify, ok := byName["verify-loop"]
	if !ok {
		t.Fatalf("verify-loop did not survive restart")
	}
	if verify.Budgets.TokenBudget != 1000 || verify.Budgets.MaxIterations != 3 {
		t.Fatalf("budgets not preserved across restart: %+v", verify.Budgets)
	}
	if verify.Type != scheduleJobTypeVerify {
		t.Fatalf("type not preserved: %q", verify.Type)
	}
	cron, ok := byName["cron-job"]
	if !ok {
		t.Fatalf("cron-job did not survive restart")
	}
	if cron.Cron != "0 3 * * *" {
		t.Fatalf("cron expression not preserved: %q", cron.Cron)
	}
	if cron.NextRun.IsZero() {
		t.Fatalf("cron NextRun should be recomputed on load")
	}
}

func TestSchedulerPollDueJobDetection(t *testing.T) {
	now := mustTime(t, "2026-06-21T10:00:00Z")
	var mu sync.Mutex
	var fired []string
	runFn := func(req ScheduleRunRequest) error {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, req.Job.Name)
		return nil
	}
	s := newTestScheduler(t, &now, runFn)

	if _, err := s.Add(JobDefinition{Name: "every-30m", Objective: "x", Type: scheduleJobTypeGoal, Interval: "30m"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Not yet due.
	if got := s.Poll(); len(got) != 0 {
		t.Fatalf("expected no due jobs immediately after add, got %d", len(got))
	}

	// Advance past the next-run boundary.
	now = now.Add(31 * time.Minute)
	due := s.Poll()
	if len(due) != 1 || due[0].Name != "every-30m" {
		t.Fatalf("expected every-30m to fire, got %+v", due)
	}
	mu.Lock()
	if len(fired) != 1 || fired[0] != "every-30m" {
		mu.Unlock()
		t.Fatalf("runFn should have fired once, got %v", fired)
	}
	mu.Unlock()

	// After firing, NextRun is rescheduled and it is not immediately due again.
	if got := s.Poll(); len(got) != 0 {
		t.Fatalf("expected job to be rescheduled, got %d due", len(got))
	}
}

func TestSchedulerMalformedScheduleDisablesWithoutPanic(t *testing.T) {
	now := mustTime(t, "2026-06-21T10:00:00Z")
	statePath := filepath.Join(t.TempDir(), "schedule.json")

	// Add at a time when the schedule is valid would be the normal path; instead
	// we simulate a previously-stored bad definition by writing state directly,
	// because Add rejects a malformed schedule outright.
	bad := kernforgeScheduleState{
		Version: kernforgeScheduleStateVersion,
		Jobs: []JobDefinition{
			{ID: "sched-bad", Name: "broken", Objective: "x", Type: scheduleJobTypeGoal, Cron: "not a cron", Enabled: true, CreatedAt: now},
			{ID: "sched-good", Name: "ok", Objective: "y", Type: scheduleJobTypeGoal, Interval: "1h", Enabled: true, CreatedAt: now},
		},
	}
	data, err := json.MarshalIndent(bad, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	ranCount := 0
	runFn := func(req ScheduleRunRequest) error {
		ranCount++
		return nil
	}
	s := NewDaemonScheduler(statePath, time.Minute, func() time.Time { return now }, runFn)
	if err := s.Load(); err != nil {
		t.Fatalf("Load with malformed job should not error: %v", err)
	}

	jobs := s.Jobs()
	byName := map[string]JobDefinition{}
	for _, job := range jobs {
		byName[job.Name] = job
	}
	broken, ok := byName["broken"]
	if !ok {
		t.Fatalf("malformed job should be kept (disabled), not dropped")
	}
	if broken.Enabled {
		t.Fatalf("malformed job should be disabled")
	}
	if !broken.NextRun.IsZero() {
		t.Fatalf("malformed job NextRun should be zeroed")
	}
	if broken.LastError == "" {
		t.Fatalf("malformed job should record a reason")
	}

	// Advance well past any boundary; Poll must not run the disabled job and must
	// not panic.
	now = now.Add(72 * time.Hour)
	due := s.Poll()
	for _, job := range due {
		if job.Name == "broken" {
			t.Fatalf("disabled malformed job must never fire")
		}
	}
	// RunNow on a disabled job is rejected (fail-closed).
	if _, err := s.RunNow("broken"); err == nil {
		t.Fatalf("RunNow on a disabled job should be rejected")
	}
}

func TestSchedulerRunNow(t *testing.T) {
	now := mustTime(t, "2026-06-21T10:00:00Z")
	var fired int
	runFn := func(req ScheduleRunRequest) error {
		fired++
		return nil
	}
	s := newTestScheduler(t, &now, runFn)
	if _, err := s.Add(JobDefinition{Name: "manual", Objective: "x", Type: scheduleJobTypeGoal, Interval: "6h"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	job, err := s.RunNow("manual")
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if fired != 1 {
		t.Fatalf("expected runFn to fire once, got %d", fired)
	}
	if !job.LastRun.Equal(now) {
		t.Fatalf("RunNow should set LastRun to now")
	}
	if want := now.Add(6 * time.Hour); !job.NextRun.Equal(want) {
		t.Fatalf("RunNow should recompute NextRun to %v, got %v", want, job.NextRun)
	}
}

func TestSchedulerAddRejectsBadInput(t *testing.T) {
	now := mustTime(t, "2026-06-21T10:00:00Z")
	s := newTestScheduler(t, &now, nil)
	cases := []JobDefinition{
		{Objective: "x", Type: scheduleJobTypeGoal, Interval: "1h"},                 // missing name
		{Name: "a", Type: scheduleJobTypeGoal, Interval: "1h"},                      // missing objective
		{Name: "b", Objective: "x", Type: "bogus", Interval: "1h"},                  // bad type
		{Name: "c", Objective: "x", Type: scheduleJobTypeGoal},                      // no schedule
		{Name: "d", Objective: "x", Type: scheduleJobTypeGoal, Interval: "2s"},      // below min
		{Name: "e", Objective: "x", Type: scheduleJobTypeGoal, Cron: "bad cron ex"}, // bad cron
	}
	for i, tc := range cases {
		if _, err := s.Add(tc); err == nil {
			t.Errorf("case %d: expected Add to reject %+v", i, tc)
		}
	}
	if got := s.Jobs(); len(got) != 0 {
		t.Fatalf("no job should have been added, got %+v", got)
	}
}
