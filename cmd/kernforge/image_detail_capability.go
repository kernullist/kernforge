package main

import (
	"context"
	"strings"
	"sync"
)

type imageDetailOriginalSupportContextKey struct{}

var codexImageDetailSupport = struct {
	sync.RWMutex
	byModel map[string]bool
}{
	byModel: map[string]bool{
		openAICodexDefaultModel: true,
		"gpt-5.4":               true,
		"gpt-5.4-mini":          true,
		"gpt-5.3-codex":         true,
		"gpt-5.2":               false,
		"codex-auto-review":     true,
	},
}

func contextWithOriginalImageDetailSupport(ctx context.Context, supported bool) context.Context {
	return context.WithValue(ctx, imageDetailOriginalSupportContextKey{}, supported)
}

func originalImageDetailSupportFromContext(ctx context.Context) (bool, bool) {
	supported, ok := ctx.Value(imageDetailOriginalSupportContextKey{}).(bool)
	return supported, ok
}

func canRequestOriginalImageDetail(provider string, model string) bool {
	if !providerUsesCodexImageDetailPolicy(provider) {
		return true
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || model == codexCLIDefaultModel {
		return true
	}
	codexImageDetailSupport.RLock()
	supported, ok := codexImageDetailSupport.byModel[model]
	codexImageDetailSupport.RUnlock()
	if ok {
		return supported
	}
	return false
}

func registerCodexModelImageDetailSupport(model string, supported bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return
	}
	codexImageDetailSupport.Lock()
	codexImageDetailSupport.byModel[model] = supported
	codexImageDetailSupport.Unlock()
}

func providerUsesCodexImageDetailPolicy(provider string) bool {
	switch normalizeProviderName(provider) {
	case "openai-codex", "codex-cli":
		return true
	default:
		return false
	}
}

func adaptToolDefinitionsForImageDetailSupport(tools []ToolDefinition, provider string, model string) []ToolDefinition {
	if len(tools) == 0 || canRequestOriginalImageDetail(provider, model) {
		return tools
	}
	out := make([]ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(tool.Name), "view_image") {
			tool = withoutViewImageOriginalDetailInput(tool)
		}
		out = append(out, tool)
	}
	return out
}

func withoutViewImageOriginalDetailInput(tool ToolDefinition) ToolDefinition {
	tool.InputSchema = cloneSchemaMap(tool.InputSchema)
	properties, ok := tool.InputSchema["properties"].(map[string]any)
	if ok {
		delete(properties, "detail")
	}
	return tool
}

func cloneSchemaMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneSchemaValue(value)
	}
	return out
}

func cloneSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneSchemaMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneSchemaValue(item)
		}
		return out
	default:
		return value
	}
}

func sanitizeToolExecutionImageDetailForModel(result ToolExecutionResult, provider string, model string) ToolExecutionResult {
	if canRequestOriginalImageDetail(provider, model) {
		return result
	}
	result.ContentItems = sanitizeOriginalImageDetailItems(result.ContentItems)
	result.ModelContentItems = sanitizeOriginalImageDetailItems(result.ModelContentItems)
	return result
}

func sanitizeOriginalImageDetailItems(items []ToolContentItem) []ToolContentItem {
	if len(items) == 0 {
		return items
	}
	out := append([]ToolContentItem(nil), items...)
	for i := range out {
		if strings.TrimSpace(out[i].Type) == "input_image" && strings.TrimSpace(out[i].Detail) == imageDetailOriginal {
			out[i].Detail = imageDetailHigh
		}
	}
	return out
}
