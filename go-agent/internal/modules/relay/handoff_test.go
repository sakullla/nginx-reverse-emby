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
	if err := parentLease.activate(); err != nil {
		t.Fatal(err)
	}
	defer parentRegistry.Close()
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

	if err := child.close(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.Resume(); err != nil {
		t.Fatal(err)
	}
	afterAbort := dialRelayHandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer afterAbort.Close()
	writeRelayHandoffPacket(t, afterAbort, "after-abort")
	readRelayHandoffPacket(t, parentLease.packet, "after-abort")

	secondBundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer secondBundle.Close()
	successorRegistry := ingress.NewProcessPacketRegistry()
	successorSet, err := successorRegistry.Import(secondBundle.Descriptors, secondBundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer successorSet.Close()
	defer successorRegistry.Close()
	successor := newRelayIngressManager(nil)
	successor.processPackets = successorRegistry
	successorLease, err := successor.acquire(t.Context(), "successor", listener, "127.0.0.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer successorLease.release()
	defer successor.close()
	if err := successorLease.activate(); err != nil {
		t.Fatal(err)
	}
	if err := successorRegistry.ValidateImported(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.BeginForwarding(); err != nil {
		t.Fatal(err)
	}
	if err := successorRegistry.ActivateImported(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.FlushForwarding(); err != nil {
		t.Fatal(err)
	}
	if err := successorRegistry.TakeAuthorityImported(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.FinalizeForwarding(); err != nil {
		t.Fatal(err)
	}
	afterAuthority := dialRelayHandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer afterAuthority.Close()
	writeRelayHandoffPacket(t, afterAuthority, "after-authority")
	readRelayHandoffPacket(t, successorLease.packet, "after-authority")
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
	if err := parentLease.activate(); err != nil {
		t.Fatal(err)
	}
	oldClient, err := net.Dial("tcp", parentLease.binding.stream.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer oldClient.Close()
	oldServer := acceptRelayHandoffStream(t, parentLease.stream)
	defer oldServer.Close()
	requireRelayUOTFrame(t, oldClient, oldServer, "old-before")
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
	if err := childLease.activate(); err != nil {
		t.Fatal(err)
	}
	if err := childStreams.ValidateImported(); err != nil {
		t.Fatal(err)
	}
	if err := childPackets.ValidateImported(); err != nil {
		t.Fatalf("UOT unexpectedly required a packet descriptor: %v", err)
	}
	if err := parentStreams.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := childStreams.ActivateImported(); err != nil {
		t.Fatal(err)
	}
	requireRelayUOTFrame(t, oldClient, oldServer, "old-during")
	newClient, err := net.Dial("tcp", parentLease.binding.stream.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer newClient.Close()
	newServer := acceptRelayHandoffStream(t, childLease.stream)
	defer newServer.Close()
	requireRelayUOTFrame(t, newClient, newServer, "new-forwarded")

	if err := child.close(); err != nil {
		t.Fatal(err)
	}
	if err := parentStreams.Resume(); err != nil {
		t.Fatal(err)
	}
	afterAbortClient, err := net.Dial("tcp", parentLease.binding.stream.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer afterAbortClient.Close()
	afterAbortServer := acceptRelayHandoffStream(t, parentLease.stream)
	defer afterAbortServer.Close()
	requireRelayUOTFrame(t, afterAbortClient, afterAbortServer, "after-abort")
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

func acceptRelayHandoffStream(t *testing.T, listener net.Listener) net.Conn {
	t.Helper()
	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		conn, err := listener.Accept()
		done <- result{conn: conn, err: err}
	}()
	select {
	case accepted := <-done:
		if accepted.err != nil {
			t.Fatal(accepted.err)
		}
		return accepted.conn
	case <-time.After(3 * time.Second):
		t.Fatal("timed out accepting Relay UOT stream")
		return nil
	}
}

func requireRelayUOTFrame(t *testing.T, client net.Conn, server net.Conn, want string) {
	t.Helper()
	if err := server.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := WriteUOTPacket(client, []byte(want)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadUOTPacket(server)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("UOT frame = %q, want %q", got, want)
	}
}
