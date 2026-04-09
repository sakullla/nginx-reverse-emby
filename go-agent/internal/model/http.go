package model

type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HTTPBackend struct {
	URL string `json:"url"`
}

type LoadBalancing struct {
	Strategy string `json:"strategy,omitempty"`
}

type HTTPRule struct {
	FrontendURL      string        `json:"frontend_url"`
	BackendURL       string        `json:"backend_url"`
	Backends         []HTTPBackend `json:"backends,omitempty"`
	LoadBalancing    LoadBalancing `json:"load_balancing,omitempty"`
	ProxyRedirect    bool          `json:"proxy_redirect,omitempty"`
	PassProxyHeaders bool          `json:"pass_proxy_headers,omitempty"`
	UserAgent        string        `json:"user_agent,omitempty"`
	CustomHeaders    []HTTPHeader  `json:"custom_headers,omitempty"`
	RelayChain       []int         `json:"relay_chain,omitempty"`
	Revision         int64         `json:"revision,omitempty"`
}

type HTTPListener struct {
	Rules []HTTPRule `json:"rules,omitempty"`
}
