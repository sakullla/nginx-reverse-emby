//go:build !integration

package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
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
	stopErr                           error
}

type generationTestSandbox struct{}

type generationControlledRunner struct {
	mu      sync.Mutex
	process *hostProcess
}

type generationLogFenceRetirer struct {
	identities []pluginprocess.RuntimeLogIdentity
	intents    map[string][]pluginprocess.RuntimeLogIdentity
	staged     []string
	drained    []string
	aborted    []string
	store      agentcore.PluginLogRetirementIntentStore
	err        error
	markErrors []error
}

func (retirer *generationLogFenceRetirer) MarkPluginRuntimeLogRetirementIntentDrained(id string) error {
	if len(retirer.markErrors) > 0 {
		err := retirer.markErrors[0]
		retirer.markErrors = retirer.markErrors[1:]
		if err != nil {
			return err
		}
	}
	if retirer.store != nil {
		if err := retirer.store.MarkPluginRuntimeLogRetirementIntentDrained(id); err != nil {
			return err
		}
	}
	retirer.drained = append(retirer.drained, id)
	return retirer.err
}

func (retirer *generationLogFenceRetirer) StagePluginRuntimeLogRetirementIntent(id string, revision int64, identities []pluginprocess.RuntimeLogIdentity) error {
	if retirer.intents == nil {
		retirer.intents = make(map[string][]pluginprocess.RuntimeLogIdentity)
	}
	retirer.intents[id] = append([]pluginprocess.RuntimeLogIdentity(nil), identities...)
	retirer.staged = append(retirer.staged, id)
	if retirer.store != nil {
		if err := retirer.store.StagePluginRuntimeLogRetirementIntent(id, revision, identities); err != nil {
			return err
		}
	}
	return retirer.err
}

func (retirer *generationLogFenceRetirer) CompletePluginRuntimeLogRetirementIntent(id string) error {
	if retirer.err != nil {
		return retirer.err
	}
	if retirer.store != nil {
		if err := retirer.store.CompletePluginRuntimeLogRetirementIntent(id); err != nil {
			return err
		}
	}
	retirer.identities = append(retirer.identities, retirer.intents[id]...)
	delete(retirer.intents, id)
	return nil
}

func (retirer *generationLogFenceRetirer) AbortPluginRuntimeLogRetirementIntent(id string) error {
	retirer.aborted = append(retirer.aborted, id)
	if retirer.store != nil {
		if err := retirer.store.AbortPluginRuntimeLogRetirementIntent(id); err != nil {
			return err
		}
	}
	delete(retirer.intents, id)
	return retirer.err
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
	err := c.stopErr
	c.mu.Unlock()
	if err != nil {
		return pluginsdk.LifecycleResponse{}, err
	}
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
	t.Parallel()
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
	retirer := &generationLogFenceRetirer{}
	moduleUnderTest.SetRuntimeLogFenceRetirer(retirer)
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
	if len(retirer.identities) != 0 {
		t.Fatalf("destroy retired a generation without a semantic removal: %+v", retirer.identities)
	}
}

func TestRPCGenerationRequiredFailureRetryPreservesSameFenceSequence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	generation := rpcPluginGenerationForTest(t, root, 11, "operation-11")
	client := &generationLifecycleClient{prepareErr: context.DeadlineExceeded}
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
		return client, hostCloser{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	store := agentcore.NewInMemory()
	retirer := &generationLogFenceRetirer{store: store}
	moduleUnderTest := NewGenerationModule(host)
	moduleUnderTest.SetRuntimeLogFenceRetirer(retirer)
	draft := rpcGenerationLogDraft(generation, "before required failure")
	first, err := store.EnqueuePluginLogReports(fmt.Sprintf("%064x", 1), []model.PluginRuntimeLogReport{draft})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AcknowledgePluginLogReports(first); err != nil {
		t.Fatal(err)
	}
	snapshot := rpcSnapshotWithRequiredInstance(t, 11, []model.PluginGeneration{generation}, generation.InstanceID)
	if _, err := moduleUnderTest.Prepare(t.Context(), module.ApplyRequest{Previous: snapshot, Next: snapshot}); err == nil {
		t.Fatal("required candidate failure was accepted")
	}
	if len(retirer.identities) != 0 {
		t.Fatalf("failed candidate retired live fence: %+v", retirer.identities)
	}
	client.mu.Lock()
	client.prepareErr = nil
	client.mu.Unlock()
	prepared, err := moduleUnderTest.Prepare(t.Context(), module.ApplyRequest{Previous: snapshot, Next: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	retry := prepared.(*generationTransaction)
	second, err := store.EnqueuePluginLogReports(fmt.Sprintf("%064x", 2), []model.PluginRuntimeLogReport{rpcGenerationLogDraft(generation, "after retry")})
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Sequence != 2 {
		t.Fatalf("same-fence retry sequence = %d", second[0].Sequence)
	}
	if err := retry.Destroy(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(retirer.identities) != 0 {
		t.Fatalf("retry candidate rollback retired live fence: %+v", retirer.identities)
	}
}

func TestRPCGenerationReplacementAndRemovalRetireOldOnlyAfterDrain(t *testing.T) {
	t.Parallel()
	for _, replacement := range []bool{false, true} {
		name := "removal"
		if replacement {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			oldGeneration := rpcPluginGenerationForTest(t, root, 15, "operation-15")
			client := &generationLifecycleClient{}
			host, err := NewHost(pluginprocess.Installer{RuntimeRoot: filepath.Join(root, "runtime")}, pluginprocess.NewSupervisor(hostRunner{}, generationTestSandbox{}, io.Discard), func(context.Context, DialConfig) (LifecycleClient, io.Closer, error) {
				return client, hostCloser{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = host.Close(context.Background()) })
			retirer := &generationLogFenceRetirer{}
			moduleUnderTest := NewGenerationModule(host)
			moduleUnderTest.SetRuntimeLogFenceRetirer(retirer)
			oldSnapshot := model.Snapshot{Revision: 15, PluginGenerations: []model.PluginGeneration{oldGeneration}}
			old := preparePublishRPCGeneration(t, moduleUnderTest, model.Snapshot{}, oldSnapshot)
			nextSnapshot := model.Snapshot{Revision: 16, PluginGenerations: []model.PluginGeneration{}}
			if replacement {
				newGeneration := rpcPluginGenerationForTest(t, root, 16, "operation-16")
				nextSnapshot.PluginGenerations = []model.PluginGeneration{newGeneration}
			}
			next := preparePublishRPCGeneration(t, moduleUnderTest, oldSnapshot, nextSnapshot)
			if len(retirer.identities) != 0 {
				t.Fatalf("old fence retired before drain: %+v", retirer.identities)
			}
			if err := old.Destroy(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(retirer.drained) != 1 || len(retirer.identities) != 0 {
				t.Fatalf("post-drain state = drained:%v completed:%+v", retirer.drained, retirer.identities)
			}
			if err := retirer.CompletePluginRuntimeLogRetirementIntent(retirer.drained[0]); err != nil {
				t.Fatal(err)
			}
			if len(retirer.identities) != 1 || retirer.identities[0] != runtimeLogIdentityFromGeneration(oldGeneration) {
				t.Fatalf("post-cutover retirement = %+v", retirer.identities)
			}
			if err := next.Destroy(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(retirer.identities) != 1 {
				t.Fatalf("active/new fence was retired: %+v", retirer.identities)
			}
		})
	}
}

func TestRPCGenerationRequiredPrepareFailureCleansCandidateWithoutPublishing(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestRPCGenerationOptionalPrepareFailurePublishesFencedDegradedStatus(t *testing.T) {
	t.Parallel()
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

func preparePublishRPCGeneration(t *testing.T, moduleUnderTest *GenerationModule, previous, next model.Snapshot) *generationTransaction {
	t.Helper()
	prepared, err := moduleUnderTest.Prepare(t.Context(), module.ApplyRequest{Previous: previous, Next: next})
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
	return transaction
}

func rpcGenerationLogDraft(generation model.PluginGeneration, message string) model.PluginRuntimeLogReport {
	identity := runtimeLogIdentityFromGeneration(generation)
	return model.PluginRuntimeLogReport{
		Revision: identity.Revision, GenerationID: identity.ProviderGenerationID, InstanceID: identity.InstanceID,
		PluginID: identity.PluginID, AgentID: identity.AgentID, PackageDigest: identity.PackageDigest,
		ArtifactDigest: identity.ArtifactDigest, Entries: []model.PluginRuntimeLogEntry{{Level: "info", Message: message}},
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
