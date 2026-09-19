//go:build linux

package service

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func publishPKIAtomicNoReplace(source, destination string) error {
	return publishPKIAtomicNoReplaceLinux(source, destination, func(source, destination string) error {
		return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
	})
}

func publishPKIAtomicNoReplaceLinux(source, destination string, renameNoReplace func(string, string) error) error {
	err := renameNoReplace(source, destination)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EEXIST) {
		return newPKIPublishConflict(destination)
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		if linkErr := os.Link(source, destination); linkErr != nil {
			if errors.Is(linkErr, os.ErrExist) {
				return newPKIPublishConflict(destination)
			}
			return fmt.Errorf("atomic no-replace PKI publish is unsupported and hard-link fallback failed: %w", errors.Join(errors.ErrUnsupported, err, linkErr))
		}
		if removeErr := os.Remove(source); removeErr != nil {
			return fmt.Errorf("%w: remove source after atomic hard-link PKI publish: %w", errPKIVaultCleanup, removeErr)
		}
		return nil
	}
	return fmt.Errorf("atomic no-replace PKI publish: %w", err)
}
