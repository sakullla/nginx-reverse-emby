package wireguard

import (
	"bytes"
	"encoding/binary"
	"errors"
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

	if err := child.Close(); err != nil {
		t.Fatalf("child Close() error = %v", err)
	}
	if err := parentRegistry.Resume(); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	afterAbort := dialWireGuardProcessClient(t, port)
	defer afterAbort.Close()
	sendWireGuardInitiation(t, afterAbort, 31)
	readWireGuardProcessPacket(t, parentReceive[0], 31)

	secondBundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatalf("second Export() error = %v", err)
	}
	defer secondBundle.Close()
	successorRegistry := ingress.NewProcessPacketRegistry()
	successorSet, err := successorRegistry.Import(secondBundle.Descriptors, secondBundle.Files)
	if err != nil {
		t.Fatalf("successor Import() error = %v", err)
	}
	defer successorSet.Close()
	defer successorRegistry.Close()
	successor := newProcessWireGuardBind(successorRegistry, identity, []string{"127.0.0.1"})
	successorReceive, successorPort, err := successor.Open(port)
	if err != nil {
		t.Fatalf("successor Open() error = %v", err)
	}
	defer successor.Close()
	if successorPort != port {
		t.Fatalf("successor port = %d, want %d", successorPort, port)
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
	afterAuthority := dialWireGuardProcessClient(t, port)
	defer afterAuthority.Close()
	sendWireGuardInitiation(t, afterAuthority, 41)
	readWireGuardProcessPacket(t, successorReceive[0], 41)
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
	if err := classifier.observeSend([][]byte{initiation}, firstRemote); err != nil {
		t.Fatalf("observeSend() error = %v", err)
	}

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

func TestProcessWireGuardClassifierKeepsActiveReceiverAtLimitAcrossRoaming(t *testing.T) {
	classifier := newProcessWireGuardClassifier()
	var released []ingress.AssociationKey
	classifier.setReleaser(func(key ingress.AssociationKey) { released = append(released, key) })
	originalRemote := netip.MustParseAddrPort("127.0.0.1:51820")
	originalKey := ingress.AssociationKey("wireguard|" + originalRemote.String())
	classifier.remotes[originalRemote.String()] = originalKey
	classifier.remoteFIFO = append(classifier.remoteFIFO, originalRemote.String())
	for receiver := uint32(1); receiver <= wireGuardAssociationLimit; receiver++ {
		classifier.receivers[receiver] = originalKey
	}

	newInitiation := make([]byte, wireGuardInitiationSize)
	binary.LittleEndian.PutUint32(newInitiation[:4], wireGuardMessageInitiation)
	binary.LittleEndian.PutUint32(newInitiation[4:8], uint32(wireGuardAssociationLimit+1))
	if err := classifier.observeSend([][]byte{newInitiation}, originalRemote); !errors.Is(err, errWireGuardAssociationLimit) {
		t.Fatalf("observeSend() error = %v, want association limit", err)
	}
	if got := classifier.receivers[1]; got != originalKey {
		t.Fatalf("active receiver key = %q, want %q", got, originalKey)
	}
	if len(released) != 0 {
		t.Fatalf("receiver-limit rejection released live keys: %v", released)
	}

	transport := make([]byte, wireGuardTransportMinSize)
	binary.LittleEndian.PutUint32(transport[:4], wireGuardMessageTransport)
	binary.LittleEndian.PutUint32(transport[4:8], 1)
	key, ok := classifier.Classify(transport, ingress.PacketMetadata{
		Network:    "udp",
		LocalAddr:  &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 51820},
		RemoteAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 51821},
	})
	if !ok || key != originalKey {
		t.Fatalf("Classify() = %q, %t, want active receiver pin %q", key, ok, originalKey)
	}

	receivers := make([]uint32, 0, wireGuardAssociationLimit)
	for receiver := uint32(1); receiver <= wireGuardAssociationLimit; receiver++ {
		receivers = append(receivers, receiver)
	}
	classifier.releaseAssociations(receivers, []string{originalRemote.String()})
	if len(classifier.receivers) != 0 || len(classifier.remotes) != 0 {
		t.Fatalf("releaseAssociations() left %d receivers and %d remotes", len(classifier.receivers), len(classifier.remotes))
	}
	if err := classifier.observeSend([][]byte{newInitiation}, originalRemote); err != nil {
		t.Fatalf("observeSend() after lifecycle release error = %v", err)
	}
}

func TestWireGuardEndpointReleaseLinearizesSuccessorReceiverClaim(t *testing.T) {
	const remote = "127.0.0.1:51820"
	key := ingress.AssociationKey("wireguard|" + remote)
	classifier := newProcessWireGuardClassifier()
	classifier.remotes[remote] = key
	classifier.remoteFIFO = append(classifier.remoteFIFO, remote)
	classifier.receivers[7] = key
	physical := &blockingProcessWireGuardBind{
		classifier:     classifier,
		releaseStarted: make(chan struct{}),
		allowRelease:   make(chan struct{}),
		sent:           make(chan struct{}, 1),
	}
	manager := &wireGuardIngressManager{}
	broker := &wireGuardBindBroker{
		manager: manager, physical: physical,
		endpoints: make(map[*wireGuardBindEndpoint]struct{}),
		receivers: make(map[uint32]*wireGuardBindEndpoint),
		remotes:   make(map[string][]*wireGuardBindEndpoint),
	}
	oldEndpoint := &wireGuardBindEndpoint{binding: broker, opened: true, associations: make(map[string]*wireGuardAssociation)}
	successor := &wireGuardBindEndpoint{binding: broker, opened: true, associations: make(map[string]*wireGuardAssociation)}
	broker.endpoints[oldEndpoint] = struct{}{}
	broker.endpoints[successor] = struct{}{}
	broker.receivers[7] = oldEndpoint
	broker.remotes[remote] = []*wireGuardBindEndpoint{oldEndpoint, successor}
	broker.remoteCount = 2

	released := make(chan struct{})
	go func() {
		broker.releaseEndpoint(oldEndpoint)
		close(released)
	}()
	<-physical.releaseStarted
	payload := make([]byte, wireGuardInitiationSize)
	binary.LittleEndian.PutUint32(payload[:4], wireGuardMessageInitiation)
	binary.LittleEndian.PutUint32(payload[4:8], 7)
	sendDone := make(chan error, 1)
	go func() {
		sendDone <- broker.send(successor, [][]byte{payload}, &conn.StdNetEndpoint{AddrPort: netip.MustParseAddrPort(remote)})
	}()
	select {
	case <-physical.sent:
		t.Fatal("successor send crossed retiring endpoint classifier cleanup")
	case <-time.After(100 * time.Millisecond):
	}
	close(physical.allowRelease)
	<-released
	if err := <-sendDone; err != nil {
		t.Fatalf("successor send error = %v", err)
	}
	select {
	case <-physical.sent:
	case <-time.After(time.Second):
		t.Fatal("successor send did not resume after cleanup")
	}
	if got := classifier.receivers[7]; got != key {
		t.Fatalf("successor receiver key = %q, want %q", got, key)
	}
}

type blockingProcessWireGuardBind struct {
	classifier     *processWireGuardClassifier
	releaseStarted chan struct{}
	allowRelease   chan struct{}
	sent           chan struct{}
}

func (*blockingProcessWireGuardBind) Open(uint16) ([]conn.ReceiveFunc, uint16, error) {
	return nil, 0, nil
}
func (*blockingProcessWireGuardBind) Close() error         { return nil }
func (*blockingProcessWireGuardBind) SetMark(uint32) error { return nil }
func (b *blockingProcessWireGuardBind) Send(payloads [][]byte, endpoint conn.Endpoint) error {
	destination, err := endpointAddrPort(endpoint)
	if err != nil {
		return err
	}
	if err := b.classifier.observeSend(payloads, destination); err != nil {
		return err
	}
	b.sent <- struct{}{}
	return nil
}
func (*blockingProcessWireGuardBind) ParseEndpoint(raw string) (conn.Endpoint, error) {
	addrPort, err := netip.ParseAddrPort(raw)
	if err != nil {
		return nil, err
	}
	return &conn.StdNetEndpoint{AddrPort: addrPort}, nil
}
func (*blockingProcessWireGuardBind) BatchSize() int { return 1 }
func (b *blockingProcessWireGuardBind) releaseAssociations(receivers []uint32, remotes []string) {
	close(b.releaseStarted)
	<-b.allowRelease
	b.classifier.releaseAssociations(receivers, remotes)
}

func TestProcessWireGuardClassifierDropsUnknownPeerWhenAllPinsAreActive(t *testing.T) {
	classifier := newProcessWireGuardClassifier()
	for index := 0; index < wireGuardAssociationLimit; index++ {
		remote := netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, byte(index >> 8), byte(index), 1}), uint16(10000+index))
		key := ingress.AssociationKey("wireguard|" + remote.String())
		classifier.remotes[remote.String()] = key
		classifier.remoteFIFO = append(classifier.remoteFIFO, remote.String())
		classifier.receivers[uint32(index+1)] = key
	}
	unknown := &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 51820}
	payload := make([]byte, wireGuardInitiationSize)
	binary.LittleEndian.PutUint32(payload[:4], wireGuardMessageInitiation)
	key, ok := classifier.Classify(payload, ingress.PacketMetadata{Network: "udp", RemoteAddr: unknown})
	if !ok || key != ingress.AssociationKey("wireguard|"+unknown.String()) {
		t.Fatalf("Classify() = %q, %t, want isolated overflow key", key, ok)
	}
	if classifier.admitInbound(payload, unknown) {
		t.Fatal("unknown peer was admitted while every bounded association pin was active")
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
