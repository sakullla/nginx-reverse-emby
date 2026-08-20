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
	"net/http"
	"net/url"
	"strings"
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
	permission := pluginHostOperationPermission(call.Operation)
	if permission == "" || !pluginCandidateHasGrant(candidate, permission) {
		return pluginHostRuntimeFailure(pluginsdk.ErrorPermissionDenied, "host resource permission was not granted", false)
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
	errPluginHostInvalid = errors.New("plugin host resource request is invalid")
	errPluginHostDenied  = errors.New("plugin host resource request is denied")
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
	if call.Operation == "secret.put" {
		return true
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
	return map[string]any{"material": material}, nil
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
