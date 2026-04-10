package storage

type AgentRow struct {
	ID                string
	Name              string
	AgentURL          string
	AgentToken        string
	Version           string
	Platform          string
	DesiredVersion    string
	TagsJSON          string
	CapabilitiesJSON  string
	Mode              string
	DesiredRevision   int
	CurrentRevision   int
	LastApplyRevision int
	LastApplyStatus   string
	LastApplyMessage  string
	LastSeenAt        string
	LastSeenIP        string
	IsLocal           bool
}

type HTTPRuleRow struct {
	ID                int
	AgentID           string
	FrontendURL       string
	BackendURL        string
	BackendsJSON      string
	LoadBalancingJSON string
	Enabled           bool
	TagsJSON          string
	ProxyRedirect     bool
	RelayChainJSON    string
	PassProxyHeaders  bool
	UserAgent         string
	CustomHeadersJSON string
	Revision          int
}

type LocalAgentStateRow struct {
	DesiredRevision   int
	CurrentRevision   int
	LastApplyRevision int
	LastApplyStatus   string
	LastApplyMessage  string
	DesiredVersion    string
}
