package channel

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type muxIdleDeadlineConn struct {
	net.Conn
	timeout time.Duration
}

func (conn *muxIdleDeadlineConn) Read(payload []byte) (int, error) {
	if err := conn.Conn.SetReadDeadline(time.Now().Add(conn.timeout)); err != nil {
		return 0, err
	}
	return conn.Conn.Read(payload)
}

func (conn *muxIdleDeadlineConn) Write(payload []byte) (int, error) {
	if err := conn.Conn.SetWriteDeadline(time.Now().Add(conn.timeout)); err != nil {
		return 0, err
	}
	return conn.Conn.Write(payload)
}

func TestMuxKeepaliveSurvivesBidirectionalIdleDeadline(t *testing.T) {
	left, right := net.Pipe()
	idleTimeout := 100 * time.Millisecond
	keepaliveInterval := 10 * time.Millisecond
	keepaliveTimeout := 50 * time.Millisecond
	opener := newMuxOpener(&muxIdleDeadlineConn{Conn: left, timeout: idleTimeout}, keepaliveInterval, keepaliveTimeout)
	acceptor := newMuxAcceptor(&muxIdleDeadlineConn{Conn: right, timeout: idleTimeout}, keepaliveInterval, keepaliveTimeout)
	t.Cleanup(func() {
		_ = opener.Close()
		_ = acceptor.Close()
	})

	observation := time.NewTimer(5 * idleTimeout)
	defer observation.Stop()
	select {
	case <-opener.Done():
		t.Fatalf("opener closed while idle keepalives were active: %v", opener.Err())
	case <-acceptor.Done():
		t.Fatalf("acceptor closed while idle keepalives were active: %v", acceptor.Err())
	case <-observation.C:
	}
}

func TestMuxKeepaliveClosesUnresponsivePeer(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	go func() {
		_, _ = io.Copy(io.Discard, right)
	}()

	muxConn := newMuxAcceptor(left, 10*time.Millisecond, 30*time.Millisecond)
	t.Cleanup(func() { _ = muxConn.Close() })
	select {
	case <-muxConn.Done():
		if !errors.Is(muxConn.Err(), errMuxKeepalive) {
			t.Fatalf("mux error = %v, want keepalive timeout", muxConn.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("mux did not close after its peer ignored keepalive pings")
	}
}

func TestChannelKeepaliveIntervalHasCPUSafetyFloor(t *testing.T) {
	cfg := (Config{KeepaliveInterval: time.Nanosecond}).withDefaults()
	if cfg.KeepaliveInterval != minimumKeepaliveInterval {
		t.Fatalf("keepalive interval = %s, want safety floor %s", cfg.KeepaliveInterval, minimumKeepaliveInterval)
	}
}
