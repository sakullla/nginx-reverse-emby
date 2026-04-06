package model

type L4Rule struct {
	Protocol     string `json:"protocol"`
	ListenHost   string `json:"listen_host"`
	ListenPort   int    `json:"listen_port"`
	UpstreamHost string `json:"upstream_host"`
	UpstreamPort int    `json:"upstream_port"`
	RelayChain   []int  `json:"relay_chain,omitempty"`
	Revision     int64  `json:"revision,omitempty"`
}
