package main

import "testing"

func TestEffectiveProjectAnalysisModeDefaultsToMap(t *testing.T) {
	mode := effectiveProjectAnalysisMode("", "security-sensitive startup path")
	if mode != "map" {
		t.Fatalf("expected default mode map, got %q", mode)
	}
}

func TestParseAnalyzeProjectArgsParsesExplicitMode(t *testing.T) {
	mode, goal, err := parseAnalyzeProjectArgs("--mode security anti cheat trust boundary")
	if err != nil {
		t.Fatalf("parseAnalyzeProjectArgs returned error: %v", err)
	}
	if mode != "security" {
		t.Fatalf("expected security mode, got %q", mode)
	}
	if goal != "anti cheat trust boundary" {
		t.Fatalf("expected goal to preserve remaining text, got %q", goal)
	}
}

func TestParseAnalyzeProjectArgsParsesEqualsMode(t *testing.T) {
	mode, goal, err := parseAnalyzeProjectArgs("--mode=trace trace startup dispatch path")
	if err != nil {
		t.Fatalf("parseAnalyzeProjectArgs returned error: %v", err)
	}
	if mode != "trace" {
		t.Fatalf("expected trace mode, got %q", mode)
	}
	if goal != "trace startup dispatch path" {
		t.Fatalf("expected goal to preserve remaining text, got %q", goal)
	}
}

func TestParseAnalyzeProjectArgsRejectsInvalidMode(t *testing.T) {
	_, _, err := parseAnalyzeProjectArgs("--mode weird map startup")
	if err == nil {
		t.Fatalf("expected invalid mode error")
	}
}

func TestParseAnalyzeProjectArgsRequiresGoal(t *testing.T) {
	_, _, err := parseAnalyzeProjectArgs("--mode security")
	if err == nil {
		t.Fatalf("expected missing goal error")
	}
}

func TestProjectAnalysisModeStatusReportsDefaultMap(t *testing.T) {
	status := projectAnalysisModeStatus("", "trace startup dispatch")
	if status != "default(map)" {
		t.Fatalf("expected default(map) status, got %q", status)
	}
}
