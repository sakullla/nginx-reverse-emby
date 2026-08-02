//go:build !windows

package pki

import (
	"fmt"
	"os"
	"syscall"
)

func replaceActiveFile(source, target string) error {
	return os.Rename(source, target)
}

func publishDirectory(source, target string) error {
	return os.Rename(source, target)
}

func publishImmutableFile(source, target string) error {
	// Linking a fully-synced temporary file publishes the immutable name with
	// O_EXCL semantics. Unlike os.Rename, this can never replace a snapshot
	// that another process published between the caller's check and commit.
	if err := os.Link(source, target); err != nil {
		return err
	}
	return os.Remove(source)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func restrictPath(path string, directory bool) error {
	return verifyPrivatePath(path, directory)
}

func verifyPrivatePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("PKI path has an unsafe file type: %s", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("PKI path permissions are too broad: %s", path)
	}
	if err := verifyPrivateOwner(info, path); err != nil {
		return err
	}
	return nil
}

func verifyPrivateFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("PKI file permissions are unsafe: %s", file.Name())
	}
	if err := verifyPrivateOwner(info, file.Name()); err != nil {
		return err
	}
	return nil
}

func migratePrivatePath(path string, directory bool) error {
	return verifyPrivatePath(path, directory)
}

func verifyPrivateOwner(info os.FileInfo, path string) error {
	status, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("PKI path owner is unavailable: %s", path)
	}
	if status.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("PKI path is owned by uid %d, want uid %d: %s", status.Uid, os.Geteuid(), path)
	}
	return nil
}
