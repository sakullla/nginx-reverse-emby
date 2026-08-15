//go:build linux

package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	supervisor := pluginprocess.NewSupervisor(nil, nil, os.Stderr)
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, supervisor, nil)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := hex.EncodeToString(hash.Sum(nil))
	candidate := HostCandidate{InstanceID: "sandbox", PluginID: "plugin", PluginVersion: "1", PackageDigest: artifactDigest, Generation: "g1", Artifact: pluginprocess.Artifact{CachePath: cache, SHA256: artifactDigest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Scopes: []string{"relay.read"}, Process: pluginprocess.InstanceSpec{Args: []string{"-test.run=^TestRPCRealLinuxSandboxGRPCHandshake$"}, Environment: []string{"NRE_TEST_LINUX_SANDBOX_GUEST=1"}, GracePeriod: time.Second}, Dial: DialConfig{Network: "unix", Deadline: 5 * time.Second}}
	if os.Geteuid() == 0 {
		candidate.Process.Environment = append(candidate.Process.Environment, "NRE_TEST_REQUIRE_NAMESPACE_ROOT=1")
	}
	candidate.Requirement = agentSandboxRequirement(t, candidate.PackageDigest)
	candidate.Process.Environment = append(candidate.Process.Environment, "GOMAXPROCS=64", "NRE_TEST_EXPECT_NPROC="+strconv.Itoa(candidate.Requirement.Budget().Processes))
	if _, err := host.Activate(t.Context(), candidate); err != nil {
		t.Fatal(err)
	}
	replacement := candidate
	replacement.Generation = "g2"
	if _, err := host.Activate(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(context.Background(), candidate.InstanceID); err != nil {
		t.Fatal(err)
	}
	assertNoRPCAttemptDirectories(t, filepath.Join(root, "runtime"))
}

func runLinuxSandboxGuest(t *testing.T) {
	time.Sleep(1200 * time.Millisecond)
	if os.Getenv("NRE_TEST_REQUIRE_NAMESPACE_ROOT") == "1" {
		if os.Getpid() != 1 {
			t.Fatalf("production sandbox used fallback instead of the leased-UID PID namespace: pid=%d", os.Getpid())
		}
		if _, err := os.Stat("/etc/passwd"); err == nil {
			t.Fatal("production leased-UID sandbox did not enter the minimal root")
		}
	}
	if os.Getenv("GOMAXPROCS") != "1" {
		t.Fatalf("sandbox GOMAXPROCS = %q, want 1", os.Getenv("GOMAXPROCS"))
	}
	expectedNPROC, err := strconv.ParseUint(os.Getenv("NRE_TEST_EXPECT_NPROC"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	var nproc unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NPROC, &nproc); err != nil || nproc.Cur != expectedNPROC || nproc.Max != expectedNPROC {
		t.Fatalf("sandbox RLIMIT_NPROC = %+v, %v; want %d", nproc, err, expectedNPROC)
	}
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
	server.RegisterService(agentAttemptServiceDesc(string(cookieBytes), server.GracefulStop), struct{}{})
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
}

func assertNoRPCAttemptDirectories(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".p-") {
			t.Fatalf("RPC attempt directory leaked after replacement/stop: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
