package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClientUsesVersionedBaseURLWithoutDuplicatingV1(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL+"/api/v1", "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestOpenAIClientOmitsToolChoiceWithoutTools(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openrouter:z-ai/glm-5v-turbo",
		Messages: []Message{
			{
				Role: "user",
				Text: "hello",
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := body["tool_choice"]; ok {
		t.Fatalf("tool_choice should be omitted when no tools are provided")
	}
}

func TestOpenAIClientSetsToolChoiceWhenToolsExist(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{
			{
				Role: "user",
				Text: "hello",
			},
		},
		Tools: []ToolDefinition{
			{
				Name:        "read_file",
				Description: "Read a file",
				InputSchema: map[string]any{
					"type": "object",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	value, ok := body["tool_choice"].(string)
	if !ok {
		t.Fatalf("tool_choice should be present when tools are provided")
	}
	if value != "auto" {
		t.Fatalf("unexpected tool_choice: %v", value)
	}
}

func TestOpenAIClientIncludesStructuredErrorDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"Provider returned error","type":"server_error","param":"model","code":"backend_failure"}}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	text := err.Error()
	for _, part := range []string{"Provider returned error", "type=server_error", "param=model", "code=backend_failure"} {
		if !strings.Contains(text, part) {
			t.Fatalf("expected %q in error, got %q", part, text)
		}
	}
}
