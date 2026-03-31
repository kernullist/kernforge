package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var onePixelPNG = []byte{
	137, 80, 78, 71, 13, 10, 26, 10,
	0, 0, 0, 13, 73, 72, 68, 82,
	0, 0, 0, 1, 0, 0, 0, 1,
	8, 2, 0, 0, 0, 144, 119, 83,
	222, 0, 0, 0, 12, 73, 68, 65,
	84, 8, 153, 99, 248, 15, 4, 0,
	9, 251, 3, 253, 160, 37, 90, 158,
	0, 0, 0, 0, 73, 69, 78, 68,
	174, 66, 96, 130,
}

func writeTestImage(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, onePixelPNG, 0o644); err != nil {
		t.Fatalf("write test image: %v", err)
	}
	return path
}

func TestParseImageInputListResolvesRelativePath(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")

	images, err := parseImageInputList(dir, "shot.png")
	if err != nil {
		t.Fatalf("parseImageInputList: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].Path != "shot.png" {
		t.Fatalf("expected stored path shot.png, got %q", images[0].Path)
	}
	if images[0].MediaType != "image/png" {
		t.Fatalf("expected image/png, got %q", images[0].MediaType)
	}
}

func TestExpandMentionsAttachesImageAndTextContext(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	agent := &Agent{
		Session: &Session{WorkingDir: dir},
	}

	text, images := agent.expandMentions(context.Background(), "@shot.png explain @main.go")

	if len(images) != 1 {
		t.Fatalf("expected 1 image attachment, got %d", len(images))
	}
	if images[0].Path != "shot.png" {
		t.Fatalf("expected image path shot.png, got %q", images[0].Path)
	}
	if strings.Contains(text, "@shot.png") {
		t.Fatalf("expected image mention to be normalized in prompt text: %q", text)
	}
	if !strings.Contains(text, "Attached context:") || !strings.Contains(text, "Referenced file:") {
		t.Fatalf("expected text file context to be injected, got %q", text)
	}
}

func TestOpenAIClientEncodesImageMentions(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		messages := payload["messages"].([]any)
		user := messages[0].(map[string]any)
		content := user["content"].([]any)
		if len(content) != 2 {
			t.Fatalf("expected text + image parts, got %d", len(content))
		}
		imagePart := content[1].(map[string]any)
		imageURL := imagePart["image_url"].(map[string]any)["url"].(string)
		if !strings.HasPrefix(imageURL, "data:image/png;base64,") {
			t.Fatalf("expected data URI image, got %q", imageURL)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model:      "gpt-4.1",
		WorkingDir: dir,
		Messages: []Message{{
			Role: "user",
			Text: "describe this",
			Images: []MessageImage{{
				Path:      "shot.png",
				MediaType: "image/png",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestAnthropicClientEncodesImageMentions(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		messages := payload["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		if len(content) != 2 {
			t.Fatalf("expected image + text blocks, got %d", len(content))
		}
		imageBlock := content[0].(map[string]any)
		if imageBlock["type"].(string) != "image" {
			t.Fatalf("expected first block to be image, got %v", imageBlock["type"])
		}
		source := imageBlock["source"].(map[string]any)
		if source["media_type"].(string) != "image/png" {
			t.Fatalf("expected image/png, got %v", source["media_type"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer server.Close()

	client := NewAnthropicClient(server.URL, "test-key")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model:      "claude-sonnet",
		WorkingDir: dir,
		Messages: []Message{{
			Role: "user",
			Text: "describe this",
			Images: []MessageImage{{
				Path:      "shot.png",
				MediaType: "image/png",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestOllamaClientEncodesImageMentions(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		messages := payload["messages"].([]any)
		user := messages[0].(map[string]any)
		images := user["images"].([]any)
		if len(images) != 1 {
			t.Fatalf("expected 1 encoded image, got %d", len(images))
		}
		if images[0].(string) == "" {
			t.Fatal("expected non-empty base64 image payload")
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"},"done_reason":"stop"}`))
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "")
	_, err := client.Complete(context.Background(), ChatRequest{
		Model:      "llava",
		WorkingDir: dir,
		Messages: []Message{{
			Role: "user",
			Text: "describe this",
			Images: []MessageImage{{
				Path:      "shot.png",
				MediaType: "image/png",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}
