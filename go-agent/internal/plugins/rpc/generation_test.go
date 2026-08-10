package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type generationLifecycleClient struct {
	mu                                sync.Mutex
	handshakes, prepares, activations int
	stops                             int
	prepareErr                        error
	activateErr                       error
}

type generationTestSandbox struct{}

func (generationTestSandbox) Available() bool                       { return true }
func (generationTestSandbox) Provider() string                      { return "generation-test" }
func (generationTestSandbox) Validate(pluginprocess.Security) error { return nil }
func (generationTestSandbox) Configure(*exec.Cmd, pluginprocess.Security) (func() error, func() error, func(int) error, error) {
	return nil, nil, nil, nil
}

func (c *generationLifecycleClient) Handshake(_ context.Context, request pluginsdk.RPCHandshakeRequest) (pluginsdk.RPCHandshakeResponse, error) {
	c.mu.Lock()
	c.handshakes++
	c.mu.Unlock()
	return pluginsdk.RPCHandshakeResponse{ABI: pluginsdk.RPCABIV1, Capabilities: append([]string(nil), request.GrantedScopes...), Features: append([]string(nil), request.RequiredFeatures...)}, nil
}

func (c *generationLifecycleClient) Prepare(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	c.mu.Lock()
	c.prepares++
	err := c.prepareErr
	c.mu.Unlock()
	if err != nil {
		return pluginsdk.LifecycleResponse{}, err
	}
	return readyLifecycleResponse(), nil
}

func (c *generationLifecycleClient) Activate(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	c.mu.Lock()
	c.activations++
	err := c.activateErr
	c.mu.Unlock()
	if err != nil {
		return pluginsdk.LifecycleResponse{}, err
	}
	return readyLifecycleResponse(), nil
}

func (c *generationLifecycleClient) Stop(context.Context, pluginsdk.LifecycleRequest) (pluginsdk.LifecycleResponse, error) {
	c.mu.Lock()
	c.stops++
	c.mu.Unlock()
	return readyLifecycleResponse(), nil
}

func readyLifecycleResponse() pluginsdk.LifecycleResponse {
	return pluginsdk.LifecycleResponse{Success: &pluginsdk.LifecycleSuccess{Ready: true}}
}

func (c *generationLifecycleClient) counts() (int, int, int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handshakes, c.prepares, c.activations, c.stops
}

func TestRPCGenerationPrepareReadyAtomicPublishAndDestroy(t *testing.T) {
	root := t.TempDir()
	generation := rpcPluginGenerationForTest(t, root, 1, "operation-1")
	client := &generationLifecycleClient{}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return client, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	moduleUnderTest := NewGenerationModule(host)
	request := module.ApplyRequest{Previous: model.Snapshot{}, Next: model.Snapshot{Revision: 1, PluginGenerations: []model.PluginGeneration{generation}}}
	prepared, err := moduleUnderTest.Prepare(t.Context(), request)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	transaction := prepared.(*generationTransaction)
	if _, active := host.Active(generation.InstanceID); active {
		t.Fatal("prepared candidate became visible")
	}
	if _, _, activations, _ := client.counts(); activations != 0 {
		t.Fatalf("activations after Prepare = %d", activations)
	}
	if err := transaction.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := transaction.PrepareGenerationPublication(t.Context()); err != nil {
		t.Fatal(err)
	}
	transaction.FinalizeGenerationPublication()
	if _, active := host.Active(generation.InstanceID); !active {
		t.Fatal("final publication did not expose candidate")
	}
	statuses := transaction.PluginRuntimeStatuses()
	if len(statuses) != 1 || statuses[0].PluginID != generation.PluginID || statuses[0].OperationID != generation.OperationID || statuses[0].Revision != 1 || statuses[0].GenerationID != generation.ID || statuses[0].PackageDigest != generation.PackageDigest || statuses[0].ArtifactDigest != generation.Artifact.SHA256 || statuses[0].ConfigVersion != generation.ConfigVersion || statuses[0].Sequence != 1 || statuses[0].State != "active" {
		t.Fatalf("status identity = %+v", statuses)
	}
	if err := transaction.Destroy(t.Context()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if _, active := host.Active(generation.InstanceID); active {
		t.Fatal("destroy left published instance active")
	}
	if _, _, activations, stops := client.counts(); activations != 1 || stops != 1 {
		t.Fatalf("lifecycle activations/stops = %d/%d", activations, stops)
	}
}

func TestRPCGenerationPrepareFailureCleansCandidateWithoutPublishing(t *testing.T) {
	root := t.TempDir()
	generation := rpcPluginGenerationForTest(t, root, 2, "operation-2")
	client := &generationLifecycleClient{prepareErr: context.DeadlineExceeded}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return client, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	moduleUnderTest := NewGenerationModule(host)
	_, err = moduleUnderTest.Prepare(t.Context(), module.ApplyRequest{Next: model.Snapshot{Revision: 2, PluginGenerations: []model.PluginGeneration{generation}}})
	if err == nil {
		t.Fatal("Prepare() accepted failed candidate")
	}
	if _, active := host.Active(generation.InstanceID); active {
		t.Fatal("failed candidate became active")
	}
	host.mu.RLock()
	pending := len(host.pending)
	host.mu.RUnlock()
	if pending != 0 {
		t.Fatalf("failed candidate pending ownership = %d", pending)
	}
}

func TestRPCGenerationActivationFailureCleansAllCandidatesAndPublishesNone(t *testing.T) {
	root := t.TempDir()
	first := rpcPluginGenerationForTest(t, root, 3, "operation-3a")
	first.InstanceID = "instance-a"
	second := rpcPluginGenerationForTest(t, root, 3, "operation-3b")
	second.InstanceID = "instance-b"
	firstClient := &generationLifecycleClient{}
	secondClient := &generationLifecycleClient{activateErr: context.DeadlineExceeded}
	clients := []LifecycleClient{firstClient, secondClient}
	var clientsMu sync.Mutex
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		clientsMu.Lock()
		defer clientsMu.Unlock()
		client := clients[0]
		clients = clients[1:]
		return client, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	moduleUnderTest := NewGenerationModule(host)
	prepared, err := moduleUnderTest.Prepare(t.Context(), module.ApplyRequest{Next: model.Snapshot{Revision: 3, PluginGenerations: []model.PluginGeneration{first, second}}})
	if err != nil {
		t.Fatal(err)
	}
	transaction := prepared.(*generationTransaction)
	if err := transaction.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := transaction.PrepareGenerationPublication(t.Context()); err == nil {
		t.Fatal("PrepareGenerationPublication() accepted activation failure")
	}
	if err := transaction.Destroy(t.Context()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if _, active := host.Active(first.InstanceID); active {
		t.Fatal("first candidate became active after sibling failure")
	}
	if _, active := host.Active(second.InstanceID); active {
		t.Fatal("failed sibling became active")
	}
	if _, _, _, stops := firstClient.counts(); stops != 1 {
		t.Fatalf("first candidate stops = %d, want cleanup", stops)
	}
}

func TestRPCGenerationDisableCutsOverBeforePreviousDrainCleanup(t *testing.T) {
	root := t.TempDir()
	generation := rpcPluginGenerationForTest(t, root, 4, "operation-4")
	client := &generationLifecycleClient{}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return client, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	moduleUnderTest := NewGenerationModule(host)
	firstPrepared, err := moduleUnderTest.Prepare(t.Context(), module.ApplyRequest{Next: model.Snapshot{Revision: 4, PluginGenerations: []model.PluginGeneration{generation}}})
	if err != nil {
		t.Fatal(err)
	}
	first := firstPrepared.(*generationTransaction)
	if err := first.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := first.PrepareGenerationPublication(t.Context()); err != nil {
		t.Fatal(err)
	}
	first.FinalizeGenerationPublication()

	disabledPrepared, err := moduleUnderTest.Prepare(t.Context(), module.ApplyRequest{
		Previous: model.Snapshot{Revision: 4, PluginGenerations: []model.PluginGeneration{generation}},
		Next:     model.Snapshot{Revision: 5, PluginGenerations: []model.PluginGeneration{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled := disabledPrepared.(*generationTransaction)
	if err := disabled.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := disabled.PrepareGenerationPublication(t.Context()); err != nil {
		t.Fatal(err)
	}
	disabled.FinalizeGenerationPublication()
	if _, active := host.Active(generation.InstanceID); active {
		t.Fatal("disabled generation left behavior visible")
	}
	if _, _, _, stops := client.counts(); stops != 0 {
		t.Fatalf("previous generation stopped before drain = %d", stops)
	}
	if err := first.Destroy(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, stops := client.counts(); stops != 1 {
		t.Fatalf("previous generation stops after drain = %d", stops)
	}
	if err := disabled.Destroy(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func rpcPluginGenerationForTest(t *testing.T, root string, revision int64, operationID string) model.PluginGeneration {
	t.Helper()
	payload := []byte("rpc generation executable")
	cachePath := filepath.Join(root, "cache-"+operationID)
	if err := os.WriteFile(cachePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return model.PluginGeneration{
		ID: "generation-" + operationID, InstanceID: "instance", OperationID: operationID, Revision: revision,
		PluginID: "example.rpc", PluginVersion: "1.0.0", PackageDigest: strings.Repeat("a", 64),
		Runtime: model.PluginRuntimeDescriptor{Kind: model.PluginRuntimeRPCService, ABI: model.PluginRPCABIV1, HostScope: "agent", Entry: "artifacts/plugin"},
		Artifact: model.PluginArtifactDescriptor{ArtifactID: "artifact-" + operationID, PackageIdentity: "example.rpc@1.0.0", RelativePath: "artifacts/plugin",
			SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(payload)), Mode: "executable", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
			SignatureVerified: true, SignerKeyID: "release-key", SignerFingerprint: strings.Repeat("b", 64), LocalPath: cachePath},
		ExtensionPoints: []string{"http.request"}, ConfigVersion: 1, Config: json.RawMessage(`{}`),
		Grants:         []model.PluginGrantProjection{{Name: "agent.read"}},
		ResourceBudget: model.PluginResourceBudget{TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100, Restarts: 1},
		Target:         model.PluginTargetBinding{Kind: "agent", ID: "edge", ResourceGroupID: "default", Version: 1},
		FailurePolicy:  model.PluginFailurePolicy{OnError: "degraded", OnBudget: "fail-closed", Restart: "on-failure", CoreFallback: "preserve"},
	}
}
