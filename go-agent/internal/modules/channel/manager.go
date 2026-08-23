package channel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

// Manager owns the reverse channel sessions of one agent. Sessions are applied
// idempotently by the control plane and survive until teardown or Close.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

type session struct {
	spec    SessionSpec
	cancel  context.CancelFunc
	runtime sessionRuntime
}

type sessionRuntime interface {
	status() SessionStatus
}

// NewManager creates a channel manager. Credentials are required: channel
// peers always authenticate with agent-identity tunnel PKI credentials.
func NewManager(cfg Config) (*Manager, error) {
	cfg = cfg.withDefaults()
	if strings.TrimSpace(cfg.AgentID) == "" {
		return nil, errors.New("channel manager agent id is required")
	}
	if cfg.Credentials == nil {
		return nil, errors.New("channel manager tunnel credential provider is required")
	}
	return &Manager{cfg: cfg, sessions: make(map[string]*session)}, nil
}

// Ensure applies a session spec idempotently: an identical live session is
// kept and its current status returned. For the exit role Ensure performs one
// bounded connect attempt before returning so the caller observes the initial
// connectivity state; reconnect continues in the background.
func (m *Manager) Ensure(ctx context.Context, spec SessionSpec) (SessionStatus, error) {
	if m == nil {
		return SessionStatus{}, errors.New("channel manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := spec.validate(m.cfg.AgentID); err != nil {
		return SessionStatus{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionStatus{}, errors.New("channel manager is closed")
	}
	if existing, ok := m.sessions[spec.SessionID]; ok {
		if existing.spec.comparableEqual(spec) {
			runtime := existing.runtime
			m.mu.Unlock()
			return runtime.status(), nil
		}
		existing.cancel()
		delete(m.sessions, spec.SessionID)
	}
	m.mu.Unlock()

	sessionCtx, cancel := context.WithCancel(context.Background())
	var runtime sessionRuntime
	var waitFirst <-chan SessionStatus
	var err error
	switch spec.Role {
	case RoleEntry:
		runtime, err = startEntryRuntime(sessionCtx, m.cfg, spec)
	case RoleExit:
		var exit *exitRuntime
		exit, err = startExitRuntime(sessionCtx, m.cfg, spec)
		if err == nil {
			runtime = exit
			waitFirst = exit.first
		}
	}
	if err != nil {
		cancel()
		return SessionStatus{}, err
	}
	current := &session{spec: spec.comparable(), cancel: cancel, runtime: runtime}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return SessionStatus{}, errors.New("channel manager is closed")
	}
	if existing, ok := m.sessions[spec.SessionID]; ok {
		// A concurrent Ensure for the same session raced past the existence
		// check: exactly one runtime may own the slot, so discard this one
		// instead of overwriting (and leaking) the winner.
		if existing.spec.comparableEqual(spec) {
			m.mu.Unlock()
			cancel()
			return existing.runtime.status(), nil
		}
		existing.cancel()
	}
	m.sessions[spec.SessionID] = current
	m.mu.Unlock()

	if waitFirst != nil {
		waitCtx, waitCancel := context.WithTimeout(ctx, m.cfg.ConnectTimeout+5*time.Second)
		defer waitCancel()
		select {
		case status := <-waitFirst:
			return status, nil
		case <-waitCtx.Done():
			return runtime.status(), nil
		}
	}
	return runtime.status(), nil
}

// Teardown releases one session. It is idempotent.
func (m *Manager) Teardown(_ context.Context, sessionID string) error {
	if m == nil {
		return errors.New("channel manager is unavailable")
	}
	if err := validateSessionID(sessionID); err != nil {
		return err
	}
	m.mu.Lock()
	existing, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()
	if ok {
		existing.cancel()
	}
	return nil
}

// Status reports the live state of one session. A canceled or expired caller
// context fails the lookup instead of returning a guessed online snapshot.
func (m *Manager) Status(ctx context.Context, sessionID string) (SessionStatus, error) {
	if m == nil {
		return SessionStatus{}, errors.New("channel manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SessionStatus{}, err
	}
	if err := validateSessionID(sessionID); err != nil {
		return SessionStatus{}, err
	}
	m.mu.Lock()
	existing, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SessionStatus{}, err
	}
	if !ok {
		return SessionStatus{SessionID: sessionID, State: StateOffline, LastError: "session is not tracked"}, nil
	}
	return existing.runtime.status(), nil
}

// Close tears down every session.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	sessions := make([]*session, 0, len(m.sessions))
	for id, current := range m.sessions {
		sessions = append(sessions, current)
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	for _, current := range sessions {
		current.cancel()
	}
	return nil
}

func (spec SessionSpec) comparableEqual(other SessionSpec) bool {
	return spec.comparable().equal(other.comparable())
}

func (spec SessionSpec) equal(other SessionSpec) bool {
	if spec.SessionID != other.SessionID || spec.Role != other.Role || spec.Protocol != other.Protocol ||
		spec.EntryAgentID != other.EntryAgentID || spec.ExitAgentID != other.ExitAgentID ||
		spec.ListenHost != other.ListenHost || spec.BridgeHost != other.BridgeHost ||
		spec.DialAddress != other.DialAddress || spec.BackendAddress != other.BackendAddress ||
		len(spec.RelayChain) != len(other.RelayChain) {
		return false
	}
	for index := range spec.RelayChain {
		if spec.RelayChain[index].Address != other.RelayChain[index].Address ||
			spec.RelayChain[index].ServerName != other.RelayChain[index].ServerName ||
			spec.RelayChain[index].Listener.ID != other.RelayChain[index].Listener.ID {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// entry role
// ---------------------------------------------------------------------------

type entryRuntime struct {
	spec   SessionSpec
	cfg    Config
	cancel context.CancelFunc
	wg     sync.WaitGroup

	ingress net.Listener
	bridge  net.Listener
	udp     *net.UDPConn

	mu        sync.Mutex
	active    *mux
	lastError string
}

func startEntryRuntime(ctx context.Context, cfg Config, spec SessionSpec) (*entryRuntime, error) {
	tlsConfig, err := relay.AgentTunnelServerTLSConfig(ctx, cfg.Credentials, spec.ExitAgentID)
	if err != nil {
		return nil, fmt.Errorf("channel entry tunnel tls: %w", err)
	}
	runtimeCtx, cancel := context.WithCancel(ctx)
	runtime := &entryRuntime{spec: spec, cfg: cfg, cancel: cancel}

	ingress, err := net.Listen("tcp", joinHostPort(normalizeListenHost(spec.ListenHost), spec.ListenPort))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("channel entry ingress listen: %w", err)
	}
	runtime.ingress = ingress

	if spec.Protocol == ProtocolUDP {
		address, err := net.ResolveUDPAddr("udp", joinHostPort(normalizeBridgeHost(spec.BridgeHost), spec.BridgePort))
		if err != nil {
			cancel()
			_ = ingress.Close()
			return nil, fmt.Errorf("channel entry bridge address: %w", err)
		}
		socket, err := net.ListenUDP("udp", address)
		if err != nil {
			cancel()
			_ = ingress.Close()
			return nil, fmt.Errorf("channel entry bridge listen: %w", err)
		}
		runtime.udp = socket
	} else {
		bridge, err := net.Listen("tcp", joinHostPort(normalizeBridgeHost(spec.BridgeHost), spec.BridgePort))
		if err != nil {
			cancel()
			_ = ingress.Close()
			return nil, fmt.Errorf("channel entry bridge listen: %w", err)
		}
		runtime.bridge = bridge
	}

	runtime.wg.Add(1)
	go func() {
		defer runtime.wg.Done()
		runtime.acceptIngress(runtimeCtx, tlsConfig)
	}()
	if runtime.bridge != nil {
		runtime.wg.Add(1)
		go func() {
			defer runtime.wg.Done()
			runtime.acceptBridge(runtimeCtx)
		}()
	}
	if runtime.udp != nil {
		runtime.wg.Add(1)
		go func() {
			defer runtime.wg.Done()
			runtime.serveBridgeUDP(runtimeCtx)
		}()
	}
	go func() {
		<-runtimeCtx.Done()
		runtime.close()
	}()
	return runtime, nil
}

func (r *entryRuntime) close() {
	r.cancel()
	if r.ingress != nil {
		_ = r.ingress.Close()
	}
	if r.bridge != nil {
		_ = r.bridge.Close()
	}
	if r.udp != nil {
		_ = r.udp.Close()
	}
	r.mu.Lock()
	active := r.active
	r.mu.Unlock()
	if active != nil {
		_ = active.Close()
	}
}

func (r *entryRuntime) status() SessionStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := SessionStatus{
		SessionID:      r.spec.SessionID,
		Role:           RoleEntry,
		State:          StateOffline,
		IngressAddress: r.ingress.Addr().String(),
		LastError:      r.lastError,
	}
	if r.bridge != nil {
		status.BridgeAddress = r.bridge.Addr().String()
	}
	if r.udp != nil {
		status.BridgeAddress = r.udp.LocalAddr().String()
	}
	if r.active != nil {
		status.State = StateOnline
		status.LastError = ""
	}
	return status
}

func (r *entryRuntime) acceptIngress(ctx context.Context, tlsConfig *tls.Config) {
	for {
		conn, err := r.ingress.Accept()
		if err != nil {
			if ctx.Err() == nil {
				r.setError("ingress accept: " + err.Error())
			}
			return
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.serveIngressConn(ctx, tlsConfig, conn)
		}()
	}
}

func (r *entryRuntime) serveIngressConn(ctx context.Context, tlsConfig *tls.Config, conn net.Conn) {
	tlsConn := tls.Server(conn, tlsConfig)
	handshakeCtx, cancel := context.WithTimeout(ctx, r.cfg.ConnectTimeout)
	err := tlsConn.HandshakeContext(handshakeCtx)
	cancel()
	if err != nil {
		_ = tlsConn.Close()
		return
	}
	_ = tlsConn.SetDeadline(time.Time{})
	muxConn := newMuxOpener(tlsConn)
	r.mu.Lock()
	previous := r.active
	r.active = muxConn
	r.lastError = ""
	r.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	select {
	case <-muxConn.Done():
	case <-ctx.Done():
	}
	r.mu.Lock()
	if r.active == muxConn {
		r.active = nil
		if err := muxConn.Err(); err != nil && !errors.Is(err, errMuxClosed) && ctx.Err() == nil {
			r.lastError = "channel closed: " + err.Error()
		}
	}
	r.mu.Unlock()
	_ = muxConn.Close()
}

func (r *entryRuntime) setError(message string) {
	r.mu.Lock()
	r.lastError = message
	r.mu.Unlock()
}

func (r *entryRuntime) currentMux() *mux {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func (r *entryRuntime) acceptBridge(ctx context.Context) {
	for {
		conn, err := r.bridge.Accept()
		if err != nil {
			if ctx.Err() == nil {
				r.setError("bridge accept: " + err.Error())
			}
			return
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.serveBridgeConn(ctx, conn)
		}()
	}
}

func (r *entryRuntime) serveBridgeConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	muxConn := r.currentMux()
	if muxConn == nil {
		return
	}
	stream, err := muxConn.OpenStream(ctx, false)
	if err != nil {
		return
	}
	defer stream.Close()
	pipeStream(stream, conn)
}

// pipeStream copies between a mux stream and a raw connection. A clean EOF on
// one side is propagated as a half-close so queued and in-flight tail bytes
// are still delivered to the peer; both endpoints are fully closed only after
// both directions ended. An abnormal end resets both endpoints to unblock the
// other direction.
func pipeStream(stream *muxStream, conn net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if copyConnToStream(stream, conn) {
			// The raw peer finished sending: forward its FIN so the stream
			// peer can still drain everything already in flight.
			_ = stream.CloseWrite()
		} else {
			abortStreamPair(stream, conn)
		}
	}()
	go func() {
		defer wg.Done()
		if copyStreamToConn(stream, conn) {
			// The stream peer finished sending: half-close the raw peer.
			closeConnWrite(conn)
		} else {
			abortStreamPair(stream, conn)
		}
	}()
	wg.Wait()
	_ = conn.Close()
	_ = stream.Close()
}

// abortStreamPair unblocks both directions after an abnormal end.
func abortStreamPair(stream *muxStream, conn net.Conn) {
	_ = conn.Close()
	_ = stream.Close()
}

// copyConnToStream pumps the raw connection into the mux stream and reports
// whether it ended with a clean EOF (io.EOF).
func copyConnToStream(stream *muxStream, conn net.Conn) bool {
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if _, writeErr := stream.Write(buf[:n]); writeErr != nil {
				return false
			}
		}
		if err != nil {
			return errors.Is(err, io.EOF)
		}
	}
}

// copyStreamToConn pumps the mux stream into the raw connection and reports
// whether it ended with a clean EOF (io.EOF).
func copyStreamToConn(stream *muxStream, conn net.Conn) bool {
	buf := make([]byte, 32*1024)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if _, writeErr := conn.Write(buf[:n]); writeErr != nil {
				return false
			}
		}
		if err != nil {
			return errors.Is(err, io.EOF)
		}
	}
}

// closeConnWrite half-closes a raw connection when it supports it.
func closeConnWrite(conn net.Conn) {
	type closeWriter interface{ CloseWrite() error }
	if writer, ok := conn.(closeWriter); ok {
		_ = writer.CloseWrite()
	}
}

// ---------------------------------------------------------------------------
// entry role UDP bridge
// ---------------------------------------------------------------------------

type udpAssociation struct {
	stream   *muxStream
	peer     *net.UDPAddr
	lastSeen time.Time
}

func (r *entryRuntime) serveBridgeUDP(ctx context.Context) {
	associations := make(map[string]*udpAssociation)
	sweepInterval := r.cfg.UDPIdleTimeout / 2
	_ = r.udp.SetReadDeadline(time.Now().Add(sweepInterval))
	buf := make([]byte, muxMaxPayload)
	for {
		n, peer, err := r.udp.ReadFromUDP(buf)
		now := time.Now()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() && ctx.Err() == nil {
				for key, association := range associations {
					if now.Sub(association.lastSeen) > r.cfg.UDPIdleTimeout {
						_ = association.stream.Close()
						delete(associations, key)
					}
				}
				_ = r.udp.SetReadDeadline(time.Now().Add(sweepInterval))
				continue
			}
			return
		}
		payload := append([]byte(nil), buf[:n]...)
		key := peer.String()
		association, ok := associations[key]
		if !ok {
			muxConn := r.currentMux()
			if muxConn == nil {
				continue
			}
			stream, openErr := muxConn.OpenStream(ctx, true)
			if openErr != nil {
				continue
			}
			association = &udpAssociation{stream: stream, peer: peer}
			associations[key] = association
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				r.pumpUDPAssociation(association)
			}()
		}
		association.lastSeen = now
		if _, err := association.stream.Write(payload); err != nil {
			_ = association.stream.Close()
			delete(associations, key)
		}
	}
}

func (r *entryRuntime) pumpUDPAssociation(association *udpAssociation) {
	buf := make([]byte, muxMaxPayload)
	for {
		n, err := association.stream.Read(buf)
		if n > 0 {
			if _, writeErr := r.udp.WriteToUDP(buf[:n], association.peer); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// exit role
// ---------------------------------------------------------------------------

type exitRuntime struct {
	spec SessionSpec
	cfg  Config

	cancel context.CancelFunc
	wg     sync.WaitGroup

	first  chan SessionStatus
	mu     sync.Mutex
	online bool
	last   string
}

func startExitRuntime(ctx context.Context, cfg Config, spec SessionSpec) (*exitRuntime, error) {
	runtimeCtx, cancel := context.WithCancel(ctx)
	runtime := &exitRuntime{
		spec:   spec,
		cfg:    cfg,
		cancel: cancel,
		first:  make(chan SessionStatus, 1),
	}
	runtime.wg.Add(1)
	go func() {
		defer runtime.wg.Done()
		runtime.run(runtimeCtx)
	}()
	return runtime, nil
}

func (r *exitRuntime) status() SessionStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := StateOffline
	lastError := r.last
	if r.online {
		state = StateOnline
		lastError = ""
	}
	return SessionStatus{
		SessionID: r.spec.SessionID,
		Role:      RoleExit,
		State:     state,
		LastError: lastError,
	}
}

func (r *exitRuntime) setState(online bool, lastError string) {
	r.mu.Lock()
	r.online = online
	r.last = lastError
	r.mu.Unlock()
}

func (r *exitRuntime) reportFirst() {
	select {
	case r.first <- r.status():
	default:
	}
}

func (r *exitRuntime) run(ctx context.Context) {
	defer r.cancel()
	backoff := r.cfg.BackoffBase
	for {
		if ctx.Err() != nil {
			return
		}
		established, err := r.connectOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if established {
			backoff = r.cfg.BackoffBase
		}
		if err != nil {
			r.setState(false, err.Error())
		} else {
			r.setState(false, "channel closed")
		}
		r.reportFirst()
		timer := time.NewTimer(backoffWithJitter(backoff))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff *= 2
		if backoff > r.cfg.BackoffLimit {
			backoff = r.cfg.BackoffLimit
		}
	}
}

func backoffWithJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	jitter := int64(base) / 5
	if jitter <= 0 {
		return base
	}
	return base + time.Duration(rand.Int64N(2*jitter)) - time.Duration(jitter)
}

// connectOnce dials and serves one channel generation. It returns established
// = true once the tunnel handshake completed; a later drop is reported through
// the returned error and triggers a fast reconnect.
func (r *exitRuntime) connectOnce(ctx context.Context) (bool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, r.cfg.ConnectTimeout)
	conn, err := r.dial(dialCtx)
	cancel()
	if err != nil {
		return false, err
	}
	tlsConfig, err := relay.AgentTunnelClientTLSConfig(ctx, r.cfg.Credentials, r.spec.EntryAgentID)
	if err != nil {
		_ = conn.Close()
		return false, fmt.Errorf("channel exit tunnel tls: %w", err)
	}
	tlsConn := tls.Client(conn, tlsConfig)
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, r.cfg.ConnectTimeout)
	err = tlsConn.HandshakeContext(handshakeCtx)
	cancelHandshake()
	if err != nil {
		_ = tlsConn.Close()
		return false, fmt.Errorf("channel exit handshake: %w", err)
	}
	_ = tlsConn.SetDeadline(time.Time{})
	r.setState(true, "")
	r.reportFirst()
	defer r.setState(false, "")

	muxConn := newMuxAcceptor(tlsConn)
	defer muxConn.Close()
	serveCtx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	go func() {
		select {
		case <-muxConn.Done():
			cancelServe()
		case <-serveCtx.Done():
			_ = muxConn.Close()
		}
	}()
	for {
		stream, acceptErr := muxConn.AcceptStream()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return true, nil
			}
			return true, fmt.Errorf("channel mux accept: %w", muxConn.Err())
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.serveStream(serveCtx, stream)
		}()
	}
}

func (r *exitRuntime) dial(ctx context.Context) (net.Conn, error) {
	if len(r.spec.RelayChain) == 0 {
		dialer := &net.Dialer{}
		return dialer.DialContext(ctx, "tcp", r.spec.DialAddress)
	}
	conn, err := relay.Dial(ctx, "tcp", r.spec.DialAddress, r.spec.RelayChain, tunnelOnlyMaterialProvider{tunnel: r.cfg.Credentials})
	if err != nil {
		return nil, fmt.Errorf("channel exit relay dial: %w", err)
	}
	return conn, nil
}

func (r *exitRuntime) serveStream(ctx context.Context, stream *muxStream) {
	if stream.protocol == muxProtocolUDP {
		r.serveUDPStream(ctx, stream)
		return
	}
	dialer := &net.Dialer{Timeout: r.cfg.ConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", r.spec.BackendAddress)
	if err != nil {
		stream.rejectOpen("backend dial failed: " + err.Error())
		return
	}
	defer conn.Close()
	if err := stream.acceptOpen(); err != nil {
		return
	}
	defer stream.Close()
	pipeStream(stream, conn)
}

func (r *exitRuntime) serveUDPStream(ctx context.Context, stream *muxStream) {
	backend, err := net.ResolveUDPAddr("udp", r.spec.BackendAddress)
	if err != nil {
		stream.rejectOpen("backend address is invalid: " + err.Error())
		return
	}
	socket, err := net.DialUDP("udp", nil, backend)
	if err != nil {
		stream.rejectOpen("backend dial failed: " + err.Error())
		return
	}
	if err := stream.acceptOpen(); err != nil {
		_ = socket.Close()
		return
	}
	defer stream.Close()
	defer socket.Close()

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, muxMaxPayload)
		for {
			n, readErr := stream.Read(buf)
			if n > 0 {
				if _, writeErr := socket.Write(buf[:n]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, muxMaxPayload)
		for {
			_ = socket.SetReadDeadline(time.Now().Add(r.cfg.UDPIdleTimeout))
			n, readErr := socket.Read(buf)
			if n > 0 {
				if _, writeErr := stream.Write(buf[:n]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()
	<-done
	_ = socket.Close()
	<-done
}

// tunnelOnlyMaterialProvider adapts the tunnel PKI facade to the relay
// TLSMaterialProvider contract. Channel relay hops authenticate with the host
// tunnel PKI, so legacy managed-certificate material is never consulted.
type tunnelOnlyMaterialProvider struct {
	tunnel relay.TunnelCredentialProvider
}

func (p tunnelOnlyMaterialProvider) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, errors.New("managed certificate material is unavailable for channel relay hops")
}

func (p tunnelOnlyMaterialProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, errors.New("managed certificate material is unavailable for channel relay hops")
}

func (p tunnelOnlyMaterialProvider) InstallTunnelCertificate(ctx context.Context, storageIdentity string, config *tls.Config) (relay.TunnelCredentialMetadata, error) {
	return p.tunnel.InstallTunnelCertificate(ctx, storageIdentity, config)
}

func (p tunnelOnlyMaterialProvider) LoadTunnelCredential(ctx context.Context, storageIdentity string) (relay.TunnelCredentialMetadata, error) {
	return p.tunnel.LoadTunnelCredential(ctx, storageIdentity)
}

func (p tunnelOnlyMaterialProvider) LoadTunnelSecurity(ctx context.Context) (relay.TunnelSecurityState, error) {
	return p.tunnel.LoadTunnelSecurity(ctx)
}
