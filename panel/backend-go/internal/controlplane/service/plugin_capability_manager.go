package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

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
}

type PluginCapabilityRuntime interface {
	ActiveGeneration(string) (string, bool)
	InvokeAction(context.Context, string, string, pluginsdk.RPCActionRequest) error
}

type PluginDynamicActionRequest struct {
	PluginID   string
	InstanceID string
	ActionID   string
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
}

func NewPluginCapabilityManager(store PluginCapabilityManagerStore, resourceAuthorizer PluginCapabilityResourceAuthorizer, runtime PluginCapabilityRuntime, packages *PluginService) (*PluginCapabilityManager, error) {
	if store == nil || resourceAuthorizer == nil || runtime == nil || packages == nil {
		return nil, errors.New("plugin capability durable store, authorization owner, package validator, and runtime are required")
	}
	manager := &PluginCapabilityManager{store: store, resourceAuthorizer: resourceAuthorizer, runtime: runtime, handles: pluginhost.NewResourceHandleBroker(), actions: pluginhost.NewDynamicActionRegistry()}
	manager.loadPackage = packages.loadValidatedCapabilityPackage
	return manager, nil
}

// InvokeDynamicAction rereads every durable owner before each dispatch. The
// signed package projection and current grants are never retained as authority.
func (manager *PluginCapabilityManager) InvokeDynamicAction(ctx context.Context, request PluginDynamicActionRequest) error {
	if manager == nil || ctx == nil {
		return errors.New("plugin capability manager and context are required")
	}
	instance, ok, err := manager.store.GetPluginInstance(ctx, request.InstanceID)
	if err != nil || !ok {
		return errors.Join(errors.New("plugin capability instance is unavailable"), err)
	}
	if instance.PluginID != request.PluginID || instance.ResourceGroupID != request.Target.ResourceGroupID || !instance.DesiredEnabled {
		return errors.New("plugin capability instance is not an enabled owner of the target")
	}
	installed, ok, err := manager.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil || !ok || installed.ActivePackageIdentity == "" || installed.ActivePackageDigest == "" {
		return errors.Join(errors.New("plugin capability active package is unavailable"), err)
	}
	packageRow, ok, err := manager.store.GetPluginPackageByIdentity(ctx, installed.ActivePackageIdentity)
	if err != nil || !ok || packageRow.Digest != installed.ActivePackageDigest || packageRow.PluginID != request.PluginID {
		return errors.Join(errors.New("plugin capability package identity is stale"), err)
	}
	validated, err := manager.loadPackage(ctx, packageRow)
	if err != nil {
		return fmt.Errorf("validate plugin capability package: %w", err)
	}
	action, ok := findDynamicAction(validated.DynamicActions, request.ActionID, request.Target.Kind)
	if !ok {
		return errors.New("dynamic action is absent from the signed declarative UI")
	}
	grants, err := manager.store.ListPluginGrants(ctx, request.PluginID)
	if err != nil {
		return fmt.Errorf("load current plugin grants: %w", err)
	}
	generation, ok := manager.runtime.ActiveGeneration(request.InstanceID)
	if !ok {
		return errors.New("plugin capability runtime generation is not active")
	}
	policy, err := BuildPluginCapabilityPolicy(ctx, manager.store, manager.resourceAuthorizer, PluginCapabilityPolicyInput{Package: validated, PackageIdentity: packageRow.Identity, Instance: instance, Grants: grants, Actor: request.Actor, Generation: generation, Target: request.Target})
	if err != nil {
		return err
	}
	call := pluginsdk.HostCapabilityCall{PluginID: request.PluginID, InstanceID: request.InstanceID, Generation: generation, Actor: pluginsdk.HostActor{ID: request.Actor.ID, ResourceGroupID: instance.ResourceGroupID}, Target: request.Target, QuotaMetric: "plugin.action", QuotaUnits: 1}
	call.Capability = pluginsdk.CapabilityUIDynamicActions
	if err := policy.Authorize(ctx, call); err != nil {
		return err
	}
	call.Capability = action.Capability
	if err := policy.Authorize(ctx, call); err != nil {
		return err
	}
	if err := manager.runtime.InvokeAction(ctx, request.InstanceID, generation, pluginsdk.RPCActionRequest{Generation: generation, ActionID: action.ID, TargetKind: request.Target.Kind, TargetID: request.Target.ID}); err != nil {
		return err
	}
	// A durable post-check prevents an action response from being accepted after
	// a concurrent grant deletion or package rotation.
	return manager.verifyFence(ctx, request.PluginID, packageRow, grants, request.InstanceID, generation)
}

func (manager *PluginCapabilityManager) verifyFence(ctx context.Context, pluginID string, packageRow storage.PluginPackageRow, grants []storage.PluginGrantRow, instanceID, generation string) error {
	installed, ok, err := manager.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil || !ok || installed.ActivePackageIdentity != packageRow.Identity || installed.ActivePackageDigest != packageRow.Digest {
		return errors.Join(errors.New("plugin package rotated during capability dispatch"), err)
	}
	current, err := manager.store.ListPluginGrants(ctx, pluginID)
	if err != nil || !reflect.DeepEqual(current, grants) {
		return errors.Join(errors.New("plugin grants changed during capability dispatch"), err)
	}
	active, ok := manager.runtime.ActiveGeneration(instanceID)
	if !ok || active != generation {
		return errors.New("plugin generation drained during capability dispatch")
	}
	return nil
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
