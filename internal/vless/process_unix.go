//go:build !windows

package vless

import (
	"os/exec"
	"syscall"
	"time"
)

// configureProcess puts the Xray child in its own process group so the whole
// tree can be terminated without signalling this process.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate stops the child and every process in its group.
//
// The wait is bounded: a child that ignores the graceful signal would otherwise
// block here forever, and this runs on the rotation path that is responsible for
// refreshing every listener.
func terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	select {
	case <-exited:
		return
	case <-time.After(terminateGrace):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	<-exited
}
