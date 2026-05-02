package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var runPRReviewGitHubCommand = func(root string, args ...string) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", err
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = root
	data, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(data)), err
}

const (
	AutomationTypeRecurringVerification = "recurring_verification"
	AutomationTypePRReview              = "pr_review"

	AutomationStatusActive   = "active"
	AutomationStatusPaused   = "paused"
	AutomationStatusRunning  = "running"
	AutomationStatusComplete = "complete"
	AutomationStatusFailed   = "failed"
)

type SessionAutomation struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	Command     string    `json:"command"`
	Status      string    `json:"status"`
	Schedule    string    `json:"schedule,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	LastRunAt   time.Time `json:"last_run_at,omitempty"`
	NextRunAt   time.Time `json:"next_run_at,omitempty"`
	NextRunHint string    `json:"next_run_hint,omitempty"`
	LastResult  string    `json:"last_result,omitempty"`
}

func (s *Session) normalizeAutomations() {
	if s == nil {
		return
	}
	items := make([]SessionAutomation, 0, len(s.Automations))
	seen := map[string]struct{}{}
	for _, item := range s.Automations {
		item = normalizeSessionAutomation(item)
		if item.ID == "" {
			continue
		}
		key := strings.ToLower(item.ID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}
	if len(items) > 40 {
		items = append([]SessionAutomation(nil), items[len(items)-40:]...)
	}
	s.Automations = items
}

func normalizeSessionAutomation(item SessionAutomation) SessionAutomation {
	now := time.Now()
	item.ID = strings.TrimSpace(item.ID)
	item.Type = strings.TrimSpace(strings.ToLower(item.Type))
	item.Name = strings.TrimSpace(item.Name)
	item.Command = strings.TrimSpace(item.Command)
	item.Status = strings.TrimSpace(strings.ToLower(item.Status))
	item.Schedule = strings.TrimSpace(item.Schedule)
	item.NextRunHint = strings.TrimSpace(item.NextRunHint)
	item.LastResult = compactPromptSection(item.LastResult, 360)
	if item.Type == "" {
		item.Type = AutomationTypeRecurringVerification
	}
	if item.Name == "" {
		item.Name = strings.ReplaceAll(item.Type, "_", " ")
	}
	if item.Command == "" {
		if item.Type == AutomationTypePRReview {
			item.Command = "/review-pr"
		} else {
			item.Command = "/verify"
		}
	}
	switch item.Status {
	case "", AutomationStatusActive, AutomationStatusPaused, AutomationStatusRunning, AutomationStatusComplete, AutomationStatusFailed:
		if item.Status == "" {
			item.Status = AutomationStatusActive
		}
	default:
		item.Status = AutomationStatusActive
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	item = refreshAutomationScheduleHint(item, now)
	return item
}

func (rt *runtimeState) handleAutomationCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	rt.session.normalizeAutomations()
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 || strings.EqualFold(fields[0], "list") || strings.EqualFold(fields[0], "status") {
		rt.printAutomationStatus()
		return rt.store.Save(rt.session)
	}
	switch strings.ToLower(fields[0]) {
	case "add":
		return rt.handleAutomationAdd(fields[1:])
	case "due":
		rt.printAutomationDueStatus(time.Now())
		return rt.store.Save(rt.session)
	case "digest":
		rt.printAutomationDigest(time.Now())
		return rt.store.Save(rt.session)
	case "monitor":
		notifyOptions, parseErr := parseAutomationNotificationOptions(fields[1:], false)
		if parseErr != nil {
			return parseErr
		}
		err := rt.monitorAutomations(time.Now())
		if notifyOptions.ShouldNotify() {
			if notifyErr := rt.dispatchAutomationNotification(time.Now(), notifyOptions); notifyErr != nil && err == nil {
				err = notifyErr
			}
		}
		return err
	case "watch":
		options, err := parseAutomationWatchOptions(fields[1:])
		if err != nil {
			return err
		}
		return rt.watchAutomations(options)
	case "daemon-start", "start-daemon":
		options, err := parseAutomationWatchOptions(fields[1:])
		if err != nil {
			return err
		}
		if options.Cycles > 0 {
			return fmt.Errorf("automation daemon-start does not accept --cycles; use /automation watch for finite loops")
		}
		if !options.Notify && strings.TrimSpace(options.WebhookURL) == "" {
			options.Notify = true
		}
		return rt.startAutomationDaemon(options)
	case "daemon-status", "daemon":
		return rt.printAutomationDaemonStatus()
	case "daemon-stop", "stop-daemon":
		return rt.stopAutomationDaemon()
	case "notify":
		notifyOptions, err := parseAutomationNotificationOptions(fields[1:], true)
		if err != nil {
			return err
		}
		now := time.Now()
		rt.printAutomationDigest(now)
		return rt.dispatchAutomationNotification(now, notifyOptions)
	case "run-due":
		return rt.runDueAutomations(time.Now())
	case "run":
		if len(fields) < 2 {
			return fmt.Errorf("usage: /automation run <id>")
		}
		return rt.runAutomation(fields[1])
	case "pause", "resume", "remove":
		if len(fields) < 2 {
			return fmt.Errorf("usage: /automation %s <id>", strings.ToLower(fields[0]))
		}
		return rt.updateAutomationState(strings.ToLower(fields[0]), fields[1])
	default:
		return fmt.Errorf("usage: /automation [list|due|digest|monitor [--notify]|watch [--interval 5m] [--cycles N] [--notify]|daemon-start|daemon-status|daemon-stop|notify|run-due|add recurring-verification|add pr-review|run <id>|pause <id>|resume <id>|remove <id>]")
	}
}

func (rt *runtimeState) handleAutomationAdd(fields []string) error {
	if len(fields) == 0 {
		return fmt.Errorf("usage: /automation add <recurring-verification|pr-review> [--every <duration>|--manual] [command]")
	}
	typ := strings.ToLower(strings.TrimSpace(fields[0]))
	options, commandFields, err := parseAutomationAddOptions(fields[1:])
	if err != nil {
		return err
	}
	command := strings.TrimSpace(strings.Join(commandFields, " "))
	switch typ {
	case "verification", "recurring-verification", AutomationTypeRecurringVerification:
		typ = AutomationTypeRecurringVerification
		if command == "" {
			command = "/verify"
		}
	case "pr", "pr-review", AutomationTypePRReview:
		typ = AutomationTypePRReview
		if command == "" {
			command = "/review-pr"
		}
	default:
		return fmt.Errorf("unknown automation type: %s", fields[0])
	}
	if err := validateAutomationCommand(typ, command); err != nil {
		return err
	}
	now := time.Now()
	schedule := strings.TrimSpace(options.Schedule)
	if schedule == "" {
		schedule = "manual-recurring"
	}
	id := automationID(typ, command)
	item := normalizeSessionAutomation(SessionAutomation{
		ID:        id,
		Type:      typ,
		Command:   command,
		Status:    AutomationStatusActive,
		Schedule:  schedule,
		CreatedAt: now,
		UpdatedAt: now,
	})
	for index := range rt.session.Automations {
		if strings.EqualFold(rt.session.Automations[index].ID, item.ID) {
			rt.session.Automations[index] = item
			fmt.Fprintln(rt.writer, rt.ui.successLine("Updated automation: "+item.ID))
			return rt.store.Save(rt.session)
		}
	}
	rt.session.Automations = append(rt.session.Automations, item)
	fmt.Fprintln(rt.writer, rt.ui.successLine("Added automation: "+item.ID))
	return rt.store.Save(rt.session)
}

func automationID(typ string, command string) string {
	key := strings.TrimSpace(typ) + ":" + strings.TrimSpace(command)
	return "auto-" + shortStableID(key)
}

type automationAddOptions struct {
	Schedule string
}

func parseAutomationAddOptions(fields []string) (automationAddOptions, []string, error) {
	options := automationAddOptions{}
	for index := 0; index < len(fields); index++ {
		field := strings.TrimSpace(fields[index])
		lower := strings.ToLower(field)
		switch {
		case lower == "--manual":
			options.Schedule = "manual-recurring"
		case lower == "--hourly":
			options.Schedule = "every 1h"
		case lower == "--daily":
			options.Schedule = "every 24h"
		case lower == "--every":
			if index+1 >= len(fields) {
				return options, nil, fmt.Errorf("--every requires a duration such as 30m, 2h, or 1d")
			}
			schedule, err := normalizeAutomationIntervalSchedule(fields[index+1])
			if err != nil {
				return options, nil, err
			}
			options.Schedule = schedule
			index++
		case strings.HasPrefix(lower, "--every="):
			schedule, err := normalizeAutomationIntervalSchedule(strings.TrimPrefix(field, "--every="))
			if err != nil {
				return options, nil, err
			}
			options.Schedule = schedule
		case strings.HasPrefix(lower, "--schedule="):
			schedule, err := normalizeAutomationScheduleSpec(strings.TrimPrefix(field, "--schedule="))
			if err != nil {
				return options, nil, err
			}
			options.Schedule = schedule
		case strings.HasPrefix(field, "/"):
			return options, fields[index:], nil
		default:
			return options, fields[index:], nil
		}
	}
	return options, nil, nil
}

func normalizeAutomationIntervalSchedule(raw string) (string, error) {
	duration, err := parseAutomationDuration(raw)
	if err != nil {
		return "", err
	}
	return "every " + duration.String(), nil
}

func normalizeAutomationScheduleSpec(raw string) (string, error) {
	spec := strings.TrimSpace(strings.ToLower(raw))
	switch spec {
	case "", "manual", "manual-recurring":
		return "manual-recurring", nil
	case "hourly":
		return "every 1h", nil
	case "daily":
		return "every 24h", nil
	}
	for _, prefix := range []string{"every ", "every:", "@every "} {
		if strings.HasPrefix(spec, prefix) {
			return normalizeAutomationIntervalSchedule(strings.TrimSpace(strings.TrimPrefix(spec, prefix)))
		}
	}
	return "", fmt.Errorf("unsupported automation schedule: %s", raw)
}

func parseAutomationDuration(raw string) (time.Duration, error) {
	text := strings.TrimSpace(strings.ToLower(raw))
	if text == "" {
		return 0, fmt.Errorf("automation schedule duration is empty")
	}
	if duration, err := time.ParseDuration(text); err == nil {
		if duration < time.Minute {
			return 0, fmt.Errorf("automation schedule duration must be at least 1m")
		}
		return duration, nil
	}
	multiplier := time.Duration(0)
	numberText := text
	switch {
	case strings.HasSuffix(text, "d"):
		multiplier = 24 * time.Hour
		numberText = strings.TrimSuffix(text, "d")
	case strings.HasSuffix(text, "w"):
		multiplier = 7 * 24 * time.Hour
		numberText = strings.TrimSuffix(text, "w")
	}
	if multiplier == 0 {
		return 0, fmt.Errorf("invalid automation schedule duration: %s", raw)
	}
	count, err := strconv.Atoi(strings.TrimSpace(numberText))
	if err != nil || count < 1 {
		return 0, fmt.Errorf("invalid automation schedule duration: %s", raw)
	}
	return time.Duration(count) * multiplier, nil
}

func automationScheduleDuration(schedule string) (time.Duration, bool) {
	spec := strings.TrimSpace(strings.ToLower(schedule))
	if spec == "" || spec == "manual" || spec == "manual-recurring" {
		return 0, false
	}
	normalized, err := normalizeAutomationScheduleSpec(spec)
	if err != nil {
		return 0, false
	}
	for _, prefix := range []string{"every ", "every:", "@every "} {
		if strings.HasPrefix(normalized, prefix) {
			duration, err := parseAutomationDuration(strings.TrimSpace(strings.TrimPrefix(normalized, prefix)))
			if err == nil {
				return duration, true
			}
		}
	}
	return 0, false
}

func refreshAutomationScheduleHint(item SessionAutomation, now time.Time) SessionAutomation {
	if strings.TrimSpace(item.ID) == "" {
		return item
	}
	normalized, err := normalizeAutomationScheduleSpec(item.Schedule)
	if err != nil {
		normalized = "manual-recurring"
	}
	item.Schedule = normalized
	duration, scheduled := automationScheduleDuration(item.Schedule)
	if !scheduled {
		item.NextRunAt = time.Time{}
		item.NextRunHint = "run with /automation run " + item.ID
		return item
	}
	anchor := item.CreatedAt
	if !item.LastRunAt.IsZero() {
		anchor = item.LastRunAt
	}
	if anchor.IsZero() {
		anchor = now
	}
	if item.NextRunAt.IsZero() || !item.NextRunAt.After(anchor) {
		item.NextRunAt = anchor.Add(duration)
	}
	item.NextRunHint = "next due at " + item.NextRunAt.Format(time.RFC3339) + "; run with /automation run-due"
	return item
}

func automationIsDue(item SessionAutomation, now time.Time) bool {
	if !strings.EqualFold(item.Status, AutomationStatusActive) {
		return false
	}
	if _, scheduled := automationScheduleDuration(item.Schedule); !scheduled {
		return false
	}
	item = refreshAutomationScheduleHint(item, now)
	if item.NextRunAt.IsZero() {
		return false
	}
	return !item.NextRunAt.After(now)
}

type automationRuntimeSummary struct {
	Total     int
	Active    int
	Scheduled int
	Due       int
	Failed    int
	Paused    int
}

func summarizeAutomations(items []SessionAutomation, now time.Time) automationRuntimeSummary {
	summary := automationRuntimeSummary{Total: len(items)}
	for _, item := range items {
		switch strings.TrimSpace(strings.ToLower(item.Status)) {
		case AutomationStatusActive:
			summary.Active++
		case AutomationStatusFailed:
			summary.Failed++
		case AutomationStatusPaused:
			summary.Paused++
		}
		if _, scheduled := automationScheduleDuration(item.Schedule); scheduled {
			summary.Scheduled++
		}
		if automationIsDue(item, now) {
			summary.Due++
		}
	}
	return summary
}

func automationSummaryLine(summary automationRuntimeSummary) string {
	return fmt.Sprintf("total=%d active=%d scheduled=%d due=%d failed=%d paused=%d", summary.Total, summary.Active, summary.Scheduled, summary.Due, summary.Failed, summary.Paused)
}

func validateAutomationCommand(typ string, command string) error {
	cmd, ok := ParseCommand(command)
	if !ok {
		return fmt.Errorf("automation command must be a slash command: %s", command)
	}
	switch strings.TrimSpace(typ) {
	case AutomationTypeRecurringVerification:
		switch cmd.Name {
		case "verify", "verify-dashboard", "verify-dashboard-html":
			return nil
		default:
			return fmt.Errorf("recurring verification automation only allows /verify and verification dashboard commands")
		}
	case AutomationTypePRReview:
		if cmd.Name == "review-pr" {
			options := parsePRReviewAutomationOptions(cmd.Args)
			if options.HasGitHubWrite() {
				return fmt.Errorf("PR review automation cannot perform GitHub write-side actions; use /review-pr write flags manually")
			}
			return nil
		}
		return fmt.Errorf("PR review automation only allows /review-pr")
	default:
		return fmt.Errorf("unknown automation type: %s", typ)
	}
}

func (rt *runtimeState) printAutomationStatus() {
	fmt.Fprintln(rt.writer, rt.ui.section("Automations"))
	if len(rt.session.Automations) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No automations configured."))
		return
	}
	now := time.Now()
	for _, item := range rt.session.Automations {
		line := fmt.Sprintf("%s  type=%s  status=%s  schedule=%s  command=%s", item.ID, item.Type, item.Status, valueOrDefault(item.Schedule, "manual-recurring"), item.Command)
		if automationIsDue(item, now) {
			line += "  due=yes"
		} else if !item.NextRunAt.IsZero() {
			line += "  next=" + item.NextRunAt.Format(time.RFC3339)
		}
		if strings.TrimSpace(item.LastResult) != "" {
			line += "  result=" + compactPromptSection(item.LastResult, 120)
		}
		fmt.Fprintln(rt.writer, line)
	}
}

func (rt *runtimeState) printAutomationDueStatus(now time.Time) {
	fmt.Fprintln(rt.writer, rt.ui.section("Due Automations"))
	due := 0
	for _, item := range rt.session.Automations {
		if !automationIsDue(item, now) {
			continue
		}
		due++
		fmt.Fprintf(rt.writer, "%s  type=%s  command=%s  schedule=%s\n", item.ID, item.Type, item.Command, valueOrDefault(item.Schedule, "manual-recurring"))
	}
	if due == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No due automations."))
	}
}

func (rt *runtimeState) printAutomationDigest(now time.Time) {
	fmt.Fprintln(rt.writer, rt.ui.section("Automation Digest"))
	summary := summarizeAutomations(rt.session.Automations, now)
	fmt.Fprintln(rt.writer, automationSummaryLine(summary))
	if summary.Total == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No automations configured."))
		return
	}
	for _, item := range rt.session.Automations {
		if !automationIsDue(item, now) && !strings.EqualFold(item.Status, AutomationStatusFailed) {
			continue
		}
		line := fmt.Sprintf("%s  type=%s  status=%s  schedule=%s  command=%s", item.ID, item.Type, item.Status, valueOrDefault(item.Schedule, "manual-recurring"), item.Command)
		if automationIsDue(item, now) {
			line += "  due=yes"
		}
		if strings.TrimSpace(item.LastResult) != "" {
			line += "  result=" + compactPromptSection(item.LastResult, 160)
		}
		fmt.Fprintln(rt.writer, line)
	}
	if summary.Due == 0 && summary.Failed == 0 {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("No due or failed automations."))
	}
}

func (rt *runtimeState) printAutomationStartupNotice(now time.Time) {
	if rt == nil || rt.session == nil {
		return
	}
	summary := summarizeAutomations(rt.session.Automations, now)
	if summary.Due == 0 && summary.Failed == 0 {
		return
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine("Automation attention: "+automationSummaryLine(summary)+". Use /automation digest or /automation run-due."))
}

func (rt *runtimeState) updateAutomationState(action string, id string) error {
	for index := range rt.session.Automations {
		if !strings.EqualFold(rt.session.Automations[index].ID, strings.TrimSpace(id)) {
			continue
		}
		switch action {
		case "pause":
			rt.session.Automations[index].Status = AutomationStatusPaused
		case "resume":
			rt.session.Automations[index].Status = AutomationStatusActive
			rt.session.Automations[index] = refreshAutomationScheduleHint(rt.session.Automations[index], time.Now())
		case "remove":
			rt.session.Automations = append(rt.session.Automations[:index], rt.session.Automations[index+1:]...)
			fmt.Fprintln(rt.writer, rt.ui.successLine("Removed automation: "+id))
			return rt.store.Save(rt.session)
		}
		rt.session.Automations[index].UpdatedAt = time.Now()
		fmt.Fprintln(rt.writer, rt.ui.successLine("Updated automation: "+id))
		return rt.store.Save(rt.session)
	}
	return fmt.Errorf("automation not found: %s", id)
}

func (rt *runtimeState) runAutomation(id string) error {
	return rt.runAutomationAt(id, time.Now())
}

func (rt *runtimeState) runAutomationAt(id string, now time.Time) error {
	for index := range rt.session.Automations {
		if !strings.EqualFold(rt.session.Automations[index].ID, strings.TrimSpace(id)) {
			continue
		}
		item := rt.session.Automations[index]
		if item.Status == AutomationStatusPaused {
			return fmt.Errorf("automation is paused: %s", id)
		}
		rt.session.Automations[index].Status = AutomationStatusRunning
		rt.session.Automations[index].LastRunAt = now
		rt.session.Automations[index].UpdatedAt = now
		result, err := rt.executeSafeSuggestionCommand(item.Command)
		if err != nil {
			rt.session.Automations[index].Status = AutomationStatusFailed
			rt.session.Automations[index].LastResult = err.Error()
			rt.session.Automations[index] = refreshAutomationScheduleHint(rt.session.Automations[index], now)
			_ = rt.store.Save(rt.session)
			return err
		}
		rt.session.Automations[index].Status = AutomationStatusActive
		rt.session.Automations[index].LastResult = result
		rt.session.Automations[index] = refreshAutomationScheduleHint(rt.session.Automations[index], now)
		rt.session.AppendConversationEvent(ConversationEvent{
			Kind:     conversationEventKindVerification,
			Severity: conversationSeverityInfo,
			Summary:  "automation run completed: " + item.ID,
			Entities: map[string]string{
				"automation_id": item.ID,
				"automation":    item.Type,
				"command":       item.Command,
			},
		})
		fmt.Fprintln(rt.writer, rt.ui.successLine("Automation completed: "+item.ID))
		return rt.store.Save(rt.session)
	}
	return fmt.Errorf("automation not found: %s", id)
}

func (rt *runtimeState) runDueAutomations(now time.Time) error {
	ids := make([]string, 0)
	for _, item := range rt.session.Automations {
		if automationIsDue(item, now) {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No due automations."))
		return rt.store.Save(rt.session)
	}
	failures := make([]string, 0)
	for _, id := range ids {
		if err := rt.runAutomationAt(id, now); err != nil {
			failures = append(failures, id+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("due automation failures: %s", strings.Join(failures, "; "))
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Due automations completed: %d", len(ids))))
	return rt.store.Save(rt.session)
}

func (rt *runtimeState) monitorAutomations(now time.Time) error {
	summary := summarizeAutomations(rt.session.Automations, now)
	if summary.Total == 0 {
		rt.printAutomationDigest(now)
		return rt.store.Save(rt.session)
	}
	if summary.Due == 0 {
		rt.printAutomationDigest(now)
		return rt.store.Save(rt.session)
	}
	err := rt.runDueAutomations(now)
	rt.printAutomationDigest(time.Now())
	return err
}

type automationWatchOptions struct {
	Interval   time.Duration
	Cycles     int
	Notify     bool
	WebhookURL string
}

func parseAutomationWatchOptions(fields []string) (automationWatchOptions, error) {
	options := automationWatchOptions{
		Interval: 5 * time.Minute,
		Cycles:   0,
	}
	for index := 0; index < len(fields); index++ {
		field := strings.TrimSpace(fields[index])
		lower := strings.ToLower(field)
		switch {
		case lower == "--notify" || lower == "--write-digest" || lower == "--digest-file":
			options.Notify = true
		case lower == "--no-notify" || lower == "--no-digest-file":
			options.Notify = false
		case lower == "--webhook-url" || lower == "--webhook":
			if index+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a URL", field)
			}
			options.Notify = true
			options.WebhookURL = strings.TrimSpace(fields[index+1])
			index++
		case strings.HasPrefix(lower, "--webhook-url="):
			options.Notify = true
			options.WebhookURL = strings.TrimSpace(field[len("--webhook-url="):])
		case strings.HasPrefix(lower, "--webhook="):
			options.Notify = true
			options.WebhookURL = strings.TrimSpace(field[len("--webhook="):])
		case lower == "--once":
			options.Cycles = 1
		case lower == "--interval" || lower == "--every":
			if index+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a duration such as 30s, 5m, or 1h", field)
			}
			interval, err := parseAutomationWatchInterval(fields[index+1])
			if err != nil {
				return options, err
			}
			options.Interval = interval
			index++
		case strings.HasPrefix(lower, "--interval="):
			interval, err := parseAutomationWatchInterval(strings.TrimSpace(field[len("--interval="):]))
			if err != nil {
				return options, err
			}
			options.Interval = interval
		case strings.HasPrefix(lower, "--every="):
			interval, err := parseAutomationWatchInterval(strings.TrimSpace(field[len("--every="):]))
			if err != nil {
				return options, err
			}
			options.Interval = interval
		case lower == "--cycles" || lower == "--iterations":
			if index+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a non-negative integer", field)
			}
			cycles, err := parseAutomationWatchCycles(fields[index+1])
			if err != nil {
				return options, err
			}
			options.Cycles = cycles
			index++
		case strings.HasPrefix(lower, "--cycles="):
			cycles, err := parseAutomationWatchCycles(strings.TrimSpace(field[len("--cycles="):]))
			if err != nil {
				return options, err
			}
			options.Cycles = cycles
		case strings.HasPrefix(lower, "--iterations="):
			cycles, err := parseAutomationWatchCycles(strings.TrimSpace(field[len("--iterations="):]))
			if err != nil {
				return options, err
			}
			options.Cycles = cycles
		default:
			return options, fmt.Errorf("unknown automation watch option: %s", field)
		}
	}
	return options, nil
}

func parseAutomationWatchInterval(raw string) (time.Duration, error) {
	text := strings.TrimSpace(strings.ToLower(raw))
	if text == "" {
		return 0, fmt.Errorf("automation watch interval is empty")
	}
	duration, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("invalid automation watch interval: %s", raw)
	}
	if duration < time.Second {
		return 0, fmt.Errorf("automation watch interval must be at least 1s")
	}
	return duration, nil
}

func parseAutomationWatchCycles(raw string) (int, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return 0, fmt.Errorf("automation watch cycles is empty")
	}
	cycles, err := strconv.Atoi(text)
	if err != nil || cycles < 0 {
		return 0, fmt.Errorf("automation watch cycles must be a non-negative integer")
	}
	return cycles, nil
}

func (rt *runtimeState) watchAutomations(options automationWatchOptions) error {
	if options.Interval <= 0 {
		options.Interval = 5 * time.Minute
	}
	cycleLabel := "forever"
	if options.Cycles > 0 {
		cycleLabel = fmt.Sprintf("%d", options.Cycles)
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine(fmt.Sprintf("Automation watch started: interval=%s cycles=%s notify=%t", options.Interval, cycleLabel, options.Notify)))
	completed := 0
	failures := []string{}
	for {
		completed++
		now := time.Now()
		fmt.Fprintln(rt.writer, rt.ui.infoLine(fmt.Sprintf("Automation watch cycle %d", completed)))
		err := rt.monitorAutomations(now)
		if options.Notify || strings.TrimSpace(options.WebhookURL) != "" {
			notifyOptions := automationNotificationOptions{
				WriteDigest: options.Notify,
				WebhookURL:  options.WebhookURL,
			}
			if notifyErr := rt.dispatchAutomationNotification(time.Now(), notifyOptions); notifyErr != nil && err == nil {
				err = notifyErr
			}
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("cycle %d: %s", completed, err.Error()))
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Automation watch cycle failed: "+err.Error()))
		}
		if options.Cycles > 0 && completed >= options.Cycles {
			break
		}
		time.Sleep(options.Interval)
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Automation watch completed: cycles=%d", completed)))
	if len(failures) > 0 {
		return fmt.Errorf("automation watch failures: %s", strings.Join(failures, "; "))
	}
	return nil
}

type automationDaemonState struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Command   string    `json:"command"`
	LogPath   string    `json:"log_path"`
}

func (rt *runtimeState) startAutomationDaemon(options automationWatchOptions) error {
	root := automationWorkspaceRoot(rt)
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	if state, ok := readAutomationDaemonState(root); ok && backgroundProcessRunning(state.PID) {
		fmt.Fprintf(rt.writer, "Automation daemon already running pid=%d log=%s\n", state.PID, state.LogPath)
		return nil
	}
	outDir := filepath.Join(root, ".kernforge", "automation")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	command := renderAutomationDaemonWatchCommand(options)
	logPath := filepath.Join(outDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	args := []string{"-cwd", root}
	if rt.session != nil && strings.TrimSpace(rt.session.ID) != "" {
		args = append(args, "-resume", rt.session.ID)
	}
	args = append(args, "-command", command)
	cmd := exec.Command(exe, args...)
	cmd.Dir = root
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	state := automationDaemonState{
		PID:       cmd.Process.Pid,
		StartedAt: time.Now(),
		Command:   command,
		LogPath:   logPath,
	}
	if err := writeAutomationDaemonState(root, state); err != nil {
		_ = terminateBackgroundProcess(cmd.Process.Pid)
		return err
	}
	_ = cmd.Process.Release()
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindAutomation,
		Severity: conversationSeverityInfo,
		Summary:  "automation daemon started",
		ArtifactRefs: []string{
			logPath,
			automationDaemonStatePath(root),
		},
		Entities: map[string]string{
			"pid":     fmt.Sprintf("%d", state.PID),
			"command": state.Command,
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintf(rt.writer, "Automation daemon started pid=%d log=%s\n", state.PID, logPath)
	return nil
}

func (rt *runtimeState) printAutomationDaemonStatus() error {
	root := automationWorkspaceRoot(rt)
	state, ok := readAutomationDaemonState(root)
	if !ok {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Automation daemon is not running."))
		return nil
	}
	status := "stale"
	if backgroundProcessRunning(state.PID) {
		status = "running"
	}
	fmt.Fprintf(rt.writer, "Automation daemon %s pid=%d command=%s log=%s\n", status, state.PID, state.Command, state.LogPath)
	return nil
}

func (rt *runtimeState) stopAutomationDaemon() error {
	root := automationWorkspaceRoot(rt)
	state, ok := readAutomationDaemonState(root)
	if !ok {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Automation daemon is not running."))
		return nil
	}
	if backgroundProcessRunning(state.PID) {
		_ = terminateBackgroundProcess(state.PID)
	}
	_ = os.Remove(automationDaemonStatePath(root))
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindAutomation,
		Severity: conversationSeverityInfo,
		Summary:  "automation daemon stopped",
		Entities: map[string]string{
			"pid": fmt.Sprintf("%d", state.PID),
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintf(rt.writer, "Automation daemon stopped pid=%d\n", state.PID)
	return nil
}

func renderAutomationDaemonWatchCommand(options automationWatchOptions) string {
	parts := []string{"/automation", "watch", "--interval", options.Interval.String()}
	if options.Notify {
		parts = append(parts, "--notify")
	}
	if strings.TrimSpace(options.WebhookURL) != "" {
		parts = append(parts, "--webhook-url", strings.TrimSpace(options.WebhookURL))
	}
	return strings.Join(parts, " ")
}

func automationWorkspaceRoot(rt *runtimeState) string {
	if rt == nil {
		return ""
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" && rt.session != nil {
		root = sessionBaseWorkingDir(rt.session)
	}
	return strings.TrimSpace(root)
}

func automationDaemonStatePath(root string) string {
	return filepath.Join(root, ".kernforge", "automation", "daemon.json")
}

func readAutomationDaemonState(root string) (automationDaemonState, bool) {
	data, err := os.ReadFile(automationDaemonStatePath(root))
	if err != nil {
		return automationDaemonState{}, false
	}
	state := automationDaemonState{}
	if err := json.Unmarshal(data, &state); err != nil {
		return automationDaemonState{}, false
	}
	return state, state.PID > 0
}

func writeAutomationDaemonState(root string, state automationDaemonState) error {
	path := automationDaemonStatePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

type automationNotificationOptions struct {
	WriteDigest bool
	WebhookURL  string
}

func (o automationNotificationOptions) ShouldNotify() bool {
	return o.WriteDigest || strings.TrimSpace(o.WebhookURL) != ""
}

func parseAutomationNotificationOptions(fields []string, defaultWriteDigest bool) (automationNotificationOptions, error) {
	options := automationNotificationOptions{
		WriteDigest: defaultWriteDigest,
	}
	for index := 0; index < len(fields); index++ {
		field := strings.TrimSpace(fields[index])
		lower := strings.ToLower(field)
		switch {
		case lower == "--notify" || lower == "--write-digest" || lower == "--digest-file":
			options.WriteDigest = true
		case lower == "--no-file" || lower == "--no-digest-file":
			options.WriteDigest = false
		case lower == "--webhook-url" || lower == "--webhook":
			if index+1 >= len(fields) {
				return options, fmt.Errorf("%s requires a URL", field)
			}
			options.WebhookURL = strings.TrimSpace(fields[index+1])
			index++
		case strings.HasPrefix(lower, "--webhook-url="):
			options.WebhookURL = strings.TrimSpace(field[len("--webhook-url="):])
		case strings.HasPrefix(lower, "--webhook="):
			options.WebhookURL = strings.TrimSpace(field[len("--webhook="):])
		default:
			return options, fmt.Errorf("unknown automation notification option: %s", field)
		}
	}
	return options, nil
}

func (rt *runtimeState) dispatchAutomationNotification(now time.Time, options automationNotificationOptions) error {
	if !options.ShouldNotify() {
		if rt.store != nil {
			return rt.store.Save(rt.session)
		}
		return nil
	}
	failures := []string{}
	if options.WriteDigest {
		if err := rt.writeAutomationDigestArtifact(now); err != nil {
			failures = append(failures, "digest file: "+err.Error())
		}
	}
	if strings.TrimSpace(options.WebhookURL) != "" {
		if err := rt.postAutomationDigestWebhook(now, options.WebhookURL); err != nil {
			failures = append(failures, "webhook: "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("automation notification failures: %s", strings.Join(failures, "; "))
	}
	if rt.store != nil {
		return rt.store.Save(rt.session)
	}
	return nil
}

func (rt *runtimeState) writeAutomationDigestArtifact(now time.Time) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = sessionBaseWorkingDir(rt.session)
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	rt.session.normalizeAutomations()
	outDir := filepath.Join(root, ".kernforge", "automation")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(outDir, "latest_digest.md")
	body := renderAutomationDigestMarkdown(rt.session.Automations, now)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	summary := summarizeAutomations(rt.session.Automations, now)
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:         conversationEventKindAutomation,
		Severity:     automationDigestSeverity(summary),
		Summary:      "automation digest artifact generated",
		ArtifactRefs: []string{path},
		Entities: map[string]string{
			"total":     fmt.Sprintf("%d", summary.Total),
			"due":       fmt.Sprintf("%d", summary.Due),
			"failed":    fmt.Sprintf("%d", summary.Failed),
			"scheduled": fmt.Sprintf("%d", summary.Scheduled),
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated automation digest artifact: "+path))
	return nil
}

type automationWebhookPayload struct {
	GeneratedAt string `json:"generated_at"`
	SessionID   string `json:"session_id,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	Summary     string `json:"summary"`
	Markdown    string `json:"markdown"`
}

func (rt *runtimeState) postAutomationDigestWebhook(now time.Time, rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("webhook URL is empty")
	}
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	rt.session.normalizeAutomations()
	summary := summarizeAutomations(rt.session.Automations, now)
	payload := automationWebhookPayload{
		GeneratedAt: now.Format(time.RFC3339),
		SessionID:   strings.TrimSpace(rt.session.ID),
		Workspace:   workspaceSnapshotRoot(rt.workspace),
		Summary:     automationSummaryLine(summary),
		Markdown:    renderAutomationDigestMarkdown(rt.session.Automations, now),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kernforge-automation")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindAutomation,
		Severity: automationDigestSeverity(summary),
		Summary:  "automation digest webhook sent",
		Entities: map[string]string{
			"status":    resp.Status,
			"webhook":   redactAutomationWebhookURL(rawURL),
			"total":     fmt.Sprintf("%d", summary.Total),
			"due":       fmt.Sprintf("%d", summary.Due),
			"failed":    fmt.Sprintf("%d", summary.Failed),
			"scheduled": fmt.Sprintf("%d", summary.Scheduled),
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Sent automation digest webhook: "+redactAutomationWebhookURL(rawURL)))
	return nil
}

func redactAutomationWebhookURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "(invalid)"
	}
	parsed.User = nil
	if parsed.RawQuery != "" {
		parsed.RawQuery = "redacted=1"
	}
	parsed.Fragment = ""
	return parsed.String()
}

func renderAutomationDigestMarkdown(items []SessionAutomation, now time.Time) string {
	summary := summarizeAutomations(items, now)
	var b strings.Builder
	fmt.Fprintf(&b, "# Automation Digest\n\n")
	fmt.Fprintf(&b, "Generated: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "Summary: %s\n", automationSummaryLine(summary))
	writeAutomationDigestMarkdownSection(&b, "Due", items, now, func(item SessionAutomation) bool {
		return automationIsDue(item, now)
	})
	writeAutomationDigestMarkdownSection(&b, "Failed", items, now, func(item SessionAutomation) bool {
		return strings.EqualFold(item.Status, AutomationStatusFailed)
	})
	writeAutomationDigestMarkdownSection(&b, "Paused", items, now, func(item SessionAutomation) bool {
		return strings.EqualFold(item.Status, AutomationStatusPaused)
	})
	writeAutomationDigestMarkdownSection(&b, "All Automations", items, now, func(item SessionAutomation) bool {
		return true
	})
	return strings.TrimSpace(b.String()) + "\n"
}

func writeAutomationDigestMarkdownSection(b *strings.Builder, title string, items []SessionAutomation, now time.Time, include func(SessionAutomation) bool) {
	fmt.Fprintf(b, "\n## %s\n\n", title)
	count := 0
	for _, item := range items {
		item = normalizeSessionAutomation(item)
		if !include(item) {
			continue
		}
		count++
		line := fmt.Sprintf("- %s [%s] type=%s schedule=%s command=%s", item.ID, item.Status, item.Type, valueOrDefault(item.Schedule, "manual-recurring"), item.Command)
		if automationIsDue(item, now) {
			line += " due=yes"
		}
		if !item.NextRunAt.IsZero() {
			line += " next=" + item.NextRunAt.Format(time.RFC3339)
		}
		if strings.TrimSpace(item.LastResult) != "" {
			line += " result=" + compactPromptSection(item.LastResult, 180)
		}
		fmt.Fprintln(b, line)
	}
	if count == 0 {
		fmt.Fprintln(b, "- none")
	}
}

func automationDigestSeverity(summary automationRuntimeSummary) string {
	if summary.Failed > 0 || summary.Due > 0 {
		return conversationSeverityWarn
	}
	return conversationSeverityInfo
}

func (rt *runtimeState) executeSafeSuggestionCommand(command string) (string, error) {
	cmd, ok := ParseCommand(command)
	if !ok {
		return "", fmt.Errorf("suggestion command must be a slash command: %s", command)
	}
	switch cmd.Name {
	case "verify":
		if err := rt.handleVerifyCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /verify", nil
	case "verify-dashboard":
		if err := rt.handleVerifyDashboardCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /verify-dashboard", nil
	case "verify-dashboard-html":
		if err := rt.handleVerifyDashboardHTMLCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /verify-dashboard-html", nil
	case "suggest-dashboard-html":
		if err := rt.handleSuggestDashboardHTMLCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /suggest-dashboard-html", nil
	case "session-dashboard-html":
		if err := rt.handleSessionDashboardHTMLCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /session-dashboard-html", nil
	case "continuity":
		if err := rt.handleContinuityCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /continuity", nil
	case "recover":
		if err := rt.handleRecoverCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /recover", nil
	case "completion-audit":
		if err := rt.handleCompletionAuditCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /completion-audit", nil
	case "evidence-dashboard":
		if err := rt.handleEvidenceDashboard(cmd.Args, false); err != nil {
			return "", err
		}
		return "executed /evidence-dashboard", nil
	case "evidence-dashboard-html":
		if err := rt.handleEvidenceDashboard(cmd.Args, true); err != nil {
			return "", err
		}
		return "executed /evidence-dashboard-html", nil
	case "docs-refresh":
		if err := rt.handleDocsRefreshCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /docs-refresh", nil
	case "analyze-dashboard":
		if err := rt.handleAnalyzeDashboardCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /analyze-dashboard", nil
	case "review-pr":
		options := parsePRReviewAutomationOptions(cmd.Args)
		if options.HasGitHubWrite() {
			return "", fmt.Errorf("/review-pr GitHub write-side flags are not allowed from automatic suggestion execution")
		}
		if err := rt.handlePRReviewAutomationCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /review-pr", nil
	case "automation":
		automationFields := strings.Fields(strings.TrimSpace(cmd.Args))
		if len(automationFields) == 0 || !strings.EqualFold(automationFields[0], "add") {
			return "", fmt.Errorf("only /automation add is allowed from suggestion execution")
		}
		if err := rt.handleAutomationCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /automation add", nil
	default:
		return "", fmt.Errorf("command is not allowed for automatic execution: /%s", cmd.Name)
	}
}

func (rt *runtimeState) handlePRReviewAutomationCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	options := parsePRReviewAutomationOptions(args)
	branch := runGitText(root, "rev-parse", "--abbrev-ref", "HEAD")
	status := runGitText(root, "status", "--short")
	diffStat := runGitText(root, "diff", "--stat")
	nameOnly := runGitText(root, "diff", "--name-only")
	github := PRReviewGitHubContext{}
	if options.GitHub {
		github = collectPRReviewGitHubContext(root)
	}
	outDir := filepath.Join(root, ".kernforge", "pr_review")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(outDir, "latest.md")
	body := renderPRReviewAutomationReport(branch, status, diffStat, nameOnly, github)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	artifactRefs := []string{path}
	commentDraftStatus := "not_requested"
	commentPostStatus := "not_requested"
	commentPostResult := ""
	threadResolveStatus := "not_requested"
	threadResolveResult := ""
	issueDraftStatus := "not_requested"
	issueCreateStatus := "not_requested"
	issueCreateResult := ""
	commentPath := ""
	issuePath := ""
	if options.DraftComments || options.PostComments {
		commentPath = filepath.Join(outDir, "comments.md")
		if err := os.WriteFile(commentPath, []byte(renderPRReviewCommentDraft(branch, status+"\n"+nameOnly, diffStat, github)), 0o644); err != nil {
			return err
		}
		artifactRefs = append(artifactRefs, commentPath)
		commentDraftStatus = "generated"
	}
	if options.DraftIssue || options.CreateIssue {
		issuePath = filepath.Join(outDir, "issue.md")
		if err := os.WriteFile(issuePath, []byte(renderPRReviewIssueDraft(branch, status+"\n"+nameOnly, diffStat, github, options)), 0o644); err != nil {
			return err
		}
		artifactRefs = append(artifactRefs, issuePath)
		issueDraftStatus = "generated"
	}
	writeFailures := []string{}
	if options.PostComments {
		commentPostStatus = "failed"
		result, err := postPRReviewComments(root, commentPath)
		commentPostResult = result
		if err == nil {
			commentPostStatus = "posted"
		} else {
			writeFailures = append(writeFailures, "post comments: "+valueOrDefault(commentPostResult, err.Error()))
		}
	}
	if len(options.ResolveThreads) > 0 {
		threadResolveStatus = "resolved"
		results := []string{}
		for _, threadID := range options.ResolveThreads {
			result, err := resolvePRReviewThread(root, threadID)
			results = append(results, threadID+": "+result)
			if err != nil {
				threadResolveStatus = "failed"
				writeFailures = append(writeFailures, "resolve thread "+threadID+": "+valueOrDefault(result, err.Error()))
			}
		}
		threadResolveResult = compactPromptSection(strings.Join(results, "; "), 500)
	}
	if options.CreateIssue {
		issueCreateStatus = "failed"
		title := prReviewIssueTitle(branch, github)
		result, err := createPRReviewIssue(root, title, issuePath, options)
		issueCreateResult = result
		if err == nil {
			issueCreateStatus = "created"
		} else {
			writeFailures = append(writeFailures, "create issue: "+valueOrDefault(result, err.Error()))
		}
	}
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:         conversationEventKindHandoff,
		Severity:     prReviewWriteSeverity(commentPostStatus, threadResolveStatus, issueCreateStatus),
		Summary:      "PR review automation report generated",
		ArtifactRefs: artifactRefs,
		Entities: map[string]string{
			"automation":       "pr_review",
			"branch":           strings.TrimSpace(branch),
			"github":           github.Status,
			"comment_draft":    commentDraftStatus,
			"comment_post":     commentPostStatus,
			"post_result":      commentPostResult,
			"thread_resolve":   threadResolveStatus,
			"thread_result":    threadResolveResult,
			"resolved_threads": strings.Join(options.ResolveThreads, ","),
			"issue_draft":      issueDraftStatus,
			"issue_create":     issueCreateStatus,
			"issue_result":     issueCreateResult,
			"issue_labels":     strings.Join(options.IssueLabels, ","),
			"issue_assignees":  strings.Join(options.IssueAssignees, ","),
			"issue_milestone":  options.IssueMilestone,
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated PR review automation report: "+path))
	if strings.EqualFold(commentPostStatus, "posted") {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Posted PR review comments with gh pr review."))
	}
	if strings.EqualFold(threadResolveStatus, "resolved") {
		fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Resolved GitHub review threads: %d", len(options.ResolveThreads))))
	}
	if strings.EqualFold(issueCreateStatus, "created") {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Created GitHub follow-up issue."))
	}
	if len(writeFailures) > 0 {
		return fmt.Errorf("PR review GitHub write failures: %s", strings.Join(writeFailures, "; "))
	}
	return nil
}

type PRReviewAutomationOptions struct {
	GitHub         bool
	DraftComments  bool
	PostComments   bool
	DraftIssue     bool
	CreateIssue    bool
	ResolveThreads []string
	IssueLabels    []string
	IssueAssignees []string
	IssueMilestone string
}

func parsePRReviewAutomationOptions(args string) PRReviewAutomationOptions {
	options := PRReviewAutomationOptions{}
	fields := splitAnalysisCommandLine(strings.TrimSpace(args))
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "--github", "--gh":
			options.GitHub = true
		case "--draft-comments", "--comments":
			options.DraftComments = true
		case "--post-comments", "--publish-comments":
			options.GitHub = true
			options.DraftComments = true
			options.PostComments = true
		case "--draft-issue", "--issue":
			options.DraftIssue = true
		case "--create-issue", "--post-issue":
			options.GitHub = true
			options.DraftIssue = true
			options.CreateIssue = true
		case "--no-github", "--local":
			options.GitHub = false
		case "--resolve-thread":
			if index+1 < len(fields) {
				options.ResolveThreads = appendPRReviewThreadIDs(options.ResolveThreads, fields[index+1])
				index++
			}
		case "--label", "--labels":
			if index+1 < len(fields) {
				options.IssueLabels = appendPRReviewIssueValues(options.IssueLabels, fields[index+1])
				index++
			}
		case "--assignee", "--assignees", "--assign":
			if index+1 < len(fields) {
				options.IssueAssignees = appendPRReviewIssueValues(options.IssueAssignees, fields[index+1])
				index++
			}
		case "--milestone":
			if index+1 < len(fields) {
				options.IssueMilestone = strings.TrimSpace(fields[index+1])
				index++
			}
		default:
			lower := strings.ToLower(strings.TrimSpace(field))
			if strings.HasPrefix(lower, "--resolve-thread=") {
				options.ResolveThreads = appendPRReviewThreadIDs(options.ResolveThreads, strings.TrimSpace(field[len("--resolve-thread="):]))
			} else if strings.HasPrefix(lower, "--label=") {
				options.IssueLabels = appendPRReviewIssueValues(options.IssueLabels, strings.TrimSpace(field[len("--label="):]))
			} else if strings.HasPrefix(lower, "--labels=") {
				options.IssueLabels = appendPRReviewIssueValues(options.IssueLabels, strings.TrimSpace(field[len("--labels="):]))
			} else if strings.HasPrefix(lower, "--assignee=") {
				options.IssueAssignees = appendPRReviewIssueValues(options.IssueAssignees, strings.TrimSpace(field[len("--assignee="):]))
			} else if strings.HasPrefix(lower, "--assignees=") {
				options.IssueAssignees = appendPRReviewIssueValues(options.IssueAssignees, strings.TrimSpace(field[len("--assignees="):]))
			} else if strings.HasPrefix(lower, "--milestone=") {
				options.IssueMilestone = strings.TrimSpace(field[len("--milestone="):])
			}
		}
	}
	if options.PostComments || options.CreateIssue || len(options.ResolveThreads) > 0 {
		options.GitHub = true
	}
	if options.PostComments {
		options.DraftComments = true
	}
	if options.CreateIssue {
		options.DraftIssue = true
	}
	return options
}

func (o PRReviewAutomationOptions) HasGitHubWrite() bool {
	return o.PostComments || o.CreateIssue || len(o.ResolveThreads) > 0
}

func appendPRReviewIssueValues(items []string, raw string) []string {
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value != "" {
			items = append(items, value)
		}
	}
	return analysisUniqueStrings(items)
}

func appendPRReviewThreadIDs(items []string, raw string) []string {
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id != "" {
			items = append(items, id)
		}
	}
	return analysisUniqueStrings(items)
}

type PRReviewGitHubContext struct {
	Requested        bool
	Available        bool
	Status           string
	Error            string
	URL              string
	Title            string
	State            string
	Author           string
	BaseRef          string
	HeadRef          string
	ReviewDecision   string
	MergeStateStatus string
	IsDraft          bool
	CommentCount     int
	ReviewSummary    string
	CheckSummary     string
}

type ghPRViewPayload struct {
	URL              string `json:"url"`
	Title            string `json:"title"`
	State            string `json:"state"`
	BaseRefName      string `json:"baseRefName"`
	HeadRefName      string `json:"headRefName"`
	ReviewDecision   string `json:"reviewDecision"`
	MergeStateStatus string `json:"mergeStateStatus"`
	IsDraft          bool   `json:"isDraft"`
	Author           struct {
		Login string `json:"login"`
	} `json:"author"`
	Comments []struct {
		Body string `json:"body"`
	} `json:"comments"`
	Reviews []struct {
		State  string `json:"state"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	} `json:"reviews"`
	StatusCheckRollup []map[string]any `json:"statusCheckRollup"`
}

func collectPRReviewGitHubContext(root string) PRReviewGitHubContext {
	ctx := PRReviewGitHubContext{
		Requested: true,
		Status:    "unavailable",
	}
	out, err := runPRReviewGitHubCommand(root, "pr", "view", "--json", "url,title,state,author,baseRefName,headRefName,isDraft,reviewDecision,mergeStateStatus,comments,reviews,statusCheckRollup")
	if err != nil {
		ctx.Error = compactPromptSection(strings.TrimSpace(strings.TrimSpace(out)+"\n"+err.Error()), 500)
		return ctx
	}
	payload := ghPRViewPayload{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		ctx.Error = compactPromptSection("failed to parse gh pr view JSON: "+err.Error(), 500)
		return ctx
	}
	ctx.Available = true
	ctx.Status = "connected"
	ctx.URL = strings.TrimSpace(payload.URL)
	ctx.Title = strings.TrimSpace(payload.Title)
	ctx.State = strings.TrimSpace(payload.State)
	ctx.Author = strings.TrimSpace(payload.Author.Login)
	ctx.BaseRef = strings.TrimSpace(payload.BaseRefName)
	ctx.HeadRef = strings.TrimSpace(payload.HeadRefName)
	ctx.ReviewDecision = strings.TrimSpace(payload.ReviewDecision)
	ctx.MergeStateStatus = strings.TrimSpace(payload.MergeStateStatus)
	ctx.IsDraft = payload.IsDraft
	ctx.CommentCount = len(payload.Comments)
	ctx.ReviewSummary = summarizePRReviews(payload.Reviews)
	ctx.CheckSummary = summarizePRChecks(payload.StatusCheckRollup)
	return ctx
}

func postPRReviewComments(root string, commentPath string) (string, error) {
	commentPath = strings.TrimSpace(commentPath)
	if commentPath == "" {
		return "", fmt.Errorf("comment draft path is empty")
	}
	out, err := runPRReviewGitHubCommand(root, "pr", "review", "--comment", "--body-file", commentPath)
	result := compactPromptSection(strings.TrimSpace(strings.TrimSpace(out)+"\n"+errorString(err)), 500)
	if err != nil {
		return result, err
	}
	return valueOrDefault(result, "gh pr review completed"), nil
}

func resolvePRReviewThread(root string, threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", fmt.Errorf("review thread id is empty")
	}
	query := `mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{id isResolved}}}`
	out, err := runPRReviewGitHubCommand(root, "api", "graphql", "-f", "query="+query, "-F", "id="+threadID)
	result := compactPromptSection(strings.TrimSpace(strings.TrimSpace(out)+"\n"+errorString(err)), 500)
	if err != nil {
		return result, err
	}
	return valueOrDefault(result, "thread resolved"), nil
}

func createPRReviewIssue(root string, title string, issuePath string, options PRReviewAutomationOptions) (string, error) {
	issuePath = strings.TrimSpace(issuePath)
	if issuePath == "" {
		return "", fmt.Errorf("issue draft path is empty")
	}
	args := []string{"issue", "create", "--title", title, "--body-file", issuePath}
	for _, label := range options.IssueLabels {
		args = append(args, "--label", label)
	}
	for _, assignee := range options.IssueAssignees {
		args = append(args, "--assignee", assignee)
	}
	if strings.TrimSpace(options.IssueMilestone) != "" {
		args = append(args, "--milestone", strings.TrimSpace(options.IssueMilestone))
	}
	out, err := runPRReviewGitHubCommand(root, args...)
	result := compactPromptSection(strings.TrimSpace(strings.TrimSpace(out)+"\n"+errorString(err)), 500)
	if err != nil {
		return result, err
	}
	return valueOrDefault(result, "gh issue create completed"), nil
}

func prReviewWriteSeverity(statuses ...string) string {
	for _, status := range statuses {
		if strings.EqualFold(strings.TrimSpace(status), "failed") {
			return conversationSeverityWarn
		}
	}
	return conversationSeverityInfo
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func summarizePRReviews(reviews []struct {
	State  string `json:"state"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
}) string {
	if len(reviews) == 0 {
		return "none"
	}
	counts := map[string]int{}
	for _, review := range reviews {
		state := strings.ToLower(strings.TrimSpace(review.State))
		if state == "" {
			state = "unknown"
		}
		counts[state]++
	}
	return renderSortedCountSummary(counts)
}

func summarizePRChecks(items []map[string]any) string {
	if len(items) == 0 {
		return "none"
	}
	counts := map[string]int{}
	for _, item := range items {
		state := strings.ToLower(firstNonBlankString(
			stringMapValue(item, "conclusion"),
			stringMapValue(item, "status"),
			stringMapValue(item, "state"),
		))
		if state == "" {
			state = "unknown"
		}
		counts[state]++
	}
	return renderSortedCountSummary(counts)
}

func stringMapValue(item map[string]any, key string) string {
	value, ok := item[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func renderSortedCountSummary(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, " ")
}

func runGitText(root string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	data, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(data) + "\n" + err.Error())
	}
	return strings.TrimSpace(string(data))
}

func renderPRReviewAutomationReport(branch string, status string, diffStat string, nameOnly string, github PRReviewGitHubContext) string {
	now := time.Now().Format(time.RFC3339)
	if strings.TrimSpace(status) == "" {
		status = "clean"
	}
	if strings.TrimSpace(diffStat) == "" {
		diffStat = "no unstaged diff"
	}
	if strings.TrimSpace(nameOnly) == "" {
		nameOnly = "no changed files"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `# PR Review Automation

Generated: %s
Branch: %s

## Status

%s

## Diff Stat

%s

## Changed Files

%s

`, now, valueOrDefault(strings.TrimSpace(branch), "unknown"), status, diffStat, nameOnly)
	if github.Requested {
		b.WriteString(renderPRReviewGitHubSection(github))
		b.WriteString("\n\n")
	}
	b.WriteString(`## Review Checklist

- Correctness: inspect modified control flow and error paths.
- Security: check trust boundaries, input validation, and risky Windows/security surfaces.
- Stability: confirm verification coverage is current.
- Maintainability: ensure docs and handoff artifacts match the implementation.
`)
	return strings.TrimSpace(b.String())
}

func renderPRReviewGitHubSection(github PRReviewGitHubContext) string {
	if !github.Available {
		return strings.TrimSpace(fmt.Sprintf(`## GitHub PR

Status: %s
Reason: %s`, valueOrDefault(github.Status, "unavailable"), valueOrDefault(github.Error, "gh pr view did not return PR metadata")))
	}
	return strings.TrimSpace(fmt.Sprintf(`## GitHub PR

Status: %s
URL: %s
Title: %s
State: %s
Draft: %t
Author: %s
Base: %s
Head: %s
Review Decision: %s
Merge State: %s
Reviews: %s
Comments: %d
Checks: %s`, valueOrDefault(github.Status, "connected"), valueOrUnset(github.URL), valueOrUnset(github.Title), valueOrUnset(github.State), github.IsDraft, valueOrUnset(github.Author), valueOrUnset(github.BaseRef), valueOrUnset(github.HeadRef), valueOrUnset(github.ReviewDecision), valueOrUnset(github.MergeStateStatus), valueOrDefault(github.ReviewSummary, "none"), github.CommentCount, valueOrDefault(github.CheckSummary, "none")))
}

func renderPRReviewCommentDraft(branch string, nameOnly string, diffStat string, github PRReviewGitHubContext) string {
	now := time.Now().Format(time.RFC3339)
	paths := prReviewChangedFiles(nameOnly)
	var b strings.Builder
	fmt.Fprintf(&b, "# PR Review Comment Draft\n\nGenerated: %s\nBranch: %s\n", now, valueOrDefault(strings.TrimSpace(branch), "unknown"))
	if github.Available && strings.TrimSpace(github.URL) != "" {
		fmt.Fprintf(&b, "PR: %s\n", github.URL)
	}
	b.WriteString("\n## Review Summary Draft\n\n")
	b.WriteString("Focus this review on correctness, security-sensitive edge cases, verification coverage, and user-visible behavior changes.\n")
	if strings.TrimSpace(diffStat) != "" {
		b.WriteString("\nDiff stat:\n\n```text\n")
		b.WriteString(strings.TrimSpace(diffStat))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n## File Comment Drafts\n\n")
	if len(paths) == 0 {
		b.WriteString("- No changed files were detected in the local diff.\n")
	} else {
		for _, path := range limitStrings(paths, 24) {
			fmt.Fprintf(&b, "- `%s`: %s\n", path, prReviewDraftFocusForPath(path))
		}
		if len(paths) > 24 {
			fmt.Fprintf(&b, "- Additional files omitted: %d\n", len(paths)-24)
		}
	}
	b.WriteString("\n## Before Posting\n\n")
	b.WriteString("- Re-open the exact diff hunk before posting any comment.\n")
	b.WriteString("- Convert file-level notes into line comments only when the line anchor is verified.\n")
	b.WriteString("- Do not post generic comments that do not identify a concrete risk or missing verification.\n")
	return strings.TrimSpace(b.String())
}

func renderPRReviewIssueDraft(branch string, nameOnly string, diffStat string, github PRReviewGitHubContext, options PRReviewAutomationOptions) string {
	paths := prReviewChangedFiles(nameOnly)
	var b strings.Builder
	fmt.Fprintf(&b, "# PR Review Follow-up\n\n")
	fmt.Fprintf(&b, "Branch: %s\n", valueOrDefault(strings.TrimSpace(branch), "unknown"))
	if github.Available && strings.TrimSpace(github.URL) != "" {
		fmt.Fprintf(&b, "PR: %s\n", github.URL)
	}
	if github.Available && strings.TrimSpace(github.ReviewDecision) != "" {
		fmt.Fprintf(&b, "Review decision: %s\n", github.ReviewDecision)
	}
	if len(options.IssueLabels) > 0 {
		fmt.Fprintf(&b, "Labels: %s\n", strings.Join(options.IssueLabels, ", "))
	}
	if len(options.IssueAssignees) > 0 {
		fmt.Fprintf(&b, "Assignees: %s\n", strings.Join(options.IssueAssignees, ", "))
	}
	if strings.TrimSpace(options.IssueMilestone) != "" {
		fmt.Fprintf(&b, "Milestone: %s\n", strings.TrimSpace(options.IssueMilestone))
	}
	b.WriteString("\n## Summary\n\n")
	b.WriteString("Track concrete follow-up from the PR review. Replace this summary with the exact risk, owner, and verification requirement before assigning.\n")
	if strings.TrimSpace(diffStat) != "" {
		b.WriteString("\n## Diff Stat\n\n```text\n")
		b.WriteString(strings.TrimSpace(diffStat))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n## Candidate Files\n\n")
	if len(paths) == 0 {
		b.WriteString("- No changed files were detected in the local diff.\n")
	} else {
		for _, path := range limitStrings(paths, 24) {
			fmt.Fprintf(&b, "- `%s`: %s\n", path, prReviewDraftFocusForPath(path))
		}
	}
	b.WriteString("\n## Verification\n\n")
	b.WriteString("- [ ] Re-open the exact diff before assigning this issue.\n")
	b.WriteString("- [ ] Add the narrow verification command and expected signal.\n")
	b.WriteString("- [ ] Link any evidence, dashboard, or failing check artifact.\n")
	return strings.TrimSpace(b.String())
}

func prReviewIssueTitle(branch string, github PRReviewGitHubContext) string {
	if github.Available && strings.TrimSpace(github.Title) != "" {
		return "PR review follow-up: " + compactPromptSection(github.Title, 80)
	}
	return "PR review follow-up: " + compactPromptSection(valueOrDefault(strings.TrimSpace(branch), "unknown branch"), 80)
}

func prReviewChangedFiles(nameOnly string) []string {
	out := []string{}
	for _, rawLine := range strings.Split(nameOnly, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.EqualFold(line, "no changed files") {
			continue
		}
		if gitStatusOutputNoiseLine(line) {
			continue
		}
		if parsed, ok := parseGitStatusShortChangedPath(rawLine); ok {
			line = parsed
		}
		out = append(out, line)
	}
	return analysisUniqueStrings(out)
}

func gitStatusOutputNoiseLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return strings.HasPrefix(lower, "warning: in the working copy") ||
		strings.HasPrefix(lower, "warning: lf will be replaced") ||
		strings.HasPrefix(lower, "warning: crlf will be replaced")
}

func parseGitStatusShortChangedPath(line string) (string, bool) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) >= 3 && line[1] == ' ' && isGitStatusShortCode(line[0]) {
		path := strings.TrimSpace(line[2:])
		if path != "" {
			return normalizeGitStatusShortPath(path), true
		}
	}
	if len(line) < 4 || line[2] != ' ' {
		return "", false
	}
	x := line[0]
	y := line[1]
	if !isGitStatusShortCode(x) || !isGitStatusShortCode(y) {
		return "", false
	}
	path := strings.TrimSpace(line[3:])
	if path == "" {
		return "", false
	}
	return normalizeGitStatusShortPath(path), true
}

func normalizeGitStatusShortPath(path string) string {
	path = strings.TrimSpace(path)
	if strings.Contains(path, " -> ") {
		parts := strings.Split(path, " -> ")
		path = strings.TrimSpace(parts[len(parts)-1])
	}
	return path
}

func isGitStatusShortCode(ch byte) bool {
	return strings.ContainsRune(" MADRCU?!", rune(ch))
}

func prReviewDraftFocusForPath(path string) string {
	lower := strings.ToLower(strings.TrimSpace(path))
	switch {
	case strings.HasSuffix(lower, ".go"):
		return "check error handling, context cancellation, persistence side effects, and targeted go test coverage."
	case strings.HasSuffix(lower, ".cpp") || strings.HasSuffix(lower, ".cc") || strings.HasSuffix(lower, ".c") || strings.HasSuffix(lower, ".h") || strings.HasSuffix(lower, ".hpp"):
		return "check lifetime, bounds, ownership, Windows API failure paths, and build or sanitizer coverage."
	case strings.HasSuffix(lower, ".ps1") || strings.HasSuffix(lower, ".bat") || strings.HasSuffix(lower, ".cmd"):
		return "check quoting, path handling, destructive operations, privilege assumptions, and dry-run behavior."
	case strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".toml"):
		return "check schema compatibility, default behavior, secrets exposure, and migration or fallback handling."
	case strings.HasSuffix(lower, ".md"):
		return "check that documented commands, paths, and caveats match the implemented behavior."
	default:
		return "check correctness impact, security-sensitive assumptions, and whether verification evidence covers this file."
	}
}

func (rt *runtimeState) promoteSuggestionPreferenceToMemory(record SuggestionRecord, status string) error {
	if rt == nil || rt.longMem == nil || rt.session == nil {
		return nil
	}
	suggestion := record.Suggestion
	status = strings.TrimSpace(status)
	if status == "" {
		status = record.Status
	}
	summary := fmt.Sprintf("Suggestion %s: %s", status, suggestion.Title)
	if strings.TrimSpace(suggestion.Command) != "" {
		summary += " command=" + suggestion.Command
	}
	id := "suggestion-" + status + "-" + shortStableID(suggestion.DedupKey+"-"+status)
	if _, ok, err := rt.longMem.Get(id); err != nil {
		return err
	} else if ok {
		return nil
	}
	return rt.longMem.Append(PersistentMemoryRecord{
		ID:                     id,
		SessionID:              rt.session.ID,
		SessionName:            rt.session.Name,
		Provider:               rt.session.Provider,
		Model:                  rt.session.Model,
		Workspace:              workspaceSnapshotRoot(rt.workspace),
		CreatedAt:              time.Now(),
		Request:                "/suggest " + status + " " + suggestion.ID,
		Reply:                  summary,
		Summary:                summary,
		Importance:             PersistentMemoryMedium,
		Trust:                  PersistentMemoryConfirmed,
		VerificationCategories: []string{"suggestion-preference"},
		VerificationTags:       []string{status, suggestion.Type},
		Files:                  append([]string(nil), suggestion.EvidenceRefs...),
		Keywords:               []string{"suggestion", status, suggestion.Type, suggestion.Command},
	})
}

func (rt *runtimeState) syncSuggestionToTaskGraph(record SuggestionRecord) {
	if rt == nil || rt.session == nil {
		return
	}
	suggestion := record.Suggestion
	if strings.TrimSpace(suggestion.DedupKey) == "" {
		return
	}
	status := "ready"
	switch strings.TrimSpace(record.Status) {
	case SuggestionStatusAccepted:
		status = "in_progress"
	case SuggestionStatusDismissed:
		status = "canceled"
	case SuggestionStatusExecuted:
		status = "completed"
	}
	graph := rt.session.EnsureTaskGraph()
	if graph == nil {
		return
	}
	node := TaskNode{
		ID:                 "suggest:" + shortStableID(suggestion.DedupKey),
		Title:              "Suggestion: " + suggestion.Title,
		Kind:               suggestionTaskKind(suggestion),
		Status:             status,
		ReadOnlyWorkerTool: suggestion.Command,
		LifecycleNote:      compactPromptSection(suggestion.Reason, 220),
		LastUpdated:        time.Now(),
	}
	graph.UpsertNode(node)
}

func (rt *runtimeState) syncSuggestionCandidatesToTaskGraph(items []Suggestion, mem *SuggestionMemory) {
	for _, item := range items {
		rt.syncSuggestionToTaskGraph(SuggestionRecord{
			Suggestion: item,
			Status:     suggestionMemoryStatus(item, mem),
		})
	}
}

func suggestionTaskKind(item Suggestion) string {
	switch strings.TrimSpace(strings.ToLower(item.Type)) {
	case "run_verification", "inspect_failure", AutomationTypeRecurringVerification:
		return "verification"
	case "refresh_analysis", "retry_or_switch_model":
		return "inspection"
	case "evidence_capture", "fuzz_next_step", "checkpoint_or_worktree", "continue_workflow", "cleanup_or_close_feature":
		return "task"
	default:
		return inferTaskNodeKind(item.Title + " " + item.Command)
	}
}

func sessionHasActiveAutomation(sess *Session, typ string) bool {
	if sess == nil {
		return false
	}
	sess.normalizeAutomations()
	for _, item := range sess.Automations {
		if strings.EqualFold(item.Type, strings.TrimSpace(typ)) && item.Status == AutomationStatusActive {
			return true
		}
	}
	return false
}
