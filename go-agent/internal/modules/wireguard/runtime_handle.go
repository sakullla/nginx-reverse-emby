package wireguard

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard/wgnetstack"
	"golang.zx2c4.com/wireguard/device"

	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
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
	WritePacket(payload []byte, peer *net.UDPAddr, source string) error
}

type RuntimeHandle interface {
	DialContext(ctx context.Context, network string, address string) (net.Conn, error)
	ListenTCP(ctx context.Context, address string) (net.Listener, error)
	ListenTransparentTCP(ctx context.Context) (net.Listener, error)
	ListenUDP(ctx context.Context, address string) (PacketConn, error)
	ListenTransparentUDP(ctx context.Context, address string) (TransparentUDPConn, error)
	Close() error
}

type endpointResolutionState interface {
	EndpointResolutionPending() bool
}

type Factory func(context.Context, Config) (RuntimeHandle, error)

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
	previous               map[runtimeKey]*runtimeEntry
	candidates             map[runtimeKey]*runtimeEntry
	newRuntimes            []RuntimeHandle
	closeFirstReplacements []runtimeReplacement
	committed              bool
	rolledBack             bool
	previousRestored       bool
}

type runtimeKey struct {
	agentID   string
	profileID int
}

type runtimeEntry struct {
	fingerprint string
	config      Config
	runtime     RuntimeHandle
}

func NewManager(options ManagerOptions) *Manager {
	factory := options.Factory
	if factory == nil {
		factory = NewRuntimeHandle
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

		if existing, ok := m.runtimes[key]; ok && existing.fingerprint == fingerprint && !runtimeEndpointResolutionPending(existing.runtime) {
			continue
		}

		if existing, ok := m.runtimes[key]; ok {
			handle, err := m.factory(ctx, cfg)
			if err == nil {
				replacements = append(replacements, runtimeReplacement{
					key:         key,
					profileID:   profile.ID,
					fingerprint: fingerprint,
					config:      cloneConfig(cfg),
					runtime:     handle,
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
		handle, err := m.factory(ctx, cfg)
		if err != nil {
			closeReplacementRuntimes(replacements)
			return fmt.Errorf("wireguard profile %d runtime: %w", profile.ID, err)
		}
		replacements = append(replacements, runtimeReplacement{
			key:         key,
			profileID:   profile.ID,
			fingerprint: fingerprint,
			config:      cloneConfig(cfg),
			runtime:     handle,
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
		handle, err := m.factory(ctx, replacement.config)
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
		replacement.runtime = handle
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
	previous := cloneRuntimeEntries(m.runtimes)
	var newRuntimes []RuntimeHandle
	var closeFirstReplacements []runtimeReplacement

	closeNewRuntimes := func() {
		for _, rt := range newRuntimes {
			_ = rt.Close()
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

		if existing, ok := m.runtimes[key]; ok && existing.fingerprint == fingerprint && !runtimeEndpointResolutionPending(existing.runtime) {
			candidates[key] = &runtimeEntry{
				fingerprint: existing.fingerprint,
				config:      cloneConfig(existing.config),
				runtime:     existing.runtime,
			}
			continue
		}

		existing, hasExisting := m.runtimes[key]
		handle, err := m.factory(ctx, cfg)
		if err != nil {
			if !hasExisting || !sameListenPort(existing.config.ListenPort, cfg.ListenPort) || !isListenPortConflict(err) {
				return nil, wrapPrepareError(profile.ID, "runtime", err)
			}
			if preflightErr := m.preflight(ctx, cfg); preflightErr != nil {
				return nil, wrapPrepareError(profile.ID, "preflight", preflightErr)
			}
			_ = existing.runtime.Close()
			delete(m.runtimes, key)
			handle, err = m.factory(ctx, cfg)
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
				runtime:            handle,
				existing:           existing,
				requiresCloseFirst: true,
			}
			closeFirstReplacements = append(closeFirstReplacements, replacement)
			candidates[key] = &runtimeEntry{
				fingerprint: fingerprint,
				config:      cloneConfig(cfg),
				runtime:     handle,
			}
			continue
		}
		newRuntimes = append(newRuntimes, handle)
		candidates[key] = &runtimeEntry{
			fingerprint: fingerprint,
			config:      cloneConfig(cfg),
			runtime:     handle,
		}
	}

	return &Transaction{
		manager:                m,
		previous:               previous,
		candidates:             candidates,
		newRuntimes:            newRuntimes,
		closeFirstReplacements: closeFirstReplacements,
	}, nil
}

func (t *Transaction) Runtime(profileID int) (RuntimeHandle, bool) {
	return t.RuntimeForAgent("", profileID)
}

func (t *Transaction) RuntimeForAgent(agentID string, profileID int) (RuntimeHandle, bool) {
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

func (t *Transaction) RestorePrevious(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.rolledBack {
		t.mu.Unlock()
		return nil
	}
	previous := cloneRuntimeEntries(t.previous)
	manager := t.manager
	t.mu.Unlock()
	if manager == nil {
		return nil
	}
	if err := manager.restoreRuntimeEntries(ctx, previous); err != nil {
		return err
	}
	t.mu.Lock()
	if !t.rolledBack {
		t.previousRestored = true
	}
	t.mu.Unlock()
	return nil
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

	for key, existing := range t.manager.runtimes {
		candidate, ok := candidates[key]
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
	if t.rolledBack {
		t.mu.Unlock()
		return
	}
	committed := t.committed
	previousRestored := t.previousRestored
	t.rolledBack = true
	previous := cloneRuntimeEntries(t.previous)
	candidates := cloneRuntimeEntries(t.candidates)
	newRuntimes := append([]RuntimeHandle(nil), t.newRuntimes...)
	closeFirstReplacements := append([]runtimeReplacement(nil), t.closeFirstReplacements...)
	manager := t.manager
	t.mu.Unlock()

	if committed {
		if manager != nil {
			if !previousRestored {
				_ = manager.restoreRuntimeEntries(context.Background(), previous)
			}
		}
		return
	}

	for _, rt := range newRuntimes {
		_ = rt.Close()
	}
	for _, candidate := range candidates {
		usedByCloseFirst := false
		for _, replacement := range closeFirstReplacements {
			if replacement.runtime == candidate.runtime {
				usedByCloseFirst = true
				break
			}
		}
		if usedByCloseFirst && !previousRestored {
			continue
		}
		_ = candidate.runtime.Close()
	}
	if manager != nil && !previousRestored {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		_ = manager.rollbackCloseFirstReplacements(context.Background(), closeFirstReplacements)
	}
}

func (m *Manager) restoreRuntimeEntries(ctx context.Context, previous map[runtimeKey]*runtimeEntry) error {
	if m == nil {
		return nil
	}
	restored := make(map[runtimeKey]*runtimeEntry, len(previous))
	for key, entry := range previous {
		if entry == nil {
			continue
		}
		runtime, err := m.factory(ctx, entry.config)
		if err != nil {
			for _, restoredEntry := range restored {
				_ = restoredEntry.runtime.Close()
			}
			return err
		}
		restored[key] = &runtimeEntry{
			fingerprint: entry.fingerprint,
			config:      cloneConfig(entry.config),
			runtime:     runtime,
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for key, current := range m.runtimes {
		if restoredEntry, ok := restored[key]; ok && restoredEntry.runtime == current.runtime {
			continue
		}
		_ = current.runtime.Close()
	}
	m.runtimes = restored
	return nil
}

type runtimeReplacement struct {
	key                runtimeKey
	profileID          int
	fingerprint        string
	config             Config
	runtime            RuntimeHandle
	existing           *runtimeEntry
	requiresCloseFirst bool
}

func PreflightConfig(ctx context.Context, cfg Config) error {
	_, _, err := ipcConfig(ctx, cfg, lookupEndpointIP)
	return err
}

func sameListenPort(existingPort, nextPort int) bool {
	return existingPort > 0 && existingPort == nextPort
}

func runtimeEndpointResolutionPending(runtime RuntimeHandle) bool {
	state, ok := runtime.(endpointResolutionState)
	return ok && state.EndpointResolutionPending()
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
	cloned.BindAddresses = slices.Clone(cfg.BindAddresses)
	cloned.Addresses = slices.Clone(cfg.Addresses)
	cloned.DNS = slices.Clone(cfg.DNS)
	cloned.Peers = clonePeerConfigs(cfg.Peers)
	cloned.Tags = slices.Clone(cfg.Tags)
	cloned.PrivateKeyBytes = slices.Clone(cfg.PrivateKeyBytes)
	cloned.AddressPrefixes = slices.Clone(cfg.AddressPrefixes)
	cloned.AddressAddrs = slices.Clone(cfg.AddressAddrs)
	cloned.DNSAddrs = slices.Clone(cfg.DNSAddrs)
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
	cloned := slices.Clone(peers)
	for i, peer := range peers {
		cloned[i].AllowedIPs = slices.Clone(peer.AllowedIPs)
		cloned[i].PublicKeyBytes = slices.Clone(peer.PublicKeyBytes)
		cloned[i].PresharedKeyBytes = slices.Clone(peer.PresharedKeyBytes)
		cloned[i].AllowedPrefixes = slices.Clone(peer.AllowedPrefixes)
	}
	return cloned
}

func (m *Manager) Runtime(profileID int) (RuntimeHandle, bool) {
	return m.RuntimeForAgent("", profileID)
}

func (m *Manager) RuntimeForAgent(agentID string, profileID int) (RuntimeHandle, bool) {
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
	mu                        sync.Mutex
	net                       wgnetstack.RuntimeNet
	stack                     *stack.Stack
	device                    *device.Device
	tun                       interface{ Close() error }
	tcp                       *transparentTCPDispatcher
	udp                       *transparentUDPDispatcher
	releaseScavenger          func()
	tcpHandlerInstalled       bool
	udpHandlerInstalled       bool
	endpointResolutionPending bool
	closed                    bool
}

func NewRuntimeHandle(ctx context.Context, cfg Config) (RuntimeHandle, error) {
	tunDevice, tnet, gstack, err := wgnetstack.CreateNetTUN(cfg.AddressAddrs, cfg.DNSAddrs, cfg.MTU)
	if err != nil {
		return nil, err
	}

	dev := device.NewDevice(tunDevice, newWireGuardBind(cfg.BindAddresses), device.NewLogger(device.LogLevelSilent, "wireguard: "))
	rt := newNetstackRuntime(tunDevice, tnet, gstack, dev)
	rt.releaseScavenger = retainWireGuardMemoryScavenger()
	ipc, endpointResolutionPending, err := ipcConfig(ctx, cfg, lookupEndpointIP)
	if err != nil {
		rt.Close()
		return nil, err
	}
	rt.endpointResolutionPending = endpointResolutionPending
	if err := dev.IpcSet(ipc); err != nil {
		rt.Close()
		return nil, err
	}
	if err := dev.Up(); err != nil {
		rt.Close()
		return nil, err
	}
	startWireGuardRuntimeWarmup(rt, cfg)
	return rt, nil
}

func newNetstackRuntime(tunDevice interface{ Close() error }, tnet wgnetstack.RuntimeNet, gstack *stack.Stack, dev *device.Device) *netstackRuntime {
	rt := &netstackRuntime{net: tnet, stack: gstack, device: dev, tun: tunDevice}
	if gstack != nil {
		rt.tcp = newTransparentTCPDispatcher(gstack)
		rt.udp = newTransparentUDPDispatcher(gstack)
	}
	return rt
}

func (r *netstackRuntime) EndpointResolutionPending() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.endpointResolutionPending
}

const wireGuardRuntimeWarmupTimeout = 2 * time.Second

func startWireGuardRuntimeWarmup(rt RuntimeHandle, cfg Config) {
	if len(wireGuardWarmupTargets(cfg)) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), wireGuardRuntimeWarmupTimeout)
	defer cancel()
	warmWireGuardRuntime(ctx, rt, cfg)
}

func warmWireGuardRuntime(ctx context.Context, rt RuntimeHandle, cfg Config) {
	for _, target := range wireGuardWarmupTargets(cfg) {
		conn, err := rt.DialContext(ctx, "udp", target)
		if err != nil {
			continue
		}
		_ = setConnDeadlineFromContext(conn, ctx)
		_, _ = conn.Write(wireGuardDNSWarmupQuery())
		var buf [512]byte
		_, _ = conn.Read(buf[:])
		_ = conn.Close()
		return
	}
}

func wireGuardDNSWarmupQuery() []byte {
	return []byte{
		0x12, 0x34, // ID
		0x01, 0x00, // recursion desired
		0x00, 0x01, // questions
		0x00, 0x00, // answers
		0x00, 0x00, // authority
		0x00, 0x00, // additional
		0x00,       // root name
		0x00, 0x02, // NS
		0x00, 0x01, // IN
	}
}

func setConnDeadlineFromContext(conn net.Conn, ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		return conn.SetDeadline(deadline)
	}
	return nil
}

func wireGuardWarmupTargets(cfg Config) []string {
	targets := make([]string, 0, len(cfg.DNSAddrs))
	for _, addr := range cfg.DNSAddrs {
		if !addr.IsValid() || addr.IsUnspecified() {
			continue
		}
		targets = append(targets, net.JoinHostPort(addr.String(), "53"))
	}
	return targets
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

func (r *netstackRuntime) ListenTransparentTCP(ctx context.Context) (net.Listener, error) {
	_ = ctx
	if r.tcp == nil {
		return nil, fmt.Errorf("wireguard transparent tcp dispatcher is unavailable")
	}
	r.installTransparentTCPHandler()
	return r.tcp.Listen(), nil
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
	if r.stack == nil {
		return nil, fmt.Errorf("wireguard netstack is unavailable")
	}
	if isWildcardUDPPort(addr) {
		if r.udp == nil {
			return nil, fmt.Errorf("wireguard transparent udp dispatcher is unavailable")
		}
		r.installTransparentUDPHandler()
		return r.udp.Listen(), nil
	}

	var wq waiter.Queue
	ep, tcpipErr := r.stack.NewEndpoint(udp.ProtocolNumber, netProto, &wq)
	if tcpipErr != nil {
		return nil, errors.New(tcpipErr.String())
	}
	ep.SocketOptions().SetReuseAddress(true)
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
	return &netstackTransparentUDPConn{stack: r.stack, ep: ep, wq: &wq}, nil
}

func (r *netstackRuntime) installTransparentTCPHandler() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tcpHandlerInstalled || r.stack == nil || r.tcp == nil {
		return
	}
	r.stack.SetTransportProtocolHandler(tcp.ProtocolNumber, r.tcp.HandlePacket)
	r.tcpHandlerInstalled = true
}

func (r *netstackRuntime) installTransparentUDPHandler() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.udpHandlerInstalled || r.stack == nil || r.udp == nil {
		return
	}
	r.stack.SetTransportProtocolHandler(udp.ProtocolNumber, r.udp.HandlePacket)
	r.udpHandlerInstalled = true
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
	r.stack = nil
	tcpDispatcher := r.tcp
	udpDispatcher := r.udp
	releaseScavenger := r.releaseScavenger
	r.tcp = nil
	r.udp = nil
	r.releaseScavenger = nil
	r.mu.Unlock()

	if releaseScavenger != nil {
		releaseScavenger()
	}
	if tcpDispatcher != nil {
		tcpDispatcher.Close()
	}
	if udpDispatcher != nil {
		udpDispatcher.Close()
	}
	if dev != nil {
		dev.Close()
		return nil
	}
	if tunDevice == nil {
		return nil
	}
	return tunDevice.Close()
}

func ipcConfig(ctx context.Context, cfg Config, resolve endpointResolver) (string, bool, error) {
	var builder strings.Builder
	endpointResolutionPending := false
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
			endpointResolutionPending = true
			endpoint = ""
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
	return builder.String(), endpointResolutionPending, nil
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
