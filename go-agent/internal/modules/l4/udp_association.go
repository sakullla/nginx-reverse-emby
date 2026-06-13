package l4

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type udpProxyAssociation struct {
	udpRuleID     int
	clientIP      string
	listenAddr    string
	requestedHost string
	requestedPort int
	peerIP        string
	peerPort      int
	locked        bool
	refCount      int
}

func udpAssociationKey(parts ...string) string {
	return strings.Join(parts, "|")
}

func (s *Server) registerProxyUDPAssociation(client net.Conn, rule model.L4Rule, req model.ClientRequest, bindAddr net.Addr) (func(), error) {
	if client == nil {
		return func() {}, nil
	}
	remoteAddr, ok := client.RemoteAddr().(*net.TCPAddr)
	if !ok || remoteAddr.IP == nil {
		return func() {}, nil
	}
	listenAddr := udpAssociationListenScope(bindAddr)
	association := udpProxyAssociation{
		udpRuleID:     rule.ID,
		clientIP:      remoteAddr.IP.String(),
		listenAddr:    listenAddr,
		requestedHost: strings.TrimSpace(req.Host),
		requestedPort: req.Port,
	}
	if req.Port != 0 {
		peerIP := association.clientIP
		locked := true
		if ip := net.ParseIP(association.requestedHost); ip != nil && !ip.IsUnspecified() {
			peerIP = ip.String()
		} else if ip != nil && ip.IsUnspecified() {
			peerIP = association.clientIP
		} else if association.requestedHost != "" && net.ParseIP(association.requestedHost) == nil {
			return func() {}, fmt.Errorf("domain-form SOCKS5 UDP association source hints with port are not supported")
		}
		association.peerIP = peerIP
		association.peerPort = req.Port
		association.locked = locked
	}
	key := udpAssociationStorageKey(association, remoteAddr.Port)

	s.udpMu.Lock()
	if s.udpAssociations == nil {
		s.udpAssociations = make(map[string]udpProxyAssociation)
	}
	if existing, ok := s.udpAssociations[key]; ok {
		existing.refCount++
		s.udpAssociations[key] = existing
	} else {
		association.refCount = 1
		s.udpAssociations[key] = association
	}
	s.udpMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.udpMu.Lock()
			if existing, ok := s.udpAssociations[key]; ok {
				existing.refCount--
				if existing.refCount <= 0 {
					delete(s.udpAssociations, key)
				} else {
					s.udpAssociations[key] = existing
				}
			}
			s.udpMu.Unlock()
		})
	}, nil
}

func udpAssociationStorageKey(association udpProxyAssociation, controlPort int) string {
	base := []string{strconv.Itoa(association.udpRuleID), association.listenAddr}
	if association.requestedPort != 0 {
		return udpAssociationKey(append(base, association.peerIP, strconv.Itoa(association.peerPort))...)
	}
	return udpAssociationKey(append(base, "pending", association.clientIP, strconv.Itoa(controlPort))...)
}

func udpAssociationListenScope(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

func (s *Server) hasProxyUDPAssociation(peer *net.UDPAddr, listener net.Addr) bool {
	if peer == nil || peer.IP == nil {
		return false
	}
	peerIP := peer.IP.String()
	listenAddr := udpAssociationListenScope(listener)
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	for _, association := range s.udpAssociations {
		if association.listenAddr != listenAddr {
			continue
		}
		if association.locked {
			if association.peerIP == peerIP && association.peerPort == peer.Port {
				return true
			}
			continue
		}
		if association.requestedPort != 0 && association.clientIP == peerIP {
			return true
		}
	}
	for key, association := range s.udpAssociations {
		if association.listenAddr != listenAddr || association.requestedPort != 0 || association.clientIP != peerIP || association.peerPort != 0 {
			continue
		}
		association.peerIP = peerIP
		association.peerPort = peer.Port
		association.locked = true
		s.udpAssociations[key] = association
		return true
	}
	return false
}

func (s *Server) proxyUDPBindAddr(client net.Conn, rule model.L4Rule) net.Addr {
	var bind *net.UDPAddr
	s.udpMu.Lock()
	for _, conn := range s.udpConns {
		addr, ok := conn.LocalAddr().(*net.UDPAddr)
		if !ok || addr == nil {
			continue
		}
		if rule.ListenPort != 0 && addr.Port != rule.ListenPort {
			continue
		}
		if host := strings.TrimSpace(rule.ListenHost); host != "" {
			want := net.ParseIP(host)
			if want != nil && !want.IsUnspecified() && !addr.IP.Equal(want) {
				continue
			}
		}
		bind = cloneUDPAddr(addr)
		break
	}
	s.udpMu.Unlock()
	if bind == nil {
		addr, err := net.ResolveUDPAddr("udp", l4ListenAddress(rule))
		if err != nil {
			return nil
		}
		bind = addr
	}
	if bind.IP == nil || bind.IP.IsUnspecified() {
		if local, ok := client.LocalAddr().(*net.TCPAddr); ok && local.IP != nil && !local.IP.IsUnspecified() {
			bind.IP = append(net.IP(nil), local.IP...)
		}
	}
	return bind
}

func (s *Server) proxyUDPAssociationListenAddr(rule model.L4Rule, fallback net.Addr) net.Addr {
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	for _, conn := range s.udpConns {
		addr, ok := conn.LocalAddr().(*net.UDPAddr)
		if !ok || addr == nil {
			continue
		}
		if rule.ListenPort != 0 && addr.Port != rule.ListenPort {
			continue
		}
		if host := strings.TrimSpace(rule.ListenHost); host != "" {
			want := net.ParseIP(host)
			if want != nil && !want.IsUnspecified() && !addr.IP.Equal(want) {
				continue
			}
		}
		return cloneUDPAddr(addr)
	}
	return fallback
}
