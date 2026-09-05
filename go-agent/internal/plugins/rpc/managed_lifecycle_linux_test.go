//go:build linux && integration

package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/rpcplugin"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type managedLifecycleConfig struct {
	Endpoint        sdk.ManagedNetworkEndpoint
	ExpectedMatch   bool
	FailAfterListen bool
}

func TestIntegrationManagedLifecycleChild(t *testing.T) {
	if os.Getenv(sdk.EnvPluginEndpoint) == "" {
		return
	}
	var listener *sdk.ManagedNetworkHandle
	client, err := sdk.NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	hooks := rpcplugin.HookFuncs{PrepareFunc: func(ctx context.Context, generation *rpcplugin.Generation, config []byte) error {
		var configuration managedLifecycleConfig
		if err := json.Unmarshal(config, &configuration); err != nil {
			return err
		}
		reference, err := client.ResolveDataset(ctx, sdk.DatasetResolveRequest{SourceID: "regions"})
		if err != nil {
			return err
		}
		if reference.Generation != generation.ID() || reference.InstanceID != "instance" {
			return errors.New("dataset resolver did not bind actual lifecycle generation")
		}
		result, err := client.QueryDatasets(ctx, sdk.DatasetQueryRequest{Reference: reference, Address: "192.0.2.1", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}, Budget: sdk.DatasetQueryBudget{MaxDurationMicros: 2000, MaxResponseBytes: 32768}})
		if err != nil {
			return err
		}
		if result.Status != sdk.DatasetQueryOK || len(result.Matches) != 1 || result.Matches[0].Matched != configuration.ExpectedMatch {
			return errors.New("dataset query unavailable in Prepare")
		}
		secretRequest := sdk.ScopedSecretRequest{Action: sdk.ScopedSecretRead, Binding: sdk.ManagedBinding{InstanceID: "instance", Generation: generation.ID(), EntryID: "instance"}, Reference: sdk.ScopedSecretReference{InstanceID: "instance", ID: "credential", Scope: "relay", Version: strings.Repeat("a", 32)}}
		delivered, err := client.ScopedSecret(ctx, secretRequest)
		if err != nil {
			return err
		}
		defer delivered.Material.Close()
		if err := delivered.Material.WithBytes(func(value []byte) error {
			if string(value) != "integration-secret" {
				return errors.New("wrong scoped material")
			}
			return nil
		}); err != nil {
			return err
		}
		malformed := secretRequest
		malformed.Reference.ID = "malformed"
		if value, err := client.ScopedSecret(ctx, malformed); err == nil {
			value.Material.Close()
			return errors.New("malformed control response accepted")
		}
		wrong := secretRequest
		wrong.Binding.Generation = "provider-generation"
		if value, err := client.ScopedSecret(ctx, wrong); err == nil {
			value.Material.Close()
			return errors.New("provider ID accepted as runtime authority")
		}
		endpoint := configuration.Endpoint
		response, err := client.ManagedNetwork(ctx, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Binding: sdk.ManagedBinding{InstanceID: "instance", Generation: generation.ID(), EntryID: "instance"}, RequestID: "listen", Endpoint: &endpoint, Protocol: "tcp", MaxFlows: 4, IdleMS: 30000})
		listener = response.Handle
		if err == nil && configuration.FailAfterListen {
			return errors.New("candidate deliberately rejected after listen")
		}
		return err
	}, ActivateFunc: func(ctx context.Context, generation *rpcplugin.Generation) error {
		go func() {
			ctx := context.Background()
			response, err := client.ManagedNetwork(ctx, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Binding: listener.Binding, RequestID: "accept", Handle: listener, WaitMS: 30000})
			if err != nil {
				return
			}
			stream, err := sdk.NewManagedTCPStream(ctx, client, *response.Handle)
			if err != nil {
				return
			}
			defer stream.Close()
			io.Copy(stream, stream)
		}()
		return nil
	}}
	adapter, err := rpcplugin.NewAdapter(rpcplugin.Config{PluginID: "managed.test", PluginVersion: "1.0.0", RequiredGrants: []string{sdk.PermissionManagedNetworkListen, string(sdk.CapabilityDatasetQuery), string(sdk.CapabilityDatasetResolve), sdk.PermissionScopedSecretRead}, SupportedFeatures: sdk.RequiredRPCFeatures([]string{sdk.PermissionManagedNetworkListen, string(sdk.CapabilityDatasetQuery), string(sdk.CapabilityDatasetResolve), sdk.PermissionScopedSecretRead}), Timeouts: rpcplugin.UniformTimeouts(5 * time.Second)}, hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := sdk.ServeRPCPlugin(context.Background(), adapter); err != nil {
		t.Fatal(err)
	}
}
func TestIntegrationManagedLifecycleSandboxAndRevoke(t *testing.T) {
	directory := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	cachePath := filepath.Join(directory, "cache.bin")
	if err := os.WriteFile(cachePath, binary, 0o600); err != nil {
		t.Fatal(err)
	}
	packageDigest := strings.Repeat("a", 64)
	requirement, err := pluginprocess.NewSandboxRequirement(pluginprocess.SandboxRequirementProjection{PackageDigest: packageDigest, Permissions: []pluginprocess.SandboxPermission{pluginprocess.SandboxPermission(sdk.PermissionManagedNetworkListen), pluginprocess.SandboxPermission(sdk.CapabilityDatasetQuery), pluginprocess.SandboxPermission(sdk.CapabilityDatasetResolve), pluginprocess.SandboxPermission(sdk.PermissionScopedSecretRead)}, ResourceBudget: pluginprocess.ManifestResourceBudget{TimeoutMS: 5000, MemoryBytes: 256 << 20, Concurrency: 4, InputBytes: 1 << 20, OutputBytes: 1 << 20, CPUMillis: 1000, Restarts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(directory, "runtime")}, pluginprocess.NewSupervisor(nil, nil, os.Stderr), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	fences := filepath.Join(directory, "fences.json")
	if err := host.SetRevocationPath(fences); err != nil {
		t.Fatal(err)
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: reservation.Addr().(*net.TCPAddr).Port}
	reservation.Close()
	config, _ := json.Marshal(managedLifecycleConfig{Endpoint: endpoint, ExpectedMatch: true})
	view, datasetProvider := managedDatasetView(t, 1, "192.0.2.0/24")
	candidate := HostCandidate{InstanceID: "instance", PluginID: "managed.test", PluginVersion: "1.0.0", PackageDigest: packageDigest, Generation: view.ID(), ProviderGenerationID: "provider-generation", OperationID: "operation", Revision: 1, AgentID: "edge", Artifact: pluginprocess.Artifact{CachePath: cachePath, SHA256: hex.EncodeToString(digest[:]), GOOS: "linux", GOARCH: "amd64"}, Requirement: requirement, Scopes: []string{sdk.PermissionManagedNetworkListen, string(sdk.CapabilityDatasetQuery), string(sdk.CapabilityDatasetResolve), sdk.PermissionScopedSecretRead}, Grants: []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}, {Name: sdk.PermissionScopedSecretRead, ResourceKind: "secret-scope", ResourceID: "relay"}}, Config: config, Process: pluginprocess.InstanceSpec{Args: []string{"-test.run=^TestIntegrationManagedLifecycleChild$"}, GracePeriod: time.Second}, Dial: DialConfig{Network: "unix", Deadline: 5 * time.Second}, services: &runtimeServices{datasets: datasetProvider}}
	var authorityMu sync.Mutex
	authorities := map[string]HostCandidate{candidate.Generation: candidate}
	deliveries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Agent-Token") != "agent-token" || r.URL.Path != "/api/agent-plugin-secrets/redeem" {
			http.Error(w, "denied", 403)
			return
		}
		var request model.PluginSecretRedemptionRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Validate() != nil {
			http.Error(w, "invalid", 400)
			return
		}
		authorityMu.Lock()
		expected, found := authorities[request.RuntimeGenerationID]
		authorityMu.Unlock()
		if !found || request.GenerationID != expected.ProviderGenerationID || request.Revision != uint64(expected.Revision) || request.PackageDigest != expected.PackageDigest || request.ArtifactDigest != expected.Artifact.SHA256 {
			http.Error(w, "authority mismatch", 403)
			return
		}
		scoped, err := sdk.DecodeScopedSecretRequest(request.Scoped)
		if err != nil {
			http.Error(w, "invalid scoped", 400)
			return
		}
		defer scoped.Material.Close()
		if scoped.Reference.ID == "malformed" {
			w.Write([]byte(`{"scoped":{},"secrets":[{"id":"wrong"}]}`))
			return
		}
		material, _ := sdk.NewManagedSecretMaterial([]byte("integration-secret"))
		defer material.Close()
		encoded, err := sdk.EncodeScopedSecretResponse(scoped, sdk.ScopedSecretResponse{Reference: scoped.Reference, Material: material})
		if err != nil {
			http.Error(w, "invalid", 500)
			return
		}
		defer clear(encoded)
		authorityMu.Lock()
		deliveries++
		authorityMu.Unlock()
		json.NewEncoder(w).Encode(model.PluginSecretRedemptionResponse{Scoped: encoded})
	}))
	defer server.Close()
	host.SetSecretRedeemer(control.NewSyncClient(control.SyncClientConfig{MasterURL: server.URL, AgentToken: "agent-token"}, server.Client()))
	instance, err := host.Activate(t.Context(), candidate)
	if err != nil {
		t.Fatalf("real sandbox lifecycle: %v", err)
	}
	if instance.Status().PID <= 0 {
		t.Fatal("no actual process")
	}
	peer, err := net.Dial("tcp", net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(5 * time.Second))
	peer.Write([]byte("echo"))
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(peer, buffer); err != nil || string(buffer) != "echo" {
		t.Fatalf("sandbox relay: %q %v", buffer, err)
	}
	failedView, failedProvider := managedDatasetView(t, 3, "192.0.2.0/24")
	failedCandidate := candidate
	failedCandidate.Generation = failedView.ID()
	failedCandidate.Revision = 3
	failedCandidate.services = &runtimeServices{datasets: failedProvider}
	failedCandidate.ProviderGenerationID = "provider-failed"
	failedCandidate.OperationID = "operation-failed"
	failedCandidate.Config, _ = json.Marshal(managedLifecycleConfig{Endpoint: endpoint, ExpectedMatch: true, FailAfterListen: true})
	authorityMu.Lock()
	authorities[failedCandidate.Generation] = failedCandidate
	authorityMu.Unlock()
	if _, err := host.PrepareCandidate(t.Context(), failedCandidate); err == nil {
		t.Fatal("failed prepared listener candidate succeeded")
	}
	peer.Write([]byte("kept"))
	if _, err := io.ReadFull(peer, buffer); err != nil || string(buffer) != "kept" {
		t.Fatalf("candidate failure displaced old listener: %v", err)
	}
	nextView, nextProvider := managedDatasetView(t, 2, "198.51.100.0/24")
	nextCandidate := candidate
	nextCandidate.Generation = nextView.ID()
	nextCandidate.ProviderGenerationID = "provider-next"
	nextCandidate.Revision = 2
	nextCandidate.OperationID = "operation-next"
	nextCandidate.services = &runtimeServices{datasets: nextProvider}
	nextCandidate.Config, _ = json.Marshal(managedLifecycleConfig{Endpoint: endpoint, ExpectedMatch: false})
	authorityMu.Lock()
	authorities[nextCandidate.Generation] = nextCandidate
	authorityMu.Unlock()
	replacement, err := host.PrepareCandidate(t.Context(), nextCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.ReadyCandidate(replacement); err != nil {
		t.Fatal(err)
	}
	if err := host.ActivatePreparedCandidate(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if err := host.PublishPreparedGeneration(nextCandidate.Generation, []*HostedInstance{replacement}); err != nil {
		t.Fatal(err)
	}
	peer.Write([]byte("old!"))
	if _, err := io.ReadFull(peer, buffer); err != nil || string(buffer) != "old!" {
		t.Fatalf("old flow failed during switch: %v", err)
	}
	request := model.PluginGenerationRevokeRequest{InstanceID: candidate.InstanceID, PluginID: candidate.PluginID, GenerationID: candidate.Generation, ProviderGenerationID: candidate.ProviderGenerationID, Revision: 1, FenceID: "fence"}
	if err := host.RevokeGeneration(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	host.mu.RLock()
	retained := len(host.instances)
	host.mu.RUnlock()
	if retained != 1 {
		t.Fatalf("terminated generation retained resources: %d", retained)
	}
	if replacement.terminated() || replacement.Status().PID <= 0 {
		t.Fatal("old revoke stopped newer generation")
	}
	if !instance.terminated() {
		t.Fatal("ack before process exit")
	}
	if _, err := peer.Read(buffer); err == nil {
		t.Fatal("revoked socket survived")
	}
	nextPeer, err := net.Dial("tcp", net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer nextPeer.Close()
	nextPeer.SetDeadline(time.Now().Add(5 * time.Second))
	nextPeer.Write([]byte("new!"))
	if _, err := io.ReadFull(nextPeer, buffer); err != nil || string(buffer) != "new!" {
		t.Fatalf("new generation did not survive: %v", err)
	}
	if _, err := host.Activate(t.Context(), candidate); err == nil {
		t.Fatal("revoked generation relaunched")
	}
	restarted, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(directory, "restart")}, pluginprocess.NewSupervisor(nil, nil, io.Discard), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(context.Background())
	if err := restarted.SetRevocationPath(fences); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Activate(t.Context(), candidate); err == nil {
		t.Fatal("restart lost durable fence")
	}
	authorityMu.Lock()
	readCount := deliveries
	authorityMu.Unlock()
	if readCount != 3 {
		t.Fatalf("actual HTTP scoped reads = %d, want each launched generation", readCount)
	}
	if err := restarted.RevokeGeneration(t.Context(), request); err != nil {
		t.Fatal("idempotent fence", err)
	}
}
func TestIntegrationManagedSDKProcessTransport(t *testing.T) {
	TestManagedRuntimeSDKProcessTransport(t)
}
