package main

import (
	"context"
	"net/http"
	"net/http/httptest"
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
