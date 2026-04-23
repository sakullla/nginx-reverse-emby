package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

func TestDialQUICRoundTripTCP(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-quic", "pin_only", true, false)
	listener.ListenPort = pickFreeUDPPort(t)
	listener.TransportMode = "quic"
	listener.AllowTransportFallback = false
	hop.Address = net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort))
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

func TestDialQUICForwardsInitialPayload(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-initial", "pin_only", true, false)
	listener.ListenPort = pickFreeUDPPort(t)
	listener.TransportMode = "quic"
	listener.AllowTransportFallback = false
	hop.Address = net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort))
	hop.Listener = listener

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	initial := []byte("quic-initial-payload")
	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider, DialOptions{InitialPayload: initial})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	reply := make([]byte, len(initial))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read initial payload reply: %v", err)
	}
	if !bytes.Equal(reply, initial) {
		t.Fatalf("initial payload reply = %q, want %q", reply, initial)
	}
}

func TestPickFreeUDPPortReturnsBindablePort(t *testing.T) {
	port := pickFreeUDPPort(t)

	udpListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatalf("failed to listen on udp port %d: %v", port, err)
	}
	defer udpListener.Close()
}

func TestDialFallsBackToTLSTCP(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()
	resetTLSTCPSessionPoolForTest()

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

func TestQUICStreamConnCloseUnblocksLocalRead(t *testing.T) {
	backendLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen backend: %v", err)
	}
	defer backendLn.Close()

	backendAccepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := backendLn.Accept()
		if acceptErr != nil {
			return
		}
		backendAccepted <- conn
	}()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-close", "pin_only", true, false)
	listener.ListenPort = pickFreeUDPPort(t)
	listener.TransportMode = ListenerTransportModeQUIC
	listener.AllowTransportFallback = false
	hop.Address = net.JoinHostPort(listener.ListenHost, fmt.Sprintf("%d", listener.ListenPort))
	hop.Listener = listener

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendLn.Addr().String(), []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	select {
	case backendConn := <-backendAccepted:
		defer backendConn.Close()
	case <-time.After(time.Second):
		t.Fatal("backend did not accept relayed connection")
	}

	readErrCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, readErr := conn.Read(buf)
		readErrCh <- readErr
	}()

	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case readErr := <-readErrCh:
		if readErr == nil {
			t.Fatal("Read() error = nil, want local close to unblock read")
		}
	case <-time.After(time.Second):
		t.Fatal("Read() did not unblock after Close()")
	}
}

func TestDialQUICDoesNotScoreCallerCancellationAsTransportFailure(t *testing.T) {
	now := time.Unix(1700000000, 0)
	score := upstream.NewScoreStore(func() time.Time { return now })
	restoreScore := setRelayRuntimeScoreForTest(score)
	defer restoreScore()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-cancel", "pin_only", true, false)
	listener.TransportMode = ListenerTransportModeQUIC
	listener.AllowTransportFallback = false
	hop.Listener = listener

	prevDial := quicDialAddr
	quicDialAddr = func(ctx context.Context, addr string, tlsConf *tls.Config, conf *quic.Config) (*quic.Conn, error) {
		return nil, ctx.Err()
	}
	defer func() {
		quicDialAddr = prevDial
	}()

	cases := []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		err  error
	}{
		{
			name: "canceled",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			err: context.Canceled,
		},
		{
			name: "deadline exceeded",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
				return ctx, cancel
			},
			err: context.DeadlineExceeded,
		},
	}

	for _, tc := range cases {
		ctx, cancel := tc.ctx()
		_, _, err := DialWithResult(ctx, "tcp", "127.0.0.1:80", []Hop{hop}, provider)
		cancel()
		if !errors.Is(err, tc.err) {
			t.Fatalf("%s: error = %v, want %v", tc.name, err, tc.err)
		}
	}

	state := score.State(relayQUICPathKey(hop))
	if state.ConsecutiveHighSeverity != 0 {
		t.Fatalf("ConsecutiveHighSeverity = %d, want 0 after caller-side cancellations", state.ConsecutiveHighSeverity)
	}
	if state.ProbeOnly {
		t.Fatal("ProbeOnly = true after caller-side cancellations, want false")
	}
}
