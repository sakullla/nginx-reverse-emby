package wireguard

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type PacketConn = net.PacketConn

type Runtime interface {
	DialContext(ctx context.Context, network string, address string) (net.Conn, error)
	ListenTCP(ctx context.Context, address string) (net.Listener, error)
	ListenUDP(ctx context.Context, address string) (PacketConn, error)
	Close() error
}

type Factory func(context.Context, Config) (Runtime, error)

type endpointResolver func(context.Context, string) ([]net.IP, error)

type ManagerOptions struct {
	Factory Factory
}

type Manager struct {
	mu       sync.Mutex
	factory  Factory
	runtimes map[int]*runtimeEntry
}

type runtimeEntry struct {
	fingerprint string
	runtime     Runtime
}

func NewManager(options ManagerOptions) *Manager {
	factory := options.Factory
	if factory == nil {
		factory = NewRuntime
	}
	return &Manager{
		factory:  factory,
		runtimes: make(map[int]*runtimeEntry),
	}
}

func (m *Manager) Apply(ctx context.Context, profiles []model.WireGuardProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := make(map[int]struct{}, len(profiles))
	for _, profile := range profiles {
		if !profile.Enabled {
			seen[profile.ID] = struct{}{}
			if existing, ok := m.runtimes[profile.ID]; ok {
				_ = existing.runtime.Close()
				delete(m.runtimes, profile.ID)
			}
			continue
		}

		cfg, err := NormalizeConfig(profile)
		if err != nil {
			return fmt.Errorf("wireguard profile %d: %w", profile.ID, err)
		}
		fingerprint, err := Fingerprint(profile)
		if err != nil {
			return fmt.Errorf("wireguard profile %d fingerprint: %w", profile.ID, err)
		}
		seen[profile.ID] = struct{}{}

		if existing, ok := m.runtimes[profile.ID]; ok && existing.fingerprint == fingerprint {
			continue
		}

		if existing, ok := m.runtimes[profile.ID]; ok {
			_ = existing.runtime.Close()
			delete(m.runtimes, profile.ID)
		}
		runtime, err := m.factory(ctx, cfg)
		if err != nil {
			return fmt.Errorf("wireguard profile %d runtime: %w", profile.ID, err)
		}
		m.runtimes[profile.ID] = &runtimeEntry{fingerprint: fingerprint, runtime: runtime}
	}

	for profileID, existing := range m.runtimes {
		if _, ok := seen[profileID]; ok {
			continue
		}
		_ = existing.runtime.Close()
		delete(m.runtimes, profileID)
	}

	return nil
}

func (m *Manager) Runtime(profileID int) (Runtime, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.runtimes[profileID]
	if !ok {
		return nil, false
	}
	return entry.runtime, true
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for profileID, existing := range m.runtimes {
		if err := existing.runtime.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.runtimes, profileID)
	}
	return firstErr
}

type netstackRuntime struct {
	mu     sync.Mutex
	net    *netstack.Net
	device *device.Device
	tun    interface{ Close() error }
	closed bool
}

func NewRuntime(ctx context.Context, cfg Config) (Runtime, error) {
	tunDevice, tnet, err := netstack.CreateNetTUN(cfg.AddressAddrs, cfg.DNSAddrs, cfg.MTU)
	if err != nil {
		return nil, err
	}

	dev := device.NewDevice(tunDevice, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, "wireguard: "))
	runtime := &netstackRuntime{net: tnet, device: dev, tun: tunDevice}
	ipc, err := ipcConfig(ctx, cfg, lookupEndpointIP)
	if err != nil {
		runtime.Close()
		return nil, err
	}
	if err := dev.IpcSet(ipc); err != nil {
		runtime.Close()
		return nil, err
	}
	if err := dev.Up(); err != nil {
		runtime.Close()
		return nil, err
	}
	return runtime, nil
}

func (r *netstackRuntime) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	return r.net.DialContext(ctx, network, address)
}

func (r *netstackRuntime) ListenTCP(ctx context.Context, address string) (net.Listener, error) {
	_ = ctx

	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err
	}
	return r.net.ListenTCP(addr)
}

func (r *netstackRuntime) ListenUDP(ctx context.Context, address string) (PacketConn, error) {
	_ = ctx

	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	return r.net.ListenUDP(addr)
}

func (r *netstackRuntime) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	dev := r.device
	tunDevice := r.tun
	r.device = nil
	r.tun = nil
	r.mu.Unlock()

	if dev != nil {
		dev.Close()
		return nil
	}
	if tunDevice == nil {
		return nil
	}
	return tunDevice.Close()
}

func ipcConfig(ctx context.Context, cfg Config, resolve endpointResolver) (string, error) {
	var builder strings.Builder
	builder.WriteString("private_key=")
	builder.WriteString(hex.EncodeToString(cfg.PrivateKeyBytes))
	builder.WriteByte('\n')
	if cfg.ListenPort > 0 {
		fmt.Fprintf(&builder, "listen_port=%d\n", cfg.ListenPort)
	}
	builder.WriteString("replace_peers=true\n")

	for _, peer := range cfg.Peers {
		builder.WriteString("public_key=")
		builder.WriteString(hex.EncodeToString(peer.PublicKeyBytes))
		builder.WriteByte('\n')
		if len(peer.PresharedKeyBytes) > 0 {
			builder.WriteString("preshared_key=")
			builder.WriteString(hex.EncodeToString(peer.PresharedKeyBytes))
			builder.WriteByte('\n')
		}
		endpoint, err := ipcEndpoint(ctx, peer, resolve)
		if err != nil {
			return "", err
		}
		if endpoint != "" {
			builder.WriteString("endpoint=")
			builder.WriteString(endpoint)
			builder.WriteByte('\n')
		}
		if peer.PersistentKeepaliveSeconds > 0 {
			fmt.Fprintf(&builder, "persistent_keepalive_interval=%d\n", peer.PersistentKeepaliveSeconds)
		}
		builder.WriteString("replace_allowed_ips=true\n")
		for _, allowed := range peer.AllowedPrefixes {
			builder.WriteString("allowed_ip=")
			builder.WriteString(allowed.String())
			builder.WriteByte('\n')
		}
	}
	builder.WriteByte('\n')
	return builder.String(), nil
}

func ipcEndpoint(ctx context.Context, peer PeerConfig, resolve endpointResolver) (string, error) {
	host := strings.TrimSpace(peer.EndpointHost)
	if host == "" {
		return "", nil
	}
	port := strconv.Itoa(int(peer.EndpointPort))
	if addr, err := netip.ParseAddr(host); err == nil && addr.IsValid() {
		return net.JoinHostPort(addr.String(), port), nil
	}
	ips, err := resolve(ctx, host)
	if err != nil {
		return "", fmt.Errorf("resolve endpoint %s: %w", host, err)
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok || !addr.IsValid() {
			continue
		}
		return net.JoinHostPort(addr.Unmap().String(), port), nil
	}
	return "", fmt.Errorf("resolve endpoint %s: no IP addresses returned", host)
}

func lookupEndpointIP(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}
