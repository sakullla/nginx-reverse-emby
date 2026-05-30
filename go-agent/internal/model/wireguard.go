package model

type WireGuardPeer struct {
	Name                       string   `json:"name"`
	PublicKey                  string   `json:"public_key"`
	PresharedKey               string   `json:"preshared_key,omitempty"`
	Endpoint                   string   `json:"endpoint"`
	AllowedIPs                 []string `json:"allowed_ips"`
	Reserved                   []byte   `json:"reserved,omitempty"`
	PersistentKeepaliveSeconds int      `json:"persistent_keepalive_seconds,omitempty"`
}

type WireGuardProfile struct {
	ID             int             `json:"id"`
	AgentID        string          `json:"agent_id"`
	Name           string          `json:"name"`
	Mode           string          `json:"mode"`
	PrivateKey     string          `json:"private_key,omitempty"`
	ListenPort     int             `json:"listen_port"`
	PublicEndpoint string          `json:"public_endpoint,omitempty"`
	BindAddresses  []string        `json:"bind_addresses,omitempty"`
	Addresses      []string        `json:"addresses"`
	Peers          []WireGuardPeer `json:"peers"`
	DNS            []string        `json:"dns"`
	MTU            int             `json:"mtu"`
	Enabled        bool            `json:"enabled"`
	Tags           []string        `json:"tags"`
	Revision       int64           `json:"revision"`
}
