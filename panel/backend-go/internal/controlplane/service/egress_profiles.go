package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrEgressProfileNotFound = errors.New("egress profile not found")

type EgressProfile struct {
	ID              int                    `json:"id"`
	Name            string                 `json:"name"`
	Type            string                 `json:"type"`
	ProxyURL        string                 `json:"proxy_url,omitempty"`
	WireGuardConfig *EgressWireGuardConfig `json:"wireguard_config,omitempty"`
	Enabled         bool                   `json:"enabled"`
	Description     string                 `json:"description,omitempty"`
	Revision        int                    `json:"revision"`
}

type EgressProfileInput struct {
	ID              *int                   `json:"id,omitempty"`
	Name            *string                `json:"name,omitempty"`
	Type            *string                `json:"type,omitempty"`
	ProxyURL        *string                `json:"proxy_url,omitempty"`
	WireGuardConfig *EgressWireGuardConfig `json:"wireguard_config,omitempty"`
	Enabled         *bool                  `json:"enabled,omitempty"`
	Description     *string                `json:"description,omitempty"`
}

type EgressWireGuardConfig struct {
	PrivateKey string          `json:"private_key,omitempty"`
	Addresses  []string        `json:"addresses"`
	Peers      []WireGuardPeer `json:"peers"`
	DNS        []string        `json:"dns,omitempty"`
	MTU        int             `json:"mtu,omitempty"`
}

type egressProfileStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error)
	ListL4Rules(context.Context, string) ([]storage.L4RuleRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
	ListEgressProfiles(context.Context) ([]storage.EgressProfileRow, error)
	SaveEgressProfiles(context.Context, []storage.EgressProfileRow) error
}

type egressProfileService struct {
	cfg   config.Config
	store egressProfileStore
}

func NewEgressProfileService(store egressProfileStore) *egressProfileService {
	return NewEgressProfileServiceWithConfig(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
}

func NewEgressProfileServiceWithConfig(cfg config.Config, store egressProfileStore) *egressProfileService {
	if strings.TrimSpace(cfg.LocalAgentID) == "" {
		cfg.LocalAgentID = "local"
	}
	return &egressProfileService{cfg: cfg, store: store}
}

func (s *egressProfileService) List(ctx context.Context) ([]EgressProfile, error) {
	rows, err := s.store.ListEgressProfiles(ctx)
	if err != nil {
		return nil, err
	}
	profiles := make([]EgressProfile, 0, len(rows))
	for _, row := range rows {
		profile, err := egressProfileFromRow(row)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, redactEgressProfile(profile))
	}
	return profiles, nil
}

func (s *egressProfileService) Get(ctx context.Context, id int) (EgressProfile, error) {
	rows, err := s.store.ListEgressProfiles(ctx)
	if err != nil {
		return EgressProfile{}, err
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		profile, err := egressProfileFromRow(row)
		if err != nil {
			return EgressProfile{}, err
		}
		return redactEgressProfile(profile), nil
	}
	return EgressProfile{}, ErrEgressProfileNotFound
}

func (s *egressProfileService) Create(ctx context.Context, input EgressProfileInput) (EgressProfile, error) {
	rows, err := s.store.ListEgressProfiles(ctx)
	if err != nil {
		return EgressProfile{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return EgressProfile{}, err
	}
	allocatedID := allocator.AllocateEgressProfileID(preferredInt(input.ID))
	normalizedInput := input
	normalizedInput.ID = nil
	profile, err := normalizeEgressProfileInput(normalizedInput, EgressProfile{}, allocatedID)
	if err != nil {
		return EgressProfile{}, err
	}
	profile.Revision = maxEgressProfileRevision(rows) + 1

	nextRows := append(append([]storage.EgressProfileRow(nil), rows...), egressProfileToRow(profile))
	if err := s.store.SaveEgressProfiles(ctx, nextRows); err != nil {
		return EgressProfile{}, err
	}
	return redactEgressProfile(profile), nil
}

func (s *egressProfileService) Update(ctx context.Context, id int, input EgressProfileInput) (EgressProfile, error) {
	rows, err := s.store.ListEgressProfiles(ctx)
	if err != nil {
		return EgressProfile{}, err
	}
	targetIndex := -1
	var current EgressProfile
	maxRevision := 0
	for i, row := range rows {
		if int(row.Revision) > maxRevision {
			maxRevision = int(row.Revision)
		}
		if row.ID != id {
			continue
		}
		targetIndex = i
		current, err = egressProfileFromRow(row)
		if err != nil {
			return EgressProfile{}, err
		}
	}
	if targetIndex < 0 {
		return EgressProfile{}, ErrEgressProfileNotFound
	}

	profile, err := normalizeEgressProfileInput(input, current, id)
	if err != nil {
		return EgressProfile{}, err
	}
	profile.Revision = maxRevision + 1

	nextRows := append([]storage.EgressProfileRow(nil), rows...)
	nextRows[targetIndex] = egressProfileToRow(profile)
	if err := s.store.SaveEgressProfiles(ctx, nextRows); err != nil {
		return EgressProfile{}, err
	}
	return redactEgressProfile(profile), nil
}

func (s *egressProfileService) Delete(ctx context.Context, id int) (EgressProfile, error) {
	rows, err := s.store.ListEgressProfiles(ctx)
	if err != nil {
		return EgressProfile{}, err
	}
	targetIndex := -1
	var deleted EgressProfile
	for i, row := range rows {
		if row.ID != id {
			continue
		}
		targetIndex = i
		deleted, err = egressProfileFromRow(row)
		if err != nil {
			return EgressProfile{}, err
		}
		break
	}
	if targetIndex < 0 {
		return EgressProfile{}, ErrEgressProfileNotFound
	}
	if err := s.ensureProfileNotReferenced(ctx, id); err != nil {
		return EgressProfile{}, err
	}

	nextRows := append([]storage.EgressProfileRow(nil), rows[:targetIndex]...)
	nextRows = append(nextRows, rows[targetIndex+1:]...)
	if err := s.store.SaveEgressProfiles(ctx, nextRows); err != nil {
		return EgressProfile{}, err
	}
	return redactEgressProfile(deleted), nil
}

func (s *egressProfileService) ensureProfileNotReferenced(ctx context.Context, profileID int) error {
	agentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return err
	}
	for _, agentID := range agentIDs {
		httpRows, err := s.store.ListHTTPRules(ctx, agentID)
		if err != nil {
			return err
		}
		for _, row := range httpRows {
			if row.Enabled && row.EgressProfileID != nil && *row.EgressProfileID == profileID {
				return fmt.Errorf("%w: egress profile is referenced by HTTP rule %d", ErrInvalidArgument, row.ID)
			}
		}

		l4Rows, err := s.store.ListL4Rules(ctx, agentID)
		if err != nil {
			return err
		}
		for _, row := range l4Rows {
			if row.Enabled && row.EgressProfileID != nil && *row.EgressProfileID == profileID {
				return fmt.Errorf("%w: egress profile is referenced by l4 rule %d", ErrInvalidArgument, row.ID)
			}
		}
	}
	return nil
}

func normalizeEgressProfileInput(input EgressProfileInput, fallback EgressProfile, suggestedID int) (EgressProfile, error) {
	id := fallback.ID
	if input.ID != nil && *input.ID > 0 {
		id = *input.ID
	}
	if id <= 0 {
		id = suggestedID
	}
	if id <= 0 {
		return EgressProfile{}, fmt.Errorf("%w: egress profile id is required", ErrInvalidArgument)
	}

	name := strings.TrimSpace(fallback.Name)
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if name == "" {
		name = fmt.Sprintf("Egress Profile %d", id)
	}

	profileType := strings.ToLower(strings.TrimSpace(fallback.Type))
	if input.Type != nil {
		profileType = strings.ToLower(strings.TrimSpace(*input.Type))
	}
	if profileType == "" {
		profileType = "direct"
	}

	proxyURL := strings.TrimSpace(fallback.ProxyURL)
	if input.ProxyURL != nil {
		proxyURL = mergeEgressProxyURL(strings.TrimSpace(*input.ProxyURL), fallback.ProxyURL)
	}

	wireGuardConfig := cloneEgressWireGuardConfig(fallback.WireGuardConfig)
	if input.WireGuardConfig != nil {
		wireGuardConfig = mergeEgressWireGuardConfig(*input.WireGuardConfig, fallback.WireGuardConfig)
	}

	enabled := true
	if fallback.ID > 0 {
		enabled = fallback.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	description := strings.TrimSpace(fallback.Description)
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}

	switch profileType {
	case "direct":
		proxyURL = ""
		wireGuardConfig = nil
	case "socks":
		if err := requireEgressProxyURLScheme(proxyURL, "socks", "socks5", "socks5h"); err != nil {
			return EgressProfile{}, err
		}
		wireGuardConfig = nil
	case "http":
		if err := requireEgressProxyURLScheme(proxyURL, "http", "https"); err != nil {
			return EgressProfile{}, err
		}
		wireGuardConfig = nil
	case "wireguard":
		proxyURL = ""
		if err := requireEgressWireGuardConfig(wireGuardConfig); err != nil {
			return EgressProfile{}, err
		}
	default:
		return EgressProfile{}, fmt.Errorf("%w: egress profile type must be direct, socks, http, or wireguard", ErrInvalidArgument)
	}

	return EgressProfile{
		ID:              id,
		Name:            name,
		Type:            profileType,
		ProxyURL:        proxyURL,
		WireGuardConfig: wireGuardConfig,
		Enabled:         enabled,
		Description:     description,
		Revision:        fallback.Revision,
	}, nil
}

func requireEgressProxyURLScheme(raw string, allowed ...string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Host == "" {
		return fmt.Errorf("%w: proxy_url must be a valid URL", ErrInvalidArgument)
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	for _, candidate := range allowed {
		if scheme == candidate {
			return nil
		}
	}
	return fmt.Errorf("%w: proxy_url scheme must be %s", ErrInvalidArgument, strings.Join(allowed, ", "))
}

func requireEgressWireGuardConfig(config *EgressWireGuardConfig) error {
	if config == nil {
		return fmt.Errorf("%w: wireguard_config is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(config.PrivateKey) == "" {
		return fmt.Errorf("%w: wireguard_config.private_key is required", ErrInvalidArgument)
	}
	if err := validateWireGuardKey(config.PrivateKey, true); err != nil {
		return fmt.Errorf("%w: wireguard_config.private_key must be a WireGuard key", ErrInvalidArgument)
	}
	if len(config.Addresses) == 0 {
		return fmt.Errorf("%w: wireguard_config.addresses is required", ErrInvalidArgument)
	}
	if err := validateWireGuardPrefixes(config.Addresses, "wireguard_config.addresses"); err != nil {
		return err
	}
	if config.Peers == nil {
		config.Peers = []WireGuardPeer{}
	}
	peers, err := normalizeWireGuardPeers(config.Peers, nil, true)
	if err != nil {
		return err
	}
	config.Peers = peers
	if err := validateWireGuardDNSAddrs(config.DNS); err != nil {
		return err
	}
	if config.MTU < 0 {
		return fmt.Errorf("%w: wireguard_config.mtu must be non-negative", ErrInvalidArgument)
	}
	return nil
}

func egressProfileFromRow(row storage.EgressProfileRow) (EgressProfile, error) {
	config, err := parseEgressWireGuardConfig(row.WireGuardConfigJSON)
	if err != nil {
		return EgressProfile{}, err
	}
	return EgressProfile{
		ID:              row.ID,
		Name:            strings.TrimSpace(row.Name),
		Type:            strings.TrimSpace(row.Type),
		ProxyURL:        strings.TrimSpace(row.ProxyURL),
		WireGuardConfig: config,
		Enabled:         row.Enabled,
		Description:     strings.TrimSpace(row.Description),
		Revision:        int(row.Revision),
	}, nil
}

func egressProfileToRow(profile EgressProfile) storage.EgressProfileRow {
	return storage.EgressProfileRow{
		ID:                  profile.ID,
		Name:                profile.Name,
		Type:                profile.Type,
		ProxyURL:            profile.ProxyURL,
		WireGuardConfigJSON: marshalEgressWireGuardConfig(profile.WireGuardConfig),
		Enabled:             profile.Enabled,
		Description:         profile.Description,
		Revision:            int64(profile.Revision),
	}
}

func parseEgressWireGuardConfig(raw string) (*EgressWireGuardConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var config EgressWireGuardConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, err
	}
	if config.Peers == nil {
		config.Peers = []WireGuardPeer{}
	}
	if config.Addresses == nil {
		config.Addresses = []string{}
	}
	if config.DNS == nil {
		config.DNS = []string{}
	}
	return &config, nil
}

func marshalEgressWireGuardConfig(config *EgressWireGuardConfig) string {
	if config == nil {
		return ""
	}
	return marshalJSON(config, "{}")
}

func redactEgressProfile(profile EgressProfile) EgressProfile {
	profile.ProxyURL = redactProxyURL(profile.ProxyURL)
	profile.WireGuardConfig = redactEgressWireGuardConfig(profile.WireGuardConfig)
	return profile
}

func redactProxyURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil || parsed.User == nil {
		return trimmed
	}
	username := parsed.User.Username()
	if _, hasPassword := parsed.User.Password(); !hasPassword {
		return trimmed
	}
	parsed.User = url.UserPassword(username, redactedProxyPassword)
	return parsed.String()
}

func mergeEgressProxyURL(input string, fallback string) string {
	if strings.TrimSpace(input) == redactedProxyPassword {
		return strings.TrimSpace(fallback)
	}
	parsed, err := url.Parse(strings.TrimSpace(input))
	if err != nil || parsed == nil || parsed.User == nil {
		return strings.TrimSpace(input)
	}
	if password, hasPassword := parsed.User.Password(); !hasPassword || password != redactedProxyPassword {
		return strings.TrimSpace(input)
	}
	fallbackParsed, err := url.Parse(strings.TrimSpace(fallback))
	if err != nil || fallbackParsed == nil || fallbackParsed.User == nil {
		return strings.TrimSpace(input)
	}
	fallbackPassword, hasFallbackPassword := fallbackParsed.User.Password()
	if !hasFallbackPassword {
		return strings.TrimSpace(input)
	}
	parsed.User = url.UserPassword(parsed.User.Username(), fallbackPassword)
	return parsed.String()
}

func redactEgressWireGuardConfig(config *EgressWireGuardConfig) *EgressWireGuardConfig {
	if config == nil {
		return nil
	}
	redacted := cloneEgressWireGuardConfig(config)
	if strings.TrimSpace(redacted.PrivateKey) != "" {
		redacted.PrivateKey = redactedProxyPassword
	}
	for i := range redacted.Peers {
		if strings.TrimSpace(redacted.Peers[i].PresharedKey) != "" {
			redacted.Peers[i].PresharedKey = redactedProxyPassword
		}
	}
	return redacted
}

func mergeEgressWireGuardConfig(input EgressWireGuardConfig, fallback *EgressWireGuardConfig) *EgressWireGuardConfig {
	config := cloneEgressWireGuardConfig(&input)
	if fallback == nil {
		return config
	}
	if config.PrivateKey == redactedProxyPassword {
		config.PrivateKey = fallback.PrivateKey
	}
	fallbackPeersByPublicKey := make(map[string]WireGuardPeer, len(fallback.Peers))
	for _, peer := range fallback.Peers {
		fallbackPeersByPublicKey[strings.TrimSpace(peer.PublicKey)] = peer
	}
	for i := range config.Peers {
		if config.Peers[i].PresharedKey != redactedProxyPassword {
			continue
		}
		if peer, ok := fallbackPeersByPublicKey[strings.TrimSpace(config.Peers[i].PublicKey)]; ok {
			config.Peers[i].PresharedKey = peer.PresharedKey
		}
	}
	return config
}

func cloneEgressWireGuardConfig(config *EgressWireGuardConfig) *EgressWireGuardConfig {
	if config == nil {
		return nil
	}
	clone := *config
	clone.Addresses = append([]string(nil), config.Addresses...)
	clone.Peers = append([]WireGuardPeer(nil), config.Peers...)
	clone.DNS = append([]string(nil), config.DNS...)
	return &clone
}

func maxEgressProfileRevision(rows []storage.EgressProfileRow) int {
	maxRevision := 0
	for _, row := range rows {
		if int(row.Revision) > maxRevision {
			maxRevision = int(row.Revision)
		}
	}
	return maxRevision
}
