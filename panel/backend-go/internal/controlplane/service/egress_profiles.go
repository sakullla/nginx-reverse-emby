package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
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
	EgressProfileReferences(context.Context, int) ([]storage.EgressProfileReference, error)
	SaveAgent(context.Context, storage.AgentRow) error
	SaveEgressProfiles(context.Context, []storage.EgressProfileRow) error
}

type egressProfileLookupStore interface {
	ListEgressProfiles(context.Context) ([]storage.EgressProfileRow, error)
}

type egressProfileService struct {
	cfg               config.Config
	store             egressProfileStore
	localApplyTrigger func(context.Context) error
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

func (s *egressProfileService) SetLocalApplyTrigger(trigger func(context.Context) error) {
	s.localApplyTrigger = wrapLocalApplyTrigger(trigger)
}

func (s *egressProfileService) triggerLocalApply(ctx context.Context, agentID string) error {
	if !s.cfg.EnableLocalAgent || agentID != s.cfg.LocalAgentID || s.localApplyTrigger == nil {
		return nil
	}
	return s.localApplyTrigger(ctx)
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
	profile.Revision = allocator.AllocateRevisionGlobal(maxEgressProfileRevision(rows))

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
	if input.ID != nil && *input.ID != id {
		return EgressProfile{}, fmt.Errorf("%w: egress profile id in body must match path id", ErrInvalidArgument)
	}

	normalizedInput := input
	normalizedInput.ID = nil
	profile, err := normalizeEgressProfileInput(normalizedInput, current, id)
	if err != nil {
		return EgressProfile{}, err
	}
	if current.Enabled && !profile.Enabled {
		if err := s.ensureProfileNotReferenced(ctx, id); err != nil {
			return EgressProfile{}, err
		}
	}
	if profile.Enabled {
		if err := s.validateProfileReferences(ctx, profile); err != nil {
			return EgressProfile{}, err
		}
	}
	affectedAgentIDs, err := s.profileSnapshotTargetAgentIDs(ctx, id)
	if err != nil {
		return EgressProfile{}, err
	}
	allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
	if err != nil {
		return EgressProfile{}, err
	}
	if len(affectedAgentIDs) > 0 {
		profile.Revision = allocator.AllocateRevisionForTargets(affectedAgentIDs, maxRevision)
	} else {
		profile.Revision = maxRevision + 1
	}
	agentRollbackRows, err := snapshotAgentRowsForRollback(ctx, s.store, affectedAgentIDs)
	if err != nil {
		return EgressProfile{}, err
	}

	nextRows := append([]storage.EgressProfileRow(nil), rows...)
	nextRows[targetIndex] = egressProfileToRow(profile)
	if err := s.store.SaveEgressProfiles(ctx, nextRows); err != nil {
		return EgressProfile{}, err
	}
	rollbackPostSave := func(err error) (EgressProfile, error) {
		_ = s.store.SaveEgressProfiles(ctx, rows)
		restoreAgentRowsBestEffort(ctx, s.store, agentRollbackRows)
		return EgressProfile{}, err
	}
	if err := s.bumpRemoteDesiredRevisions(ctx, affectedAgentIDs, profile.Revision); err != nil {
		return rollbackPostSave(err)
	}
	if err := s.triggerLocalApplyForAgents(ctx, affectedAgentIDs); err != nil {
		return rollbackPostSave(err)
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
	references, err := s.store.EgressProfileReferences(ctx, profileID)
	if err != nil {
		return err
	}
	for _, reference := range references {
		switch reference.Kind {
		case "http":
			return fmt.Errorf("%w: egress profile is referenced by HTTP rule %d", ErrInvalidArgument, reference.ID)
		case "l4":
			return fmt.Errorf("%w: egress profile is referenced by l4 rule %d", ErrInvalidArgument, reference.ID)
		default:
			return fmt.Errorf("%w: egress profile is referenced by %s rule %d", ErrInvalidArgument, reference.Kind, reference.ID)
		}
	}
	return nil
}

func (s *egressProfileService) validateProfileReferences(ctx context.Context, profile EgressProfile) error {
	references, err := s.store.EgressProfileReferences(ctx, profile.ID)
	if err != nil {
		return err
	}
	for _, reference := range references {
		switch reference.Kind {
		case "http":
			row, ok, err := s.referencedHTTPRule(ctx, reference)
			if err != nil {
				return err
			}
			if !ok || !row.Enabled {
				continue
			}
			if !egressProfileSupportsHTTP(profile) {
				return fmt.Errorf("%w: egress profile %d does not support referenced HTTP rule %d", ErrInvalidArgument, profile.ID, reference.ID)
			}
		case "l4":
			row, ok, err := s.referencedL4Rule(ctx, reference)
			if err != nil {
				return err
			}
			if !ok || !row.Enabled {
				continue
			}
			if !egressProfileSupportsL4(profile) {
				return fmt.Errorf("%w: egress profile %d does not support referenced l4 rule %d", ErrInvalidArgument, profile.ID, reference.ID)
			}
			if strings.EqualFold(row.Protocol, "udp") && strings.EqualFold(profile.Type, "http") {
				return fmt.Errorf("%w: UDP l4 rule %d cannot use HTTP egress profile %d", ErrInvalidArgument, reference.ID, profile.ID)
			}
		}
	}
	return nil
}

func (s *egressProfileService) referencedHTTPRule(ctx context.Context, reference storage.EgressProfileReference) (storage.HTTPRuleRow, bool, error) {
	rows, err := s.store.ListHTTPRules(ctx, reference.AgentID)
	if err != nil {
		return storage.HTTPRuleRow{}, false, err
	}
	for _, row := range rows {
		if row.ID == reference.ID {
			return row, true, nil
		}
	}
	return storage.HTTPRuleRow{}, false, nil
}

func (s *egressProfileService) referencedL4Rule(ctx context.Context, reference storage.EgressProfileReference) (storage.L4RuleRow, bool, error) {
	rows, err := s.store.ListL4Rules(ctx, reference.AgentID)
	if err != nil {
		return storage.L4RuleRow{}, false, err
	}
	for _, row := range rows {
		if row.ID == reference.ID {
			return row, true, nil
		}
	}
	return storage.L4RuleRow{}, false, nil
}

func (s *egressProfileService) profileSnapshotTargetAgentIDs(ctx context.Context, profileID int) ([]string, error) {
	references, err := s.store.EgressProfileReferences(ctx, profileID)
	if err != nil {
		return nil, err
	}
	if len(references) == 0 {
		return nil, nil
	}
	relayRows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	knownAgentIDs, err := allKnownAgentIDs(ctx, s.cfg, s.store)
	if err != nil {
		return nil, err
	}
	knownAgents := make(map[string]struct{}, len(knownAgentIDs))
	for _, agentID := range knownAgentIDs {
		knownAgents[strings.TrimSpace(agentID)] = struct{}{}
	}
	affected := make(map[string]struct{})
	addAgent := func(agentID string) {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" {
			return
		}
		if _, ok := knownAgents[agentID]; !ok {
			return
		}
		affected[agentID] = struct{}{}
	}
	addRuleExecutors := func(ruleAgentID string, relayLayersJSON string) {
		relayLayers := parseIntLayers(relayLayersJSON)
		if len(relayLayers) == 0 {
			addAgent(ruleAgentID)
			return
		}
		for agentID := range egressProfileFinalHopAgentIDs(relayLayers, relayRows) {
			addAgent(agentID)
		}
	}
	for _, reference := range references {
		switch reference.Kind {
		case "http":
			row, ok, err := s.referencedHTTPRule(ctx, reference)
			if err != nil {
				return nil, err
			}
			if ok && row.Enabled {
				addRuleExecutors(row.AgentID, row.RelayLayersJSON)
			}
		case "l4":
			row, ok, err := s.referencedL4Rule(ctx, reference)
			if err != nil {
				return nil, err
			}
			if ok && row.Enabled {
				addRuleExecutors(row.AgentID, row.RelayLayersJSON)
			}
		}
	}
	agentIDs := make([]string, 0, len(affected))
	for agentID := range affected {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	return agentIDs, nil
}

func (s *egressProfileService) bumpRemoteDesiredRevisions(ctx context.Context, agentIDs []string, revision int) error {
	if len(agentIDs) == 0 {
		return nil
	}
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	agentsByID := make(map[string]storage.AgentRow, len(agents))
	for _, row := range agents {
		agentsByID[row.ID] = row
	}
	for _, agentID := range agentIDs {
		if s.cfg.EnableLocalAgent && agentID == s.cfg.LocalAgentID {
			continue
		}
		row, ok := agentsByID[agentID]
		if !ok {
			return ErrAgentNotFound
		}
		nextRevision := maxInt(revision, row.DesiredRevision, row.CurrentRevision+1)
		if row.DesiredRevision < nextRevision {
			row.DesiredRevision = nextRevision
		}
		if err := s.store.SaveAgent(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (s *egressProfileService) triggerLocalApplyForAgents(ctx context.Context, agentIDs []string) error {
	for _, agentID := range agentIDs {
		if err := s.triggerLocalApply(ctx, agentID); err != nil {
			return err
		}
	}
	return nil
}

func egressProfileFinalHopAgentIDs(relayLayers [][]int, relayRows []storage.RelayListenerRow) map[string]struct{} {
	relayAgentByID := make(map[int]string, len(relayRows))
	for _, row := range relayRows {
		if row.ID <= 0 || !row.Enabled {
			continue
		}
		relayAgentByID[row.ID] = strings.TrimSpace(row.AgentID)
	}

	agentIDs := make(map[string]struct{})
	for i := len(relayLayers) - 1; i >= 0; i-- {
		if len(relayLayers[i]) == 0 {
			continue
		}
		for _, finalHopID := range relayLayers[i] {
			finalHopAgentID := strings.TrimSpace(relayAgentByID[finalHopID])
			if finalHopAgentID == "" {
				continue
			}
			agentIDs[finalHopAgentID] = struct{}{}
		}
		return agentIDs
	}
	return agentIDs
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
		var err error
		proxyURL, err = mergeEgressProxyURL(strings.TrimSpace(*input.ProxyURL), fallback.ProxyURL)
		if err != nil {
			return EgressProfile{}, err
		}
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
		if err := requireEgressProxyURLScheme(proxyURL, "http"); err != nil {
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
	if err := requireEgressProxyURLHostPort(parsed.Host); err != nil {
		return err
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	for _, candidate := range allowed {
		if scheme == candidate {
			return nil
		}
	}
	return fmt.Errorf("%w: proxy_url scheme must be %s", ErrInvalidArgument, strings.Join(allowed, ", "))
}

func requireEgressProxyURLHostPort(hostPort string) error {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return fmt.Errorf("%w: proxy_url must include host and port", ErrInvalidArgument)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%w: proxy_url must include host", ErrInvalidArgument)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("%w: proxy_url port must be between 1 and 65535", ErrInvalidArgument)
	}
	return nil
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

func mergeEgressProxyURL(input string, fallback string) (string, error) {
	if strings.TrimSpace(input) == redactedProxyPassword {
		return strings.TrimSpace(fallback), nil
	}
	trimmedInput := strings.TrimSpace(input)
	parsed, err := url.Parse(trimmedInput)
	if err != nil || parsed == nil || parsed.User == nil {
		return trimmedInput, nil
	}
	if password, hasPassword := parsed.User.Password(); !hasPassword || password != redactedProxyPassword {
		return trimmedInput, nil
	}
	trimmedFallback := strings.TrimSpace(fallback)
	if trimmedInput != redactProxyURL(trimmedFallback) {
		return "", fmt.Errorf("%w: redacted proxy_url can only preserve the existing password when the URL is otherwise unchanged", ErrInvalidArgument)
	}
	fallbackParsed, err := url.Parse(trimmedFallback)
	if err != nil || fallbackParsed == nil || fallbackParsed.User == nil {
		return trimmedInput, nil
	}
	fallbackPassword, hasFallbackPassword := fallbackParsed.User.Password()
	if !hasFallbackPassword {
		return trimmedInput, nil
	}
	parsed.User = url.UserPassword(parsed.User.Username(), fallbackPassword)
	return parsed.String(), nil
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

func getEnabledEgressProfile(ctx context.Context, store egressProfileLookupStore, id int) (EgressProfile, error) {
	if id <= 0 {
		return EgressProfile{}, fmt.Errorf("%w: egress profile id is required", ErrInvalidArgument)
	}
	rows, err := store.ListEgressProfiles(ctx)
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
		if !profile.Enabled {
			return EgressProfile{}, fmt.Errorf("%w: egress profile %d is disabled", ErrInvalidArgument, id)
		}
		return profile, nil
	}
	return EgressProfile{}, fmt.Errorf("%w: egress profile %d not found", ErrInvalidArgument, id)
}

func normalizeEgressProfileIDInput(input *int, fallback *int) (*int, error) {
	if input == nil {
		return normalizeOptionalPositiveInt(fallback), nil
	}
	if *input < 0 {
		return nil, fmt.Errorf("%w: egress_profile_id must be non-negative", ErrInvalidArgument)
	}
	if *input == 0 {
		return nil, nil
	}
	copied := *input
	return &copied, nil
}

func egressProfileSupportsHTTP(profile EgressProfile) bool {
	return egressProfileTypeSupported(profile)
}

func egressProfileSupportsL4(profile EgressProfile) bool {
	return egressProfileTypeSupported(profile)
}

func egressProfileTypeSupported(profile EgressProfile) bool {
	switch strings.ToLower(strings.TrimSpace(profile.Type)) {
	case "direct", "socks", "http", "wireguard":
		return true
	default:
		return false
	}
}
