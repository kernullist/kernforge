package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Local daemon scheduler for unattended goal/verification runs.
//
// The scheduler is deliberately SELF-CONTAINED and FAIL-CLOSED: the daemon only
// calls Start/Stop/Poll and the RPC handler. All schedule math is driven by an
// injectable clock (nowFn) so the core never calls time.Now directly and is
// fully testable without starting a real daemon. A malformed schedule disables
// the job (logged, NextRun zeroed) rather than crashing the poll loop, so a bad
// definition can never take the daemon down.
//
// Scheduled jobs are isolated from foreground goals: each job carries its own
// workspace and per-job budgets, and the daemon runs them through the same
// background execution transport (BackgroundShellBundle) or the in-process goal
// runner via an injected runFn. Budgets are never inherited implicitly.

const kernforgeScheduleStateVersion = 1

const (
	scheduleJobTypeGoal   = "goal"
	scheduleJobTypeVerify = "verify"
	scheduleJobTypeBatch  = "batch"
)

const (
	// defaultSchedulerPollSeconds bounds how often the poll loop wakes to look
	// for due jobs when the daemon scheduler config does not pin a value.
	defaultSchedulerPollSeconds = 30

	// minSchedulerIntervalSeconds is the smallest interval schedule we accept.
	// A faster cadence would let a runaway definition saturate the daemon, so a
	// sub-minimum interval is rejected (fail-closed) rather than clamped.
	minSchedulerIntervalSeconds = 10

	// maxScheduledJobs bounds the persisted registry so a misbehaving caller
	// cannot grow the on-disk state without limit.
	maxScheduledJobs = 64
)

// ScheduleBudgets carries the per-job budgets. Zero means "no budget" for that
// dimension; budgets are never inherited from the daemon or from another job.
type ScheduleBudgets struct {
	TokenBudget       int `json:"token_budget,omitempty"`
	TimeBudgetSeconds int `json:"time_budget_seconds,omitempty"`
	MaxIterations     int `json:"max_iterations,omitempty"`
}

// JobDefinition is one persisted scheduled job. Schedule is either an interval
// (e.g. "30m", "6h") or a 5-field cron subset expression; if both are set the
// interval takes precedence. A definition that fails to parse is kept on disk
// but disabled so the daemon stays up.
type JobDefinition struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Objective string          `json:"objective"`
	Type      string          `json:"type"`
	Workspace string          `json:"workspace,omitempty"`
	Interval  string          `json:"interval,omitempty"`
	Cron      string          `json:"cron,omitempty"`
	Budgets   ScheduleBudgets `json:"budgets,omitempty"`
	Enabled   bool            `json:"enabled"`
	CreatedAt time.Time       `json:"created_at"`
	LastRun   time.Time       `json:"last_run,omitempty"`
	NextRun   time.Time       `json:"next_run,omitempty"`
	// LastError records why a malformed schedule was disabled. It is advisory
	// (surfaced to list) and never alters control flow.
	LastError string `json:"last_error,omitempty"`
}

// kernforgeScheduleState is the on-disk persistence shape, stored alongside the
// daemon state so scheduled jobs survive a daemon restart.
type kernforgeScheduleState struct {
	Version int             `json:"version"`
	Jobs    []JobDefinition `json:"jobs"`
}

// ScheduleRunRequest is handed to the injected runFn when a job is due. The
// runFn owns the actual execution transport (BackgroundShellBundle or the
// in-process goal runner) and must respect the per-job budgets and workspace
// isolation; the scheduler core never runs code itself.
type ScheduleRunRequest struct {
	Job       JobDefinition
	Triggered time.Time
}

// ScheduleRunFunc executes a due job. It must be non-blocking-friendly (the
// scheduler calls it from the poll goroutine), fail closed on error, and never
// touch foreground goal state. Returning an error marks the run failed but does
// not disable the job or stop the loop.
type ScheduleRunFunc func(ScheduleRunRequest) error

// DaemonScheduler is the in-memory registry plus poll loop. It is safe for
// concurrent use. The zero value is not usable; build one with NewDaemonScheduler.
type DaemonScheduler struct {
	mu        sync.Mutex
	jobs      map[string]JobDefinition
	order     []string
	statePath string
	pollEvery time.Duration
	nowFn     func() time.Time
	runFn     ScheduleRunFunc

	ticker  *time.Ticker
	stop    chan struct{}
	done    chan struct{}
	started bool
}

// NewDaemonScheduler builds a scheduler. nowFn MUST be supplied by tests so the
// core never calls time.Now; in production pass time.Now. runFn may be nil for
// pure registry/parsing use (the poll loop then only recomputes NextRun).
func NewDaemonScheduler(statePath string, pollEvery time.Duration, nowFn func() time.Time, runFn ScheduleRunFunc) *DaemonScheduler {
	if nowFn == nil {
		nowFn = time.Now
	}
	if pollEvery <= 0 {
		pollEvery = time.Duration(defaultSchedulerPollSeconds) * time.Second
	}
	return &DaemonScheduler{
		jobs:      map[string]JobDefinition{},
		statePath: strings.TrimSpace(statePath),
		pollEvery: pollEvery,
		nowFn:     nowFn,
		runFn:     runFn,
	}
}

// Load reads persisted jobs from disk. A missing file is not an error. A
// malformed job is kept disabled rather than dropped, so durability never
// silently loses a definition.
func (s *DaemonScheduler) Load() error {
	if s == nil {
		return fmt.Errorf("scheduler is not configured")
	}
	if strings.TrimSpace(s.statePath) == "" {
		return nil
	}
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var state kernforgeScheduleState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse schedule state %s: %w", s.statePath, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = map[string]JobDefinition{}
	s.order = nil
	for _, job := range state.Jobs {
		job.normalize()
		if job.ID == "" {
			continue
		}
		s.applyScheduleLocked(&job, s.nowFn())
		if _, ok := s.jobs[job.ID]; !ok {
			s.order = append(s.order, job.ID)
		}
		s.jobs[job.ID] = job
	}
	return nil
}

func (s *DaemonScheduler) persistLocked() error {
	if strings.TrimSpace(s.statePath) == "" {
		return nil
	}
	state := kernforgeScheduleState{
		Version: kernforgeScheduleStateVersion,
		Jobs:    s.snapshotLocked(),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.statePath, data, 0o600)
}

func (s *DaemonScheduler) snapshotLocked() []JobDefinition {
	out := make([]JobDefinition, 0, len(s.order))
	for _, id := range s.order {
		if job, ok := s.jobs[id]; ok {
			out = append(out, job)
		}
	}
	return out
}

// Jobs returns a stable-ordered snapshot of the registry.
func (s *DaemonScheduler) Jobs() []JobDefinition {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Add registers a new job, parses its schedule, computes NextRun, persists, and
// returns the stored definition. A duplicate name is rejected. A malformed
// schedule is rejected at add time (so the operator sees the error) rather than
// silently disabled; only a previously-stored bad schedule is tolerated on load.
func (s *DaemonScheduler) Add(job JobDefinition) (JobDefinition, error) {
	if s == nil {
		return JobDefinition{}, fmt.Errorf("scheduler is not configured")
	}
	job.normalize()
	if job.Name == "" {
		return JobDefinition{}, fmt.Errorf("scheduled job requires a name")
	}
	if job.Objective == "" {
		return JobDefinition{}, fmt.Errorf("scheduled job requires an objective")
	}
	if !isScheduleJobType(job.Type) {
		return JobDefinition{}, fmt.Errorf("scheduled job type must be one of goal|verify|batch, got %q", job.Type)
	}
	if strings.TrimSpace(job.Interval) == "" && strings.TrimSpace(job.Cron) == "" {
		return JobDefinition{}, fmt.Errorf("scheduled job requires an interval or a cron expression")
	}
	now := s.nowFn()
	if _, err := nextScheduleRun(job, now); err != nil {
		return JobDefinition{}, fmt.Errorf("invalid schedule: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.findByNameLocked(job.Name); ok {
		return JobDefinition{}, fmt.Errorf("a scheduled job named %q already exists", job.Name)
	}
	if len(s.order) >= maxScheduledJobs {
		return JobDefinition{}, fmt.Errorf("cannot add scheduled job: the maximum of %d jobs is already registered", maxScheduledJobs)
	}
	if job.ID == "" {
		job.ID = s.nextJobIDLocked(now)
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	// A freshly added job whose schedule validated is enabled. applyScheduleLocked
	// can only flip it to disabled (it never enables), so a malformed schedule
	// discovered later still fails closed.
	job.Enabled = true
	s.applyScheduleLocked(&job, now)
	s.jobs[job.ID] = job
	s.order = append(s.order, job.ID)
	if err := s.persistLocked(); err != nil {
		delete(s.jobs, job.ID)
		s.order = s.order[:len(s.order)-1]
		return JobDefinition{}, err
	}
	return job, nil
}

// Remove deletes a job by name or id and persists. It reports whether a job was
// removed.
func (s *DaemonScheduler) Remove(selector string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("scheduler is not configured")
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return false, fmt.Errorf("remove requires a job name or id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.resolveSelectorLocked(selector)
	if !ok {
		return false, nil
	}
	delete(s.jobs, id)
	filtered := s.order[:0]
	for _, existing := range s.order {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}
	s.order = append([]string(nil), filtered...)
	if err := s.persistLocked(); err != nil {
		return false, err
	}
	return true, nil
}

// RunNow forces a job to run immediately regardless of its NextRun. It records
// LastRun, recomputes NextRun, persists, and dispatches through runFn. It does
// not require the poll loop to be running. RunNow on a disabled job is rejected.
func (s *DaemonScheduler) RunNow(selector string) (JobDefinition, error) {
	if s == nil {
		return JobDefinition{}, fmt.Errorf("scheduler is not configured")
	}
	now := s.nowFn()
	s.mu.Lock()
	id, ok := s.resolveSelectorLocked(strings.TrimSpace(selector))
	if !ok {
		s.mu.Unlock()
		return JobDefinition{}, fmt.Errorf("no scheduled job matches %q", selector)
	}
	job := s.jobs[id]
	if !job.Enabled {
		s.mu.Unlock()
		return JobDefinition{}, fmt.Errorf("scheduled job %q is disabled: %s", job.Name, firstNonBlankString(job.LastError, "no reason recorded"))
	}
	job.LastRun = now
	s.applyScheduleLocked(&job, now)
	s.jobs[id] = job
	runFn := s.runFn
	if err := s.persistLocked(); err != nil {
		s.mu.Unlock()
		return JobDefinition{}, err
	}
	s.mu.Unlock()

	if runFn != nil {
		if err := runFn(ScheduleRunRequest{Job: job, Triggered: now}); err != nil {
			return job, err
		}
	}
	return job, nil
}

// Poll runs one scheduling pass: every enabled, non-malformed job whose NextRun
// is at or before now is dispatched through runFn, then LastRun is set and
// NextRun recomputed. It returns the jobs that fired. Poll is the single unit of
// scheduling work; the loop goroutine simply calls Poll on each tick. A runFn
// error marks the run but never stops the loop (fail-closed).
func (s *DaemonScheduler) Poll() []JobDefinition {
	if s == nil {
		return nil
	}
	now := s.nowFn()
	s.mu.Lock()
	var due []JobDefinition
	for _, id := range s.order {
		job := s.jobs[id]
		if !job.Enabled {
			continue
		}
		if job.NextRun.IsZero() || job.NextRun.After(now) {
			continue
		}
		job.LastRun = now
		s.applyScheduleLocked(&job, now)
		s.jobs[id] = job
		due = append(due, job)
	}
	runFn := s.runFn
	if len(due) > 0 {
		_ = s.persistLocked()
	}
	s.mu.Unlock()

	if runFn == nil || len(due) == 0 {
		return due
	}
	fired := make([]JobDefinition, 0, len(due))
	for _, job := range due {
		// A panicking runFn must not take the daemon down: contain it per job.
		func(j JobDefinition) {
			defer func() {
				_ = recover()
			}()
			_ = runFn(ScheduleRunRequest{Job: j, Triggered: now})
		}(job)
		fired = append(fired, job)
	}
	return fired
}

// recordRunError stores reason on a job's advisory LastError so an operator
// listing jobs can see why the last unattended run failed. It is called by the
// daemon's runFn after a fired job's execution transport fails. It deliberately
// does NOT disable the job or change its NextRun: a transient run failure must not
// stop an otherwise healthy schedule (a malformed SCHEDULE is the only thing that
// disables a job, via applyScheduleLocked). A missing job id is a no-op. It never
// returns an error; a persist failure is swallowed because the in-memory LastError
// is still surfaced to list.
func (s *DaemonScheduler) recordRunError(jobID string, reason string) {
	if s == nil {
		return
	}
	jobID = strings.TrimSpace(jobID)
	reason = truncateStatusSnippet(firstNonEmptyLine(reason), 200)
	if jobID == "" || reason == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return
	}
	job.LastError = reason
	s.jobs[jobID] = job
	_ = s.persistLocked()
}

// Start launches the poll loop goroutine. It is idempotent. Stop terminates it.
func (s *DaemonScheduler) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.ticker = time.NewTicker(s.pollEvery)
	stop := s.stop
	done := s.done
	ticker := s.ticker
	s.mu.Unlock()

	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.Poll()
			}
		}
	}()
}

// Stop terminates the poll loop and waits for the goroutine to exit. It is safe
// to call when not started.
func (s *DaemonScheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	ticker := s.ticker
	stop := s.stop
	done := s.done
	s.ticker = nil
	s.stop = nil
	s.done = nil
	s.mu.Unlock()

	if ticker != nil {
		ticker.Stop()
	}
	if stop != nil {
		close(stop)
	}
	if done != nil {
		<-done
	}
}

func (s *DaemonScheduler) findByNameLocked(name string) (string, bool) {
	name = strings.TrimSpace(name)
	for id, job := range s.jobs {
		if strings.EqualFold(strings.TrimSpace(job.Name), name) {
			return id, true
		}
	}
	return "", false
}

func (s *DaemonScheduler) resolveSelectorLocked(selector string) (string, bool) {
	if _, ok := s.jobs[selector]; ok {
		return selector, true
	}
	return s.findByNameLocked(selector)
}

func (s *DaemonScheduler) nextJobIDLocked(now time.Time) string {
	base := fmt.Sprintf("sched-%s", now.Format("20060102-150405"))
	id := base
	suffix := 0
	for {
		if _, ok := s.jobs[id]; !ok {
			return id
		}
		suffix++
		id = fmt.Sprintf("%s-%03d", base, suffix)
	}
}

// applyScheduleLocked recomputes NextRun and Enabled. A malformed schedule fails
// closed: the job is disabled, NextRun is zeroed, and the reason is recorded.
// This is the single place that decides whether a job is runnable, so a bad
// schedule can never reach the run path.
func (s *DaemonScheduler) applyScheduleLocked(job *JobDefinition, now time.Time) {
	if job == nil {
		return
	}
	next, err := nextScheduleRun(*job, now)
	if err != nil {
		job.Enabled = false
		job.NextRun = time.Time{}
		job.LastError = truncateStatusSnippet(firstNonEmptyLine(err.Error()), 200)
		return
	}
	job.LastError = ""
	job.NextRun = next
}

func (j *JobDefinition) normalize() {
	if j == nil {
		return
	}
	j.ID = strings.TrimSpace(j.ID)
	j.Name = strings.Join(strings.Fields(strings.TrimSpace(j.Name)), " ")
	j.Objective = strings.TrimSpace(j.Objective)
	j.Type = strings.TrimSpace(strings.ToLower(j.Type))
	if j.Type == "" {
		j.Type = scheduleJobTypeGoal
	}
	j.Workspace = strings.TrimSpace(j.Workspace)
	j.Interval = strings.TrimSpace(j.Interval)
	j.Cron = strings.TrimSpace(j.Cron)
	if j.Budgets.TokenBudget < 0 {
		j.Budgets.TokenBudget = 0
	}
	if j.Budgets.TimeBudgetSeconds < 0 {
		j.Budgets.TimeBudgetSeconds = 0
	}
	if j.Budgets.MaxIterations < 0 {
		j.Budgets.MaxIterations = 0
	}
	j.LastError = strings.TrimSpace(j.LastError)
}

func isScheduleJobType(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case scheduleJobTypeGoal, scheduleJobTypeVerify, scheduleJobTypeBatch:
		return true
	default:
		return false
	}
}

// nextScheduleRun computes the next run time strictly after the reference point.
// Interval takes precedence over cron when both are set. An empty or malformed
// schedule returns an error so the caller can fail closed.
func nextScheduleRun(job JobDefinition, now time.Time) (time.Time, error) {
	if interval := strings.TrimSpace(job.Interval); interval != "" {
		d, err := parseScheduleInterval(interval)
		if err != nil {
			return time.Time{}, err
		}
		base := job.LastRun
		if base.IsZero() {
			base = now
		}
		next := base.Add(d)
		// If LastRun is far in the past, advance to the first slot after now so a
		// long daemon downtime does not cause a burst of catch-up runs.
		if !next.After(now) {
			next = now.Add(d)
		}
		return next, nil
	}
	if cronExpr := strings.TrimSpace(job.Cron); cronExpr != "" {
		return nextCronRun(cronExpr, now)
	}
	return time.Time{}, fmt.Errorf("no interval or cron schedule is set")
}

// parseScheduleInterval parses a minimal interval form: a positive integer
// followed by a unit s|m|h|d (e.g. "30m", "6h", "1d"). It deliberately does NOT
// accept time.ParseDuration's full grammar so the surface is small and the
// minimum-cadence floor is enforced consistently.
func parseScheduleInterval(input string) (time.Duration, error) {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return 0, fmt.Errorf("empty interval")
	}
	unit := input[len(input)-1]
	numberPart := input[:len(input)-1]
	if numberPart == "" {
		return 0, fmt.Errorf("interval %q is missing a value before the unit", input)
	}
	value, err := strconv.Atoi(numberPart)
	if err != nil {
		return 0, fmt.Errorf("interval %q has a non-integer value", input)
	}
	if value <= 0 {
		return 0, fmt.Errorf("interval %q must be positive", input)
	}
	var unitDuration time.Duration
	switch unit {
	case 's':
		unitDuration = time.Second
	case 'm':
		unitDuration = time.Minute
	case 'h':
		unitDuration = time.Hour
	case 'd':
		unitDuration = 24 * time.Hour
	default:
		return 0, fmt.Errorf("interval %q has an unknown unit %q (use s, m, h, or d)", input, string(unit))
	}
	d := time.Duration(value) * unitDuration
	if d < time.Duration(minSchedulerIntervalSeconds)*time.Second {
		return 0, fmt.Errorf("interval %q is below the minimum of %ds", input, minSchedulerIntervalSeconds)
	}
	return d, nil
}

// cronField is a parsed single field of a 5-field cron expression as a set of
// allowed integer values within its min..max range.
type cronField struct {
	allowed map[int]bool
}

func (f cronField) matches(value int) bool {
	return f.allowed[value]
}

// nextCronRun computes the next minute-aligned time strictly after now that
// matches a 5-field cron subset: "minute hour day-of-month month day-of-week".
// Supported tokens per field: "*", a single integer, a comma list, a step on a
// wildcard ("*/N"), and an inclusive range ("a-b"). Day-of-week 0 and 7 both
// mean Sunday. This is intentionally a subset (no "L", "W", "#", or named
// months/days) to avoid an external dependency.
func nextCronRun(expr string, now time.Time) (time.Time, error) {
	fields, err := parseCronExpression(expr)
	if err != nil {
		return time.Time{}, err
	}
	// Start at the next whole minute after now; cron is minute-resolution.
	cursor := now.Truncate(time.Minute).Add(time.Minute)
	// Bound the search to one year of minutes so an impossible expression (e.g.
	// "0 0 30 2 *") fails closed instead of looping forever.
	maxMinutes := 366 * 24 * 60
	for i := 0; i < maxMinutes; i++ {
		if cronTimeMatches(fields, cursor) {
			return cursor, nil
		}
		cursor = cursor.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron expression %q has no matching time within a year", expr)
}

func cronTimeMatches(fields [5]cronField, t time.Time) bool {
	if !fields[0].matches(t.Minute()) {
		return false
	}
	if !fields[1].matches(t.Hour()) {
		return false
	}
	if !fields[3].matches(int(t.Month())) {
		return false
	}
	// Standard cron semantics: when both day-of-month and day-of-week are
	// restricted (neither is a full wildcard), a match on EITHER is sufficient.
	domRestricted := !fields[2].allowed[cronWildcardSentinel]
	dowRestricted := !fields[4].allowed[cronWildcardSentinel]
	dom := fields[2].matches(t.Day())
	dow := fields[4].matches(cronWeekday(t))
	switch {
	case domRestricted && dowRestricted:
		return dom || dow
	case domRestricted:
		return dom
	case dowRestricted:
		return dow
	default:
		return true
	}
}

// cronWildcardSentinel marks a field that was specified as "*". It is stored in
// the allowed set in addition to the concrete values so the day-of-month vs
// day-of-week OR semantics can tell a wildcard from an explicit full range.
const cronWildcardSentinel = -1

func cronWeekday(t time.Time) int {
	return int(t.Weekday())
}

func parseCronExpression(expr string) ([5]cronField, error) {
	var out [5]cronField
	parts := strings.Fields(strings.TrimSpace(expr))
	if len(parts) != 5 {
		return out, fmt.Errorf("cron expression must have exactly 5 fields (minute hour day-of-month month day-of-week), got %d", len(parts))
	}
	bounds := [5][2]int{
		{0, 59}, // minute
		{0, 23}, // hour
		{1, 31}, // day of month
		{1, 12}, // month
		{0, 7},  // day of week (0 and 7 both Sunday)
	}
	for i := 0; i < 5; i++ {
		field, err := parseCronField(parts[i], bounds[i][0], bounds[i][1])
		if err != nil {
			return out, fmt.Errorf("cron field %d (%q): %w", i+1, parts[i], err)
		}
		out[i] = field
	}
	return out, nil
}

func parseCronField(token string, min int, max int) (cronField, error) {
	token = strings.TrimSpace(token)
	field := cronField{allowed: map[int]bool{}}
	if token == "" {
		return field, fmt.Errorf("empty field")
	}
	for _, segment := range strings.Split(token, ",") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return field, fmt.Errorf("empty list element")
		}
		if err := addCronSegment(&field, segment, min, max); err != nil {
			return field, err
		}
	}
	if len(field.allowed) == 0 {
		return field, fmt.Errorf("no values matched")
	}
	return field, nil
}

func addCronSegment(field *cronField, segment string, min int, max int) error {
	// Step on a wildcard or range: "*/N" or "a-b/N".
	step := 1
	body := segment
	if slash := strings.Index(segment, "/"); slash >= 0 {
		body = strings.TrimSpace(segment[:slash])
		stepText := strings.TrimSpace(segment[slash+1:])
		value, err := strconv.Atoi(stepText)
		if err != nil || value <= 0 {
			return fmt.Errorf("invalid step %q", stepText)
		}
		step = value
	}
	if body == "*" {
		field.allowed[cronWildcardSentinel] = true
		for v := min; v <= max; v += step {
			field.markCron(v, min, max)
		}
		return nil
	}
	if dash := strings.Index(body, "-"); dash >= 0 {
		lowText := strings.TrimSpace(body[:dash])
		highText := strings.TrimSpace(body[dash+1:])
		low, err := strconv.Atoi(lowText)
		if err != nil {
			return fmt.Errorf("invalid range start %q", lowText)
		}
		high, err := strconv.Atoi(highText)
		if err != nil {
			return fmt.Errorf("invalid range end %q", highText)
		}
		if low > high {
			return fmt.Errorf("range start %d is greater than end %d", low, high)
		}
		if low < min || high > max {
			return fmt.Errorf("range %d-%d is outside %d-%d", low, high, min, max)
		}
		for v := low; v <= high; v += step {
			field.markCron(v, min, max)
		}
		return nil
	}
	value, err := strconv.Atoi(body)
	if err != nil {
		return fmt.Errorf("invalid value %q", body)
	}
	if value < min || value > max {
		return fmt.Errorf("value %d is outside %d-%d", value, min, max)
	}
	field.markCron(value, min, max)
	return nil
}

// markCron records a value, normalizing day-of-week 7 to 0 so both spellings of
// Sunday match.
func (f *cronField) markCron(value int, min int, max int) {
	if min == 0 && max == 7 && value == 7 {
		value = 0
	}
	f.allowed[value] = true
}

// renderScheduleJobLines formats jobs for the list subcommand. Times use RFC3339
// in the local zone; an empty time renders as "-".
func renderScheduleJobLines(jobs []JobDefinition) []string {
	sorted := append([]JobDefinition(nil), jobs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	lines := make([]string, 0, len(sorted))
	for _, job := range sorted {
		schedule := job.Interval
		if schedule == "" {
			schedule = "cron(" + job.Cron + ")"
		} else {
			schedule = "every " + schedule
		}
		state := "enabled"
		if !job.Enabled {
			state = "disabled"
			if job.LastError != "" {
				state += ": " + job.LastError
			}
		}
		line := fmt.Sprintf("%s [%s] type=%s %s next=%s %s",
			job.Name,
			job.ID,
			job.Type,
			schedule,
			scheduleTimeOrDash(job.NextRun),
			state,
		)
		lines = append(lines, line)
	}
	return lines
}

func scheduleTimeOrDash(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339)
}
