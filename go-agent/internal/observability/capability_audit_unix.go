//go:build !windows

package observability

import (
	"errors"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

func durableAuditRename(source, target string) error { return os.Rename(source, target) }

func durableAuditCreate(source, target string) error {
	if err := os.Link(source, target); err != nil {
		return err
	}
	return os.Remove(source)
}

func syncAuditDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func capabilityAuditFreeSpace(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	if stat.Bsize <= 0 {
		return 0, errors.New("capability audit filesystem block size is invalid")
	}
	blockSize := uint64(stat.Bsize)
	if uint64(stat.Bavail) > math.MaxUint64/blockSize {
		return math.MaxUint64, nil
	}
	return uint64(stat.Bavail) * blockSize, nil
}
