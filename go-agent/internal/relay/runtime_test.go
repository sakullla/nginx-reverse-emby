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
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
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

func TestValidateListenerAcceptsIPv6PublicHost(t *testing.T) {
	t.Parallel()

	err := ValidateListener(Listener{
		ID:         1,
		AgentID:    "agent-a",
		Name:       "relay-v6",
		ListenHost: "127.0.0.1",
		ListenPort: 18443,
		PublicHost: "2001:db8::1",
		PublicPort: 28443,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "spki_sha256",
			Value: "cGlubmVk",
		}},
	})
	if err != nil {
		t.Fatalf("ValidateListener returned error: %v", err)
	}
}

func TestValidateListenerRejectsBracketedIPv6PublicHost(t *testing.T) {
	t.Parallel()

	err := ValidateListener(Listener{
		ID:         1,
		AgentID:    "agent-a",
		Name:       "relay-v6",
		ListenHost: "127.0.0.1",
		ListenPort: 18443,
		PublicHost: "[2001:db8::1]",
		PublicPort: 28443,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "spki_sha256",
			Value: "cGlubmVk",
		}},
	})
	if err == nil {
		t.Fatal("expected bracketed public_host to be rejected")
	}
	if got := err.Error(); got != "public_host must be a valid IP address or hostname" {
		t.Fatalf("unexpected error: %q", got)
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

func TestDialFailsClosedWhenHopRejectsObfsMode(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-unsupported-mode", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	hop.Listener.ObfsMode = "future_mode"
	_, err = Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
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

func TestOneHopRelayUDPDataFlow(t *testing.T) {
	backendAddr, stopBackend := startUDPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-one-udp", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "udp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertUDPRelayRoundTrip(t, conn, []byte("one-hop-udp"))
}

func TestTLSTCPSessionPoolReusesOuterConnection(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-mux", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	resetTLSTCPSessionPoolForTest()

	connA, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial(A) error = %v", err)
	}
	defer connA.Close()

	connB, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial(B) error = %v", err)
	}
	defer connB.Close()

	stats := currentTLSTCPSessionPoolStats()
	if stats.ActiveSessions != 1 {
		t.Fatalf("ActiveSessions = %d, want 1", stats.ActiveSessions)
	}
	if stats.LogicalStreams < 2 {
		t.Fatalf("LogicalStreams = %d, want at least 2", stats.LogicalStreams)
	}
}

func TestTLSTCPSessionPoolReusesOuterConnectionForUDPStreams(t *testing.T) {
	backendAddr, stopBackend := startUDPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-mux-udp", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	resetTLSTCPSessionPoolForTest()

	connA, err := Dial(context.Background(), "udp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial(A) error = %v", err)
	}
	defer connA.Close()

	connB, err := Dial(context.Background(), "udp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial(B) error = %v", err)
	}
	defer connB.Close()

	assertUDPRelayRoundTrip(t, connA, []byte("udp-mux-a"))
	assertUDPRelayRoundTrip(t, connB, []byte("udp-mux-b"))

	stats := currentTLSTCPSessionPoolStats()
	if stats.ActiveSessions != 1 {
		t.Fatalf("ActiveSessions = %d, want 1", stats.ActiveSessions)
	}
	if stats.LogicalStreams < 2 {
		t.Fatalf("LogicalStreams = %d, want at least 2", stats.LogicalStreams)
	}
}

func TestOneHopRelayDataFlowWithEarlyWindowMask(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-one-obfs", "pin_only", true, false)
	listener.ObfsMode = RelayObfsModeEarlyWindowV2
	hop.Listener = listener

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
	if !listenerUsesEarlyWindowMask(hop.Listener) {
		t.Fatal("expected listener to use early window masking")
	}

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

func TestMultiHopRelayUDPDataFlow(t *testing.T) {
	backendAddr, stopBackend := startUDPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listenerA, hopA := newRelayEndpoint(t, provider, 1, "relay-a-udp", "pin_only", true, false)
	listenerB, hopB := newRelayEndpoint(t, provider, 2, "relay-b-udp", "pin_only", true, false)

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

	conn, err := Dial(context.Background(), "udp", backendAddr, []Hop{hopA, hopB}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertUDPRelayRoundTrip(t, conn, []byte("multi-hop-udp"))
}

func TestMultiHopRelayDataFlowWithEarlyWindowMask(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listenerA, hopA := newRelayEndpoint(t, provider, 1, "relay-a-obfs", "pin_only", true, false)
	listenerB, hopB := newRelayEndpoint(t, provider, 2, "relay-b-obfs", "pin_only", true, false)
	listenerA.ObfsMode = RelayObfsModeEarlyWindowV2
	listenerB.ObfsMode = RelayObfsModeEarlyWindowV2
	hopA.Listener = listenerA
	hopB.Listener = listenerB

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

func TestFinalTargetFailureDoesNotDemoteRelayQUICTransport(t *testing.T) {
	resetTLSTCPSessionPoolForTest()

	now := time.Unix(1700000000, 0)
	score := upstream.NewScoreStore(func() time.Time { return now })
	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	provider := newFakeTLSMaterialProvider()
	quicListener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-target-fail", "pin_only", true, false)
	sharedPort := pickFreeDualStackPort(t)
	quicListener.ListenPort = sharedPort
	quicListener.TransportMode = ListenerTransportModeQUIC
	quicListener.AllowTransportFallback = true
	hop.Address = net.JoinHostPort(quicListener.ListenHost, fmt.Sprintf("%d", sharedPort))
	hop.Listener = quicListener

	tlsListener := quicListener
	tlsListener.ID = 2
	tlsListener.Name = "relay-tls-target-fail"
	tlsListener.TransportMode = ListenerTransportModeTLSTCP
	tlsListener.AllowTransportFallback = false

	server, err := Start(context.Background(), []Listener{tlsListener, quicListener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	markRelayVerifiedFallback(hop)

	target := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", pickFreeTCPPort(t)))
	for i := 0; i < 2; i++ {
		conn, result, err := DialWithResult(context.Background(), "tcp", target, []Hop{hop}, provider)
		if err == nil {
			conn.Close()
			t.Fatalf("DialWithResult(%d) error = nil, want final target failure", i)
		}
		if result.TransportMode != "" {
			t.Fatalf("DialWithResult(%d) TransportMode = %q, want empty result on failed open", i, result.TransportMode)
		}
	}

	state := score.State(relayQUICPathKey(hop))
	if state.ProbeOnly {
		t.Fatal("ProbeOnly = true after final target failures, want false for healthy QUIC transport")
	}
	if got := selectRelayRuntimeTransport(hop); got != ListenerTransportModeQUIC {
		t.Fatalf("selectRelayRuntimeTransport() = %q, want %q after backend-only failures", got, ListenerTransportModeQUIC)
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
	resetTLSTCPSessionPoolForTest()

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
	resetTLSTCPSessionPoolForTest()

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
	resetTLSTCPSessionPoolForTest()

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

func TestServerOpenUpstreamDefersHostnameResolutionToLastHop(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	_, port, err := net.SplitHostPort(backendAddr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &Server{
		ctx: ctx,
		finalHopSelector: newFinalHopSelector(finalHopSelectorConfig{
			Resolver: relayResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
				if host != "deferred.example" {
					t.Fatalf("unexpected host %q", host)
				}
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
			}),
		}),
	}

	conn, err := server.openUpstream("tcp", net.JoinHostPort("deferred.example", port), nil, DialOptions{})
	if err != nil {
		t.Fatalf("openUpstream() error = %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, []byte("last-hop-dns"))
}

func TestServerOpenUDPPeerDefersHostnameResolutionToLastHop(t *testing.T) {
	backendAddr, stopBackend := startUDPEchoServer(t)
	defer stopBackend()

	_, port, err := net.SplitHostPort(backendAddr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &Server{
		ctx: ctx,
		finalHopSelector: newFinalHopSelector(finalHopSelectorConfig{
			Resolver: relayResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
				if host != "deferred.example" {
					t.Fatalf("unexpected host %q", host)
				}
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
			}),
		}),
	}

	peer, err := server.openUDPPeer(net.JoinHostPort("deferred.example", port), nil)
	if err != nil {
		t.Fatalf("openUDPPeer() error = %v", err)
	}
	defer peer.Close()

	if err := peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := peer.WritePacket([]byte("udp-last-hop-dns")); err != nil {
		t.Fatalf("WritePacket() error = %v", err)
	}
	payload, err := peer.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	if string(payload) != "udp-last-hop-dns" {
		t.Fatalf("payload = %q", payload)
	}
}

func TestDialWithResultReturnsSelectedAddressFromFinalHop(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-selected-address", "pin_only", true, false)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("DialWithResult() error = %v", err)
	}
	defer conn.Close()

	if result.SelectedAddress != backendAddr {
		t.Fatalf("SelectedAddress = %q, want %q", result.SelectedAddress, backendAddr)
	}
}

func TestDialWithResultUsesFallbackAfterQUICProbeDialFails(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()
	resetTLSTCPSessionPoolForTest()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-fallback", "pin_only", true, false)
	listener.ListenPort = pickFreeDualStackPort(t)
	hop.Address = net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort))
	hop.Listener = listener
	listener.TransportMode = "quic"
	listener.AllowTransportFallback = true
	hop.Listener.TransportMode = "quic"
	hop.Listener.AllowTransportFallback = true

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	now := time.Unix(1700000000, 0)
	score := upstream.NewScoreStore(func() time.Time { return now })
	key := relayQUICPathKey(hop)
	score.ObserveFailure(key, upstream.FailureTimeout)
	score.ObserveFailure(key, upstream.FailureTimeout)
	score.ArmProbe(key, relayQUICProbeInterval)
	now = now.Add(relayQUICProbeInterval)

	restorePlanner := setRelayPlannerForTest(upstream.NewPlanner())
	defer restorePlanner()
	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()

	prevQUICDial := quicDialAddr
	quicDialCalls := 0
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		quicDialCalls++
		if addr != hop.Address {
			t.Fatalf("quicDialAddr() address = %q, want %q", addr, hop.Address)
		}
		return nil, errors.New("quic probe failed")
	}
	defer func() {
		quicDialAddr = prevQUICDial
	}()

	conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err == nil {
		defer conn.Close()
		t.Fatal("DialWithResult() error = nil, want combined QUIC+fallback failure against QUIC-only listener")
	}
	if result.TransportMode != "" {
		t.Fatalf("TransportMode = %q, want empty result on total failure", result.TransportMode)
	}
	if quicDialCalls != 1 {
		t.Fatalf("quicDialCalls = %d, want 1 failed QUIC probe before fallback", quicDialCalls)
	}
}

func TestDialWithResultUsesRuntimeFallbackAfterRepeatedQUICFailures(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()
	resetTLSTCPSessionPoolForTest()
	restoreScore := setRelayRuntimeScoreForTest(upstream.NewScoreStore(time.Now))
	defer restoreScore()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	provider := newFakeTLSMaterialProvider()
	quicListener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-runtime-fallback", "pin_only", true, false)
	sharedPort := pickFreeDualStackPort(t)
	quicListener.ListenPort = sharedPort
	quicListener.TransportMode = "quic"
	quicListener.AllowTransportFallback = true
	hop.Address = net.JoinHostPort(quicListener.ListenHost, fmt.Sprintf("%d", sharedPort))
	hop.Listener = quicListener

	tlsListener := quicListener
	tlsListener.ID = 2
	tlsListener.Name = "relay-tls-runtime-fallback"
	tlsListener.TransportMode = ListenerTransportModeTLSTCP
	tlsListener.AllowTransportFallback = false

	server, err := Start(context.Background(), []Listener{tlsListener, quicListener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	prevQUICDial := quicDialAddr
	attempts := 0
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		attempts++
		return nil, errors.New("quic unavailable")
	}
	defer func() {
		quicDialAddr = prevQUICDial
	}()

	for i := 0; i < 3; i++ {
		conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
		if err != nil {
			t.Fatalf("DialWithResult(%d) error = %v", i, err)
		}
		if result.TransportMode != ListenerTransportModeTLSTCP {
			t.Fatalf("DialWithResult(%d) TransportMode = %q, want %q", i, result.TransportMode, ListenerTransportModeTLSTCP)
		}
		conn.Close()
	}
	if attempts != 2 {
		t.Fatalf("quic attempts = %d, want 2 initial QUIC failures before probe interval backoff", attempts)
	}
}

func TestResolveCandidatesDoesNotConsumeQUICProbeWindow(t *testing.T) {
	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-resolve-probe-window", "pin_only", true, false)
	listener.ListenPort = pickFreeDualStackPort(t)
	listener.TransportMode = ListenerTransportModeQUIC
	listener.AllowTransportFallback = true
	hop.Listener = listener
	hop.Address = net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort))

	now := time.Unix(1700000000, 0)
	score := upstream.NewScoreStore(func() time.Time { return now })
	key := relayQUICPathKey(hop)
	score.ObserveFailure(key, upstream.FailureTimeout)
	score.ObserveFailure(key, upstream.FailureTimeout)
	score.ArmProbe(key, relayQUICProbeInterval)
	now = now.Add(relayQUICProbeInterval)

	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	prevQUICDial := quicDialAddr
	quicDialCalls := 0
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		quicDialCalls++
		return nil, errors.New("quic still unavailable")
	}
	defer func() {
		quicDialAddr = prevQUICDial
	}()

	if _, err := ResolveCandidates(context.Background(), "deferred.example:8096", []Hop{hop}, provider); err == nil {
		t.Fatal("ResolveCandidates() error = nil, want fallback failure while no tls_tcp listener exists")
	}
	if quicDialCalls != 1 {
		t.Fatalf("ResolveCandidates() quicDialCalls = %d, want 1 real QUIC attempt", quicDialCalls)
	}

	state := score.State(key)
	if state.NextProbeAt.After(now) {
		t.Fatalf("ResolveCandidates() consumed probe window; NextProbeAt = %v now = %v", state.NextProbeAt, now)
	}
}

func TestResolveCandidatesDoesNotMutateRelayVerifiedFallbackState(t *testing.T) {
	resetTLSTCPSessionPoolForTest()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	provider := newFakeTLSMaterialProvider()
	quicListener, hop := newRelayEndpoint(t, provider, 1, "relay-resolve-fallback-state", "pin_only", true, false)
	sharedPort := pickFreeDualStackPort(t)
	quicListener.ListenPort = sharedPort
	quicListener.TransportMode = ListenerTransportModeQUIC
	quicListener.AllowTransportFallback = true
	hop.Address = net.JoinHostPort(quicListener.ListenHost, fmt.Sprintf("%d", sharedPort))
	hop.Listener = quicListener

	tlsListener := quicListener
	tlsListener.ID = 2
	tlsListener.Name = "relay-tls-resolve-fallback-state"
	tlsListener.TransportMode = ListenerTransportModeTLSTCP
	tlsListener.AllowTransportFallback = false

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &Server{
		ctx:      ctx,
		cancel:   cancel,
		provider: provider,
		finalHopSelector: newFinalHopSelector(finalHopSelectorConfig{
			Resolver: relayResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
				if host != "deferred.example" {
					t.Fatalf("unexpected host %q", host)
				}
				return []net.IPAddr{
					{IP: net.ParseIP("127.0.0.10")},
					{IP: net.ParseIP("127.0.0.11")},
				}, nil
			}),
		}),
		conns:     make(map[net.Conn]struct{}),
		quicConns: make(map[*quic.Conn]struct{}),
	}
	normalizedTLS, err := normalizeListener(tlsListener)
	if err != nil {
		t.Fatalf("normalizeListener(tls) error = %v", err)
	}
	if err := server.startListener(normalizedTLS); err != nil {
		t.Fatalf("startListener(tls) error = %v", err)
	}
	normalizedQUIC, err := normalizeListener(quicListener)
	if err != nil {
		t.Fatalf("normalizeListener(quic) error = %v", err)
	}
	if err := server.startListener(normalizedQUIC); err != nil {
		t.Fatalf("startListener(quic) error = %v", err)
	}
	defer server.Close()

	prevQUICDial := quicDialAddr
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		return nil, errors.New("quic unavailable")
	}
	defer func() {
		quicDialAddr = prevQUICDial
	}()

	addresses, err := ResolveCandidates(context.Background(), "deferred.example:8096", []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("ResolveCandidates() error = %v", err)
	}
	if len(addresses) == 0 {
		t.Fatal("ResolveCandidates() returned no addresses")
	}
	if relayVerifiedFallbackAvailable(hop) {
		t.Fatal("ResolveCandidates() marked relay fallback verified; diagnostics must not mutate live fallback state")
	}
}

func TestResolveCandidatesDoesNotClearRelayVerifiedFallbackState(t *testing.T) {
	resetTLSTCPSessionPoolForTest()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-resolve-fallback-clear", "pin_only", true, false)
	listener.ListenPort = pickFreeDualStackPort(t)
	listener.TransportMode = ListenerTransportModeQUIC
	listener.AllowTransportFallback = true
	hop.Listener = listener
	hop.Address = net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort))

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	markRelayVerifiedFallback(hop)
	if !relayVerifiedFallbackAvailable(hop) {
		t.Fatal("precondition failed: expected verified fallback state")
	}

	prevQUICDial := quicDialAddr
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		return nil, errors.New("quic unavailable")
	}
	defer func() {
		quicDialAddr = prevQUICDial
	}()

	if _, err := ResolveCandidates(context.Background(), "deferred.example:8096", []Hop{hop}, provider); err == nil {
		t.Fatal("ResolveCandidates() error = nil, want combined fallback failure")
	}
	if !relayVerifiedFallbackAvailable(hop) {
		t.Fatal("ResolveCandidates() cleared verified fallback state; diagnostics must not mutate live fallback state")
	}
}

func TestRelayTransportCandidatesDoesNotAssumeTLSTCPFallbackExistsForQUICHop(t *testing.T) {
	now := time.Unix(1700000000, 0)
	score := upstream.NewScoreStore(func() time.Time { return now })
	hop := Hop{
		Address: "relay.example:443",
		Listener: Listener{
			ID:                     11,
			Revision:               7,
			TransportMode:          ListenerTransportModeQUIC,
			AllowTransportFallback: true,
		},
		ServerName: "relay-a.example",
	}
	key := relayQUICPathKey(hop)
	score.ObserveFailure(key, upstream.FailureTimeout)
	score.ObserveFailure(key, upstream.FailureTimeout)
	score.ArmProbe(key, relayQUICProbeInterval)

	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	candidates := relayTransportCandidates(hop)
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1 without verified tls_tcp fallback identity", len(candidates))
	}
	if candidates[0].Key.Family != upstream.PathFamilyRelayQUIC {
		t.Fatalf("candidate family = %q, want %q", candidates[0].Key.Family, upstream.PathFamilyRelayQUIC)
	}
}

func TestRelayQUICHealthIsScopedPerHopIdentity(t *testing.T) {
	now := time.Unix(1700000000, 0)
	score := upstream.NewScoreStore(func() time.Time { return now })
	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	hopA := Hop{
		Address: "relay.example:443",
		Listener: Listener{
			ID:            101,
			Revision:      1,
			TransportMode: ListenerTransportModeQUIC,
		},
		ServerName: "relay-a.example",
	}
	hopB := Hop{
		Address: "relay.example:443",
		Listener: Listener{
			ID:            202,
			Revision:      2,
			TransportMode: ListenerTransportModeQUIC,
		},
		ServerName: "relay-b.example",
	}

	observeRelayQUICFailureForHop(hopA)
	observeRelayQUICFailureForHop(hopA)

	candidatesA := relayTransportCandidates(hopA)
	if !candidatesA[0].ProbeOnly {
		t.Fatal("hopA ProbeOnly = false after repeated failures, want true")
	}

	candidatesB := relayTransportCandidates(hopB)
	if candidatesB[0].ProbeOnly {
		t.Fatal("hopB ProbeOnly = true after unrelated hopA failures, want false")
	}
}

func TestSelectRelayRuntimeTransportHonorsQUICProbeInterval(t *testing.T) {
	now := time.Unix(1700000000, 0)
	score := upstream.NewScoreStore(func() time.Time { return now })
	restorePlanner := setRelayPlannerForTest(upstream.NewPlanner())
	defer restorePlanner()
	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	hop := Hop{
		Address: "relay.example:443",
		Listener: Listener{
			TransportMode:          ListenerTransportModeQUIC,
			AllowTransportFallback: true,
			ID:                     77,
			Revision:               5,
		},
		ServerName: "relay.example",
	}
	key := relayQUICPathKey(hop)
	score.ObserveFailure(key, upstream.FailureTimeout)
	score.ObserveFailure(key, upstream.FailureTimeout)
	score.ArmProbe(key, relayQUICProbeInterval)

	if got := selectRelayRuntimeTransport(hop); got != ListenerTransportModeQUIC {
		t.Fatalf("selectRelayRuntimeTransport(before fallback verified) = %q, want %q", got, ListenerTransportModeQUIC)
	}

	markRelayVerifiedFallback(hop)
	if got := selectRelayRuntimeTransport(hop); got != ListenerTransportModeTLSTCP {
		t.Fatalf("selectRelayRuntimeTransport(before deadline with verified fallback) = %q, want %q", got, ListenerTransportModeTLSTCP)
	}

	now = now.Add(relayQUICProbeInterval)
	if got := selectRelayRuntimeTransport(hop); got != ListenerTransportModeQUIC {
		t.Fatalf("selectRelayRuntimeTransport(after deadline) = %q, want %q", got, ListenerTransportModeQUIC)
	}
}

func TestDialWithResultRecoversQUICAfterProbeSuccesses(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()
	resetTLSTCPSessionPoolForTest()

	prevSessionPool := relaySessionPool
	relaySessionPool = newSessionPool()
	defer func() {
		relaySessionPool = prevSessionPool
	}()

	now := time.Unix(1700000000, 0)
	score := upstream.NewScoreStore(func() time.Time { return now })
	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()
	restoreFallbacks := setRelayVerifiedFallbacksForTest(newRelayVerifiedFallbackStore())
	defer restoreFallbacks()

	provider := newFakeTLSMaterialProvider()
	quicListener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-recovery", "pin_only", true, false)
	sharedPort := pickFreeDualStackPort(t)
	quicListener.ListenPort = sharedPort
	quicListener.TransportMode = "quic"
	quicListener.AllowTransportFallback = true
	hop.Address = net.JoinHostPort(quicListener.ListenHost, fmt.Sprintf("%d", sharedPort))
	hop.Listener = quicListener

	tlsListener := quicListener
	tlsListener.ID = 2
	tlsListener.Name = "relay-tls-recovery"
	tlsListener.TransportMode = ListenerTransportModeTLSTCP
	tlsListener.AllowTransportFallback = false

	server, err := Start(context.Background(), []Listener{tlsListener, quicListener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	prevQUICDial := quicDialAddr
	quicDialFailures := 0
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		if quicDialFailures < 2 {
			quicDialFailures++
			return nil, errors.New("quic unavailable")
		}
		return prevQUICDial(ctx, addr, tlsConf, conf)
	}
	defer func() {
		quicDialAddr = prevQUICDial
	}()

	for i := 0; i < 3; i++ {
		conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
		if err != nil {
			t.Fatalf("DialWithResult(fallback %d) error = %v", i, err)
		}
		if result.TransportMode != ListenerTransportModeTLSTCP {
			t.Fatalf("DialWithResult(fallback %d) TransportMode = %q, want %q before probe interval", i, result.TransportMode, ListenerTransportModeTLSTCP)
		}
		conn.Close()
	}
	if quicDialFailures != 2 {
		t.Fatalf("quicDialFailures = %d, want 2 before QUIC recovery", quicDialFailures)
	}

	state := score.State(relayQUICPathKey(hop))
	if !state.ProbeOnly {
		t.Fatal("ProbeOnly = false after repeated QUIC failures, want true")
	}

	for probe := 0; probe < 3; probe++ {
		now = now.Add(relayQUICProbeInterval)
		conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
		if err != nil {
			t.Fatalf("DialWithResult(probe %d) error = %v", probe, err)
		}
		if result.TransportMode != ListenerTransportModeQUIC {
			t.Fatalf("DialWithResult(probe %d) TransportMode = %q, want %q", probe, result.TransportMode, ListenerTransportModeQUIC)
		}
		assertRoundTrip(t, conn, []byte(fmt.Sprintf("probe-%d", probe)))
		conn.Close()
	}

	state = score.State(relayQUICPathKey(hop))
	if state.ProbeOnly {
		t.Fatal("ProbeOnly = true after three successful QUIC probes, want false")
	}

	conn, result, err := DialWithResult(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("DialWithResult(recovered) error = %v", err)
	}
	defer conn.Close()

	if result.TransportMode != ListenerTransportModeQUIC {
		t.Fatalf("DialWithResult(recovered) TransportMode = %q, want %q", result.TransportMode, ListenerTransportModeQUIC)
	}
}

func TestResolveCandidatesUsesLastHopResolution(t *testing.T) {
	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-resolve", "pin_only", true, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := &Server{
		ctx:      ctx,
		cancel:   cancel,
		provider: provider,
		finalHopSelector: newFinalHopSelector(finalHopSelectorConfig{
			Resolver: relayResolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
				if host != "deferred.example" {
					t.Fatalf("unexpected host %q", host)
				}
				return []net.IPAddr{
					{IP: net.ParseIP("127.0.0.10")},
					{IP: net.ParseIP("127.0.0.11")},
				}, nil
			}),
		}),
		conns:     make(map[net.Conn]struct{}),
		quicConns: make(map[*quic.Conn]struct{}),
	}
	normalizedListener, err := normalizeListener(listener)
	if err != nil {
		t.Fatalf("normalizeListener() error = %v", err)
	}
	if err := server.startListener(normalizedListener); err != nil {
		t.Fatalf("startListener() error = %v", err)
	}
	defer server.Close()

	addresses, err := ResolveCandidates(context.Background(), "deferred.example:8096", []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("ResolveCandidates() error = %v", err)
	}
	if len(addresses) != 2 {
		t.Fatalf("addresses = %+v", addresses)
	}
	if addresses[0] != "127.0.0.10:8096" {
		t.Fatalf("first address = %q", addresses[0])
	}
	if addresses[1] != "127.0.0.11:8096" {
		t.Fatalf("second address = %q", addresses[1])
	}
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

func startUDPEchoServer(t *testing.T) (string, func()) {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to resolve udp addr: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("failed to listen udp echo server: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for {
			n, peer, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if _, err := conn.WriteToUDP(buf[:n], peer); err != nil {
				return
			}
		}
	}()

	return conn.LocalAddr().String(), func() {
		_ = conn.Close()
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

func assertUDPRelayRoundTrip(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()

	if err := WriteUOTPacket(conn, payload); err != nil {
		t.Fatalf("failed to write udp payload: %v", err)
	}

	reply, err := ReadUOTPacket(conn)
	if err != nil {
		t.Fatalf("failed to read udp payload: %v", err)
	}
	if !bytes.Equal(reply, payload) {
		t.Fatalf("udp payload mismatch: got %q want %q", reply, payload)
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

func pickFreeUDPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to reserve udp port: %v", err)
	}
	defer ln.Close()

	return ln.LocalAddr().(*net.UDPAddr).Port
}

func pickFreeDualStackPort(t *testing.T) int {
	t.Helper()

	tryPair := func(tcpFirst bool) (int, bool) {
		if tcpFirst {
			tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return 0, false
			}
			port := tcpLn.Addr().(*net.TCPAddr).Port
			udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
			if err != nil {
				_ = tcpLn.Close()
				return 0, false
			}
			_ = udpLn.Close()
			_ = tcpLn.Close()
			return port, true
		}

		udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		if err != nil {
			return 0, false
		}
		port := udpLn.LocalAddr().(*net.UDPAddr).Port
		tcpLn, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			_ = udpLn.Close()
			return 0, false
		}
		_ = tcpLn.Close()
		_ = udpLn.Close()
		return port, true
	}

	for attempt := 0; attempt < 64; attempt++ {
		if port, ok := tryPair(attempt%2 == 0); ok {
			return port
		}
	}

	for attempt := 0; attempt < 64; attempt++ {
		tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to reserve dual-stack tcp port: %v", err)
		}
		port := tcpLn.Addr().(*net.TCPAddr).Port
		udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
		if err == nil {
			_ = udpLn.Close()
			_ = tcpLn.Close()
			return port
		}
		_ = tcpLn.Close()
	}

	t.Fatal("failed to reserve port usable for both tcp and udp")
	return 0
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
