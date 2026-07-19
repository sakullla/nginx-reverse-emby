package wireguard

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
)

func TestWireGuardModuleStoresProcessPacketRegistry(t *testing.T) {
	mod := NewManagedModule(nil)
	registry := ingress.NewProcessPacketRegistry()
	mod.SetProcessPacketRegistry(registry)
	if mod.ingress.processPackets != registry {
		t.Fatal("SetProcessPacketRegistry() did not configure the production ingress manager")
	}
}
