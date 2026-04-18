package l4

import (
	"bytes"
	"net"
	"testing"
)

func TestParseProxyProtocolV1(t *testing.T) {
	header := []byte("PROXY TCP4 198.51.100.10 203.0.113.20 12345 443\r\npayload")
	info, payload, err := parseProxyHeader(bytes.NewReader(header))
	if err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	if info.Source.String() != "198.51.100.10:12345" {
		t.Fatalf("unexpected source: %s", info.Source.String())
	}
	if info.Destination.String() != "203.0.113.20:443" {
		t.Fatalf("unexpected destination: %s", info.Destination.String())
	}
	if payload != nil {
		t.Fatalf("unexpected payload: %q", string(payload))
	}
}

func TestBuildProxyProtocolV2Frame(t *testing.T) {
	frame, err := buildProxyHeader(proxyInfo{
		Source:      mustTCPAddr(t, "198.51.100.10:12345"),
		Destination: mustTCPAddr(t, "203.0.113.20:443"),
		Version:     2,
	})
	if err != nil {
		t.Fatalf("build v2: %v", err)
	}
	if !bytes.HasPrefix(frame, []byte{0x0d, 0x0a, 0x0d, 0x0a}) {
		t.Fatalf("missing proxy v2 signature")
	}
}

func TestParseProxyProtocolV1Unknown(t *testing.T) {
	header := []byte("PROXY UNKNOWN\r\npayload")
	info, payload, err := parseProxyHeader(bytes.NewReader(header))
	if err != nil {
		t.Fatalf("parse v1 unknown: %v", err)
	}
	if info != nil {
		t.Fatalf("expected UNKNOWN header to return no proxy tuple, got %#v", info)
	}
	if payload != nil {
		t.Fatalf("unexpected payload: %q", string(payload))
	}
}

func TestParseProxyHeaderDoesNotCopyBufferedPayloadWhenHeaderDecoded(t *testing.T) {
	header := []byte("PROXY TCP4 198.51.100.10 203.0.113.20 12345 443\r\npayload")
	info, payload, err := parseProxyHeader(bytes.NewReader(header))
	if err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	if info == nil {
		t.Fatal("expected parsed proxy info")
	}
	if payload != nil {
		t.Fatalf("expected no copied payload buffer, got %q", string(payload))
	}
}

func mustTCPAddr(t *testing.T, raw string) *net.TCPAddr {
	t.Helper()

	addr, err := net.ResolveTCPAddr("tcp", raw)
	if err != nil {
		t.Fatalf("resolve tcp addr %q: %v", raw, err)
	}
	return addr
}
