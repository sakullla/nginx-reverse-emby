package proxyproto

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type EntryAuth struct {
	Enabled  bool
	Username string
	Password string
}

type ClientRequest struct {
	Protocol       string
	Target         string
	Host           string
	Port           int
	InitialPayload []byte
}

type SOCKS5UDPPacket struct {
	Target  string
	Payload []byte
}

func ReadClientRequest(ctx context.Context, conn net.Conn, auth EntryAuth) (ClientRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ClientRequest{}, err
	}
	resetDeadline := applyContextDeadline(ctx, conn)
	defer resetDeadline()

	var first [1]byte
	if _, err := io.ReadFull(conn, first[:]); err != nil {
		return ClientRequest{}, err
	}

	switch first[0] {
	case 0x04:
		if auth.Enabled {
			writeSOCKS4Reply(conn, false, 0, nil)
			return ClientRequest{}, fmt.Errorf("SOCKS4 does not support proxy entry authentication")
		}
		return readSOCKS4Request(conn, conn)
	case 0x05:
		return readSOCKS5Request(conn, conn, auth)
	default:
		if isHTTPProxyRequestStart(first[0]) {
			return readHTTPProxyRequest(first[0], conn, auth)
		}
		return ClientRequest{}, fmt.Errorf("unsupported proxy entry protocol 0x%02x", first[0])
	}
}

func readSOCKS4Request(reader io.Reader, conn net.Conn) (ClientRequest, error) {
	var header [7]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		writeSOCKS4Reply(conn, false, 0, nil)
		return ClientRequest{}, err
	}
	cmd := header[0]
	port := int(header[1])<<8 | int(header[2])
	ip := net.IPv4(header[3], header[4], header[5], header[6])
	if _, err := readNullString(reader); err != nil {
		writeSOCKS4Reply(conn, false, port, ip.To4())
		return ClientRequest{}, err
	}
	if cmd != 0x01 {
		writeSOCKS4Reply(conn, false, port, ip.To4())
		return ClientRequest{}, fmt.Errorf("unsupported SOCKS4 command %d", cmd)
	}

	host := ip.String()
	protocol := "socks4"
	if header[3] == 0 && header[4] == 0 && header[5] == 0 && header[6] != 0 {
		domain, err := readNullString(reader)
		if err != nil {
			writeSOCKS4Reply(conn, false, port, ip.To4())
			return ClientRequest{}, err
		}
		host = domain
		protocol = "socks4a"
	}
	req, err := newClientRequest(protocol, host, port)
	if err != nil {
		writeSOCKS4Reply(conn, false, port, ip.To4())
		return ClientRequest{}, err
	}
	return req, nil
}

func readSOCKS5Request(reader io.Reader, conn net.Conn, auth EntryAuth) (ClientRequest, error) {
	methods, err := readSOCKS5Methods(reader)
	if err != nil {
		return ClientRequest{}, err
	}
	method := byte(0x00)
	if auth.Enabled {
		method = 0x02
	}
	if !hasMethod(methods, method) {
		_, _ = conn.Write([]byte{0x05, 0xff})
		return ClientRequest{}, fmt.Errorf("SOCKS5 method %d not offered", method)
	}
	if _, err := conn.Write([]byte{0x05, method}); err != nil {
		return ClientRequest{}, err
	}
	if method == 0x02 {
		if err := readSOCKS5PasswordAuth(reader, conn, auth); err != nil {
			return ClientRequest{}, err
		}
	}

	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		writeSOCKS5Reply(conn, 0x01)
		return ClientRequest{}, err
	}
	if header[0] != 0x05 {
		writeSOCKS5Reply(conn, 0x01)
		return ClientRequest{}, fmt.Errorf("invalid SOCKS5 request version %d", header[0])
	}
	switch header[1] {
	case 0x01, 0x03:
		host, err := readSOCKS5Host(reader, header[3])
		if err != nil {
			writeSOCKS5Reply(conn, 0x08)
			return ClientRequest{}, err
		}
		var portBytes [2]byte
		if _, err := io.ReadFull(reader, portBytes[:]); err != nil {
			writeSOCKS5Reply(conn, 0x01)
			return ClientRequest{}, err
		}
		port := int(portBytes[0])<<8 | int(portBytes[1])
		protocol := "socks5"
		if header[1] == 0x03 {
			protocol = "socks5-udp"
		}
		req, err := newClientRequest(protocol, host, port)
		if err != nil {
			writeSOCKS5Reply(conn, 0x01)
			return ClientRequest{}, err
		}
		return req, nil
	default:
		writeSOCKS5Reply(conn, 0x07)
		return ClientRequest{}, fmt.Errorf("unsupported SOCKS5 command %d", header[1])
	}
}

func readHTTPProxyRequest(first byte, conn net.Conn, auth EntryAuth) (ClientRequest, error) {
	header, err := readHTTPHeader(first, conn)
	if err != nil {
		writeHTTPProxyError(conn, http.StatusBadRequest)
		return ClientRequest{}, err
	}
	reader := bufio.NewReader(bytes.NewReader(header))
	req, err := http.ReadRequest(reader)
	if err != nil {
		writeHTTPProxyError(conn, http.StatusBadRequest)
		return ClientRequest{}, err
	}
	defer req.Body.Close()
	if auth.Enabled && !validHTTPBasicAuth(req.Header.Get("Proxy-Authorization"), auth) {
		_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"proxy\"\r\nContent-Length: 0\r\n\r\n")
		return ClientRequest{}, fmt.Errorf("HTTP proxy authentication failed")
	}
	if req.Method == http.MethodConnect {
		target := strings.TrimSpace(req.Host)
		if target == "" && req.URL != nil {
			target = strings.TrimSpace(req.URL.Host)
		}
		if target == "" {
			target = strings.TrimSpace(req.RequestURI)
		}
		host, port, err := splitTarget(target)
		if err != nil {
			writeHTTPProxyError(conn, http.StatusBadRequest)
			return ClientRequest{}, err
		}
		return newClientRequest("http", host, port)
	}

	target := ""
	if req.URL != nil {
		target = strings.TrimSpace(req.URL.Host)
	}
	if target == "" {
		target = strings.TrimSpace(req.Host)
	}
	target = withDefaultHTTPPort(target, req.URL)
	host, port, err := splitTarget(target)
	if err != nil {
		writeHTTPProxyError(conn, http.StatusBadRequest)
		return ClientRequest{}, err
	}
	clientReq, err := newClientRequest("http_forward", host, port)
	if err != nil {
		writeHTTPProxyError(conn, http.StatusBadRequest)
		return ClientRequest{}, err
	}
	initial, err := buildHTTPForwardInitialPayload(req)
	if err != nil {
		writeHTTPProxyError(conn, http.StatusBadRequest)
		return ClientRequest{}, err
	}
	clientReq.InitialPayload = initial
	return clientReq, nil
}

func WriteClientRequestSuccess(conn net.Conn, req ClientRequest) error {
	switch req.Protocol {
	case "http":
		_, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		return err
	case "http_forward":
		return nil
	case "socks4", "socks4a":
		writeSOCKS4Reply(conn, true, req.Port, net.ParseIP(req.Host))
		return nil
	case "socks5", "socks5-udp":
		writeSOCKS5Reply(conn, 0x00)
		return nil
	default:
		return nil
	}
}

func WriteClientRequestFailure(conn net.Conn, req ClientRequest, status int) error {
	switch req.Protocol {
	case "http", "http_forward":
		if status <= 0 {
			status = http.StatusBadGateway
		}
		_, err := fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\n\r\n", status, http.StatusText(status))
		return err
	case "socks4", "socks4a":
		writeSOCKS4Reply(conn, false, req.Port, net.ParseIP(req.Host))
		return nil
	case "socks5", "socks5-udp":
		writeSOCKS5Reply(conn, 0x01)
		return nil
	default:
		return nil
	}
}

func ParseSOCKS5UDPPacket(buf []byte) (SOCKS5UDPPacket, error) {
	if len(buf) < 4 {
		return SOCKS5UDPPacket{}, fmt.Errorf("SOCKS5 UDP packet too short")
	}
	if buf[0] != 0x00 || buf[1] != 0x00 {
		return SOCKS5UDPPacket{}, fmt.Errorf("SOCKS5 UDP packet has invalid reserved field")
	}
	if buf[2] != 0x00 {
		return SOCKS5UDPPacket{}, fmt.Errorf("SOCKS5 UDP fragmentation is not supported")
	}

	reader := bytes.NewReader(buf[4:])
	host, err := readSOCKS5Host(reader, buf[3])
	if err != nil {
		return SOCKS5UDPPacket{}, err
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(reader, portBytes[:]); err != nil {
		return SOCKS5UDPPacket{}, err
	}
	port := int(portBytes[0])<<8 | int(portBytes[1])
	req, err := newClientRequest("socks5-udp", host, port)
	if err != nil {
		return SOCKS5UDPPacket{}, err
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return SOCKS5UDPPacket{}, err
	}
	return SOCKS5UDPPacket{
		Target:  req.Target,
		Payload: append([]byte(nil), payload...),
	}, nil
}

func BuildSOCKS5UDPPacket(target string, payload []byte) ([]byte, error) {
	host, port, err := splitTarget(target)
	if err != nil {
		return nil, err
	}

	packet := []byte{0x00, 0x00, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			packet = append(packet, 0x01)
			packet = append(packet, ipv4...)
		} else {
			packet = append(packet, 0x04)
			packet = append(packet, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("SOCKS5 domain target is invalid")
		}
		packet = append(packet, 0x03, byte(len(host)))
		packet = append(packet, []byte(host)...)
	}
	packet = append(packet, byte(port>>8), byte(port))
	packet = append(packet, payload...)
	return packet, nil
}

func isHTTPProxyRequestStart(first byte) bool {
	switch first {
	case 'C', 'D', 'G', 'H', 'O', 'P', 'T':
		return true
	default:
		return false
	}
}

func withDefaultHTTPPort(target string, u *url.URL) string {
	if target == "" {
		return target
	}
	if _, _, err := net.SplitHostPort(target); err == nil {
		return target
	}
	scheme := ""
	if u != nil {
		scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	}
	host := strings.TrimSpace(target)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	switch scheme {
	case "https":
		return net.JoinHostPort(host, "443")
	default:
		return net.JoinHostPort(host, "80")
	}
}

func buildHTTPForwardInitialPayload(req *http.Request) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("missing HTTP request")
	}
	uri := "/"
	if req.URL != nil && req.URL.RequestURI() != "" {
		uri = req.URL.RequestURI()
	}
	proto := req.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	host := strings.TrimSpace(req.Host)
	if host == "" && req.URL != nil {
		host = strings.TrimSpace(req.URL.Host)
	}
	var payload bytes.Buffer
	if _, err := fmt.Fprintf(&payload, "%s %s %s\r\n", req.Method, uri, proto); err != nil {
		return nil, err
	}
	if host != "" {
		if _, err := fmt.Fprintf(&payload, "Host: %s\r\n", host); err != nil {
			return nil, err
		}
	}
	for key, values := range req.Header {
		if skipForwardProxyHeader(key) {
			continue
		}
		for _, value := range values {
			if _, err := fmt.Fprintf(&payload, "%s: %s\r\n", key, value); err != nil {
				return nil, err
			}
		}
	}
	payload.WriteString("\r\n")
	return payload.Bytes(), nil
}

func skipForwardProxyHeader(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "proxy-authorization", "proxy-authenticate", "proxy-connection":
		return true
	default:
		return false
	}
}

func readHTTPHeader(first byte, reader io.Reader) ([]byte, error) {
	const maxHTTPHeaderBytes = 64 * 1024
	header := []byte{first}
	var next [1]byte
	for len(header) < maxHTTPHeaderBytes {
		if _, err := io.ReadFull(reader, next[:]); err != nil {
			return nil, err
		}
		header = append(header, next[0])
		if bytes.HasSuffix(header, []byte("\r\n\r\n")) {
			return header, nil
		}
	}
	return nil, fmt.Errorf("HTTP proxy request header too large")
}

func readSOCKS5Methods(reader io.Reader) ([]byte, error) {
	n, err := readByte(reader)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("SOCKS5 method list is empty")
	}
	methods := make([]byte, int(n))
	_, err = io.ReadFull(reader, methods)
	return methods, err
}

func readSOCKS5PasswordAuth(reader io.Reader, conn net.Conn, auth EntryAuth) error {
	version, err := readByte(reader)
	if err != nil {
		return err
	}
	if version != 0x01 {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return fmt.Errorf("invalid SOCKS5 auth version %d", version)
	}
	username, err := readSOCKS5AuthField(reader)
	if err != nil {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return err
	}
	password, err := readSOCKS5AuthField(reader)
	if err != nil {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return err
	}
	if username != auth.Username || password != auth.Password {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return fmt.Errorf("SOCKS5 authentication failed")
	}
	_, err = conn.Write([]byte{0x01, 0x00})
	return err
}

func readSOCKS5AuthField(reader io.Reader) (string, error) {
	n, err := readByte(reader)
	if err != nil {
		return "", err
	}
	buf := make([]byte, int(n))
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readSOCKS5Host(reader io.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		var ip [4]byte
		if _, err := io.ReadFull(reader, ip[:]); err != nil {
			return "", err
		}
		return net.IPv4(ip[0], ip[1], ip[2], ip[3]).String(), nil
	case 0x03:
		n, err := readByte(reader)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "", fmt.Errorf("SOCKS5 domain target is empty")
		}
		buf := make([]byte, int(n))
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return string(buf), nil
	case 0x04:
		var ip [16]byte
		if _, err := io.ReadFull(reader, ip[:]); err != nil {
			return "", err
		}
		return net.IP(ip[:]).String(), nil
	default:
		return "", fmt.Errorf("unsupported SOCKS5 address type %d", atyp)
	}
}

func newClientRequest(protocol string, host string, port int) (ClientRequest, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return ClientRequest{}, fmt.Errorf("proxy target missing host")
	}
	if port < 1 || port > 65535 {
		return ClientRequest{}, fmt.Errorf("proxy target port out of range")
	}
	return ClientRequest{
		Protocol: protocol,
		Target:   net.JoinHostPort(host, strconv.Itoa(port)),
		Host:     host,
		Port:     port,
	}, nil
}

func splitTarget(target string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(target))
	if err != nil {
		return "", 0, fmt.Errorf("proxy target must include host and port: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, fmt.Errorf("proxy target port must be numeric: %w", err)
	}
	return host, port, nil
}

func readNullString(reader io.Reader) (string, error) {
	const maxNullStringBytes = 4096
	value := make([]byte, 0, 32)
	for len(value) < maxNullStringBytes {
		b, err := readByte(reader)
		if err != nil {
			return "", err
		}
		if b == 0 {
			return string(value), nil
		}
		value = append(value, b)
	}
	return "", fmt.Errorf("null-terminated proxy field too large")
}

func readByte(reader io.Reader) (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(reader, b[:])
	return b[0], err
}

func hasMethod(methods []byte, want byte) bool {
	for _, method := range methods {
		if method == want {
			return true
		}
	}
	return false
}

func validHTTPBasicAuth(header string, auth EntryAuth) bool {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	want := auth.Username + ":" + auth.Password
	return subtle.ConstantTimeCompare(decoded, []byte(want)) == 1
}

func writeSOCKS4Reply(conn net.Conn, ok bool, port int, ip net.IP) {
	code := byte(0x5b)
	if ok {
		code = 0x5a
	}
	reply := []byte{0x00, code, byte(port >> 8), byte(port), 0, 0, 0, 0}
	if ipv4 := ip.To4(); ipv4 != nil {
		copy(reply[4:], ipv4)
	}
	_, _ = conn.Write(reply)
}

func writeSOCKS5Reply(conn net.Conn, status byte) {
	_, _ = conn.Write([]byte{0x05, status, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

func writeHTTPProxyError(conn net.Conn, status int) {
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\n\r\n", status, http.StatusText(status))
}

func applyContextDeadline(ctx context.Context, conn net.Conn) func() {
	if conn == nil {
		return func() {}
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		return func() { _ = conn.SetDeadline(time.Time{}) }
	}
	if ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}
