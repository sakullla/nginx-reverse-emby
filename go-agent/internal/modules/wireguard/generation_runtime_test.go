package wireguard

import (
	"context"
	"encoding/binary"
	"errors"
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
	factory := &wireGuardGenerationTestFactory{probeBeforePublish: true}
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
	if err := factory.prepublishErrorAt(t, 0); err != nil {
		t.Fatalf("candidate startup Send() error = %v, want silent prepublication drop", err)
	}
	if firstEndpoint.unpublishedSendCount() == 0 || len(firstEndpoint.binding.receivers) != 0 || firstEndpoint.binding.remoteCount != 0 {
		t.Fatal("unpublished candidate startup populated live association maps")
	}
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

	if err := secondEndpoint.Send([][]byte{wireGuardGenerationInitiation(77)}, remoteOne); err != nil {
		t.Fatalf("active same-remote Send(initiation) error = %v", err)
	}
	firstEndpoint.binding.dispatch(wireGuardGenerationResponse(91, 77), remoteOne)
	assertWireGuardGenerationPacket(t, secondEndpoint, wireGuardMessageResponse)
	assertWireGuardGenerationNoPacket(t, firstEndpoint, "active same-remote response")
	if err := firstEndpoint.Send([][]byte{wireGuardGenerationInitiation(78)}, remoteOne); err != nil {
		t.Fatalf("old associated Send(initiation) error = %v", err)
	}
	firstEndpoint.binding.dispatch(wireGuardGenerationTransport(78), wireGuardGenerationEndpoint(31004))
	assertWireGuardGenerationPacket(t, firstEndpoint, wireGuardMessageTransport)
	assertWireGuardGenerationNoPacket(t, secondEndpoint, "receiver-index pinned packet")
	firstEndpoint.beginDrain()
	drainingRemote := wireGuardGenerationEndpoint(31006)
	dropsBefore := firstEndpoint.unpublishedSendCount()
	if err := firstEndpoint.Send([][]byte{wireGuardGenerationInitiation(88)}, drainingRemote); err != nil {
		t.Fatalf("draining Send() error = %v, want silent admission drop", err)
	}
	if firstEndpoint.unpublishedSendCount() != dropsBefore+1 {
		t.Fatal("draining Send() did not record an admission drop")
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
	if _, ok := firstEndpoint.binding.receivers[78]; ok {
		t.Fatal("destroyed endpoint retained its receiver-index mapping")
	}
	if firstEndpoint.binding.remoteContainsLocked(remoteOne.DstToString(), firstEndpoint) {
		t.Fatal("destroyed endpoint retained its generation-scoped remote mapping")
	}
	if !firstEndpoint.binding.remoteContainsLocked(remoteOne.DstToString(), secondEndpoint) {
		t.Fatal("destroying old endpoint removed the active generation remote mapping")
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

func TestWireGuardGenerationRealRuntimeCannotTransmitBeforePublication(t *testing.T) {
	registry := module.NewRegistry()
	owner := NewManagedModuleWithConfig(nil, ModuleConfig{
		GenerationSelector:     registry,
		SessionRegistrar:       wireGuardGenerationNoopRegistrar{},
		ExternalDrainLifecycle: true,
	})
	defer owner.Stop(context.Background())
	next := wireGuardGenerationSnapshot(1, 55)
	next.WireGuardProfiles[0].PrivateKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	next.WireGuardProfiles[0].Peers[0].PublicKey = "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="
	next.WireGuardProfiles[0].Peers[0].PersistentKeepaliveSeconds = 1
	tx, err := owner.Prepare(context.Background(), module.ApplyRequest{Next: next})
	if err != nil {
		t.Fatalf("Prepare(real runtime) error = %v", err)
	}
	defer tx.Rollback()
	generationTx := tx.(*wireGuardGenerationTransaction)
	endpoints := generationTx.factory.endpointsSnapshot()
	if len(endpoints) != 1 {
		t.Fatalf("real runtime endpoints = %d, want 1", len(endpoints))
	}
	endpoint := endpoints[0]
	deadline := time.Now().Add(2 * time.Second)
	for endpoint.unpublishedSendCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if endpoint.unpublishedSendCount() == 0 {
		t.Fatal("real WireGuard keepalive did not exercise prepublication outbound admission")
	}
	endpoint.binding.mu.Lock()
	receivers := len(endpoint.binding.receivers)
	remotes := endpoint.binding.remoteCount
	endpoint.binding.mu.Unlock()
	if receivers != 0 || remotes != 0 {
		t.Fatalf("real unpublished runtime populated live maps: receivers=%d remotes=%d", receivers, remotes)
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

func TestWireGuardGenerationPeerRemovalAndRotationRevokeOldProfile(t *testing.T) {
	const rotatedKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
	for _, testCase := range []struct {
		name   string
		first  func(model.Snapshot) model.Snapshot
		second func(model.Snapshot) model.Snapshot
	}{
		{
			name: "peer deletion",
			first: func(snapshot model.Snapshot) model.Snapshot {
				peer := snapshot.WireGuardProfiles[0].Peers[0]
				peer.Name = "removed-peer"
				peer.PublicKey = rotatedKey
				peer.Endpoint = "127.0.0.1:51821"
				snapshot.WireGuardProfiles[0].Peers = append(snapshot.WireGuardProfiles[0].Peers, peer)
				return snapshot
			},
			second: func(snapshot model.Snapshot) model.Snapshot { return snapshot },
		},
		{
			name:  "peer rotation",
			first: func(snapshot model.Snapshot) model.Snapshot { return snapshot },
			second: func(snapshot model.Snapshot) model.Snapshot {
				snapshot.WireGuardProfiles[0].Peers[0].PublicKey = rotatedKey
				return snapshot
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			registry := module.NewRegistry()
			factory := &wireGuardGenerationTestFactory{}
			owner := NewManagedModuleWithConfig(factory.create, ModuleConfig{GenerationSelector: registry})
			defer owner.Stop(context.Background())
			first := testCase.first(wireGuardGenerationSnapshot(1, 53, 54))
			firstTx := prepareWireGuardGenerationTransaction(t, owner, model.Snapshot{}, first)
			firstTx.FinalizeCommitSuccess()
			target := factory.runtimeAt(t, 0)
			unrelated := factory.runtimeAt(t, 1)
			if err := target.endpoint.touchAssociation("127.0.0.1:35001"); err != nil {
				t.Fatalf("touch target association: %v", err)
			}
			if err := unrelated.endpoint.touchAssociation("127.0.0.1:35002"); err != nil {
				t.Fatalf("touch unrelated association: %v", err)
			}

			second := testCase.second(wireGuardGenerationSnapshot(2, 53, 54))
			secondTx := prepareWireGuardGenerationTransaction(t, owner, first, second)
			secondTx.FinalizeCommitSuccess()
			if !target.closedState() {
				t.Fatal("peer deletion/rotation did not revoke the containing old profile")
			}
			if unrelated.closedState() {
				t.Fatal("peer deletion/rotation revoked an unrelated profile")
			}
		})
	}
}

func TestWireGuardGenerationLifecycleFailuresAreObservable(t *testing.T) {
	t.Run("drain activation", func(t *testing.T) {
		registry := module.NewRegistry()
		factory := &wireGuardGenerationTestFactory{}
		owner := NewManagedModuleWithConfig(factory.create, ModuleConfig{GenerationSelector: registry})
		defer owner.Stop(context.Background())
		first := wireGuardGenerationSnapshot(2, 91)
		if err := owner.Apply(context.Background(), module.ApplyRequest{Next: first}); err != nil {
			t.Fatalf("Apply(first) error = %v", err)
		}
		second := wireGuardGenerationSnapshot(1, 91)
		second.WireGuardProfiles[0].Peers[0].PersistentKeepaliveSeconds = 15
		err := owner.Apply(context.Background(), module.ApplyRequest{Previous: first, Next: second})
		if err == nil || !strings.Contains(err.Error(), "revision must increase") {
			t.Fatalf("Apply(non-increasing revision) error = %v", err)
		}
		if owner.LastGenerationLifecycleError() == nil {
			t.Fatal("drain activation failure was not recorded on the module")
		}
		if !factory.runtimeAt(t, 1).closedState() {
			t.Fatal("untracked generation runtime remained open after activation refusal")
		}
		if factory.runtimeAt(t, 0).closedState() {
			t.Fatal("activation refusal closed the previous active runtime")
		}
	})

	t.Run("deferred session registration", func(t *testing.T) {
		registry := module.NewRegistry()
		factory := &wireGuardGenerationTestFactory{}
		controller := generation.NewDrainController(nil)
		owner := NewManagedModuleWithConfig(factory.create, ModuleConfig{
			GenerationSelector: registry,
			DrainController:    controller,
			SessionRegistrar:   wireGuardGenerationFailingRegistrar{err: errors.New("registrar unavailable")},
		})
		defer owner.Stop(context.Background())
		next := wireGuardGenerationSnapshot(1, 92)
		tx := prepareWireGuardGenerationTransaction(t, owner, model.Snapshot{}, next)
		runtime := factory.runtimeAt(t, 0)
		if err := runtime.endpoint.touchAssociation("127.0.0.1:35003"); err != nil {
			t.Fatalf("touch deferred association: %v", err)
		}
		tx.FinalizeCommitSuccess()
		if err := tx.CommitSuccessError(); err == nil || !strings.Contains(err.Error(), "registrar unavailable") {
			t.Fatalf("CommitSuccessError() = %v, want registrar failure", err)
		}
		if !runtime.closedState() {
			t.Fatal("registrar failure did not close the affected profile runtime")
		}
		if owner.LastGenerationLifecycleError() == nil {
			t.Fatal("registrar failure was not recorded on the module")
		}
	})

	t.Run("retired runtime destroy", func(t *testing.T) {
		registry := module.NewRegistry()
		factory := &wireGuardGenerationTestFactory{closeErrorProfileID: 93}
		owner := NewManagedModuleWithConfig(factory.create, ModuleConfig{GenerationSelector: registry})
		defer owner.Stop(context.Background())
		first := wireGuardGenerationSnapshot(1, 93)
		if err := owner.Apply(context.Background(), module.ApplyRequest{Next: first}); err != nil {
			t.Fatalf("Apply(first) error = %v", err)
		}
		second := wireGuardGenerationSnapshot(2, 93)
		second.WireGuardProfiles[0].Peers[0].PersistentKeepaliveSeconds = 20
		err := owner.Apply(context.Background(), module.ApplyRequest{Previous: first, Next: second})
		if err == nil || !strings.Contains(err.Error(), "forced runtime close failure") {
			t.Fatalf("Apply(second) error = %v, want retired destroy failure", err)
		}
		if owner.LastGenerationLifecycleError() == nil {
			t.Fatal("retired runtime destroy failure was not recorded on the module")
		}
	})
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
	mu                  sync.Mutex
	runtimes            []*wireGuardGenerationTestRuntime
	prepublishErrors    []error
	failProfileID       int
	closeErrorProfileID int
	probeBeforePublish  bool
}

func (f *wireGuardGenerationTestFactory) create(_ context.Context, cfg Config) (RuntimeHandle, error) {
	endpoint, ok := cfg.bind.(*wireGuardBindEndpoint)
	if !ok || endpoint == nil {
		return nil, errors.New("generation factory did not receive a stable bind endpoint")
	}
	if _, _, err := endpoint.Open(uint16(cfg.ListenPort)); err != nil {
		return nil, err
	}
	if f.probeBeforePublish {
		err := endpoint.Send([][]byte{wireGuardGenerationInitiation(uint32(cfg.ID + 1000))}, wireGuardGenerationEndpoint(uint16(34000+cfg.ID)))
		f.mu.Lock()
		f.prepublishErrors = append(f.prepublishErrors, err)
		f.mu.Unlock()
	}
	if cfg.ID == f.failProfileID {
		return nil, errors.New("forced generation factory failure")
	}
	runtime := &wireGuardGenerationTestRuntime{recordingRuntime: &recordingRuntime{}, endpoint: endpoint}
	if cfg.ID == f.closeErrorProfileID {
		runtime.closeErr = errors.New("forced runtime close failure")
	}
	f.mu.Lock()
	f.runtimes = append(f.runtimes, runtime)
	f.mu.Unlock()
	return runtime, nil
}

func (f *wireGuardGenerationTestFactory) prepublishErrorAt(t *testing.T, index int) error {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.prepublishErrors) {
		t.Fatalf("prepublish error index %d out of range %d", index, len(f.prepublishErrors))
	}
	return f.prepublishErrors[index]
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
	closeErr error
}

func (r *wireGuardGenerationTestRuntime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.recordingRuntime.Close()
	return r.closeErr
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

type wireGuardGenerationFailingRegistrar struct{ err error }

func (r wireGuardGenerationFailingRegistrar) RegisterSession(string, generation.EntityKey, string, generation.Session) (*generation.SessionHandle, error) {
	return nil, r.err
}
