package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func printHubCheatsheet(rt *runtimeState, title string, lines []string) {
	fmt.Fprintln(rt.writer, rt.ui.section(title))
	for _, line := range lines {
		fmt.Fprintln(rt.writer, rt.ui.dim(line))
	}
	topic := strings.TrimPrefix(strings.ToLower(title), "/")
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Use /help "+topic+" for details."))
}

func (rt *runtimeState) handleSelectionFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "":
		if selection := rt.session.CurrentSelection(); selection != nil && selection.HasSelection() {
			return rt.handleSelectionCommand()
		}
		printHubCheatsheet(rt, "/selection", []string{
			"/selection              Show the active selection (after /selection open)",
			"/selection list         List saved selections",
			"/selection open <path>  Open a file in the viewer",
			"/selection use <n>      Activate selection n",
			"/selection drop <n>     Drop selection n",
			"/selection note <text>  Annotate the active selection",
			"/selection tag <tags>   Tag the active selection",
			"/selection clear        Clear the active selection",
			"/selection clear-all    Clear all selections",
			"/selection diff         Diff the active selection",
			"/selection edit <task>  Edit within the active selection",
		})
		return nil
	case "list", "selections":
		return rt.handleSelectionsCommand()
	case "open":
		return rt.handleOpenCommand(rest)
	case "use":
		return rt.handleUseSelectionCommand(rest)
	case "drop":
		return rt.handleDropSelectionCommand(rest)
	case "note":
		return rt.handleSelectionNoteCommand(rest)
	case "tag":
		return rt.handleSelectionTagCommand(rest)
	case "clear":
		if current := rt.session.ActiveSelection; !rt.session.RemoveSelection(current) {
			rt.session.ClearSelections()
		}
		_ = rt.store.Save(rt.session)
		fmt.Fprintln(rt.writer, rt.ui.successLine("Cleared current selection"))
		return nil
	case "clear-all", "clear-all-selections":
		rt.session.ClearSelections()
		_ = rt.store.Save(rt.session)
		fmt.Fprintln(rt.writer, rt.ui.successLine("Cleared all selections"))
		return nil
	case "diff":
		return rt.handleSelectionDiffCommand()
	case "edit":
		return rt.handleSelectionEditCommand(rest)
	case "show", "status":
		return rt.handleSelectionCommand()
	default:
		return fmt.Errorf("usage: /selection [list|open|use|drop|note|tag|clear|clear-all|diff|edit]")
	}
}

func (rt *runtimeState) handleMCPFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	if subcommand == "" {
		return rt.printMCPStatus()
	}
	switch subcommand {
	case "resources", "resource-list":
		return rt.printMCPResources()
	case "resource", "read-resource":
		return rt.readMCPResource(rest)
	case "prompts", "prompt-list":
		return rt.printMCPPrompts()
	case "prompt", "get-prompt":
		return rt.runMCPPrompt(rest)
	case "skills":
		return rt.printSkills()
	default:
		return rt.handleMCPSubcommand(tokenizeCommandArgs(args))
	}
}

func (rt *runtimeState) printMCPStatus() error {
	statuses := rt.mcpStatus()
	if len(statuses) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No MCP servers configured."))
		printHubCheatsheet(rt, "/mcp", []string{
			"/mcp                         Show configured MCP servers",
			"/mcp add|remove|enable|disable|auth  Manage servers",
			"/mcp resources               List MCP resources",
			"/mcp resource <server:uri>   Read one resource",
			"/mcp prompts                 List MCP prompts",
			"/mcp prompt <server:name>    Resolve one prompt",
			"/mcp skills                  List discovered skills",
		})
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("MCP"))
	for _, status := range statuses {
		environmentID := mcpStatusEnvironmentID(status)
		authSuffix := mcpStatusAuthSuffix(status)
		if strings.TrimSpace(status.Error) != "" {
			fmt.Fprintf(rt.writer, "%s  transport=%s  env=%s%s  error=%s\n", status.Name, valueOrDefault(status.Transport, "stdio"), environmentID, authSuffix, status.Error)
			continue
		}
		extra := ""
		if rt.mcp != nil {
			if server, ok := rt.mcp.ServerConfig(status.Name); ok {
				extra = webResearchMCPStatusSummary(server, os.Getenv)
			}
		}
		location := status.Cwd
		locationLabel := "cwd"
		if strings.TrimSpace(status.URL) != "" {
			location = status.URL
			locationLabel = "url"
		}
		fmt.Fprintf(rt.writer, "%s  tools=%d  resources=%d  prompts=%d  transport=%s  env=%s%s  %s=%s%s\n", status.Name, status.ToolCount, status.ResourceCount, status.PromptCount, valueOrDefault(status.Transport, "stdio"), environmentID, authSuffix, locationLabel, location, extra)
	}
	return nil
}

func (rt *runtimeState) printMCPResources() error {
	items := rt.mcpResources()
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No MCP resources discovered."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Resources"))
	for _, item := range items {
		label := item.Resource.URI
		if label == "" {
			label = item.Resource.Name
		}
		line := fmt.Sprintf("%s:%s", item.Server, label)
		if item.Resource.Name != "" && item.Resource.Name != label {
			line += " (" + item.Resource.Name + ")"
		}
		fmt.Fprintln(rt.writer, line)
		if strings.TrimSpace(item.Resource.Description) != "" {
			fmt.Fprintln(rt.writer, rt.ui.dim("  "+item.Resource.Description))
		}
	}
	return nil
}

func (rt *runtimeState) readMCPResource(args string) error {
	if strings.TrimSpace(args) == "" {
		return fmt.Errorf("usage: /mcp resource <server:resource-uri-or-name>")
	}
	if rt.mcp == nil {
		return fmt.Errorf("no MCP servers configured")
	}
	display, text, err := rt.mcp.ReadResource(context.Background(), args)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Resource"))
	fmt.Fprintln(rt.writer, rt.ui.dim(display))
	fmt.Fprintln(rt.writer, text)
	return nil
}

func (rt *runtimeState) printMCPPrompts() error {
	items := rt.mcpPrompts()
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No MCP prompts discovered."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Prompts"))
	for _, item := range items {
		argLabels := []string{}
		for _, arg := range item.Prompt.Arguments {
			label := arg.Name
			if arg.Required {
				label += "*"
			}
			argLabels = append(argLabels, label)
		}
		fmt.Fprintf(rt.writer, "%s:%s(%s)\n", item.Server, item.Prompt.Name, strings.Join(argLabels, ", "))
		if strings.TrimSpace(item.Prompt.Description) != "" {
			fmt.Fprintln(rt.writer, rt.ui.dim("  "+item.Prompt.Description))
		}
	}
	return nil
}

func (rt *runtimeState) runMCPPrompt(args string) error {
	if strings.TrimSpace(args) == "" {
		return fmt.Errorf("usage: /mcp prompt <server:prompt-name> [json-arguments]")
	}
	if rt.mcp == nil {
		return fmt.Errorf("no MCP servers configured")
	}
	target, promptArgs, err := parsePromptCommandArgs(args)
	if err != nil {
		return err
	}
	display, text, err := rt.mcp.GetPrompt(context.Background(), target, promptArgs)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Prompt"))
	fmt.Fprintln(rt.writer, rt.ui.dim(display))
	fmt.Fprintln(rt.writer, text)
	return nil
}

func (rt *runtimeState) printSkills() error {
	items := rt.skills.Items()
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No skills discovered."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Skills"))
	for _, skill := range items {
		label := skill.Name
		if skill.Enabled {
			label += " [enabled]"
		}
		fmt.Fprintln(rt.writer, label)
		fmt.Fprintln(rt.writer, rt.ui.dim("  "+skill.Path))
		if strings.TrimSpace(skill.Summary) != "" {
			fmt.Fprintln(rt.writer, rt.ui.dim("  "+skill.Summary))
		}
	}
	return nil
}

func (rt *runtimeState) handleHooksFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "":
		rt.handleHooksCommand()
		return nil
	case "reload":
		rt.reloadHooks()
		fmt.Fprintln(rt.writer, rt.ui.successLine("Reloaded hook configuration"))
		return nil
	case "override", "overrides":
		return rt.handleOverrideFamilyCommand(rest)
	default:
		printHubCheatsheet(rt, "/hooks", []string{
			"/hooks                 Show loaded hook rules",
			"/hooks reload          Reload hook configuration only",
			"/hooks override        List temporary overrides",
			"/hooks override add    Add a temporary override",
			"/hooks override clear  Clear overrides",
			"/reload                Reload config, memory, skills, hooks, and MCP",
		})
		return nil
	}
}

func (rt *runtimeState) handleSettingsFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "":
		printHubCheatsheet(rt, "/settings", []string{
			"/settings preset [speed|balanced|strict]",
			"/settings auto-verify [on|off]",
			"/settings locale-auto [on|off]",
			"/settings max-tool-iterations <n|0|unlimited>",
			"/settings progress-display [quiet|compact|auto|stream]",
			"/config                       Show full effective config",
		})
		fmt.Fprintln(rt.writer, rt.ui.statusKV("runtime_preset", configRuntimePreset(rt.cfg)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_verify", fmt.Sprintf("%t", configAutoVerify(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("inject_project_analysis", fmt.Sprintf("%t", configInjectProjectAnalysis(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("locale_auto", fmt.Sprintf("%t", configAutoLocale(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("max_tool_iterations", formatMaxToolIterations(configMaxToolIterations(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("progress_display", configProgressDisplay(rt.cfg)))
		return nil
	case "preset", "runtime-preset", "mode":
		return rt.handleRuntimePresetCommand(rest)
	case "auto-verify", "set-auto-verify":
		return rt.handleSetAutoVerifyCommand(rest)
	case "locale-auto", "locale":
		return rt.handleLocaleAutoCommand(rest)
	case "max-tool-iterations", "set-max-tool-iterations", "tool-iterations":
		return rt.handleSetMaxToolIterationsCommand(rest)
	case "progress-display", "progress":
		return rt.handleProgressDisplayCommand(rest)
	default:
		return fmt.Errorf("usage: /settings [preset|auto-verify|locale-auto|max-tool-iterations|progress-display]")
	}
}

func (rt *runtimeState) handleRuntimePresetCommand(args string) error {
	if rt == nil {
		return fmt.Errorf("no runtime")
	}
	arg := strings.TrimSpace(args)
	if arg == "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("runtime_preset: "+configRuntimePreset(rt.cfg)))
		fmt.Fprintln(rt.writer, rt.ui.hintLine(localizedText(rt.cfg,
			"Presets: speed (default, faster), balanced (auto review on), strict (verify+classifier+analysis inject).",
			"프리셋: speed(기본·빠름), balanced(자동 리뷰 on), strict(검증+분류기+분석 주입).")))
		return nil
	}
	if !applyRuntimePreset(&rt.cfg, arg) {
		return fmt.Errorf("usage: /settings preset [speed|balanced|strict]")
	}
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(localizedText(rt.cfg,
		"runtime_preset set to "+configRuntimePreset(rt.cfg),
		"runtime_preset을 "+configRuntimePreset(rt.cfg)+"로 설정했습니다")))
	review := configReviewHarness(rt.cfg)
	autoAfter := review.AutoAfterChange != nil && *review.AutoAfterChange
	fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_verify", fmt.Sprintf("%t", configAutoVerify(rt.cfg))))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("review.auto_after_change", fmt.Sprintf("%t", autoAfter)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("semantic_classifier", requestSemanticClassifierStatusLine(rt.cfg.RequestRuntime.SemanticClassifier)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("inject_project_analysis", fmt.Sprintf("%t", configInjectProjectAnalysis(rt.cfg))))
	return nil
}

func (rt *runtimeState) handleSetMaxToolIterationsCommand(args string) error {
	if strings.TrimSpace(args) == "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("max_tool_iterations: "+formatMaxToolIterations(configMaxToolIterations(rt.cfg))))
		return nil
	}
	argText := strings.TrimSpace(args)
	var val int
	if strings.EqualFold(argText, "unlimited") || strings.EqualFold(argText, "none") || strings.EqualFold(argText, "off") {
		val = 0
	} else {
		parsed, err := strconv.Atoi(argText)
		if err != nil || parsed < 0 {
			return fmt.Errorf("invalid value: must be a non-negative integer (0 or \"unlimited\" disables the cap)")
		}
		val = parsed
	}
	rt.cfg.MaxToolIterations = val
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("max_tool_iterations set to "+formatMaxToolIterations(val)))
	return nil
}

func (rt *runtimeState) handleAnalyzeFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "":
		printHubCheatsheet(rt, "/analyze", []string{
			"/analyze project [--mode ...] [goal]  Multi-agent project analysis",
			"/analyze dashboard [latest|path]      Open analysis docs portal",
			"/analyze performance [focus]          Performance analysis pass",
			"/analyze docs-refresh                 Regen docs/dashboard/corpus",
		})
		return nil
	case "project", "run":
		return rt.handleAnalyzeProjectCommand(rest)
	case "dashboard":
		return rt.handleAnalyzeDashboardCommand(rest)
	case "performance", "perf":
		return rt.handleAnalyzePerformanceCommand(rest)
	case "docs-refresh", "refresh", "docs":
		return rt.handleDocsRefreshCommand(rest)
	default:
		return rt.handleAnalyzeProjectCommand(strings.TrimSpace(args))
	}
}

func (rt *runtimeState) handleProbeFamilyCommand(args string) error {
	subcommand, rest := commandSubcommandAndRest(args)
	switch subcommand {
	case "":
		printHubCheatsheet(rt, "/probe", []string{
			"/probe fuzz <name|flags>           Directed function fuzz planning",
			"/probe campaign [status|run|...]   Fuzz campaign planner",
			"/probe scan [status|run|...]       Source bug-pattern scan",
			"/probe root-cause <problem>        Symptom → root-cause analysis",
			"/probe patterns [list|match|...]   Root-cause pattern packs",
			"/probe driver-poc <name> [--type]  Kernel driver POC generator",
		})
		return nil
	case "fuzz", "fuzz-func":
		return rt.handleFuzzFuncCommand(rest)
	case "campaign", "fuzz-campaign":
		return rt.handleFuzzCampaignCommand(rest)
	case "scan", "source-scan":
		return rt.handleSourceScanCommand(rest)
	case "root-cause", "find-root-cause", "rca":
		return rt.handleFindRootCauseCommand(rest)
	case "patterns", "root-cause-patterns":
		return rt.handleRootCausePatternsCommand(rest)
	case "driver-poc", "create-driver-poc", "poc":
		return rt.handleCreateDriverPOCCommand(rest)
	default:
		return fmt.Errorf("usage: /probe [fuzz|campaign|scan|root-cause|patterns|driver-poc]")
	}
}
