package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestOpenAIClientNormalizesAssistantToolCallArguments(t *testing.T) {
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
				Role: "assistant",
				ToolCalls: []ToolCall{
					{
						ID:        "call_1",
						Name:      "read_file",
						Arguments: "path=C:\\\\temp\\\\note.txt",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	messages, ok := body["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("messages missing from request body: %#v", body["messages"])
	}
	assistant, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("assistant message has unexpected type: %#v", messages[0])
	}
	if _, ok := assistant["content"]; ok {
		t.Fatalf("assistant content should be omitted when only tool calls are present")
	}
	toolCalls, ok := assistant["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("tool_calls missing from assistant message: %#v", assistant["tool_calls"])
	}
	call, ok := toolCalls[0].(map[string]any)
	if !ok {
		t.Fatalf("tool call has unexpected type: %#v", toolCalls[0])
	}
	function, ok := call["function"].(map[string]any)
	if !ok {
		t.Fatalf("function payload has unexpected type: %#v", call["function"])
	}
	args, ok := function["arguments"].(string)
	if !ok {
		t.Fatalf("arguments should be encoded as a string: %#v", function["arguments"])
	}
	expected := `{"raw":"path=C:\\\\temp\\\\note.txt"}`
	if args != expected {
		t.Fatalf("unexpected normalized arguments: got %q want %q", args, expected)
	}
}

func TestOpenAIClientIncludesRequestPreviewOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"Provider returned error","code":"400"}}`))
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
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), `request={"model":"openai/gpt-4.1"`) {
		t.Fatalf("expected request preview in error, got %q", err.Error())
	}
}

func TestOpenAIClientStreamsTextDeltas(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	var deltas []string
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
		OnTextDelta: func(text string) {
			deltas = append(deltas, text)
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if body["stream"] != true {
		t.Fatalf("expected stream=true in request body, got %#v", body["stream"])
	}
	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("unexpected deltas: %#v", deltas)
	}
	if resp.Message.Text != "hello" {
		t.Fatalf("unexpected streamed text: %q", resp.Message.Text)
	}
	if resp.StopReason != "stop" {
		t.Fatalf("unexpected stop reason: %q", resp.StopReason)
	}
}

func TestOpenAIClientStreamsToolCallArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\"\"}}]},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"main.go\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	resp, err := client.Complete(context.Background(), ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		OnTextDelta: func(text string) {},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected one streamed tool call, got %#v", resp.Message.ToolCalls)
	}
	call := resp.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "read_file" {
		t.Fatalf("unexpected tool call identity: %#v", call)
	}
	if call.Arguments != "{\"path\":\"main.go\"}" {
		t.Fatalf("unexpected tool call arguments: %q", call.Arguments)
	}
	if resp.StopReason != "tool_calls" {
		t.Fatalf("unexpected stop reason: %q", resp.StopReason)
	}
}

func TestOpenAIClientStreamHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, err := client.Complete(ctx, ChatRequest{
			Model: "openai/gpt-4.1",
			Messages: []Message{{
				Role: "user",
				Text: "hello",
			}},
			OnTextDelta: func(string) {},
		})
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("stream request did not stop after context cancellation")
	}
}

func TestOpenAIClientReturnsPartialTextOnStreamDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial answer\"},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	resp, err := client.Complete(ctx, ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "hello",
		}},
		OnTextDelta: func(string) {},
	})
	if err != nil {
		t.Fatalf("expected partial response instead of timeout, got %v", err)
	}
	if resp.Message.Text != "partial answer" {
		t.Fatalf("unexpected partial text: %q", resp.Message.Text)
	}
	if resp.StopReason != "partial" {
		t.Fatalf("unexpected stop reason: %q", resp.StopReason)
	}
}

func TestOpenAIClientDoesNotReturnPartialToolCallOnStreamDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\"\"}}]},\"finish_reason\":\"\"}]}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.Complete(ctx, ChatRequest{
		Model: "openai/gpt-4.1",
		Messages: []Message{{
			Role: "user",
			Text: "inspect",
		}},
		OnTextDelta: func(string) {},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout for partial tool call stream, got %v", err)
	}
}
