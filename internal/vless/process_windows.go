//go:build windows

package vless

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const createNewProcessGroup = 0x00000200

// configureProcess detaches the child into its own process group so taskkill
// can stop the tree even when it spawns helpers.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// terminate stops the child process tree.
//
// Stopping a listener runs inside the window where its port is already closed,
// so the kill has to be as cheap as possible: every extra process launch is
// time in which a caller dialing this exit is refused and fails over to another
// node, paying another tunnel and TLS handshake. TerminateProcess on the handle
// we already hold does it directly; taskkill is only a fallback for the case
// where the child spawned helpers that outlive it.
func terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	if cmd.Process.Kill() == nil {
		<-exited
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
	select {
	case <-exited:
	case <-time.After(terminateGrace):
	}
}
