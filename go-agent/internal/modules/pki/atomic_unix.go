//go:build !windows

package pki

import "os"

func replaceActiveFile(source, target string) error {
	return os.Rename(source, target)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func restrictPath(string, bool) error { return nil }
