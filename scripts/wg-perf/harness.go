package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	mode            string
	masterAddr      string
	entryAddress    string
	directAddress   string
	rttIterations   int
	c1Bytes         int64
	c1Duration      time.Duration
	c8BytesPerConn  int64
	c8Duration      time.Duration
	c8Concurrency   int
	benchmarkFilter string
	preMeasureWait  time.Duration
	backendAddr     string
	backendHost     string
	backendPort     int
	wgEntryHost     string
	wgEntryPort     int
	wgRelayHost     string
	wgRelayPort     int
	wgTunnelHost    string
	wgTunnelPort    int
	wgBindAddresses []string
}

type benchmarkCase struct {
	name string
	run  func() result
}

type snapshot struct {
	DesiredVersion      string              `json:"desired_version"`
	DesiredRevision     int64               `json:"desired_revision"`
	Rules               []httpRule          `json:"rules"`
	L4Rules             []l4Rule            `json:"l4_rules"`
	RelayListeners      []relayListener     `json:"relay_listeners"`
	WireGuardProfiles   []wireGuardProfile  `json:"wireguard_profiles"`
	Certificates        []certificateBundle `json:"certificates"`
	CertificatePolicies []certificatePolicy `json:"certificate_policies"`
}

type httpRule struct{}

type l4Rule struct {
	ID                   int         `json:"id,omitempty"`
	Name                 string      `json:"name,omitempty"`
	Protocol             string      `json:"protocol"`
	ListenHost           string      `json:"listen_host"`
	ListenPort           int         `json:"listen_port"`
	UpstreamHost         string      `json:"upstream_host"`
	UpstreamPort         int         `json:"upstream_port"`
	Backends             []l4Backend `json:"backends,omitempty"`
	RelayLayers          [][]int     `json:"relay_layers,omitempty"`
	ListenMode           string      `json:"listen_mode,omitempty"`
	WireGuardProfileID   *int        `json:"wireguard_profile_id,omitempty"`
	WireGuardInboundMode string      `json:"wireguard_inbound_mode,omitempty"`
	WireGuardListenHost  string      `json:"wireguard_listen_host,omitempty"`
	ProxyEgressMode      string      `json:"proxy_egress_mode,omitempty"`
	Enabled              bool        `json:"enabled"`
	Revision             int64       `json:"revision"`
}

type l4Backend struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type relayListener struct {
	ID                 int        `json:"id"`
	AgentID            string     `json:"agent_id"`
	Name               string     `json:"name"`
	ListenHost         string     `json:"listen_host"`
	BindHosts          []string   `json:"bind_hosts"`
	ListenPort         int        `json:"listen_port"`
	PublicHost         string     `json:"public_host"`
	PublicPort         int        `json:"public_port"`
	Enabled            bool       `json:"enabled"`
	CertificateID      *int       `json:"certificate_id"`
	TLSMode            string     `json:"tls_mode"`
	TransportMode      string     `json:"transport_mode"`
	WireGuardProfileID *int       `json:"wireguard_profile_id,omitempty"`
	PinSet             []relayPin `json:"pin_set"`
	AllowSelfSigned    bool       `json:"allow_self_signed"`
	Revision           int64      `json:"revision"`
}

type relayPin struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type certificateBundle struct {
	ID       int    `json:"id"`
	Domain   string `json:"domain"`
	Revision int64  `json:"revision"`
	CertPEM  string `json:"cert_pem"`
	KeyPEM   string `json:"key_pem"`
}

type certificatePolicy struct {
	ID              int    `json:"id"`
	Domain          string `json:"domain"`
	Enabled         bool   `json:"enabled"`
	Scope           string `json:"scope"`
	IssuerMode      string `json:"issuer_mode"`
	Status          string `json:"status"`
	Revision        int64  `json:"revision"`
	Usage           string `json:"usage"`
	CertificateType string `json:"certificate_type"`
	SelfSigned      bool   `json:"self_signed"`
}

type wireGuardProfile struct {
	ID             int             `json:"id"`
	AgentID        string          `json:"agent_id"`
	Name           string          `json:"name"`
	Mode           string          `json:"mode"`
	PrivateKey     string          `json:"private_key,omitempty"`
	ListenPort     int             `json:"listen_port"`
	PublicEndpoint string          `json:"public_endpoint,omitempty"`
	BindAddresses  []string        `json:"bind_addresses,omitempty"`
	Addresses      []string        `json:"addresses"`
	Peers          []wireGuardPeer `json:"peers"`
	DNS            []string        `json:"dns"`
	MTU            int             `json:"mtu"`
	Enabled        bool            `json:"enabled"`
	Tags           []string        `json:"tags"`
	Revision       int64           `json:"revision"`
}

type wireGuardPeer struct {
	Name         string   `json:"name"`
	PublicKey    string   `json:"public_key"`
	PresharedKey string   `json:"preshared_key,omitempty"`
	Endpoint     string   `json:"endpoint"`
	AllowedIPs   []string `json:"allowed_ips"`
}

type heartbeatRequest struct {
	AgentID string `json:"agent_id"`
}

type result struct {
	Name        string  `json:"name"`
	Target      string  `json:"target"`
	Concurrency int     `json:"concurrency,omitempty"`
	Bytes       int64   `json:"bytes,omitempty"`
	Seconds     float64 `json:"seconds,omitempty"`
	MBps        float64 `json:"MBps,omitempty"`
	Mbps        float64 `json:"Mbps,omitempty"`
	MinUS       float64 `json:"min_us,omitempty"`
	AvgUS       float64 `json:"avg_us,omitempty"`
	P50US       float64 `json:"p50_us,omitempty"`
	P95US       float64 `json:"p95_us,omitempty"`
	P99US       float64 `json:"p99_us,omitempty"`
}

const (
	protocolModeEcho              = 1
	protocolModeDownload          = 2
	protocolModeDownloadUnlimited = 3
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	cfg := loadConfig()
	if cfg.mode == "backend" {
		if err := startBackend(context.Background(), cfg.backendAddr); err != nil {
			log.Fatal(err)
		}
		select {}
	}

	certPEM, keyPEM, pin, err := issueRelayCert(cfg.wgEntryHost)
	if err != nil {
		log.Fatal(err)
	}
	snapshots := buildSnapshots(cfg, certPEM, keyPEM, pin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := startMaster(ctx, cfg.masterAddr, snapshots); err != nil {
		log.Fatal(err)
	}

	log.Printf("waiting for direct-b at %s", cfg.directAddress)
	mustWaitForEcho("direct-b", cfg.directAddress, 40*time.Second)
	log.Printf("waiting for wg-entry at %s", cfg.entryAddress)
	mustWaitForEcho("wg-entry", cfg.entryAddress, 40*time.Second)
	if cfg.preMeasureWait > 0 {
		log.Printf("waiting %s before measurements", cfg.preMeasureWait)
		time.Sleep(cfg.preMeasureWait)
	}

	benchmarks := []benchmarkCase{
		{name: "direct_b_connect", run: func() result { return measureConnectEcho("direct_b_connect", cfg.directAddress, cfg.rttIterations) }},
		{name: "wg_to_b_connect", run: func() result { return measureConnectEcho("wg_to_b_connect", cfg.entryAddress, cfg.rttIterations) }},
		{name: "direct_b_rtt", run: func() result { return measureRTT("direct_b_rtt", cfg.directAddress, cfg.rttIterations) }},
		{name: "wg_to_b_rtt", run: func() result { return measureRTT("wg_to_b_rtt", cfg.entryAddress, cfg.rttIterations) }},
		{name: "direct_b_c1", run: func() result {
			return measureThroughput("direct_b_c1", cfg.directAddress, 1, cfg.c1Bytes, cfg.c1Duration)
		}},
		{name: "wg_to_b_c1", run: func() result {
			return measureThroughput("wg_to_b_c1", cfg.entryAddress, 1, cfg.c1Bytes, cfg.c1Duration)
		}},
		{name: "direct_b_c8", run: func() result {
			return measureThroughput("direct_b_c8", cfg.directAddress, cfg.c8Concurrency, cfg.c8BytesPerConn, cfg.c8Duration)
		}},
		{name: "wg_to_b_c8", run: func() result {
			return measureThroughput("wg_to_b_c8", cfg.entryAddress, cfg.c8Concurrency, cfg.c8BytesPerConn, cfg.c8Duration)
		}},
	}
	selected, err := selectBenchmarks(cfg.benchmarkFilter, benchmarks)
	if err != nil {
		log.Fatal(err)
	}
	results := make([]result, 0, len(selected))
	for _, bench := range selected {
		log.Printf("running benchmark %s", bench.name)
		results = append(results, bench.run())
	}
	for _, res := range results {
		emit("RESULT", res)
	}
	emit("SUMMARY", results)
}

func loadConfig() config {
	backendHost := envString("HARNESS_BACKEND_HOST", "172.30.3.13")
	backendPort := envInt("HARNESS_BACKEND_PORT", 9002)
	return config{
		mode:            envString("HARNESS_MODE", "bench"),
		masterAddr:      envString("HARNESS_MASTER_ADDR", ":8080"),
		entryAddress:    envString("HARNESS_ENTRY_ADDRESS", "10.80.0.1:7000"),
		directAddress:   envString("HARNESS_DIRECT_ADDRESS", "172.30.0.20:9001"),
		rttIterations:   envInt("HARNESS_RTT_ITERATIONS", 300),
		c1Bytes:         envBytes("HARNESS_C1_BYTES", 512<<20),
		c1Duration:      envSeconds("HARNESS_C1_DURATION_SECONDS", 10),
		c8BytesPerConn:  envBytes("HARNESS_C8_BYTES_PER_CONN", 256<<20),
		c8Duration:      envSeconds("HARNESS_C8_DURATION_SECONDS", 10),
		c8Concurrency:   envInt("HARNESS_C8_CONCURRENCY", 8),
		benchmarkFilter: envString("HARNESS_BENCHMARKS", ""),
		preMeasureWait:  time.Duration(envInt("HARNESS_PRE_MEASURE_DELAY_MS", 0)) * time.Millisecond,
		backendAddr:     envString("HARNESS_BACKEND_LISTEN_ADDR", fmt.Sprintf(":%d", backendPort)),
		backendHost:     backendHost,
		backendPort:     backendPort,
		wgEntryHost:     envString("HARNESS_WG_ENTRY_HOST", "172.30.0.10"),
		wgEntryPort:     envInt("HARNESS_WG_ENTRY_PORT", 51820),
		wgRelayHost:     envString("HARNESS_WG_RELAY_HOST", "172.30.2.15"),
		wgRelayPort:     envInt("HARNESS_WG_RELAY_PORT", 51820),
		wgTunnelHost:    envString("HARNESS_WG_TUNNEL_HOST", "10.80.0.1"),
		wgTunnelPort:    envInt("HARNESS_WG_TUNNEL_PORT", 9443),
		wgBindAddresses: envList("HARNESS_WG_BIND_ADDRESSES"),
	}
}

func selectBenchmarks(filter string, benchmarks []benchmarkCase) ([]benchmarkCase, error) {
	if strings.TrimSpace(filter) == "" {
		return benchmarks, nil
	}
	byName := make(map[string]benchmarkCase, len(benchmarks))
	for _, bench := range benchmarks {
		byName[bench.name] = bench
	}
	fields := strings.FieldsFunc(filter, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	selected := make([]benchmarkCase, 0, len(fields))
	for _, field := range fields {
		name := strings.TrimSpace(field)
		if name == "" {
			continue
		}
		bench, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown HARNESS_BENCHMARKS item %q", name)
		}
		selected = append(selected, bench)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("HARNESS_BENCHMARKS did not select any benchmark")
	}
	return selected, nil
}

func buildSnapshots(cfg config, certPEM, keyPEM, pin string) map[string]snapshot {
	listeners := []relayListener{
		newHarnessRelayListener(2, "relay-a1", "172.30.2.11", 9443, "0.0.0.0", 9443, certIDForRelay(2), pin, nil),
		newHarnessRelayListener(3, "relay-a2", "172.30.2.12", 9443, "0.0.0.0", 9443, certIDForRelay(3), pin, nil),
		newHarnessRelayListener(4, "relay-b3", "172.30.4.13", 9443, "0.0.0.0", 9443, certIDForRelay(4), pin, nil),
		newHarnessRelayListener(5, "relay-b4", "172.30.4.14", 9443, "0.0.0.0", 9443, certIDForRelay(5), pin, nil),
	}
	certs := make([]certificateBundle, 0, len(listeners))
	policies := make([]certificatePolicy, 0, len(listeners))
	for _, listener := range listeners {
		certID := *listener.CertificateID
		certs = append(certs, certificateBundle{
			ID:       certID,
			Domain:   listener.PublicHost,
			Revision: 1,
			CertPEM:  certPEM,
			KeyPEM:   keyPEM,
		})
		policies = append(policies, certificatePolicy{
			ID:              certID,
			Domain:          listener.PublicHost,
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Status:          "active",
			Revision:        1,
			Usage:           "relay_tunnel",
			CertificateType: "uploaded",
			SelfSigned:      true,
		})
	}

	const (
		clientWGPublic = "B1VKlIC/KoWYbYEJ0KVzymbo5RA5PS7oURi87WiinhU="
		relayWGPrivate = "AAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEA="
	)
	relayWGProfile := wireGuardProfile{
		ID:             1,
		AgentID:        "relay-wg",
		Name:           "relay-wg",
		Mode:           "generic_wireguard",
		PrivateKey:     relayWGPrivate,
		ListenPort:     cfg.wgRelayPort,
		PublicEndpoint: fmt.Sprintf("%s:%d", cfg.wgRelayHost, cfg.wgRelayPort),
		BindAddresses:  append([]string(nil), cfg.wgBindAddresses...),
		Addresses:      []string{cfg.wgTunnelHost + "/24"},
		Peers: []wireGuardPeer{{
			Name:       "perf",
			PublicKey:  clientWGPublic,
			AllowedIPs: []string{"10.80.0.2/32"},
		}},
		MTU:      1280,
		Enabled:  true,
		Revision: 1,
	}

	return map[string]snapshot{
		"relay-wg": {
			DesiredVersion:  "perf",
			DesiredRevision: 1,
			Rules:           []httpRule{},
			L4Rules: []l4Rule{{
				ID:                   101,
				Name:                 "wg-entry-layered-relay-to-b",
				Protocol:             "tcp",
				ListenHost:           "0.0.0.0",
				ListenPort:           7000,
				Backends:             []l4Backend{{Host: cfg.backendHost, Port: cfg.backendPort}},
				UpstreamHost:         cfg.backendHost,
				UpstreamPort:         cfg.backendPort,
				RelayLayers:          [][]int{{2, 3}, {4, 5}},
				ListenMode:           "wireguard",
				WireGuardProfileID:   intPtr(1),
				WireGuardInboundMode: "address",
				WireGuardListenHost:  cfg.wgTunnelHost,
				Enabled:              true,
				Revision:             1,
			}},
			RelayListeners:      listeners,
			WireGuardProfiles:   []wireGuardProfile{relayWGProfile},
			Certificates:        certs,
			CertificatePolicies: policies,
		},
		"relay-a1": {
			DesiredVersion:      "perf",
			DesiredRevision:     1,
			Rules:               []httpRule{},
			L4Rules:             []l4Rule{},
			RelayListeners:      listeners,
			WireGuardProfiles:   []wireGuardProfile{},
			Certificates:        certs,
			CertificatePolicies: policies,
		},
		"relay-a2": {
			DesiredVersion:      "perf",
			DesiredRevision:     1,
			Rules:               []httpRule{},
			L4Rules:             []l4Rule{},
			RelayListeners:      listeners,
			WireGuardProfiles:   []wireGuardProfile{},
			Certificates:        certs,
			CertificatePolicies: policies,
		},
		"relay-b3": {
			DesiredVersion:      "perf",
			DesiredRevision:     1,
			Rules:               []httpRule{},
			L4Rules:             []l4Rule{},
			RelayListeners:      listeners,
			WireGuardProfiles:   []wireGuardProfile{},
			Certificates:        certs,
			CertificatePolicies: policies,
		},
		"relay-b4": {
			DesiredVersion:      "perf",
			DesiredRevision:     1,
			Rules:               []httpRule{},
			L4Rules:             []l4Rule{},
			RelayListeners:      listeners,
			WireGuardProfiles:   []wireGuardProfile{},
			Certificates:        certs,
			CertificatePolicies: policies,
		},
		"agent-b": {
			DesiredVersion:  "perf",
			DesiredRevision: 1,
			Rules:           []httpRule{},
			L4Rules: []l4Rule{{
				ID:           201,
				Name:         "b-direct-to-echo",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   9001,
				Backends:     []l4Backend{{Host: cfg.backendHost, Port: cfg.backendPort}},
				UpstreamHost: cfg.backendHost,
				UpstreamPort: cfg.backendPort,
				Enabled:      true,
				Revision:     1,
			}},
			RelayListeners:      listeners,
			WireGuardProfiles:   []wireGuardProfile{},
			Certificates:        []certificateBundle{},
			CertificatePolicies: []certificatePolicy{},
		},
	}
}

func newHarnessRelayListener(id int, agentID, publicHost string, publicPort int, listenHost string, listenPort int, certID int, pin string, wgProfileID *int) relayListener {
	transportMode := "tls_tcp"
	if wgProfileID != nil {
		transportMode = "wireguard"
	}
	return relayListener{
		ID:                 id,
		AgentID:            agentID,
		Name:               agentID,
		ListenHost:         listenHost,
		BindHosts:          []string{listenHost},
		ListenPort:         listenPort,
		PublicHost:         publicHost,
		PublicPort:         publicPort,
		Enabled:            true,
		CertificateID:      &certID,
		TLSMode:            "pin_only",
		TransportMode:      transportMode,
		WireGuardProfileID: wgProfileID,
		PinSet: []relayPin{{
			Type:  "spki_sha256",
			Value: pin,
		}},
		AllowSelfSigned: true,
		Revision:        1,
	}
}

func intPtr(v int) *int         { return &v }
func certIDForRelay(id int) int { return 10 + id }

func startMaster(ctx context.Context, address string, snapshots map[string]snapshot) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/agents/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req heartbeatRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		snap, ok := snapshots[req.AgentID]
		if !ok {
			http.Error(w, "unknown agent", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sync": snap})
	})
	server := &http.Server{Addr: address, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("master error: %v", err)
			os.Exit(1)
		}
	}()
	return nil
}

func startBackend(ctx context.Context, address string) error {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				handleBackendConn(c)
			}(conn)
		}
	}()
	return nil
}

func handleBackendConn(conn net.Conn) {
	var mode [1]byte
	if _, err := io.ReadFull(conn, mode[:]); err != nil {
		return
	}
	switch mode[0] {
	case protocolModeEcho:
		_, _ = io.Copy(conn, conn)
	case protocolModeDownload:
		var sizeBuf [8]byte
		if _, err := io.ReadFull(conn, sizeBuf[:]); err != nil {
			return
		}
		remaining := int64(binary.BigEndian.Uint64(sizeBuf[:]))
		payload := bytes.Repeat([]byte{7}, 64*1024)
		for remaining > 0 {
			chunk := payload
			if remaining < int64(len(chunk)) {
				chunk = chunk[:remaining]
			}
			n, err := conn.Write(chunk)
			if err != nil {
				return
			}
			remaining -= int64(n)
		}
	case protocolModeDownloadUnlimited:
		payload := bytes.Repeat([]byte{7}, 64*1024)
		for {
			if _, err := conn.Write(payload); err != nil {
				return
			}
		}
	}
}

func mustWaitForEcho(name, address string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := echoOnce(address, []byte("ready")); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Fatalf("%s not ready at %s within %s", name, address, timeout)
}

func echoOnce(address string, payload []byte) error {
	conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	if _, err := conn.Write([]byte{protocolModeEcho}); err != nil {
		return err
	}
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if !bytes.Equal(reply, payload) {
		return fmt.Errorf("echo mismatch: got %q want %q", reply, payload)
	}
	return nil
}

func measureConnectEcho(name, address string, iterations int) result {
	samples := make([]float64, 0, iterations)
	payload := []byte("connect")
	for i := 0; i < iterations+3; i++ {
		start := time.Now()
		if err := echoOnce(address, payload); err != nil {
			log.Fatal(err)
		}
		if i >= 3 {
			samples = append(samples, float64(time.Since(start).Microseconds()))
		}
	}
	return latencyResult(name, address, samples)
}

func measureRTT(name, address string, iterations int) result {
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	_, _ = conn.Write([]byte{protocolModeEcho})
	buf := []byte{1}
	reply := []byte{0}
	samples := make([]float64, 0, iterations)
	for i := 0; i < iterations+20; i++ {
		start := time.Now()
		if _, err := conn.Write(buf); err != nil {
			log.Fatal(err)
		}
		if _, err := io.ReadFull(conn, reply); err != nil {
			log.Fatal(err)
		}
		if i >= 20 {
			samples = append(samples, float64(time.Since(start).Microseconds()))
		}
	}
	return latencyResult(name, address, samples)
}

func latencyResult(name, address string, samples []float64) result {
	sort.Float64s(samples)
	sum := 0.0
	min := math.Inf(1)
	for _, sample := range samples {
		sum += sample
		if sample < min {
			min = sample
		}
	}
	return result{Name: name, Target: address, MinUS: min, AvgUS: sum / float64(len(samples)), P50US: percentile(samples, 0.50), P95US: percentile(samples, 0.95), P99US: percentile(samples, 0.99)}
}

func measureThroughput(name, address string, concurrency int, bytesPerConn int64, duration time.Duration) result {
	start := time.Now()
	deadline := start.Add(duration)
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)
	var total int64
	var totalMu sync.Mutex
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int64
			var err error
			if duration > 0 {
				n, err = transferForDuration(address, deadline)
			} else {
				n, err = transfer(address, bytesPerConn)
			}
			totalMu.Lock()
			total += n
			totalMu.Unlock()
			if err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	if err := <-errCh; err != nil {
		log.Fatal(err)
	}
	elapsed := time.Since(start).Seconds()
	return result{Name: name, Target: address, Concurrency: concurrency, Bytes: total, Seconds: elapsed, MBps: float64(total) / elapsed / 1_000_000, Mbps: float64(total) * 8 / elapsed / 1_000_000}
}

func transfer(address string, totalBytes int64) (int64, error) {
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	req := make([]byte, 9)
	req[0] = protocolModeDownload
	binary.BigEndian.PutUint64(req[1:], uint64(totalBytes))
	if _, err := conn.Write(req); err != nil {
		return 0, err
	}
	buf := make([]byte, 64*1024)
	var readBytes int64
	for readBytes < totalBytes {
		want := len(buf)
		if remaining := totalBytes - readBytes; remaining < int64(want) {
			want = int(remaining)
		}
		n, err := io.ReadFull(conn, buf[:want])
		readBytes += int64(n)
		if err != nil {
			return readBytes, err
		}
	}
	return readBytes, nil
}

func transferForDuration(address string, deadline time.Time) (int64, error) {
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	_ = conn.SetReadDeadline(deadline)
	_, _ = conn.Write([]byte{protocolModeDownloadUnlimited})
	buf := make([]byte, 64*1024)
	var readBytes int64
	for {
		n, err := conn.Read(buf)
		readBytes += int64(n)
		if err == nil {
			continue
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return readBytes, nil
		}
		if err == io.EOF {
			return readBytes, nil
		}
		return readBytes, err
	}
}

func issueRelayCert(host string) (string, string, string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", "", err
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", "", err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return "", "", "", err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return string(certPEM), string(keyPEM), base64.StdEncoding.EncodeToString(sum[:]), nil
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*p)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func emit(prefix string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s %s\n", prefix, data)
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envList(name string) []string {
	value := os.Getenv(name)
	if strings.TrimSpace(value) == "" {
		return nil
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func envInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatal(err)
	}
	return parsed
}

func envBytes(name string, fallback int64) int64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		log.Fatal(err)
	}
	return parsed
}

func envSeconds(name string, fallback int) time.Duration {
	return time.Duration(envInt(name, fallback)) * time.Second
}
