package service

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func (m *PluginCapabilityManager) policyAuthority(ctx context.Context, tx *storage.GormStore, c pluginhost.Candidate, request sdk.PolicyControlRequest, replaying bool) (sdk.PolicyControlAuthority, consumptionOwner, error) {
	id := request.InstanceID
	if id == "" {
		id = c.InstanceID
	}
	owner, err := m.consumptionOwner(ctx, tx, c, id)
	if err != nil {
		return sdk.PolicyControlAuthority{}, owner, err
	}
	if !sdk.RuntimeProjectsAgentPolicy(owner.manifest.Runtime) {
		return sdk.PolicyControlAuthority{}, owner, errPluginHostDenied
	}
	a := sdk.PolicyControlAuthority{CallerInstanceID: c.InstanceID, CallerPluginID: c.Identity.PluginID, CallerGeneration: c.Identity.Generation, CallerResourceGroupID: c.ResourceGroupID, CallerLive: true, Replaying: replaying, InstanceID: owner.instance.ID, PluginID: owner.instance.PluginID, ResourceGroupID: owner.instance.ResourceGroupID, Stage: sdk.PolicyStageIdentity{Kind: owner.manifest.Runtime.PolicyKind, PolicyID: owner.instance.ID}, Entry: request.Entry, InstanceVersion: owner.instance.StateVersion, SettingsRevision: owner.settings.Revision, Handling: owner.handling, Grants: owner.grants}
	if owner.settings.DefaultMode != "" {
		mode := sdk.PolicyMode(owner.settings.DefaultMode)
		a.DefaultMode = &mode
	}
	if request.Entry != nil {
		if !slices.Contains(owner.targets, request.Entry.NodeID) {
			return a, owner, errPluginHostDenied
		}
		catalog, err := tx.LoadAgentPluginPolicies(ctx, request.Entry.NodeID)
		if err != nil {
			return a, owner, err
		}
		original, err := policyEntryOriginal(ctx, tx, *request.Entry)
		if err != nil {
			return a, owner, err
		}
		ref, composed, err := tx.ComposeEntryPolicy(ctx, *request.Entry, original, catalog)
		if err != nil {
			return a, owner, err
		}
		var stages []storage.PolicyStage
		if composed != nil {
			stages = composed.Stages
		} else if ref != nil {
			for _, policy := range catalog {
				if policy.ID == ref.ID {
					stages = policy.Stages
				}
			}
		}
		for _, stage := range stages {
			if stage.Kind == a.Stage.Kind && stage.PolicyID == a.Stage.PolicyID && stage.InstanceID == id {
				a.EntryAuthorized = true
			}
		}
	}
	return a, owner, sdk.ValidatePolicyControlAuthority(request, a)
}

func policyEntryOriginal(ctx context.Context, tx *storage.GormStore, entry sdk.PolicyEntryTarget) (*storage.PolicyRef, error) {
	switch entry.Kind {
	case sdk.PolicyEntryHTTP:
		rows, err := tx.ListHTTPRules(ctx, entry.NodeID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if strconv.Itoa(row.ID) == entry.ID {
				return parseRulePolicyRef(row.PolicyRefJSON), nil
			}
		}
	case sdk.PolicyEntryTCP, sdk.PolicyEntryUDP:
		rows, err := tx.ListL4Rules(ctx, entry.NodeID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			kind := sdk.PolicyEntryTCP
			if row.Protocol == "udp" {
				kind = sdk.PolicyEntryUDP
			}
			if strconv.Itoa(row.ID) == entry.ID && kind == entry.Kind {
				return parseRulePolicyRef(row.PolicyRefJSON), nil
			}
		}
	case sdk.PolicyEntryManagedTCP, sdk.PolicyEntryManagedUDP:
		instance, found, err := tx.GetPluginInstance(ctx, entry.ID)
		if err != nil || !found || !instance.DesiredEnabled {
			return nil, errPluginHostDenied
		}
		var targets []string
		if json.Unmarshal([]byte(instance.TargetJSON), &targets) != nil || !slices.Contains(targets, entry.NodeID) {
			return nil, errPluginHostDenied
		}
		installed, found, err := tx.GetInstalledPlugin(ctx, instance.PluginID)
		if err != nil || !found || installed.DesiredLifecycle != "enabled" {
			return nil, errPluginHostDenied
		}
		pkg, found, err := tx.GetPluginPackageByIdentity(ctx, installed.ActivePackageIdentity)
		if err != nil || !found {
			return nil, errPluginHostDenied
		}
		var manifest sdk.Manifest
		if json.Unmarshal([]byte(pkg.ManifestJSON), &manifest) != nil || !sdk.RuntimeProjectsAgentRPC(manifest.Runtime) {
			return nil, errPluginHostDenied
		}
		declared := false
		for _, permission := range manifest.Permissions {
			if permission.Name == sdk.PermissionManagedNetworkListen {
				declared = true
			}
		}
		grants, err := tx.ListPluginGrants(ctx, instance.PluginID)
		if err != nil {
			return nil, err
		}
		managed := false
		for _, grant := range grants {
			if grant.Permission == sdk.PermissionManagedNetworkListen && grant.PackageDigest == pkg.Digest && (grant.PackageIdentity == "" || grant.PackageIdentity == pkg.Identity) {
				managed = true
			}
		}
		if !declared || !managed {
			return nil, errPluginHostDenied
		}
		chains, err := storage.CanonicalPluginPolicyChains(instance.PolicyChainsJSON)
		if err != nil || len(chains) > 1 {
			return nil, errPluginHostInvalid
		}
		if len(chains) == 1 {
			return &storage.PolicyRef{ID: chains[0]}, nil
		}
		return nil, nil
	}
	return nil, errPluginHostDenied
}

func (m *PluginCapabilityManager) managePolicyControl(ctx context.Context, c pluginhost.Candidate, call sdk.HostRuntimeCall, request sdk.PolicyControlRequest) (sdk.PolicyControlResponse, error) {
	store, ok := m.store.(*storage.GormStore)
	if !ok || m.plugins == nil {
		return sdk.PolicyControlResponse{}, errPluginHostUnavailable
	}
	id := request.InstanceID
	if id == "" {
		id = c.InstanceID
	}
	key := pluginHostOperationKey(c, request.OperationID)
	unlock := m.lockOperation("consumption:" + key)
	defer unlock()
	digest := consumptionRequestDigest(call)
	var response sdk.PolicyControlResponse
	var targets []string
	replayed := false
	err := store.SecurityTransaction(ctx, func(tx *storage.GormStore) error {
		row, found, err := tx.GetPluginConsumptionOperation(ctx, key)
		if err != nil {
			return err
		}
		if request.Action == sdk.PolicyControlInspect {
			found = false
		}
		if found && (row.Kind != call.Operation || row.RequestDigest != digest || row.ResourceGroupID != c.ResourceGroupID) {
			return errPluginHostDenied
		}
		_, owner, err := m.policyAuthority(ctx, tx, c, request, found)
		if err != nil {
			return err
		}
		if found {
			if json.Unmarshal([]byte(row.ResponseJSON), &response) != nil {
				return errPluginHostInvalid
			}
			replayed = true
			return response.ValidateFor(request)
		}
		targets = owner.targets
		if request.Entry != nil {
			targets = []string{request.Entry.NodeID}
		}
		if request.Action == sdk.PolicyControlInspect {
			response, err = m.policyResponse(ctx, tx, owner, request)
			return err
		}
		return nil
	})
	if err != nil || replayed || request.Action == sdk.PolicyControlInspect {
		return response, err
	}
	mutation := func(ctx context.Context, tx *storage.GormStore, _ map[string]int64) error {
		_, owner, err := m.policyAuthority(ctx, tx, c, request, false)
		if err != nil {
			return err
		}
		actual := owner.targets
		if request.Entry != nil {
			actual = []string{request.Entry.NodeID}
		}
		if !slices.Equal(actual, targets) {
			return storage.ErrPluginConflict
		}
		config, err := m.consumptionConfig(ctx, tx, owner, request.Config)
		if err != nil {
			return err
		}
		if request.Action == sdk.PolicyControlReplaceInstance {
			if err := m.validateDefaultModeChange(ctx, tx, owner, request.Mode); err != nil {
				return err
			}
		}
		if err := tx.UpdatePluginConsumptionInstance(ctx, id, *request.ExpectedInstanceVersion, config); err != nil {
			return err
		}
		row := owner.settings
		row.Revision++
		if request.Action == sdk.PolicyControlReplaceInstance {
			row.DefaultMode = string(request.Mode)
		} else {
			entry := request.Entry
			if err := tx.PutPluginPolicyEntryMode(ctx, storage.PluginPolicyEntryModeRow{InstanceID: id, NodeID: entry.NodeID, Kind: entry.Kind, EntryID: entry.ID, Mode: string(request.Mode)}, request.Action == sdk.PolicyControlResetEntry); err != nil {
				return err
			}
		}
		return tx.PutPluginPolicySettings(ctx, row)
	}
	beforeCommit := func(ctx context.Context, tx *storage.GormStore) error {
		owner, err := m.consumptionOwner(ctx, tx, c, id)
		if err != nil {
			return err
		}
		response, err = m.policyResponse(ctx, tx, owner, request)
		if err != nil {
			return err
		}
		data, _ := json.Marshal(response)
		encodedTargets, _ := json.Marshal(targets)
		return tx.PutPluginConsumptionOperation(ctx, storage.PluginConsumptionOperationRow{ID: key, Kind: call.Operation, RequestDigest: digest, ResponseJSON: string(data), TargetsJSON: string(encodedTargets), ResourceGroupID: c.ResourceGroupID})
	}
	if len(targets) == 0 {
		err = store.SecurityTransaction(ctx, func(tx *storage.GormStore) error {
			if err := mutation(ctx, tx, nil); err != nil {
				return err
			}
			return beforeCommit(ctx, tx)
		})
	} else {
		executor := newMutationExecutor(m.plugins.cfg, store)
		_, err = executor.Execute(ctx, revision.MutationRequest{OperationID: "consumption-" + key, Kind: "policy.control", ForceRevision: true, Request: request, Targets: configMutationTargets(m.plugins.cfg, targets, nil), ResourceState: consumptionResourceState(id), Mutate: mutation, BeforeCommit: beforeCommit})
	}
	return response, err
}

func (m *PluginCapabilityManager) policyResponse(ctx context.Context, tx *storage.GormStore, owner consumptionOwner, request sdk.PolicyControlRequest) (sdk.PolicyControlResponse, error) {
	settings, err := tx.PolicySettingsSnapshot(ctx, owner.instance, owner.handling, request.Entry)
	if err != nil {
		return sdk.PolicyControlResponse{}, err
	}
	response := sdk.PolicyControlResponse{OperationID: request.OperationID, InstanceID: owner.instance.ID, Stage: sdk.PolicyStageIdentity{Kind: owner.manifest.Runtime.PolicyKind, PolicyID: owner.instance.ID}, Entry: request.Entry, Desired: settings}
	if request.Action == sdk.PolicyControlInspect && request.Entry != nil {
		node := &sdk.PolicySettingsNodeStatus{Phase: "unavailable"}
		response.Node = node
		pointer, found, err := tx.GetAgentRevisionPointer(ctx, request.Entry.NodeID)
		if err != nil {
			return response, err
		}
		if found && pointer.AppliedRevision > 0 {
			snapshot, revision, err := tx.ImmutableAgentSnapshot(ctx, request.Entry.NodeID, pointer.AppliedRevision)
			if err != nil {
				return response, err
			}
			ref := policyEntrySnapshotRef(snapshot, *request.Entry)
			if ref != nil {
				for _, mode := range ref.StageModes {
					if mode.Stage == response.Stage {
						copy := mode.Snapshot
						node.Applied = &copy
					}
				}
				if node.Applied == nil {
					for _, policy := range snapshot.PluginPolicies {
						if policy.ID == ref.ID {
							for _, stage := range policy.Stages {
								if stage.PolicyID == response.Stage.PolicyID && stage.Kind == response.Stage.Kind && stage.PolicySettings != nil {
									copy := *stage.PolicySettings
									node.Applied = &copy
								}
							}
						}
					}
				}
			}
			if node.Applied != nil {
				node.Generation = revision.RuntimeGenerationID
				if node.Generation == "" {
					node.Generation = revision.GenerationID
				}
				node.Phase = "preparing"
				a, _ := json.Marshal(node.Applied)
				b, _ := json.Marshal(response.Desired)
				if string(a) == string(b) {
					node.Phase = "applied"
				}
			}
		}
		if found && pointer.DesiredRevision > 0 {
			desired, _, err := tx.GetCoordinatorRevision(ctx, request.Entry.NodeID, pointer.DesiredRevision)
			if err != nil {
				return response, err
			}
			if desired.State == storage.AgentRevisionStateFailed {
				node.Phase = "failed"
				node.Failure = "apply-failed"
			}
		}
	}
	return response, response.ValidateFor(request)
}

func policyEntrySnapshotRef(snapshot storage.Snapshot, entry sdk.PolicyEntryTarget) *storage.PolicyRef {
	switch entry.Kind {
	case sdk.PolicyEntryHTTP:
		for _, row := range snapshot.Rules {
			if strconv.Itoa(row.ID) == entry.ID {
				return row.PolicyRef
			}
		}
	case sdk.PolicyEntryTCP, sdk.PolicyEntryUDP:
		for _, row := range snapshot.L4Rules {
			kind := sdk.PolicyEntryTCP
			if row.Protocol == "udp" {
				kind = sdk.PolicyEntryUDP
			}
			if strconv.Itoa(row.ID) == entry.ID && entry.Kind == kind {
				return row.PolicyRef
			}
		}
	case sdk.PolicyEntryManagedTCP, sdk.PolicyEntryManagedUDP:
		for _, generation := range snapshot.PluginGenerations {
			if generation.InstanceID == entry.ID {
				protocol := "tcp"
				if entry.Kind == sdk.PolicyEntryManagedUDP {
					protocol = "udp"
				}
				if ref := generation.ManagedNetworkPolicies[protocol]; ref != nil {
					return ref
				}
				return generation.ManagedNetworkPolicy
			}
		}
	}
	return nil
}
