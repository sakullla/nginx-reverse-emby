package relay

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	defaultObfsMaxDataBytes = 4096
	defaultObfsMaxPadFrames = 4
	maxObfsPadFrameBytes    = 32
	maxObfsDataFrameBytes   = 512
)

var errInvalidRelayObfsFrame = errors.New("invalid relay obfs frame")

type obfsFrameType byte

const (
	obfsFrameData obfsFrameType = 1
	obfsFramePad  obfsFrameType = 2
	obfsFrameEnd  obfsFrameType = 3
)

type obfsConfig struct {
	MaxDataBytes int
	MaxPadFrames int
	Seed         int64
}

func defaultObfsConfig() obfsConfig {
	return obfsConfig{
		MaxDataBytes: defaultObfsMaxDataBytes,
		MaxPadFrames: defaultObfsMaxPadFrames,
		Seed:         time.Now().UnixNano(),
	}
}

func normalizeObfsConfig(cfg obfsConfig) obfsConfig {
	if cfg.MaxDataBytes <= 0 {
		cfg.MaxDataBytes = defaultObfsMaxDataBytes
	}
	if cfg.MaxPadFrames < 0 {
		cfg.MaxPadFrames = 0
	}
	if cfg.Seed == 0 {
		cfg.Seed = time.Now().UnixNano()
	}
	return cfg
}

type obfsFirstSegmentWriter struct {
	mu     sync.Mutex
	writer io.Writer
	cfg    obfsConfig
	random *rand.Rand
	framed bool
	closed bool
}

func newObfsFirstSegmentWriter(w io.Writer, cfg obfsConfig) *obfsFirstSegmentWriter {
	cfg = normalizeObfsConfig(cfg)
	return &obfsFirstSegmentWriter{
		writer: w,
		cfg:    cfg,
		random: rand.New(rand.NewSource(cfg.Seed)),
	}
}

func (w *obfsFirstSegmentWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, io.ErrClosedPipe
	}
	if w.framed {
		if err := writeAll(w.writer, p); err != nil {
			return 0, err
		}
		return len(p), nil
	}

	segmentBytes := len(p)
	if segmentBytes > w.cfg.MaxDataBytes {
		segmentBytes = w.cfg.MaxDataBytes
	}
	if err := w.writeFramedSegment(p[:segmentBytes]); err != nil {
		return 0, err
	}
	w.framed = true

	if segmentBytes < len(p) {
		if err := writeAll(w.writer, p[segmentBytes:]); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (w *obfsFirstSegmentWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}

func (w *obfsFirstSegmentWriter) writeFramedSegment(payload []byte) error {
	remaining := payload
	padBudget := w.cfg.MaxPadFrames

	for len(remaining) > 0 {
		chunkSize := len(remaining)
		if chunkSize > maxObfsDataFrameBytes {
			chunkSize = maxObfsDataFrameBytes
		}
		if chunkSize > 1 {
			chunkSize = 1 + w.random.Intn(chunkSize)
		}

		if err := writeObfsFrame(w.writer, obfsFrameData, remaining[:chunkSize]); err != nil {
			return err
		}
		remaining = remaining[chunkSize:]

		if padBudget > 0 && (len(remaining) > 0 || w.random.Intn(2) == 0) {
			padBudget--
			padLen := 1 + w.random.Intn(maxObfsPadFrameBytes)
			pad := make([]byte, padLen)
			if _, err := w.random.Read(pad); err != nil {
				return err
			}
			if err := writeObfsFrame(w.writer, obfsFramePad, pad); err != nil {
				return err
			}
		}
	}

	return writeObfsFrame(w.writer, obfsFrameEnd, nil)
}

type obfsFirstSegmentReader struct {
	reader      *bufio.Reader
	cfg         obfsConfig
	strictEOF   bool
	passthrough bool
	done        bool
	buffer      []byte
}

func newObfsFirstSegmentReader(r io.Reader, cfg obfsConfig) io.Reader {
	return newObfsFirstSegmentReaderWithMode(r, cfg, true)
}

func newObfsFirstSegmentReaderWithMode(r io.Reader, cfg obfsConfig, strictEOF bool) *obfsFirstSegmentReader {
	cfg = normalizeObfsConfig(cfg)
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return &obfsFirstSegmentReader{
		reader:    br,
		cfg:       cfg,
		strictEOF: strictEOF,
	}
}

func (r *obfsFirstSegmentReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for {
		if len(r.buffer) > 0 {
			n := copy(p, r.buffer)
			r.buffer = r.buffer[n:]
			return n, nil
		}
		if r.passthrough {
			return r.reader.Read(p)
		}
		if r.done {
			if r.strictEOF {
				if _, err := r.reader.Peek(1); err != nil {
					if errors.Is(err, io.EOF) {
						return 0, io.EOF
					}
					return 0, err
				}
				return 0, errInvalidRelayObfsFrame
			}
			r.passthrough = true
			continue
		}

		frameType, payload, err := readObfsFrame(r.reader, r.cfg)
		if err != nil {
			return 0, err
		}
		switch frameType {
		case obfsFrameData:
			r.buffer = payload
		case obfsFramePad:
		case obfsFrameEnd:
			r.done = true
		default:
			return 0, errInvalidRelayObfsFrame
		}
	}
}

type obfsConn struct {
	net.Conn
	reader io.Reader
	writer io.WriteCloser
}

func wrapConnWithFirstSegmentObfs(conn net.Conn, cfg obfsConfig) net.Conn {
	reader := newObfsFirstSegmentReaderWithMode(conn, cfg, false)
	writer := newObfsFirstSegmentWriter(conn, cfg)
	return &obfsConn{
		Conn:   conn,
		reader: reader,
		writer: writer,
	}
}

func WrapConnWithFirstSegmentObfs(conn net.Conn) net.Conn {
	return wrapConnWithFirstSegmentObfs(conn, defaultObfsConfig())
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
	if frameType == obfsFrameData && len(payload) > defaultObfsMaxDataBytes {
		return fmt.Errorf("%w: data frame too large", errInvalidRelayObfsFrame)
	}
	if frameType == obfsFramePad && len(payload) > maxObfsPadFrameBytes {
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

func readObfsFrame(r io.Reader, cfg obfsConfig) (obfsFrameType, []byte, error) {
	var header [3]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}

	frameType := obfsFrameType(header[0])
	payloadLen := int(binary.BigEndian.Uint16(header[1:]))
	switch frameType {
	case obfsFrameData:
		if payloadLen == 0 || payloadLen > cfg.MaxDataBytes {
			return 0, nil, errInvalidRelayObfsFrame
		}
	case obfsFramePad:
		if payloadLen == 0 || payloadLen > maxObfsPadFrameBytes {
			return 0, nil, errInvalidRelayObfsFrame
		}
	case obfsFrameEnd:
		if payloadLen != 0 {
			return 0, nil, errInvalidRelayObfsFrame
		}
		return frameType, nil, nil
	default:
		return 0, nil, errInvalidRelayObfsFrame
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return frameType, payload, nil
}
