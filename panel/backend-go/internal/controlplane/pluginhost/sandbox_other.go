//go:build !windows && !linux

package pluginhost

import (
	"errors"
	"os/exec"
)

func validatePlatformSandbox(Candidate) error {
	return errors.New("control-plane plugin sandbox is unavailable")
}
func configurePlatformSandbox(_ *exec.Cmd, c Candidate) (func() error, func(int) (func() error, error), error) {
	if !hasUnsandboxedGrant(c.Grants) {
		return nil, nil, validatePlatformSandbox(c)
	}
	return func() error { return nil }, func(int) (func() error, error) { return func() error { return nil }, nil }, nil
}
