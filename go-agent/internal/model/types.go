package model

import "encoding/json"

type AgentConfig struct {
	OutboundProxyURL     string `json:"outbound_proxy_url,omitempty"`
	TrafficStatsInterval string `json:"traffic_stats_interval,omitempty"`
	TrafficStatsEnabled  *bool  `json:"traffic_stats_enabled,omitempty"`
	TrafficBlocked       bool   `json:"traffic_blocked,omitempty"`
	TrafficBlockReason   string `json:"traffic_block_reason,omitempty"`
}

type Snapshot struct {
	DesiredVersion      string                     `json:"desired_version"`
	Revision            int64                      `json:"desired_revision"`
	VersionPackage      *VersionPackage            `json:"version_package,omitempty"`
	AgentConfig         AgentConfig                `json:"agent_config,omitempty"`
	Rules               []HTTPRule                 `json:"rules"`
	L4Rules             []L4Rule                   `json:"l4_rules"`
	EgressProfiles      []EgressProfile            `json:"egress_profiles"`
	RelayListeners      []RelayListener            `json:"relay_listeners"`
	WireGuardProfiles   []WireGuardProfile         `json:"wireguard_profiles"`
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
	ID              int                    `json:"id"`
	Name            string                 `json:"name"`
	Type            string                 `json:"type"`
	ProxyURL        string                 `json:"proxy_url,omitempty"`
	WireGuardConfig *EgressWireGuardConfig `json:"wireguard_config,omitempty"`
	Enabled         bool                   `json:"enabled,omitempty"`
	Description     string                 `json:"description,omitempty"`
	Revision        int64                  `json:"revision,omitempty"`
}

type EgressWireGuardConfig struct {
	PrivateKey string          `json:"private_key,omitempty"`
	Addresses  []string        `json:"addresses,omitempty"`
	Peers      []WireGuardPeer `json:"peers,omitempty"`
	DNS        []string        `json:"dns,omitempty"`
	MTU        int             `json:"mtu,omitempty"`
}
