The model stopped before producing a usable response because the response hit a token limit. Do not treat the turn as complete.
{{if .StopReason}}
Stop reason: {{.StopReason}}.
{{end}}
Continue from the partial state if another model turn is available; otherwise report the token-limit blocker clearly.
