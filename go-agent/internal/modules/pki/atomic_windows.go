//go:build windows

package pki

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func replaceActiveFile(source, target string) error {
	return moveFileWriteThrough(source, target, true)
}

func publishDirectory(source, target string) error {
	return moveFileWriteThrough(source, target, false)
}

func publishImmutableFile(source, target string) error {
	return moveFileWriteThrough(source, target, false)
}

func moveFileWriteThrough(source, target string, replace bool) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(from, to, flags)
}

// Every durable file replacement and directory publication uses MoveFileEx
// with MOVEFILE_WRITE_THROUGH. Opening directories for FlushFileBuffers is not
// portable across supported Windows versions, so callers establish ordering
// through these write-through commit operations instead.
func syncDirectory(string) error { return nil }

func restrictPath(path string, directory bool) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	inheritance := ""
	if directory {
		inheritance = "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf(
		"D:P(A;%s;GA;;;%s)(A;%s;GA;;;SY)(A;%s;GA;;;BA)",
		inheritance, user.User.Sid.String(), inheritance, inheritance,
	))
	if err != nil {
		return err
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		user.User.Sid, nil, acl, nil,
	); err != nil {
		return err
	}
	return verifyPrivatePath(path, directory)
}

func verifyPrivatePath(path string, directory bool) error {
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	return verifyPrivateSecurityDescriptor(descriptor, directory, path)
}

func verifyPrivateFile(file *os.File) error {
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	return verifyPrivateSecurityDescriptor(descriptor, false, file.Name())
}

func verifyPrivateSecurityDescriptor(descriptor *windows.SECURITY_DESCRIPTOR, directory bool, path string) error {
	if descriptor == nil {
		return fmt.Errorf("PKI path has no security descriptor: %s", path)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("PKI path DACL inherits broad access: %s", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("PKI path has no restrictive DACL: %s", path)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return fmt.Errorf("PKI path owner is not the current process identity: %s", path)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	wanted := map[string]struct{}{
		user.User.Sid.String():  {},
		system.String():         {},
		administrators.String(): {},
	}
	seen := make(map[string]struct{}, len(wanted))
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return err
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			return fmt.Errorf("PKI path DACL contains an unsafe ACE: %s", path)
		}
		inheritance := ace.Header.AceFlags & (windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
		if !directory && inheritance != 0 {
			return fmt.Errorf("PKI file DACL has unsafe inheritance flags %#x: %s", ace.Header.AceFlags, path)
		}
		expandedFullControl := windows.ACCESS_MASK(windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff)
		if ace.Mask&windows.GENERIC_ALL == 0 && ace.Mask&expandedFullControl != expandedFullControl {
			return fmt.Errorf("PKI path DACL mask %#x does not grant its owner full control: %s", ace.Mask, path)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		name := sid.String()
		if _, ok := wanted[name]; !ok {
			return fmt.Errorf("PKI path DACL grants an unexpected principal %s: %s", name, path)
		}
		seen[name] = struct{}{}
	}
	for name := range wanted {
		if _, ok := seen[name]; !ok {
			return fmt.Errorf("PKI path DACL omits required principal %s: %s", name, path)
		}
	}
	return nil
}

func migratePrivatePath(path string, directory bool) error {
	return restrictPath(path, directory)
}
