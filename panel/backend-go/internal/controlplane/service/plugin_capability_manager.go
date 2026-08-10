package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type PluginCapabilityManagerStore interface {
	PluginCapabilityStore
	GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error)
	GetPluginPackageByIdentity(context.Context, string) (storage.PluginPackageRow, bool, error)
	ListPluginGrants(context.Context, string) ([]storage.PluginGrantRow, error)
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	ClaimPluginCapabilityOperation(context.Context, string, string, string, string, string, time.Time, time.Time) (storage.IdempotencyRecordRow, bool, error)
	CompletePluginCapabilityOperation(context.Context, string, string, string, string, string) error
}

type PluginCapabilityRuntime interface {
	ActiveGeneration(string) (string, bool)
	InvokeAction(context.Context, string, string, pluginsdk.RPCActionRequest) error
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

type PluginCapabilityManager struct {
	store              PluginCapabilityManagerStore
	resourceAuthorizer PluginCapabilityResourceAuthorizer
	runtime            PluginCapabilityRuntime
	loadPackage        func(context.Context, storage.PluginPackageRow) (plugins.ValidatedPackage, error)
	handles            *pluginhost.ResourceHandleBroker
	actions            *pluginhost.DynamicActionRegistry
	operationLocksMu   sync.Mutex
	operationLocks     map[string]*pluginCapabilityOperationLock
}

type pluginCapabilityOperationLock struct {
	mu   sync.Mutex
	refs int
}

func NewPluginCapabilityManager(store PluginCapabilityManagerStore, resourceAuthorizer PluginCapabilityResourceAuthorizer, runtime PluginCapabilityRuntime, packages *PluginService) (*PluginCapabilityManager, error) {
	if store == nil || resourceAuthorizer == nil || runtime == nil || packages == nil {
		return nil, errors.New("plugin capability durable store, authorization owner, package validator, and runtime are required")
	}
	manager := &PluginCapabilityManager{store: store, resourceAuthorizer: resourceAuthorizer, runtime: runtime, handles: pluginhost.NewResourceHandleBroker(), actions: pluginhost.NewDynamicActionRegistry(), operationLocks: make(map[string]*pluginCapabilityOperationLock)}
	manager.loadPackage = packages.loadValidatedCapabilityPackage
	return manager, nil
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
		result, outcomeErr := decodePluginActionOutcome(record.ResponseJSON, request.OperationID)
		result.Replayed = true
		return result, outcomeErr
	}
	instance, _, action, generation, policy, err := manager.actionPolicy(ctx, request)
	if err != nil {
		return manager.completePluginAction(ctx, request.OperationID, claimToken, err)
	}

	call := pluginsdk.HostCapabilityCall{
		PluginID: request.PluginID, InstanceID: request.InstanceID, Generation: generation,
		Actor:  pluginsdk.HostActor{ID: request.Actor.ID, ResourceGroupID: instance.ResourceGroupID},
		Target: request.Target, QuotaMetric: "plugin.action", QuotaUnits: 1,
	}
	dispatchCtx, cancelDispatch := context.WithTimeout(ctx, 25*time.Second)
	defer cancelDispatch()
	invokeRuntime := func(callCtx context.Context, _ pluginsdk.HostCapabilityCall) error {
		return manager.runtime.InvokeAction(callCtx, request.InstanceID, generation, pluginsdk.RPCActionRequest{
			Generation: generation, ActionID: action.ID, TargetKind: request.Target.Kind,
			TargetID: request.Target.ID, OperationID: request.OperationID,
		})
	}
	var dispatchErr error
	if action.Capability == pluginsdk.CapabilityServiceRevocableResourceHandle {
		handleCall := call
		handleCall.Capability = pluginsdk.CapabilityServiceRevocableResourceHandle
		handleCall.QuotaMetric = "plugin.resource.handle.action"
		handleToken, issueErr := manager.handles.Issue(dispatchCtx, policy, handleCall, request.Target)
		if issueErr != nil {
			dispatchErr = issueErr
		} else {
			defer manager.handles.Release(handleToken)
			dispatchErr = manager.actions.Dispatch(dispatchCtx, request.OperationID, action, call, policy, func(callCtx context.Context, actionCall pluginsdk.HostCapabilityCall) error {
				resolved, resolveErr := manager.handles.ResolveWithPolicy(callCtx, handleToken, handleCall, policy)
				if resolveErr != nil {
					return resolveErr
				}
				if target, ok := resolved.(pluginsdk.HostTarget); !ok || target != request.Target {
					return errors.New("plugin action resource handle target is invalid")
				}
				return invokeRuntime(callCtx, actionCall)
			})
		}
	} else {
		dispatchErr = manager.actions.Dispatch(dispatchCtx, request.OperationID, action, call, policy, invokeRuntime)
	}
	if dispatchErr == nil {
		// A successful action may rotate or delete its target. Revoke the old
		// epoch before publishing the durable outcome; future calls must rebuild
		// authorization and handles from current platform state.
		manager.RevokeTarget(request.Target)
	}
	return manager.completePluginAction(ctx, request.OperationID, claimToken, dispatchErr)
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

func (manager *PluginCapabilityManager) IssueResourceHandle(ctx context.Context, request PluginResourceHandleRequest, resource any) (string, error) {
	instance, _, _, generation, policy, err := manager.capabilityPolicy(ctx, request)
	if err != nil {
		return "", err
	}
	call := pluginsdk.HostCapabilityCall{
		PluginID: request.PluginID, InstanceID: request.InstanceID, Generation: generation,
		Actor:  pluginsdk.HostActor{ID: request.Actor.ID, ResourceGroupID: instance.ResourceGroupID},
		Target: request.Target, QuotaMetric: "plugin.resource.handle.issue", QuotaUnits: 1,
	}
	return manager.handles.Issue(ctx, policy, call, resource)
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
	return manager.handles.ResolveWithPolicy(ctx, token, call, policy)
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
