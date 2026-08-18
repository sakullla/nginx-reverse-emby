package pluginsdk

import "testing"

func TestNodeAddressesFromHeartbeatUsesReportedIPs(t *testing.T) {
	got := NodeAddressesFromHeartbeat(" ss.example.com ", " 203.0.113.10 ", " 2001:db8::1 ")
	if got.DDNS != "ss.example.com" || got.IPv4 != "203.0.113.10" || got.IPv6 != "2001:db8::1" {
		t.Fatalf("got %#v", got)
	}
	host, source, ok := got.SelectShareHost()
	if !ok || host != "ss.example.com" || source != ShareHostSourceDDNS {
		t.Fatalf("got host=%q source=%q ok=%v", host, source, ok)
	}
	host, source, ok = NodeAddressesFromHeartbeat("", "203.0.113.10", "2001:db8::1").SelectShareHost()
	if !ok || host != "203.0.113.10" || source != ShareHostSourceIPv4 {
		t.Fatalf("fallback host=%q source=%q ok=%v", host, source, ok)
	}
}

func TestSelectShareHostPrefersDDNSThenIPv4ThenIPv6(t *testing.T) {
	host, source, ok := (NodeAddresses{DDNS: "ss.example.com", IPv4: "203.0.113.10", IPv6: "2001:db8::1"}).SelectShareHost()
	if !ok || host != "ss.example.com" || source != ShareHostSourceDDNS {
		t.Fatalf("got host=%q source=%q ok=%v", host, source, ok)
	}
	host, source, ok = (NodeAddresses{IPv4: "203.0.113.10", IPv6: "2001:db8::1"}).SelectShareHost()
	if !ok || host != "203.0.113.10" || source != ShareHostSourceIPv4 {
		t.Fatalf("got host=%q source=%q ok=%v", host, source, ok)
	}
	host, source, ok = (NodeAddresses{IPv6: "2001:db8::1"}).SelectShareHost()
	if !ok || host != "2001:db8::1" || source != ShareHostSourceIPv6 {
		t.Fatalf("got host=%q source=%q ok=%v", host, source, ok)
	}
}

func TestSelectShareHostRejectsWildcardLoopbackAndEmpty(t *testing.T) {
	for _, addresses := range []NodeAddresses{
		{DDNS: "0.0.0.0", IPv4: "127.0.0.1", IPv6: "::1"},
		{DDNS: "0.0.0.0.", IPv4: "127.0.0.1.", IPv6: "::1."},
		{DDNS: "::", IPv4: "localhost"},
		{},
	} {
		if host, source, ok := addresses.SelectShareHost(); ok {
			t.Fatalf("accepted host=%q source=%q from %#v", host, source, addresses)
		}
	}
}

func TestShareableHostStripsBracketsAndTrailingDot(t *testing.T) {
	host, ok := ShareableHost("[2001:db8::2]")
	if !ok || host != "2001:db8::2" {
		t.Fatalf("got host=%q ok=%v", host, ok)
	}
	host, ok = ShareableHost("edge.example.com.")
	if !ok || host != "edge.example.com" {
		t.Fatalf("got host=%q ok=%v", host, ok)
	}
}
