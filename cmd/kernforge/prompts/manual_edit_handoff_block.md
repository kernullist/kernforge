This request explicitly asks you to inspect and fix the code. Do not hand the patch back to the user. Read the relevant file if needed, then use the available edit tools directly. Only ask the user to edit manually if an edit tool actually failed, and cite that exact tool error.
{{if .Reason}}
Reason: {{.Reason}}.
{{end}}{{if .Count}}
Retry count: {{.Count}}.
{{end}}
