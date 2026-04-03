package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (rt *runtimeState) hookRuleCount() int {
	if rt.hooks == nil || rt.hooks.Engine == nil {
		return 0
	}
	return len(rt.hooks.Engine.Rules)
}

func (rt *runtimeState) handleHooksCommand() {
	fmt.Fprintln(rt.writer, rt.ui.section("Hooks"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("enabled", fmt.Sprintf("%t", configHooksEnabled(rt.cfg) && rt.hooks != nil && rt.hooks.Engine != nil && rt.hooks.Engine.Enabled)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("fail_closed", fmt.Sprintf("%t", configHooksFailClosed(rt.cfg))))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("presets", strings.Join(rt.cfg.HookPresets, ", ")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("rules", fmt.Sprintf("%d", rt.hookRuleCount())))
	if rt.hookOverrides != nil {
		if overrides, err := rt.hookOverrides.List(rt.workspace.BaseRoot); err == nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("active_overrides", fmt.Sprintf("%d", len(overrides))))
		}
	}
	if len(rt.hookWarns) > 0 {
		for _, warn := range rt.hookWarns {
			fmt.Fprintln(rt.writer, rt.ui.warnLine(warn))
		}
	}
	if rt.hooks == nil || rt.hooks.Engine == nil || len(rt.hooks.Engine.Rules) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No hook rules loaded."))
		return
	}
	for _, rule := range rt.hooks.Engine.Rules {
		status := "enabled"
		if rule.Enabled != nil && !*rule.Enabled {
			status = "disabled"
		}
		action := strings.TrimSpace(rule.Action.Type)
		if action == "" {
			action = "allow"
		}
		fmt.Fprintf(rt.writer, "- %s  priority=%d  action=%s  events=%s  %s\n", rule.ID, rule.Priority, action, joinHookEvents(rule.Events), status)
	}
}

func (rt *runtimeState) handleHookOverridesCommand() error {
	if rt.hookOverrides == nil {
		return fmt.Errorf("hook override store is not configured")
	}
	items, err := rt.hookOverrides.List(rt.workspace.BaseRoot)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Hook Overrides"))
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No active hook overrides for this workspace."))
		return nil
	}
	for _, item := range items {
		line := fmt.Sprintf("- %s  rule=%s  expires=%s", rt.ui.dim(item.ID), item.RuleID, item.ExpiresAt.Format(time.RFC3339))
		if strings.TrimSpace(item.Reason) != "" {
			line += "  " + item.Reason
		}
		fmt.Fprintln(rt.writer, line)
	}
	return nil
}

func (rt *runtimeState) handleHookOverrideAddCommand(args string) error {
	if rt.hookOverrides == nil {
		return fmt.Errorf("hook override store is not configured")
	}
	parts := strings.Fields(strings.TrimSpace(args))
	if len(parts) < 3 {
		return fmt.Errorf("usage: /override-add <rule-id> <hours> <reason>")
	}
	hours, err := strconv.Atoi(parts[1])
	if err != nil || hours < 1 {
		return fmt.Errorf("invalid hours: %s", parts[1])
	}
	record, err := rt.hookOverrides.Append(HookOverrideRecord{
		RuleID:    parts[0],
		Workspace: workspaceSnapshotRoot(rt.workspace),
		Reason:    strings.Join(parts[2:], " "),
		ExpiresAt: time.Now().Add(time.Duration(hours) * time.Hour),
	})
	if err != nil {
		return err
	}
	rt.recordHookOverrideEvent("active", []HookOverrideRecord{record})
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Created hook override %s for rule %s until %s", record.ID, record.RuleID, record.ExpiresAt.Format(time.RFC3339))))
	return nil
}

func (rt *runtimeState) handleHookOverrideClearCommand(args string) error {
	if rt.hookOverrides == nil {
		return fmt.Errorf("hook override store is not configured")
	}
	query := strings.TrimSpace(args)
	if query == "" {
		return fmt.Errorf("usage: /override-clear <override-id|rule-id|all>")
	}
	removed, err := rt.hookOverrides.Remove(query, rt.workspace.BaseRoot, strings.EqualFold(query, "all"))
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		return fmt.Errorf("no matching hook overrides found")
	}
	rt.recordHookOverrideEvent("cleared", removed)
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Removed %d hook override(s)", len(removed))))
	return nil
}

func (rt *runtimeState) recordHookOverrideEvent(outcome string, items []HookOverrideRecord) {
	for _, item := range items {
		if rt.evidence != nil {
			_ = rt.evidence.Append(EvidenceRecord{
				SessionID:   rt.session.ID,
				Workspace:   workspaceSnapshotRoot(rt.workspace),
				CreatedAt:   time.Now(),
				Kind:        "hook_override",
				Category:    "policy",
				Subject:     item.RuleID,
				Outcome:     outcome,
				Severity:    "low",
				Confidence:  "high",
				SignalClass: "policy",
				RiskScore:   10,
				SeverityReasons: []string{
					"manual policy override recorded",
				},
				Tags: []string{"override", "policy"},
				Attributes: map[string]string{
					"override_id": item.ID,
					"reason":      item.Reason,
					"expires_at":  item.ExpiresAt.Format(time.RFC3339),
				},
			})
		}
		if rt.longMem != nil {
			_ = rt.longMem.Append(PersistentMemoryRecord{
				SessionID:   rt.session.ID,
				SessionName: rt.session.Name,
				Provider:    rt.session.Provider,
				Model:       rt.session.Model,
				Workspace:   workspaceSnapshotRoot(rt.workspace),
				CreatedAt:   time.Now(),
				Request:     fmt.Sprintf("hook override %s", outcome),
				Reply:       fmt.Sprintf("rule=%s reason=%s", item.RuleID, item.Reason),
				Summary:     fmt.Sprintf("Hook override %s for rule %s until %s. Reason: %s", outcome, item.RuleID, item.ExpiresAt.Format(time.RFC3339), item.Reason),
				Importance:  PersistentMemoryMedium,
				Trust:       PersistentMemoryConfirmed,
				Keywords:    []string{"hook", "override", "policy", strings.ToLower(outcome), strings.ToLower(item.RuleID)},
			})
		}
	}
}
