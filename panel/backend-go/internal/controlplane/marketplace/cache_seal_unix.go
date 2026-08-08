//go:build !windows

package marketplace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func sealCacheTree(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("verified cache contains symlink %s", name)
		}
		if entry.IsDir() {
			directories = append(directories, name)
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("verified cache contains non-regular file %s", name)
		}
		return os.Chmod(name, 0o444)
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index], 0o555); err != nil {
			return err
		}
	}
	return nil
}

func unsealCacheTree(root string) error {
	return filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return os.Chmod(name, 0o755)
		}
		return os.Chmod(name, 0o644)
	})
}
