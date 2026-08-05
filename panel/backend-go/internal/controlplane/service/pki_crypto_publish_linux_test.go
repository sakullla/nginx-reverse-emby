//go:build linux

package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPublishPKIAtomicNoReplaceLinuxFallsBackToHardLink(t *testing.T) {
	t.Run("publishes when renameat2 is unsupported", func(t *testing.T) {
		directory := t.TempDir()
		source := filepath.Join(directory, "staging")
		destination := filepath.Join(directory, "canonical")
		payload := []byte("published")
		if err := os.WriteFile(source, payload, 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}

		err := publishPKIAtomicNoReplaceLinux(source, destination, func(string, string) error {
			return unix.EOPNOTSUPP
		})
		if err != nil {
			t.Fatalf("publish fallback error = %v", err)
		}
		if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source after fallback error = %v, want os.ErrNotExist", err)
		}
		stored, err := os.ReadFile(destination)
		if err != nil || !bytes.Equal(stored, payload) {
			t.Fatalf("published payload = %q, %v; want %q", stored, err, payload)
		}
	})

	t.Run("preserves an existing destination", func(t *testing.T) {
		directory := t.TempDir()
		source := filepath.Join(directory, "staging")
		destination := filepath.Join(directory, "canonical")
		winner := []byte("winner")
		if err := os.WriteFile(source, []byte("loser"), 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}
		if err := os.WriteFile(destination, winner, 0o600); err != nil {
			t.Fatalf("write destination: %v", err)
		}

		err := publishPKIAtomicNoReplaceLinux(source, destination, func(string, string) error {
			return unix.EINVAL
		})
		if !errors.Is(err, os.ErrExist) || !isPurePKIPublishConflict(err) {
			t.Fatalf("publish conflict error = %v, want pure typed os.ErrExist", err)
		}
		stored, readErr := os.ReadFile(destination)
		if readErr != nil || !bytes.Equal(stored, winner) {
			t.Fatalf("destination payload = %q, %v; want %q", stored, readErr, winner)
		}
	})
}
