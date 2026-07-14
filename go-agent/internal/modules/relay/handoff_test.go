package relay

import (
	"context"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
)

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
