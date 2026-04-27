package model

import "encoding/json"

type AgentConfig struct {
	OutboundProxyURL string `json:"outbound_proxy_url,omitempty"`
}

type Snapshot struct {
	DesiredVersion      string                     `json:"desired_version"`
	Revision            int64                      `json:"desired_revision"`
	VersionPackage      *VersionPackage            `json:"version_package,omitempty"`
	AgentConfig         AgentConfig                `json:"agent_config,omitempty"`
	Rules               []HTTPRule                 `json:"rules"`
	L4Rules             []L4Rule                   `json:"l4_rules"`
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
