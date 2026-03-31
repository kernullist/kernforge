package main

import (
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
	rt := &runtimeState{}
	rt.rememberInputHistory("first")
	rt.rememberInputHistory("")
	rt.rememberInputHistory("second\ncontinued")
	rt.rememberInputHistory(" third ")

	want := []string{"first", " third "}
	if !reflect.DeepEqual(rt.inputHistoryEntries(), want) {
		t.Fatalf("inputHistoryEntries() = %#v, want %#v", rt.inputHistoryEntries(), want)
	}
}
