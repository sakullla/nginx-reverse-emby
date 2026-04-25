package relay

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

var relayTLSTCPSessionPool = newTLSTCPSessionPool()
var errTLSTCPInteractiveAdmissionRejected = errors.New("tls_tcp relay interactive admission rejected: all tunnels are congested")

const tlsTCPBulkFrameSize = 64 * 1024
const tlsTCPMuxSessionsPerKey = 4
const tlsTCPMuxTargetStreamsPerSession = 2
const tlsTCPWriteQueueDepth = 8
const tlsTCPSingleStreamWritePipeline = 4
const tlsTCPInteractiveAdmissionQueuedWrites = 4
const tlsTCPInteractiveAdmissionBufferedBytes = 512 << 10

var tlsTCPMaxBufferedReadBytes = 32 << 20
var tlsTCPResumeBufferedReadBytes = 16 << 20

var tlsTCPBulkBufferPool = sync.Pool{
	New: func() any {
		return make([]byte, tlsTCPBulkFrameSize)
	},
}

type tlsTCPSessionPoolStats struct {
	ActiveSessions int
	LogicalStreams int
}

type tlsTCPSessionPool struct {
	mu       sync.Mutex
	sessions map[string][]*tlsTCPTunnel
}

type tlsTCPTunnel struct {
	key        string
	rawConn    net.Conn
	reader     io.Reader
	writer     io.Writer
	closeOuter func() error

	writeMu           sync.Mutex
	writeDeadlineNext time.Time
	writeReqCh        chan *tlsTCPWriteRequest
	writePumpOnce     sync.Once

	streamsMu      sync.Mutex
	streams        map[uint32]*tlsTCPLogicalStream
	nextStreamID   atomic.Uint32
	pendingStreams atomic.Int64
	queuedWrites   atomic.Int64
	bufferedBytes  atomic.Int64

	closed    chan struct{}
	closeOnce sync.Once
}

type tlsTCPLogicalStream struct {
	tunnel   *tlsTCPTunnel
	streamID uint32

	readMu            sync.Mutex
	readChunks        []tlsTCPReadChunk
	readBufferedBytes int
	readCh            chan struct{}
	readSpaceCh       chan struct{}
	readErr           error
	readErrSet        bool
	writeClosed       bool

	openResultCh chan muxOpenResult
}

type tlsTCPReadChunk struct {
	payload []byte
	release func()
}

type tlsTCPWriteRequest struct {
	frame muxFrame
	done  chan error
}

func (c *tlsTCPReadChunk) consume(n int) {
	c.payload = c.payload[n:]
}

func (c *tlsTCPReadChunk) releaseNow() {
	if c.release != nil {
		c.release()
		c.release = nil
	}
}

func newTLSTCPSessionPool() *tlsTCPSessionPool {
	return &tlsTCPSessionPool{
		sessions: make(map[string][]*tlsTCPTunnel),
	}
}

func currentTLSTCPSessionPoolStats() tlsTCPSessionPoolStats {
	relayTLSTCPSessionPool.mu.Lock()
	defer relayTLSTCPSessionPool.mu.Unlock()

	stats := tlsTCPSessionPoolStats{}
	for _, sessions := range relayTLSTCPSessionPool.sessions {
		stats.ActiveSessions += len(sessions)
		for _, session := range sessions {
			stats.LogicalStreams += session.logicalStreamCount()
		}
	}
	return stats
}

func resetTLSTCPSessionPoolForTest() {
	relayTLSTCPSessionPool.mu.Lock()
	sessions := relayTLSTCPSessionPool.sessions
	relayTLSTCPSessionPool.sessions = make(map[string][]*tlsTCPTunnel)
	relayTLSTCPSessionPool.mu.Unlock()

	for _, sessionGroup := range sessions {
		for _, session := range sessionGroup {
			_ = session.close()
		}
	}
}

func (p *tlsTCPSessionPool) allTunnelsForTest() []*tlsTCPTunnel {
	p.mu.Lock()
	defer p.mu.Unlock()

	var tunnels []*tlsTCPTunnel
	for _, sessions := range p.sessions {
		tunnels = append(tunnels, sessions...)
	}
	return tunnels
}

func (p *tlsTCPSessionPool) getOrDial(ctx context.Context, key string, class upstream.TrafficClass, dial func(context.Context) (*tlsTCPTunnel, error)) (*tlsTCPTunnel, func(), error) {
	if existing, release := p.reserveExisting(key, class); existing != nil {
		return existing, release, nil
	}

	tunnel, err := dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	stored, release := p.storeOrReserve(key, class, tunnel)
	if stored == nil {
		_ = tunnel.close()
		return nil, nil, errTLSTCPInteractiveAdmissionRejected
	}
	if stored != tunnel {
		_ = tunnel.close()
	}
	return stored, release, nil
}

func (p *tlsTCPSessionPool) reserveExisting(key string, class upstream.TrafficClass) (*tlsTCPTunnel, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sessions := p.activeSessionsLocked(key)
	if len(sessions) == 0 {
		return nil, nil
	}

	acceptable := acceptableTLSTCPTunnels(sessions, class)
	if len(acceptable) > 0 {
		sessions = acceptable
	} else if len(sessions) < tlsTCPMuxSessionsPerKey {
		return nil, nil
	}
	least := leastLoadedTLSTCPTunnel(sessions)
	if len(sessions) < tlsTCPMuxSessionsPerKey && least.tlsTCPLoad() >= tlsTCPMuxTargetStreamsPerSession {
		return nil, nil
	}
	return least, least.reserveStreamSlot()
}

func (p *tlsTCPSessionPool) storeOrReserve(key string, class upstream.TrafficClass, tunnel *tlsTCPTunnel) (*tlsTCPTunnel, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sessions := p.activeSessionsLocked(key)
	if len(sessions) >= tlsTCPMuxSessionsPerKey {
		acceptable := acceptableTLSTCPTunnels(sessions, class)
		if len(acceptable) > 0 {
			sessions = acceptable
		}
		least := leastLoadedTLSTCPTunnel(sessions)
		return least, least.reserveStreamSlot()
	}
	p.sessions[key] = append(sessions, tunnel)
	release := tunnel.reserveStreamSlot()

	go func() {
		<-tunnel.closed
		p.remove(key, tunnel)
	}()

	return tunnel, release
}

func (p *tlsTCPSessionPool) activeSessionsLocked(key string) []*tlsTCPTunnel {
	sessions := p.sessions[key]
	if len(sessions) == 0 {
		return nil
	}
	active := sessions[:0]
	for _, tunnel := range sessions {
		select {
		case <-tunnel.closed:
		default:
			active = append(active, tunnel)
		}
	}
	if len(active) == 0 {
		delete(p.sessions, key)
		return nil
	}
	p.sessions[key] = active
	return active
}

func leastLoadedTLSTCPTunnel(sessions []*tlsTCPTunnel) *tlsTCPTunnel {
	least := sessions[0]
	leastLoad := least.tlsTCPLoad()
	for _, tunnel := range sessions[1:] {
		if load := tunnel.tlsTCPLoad(); load < leastLoad {
			least = tunnel
			leastLoad = load
		}
	}
	return least
}

func acceptableTLSTCPTunnels(sessions []*tlsTCPTunnel, class upstream.TrafficClass) []*tlsTCPTunnel {
	if !isConservativeInteractiveClass(class) {
		return sessions
	}
	acceptable := make([]*tlsTCPTunnel, 0, len(sessions))
	for _, tunnel := range sessions {
		if tunnel.canAcceptTrafficClass(class) {
			acceptable = append(acceptable, tunnel)
		}
	}
	return acceptable
}

func (p *tlsTCPSessionPool) remove(key string, tunnel *tlsTCPTunnel) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sessions := p.sessions[key]
	for i, existing := range sessions {
		if existing != tunnel {
			continue
		}
		copy(sessions[i:], sessions[i+1:])
		sessions[len(sessions)-1] = nil
		sessions = sessions[:len(sessions)-1]
		break
	}
	if len(sessions) == 0 {
		delete(p.sessions, key)
		return
	}
	p.sessions[key] = sessions
}

func dialTLSTCPMux(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, options DialOptions) (net.Conn, error) {
	conn, _, err := dialTLSTCPMuxWithResult(ctx, network, target, chain, provider, options)
	return conn, err
}

func dialTLSTCPMuxWithResult(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, options DialOptions) (net.Conn, DialResult, error) {
	firstHop := chain[0]
	sessionKey, err := tlsTCPSessionPoolKey(firstHop)
	if err != nil {
		return nil, DialResult{}, err
	}
	trafficClass := relayDialTrafficClass(network, options)

	tunnel, release, err := relayTLSTCPSessionPool.getOrDial(ctx, sessionKey, trafficClass, func(dialCtx context.Context) (*tlsTCPTunnel, error) {
		return dialNewTLSTCPTunnel(dialCtx, firstHop, provider)
	})
	if err != nil {
		return nil, DialResult{}, err
	}
	defer release()

	conn, result, err := tunnel.openStream(ctx, relayOpenFrame{
		Kind:        network,
		Target:      target,
		Chain:       append([]Hop(nil), chain[1:]...),
		Metadata:    relayMetadataForDialOptions(network, options),
		InitialData: options.InitialPayload,
	})
	if err != nil {
		return nil, DialResult{SelectedAddress: result.SelectedAddress}, err
	}
	return conn, DialResult{SelectedAddress: result.SelectedAddress}, nil
}

func resolveCandidatesTLSTCPMux(ctx context.Context, target string, chain []Hop, provider TLSMaterialProvider) ([]string, error) {
	firstHop := chain[0]
	sessionKey, err := tlsTCPSessionPoolKey(firstHop)
	if err != nil {
		return nil, err
	}

	tunnel, release, err := relayTLSTCPSessionPool.getOrDial(ctx, sessionKey, upstream.TrafficClassUnknown, func(dialCtx context.Context) (*tlsTCPTunnel, error) {
		return dialNewTLSTCPTunnel(dialCtx, firstHop, provider)
	})
	if err != nil {
		return nil, err
	}
	defer release()

	stream, result, err := tunnel.openStream(ctx, relayOpenFrame{
		Kind:   "resolve",
		Target: target,
		Chain:  append([]Hop(nil), chain[1:]...),
	})
	if err != nil {
		return nil, err
	}
	_ = stream.Close()
	return append([]string(nil), result.ResolvedCandidates...), nil
}

func dialNewTLSTCPTunnel(ctx context.Context, hop Hop, provider TLSMaterialProvider) (*tlsTCPTunnel, error) {
	tlsConfig, err := clientTLSConfig(ctx, provider, hop.Listener, hop.Address, hop.ServerName)
	if err != nil {
		return nil, err
	}

	rawConn, err := dialRelayTCP(ctx, hop.Address)
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

func (t *tlsTCPTunnel) openStream(ctx context.Context, req relayOpenFrame) (net.Conn, muxOpenResult, error) {
	streamID := t.nextStreamID.Add(1)
	stream := &tlsTCPLogicalStream{
		tunnel:       t,
		streamID:     streamID,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan muxOpenResult, 1),
	}
	t.registerStream(stream)

	payload, err := marshalMuxOpenPayload(req)
	if err != nil {
		t.removeStream(streamID)
		return nil, muxOpenResult{}, err
	}
	if err := t.writeFrame(ctx, muxFrame{
		Type:     muxFrameTypeOpen,
		Flags:    muxFlagAckRequired,
		StreamID: streamID,
		Payload:  payload,
	}); err != nil {
		t.removeStream(streamID)
		return nil, muxOpenResult{}, err
	}

	select {
	case result := <-stream.openResultCh:
		if !result.OK {
			t.removeStream(streamID)
			if result.Error == "" {
				return nil, result, &relayApplicationError{message: "relay connection failed"}
			}
			return nil, result, &relayApplicationError{message: fmt.Sprintf("relay connection failed: %s", result.Error)}
		}
		return stream, result, nil
	case <-ctx.Done():
		t.removeStream(streamID)
		return nil, muxOpenResult{}, ctx.Err()
	case <-t.closed:
		t.removeStream(streamID)
		return nil, muxOpenResult{}, io.EOF
	}
}

func (t *tlsTCPTunnel) writeFrame(ctx context.Context, frame muxFrame) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if err := t.refreshWriteDeadlineLocked(); err != nil {
		return err
	}
	// This synchronous path owns frame payload lifetime. Queued writers hand
	// payload release to the write pump via writeRequestBatch/enqueueWriteFrame.
	err := writeMuxFrame(t.writer, frame)
	frame.releasePayload()
	return err
}

func (t *tlsTCPTunnel) refreshWriteDeadlineLocked() error {
	timeout := getRelayFrameTimeout()
	if timeout <= 0 || t.rawConn == nil {
		return nil
	}

	now := time.Now()
	if !t.writeDeadlineNext.IsZero() && now.Before(t.writeDeadlineNext.Add(-(timeout / 4))) {
		return nil
	}

	next := now.Add(timeout)
	if err := t.rawConn.SetWriteDeadline(next); err != nil {
		return err
	}
	t.writeDeadlineNext = next
	return nil
}

func (t *tlsTCPTunnel) startWritePump() {
	t.writePumpOnce.Do(func() {
		t.writeReqCh = make(chan *tlsTCPWriteRequest, tlsTCPWriteQueueDepth)
		go t.writePump()
	})
}

func (t *tlsTCPTunnel) writePump() {
	for {
		select {
		case <-t.closed:
			return
		case req := <-t.writeReqCh:
			if req == nil {
				continue
			}
			batch := []*tlsTCPWriteRequest{req}
		drain:
			for len(batch) < tlsTCPWriteQueueDepth {
				select {
				case next := <-t.writeReqCh:
					if next != nil {
						batch = append(batch, next)
					}
				default:
					break drain
				}
			}

			err := t.writeRequestBatch(batch)
			for _, item := range batch {
				item.done <- err
			}
			if err != nil {
				t.failQueuedWriteRequests(err)
				_ = t.close()
				return
			}
		}
	}
}

func (t *tlsTCPTunnel) writeRequestBatch(batch []*tlsTCPWriteRequest) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if err := t.refreshWriteDeadlineLocked(); err != nil {
		for _, req := range batch {
			t.queuedWrites.Add(-1)
			t.bufferedBytes.Add(-int64(len(req.frame.Payload)))
			req.frame.releasePayload()
		}
		return err
	}
	for i, req := range batch {
		if err := writeMuxFrame(t.writer, req.frame); err != nil {
			t.queuedWrites.Add(-1)
			t.bufferedBytes.Add(-int64(len(req.frame.Payload)))
			req.frame.releasePayload()
			for _, pending := range batch[i+1:] {
				t.queuedWrites.Add(-1)
				t.bufferedBytes.Add(-int64(len(pending.frame.Payload)))
				pending.frame.releasePayload()
			}
			return err
		}
		t.queuedWrites.Add(-1)
		t.bufferedBytes.Add(-int64(len(req.frame.Payload)))
		req.frame.releasePayload()
	}
	return nil
}

func (t *tlsTCPTunnel) failQueuedWriteRequests(err error) {
	if t.writeReqCh == nil {
		return
	}
	for {
		select {
		case req := <-t.writeReqCh:
			if req == nil {
				continue
			}
			t.queuedWrites.Add(-1)
			t.bufferedBytes.Add(-int64(len(req.frame.Payload)))
			req.frame.releasePayload()
			req.done <- err
		default:
			return
		}
	}
}

func (t *tlsTCPTunnel) enqueueWriteFrame(ctx context.Context, frame muxFrame) (*tlsTCPWriteRequest, error) {
	t.startWritePump()

	payloadSize := int64(len(frame.Payload))
	req := &tlsTCPWriteRequest{
		frame: frame,
		done:  make(chan error, 1),
	}
	t.queuedWrites.Add(1)
	t.bufferedBytes.Add(payloadSize)
	select {
	case <-t.closed:
		t.queuedWrites.Add(-1)
		t.bufferedBytes.Add(-payloadSize)
		frame.releasePayload()
		return nil, io.EOF
	case <-ctx.Done():
		t.queuedWrites.Add(-1)
		t.bufferedBytes.Add(-payloadSize)
		frame.releasePayload()
		return nil, ctx.Err()
	case t.writeReqCh <- req:
		return req, nil
	}
}

func waitTLSTCPWriteRequest(ctx context.Context, req *tlsTCPWriteRequest, tunnel *tlsTCPTunnel) error {
	select {
	case err := <-req.done:
		return err
	case <-tunnel.closed:
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}
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
			frame.releasePayload()
			continue
		}

		switch frame.Type {
		case muxFrameTypeOpenResult:
			result, err := readMuxOpenResultPayload(frame.Payload)
			frame.releasePayload()
			if err != nil {
				stream.deliverOpenResult(muxOpenResult{OK: false, Error: err.Error()})
				continue
			}
			stream.deliverOpenResult(result)
		case muxFrameTypeData:
			stream.appendDataChunk(frame.takeReadChunk())
		case muxFrameTypeFin:
			frame.releasePayload()
			stream.setReadError(io.EOF)
			t.removeStream(frame.StreamID)
		case muxFrameTypeRst:
			frame.releasePayload()
			stream.setReadError(io.ErrClosedPipe)
			t.removeStream(frame.StreamID)
		default:
			frame.releasePayload()
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

func (t *tlsTCPTunnel) tlsTCPLoad() int64 {
	return int64(t.logicalStreamCount()) +
		t.pendingStreams.Load() +
		t.queuedWrites.Load() +
		(t.bufferedBytes.Load() / tlsTCPBulkFrameSize)
}

func (t *tlsTCPTunnel) reserveStreamSlot() func() {
	t.pendingStreams.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			t.pendingStreams.Add(-1)
		})
	}
}

func (t *tlsTCPTunnel) canAcceptTrafficClass(class upstream.TrafficClass) bool {
	if !isConservativeInteractiveClass(class) {
		return true
	}
	if t.queuedWrites.Load() >= tlsTCPInteractiveAdmissionQueuedWrites {
		return false
	}
	if t.bufferedBytes.Load() >= tlsTCPInteractiveAdmissionBufferedBytes {
		return false
	}
	return true
}

func isConservativeInteractiveClass(class upstream.TrafficClass) bool {
	return class == upstream.TrafficClassInteractive
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
		stream.deliverOpenResult(muxOpenResult{OK: false, Error: err.Error()})
		stream.setReadError(err)
	}
}

func (s *tlsTCPLogicalStream) Read(p []byte) (int, error) {
	for {
		notifyReadSpace := false
		s.readMu.Lock()
		if len(s.readChunks) > 0 {
			total := 0
			for total < len(p) && len(s.readChunks) > 0 {
				head := &s.readChunks[0]
				if len(head.payload) == 0 {
					head.releaseNow()
					s.readChunks[0] = tlsTCPReadChunk{}
					s.readChunks = s.readChunks[1:]
					continue
				}
				n := copy(p[total:], head.payload)
				total += n
				s.readBufferedBytes -= n
				head.consume(n)
				if len(head.payload) > 0 {
					break
				}
				head.releaseNow()
				s.readChunks[0] = tlsTCPReadChunk{}
				s.readChunks = s.readChunks[1:]
			}
			s.readMu.Unlock()
			if total > 0 {
				notifyReadSpace = true
			}
			if notifyReadSpace {
				s.notifyReadSpace()
			}
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

func (s *tlsTCPLogicalStream) ReadFrom(r io.Reader) (int64, error) {
	s.readMu.Lock()
	writeClosed := s.writeClosed
	s.readMu.Unlock()
	if writeClosed {
		return 0, io.ErrClosedPipe
	}

	buf := tlsTCPBulkBufferPool.Get().([]byte)
	defer tlsTCPBulkBufferPool.Put(buf)

	if s.tunnel.logicalStreamCount() <= 1 {
		return s.readFromSingleStream(r, buf)
	}

	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			// Multi-stream writes stay on the synchronous writeFrame path, so the
			// shared bulk buffer is consumed before the next read reuses it. The
			// single-stream fast path above copies into queued frames instead.
			if frameErr := s.tunnel.writeFrame(context.Background(), muxFrame{
				Type:     muxFrameTypeData,
				StreamID: s.streamID,
				Payload:  buf[:n],
			}); frameErr != nil {
				return total, frameErr
			}
			total += int64(n)
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func (s *tlsTCPLogicalStream) readFromSingleStream(r io.Reader, buf []byte) (int64, error) {
	var total int64
	var inflight []*tlsTCPWriteRequest
	flushOldest := func() error {
		err := waitTLSTCPWriteRequest(context.Background(), inflight[0], s.tunnel)
		inflight = inflight[1:]
		return err
	}

	for {
		n, err := r.Read(buf)
		if n > 0 {
			frame := newQueuedTLSTCPDataFrame(s.streamID, buf[:n])
			req, enqueueErr := s.tunnel.enqueueWriteFrame(context.Background(), frame)
			if enqueueErr != nil {
				return total, enqueueErr
			}
			inflight = append(inflight, req)
			total += int64(n)
			if len(inflight) >= tlsTCPSingleStreamWritePipeline {
				if waitErr := flushOldest(); waitErr != nil {
					return total, waitErr
				}
			}
		}
		if errors.Is(err, io.EOF) {
			for len(inflight) > 0 {
				if waitErr := flushOldest(); waitErr != nil {
					return total, waitErr
				}
			}
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func newQueuedTLSTCPDataFrame(streamID uint32, payload []byte) muxFrame {
	buf := tlsTCPBulkBufferPool.Get().([]byte)
	copy(buf, payload)
	return muxFrame{
		Type:     muxFrameTypeData,
		StreamID: streamID,
		Payload:  buf[:len(payload)],
		payloadRelease: func() {
			tlsTCPBulkBufferPool.Put(buf)
		},
	}
}

func (s *tlsTCPLogicalStream) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for {
		notifyReadSpace := false
		s.readMu.Lock()
		if len(s.readChunks) > 0 {
			head := s.readChunks[0]
			chunk := head.payload
			s.readChunks[0] = tlsTCPReadChunk{}
			s.readChunks = s.readChunks[1:]
			s.readBufferedBytes -= len(chunk)
			s.readMu.Unlock()
			notifyReadSpace = len(chunk) > 0

			n, err := w.Write(chunk)
			total += int64(n)
			if notifyReadSpace {
				s.notifyReadSpace()
			}
			if err != nil {
				if n < len(chunk) {
					head.payload = chunk[n:]
					s.prependReadChunk(head)
				} else {
					head.releaseNow()
				}
				return total, err
			}
			if n != len(chunk) {
				head.payload = chunk[n:]
				s.prependReadChunk(head)
				return total, io.ErrShortWrite
			}
			head.releaseNow()
			continue
		}
		if s.readErrSet {
			err := s.readErr
			s.readMu.Unlock()
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
		s.readMu.Unlock()

		select {
		case <-s.readCh:
		case <-s.tunnel.closed:
			return total, io.EOF
		}
	}
}

func (s *tlsTCPLogicalStream) prependReadChunk(chunk tlsTCPReadChunk) {
	if len(chunk.payload) == 0 {
		chunk.releaseNow()
		return
	}
	s.readMu.Lock()
	s.ensureReadSpaceChLocked()
	s.readChunks = append([]tlsTCPReadChunk{chunk}, s.readChunks...)
	s.readBufferedBytes += len(chunk.payload)
	s.readMu.Unlock()
	s.notifyReadable()
}

func (s *tlsTCPLogicalStream) Close() error {
	_ = s.CloseWrite()
	s.tunnel.removeStream(s.streamID)
	s.discardReadChunks()
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
	s.discardReadChunks()
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
	s.appendDataChunk(tlsTCPReadChunk{payload: payload})
}

func (s *tlsTCPLogicalStream) appendDataChunk(chunk tlsTCPReadChunk) {
	if len(chunk.payload) == 0 {
		chunk.releaseNow()
		return
	}
	blocked := false
	for {
		s.readMu.Lock()
		waitCh := s.ensureReadSpaceChLocked()
		if s.readErrSet {
			s.readMu.Unlock()
			chunk.releaseNow()
			return
		}
		if blocked && tlsTCPMaxBufferedReadBytes > 0 && s.readBufferedBytes > tlsTCPResumeThreshold() {
			s.readMu.Unlock()
			select {
			case <-waitCh:
			case <-s.tunnel.closed:
				chunk.releaseNow()
				return
			}
			continue
		}
		canQueue := tlsTCPMaxBufferedReadBytes <= 0 ||
			s.readBufferedBytes == 0 ||
			s.readBufferedBytes+len(chunk.payload) <= tlsTCPMaxBufferedReadBytes
		if canQueue {
			s.readChunks = append(s.readChunks, chunk)
			s.readBufferedBytes += len(chunk.payload)
			s.readMu.Unlock()
			s.notifyReadable()
			return
		}
		blocked = true
		s.readMu.Unlock()

		select {
		case <-waitCh:
		case <-s.tunnel.closed:
			chunk.releaseNow()
			return
		}
	}
}

func (s *tlsTCPLogicalStream) discardReadChunks() {
	s.readMu.Lock()
	chunks := s.readChunks
	s.readChunks = nil
	s.readBufferedBytes = 0
	s.readMu.Unlock()
	s.notifyReadSpace()

	for i := range chunks {
		chunks[i].releaseNow()
	}
}

func (s *tlsTCPLogicalStream) setReadError(err error) {
	s.readMu.Lock()
	if !s.readErrSet {
		s.readErr = err
		s.readErrSet = true
	}
	s.readMu.Unlock()
	s.notifyReadable()
	s.notifyReadSpace()
}

func (s *tlsTCPLogicalStream) deliverOpenResult(result muxOpenResult) {
	select {
	case s.openResultCh <- result:
	default:
	}
}

func (s *tlsTCPLogicalStream) notifyReadable() {
	select {
	case s.readCh <- struct{}{}:
	default:
	}
}

func (s *tlsTCPLogicalStream) ensureReadSpaceChLocked() chan struct{} {
	if s.readSpaceCh == nil {
		s.readSpaceCh = make(chan struct{}, 1)
	}
	return s.readSpaceCh
}

func tlsTCPResumeThreshold() int {
	if tlsTCPResumeBufferedReadBytes < 0 {
		return 0
	}
	if tlsTCPResumeBufferedReadBytes >= tlsTCPMaxBufferedReadBytes {
		return tlsTCPMaxBufferedReadBytes / 2
	}
	return tlsTCPResumeBufferedReadBytes
}

func (s *tlsTCPLogicalStream) notifyReadSpace() {
	s.readMu.Lock()
	ch := s.ensureReadSpaceChLocked()
	s.readMu.Unlock()
	select {
	case ch <- struct{}{}:
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
			frame.releasePayload()
			if err != nil {
				_ = s.writeOpenResult(frame.StreamID, muxOpenResult{OK: false, Error: err.Error()})
				continue
			}
			if !strings.EqualFold(request.Kind, "tcp") && !strings.EqualFold(request.Kind, "udp") && !strings.EqualFold(request.Kind, "resolve") && !strings.EqualFold(request.Kind, relayOpenKindProbe) {
				_ = s.writeOpenResult(frame.StreamID, muxOpenResult{OK: false, Error: fmt.Sprintf("unsupported network %q", request.Kind)})
				continue
			}

			stream := &tlsTCPLogicalStream{
				tunnel:       s.tunnel,
				streamID:     frame.StreamID,
				readCh:       make(chan struct{}, 1),
				openResultCh: make(chan muxOpenResult, 1),
			}
			s.tunnel.registerStream(stream)
			go s.handleStream(listener, stream, request)
		case muxFrameTypeData:
			if stream := s.tunnel.getStream(frame.StreamID); stream != nil {
				stream.appendDataChunk(frame.takeReadChunk())
			} else {
				frame.releasePayload()
			}
		case muxFrameTypeFin:
			frame.releasePayload()
			if stream := s.tunnel.getStream(frame.StreamID); stream != nil {
				stream.setReadError(io.EOF)
				s.tunnel.removeStream(frame.StreamID)
			}
		case muxFrameTypeRst:
			frame.releasePayload()
			if stream := s.tunnel.getStream(frame.StreamID); stream != nil {
				stream.setReadError(io.ErrClosedPipe)
				s.tunnel.removeStream(frame.StreamID)
			}
		default:
			frame.releasePayload()
		}
	}
}

func (s *serverTLSTCPSession) handleStream(listener Listener, stream *tlsTCPLogicalStream, request relayOpenFrame) {
	options := relayDialOptionsFromMetadata(request.Kind, request.Metadata)
	if strings.EqualFold(request.Kind, relayOpenKindProbe) {
		timings, err := s.server.probeRelayPath(s.server.ctx, relayProbeNetworkFromMetadata(request.Metadata), request.Target, request.Chain)
		if err != nil {
			_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
			s.tunnel.removeStream(stream.streamID)
			return
		}
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: true, ProbeTimings: timings})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	if strings.EqualFold(request.Kind, "resolve") {
		resolvedCandidates, err := s.server.resolveTargetCandidates(request.Target, request.Chain)
		if err != nil {
			_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
			s.tunnel.removeStream(stream.streamID)
			return
		}
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: true, ResolvedCandidates: resolvedCandidates})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	if strings.EqualFold(request.Kind, "udp") {
		s.handleUDPStream(listener, stream, request)
		return
	}

	upstream, upstreamResult, err := s.server.openUpstreamWithResult(request.Kind, request.Target, request.Chain, options)
	if err != nil {
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error(), SelectedAddress: upstreamResult.SelectedAddress})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	s.server.trackConn(upstream)
	defer s.server.untrackConn(upstream)
	defer upstream.Close()

	if len(request.InitialData) > 0 {
		if _, err := upstream.Write(request.InitialData); err != nil {
			_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
			s.tunnel.removeStream(stream.streamID)
			return
		}
	}
	if err := s.writeOpenResult(stream.streamID, muxOpenResult{
		OK:              true,
		SelectedAddress: upstreamResult.SelectedAddress,
	}); err != nil {
		s.tunnel.removeStream(stream.streamID)
		return
	}

	pipeBothWays(wrapIdleConn(stream), wrapIdleConn(upstream))
	s.tunnel.removeStream(stream.streamID)
}

func (s *serverTLSTCPSession) handleUDPStream(listener Listener, stream *tlsTCPLogicalStream, request relayOpenFrame) {
	upstream, selectedAddress, err := s.server.openUDPPeerWithResultOptions(
		request.Target,
		request.Chain,
		relayDialOptionsFromMetadata(request.Kind, request.Metadata),
	)
	if err != nil {
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	defer upstream.Close()

	if err := s.writeOpenResult(stream.streamID, muxOpenResult{OK: true, SelectedAddress: selectedAddress}); err != nil {
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
