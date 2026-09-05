package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
)

const (
	HostRuntimePolicyControl                   = "policy.control"
	PermissionPolicyControl                    = "policy.control"
	CapabilityPolicyControl     HostCapability = PermissionPolicyControl
	PolicyControlConfigMaxBytes                = 64 << 10
	// This signed manifest declaration describes how the guest produces its
	// decision. It is not an operator's mode and never comes from Config.mode.
	PolicyModeHandlingMetadataKey = "policy.mode.handling"
)

type PolicyMode string

const (
	PolicyModeObserve PolicyMode = "observe"
	PolicyModeEnforce PolicyMode = "enforce"
)

func (mode PolicyMode) Validate() error {
	if mode != PolicyModeObserve && mode != PolicyModeEnforce {
		return errors.New("policy mode must be observe or enforce")
	}
	return nil
}

// Legacy preserves the old guest/config/overlay/static-failure-policy path.
// Raw guests return their actual allow/deny decision regardless of operator
// mode; Host performs observation, including failed checks. LegacyWAF is an
// explicit supported bridge: Host supplies the public legacy deny-mode WAF
// overlay so the guest returns raw hits, then Host applies observe/enforce and
// handles failed checks. Unknown legacy guests must migrate
// before accepting a typed mode; fail-open is never an observe declaration.
type PolicyModeHandling string

const (
	PolicyModeHandlingLegacy    PolicyModeHandling = "legacy"
	PolicyModeHandlingRaw       PolicyModeHandling = "raw-decision-v1"
	PolicyModeHandlingLegacyWAF PolicyModeHandling = "legacy-waf-v1"
)

func (handling PolicyModeHandling) Validate() error {
	switch handling {
	case PolicyModeHandlingLegacy, PolicyModeHandlingRaw, PolicyModeHandlingLegacyWAF:
		return nil
	}
	return errors.New("unsupported policy mode handling")
}

func PolicyModeHandlingForManifest(manifest Manifest) (PolicyModeHandling, error) {
	value, present := manifest.Metadata[PolicyModeHandlingMetadataKey]
	if !present {
		return PolicyModeHandlingLegacy, nil
	}
	handling := PolicyModeHandling(value)
	if handling.Validate() != nil || !RuntimeProjectsAgentPolicy(manifest.Runtime) {
		return "", errors.New("policy mode handling requires a supported Agent policy face")
	}
	if handling == PolicyModeHandlingLegacyWAF && manifest.Runtime.PolicyKind != PolicyOverlayStageWAF {
		return "", errors.New("legacy WAF mode handling requires a WAF policy")
	}
	if handling != PolicyModeHandlingLegacy && policyOverlayStageOrder(manifest.Runtime.PolicyKind) == 0 {
		return "", errors.New("typed mode handling requires a known policy stage kind")
	}
	return handling, nil
}

type PolicyStageIdentity struct {
	Kind     string `json:"kind"`
	PolicyID string `json:"policy_id,omitempty"`
}

func (stage PolicyStageIdentity) Validate() error {
	if policyOverlayStageOrder(stage.Kind) == 0 || ValidatePolicyIdentity(stage.PolicyID) != nil {
		return errors.New("policy stage identity is invalid")
	}
	return nil
}
func (stage PolicyStageIdentity) validateSelector() error {
	if policyOverlayStageOrder(stage.Kind) == 0 || (stage.PolicyID != "" && ValidatePolicyIdentity(stage.PolicyID) != nil) {
		return errors.New("policy stage selector is invalid")
	}
	return nil
}
func (stage PolicyStageIdentity) matches(target PolicyStageIdentity) bool {
	return stage.Kind == target.Kind && (stage.PolicyID == "" || stage.PolicyID == target.PolicyID)
}

// Entry identity is Host-owned. The same numeric/string ID on a different
// node, protocol, or resource kind is a different entry. Managed entries are
// generic plugin listeners, never plugin-specific account or protocol config.
type PolicyEntryTarget struct {
	NodeID string `json:"node_id"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
}

const (
	PolicyEntryHTTP       = "http-rule"
	PolicyEntryTCP        = "tcp-rule"
	PolicyEntryUDP        = "udp-rule"
	PolicyEntryManagedTCP = "plugin-tcp"
	PolicyEntryManagedUDP = "plugin-udp"
)

func (entry PolicyEntryTarget) Validate() error {
	if ValidatePolicyIdentity(entry.NodeID) != nil || ValidatePolicyIdentity(entry.ID) != nil {
		return errors.New("policy entry identity is invalid")
	}
	switch entry.Kind {
	case PolicyEntryHTTP, PolicyEntryTCP, PolicyEntryUDP, PolicyEntryManagedTCP, PolicyEntryManagedUDP:
		return nil
	}
	return errors.New("policy entry kind is invalid")
}

// Nil DefaultMode explicitly means legacy/unmigrated. A new installation is
// observe only after an explicit observed default is persisted. EntryMode is
// limited to this stage and cannot lower an enforced instance default.
type PolicyModeSettings struct {
	Handling    PolicyModeHandling `json:"handling"`
	DefaultMode *PolicyMode        `json:"default_mode,omitempty"`
	EntryMode   *PolicyMode        `json:"entry_mode,omitempty"`
}

func (settings PolicyModeSettings) Validate() error {
	if settings.Handling.Validate() != nil {
		return errors.New("policy settings handling is invalid")
	}
	if settings.Handling == PolicyModeHandlingLegacy {
		if settings.DefaultMode != nil || settings.EntryMode != nil {
			return errors.New("legacy policy settings cannot claim typed modes")
		}
		return nil
	}
	if settings.DefaultMode == nil || settings.DefaultMode.Validate() != nil {
		return errors.New("typed policy settings require a default mode")
	}
	if settings.EntryMode != nil {
		if settings.EntryMode.Validate() != nil {
			return errors.New("entry policy mode is invalid")
		}
		if *settings.DefaultMode == PolicyModeEnforce && *settings.EntryMode == PolicyModeObserve {
			return errors.New("entry mode cannot weaken an enforced default")
		}
	}
	return nil
}
func ResolvePolicyMode(settings PolicyModeSettings) (PolicyMode, error) {
	if err := settings.Validate(); err != nil {
		return "", err
	}
	if settings.Handling == PolicyModeHandlingLegacy {
		return "", nil
	}
	if settings.EntryMode != nil {
		return *settings.EntryMode, nil
	}
	return *settings.DefaultMode, nil
}

// Reusable by an atomic dataset-binding + Config + defaults mutation. The
// enclosing operation checks the common instance clock and capabilities; this
// gate separately checks the policy settings clock and supported guest bridge.
type PolicyDefaultSettingsUpdate struct {
	Stage            PolicyStageIdentity `json:"stage"`
	Mode             PolicyMode          `json:"mode"`
	ExpectedRevision uint64              `json:"expected_revision"`
}

func (update PolicyDefaultSettingsUpdate) Validate() error {
	if update.Stage.validateSelector() != nil || update.Mode.Validate() != nil || update.ExpectedRevision == math.MaxUint64 {
		return errors.New("policy default settings update is invalid")
	}
	return nil
}
func ValidatePolicyDefaultSettingsUpdate(update PolicyDefaultSettingsUpdate, targetStage PolicyStageIdentity, handling PolicyModeHandling, currentRevision uint64) error {
	if update.Validate() != nil || targetStage.Validate() != nil || !update.Stage.matches(targetStage) || update.ExpectedRevision != currentRevision {
		return errors.New("policy default settings authority or revision differs")
	}
	return validateTypedPolicyHandling(targetStage, handling)
}
func validateTypedPolicyHandling(stage PolicyStageIdentity, handling PolicyModeHandling) error {
	if handling == PolicyModeHandlingRaw {
		return nil
	}
	if handling == PolicyModeHandlingLegacyWAF && stage.Kind == PolicyOverlayStageWAF {
		return nil
	}
	return errors.New("policy requires supported mode handling or explicit legacy migration")
}

type PolicyControlAction string

const (
	PolicyControlInspect         PolicyControlAction = "inspect"
	PolicyControlReplaceInstance PolicyControlAction = "replace-instance"
	PolicyControlReplaceEntry    PolicyControlAction = "replace-entry"
	PolicyControlResetEntry      PolicyControlAction = "reset-entry"
)

// Every mutation is one durable operation and advances BOTH settings revision
// and the common instance version once. The latter is also advanced by ordinary
// instance Config changes, configure/upgrade/rollback and atomic data-consumer
// updates, so those paths cannot bypass this CAS. A retry with the same operation
// identity returns the original versions/result, never a later "latest" view.
// Host must validate all affected stored entry settings against a changed
// default before publication; an incompatible candidate preserves the old state.
//
// Config, when present, is committed with the typed default in the same candidate
// snapshot. It uses the normal schema/writeOnly/secret rules; it is not a way to
// persist secret material. For a simultaneous dataset change use the public
// dataset-binding InstanceUpdate bundle rather than sequential operations.
type PolicyControlRequest struct {
	Action                  PolicyControlAction `json:"action"`
	OperationID             string              `json:"operation_id,omitempty"`
	InstanceID              string              `json:"instance_id,omitempty"`
	Stage                   PolicyStageIdentity `json:"stage"`
	Entry                   *PolicyEntryTarget  `json:"entry,omitempty"`
	Mode                    PolicyMode          `json:"mode,omitempty"`
	ExpectedRevision        *uint64             `json:"expected_revision,omitempty"`
	ExpectedInstanceVersion *uint64             `json:"expected_instance_version,omitempty"`
	Config                  json.RawMessage     `json:"config,omitempty"`
}

func (request PolicyControlRequest) Validate() error {
	if request.InstanceID != "" && ValidatePolicyIdentity(request.InstanceID) != nil {
		return errors.New("policy control instance is invalid")
	}
	if request.Stage.validateSelector() != nil {
		return errors.New("policy control stage is invalid")
	}
	if request.Entry != nil {
		if request.Entry.Validate() != nil || (request.Stage.Kind == PolicyOverlayStageWAF && request.Entry.Kind != PolicyEntryHTTP) {
			return errors.New("policy control entry is invalid for the stage")
		}
	}
	if request.Action == PolicyControlInspect {
		if request.OperationID != "" || request.Mode != "" || request.ExpectedRevision != nil || request.ExpectedInstanceVersion != nil || len(request.Config) != 0 {
			return errors.New("policy inspect does not accept mutation fields")
		}
		return nil
	}
	if ValidatePolicyIdentity(request.OperationID) != nil || request.ExpectedRevision == nil || *request.ExpectedRevision == math.MaxUint64 || request.ExpectedInstanceVersion == nil || *request.ExpectedInstanceVersion == 0 || *request.ExpectedInstanceVersion == math.MaxUint64 {
		return errors.New("policy mutation needs operation identity and both expected versions")
	}
	switch request.Action {
	case PolicyControlReplaceInstance:
		if request.Entry != nil || request.Mode.Validate() != nil {
			return errors.New("instance policy mutation is invalid")
		}
		if len(request.Config) > 0 {
			if validatePolicyOverlayJSON(request.Config, PolicyControlConfigMaxBytes) != nil || !policyOverlayJSONObject(request.Config) {
				return errors.New("atomic policy Config must be a bounded JSON object")
			}
		}
	case PolicyControlReplaceEntry:
		if request.Entry == nil || request.Mode.Validate() != nil || len(request.Config) != 0 {
			return errors.New("entry policy mutation is invalid")
		}
	case PolicyControlResetEntry:
		if request.Entry == nil || request.Mode != "" || len(request.Config) != 0 {
			return errors.New("entry reset only removes its own override")
		}
	default:
		return errors.New("unsupported policy control action")
	}
	return nil
}

// Authority is assembled from a live authenticated Host caller and separately
// resolved policy instance/stage/entry records, never copied from request JSON.
// A management instance can target another instance only when Host independently
// resolves it as owned by this plugin and resource group. EntryAuthorized also
// proves that this exact policy stage applies to the selected entry.
type PolicyControlAuthority struct {
	CallerInstanceID, CallerPluginID, CallerGeneration, CallerResourceGroupID string
	CallerLive                                                                bool
	// Only Host may set Replaying after matching a durable operation ID and
	// complete canonical request fingerprint; it never skips current authority.
	Replaying                             bool
	InstanceID, PluginID, ResourceGroupID string
	Stage                                 PolicyStageIdentity
	Entry                                 *PolicyEntryTarget
	EntryAuthorized                       bool
	InstanceVersion, SettingsRevision     uint64
	Handling                              PolicyModeHandling
	DefaultMode                           *PolicyMode
	Grants                                []string
}

func ValidatePolicyControlAuthority(request PolicyControlRequest, authority PolicyControlAuthority) error {
	if request.Validate() != nil {
		return errors.New("policy control request is invalid")
	}
	for _, id := range []string{authority.CallerInstanceID, authority.CallerPluginID, authority.CallerGeneration, authority.CallerResourceGroupID, authority.InstanceID, authority.PluginID, authority.ResourceGroupID} {
		if ValidatePolicyIdentity(id) != nil {
			return errors.New("policy control Host authority is incomplete")
		}
	}
	if !authority.CallerLive || authority.InstanceVersion == 0 || !hasManagedGrant(authority.Grants, PermissionPolicyControl) || authority.CallerPluginID != authority.PluginID || authority.CallerResourceGroupID != authority.ResourceGroupID || authority.Stage.Validate() != nil || !request.Stage.matches(authority.Stage) {
		return errors.New("policy control authority is denied")
	}
	wanted := request.InstanceID
	if wanted == "" {
		wanted = authority.CallerInstanceID
	}
	if wanted != authority.InstanceID {
		return errors.New("policy control resolved instance differs")
	}
	if !equalPolicyEntries(request.Entry, authority.Entry) {
		return errors.New("policy control resolved entry differs")
	}
	if request.Entry != nil && (!authority.EntryAuthorized || authority.Entry.Validate() != nil) {
		return errors.New("policy entry stage is not authorized")
	}
	if request.Action == PolicyControlInspect {
		return nil
	}
	if len(request.Config) > 0 && !hasManagedGrant(authority.Grants, PermissionStorageWrite) {
		return errors.New("atomic Config update requires storage.write")
	}
	if authority.Replaying {
		return nil
	}
	if *request.ExpectedRevision != authority.SettingsRevision || *request.ExpectedInstanceVersion != authority.InstanceVersion {
		return errors.New("policy control revision conflict")
	}
	if request.Action == PolicyControlReplaceInstance {
		return ValidatePolicyDefaultSettingsUpdate(PolicyDefaultSettingsUpdate{Stage: request.Stage, Mode: request.Mode, ExpectedRevision: *request.ExpectedRevision}, authority.Stage, authority.Handling, authority.SettingsRevision)
	}
	if request.Action == PolicyControlResetEntry {
		return nil
	}
	if validateTypedPolicyHandling(authority.Stage, authority.Handling) != nil || authority.DefaultMode == nil {
		return errors.New("entry mode requires an explicitly supported instance default")
	}
	return (PolicyModeSettings{Handling: authority.Handling, DefaultMode: authority.DefaultMode, EntryMode: &request.Mode}).Validate()
}
func equalPolicyEntries(left, right *PolicyEntryTarget) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type PolicySettingsVersion struct {
	Revision        uint64 `json:"revision"`
	InstanceVersion uint64 `json:"instance_version"`
}
type PolicySettingsSnapshot struct {
	Version  PolicySettingsVersion `json:"version"`
	Settings PolicyModeSettings    `json:"settings"`
}

func (snapshot PolicySettingsSnapshot) Validate() error {
	if snapshot.Version.InstanceVersion == 0 || snapshot.Settings.Validate() != nil {
		return errors.New("policy settings snapshot is invalid")
	}
	if snapshot.Settings.Handling != PolicyModeHandlingLegacy && snapshot.Version.Revision == 0 {
		return errors.New("typed settings require a committed revision")
	}
	return nil
}

// Node is present only for entry inspection. Mutation acknowledgements and
// instance-global inspect do not claim that every target node has applied.
type PolicySettingsNodeStatus struct {
	Phase      string                  `json:"phase"`
	Applied    *PolicySettingsSnapshot `json:"applied,omitempty"`
	Generation string                  `json:"generation,omitempty"`
	Failure    string                  `json:"failure,omitempty"`
}
type PolicyControlResponse struct {
	OperationID string                    `json:"operation_id,omitempty"`
	InstanceID  string                    `json:"instance_id"`
	Stage       PolicyStageIdentity       `json:"stage"`
	Entry       *PolicyEntryTarget        `json:"entry,omitempty"`
	Desired     PolicySettingsSnapshot    `json:"desired"`
	Node        *PolicySettingsNodeStatus `json:"node,omitempty"`
}

func (response PolicyControlResponse) ValidateFor(request PolicyControlRequest) error {
	if request.Validate() != nil || ValidatePolicyIdentity(response.InstanceID) != nil || response.Stage.Validate() != nil || !request.Stage.matches(response.Stage) || !equalPolicyEntries(request.Entry, response.Entry) || response.Desired.Validate() != nil {
		return errors.New("policy control response identity or desired state is invalid")
	}
	if request.InstanceID != "" && response.InstanceID != request.InstanceID {
		return errors.New("policy response belongs to another instance")
	}
	if response.Desired.Settings.Handling == PolicyModeHandlingLegacyWAF && response.Stage.Kind != PolicyOverlayStageWAF {
		return errors.New("legacy WAF handling belongs to another kind")
	}
	if response.Entry == nil && response.Desired.Settings.EntryMode != nil {
		return errors.New("instance response contains an entry override")
	}
	if request.Action == PolicyControlInspect {
		if response.OperationID != "" {
			return errors.New("policy inspect response contains operation acknowledgement")
		}
	} else {
		if response.OperationID != request.OperationID || response.Desired.Version.Revision != *request.ExpectedRevision+1 || response.Desired.Version.InstanceVersion != *request.ExpectedInstanceVersion+1 {
			return errors.New("policy acknowledgement changed operation or version")
		}
		switch request.Action {
		case PolicyControlReplaceInstance:
			if response.Desired.Settings.DefaultMode == nil || *response.Desired.Settings.DefaultMode != request.Mode {
				return errors.New("policy default acknowledgement differs")
			}
		case PolicyControlReplaceEntry:
			if response.Desired.Settings.EntryMode == nil || *response.Desired.Settings.EntryMode != request.Mode {
				return errors.New("policy entry acknowledgement differs")
			}
		case PolicyControlResetEntry:
			if response.Desired.Settings.EntryMode != nil {
				return errors.New("policy reset retained an override")
			}
		}
	}
	if response.Node != nil {
		if response.Entry == nil || request.Action != PolicyControlInspect {
			return errors.New("node application state requires entry inspect")
		}
		node := response.Node
		if node.Applied != nil {
			if node.Applied.Validate() != nil || ValidatePolicyIdentity(node.Generation) != nil || node.Applied.Version.Revision > response.Desired.Version.Revision || node.Applied.Version.InstanceVersion > response.Desired.Version.InstanceVersion {
				return errors.New("applied policy state is invalid")
			}
			if node.Applied.Settings.Handling == PolicyModeHandlingLegacyWAF && response.Stage.Kind != PolicyOverlayStageWAF {
				return errors.New("applied WAF mode belongs to another stage")
			}
		} else if node.Generation != "" {
			return errors.New("generation requires applied policy state")
		}
		switch node.Phase {
		case "applied":
			if node.Applied == nil || !equalPolicySnapshots(*node.Applied, response.Desired) || node.Failure != "" {
				return errors.New("applied policy state does not match desired")
			}
		case "preparing", "unavailable":
			if node.Failure != "" {
				return errors.New("pending policy state contains unexpected failure")
			}
		case "failed":
			if !validPolicyCheckFailure(node.Failure) {
				return errors.New("policy check failure code is invalid")
			}
		default:
			return errors.New("policy node phase is invalid")
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > PluginHostPayloadMaxBytes {
		return errors.New("policy response exceeds transport bound")
	}
	return nil
}
func equalPolicySnapshots(a, b PolicySettingsSnapshot) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func DecodePolicyControlRequest(raw json.RawMessage) (PolicyControlRequest, error) {
	var request PolicyControlRequest
	if err := decodePolicyConsumptionJSON(raw, &request, PolicyControlConfigMaxBytes+4096); err != nil {
		return request, err
	}
	if err := request.Validate(); err != nil {
		return PolicyControlRequest{}, err
	}
	return request, nil
}
func DecodePolicyControlResponse(request PolicyControlRequest, raw json.RawMessage) (PolicyControlResponse, error) {
	var response PolicyControlResponse
	if err := decodePolicyConsumptionJSON(raw, &response, PluginHostPayloadMaxBytes); err != nil {
		return response, err
	}
	if err := response.ValidateFor(request); err != nil {
		return PolicyControlResponse{}, err
	}
	return response, nil
}
func (client *HostRuntimeClient) ControlPolicy(ctx context.Context, request PolicyControlRequest) (PolicyControlResponse, error) {
	if err := request.Validate(); err != nil {
		return PolicyControlResponse{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return PolicyControlResponse{}, err
	}
	var response json.RawMessage
	if err := client.Call(ctx, HostRuntimeCall{Operation: HostRuntimePolicyControl, OperationID: request.OperationID, Payload: payload}, &response); err != nil {
		return PolicyControlResponse{}, err
	}
	return DecodePolicyControlResponse(request, response)
}
