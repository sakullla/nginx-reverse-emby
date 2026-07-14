package ingress

import (
	"context"
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

type recordingProcessPacketGate struct {
	activateErr error
	pauseErr    error
	prepareErr  error
	activations int
	pauses      int
	resumes     int
	takes       int
}

func (g *recordingProcessPacketGate) Activate() error { g.activations++; return g.activateErr }
func (g *recordingProcessPacketGate) Pause() error    { g.pauses++; return g.pauseErr }
func (g *recordingProcessPacketGate) Resume() error   { g.resumes++; return nil }
func (g *recordingProcessPacketGate) PrepareAuthority() error {
	return g.prepareErr
}
func (g *recordingProcessPacketGate) TakeAuthority() error   { g.takes++; return nil }
func (*recordingProcessPacketGate) Physical() net.PacketConn { return nil }

func TestProcessPacketHandoffKeepsOldAssociationForwardsNewAndTransfersAuthority(t *testing.T) {
	if !platform.SupportsHotRestart() {
		t.Skipf("packet FD handoff is unsupported on %s", runtime.GOOS)
	}
	const bindingID = "l4:udp:handoff"
	parentRegistry := NewProcessPacketRegistry()
	parent, err := parentRegistry.NewBroker(context.Background(), bindingID, "udp", func(context.Context) (net.PacketConn, error) {
		return net.ListenPacket("udp", "127.0.0.1:0")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parentEndpoint := parent.NewEndpoint("parent", 16)
	if _, err := parent.Activate(parentEndpoint); err != nil {
		t.Fatal(err)
	}

	oldClient := listenPacketClient(t)
	defer oldClient.Close()
	sendPacket(t, oldClient, parent.LocalAddr(), "old-before")
	readProcessPacket(t, parentEndpoint, "old-before")

	bundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	childRegistry := NewProcessPacketRegistry()
	packetSet, err := childRegistry.Import(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer packetSet.Close()
	defer childRegistry.Close()
	child, err := childRegistry.NewBroker(context.Background(), bindingID, "udp", func(context.Context) (net.PacketConn, error) {
		t.Fatal("child rebound inherited packet address")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	childEndpoint := child.NewEndpoint("child", 16)
	if _, err := child.Activate(childEndpoint); err != nil {
		t.Fatal(err)
	}
	if err := childRegistry.ValidateImported(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.BeginForwarding(); err != nil {
		t.Fatal(err)
	}
	if err := childRegistry.ActivateImported(); err != nil {
		t.Fatal(err)
	}

	sendPacket(t, oldClient, parent.LocalAddr(), "old-during")
	readProcessPacket(t, parentEndpoint, "old-during")
	newClient := listenPacketClient(t)
	defer newClient.Close()
	sendPacket(t, newClient, parent.LocalAddr(), "new-forwarded")
	readProcessPacket(t, childEndpoint, "new-forwarded")

	if err := parentRegistry.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.FlushForwarding(); err != nil {
		t.Fatal(err)
	}
	if err := childRegistry.TakeAuthorityImported(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.FinalizeForwarding(); err != nil {
		t.Fatal(err)
	}
	authorityClient := listenPacketClient(t)
	defer authorityClient.Close()
	sendPacket(t, authorityClient, parent.LocalAddr(), "child-authority")
	readProcessPacket(t, childEndpoint, "child-authority")
	reexported, err := childRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer reexported.Close()
	if len(reexported.Descriptors) != 1 || reexported.Descriptors[0].ID != bindingID {
		t.Fatalf("re-exported packet descriptors = %+v", reexported.Descriptors)
	}
}

func TestProcessPacketForwardingRollbackRestoresParent(t *testing.T) {
	if !platform.SupportsHotRestart() {
		t.Skipf("packet FD handoff is unsupported on %s", runtime.GOOS)
	}
	parentRegistry := NewProcessPacketRegistry()
	parent, err := parentRegistry.NewBroker(context.Background(), "packet", "udp", func(context.Context) (net.PacketConn, error) {
		return net.ListenPacket("udp", "127.0.0.1:0")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	endpoint := parent.NewEndpoint("parent", 8)
	if _, err := parent.Activate(endpoint); err != nil {
		t.Fatal(err)
	}
	bundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if err := parentRegistry.BeginForwarding(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.Resume(); err != nil {
		t.Fatal(err)
	}
	client := listenPacketClient(t)
	defer client.Close()
	sendPacket(t, client, parent.LocalAddr(), "rollback")
	readProcessPacket(t, endpoint, "rollback")
}

func TestProcessPacketPauseWaitsForLastForwardBeforeBarrier(t *testing.T) {
	if !platform.SupportsHotRestart() {
		t.Skipf("packet FD handoff is unsupported on %s", runtime.GOOS)
	}
	parentRegistry := NewProcessPacketRegistry()
	parent, err := parentRegistry.NewBroker(context.Background(), "packet", "udp", func(context.Context) (net.PacketConn, error) {
		return net.ListenPacket("udp", "127.0.0.1:0")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parentEndpoint := parent.NewEndpoint("parent", 8)
	if _, err := parent.Activate(parentEndpoint); err != nil {
		t.Fatal(err)
	}
	bundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	childRegistry := NewProcessPacketRegistry()
	packetSet, err := childRegistry.Import(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer packetSet.Close()
	defer childRegistry.Close()
	child, err := childRegistry.NewBroker(context.Background(), "packet", "udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	childEndpoint := child.NewEndpoint("child", 8)
	if _, err := child.Activate(childEndpoint); err != nil {
		t.Fatal(err)
	}
	if err := childRegistry.ValidateImported(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.BeginForwarding(); err != nil {
		t.Fatal(err)
	}
	if err := childRegistry.ActivateImported(); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	parent.mu.Lock()
	parent.beforeProcessForward = func() {
		close(entered)
		<-release
	}
	parent.mu.Unlock()
	client := listenPacketClient(t)
	defer client.Close()
	sendPacket(t, client, parent.LocalAddr(), "last-forward")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("parent did not enter forwarding hook")
	}
	paused := make(chan error, 1)
	go func() { paused <- parentRegistry.Pause() }()
	select {
	case err := <-paused:
		t.Fatalf("Pause() returned before in-flight forwarding completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	readProcessPacket(t, childEndpoint, "last-forward")
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.FlushForwarding(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.Resume(); err != nil {
		t.Fatal(err)
	}
}

func TestProcessPacketActivationAndPauseCompensateEarlierGates(t *testing.T) {
	activationErr := errors.New("activate second")
	first := &recordingProcessPacketGate{}
	second := &recordingProcessPacketGate{activateErr: activationErr}
	registry := NewProcessPacketRegistry()
	registry.strict = true
	registry.imported = &hotrestart.PacketSet{Conns: map[string]*hotrestart.GatedPacketConn{}}
	registry.claimed = []processPacketClaim{{gate: first}, {gate: second}}
	if err := registry.ActivateImported(); !errors.Is(err, activationErr) {
		t.Fatalf("ActivateImported() error = %v", err)
	}
	if first.activations != 1 || first.pauses != 1 || second.activations != 1 {
		t.Fatalf("activation compensation = first %d/%d, second %d", first.activations, first.pauses, second.activations)
	}

	pauseErr := errors.New("pause second")
	first = &recordingProcessPacketGate{}
	second = &recordingProcessPacketGate{pauseErr: pauseErr}
	registry.claimed = []processPacketClaim{{gate: first}, {gate: second}}
	if err := registry.Pause(); !errors.Is(err, pauseErr) {
		t.Fatalf("Pause() error = %v", err)
	}
	if first.pauses != 1 || first.resumes != 1 || second.pauses != 1 {
		t.Fatalf("pause compensation = first %d/%d, second %d", first.pauses, first.resumes, second.pauses)
	}
}

func TestProcessPacketAuthorityPreflightPreventsPartialTakeover(t *testing.T) {
	prepareErr := errors.New("second gate not ready")
	first := &recordingProcessPacketGate{}
	second := &recordingProcessPacketGate{prepareErr: prepareErr}
	registry := NewProcessPacketRegistry()
	registry.claimed = []processPacketClaim{{gate: first}, {gate: second}}
	if err := registry.TakeAuthorityImported(); !errors.Is(err, prepareErr) {
		t.Fatalf("TakeAuthorityImported() error = %v", err)
	}
	if first.takes != 0 || second.takes != 0 {
		t.Fatalf("partial authority takeover = %d/%d", first.takes, second.takes)
	}
}

func listenPacketClient(t *testing.T) net.PacketConn {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func sendPacket(t *testing.T, conn net.PacketConn, target net.Addr, payload string) {
	t.Helper()
	if _, err := conn.WriteTo([]byte(payload), target); err != nil {
		t.Fatal(err)
	}
}

func readProcessPacket(t *testing.T, conn net.PacketConn, want string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 128)
	n, _, err := conn.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != want {
		t.Fatalf("packet payload = %q, want %q", got, want)
	}
}
