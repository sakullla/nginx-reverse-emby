package storage

type AgentRow struct {
	ID                string
	Name              string
	AgentURL          string
	AgentToken        string
	Version           string
	Platform          string
	DesiredVersion    string
	DesiredRevision   int
	CurrentRevision   int
	LastApplyRevision int
	LastApplyStatus   string
	LastApplyMessage  string
	IsLocal           bool
}

type HTTPRuleRow struct {
	ID          int
	AgentID     string
	FrontendURL string
	BackendURL  string
	Enabled     bool
	Revision    int
}

type LocalAgentStateRow struct {
	DesiredRevision   int
	CurrentRevision   int
	LastApplyRevision int
	LastApplyStatus   string
	LastApplyMessage  string
	DesiredVersion    string
}
