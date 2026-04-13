package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	"github.com/quic-go/quic-go"
)

func TestDialQUICRoundTripTCP(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-quic", "pin_only", true, false)
	listener.TransportMode = "quic"
	listener.AllowTransportFallback = false
	hop.Listener = listener

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	if len(server.quicListeners) != 1 {
		t.Fatalf("quic listener count = %d", len(server.quicListeners))
	}

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, []byte("quic-round-trip"))
}

func TestDialFallsBackToTLSTCP(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-fallback", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	hop.Listener.TransportMode = "quic"
	hop.Listener.AllowTransportFallback = true

	prevDial := quicDialAddr
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		return nil, errors.New("quic unavailable")
	}
	defer func() {
		quicDialAddr = prevDial
	}()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, []byte("fallback-round-trip"))
}
