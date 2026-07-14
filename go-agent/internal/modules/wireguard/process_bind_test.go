package wireguard

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
	"golang.zx2c4.com/wireguard/conn"
)

func TestProcessWireGuardBindHandoffPinsOldAndForwardsNew(t *testing.T) {
	if !platform.SupportsHotRestart() {
		t.Skip("packet FD handoff is unsupported on this platform")
	}
	const identity = "agent/7/0/127.0.0.1"
	parentRegistry := ingress.NewProcessPacketRegistry()
	parent := newProcessWireGuardBind(parentRegistry, identity, []string{"127.0.0.1"})
	parentReceive, port, err := parent.Open(0)
	if err != nil {
		t.Fatalf("parent Open() error = %v", err)
	}
	defer parent.Close()
	defer parentRegistry.Close()

	oldClient := dialWireGuardProcessClient(t, port)
	defer oldClient.Close()
	sendWireGuardInitiation(t, oldClient, 11)
	readWireGuardProcessPacket(t, parentReceive[0], 11)

	bundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	defer bundle.Close()
	childRegistry := ingress.NewProcessPacketRegistry()
	packetSet, err := childRegistry.Import(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	defer packetSet.Close()
	defer childRegistry.Close()
	child := newProcessWireGuardBind(childRegistry, identity, []string{"127.0.0.1"})
	childReceive, childPort, err := child.Open(port)
	if err != nil {
		t.Fatalf("child Open() error = %v", err)
	}
	defer child.Close()
	if childPort != port {
		t.Fatalf("child port = %d, want %d", childPort, port)
	}
	if err := childRegistry.ValidateImported(); err != nil {
		t.Fatalf("ValidateImported() error = %v", err)
	}
	if err := parentRegistry.BeginForwarding(); err != nil {
		t.Fatalf("BeginForwarding() error = %v", err)
	}
	defer parentRegistry.Resume()
	if err := childRegistry.ActivateImported(); err != nil {
		t.Fatalf("ActivateImported() error = %v", err)
	}

	sendWireGuardInitiation(t, oldClient, 12)
	readWireGuardProcessPacket(t, parentReceive[0], 12)
	newClient := dialWireGuardProcessClient(t, port)
	defer newClient.Close()
	sendWireGuardInitiation(t, newClient, 21)
	readWireGuardProcessPacket(t, childReceive[0], 21)
}

func TestProcessWireGuardBindCarriesProductionPackets(t *testing.T) {
	registry := ingress.NewProcessPacketRegistry()
	bind := newProcessWireGuardBind(registry, "agent/7/0/127.0.0.1", []string{"127.0.0.1"})
	receive, port, err := bind.Open(0)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer bind.Close()
	defer registry.Close()
	if len(receive) != 1 || port == 0 {
		t.Fatalf("Open() = %d receive funcs, port %d", len(receive), port)
	}

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: int(port)})
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	defer client.Close()
	payload := make([]byte, wireGuardInitiationSize)
	binary.LittleEndian.PutUint32(payload[:4], wireGuardMessageInitiation)
	binary.LittleEndian.PutUint32(payload[4:8], 9)
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}

	packets := [][]byte{make([]byte, wireGuardReceiveBufferSize)}
	sizes := make([]int, 1)
	endpoints := make([]conn.Endpoint, 1)
	count, err := receive[0](packets, sizes, endpoints)
	if err != nil {
		t.Fatalf("receive() error = %v", err)
	}
	if count != 1 || sizes[0] != len(payload) || !bytes.Equal(packets[0][:sizes[0]], payload) {
		t.Fatalf("receive() count=%d size=%d payload match=%t", count, sizes[0], bytes.Equal(packets[0][:sizes[0]], payload))
	}
	response := []byte("reply")
	if err := bind.Send([][]byte{response}, endpoints[0]); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(response))
	if _, err := client.Read(got); err != nil {
		t.Fatalf("client Read() error = %v", err)
	}
	if !bytes.Equal(got, response) {
		t.Fatalf("reply = %q, want %q", got, response)
	}
}

func TestProcessWireGuardBindConsumesImportedDescriptor(t *testing.T) {
	registry := ingress.NewProcessPacketRegistry()
	set, err := registry.Import(nil, nil)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	defer set.Close()
	defer registry.Close()

	bind := newProcessWireGuardBind(registry, "agent/7/51820/127.0.0.1", []string{"127.0.0.1"})
	_, _, err = bind.Open(51820)
	if err == nil || !strings.Contains(err.Error(), `inherited packet descriptor "wireguard:agent/7/51820/127.0.0.1:udp4:127.0.0.1" is missing`) {
		t.Fatalf("Open() error = %v, want missing process packet descriptor", err)
	}
}

func TestProcessWireGuardClassifierPinsReceiverAcrossRoaming(t *testing.T) {
	classifier := newProcessWireGuardClassifier()
	initiation := make([]byte, wireGuardInitiationSize)
	binary.LittleEndian.PutUint32(initiation[:4], wireGuardMessageInitiation)
	binary.LittleEndian.PutUint32(initiation[4:8], 42)
	firstRemote := netip.MustParseAddrPort("127.0.0.1:51820")
	classifier.observeSend([][]byte{initiation}, firstRemote)

	transport := make([]byte, wireGuardTransportMinSize)
	binary.LittleEndian.PutUint32(transport[:4], wireGuardMessageTransport)
	binary.LittleEndian.PutUint32(transport[4:8], 42)
	key, ok := classifier.Classify(transport, ingress.PacketMetadata{
		Network:    "udp",
		LocalAddr:  &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 51820},
		RemoteAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 51821},
	})
	if !ok || key != ingress.AssociationKey("wireguard|"+firstRemote.String()) {
		t.Fatalf("Classify() = %q, %t, want receiver pinned to %q", key, ok, firstRemote)
	}
}

func dialWireGuardProcessClient(t *testing.T, port uint16) *net.UDPConn {
	t.Helper()
	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: int(port)})
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	return client
}

func sendWireGuardInitiation(t *testing.T, client *net.UDPConn, sender uint32) {
	t.Helper()
	payload := make([]byte, wireGuardInitiationSize)
	binary.LittleEndian.PutUint32(payload[:4], wireGuardMessageInitiation)
	binary.LittleEndian.PutUint32(payload[4:8], sender)
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}

func readWireGuardProcessPacket(t *testing.T, receive conn.ReceiveFunc, sender uint32) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		packets := [][]byte{make([]byte, wireGuardReceiveBufferSize)}
		sizes := make([]int, 1)
		endpoints := make([]conn.Endpoint, 1)
		count, err := receive(packets, sizes, endpoints)
		if err == nil && (count != 1 || sizes[0] != wireGuardInitiationSize || binary.LittleEndian.Uint32(packets[0][4:8]) != sender) {
			err = fmt.Errorf("received count=%d size=%d sender=%d", count, sizes[0], binary.LittleEndian.Uint32(packets[0][4:8]))
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("receive() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for WireGuard process packet")
	}
}
