//go:build !windows

package pluginhost

import (
	"errors"
	"os/exec"
	"syscall"
)

func platformExpectedTerminationWaitError(err error, interruptAccepted, killAccepted bool) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return false
	}
	signal := status.Signal()
	return (interruptAccepted && signal == syscall.SIGINT) || (killAccepted && signal == syscall.SIGKILL)
}
