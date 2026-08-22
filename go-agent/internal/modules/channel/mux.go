package channel

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	muxFrameOpen       byte = 1
	muxFrameOpenResult byte = 2
	muxFrameData       byte = 3
	muxFrameFin        byte = 4
	muxFrameRst        byte = 5

	muxProtocolTCP byte = 1
	muxProtocolUDP byte = 2

	muxHeaderSize   = 9
	muxMaxPayload   = 64*1024 + 2
	muxStreamQueue  = 64
	muxWriteTimeout = 30 * time.Second
)

var (
	errMuxClosed    = errors.New("channel mux is closed")
	errStreamClosed = errors.New("channel stream is closed")
	errStreamReset  = errors.New("channel stream was reset")
)

// mux multiplexes independent streams over one ordered connection. The opener
// side (the entry agent bridge) opens streams; the acceptor side (the exit
// agent) receives them. Either side may write data, half-close with FIN, or
// abort with RST.
type mux struct {
	conn   net.Conn
	opener bool

	writeMu sync.Mutex

	mu      sync.Mutex
	streams map[uint32]*muxStream
	nextID  uint32
	readErr error

	acceptCh  chan *muxStream
	done      chan struct{}
	closeOnce sync.Once
}

func newMuxOpener(conn net.Conn) *mux {
	return newMux(conn, true)
}

func newMuxAcceptor(conn net.Conn) *mux {
	return newMux(conn, false)
}

func newMux(conn net.Conn, opener bool) *mux {
	m := &mux{
		conn:     conn,
		opener:   opener,
		streams:  make(map[uint32]*muxStream),
		nextID:   1,
		acceptCh: make(chan *muxStream, 16),
		done:     make(chan struct{}),
	}
	go m.readLoop()
	return m
}

func (m *mux) writeFrame(frameType byte, streamID uint32, payload []byte) error {
	if len(payload) > muxMaxPayload {
		return fmt.Errorf("channel mux payload exceeds %d bytes", muxMaxPayload)
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	select {
	case <-m.done:
		return errMuxClosed
	default:
	}
	_ = m.conn.SetWriteDeadline(time.Now().Add(muxWriteTimeout))
	var header [muxHeaderSize]byte
	header[0] = frameType
	binary.BigEndian.PutUint32(header[1:5], streamID)
	binary.BigEndian.PutUint32(header[5:9], uint32(len(payload)))
	if _, err := m.conn.Write(header[:]); err != nil {
		m.closeWithError(err)
		return err
	}
	if len(payload) > 0 {
		if _, err := m.conn.Write(payload); err != nil {
			m.closeWithError(err)
			return err
		}
	}
	_ = m.conn.SetWriteDeadline(time.Time{})
	return nil
}

func (m *mux) readLoop() {
	var readErr error
	defer func() {
		m.closeWithError(readErr)
	}()
	header := make([]byte, muxHeaderSize)
	for {
		if _, err := io.ReadFull(m.conn, header); err != nil {
			readErr = err
			return
		}
		frameType := header[0]
		streamID := binary.BigEndian.Uint32(header[1:5])
		size := binary.BigEndian.Uint32(header[5:9])
		if size > muxMaxPayload {
			readErr = fmt.Errorf("channel mux payload size %d is invalid", size)
			return
		}
		var payload []byte
		if size > 0 {
			payload = make([]byte, size)
			if _, err := io.ReadFull(m.conn, payload); err != nil {
				readErr = err
				return
			}
		}
		switch frameType {
		case muxFrameOpen:
			if !m.handleOpen(streamID, payload) {
				readErr = errors.New("channel mux open frame is invalid")
				return
			}
		case muxFrameOpenResult:
			m.handleOpenResult(streamID, payload)
		case muxFrameData:
			if !m.handleData(streamID, payload) {
				readErr = fmt.Errorf("channel mux data frame for unknown stream %d", streamID)
				return
			}
		case muxFrameFin:
			m.handleFin(streamID)
		case muxFrameRst:
			m.handleRst(streamID)
		default:
			readErr = fmt.Errorf("channel mux frame type %d is unsupported", frameType)
			return
		}
	}
}

func (m *mux) handleOpen(streamID uint32, payload []byte) bool {
	if m.opener || len(payload) != 1 || (payload[0] != muxProtocolTCP && payload[0] != muxProtocolUDP) {
		return false
	}
	stream := newMuxStream(m, streamID, payload[0])
	m.mu.Lock()
	if _, exists := m.streams[streamID]; exists {
		m.mu.Unlock()
		return false
	}
	m.streams[streamID] = stream
	m.mu.Unlock()
	select {
	case m.acceptCh <- stream:
	case <-m.done:
	}
	return true
}

func (m *mux) handleOpenResult(streamID uint32, payload []byte) {
	m.mu.Lock()
	stream := m.streams[streamID]
	m.mu.Unlock()
	if stream == nil {
		return
	}
	var err error
	if len(payload) == 0 || payload[0] != 0 {
		message := ""
		if len(payload) > 1 {
			message = string(payload[1:])
		}
		if message == "" {
			message = "stream open was rejected"
		}
		err = errors.New(message)
	}
	stream.deliverOpenResult(err)
}

func (m *mux) handleData(streamID uint32, payload []byte) bool {
	m.mu.Lock()
	stream := m.streams[streamID]
	m.mu.Unlock()
	if stream == nil {
		// The peer may still flush data for a stream we closed; discard it.
		return true
	}
	select {
	case stream.incoming <- payload:
	case <-stream.done:
	case <-m.done:
	}
	return true
}

func (m *mux) handleFin(streamID uint32) {
	m.mu.Lock()
	stream := m.streams[streamID]
	m.mu.Unlock()
	if stream != nil {
		stream.closeRead()
	}
}

func (m *mux) handleRst(streamID uint32) {
	m.mu.Lock()
	stream := m.streams[streamID]
	delete(m.streams, streamID)
	m.mu.Unlock()
	if stream != nil {
		stream.reset()
	}
}

// OpenStream opens a new stream towards the acceptor and waits for it to be
// accepted. Only the opener side may call OpenStream.
func (m *mux) OpenStream(ctx context.Context, udp bool) (*muxStream, error) {
	if !m.opener {
		return nil, errors.New("only the channel opener may open streams")
	}
	protocol := muxProtocolTCP
	if udp {
		protocol = muxProtocolUDP
	}
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	stream := newMuxStream(m, id, protocol)
	m.streams[id] = stream
	m.mu.Unlock()

	if err := m.writeFrame(muxFrameOpen, id, []byte{protocol}); err != nil {
		m.removeStream(id)
		return nil, err
	}
	select {
	case err := <-stream.openResult:
		if err != nil {
			m.removeStream(id)
			return nil, err
		}
		return stream, nil
	case <-ctx.Done():
		_ = stream.Close()
		return nil, ctx.Err()
	case <-m.done:
		m.removeStream(id)
		return nil, errMuxClosed
	}
}

// AcceptStream returns the next stream opened by the peer. Only the acceptor
// side may call AcceptStream.
func (m *mux) AcceptStream() (*muxStream, error) {
	if m.opener {
		return nil, errors.New("only the channel acceptor may accept streams")
	}
	select {
	case stream := <-m.acceptCh:
		return stream, nil
	case <-m.done:
		return nil, errMuxClosed
	}
}

func (m *mux) removeStream(id uint32) {
	m.mu.Lock()
	delete(m.streams, id)
	m.mu.Unlock()
}

func (m *mux) closeWithError(err error) {
	m.closeOnce.Do(func() {
		if err == nil {
			err = errMuxClosed
		}
		m.mu.Lock()
		m.readErr = err
		streams := make([]*muxStream, 0, len(m.streams))
		for _, stream := range m.streams {
			streams = append(streams, stream)
		}
		m.mu.Unlock()
		_ = m.conn.Close()
		close(m.done)
		for _, stream := range streams {
			stream.reset()
		}
	})
}

// Close terminates the mux and every open stream.
func (m *mux) Close() error {
	m.closeWithError(nil)
	return nil
}

// Err reports the terminal read error, if any.
func (m *mux) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readErr
}

// Done is closed when the mux terminates.
func (m *mux) Done() <-chan struct{} {
	return m.done
}

// muxStream is one logical stream. For UDP streams each Read returns exactly
// one datagram and each Write carries exactly one datagram.
type muxStream struct {
	m        *mux
	id       uint32
	protocol byte

	incoming   chan []byte
	readEOF    chan struct{}
	done       chan struct{}
	openResult chan error

	eofOnce   sync.Once
	finOnce   sync.Once
	closeOnce sync.Once

	writeMu sync.Mutex
}

func newMuxStream(m *mux, id uint32, protocol byte) *muxStream {
	return &muxStream{
		m:          m,
		id:         id,
		protocol:   protocol,
		incoming:   make(chan []byte, muxStreamQueue),
		readEOF:    make(chan struct{}),
		done:       make(chan struct{}),
		openResult: make(chan error, 1),
	}
}

func (s *muxStream) deliverOpenResult(err error) {
	select {
	case s.openResult <- err:
	default:
	}
}

// acceptOpen acknowledges a peer-opened stream on the acceptor side.
func (s *muxStream) acceptOpen() error {
	return s.m.writeFrame(muxFrameOpenResult, s.id, []byte{0})
}

// rejectOpen refuses a peer-opened stream on the acceptor side.
func (s *muxStream) rejectOpen(message string) {
	payload := append([]byte{1}, []byte(message)...)
	if err := s.m.writeFrame(muxFrameOpenResult, s.id, payload); err != nil {
		return
	}
	s.m.removeStream(s.id)
	s.reset()
}

func (s *muxStream) closeRead() {
	s.eofOnce.Do(func() { close(s.readEOF) })
}

func (s *muxStream) reset() {
	s.closeOnce.Do(func() {
		s.closeRead()
		close(s.done)
		s.deliverOpenResult(errStreamReset)
	})
}

func (s *muxStream) Read(p []byte) (int, error) {
	for {
		select {
		case data := <-s.incoming:
			if len(data) > len(p) {
				return 0, fmt.Errorf("channel stream read buffer %d smaller than message %d", len(p), len(data))
			}
			return copy(p, data), nil
		case <-s.readEOF:
			select {
			case data := <-s.incoming:
				if len(data) > len(p) {
					return 0, fmt.Errorf("channel stream read buffer %d smaller than message %d", len(p), len(data))
				}
				return copy(p, data), nil
			default:
			}
			select {
			case <-s.done:
				return 0, errStreamReset
			default:
			}
			return 0, io.EOF
		case <-s.done:
			select {
			case data := <-s.incoming:
				if len(data) > len(p) {
					return 0, fmt.Errorf("channel stream read buffer %d smaller than message %d", len(p), len(data))
				}
				return copy(p, data), nil
			default:
			}
			return 0, errStreamReset
		}
	}
}

func (s *muxStream) Write(p []byte) (int, error) {
	if len(p) > muxMaxPayload {
		return 0, fmt.Errorf("channel stream write exceeds %d bytes", muxMaxPayload)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.done:
		return 0, errStreamClosed
	default:
	}
	if err := s.m.writeFrame(muxFrameData, s.id, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// CloseWrite half-closes the stream: the peer observes EOF after draining all
// queued data.
func (s *muxStream) CloseWrite() error {
	s.finOnce.Do(func() {
		_ = s.m.writeFrame(muxFrameFin, s.id, nil)
	})
	return nil
}

// Close aborts the stream and releases it from the mux.
func (s *muxStream) Close() error {
	s.closeOnce.Do(func() {
		_ = s.m.writeFrame(muxFrameRst, s.id, nil)
		s.m.removeStream(s.id)
		s.closeRead()
		close(s.done)
		s.deliverOpenResult(errStreamClosed)
	})
	return nil
}
