package service

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type FullSnapshotValidator struct{}

func (FullSnapshotValidator) Validate(_ context.Context, input revision.SnapshotValidation) error {
	if strings.TrimSpace(input.Target.AgentID) == "" {
		return revision.NewError(revision.ErrorCodeInvalidRequest, "snapshot target agent is required", nil)
	}
	snapshot := input.Snapshot
	if input.IntentSnapshot != nil {
		snapshot = *input.IntentSnapshot
	}
	if err := validateSnapshotCapabilities(input.Target, snapshot); err != nil {
		return err
	}
	if err := validateSnapshotDDNS(snapshot.DDNSConfig); err != nil {
		return err
	}
	if err := validateSnapshotResources(snapshot); err != nil {
		return err
	}
	if err := validateSnapshotReferences(input.Target, snapshot); err != nil {
		return err
	}
	return validateSnapshotListenerClaims(snapshot)
}

func validateSnapshotCapabilities(target revision.Target, snapshot storage.Snapshot) error {
	capabilities := make(map[string]struct{}, len(target.Capabilities))
	for _, capability := range target.Capabilities {
		capabilities[strings.ToLower(strings.TrimSpace(capability))] = struct{}{}
	}
	requiresEgress := false
	retiredEgressProfileIDs := make(map[int]struct{})
	for _, profile := range snapshot.EgressProfiles {
		if snapshotEgressProfileRetired(profile) {
			retiredEgressProfileIDs[profile.ID] = struct{}{}
			continue
		}
		if !profile.Enabled {
			continue
		}
		requiresEgress = true
	}
	referenceRequiresEgress := func(profileID *int) bool {
		if profileID == nil {
			return false
		}
		_, retired := retiredEgressProfileIDs[*profileID]
		return !retired
	}
	for _, rule := range snapshot.Rules {
		if !snapshotResourceBelongsToTarget(target.AgentID, rule.AgentID) {
			continue
		}
		requiresEgress = requiresEgress || referenceRequiresEgress(rule.EgressProfileID)
	}
	for _, rule := range snapshot.L4Rules {
		if snapshotL4RuleRetired(rule) || !snapshotResourceBelongsToTarget(target.AgentID, rule.AgentID) {
			continue
		}
		requiresEgress = requiresEgress || referenceRequiresEgress(rule.EgressProfileID)
	}
	if requiresEgress {
		if _, ok := capabilities["egress_profiles"]; !ok {
			return revision.NewError(revision.ErrorCodeUnprocessable, "snapshot requires the egress_profiles capability", nil)
		}
	}
	return nil
}

func validateSnapshotDDNS(config *storage.DDNSConfig) error {
	if config == nil || !config.Enabled {
		return nil
	}
	families := []struct {
		name   string
		family storage.DDNSFamily
	}{
		{name: "IPv4", family: config.IPv4},
		{name: "IPv6", family: config.IPv6},
	}
	for _, item := range families {
		if !item.family.Enabled {
			continue
		}
		switch item.family.Source {
		case "", "public_api":
		case "interface":
			if strings.TrimSpace(item.family.Interface) == "" {
				return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("DDNS %s interface is required", item.name), nil)
			}
		default:
			return revision.NewError(
				revision.ErrorCodeUnprocessable,
				fmt.Sprintf("DDNS %s source must be public_api or interface", item.name),
				nil,
			)
		}
	}
	return nil
}

func snapshotResourceBelongsToTarget(targetAgentID, resourceAgentID string) bool {
	resourceAgentID = strings.TrimSpace(resourceAgentID)
	return resourceAgentID == "" || resourceAgentID == strings.TrimSpace(targetAgentID)
}

func snapshotL4RuleRetired(rule storage.L4Rule) bool {
	return strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard")
}

func snapshotRelayListenerRetired(listener storage.RelayListener) bool {
	return strings.EqualFold(strings.TrimSpace(listener.TransportMode), "wireguard")
}

func snapshotEgressProfileRetired(profile storage.EgressProfile) bool {
	return strings.EqualFold(strings.TrimSpace(profile.Type), "wireguard")
}

func validateSnapshotResources(snapshot storage.Snapshot) error {
	httpIDs := map[int]struct{}{}
	frontends := map[string]int{}
	for _, rule := range snapshot.Rules {
		if rule.ID <= 0 {
			return revision.NewError(revision.ErrorCodeUnprocessable, "HTTP snapshot rule id must be positive", nil)
		}
		if _, exists := httpIDs[rule.ID]; exists {
			return revision.NewError(revision.ErrorCodeConflict, fmt.Sprintf("HTTP snapshot rule id %d is duplicated", rule.ID), nil)
		}
		httpIDs[rule.ID] = struct{}{}
		frontend, err := canonicalSnapshotFrontend(rule.FrontendURL)
		if err != nil {
			return err
		}
		if existingID, exists := frontends[frontend]; exists {
			return revision.NewError(
				revision.ErrorCodeConflict,
				fmt.Sprintf("HTTP frontend %q is shared by rules %d and %d", frontend, existingID, rule.ID),
				nil,
			)
		}
		frontends[frontend] = rule.ID
		if len(rule.Backends) == 0 {
			return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("HTTP rule %d has no backend", rule.ID), nil)
		}
		for _, backend := range rule.Backends {
			parsed, err := url.Parse(strings.TrimSpace(backend.URL))
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("HTTP rule %d has an invalid backend", rule.ID), err)
			}
		}
	}

	l4IDs := map[int]struct{}{}
	for _, rule := range snapshot.L4Rules {
		if snapshotL4RuleRetired(rule) {
			continue
		}
		if rule.ID <= 0 {
			return revision.NewError(revision.ErrorCodeUnprocessable, "L4 snapshot rule id must be positive", nil)
		}
		if _, exists := l4IDs[rule.ID]; exists {
			return revision.NewError(revision.ErrorCodeConflict, fmt.Sprintf("L4 snapshot rule id %d is duplicated", rule.ID), nil)
		}
		l4IDs[rule.ID] = struct{}{}
		if rule.ListenPort < 1 || rule.ListenPort > 65535 {
			return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("L4 rule %d has an invalid listen port", rule.ID), nil)
		}
		proxyEntry := strings.EqualFold(strings.TrimSpace(rule.ListenMode), "proxy")
		if len(rule.Backends) == 0 && !proxyEntry {
			return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("L4 rule %d has no backend", rule.ID), nil)
		}
		for _, backend := range rule.Backends {
			if strings.TrimSpace(backend.Host) == "" || backend.Port < 1 || backend.Port > 65535 {
				return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("L4 rule %d has an invalid backend", rule.ID), nil)
			}
		}
	}

	if err := validateUniqueSnapshotIDs("relay listener", relaySnapshotIDs(snapshot.RelayListeners)); err != nil {
		return err
	}
	if err := validateUniqueSnapshotIDs("egress profile", egressSnapshotIDs(snapshot.EgressProfiles)); err != nil {
		return err
	}
	for _, profile := range snapshot.EgressProfiles {
		if snapshotEgressProfileRetired(profile) {
			continue
		}
		if err := validateSnapshotEgressProfile(profile); err != nil {
			return err
		}
	}
	certificateIDs := make([]int, 0, len(snapshot.Certificates))
	for _, row := range snapshot.Certificates {
		certificateIDs = append(certificateIDs, row.ID)
	}
	if err := validateUniqueSnapshotIDs("certificate material", certificateIDs); err != nil {
		return err
	}
	policyIDs := make([]int, 0, len(snapshot.CertificatePolicies))
	for _, row := range snapshot.CertificatePolicies {
		policyIDs = append(policyIDs, row.ID)
	}
	if err := validateUniqueSnapshotIDs("certificate policy", policyIDs); err != nil {
		return err
	}
	return nil
}

func validateSnapshotEgressProfile(profile storage.EgressProfile) error {
	profileType := strings.ToLower(strings.TrimSpace(profile.Type))
	if profile.WireGuardConfigInvalid {
		return revision.NewError(
			revision.ErrorCodeUnprocessable,
			fmt.Sprintf("egress profile %d has invalid wireguard_config JSON", profile.ID),
			nil,
		)
	}
	invalidPayload := func(message string, cause error) error {
		return revision.NewError(
			revision.ErrorCodeUnprocessable,
			fmt.Sprintf("egress profile %d %s", profile.ID, message),
			cause,
		)
	}

	switch profileType {
	case "direct":
		if strings.TrimSpace(profile.ProxyURL) != "" || profile.WireGuardConfig != nil {
			return invalidPayload("direct type cannot include proxy_url or wireguard_config", nil)
		}
	case "socks":
		if err := requireEgressProxyURLScheme(profile.ProxyURL, "socks", "socks5", "socks5h"); err != nil {
			return invalidPayload("has invalid socks proxy_url", err)
		}
		if profile.WireGuardConfig != nil {
			return invalidPayload("socks type cannot include wireguard_config", nil)
		}
	case "http":
		if err := requireEgressProxyURLScheme(profile.ProxyURL, "http"); err != nil {
			return invalidPayload("has invalid HTTP proxy_url", err)
		}
		if profile.WireGuardConfig != nil {
			return invalidPayload("HTTP type cannot include wireguard_config", nil)
		}
	default:
		return invalidPayload("type must be direct, socks, or http", nil)
	}
	return nil
}

func validateSnapshotReferences(target revision.Target, snapshot storage.Snapshot) error {
	relays := map[int]storage.RelayListener{}
	for _, listener := range snapshot.RelayListeners {
		relays[listener.ID] = listener
	}
	egress := map[int]storage.EgressProfile{}
	for _, profile := range snapshot.EgressProfiles {
		egress[profile.ID] = profile
	}
	type certificateReference struct {
		enabled bool
		usable  bool
	}
	certificates := map[int]certificateReference{}
	for _, certificate := range snapshot.Certificates {
		certificates[certificate.ID] = certificateReference{enabled: true, usable: true}
	}
	for _, policy := range snapshot.CertificatePolicies {
		reference := certificates[policy.ID]
		if policy.Enabled {
			reference.enabled = true
			if !isMasterIssuedSnapshotCertificate(policy.IssuerMode) {
				reference.usable = true
			}
		}
		certificates[policy.ID] = reference
	}

	for _, rule := range snapshot.Rules {
		if err := validateSnapshotTargetResourceOwner("HTTP rule", rule.ID, target.AgentID, rule.AgentID); err != nil {
			return err
		}
		if err := validateRelayLayerReferences("HTTP rule", rule.ID, rule.RelayLayers, relays); err != nil {
			return err
		}
		if err := validateEgressReference("HTTP rule", rule.ID, rule.EgressProfileID, egress); err != nil {
			return err
		}
	}
	for _, rule := range snapshot.L4Rules {
		if snapshotL4RuleRetired(rule) {
			continue
		}
		if err := validateSnapshotTargetResourceOwner("L4 rule", rule.ID, target.AgentID, rule.AgentID); err != nil {
			return err
		}
		if err := validateRelayLayerReferences("L4 rule", rule.ID, rule.RelayLayers, relays); err != nil {
			return err
		}
		if err := validateEgressReference("L4 rule", rule.ID, rule.EgressProfileID, egress); err != nil {
			return err
		}
	}
	for _, listener := range snapshot.RelayListeners {
		if snapshotRelayListenerRetired(listener) {
			continue
		}
		listenerAgentID := strings.TrimSpace(listener.AgentID)
		if listenerAgentID == "" {
			listenerAgentID = strings.TrimSpace(target.AgentID)
		}
		if listener.CertificateID != nil {
			reference, found := certificates[*listener.CertificateID]
			if !found {
				return revision.NewError(revision.ErrorCodeNotFound, fmt.Sprintf("relay listener %d references missing certificate %d", listener.ID, *listener.CertificateID), nil)
			}
			if !reference.enabled {
				return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("relay listener %d references disabled certificate %d", listener.ID, *listener.CertificateID), nil)
			}
			if !reference.usable {
				return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("relay listener %d references unavailable certificate %d", listener.ID, *listener.CertificateID), nil)
			}
		}
		for _, certificateID := range listener.TrustedCACertificateIDs {
			reference, found := certificates[certificateID]
			if !found {
				return revision.NewError(revision.ErrorCodeNotFound, fmt.Sprintf("relay listener %d references missing trusted CA %d", listener.ID, certificateID), nil)
			}
			if !reference.enabled {
				return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("relay listener %d references disabled trusted CA %d", listener.ID, certificateID), nil)
			}
			if !reference.usable {
				return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("relay listener %d references unavailable trusted CA %d", listener.ID, certificateID), nil)
			}
		}
	}
	return nil
}

func validateSnapshotTargetResourceOwner(kind string, resourceID int, targetAgentID, resourceAgentID string) error {
	if snapshotResourceBelongsToTarget(targetAgentID, resourceAgentID) {
		return nil
	}
	return revision.NewError(
		revision.ErrorCodeUnprocessable,
		fmt.Sprintf("%s %d belongs to agent %q, not snapshot target %q", kind, resourceID, strings.TrimSpace(resourceAgentID), strings.TrimSpace(targetAgentID)),
		nil,
	)
}

type snapshotListenerClaim struct {
	network string
	host    string
	port    int
	owner   string
}

func validateSnapshotListenerClaims(snapshot storage.Snapshot) error {
	claims := make([]snapshotListenerClaim, 0)
	for _, rule := range snapshot.Rules {
		claim, err := snapshotHTTPListenerClaim(rule)
		if err != nil {
			return err
		}
		claims = append(claims, claim)
	}
	for _, rule := range snapshot.L4Rules {
		if snapshotL4RuleRetired(rule) {
			continue
		}
		network := strings.ToLower(strings.TrimSpace(rule.Protocol))
		if network != "tcp" && network != "udp" {
			return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("L4 rule %d has unsupported protocol %q", rule.ID, rule.Protocol), nil)
		}
		claims = append(claims, snapshotListenerClaim{
			network: network, host: rule.ListenHost, port: rule.ListenPort,
			owner: fmt.Sprintf("L4 rule %d", rule.ID),
		})
	}
	for _, listener := range snapshot.RelayListeners {
		if snapshotRelayListenerRetired(listener) || !listener.Enabled {
			continue
		}
		network := "tcp"
		switch strings.ToLower(strings.TrimSpace(listener.TransportMode)) {
		case "", "tls_tcp":
		case "quic":
			network = "udp"
		default:
			return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("relay listener %d has unsupported transport %q", listener.ID, listener.TransportMode), nil)
		}
		hosts := append([]string(nil), listener.BindHosts...)
		if len(hosts) == 0 {
			hosts = []string{listener.ListenHost}
		}
		for _, host := range hosts {
			claims = append(claims, snapshotListenerClaim{
				network: network, host: host, port: listener.ListenPort,
				owner: fmt.Sprintf("relay listener %d", listener.ID),
			})
		}
	}
	for i := range claims {
		claims[i].host = normalizeSnapshotListenHost(claims[i].host)
		if claims[i].port < 1 || claims[i].port > 65535 {
			return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("%s has an invalid listen port", claims[i].owner), nil)
		}
		for j := 0; j < i; j++ {
			if claims[i].owner == claims[j].owner || claims[i].network != claims[j].network || claims[i].port != claims[j].port {
				continue
			}
			if snapshotHostsOverlap(claims[i].host, claims[j].host) {
				return revision.NewError(
					revision.ErrorCodeConflict,
					fmt.Sprintf("%s conflicts with %s on %s %s:%d", claims[i].owner, claims[j].owner, claims[i].network, claims[i].host, claims[i].port),
					nil,
				)
			}
		}
	}
	return nil
}

func snapshotHTTPListenerClaim(rule storage.HTTPRule) (snapshotListenerClaim, error) {
	parsed, err := url.Parse(strings.TrimSpace(rule.FrontendURL))
	if err != nil || parsed.Host == "" {
		return snapshotListenerClaim{}, revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("HTTP rule %d has an invalid frontend", rule.ID), err)
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	port := 0
	if rawPort := parsed.Port(); rawPort != "" {
		port, err = strconv.Atoi(rawPort)
		if err != nil {
			return snapshotListenerClaim{}, revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("HTTP rule %d has an invalid frontend port", rule.ID), err)
		}
	} else {
		switch scheme {
		case "http":
			port = 80
		case "https":
			port = 443
		default:
			return snapshotListenerClaim{}, revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("HTTP rule %d has an unsupported frontend scheme", rule.ID), nil)
		}
	}
	return snapshotListenerClaim{
		network: "tcp", host: "0.0.0.0", port: port,
		owner: fmt.Sprintf("HTTP %s ingress", scheme),
	}, nil
}

func isMasterIssuedSnapshotCertificate(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	return mode == "" || mode == "master_cf_dns"
}

func canonicalSnapshotFrontend(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", revision.NewError(revision.ErrorCodeUnprocessable, "HTTP frontend URL is invalid", err)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func validateUniqueSnapshotIDs(kind string, ids []int) error {
	seen := map[int]struct{}{}
	for _, id := range ids {
		if id <= 0 {
			return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("%s id must be positive", kind), nil)
		}
		if _, exists := seen[id]; exists {
			return revision.NewError(revision.ErrorCodeConflict, fmt.Sprintf("%s id %d is duplicated", kind, id), nil)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func relaySnapshotIDs(rows []storage.RelayListener) []int {
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if snapshotRelayListenerRetired(row) {
			continue
		}
		ids = append(ids, row.ID)
	}
	return ids
}

func egressSnapshotIDs(rows []storage.EgressProfile) []int {
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		if snapshotEgressProfileRetired(row) {
			continue
		}
		ids = append(ids, row.ID)
	}
	return ids
}

func validateRelayLayerReferences(kind string, resourceID int, layers [][]int, relays map[int]storage.RelayListener) error {
	for _, layer := range layers {
		for _, relayID := range layer {
			listener, found := relays[relayID]
			if !found {
				return revision.NewError(revision.ErrorCodeNotFound, fmt.Sprintf("%s %d references missing relay listener %d", kind, resourceID, relayID), nil)
			}
			if snapshotRelayListenerRetired(listener) {
				continue
			}
			if !listener.Enabled {
				return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("%s %d references disabled relay listener %d", kind, resourceID, relayID), nil)
			}
		}
	}
	return nil
}

func validateEgressReference(kind string, resourceID int, profileID *int, profiles map[int]storage.EgressProfile) error {
	if profileID == nil {
		return nil
	}
	profile, found := profiles[*profileID]
	if !found {
		return revision.NewError(revision.ErrorCodeNotFound, fmt.Sprintf("%s %d references missing egress profile %d", kind, resourceID, *profileID), nil)
	}
	if snapshotEgressProfileRetired(profile) {
		return nil
	}
	if !profile.Enabled {
		return revision.NewError(revision.ErrorCodeUnprocessable, fmt.Sprintf("%s %d references disabled egress profile %d", kind, resourceID, *profileID), nil)
	}
	return nil
}

func normalizeSnapshotListenHost(host string) string {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" || host == "*" {
		return "0.0.0.0"
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return parsed.String()
	}
	return strings.ToLower(host)
}

func snapshotHostsOverlap(left, right string) bool {
	if left == right {
		return true
	}
	return isSnapshotWildcardHost(left) || isSnapshotWildcardHost(right)
}

func isSnapshotWildcardHost(host string) bool {
	return host == "0.0.0.0" || host == "::"
}
