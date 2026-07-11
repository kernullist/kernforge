package main

import (
	"fmt"
	"strings"
)

func (rt *runtimeState) handleDecisionCommand(args string) error {
	if rt == nil {
		return fmt.Errorf("decision dashboard runtime is unavailable")
	}
	if strings.TrimSpace(args) != "" {
		return fmt.Errorf("usage: /decision (view, edit, profile, and export actions are available inside the dashboard)")
	}
	if !rt.interactive {
		return fmt.Errorf("/decision requires an interactive KernForge session so the dashboard server remains available")
	}
	workspace := firstNonBlankString(rt.workspace.BaseRoot, rt.workspace.Root)
	if rt.decisionDashboard == nil {
		rt.decisionDashboard = NewDecisionDashboardServer(rt.decisionStore, rt.decisionProfileStore, workspace)
	} else {
		rt.decisionDashboard.SetWorkspace(workspace)
	}
	launchURL, err := rt.decisionDashboard.LaunchURL()
	if err != nil {
		return err
	}
	origin := rt.decisionDashboard.Origin()
	if decisionDashboardOpenURL == nil {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Decision dashboard ready: "+origin))
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Browser opener is unavailable. Open this authenticated URL manually: "+launchURL))
		return nil
	}
	if err := decisionDashboardOpenURL(launchURL); err != nil {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Decision dashboard ready: "+origin))
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Could not open the browser automatically: "+err.Error()))
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Open this authenticated URL manually: "+launchURL))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Opened decision dashboard: "+origin))
	return nil
}
