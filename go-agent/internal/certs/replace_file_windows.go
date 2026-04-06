//go:build windows

package certs

import "golang.org/x/sys/windows"

func replaceFile(sourcePath, targetPath string) error {
	sourcePtr, err := windows.UTF16PtrFromString(sourcePath)
	if err != nil {
		return err
	}
	targetPtr, err := windows.UTF16PtrFromString(targetPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourcePtr,
		targetPtr,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}
