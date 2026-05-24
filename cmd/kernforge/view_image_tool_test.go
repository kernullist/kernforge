package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewImageToolReturnsResponsesContentItem(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")
	tool := NewViewImageTool(Workspace{BaseRoot: dir, Root: dir})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path": "shot.png",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if result.DisplayText == "" {
		t.Fatal("expected display JSON")
	}
	if len(result.ContentItems) != 1 {
		t.Fatalf("expected one content item, got %#v", result.ContentItems)
	}
	item := result.ContentItems[0]
	if item.Type != "input_image" || item.Detail != imageDetailHigh {
		t.Fatalf("unexpected content item: %#v", item)
	}
	if !strings.HasPrefix(item.ImageURL, "data:image/png;base64,") {
		t.Fatalf("expected PNG data URL, got %q", item.ImageURL)
	}
	payload := map[string]string{}
	if err := json.Unmarshal([]byte(result.DisplayText), &payload); err != nil {
		t.Fatalf("display JSON: %v", err)
	}
	if payload["detail"] != imageDetailHigh || payload["image_url"] != item.ImageURL {
		t.Fatalf("unexpected display payload: %#v", payload)
	}
}

func TestViewImageToolRejectsUnsupportedDetail(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")
	tool := NewViewImageTool(Workspace{BaseRoot: dir, Root: dir})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path":   "shot.png",
		"detail": "low",
	})
	if err == nil {
		t.Fatal("expected unsupported detail error")
	}
	if !strings.Contains(err.Error(), "view_image.detail only supports") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestViewImageToolReturnsUnsupportedMessageForTextOnlyModel(t *testing.T) {
	tool := NewViewImageTool(Workspace{BaseRoot: t.TempDir(), Root: t.TempDir()})
	ctx := contextWithImageInputSupport(context.Background(), false)

	result, err := tool.ExecuteDetailed(ctx, map[string]any{
		"path": "missing.png",
	})
	if err == nil {
		t.Fatal("expected unsupported image input error")
	}
	if result.DisplayText != viewImageUnsupportedMessage {
		t.Fatalf("expected model-visible unsupported message, got %#v", result)
	}
	if err.Error() != viewImageUnsupportedMessage {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestViewImageToolRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	tool := NewViewImageTool(Workspace{BaseRoot: dir, Root: dir})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path": ".",
	})
	if err == nil {
		t.Fatal("expected directory error")
	}
	if !strings.Contains(err.Error(), "is not a file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestViewImageToolRejectsSymlinkOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.png")
	if err := os.WriteFile(external, onePixelPNG, 0o644); err != nil {
		t.Fatalf("WriteFile external image: %v", err)
	}
	link := filepath.Join(dir, "link.png")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	tool := NewViewImageTool(Workspace{BaseRoot: dir, Root: dir})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path": "link.png",
	})
	if err == nil {
		t.Fatal("expected outside symlink error")
	}
	if !strings.Contains(err.Error(), "resolves outside the active workspace root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestViewImageToolUsesActiveWorkspaceRootWithoutBaseFallback(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := filepath.Join(baseRoot, "worktree")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeTestImage(t, baseRoot, "shot.png")
	tool := NewViewImageTool(Workspace{BaseRoot: baseRoot, Root: activeRoot})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path": "shot.png",
	})
	if err == nil {
		t.Fatal("expected active-root lookup miss")
	}
	wantPath := filepath.Join(activeRoot, "shot.png")
	if !strings.Contains(err.Error(), wantPath) {
		t.Fatalf("expected error to reference active root path %q, got %v", wantPath, err)
	}
	basePath := filepath.Join(baseRoot, "shot.png")
	if strings.Contains(err.Error(), basePath) {
		t.Fatalf("view_image must not fall back to base root path %q, got %v", basePath, err)
	}
}

func TestViewImageToolIncludesEffectiveWorkspaceRootsMeta(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := filepath.Join(baseRoot, "worktree")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeTestImage(t, activeRoot, "shot.png")
	tool := NewViewImageTool(Workspace{BaseRoot: baseRoot, Root: activeRoot})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path": "shot.png",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(result.Meta, "workspace_root"); !sameFilePath(got, baseRoot) {
		t.Fatalf("expected base workspace_root %q, got %#v", baseRoot, result.Meta)
	}
	if got := toolMetaString(result.Meta, "active_workspace_root"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected active workspace root %q, got %#v", activeRoot, result.Meta)
	}
	roots := toolMetaStringSlice(result.Meta, "workspace_roots")
	if len(roots) != 2 || !sameFilePath(roots[0], baseRoot) || !sameFilePath(roots[1], activeRoot) {
		t.Fatalf("expected effective workspace_roots [%q %q], got %#v", baseRoot, activeRoot, result.Meta)
	}
}

func TestViewImageToolDowngradesOriginalWhenContextDisallows(t *testing.T) {
	dir := t.TempDir()
	writeTestImage(t, dir, "shot.png")
	tool := NewViewImageTool(Workspace{BaseRoot: dir, Root: dir})
	ctx := contextWithOriginalImageDetailSupport(context.Background(), false)

	result, err := tool.ExecuteDetailed(ctx, map[string]any{
		"path":   "shot.png",
		"detail": imageDetailOriginal,
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if len(result.ContentItems) != 1 {
		t.Fatalf("expected one content item, got %#v", result.ContentItems)
	}
	if result.ContentItems[0].Detail != imageDetailHigh {
		t.Fatalf("expected high detail, got %#v", result.ContentItems[0])
	}
	payload := map[string]string{}
	if err := json.Unmarshal([]byte(result.DisplayText), &payload); err != nil {
		t.Fatalf("display JSON: %v", err)
	}
	if payload["detail"] != imageDetailHigh {
		t.Fatalf("display detail = %q, want high", payload["detail"])
	}
}

func TestLoadImageForPromptDownscalesLargePNG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.png")
	if err := os.WriteFile(path, largePNGForTest(t, 4096, 2048), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	image, err := loadImageForPrompt(path, imageDetailHigh)
	if err != nil {
		t.Fatalf("loadImageForPrompt: %v", err)
	}
	if image.Width > maxPromptImageDimension || image.Height > maxPromptImageDimension {
		t.Fatalf("expected resized image within bounds, got %dx%d", image.Width, image.Height)
	}
	if image.Detail != imageDetailHigh || image.MediaType != "image/png" {
		t.Fatalf("unexpected processed image: %#v", image)
	}
}

func TestLoadImageForPromptPreservesOriginalDetail(t *testing.T) {
	dir := t.TempDir()
	path := writeTestImage(t, dir, "shot.png")

	image, err := loadImageForPrompt(path, imageDetailOriginal)
	if err != nil {
		t.Fatalf("loadImageForPrompt: %v", err)
	}
	if image.Detail != imageDetailOriginal {
		t.Fatalf("expected original detail, got %q", image.Detail)
	}
	if got := base64.StdEncoding.EncodeToString(image.Data); got != base64.StdEncoding.EncodeToString(onePixelPNG) {
		t.Fatalf("expected original bytes to be preserved")
	}
}

func largePNGForTest(t *testing.T, width int, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 10, B: 10, A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("encode large png: %v", err)
	}
	return out.Bytes()
}
