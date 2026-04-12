package relay

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestValidateListener(t *testing.T) {
	t.Parallel()

	validPin := model.RelayPin{Type: "spki_sha256", Value: "cGlubmVk"}
	tests := []struct {
		name     string
		listener Listener
		wantErr  string
	}{
		{
			name: "accepts valid pinned listener",
			listener: Listener{
				ID:         1,
				AgentID:    "agent-a",
				Name:       "relay-a",
				ListenHost: "127.0.0.1",
				ListenPort: 18443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{validPin},
				Revision:   3,
			},
		},
		{
			name: "rejects missing listen host",
			listener: Listener{
				ID:         1,
				AgentID:    "agent-a",
				Name:       "relay-a",
				ListenPort: 18443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{validPin},
			},
			wantErr: "listen_host is required",
		},
		{
			name: "rejects invalid listen port",
			listener: Listener{
				ID:         1,
				AgentID:    "agent-a",
				Name:       "relay-a",
				ListenHost: "127.0.0.1",
				ListenPort: 0,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{validPin},
			},
			wantErr: "listen_port must be between 1 and 65535",
		},
		{
			name: "rejects invalid tls mode",
			listener: Listener{
				ID:         1,
				AgentID:    "agent-a",
				Name:       "relay-a",
				ListenHost: "127.0.0.1",
				ListenPort: 18443,
				Enabled:    true,
				TLSMode:    "unknown",
				PinSet:     []model.RelayPin{validPin},
			},
			wantErr: "unsupported tls_mode",
		},
		{
			name: "rejects missing trust material",
			listener: Listener{
				ID:         1,
				AgentID:    "agent-a",
				Name:       "relay-a",
				ListenHost: "127.0.0.1",
				ListenPort: 18443,
				Enabled:    true,
				TLSMode:    "pin_or_ca",
			},
			wantErr: "pin_set and trusted_ca_certificate_ids cannot both be empty",
		},
		{
			name: "rejects invalid host shape",
			listener: Listener{
				ID:         1,
				AgentID:    "agent-a",
				Name:       "relay-a",
				ListenHost: "bad host",
				ListenPort: 18443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSet:     []model.RelayPin{validPin},
			},
			wantErr: "listen_host must be a valid IP address or hostname",
		},
		{
			name: "rejects pin only without pins",
			listener: Listener{
				ID:                      1,
				AgentID:                 "agent-a",
				Name:                    "relay-a",
				ListenHost:              "127.0.0.1",
				ListenPort:              18443,
				Enabled:                 true,
				TLSMode:                 "pin_only",
				TrustedCACertificateIDs: []int{1},
			},
			wantErr: "pin_only requires pin_set",
		},
		{
			name: "rejects ca only without CA ids",
			listener: Listener{
				ID:         1,
				AgentID:    "agent-a",
				Name:       "relay-a",
				ListenHost: "127.0.0.1",
				ListenPort: 18443,
				Enabled:    true,
				TLSMode:    "ca_only",
				PinSet:     []model.RelayPin{validPin},
			},
			wantErr: "ca_only requires trusted_ca_certificate_ids",
		},
		{
			name: "rejects pin and ca without both",
			listener: Listener{
				ID:         1,
				AgentID:    "agent-a",
				Name:       "relay-a",
				ListenHost: "127.0.0.1",
				ListenPort: 18443,
				Enabled:    true,
				TLSMode:    "pin_and_ca",
				PinSet:     []model.RelayPin{validPin},
			},
			wantErr: "pin_and_ca requires pin_set and trusted_ca_certificate_ids",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateListener(tc.listener)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("ValidateListener returned error: %v", err)
			}
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tc.wantErr)
				}
				if got := err.Error(); got != tc.wantErr {
					t.Fatalf("unexpected error: got %q want %q", got, tc.wantErr)
				}
			}
		})
	}
}

func TestNormalizeListenerDerivesBindAndPublicFields(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeListener(Listener{
		ID:         1,
		AgentID:    "agent-a",
		Name:       "relay-a",
		BindHosts:  []string{"127.0.0.1", "127.0.0.2"},
		ListenPort: 18443,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "spki_sha256",
			Value: "cGlubmVk",
		}},
	})
	if err != nil {
		t.Fatalf("normalizeListener returned error: %v", err)
	}
	if normalized.ListenHost != "127.0.0.1" {
		t.Fatalf("expected listen_host mirror from bind_hosts, got %q", normalized.ListenHost)
	}
	if normalized.PublicHost != "127.0.0.1" {
		t.Fatalf("expected public_host fallback to first bind host, got %q", normalized.PublicHost)
	}
	if normalized.PublicPort != 18443 {
		t.Fatalf("expected public_port fallback to listen_port, got %d", normalized.PublicPort)
	}
}

func TestNormalizeListenerFallsBackBindHostsFromListenHost(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeListener(Listener{
		ID:         1,
		AgentID:    "agent-a",
		Name:       "relay-a",
		ListenHost: "127.0.0.1",
		ListenPort: 18443,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "spki_sha256",
			Value: "cGlubmVk",
		}},
	})
	if err != nil {
		t.Fatalf("normalizeListener returned error: %v", err)
	}
	if len(normalized.BindHosts) != 1 || normalized.BindHosts[0] != "127.0.0.1" {
		t.Fatalf("expected bind_hosts fallback from listen_host, got %+v", normalized.BindHosts)
	}
}

func TestStartBindsAllConfiguredHosts(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-bind-all", "pin_only", true, false)
	listener.BindHosts = []string{"127.0.0.1", "127.0.0.2"}
	listener.ListenHost = ""

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	if len(server.listeners) != 2 {
		t.Fatalf("expected two active listeners for bind_hosts, got %d", len(server.listeners))
	}

	for _, host := range listener.BindHosts {
		testHop := hop
		testHop.Address = net.JoinHostPort(host, fmt.Sprintf("%d", listener.ListenPort))
		testHop.Listener = listener
		testHop.ServerName = ""

		conn, dialErr := Dial(context.Background(), "tcp", backendAddr, []Hop{testHop}, provider)
		if dialErr != nil {
			t.Fatalf("Dial returned error for host %s: %v", host, dialErr)
		}
		assertRoundTrip(t, conn, []byte("bind-all"))
		conn.Close()
	}
}

func TestDialRejectsUDP(t *testing.T) {
	t.Parallel()

	_, err := Dial(context.Background(), "udp", "127.0.0.1:9000", []Hop{{Address: "127.0.0.1:9001"}}, &fakeTLSMaterialProvider{})
	if err == nil {
		t.Fatal("expected udp relay dial to fail")
	}
}

func TestDialFailsClosedWhenHopRejectsTransportMode(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-unsupported-mode", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	_, err = Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider, DialOptions{
		TransportMode: "future_mode",
	})
	if err == nil || !strings.Contains(err.Error(), "future_mode") {
		t.Fatalf("Dial() error = %v", err)
	}
}

func TestOneHopRelayDataFlow(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-one", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, []byte("one-hop"))
}

func TestOneHopRelayDataFlowWithFirstSegmentObfs(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-one-obfs", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider, DialOptions{
		TransportMode: TransportModeFirstSegmentV1,
	})
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, bytes.Repeat([]byte{0x16, 0x03, 0x01, 0x20}, 256))
}

func TestMultiHopRelayDataFlow(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listenerA, hopA := newRelayEndpoint(t, provider, 1, "relay-a", "pin_only", true, false)
	listenerB, hopB := newRelayEndpoint(t, provider, 2, "relay-b", "pin_only", true, false)

	serverA, err := Start(context.Background(), []Listener{listenerA}, provider)
	if err != nil {
		t.Fatalf("failed to start first relay: %v", err)
	}
	defer serverA.Close()

	serverB, err := Start(context.Background(), []Listener{listenerB}, provider)
	if err != nil {
		t.Fatalf("failed to start second relay: %v", err)
	}
	defer serverB.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hopA, hopB}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, []byte("multi-hop"))
}

func TestMultiHopRelayDataFlowWithFirstSegmentObfs(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listenerA, hopA := newRelayEndpoint(t, provider, 1, "relay-a-obfs", "pin_only", true, false)
	listenerB, hopB := newRelayEndpoint(t, provider, 2, "relay-b-obfs", "pin_only", true, false)

	serverA, err := Start(context.Background(), []Listener{listenerA}, provider)
	if err != nil {
		t.Fatalf("failed to start first relay: %v", err)
	}
	defer serverA.Close()

	serverB, err := Start(context.Background(), []Listener{listenerB}, provider)
	if err != nil {
		t.Fatalf("failed to start second relay: %v", err)
	}
	defer serverB.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hopA, hopB}, provider, DialOptions{
		TransportMode: TransportModeFirstSegmentV1,
	})
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, bytes.Repeat([]byte{0x16, 0x03, 0x01, 0x20}, 512))
}

func TestDialSurfacesFinalTargetFailure(t *testing.T) {
	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-final-fail", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	target := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", pickFreeTCPPort(t)))
	conn, err := Dial(context.Background(), "tcp", target, []Hop{hop}, provider)
	if err == nil {
		conn.Close()
		t.Fatal("expected final target dial failure")
	}
}

func TestDialSurfacesDownstreamRelayFailure(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listenerA, hopA := newRelayEndpoint(t, provider, 1, "relay-a", "pin_only", true, false)
	_, hopB := newRelayEndpoint(t, provider, 2, "relay-b", "pin_only", true, false)

	serverA, err := Start(context.Background(), []Listener{listenerA}, provider)
	if err != nil {
		t.Fatalf("failed to start first relay: %v", err)
	}
	defer serverA.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hopA, hopB}, provider)
	if err == nil {
		conn.Close()
		t.Fatal("expected downstream relay dial failure")
	}
}

func TestPinVerificationBehavior(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-pin", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	badHop := hop
	badHop.Listener.PinSet = []model.RelayPin{{Type: "spki_sha256", Value: base64.StdEncoding.EncodeToString([]byte("wrong"))}}

	if _, err := Dial(context.Background(), "tcp", backendAddr, []Hop{badHop}, provider); err == nil {
		t.Fatal("expected pin verification failure")
	}
}

func TestCAVerificationBehavior(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-ca", "ca_only", false, true)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("expected CA verification to succeed: %v", err)
	}
	conn.Close()

	badHop := hop
	badHop.Listener.TrustedCACertificateIDs = []int{999}
	if _, err := Dial(context.Background(), "tcp", backendAddr, []Hop{badHop}, provider); err == nil {
		t.Fatal("expected CA verification failure")
	}
}

func TestPinAndCAVerificationRequiresBoth(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-both", "pin_and_ca", true, true)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("expected pin_and_ca verification to succeed: %v", err)
	}
	conn.Close()

	badPinHop := hop
	badPinHop.Listener.PinSet = []model.RelayPin{{Type: "spki_sha256", Value: base64.StdEncoding.EncodeToString([]byte("wrong"))}}
	if _, err := Dial(context.Background(), "tcp", backendAddr, []Hop{badPinHop}, provider); err == nil {
		t.Fatal("expected pin_and_ca to fail when pin verification fails")
	}

	badCAHop := hop
	badCAHop.Listener.TrustedCACertificateIDs = []int{999}
	if _, err := Dial(context.Background(), "tcp", backendAddr, []Hop{badCAHop}, provider); err == nil {
		t.Fatal("expected pin_and_ca to fail when CA verification fails")
	}
}

func TestPinAndCAVerificationWorksWithDerivedRelayMaterial(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	certificateID := 10
	caID := 100
	cert, parsed := newServerCertificate(t, certificateOptions{
		commonName: "127.0.0.1",
		ipAddrs:    []net.IP{net.ParseIP("127.0.0.1")},
		dnsNames:   []string{"localhost"},
	})
	derivedPin := spkiPin(t, parsed)

	provider.mu.Lock()
	provider.serverCerts[certificateID] = cert
	provider.caCerts[caID] = []*x509.Certificate{parsed}
	provider.mu.Unlock()

	listener := Listener{
		ID:                      1,
		AgentID:                 "agent-1",
		Name:                    "relay-derived",
		ListenHost:              "127.0.0.1",
		ListenPort:              pickFreeTCPPort(t),
		Enabled:                 true,
		CertificateID:           &certificateID,
		TLSMode:                 "pin_and_ca",
		PinSet:                  []model.RelayPin{{Type: "spki_sha256", Value: derivedPin}},
		TrustedCACertificateIDs: []int{caID},
		AllowSelfSigned:         true,
		Tags:                    []string{"relay"},
		Revision:                1,
	}
	hop := Hop{
		Address:    net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort)),
		Listener:   listener,
		ServerName: "127.0.0.1",
	}

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("expected derived pin_and_ca verification to succeed: %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, []byte("derived-pin-and-ca"))
}

func TestPinOrCAVerificationAcceptsEither(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-either", "pin_or_ca", true, true)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	pinOnlyHop := hop
	pinOnlyHop.Listener.TrustedCACertificateIDs = []int{999}
	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{pinOnlyHop}, provider)
	if err != nil {
		t.Fatalf("expected pin_or_ca to accept a valid pin: %v", err)
	}
	conn.Close()

	caOnlyHop := hop
	caOnlyHop.Listener.PinSet = []model.RelayPin{{Type: "spki_sha256", Value: base64.StdEncoding.EncodeToString([]byte("wrong"))}}
	conn, err = Dial(context.Background(), "tcp", backendAddr, []Hop{caOnlyHop}, provider)
	if err != nil {
		t.Fatalf("expected pin_or_ca to accept a valid CA: %v", err)
	}
	conn.Close()
}

func TestCAVerificationSupportsServerNameOverride(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpointWithCert(t, provider, relayEndpointOptions{
		id:           1,
		name:         "relay-sni",
		tlsMode:      "ca_only",
		includeCA:    true,
		serverName:   "relay.internal.test",
		certDNSNames: []string{"relay.internal.test"},
	})

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	hop.ServerName = ""
	if _, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider); err == nil {
		t.Fatal("expected hostname mismatch without server-name override")
	}

	hop.ServerName = "relay.internal.test"
	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("expected server-name override to succeed: %v", err)
	}
	conn.Close()
}

func TestRelayPreservesHalfClose(t *testing.T) {
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen backend: %v", err)
	}
	defer backendLn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := backendLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		payload, err := io.ReadAll(conn)
		if err != nil {
			return
		}
		_, _ = conn.Write(append([]byte("ack:"), payload...))
	}()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-half-close", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendLn.Addr().String(), []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("request")); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	closeWriter, ok := conn.(interface{ CloseWrite() error })
	if !ok {
		t.Fatalf("relay connection does not support CloseWrite")
	}
	if err := closeWriter.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite failed: %v", err)
	}

	reply, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("failed to read response after half-close: %v", err)
	}
	if !bytes.Equal(reply, []byte("ack:request")) {
		t.Fatalf("unexpected half-close response: got %q", reply)
	}

	<-done
}

func TestDialTimesOutOnStalledHandshake(t *testing.T) {
	withRelayTimeouts(50*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond, func() {
		stallLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen for stalled peer: %v", err)
		}
		defer stallLn.Close()

		go func() {
			conn, err := stallLn.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			time.Sleep(250 * time.Millisecond)
		}()

		provider := newFakeTLSMaterialProvider()
		_, hop := newRelayEndpoint(t, provider, 1, "relay-handshake-timeout", "pin_only", true, false)
		hop.Address = stallLn.Addr().String()

		_, err = Dial(context.Background(), "tcp", "127.0.0.1:9", []Hop{hop}, provider)
		if err == nil {
			t.Fatal("expected stalled handshake to time out")
		}
	})
}

func TestDialTimesOutOnStalledRelayResponse(t *testing.T) {
	withRelayTimeouts(100*time.Millisecond, 100*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond, func() {
		provider := newFakeTLSMaterialProvider()
		_, hop := newRelayEndpoint(t, provider, 1, "relay-frame-timeout", "pin_only", true, false)

		stallLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen for stalled relay: %v", err)
		}
		defer stallLn.Close()

		go func() {
			rawConn, err := stallLn.Accept()
			if err != nil {
				return
			}
			defer rawConn.Close()

			tlsConfig, cfgErr := serverTLSConfig(context.Background(), provider, hop.Listener)
			if cfgErr != nil {
				return
			}
			tlsConn := tls.Server(rawConn, tlsConfig)
			if cfgErr = tlsConn.Handshake(); cfgErr != nil {
				return
			}
			_, _ = readRelayRequest(tlsConn)
			time.Sleep(250 * time.Millisecond)
		}()

		hop.Address = stallLn.Addr().String()
		if _, err := Dial(context.Background(), "tcp", "127.0.0.1:9", []Hop{hop}, provider); err == nil {
			t.Fatal("expected stalled relay response to time out")
		}
	})
}

func TestIdleRelayConnectionTimesOut(t *testing.T) {
	withRelayTimeouts(100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond, 50*time.Millisecond, func() {
		backendLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen backend: %v", err)
		}
		defer backendLn.Close()

		go func() {
			conn, err := backendLn.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			time.Sleep(250 * time.Millisecond)
		}()

		provider := newFakeTLSMaterialProvider()
		listener, hop := newRelayEndpoint(t, provider, 1, "relay-idle-timeout", "pin_only", true, false)

		server, err := Start(context.Background(), []Listener{listener}, provider)
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
		defer server.Close()

		conn, err := Dial(context.Background(), "tcp", backendLn.Addr().String(), []Hop{hop}, provider)
		if err != nil {
			t.Fatalf("Dial returned error: %v", err)
		}
		defer conn.Close()

		readDone := make(chan error, 1)
		go func() {
			var buf [1]byte
			_, readErr := conn.Read(buf[:])
			readDone <- readErr
		}()

		select {
		case err := <-readDone:
			if err == nil {
				t.Fatal("expected idle relay connection to close or time out")
			}
		case <-time.After(time.Second):
			t.Fatal("idle relay connection did not time out")
		}
	})
}

func TestServerCloseStopsActiveRelayConnections(t *testing.T) {
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen backend: %v", err)
	}
	defer backendLn.Close()

	backendAccepted := make(chan struct{})
	backendRelease := make(chan struct{})
	go func() {
		conn, err := backendLn.Accept()
		if err != nil {
			close(backendAccepted)
			return
		}
		defer conn.Close()
		close(backendAccepted)
		<-backendRelease
	}()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-close", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	conn, err := Dial(context.Background(), "tcp", backendLn.Addr().String(), []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	<-backendAccepted

	done := make(chan struct{})
	go func() {
		server.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server.Close hung with active relay connections")
	}

	close(backendRelease)
}

type fakeTLSMaterialProvider struct {
	mu          sync.RWMutex
	serverCerts map[int]tls.Certificate
	caCerts     map[int][]*x509.Certificate
}

func newFakeTLSMaterialProvider() *fakeTLSMaterialProvider {
	return &fakeTLSMaterialProvider{
		serverCerts: make(map[int]tls.Certificate),
		caCerts:     make(map[int][]*x509.Certificate),
	}
}

func (p *fakeTLSMaterialProvider) ServerCertificate(_ context.Context, certificateID int) (*tls.Certificate, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	cert, ok := p.serverCerts[certificateID]
	if !ok {
		return nil, fmt.Errorf("missing server certificate %d", certificateID)
	}
	copyCert := cert
	return &copyCert, nil
}

func (p *fakeTLSMaterialProvider) TrustedCAPool(_ context.Context, certificateIDs []int) (*x509.CertPool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(certificateIDs) == 0 {
		return nil, nil
	}

	pool := x509.NewCertPool()
	for _, id := range certificateIDs {
		existing, ok := p.caCerts[id]
		if !ok {
			continue
		}
		for _, cert := range existing {
			pool.AddCert(cert)
		}
	}
	if len(pool.Subjects()) == 0 {
		return nil, nil
	}
	return pool, nil
}

func newRelayEndpoint(t *testing.T, provider *fakeTLSMaterialProvider, id int, name, tlsMode string, includePin, includeCA bool) (Listener, Hop) {
	return newRelayEndpointWithCert(t, provider, relayEndpointOptions{
		id:         id,
		name:       name,
		tlsMode:    tlsMode,
		includePin: includePin,
		includeCA:  includeCA,
		serverName: "127.0.0.1",
		certIPs:    []net.IP{net.ParseIP("127.0.0.1")},
		certDNSNames: []string{
			"localhost",
		},
	})
}

type relayEndpointOptions struct {
	id           int
	name         string
	tlsMode      string
	includePin   bool
	includeCA    bool
	serverName   string
	certIPs      []net.IP
	certDNSNames []string
}

func newRelayEndpointWithCert(t *testing.T, provider *fakeTLSMaterialProvider, options relayEndpointOptions) (Listener, Hop) {
	t.Helper()

	certificateID := options.id * 10
	caID := options.id * 100
	cert, parsed := newServerCertificate(t, certificateOptions{
		commonName: options.serverName,
		ipAddrs:    options.certIPs,
		dnsNames:   options.certDNSNames,
	})

	provider.mu.Lock()
	provider.serverCerts[certificateID] = cert
	if options.includeCA {
		provider.caCerts[caID] = []*x509.Certificate{parsed}
	}
	provider.mu.Unlock()

	listener := Listener{
		ID:            options.id,
		AgentID:       fmt.Sprintf("agent-%d", options.id),
		Name:          options.name,
		ListenHost:    "127.0.0.1",
		ListenPort:    pickFreeTCPPort(t),
		Enabled:       true,
		CertificateID: &certificateID,
		TLSMode:       options.tlsMode,
		Tags:          []string{"relay"},
		Revision:      int64(options.id),
	}
	if options.includePin {
		listener.PinSet = []model.RelayPin{{
			Type:  "spki_sha256",
			Value: spkiPin(t, parsed),
		}}
	}
	if options.includeCA {
		listener.TrustedCACertificateIDs = []int{caID}
		listener.AllowSelfSigned = true
	}

	return listener, Hop{
		Address:    net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort)),
		Listener:   listener,
		ServerName: options.serverName,
	}
}

type certificateOptions struct {
	commonName string
	ipAddrs    []net.IP
	dnsNames   []string
}

func newServerCertificate(t *testing.T, options certificateOptions) (tls.Certificate, *x509.Certificate) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName: options.commonName,
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           append([]net.IP(nil), options.ipAddrs...),
		DNSNames:              append([]string(nil), options.dnsNames...),
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	parsed, err := x509.ParseCertificate(derBytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  privateKey,
		Leaf:        parsed,
	}, parsed
}

func spkiPin(t *testing.T, cert *x509.Certificate) string {
	t.Helper()

	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func startTCPEchoServer(t *testing.T) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for echo server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func assertRoundTrip(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()

	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("failed to write payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("failed to read payload: %v", err)
	}

	if !bytes.Equal(reply, payload) {
		t.Fatalf("payload mismatch: got %q want %q", reply, payload)
	}
}

func pickFreeTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve tcp port: %v", err)
	}
	defer ln.Close()

	return ln.Addr().(*net.TCPAddr).Port
}

func withRelayTimeouts(dial, handshake, frame, idle time.Duration, fn func()) {
	prevDial := relayDialTimeout
	prevHandshake := relayHandshakeTimeout
	prevFrame := relayFrameTimeout
	prevIdle := relayIdleTimeout

	relayDialTimeout = dial
	relayHandshakeTimeout = handshake
	relayFrameTimeout = frame
	relayIdleTimeout = idle
	defer func() {
		relayDialTimeout = prevDial
		relayHandshakeTimeout = prevHandshake
		relayFrameTimeout = prevFrame
		relayIdleTimeout = prevIdle
	}()

	fn()
}
