package relay

import (
	"context"
	"testing"
)

func TestServerTLSConfigDisablesDynamicRecordSizing(t *testing.T) {
	provider := newFakeTLSMaterialProvider()
	listener, _ := newRelayEndpoint(t, provider, 1, "server-dynamic-record-sizing", "pin_only", true, false)

	cfg, err := serverTLSConfig(context.Background(), provider, listener)
	if err != nil {
		t.Fatalf("serverTLSConfig() error = %v", err)
	}
	if !cfg.DynamicRecordSizingDisabled {
		t.Fatal("serverTLSConfig() should disable dynamic record sizing for relay traffic")
	}
}

func TestClientTLSConfigDisablesDynamicRecordSizing(t *testing.T) {
	provider := newFakeTLSMaterialProvider()
	_, hop := newRelayEndpoint(t, provider, 1, "client-dynamic-record-sizing", "pin_only", true, false)

	cfg, err := clientTLSConfig(context.Background(), provider, hop.Listener, hop.Address, hop.ServerName)
	if err != nil {
		t.Fatalf("clientTLSConfig() error = %v", err)
	}
	if !cfg.DynamicRecordSizingDisabled {
		t.Fatal("clientTLSConfig() should disable dynamic record sizing for relay traffic")
	}
}
