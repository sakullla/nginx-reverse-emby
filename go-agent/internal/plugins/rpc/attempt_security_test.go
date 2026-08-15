package rpc

import (
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRPCAttemptSecuritySetupFailureReturnsRetryableCleanupOwner(t *testing.T) {
	runtimeDirectory := t.TempDir()
	setupErr := errors.New("TLS setup failed")
	cleanupErr := errors.New("credential cleanup failed")
	cleanupCalls := 0
	attempt, err := provisionAttemptSecurityWithOps(runtimeDirectory, DialConfig{Network: "tcp", Address: "127.0.0.1:12345"}, attemptSecurityOps{
		writeTLS: func(string) (*tls.Config, []string, error) { return nil, nil, setupErr },
		cleanup: func(runtimeRoot, attemptRoot string) error {
			cleanupCalls++
			if cleanupCalls == 1 {
				return cleanupErr
			}
			return cleanupAttemptDirectory(runtimeRoot, attemptRoot)
		},
	})
	if !errors.Is(err, setupErr) {
		t.Fatalf("setup error = %v", err)
	}
	if attempt.cleanup == nil || attempt.credentialDirectory == "" {
		t.Fatalf("setup failure did not return its partial owner: %+v", attempt)
	}
	if err := attempt.cleanup(); !errors.Is(err, cleanupErr) {
		t.Fatalf("first cleanup error = %v", err)
	}
	if _, err := os.Stat(attempt.credentialDirectory); err != nil {
		t.Fatalf("failed cleanup lost retryable credential owner: %v", err)
	}
	if err := attempt.cleanup(); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
	if _, err := os.Stat(attempt.credentialDirectory); !os.IsNotExist(err) {
		t.Fatalf("credential directory survived retry: %v", err)
	}
}

func TestRPCAttemptSecurityIsFreshPrivateAndDestroyed(t *testing.T) {
	runtimeDirectory := t.TempDir()
	first, err := provisionAttemptSecurity(runtimeDirectory, DialConfig{Network: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := provisionAttemptSecurity(runtimeDirectory, DialConfig{Network: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if first.dial.Cookie == second.dial.Cookie || first.dial.Address == second.dial.Address || first.dial.Cookie == "" {
		t.Fatal("attempt cookie or endpoint was reused")
	}
	for _, attempt := range []attemptSecurity{first, second} {
		info, err := os.Stat(attempt.credentialDirectory)
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
			t.Fatalf("credential directory is not private: %v, %v", info, err)
		}
		if err := validateUnixEndpoint(attempt.endpointDirectory, attempt.dial.Address); err != nil || filepath.Base(attempt.guestEndpoint) != filepath.Base(attempt.dial.Address) {
			t.Fatalf("unix endpoint is not host-managed: %+v", attempt)
		}
		if err := attempt.cleanup(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(attempt.credentialDirectory); !os.IsNotExist(err) {
			t.Fatal("attempt credentials survived cleanup")
		}
	}
}

func TestRPCAttemptSecurityOwnsMutualTLSMaterial(t *testing.T) {
	attempt, err := provisionAttemptSecurity(t.TempDir(), DialConfig{Network: "tcp", Address: "127.0.0.1:43210"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = attempt.cleanup() })
	if attempt.dial.TLSConfig == nil || attempt.dial.TLSConfig.ServerName != "nre-plugin" || len(attempt.dial.TLSConfig.Certificates) != 1 || attempt.dial.TLSConfig.RootCAs == nil {
		t.Fatal("host did not provision a complete mutual-TLS client identity")
	}
	for _, name := range []string{"cookie", "ca.crt", "server.crt", "server.key"} {
		info, err := os.Stat(filepath.Join(attempt.credentialDirectory, name))
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
			t.Fatalf("credential %s is not private: %v, %v", name, info, err)
		}
	}
}

func TestRPCAttemptCleanupRejectsOutsideRuntime(t *testing.T) {
	runtimeDirectory := t.TempDir()
	outside := filepath.Join(t.TempDir(), ".rpc-attempt-outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cleanupAttemptDirectory(runtimeDirectory, outside); err == nil {
		t.Fatal("cleanup accepted an attempt directory outside the managed runtime")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("rejected cleanup changed outside directory: %v", err)
	}
}
