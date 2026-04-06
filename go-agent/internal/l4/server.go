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
)

type Server struct {
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	tcpListeners []net.Listener
	udpConns     []*net.UDPConn
}

func NewServer(ctx context.Context, rules []model.L4Rule) (*Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &Server{ctx: ctx, cancel: cancel}
	for _, rule := range rules {
		if err := ValidateRule(rule); err != nil {
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

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleTCPConnection(c, rule)
		}(conn)
	}
}

func (s *Server) handleTCPConnection(client net.Conn, rule model.L4Rule) {
	defer client.Close()

	upstream, err := net.Dial("tcp", net.JoinHostPort(rule.UpstreamHost, strconv.Itoa(rule.UpstreamPort)))
	if err != nil {
		return
	}
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

func (s *Server) startUDPListener(rule model.L4Rule) error {
	addr := &net.UDPAddr{
		IP:   net.ParseIP(rule.ListenHost),
		Port: rule.ListenPort,
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

	for _, ln := range s.tcpListeners {
		ln.Close()
	}
	for _, conn := range s.udpConns {
		conn.Close()
	}
	s.wg.Wait()
	return nil
}
