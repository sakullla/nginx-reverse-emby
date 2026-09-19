//go:build darwin || freebsd || linux

package plugins

import (
	"errors"
	"os"
	"syscall"
)

func platformStableFileKey(_ string, info os.FileInfo) (stableFileKey, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return stableFileKey{}, errors.New("filesystem did not expose a stable device/inode identity")
	}
	return stableFileKey{volume: uint64(stat.Dev), low: uint64(stat.Ino)}, nil
}
