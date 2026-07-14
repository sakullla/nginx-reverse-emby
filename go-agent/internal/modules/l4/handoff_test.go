package l4

import (
	"context"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

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
