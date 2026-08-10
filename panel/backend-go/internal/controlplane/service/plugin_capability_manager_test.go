package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type capabilityManagerStore struct {
	capabilityTestStore
	mu         sync.Mutex
	installed  storage.InstalledPluginRow
	packageRow storage.PluginPackageRow
	instance   storage.PluginInstanceRow
	grants     []storage.PluginGrantRow
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

type capabilityRuntimeStub struct {
	generation string
	calls      int
	afterCall  func()
}

func (runtime *capabilityRuntimeStub) ActiveGeneration(string) (string, bool) {
	return runtime.generation, runtime.generation != ""
}

func (runtime *capabilityRuntimeStub) InvokeAction(_ context.Context, _, generation string, request pluginsdk.RPCActionRequest) error {
	if generation != runtime.generation || request.Generation != runtime.generation {
		return errors.New("wrong runtime generation")
	}
	runtime.calls++
	if runtime.afterCall != nil {
		runtime.afterCall()
	}
	return nil
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
	store := &capabilityManagerStore{installed: storage.InstalledPluginRow{PluginID: pluginID, ActivePackageDigest: digest, ActivePackageIdentity: identity}, packageRow: storage.PluginPackageRow{Identity: identity, Digest: digest, PluginID: pluginID}, instance: storage.PluginInstanceRow{ID: "instance-1", PluginID: pluginID, ResourceGroupID: target.ResourceGroupID, DesiredEnabled: true}, grants: grants}
	runtime := &capabilityRuntimeStub{generation: "generation-1"}
	manager := &PluginCapabilityManager{store: store, resourceAuthorizer: capabilityTestAuthorizer{}, runtime: runtime}
	manager.loadPackage = func(context.Context, storage.PluginPackageRow) (plugins.ValidatedPackage, error) {
		return validated, nil
	}
	actor := authz.Actor{ID: "operator", Permissions: []string{authz.PermissionResourceWrite}, VisibleResourceGroups: []string{target.ResourceGroupID}}
	request := PluginDynamicActionRequest{PluginID: pluginID, InstanceID: "instance-1", ActionID: action.ID, Actor: actor, Target: target}
	if err := manager.InvokeDynamicAction(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 1 || store.quotaCalls != 2 || len(store.audits) != 2 {
		t.Fatalf("dispatch side effects calls=%d quota=%d audits=%d", runtime.calls, store.quotaCalls, len(store.audits))
	}

	store.mu.Lock()
	store.grants = store.grants[:1]
	store.mu.Unlock()
	if err := manager.InvokeDynamicAction(t.Context(), request); err == nil || runtime.calls != 1 {
		t.Fatalf("deleted durable grant dispatch error=%v calls=%d", err, runtime.calls)
	}

	store.mu.Lock()
	store.grants = append([]storage.PluginGrantRow(nil), grants...)
	store.mu.Unlock()
	runtime.afterCall = func() {
		store.mu.Lock()
		store.grants = store.grants[:1]
		store.mu.Unlock()
	}
	if err := manager.InvokeDynamicAction(t.Context(), request); err == nil || !strings.Contains(err.Error(), "grants changed") {
		t.Fatalf("concurrent durable grant revocation error=%v", err)
	}
}

func TestPluginCapabilityManagerFailsClosedWhenValidatedProjectionCannotBeLoaded(t *testing.T) {
	manager := &PluginCapabilityManager{store: &capabilityManagerStore{}, resourceAuthorizer: capabilityTestAuthorizer{}, runtime: &capabilityRuntimeStub{generation: "generation-1"}}
	manager.loadPackage = func(context.Context, storage.PluginPackageRow) (plugins.ValidatedPackage, error) {
		return plugins.ValidatedPackage{}, errors.New("signature invalid")
	}
	if err := manager.InvokeDynamicAction(t.Context(), PluginDynamicActionRequest{}); err == nil {
		t.Fatal("malformed durable projection was accepted")
	}
}
