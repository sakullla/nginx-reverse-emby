package model

type Snapshot struct {
	DesiredVersion string
}

type HTTPProxyConfig struct {
	FrontendOrigin  string
	HeaderOverrides map[string]string
}
