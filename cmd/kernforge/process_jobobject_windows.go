//go:build windows

package main

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processContainment bounds a child process and every descendant it spawns to a
// Windows Job Object. This is process-tree containment, NOT a full sandbox: it
// does not restrict file, registry, or network access. Its only guarantee is
// that when the job handle is closed (because the agent context ended, the
// command timed out, or kernforge itself exits) the WHOLE process tree is
// terminated, so detached grandchildren cannot outlive cancel/timeout.
type processContainment struct {
	mu     sync.Mutex
	job    windows.Handle
	closed bool
}

// access rights required to assign an already-started process to a job object.
const jobAssignProcessAccess = windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE

// prepareProcessContainment creates a kill-on-close job object and arranges for
// the command's child to be created suspended so it can be assigned to the job
// before it runs (closing the grandchild race window). It returns the
// containment handle and an assign callback the caller MUST invoke right after
// cmd.Start(). On any failure it degrades to a no-op so the command still runs
// (containment is best-effort, never a hard gate on execution).
//
// Usage:
//
//	containment, assign := prepareProcessContainment(cmd)
//	defer containment.Close() // closing kills the whole tree
//	cmd.Start()
//	assign()                  // assigns + resumes the suspended child
func prepareProcessContainment(cmd *exec.Cmd) (*processContainment, func()) {
	noop := func() {}
	if cmd == nil {
		return nil, noop
	}
	job, err := createKillOnCloseJob()
	if err != nil || job == 0 {
		return nil, noop
	}
	// Create the child suspended so it cannot spawn descendants before we have
	// assigned it to the job. We resume it inside the assign callback.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	c := &processContainment{job: job}
	assign := func() {
		c.assignAndResume(cmd)
	}
	return c, assign
}

// createKillOnCloseJob creates an anonymous job object whose entire tree is
// killed when the last handle to it is closed.
func createKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// assignAndResume assigns the started child process to the job and then resumes
// every suspended thread it has. Assignment failures are tolerated (the command
// still runs uncontained); resume is always attempted so a suspended child is
// never left frozen.
func (c *processContainment) assignAndResume(cmd *exec.Cmd) {
	if c == nil {
		resumeProcessThreads(processPID(cmd))
		return
	}
	pid := processPID(cmd)
	if pid <= 0 {
		return
	}
	c.mu.Lock()
	job := c.job
	closed := c.closed
	c.mu.Unlock()
	if job != 0 && !closed {
		if handle, err := windows.OpenProcess(jobAssignProcessAccess, false, uint32(pid)); err == nil {
			// Ignore the assign error: on failure the child simply runs without
			// job containment, which is the pre-existing behavior.
			_ = windows.AssignProcessToJobObject(job, handle)
			_ = windows.CloseHandle(handle)
		}
	}
	// Always resume, even if assignment failed, so we never leak a frozen child.
	resumeProcessThreads(pid)
}

// Close closes the job handle. Because the job was created with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, this terminates the entire process tree.
func (c *processContainment) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.job != 0 {
		_ = windows.CloseHandle(c.job)
		c.job = 0
	}
}

func processPID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

// resumeProcessThreads resumes all threads of the given process. A process
// created with CREATE_SUSPENDED has exactly one suspended primary thread, but we
// walk all threads defensively in case more exist.
func resumeProcessThreads(pid int) {
	if pid <= 0 {
		return
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return
	}
	for {
		if entry.OwnerProcessID == uint32(pid) {
			if thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID); openErr == nil {
				_, _ = windows.ResumeThread(thread)
				_ = windows.CloseHandle(thread)
			}
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			return
		}
	}
}
