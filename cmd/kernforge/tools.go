package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

type Tool interface {
	Definition() ToolDefinition
	Execute(ctx context.Context, input any) (string, error)
}

type modelHiddenTool interface {
	HiddenFromModel() bool
}

type ToolExecutionResult struct {
	DisplayText       string            `json:"display_text,omitempty"`
	ContentItems      []ToolContentItem `json:"content_items,omitempty"`
	ModelText         string            `json:"model_text,omitempty"`
	ModelContentItems []ToolContentItem `json:"model_content_items,omitempty"`
	Meta              map[string]any    `json:"meta,omitempty"`
}

type detailedTool interface {
	ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error)
}

type readOnlyToolCallSupport interface {
	ReadOnlyToolCall() bool
}

type parallelToolCallSupport interface {
	SupportsParallelToolCalls() bool
}

type toolWorkspaceProvider interface {
	hookWorkspace() Workspace
}

type selfManagedToolUseHooks interface {
	managesDefaultToolUseHooks() bool
}

type toolCallHookMetadata struct {
	ID   string
	Name string
}

type toolCallHookMetadataContextKey struct{}

func contextWithToolCallHookMetadata(ctx context.Context, call ToolCall) context.Context {
	meta := toolCallHookMetadata{
		ID:   strings.TrimSpace(call.ID),
		Name: strings.TrimSpace(call.Name),
	}
	if meta.ID == "" && meta.Name == "" {
		return ctx
	}
	return context.WithValue(ctx, toolCallHookMetadataContextKey{}, meta)
}

func toolCallHookMetadataFromContext(ctx context.Context) toolCallHookMetadata {
	if ctx == nil {
		return toolCallHookMetadata{}
	}
	meta, _ := ctx.Value(toolCallHookMetadataContextKey{}).(toolCallHookMetadata)
	return toolCallHookMetadata{
		ID:   strings.TrimSpace(meta.ID),
		Name: strings.TrimSpace(meta.Name),
	}
}

func requireToolInputObject(input any, toolName string) (map[string]any, error) {
	args, ok := input.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s input must be an object", strings.TrimSpace(toolName))
	}
	return args, nil
}

type sharedToolHintsAware interface {
	setSharedToolHints(*ToolHints)
}

type sharedToolHintsCapacityAware interface {
	sharedToolHintsMaxReadSpans() int
}

type ToolRegistry struct {
	tools       map[string]Tool
	definitions map[string]ToolDefinition
	issues      []ToolRegistrationIssue
}

type ToolRegistrationIssue struct {
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

func (i ToolRegistrationIssue) Summary() string {
	name := strings.TrimSpace(i.Name)
	reason := strings.TrimSpace(i.Reason)
	if name == "" {
		return reason
	}
	if reason == "" {
		return name
	}
	return name + ": " + reason
}

func NewToolRegistry(items ...Tool) *ToolRegistry {
	sharedHints := &ToolHints{maxReadSpans: sharedToolHintsLimit(items)}
	byName := make(map[string]Tool, len(items))
	definitions := make(map[string]ToolDefinition, len(items))
	issues := []ToolRegistrationIssue{}
	for _, item := range items {
		if isNilTool(item) {
			continue
		}
		def := snapshotToolDefinition(item.Definition())
		if err := validateToolDefinition(def); err != nil {
			issues = append(issues, ToolRegistrationIssue{
				Name:   def.Name,
				Reason: err.Error(),
			})
			continue
		}
		if _, exists := byName[def.Name]; exists {
			issues = append(issues, ToolRegistrationIssue{
				Name:   def.Name,
				Reason: "duplicate tool name; first registration kept",
			})
			continue
		}
		if aware, ok := item.(sharedToolHintsAware); ok {
			aware.setSharedToolHints(sharedHints)
		}
		byName[def.Name] = item
		if !toolHiddenFromModel(item) {
			definitions[def.Name] = def
		}
	}
	return &ToolRegistry{tools: byName, definitions: definitions, issues: issues}
}

func sharedToolHintsLimit(items []Tool) int {
	for _, item := range items {
		if isNilTool(item) {
			continue
		}
		aware, ok := item.(sharedToolHintsCapacityAware)
		if !ok {
			continue
		}
		if maxReadSpans := aware.sharedToolHintsMaxReadSpans(); maxReadSpans > 0 {
			return maxReadSpans
		}
	}
	return defaultReadHintSpans
}

func isNilTool(tool Tool) bool {
	if tool == nil {
		return true
	}
	value := reflect.ValueOf(tool)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func validToolDefinition(def ToolDefinition) bool {
	return validateToolDefinition(def) == nil
}

func validateToolDefinition(def ToolDefinition) error {
	if strings.TrimSpace(def.Name) == "" {
		return fmt.Errorf("missing tool name")
	}
	if def.InputSchema == nil {
		return fmt.Errorf("missing input schema")
	}
	if !validToolInputSchema(def.InputSchema) {
		return fmt.Errorf("invalid input schema")
	}
	return nil
}

func validToolInputSchema(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	if len(schema) == 0 {
		return true
	}
	if !validToolJSONSchemaMap(schema) {
		return false
	}
	_, ok := schema["type"]
	if !ok {
		return true
	}
	return toolSchemaHasType(schema, "object")
}

func validToolJSONSchemaMap(schema map[string]any) bool {
	if rawType, ok := schema["type"]; ok && !validToolSchemaType(rawType) {
		return false
	}
	if rawProperties, ok := schema["properties"]; ok && rawProperties != nil {
		properties, ok := rawProperties.(map[string]any)
		if !ok {
			return false
		}
		for _, value := range properties {
			if !validToolJSONSchemaValue(value) {
				return false
			}
		}
	}
	if rawItems, ok := schema["items"]; ok && rawItems != nil {
		if !validToolJSONSchemaValueOrList(rawItems) {
			return false
		}
	}
	if rawItems, ok := schema["prefixItems"]; ok && rawItems != nil {
		if !validToolJSONSchemaList(rawItems) {
			return false
		}
	}
	if rawAdditional, ok := schema["additionalProperties"]; ok && rawAdditional != nil {
		switch typed := rawAdditional.(type) {
		case bool:
		case map[string]any:
			if !validToolJSONSchemaMap(typed) {
				return false
			}
		default:
			return false
		}
	}
	if rawRequired, ok := schema["required"]; ok && rawRequired != nil {
		if !validToolStringArray(rawRequired) {
			return false
		}
	}
	if rawEnum, ok := schema["enum"]; ok && rawEnum != nil {
		if !validToolArray(rawEnum) {
			return false
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		raw, ok := schema[key]
		if ok && raw != nil && !validToolJSONSchemaList(raw) {
			return false
		}
	}
	for _, key := range []string{"$defs", "definitions"} {
		raw, ok := schema[key]
		if ok && raw != nil {
			definitions, ok := raw.(map[string]any)
			if !ok {
				return false
			}
			for _, value := range definitions {
				if !validToolJSONSchemaValue(value) {
					return false
				}
			}
		}
	}
	return true
}

func validToolSchemaType(raw any) bool {
	switch typed := raw.(type) {
	case string:
		return validToolPrimitiveType(typed)
	case []string:
		if len(typed) == 0 {
			return false
		}
		for _, item := range typed {
			if !validToolPrimitiveType(item) {
				return false
			}
		}
		return true
	case []any:
		if len(typed) == 0 {
			return false
		}
		for _, item := range typed {
			value, ok := item.(string)
			if !ok || !validToolPrimitiveType(value) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validToolPrimitiveType(name string) bool {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "array", "boolean", "integer", "null", "number", "object", "string":
		return true
	default:
		return false
	}
}

func validToolJSONSchemaValue(raw any) bool {
	switch typed := raw.(type) {
	case bool:
		return true
	case map[string]any:
		return validToolJSONSchemaMap(typed)
	default:
		return false
	}
}

func validToolJSONSchemaValueOrList(raw any) bool {
	if validToolJSONSchemaValue(raw) {
		return true
	}
	return validToolJSONSchemaList(raw)
}

func validToolJSONSchemaList(raw any) bool {
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if !validToolJSONSchemaValue(item) {
			return false
		}
	}
	return true
}

func validToolStringArray(raw any) bool {
	switch typed := raw.(type) {
	case []string:
		return true
	case []any:
		for _, item := range typed {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validToolArray(raw any) bool {
	switch raw.(type) {
	case []any, []string, []int, []float64, []bool:
		return true
	default:
		return false
	}
}

func toolHiddenFromModel(tool Tool) bool {
	hidden, ok := tool.(modelHiddenTool)
	return ok && hidden.HiddenFromModel()
}

func snapshotToolDefinition(def ToolDefinition) ToolDefinition {
	def.Name = strings.TrimSpace(def.Name)
	def.InputSchema = normalizeToolInputSchema(cloneToolDefinitionMap(def.InputSchema))
	def.OutputSchema = cloneToolDefinitionMap(def.OutputSchema)
	return def
}

func normalizeToolInputSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	normalizeToolJSONSchemaMap(schema)
	pruneUnreachableToolDefinitions(schema)
	compactLargeToolSchema(schema)
	return schema
}

const maxCompactToolSchemaBytes = 4000
const maxCompactToolSchemaDepth = 2

func compactLargeToolSchema(schema map[string]any) {
	if toolSchemaFitsCompactBudget(schema) {
		return
	}
	stripToolSchemaDescriptions(schema)
	if toolSchemaFitsCompactBudget(schema) {
		return
	}
	dropToolSchemaDefinitions(schema)
	if toolSchemaFitsCompactBudget(schema) {
		return
	}
	collapseDeepToolSchemaObjects(schema, 0)
}

func toolSchemaFitsCompactBudget(schema map[string]any) bool {
	data, err := json.Marshal(schema)
	return err == nil && len(data) <= maxCompactToolSchemaBytes
}

func normalizeToolJSONSchemaMap(schema map[string]any) {
	if rawProperties, ok := schema["properties"]; ok {
		if properties, ok := rawProperties.(map[string]any); ok {
			for key, value := range properties {
				properties[key] = normalizeToolJSONSchemaValue(value)
			}
		}
	}
	if rawItems, ok := schema["items"]; ok {
		schema["items"] = normalizeToolJSONSchemaValue(rawItems)
	}
	if rawItems, ok := schema["prefixItems"]; ok {
		schema["prefixItems"] = normalizeToolJSONSchemaValue(rawItems)
	}
	if rawAdditional, ok := schema["additionalProperties"]; ok {
		if nested, ok := rawAdditional.(map[string]any); ok {
			normalizeToolJSONSchemaMap(nested)
		}
	}
	if raw, ok := schema["anyOf"]; ok {
		schema["anyOf"] = normalizeToolJSONSchemaValue(raw)
	}
	for _, key := range []string{"$defs", "definitions"} {
		if raw, ok := schema[key]; ok {
			if definitions, ok := raw.(map[string]any); ok {
				for name, value := range definitions {
					definitions[name] = normalizeToolJSONSchemaValue(value)
				}
			} else {
				delete(schema, key)
			}
		}
	}
	if rawConst, ok := schema["const"]; ok {
		delete(schema, "const")
		schema["enum"] = []any{rawConst}
	}
	if _, ok := schema["type"]; !ok {
		if schemaHasReferenceOrComposition(schema) {
			return
		}
		if inferred, ok := inferToolSchemaType(schema); ok {
			schema["type"] = inferred
		} else {
			clearUnrecognizedToolSchema(schema)
			return
		}
	}
	if toolSchemaHasType(schema, "object") || (schema["type"] == nil && toolSchemaInfersObject(schema)) {
		if raw, ok := schema["properties"]; !ok || raw == nil {
			schema["properties"] = map[string]any{}
		}
	}
	if toolSchemaHasType(schema, "array") {
		if raw, ok := schema["items"]; !ok || raw == nil {
			schema["items"] = map[string]any{"type": "string"}
		}
	}
	dropUnsupportedToolSchemaKeywords(schema)
}

func schemaHasReferenceOrComposition(schema map[string]any) bool {
	for _, key := range []string{"$ref", "anyOf"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}

func dropUnsupportedToolSchemaKeywords(schema map[string]any) {
	for key := range schema {
		switch key {
		case "$ref", "type", "description", "enum", "items", "properties", "required", "additionalProperties", "anyOf", "$defs", "definitions":
		default:
			delete(schema, key)
		}
	}
}

func clearUnrecognizedToolSchema(schema map[string]any) {
	if len(schema) == 0 {
		return
	}
	for key := range schema {
		delete(schema, key)
	}
}

func normalizeToolJSONSchemaValue(raw any) any {
	switch typed := raw.(type) {
	case bool:
		return map[string]any{"type": "string"}
	case map[string]any:
		normalizeToolJSONSchemaMap(typed)
		return typed
	case []any:
		for i, item := range typed {
			typed[i] = normalizeToolJSONSchemaValue(item)
		}
		return typed
	default:
		return raw
	}
}

func inferToolSchemaType(schema map[string]any) (string, bool) {
	if toolSchemaInfersObject(schema) {
		return "object", true
	}
	for _, key := range []string{"items", "prefixItems"} {
		if _, ok := schema[key]; ok {
			return "array", true
		}
	}
	for _, key := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"} {
		if _, ok := schema[key]; ok {
			return "number", true
		}
	}
	for _, key := range []string{"enum", "format"} {
		if _, ok := schema[key]; ok {
			return "string", true
		}
	}
	return "", false
}

func toolSchemaHasType(schema map[string]any, name string) bool {
	rawType, ok := schema["type"]
	if !ok {
		return false
	}
	target := strings.TrimSpace(strings.ToLower(name))
	switch typed := rawType.(type) {
	case string:
		return strings.TrimSpace(strings.ToLower(typed)) == target
	case []string:
		for _, item := range typed {
			if strings.TrimSpace(strings.ToLower(item)) == target {
				return true
			}
		}
	case []any:
		for _, raw := range typed {
			item, ok := raw.(string)
			if ok && strings.TrimSpace(strings.ToLower(item)) == target {
				return true
			}
		}
	}
	return false
}

func toolSchemaInfersObject(schema map[string]any) bool {
	for _, key := range []string{"properties", "required", "additionalProperties"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}

type toolDefinitionRef struct {
	table string
	name  string
}

func pruneUnreachableToolDefinitions(schema map[string]any) {
	reachable := collectReachableToolDefinitionRefs(schema)
	for _, table := range []string{"$defs", "definitions"} {
		raw, ok := schema[table]
		if !ok {
			continue
		}
		definitions, ok := raw.(map[string]any)
		if !ok {
			delete(schema, table)
			continue
		}
		for name := range definitions {
			if !reachable[toolDefinitionRef{table: table, name: name}] {
				delete(definitions, name)
			}
		}
		if len(definitions) == 0 {
			delete(schema, table)
		}
	}
}

func collectReachableToolDefinitionRefs(schema map[string]any) map[toolDefinitionRef]bool {
	reachable := map[toolDefinitionRef]bool{}
	pending := collectToolDefinitionRefsFromValue(schema, false)
	for len(pending) > 0 {
		ref := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reachable[ref] {
			continue
		}
		reachable[ref] = true
		if definition, ok := toolDefinitionForRef(schema, ref); ok {
			pending = append(pending, collectToolDefinitionRefsFromValue(definition, true)...)
		}
	}
	return reachable
}

func toolDefinitionForRef(schema map[string]any, ref toolDefinitionRef) (any, bool) {
	definitions, ok := schema[ref.table].(map[string]any)
	if !ok {
		return nil, false
	}
	definition, ok := definitions[ref.name]
	return definition, ok
}

func collectToolDefinitionRefsFromValue(value any, includeDefinitionTables bool) []toolDefinitionRef {
	var refs []toolDefinitionRef
	collectToolDefinitionRefs(value, includeDefinitionTables, &refs)
	return refs
}

func collectToolDefinitionRefs(value any, includeDefinitionTables bool, refs *[]toolDefinitionRef) {
	switch typed := value.(type) {
	case map[string]any:
		if rawRef, ok := typed["$ref"].(string); ok {
			if ref, ok := parseLocalToolDefinitionRef(rawRef); ok {
				*refs = append(*refs, ref)
			}
		}
		if includeDefinitionTables {
			for _, child := range typed {
				collectToolDefinitionRefs(child, includeDefinitionTables, refs)
			}
			return
		}
		forEachToolSchemaChild(typed, includeDefinitionTables, func(child any) {
			collectToolDefinitionRefs(child, includeDefinitionTables, refs)
		})
	case []any:
		for _, child := range typed {
			collectToolDefinitionRefs(child, includeDefinitionTables, refs)
		}
	}
}

func parseLocalToolDefinitionRef(raw string) (toolDefinitionRef, bool) {
	fragment, ok := strings.CutPrefix(strings.TrimSpace(raw), "#")
	if !ok {
		return toolDefinitionRef{}, false
	}
	if decoded, err := url.PathUnescape(fragment); err == nil {
		fragment = decoded
	}
	parts := strings.Split(fragment, "/")
	if len(parts) < 3 || parts[0] != "" {
		return toolDefinitionRef{}, false
	}
	table := decodeJSONPointerToken(parts[1])
	if table != "$defs" && table != "definitions" {
		return toolDefinitionRef{}, false
	}
	name := decodeJSONPointerToken(parts[2])
	if name == "" {
		return toolDefinitionRef{}, false
	}
	return toolDefinitionRef{table: table, name: name}, true
}

func decodeJSONPointerToken(token string) string {
	token = strings.ReplaceAll(token, "~1", "/")
	token = strings.ReplaceAll(token, "~0", "~")
	return token
}

func forEachToolSchemaChild(schema map[string]any, includeDefinitionTables bool, visitor func(any)) {
	if rawProperties, ok := schema["properties"].(map[string]any); ok {
		for _, child := range rawProperties {
			visitor(child)
		}
	}
	for _, key := range []string{"items", "anyOf"} {
		if child, ok := schema[key]; ok {
			visitor(child)
		}
	}
	if child, ok := schema["additionalProperties"]; ok && !isBoolValue(child) {
		visitor(child)
	}
	if includeDefinitionTables {
		for _, key := range []string{"$defs", "definitions"} {
			definitions, ok := schema[key].(map[string]any)
			if !ok {
				continue
			}
			for _, child := range definitions {
				visitor(child)
			}
		}
	}
}

func stripToolSchemaDescriptions(value any) {
	switch typed := value.(type) {
	case map[string]any:
		delete(typed, "description")
		forEachToolSchemaChild(typed, true, func(child any) {
			stripToolSchemaDescriptions(child)
		})
	case []any:
		for _, child := range typed {
			stripToolSchemaDescriptions(child)
		}
	}
}

func dropToolSchemaDefinitions(schema map[string]any) {
	rewriteToolDefinitionRefsToEmptySchemas(schema)
	delete(schema, "$defs")
	delete(schema, "definitions")
}

func rewriteToolDefinitionRefsToEmptySchemas(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if rawRef, ok := typed["$ref"].(string); ok {
			if _, ok := parseLocalToolDefinitionRef(rawRef); ok {
				for key := range typed {
					delete(typed, key)
				}
				return
			}
		}
		forEachToolSchemaChild(typed, false, func(child any) {
			rewriteToolDefinitionRefsToEmptySchemas(child)
		})
	case []any:
		for _, child := range typed {
			rewriteToolDefinitionRefsToEmptySchemas(child)
		}
	}
}

func collapseDeepToolSchemaObjects(value any, depth int) {
	switch typed := value.(type) {
	case map[string]any:
		if depth >= maxCompactToolSchemaDepth && isComplexToolSchemaObject(typed) {
			for key := range typed {
				delete(typed, key)
			}
			return
		}

		forEachToolSchemaChild(typed, false, func(child any) {
			collapseDeepToolSchemaObjects(child, depth+1)
		})
	case []any:
		for _, child := range typed {
			collapseDeepToolSchemaObjects(child, depth)
		}
	}
}

func isBoolValue(value any) bool {
	_, ok := value.(bool)
	return ok
}

func isComplexToolSchemaObject(schema map[string]any) bool {
	for _, key := range []string{"properties", "items", "additionalProperties", "$ref", "anyOf"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}

func cloneToolDefinitionMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = cloneToolDefinitionValue(value)
	}
	return dst
}

func cloneToolDefinitionValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneToolDefinitionMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneToolDefinitionValue(item)
		}
		return cloned
	default:
		return value
	}
}

func (r *ToolRegistry) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(r.definitions))
	for _, def := range r.definitions {
		out = append(out, snapshotToolDefinition(def))
	}
	return out
}

func (r *ToolRegistry) ToolNames() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func (r *ToolRegistry) ToolCallReadOnly(name string) bool {
	if r == nil {
		return false
	}
	tool, ok := r.tools[strings.TrimSpace(name)]
	if !ok || isNilTool(tool) {
		return false
	}
	readOnly, ok := tool.(readOnlyToolCallSupport)
	return ok && readOnly.ReadOnlyToolCall()
}

func (r *ToolRegistry) ToolCallSupportsParallel(name string) bool {
	if r == nil {
		return false
	}
	tool, ok := r.tools[strings.TrimSpace(name)]
	if !ok || isNilTool(tool) {
		return false
	}
	parallel, ok := tool.(parallelToolCallSupport)
	return ok && parallel.SupportsParallelToolCalls()
}

func (r *ToolRegistry) RegistrationIssues() []ToolRegistrationIssue {
	if r == nil || len(r.issues) == 0 {
		return nil
	}
	out := make([]ToolRegistrationIssue, len(r.issues))
	copy(out, r.issues)
	return out
}

func (r *ToolRegistry) DefinitionsExcluding(disabled map[string]bool) []ToolDefinition {
	if len(disabled) == 0 {
		return r.Definitions()
	}
	out := make([]ToolDefinition, 0, len(r.definitions))
	for _, def := range r.definitions {
		if disabled[strings.TrimSpace(def.Name)] {
			continue
		}
		out = append(out, snapshotToolDefinition(def))
	}
	return out
}

func (r *ToolRegistry) Execute(ctx context.Context, name string, args string) (string, error) {
	result, err := r.ExecuteDetailed(ctx, name, args)
	if err != nil {
		return "", err
	}
	return result.DisplayText, nil
}

func (r *ToolRegistry) ExecuteDetailed(ctx context.Context, name string, args string) (ToolExecutionResult, error) {
	tool, ok := r.tools[name]
	if !ok {
		return ToolExecutionResult{}, fmt.Errorf("unknown tool: %s", name)
	}
	payload := map[string]any{}
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &payload); err != nil {
			return ToolExecutionResult{}, fmt.Errorf("%w: tool %s received invalid JSON: %v", ErrInvalidToolArgumentsJSON, name, err)
		}
	}
	hookState, err := runDefaultPreToolUseHook(ctx, tool, name, payload)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if len(hookState.updatedInput) > 0 {
		payload = cloneStringAnyMap(hookState.updatedInput)
	}
	if detailed, ok := tool.(detailedTool); ok {
		result, err := detailed.ExecuteDetailed(ctx, payload)
		result.DisplayText = strings.TrimSpace(result.DisplayText)
		result.ModelText = strings.TrimSpace(result.ModelText)
		result.ContentItems = normalizeToolContentItems(result.ContentItems)
		result.ModelContentItems = normalizeToolContentItems(result.ModelContentItems)
		result.Meta = mergeToolMetaMaps(defaultToolExecutionMeta(name, payload), result.Meta)
		result.Meta = applyDefaultToolUseHookMeta(result.Meta, hookState)
		result.Meta["success"] = err == nil
		if err != nil {
			result.Meta["error"] = err.Error()
		} else {
			result = runDefaultPostToolUseHook(ctx, tool, name, payload, result)
		}
		return result, err
	}
	out, err := tool.Execute(ctx, payload)
	meta := defaultToolExecutionMeta(name, payload)
	meta = applyDefaultToolUseHookMeta(meta, hookState)
	meta["success"] = err == nil
	if err != nil {
		meta["error"] = err.Error()
	}
	result := ToolExecutionResult{
		DisplayText: strings.TrimSpace(out),
		Meta:        meta,
	}
	if err == nil {
		result = runDefaultPostToolUseHook(ctx, tool, name, payload, result)
	}
	return result, err
}

type defaultToolUseHookState struct {
	enabled       bool
	updatedInput  HookPayload
	originalInput map[string]any
}

func runDefaultPreToolUseHook(ctx context.Context, tool Tool, name string, payload map[string]any) (defaultToolUseHookState, error) {
	ws, ok := defaultToolUseHookWorkspace(tool)
	if !ok {
		return defaultToolUseHookState{}, nil
	}
	originalInput := cloneStringAnyMap(payload)
	verdict, err := ws.Hook(ctx, HookPreToolUse, defaultToolUseHookPayload(ctx, ws, name, payload))
	if err != nil {
		return defaultToolUseHookState{}, err
	}
	if err := defaultToolUseHookVerdictError(verdict); err != nil {
		return defaultToolUseHookState{}, err
	}
	state := defaultToolUseHookState{enabled: true}
	if len(verdict.UpdatedInput) > 0 {
		state.updatedInput = cloneStringAnyMap(verdict.UpdatedInput)
		state.originalInput = originalInput
	}
	return state, nil
}

func runDefaultPostToolUseHook(ctx context.Context, tool Tool, name string, payload map[string]any, result ToolExecutionResult) ToolExecutionResult {
	ws, ok := defaultToolUseHookWorkspace(tool)
	if !ok {
		return result
	}
	hookPayload := defaultToolUseHookPayload(ctx, ws, name, payload)
	hookPayload["tool_response"] = defaultToolUseHookResponse(result)
	verdict, err := ws.Hook(ctx, HookPostToolUse, hookPayload)
	if result.Meta == nil {
		result.Meta = map[string]any{}
	}
	if err != nil {
		if feedback, ok := postToolUseHookFeedbackFromError(err); ok {
			return applyPostToolUseHookFeedback(result, feedback)
		}
		result.Meta["post_tool_use_hook_error"] = err.Error()
		return result
	}
	if len(verdict.ContextAdds) > 0 {
		result.Meta["post_tool_use_context_adds"] = append([]string(nil), verdict.ContextAdds...)
	}
	if feedback, ok := postToolUseHookFeedbackFromVerdict(verdict); ok {
		return applyPostToolUseHookFeedback(result, feedback)
	}
	return result
}

func postToolUseHookFeedbackFromError(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	text := strings.TrimSpace(err.Error())
	switch {
	case text == "hook denied":
		return "PostToolUse hook stopped execution", true
	case strings.HasPrefix(text, "hook denied:"):
		feedback := strings.TrimSpace(strings.TrimPrefix(text, "hook denied:"))
		if feedback == "" {
			feedback = "PostToolUse hook stopped execution"
		}
		return feedback, true
	default:
		return "", false
	}
}

func postToolUseHookFeedbackFromVerdict(verdict HookVerdict) (string, bool) {
	if feedback := strings.TrimSpace(verdict.DenyReason); feedback != "" {
		return feedback, true
	}
	if !verdict.Allow {
		return "PostToolUse hook stopped execution", true
	}
	return "", false
}

func applyPostToolUseHookFeedback(result ToolExecutionResult, feedback string) ToolExecutionResult {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		feedback = "PostToolUse hook stopped execution"
	}
	if result.Meta == nil {
		result.Meta = map[string]any{}
	}
	result.Meta["post_tool_use_hook_feedback"] = feedback
	result.Meta["post_tool_use_hook_stopped"] = true
	result.DisplayText = feedback
	result.ModelText = feedback
	result.ContentItems = nil
	result.ModelContentItems = nil
	return result
}

func defaultToolUseHookWorkspace(tool Tool) (Workspace, bool) {
	if tool == nil {
		return Workspace{}, false
	}
	if managed, ok := tool.(selfManagedToolUseHooks); ok && managed.managesDefaultToolUseHooks() {
		return Workspace{}, false
	}
	provider, ok := tool.(toolWorkspaceProvider)
	if !ok {
		return Workspace{}, false
	}
	return provider.hookWorkspace(), true
}

func defaultToolUseHookVerdictError(verdict HookVerdict) error {
	if strings.TrimSpace(verdict.DenyReason) != "" {
		return fmt.Errorf("hook denied: %s", strings.TrimSpace(verdict.DenyReason))
	}
	if !verdict.Allow {
		return fmt.Errorf("hook denied")
	}
	return nil
}

func defaultToolUseHookPayload(ctx context.Context, ws Workspace, name string, payload map[string]any) HookPayload {
	hookPayload := HookPayload{
		"tool_name":  strings.TrimSpace(name),
		"tool_kind":  "function",
		"tool_input": cloneStringAnyMap(payload),
	}
	if meta := toolCallHookMetadataFromContext(ctx); meta.ID != "" && (meta.Name == "" || meta.Name == strings.TrimSpace(name)) {
		hookPayload["tool_use_id"] = meta.ID
	}
	for key, value := range defaultToolExecutionMeta(name, payload) {
		if key == "tool_name" {
			continue
		}
		hookPayload[key] = value
	}
	paths := collectDefaultToolUseHookPaths(payload)
	hookPayload["file_tags"] = normalizedHookFileTagsForPaths(paths)
	if command := strings.TrimSpace(stringValue(payload, "command")); command != "" {
		hookPayload["risk_tags"] = hookCommandRiskTags(command)
	} else {
		hookPayload["risk_tags"] = []string{}
	}
	if workDir := strings.TrimSpace(workspaceEffectiveActiveRoot(ws, nil)); workDir != "" {
		hookPayload["work_dir"] = workDir
	}
	addEffectiveExecutionContextMetadata(hookPayload, ws, nil)
	return hookPayload
}

func collectDefaultToolUseHookPaths(payload map[string]any) []string {
	if payload == nil {
		return nil
	}
	paths := []string{}
	if path := strings.TrimSpace(stringValue(payload, "path")); path != "" {
		paths = append(paths, path)
	}
	paths = append(paths, stringSliceValue(payload, "paths")...)
	return uniqueStrings(paths)
}

func defaultToolUseHookResponse(result ToolExecutionResult) any {
	if text := strings.TrimSpace(toolExecutionModelText(result)); text != "" {
		return text
	}
	if len(result.ModelContentItems) > 0 {
		return result.ModelContentItems
	}
	if len(result.ContentItems) > 0 {
		return result.ContentItems
	}
	return strings.TrimSpace(result.DisplayText)
}

func applyDefaultToolUseHookMeta(meta map[string]any, state defaultToolUseHookState) map[string]any {
	if meta == nil {
		meta = map[string]any{}
	}
	if !state.enabled {
		return meta
	}
	if len(state.updatedInput) > 0 {
		meta["hook_rewritten"] = true
		meta["original_input"] = cloneStringAnyMap(state.originalInput)
	}
	return meta
}

func toolExecutionModelText(result ToolExecutionResult) string {
	if text := strings.TrimSpace(result.ModelText); text != "" {
		return text
	}
	return strings.TrimSpace(result.DisplayText)
}

func toolExecutionModelContentItems(result ToolExecutionResult) []ToolContentItem {
	if len(result.ModelContentItems) > 0 {
		return normalizeToolContentItems(result.ModelContentItems)
	}
	return normalizeToolContentItems(result.ContentItems)
}

func toolExecutionModelTextWithError(result ToolExecutionResult, err error) string {
	if err == nil {
		return toolExecutionModelText(result)
	}
	text := toolExecutionModelText(result)
	if text == "" {
		return err.Error()
	}
	return text + "\n\nERROR: " + err.Error()
}

func mergeToolMetaMaps(base map[string]any, extra map[string]any) map[string]any {
	merged := cloneMetaMap(base)
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func defaultToolExecutionMeta(name string, payload map[string]any) map[string]any {
	meta := map[string]any{
		"tool_name": strings.TrimSpace(name),
		"effect":    inferToolExecutionEffect(name),
	}
	if path := strings.TrimSpace(stringValue(payload, "path")); path != "" {
		meta["path"] = path
	}
	if paths := normalizeTaskStateList(stringSliceValue(payload, "paths"), 16); len(paths) > 0 {
		meta["paths"] = paths
	}
	if command := strings.TrimSpace(stringValue(payload, "command")); command != "" {
		meta["command"] = command
	}
	if ownerNodeID := strings.TrimSpace(stringValue(payload, "owner_node_id")); ownerNodeID != "" {
		meta["owner_node_id"] = ownerNodeID
	}
	if pattern := strings.TrimSpace(stringValue(payload, "pattern")); pattern != "" {
		meta["pattern"] = pattern
	}
	if len(normalizeTaskStateList(stringSliceValue(payload, "commands"), 8)) > 0 {
		meta["commands"] = normalizeTaskStateList(stringSliceValue(payload, "commands"), 8)
	}
	if glob := strings.TrimSpace(stringValue(payload, "glob")); glob != "" {
		meta["glob"] = glob
	}
	return meta
}

func inferToolExecutionEffect(name string) string {
	switch strings.TrimSpace(name) {
	case "list_files", "read_file", "grep", "git_status", "git_diff":
		return "inspect"
	case "get_goal":
		return "inspect"
	case "apply_edit_proposal", "write_file", "replace_in_file", "apply_patch":
		return "edit"
	case "update_plan", "create_goal", "update_goal":
		return "plan"
	case "run_shell", "check_shell_job", "check_shell_bundle", "run_shell_background", "run_shell_bundle_background", "cancel_shell_job", "cancel_shell_bundle":
		return "execute"
	case "git_add", "git_commit", "git_push", "git_create_pr":
		return "git_mutation"
	default:
		return "task"
	}
}

func textLineCount(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n"))
}

func countListedEntries(text string) int {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	if normalized == "" || normalized == "(no files found)" {
		return 0
	}
	count := 0
	for _, line := range strings.Split(normalized, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

type Workspace struct {
	BaseRoot              string
	Root                  string
	Shell                 string
	ShellTimeout          time.Duration
	ReadHintSpans         int
	ReadCacheEntries      int
	VerificationToolPaths map[string]string
	ToolHints             *ToolHints
	Perms                 *PermissionManager
	UserInputRequests     *UserInputRequestTracker
	PrepareEdit           func(string) error
	PrepareEditAtRoot     func(string, string) error
	ReviewEdit            func(context.Context, EditPreview) error
	ReportProgress        func(string)
	CurrentSelection      func() *ViewerSelection
	PreviewEdit           func(EditPreview) (bool, error)
	ConfirmVerification   func(VerificationPlan) (bool, error)
	UpdatePlan            func([]PlanItem)
	GetPlan               func() []PlanItem
	RunHook               func(context.Context, HookEvent, HookPayload) (HookVerdict, error)
	BackgroundJobs        *BackgroundJobManager
	ResolveEditTarget     func(EditRoutingRequest) (EditRoutingResult, error)
	ResolveShellRoot      func(string) (ShellRoutingResult, error)
	GoalSession           *Session
	GoalStore             *SessionStore
}

type EditPreview struct {
	Title     string
	Preview   string
	Paths     []string
	Operation string
	Proposals []EditProposal
}

type ToolHints struct {
	mu              sync.Mutex
	recentReadSpans []readSpanHint
	maxReadSpans    int
}

type readSpanHint struct {
	path            string
	startLine       int
	endLine         int
	modTimeUnixNano int64
	size            int64
}

func (w Workspace) Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	abs, err := w.resolveAgainstRoot(w.Root, path)
	if err != nil {
		return "", err
	}
	return w.ensureWithinBaseRoot(path, abs)
}

func (w Workspace) resolveEditFallback(req EditRoutingRequest) (EditRoutingResult, error) {
	req = req.normalized()
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = "."
	}
	if !req.lookupIntent() {
		abs, err := w.resolveAgainstRoot(w.Root, path)
		if err != nil {
			return EditRoutingResult{}, err
		}
		abs, err = w.ensureWithinBaseRoot(path, abs)
		if err != nil {
			return EditRoutingResult{}, err
		}
		if req.AllowBaseFallback && !w.pathLooksAbsoluteForLookup(path) && !sameFilePath(w.Root, w.BaseRoot) {
			if _, err := os.Stat(abs); err == nil {
				return EditRoutingResult{
					AbsolutePath: abs,
					DisplayRoot:  w.Root,
					OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
				}, nil
			} else if err != nil && !os.IsNotExist(err) {
				return EditRoutingResult{}, err
			}
			fallback, err := w.resolveAgainstRoot(w.BaseRoot, path)
			if err != nil {
				return EditRoutingResult{}, err
			}
			fallback, err = ensurePathWithinRoot(path, w.BaseRoot, fallback)
			if err != nil {
				return EditRoutingResult{}, err
			}
			if _, err := os.Stat(fallback); err == nil {
				return EditRoutingResult{
					AbsolutePath: fallback,
					DisplayRoot:  w.BaseRoot,
					OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
				}, nil
			} else if err != nil && !os.IsNotExist(err) {
				return EditRoutingResult{}, err
			}
		}
		return EditRoutingResult{
			AbsolutePath: abs,
			DisplayRoot:  w.Root,
			OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
		}, nil
	}
	primary, err := w.Resolve(path)
	if err != nil {
		return EditRoutingResult{}, err
	}
	activeRoot := w.Root
	if strings.TrimSpace(activeRoot) == "" {
		activeRoot = w.BaseRoot
	}
	primary, err = ensureResolvedPathWithinRoot(activeRoot, primary)
	if err != nil {
		return EditRoutingResult{}, err
	}
	if w.pathLooksAbsoluteForLookup(path) || sameFilePath(w.Root, w.BaseRoot) {
		return EditRoutingResult{
			AbsolutePath: primary,
			DisplayRoot:  w.Root,
			OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
		}, nil
	}
	if _, err := os.Stat(primary); err == nil {
		return EditRoutingResult{
			AbsolutePath: primary,
			DisplayRoot:  w.Root,
			OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
		}, nil
	} else if !os.IsNotExist(err) {
		return EditRoutingResult{}, err
	}
	fallback, err := w.resolveAgainstRoot(w.BaseRoot, path)
	if err != nil {
		return EditRoutingResult{}, err
	}
	if _, err := os.Stat(fallback); err == nil {
		fallback, err = ensureResolvedPathWithinRoot(w.BaseRoot, fallback)
		if err != nil {
			return EditRoutingResult{}, err
		}
		return EditRoutingResult{
			AbsolutePath: fallback,
			DisplayRoot:  w.BaseRoot,
			OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
		}, nil
	} else if err != nil && !os.IsNotExist(err) {
		return EditRoutingResult{}, err
	}
	return EditRoutingResult{
		AbsolutePath: primary,
		DisplayRoot:  w.Root,
		OwnerNodeID:  strings.TrimSpace(req.OwnerNodeID),
	}, nil
}

func (w Workspace) ResolveEditPath(path string, ownerNodeID string, forLookup bool) (EditRoutingResult, error) {
	req := EditRoutingRequest{
		Path:        path,
		OwnerNodeID: ownerNodeID,
		ForLookup:   forLookup,
		Intent:      editRoutingIntentForLookup(forLookup),
	}
	if w.ResolveEditTarget != nil {
		return w.ResolveEditTarget(req)
	}
	return w.resolveEditFallback(req)
}

func (w Workspace) ResolveLookupPath(path string, ownerNodeID string) (EditRoutingResult, error) {
	req := EditRoutingRequest{
		Path:        path,
		OwnerNodeID: ownerNodeID,
		ForLookup:   true,
		Intent:      editRoutingIntentLookup,
	}
	route, err := w.ResolveEditPathWithOptions(req)
	if err == nil {
		if fallback, ok := w.lookupFallbackForMissingOwnedWorktreeRoute(req, route); ok {
			return fallback, nil
		}
		return route, nil
	}
	if strings.TrimSpace(ownerNodeID) == "" || !isLookupOwnerFallbackError(err) {
		return EditRoutingResult{}, err
	}
	req.OwnerNodeID = ""
	fallback, fallbackErr := w.ResolveEditPathWithOptions(req)
	if fallbackErr != nil {
		if baseFallback, ok := w.resolveLookupAcrossWorkspaceRoots(path); ok {
			return baseFallback, nil
		}
		return EditRoutingResult{}, err
	}
	return fallback, nil
}

func (w Workspace) resolveLookupAcrossWorkspaceRoots(path string) (EditRoutingResult, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	type candidate struct {
		root string
		abs  string
	}
	candidates := make([]candidate, 0, 2)
	seenRoots := map[string]struct{}{}
	for _, root := range []string{w.Root, w.BaseRoot} {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		cleanRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		key := strings.ToLower(filepath.Clean(cleanRoot))
		if _, ok := seenRoots[key]; ok {
			continue
		}
		seenRoots[key] = struct{}{}
		abs, err := w.resolveAgainstRoot(root, path)
		if err != nil {
			continue
		}
		abs, err = ensureResolvedPathWithinRoot(root, abs)
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{root: root, abs: abs})
	}
	if len(candidates) == 0 {
		return EditRoutingResult{}, false
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate.abs); err == nil {
			return EditRoutingResult{
				AbsolutePath: candidate.abs,
				DisplayRoot:  candidate.root,
			}, true
		} else if err != nil && !os.IsNotExist(err) {
			return EditRoutingResult{}, false
		}
	}
	return EditRoutingResult{
		AbsolutePath: candidates[0].abs,
		DisplayRoot:  candidates[0].root,
	}, true
}

func (w Workspace) lookupFallbackForMissingOwnedWorktreeRoute(req EditRoutingRequest, route EditRoutingResult) (EditRoutingResult, bool) {
	if strings.TrimSpace(req.OwnerNodeID) == "" || strings.TrimSpace(route.WorktreeRoot) == "" {
		return EditRoutingResult{}, false
	}
	if strings.TrimSpace(route.AbsolutePath) == "" {
		return EditRoutingResult{}, false
	}
	if _, err := os.Stat(route.AbsolutePath); err == nil {
		return EditRoutingResult{}, false
	} else if !os.IsNotExist(err) {
		return EditRoutingResult{}, false
	}
	fallbackReq := req
	fallbackReq.OwnerNodeID = ""
	fallback, err := w.ResolveEditPathWithOptions(fallbackReq)
	if err != nil {
		return EditRoutingResult{}, false
	}
	if _, err := os.Stat(fallback.AbsolutePath); err != nil {
		return EditRoutingResult{}, false
	}
	return fallback, true
}

func isEditTargetMismatchError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrEditTargetMismatch) {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "edit target mismatch") ||
		strings.Contains(text, "outside editable ownership")
}

func isLookupOwnerFallbackError(err error) bool {
	if err == nil {
		return false
	}
	if isEditTargetMismatchError(err) || errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "cannot find path") ||
		strings.Contains(text, "no such file") ||
		strings.Contains(text, "system cannot find")
}

func (w Workspace) ResolveEditPathWithOptions(req EditRoutingRequest) (EditRoutingResult, error) {
	req = req.normalized()
	if w.ResolveEditTarget != nil {
		return w.ResolveEditTarget(req)
	}
	return w.resolveEditFallback(req)
}

func (w Workspace) ResolveShellWorkingDir(ownerNodeID string) (ShellRoutingResult, error) {
	if w.ResolveShellRoot != nil {
		return w.ResolveShellRoot(ownerNodeID)
	}
	return ShellRoutingResult{
		Root:        firstNonBlankString(w.Root, w.BaseRoot),
		OwnerNodeID: strings.TrimSpace(ownerNodeID),
	}, nil
}

func (w Workspace) ResolveShellWorkDir(ownerNodeID string, workdir string) (ShellRoutingResult, string, error) {
	route, err := w.ResolveShellWorkingDir(ownerNodeID)
	if err != nil {
		return ShellRoutingResult{}, "", err
	}
	root := firstNonBlankString(route.Root, w.Root, w.BaseRoot)
	if strings.TrimSpace(workdir) == "" {
		return route, root, nil
	}
	resolved, err := w.resolveAgainstRoot(root, filepath.FromSlash(strings.TrimSpace(workdir)))
	if err != nil {
		return ShellRoutingResult{}, "", err
	}
	resolved, err = ensurePathWithinRoot(workdir, root, resolved)
	if err != nil {
		return ShellRoutingResult{}, "", err
	}
	return route, resolved, nil
}

func (w Workspace) ConfirmVerificationPlan(plan VerificationPlan) (bool, error) {
	return w.ConfirmVerificationPlanWithContext(context.Background(), plan)
}

func (w Workspace) ConfirmVerificationPlanWithContext(ctx context.Context, plan VerificationPlan) (bool, error) {
	if allowed, decided, message, err := w.permissionRequestHookNoCachedPermission(ctx, ActionShell, verificationPlanPermissionDetail(plan), verificationPlanPermissionInput(plan)); err != nil {
		return false, err
	} else if decided {
		if allowed {
			return true, nil
		}
		if strings.TrimSpace(message) != "" {
			return false, fmt.Errorf("verification permission denied: %s", message)
		}
		return false, fmt.Errorf("verification permission denied by hook")
	}
	if w.ConfirmVerification == nil {
		return true, nil
	}
	if w.UserInputRequests != nil {
		w.UserInputRequests.MarkRequested()
	}
	return w.ConfirmVerification(plan)
}

func verificationPlanPermissionDetail(plan VerificationPlan) string {
	var commands []string
	for _, step := range plan.Steps {
		if command := strings.TrimSpace(step.Command); command != "" {
			commands = append(commands, command)
		}
	}
	if len(commands) == 0 {
		return "verification"
	}
	if len(commands) == 1 {
		return commands[0]
	}
	return fmt.Sprintf("verification bundle with %d command(s): %s", len(commands), strings.Join(commands, "; "))
}

func verificationPlanPermissionInput(plan VerificationPlan) HookPayload {
	var commands []string
	for _, step := range plan.Steps {
		if command := strings.TrimSpace(step.Command); command != "" {
			commands = append(commands, command)
		}
	}
	commandText := strings.Join(commands, "\n")
	return HookPayload{
		"command":                 commandText,
		"risk_tags":               hookCommandRiskTags(commandText),
		"verification":            true,
		"verification_mode":       string(plan.Mode),
		"verification_step_count": len(plan.Steps),
		"changed_files":           append([]string(nil), plan.ChangedPaths...),
	}
}

func (w Workspace) toolHints() *ToolHints {
	if w.ToolHints != nil {
		return w.ToolHints
	}
	return nil
}

func (w Workspace) defaultReadHintSpans() int {
	if w.ReadHintSpans > 0 {
		return w.ReadHintSpans
	}
	return defaultReadHintSpans
}

func (w Workspace) defaultReadCacheEntries() int {
	if w.ReadCacheEntries > 0 {
		return w.ReadCacheEntries
	}
	return defaultReadCacheEntries
}

func (h *ToolHints) rememberReadSpan(span readSpanHint) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.maxReadSpans <= 0 {
		h.maxReadSpans = defaultReadHintSpans
	}
	filtered := h.recentReadSpans[:0]
	for _, existing := range h.recentReadSpans {
		if existing.path == span.path && existing.startLine == span.startLine && existing.endLine == span.endLine &&
			existing.modTimeUnixNano == span.modTimeUnixNano && existing.size == span.size {
			continue
		}
		filtered = append(filtered, existing)
	}
	h.recentReadSpans = append(filtered, span)
	if len(h.recentReadSpans) > h.maxReadSpans {
		h.recentReadSpans = h.recentReadSpans[len(h.recentReadSpans)-h.maxReadSpans:]
	}
}

func (h *ToolHints) readCacheHint(path string, lineNo int, info fs.FileInfo) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	best := ""
	bestDistance := 0
	for i := len(h.recentReadSpans) - 1; i >= 0; i-- {
		span := h.recentReadSpans[i]
		if span.path != path {
			continue
		}
		if span.modTimeUnixNano != info.ModTime().UnixNano() || span.size != info.Size() {
			continue
		}
		if lineNo >= span.startLine && lineNo <= span.endLine {
			return "[cached-nearby:inside]"
		}
		distance := 0
		if lineNo < span.startLine {
			distance = span.startLine - lineNo
		} else {
			distance = lineNo - span.endLine
		}
		if distance > 12 {
			continue
		}
		if best == "" || distance < bestDistance {
			bestDistance = distance
			best = fmt.Sprintf("[cached-nearby:%d]", distance)
		}
	}
	return best
}

func (w Workspace) ResolveForLookup(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	primary, err := w.Resolve(path)
	if err != nil {
		return "", err
	}
	activeRoot := w.Root
	if strings.TrimSpace(activeRoot) == "" {
		activeRoot = w.BaseRoot
	}
	primary, err = ensureResolvedPathWithinRoot(activeRoot, primary)
	if err != nil {
		return "", err
	}
	if w.pathLooksAbsoluteForLookup(path) || sameFilePath(w.Root, w.BaseRoot) {
		return primary, nil
	}
	if _, err := os.Stat(primary); err == nil {
		return primary, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	fallback, err := w.resolveAgainstRoot(w.BaseRoot, path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(fallback); err == nil {
		fallback, err = ensureResolvedPathWithinRoot(w.BaseRoot, fallback)
		if err != nil {
			return "", err
		}
		return fallback, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return primary, nil
}

func (w Workspace) ResolveForActiveLookup(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	activeRoot := strings.TrimSpace(w.Root)
	if activeRoot == "" {
		activeRoot = strings.TrimSpace(w.BaseRoot)
	}
	abs, err := w.resolveAgainstRoot(activeRoot, path)
	if err != nil {
		return "", err
	}
	return ensureResolvedPathWithinRoot(activeRoot, abs)
}

func (w Workspace) resolveAgainstRoot(root, path string) (string, error) {
	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else if resolved, ok := w.resolveWindowsVolumeRootedPath(path); ok {
		abs = resolved
	} else {
		base := root
		if strings.TrimSpace(base) == "" {
			base = w.Root
		}
		abs = filepath.Clean(filepath.Join(base, path))
	}
	return abs, nil
}

func (w Workspace) pathLooksAbsoluteForLookup(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	_, ok := w.resolveWindowsVolumeRootedPath(path)
	return ok
}

func (w Workspace) resolveWindowsVolumeRootedPath(path string) (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || filepath.IsAbs(trimmed) {
		return "", false
	}
	if (!strings.HasPrefix(trimmed, "/") && !strings.HasPrefix(trimmed, `\`)) ||
		strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, `\\`) {
		return "", false
	}
	volume := workspacePathVolume(w.Root)
	if volume == "" {
		volume = workspacePathVolume(w.BaseRoot)
	}
	if volume == "" {
		return "", false
	}
	relative := strings.TrimLeft(trimmed, `/\`)
	if relative == "" {
		return filepath.Clean(volume + string(filepath.Separator)), true
	}
	return filepath.Clean(volume + string(filepath.Separator) + filepath.FromSlash(relative)), true
}

func workspacePathVolume(path string) string {
	candidate := strings.TrimSpace(path)
	if candidate == "" {
		return ""
	}
	if volume := filepath.VolumeName(candidate); volume != "" {
		return volume
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return ""
	}
	return filepath.VolumeName(abs)
}

func (w Workspace) ensureWithinBaseRoot(originalPath, abs string) (string, error) {
	activeRoot := w.Root
	if strings.TrimSpace(activeRoot) == "" {
		activeRoot = w.BaseRoot
	}
	return ensurePathWithinRoot(originalPath, activeRoot, abs)
}

func sameFilePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	left, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	right, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	leftInfo, leftStatErr := os.Stat(left)
	rightInfo, rightStatErr := os.Stat(right)
	if leftStatErr == nil && rightStatErr == nil {
		return os.SameFile(leftInfo, rightInfo)
	}
	return sameCleanPathForOS(left, right)
}

func sameCleanPathForOS(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if workspacePathsAreCaseInsensitiveByDefault() {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func workspacePathsAreCaseInsensitiveByDefault() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

func (w Workspace) CheckEditBoundary(path string) error {
	if err := w.ensureProtectedEditPath(path); err != nil {
		return err
	}
	if err := w.ensureResolvedWritePathWithinRoot(path); err != nil {
		return err
	}
	return nil
}

func (w Workspace) EnsureWrite(path string) error {
	return w.EnsureWriteWithContext(context.Background(), path)
}

func (w Workspace) EnsureWriteWithContext(ctx context.Context, path string) error {
	if err := w.CheckEditBoundary(path); err != nil {
		return err
	}
	if w.Perms == nil {
		return nil
	}
	if allowed, decided, message, err := w.permissionRequestHook(ctx, ActionWrite, path, HookPayload{
		"path":          relOrAbs(firstNonBlankString(w.Root, w.BaseRoot), path),
		"absolute_path": path,
		"file_tags":     hookFileTags(path),
	}); err != nil {
		return fmt.Errorf("%w: write approval hook failed for %s: %v", ErrWriteDenied, path, err)
	} else if decided {
		if allowed {
			return nil
		}
		if strings.TrimSpace(message) != "" {
			return fmt.Errorf("%w: write approval denied for %s: %s", ErrWriteDenied, path, message)
		}
		return fmt.Errorf("%w: write approval denied by hook for %s", ErrWriteDenied, path)
	}
	ok, err := w.Perms.Allow(ActionWrite, path)
	if err != nil {
		return fmt.Errorf("%w: write approval unavailable for %s: %v", ErrWriteDenied, path, err)
	}
	if !ok {
		return fmt.Errorf("%w: user denied write approval for %s", ErrWriteDenied, path)
	}
	return nil
}

func (w Workspace) ensureResolvedWritePathWithinRoot(path string) error {
	roots := normalizeTaskStateList([]string{w.Root, w.BaseRoot}, 2)
	if len(roots) == 0 {
		return nil
	}
	var rootAbsList []string
	for _, root := range roots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		if resolvedRoot, err := filepath.EvalSymlinks(rootAbs); err == nil {
			rootAbs = resolvedRoot
		}
		rootAbsList = append(rootAbsList, rootAbs)
	}
	targetAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	checkPath := targetAbs
	for {
		resolved, err := filepath.EvalSymlinks(checkPath)
		if err == nil {
			resolvedAbs, absErr := filepath.Abs(resolved)
			if absErr != nil {
				return absErr
			}
			if !pathWithinAnyRoot(rootAbsList, resolvedAbs) {
				return fmt.Errorf("%w: refusing to write through a path that resolves outside the active workspace root: %s -> %s", ErrEditTargetMismatch, targetAbs, resolvedAbs)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath || strings.TrimSpace(parent) == "" {
			return nil
		}
		checkPath = parent
	}
}

func pathWithinAnyRoot(roots []string, path string) bool {
	for _, root := range roots {
		if pathWithinRoot(root, path) {
			return true
		}
	}
	return false
}

func pathWithinRoot(root string, path string) bool {
	rootClean := filepath.Clean(root)
	pathClean := filepath.Clean(path)
	rel, err := filepath.Rel(rootClean, pathClean)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func ensureResolvedPathWithinRoot(root string, target string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return target, nil
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolvedRoot, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolvedRoot
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	checkPath := targetAbs
	for {
		resolved, err := filepath.EvalSymlinks(checkPath)
		if err == nil {
			resolvedAbs, absErr := filepath.Abs(resolved)
			if absErr != nil {
				return "", absErr
			}
			if !pathWithinRoot(rootAbs, resolvedAbs) {
				return "", fmt.Errorf("path resolves outside the active workspace root: %s -> %s", targetAbs, resolvedAbs)
			}
			return targetAbs, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath || strings.TrimSpace(parent) == "" {
			return targetAbs, nil
		}
		checkPath = parent
	}
}

func (w Workspace) EnsureGit(detail string) error {
	return w.EnsureGitWithContext(context.Background(), detail)
}

func (w Workspace) EnsureGitWithContext(ctx context.Context, detail string) error {
	if w.Perms == nil {
		return nil
	}
	if allowed, decided, message, err := w.permissionRequestHook(ctx, ActionGit, detail, HookPayload{
		"command": "git " + strings.TrimSpace(detail),
		"detail":  detail,
	}); err != nil {
		return err
	} else if decided {
		if allowed {
			return nil
		}
		if strings.TrimSpace(message) != "" {
			return fmt.Errorf("git permission denied: %s", message)
		}
		return fmt.Errorf("git permission denied by hook")
	}
	ok, err := w.Perms.Allow(ActionGit, detail)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("git permission denied")
	}
	return nil
}

func (w Workspace) ensureProtectedEditPath(path string) error {
	targetAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(w.Root)
	if err != nil {
		return err
	}
	targetScope, targetProtected := protectedWorktreeScope(targetAbs)
	if !targetProtected {
		return nil
	}
	rootScope, rootProtected := protectedWorktreeScope(rootAbs)
	if rootProtected && sameCleanPathForOS(rootScope, targetScope) {
		return nil
	}
	return fmt.Errorf("%w: refusing to edit nested worktree-managed path outside the active workspace root: %s", ErrEditTargetMismatch, targetAbs)
}

func protectedWorktreeScope(path string) (string, bool) {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	trimmed := strings.TrimPrefix(cleaned, volume)
	trimmed = strings.TrimLeft(trimmed, string(filepath.Separator))
	parts := strings.Split(trimmed, string(filepath.Separator))
	for i := 0; i+1 < len(parts); i++ {
		first := strings.ToLower(strings.TrimSpace(parts[i]))
		second := strings.ToLower(strings.TrimSpace(parts[i+1]))
		if (first == ".claude" || first == ".git") && second == "worktrees" {
			scopeEnd := i + 2
			if i+2 < len(parts) {
				scopeEnd = i + 3
			}
			scopeParts := parts[:scopeEnd]
			prefix := filepath.Join(scopeParts...)
			if volume != "" {
				prefix = volume + string(filepath.Separator) + prefix
			} else {
				prefix = string(filepath.Separator) + prefix
			}
			return filepath.Clean(prefix), true
		}
	}
	return "", false
}

func (w Workspace) EnsureShell(command string) error {
	return w.EnsureShellWithContext(context.Background(), command)
}

func (w Workspace) EnsureShellWithContext(ctx context.Context, command string) error {
	if w.Perms == nil {
		return nil
	}
	if allowed, decided, message, err := w.permissionRequestHook(ctx, ActionShell, command, HookPayload{
		"command":   command,
		"risk_tags": hookCommandRiskTags(command),
	}); err != nil {
		return err
	} else if decided {
		if allowed {
			return nil
		}
		if strings.TrimSpace(message) != "" {
			return fmt.Errorf("shell permission denied: %s", message)
		}
		return fmt.Errorf("shell permission denied by hook")
	}
	ok, err := w.Perms.Allow(ActionShell, command)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("shell permission denied")
	}
	return nil
}

func (w Workspace) permissionRequestHook(ctx context.Context, action Action, detail string, toolInput HookPayload) (bool, bool, string, error) {
	return w.permissionRequestHookWithOptions(ctx, action, detail, toolInput, true)
}

func (w Workspace) permissionRequestHookNoCachedPermission(ctx context.Context, action Action, detail string, toolInput HookPayload) (bool, bool, string, error) {
	return w.permissionRequestHookWithOptions(ctx, action, detail, toolInput, false)
}

func (w Workspace) permissionRequestHookWithOptions(ctx context.Context, action Action, detail string, toolInput HookPayload, useCachedPermission bool) (bool, bool, string, error) {
	if w.RunHook == nil {
		return false, false, "", nil
	}
	if useCachedPermission && w.Perms != nil {
		if allowed, decided, err := w.Perms.allowWithoutPrompt(action); decided || err != nil {
			if err != nil {
				return allowed, true, err.Error(), nil
			}
			return allowed, decided, "", nil
		}
	}
	if useCachedPermission && w.Perms == nil {
		return false, false, "", nil
	}
	if !useCachedPermission && w.Perms != nil {
		if _, _, err := w.Perms.allowWithoutPrompt(action); err != nil {
			return false, true, err.Error(), nil
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	input := HookPayload{}
	for key, value := range toolInput {
		if strings.TrimSpace(key) != "" {
			input[key] = value
		}
	}
	toolName := permissionRequestToolName(action)
	toolKind := permissionRequestToolKind(action)
	payload := HookPayload{
		"tool_name":  toolName,
		"tool_kind":  toolKind,
		"tool_input": input,
		"action":     string(action),
		"permission": string(action),
		"detail":     strings.TrimSpace(detail),
	}
	for key, value := range input {
		if _, exists := payload[key]; !exists {
			payload[key] = value
		}
	}
	verdict, err := w.Hook(ctx, HookPermissionRequest, payload)
	if err != nil {
		return false, false, "", err
	}
	switch strings.ToLower(strings.TrimSpace(verdict.PermissionDecision)) {
	case "":
		return false, false, "", nil
	case "allow":
		return true, true, "", nil
	case "deny":
		return false, true, strings.TrimSpace(verdict.PermissionMessage), nil
	default:
		return false, false, "", fmt.Errorf("unsupported permission request hook decision: %s", verdict.PermissionDecision)
	}
}

func permissionRequestToolName(action Action) string {
	switch action {
	case ActionShell, ActionShellWrite:
		return "run_shell"
	case ActionGit:
		return "git"
	case ActionWrite:
		return "write"
	default:
		return string(action)
	}
}

func permissionRequestToolKind(action Action) string {
	switch action {
	case ActionShell, ActionShellWrite:
		return "shell"
	case ActionGit:
		return "git"
	case ActionWrite:
		return "edit"
	default:
		return string(action)
	}
}

func (w Workspace) defaultShellTimeout() time.Duration {
	if w.ShellTimeout > 0 {
		return w.ShellTimeout
	}
	return time.Duration(currentDefaultShellTimeoutSecs) * time.Second
}

func (w Workspace) ConfirmEdit(preview EditPreview) error {
	if w.PreviewEdit == nil {
		return nil
	}
	if w.UserInputRequests != nil {
		w.UserInputRequests.MarkRequested()
	}
	ok, err := w.PreviewEdit(preview)
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) || errors.Is(err, io.EOF) {
			return ErrEditCanceled
		}
		return err
	}
	if !ok {
		return ErrEditCanceled
	}
	return nil
}

func (w Workspace) ReviewProposedEdit(ctx context.Context, preview EditPreview) error {
	if w.ReviewEdit == nil {
		return nil
	}
	return w.ReviewEdit(ctx, preview)
}

func (w Workspace) BeforeEdit(reason string) error {
	if w.PrepareEdit == nil {
		return nil
	}
	return w.PrepareEdit(reason)
}

func (w Workspace) BeforeEditForRoot(reason string, root string) error {
	if w.PrepareEditAtRoot != nil {
		return w.PrepareEditAtRoot(reason, root)
	}
	return w.BeforeEdit(reason)
}

func (w Workspace) Progress(message string) {
	if w.ReportProgress == nil {
		return
	}
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return
	}
	w.ReportProgress(trimmed)
}

func (w Workspace) Selection() *ViewerSelection {
	if w.CurrentSelection == nil {
		return nil
	}
	return w.CurrentSelection()
}

func (w Workspace) Hook(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
	if w.RunHook == nil {
		return HookVerdict{Allow: true}, nil
	}
	if payload == nil {
		payload = HookPayload{}
	}
	enrichHookPayloadFromContext(ctx, payload)
	if strings.TrimSpace(stringsValueFromAny(payload["event"])) == "" {
		payload["event"] = string(event)
	}
	if strings.TrimSpace(stringsValueFromAny(payload["hook_event_name"])) == "" {
		payload["hook_event_name"] = string(event)
	}
	enrichHookSubagentIdentity(payload)
	return w.RunHook(ctx, event, payload)
}

func enrichHookPayloadFromContext(ctx context.Context, payload HookPayload) {
	if ctx == nil || payload == nil {
		return
	}
	metadata, ok := ctx.Value(mcpTurnMetadataContextKey{}).(map[string]any)
	if !ok || len(metadata) == 0 {
		return
	}
	for _, key := range []string{
		"session_id",
		"thread_id",
		"turn_id",
		"trace_id",
		"thread_source",
		"provider",
		"model",
		"reasoning_effort",
		"permission_mode",
		"active_permission_profile_id",
		"active_permission_profile",
		"sandbox",
		"cwd",
		"workspace_root",
		"active_workspace_root",
		"workspace_roots",
		"workspaces",
	} {
		if _, exists := payload[key]; exists {
			continue
		}
		if value, ok := metadata[key]; ok {
			payload[key] = value
		}
	}
}

func firstLine(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if trimmed == "" {
		return ""
	}
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
}

func shellInvocation(shell, command string) (string, []string) {
	base := strings.ToLower(strings.TrimSpace(shell))
	switch {
	case strings.Contains(base, "powershell") || strings.Contains(base, "pwsh"):
		wrapped := "[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new(); $OutputEncoding=[System.Text.UTF8Encoding]::new(); " + command
		return shell, []string{"-NoProfile", "-Command", wrapped}
	case base == "cmd":
		return "cmd", []string{"/C", command}
	case base == "bash":
		return "bash", []string{"-lc", command}
	case base == "sh":
		return "sh", []string{"-lc", command}
	default:
		if runtime.GOOS == "windows" {
			return "powershell", []string{"-NoProfile", "-Command", command}
		}
		return "sh", []string{"-lc", command}
	}
}

func relOrAbs(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	if rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func ensureParentDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func isText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	limit := len(data)
	if limit > 4096 {
		limit = 4096
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return false
		}
	}
	return true
}

type ListFilesTool struct{ ws Workspace }

var errListFilesMaxEntriesReached = errors.New("max entries reached")

func NewListFilesTool(ws Workspace) ListFilesTool { return ListFilesTool{ws: ws} }

func (t ListFilesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "list_files",
		Description: "List files and directories in the workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string"},
				"recursive":   map[string]any{"type": "boolean"},
				"max_entries": map[string]any{"type": "integer"},
			},
		},
	}
}

func (t ListFilesTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t ListFilesTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	ownerNodeID := stringValue(args, "owner_node_id")
	route, err := t.ws.ResolveLookupPath(stringValue(args, "path"), ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	root := route.AbsolutePath
	displayRoot := t.ws.Root
	if strings.TrimSpace(ownerNodeID) != "" {
		displayRoot = firstNonBlankString(route.DisplayRoot, route.WorktreeRoot, t.ws.Root)
	}
	recursive := boolValue(args, "recursive", false)
	maxEntries := intValue(args, "max_entries", 200)
	if maxEntries <= 0 {
		maxEntries = 200
	}
	if info, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			display, meta := buildMissingListFilesPathResult(displayRoot, root, recursive, maxEntries)
			return ToolExecutionResult{
				DisplayText: display,
				Meta:        meta,
			}, fmt.Errorf("list_files target does not exist: %s", relOrAbs(displayRoot, root))
		}
		return ToolExecutionResult{}, err
	} else if !info.IsDir() {
		displayPath := relOrAbs(displayRoot, root)
		return ToolExecutionResult{
			DisplayText: displayPath,
			Meta: map[string]any{
				"path":        displayPath,
				"path_type":   "file",
				"recursive":   recursive,
				"max_entries": maxEntries,
				"entry_count": 1,
				"truncated":   false,
			},
		}, nil
	}
	var lines []string
	truncated := false
	if recursive {
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if path == root {
				return nil
			}
			rel := relOrAbs(displayRoot, path)
			if d.IsDir() {
				rel += "/"
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
			}
			lines = append(lines, rel)
			if len(lines) >= maxEntries {
				truncated = true
				return errListFilesMaxEntriesReached
			}
			return nil
		})
		if err != nil && !errors.Is(err, errListFilesMaxEntriesReached) {
			return ToolExecutionResult{}, err
		}
	} else {
		entries, err := os.ReadDir(root)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		for index, entry := range entries {
			if index >= maxEntries {
				truncated = true
				break
			}
			rel := relOrAbs(displayRoot, filepath.Join(root, entry.Name()))
			if entry.IsDir() {
				rel += "/"
			}
			lines = append(lines, rel)
		}
	}
	text := "(no files found)"
	listedCount := len(lines)
	if len(lines) == 0 {
		return ToolExecutionResult{
			DisplayText: text,
			Meta: map[string]any{
				"path":        relOrAbs(displayRoot, root),
				"recursive":   recursive,
				"max_entries": maxEntries,
				"entry_count": 0,
				"truncated":   false,
			},
		}, nil
	}
	if truncated {
		lines = append(lines, fmt.Sprintf("... (truncated at %d entries; use max_entries or a narrower path to continue)", maxEntries))
	}
	text = strings.Join(lines, "\n")
	return ToolExecutionResult{
		DisplayText: text,
		Meta: map[string]any{
			"path":        relOrAbs(displayRoot, root),
			"recursive":   recursive,
			"max_entries": maxEntries,
			"entry_count": listedCount,
			"truncated":   truncated,
		},
	}, nil
}

func buildMissingListFilesPathResult(displayRoot string, path string, recursive bool, maxEntries int) (string, map[string]any) {
	resolvedPath := relOrAbs(displayRoot, path)
	candidatePaths, candidatesTruncated := findMissingReadFileCandidates(displayRoot, path, 16, 5000)
	parentPath := relOrAbs(displayRoot, filepath.Dir(path))
	parentExists := false
	parentEntryCount := 0
	if info, err := os.Stat(filepath.Dir(path)); err == nil && info.IsDir() {
		parentExists = true
		if entries, readErr := os.ReadDir(filepath.Dir(path)); readErr == nil {
			parentEntryCount = len(entries)
		}
	}
	lines := []string{
		"list_files target does not exist: " + resolvedPath,
		"",
		"Confirm the nearest existing parent directory, then retry list_files with that path.",
	}
	if parentExists {
		lines = append(lines, "Parent directory exists: "+parentPath)
	} else {
		lines = append(lines, "Parent directory does not exist: "+parentPath)
	}
	if len(candidatePaths) > 0 {
		lines = append(lines, "", "Candidate paths with the same filename:")
		for _, candidate := range candidatePaths {
			lines = append(lines, "- "+candidate)
		}
		if candidatesTruncated {
			lines = append(lines, "- ... (more candidates omitted)")
		}
	}
	return strings.Join(lines, "\n"), map[string]any{
		"path":                       resolvedPath,
		"path_type":                  "missing",
		"recursive":                  recursive,
		"max_entries":                maxEntries,
		"entry_count":                0,
		"truncated":                  false,
		"error_kind":                 "missing_path",
		"parent_path":                parentPath,
		"parent_exists":              parentExists,
		"parent_entry_count":         parentEntryCount,
		"candidate_paths":            candidatePaths,
		"candidate_search_truncated": candidatesTruncated,
	}
}

type readFileCacheEntry struct {
	path            string
	startLine       int
	endLine         int
	modTimeUnixNano int64
	size            int64
	renderedLines   []string
	output          string
}

type ReadFileTool struct {
	ws        Workspace
	mu        sync.Mutex
	cache     map[string]readFileCacheEntry
	cacheKeys []string
	maxCache  int
}

func NewReadFileTool(ws Workspace) *ReadFileTool {
	if ws.ToolHints == nil {
		ws.ToolHints = &ToolHints{maxReadSpans: ws.defaultReadHintSpans()}
	}
	return &ReadFileTool{
		ws:       ws,
		cache:    make(map[string]readFileCacheEntry),
		maxCache: ws.defaultReadCacheEntries(),
	}
}

func (t *ReadFileTool) sharedToolHintsMaxReadSpans() int {
	return t.ws.defaultReadHintSpans()
}

func (t *ReadFileTool) setSharedToolHints(hints *ToolHints) {
	if hints == nil {
		return
	}
	t.ws.ToolHints = hints
}

func (t *ReadFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "read_file",
		Description: "Read a file from the workspace. Supports line ranges.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string"},
				"start_line": map[string]any{"type": "integer"},
				"end_line":   map[string]any{"type": "integer"},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t *ReadFileTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	ownerNodeID := stringValue(args, "owner_node_id")
	route, err := t.ws.ResolveLookupPath(stringValue(args, "path"), ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	path := route.AbsolutePath
	displayRoot := t.ws.Root
	if strings.TrimSpace(ownerNodeID) != "" {
		displayRoot = firstNonBlankString(route.DisplayRoot, route.WorktreeRoot, t.ws.Root)
	}
	startArg := intValue(args, "start_line", 1)
	endArg := intValue(args, "end_line", 0)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			display, meta := buildMissingReadFileResult(displayRoot, path, startArg, endArg)
			return ToolExecutionResult{
				DisplayText: display,
				Meta:        meta,
			}, fmt.Errorf("read_file target does not exist: %s", relOrAbs(displayRoot, path))
		}
		return ToolExecutionResult{}, err
	}
	start := startArg
	end := endArg
	if start < 1 {
		start = 1
	}
	if end > 0 && end < start {
		_, normalizedEnd, err := readRenderedFileRange(ctx, path, start, end)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		display, meta := buildInvalidReadFileRangeResult(displayRoot, path, startArg, endArg, normalizedEnd)
		return ToolExecutionResult{
			DisplayText: display,
			Meta:        meta,
		}, nil
	}
	cacheKey := readFileCacheKey(path, startArg, endArg)
	if cached, ok := t.lookupCachedRead(cacheKey, info); ok {
		normalizedStart, normalizedEnd := normalizeRenderedRangeBounds(cached, startArg, endArg)
		return ToolExecutionResult{
			DisplayText: "NOTE: returning cached content for an unchanged read_file range.\n" + cached,
			Meta:        buildReadFileMeta(displayRoot, path, startArg, endArg, normalizedStart, normalizedEnd, cached, "exact"),
		}, nil
	}
	if covered, ok := t.lookupCoveredCachedRead(path, startArg, endArg, info); ok {
		normalizedStart, normalizedEnd := normalizeRenderedRangeBounds(covered, startArg, endArg)
		return ToolExecutionResult{
			DisplayText: "NOTE: returning content from a cached overlapping read_file range.\n" + covered,
			Meta:        buildReadFileMeta(displayRoot, path, startArg, endArg, normalizedStart, normalizedEnd, covered, "covered"),
		}, nil
	}
	if overlap, ok := t.lookupPartialOverlap(path, start, end, info); ok {
		renderedLines, normalizedEnd, readErr := readRenderedRangeWithCachedOverlap(ctx, path, start, end, overlap)
		if readErr != nil {
			return ToolExecutionResult{}, readErr
		}
		if start > normalizedEnd {
			display, meta := buildOutOfRangeReadFileResult(displayRoot, path, startArg, endArg, normalizedEnd)
			return ToolExecutionResult{
				DisplayText: display,
				Meta:        meta,
			}, nil
		}
		result := strings.Join(renderedLines, "\n")
		t.storeCachedRead(cacheKey, path, start, normalizedEnd, info, renderedLines, result)
		t.recordReadSpanHint(path, start, normalizedEnd, info)
		return ToolExecutionResult{
			DisplayText: "NOTE: returning content assembled from a cached partial overlap plus newly read lines.\n" + result,
			Meta:        buildReadFileMeta(displayRoot, path, startArg, endArg, start, normalizedEnd, result, "partial_overlap"),
		}, nil
	}
	renderedLines, normalizedEnd, err := readRenderedFileRange(ctx, path, start, end)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	if start > normalizedEnd {
		display, meta := buildOutOfRangeReadFileResult(displayRoot, path, startArg, endArg, normalizedEnd)
		return ToolExecutionResult{
			DisplayText: display,
			Meta:        meta,
		}, nil
	}
	result := strings.Join(renderedLines, "\n")
	t.storeCachedRead(cacheKey, path, start, normalizedEnd, info, renderedLines, result)
	t.recordReadSpanHint(path, start, normalizedEnd, info)
	return ToolExecutionResult{
		DisplayText: result,
		Meta:        buildReadFileMeta(displayRoot, path, startArg, endArg, start, normalizedEnd, result, "fresh"),
	}, nil
}

func buildReadFileMeta(root string, path string, requestedStart int, requestedEnd int, actualStart int, actualEnd int, output string, cacheMode string) map[string]any {
	lineCount := 0
	if actualEnd >= actualStart && actualStart > 0 {
		lineCount = actualEnd - actualStart + 1
	}
	resolvedPath := relOrAbs(root, path)
	return map[string]any{
		"path":           resolvedPath,
		"requested_path": resolvedPath,
		"start_line":     requestedStart,
		"end_line":       requestedEnd,
		"actual_start":   actualStart,
		"actual_end":     actualEnd,
		"line_count":     lineCount,
		"cache_mode":     cacheMode,
		"output_lines":   textLineCount(output),
	}
}

func buildOutOfRangeReadFileResult(root string, path string, requestedStart int, requestedEnd int, lineCount int) (string, map[string]any) {
	resolvedPath := relOrAbs(root, path)
	lines := []string{
		"read_file range is outside file: " + resolvedPath,
		fmt.Sprintf("Requested start_line: %d", requestedStart),
		fmt.Sprintf("Requested end_line: %d", requestedEnd),
		fmt.Sprintf("File line count: %d", lineCount),
		"Use a start_line within the file line count, or omit the range to read from the beginning.",
	}
	meta := map[string]any{
		"path":            resolvedPath,
		"requested_path":  resolvedPath,
		"start_line":      requestedStart,
		"end_line":        requestedEnd,
		"actual_start":    0,
		"actual_end":      lineCount,
		"line_count":      0,
		"file_line_count": lineCount,
		"cache_mode":      "out_of_range",
		"error_kind":      "range_out_of_bounds",
	}
	return strings.Join(lines, "\n"), meta
}

func buildInvalidReadFileRangeResult(root string, path string, requestedStart int, requestedEnd int, lineCount int) (string, map[string]any) {
	resolvedPath := relOrAbs(root, path)
	lines := []string{
		"read_file received an invalid line range: " + resolvedPath,
		fmt.Sprintf("Requested start_line: %d", requestedStart),
		fmt.Sprintf("Requested end_line: %d", requestedEnd),
		fmt.Sprintf("File line count: %d", lineCount),
		"Use end_line greater than or equal to start_line, or omit end_line to read through the file.",
	}
	meta := map[string]any{
		"path":            resolvedPath,
		"requested_path":  resolvedPath,
		"start_line":      requestedStart,
		"end_line":        requestedEnd,
		"actual_start":    0,
		"actual_end":      lineCount,
		"line_count":      0,
		"file_line_count": lineCount,
		"cache_mode":      "invalid_range",
		"error_kind":      "invalid_line_range",
	}
	return strings.Join(lines, "\n"), meta
}

func buildMissingReadFileResult(root string, path string, requestedStart int, requestedEnd int) (string, map[string]any) {
	resolvedPath := relOrAbs(root, path)
	parent := filepath.Dir(path)
	parentPath := relOrAbs(root, parent)
	lines := []string{
		"read_file target does not exist: " + resolvedPath,
		"Parent directory: " + parentPath,
	}
	parentExists := false
	parentEntryCount := 0

	entries, err := os.ReadDir(parent)
	switch {
	case err == nil:
		parentExists = true
		parentEntryCount = len(entries)
		if len(entries) == 0 {
			lines = append(lines,
				"Parent directory exists but is empty.",
				"For document or report authoring tasks, treat this as document not created yet. Use list_files on the parent directory before retrying read_file, or create/update the file with edit tools.",
			)
		} else {
			lines = append(lines, "Known entries in parent:")
			for _, entry := range entries[:minInt(len(entries), 12)] {
				item := relOrAbs(root, filepath.Join(parent, entry.Name()))
				if entry.IsDir() {
					item += "/"
				}
				lines = append(lines, "- "+item)
			}
			if len(entries) > 12 {
				lines = append(lines, fmt.Sprintf("- ... (%d more)", len(entries)-12))
			}
			lines = append(lines, "If this path is a generated document, confirm the actual filename with list_files before retrying read_file.")
		}
	case os.IsNotExist(err):
		lines = append(lines,
			"Parent directory does not exist.",
			"Use list_files on the nearest existing ancestor before retrying read_file. For document or report authoring tasks, treat this as document not created yet.",
		)
	default:
		lines = append(lines, "Could not inspect parent directory: "+strings.TrimSpace(err.Error()))
	}

	candidatePaths, candidateSearchTruncated := findMissingReadFileCandidates(root, path, 12, 5000)
	if len(candidatePaths) > 0 {
		lines = append(lines, "Possible matching paths:")
		for _, candidate := range candidatePaths {
			lines = append(lines, "- "+candidate)
		}
		if candidateSearchTruncated {
			lines = append(lines, "- ... (candidate search truncated)")
		}
		lines = append(lines, "If one of these is the intended file, retry read_file with that path.")
	}

	meta := map[string]any{
		"path":                       resolvedPath,
		"requested_path":             resolvedPath,
		"start_line":                 requestedStart,
		"end_line":                   requestedEnd,
		"cache_mode":                 "missing",
		"error_kind":                 "not_found",
		"parent_path":                parentPath,
		"parent_exists":              parentExists,
		"parent_entry_count":         parentEntryCount,
		"candidate_paths":            candidatePaths,
		"candidate_search_truncated": candidateSearchTruncated,
	}
	return strings.Join(lines, "\n"), meta
}

func findMissingReadFileCandidates(root string, missingPath string, maxMatches int, maxVisited int) ([]string, bool) {
	root = cleanAbsPath(root)
	if root == "" || maxMatches <= 0 || maxVisited <= 0 {
		return nil, false
	}
	targetName := strings.TrimSpace(filepath.Base(missingPath))
	if targetName == "" || targetName == "." || targetName == string(os.PathSeparator) {
		return nil, false
	}
	info, err := os.Stat(root)
	if err != nil || info == nil || !info.IsDir() {
		return nil, false
	}
	targetKey := strings.ToLower(targetName)
	missingClean := cleanAbsPath(missingPath)
	matches := make([]string, 0, maxMatches)
	visited := 0
	truncated := false
	errStop := errors.New("candidate search limit reached")
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == root {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		visited++
		if visited > maxVisited {
			truncated = true
			return errStop
		}
		if entry.IsDir() {
			return nil
		}
		if strings.ToLower(entry.Name()) != targetKey {
			return nil
		}
		if samePath(path, missingClean) {
			return nil
		}
		matches = append(matches, relOrAbs(root, path))
		if len(matches) >= maxMatches {
			truncated = true
			return errStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return matches, truncated
	}
	return matches, truncated
}

func normalizeRenderedRangeBounds(output string, requestedStart int, requestedEnd int) (int, int) {
	normalized := strings.ReplaceAll(strings.TrimSpace(output), "\r\n", "\n")
	if normalized == "" {
		return requestedStart, requestedEnd
	}
	lines := strings.Split(normalized, "\n")
	firstLine := strings.TrimSpace(lines[0])
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	parseLineNo := func(text string) int {
		if strings.TrimSpace(text) == "" {
			return 0
		}
		prefix := text
		if divider := strings.Index(prefix, "|"); divider >= 0 {
			prefix = prefix[:divider]
		}
		prefix = strings.TrimSpace(prefix)
		value, _ := strconv.Atoi(prefix)
		return value
	}
	start := parseLineNo(firstLine)
	end := parseLineNo(lastLine)
	if start == 0 {
		start = requestedStart
	}
	if end == 0 {
		end = requestedEnd
	}
	return start, end
}

func readFileCacheKey(path string, start, end int) string {
	return fmt.Sprintf("%s:%d:%d", normalizeReadFileCachePath(path), start, end)
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func (t *ReadFileTool) lookupCachedRead(cacheKey string, info fs.FileInfo) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	entry, ok := t.cache[cacheKey]
	if !ok {
		return "", false
	}
	if entry.size != info.Size() {
		return "", false
	}
	if entry.modTimeUnixNano != info.ModTime().UnixNano() {
		return "", false
	}
	return entry.output, true
}

func (t *ReadFileTool) lookupCoveredCachedRead(path string, start, end int, info fs.FileInfo) (string, bool) {
	if end <= 0 {
		return "", false
	}

	normalizedPath := normalizeReadFileCachePath(path)

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, cacheKey := range t.cacheKeys {
		entry, ok := t.cache[cacheKey]
		if !ok {
			continue
		}
		if entry.path != normalizedPath {
			continue
		}
		if entry.size != info.Size() {
			continue
		}
		if entry.modTimeUnixNano != info.ModTime().UnixNano() {
			continue
		}
		if start < entry.startLine || end > entry.endLine {
			continue
		}
		offsetStart := start - entry.startLine
		offsetEnd := end - entry.startLine + 1
		if offsetStart < 0 || offsetEnd > len(entry.renderedLines) || offsetStart >= offsetEnd {
			continue
		}
		return strings.Join(entry.renderedLines[offsetStart:offsetEnd], "\n"), true
	}

	return "", false
}

func (t *ReadFileTool) lookupPartialOverlap(path string, start, end int, info fs.FileInfo) (readFileCacheEntry, bool) {
	if end <= 0 {
		return readFileCacheEntry{}, false
	}

	normalizedPath := normalizeReadFileCachePath(path)

	t.mu.Lock()
	defer t.mu.Unlock()

	best := readFileCacheEntry{}
	bestOverlap := 0
	for i := len(t.cacheKeys) - 1; i >= 0; i-- {
		cacheKey := t.cacheKeys[i]
		entry, ok := t.cache[cacheKey]
		if !ok {
			continue
		}
		if entry.path != normalizedPath {
			continue
		}
		if entry.size != info.Size() {
			continue
		}
		if entry.modTimeUnixNano != info.ModTime().UnixNano() {
			continue
		}
		overlapStart := readFileMaxInt(start, entry.startLine)
		overlapEnd := readFileMinInt(end, entry.endLine)
		if overlapStart > overlapEnd {
			continue
		}
		overlapLen := overlapEnd - overlapStart + 1
		requestLen := end - start + 1
		if overlapLen <= 0 || overlapLen >= requestLen {
			continue
		}
		if overlapLen > bestOverlap {
			best = cloneReadFileCacheEntry(entry)
			bestOverlap = overlapLen
		}
	}

	if bestOverlap == 0 {
		return readFileCacheEntry{}, false
	}
	return best, true
}

func (t *ReadFileTool) storeCachedRead(cacheKey, path string, start, end int, info fs.FileInfo, renderedLines []string, output string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cache == nil {
		t.cache = make(map[string]readFileCacheEntry)
	}
	if existing, ok := t.cache[cacheKey]; ok {
		existing.path = normalizeReadFileCachePath(path)
		existing.startLine = start
		existing.endLine = end
		existing.modTimeUnixNano = info.ModTime().UnixNano()
		existing.size = info.Size()
		existing.renderedLines = append([]string(nil), renderedLines...)
		existing.output = output
		t.cache[cacheKey] = existing
		return
	}

	t.cache[cacheKey] = readFileCacheEntry{
		path:            normalizeReadFileCachePath(path),
		startLine:       start,
		endLine:         end,
		modTimeUnixNano: info.ModTime().UnixNano(),
		size:            info.Size(),
		renderedLines:   append([]string(nil), renderedLines...),
		output:          output,
	}
	t.cacheKeys = append(t.cacheKeys, cacheKey)
	if t.maxCache <= 0 {
		t.maxCache = t.ws.defaultReadCacheEntries()
	}
	if len(t.cacheKeys) > t.maxCache {
		evictKey := t.cacheKeys[0]
		t.cacheKeys = t.cacheKeys[1:]
		delete(t.cache, evictKey)
	}
}

func normalizeReadFileCachePath(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

func (t *ReadFileTool) recordReadSpanHint(path string, start, end int, info fs.FileInfo) {
	hints := t.ws.toolHints()
	if hints == nil {
		return
	}
	hints.rememberReadSpan(readSpanHint{
		path:            normalizeReadFileCachePath(path),
		startLine:       start,
		endLine:         end,
		modTimeUnixNano: info.ModTime().UnixNano(),
		size:            info.Size(),
	})
}

func cloneReadFileCacheEntry(entry readFileCacheEntry) readFileCacheEntry {
	cloned := entry
	cloned.renderedLines = append([]string(nil), entry.renderedLines...)
	return cloned
}

func readRenderedFileRange(ctx context.Context, path string, start, end int) ([]string, int, error) {
	if start < 1 {
		start = 1
	}
	invalidRequestedRange := end > 0 && end < start

	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()

	if err := rejectBinaryFile(file); err != nil {
		return nil, 0, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, 0, err
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	renderedLines := make([]string, 0)
	lineNumber := 0
	lastLine := 0
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}

		lineNumber++
		lastLine = lineNumber
		if invalidRequestedRange {
			continue
		}
		if lineNumber < start {
			continue
		}
		if end > 0 && lineNumber > end {
			break
		}
		renderedLines = append(renderedLines, fmt.Sprintf("%4d | %s", lineNumber, scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	if invalidRequestedRange || end == 0 || end > lastLine {
		end = lastLine
	}
	return renderedLines, end, nil
}

func readRenderedRangeWithCachedOverlap(ctx context.Context, path string, start, end int, overlap readFileCacheEntry) ([]string, int, error) {
	headLines := make([]string, 0)
	tailLines := make([]string, 0)
	normalizedEnd := end
	var err error

	if start < overlap.startLine {
		headLines, _, err = readRenderedFileRange(ctx, path, start, overlap.startLine-1)
		if err != nil {
			return nil, 0, err
		}
	}

	overlapStart := readFileMaxInt(start, overlap.startLine)
	overlapEnd := readFileMinInt(end, overlap.endLine)
	offsetStart := overlapStart - overlap.startLine
	offsetEnd := overlapEnd - overlap.startLine + 1
	middleLines := append([]string(nil), overlap.renderedLines[offsetStart:offsetEnd]...)

	if end > overlap.endLine {
		tailLines, normalizedEnd, err = readRenderedFileRange(ctx, path, overlap.endLine+1, end)
		if err != nil {
			return nil, 0, err
		}
	}

	combined := make([]string, 0, len(headLines)+len(middleLines)+len(tailLines))
	combined = append(combined, headLines...)
	combined = append(combined, middleLines...)
	combined = append(combined, tailLines...)
	if normalizedEnd == 0 {
		normalizedEnd = overlapEnd
	}
	return combined, normalizedEnd, nil
}

func rejectBinaryFile(file *os.File) error {
	preview := make([]byte, 8192)
	n, err := file.Read(preview)
	if err != nil && err != io.EOF {
		return err
	}
	if !isText(preview[:n]) {
		return fmt.Errorf("refusing to read binary file: %s", file.Name())
	}
	return nil
}

func readFileMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func readFileMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type GrepTool struct{ ws Workspace }

func NewGrepTool(ws Workspace) *GrepTool { return &GrepTool{ws: ws} }

func (t *GrepTool) sharedToolHintsMaxReadSpans() int {
	return t.ws.defaultReadHintSpans()
}

func (t *GrepTool) setSharedToolHints(hints *ToolHints) {
	if hints == nil {
		return
	}
	t.ws.ToolHints = hints
}

func (t GrepTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "grep",
		Description: "Search text across files in the workspace using a regular expression.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string"},
				"path":        map[string]any{"type": "string"},
				"glob":        map[string]any{"type": "string"},
				"max_results": map[string]any{"type": "integer"},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t GrepTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t GrepTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	ownerNodeID := stringValue(args, "owner_node_id")
	route, err := t.ws.ResolveLookupPath(stringValue(args, "path"), ownerNodeID)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	root := route.AbsolutePath
	displayRoot := t.ws.Root
	if strings.TrimSpace(ownerNodeID) != "" {
		displayRoot = firstNonBlankString(route.DisplayRoot, route.WorktreeRoot, t.ws.Root)
	}
	pattern := stringValue(args, "pattern")
	glob := stringValue(args, "glob")
	maxResults := intValue(args, "max_results", 100)
	if maxResults <= 0 {
		maxResults = 100
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		display, meta := buildInvalidGrepPatternResult(displayRoot, root, pattern, glob, maxResults, err)
		return ToolExecutionResult{
			DisplayText: display,
			Meta:        meta,
		}, err
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			display, meta := buildMissingGrepPathResult(displayRoot, root, re.String(), glob, maxResults)
			return ToolExecutionResult{
				DisplayText: display,
				Meta:        meta,
			}, fmt.Errorf("grep target does not exist: %s", relOrAbs(displayRoot, root))
		}
		return ToolExecutionResult{}, err
	}
	var matches []string
	matchedFiles := map[string]struct{}{}
	stop := fmt.Errorf("max results reached")
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if glob != "" {
			ok, err := filepath.Match(glob, filepath.Base(path))
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil || !isText(data) {
			return nil
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		lineNo := 0
		fileInfo, statErr := os.Stat(path)
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if re.MatchString(line) {
				matchPrefix := fmt.Sprintf("%s:%d: %s", relOrAbs(displayRoot, path), lineNo, line)
				if statErr == nil {
					if hint := t.grepReadCacheHint(path, lineNo, fileInfo); hint != "" {
						matchPrefix += " " + hint
					}
				}
				matches = append(matches, matchPrefix)
				matchedFiles[relOrAbs(displayRoot, path)] = struct{}{}
				if len(matches) >= maxResults {
					return stop
				}
			}
		}
		return nil
	})
	if err != nil && err != stop {
		return ToolExecutionResult{}, err
	}
	if len(matches) == 0 {
		return ToolExecutionResult{
			DisplayText: "(no matches)",
			Meta: map[string]any{
				"path":          relOrAbs(displayRoot, root),
				"pattern":       re.String(),
				"glob":          glob,
				"match_count":   0,
				"file_count":    0,
				"max_results":   maxResults,
				"truncated":     false,
				"matched_paths": []string{},
			},
		}, nil
	}
	paths := make([]string, 0, len(matchedFiles))
	for path := range matchedFiles {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return ToolExecutionResult{
		DisplayText: strings.Join(matches, "\n"),
		Meta: map[string]any{
			"path":          relOrAbs(displayRoot, root),
			"pattern":       re.String(),
			"glob":          glob,
			"match_count":   len(matches),
			"file_count":    len(paths),
			"max_results":   maxResults,
			"truncated":     len(matches) >= maxResults,
			"matched_paths": paths,
		},
	}, nil
}

func buildInvalidGrepPatternResult(displayRoot string, path string, pattern string, glob string, maxResults int, patternErr error) (string, map[string]any) {
	resolvedPath := relOrAbs(displayRoot, path)
	detail := strings.TrimSpace(fmt.Sprint(patternErr))
	lines := []string{
		"grep received an invalid regular expression: " + pattern,
	}
	if detail != "" {
		lines = append(lines, "Reason: "+detail)
	}
	lines = append(lines,
		"",
		"Revise the pattern and retry grep. Escape regex metacharacters when you intended a literal search.",
	)
	return strings.Join(lines, "\n"), map[string]any{
		"path":          resolvedPath,
		"pattern":       pattern,
		"glob":          glob,
		"match_count":   0,
		"file_count":    0,
		"max_results":   maxResults,
		"truncated":     false,
		"matched_paths": []string{},
		"error_kind":    "invalid_pattern",
	}
}

func buildMissingGrepPathResult(displayRoot string, path string, pattern string, glob string, maxResults int) (string, map[string]any) {
	resolvedPath := relOrAbs(displayRoot, path)
	candidatePaths, candidatesTruncated := findMissingReadFileCandidates(displayRoot, path, 16, 5000)
	parentExists := false
	if parent := strings.TrimSpace(filepath.Dir(path)); parent != "" && parent != "." {
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			parentExists = true
		}
	}
	lines := []string{
		"grep target does not exist: " + resolvedPath,
		"",
		"The requested search root is missing. Confirm the path with list_files or choose one of the candidate paths below.",
	}
	if len(candidatePaths) > 0 {
		lines = append(lines, "", "Candidate paths with the same filename:")
		for _, candidate := range candidatePaths {
			lines = append(lines, "- "+candidate)
		}
		if candidatesTruncated {
			lines = append(lines, "- ... (more candidates omitted)")
		}
	}
	return strings.Join(lines, "\n"), map[string]any{
		"path":                       resolvedPath,
		"pattern":                    pattern,
		"glob":                       glob,
		"match_count":                0,
		"file_count":                 0,
		"max_results":                maxResults,
		"truncated":                  false,
		"matched_paths":              []string{},
		"error_kind":                 "missing_path",
		"parent_exists":              parentExists,
		"candidate_paths":            candidatePaths,
		"candidate_search_truncated": candidatesTruncated,
	}
}

func (t GrepTool) grepReadCacheHint(path string, lineNo int, info fs.FileInfo) string {
	hints := t.ws.toolHints()
	if hints == nil {
		return ""
	}
	return hints.readCacheHint(normalizeReadFileCachePath(path), lineNo, info)
}

type WriteFileTool struct{ ws Workspace }

func NewWriteFileTool(ws Workspace) WriteFileTool { return WriteFileTool{ws: ws} }

func (t WriteFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "write_file",
		Description: "Write or append to a text file in the workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string"},
				"content":       map[string]any{"type": "string"},
				"append":        map[string]any{"type": "boolean"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t WriteFileTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	route, err := t.ws.ResolveEditPathWithOptions(EditRoutingRequest{
		Path:              stringValue(args, "path"),
		OwnerNodeID:       stringValue(args, "owner_node_id"),
		ForLookup:         false,
		AllowBaseFallback: true,
	})
	if err != nil {
		return "", err
	}
	path := route.AbsolutePath
	displayPath := route.DisplayPath()
	editRoot := firstNonBlankString(route.WorktreeRoot, route.DisplayRoot, t.ws.Root)
	content := stringValue(args, "content")
	before := ""
	beforeExists := false
	if err := t.ws.CheckEditBoundary(path); err != nil {
		return "", err
	}
	if existing, err := os.ReadFile(path); err == nil {
		before = string(existing)
		beforeExists = true
	}
	if suspiciousRewritePayload(path, before, content) {
		return "", fmt.Errorf("%w: write_file content looks like a malformed serialized payload instead of real file contents; use apply_patch or provide the final file text", ErrInvalidEditPayload)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	reason := "write " + displayPath
	after := content
	if boolValue(args, "append", false) {
		after = before + content
	}
	if beforeExists && after == before {
		return fmt.Sprintf("no changes to %s; file already matches requested content", displayPath), nil
	}
	if boolValue(args, "append", false) {
		if _, err := t.ws.Hook(ctx, HookPreEdit, HookPayload{
			"path":          displayPath,
			"absolute_path": path,
			"operation":     "write_file",
			"reason":        reason,
			"file_tags":     hookFileTags(path),
			"owner_node_id": route.OwnerNodeID,
			"worktree_root": route.WorktreeRoot,
			"specialist":    route.Specialist,
		}); err != nil {
			return "", err
		}
		preview := EditPreview{
			Title:     "Append to " + displayPath,
			Preview:   buildSelectionAwareEditPreview(t.ws, displayPath, before, after),
			Paths:     []string{displayPath},
			Operation: "write_file",
		}
		if err := t.ws.ReviewProposedEdit(ctx, preview); err != nil {
			return "", err
		}
		if err := t.ws.ConfirmEdit(preview); err != nil {
			return "", err
		}
		if err := t.ws.EnsureWriteWithContext(ctx, path); err != nil {
			return "", err
		}
		if err := t.ws.BeforeEditForRoot(reason, editRoot); err != nil {
			return "", err
		}
		if err := ensureParentDir(path); err != nil {
			return "", err
		}
		t.ws.Progress("Writing " + displayPath + "...")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if _, err := f.WriteString(content); err != nil {
			return "", err
		}
		t.ws.Progress("Saved " + displayPath + ".")
	} else {
		if _, err := t.ws.Hook(ctx, HookPreEdit, HookPayload{
			"path":          displayPath,
			"absolute_path": path,
			"operation":     "write_file",
			"reason":        reason,
			"file_tags":     hookFileTags(path),
			"owner_node_id": route.OwnerNodeID,
			"worktree_root": route.WorktreeRoot,
			"specialist":    route.Specialist,
		}); err != nil {
			return "", err
		}
		preview := EditPreview{
			Title:     "Write " + displayPath,
			Preview:   buildSelectionAwareEditPreview(t.ws, displayPath, before, after),
			Paths:     []string{displayPath},
			Operation: "write_file",
		}
		if err := t.ws.ReviewProposedEdit(ctx, preview); err != nil {
			return "", err
		}
		if err := t.ws.ConfirmEdit(preview); err != nil {
			return "", err
		}
		if err := t.ws.EnsureWriteWithContext(ctx, path); err != nil {
			return "", err
		}
		if err := t.ws.BeforeEditForRoot(reason, editRoot); err != nil {
			return "", err
		}
		if err := ensureParentDir(path); err != nil {
			return "", err
		}
		t.ws.Progress("Writing " + displayPath + "...")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
		t.ws.Progress("Saved " + displayPath + ".")
	}
	t.ws.Progress("Running post-edit hooks for " + displayPath + "...")
	if _, err := t.ws.Hook(ctx, HookPostEdit, HookPayload{
		"path":          displayPath,
		"absolute_path": path,
		"operation":     "write_file",
		"reason":        reason,
		"file_tags":     hookFileTags(path),
		"owner_node_id": route.OwnerNodeID,
		"worktree_root": route.WorktreeRoot,
		"specialist":    route.Specialist,
	}); err != nil {
		return "", err
	}
	t.ws.Progress("Post-edit hooks finished for " + displayPath + ".")
	return joinNonEmpty(
		fmt.Sprintf("wrote %d bytes to %s", len(content), displayPath),
		buildEditPreview(displayPath, before, after),
	), nil
}

func (t WriteFileTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	planned := writeFileMutationPreview(t.ws, args)
	text, err := t.Execute(ctx, input)
	path := strings.TrimSpace(stringValue(args, "path"))
	if planned.DisplayPath != "" {
		path = planned.DisplayPath
	}
	committedPlanned := err == nil || planned.Committed()
	changedWorkspace := planned.ChangedOnDisk()
	changedPaths, changedCount := changedWorkspacePathMeta(path, changedWorkspace)
	bytesWritten := 0
	if changedWorkspace {
		bytesWritten = len(stringValue(args, "content"))
	}
	meta := map[string]any{
		"path":                  path,
		"changed_paths":         changedPaths,
		"changed_count":         changedCount,
		"append":                boolValue(args, "append", false),
		"owner_node_id":         strings.TrimSpace(stringValue(args, "owner_node_id")),
		"bytes_written":         bytesWritten,
		"changed_workspace":     changedWorkspace,
		"requires_verification": changedWorkspace,
		"effect":                "edit",
	}
	if changedWorkspace && committedPlanned && strings.TrimSpace(planned.UnifiedDiff) != "" {
		meta["unified_diff"] = planned.UnifiedDiff
	} else if changedWorkspace && err != nil {
		meta["turn_diff_invalidated"] = true
		meta["unified_diff_unavailable_reason"] = "workspace changed but final contents did not match the planned edit after tool failure"
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func writeFileUnifiedDiffPreview(ws Workspace, args map[string]any) (string, string) {
	planned := writeFileMutationPreview(ws, args)
	return planned.DisplayPath, planned.UnifiedDiff
}

func writeFileMutationPreview(ws Workspace, args map[string]any) plannedTextFileEdit {
	route, err := ws.ResolveEditPathWithOptions(EditRoutingRequest{
		Path:              stringValue(args, "path"),
		OwnerNodeID:       stringValue(args, "owner_node_id"),
		ForLookup:         false,
		AllowBaseFallback: true,
	})
	if err != nil {
		return plannedTextFileEdit{}
	}
	before := ""
	beforeExists := false
	if existing, readErr := os.ReadFile(route.AbsolutePath); readErr == nil {
		before = string(existing)
		beforeExists = true
	}
	after := stringValue(args, "content")
	if boolValue(args, "append", false) {
		after = before + after
	}
	displayPath := route.DisplayPath()
	return plannedTextFileEdit{
		AbsolutePath:  route.AbsolutePath,
		DisplayPath:   displayPath,
		Before:        before,
		After:         after,
		BeforeExists:  beforeExists,
		AfterExists:   true,
		UnifiedDiff:   buildUnifiedDiff(displayPath, before, after),
		PlanAvailable: strings.TrimSpace(displayPath) != "",
	}
}

type plannedTextFileEdit struct {
	AbsolutePath  string
	DisplayPath   string
	Before        string
	After         string
	BeforeExists  bool
	AfterExists   bool
	UnifiedDiff   string
	PlanAvailable bool
}

func (p plannedTextFileEdit) PlannedChange() bool {
	if !p.PlanAvailable {
		return false
	}
	return p.BeforeExists != p.AfterExists || p.Before != p.After
}

func (p plannedTextFileEdit) Committed() bool {
	if !p.PlannedChange() {
		return false
	}
	if strings.TrimSpace(p.AbsolutePath) == "" {
		return false
	}
	if !p.AfterExists {
		_, err := os.Stat(p.AbsolutePath)
		return errors.Is(err, os.ErrNotExist)
	}
	data, err := os.ReadFile(p.AbsolutePath)
	return err == nil && string(data) == p.After
}

func (p plannedTextFileEdit) ChangedOnDisk() bool {
	if !p.PlanAvailable {
		return false
	}
	if strings.TrimSpace(p.AbsolutePath) == "" {
		return false
	}
	data, err := os.ReadFile(p.AbsolutePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p.BeforeExists
		}
		return false
	}
	return !p.BeforeExists || string(data) != p.Before
}

func suspiciousRewritePayload(path, before, after string) bool {
	if strings.TrimSpace(before) == "" || strings.TrimSpace(after) == "" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs", ".rs", ".json", ".yaml", ".yml", ".toml", ".md":
	default:
		return false
	}

	beforeLines := strings.Count(strings.ReplaceAll(before, "\r\n", "\n"), "\n") + 1
	afterNormalized := strings.ReplaceAll(after, "\r\n", "\n")
	afterLines := strings.Count(afterNormalized, "\n") + 1
	if beforeLines < 5 || afterLines != 1 {
		return false
	}

	suspiciousBits := []string{
		"{'",
		"[[",
		"'lines':",
		"'line':",
		"\\\\n':",
		"\\n':",
	}
	hits := 0
	for _, bit := range suspiciousBits {
		if strings.Contains(after, bit) {
			hits++
		}
	}
	return hits >= 2
}

func suspiciousReplacePayload(path, search, replace, before, after string) bool {
	if strings.TrimSpace(search) == "" || strings.TrimSpace(replace) == "" {
		return false
	}
	if !suspiciousRewritePayload(path, before, after) {
		return false
	}

	suspiciousBits := []string{
		"{'",
		"'trimmed':",
		"'lines':",
		"'line':",
		"\\n",
		"\\t",
	}
	hits := 0
	for _, bit := range suspiciousBits {
		if strings.Contains(replace, bit) {
			hits++
		}
	}
	return hits >= 2
}

type ReplaceInFileTool struct{ ws Workspace }

func NewReplaceInFileTool(ws Workspace) ReplaceInFileTool { return ReplaceInFileTool{ws: ws} }

func (t ReplaceInFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "replace_in_file",
		Description: "Replace an exact text match in a file. Use this only for very small single-location substitutions when you have just read the same file path and confirmed the exact search text.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          map[string]any{"type": "string"},
				"search":        map[string]any{"type": "string"},
				"replace":       map[string]any{"type": "string"},
				"all":           map[string]any{"type": "boolean"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"path", "search", "replace"},
		},
	}
}

func (t ReplaceInFileTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	route, err := t.ws.ResolveEditPathWithOptions(EditRoutingRequest{
		Path:              stringValue(args, "path"),
		OwnerNodeID:       stringValue(args, "owner_node_id"),
		ForLookup:         false,
		AllowBaseFallback: true,
	})
	if err != nil {
		return "", err
	}
	path := route.AbsolutePath
	displayPath := route.DisplayPath()
	editRoot := firstNonBlankString(route.WorktreeRoot, route.DisplayRoot, t.ws.Root)
	if err := t.ws.CheckEditBoundary(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	search := stringValue(args, "search")
	replace := stringValue(args, "replace")
	content := string(data)
	count := strings.Count(content, search)
	if count == 0 {
		return "", fmt.Errorf("%w: search text not found in %s", ErrEditTargetMismatch, path)
	}
	all := boolValue(args, "all", false)
	if !all && count > 1 {
		return "", fmt.Errorf("search text appears %d times; set all=true or use a more specific match", count)
	}
	var updated string
	if all {
		updated = strings.ReplaceAll(content, search, replace)
	} else {
		updated = strings.Replace(content, search, replace, 1)
	}
	if suspiciousReplacePayload(path, search, replace, content, updated) {
		return "", fmt.Errorf("%w: replace_in_file replacement looks like a malformed serialized payload instead of real code; use apply_patch or provide the exact replacement text", ErrInvalidEditPayload)
	}
	if updated == content {
		return fmt.Sprintf("no changes to %s; replacement leaves file unchanged", displayPath), nil
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	if _, err := t.ws.Hook(ctx, HookPreEdit, HookPayload{
		"path":          displayPath,
		"absolute_path": path,
		"operation":     "replace_in_file",
		"reason":        "replace in " + displayPath,
		"file_tags":     hookFileTags(path),
		"owner_node_id": route.OwnerNodeID,
		"worktree_root": route.WorktreeRoot,
		"specialist":    route.Specialist,
	}); err != nil {
		return "", err
	}
	preview := EditPreview{
		Title:     "Update " + displayPath,
		Preview:   buildSelectionAwareEditPreview(t.ws, displayPath, content, updated),
		Paths:     []string{displayPath},
		Operation: "replace_in_file",
	}
	if err := t.ws.ReviewProposedEdit(ctx, preview); err != nil {
		return "", err
	}
	if err := t.ws.ConfirmEdit(preview); err != nil {
		return "", err
	}
	if err := t.ws.EnsureWriteWithContext(ctx, path); err != nil {
		return "", err
	}
	if err := t.ws.BeforeEditForRoot("replace in "+displayPath, editRoot); err != nil {
		return "", err
	}
	t.ws.Progress("Writing " + displayPath + "...")
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return "", err
	}
	t.ws.Progress("Saved " + displayPath + ".")
	t.ws.Progress("Running post-edit hooks for " + displayPath + "...")
	if _, err := t.ws.Hook(ctx, HookPostEdit, HookPayload{
		"path":          displayPath,
		"absolute_path": path,
		"operation":     "replace_in_file",
		"reason":        "replace in " + displayPath,
		"file_tags":     hookFileTags(path),
		"owner_node_id": route.OwnerNodeID,
		"worktree_root": route.WorktreeRoot,
		"specialist":    route.Specialist,
	}); err != nil {
		return "", err
	}
	t.ws.Progress("Post-edit hooks finished for " + displayPath + ".")
	return joinNonEmpty(
		fmt.Sprintf("updated %s (%d replacement(s))", displayPath, count),
		buildEditPreview(displayPath, content, updated),
	), nil
}

func (t ReplaceInFileTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	planned, plannedReplacements := replaceInFileMutationPreview(t.ws, args)
	text, err := t.Execute(ctx, input)
	path := strings.TrimSpace(stringValue(args, "path"))
	if planned.DisplayPath != "" {
		path = planned.DisplayPath
	}
	all := boolValue(args, "all", false)
	replacements := 0
	if err == nil {
		if all {
			if parsed, parseErr := parseReplacementCountFromOutput(text); parseErr == nil {
				replacements = parsed
			}
		} else {
			replacements = 1
		}
		if replacements == 0 && plannedReplacements > 0 {
			replacements = plannedReplacements
		}
	}
	committedPlanned := err == nil || planned.Committed()
	changedWorkspace := planned.ChangedOnDisk()
	if changedWorkspace && replacements == 0 && plannedReplacements > 0 {
		replacements = plannedReplacements
	}
	if !changedWorkspace {
		replacements = 0
	}
	changedPaths, changedCount := changedWorkspacePathMeta(path, changedWorkspace)
	meta := map[string]any{
		"path":                  path,
		"changed_paths":         changedPaths,
		"changed_count":         changedCount,
		"all":                   all,
		"owner_node_id":         strings.TrimSpace(stringValue(args, "owner_node_id")),
		"applied_replacements":  replacements,
		"changed_workspace":     changedWorkspace,
		"requires_verification": changedWorkspace,
		"effect":                "edit",
	}
	if changedWorkspace && committedPlanned && strings.TrimSpace(planned.UnifiedDiff) != "" {
		meta["unified_diff"] = planned.UnifiedDiff
	} else if changedWorkspace && err != nil {
		meta["turn_diff_invalidated"] = true
		meta["unified_diff_unavailable_reason"] = "workspace changed but final contents did not match the planned edit after tool failure"
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func replaceInFileUnifiedDiffPreview(ws Workspace, args map[string]any) (string, string, int) {
	planned, replacements := replaceInFileMutationPreview(ws, args)
	return planned.DisplayPath, planned.UnifiedDiff, replacements
}

func replaceInFileMutationPreview(ws Workspace, args map[string]any) (plannedTextFileEdit, int) {
	route, err := ws.ResolveEditPathWithOptions(EditRoutingRequest{
		Path:              stringValue(args, "path"),
		OwnerNodeID:       stringValue(args, "owner_node_id"),
		ForLookup:         false,
		AllowBaseFallback: true,
	})
	if err != nil {
		return plannedTextFileEdit{}, 0
	}
	data, err := os.ReadFile(route.AbsolutePath)
	if err != nil {
		return plannedTextFileEdit{
			AbsolutePath:  route.AbsolutePath,
			DisplayPath:   route.DisplayPath(),
			PlanAvailable: strings.TrimSpace(route.DisplayPath()) != "",
		}, 0
	}
	before := string(data)
	search := stringValue(args, "search")
	count := strings.Count(before, search)
	if count == 0 {
		return plannedTextFileEdit{
			AbsolutePath:  route.AbsolutePath,
			DisplayPath:   route.DisplayPath(),
			Before:        before,
			After:         before,
			BeforeExists:  true,
			AfterExists:   true,
			PlanAvailable: strings.TrimSpace(route.DisplayPath()) != "",
		}, 0
	}
	all := boolValue(args, "all", false)
	if !all && count > 1 {
		return plannedTextFileEdit{
			AbsolutePath:  route.AbsolutePath,
			DisplayPath:   route.DisplayPath(),
			Before:        before,
			After:         before,
			BeforeExists:  true,
			AfterExists:   true,
			PlanAvailable: strings.TrimSpace(route.DisplayPath()) != "",
		}, 0
	}
	replace := stringValue(args, "replace")
	after := ""
	if all {
		after = strings.ReplaceAll(before, search, replace)
	} else {
		after = strings.Replace(before, search, replace, 1)
		count = 1
	}
	displayPath := route.DisplayPath()
	return plannedTextFileEdit{
		AbsolutePath:  route.AbsolutePath,
		DisplayPath:   displayPath,
		Before:        before,
		After:         after,
		BeforeExists:  true,
		AfterExists:   true,
		UnifiedDiff:   buildUnifiedDiff(displayPath, before, after),
		PlanAvailable: strings.TrimSpace(displayPath) != "",
	}, count
}

func parseReplacementCountFromOutput(text string) (int, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		start := strings.Index(line, "(")
		end := strings.Index(line, " replacement")
		if start < 0 || end <= start {
			continue
		}
		return strconv.Atoi(strings.TrimSpace(line[start+1 : end]))
	}
	return 0, fmt.Errorf("replacement count not found")
}

type RunShellTool struct{ ws Workspace }

func NewRunShellTool(ws Workspace) RunShellTool { return RunShellTool{ws: ws} }

func (t RunShellTool) shellHookPayload(payload HookPayload) HookPayload {
	addEffectiveExecutionContextMetadata(payload, t.ws, nil)
	return payload
}

type shellMutationClass string

const (
	shellMutationReadOnly              shellMutationClass = "read_only"
	shellMutationCacheOnly             shellMutationClass = "cache_only"
	shellMutationExternalInstall       shellMutationClass = "external_install"
	shellMutationGitMutation           shellMutationClass = "git_mutation"
	shellMutationUnsafe                shellMutationClass = "unsafe"
	shellMutationUnsupported           shellMutationClass = "unsupported"
	shellMutationVerificationArtifacts shellMutationClass = "verification_artifacts"
	shellMutationWorkspaceWrite        shellMutationClass = "workspace_write"
)

const (
	shellOutputTailLimit      = 64 * 1024
	shellOutputHeartbeatEvery = 15 * time.Second
	shellOutputProgressEvery  = 2 * time.Second
)

var shellManualWorkspaceWriteCommandPattern = regexp.MustCompile(`(?i)(^|[;|&(){}])\s*(?:set-content|add-content|clear-content|out-file|tee-object|new-item|remove-item|rename-item|move-item|copy-item|set-acl|export-csv|export-clixml|start-transcript|stop-transcript|mkdir|md|del|erase|copy|move|ren|rename|rm|mv|cp|touch)\b`)
var shellNestedManualWorkspaceWriteCommandPattern = regexp.MustCompile(`(?i)(^|[\s;|&(){}"'` + "`" + `])(?:set-content|add-content|clear-content|out-file|tee-object|new-item|remove-item|rename-item|move-item|copy-item|set-acl|export-csv|export-clixml|start-transcript|stop-transcript|mkdir|md|del|erase|copy|move|ren|rename|rm|mv|cp|touch)\b`)

type shellCommandAssessment struct {
	Class  shellMutationClass
	Reason string
}

type shellOutputEvent struct {
	data []byte
}

type shellOutputCollector struct {
	ws             Workspace
	commandSummary string
	startedAt      time.Time
	tailLimit      int

	mu             sync.Mutex
	tail           []byte
	totalBytes     int
	lastOutputLine string
	lastProgressAt time.Time
	lineBuffer     []byte
}

func (t RunShellTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "run_shell",
		Description: "Run a shell command in the workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":       map[string]any{"type": "string"},
				"workdir":       map[string]any{"type": "string"},
				"timeout_ms":    map[string]any{"type": "integer"},
				"owner_node_id": map[string]any{"type": "string"},
			},
			"required": []string{"command"},
		},
	}
}

func (t RunShellTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	command := stringValue(args, "command")
	ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id"))
	workdir := strings.TrimSpace(stringValue(args, "workdir"))
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	shellRoute, workDir, err := t.ws.ResolveShellWorkDir(ownerNodeID, workdir)
	if err != nil {
		return "", err
	}
	effectiveOwnerNodeID := firstNonBlankString(shellRoute.OwnerNodeID, ownerNodeID)
	originalCommand := command
	verdict, err := t.ws.Hook(ctx, HookPreToolUse, t.shellHookPayload(HookPayload{
		"tool_name":     "run_shell",
		"tool_kind":     "shell",
		"command":       originalCommand,
		"risk_tags":     hookCommandRiskTags(originalCommand),
		"file_tags":     []string{},
		"owner_node_id": effectiveOwnerNodeID,
		"specialist":    shellRoute.Specialist,
		"work_dir":      workDir,
	}))
	if err != nil {
		return "", err
	}
	if updatedCommand, changed, updateErr := hookUpdatedCommand(verdict, "run_shell", command); updateErr != nil {
		return "", updateErr
	} else if changed {
		command = updatedCommand
		args["command"] = command
	}
	if guidance := runShellCompatibilityGuidance(t.ws.Shell, command); guidance != "" {
		return guidance, fmt.Errorf("shell command is incompatible with the active shell")
	}
	assessment := assessShellCommandMutation(command)
	if assessment.Class == shellMutationUnsafe {
		return "", shellCommandUnsafeError("run_shell", assessment)
	}
	if assessment.Class == shellMutationUnsupported {
		return "", shellCommandUnsupportedSyntaxError("run_shell", assessment)
	}
	if assessment.Class == shellMutationReadOnly || assessment.Class == shellMutationCacheOnly {
		if guidance := runShellDedicatedToolGuidance(command); guidance != "" {
			return guidance, fmt.Errorf("run_shell command should use a dedicated workspace tool")
		}
	}
	if assessment.Class == shellMutationWorkspaceWrite {
		if reason := shellCommandManualWorkspaceWriteReason(command); reason != "" {
			return "", fmt.Errorf("run_shell cannot perform manual workspace file writes; use apply_patch or apply_edit_proposal so edits stay reviewable (%s)", reason)
		}
		return "", fmt.Errorf("run_shell cannot modify workspace files because shell writes bypass the diff preview and review gate; use apply_patch or apply_edit_proposal instead (%s)", assessment.Reason)
	}
	if assessment.Class == shellMutationVerificationArtifacts {
		t.ws.Progress("run_shell recognized a verification/build command that may write workspace build artifacts. Source edits are still blocked.")
		ok, confirmErr := t.ws.ConfirmVerificationPlanWithContext(ctx, VerificationPlan{
			Mode:         VerificationAdaptive,
			ChangedPaths: collectVerificationChangedPaths(workDir, nil),
			Steps: []VerificationStep{{
				Label:   "shell verification",
				Command: command,
				Status:  VerificationPending,
			}},
		})
		if confirmErr != nil {
			return "", confirmErr
		}
		if !ok {
			return skippedVerificationCommandText(), nil
		}
	}
	var workspaceBeforeShell map[string]workspaceFileSignature
	if assessment.Class == shellMutationVerificationArtifacts {
		snapshot, snapshotErr := snapshotWorkspaceFiles(workDir)
		if snapshotErr != nil {
			return "", snapshotErr
		}
		if externalLinks := workspaceSnapshotExternalSymlinkPaths(snapshot); len(externalLinks) > 0 {
			return "", fmt.Errorf("run_shell verification command is blocked because the workspace contains symlinks that resolve outside the active root: %s", strings.Join(externalLinks, ", "))
		}
		workspaceBeforeShell = snapshot
	}
	if err := t.ws.EnsureShellWithContext(ctx, command); err != nil {
		return "", err
	}
	timeout := t.ws.defaultShellTimeout()
	if timeoutMs := intValue(args, "timeout_ms", 0); timeoutMs > 0 {
		timeout = time.Duration(timeoutMs) * time.Millisecond
	}
	text, err := t.runShellCommand(ctx, workDir, command, timeout)
	if err != nil {
		if workspaceErr := detectUnexpectedShellWorkspaceChanges(workDir, workspaceBeforeShell); workspaceErr != nil {
			err = fmt.Errorf("%w; %v", err, workspaceErr)
		}
		text = appendRunShellGuidance(text, runShellFailureGuidance(t.ws.Shell, command, text, err))
		_, _ = t.ws.Hook(ctx, HookPostToolUse, t.shellHookPayload(HookPayload{
			"tool_name":     "run_shell",
			"tool_kind":     "shell",
			"command":       command,
			"risk_tags":     hookCommandRiskTags(command),
			"output":        text,
			"error":         err.Error(),
			"owner_node_id": effectiveOwnerNodeID,
			"specialist":    shellRoute.Specialist,
			"work_dir":      workDir,
		}))
		return text, err
	}
	if text == "" {
		text = "(no output)"
	}
	if workspaceErr := detectUnexpectedShellWorkspaceChanges(workDir, workspaceBeforeShell); workspaceErr != nil {
		_, _ = t.ws.Hook(ctx, HookPostToolUse, t.shellHookPayload(HookPayload{
			"tool_name":     "run_shell",
			"tool_kind":     "shell",
			"command":       command,
			"risk_tags":     hookCommandRiskTags(command),
			"output":        text,
			"error":         workspaceErr.Error(),
			"owner_node_id": effectiveOwnerNodeID,
			"specialist":    shellRoute.Specialist,
			"work_dir":      workDir,
		}))
		return text, workspaceErr
	}
	if _, err := t.ws.Hook(ctx, HookPostToolUse, t.shellHookPayload(HookPayload{
		"tool_name":     "run_shell",
		"tool_kind":     "shell",
		"command":       command,
		"risk_tags":     hookCommandRiskTags(command),
		"output":        text,
		"owner_node_id": effectiveOwnerNodeID,
		"specialist":    shellRoute.Specialist,
		"work_dir":      workDir,
	})); err != nil {
		return "", err
	}
	return text, nil
}

func skippedVerificationCommandText() string {
	return "verification command skipped because the user declined to run it. Do not retry this verification command or poll a background job for it unless the user explicitly approves verification; disclose that verification was not run. Do not relabel resolved code-review findings as remaining bugs only because verification is missing."
}

func detectUnexpectedShellWorkspaceChanges(workDir string, before map[string]workspaceFileSignature) error {
	if len(before) == 0 || strings.TrimSpace(workDir) == "" {
		return nil
	}
	current, err := snapshotWorkspaceFiles(workDir)
	if err != nil {
		return err
	}
	changed := changedWorkspaceSignaturePaths(before, current)
	if len(changed) == 0 {
		return nil
	}
	unexpected := verificationWorkspaceSourceOrConfigChanges(changed)
	if len(unexpected) == 0 {
		return nil
	}
	return fmt.Errorf("run_shell verification command modified workspace source/config files outside the edit review gate: %s", strings.Join(unexpected, ", "))
}

func (t RunShellTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	originalCommand := stringValue(args, "command")
	ownerNodeID := strings.TrimSpace(stringValue(args, "owner_node_id"))
	workdir := strings.TrimSpace(stringValue(args, "workdir"))
	text, err := t.Execute(ctx, input)
	command := stringValue(args, "command")
	if strings.TrimSpace(command) == "" {
		command = originalCommand
	}
	_, workDir, _ := t.ws.ResolveShellWorkDir(ownerNodeID, workdir)
	assessment := assessShellCommandMutation(command)
	verificationLike := assessment.Class == shellMutationVerificationArtifacts || runShellOutputLooksLikeVerification(text) || runShellOutputLooksLikeSkippedVerification(text)
	meta := map[string]any{
		"command":           command,
		"mutation_class":    string(assessment.Class),
		"verification_like": verificationLike,
		"owner_node_id":     ownerNodeID,
		"work_dir":          workDir,
		"changed_workspace": false,
		"effect":            "execute",
	}
	if originalCommand != "" && command != originalCommand {
		meta["hook_rewritten"] = true
		meta["original_command"] = originalCommand
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	if verificationLike {
		status := VerificationPassed
		commandStatus := "completed"
		if runShellOutputLooksLikeSkippedVerification(text) {
			status = VerificationSkipped
			commandStatus = "declined"
		} else if err != nil {
			status = VerificationFailed
			commandStatus = "failed"
		}
		meta["verification_status"] = string(status)
		meta["verification_evidence"] = status == VerificationPassed
		meta["verification_approved"] = status != VerificationSkipped
		meta["command_execution_status"] = commandStatus
		if status == VerificationSkipped {
			meta["verification_declined"] = true
		}
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func runShellDedicatedToolGuidance(command string) string {
	if runShellLooksLikeFileReadInspection(command) {
		return "Use the read_file tool instead of run_shell for source file inspection. This avoids shell approval prompts and keeps file evidence structured for review."
	}
	args := shellLikeFields(command)
	if len(args) < 2 {
		return ""
	}
	program := strings.ToLower(strings.TrimSuffix(filepath.Base(args[0]), ".exe"))
	if program != "git" {
		return ""
	}
	switch strings.ToLower(args[1]) {
	case "status":
		return "Use the git_status tool instead of run_shell for git status inspection. This avoids interactive shell approval and keeps the review/repair loop deterministic."
	case "diff":
		return "Use the git_diff tool instead of run_shell for git diff inspection. Pass a path to git_diff when you need a focused diff."
	default:
	}
	return ""
}

func shellLikeFields(command string) []string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return nil
	}
	for i, field := range fields {
		fields[i] = strings.Trim(field, "\"'")
	}
	return fields
}

func runShellLooksLikeFileReadInspection(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "get-content") {
		return true
	}
	fields := shellLikeFields(command)
	if len(fields) == 0 {
		return false
	}
	first := strings.ToLower(strings.TrimSuffix(filepath.Base(fields[0]), ".exe"))
	switch first {
	case "cat", "type", "gc":
		return len(fields) >= 2
	default:
		return false
	}
}

func (t RunShellTool) runShellCommand(ctx context.Context, workDir string, command string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name, shellArgs := shellInvocation(t.ws.Shell, command)
	cmd := exec.CommandContext(runCtx, name, shellArgs...)
	cmd.Dir = workDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	collector := newShellOutputCollector(t.ws, command)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan struct{})
	defer close(done)
	go t.emitShellHeartbeats(done, collector)

	streamErrs := make(chan error, 2)
	events := make(chan shellOutputEvent, 32)
	var streamWG sync.WaitGroup
	streamWG.Add(2)
	go func() {
		defer streamWG.Done()
		if readErr := streamShellOutput(runCtx, stdout, events); readErr != nil {
			streamErrs <- readErr
		}
	}()
	go func() {
		defer streamWG.Done()
		if readErr := streamShellOutput(runCtx, stderr, events); readErr != nil {
			streamErrs <- readErr
		}
	}()
	waitErrs := make(chan error, 1)
	go func() {
		waitErrs <- cmd.Wait()
	}()
	go func() {
		streamWG.Wait()
		close(events)
		close(streamErrs)
	}()
	var waitErr error
	waitDone := false
	eventsClosed := false
	for !waitDone || !eventsClosed {
		select {
		case event, ok := <-events:
			if !ok {
				eventsClosed = true
				continue
			}
			collector.AppendBytes(event.data)
		case err := <-waitErrs:
			waitErr = err
			waitDone = true
			if !eventsClosed {
				_ = drainShellOutputEvents(collector, events, 200*time.Millisecond)
				eventsClosed = true
			}
		case <-runCtx.Done():
			if cmd.Process != nil {
				_ = terminateBackgroundProcess(cmd.Process.Pid)
			}
			_ = drainShellOutputEvents(collector, events, 500*time.Millisecond)
			eventsClosed = true
			select {
			case err := <-waitErrs:
				waitErr = err
				waitDone = true
			case <-time.After(1500 * time.Millisecond):
				return collector.Text(), runCtx.Err()
			}
		}
	}
	err = waitErr
	err = mergeShellStreamErrors(err, streamErrs, 100*time.Millisecond)
	text := collector.Text()
	if runCtx.Err() == context.Canceled {
		if text == "" {
			text = "command canceled"
		}
		return text, runCtx.Err()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("command failed [%s]: %w", summarizeShellCommand(command), err)
	}
	return text, nil
}

func mergeShellStreamErrors(err error, streamErrs <-chan error, limit time.Duration) error {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		select {
		case readErr, ok := <-streamErrs:
			if !ok {
				return err
			}
			if err == nil {
				err = readErr
			}
		case <-timer.C:
			return err
		}
	}
}

func drainShellOutputEvents(collector *shellOutputCollector, events <-chan shellOutputEvent, limit time.Duration) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return true
			}
			collector.AppendBytes(event.data)
		case <-timer.C:
			return false
		}
	}
}

func summarizeShellCommand(command string) string {
	command = strings.TrimSpace(command)
	if len(command) <= 120 {
		return command
	}
	return command[:120] + "..."
}

func shellCommandLikelyMutatesWorkspace(command string) bool {
	return assessShellCommandMutation(command).Class == shellMutationWorkspaceWrite
}

func newShellOutputCollector(ws Workspace, command string) *shellOutputCollector {
	return &shellOutputCollector{
		ws:             ws,
		commandSummary: summarizeShellCommand(command),
		startedAt:      time.Now(),
		tailLimit:      shellOutputTailLimit,
	}
}

func (t RunShellTool) emitShellHeartbeats(done <-chan struct{}, collector *shellOutputCollector) {
	ticker := time.NewTicker(shellOutputHeartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if heartbeat := collector.Heartbeat(); heartbeat != "" {
				t.ws.Progress(heartbeat)
			}
		}
	}
}

func streamShellOutput(ctx context.Context, reader io.Reader, events chan<- shellOutputEvent) error {
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			chunk := append([]byte(nil), buffer[:n]...)
			select {
			case events <- shellOutputEvent{data: chunk}:
			case <-ctx.Done():
				return nil
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func (c *shellOutputCollector) AppendBytes(chunk []byte) {
	trimmedLine, sawDelimiter := c.consumeProgressChunk(chunk)
	now := time.Now()

	c.mu.Lock()
	c.totalBytes += len(chunk)
	c.tail = appendShellOutputTail(c.tail, chunk, c.tailLimit)
	emitProgress := false
	if trimmedLine != "" {
		c.lastOutputLine = trimmedLine
		if sawDelimiter || now.Sub(c.lastProgressAt) >= shellOutputProgressEvery {
			c.lastProgressAt = now
			emitProgress = true
		}
	}
	c.mu.Unlock()

	if emitProgress {
		c.ws.Progress("run_shell output: " + trimmedLine)
	}
}

func (c *shellOutputCollector) Text() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	text := strings.TrimSpace(normalizeShellOutputForDisplay(c.tail))
	if text == "" {
		return ""
	}
	if c.totalBytes <= len(c.tail) {
		return text
	}
	return fmt.Sprintf("[run_shell output truncated to last %s of %s]\n%s", formatShellByteCount(len(c.tail)), formatShellByteCount(c.totalBytes), text)
}

func (c *shellOutputCollector) Heartbeat() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := time.Since(c.startedAt).Round(time.Second)
	if elapsed <= 0 {
		elapsed = time.Second
	}
	message := fmt.Sprintf("run_shell still running after %s: %s", elapsed, c.commandSummary)
	current := c.lastOutputLine
	if current == "" && len(c.lineBuffer) > 0 {
		current = summarizeShellProgressLine(string(c.lineBuffer))
	}
	if current != "" {
		message += " | last output: " + current
	}
	if c.totalBytes > 0 {
		message += " | buffered " + formatShellByteCount(c.totalBytes)
	}
	return message
}

func appendShellOutputTail(current []byte, chunk []byte, limit int) []byte {
	if limit <= 0 {
		limit = shellOutputTailLimit
	}
	if len(chunk) >= limit {
		return append([]byte(nil), chunk[len(chunk)-limit:]...)
	}
	if len(current)+len(chunk) <= limit {
		return append(current, chunk...)
	}
	trim := len(current) + len(chunk) - limit
	if trim > len(current) {
		trim = len(current)
	}
	next := append([]byte(nil), current[trim:]...)
	return append(next, chunk...)
}

func normalizeShellOutputForDisplay(raw []byte) string {
	text := decodePossiblyUTF16(raw)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func decodePossiblyUTF16(raw []byte) string {
	if len(raw) >= 2 {
		if raw[0] == 0xFF && raw[1] == 0xFE {
			return string(utf16.Decode(bytesToUint16s(raw[2:], true)))
		}
		if raw[0] == 0xFE && raw[1] == 0xFF {
			return string(utf16.Decode(bytesToUint16s(raw[2:], false)))
		}
	}
	zeroBytes := 0
	sample := raw
	if len(sample) > 128 {
		sample = sample[:128]
	}
	for _, b := range sample {
		if b == 0 {
			zeroBytes++
		}
	}
	if len(sample) > 0 && zeroBytes >= len(sample)/4 {
		return string(utf16.Decode(bytesToUint16s(raw, true)))
	}
	return string(raw)
}

func bytesToUint16s(raw []byte, littleEndian bool) []uint16 {
	if len(raw)%2 == 1 {
		raw = raw[:len(raw)-1]
	}
	words := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		if littleEndian {
			words = append(words, uint16(raw[i])|uint16(raw[i+1])<<8)
		} else {
			words = append(words, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
	}
	return words
}

func summarizeShellProgressLine(chunk string) string {
	lines := strings.Split(strings.ReplaceAll(chunk, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if len(line) > 160 {
			return line[:160] + "..."
		}
		return line
	}
	return ""
}

func (c *shellOutputCollector) consumeProgressChunk(chunk []byte) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lastLine := ""
	sawDelimiter := false
	for _, b := range chunk {
		switch b {
		case '\r', '\n':
			sawDelimiter = true
			line := summarizeShellProgressLine(string(c.lineBuffer))
			if line != "" {
				lastLine = line
			}
			c.lineBuffer = c.lineBuffer[:0]
		default:
			c.lineBuffer = append(c.lineBuffer, b)
			if len(c.lineBuffer) > 2048 {
				c.lineBuffer = append([]byte(nil), c.lineBuffer[len(c.lineBuffer)-2048:]...)
			}
		}
	}
	if lastLine == "" && len(c.lineBuffer) > 0 {
		lastLine = summarizeShellProgressLine(string(c.lineBuffer))
	}
	return lastLine, sawDelimiter
}

func formatShellByteCount(size int) string {
	if size >= 1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(size)/float64(1024*1024))
	}
	if size >= 1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%d B", size)
}

func assessShellCommandMutation(command string) shellCommandAssessment {
	unquoted := shellCommandWithoutQuotedLiterals(command)
	lower := strings.ToLower(strings.TrimSpace(unquoted))
	if lower == "" {
		return shellCommandAssessment{Class: shellMutationReadOnly, Reason: "empty command"}
	}
	if shellCommandContainsPowerShellStopParsing(command) {
		return shellCommandAssessment{Class: shellMutationUnsupported, Reason: "PowerShell stop-parsing token --% cannot be safely analyzed"}
	}
	if shellCommandContainsPowerShellEncodedCommand(command) {
		return shellCommandAssessment{Class: shellMutationUnsupported, Reason: "PowerShell EncodedCommand payload cannot be safely analyzed"}
	}
	if reason := shellCommandManualWorkspaceWriteReason(command); reason != "" {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: reason}
	}
	if reason := shellCommandUnsafeReadOnlyToolReason(command); reason != "" {
		return shellCommandAssessment{Class: shellMutationUnsafe, Reason: reason}
	}

	tokens := shellCommandAssessmentTokens(command)
	if len(tokens) == 0 {
		return shellCommandAssessment{Class: shellMutationReadOnly, Reason: "no workspace write markers detected"}
	}
	if shellCommandMutatesGitState(lower) {
		return shellCommandAssessment{Class: shellMutationGitMutation, Reason: "command mutates git state"}
	}

	if shellCommandHasPrefixTokens(tokens,
		[]string{"set-content"},
		[]string{"add-content"},
		[]string{"out-file"},
		[]string{"move-item"},
		[]string{"copy-item"},
		[]string{"remove-item"},
		[]string{"rename-item"},
		[]string{"new-item"},
		[]string{"mkdir"},
		[]string{"md"},
		[]string{"del"},
		[]string{"erase"},
		[]string{"copy"},
		[]string{"move"},
		[]string{"ren"},
		[]string{"rename"},
		[]string{"rm"},
		[]string{"mv"},
		[]string{"cp"},
		[]string{"touch"},
		[]string{"black"},
		[]string{"go", "generate"},
		[]string{"go", "mod", "tidy"},
		[]string{"go", "mod", "vendor"},
		[]string{"go", "get"},
		[]string{"cargo", "add"},
		[]string{"cargo", "vendor"},
		[]string{"dotnet", "add"},
		[]string{"npm", "install"},
		[]string{"npm", "add"},
		[]string{"pnpm", "install"},
		[]string{"pnpm", "add"},
		[]string{"yarn", "install"},
		[]string{"yarn", "add"},
		[]string{"bun", "install"},
	) {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "command commonly writes build outputs or workspace-managed files"}
	}
	if tokens[0] == "sed" && shellCommandContainsToken(tokens, "-i") {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "sed -i edits files in place"}
	}
	if tokens[0] == "perl" && shellCommandContainsTokenPrefix(tokens, "-pi") {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "perl -pi edits files in place"}
	}
	if shellCommandHasPrefixTokens(tokens,
		[]string{"gofmt"},
		[]string{"goimports"},
		[]string{"clang-format"},
	) && (shellCommandContainsToken(tokens, "-w") || shellCommandContainsTokenPrefix(tokens, "-w=") || shellCommandContainsToken(tokens, "-i")) {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "formatter command edits workspace files in place"}
	}
	if shellCommandHasPrefixTokens(tokens,
		[]string{"prettier"},
	) && shellCommandContainsToken(tokens, "--write") {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "formatter command edits workspace files in place"}
	}
	if shellCommandHasPrefixTokens(tokens,
		[]string{"ruff", "format"},
	) {
		return shellCommandAssessment{Class: shellMutationWorkspaceWrite, Reason: "formatter command edits workspace files in place"}
	}

	if shellCommandHasPrefixTokens(tokens,
		[]string{"go", "list"},
		[]string{"go", "mod", "download"},
		[]string{"git", "status"},
		[]string{"git", "diff"},
		[]string{"npm", "view"},
		[]string{"pip", "show"},
		[]string{"pip", "list"},
	) {
		return shellCommandAssessment{Class: shellMutationCacheOnly, Reason: "command is read-only or writes only to external caches"}
	}

	verificationPrefixes := [][]string{
		{"go", "test"},
		{"go", "build"},
		{"cargo", "test"},
		{"cargo", "check"},
		{"cargo", "build"},
		{"pytest"},
		{"ctest"},
		{"cmake", "--build"},
		{"msbuild"},
		{"ninja"},
		{"dotnet", "test"},
		{"dotnet", "build"},
		{"npm", "test"},
		{"pnpm", "test"},
		{"yarn", "test"},
		{"bun", "test"},
	}
	if shellCommandInvokesVerificationCommand(tokens, verificationPrefixes...) {
		return shellCommandAssessment{Class: shellMutationVerificationArtifacts, Reason: "command may write build or test artifacts under the workspace"}
	}

	if shellCommandHasPrefixTokens(tokens,
		[]string{"go", "install"},
		[]string{"pip", "install"},
		[]string{"winget", "install"},
		[]string{"choco", "install"},
		[]string{"apt", "install"},
		[]string{"brew", "install"},
		[]string{"uv", "tool", "install"},
	) {
		return shellCommandAssessment{Class: shellMutationExternalInstall, Reason: "command installs tools outside the workspace"}
	}

	return shellCommandAssessment{Class: shellMutationReadOnly, Reason: "no workspace write markers detected"}
}

func shellCommandUnsupportedSyntaxError(toolName string, assessment shellCommandAssessment) error {
	return fmt.Errorf("%s command uses unsupported shell syntax that cannot be safely analyzed (%s)", toolName, assessment.Reason)
}

func shellCommandUnsafeError(toolName string, assessment shellCommandAssessment) error {
	return fmt.Errorf("%s command uses a read-only-looking tool form that can execute external commands; use an explicit shell command only after reviewing the risk (%s)", toolName, assessment.Reason)
}

func shellCommandUnsafeReadOnlyToolReason(command string) string {
	if reason := shellCommandUnsafeRipgrepReason(command); reason != "" {
		return reason
	}
	if reason := shellCommandUnsafeGitReason(command); reason != "" {
		return reason
	}
	if reason := shellCommandUnsafeShellExpansionReason(command); reason != "" {
		return reason
	}
	return ""
}

func shellCommandUnsafeShellExpansionReason(command string) string {
	tokens := splitShellCommandWords(shellCommandSeparatorsForTokenizing(strings.ToLower(strings.TrimSpace(command))))
	for start := 0; start < len(tokens); {
		for start < len(tokens) && shellCommandTokenIsSegmentDelimiter(tokens[start]) {
			start++
		}
		end := start
		for end < len(tokens) && !shellCommandTokenIsSegmentDelimiter(tokens[end]) {
			end++
		}
		if start < end {
			payload := shellCommandPOSIXShellPayload(tokens[start:end])
			if payload != "" && shellCommandContainsUnquotedShellExpansion(payload) {
				return "POSIX shell expansion can rewrite arguments for a read-only-looking command"
			}
		}
		start = end + 1
	}
	return ""
}

func shellCommandPOSIXShellPayload(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	base := shellTokenBaseName(tokens[0])
	if base != "bash" && base != "sh" && base != "zsh" {
		return ""
	}
	unwrapped := unwrapShellCommandLCWrapperTokens(tokens)
	if len(unwrapped) == len(tokens) {
		return ""
	}
	return strings.Join(unwrapped, " ")
}

func shellCommandContainsUnquotedShellExpansion(command string) bool {
	quote := byte(0)
	atWordStart := true
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			atWordStart = false
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			atWordStart = false
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			atWordStart = true
			continue
		}
		if ch == '~' && atWordStart {
			return true
		}
		switch ch {
		case '*', '?', '[', ']', '{', '}':
			return true
		}
		atWordStart = false
	}
	return false
}

func shellCommandUnsafeGitReason(command string) string {
	tokens := shellCommandRawInspectionTokens(command)
	for start := 0; start < len(tokens); {
		for start < len(tokens) && shellCommandTokenIsSegmentDelimiter(tokens[start]) {
			start++
		}
		end := start
		for end < len(tokens) && !shellCommandTokenIsSegmentDelimiter(tokens[end]) {
			end++
		}
		if start < end {
			if reason := shellCommandUnsafeGitSegmentReason(tokens[start:end]); reason != "" {
				return reason
			}
		}
		start = end + 1
	}
	return ""
}

func shellCommandUnsafeGitSegmentReason(tokens []string) string {
	if len(tokens) == 0 || shellTokenBaseName(tokens[0]) != "git" {
		return ""
	}

	subcommandIndex := -1
	for i := 1; i < len(tokens); i++ {
		arg := tokens[i]
		if shellCommandGitHasUnsafeGlobalOption(arg) {
			return fmt.Sprintf("git global option %s can redirect repository, config, helper, or pager behavior", arg)
		}
		if shellCommandGitGlobalOptionHasInlineValue(arg) {
			continue
		}
		if shellCommandGitGlobalOptionTakesValue(arg) {
			i++
			continue
		}
		if arg == "--" || strings.HasPrefix(arg, "-") {
			continue
		}
		subcommandIndex = i
		break
	}

	if subcommandIndex == -1 {
		return ""
	}

	subcommand := tokens[subcommandIndex]
	args := tokens[subcommandIndex+1:]
	switch subcommand {
	case "status", "log", "diff", "show":
		if reason := shellCommandUnsafeGitSubcommandOptionReason(args); reason != "" {
			return reason
		}
	case "cat-file":
		if reason := shellCommandUnsafeGitSubcommandOptionReason(args); reason != "" {
			return reason
		}
		for _, arg := range args {
			if arg == "--filters" || strings.HasPrefix(arg, "--filters=") {
				return "git cat-file --filters can invoke configured content filters"
			}
		}
	case "branch":
		if reason := shellCommandUnsafeGitSubcommandOptionReason(args); reason != "" {
			return reason
		}
		if !shellCommandGitBranchArgsReadOnly(args) {
			return "git branch arguments can create, rename, or delete branches"
		}
	}
	return ""
}

func shellCommandUnsafeGitSubcommandOptionReason(args []string) string {
	for _, arg := range args {
		switch {
		case arg == "--output" || strings.HasPrefix(arg, "--output="):
			return "git --output can write command output to a file"
		case arg == "--ext-diff":
			return "git --ext-diff can execute an external diff command"
		case arg == "--textconv":
			return "git --textconv can execute configured text conversion filters"
		case arg == "--exec" || strings.HasPrefix(arg, "--exec="):
			return "git --exec can redirect helper execution"
		}
	}
	return ""
}

func shellCommandGitHasUnsafeGlobalOption(arg string) bool {
	switch {
	case arg == "-c" || strings.HasPrefix(arg, "-c"):
		return true
	case arg == "-C" || strings.HasPrefix(arg, "-C"):
		return true
	case arg == "-p":
		return true
	case arg == "--config-env" || strings.HasPrefix(arg, "--config-env="):
		return true
	case arg == "--exec-path" || strings.HasPrefix(arg, "--exec-path="):
		return true
	case arg == "--git-dir" || strings.HasPrefix(arg, "--git-dir="):
		return true
	case arg == "--namespace" || strings.HasPrefix(arg, "--namespace="):
		return true
	case arg == "--paginate":
		return true
	case arg == "--super-prefix" || strings.HasPrefix(arg, "--super-prefix="):
		return true
	case arg == "--work-tree" || strings.HasPrefix(arg, "--work-tree="):
		return true
	default:
		return false
	}
}

func shellCommandGitGlobalOptionTakesValue(arg string) bool {
	switch arg {
	case "-c", "-C", "--config-env", "--exec-path", "--git-dir", "--namespace", "--super-prefix", "--work-tree":
		return true
	default:
		return false
	}
}

func shellCommandGitGlobalOptionHasInlineValue(arg string) bool {
	switch {
	case (strings.HasPrefix(arg, "-c") || strings.HasPrefix(arg, "-C")) && len(arg) > 2:
		return true
	case strings.HasPrefix(arg, "--config-env="):
		return true
	case strings.HasPrefix(arg, "--exec-path="):
		return true
	case strings.HasPrefix(arg, "--git-dir="):
		return true
	case strings.HasPrefix(arg, "--namespace="):
		return true
	case strings.HasPrefix(arg, "--super-prefix="):
		return true
	case strings.HasPrefix(arg, "--work-tree="):
		return true
	default:
		return false
	}
}

func shellCommandGitBranchArgsReadOnly(args []string) bool {
	if len(args) == 0 {
		return true
	}
	sawReadOnlyFlag := false
	for _, arg := range args {
		switch arg {
		case "--list", "-l", "--show-current", "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose":
			sawReadOnlyFlag = true
		default:
			if strings.HasPrefix(arg, "--format=") {
				sawReadOnlyFlag = true
				continue
			}
			return false
		}
	}
	return sawReadOnlyFlag
}

func shellCommandUnsafeRipgrepReason(command string) string {
	tokens := shellCommandRawInspectionTokens(command)
	for start := 0; start < len(tokens); {
		for start < len(tokens) && shellCommandTokenIsSegmentDelimiter(tokens[start]) {
			start++
		}
		end := start
		for end < len(tokens) && !shellCommandTokenIsSegmentDelimiter(tokens[end]) {
			end++
		}
		if start < end {
			if reason := shellCommandUnsafeRipgrepSegmentReason(tokens[start:end]); reason != "" {
				return reason
			}
		}
		start = end + 1
	}
	return ""
}

func shellCommandUnsafeRipgrepSegmentReason(tokens []string) string {
	if len(tokens) == 0 || shellTokenBaseName(tokens[0]) != "rg" {
		return ""
	}
	for _, arg := range tokens[1:] {
		switch {
		case arg == "--pre" || strings.HasPrefix(arg, "--pre="):
			return "ripgrep --pre can execute an arbitrary preprocessor command"
		case arg == "--hostname-bin" || strings.HasPrefix(arg, "--hostname-bin="):
			return "ripgrep --hostname-bin can execute an arbitrary command"
		case arg == "--search-zip" || arg == "-z":
			return "ripgrep archive search can invoke external helper programs"
		}
	}
	return ""
}

func shellCommandManualWorkspaceWriteReason(command string) string {
	unquotedLower := strings.ToLower(shellCommandWithoutQuotedLiterals(command))
	if shellCommandHasWorkspaceWriteRedirection(command) || strings.Contains(unquotedLower, "| tee ") || strings.HasPrefix(unquotedLower, "tee ") {
		return "output redirection or tee can create workspace files"
	}
	if reason := shellCommandNestedWorkspaceWriteRedirectionReason(command); reason != "" {
		return reason
	}
	if shellManualWorkspaceWriteCommandPattern.MatchString(unquotedLower) {
		return "manual shell file-write primitive can modify workspace files"
	}
	if shellCommandContainsOutFileArgument(unquotedLower) {
		return "PowerShell -OutFile argument can create workspace files"
	}
	compactUnquoted := compactShellMutationText(unquotedLower)
	compactRaw := compactShellMutationText(command)
	dotNetMutators := []string{
		"[system.io.file]::writealltext",
		"[system.io.file]::writealllines",
		"[system.io.file]::writeallbytes",
		"[system.io.file]::appendalltext",
		"[system.io.file]::appendalllines",
		"[system.io.file]::create",
		"[system.io.file]::delete",
		"[system.io.file]::move",
		"[system.io.file]::copy",
		"[system.io.file]::replace",
		"[io.file]::writealltext",
		"[io.file]::writealllines",
		"[io.file]::writeallbytes",
		"[io.file]::appendalltext",
		"[io.file]::appendalllines",
		"[io.file]::create",
		"[io.file]::delete",
		"[io.file]::move",
		"[io.file]::copy",
		"[io.file]::replace",
	}
	for _, marker := range dotNetMutators {
		if strings.Contains(compactUnquoted, marker) {
			return ".NET file mutation API can modify workspace files"
		}
	}
	if shellCommandInvokesNestedShell(unquotedLower) {
		rawLower := strings.ToLower(command)
		if shellNestedManualWorkspaceWriteCommandPattern.MatchString(rawLower) {
			return "nested shell command contains a file-write primitive"
		}
		for _, marker := range dotNetMutators {
			if strings.Contains(compactRaw, marker) {
				return "nested shell command contains a .NET file mutation API"
			}
		}
	}
	return ""
}

func shellCommandNestedWorkspaceWriteRedirectionReason(command string) string {
	tokens := splitShellCommandWords(shellCommandSeparatorsForTokenizing(strings.ToLower(strings.TrimSpace(command))))
	for start := 0; start < len(tokens); {
		for start < len(tokens) && shellCommandTokenIsSegmentDelimiter(tokens[start]) {
			start++
		}
		end := start
		for end < len(tokens) && !shellCommandTokenIsSegmentDelimiter(tokens[end]) {
			end++
		}
		if start < end {
			payload := shellCommandNestedShellPayload(tokens[start:end])
			if payload != "" && shellCommandHasWorkspaceWriteRedirection(payload) {
				return "nested shell command contains output redirection"
			}
		}
		start = end + 1
	}
	return ""
}

func shellCommandNestedShellPayload(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	base := shellTokenBaseName(tokens[0])
	switch base {
	case "cmd", "powershell", "pwsh":
		unwrapped := unwrapShellCommandWrapperTokens(tokens)
		if len(unwrapped) == len(tokens) {
			return ""
		}
		return strings.Join(unwrapped, " ")
	case "bash", "sh", "zsh":
		unwrapped := unwrapShellCommandLCWrapperTokens(tokens)
		if len(unwrapped) == len(tokens) {
			return ""
		}
		return strings.Join(unwrapped, " ")
	default:
		return ""
	}
}

func shellCommandContainsOutFileArgument(command string) bool {
	tokens := splitShellCommandWords(command)
	for _, token := range tokens {
		if token == "-outfile" || strings.HasPrefix(token, "-outfile:") {
			return true
		}
	}
	return false
}

func shellCommandInvokesNestedShell(command string) bool {
	tokens := splitShellCommandWords(command)
	for _, token := range tokens {
		switch strings.TrimSuffix(token, ".exe") {
		case "powershell", "pwsh", "cmd", "bash", "sh", "zsh":
			return true
		}
	}
	return false
}

func shellCommandAssessmentTokens(command string) []string {
	unquoted := strings.ToLower(strings.TrimSpace(shellCommandWithoutQuotedLiterals(command)))
	tokens := splitShellCommandWords(unquoted)
	rawTokens := shellCommandRawInspectionTokens(command)
	unwrappedTokens := unwrapShellCommandWrapperTokens(tokens)
	if len(rawTokens) > 0 && shellCommandShouldPreferRawTokens(unwrappedTokens) {
		return rawTokens
	}
	return unwrappedTokens
}

func shellCommandRawInspectionTokens(command string) []string {
	rawTokens := splitShellCommandWords(shellCommandSeparatorsForTokenizing(strings.ToLower(strings.TrimSpace(command))))
	rawTokens = unwrapShellCommandWrapperTokens(rawTokens)
	if shellCommandTokensStartPOSIXShell(rawTokens) {
		rawTokens = unwrapShellCommandLCWrapperInspectionTokens(rawTokens)
	} else {
		rawTokens = unwrapShellCommandLCWrapperTokens(rawTokens)
	}
	rawTokens = retokenizeNestedShellPayload(rawTokens)
	return rawTokens
}

func shellCommandContainsPowerShellStopParsing(command string) bool {
	if shellCommandContainsUnquotedStopParsingToken(command) {
		return true
	}
	rawTokens := splitShellCommandWords(shellCommandSeparatorsForTokenizing(strings.ToLower(strings.TrimSpace(command))))
	for i := 0; i < len(rawTokens); i++ {
		if !shellCommandTokenCanBeginNestedCommand(rawTokens, i) {
			continue
		}
		base := shellTokenBaseName(rawTokens[i])
		if base != "powershell" && base != "pwsh" {
			continue
		}
		payloadTokens := unwrapShellCommandWrapperTokens(rawTokens[i:])
		if len(payloadTokens) == len(rawTokens[i:]) {
			continue
		}
		if len(payloadTokens) == 1 {
			if shellCommandContainsUnquotedStopParsingToken(payloadTokens[0]) {
				return true
			}
			continue
		}
		if shellCommandContainsToken(payloadTokens, "--%") {
			return true
		}
	}
	return false
}

func shellCommandContainsPowerShellEncodedCommand(command string) bool {
	rawTokens := splitShellCommandWords(shellCommandSeparatorsForTokenizing(strings.ToLower(strings.TrimSpace(command))))
	for i := 0; i < len(rawTokens); i++ {
		if !shellCommandTokenCanBeginNestedCommand(rawTokens, i) {
			continue
		}
		base := shellTokenBaseName(rawTokens[i])
		if base != "powershell" && base != "pwsh" {
			continue
		}
		for j := i + 1; j < len(rawTokens); j++ {
			if shellCommandTokenIsSegmentDelimiter(rawTokens[j]) {
				break
			}
			if shellCommandIsPowerShellEncodedCommandFlag(rawTokens[j]) {
				return true
			}
		}
	}
	return false
}

func shellCommandIsPowerShellEncodedCommandFlag(token string) bool {
	normalized := strings.TrimSpace(strings.Trim(token, `"'`))
	if strings.HasPrefix(normalized, "/") {
		normalized = "-" + strings.TrimPrefix(normalized, "/")
	}
	if idx := strings.IndexByte(normalized, ':'); idx >= 0 {
		normalized = normalized[:idx]
	}
	return len(normalized) >= 2 && strings.HasPrefix("-encodedcommand", normalized)
}

func shellCommandIsPowerShellCommandFlag(token string) bool {
	normalized := strings.TrimSpace(strings.Trim(token, `"'`))
	normalized = strings.ToLower(normalized)
	return normalized == "-command" || normalized == "/command" || normalized == "-c"
}

func shellCommandPowerShellInlineCommandPayload(token string) (string, bool) {
	normalized := strings.TrimSpace(strings.Trim(token, `"'`))
	lower := strings.ToLower(normalized)
	for _, prefix := range []string{"-command:", "/command:"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(normalized[len(prefix):]), true
		}
	}
	return "", false
}

func shellCommandContainsUnquotedStopParsingToken(command string) bool {
	const marker = "--%"
	quote := byte(0)
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if !strings.HasPrefix(command[i:], marker) {
			continue
		}
		if shellCommandStopParsingBoundaryBefore(command, i) && shellCommandStopParsingBoundaryAfter(command, i+len(marker)) {
			return true
		}
	}
	return false
}

func shellCommandStopParsingBoundaryBefore(command string, idx int) bool {
	if idx <= 0 {
		return true
	}
	return shellCommandStopParsingBoundaryByte(command[idx-1])
}

func shellCommandStopParsingBoundaryAfter(command string, idx int) bool {
	if idx >= len(command) {
		return true
	}
	return shellCommandStopParsingBoundaryByte(command[idx])
}

func shellCommandStopParsingBoundaryByte(ch byte) bool {
	switch ch {
	case ' ', '\t', '\r', '\n', ';', '|', '&', '(', ')', '{', '}':
		return true
	default:
		return false
	}
}

func shellCommandTokenCanBeginNestedCommand(tokens []string, idx int) bool {
	if idx == 0 {
		return true
	}
	prev := tokens[idx-1]
	if shellCommandTokenIsSegmentDelimiter(prev) {
		return true
	}
	if prev == "/c" || prev == "-command" || prev == "/command" || prev == "-c" {
		return true
	}
	return false
}

func shellCommandShouldPreferRawTokens(tokens []string) bool {
	first := ""
	for _, token := range tokens {
		if shellCommandTokenIsSegmentDelimiter(token) {
			continue
		}
		first = strings.TrimSpace(token)
		break
	}
	if first == "" {
		return true
	}
	if strings.HasPrefix(first, "-") {
		return true
	}
	if strings.Contains(first, ".") || strings.ContainsAny(first, `/\`) {
		return true
	}
	return false
}

func unwrapShellCommandWrapperTokens(tokens []string) []string {
unwrap:
	for {
		if len(tokens) >= 3 && shellTokenBaseName(tokens[0]) == "cmd" && tokens[1] == "/s" && tokens[2] == "/c" {
			tokens = tokens[3:]
			continue
		}
		if len(tokens) >= 2 && shellTokenBaseName(tokens[0]) == "cmd" && tokens[1] == "/c" {
			tokens = tokens[2:]
			continue
		}
		if len(tokens) >= 2 {
			base := shellTokenBaseName(tokens[0])
			if base == "powershell" || base == "pwsh" {
				for i := 1; i < len(tokens); i++ {
					token := tokens[i]
					if payload, ok := shellCommandPowerShellInlineCommandPayload(token); ok {
						if payload == "" {
							tokens = nil
						} else {
							tokens = []string{payload}
						}
						continue unwrap
					}
					if shellCommandIsPowerShellCommandFlag(token) {
						if i+1 < len(tokens) {
							tokens = tokens[i+1:]
						} else {
							tokens = nil
						}
						continue unwrap
					}
				}
			}
		}
		return tokens
	}
}

func unwrapShellCommandLCWrapperTokens(tokens []string) []string {
	if len(tokens) < 3 {
		return tokens
	}
	base := shellTokenBaseName(tokens[0])
	if base != "bash" && base != "sh" && base != "zsh" {
		return tokens
	}
	for i := 1; i < len(tokens); i++ {
		switch tokens[i] {
		case "-c", "-lc":
			if i+1 < len(tokens) {
				return tokens[i+1 : i+2]
			}
			return nil
		}
	}
	return tokens
}

func shellCommandTokensStartPOSIXShell(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	base := shellTokenBaseName(tokens[0])
	return base == "bash" || base == "sh" || base == "zsh"
}

func unwrapShellCommandLCWrapperInspectionTokens(tokens []string) []string {
	if len(tokens) < 3 || !shellCommandTokensStartPOSIXShell(tokens) {
		return tokens
	}
	for i := 1; i < len(tokens); i++ {
		switch tokens[i] {
		case "-c", "-lc":
			if i+1 < len(tokens) {
				return splitPOSIXShellCommandWords(shellCommandSeparatorsForTokenizing(tokens[i+1]))
			}
			return nil
		}
	}
	return tokens
}

func retokenizeNestedShellPayload(tokens []string) []string {
	if len(tokens) != 1 {
		return tokens
	}
	payload := strings.TrimSpace(tokens[0])
	if !strings.ContainsAny(payload, " \t;&|()") {
		return tokens
	}
	return splitShellCommandWords(shellCommandSeparatorsForTokenizing(payload))
}

func splitPOSIXShellCommandWords(command string) []string {
	var tokens []string
	var current strings.Builder
	quote := byte(0)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(current.String()))
		current.Reset()
	}
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote == '\'' {
			if ch == quote {
				quote = 0
				continue
			}
			current.WriteByte(ch)
			continue
		}
		if ch == '"' {
			if quote == '"' {
				quote = 0
			} else if quote == 0 {
				quote = '"'
			} else {
				current.WriteByte(ch)
			}
			continue
		}
		if ch == '\'' && quote == 0 {
			quote = '\''
			continue
		}
		if ch == '\\' {
			if i+1 >= len(command) {
				current.WriteByte(ch)
				continue
			}
			next := command[i+1]
			i++
			if quote == '"' {
				if next == '\n' {
					current.WriteByte(ch)
					current.WriteByte(next)
					continue
				}
				switch next {
				case '"', '\\', '$', '`':
					current.WriteByte(next)
				default:
					current.WriteByte(ch)
					current.WriteByte(next)
				}
				continue
			}
			current.WriteByte(next)
			continue
		}
		if quote == 0 {
			switch ch {
			case ' ', '\t', '\r', '\n':
				flush()
				continue
			}
		}
		current.WriteByte(ch)
	}
	flush()
	return tokens
}

func shellCommandSeparatorsForTokenizing(command string) string {
	replacer := strings.NewReplacer(
		"&&", " && ",
		"||", " || ",
		"&", " & ",
		";", " ; ",
		"|", " | ",
		"(", " ( ",
		")", " ) ",
	)
	return replacer.Replace(command)
}

func shellTokenBaseName(token string) string {
	base := strings.TrimSpace(strings.TrimSuffix(token, ".exe"))
	base = strings.Trim(base, `"'`)
	if idx := strings.LastIndexAny(base, `/\`); idx >= 0 && idx+1 < len(base) {
		base = base[idx+1:]
	}
	return base
}

func compactShellMutationText(command string) string {
	lower := strings.ToLower(command)
	var b strings.Builder
	b.Grow(len(lower))
	for _, ch := range lower {
		switch ch {
		case ' ', '\t', '\r', '\n', '`':
			continue
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

func shellCommandWithoutQuotedLiterals(command string) string {
	var b strings.Builder
	b.Grow(len(command))
	quote := rune(0)
	for _, ch := range command {
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			switch ch {
			case '\r', '\n', ';', '|', '&', '(', ')', '{', '}':
				b.WriteRune(ch)
			default:
				b.WriteRune(' ')
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
			b.WriteRune(' ')
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

func shellCommandMutatesGitState(command string) bool {
	tokens := shellCommandRawInspectionTokens(command)
	if len(tokens) == 0 {
		return false
	}
	for start := 0; start < len(tokens); {
		for start < len(tokens) && shellCommandTokenIsSegmentDelimiter(tokens[start]) {
			start++
		}
		end := start
		for end < len(tokens) && !shellCommandTokenIsSegmentDelimiter(tokens[end]) {
			end++
		}
		if start < end && shellCommandGitSegmentMutatesState(tokens[start:end]) {
			return true
		}
		start = end + 1
	}
	return false
}

func shellCommandGitSegmentMutatesState(tokens []string) bool {
	if len(tokens) == 0 || shellTokenBaseName(tokens[0]) != "git" {
		return false
	}
	subcommandIndex := shellCommandGitSubcommandIndex(tokens)
	if subcommandIndex == -1 {
		return false
	}
	subcommand := tokens[subcommandIndex]
	switch subcommand {
	case "add", "am", "apply", "checkout", "cherry-pick", "clean", "clone", "commit", "config", "init", "merge", "mv", "pull", "push", "rebase", "reset", "restore", "revert", "rm", "stash", "switch", "tag":
		return true
	case "branch":
		return !shellCommandGitBranchArgsReadOnly(tokens[subcommandIndex+1:])
	default:
		return false
	}
}

func shellCommandGitSubcommandIndex(tokens []string) int {
	if len(tokens) == 0 || shellTokenBaseName(tokens[0]) != "git" {
		return -1
	}
	for i := 1; i < len(tokens); i++ {
		arg := tokens[i]
		if shellCommandGitGlobalOptionHasInlineValue(arg) {
			continue
		}
		if shellCommandGitGlobalOptionTakesValue(arg) {
			i++
			continue
		}
		if arg == "--" || strings.HasPrefix(arg, "-") {
			continue
		}
		return i
	}
	return -1
}

func shellCommandHasWorkspaceWriteRedirection(command string) bool {
	cleaned := strings.TrimSpace(command)
	if cleaned == "" {
		return false
	}
	quote := byte(0)
	for i := 0; i < len(cleaned); i++ {
		ch := cleaned[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch != '>' {
			continue
		}
		target, ok := shellCommandRedirectionTarget(cleaned, i+1)
		if !ok {
			return true
		}
		target = strings.ToLower(strings.TrimSpace(strings.Trim(target, `"'`)))
		switch target {
		case "/dev/null", "$null", "nul":
			continue
		default:
			if strings.HasPrefix(target, "&") {
				continue
			}
			return true
		}
	}
	return false
}

func shellCommandRedirectionTarget(command string, start int) (string, bool) {
	i := start
	if i < len(command) && command[i] == '>' {
		i++
	}
	for i < len(command) && (command[i] == ' ' || command[i] == '\t' || command[i] == '\r' || command[i] == '\n') {
		i++
	}
	if i >= len(command) {
		return "", false
	}
	if command[i] == '\'' || command[i] == '"' {
		quote := command[i]
		i++
		targetStart := i
		for i < len(command) && command[i] != quote {
			i++
		}
		if i >= len(command) {
			return strings.TrimSpace(command[targetStart:]), true
		}
		return strings.TrimSpace(command[targetStart:i]), true
	}
	if command[i] == '&' {
		targetStart := i
		i++
		for i < len(command) && !shellCommandRedirectionTargetBoundary(command[i]) {
			i++
		}
		return strings.TrimSpace(command[targetStart:i]), true
	}
	targetStart := i
	for i < len(command) && !shellCommandRedirectionTargetBoundary(command[i]) {
		i++
	}
	return strings.TrimSpace(command[targetStart:i]), true
}

func shellCommandRedirectionTargetBoundary(ch byte) bool {
	switch ch {
	case ' ', '\t', '\r', '\n', ';', '|', '&', '(', ')':
		return true
	default:
		return false
	}
}

func splitShellCommandWords(command string) []string {
	var tokens []string
	var current strings.Builder
	quote := byte(0)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, strings.ToLower(current.String()))
		current.Reset()
	}
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote != 0 {
			if ch == '\\' && i+1 < len(command) && command[i+1] == quote {
				current.WriteByte(command[i+1])
				i++
				continue
			}
			if ch == quote {
				quote = 0
				continue
			}
			current.WriteByte(ch)
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			current.WriteByte(ch)
		}
	}
	flush()
	return tokens
}

func shellCommandHasPrefixTokens(tokens []string, prefixes ...[]string) bool {
	for _, prefix := range prefixes {
		if len(tokens) < len(prefix) {
			continue
		}
		matched := true
		for i := 0; i < len(prefix); i++ {
			token := tokens[i]
			if i == 0 {
				token = shellTokenBaseName(token)
			}
			if token != prefix[i] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func shellCommandHasSegmentPrefixTokens(tokens []string, prefixes ...[]string) bool {
	start := 0
	for start < len(tokens) {
		for start < len(tokens) && shellCommandTokenIsSegmentDelimiter(tokens[start]) {
			start++
		}
		end := start
		for end < len(tokens) && !shellCommandTokenIsSegmentDelimiter(tokens[end]) {
			end++
		}
		if start < end && shellCommandHasPrefixTokens(tokens[start:end], prefixes...) {
			return true
		}
		start = end + 1
	}
	return false
}

func shellCommandInvokesVerificationCommand(tokens []string, prefixes ...[]string) bool {
	if shellCommandHasPrefixTokens(tokens, prefixes...) || shellCommandHasSegmentPrefixTokens(tokens, prefixes...) {
		return true
	}
	aliases := shellCommandVerificationAliases(tokens, prefixes...)
	if len(aliases) == 0 {
		return false
	}
	for i, token := range tokens {
		if token != "&" || i+1 >= len(tokens) {
			continue
		}
		if shellCommandTokenMatchesAlias(tokens[i+1], aliases) {
			return true
		}
	}
	return false
}

func shellCommandVerificationAliases(tokens []string, prefixes ...[]string) map[string]bool {
	aliases := map[string]bool{}
	for i := 0; i+2 < len(tokens); i++ {
		name := strings.TrimSpace(tokens[i])
		if !strings.HasPrefix(name, "$") || tokens[i+1] != "=" || shellTokenBaseName(tokens[i+2]) != "get-command" {
			continue
		}
		if shellCommandSegmentContainsVerificationExecutable(tokens[i+3:], prefixes...) {
			aliases[name] = true
		}
	}
	return aliases
}

func shellCommandSegmentContainsVerificationExecutable(tokens []string, prefixes ...[]string) bool {
	for _, token := range tokens {
		if shellCommandTokenIsSegmentDelimiter(token) {
			return false
		}
		if shellCommandTokenMatchesVerificationExecutable(token, prefixes...) {
			return true
		}
	}
	return false
}

func shellCommandTokenMatchesVerificationExecutable(token string, prefixes ...[]string) bool {
	base := shellTokenBaseName(token)
	for _, prefix := range prefixes {
		if len(prefix) > 0 && base == prefix[0] {
			return true
		}
	}
	return false
}

func shellCommandTokenMatchesAlias(token string, aliases map[string]bool) bool {
	token = strings.TrimSpace(strings.Trim(token, `"'`))
	if idx := strings.IndexAny(token, ".["); idx >= 0 {
		token = token[:idx]
	}
	return aliases[token]
}

func shellCommandTokenIsSegmentDelimiter(token string) bool {
	switch strings.TrimSpace(token) {
	case ";", "&", "&&", "||", "|", "(", ")":
		return true
	default:
		return false
	}
}

func shellCommandTokensHaveCommandWord(tokens []string) bool {
	for _, token := range tokens {
		if !shellCommandTokenIsSegmentDelimiter(token) {
			return true
		}
	}
	return false
}

func shellCommandContainsToken(tokens []string, want string) bool {
	for _, token := range tokens {
		if token == want {
			return true
		}
	}
	return false
}

func shellCommandContainsTokenPrefix(tokens []string, prefix string) bool {
	for _, token := range tokens {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

func hookCommandRiskTags(command string) []string {
	lower := strings.ToLower(strings.TrimSpace(command))
	var tags []string
	if strings.Contains(lower, "bcdedit") || strings.Contains(lower, "verifier") {
		tags = append(tags, "windows")
	}
	if strings.Contains(lower, "signtool") || strings.Contains(lower, "symchk") {
		tags = append(tags, "signing")
	}
	if strings.Contains(lower, "fltmc") || strings.Contains(lower, ".sys") {
		tags = append(tags, "driver")
	}
	return uniqueStrings(tags)
}

func hookFileTags(path string) []string {
	lower := strings.ToLower(filepath.ToSlash(path))
	var tags []string
	switch filepath.Ext(lower) {
	case ".c", ".cc", ".cpp", ".h", ".hpp":
		tags = append(tags, "cpp")
	case ".go":
		tags = append(tags, "go")
	case ".sys", ".inf", ".cat":
		tags = append(tags, "driver")
	}
	if strings.Contains(lower, "/driver/") || strings.HasSuffix(lower, ".sys") || strings.HasSuffix(lower, ".inf") || strings.HasSuffix(lower, ".cat") {
		tags = append(tags, "driver")
	}
	if strings.Contains(lower, "kernel") || strings.Contains(lower, "/driver/") || strings.HasSuffix(lower, ".sys") {
		tags = append(tags, "kernel")
	}
	return uniqueStrings(tags)
}

func normalizedHookFileTagsForPaths(paths []string) []string {
	if len(paths) == 0 {
		return []string{}
	}
	var tags []string
	for _, path := range paths {
		tags = append(tags, hookFileTags(path)...)
	}
	return uniqueStrings(tags)
}

type GitStatusTool struct{ ws Workspace }

func NewGitStatusTool(ws Workspace) GitStatusTool { return GitStatusTool{ws: ws} }

type GitAddTool struct{ ws Workspace }

func NewGitAddTool(ws Workspace) GitAddTool { return GitAddTool{ws: ws} }

func (t GitAddTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_add",
		Description: "Stage specific paths or all tracked and untracked changes in the current workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"paths": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"all": map[string]any{"type": "boolean"},
			},
		},
	}
}

func (t GitAddTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	all := boolValue(args, "all", false)
	paths := stringSliceValue(args, "paths")
	if all && len(paths) > 0 {
		return "", fmt.Errorf("provide either all=true or paths, not both")
	}
	if !all && len(paths) == 0 {
		return "", fmt.Errorf("paths are required unless all=true")
	}
	if err := t.ws.EnsureGitWithContext(ctx, "stage changes with git_add"); err != nil {
		return "", err
	}
	cmdArgs := []string{"add"}
	if all {
		cmdArgs = append(cmdArgs, "--all")
	} else {
		for _, rawPath := range paths {
			resolved, err := t.ws.Resolve(rawPath)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(t.ws.Root, resolved)
			if err != nil {
				return "", err
			}
			cmdArgs = append(cmdArgs, rel)
		}
	}
	if _, err := runGitCommand(ctx, t.ws.Root, cmdArgs...); err != nil {
		return "", err
	}
	status, err := runGitHelperCommand(ctx, t.ws.Root, "status", "--short")
	if err != nil {
		return "", err
	}
	summary := "staged changes"
	if all {
		summary = "staged all changes"
	} else {
		summary = fmt.Sprintf("staged %d path(s)", len(paths))
	}
	if status == "(no output)" {
		status = "(no staged or unstaged changes remain)"
	}
	return joinNonEmpty(summary, status), nil
}

func (t GitAddTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	all := boolValue(args, "all", false)
	paths := normalizeTaskStateList(stringSliceValue(args, "paths"), 32)
	text, err := t.Execute(ctx, input)
	meta := map[string]any{
		"effect":       "git_mutation",
		"all":          all,
		"paths":        paths,
		"stage_scope":  "paths",
		"staged":       err == nil,
		"staged_count": len(paths),
	}
	if all {
		meta["stage_scope"] = "all"
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

type GitCommitTool struct{ ws Workspace }

func NewGitCommitTool(ws Workspace) GitCommitTool { return GitCommitTool{ws: ws} }

func (t GitCommitTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_commit",
		Description: "Create a git commit from currently staged changes.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message":     map[string]any{"type": "string"},
				"allow_empty": map[string]any{"type": "boolean"},
			},
			"required": []string{"message"},
		},
	}
}

func (t GitCommitTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	message := stringValue(args, "message")
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("message is required")
	}
	if err := t.ws.EnsureGitWithContext(ctx, "create commit: "+firstLine(message)); err != nil {
		return "", err
	}
	cmdArgs := []string{"commit", "-m", message}
	if boolValue(args, "allow_empty", false) {
		cmdArgs = append(cmdArgs, "--allow-empty")
	}
	out, err := runGitCommand(ctx, t.ws.Root, cmdArgs...)
	if err != nil {
		return out, err
	}
	shortSHA, err := runGitHelperCommand(ctx, t.ws.Root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return out, err
	}
	subject, err := runGitHelperCommand(ctx, t.ws.Root, "log", "-1", "--pretty=%s")
	if err != nil {
		return out, err
	}
	return joinNonEmpty(
		fmt.Sprintf("created commit %s: %s", shortSHA, subject),
		out,
	), nil
}

func (t GitCommitTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	message := stringValue(args, "message")
	allowEmpty := boolValue(args, "allow_empty", false)
	text, err := t.Execute(ctx, input)
	commitSHA := ""
	commitSubject := strings.TrimSpace(firstLine(message))
	branch := ""
	if err == nil {
		commitSHA, _ = runGitHelperCommand(ctx, t.ws.Root, "rev-parse", "--short", "HEAD")
		if subject, subjectErr := runGitHelperCommand(ctx, t.ws.Root, "log", "-1", "--pretty=%s"); subjectErr == nil {
			commitSubject = subject
		}
		branch, _ = gitCurrentBranch(ctx, t.ws.Root)
	}
	meta := map[string]any{
		"effect":         "git_mutation",
		"message":        message,
		"allow_empty":    allowEmpty,
		"created_commit": err == nil,
		"commit_sha":     commitSHA,
		"commit_subject": commitSubject,
		"branch":         branch,
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

type GitPushTool struct{ ws Workspace }

func NewGitPushTool(ws Workspace) GitPushTool { return GitPushTool{ws: ws} }

func (t GitPushTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_push",
		Description: "Push the current or specified branch to a remote and optionally set upstream.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"remote":       map[string]any{"type": "string"},
				"branch":       map[string]any{"type": "string"},
				"set_upstream": map[string]any{"type": "boolean"},
			},
		},
	}
}

func (t GitPushTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	remote := stringValue(args, "remote")
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	branch := stringValue(args, "branch")
	if strings.TrimSpace(branch) == "" {
		currentBranch, err := gitCurrentBranch(ctx, t.ws.Root)
		if err != nil {
			return "", err
		}
		branch = currentBranch
	}
	changedFiles, _ := gitChangedFiles(ctx, t.ws.Root)
	if _, err := t.ws.Hook(ctx, HookPreGitPush, HookPayload{
		"remote":        remote,
		"branch":        branch,
		"changed_files": changedFiles,
	}); err != nil {
		return "", err
	}
	if _, err := t.ws.Hook(ctx, HookPreToolUse, HookPayload{
		"tool_name":     "git_push",
		"tool_kind":     "git",
		"command":       fmt.Sprintf("git push %s %s", remote, branch),
		"branch":        branch,
		"changed_files": changedFiles,
	}); err != nil {
		return "", err
	}
	if err := t.ws.EnsureGitWithContext(ctx, fmt.Sprintf("push branch %s to %s", branch, remote)); err != nil {
		return "", err
	}
	cmdArgs := []string{"push"}
	if boolValue(args, "set_upstream", true) {
		hasUpstream, err := gitHasUpstream(ctx, t.ws.Root)
		if err != nil {
			return "", err
		}
		if !hasUpstream {
			cmdArgs = append(cmdArgs, "-u")
		}
	}
	cmdArgs = append(cmdArgs, remote, branch)
	out, err := runGitCommand(ctx, t.ws.Root, cmdArgs...)
	if err != nil {
		return out, err
	}
	if _, err := t.ws.Hook(ctx, HookPostToolUse, HookPayload{
		"tool_name": "git_push",
		"tool_kind": "git",
		"command":   strings.Join(append([]string{"git"}, cmdArgs...), " "),
		"branch":    branch,
		"output":    out,
	}); err != nil {
		return "", err
	}
	return joinNonEmpty(
		fmt.Sprintf("pushed %s to %s", branch, remote),
		out,
	), nil
}

func (t GitPushTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	remote := firstNonBlankString(stringValue(args, "remote"), "origin")
	branch := stringValue(args, "branch")
	setUpstream := boolValue(args, "set_upstream", true)
	text, err := t.Execute(ctx, input)
	if strings.TrimSpace(branch) == "" && err == nil {
		branch, _ = gitCurrentBranch(ctx, t.ws.Root)
	}
	meta := map[string]any{
		"effect":       "git_mutation",
		"remote":       remote,
		"branch":       strings.TrimSpace(branch),
		"set_upstream": setUpstream,
		"pushed":       err == nil,
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

type GitCreatePRTool struct{ ws Workspace }

func NewGitCreatePRTool(ws Workspace) GitCreatePRTool { return GitCreatePRTool{ws: ws} }

func (t GitCreatePRTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_create_pr",
		Description: "Create a GitHub pull request for the current branch using the gh CLI. By default this pushes the branch first.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":       map[string]any{"type": "string"},
				"body":        map[string]any{"type": "string"},
				"base_branch": map[string]any{"type": "string"},
				"remote":      map[string]any{"type": "string"},
				"branch":      map[string]any{"type": "string"},
				"draft":       map[string]any{"type": "boolean"},
				"fill":        map[string]any{"type": "boolean"},
				"push":        map[string]any{"type": "boolean"},
			},
		},
	}
}

func (t GitCreatePRTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return "", fmt.Errorf("gh CLI is required to create a pull request: %w", err)
	}
	branch := stringValue(args, "branch")
	if strings.TrimSpace(branch) == "" {
		currentBranch, err := gitCurrentBranch(ctx, t.ws.Root)
		if err != nil {
			return "", err
		}
		branch = currentBranch
	}
	remote := stringValue(args, "remote")
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	fill := boolValue(args, "fill", false)
	title := stringValue(args, "title")
	if !fill && strings.TrimSpace(title) == "" {
		return "", fmt.Errorf("title is required unless fill=true")
	}
	if boolValue(args, "push", true) {
		pushTool := NewGitPushTool(t.ws)
		if _, err := pushTool.Execute(ctx, map[string]any{
			"remote":       remote,
			"branch":       branch,
			"set_upstream": true,
		}); err != nil {
			return "", err
		}
	}
	changedFiles, _ := gitChangedFiles(ctx, t.ws.Root)
	if _, err := t.ws.Hook(ctx, HookPreCreatePR, HookPayload{
		"remote":        remote,
		"branch":        branch,
		"changed_files": changedFiles,
		"title":         title,
	}); err != nil {
		return "", err
	}
	if _, err := t.ws.Hook(ctx, HookPreToolUse, HookPayload{
		"tool_name":     "git_create_pr",
		"tool_kind":     "git",
		"command":       "gh pr create",
		"branch":        branch,
		"changed_files": changedFiles,
	}); err != nil {
		return "", err
	}
	if err := t.ws.EnsureGitWithContext(ctx, "create pull request for "+branch); err != nil {
		return "", err
	}
	cmdArgs := []string{"pr", "create", "--head", branch}
	if base := stringValue(args, "base_branch"); strings.TrimSpace(base) != "" {
		cmdArgs = append(cmdArgs, "--base", base)
	}
	if boolValue(args, "draft", false) {
		cmdArgs = append(cmdArgs, "--draft")
	}
	if fill {
		cmdArgs = append(cmdArgs, "--fill")
	} else {
		cmdArgs = append(cmdArgs, "--title", title, "--body", stringValue(args, "body"))
	}
	out, err := runCommand(ctx, t.ws.Root, "gh", cmdArgs...)
	if err != nil {
		return out, err
	}
	if _, err := t.ws.Hook(ctx, HookPostToolUse, HookPayload{
		"tool_name": "git_create_pr",
		"tool_kind": "git",
		"command":   strings.Join(append([]string{"gh"}, cmdArgs...), " "),
		"branch":    branch,
		"output":    out,
	}); err != nil {
		return "", err
	}
	return joinNonEmpty(
		fmt.Sprintf("created pull request for %s", branch),
		out,
	), nil
}

func (t GitCreatePRTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	branch := strings.TrimSpace(stringValue(args, "branch"))
	remote := firstNonBlankString(stringValue(args, "remote"), "origin")
	fill := boolValue(args, "fill", false)
	draft := boolValue(args, "draft", false)
	push := boolValue(args, "push", true)
	baseBranch := strings.TrimSpace(stringValue(args, "base_branch"))
	title := stringValue(args, "title")
	text, err := t.Execute(ctx, input)
	if branch == "" && err == nil {
		branch, _ = gitCurrentBranch(ctx, t.ws.Root)
	}
	meta := map[string]any{
		"effect":      "git_mutation",
		"remote":      remote,
		"branch":      branch,
		"base_branch": baseBranch,
		"draft":       draft,
		"fill":        fill,
		"push":        push,
		"title":       title,
		"pr_created":  err == nil,
		"pr_url":      firstHTTPURL(text),
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func (t GitStatusTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_status",
		Description: "Show git status for the current workspace.",
		InputSchema: emptyObjectSchema(),
	}
}

func (t GitStatusTool) Execute(ctx context.Context, input any) (string, error) {
	if _, err := requireToolInputObject(input, t.Definition().Name); err != nil {
		return "", err
	}
	cmd := newGitHelperCommand(ctx, t.ws.Root, "status", "--short", "--branch")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("git status failed: %w", err)
	}
	if text == "" {
		return "(clean working tree)", nil
	}
	return text, nil
}

func (t GitStatusTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	text, err := t.Execute(ctx, input)
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	branch := ""
	changedPaths := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			branch = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		if len(trimmed) > 3 {
			changedPaths = append(changedPaths, strings.TrimSpace(trimmed[3:]))
		}
	}
	meta := map[string]any{
		"branch":        branch,
		"changed_paths": normalizeTaskStateList(changedPaths, 32),
		"changed_count": len(changedPaths),
		"clean":         len(changedPaths) == 0 && err == nil,
		"effect":        "inspect",
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

type GitDiffTool struct{ ws Workspace }

func NewGitDiffTool(ws Workspace) GitDiffTool { return GitDiffTool{ws: ws} }

func (t GitDiffTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "git_diff",
		Description: "Show git diff for the workspace or a specific path.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
				"staged": map[string]any{"type": "boolean"},
			},
		},
	}
}

func (t GitDiffTool) Execute(ctx context.Context, input any) (string, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	cmdArgs := []string{"diff"}
	if boolValue(args, "staged", false) {
		cmdArgs = append(cmdArgs, "--staged")
	}
	if pathArg := stringValue(args, "path"); pathArg != "" {
		path, err := t.ws.Resolve(pathArg)
		if err != nil {
			return "", err
		}
		cmdArgs = append(cmdArgs, "--", path)
	}
	cmd := newGitHelperCommand(ctx, t.ws.Root, cmdArgs...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("git diff failed: %w", err)
	}
	if text == "" {
		return "(no diff)", nil
	}
	return text, nil
}

func (t GitDiffTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	text, err := t.Execute(ctx, input)
	fileCount := 0
	for _, line := range strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "diff --git ") {
			fileCount++
		}
	}
	meta := map[string]any{
		"path":       strings.TrimSpace(stringValue(args, "path")),
		"staged":     boolValue(args, "staged", false),
		"has_diff":   strings.TrimSpace(text) != "" && strings.TrimSpace(text) != "(no diff)",
		"file_count": fileCount,
		"line_count": textLineCount(text),
		"effect":     "inspect",
	}
	addEffectiveExecutionContextMetadata(meta, t.ws, nil)
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func runCommand(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s failed: %w", summarizeExec(name, args...), err)
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

func runGitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := runCommand(ctx, dir, "git", args...)
	if err != nil {
		return out, fmt.Errorf("git command failed: %w", err)
	}
	return out, nil
}

func runGitHelperCommand(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := newGitHelperCommand(ctx, dir, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("git command failed: %w", err)
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}

func summarizeExec(name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	parts = append(parts, args...)
	return summarizeShellCommand(strings.Join(parts, " "))
}

func gitCurrentBranch(ctx context.Context, dir string) (string, error) {
	branch, err := runGitHelperCommand(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if branch == "HEAD" {
		return "", fmt.Errorf("git repository is in detached HEAD state")
	}
	return branch, nil
}

func gitHasUpstream(ctx context.Context, dir string) (bool, error) {
	cmd := newGitHelperCommand(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if strings.Contains(text, "no upstream configured") || strings.Contains(text, "HEAD branch has no upstream branch") {
			return false, nil
		}
		if text == "" {
			text = err.Error()
		}
		return false, fmt.Errorf("failed to inspect upstream branch: %s", text)
	}
	return true, nil
}

func gitChangedFiles(ctx context.Context, dir string) ([]string, error) {
	cmd := newGitHelperCommand(ctx, dir, "-c", "core.quotePath=false", "status", "--short")
	data, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(data))
		if text == "" {
			text = err.Error()
		}
		return nil, fmt.Errorf("git status --short failed: %s", text)
	}
	out := strings.TrimRight(string(data), "\r\n")
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		path, ok := parseGitStatusShortPath(line)
		if ok {
			files = append(files, path)
		}
	}
	return uniqueStrings(files), nil
}

func firstHTTPURL(text string) string {
	matches := regexp.MustCompile(`https?://[^\s]+`).FindString(strings.TrimSpace(text))
	return strings.TrimSpace(matches)
}

type UpdatePlanTool struct{ ws Workspace }

func NewUpdatePlanTool(ws Workspace) UpdatePlanTool { return UpdatePlanTool{ws: ws} }

func (t UpdatePlanTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "update_plan",
		Description: "Update the shared task plan shown to the user.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"step":   map[string]any{"type": "string"},
							"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
						},
						"required": []string{"step", "status"},
					},
				},
			},
			"required": []string{"items"},
		},
	}
}

func (t UpdatePlanTool) Execute(ctx context.Context, input any) (string, error) {
	_ = ctx
	if t.ws.UpdatePlan == nil {
		return "", fmt.Errorf("plan updates are not configured")
	}
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return "", err
	}
	rawItems, ok := args["items"].([]any)
	if !ok {
		return "", fmt.Errorf("items must be an array")
	}
	items := make([]PlanItem, 0, len(rawItems))
	for _, raw := range rawItems {
		obj, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("each plan item must be an object")
		}
		items = append(items, PlanItem{
			Step:   stringValue(obj, "step"),
			Status: stringValue(obj, "status"),
		})
	}
	t.ws.UpdatePlan(items)
	if len(items) == 0 {
		return "cleared plan", nil
	}
	var lines []string
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("[%s] %s", item.Status, item.Step))
	}
	return strings.Join(lines, "\n"), nil
}

func (t UpdatePlanTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, inputErr := requireToolInputObject(input, t.Definition().Name)
	if inputErr != nil {
		return ToolExecutionResult{}, inputErr
	}
	text, err := t.Execute(ctx, input)
	rawItems, _ := args["items"].([]any)
	pendingCount := 0
	inProgressCount := 0
	completedCount := 0
	for _, raw := range rawItems {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(stringValue(obj, "status"))) {
		case "completed":
			completedCount++
		case "in_progress":
			inProgressCount++
		default:
			pendingCount++
		}
	}
	meta := map[string]any{
		"plan_item_count":   len(rawItems),
		"pending_count":     pendingCount,
		"in_progress_count": inProgressCount,
		"completed_count":   completedCount,
		"effect":            "plan",
	}
	return ToolExecutionResult{DisplayText: text, Meta: meta}, err
}

func stringValue(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case string:
			return x
		}
	}
	return ""
}

func boolValue(m map[string]any, key string, def bool) bool {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case bool:
			return x
		}
	}
	return def
}

func intValue(m map[string]any, key string, def int) int {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case float64:
			return int(x)
		case int:
			return x
		}
	}
	return def
}

func stringSliceValue(m map[string]any, key string) []string {
	if v, ok := m[key]; ok {
		switch x := v.(type) {
		case []string:
			return append([]string(nil), x...)
		case []any:
			out := make([]string, 0, len(x))
			for _, item := range x {
				s, ok := item.(string)
				if !ok {
					continue
				}
				if strings.TrimSpace(s) == "" {
					continue
				}
				out = append(out, s)
			}
			return out
		}
	}
	return nil
}
