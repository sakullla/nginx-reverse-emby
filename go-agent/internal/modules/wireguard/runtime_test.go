package wireguard

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestRuntimePrepareCommitAndRollback(t *testing.T) {
	t.Parallel()

	factory := &moduleRecordingFactory{}
	runtime := NewRuntime(factory.Create)
	defer runtime.Close()

	initial := testWireGuardProfile(7, "local-agent", "peer.example.com:51820", "10.10.0.0/24")
	updated := initial
	updated.Peers = append([]model.WireGuardPeer(nil), initial.Peers...)
	updated.Peers[0].Endpoint = "peer.example.com:51821"

	if err := runtime.Apply(context.Background(), []model.WireGuardProfile{initial}); err != nil {
		t.Fatalf("Apply(initial) error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1", len(factory.created))
	}

	got, ok := runtime.RuntimeForAgent("local-agent", initial.ID)
	if !ok || got != factory.created[0] {
		t.Fatalf("RuntimeForAgent(initial) = %v, %v; want initial runtime", got, ok)
	}

	tx, err := runtime.Prepare(context.Background(), []model.WireGuardProfile{updated})
	if err != nil {
		t.Fatalf("Prepare(updated) error = %v", err)
	}
	if len(factory.created) != 2 {
		t.Fatalf("created runtimes = %d, want 2", len(factory.created))
	}

	candidate, ok := tx.RuntimeForAgent("local-agent", updated.ID)
	if !ok || candidate != factory.created[1] {
		t.Fatalf("transaction provider runtime = %v, %v; want candidate runtime", candidate, ok)
	}

	tx.Rollback()
	if !factory.created[1].closed {
		t.Fatal("candidate runtime was not closed on rollback")
	}
	got, ok = runtime.RuntimeForAgent("local-agent", initial.ID)
	if !ok || got != factory.created[0] {
		t.Fatalf("RuntimeForAgent(after rollback) = %v, %v; want original runtime", got, ok)
	}

	tx, err = runtime.Prepare(context.Background(), []model.WireGuardProfile{updated})
	if err != nil {
		t.Fatalf("Prepare(updated second time) error = %v", err)
	}
	if len(factory.created) != 3 {
		t.Fatalf("created runtimes = %d, want 3", len(factory.created))
	}

	runtime.Commit(tx, []model.WireGuardProfile{updated})
	if !factory.created[0].closed {
		t.Fatal("original runtime was not closed on commit")
	}
	got, ok = runtime.RuntimeForAgent("local-agent", updated.ID)
	if !ok || got != factory.created[2] {
		t.Fatalf("RuntimeForAgent(after commit) = %v, %v; want committed runtime", got, ok)
	}
}

func TestRuntimeProviderFiltersByLocalAgentID(t *testing.T) {
	t.Parallel()

	factory := &moduleRecordingFactory{}
	runtime := NewRuntime(factory.Create)
	defer runtime.Close()

	local := testWireGuardProfile(7, "local-agent", "peer.example.com:51820", "10.10.0.0/24")
	remote := testWireGuardProfile(7, "remote-agent", "peer.example.com:51820", "10.20.0.0/24")

	if err := runtime.Apply(context.Background(), []model.WireGuardProfile{local, remote}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got, ok := runtime.Runtime(local.ID); ok || got != nil {
		t.Fatalf("Runtime(unfiltered) = %v, %v; want ambiguous miss", got, ok)
	}
	got, ok := runtime.RuntimeForAgent("local-agent", local.ID)
	if !ok || got != factory.created[0] {
		t.Fatalf("RuntimeForAgent(local) = %v, %v; want local runtime", got, ok)
	}

	gotProvider, ok := runtime.RuntimeForAgent("local-agent", local.ID)
	if !ok || gotProvider != factory.created[0] {
		t.Fatalf("RuntimeForAgent(local) = %v, %v; want local runtime", gotProvider, ok)
	}
}

func TestRuntimeCloseDelegatesToUnderlyingManager(t *testing.T) {
	t.Parallel()

	factory := &moduleRecordingFactory{}
	runtime := NewRuntime(factory.Create)

	if err := runtime.Apply(context.Background(), []model.WireGuardProfile{
		testWireGuardProfile(7, "local-agent", "peer.example.com:51820", "10.10.0.0/24"),
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !factory.created[0].closed {
		t.Fatal("underlying runtime was not closed")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if factory.created[0].closeCount != 1 {
		t.Fatalf("underlying close count = %d, want 1", factory.created[0].closeCount)
	}
}

func TestModuleExposesWireGuardCapabilityAndDelegatesLifecycle(t *testing.T) {
	t.Parallel()

	factory := &moduleRecordingFactory{}
	runtime := NewRuntime(factory.Create)
	mod := NewModule(runtime)

	caps := mod.Capabilities(model.Snapshot{})
	if len(caps) != 1 || caps[0].Name != "wireguard" || !caps[0].Enabled {
		t.Fatalf("Capabilities() = %+v, want wireguard capability", caps)
	}

	snapshot := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(7, "local-agent", "peer.example.com:51820", "10.10.0.0/24"),
	}}
	if err := mod.Apply(context.Background(), module.ApplyRequest{Next: snapshot}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1", len(factory.created))
	}
	if err := mod.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !factory.created[0].closed {
		t.Fatal("module stop did not close the runtime")
	}
}

func TestModulePublishesOverlayRuntimeProvider(t *testing.T) {
	t.Parallel()

	factory := &moduleRecordingFactory{}
	runtime := NewRuntime(factory.Create)
	mod := NewModule(runtime)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	next := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(9, "local", "peer.example.com:51820", "127.0.0.1/32"),
	}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	provider, ok := registry.Resolve(module.ProviderOverlayRuntime)
	if !ok {
		t.Fatal("overlay.runtime provider missing")
	}
	overlay, ok := provider.(module.OverlayRuntime)
	if !ok {
		t.Fatalf("overlay provider type = %T, want module.OverlayRuntime", provider)
	}
	if _, err := overlay.DialContext(context.Background(), "local", 9, "tcp", "127.0.0.1:80"); err != errRecordingRuntimeDial {
		t.Fatalf("DialContext() error = %v, want %v", err, errRecordingRuntimeDial)
	}
}

func TestCommittedProviderDoesNotExposePendingCandidateDuringPrepare(t *testing.T) {
	factory := &moduleRecordingFactory{}
	runtime := NewRuntime(factory.Create)
	mod := NewModule(runtime)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	previous := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(30, "local", "peer.example.com:51820", "127.0.0.1/32"),
	}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	provider, ok := registry.Resolve(module.ProviderOverlayRuntime)
	if !ok {
		t.Fatal("overlay.runtime provider missing")
	}
	overlay, ok := provider.(module.OverlayRuntime)
	if !ok {
		t.Fatalf("overlay provider type = %T, want module.OverlayRuntime", provider)
	}

	next := previous
	next.WireGuardProfiles = []model.WireGuardProfile{
		testWireGuardProfile(30, "local", "peer.example.com:51821", "127.0.0.2/32"),
	}
	tx, err := mod.Prepare(context.Background(), module.ApplyRequest{Previous: previous, Next: next})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer tx.Rollback()
	if len(factory.created) != 2 {
		t.Fatalf("created runtimes = %d, want committed plus candidate", len(factory.created))
	}

	if _, err := overlay.DialContext(context.Background(), "local", 30, "tcp", "127.0.0.1:80"); err != errRecordingRuntimeDial {
		t.Fatalf("DialContext() error = %v, want %v", err, errRecordingRuntimeDial)
	}
	if factory.created[0].dialCount != 1 {
		t.Fatalf("committed runtime dial count = %d, want 1", factory.created[0].dialCount)
	}
	if factory.created[1].dialCount != 0 {
		t.Fatalf("candidate runtime dial count = %d, want 0", factory.created[1].dialCount)
	}
}

func TestCommittedProviderDoesNotExposeCloseFirstCandidateDuringPrepare(t *testing.T) {
	factory := &closeFirstRecordingFactory{}
	runtime := &Runtime{manager: NewManager(ManagerOptions{
		Factory:   factory.Create,
		Preflight: func(context.Context, Config) error { return nil },
	})}
	mod := NewModule(runtime)
	committedProviders := testProviderRegistry{}
	if err := mod.RegisterProviders(committedProviders); err != nil {
		t.Fatalf("RegisterProviders() error = %v", err)
	}
	committedOverlay, ok := committedProviders[module.ProviderOverlayRuntime].(module.OverlayRuntime)
	if !ok {
		t.Fatalf("committed overlay provider type = %T, want module.OverlayRuntime", committedProviders[module.ProviderOverlayRuntime])
	}

	previousProfile := testWireGuardProfile(33, "local", "peer.example.com:51820", "127.0.0.1/32")
	previousProfile.ListenPort = 51933
	previous := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{previousProfile}}
	if err := mod.Apply(context.Background(), module.ApplyRequest{Next: previous}); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes after initial apply = %d, want 1", len(factory.created))
	}

	nextProfile := testWireGuardProfile(33, "local", "peer.example.com:51821", "127.0.0.2/32")
	nextProfile.ListenPort = previousProfile.ListenPort
	next := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{nextProfile}}
	tx, err := mod.Prepare(context.Background(), module.ApplyRequest{Previous: previous, Next: next})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer tx.Rollback()
	if len(factory.created) != 2 {
		t.Fatalf("created runtimes after close-first prepare = %d, want committed plus candidate", len(factory.created))
	}

	_, _ = committedOverlay.DialContext(context.Background(), "local", 33, "tcp", "127.0.0.1:80")
	if factory.created[1].dialCount != 0 {
		t.Fatalf("candidate runtime dial count through committed provider = %d, want 0", factory.created[1].dialCount)
	}

	pendingProviders := testProviderRegistry{}
	providerTx, ok := tx.(interface {
		RegisterProviders(module.ProviderRegistry) error
	})
	if !ok {
		t.Fatal("wireguard transaction does not register transaction-local providers")
	}
	if err := providerTx.RegisterProviders(pendingProviders); err != nil {
		t.Fatalf("transaction RegisterProviders() error = %v", err)
	}
	pendingOverlay, ok := pendingProviders[module.ProviderOverlayRuntime].(module.OverlayRuntime)
	if !ok {
		t.Fatalf("pending overlay provider type = %T, want module.OverlayRuntime", pendingProviders[module.ProviderOverlayRuntime])
	}
	if _, err := pendingOverlay.DialContext(context.Background(), "local", 33, "tcp", "127.0.0.1:80"); err != errRecordingRuntimeDial {
		t.Fatalf("pending overlay DialContext() error = %v, want %v", err, errRecordingRuntimeDial)
	}
	if factory.created[1].dialCount != 1 {
		t.Fatalf("candidate runtime dial count through pending provider = %d, want 1", factory.created[1].dialCount)
	}
}

func TestTransactionLocalProviderExposesCandidateDuringRegistryApply(t *testing.T) {
	factory := &moduleRecordingFactory{}
	registry := module.NewRegistry()
	mustRegister(t, registry, NewModule(NewRuntime(factory.Create)))
	mustRegister(t, registry, &overlayPreparingConsumerModule{profileID: 31, agentID: "local"})

	next := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(31, "local", "peer.example.com:51820", "127.0.0.1/32"),
	}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1 candidate", len(factory.created))
	}
	if factory.created[0].dialCount != 1 {
		t.Fatalf("candidate runtime dial count = %d, want 1", factory.created[0].dialCount)
	}
}

func TestLaterCommitFailureDoesNotExposeCandidateThroughCommittedProvider(t *testing.T) {
	factory := &moduleRecordingFactory{}
	registry := module.NewRegistry()
	mustRegister(t, registry, NewModule(NewRuntime(factory.Create)))

	previous := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(32, "local", "peer.example.com:51820", "127.0.0.1/32"),
	}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	provider, ok := registry.Resolve(module.ProviderOverlayRuntime)
	if !ok {
		t.Fatal("overlay.runtime provider missing")
	}
	overlay, ok := provider.(module.OverlayRuntime)
	if !ok {
		t.Fatalf("overlay provider type = %T, want module.OverlayRuntime", provider)
	}
	failErr := errors.New("later commit failed")
	mustRegister(t, registry, committedProviderProbeFailingModule{
		name:      "later",
		err:       failErr,
		provider:  overlay,
		agentID:   "local",
		profileID: 32,
	})

	next := previous
	next.WireGuardProfiles = []model.WireGuardProfile{
		testWireGuardProfile(32, "local", "peer.example.com:51821", "127.0.0.2/32"),
	}
	err := registry.Apply(context.Background(), previous, next)
	if !errors.Is(err, failErr) {
		t.Fatalf("Apply() error = %v, want later commit failure", err)
	}
	if len(factory.created) != 2 {
		t.Fatalf("created runtimes = %d, want committed plus candidate", len(factory.created))
	}
	if factory.created[0].dialCount != 1 {
		t.Fatalf("committed runtime dial count = %d, want 1", factory.created[0].dialCount)
	}
	if factory.created[1].dialCount != 0 {
		t.Fatalf("candidate runtime dial count = %d, want 0", factory.created[1].dialCount)
	}
}

func TestModuleStateDoesNotAdvanceWhenLaterModuleApplyFails(t *testing.T) {
	factory := &moduleRecordingFactory{}
	runtime := NewRuntime(factory.Create)
	mod := NewModule(runtime)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)
	mustRegister(t, registry, failingModule{name: "later", err: errors.New("later apply failed")})

	next := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(10, "local", "peer.example.com:51820", "127.0.0.1/32"),
	}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err == nil {
		t.Fatal("Apply() error = nil, want later module failure")
	}
	if got, ok := runtime.RuntimeForAgent("local", 10); ok || got != nil {
		t.Fatalf("RuntimeForAgent() = %v, %v; want state not advanced after rollback", got, ok)
	}
	if len(factory.created) != 1 {
		t.Fatalf("created runtimes = %d, want 1 prepared runtime", len(factory.created))
	}
	if !factory.created[0].closed {
		t.Fatal("prepared runtime was not closed on rollback")
	}
}

func TestModuleRollbackAfterCommitRestoresPreviousRuntime(t *testing.T) {
	factory := &moduleRecordingFactory{}
	runtime := NewRuntime(factory.Create)
	mod := NewModule(runtime)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	previous := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(20, "local", "peer.example.com:51820", "127.0.0.1/32"),
	}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}
	original, ok := runtime.RuntimeForAgent("local", 20)
	if !ok {
		t.Fatal("initial runtime missing")
	}

	failErr := errors.New("later commit failed")
	mustRegister(t, registry, commitFailingModule{name: "later-transaction", err: failErr})
	next := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(21, "local", "peer.example.com:51821", "127.0.0.2/32"),
	}}
	err := registry.Apply(context.Background(), previous, next)
	if !errors.Is(err, failErr) {
		t.Fatalf("Apply() error = %v, want later commit failure", err)
	}

	restored, ok := runtime.RuntimeForAgent("local", 20)
	if !ok {
		t.Fatal("previous runtime missing after committed rollback")
	}
	if restored != original {
		t.Fatal("rollback replaced the live original runtime instead of preserving previous runtime")
	}
	if got, ok := runtime.RuntimeForAgent("local", 21); ok || got != nil {
		t.Fatalf("next runtime remained after committed rollback: %v, %v", got, ok)
	}
	profiles := runtime.Profiles()
	if len(profiles) != 1 || profiles[0].ID != 20 {
		t.Fatalf("profiles after committed rollback = %+v, want previous profile", profiles)
	}
}

func TestModulePublishesTransparentListenerProvider(t *testing.T) {
	t.Parallel()

	factory := &moduleRecordingFactory{}
	runtime := NewRuntime(factory.Create)
	mod := NewModule(runtime)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	next := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{
		testWireGuardProfile(9, "local", "peer.example.com:51820", "127.0.0.1/32"),
	}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	provider, ok := registry.Resolve(module.ProviderTransparentListener)
	if !ok {
		t.Fatal("transparent.listener provider missing")
	}
	listener, ok := provider.(module.TransparentListener)
	if !ok {
		t.Fatalf("transparent listener provider type = %T, want module.TransparentListener", provider)
	}
	if _, err := listener.ListenTransparentTCP(context.Background(), "local", 9); err != errRecordingTransparentTCP {
		t.Fatalf("ListenTransparentTCP() error = %v, want %v", err, errRecordingTransparentTCP)
	}
	udpConn, err := listener.ListenTransparentUDP(context.Background(), "local", 9, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenTransparentUDP() error = %v", err)
	}
	packet, err := udpConn.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	if packet.OriginalDst != "127.0.0.1:53" || string(packet.Payload) != "payload" {
		t.Fatalf("ReadPacket() = %+v, want original dst and payload", packet)
	}
	if err := udpConn.WritePacket([]byte("reply"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53}, "127.0.0.1:5300"); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}
	if !reflect.DeepEqual(factory.created[0].udp.writes, [][]byte{[]byte("reply")}) {
		t.Fatalf("udp writes = %#v, want reply", factory.created[0].udp.writes)
	}
}

func mustRegister(t *testing.T, registry *module.Registry, mod module.Module) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
}

type failingModule struct {
	name string
	err  error
}

func (m failingModule) Name() string { return m.name }

func (m failingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (failingModule) RegisterProviders(module.ProviderRegistry) error { return nil }
func (failingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (m failingModule) Apply(context.Context, module.ApplyRequest) error { return m.err }
func (failingModule) Stop(context.Context) error                         { return nil }

type commitFailingModule struct {
	name string
	err  error
}

func (m commitFailingModule) Name() string { return m.name }

func (m commitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (commitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }
func (commitFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (commitFailingModule) Apply(context.Context, module.ApplyRequest) error { return nil }
func (m commitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}
func (commitFailingModule) Stop(context.Context) error { return nil }

type overlayPreparingConsumerModule struct {
	profileID int
	agentID   string
}

func (*overlayPreparingConsumerModule) Name() string { return "overlay-consumer" }

func (m *overlayPreparingConsumerModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.Name(), Requires: []module.ProviderRef{module.ProviderOverlayRuntime}}
}

func (*overlayPreparingConsumerModule) RegisterProviders(module.ProviderRegistry) error { return nil }
func (*overlayPreparingConsumerModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (*overlayPreparingConsumerModule) Apply(context.Context, module.ApplyRequest) error { return nil }
func (*overlayPreparingConsumerModule) Stop(context.Context) error                       { return nil }

func (m *overlayPreparingConsumerModule) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	provider, ok := req.Providers.Resolve(module.ProviderOverlayRuntime)
	if !ok {
		return nil, errors.New("overlay.runtime provider missing")
	}
	overlay, ok := provider.(module.OverlayRuntime)
	if !ok {
		return nil, errors.New("overlay.runtime provider has wrong type")
	}
	_, err := overlay.DialContext(ctx, m.agentID, m.profileID, "tcp", "127.0.0.1:80")
	if !errors.Is(err, errRecordingRuntimeDial) {
		return nil, err
	}
	return module.TransactionFuncs{}, nil
}

type committedProviderProbeFailingModule struct {
	name      string
	err       error
	provider  module.OverlayRuntime
	agentID   string
	profileID int
}

func (m committedProviderProbeFailingModule) Name() string { return m.name }

func (m committedProviderProbeFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (committedProviderProbeFailingModule) RegisterProviders(module.ProviderRegistry) error {
	return nil
}
func (committedProviderProbeFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (committedProviderProbeFailingModule) Apply(context.Context, module.ApplyRequest) error {
	return nil
}
func (m committedProviderProbeFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{
		CommitFunc: func() error {
			_, _ = m.provider.DialContext(context.Background(), m.agentID, m.profileID, "tcp", "127.0.0.1:80")
			return m.err
		},
	}, nil
}
func (committedProviderProbeFailingModule) Stop(context.Context) error { return nil }

type testProviderRegistry map[module.ProviderRef]any

func (r testProviderRegistry) Provide(ref module.ProviderRef, provider any) error {
	r[ref] = provider
	return nil
}

func testWireGuardProfile(id int, agentID string, endpoint string, allowedIPs ...string) model.WireGuardProfile {
	return model.WireGuardProfile{
		ID:         id,
		AgentID:    agentID,
		Name:       "wireguard",
		Mode:       ModeGenericWireGuard,
		PrivateKey: wireGuardTestKey,
		Addresses:  []string{"10.10.0.2/32"},
		Peers: []model.WireGuardPeer{{
			Name:       "peer",
			PublicKey:  wireGuardTestKey,
			Endpoint:   endpoint,
			AllowedIPs: append([]string(nil), allowedIPs...),
			Reserved:   nil,
		}},
		Enabled: true,
	}
}

type moduleRecordingFactory struct {
	created []*recordingRuntime
}

func (f *moduleRecordingFactory) Create(context.Context, Config) (RuntimeHandle, error) {
	runtime := &recordingRuntime{}
	f.created = append(f.created, runtime)
	return runtime, nil
}

type closeFirstRecordingFactory struct {
	created           []*recordingRuntime
	failedReplacement bool
}

func (f *closeFirstRecordingFactory) Create(_ context.Context, cfg Config) (RuntimeHandle, error) {
	if cfg.ID == 33 && cfg.Peers[0].EndpointPort == 51821 && !f.failedReplacement {
		f.failedReplacement = true
		return nil, errors.New("address already in use")
	}
	runtime := &recordingRuntime{}
	f.created = append(f.created, runtime)
	return runtime, nil
}

type recordingRuntime struct {
	closed     bool
	closeCount int
	dialCount  int
	udp        *recordingTransparentUDPConn
}

var errRecordingRuntimeDial = errors.New("recording dial")
var errRecordingTransparentTCP = errors.New("recording transparent tcp")

func (r *recordingRuntime) DialContext(context.Context, string, string) (net.Conn, error) {
	r.dialCount++
	return nil, errRecordingRuntimeDial
}

func (r *recordingRuntime) ListenTCP(context.Context, string) (net.Listener, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, errRecordingTransparentTCP
}

func (r *recordingRuntime) ListenUDP(context.Context, string) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

func (r *recordingRuntime) ListenTransparentUDP(context.Context, string) (TransparentUDPConn, error) {
	r.udp = &recordingTransparentUDPConn{
		packets: []TransparentUDPPacket{{
			Peer:        &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53},
			OriginalDst: "127.0.0.1:53",
			Payload:     []byte("payload"),
		}},
	}
	return r.udp, nil
}

func (r *recordingRuntime) Close() error {
	r.closeCount++
	r.closed = true
	return nil
}

const wireGuardTestKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

type recordingTransparentUDPConn struct {
	packets []TransparentUDPPacket
	writes  [][]byte
}

func (c *recordingTransparentUDPConn) Close() error { return nil }

func (c *recordingTransparentUDPConn) LocalAddr() net.Addr { return &net.UDPAddr{} }

func (c *recordingTransparentUDPConn) ReadPacket() (TransparentUDPPacket, error) {
	if len(c.packets) == 0 {
		return TransparentUDPPacket{}, io.EOF
	}
	packet := c.packets[0]
	c.packets = c.packets[1:]
	return packet, nil
}

func (c *recordingTransparentUDPConn) WritePacket(payload []byte, peer *net.UDPAddr, source string) error {
	c.writes = append(c.writes, append([]byte(nil), payload...))
	return nil
}
