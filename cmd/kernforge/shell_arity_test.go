package main

import (
	"regexp"
	"testing"
)

func TestShellCommandArityPattern(t *testing.T) {
	cases := map[string]string{
		"git commit -m x":   `^git\s+commit\b`,
		"go build ./...":    `^go\s+build\b`,
		"npm run build":     `^npm\s+run\s+build\b`,
		"docker compose up": `^docker\s+compose\s+up\b`,
		"ls -la":            `^ls\b`,
		"make":              `^make\b`,
	}
	for cmd, want := range cases {
		got := shellCommandArityPattern(cmd)
		if got != want {
			t.Fatalf("shellCommandArityPattern(%q) = %q, want %q", cmd, got, want)
		}
		// The derived pattern must compile and match the command it came from.
		re, err := regexp.Compile(got)
		if err != nil {
			t.Fatalf("pattern %q does not compile: %v", got, err)
		}
		if !re.MatchString(cmd) {
			t.Fatalf("pattern %q does not match its own command %q", got, cmd)
		}
	}
	if got := shellCommandArityPattern("   "); got != "" {
		t.Fatalf("empty command must yield an empty pattern, got %q", got)
	}
	// The family pattern matches other commands in the family but not a different one.
	gitCommit := regexp.MustCompile(shellCommandArityPattern("git commit -m x"))
	if !gitCommit.MatchString("git commit -am y") {
		t.Fatalf("git-commit family pattern should match another git commit")
	}
	if gitCommit.MatchString("git push") {
		t.Fatalf("git-commit family pattern must not match git push")
	}
}
