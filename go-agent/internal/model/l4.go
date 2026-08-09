package model

import "encoding/json"

type L4Backend struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type L4ProxyProtocolTuning struct {
	Decode       bool     `json:"decode,omitempty"`
	Send         bool     `json:"send,omitempty"`
	TrustedPeers []string `json:"trusted_peers,omitempty"`
}

type L4ProxyEntryAuth struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type L4Tuning struct {
	ProxyProtocol L4ProxyProtocolTuning `json:"proxy_protocol,omitempty"`
}

type L4Rule struct {
	ID         int    `json:"id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Protocol   string `json:"protocol"`
	ListenHost string `json:"listen_host"`
	ListenPort int    `json:"listen_port"`
	// UpstreamHost is retained only to ignore legacy payloads; runtime uses Backends.
	UpstreamHost string `json:"upstream_host"`
	// UpstreamPort is retained only to ignore legacy payloads; runtime uses Backends.
	UpstreamPort  int           `json:"upstream_port"`
	Backends      []L4Backend   `json:"backends,omitempty"`
	LoadBalancing LoadBalancing `json:"load_balancing,omitempty"`
	Tuning        L4Tuning      `json:"tuning,omitempty"`
	// RelayChain is retained only to ignore legacy payloads; runtime uses RelayLayers.
	RelayChain      []int            `json:"relay_chain,omitempty"`
	RelayLayers     [][]int          `json:"relay_layers,omitempty"`
	RelayObfs       bool             `json:"relay_obfs,omitempty"`
	ListenMode      string           `json:"listen_mode,omitempty"`
	EgressProfileID *int             `json:"egress_profile_id,omitempty"`
	ProxyEntryAuth  L4ProxyEntryAuth `json:"proxy_entry_auth,omitempty"`
	PolicyRef       *PolicyRef       `json:"policy_ref,omitempty"`
	Enabled         bool             `json:"enabled"`
	Tags            []string         `json:"tags,omitempty"`
	Revision        int64            `json:"revision,omitempty"`
}

func (r *L4Rule) UnmarshalJSON(data []byte) error {
	// Older masters runtime-filtered rules but omitted enabled from the persisted payload.
	type wire L4Rule
	decoded := wire{Enabled: true}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = L4Rule(decoded)
	return nil
}
