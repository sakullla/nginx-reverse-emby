//go:build !windows

package observability

import "os"

func durableAuditRename(source, target string) error { return os.Rename(source, target) }

func syncAuditDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
