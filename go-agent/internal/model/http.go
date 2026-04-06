package model

type HTTPRoute struct {
	Host            string            `json:"host"`
	BackendURL      string            `json:"backend_url"`
	ProxyRedirect   bool              `json:"proxy_redirect,omitempty"`
	FrontendOrigin  string            `json:"frontend_origin,omitempty"`
	HeaderOverrides map[string]string `json:"header_overrides,omitempty"`
}

type HTTPListener struct {
	HTTPProxyConfig HTTPProxyConfig `json:"http_proxy_config,omitempty"`
	Routes          []HTTPRoute     `json:"routes,omitempty"`
}
