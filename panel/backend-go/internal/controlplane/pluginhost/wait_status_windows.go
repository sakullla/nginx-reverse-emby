//go:build windows

package pluginhost

import (
	"errors"
	"os/exec"
)

func platformExpectedTerminationWaitError(err error, _ bool, killAccepted bool) bool {
	if !killAccepted {
		return false
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ProcessState != nil && exitErr.ProcessState.ExitCode() == 1
}
