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
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type config struct {
	mode            string
	masterAddr      string
	directAddress   string
	relayAddress    string
	backendURL      string
	backendListen   string
	relayPublicHost string
	relayPublicPort int
	relayLayers     [][]int
	downloadBytes   int64
	c1Duration      time.Duration
	c8Duration      time.Duration
	c8Concurrency   int
	benchmarkFilter string
	preMeasureWait  time.Duration
}

type benchmarkCase struct {
	name string
	run  func() result
}

type snapshot struct {
	DesiredVersion      string              `json:"desired_version"`
	DesiredRevision     int64               `json:"desired_revision"`
	Rules               []httpRule          `json:"rules"`
	RelayListeners      []relayListener     `json:"relay_listeners"`
	Certificates        []certificateBundle `json:"certificates"`
	CertificatePolicies []certificatePolicy `json:"certificate_policies"`
}

type httpRule struct {
	ID            int           `json:"id"`
	AgentID       string        `json:"agent_id"`
	FrontendURL   string        `json:"frontend_url"`
	Backends      []httpBackend `json:"backends"`
	RelayLayers   [][]int       `json:"relay_layers,omitempty"`
	ProxyRedirect bool          `json:"proxy_redirect,omitempty"`
	Enabled       bool          `json:"enabled"`
	Revision      int64         `json:"revision"`
}

type httpBackend struct {
	URL string `json:"url"`
}

type relayListener struct {
	ID                      int        `json:"id"`
	AgentID                 string     `json:"agent_id"`
	Name                    string     `json:"name"`
	ListenHost              string     `json:"listen_host"`
	BindHosts               []string   `json:"bind_hosts"`
	ListenPort              int        `json:"listen_port"`
	PublicHost              string     `json:"public_host"`
	PublicPort              int        `json:"public_port"`
	Enabled                 bool       `json:"enabled"`
	CertificateID           *int       `json:"certificate_id"`
	TLSMode                 string     `json:"tls_mode"`
	TransportMode           string     `json:"transport_mode"`
	AllowTransportFallback  bool       `json:"allow_transport_fallback"`
	ObfsMode                string     `json:"obfs_mode"`
	PinSet                  []relayPin `json:"pin_set"`
	TrustedCACertificateIDs []int      `json:"trusted_ca_certificate_ids"`
	AllowSelfSigned         bool       `json:"allow_self_signed"`
	Tags                    []string   `json:"tags"`
	Revision                int64      `json:"revision"`
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
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	cfg := loadConfig()

	if cfg.mode == "backend" {
		if err := startBackend(cfg.backendListen); err != nil {
			log.Fatal(err)
		}
		select {}
	}

	certPEM, keyPEM, pin, err := issueRelayCert(cfg.relayPublicHost)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	if err := startMaster(ctx, cfg.masterAddr, buildSnapshots(cfg, certPEM, keyPEM, pin)); err != nil {
		log.Fatal(err)
	}
	log.Printf("mock master listening on %s", cfg.masterAddr)

	waitForHTTP("direct", cfg.directAddress, 45*time.Second)
	waitForHTTP("relay", cfg.relayAddress, 45*time.Second)
	if cfg.preMeasureWait > 0 {
		log.Printf("waiting %s before measurements", cfg.preMeasureWait)
		time.Sleep(cfg.preMeasureWait)
	}

	benchmarks := []benchmarkCase{
		{name: "http_direct_c1", run: func() result {
			return measureHTTPThroughput("http_direct_c1", cfg.directAddress, 1, cfg.downloadBytes, cfg.c1Duration)
		}},
		{name: "http_relay_c1", run: func() result {
			return measureHTTPThroughput("http_relay_c1", cfg.relayAddress, 1, cfg.downloadBytes, cfg.c1Duration)
		}},
		{name: "http_direct_c8", run: func() result {
			return measureHTTPThroughput("http_direct_c8", cfg.directAddress, cfg.c8Concurrency, cfg.downloadBytes, cfg.c8Duration)
		}},
		{name: "http_relay_c8", run: func() result {
			return measureHTTPThroughput("http_relay_c8", cfg.relayAddress, cfg.c8Concurrency, cfg.downloadBytes, cfg.c8Duration)
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
	return config{
		mode:            envString("HARNESS_MODE", "bench"),
		masterAddr:      envString("HARNESS_MASTER_ADDR", ":8080"),
		directAddress:   envString("HARNESS_DIRECT_ADDRESS", "http://172.31.1.10:8081"),
		relayAddress:    envString("HARNESS_RELAY_ADDRESS", "http://172.31.1.10:8082"),
		backendURL:      envString("HARNESS_BACKEND_URL", "http://172.31.3.13:9002"),
		backendListen:   envString("HARNESS_BACKEND_LISTEN_ADDR", ":9002"),
		relayPublicHost: envString("HARNESS_RELAY_PUBLIC_HOST", "172.31.2.11"),
		relayPublicPort: envInt("HARNESS_RELAY_PUBLIC_PORT", 9443),
		relayLayers:     envRelayLayers("HARNESS_RELAY_LAYER_IDS", [][]int{{1}}),
		downloadBytes:   envBytes("HARNESS_DOWNLOAD_BYTES", 512<<20),
		c1Duration:      envSeconds("HARNESS_C1_DURATION_SECONDS", 0),
		c8Duration:      envSeconds("HARNESS_C8_DURATION_SECONDS", 0),
		c8Concurrency:   envInt("HARNESS_C8_CONCURRENCY", 8),
		benchmarkFilter: envString("HARNESS_BENCHMARKS", ""),
		preMeasureWait:  time.Duration(envInt("HARNESS_PRE_MEASURE_DELAY_MS", 0)) * time.Millisecond,
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
	listener := newHarnessRelayListener(1, "relay-b", cfg.relayPublicHost, cfg.relayPublicPort, certIDForRelay(1), pin)
	certID := *listener.CertificateID
	certs := []certificateBundle{{
		ID:       certID,
		Domain:   listener.PublicHost,
		Revision: 1,
		CertPEM:  certPEM,
		KeyPEM:   keyPEM,
	}}
	policies := []certificatePolicy{{
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
	}}

	return map[string]snapshot{
		"agent-a": {
			DesiredVersion:  "perf",
			DesiredRevision: 1,
			Rules: []httpRule{
				{
					ID:          101,
					AgentID:     "agent-a",
					FrontendURL: trimHTTPURL(cfg.directAddress),
					Backends:    []httpBackend{{URL: trimHTTPURL(cfg.backendURL)}},
					Enabled:     true,
					Revision:    1,
				},
				{
					ID:          102,
					AgentID:     "agent-a",
					FrontendURL: trimHTTPURL(cfg.relayAddress),
					Backends:    []httpBackend{{URL: trimHTTPURL(cfg.backendURL)}},
					RelayLayers: cfg.relayLayers,
					Enabled:     true,
					Revision:    1,
				},
			},
			RelayListeners:      []relayListener{listener},
			Certificates:        certs,
			CertificatePolicies: policies,
		},
		"relay-b": {
			DesiredVersion:      "perf",
			DesiredRevision:     1,
			Rules:               nil,
			RelayListeners:      []relayListener{listener},
			Certificates:        certs,
			CertificatePolicies: policies,
		},
	}
}

func newHarnessRelayListener(id int, agentID, publicHost string, publicPort, certID int, pin string) relayListener {
	return relayListener{
		ID:                      id,
		AgentID:                 agentID,
		Name:                    agentID,
		ListenHost:              "0.0.0.0",
		BindHosts:               []string{"0.0.0.0"},
		ListenPort:              publicPort,
		PublicHost:              publicHost,
		PublicPort:              publicPort,
		Enabled:                 true,
		CertificateID:           &certID,
		TLSMode:                 "pin_only",
		TransportMode:           "tls_tcp",
		AllowTransportFallback:  true,
		ObfsMode:                "off",
		PinSet:                  []relayPin{{Type: "spki_sha256", Value: pin}},
		TrustedCACertificateIDs: nil,
		AllowSelfSigned:         true,
		Tags:                    []string{"relay"},
		Revision:                1,
	}
}

func certIDForRelay(id int) int {
	return 10 + id
}

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

func startBackend(address string) error {
	server := &http.Server{
		Addr: address,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/healthz":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			case "/download":
				serveInfiniteDownload(w, r)
			default:
				http.NotFound(w, r)
			}
		}),
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("backend error: %v", err)
			os.Exit(1)
		}
	}()
	return nil
}

func serveInfiniteDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	payload := bytes.Repeat([]byte{7}, 64*1024)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
		defer flusher.Flush()
	}
	for {
		if _, err := w.Write(payload); err != nil {
			return
		}
	}
}

func waitForHTTP(name, address string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	healthURL := strings.TrimRight(trimHTTPURL(address), "/") + "/healthz"
	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				log.Printf("%s ready at %s", name, address)
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	log.Fatalf("%s not ready at %s within %s", name, address, timeout)
}

func measureHTTPThroughput(name, address string, concurrency int, bytesPerConn int64, duration time.Duration) result {
	client := newHTTPClient(concurrency)
	target := strings.TrimRight(trimHTTPURL(address), "/") + "/download"
	start := time.Now()
	if duration > 0 {
		bytes := measureHTTPDuration(client, target, concurrency, duration)
		elapsed := time.Since(start)
		return result{
			Name:        name,
			Target:      address,
			Concurrency: concurrency,
			Bytes:       bytes,
			Seconds:     elapsed.Seconds(),
			MBps:        float64(bytes) / elapsed.Seconds() / (1024 * 1024),
		}
	}
	bytes := measureHTTPFixedBytes(client, target, concurrency, bytesPerConn)
	elapsed := time.Since(start)
	return result{
		Name:        name,
		Target:      address,
		Concurrency: concurrency,
		Bytes:       bytes,
		Seconds:     elapsed.Seconds(),
		MBps:        float64(bytes) / elapsed.Seconds() / (1024 * 1024),
	}
}

func measureHTTPDuration(client *http.Client, target string, concurrency int, duration time.Duration) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var total atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				errCh <- err
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				errCh <- err
				return
			}
			defer resp.Body.Close()
			n, err := io.Copy(io.Discard, resp.Body)
			total.Add(n)
			if err != nil && ctx.Err() == nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			log.Fatal(err)
		}
	}
	return total.Load()
}

func measureHTTPFixedBytes(client *http.Client, target string, concurrency int, bytesPerConn int64) int64 {
	var total atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				errCh <- err
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			defer resp.Body.Close()
			n, err := io.CopyN(io.Discard, resp.Body, bytesPerConn)
			total.Add(n)
			if err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			log.Fatal(err)
		}
	}
	return total.Load()
}

func newHTTPClient(concurrency int) *http.Client {
	maxConns := concurrency * 4
	if maxConns < 16 {
		maxConns = 16
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DisableCompression:    true,
		MaxIdleConns:          maxConns,
		MaxIdleConnsPerHost:   maxConns,
		MaxConnsPerHost:       maxConns,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var dialer net.Dialer
			dialer.Timeout = 5 * time.Second
			dialer.KeepAlive = 30 * time.Second
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Transport: transport}
}

func issueRelayCert(host string) (string, string, string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", "", err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
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

func trimHTTPURL(raw string) string {
	return strings.TrimSpace(raw)
}

func envRelayLayers(name string, fallback [][]int) [][]int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	if strings.EqualFold(value, "none") || strings.EqualFold(value, "direct") {
		return nil
	}
	segments := strings.Split(value, ";")
	if len(segments) == 1 {
		segments = []string{value}
	}
	layers := make([][]int, 0, len(segments))
	for _, segment := range segments {
		fields := strings.FieldsFunc(segment, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		})
		layer := make([]int, 0, len(fields))
		for _, field := range fields {
			trimmed := strings.TrimSpace(field)
			if trimmed == "" {
				continue
			}
			id, err := strconv.Atoi(trimmed)
			if err != nil {
				log.Fatalf("%s: %v", name, err)
			}
			layer = append(layer, id)
		}
		if len(layer) > 0 {
			layers = append(layers, layer)
		}
	}
	if len(layers) == 0 {
		return fallback
	}
	return layers
}

func emit(prefix string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s %s\n", prefix, data)
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatalf("%s: %v", name, err)
	}
	return parsed
}

func envBytes(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		log.Fatalf("%s: %v", name, err)
	}
	return parsed
}

func envSeconds(name string, fallback int) time.Duration {
	return time.Duration(envInt(name, fallback)) * time.Second
}

func sortResults(results []result) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
}
