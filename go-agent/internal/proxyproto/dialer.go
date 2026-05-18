package proxyproto

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

func Dial(ctx context.Context, proxyURL string, target string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := ParseProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	host, port, err := splitTarget(target)
	if err != nil {
		return nil, err
	}
	if _, err := newClientRequest("tcp", host, port); err != nil {
		return nil, err
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, err
	}
	if err := handshakeProxy(ctx, conn, cfg, host, port, target); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

type UDPAssociation struct {
	control   net.Conn
	packet    *net.UDPConn
	relay     *net.UDPAddr
	remoteDNS bool
}

func DialUDP(ctx context.Context, proxyURL string) (*UDPAssociation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := ParseProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	if cfg.HTTPConnect || cfg.SOCKSVersion != 5 {
		return nil, fmt.Errorf("UDP proxy egress requires a SOCKS5-family proxy")
	}
	packet, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}

	var dialer net.Dialer
	control, err := dialer.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		_ = packet.Close()
		return nil, err
	}
	resetDeadline := applyContextDeadline(ctx, control)
	defer resetDeadline()
	if err := handshakeSOCKS5UDPAssociate(control, cfg, packet.LocalAddr().(*net.UDPAddr)); err != nil {
		_ = packet.Close()
		_ = control.Close()
		return nil, err
	}
	bindAddr, err := readSOCKS5ReplyBindAddress(control)
	if err != nil {
		_ = packet.Close()
		_ = control.Close()
		return nil, err
	}
	bindAddr, err = socks5UDPRelayAddr(bindAddr, control.RemoteAddr())
	if err != nil {
		_ = packet.Close()
		_ = control.Close()
		return nil, err
	}
	return &UDPAssociation{
		control:   control,
		packet:    packet,
		relay:     bindAddr,
		remoteDNS: cfg.RemoteDNS,
	}, nil
}

func (a *UDPAssociation) Close() error {
	if a == nil {
		return nil
	}
	var err error
	if a.packet != nil {
		err = a.packet.Close()
	}
	if a.control != nil {
		if closeErr := a.control.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func (a *UDPAssociation) SetReadDeadline(t time.Time) error {
	if a == nil || a.packet == nil {
		return nil
	}
	return a.packet.SetReadDeadline(t)
}

func (a *UDPAssociation) SetWriteDeadline(t time.Time) error {
	if a == nil || a.packet == nil {
		return nil
	}
	return a.packet.SetWriteDeadline(t)
}

func (a *UDPAssociation) ReadPacket() (string, []byte, error) {
	if a == nil || a.packet == nil {
		return "", nil, fmt.Errorf("SOCKS5 UDP association is closed")
	}
	buf := make([]byte, 64*1024)
	for {
		n, addr, err := a.packet.ReadFromUDP(buf)
		if err != nil {
			return "", nil, err
		}
		if a.relay != nil && addr != nil && (!addr.IP.Equal(a.relay.IP) || addr.Port != a.relay.Port) {
			continue
		}
		packet, err := ParseSOCKS5UDPPacket(buf[:n])
		if err != nil {
			return "", nil, err
		}
		return packet.Target, packet.Payload, nil
	}
}

func (a *UDPAssociation) WritePacket(target string, payload []byte) error {
	if a == nil || a.packet == nil {
		return fmt.Errorf("SOCKS5 UDP association is closed")
	}
	packet, err := buildSOCKS5UDPPacketForProxy(target, payload, a.remoteDNS)
	if err != nil {
		return err
	}
	_, err = a.packet.WriteToUDP(packet, a.relay)
	return err
}

func handshakeProxy(ctx context.Context, conn net.Conn, cfg ProxyURL, host string, port int, target string) error {
	resetDeadline := applyContextDeadline(ctx, conn)
	defer resetDeadline()

	if cfg.HTTPConnect {
		return handshakeHTTPConnect(conn, cfg, target)
	}
	switch cfg.SOCKSVersion {
	case 4:
		return handshakeSOCKS4(conn, cfg, host, port)
	case 5:
		return handshakeSOCKS5(conn, cfg, host, port)
	default:
		return fmt.Errorf("unsupported SOCKS version %d", cfg.SOCKSVersion)
	}
}

func handshakeHTTPConnect(conn net.Conn, cfg ProxyURL, target string) error {
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target); err != nil {
		return err
	}
	if cfg.Username != "" || cfg.Password != "" {
		token := base64.StdEncoding.EncodeToString([]byte(cfg.Username + ":" + cfg.Password))
		if _, err := fmt.Fprintf(conn, "Proxy-Authorization: Basic %s\r\n", token); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(conn, "\r\n"); err != nil {
		return err
	}

	var first [1]byte
	if _, err := io.ReadFull(conn, first[:]); err != nil {
		return err
	}
	header, err := readHTTPHeader(first[0], conn)
	if err != nil {
		return err
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(header)), nil)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP proxy CONNECT failed: %s", resp.Status)
	}
	return nil
}

func handshakeSOCKS5UDPAssociate(conn net.Conn, cfg ProxyURL, localAddr *net.UDPAddr) error {
	method := byte(0x00)
	if cfg.Username != "" || cfg.Password != "" {
		method = 0x02
	}
	if _, err := conn.Write([]byte{0x05, 0x01, method}); err != nil {
		return err
	}
	var selection [2]byte
	if _, err := io.ReadFull(conn, selection[:]); err != nil {
		return err
	}
	if selection[0] != 0x05 {
		return fmt.Errorf("invalid SOCKS5 method response version %d", selection[0])
	}
	if selection[1] == 0xff {
		return fmt.Errorf("SOCKS5 proxy rejected authentication methods")
	}
	if selection[1] != method {
		return fmt.Errorf("SOCKS5 proxy selected unexpected method %d", selection[1])
	}
	if method == 0x02 {
		if err := writeSOCKS5PasswordAuth(conn, cfg.Username, cfg.Password); err != nil {
			return err
		}
	}
	host := "0.0.0.0"
	port := 1
	if localAddr != nil {
		port = localAddr.Port
		if localAddr.IP != nil && !localAddr.IP.IsUnspecified() {
			host = localAddr.IP.String()
		}
	}
	req, err := socks5Request(0x03, host, port)
	if err != nil {
		return err
	}
	if _, err := conn.Write(req); err != nil {
		return err
	}
	return nil
}

func handshakeSOCKS5(conn net.Conn, cfg ProxyURL, host string, port int) error {
	method := byte(0x00)
	if cfg.Username != "" || cfg.Password != "" {
		method = 0x02
	}
	if _, err := conn.Write([]byte{0x05, 0x01, method}); err != nil {
		return err
	}
	var selection [2]byte
	if _, err := io.ReadFull(conn, selection[:]); err != nil {
		return err
	}
	if selection[0] != 0x05 {
		return fmt.Errorf("invalid SOCKS5 method response version %d", selection[0])
	}
	if selection[1] == 0xff {
		return fmt.Errorf("SOCKS5 proxy rejected authentication methods")
	}
	if selection[1] != method {
		return fmt.Errorf("SOCKS5 proxy selected unexpected method %d", selection[1])
	}
	if method == 0x02 {
		if err := writeSOCKS5PasswordAuth(conn, cfg.Username, cfg.Password); err != nil {
			return err
		}
	}

	connectHost := host
	if !cfg.RemoteDNS {
		resolvedHost, err := resolveLocalIP(host)
		if err != nil {
			return err
		}
		connectHost = resolvedHost
	}
	req, err := socks5ConnectRequest(connectHost, port)
	if err != nil {
		return err
	}
	if _, err := conn.Write(req); err != nil {
		return err
	}
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply[0] != 0x05 {
		return fmt.Errorf("invalid SOCKS5 reply version %d", reply[0])
	}
	if reply[1] != 0x00 {
		return fmt.Errorf("SOCKS5 CONNECT failed with status %d", reply[1])
	}
	return drainSOCKS5BindAddress(conn, reply[3])
}

func handshakeSOCKS4(conn net.Conn, cfg ProxyURL, host string, port int) error {
	connectHost := host
	if !cfg.RemoteDNS {
		resolvedHost, err := resolveLocalIP(host)
		if err != nil {
			return err
		}
		connectHost = resolvedHost
	}
	ip := net.ParseIP(connectHost).To4()
	useDomain := cfg.RemoteDNS && ip == nil
	if ip == nil && !useDomain {
		return fmt.Errorf("SOCKS4 requires an IPv4 target")
	}
	if useDomain {
		ip = net.IPv4(0, 0, 0, 1)
	}

	req := []byte{0x04, 0x01, byte(port >> 8), byte(port), ip[0], ip[1], ip[2], ip[3]}
	req = append(req, []byte(cfg.Username)...)
	req = append(req, 0)
	if useDomain {
		req = append(req, []byte(connectHost)...)
		req = append(req, 0)
	}
	if _, err := conn.Write(req); err != nil {
		return err
	}
	var reply [8]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply[1] != 0x5a {
		return fmt.Errorf("SOCKS4 CONNECT failed with status %d", reply[1])
	}
	return nil
}

func resolveLocalIP(host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("resolve proxy target locally: %w", err)
	}
	for _, ip := range ips {
		if ipv4 := ip.To4(); ipv4 != nil {
			return ipv4.String(), nil
		}
	}
	for _, ip := range ips {
		if ip16 := ip.To16(); ip16 != nil {
			return ip16.String(), nil
		}
	}
	return "", fmt.Errorf("resolve proxy target locally: no IPs for %s", host)
}

func writeSOCKS5PasswordAuth(conn net.Conn, username string, password string) error {
	if len(username) > 255 || len(password) > 255 {
		return fmt.Errorf("SOCKS5 username/password must be at most 255 bytes")
	}
	req := []byte{0x01, byte(len(username))}
	req = append(req, []byte(username)...)
	req = append(req, byte(len(password)))
	req = append(req, []byte(password)...)
	if _, err := conn.Write(req); err != nil {
		return err
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply[0] != 0x01 {
		return fmt.Errorf("invalid SOCKS5 auth response version %d", reply[0])
	}
	if reply[1] != 0x00 {
		return fmt.Errorf("SOCKS5 authentication failed")
	}
	return nil
}

func socks5ConnectRequest(host string, port int) ([]byte, error) {
	return socks5Request(0x01, host, port)
}

func socks5Request(command byte, host string, port int) ([]byte, error) {
	req := []byte{0x05, command, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			req = append(req, 0x01)
			req = append(req, ipv4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, fmt.Errorf("SOCKS5 domain target is too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, []byte(host)...)
	}
	req = append(req, byte(port>>8), byte(port))
	return req, nil
}

func buildSOCKS5UDPPacketForProxy(target string, payload []byte, remoteDNS bool) ([]byte, error) {
	host, port, err := splitTarget(target)
	if err != nil {
		return nil, err
	}
	if !remoteDNS {
		resolvedHost, err := resolveLocalIP(host)
		if err != nil {
			return nil, err
		}
		host = resolvedHost
	}
	return BuildSOCKS5UDPPacket(net.JoinHostPort(host, fmt.Sprintf("%d", port)), payload)
}

func socks5UDPRelayAddr(bindAddr *net.UDPAddr, controlRemoteAddr net.Addr) (*net.UDPAddr, error) {
	if bindAddr == nil {
		return nil, fmt.Errorf("SOCKS5 UDP relay address is missing")
	}
	if bindAddr.IP != nil && !bindAddr.IP.IsUnspecified() {
		return bindAddr, nil
	}
	if controlRemoteAddr == nil {
		return nil, fmt.Errorf("SOCKS5 UDP relay host is unspecified and control peer is missing")
	}
	host, _, err := net.SplitHostPort(controlRemoteAddr.String())
	if err != nil {
		return nil, fmt.Errorf("parse SOCKS5 control peer %q: %w", controlRemoteAddr.String(), err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("parse SOCKS5 control peer host %q", host)
	}
	return &net.UDPAddr{IP: ip, Port: bindAddr.Port, Zone: bindAddr.Zone}, nil
}

func readSOCKS5ReplyBindAddress(conn net.Conn) (*net.UDPAddr, error) {
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return nil, err
	}
	if reply[0] != 0x05 {
		return nil, fmt.Errorf("invalid SOCKS5 reply version %d", reply[0])
	}
	if reply[1] != 0x00 {
		return nil, fmt.Errorf("SOCKS5 UDP ASSOCIATE failed with status %d", reply[1])
	}
	host, err := readSOCKS5Host(conn, reply[3])
	if err != nil {
		return nil, err
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(conn, portBytes[:]); err != nil {
		return nil, err
	}
	return net.ResolveUDPAddr("udp", net.JoinHostPort(host, fmt.Sprintf("%d", int(portBytes[0])<<8|int(portBytes[1]))))
}

func drainSOCKS5BindAddress(conn net.Conn, atyp byte) error {
	var toRead int
	switch atyp {
	case 0x01:
		toRead = net.IPv4len + 2
	case 0x03:
		n, err := readByte(conn)
		if err != nil {
			return err
		}
		toRead = int(n) + 2
	case 0x04:
		toRead = net.IPv6len + 2
	default:
		return fmt.Errorf("unsupported SOCKS5 bind address type %d", atyp)
	}
	buf := make([]byte, toRead)
	_, err := io.ReadFull(conn, buf)
	return err
}
