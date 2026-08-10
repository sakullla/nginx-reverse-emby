//go:build linux

package rpc

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

	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
)

func TestRPCRealLinuxSandboxGRPCHandshake(t *testing.T) {
	if os.Getenv("NRE_TEST_LINUX_SANDBOX_GUEST") == "1" {
		runLinuxSandboxGuest(t)
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
	supervisor := pluginprocess.NewSupervisor(nil, nil, os.Stderr)
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, supervisor, nil)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := hex.EncodeToString(hash.Sum(nil))
	candidate := HostCandidate{InstanceID: "sandbox", PluginID: "plugin", PluginVersion: "1", PackageDigest: artifactDigest, Generation: "g1", Artifact: pluginprocess.Artifact{CachePath: cache, SHA256: artifactDigest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Scopes: []string{"relay.read"}, Process: pluginprocess.InstanceSpec{Args: []string{"-test.run=^TestRPCRealLinuxSandboxGRPCHandshake$"}, Environment: []string{"NRE_TEST_LINUX_SANDBOX_GUEST=1"}, GracePeriod: time.Second}, Dial: DialConfig{Network: "unix", Deadline: 5 * time.Second}}
	candidate.Requirement = agentSandboxRequirement(t, candidate.PackageDigest)
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(context.Background(), candidate.InstanceID); err != nil {
		t.Fatal(err)
	}
}

func runLinuxSandboxGuest(t *testing.T) {
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
	server.RegisterService(agentAttemptServiceDesc(string(cookieBytes)), struct{}{})
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}
