package cloudflared

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// prepare keeps Ctrl+C in the terminal from reaching cloudflared directly, so
// that portlift alone decides when the tunnel stops.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killWithParent puts the started process in a job object that Windows tears
// down when portlift exits for any reason, so a crash or a killed terminal
// cannot leave cloudflared serving the tunnel on its own.
func killWithParent(cmd *exec.Cmd) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(proc)
	// The job handle is deliberately left open: closing it, which happens
	// when portlift exits, is what kills cloudflared.
	windows.AssignProcessToJobObject(job, proc)
}

// interrupt has nothing gentler than Kill to offer on Windows.
func interrupt(cmd *exec.Cmd) { cmd.Process.Kill() }
