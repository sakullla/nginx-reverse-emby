//go:build !windows

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type pkiRestoreProcessLock struct {
	fd        int
	exclusive bool
	intent    bool
}

func acquirePKIRestoreProcessLock(ctx context.Context, activeDatabasePath string, exclusive bool) (*pkiRestoreProcessLock, error) {
	fd, err := unix.Open(activeDatabasePath+".pki-restore.lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	lock := &pkiRestoreProcessLock{fd: fd, exclusive: exclusive}
	if exclusive {
		if err := lock.lockRange(ctx, true); err != nil {
			_ = unix.Close(fd)
			return nil, err
		}
		lock.intent = true
		return lock, nil
	}
	if err := lock.lockRange(ctx, false); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return lock, nil
}

func (l *pkiRestoreProcessLock) lockRange(ctx context.Context, exclusive bool) error {
	mode := unix.LOCK_SH | unix.LOCK_NB
	if exclusive {
		mode = unix.LOCK_EX | unix.LOCK_NB
	}
	for {
		err := unix.Flock(l.fd, mode)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
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

func (l *pkiRestoreProcessLock) Downgrade(context.Context) error {
	if l == nil || !l.exclusive {
		return nil
	}
	if err := unix.Flock(l.fd, unix.LOCK_SH); err != nil {
		return err
	}
	l.exclusive = false
	l.intent = false
	return nil
}

func (l *pkiRestoreProcessLock) Close() error {
	if l == nil || l.fd < 0 {
		return nil
	}
	result := errors.Join(unix.Flock(l.fd, unix.LOCK_UN), unix.Close(l.fd))
	l.fd = -1
	return result
}

func renamePKIRestorePath(from, to string) error {
	return os.Rename(from, to)
}

func syncPKIRestoreDirectory(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func rejectAliasedPKIRestoreDatabasePath(path string) error {
	var info unix.Stat_t
	if err := unix.Stat(path, &info); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if info.Nlink > 1 {
		return pkiInvariant("protected restore database hard links are not supported")
	}
	return nil
}
