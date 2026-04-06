package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (rt *runtimeState) requireSelection() (ViewerSelection, error) {
	selection := rt.session.CurrentSelection()
	if selection == nil || !selection.HasSelection() {
		return ViewerSelection{}, fmt.Errorf("no current selection. Use /open and select a range first")
	}
	return *selection, nil
}

func (rt *runtimeState) handleSelectionCommand() error {
	selection, err := rt.requireSelection()
	if err != nil {
		return err
	}
	preview, err := loadSelectionPreview(rt.workspace.Root, selection)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Selection"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("range", selection.Summary(rt.workspace.Root)))
	fmt.Fprintln(rt.writer, preview)
	return nil
}

func (rt *runtimeState) handleSelectionsCommand() error {
	rt.session.normalizeSelectionState()
	if len(rt.session.Selections) == 0 {
		return fmt.Errorf("no saved selections. Use /open and select a range first")
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Selections"))
	for i, selection := range rt.session.Selections {
		suffix := ""
		if i == rt.session.ActiveSelection {
			suffix = " [active]"
		}
		fmt.Fprintf(rt.writer, "%d. %s%s\n", i+1, selection.Summary(rt.workspace.Root), suffix)
	}
	return nil
}

func (rt *runtimeState) handleSelectionNoteCommand(note string) error {
	selection := rt.session.CurrentSelection()
	if selection == nil || !selection.HasSelection() {
		return fmt.Errorf("no current selection. Use /open and select a range first")
	}
	if strings.TrimSpace(note) == "" {
		return fmt.Errorf("usage: /note-selection <text>")
	}
	rt.session.Selections[rt.session.ActiveSelection].Note = strings.TrimSpace(note)
	active := rt.session.Selections[rt.session.ActiveSelection]
	rt.session.LastSelection = &active
	_ = rt.store.Save(rt.session)
	_ = SyncWorkspaceSelections(rt.workspace.Root, rt.session.Selections)
	fmt.Fprintln(rt.writer, rt.ui.successLine("Updated note on active selection and synced to workspace"))
	return nil
}

func (rt *runtimeState) handleSelectionTagCommand(tags string) error {
	selection := rt.session.CurrentSelection()
	if selection == nil || !selection.HasSelection() {
		return fmt.Errorf("no current selection. Use /open and select a range first")
	}
	if strings.TrimSpace(tags) == "" {
		return fmt.Errorf("usage: /tag-selection <tag[,tag2,...]>")
	}
	rt.session.Selections[rt.session.ActiveSelection].SetTags(tags)
	active := rt.session.Selections[rt.session.ActiveSelection]
	rt.session.LastSelection = &active
	_ = rt.store.Save(rt.session)
	_ = SyncWorkspaceSelections(rt.workspace.Root, rt.session.Selections)
	fmt.Fprintln(rt.writer, rt.ui.successLine("Updated tags on active selection and synced to workspace"))
	return nil
}

func (rt *runtimeState) handleUseSelectionCommand(arg string) error {
	index, err := parsePositiveInt(strings.TrimSpace(arg))
	if err != nil || index < 1 {
		return fmt.Errorf("usage: /use-selection <n>")
	}
	if !rt.session.SetActiveSelection(index - 1) {
		return fmt.Errorf("selection index out of range: %d", index)
	}
	_ = rt.store.Save(rt.session)
	fmt.Fprintln(rt.writer, rt.ui.successLine("Active selection set to "+rt.session.CurrentSelection().Summary(rt.workspace.Root)))
	return nil
}

func (rt *runtimeState) handleDropSelectionCommand(arg string) error {
	index, err := parsePositiveInt(strings.TrimSpace(arg))
	if err != nil || index < 1 {
		return fmt.Errorf("usage: /drop-selection <n>")
	}
	if !rt.session.RemoveSelection(index - 1) {
		return fmt.Errorf("selection index out of range: %d", index)
	}
	_ = rt.store.Save(rt.session)
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Dropped selection %d", index)))
	return nil
}

func (rt *runtimeState) handleSelectionDiffCommand() error {
	selection, err := rt.requireSelection()
	if err != nil {
		return err
	}
	diff, err := renderSelectionGitDiff(rt.workspace.Root, selection)
	if err != nil {
		return err
	}
	if viewErr := rt.presentDiffView("Selection Diff", selection.Summary(rt.workspace.Root), diff); viewErr == nil {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Opened selection diff in internal diff view"))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Selection Diff"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("range", selection.Summary(rt.workspace.Root)))
	fmt.Fprintln(rt.writer, diff)
	return nil
}

func (rt *runtimeState) handleSelectionReviewCommand(extra string) error {
	selection, err := rt.requireSelection()
	if err != nil {
		return err
	}
	prompt := selection.RelativePrompt(rt.workspace.Root) + " review only this selected code. Focus on bugs, risks, regressions, and missing tests."
	if strings.TrimSpace(extra) != "" {
		prompt += " " + strings.TrimSpace(extra)
	}
	prompt = rt.appendSelectionSimulationReviewContext(prompt, []ViewerSelection{selection})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	reply, err := rt.runAgentReply(ctx, prompt)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) != "" {
		rt.printAssistant(reply)
	}
	return nil
}

func (rt *runtimeState) handleSelectionsReviewCommand(args string) error {
	rt.session.normalizeSelectionState()
	if len(rt.session.Selections) == 0 {
		return fmt.Errorf("no saved selections. Use /open and select a range first")
	}
	selected, extra, err := parseSelectionReviewArgs(rt.session, args)
	if err != nil {
		return err
	}
	prompt := buildSelectionContextPrompt(rt.workspace.Root, selected) + " review only these selected code regions together. Focus on bugs, risks, regressions, duplication, and missing tests across the selected regions."
	if strings.TrimSpace(extra) != "" {
		prompt += " " + strings.TrimSpace(extra)
	}
	prompt = rt.appendSelectionSimulationReviewContext(prompt, selected)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	reply, err := rt.runAgentReply(ctx, prompt)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) != "" {
		rt.printAssistant(reply)
	}
	return nil
}

func (rt *runtimeState) appendSelectionSimulationReviewContext(prompt string, selections []ViewerSelection) string {
	return rt.appendSimulationContextForHints(prompt, buildSelectionSimulationMatchHints(selections), "Additional simulation risk focus")
}

func (rt *runtimeState) appendSimulationPlanningContext(prompt string, task string) string {
	return rt.appendSimulationContextForHints(prompt, buildSimulationTextMatchHints(task), "Additional simulation planning focus")
}

func (rt *runtimeState) appendSimulationContextForHints(prompt string, hints []string, heading string) string {
	if rt == nil || rt.evidence == nil || len(hints) == 0 {
		return prompt
	}
	records, err := rt.evidence.Search("kind:simulation_finding outcome:failed", rt.workspace.BaseRoot, 16)
	if err != nil || len(records) == 0 {
		return prompt
	}
	var matched []string
	for _, record := range records {
		haystack := strings.ToLower(strings.Join([]string{
			record.Subject,
			record.VerificationSummary,
			record.Category,
			record.SignalClass,
			strings.Join(record.Tags, " "),
		}, " "))
		for _, hint := range hints {
			if hint != "" && strings.Contains(haystack, hint) {
				line := fmt.Sprintf("%s severity=%s signal=%s risk=%d", record.Subject, valueOrDefault(record.Severity, "unknown"), valueOrDefault(record.SignalClass, "unknown"), record.RiskScore)
				matched = append(matched, line)
				break
			}
		}
		if len(matched) >= 3 {
			break
		}
	}
	if len(matched) == 0 {
		return prompt
	}
	return strings.TrimSpace(prompt) + "\n\n" + heading + ":\n- " + strings.Join(uniqueStrings(matched), "\n- ")
}

func buildSelectionSimulationMatchHints(selections []ViewerSelection) []string {
	var hints []string
	for _, selection := range selections {
		rel := filepath.ToSlash(strings.ToLower(strings.TrimSpace(selection.FilePath)))
		base := strings.ToLower(strings.TrimSpace(filepath.Base(selection.FilePath)))
		if rel != "" {
			hints = append(hints, rel)
			parts := strings.Split(rel, "/")
			for _, part := range parts {
				if len(strings.TrimSpace(part)) >= 3 {
					hints = append(hints, part)
				}
			}
		}
		if len(base) >= 3 {
			hints = append(hints, base)
			trimmedExt := strings.TrimSuffix(base, filepath.Ext(base))
			if len(trimmedExt) >= 3 {
				hints = append(hints, trimmedExt)
			}
		}
	}
	return uniqueStrings(hints)
}

func buildSimulationTextMatchHints(text string) []string {
	var hints []string
	for _, token := range extractPersistentMemoryTokens(text) {
		token = strings.TrimSpace(strings.ToLower(token))
		if len(token) >= 3 {
			hints = append(hints, token)
		}
	}
	return uniqueStrings(hints)
}

func (rt *runtimeState) handleSelectionEditCommand(task string) error {
	selection, err := rt.requireSelection()
	if err != nil {
		return err
	}
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("usage: /edit-selection <task>")
	}
	prompt := selection.RelativePrompt(rt.workspace.Root) + " edit this selected code. Keep the change strictly focused on the selected range unless adjacent lines must change for correctness. Avoid unrelated edits outside the selection. If you must touch code outside the selection, keep it minimal and explain why in the final answer. Task: " + strings.TrimSpace(task)
	prompt = rt.appendSelectionSimulationReviewContext(prompt, []ViewerSelection{selection})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	reply, err := rt.runAgentReply(ctx, prompt)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) != "" {
		rt.printAssistant(reply)
	}
	return nil
}
