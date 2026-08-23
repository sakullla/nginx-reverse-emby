package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const (
	EnvPluginHostEndpoint      = "NRE_PLUGIN_HOST_ENDPOINT"
	HeaderPluginHostCredential = "X-NRE-Plugin-Host-Credential"
	PluginHostCallPath         = "/nre.plugin.host.v1/call"
	PluginHostPayloadMaxBytes  = 1 << 20

	HostRuntimePluginCall       = "plugin.call"
	HostRuntimeHTTPRule         = "http.rule"
	HostRuntimeHTTPBackendOffer = "http.backend-offer"
	HostRuntimeL4Rule           = "l4.rule"
	HostRuntimeChannelReverse   = "channel.reverse"
	HostRuntimeEventEmit        = "event.emit"
	HTTPBackendOfferCatalogKey  = "http.backend-offers"
)

type HostRuntimeCall struct {
	Operation   string          `json:"operation"`
	OperationID string          `json:"operation_id,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

func (call HostRuntimeCall) Validate() error {
	if err := ValidatePolicyIdentity(call.Operation); err != nil {
		return fmt.Errorf("host runtime operation: %w", err)
	}
	if call.OperationID != "" {
		if err := ValidatePolicyIdentity(call.OperationID); err != nil {
			return fmt.Errorf("host runtime operation id: %w", err)
		}
	}
	if len(call.Payload) > PluginHostPayloadMaxBytes || (len(call.Payload) > 0 && !json.Valid(call.Payload)) {
		return errors.New("host runtime payload is invalid or exceeds the canonical bound")
	}
	return nil
}

type HostRuntimeResponse struct {
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *RuntimeError   `json:"error,omitempty"`
}

func (response HostRuntimeResponse) Validate() error {
	if len(response.Payload) > PluginHostPayloadMaxBytes || (len(response.Payload) > 0 && !json.Valid(response.Payload)) {
		return errors.New("host runtime response payload is invalid or exceeds the canonical bound")
	}
	if response.Error != nil {
		if err := response.Error.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type HostRuntimeClient struct {
	client     *http.Client
	credential string
}

func NewHostRuntimeClientFromEnvironment() (*HostRuntimeClient, error) {
	endpoint := strings.TrimSpace(os.Getenv(EnvPluginHostEndpoint))
	if endpoint == "" {
		return nil, errors.New("plugin host runtime endpoint is unavailable")
	}
	network, address, ok := strings.Cut(endpoint, ":")
	if !ok || network != "unix" || strings.TrimSpace(address) == "" {
		return nil, errors.New("plugin host runtime endpoint is invalid")
	}
	cookieFile := strings.TrimSpace(os.Getenv("NRE_PLUGIN_COOKIE_FILE"))
	credential, err := os.ReadFile(cookieFile)
	if err != nil || strings.TrimSpace(string(credential)) == "" {
		return nil, errors.New("plugin host runtime credential is unavailable")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}
	return &HostRuntimeClient{
		client:     &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		credential: strings.TrimSpace(string(credential)),
	}, nil
}

func (client *HostRuntimeClient) Call(ctx context.Context, call HostRuntimeCall, result any) error {
	if client == nil || client.client == nil || strings.TrimSpace(client.credential) == "" {
		return errors.New("plugin host runtime client is unavailable")
	}
	if err := call.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(call)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://plugin-host"+PluginHostCallPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderPluginHostCredential, client.credential)
	response, err := client.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, PluginHostPayloadMaxBytes+4096))
	if err != nil {
		return err
	}
	if len(encoded) > PluginHostPayloadMaxBytes+2048 {
		return errors.New("plugin host runtime response exceeds the canonical bound")
	}
	var wire HostRuntimeResponse
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return fmt.Errorf("decode plugin host runtime response: %w", err)
	}
	if err := wire.Validate(); err != nil {
		return err
	}
	if wire.Error != nil {
		return wire.Error
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("plugin host runtime returned status %d", response.StatusCode)
	}
	if result == nil || len(wire.Payload) == 0 {
		return nil
	}
	decoder = json.NewDecoder(bytes.NewReader(wire.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("decode plugin host runtime payload: %w", err)
	}
	return nil
}

// PluginCallRequest is the opaque control-plane → Agent execution-face
// envelope. Hosts must forward Name and Payload without interpreting them.
type PluginCallRequest struct {
	AgentID string          `json:"agent_id"`
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func (request PluginCallRequest) Validate() error {
	if err := ValidatePolicyIdentity(request.AgentID); err != nil {
		return fmt.Errorf("plugin.call agent id: %w", err)
	}
	if err := ValidatePolicyIdentity(request.Name); err != nil {
		return fmt.Errorf("plugin.call name: %w", err)
	}
	if len(request.Payload) > PluginHostPayloadMaxBytes || (len(request.Payload) > 0 && !json.Valid(request.Payload)) {
		return errors.New("plugin.call payload is invalid or exceeds the canonical bound")
	}
	return nil
}

const (
	HTTPRuleActionCreate  = "create"
	HTTPRuleActionCutover = "cutover"
	HTTPRuleActionList    = "list"
)

// HTTPRuleRequest creates, lists, or switches a control-plane HTTP rule.
// Cutover with an empty RuleRef is an explicit error and must not rewrite
// other rules. Create Domain accepts a hostname or an http(s) URL; the host
// normalizes by stripping a path and preserving the frontend scheme.
type HTTPRuleRequest struct {
	Action  string `json:"action"`
	AgentID string `json:"agent_id,omitempty"`
	Domain  string `json:"domain,omitempty"`
	Port    int    `json:"port,omitempty"`
	RuleRef string `json:"rule_ref,omitempty"`
}

func (request HTTPRuleRequest) Validate() error {
	switch request.Action {
	case HTTPRuleActionCreate:
		if err := ValidatePolicyIdentity(request.AgentID); err != nil {
			return fmt.Errorf("http.rule agent id: %w", err)
		}
		if _, err := NormalizeHTTPRuleFrontend(request.Domain); err != nil {
			return errors.New("http.rule domain is invalid")
		}
		if request.Port <= 0 || request.Port > 65535 {
			return errors.New("http.rule port is invalid")
		}
		if strings.TrimSpace(request.RuleRef) != "" {
			return errors.New("http.rule create does not accept rule_ref")
		}
		return nil
	case HTTPRuleActionCutover:
		if strings.TrimSpace(request.RuleRef) == "" {
			return errors.New("http.rule cutover rule_ref is required")
		}
		if err := ValidatePolicyIdentity(request.RuleRef); err != nil {
			return fmt.Errorf("http.rule rule_ref: %w", err)
		}
		if request.AgentID != "" {
			if err := ValidatePolicyIdentity(request.AgentID); err != nil {
				return fmt.Errorf("http.rule agent id: %w", err)
			}
		}
		if request.Domain != "" {
			if _, err := NormalizeHTTPRuleFrontend(request.Domain); err != nil {
				return errors.New("http.rule domain is invalid")
			}
		}
		if request.Port < 0 || request.Port > 65535 {
			return errors.New("http.rule port is invalid")
		}
		return nil
	case HTTPRuleActionList:
		if err := ValidatePolicyIdentity(request.AgentID); err != nil {
			return fmt.Errorf("http.rule agent id: %w", err)
		}
		if request.Domain != "" || request.Port != 0 || strings.TrimSpace(request.RuleRef) != "" {
			return errors.New("http.rule list does not accept domain, port, or rule_ref")
		}
		return nil
	default:
		return fmt.Errorf("http.rule action %q is unsupported", request.Action)
	}
}

// HTTPRuleListItem is one host-owned HTTP rule as returned by http.rule list.
type HTTPRuleListItem struct {
	RuleRef     string `json:"rule_ref"`
	FrontendURL string `json:"frontend_url"`
	Backend     string `json:"backend"`
	Enabled     bool   `json:"enabled"`
}

// HTTPRuleListResponse is the http.rule list payload.
type HTTPRuleListResponse struct {
	Rules []HTTPRuleListItem `json:"rules"`
}

// NormalizeHTTPRuleFrontend accepts a hostname or an http(s) URL, strips any
// path/query/fragment, and returns a canonical frontend URL. A bare hostname
// becomes an http frontend; an https:// input keeps an HTTPS frontend.
func NormalizeHTTPRuleFrontend(domain string) (string, error) {
	if domain == "" || domain != strings.TrimSpace(domain) || strings.ContainsAny(domain, "\r\n\x00") {
		return "", errors.New("http.rule domain is invalid")
	}
	scheme := "http"
	host := domain
	lower := strings.ToLower(domain)
	switch {
	case strings.HasPrefix(lower, "https://"):
		scheme = "https"
		host = domain[len("https://"):]
	case strings.HasPrefix(lower, "http://"):
		scheme = "http"
		host = domain[len("http://"):]
	case strings.Contains(domain, "://"):
		return "", errors.New("http.rule domain is invalid")
	}
	if cut := strings.IndexAny(host, "/?#"); cut >= 0 {
		if cut == 0 {
			return "", errors.New("http.rule domain is invalid")
		}
		host = host[:cut]
	}
	host = strings.TrimSpace(host)
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " \r\n\x00/") {
		return "", errors.New("http.rule domain is invalid")
	}
	frontend := scheme + "://" + host
	parsed, err := url.Parse(frontend)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("http.rule domain is invalid")
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "", errors.New("http.rule domain is invalid")
	}
	return strings.ToLower(parsed.Scheme) + "://" + parsed.Host, nil
}

const (
	L4RuleActionCreate  = "create"
	L4RuleActionUpdate  = "update"
	L4RuleActionEnable  = "enable"
	L4RuleActionDisable = "disable"
	L4RuleActionDelete  = "delete"

	L4RuleProtocolTCP = "tcp"
	L4RuleProtocolUDP = "udp"
)

// L4RuleBackend is one host:port target of a host-managed L4 rule.
type L4RuleBackend struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// L4RuleLoadBalancing selects the backend selection strategy.
type L4RuleLoadBalancing struct {
	Strategy string `json:"strategy"`
}

// L4RuleProxyProtocolTuning controls PROXY protocol decode/send behavior.
type L4RuleProxyProtocolTuning struct {
	Decode       bool     `json:"decode"`
	Send         bool     `json:"send"`
	TrustedPeers []string `json:"trusted_peers,omitempty"`
}

// L4RuleTuning carries optional per-rule behavior tuning.
type L4RuleTuning struct {
	ProxyProtocol L4RuleProxyProtocolTuning `json:"proxy_protocol"`
}

// L4RuleRequest drives the full lifecycle of a host-managed L4 rule. Create
// and update carry the rule specification; enable, disable, and delete
// address an existing rule by RuleRef only. The rule stays host-owned and is
// attributed to the calling plugin.
type L4RuleRequest struct {
	Action          string               `json:"action"`
	AgentID         string               `json:"agent_id,omitempty"`
	Name            string               `json:"name,omitempty"`
	Protocol        string               `json:"protocol,omitempty"`
	ListenHost      string               `json:"listen_host,omitempty"`
	ListenPort      int                  `json:"listen_port,omitempty"`
	Backends        []L4RuleBackend      `json:"backends,omitempty"`
	LoadBalancing   *L4RuleLoadBalancing `json:"load_balancing,omitempty"`
	Tuning          *L4RuleTuning        `json:"tuning,omitempty"`
	RelayLayers     [][]int              `json:"relay_layers,omitempty"`
	RelayObfs       bool                 `json:"relay_obfs,omitempty"`
	ListenMode      string               `json:"listen_mode,omitempty"`
	EgressProfileID *int                 `json:"egress_profile_id,omitempty"`
	Enabled         *bool                `json:"enabled,omitempty"`
	Tags            []string             `json:"tags,omitempty"`
	RuleRef         string               `json:"rule_ref,omitempty"`
}

func (request L4RuleRequest) Validate() error {
	if err := validateL4RuleRef("l4.rule", request.Action, request.RuleRef); err != nil {
		return err
	}
	if request.AgentID != "" {
		if err := ValidatePolicyIdentity(request.AgentID); err != nil {
			return fmt.Errorf("l4.rule agent id: %w", err)
		}
	}
	switch request.Action {
	case L4RuleActionCreate:
		if request.AgentID == "" {
			return errors.New("l4.rule create agent id is required")
		}
		return request.validateSpec()
	case L4RuleActionUpdate:
		return request.validateSpec()
	case L4RuleActionEnable, L4RuleActionDisable, L4RuleActionDelete:
		return nil
	default:
		return fmt.Errorf("l4.rule action %q is unsupported", request.Action)
	}
}

func validateL4RuleRef(operation, action, ruleRef string) error {
	switch action {
	case L4RuleActionCreate:
		if strings.TrimSpace(ruleRef) != "" {
			return fmt.Errorf("%s create does not accept rule_ref", operation)
		}
		return nil
	case L4RuleActionUpdate, L4RuleActionEnable, L4RuleActionDisable, L4RuleActionDelete:
		if strings.TrimSpace(ruleRef) == "" {
			return fmt.Errorf("%s %s rule_ref is required", operation, action)
		}
		if err := ValidatePolicyIdentity(ruleRef); err != nil {
			return fmt.Errorf("%s rule_ref: %w", operation, err)
		}
		return nil
	default:
		return nil
	}
}

func (request L4RuleRequest) validateSpec() error {
	switch request.Protocol {
	case "":
		if request.Action == L4RuleActionCreate {
			return errors.New("l4.rule protocol is required")
		}
	case L4RuleProtocolTCP, L4RuleProtocolUDP:
	default:
		return fmt.Errorf("l4.rule protocol %q is unsupported", request.Protocol)
	}
	if request.ListenPort < 0 || request.ListenPort > 65535 {
		return errors.New("l4.rule listen port is invalid")
	}
	if request.Action == L4RuleActionCreate && request.ListenPort == 0 {
		return errors.New("l4.rule listen port is required")
	}
	if err := validateL4RuleEndpoint("l4.rule listen host", request.ListenHost, true); err != nil {
		return err
	}
	if err := validateL4RuleName(request.Name); err != nil {
		return err
	}
	for _, backend := range request.Backends {
		if err := validateL4RuleEndpoint("l4.rule backend host", backend.Host, false); err != nil {
			return err
		}
		if backend.Port <= 0 || backend.Port > 65535 {
			return errors.New("l4.rule backend port is invalid")
		}
	}
	if request.LoadBalancing != nil && request.LoadBalancing.Strategy != "" {
		if err := ValidatePolicyIdentity(request.LoadBalancing.Strategy); err != nil {
			return fmt.Errorf("l4.rule load balancing strategy: %w", err)
		}
	}
	if request.ListenMode != "" {
		if err := ValidatePolicyIdentity(request.ListenMode); err != nil {
			return fmt.Errorf("l4.rule listen mode: %w", err)
		}
	}
	if request.EgressProfileID != nil && *request.EgressProfileID <= 0 {
		return errors.New("l4.rule egress profile id is invalid")
	}
	for _, layer := range request.RelayLayers {
		for _, hop := range layer {
			if hop <= 0 {
				return errors.New("l4.rule relay layer hop is invalid")
			}
		}
	}
	for _, tag := range request.Tags {
		if err := validateL4RuleTag(tag); err != nil {
			return err
		}
	}
	return nil
}

func validateL4RuleName(name string) error {
	if name == "" {
		return nil
	}
	if name != strings.TrimSpace(name) || len(name) > 128 || strings.ContainsAny(name, "\r\n\x00") {
		return errors.New("l4.rule name is invalid")
	}
	return nil
}

func validateL4RuleEndpoint(field, host string, optional bool) error {
	if host == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("%s is required", field)
	}
	if host != strings.TrimSpace(host) || len(host) > 253 || strings.ContainsAny(host, "\r\n\x00 /") {
		return fmt.Errorf("%s is invalid", field)
	}
	return nil
}

func validateL4RuleTag(tag string) error {
	if tag == "" || tag != strings.TrimSpace(tag) || len(tag) > 128 || strings.ContainsAny(tag, "\r\n\x00") {
		return errors.New("l4.rule tag is invalid")
	}
	return nil
}

// L4RuleResponse reports the host-owned rule reference after a mutation.
type L4RuleResponse struct {
	RuleRef string `json:"rule_ref"`
	Enabled bool   `json:"enabled,omitempty"`
}

func (response L4RuleResponse) Validate() error {
	if err := ValidatePolicyIdentity(response.RuleRef); err != nil {
		return fmt.Errorf("l4.rule rule_ref: %w", err)
	}
	return nil
}

const (
	ChannelReverseActionEnsure   = "ensure"
	ChannelReverseActionStatus   = "status"
	ChannelReverseActionTeardown = "teardown"

	ChannelReverseStateOnline  = "online"
	ChannelReverseStateOffline = "offline"
)

// ChannelReverseRequest manages a host-mediated reverse channel session: the
// exit agent dials out to the entry agent and the host bridges accepted entry
// traffic back over the established channel. Ensure creates or re-attaches a
// session (idempotent on SessionRef), status reports connectivity, teardown
// releases it. An optional relay chain routes the channel through host relay
// listeners.
type ChannelReverseRequest struct {
	Action       string `json:"action"`
	SessionRef   string `json:"session_ref,omitempty"`
	EntryAgentID string `json:"entry_agent_id,omitempty"`
	ExitAgentID  string `json:"exit_agent_id,omitempty"`
	Protocol     string `json:"protocol,omitempty"`
	BackendHost  string `json:"backend_host,omitempty"`
	BackendPort  int    `json:"backend_port,omitempty"`
	RelayChain   []int  `json:"relay_chain,omitempty"`
}

func (request ChannelReverseRequest) Validate() error {
	switch request.Action {
	case ChannelReverseActionEnsure:
		if err := ValidatePolicyIdentity(request.EntryAgentID); err != nil {
			return fmt.Errorf("channel.reverse entry agent id: %w", err)
		}
		if err := ValidatePolicyIdentity(request.ExitAgentID); err != nil {
			return fmt.Errorf("channel.reverse exit agent id: %w", err)
		}
		switch request.Protocol {
		case L4RuleProtocolTCP, L4RuleProtocolUDP:
		default:
			return fmt.Errorf("channel.reverse protocol %q is unsupported", request.Protocol)
		}
		if err := validateL4RuleEndpoint("channel.reverse backend host", request.BackendHost, false); err != nil {
			return err
		}
		if request.BackendPort <= 0 || request.BackendPort > 65535 {
			return errors.New("channel.reverse backend port is invalid")
		}
		for _, hop := range request.RelayChain {
			if hop <= 0 {
				return errors.New("channel.reverse relay chain hop is invalid")
			}
		}
		if request.SessionRef != "" {
			if err := ValidatePolicyIdentity(request.SessionRef); err != nil {
				return fmt.Errorf("channel.reverse session_ref: %w", err)
			}
		}
		return nil
	case ChannelReverseActionStatus, ChannelReverseActionTeardown:
		if strings.TrimSpace(request.SessionRef) == "" {
			return fmt.Errorf("channel.reverse %s session_ref is required", request.Action)
		}
		if err := ValidatePolicyIdentity(request.SessionRef); err != nil {
			return fmt.Errorf("channel.reverse session_ref: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("channel.reverse action %q is unsupported", request.Action)
	}
}

// ChannelReverseResponse reports the session reference and its connectivity
// state. The bridge endpoint is the entry-agent loopback address an L4 rule
// backend may point at; it is assigned by the host on ensure.
type ChannelReverseResponse struct {
	SessionRef string `json:"session_ref"`
	State      string `json:"state"`
	BridgeHost string `json:"bridge_host,omitempty"`
	BridgePort int    `json:"bridge_port,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

func (response ChannelReverseResponse) Validate() error {
	if err := ValidatePolicyIdentity(response.SessionRef); err != nil {
		return fmt.Errorf("channel.reverse session_ref: %w", err)
	}
	switch response.State {
	case ChannelReverseStateOnline, ChannelReverseStateOffline:
	default:
		return fmt.Errorf("channel.reverse state %q is unsupported", response.State)
	}
	if response.BridgeHost != "" {
		if err := validateL4RuleEndpoint("channel.reverse bridge host", response.BridgeHost, false); err != nil {
			return err
		}
		if response.BridgePort <= 0 || response.BridgePort > 65535 {
			return errors.New("channel.reverse bridge port is invalid")
		}
	} else if response.BridgePort != 0 {
		return errors.New("channel.reverse bridge port requires a bridge host")
	}
	if len(response.LastError) > 512 || strings.ContainsAny(response.LastError, "\r\n\x00") {
		return errors.New("channel.reverse last error is not safe host text")
	}
	return nil
}
