//go:build windows

package storage

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

type pkiRestoreProcessLock struct {
	handle    windows.Handle
	exclusive bool
	intent    bool
}

func acquirePKIRestoreProcessLock(ctx context.Context, activeDatabasePath string, exclusive bool) (*pkiRestoreProcessLock, error) {
	lockPath := activeDatabasePath + ".pki-restore.lock"
	path, err := windows.UTF16PtrFromString(lockPath)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	lock := &pkiRestoreProcessLock{handle: handle, exclusive: exclusive}
	if exclusive {
		if err := lock.lockRange(ctx, 1, true); err != nil {
			_ = windows.CloseHandle(handle)
			return nil, err
		}
		lock.intent = true
	}
	if err := lock.lockRange(ctx, 0, exclusive); err != nil {
		if lock.intent {
			_ = lock.unlockRange(1)
		}
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	return lock, nil
}

func (l *pkiRestoreProcessLock) lockRange(ctx context.Context, offset uint32, exclusive bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	for {
		overlapped := windows.Overlapped{Offset: offset}
		err := windows.LockFileEx(l.handle, flags, 0, 1, 0, &overlapped)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) && !errors.Is(err, windows.ERROR_IO_PENDING) {
			return err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *pkiRestoreProcessLock) unlockRange(offset uint32) error {
	overlapped := windows.Overlapped{Offset: offset}
	return windows.UnlockFileEx(l.handle, 0, 1, 0, &overlapped)
}

func (l *pkiRestoreProcessLock) Downgrade(ctx context.Context) error {
	if l == nil || !l.exclusive {
		return nil
	}
	if err := l.unlockRange(0); err != nil {
		return err
	}
	if err := l.lockRange(ctx, 0, false); err != nil {
		return err
	}
	l.exclusive = false
	if l.intent {
		if err := l.unlockRange(1); err != nil {
			return err
		}
		l.intent = false
	}
	return nil
}

func (l *pkiRestoreProcessLock) Close() error {
	if l == nil || l.handle == 0 || l.handle == windows.InvalidHandle {
		return nil
	}
	var result error
	result = errors.Join(result, l.unlockRange(0))
	if l.intent {
		result = errors.Join(result, l.unlockRange(1))
	}
	result = errors.Join(result, windows.CloseHandle(l.handle))
	l.handle = windows.InvalidHandle
	return result
}

func renamePKIRestorePath(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromPtr, toPtr, windows.MOVEFILE_WRITE_THROUGH)
}

func syncPKIRestoreDirectory(path string) error {
	directory, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(directory, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	flushErr := windows.FlushFileBuffers(handle)
	// Windows does not guarantee that directory handles accept
	// FlushFileBuffers (notably on some NTFS configurations). Every restore
	// rename itself uses MOVEFILE_WRITE_THROUGH, so these documented
	// unsupported-directory results do not weaken rename durability.
	if errors.Is(flushErr, windows.ERROR_ACCESS_DENIED) || errors.Is(flushErr, windows.ERROR_INVALID_HANDLE) {
		flushErr = nil
	}
	closeErr := windows.CloseHandle(handle)
	return errors.Join(flushErr, closeErr)
}

func rejectAliasedPKIRestoreDatabasePath(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(pathPtr, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	var info windows.ByHandleFileInformation
	infoErr := windows.GetFileInformationByHandle(handle, &info)
	closeErr := windows.CloseHandle(handle)
	if infoErr != nil || closeErr != nil {
		return errors.Join(infoErr, closeErr)
	}
	if info.NumberOfLinks > 1 {
		return pkiInvariant("protected restore database hard links are not supported")
	}
	return nil
}
