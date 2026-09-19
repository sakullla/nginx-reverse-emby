//go:build !windows && !linux

package pluginhost

import (
	"errors"
	"os/exec"
)

func validatePlatformSandbox(Candidate) error {
	return errors.New("control-plane plugin sandbox is unavailable")
}
func configurePlatformSandbox(_ *exec.Cmd, c Candidate) (func() error, func() error, func(int) error, error) {
	return nil, nil, nil, validatePlatformSandbox(c)
}
