package relay

import "testing"

func TestRelayTCPStaysPlain(t *testing.T) {
	dialer := newRelayTCPDialer()
	if dialer.MultipathTCP() {
		t.Fatal("relay dialer enables MPTCP")
	}
	listenConfig := newRelayTCPListenConfig()
	if listenConfig.MultipathTCP() {
		t.Fatal("relay listener enables MPTCP")
	}
}
