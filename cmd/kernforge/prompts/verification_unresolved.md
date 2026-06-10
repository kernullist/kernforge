{{.Message}}
{{if .RuntimeState}}
Runtime state: {{.RuntimeState}}.
{{end}}{{if .Reason}}
Reason: {{.Reason}}.
{{end}}{{if .UnresolvedVerification}}
Unresolved verification: true.
{{end}}{{if .GeneratedDocumentHarnessOwnsIt}}
Generated document artifact checks own this verification disclosure.
{{end}}
