//go:build !windows

package service

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type pkiOSDirectoryLock struct {
	file *os.File
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
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &pkiOSDirectoryLock{file: file}, nil
}

func (lock *pkiOSDirectoryLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func pkiDirectorySyncUnsupported(err error) bool {
	return errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP)
}
