package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var measurementStart = make(chan struct{})
var measurementOnce sync.Once
var nodesReady = make(chan struct{})
var nodesReadyMu sync.Mutex
var readyNodes = make(map[string]bool)
var eventAcks sync.Map
var nextEventID atomic.Uint64

func markNodeReady(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id != "agent-a" && id != "relay-b" && id != "relay-c" {
		http.Error(w, "unknown fixture node", 400)
		return
	}
	nodesReadyMu.Lock()
	defer nodesReadyMu.Unlock()
	if !readyNodes[id] {
		readyNodes[id] = true
		if len(readyNodes) == 3 {
			close(nodesReady)
		}
	}
	w.WriteHeader(http.StatusOK)
}

type l4Rule struct {
	ID          int         `json:"id"`
	AgentID     string      `json:"agent_id"`
	Name        string      `json:"name"`
	Protocol    string      `json:"protocol"`
	ListenHost  string      `json:"listen_host"`
	ListenPort  int         `json:"listen_port"`
	Backends    []l4Backend `json:"backends"`
	RelayLayers [][]int     `json:"relay_layers,omitempty"`
	Enabled     bool        `json:"enabled"`
	Revision    int         `json:"revision"`
}

type l4Backend struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func verificationL4Rules(cfg config) []l4Rule {
	u, err := url.Parse(cfg.backendURL)
	if err != nil {
		panic(err)
	}
	var rules []l4Rule
	for i, layers := range [][][]int{nil, cfg.relayLayers, cfg.relay2Layers} {
		for j, protocol := range []string{"tcp", "udp", "tcp"} {
			rules = append(rules, l4Rule{ID: 201 + i + j*10, AgentID: "agent-a", Name: fmt.Sprintf("verify-%d-%d", i, j), Protocol: protocol, ListenHost: "0.0.0.0", ListenPort: 7001 + i + j*100, Backends: []l4Backend{{Host: u.Hostname(), Port: 9002 + j}}, RelayLayers: layers, Enabled: true, Revision: 1})
		}
	}
	return rules
}

func verificationPayload(size int) []byte {
	p := make([]byte, size)
	for i := range p {
		p[i] = byte((i*31 + i/251) % 256)
	}
	return p
}

func serveVerificationHTTP(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/check":
		payload := verificationPayload(1 << 20)
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	case "/echo":
		payload, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return true
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	case "/events":
		id := r.URL.Query().Get("id")
		ack := make(chan struct{}, 1)
		eventAcks.Store(id, ack)
		defer eventAcks.Delete(id)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return true
		case <-ack:
		}
		_, _ = io.WriteString(w, "data: last\n\n")
	case "/event-ack":
		ack, ok := eventAcks.Load(r.URL.Query().Get("id"))
		if !ok {
			http.Error(w, "event stream missing", 404)
			return true
		}
		select {
		case ack.(chan struct{}) <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}

func startVerificationBackends() error {
	udp, err := net.ListenPacket("udp", ":9003")
	if err != nil {
		return err
	}
	go func() {
		buf := make([]byte, 65536)
		for {
			n, peer, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := udp.WriteTo(buf[:n], peer); err != nil {
				return
			}
		}
	}()
	tcp, err := net.Listen("tcp", ":9004")
	if err != nil {
		_ = udp.Close()
		return err
	}
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(45 * time.Second))
				payload, err := io.ReadAll(io.LimitReader(conn, 1<<20))
				if err != nil {
					return
				}
				_, _ = conn.Write(payload)
			}()
		}
	}()
	return nil
}

func verifyForwarding(cfg config) error {
	type check struct {
		name string
		run  func() error
	}
	var checks []check
	for i, name := range []string{"direct", "relay", "relay2"} {
		address := []string{cfg.directAddress, cfg.relayAddress, cfg.relay2Address}[i]
		checks = append(checks, check{"http_" + name, func() error { return verifyHTTP(address) }})
		tcpAddress := fmt.Sprintf("http://172.31.1.10:%d", 7001+i)
		checks = append(checks, check{"l4_tcp_" + name, func() error { return verifyHTTP(tcpAddress) }})
		udpAddress := fmt.Sprintf("172.31.1.10:%d", 7101+i)
		checks = append(checks, check{"l4_udp_" + name, func() error { return verifyUDP(udpAddress) }})
		halfCloseAddress := fmt.Sprintf("172.31.1.10:%d", 7201+i)
		checks = append(checks, check{"l4_half_close_" + name, func() error { return verifyHalfClose(halfCloseAddress) }})
	}
	var wg sync.WaitGroup
	errors := make(chan error, len(checks))
	for _, test := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			if err := test.run(); err != nil {
				errors <- fmt.Errorf("%s: %w", test.name, err)
				return
			}
			emit("VERIFY", map[string]any{"name": test.name, "passed": true, "seconds": time.Since(start).Seconds()})
		}()
	}
	wg.Wait()
	close(errors)
	var failures []string
	for err := range errors {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		return fmt.Errorf("forwarding verification failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

func verifyHTTP(address string) error {
	client := newHTTPClient(1)
	client.Timeout = 60 * time.Second
	defer client.CloseIdleConnections()
	for _, upload := range []bool{false, true} {
		payload := verificationPayload(1 << 20)
		method, path := "GET", "/check"
		var body io.Reader
		if upload {
			method, path, body = "POST", "/echo", bytes.NewReader(payload)
		}
		req, err := http.NewRequest(method, address+path, body)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, int64(len(payload))+1))
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode != 200 || sha256.Sum256(data) != sha256.Sum256(payload) {
			return fmt.Errorf("%s status=%d bytes=%d or hash mismatch", method, resp.StatusCode, len(data))
		}
	}
	id := fmt.Sprint(nextEventID.Add(1))
	resp, err := client.Get(address + "/events?id=" + id)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	if line != "data: first\n" {
		return fmt.Errorf("invalid first event: %q", line)
	}
	ack, err := client.Post(address+"/event-ack?id="+id, "text/plain", nil)
	if err != nil {
		return err
	}
	_ = ack.Body.Close()
	if ack.StatusCode != http.StatusNoContent {
		return fmt.Errorf("event acknowledgement status=%d", ack.StatusCode)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 || line+string(rest) != "data: first\n\ndata: last\n\n" {
		return fmt.Errorf("invalid event stream, status=%d", resp.StatusCode)
	}
	return nil
}

func verifyUDP(address string) error {
	conn, err := net.DialTimeout("udp", address, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	buf := make([]byte, 65536)
	for _, size := range []int{128, 1400, 60000, 128} {
		start := time.Now()
		payload := verificationPayload(size)
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := conn.Write(payload); err != nil {
			return err
		}
		n, err := conn.Read(buf)
		if err != nil {
			return fmt.Errorf("%d-byte datagram: %w", size, err)
		}
		if !bytes.Equal(buf[:n], payload) {
			return fmt.Errorf("UDP %d-byte packet mismatch: %d", size, n)
		}
		emit("UDP_PACKET", map[string]any{"target": address, "bytes": size, "seconds": time.Since(start).Seconds()})
		// Exercise the 250ms poll boundary between datagrams too.
		time.Sleep(300 * time.Millisecond)
	}
	return nil
}

func verifyHalfClose(address string) error {
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(45 * time.Second))
	payload := verificationPayload(4096)
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		return err
	}
	got, err := io.ReadAll(conn)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("half-close response mismatch: %d", len(got))
	}
	return nil
}
