package http

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

func TestHTTP3ProcessPacketHandoffRoutesOldNewAndAbort(t *testing.T) {
	if !platform.SupportsHotRestart() {
		t.Skip("packet FD handoff is unsupported on this platform")
	}
	spec := runtimeListenerSpec{address: "127.0.0.1:0", bindingKey: "https:handoff", scheme: "https"}
	parentRegistry := ingress.NewProcessPacketRegistry()
	parent := newHTTPIngressManager()
	parent.processPackets = parentRegistry
	parentLease, err := parent.acquire(t.Context(), "parent", spec, Providers{}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer parentLease.release()
	defer parent.close()
	defer parentRegistry.Close()
	if _, err := parentLease.activate(); err != nil {
		t.Fatal(err)
	}
	oldClient := dialHTTPHandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer oldClient.Close()
	writeHTTPHandoffPacket(t, oldClient, "old-before")
	readHTTPHandoffPacket(t, parentLease.packet, "old-before")

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
	child := newHTTPIngressManager()
	child.processPackets = childRegistry
	childLease, err := child.acquire(t.Context(), "child", spec, Providers{}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer childLease.release()
	defer child.close()
	if got, want := childLease.binding.packet.LocalAddr().String(), parentLease.binding.packet.LocalAddr().String(); got != want {
		t.Fatalf("child packet address = %s, want inherited %s", got, want)
	}
	if _, err := childLease.activate(); err != nil {
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
	writeHTTPHandoffPacket(t, oldClient, "old-during")
	readHTTPHandoffPacket(t, parentLease.packet, "old-during")
	newClient := dialHTTPHandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer newClient.Close()
	writeHTTPHandoffPacket(t, newClient, "new-forwarded")
	readHTTPHandoffPacket(t, childLease.packet, "new-forwarded")

	if err := parentRegistry.Resume(); err != nil {
		t.Fatal(err)
	}
	afterAbort := dialHTTPHandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer afterAbort.Close()
	writeHTTPHandoffPacket(t, afterAbort, "after-abort")
	readHTTPHandoffPacket(t, parentLease.packet, "after-abort")
}

func TestHTTPIngressConsumesProcessPacketDescriptor(t *testing.T) {
	registry := ingress.NewProcessPacketRegistry()
	set, err := registry.Import(nil, nil)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	defer set.Close()
	defer registry.Close()

	mod := NewModule(Config{})
	mod.SetProcessPacketRegistry(registry)
	lease, err := mod.ingress.acquire(context.Background(), "generation-2", runtimeListenerSpec{
		address: "127.0.0.1:0", bindingKey: "https:127.0.0.1:0", scheme: "https",
	}, Providers{}, true)
	if lease != nil {
		_ = lease.release()
	}
	if err == nil || !strings.Contains(err.Error(), `inherited packet descriptor "http:https:127.0.0.1:0" is missing`) {
		t.Fatalf("acquire() error = %v, want missing process packet descriptor", err)
	}
}

func dialHTTPHandoffClient(t *testing.T, target net.Addr) *net.UDPConn {
	t.Helper()
	client, err := net.DialUDP("udp", nil, target.(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeHTTPHandoffPacket(t *testing.T, client *net.UDPConn, payload string) {
	t.Helper()
	if _, err := client.Write(httpHandoffPacket(payload)); err != nil {
		t.Fatal(err)
	}
}

func readHTTPHandoffPacket(t *testing.T, endpoint *ingress.PacketEndpoint, want string) {
	t.Helper()
	if err := endpoint.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 256)
	n, _, err := endpoint.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	wantPacket := httpHandoffPacket(want)
	if got := string(buffer[:n]); got != string(wantPacket) {
		t.Fatalf("packet = %x, want %x", buffer[:n], wantPacket)
	}
}

func httpHandoffPacket(label string) []byte {
	cid := []byte(label)
	packet := []byte{0xc0, 0, 0, 0, 1, byte(len(cid))}
	return append(packet, cid...)
}
