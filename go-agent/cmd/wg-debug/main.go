package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

func main() {
	tcpTargets := flag.String("tcp", "", "comma-separated TCP targets inside the WireGuard tunnel")
	httpTarget := flag.String("http", "", "HTTP/HTTPS URL to fetch through the WireGuard tunnel")
	dnsServers := flag.String("dns", "", "comma-separated DNS servers to use inside the WireGuard tunnel")
	udpDelay := flag.Duration("udp-delay", 0, "artificial one-way UDP delay before the WireGuard endpoint")
	resolveSystemDNS := flag.Bool("resolve-system-dns", false, "resolve HTTP target host with system DNS before dialing through WireGuard")
	warmupTCP := flag.String("warmup-tcp", "", "comma-separated TCP targets to connect before measurements")
	warmupHTTPTCP := flag.Bool("warmup-http-tcp", false, "connect to the HTTP target host:port once before HTTP measurements")
	warmupCount := flag.Int("warmup-count", 1, "TCP warmup attempts per warmup target")
	count := flag.Int("count", 3, "TCP connect attempts per target")
	timeout := flag.Duration("timeout", 10*time.Second, "per-attempt timeout")
	flag.Parse()

	raw := strings.TrimSpace(os.Getenv("WG_DEBUG_URL"))
	if raw == "" {
		fmt.Fprintln(os.Stderr, "WG_DEBUG_URL is required")
		os.Exit(2)
	}

	profile, err := parseWireGuardURI(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse WireGuard URI: %v\n", err)
		os.Exit(2)
	}
	if overrideDNS := splitCSV(*dnsServers); len(overrideDNS) > 0 {
		profile.DNS = overrideDNS
	}
	if *udpDelay > 0 {
		proxy, err := startUDPDelayProxy(profile.Peers[0].Endpoint, *udpDelay)
		if err != nil {
			fmt.Fprintf(os.Stderr, "start UDP delay proxy: %v\n", err)
			os.Exit(1)
		}
		defer proxy.close()
		rewritePeerEndpointForUDPProxy(&profile, proxy.localAddr())
		fmt.Printf("udp-delay-proxy listen=%s target=%s one_way=%s\n", proxy.localAddr(), proxy.target, *udpDelay)
	}
	cfg, err := wireguard.NormalizeConfig(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "normalize WireGuard config: %v\n", err)
		os.Exit(2)
	}

	ctx := context.Background()
	rt, err := wireguard.NewRuntime(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start userspace WireGuard runtime: %v\n", err)
		os.Exit(1)
	}
	defer rt.Close()

	fmt.Printf("wireguard name=%q endpoint=%s mtu=%d address=%s allowed=%s\n",
		profile.Name,
		redactedEndpoint(profile.Peers[0].Endpoint),
		profile.MTU,
		strings.Join(profile.Addresses, ","),
		strings.Join(profile.Peers[0].AllowedIPs, ","),
	)

	warmupTargets := splitCSV(*warmupTCP)
	if *warmupHTTPTCP && strings.TrimSpace(*httpTarget) != "" {
		target, err := httpWarmupTarget(strings.TrimSpace(*httpTarget))
		if err != nil {
			fmt.Fprintf(os.Stderr, "http warmup target: %v\n", err)
			os.Exit(2)
		}
		warmupTargets = append(warmupTargets, target)
	}
	for _, target := range warmupTargets {
		measureTCPWithLabel(ctx, rt, "warmup-tcp", target, *warmupCount, *timeout)
	}

	if strings.TrimSpace(*tcpTargets) == "" && strings.TrimSpace(*httpTarget) == "" {
		fmt.Println("runtime started; pass -tcp host:port or -http URL to measure through the tunnel")
		return
	}

	for _, target := range splitCSV(*tcpTargets) {
		measureTCP(ctx, rt, target, *count, *timeout)
	}
	if strings.TrimSpace(*httpTarget) != "" {
		measureHTTP(ctx, rt, strings.TrimSpace(*httpTarget), *count, *timeout, shouldResolveHTTPWithSystemDNS(*resolveSystemDNS, len(profile.DNS) > 0))
	}
}

func parseWireGuardURI(raw string) (model.WireGuardProfile, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return model.WireGuardProfile{}, err
	}
	if !strings.EqualFold(u.Scheme, "wireguard") {
		return model.WireGuardProfile{}, fmt.Errorf("scheme must be wireguard")
	}
	privateKey := ""
	if u.User != nil {
		privateKey = strings.TrimSpace(u.User.Username())
	}
	if privateKey == "" {
		return model.WireGuardProfile{}, fmt.Errorf("private key is required in URI userinfo")
	}
	endpoint := strings.TrimSpace(u.Host)
	if endpoint == "" {
		return model.WireGuardProfile{}, fmt.Errorf("endpoint host:port is required")
	}

	q := u.Query()
	publicKey := strings.TrimSpace(firstQuery(q, "publickey", "public-key", "public_key"))
	if publicKey == "" {
		return model.WireGuardProfile{}, fmt.Errorf("publickey query parameter is required")
	}
	addresses := splitCSV(firstQuery(q, "address", "addresses"))
	if len(addresses) == 0 {
		return model.WireGuardProfile{}, fmt.Errorf("address query parameter is required")
	}
	allowedIPs := splitCSV(firstQuery(q, "allowed-ips", "allowed_ips"))
	if len(allowedIPs) == 0 {
		allowedIPs = []string{"0.0.0.0/0", "::/0"}
	}
	dnsServers := splitCSV(firstQuery(q, "dns", "dns-server", "dns_servers"))
	mtu := 0
	if rawMTU := strings.TrimSpace(q.Get("mtu")); rawMTU != "" {
		parsed, err := strconv.Atoi(rawMTU)
		if err != nil {
			return model.WireGuardProfile{}, fmt.Errorf("mtu must be numeric: %w", err)
		}
		mtu = parsed
	}
	name := strings.TrimSpace(u.Fragment)
	if name == "" {
		name = "wg-debug"
	}

	return model.WireGuardProfile{
		Name:       name,
		Mode:       wireguard.ModeGenericWireGuard,
		PrivateKey: privateKey,
		Addresses:  addresses,
		DNS:        dnsServers,
		MTU:        mtu,
		Enabled:    true,
		Peers: []model.WireGuardPeer{{
			Name:         "peer",
			PublicKey:    publicKey,
			PresharedKey: firstQuery(q, "preshared-key", "preshared_key"),
			Endpoint:     endpoint,
			AllowedIPs:   allowedIPs,
		}},
	}, nil
}

func firstQuery(values url.Values, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(values.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func splitCSV(value string) []string {
	fields := strings.Split(value, ",")
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		trimmed := strings.TrimSpace(field)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func shouldResolveHTTPWithSystemDNS(resolveFlag bool, _ bool) bool {
	return resolveFlag
}

func httpWarmupTarget(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("HTTP URL host is required")
	}
	if port := u.Port(); port != "" {
		return net.JoinHostPort(u.Hostname(), port), nil
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return net.JoinHostPort(u.Hostname(), "80"), nil
	case "https":
		return net.JoinHostPort(u.Hostname(), "443"), nil
	default:
		return "", fmt.Errorf("unsupported HTTP URL scheme %q", u.Scheme)
	}
}

func redactedEndpoint(endpoint string) string {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint
	}
	return net.JoinHostPort(host, port)
}

func rewritePeerEndpointForUDPProxy(profile *model.WireGuardProfile, endpoint string) {
	if profile == nil || len(profile.Peers) == 0 {
		return
	}
	profile.Peers[0].Endpoint = endpoint
}

type udpDelayProxy struct {
	conn       *net.UDPConn
	clientAddr string
	target     string
	targetAddr *net.UDPAddr
	delay      time.Duration
	done       chan struct{}
}

func startUDPDelayProxy(target string, delay time.Duration) (*udpDelayProxy, error) {
	targetAddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	p := &udpDelayProxy{
		conn:       conn,
		clientAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		target:     target,
		targetAddr: targetAddr,
		delay:      delay,
		done:       make(chan struct{}),
	}
	go p.loop()
	return p, nil
}

func (p *udpDelayProxy) localAddr() string {
	return p.clientAddr
}

func (p *udpDelayProxy) close() {
	_ = p.conn.Close()
	<-p.done
}

func (p *udpDelayProxy) loop() {
	defer close(p.done)
	buf := make([]byte, 64*1024)
	var client *net.UDPAddr
	for {
		n, from, err := p.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		packet := append([]byte(nil), buf[:n]...)
		if from.IP.Equal(p.targetAddr.IP) && from.Port == p.targetAddr.Port {
			if client == nil {
				continue
			}
			go p.delayedWrite(packet, client)
			continue
		}
		client = from
		go p.delayedWrite(packet, p.targetAddr)
	}
}

func (p *udpDelayProxy) delayedWrite(packet []byte, dst *net.UDPAddr) {
	time.Sleep(p.delay)
	_, _ = p.conn.WriteToUDP(packet, dst)
}

func measureTCP(ctx context.Context, rt wireguard.Runtime, target string, count int, timeout time.Duration) {
	measureTCPWithLabel(ctx, rt, "tcp", target, count, timeout)
}

func measureTCPWithLabel(ctx context.Context, rt wireguard.Runtime, label string, target string, count int, timeout time.Duration) {
	for i := 0; i < count; i++ {
		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		conn, err := rt.DialContext(attemptCtx, "tcp", target)
		elapsed := time.Since(start)
		cancel()
		if err != nil {
			fmt.Printf("%s target=%s attempt=%d error=%v elapsed=%s\n", label, target, i+1, err, elapsed)
			continue
		}
		_ = conn.Close()
		fmt.Printf("%s target=%s attempt=%d connect=%s\n", label, target, i+1, elapsed)
	}
}

func measureHTTP(ctx context.Context, rt wireguard.Runtime, target string, count int, timeout time.Duration, resolveWithSystemDNS bool) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			trace := httptrace.ContextClientTrace(ctx)
			if trace != nil && trace.ConnectStart != nil {
				trace.ConnectStart(network, address)
			}
			start := time.Now()
			if resolveWithSystemDNS {
				resolved, err := resolveDialAddressWithSystemDNS(ctx, address)
				if err != nil {
					if trace != nil && trace.ConnectDone != nil {
						trace.ConnectDone(network, address, err)
					}
					return nil, err
				}
				address = resolved
			}
			conn, err := rt.DialContext(ctx, network, address)
			if trace != nil && trace.ConnectDone != nil {
				trace.ConnectDone(network, address, err)
			}
			if err == nil {
				fmt.Printf("http-dial network=%s address=%s elapsed=%s\n", network, address, time.Since(start))
			}
			return conn, err
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	for i := 0; i < count; i++ {
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			fmt.Printf("http url=%s attempt=%d error=%v elapsed=%s\n", target, i+1, err, time.Since(start))
			continue
		}
		trace := &httptrace.ClientTrace{
			GetConn: func(address string) {
				fmt.Printf("http-trace url=%s attempt=%d get_conn=%s elapsed=%s\n", target, i+1, address, time.Since(start))
			},
			GotConn: func(info httptrace.GotConnInfo) {
				fmt.Printf("http-trace url=%s attempt=%d got_conn reused=%t was_idle=%t elapsed=%s\n", target, i+1, info.Reused, info.WasIdle, time.Since(start))
			},
			ConnectStart: func(network, address string) {
				fmt.Printf("http-trace url=%s attempt=%d connect_start network=%s address=%s elapsed=%s\n", target, i+1, network, address, time.Since(start))
			},
			ConnectDone: func(network, address string, err error) {
				fmt.Printf("http-trace url=%s attempt=%d connect_done network=%s address=%s err=%v elapsed=%s\n", target, i+1, network, address, err, time.Since(start))
			},
			GotFirstResponseByte: func() {
				fmt.Printf("http-trace url=%s attempt=%d first_byte=%s\n", target, i+1, time.Since(start))
			},
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("http url=%s attempt=%d error=%v elapsed=%s\n", target, i+1, err, time.Since(start))
			continue
		}
		n, readErr := io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		elapsed := time.Since(start)
		fmt.Printf("http url=%s attempt=%d status=%d bytes=%d elapsed=%s mbps=%.2f\n",
			target,
			i+1,
			resp.StatusCode,
			n,
			elapsed,
			float64(n)*8/elapsed.Seconds()/1_000_000,
		)
		if readErr != nil {
			fmt.Printf("http url=%s attempt=%d read_error=%v\n", target, i+1, readErr)
		}
	}
}

func resolveDialAddressWithSystemDNS(ctx context.Context, address string) (string, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	if ip := net.ParseIP(host); ip != nil {
		return address, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if addr.IP == nil {
			continue
		}
		if addr.IP.To4() != nil {
			return dialAddressFromResolvedIP(address, addr.IP.String())
		}
	}
	if len(addrs) > 0 && addrs[0].IP != nil {
		return dialAddressFromResolvedIP(address, addrs[0].IP.String())
	}
	return "", fmt.Errorf("no system DNS address for %s", host)
}

func dialAddressFromResolvedIP(address string, ip string) (string, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(ip, port), nil
}
