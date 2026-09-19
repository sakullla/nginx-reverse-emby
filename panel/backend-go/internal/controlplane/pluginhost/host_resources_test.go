package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestHostResourceServerCleanupTreatsClosedListenerAsSuccess(t *testing.T) {
	cookie := make([]byte, 32)
	if _, err := rand.Read(cookie); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(os.TempDir(), "nre-host-"+hex.EncodeToString(cookie[:6])+".sock")
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	candidate := Candidate{
		InstanceID: "example-instance",
		hostEndpoint: Endpoint{
			Network: "unix",
			Address: socketPath,
			Cookie:  hex.EncodeToString(cookie),
		},
	}
	cleanup, err := startHostResourceServer(context.Background(), candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup returned a normal listener-close error: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("repeated cleanup returned an error: %v", err)
	}
}
