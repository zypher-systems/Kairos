package exec

import (
	"os/exec"
	"syscall"
)

// setProcessGroup isolates the command into its own process group so the
// group can be killed wholesale on timeout/cancel.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup SIGKILLs the entire process group.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// The group id equals the leader pid thanks to Setpgid.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
