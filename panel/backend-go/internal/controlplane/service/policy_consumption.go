package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type consumptionOwner struct {
	instance   storage.PluginInstanceRow
	manifest   sdk.Manifest
	packageRow storage.PluginPackageRow
	grants     []string
	resources  map[string][]string
	targets    []string
	handling   sdk.PolicyModeHandling
	settings   storage.PluginPolicySettingsRow
}

// The private process endpoint supplies candidate, but durable authority is
// checked again under the same transaction as every CAS and publication.
func (m *PluginCapabilityManager) consumptionOwner(ctx context.Context, tx *storage.GormStore, c pluginhost.Candidate, id string) (consumptionOwner, error) {
	var result consumptionOwner
	if ctx.Err() != nil || m.plugins == nil {
		return result, errPluginHostUnavailable
	}
	ids := []string{c.InstanceID, id}
	sort.Strings(ids)
	for _, key := range slices.Compact(ids) {
		if _, found, err := tx.GetPluginInstance(ctx, key); err != nil || !found {
			return result, errPluginHostDenied
		}
	}
	caller, _, err := tx.GetPluginInstance(ctx, c.InstanceID)
	if err != nil {
		return result, err
	}
	if caller.PluginID != c.Identity.PluginID || caller.ResourceGroupID != c.ResourceGroupID || c.IncarnationID == "" || caller.IncarnationID != c.IncarnationID {
		return result, errPluginHostDenied
	}
	runtime, found, err := tx.GetPluginRuntime(ctx, c.InstanceID)
	if err != nil || !found {
		return result, errPluginHostDenied
	}
	active := runtime.ActiveGeneration == c.Identity.Generation && runtime.ActivePackageDigest == c.Identity.PackageDigest && runtime.State != "stopped" && runtime.State != "failed"
	pending := runtime.CandidateGeneration == c.Identity.Generation && runtime.CandidatePackageDigest == c.Identity.PackageDigest && runtime.CandidateState == "starting"
	if !active && !pending {
		return result, errPluginHostDenied
	}
	instance, found, err := tx.GetPluginInstance(ctx, id)
	if err != nil || !found {
		return result, errPluginHostDenied
	}
	if instance.PluginID != caller.PluginID || instance.ResourceGroupID != caller.ResourceGroupID || !instance.DesiredEnabled || instance.ConfigVersion == 0 {
		return result, errPluginHostDenied
	}
	installed, found, err := tx.GetInstalledPlugin(ctx, instance.PluginID)
	if err != nil || !found {
		return result, errPluginHostDenied
	}
	identity := installed.ActivePackageIdentity
	if pending && installed.StagedPackageDigest == c.Identity.PackageDigest {
		identity = installed.StagedPackageIdentity
	}
	pkg, found, err := tx.GetPluginPackageByIdentity(ctx, identity)
	if err != nil || !found || pkg.PluginID != instance.PluginID || pkg.Digest != c.Identity.PackageDigest || pkg.SignatureVerdict != "verified" {
		return result, errPluginHostDenied
	}
	if json.Unmarshal([]byte(pkg.ManifestJSON), &result.manifest) != nil {
		return result, errPluginHostInvalid
	}
	result.instance, result.packageRow = instance, pkg
	scoped := *m.plugins
	scoped.store = tx
	grants, err := scoped.controlPlaneGenerationGrants(ctx, installed, pkg)
	if err != nil {
		return result, err
	}
	result.resources = map[string][]string{}
	for _, grant := range grants {
		if pluginCandidateHasGrant(c, grant.Name) && slices.Contains(c.Identity.Scopes, grant.Name) {
			result.grants = append(result.grants, grant.Name)
			resource := grant.ResourceID
			if grant.ResourceKind != "" {
				resource = grant.ResourceKind + ":" + resource
			}
			result.resources[grant.Name] = append(result.resources[grant.Name], resource)
		}
	}
	handling, err := sdk.PolicyModeHandlingForManifest(result.manifest)
	if err != nil {
		return result, err
	}
	result.handling = handling
	result.settings, err = tx.GetPluginPolicySettings(ctx, id)
	if err != nil {
		return result, err
	}
	if sdk.RuntimeProjectsControlPlaneUIAndAgentPolicy(result.manifest.Runtime) {
		agents, err := tx.ListAgents(ctx)
		if err != nil {
			return result, err
		}
		for _, agent := range agents {
			result.targets = append(result.targets, agent.ID)
		}
		if m.plugins.cfg.EnableLocalAgent {
			result.targets = append(result.targets, m.plugins.cfg.LocalAgentID)
		}
	} else if sdk.RuntimeProjectsAgentRPC(result.manifest.Runtime) {
		result.targets, err = pluginExplicitTargetIDs(json.RawMessage(instance.TargetJSON))
	} else if sdk.RuntimeProjectsAgentPolicy(result.manifest.Runtime) {
		result.targets, err = pluginTargetIDs(json.RawMessage(instance.TargetJSON), tx.LocalAgentID())
	} else {
		return result, errPluginHostDenied
	}
	if err != nil {
		return result, err
	}
	result.targets = uniqueAgentIDs(result.targets)
	return result, nil
}

func consumptionRequestDigest(call sdk.HostRuntimeCall) string {
	data, _ := json.Marshal(call)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func consumptionResourceState(instanceID string) revision.ResourceStateReader {
	return func(ctx context.Context, tx *storage.GormStore, _ revision.Target) (any, error) {
		row, found, err := tx.GetPluginInstance(ctx, instanceID)
		if err != nil || !found {
			return nil, errPluginHostDenied
		}
		settings, err := tx.GetPluginPolicySettings(ctx, instanceID)
		return struct {
			Instance storage.PluginInstanceRow
			Settings storage.PluginPolicySettingsRow
		}{row, settings}, err
	}
}

func (m *PluginCapabilityManager) consumptionConfig(ctx context.Context, tx *storage.GormStore, owner consumptionOwner, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var schema map[string]any
	if json.Unmarshal([]byte(owner.packageRow.ConfigSchemaJSON), &schema) != nil {
		return nil, errPluginHostInvalid
	}
	if err := plugins.ValidateConfigWritableInput(schema, raw); err != nil {
		return nil, errPluginHostInvalid
	}
	var handles []storage.PluginInstanceSecretHandle
	if json.Unmarshal([]byte(pluginDefaultJSONArray(owner.instance.SecretHandlesJSON)), &handles) != nil {
		return nil, errPluginHostInvalid
	}
	public, _, retained, err := pluginPrepareBrokeredConfig(schema, json.RawMessage(owner.instance.ConfigJSON), raw, handles, nil)
	if err != nil {
		return nil, errPluginHostInvalid
	}
	// Retained required writeOnly fields must participate in schema validation.
	// Materialization stays within this transaction and never enters the request,
	// operation outcome, public Config or error returned to the process.
	scoped := *m.plugins
	scoped.store = tx
	if len(retained) > 0 {
		if scoped.secretVault == nil {
			return nil, errPluginHostUnavailable
		}
		scoped.secretVault, err = scoped.secretVault.WithStore(tx)
		if err != nil {
			return nil, errPluginHostUnavailable
		}
	}
	encodedHandles, _ := json.Marshal(retained)
	materialized, _, err := scoped.materializeStoredPluginConfig(ctx, schema, string(public), string(encodedHandles), owner.instance.ResourceGroupID, "plugin/"+owner.instance.PluginID, "policy-consumption")
	clear(materialized)
	if err != nil {
		return nil, errPluginHostInvalid
	}
	return public, nil
}

func (m *PluginCapabilityManager) dispatchPolicyConsumption(ctx context.Context, c pluginhost.Candidate, call sdk.HostRuntimeCall) sdk.HostRuntimeResponse {
	var payload any
	var err error
	switch call.Operation {
	case sdk.HostRuntimeDatasetBinding:
		var request sdk.DatasetBindingRequest
		request, err = sdk.DecodeDatasetBindingRequest(call.Payload)
		if err == nil && call.OperationID != request.OperationID {
			err = errPluginHostInvalid
		}
		if err == nil {
			payload, err = m.manageDatasetBinding(ctx, c, call, request)
		}
	case sdk.HostRuntimePolicyControl:
		var request sdk.PolicyControlRequest
		request, err = sdk.DecodePolicyControlRequest(call.Payload)
		if err == nil && call.OperationID != request.OperationID {
			err = errPluginHostInvalid
		}
		if err == nil {
			payload, err = m.managePolicyControl(ctx, c, call, request)
		}
	}
	if err != nil {
		code := sdk.ErrorInvalidArgument
		if errors.Is(err, errPluginHostDenied) {
			code = sdk.ErrorPermissionDenied
		} else if errors.Is(err, errPluginHostUnavailable) {
			code = sdk.ErrorUnavailable
		}
		return pluginHostRuntimeFailure(code, "policy consumption operation failed", code == sdk.ErrorUnavailable)
	}
	data, err := json.Marshal(payload)
	if err != nil || len(data) > sdk.PluginHostPayloadMaxBytes {
		return pluginHostRuntimeFailure(sdk.ErrorInternal, "policy consumption response is invalid", false)
	}
	return sdk.HostRuntimeResponse{Payload: data}
}

func scopeContains(scopes []string, id string) bool {
	for _, scope := range scopes {
		if scope == "" || scope == id {
			return true
		}
	}
	return false
}

func (m *PluginCapabilityManager) bindingAuthority(ctx context.Context, tx *storage.GormStore, c pluginhost.Candidate, request sdk.DatasetBindingRequest, replay *storage.PluginConsumptionOperationRow) (sdk.DatasetBindingAuthorization, consumptionOwner, error) {
	owner, err := m.consumptionOwner(ctx, tx, c, request.InstanceID)
	if err != nil {
		return sdk.DatasetBindingAuthorization{}, owner, err
	}
	a := sdk.DatasetBindingAuthorization{CallerPluginID: c.Identity.PluginID, CallerInstanceID: c.InstanceID, CallerGeneration: c.Identity.Generation, ResourceGroupID: c.ResourceGroupID, TargetPluginID: owner.instance.PluginID, TargetInstanceID: owner.instance.ID, TargetResourceGroupID: owner.instance.ResourceGroupID, DeclaredScopes: c.Identity.Scopes, GrantedScopes: owner.grants, EffectiveAgentIDs: owner.targets, GrantedAgentIDs: append([]string(nil), owner.targets...), InstanceRevision: owner.instance.StateVersion, PolicyRevision: owner.settings.Revision, PolicyModeHandling: owner.handling}
	if sdk.RuntimeProjectsAgentPolicy(owner.manifest.Runtime) {
		a.PolicyStage = &sdk.PolicyStageIdentity{Kind: owner.manifest.Runtime.PolicyKind, PolicyID: owner.instance.ID}
	}
	source, sourceErr := tx.GetDatasetSource(ctx, request.SourceID)
	if sourceErr != nil && (replay == nil || !errors.Is(sourceErr, storage.ErrDatasetNotFound)) {
		return a, owner, errPluginHostDenied
	}
	if sourceErr == nil && source.ResourceGroupID != c.ResourceGroupID {
		return a, owner, errPluginHostDenied
	}
	if replay != nil && replay.ResourceGroupID != c.ResourceGroupID {
		return a, owner, errPluginHostDenied
	}
	if scopeContains(owner.resources[string(sdk.CapabilityDatasetBind)], request.SourceID) && (len(c.GrantSelectors[string(sdk.CapabilityDatasetBind)]) == 0 || scopeContains(c.GrantSelectors[string(sdk.CapabilityDatasetBind)], request.SourceID)) {
		a.SourceIDs = []string{request.SourceID}
	}
	row, err := tx.GetPluginDatasetConsumption(ctx, request.InstanceID, request.SourceID)
	if err != nil {
		return a, owner, err
	}
	a.Revision = row.Revision
	if row.RecordJSON != "" {
		var record sdk.DatasetBindingRecord
		if json.Unmarshal([]byte(row.RecordJSON), &record) != nil {
			return a, owner, errPluginHostInvalid
		}
		a.Current = &record
	}
	bindings, err := tx.ListInstanceDatasetBindings(ctx, request.InstanceID, request.SourceID)
	if err != nil {
		return a, owner, err
	}
	for _, binding := range bindings {
		a.BoundAgentIDs = append(a.BoundAgentIDs, binding.AgentID)
	}
	// A target can have acquired this logical binding through an ordinary
	// instance target update, without a physical row. Keep actual old deliveries
	// visible during removal until the coordinator acknowledges their absence.
	nodes, err := tx.ListAgents(ctx)
	if err != nil {
		return a, owner, err
	}
	known := []string{tx.LocalAgentID()}
	for _, node := range nodes {
		known = append(known, node.ID)
	}
	for _, agent := range uniqueAgentIDs(known) {
		pointer, found, err := tx.GetAgentRevisionPointer(ctx, agent)
		if err != nil {
			return a, owner, err
		}
		if !found {
			continue
		}
		for _, revision := range []int64{pointer.AppliedRevision, pointer.LastKnownGoodRevision} {
			if revision <= 0 {
				continue
			}
			snapshot, _, err := tx.ImmutableAgentSnapshot(ctx, agent, revision)
			if err != nil {
				return a, owner, err
			}
			if datasetSpecFromSnapshot(snapshot, request.InstanceID, request.SourceID) != nil {
				a.BoundAgentIDs = append(a.BoundAgentIDs, agent)
				break
			}
		}
	}
	a.BoundAgentIDs = uniqueAgentIDs(a.BoundAgentIDs)
	a.GrantedAgentIDs = uniqueAgentIDs(append(a.GrantedAgentIDs, a.BoundAgentIDs...))
	if replay != nil {
		var response sdk.DatasetBindingResponse
		var targets []string
		if json.Unmarshal([]byte(replay.ResponseJSON), &response) != nil || json.Unmarshal([]byte(replay.TargetsJSON), &targets) != nil {
			return a, owner, errPluginHostInvalid
		}
		a.GrantedAgentIDs = uniqueAgentIDs(append(a.GrantedAgentIDs, targets...))
		digest, _ := sdk.DatasetBindingRequestDigest(request)
		a.Replay = &sdk.DatasetBindingReplay{RequestDigest: digest, ResolvedAgentIDs: targets, Response: response}
	} else if request.Spec != nil {
		if m.datasets == nil {
			return a, owner, errPluginHostUnavailable
		}
		scoped := *m.datasets
		scoped.store = tx
		index, err := scoped.loadIndex(ctx, request.SourceID, request.Spec.VersionDigest)
		if err != nil {
			return a, owner, err
		}
		if err := validateDatasetBoundClasses(ctx, index, request.Spec.Classifications); err != nil {
			return a, owner, err
		}
		version := index.Version()
		a.Version = &version
		// The full catalog can contain thousands of classes. Verify it via the
		// paginated immutable index above, then pass only required name/kind facts.
		for _, class := range request.Spec.Classifications {
			a.Catalog = append(a.Catalog, sdk.DatasetClassification{Name: class.Name, Kind: class.Kind})
		}
	}
	return a, owner, nil
}

func (m *PluginCapabilityManager) manageDatasetBinding(ctx context.Context, c pluginhost.Candidate, call sdk.HostRuntimeCall, request sdk.DatasetBindingRequest) (sdk.DatasetBindingResponse, error) {
	store, ok := m.store.(*storage.GormStore)
	if !ok || m.datasets == nil {
		return sdk.DatasetBindingResponse{}, errPluginHostUnavailable
	}
	key := pluginHostOperationKey(c, request.OperationID)
	unlock := m.lockOperation("consumption:" + key)
	defer unlock()
	digest := consumptionRequestDigest(call)
	var response sdk.DatasetBindingResponse
	var selected, affected []string
	var replayed bool
	affectedAgents := func(selected []string, a sdk.DatasetBindingAuthorization) []string {
		agents := append(append([]string(nil), selected...), a.BoundAgentIDs...)
		// Config and policy defaults belong to the whole instance, even when
		// the dataset selection only covers a subset of its execution targets.
		if request.InstanceUpdate != nil {
			agents = append(agents, a.EffectiveAgentIDs...)
		}
		return uniqueAgentIDs(agents)
	}
	err := store.SecurityTransaction(ctx, func(tx *storage.GormStore) error {
		var replay *storage.PluginConsumptionOperationRow
		if request.Action != sdk.DatasetBindingInspect {
			row, found, err := tx.GetPluginConsumptionOperation(ctx, key)
			if err != nil {
				return err
			}
			if found {
				if row.Kind != call.Operation || row.RequestDigest != digest {
					return errPluginHostDenied
				}
				replay = &row
			}
		}
		a, _, err := m.bindingAuthority(ctx, tx, c, request, replay)
		if err != nil {
			return err
		}
		selected, err = sdk.ValidateDatasetBindingAuthorization(request, a)
		if err != nil {
			return errPluginHostDenied
		}
		if replay != nil {
			response = a.Replay.Response
			replayed = true
			return response.ValidateFor(request)
		}
		affected = affectedAgents(selected, a)
		if request.Action == sdk.DatasetBindingInspect {
			response, err = m.bindingResponse(ctx, tx, request, affected)
			return err
		}
		return nil
	})
	if err != nil || replayed || request.Action == sdk.DatasetBindingInspect {
		return response, err
	}
	mutation := func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
		a, owner, err := m.bindingAuthority(ctx, tx, c, request, nil)
		if err != nil {
			return err
		}
		agents, err := sdk.ValidateDatasetBindingAuthorization(request, a)
		if err != nil {
			return errPluginHostDenied
		}
		if !slices.Equal(agents, selected) || !slices.Equal(affectedAgents(agents, a), affected) {
			return storage.ErrPluginConflict
		}
		if request.Action == sdk.DatasetBindingBind {
			if a.Current != nil {
				return storage.ErrPluginConflict
			}
		} else if a.Current == nil || a.Revision != request.ExpectedRevision {
			return storage.ErrPluginConflict
		}
		var config json.RawMessage
		if update := request.InstanceUpdate; update != nil {
			if owner.instance.StateVersion != update.ExpectedRevision {
				return storage.ErrPluginConflict
			}
			if update.PolicyDefaults != nil {
				if a.PolicyStage == nil || sdk.ValidatePolicyDefaultSettingsUpdate(*update.PolicyDefaults, *a.PolicyStage, a.PolicyModeHandling, a.PolicyRevision) != nil {
					return storage.ErrPluginConflict
				}
				if err := m.validateDefaultModeChange(ctx, tx, owner, update.PolicyDefaults.Mode); err != nil {
					return err
				}
			}
			config, err = m.consumptionConfig(ctx, tx, owner, update.Config)
			if err != nil {
				return err
			}
		}
		// Every comparison/validation above precedes all bundle writes.
		if update := request.InstanceUpdate; update != nil {
			if err := tx.UpdatePluginConsumptionInstance(ctx, owner.instance.ID, update.ExpectedRevision, config); err != nil {
				return err
			}
			if update.PolicyDefaults != nil {
				if err := tx.PutPluginPolicySettings(ctx, storage.PluginPolicySettingsRow{InstanceID: owner.instance.ID, Revision: owner.settings.Revision + 1, DefaultMode: string(update.PolicyDefaults.Mode)}); err != nil {
					return err
				}
			}
		}
		next := storage.PluginDatasetConsumptionRow{InstanceID: request.InstanceID, SourceID: request.SourceID, Revision: a.Revision + 1}
		if request.Action != sdk.DatasetBindingUnbind {
			record := sdk.DatasetBindingRecord{InstanceID: request.InstanceID, SourceID: request.SourceID, Revision: next.Revision, Targets: request.Targets, Spec: *request.Spec}
			encoded, _ := json.Marshal(record)
			next.RecordJSON = string(encoded)
		}
		if err := tx.PutPluginDatasetConsumption(ctx, next); err != nil {
			return err
		}
		for _, agent := range affected {
			if request.Action == sdk.DatasetBindingUnbind || !slices.Contains(selected, agent) {
				if err := tx.RemoveDatasetBinding(ctx, request.SourceID, agent, request.InstanceID); err != nil {
					return err
				}
				continue
			}
			classes, _ := json.Marshal(request.Spec.Classifications)
			if err := tx.PutDatasetBinding(ctx, storage.DatasetBindingRow{AgentID: agent, InstanceID: request.InstanceID, SourceID: request.SourceID, VersionDigest: request.Spec.VersionDigest, ClassificationsJSON: string(classes), Revision: revisions[agent]}); err != nil {
				return err
			}
		}
		return nil
	}
	beforeCommit := func(ctx context.Context, tx *storage.GormStore) error {
		var err error
		response, err = m.bindingResponse(ctx, tx, request, affected)
		if err != nil {
			return err
		}
		data, _ := json.Marshal(response)
		targets, _ := json.Marshal(selected)
		return tx.PutPluginConsumptionOperation(ctx, storage.PluginConsumptionOperationRow{ID: key, Kind: call.Operation, RequestDigest: digest, ResponseJSON: string(data), TargetsJSON: string(targets), ResourceGroupID: c.ResourceGroupID})
	}
	if len(affected) == 0 {
		err = store.SecurityTransaction(ctx, func(tx *storage.GormStore) error {
			if err := mutation(ctx, tx, map[string]int64{}); err != nil {
				return err
			}
			return beforeCommit(ctx, tx)
		})
	} else {
		_, err = m.datasets.executor.Execute(ctx, revision.MutationRequest{OperationID: "consumption-" + key, Kind: "dataset.binding", ForceRevision: true, Request: request, Targets: configMutationTargets(m.datasets.cfg, affected, nil), ResourceState: consumptionResourceState(request.InstanceID), Mutate: mutation, BeforeCommit: beforeCommit})
	}
	return response, err
}

func (m *PluginCapabilityManager) bindingResponse(ctx context.Context, tx *storage.GormStore, request sdk.DatasetBindingRequest, agents []string) (sdk.DatasetBindingResponse, error) {
	row, err := tx.GetPluginDatasetConsumption(ctx, request.InstanceID, request.SourceID)
	if err != nil {
		return sdk.DatasetBindingResponse{}, err
	}
	response := sdk.DatasetBindingResponse{OperationID: request.OperationID, InstanceID: request.InstanceID, SourceID: request.SourceID, Revision: row.Revision, Targets: []sdk.DatasetBindingTargetStatus{}}
	if row.RecordJSON != "" {
		var record sdk.DatasetBindingRecord
		if json.Unmarshal([]byte(row.RecordJSON), &record) != nil {
			return response, errPluginHostInvalid
		}
		response.Desired = &record
	}
	if request.InstanceUpdate != nil {
		instance, _, err := tx.GetPluginInstance(ctx, request.InstanceID)
		if err != nil {
			return response, err
		}
		response.InstanceRevision = instance.StateVersion
		if request.InstanceUpdate.PolicyDefaults != nil {
			settings, err := tx.GetPluginPolicySettings(ctx, request.InstanceID)
			if err != nil {
				return response, err
			}
			response.PolicyRevision = settings.Revision
		}
	}
	for _, agent := range agents {
		status := sdk.DatasetBindingTargetStatus{AgentID: agent, State: "pending"}
		bindings, err := tx.ResolveDatasetBindings(ctx, agent)
		if err != nil {
			return response, err
		}
		for _, binding := range bindings {
			if binding.InstanceID == request.InstanceID && binding.SourceID == request.SourceID {
				var classes []sdk.DatasetClassification
				if json.Unmarshal([]byte(binding.ClassificationsJSON), &classes) != nil {
					return response, errPluginHostInvalid
				}
				status.Desired = &sdk.DatasetBindingSpec{VersionDigest: binding.VersionDigest, Classifications: classes}
			}
		}
		pointer, found, err := tx.GetAgentRevisionPointer(ctx, agent)
		if err != nil {
			return response, err
		}
		if found && pointer.AppliedRevision > 0 {
			snapshot, revision, err := tx.ImmutableAgentSnapshot(ctx, agent, pointer.AppliedRevision)
			if err != nil {
				return response, err
			}
			status.Applied = datasetSpecFromSnapshot(snapshot, request.InstanceID, request.SourceID)
			if status.Applied != nil {
				status.Generation = revision.RuntimeGenerationID
				if status.Generation == "" {
					status.Generation = revision.GenerationID
				}
				status.ConfigRevision = uint64(pointer.AppliedRevision)
			}
		}
		if found && pointer.LastKnownGoodRevision > 0 {
			snapshot, _, err := tx.ImmutableAgentSnapshot(ctx, agent, pointer.LastKnownGoodRevision)
			if err != nil {
				return response, err
			}
			status.LastGood = datasetSpecFromSnapshot(snapshot, request.InstanceID, request.SourceID)
		}
		if status.Desired == nil && status.Applied == nil {
			status.State = "unbound"
		} else if status.Desired != nil && status.Applied != nil && sameDatasetBindingSpec(*status.Desired, *status.Applied) {
			status.State = "applied"
		}
		if found && pointer.DesiredRevision > 0 {
			desired, _, err := tx.GetCoordinatorRevision(ctx, agent, pointer.DesiredRevision)
			if err != nil {
				return response, err
			}
			if desired.State == storage.AgentRevisionStateFailed {
				status.State = "failed"
				status.Error = &sdk.RuntimeError{Code: sdk.ErrorUnavailable, Message: "dataset candidate application failed", Retryable: true}
			}
		}
		if agent != tx.LocalAgentID() {
			nodes, err := tx.ListAgents(ctx)
			if err != nil {
				return response, err
			}
			online := false
			for _, node := range nodes {
				if node.ID == agent {
					seen, err := time.Parse(time.RFC3339Nano, node.LastSeenAt)
					online = err == nil && time.Since(seen) < 2*time.Minute
				}
			}
			if !online {
				status.State = "offline"
			}
		}
		response.Targets = append(response.Targets, status)
	}
	return response, response.ValidateFor(request)
}
func datasetSpecFromSnapshot(snapshot storage.Snapshot, instance, source string) *sdk.DatasetBindingSpec {
	for _, dataset := range snapshot.Datasets {
		if dataset.Version.SourceID == source {
			for _, binding := range dataset.Bindings {
				if binding.InstanceID == instance {
					return &sdk.DatasetBindingSpec{VersionDigest: dataset.Version.Digest, Classifications: append([]sdk.DatasetClassification(nil), binding.Classifications...)}
				}
			}
		}
	}
	return nil
}
func sameDatasetBindingSpec(a, b sdk.DatasetBindingSpec) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func (m *PluginCapabilityManager) validateDefaultModeChange(ctx context.Context, tx *storage.GormStore, owner consumptionOwner, mode sdk.PolicyMode) error {
	rows, err := tx.ListPluginPolicyEntryModes(ctx, owner.instance.ID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		entry := sdk.PolicyMode(row.Mode)
		if err := (sdk.PolicyModeSettings{Handling: owner.handling, DefaultMode: &mode, EntryMode: &entry}).Validate(); err != nil {
			return errPluginHostInvalid
		}
	}
	return nil
}
