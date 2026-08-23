package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const pluginHostSecretPurpose = "plugin.host.runtime"

const (
	pluginHostOperationScope = "plugin.host.runtime"
	pluginHostOperationTTL   = 30 * 24 * time.Hour
)

type pluginHostResourceStore interface {
	GetPluginRuntimeState(context.Context, string, string) ([]byte, bool, error)
	PutPluginRuntimeState(context.Context, storage.PluginRuntimeStateRow) error
	AppendAuditEvent(context.Context, storage.AuditEventRow) error
}

func (manager *PluginCapabilityManager) DispatchPluginHostResource(ctx context.Context, candidate pluginhost.Candidate, call pluginsdk.HostRuntimeCall) pluginsdk.HostRuntimeResponse {
	if manager == nil || ctx == nil || candidate.InstanceID == "" || candidate.Identity.PluginID == "" || candidate.Identity.Generation == "" {
		return pluginHostRuntimeFailure(pluginsdk.ErrorUnavailable, "host resource owner is unavailable", true)
	}
	if call.Operation == "operation.inspect" {
		if !pluginCandidateHasGrant(candidate, "storage.read") {
			return pluginHostRuntimeFailure(pluginsdk.ErrorPermissionDenied, "host resource permission was not granted", false)
		}
		return manager.pluginHostOperationInspect(ctx, candidate, call.Payload)
	}
	if call.Operation == pluginsdk.HostRuntimePluginCall {
		if call.OperationID != "" {
			return manager.dispatchDurablePluginHostResource(ctx, candidate, call)
		}
		return manager.dispatchPluginHostResource(ctx, candidate, call)
	}
	permission := pluginHostOperationPermission(call.Operation)
	if permission == "" || !pluginCandidateHasGrant(candidate, permission) {
		return pluginHostRuntimeFailure(pluginsdk.ErrorPermissionDenied, "host resource permission was not granted", false)
	}
	if pluginHostChannelReverseIsLookup(call) {
		return manager.dispatchPluginHostResource(ctx, candidate, call)
	}
	if pluginHostCallRequiresOperation(call) && call.OperationID == "" {
		return pluginHostRuntimeFailure(pluginsdk.ErrorInvalidArgument, "host resource operation id is required", false)
	}
	if call.OperationID != "" {
		return manager.dispatchDurablePluginHostResource(ctx, candidate, call)
	}
	return manager.dispatchPluginHostResource(ctx, candidate, call)
}

func (manager *PluginCapabilityManager) dispatchPluginHostResource(ctx context.Context, candidate pluginhost.Candidate, call pluginsdk.HostRuntimeCall) pluginsdk.HostRuntimeResponse {
	var payload any
	var err error
	switch call.Operation {
	case "secret.describe":
		payload, err = manager.pluginHostSecretDescribe(ctx, candidate, call.Payload)
	case "secret.put":
		payload, err = manager.pluginHostSecretPut(ctx, candidate, call.Payload)
	case "secret.reveal":
		payload, err = manager.pluginHostSecretReveal(ctx, candidate, call.Payload)
	case "state.get":
		payload, err = manager.pluginHostStateGet(ctx, candidate, call.Payload)
	case "state.put":
		payload, err = manager.pluginHostStatePut(ctx, candidate, call.Payload)
	case "http.secret-request":
		payload, err = manager.pluginHostSecretRequest(ctx, candidate, call.Payload)
	case "event.emit":
		payload, err = manager.pluginHostEvent(ctx, candidate, call.Payload)
	case pluginsdk.HostRuntimePluginCall:
		payload, err = manager.pluginHostPluginCall(ctx, candidate, call.Payload)
	case pluginsdk.HostRuntimeHTTPRule:
		payload, err = manager.pluginHostHTTPRule(ctx, candidate, call.Payload)
	case pluginsdk.HostRuntimeL4Rule:
		payload, err = manager.pluginHostL4Rule(ctx, candidate, call.Payload)
	case pluginsdk.HostRuntimeChannelReverse:
		payload, err = manager.pluginHostChannelReverse(ctx, candidate, call.Payload)
	default:
		return pluginHostRuntimeFailure(pluginsdk.ErrorInvalidArgument, "host resource operation is unsupported", false)
	}
	if err != nil {
		code, retryable := pluginsdk.ErrorUnavailable, true
		if errors.Is(err, errPluginHostInvalid) {
			code, retryable = pluginsdk.ErrorInvalidArgument, false
		} else if errors.Is(err, errPluginHostDenied) {
			code, retryable = pluginsdk.ErrorPermissionDenied, false
		}
		return pluginHostRuntimeFailure(code, "host resource operation failed", retryable)
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > pluginsdk.PluginHostPayloadMaxBytes {
		return pluginHostRuntimeFailure(pluginsdk.ErrorInternal, "host resource response is invalid", false)
	}
	return pluginsdk.HostRuntimeResponse{Payload: encoded}
}

var (
	errPluginHostInvalid     = errors.New("plugin host resource request is invalid")
	errPluginHostDenied      = errors.New("plugin host resource request is denied")
	errPluginHostUnavailable = errors.New("plugin host resource request is unavailable")
)

func pluginHostOperationPermission(operation string) string {
	switch operation {
	case "secret.describe", "secret.put", "secret.reveal":
		return "secret.use"
	case "state.get":
		return "storage.read"
	case "state.put":
		return "storage.write"
	case "http.secret-request":
		return pluginsdk.PermissionHTTPOutbound
	case "event.emit":
		return "event.emit"
	case "operation.inspect":
		return "storage.read"
	case pluginsdk.HostRuntimeHTTPRule:
		return pluginsdk.PermissionHTTPRule
	case pluginsdk.HostRuntimeL4Rule:
		return pluginsdk.PermissionL4Rule
	case pluginsdk.HostRuntimeChannelReverse:
		return pluginsdk.PermissionChannelReverse
	default:
		return ""
	}
}

func pluginCandidateHasGrant(candidate pluginhost.Candidate, permission string) bool {
	for _, granted := range candidate.Grants {
		if granted == permission {
			return true
		}
	}
	return false
}

func pluginHostCallRequiresOperation(call pluginsdk.HostRuntimeCall) bool {
	if call.Operation == "secret.put" || call.Operation == pluginsdk.HostRuntimeHTTPRule || call.Operation == pluginsdk.HostRuntimeL4Rule {
		return true
	}
	if call.Operation == pluginsdk.HostRuntimeChannelReverse {
		return !pluginHostChannelReverseIsLookup(call)
	}
	if call.Operation != "http.secret-request" {
		return false
	}
	var input pluginHostHTTPRequest
	if decodePluginHostPayload(call.Payload, &input) != nil {
		return false
	}
	return strings.ToUpper(strings.TrimSpace(input.Method)) != http.MethodGet
}

func pluginHostChannelReverseIsLookup(call pluginsdk.HostRuntimeCall) bool {
	if call.Operation != pluginsdk.HostRuntimeChannelReverse {
		return false
	}
	var input pluginsdk.ChannelReverseRequest
	if decodePluginHostPayload(call.Payload, &input) != nil {
		return false
	}
	return input.Action == pluginsdk.ChannelReverseActionStatus
}

type pluginHostDurableOutcome struct {
	Status   string                        `json:"status"`
	Response pluginsdk.HostRuntimeResponse `json:"response"`
}

type pluginHostOperationInspectPayload struct {
	OperationID string `json:"operation_id"`
}

func (manager *PluginCapabilityManager) dispatchDurablePluginHostResource(ctx context.Context, candidate pluginhost.Candidate, call pluginsdk.HostRuntimeCall) pluginsdk.HostRuntimeResponse {
	key := pluginHostOperationKey(candidate, call.OperationID)
	unlock := manager.lockOperation(key)
	defer unlock()
	fingerprint := pluginHostCallFingerprint(candidate, call)
	now := time.Now().UTC()
	claimToken := capabilityAuditID()
	record, claimed, err := manager.store.ClaimPluginCapabilityOperation(ctx, pluginHostOperationScope, key, fingerprint, call.OperationID, claimToken, now, now.Add(pluginHostOperationTTL))
	if err != nil {
		if errors.Is(err, storage.ErrPluginCapabilityOperationConflict) {
			return pluginHostRuntimeFailure(pluginsdk.ErrorInvalidArgument, "host resource operation id was reused", false)
		}
		return pluginHostRuntimeFailure(pluginsdk.ErrorUnavailable, "host resource operation could not be claimed", true)
	}
	if !claimed {
		return pluginHostStoredOutcome(record)
	}
	if storage.PluginCapabilityOperationRecovered(record) {
		return pluginHostRuntimeFailure(pluginsdk.ErrorUnavailable, "host resource operation outcome is unknown", false)
	}
	response := manager.dispatchPluginHostResource(ctx, candidate, call)
	if response.Error != nil && response.Error.Retryable {
		renewCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = manager.store.RenewPluginCapabilityOperation(renewCtx, pluginHostOperationScope, key, call.OperationID, claimToken, time.Now().UTC())
		return response
	}
	status := "committed"
	if response.Error != nil {
		status = "failed"
	}
	encoded, err := json.Marshal(pluginHostDurableOutcome{Status: status, Response: response})
	if err != nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorInternal, "host resource operation outcome is invalid", false)
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := manager.store.CompletePluginCapabilityOperation(commitCtx, pluginHostOperationScope, key, call.OperationID, claimToken, string(encoded)); err != nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorUnavailable, "host resource operation outcome could not be stored", true)
	}
	return response
}

func (manager *PluginCapabilityManager) pluginHostOperationInspect(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) pluginsdk.HostRuntimeResponse {
	var input pluginHostOperationInspectPayload
	if decodePluginHostPayload(raw, &input) != nil || pluginsdk.ValidatePolicyIdentity(input.OperationID) != nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorInvalidArgument, "host resource operation identity is invalid", false)
	}
	record, found, err := manager.store.GetIdempotencyRecord(ctx, pluginHostOperationScope, pluginHostOperationKey(candidate, input.OperationID))
	if err != nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorUnavailable, "host resource operation could not be inspected", true)
	}
	if !found || !record.ExpiresAt.After(time.Now().UTC()) {
		return pluginHostOperationInspection("absent", pluginsdk.HostRuntimeResponse{})
	}
	var outcome pluginHostDurableOutcome
	if json.Unmarshal([]byte(record.ResponseJSON), &outcome) != nil || (outcome.Status != "committed" && outcome.Status != "failed") {
		return pluginHostOperationInspection("unknown", pluginsdk.HostRuntimeResponse{})
	}
	return pluginHostOperationInspection(outcome.Status, outcome.Response)
}

func pluginHostOperationInspection(state string, response pluginsdk.HostRuntimeResponse) pluginsdk.HostRuntimeResponse {
	payload, err := json.Marshal(struct {
		State    string                        `json:"state"`
		Response pluginsdk.HostRuntimeResponse `json:"response,omitempty"`
	}{State: state, Response: response})
	if err != nil {
		return pluginHostRuntimeFailure(pluginsdk.ErrorInternal, "host resource operation inspection is invalid", false)
	}
	return pluginsdk.HostRuntimeResponse{Payload: payload}
}

func pluginHostStoredOutcome(record storage.IdempotencyRecordRow) pluginsdk.HostRuntimeResponse {
	var outcome pluginHostDurableOutcome
	if json.Unmarshal([]byte(record.ResponseJSON), &outcome) != nil || (outcome.Status != "committed" && outcome.Status != "failed") {
		return pluginHostRuntimeFailure(pluginsdk.ErrorUnavailable, "host resource operation is pending", true)
	}
	return outcome.Response
}

func pluginHostOperationKey(candidate pluginhost.Candidate, operationID string) string {
	digest := sha256.Sum256([]byte(candidate.Identity.PluginID + "\x00" + candidate.InstanceID + "\x00" + operationID))
	return hex.EncodeToString(digest[:])
}

func pluginHostCallFingerprint(candidate pluginhost.Candidate, call pluginsdk.HostRuntimeCall) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(candidate.Identity.PluginID))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(candidate.InstanceID))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(call.Operation))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(call.Payload)
	return hex.EncodeToString(digest.Sum(nil))
}

func pluginHostRuntimeFailure(code pluginsdk.ErrorCode, message string, retryable bool) pluginsdk.HostRuntimeResponse {
	return pluginsdk.HostRuntimeResponse{Error: &pluginsdk.RuntimeError{Code: code, Message: message, Retryable: retryable}}
}

type pluginHostSecretPayload struct {
	Ref             string `json:"ref"`
	ExpectedVersion string `json:"expected_version,omitempty"`
	Material        string `json:"material,omitempty"`
}

type pluginHostSecretResult struct {
	Found   bool   `json:"found"`
	Ref     string `json:"ref,omitempty"`
	Version string `json:"version,omitempty"`
}

func (manager *PluginCapabilityManager) pluginHostSecretDescribe(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (pluginHostSecretResult, error) {
	var input pluginHostSecretPayload
	if decodePluginHostPayload(raw, &input) != nil || !validPluginHostRef(input.Ref) || manager.secretVault == nil {
		return pluginHostSecretResult{}, errPluginHostInvalid
	}
	metadata, found, err := manager.pluginHostSecretMetadata(ctx, candidate, input.Ref)
	if err != nil || !found {
		return pluginHostSecretResult{Found: false}, err
	}
	return pluginHostSecretResult{Found: true, Ref: input.Ref, Version: fmt.Sprintf("v%d", metadata.ActiveVersion)}, nil
}

func (manager *PluginCapabilityManager) pluginHostSecretPut(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (pluginHostSecretResult, error) {
	var input pluginHostSecretPayload
	if decodePluginHostPayload(raw, &input) != nil || !validPluginHostRef(input.Ref) || input.Material == "" || len(input.Material) > 8192 || manager.secretVault == nil {
		return pluginHostSecretResult{}, errPluginHostInvalid
	}
	unlock := manager.lockOperation("plugin-host-secret:" + pluginHostSecretName(candidate, input.Ref))
	defer unlock()
	metadata, found, err := manager.pluginHostSecretMetadata(ctx, candidate, input.Ref)
	if err != nil {
		return pluginHostSecretResult{}, err
	}
	op := secrets.OperationContext{ActorID: "plugin/" + candidate.Identity.PluginID, CorrelationID: candidate.Identity.Generation, ResourceGroupID: candidate.ResourceGroupID}
	if !found {
		if input.ExpectedVersion != "" {
			return pluginHostSecretResult{}, errPluginHostDenied
		}
		metadata, err = manager.secretVault.Create(ctx, op, pluginHostSecretName(candidate, input.Ref), pluginHostSecretPurpose, input.Material)
	} else {
		if input.ExpectedVersion != fmt.Sprintf("v%d", metadata.ActiveVersion) {
			return pluginHostSecretResult{}, errPluginHostDenied
		}
		metadata, err = manager.secretVault.Rotate(ctx, op, metadata.ID, input.Material)
	}
	if err != nil {
		return pluginHostSecretResult{}, err
	}
	return pluginHostSecretResult{Found: true, Ref: input.Ref, Version: fmt.Sprintf("v%d", metadata.ActiveVersion)}, nil
}

func (manager *PluginCapabilityManager) pluginHostSecretReveal(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (map[string]any, error) {
	var input pluginHostSecretPayload
	if decodePluginHostPayload(raw, &input) != nil || !validPluginHostRef(input.Ref) || manager.secretVault == nil {
		return nil, errPluginHostInvalid
	}
	metadata, found, err := manager.pluginHostSecretMetadata(ctx, candidate, input.Ref)
	if err != nil || !found {
		return nil, errors.Join(err, errPluginHostDenied)
	}
	material, err := manager.secretVault.Resolve(ctx, secrets.OperationContext{ActorID: "plugin/" + candidate.Identity.PluginID, CorrelationID: candidate.Identity.Generation, ResourceGroupID: candidate.ResourceGroupID}, metadata.ID)
	if err != nil {
		return nil, err
	}
	defer clear(material)
	// The response is marshaled by dispatchPluginHostResource after this helper
	// returns. Keep the Vault-owned plaintext eligible for immediate clearing,
	// but hand the response encoder an independent copy so the deferred clear
	// does not turn a valid secret into an equal-length all-zero payload.
	return map[string]any{"material": append([]byte(nil), material...)}, nil
}

func (manager *PluginCapabilityManager) pluginHostSecretMetadata(ctx context.Context, candidate pluginhost.Candidate, ref string) (secrets.Metadata, bool, error) {
	items, err := manager.secretVault.List(ctx, []string{candidate.ResourceGroupID})
	if err != nil {
		return secrets.Metadata{}, false, err
	}
	name := pluginHostSecretName(candidate, ref)
	for _, item := range items {
		if item.Name == name && item.Purpose == pluginHostSecretPurpose && item.ResourceGroupID == candidate.ResourceGroupID {
			return item, true, nil
		}
	}
	return secrets.Metadata{}, false, nil
}

func pluginHostSecretName(candidate pluginhost.Candidate, ref string) string {
	digest := sha256.Sum256([]byte(candidate.Identity.PluginID + "\x00" + candidate.InstanceID + "\x00" + ref))
	return "plugin-runtime-" + hex.EncodeToString(digest[:16])
}

type pluginHostStatePayload struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value,omitempty"`
}

func (manager *PluginCapabilityManager) pluginHostStateGet(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (map[string]any, error) {
	var input pluginHostStatePayload
	store, ok := manager.store.(pluginHostResourceStore)
	if !ok || decodePluginHostPayload(raw, &input) != nil || !validPluginHostRef(input.Key) {
		return nil, errPluginHostInvalid
	}
	value, found, err := store.GetPluginRuntimeState(ctx, candidate.InstanceID, input.Key)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]any{"found": false}, nil
	}
	return map[string]any{"found": true, "value": json.RawMessage(value)}, nil
}

func (manager *PluginCapabilityManager) pluginHostStatePut(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (map[string]any, error) {
	var input pluginHostStatePayload
	store, ok := manager.store.(pluginHostResourceStore)
	if !ok || decodePluginHostPayload(raw, &input) != nil || !validPluginHostRef(input.Key) || len(input.Value) == 0 || len(input.Value) > pluginsdk.PluginHostPayloadMaxBytes || !json.Valid(input.Value) {
		return nil, errPluginHostInvalid
	}
	err := store.PutPluginRuntimeState(ctx, storage.PluginRuntimeStateRow{InstanceID: candidate.InstanceID, Key: input.Key, PluginID: candidate.Identity.PluginID, ResourceGroupID: candidate.ResourceGroupID, Value: input.Value})
	return map[string]any{"stored": err == nil}, err
}

type pluginHostHTTPRequest struct {
	SecretRef string            `json:"secret_ref"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      []byte            `json:"body,omitempty"`
}

func (manager *PluginCapabilityManager) pluginHostSecretRequest(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (map[string]any, error) {
	var input pluginHostHTTPRequest
	if decodePluginHostPayload(raw, &input) != nil || !validPluginHostRef(input.SecretRef) || len(input.Body) > pluginsdk.PluginHostPayloadMaxBytes || manager.secretVault == nil || !pluginCandidateHasGrant(candidate, "secret.use") {
		return nil, errPluginHostInvalid
	}
	parsed, err := url.Parse(input.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Port() != "" && parsed.Port() != "443") || !pluginCandidateAllowsOutboundHost(candidate, parsed.Hostname()) {
		return nil, errPluginHostInvalid
	}
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, errPluginHostInvalid
	}
	metadata, found, err := manager.pluginHostSecretMetadata(ctx, candidate, input.SecretRef)
	if err != nil || !found {
		return nil, errors.Join(err, errPluginHostDenied)
	}
	material, err := manager.secretVault.Resolve(ctx, secrets.OperationContext{ActorID: "plugin/" + candidate.Identity.PluginID, CorrelationID: candidate.Identity.Generation, ResourceGroupID: candidate.ResourceGroupID}, metadata.ID)
	if err != nil {
		return nil, err
	}
	defer clear(material)
	request, err := http.NewRequestWithContext(ctx, method, parsed.String(), bytes.NewReader(input.Body))
	if err != nil {
		return nil, errPluginHostInvalid
	}
	for name, value := range input.Headers {
		switch http.CanonicalHeaderKey(name) {
		case "Accept", "Content-Type":
			if strings.ContainsAny(value, "\r\n\x00") || len(value) > 256 {
				return nil, errPluginHostInvalid
			}
			request.Header.Set(name, value)
		default:
			return nil, errPluginHostInvalid
		}
	}
	request.Header.Set("Authorization", "Bearer "+string(material))
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, pluginsdk.PluginHostPayloadMaxBytes+1))
	if err != nil || len(body) > pluginsdk.PluginHostPayloadMaxBytes {
		return nil, errors.Join(err, errPluginHostInvalid)
	}
	return map[string]any{
		"status":         response.StatusCode,
		"body":           body,
		"content_type":   response.Header.Get("Content-Type"),
		"request_method": method,
		"headers": map[string]string{
			"Retry-After": response.Header.Get("Retry-After"),
		},
	}, nil
}

func pluginCandidateAllowsOutboundHost(candidate pluginhost.Candidate, hostname string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if hostname == "" || !pluginCandidateHasGrant(candidate, pluginsdk.PermissionHTTPOutbound) {
		return false
	}
	for _, selector := range candidate.GrantSelectors[pluginsdk.PermissionHTTPOutbound] {
		allowed := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(selector), "."))
		if allowed == hostname {
			return true
		}
	}
	return false
}

type pluginHostEventPayload struct {
	Action     string          `json:"action"`
	Result     string          `json:"result"`
	TargetKind string          `json:"target_kind,omitempty"`
	TargetID   string          `json:"target_id,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

func (manager *PluginCapabilityManager) pluginHostEvent(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (map[string]any, error) {
	var input pluginHostEventPayload
	store, ok := manager.store.(pluginHostResourceStore)
	if !ok || decodePluginHostPayload(raw, &input) != nil || !validPluginHostRef(input.Action) || (input.Result != "success" && input.Result != "failure" && input.Result != "pending") || len(input.Metadata) > 4096 || (len(input.Metadata) > 0 && !json.Valid(input.Metadata)) {
		return nil, errPluginHostInvalid
	}
	metadata := input.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	err := store.AppendAuditEvent(ctx, storage.AuditEventRow{ID: capabilityAuditID(), ActorID: "plugin/" + candidate.Identity.PluginID, Action: input.Action, TargetKind: input.TargetKind, TargetID: input.TargetID, ResourceGroupID: candidate.ResourceGroupID, CorrelationID: candidate.Identity.Generation, Result: input.Result, MetadataJSON: string(metadata), CreatedAt: time.Now().UTC()})
	return map[string]any{"emitted": err == nil}, err
}

type pluginHostTaskDispatcher interface {
	HasSession(string) bool
	CreateAndDispatchContext(context.Context, TaskCreateRequest) (TaskRecord, error)
	WaitForTask(context.Context, string) (TaskRecord, error)
}

type pluginHostHTTPRuleService interface {
	Create(context.Context, string, HTTPRuleInput) (HTTPRule, error)
	Get(context.Context, string, int) (HTTPRule, error)
	Update(context.Context, string, int, HTTPRuleInput) (HTTPRule, error)
	List(context.Context, string) ([]HTTPRule, error)
}

// pluginHostChannelSessions dispatches agent-scoped tasks. The reverse
// channel data plane is driven by agent tasks rather than a panel service.
type pluginHostChannelSessions interface {
	DispatchAgentTask(context.Context, string, string, map[string]any) (map[string]any, error)
}

type pluginHostL4RuleService interface {
	Create(context.Context, string, L4RuleInput) (L4Rule, error)
	Get(context.Context, string, int) (L4Rule, error)
	Update(context.Context, string, int, L4RuleInput) (L4Rule, error)
	Delete(context.Context, string, int) (L4Rule, error)
	List(context.Context, string) ([]L4Rule, error)
}

type pluginHostRuntimeState struct {
	mu             sync.Mutex
	tasks          pluginHostTaskDispatcher
	rules          pluginHostHTTPRuleService
	l4rules        pluginHostL4RuleService
	channels       pluginHostChannelSessions
	channelProject pluginHostChannelListenerProjector
	lookup         func(context.Context, string, string) (pluginCallTarget, error)
}

var pluginHostRuntimeStates sync.Map

func pluginHostState(manager *PluginCapabilityManager) *pluginHostRuntimeState {
	if manager == nil {
		return &pluginHostRuntimeState{}
	}
	actual, _ := pluginHostRuntimeStates.LoadOrStore(manager, &pluginHostRuntimeState{})
	return actual.(*pluginHostRuntimeState)
}

func (manager *PluginCapabilityManager) SetTaskService(tasks pluginHostTaskDispatcher) {
	state := pluginHostState(manager)
	state.mu.Lock()
	state.tasks = tasks
	state.mu.Unlock()
}

func (manager *PluginCapabilityManager) SetRuleService(rules pluginHostHTTPRuleService) {
	state := pluginHostState(manager)
	state.mu.Lock()
	state.rules = rules
	state.mu.Unlock()
}

func (manager *PluginCapabilityManager) SetL4RuleService(rules pluginHostL4RuleService) {
	state := pluginHostState(manager)
	state.mu.Lock()
	state.l4rules = rules
	state.mu.Unlock()
}

func (manager *PluginCapabilityManager) SetChannelTaskDispatcher(sessions pluginHostChannelSessions) {
	state := pluginHostState(manager)
	state.mu.Lock()
	state.channels = sessions
	state.mu.Unlock()
}

func (manager *PluginCapabilityManager) SetChannelListenerProjector(projector pluginHostChannelListenerProjector) {
	state := pluginHostState(manager)
	state.mu.Lock()
	state.channelProject = projector
	state.mu.Unlock()
}

func (manager *PluginCapabilityManager) HostRuntimeServicesBound() (tasksBound, rulesBound bool) {
	if manager == nil {
		return false, false
	}
	state := pluginHostState(manager)
	return state.taskDispatcher() != nil, state.ruleService() != nil
}

func (manager *PluginCapabilityManager) setPluginCallLookup(lookup func(context.Context, string, string) (pluginCallTarget, error)) {
	state := pluginHostState(manager)
	state.mu.Lock()
	state.lookup = lookup
	state.mu.Unlock()
}

func (state *pluginHostRuntimeState) taskDispatcher() pluginHostTaskDispatcher {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.tasks
}

func (state *pluginHostRuntimeState) ruleService() pluginHostHTTPRuleService {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.rules
}

func (state *pluginHostRuntimeState) l4RuleService() pluginHostL4RuleService {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.l4rules
}

func (state *pluginHostRuntimeState) channelTaskDispatcher() pluginHostChannelSessions {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.channels
}

func (state *pluginHostRuntimeState) channelListenerProjector() pluginHostChannelListenerProjector {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.channelProject
}

func (state *pluginHostRuntimeState) pluginCallLookup() func(context.Context, string, string) (pluginCallTarget, error) {
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.lookup
}

type pluginCallLookupStore interface {
	GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error)
	ListPluginInstances(context.Context, string) ([]storage.PluginInstanceRow, error)
	GetPluginAgentRuntimeStatusFence(context.Context, string, string, string) (storage.PluginAgentRuntimeStatusRow, bool, error)
}

type pluginCallTarget struct {
	InstanceID string
	Generation string
	PluginID   string
}

func (manager *PluginCapabilityManager) pluginHostPluginCall(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (json.RawMessage, error) {
	var input pluginsdk.PluginCallRequest
	if decodePluginHostPayload(raw, &input) != nil || input.Validate() != nil {
		return nil, errPluginHostInvalid
	}
	if err := manager.authorizePluginCallCaller(ctx, candidate); err != nil {
		return nil, err
	}
	target, err := manager.resolvePluginCallTarget(ctx, candidate.Identity.PluginID, input.AgentID)
	if err != nil {
		return nil, err
	}
	if target.PluginID != candidate.Identity.PluginID {
		return nil, errPluginHostDenied
	}
	tasks := pluginHostState(manager).taskDispatcher()
	if tasks == nil || !tasks.HasSession(input.AgentID) {
		return nil, errPluginHostUnavailable
	}
	payload := map[string]any{
		"plugin_id":   candidate.Identity.PluginID,
		"instance_id": target.InstanceID,
		"generation":  target.Generation,
		"name":        input.Name,
	}
	if len(input.Payload) > 0 {
		payload["payload"] = input.Payload
	}
	record, err := tasks.CreateAndDispatchContext(ctx, TaskCreateRequest{
		AgentID: input.AgentID,
		Type:    TaskTypePluginCall,
		Payload: payload,
	})
	if err != nil {
		if errors.Is(err, errTaskSessionUnavailable) {
			return nil, errPluginHostUnavailable
		}
		return nil, err
	}
	record, err = tasks.WaitForTask(ctx, record.ID)
	if err != nil {
		reason := "task_failed"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			reason = "deadline_exceeded"
		case errors.Is(err, context.Canceled):
			reason = "canceled"
		case errors.Is(err, ErrTaskNotFound):
			reason = "task_not_found"
		}
		log.Printf("[plugin.call] task failed plugin=%q agent=%q name=%q task=%q reason=%q", candidate.Identity.PluginID, input.AgentID, input.Name, record.ID, reason)
		return nil, errors.Join(err, errPluginHostUnavailable)
	}
	if record.Result == nil {
		return nil, errPluginHostUnavailable
	}
	encoded, err := json.Marshal(record.Result["payload"])
	if err != nil || (len(encoded) > 0 && !json.Valid(encoded)) || len(encoded) > pluginsdk.PluginHostPayloadMaxBytes {
		return nil, errPluginHostUnavailable
	}
	if encoded == nil {
		encoded = []byte("null")
	}
	return json.RawMessage(encoded), nil
}

func (manager *PluginCapabilityManager) authorizePluginCallCaller(ctx context.Context, candidate pluginhost.Candidate) error {
	store, ok := manager.store.(pluginCallLookupStore)
	if !ok {
		return nil
	}
	installed, found, err := store.GetInstalledPlugin(ctx, candidate.Identity.PluginID)
	if err != nil {
		return err
	}
	if found && strings.TrimSpace(installed.HostScope) != "" && strings.TrimSpace(installed.HostScope) != "control-plane" {
		return errPluginHostDenied
	}
	return nil
}

func (manager *PluginCapabilityManager) resolvePluginCallTarget(ctx context.Context, pluginID, agentID string) (pluginCallTarget, error) {
	if lookup := pluginHostState(manager).pluginCallLookup(); lookup != nil {
		return lookup(ctx, pluginID, agentID)
	}
	store, ok := manager.store.(pluginCallLookupStore)
	if !ok {
		return pluginCallTarget{}, errPluginHostUnavailable
	}
	installed, found, err := store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return pluginCallTarget{}, err
	}
	if !found {
		return pluginCallTarget{}, errPluginHostUnavailable
	}
	instances, err := store.ListPluginInstances(ctx, pluginID)
	if err != nil {
		return pluginCallTarget{}, err
	}
	for _, instance := range instances {
		if !instance.DesiredEnabled {
			continue
		}
		if !pluginHostInstanceTargetsAgent(instance.TargetJSON, agentID) {
			continue
		}
		operationID := strings.TrimSpace(instance.PendingOperationID)
		if operationID == "" {
			operationID = strings.TrimSpace(installed.LastOperationID)
		}
		if operationID == "" {
			continue
		}
		status, ok, err := store.GetPluginAgentRuntimeStatusFence(ctx, operationID, agentID, instance.ID)
		if err != nil {
			return pluginCallTarget{}, err
		}
		if !ok || status.PluginID != pluginID || (status.State != "active" && status.State != "prepared") {
			continue
		}
		generation := strings.TrimSpace(status.GenerationID)
		if generation == "" {
			generation = candidatePluginCallGeneration(instance)
		}
		return pluginCallTarget{InstanceID: instance.ID, Generation: generation, PluginID: pluginID}, nil
	}
	return pluginCallTarget{}, errPluginHostUnavailable
}

func candidatePluginCallGeneration(instance storage.PluginInstanceRow) string {
	if strings.TrimSpace(instance.PendingOperationID) != "" {
		return instance.PendingOperationID
	}
	return instance.ID
}

func (manager *PluginCapabilityManager) pluginHostHTTPRule(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (map[string]any, error) {
	var input pluginsdk.HTTPRuleRequest
	if decodePluginHostPayload(raw, &input) != nil || input.Validate() != nil {
		return nil, errPluginHostInvalid
	}
	rules := pluginHostState(manager).ruleService()
	if rules == nil {
		return nil, errPluginHostUnavailable
	}
	ctx = WithSystemMutationPrincipal(ctx, "plugin/"+candidate.Identity.PluginID)
	switch input.Action {
	case pluginsdk.HTTPRuleActionCreate:
		return manager.pluginHostHTTPRuleCreate(ctx, rules, input)
	case pluginsdk.HTTPRuleActionCutover:
		return manager.pluginHostHTTPRuleCutover(ctx, rules, input)
	default:
		return nil, errPluginHostInvalid
	}
}

func (manager *PluginCapabilityManager) pluginHostHTTPRuleCreate(ctx context.Context, rules pluginHostHTTPRuleService, input pluginsdk.HTTPRuleRequest) (map[string]any, error) {
	frontend, err := pluginHostRuleFrontendURL(input.Domain)
	if err != nil {
		return nil, errPluginHostInvalid
	}
	backend, err := pluginHostRuleBackendURL(input.Port)
	if err != nil {
		return nil, errPluginHostInvalid
	}
	enabled := true
	rule, err := rules.Create(ctx, input.AgentID, HTTPRuleInput{
		FrontendURL: &frontend,
		Backends:    &[]HTTPRuleBackend{{URL: backend}},
		Enabled:     &enabled,
	})
	if err != nil {
		if errors.Is(err, ErrAgentNotFound) || errors.Is(err, ErrInvalidArgument) {
			return nil, errPluginHostInvalid
		}
		return nil, err
	}
	return pluginHostHTTPRuleResult(rule), nil
}

func (manager *PluginCapabilityManager) pluginHostHTTPRuleCutover(ctx context.Context, rules pluginHostHTTPRuleService, input pluginsdk.HTTPRuleRequest) (map[string]any, error) {
	if strings.TrimSpace(input.RuleRef) == "" {
		return nil, errPluginHostInvalid
	}
	rule, err := manager.resolvePluginHostHTTPRule(ctx, rules, input)
	if err != nil {
		return nil, err
	}
	update := HTTPRuleInput{}
	if input.Port > 0 {
		backend, err := pluginHostRuleBackendURL(input.Port)
		if err != nil {
			return nil, errPluginHostInvalid
		}
		update.Backends = &[]HTTPRuleBackend{{URL: backend}}
	}
	if strings.TrimSpace(input.Domain) != "" {
		frontend, err := pluginHostRuleFrontendURL(input.Domain)
		if err != nil {
			return nil, errPluginHostInvalid
		}
		update.FrontendURL = &frontend
	}
	if update.Backends == nil && update.FrontendURL == nil {
		return pluginHostHTTPRuleResult(rule), nil
	}
	updated, err := rules.Update(ctx, rule.AgentID, rule.ID, update)
	if err != nil {
		if errors.Is(err, ErrRuleNotFound) || errors.Is(err, ErrInvalidArgument) {
			return nil, errPluginHostInvalid
		}
		return nil, err
	}
	return pluginHostHTTPRuleResult(updated), nil
}

func (manager *PluginCapabilityManager) resolvePluginHostHTTPRule(ctx context.Context, rules pluginHostHTTPRuleService, input pluginsdk.HTTPRuleRequest) (HTTPRule, error) {
	agentID := strings.TrimSpace(input.AgentID)
	if id, err := parsePluginHostRuleRef(input.RuleRef); err == nil {
		if agentID == "" {
			return HTTPRule{}, errPluginHostInvalid
		}
		rule, err := rules.Get(ctx, agentID, id)
		if err != nil {
			if errors.Is(err, ErrRuleNotFound) {
				return HTTPRule{}, errPluginHostInvalid
			}
			return HTTPRule{}, err
		}
		return rule, nil
	}
	if agentID == "" {
		return HTTPRule{}, errPluginHostInvalid
	}
	listed, err := rules.List(ctx, agentID)
	if err != nil {
		return HTTPRule{}, err
	}
	want := strings.TrimSpace(input.RuleRef)
	for _, rule := range listed {
		if rule.FrontendURL == want {
			return rule, nil
		}
		if _, host, ok := parseRuleFrontendTarget(rule.FrontendURL); ok && host == want {
			return rule, nil
		}
	}
	return HTTPRule{}, errPluginHostInvalid
}

func pluginHostInstanceTargetsAgent(raw, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return false
	}
	var targets []string
	if json.Unmarshal([]byte(raw), &targets) != nil {
		return false
	}
	for _, target := range targets {
		if strings.TrimSpace(target) == agentID {
			return true
		}
	}
	return false
}

func pluginHostHTTPRuleResult(rule HTTPRule) map[string]any {
	backend := ""
	if len(rule.Backends) > 0 {
		backend = strings.TrimSpace(rule.Backends[0].URL)
	}
	return map[string]any{
		"rule_ref":     strconv.Itoa(rule.ID),
		"agent_id":     rule.AgentID,
		"frontend_url": rule.FrontendURL,
		"backend_url":  backend,
	}
}

const (
	pluginHostL4RuleTagPrefix         = "plugin:"
	pluginHostL4RuleInstanceTagPrefix = "plugin-instance:"
)

func (manager *PluginCapabilityManager) pluginHostL4Rule(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (map[string]any, error) {
	var input pluginsdk.L4RuleRequest
	if decodePluginHostPayload(raw, &input) != nil || input.Validate() != nil {
		return nil, errPluginHostInvalid
	}
	rules := pluginHostState(manager).l4RuleService()
	if rules == nil {
		return nil, errPluginHostUnavailable
	}
	ctx = WithSystemMutationPrincipal(ctx, "plugin/"+candidate.Identity.PluginID)
	switch input.Action {
	case pluginsdk.L4RuleActionCreate:
		return manager.pluginHostL4RuleCreate(ctx, candidate, rules, input)
	case pluginsdk.L4RuleActionUpdate:
		return manager.pluginHostL4RuleUpdate(ctx, candidate, rules, input)
	case pluginsdk.L4RuleActionEnable, pluginsdk.L4RuleActionDisable:
		return manager.pluginHostL4RuleSetEnabled(ctx, candidate, rules, input, input.Action == pluginsdk.L4RuleActionEnable)
	case pluginsdk.L4RuleActionDelete:
		return manager.pluginHostL4RuleDelete(ctx, candidate, rules, input)
	default:
		return nil, errPluginHostInvalid
	}
}

func (manager *PluginCapabilityManager) pluginHostL4RuleCreate(ctx context.Context, candidate pluginhost.Candidate, rules pluginHostL4RuleService, input pluginsdk.L4RuleRequest) (map[string]any, error) {
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		return nil, errPluginHostInvalid
	}
	ruleInput := pluginHostL4RuleInput(input)
	tags := pluginHostL4RuleTags(input.Tags, candidate)
	ruleInput.Tags = &tags
	rule, err := rules.Create(ctx, agentID, ruleInput)
	if err != nil {
		if errors.Is(err, ErrAgentNotFound) || errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrL4Unsupported) {
			return nil, errPluginHostInvalid
		}
		return nil, err
	}
	return pluginHostL4RuleResult(rule), nil
}

func (manager *PluginCapabilityManager) pluginHostL4RuleUpdate(ctx context.Context, candidate pluginhost.Candidate, rules pluginHostL4RuleService, input pluginsdk.L4RuleRequest) (map[string]any, error) {
	rule, err := manager.resolvePluginHostL4Rule(ctx, candidate, rules, input)
	if err != nil {
		return nil, err
	}
	ruleInput := pluginHostL4RuleInput(input)
	if input.Tags != nil {
		tags := pluginHostL4RuleTags(input.Tags, candidate)
		ruleInput.Tags = &tags
	}
	updated, err := rules.Update(ctx, rule.AgentID, rule.ID, ruleInput)
	if err != nil {
		if errors.Is(err, ErrRuleNotFound) || errors.Is(err, ErrInvalidArgument) {
			return nil, errPluginHostInvalid
		}
		return nil, err
	}
	return pluginHostL4RuleResult(updated), nil
}

func (manager *PluginCapabilityManager) pluginHostL4RuleSetEnabled(ctx context.Context, candidate pluginhost.Candidate, rules pluginHostL4RuleService, input pluginsdk.L4RuleRequest, enabled bool) (map[string]any, error) {
	rule, err := manager.resolvePluginHostL4Rule(ctx, candidate, rules, input)
	if err != nil {
		return nil, err
	}
	updated, err := rules.Update(ctx, rule.AgentID, rule.ID, L4RuleInput{Enabled: &enabled})
	if err != nil {
		if errors.Is(err, ErrRuleNotFound) || errors.Is(err, ErrInvalidArgument) {
			return nil, errPluginHostInvalid
		}
		return nil, err
	}
	return pluginHostL4RuleResult(updated), nil
}

func (manager *PluginCapabilityManager) pluginHostL4RuleDelete(ctx context.Context, candidate pluginhost.Candidate, rules pluginHostL4RuleService, input pluginsdk.L4RuleRequest) (map[string]any, error) {
	rule, err := manager.resolvePluginHostL4Rule(ctx, candidate, rules, input)
	if err != nil {
		return nil, err
	}
	deleted, err := rules.Delete(ctx, rule.AgentID, rule.ID)
	if err != nil {
		if errors.Is(err, ErrRuleNotFound) || errors.Is(err, ErrInvalidArgument) {
			return nil, errPluginHostInvalid
		}
		return nil, err
	}
	return pluginHostL4RuleResult(deleted), nil
}

func (manager *PluginCapabilityManager) resolvePluginHostL4Rule(ctx context.Context, candidate pluginhost.Candidate, rules pluginHostL4RuleService, input pluginsdk.L4RuleRequest) (L4Rule, error) {
	id, err := parsePluginHostRuleRef(input.RuleRef)
	if err != nil {
		return L4Rule{}, errPluginHostInvalid
	}
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		return L4Rule{}, errPluginHostInvalid
	}
	rule, err := rules.Get(ctx, agentID, id)
	if err != nil {
		if errors.Is(err, ErrRuleNotFound) || errors.Is(err, ErrAgentNotFound) || errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrL4Unsupported) {
			return L4Rule{}, errPluginHostInvalid
		}
		return L4Rule{}, err
	}
	if !pluginHostL4RuleOwnedBy(rule, candidate.Identity.PluginID) {
		return L4Rule{}, errPluginHostDenied
	}
	return rule, nil
}

func pluginHostL4RuleInput(input pluginsdk.L4RuleRequest) L4RuleInput {
	ruleInput := L4RuleInput{Enabled: input.Enabled, EgressProfileID: input.EgressProfileID}
	if name := strings.TrimSpace(input.Name); name != "" {
		ruleInput.Name = &name
	}
	if protocol := strings.ToLower(strings.TrimSpace(input.Protocol)); protocol != "" {
		ruleInput.Protocol = &protocol
	}
	if host := strings.TrimSpace(input.ListenHost); host != "" {
		ruleInput.ListenHost = &host
	}
	if input.ListenPort > 0 {
		port := input.ListenPort
		ruleInput.ListenPort = &port
	}
	if input.Backends != nil {
		backends := make([]L4Backend, 0, len(input.Backends))
		for _, backend := range input.Backends {
			backends = append(backends, L4Backend{Host: strings.TrimSpace(backend.Host), Port: backend.Port})
		}
		ruleInput.Backends = &backends
	}
	if input.LoadBalancing != nil {
		ruleInput.LoadBalancing = &L4LoadBalancing{Strategy: input.LoadBalancing.Strategy}
	}
	if input.Tuning != nil {
		ruleInput.Tuning = &L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{
			Decode:       input.Tuning.ProxyProtocol.Decode,
			Send:         input.Tuning.ProxyProtocol.Send,
			TrustedPeers: append([]string(nil), input.Tuning.ProxyProtocol.TrustedPeers...),
		}}
	}
	if input.RelayLayers != nil {
		layers := cloneIntLayers(input.RelayLayers)
		ruleInput.RelayLayers = &layers
	}
	if input.RelayObfs {
		obfs := true
		ruleInput.RelayObfs = &obfs
	}
	if mode := strings.TrimSpace(input.ListenMode); mode != "" {
		ruleInput.ListenMode = &mode
	}
	return ruleInput
}

func pluginHostL4RuleTags(requested []string, candidate pluginhost.Candidate) []string {
	tags := make([]string, 0, len(requested)+3)
	for _, tag := range requested {
		if tag == "plugin" || strings.HasPrefix(tag, pluginHostL4RuleTagPrefix) || strings.HasPrefix(tag, pluginHostL4RuleInstanceTagPrefix) {
			continue
		}
		tags = append(tags, tag)
	}
	tags = append(tags, pluginPublishRuleTags(candidate.Identity.PluginID)...)
	if instanceID := strings.TrimSpace(candidate.InstanceID); instanceID != "" {
		tags = append(tags, pluginHostL4RuleInstanceTagPrefix+instanceID)
	}
	return tags
}

func pluginHostL4RuleOwnedBy(rule L4Rule, pluginID string) bool {
	want := pluginHostL4RuleTagPrefix + strings.TrimSpace(pluginID)
	for _, tag := range rule.Tags {
		if tag == want {
			return true
		}
	}
	return false
}

func pluginHostL4RuleResult(rule L4Rule) map[string]any {
	return map[string]any{
		"rule_ref": strconv.Itoa(rule.ID),
		"agent_id": rule.AgentID,
		"enabled":  rule.Enabled,
	}
}

func decodePluginHostPayload(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errPluginHostInvalid
	}
	return nil
}

func validPluginHostRef(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 190 && !strings.ContainsAny(value, "\r\n\x00")
}
