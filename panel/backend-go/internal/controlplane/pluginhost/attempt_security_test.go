package pluginhost

import (
	"os"
	"path/filepath"
	"testing"
)

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
		if filepath.Dir(attempt.endpoint.Address) != attempt.endpointDirectory || attempt.guestEndpoint != controlGuestEndpointDirectory+"/rpc.sock" {
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
