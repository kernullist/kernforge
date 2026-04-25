package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type SessionWorktree struct {
	ID        string    `json:"id"`
	Root      string    `json:"root"`
	Branch    string    `json:"branch,omitempty"`
	Managed   bool      `json:"managed,omitempty"`
	Active    bool      `json:"active,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type SpecialistWorktree struct {
	Specialist      string    `json:"specialist"`
	Root            string    `json:"root"`
	Branch          string    `json:"branch,omitempty"`
	OwnershipPaths  []string  `json:"ownership_paths,omitempty"`
	NodeIDs         []string  `json:"node_ids,omitempty"`
	Managed         bool      `json:"managed,omitempty"`
	AutoCreated     bool      `json:"auto_created,omitempty"`
	LastOwnerNodeID string    `json:"last_owner_node_id,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type WorktreeManager struct {
	RootDir      string
	BranchPrefix string
}

var worktreeSlugPattern = regexp.MustCompile(`[^a-z0-9._-]+`)

func (w *SessionWorktree) Normalize() {
	if w == nil {
		return
	}
	w.ID = strings.TrimSpace(w.ID)
	w.Root = strings.TrimSpace(w.Root)
	w.Branch = strings.TrimSpace(w.Branch)
}

func (w *SpecialistWorktree) Normalize() {
	if w == nil {
		return
	}
	w.Specialist = normalizeSpecialistProfileName(w.Specialist)
	w.Root = strings.TrimSpace(w.Root)
	w.Branch = strings.TrimSpace(w.Branch)
	w.OwnershipPaths = normalizeTaskStateList(w.OwnershipPaths, 32)
	w.NodeIDs = normalizeTaskStateList(w.NodeIDs, 16)
	w.LastOwnerNodeID = strings.TrimSpace(w.LastOwnerNodeID)
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now()
	}
	if w.UpdatedAt.IsZero() {
		w.UpdatedAt = w.CreatedAt
	}
}

func sessionBaseWorkingDir(sess *Session) string {
	if sess == nil {
		return ""
	}
	base := strings.TrimSpace(sess.BaseWorkingDir)
	if base != "" {
		return base
	}
	return strings.TrimSpace(sess.WorkingDir)
}

func sessionHasActiveWorktree(sess *Session) bool {
	if sess == nil || sess.Worktree == nil {
		return false
	}
	sess.Worktree.Normalize()
	return sess.Worktree.Active && strings.TrimSpace(sess.Worktree.Root) != ""
}

func sanitizeWorktreeSlug(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return "session"
	}
	lower = strings.ReplaceAll(lower, string(filepath.Separator), "-")
	lower = strings.ReplaceAll(lower, "/", "-")
	lower = worktreeSlugPattern.ReplaceAllString(lower, "-")
	lower = strings.Trim(lower, "-.")
	lower = strings.Join(strings.FieldsFunc(lower, func(r rune) bool {
		return r == '-'
	}), "-")
	if lower == "" {
		return "session"
	}
	return lower
}

func sanitizeBranchPrefix(prefix string) string {
	trimmed := strings.TrimSpace(prefix)
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	trimmed = strings.Trim(trimmed, "/")
	return trimmed
}

func gitRepositoryRoot(ctx context.Context, dir string) (string, error) {
	out, err := runGitCommand(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Clean(firstNonBlankString(strings.TrimSpace(out))), nil
}

func newWorktreeManager(cfg Config) WorktreeManager {
	return WorktreeManager{
		RootDir:      configWorktreeIsolationRootDir(cfg),
		BranchPrefix: configWorktreeIsolationBranchPrefix(cfg),
	}
}

func (m WorktreeManager) Create(ctx context.Context, baseRoot string, requestedName string) (SessionWorktree, error) {
	repoRoot, err := gitRepositoryRoot(ctx, baseRoot)
	if err != nil {
		return SessionWorktree{}, fmt.Errorf("worktree isolation requires a git repository: %w", err)
	}
	if strings.TrimSpace(m.RootDir) == "" {
		return SessionWorktree{}, fmt.Errorf("worktree isolation root directory is not configured")
	}
	slugBase := sanitizeWorktreeSlug(firstNonBlankString(requestedName, filepath.Base(repoRoot)))
	timestamp := time.Now().Format("20060102-150405")
	token := slugBase + "-" + timestamp
	repoName := sanitizeWorktreeSlug(filepath.Base(repoRoot))
	targetRoot := filepath.Join(m.RootDir, repoName, token)
	if _, err := os.Stat(targetRoot); err == nil {
		return SessionWorktree{}, fmt.Errorf("worktree path already exists: %s", targetRoot)
	} else if err != nil && !os.IsNotExist(err) {
		return SessionWorktree{}, err
	}
	if err := os.MkdirAll(filepath.Dir(targetRoot), 0o755); err != nil {
		return SessionWorktree{}, err
	}
	branch := token
	if prefix := sanitizeBranchPrefix(m.BranchPrefix); prefix != "" {
		branch = prefix + "/" + token
	}
	if _, err := runGitCommand(ctx, repoRoot, "worktree", "add", "-b", branch, targetRoot); err != nil {
		return SessionWorktree{}, err
	}
	return SessionWorktree{
		ID:        token,
		Root:      filepath.Clean(targetRoot),
		Branch:    branch,
		Managed:   true,
		Active:    true,
		CreatedAt: time.Now(),
	}, nil
}

func (m WorktreeManager) Remove(ctx context.Context, baseRoot string, worktree SessionWorktree) error {
	worktree.Normalize()
	if strings.TrimSpace(worktree.Root) == "" {
		return nil
	}
	if _, err := os.Stat(worktree.Root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	changed, err := gitChangedFiles(ctx, worktree.Root)
	if err != nil {
		return err
	}
	if len(changed) > 0 {
		return fmt.Errorf("worktree has uncommitted changes; commit or discard them before cleanup")
	}
	repoRoot, err := gitRepositoryRoot(ctx, baseRoot)
	if err != nil {
		return err
	}
	_, err = runGitCommand(ctx, repoRoot, "worktree", "remove", worktree.Root)
	return err
}

func (rt *runtimeState) syncWorkspaceFromSession() {
	if rt == nil || rt.session == nil {
		return
	}
	rt.session.normalizeWorkingDirState()
	baseRoot := sessionBaseWorkingDir(rt.session)
	activeRoot := strings.TrimSpace(rt.session.WorkingDir)
	if activeRoot == "" {
		activeRoot = baseRoot
		rt.session.WorkingDir = activeRoot
	}
	rt.workspace.BaseRoot = baseRoot
	rt.workspace.Root = activeRoot
	rt.workspace.Shell = rt.cfg.Shell
	rt.workspace.ShellTimeout = configShellTimeout(rt.cfg)
	rt.workspace.ReadHintSpans = configReadHintSpans(rt.cfg)
	rt.workspace.ReadCacheEntries = configReadCacheEntries(rt.cfg)
	rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
	rt.workspace.PrepareEditAtRoot = rt.prepareEditAtRoot
	rt.workspace.ResolveEditTarget = rt.resolveEditTarget
	rt.workspace.ResolveShellRoot = rt.resolveShellRoot
	if rt.backgroundJobs != nil {
		rt.backgroundJobs.root = filepath.Join(baseRoot, userConfigDirName, "jobs")
		rt.backgroundJobs.session = rt.session
		rt.backgroundJobs.store = rt.store
		rt.workspace.BackgroundJobs = rt.backgroundJobs
	}
	if rt.agent != nil {
		rt.agent.Session = rt.session
		rt.agent.Workspace = rt.workspace
	}
}

func (rt *runtimeState) reloadSessionContext() error {
	if rt == nil || rt.session == nil {
		return nil
	}
	rt.syncWorkspaceFromSession()
	mem, err := LoadMemory(rt.workspace.BaseRoot, rt.cfg.MemoryFiles)
	if err != nil {
		return err
	}
	rt.memory = mem
	rt.reloadHooks()
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
		rt.agent.Memory = rt.memory
		rt.agent.Store = rt.store
		rt.agent.Session = rt.session
		rt.agent.Workspace = rt.workspace
		rt.agent.LongMem = rt.longMem
	}
	rt.reloadExtensions()
	rt.syncClientFromConfig()
	return nil
}

func (rt *runtimeState) attachWorktree(worktree SessionWorktree) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("session is not initialized")
	}
	baseRoot := sessionBaseWorkingDir(rt.session)
	if baseRoot == "" {
		baseRoot = strings.TrimSpace(rt.session.WorkingDir)
	}
	rt.session.BaseWorkingDir = baseRoot
	worktree.Active = true
	worktree.Normalize()
	rt.session.Worktree = &worktree
	rt.session.WorkingDir = worktree.Root
	if err := rt.reloadSessionContext(); err != nil {
		return err
	}
	return rt.store.Save(rt.session)
}

func (rt *runtimeState) detachWorktreeRecord() error {
	if rt == nil || rt.session == nil || rt.session.Worktree == nil {
		return nil
	}
	rt.session.Worktree.Active = false
	rt.session.WorkingDir = sessionBaseWorkingDir(rt.session)
	if err := rt.reloadSessionContext(); err != nil {
		return err
	}
	return rt.store.Save(rt.session)
}

func (rt *runtimeState) handleWorktreeCommand(args string) error {
	parts := strings.Fields(strings.TrimSpace(args))
	subcommand := "status"
	if len(parts) > 0 {
		subcommand = strings.ToLower(strings.TrimSpace(parts[0]))
	}
	switch subcommand {
	case "", "status":
		fmt.Fprintln(rt.writer, rt.ui.section("Worktree"))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("enabled", fmt.Sprintf("%t", configWorktreeIsolationEnabled(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("base_root", sessionBaseWorkingDir(rt.session)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("active_root", rt.session.WorkingDir))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("manager_root", configWorktreeIsolationRootDir(rt.cfg)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("branch_prefix", configWorktreeIsolationBranchPrefix(rt.cfg)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_for_tracked_features", fmt.Sprintf("%t", configWorktreeIsolationAutoForTrackedFeatures(rt.cfg))))
		if rt.session.Worktree != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_root", valueOrUnset(rt.session.Worktree.Root)))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_branch", valueOrUnset(rt.session.Worktree.Branch)))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_active", fmt.Sprintf("%t", rt.session.Worktree.Active)))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_managed", fmt.Sprintf("%t", rt.session.Worktree.Managed)))
		}
		return nil
	case "create":
		if rt.session.Worktree != nil {
			return fmt.Errorf("this session already has worktree metadata; use /worktree cleanup before creating another isolated worktree")
		}
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), parts[0]))
		name = strings.TrimSpace(name)
		if name == "" {
			name = firstNonBlankString(rt.session.ActiveFeatureID, rt.session.Name, filepath.Base(sessionBaseWorkingDir(rt.session)))
		}
		manager := newWorktreeManager(rt.cfg)
		worktree, err := manager.Create(context.Background(), sessionBaseWorkingDir(rt.session), name)
		if err != nil {
			return err
		}
		if err := rt.attachWorktree(worktree); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Attached isolated worktree: "+worktree.Root))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("branch", worktree.Branch))
		if handoff := worktreeHandoff("create", rt.session.ActiveFeatureID); strings.TrimSpace(handoff) != "" {
			fmt.Fprintln(rt.writer)
			fmt.Fprintln(rt.writer, handoff)
		}
		return nil
	case "leave":
		if rt.session.Worktree == nil || !rt.session.Worktree.Active {
			return fmt.Errorf("no active isolated worktree is attached to this session")
		}
		if err := rt.detachWorktreeRecord(); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Returned to base workspace root: "+sessionBaseWorkingDir(rt.session)))
		if handoff := worktreeHandoff("leave", rt.session.ActiveFeatureID); strings.TrimSpace(handoff) != "" {
			fmt.Fprintln(rt.writer)
			fmt.Fprintln(rt.writer, handoff)
		}
		return nil
	case "cleanup":
		if rt.session.Worktree == nil {
			return fmt.Errorf("no managed worktree is recorded for this session")
		}
		manager := newWorktreeManager(rt.cfg)
		record := *rt.session.Worktree
		if record.Active {
			if err := rt.detachWorktreeRecord(); err != nil {
				return err
			}
		}
		if err := manager.Remove(context.Background(), sessionBaseWorkingDir(rt.session), record); err != nil {
			_ = rt.store.Save(rt.session)
			return err
		}
		rt.session.Worktree = nil
		rt.session.WorkingDir = sessionBaseWorkingDir(rt.session)
		if err := rt.reloadSessionContext(); err != nil {
			return err
		}
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Removed isolated worktree"))
		if handoff := worktreeHandoff("cleanup", rt.session.ActiveFeatureID); strings.TrimSpace(handoff) != "" {
			fmt.Fprintln(rt.writer)
			fmt.Fprintln(rt.writer, handoff)
		}
		return nil
	default:
		return fmt.Errorf("usage: /worktree [status|create [name]|leave|cleanup]")
	}
}

func (rt *runtimeState) ensureTrackedFeatureWorktree(feature FeatureWorkflow) error {
	if rt == nil || rt.session == nil {
		return nil
	}
	if !configWorktreeIsolationEnabled(rt.cfg) || !configWorktreeIsolationAutoForTrackedFeatures(rt.cfg) {
		return nil
	}
	if sessionHasActiveWorktree(rt.session) {
		return nil
	}
	if rt.session.Worktree != nil {
		return fmt.Errorf("tracked feature worktree isolation requested, but this session already has inactive worktree metadata; run /worktree cleanup first")
	}
	manager := newWorktreeManager(rt.cfg)
	requestedName := firstNonBlankString(feature.ID, feature.Request, rt.session.ActiveFeatureID)
	worktree, err := manager.Create(context.Background(), sessionBaseWorkingDir(rt.session), requestedName)
	if err != nil {
		return fmt.Errorf("create tracked feature worktree: %w", err)
	}
	if err := rt.attachWorktree(worktree); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Tracked feature implementation is now running inside an isolated worktree."))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_root", worktree.Root))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_branch", worktree.Branch))
	return nil
}
