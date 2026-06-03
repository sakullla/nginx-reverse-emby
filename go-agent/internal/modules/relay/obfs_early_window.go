package relay

import (
	"bufio"
	"errors"
	"io"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	defaultEarlyWindowMaskBytes  = 32 * 1024
	defaultEarlyWindowMaskWrites = 8
	maxEarlyWindowPadFrameBytes  = 32
	maxEarlyWindowDataFrameBytes = 512
)

type earlyWindowMaskConfig struct {
	MaxBytes  int
	MaxWrites int
	Seed      int64
}

func defaultEarlyWindowMaskConfig() earlyWindowMaskConfig {
	return earlyWindowMaskConfig{
		MaxBytes:  defaultEarlyWindowMaskBytes,
		MaxWrites: defaultEarlyWindowMaskWrites,
		Seed:      time.Now().UnixNano(),
	}
}

func normalizeEarlyWindowMaskConfig(cfg earlyWindowMaskConfig) earlyWindowMaskConfig {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = defaultEarlyWindowMaskBytes
	}
	if cfg.MaxWrites <= 0 {
		cfg.MaxWrites = defaultEarlyWindowMaskWrites
	}
	if cfg.Seed == 0 {
		cfg.Seed = time.Now().UnixNano()
	}
	return cfg
}

type earlyWindowMaskWriter struct {
	mu     sync.Mutex
	writer io.Writer
	cfg    earlyWindowMaskConfig
	random *rand.Rand
	closed bool

	windowClosed  bool
	maskedBytes   int
	maskedWrites  int
	padFramesLeft int
}

func newEarlyWindowMaskWriter(w io.Writer, cfg earlyWindowMaskConfig) *earlyWindowMaskWriter {
	cfg = normalizeEarlyWindowMaskConfig(cfg)
	return &earlyWindowMaskWriter{
		writer:        w,
		cfg:           cfg,
		random:        rand.New(rand.NewSource(cfg.Seed)),
		padFramesLeft: cfg.MaxWrites * 2,
	}
}

func (w *earlyWindowMaskWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, io.ErrClosedPipe
	}
	if w.windowClosed {
		if err := writeAll(w.writer, p); err != nil {
			return 0, err
		}
		return len(p), nil
	}

	if w.maskedWrites >= w.cfg.MaxWrites || w.maskedBytes >= w.cfg.MaxBytes {
		if err := w.closeWindow(); err != nil {
			return 0, err
		}
		if err := writeAll(w.writer, p); err != nil {
			return 0, err
		}
		return len(p), nil
	}

	remainingBudget := w.cfg.MaxBytes - w.maskedBytes
	maskedBytes := len(p)
	if maskedBytes > remainingBudget {
		maskedBytes = remainingBudget
	}
	if maskedBytes > 0 {
		if err := w.writeMaskedChunks(p[:maskedBytes]); err != nil {
			return 0, err
		}
		w.maskedBytes += maskedBytes
		w.maskedWrites++
	}

	if w.maskedWrites >= w.cfg.MaxWrites || w.maskedBytes >= w.cfg.MaxBytes {
		if err := w.closeWindow(); err != nil {
			return 0, err
		}
	}

	if maskedBytes < len(p) {
		if err := writeAll(w.writer, p[maskedBytes:]); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (w *earlyWindowMaskWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.windowClosed && (w.maskedBytes > 0 || w.maskedWrites > 0) {
		if err := w.closeWindow(); err != nil {
			return err
		}
	}
	w.closed = true
	return nil
}

func (w *earlyWindowMaskWriter) writeMaskedChunks(payload []byte) error {
	remaining := payload
	for len(remaining) > 0 {
		chunkSize := len(remaining)
		if chunkSize > maxEarlyWindowDataFrameBytes {
			chunkSize = maxEarlyWindowDataFrameBytes
		}
		if chunkSize > 1 {
			chunkSize = 1 + w.random.Intn(chunkSize)
		}

		if err := writeObfsFrame(w.writer, obfsFrameData, remaining[:chunkSize]); err != nil {
			return err
		}
		remaining = remaining[chunkSize:]

		if w.padFramesLeft > 0 && (len(remaining) > 0 || w.random.Intn(2) == 0) {
			w.padFramesLeft--
			padLen := 1 + w.random.Intn(maxEarlyWindowPadFrameBytes)
			pad := make([]byte, padLen)
			if _, err := w.random.Read(pad); err != nil {
				return err
			}
			if err := writeObfsFrame(w.writer, obfsFramePad, pad); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *earlyWindowMaskWriter) closeWindow() error {
	if w.windowClosed {
		return nil
	}
	w.windowClosed = true
	return writeObfsFrame(w.writer, obfsFrameEnd, nil)
}

type earlyWindowMaskReader struct {
	reader      *bufio.Reader
	cfg         earlyWindowMaskConfig
	strictEOF   bool
	passthrough bool
	done        bool
	buffer      []byte
}

func newEarlyWindowMaskReader(r io.Reader, cfg earlyWindowMaskConfig) io.Reader {
	return newEarlyWindowMaskReaderWithMode(r, cfg, true)
}

func newEarlyWindowMaskReaderWithMode(r io.Reader, cfg earlyWindowMaskConfig, strictEOF bool) *earlyWindowMaskReader {
	cfg = normalizeEarlyWindowMaskConfig(cfg)
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return &earlyWindowMaskReader{
		reader:    br,
		cfg:       cfg,
		strictEOF: strictEOF,
	}
}

func (r *earlyWindowMaskReader) Read(p []byte) (int, error) {
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

		frameType, payload, err := readEarlyWindowFrame(r.reader, r.cfg)
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

func wrapConnWithEarlyWindowMask(conn net.Conn, cfg earlyWindowMaskConfig) net.Conn {
	reader := newEarlyWindowMaskReaderWithMode(conn, cfg, false)
	writer := newEarlyWindowMaskWriter(conn, cfg)
	return &earlyWindowMaskConn{
		obfsConn: obfsConn{
			Conn:   conn,
			reader: reader,
			writer: writer,
		},
	}
}

func WrapConnWithEarlyWindowMask(conn net.Conn) net.Conn {
	return wrapConnWithEarlyWindowMask(conn, defaultEarlyWindowMaskConfig())
}

type earlyWindowMaskConn struct {
	obfsConn
}

func readEarlyWindowFrame(r io.Reader, cfg earlyWindowMaskConfig) (obfsFrameType, []byte, error) {
	var header [3]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}

	frameType := obfsFrameType(header[0])
	payloadLen := int(header[1])<<8 | int(header[2])
	switch frameType {
	case obfsFrameData:
		if payloadLen == 0 || payloadLen > cfg.MaxBytes {
			return 0, nil, errInvalidRelayObfsFrame
		}
	case obfsFramePad:
		if payloadLen == 0 || payloadLen > maxEarlyWindowPadFrameBytes {
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
