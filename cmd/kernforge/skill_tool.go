package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// LoadSkillTool lets the model pull a discovered skill's full instructions into
// the conversation on demand. Skills are otherwise advertised only by name and
// summary, so without this tool the model can never obtain the body of a skill
// that is not enabled by default; enabled skills are injected in full by the
// system prompt, but the model still benefits from being able to re-load any
// skill explicitly. The catalog snapshot is captured at registry-build time;
// the registry is rebuilt on every config/skill reload (reloadExtensions sets
// the agent catalog and then rebuilds the tool registry), so the snapshot stays
// current.
type LoadSkillTool struct {
	skills SkillCatalog
}

func NewLoadSkillTool(skills SkillCatalog) LoadSkillTool {
	return LoadSkillTool{skills: skills}
}

func (t LoadSkillTool) ReadOnlyToolCall() bool {
	return true
}

func (t LoadSkillTool) SupportsParallelToolCalls() bool {
	return true
}

func (t LoadSkillTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "load_skill",
		Description: "Load the full instructions for a local skill by name. Use this when a skill listed in the local skill catalog is relevant to the request; the returned body is the authoritative procedure to follow for that task. Returns an error that lists the available skills when the name is unknown.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The skill name exactly as listed in the local skill catalog.",
				},
			},
			"required": []any{"name"},
		},
	}
}

func (t LoadSkillTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t LoadSkillTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, t.Definition().Name)
	if err != nil {
		return ToolExecutionResult{}, err
	}
	name := strings.TrimSpace(stringValue(args, "name"))
	if name == "" {
		msg := "load_skill requires a non-empty 'name'. Available skills: " + t.availableNames()
		return ToolExecutionResult{
			DisplayText: "load_skill requires a non-empty 'name'.",
			ModelText:   "load_skill error: " + msg,
			Meta:        map[string]any{"success": false},
		}, nil
	}
	skill, ok := t.skills.Lookup(name)
	if !ok {
		msg := fmt.Sprintf("Unknown skill %q. Available skills: %s", name, t.availableNames())
		return ToolExecutionResult{
			DisplayText: msg,
			ModelText:   "load_skill error: " + msg,
			Meta:        map[string]any{"success": false},
		}, nil
	}
	body := renderSkillPromptSection(skill)
	return ToolExecutionResult{
		DisplayText: fmt.Sprintf("Loaded skill %q (%d chars).", skill.Name, len(skill.Content)),
		ModelText:   "Skill instructions to apply for this task:\n\n" + body,
		Meta: map[string]any{
			"success":       true,
			"skill":         skill.Name,
			"allowed_tools": skill.AllowedTools,
		},
	}, nil
}

func (t LoadSkillTool) availableNames() string {
	items := t.skills.Items()
	if len(items) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(items))
	for _, skill := range items {
		names = append(names, skill.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
