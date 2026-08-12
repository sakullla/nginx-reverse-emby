package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type capabilityManagerTargetStore struct {
	*capabilityManagerStore
	targets *storage.SQLiteStore
}

func (store *capabilityManagerTargetStore) PluginCapabilityTargetBinding(ctx context.Context, kind, id string) (storage.PluginCapabilityTargetBinding, bool, error) {
	return store.targets.PluginCapabilityTargetBinding(ctx, kind, id)
}

type capabilityManagerStore struct {
	capabilityTestStore
	mu            sync.Mutex
	installed     storage.InstalledPluginRow
	packageRow    storage.PluginPackageRow
	instance      storage.PluginInstanceRow
	grants        []storage.PluginGrantRow
	operations    map[string]storage.IdempotencyRecordRow
	targetVersion string
	targetGroup   string
	targetExists  bool
	resourceCalls map[string]int
	resourceErrs  map[string]error
	completeFails map[string]int
}

func (store *capabilityManagerStore) ClaimPluginCapabilityOperation(_ context.Context, scope, key, fingerprint, operationID, claimToken string, now, expiresAt time.Time) (storage.IdempotencyRecordRow, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.operations == nil {
		store.operations = make(map[string]storage.IdempotencyRecordRow)
	}
	mapKey := scope + "\x00" + key
	if existing, ok := store.operations[mapKey]; ok && existing.ExpiresAt.After(now) {
		if existing.RequestFingerprint != fingerprint || existing.OperationID != operationID {
			return storage.IdempotencyRecordRow{}, false, storage.ErrPluginCapabilityOperationConflict
		}
		return existing, false, nil
	}
	if existing, ok := store.operations[mapKey]; ok {
		if existing.RequestFingerprint != fingerprint || existing.OperationID != operationID {
			return storage.IdempotencyRecordRow{}, false, storage.ErrPluginCapabilityOperationConflict
		}
		existing.ResponseJSON = `{"status":"pending","claim_token":"` + claimToken + `","recovered":true}`
		existing.ExpiresAt = expiresAt
		store.operations[mapKey] = existing
		return existing, true, nil
	}
	record := storage.IdempotencyRecordRow{Scope: scope, Key: key, RequestFingerprint: fingerprint, OperationID: operationID, ResponseJSON: `{"status":"pending","claim_token":"` + claimToken + `"}`, CreatedAt: now, ExpiresAt: expiresAt}
	store.operations[mapKey] = record
	return record, true, nil
}

func (store *capabilityManagerStore) CompletePluginCapabilityOperation(_ context.Context, scope, key, operationID, _ string, responseJSON string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	mapKey := scope + "\x00" + key
	if store.completeFails[mapKey] > 0 {
		store.completeFails[mapKey]--
		return errors.New("injected operation completion failure")
	}
	record, ok := store.operations[mapKey]
	if !ok || record.OperationID != operationID {
		return errors.New("operation owner missing")
	}
	record.ResponseJSON = responseJSON
	store.operations[mapKey] = record
	return nil
}

func (store *capabilityManagerStore) RenewPluginCapabilityOperation(_ context.Context, scope, key, operationID, _ string, now time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	mapKey := scope + "\x00" + key
	record, ok := store.operations[mapKey]
	if !ok || record.OperationID != operationID {
		return errors.New("operation owner missing")
	}
	record.ExpiresAt = now.Add(storage.PluginCapabilityOperationLease)
	store.operations[mapKey] = record
	return nil
}

func (store *capabilityManagerStore) GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.installed, true, nil
}

func (store *capabilityManagerStore) GetPluginPackageByIdentity(context.Context, string) (storage.PluginPackageRow, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.packageRow, true, nil
}

func (store *capabilityManagerStore) ListPluginGrants(context.Context, string) ([]storage.PluginGrantRow, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]storage.PluginGrantRow(nil), store.grants...), nil
}

func (store *capabilityManagerStore) GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.instance, true, nil
}

func (store *capabilityManagerStore) PluginCapabilityTargetBinding(_ context.Context, kind, id string) (storage.PluginCapabilityTargetBinding, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	groupID := store.targetGroup
	if groupID == "" {
		groupID = store.instance.ResourceGroupID
	}
	return storage.PluginCapabilityTargetBinding{Kind: kind, ID: id, ResourceGroupID: groupID, Version: store.targetVersion}, store.targetExists, nil
}

func (store *capabilityManagerStore) ExecutePluginCapabilityResourceCall(_ context.Context, binding storage.PluginCapabilityTargetBinding, call pluginsdk.RPCResourceCall) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.resourceCalls == nil {
		store.resourceCalls = make(map[string]int)
	}
	store.resourceCalls[call.RequestID]++
	if binding.Version != store.targetVersion || call.ResourceHandle == "" {
		return nil, errors.New("stale resource adapter call")
	}
	if err := store.resourceErrs[call.RequestID]; err != nil {
		return nil, err
	}
	return []byte(`{"available":true}`), nil
}

type capabilityTrafficSummaryStub struct {
	summary TrafficSummary
	err     error
}

func (stub capabilityTrafficSummaryStub) Summary(context.Context, string) (TrafficSummary, error) {
	return stub.summary, stub.err
}

type capabilityRuntimeStub struct {
	mu          sync.Mutex
	generation  string
	calls       int
	invokeErr   error
	commitErr   bool
	lastRequest pluginsdk.RPCActionRequest
	afterCall   func()
	started     chan struct{}
	release     chan struct{}
	block       bool
	results     map[string]pluginsdk.RPCActionResponse
	queryErr    error
	plan        pluginsdk.RPCActionPlanResponse
}

func (runtime *capabilityRuntimeStub) ActiveGeneration(string) (string, bool) {
	return runtime.generation, runtime.generation != ""
}

func (runtime *capabilityRuntimeStub) PlanAction(_ context.Context, _, _ string, request pluginsdk.RPCActionRequest) (pluginsdk.RPCActionPlanResponse, error) {
	if runtime.plan.Error != nil || len(runtime.plan.Calls) != 0 {
		plan := runtime.plan
		plan.Calls = append([]pluginsdk.RPCResourceCall(nil), plan.Calls...)
		for index := range plan.Calls {
			if plan.Calls[index].ResourceHandle == "" {
				plan.Calls[index].ResourceHandle = request.ResourceHandle
			}
		}
		return plan, nil
	}
	if request.ResourceHandle == "" {
		return runtime.plan, nil
	}
	return pluginsdk.RPCActionPlanResponse{Calls: []pluginsdk.RPCResourceCall{{RequestID: "resource-call-1", ResourceHandle: request.ResourceHandle, Operation: pluginsdk.RPCResourceInspect}}}, nil
}

func (runtime *capabilityRuntimeStub) InvokeAction(ctx context.Context, _, generation string, request pluginsdk.RPCActionRequest) error {
	if generation != runtime.generation || request.Generation != runtime.generation {
		return errors.New("wrong runtime generation")
	}
	runtime.mu.Lock()
	runtime.calls++
	runtime.lastRequest = request
	afterCall, invokeErr, started, block := runtime.afterCall, runtime.invokeErr, runtime.started, runtime.block
	commitErr := runtime.commitErr
	runtime.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-runtime.release:
		}
	}
	if afterCall != nil {
		afterCall()
	}
	runtime.mu.Lock()
	if runtime.results == nil {
		runtime.results = make(map[string]pluginsdk.RPCActionResponse)
	}
	if invokeErr == nil || commitErr {
		runtime.results[request.OperationID] = pluginsdk.RPCActionResponse{Accepted: true, OperationID: request.OperationID}
	} else {
		var runtimeErr *pluginsdk.RuntimeError
		if errors.As(invokeErr, &runtimeErr) {
			runtime.results[request.OperationID] = pluginsdk.RPCActionResponse{OperationID: request.OperationID, Error: runtimeErr}
		}
	}
	runtime.mu.Unlock()
	return invokeErr
}

func (runtime *capabilityRuntimeStub) QueryAction(_ context.Context, _, generation string, request pluginsdk.RPCActionQueryRequest) (pluginsdk.RPCActionResponse, error) {
	if generation != runtime.generation || request.Generation != runtime.generation {
		return pluginsdk.RPCActionResponse{}, errors.New("wrong runtime generation")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.queryErr != nil {
		return pluginsdk.RPCActionResponse{}, runtime.queryErr
	}
	if result, ok := runtime.results[request.OperationID]; ok {
		return result, nil
	}
	return pluginsdk.RPCActionResponse{OperationID: request.OperationID, Missing: true}, nil
}

func (runtime *capabilityRuntimeStub) callCount() int {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.calls
}

func (runtime *capabilityRuntimeStub) request() pluginsdk.RPCActionRequest {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.lastRequest
}

func TestPluginCapabilityManagerRereadsDurableGrantAndDispatchesExactRuntimeGeneration(t *testing.T) {
	digest, identity := strings.Repeat("a", 64), strings.Repeat("b", 64)
	pluginID := "official.service"
	target := pluginsdk.HostTarget{Kind: "relay", ID: "relay-1", ResourceGroupID: "group-a"}
	action := pluginsdk.DynamicAction{ID: "rotate", Label: "Rotate", Capability: pluginsdk.CapabilityServiceRevocableResourceHandle, TargetKind: target.Kind}
	validated := plugins.ValidatedPackage{Digest: digest, Manifest: plugins.Manifest{ID: pluginID, Signature: plugins.Signature{Algorithm: "ed25519", KeyID: plugins.OfficialSignatureKeyID}, Permissions: []plugins.Permission{{Name: string(pluginsdk.CapabilityUIDynamicActions)}, {Name: string(pluginsdk.CapabilityServiceRevocableResourceHandle)}}}, DynamicActions: []pluginsdk.DynamicAction{action}}
	grants := []storage.PluginGrantRow{
		{ID: "grant-ui", PluginID: pluginID, PackageDigest: digest, PackageIdentity: identity, Permission: string(pluginsdk.CapabilityUIDynamicActions)},
		{ID: "grant-handle", PluginID: pluginID, PackageDigest: digest, PackageIdentity: identity, Permission: string(pluginsdk.CapabilityServiceRevocableResourceHandle)},
	}
	store := &capabilityManagerStore{installed: storage.InstalledPluginRow{PluginID: pluginID, ActivePackageDigest: digest, ActivePackageIdentity: identity}, packageRow: storage.PluginPackageRow{Identity: identity, Digest: digest, PluginID: pluginID}, instance: storage.PluginInstanceRow{ID: "instance-1", PluginID: pluginID, ResourceGroupID: target.ResourceGroupID, DesiredEnabled: true}, grants: grants, targetVersion: "target-version-1", targetExists: true}
	runtime := &capabilityRuntimeStub{generation: "generation-1"}
	manager := &PluginCapabilityManager{store: store, resourceAuthorizer: capabilityTestAuthorizer{}, runtime: runtime, handles: pluginhost.NewResourceHandleBroker(), actions: pluginhost.NewDynamicActionRegistry()}
	manager.loadPackage = func(context.Context, storage.PluginPackageRow) (plugins.ValidatedPackage, error) {
		return validated, nil
	}
	actor := authz.Actor{ID: "operator", Permissions: []string{authz.PermissionResourceWrite}, VisibleResourceGroups: []string{target.ResourceGroupID}}
	request := PluginDynamicActionRequest{OperationID: "action-op-1", PluginID: pluginID, InstanceID: "instance-1", ActionID: action.ID, Actor: actor, Target: target}
	if _, err := manager.InvokeDynamicAction(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 1 || store.quotaCalls != 4 || len(store.audits) != 4 {
		t.Fatalf("dispatch side effects calls=%d quota=%d audits=%d", runtime.calls, store.quotaCalls, len(store.audits))
	}

	store.mu.Lock()
	store.grants = store.grants[:1]
	store.mu.Unlock()
	request.OperationID = "action-op-2"
	if _, err := manager.InvokeDynamicAction(t.Context(), request); err == nil || runtime.calls != 1 {
		t.Fatalf("deleted durable grant dispatch error=%v calls=%d", err, runtime.calls)
	}

	store.mu.Lock()
	store.grants = append([]storage.PluginGrantRow(nil), grants...)
	store.mu.Unlock()
	request.OperationID = "action-op-3"
	runtime.afterCall = func() {
		store.mu.Lock()
		store.grants = store.grants[:1]
		store.mu.Unlock()
	}
	if result, err := manager.InvokeDynamicAction(t.Context(), request); err != nil || result.OperationID != request.OperationID {
		t.Fatalf("committed action after unrelated durable change result=%+v error=%v", result, err)
	}
	runtime.afterCall = nil
	store.mu.Lock()
	store.installed.ActivePackageIdentity = ""
	store.installed.ActivePackageDigest = ""
	store.mu.Unlock()
	result, err := manager.InvokeDynamicAction(t.Context(), request)
	if err != nil || !result.Replayed || runtime.calls != 2 {
		t.Fatalf("durable exact replay result=%+v error=%v calls=%d", result, err, runtime.calls)
	}
	request.Target.ID = "relay-2"
	if _, err := manager.InvokeDynamicAction(t.Context(), request); !errors.Is(err, storage.ErrPluginCapabilityOperationConflict) || runtime.calls != 2 {
		t.Fatalf("mismatched operation reuse error=%v calls=%d", err, runtime.calls)
	}
}

func TestPluginCapabilityManagerCrossManagerPendingDoesNotDuplicate(t *testing.T) {
	first, store, runtime, request := newCapabilityManagerFixture(t)
	second := &PluginCapabilityManager{store: store, resourceAuthorizer: first.resourceAuthorizer, runtime: runtime, handles: pluginhost.NewResourceHandleBroker(), actions: pluginhost.NewDynamicActionRegistry()}
	second.loadPackage = first.loadPackage
	runtime.block = true
	runtime.started = make(chan struct{}, 1)
	runtime.release = make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := first.InvokeDynamicAction(context.Background(), request)
		firstDone <- err
	}()
	<-runtime.started
	result, err := second.InvokeDynamicAction(t.Context(), request)
	if !errors.Is(err, errPluginActionPending) || !result.Replayed || runtime.callCount() != 1 {
		t.Fatalf("cross-manager pending result=%+v error=%v calls=%d", result, err, runtime.callCount())
	}
	close(runtime.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	result, err = second.InvokeDynamicAction(t.Context(), request)
	if err != nil || !result.Replayed || runtime.callCount() != 1 {
		t.Fatalf("cross-manager replay result=%+v error=%v calls=%d", result, err, runtime.callCount())
	}
}

func TestPluginCapabilityManagerGenerationDrainCancelsRealRuntimeDispatch(t *testing.T) {
	manager, _, runtime, request := newCapabilityManagerFixture(t)
	runtime.block = true
	runtime.started = make(chan struct{}, 1)
	runtime.release = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := manager.InvokeDynamicAction(context.Background(), request)
		done <- err
	}()
	<-runtime.started
	manager.RevokeGeneration(request.InstanceID, runtime.generation)
	select {
	case err := <-done:
		if !errors.Is(err, pluginhost.ErrCapabilityDenied) {
			t.Fatalf("drained dispatch error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("generation drain did not cancel the guest RPC")
	}
	result, err := manager.InvokeDynamicAction(t.Context(), request)
	if !errors.Is(err, errPluginActionPending) || !result.Replayed || runtime.callCount() != 1 {
		t.Fatalf("durable ambiguous replay result=%+v error=%v calls=%d", result, err, runtime.callCount())
	}
}

func TestPluginCapabilityManagerCompletionFailureAfterSideEffectRecoversWithoutReexecution(t *testing.T) {
	manager, store, _, request := newCapabilityManagerFixture(t)
	call := pluginsdk.RPCResourceCall{RequestID: "resource-call-side-effect", ResourceHandle: mustIssueCapabilityResourceHandle(t, manager, request), Operation: pluginsdk.RPCResourceInspect}
	key := pluginCapabilityResourceCallKey(request.OperationID, call.RequestID)
	mapKey := pluginCapabilityResourceCallScope + "\x00" + key
	store.completeFails = map[string]int{mapKey: 1}
	if _, err := manager.executeDurableResourceCall(t.Context(), request, call.ResourceHandle, call); err == nil || store.resourceCalls[call.RequestID] != 1 {
		t.Fatalf("first post-side-effect completion error=%v calls=%d", err, store.resourceCalls[call.RequestID])
	}
	store.mu.Lock()
	record := store.operations[mapKey]
	record.ExpiresAt = time.Now().Add(-time.Second)
	store.operations[mapKey] = record
	store.mu.Unlock()
	result, err := manager.executeDurableResourceCall(t.Context(), request, call.ResourceHandle, call)
	if err != nil || result.Error == nil || result.Error.Code != pluginsdk.ErrorUnavailable || store.resourceCalls[call.RequestID] != 1 {
		t.Fatalf("recovered post-side-effect result=%+v error=%v calls=%d", result, err, store.resourceCalls[call.RequestID])
	}
	replayed, err := manager.executeDurableResourceCall(t.Context(), request, call.ResourceHandle, call)
	if err != nil || replayed.Error == nil || replayed.Error.Message != result.Error.Message || store.resourceCalls[call.RequestID] != 1 {
		t.Fatalf("post-side-effect replay=%+v error=%v calls=%d", replayed, err, store.resourceCalls[call.RequestID])
	}
}

func mustIssueCapabilityResourceHandle(t *testing.T, manager *PluginCapabilityManager, request PluginDynamicActionRequest) string {
	t.Helper()
	handle, err := manager.IssueResourceHandle(t.Context(), PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target})
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func newCapabilityManagerFixture(t *testing.T) (*PluginCapabilityManager, *capabilityManagerStore, *capabilityRuntimeStub, PluginDynamicActionRequest) {
	t.Helper()
	digest, identity := strings.Repeat("c", 64), strings.Repeat("d", 64)
	pluginID := "official.service"
	target := pluginsdk.HostTarget{Kind: "relay", ID: "relay-1", ResourceGroupID: "group-a"}
	action := pluginsdk.DynamicAction{ID: "rotate", Label: "Rotate", Capability: pluginsdk.CapabilityServiceRevocableResourceHandle, TargetKind: target.Kind}
	validated := plugins.ValidatedPackage{Digest: digest, Manifest: plugins.Manifest{ID: pluginID, Signature: plugins.Signature{Algorithm: "ed25519", KeyID: plugins.OfficialSignatureKeyID}, Permissions: []plugins.Permission{{Name: string(pluginsdk.CapabilityUIDynamicActions)}, {Name: string(pluginsdk.CapabilityServiceRevocableResourceHandle)}}}, DynamicActions: []pluginsdk.DynamicAction{action}}
	store := &capabilityManagerStore{
		installed:  storage.InstalledPluginRow{PluginID: pluginID, ActivePackageDigest: digest, ActivePackageIdentity: identity},
		packageRow: storage.PluginPackageRow{Identity: identity, Digest: digest, PluginID: pluginID},
		instance:   storage.PluginInstanceRow{ID: "instance-1", PluginID: pluginID, ResourceGroupID: target.ResourceGroupID, DesiredEnabled: true},
		grants: []storage.PluginGrantRow{
			{ID: "grant-ui", PluginID: pluginID, PackageDigest: digest, PackageIdentity: identity, Permission: string(pluginsdk.CapabilityUIDynamicActions)},
			{ID: "grant-handle", PluginID: pluginID, PackageDigest: digest, PackageIdentity: identity, Permission: string(pluginsdk.CapabilityServiceRevocableResourceHandle)},
		},
		targetVersion: "target-version-1",
		targetExists:  true,
	}
	runtime := &capabilityRuntimeStub{generation: "generation-1"}
	manager := &PluginCapabilityManager{store: store, resourceAuthorizer: capabilityTestAuthorizer{}, runtime: runtime, handles: pluginhost.NewResourceHandleBroker(), actions: pluginhost.NewDynamicActionRegistry()}
	manager.loadPackage = func(context.Context, storage.PluginPackageRow) (plugins.ValidatedPackage, error) {
		return validated, nil
	}
	actor := authz.Actor{ID: "operator", Permissions: []string{authz.PermissionResourceWrite}, VisibleResourceGroups: []string{target.ResourceGroupID}}
	return manager, store, runtime, PluginDynamicActionRequest{OperationID: "action-operation", PluginID: pluginID, InstanceID: "instance-1", ActionID: action.ID, Actor: actor, Target: target}
}

func TestPluginCapabilityManagerRejectsCallerSpoofedResourceGroupBeforeGuestDispatch(t *testing.T) {
	manager, store, runtime, request := newCapabilityManagerFixture(t)
	store.targetGroup = "group-b"
	request.Actor.VisibleResourceGroups = []string{"group-a", "group-b"}
	if _, err := manager.InvokeDynamicAction(t.Context(), request); err == nil || !strings.Contains(err.Error(), "outside the instance resource group") {
		t.Fatalf("cross-group spoof error=%v", err)
	}
	if runtime.calls != 0 {
		t.Fatalf("guest calls=%d, want zero", runtime.calls)
	}
}
