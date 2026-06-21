package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Real execution transport for fired scheduled jobs.
//
// The scheduler core (job_scheduler.go) never runs code itself: it only decides
// which jobs are due and hands each one to an injected ScheduleRunFunc. This file
// holds the daemon's REAL runFn, kept here (not in the scheduler core) so the core
// stays seam-injectable and clock-injectable for hermetic tests.
//
// Safety model for UNATTENDED scheduled runs:
//  1. Permission/verification gates are NOT widened. A type=goal run goes through
//     the same in-process goal runner an interactive run uses; the only autonomy it
//     gets is the existing withAutonomousGoalPermissions envelope that an
//     interactive /goal run also applies. A type=verify|batch run is enqueued as a
//     BackgroundShellBundle and is fail-closed: any command that is not read-only,
//     cache-only, external-install, or verification/build is rejected before it can
//     start, so a scheduled job can never silently auto-approve a dangerous shell,
//     git, or workspace-write command.
//  2. Per-workspace isolation: each job resolves its OWN workspace runtime through
//     the daemon's ensureServer (the same per-workspace runtime map used by RPC), so
//     one job's run cannot touch another workspace's session, store, or background
//     jobs.
//  3. Budgets and a wall-clock timeout: the job's token/time/iteration budgets are
//     copied onto the GoalState (goal runner enforces them), and the goroutine runs
//     under a context bounded by the job's time budget so a runaway run cannot pin a
//     worker forever.
//  4. No foreground interference: the runner only ever executes inside the daemon
//     process on its own goroutines against daemon-resolved runtimes; it never shares
//     state with an interactive session and never pre-empts one. In-flight tracking
//     skips a job whose previous run is still active, and a global cap bounds total
//     concurrent scheduled runs.

const (
	// maxConcurrentScheduledRuns caps how many scheduled jobs may execute at once
	// across the whole daemon. A fired job beyond the cap is skipped-and-logged for
	// this tick (it will be reconsidered on the next poll) so a burst of due jobs
	// cannot saturate the host.
	maxConcurrentScheduledRuns = 4

	// defaultScheduledRunTimeout bounds a scheduled run that did not set a time
	// budget, so an unbounded goal loop cannot pin a worker forever.
	defaultScheduledRunTimeout = 30 * time.Minute
)

// scheduledRunGuard tracks in-flight scheduled runs. It enforces both the
// per-job "do not run again while still running" rule and the global concurrency
// cap. It is safe for concurrent use.
type scheduledRunGuard struct {
	mu       sync.Mutex
	inFlight map[string]bool
	active   int
	cap      int
}

func newScheduledRunGuard(concurrencyCap int) *scheduledRunGuard {
	if concurrencyCap <= 0 {
		concurrencyCap = maxConcurrentScheduledRuns
	}
	return &scheduledRunGuard{
		inFlight: map[string]bool{},
		cap:      concurrencyCap,
	}
}

// tryAcquire reserves a slot for jobID. It returns (acquired, reason). When
// acquired is false, reason explains why (already in flight, or at capacity) so
// the caller can skip-and-log without crashing the loop.
func (g *scheduledRunGuard) tryAcquire(jobID string) (bool, string) {
	if g == nil {
		return true, ""
	}
	jobID = strings.TrimSpace(jobID)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inFlight == nil {
		g.inFlight = map[string]bool{}
	}
	if jobID != "" && g.inFlight[jobID] {
		return false, "a previous run of this job is still in flight"
	}
	if g.active >= g.cap {
		return false, fmt.Sprintf("the maximum of %d concurrent scheduled runs is already active", g.cap)
	}
	if jobID != "" {
		g.inFlight[jobID] = true
	}
	g.active++
	return true, ""
}

// release frees the slot reserved by a prior tryAcquire.
func (g *scheduledRunGuard) release(jobID string) {
	if g == nil {
		return
	}
	jobID = strings.TrimSpace(jobID)
	g.mu.Lock()
	defer g.mu.Unlock()
	if jobID != "" {
		delete(g.inFlight, jobID)
	}
	if g.active > 0 {
		g.active--
	}
}

// scheduledGoalRunner runs a fired type=goal job in-process. It is a function
// variable so tests can override it with a stub instead of constructing a live
// agent/provider; the daemon defaults it to runScheduledGoalForJob, which drives
// the genuine in-process goal runner (covered-by-construction end to end).
var scheduledGoalRunner = runScheduledGoalForJob

// scheduledBundleEnqueuer enqueues a fired type=verify|batch job as a background
// shell bundle. It is a function variable for the same testing reason; the daemon
// defaults it to enqueueScheduledBundleForJob.
var scheduledBundleEnqueuer = enqueueScheduledBundleForJob

// buildScheduledRunFunc returns the daemon's real ScheduleRunFunc. It dispatches a
// fired job to the goal runner (type=goal) or the bundle enqueuer
// (type=verify|batch), enforces the in-flight/concurrency guard, runs the work in
// a bounded goroutine so the poll loop is never blocked, and records LastError on
// failure. It also keeps the durable run-log so an unattended run stays auditable.
//
// It NEVER lets a failure crash the daemon or the poll loop: every dispatched run
// recovers from a panic and records the failure as a run-log entry. The scheduler
// core already contains a per-job panic guard around the runFn call; this is the
// inner, finer-grained guard so a panic inside a goroutine cannot take the process
// down.
func (d *kernforgeDaemonServer) buildScheduledRunFunc() ScheduleRunFunc {
	return func(req ScheduleRunRequest) error {
		// Durable audit trail first: record that the job fired regardless of how the
		// execution transport behaves. A run-log write failure must not block
		// execution, so its error is folded into the returned error only when the
		// dispatch itself otherwise succeeds.
		logErr := recordScheduledJobRun(req)

		acquired, skipReason := d.scheduledRuns.tryAcquire(req.Job.ID)
		if !acquired {
			d.logScheduledRunEvent(req.Job, "skipped", skipReason)
			// A skip is not a job failure: returning nil keeps the job enabled and on
			// its normal cadence so the next poll can retry once a slot frees up.
			return logErr
		}

		go d.runScheduledJob(req)
		return logErr
	}
}

// runScheduledJob executes one fired job on a daemon goroutine. It owns releasing
// the concurrency slot and contains any panic so the daemon process survives a
// faulty run.
func (d *kernforgeDaemonServer) runScheduledJob(req ScheduleRunRequest) {
	defer d.scheduledRuns.release(req.Job.ID)
	defer func() {
		if r := recover(); r != nil {
			d.logScheduledRunEvent(req.Job, "panic", fmt.Sprintf("scheduled run panicked: %v", r))
			d.recordScheduledRunFailure(req.Job, fmt.Errorf("scheduled run panicked: %v", r))
		}
	}()

	ctx, cancel := scheduledRunContext(req.Job)
	defer cancel()

	if err := d.dispatchScheduledRun(ctx, req); err != nil {
		d.logScheduledRunEvent(req.Job, "error", err.Error())
		d.recordScheduledRunFailure(req.Job, err)
		return
	}
	d.logScheduledRunEvent(req.Job, "completed", "")
}

// dispatchScheduledRun resolves the job's workspace runtime and routes to the
// correct execution transport based on the job type.
func (d *kernforgeDaemonServer) dispatchScheduledRun(ctx context.Context, req ScheduleRunRequest) error {
	server, err := d.ensureServer(req.Job.Workspace, "scheduler")
	if err != nil {
		return fmt.Errorf("resolve workspace runtime: %w", err)
	}
	if server == nil {
		return fmt.Errorf("workspace runtime is unavailable")
	}
	switch strings.TrimSpace(strings.ToLower(req.Job.Type)) {
	case scheduleJobTypeGoal:
		return scheduledGoalRunner(ctx, server, req.Job)
	case scheduleJobTypeVerify, scheduleJobTypeBatch:
		return scheduledBundleEnqueuer(server, req.Job)
	default:
		return fmt.Errorf("unsupported scheduled job type %q", req.Job.Type)
	}
}

// scheduledRunContext derives a wall-clock-bounded context from the job's time
// budget. A zero budget falls back to defaultScheduledRunTimeout so an unbounded
// goal loop can never pin a worker forever.
func scheduledRunContext(job JobDefinition) (context.Context, context.CancelFunc) {
	timeout := defaultScheduledRunTimeout
	if job.Budgets.TimeBudgetSeconds > 0 {
		timeout = time.Duration(job.Budgets.TimeBudgetSeconds) * time.Second
	}
	return context.WithTimeout(context.Background(), timeout)
}

// recordScheduledRunFailure stores the failure reason on the job's LastError via
// the scheduler so an operator listing jobs can see why the last unattended run
// failed. It never disables the job (a transient run failure should not stop a
// healthy schedule) and never propagates an error.
func (d *kernforgeDaemonServer) recordScheduledRunFailure(job JobDefinition, runErr error) {
	if d == nil || d.scheduler == nil || runErr == nil {
		return
	}
	d.scheduler.recordRunError(job.ID, runErr.Error())
}

// logScheduledRunEvent appends a single ASCII status line about a scheduled run to
// the daemon log. It is best-effort and never fails the run.
func (d *kernforgeDaemonServer) logScheduledRunEvent(job JobDefinition, outcome string, detail string) {
	parts := []string{
		fmt.Sprintf("time=%s", time.Now().Format(time.RFC3339)),
		fmt.Sprintf("event=scheduled_run outcome=%s", strings.TrimSpace(outcome)),
		fmt.Sprintf("job=%s", strings.TrimSpace(job.ID)),
		fmt.Sprintf("type=%s", strings.TrimSpace(job.Type)),
	}
	if name := strings.TrimSpace(job.Name); name != "" {
		parts = append(parts, fmt.Sprintf("name=%s", name))
	}
	if detail = strings.TrimSpace(firstNonEmptyLine(detail)); detail != "" {
		parts = append(parts, fmt.Sprintf("detail=%s", truncateStatusSnippet(detail, 200)))
	}
	fmt.Fprintln(os.Stderr, strings.Join(parts, " "))
}

// runScheduledGoalForJob is the daemon default for a fired type=goal job. It runs
// the objective through the genuine in-process goal runner on the job's own
// workspace runtime, bounded by the job budgets and the supplied context. This is
// the live end-to-end path; it is covered-by-construction (a hermetic test cannot
// stand up a real provider/agent, so the dispatch wiring is tested via the
// injectable seam instead).
func runScheduledGoalForJob(ctx context.Context, server *kernforgeMCPServer, job JobDefinition) error {
	if server == nil || server.rt == nil {
		return fmt.Errorf("workspace runtime is unavailable")
	}
	return server.rt.runScheduledGoal(ctx, job)
}

// runScheduledGoal records the objective as a GoalState on this runtime's session
// (carrying the job budgets) and runs the autonomous goal loop under the SAME
// autonomous-permission envelope an interactive /goal run uses. It accepts an
// external, already-bounded context so the daemon's wall-clock timeout applies; it
// deliberately does not start the TTY escape watcher (there is no interactive
// terminal in the daemon) and never touches a foreground session.
func (rt *runtimeState) runScheduledGoal(ctx context.Context, job JobDefinition) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session for scheduled goal")
	}
	if rt.store == nil {
		return fmt.Errorf("scheduled goal requires a persisted session")
	}
	objective := strings.TrimSpace(job.Objective)
	if objective == "" {
		return fmt.Errorf("scheduled goal has no objective")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	goal := GoalState{
		ID:                fmt.Sprintf("sched-goal-%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000),
		Objective:         objective,
		Status:            goalStatusRunning,
		MaxIterations:     job.Budgets.MaxIterations,
		TimeBudgetSeconds: job.Budgets.TimeBudgetSeconds,
		TokenBudget:       job.Budgets.TokenBudget,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	goal.Normalize()
	rt.primeGoalRuntimeState(&goal, "scheduled")
	rt.session.UpsertGoal(goal)
	if err := rt.store.Save(rt.session); err != nil {
		return err
	}
	if err := rt.writeGoalArtifacts(goal); err != nil {
		return err
	}
	// Fail closed when there is no usable model: a scheduled goal must not silently
	// no-op. Mirror runGoalBySelector by recording the blocker on the goal.
	if rt.clientErr != nil && rt.goalReply == nil {
		goal.Status = goalStatusBlocked
		if goalErrorLooksUsageLimited(rt.clientErr) {
			goal.Status = goalStatusUsageLimited
		}
		goal.LastError = rt.clientErr.Error()
		goal.Touch()
		rt.session.UpsertGoal(goal)
		_ = rt.writeGoalArtifacts(goal)
		_ = rt.store.Save(rt.session)
		return rt.clientErr
	}
	return rt.withAutonomousGoalPermissions(func() error {
		return rt.runGoalLoop(ctx, goal.ID)
	})
}

// enqueueScheduledBundleForJob is the daemon default for a fired
// type=verify|batch job. It enqueues the job's command(s) as a background shell
// bundle on the job's own workspace runtime via the existing BackgroundJobManager,
// rather than running a goal loop. Each command is fail-closed: anything that is
// not read-only, cache-only, external-install, or verification/build is rejected
// before it can start, so a scheduled job cannot widen shell authority.
func enqueueScheduledBundleForJob(server *kernforgeMCPServer, job JobDefinition) error {
	if server == nil || server.rt == nil {
		return fmt.Errorf("workspace runtime is unavailable")
	}
	rt := server.rt
	manager := rt.backgroundJobs
	if manager == nil {
		return fmt.Errorf("background jobs are not configured for this workspace")
	}
	commands := scheduledBundleCommands(job.Objective)
	if len(commands) == 0 {
		return fmt.Errorf("scheduled %s job has no command to run", job.Type)
	}
	_, workDir, err := rt.workspace.ResolveShellWorkDir("", "")
	if err != nil {
		return err
	}
	jobs := make([]BackgroundShellJob, 0, len(commands))
	for _, command := range commands {
		assessment := assessShellCommandMutation(command)
		if err := scheduledBundleCommandAllowed(assessment, command); err != nil {
			return err
		}
		started, startErr := manager.StartShellJob(rt.workspace.Shell, workDir, command, assessment, "")
		if startErr != nil {
			return startErr
		}
		jobs = append(jobs, started)
	}
	if _, err := manager.RecordShellBundle(jobs, "", BackgroundShellBundleOptions{VerificationLike: true}); err != nil {
		return err
	}
	return nil
}

// scheduledBundleCommandAllowed rejects any command class that a scheduled,
// unattended bundle must never auto-run. It mirrors the read-only/verification
// fail-closed posture of the run_shell_background tools so a scheduled job cannot
// silently auto-approve a dangerous shell, git, or workspace-write command.
func scheduledBundleCommandAllowed(assessment shellCommandAssessment, command string) error {
	switch assessment.Class {
	case shellMutationReadOnly,
		shellMutationCacheOnly,
		shellMutationExternalInstall,
		shellMutationVerificationArtifacts:
		return nil
	case shellMutationUnsafe:
		return fmt.Errorf("scheduled command rejected (unsafe): %s", truncateStatusSnippet(firstNonEmptyLine(command), 160))
	case shellMutationUnsupported:
		return fmt.Errorf("scheduled command rejected (unsupported syntax): %s", truncateStatusSnippet(firstNonEmptyLine(command), 160))
	case shellMutationGitMutation:
		return fmt.Errorf("scheduled command rejected (mutates git state): %s", truncateStatusSnippet(firstNonEmptyLine(command), 160))
	case shellMutationWorkspaceWrite:
		return fmt.Errorf("scheduled command rejected (workspace write): %s", truncateStatusSnippet(firstNonEmptyLine(command), 160))
	default:
		return fmt.Errorf("scheduled command rejected (unrecognized class %q): %s", string(assessment.Class), truncateStatusSnippet(firstNonEmptyLine(command), 160))
	}
}

// scheduledBundleCommands splits a verify/batch objective into individual shell
// commands. Each non-blank line is one command, so a multi-command batch can be
// expressed across lines without a shell parser.
func scheduledBundleCommands(objective string) []string {
	commands := make([]string, 0, 4)
	for _, line := range strings.Split(objective, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		commands = append(commands, trimmed)
	}
	return commands
}
