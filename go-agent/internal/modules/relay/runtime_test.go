package relay

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestNormalizeListenerDerivesRepresentativeRelayEndpoint(t *testing.T) {
	normalized, err := normalizeListener(Listener{
		ID: 1, AgentID: "agent-a", Name: "relay-a",
		BindHosts: []string{"127.0.0.1", "127.0.0.2"}, ListenPort: 18443, Enabled: true,
		TLSMode: "pin_only", PinSet: []model.RelayPin{{Type: "spki_sha256", Value: "cGlubmVk"}},
	})
	if err != nil {
		t.Fatalf("normalizeListener() error = %v", err)
	}
	if normalized.ListenHost != "127.0.0.1" || normalized.PublicHost != "127.0.0.1" || normalized.PublicPort != 18443 {
		t.Fatalf("normalized listener = %+v", normalized)
	}
}
