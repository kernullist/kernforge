{{if .ReadOnlyAnalysis}}Your last reply was empty. This is a read-only analysis or review request. If you need more evidence, use read_file, grep, or list_files on the referenced code first. Then provide a concrete final answer with findings, likely root causes, and file references. Do not return an empty message.{{else}}Please provide the final answer to the user now. Do not return an empty message.{{end}}
{{if .StopReason}}
Last stop reason: {{.StopReason}}.
{{end}}{{if .Count}}
Empty response count: {{.Count}}.
{{end}}
