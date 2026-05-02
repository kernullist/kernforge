package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type SessionEventStreamRecord struct {
	SessionID string            `json:"session_id"`
	Workspace string            `json:"workspace,omitempty"`
	Provider  string            `json:"provider,omitempty"`
	Model     string            `json:"model,omitempty"`
	Event     ConversationEvent `json:"event"`
}

func (rt *runtimeState) handleEventsCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	options, err := parseEventsCommandOptions(args)
	if err != nil {
		return err
	}
	switch options.Action {
	case "export":
		return rt.exportSessionEvents(options.OutputPath)
	default:
		return rt.printSessionEventsTail(options.Limit)
	}
}

type eventsCommandOptions struct {
	Action     string
	Limit      int
	OutputPath string
}

func parseEventsCommandOptions(args string) (eventsCommandOptions, error) {
	options := eventsCommandOptions{
		Action: "tail",
		Limit:  20,
	}
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return options, nil
	}
	first := strings.ToLower(strings.TrimSpace(fields[0]))
	switch first {
	case "tail", "list", "show":
		options.Action = "tail"
		fields = fields[1:]
	case "export", "write":
		options.Action = "export"
		fields = fields[1:]
	default:
		if n, ok := parseEventsLimit(first); ok {
			options.Limit = n
			return options, nil
		}
		return options, fmt.Errorf("usage: /events [tail [n]|export [path]]")
	}
	for _, field := range fields {
		trimmed := strings.TrimSpace(field)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "--limit=") {
			n, ok := parseEventsLimit(strings.TrimSpace(trimmed[len("--limit="):]))
			if !ok {
				return options, fmt.Errorf("invalid events limit: %s", trimmed)
			}
			options.Limit = n
			continue
		}
		if n, ok := parseEventsLimit(trimmed); ok && options.Action == "tail" {
			options.Limit = n
			continue
		}
		if options.Action == "export" && options.OutputPath == "" {
			options.OutputPath = trimmed
			continue
		}
		return options, fmt.Errorf("usage: /events [tail [n]|export [path]]")
	}
	return options, nil
}

func parseEventsLimit(raw string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 0, false
	}
	if n > 200 {
		n = 200
	}
	return n, true
}

func (rt *runtimeState) printSessionEventsTail(limit int) error {
	events := recentSessionEvents(rt.session.ConversationEvents, limit)
	if len(events) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No session events recorded."))
		return nil
	}
	for _, event := range events {
		line, err := marshalSessionEventStreamRecord(rt.session, event)
		if err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, line)
	}
	return nil
}

func (rt *runtimeState) exportSessionEvents(rawPath string) error {
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = sessionBaseWorkingDir(rt.session)
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	outputPath := strings.TrimSpace(rawPath)
	if outputPath == "" {
		outputPath = filepath.Join(root, userConfigDirName, "events", rt.session.ID+".jsonl")
	} else if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(root, outputPath)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	latestPath := filepath.Join(root, userConfigDirName, "events", "latest.jsonl")
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindEventStream,
		Severity: conversationSeverityInfo,
		Summary:  "session event stream exported",
		ArtifactRefs: []string{
			outputPath,
			latestPath,
		},
		Entities: map[string]string{
			"format": "jsonl",
		},
	})
	body, err := renderSessionEventsJSONL(rt.session, rt.session.ConversationEvents)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, []byte(body), 0o644); err != nil {
		return err
	}
	if !strings.EqualFold(outputPath, latestPath) {
		if err := os.MkdirAll(filepath.Dir(latestPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(latestPath, []byte(body), 0o644); err != nil {
			return err
		}
	}
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Exported session events: "+outputPath))
	if !strings.EqualFold(outputPath, latestPath) {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("latest", latestPath))
	}
	return nil
}

func recentSessionEvents(events []ConversationEvent, limit int) []ConversationEvent {
	if limit <= 0 {
		limit = 20
	}
	if len(events) <= limit {
		return append([]ConversationEvent(nil), events...)
	}
	return append([]ConversationEvent(nil), events[len(events)-limit:]...)
}

func renderSessionEventsJSONL(session *Session, events []ConversationEvent) (string, error) {
	lines := make([]string, 0, len(events))
	for _, event := range events {
		line, err := marshalSessionEventStreamRecord(session, event)
		if err != nil {
			return "", err
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func marshalSessionEventStreamRecord(session *Session, event ConversationEvent) (string, error) {
	record := SessionEventStreamRecord{
		Event: event,
	}
	if session != nil {
		record.SessionID = session.ID
		record.Workspace = session.WorkingDir
		record.Provider = session.Provider
		record.Model = session.Model
	}
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
