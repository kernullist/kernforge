package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatWrittenFileRespectsGate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	src := "package x\nfunc  F( ){}\n" // intentionally unformatted
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// Disabled gate must never format.
	Workspace{FormatOnWrite: false}.formatWrittenFile(context.Background(), p)
	if got, _ := os.ReadFile(p); string(got) != src {
		t.Fatalf("disabled gate must leave the file unchanged, got %q", got)
	}
	// An unknown extension has no formatter and is left unchanged even when enabled.
	q := filepath.Join(dir, "x.unknownext")
	if err := os.WriteFile(q, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	Workspace{FormatOnWrite: true}.formatWrittenFile(context.Background(), q)
	if got, _ := os.ReadFile(q); string(got) != src {
		t.Fatalf("unknown extension must be left unchanged, got %q", got)
	}
}

func TestFormatWrittenFileFormatsGo(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not on PATH")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	if err := os.WriteFile(p, []byte("package x\nfunc  F( ){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Workspace{FormatOnWrite: true}.formatWrittenFile(context.Background(), p)
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), "func F() {") {
		t.Fatalf("gofmt should have reformatted the file, got %q", got)
	}
}
