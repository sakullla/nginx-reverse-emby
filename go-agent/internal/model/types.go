package model

type Snapshot struct {
	DesiredVersion string
}

type RuntimeState struct {
	NodeID   string            `json:"node_id,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type HTTPProxyConfig struct {
	FrontendOrigin  string
	HeaderOverrides map[string]string
}

type L4Rule struct {
	Protocol   string
	RelayChain []int
}

type RelayListener struct {
	TLSMode                 string
	PinSet                  []string
	TrustedCACertificateIDs []int
}
