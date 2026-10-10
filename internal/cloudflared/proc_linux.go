package cloudflared

import (
	"os"
	"os/exec"
	"syscall"
)

// prepare keeps Ctrl+C in the terminal from reaching cloudflared directly, so
// that portlift alone decides when the tunnel stops, and asks the kernel to
// stop cloudflared if portlift dies without getting the chance.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}

func killWithParent(*exec.Cmd) {}

func interrupt(cmd *exec.Cmd) { cmd.Process.Signal(os.Interrupt) }
