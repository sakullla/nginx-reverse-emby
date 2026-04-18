package relay

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var relayTLSTCPSessionPool = newTLSTCPSessionPool()

type tlsTCPSessionPoolStats struct {
	ActiveSessions int
	LogicalStreams int
}

type tlsTCPSessionPool struct {
	mu       sync.Mutex
	sessions map[string]*tlsTCPTunnel
}

type tlsTCPTunnel struct {
	key        string
	rawConn    net.Conn
	reader     io.Reader
	writer     io.Writer
	closeOuter func() error

	writeMu sync.Mutex

	streamsMu    sync.Mutex
	streams      map[uint32]*tlsTCPLogicalStream
	nextStreamID atomic.Uint32

	closed    chan struct{}
	closeOnce sync.Once
}

type tlsTCPLogicalStream struct {
	tunnel   *tlsTCPTunnel
	streamID uint32

	readMu      sync.Mutex
	readChunks  [][]byte
	readOffset  int
	readCh      chan struct{}
	readErr     error
	readErrSet  bool
	writeClosed bool

	openResultCh chan error
}

func newTLSTCPSessionPool() *tlsTCPSessionPool {
	return &tlsTCPSessionPool{
		sessions: make(map[string]*tlsTCPTunnel),
	}
}

func currentTLSTCPSessionPoolStats() tlsTCPSessionPoolStats {
	relayTLSTCPSessionPool.mu.Lock()
	defer relayTLSTCPSessionPool.mu.Unlock()

	stats := tlsTCPSessionPoolStats{ActiveSessions: len(relayTLSTCPSessionPool.sessions)}
	for _, session := range relayTLSTCPSessionPool.sessions {
		stats.LogicalStreams += session.logicalStreamCount()
	}
	return stats
}

func resetTLSTCPSessionPoolForTest() {
	relayTLSTCPSessionPool.mu.Lock()
	sessions := relayTLSTCPSessionPool.sessions
	relayTLSTCPSessionPool.sessions = make(map[string]*tlsTCPTunnel)
	relayTLSTCPSessionPool.mu.Unlock()

	for _, session := range sessions {
		_ = session.close()
	}
}

func (p *tlsTCPSessionPool) getOrDial(ctx context.Context, key string, dial func(context.Context) (*tlsTCPTunnel, error)) (*tlsTCPTunnel, error) {
	if existing := p.get(key); existing != nil {
		return existing, nil
	}

	tunnel, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	return p.store(key, tunnel), nil
}

func (p *tlsTCPSessionPool) get(key string) *tlsTCPTunnel {
	p.mu.Lock()
	defer p.mu.Unlock()

	tunnel := p.sessions[key]
	if tunnel == nil {
		return nil
	}
	select {
	case <-tunnel.closed:
		delete(p.sessions, key)
		return nil
	default:
		return tunnel
	}
}

func (p *tlsTCPSessionPool) store(key string, tunnel *tlsTCPTunnel) *tlsTCPTunnel {
	p.mu.Lock()
	existing := p.sessions[key]
	if existing != nil {
		select {
		case <-existing.closed:
		default:
			p.mu.Unlock()
			_ = tunnel.close()
			return existing
		}
	}
	p.sessions[key] = tunnel
	p.mu.Unlock()

	go func() {
		<-tunnel.closed
		p.remove(key, tunnel)
	}()

	return tunnel
}

func (p *tlsTCPSessionPool) remove(key string, tunnel *tlsTCPTunnel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing := p.sessions[key]; existing == tunnel {
		delete(p.sessions, key)
	}
}

func dialTLSTCPMux(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider) (net.Conn, error) {
	firstHop := chain[0]
	sessionKey, err := tlsTCPSessionPoolKey(firstHop)
	if err != nil {
		return nil, err
	}

	tunnel, err := relayTLSTCPSessionPool.getOrDial(ctx, sessionKey, func(dialCtx context.Context) (*tlsTCPTunnel, error) {
		return dialNewTLSTCPTunnel(dialCtx, firstHop, provider)
	})
	if err != nil {
		return nil, err
	}

	return tunnel.openStream(ctx, relayOpenFrame{
		Kind:   network,
		Target: target,
		Chain:  append([]Hop(nil), chain[1:]...),
	})
}

func dialNewTLSTCPTunnel(ctx context.Context, hop Hop, provider TLSMaterialProvider) (*tlsTCPTunnel, error) {
	tlsConfig, err := clientTLSConfig(ctx, provider, hop.Listener, hop.Address, hop.ServerName)
	if err != nil {
		return nil, err
	}

	rawConn, err := dialTCP(ctx, hop.Address)
	if err != nil {
		return nil, err
	}

	relayConn := tls.Client(rawConn, tlsConfig)
	if err := handshakeTLS(ctx, relayConn); err != nil {
		_ = rawConn.Close()
		return nil, err
	}

	reader := io.Reader(relayConn)
	writer := io.Writer(relayConn)
	if listenerUsesEarlyWindowMask(hop.Listener) {
		masked := wrapConnWithEarlyWindowMask(relayConn, defaultEarlyWindowMaskConfig())
		reader = masked
		writer = masked
	}

	tunnel := &tlsTCPTunnel{
		key:        hop.Address,
		rawConn:    relayConn,
		reader:     reader,
		writer:     writer,
		closeOuter: relayConn.Close,
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	go tunnel.readLoop()
	return tunnel, nil
}

func tlsTCPSessionPoolKey(hop Hop) (string, error) {
	serverName, err := verificationServerName(hop.Address, hop.ServerName)
	if err != nil {
		return "", err
	}
	pinSetJSON, err := json.Marshal(hop.Listener.PinSet)
	if err != nil {
		return "", err
	}
	trustedCAJSON, err := json.Marshal(hop.Listener.TrustedCACertificateIDs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%d|%d|%s|%s|%s|%s|%t|%d|%s|%s",
		hop.Listener.ID,
		hop.Listener.Revision,
		hop.Address,
		serverName,
		normalizeListenerTransportModeValue(hop.Listener.TransportMode),
		normalizeTLSModeValue(hop.Listener.TLSMode),
		hop.Listener.AllowSelfSigned,
		valueOrZero(hop.Listener.CertificateID),
		string(pinSetJSON),
		string(trustedCAJSON),
	), nil
}

func normalizeTLSModeValue(mode string) string {
	normalized, err := normalizeTLSMode(mode)
	if err != nil {
		return strings.TrimSpace(strings.ToLower(mode))
	}
	return normalized
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func (t *tlsTCPTunnel) openStream(ctx context.Context, req relayOpenFrame) (net.Conn, error) {
	streamID := t.nextStreamID.Add(1)
	stream := &tlsTCPLogicalStream{
		tunnel:       t,
		streamID:     streamID,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan error, 1),
	}
	t.registerStream(stream)

	payload, err := marshalMuxOpenPayload(req)
	if err != nil {
		t.removeStream(streamID)
		return nil, err
	}
	if err := t.writeFrame(ctx, muxFrame{
		Type:     muxFrameTypeOpen,
		Flags:    muxFlagAckRequired,
		StreamID: streamID,
		Payload:  payload,
	}); err != nil {
		t.removeStream(streamID)
		return nil, err
	}

	select {
	case err := <-stream.openResultCh:
		if err != nil {
			t.removeStream(streamID)
			return nil, err
		}
		return stream, nil
	case <-ctx.Done():
		t.removeStream(streamID)
		return nil, ctx.Err()
	case <-t.closed:
		t.removeStream(streamID)
		return nil, io.EOF
	}
}

func (t *tlsTCPTunnel) writeFrame(ctx context.Context, frame muxFrame) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	return withWriteDeadline(t.rawConn, relayFrameTimeout, func() error {
		return writeMuxFrame(t.writer, frame)
	})
}

func (t *tlsTCPTunnel) readLoop() {
	for {
		frame, err := readMuxFrame(t.reader)
		if err != nil {
			t.failAllStreams(err)
			_ = t.close()
			return
		}

		stream := t.getStream(frame.StreamID)
		if stream == nil {
			continue
		}

		switch frame.Type {
		case muxFrameTypeOpenResult:
			result, err := readMuxOpenResultPayload(frame.Payload)
			if err != nil {
				stream.deliverOpenResult(err)
				continue
			}
			if !result.OK {
				stream.deliverOpenResult(fmt.Errorf("relay connection failed: %s", result.Error))
				continue
			}
			stream.deliverOpenResult(nil)
		case muxFrameTypeData:
			stream.appendData(frame.Payload)
		case muxFrameTypeFin:
			stream.setReadError(io.EOF)
			t.removeStream(frame.StreamID)
		case muxFrameTypeRst:
			stream.setReadError(io.ErrClosedPipe)
			t.removeStream(frame.StreamID)
		}
	}
}

func (t *tlsTCPTunnel) close() error {
	var err error
	t.closeOnce.Do(func() {
		close(t.closed)
		err = t.closeOuter()
	})
	return err
}

func (t *tlsTCPTunnel) logicalStreamCount() int {
	t.streamsMu.Lock()
	defer t.streamsMu.Unlock()
	return len(t.streams)
}

func (t *tlsTCPTunnel) registerStream(stream *tlsTCPLogicalStream) {
	t.streamsMu.Lock()
	t.streams[stream.streamID] = stream
	t.streamsMu.Unlock()
}

func (t *tlsTCPTunnel) getStream(id uint32) *tlsTCPLogicalStream {
	t.streamsMu.Lock()
	defer t.streamsMu.Unlock()
	return t.streams[id]
}

func (t *tlsTCPTunnel) removeStream(id uint32) {
	t.streamsMu.Lock()
	delete(t.streams, id)
	t.streamsMu.Unlock()
}

func (t *tlsTCPTunnel) failAllStreams(err error) {
	t.streamsMu.Lock()
	streams := make([]*tlsTCPLogicalStream, 0, len(t.streams))
	for _, stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[uint32]*tlsTCPLogicalStream)
	t.streamsMu.Unlock()

	for _, stream := range streams {
		stream.deliverOpenResult(err)
		stream.setReadError(err)
	}
}

func (s *tlsTCPLogicalStream) Read(p []byte) (int, error) {
	for {
		s.readMu.Lock()
		if len(s.readChunks) > 0 {
			total := 0
			for total < len(p) && len(s.readChunks) > 0 {
				head := s.readChunks[0]
				n := copy(p[total:], head[s.readOffset:])
				total += n
				s.readOffset += n
				if s.readOffset < len(head) {
					break
				}
				s.readChunks[0] = nil
				s.readChunks = s.readChunks[1:]
				s.readOffset = 0
			}
			s.readMu.Unlock()
			return total, nil
		}
		if s.readErrSet {
			err := s.readErr
			s.readMu.Unlock()
			return 0, err
		}
		s.readMu.Unlock()

		select {
		case <-s.readCh:
		case <-s.tunnel.closed:
			return 0, io.EOF
		}
	}
}

func (s *tlsTCPLogicalStream) Write(p []byte) (int, error) {
	s.readMu.Lock()
	writeClosed := s.writeClosed
	s.readMu.Unlock()
	if writeClosed {
		return 0, io.ErrClosedPipe
	}
	if err := s.tunnel.writeFrame(context.Background(), muxFrame{
		Type:     muxFrameTypeData,
		StreamID: s.streamID,
		Payload:  append([]byte(nil), p...),
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *tlsTCPLogicalStream) Close() error {
	_ = s.CloseWrite()
	s.tunnel.removeStream(s.streamID)
	s.setReadError(io.EOF)
	return nil
}

func (s *tlsTCPLogicalStream) LocalAddr() net.Addr {
	return s.tunnel.rawConn.LocalAddr()
}

func (s *tlsTCPLogicalStream) RemoteAddr() net.Addr {
	return s.tunnel.rawConn.RemoteAddr()
}

func (s *tlsTCPLogicalStream) SetDeadline(t time.Time) error {
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	return s.SetWriteDeadline(t)
}

func (s *tlsTCPLogicalStream) SetReadDeadline(t time.Time) error {
	return nil
}

func (s *tlsTCPLogicalStream) SetWriteDeadline(t time.Time) error {
	return nil
}

func (s *tlsTCPLogicalStream) CloseWrite() error {
	s.readMu.Lock()
	if s.writeClosed {
		s.readMu.Unlock()
		return nil
	}
	s.writeClosed = true
	s.readMu.Unlock()
	return s.tunnel.writeFrame(context.Background(), muxFrame{
		Type:     muxFrameTypeFin,
		StreamID: s.streamID,
	})
}

func (s *tlsTCPLogicalStream) CloseRead() error {
	s.setReadError(io.EOF)
	return nil
}

func (s *tlsTCPLogicalStream) ConnectionState() tls.ConnectionState {
	if conn, ok := s.tunnel.rawConn.(interface{ ConnectionState() tls.ConnectionState }); ok {
		return conn.ConnectionState()
	}
	return tls.ConnectionState{}
}

func (s *tlsTCPLogicalStream) appendData(payload []byte) {
	s.readMu.Lock()
	s.readChunks = append(s.readChunks, payload)
	s.readMu.Unlock()
	s.notifyReadable()
}

func (s *tlsTCPLogicalStream) setReadError(err error) {
	s.readMu.Lock()
	if !s.readErrSet {
		s.readErr = err
		s.readErrSet = true
	}
	s.readMu.Unlock()
	s.notifyReadable()
}

func (s *tlsTCPLogicalStream) deliverOpenResult(err error) {
	select {
	case s.openResultCh <- err:
	default:
	}
}

func (s *tlsTCPLogicalStream) notifyReadable() {
	select {
	case s.readCh <- struct{}{}:
	default:
	}
}

type serverTLSTCPSession struct {
	tunnel *tlsTCPTunnel
	server *Server
}

func newServerTLSTCPSession(conn net.Conn) *serverTLSTCPSession {
	tunnel := &tlsTCPTunnel{
		key:        "server",
		rawConn:    conn,
		reader:     conn,
		writer:     conn,
		closeOuter: conn.Close,
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	return &serverTLSTCPSession{tunnel: tunnel}
}

func (s *Server) handleMuxTLSTCPConn(clientConn net.Conn, listener Listener) {
	session := newServerTLSTCPSession(clientConn)
	session.server = s
	session.run(listener)
}

func (s *serverTLSTCPSession) run(listener Listener) {
	for {
		frame, err := readMuxFrame(s.tunnel.reader)
		if err != nil {
			_ = s.tunnel.close()
			return
		}

		switch frame.Type {
		case muxFrameTypeOpen:
			request, err := readMuxOpenPayload(frame.Payload)
			if err != nil {
				_ = s.writeOpenResult(frame.StreamID, muxOpenResult{OK: false, Error: err.Error()})
				continue
			}
			if !strings.EqualFold(request.Kind, "tcp") && !strings.EqualFold(request.Kind, "udp") {
				_ = s.writeOpenResult(frame.StreamID, muxOpenResult{OK: false, Error: fmt.Sprintf("unsupported network %q", request.Kind)})
				continue
			}

			stream := &tlsTCPLogicalStream{
				tunnel:       s.tunnel,
				streamID:     frame.StreamID,
				readCh:       make(chan struct{}, 1),
				openResultCh: make(chan error, 1),
			}
			s.tunnel.registerStream(stream)
			go s.handleStream(listener, stream, request)
		case muxFrameTypeData:
			if stream := s.tunnel.getStream(frame.StreamID); stream != nil {
				stream.appendData(frame.Payload)
			}
		case muxFrameTypeFin:
			if stream := s.tunnel.getStream(frame.StreamID); stream != nil {
				stream.setReadError(io.EOF)
				s.tunnel.removeStream(frame.StreamID)
			}
		case muxFrameTypeRst:
			if stream := s.tunnel.getStream(frame.StreamID); stream != nil {
				stream.setReadError(io.ErrClosedPipe)
				s.tunnel.removeStream(frame.StreamID)
			}
		}
	}
}

func (s *serverTLSTCPSession) handleStream(listener Listener, stream *tlsTCPLogicalStream, request relayOpenFrame) {
	if strings.EqualFold(request.Kind, "udp") {
		s.handleUDPStream(listener, stream, request)
		return
	}

	upstream, err := s.server.openUpstream(request.Kind, request.Target, request.Chain, DialOptions{})
	if err != nil {
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	s.server.trackConn(upstream)
	defer s.server.untrackConn(upstream)
	defer upstream.Close()

	if err := s.writeOpenResult(stream.streamID, muxOpenResult{OK: true}); err != nil {
		s.tunnel.removeStream(stream.streamID)
		return
	}

	pipeBothWays(wrapIdleConn(stream), wrapIdleConn(upstream))
	s.tunnel.removeStream(stream.streamID)
}

func (s *serverTLSTCPSession) handleUDPStream(listener Listener, stream *tlsTCPLogicalStream, request relayOpenFrame) {
	upstream, err := s.server.openUDPPeer(request.Target, request.Chain)
	if err != nil {
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	defer upstream.Close()

	if err := s.writeOpenResult(stream.streamID, muxOpenResult{OK: true}); err != nil {
		s.tunnel.removeStream(stream.streamID)
		return
	}

	pipeUDPPackets(stream, upstream)
	s.tunnel.removeStream(stream.streamID)
}

func (s *serverTLSTCPSession) writeOpenResult(streamID uint32, result muxOpenResult) error {
	payload, err := marshalMuxOpenResultPayload(result)
	if err != nil {
		return err
	}
	return s.tunnel.writeFrame(context.Background(), muxFrame{
		Type:     muxFrameTypeOpenResult,
		StreamID: streamID,
		Payload:  payload,
	})
}
