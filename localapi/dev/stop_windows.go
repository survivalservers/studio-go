//go:build windows

package dev

import (
	"os/exec"
	"syscall"
)

const (
	defaultStop = "local_api"
	stopMethods = "local_api (the notice only), ctrl_break (as a hosting machine's default), kill"
)

// prepareCommand: for ctrl_break the server starts in its own process group, as on a hosting machine, so Ctrl+Break
// can be sent to it alone. Windows then keeps Ctrl+C in the terminal from reaching it: Ctrl+C reaches the dev
// command, which sends the notice and, after the hold-back, Ctrl+Break. Otherwise the server shares the dev command's
// console and Ctrl+C reaches it directly.
func prepareCommand(cmd *exec.Cmd, method string) {
	if method == "ctrl_break" {
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	}
}

// exitCode is the server's own exit code (Windows has no signals to add).
func exitCode(ee *exec.ExitError) int { return ee.ExitCode() }

func validStop(m string) bool { return m == "local_api" || m == "kill" || m == "ctrl_break" }

var generateConsoleCtrlEvent = syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")

// signalStop sends the stop the version would get on a Windows hosting machine. local_api sends nothing: the shutdown
// notice is the signal.
func signalStop(cmd *exec.Cmd, method string) error {
	if cmd.Process == nil {
		return nil
	}
	switch method {
	case "kill":
		return cmd.Process.Kill()
	case "ctrl_break":
		if ok, _, err := generateConsoleCtrlEvent.Call(syscall.CTRL_BREAK_EVENT, uintptr(cmd.Process.Pid)); ok == 0 {
			return err
		}
	}
	return nil
}
