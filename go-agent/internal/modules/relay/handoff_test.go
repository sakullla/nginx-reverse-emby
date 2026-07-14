package relay

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

func TestRelayQUICProcessPacketHandoffRoutesOldNewAndAbort(t *testing.T) {
	if !platform.SupportsHotRestart() {
		t.Skip("packet FD handoff is unsupported on this platform")
	}
	listener := Listener{ID: 8, TransportMode: ListenerTransportModeQUIC, ListenPort: 0}
	parentRegistry := ingress.NewProcessPacketRegistry()
	parent := newRelayIngressManager(nil)
	parent.processPackets = parentRegistry
	parentLease, err := parent.acquire(t.Context(), "parent", listener, "127.0.0.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer parentLease.release()
	defer parent.close()
	defer parentRegistry.Close()
	if err := parentLease.activate(); err != nil {
		t.Fatal(err)
	}
	oldClient := dialRelayHandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer oldClient.Close()
	writeRelayHandoffPacket(t, oldClient, "old-before")
	readRelayHandoffPacket(t, parentLease.packet, "old-before")

	bundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	childRegistry := ingress.NewProcessPacketRegistry()
	set, err := childRegistry.Import(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	defer childRegistry.Close()
	child := newRelayIngressManager(nil)
	child.processPackets = childRegistry
	childLease, err := child.acquire(t.Context(), "child", listener, "127.0.0.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer childLease.release()
	defer child.close()
	if got, want := childLease.binding.packet.LocalAddr().String(), parentLease.binding.packet.LocalAddr().String(); got != want {
		t.Fatalf("child packet address = %s, want inherited %s", got, want)
	}
	if err := childLease.activate(); err != nil {
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
	writeRelayHandoffPacket(t, oldClient, "old-during")
	readRelayHandoffPacket(t, parentLease.packet, "old-during")
	newClient := dialRelayHandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer newClient.Close()
	writeRelayHandoffPacket(t, newClient, "new-forwarded")
	readRelayHandoffPacket(t, childLease.packet, "new-forwarded")

	if err := parentRegistry.Resume(); err != nil {
		t.Fatal(err)
	}
	afterAbort := dialRelayHandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer afterAbort.Close()
	writeRelayHandoffPacket(t, afterAbort, "after-abort")
	readRelayHandoffPacket(t, parentLease.packet, "after-abort")
}

func TestRelayUOTUsesExistingTLSTCPStreamHandoff(t *testing.T) {
	if !platform.SupportsHotRestart() {
		t.Skip("stream FD handoff is unsupported on this platform")
	}
	// UOT frames are multiplexed inside the TLS/TCP relay listener; they do not
	// own a packet socket. This verifies the child consumes that production
	// listener through the T37 stream registry while requiring no packet claim.
	listener := Listener{ID: 9, TransportMode: ListenerTransportModeTLSTCP, ListenPort: 0}
	parentStreams := ingress.NewProcessStreamRegistry()
	parent := newRelayIngressManager(nil)
	parent.processStreams = parentStreams
	parentLease, err := parent.acquire(t.Context(), "parent", listener, "127.0.0.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer parentLease.release()
	defer parent.close()
	bundle, err := parentStreams.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	childStreams := ingress.NewProcessStreamRegistry()
	streamSet, err := childStreams.Import(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer streamSet.Close()
	childPackets := ingress.NewProcessPacketRegistry()
	packetSet, err := childPackets.Import(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer packetSet.Close()
	defer childPackets.Close()
	child := newRelayIngressManager(nil)
	child.processStreams = childStreams
	child.processPackets = childPackets
	childLease, err := child.acquire(t.Context(), "child", listener, "127.0.0.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer childLease.release()
	defer child.close()
	if err := childStreams.ValidateImported(); err != nil {
		t.Fatal(err)
	}
	if err := childPackets.ValidateImported(); err != nil {
		t.Fatalf("UOT unexpectedly required a packet descriptor: %v", err)
	}
}

func TestRelayQUICIngressConsumesProcessPacketDescriptor(t *testing.T) {
	registry := ingress.NewProcessPacketRegistry()
	set, err := registry.Import(nil, nil)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	defer set.Close()
	defer registry.Close()

	mod := NewModule(Config{})
	mod.SetProcessPacketRegistry(registry)
	lease, err := mod.ingress.acquire(context.Background(), "generation-2", Listener{
		TransportMode: ListenerTransportModeQUIC,
		ListenPort:    0,
	}, "127.0.0.1", nil)
	if lease != nil {
		_ = lease.release()
	}
	if err == nil || !strings.Contains(err.Error(), `inherited packet descriptor "relay:udp:127.0.0.1:0" is missing`) {
		t.Fatalf("acquire() error = %v, want missing process packet descriptor", err)
	}
}

func dialRelayHandoffClient(t *testing.T, target net.Addr) *net.UDPConn {
	t.Helper()
	client, err := net.DialUDP("udp", nil, target.(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeRelayHandoffPacket(t *testing.T, client *net.UDPConn, payload string) {
	t.Helper()
	if _, err := client.Write(relayTestQUICLongPacket([]byte(payload))); err != nil {
		t.Fatal(err)
	}
}

func readRelayHandoffPacket(t *testing.T, endpoint *ingress.PacketEndpoint, want string) {
	t.Helper()
	if err := endpoint.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 256)
	n, _, err := endpoint.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	wantPacket := relayTestQUICLongPacket([]byte(want))
	if got := string(buffer[:n]); got != string(wantPacket) {
		t.Fatalf("packet = %x, want %x", buffer[:n], wantPacket)
	}
}
