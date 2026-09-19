//go:build darwin

package service

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func publishPKIAtomicNoReplace(source, destination string) error {
	err := unix.RenameatxNp(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_EXCL)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EEXIST) {
		return newPKIPublishConflict(destination)
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("atomic no-replace PKI publish is unsupported: %w", errors.Join(errors.ErrUnsupported, err))
	}
	return fmt.Errorf("atomic no-replace PKI publish: %w", err)
}
