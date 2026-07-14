package l4

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

func TestL4UDPProcessPacketHandoffRoutesOldNewAndAbort(t *testing.T) {
	if !platform.SupportsHotRestart() {
		t.Skip("packet FD handoff is unsupported on this platform")
	}
	rule := model.L4Rule{ID: 7, Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 0, Enabled: true}
	parentRegistry := ingress.NewProcessPacketRegistry()
	parent := newL4IngressManager()
	parent.processPackets = parentRegistry
	parentLease, err := parent.acquire(t.Context(), "parent", rule, &Server{})
	if err != nil {
		t.Fatal(err)
	}
	defer parentLease.release()
	defer parent.close()
	defer parentRegistry.Close()
	parentLease.binding.packet.SetSelector(nil)
	if _, err := parentLease.binding.packet.Activate(parentLease.packet); err != nil {
		t.Fatal(err)
	}
	oldClient := dialL4HandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer oldClient.Close()
	writeL4HandoffPacket(t, oldClient, "old-before")
	readL4HandoffPacket(t, parentLease.packet, "old-before")

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
	child := newL4IngressManager()
	child.processPackets = childRegistry
	childLease, err := child.acquire(t.Context(), "child", rule, &Server{})
	if err != nil {
		t.Fatal(err)
	}
	defer childLease.release()
	defer child.close()
	if got, want := childLease.binding.packet.LocalAddr().String(), parentLease.binding.packet.LocalAddr().String(); got != want {
		t.Fatalf("child packet address = %s, want inherited %s", got, want)
	}
	childLease.binding.packet.SetSelector(nil)
	if _, err := childLease.binding.packet.Activate(childLease.packet); err != nil {
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
	writeL4HandoffPacket(t, oldClient, "old-during")
	readL4HandoffPacket(t, parentLease.packet, "old-during")
	newClient := dialL4HandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer newClient.Close()
	writeL4HandoffPacket(t, newClient, "new-forwarded")
	readL4HandoffPacket(t, childLease.packet, "new-forwarded")

	if err := parentRegistry.Resume(); err != nil {
		t.Fatal(err)
	}
	afterAbort := dialL4HandoffClient(t, parentLease.binding.packet.LocalAddr())
	defer afterAbort.Close()
	writeL4HandoffPacket(t, afterAbort, "after-abort")
	readL4HandoffPacket(t, parentLease.packet, "after-abort")
}

func TestL4UDPIngressConsumesProcessPacketDescriptor(t *testing.T) {
	registry := ingress.NewProcessPacketRegistry()
	set, err := registry.Import(nil, nil)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	defer set.Close()
	defer registry.Close()

	mod := NewModule(Config{})
	mod.SetProcessPacketRegistry(registry)
	rule := model.L4Rule{ID: 7, Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 0, Enabled: true}
	lease, err := mod.ingress.acquire(context.Background(), "generation-2", rule, &Server{})
	if lease != nil {
		_ = lease.release()
	}
	if err == nil || !strings.Contains(err.Error(), `inherited packet descriptor "l4:`) || !strings.Contains(err.Error(), `is missing`) {
		t.Fatalf("acquire() error = %v, want missing process packet descriptor", err)
	}
}

func dialL4HandoffClient(t *testing.T, target net.Addr) *net.UDPConn {
	t.Helper()
	client, err := net.DialUDP("udp", nil, target.(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeL4HandoffPacket(t *testing.T, client *net.UDPConn, payload string) {
	t.Helper()
	if _, err := client.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
}

func readL4HandoffPacket(t *testing.T, endpoint *ingress.PacketEndpoint, want string) {
	t.Helper()
	if err := endpoint.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 256)
	n, _, err := endpoint.ReadFrom(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != want {
		t.Fatalf("packet = %q, want %q", got, want)
	}
}
