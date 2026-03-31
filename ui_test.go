package main

import (
	"strings"
	"testing"
)

func TestBannerUsesCurrentKernforgeBranding(t *testing.T) {
	ui := UI{color: false}
	banner := ui.banner("openai", "gpt-5.4", "session-123", `F:\kernullist\kernforge`)

	for _, needle := range []string{
		"Kernforge",
		"forge-ready terminal coding agent",
		"provider=openai",
		"model=gpt-5.4",
		"workspace=F:\\kernullist\\kernforge",
	} {
		if !strings.Contains(banner, needle) {
			t.Fatalf("expected banner to contain %q\n%s", needle, banner)
		}
	}

	for _, legacy := range []string{"IM-CLI", "im-cli", "imcli"} {
		if strings.Contains(banner, legacy) {
			t.Fatalf("banner should not contain legacy branding %q\n%s", legacy, banner)
		}
	}
}
