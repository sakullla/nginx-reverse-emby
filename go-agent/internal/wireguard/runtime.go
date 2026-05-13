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

type Preflight func(context.Context, Config) error

type endpointResolver func(context.Context, string) ([]net.IP, error)

type ManagerOptions struct {
	Factory   Factory
	Preflight Preflight
}

type Manager struct {
	mu        sync.Mutex
	factory   Factory
	preflight Preflight
	runtimes  map[int]*runtimeEntry
}

type Transaction struct {
	mu          sync.Mutex
	manager     *Manager
	candidates  map[int]*runtimeEntry
	newRuntimes []Runtime
	committed   bool
	rolledBack  bool
}

type runtimeEntry struct {
	fingerprint string
	config      Config
	runtime     Runtime
}

func NewManager(options ManagerOptions) *Manager {
	factory := options.Factory
	if factory == nil {
		factory = NewRuntime
	}
	preflight := options.Preflight
	if preflight == nil {
		preflight = PreflightConfig
	}
	return &Manager{
		factory:   factory,
		preflight: preflight,
		runtimes:  make(map[int]*runtimeEntry),
	}
}

func (m *Manager) Apply(ctx context.Context, profiles []model.WireGuardProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := make(map[int]struct{}, len(profiles))
	var replacements []runtimeReplacement
	for _, profile := range profiles {
		seen[profile.ID] = struct{}{}
		if !profile.Enabled {
			continue
		}

		cfg, err := NormalizeConfig(profile)
		if err != nil {
			closeReplacementRuntimes(replacements)
			return fmt.Errorf("wireguard profile %d: %w", profile.ID, err)
		}
		fingerprint, err := Fingerprint(profile)
		if err != nil {
			closeReplacementRuntimes(replacements)
			return fmt.Errorf("wireguard profile %d fingerprint: %w", profile.ID, err)
		}

		if existing, ok := m.runtimes[profile.ID]; ok && existing.fingerprint == fingerprint {
			continue
		}

		if existing, ok := m.runtimes[profile.ID]; ok {
			runtime, err := m.factory(ctx, cfg)
			if err == nil {
				replacements = append(replacements, runtimeReplacement{
					profileID:   profile.ID,
					fingerprint: fingerprint,
					config:      cloneConfig(cfg),
					runtime:     runtime,
					existing:    existing,
				})
				continue
			}
			if !sameListenPort(existing.config.ListenPort, cfg.ListenPort) || !isListenPortConflict(err) {
				closeReplacementRuntimes(replacements)
				return fmt.Errorf("wireguard profile %d runtime: %w", profile.ID, err)
			}
			if preflightErr := m.preflight(ctx, cfg); preflightErr != nil {
				closeReplacementRuntimes(replacements)
				return fmt.Errorf("wireguard profile %d preflight: %w", profile.ID, preflightErr)
			}
			replacements = append(replacements, runtimeReplacement{
				profileID:          profile.ID,
				fingerprint:        fingerprint,
				config:             cloneConfig(cfg),
				existing:           existing,
				requiresCloseFirst: true,
			})
			continue
		}
		runtime, err := m.factory(ctx, cfg)
		if err != nil {
			closeReplacementRuntimes(replacements)
			return fmt.Errorf("wireguard profile %d runtime: %w", profile.ID, err)
		}
		replacements = append(replacements, runtimeReplacement{
			profileID:   profile.ID,
			fingerprint: fingerprint,
			config:      cloneConfig(cfg),
			runtime:     runtime,
		})
	}

	var closeFirstApplied []runtimeReplacement
	for _, replacement := range replacements {
		if !replacement.requiresCloseFirst {
			continue
		}
		if replacement.existing != nil {
			_ = replacement.existing.runtime.Close()
			delete(m.runtimes, replacement.profileID)
		}
		runtime, err := m.factory(ctx, replacement.config)
		if err != nil {
			rollbackErr := m.rollbackCloseFirstReplacement(ctx, replacement)
			if appliedRollbackErr := m.rollbackCloseFirstReplacements(ctx, closeFirstApplied); rollbackErr == nil {
				rollbackErr = appliedRollbackErr
			}
			closePendingReplacementRuntimes(replacements)
			if rollbackErr != nil {
				return fmt.Errorf("wireguard profile %d runtime: %w; rollback failed: %v", replacement.profileID, err, rollbackErr)
			}
			return fmt.Errorf("wireguard profile %d runtime: %w", replacement.profileID, err)
		}
		replacement.runtime = runtime
		m.runtimes[replacement.profileID] = &runtimeEntry{
			fingerprint: replacement.fingerprint,
			config:      cloneConfig(replacement.config),
			runtime:     replacement.runtime,
		}
		closeFirstApplied = append(closeFirstApplied, replacement)
	}

	for _, replacement := range replacements {
		if replacement.requiresCloseFirst {
			continue
		}
		if replacement.existing != nil {
			_ = replacement.existing.runtime.Close()
		}
		m.runtimes[replacement.profileID] = &runtimeEntry{
			fingerprint: replacement.fingerprint,
			config:      cloneConfig(replacement.config),
			runtime:     replacement.runtime,
		}
	}

	for profileID, existing := range m.runtimes {
		if _, ok := seen[profileID]; ok {
			continue
		}
		_ = existing.runtime.Close()
		delete(m.runtimes, profileID)
	}
	for _, profile := range profiles {
		if profile.Enabled {
			continue
		}
		if existing, ok := m.runtimes[profile.ID]; ok {
			_ = existing.runtime.Close()
			delete(m.runtimes, profile.ID)
		}
	}

	return nil
}

func (m *Manager) Prepare(ctx context.Context, profiles []model.WireGuardProfile) (*Transaction, error) {
	if m == nil {
		return nil, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	candidates := make(map[int]*runtimeEntry, len(profiles))
	var newRuntimes []Runtime

	closeNewRuntimes := func() {
		for _, runtime := range newRuntimes {
			_ = runtime.Close()
		}
	}

	for _, profile := range profiles {
		if !profile.Enabled {
			continue
		}

		cfg, err := NormalizeConfig(profile)
		if err != nil {
			closeNewRuntimes()
			return nil, fmt.Errorf("wireguard profile %d: %w", profile.ID, err)
		}
		fingerprint, err := Fingerprint(profile)
		if err != nil {
			closeNewRuntimes()
			return nil, fmt.Errorf("wireguard profile %d fingerprint: %w", profile.ID, err)
		}

		if existing, ok := m.runtimes[profile.ID]; ok && existing.fingerprint == fingerprint {
			candidates[profile.ID] = &runtimeEntry{
				fingerprint: existing.fingerprint,
				config:      cloneConfig(existing.config),
				runtime:     existing.runtime,
			}
			continue
		}

		runtime, err := m.factory(ctx, cfg)
		if err != nil {
			closeNewRuntimes()
			return nil, fmt.Errorf("wireguard profile %d runtime: %w", profile.ID, err)
		}
		newRuntimes = append(newRuntimes, runtime)
		candidates[profile.ID] = &runtimeEntry{
			fingerprint: fingerprint,
			config:      cloneConfig(cfg),
			runtime:     runtime,
		}
	}

	return &Transaction{
		manager:     m,
		candidates:  candidates,
		newRuntimes: newRuntimes,
	}, nil
}

func (t *Transaction) Runtime(profileID int) (Runtime, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.rolledBack {
		return nil, false
	}
	entry, ok := t.candidates[profileID]
	if !ok {
		return nil, false
	}
	return entry.runtime, true
}

func (t *Transaction) Commit() {
	if t == nil || t.manager == nil {
		return
	}

	t.mu.Lock()
	if t.committed || t.rolledBack {
		t.mu.Unlock()
		return
	}
	t.committed = true
	candidates := cloneRuntimeEntries(t.candidates)
	t.mu.Unlock()

	t.manager.mu.Lock()
	defer t.manager.mu.Unlock()

	for profileID, existing := range t.manager.runtimes {
		candidate, ok := candidates[profileID]
		if ok && candidate.runtime == existing.runtime {
			continue
		}
		_ = existing.runtime.Close()
	}
	t.manager.runtimes = candidates
}

func (t *Transaction) Rollback() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.committed || t.rolledBack {
		t.mu.Unlock()
		return
	}
	t.rolledBack = true
	newRuntimes := append([]Runtime(nil), t.newRuntimes...)
	t.mu.Unlock()

	for _, runtime := range newRuntimes {
		_ = runtime.Close()
	}
}

type runtimeReplacement struct {
	profileID          int
	fingerprint        string
	config             Config
	runtime            Runtime
	existing           *runtimeEntry
	requiresCloseFirst bool
}

func PreflightConfig(ctx context.Context, cfg Config) error {
	_, err := ipcConfig(ctx, cfg, lookupEndpointIP)
	return err
}

func sameListenPort(existingPort, nextPort int) bool {
	return existingPort > 0 && existingPort == nextPort
}

func isListenPortConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address") ||
		strings.Contains(message, "an attempt was made to access a socket") ||
		strings.Contains(message, "eaddrinuse")
}

func closeReplacementRuntimes(replacements []runtimeReplacement) {
	for _, replacement := range replacements {
		if replacement.runtime != nil {
			_ = replacement.runtime.Close()
		}
	}
}

func closePendingReplacementRuntimes(replacements []runtimeReplacement) {
	for _, replacement := range replacements {
		if replacement.requiresCloseFirst || replacement.runtime == nil {
			continue
		}
		_ = replacement.runtime.Close()
	}
}

func (m *Manager) rollbackCloseFirstReplacements(ctx context.Context, replacements []runtimeReplacement) error {
	for i := len(replacements) - 1; i >= 0; i-- {
		if err := m.rollbackCloseFirstReplacement(ctx, replacements[i]); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) rollbackCloseFirstReplacement(ctx context.Context, replacement runtimeReplacement) error {
	if replacement.runtime != nil {
		_ = replacement.runtime.Close()
	}
	if replacement.existing == nil {
		delete(m.runtimes, replacement.profileID)
		return nil
	}
	rollbackRuntime, err := m.factory(ctx, replacement.existing.config)
	if err != nil {
		delete(m.runtimes, replacement.profileID)
		return err
	}
	m.runtimes[replacement.profileID] = &runtimeEntry{
		fingerprint: replacement.existing.fingerprint,
		config:      cloneConfig(replacement.existing.config),
		runtime:     rollbackRuntime,
	}
	return nil
}

func cloneConfig(cfg Config) Config {
	cloned := cfg
	cloned.Addresses = append([]string(nil), cfg.Addresses...)
	cloned.DNS = append([]string(nil), cfg.DNS...)
	cloned.Peers = clonePeerConfigs(cfg.Peers)
	cloned.Tags = append([]string(nil), cfg.Tags...)
	cloned.PrivateKeyBytes = append([]byte(nil), cfg.PrivateKeyBytes...)
	cloned.AddressPrefixes = append([]netip.Prefix(nil), cfg.AddressPrefixes...)
	cloned.AddressAddrs = append([]netip.Addr(nil), cfg.AddressAddrs...)
	cloned.DNSAddrs = append([]netip.Addr(nil), cfg.DNSAddrs...)
	return cloned
}

func cloneRuntimeEntries(entries map[int]*runtimeEntry) map[int]*runtimeEntry {
	cloned := make(map[int]*runtimeEntry, len(entries))
	for profileID, entry := range entries {
		if entry == nil {
			continue
		}
		cloned[profileID] = &runtimeEntry{
			fingerprint: entry.fingerprint,
			config:      cloneConfig(entry.config),
			runtime:     entry.runtime,
		}
	}
	return cloned
}

func clonePeerConfigs(peers []PeerConfig) []PeerConfig {
	if len(peers) == 0 {
		return nil
	}
	cloned := make([]PeerConfig, len(peers))
	for i, peer := range peers {
		cloned[i] = peer
		cloned[i].AllowedIPs = append([]string(nil), peer.AllowedIPs...)
		cloned[i].PublicKeyBytes = append([]byte(nil), peer.PublicKeyBytes...)
		cloned[i].PresharedKeyBytes = append([]byte(nil), peer.PresharedKeyBytes...)
		cloned[i].AllowedPrefixes = append([]netip.Prefix(nil), peer.AllowedPrefixes...)
	}
	return cloned
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
