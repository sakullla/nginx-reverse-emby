//go:build !linux && !windows && !darwin

package service

import (
	"errors"
	"fmt"
)

func publishPKIAtomicNoReplace(string, string) error {
	return fmt.Errorf("atomic no-replace PKI publish is unsupported on this platform: %w", errors.ErrUnsupported)
}
