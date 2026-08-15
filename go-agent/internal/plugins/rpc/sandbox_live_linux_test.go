//go:build linux

package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
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
	descriptors := []pluginsdk.HTTPBackendProviderDescriptor{{ID: "default", DisplayName: "Default"}, {ID: "secondary", DisplayName: "Secondary"}}
	candidate := HostCandidate{InstanceID: "sandbox", PluginID: "plugin", PluginVersion: "1", PackageDigest: artifactDigest, Generation: "g1", Artifact: pluginprocess.Artifact{CachePath: cache, SHA256: artifactDigest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, Scopes: []string{"relay.read", pluginsdk.PermissionHTTPOutbound}, HTTPBackendProviders: descriptors, Process: pluginprocess.InstanceSpec{Args: []string{"-test.run=^TestRPCRealLinuxSandboxGRPCHandshake$"}, Environment: []string{"NRE_TEST_LINUX_SANDBOX_GUEST=1", "NRE_TEST_GUEST_GENERATION=g1"}, GracePeriod: time.Second}, Dial: DialConfig{Network: "unix", Deadline: 5 * time.Second, HTTPBackendProviders: httpBackendProviderIdentities("sandbox", "g1", descriptors)}}
	if os.Geteuid() == 0 {
		candidate.Process.Environment = append(candidate.Process.Environment, "NRE_TEST_REQUIRE_NAMESPACE_ROOT=1")
	}
	candidate.Requirement = providerSandboxRequirement(t, candidate.PackageDigest)
	candidate.Process.Environment = append(candidate.Process.Environment, "GOMAXPROCS=64", "NRE_TEST_EXPECT_NPROC="+strconv.Itoa(candidate.Requirement.Budget().Processes))
	first, err := host.PrepareCandidate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.ActivatePreparedCandidate(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	firstPublication, err := host.PrepareGenerationPublication("g1", []*HostedInstance{first})
	if err != nil {
		t.Fatal(err)
	}
	firstPublication.Publish()
	oldResponse := openLinuxProviderPayload(t, first, "default")
	prefix := make([]byte, 32<<10)
	n, err := io.ReadFull(oldResponse.Body, prefix)
	if err != nil || n != len(prefix) {
		t.Fatalf("read first g1 stream chunk = %d, %v", n, err)
	}
	assertLinuxProviderPayload(t, first, "secondary", "g1", len("secondary:g1"))
	replacement := candidate
	replacement.Generation = "g2"
	replacement.Dial.HTTPBackendProviders = httpBackendProviderIdentities("sandbox", "g2", descriptors)
	replacement.Process.Environment = []string{"NRE_TEST_LINUX_SANDBOX_GUEST=1", "NRE_TEST_GUEST_GENERATION=g2", "GOMAXPROCS=64", "NRE_TEST_EXPECT_NPROC=" + strconv.Itoa(candidate.Requirement.Budget().Processes)}
	second, err := host.PrepareCandidate(t.Context(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.ActivatePreparedCandidate(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	secondPublication, err := host.PrepareGenerationPublication("g2", []*HostedInstance{second})
	if err != nil {
		t.Fatal(err)
	}
	secondPublication.Publish()
	assertLinuxProviderPayload(t, second, "default", "g2", 2<<20)
	if first.terminated() {
		t.Fatal("g1 provider attempt stopped while its progressive response lease was active")
	}
	remainder, err := io.ReadAll(oldResponse.Body)
	_ = oldResponse.Body.Close()
	if err != nil || len(prefix)+len(remainder) != 2<<20 || oldResponse.Header.Get("X-Guest-Generation") != "g1" {
		t.Fatalf("g1 progressive stream after g2 publish = %d bytes/%q, %v", len(prefix)+len(remainder), oldResponse.Header.Get("X-Guest-Generation"), err)
	}
	if err := host.DestroyCandidate(first); err != nil {
		t.Fatal(err)
	}
	if !first.terminated() {
		t.Fatal("g1 provider attempt remained after its final stream lease and destroy")
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
	providerCtx, cancelProviders := context.WithCancel(context.Background())
	defer cancelProviders()
	providerErr := make(chan error, 1)
	generationID := os.Getenv("NRE_TEST_GUEST_GENERATION")
	go func() {
		providerErr <- pluginsdk.ServeHTTPBackendProviders(providerCtx, map[string]http.Handler{
			"default": http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Header.Get(pluginsdk.HeaderHTTPBackendProviderCredential) != "" || request.Header.Get("X-Forwarded-Host") != "public.example.test" {
					http.Error(response, "provider header contract failed", http.StatusBadRequest)
					return
				}
				response.Header().Set("X-Guest-Generation", generationID)
				chunk := bytes.Repeat([]byte(generationID+":"), 4096)
				remaining := 2 << 20
				for remaining > 0 {
					payload := chunk
					if len(payload) > remaining {
						payload = payload[:remaining]
					}
					if _, err := response.Write(payload); err != nil {
						return
					}
					remaining -= len(payload)
					time.Sleep(time.Millisecond)
				}
			}),
			"secondary": http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(response, "secondary:"+generationID)
			}),
		})
	}()
	if err := server.Serve(listener); err != nil {
		t.Fatal(err)
	}
	cancelProviders()
	if err := <-providerErr; err != nil {
		t.Fatal(err)
	}
}

func providerSandboxRequirement(t *testing.T, digest string) pluginprocess.SandboxRequirement {
	t.Helper()
	requirement, err := pluginprocess.NewSandboxRequirement(pluginprocess.SandboxRequirementProjection{
		PackageDigest:   digest,
		Permissions:     []pluginprocess.SandboxPermission{pluginprocess.PermissionAgentRead, pluginprocess.PermissionHTTPOutbound},
		ExtensionPoints: []pluginprocess.SandboxExtensionPoint{pluginprocess.ExtensionHTTPRequest, pluginprocess.ExtensionHTTPBackendProvider},
		ResourceBudget:  pluginprocess.ManifestResourceBudget{TimeoutMS: 5000, MemoryBytes: 256 << 20, Concurrency: 8, InputBytes: 4 << 20, OutputBytes: 4 << 20, CPUMillis: 1000, Restarts: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	return requirement
}

func assertLinuxProviderPayload(t *testing.T, instance *HostedInstance, providerID, generationID string, expectedBytes int) {
	t.Helper()
	response := openLinuxProviderPayload(t, instance, providerID)
	payload, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != expectedBytes {
		t.Fatalf("provider %s payload bytes = %d, want %d", providerID, len(payload), expectedBytes)
	}
	if providerID == "default" && response.Header.Get("X-Guest-Generation") != generationID {
		t.Fatalf("provider generation header = %q, want %q", response.Header.Get("X-Guest-Generation"), generationID)
	}
}

func openLinuxProviderPayload(t *testing.T, instance *HostedInstance, providerID string) *http.Response {
	t.Helper()
	handle := newHTTPBackendProviderHandle(instance, providerID)
	lease, err := handle.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://provider.nre.internal/test", nil)
	request.Header.Set("Forwarded", "for=spoofed;proto=http")
	request.Header.Set(pluginsdk.HeaderHTTPBackendProviderCredential, "spoofed")
	response, err := handle.RoundTrip(request, HTTPBackendProviderAuthority{Scheme: "https", Host: "public.example.test", ClientAddress: "203.0.113.9:44321"})
	if err != nil {
		_ = lease.Close()
		t.Fatal(err)
	}
	WrapHTTPBackendProviderResponseLease(request.Context(), response, lease)
	return response
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
