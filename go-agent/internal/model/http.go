package model

type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HTTPRule struct {
	FrontendURL      string       `json:"frontend_url"`
	BackendURL       string       `json:"backend_url"`
	ProxyRedirect    bool         `json:"proxy_redirect,omitempty"`
	PassProxyHeaders bool         `json:"pass_proxy_headers,omitempty"`
	UserAgent        string       `json:"user_agent,omitempty"`
	CustomHeaders    []HTTPHeader `json:"custom_headers,omitempty"`
	Revision         int64        `json:"revision,omitempty"`
}

type HTTPListener struct {
	Rules []HTTPRule `json:"rules,omitempty"`
}
