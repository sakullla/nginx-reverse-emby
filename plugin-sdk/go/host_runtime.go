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
	"os"
	"strings"
)

const (
	EnvPluginHostEndpoint      = "NRE_PLUGIN_HOST_ENDPOINT"
	HeaderPluginHostCredential = "X-NRE-Plugin-Host-Credential"
	PluginHostCallPath         = "/nre.plugin.host.v1/call"
	PluginHostPayloadMaxBytes  = 1 << 20

	HostRuntimePluginCall = "plugin.call"
	HostRuntimeHTTPRule   = "http.rule"
	HostRuntimeEventEmit  = "event.emit"
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
)

// HTTPRuleRequest creates or switches a control-plane HTTP rule. Cutover with
// an empty RuleRef is an explicit error and must not rewrite other rules.
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
		if strings.TrimSpace(request.Domain) == "" || request.Domain != strings.TrimSpace(request.Domain) || len(request.Domain) > 253 || strings.ContainsAny(request.Domain, "\r\n\x00 /") {
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
		if request.Port < 0 || request.Port > 65535 {
			return errors.New("http.rule port is invalid")
		}
		return nil
	default:
		return fmt.Errorf("http.rule action %q is unsupported", request.Action)
	}
}
