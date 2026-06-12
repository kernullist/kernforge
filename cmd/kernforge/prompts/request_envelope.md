Request envelope:
{{if .PrimaryClass}}- Primary class: {{.PrimaryClass}}.
{{end}}{{if .ClassesText}}- Classes: {{.ClassesText}}.
{{end}}{{if .Boundary}}- Action boundary: {{.Boundary}}.
{{end}}- Allows file mutation: {{.AllowsFileMutation}}.
- Allows git mutation: {{.AllowsGitMutation}}.
- Requires verification: {{.RequiresVerification}}.
{{if .RequiresFreshExternalInfo}}- Requires fresh external information: true.
{{end}}{{if .ReviewRequestClass}}- Review request class: {{.ReviewRequestClass}}.
{{end}}{{if .ConfidenceText}}- Classification confidence: {{.ConfidenceText}}.
{{end}}{{if .WarningsText}}- Classification warnings: {{.WarningsText}}.
{{end}}{{if .ReadOnlyAnalysis}}
The latest user request is analysis-only. Investigate and explain the issue, but do not modify files or call edit tools unless the user explicitly asks for a fix.

Request mode: analysis-only.
- Investigate, explain, or document the issue.
- Do not modify files or call edit tools unless the user explicitly asks for a fix.
{{else if .DocumentAuthoring}}
The latest user request is document-authoring. Produce the requested document or report as the deliverable. You may create or update the target document file, but do not modify source code.

Request mode: document-authoring.
- Produce the requested document or report as the deliverable.
- You may create or update the target document file (for example a .md file) using the available file tools.
- Do not modify, fix, or refactor source code; describe needed changes in the document instead unless the user gives an explicit source-edit command.
{{else if .ExplicitEditRequest}}
The latest user request explicitly asks for a fix. Inspect the relevant code and apply the necessary edit directly with the available tools. Do not hand the patch back to the user unless an edit tool actually fails.

Request mode: inspect-and-fix.
- Investigate the referenced code and apply the necessary fix directly when needed.
- Use available inspect tools first, then use edit tools to make the change.
- Do not ask the user to apply the patch manually unless an edit tool actually failed and you cite that tool error.
{{end}}{{if .GoalPromptDraftOnly}}
Goal prompt draft mode:
- The user asked to draft goal prompt text, not to activate or run a goal.
- Do not call create_goal or update_goal unless the user explicitly asks for goal activation or execution.
{{end}}{{if .ExplicitGitRequest}}
Git intent:
- The user explicitly asked for a git action such as staging, committing, pushing, or opening a PR.
- If you perform a git-mutating action, summarize exactly what you are about to do.
{{end}}{{if .RequiresFreshExternalInfo}}
Fresh external information:
- This request likely needs current external evidence before relying on memory or local-only context.
{{end}}{{if .GitMutationGuard}}
Do not stage, commit, push, or open a PR unless the user explicitly asks for that git action.
{{end}}
