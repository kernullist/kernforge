package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInputHistoryNavigatorPreviousAndNext(t *testing.T) {
	nav := newInputHistoryNavigator([]string{"alpha", "beta", "gamma"}, "draft")

	got, ok := nav.Previous("draft")
	if !ok || got != "gamma" {
		t.Fatalf("first Previous() = %q, %v; want gamma, true", got, ok)
	}

	got, ok = nav.Previous(got)
	if !ok || got != "beta" {
		t.Fatalf("second Previous() = %q, %v; want beta, true", got, ok)
	}

	got, ok = nav.Next(got)
	if !ok || got != "gamma" {
		t.Fatalf("first Next() = %q, %v; want gamma, true", got, ok)
	}

	got, ok = nav.Next(got)
	if !ok || got != "draft" {
		t.Fatalf("second Next() = %q, %v; want draft, true", got, ok)
	}

	got, ok = nav.Next(got)
	if ok || got != "draft" {
		t.Fatalf("third Next() = %q, %v; want draft, false", got, ok)
	}
}

func TestInputHistoryNavigatorSyncBufferDetachesFromHistory(t *testing.T) {
	nav := newInputHistoryNavigator([]string{"alpha", "beta"}, "")

	got, ok := nav.Previous("")
	if !ok || got != "beta" {
		t.Fatalf("Previous() = %q, %v; want beta, true", got, ok)
	}

	nav.SyncBuffer("beta edited")

	got, ok = nav.Next("beta edited")
	if ok || got != "beta edited" {
		t.Fatalf("Next() after edit = %q, %v; want beta edited, false", got, ok)
	}
}

func TestRememberInputHistory(t *testing.T) {
	// Point persistence at a temp file so the test never touches the real
	// user config directory.
	rt := &runtimeState{inputHistoryPath: filepath.Join(t.TempDir(), "input-history")}
	rt.rememberInputHistory("first")
	rt.rememberInputHistory("")
	rt.rememberInputHistory("second\ncontinued")
	rt.rememberInputHistory(" third ")

	want := []string{"first", " third "}
	if !reflect.DeepEqual(rt.inputHistoryEntries(), want) {
		t.Fatalf("inputHistoryEntries() = %#v, want %#v", rt.inputHistoryEntries(), want)
	}
}

func TestRememberInputHistorySkipsConsecutiveDuplicates(t *testing.T) {
	rt := &runtimeState{inputHistoryPath: filepath.Join(t.TempDir(), "input-history")}
	rt.rememberInputHistory("build")
	rt.rememberInputHistory("build")
	rt.rememberInputHistory("test")
	rt.rememberInputHistory("build")

	want := []string{"build", "test", "build"}
	if !reflect.DeepEqual(rt.inputHistoryEntries(), want) {
		t.Fatalf("inputHistoryEntries() = %#v, want %#v", rt.inputHistoryEntries(), want)
	}
}

func TestInputHistoryPersistAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input-history")
	writer := &runtimeState{inputHistoryPath: path}
	writer.rememberInputHistory("alpha")
	writer.rememberInputHistory("beta")
	writer.rememberInputHistory("gamma")

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("history file not written: %v", err)
	}

	reader := &runtimeState{inputHistoryPath: path}
	reader.loadInputHistory()
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(reader.inputHistoryEntries(), want) {
		t.Fatalf("loaded history = %#v, want %#v", reader.inputHistoryEntries(), want)
	}

	// A second load must be a no-op (no duplication).
	reader.loadInputHistory()
	if !reflect.DeepEqual(reader.inputHistoryEntries(), want) {
		t.Fatalf("history after second load = %#v, want %#v", reader.inputHistoryEntries(), want)
	}
}

func TestInputHistoryNavigatorPrefixSearch(t *testing.T) {
	nav := newInputHistoryNavigator([]string{"build", "git status", "go test", "git push"}, "")
	nav.SetPrefix("git")

	got, ok := nav.Previous("git")
	if !ok || got != "git push" {
		t.Fatalf("first Previous() = %q, %v; want git push, true", got, ok)
	}
	got, ok = nav.Previous(got)
	if !ok || got != "git status" {
		t.Fatalf("second Previous() = %q, %v; want git status, true", got, ok)
	}
	// No earlier "git" entry exists, so Previous stops.
	if got, ok = nav.Previous(got); ok {
		t.Fatalf("third Previous() = %q, %v; want no match", got, ok)
	}
}
