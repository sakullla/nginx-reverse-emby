package service

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestParseWireGuardURIParsesOutboundProfile(t *testing.T) {
	raw := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&preshared-key=" + testWireGuardPresharedKey + "&address=10.44.0.2/32,fd44::2/128&allowedips=10.0.0.0/8,fd00::/8&dns=1.1.1.1,2606:4700:4700::1111&mtu=1420&reserved=1,2,255#Edge%20WG"

	parsed, err := ParseWireGuardURI(raw)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}

	if parsed.Name != "Edge WG" {
		t.Fatalf("Name = %q", parsed.Name)
	}
	if parsed.PrivateKey != testWireGuardPrivateKey {
		t.Fatalf("PrivateKey = %q", parsed.PrivateKey)
	}
	if parsed.Endpoint != "edge.example.com:51820" {
		t.Fatalf("Endpoint = %q", parsed.Endpoint)
	}
	if parsed.PublicKey != testWireGuardPublicKey {
		t.Fatalf("PublicKey = %q", parsed.PublicKey)
	}
	if parsed.PresharedKey != testWireGuardPresharedKey {
		t.Fatalf("PresharedKey = %q", parsed.PresharedKey)
	}
	if got := strings.Join(parsed.Addresses, ","); got != "10.44.0.2/32,fd44::2/128" {
		t.Fatalf("Addresses = %q", got)
	}
	if got := strings.Join(parsed.AllowedIPs, ","); got != "10.0.0.0/8,fd00::/8" {
		t.Fatalf("AllowedIPs = %q", got)
	}
	if got := strings.Join(parsed.DNS, ","); got != "1.1.1.1,2606:4700:4700::1111" {
		t.Fatalf("DNS = %q", got)
	}
	if parsed.MTU != 1420 {
		t.Fatalf("MTU = %d", parsed.MTU)
	}
	if got := parsed.Reserved; len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 255 {
		t.Fatalf("Reserved = %+v", got)
	}
}

func TestParseWireGuardURIDefaultsAllowedIPs(t *testing.T) {
	raw := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32"

	parsed, err := ParseWireGuardURI(raw)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}

	if got := strings.Join(parsed.AllowedIPs, ","); got != "0.0.0.0/0,::/0" {
		t.Fatalf("AllowedIPs = %q", got)
	}
}

func TestParseWireGuardURIAcceptsAllowedIPsHyphenatedField(t *testing.T) {
	raw := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32&allowed-ips=10.0.0.0/8,fd00::/8"

	parsed, err := ParseWireGuardURI(raw)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	if got := strings.Join(parsed.AllowedIPs, ","); got != "10.0.0.0/8,fd00::/8" {
		t.Fatalf("AllowedIPs = %q", got)
	}
}

func TestWireGuardProfileInputFromURIRejectsReserved(t *testing.T) {
	raw := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&preshared-key=" + testWireGuardPresharedKey + "&address=10.44.0.2/32&reserved=1,2,3#Edge"

	parsed, err := ParseWireGuardURI(raw)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	_, err = WireGuardProfileInputFromURI(parsed, "")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("WireGuardProfileInputFromURI() error = %v, want ErrInvalidArgument", err)
	}
	if !strings.Contains(err.Error(), "reserved is not supported") {
		t.Fatalf("WireGuardProfileInputFromURI() error = %v, want reserved unsupported message", err)
	}
}

func TestParseWireGuardURIPreservesPlusSignsInKeys(t *testing.T) {
	privateKey := "AAcOFRwjKjE4P0ZNVFtiaXB3foWMk5qhqK+2vcTL0tk="
	publicKey := "BQwTGiEoLzY9REtSWWBnbnV8g4qRmJ+mrbS7wsnQ194="
	presharedKey := "Bg0UGyIpMDc+RUxTWmFob3Z9hIuSmaCnrrW8w8rR2N8="
	raw := "wireguard://" + privateKey + "@edge.example.com:51820?publickey=" + publicKey + "&preshared-key=" + presharedKey + "&address=10.44.0.2/32"

	parsed, err := ParseWireGuardURI(raw)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}

	if parsed.PrivateKey != privateKey || parsed.PublicKey != publicKey || parsed.PresharedKey != presharedKey {
		t.Fatalf("keys = private %q public %q psk %q", parsed.PrivateKey, parsed.PublicKey, parsed.PresharedKey)
	}
}

func TestParseWireGuardURIAcceptsLegacyPSKField(t *testing.T) {
	raw := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&psk=" + testWireGuardPresharedKey + "&address=10.44.0.2/32"

	parsed, err := ParseWireGuardURI(raw)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	if parsed.PresharedKey != testWireGuardPresharedKey {
		t.Fatalf("PresharedKey = %q", parsed.PresharedKey)
	}
}

func TestParseWireGuardURIRejectsMissingRequiredFields(t *testing.T) {
	tests := []string{
		"http://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32",
		"wireguard://edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32",
		"wireguard://" + testWireGuardPrivateKey + "@:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32",
		"wireguard://" + testWireGuardPrivateKey + "@edge.example.com?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32",
		"wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?address=10.44.0.2/32",
		"wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey,
		"wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&address=10.44.0.2/32&reserved=1,256",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			_, err := ParseWireGuardURI(raw)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ParseWireGuardURI() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestWireGuardURIPreviewRedactsSecrets(t *testing.T) {
	raw := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&preshared-key=" + testWireGuardPresharedKey + "&address=10.44.0.2/32&dns=1.1.1.1#Edge"

	preview, err := RedactWireGuardURIPreview(raw)
	if err != nil {
		t.Fatalf("RedactWireGuardURIPreview() error = %v", err)
	}

	if strings.Contains(preview, testWireGuardPrivateKey) || strings.Contains(preview, testWireGuardPresharedKey) {
		t.Fatalf("preview leaked secret: %s", preview)
	}
	for _, want := range []string{"wireguard://xxxxx@edge.example.com:51820", "publickey=" + url.QueryEscape(testWireGuardPublicKey), "preshared-key=xxxxx", "address=10.44.0.2%2F32", "#Edge"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview = %s, missing %q", preview, want)
		}
	}
}
