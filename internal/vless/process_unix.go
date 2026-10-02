//go:build !windows

package vless

import (
	"os/exec"
	"syscall"
)

// configureProcess puts the Xray child in its own process group so the whole
// tree can be terminated without signalling this process.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate stops the child and every process in its group.
func terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = cmd.Wait()
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
