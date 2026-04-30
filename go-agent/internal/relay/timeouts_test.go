package relay

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

type fakeRelayTCPBufferConn struct {
	readBuffer  int
	writeBuffer int
	noDelay     bool
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

func (c *fakeRelayTCPBufferConn) SetNoDelay(noDelay bool) error {
	c.noDelay = noDelay
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

func TestIdleDeadlineConnReadFromRefreshesWriteDeadlinePerChunk(t *testing.T) {
	conn := &recordingBulkConn{}
	wrapped := &idleDeadlineConn{Conn: conn, timeout: time.Minute}

	n, err := wrapped.ReadFrom(&relayChunkedReader{chunks: [][]byte{
		[]byte("first"),
		[]byte("second"),
	}})
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if n != int64(len("firstsecond")) {
		t.Fatalf("ReadFrom() = %d, want %d", n, len("firstsecond"))
	}
	if conn.writeDeadlineCalls < 2 {
		t.Fatalf("SetWriteDeadline calls = %d, want at least 2", conn.writeDeadlineCalls)
	}
}

func TestIdleDeadlineConnWriteToRefreshesReadDeadlinePerChunk(t *testing.T) {
	conn := &recordingBulkConn{readChunks: [][]byte{
		[]byte("first"),
		[]byte("second"),
	}}
	wrapped := &idleDeadlineConn{Conn: conn, timeout: time.Minute}

	var dst bytes.Buffer
	n, err := wrapped.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if n != int64(len("firstsecond")) {
		t.Fatalf("WriteTo() = %d, want %d", n, len("firstsecond"))
	}
	if got := dst.String(); got != "firstsecond" {
		t.Fatalf("WriteTo() payload = %q, want %q", got, "firstsecond")
	}
	if conn.readDeadlineCalls < 2 {
		t.Fatalf("SetReadDeadline calls = %d, want at least 2", conn.readDeadlineCalls)
	}
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
	if !conn.noDelay {
		t.Fatal("relay TCP connection should enable TCP_NODELAY")
	}
}

func TestRelayTCPDialerEnablesMultipathTCP(t *testing.T) {
	dialer := newRelayTCPDialer()

	if !dialer.MultipathTCP() {
		t.Fatal("relay TCP dialer should enable MPTCP")
	}
}

func TestRelayTCPListenConfigEnablesMultipathTCP(t *testing.T) {
	listenConfig := newRelayTCPListenConfig()

	if !listenConfig.MultipathTCP() {
		t.Fatal("relay TCP listener should enable MPTCP")
	}
}

func TestTuneBulkRelayConnEnablesNoDelay(t *testing.T) {
	conn := &fakeRelayTCPBufferConn{}

	tuneBulkRelayConn(conn)

	if !conn.noDelay {
		t.Fatal("TCP_NODELAY not enabled")
	}
}

type recordingBulkConn struct {
	readChunks         [][]byte
	readDeadlineCalls  int
	writeDeadlineCalls int
}

func (c *recordingBulkConn) Read(p []byte) (int, error) {
	if len(c.readChunks) == 0 {
		return 0, io.EOF
	}
	chunk := c.readChunks[0]
	c.readChunks = c.readChunks[1:]
	return copy(p, chunk), nil
}

func (c *recordingBulkConn) Write(p []byte) (int, error) {
	return len(p), nil
}

func (c *recordingBulkConn) Close() error                       { return nil }
func (c *recordingBulkConn) LocalAddr() net.Addr                { return nil }
func (c *recordingBulkConn) RemoteAddr() net.Addr               { return nil }
func (c *recordingBulkConn) SetDeadline(_ time.Time) error      { return nil }
func (c *recordingBulkConn) SetReadDeadline(_ time.Time) error  { c.readDeadlineCalls++; return nil }
func (c *recordingBulkConn) SetWriteDeadline(_ time.Time) error { c.writeDeadlineCalls++; return nil }

func (c *recordingBulkConn) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(io.Discard, r)
}

func (c *recordingBulkConn) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for {
		buf := make([]byte, 1024)
		n, err := c.Read(buf)
		if n > 0 {
			written, writeErr := w.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if err != nil {
			if err == io.EOF {
				return total, nil
			}
			return total, err
		}
	}
}

type relayChunkedReader struct {
	chunks [][]byte
}

func (r *relayChunkedReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}
