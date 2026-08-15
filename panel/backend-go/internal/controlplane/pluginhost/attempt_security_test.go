package pluginhost

import (
	"crypto/tls"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginHostAttemptSetupFailureReturnsRetryableCleanupOwner(t *testing.T) {
	runtimeDirectory := t.TempDir()
	setupErr := errors.New("TLS setup failed")
	cleanupErr := errors.New("credential cleanup failed")
	cleanupCalls := 0
	attempt, err := provisionControlAttemptSecurityWithOps(runtimeDirectory, Endpoint{Network: "tcp", Address: "127.0.0.1:12345"}, controlAttemptSecurityOps{
		writeTLS: func(string) (*tls.Config, []string, error) { return nil, nil, setupErr },
		cleanup: func(runtimeRoot, attemptRoot string) error {
			cleanupCalls++
			if cleanupCalls == 1 {
				return cleanupErr
			}
			return cleanupControlAttemptDirectory(runtimeRoot, attemptRoot)
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

func TestPluginHostAttemptSecurityIsFreshAndDestroyed(t *testing.T) {
	runtimeDirectory := t.TempDir()
	first, err := provisionControlAttemptSecurity(runtimeDirectory, Endpoint{Network: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := provisionControlAttemptSecurity(runtimeDirectory, Endpoint{Network: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if first.endpoint.Cookie == second.endpoint.Cookie || first.endpoint.Address == second.endpoint.Address || first.endpoint.Cookie == "" {
		t.Fatal("attempt cookie or endpoint was reused")
	}
	for _, attempt := range []controlAttemptSecurity{first, second} {
		if err := validateEndpoint(filepath.Dir(attempt.endpointDirectory), attempt.endpoint); err != nil || filepath.Base(attempt.guestEndpoint) != filepath.Base(attempt.endpoint.Address) {
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

func TestPluginHostAttemptSecurityOwnsMutualTLS(t *testing.T) {
	attempt, err := provisionControlAttemptSecurity(t.TempDir(), Endpoint{Network: "tcp", Address: "127.0.0.1:43210"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = attempt.cleanup() })
	if attempt.endpoint.TLSConfig == nil || attempt.endpoint.TLSConfig.ServerName != "nre-plugin" || len(attempt.endpoint.TLSConfig.Certificates) != 1 || attempt.endpoint.TLSConfig.RootCAs == nil {
		t.Fatal("host did not provision complete mutual-TLS material")
	}
}

func TestPluginHostAttemptCleanupRejectsOutsideRuntime(t *testing.T) {
	runtimeDirectory := t.TempDir()
	outside := filepath.Join(t.TempDir(), ".rpc-attempt-outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cleanupControlAttemptDirectory(runtimeDirectory, outside); err == nil {
		t.Fatal("cleanup accepted an attempt directory outside the managed runtime")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("rejected cleanup changed outside directory: %v", err)
	}
}
