package nodetransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// instanceWorkspace models the real layout the sandbox is bound to:
// <root>/<instanceID>/<appID>/... under the plugin resource root.
func instanceWorkspace(t *testing.T, root, appID string) string {
	t.Helper()
	dir := filepath.Join(root, "inst-1", appID, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func waitForState(t *testing.T, service *Service, transferID, wantState string) (string, int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, state, bytes, lastError, err := service.Status(transferID)
		if err != nil {
			t.Fatalf("status %s: %v", transferID, err)
		}
		if state == wantState {
			return lastError, bytes
		}
		if state == StateFailed {
			t.Fatalf("transfer %s failed: %s", transferID, lastError)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("transfer %s never reached %s", transferID, wantState)
	return "", 0
}

func TestServiceSendReceiveRoundTripStoresVerifiedArtifact(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(instanceWorkspace(t, root, "srcapp"), "app-20261022.tar.zst")
	content := []byte("backup artifact bytes")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	service, err := NewService(Config{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	address, err := service.OpenSend(context.Background(), "nt-send-1", "inst-1", "srcapp", "app-20261022.tar.zst")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartReceive("nt-recv-1", "inst-1", "dstapp", "app-20261022.tar.zst", address, int64(len(content)), hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	waitForState(t, service, "nt-recv-1", StateCompleted)
	waitForState(t, service, "nt-send-1", StateCompleted)
	stored, err := os.ReadFile(filepath.Join(root, "inst-1", "dstapp", "backups", "app-20261022.tar.zst"))
	if err != nil {
		t.Fatalf("stored artifact missing: %v", err)
	}
	if string(stored) != string(content) {
		t.Fatal("stored artifact content differs")
	}
	remaining, err := filepath.Glob(filepath.Join(root, "inst-1", "dstapp", "backups", "*.incoming-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("part files left behind after rename: %q", remaining)
	}
}

func TestServiceReceiveRejectsDigestMismatch(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(instanceWorkspace(t, root, "srcapp"), "a.tar.zst")
	if err := os.WriteFile(source, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	address, err := service.OpenSend(context.Background(), "nt-send-2", "inst-1", "srcapp", "a.tar.zst")
	if err != nil {
		t.Fatal(err)
	}
	badDigest := make64('0')
	if err := service.StartReceive("nt-recv-2", "inst-1", "dstapp", "a.tar.zst", address, int64(len("artifact")), badDigest); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, state, _, lastError, err := service.Status("nt-recv-2")
		if err != nil {
			t.Fatal(err)
		}
		if state == StateCompleted {
			t.Fatal("receive with a wrong digest completed")
		}
		if state == StateFailed {
			if lastError == "" {
				t.Fatal("failed receive lacks an error")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("receive never reached a terminal state, state = %s", state)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(root, "inst-1", "dstapp", "backups", "a.tar.zst")); err == nil {
		t.Fatal("unverified artifact was stored")
	}
}

func TestServiceReceiveCapsReadAtDeclaredSize(t *testing.T) {
	root := t.TempDir()
	// A sender stream larger than the declared size must fail verification
	// without writing more than size+1 bytes to staging.
	source := filepath.Join(instanceWorkspace(t, root, "srcapp"), "big.tar.zst")
	payload := make([]byte, 512)
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	address, err := service.OpenSend(context.Background(), "nt-send-3", "inst-1", "srcapp", "big.tar.zst")
	if err != nil {
		t.Fatal(err)
	}
	undersized := int64(len(payload)) - 100
	digest := sha256.Sum256(payload)
	if err := service.StartReceive("nt-recv-3", "inst-1", "dstapp", "big.tar.zst", address, undersized, hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, state, _, _, err := service.Status("nt-recv-3")
		if err != nil {
			t.Fatal(err)
		}
		if state == StateFailed {
			break
		}
		if state == StateCompleted {
			t.Fatal("oversized stream accepted")
		}
		if time.Now().After(deadline) {
			t.Fatalf("receive never reached a terminal state, state = %s", state)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(root, "inst-1", "dstapp", "backups", "big.tar.zst")); err == nil {
		t.Fatal("oversized artifact was stored")
	}
}

func TestServiceRejectsPathsOutsideManagedRoot(t *testing.T) {
	service, err := NewService(Config{WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenSend(context.Background(), "nt-x", "../escape", "app1", "a.tar.zst"); err == nil {
		t.Fatal("instance id escape accepted")
	}
	if _, err := service.OpenSend(context.Background(), "nt-x", "inst-1", "app1", "../a.tar.zst"); err == nil {
		t.Fatal("artifact escape accepted")
	}
	if err := service.StartReceive("nt-y", "inst-1", "app1", "a/b.tar.zst", "127.0.0.1:1", 1, make64('a')); err == nil {
		t.Fatal("artifact name with separator accepted")
	}
}

func TestServiceOpenSendRequiresExistingArtifact(t *testing.T) {
	service, err := NewService(Config{WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenSend(context.Background(), "nt-z", "inst-1", "app1", "missing.tar.zst"); err == nil {
		t.Fatal("missing artifact accepted")
	}
}

func TestServiceCloseDropsRecords(t *testing.T) {
	service, err := NewService(Config{WorkspaceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartReceive("nt-c", "inst-1", "app1", "a.tar.zst", "127.0.0.1:1", 10, make64('b')); err != nil {
		t.Fatal(err)
	}
	if err := service.Close("nt-c"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := service.Status("nt-c"); err == nil {
		t.Fatal("closed transfer still queryable")
	}
}

func make64(char byte) string {
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = char
	}
	return string(buf)
}
