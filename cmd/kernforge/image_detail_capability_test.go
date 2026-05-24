package main

import "testing"

func TestAdaptToolDefinitionsRemovesOriginalDetailForUnsupportedCodexModel(t *testing.T) {
	tool := NewViewImageTool(Workspace{}).Definition()
	adapted := adaptToolDefinitionsForImageDetailSupport([]ToolDefinition{tool}, "openai-codex", "gpt-5.2")
	if len(adapted) != 1 {
		t.Fatalf("expected one tool, got %d", len(adapted))
	}
	props := adapted[0].InputSchema["properties"].(map[string]any)
	if _, ok := props["detail"]; ok {
		t.Fatalf("unsupported Codex model should not receive view_image.detail schema: %#v", props["detail"])
	}
	originalProps := tool.InputSchema["properties"].(map[string]any)
	if _, ok := originalProps["detail"]; !ok {
		t.Fatalf("adaptation mutated original tool schema")
	}
}

func TestAdaptToolDefinitionsKeepsOriginalDetailForSupportedCodexModel(t *testing.T) {
	tool := NewViewImageTool(Workspace{}).Definition()
	adapted := adaptToolDefinitionsForImageDetailSupport([]ToolDefinition{tool}, "openai-codex", "gpt-5.4")
	props := adapted[0].InputSchema["properties"].(map[string]any)
	if _, ok := props["detail"]; !ok {
		t.Fatalf("supported Codex model should keep view_image.detail schema")
	}
}

func TestAdaptToolDefinitionsKeepsOriginalDetailForNonCodexProvider(t *testing.T) {
	tool := NewViewImageTool(Workspace{}).Definition()
	adapted := adaptToolDefinitionsForImageDetailSupport([]ToolDefinition{tool}, "lmstudio", "qwen/qwen3.6-27b")
	props := adapted[0].InputSchema["properties"].(map[string]any)
	if _, ok := props["detail"]; !ok {
		t.Fatalf("non-Codex providers should keep existing view_image.detail schema")
	}
}

func TestSanitizeToolExecutionImageDetailForUnsupportedCodexModel(t *testing.T) {
	result := ToolExecutionResult{
		ContentItems: []ToolContentItem{{
			Type:     "input_image",
			ImageURL: "data:image/png;base64,AAA",
			Detail:   imageDetailOriginal,
		}},
		ModelContentItems: []ToolContentItem{{
			Type:     "input_image",
			ImageURL: "data:image/png;base64,BBB",
			Detail:   imageDetailOriginal,
		}},
	}
	got := sanitizeToolExecutionImageDetailForModel(result, "openai-codex", "gpt-5.2")
	if got.ContentItems[0].Detail != imageDetailHigh {
		t.Fatalf("content item detail = %q, want high", got.ContentItems[0].Detail)
	}
	if got.ModelContentItems[0].Detail != imageDetailHigh {
		t.Fatalf("model content item detail = %q, want high", got.ModelContentItems[0].Detail)
	}
	if result.ContentItems[0].Detail != imageDetailOriginal {
		t.Fatalf("sanitizer mutated input result")
	}
}

func TestCanRequestOriginalImageDetailUsesDiscoveredCodexModelSupport(t *testing.T) {
	registerCodexModelImageDetailSupport("future-codex-model", true)
	if !canRequestOriginalImageDetail("openai-codex", "future-codex-model") {
		t.Fatalf("expected discovered model support to be honored")
	}
	registerCodexModelImageDetailSupport("future-codex-model", false)
	if canRequestOriginalImageDetail("openai-codex", "future-codex-model") {
		t.Fatalf("expected discovered unsupported model to be honored")
	}
}

func TestCanRequestImageInputUsesDiscoveredCodexModalities(t *testing.T) {
	registerCodexModelImageInputSupport("future-text-only-codex-model", false)
	if canRequestImageInput("openai-codex", "future-text-only-codex-model") {
		t.Fatalf("expected text-only Codex model to reject image input")
	}
	registerCodexModelImageInputSupport("future-vision-codex-model", true)
	if !canRequestImageInput("openai-codex", "future-vision-codex-model") {
		t.Fatalf("expected image-capable Codex model to allow image input")
	}
	if !canRequestImageInput("lmstudio", "text-only") {
		t.Fatalf("non-Codex providers should keep image input behavior unchanged")
	}
}

func TestCodexInputModalitiesDefaultToImageSupportWhenAbsent(t *testing.T) {
	if !codexInputModalitiesSupportImages(nil) {
		t.Fatalf("missing input_modalities should match Codex default image support")
	}
	textOnly := []string{"text"}
	if codexInputModalitiesSupportImages(&textOnly) {
		t.Fatalf("text-only input_modalities should not support images")
	}
	vision := []string{"text", "image"}
	if !codexInputModalitiesSupportImages(&vision) {
		t.Fatalf("image input modality should support images")
	}
}
