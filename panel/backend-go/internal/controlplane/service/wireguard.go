package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrWireGuardProfileNotFound = errors.New("wireguard profile not found")

type WireGuardPeer struct {
	Name                       string   `json:"name"`
	PublicKey                  string   `json:"public_key"`
	PresharedKey               string   `json:"preshared_key,omitempty"`
	Endpoint                   string   `json:"endpoint"`
	AllowedIPs                 []string `json:"allowed_ips"`
	PersistentKeepaliveSeconds int      `json:"persistent_keepalive_seconds,omitempty"`
}

type WireGuardProfile struct {
	ID         int             `json:"id"`
	AgentID    string          `json:"agent_id"`
	Name       string          `json:"name"`
	Mode       string          `json:"mode"`
	PrivateKey string          `json:"private_key,omitempty"`
	ListenPort int             `json:"listen_port"`
	Addresses  []string        `json:"addresses"`
	Peers      []WireGuardPeer `json:"peers"`
	DNS        []string        `json:"dns"`
	MTU        int             `json:"mtu"`
	Enabled    bool            `json:"enabled"`
	Tags       []string        `json:"tags"`
	Revision   int             `json:"revision"`
}

type WireGuardProfileInput struct {
	ID         int             `json:"id,omitempty"`
	Name       string          `json:"name"`
	Mode       string          `json:"mode"`
	PrivateKey string          `json:"private_key,omitempty"`
	ListenPort int             `json:"listen_port"`
	Addresses  []string        `json:"addresses"`
	Peers      []WireGuardPeer `json:"peers"`
	DNS        []string        `json:"dns"`
	MTU        int             `json:"mtu"`
	Enabled    *bool           `json:"enabled,omitempty"`
	Tags       []string        `json:"tags"`
}

type wireGuardProfileStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
	ListWireGuardProfiles(context.Context, string) ([]storage.WireGuardProfileRow, error)
	SaveWireGuardProfiles(context.Context, string, []storage.WireGuardProfileRow) error
	SaveAgent(context.Context, storage.AgentRow) error
}

type wireGuardProfileService struct {
	cfg   config.Config
	store wireGuardProfileStore
}

func NewWireGuardProfileService(cfg config.Config, store wireGuardProfileStore) *wireGuardProfileService {
	return &wireGuardProfileService{cfg: cfg, store: store}
}

func (s *wireGuardProfileService) List(ctx context.Context, agentID string) ([]WireGuardProfile, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListWireGuardProfiles(ctx, resolvedID)
	if err != nil {
		return nil, err
	}
	profiles := make([]WireGuardProfile, 0, len(rows))
	for _, row := range rows {
		profiles = append(profiles, redactWireGuardProfile(wireGuardProfileFromRow(row)))
	}
	return profiles, nil
}

func (s *wireGuardProfileService) Create(ctx context.Context, agentID string, input WireGuardProfileInput) (WireGuardProfile, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return WireGuardProfile{}, err
	}
	rows, err := s.store.ListWireGuardProfiles(ctx, resolvedID)
	if err != nil {
		return WireGuardProfile{}, err
	}

	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return WireGuardProfile{}, err
	}

	usedIDs := map[int]struct{}{}
	for _, row := range rows {
		if row.ID > 0 {
			usedIDs[row.ID] = struct{}{}
		}
	}
	maxRevision := maxWireGuardProfileRevision(rows)
	profile, err := normalizeWireGuardProfileInput(input, WireGuardProfile{}, allocatePreferredID(usedIDs, input.ID))
	if err != nil {
		return WireGuardProfile{}, err
	}
	if err := validateRequiredWireGuardProfileEssentials(profile); err != nil {
		return WireGuardProfile{}, err
	}
	profile.AgentID = resolvedID
	profile.Revision = allocator.AllocateRevisionForAgent(resolvedID, maxRevision)

	rows = append(rows, wireGuardProfileToRow(profile))
	if err := s.store.SaveWireGuardProfiles(ctx, resolvedID, rows); err != nil {
		return WireGuardProfile{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, profile.Revision); err != nil {
		return WireGuardProfile{}, err
	}
	return redactWireGuardProfile(profile), nil
}

func (s *wireGuardProfileService) Update(ctx context.Context, agentID string, id int, input WireGuardProfileInput) (WireGuardProfile, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return WireGuardProfile{}, err
	}
	rows, err := s.store.ListWireGuardProfiles(ctx, resolvedID)
	if err != nil {
		return WireGuardProfile{}, err
	}

	targetIndex := -1
	var current WireGuardProfile
	for i, row := range rows {
		profile := wireGuardProfileFromRow(row)
		if profile.ID == id {
			targetIndex = i
			current = profile
		}
	}
	if targetIndex < 0 {
		return WireGuardProfile{}, ErrWireGuardProfileNotFound
	}

	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return WireGuardProfile{}, err
	}
	maxRevision := maxWireGuardProfileRevision(rows)
	input.ID = 0
	profile, err := normalizeWireGuardProfileInput(input, current, id)
	if err != nil {
		return WireGuardProfile{}, err
	}
	profile.AgentID = resolvedID
	profile.Revision = allocator.AllocateRevisionForAgent(resolvedID, maxRevision)
	rows[targetIndex] = wireGuardProfileToRow(profile)
	if err := s.store.SaveWireGuardProfiles(ctx, resolvedID, rows); err != nil {
		return WireGuardProfile{}, err
	}
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, profile.Revision); err != nil {
		return WireGuardProfile{}, err
	}
	return redactWireGuardProfile(profile), nil
}

func (s *wireGuardProfileService) Delete(ctx context.Context, agentID string, id int) (WireGuardProfile, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return WireGuardProfile{}, err
	}
	rows, err := s.store.ListWireGuardProfiles(ctx, resolvedID)
	if err != nil {
		return WireGuardProfile{}, err
	}

	targetIndex := -1
	var deleted WireGuardProfile
	for i, row := range rows {
		profile := wireGuardProfileFromRow(row)
		if profile.ID == id {
			targetIndex = i
			deleted = profile
			break
		}
	}
	if targetIndex < 0 {
		return WireGuardProfile{}, ErrWireGuardProfileNotFound
	}

	nextRows := append([]storage.WireGuardProfileRow(nil), rows[:targetIndex]...)
	nextRows = append(nextRows, rows[targetIndex+1:]...)
	if err := s.store.SaveWireGuardProfiles(ctx, resolvedID, nextRows); err != nil {
		return WireGuardProfile{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return WireGuardProfile{}, err
	}
	nextRevision := allocator.AllocateRevisionForAgent(resolvedID, deleted.Revision)
	if err := s.bumpRemoteDesiredRevision(ctx, resolvedID, nextRevision); err != nil {
		return WireGuardProfile{}, err
	}
	return redactWireGuardProfile(deleted), nil
}

func (s *wireGuardProfileService) ensureAgentExists(ctx context.Context, agentID string) (string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		resolvedID = s.cfg.LocalAgentID
	}
	if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
		return resolvedID, nil
	}
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.ID == resolvedID {
			return resolvedID, nil
		}
	}
	return "", ErrAgentNotFound
}

func (s *wireGuardProfileService) bumpRemoteDesiredRevision(ctx context.Context, agentID string, revision int) error {
	if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
		return nil
	}
	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID != agentID {
			continue
		}
		nextRevision := revision
		if row.DesiredRevision > nextRevision {
			nextRevision = row.DesiredRevision
		}
		if row.CurrentRevision > nextRevision {
			nextRevision = row.CurrentRevision
		}
		if row.DesiredRevision < nextRevision {
			row.DesiredRevision = nextRevision
		}
		return s.store.SaveAgent(ctx, row)
	}
	return ErrAgentNotFound
}

func normalizeWireGuardProfileInput(input WireGuardProfileInput, fallback WireGuardProfile, suggestedID int) (WireGuardProfile, error) {
	id := fallback.ID
	if input.ID > 0 {
		id = input.ID
	}
	if id <= 0 {
		id = suggestedID
	}

	privateKey := strings.TrimSpace(input.PrivateKey)
	if privateKey == redactedProxyPassword {
		privateKey = fallback.PrivateKey
	}
	if err := validateWireGuardKey(privateKey, true); err != nil {
		return WireGuardProfile{}, fmt.Errorf("%w: private_key must be a WireGuard key", ErrInvalidArgument)
	}

	addresses := normalizeStringList(input.Addresses)
	if len(addresses) == 0 && fallback.ID > 0 {
		addresses = append([]string(nil), fallback.Addresses...)
	}
	if err := validateWireGuardPrefixes(addresses, "addresses"); err != nil {
		return WireGuardProfile{}, err
	}

	peers, err := normalizeWireGuardPeers(input.Peers, fallback.Peers)
	if err != nil {
		return WireGuardProfile{}, err
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = strings.TrimSpace(fallback.Name)
	}
	if name == "" {
		name = fmt.Sprintf("WireGuard %d", id)
	}

	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(fallback.Mode))
	}
	if mode == "" {
		mode = "generic_wireguard"
	}
	if mode != "generic_wireguard" {
		return WireGuardProfile{}, fmt.Errorf("%w: mode must be generic_wireguard", ErrInvalidArgument)
	}

	listenPort := input.ListenPort
	if listenPort == 0 {
		listenPort = fallback.ListenPort
	}
	if listenPort < 0 || listenPort > 65535 {
		return WireGuardProfile{}, fmt.Errorf("%w: listen_port must be a valid port", ErrInvalidArgument)
	}

	mtu := input.MTU
	if mtu == 0 {
		mtu = fallback.MTU
	}
	if mtu < 0 {
		return WireGuardProfile{}, fmt.Errorf("%w: mtu must be non-negative", ErrInvalidArgument)
	}

	dns := normalizeStringList(input.DNS)
	if len(dns) == 0 && fallback.ID > 0 {
		dns = append([]string(nil), fallback.DNS...)
	}
	tags := normalizeTags(input.Tags)
	if len(tags) == 0 && fallback.ID > 0 {
		tags = append([]string(nil), fallback.Tags...)
	}

	enabled := true
	if fallback.ID > 0 {
		enabled = fallback.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	return WireGuardProfile{
		ID:         id,
		AgentID:    fallback.AgentID,
		Name:       name,
		Mode:       mode,
		PrivateKey: privateKey,
		ListenPort: listenPort,
		Addresses:  addresses,
		Peers:      peers,
		DNS:        dns,
		MTU:        mtu,
		Enabled:    enabled,
		Tags:       tags,
		Revision:   fallback.Revision,
	}, nil
}

func normalizeWireGuardPeers(input []WireGuardPeer, fallback []WireGuardPeer) ([]WireGuardPeer, error) {
	source := input
	if len(source) == 0 && len(fallback) > 0 {
		source = fallback
	}
	fallbackByPublicKey := make(map[string]WireGuardPeer, len(fallback))
	for _, peer := range fallback {
		publicKey := strings.TrimSpace(peer.PublicKey)
		if publicKey != "" {
			fallbackByPublicKey[publicKey] = peer
		}
	}
	peers := make([]WireGuardPeer, 0, len(source))
	seenPublicKeys := map[string]struct{}{}
	for _, peer := range source {
		normalized := WireGuardPeer{
			Name:                       strings.TrimSpace(peer.Name),
			PublicKey:                  strings.TrimSpace(peer.PublicKey),
			PresharedKey:               strings.TrimSpace(peer.PresharedKey),
			Endpoint:                   strings.TrimSpace(peer.Endpoint),
			AllowedIPs:                 normalizeStringList(peer.AllowedIPs),
			PersistentKeepaliveSeconds: peer.PersistentKeepaliveSeconds,
		}
		if normalized.PresharedKey == redactedProxyPassword {
			fallbackPeer, ok := fallbackByPublicKey[normalized.PublicKey]
			if !ok {
				return nil, fmt.Errorf("%w: peers preshared_key redaction requires matching public_key", ErrInvalidArgument)
			}
			normalized.PresharedKey = fallbackPeer.PresharedKey
		}
		if err := validateWireGuardKey(normalized.PublicKey, true); err != nil {
			return nil, fmt.Errorf("%w: peers public_key must be a WireGuard key", ErrInvalidArgument)
		}
		if _, exists := seenPublicKeys[normalized.PublicKey]; exists {
			return nil, fmt.Errorf("%w: duplicate peers public_key %q", ErrInvalidArgument, normalized.PublicKey)
		}
		seenPublicKeys[normalized.PublicKey] = struct{}{}
		if err := validateWireGuardKey(normalized.PresharedKey, false); err != nil {
			return nil, fmt.Errorf("%w: peers preshared_key must be a WireGuard key", ErrInvalidArgument)
		}
		if err := validateWireGuardPrefixes(normalized.AllowedIPs, "allowed_ips"); err != nil {
			return nil, err
		}
		if normalized.Endpoint != "" {
			if err := validateWireGuardPeerEndpoint(normalized.Endpoint); err != nil {
				return nil, err
			}
		}
		if normalized.PersistentKeepaliveSeconds < 0 {
			return nil, fmt.Errorf("%w: persistent_keepalive_seconds must be non-negative", ErrInvalidArgument)
		}
		peers = append(peers, normalized)
	}
	return peers, nil
}

func validateWireGuardPeerEndpoint(endpoint string) error {
	host, portValue, err := net.SplitHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("%w: endpoint must be host:port", ErrInvalidArgument)
	}
	if strings.TrimSpace(host) == "" || strings.TrimSpace(portValue) == "" {
		return fmt.Errorf("%w: endpoint must include host and port", ErrInvalidArgument)
	}
	if !isValidWireGuardEndpointHost(host) {
		return fmt.Errorf("%w: endpoint host must be a valid IP address or DNS name", ErrInvalidArgument)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%w: endpoint port must be numeric and between 1 and 65535", ErrInvalidArgument)
	}
	return nil
}

func isValidWireGuardEndpointHost(host string) bool {
	if host == "" || host != strings.TrimSpace(host) || len(host) > 253 {
		return false
	}
	for _, r := range host {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.IsValid() {
		return true
	}
	return isValidWireGuardEndpointDNSName(host)
}

func isValidWireGuardEndpointDNSName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func validateWireGuardKey(value string, required bool) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			return fmt.Errorf("missing key")
		}
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid key")
	}
	return nil
}

func validateRequiredWireGuardProfileEssentials(profile WireGuardProfile) error {
	if len(profile.Addresses) == 0 {
		return fmt.Errorf("%w: addresses is required", ErrInvalidArgument)
	}
	if len(profile.Peers) == 0 {
		return fmt.Errorf("%w: peers is required", ErrInvalidArgument)
	}
	return nil
}

func maxWireGuardProfileRevision(rows []storage.WireGuardProfileRow) int {
	maxRevision := 0
	for _, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	return maxRevision
}

func validateWireGuardPrefixes(values []string, field string) error {
	for _, value := range values {
		if _, err := netip.ParsePrefix(value); err != nil {
			return fmt.Errorf("%w: %s must be CIDR", ErrInvalidArgument, field)
		}
	}
	return nil
}

func normalizeStringList(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
}

func redactWireGuardProfile(profile WireGuardProfile) WireGuardProfile {
	if profile.PrivateKey != "" {
		profile.PrivateKey = redactedProxyPassword
	}
	for i := range profile.Peers {
		if profile.Peers[i].PresharedKey != "" {
			profile.Peers[i].PresharedKey = redactedProxyPassword
		}
	}
	return profile
}

func wireGuardProfileFromRow(row storage.WireGuardProfileRow) WireGuardProfile {
	return WireGuardProfile{
		ID:         row.ID,
		AgentID:    row.AgentID,
		Name:       row.Name,
		Mode:       row.Mode,
		PrivateKey: row.PrivateKey,
		ListenPort: row.ListenPort,
		Addresses:  parseStringArray(row.AddressesJSON),
		Peers:      parseWireGuardPeers(row.PeersJSON),
		DNS:        parseStringArray(row.DNSJSON),
		MTU:        row.MTU,
		Enabled:    row.Enabled,
		Tags:       parseStringArray(row.TagsJSON),
		Revision:   row.Revision,
	}
}

func wireGuardProfileToRow(profile WireGuardProfile) storage.WireGuardProfileRow {
	return storage.WireGuardProfileRow{
		ID:            profile.ID,
		AgentID:       profile.AgentID,
		Name:          profile.Name,
		Mode:          profile.Mode,
		PrivateKey:    profile.PrivateKey,
		ListenPort:    profile.ListenPort,
		AddressesJSON: marshalJSON(profile.Addresses, "[]"),
		PeersJSON:     marshalJSON(profile.Peers, "[]"),
		DNSJSON:       marshalJSON(profile.DNS, "[]"),
		MTU:           profile.MTU,
		Enabled:       profile.Enabled,
		TagsJSON:      marshalJSON(profile.Tags, "[]"),
		Revision:      profile.Revision,
	}
}

func parseWireGuardPeers(raw string) []WireGuardPeer {
	var peers []WireGuardPeer
	if err := json.Unmarshal([]byte(defaultString(raw, "[]")), &peers); err != nil {
		return []WireGuardPeer{}
	}
	if peers == nil {
		return []WireGuardPeer{}
	}
	return peers
}
