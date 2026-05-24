package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ViewImageTool struct {
	ws Workspace
}

const viewImageUnsupportedMessage = "view_image is not allowed because you do not support image inputs"

func NewViewImageTool(ws Workspace) ViewImageTool {
	return ViewImageTool{ws: ws}
}

func (t ViewImageTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "view_image",
		Description: "View a local image from the filesystem (only use if given a full filepath by the user, and the image isn't already attached to the thread context within <image ...> tags).",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Local filesystem path to an image file",
				},
				"detail": map[string]any{
					"type":        "string",
					"enum":        []any{imageDetailHigh, imageDetailOriginal},
					"description": "Optional detail override. Supported values are `high` and `original`; omit this field for default high resized behavior. Use `original` to preserve the file's original resolution instead of resizing to fit.",
				},
			},
			"required": []any{"path"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"image_url": map[string]any{
					"type":        "string",
					"description": "Data URL for the loaded image.",
				},
				"detail": map[string]any{
					"type":        "string",
					"enum":        []any{imageDetailHigh, imageDetailOriginal},
					"description": "Image detail hint returned by view_image. Returns `high` for default resized behavior or `original` when original resolution is preserved.",
				},
			},
			"required": []any{"image_url", "detail"},
		},
	}
}

func (t ViewImageTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t ViewImageTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolExecutionResult{}, err
	}
	if supported, ok := imageInputSupportFromContext(ctx); ok && !supported {
		return ToolExecutionResult{DisplayText: viewImageUnsupportedMessage}, fmt.Errorf(viewImageUnsupportedMessage)
	}
	args, err := requireToolInputObject(input, "view_image")
	if err != nil {
		return ToolExecutionResult{}, err
	}
	rawPath := strings.TrimSpace(stringValue(args, "path"))
	if rawPath == "" {
		return ToolExecutionResult{}, fmt.Errorf("view_image.path is required")
	}
	detail, err := normalizeImageDetail(stringValue(args, "detail"))
	if err != nil {
		got := strings.TrimSpace(stringValue(args, "detail"))
		return ToolExecutionResult{}, fmt.Errorf("view_image.detail only supports `high` or `original`; omit `detail` for default high resized behavior, got `%s`", got)
	}
	if detail == "" {
		detail = imageDetailHigh
	}
	if detail == imageDetailOriginal {
		if supported, ok := originalImageDetailSupportFromContext(ctx); ok && !supported {
			detail = imageDetailHigh
		}
	}
	absPath, err := t.ws.ResolveForActiveLookup(filepath.FromSlash(rawPath))
	if err != nil {
		return ToolExecutionResult{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return ToolExecutionResult{}, fmt.Errorf("unable to locate image at `%s`: %w", absPath, err)
	}
	if !info.Mode().IsRegular() {
		return ToolExecutionResult{}, fmt.Errorf("image path `%s` is not a file", absPath)
	}
	image, err := loadImageForPrompt(absPath, detail)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	imageURL := promptImageDataURI(image)
	display, _ := json.Marshal(map[string]string{
		"image_url": imageURL,
		"detail":    image.Detail,
	})
	meta := map[string]any{
		"path":       absPath,
		"detail":     image.Detail,
		"media_type": image.MediaType,
		"width":      image.Width,
		"height":     image.Height,
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{
		DisplayText: string(display),
		ContentItems: []ToolContentItem{{
			Type:     "input_image",
			ImageURL: imageURL,
			Detail:   image.Detail,
		}},
		Meta: meta,
	}, nil
}
