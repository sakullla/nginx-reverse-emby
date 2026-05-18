package wireguard

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

type PacketConn = net.PacketConn

type TransparentUDPPacket struct {
	Peer        *net.UDPAddr
	OriginalDst string
	Payload     []byte
}

type TransparentUDPConn interface {
	io.Closer
	LocalAddr() net.Addr
	ReadPacket() (TransparentUDPPacket, error)
	WritePacket(payload []byte, peer *net.UDPAddr) error
}

type Runtime interface {
	DialContext(ctx context.Context, network string, address string) (net.Conn, error)
	ListenTCP(ctx context.Context, address string) (net.Listener, error)
	ListenUDP(ctx context.Context, address string) (PacketConn, error)
	ListenTransparentUDP(ctx context.Context, address string) (TransparentUDPConn, error)
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
	runtimes  map[runtimeKey]*runtimeEntry
}

type Transaction struct {
	mu                     sync.Mutex
	manager                *Manager
	candidates             map[runtimeKey]*runtimeEntry
	newRuntimes            []Runtime
	closeFirstReplacements []runtimeReplacement
	committed              bool
	rolledBack             bool
}

type runtimeKey struct {
	agentID   string
	profileID int
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
		runtimes:  make(map[runtimeKey]*runtimeEntry),
	}
}

func (m *Manager) Apply(ctx context.Context, profiles []model.WireGuardProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	seen := make(map[runtimeKey]struct{}, len(profiles))
	var replacements []runtimeReplacement
	for _, profile := range profiles {
		key := keyForProfile(profile)
		seen[key] = struct{}{}
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

		if existing, ok := m.runtimes[key]; ok && existing.fingerprint == fingerprint {
			continue
		}

		if existing, ok := m.runtimes[key]; ok {
			runtime, err := m.factory(ctx, cfg)
			if err == nil {
				replacements = append(replacements, runtimeReplacement{
					key:         key,
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
				key:                key,
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
			key:         key,
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
			delete(m.runtimes, replacement.key)
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
		m.runtimes[replacement.key] = &runtimeEntry{
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
		m.runtimes[replacement.key] = &runtimeEntry{
			fingerprint: replacement.fingerprint,
			config:      cloneConfig(replacement.config),
			runtime:     replacement.runtime,
		}
	}

	for key, existing := range m.runtimes {
		if _, ok := seen[key]; ok {
			continue
		}
		_ = existing.runtime.Close()
		delete(m.runtimes, key)
	}
	for _, profile := range profiles {
		if profile.Enabled {
			continue
		}
		key := keyForProfile(profile)
		if existing, ok := m.runtimes[key]; ok {
			_ = existing.runtime.Close()
			delete(m.runtimes, key)
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

	candidates := make(map[runtimeKey]*runtimeEntry, len(profiles))
	var newRuntimes []Runtime
	var closeFirstReplacements []runtimeReplacement

	closeNewRuntimes := func() {
		for _, runtime := range newRuntimes {
			_ = runtime.Close()
		}
	}
	rollbackPrepared := func() error {
		closeNewRuntimes()
		return m.rollbackCloseFirstReplacements(ctx, closeFirstReplacements)
	}
	wrapPrepareError := func(profileID int, stage string, err error) error {
		if rollbackErr := rollbackPrepared(); rollbackErr != nil {
			return fmt.Errorf("wireguard profile %d %s: %w; rollback failed: %v", profileID, stage, err, rollbackErr)
		}
		return fmt.Errorf("wireguard profile %d %s: %w", profileID, stage, err)
	}

	for _, profile := range profiles {
		if !profile.Enabled {
			continue
		}
		key := keyForProfile(profile)

		cfg, err := NormalizeConfig(profile)
		if err != nil {
			if rollbackErr := rollbackPrepared(); rollbackErr != nil {
				return nil, fmt.Errorf("wireguard profile %d: %w; rollback failed: %v", profile.ID, err, rollbackErr)
			}
			return nil, fmt.Errorf("wireguard profile %d: %w", profile.ID, err)
		}
		fingerprint, err := Fingerprint(profile)
		if err != nil {
			return nil, wrapPrepareError(profile.ID, "fingerprint", err)
		}

		if existing, ok := m.runtimes[key]; ok && existing.fingerprint == fingerprint {
			candidates[key] = &runtimeEntry{
				fingerprint: existing.fingerprint,
				config:      cloneConfig(existing.config),
				runtime:     existing.runtime,
			}
			continue
		}

		existing, hasExisting := m.runtimes[key]
		runtime, err := m.factory(ctx, cfg)
		if err != nil {
			if !hasExisting || !sameListenPort(existing.config.ListenPort, cfg.ListenPort) || !isListenPortConflict(err) {
				return nil, wrapPrepareError(profile.ID, "runtime", err)
			}
			if preflightErr := m.preflight(ctx, cfg); preflightErr != nil {
				return nil, wrapPrepareError(profile.ID, "preflight", preflightErr)
			}
			_ = existing.runtime.Close()
			delete(m.runtimes, key)
			runtime, err = m.factory(ctx, cfg)
			if err != nil {
				rollbackErr := m.rollbackCloseFirstReplacement(ctx, runtimeReplacement{
					key:       key,
					profileID: profile.ID,
					config:    cloneConfig(cfg),
					existing:  existing,
				})
				if appliedRollbackErr := rollbackPrepared(); rollbackErr == nil {
					rollbackErr = appliedRollbackErr
				}
				if rollbackErr != nil {
					return nil, fmt.Errorf("wireguard profile %d runtime: %w; rollback failed: %v", profile.ID, err, rollbackErr)
				}
				return nil, fmt.Errorf("wireguard profile %d runtime: %w", profile.ID, err)
			}
			replacement := runtimeReplacement{
				key:                key,
				profileID:          profile.ID,
				fingerprint:        fingerprint,
				config:             cloneConfig(cfg),
				runtime:            runtime,
				existing:           existing,
				requiresCloseFirst: true,
			}
			closeFirstReplacements = append(closeFirstReplacements, replacement)
			candidates[key] = &runtimeEntry{
				fingerprint: fingerprint,
				config:      cloneConfig(cfg),
				runtime:     runtime,
			}
			m.runtimes[key] = &runtimeEntry{
				fingerprint: fingerprint,
				config:      cloneConfig(cfg),
				runtime:     runtime,
			}
			continue
		}
		newRuntimes = append(newRuntimes, runtime)
		candidates[key] = &runtimeEntry{
			fingerprint: fingerprint,
			config:      cloneConfig(cfg),
			runtime:     runtime,
		}
	}

	return &Transaction{
		manager:                m,
		candidates:             candidates,
		newRuntimes:            newRuntimes,
		closeFirstReplacements: closeFirstReplacements,
	}, nil
}

func (t *Transaction) Runtime(profileID int) (Runtime, bool) {
	return t.RuntimeForAgent("", profileID)
}

func (t *Transaction) RuntimeForAgent(agentID string, profileID int) (Runtime, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.rolledBack {
		return nil, false
	}
	entry, ok := runtimeEntryByProfile(t.candidates, agentID, profileID)
	if !ok {
		return nil, false
	}
	return entry.runtime, true
}

func (t *Transaction) HasCloseFirstReplacements() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.committed && !t.rolledBack && len(t.closeFirstReplacements) > 0
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
	closeFirstReplacements := append([]runtimeReplacement(nil), t.closeFirstReplacements...)
	manager := t.manager
	t.mu.Unlock()

	for _, runtime := range newRuntimes {
		_ = runtime.Close()
	}
	if manager != nil {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		_ = manager.rollbackCloseFirstReplacements(context.Background(), closeFirstReplacements)
	}
}

type runtimeReplacement struct {
	key                runtimeKey
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
		delete(m.runtimes, replacement.key)
		return nil
	}
	rollbackRuntime, err := m.factory(ctx, replacement.existing.config)
	if err != nil {
		delete(m.runtimes, replacement.key)
		return err
	}
	m.runtimes[replacement.key] = &runtimeEntry{
		fingerprint: replacement.existing.fingerprint,
		config:      cloneConfig(replacement.existing.config),
		runtime:     rollbackRuntime,
	}
	return nil
}

func keyForProfile(profile model.WireGuardProfile) runtimeKey {
	return runtimeKey{
		agentID:   strings.TrimSpace(profile.AgentID),
		profileID: profile.ID,
	}
}

func runtimeEntryByProfile(entries map[runtimeKey]*runtimeEntry, agentID string, profileID int) (*runtimeEntry, bool) {
	key := runtimeKey{agentID: strings.TrimSpace(agentID), profileID: profileID}
	if key.agentID != "" {
		entry, ok := entries[key]
		return entry, ok
	}
	var found *runtimeEntry
	for candidateKey, entry := range entries {
		if candidateKey.profileID != profileID {
			continue
		}
		if found != nil {
			return nil, false
		}
		found = entry
	}
	return found, found != nil
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

func cloneRuntimeEntries(entries map[runtimeKey]*runtimeEntry) map[runtimeKey]*runtimeEntry {
	cloned := make(map[runtimeKey]*runtimeEntry, len(entries))
	for key, entry := range entries {
		if entry == nil {
			continue
		}
		cloned[key] = &runtimeEntry{
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
	return m.RuntimeForAgent("", profileID)
}

func (m *Manager) RuntimeForAgent(agentID string, profileID int) (Runtime, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := runtimeEntryByProfile(m.runtimes, agentID, profileID)
	if !ok {
		return nil, false
	}
	return entry.runtime, true
}

func (m *Manager) Recreate(ctx context.Context, profiles []model.WireGuardProfile) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	for _, profile := range profiles {
		key := keyForProfile(profile)
		if existing, ok := m.runtimes[key]; ok {
			_ = existing.runtime.Close()
			delete(m.runtimes, key)
		}
	}
	m.mu.Unlock()
	return m.Apply(ctx, profiles)
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for key, existing := range m.runtimes {
		if err := existing.runtime.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.runtimes, key)
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

func (r *netstackRuntime) ListenTransparentUDP(ctx context.Context, address string) (TransparentUDPConn, error) {
	_ = ctx

	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	fullAddr, netProto, err := udpFullAddress(addr)
	if err != nil {
		return nil, err
	}
	netstackStack := stackFromNetstackNet(r.net)
	if netstackStack == nil {
		return nil, fmt.Errorf("wireguard netstack is unavailable")
	}

	var wq waiter.Queue
	ep, tcpipErr := netstackStack.NewEndpoint(udp.ProtocolNumber, netProto, &wq)
	if tcpipErr != nil {
		return nil, errors.New(tcpipErr.String())
	}
	ep.SocketOptions().SetReceiveOriginalDstAddress(true)
	if tcpipErr := ep.Bind(fullAddr); tcpipErr != nil {
		ep.Close()
		return nil, &net.OpError{
			Op:   "bind",
			Net:  "udp",
			Addr: udpAddrFromFullAddress(fullAddr),
			Err:  errors.New(tcpipErr.String()),
		}
	}
	return &netstackTransparentUDPConn{ep: ep, wq: &wq}, nil
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

type netstackNetLayout struct {
	ep    *channel.Endpoint
	stack *stack.Stack
}

func stackFromNetstackNet(net *netstack.Net) *stack.Stack {
	if net == nil {
		return nil
	}
	return (*netstackNetLayout)(unsafe.Pointer(net)).stack
}

type netstackTransparentUDPConn struct {
	ep tcpip.Endpoint
	wq *waiter.Queue
}

func (c *netstackTransparentUDPConn) Close() error {
	c.ep.Close()
	return nil
}

func (c *netstackTransparentUDPConn) LocalAddr() net.Addr {
	addr, err := c.ep.GetLocalAddress()
	if err != nil {
		return nil
	}
	return udpAddrFromFullAddress(addr)
}

func (c *netstackTransparentUDPConn) ReadPacket() (TransparentUDPPacket, error) {
	payload := make([]byte, 64*1024)
	writer := tcpip.SliceWriter(payload)
	res, err := c.read(&writer, tcpip.ReadOptions{NeedRemoteAddr: true})
	if err != nil {
		return TransparentUDPPacket{}, err
	}
	originalDst := ""
	if res.ControlMessages.HasOriginalDstAddress {
		originalDst = udpAddrFromFullAddress(res.ControlMessages.OriginalDstAddress).String()
	}
	return TransparentUDPPacket{
		Peer:        udpAddrFromFullAddress(res.RemoteAddr),
		OriginalDst: originalDst,
		Payload:     append([]byte(nil), payload[:res.Count]...),
	}, nil
}

func (c *netstackTransparentUDPConn) WritePacket(payload []byte, peer *net.UDPAddr) error {
	var opts tcpip.WriteOptions
	if peer != nil {
		addr, _, err := udpFullAddress(peer)
		if err != nil {
			return err
		}
		opts.To = &addr
	}
	reader := bytes.NewReader(payload)
	for {
		_, tcpipErr := c.ep.Write(reader, opts)
		if tcpipErr == nil {
			return nil
		}
		if _, ok := tcpipErr.(*tcpip.ErrWouldBlock); !ok {
			return errors.New(tcpipErr.String())
		}
		entry, notifyCh := waiter.NewChannelEntry(waiter.WritableEvents)
		c.wq.EventRegister(&entry)
		select {
		case <-notifyCh:
		}
		c.wq.EventUnregister(&entry)
	}
}

func (c *netstackTransparentUDPConn) read(dst io.Writer, opts tcpip.ReadOptions) (tcpip.ReadResult, error) {
	for {
		res, tcpipErr := c.ep.Read(dst, opts)
		if tcpipErr == nil {
			return res, nil
		}
		if _, ok := tcpipErr.(*tcpip.ErrClosedForReceive); ok {
			return tcpip.ReadResult{}, io.EOF
		}
		if _, ok := tcpipErr.(*tcpip.ErrWouldBlock); !ok {
			return tcpip.ReadResult{}, errors.New(tcpipErr.String())
		}
		entry, notifyCh := waiter.NewChannelEntry(waiter.ReadableEvents)
		c.wq.EventRegister(&entry)
		select {
		case <-notifyCh:
		}
		c.wq.EventUnregister(&entry)
	}
}

func udpFullAddress(addr *net.UDPAddr) (tcpip.FullAddress, tcpip.NetworkProtocolNumber, error) {
	if addr == nil {
		return tcpip.FullAddress{}, ipv4.ProtocolNumber, nil
	}
	if addr.Port < 0 || addr.Port > 65535 {
		return tcpip.FullAddress{}, 0, fmt.Errorf("udp port out of range: %d", addr.Port)
	}
	out := tcpip.FullAddress{Port: uint16(addr.Port)}
	if len(addr.IP) == 0 || addr.IP.IsUnspecified() {
		if addr.IP != nil && addr.IP.To4() == nil && addr.IP.To16() != nil {
			return out, ipv6.ProtocolNumber, nil
		}
		return out, ipv4.ProtocolNumber, nil
	}
	ip, ok := netip.AddrFromSlice(addr.IP)
	if !ok || !ip.IsValid() {
		return tcpip.FullAddress{}, 0, fmt.Errorf("invalid udp address %q", addr.IP.String())
	}
	ip = ip.Unmap()
	out.Addr = tcpip.AddrFromSlice(ip.AsSlice())
	if ip.Is4() {
		return out, ipv4.ProtocolNumber, nil
	}
	return out, ipv6.ProtocolNumber, nil
}

func udpAddrFromFullAddress(addr tcpip.FullAddress) *net.UDPAddr {
	return &net.UDPAddr{IP: net.IP(addr.Addr.AsSlice()), Port: int(addr.Port)}
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
