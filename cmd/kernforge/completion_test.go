package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompleteSlashSubcommandEnumeratedArguments(t *testing.T) {
	rt := &runtimeState{
		cfg: DefaultConfig(t.TempDir()),
	}

	cases := []struct {
		input       string
		wantBuffer  string
		wantSuggest []string
	}{
		{input: "/permissions a", wantBuffer: "/permissions acceptEdits "},
		{input: "/permissions :", wantSuggest: []string{"/permissions :read-only", "/permissions :workspace", "/permissions :danger-full-access"}},
		{input: "/analyze-project ", wantBuffer: "/analyze-project --"},
		{input: "/analyze-project --m", wantBuffer: "/analyze-project --mode "},
		{input: "/analyze-project --d", wantBuffer: "/analyze-project --d"},
		{input: "/analyze-project --p", wantBuffer: "/analyze-project --path "},
		{input: "/analyze-project --mode ", wantSuggest: []string{"/analyze-project --mode map", "/analyze-project --mode trace", "/analyze-project --mode impact", "/analyze-project --mode surface", "/analyze-project --mode security", "/analyze-project --mode performance"}},
		{input: "/analyze-project --mode sec", wantBuffer: "/analyze-project --mode security "},
		{input: "/analyze-project --docs --m", wantBuffer: "/analyze-project --docs --mode "},
		{input: "/analyze-project --mode surface --d", wantBuffer: "/analyze-project --mode surface --d"},
		{input: "/checkpoint-auto of", wantBuffer: "/checkpoint-auto off "},
		{input: "/locale-auto of", wantBuffer: "/locale-auto off "},
		{input: "/set-auto-verify of", wantBuffer: "/set-auto-verify off "},
		{input: "/progress-display ", wantSuggest: []string{"/progress-display auto", "/progress-display compact", "/progress-display stream"}},
		{input: "/progress-display st", wantBuffer: "/progress-display stream "},
		{input: "/progress_display ", wantSuggest: []string{"/progress-display auto", "/progress-display compact", "/progress-display stream"}},
		{input: "/progress_display st", wantBuffer: "/progress-display stream "},
		{input: "/worktree ", wantSuggest: []string{"/worktree status", "/worktree list", "/worktree create", "/worktree enter", "/worktree attach", "/worktree leave", "/worktree cleanup"}},
		{input: "/worktree cr", wantBuffer: "/worktree create "},
		{input: "/specialists ", wantSuggest: []string{"/specialists status", "/specialists assign", "/specialists cleanup"}},
		{input: "/specialists cl", wantBuffer: "/specialists cleanup "},
		{input: "/effort ", wantSuggest: []string{"/effort undefined", "/effort minimal", "/effort low", "/effort medium", "/effort high", "/effort xhigh"}},
		{input: "/effort h", wantBuffer: "/effort high "},
		{input: "/provider ", wantSuggest: []string{"/provider status", "/provider openai-codex-subscription", "/provider openai-codex-cli", "/provider openai-api", "/provider anthropic-claude-cli", "/provider anthropic-api", "/provider deepseek", "/provider openrouter", "/provider opencode", "/provider opencode-go", "/provider ollama", "/provider lmstudio", "/provider vllm", "/provider llama.cpp"}},
		{input: "/provider st", wantBuffer: "/provider status "},
		{input: "/codex-auth ", wantSuggest: []string{"/codex-auth status", "/codex-auth login", "/codex-auth logout", "/codex-auth path"}},
		{input: "/codex-auth lo", wantBuffer: "/codex-auth log"},
		{input: "/verify --", wantBuffer: "/verify --full "},
		{input: "/verify-dashboard a", wantBuffer: "/verify-dashboard all "},
		{input: "/verify-dashboard-html a", wantBuffer: "/verify-dashboard-html all "},
		{input: "/model ", wantSuggest: []string{"/model status", "/model main", "/model analysis-worker", "/model analysis-reviewer", "/model cross-review", "/model clear", "/model task-owner"}},
		{input: "/model cross-review ", wantSuggest: []string{"/model cross-review status", "/model cross-review openai-codex-subscription", "/model cross-review openai-codex-cli", "/model cross-review openai-api", "/model cross-review anthropic-claude-cli", "/model cross-review anthropic-api", "/model cross-review deepseek", "/model cross-review openrouter", "/model cross-review opencode", "/model cross-review opencode-go", "/model cross-review ollama", "/model cross-review lmstudio", "/model cross-review vllm", "/model cross-review llama.cpp"}},
		{input: "/model cross-review op", wantBuffer: "/model cross-review open"},
		{input: "/model clear ", wantBuffer: "/model clear cross-review "},
		{input: "/review ", wantSuggest: []string{"/review change", "/review plan", "/review selection", "/review pr", "/review final", "/review goal", "/review analysis", "/review --no-model", "/review --mode", "/review --follow-up", "/review --no-follow-up"}},
		{input: "/review --mode ", wantSuggest: []string{"/review --mode general-change", "/review --mode security-hardening", "/review --mode core-build", "/review --mode live-fix", "/review --mode refactor", "/review --mode research", "/review --mode ui-polish"}},
		{input: "/mem-prune a", wantBuffer: "/mem-prune all "},
		{input: "/profile ", wantSuggest: []string{"/profile list", "/profile show", "/profile status", "/profile pin", "/profile unpin", "/profile rename", "/profile delete"}},
		{input: "/set-analysis-models ", wantSuggest: []string{"/set-analysis-models status", "/set-analysis-models worker", "/set-analysis-models reviewer", "/set-analysis-models clear"}},
		{input: "/set-analysis-models w", wantBuffer: "/set-analysis-models worker "},
		{input: "/set-analysis-models worker op", wantBuffer: "/set-analysis-models worker open"},
		{input: "/set-specialist-model ", wantSuggest: []string{"/set-specialist-model status", "/set-specialist-model clear", "/set-specialist-model attack-surface-analyst", "/set-specialist-model driver-build-fixer", "/set-specialist-model implementation-owner", "/set-specialist-model kernel-investigator", "/set-specialist-model memory-inspection-analyst", "/set-specialist-model planner", "/set-specialist-model telemetry-analyst", "/set-specialist-model unreal-integrity-analyst"}},
		{input: "/set-specialist-model pl", wantBuffer: "/set-specialist-model planner "},
		{input: "/set-specialist-model planner op", wantBuffer: "/set-specialist-model planner open"},
		{input: "/set-specialist-model clear al", wantBuffer: "/set-specialist-model clear all "},
		{input: "/new-feature ", wantSuggest: []string{"/new-feature start", "/new-feature status", "/new-feature list", "/new-feature plan", "/new-feature implement", "/new-feature close"}},
		{input: "/new-feature im", wantBuffer: "/new-feature implement "},
		{input: "/investigate ", wantSuggest: []string{"/investigate status", "/investigate start", "/investigate snapshot", "/investigate note", "/investigate stop", "/investigate show", "/investigate list", "/investigate dashboard", "/investigate dashboard-html"}},
		{input: "/investigate start d", wantBuffer: "/investigate start driver-visibility "},
		{input: "/simulate ", wantSuggest: []string{"/simulate status", "/simulate show", "/simulate list", "/simulate dashboard", "/simulate dashboard-html", "/simulate tamper-surface", "/simulate stealth-surface", "/simulate forensic-blind-spot"}},
		{input: "/simulate t", wantBuffer: "/simulate tamper-surface "},
		{input: "/fuzz-func ", wantSuggest: []string{"/fuzz-func <function-name>", "/fuzz-func <function-name> --file <path>", "/fuzz-func <function-name> @<path>", "/fuzz-func <function-name> --source-scan focused", "/fuzz-func <function-name> --source-scan full", "/fuzz-func <function-name> --no-source-scan", "/fuzz-func --from-candidate <id>", "/fuzz-func --file <path>", "/fuzz-func @<path>", "/fuzz-func status", "/fuzz-func show", "/fuzz-func list", "/fuzz-func continue", "/fuzz-func continue --profile extended", "/fuzz-func repro <crash>", "/fuzz-func minimize <crash>", "/fuzz-func language"}},
		{input: "/fuzz-func sh", wantBuffer: "/fuzz-func show "},
		{input: "/fuzz-func language ", wantSuggest: []string{"/fuzz-func language system", "/fuzz-func language english"}},
		{input: "/fuzz-campaign ", wantSuggest: []string{"/fuzz-campaign status", "/fuzz-campaign run", "/fuzz-campaign new", "/fuzz-campaign list", "/fuzz-campaign show"}},
		{input: "/fuzz-campaign sh", wantBuffer: "/fuzz-campaign show "},
		{input: "/source-scan ", wantSuggest: []string{"/source-scan status", "/source-scan run", "/source-scan run --limit 50", "/source-scan run --only-slugs probe-copy-size-drift,double-fetch-user-buffer", "/source-scan run --files driver/nsi.c,api/registry.c", "/source-scan list", "/source-scan show", "/source-scan revalidate"}},
		{input: "/create-driver-poc ", wantSuggest: []string{"/create-driver-poc <driver-name>", "/create-driver-poc <driver-name> --type objectfilter", "/create-driver-poc <driver-name> --type minifilter", "/create-driver-poc <driver-name> --type registryfilter", "/create-driver-poc <driver-name> --type wfpcallout"}},
		{input: "/create-driver-poc Acme --", wantBuffer: "/create-driver-poc Acme --type "},
		{input: "/create-driver-poc Acme --type ", wantSuggest: []string{"/create-driver-poc Acme --type objectfilter", "/create-driver-poc Acme --type minifilter", "/create-driver-poc Acme --type registryfilter", "/create-driver-poc Acme --type wfpcallout"}},
		{input: "/create-driver-poc Acme --type wf", wantBuffer: "/create-driver-poc Acme --type wfpcallout "},
		{input: "/init ", wantSuggest: []string{"/init config", "/init hooks", "/init memory-policy", "/init skill", "/init verify"}},
		{input: "/init m", wantBuffer: "/init memory-policy "},
	}

	for _, tc := range cases {
		gotBuffer, gotSuggest, ok := rt.completeSlashCommand(tc.input)
		if !ok {
			t.Fatalf("%q: expected completion to apply", tc.input)
		}
		if tc.wantBuffer != "" && gotBuffer != tc.wantBuffer {
			t.Fatalf("%q: unexpected buffer: got %q want %q", tc.input, gotBuffer, tc.wantBuffer)
		}
		if tc.wantSuggest != nil {
			if len(gotSuggest) != len(tc.wantSuggest) {
				t.Fatalf("%q: unexpected suggestion count: got %#v want %#v", tc.input, gotSuggest, tc.wantSuggest)
			}
			for i := range tc.wantSuggest {
				if gotSuggest[i] != tc.wantSuggest[i] {
					t.Fatalf("%q: unexpected suggestion[%d]: got %q want %q", tc.input, i, gotSuggest[i], tc.wantSuggest[i])
				}
			}
		}
	}
}

func TestCompleteAnalyzeProjectPathArgument(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "driver"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rt := &runtimeState{
		cfg:       DefaultConfig(root),
		workspace: Workspace{Root: root, BaseRoot: root},
	}
	got, suggestions, ok := rt.completeLine("/analyze-project --path src/dr")
	if !ok {
		t.Fatalf("expected completion to handle analyze-project path")
	}
	if len(suggestions) != 0 {
		t.Fatalf("expected direct completion, got suggestions %#v", suggestions)
	}
	if got != "/analyze-project --path src/driver/" {
		t.Fatalf("unexpected completion: %q", got)
	}
}

func TestCompleteSlashCommandIncludesRecentlyAddedCommands(t *testing.T) {
	rt := &runtimeState{}

	cases := []struct {
		input      string
		wantBuffer string
	}{
		{input: "/evi", wantBuffer: "/evidence"},
		{input: "/invest", wantBuffer: "/investigate"},
		{input: "/new-f", wantBuffer: "/new-feature "},
		{input: "/docs-r", wantBuffer: "/docs-refresh "},
		{input: "/analyze-d", wantBuffer: "/analyze-dashboard "},
		{input: "/simu", wantBuffer: "/simulate"},
		{input: "/fuzz-f", wantBuffer: "/fuzz-func "},
		{input: "/fuzz-c", wantBuffer: "/fuzz-campaign "},
		{input: "/create-d", wantBuffer: "/create-driver-poc "},
		{input: "/find-r", wantBuffer: "/find-root-cause "},
		{input: "/spec", wantBuffer: "/specialists "},
		{input: "/workt", wantBuffer: "/worktree "},
		{input: "/override-a", wantBuffer: "/override-add "},
		{input: "/hook-r", wantBuffer: "/hook-reload "},
		{input: "/eff", wantBuffer: "/effort "},
		{input: "/progress_", wantBuffer: "/progress-display "},
		{input: "/codex-a", wantBuffer: "/codex-auth "},
		{input: "/codex-l", wantBuffer: "/codex-login "},
		{input: "/completion-a", wantBuffer: "/completion-audit "},
		{input: "/recov", wantBuffer: "/recover "},
		{input: "/progress-d", wantBuffer: "/progress-display "},
	}

	for _, tc := range cases {
		gotBuffer, _, ok := rt.completeSlashCommand(tc.input)
		if !ok {
			t.Fatalf("%q: expected completion to apply", tc.input)
		}
		if gotBuffer != tc.wantBuffer {
			t.Fatalf("%q: unexpected buffer: got %q want %q", tc.input, gotBuffer, tc.wantBuffer)
		}
	}
}

func TestCompleteSlashSubcommandFuzzFuncAtPathListsWorkspaceCandidates(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "include"), 0o755); err != nil {
		t.Fatalf("mkdir include: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "driver.cpp"), []byte(""), 0o644); err != nil {
		t.Fatalf("write driver.cpp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "guard.cpp"), []byte(""), 0o644); err != nil {
		t.Fatalf("write guard.cpp: %v", err)
	}

	rt := &runtimeState{
		cfg: DefaultConfig(dir),
		workspace: Workspace{
			BaseRoot: dir,
			Root:     dir,
		},
	}

	cases := []struct {
		input       string
		wantSuggest []string
	}{
		{
			input:       "/fuzz-func @",
			wantSuggest: []string{"/fuzz-func @include/", "/fuzz-func @src/"},
		},
		{
			input:       "/fuzz-func ValidateRequest @src/",
			wantSuggest: []string{"/fuzz-func ValidateRequest @src/driver.cpp", "/fuzz-func ValidateRequest @src/guard.cpp"},
		},
	}

	for _, tc := range cases {
		gotBuffer, gotSuggest, ok := rt.completeSlashCommand(tc.input)
		if !ok {
			t.Fatalf("%q: expected completion to apply", tc.input)
		}
		if gotBuffer != tc.input {
			t.Fatalf("%q: unexpected buffer: got %q want %q", tc.input, gotBuffer, tc.input)
		}
		if len(gotSuggest) != len(tc.wantSuggest) {
			t.Fatalf("%q: unexpected suggestion count: got %#v want %#v", tc.input, gotSuggest, tc.wantSuggest)
		}
		for i := range tc.wantSuggest {
			if gotSuggest[i] != tc.wantSuggest[i] {
				t.Fatalf("%q: unexpected suggestion[%d]: got %q want %q", tc.input, i, gotSuggest[i], tc.wantSuggest[i])
			}
		}
		for _, suggestion := range gotSuggest {
			if suggestion == "/fuzz-func @<path>" || suggestion == "/fuzz-func ValidateRequest @<path>" {
				t.Fatalf("%q: placeholder suggestion leaked into @ completion: %#v", tc.input, gotSuggest)
			}
		}
	}
}

func TestCompleteSlashSubcommandDynamicIdentifiers(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)

	store := NewSessionStore(dir)
	if err := store.Save(&Session{
		ID:         "session-abc",
		Name:       "Recent Session",
		WorkingDir: dir,
		Provider:   "openai",
		Model:      "gpt-5.4",
		CreatedAt:  now,
		UpdatedAt:  now,
		Messages:   []Message{},
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	evidence := &EvidenceStore{Path: dir + "\\evidence.json"}
	if err := evidence.Append(EvidenceRecord{
		ID:        "evidence-abc",
		Workspace: dir,
		CreatedAt: now,
		Kind:      "verification",
		Subject:   "subject",
	}); err != nil {
		t.Fatalf("append evidence: %v", err)
	}

	longMem := &PersistentMemoryStore{Path: dir + "\\memory.json"}
	if err := longMem.Append(PersistentMemoryRecord{
		ID:          "mem-abc",
		SessionID:   "session-abc",
		SessionName: "Recent Session",
		Workspace:   dir,
		CreatedAt:   now,
		Request:     "request",
		Reply:       "reply",
		Summary:     "summary",
	}); err != nil {
		t.Fatalf("append memory: %v", err)
	}

	investigations := &InvestigationStore{Path: dir + "\\investigations.json"}
	if _, err := investigations.Append(InvestigationRecord{
		ID:        "invest-abc",
		Workspace: dir,
		Preset:    "driver-visibility",
		Status:    InvestigationCompleted,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("append investigation: %v", err)
	}

	simulations := &SimulationStore{Path: dir + "\\simulations.json"}
	if _, err := simulations.Append(SimulationResult{
		ID:        "sim-abc",
		Workspace: dir,
		Profile:   "tamper-surface",
		CreatedAt: now,
		Summary:   "summary",
	}); err != nil {
		t.Fatalf("append simulation: %v", err)
	}
	functionFuzz := &FunctionFuzzStore{Path: dir + "\\function_fuzz.json"}
	if _, err := functionFuzz.Append(FunctionFuzzRun{
		ID:               "fuzz-abc",
		Workspace:        dir,
		TargetQuery:      "ValidateRequest",
		TargetSymbolID:   "func:ValidateRequest",
		TargetSymbolName: "ValidateRequest",
		CreatedAt:        now,
		PrimaryEngine:    "libFuzzer + ASan/UBSan",
		Summary:          "summary",
	}); err != nil {
		t.Fatalf("append function fuzz: %v", err)
	}

	fuzzCampaigns := &FuzzCampaignStore{Path: dir + "\\fuzz_campaigns.json"}
	if _, err := fuzzCampaigns.Append(FuzzCampaign{
		ID:        "campaign-abc",
		Workspace: dir,
		Name:      "campaign",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("append fuzz campaign: %v", err)
	}

	featureStore := NewFeatureStore(dir)
	feature, err := featureStore.Create(dir, "add tracked feature workflow", "openai / gpt-5.4", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}

	rt := &runtimeState{
		store:          store,
		evidence:       evidence,
		longMem:        longMem,
		investigations: investigations,
		simulations:    simulations,
		functionFuzz:   functionFuzz,
		fuzzCampaigns:  fuzzCampaigns,
		workspace: Workspace{
			BaseRoot: dir,
			Root:     dir,
		},
	}

	cases := []struct {
		input      string
		wantBuffer string
	}{
		{input: "/resume sess", wantBuffer: "/resume session-abc "},
		{input: "/evidence-show evid", wantBuffer: "/evidence-show evidence-abc "},
		{input: "/mem-show mem", wantBuffer: "/mem-show mem-abc "},
		{input: "/mem-promote mem", wantBuffer: "/mem-promote mem-abc "},
		{input: "/investigate show inv", wantBuffer: "/investigate show invest-abc "},
		{input: "/simulate show sim", wantBuffer: "/simulate show sim-abc "},
		{input: "/fuzz-func show fu", wantBuffer: "/fuzz-func show fuzz-abc "},
		{input: "/fuzz-campaign show camp", wantBuffer: "/fuzz-campaign show campaign-abc "},
		{input: "/new-feature status " + feature.ID[:8], wantBuffer: "/new-feature status " + feature.ID + " "},
	}

	for _, tc := range cases {
		gotBuffer, _, ok := rt.completeSlashCommand(tc.input)
		if !ok {
			t.Fatalf("%q: expected completion to apply", tc.input)
		}
		if gotBuffer != tc.wantBuffer {
			t.Fatalf("%q: unexpected buffer: got %q want %q", tc.input, gotBuffer, tc.wantBuffer)
		}
	}
}

func TestCommandCompletionDescriptionCoversCommandsAndSubcommands(t *testing.T) {
	cases := map[string]string{
		"/status":                                                "Show current session state, approvals, and extension status.",
		"/effort high":                                           "Favor deeper reasoning.",
		"/provider status":                                       "Show the current provider, base URL, key state, and billing visibility.",
		"/codex-auth login":                                      "Start device OAuth login for OpenAI Codex.",
		"/verify":                                                "Run verification and suggest the next repair, dashboard, checkpoint, or feature workflow step.",
		"/specialists":                                           "Show task ownership profiles plus editable ownership and worktree routing state.",
		"/specialists cleanup":                                   "Remove one or all task-owner worktrees recorded for this session.",
		"/worktree cleanup":                                      "Remove the recorded isolated worktree after it is clean.",
		"/new-feature status":                                    "Show the current state of a tracked feature.",
		"/analyze-project":                                       "Run project analysis and suggest the next dashboard, fuzzing, or verification step.",
		"/analyze-project --path":                                "Limit analysis to one workspace directory or file path; a goal is optional.",
		"/analyze-project --mode map":                            "Build the default architecture map: subsystems, ownership, module boundaries, entry points, docs, dashboard, and reusable knowledge base.",
		"/analyze-project --mode trace":                          "Follow one runtime or request flow through callers, callees, dispatch points, ownership transitions, and source anchors.",
		"/analyze-project --mode impact":                         "Estimate change blast radius: upstream/downstream dependencies, affected files, retest targets, and stale documentation risks.",
		"/analyze-project --mode surface":                        "Inventory exposed entry surfaces: IOCTL, RPC, parsers, handles, memory-copy paths, telemetry decoders, network inputs, and fuzz targets.",
		"/analyze-project --mode security":                       "Analyze trust boundaries, validation, privileged paths, tamper-sensitive state, enforcement points, and driver/IOCTL/handle/RPC risks.",
		"/analyze-project --mode performance":                    "Map performance risk: startup cost, hot paths, blocking chains, allocation/copy pressure, contention, and profiling order.",
		"/analyze-project --path src/ --mode map":                "Build the default architecture map: subsystems, ownership, module boundaries, entry points, docs, dashboard, and reusable knowledge base.",
		"/analyze-project surface":                               "Focus on concrete IOCTL, RPC, parser, handle, memory, and network surfaces.",
		"/analyze-dashboard":                                     "Open the latest project analysis document portal with search, graph-linked stale diff, trust/data graphs, attack flows, and drilldowns.",
		"/analyze-dashboard latest":                              "Open the latest analyze-project document portal.",
		"/docs-refresh":                                          "Regenerate latest project analysis docs, graph section stale markers, schema manifest, dashboard, and vector corpus from saved artifacts.",
		"/set-specialist-model planner":                          "Configure an optional provider/model override for the planner task owner profile.",
		"/simulate tamper-surface":                               "Model obvious tamper vectors and exposed surfaces.",
		"/fuzz-func":                                             "Auto-plan directed function fuzzing and suggest the campaign handoff when source-only scenarios are ready.",
		"/fuzz-func <function-name>":                             "Target one function by name and let Kernforge resolve the best matching symbol automatically.",
		"/fuzz-func <function-name> --file <path>":               "Target one function by name and pin matching to a specific source file when names collide.",
		"/fuzz-func <function-name> @<path>":                     "Target one function by name and use @<path> as a short file-hint alias.",
		"/fuzz-func <function-name> --source-scan focused":       "Reuse a matching source candidate or run a target-scoped source scan while planning /fuzz-func.",
		"/fuzz-func <function-name> --source-scan full":          "Run workspace-wide source matchers during /fuzz-func planning before linking the best matching candidate.",
		"/fuzz-func <function-name> --no-source-scan":            "Plan /fuzz-func without source-scan candidate reuse or automatic source matcher execution.",
		"/fuzz-func --file <path>":                               "Analyze one file plus the files it includes or imports, then let Kernforge choose the best starting function automatically.",
		"/fuzz-func @<path>":                                     "Analyze one file plus the files it includes or imports, then let Kernforge choose the best starting function automatically.",
		"/fuzz-func language":                                    "Show or change /fuzz-func output language. Use system to follow the PC language or english to force English.",
		"/fuzz-func show":                                        "Open one saved function fuzz plan by id.",
		"/fuzz-func continue":                                    "Approve a pending recovered build configuration. Optional --profile smoke|extended|repro|minimize switches the run profile.",
		"/fuzz-func continue --profile extended":                 "Approve a pending recovered build configuration. Optional --profile smoke|extended|repro|minimize switches the run profile.",
		"/fuzz-func repro <crash>":                               "Re-run a saved fuzz plan in repro profile against a specific crash input (single-shot replay).",
		"/fuzz-func minimize <crash>":                            "Re-run a saved fuzz plan with libFuzzer crash minimization (-minimize_crash=1) against a specific crash input.",
		"/fuzz-campaign":                                         "Show the fuzz campaign planner and the one command Kernforge recommends next, including deduplicated finding gates plus parsed coverage and sanitizer/verifier artifact feedback.",
		"/source-scan":                                           "Run source matchers for kernel, C++, Unreal, and telemetry surfaces, then hand a candidate to /fuzz-func.",
		"/create-driver-poc":                                     "Generate a buildable x64 C++20 MSVC driver POC; add --type objectfilter|minifilter|registryfilter|wfpcallout for specialized templates.",
		"/create-driver-poc <driver-name>":                       "Create <driver-name>.sln, Driver.cpp-based <driver-name>.sys, and <driver-name>-tester.exe projects under a new workspace folder.",
		"/create-driver-poc <driver-name> --type objectfilter":   "Generate an object manager process/thread handle filter POC that strips dangerous requested access.",
		"/create-driver-poc <driver-name> --type minifilter":     "Generate a filesystem minifilter POC with user-mode decision messaging over a Filter Manager port.",
		"/create-driver-poc <driver-name> --type registryfilter": "Generate a registry callback POC that blocks access to a registered registry path.",
		"/create-driver-poc <driver-name> --type wfpcallout":     "Generate a WFP outbound callout POC that blocks traffic to a registered IPv4 target.",
		"/source-scan run":                                       "Run all enabled source matchers and persist ranked candidate records.",
		"/source-scan run --limit 50":                            "Cap the scan to the top ranked candidates before writing source-scan artifacts.",
		"/source-scan run --only-slugs probe-copy-size-drift,ioctl-dispatch-selector": "Run only the listed matcher slugs for a focused scan of specific bug-pattern families.",
		"/source-scan run --files driver/nsi.c,api/registry.c":                        "Restrict the scan to the listed comma-separated source files.",
		"/source-scan show":                 "Show one source-scan candidate by id or latest, including evidence and the exact /fuzz-func handoff.",
		"/source-scan revalidate":           "Attach source-only or native verifier feedback to one candidate and update its lifecycle state.",
		"/evidence":                         "Review evidence records and suggest verification, dashboard, or source follow-up.",
		"/mem":                              "Show persistent memory and suggest confirm, promote, verify, or dashboard follow-up.",
		"/checkpoint":                       "Create a rollback checkpoint and suggest diff or checkpoint-list follow-up.",
		"/worktree":                         "Create, inspect, detach, or clean isolated git worktrees with tracked-feature follow-up.",
		"/events":                           "Tail or export session events as JSONL for local dashboards, schedulers, and app-server style clients.",
		"/completion-audit":                 "Write a completion readiness audit with blockers, warnings, verification, tasks, jobs, and artifact evidence.",
		"/recover":                          "Write a failure recovery brief with recent errors, verification failure, jobs, actions, and next commands.",
		"/review change":                    "Review the current workspace diff, patch transaction, or supplied diff/code.",
		"/review plan":                      "Review an implementation plan or architecture proposal as a typed ReviewRun.",
		"/model":                            "Show or change main, analysis, cross-review, and task-owner model routing.",
		"/model cross-review":               "Configure the optional independent second-pass reviewer route.",
		"/model cross-review status":        "Show common review routes, lenses, automation, and route health.",
		"/model clear cross-review":         "Clear the optional independent second-pass reviewer route.",
		"/review --no-model":                "Run deterministic reviewers only and still write review artifacts.",
		"/review --mode":                    "Force review mode such as security-hardening, core-build, live-fix, refactor, research, or ui-polish.",
		"/review --mode security-hardening": "Use security, kernel, bypass, and false-positive review policy.",
	}

	for item, want := range cases {
		if got := commandCompletionDescription(item); got != want {
			t.Fatalf("%q: got %q want %q", item, got, want)
		}
	}
}

func TestSourceScanRunCompletionDescriptionsAreDistinct(t *testing.T) {
	items := []string{
		"/source-scan run",
		"/source-scan run --limit 50",
		"/source-scan run --only-slugs probe-copy-size-drift,ioctl-dispatch-selector",
		"/source-scan run --files driver/nsi.c,api/registry.c",
	}
	seen := map[string]string{}
	for _, item := range items {
		description := commandCompletionDescription(item)
		if strings.TrimSpace(description) == "" {
			t.Fatalf("%q: expected a source-scan completion description", item)
		}
		if previous, ok := seen[description]; ok {
			t.Fatalf("%q and %q share completion description %q", previous, item, description)
		}
		seen[description] = item
	}
}

func TestCompletionSuggestionsHaveSpecificDescriptions(t *testing.T) {
	rt := &runtimeState{
		cfg: DefaultConfig(t.TempDir()),
	}
	inputs := []string{
		"/permissions ",
		"/checkpoint-auto o",
		"/locale-auto o",
		"/set-auto-verify o",
		"/worktree ",
		"/specialists ",
		"/effort ",
		"/provider ",
		"/codex-auth ",
		"/profile ",
		"/model ",
		"/model cross-review ",
		"/review ",
		"/review --mode ",
		"/analyze-project --",
		"/analyze-project --mode ",
		"/set-analysis-models ",
		"/set-specialist-model ",
		"/new-feature ",
		"/investigate ",
		"/simulate ",
		"/fuzz-func ",
		"/fuzz-func language ",
		"/fuzz-campaign ",
		"/source-scan ",
		"/create-driver-poc ",
		"/init ",
	}

	for _, input := range inputs {
		_, suggestions, ok := rt.completeSlashCommand(input)
		if !ok {
			t.Fatalf("%q: expected completion suggestions", input)
		}
		if len(suggestions) == 0 {
			t.Fatalf("%q: expected at least one suggestion", input)
		}
		for _, suggestion := range suggestions {
			got := commandCompletionDescription(suggestion)
			if strings.TrimSpace(got) == "" {
				t.Fatalf("%q: missing description for suggestion %q", input, suggestion)
			}
			commandName := strings.Fields(strings.TrimPrefix(suggestion, "/"))[0]
			if got == slashCommandDescriptions[commandName] && strings.Contains(strings.TrimPrefix(suggestion, "/"), " ") {
				t.Fatalf("%q: suggestion %q fell back to parent command description %q", input, suggestion, got)
			}
		}
	}
}
