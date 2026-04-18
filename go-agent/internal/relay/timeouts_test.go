package relay

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

type fakeRelayTCPBufferConn struct {
	readBuffer  int
	writeBuffer int
}

func (c *fakeRelayTCPBufferConn) Read(_ []byte) (int, error)         { return 0, io.EOF }
func (c *fakeRelayTCPBufferConn) Write(p []byte) (int, error)        { return len(p), nil }
func (c *fakeRelayTCPBufferConn) Close() error                       { return nil }
func (c *fakeRelayTCPBufferConn) LocalAddr() net.Addr                { return nil }
func (c *fakeRelayTCPBufferConn) RemoteAddr() net.Addr               { return nil }
func (c *fakeRelayTCPBufferConn) SetDeadline(_ time.Time) error      { return nil }
func (c *fakeRelayTCPBufferConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *fakeRelayTCPBufferConn) SetWriteDeadline(_ time.Time) error { return nil }

func (c *fakeRelayTCPBufferConn) SetReadBuffer(bytes int) error {
	c.readBuffer = bytes
	return nil
}

func (c *fakeRelayTCPBufferConn) SetWriteBuffer(bytes int) error {
	c.writeBuffer = bytes
	return nil
}

func TestConfigureTimeoutsOverridesRelayPackageTimeouts(t *testing.T) {
	reset := ConfigureTimeouts(TimeoutConfig{
		DialTimeout:      9 * time.Second,
		HandshakeTimeout: 8 * time.Second,
		FrameTimeout:     7 * time.Second,
		IdleTimeout:      6 * time.Second,
	})
	defer reset()

	if relayDialTimeout != 9*time.Second {
		t.Fatalf("relayDialTimeout = %v", relayDialTimeout)
	}
	if relayIdleTimeout != 6*time.Second {
		t.Fatalf("relayIdleTimeout = %v", relayIdleTimeout)
	}
}

func TestConfigureTimeoutsAppliesNonZeroValuesAndResets(t *testing.T) {
	resetBase := ConfigureTimeouts(TimeoutConfig{
		DialTimeout:      4 * time.Second,
		HandshakeTimeout: 5 * time.Second,
		FrameTimeout:     6 * time.Second,
		IdleTimeout:      7 * time.Second,
	})
	defer resetBase()

	reset := ConfigureTimeouts(TimeoutConfig{
		DialTimeout: 11 * time.Second,
		IdleTimeout: 13 * time.Second,
	})
	if relayDialTimeout != 11*time.Second {
		t.Fatalf("relayDialTimeout = %v", relayDialTimeout)
	}
	if relayHandshakeTimeout != 5*time.Second {
		t.Fatalf("relayHandshakeTimeout = %v", relayHandshakeTimeout)
	}
	if relayFrameTimeout != 6*time.Second {
		t.Fatalf("relayFrameTimeout = %v", relayFrameTimeout)
	}
	if relayIdleTimeout != 13*time.Second {
		t.Fatalf("relayIdleTimeout = %v", relayIdleTimeout)
	}

	reset()
	if relayDialTimeout != 4*time.Second {
		t.Fatalf("relayDialTimeout after reset = %v", relayDialTimeout)
	}
	if relayIdleTimeout != 7*time.Second {
		t.Fatalf("relayIdleTimeout after reset = %v", relayIdleTimeout)
	}
}

func TestConfigureTimeoutsResetDoesNotOverwriteNewerConfiguration(t *testing.T) {
	resetOuter := ConfigureTimeouts(TimeoutConfig{
		DialTimeout:      4 * time.Second,
		HandshakeTimeout: 5 * time.Second,
		FrameTimeout:     6 * time.Second,
		IdleTimeout:      7 * time.Second,
	})
	defer resetOuter()

	resetInner := ConfigureTimeouts(TimeoutConfig{
		DialTimeout:      11 * time.Second,
		HandshakeTimeout: 12 * time.Second,
		FrameTimeout:     13 * time.Second,
		IdleTimeout:      14 * time.Second,
	})

	resetOuter()
	if relayDialTimeout != 11*time.Second {
		t.Fatalf("relayDialTimeout after stale reset = %v", relayDialTimeout)
	}
	if relayHandshakeTimeout != 12*time.Second {
		t.Fatalf("relayHandshakeTimeout after stale reset = %v", relayHandshakeTimeout)
	}
	if relayFrameTimeout != 13*time.Second {
		t.Fatalf("relayFrameTimeout after stale reset = %v", relayFrameTimeout)
	}
	if relayIdleTimeout != 14*time.Second {
		t.Fatalf("relayIdleTimeout after stale reset = %v", relayIdleTimeout)
	}

	resetInner()
	if relayDialTimeout != 5*time.Second {
		t.Fatalf("relayDialTimeout after inner reset = %v", relayDialTimeout)
	}
	if relayHandshakeTimeout != 5*time.Second {
		t.Fatalf("relayHandshakeTimeout after inner reset = %v", relayHandshakeTimeout)
	}
	if relayFrameTimeout != 5*time.Second {
		t.Fatalf("relayFrameTimeout after inner reset = %v", relayFrameTimeout)
	}
	if relayIdleTimeout != 2*time.Minute {
		t.Fatalf("relayIdleTimeout after inner reset = %v", relayIdleTimeout)
	}
}

func TestTuneBulkRelayConnAppliesReadAndWriteBuffers(t *testing.T) {
	conn := &fakeRelayTCPBufferConn{}

	tuneBulkRelayConn(conn)

	if conn.readBuffer != relayBulkSocketBufferBytes {
		t.Fatalf("readBuffer = %d, want %d", conn.readBuffer, relayBulkSocketBufferBytes)
	}
	if conn.writeBuffer != relayBulkSocketBufferBytes {
		t.Fatalf("writeBuffer = %d, want %d", conn.writeBuffer, relayBulkSocketBufferBytes)
	}
}

func TestTuneBulkRelayConnIgnoresUnsupportedConnections(t *testing.T) {
	tuneBulkRelayConn(struct{}{})
}

func TestDialTCPDoesNotTuneSocketBuffersForDirectConnections(t *testing.T) {
	originalDial := relayDialContext
	conn := &fakeRelayTCPBufferConn{}
	relayDialContext = func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("network = %q, want tcp", network)
		}
		if address != "relay.example:443" {
			t.Fatalf("address = %q, want relay.example:443", address)
		}
		return conn, nil
	}
	defer func() {
		relayDialContext = originalDial
	}()

	got, err := dialTCP(context.Background(), "relay.example:443")
	if err != nil {
		t.Fatalf("dialTCP() error = %v", err)
	}
	if got != conn {
		t.Fatalf("dialTCP() returned unexpected connection")
	}
	if conn.readBuffer != 0 {
		t.Fatalf("readBuffer = %d, want 0", conn.readBuffer)
	}
	if conn.writeBuffer != 0 {
		t.Fatalf("writeBuffer = %d, want 0", conn.writeBuffer)
	}
}

func TestDialRelayTCPTunesSocketBuffersForRelayConnections(t *testing.T) {
	originalDial := relayDialContext
	conn := &fakeRelayTCPBufferConn{}
	relayDialContext = func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("network = %q, want tcp", network)
		}
		if address != "relay.example:443" {
			t.Fatalf("address = %q, want relay.example:443", address)
		}
		return conn, nil
	}
	defer func() {
		relayDialContext = originalDial
	}()

	got, err := dialRelayTCP(context.Background(), "relay.example:443")
	if err != nil {
		t.Fatalf("dialRelayTCP() error = %v", err)
	}
	if got != conn {
		t.Fatalf("dialRelayTCP() returned unexpected connection")
	}
	if conn.readBuffer != relayBulkSocketBufferBytes {
		t.Fatalf("readBuffer = %d, want %d", conn.readBuffer, relayBulkSocketBufferBytes)
	}
	if conn.writeBuffer != relayBulkSocketBufferBytes {
		t.Fatalf("writeBuffer = %d, want %d", conn.writeBuffer, relayBulkSocketBufferBytes)
	}
}
