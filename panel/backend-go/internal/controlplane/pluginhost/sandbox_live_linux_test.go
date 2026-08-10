//go:build linux

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
)

func TestPluginHostRealLinuxSandboxGRPCHandshake(t *testing.T) {
	if os.Getenv("NRE_TEST_CONTROL_LINUX_SANDBOX_GUEST") == "1" {
		runControlLinuxSandboxGuest(t)
		return
	}
	if testing.Short() {
		t.Skip("real bwrap/cgroup/seccomp gRPC belongs to the full tier")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap unavailable")
	}
	if _, err := exec.LookPath("prlimit"); err != nil || unix.Access("/sys/fs/cgroup", unix.W_OK) != nil {
		t.Skip("writable cgroup v2 or prlimit unavailable")
	}
	root, err := os.MkdirTemp("/tmp", "nre-sandbox-")
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
	candidate := Candidate{InstanceID: "sandbox", Artifact: Artifact{CachePath: cache, SHA256: hex.EncodeToString(hash.Sum(nil)), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Identity: Identity{PluginID: "plugin", Version: "1", PackageDigest: "package", Generation: "g1", Scopes: []string{"relay.read"}}, Args: []string{"-test.run=^TestPluginHostRealLinuxSandboxGRPCHandshake$"}, Environment: []string{"NRE_TEST_CONTROL_LINUX_SANDBOX_GUEST=1"}, Endpoint: Endpoint{Network: "unix"}, Budget: ProcessBudget{CPUMillis: 1000, MemoryBytes: 256 << 20, Processes: 8, Files: 128, Network: false}, Deadline: 5 * time.Second, GracePeriod: time.Second}
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(context.Background(), candidate.InstanceID); err != nil {
		t.Fatal(err)
	}
}

func runControlLinuxSandboxGuest(t *testing.T) {
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
	server.RegisterService(controlAttemptServiceDesc(string(cookieBytes)), struct{}{})
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}
