//go:build !windows && !linux

package process

import "os/exec"

type unavailableSandbox struct{}

func newPlatformSandbox() Sandbox                  { return unavailableSandbox{} }
func (unavailableSandbox) Available() bool         { return false }
func (unavailableSandbox) Provider() string        { return "unavailable" }
func (unavailableSandbox) Validate(Security) error { return nil }
func (unavailableSandbox) Configure(*exec.Cmd, Security) (func() error, func() error, func(int) error, error) {
	return func() error { return nil }, func() error { return nil }, func(int) error { return nil }, nil
}
