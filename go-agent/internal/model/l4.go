package model

type L4Backend struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type L4ProxyProtocolTuning struct {
	Decode bool `json:"decode,omitempty"`
	Send   bool `json:"send,omitempty"`
}

type L4Tuning struct {
	ProxyProtocol L4ProxyProtocolTuning `json:"proxy_protocol,omitempty"`
}

type L4Rule struct {
	Protocol      string        `json:"protocol"`
	ListenHost    string        `json:"listen_host"`
	ListenPort    int           `json:"listen_port"`
	UpstreamHost  string        `json:"upstream_host"`
	UpstreamPort  int           `json:"upstream_port"`
	Backends      []L4Backend   `json:"backends,omitempty"`
	LoadBalancing LoadBalancing `json:"load_balancing,omitempty"`
	Tuning        L4Tuning      `json:"tuning,omitempty"`
	RelayChain    []int         `json:"relay_chain,omitempty"`
	RelayObfs     bool          `json:"relay_obfs,omitempty"`
	Revision      int64         `json:"revision,omitempty"`
}
