package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
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
	a := sdk.PolicyControlAuthority{CallerInstanceID: c.InstanceID, CallerPluginID: c.Identity.PluginID, CallerGeneration: c.Identity.Generation, CallerResourceGroupID: c.ResourceGroupID, CallerLive: true, Replaying: replaying, InstanceID: owner.instance.ID, PluginID: owner.instance.PluginID, ResourceGroupID: owner.instance.ResourceGroupID, Stage: sdk.PolicyStageIdentity{Kind: owner.manifest.Runtime.PolicyKind, PolicyID: owner.instance.ID}, InstanceVersion: owner.instance.StateVersion, SettingsRevision: owner.settings.Revision, Handling: owner.handling, Grants: owner.grants}
	if owner.settings.DefaultMode != "" {
		mode := sdk.PolicyMode(owner.settings.DefaultMode)
		a.DefaultMode = &mode
	}
	if request.Action == sdk.PolicyControlListEntries {
		entries, err := policyEntryTargets(ctx, tx, owner)
		if err != nil {
			return a, owner, err
		}
		a.Entries = entries
	} else if request.Entry != nil {
		if !slices.Contains(owner.targets, request.Entry.NodeID) {
			return a, owner, errPluginHostDenied
		}
		entry, original, err := policyEntryOriginal(ctx, tx, *request.Entry)
		if err != nil {
			return a, owner, err
		}
		a.Entry = &entry
		a.EntryAuthorized, err = policyEntryStageAuthorized(ctx, tx, owner, entry, original)
		if err != nil {
			return a, owner, err
		}
	}
	return a, owner, sdk.ValidatePolicyControlAuthority(request, a)
}

func policyEntryOriginal(ctx context.Context, tx *storage.GormStore, entry sdk.PolicyEntryTarget) (sdk.PolicyEntryTarget, *storage.PolicyRef, error) {
	switch entry.Kind {
	case sdk.PolicyEntryHTTP:
		rows, err := tx.ListHTTPRules(ctx, entry.NodeID)
		if err != nil {
			return sdk.PolicyEntryTarget{}, nil, err
		}
		for _, row := range rows {
			if strconv.Itoa(row.ID) == entry.ID {
				if row.EntryToken == "" {
					return sdk.PolicyEntryTarget{}, nil, errPluginHostDenied
				}
				return sdk.PolicyEntryTarget{NodeID: row.AgentID, Kind: sdk.PolicyEntryHTTP, ID: entry.ID, Token: row.EntryToken}, parseRulePolicyRef(row.PolicyRefJSON), nil
			}
		}
	case sdk.PolicyEntryTCP, sdk.PolicyEntryUDP:
		rows, err := tx.ListL4Rules(ctx, entry.NodeID)
		if err != nil {
			return sdk.PolicyEntryTarget{}, nil, err
		}
		for _, row := range rows {
			kind := sdk.PolicyEntryTCP
			if row.Protocol == "udp" {
				kind = sdk.PolicyEntryUDP
			}
			if strconv.Itoa(row.ID) == entry.ID && kind == entry.Kind {
				if row.EntryToken == "" {
					return sdk.PolicyEntryTarget{}, nil, errPluginHostDenied
				}
				return sdk.PolicyEntryTarget{NodeID: row.AgentID, Kind: kind, ID: entry.ID, Token: row.EntryToken}, parseRulePolicyRef(row.PolicyRefJSON), nil
			}
		}
	case sdk.PolicyEntryManagedTCP, sdk.PolicyEntryManagedUDP:
		instance, found, err := tx.GetPluginInstance(ctx, entry.ID)
		if err != nil || !found || !instance.DesiredEnabled {
			return sdk.PolicyEntryTarget{}, nil, errPluginHostDenied
		}
		var targets []string
		if json.Unmarshal([]byte(instance.TargetJSON), &targets) != nil || !slices.Contains(targets, entry.NodeID) {
			return sdk.PolicyEntryTarget{}, nil, errPluginHostDenied
		}
		installed, found, err := tx.GetInstalledPlugin(ctx, instance.PluginID)
		if err != nil || !found || installed.DesiredLifecycle != "enabled" {
			return sdk.PolicyEntryTarget{}, nil, errPluginHostDenied
		}
		pkg, found, err := tx.GetPluginPackageByIdentity(ctx, installed.ActivePackageIdentity)
		if err != nil || !found {
			return sdk.PolicyEntryTarget{}, nil, errPluginHostDenied
		}
		var manifest sdk.Manifest
		if json.Unmarshal([]byte(pkg.ManifestJSON), &manifest) != nil || !sdk.RuntimeProjectsAgentRPC(manifest.Runtime) {
			return sdk.PolicyEntryTarget{}, nil, errPluginHostDenied
		}
		declared := false
		for _, permission := range manifest.Permissions {
			if permission.Name == sdk.PermissionManagedNetworkListen {
				declared = true
			}
		}
		grants, err := tx.ListPluginGrants(ctx, instance.PluginID)
		if err != nil {
			return sdk.PolicyEntryTarget{}, nil, err
		}
		managed := false
		for _, grant := range grants {
			if grant.Permission == sdk.PermissionManagedNetworkListen && grant.PackageDigest == pkg.Digest && (grant.PackageIdentity == "" || grant.PackageIdentity == pkg.Identity) {
				managed = true
			}
		}
		if !declared || !managed {
			return sdk.PolicyEntryTarget{}, nil, errPluginHostDenied
		}
		chains, err := storage.CanonicalPluginPolicyChains(instance.PolicyChainsJSON)
		if err != nil || len(chains) > 1 {
			return sdk.PolicyEntryTarget{}, nil, errPluginHostInvalid
		}
		resolved := sdk.PolicyEntryTarget{NodeID: entry.NodeID, Kind: entry.Kind, ID: instance.ID, Token: storage.ManagedPolicyEntryToken(instance.IncarnationID, entry.NodeID, entry.Kind)}
		if len(chains) == 1 {
			return resolved, &storage.PolicyRef{ID: chains[0]}, nil
		}
		return resolved, nil, nil
	}
	return sdk.PolicyEntryTarget{}, nil, errPluginHostDenied
}

func policyEntryStageAuthorized(ctx context.Context, tx *storage.GormStore, owner consumptionOwner, entry sdk.PolicyEntryTarget, original *storage.PolicyRef) (bool, error) {
	catalog, err := tx.LoadAgentPluginPolicies(ctx, entry.NodeID)
	if err != nil {
		return false, err
	}
	ref, composed, err := tx.ComposeEntryPolicy(ctx, entry, original, catalog)
	if err != nil {
		return false, err
	}
	var stages []storage.PolicyStage
	if composed != nil {
		stages = composed.Stages
	} else if ref != nil {
		for _, policy := range catalog {
			if policy.ID == ref.ID {
				stages = policy.Stages
				break
			}
		}
	}
	for _, stage := range stages {
		if stage.Kind == owner.manifest.Runtime.PolicyKind && stage.PolicyID == owner.instance.ID && stage.InstanceID == owner.instance.ID {
			return true, nil
		}
	}
	return false, nil
}

func policyEntryTargets(ctx context.Context, tx *storage.GormStore, owner consumptionOwner) ([]sdk.PolicyEntryTarget, error) {
	candidates := make([]sdk.PolicyEntryTarget, 0)
	for _, nodeID := range owner.targets {
		httpRows, err := tx.ListHTTPRules(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		for _, row := range httpRows {
			candidates = append(candidates, sdk.PolicyEntryTarget{NodeID: nodeID, Kind: sdk.PolicyEntryHTTP, ID: strconv.Itoa(row.ID), Token: row.EntryToken})
		}
		l4Rows, err := tx.ListL4Rules(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		for _, row := range l4Rows {
			kind := sdk.PolicyEntryTCP
			if row.Protocol == "udp" {
				kind = sdk.PolicyEntryUDP
			}
			candidates = append(candidates, sdk.PolicyEntryTarget{NodeID: nodeID, Kind: kind, ID: strconv.Itoa(row.ID), Token: row.EntryToken})
		}
	}
	installed, err := tx.ListInstalledPlugins(ctx)
	if err != nil {
		return nil, err
	}
	for _, plugin := range installed {
		if plugin.DesiredLifecycle != "enabled" {
			continue
		}
		instances, err := tx.ListPluginInstances(ctx, plugin.PluginID)
		if err != nil {
			return nil, err
		}
		for _, instance := range instances {
			if !instance.DesiredEnabled || instance.ResourceGroupID != owner.instance.ResourceGroupID {
				continue
			}
			var targets []string
			if json.Unmarshal([]byte(instance.TargetJSON), &targets) != nil {
				continue
			}
			for _, nodeID := range targets {
				if !slices.Contains(owner.targets, nodeID) {
					continue
				}
				for _, kind := range []string{sdk.PolicyEntryManagedTCP, sdk.PolicyEntryManagedUDP} {
					candidates = append(candidates, sdk.PolicyEntryTarget{NodeID: nodeID, Kind: kind, ID: instance.ID, Token: storage.ManagedPolicyEntryToken(instance.IncarnationID, nodeID, kind)})
				}
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.NodeID != right.NodeID {
			return left.NodeID < right.NodeID
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.ID < right.ID
	})
	entries := make([]sdk.PolicyEntryTarget, 0, min(len(candidates), sdk.PolicyControlListMaxEntries))
	for _, candidate := range candidates {
		if len(entries) == sdk.PolicyControlListMaxEntries || candidate.Token == "" {
			continue
		}
		resolved, original, err := policyEntryOriginal(ctx, tx, candidate)
		if err != nil {
			if errors.Is(err, errPluginHostDenied) {
				continue
			}
			return nil, err
		}
		authorized, err := policyEntryStageAuthorized(ctx, tx, owner, resolved, original)
		if err != nil {
			return nil, err
		}
		if authorized {
			entries = append(entries, resolved)
		}
	}
	return entries, nil
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
		if request.Action == sdk.PolicyControlInspect || request.Action == sdk.PolicyControlListEntries {
			found = false
		}
		if found && (row.Kind != call.Operation || row.RequestDigest != digest || row.ResourceGroupID != c.ResourceGroupID) {
			return errPluginHostDenied
		}
		authority, owner, err := m.policyAuthority(ctx, tx, c, request, found)
		if err != nil {
			return err
		}
		if found {
			if json.Unmarshal([]byte(row.ResponseJSON), &response) != nil {
				return errPluginHostInvalid
			}
			replayed = true
			return sdk.ValidatePolicyControlResponseAuthority(request, response, authority)
		}
		targets = owner.targets
		if request.Entry != nil {
			targets = []string{request.Entry.NodeID}
		}
		if request.Action == sdk.PolicyControlInspect || request.Action == sdk.PolicyControlListEntries {
			response, err = m.policyResponse(ctx, tx, owner, request, authority)
			return err
		}
		return nil
	})
	if err != nil || replayed || request.Action == sdk.PolicyControlInspect || request.Action == sdk.PolicyControlListEntries {
		return response, err
	}
	mutation := func(ctx context.Context, tx *storage.GormStore, _ map[string]int64) error {
		authority, owner, err := m.policyAuthority(ctx, tx, c, request, false)
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
			entry := authority.Entry
			if err := tx.PutPluginPolicyEntryMode(ctx, storage.PluginPolicyEntryModeRow{InstanceID: id, NodeID: entry.NodeID, Kind: entry.Kind, EntryID: entry.ID, EntryToken: entry.Token, Mode: string(request.Mode), OverlayJSON: string(request.Overlay)}, request.Action == sdk.PolicyControlResetEntry); err != nil {
				return err
			}
		}
		return tx.PutPluginPolicySettings(ctx, row)
	}
	beforeCommit := func(ctx context.Context, tx *storage.GormStore) error {
		authority, owner, err := m.policyAuthority(ctx, tx, c, request, true)
		if err != nil {
			return err
		}
		response, err = m.policyResponse(ctx, tx, owner, request, authority)
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

func (m *PluginCapabilityManager) policyResponse(ctx context.Context, tx *storage.GormStore, owner consumptionOwner, request sdk.PolicyControlRequest, authority sdk.PolicyControlAuthority) (sdk.PolicyControlResponse, error) {
	settings, err := tx.PolicySettingsSnapshot(ctx, owner.instance, owner.handling, request.Entry)
	if err != nil {
		return sdk.PolicyControlResponse{}, err
	}
	response := sdk.PolicyControlResponse{OperationID: request.OperationID, InstanceID: owner.instance.ID, Stage: sdk.PolicyStageIdentity{Kind: owner.manifest.Runtime.PolicyKind, PolicyID: owner.instance.ID}, Entry: request.Entry, Desired: settings}
	if request.Action == sdk.PolicyControlListEntries {
		response.Entries = make([]sdk.PolicyEntrySnapshot, 0, len(authority.Entries))
		for _, entry := range authority.Entries {
			desired, err := tx.PolicySettingsSnapshot(ctx, owner.instance, owner.handling, &entry)
			if err != nil {
				return sdk.PolicyControlResponse{}, err
			}
			override, found, err := tx.GetPluginPolicyEntryMode(ctx, owner.instance.ID, entry)
			if err != nil {
				return sdk.PolicyControlResponse{}, err
			}
			node, err := policyEntryNodeStatus(ctx, tx, entry, response.Stage, desired)
			if err != nil {
				return sdk.PolicyControlResponse{}, err
			}
			snapshot := sdk.PolicyEntrySnapshot{Entry: entry, Desired: desired, Node: node}
			if found && override.OverlayJSON != "" {
				snapshot.Overlay = json.RawMessage(override.OverlayJSON)
			}
			response.Entries = append(response.Entries, snapshot)
		}
		return response, sdk.ValidatePolicyControlResponseAuthority(request, response, authority)
	}
	if request.Entry != nil && request.Entry.Token != "" {
		override, found, err := tx.GetPluginPolicyEntryMode(ctx, owner.instance.ID, *authority.Entry)
		if err != nil {
			return sdk.PolicyControlResponse{}, err
		}
		if found && override.OverlayJSON != "" {
			response.Overlay = json.RawMessage(override.OverlayJSON)
		}
	}
	if request.Action == sdk.PolicyControlInspect && request.Entry != nil {
		response.Node, err = policyEntryNodeStatus(ctx, tx, *authority.Entry, response.Stage, response.Desired)
		if err != nil {
			return response, err
		}
	}
	return response, sdk.ValidatePolicyControlResponseAuthority(request, response, authority)
}

func policyEntryNodeStatus(ctx context.Context, tx *storage.GormStore, entry sdk.PolicyEntryTarget, stage sdk.PolicyStageIdentity, desired sdk.PolicySettingsSnapshot) (*sdk.PolicySettingsNodeStatus, error) {
	node := &sdk.PolicySettingsNodeStatus{Phase: "unavailable"}
	pointer, found, err := tx.GetAgentRevisionPointer(ctx, entry.NodeID)
	if err != nil {
		return nil, err
	}
	currentBinding, current, err := policyEntryCurrentBinding(ctx, tx, entry, pointer)
	if err != nil {
		return nil, err
	}
	if current && found && pointer.AppliedRevision > 0 {
		snapshot, revision, err := tx.ImmutableAgentSnapshot(ctx, entry.NodeID, pointer.AppliedRevision)
		if err != nil {
			return nil, err
		}
		ref, appliedBinding := policyEntrySnapshotRef(snapshot, entry)
		if ref != nil && appliedBinding == currentBinding {
			for _, mode := range ref.StageModes {
				if mode.Stage == stage {
					copy := mode.Snapshot
					node.Applied = &copy
				}
			}
			if node.Applied == nil {
				for _, policy := range snapshot.PluginPolicies {
					if policy.ID == ref.ID {
						for _, item := range policy.Stages {
							if item.PolicyID == stage.PolicyID && item.Kind == stage.Kind && item.PolicySettings != nil {
								copy := *item.PolicySettings
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
			b, _ := json.Marshal(desired)
			if string(a) == string(b) {
				node.Phase = "applied"
			}
		}
	}
	if found && pointer.DesiredRevision > 0 {
		pending, _, err := tx.GetCoordinatorRevision(ctx, entry.NodeID, pointer.DesiredRevision)
		if err != nil {
			return nil, err
		}
		if pending.State == storage.AgentRevisionStateFailed {
			node.Phase = "failed"
			node.Failure = "apply-failed"
		}
	}
	return node, nil
}

func policyEntryCurrentBinding(ctx context.Context, tx *storage.GormStore, entry sdk.PolicyEntryTarget, pointer storage.AgentRevisionPointerRow) (string, bool, error) {
	switch entry.Kind {
	case sdk.PolicyEntryHTTP:
		id, err := strconv.Atoi(entry.ID)
		if err != nil {
			return "", false, nil
		}
		row, found, err := tx.GetHTTPRule(ctx, entry.NodeID, id)
		return "rule:" + strconv.FormatInt(int64(max(row.Revision, 0)), 10), found && row.EntryToken == entry.Token, err
	case sdk.PolicyEntryTCP, sdk.PolicyEntryUDP:
		id, err := strconv.Atoi(entry.ID)
		if err != nil {
			return "", false, nil
		}
		row, found, err := tx.GetL4Rule(ctx, entry.NodeID, id)
		return "rule:" + strconv.FormatInt(int64(max(row.Revision, 0)), 10), found && row.EntryToken == entry.Token, err
	case sdk.PolicyEntryManagedTCP, sdk.PolicyEntryManagedUDP:
		row, found, err := tx.GetPluginInstance(ctx, entry.ID)
		if err != nil || !found || storage.ManagedPolicyEntryToken(row.IncarnationID, entry.NodeID, entry.Kind) != entry.Token || pointer.DesiredRevision <= 0 {
			return "", false, err
		}
		snapshot, _, err := tx.ImmutableAgentSnapshot(ctx, entry.NodeID, pointer.DesiredRevision)
		if err != nil {
			return "", false, err
		}
		_, binding := policyEntrySnapshotRef(snapshot, entry)
		return binding, binding != "", nil
	}
	return "", false, nil
}

func policyEntrySnapshotRef(snapshot storage.Snapshot, entry sdk.PolicyEntryTarget) (*storage.PolicyRef, string) {
	switch entry.Kind {
	case sdk.PolicyEntryHTTP:
		for _, row := range snapshot.Rules {
			if strconv.Itoa(row.ID) == entry.ID {
				return row.PolicyRef, "rule:" + strconv.FormatInt(max(row.Revision, 0), 10)
			}
		}
	case sdk.PolicyEntryTCP, sdk.PolicyEntryUDP:
		for _, row := range snapshot.L4Rules {
			kind := sdk.PolicyEntryTCP
			if row.Protocol == "udp" {
				kind = sdk.PolicyEntryUDP
			}
			if strconv.Itoa(row.ID) == entry.ID && entry.Kind == kind {
				return row.PolicyRef, "rule:" + strconv.FormatInt(max(row.Revision, 0), 10)
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
					return ref, "managed:" + generation.ID
				}
				return generation.ManagedNetworkPolicy, "managed:" + generation.ID
			}
		}
	}
	return nil, ""
}
