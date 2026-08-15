//go:build linux

package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const acceleratorSourcesArtifactEnv = "NRE_ACCELERATOR_SOURCES_ARTIFACT"

func TestRPCRealAcceleratorSourcesArtifactLifecycle(t *testing.T) {
	artifactPath := strings.TrimSpace(os.Getenv(acceleratorSourcesArtifactEnv))
	if artifactPath == "" {
		t.Skip(acceleratorSourcesArtifactEnv + " is required for the cross-repository artifact test")
	}
	artifactFile, err := os.Open(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, artifactFile); err != nil {
		_ = artifactFile.Close()
		t.Fatal(err)
	}
	if err := artifactFile.Close(); err != nil {
		t.Fatal(err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	runtimeRoot := filepath.Join(t.TempDir(), "runtime")
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: runtimeRoot}, pluginprocess.NewSupervisor(nil, nil, io.Discard), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background(), "accelerator-e2e") })
	t.Setenv("PATH", t.TempDir())

	candidate := acceleratorSourcesCandidate(t, artifactPath, digest, "generation-1", []byte(`{}`))
	first, err := host.PrepareCandidate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.ActivatePreparedCandidate(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	publication, err := host.PrepareGenerationPublication(candidate.Generation, []*HostedInstance{first})
	if err != nil {
		t.Fatal(err)
	}
	publication.Publish()
	assertAcceleratorSourcesBusinessResponse(t, first)

	invalid := acceleratorSourcesCandidate(t, artifactPath, digest, "generation-invalid", []byte(`{}`))
	invalid.PluginVersion = "mismatched-version"
	if failed, err := host.PrepareCandidate(t.Context(), invalid); err == nil {
		_ = host.DestroyCandidate(failed)
		t.Fatal("invalid accelerator candidate reached readiness")
	}
	assertAcceleratorSourcesBusinessResponse(t, first)
	if first.terminated() {
		t.Fatal("failed accelerator candidate stopped the active generation")
	}

	replacement := acceleratorSourcesCandidate(t, artifactPath, digest, "generation-2", []byte(`{}`))
	second, err := host.PrepareCandidate(t.Context(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.ActivatePreparedCandidate(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	replacementPublication, err := host.PrepareGenerationPublication(replacement.Generation, []*HostedInstance{second})
	if err != nil {
		t.Fatal(err)
	}
	replacementPublication.Publish()
	assertAcceleratorSourcesBusinessResponse(t, second)
	if err := host.DestroyCandidate(first); err != nil {
		t.Fatal(err)
	}
	if !first.terminated() {
		t.Fatal("retired accelerator generation was not cleaned after its leases drained")
	}

	if err := host.Stop(context.Background(), replacement.InstanceID); err != nil {
		t.Fatal(err)
	}
	restarted := acceleratorSourcesCandidate(t, artifactPath, digest, "generation-3", []byte(`{}`))
	third, err := host.PrepareCandidate(t.Context(), restarted)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.ActivatePreparedCandidate(t.Context(), third); err != nil {
		t.Fatal(err)
	}
	restartPublication, err := host.PrepareGenerationPublication(restarted.Generation, []*HostedInstance{third})
	if err != nil {
		t.Fatal(err)
	}
	restartPublication.Publish()
	assertAcceleratorSourcesBusinessResponse(t, third)
}

func acceleratorSourcesCandidate(t *testing.T, artifactPath, digest, generation string, config []byte) HostCandidate {
	t.Helper()
	descriptors := []pluginsdk.HTTPBackendProviderDescriptor{{ID: "default", DisplayName: "Accelerator Sources"}}
	return HostCandidate{
		InstanceID: "accelerator-e2e", PluginID: "accelerator-sources", PluginVersion: "0.1.0",
		PackageDigest: digest, Generation: generation, Config: config,
		Artifact:    pluginprocess.Artifact{CachePath: artifactPath, SHA256: digest, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Requirement: providerSandboxRequirement(t, digest), Scopes: []string{pluginsdk.PermissionHTTPOutbound},
		HTTPBackendProviders: descriptors,
		Process:              pluginprocess.InstanceSpec{GracePeriod: 2 * time.Second},
		Dial:                 DialConfig{Network: "unix", Deadline: 8 * time.Second, HTTPBackendProviders: httpBackendProviderIdentities("accelerator-e2e", generation, descriptors)},
	}
}

func assertAcceleratorSourcesBusinessResponse(t *testing.T, instance *HostedInstance) {
	t.Helper()
	handle := newHTTPBackendProviderHandle(instance, "default")
	lease, err := handle.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://provider.nre.internal/", nil)
	if err != nil {
		_ = lease.Close()
		t.Fatal(err)
	}
	response, err := handle.RoundTrip(request, HTTPBackendProviderAuthority{Scheme: "https", Host: "accelerator.example.test", ClientAddress: "203.0.113.7:44321"})
	if err != nil {
		_ = lease.Close()
		t.Fatal(err)
	}
	WrapHTTPBackendProviderResponseLease(request.Context(), response, lease)
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read accelerator response: read=%v close=%v", err, closeErr)
	}
	if response.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("accelerator response status=%d bytes=%d", response.StatusCode, len(body))
	}
}
