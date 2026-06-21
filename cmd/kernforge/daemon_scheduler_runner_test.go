package main

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// newTestDaemonServerForScheduler builds a daemon server whose ensureServer can
// resolve a real temp-dir workspace runtime hermetically: no provider/model is
// configured (so no network), hooks are disabled, and the scheduler persists to a
// temp path. It returns the server and the workspace root the scheduler jobs
// should target.
func newTestDaemonServerForScheduler(t *testing.T) (*kernforgeDaemonServer, string) {
	t.Helper()
	// Redirect the user config base dir (where the durable schedule run-log lives)
	// to a temp dir so the test never writes to the real user home. os.UserHomeDir
	// reads USERPROFILE on Windows and HOME elsewhere; set both for portability.
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	root := t.TempDir()
	cfg := DefaultConfig(root)
	cfg.Provider = ""
	cfg.Model = ""
	cfg.BaseURL = ""
	cfg.PermissionMode = string(ModeBypass)
	cfg.SessionDir = filepath.Join(root, ".kernforge", "sessions")
	cfg.HooksEnabled = boolPtr(false)

	daemon := &kernforgeDaemonServer{
		fallbackCWD:    root,
		fallbackConfig: cfg,
		runtimes:       map[string]*kernforgeMCPServerRuntime{},
		stream:         newDaemonStreamHub(0),
		scheduledRuns:  newScheduledRunGuard(maxConcurrentScheduledRuns),
	}
	daemon.scheduler = daemon.buildScheduler(cfg)
	t.Cleanup(daemon.close)
	return daemon, root
}

// swapScheduledRunnerSeams overrides the goal and bundle execution seams for the
// duration of a test and restores them afterward, so the live provider/agent path
// is never exercised.
func swapScheduledRunnerSeams(t *testing.T, goalFn func(context.Context, *kernforgeMCPServer, JobDefinition) error, bundleFn func(*kernforgeMCPServer, JobDefinition) error) {
	t.Helper()
	prevGoal := scheduledGoalRunner
	prevBundle := scheduledBundleEnqueuer
	if goalFn != nil {
		scheduledGoalRunner = goalFn
	}
	if bundleFn != nil {
		scheduledBundleEnqueuer = bundleFn
	}
	t.Cleanup(func() {
		scheduledGoalRunner = prevGoal
		scheduledBundleEnqueuer = prevBundle
	})
}

func TestScheduledRunDispatchesGoalToGoalRunner(t *testing.T) {
	daemon, root := newTestDaemonServerForScheduler(t)

	type capture struct {
		job       JobDefinition
		workspace string
	}
	done := make(chan capture, 1)
	swapScheduledRunnerSeams(t,
		func(ctx context.Context, server *kernforgeMCPServer, job JobDefinition) error {
			ws := ""
			if server != nil && server.rt != nil {
				ws = server.rt.workspace.Root
			}
			done <- capture{job: job, workspace: ws}
			return nil
		},
		func(server *kernforgeMCPServer, job JobDefinition) error {
			t.Errorf("bundle enqueuer must not be called for a goal job")
			return nil
		},
	)

	runFn := daemon.buildScheduledRunFunc()
	job := JobDefinition{
		ID:        "sched-goal-1",
		Name:      "goal-job",
		Objective: "improve coverage",
		Type:      scheduleJobTypeGoal,
		Workspace: root,
		Budgets:   ScheduleBudgets{TokenBudget: 1234, TimeBudgetSeconds: 90, MaxIterations: 3},
	}
	if err := runFn(ScheduleRunRequest{Job: job, Triggered: time.Now()}); err != nil {
		t.Fatalf("runFn returned error: %v", err)
	}

	select {
	case got := <-done:
		if got.job.ID != job.ID || got.job.Type != scheduleJobTypeGoal {
			t.Fatalf("goal runner received wrong job: %+v", got.job)
		}
		if got.job.Budgets != job.Budgets {
			t.Fatalf("budgets not propagated to goal runner: got %+v want %+v", got.job.Budgets, job.Budgets)
		}
		if !samePath(got.workspace, root) {
			t.Fatalf("goal runner ran against wrong workspace: got %q want %q", got.workspace, root)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("goal runner was not dispatched")
	}
}

func TestScheduledRunDispatchesVerifyAndBatchToBundleEnqueuer(t *testing.T) {
	for _, jobType := range []string{scheduleJobTypeVerify, scheduleJobTypeBatch} {
		t.Run(jobType, func(t *testing.T) {
			daemon, root := newTestDaemonServerForScheduler(t)

			type capture struct {
				job       JobDefinition
				workspace string
			}
			done := make(chan capture, 1)
			swapScheduledRunnerSeams(t,
				func(ctx context.Context, server *kernforgeMCPServer, job JobDefinition) error {
					t.Errorf("goal runner must not be called for a %s job", jobType)
					return nil
				},
				func(server *kernforgeMCPServer, job JobDefinition) error {
					ws := ""
					if server != nil && server.rt != nil {
						ws = server.rt.workspace.Root
					}
					done <- capture{job: job, workspace: ws}
					return nil
				},
			)

			runFn := daemon.buildScheduledRunFunc()
			job := JobDefinition{
				ID:        "sched-" + jobType + "-1",
				Name:      jobType + "-job",
				Objective: "go test ./...",
				Type:      jobType,
				Workspace: root,
				Budgets:   ScheduleBudgets{TimeBudgetSeconds: 120},
			}
			if err := runFn(ScheduleRunRequest{Job: job, Triggered: time.Now()}); err != nil {
				t.Fatalf("runFn returned error: %v", err)
			}

			select {
			case got := <-done:
				if got.job.ID != job.ID || got.job.Type != jobType {
					t.Fatalf("bundle enqueuer received wrong job: %+v", got.job)
				}
				if !samePath(got.workspace, root) {
					t.Fatalf("bundle enqueuer ran against wrong workspace: got %q want %q", got.workspace, root)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("bundle enqueuer was not dispatched for %s", jobType)
			}
		})
	}
}

func TestScheduledRunErrorRecordedWithoutCrashingLoop(t *testing.T) {
	daemon, root := newTestDaemonServerForScheduler(t)

	added, err := daemon.scheduler.Add(JobDefinition{
		Name:      "failing-goal",
		Objective: "do the thing",
		Type:      scheduleJobTypeGoal,
		Workspace: root,
		Interval:  "1h",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	done := make(chan struct{})
	swapScheduledRunnerSeams(t,
		func(ctx context.Context, server *kernforgeMCPServer, job JobDefinition) error {
			defer close(done)
			return context.DeadlineExceeded
		},
		nil,
	)

	runFn := daemon.buildScheduledRunFunc()
	// The synchronous part of the runFn must not propagate the async run error: the
	// poll loop must keep running.
	if err := runFn(ScheduleRunRequest{Job: added, Triggered: time.Now()}); err != nil {
		t.Fatalf("runFn must not propagate the run error to the poll loop, got %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("goal runner was not dispatched")
	}

	// The failure must be recorded as the job's advisory LastError without disabling
	// the job. Poll for it because recordRunError runs on the dispatch goroutine.
	deadline := time.Now().Add(5 * time.Second)
	for {
		jobs := daemon.scheduler.Jobs()
		if len(jobs) != 1 {
			t.Fatalf("expected one job, got %d", len(jobs))
		}
		current := jobs[0]
		if current.LastError != "" {
			if !current.Enabled {
				t.Fatalf("a run failure must not disable the job: %+v", current)
			}
			if current.NextRun.IsZero() {
				t.Fatalf("a run failure must leave NextRun scheduled: %+v", current)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run failure was never recorded as LastError")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestScheduledRunSkipsJobAlreadyInFlight(t *testing.T) {
	daemon, root := newTestDaemonServerForScheduler(t)

	var calls int
	var mu sync.Mutex
	swapScheduledRunnerSeams(t,
		func(ctx context.Context, server *kernforgeMCPServer, job JobDefinition) error {
			mu.Lock()
			calls++
			mu.Unlock()
			return nil
		},
		nil,
	)

	job := JobDefinition{
		ID:        "sched-inflight",
		Name:      "inflight-job",
		Objective: "x",
		Type:      scheduleJobTypeGoal,
		Workspace: root,
	}
	// Pretend a previous run of this job is still active by holding its slot.
	if ok, _ := daemon.scheduledRuns.tryAcquire(job.ID); !ok {
		t.Fatalf("expected to acquire the slot for the simulated in-flight run")
	}

	runFn := daemon.buildScheduledRunFunc()
	if err := runFn(ScheduleRunRequest{Job: job, Triggered: time.Now()}); err != nil {
		t.Fatalf("runFn returned error on skip: %v", err)
	}

	// Give any (incorrectly) spawned goroutine a chance to run, then assert it never
	// dispatched while the prior run was in flight.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 0 {
		t.Fatalf("job already in flight must be skipped, but the runner was called %d time(s)", got)
	}

	// Release the simulated run; a subsequent fire should now dispatch.
	daemon.scheduledRuns.release(job.ID)
	done := make(chan struct{})
	swapScheduledRunnerSeams(t,
		func(ctx context.Context, server *kernforgeMCPServer, job JobDefinition) error {
			close(done)
			return nil
		},
		nil,
	)
	runFn = daemon.buildScheduledRunFunc()
	if err := runFn(ScheduleRunRequest{Job: job, Triggered: time.Now()}); err != nil {
		t.Fatalf("runFn returned error after release: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("expected dispatch after the in-flight run was released")
	}
}

func TestScheduledRunGuardCapsConcurrency(t *testing.T) {
	guard := newScheduledRunGuard(2)
	if ok, _ := guard.tryAcquire("a"); !ok {
		t.Fatalf("first acquire should succeed")
	}
	if ok, _ := guard.tryAcquire("b"); !ok {
		t.Fatalf("second acquire should succeed")
	}
	if ok, reason := guard.tryAcquire("c"); ok {
		t.Fatalf("third acquire should be capped")
	} else if reason == "" {
		t.Fatalf("capped acquire should explain why")
	}
	// Same job already in flight is rejected independently of the cap.
	guard.release("a")
	if ok, _ := guard.tryAcquire("b"); ok {
		t.Fatalf("acquiring an already in-flight job must be rejected")
	}
	// After release, a fresh slot is available again.
	if ok, _ := guard.tryAcquire("c"); !ok {
		t.Fatalf("a freed slot should be acquirable")
	}
}

func TestScheduledRunContextAppliesWallClockBound(t *testing.T) {
	// A job with a time budget bounds the context to that budget.
	budgeted := JobDefinition{Budgets: ScheduleBudgets{TimeBudgetSeconds: 45}}
	ctx, cancel := scheduledRunContext(budgeted)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("budgeted scheduled run context must have a deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 46*time.Second {
		t.Fatalf("budgeted deadline out of range: remaining=%v", remaining)
	}

	// A zero budget falls back to the default wall-clock timeout, never unbounded.
	unbudgeted := JobDefinition{}
	ctx2, cancel2 := scheduledRunContext(unbudgeted)
	defer cancel2()
	deadline2, ok2 := ctx2.Deadline()
	if !ok2 {
		t.Fatalf("a scheduled run with no time budget must still be wall-clock bounded")
	}
	remaining2 := time.Until(deadline2)
	if remaining2 <= 0 || remaining2 > defaultScheduledRunTimeout+time.Second {
		t.Fatalf("default deadline out of range: remaining=%v", remaining2)
	}
}

func TestScheduledBundleCommandAllowedFailsClosed(t *testing.T) {
	// Read-only and verification/build commands are allowed for an unattended bundle.
	allowed := []string{
		"go test ./...",
		"go build ./...",
		"ctest",
	}
	for _, command := range allowed {
		assessment := assessShellCommandMutation(command)
		if err := scheduledBundleCommandAllowed(assessment, command); err != nil {
			t.Errorf("expected command %q to be allowed, got %v (class=%s)", command, err, assessment.Class)
		}
	}

	// Workspace-write, git-mutation, and clearly destructive commands must be
	// rejected so a scheduled job cannot widen authority beyond verification/build.
	rejected := []string{
		"rm -rf build",
		"git commit -m wip",
		"set-content notes.txt hello",
	}
	for _, command := range rejected {
		assessment := assessShellCommandMutation(command)
		if err := scheduledBundleCommandAllowed(assessment, command); err == nil {
			t.Errorf("expected command %q to be rejected (class=%s)", command, assessment.Class)
		}
	}
}

func TestScheduledBundleCommandsSplitLines(t *testing.T) {
	got := scheduledBundleCommands("  go build ./...  \n\n go test ./pkg \n")
	want := []string{"go build ./...", "go test ./pkg"}
	if len(got) != len(want) {
		t.Fatalf("scheduledBundleCommands returned %d commands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("command %d = %q, want %q", i, got[i], want[i])
		}
	}
	if len(scheduledBundleCommands("   \n  \n")) != 0 {
		t.Fatalf("blank objective must yield no commands")
	}
}
