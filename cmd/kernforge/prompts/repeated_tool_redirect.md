{{.Reason}}

{{.NextStepRequirements}}
{{if .LoopSignature}}

Loop signature: {{.LoopSignature}}
{{end}}{{if .RepeatedSequence}}

{{.DetailTitle}}:
{{.RepeatedSequence}}
{{end}}{{if .RecentToolTurns}}

Recent tool turns:
{{.RecentToolTurns}}
{{end}}
