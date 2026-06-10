Tool call blocked by runtime policy.
Reason: {{.Reason}}
{{if .ToolCallsSummary}}
Tool calls:
{{.ToolCallsSummary}}
{{end}}{{if .Guidance}}

{{.Guidance}}
{{else}}

Choose a permitted next step from the current request envelope and tool contract. Do not repeat the blocked call unchanged.
{{end}}
