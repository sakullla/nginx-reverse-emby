package netutil

import (
	"net/url"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestNormalizeHostTrimsLowercasesAndStripsPort(t *testing.T) {
	if got := NormalizeHost(" Example.COM:8443 "); got != "example.com" {
		t.Fatalf("NormalizeHost() = %q, want %q", got, "example.com")
	}
}

func TestNormalizeHostReturnsIPv6HostWithoutPort(t *testing.T) {
	if got := NormalizeHost("[2001:db8::1]:443"); got != "2001:db8::1" {
		t.Fatalf("NormalizeHost() = %q, want IPv6 host", got)
	}
}

func TestURLAuthorityUsesDefaultPorts(t *testing.T) {
	target, err := url.Parse("https://Example.COM/path")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if got := URLAuthority(target); got != "example.com:443" {
		t.Fatalf("URLAuthority() = %q, want %q", got, "example.com:443")
	}
}

func TestAddressWithDefaultPortPreservesExplicitPort(t *testing.T) {
	target, err := url.Parse("http://example.com:8080/path")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if got := AddressWithDefaultPort(target); got != "example.com:8080" {
		t.Fatalf("AddressWithDefaultPort() = %q, want explicit port", got)
	}
}

func TestRelayListenerDialEndpointPrefersPublicHostAndPort(t *testing.T) {
	host, port := RelayListenerDialEndpoint(model.RelayListener{
		ListenHost: "0.0.0.0",
		BindHosts:  []string{"127.0.0.1"},
		ListenPort: 8443,
		PublicHost: "relay.example.com",
		PublicPort: 9443,
	})
	if host != "relay.example.com" || port != 9443 {
		t.Fatalf("RelayListenerDialEndpoint() = %s:%d, want relay.example.com:9443", host, port)
	}
}

func TestRelayListenerDialEndpointFallsBackToFirstBindHost(t *testing.T) {
	host, port := RelayListenerDialEndpoint(model.RelayListener{
		ListenHost: "0.0.0.0",
		BindHosts:  []string{" ", "127.0.0.2"},
		ListenPort: 8443,
	})
	if host != "127.0.0.2" || port != 8443 {
		t.Fatalf("RelayListenerDialEndpoint() = %s:%d, want 127.0.0.2:8443", host, port)
	}
}

func TestClientIPStripsPortWhenPresent(t *testing.T) {
	if got := ClientIP("192.0.2.10:3456"); got != "192.0.2.10" {
		t.Fatalf("ClientIP() = %q, want host", got)
	}
}
