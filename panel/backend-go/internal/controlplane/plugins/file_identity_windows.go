//go:build windows

package plugins

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func platformStableFileKey(name string, _ os.FileInfo) (stableFileKey, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return stableFileKey{}, err
	}
	handle, err := windows.CreateFile(
		path,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return stableFileKey{}, err
	}
	defer windows.CloseHandle(handle)
	var identity windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &identity); err != nil {
		return stableFileKey{}, err
	}
	if identity.FileIndexHigh == 0 && identity.FileIndexLow == 0 {
		return stableFileKey{}, errors.New("filesystem returned an empty stable file identity")
	}
	return stableFileKey{
		volume: uint64(identity.VolumeSerialNumber),
		high:   uint64(identity.FileIndexHigh),
		low:    uint64(identity.FileIndexLow),
	}, nil
}
