package model

import "encoding/json"

type AgentConfig struct {
	OutboundProxyURL     string `json:"outbound_proxy_url,omitempty"`
	TrafficStatsInterval string `json:"traffic_stats_interval,omitempty"`
	TrafficStatsEnabled  *bool  `json:"traffic_stats_enabled,omitempty"`
	TrafficBlocked       bool   `json:"traffic_blocked,omitempty"`
	TrafficBlockReason   string `json:"traffic_block_reason,omitempty"`
}

// DDNSExtractConfig is the per-agent dynamic DNS extraction configuration the
// master dispatches to the agent via the heartbeat Snapshot. JSON tags mirror
// the control-plane wire contract exactly (panel controlplane DDNSConfig) so
// the field round-trips without translation.
//
// SECURITY (R7): this struct carries only the domain plus per-family extraction
// strategy. It MUST NOT carry any Cloudflare credential — tokens live only in
// the master process environment and are never dispatched to agents.
type DDNSExtractConfig struct {
	// Enabled is the per-agent master switch. It always serializes on the
	// master (no omitempty) so an explicit off reaches the agent intact.
	Enabled bool       `json:"enabled"`
	Domain  string     `json:"domain,omitempty"`
	IPv4    DDNSFamily `json:"ipv4,omitempty"`
	IPv6    DDNSFamily `json:"ipv6,omitempty"`
}

// UnmarshalJSON derives Enabled for dispatches from a master that predates the
// switch: no "enabled" key means "whatever the family flags say", so upgrading
// the agent first never silently halts extraction. An explicit key always wins.
func (c *DDNSExtractConfig) UnmarshalJSON(data []byte) error {
	type wire DDNSExtractConfig
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	if _, present := keys["enabled"]; !present {
		decoded.Enabled = decoded.IPv4.Enabled || decoded.IPv6.Enabled
	}
	*c = DDNSExtractConfig(decoded)
	return nil
}

// DDNSFamily describes how one address family is extracted on the agent.
// Source is "public_api" (probe a public echo endpoint) or "interface" (read
// the address of the named network interface). IPv4 and IPv6 are independent.
type DDNSFamily struct {
	Enabled   bool   `json:"enabled"`
	Source    string `json:"source,omitempty"`
	Interface string `json:"interface,omitempty"`
}

type Snapshot struct {
	DesiredVersion      string                     `json:"desired_version"`
	Revision            int64                      `json:"desired_revision"`
	VersionPackage      *VersionPackage            `json:"version_package,omitempty"`
	AgentConfig         AgentConfig                `json:"agent_config,omitempty"`
	DDNSConfig          *DDNSExtractConfig         `json:"ddns_config,omitempty"`
	Rules               []HTTPRule                 `json:"rules"`
	L4Rules             []L4Rule                   `json:"l4_rules"`
	EgressProfiles      []EgressProfile            `json:"egress_profiles"`
	RelayListeners      []RelayListener            `json:"relay_listeners"`
	Certificates        []ManagedCertificateBundle `json:"certificates"`
	CertificatePolicies []ManagedCertificatePolicy `json:"certificate_policies"`
	agentConfigPresent  bool
}

func (s Snapshot) HasAgentConfig() bool {
	return s.agentConfigPresent || s.AgentConfig != (AgentConfig{})
}

func (s *Snapshot) UnmarshalJSON(data []byte) error {
	type snapshotAlias Snapshot
	var decoded snapshotAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	*s = Snapshot(decoded)
	_, s.agentConfigPresent = fields["agent_config"]
	return nil
}

type RuntimeState struct {
	NodeID          string            `json:"node_id,omitempty"`
	CurrentRevision int64             `json:"current_revision,omitempty"`
	Status          string            `json:"status,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type EgressProfile struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ProxyURL    string `json:"proxy_url,omitempty"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
	Revision    int64  `json:"revision"`
}
