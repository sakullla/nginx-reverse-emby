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
	"time"

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

type generationControlledRunner struct {
	mu      sync.Mutex
	process *hostProcess
}

func (r *generationControlledRunner) Start(context.Context, pluginprocess.InstanceSpec, pluginprocess.Sandbox, io.Writer) (pluginprocess.ManagedProcess, func() error, error) {
	process := &hostProcess{done: make(chan error, 1)}
	r.mu.Lock()
	r.process = process
	r.mu.Unlock()
	return process, func() error { return nil }, nil
}

func (r *generationControlledRunner) Fail(err error) {
	r.mu.Lock()
	process := r.process
	r.mu.Unlock()
	if process != nil {
		process.once.Do(func() { process.done <- err })
	}
}

func (generationTestSandbox) Available() bool                       { return true }
func (generationTestSandbox) Provider() string                      { return "generation-test" }
func (generationTestSandbox) Validate(pluginprocess.Security) error { return nil }
func (generationTestSandbox) Configure(*exec.Cmd, pluginprocess.Security) (func() error, func() error, func(int) error, error) {
	return nil, nil, nil, nil
}

func TestHostCandidatePreservesProviderGenerationAndAgentIdentity(t *testing.T) {
	generation := rpcPluginGenerationForTest(t, t.TempDir(), 7, "operation-7")
	generation.SecretHandles = []model.PluginSecretHandle{{ID: "secret-a", Version: 2, Digest: strings.Repeat("c", 64), Purpose: "plugin-config:" + generation.InstanceID + ":/token"}}
	candidate, err := hostCandidateFromGeneration(generation, "module-generation-context")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Generation != "module-generation-context" || candidate.ProviderGenerationID != generation.ID || candidate.AgentID != generation.Target.ID || candidate.Revision != generation.Revision || candidate.InstanceID != generation.InstanceID || candidate.PackageDigest != generation.PackageDigest || candidate.Artifact.SHA256 != generation.Artifact.SHA256 {
		t.Fatalf("host candidate identity = %+v, generation = %+v", candidate, generation)
	}
	if len(candidate.SecretHandles) != 1 || candidate.SecretHandles[0] != generation.SecretHandles[0] {
		t.Fatalf("host candidate secret fence = %+v", candidate.SecretHandles)
	}
	generation.SecretHandles[0].ID = "mutated"
	if candidate.SecretHandles[0].ID != "secret-a" {
		t.Fatal("host candidate secret handles alias the snapshot projection")
	}
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

func TestRPCGenerationRequiredPrepareFailureCleansCandidateWithoutPublishing(t *testing.T) {
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
	next := rpcSnapshotWithRequiredInstance(t, 2, []model.PluginGeneration{generation}, generation.InstanceID)
	_, err = moduleUnderTest.Prepare(t.Context(), module.ApplyRequest{Next: next})
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

func TestRPCGenerationRequiredReadinessFailureAbortsPublication(t *testing.T) {
	root := t.TempDir()
	generation := rpcPluginGenerationForTest(t, root, 23, "operation-23")
	runner := &generationControlledRunner{}
	client := &generationLifecycleClient{}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return client, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	next := rpcSnapshotWithRequiredInstance(t, 23, []model.PluginGeneration{generation}, generation.InstanceID)
	prepared, err := NewGenerationModule(host).Prepare(t.Context(), module.ApplyRequest{Next: next})
	if err != nil {
		t.Fatal(err)
	}
	transaction := prepared.(*generationTransaction)
	runner.Fail(context.DeadlineExceeded)
	select {
	case <-transaction.candidates[0].instance.attempt.handle.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("controlled required candidate did not reach failed readiness")
	}
	if err := transaction.Ready(t.Context()); err == nil {
		t.Fatal("Ready() accepted required provider failure")
	}
	if err := transaction.Destroy(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, active := host.Active(generation.InstanceID); active {
		t.Fatal("required readiness failure became active")
	}
}

func TestRPCGenerationRequiredActivationFailureAbortsPublication(t *testing.T) {
	root := t.TempDir()
	generation := rpcPluginGenerationForTest(t, root, 24, "operation-24")
	client := &generationLifecycleClient{activateErr: context.DeadlineExceeded}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return client, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	next := rpcSnapshotWithRequiredInstance(t, 24, []model.PluginGeneration{generation}, generation.InstanceID)
	prepared, err := NewGenerationModule(host).Prepare(t.Context(), module.ApplyRequest{Next: next})
	if err != nil {
		t.Fatal(err)
	}
	transaction := prepared.(*generationTransaction)
	if err := transaction.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := transaction.PrepareGenerationPublication(t.Context()); err == nil {
		t.Fatal("PrepareGenerationPublication() accepted required activation failure")
	}
	if err := transaction.Destroy(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, active := host.Active(generation.InstanceID); active {
		t.Fatal("required activation failure became active")
	}
}

func TestRPCGenerationOptionalPrepareFailurePublishesFencedDegradedStatus(t *testing.T) {
	root := t.TempDir()
	generation := rpcPluginGenerationForTest(t, root, 20, "operation-20")
	client := &generationLifecycleClient{prepareErr: context.DeadlineExceeded}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return client, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := NewGenerationModule(host).Prepare(t.Context(), module.ApplyRequest{Next: model.Snapshot{Revision: 20, PluginGenerations: []model.PluginGeneration{generation}}})
	if err != nil {
		t.Fatalf("optional Prepare() error = %v", err)
	}
	transaction := prepared.(*generationTransaction)
	if err := transaction.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := transaction.PrepareGenerationPublication(t.Context()); err != nil {
		t.Fatal(err)
	}
	transaction.FinalizeGenerationPublication()
	statuses := transaction.PluginRuntimeStatuses()
	assertOptionalFailureStatus(t, statuses, generation, "degraded", "rpc_prepare_failed")
	if _, active := host.Active(generation.InstanceID); active {
		t.Fatal("optional prepare failure became active")
	}
	host.mu.RLock()
	pending := len(host.pending)
	host.mu.RUnlock()
	if pending != 0 {
		t.Fatalf("optional prepare failure pending ownership = %d", pending)
	}
}

func TestRPCGenerationOptionalPrepareFailureDoesNotShiftSiblingIdentity(t *testing.T) {
	root := t.TempDir()
	failed := rpcPluginGenerationForTest(t, root, 22, "operation-22a")
	failed.InstanceID = "instance-failed"
	healthy := rpcPluginGenerationForTest(t, root, 22, "operation-22b")
	healthy.InstanceID = "instance-healthy"
	clients := []LifecycleClient{
		&generationLifecycleClient{prepareErr: context.DeadlineExceeded},
		&generationLifecycleClient{},
	}
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
	prepared, err := NewGenerationModule(host).Prepare(t.Context(), module.ApplyRequest{Next: model.Snapshot{Revision: 22, PluginGenerations: []model.PluginGeneration{failed, healthy}}})
	if err != nil {
		t.Fatal(err)
	}
	transaction := prepared.(*generationTransaction)
	if err := transaction.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := transaction.PrepareGenerationPublication(t.Context()); err != nil {
		t.Fatal(err)
	}
	transaction.FinalizeGenerationPublication()
	statuses := transaction.PluginRuntimeStatuses()
	if len(statuses) != 2 || statuses[0].InstanceID != failed.InstanceID || statuses[0].GenerationID != failed.ID || statuses[0].State != "degraded" ||
		statuses[1].InstanceID != healthy.InstanceID || statuses[1].GenerationID != healthy.ID || statuses[1].OperationID != healthy.OperationID || statuses[1].State != "active" {
		t.Fatalf("runtime statuses shifted across optional failure = %+v", statuses)
	}
	if _, active := host.Active(failed.InstanceID); active {
		t.Fatal("failed optional instance became active")
	}
	if _, active := host.Active(healthy.InstanceID); !active {
		t.Fatal("healthy sibling was not published")
	}
	if err := transaction.Destroy(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRPCGenerationOptionalReadinessFailureIsCleanedAndExcluded(t *testing.T) {
	root := t.TempDir()
	generation := rpcPluginGenerationForTest(t, root, 21, "operation-21")
	runner := &generationControlledRunner{}
	client := &generationLifecycleClient{}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(runner, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return client, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := NewGenerationModule(host).Prepare(t.Context(), module.ApplyRequest{Next: model.Snapshot{Revision: 21, PluginGenerations: []model.PluginGeneration{generation}}})
	if err != nil {
		t.Fatal(err)
	}
	transaction := prepared.(*generationTransaction)
	runner.Fail(context.DeadlineExceeded)
	instance := transaction.candidates[0].instance
	select {
	case <-instance.attempt.handle.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("controlled candidate did not reach failed readiness")
	}
	if err := transaction.Ready(t.Context()); err != nil {
		t.Fatalf("optional Ready() error = %v", err)
	}
	if err := transaction.PrepareGenerationPublication(t.Context()); err != nil {
		t.Fatal(err)
	}
	transaction.FinalizeGenerationPublication()
	assertOptionalFailureStatus(t, transaction.PluginRuntimeStatuses(), generation, "degraded", "rpc_readiness_failed")
	if _, active := host.Active(generation.InstanceID); active {
		t.Fatal("optional readiness failure became active")
	}
}

func TestRPCGenerationOptionalActivationFailurePublishesHealthySibling(t *testing.T) {
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
	registry := module.NewRegistry()
	if err := registry.Register(generationCoreTestModule{}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(moduleUnderTest); err != nil {
		t.Fatal(err)
	}
	generationContext, err := module.NewGenerationContext(model.Snapshot{}, model.Snapshot{Revision: 3, PluginGenerations: []model.PluginGeneration{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := registry.PrepareGeneration(t.Context(), generationContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	publication, ok := prepared.(interface{ PreparePublication(context.Context) error })
	if !ok {
		t.Fatal("prepared generation has no publication barrier")
	}
	if err := publication.PreparePublication(t.Context()); err != nil {
		t.Fatalf("PrepareGenerationPublication() error = %v", err)
	}
	active, _ := prepared.Publish()
	if coreRevision, ok := active.Resolve(generationCoreTestProvider); !ok || coreRevision != int64(3) {
		t.Fatalf("unrelated core provider = %v/%v, want revision 3", coreRevision, ok)
	}
	if _, active := host.Active(first.InstanceID); !active {
		t.Fatal("healthy sibling was not published")
	}
	if _, active := host.Active(second.InstanceID); active {
		t.Fatal("failed sibling became active")
	}
	statuses := active.PluginRuntimeStatuses()
	if len(statuses) != 2 || statuses[0].InstanceID != first.InstanceID || statuses[0].State != "active" ||
		statuses[1].InstanceID != second.InstanceID || statuses[1].State != "degraded" || statuses[1].ErrorCode != "rpc_activation_failed" || statuses[1].Sequence != 1 {
		t.Fatalf("runtime statuses = %+v", statuses)
	}
	if _, _, _, stops := firstClient.counts(); stops != 0 {
		t.Fatalf("healthy sibling stops = %d before generation drain", stops)
	}
	if _, _, _, stops := secondClient.counts(); stops != 1 {
		t.Fatalf("failed optional sibling stops = %d, want isolated cleanup", stops)
	}
	if err := active.Destroy(t.Context()); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if _, _, _, stops := firstClient.counts(); stops != 1 {
		t.Fatalf("healthy sibling stops after generation drain = %d", stops)
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

func rpcSnapshotWithRequiredInstance(t *testing.T, revision int64, generations []model.PluginGeneration, instanceID string) model.Snapshot {
	t.Helper()
	var provider model.PluginGeneration
	for _, generation := range generations {
		if generation.InstanceID == instanceID {
			provider = generation
			break
		}
	}
	snapshot := model.Snapshot{
		Revision:            revision,
		Rules:               []model.HTTPRule{{ID: 1, AgentID: provider.Target.ID, Enabled: true}},
		L4Rules:             []model.L4Rule{},
		RelayListeners:      []model.RelayListener{},
		EgressProfiles:      []model.EgressProfile{},
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
		PluginGenerations:   generations,
		PluginDependencies: []model.PluginDependencyEdge{{
			Consumer: model.PluginDependencyConsumer{Kind: "http_rule", ID: "1", ResourceGroupID: provider.Target.ResourceGroupID, Version: strings.Repeat("e", 64)}, ProviderInstanceID: instanceID,
			Target: model.PluginDependencyTarget{AgentID: provider.Target.ID, ResourceGroupID: provider.Target.ResourceGroupID, Version: provider.Target.Version},
		}},
		PluginPolicies: []model.PluginPolicy{},
	}
	wire, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"plugin_dependencies":[{"consumer":{"kind":"http_rule","id":"1","resource_group_id":"`+provider.Target.ResourceGroupID+`","version":"`+strings.Repeat("e", 64)+`"},"provider_instance_id":"`+instanceID+`"`) {
		t.Fatalf("backend-realistic dependency wire = %s", wire)
	}
	var decoded model.Snapshot
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.HasFullRevisionPayload() {
		t.Fatalf("backend-realistic dependency snapshot is incomplete: %s", wire)
	}
	return decoded
}

func assertOptionalFailureStatus(t *testing.T, statuses []model.PluginRuntimeStatus, generation model.PluginGeneration, state, errorCode string) {
	t.Helper()
	if len(statuses) != 1 {
		t.Fatalf("runtime statuses = %+v", statuses)
	}
	status := statuses[0]
	if status.InstanceID != generation.InstanceID || status.PluginID != generation.PluginID || status.OperationID != generation.OperationID ||
		status.Revision != generation.Revision || status.GenerationID != generation.ID || status.PackageDigest != generation.PackageDigest ||
		status.ArtifactDigest != generation.Artifact.SHA256 || status.ConfigVersion != generation.ConfigVersion || status.RuntimeKind != generation.Runtime.Kind ||
		status.Sequence != 1 || status.State != state || status.ErrorCode != errorCode {
		t.Fatalf("runtime status = %+v", status)
	}
}

const generationCoreTestProvider module.ProviderRef = "test.core.runtime"

type generationCoreTestModule struct{}

func (generationCoreTestModule) Name() string { return "core-test" }
func (generationCoreTestModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "core-test", Provides: []module.ProviderRef{generationCoreTestProvider}}
}
func (generationCoreTestModule) RegisterProviders(registry module.ProviderRegistry) error {
	return registry.Provide(generationCoreTestProvider, int64(0))
}
func (generationCoreTestModule) Capabilities(module.SnapshotView) []module.Capability { return nil }
func (generationCoreTestModule) Apply(context.Context, module.ApplyRequest) error     { return nil }
func (generationCoreTestModule) Stop(context.Context) error                           { return nil }
func (generationCoreTestModule) Prepare(_ context.Context, request module.ApplyRequest) (module.ModuleTransaction, error) {
	return generationCoreTestTransaction{revision: request.Next.Revision}, nil
}

type generationCoreTestTransaction struct{ revision int64 }

func (transaction generationCoreTestTransaction) RegisterProviders(registry module.ProviderRegistry) error {
	return registry.Provide(generationCoreTestProvider, transaction.revision)
}
func (generationCoreTestTransaction) Ready(context.Context) error   { return nil }
func (generationCoreTestTransaction) Destroy(context.Context) error { return nil }
func (generationCoreTestTransaction) Commit() error                 { return nil }
func (generationCoreTestTransaction) Rollback() error               { return nil }
