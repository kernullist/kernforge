package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

type EditRoutingRequest struct {
	Path        string
	OwnerNodeID string
	ForLookup   bool
}

type EditRoutingResult struct {
	AbsolutePath   string
	DisplayRoot    string
	OwnerNodeID    string
	Specialist     string
	WorktreeRoot   string
	OwnershipPaths []string
}

type ShellRoutingResult struct {
	Root         string
	OwnerNodeID  string
	Specialist   string
	WorktreeRoot string
}

func (r EditRoutingResult) DisplayPath() string {
	root := firstNonBlankString(r.DisplayRoot, r.WorktreeRoot)
	if root == "" {
		return r.AbsolutePath
	}
	return relOrAbs(root, r.AbsolutePath)
}

func specialistOwnershipPaths(profile SpecialistSubagentProfile, override []string) []string {
	patterns := normalizeTaskStateList(override, 32)
	if len(patterns) > 0 {
		return patterns
	}
	patterns = normalizeTaskStateList(profile.OwnershipPaths, 32)
	if len(patterns) > 0 {
		return patterns
	}
	if specialistProfileEditable(profile) {
		return []string{"**"}
	}
	return nil
}

func (rt *runtimeState) resolveOwnerNodeID(ownerNodeID string) string {
	trimmed := strings.TrimSpace(ownerNodeID)
	if trimmed != "" {
		return trimmed
	}
	if rt == nil || rt.session == nil || rt.session.TaskState == nil {
		return ""
	}
	return strings.TrimSpace(rt.session.TaskState.ExecutorFocusNode)
}

func (rt *runtimeState) recordEditableAssignment(nodeID string, assignment SpecialistAssignment, ownership []string, lease SpecialistWorktree) {
	if rt == nil || rt.session == nil {
		return
	}
	rt.session.RecordTaskGraphEditableAssignment(
		nodeID,
		assignment.Profile.Name,
		firstNonBlankString(assignment.Reason, "editable"),
		specialistOwnershipPaths(assignment.Profile, ownership),
		lease.Root,
		lease.Branch,
	)
}

func (rt *runtimeState) refreshEditableLease(node TaskNode, assignment SpecialistAssignment) TaskNode {
	if rt == nil || rt.session == nil || rt.session.TaskGraph == nil {
		return node
	}
	leasePaths, leaseReason := deriveEditableLeasePaths(rt.session.TaskGraph, node, assignment.Profile)
	if len(leasePaths) == 0 {
		return node
	}
	if !slices.Equal(node.EditableLeasePaths, leasePaths) || !strings.EqualFold(strings.TrimSpace(node.EditableLeaseReason), strings.TrimSpace(leaseReason)) {
		rt.session.RecordTaskGraphEditableLease(node.ID, leasePaths, leaseReason)
		if rt.store != nil {
			_ = rt.store.Save(rt.session)
		}
		if updated, ok := rt.session.TaskGraph.Node(node.ID); ok {
			return updated
		}
	}
	return node
}

func (rt *runtimeState) clearEditableWorktreeMetadataForSpecialist(name string) {
	if rt == nil || rt.session == nil || rt.session.TaskGraph == nil {
		return
	}
	target := normalizeSpecialistProfileName(name)
	if target == "" {
		return
	}
	for index := range rt.session.TaskGraph.Nodes {
		if normalizeSpecialistProfileName(rt.session.TaskGraph.Nodes[index].EditableSpecialist) != target {
			continue
		}
		rt.session.TaskGraph.Nodes[index].EditableWorktreeRoot = ""
		rt.session.TaskGraph.Nodes[index].EditableWorktreeBranch = ""
		rt.session.TaskGraph.Nodes[index].LastUpdated = time.Now()
	}
	rt.session.TaskGraph.Touch()
}

func (rt *runtimeState) resolveEditableSpecialistAssignment(nodeID string) (TaskNode, SpecialistAssignment, bool) {
	if rt == nil || rt.session == nil || rt.session.TaskGraph == nil {
		return TaskNode{}, SpecialistAssignment{}, false
	}
	node, ok := rt.session.TaskGraph.Node(nodeID)
	if !ok {
		return TaskNode{}, SpecialistAssignment{}, false
	}
	if profile, ok := configuredSpecialistProfileByName(rt.cfg, node.EditableSpecialist); ok && specialistProfileEditable(profile) {
		return node, SpecialistAssignment{
			Profile: profile,
			Reason:  firstNonBlankString(node.EditableReason, "task-graph"),
			Score:   1,
		}, true
	}
	assignment, ok := selectEditableSpecialistForTaskNode(rt.cfg, node, rt.session.TaskState, "executor")
	if !ok {
		return node, SpecialistAssignment{}, false
	}
	ownership := specialistOwnershipPaths(assignment.Profile, node.EditableOwnershipPaths)
	rt.recordEditableAssignment(node.ID, assignment, ownership, SpecialistWorktree{})
	if rt.store != nil {
		_ = rt.store.Save(rt.session)
	}
	updated, ok := rt.session.TaskGraph.Node(node.ID)
	if ok {
		node = updated
	}
	node = rt.refreshEditableLease(node, assignment)
	return node, assignment, true
}

func (rt *runtimeState) ensureSpecialistWorktreeLease(nodeID string, assignment SpecialistAssignment, ownership []string, autoCreated bool, forceCreate bool) (SpecialistWorktree, error) {
	if rt == nil || rt.session == nil {
		return SpecialistWorktree{}, fmt.Errorf("session is not initialized")
	}
	specialist := normalizeSpecialistProfileName(assignment.Profile.Name)
	if specialist == "" {
		return SpecialistWorktree{}, fmt.Errorf("editable specialist is required")
	}
	patterns := specialistOwnershipPaths(assignment.Profile, ownership)
	if existing, ok := rt.session.SpecialistWorktree(specialist); ok {
		existing.OwnershipPaths = normalizeTaskStateList(append(existing.OwnershipPaths, patterns...), 32)
		existing.NodeIDs = normalizeTaskStateList(append(existing.NodeIDs, nodeID), 16)
		existing.LastOwnerNodeID = strings.TrimSpace(nodeID)
		existing.UpdatedAt = time.Now()
		rt.session.UpsertSpecialistWorktree(existing)
		rt.recordEditableAssignment(nodeID, assignment, patterns, existing)
		if rt.store != nil {
			_ = rt.store.Save(rt.session)
		}
		return existing, nil
	}
	if !forceCreate && !configWorktreeIsolationEnabled(rt.cfg) {
		rt.recordEditableAssignment(nodeID, assignment, patterns, SpecialistWorktree{})
		return SpecialistWorktree{
			Specialist:      specialist,
			OwnershipPaths:  patterns,
			NodeIDs:         normalizeTaskStateList([]string{nodeID}, 16),
			LastOwnerNodeID: strings.TrimSpace(nodeID),
			AutoCreated:     autoCreated,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}, nil
	}
	manager := newWorktreeManager(rt.cfg)
	requestedName := firstNonBlankString(strings.TrimSpace(nodeID)+"-"+specialist, specialist)
	record, err := manager.Create(context.Background(), sessionBaseWorkingDir(rt.session), requestedName)
	if err != nil {
		return SpecialistWorktree{}, err
	}
	lease := SpecialistWorktree{
		Specialist:      specialist,
		Root:            record.Root,
		Branch:          record.Branch,
		OwnershipPaths:  patterns,
		NodeIDs:         normalizeTaskStateList([]string{nodeID}, 16),
		Managed:         record.Managed,
		AutoCreated:     autoCreated,
		LastOwnerNodeID: strings.TrimSpace(nodeID),
		CreatedAt:       firstNonZeroTime(record.CreatedAt, time.Now()),
		UpdatedAt:       time.Now(),
	}
	lease.Normalize()
	rt.session.UpsertSpecialistWorktree(lease)
	rt.recordEditableAssignment(nodeID, assignment, patterns, lease)
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return SpecialistWorktree{}, err
		}
	}
	return lease, nil
}

func (rt *runtimeState) resolveEditTarget(req EditRoutingRequest) (EditRoutingResult, error) {
	result, err := rt.workspace.resolveEditFallback(req)
	if err != nil {
		return EditRoutingResult{}, err
	}
	ownerNodeID := rt.resolveOwnerNodeID(req.OwnerNodeID)
	if ownerNodeID == "" {
		return result, nil
	}
	node, assignment, ok := rt.resolveEditableSpecialistAssignment(ownerNodeID)
	if !ok {
		result.OwnerNodeID = ownerNodeID
		return result, nil
	}
	leasePaths := effectiveEditableLeasePaths(node, assignment.Profile)
	lease, err := rt.ensureSpecialistWorktreeLease(node.ID, assignment, leasePaths, true, false)
	if err != nil {
		return EditRoutingResult{}, err
	}
	if strings.TrimSpace(lease.Root) == "" {
		result.OwnerNodeID = node.ID
		result.Specialist = assignment.Profile.Name
		result.OwnershipPaths = leasePaths
		ownershipRoot := firstNonBlankString(result.DisplayRoot, rt.workspace.Root, rt.workspace.BaseRoot)
		if err := enforceEditableOwnership(ownershipRoot, result.AbsolutePath, assignment.Profile.Name, result.OwnershipPaths); err != nil {
			return EditRoutingResult{}, err
		}
		return result, nil
	}
	absolutePath, err := rt.workspace.resolveAgainstRoot(lease.Root, req.Path)
	if err != nil {
		return EditRoutingResult{}, err
	}
	result = EditRoutingResult{
		AbsolutePath:   absolutePath,
		DisplayRoot:    lease.Root,
		OwnerNodeID:    node.ID,
		Specialist:     assignment.Profile.Name,
		WorktreeRoot:   lease.Root,
		OwnershipPaths: leasePaths,
	}
	if err := enforceEditableOwnership(lease.Root, result.AbsolutePath, assignment.Profile.Name, result.OwnershipPaths); err != nil {
		return EditRoutingResult{}, err
	}
	return result, nil
}

func (rt *runtimeState) resolveShellRoot(ownerNodeID string) (ShellRoutingResult, error) {
	root := firstNonBlankString(rt.workspace.Root, rt.workspace.BaseRoot)
	result := ShellRoutingResult{
		Root:        root,
		OwnerNodeID: rt.resolveOwnerNodeID(ownerNodeID),
	}
	if strings.TrimSpace(result.OwnerNodeID) == "" {
		return result, nil
	}
	node, assignment, ok := rt.resolveEditableSpecialistAssignment(result.OwnerNodeID)
	if !ok {
		return result, nil
	}
	lease, err := rt.ensureSpecialistWorktreeLease(node.ID, assignment, effectiveEditableLeasePaths(node, assignment.Profile), true, false)
	if err != nil {
		return ShellRoutingResult{}, err
	}
	if strings.TrimSpace(lease.Root) == "" {
		result.OwnerNodeID = node.ID
		result.Specialist = assignment.Profile.Name
		return result, nil
	}
	return ShellRoutingResult{
		Root:         lease.Root,
		OwnerNodeID:  node.ID,
		Specialist:   assignment.Profile.Name,
		WorktreeRoot: lease.Root,
	}, nil
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
