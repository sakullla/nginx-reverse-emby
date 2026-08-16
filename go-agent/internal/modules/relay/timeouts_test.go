//go:build !integration

package relay

import (
	"io"
	"net"

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

// Simulate a blackholed candidate: block until the dial context expires.

// Real dialers fail immediately once the context is spent.

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
