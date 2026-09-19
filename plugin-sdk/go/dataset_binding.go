package pluginsdk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"
)

const (
	HostRuntimeDatasetBinding                      = "dataset.binding"
	CapabilityDatasetBind           HostCapability = "dataset.bind"
	RPCFeatureDatasetBindingsV1                    = "rpc.dataset-bindings.v1"
	DatasetBindingMaxFrameBytes                    = 64 << 10
	DatasetBindingMaxTargetStatuses                = ExecutionTargetMaxAgents * 2
	DatasetBindingMaxDuration                      = 30 * time.Second
	DatasetBindingBind                             = "bind"
	DatasetBindingReplace                          = "replace"
	DatasetBindingUnbind                           = "unbind"
	DatasetBindingInspect                          = "inspect"
)

type DatasetBindingSpec struct {
	VersionDigest   string                  `json:"version_digest"`
	Classifications []DatasetClassification `json:"classifications"`
}

func (spec DatasetBindingSpec) Validate() error {
	if validateDatasetDigest(spec.VersionDigest) != nil || len(spec.Classifications) == 0 || len(spec.Classifications) > DatasetMaxQueryClassifications {
		return errors.New("dataset binding version or classifications invalid")
	}
	seen := map[string]bool{}
	for _, classification := range spec.Classifications {
		if err := classification.Validate(); err != nil {
			return err
		}
		key := datasetClassificationKey(classification)
		if seen[key] {
			return errors.New("duplicate binding classification")
		}
		seen[key] = true
	}
	return nil
}

func equalDatasetBindingSpecs(a, b DatasetBindingSpec) bool {
	if a.VersionDigest != b.VersionDigest || len(a.Classifications) != len(b.Classifications) {
		return false
	}
	keys := map[string]bool{}
	for _, value := range a.Classifications {
		keys[datasetClassificationKey(value)] = true
	}
	for _, value := range b.Classifications {
		if !keys[datasetClassificationKey(value)] {
			return false
		}
	}
	return true
}
func equalExecutionSelections(a, b ExecutionTargetSelection) bool {
	return a.Mode == b.Mode && slices.Equal(a.AgentIDs, b.AgentIDs)
}

// DatasetBindingInstanceUpdate is committed with the binding in one transaction
// and one published Agent snapshot. ExpectedRevision is the shared instance
// version; PolicyDefaults.ExpectedRevision is the separate policy-settings CAS.
// Hosts must check every comparison before writing any part of this bundle.
type DatasetBindingInstanceUpdate struct {
	ExpectedRevision uint64                       `json:"expected_revision"`
	Config           json.RawMessage              `json:"config,omitempty"`
	PolicyDefaults   *PolicyDefaultSettingsUpdate `json:"policy_defaults,omitempty"`
}

func (update DatasetBindingInstanceUpdate) Validate() error {
	if update.ExpectedRevision == 0 || update.ExpectedRevision == math.MaxUint64 || (len(update.Config) == 0 && update.PolicyDefaults == nil) {
		return errors.New("atomic instance update requires a version and changes")
	}
	if len(update.Config) > 0 {
		if validatePolicyOverlayJSON(update.Config, DatasetBindingMaxFrameBytes) != nil || !policyOverlayJSONObject(update.Config) {
			return errors.New("instance config must be a bounded opaque object")
		}
	}
	if update.PolicyDefaults != nil {
		return update.PolicyDefaults.Validate()
	}
	return nil
}

// DatasetBindingRequest manages one source consumption record for an explicitly
// selected execution instance. Caller identity is authenticated separately.
// Bind requires absence, replace/unbind compare the current binding revision.
// A repeated operation ID must replay its original acknowledgement, including
// when that acknowledgement is still pending; inspect obtains fresh status.
type DatasetBindingRequest struct {
	Action           string                        `json:"action"`
	OperationID      string                        `json:"operation_id,omitempty"`
	InstanceID       string                        `json:"instance_id"`
	SourceID         string                        `json:"source_id"`
	Targets          ExecutionTargetSelection      `json:"targets"`
	Spec             *DatasetBindingSpec           `json:"spec,omitempty"`
	ExpectedRevision uint64                        `json:"expected_revision,omitempty"`
	InstanceUpdate   *DatasetBindingInstanceUpdate `json:"instance_update,omitempty"`
}

func (request DatasetBindingRequest) Validate() error {
	if request.ExpectedRevision == math.MaxUint64 {
		return errors.New("binding revision exhausted")
	}
	if ValidatePolicyIdentity(request.InstanceID) != nil || ValidatePolicyIdentity(request.SourceID) != nil {
		return errors.New("dataset binding instance/source is invalid")
	}
	if err := request.Targets.Validate(); err != nil {
		return err
	}
	if request.Action == DatasetBindingInspect {
		if request.OperationID != "" || request.ExpectedRevision != 0 || request.Spec != nil || request.InstanceUpdate != nil {
			return errors.New("inspect has mutation fields")
		}
		return nil
	}
	if request.InstanceUpdate != nil {
		if err := request.InstanceUpdate.Validate(); err != nil {
			return err
		}
	}
	if ValidatePolicyIdentity(request.OperationID) != nil {
		return errors.New("binding mutation requires operation ID")
	}
	switch request.Action {
	case DatasetBindingBind:
		if request.ExpectedRevision != 0 {
			return errors.New("bind requires absent revision")
		}
	case DatasetBindingReplace, DatasetBindingUnbind:
		if request.ExpectedRevision == 0 {
			return errors.New("mutation requires expected binding revision")
		}
	default:
		return errors.New("unsupported binding action")
	}
	if request.Action == DatasetBindingUnbind {
		if request.Spec != nil {
			return errors.New("unbind must not include dataset spec")
		}
	} else if request.Spec == nil {
		return errors.New("binding spec missing")
	} else if err := request.Spec.Validate(); err != nil {
		return err
	}
	return nil
}

type DatasetBindingRecord struct {
	InstanceID string                   `json:"instance_id"`
	SourceID   string                   `json:"source_id"`
	Revision   uint64                   `json:"revision"`
	Targets    ExecutionTargetSelection `json:"targets"`
	Spec       DatasetBindingSpec       `json:"spec"`
}

func (record DatasetBindingRecord) Validate() error {
	if ValidatePolicyIdentity(record.InstanceID) != nil || ValidatePolicyIdentity(record.SourceID) != nil || record.Revision == 0 {
		return errors.New("invalid binding record")
	}
	if err := record.Targets.Validate(); err != nil {
		return err
	}
	return record.Spec.Validate()
}

// Desired is the actual node configuration; Applied/LastGood are reported
// versions, never guesses from desired intent. Generation is the actual runtime
// generation and ConfigRevision the actual applied immutable Agent revision.
// Pending removal has Desired=nil and may retain Applied until removal ACK.
type DatasetBindingTargetStatus struct {
	AgentID        string              `json:"agent_id"`
	State          string              `json:"state"`
	Desired        *DatasetBindingSpec `json:"desired,omitempty"`
	Applied        *DatasetBindingSpec `json:"applied,omitempty"`
	LastGood       *DatasetBindingSpec `json:"last_good,omitempty"`
	Generation     string              `json:"generation,omitempty"`
	ConfigRevision uint64              `json:"config_revision,omitempty"`
	Error          *RuntimeError       `json:"error,omitempty"`
}

func (status DatasetBindingTargetStatus) Validate() error {
	if ValidatePolicyIdentity(status.AgentID) != nil {
		return errors.New("invalid binding status agent")
	}
	for _, spec := range []*DatasetBindingSpec{status.Desired, status.Applied, status.LastGood} {
		if spec != nil {
			if err := spec.Validate(); err != nil {
				return err
			}
		}
	}
	if status.Generation != "" && ValidatePolicyIdentity(status.Generation) != nil {
		return errors.New("invalid applied generation")
	}
	if status.Applied != nil && (status.Generation == "" || status.ConfigRevision == 0) {
		return errors.New("applied data requires actual generation/revision")
	}
	if status.Error != nil {
		if err := status.Error.Validate(); err != nil {
			return err
		}
	}
	switch status.State {
	case "applied":
		if status.Desired == nil || status.Applied == nil || !equalDatasetBindingSpecs(*status.Desired, *status.Applied) || status.Error != nil {
			return errors.New("applied binding differs from desired")
		}
	case "unbound":
		if status.Desired != nil || status.Applied != nil || status.Error != nil {
			return errors.New("unbound binding retains active data")
		}
	case "pending":
		if status.Error != nil {
			return errors.New("pending must not hide a failed preparation")
		}
	case "failed":
		if status.Error == nil {
			return errors.New("failed binding requires a structured error")
		}
	case "offline":
	default:
		return errors.New("invalid binding target state")
	}
	return nil
}

type DatasetBindingResponse struct {
	InstanceRevision uint64                       `json:"instance_revision,omitempty"`
	PolicyRevision   uint64                       `json:"policy_revision,omitempty"`
	OperationID      string                       `json:"operation_id,omitempty"`
	InstanceID       string                       `json:"instance_id"`
	SourceID         string                       `json:"source_id"`
	Revision         uint64                       `json:"revision"`
	Desired          *DatasetBindingRecord        `json:"desired,omitempty"`
	Targets          []DatasetBindingTargetStatus `json:"targets"`
}

func (response DatasetBindingResponse) ValidateFor(request DatasetBindingRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.InstanceUpdate != nil {
		if response.InstanceRevision <= request.InstanceUpdate.ExpectedRevision {
			return errors.New("instance update acknowledgement did not advance version")
		}
		if request.InstanceUpdate.PolicyDefaults == nil && response.PolicyRevision != 0 {
			return errors.New("Config-only update changed policy settings revision")
		}
		if request.InstanceUpdate.PolicyDefaults != nil && response.PolicyRevision <= request.InstanceUpdate.PolicyDefaults.ExpectedRevision {
			return errors.New("policy defaults acknowledgement did not advance settings revision")
		}
	} else if response.InstanceRevision != 0 || response.PolicyRevision != 0 {
		return errors.New("binding response has unrelated instance mutation versions")
	}
	if response.OperationID != request.OperationID || response.InstanceID != request.InstanceID || response.SourceID != request.SourceID || response.Targets == nil || len(response.Targets) > DatasetBindingMaxTargetStatuses {
		return errors.New("binding response identity mismatch")
	}
	if request.Action != DatasetBindingInspect && (response.Revision == 0 || response.Revision <= request.ExpectedRevision) {
		return errors.New("mutation acknowledgement revision did not advance")
	}
	if response.Desired != nil {
		if err := response.Desired.Validate(); err != nil {
			return err
		}
		if response.Desired.InstanceID != request.InstanceID || response.Desired.SourceID != request.SourceID || response.Desired.Revision != response.Revision {
			return errors.New("desired binding identity mismatch")
		}
	}
	switch request.Action {
	case DatasetBindingBind, DatasetBindingReplace:
		if response.Desired == nil || !equalDatasetBindingSpecs(response.Desired.Spec, *request.Spec) || !equalExecutionSelections(response.Desired.Targets, request.Targets) {
			return errors.New("acknowledgement does not preserve requested desired binding")
		}
	case DatasetBindingUnbind:
		if response.Desired != nil {
			return errors.New("unbind acknowledgement retains desired record")
		}
	}
	seen := map[string]bool{}
	for _, status := range response.Targets {
		if seen[status.AgentID] {
			return errors.New("duplicate binding target status")
		}
		seen[status.AgentID] = true
		if err := status.Validate(); err != nil {
			return err
		}
		if status.Desired != nil && (response.Desired == nil || !equalDatasetBindingSpecs(*status.Desired, response.Desired.Spec)) {
			return errors.New("node desired binding differs from persisted desired record")
		}
	}
	return nil
}

// DatasetBindingAuthorization is assembled from current Host records, never
// from request JSON. TargetInstanceID may differ from CallerInstanceID, but its
// owning plugin and resource group must match and actual targets remain scoped.
// SourceIDs express explicit source-sharing grants, not source creator identity.
type DatasetBindingAuthorization struct {
	// BoundAgentIDs are existing node bindings for this owned instance/source,
	// including nodes now removed from effective targets that need removal ACK.
	BoundAgentIDs                                                       []string
	InstanceRevision, PolicyRevision                                    uint64
	PolicyStage                                                         *PolicyStageIdentity
	PolicyModeHandling                                                  PolicyModeHandling
	CallerPluginID, CallerInstanceID, CallerGeneration, ResourceGroupID string
	TargetPluginID, TargetInstanceID, TargetResourceGroupID             string
	DeclaredScopes, GrantedScopes                                       []string
	EffectiveAgentIDs, GrantedAgentIDs, SourceIDs                       []string
	Version                                                             *DatasetVersion
	Catalog                                                             []DatasetClassification
	Current                                                             *DatasetBindingRecord
	Revision                                                            uint64
	Replay                                                              *DatasetBindingReplay
}
type DatasetBindingReplay struct {
	// Persist original resolved targets with the outcome; effective target lists
	// can change later, but retry must not become a new-target mutation.
	ResolvedAgentIDs []string
	RequestDigest    string
	Response         DatasetBindingResponse
}

func DatasetBindingRequestDigest(request DatasetBindingRequest) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ValidateDatasetBindingAuthorization(request DatasetBindingRequest, authorization DatasetBindingAuthorization) ([]string, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	for _, value := range []string{authorization.CallerPluginID, authorization.CallerInstanceID, authorization.CallerGeneration, authorization.ResourceGroupID, authorization.TargetPluginID, authorization.TargetInstanceID, authorization.TargetResourceGroupID} {
		if ValidatePolicyIdentity(value) != nil {
			return nil, &RuntimeError{Code: ErrorPermissionDenied, Message: "binding authority incomplete"}
		}
	}
	if authorization.CallerPluginID != authorization.TargetPluginID || authorization.ResourceGroupID != authorization.TargetResourceGroupID || request.InstanceID != authorization.TargetInstanceID {
		return nil, &RuntimeError{Code: ErrorPermissionDenied, Message: "foreign binding instance or resource group"}
	}
	if err := ValidateHostCapabilityGrant(CapabilityDatasetBind, authorization.DeclaredScopes, authorization.GrantedScopes); err != nil {
		return nil, &RuntimeError{Code: ErrorPermissionDenied, Message: "dataset binding capability denied"}
	}
	if request.InstanceUpdate != nil {
		update := request.InstanceUpdate
		if len(update.Config) > 0 && (!hasManagedGrant(authorization.DeclaredScopes, "storage.write") || !hasManagedGrant(authorization.GrantedScopes, "storage.write")) {
			return nil, &RuntimeError{Code: ErrorPermissionDenied, Message: "atomic Config update requires storage.write"}
		}
		if update.PolicyDefaults != nil {
			if ValidateHostCapabilityGrant(CapabilityPolicyControl, authorization.DeclaredScopes, authorization.GrantedScopes) != nil || authorization.PolicyStage == nil {
				return nil, &RuntimeError{Code: ErrorPermissionDenied, Message: "atomic policy defaults update denied"}
			}
		}
	}
	sourceAllowed := false
	for _, source := range authorization.SourceIDs {
		if source == request.SourceID {
			sourceAllowed = true
		}
	}
	if !sourceAllowed {
		return nil, &RuntimeError{Code: ErrorPermissionDenied, Message: "dataset source binding grant denied"}
	}
	if authorization.Current != nil {
		if authorization.Current.Validate() != nil || authorization.Current.InstanceID != request.InstanceID || authorization.Current.SourceID != request.SourceID || authorization.Current.Revision != authorization.Revision {
			return nil, errors.New("Host current binding identity mismatch")
		}
	}
	effectiveTargets := authorization.EffectiveAgentIDs
	if authorization.Replay != nil {
		effectiveTargets = authorization.Replay.ResolvedAgentIDs
	}
	targets, err := ResolveExecutionTargets(request.Targets, effectiveTargets, authorization.GrantedAgentIDs)
	if err != nil {
		return nil, err
	}
	if request.Spec != nil && authorization.Replay == nil {
		if authorization.Version == nil || authorization.Version.Validate() != nil || authorization.Version.SourceID != request.SourceID || authorization.Version.Digest != request.Spec.VersionDigest {
			return nil, &RuntimeError{Code: ErrorUnavailable, Message: "requested immutable dataset version unavailable"}
		}
		if len(authorization.Catalog) > DatasetMaxCatalogPage {
			return nil, errors.New("binding catalog authority exceeds page bound")
		}
		for _, selector := range request.Spec.Classifications {
			found := false
			for _, known := range authorization.Catalog {
				// Catalog entries identify classifications, not their query predicates.
				// Spec validation and the binding retain the full typed attributes.
				if selector.Kind == known.Kind && selector.Name == known.Name {
					found = true
					break
				}
			}
			if !found {
				return nil, &RuntimeError{Code: ErrorInvalidArgument, Message: "requested dataset classification unavailable"}
			}
		}
	}
	return targets, nil
}

// DatasetBindingHost must atomically recheck current authority/CAS, persist the
// desired record and immutable revision, and store its outcome keyed by caller
// plugin+instance+OperationID+request digest in the same transaction. Replay must
// survive restart and never repeat mutations. Unbind also removes obsolete node
// references, retaining actual Applied until ACK; downloads/query are separate.
type DatasetBindingHost interface {
	ManageDatasetBinding(context.Context, DatasetBindingAuthorization, DatasetBindingRequest) (DatasetBindingResponse, error)
}

func DecodeDatasetBindingRequest(payload json.RawMessage) (DatasetBindingRequest, error) {
	var request DatasetBindingRequest
	if len(payload) > DatasetBindingMaxFrameBytes || validatePolicyOverlayJSON(payload, DatasetBindingMaxFrameBytes) != nil {
		return request, errors.New("binding frame invalid or oversized")
	}
	if err := decodePolicyConsumptionJSON(payload, &request, DatasetBindingMaxFrameBytes); err != nil {
		return request, err
	}
	return request, request.Validate()
}
func CallDatasetBindingHost(ctx context.Context, host DatasetBindingHost, authorization DatasetBindingAuthorization, call HostRuntimeCall) (DatasetBindingResponse, error) {
	if call.Validate() != nil || call.Operation != HostRuntimeDatasetBinding {
		return DatasetBindingResponse{}, errors.New("invalid binding HostRuntime operation")
	}
	payload := call.Payload
	ctx, cancel := context.WithTimeout(ctx, DatasetBindingMaxDuration)
	defer cancel()
	request, err := DecodeDatasetBindingRequest(payload)
	if err != nil {
		return DatasetBindingResponse{}, err
	}
	if call.OperationID != request.OperationID {
		return DatasetBindingResponse{}, errors.New("binding envelope operation ID mismatch")
	}
	selectedTargets, validationErr := ValidateDatasetBindingAuthorization(request, authorization)
	err = validationErr
	if err != nil {
		return DatasetBindingResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return DatasetBindingResponse{}, err
	}
	expectedTargets := map[string]bool{}
	for _, id := range selectedTargets {
		expectedTargets[id] = true
	}
	if len(authorization.BoundAgentIDs) > ExecutionTargetMaxAgents {
		return DatasetBindingResponse{}, errors.New("existing binding target authority exceeds bound")
	}
	for _, id := range authorization.BoundAgentIDs {
		if ValidatePolicyIdentity(id) != nil {
			return DatasetBindingResponse{}, errors.New("invalid existing binding target")
		}
		expectedTargets[id] = true
	}
	var response DatasetBindingResponse
	if authorization.Replay != nil {
		digest, _ := DatasetBindingRequestDigest(request)
		if request.Action == DatasetBindingInspect || authorization.Replay.RequestDigest != digest {
			return response, errors.New("binding operation ID reused with different request")
		}
		response = authorization.Replay.Response
	} else {
		if request.InstanceUpdate != nil {
			update := request.InstanceUpdate
			if update.ExpectedRevision != authorization.InstanceRevision || (update.PolicyDefaults != nil && update.PolicyDefaults.ExpectedRevision != authorization.PolicyRevision) {
				return response, errors.New("atomic instance or policy settings revision conflict")
			}
			if update.PolicyDefaults != nil {
				if err := ValidatePolicyDefaultSettingsUpdate(*update.PolicyDefaults, *authorization.PolicyStage, authorization.PolicyModeHandling, authorization.PolicyRevision); err != nil {
					return response, err
				}
			}
		}
		if request.Action == DatasetBindingBind && authorization.Current != nil {
			return response, errors.New("binding already exists")
		}
		if request.Action == DatasetBindingReplace || request.Action == DatasetBindingUnbind {
			if authorization.Current == nil || authorization.Current.Revision != request.ExpectedRevision || authorization.Revision != request.ExpectedRevision {
				return response, errors.New("binding revision conflict")
			}
		}
		if host == nil {
			return response, &RuntimeError{Code: ErrorUnavailable, Message: "dataset binding Host unavailable"}
		}
		response, err = host.ManageDatasetBinding(ctx, authorization, request)
		if err != nil {
			return DatasetBindingResponse{}, err
		}
	}
	if err := response.ValidateFor(request); err != nil {
		return DatasetBindingResponse{}, err
	}
	if authorization.Replay == nil && request.Action != DatasetBindingInspect && response.Revision <= authorization.Revision {
		return DatasetBindingResponse{}, errors.New("binding Host reset its persisted revision")
	}
	allowed := map[string]bool{}
	for _, id := range authorization.GrantedAgentIDs {
		allowed[id] = true
	}
	for _, status := range response.Targets {
		if !allowed[status.AgentID] || (authorization.Replay == nil && !expectedTargets[status.AgentID]) {
			return DatasetBindingResponse{}, errors.New("binding Host returned an unauthorized target")
		}
	}
	for _, id := range selectedTargets {
		found := false
		for _, status := range response.Targets {
			if status.AgentID == id {
				found = true
				break
			}
		}
		if !found {
			return DatasetBindingResponse{}, errors.New("binding Host omitted a selected target status")
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return DatasetBindingResponse{}, err
	}
	complete, _ := json.Marshal(HostRuntimeResponse{Payload: encoded})
	if len(complete) > DatasetBindingMaxFrameBytes {
		return DatasetBindingResponse{}, &RuntimeError{Code: ErrorResourceExhausted, Message: "binding response exceeds frame budget"}
	}
	if err := ctx.Err(); err != nil {
		return DatasetBindingResponse{}, err
	}
	return response, nil
}
func (client *HostRuntimeClient) ManageDatasetBinding(ctx context.Context, request DatasetBindingRequest) (DatasetBindingResponse, error) {
	if err := request.Validate(); err != nil {
		return DatasetBindingResponse{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return DatasetBindingResponse{}, err
	}
	if len(payload) > DatasetBindingMaxFrameBytes {
		return DatasetBindingResponse{}, errors.New("binding request exceeds frame budget")
	}
	var raw json.RawMessage
	if err := client.Call(ctx, HostRuntimeCall{Operation: HostRuntimeDatasetBinding, OperationID: request.OperationID, Payload: payload}, &raw); err != nil {
		return DatasetBindingResponse{}, err
	}
	return DecodeDatasetBindingResponse(request, raw)
}
func DecodeDatasetBindingResponse(request DatasetBindingRequest, raw json.RawMessage) (DatasetBindingResponse, error) {
	var response DatasetBindingResponse
	if err := decodePolicyConsumptionJSON(raw, &response, DatasetBindingMaxFrameBytes); err != nil {
		return response, err
	}
	return response, response.ValidateFor(request)
}
