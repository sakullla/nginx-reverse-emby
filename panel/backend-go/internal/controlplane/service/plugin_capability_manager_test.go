package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	goruntime "runtime"
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

func TestPluginCapabilityManagerFailsClosedWhenValidatedProjectionCannotBeLoaded(t *testing.T) {
	manager := &PluginCapabilityManager{store: &capabilityManagerStore{}, resourceAuthorizer: capabilityTestAuthorizer{}, runtime: &capabilityRuntimeStub{generation: "generation-1"}, handles: pluginhost.NewResourceHandleBroker(), actions: pluginhost.NewDynamicActionRegistry()}
	manager.loadPackage = func(context.Context, storage.PluginPackageRow) (plugins.ValidatedPackage, error) {
		return plugins.ValidatedPackage{}, errors.New("signature invalid")
	}
	if _, err := manager.InvokeDynamicAction(t.Context(), PluginDynamicActionRequest{OperationID: "action-op-invalid"}); err == nil {
		t.Fatal("malformed durable projection was accepted")
	}
}

func TestPluginCapabilityManagerConcurrentOperationReplaysOnce(t *testing.T) {
	manager, _, runtime, request := newCapabilityManagerFixture(t)
	runtime.block = true
	runtime.started = make(chan struct{}, 1)
	runtime.release = make(chan struct{})
	type response struct {
		result PluginDynamicActionResult
		err    error
	}
	responses := make(chan response, 2)
	for range 2 {
		go func() {
			result, err := manager.InvokeDynamicAction(context.Background(), request)
			responses <- response{result: result, err: err}
		}()
	}
	<-runtime.started
	close(runtime.release)
	first, second := <-responses, <-responses
	if first.err != nil || second.err != nil || runtime.callCount() != 1 || first.result.Replayed == second.result.Replayed {
		t.Fatalf("concurrent replay first=%+v second=%+v calls=%d", first, second, runtime.callCount())
	}
	manager.operationLocksMu.Lock()
	remainingLocks := len(manager.operationLocks)
	manager.operationLocksMu.Unlock()
	if remainingLocks != 0 {
		t.Fatalf("operation lock entries after completion = %d, want 0", remainingLocks)
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

func TestPluginCapabilityManagerDurableReplayPreservesRuntimeError(t *testing.T) {
	manager, _, runtime, request := newCapabilityManagerFixture(t)
	runtime.invokeErr = &pluginsdk.RuntimeError{Code: pluginsdk.ErrorUnavailable, Message: "guest unavailable", Retryable: true}
	result, err := manager.InvokeDynamicAction(t.Context(), request)
	var firstRuntimeErr *pluginsdk.RuntimeError
	if !errors.As(err, &firstRuntimeErr) || firstRuntimeErr.Code != pluginsdk.ErrorUnavailable || !firstRuntimeErr.Retryable || result.Replayed {
		t.Fatalf("initial runtime result=%+v error=%v", result, err)
	}
	result, err = manager.InvokeDynamicAction(t.Context(), request)
	var replayRuntimeErr *pluginsdk.RuntimeError
	if !errors.As(err, &replayRuntimeErr) || replayRuntimeErr.Code != pluginsdk.ErrorUnavailable || !replayRuntimeErr.Retryable || !result.Replayed || runtime.callCount() != 1 {
		t.Fatalf("replayed runtime result=%+v error=%v calls=%d", result, err, runtime.callCount())
	}
}

func TestPluginCapabilityManagerReconcilesCommittedLostResponseAndUsesOpaqueHandle(t *testing.T) {
	manager, _, runtime, request := newCapabilityManagerFixture(t)
	runtime.invokeErr = context.DeadlineExceeded
	runtime.commitErr = true
	result, err := manager.InvokeDynamicAction(t.Context(), request)
	if err != nil || result.OperationID != request.OperationID || result.Replayed {
		t.Fatalf("lost response reconciliation result=%+v error=%v", result, err)
	}
	rpcRequest := runtime.request()
	if rpcRequest.ResourceHandle == "" || rpcRequest.TargetKind != "" || rpcRequest.TargetID != "" {
		t.Fatalf("guest received raw resource identity: %+v", rpcRequest)
	}
	if len(rpcRequest.ResourceResults) != 1 || rpcRequest.ResourceResults[0].RequestID != "resource-call-1" || string(rpcRequest.ResourceResults[0].Value) != `{"available":true}` {
		t.Fatalf("guest typed resource results=%+v", rpcRequest.ResourceResults)
	}
	result, err = manager.InvokeDynamicAction(t.Context(), request)
	if err != nil || !result.Replayed || runtime.callCount() != 1 {
		t.Fatalf("durable replay result=%+v error=%v calls=%d", result, err, runtime.callCount())
	}
}

func TestPluginCapabilityManagerResourceCallOutcomesAreDurableAndErrorsReachGuest(t *testing.T) {
	manager, store, _, request := newCapabilityManagerFixture(t)
	store.resourceErrs = map[string]error{"resource-call-2": errors.New("resource request is invalid")}
	// Directly exercise the durable host boundary; InvokeDynamicAction uses this
	// method for every planned call before delivering the exact results.
	binding := storage.PluginCapabilityTargetBinding{Kind: request.Target.Kind, ID: request.Target.ID, ResourceGroupID: request.Target.ResourceGroupID, Version: store.targetVersion}
	firstCall := pluginsdk.RPCResourceCall{RequestID: "resource-call-1", ResourceHandle: "handle-1", Operation: pluginsdk.RPCResourceInspect}
	first, err := manager.executeDurableResourceCall(t.Context(), request, binding, firstCall)
	if err != nil || first.Error != nil || string(first.Value) != `{"available":true}` {
		t.Fatalf("first resource result=%+v error=%v", first, err)
	}
	replayed, err := manager.executeDurableResourceCall(t.Context(), request, binding, firstCall)
	if err != nil || replayed.Error != nil || string(replayed.Value) != string(first.Value) || store.resourceCalls[firstCall.RequestID] != 1 {
		t.Fatalf("replayed resource result=%+v error=%v calls=%d", replayed, err, store.resourceCalls[firstCall.RequestID])
	}
	secondCall := pluginsdk.RPCResourceCall{RequestID: "resource-call-2", ResourceHandle: "handle-1", Operation: pluginsdk.RPCResourceInspect}
	failed, err := manager.executeDurableResourceCall(t.Context(), request, binding, secondCall)
	if err != nil || failed.Error == nil || failed.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("failed resource result=%+v error=%v", failed, err)
	}
	failedReplay, err := manager.executeDurableResourceCall(t.Context(), request, binding, secondCall)
	if err != nil || failedReplay.Error == nil || failedReplay.Error.Code != failed.Error.Code || store.resourceCalls[secondCall.RequestID] != 1 {
		t.Fatalf("failed replay=%+v error=%v calls=%d", failedReplay, err, store.resourceCalls[secondCall.RequestID])
	}
}

func TestPluginCapabilityManagerDeliversMultiCallPartialFailureAsTypedResult(t *testing.T) {
	manager, store, runtime, request := newCapabilityManagerFixture(t)
	store.resourceErrs = map[string]error{"resource-call-2": errors.New("resource request is invalid")}
	runtime.plan = pluginsdk.RPCActionPlanResponse{Calls: []pluginsdk.RPCResourceCall{
		{RequestID: "resource-call-1", Operation: pluginsdk.RPCResourceInspect},
		{RequestID: "resource-call-2", Operation: pluginsdk.RPCResourceInspect},
	}}
	if _, err := manager.InvokeDynamicAction(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	rpcRequest := runtime.request()
	if len(rpcRequest.ResourceResults) != 2 || rpcRequest.ResourceResults[0].Error != nil || rpcRequest.ResourceResults[1].Error == nil || rpcRequest.ResourceResults[1].Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("guest resource results=%+v", rpcRequest.ResourceResults)
	}
	if store.resourceCalls["resource-call-1"] != 1 || store.resourceCalls["resource-call-2"] != 1 {
		t.Fatalf("resource call counts=%v", store.resourceCalls)
	}
}

func TestPluginCapabilityManagerRecoveredResourceClaimDoesNotRepeatCoreOperation(t *testing.T) {
	manager, store, _, request := newCapabilityManagerFixture(t)
	binding := storage.PluginCapabilityTargetBinding{Kind: request.Target.Kind, ID: request.Target.ID, ResourceGroupID: request.Target.ResourceGroupID, Version: store.targetVersion}
	call := pluginsdk.RPCResourceCall{RequestID: "resource-call-recovered", ResourceHandle: "handle-1", Operation: pluginsdk.RPCResourceInspect}
	fingerprint, err := pluginCapabilityResourceCallFingerprint(request.OperationID, binding, call)
	if err != nil {
		t.Fatal(err)
	}
	key := request.OperationID + ":" + call.RequestID
	if _, claimed, err := store.ClaimPluginCapabilityOperation(t.Context(), pluginCapabilityResourceCallScope, key, fingerprint, request.OperationID, "lost-owner", time.Now(), time.Now().Add(time.Hour)); err != nil || !claimed {
		t.Fatalf("seed lost claim claimed=%t error=%v", claimed, err)
	}
	store.mu.Lock()
	record := store.operations[pluginCapabilityResourceCallScope+"\x00"+key]
	record.ExpiresAt = time.Now().Add(-time.Second)
	store.operations[pluginCapabilityResourceCallScope+"\x00"+key] = record
	store.mu.Unlock()
	result, err := manager.executeDurableResourceCall(t.Context(), request, binding, call)
	if err != nil || result.Error == nil || result.Error.Code != pluginsdk.ErrorUnavailable || result.Error.Retryable || store.resourceCalls[call.RequestID] != 0 {
		t.Fatalf("recovered resource result=%+v error=%v calls=%d", result, err, store.resourceCalls[call.RequestID])
	}
	replayed, err := manager.executeDurableResourceCall(t.Context(), request, binding, call)
	if err != nil || replayed.Error == nil || replayed.Error.Message != result.Error.Message || store.resourceCalls[call.RequestID] != 0 {
		t.Fatalf("recovered replay=%+v error=%v calls=%d", replayed, err, store.resourceCalls[call.RequestID])
	}
}

func TestPluginCapabilityManagerCompletionFailureAfterSideEffectRecoversWithoutReexecution(t *testing.T) {
	manager, store, _, request := newCapabilityManagerFixture(t)
	binding := storage.PluginCapabilityTargetBinding{Kind: request.Target.Kind, ID: request.Target.ID, ResourceGroupID: request.Target.ResourceGroupID, Version: store.targetVersion}
	call := pluginsdk.RPCResourceCall{RequestID: "resource-call-side-effect", ResourceHandle: "handle-1", Operation: pluginsdk.RPCResourceInspect}
	key := request.OperationID + ":" + call.RequestID
	mapKey := pluginCapabilityResourceCallScope + "\x00" + key
	store.completeFails = map[string]int{mapKey: 1}
	if _, err := manager.executeDurableResourceCall(t.Context(), request, binding, call); err == nil || store.resourceCalls[call.RequestID] != 1 {
		t.Fatalf("first post-side-effect completion error=%v calls=%d", err, store.resourceCalls[call.RequestID])
	}
	store.mu.Lock()
	record := store.operations[mapKey]
	record.ExpiresAt = time.Now().Add(-time.Second)
	store.operations[mapKey] = record
	store.mu.Unlock()
	result, err := manager.executeDurableResourceCall(t.Context(), request, binding, call)
	if err != nil || result.Error == nil || result.Error.Code != pluginsdk.ErrorUnavailable || store.resourceCalls[call.RequestID] != 1 {
		t.Fatalf("recovered post-side-effect result=%+v error=%v calls=%d", result, err, store.resourceCalls[call.RequestID])
	}
	replayed, err := manager.executeDurableResourceCall(t.Context(), request, binding, call)
	if err != nil || replayed.Error == nil || replayed.Error.Message != result.Error.Message || store.resourceCalls[call.RequestID] != 1 {
		t.Fatalf("post-side-effect replay=%+v error=%v calls=%d", replayed, err, store.resourceCalls[call.RequestID])
	}
}

func TestPluginCapabilityTrafficSummaryUsesExplicitTrafficOnlyProjection(t *testing.T) {
	manager, store, _, request := newCapabilityManagerFixture(t)
	quota := int64(4096)
	remaining := int64(1024)
	manager.SetTrafficSummaryProvider(capabilityTrafficSummaryStub{summary: TrafficSummary{
		AgentID: "edge-a", CycleStart: "2026-08-01", CycleEnd: "2026-09-01", RXBytes: 100, TXBytes: 200,
		AccountedBytes: 300, UsedBytes: 3072, MonthlyQuotaBytes: &quota, QuotaPercent: 75, RemainingBytes: &remaining,
		Blocked: true, BlockReason: "private operator note", HostTotal: TrafficSummaryBreakdown{ScopeType: "host", ScopeID: "host-private", RXBytes: 999},
	}})
	binding := storage.PluginCapabilityTargetBinding{Kind: "agent", ID: "edge-a", ResourceGroupID: request.Target.ResourceGroupID, Version: store.targetVersion}
	value, err := manager.executeResourceCall(t.Context(), request, binding, pluginsdk.RPCResourceCall{RequestID: "traffic-1", ResourceHandle: "handle-1", Operation: pluginsdk.RPCResourceTrafficSummary})
	if err != nil {
		t.Fatal(err)
	}
	projection := string(value)
	for _, forbidden := range []string{"host_total", "host-private", "block_reason", "private operator note", "policy", "http_rules", "l4_rules", "relay_listeners"} {
		if strings.Contains(projection, forbidden) {
			t.Fatalf("traffic projection leaked %q: %s", forbidden, projection)
		}
	}
	for _, required := range []string{`"agent_id":"edge-a"`, `"rx_bytes":100`, `"tx_bytes":200`, `"blocked":true`} {
		if !strings.Contains(projection, required) {
			t.Fatalf("traffic projection missing %q: %s", required, projection)
		}
	}
}

func TestPluginCapabilityManagerUsesProductionOwnedDockerSocketBinding(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Unix Docker adapter is unavailable on Windows")
	}
	socketPath := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requestSeen := make(chan struct{}, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/_ping" {
			t.Errorf("Docker request=%s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		requestSeen <- struct{}{}
		writer.WriteHeader(http.StatusOK)
	})}
	go server.Serve(listener)
	defer server.Close()

	targetStore, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	defer targetStore.Close()
	if err := targetStore.EnsurePluginCapabilityDockerSocketBinding(t.Context(), "default", socketPath); err != nil {
		t.Fatal(err)
	}
	manager, baseStore, runtimeHost, request := newCapabilityManagerFixture(t)
	manager.store = &capabilityManagerTargetStore{capabilityManagerStore: baseStore, targets: targetStore}
	manager.dockerSocket = socketPath
	request.Target = pluginsdk.HostTarget{Kind: storage.PluginCapabilityDockerSocketKind, ID: storage.PluginCapabilityDockerSocketID, ResourceGroupID: "default"}
	request.Actor.VisibleResourceGroups = []string{"default"}
	baseStore.instance.ResourceGroupID = "default"
	loadPackage := manager.loadPackage
	manager.loadPackage = func(ctx context.Context, row storage.PluginPackageRow) (plugins.ValidatedPackage, error) {
		validated, loadErr := loadPackage(ctx, row)
		validated.DynamicActions[0].TargetKind = storage.PluginCapabilityDockerSocketKind
		return validated, loadErr
	}
	runtimeHost.plan = pluginsdk.RPCActionPlanResponse{Calls: []pluginsdk.RPCResourceCall{{RequestID: "docker-ping", Operation: pluginsdk.RPCResourceDockerRequest, Input: []byte(`{"action":"ping"}`)}}}
	handle, err := manager.IssueResourceHandle(t.Context(), PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target})
	if err != nil || handle == "" {
		t.Fatalf("issue production Docker handle=%q error=%v", handle, err)
	}
	manager.handles.Release(handle)
	if _, err := manager.InvokeDynamicAction(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requestSeen:
	case <-time.After(time.Second):
		t.Fatal("fixed Docker adapter did not reach the production-owned endpoint")
	}
}

func TestPluginCapabilityManagerLeaseTakeoverQueriesGuestBeforeReentry(t *testing.T) {
	first, store, runtime, request := newCapabilityManagerFixture(t)
	runtime.queryErr = errors.New("guest query transport unavailable")
	result, err := first.InvokeDynamicAction(t.Context(), request)
	if !errors.Is(err, errPluginActionPending) || result.OperationID != request.OperationID || runtime.callCount() != 0 {
		t.Fatalf("initial pending result=%+v error=%v calls=%d", result, err, runtime.callCount())
	}
	runtime.mu.Lock()
	runtime.queryErr = nil
	if runtime.results == nil {
		runtime.results = make(map[string]pluginsdk.RPCActionResponse)
	}
	runtime.results[request.OperationID] = pluginsdk.RPCActionResponse{Accepted: true, OperationID: request.OperationID}
	runtime.mu.Unlock()
	store.mu.Lock()
	record := store.operations["plugin.action\x00"+request.OperationID]
	record.ExpiresAt = time.Now().Add(-time.Second)
	store.operations["plugin.action\x00"+request.OperationID] = record
	store.mu.Unlock()
	second := &PluginCapabilityManager{store: store, resourceAuthorizer: first.resourceAuthorizer, runtime: runtime, handles: pluginhost.NewResourceHandleBroker(), actions: pluginhost.NewDynamicActionRegistry()}
	second.loadPackage = first.loadPackage
	result, err = second.InvokeDynamicAction(t.Context(), request)
	if err != nil || result.Replayed || runtime.callCount() != 0 {
		t.Fatalf("takeover reconciliation result=%+v error=%v calls=%d", result, err, runtime.callCount())
	}
	result, err = first.InvokeDynamicAction(t.Context(), request)
	if err != nil || !result.Replayed || runtime.callCount() != 0 {
		t.Fatalf("post-takeover durable replay result=%+v error=%v calls=%d", result, err, runtime.callCount())
	}
}

func TestPluginCapabilityManagerTargetRotationCancelsDispatchAndRevokesPriorHandleEpoch(t *testing.T) {
	manager, store, runtime, request := newCapabilityManagerFixture(t)
	handleRequest := PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target}
	priorToken, err := manager.IssueResourceHandle(t.Context(), handleRequest)
	if err != nil {
		t.Fatal(err)
	}
	runtime.block = true
	runtime.started = make(chan struct{}, 1)
	runtime.release = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, invokeErr := manager.InvokeDynamicAction(context.Background(), request)
		done <- invokeErr
	}()
	<-runtime.started
	store.mu.Lock()
	store.targetVersion = "target-version-2"
	store.mu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, errPluginActionPending) {
			t.Fatalf("target-rotated dispatch error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("target rotation did not cancel the guest RPC")
	}
	if _, err := manager.ResolveResourceHandle(t.Context(), priorToken, handleRequest); !errors.Is(err, pluginhost.ErrCapabilityDenied) {
		t.Fatalf("prior target handle after rotation error=%v", err)
	}
	freshToken, err := manager.IssueResourceHandle(t.Context(), handleRequest)
	if err != nil {
		t.Fatalf("issue fresh target epoch: %v", err)
	}
	if _, err := manager.ResolveResourceHandle(t.Context(), freshToken, handleRequest); err != nil {
		t.Fatalf("resolve fresh target epoch: %v", err)
	}
}

func TestPluginCapabilityManagerTargetDeletionCancelsRealRuntimeDispatch(t *testing.T) {
	manager, store, runtime, request := newCapabilityManagerFixture(t)
	runtime.block = true
	runtime.started = make(chan struct{}, 1)
	runtime.release = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, invokeErr := manager.InvokeDynamicAction(context.Background(), request)
		done <- invokeErr
	}()
	<-runtime.started
	store.mu.Lock()
	store.targetExists = false
	store.mu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, errPluginActionPending) {
			t.Fatalf("target-deleted dispatch error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("target deletion was not detected and canceled")
	}
}

func TestPluginCapabilityManagerSuccessfulHandleActionRevokesPriorTargetEpoch(t *testing.T) {
	manager, _, _, request := newCapabilityManagerFixture(t)
	handleRequest := PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target}
	priorToken, err := manager.IssueResourceHandle(t.Context(), handleRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.InvokeDynamicAction(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ResolveResourceHandle(t.Context(), priorToken, handleRequest); !errors.Is(err, pluginhost.ErrCapabilityDenied) {
		t.Fatalf("prior target handle after successful mutation error=%v", err)
	}
	if _, err := manager.IssueResourceHandle(t.Context(), handleRequest); err != nil {
		t.Fatalf("issue after successful target mutation: %v", err)
	}
}

func TestPluginCapabilityManagerResourceHandlesRereadGrantAndRevoke(t *testing.T) {
	manager, store, runtime, request := newCapabilityManagerFixture(t)
	handleRequest := PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target}
	token, err := manager.IssueResourceHandle(t.Context(), handleRequest)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manager.ResolveResourceHandle(t.Context(), token, handleRequest)
	reference, ok := resolved.(pluginResourceReference)
	if err != nil || !ok || reference.Target != request.Target || reference.Version != store.targetVersion {
		t.Fatalf("resolve resource=%v error=%v", resolved, err)
	}
	store.mu.Lock()
	store.grants = store.grants[:1]
	store.mu.Unlock()
	if _, err := manager.ResolveResourceHandle(t.Context(), token, handleRequest); !errors.Is(err, pluginhost.ErrCapabilityDenied) {
		t.Fatalf("resolve after durable grant deletion error=%v", err)
	}
	manager.RevokeGeneration(request.InstanceID, runtime.generation)
	if _, err := manager.ResolveResourceHandle(t.Context(), token, handleRequest); err == nil {
		t.Fatal("resolve after generation drain succeeded")
	}
}

func TestPluginCapabilityManagerResourceHandleTargetAndPackageRotationFailClosed(t *testing.T) {
	manager, store, _, request := newCapabilityManagerFixture(t)
	handleRequest := PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target}
	token, err := manager.IssueResourceHandle(t.Context(), handleRequest)
	if err != nil {
		t.Fatal(err)
	}
	manager.RevokeTarget(request.Target)
	if _, err := manager.ResolveResourceHandle(t.Context(), token, handleRequest); !errors.Is(err, pluginhost.ErrCapabilityDenied) {
		t.Fatalf("resolve after target deletion error=%v", err)
	}

	manager, store, _, request = newCapabilityManagerFixture(t)
	handleRequest = PluginResourceHandleRequest{PluginID: request.PluginID, InstanceID: request.InstanceID, Actor: request.Actor, Target: request.Target}
	token, err = manager.IssueResourceHandle(t.Context(), handleRequest)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.installed.ActivePackageIdentity = strings.Repeat("e", 64)
	store.installed.ActivePackageDigest = strings.Repeat("f", 64)
	store.packageRow.Identity = store.installed.ActivePackageIdentity
	store.packageRow.Digest = store.installed.ActivePackageDigest
	store.mu.Unlock()
	if _, err := manager.ResolveResourceHandle(t.Context(), token, handleRequest); !errors.Is(err, pluginhost.ErrCapabilityDenied) {
		t.Fatalf("resolve after package rotation error=%v", err)
	}
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
	if _, err := manager.InvokeDynamicAction(t.Context(), request); err == nil || !strings.Contains(err.Error(), "resource group is not authoritative") {
		t.Fatalf("cross-group spoof error=%v", err)
	}
	if runtime.calls != 0 {
		t.Fatalf("guest calls=%d, want zero", runtime.calls)
	}
}
