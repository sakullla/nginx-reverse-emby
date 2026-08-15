//go:build linux

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func TestPluginHostRealLinuxSandboxGRPCHandshake(t *testing.T) {
	if os.Getenv("NRE_TEST_CONTROL_LINUX_SANDBOX_GUEST") == "1" {
		runControlLinuxSandboxGuest(t)
		return
	}
	if testing.Short() {
		t.Skip("real kernel-isolated gRPC belongs to the full tier")
	}
	t.Setenv("PATH", t.TempDir())
	root, err := os.MkdirTemp("", "nr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	cache := filepath.Join(root, "cache")
	source, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.OpenFile(cache, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(target, hash), source); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	host, err := New(filepath.Join(root, "runtime"), nil, GRPCDialer{}, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := hex.EncodeToString(hash.Sum(nil))
	candidate := Candidate{InstanceID: "sandbox", Artifact: Artifact{CachePath: cache, SHA256: artifactDigest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: artifactDigest, Generation: "g1", Scopes: []string{"relay.read"}}, Args: []string{"-test.run=^TestPluginHostRealLinuxSandboxGRPCHandshake$"}, Environment: []string{"NRE_TEST_CONTROL_LINUX_SANDBOX_GUEST=1"}, Endpoint: Endpoint{Network: "unix"}, Deadline: 5 * time.Second, GracePeriod: time.Second}
	candidate.Requirement = mustValidatedSandboxRequirement(t, candidate.Identity.PackageDigest)
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	replacement := candidate
	replacement.Identity.Generation = "g2"
	if _, err := host.Activate(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(context.Background(), candidate.InstanceID); err != nil {
		t.Fatal(err)
	}
	assertNoControlAttemptDirectories(t, filepath.Join(root, "runtime"))
}

func runControlLinuxSandboxGuest(t *testing.T) {
	time.Sleep(1200 * time.Millisecond)
	endpoint := strings.TrimPrefix(os.Getenv("NRE_PLUGIN_ENDPOINT"), "unix:")
	cookieBytes, err := os.ReadFile(os.Getenv("NRE_PLUGIN_COOKIE_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	server.RegisterService(controlAttemptServiceDesc(string(cookieBytes), server.GracefulStop), struct{}{})
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}

func assertNoControlAttemptDirectories(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".p-") {
			t.Fatalf("control RPC attempt directory leaked after replacement/stop: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
