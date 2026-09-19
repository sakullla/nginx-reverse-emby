//go:build windows

package marketplace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fileAddFile         = 0x0002
	fileAddSubdirectory = 0x0004
	fileDeleteChild     = 0x0040
	cacheFileDenyMask   = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | windows.FILE_EXECUTE | windows.DELETE
	cacheDirDenyMask    = fileAddFile | fileAddSubdirectory | fileDeleteChild | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | windows.DELETE
	cacheFileReadMask   = windows.FILE_READ_DATA | windows.FILE_READ_EA | windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.WRITE_DAC | windows.SYNCHRONIZE
	cacheDirReadMask    = cacheFileReadMask | windows.FILE_TRAVERSE
)

func sealCacheTree(root string) error {
	type cacheEntry struct {
		path      string
		directory bool
	}
	var entries []cacheEntry
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("verified cache contains symlink %s", path)
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("verified cache contains non-regular file %s", path)
			}
		}
		entries = append(entries, cacheEntry{path: path, directory: entry.IsDir()})
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(entries) - 1; index >= 0; index-- {
		if err := sealCachePath(entries[index].path, entries[index].directory); err != nil {
			_ = unsealCacheTree(root)
			return fmt.Errorf("seal verified cache path %s: %w", entries[index].path, err)
		}
	}
	return nil
}

func sealCachePath(path string, directory bool) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	denyMask, allowMask := uint32(cacheFileDenyMask), uint32(cacheFileReadMask)
	if directory {
		denyMask, allowMask = uint32(cacheDirDenyMask), uint32(cacheDirReadMask)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(D;;0x%x;;;WD)(A;;0x%x;;;%s)", user.User.Sid.String(), denyMask, allowMask, user.User.Sid.String()))
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return err
	}
	return verifyCachePathSealed(path, directory)
}

func sealCacheContainer(path string) error {
	return sealCachePath(path, true)
}

func verifyCachePathSealed(path string, directory bool) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("verified cache DACL is not protected: %s", path)
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("verified cache has no owner: %s", path)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !owner.Equals(user.User.Sid) {
		return fmt.Errorf("verified cache owner is not the control-plane identity: %s", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("verified cache has no DACL: %s", path)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return err
	}
	want := windows.ACCESS_MASK(cacheFileDenyMask)
	if directory {
		want = windows.ACCESS_MASK(cacheDirDenyMask)
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		// Allowed and denied ACEs share the ACCESS_MASK/SidStart layout.
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return err
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(everyone) && ace.Mask&want == want {
			return nil
		}
	}
	return fmt.Errorf("verified cache DACL does not deny execute/modify: %s", path)
}

func unsealCacheTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return unsealCachePath(path, entry.IsDir())
	})
}

func unsealCacheContainer(path string) error {
	return unsealCachePath(path, true)
}

func unsealCachePath(path string, directory bool) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;;FA;;;%s)", user.User.Sid.String(), user.User.Sid.String()))
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("restore owner DACL for %s: %w", path, err)
	}
	mode := os.FileMode(0o644)
	if directory {
		mode = 0o755
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("restore path attributes for %s: %w", path, err)
	}
	return nil
}
