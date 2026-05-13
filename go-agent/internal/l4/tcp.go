package l4

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/stream"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

func (s *Server) startTCPListener(rule model.L4Rule) error {
	addr := l4ListenAddress(rule)
	ln, err := s.listenTCP(rule, addr)
	if err != nil {
		return err
	}
	s.tcpListeners = append(s.tcpListeners, ln)

	s.wg.Add(1)
	go s.tcpAcceptLoop(ln, rule)
	return nil
}

func (s *Server) listenTCP(rule model.L4Rule, addr string) (net.Listener, error) {
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") {
		runtime, err := s.wireGuardRuntime(rule)
		if err != nil {
			return nil, err
		}
		return runtime.ListenTCP(s.ctx, addr)
	}
	return net.Listen("tcp", addr)
}

func (s *Server) tcpAcceptLoop(ln net.Listener, rule model.L4Rule) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			continue
		}

		s.trackTCPConn(conn)
		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleTCPConnection(c, rule)
		}(conn)
	}
}

func (s *Server) handleTCPConnection(client net.Conn, rule model.L4Rule) {
	defer s.untrackTCPConn(client)
	defer client.Close()

	if state := s.currentTrafficBlockState(); state.Blocked {
		return
	}

	recorder := traffic.NewL4RuleRecorder(rule.ID)
	if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "proxy") {
		s.handleProxyEntryConnection(client, rule, recorder)
		return
	}

	downstreamSource, downstreamProxyInfo, err := s.prepareTCPDownstream(client, rule)
	if err != nil {
		return
	}

	var initialPayload []byte
	if ruleUsesRelay(rule) && !rule.Tuning.ProxyProtocol.Send {
		initialPayload, downstreamSource, err = s.prefetchRelayInitialPayload(client, downstreamSource)
		if err != nil {
			return
		}
	}

	upstream, candidate, connectDuration, err := s.dialTCPUpstream(rule, relay.DialOptions{
		InitialPayload: initialPayload,
		TrafficClass:   relayTCPDialTrafficClass(initialPayload),
	})
	if err != nil {
		return
	}
	s.trackTCPConn(upstream)
	defer s.untrackTCPConn(upstream)
	defer upstream.Close()

	if err := s.writeTCPProxyHeader(upstream, client, downstreamProxyInfo, rule); err != nil {
		s.observeCandidateFailure(candidate)
		return
	}
	s.observeCandidateSuccess(candidate, connectDuration)

	done := make(chan struct{}, 2)
	go func() {
		if len(initialPayload) > 0 {
			recorder.Add(int64(len(initialPayload)), 0)
			recorder.FlushIfPendingBelow(32 * 1024)
		}
		_, _ = copyL4TCP(upstream, downstreamSource, true, recorder)
		closeTCPWrite(upstream)
		closeTCPRead(client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = copyL4TCP(client, upstream, false, recorder)
		closeTCPWrite(client)
		closeTCPRead(upstream)
		done <- struct{}{}
	}()
	<-done
	<-done
	recorder.Flush()
}

func (s *Server) handleProxyEntryConnection(client net.Conn, rule model.L4Rule, recorder *traffic.Recorder) {
	auth := proxyproto.EntryAuth{
		Enabled:  rule.ProxyEntryAuth.Enabled,
		Username: rule.ProxyEntryAuth.Username,
		Password: rule.ProxyEntryAuth.Password,
	}
	req, err := proxyproto.ReadClientRequest(s.ctx, client, auth)
	if err != nil {
		return
	}
	upstream, err := s.dialProxyEntryUpstream(rule, req.Target)
	if err != nil {
		_ = proxyproto.WriteClientRequestFailure(client, req, http.StatusBadGateway)
		return
	}
	s.trackTCPConn(upstream)
	defer s.untrackTCPConn(upstream)
	defer upstream.Close()
	if err := proxyproto.WriteClientRequestSuccess(client, req); err != nil {
		return
	}
	if len(req.InitialPayload) > 0 {
		if _, err := upstream.Write(req.InitialPayload); err != nil {
			return
		}
		recorder = l4RecorderOrAggregate(recorder)
		recorder.Add(int64(len(req.InitialPayload)), 0)
		recorder.FlushIfPendingBelow(32 * 1024)
	}

	copyBidirectionalTCP(client, upstream, recorder)
}

func (s *Server) dialProxyEntryUpstream(rule model.L4Rule, target string) (net.Conn, error) {
	switch strings.ToLower(strings.TrimSpace(rule.ProxyEgressMode)) {
	case "relay":
		return s.dialRelayPath("tcp", target, rule, relay.DialOptions{})
	case "wireguard":
		runtime, err := s.wireGuardRuntime(rule)
		if err != nil {
			return nil, err
		}
		return runtime.DialContext(s.ctx, "tcp", target)
	case "proxy":
		return proxyproto.Dial(s.ctx, rule.ProxyEgressURL, target)
	default:
		return nil, fmt.Errorf("unsupported proxy_egress_mode %q", rule.ProxyEgressMode)
	}
}

func copyBidirectionalTCP(a net.Conn, b net.Conn, recorder *traffic.Recorder) {
	recorder = l4RecorderOrAggregate(recorder)

	done := make(chan struct{}, 2)
	go func() {
		_, _ = copyL4TCP(b, a, true, recorder)
		closeTCPWrite(b)
		closeTCPRead(a)
		done <- struct{}{}
	}()
	go func() {
		_, _ = copyL4TCP(a, b, false, recorder)
		closeTCPWrite(a)
		closeTCPRead(b)
		done <- struct{}{}
	}()
	<-done
	<-done
	recorder.Flush()
}

func l4RecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder {
	if recorder != nil {
		return recorder
	}
	return traffic.NewL4Recorder()
}

func copyL4TCP(dst io.Writer, src io.Reader, rxDirection bool, recorder *traffic.Recorder) (int64, error) {
	direction := stream.DirectionTX
	if rxDirection {
		direction = stream.DirectionRX
	}
	wrapped := stream.NewTrafficWriterFlushBelow(dst, direction, l4RecorderOrAggregate(recorder), 32*1024)
	return copyPreferReaderFrom(wrapped, src)
}

func (s *Server) prefetchRelayInitialPayload(_ net.Conn, source io.Reader) ([]byte, io.Reader, error) {
	if source == nil {
		return nil, source, nil
	}
	if buffered, ok := source.(*bufio.Reader); ok && buffered.Buffered() > 0 {
		limit := buffered.Buffered()
		if limit > relayInitialPayloadMax {
			limit = relayInitialPayloadMax
		}
		buf := make([]byte, limit)
		n, err := io.ReadFull(buffered, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return nil, source, err
		}
		return buf[:n], source, nil
	}

	// Do not stall relay dials waiting for client bytes. Only buffered downstream
	// data is folded into OPEN; raw connections fall back to normal relay copy.
	return nil, source, nil
}

func relayTCPDialTrafficClass(initialPayload []byte) upstream.TrafficClass {
	if len(initialPayload) == 0 {
		return upstream.TrafficClassUnknown
	}
	if len(initialPayload) >= relayInitialPayloadMax {
		return upstream.TrafficClassBulk
	}
	return upstream.ClassifyL4("tcp", int64(len(initialPayload)), 0)
}

func (s *Server) prepareTCPDownstream(client net.Conn, rule model.L4Rule) (io.Reader, *proxyInfo, error) {
	if !rule.Tuning.ProxyProtocol.Decode {
		return client, nil, nil
	}

	reader := bufio.NewReader(client)
	info, _, err := parseProxyHeader(reader)
	if err != nil {
		return nil, nil, err
	}
	return reader, info, nil
}

func (s *Server) writeTCPProxyHeader(upstream net.Conn, client net.Conn, decoded *proxyInfo, rule model.L4Rule) error {
	if !rule.Tuning.ProxyProtocol.Send {
		return nil
	}

	info := decoded
	if info == nil {
		source, destination, err := proxyInfoFromConn(client)
		if err != nil {
			return err
		}
		info = &proxyInfo{
			Source:      source,
			Destination: destination,
			Version:     1,
		}
	}

	header, err := buildProxyHeader(*info)
	if err != nil {
		return err
	}
	_, err = upstream.Write(header)
	return err
}

func proxyInfoFromConn(conn net.Conn) (*net.TCPAddr, *net.TCPAddr, error) {
	source, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported downstream source address type %T", conn.RemoteAddr())
	}
	destination, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported downstream destination address type %T", conn.LocalAddr())
	}
	return cloneTCPAddr(source), cloneTCPAddr(destination), nil
}

func cloneTCPAddr(addr *net.TCPAddr) *net.TCPAddr {
	if addr == nil {
		return nil
	}
	out := *addr
	if addr.IP != nil {
		out.IP = append(net.IP(nil), addr.IP...)
	}
	return &out
}

func closeTCPWrite(conn net.Conn) {
	if conn == nil {
		return
	}
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
		return
	}
	_ = conn.Close()
}

func closeTCPRead(conn net.Conn) {
	if conn == nil {
		return
	}
	if closer, ok := conn.(interface{ CloseRead() error }); ok {
		_ = closer.CloseRead()
	}
}
