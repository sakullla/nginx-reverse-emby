package relay

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

var errInvalidRelayObfsFrame = errors.New("invalid relay obfs frame")

type obfsFrameType byte

const (
	obfsFrameData obfsFrameType = 1
	obfsFramePad  obfsFrameType = 2
	obfsFrameEnd  obfsFrameType = 3
)

type obfsConn struct {
	net.Conn
	reader io.Reader
	writer io.WriteCloser
}

func (c *obfsConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *obfsConn) Write(p []byte) (int, error) {
	return c.writer.Write(p)
}

func (c *obfsConn) Close() error {
	_ = c.writer.Close()
	return c.Conn.Close()
}

func (c *obfsConn) CloseWrite() error {
	_ = c.writer.Close()
	if closer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return c.Conn.Close()
}

func (c *obfsConn) CloseRead() error {
	if closer, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return closer.CloseRead()
	}
	return nil
}

func (c *obfsConn) WriteTo(w io.Writer) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
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
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
	}
}

func (c *obfsConn) ReadFrom(r io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			written, writeErr := c.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
	}
}

func writeObfsFrame(w io.Writer, frameType obfsFrameType, payload []byte) error {
	if frameType == obfsFrameEnd && len(payload) != 0 {
		return fmt.Errorf("%w: end frame must be empty", errInvalidRelayObfsFrame)
	}
	if frameType == obfsFramePad && len(payload) > maxEarlyWindowPadFrameBytes {
		return fmt.Errorf("%w: pad frame too large", errInvalidRelayObfsFrame)
	}
	if len(payload) > 0xffff {
		return fmt.Errorf("%w: frame payload too large", errInvalidRelayObfsFrame)
	}

	var header [3]byte
	header[0] = byte(frameType)
	binary.BigEndian.PutUint16(header[1:], uint16(len(payload)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	return writeAll(w, payload)
}
