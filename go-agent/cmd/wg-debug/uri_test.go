package main

import (
	"testing"
)

func TestParseWireGuardURI(t *testing.T) {
	const raw = "wireguard://AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=@192.0.2.109:51820?address=10.8.0.3%2F32&allowed-ips=0.0.0.0%2F0%2C%3A%3A%2F0&dns=10.8.0.1%2C1.1.1.1&mtu=1280&preshared-key=CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCAB%3D&publickey=BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB%3D#test2"

	profile, err := parseWireGuardURI(raw)
	if err != nil {
		t.Fatalf("parseWireGuardURI() error = %v", err)
	}

	if profile.Name != "test2" {
		t.Fatalf("Name = %q, want test2", profile.Name)
	}
	if profile.PrivateKey != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatal("private key was not parsed from URI userinfo")
	}
	if profile.Peers[0].Endpoint != "192.0.2.109:51820" {
		t.Fatalf("Endpoint = %q, want 192.0.2.109:51820", profile.Peers[0].Endpoint)
	}
	if profile.Addresses[0] != "10.8.0.3/32" {
		t.Fatalf("Address = %q, want 10.8.0.3/32", profile.Addresses[0])
	}
	if profile.MTU != 1280 {
		t.Fatalf("MTU = %d, want 1280", profile.MTU)
	}
	if got := profile.DNS; len(got) != 2 || got[0] != "10.8.0.1" || got[1] != "1.1.1.1" {
		t.Fatalf("DNS = %#v, want tunnel DNS values", got)
	}
	if got := profile.Peers[0].AllowedIPs; len(got) != 2 || got[0] != "0.0.0.0/0" || got[1] != "::/0" {
		t.Fatalf("AllowedIPs = %#v, want v4/v6 defaults", got)
	}
	if profile.Peers[0].PublicKey != "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=" {
		t.Fatal("public key was not parsed from query")
	}
	if profile.Peers[0].PresharedKey != "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCAB=" {
		t.Fatal("preshared key was not parsed from query")
	}
}

func TestParseWireGuardURIRejectsMissingSecret(t *testing.T) {
	_, err := parseWireGuardURI("wireguard://@192.0.2.109:51820?address=10.8.0.3%2F32&publickey=BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB%3D")
	if err == nil {
		t.Fatal("parseWireGuardURI() error = nil, want missing private key error")
	}
}

func TestDialAddressFromResolvedIP(t *testing.T) {
	got, err := dialAddressFromResolvedIP("www.example.com:8080", "203.0.113.9")
	if err != nil {
		t.Fatalf("dialAddressFromResolvedIP() error = %v", err)
	}
	if got != "203.0.113.9:8080" {
		t.Fatalf("dial address = %q, want 203.0.113.9:8080", got)
	}
}

func TestShouldResolveHTTPWithSystemDNSDefaultsFalse(t *testing.T) {
	if shouldResolveHTTPWithSystemDNS(false, false) {
		t.Fatal("shouldResolveHTTPWithSystemDNS() = true, want default false for transparent proxy targets")
	}
	if !shouldResolveHTTPWithSystemDNS(true, false) {
		t.Fatal("shouldResolveHTTPWithSystemDNS(resolve flag) = false, want true")
	}
}

func TestHTTPWarmupTargetUsesURLHostPort(t *testing.T) {
	got, err := httpWarmupTarget("http://www.apple.com/library/test/success.html")
	if err != nil {
		t.Fatalf("httpWarmupTarget() error = %v", err)
	}
	if got != "www.apple.com:80" {
		t.Fatalf("httpWarmupTarget() = %q, want www.apple.com:80", got)
	}
}

func TestHTTPWarmupTargetUsesHTTPSDefaultPort(t *testing.T) {
	got, err := httpWarmupTarget("https://example.com/status")
	if err != nil {
		t.Fatalf("httpWarmupTarget() error = %v", err)
	}
	if got != "example.com:443" {
		t.Fatalf("httpWarmupTarget() = %q, want example.com:443", got)
	}
}

func TestHTTPWarmupTargetPreservesExplicitPort(t *testing.T) {
	got, err := httpWarmupTarget("http://example.com:8080/status")
	if err != nil {
		t.Fatalf("httpWarmupTarget() error = %v", err)
	}
	if got != "example.com:8080" {
		t.Fatalf("httpWarmupTarget() = %q, want example.com:8080", got)
	}
}

func TestRewritePeerEndpointForUDPProxy(t *testing.T) {
	profile, err := parseWireGuardURI("wireguard://AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=@192.0.2.109:51820?address=10.8.0.3%2F32&publickey=BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB%3D")
	if err != nil {
		t.Fatalf("parseWireGuardURI() error = %v", err)
	}

	rewritePeerEndpointForUDPProxy(&profile, "127.0.0.1:32000")

	if profile.Peers[0].Endpoint != "127.0.0.1:32000" {
		t.Fatalf("Endpoint = %q, want proxy endpoint", profile.Peers[0].Endpoint)
	}
}
