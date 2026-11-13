//go:build !windows

package dev

import (
	"os/exec"
	"syscall"
)

const (
	defaultStop = "sigterm"
	stopMethods = "sigterm, sigint, local_api (the notice only), kill"
)

func prepareCommand(cmd *exec.Cmd, method string) {}

// exitCode is the server's exit code as a shell reports it: 128 plus the signal's number when a signal ended it
// (143 for SIGTERM), not Go's -1.
func exitCode(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ee.ExitCode()
}

func validStop(m string) bool {
	return m == "sigterm" || m == "sigint" || m == "local_api" || m == "kill"
}

// signalStop sends the stop the version would get on a Linux hosting machine.
func signalStop(cmd *exec.Cmd, method string) error {
	if cmd.Process == nil {
		return nil
	}
	switch method {
	case "sigterm":
		return cmd.Process.Signal(syscall.SIGTERM)
	case "sigint":
		return cmd.Process.Signal(syscall.SIGINT)
	case "kill":
		return cmd.Process.Kill()
	}
	return nil
}
