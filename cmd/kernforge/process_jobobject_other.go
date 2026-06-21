//go:build !windows

package main

import "os/exec"

// processContainment is the cross-platform no-op stand-in for the Windows Job
// Object based process-tree containment. On non-Windows builds kernforge relies
// on context cancellation and terminateBackgroundProcess for cleanup, so there
// is nothing to hold here. This keeps the build cross-platform while the real
// confinement lives in process_jobobject_windows.go.
type processContainment struct{}

// prepareProcessContainment is a no-op on non-Windows platforms: it returns a
// nil containment and an empty assign callback so callers can use the same code
// path everywhere.
func prepareProcessContainment(cmd *exec.Cmd) (*processContainment, func()) {
	_ = cmd
	return nil, func() {}
}

// Close is a no-op on non-Windows platforms.
func (c *processContainment) Close() {}
