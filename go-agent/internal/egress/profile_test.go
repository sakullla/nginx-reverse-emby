package egress

import (
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestEgressProfileResolveReturnsDirectProfileForNilAndNonPositiveIDs(t *testing.T) {
	resolver := NewResolver([]model.EgressProfile{{
		ID:      2,
		Name:    "direct",
		Type:    "direct",
		Enabled: true,
	}})

	for _, tc := range []struct {
		name string
		id   *int
	}{
		{name: "nil", id: nil},
		{name: "zero", id: intPtr(0)},
		{name: "negative", id: intPtr(-3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, found, err := resolver.Resolve(tc.id, "tcp4")
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if found {
				t.Fatalf("found = true, want false")
			}
			if profile.Type != "direct" || !profile.Enabled {
				t.Fatalf("profile = %+v, want enabled direct profile", profile)
			}
		})
	}
}

func TestEgressProfileResolveValidatesProfilesByTypeAndNetwork(t *testing.T) {
	socksID := 11
	httpID := 12
	wgID := 13
	directID := 14
	resolver := NewResolver([]model.EgressProfile{
		{ID: directID, Name: "direct", Type: "direct", Enabled: true},
		{ID: socksID, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true},
		{ID: httpID, Name: "http", Type: "http", ProxyURL: "http://127.0.0.1:8080", Enabled: true},
		{ID: wgID, Name: "wg", Type: "wireguard", WireGuardConfig: &model.EgressWireGuardConfig{PrivateKey: "k"}, Enabled: true},
	})

	tests := []struct {
		name      string
		id        *int
		network   string
		wantID    int
		wantFound bool
	}{
		{name: "direct tcp4", id: intPtr(directID), network: "tcp4", wantID: directID, wantFound: true},
		{name: "socks udp6", id: intPtr(socksID), network: "UDP6", wantID: socksID, wantFound: true},
		{name: "http tcp", id: intPtr(httpID), network: "tcp", wantID: httpID, wantFound: true},
		{name: "wireguard udp", id: intPtr(wgID), network: "udp", wantID: wgID, wantFound: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile, found, err := resolver.Resolve(tc.id, tc.network)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v", found, tc.wantFound)
			}
			if profile.ID != tc.wantID {
				t.Fatalf("profile.ID = %d, want %d", profile.ID, tc.wantID)
			}
		})
	}
}

func TestEgressProfileResolveReturnsValidationErrors(t *testing.T) {
	disabledID := 21
	socksID := 22
	httpID := 23
	wgID := 24
	unknownID := 25

	resolver := NewResolver([]model.EgressProfile{
		{ID: disabledID, Name: "disabled", Type: "direct", Enabled: false},
		{ID: socksID, Name: "socks", Type: "socks", Enabled: true},
		{ID: httpID, Name: "http", Type: "http", ProxyURL: "http://proxy.example:8080", Enabled: true},
		{ID: wgID, Name: "wg", Type: "wireguard", Enabled: true},
		{ID: unknownID, Name: "unknown", Type: "something-else", Enabled: true},
	})

	tests := []struct {
		name    string
		id      int
		network string
		wantErr string
	}{
		{name: "missing", id: 999, network: "tcp", wantErr: "egress profile 999 not found"},
		{name: "disabled", id: disabledID, network: "tcp", wantErr: "egress profile 21 is disabled"},
		{name: "socks missing url", id: socksID, network: "tcp", wantErr: "ProxyURL"},
		{name: "http udp", id: httpID, network: "udp6", wantErr: "UDP"},
		{name: "wireguard missing config", id: wgID, network: "tcp", wantErr: "WireGuardConfig"},
		{name: "unknown type", id: unknownID, network: "tcp", wantErr: "unsupported egress profile type"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolver.Resolve(intPtr(tc.id), tc.network)
			if err == nil {
				t.Fatal("Resolve() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Resolve() error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func intPtr(v int) *int {
	return &v
}
