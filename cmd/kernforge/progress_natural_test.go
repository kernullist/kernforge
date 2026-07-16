package main

import (
	"strings"
	"testing"
	"time"
)

func TestNaturalProgressHidesRawToolNames(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}

	started := formatProgressEventMessage(cfg, ProgressEvent{
		Kind:             progressKindToolStarted,
		ToolName:         "read_file",
		ArgumentsPreview: "path=README.md",
	})
	if !strings.Contains(started, "Reading README.md") {
		t.Fatalf("started: %q", started)
	}
	if strings.Contains(started, "read_file") {
		t.Fatalf("must not expose tool id: %q", started)
	}

	completed := formatProgressEventMessage(cfg, ProgressEvent{
		Kind:             progressKindToolCompleted,
		ToolName:         "list_files",
		ArgumentsPreview: "path=imwatchingu",
		Status:           "38 items",
	})
	if !strings.Contains(completed, "Listed imwatchingu") {
		t.Fatalf("completed: %q", completed)
	}
	if strings.Contains(completed, "list_files") {
		t.Fatalf("must not expose tool id: %q", completed)
	}
}

func TestNaturalProgressSuppressesStreamNoise(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}
	for _, kind := range []string{
		progressKindModelStreamToolCall,
		progressKindModelStreamToolArgs,
		progressKindModelStreamToolReady,
	} {
		msg := formatProgressEventMessage(cfg, ProgressEvent{
			Kind:     kind,
			ToolName: "read_file",
		})
		if strings.TrimSpace(msg) != "" {
			t.Fatalf("stream kind %s should be silent, got %q", kind, msg)
		}
	}
}

func TestNaturalProgressThinkingDoesNotExposeProvider(t *testing.T) {
	cfg := Config{AutoLocale: boolPtr(false)}
	start := formatProgressEventMessage(cfg, ProgressEvent{
		Kind:     progressKindModelRequestStart,
		Provider: "openrouter",
		Model:    "z-ai/glm-5.2",
	})
	if strings.Contains(start, "openrouter") || strings.Contains(start, "glm") {
		t.Fatalf("start must not expose provider/model: %q", start)
	}
	wait := formatProgressEventMessage(cfg, ProgressEvent{
		Kind:     progressKindModelRequestWait,
		Provider: "openrouter",
		Model:    "z-ai/glm-5.2",
		Elapsed:  5 * time.Second,
	})
	if strings.Contains(wait, "openrouter") || strings.Contains(wait, "glm") {
		t.Fatalf("wait must not expose provider/model: %q", wait)
	}
	if !strings.Contains(wait, "thinking") && !strings.Contains(wait, "Thinking") {
		t.Fatalf("wait should sound like thinking: %q", wait)
	}
}

func TestParseToolProgressArgsKeepsCommandWithSpaces(t *testing.T) {
	path, pattern, command, query := parseToolProgressArgs(`path=src/main.go command=rg -n "foo" agent.go pattern=bar`)
	if path != "src/main.go" {
		t.Fatalf("path=%q", path)
	}
	if command != `rg -n "foo" agent.go` {
		t.Fatalf("command=%q", command)
	}
	if pattern != "bar" {
		t.Fatalf("pattern=%q", pattern)
	}
	if query != "" {
		t.Fatalf("query=%q", query)
	}
}
