package l4

import "testing"

func TestAllowsUDPDirect(t *testing.T) {
	if err := ValidateRule(Rule{Protocol: "udp", RelayChain: nil}); err != nil {
		t.Fatalf("expected udp direct to be allowed: %v", err)
	}
}

func TestAllowsUDPDirectWithEmptyRelayChain(t *testing.T) {
	if err := ValidateRule(Rule{Protocol: "udp", RelayChain: []int{}}); err != nil {
		t.Fatalf("expected udp direct with empty relay chain to be allowed: %v", err)
	}
}

func TestRejectsUDPRelay(t *testing.T) {
	if err := ValidateRule(Rule{Protocol: "udp", RelayChain: []int{1}}); err == nil {
		t.Fatal("expected udp relay to be rejected")
	}
}

func TestRejectsUDPRelayCaseInsensitive(t *testing.T) {
	if err := ValidateRule(Rule{Protocol: "UDP", RelayChain: []int{1}}); err == nil {
		t.Fatal("expected udp relay to be rejected regardless of protocol case")
	}
}
