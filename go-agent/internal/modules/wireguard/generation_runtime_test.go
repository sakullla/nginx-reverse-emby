package wireguard

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"golang.zx2c4.com/wireguard/conn"
)

func TestWireGuardGenerationStableBindPublicationAndAssociationPinning(t *testing.T) {
	registry := module.NewRegistry()
	factory := &wireGuardGenerationTestFactory{}
	owner := NewManagedModuleWithConfig(factory.create, ModuleConfig{
		GenerationSelector:     registry,
		SessionRegistrar:       wireGuardGenerationNoopRegistrar{},
		ExternalDrainLifecycle: true,
	})
	if err := registry.Register(owner); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	firstSnapshot := wireGuardGenerationSnapshot(1, 41)
	firstCandidate := prepareWireGuardGenerationCandidate(t, registry, model.Snapshot{}, firstSnapshot)
	firstRuntime := factory.runtimeAt(t, 0)
	firstEndpoint := firstRuntime.endpoint
	remoteOne := wireGuardGenerationEndpoint(31001)
	firstEndpoint.binding.dispatch(wireGuardGenerationInitiation(11), remoteOne)
	assertWireGuardGenerationNoPacket(t, firstEndpoint, "unpublished candidate")

	firstView, retired := firstCandidate.Publish()
	if retired != nil {
		t.Fatal("first publication unexpectedly retired a generation")
	}
	defer firstView.Destroy(context.Background())
	malformed := make([]byte, 8)
	binary.LittleEndian.PutUint32(malformed[:4], wireGuardMessageInitiation)
	firstEndpoint.binding.dispatch(malformed, wireGuardGenerationEndpoint(31999))
	assertWireGuardGenerationNoPacket(t, firstEndpoint, "malformed initiation")
	firstEndpoint.binding.dispatch(wireGuardGenerationInitiation(12), remoteOne)
	assertWireGuardGenerationPacket(t, firstEndpoint, wireGuardMessageInitiation)
	if err := firstEndpoint.Send([][]byte{wireGuardGenerationResponse(21, 12)}, remoteOne); err != nil {
		t.Fatalf("Send(response) error = %v", err)
	}

	secondSnapshot := wireGuardGenerationSnapshot(2, 41)
	secondSnapshot.WireGuardProfiles[0].Peers[0].PersistentKeepaliveSeconds = 15
	secondCandidate := prepareWireGuardGenerationCandidate(t, registry, firstSnapshot, secondSnapshot)
	secondRuntime := factory.runtimeAt(t, 1)
	secondEndpoint := secondRuntime.endpoint
	if firstEndpoint.binding != secondEndpoint.binding {
		t.Fatal("same profile/listen port did not reuse the stable physical bind")
	}
	if firstEndpoint.binding.refs != 2 || firstEndpoint.binding.closed {
		t.Fatalf("stable bind before cutover = refs %d closed %v, want refs 2 open", firstEndpoint.binding.refs, firstEndpoint.binding.closed)
	}

	remoteTwo := wireGuardGenerationEndpoint(31002)
	firstEndpoint.binding.dispatch(wireGuardGenerationInitiation(13), remoteTwo)
	assertWireGuardGenerationPacket(t, firstEndpoint, wireGuardMessageInitiation)
	assertWireGuardGenerationNoPacket(t, secondEndpoint, "unpublished replacement")

	secondView, retired := secondCandidate.Publish()
	defer secondView.Destroy(context.Background())
	if retired != firstView {
		t.Fatal("second publication did not retire the first generation")
	}
	remoteThree := wireGuardGenerationEndpoint(31003)
	firstEndpoint.binding.dispatch(wireGuardGenerationInitiation(14), remoteThree)
	assertWireGuardGenerationPacket(t, secondEndpoint, wireGuardMessageInitiation)

	firstEndpoint.binding.dispatch(wireGuardGenerationInitiation(15), remoteOne)
	assertWireGuardGenerationPacket(t, firstEndpoint, wireGuardMessageInitiation)
	assertWireGuardGenerationNoPacket(t, secondEndpoint, "pinned remote")

	receiverTarget, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer receiverTarget.Close()
	receiverEndpoint := &conn.StdNetEndpoint{AddrPort: receiverTarget.LocalAddr().(*net.UDPAddr).AddrPort()}
	if err := firstEndpoint.Send([][]byte{wireGuardGenerationInitiation(77)}, receiverEndpoint); err != nil {
		t.Fatalf("Send(initiation) error = %v", err)
	}
	firstEndpoint.binding.dispatch(wireGuardGenerationTransport(77), wireGuardGenerationEndpoint(31004))
	assertWireGuardGenerationPacket(t, firstEndpoint, wireGuardMessageTransport)
	assertWireGuardGenerationNoPacket(t, secondEndpoint, "receiver-index pinned packet")
	firstEndpoint.beginDrain()
	drainingRemote := wireGuardGenerationEndpoint(31006)
	if err := firstEndpoint.Send([][]byte{wireGuardGenerationInitiation(88)}, drainingRemote); err == nil || !strings.Contains(err.Error(), "draining") {
		t.Fatalf("draining Send() error = %v, want admission rejection", err)
	}
	if _, ok := firstEndpoint.binding.receivers[88]; ok {
		t.Fatal("draining send retained a rejected receiver-index mapping")
	}
	if _, ok := firstEndpoint.binding.remotes[drainingRemote.DstToString()]; ok {
		t.Fatal("draining send retained a rejected remote mapping")
	}

	if err := firstView.Destroy(context.Background()); err != nil {
		t.Fatalf("Destroy(first) error = %v", err)
	}
	if firstEndpoint.binding.closed {
		t.Fatal("retired generation destroy closed the shared physical bind")
	}
	if _, ok := firstEndpoint.binding.receivers[77]; ok {
		t.Fatal("destroyed endpoint retained its receiver-index mapping")
	}
	if _, ok := firstEndpoint.binding.remotes[remoteOne.DstToString()]; ok {
		t.Fatal("destroyed endpoint retained its remote mapping")
	}
	firstEndpoint.binding.dispatch(wireGuardGenerationInitiation(16), remoteOne)
	assertWireGuardGenerationPacket(t, secondEndpoint, wireGuardMessageInitiation)

	for receiver := uint32(1); receiver <= wireGuardAssociationLimit; receiver++ {
		secondEndpoint.binding.receivers[receiver] = secondEndpoint
	}
	fullRemote := wireGuardGenerationEndpoint(31005)
	if err := secondEndpoint.Send([][]byte{wireGuardGenerationInitiation(wireGuardAssociationLimit + 1)}, fullRemote); !errors.Is(err, errWireGuardAssociationLimit) {
		t.Fatalf("full receiver map Send() error = %v, want association limit", err)
	}
	if len(secondEndpoint.binding.receivers) != wireGuardAssociationLimit {
		t.Fatalf("receiver map size after refusal = %d, want %d", len(secondEndpoint.binding.receivers), wireGuardAssociationLimit)
	}
	if _, ok := secondEndpoint.binding.remotes[fullRemote.DstToString()]; ok {
		t.Fatal("receiver-limit refusal retained a remote mapping")
	}
	if err := owner.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !secondRuntime.closedState() {
		t.Fatal("Stop() did not close the runtime owned only by the active GenerationView")
	}
}

func TestWireGuardGenerationDeleteAndDisableRevokeOnlyTargetProfile(t *testing.T) {
	for _, testCase := range []struct {
		name string
		next func(model.Snapshot) model.Snapshot
	}{
		{name: "delete", next: func(snapshot model.Snapshot) model.Snapshot {
			snapshot.WireGuardProfiles = snapshot.WireGuardProfiles[1:]
			return snapshot
		}},
		{name: "disable", next: func(snapshot model.Snapshot) model.Snapshot {
			snapshot.WireGuardProfiles[0].Enabled = false
			return snapshot
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			registry := module.NewRegistry()
			factory := &wireGuardGenerationTestFactory{}
			owner := NewManagedModuleWithConfig(factory.create, ModuleConfig{GenerationSelector: registry})
			defer owner.Stop(context.Background())
			first := wireGuardGenerationSnapshot(1, 51, 52)
			firstTx := prepareWireGuardGenerationTransaction(t, owner, model.Snapshot{}, first)
			firstTx.FinalizeCommitSuccess()
			firstRuntime := factory.runtimeAt(t, 0)
			secondRuntime := factory.runtimeAt(t, 1)
			if err := firstRuntime.endpoint.touchAssociation("127.0.0.1:32001"); err != nil {
				t.Fatalf("touch target association: %v", err)
			}
			if err := secondRuntime.endpoint.touchAssociation("127.0.0.1:32002"); err != nil {
				t.Fatalf("touch unrelated association: %v", err)
			}

			next := testCase.next(wireGuardGenerationSnapshot(2, 51, 52))
			secondTx := prepareWireGuardGenerationTransaction(t, owner, first, next)
			secondTx.FinalizeCommitSuccess()
			if !firstRuntime.closedState() {
				t.Fatal("deleted/disabled profile runtime was not released after revoke")
			}
			if secondRuntime.closedState() {
				t.Fatal("unrelated profile runtime was revoked")
			}
			if got := owner.drain.Registry().GenerationCount(firstTx.generationID); got != 1 {
				t.Fatalf("old generation sessions = %d, want one unrelated association", got)
			}
		})
	}
}

func TestWireGuardGenerationThirdGenerationForcesOldestAndReleasesRuntime(t *testing.T) {
	registry := module.NewRegistry()
	factory := &wireGuardGenerationTestFactory{}
	owner := NewManagedModuleWithConfig(factory.create, ModuleConfig{GenerationSelector: registry, DrainTimeout: time.Hour})
	defer owner.Stop(context.Background())

	first := wireGuardGenerationSnapshot(1, 61)
	firstTx := prepareWireGuardGenerationTransaction(t, owner, model.Snapshot{}, first)
	firstTx.FinalizeCommitSuccess()
	firstRuntime := factory.runtimeAt(t, 0)
	if err := firstRuntime.endpoint.touchAssociation("127.0.0.1:33001"); err != nil {
		t.Fatalf("touch first association: %v", err)
	}

	second := wireGuardGenerationSnapshot(2, 61)
	second.WireGuardProfiles[0].Peers[0].PersistentKeepaliveSeconds = 10
	secondTx := prepareWireGuardGenerationTransaction(t, owner, first, second)
	secondTx.FinalizeCommitSuccess()
	secondRuntime := factory.runtimeAt(t, 1)
	if err := secondRuntime.endpoint.touchAssociation("127.0.0.1:33002"); err != nil {
		t.Fatalf("touch second association: %v", err)
	}
	if firstRuntime.closedState() {
		t.Fatal("old runtime closed before its association drained")
	}

	third := wireGuardGenerationSnapshot(3, 61)
	third.WireGuardProfiles[0].Peers[0].PersistentKeepaliveSeconds = 20
	thirdTx := prepareWireGuardGenerationTransaction(t, owner, second, third)
	thirdTx.FinalizeCommitSuccess()
	if !firstRuntime.closedState() {
		t.Fatal("third generation did not force and release the oldest runtime")
	}
	status := wireGuardGenerationDrainStatus(t, owner.drain.Snapshot(), firstTx.generationID)
	if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonGenerationLimit {
		t.Fatalf("oldest generation status = %+v", status)
	}
	if secondRuntime.closedState() {
		t.Fatal("third generation forced the immediately previous runtime")
	}
}

func TestWireGuardGenerationPrepareFailureReleasesAllBindLeases(t *testing.T) {
	registry := module.NewRegistry()
	factory := &wireGuardGenerationTestFactory{failProfileID: 72}
	owner := NewManagedModuleWithConfig(factory.create, ModuleConfig{
		GenerationSelector:     registry,
		SessionRegistrar:       wireGuardGenerationNoopRegistrar{},
		ExternalDrainLifecycle: true,
	})
	next := wireGuardGenerationSnapshot(1, 71, 72)
	_, err := owner.Prepare(context.Background(), module.ApplyRequest{Next: next})
	if err == nil || !strings.Contains(err.Error(), "forced generation factory failure") {
		t.Fatalf("Prepare() error = %v, want forced failure", err)
	}
	owner.ingress.mu.Lock()
	bindings := len(owner.ingress.bindings)
	owner.ingress.mu.Unlock()
	if bindings != 0 {
		t.Fatalf("stable bind count after failed prepare = %d, want 0", bindings)
	}
	if runtime := factory.runtimeAt(t, 0); !runtime.closedState() {
		t.Fatal("runtime created before prepare failure was not closed")
	}
}

func TestWireGuardGenerationRejectsConflictingBindingIdentity(t *testing.T) {
	registry := module.NewRegistry()
	factory := &wireGuardGenerationTestFactory{}
	owner := NewManagedModuleWithConfig(factory.create, ModuleConfig{
		GenerationSelector:     registry,
		SessionRegistrar:       wireGuardGenerationNoopRegistrar{},
		ExternalDrainLifecycle: true,
	})
	next := wireGuardGenerationSnapshot(1, 81, 81)
	_, err := owner.Prepare(context.Background(), module.ApplyRequest{Next: next})
	if err == nil || !strings.Contains(err.Error(), "conflicting stable binding identities") {
		t.Fatalf("Prepare() error = %v, want explicit stable binding conflict", err)
	}
}

func prepareWireGuardGenerationCandidate(t *testing.T, registry *module.Registry, previous, next model.Snapshot) module.PreparedGeneration {
	t.Helper()
	ctx, err := module.NewGenerationContext(previous, next)
	if err != nil {
		t.Fatalf("NewGenerationContext() error = %v", err)
	}
	candidate, err := registry.PrepareGeneration(context.Background(), ctx)
	if err != nil {
		t.Fatalf("PrepareGeneration() error = %v", err)
	}
	if err := candidate.Ready(context.Background()); err != nil {
		_ = candidate.Destroy(context.Background())
		t.Fatalf("Ready() error = %v", err)
	}
	return candidate
}

func prepareWireGuardGenerationTransaction(t *testing.T, owner *Module, previous, next model.Snapshot) *wireGuardGenerationTransaction {
	t.Helper()
	tx, err := owner.Prepare(context.Background(), module.ApplyRequest{Previous: previous, Next: next})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	generationTx, ok := tx.(*wireGuardGenerationTransaction)
	if !ok {
		t.Fatalf("Prepare() transaction = %T, want *wireGuardGenerationTransaction", tx)
	}
	if err := generationTx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	return generationTx
}

func wireGuardGenerationSnapshot(revision int64, profileIDs ...int) model.Snapshot {
	snapshot := model.Snapshot{Revision: revision}
	for index, profileID := range profileIDs {
		profile := testWireGuardProfile(profileID, "local", "127.0.0.1:51820", "10.0.0.0/8")
		profile.Name = "wireguard-generation"
		profile.ListenPort = 0
		profile.BindAddresses = []string{"127.0.0.1"}
		profile.Addresses = []string{netip.AddrFrom4([4]byte{10, 100, byte(index), 2}).String() + "/32"}
		snapshot.WireGuardProfiles = append(snapshot.WireGuardProfiles, profile)
	}
	return snapshot
}

func wireGuardGenerationInitiation(sender uint32) []byte {
	payload := make([]byte, wireGuardInitiationSize)
	binary.LittleEndian.PutUint32(payload[:4], wireGuardMessageInitiation)
	binary.LittleEndian.PutUint32(payload[4:8], sender)
	return payload
}

func wireGuardGenerationResponse(sender, receiver uint32) []byte {
	payload := make([]byte, wireGuardResponseSize)
	binary.LittleEndian.PutUint32(payload[:4], wireGuardMessageResponse)
	binary.LittleEndian.PutUint32(payload[4:8], sender)
	binary.LittleEndian.PutUint32(payload[8:12], receiver)
	return payload
}

func wireGuardGenerationTransport(receiver uint32) []byte {
	payload := make([]byte, wireGuardTransportMinSize)
	binary.LittleEndian.PutUint32(payload[:4], wireGuardMessageTransport)
	binary.LittleEndian.PutUint32(payload[4:8], receiver)
	return payload
}

func wireGuardGenerationEndpoint(port uint16) conn.Endpoint {
	return &conn.StdNetEndpoint{AddrPort: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)}
}

func assertWireGuardGenerationPacket(t *testing.T, endpoint *wireGuardBindEndpoint, messageType uint32) {
	t.Helper()
	select {
	case packet := <-endpoint.receive:
		if got := binary.LittleEndian.Uint32(packet.payload[:4]); got != messageType {
			t.Fatalf("packet type = %d, want %d", got, messageType)
		}
	case <-time.After(time.Second):
		t.Fatalf("endpoint %s did not receive packet type %d", endpoint.generation, messageType)
	}
}

func assertWireGuardGenerationNoPacket(t *testing.T, endpoint *wireGuardBindEndpoint, reason string) {
	t.Helper()
	select {
	case packet := <-endpoint.receive:
		t.Fatalf("%s delivered packet type %d", reason, binary.LittleEndian.Uint32(packet.payload[:4]))
	default:
	}
}

func wireGuardGenerationDrainStatus(t *testing.T, snapshot model.GenerationDrainSnapshot, generationID string) model.GenerationDrainStatus {
	t.Helper()
	for _, status := range snapshot.Generations {
		if status.GenerationID == generationID {
			return status
		}
	}
	t.Fatalf("generation %s has no drain status", generationID)
	return model.GenerationDrainStatus{}
}

type wireGuardGenerationTestFactory struct {
	mu            sync.Mutex
	runtimes      []*wireGuardGenerationTestRuntime
	failProfileID int
}

func (f *wireGuardGenerationTestFactory) create(_ context.Context, cfg Config) (RuntimeHandle, error) {
	endpoint, ok := cfg.bind.(*wireGuardBindEndpoint)
	if !ok || endpoint == nil {
		return nil, errors.New("generation factory did not receive a stable bind endpoint")
	}
	if _, _, err := endpoint.Open(uint16(cfg.ListenPort)); err != nil {
		return nil, err
	}
	if cfg.ID == f.failProfileID {
		return nil, errors.New("forced generation factory failure")
	}
	runtime := &wireGuardGenerationTestRuntime{recordingRuntime: &recordingRuntime{}, endpoint: endpoint}
	f.mu.Lock()
	f.runtimes = append(f.runtimes, runtime)
	f.mu.Unlock()
	return runtime, nil
}

func (f *wireGuardGenerationTestFactory) runtimeAt(t *testing.T, index int) *wireGuardGenerationTestRuntime {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.runtimes) {
		t.Fatalf("runtime index %d out of range %d", index, len(f.runtimes))
	}
	return f.runtimes[index]
}

type wireGuardGenerationTestRuntime struct {
	*recordingRuntime
	endpoint *wireGuardBindEndpoint
	mu       sync.Mutex
}

func (r *wireGuardGenerationTestRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recordingRuntime.Close()
}

func (r *wireGuardGenerationTestRuntime) closedState() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

type wireGuardGenerationNoopRegistrar struct{}

func (wireGuardGenerationNoopRegistrar) RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error) {
	return nil, nil
}
