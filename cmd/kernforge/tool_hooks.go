package main

func (t ListFilesTool) hookWorkspace() Workspace { return t.ws }

func (t *ReadFileTool) hookWorkspace() Workspace {
	if t == nil {
		return Workspace{}
	}
	return t.ws
}

func (t *GrepTool) hookWorkspace() Workspace {
	if t == nil {
		return Workspace{}
	}
	return t.ws
}

func (t WriteFileTool) hookWorkspace() Workspace { return t.ws }

func (t ReplaceInFileTool) hookWorkspace() Workspace { return t.ws }

func (t RunShellTool) hookWorkspace() Workspace { return t.ws }

func (t RunShellTool) managesDefaultToolUseHooks() bool { return true }

func (t GitStatusTool) hookWorkspace() Workspace { return t.ws }

func (t GitAddTool) hookWorkspace() Workspace { return t.ws }

func (t GitCommitTool) hookWorkspace() Workspace { return t.ws }

func (t GitPushTool) hookWorkspace() Workspace { return t.ws }

func (t GitPushTool) managesDefaultToolUseHooks() bool { return true }

func (t GitCreatePRTool) hookWorkspace() Workspace { return t.ws }

func (t GitCreatePRTool) managesDefaultToolUseHooks() bool { return true }

func (t GitDiffTool) hookWorkspace() Workspace { return t.ws }

func (t UpdatePlanTool) hookWorkspace() Workspace { return t.ws }

func (t ApplyEditProposalTool) hookWorkspace() Workspace { return t.ws }

func (t ApplyPatchTool) hookWorkspace() Workspace { return t.ws }

func (t ApplyPatchTool) managesDefaultToolUseHooks() bool { return true }

func (t RunBackgroundShellTool) hookWorkspace() Workspace { return t.ws }

func (t RunBackgroundShellTool) managesDefaultToolUseHooks() bool { return true }

func (t RunShellBundleBackgroundTool) hookWorkspace() Workspace { return t.ws }

func (t RunShellBundleBackgroundTool) managesDefaultToolUseHooks() bool { return true }

func (t CheckShellJobTool) hookWorkspace() Workspace { return t.ws }

func (t CheckShellBundleTool) hookWorkspace() Workspace { return t.ws }

func (t CancelShellJobTool) hookWorkspace() Workspace { return t.ws }

func (t CancelShellBundleTool) hookWorkspace() Workspace { return t.ws }

func (t GetGoalTool) hookWorkspace() Workspace { return t.ws }

func (t CreateGoalTool) hookWorkspace() Workspace { return t.ws }

func (t UpdateGoalTool) hookWorkspace() Workspace { return t.ws }

func (t MCPTool) hookWorkspace() Workspace { return t.workspace }

func (t MCPTool) managesDefaultToolUseHooks() bool { return true }

func (t ViewImageTool) hookWorkspace() Workspace { return t.ws }
