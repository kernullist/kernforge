package main

import (
	"context"
	"fmt"
	"strings"
)

// UserQuestionOption is one selectable choice presented by the ask_user tool.
type UserQuestionOption struct {
	Label       string
	Description string
}

// UserQuestion is a structured multiple-choice question the model asks the user.
type UserQuestion struct {
	Question    string
	Header      string
	Options     []UserQuestionOption
	Multiple    bool
	AllowCustom bool
}

// UserQuestionResult carries the user's answer back to the model.
type UserQuestionResult struct {
	Selected []string // chosen option labels
	Custom   string   // free-text answer when AllowCustom and the user typed one
	Canceled bool
}

// AskUserTool lets the model ask the user a structured multiple-choice question
// and block for the answer, instead of guessing or emitting a free-text "what
// should I do?". The actual terminal interaction is supplied by the runtime via
// Workspace.PromptUserChoice; without it (headless) the tool reports that no
// interactive user is attached so the model proceeds with a default.
type AskUserTool struct{ ws Workspace }

func NewAskUserTool(ws Workspace) AskUserTool { return AskUserTool{ws: ws} }

func (t AskUserTool) ReadOnlyToolCall() bool { return true }

func (t AskUserTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "ask_user",
		Description: "Ask the user a structured multiple-choice question and wait for their answer. Use ONLY when you genuinely cannot proceed without a decision that is the user's to make -- a real fork, a missing requirement, or a risky/irreversible choice. Prefer acting on a sensible default over asking. Returns the chosen option label(s).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "The question to ask the user."},
				"header":   map[string]any{"type": "string", "description": "A short heading (<=30 chars) for the question."},
				"options": map[string]any{
					"type":        "array",
					"description": "The choices (2-4 recommended). Each is an object {label, description}.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"label":       map[string]any{"type": "string", "description": "Short choice text."},
							"description": map[string]any{"type": "string", "description": "One-line explanation of the choice."},
						},
						"required": []any{"label"},
					},
				},
				"multiple":     map[string]any{"type": "boolean", "description": "Allow selecting more than one option."},
				"allow_custom": map[string]any{"type": "boolean", "description": "Allow a free-text answer instead of a listed option."},
			},
			"required": []any{"question", "options"},
		},
	}
}

func (t AskUserTool) Execute(ctx context.Context, input any) (string, error) {
	result, err := t.ExecuteDetailed(ctx, input)
	return result.DisplayText, err
}

func (t AskUserTool) ExecuteDetailed(ctx context.Context, input any) (ToolExecutionResult, error) {
	args, err := requireToolInputObject(input, "ask_user")
	if err != nil {
		return ToolExecutionResult{}, err
	}
	question := strings.TrimSpace(stringValue(args, "question"))
	if question == "" {
		return ToolExecutionResult{}, fmt.Errorf("ask_user requires a non-empty 'question'")
	}
	options := parseUserQuestionOptions(args["options"])
	if len(options) == 0 {
		return ToolExecutionResult{}, fmt.Errorf("ask_user requires at least one option in 'options'")
	}
	if t.ws.PromptUserChoice == nil {
		msg := "ask_user is unavailable: no interactive user is attached to this session."
		return ToolExecutionResult{
			DisplayText: msg,
			ModelText:   "ask_user error: " + msg + " Proceed with a sensible default instead of asking.",
			Meta:        map[string]any{"success": false},
		}, nil
	}
	multiple, _ := args["multiple"].(bool)
	allowCustom, _ := args["allow_custom"].(bool)
	result, err := t.ws.PromptUserChoice(UserQuestion{
		Question:    question,
		Header:      strings.TrimSpace(stringValue(args, "header")),
		Options:     options,
		Multiple:    multiple,
		AllowCustom: allowCustom,
	})
	if err != nil {
		return ToolExecutionResult{}, err
	}
	answer := formatUserQuestionAnswer(result)
	return ToolExecutionResult{
		DisplayText: answer,
		ModelText:   fmt.Sprintf("The user answered %q with: %s", question, answer),
		Meta:        map[string]any{"success": true, "output_bounded": true},
	}, nil
}

func parseUserQuestionOptions(raw any) []UserQuestionOption {
	arr, _ := raw.([]any)
	out := make([]UserQuestionOption, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			if label := strings.TrimSpace(v); label != "" {
				out = append(out, UserQuestionOption{Label: label})
			}
		case map[string]any:
			label := strings.TrimSpace(stringValue(v, "label"))
			if label != "" {
				out = append(out, UserQuestionOption{Label: label, Description: strings.TrimSpace(stringValue(v, "description"))})
			}
		}
	}
	return out
}

func formatUserQuestionAnswer(r UserQuestionResult) string {
	if r.Canceled {
		return "(no answer; the user dismissed the question)"
	}
	if custom := strings.TrimSpace(r.Custom); custom != "" {
		return custom
	}
	if len(r.Selected) > 0 {
		return strings.Join(r.Selected, ", ")
	}
	return "(no selection)"
}
