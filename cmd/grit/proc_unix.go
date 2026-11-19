//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// detach puts the daemon in its own session so closing the terminal that
// ran grit install does not stop it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// hideConsole is only needed on Windows.
func hideConsole() {}
