//go:build !windows

package observability

import "os"

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
