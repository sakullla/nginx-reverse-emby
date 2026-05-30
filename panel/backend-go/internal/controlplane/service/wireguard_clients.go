package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"golang.org/x/crypto/curve25519"
)

var ErrWireGuardClientNotFound = errors.New("wireguard client not found")

type WireGuardClient struct {
	ID         int      `json:"id"`
	ProfileID  int      `json:"profile_id"`
	Name       string   `json:"name"`
	PublicKey  string   `json:"public_key"`
	Address    string   `json:"address"`
	AllowedIPs []string `json:"allowed_ips"`
	DNS        []string `json:"dns"`
	Enabled    bool     `json:"enabled"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

type WireGuardClientInput struct {
	Name       string   `json:"name"`
	AllowedIPs []string `json:"allowed_ips"`
	DNS        []string `json:"dns"`
	Enabled    *bool    `json:"enabled,omitempty"`
}

type wireGuardClientStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
	ListWireGuardProfiles(context.Context, string) ([]storage.WireGuardProfileRow, error)
	SaveWireGuardProfiles(context.Context, string, []storage.WireGuardProfileRow) error
	ListWireGuardClients(context.Context, string, int) ([]storage.WireGuardClientRow, error)
	SaveWireGuardClients(context.Context, string, int, []storage.WireGuardClientRow) error
	SaveWireGuardClientProfileMutation(context.Context, string, int, []storage.WireGuardClientRow, []storage.WireGuardProfileRow) error
	MutateWireGuardClientProfile(context.Context, string, int, func(storage.WireGuardClientProfileMutation) (storage.WireGuardClientProfileMutation, error)) error
	SaveAgent(context.Context, storage.AgentRow) error
}

type wireGuardClientService struct {
	cfg               config.Config
	store             wireGuardClientStore
	profileService    *wireGuardProfileService
	localApplyTrigger func(context.Context) error
}

func NewWireGuardClientService(cfg config.Config, store wireGuardClientStore) *wireGuardClientService {
	return &wireGuardClientService{
		cfg:            cfg,
		store:          store,
		profileService: NewWireGuardProfileService(cfg, store),
	}
}

func (s *wireGuardClientService) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = wrapLocalApplyTrigger(trigger)
	s.profileService.SetLocalApplyTrigger(trigger)
}

func (s *wireGuardClientService) ListClients(ctx context.Context, agentID string, profileID int) ([]WireGuardClient, error) {
	if !s.cfg.WireGuardModuleEnabled() {
		return nil, ErrWireGuardDisabled
	}
	resolvedID, err := s.profileService.ensureAgentExists(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if _, _, _, err := s.loadProfile(ctx, resolvedID, profileID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListWireGuardClients(ctx, resolvedID, profileID)
	if err != nil {
		return nil, err
	}
	clients := make([]WireGuardClient, 0, len(rows))
	for _, row := range rows {
		clients = append(clients, wireGuardClientFromRow(row))
	}
	return clients, nil
}

func (s *wireGuardClientService) CreateClient(ctx context.Context, agentID string, profileID int, input WireGuardClientInput) (WireGuardClient, error) {
	if !s.cfg.WireGuardModuleEnabled() {
		return WireGuardClient{}, ErrWireGuardDisabled
	}
	resolvedID, err := s.profileService.ensureAgentExists(ctx, agentID)
	if err != nil {
		return WireGuardClient{}, err
	}
	if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedID); err != nil {
		return WireGuardClient{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return WireGuardClient{}, err
	}

	var row storage.WireGuardClientRow
	var revision int
	err = s.store.MutateWireGuardClientProfile(ctx, resolvedID, profileID, func(state storage.WireGuardClientProfileMutation) (storage.WireGuardClientProfileMutation, error) {
		if state.ProfileIndex < 0 {
			return state, ErrWireGuardProfileNotFound
		}
		profile := wireGuardProfileFromRow(state.Profiles[state.ProfileIndex])
		address, err := allocateWireGuardClientAddress(profile, state.Clients)
		if err != nil {
			return state, err
		}
		allowedIPs := normalizeStringList(input.AllowedIPs)
		if len(allowedIPs) == 0 {
			allowedIPs = []string{"0.0.0.0/0", "::/0"}
		}
		if err := validateWireGuardPrefixes(allowedIPs, "allowed_ips"); err != nil {
			return state, err
		}
		dns := normalizeStringList(input.DNS)
		if input.DNS == nil {
			dns = append([]string(nil), profile.DNS...)
		}
		if err := validateWireGuardDNSAddrs(dns); err != nil {
			return state, err
		}

		privateKey, publicKey, err := generateWireGuardKeyPair()
		if err != nil {
			return state, err
		}
		presharedKey, err := generateWireGuardPresharedKey()
		if err != nil {
			return state, err
		}

		now := time.Now().UTC().Format(time.RFC3339)
		id := state.NextClientID
		if id <= 0 {
			id = nextWireGuardClientID(state.Clients)
		}
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		name := strings.TrimSpace(input.Name)
		if name == "" {
			name = fmt.Sprintf("Client %d", id)
		}
		row = storage.WireGuardClientRow{
			ID:             id,
			AgentID:        resolvedID,
			ProfileID:      profileID,
			Name:           name,
			PrivateKey:     privateKey,
			PublicKey:      publicKey,
			PresharedKey:   presharedKey,
			Address:        address,
			AllowedIPsJSON: marshalJSON(allowedIPs, "[]"),
			DNSJSON:        marshalJSON(dns, "[]"),
			Enabled:        enabled,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		state.Clients = append(state.Clients, row)
		profile.Peers = upsertWireGuardClientPeer(profile.Peers, row)
		revision = allocator.AllocateRevisionForAgent(resolvedID, maxWireGuardProfileRevision(state.Profiles))
		profile.Revision = revision
		state.Profiles[state.ProfileIndex] = wireGuardProfileToRow(profile)
		return state, nil
	})
	if err != nil {
		return WireGuardClient{}, err
	}
	if err := s.profileService.bumpRemoteDesiredRevision(ctx, resolvedID, revision); err != nil {
		return WireGuardClient{}, err
	}
	if err := s.profileService.bumpProfileRelayDependents(ctx, resolvedID, profileID, revision); err != nil {
		return WireGuardClient{}, err
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return WireGuardClient{}, err
	}
	return wireGuardClientFromRow(row), nil
}

func (s *wireGuardClientService) DeleteClient(ctx context.Context, agentID string, profileID int, clientID int) (WireGuardClient, error) {
	if !s.cfg.WireGuardModuleEnabled() {
		return WireGuardClient{}, ErrWireGuardDisabled
	}
	resolvedID, err := s.profileService.ensureAgentExists(ctx, agentID)
	if err != nil {
		return WireGuardClient{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return WireGuardClient{}, err
	}
	var deleted storage.WireGuardClientRow
	var revision int
	err = s.store.MutateWireGuardClientProfile(ctx, resolvedID, profileID, func(state storage.WireGuardClientProfileMutation) (storage.WireGuardClientProfileMutation, error) {
		if state.ProfileIndex < 0 {
			return state, ErrWireGuardProfileNotFound
		}
		targetIndex := -1
		for i, row := range state.Clients {
			if row.ID == clientID {
				targetIndex = i
				deleted = row
				break
			}
		}
		if targetIndex < 0 {
			return state, ErrWireGuardClientNotFound
		}
		nextRows := append([]storage.WireGuardClientRow(nil), state.Clients[:targetIndex]...)
		nextRows = append(nextRows, state.Clients[targetIndex+1:]...)
		state.Clients = nextRows
		profile := wireGuardProfileFromRow(state.Profiles[state.ProfileIndex])
		profile.Peers = removeWireGuardClientPeer(profile.Peers, deleted.PublicKey)
		revision = allocator.AllocateRevisionForAgent(resolvedID, maxWireGuardProfileRevision(state.Profiles))
		profile.Revision = revision
		state.Profiles[state.ProfileIndex] = wireGuardProfileToRow(profile)
		return state, nil
	})
	if err != nil {
		return WireGuardClient{}, err
	}
	if err := s.profileService.bumpRemoteDesiredRevision(ctx, resolvedID, revision); err != nil {
		return WireGuardClient{}, err
	}
	if err := s.profileService.bumpProfileRelayDependents(ctx, resolvedID, profileID, revision); err != nil {
		return WireGuardClient{}, err
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return WireGuardClient{}, err
	}
	return wireGuardClientFromRow(deleted), nil
}

func (s *wireGuardClientService) UpdateClient(ctx context.Context, agentID string, profileID int, clientID int, input WireGuardClientInput) (WireGuardClient, error) {
	if !s.cfg.WireGuardModuleEnabled() {
		return WireGuardClient{}, ErrWireGuardDisabled
	}
	resolvedID, err := s.profileService.ensureAgentExists(ctx, agentID)
	if err != nil {
		return WireGuardClient{}, err
	}
	if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedID); err != nil {
		return WireGuardClient{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return WireGuardClient{}, err
	}
	var updated storage.WireGuardClientRow
	var revision int
	err = s.store.MutateWireGuardClientProfile(ctx, resolvedID, profileID, func(state storage.WireGuardClientProfileMutation) (storage.WireGuardClientProfileMutation, error) {
		if state.ProfileIndex < 0 {
			return state, ErrWireGuardProfileNotFound
		}
		targetIndex := -1
		for i, row := range state.Clients {
			if row.ID == clientID {
				targetIndex = i
				updated = row
				break
			}
		}
		if targetIndex < 0 {
			return state, ErrWireGuardClientNotFound
		}

		hasChange := false
		if input.Enabled != nil && updated.Enabled != *input.Enabled {
			updated.Enabled = *input.Enabled
			hasChange = true
		}
		name := strings.TrimSpace(input.Name)
		if name != "" && updated.Name != name {
			updated.Name = name
			hasChange = true
		}
		if input.AllowedIPs != nil {
			allowedIPs := normalizeStringList(input.AllowedIPs)
			if err := validateWireGuardPrefixes(allowedIPs, "allowed_ips"); err != nil {
				return state, err
			}
			updated.AllowedIPsJSON = marshalJSON(allowedIPs, "[]")
			hasChange = true
		}
		if input.DNS != nil {
			dns := normalizeStringList(input.DNS)
			if err := validateWireGuardDNSAddrs(dns); err != nil {
				return state, err
			}
			updated.DNSJSON = marshalJSON(dns, "[]")
			hasChange = true
		}
		if !hasChange {
			return state, fmt.Errorf("%w: no fields to update", ErrInvalidArgument)
		}

		updated.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		state.Clients[targetIndex] = updated

		profile := wireGuardProfileFromRow(state.Profiles[state.ProfileIndex])
		profile.Peers = upsertWireGuardClientPeer(profile.Peers, updated)
		revision = allocator.AllocateRevisionForAgent(resolvedID, maxWireGuardProfileRevision(state.Profiles))
		profile.Revision = revision
		state.Profiles[state.ProfileIndex] = wireGuardProfileToRow(profile)
		return state, nil
	})
	if err != nil {
		return WireGuardClient{}, err
	}
	if err := s.profileService.bumpRemoteDesiredRevision(ctx, resolvedID, revision); err != nil {
		return WireGuardClient{}, err
	}
	if err := s.profileService.bumpProfileRelayDependents(ctx, resolvedID, profileID, revision); err != nil {
		return WireGuardClient{}, err
	}
	if err := s.triggerLocalApply(ctx, resolvedID); err != nil {
		return WireGuardClient{}, err
	}
	return wireGuardClientFromRow(updated), nil
}

func (s *wireGuardClientService) ClientConfig(ctx context.Context, agentID string, profileID int, clientID int) (string, error) {
	if !s.cfg.WireGuardModuleEnabled() {
		return "", ErrWireGuardDisabled
	}
	resolvedID, err := s.profileService.ensureAgentExists(ctx, agentID)
	if err != nil {
		return "", err
	}
	if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedID); err != nil {
		return "", err
	}
	_, profile, _, err := s.loadProfile(ctx, resolvedID, profileID)
	if err != nil {
		return "", err
	}
	clients, err := s.store.ListWireGuardClients(ctx, resolvedID, profileID)
	if err != nil {
		return "", err
	}
	var client storage.WireGuardClientRow
	found := false
	for _, row := range clients {
		if row.ID == clientID {
			client = row
			found = true
			break
		}
	}
	if !found {
		return "", ErrWireGuardClientNotFound
	}

	endpoint := strings.TrimSpace(profile.PublicEndpoint)
	if endpoint == "" {
		return "", fmt.Errorf("%w: wireguard profile public endpoint is required", ErrInvalidArgument)
	}
	serverPublicKey, err := wireGuardPublicKeyFromPrivateKey(profile.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("%w: profile private_key must be a WireGuard key", ErrInvalidArgument)
	}

	dns := parseStringArray(client.DNSJSON)
	var builder strings.Builder
	builder.WriteString("[Interface]\n")
	builder.WriteString("PrivateKey = ")
	builder.WriteString(client.PrivateKey)
	builder.WriteString("\n")
	builder.WriteString("Address = ")
	builder.WriteString(client.Address)
	builder.WriteString("\n")
	if len(dns) > 0 {
		builder.WriteString("DNS = ")
		builder.WriteString(strings.Join(dns, ", "))
		builder.WriteString("\n")
	}
	builder.WriteString("\n[Peer]\n")
	builder.WriteString("PublicKey = ")
	builder.WriteString(serverPublicKey)
	builder.WriteString("\n")
	if strings.TrimSpace(client.PresharedKey) != "" {
		builder.WriteString("PresharedKey = ")
		builder.WriteString(client.PresharedKey)
		builder.WriteString("\n")
	}
	builder.WriteString("Endpoint = ")
	builder.WriteString(endpoint)
	builder.WriteString("\n")
	builder.WriteString("AllowedIPs = ")
	builder.WriteString(strings.Join(parseStringArray(client.AllowedIPsJSON), ", "))
	builder.WriteString("\n")
	return builder.String(), nil
}

func (s *wireGuardClientService) ClientURI(ctx context.Context, agentID string, profileID int, clientID int, reserved []byte) (string, error) {
	if !s.cfg.WireGuardModuleEnabled() {
		return "", ErrWireGuardDisabled
	}
	resolvedID, err := s.profileService.ensureAgentExists(ctx, agentID)
	if err != nil {
		return "", err
	}
	if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedID); err != nil {
		return "", err
	}
	_, profile, _, err := s.loadProfile(ctx, resolvedID, profileID)
	if err != nil {
		return "", err
	}
	clients, err := s.store.ListWireGuardClients(ctx, resolvedID, profileID)
	if err != nil {
		return "", err
	}
	var client storage.WireGuardClientRow
	found := false
	for _, row := range clients {
		if row.ID == clientID {
			client = row
			found = true
			break
		}
	}
	if !found {
		return "", ErrWireGuardClientNotFound
	}
	endpoint := strings.TrimSpace(profile.PublicEndpoint)
	if endpoint == "" {
		return "", fmt.Errorf("%w: wireguard profile public endpoint is required", ErrInvalidArgument)
	}
	serverPublicKey, err := wireGuardPublicKeyFromPrivateKey(profile.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("%w: profile private_key must be a WireGuard key", ErrInvalidArgument)
	}
	if len(reserved) > 3 {
		return "", fmt.Errorf("%w: reserved accepts at most 3 bytes", ErrInvalidArgument)
	}

	u := url.URL{Scheme: "wireguard", Host: endpoint, User: url.User(client.PrivateKey), Fragment: client.Name}
	q := u.Query()
	q.Set("publickey", serverPublicKey)
	if strings.TrimSpace(client.PresharedKey) != "" {
		q.Set("preshared-key", client.PresharedKey)
	}
	q.Set("address", client.Address)
	if allowedIPs := parseStringArray(client.AllowedIPsJSON); len(allowedIPs) > 0 {
		q.Set("allowed-ips", strings.Join(allowedIPs, ","))
	}
	if dns := parseStringArray(client.DNSJSON); len(dns) > 0 {
		q.Set("dns", strings.Join(dns, ","))
	}
	if profile.MTU > 0 {
		q.Set("mtu", strconv.Itoa(profile.MTU))
	}
	if len(reserved) > 0 {
		parts := make([]string, 0, len(reserved))
		for _, b := range reserved {
			parts = append(parts, strconv.Itoa(int(b)))
		}
		q.Set("reserved", strings.Join(parts, ","))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *wireGuardClientService) loadProfile(ctx context.Context, agentID string, profileID int) ([]storage.WireGuardProfileRow, WireGuardProfile, int, error) {
	rows, err := s.store.ListWireGuardProfiles(ctx, agentID)
	if err != nil {
		return nil, WireGuardProfile{}, -1, err
	}
	for i, row := range rows {
		profile := wireGuardProfileFromRow(row)
		if profile.ID == profileID {
			return rows, profile, i, nil
		}
	}
	return nil, WireGuardProfile{}, -1, ErrWireGuardProfileNotFound
}

func (s *wireGuardClientService) triggerLocalApply(ctx context.Context, agentID string) error {
	if !s.cfg.EnableLocalAgent || agentID != s.cfg.LocalAgentID || s.localApplyTrigger == nil {
		return nil
	}
	return s.localApplyTrigger(ctx)
}

func wireGuardClientFromRow(row storage.WireGuardClientRow) WireGuardClient {
	return WireGuardClient{
		ID:         row.ID,
		ProfileID:  row.ProfileID,
		Name:       row.Name,
		PublicKey:  row.PublicKey,
		Address:    row.Address,
		AllowedIPs: parseStringArray(row.AllowedIPsJSON),
		DNS:        parseStringArray(row.DNSJSON),
		Enabled:    row.Enabled,
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}

func nextWireGuardClientID(rows []storage.WireGuardClientRow) int {
	maxID := 0
	for _, row := range rows {
		if row.ID > maxID {
			maxID = row.ID
		}
	}
	return maxID + 1
}

func allocateWireGuardClientAddress(profile WireGuardProfile, clients []storage.WireGuardClientRow) (string, error) {
	var network netip.Prefix
	interfaceAddresses := map[netip.Addr]struct{}{}
	for _, raw := range profile.InterfaceAddresses {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			continue
		}
		if !prefix.Addr().IsValid() {
			continue
		}
		if !network.IsValid() {
			network = prefix.Masked()
		}
		interfaceAddresses[prefix.Addr()] = struct{}{}
	}
	if !network.IsValid() {
		return "", fmt.Errorf("%w: wireguard profile address pool is required", ErrInvalidArgument)
	}

	used := map[netip.Addr]struct{}{}
	var usedPrefixes []netip.Prefix
	used[network.Addr()] = struct{}{}
	if broadcast, ok := wireGuardIPv4Broadcast(network); ok {
		used[broadcast] = struct{}{}
	}
	for addr := range interfaceAddresses {
		used[addr] = struct{}{}
	}
	for _, peer := range profile.Peers {
		for _, allowedIP := range peer.AllowedIPs {
			prefix, err := netip.ParsePrefix(allowedIP)
			if err != nil {
				continue
			}
			prefix = prefix.Masked()
			if wireGuardPrefixesOverlap(network, prefix) {
				used[prefix.Addr()] = struct{}{}
				usedPrefixes = append(usedPrefixes, prefix)
			}
		}
	}
	for _, row := range clients {
		if prefix, err := netip.ParsePrefix(row.Address); err == nil {
			used[prefix.Addr()] = struct{}{}
		}
	}

	for addr, checked := network.Addr().Next(), 0; network.Contains(addr) && checked < 100000; addr, checked = addr.Next(), checked+1 {
		if _, exists := used[addr]; exists {
			continue
		}
		if wireGuardAddrInAnyPrefix(addr, usedPrefixes) {
			continue
		}
		bits := 128
		if addr.Is4() {
			bits = 32
		}
		return netip.PrefixFrom(addr, bits).String(), nil
	}
	return "", fmt.Errorf("%w: no available wireguard client addresses", ErrInvalidArgument)
}

func wireGuardPrefixesOverlap(a netip.Prefix, b netip.Prefix) bool {
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}

func wireGuardAddrInAnyPrefix(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func wireGuardIPv4Broadcast(prefix netip.Prefix) (netip.Addr, bool) {
	addr := prefix.Masked().Addr()
	if !addr.Is4() || prefix.Bits() >= 31 {
		return netip.Addr{}, false
	}
	octets := addr.As4()
	value := uint32(octets[0])<<24 | uint32(octets[1])<<16 | uint32(octets[2])<<8 | uint32(octets[3])
	value |= uint32(math.MaxUint32 >> prefix.Bits())
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}), true
}

func upsertWireGuardClientPeer(peers []WireGuardPeer, row storage.WireGuardClientRow) []WireGuardPeer {
	next := removeWireGuardClientPeer(peers, row.PublicKey)
	if !row.Enabled {
		return next
	}
	return append(next, WireGuardPeer{
		Name:         row.Name,
		PublicKey:    row.PublicKey,
		PresharedKey: row.PresharedKey,
		AllowedIPs:   []string{row.Address},
	})
}

func reconcileWireGuardGeneratedClientPeers(peers []WireGuardPeer, clients []storage.WireGuardClientRow) []WireGuardPeer {
	next := append([]WireGuardPeer(nil), peers...)
	for _, client := range clients {
		next = upsertWireGuardClientPeer(next, client)
	}
	return next
}

func removeWireGuardGeneratedClientPeers(peers []WireGuardPeer, clients []storage.WireGuardClientRow) []WireGuardPeer {
	next := append([]WireGuardPeer(nil), peers...)
	for _, client := range clients {
		next = removeWireGuardClientPeer(next, client.PublicKey)
	}
	return next
}

func removeWireGuardClientPeer(peers []WireGuardPeer, publicKey string) []WireGuardPeer {
	publicKey = strings.TrimSpace(publicKey)
	next := make([]WireGuardPeer, 0, len(peers))
	for _, peer := range peers {
		if publicKey != "" && strings.TrimSpace(peer.PublicKey) == publicKey {
			continue
		}
		next = append(next, peer)
	}
	return next
}

func generateWireGuardKeyPair() (string, string, error) {
	privateBytes := make([]byte, 32)
	if _, err := rand.Read(privateBytes); err != nil {
		return "", "", err
	}
	clampWireGuardPrivateKey(privateBytes)
	publicBytes, err := curve25519.X25519(privateBytes, curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(privateBytes), base64.StdEncoding.EncodeToString(publicBytes), nil
}

func generateWireGuardPresharedKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func wireGuardPublicKeyFromPrivateKey(privateKey string) (string, error) {
	privateBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKey))
	if err != nil || len(privateBytes) != 32 {
		return "", fmt.Errorf("invalid key")
	}
	clampWireGuardPrivateKey(privateBytes)
	publicBytes, err := curve25519.X25519(privateBytes, curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(publicBytes), nil
}

func clampWireGuardPrivateKey(key []byte) {
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64
}
