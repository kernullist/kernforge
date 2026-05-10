package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

func renderReviewRunMarkdown(run ReviewRun) string {
	var b strings.Builder
	b.WriteString("# KernForge Review\n\n")
	fmt.Fprintf(&b, "- Review ID: `%s`\n", run.ID)
	fmt.Fprintf(&b, "- Schema: `%s`\n", run.SchemaVersion)
	fmt.Fprintf(&b, "- Target: `%s`\n", run.Target)
	fmt.Fprintf(&b, "- Mode: `%s`\n", run.Mode)
	fmt.Fprintf(&b, "- Flow: `%s`\n", run.Flow)
	fmt.Fprintf(&b, "- Verdict: `%s`\n", valueOrDefault(run.Gate.Verdict, run.Result.Verdict))
	fmt.Fprintf(&b, "- Machine status: `%s` exit=%d\n", run.MachineStatus, run.ExitCode)
	fmt.Fprintf(&b, "- Workspace: `%s`\n", filepath.ToSlash(run.Workspace))
	if strings.TrimSpace(run.Branch) != "" {
		fmt.Fprintf(&b, "- Branch: `%s`\n", run.Branch)
	}
	if strings.TrimSpace(run.Objective) != "" {
		fmt.Fprintf(&b, "- Objective: %s\n", run.Objective)
	}
	if run.Freshness.Stale {
		fmt.Fprintf(&b, "- Freshness: stale (%s)\n", run.Freshness.StaleReason)
	}
	if run.Redaction.Redacted {
		fmt.Fprintf(&b, "- Redaction: %s\n", strings.Join(run.Redaction.Patterns, ", "))
	}
	b.WriteString("\n## Summary\n\n")
	b.WriteString(valueOrDefault(run.Result.Summary, run.Gate.Reason))
	b.WriteString("\n\n")
	if len(run.Gate.BlockingFindings) > 0 {
		b.WriteString("## Blocking Findings\n\n")
		for _, finding := range run.Findings {
			if reviewFindingBlocksGate(run, finding) {
				renderReviewFindingMarkdown(&b, finding)
			}
		}
	}
	if len(run.Gate.WarningFindings) > 0 {
		b.WriteString("## Warnings\n\n")
		for _, finding := range run.Findings {
			if !reviewFindingBlocksGate(run, finding) && reviewFindingCountsAsWarning(finding) {
				renderReviewFindingMarkdown(&b, finding)
			}
		}
	}
	if len(run.Findings) > 0 {
		b.WriteString("## All Findings\n\n")
		for _, finding := range run.Findings {
			fmt.Fprintf(&b, "- `%s` `%s` `%s`: %s\n", finding.ID, finding.Severity, finding.Category, finding.Title)
		}
		b.WriteString("\n")
	}
	if len(run.Gate.RequiredActions) > 0 {
		b.WriteString("## Required Actions\n\n")
		for _, action := range run.Gate.RequiredActions {
			if strings.TrimSpace(action) != "" {
				fmt.Fprintf(&b, "- %s\n", action)
			}
		}
		b.WriteString("\n")
	}
	if run.RepairPlan.Required {
		b.WriteString("## Repair Prompt\n\n")
		b.WriteString("```text\n")
		b.WriteString(run.RepairPlan.Prompt)
		b.WriteString("\n```\n\n")
	}
	if len(run.Gate.NextCommands) > 0 {
		b.WriteString("## Next Commands\n\n")
		for _, cmd := range run.Gate.NextCommands {
			fmt.Fprintf(&b, "- `%s`\n", cmd.Command)
			if strings.TrimSpace(cmd.Reason) != "" {
				fmt.Fprintf(&b, "  - Why: %s\n", cmd.Reason)
			}
			if strings.TrimSpace(cmd.When) != "" {
				fmt.Fprintf(&b, "  - When: %s\n", cmd.When)
			}
			if strings.TrimSpace(cmd.Safety) != "" {
				fmt.Fprintf(&b, "  - Safety: `%s`\n", cmd.Safety)
			}
			if strings.TrimSpace(cmd.ClientHint) != "" {
				fmt.Fprintf(&b, "  - Action: %s\n", cmd.ClientHint)
			}
		}
		b.WriteString("\n")
	}
	if len(run.ModelPlan.UserGuidance) > 0 {
		b.WriteString("## Model Guidance\n\n")
		for _, item := range run.ModelPlan.UserGuidance {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if len(run.ChangeSet.ChangedPaths) > 0 {
		b.WriteString("## Changed Paths\n\n")
		for _, path := range limitStrings(run.ChangeSet.ChangedPaths, 64) {
			fmt.Fprintf(&b, "- `%s`\n", filepath.ToSlash(path))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func renderReviewFindingMarkdown(b *strings.Builder, finding ReviewFinding) {
	fmt.Fprintf(b, "### %s `%s` %s\n\n", finding.ID, finding.Severity, finding.Title)
	if strings.TrimSpace(finding.Path) != "" {
		fmt.Fprintf(b, "- Path: `%s`\n", filepath.ToSlash(finding.Path))
	}
	if strings.TrimSpace(finding.Symbol) != "" {
		fmt.Fprintf(b, "- Symbol: `%s`\n", finding.Symbol)
	}
	fmt.Fprintf(b, "- Category: `%s`\n", finding.Category)
	if strings.TrimSpace(finding.Evidence) != "" {
		fmt.Fprintf(b, "- Evidence: %s\n", finding.Evidence)
	}
	if strings.TrimSpace(finding.Impact) != "" {
		fmt.Fprintf(b, "- Impact: %s\n", finding.Impact)
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		fmt.Fprintf(b, "- Required fix: %s\n", finding.RequiredFix)
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" {
		fmt.Fprintf(b, "- Test: `%s`\n", finding.TestRecommendation)
	}
	b.WriteString("\n")
}

func renderReviewEvidenceMarkdown(run ReviewRun) string {
	var b strings.Builder
	b.WriteString("# KernForge Review Evidence\n\n")
	fmt.Fprintf(&b, "- Review ID: `%s`\n", run.ID)
	fmt.Fprintf(&b, "- Fingerprint: `%s`\n", run.ReviewFingerprint)
	if len(run.Evidence.Sources) > 0 {
		fmt.Fprintf(&b, "- Sources: %s\n", strings.Join(run.Evidence.Sources, ", "))
	}
	if len(run.Evidence.Warnings) > 0 {
		b.WriteString("\n## Warnings\n\n")
		for _, warning := range run.Evidence.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}
	if strings.TrimSpace(run.Evidence.Text) != "" {
		b.WriteString("\n")
		b.WriteString(run.Evidence.Text)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func reviewRunPrefersKorean(cfg Config, run ReviewRun) bool {
	language, _ := inferResponseLanguageForUserText(run.Objective, cfg)
	return language == "ko"
}

func reviewRunLocalizedText(cfg Config, run ReviewRun, english string, korean string) string {
	if reviewRunPrefersKorean(cfg, run) {
		return korean
	}
	return english
}

func renderReviewCLIResult(cfg Config, run ReviewRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n", reviewRunLocalizedText(cfg, run, "Review", "리뷰"), run.ID, run.Gate.Verdict)
	fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Target", "대상"), run.Target)
	fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Mode", "모드"), run.Mode)
	fmt.Fprintf(&b, "- %s: %d blocker=%d warning=%d\n", reviewRunLocalizedText(cfg, run, "Findings", "발견"), len(run.Findings), len(run.Gate.BlockingFindings), len(run.Gate.WarningFindings))
	if len(run.ArtifactRefs) > 0 {
		fmt.Fprintf(&b, "- %s: %s\n", reviewRunLocalizedText(cfg, run, "Report", "보고서"), run.ArtifactRefs[0])
	}
	rendered := map[string]bool{}
	for _, finding := range run.Findings {
		if reviewFindingBlocksGate(run, finding) {
			renderReviewCLIFinding(&b, cfg, run, finding, reviewRunLocalizedText(cfg, run, "Fix", "수정"))
			rendered[finding.ID] = true
		}
	}
	warnings := reviewCLIWarningFindings(run)
	if len(warnings) > 0 {
		fmt.Fprintf(&b, "\n%s:\n", reviewRunLocalizedText(cfg, run, "Warnings", "경고"))
		for _, finding := range warnings {
			if rendered[finding.ID] {
				continue
			}
			renderReviewCLIFinding(&b, cfg, run, finding, reviewRunLocalizedText(cfg, run, "Suggested fix", "권장 조치"))
			rendered[finding.ID] = true
		}
	}
	if len(run.Gate.NextCommands) > 0 {
		fmt.Fprintf(&b, "\n%s:\n", reviewRunLocalizedText(cfg, run, "Next commands", "다음 명령"))
		for _, cmd := range run.Gate.NextCommands {
			renderReviewCLINextCommand(&b, cfg, run, cmd)
		}
	}
	return strings.TrimSpace(b.String())
}

func renderReviewCLIFinding(b *strings.Builder, cfg Config, run ReviewRun, finding ReviewFinding, fixLabel string) {
	fmt.Fprintf(b, "\n[%s] %s: %s\n", finding.ID, finding.Severity, finding.Title)
	if strings.TrimSpace(finding.Evidence) != "" && !strings.EqualFold(strings.TrimSpace(finding.Evidence), strings.TrimSpace(finding.Title)) {
		fmt.Fprintf(b, "%s: %s\n", reviewRunLocalizedText(cfg, run, "Evidence", "근거"), finding.Evidence)
	}
	if strings.TrimSpace(finding.Impact) != "" {
		fmt.Fprintf(b, "%s: %s\n", reviewRunLocalizedText(cfg, run, "Impact", "영향"), finding.Impact)
	}
	if strings.TrimSpace(finding.RequiredFix) != "" {
		fmt.Fprintf(b, "%s: %s\n", fixLabel, finding.RequiredFix)
	}
	if strings.TrimSpace(finding.TestRecommendation) != "" {
		fmt.Fprintf(b, "%s: %s\n", reviewRunLocalizedText(cfg, run, "Test", "테스트"), finding.TestRecommendation)
	}
}

func renderReviewCLINextCommand(b *strings.Builder, cfg Config, run ReviewRun, cmd ReviewNextCommand) {
	fmt.Fprintf(b, "- %s\n", cmd.Command)
	if reason := reviewNextCommandReasonText(cfg, run, cmd); strings.TrimSpace(reason) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "Why", "이유"), reason)
	}
	if when := reviewNextCommandWhenText(cfg, run, cmd); strings.TrimSpace(when) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "When", "시점"), when)
	}
	if strings.TrimSpace(cmd.Safety) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "Safety", "안전성"), cmd.Safety)
	}
	if hint := reviewNextCommandHintText(cfg, run, cmd); strings.TrimSpace(hint) != "" {
		fmt.Fprintf(b, "  %s: %s\n", reviewRunLocalizedText(cfg, run, "Action", "실행 방법"), hint)
	}
}

func reviewNextCommandReasonText(cfg Config, run ReviewRun, cmd ReviewNextCommand) string {
	if !reviewRunPrefersKorean(cfg, run) {
		return cmd.Reason
	}
	switch strings.TrimSpace(cmd.ID) {
	case "verify":
		return "변경된 파일에 대한 최신 빌드/테스트 근거가 없습니다."
	case "repair":
		return "차단 finding이 있어서 위 RF 항목을 기준으로 수정 작업을 이어가야 합니다."
	case "repair-warnings":
		return "경고 finding이 실제 코드 수정으로 이어질 수 있는 항목입니다."
	case "completion-audit":
		return "경고가 남아 있으므로 완료 선언 전에 최종 준비 상태를 점검해야 합니다."
	case "set-security-model":
		return "보안 민감 리뷰가 전용 보안 리뷰어 없이 fallback 모델로 실행되었습니다."
	case "set-false-positive-model":
		return "탐지/안티치트 리뷰가 전용 오탐 리뷰어 없이 fallback 모델로 실행되었습니다."
	default:
		return cmd.Reason
	}
}

func reviewNextCommandWhenText(cfg Config, run ReviewRun, cmd ReviewNextCommand) string {
	if !reviewRunPrefersKorean(cfg, run) {
		return cmd.When
	}
	switch strings.TrimSpace(cmd.ID) {
	case "verify":
		return "완료 선언 또는 git write 전에"
	case "repair":
		return "리뷰 finding을 확인한 직후"
	case "repair-warnings":
		return "경고를 수용하지 않고 바로 수정하려는 경우"
	case "completion-audit":
		return "최종 답변 또는 완료 처리 전에"
	case "set-security-model", "set-false-positive-model":
		return "다음 보안/탐지 리뷰 전에"
	default:
		return cmd.When
	}
}

func reviewNextCommandHintText(cfg Config, run ReviewRun, cmd ReviewNextCommand) string {
	if !reviewRunPrefersKorean(cfg, run) {
		return cmd.ClientHint
	}
	switch strings.TrimSpace(cmd.ID) {
	case "verify":
		return "`/verify --full`로 검증을 실행한 뒤 `/review`를 다시 실행해 최신 근거를 붙이세요."
	case "repair":
		return "이 명령을 실행하거나 자연어로 `수정해줘`라고 이어가면 최신 리뷰 finding을 기준으로 repair 흐름을 시작합니다."
	case "repair-warnings":
		return "자연어로 `수정해줘`라고 이어가거나 이 명령을 실행하면 최신 warning finding을 기준으로 repair 흐름을 시작합니다."
	case "completion-audit":
		return "남은 경고를 수용 가능한 잔여 리스크로 볼 수 있는지 읽기 전용으로 점검합니다."
	case "set-security-model":
		return "`/review models security`에서 전용 보안 리뷰어 모델을 번호로 선택하세요."
	case "set-false-positive-model":
		return "`/review models false-positive`에서 전용 오탐 리뷰어 모델을 번호로 선택하세요."
	default:
		return cmd.ClientHint
	}
}

func reviewCLIWarningFindings(run ReviewRun) []ReviewFinding {
	warningIDs := reviewFindingIDSet(run.Gate.WarningFindings)
	var out []ReviewFinding
	for _, finding := range run.Findings {
		if reviewFindingBlocksGate(run, finding) {
			continue
		}
		if len(warningIDs) > 0 {
			if warningIDs[finding.ID] {
				out = append(out, finding)
			}
			continue
		}
		if strings.EqualFold(run.Gate.Verdict, reviewVerdictApprovedWithWarnings) &&
			reviewFindingCountsAsWarning(finding) {
			out = append(out, finding)
		}
	}
	return out
}

func reviewFindingIDSet(ids []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = true
		}
	}
	return out
}
