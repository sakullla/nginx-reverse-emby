//go:build !windows

package certs

import "os"

func replaceFile(sourcePath, targetPath string) error {
	return os.Rename(sourcePath, targetPath)
}
