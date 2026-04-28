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
	req := []byte{0x05, 0x01, 0x00}
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
