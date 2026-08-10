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

type capabilityManagerStore struct {
	capabilityTestStore
	mu            sync.Mutex
	installed     storage.InstalledPluginRow
	packageRow    storage.PluginPackageRow
	instance      storage.PluginInstanceRow
	grants        []storage.PluginGrantRow
	operations    map[string]storage.IdempotencyRecordRow
	targetVersion string
	targetExists  bool
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
	record := storage.IdempotencyRecordRow{Scope: scope, Key: key, RequestFingerprint: fingerprint, OperationID: operationID, ResponseJSON: `{"status":"pending","claim_token":"` + claimToken + `"}`, CreatedAt: now, ExpiresAt: expiresAt}
	store.operations[mapKey] = record
	return record, true, nil
}

func (store *capabilityManagerStore) CompletePluginCapabilityOperation(_ context.Context, scope, key, operationID, _ string, responseJSON string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	mapKey := scope + "\x00" + key
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

func (store *capabilityManagerStore) PluginCapabilityTargetVersion(context.Context, string, string) (string, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.targetVersion, store.targetExists, nil
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
}

func (runtime *capabilityRuntimeStub) ActiveGeneration(string) (string, bool) {
	return runtime.generation, runtime.generation != ""
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
	result, err = manager.InvokeDynamicAction(t.Context(), request)
	if err != nil || !result.Replayed || runtime.callCount() != 1 {
		t.Fatalf("durable replay result=%+v error=%v calls=%d", result, err, runtime.callCount())
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
