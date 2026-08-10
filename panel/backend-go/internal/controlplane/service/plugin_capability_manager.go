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
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type PluginCapabilityManagerStore interface {
	PluginCapabilityStore
	GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error)
	GetPluginPackageByIdentity(context.Context, string) (storage.PluginPackageRow, bool, error)
	ListPluginGrants(context.Context, string) ([]storage.PluginGrantRow, error)
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	PluginCapabilityTargetBinding(context.Context, string, string) (storage.PluginCapabilityTargetBinding, bool, error)
	ExecutePluginCapabilityResourceCall(context.Context, storage.PluginCapabilityTargetBinding, pluginsdk.RPCResourceCall) ([]byte, error)
	ClaimPluginCapabilityOperation(context.Context, string, string, string, string, string, time.Time, time.Time) (storage.IdempotencyRecordRow, bool, error)
	RenewPluginCapabilityOperation(context.Context, string, string, string, string, time.Time) error
	CompletePluginCapabilityOperation(context.Context, string, string, string, string, string) error
}

type PluginCapabilityRuntime interface {
	ActiveGeneration(string) (string, bool)
	PlanAction(context.Context, string, string, pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error)
	InvokeAction(context.Context, string, string, pluginsdk.RPCActionRequest) error
	QueryAction(context.Context, string, string, pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error)
}

type PluginCapabilityTrafficSummaryProvider interface {
	Summary(context.Context, string) (TrafficSummary, error)
}

type PluginDynamicActionRequest struct {
	OperationID string
	PluginID    string
	InstanceID  string
	ActionID    string
	Actor       authz.Actor
	Target      pluginsdk.HostTarget
}

type PluginDynamicActionResult struct {
	OperationID string `json:"operation_id"`
	Replayed    bool   `json:"replayed"`
}

type PluginResourceHandleRequest struct {
	PluginID   string
	InstanceID string
	Actor      authz.Actor
	Target     pluginsdk.HostTarget
}

type pluginResourceReference struct {
	Target  pluginsdk.HostTarget
	Version string
}

type PluginCapabilityManager struct {
	store              PluginCapabilityManagerStore
	resourceAuthorizer PluginCapabilityResourceAuthorizer
	runtime            PluginCapabilityRuntime
	loadPackage        func(context.Context, storage.PluginPackageRow) (plugins.ValidatedPackage, error)
	handles            *pluginhost.ResourceHandleBroker
	actions            *pluginhost.DynamicActionRegistry
	operationLocksMu   sync.Mutex
	operationLocks     map[string]*pluginCapabilityOperationLock
	secretVault        *secrets.Vault
	trafficSummary     PluginCapabilityTrafficSummaryProvider
	cloudflare         cloudflareDNSClient
	dockerSocket       string
}

type pluginCapabilityOperationLock struct {
	mu   sync.Mutex
	refs int
}

func NewPluginCapabilityManager(store PluginCapabilityManagerStore, resourceAuthorizer PluginCapabilityResourceAuthorizer, runtime PluginCapabilityRuntime, packages *PluginService) (*PluginCapabilityManager, error) {
	if store == nil || resourceAuthorizer == nil || runtime == nil || packages == nil {
		return nil, errors.New("plugin capability durable store, authorization owner, package validator, and runtime are required")
	}
	manager := &PluginCapabilityManager{store: store, resourceAuthorizer: resourceAuthorizer, runtime: runtime, handles: pluginhost.NewResourceHandleBroker(), actions: pluginhost.NewDynamicActionRegistry(), operationLocks: make(map[string]*pluginCapabilityOperationLock), cloudflare: newHTTPCloudflareClient("", 10*time.Second), dockerSocket: storage.PluginCapabilityDockerSocketPath}
	manager.loadPackage = packages.loadValidatedCapabilityPackage
	return manager, nil
}

func (manager *PluginCapabilityManager) SetCoreResourceVault(vault *secrets.Vault) {
	if manager != nil {
		manager.secretVault = vault
	}
}

func (manager *PluginCapabilityManager) SetTrafficSummaryProvider(provider PluginCapabilityTrafficSummaryProvider) {
	if manager != nil {
		manager.trafficSummary = provider
	}
}

// InvokeDynamicAction claims a durable operation before entering the guest.
// A committed outcome is replayed exactly, independent of later unrelated
// grant changes, while reuse with a different canonical payload is denied.
func (manager *PluginCapabilityManager) InvokeDynamicAction(ctx context.Context, request PluginDynamicActionRequest) (PluginDynamicActionResult, error) {
	if manager == nil || ctx == nil {
		return PluginDynamicActionResult{}, errors.New("plugin capability manager and context are required")
	}
	if err := pluginsdk.ValidatePolicyIdentity(request.OperationID); err != nil {
		return PluginDynamicActionResult{}, fmt.Errorf("plugin action operation id: %w", err)
	}
	unlockOperation := manager.lockOperation(request.OperationID)
	defer unlockOperation()

	fingerprint, err := pluginActionFingerprint(request)
	if err != nil {
		return PluginDynamicActionResult{}, err
	}
	now := time.Now().UTC()
	claimToken := capabilityAuditID()
	record, claimed, err := manager.store.ClaimPluginCapabilityOperation(ctx, "plugin.action", request.OperationID, fingerprint, request.OperationID, claimToken, now, now.Add(24*time.Hour))
	if err != nil {
		return PluginDynamicActionResult{}, err
	}
	if !claimed {
		if pluginActionOutcomePending(record.ResponseJSON) {
			return PluginDynamicActionResult{OperationID: request.OperationID, Replayed: true}, pluginActionPendingError(nil)
		}
		result, outcomeErr := decodePluginActionOutcome(record.ResponseJSON, request.OperationID)
		result.Replayed = true
		return result, outcomeErr
	}
	instance, _, action, generation, policy, err := manager.actionPolicy(ctx, request)
	if err != nil {
		return manager.completePluginAction(ctx, request.OperationID, claimToken, err)
	}
	queryCtx, cancelQuery := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	queryResult, queryErr := manager.runtime.QueryAction(queryCtx, request.InstanceID, generation, pluginsdk.RPCActionQueryRequest{Generation: generation, OperationID: request.OperationID})
	cancelQuery()
	if queryErr != nil {
		return manager.retainPendingPluginAction(ctx, request.OperationID, claimToken, queryErr)
	}
	if !queryResult.Missing {
		if queryResult.Pending {
			return manager.retainPendingPluginAction(ctx, request.OperationID, claimToken, nil)
		}
		if queryResult.Error != nil {
			return manager.completePluginAction(ctx, request.OperationID, claimToken, queryResult.Error)
		}
		if queryResult.Accepted {
			return manager.completePluginAction(ctx, request.OperationID, claimToken, nil)
		}
		return manager.retainPendingPluginAction(ctx, request.OperationID, claimToken, errors.New("plugin action query returned an unknown state"))
	}

	call := pluginsdk.HostCapabilityCall{
		PluginID: request.PluginID, InstanceID: request.InstanceID, Generation: generation,
		Actor:  pluginsdk.HostActor{ID: request.Actor.ID, ResourceGroupID: instance.ResourceGroupID},
		Target: request.Target, QuotaMetric: "plugin.action", QuotaUnits: 1,
	}
	dispatchCtx, cancelDispatch := context.WithTimeout(ctx, 25*time.Second)
	defer cancelDispatch()
	invokeRuntime := func(callCtx context.Context, _ pluginsdk.HostCapabilityCall, resourceHandle string) error {
		rpcRequest := pluginsdk.RPCActionRequest{
			Generation: generation, ActionID: action.ID, TargetKind: request.Target.Kind,
			TargetID: request.Target.ID, OperationID: request.OperationID,
		}
		if resourceHandle != "" {
			rpcRequest.TargetKind = ""
			rpcRequest.TargetID = ""
			rpcRequest.ResourceHandle = resourceHandle
			plan, planErr := manager.runtime.PlanAction(callCtx, request.InstanceID, generation, rpcRequest)
			if planErr != nil {
				return planErr
			}
			if len(plan.Calls) == 0 {
				return errors.New("resource-handle action did not request a typed core operation")
			}
			for _, resourceCall := range plan.Calls {
				resourceResult, executeErr := manager.executeDurableResourceCall(callCtx, request, resourceHandle, resourceCall)
				if executeErr != nil {
					return executeErr
				}
				rpcRequest.ResourceResults = append(rpcRequest.ResourceResults, resourceResult)
			}
		}
		return manager.runtime.InvokeAction(callCtx, request.InstanceID, generation, rpcRequest)
	}
	var dispatchErr error
	if action.Capability == pluginsdk.CapabilityServiceRevocableResourceHandle {
		handleToken, issueErr := manager.IssueResourceHandle(dispatchCtx, PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target})
		if issueErr != nil {
			dispatchErr = issueErr
		} else {
			defer manager.handles.Release(handleToken)
			stopTargetWatch := manager.watchTarget(dispatchCtx, request.Target)
			defer stopTargetWatch()
			dispatchErr = manager.actions.Dispatch(dispatchCtx, request.OperationID, action, call, policy, func(callCtx context.Context, actionCall pluginsdk.HostCapabilityCall) error {
				return invokeRuntime(callCtx, actionCall, handleToken)
			})
		}
	} else {
		dispatchErr = manager.actions.Dispatch(dispatchCtx, request.OperationID, action, call, policy, func(callCtx context.Context, actionCall pluginsdk.HostCapabilityCall) error {
			return invokeRuntime(callCtx, actionCall, "")
		})
	}
	if dispatchErr == nil {
		// A successful action may rotate or delete its target. Revoke the old
		// epoch before publishing the durable outcome; future calls must rebuild
		// authorization and handles from current platform state.
		manager.RevokeTarget(request.Target)
	}
	if dispatchErr == nil || pluginActionErrorIsDefinitive(dispatchErr) {
		return manager.completePluginAction(ctx, request.OperationID, claimToken, dispatchErr)
	}
	reconcileCtx, cancelReconcile := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	reconciled, reconcileErr := manager.runtime.QueryAction(reconcileCtx, request.InstanceID, generation, pluginsdk.RPCActionQueryRequest{Generation: generation, OperationID: request.OperationID})
	cancelReconcile()
	if reconcileErr == nil {
		if reconciled.Accepted {
			return manager.completePluginAction(ctx, request.OperationID, claimToken, nil)
		}
		if reconciled.Error != nil {
			return manager.completePluginAction(ctx, request.OperationID, claimToken, reconciled.Error)
		}
	}
	return manager.retainPendingPluginAction(ctx, request.OperationID, claimToken, errors.Join(dispatchErr, reconcileErr))
}

const pluginCapabilityResourceCallScope = "plugin.action.resource"

type pluginCapabilityResourceCallOutcome struct {
	Status    string                  `json:"status"`
	RequestID string                  `json:"request_id"`
	Value     []byte                  `json:"value,omitempty"`
	Error     *pluginsdk.RuntimeError `json:"error,omitempty"`
}

func (manager *PluginCapabilityManager) executeDurableResourceCall(ctx context.Context, request PluginDynamicActionRequest, expectedHandle string, call pluginsdk.RPCResourceCall) (pluginsdk.RPCResourceResult, error) {
	fingerprint, err := pluginCapabilityResourceCallFingerprint(request, call)
	if err != nil {
		return pluginsdk.RPCResourceResult{}, err
	}
	key := pluginCapabilityResourceCallKey(request.OperationID, call.RequestID)
	claimToken := capabilityAuditID()
	now := time.Now().UTC()
	record, claimed, err := manager.store.ClaimPluginCapabilityOperation(ctx, pluginCapabilityResourceCallScope, key, fingerprint, request.OperationID, claimToken, now, now.Add(24*time.Hour))
	if err != nil {
		return pluginsdk.RPCResourceResult{}, err
	}
	if !claimed {
		if pluginActionOutcomePending(record.ResponseJSON) {
			return pluginsdk.RPCResourceResult{}, pluginActionPendingError(errors.New("plugin resource call result is pending"))
		}
		return decodePluginCapabilityResourceCallOutcome(record.ResponseJSON, call.RequestID)
	}

	var result pluginsdk.RPCResourceResult
	if storage.PluginCapabilityOperationRecovered(record) {
		result = pluginsdk.RPCResourceResult{RequestID: call.RequestID, Error: &pluginsdk.RuntimeError{Code: pluginsdk.ErrorUnavailable, Message: "resource call outcome is unavailable after owner recovery", Retryable: false}}
	} else {
		var binding storage.PluginCapabilityTargetBinding
		var resolved any
		var resolveErr error
		if call.ResourceHandle != expectedHandle {
			resolveErr = fmt.Errorf("%w: plugin action plan used an unknown resource handle", pluginhost.ErrCapabilityDenied)
		} else {
			resolved, resolveErr = manager.ResolveResourceHandle(ctx, call.ResourceHandle, PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target})
		}
		if resolveErr == nil {
			reference, ok := resolved.(pluginResourceReference)
			if !ok {
				resolveErr = fmt.Errorf("%w: plugin action resource handle projection is invalid", pluginhost.ErrCapabilityDenied)
			} else {
				binding = storage.PluginCapabilityTargetBinding{Kind: reference.Target.Kind, ID: reference.Target.ID, ResourceGroupID: reference.Target.ResourceGroupID, Version: reference.Version}
			}
		}
		value, executeErr := []byte(nil), resolveErr
		if executeErr == nil {
			value, executeErr = manager.executeResourceCall(ctx, request, binding, call)
		}
		result = pluginsdk.RPCResourceResult{RequestID: call.RequestID, Value: value}
		if executeErr != nil {
			result.Value = nil
			result.Error = pluginCapabilityResourceRuntimeError(executeErr)
		}
	}
	if err := result.Validate(); err != nil {
		return pluginsdk.RPCResourceResult{}, err
	}
	encoded, err := json.Marshal(pluginCapabilityResourceCallOutcome{Status: "complete", RequestID: result.RequestID, Value: result.Value, Error: result.Error})
	if err != nil {
		return pluginsdk.RPCResourceResult{}, err
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := manager.store.CompletePluginCapabilityOperation(commitCtx, pluginCapabilityResourceCallScope, key, request.OperationID, claimToken, string(encoded)); err != nil {
		return pluginsdk.RPCResourceResult{}, errors.Join(errors.New("persist plugin resource call outcome"), err)
	}
	return result, nil
}

func pluginCapabilityResourceCallKey(operationID, requestID string) string {
	payload, _ := json.Marshal(struct {
		Domain      string `json:"domain"`
		OperationID string `json:"operation_id"`
		RequestID   string `json:"request_id"`
	}{"plugin.action.resource.v1", operationID, requestID})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func pluginCapabilityResourceCallFingerprint(request PluginDynamicActionRequest, call pluginsdk.RPCResourceCall) (string, error) {
	payload, err := json.Marshal(struct {
		OperationID, RequestID, PluginID, InstanceID, Kind, ID, Group string
		Operation                                                     pluginsdk.RPCResourceOperation
		Input                                                         []byte
	}{request.OperationID, call.RequestID, request.PluginID, request.InstanceID, request.Target.Kind, request.Target.ID, request.Target.ResourceGroupID, call.Operation, call.Input})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func decodePluginCapabilityResourceCallOutcome(value, requestID string) (pluginsdk.RPCResourceResult, error) {
	var outcome pluginCapabilityResourceCallOutcome
	if err := json.Unmarshal([]byte(value), &outcome); err != nil || outcome.Status != "complete" || outcome.RequestID != requestID {
		return pluginsdk.RPCResourceResult{}, errors.New("durable plugin resource call outcome is invalid")
	}
	result := pluginsdk.RPCResourceResult{RequestID: outcome.RequestID, Value: outcome.Value, Error: outcome.Error}
	if err := result.Validate(); err != nil {
		return pluginsdk.RPCResourceResult{}, err
	}
	return result, nil
}

func pluginCapabilityResourceRuntimeError(err error) *pluginsdk.RuntimeError {
	if err == nil {
		return nil
	}
	if errors.Is(err, pluginhost.ErrCapabilityDenied) {
		return &pluginsdk.RuntimeError{Code: pluginsdk.ErrorPermissionDenied, Message: "resource handle is no longer authorized", Retryable: false}
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "invalid") || strings.Contains(message, "unsupported") || strings.Contains(message, "unknown") || strings.Contains(message, "requires") {
		return &pluginsdk.RuntimeError{Code: pluginsdk.ErrorInvalidArgument, Message: "resource call request is invalid", Retryable: false}
	}
	return &pluginsdk.RuntimeError{Code: pluginsdk.ErrorUnavailable, Message: "resource call is unavailable", Retryable: true}
}

func (manager *PluginCapabilityManager) executeResourceCall(ctx context.Context, request PluginDynamicActionRequest, binding storage.PluginCapabilityTargetBinding, call pluginsdk.RPCResourceCall) ([]byte, error) {
	switch call.Operation {
	case pluginsdk.RPCResourceTrafficSummary:
		if binding.Kind != "agent" || manager.trafficSummary == nil {
			return nil, errors.New("traffic summary resource adapter is unavailable")
		}
		summary, err := manager.trafficSummary.Summary(ctx, binding.ID)
		if err != nil {
			return nil, err
		}
		return pluginCapabilityResourceJSON(struct {
			AgentID           string  `json:"agent_id"`
			CycleStart        string  `json:"cycle_start"`
			CycleEnd          string  `json:"cycle_end"`
			RXBytes           uint64  `json:"rx_bytes"`
			TXBytes           uint64  `json:"tx_bytes"`
			AccountedBytes    uint64  `json:"accounted_bytes"`
			UsedBytes         uint64  `json:"used_bytes"`
			MonthlyQuotaBytes *int64  `json:"monthly_quota_bytes"`
			QuotaPercent      float64 `json:"quota_percent"`
			RemainingBytes    *int64  `json:"remaining_bytes"`
			OverQuota         bool    `json:"over_quota"`
			Blocked           bool    `json:"blocked"`
		}{summary.AgentID, summary.CycleStart, summary.CycleEnd, summary.RXBytes, summary.TXBytes, summary.AccountedBytes, summary.UsedBytes, summary.MonthlyQuotaBytes, summary.QuotaPercent, summary.RemainingBytes, summary.OverQuota, summary.Blocked})
	case pluginsdk.RPCResourceDNSApply:
		if manager.secretVault == nil || manager.cloudflare == nil || (binding.Kind != "secret" && binding.Kind != "vault.secret") {
			return nil, errors.New("Cloudflare DNS resource adapter is unavailable")
		}
		var input struct {
			FQDN    string `json:"fqdn"`
			Type    string `json:"type"`
			Content string `json:"content"`
			TTL     int    `json:"ttl"`
		}
		decoder := json.NewDecoder(bytes.NewReader(call.Input))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.FQDN) == "" || (input.Type != "A" && input.Type != "AAAA") || strings.TrimSpace(input.Content) == "" || input.TTL < 60 || input.TTL > 86400 {
			return nil, errors.New("Cloudflare DNS resource request is invalid")
		}
		token, err := manager.secretVault.Resolve(ctx, secrets.OperationContext{ActorID: request.Actor.ID, SessionID: request.Actor.SessionID, ResourceGroupID: binding.ResourceGroupID}, binding.ID)
		if err != nil {
			return nil, err
		}
		defer func() {
			for index := range token {
				token[index] = 0
			}
		}()
		outcome, err := manager.cloudflare.EnsureRecord(ctx, string(token), strings.TrimSpace(input.FQDN), input.Type, strings.TrimSpace(input.Content), input.TTL)
		if err != nil {
			return nil, err
		}
		return pluginCapabilityResourceJSON(map[string]any{"action": outcome.Action})
	case pluginsdk.RPCResourceDockerRequest:
		if binding.Kind != "docker.socket" || runtime.GOOS == "windows" || strings.TrimSpace(manager.dockerSocket) == "" {
			return nil, errors.New("Docker resource adapter is unavailable")
		}
		var input struct {
			Action      string `json:"action"`
			ContainerID string `json:"container_id"`
		}
		decoder := json.NewDecoder(bytes.NewReader(call.Input))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, errors.New("Docker resource request is invalid")
		}
		method, path := http.MethodGet, "/_ping"
		switch input.Action {
		case "ping":
		case "start", "stop", "restart":
			if err := pluginsdk.ValidatePolicyIdentity(input.ContainerID); err != nil {
				return nil, errors.New("Docker container identity is invalid")
			}
			method, path = http.MethodPost, "/v1.41/containers/"+input.ContainerID+"/"+input.Action
		default:
			return nil, errors.New("Docker resource action is unsupported")
		}
		transport := &http.Transport{DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(dialCtx, "unix", manager.dockerSocket)
		}}
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		httpRequest, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(httpRequest)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, pluginsdk.RPCResourcePayloadMaxBytes+1))
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("Docker resource action failed with status %d", response.StatusCode)
		}
		return pluginCapabilityResourceJSON(map[string]any{"accepted": true, "status": response.StatusCode})
	default:
		return manager.store.ExecutePluginCapabilityResourceCall(ctx, binding, call)
	}
}

func pluginCapabilityResourceJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > pluginsdk.RPCResourcePayloadMaxBytes {
		return nil, errors.New("plugin capability resource response exceeds the canonical bound")
	}
	return encoded, nil
}

func pluginActionErrorIsDefinitive(err error) bool {
	var runtimeErr *pluginsdk.RuntimeError
	return errors.As(err, &runtimeErr)
}

func pluginActionOutcomePending(value string) bool {
	var pending struct {
		Status string `json:"status"`
	}
	return json.Unmarshal([]byte(value), &pending) == nil && pending.Status == "pending"
}

func pluginActionPendingError(cause error) error {
	return errors.Join(errPluginActionPending, &pluginsdk.RuntimeError{Code: pluginsdk.ErrorUnavailable, Message: "plugin action result is pending", Retryable: true}, cause)
}

func (manager *PluginCapabilityManager) retainPendingPluginAction(ctx context.Context, operationID, claimToken string, cause error) (PluginDynamicActionResult, error) {
	renewCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	renewErr := manager.store.RenewPluginCapabilityOperation(renewCtx, "plugin.action", operationID, operationID, claimToken, time.Now().UTC())
	return PluginDynamicActionResult{OperationID: operationID}, pluginActionPendingError(errors.Join(cause, renewErr))
}

func (manager *PluginCapabilityManager) lockOperation(operationID string) func() {
	manager.operationLocksMu.Lock()
	if manager.operationLocks == nil {
		manager.operationLocks = make(map[string]*pluginCapabilityOperationLock)
	}
	entry := manager.operationLocks[operationID]
	if entry == nil {
		entry = &pluginCapabilityOperationLock{}
		manager.operationLocks[operationID] = entry
	}
	entry.refs++
	manager.operationLocksMu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		manager.operationLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 && manager.operationLocks[operationID] == entry {
			delete(manager.operationLocks, operationID)
		}
		manager.operationLocksMu.Unlock()
	}
}

func (manager *PluginCapabilityManager) completePluginAction(ctx context.Context, operationID, claimToken string, actionErr error) (PluginDynamicActionResult, error) {
	outcome := pluginActionOutcome{Status: "succeeded"}
	if actionErr != nil {
		outcome = pluginActionFailedOutcome(actionErr)
	}
	responseJSON, err := json.Marshal(outcome)
	if err != nil {
		return PluginDynamicActionResult{OperationID: operationID}, errors.Join(err, actionErr)
	}
	commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := manager.store.CompletePluginCapabilityOperation(commitCtx, "plugin.action", operationID, operationID, claimToken, string(responseJSON)); err != nil {
		return PluginDynamicActionResult{OperationID: operationID}, errors.Join(errors.New("persist plugin action outcome"), err, actionErr)
	}
	return PluginDynamicActionResult{OperationID: operationID}, actionErr
}

func (manager *PluginCapabilityManager) actionPolicy(ctx context.Context, request PluginDynamicActionRequest) (storage.PluginInstanceRow, storage.PluginPackageRow, pluginsdk.DynamicAction, string, pluginhost.CapabilityPolicy, error) {
	instance, packageRow, validated, generation, policy, err := manager.capabilityPolicy(ctx, PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target})
	if err != nil {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, pluginsdk.DynamicAction{}, "", pluginhost.CapabilityPolicy{}, err
	}
	action, ok := findDynamicAction(validated.DynamicActions, request.ActionID, request.Target.Kind)
	if !ok {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, pluginsdk.DynamicAction{}, "", pluginhost.CapabilityPolicy{}, errors.New("dynamic action is absent from the signed declarative UI")
	}
	return instance, packageRow, action, generation, policy, nil
}

func (manager *PluginCapabilityManager) capabilityPolicy(ctx context.Context, request PluginResourceHandleRequest) (storage.PluginInstanceRow, storage.PluginPackageRow, plugins.ValidatedPackage, string, pluginhost.CapabilityPolicy, error) {
	targetBinding, exists, err := manager.store.PluginCapabilityTargetBinding(ctx, request.Target.Kind, request.Target.ID)
	if err != nil || !exists || targetBinding.Version == "" || targetBinding.ResourceGroupID == "" {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, plugins.ValidatedPackage{}, "", pluginhost.CapabilityPolicy{}, errors.Join(errors.New("plugin capability target binding is unavailable"), err)
	}
	if request.Target.ResourceGroupID != targetBinding.ResourceGroupID {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, plugins.ValidatedPackage{}, "", pluginhost.CapabilityPolicy{}, errors.New("plugin capability target resource group is not authoritative")
	}
	instance, ok, err := manager.store.GetPluginInstance(ctx, request.InstanceID)
	if err != nil || !ok {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, plugins.ValidatedPackage{}, "", pluginhost.CapabilityPolicy{}, errors.Join(errors.New("plugin capability instance is unavailable"), err)
	}
	if instance.PluginID != request.PluginID || instance.ResourceGroupID != request.Target.ResourceGroupID || !instance.DesiredEnabled {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, plugins.ValidatedPackage{}, "", pluginhost.CapabilityPolicy{}, errors.New("plugin capability instance is not an enabled owner of the target")
	}
	installed, ok, err := manager.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil || !ok || installed.ActivePackageIdentity == "" || installed.ActivePackageDigest == "" {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, plugins.ValidatedPackage{}, "", pluginhost.CapabilityPolicy{}, errors.Join(errors.New("plugin capability active package is unavailable"), err)
	}
	packageRow, ok, err := manager.store.GetPluginPackageByIdentity(ctx, installed.ActivePackageIdentity)
	if err != nil || !ok || packageRow.Digest != installed.ActivePackageDigest || packageRow.PluginID != request.PluginID {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, plugins.ValidatedPackage{}, "", pluginhost.CapabilityPolicy{}, errors.Join(errors.New("plugin capability package identity is stale"), err)
	}
	validated, err := manager.loadPackage(ctx, packageRow)
	if err != nil {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, plugins.ValidatedPackage{}, "", pluginhost.CapabilityPolicy{}, fmt.Errorf("validate plugin capability package: %w", err)
	}
	grants, err := manager.store.ListPluginGrants(ctx, request.PluginID)
	if err != nil {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, plugins.ValidatedPackage{}, "", pluginhost.CapabilityPolicy{}, fmt.Errorf("load current plugin grants: %w", err)
	}
	generation, ok := manager.runtime.ActiveGeneration(request.InstanceID)
	if !ok {
		return storage.PluginInstanceRow{}, storage.PluginPackageRow{}, plugins.ValidatedPackage{}, "", pluginhost.CapabilityPolicy{}, errors.New("plugin capability runtime generation is not active")
	}
	policy, err := BuildPluginCapabilityPolicy(ctx, manager.store, manager.resourceAuthorizer, PluginCapabilityPolicyInput{Package: validated, PackageIdentity: packageRow.Identity, Instance: instance, Grants: grants, Actor: request.Actor, Generation: generation, Target: request.Target})
	return instance, packageRow, validated, generation, policy, err
}

func (manager *PluginCapabilityManager) IssueResourceHandle(ctx context.Context, request PluginResourceHandleRequest) (string, error) {
	instance, _, _, generation, policy, err := manager.capabilityPolicy(ctx, request)
	if err != nil {
		return "", err
	}
	binding, exists, err := manager.store.PluginCapabilityTargetBinding(ctx, request.Target.Kind, request.Target.ID)
	if err != nil || !exists || binding.Version == "" || binding.ResourceGroupID != request.Target.ResourceGroupID {
		return "", errors.Join(errors.New("plugin capability target is unavailable"), err)
	}
	call := pluginsdk.HostCapabilityCall{
		PluginID: request.PluginID, InstanceID: request.InstanceID, Generation: generation,
		Actor:  pluginsdk.HostActor{ID: request.Actor.ID, ResourceGroupID: instance.ResourceGroupID},
		Target: request.Target, QuotaMetric: "plugin.resource.handle.issue", QuotaUnits: 1,
	}
	return manager.handles.Issue(ctx, policy, call, pluginResourceReference{Target: request.Target, Version: binding.Version})
}

func (manager *PluginCapabilityManager) ResolveResourceHandle(ctx context.Context, token string, request PluginResourceHandleRequest) (any, error) {
	instance, _, _, generation, policy, err := manager.capabilityPolicy(ctx, request)
	if err != nil {
		return nil, err
	}
	call := pluginsdk.HostCapabilityCall{
		PluginID: request.PluginID, InstanceID: request.InstanceID, Generation: generation,
		Actor:  pluginsdk.HostActor{ID: request.Actor.ID, ResourceGroupID: instance.ResourceGroupID},
		Target: request.Target, QuotaMetric: "plugin.resource.handle.resolve", QuotaUnits: 1,
	}
	resource, err := manager.handles.ResolveWithPolicy(ctx, token, call, policy)
	if err != nil {
		return nil, err
	}
	reference, ok := resource.(pluginResourceReference)
	if !ok || reference.Target != request.Target {
		return nil, fmt.Errorf("%w: plugin resource handle projection is invalid", pluginhost.ErrCapabilityDenied)
	}
	binding, exists, err := manager.store.PluginCapabilityTargetBinding(ctx, request.Target.Kind, request.Target.ID)
	if err != nil || !exists || binding.Version != reference.Version || binding.ResourceGroupID != request.Target.ResourceGroupID {
		manager.RevokeTarget(request.Target)
		return nil, fmt.Errorf("%w: plugin resource handle target was deleted or rotated", pluginhost.ErrCapabilityDenied)
	}
	return reference, nil
}

func (manager *PluginCapabilityManager) watchTarget(ctx context.Context, target pluginsdk.HostTarget) func() {
	watchCtx, cancel := context.WithCancel(ctx)
	binding, exists, err := manager.store.PluginCapabilityTargetBinding(watchCtx, target.Kind, target.ID)
	if err != nil || !exists || binding.Version == "" || binding.ResourceGroupID != target.ResourceGroupID {
		manager.RevokeTarget(target)
		cancel()
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				current, currentExists, currentErr := manager.store.PluginCapabilityTargetBinding(watchCtx, target.Kind, target.ID)
				if currentErr != nil || !currentExists || current.Version != binding.Version || current.ResourceGroupID != binding.ResourceGroupID {
					manager.RevokeTarget(target)
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func pluginActionFingerprint(request PluginDynamicActionRequest) (string, error) {
	payload, err := json.Marshal(struct {
		PluginID, InstanceID, ActionID, ActorID, TargetKind, TargetID, ResourceGroupID string
	}{request.PluginID, request.InstanceID, request.ActionID, request.Actor.ID, request.Target.Kind, request.Target.ID, request.Target.ResourceGroupID})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

var errPluginActionPending = errors.New("plugin action operation is pending")

type pluginActionOutcome struct {
	Status      string              `json:"status"`
	Error       string              `json:"error,omitempty"`
	ErrorClass  string              `json:"error_class,omitempty"`
	RuntimeCode pluginsdk.ErrorCode `json:"runtime_code,omitempty"`
	Retryable   bool                `json:"retryable,omitempty"`
}

func pluginActionFailedOutcome(actionErr error) pluginActionOutcome {
	outcome := pluginActionOutcome{Status: "failed", Error: safeRuntimeError(actionErr), ErrorClass: "internal"}
	switch {
	case errors.Is(actionErr, pluginhost.ErrCapabilityDenied):
		outcome.ErrorClass = "capability_denied"
	case errors.Is(actionErr, context.DeadlineExceeded):
		outcome.ErrorClass = "deadline_exceeded"
	case errors.Is(actionErr, context.Canceled):
		outcome.ErrorClass = "canceled"
	default:
		var runtimeErr *pluginsdk.RuntimeError
		if errors.As(actionErr, &runtimeErr) {
			outcome.ErrorClass = "runtime"
			outcome.RuntimeCode = runtimeErr.Code
			outcome.Retryable = runtimeErr.Retryable
			outcome.Error = runtimeErr.Message
		}
	}
	return outcome
}

func decodePluginActionOutcome(value, operationID string) (PluginDynamicActionResult, error) {
	var outcome pluginActionOutcome
	if err := json.Unmarshal([]byte(value), &outcome); err != nil {
		return PluginDynamicActionResult{}, fmt.Errorf("decode durable plugin action outcome: %w", err)
	}
	result := PluginDynamicActionResult{OperationID: operationID}
	switch outcome.Status {
	case "succeeded":
		return result, nil
	case "failed":
		switch outcome.ErrorClass {
		case "capability_denied":
			return result, fmt.Errorf("%w: %s", pluginhost.ErrCapabilityDenied, outcome.Error)
		case "deadline_exceeded":
			return result, errors.Join(context.DeadlineExceeded, errors.New(outcome.Error))
		case "canceled":
			return result, errors.Join(context.Canceled, errors.New(outcome.Error))
		case "runtime":
			runtimeErr := &pluginsdk.RuntimeError{Code: outcome.RuntimeCode, Message: outcome.Error, Retryable: outcome.Retryable}
			if err := runtimeErr.Validate(); err != nil {
				return PluginDynamicActionResult{}, fmt.Errorf("decode durable plugin action runtime outcome: %w", err)
			}
			return result, runtimeErr
		case "internal", "":
			return result, errors.New(outcome.Error)
		default:
			return PluginDynamicActionResult{}, errors.New("durable plugin action error class is invalid")
		}
	case "pending":
		return result, errPluginActionPending
	default:
		return PluginDynamicActionResult{}, errors.New("durable plugin action outcome is invalid")
	}
}

func (manager *PluginCapabilityManager) RevokeGeneration(instanceID, generation string) {
	if manager == nil {
		return
	}
	manager.handles.RevokeGeneration(instanceID, generation)
	manager.actions.RevokeGeneration(instanceID, generation)
}

func (manager *PluginCapabilityManager) RevokeTarget(target pluginsdk.HostTarget) {
	if manager != nil {
		manager.handles.RevokeTarget(target)
		manager.actions.RevokeTarget(target)
	}
}

func findDynamicAction(actions []pluginsdk.DynamicAction, id, targetKind string) (pluginsdk.DynamicAction, bool) {
	for _, action := range actions {
		if action.ID == id && action.TargetKind == targetKind {
			return action, true
		}
	}
	return pluginsdk.DynamicAction{}, false
}

func (s *PluginService) loadValidatedCapabilityPackage(ctx context.Context, row storage.PluginPackageRow) (plugins.ValidatedPackage, error) {
	if err := s.validateStoredPackageIntegrity(ctx, row); err != nil {
		return plugins.ValidatedPackage{}, err
	}
	validator, err := s.packageBoundValidator(row)
	if err != nil {
		return plugins.ValidatedPackage{}, err
	}
	validated, err := validator.ValidatePackageIntegrity(row.CachePath, plugins.PackageExpectation{ID: row.PluginID, Version: row.Version, SHA256: row.Digest, SignatureKeyID: row.SignatureKeyID})
	if err != nil {
		return plugins.ValidatedPackage{}, err
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(row.ManifestJSON), &manifest); err != nil || !reflect.DeepEqual(manifest, validated.Manifest) {
		return plugins.ValidatedPackage{}, errors.New("durable manifest differs from signed package projection")
	}
	if strings.HasPrefix(validated.Manifest.ID, "official.") && validated.Manifest.Signature.KeyID != plugins.OfficialSignatureKeyID {
		return plugins.ValidatedPackage{}, errors.New("official package signer identity is not canonical")
	}
	return validated, nil
}
