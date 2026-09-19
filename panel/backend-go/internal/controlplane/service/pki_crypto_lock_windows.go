//go:build windows

package service

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type pkiOSDirectoryLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquirePKIOSDirectoryLock(directory string) (pkiDirectoryLock, error) {
	file, err := os.OpenFile(filepath.Join(directory, pkiDirectoryLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	lock := &pkiOSDirectoryLock{file: file}
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &lock.overlapped); err != nil {
		_ = file.Close()
		return nil, err
	}
	return lock, nil
}

func (lock *pkiOSDirectoryLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, &lock.overlapped)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func pkiDirectorySyncUnsupported(_ error) bool {
	return false
}
