package l4

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type RelayMaterialProvider interface {
	relay.TLSMaterialProvider
}

type Server struct {
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	tcpListeners []net.Listener
	udpConns     []*net.UDPConn

	relayListenersByID map[int]model.RelayListener
	relayProvider      RelayMaterialProvider

	tcpMu    sync.Mutex
	tcpConns map[net.Conn]struct{}
	closing  bool
}

func NewServer(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}
	s := &Server{
		ctx:                ctx,
		cancel:             cancel,
		tcpConns:           make(map[net.Conn]struct{}),
		udpConns:           nil,
		tcpListeners:       nil,
		relayListenersByID: relayListenersByID,
		relayProvider:      relayProvider,
	}
	for _, rule := range rules {
		if err := ValidateRule(rule); err != nil {
			s.Close()
			return nil, err
		}
		if err := s.validateRelayChain(rule); err != nil {
			s.Close()
			return nil, err
		}

		switch strings.ToLower(rule.Protocol) {
		case "tcp":
			if err := s.startTCPListener(rule); err != nil {
				s.Close()
				return nil, err
			}
		case "udp":
			if err := s.startUDPListener(rule); err != nil {
				s.Close()
				return nil, err
			}
		default:
			s.Close()
			return nil, fmt.Errorf("unsupported protocol %q", rule.Protocol)
		}
	}
	return s, nil
}

func (s *Server) startTCPListener(rule model.L4Rule) error {
	addr := net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.tcpListeners = append(s.tcpListeners, ln)

	s.wg.Add(1)
	go s.tcpAcceptLoop(ln, rule)
	return nil
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

	upstream, err := s.dialTCPUpstream(rule)
	if err != nil {
		return
	}
	s.trackTCPConn(upstream)
	defer s.untrackTCPConn(upstream)
	defer upstream.Close()

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(upstream, client)
		upstream.Close()
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, upstream)
		client.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (s *Server) dialTCPUpstream(rule model.L4Rule) (net.Conn, error) {
	target := net.JoinHostPort(rule.UpstreamHost, strconv.Itoa(rule.UpstreamPort))
	if len(rule.RelayChain) == 0 {
		return (&net.Dialer{}).DialContext(s.ctx, "tcp", target)
	}
	hops, err := s.resolveRelayHops(rule)
	if err != nil {
		return nil, err
	}
	return relay.Dial(s.ctx, "tcp", target, hops, s.relayProvider)
}

func (s *Server) startUDPListener(rule model.L4Rule) error {
	addrStr := net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	addr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	s.udpConns = append(s.udpConns, conn)

	s.wg.Add(1)
	go s.udpReadLoop(conn, rule)
	return nil
}

func (s *Server) udpReadLoop(conn *net.UDPConn, rule model.L4Rule) {
	defer s.wg.Done()
	upstreamAddr := net.JoinHostPort(rule.UpstreamHost, strconv.Itoa(rule.UpstreamPort))
	buf := make([]byte, 64*1024)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			return
		}
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if s.ctx.Err() != nil {
					return
				}
				continue
			}
			return
		}

		packet := append([]byte(nil), buf[:n]...)
		s.wg.Add(1)
		go func(payload []byte, peerAddr *net.UDPAddr) {
			defer s.wg.Done()
			s.proxyUDPPacket(conn, upstreamAddr, payload, peerAddr)
		}(packet, peer)
	}
}

func (s *Server) proxyUDPPacket(listener *net.UDPConn, upstreamAddr string, payload []byte, peer *net.UDPAddr) {
	upstream, err := net.Dial("udp", upstreamAddr)
	if err != nil {
		return
	}
	defer upstream.Close()

	upstream.SetDeadline(time.Now().Add(time.Second))
	if _, err := upstream.Write(payload); err != nil {
		return
	}

	reply := make([]byte, 64*1024)
	n, err := upstream.Read(reply)
	if err != nil {
		return
	}
	listener.WriteToUDP(reply[:n], peer)
}

func (s *Server) Close() error {
	if s.cancel != nil {
		s.cancel()
	}

	s.tcpMu.Lock()
	s.closing = true
	s.tcpMu.Unlock()

	for _, ln := range s.tcpListeners {
		ln.Close()
	}
	s.closeTCPConns()
	for _, conn := range s.udpConns {
		conn.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *Server) trackTCPConn(conn net.Conn) {
	if conn == nil {
		return
	}
	s.tcpMu.Lock()
	if s.tcpConns == nil {
		s.tcpConns = make(map[net.Conn]struct{})
	}
	closing := s.closing
	if !closing {
		s.tcpConns[conn] = struct{}{}
	}
	s.tcpMu.Unlock()

	if closing {
		conn.Close()
	}
}

func (s *Server) untrackTCPConn(conn net.Conn) {
	if conn == nil {
		return
	}
	s.tcpMu.Lock()
	defer s.tcpMu.Unlock()
	delete(s.tcpConns, conn)
}

func (s *Server) closeTCPConns() {
	s.tcpMu.Lock()
	conns := s.tcpConns
	s.tcpConns = nil
	s.tcpMu.Unlock()

	for conn := range conns {
		conn.Close()
	}
}

func (s *Server) validateRelayChain(rule model.L4Rule) error {
	if len(rule.RelayChain) == 0 {
		return nil
	}
	if s.relayProvider == nil {
		return fmt.Errorf("l4 rule %s:%d requires relay tls material provider", rule.ListenHost, rule.ListenPort)
	}
	_, err := s.resolveRelayHops(rule)
	return err
}

func (s *Server) resolveRelayHops(rule model.L4Rule) ([]relay.Hop, error) {
	hops := make([]relay.Hop, 0, len(rule.RelayChain))
	for _, listenerID := range rule.RelayChain {
		listener, ok := s.relayListenersByID[listenerID]
		if !ok {
			return nil, fmt.Errorf("relay listener %d not found", listenerID)
		}
		if !listener.Enabled {
			return nil, fmt.Errorf("relay listener %d is disabled", listenerID)
		}
		if err := relay.ValidateListener(listener); err != nil {
			return nil, fmt.Errorf("relay listener %d: %w", listenerID, err)
		}
		hops = append(hops, relay.Hop{
			Address:  net.JoinHostPort(listener.ListenHost, strconv.Itoa(listener.ListenPort)),
			Listener: listener,
		})
	}
	return hops, nil
}
