package main

import "testing"

func TestEvaluateArchitectureAnswerRejectsTopLevelClosedSetViolations(t *testing.T) {
	pack := ArchitectureFactPack{
		TopLevelDirectories: []ArchitectureDirectoryFact{
			{Path: "Common/"},
			{Path: "Driver/"},
			{Path: "DriverConsole/"},
		},
		TopLevelNonDirectoryExclusions: []string{
			"Driver/Callbacks.h",
			"Driver/BuildCab/Driver.inf",
		},
	}
	answer := `
## 최상위 디렉터리 구조

| 디렉터리 | 역할 |
| --- | --- |
| Driver/ | 커널 드라이버 |
| Driver/Callbacks.h | 오브젝트 콜백 헤더 |
`
	evaluation := evaluateArchitectureAnswerAgainstFacts(answer, pack)
	if !architectureAnswerHasBlockingViolations(evaluation) {
		t.Fatalf("expected blocking closed-set violation, got %+v", evaluation)
	}
	if !architectureEvaluationHasCode(evaluation, "top_level_exclusion_listed") {
		t.Fatalf("expected top_level_exclusion_listed, got %+v", evaluation)
	}
}

func TestEvaluateArchitectureAnswerAllowsExcludedFilesOutsideTopLevelRows(t *testing.T) {
	pack := ArchitectureFactPack{
		TopLevelDirectories: []ArchitectureDirectoryFact{
			{Path: "Common/"},
			{Path: "Driver/"},
		},
		TopLevelNonDirectoryExclusions: []string{"Driver/Callbacks.h"},
	}
	answer := `
## 최상위 디렉터리 구조

| 디렉터리 | 역할 |
| --- | --- |
| Driver/ | 커널 드라이버 |
| Common/ | 공유 계약 |

추가로 읽을 파일: Driver/Callbacks.h
`
	evaluation := evaluateArchitectureAnswerAgainstFacts(answer, pack)
	if architectureAnswerHasBlockingViolations(evaluation) {
		t.Fatalf("did not expect blocking violation, got %+v", evaluation)
	}
}

func TestEvaluateArchitectureAnswerRejectsAccessorLifecycleRelabel(t *testing.T) {
	pack := ArchitectureFactPack{
		CriticalAnchors: []ArchitectureAnchorFact{
			{
				Role:     "control_pid_accessor",
				Symbol:   "DriverCore::GetControlPid",
				Location: "Driver/DriverCore.cpp:1285",
			},
		},
	}
	answer := `
| 역할 | 파일:라인 |
| --- | --- |
| Finalize/Unload cleanup | Driver/DriverCore.cpp:1285 |
`
	evaluation := evaluateArchitectureAnswerAgainstFacts(answer, pack)
	if !architectureAnswerHasBlockingViolations(evaluation) {
		t.Fatalf("expected blocking accessor relabel violation, got %+v", evaluation)
	}
	if !architectureEvaluationHasCode(evaluation, "accessor_anchor_relabelled_as_lifecycle") {
		t.Fatalf("expected accessor relabel violation, got %+v", evaluation)
	}
}

func TestEvaluateArchitectureAnswerFlagsExactAnchorMissingSymbol(t *testing.T) {
	pack := ArchitectureFactPack{
		CriticalAnchors: []ArchitectureAnchorFact{
			{
				Role:     "ioctl_dispatch",
				Symbol:   "DriverCore::DeviceIoControlIrpHandleRoutine",
				Location: "Driver/DriverCore.cpp:523",
			},
		},
	}
	answer := `
| 역할 | 파일:라인 |
| --- | --- |
| IOCTL dispatch | Driver/DriverCore.cpp:523 |
`
	evaluation := evaluateArchitectureAnswerAgainstFacts(answer, pack)
	if !architectureEvaluationHasCode(evaluation, "exact_anchor_symbol_missing") {
		t.Fatalf("expected exact anchor symbol violation, got %+v", evaluation)
	}
	if architectureAnswerHasBlockingViolations(evaluation) {
		t.Fatalf("missing exact symbol should warn but not block, got %+v", evaluation)
	}
}

func TestEvaluateArchitectureAnswerMatchesWindowsStyleAnchorPaths(t *testing.T) {
	pack := ArchitectureFactPack{
		CriticalAnchors: []ArchitectureAnchorFact{
			{
				Role:     "ioctl_dispatch",
				Symbol:   "DriverCore::DeviceIoControlIrpHandleRoutine",
				Location: "Driver/DriverCore.cpp:523",
			},
		},
	}
	answer := `
| 역할 | 파일:라인 |
| --- | --- |
| IOCTL dispatch | Driver\DriverCore.cpp:523 |
`
	evaluation := evaluateArchitectureAnswerAgainstFacts(answer, pack)
	if !architectureEvaluationHasCode(evaluation, "exact_anchor_symbol_missing") {
		t.Fatalf("expected exact anchor violation for Windows-style path, got %+v", evaluation)
	}
}

func TestEvaluateArchitectureAnswerAcceptsExactSymbolWithWindowsStylePath(t *testing.T) {
	pack := ArchitectureFactPack{
		CriticalAnchors: []ArchitectureAnchorFact{
			{
				Role:     "ioctl_dispatch",
				Symbol:   "DriverCore::DeviceIoControlIrpHandleRoutine",
				Location: "Driver/DriverCore.cpp:523",
			},
		},
	}
	answer := `
| 심볼 | 파일:라인 |
| --- | --- |
| DriverCore::DeviceIoControlIrpHandleRoutine | Driver\DriverCore.cpp:523 |
`
	evaluation := evaluateArchitectureAnswerAgainstFacts(answer, pack)
	if architectureEvaluationHasCode(evaluation, "exact_anchor_symbol_missing") {
		t.Fatalf("did not expect exact anchor violation when symbol is present, got %+v", evaluation)
	}
}

func TestEvaluateArchitectureAnswerRejectsAccessorLifecycleRelabelWithWindowsStylePath(t *testing.T) {
	pack := ArchitectureFactPack{
		CriticalAnchors: []ArchitectureAnchorFact{
			{
				Role:     "control_pid_accessor",
				Symbol:   "DriverCore::GetControlPid",
				Location: "Driver/DriverCore.cpp:1285",
			},
		},
	}
	answer := `
| 역할 | 파일:라인 |
| --- | --- |
| Finalize/Unload cleanup | Driver\DriverCore.cpp:1285 |
`
	evaluation := evaluateArchitectureAnswerAgainstFacts(answer, pack)
	if !architectureEvaluationHasCode(evaluation, "accessor_anchor_relabelled_as_lifecycle") {
		t.Fatalf("expected accessor relabel violation for Windows-style path, got %+v", evaluation)
	}
}

func TestEvaluateArchitectureAnswerRejectsCollapsedDriverFlows(t *testing.T) {
	pack := ArchitectureFactPack{
		DomainHints: []string{"windows_driver"},
	}
	answer := `
IRP_MJ_CREATE -> IRP_MJ_DEVICE_CONTROL -> DeviceIoControl command dispatch
DriverEntry -> Initialize -> StartObjectFilter runtime callback registration
`
	evaluation := evaluateArchitectureAnswerAgainstFacts(answer, pack)
	if !architectureAnswerHasBlockingViolations(evaluation) {
		t.Fatalf("expected blocking flow separation violation, got %+v", evaluation)
	}
	if !architectureEvaluationHasCode(evaluation, "irp_create_and_device_control_collapsed") {
		t.Fatalf("expected IRP flow separation violation, got %+v", evaluation)
	}
	if !architectureEvaluationHasCode(evaluation, "runtime_registration_collapsed_into_init") {
		t.Fatalf("expected runtime registration separation violation, got %+v", evaluation)
	}
}

func architectureEvaluationHasCode(evaluation ArchitectureAnswerEvaluation, code string) bool {
	for _, violation := range evaluation.Violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}
