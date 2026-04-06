package main

import "strings"

func (rt *runtimeState) presentDiffView(title string, subtitle string, diff string) error {
	if !rt.interactive {
		return nil
	}
	if strings.TrimSpace(diff) == "" {
		diff = "(no diff)"
	}
	return OpenReadOnlyDiffView(title, subtitle, diff)
}
