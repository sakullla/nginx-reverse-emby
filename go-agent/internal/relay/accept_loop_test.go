package relay

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcceptLoopReturnsOnClosedListenerError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln := &permanentAcceptErrorListener{err: net.ErrClosed}
	server := &Server{ctx: ctx}
	done := make(chan struct{})
	server.wg.Add(1)
	go func() {
		server.acceptLoop(ln, Listener{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("acceptLoop did not return after listener closed")
	}

	if calls := ln.calls.Load(); calls != 1 {
		t.Fatalf("Accept() calls = %d, want 1", calls)
	}
}

func TestAcceptQUICLoopReturnsWhenListenerClosesOutsideServerClose(t *testing.T) {
	provider := newFakeTLSMaterialProvider()
	listener, _ := newRelayEndpoint(t, provider, 81, "relay-quic-accept-close", "pin_only", true, false)
	listener.ListenPort = pickFreeUDPPort(t)
	listener.TransportMode = ListenerTransportModeQUIC
	listener.AllowTransportFallback = false

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()
	if len(server.quicListeners) != 1 {
		t.Fatalf("quic listener count = %d, want 1", len(server.quicListeners))
	}

	done := make(chan struct{})
	go func() {
		server.wg.Wait()
		close(done)
	}()

	if err := server.quicListeners[0].Close(); err != nil {
		t.Fatalf("Close() quic listener error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		_ = server.Close()
		<-done
		t.Fatalf("acceptQUICLoop did not return after listener %s closed", net.JoinHostPort(listener.ListenHost, strconv.Itoa(listener.ListenPort)))
	}
}

type permanentAcceptErrorListener struct {
	calls atomic.Int64
	err   error
}

func (l *permanentAcceptErrorListener) Accept() (net.Conn, error) {
	l.calls.Add(1)
	return nil, l.err
}

func (l *permanentAcceptErrorListener) Close() error {
	return nil
}

func (l *permanentAcceptErrorListener) Addr() net.Addr {
	return permanentAcceptErrorAddr("accept-loop-test")
}

type permanentAcceptErrorAddr string

func (a permanentAcceptErrorAddr) Network() string { return string(a) }
func (a permanentAcceptErrorAddr) String() string  { return string(a) }
