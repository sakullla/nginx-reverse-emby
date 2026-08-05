//go:build windows

package service

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func publishPKIAtomicNoReplace(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return newPKIPublishConflict(destination)
	}
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return fmt.Errorf("atomic no-replace PKI publish is unsupported: %w", errors.Join(errors.ErrUnsupported, err))
	}
	return fmt.Errorf("atomic no-replace PKI publish: %w", err)
}
