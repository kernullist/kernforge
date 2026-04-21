package main

import "testing"

func TestOwnershipPatternMatchesBasenameAndPathGlobs(t *testing.T) {
	cases := []struct {
		name    string
		relPath string
		pattern string
		want    bool
	}{
		{
			name:    "basename wildcard matches nested file",
			relPath: "driver/acme.sys",
			pattern: "*.sys",
			want:    true,
		},
		{
			name:    "double star matches nested directory",
			relPath: "telemetry/providers/kernel/main.cpp",
			pattern: "telemetry/**",
			want:    true,
		},
		{
			name:    "path glob is case insensitive",
			relPath: "config/DefaultGame.ini",
			pattern: "Config/**",
			want:    true,
		},
		{
			name:    "single star does not cross directories",
			relPath: "providers/nested/manifest.xml",
			pattern: "providers/*.xml",
			want:    false,
		},
		{
			name:    "question mark matches a single basename character",
			relPath: "patterns/file1.sig",
			pattern: "file?.sig",
			want:    true,
		},
		{
			name:    "catch all pattern matches everything",
			relPath: "src/main.go",
			pattern: "**",
			want:    true,
		},
		{
			name:    "non matching basename wildcard is rejected",
			relPath: "driver/acme.dll",
			pattern: "*.sys",
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ownershipPatternMatches(tc.relPath, tc.pattern); got != tc.want {
				t.Fatalf("ownershipPatternMatches(%q, %q) = %t, want %t", tc.relPath, tc.pattern, got, tc.want)
			}
		})
	}
}
