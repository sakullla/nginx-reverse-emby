package model

type Snapshot struct {
	DesiredVersion string
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
