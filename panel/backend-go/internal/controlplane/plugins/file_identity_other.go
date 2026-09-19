//go:build !darwin && !freebsd && !linux && !windows

package plugins

import (
	"errors"
	"os"
)

func platformStableFileKey(_ string, _ os.FileInfo) (stableFileKey, error) {
	return stableFileKey{}, errors.New("stable hardlink identity is unsupported on this platform")
}
