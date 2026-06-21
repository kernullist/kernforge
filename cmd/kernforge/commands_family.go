package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func commandSubcommandAndRest(args string) (string, string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "", ""
	}
	fields := splitCommandFields(trimmed)
	if len(fields) == 0 {
		return "", ""
	}
	subcommand := normalizeSlashCommandName(fields[0])
	rest := strings.TrimSpace(trimmed)
	if strings.HasPrefix(rest, fields[0]) {
		rest = strings.TrimSpace(rest[len(fields[0]):])
	} else {
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) == 2 {
			rest = strings.TrimSpace(parts[1])
		} else {
			rest = ""
		}
	}
	return subcommand, rest
}

func commandArgsHaveHTMLFlag(args string) (string, bool) {
	fields := splitCommandFields(strings.TrimSpace(args))
	var kept []string
	html := false
	for _, field := range fields {
		if strings.EqualFold(field, "--html") || strings.EqualFold(field, "html") {
			html = true
			continue
		}
		kept = append(kept, field)
	}
	return strings.Join(kept, " "), html
}

func (rt *runtimeState) handleMemoryFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "":
		return rt.handleLoadedMemoryCommand()
	case "loaded", "context":
		return rt.handleLoadedMemoryCommand()
	case "recent":
		return rt.handlePersistentMemoryRecent(rest)
	case "search":
		return rt.handlePersistentMemorySearch(rest)
	case "show":
		return rt.handlePersistentMemoryShow(rest)
	case "promote", "demote", "confirm", "tentative":
		return rt.handlePersistentMemoryAdjust(rest, subcommand)
	case "dashboard":
		query, html := commandArgsHaveHTMLFlag(rest)
		return rt.handlePersistentMemoryDashboard(query, html)
	case "dashboard-html":
		return rt.handlePersistentMemoryDashboard(rest, true)
	case "prune":
		return rt.handlePersistentMemoryPrune(rest)
	case "stats":
		return rt.handlePersistentMemoryStats()
	case "note", "capture", "memo":
		return rt.handleMarkdownMemoryNote(rest)
	case "notes":
		return rt.handleMarkdownMemoryNotesList()
	case "note-show":
		return rt.handleMarkdownMemoryNoteShow(rest)
	default:
		return fmt.Errorf("usage: /memory [loaded|recent|search|show|promote|demote|confirm|tentative|dashboard [--html]|prune|stats|note [title -- body]|notes|note-show <file>]")
	}
}

// markdownMemory returns the workspace-scoped markdown memory store.
func (rt *runtimeState) markdownMemory() *MarkdownMemoryStore {
	return NewMarkdownMemoryStore(rt.workspace.BaseRoot)
}

// handleMarkdownMemoryNote quick-captures a markdown note. The argument may be
// just a body, or "title -- body" to file the note under a titled note file.
func (rt *runtimeState) handleMarkdownMemoryNote(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return fmt.Errorf("usage: /memory note [title --] <text>")
	}
	title := ""
	body := rest
	if idx := strings.Index(rest, "--"); idx >= 0 {
		title = strings.TrimSpace(rest[:idx])
		body = strings.TrimSpace(rest[idx+2:])
	}
	store := rt.markdownMemory()
	if store == nil {
		return fmt.Errorf("markdown memory is unavailable: no workspace base directory")
	}
	path, err := store.AppendNote(title, body)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Saved note to "+path))
	return nil
}

func (rt *runtimeState) handleMarkdownMemoryNotesList() error {
	store := rt.markdownMemory()
	if store == nil {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Markdown memory is unavailable for this workspace."))
		return nil
	}
	notes, err := store.ListNotes()
	if err != nil {
		return err
	}
	if len(notes) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No markdown notes yet. Add one with /memory note <text>."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Markdown Memory"))
	fmt.Fprintln(rt.writer, rt.ui.dim(store.Dir))
	for _, note := range notes {
		fmt.Fprintf(rt.writer, "%s  %s  %dB  %s\n",
			rt.ui.dim(note.File),
			rt.ui.info(note.ModifiedAt.Format(time.RFC3339)),
			note.SizeBytes,
			note.Title,
		)
	}
	return nil
}

func (rt *runtimeState) handleMarkdownMemoryNoteShow(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return fmt.Errorf("usage: /memory note-show <file>")
	}
	store := rt.markdownMemory()
	if store == nil {
		return fmt.Errorf("markdown memory is unavailable for this workspace")
	}
	content, err := store.ReadNote(rest)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Note: "+rest))
	fmt.Fprintln(rt.writer, content)
	return nil
}

func (rt *runtimeState) handleLoadedMemoryCommand() error {
	if len(rt.memory.Files) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No memory files loaded."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory"))
	for _, file := range rt.memory.Files {
		fmt.Fprintln(rt.writer, rt.ui.dim(file.Path))
	}
	return nil
}

func (rt *runtimeState) handleEvidenceFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "", "recent":
		return rt.handleEvidenceRecent(rest)
	case "search":
		return rt.handleEvidenceSearch(rest)
	case "show":
		return rt.handleEvidenceShow(rest)
	case "dashboard":
		query, html := commandArgsHaveHTMLFlag(rest)
		return rt.handleEvidenceDashboard(query, html)
	case "dashboard-html":
		return rt.handleEvidenceDashboard(rest, true)
	default:
		return fmt.Errorf("usage: /evidence [recent|search|show|dashboard [--html]]")
	}
}

func (rt *runtimeState) handleVerifyFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "":
		return rt.handleVerifyCommand(args)
	case "dashboard":
		filtered, html := commandArgsHaveHTMLFlag(rest)
		if html {
			return rt.handleVerifyDashboardHTMLCommand(filtered)
		}
		return rt.handleVerifyDashboardCommand(filtered)
	case "dashboard-html":
		return rt.handleVerifyDashboardHTMLCommand(rest)
	case "tools":
		return rt.handleVerifyToolsCommand(rest)
	default:
		return rt.handleVerifyCommand(args)
	}
}

func (rt *runtimeState) handleVerifyToolsCommand(args string) error {
	action, rest := commandSubcommandAndRest(args)
	if action == "" {
		return fmt.Errorf("usage: /verify tools <detect|set|clear> [tool] [path]")
	}
	switch action {
	case "detect":
		return rt.handleDetectVerificationToolsCommand()
	case "set":
		toolName, pathValue := commandSubcommandAndRest(rest)
		if toolName == "" {
			return fmt.Errorf("usage: /verify tools set <msbuild|cmake|ctest|ninja> [path]")
		}
		return rt.handleSetVerificationToolPathCommand(toolName, pathValue)
	case "clear":
		toolName, _ := commandSubcommandAndRest(rest)
		if toolName == "" {
			return fmt.Errorf("usage: /verify tools clear <msbuild|cmake|ctest|ninja>")
		}
		return rt.handleClearVerificationToolPathCommand(toolName)
	default:
		return fmt.Errorf("usage: /verify tools <detect|set|clear> [tool] [path]")
	}
}

func (rt *runtimeState) handleOverrideFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "", "status", "list":
		return rt.handleHookOverridesCommand()
	case "add":
		return rt.handleHookOverrideAddCommand(rest)
	case "clear":
		return rt.handleHookOverrideClearCommand(rest)
	default:
		return fmt.Errorf("usage: /override [status|add|clear]")
	}
}

func (rt *runtimeState) handleCheckpointFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "auto":
		return rt.handleCheckpointAutoCommand(rest)
	case "diff":
		return rt.handleCheckpointDiffCommand(rest)
	case "list":
		if strings.TrimSpace(rest) != "" {
			return fmt.Errorf("usage: /checkpoint list")
		}
		return rt.handleCheckpointsCommand()
	case "rollback":
		return rt.handleRollbackCommand(rest)
	default:
		return rt.handleCheckpointCommand(args)
	}
}

func (rt *runtimeState) handleSuggestFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	if subcommand == "dashboard" || subcommand == "dashboard-html" {
		rest, _ = commandArgsHaveHTMLFlag(rest)
		return rt.handleSuggestDashboardHTMLCommand(rest)
	}
	return rt.handleSuggestCommand(args)
}

func (rt *runtimeState) handleSessionFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "", "status":
		return rt.handleSessionStatusCommand()
	case "list":
		return rt.handleSessionListCommand(rest)
	case "search", "find":
		return rt.handleSessionListCommand(joinCommandArgs("search", rest))
	case "events":
		return rt.handleEventsCommand(rest)
	case "continuity":
		return rt.handleContinuityCommand(rest)
	case "recover":
		return rt.handleRecoverCommand(rest)
	case "audit", "completion-audit":
		return rt.handleCompletionAuditCommand(rest)
	case "jobs":
		return rt.handleJobsCommand(rest)
	case "handoff":
		return rt.handleDelegationHandoffCommand(rest)
	case "tasks":
		if strings.TrimSpace(rest) != "" {
			return fmt.Errorf("usage: /session tasks")
		}
		return rt.handleTasksCommand()
	case "dashboard", "dashboard-html":
		rest, _ = commandArgsHaveHTMLFlag(rest)
		return rt.handleSessionDashboardHTMLCommand(rest)
	default:
		return fmt.Errorf("usage: /session [status|list|search|events|continuity|recover|audit|jobs|handoff|tasks|dashboard [--html]]")
	}
}

func (rt *runtimeState) handleSessionStatusCommand() error {
	fmt.Fprintln(rt.writer, rt.ui.section("Session"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("session_id", rt.session.ID))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("name", rt.session.Name))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("stored_at", filepath.Join(rt.store.Root(), rt.session.ID+".json")))
	return nil
}

func (rt *runtimeState) handleSessionListCommand(args string) error {
	sessionArgs := strings.TrimSpace(args)
	if sessionArgs != "" {
		query := sessionArgs
		parts := strings.SplitN(sessionArgs, " ", 2)
		if len(parts) > 0 {
			action := normalizeSlashCommandName(parts[0])
			if action == "search" || action == "find" {
				if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
					return fmt.Errorf("usage: /session search <query>")
				}
				query = strings.TrimSpace(parts[1])
			}
		}
		items, err := rt.store.Search(query, 20)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No sessions matched query."))
			return nil
		}
		fmt.Fprintln(rt.writer, rt.ui.section("Session Search"))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("query", query))
		for _, item := range items {
			fmt.Fprintf(rt.writer, "%s  %s  %s\n", rt.ui.dim(item.ID), rt.ui.info(item.UpdatedAt.Format(time.RFC3339)), item.Name)
			if strings.TrimSpace(item.WorkingDir) != "" {
				fmt.Fprintf(rt.writer, "  %s %s\n", rt.ui.dim("cwd:"), rt.ui.dim(item.WorkingDir))
			}
			if strings.TrimSpace(item.Snippet) != "" {
				fmt.Fprintf(rt.writer, "  %s %s\n", rt.ui.dim(item.MatchField+":"), item.Snippet)
			}
		}
		return nil
	}
	items, err := rt.store.List()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No saved sessions."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Sessions"))
	for _, item := range items {
		fmt.Fprintf(rt.writer, "%s  %s  %s\n", rt.ui.dim(item.ID), rt.ui.info(item.UpdatedAt.Format(time.RFC3339)), item.Name)
	}
	return nil
}

func (rt *runtimeState) handleTasksCommand() error {
	if len(rt.session.Plan) == 0 {
		if rt.session.TaskGraph != nil && len(rt.session.TaskGraph.Nodes) > 0 {
			fmt.Fprintln(rt.writer, rt.ui.section("Tasks"))
			fmt.Fprintln(rt.writer, rt.session.TaskGraph.RenderExportSection())
			return nil
		}
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No active plan."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Tasks"))
	var completed int
	var inProgress int
	var pending int
	for _, item := range rt.session.Plan {
		switch item.Status {
		case "completed":
			completed++
		case "in_progress":
			inProgress++
		default:
			pending++
		}
	}
	fmt.Fprintln(rt.writer)
	rt.printKVGroup("Summary",
		kv("total", fmt.Sprintf("%d", len(rt.session.Plan))),
		kv("completed", fmt.Sprintf("%d", completed)),
		kv("in_progress", fmt.Sprintf("%d", inProgress)),
		kv("pending", fmt.Sprintf("%d", pending)),
	)
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.subsection("Plan"))
	for i, item := range rt.session.Plan {
		fmt.Fprintln(rt.writer, rt.ui.planItem(i, item.Status, item.Step))
	}
	return nil
}
