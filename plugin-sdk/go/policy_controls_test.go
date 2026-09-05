package pluginsdk

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
)

func policyModePointer(mode PolicyMode) *PolicyMode { return &mode }
func policyVersionPointer(version uint64) *uint64   { return &version }
func policyControlFixture() (PolicyControlRequest, PolicyControlAuthority) {
	request := PolicyControlRequest{Action: PolicyControlReplaceInstance, OperationID: "set-default", InstanceID: "policy-instance", Stage: PolicyStageIdentity{Kind: PolicyOverlayStageIP, PolicyID: "policy-instance"}, Mode: PolicyModeObserve, ExpectedRevision: policyVersionPointer(0), ExpectedInstanceVersion: policyVersionPointer(3), Config: json.RawMessage(`{"mode":"opaque-business-value","deny":["192.0.2.1"]}`)}
	authority := PolicyControlAuthority{CallerInstanceID: "management-instance", CallerPluginID: "ip-plugin", CallerGeneration: "generation-a", CallerResourceGroupID: "group-a", CallerLive: true, InstanceID: "policy-instance", PluginID: "ip-plugin", ResourceGroupID: "group-a", Stage: request.Stage, InstanceVersion: 3, Handling: PolicyModeHandlingRaw, Grants: []string{PermissionPolicyControl, PermissionStorageWrite}}
	return request, authority
}

func TestPolicyControlAtomicTransportReplayAndLegacyInspect(t *testing.T) {
	request, authority := policyControlFixture()
	state := PolicySettingsSnapshot{Version: PolicySettingsVersion{InstanceVersion: 3}, Settings: PolicyModeSettings{Handling: PolicyModeHandlingLegacy}}
	var storedConfig json.RawMessage
	type outcome struct {
		request  []byte
		response PolicyControlResponse
	}
	results := map[string]outcome{}
	client := managedTestClient(t, func(_ *http.Request, call HostRuntimeCall) HostRuntimeResponse {
		if call.Operation != HostRuntimePolicyControl {
			t.Fatal("wrong public operation")
		}
		decoded, err := DecodePolicyControlRequest(call.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if call.OperationID != decoded.OperationID {
			t.Fatal("outer operation identity changed")
		}
		canonical, _ := json.Marshal(decoded)
		current := authority
		current.SettingsRevision = state.Version.Revision
		current.InstanceVersion = state.Version.InstanceVersion
		current.DefaultMode = state.Settings.DefaultMode
		current.Entry = decoded.Entry
		current.EntryAuthorized = decoded.Entry != nil
		prior, exists := results[decoded.OperationID]
		if exists {
			if !bytes.Equal(prior.request, canonical) {
				return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorPermissionDenied, Message: "different intent"}}
			}
			current.Replaying = true
		}
		if err := ValidatePolicyControlAuthority(decoded, current); err != nil {
			return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorPermissionDenied, Message: "denied"}}
		}
		if exists {
			data, _ := json.Marshal(prior.response)
			return HostRuntimeResponse{Payload: data}
		}
		if decoded.Action != PolicyControlInspect {
			state.Version.Revision++
			state.Version.InstanceVersion++
			switch decoded.Action {
			case PolicyControlReplaceInstance:
				state.Settings = PolicyModeSettings{Handling: PolicyModeHandlingRaw, DefaultMode: policyModePointer(decoded.Mode)}
				storedConfig = append(json.RawMessage(nil), decoded.Config...)
			case PolicyControlReplaceEntry:
				state.Settings.EntryMode = policyModePointer(decoded.Mode)
			case PolicyControlResetEntry:
				state.Settings.EntryMode = nil
			}
		}
		response := PolicyControlResponse{OperationID: decoded.OperationID, InstanceID: current.InstanceID, Stage: current.Stage, Entry: decoded.Entry, Desired: state}
		if err := response.ValidateFor(decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.OperationID != "" {
			results[decoded.OperationID] = outcome{request: canonical, response: response}
		}
		data, _ := json.Marshal(response)
		return HostRuntimeResponse{Payload: data}
	})
	inspect := PolicyControlRequest{Action: PolicyControlInspect, InstanceID: request.InstanceID, Stage: request.Stage}
	legacy, err := client.ControlPolicy(t.Context(), inspect)
	if err != nil || legacy.Desired.Settings.Handling != PolicyModeHandlingLegacy || legacy.Desired.Settings.DefaultMode != nil {
		t.Fatal("legacy inspect inferred a new mode", err)
	}
	first, err := client.ControlPolicy(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedConfig, request.Config) || first.Desired.Version != (PolicySettingsVersion{Revision: 1, InstanceVersion: 4}) || *first.Desired.Settings.DefaultMode != PolicyModeObserve {
		t.Fatal("atomic configuration and trusted settings diverged")
	}
	entry := request
	entry.Action = PolicyControlReplaceEntry
	entry.OperationID = "set-entry"
	entry.Config = nil
	entry.ExpectedRevision = policyVersionPointer(1)
	entry.ExpectedInstanceVersion = policyVersionPointer(4)
	entry.Entry = &PolicyEntryTarget{NodeID: "local", Kind: PolicyEntryTCP, ID: "42"}
	entry.Mode = PolicyModeEnforce
	if _, err := client.ControlPolicy(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	replayed, err := client.ControlPolicy(t.Context(), request)
	if err != nil || replayed.Desired.Version != first.Desired.Version || state.Version.Revision != 2 {
		t.Fatal("retry did not return its original safe result", err)
	}
	changed := request
	changed.Config = json.RawMessage(`{"deny":[]}`)
	if _, err := client.ControlPolicy(t.Context(), changed); err == nil {
		t.Fatal("distinct intent reused an operation result")
	}
	reset := entry
	reset.Action = PolicyControlResetEntry
	reset.OperationID = "reset-entry"
	reset.Mode = ""
	reset.ExpectedRevision = policyVersionPointer(2)
	reset.ExpectedInstanceVersion = policyVersionPointer(5)
	if result, err := client.ControlPolicy(t.Context(), reset); err != nil || result.Desired.Settings.EntryMode != nil {
		t.Fatal("entry reset did not restore instance default", err)
	}
}

func TestPolicyControlAuthorityRejectsCrossScopeAndStaleConfiguration(t *testing.T) {
	request, authority := policyControlFixture()
	if err := ValidatePolicyControlAuthority(request, authority); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"caller-revoked", "caller-generation", "plugin", "group", "instance", "stage", "settings-cas", "instance-cas", "control-grant", "config-grant", "legacy", "wrong-bridge"} {
		t.Run(name, func(t *testing.T) {
			changed := authority
			switch name {
			case "caller-revoked":
				changed.CallerLive = false
			case "caller-generation":
				changed.CallerGeneration = ""
			case "plugin":
				changed.PluginID = "other-plugin"
			case "group":
				changed.ResourceGroupID = "other-group"
			case "instance":
				changed.InstanceID = "other-instance"
			case "stage":
				changed.Stage.PolicyID = "other-policy"
			case "settings-cas":
				changed.SettingsRevision++
			case "instance-cas":
				changed.InstanceVersion++
			case "control-grant":
				changed.Grants = []string{PermissionStorageWrite}
			case "config-grant":
				changed.Grants = []string{PermissionPolicyControl}
			case "legacy":
				changed.Handling = PolicyModeHandlingLegacy
			case "wrong-bridge":
				changed.Handling = PolicyModeHandlingLegacyWAF
			}
			if ValidatePolicyControlAuthority(request, changed) == nil {
				t.Fatal("unauthorized/incoherent control accepted")
			}
		})
	}
	entry := request
	entry.Action = PolicyControlReplaceEntry
	entry.Config = nil
	entry.Entry = &PolicyEntryTarget{NodeID: "local", Kind: PolicyEntryManagedUDP, ID: "ss-instance"}
	entry.Mode = PolicyModeObserve
	current := authority
	current.Entry = entry.Entry
	current.EntryAuthorized = true
	current.DefaultMode = policyModePointer(PolicyModeEnforce)
	if ValidatePolicyControlAuthority(entry, current) == nil {
		t.Fatal("entry mode weakened globally enforced default")
	}
	current.DefaultMode = policyModePointer(PolicyModeObserve)
	if err := ValidatePolicyControlAuthority(entry, current); err != nil {
		t.Fatal(err)
	}
	current.EntryAuthorized = false
	if ValidatePolicyControlAuthority(entry, current) == nil {
		t.Fatal("entry not owned by stage accepted")
	}
	current.EntryAuthorized = true
	other := *entry.Entry
	other.NodeID = "other-node"
	current.Entry = &other
	if ValidatePolicyControlAuthority(entry, current) == nil {
		t.Fatal("same entry ID on another node accepted")
	}
	current = authority
	current.Replaying = true
	current.SettingsRevision = 99
	current.InstanceVersion = 100
	if err := ValidatePolicyControlAuthority(request, current); err != nil {
		t.Fatal("authorized matching replay could not bypass stale CAS", err)
	}
	current.Grants = nil
	if ValidatePolicyControlAuthority(request, current) == nil {
		t.Fatal("historical replay bypassed current permissions")
	}
}

func TestPolicyControlStrictDecodeAndBudgets(t *testing.T) {
	request, _ := policyControlFixture()
	valid, _ := json.Marshal(request)
	if _, err := DecodePolicyControlRequest(valid); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		string(valid) + ` {}`, strings.Replace(string(valid), `"mode":"observe"`, `"mode":"observe","mode":"enforce"`, 1),
		strings.Replace(string(valid), `"action":`, `"Action":`, 1), strings.Replace(string(valid), `"kind":"ip"`, `"Kind":"ip"`, 1),
		strings.Replace(string(valid), `"mode":"observe"`, `"mode":"fail-open"`, 1),
		strings.Replace(string(valid), `"expected_revision":0,`, "", 1),
		strings.Replace(string(valid), `"expected_instance_version":3,`, "", 1),
		strings.Replace(string(valid), `"config":{`, `"config":{"duplicate":1,"duplicate":2,`, 1),
		`{"action":"inspect","stage":{"kind":"ip"},"replaying":true}`,
	} {
		if _, err := DecodePolicyControlRequest(json.RawMessage(raw)); err == nil {
			t.Fatal("malformed policy control accepted", raw)
		}
	}
	for _, change := range []func(*PolicyControlRequest){
		func(r *PolicyControlRequest) { r.Config = json.RawMessage(`null`) },
		func(r *PolicyControlRequest) {
			r.Config = json.RawMessage(`{"rules":"` + strings.Repeat("x", PolicyControlConfigMaxBytes) + `"}`)
		},
		func(r *PolicyControlRequest) { r.ExpectedRevision = policyVersionPointer(math.MaxUint64) },
		func(r *PolicyControlRequest) { r.ExpectedInstanceVersion = policyVersionPointer(0) },
		func(r *PolicyControlRequest) { r.Action = PolicyControlInspect },
		func(r *PolicyControlRequest) { r.Action = PolicyControlReplaceEntry },
		func(r *PolicyControlRequest) { r.Action = PolicyControlResetEntry },
		func(r *PolicyControlRequest) {
			r.Action = PolicyControlReplaceEntry
			r.Entry = &PolicyEntryTarget{NodeID: "n", Kind: PolicyEntryTCP, ID: "1"}
			r.Stage.Kind = PolicyOverlayStageWAF
			r.Config = nil
		},
	} {
		changed := request
		change(&changed)
		if changed.Validate() == nil {
			t.Fatal("invalid policy control passed validation")
		}
	}
}

func TestPolicyControlResponseDoesNotConfuseAcknowledgementWithApplication(t *testing.T) {
	request, _ := policyControlFixture()
	response := PolicyControlResponse{OperationID: request.OperationID, InstanceID: request.InstanceID, Stage: request.Stage, Desired: PolicySettingsSnapshot{Version: PolicySettingsVersion{Revision: 1, InstanceVersion: 4}, Settings: PolicyModeSettings{Handling: PolicyModeHandlingRaw, DefaultMode: policyModePointer(PolicyModeObserve)}}}
	if err := response.ValidateFor(request); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(response)
	for _, raw := range []string{strings.Replace(string(encoded), `"revision":1`, `"revision":1,"Revision":2`, 1), strings.Replace(string(encoded), `"default_mode":"observe"`, `"default_mode":"observe","default_mode":"enforce"`, 1), strings.Replace(string(encoded), `"handling":`, `"Handling":`, 1)} {
		if _, err := DecodePolicyControlResponse(request, json.RawMessage(raw)); err == nil {
			t.Fatal("ambiguous nested policy response accepted")
		}
		client := managedTestClient(t, func(*http.Request, HostRuntimeCall) HostRuntimeResponse {
			return HostRuntimeResponse{Payload: json.RawMessage(raw)}
		})
		if _, err := client.ControlPolicy(t.Context(), request); err == nil {
			t.Fatal("client accepted ambiguous nested policy response")
		}
	}
	for _, change := range []func(*PolicyControlResponse){func(r *PolicyControlResponse) { r.OperationID = "another-operation" }, func(r *PolicyControlResponse) { r.Desired.Version.InstanceVersion = 3 }, func(r *PolicyControlResponse) { r.Stage.PolicyID = "other" }, func(r *PolicyControlResponse) { r.Node = &PolicySettingsNodeStatus{Phase: "applied"} }} {
		changed := response
		change(&changed)
		if changed.ValidateFor(request) == nil {
			t.Fatal("invalid acknowledgement accepted")
		}
	}
	inspect := PolicyControlRequest{Action: PolicyControlInspect, InstanceID: request.InstanceID, Stage: request.Stage, Entry: &PolicyEntryTarget{NodeID: "node", Kind: PolicyEntryHTTP, ID: "1"}}
	response.OperationID = ""
	response.Entry = inspect.Entry
	old := PolicySettingsSnapshot{Version: PolicySettingsVersion{InstanceVersion: 3}, Settings: PolicyModeSettings{Handling: PolicyModeHandlingLegacy}}
	response.Node = &PolicySettingsNodeStatus{Phase: "preparing", Applied: &old, Generation: "old-generation"}
	if err := response.ValidateFor(inspect); err != nil {
		t.Fatal("truthful old applied + new desired rejected", err)
	}
	response.Node.Phase = "applied"
	if response.ValidateFor(inspect) == nil {
		t.Fatal("old snapshot labelled applied-to-desired")
	}
	response.Node.Phase = "failed"
	response.Node.Failure = "guest-failure"
	if err := response.ValidateFor(inspect); err != nil {
		t.Fatal(err)
	}
	response.Node.Failure = "arbitrary diagnostic with material"
	if response.ValidateFor(inspect) == nil {
		t.Fatal("unbounded diagnostic text accepted")
	}
}

func TestPolicyModeDecisionsAndExplicitLegacyBridge(t *testing.T) {
	stage := PolicyStageIdentity{Kind: PolicyOverlayStageIP, PolicyID: "ip"}
	for _, mode := range []PolicyMode{PolicyModeObserve, PolicyModeEnforce} {
		for _, prior := range []bool{false, true} {
			for _, outcome := range []string{PolicyCheckAllow, PolicyCheckDeny, PolicyCheckError} {
				projection := PolicyStageModeProjection{Stage: stage, Settings: PolicyModeSettings{Handling: PolicyModeHandlingRaw, DefaultMode: policyModePointer(mode)}}
				check := PolicyCheckResult{Outcome: outcome}
				if outcome == PolicyCheckError {
					check.Failure = "budget-exceeded"
				}
				result, err := ApplyPolicyMode(projection, check, prior)
				if err != nil {
					t.Fatal(err)
				}
				if prior && !result.Denied {
					t.Fatal("stage weakened previous/global denial")
				}
				if result.Denied != (prior || (mode == PolicyModeEnforce && outcome != PolicyCheckAllow)) {
					t.Fatal("incorrect observed/enforced action")
				}
				if outcome == PolicyCheckError && (result.Checked || !result.CheckFailed || result.WouldDeny || result.Failure != "budget-exceeded") {
					t.Fatal("failed check pretended to be successful/known deny")
				}
				if outcome == PolicyCheckDeny && mode == PolicyModeObserve && !result.WouldDeny {
					t.Fatal("observed deny lost would-deny")
				}
			}
		}
	}
	legacy := PolicyStageModeProjection{Stage: stage, Settings: PolicyModeSettings{Handling: PolicyModeHandlingLegacy}}
	if mode, err := ResolvePolicyMode(legacy.Settings); err != nil || mode != "" {
		t.Fatal("legacy inferred a typed mode")
	}
	if _, err := ApplyPolicyMode(legacy, PolicyCheckResult{Outcome: PolicyCheckError, Failure: "guest-failure"}, false); err == nil {
		t.Fatal("legacy failure policy silently became observe")
	}
	projection := PolicyStageModeProjection{Stage: PolicyStageIdentity{Kind: PolicyOverlayStageWAF, PolicyID: "waf"}, Settings: PolicyModeSettings{Handling: PolicyModeHandlingLegacyWAF, DefaultMode: policyModePointer(PolicyModeEnforce)}}
	envelope, err := DecodePolicyOverlay(json.RawMessage(`{"mode":"observe"}`), PolicyOverlayDecodeContext{Format: PolicyOverlayFormatLegacyWAF, LegacyPolicyID: "waf"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := PolicyOverlayForMode(envelope, projection)
	if err != nil || string(payload) != `{"mode":"deny"}` {
		t.Fatal("explicit trusted WAF bridge failed", err)
	}
	if string(envelope.Stages[0].Payload) != `{"mode":"observe"}` {
		t.Fatal("bridge mutated old stored overlay")
	}
	projection.Settings.DefaultMode = policyModePointer(PolicyModeObserve)
	payload, err = PolicyOverlayForMode(envelope, projection)
	if err != nil || string(payload) != `{"mode":"deny"}` {
		t.Fatal("typed observe did not request raw legacy-WAF deny hits", err)
	}
	observed, err := ApplyPolicyMode(projection, PolicyCheckResult{Outcome: PolicyCheckDeny}, false)
	if err != nil || observed.Denied || !observed.Checked || !observed.WouldDeny {
		t.Fatal("legacy-WAF hit was lost instead of becoming would-deny", err)
	}
	legacyWAF := projection
	legacyWAF.Settings = PolicyModeSettings{Handling: PolicyModeHandlingLegacy}
	payload, err = PolicyOverlayForMode(envelope, legacyWAF)
	if err != nil || string(payload) != `{"mode":"observe"}` {
		t.Fatal("old untyped WAF observe semantics changed", err)
	}
	projection.Settings.Handling = PolicyModeHandlingRaw
	payload, err = PolicyOverlayForMode(envelope, projection)
	if err != nil || string(payload) != `{"mode":"observe"}` {
		t.Fatal("raw mode implementation guessed opaque payload mode", err)
	}
	projection.Stage.PolicyID = "other"
	if _, err := PolicyOverlayForMode(envelope, projection); err == nil {
		t.Fatal("mode handling selected another policy's overlay")
	}
}

func TestPolicyDefaultBundleGateAndManifestModeHandling(t *testing.T) {
	update := PolicyDefaultSettingsUpdate{Stage: PolicyStageIdentity{Kind: "ip"}, Mode: PolicyModeObserve, ExpectedRevision: 0}
	stage := PolicyStageIdentity{Kind: "ip", PolicyID: "owned"}
	if err := ValidatePolicyDefaultSettingsUpdate(update, stage, PolicyModeHandlingRaw, 0); err != nil {
		t.Fatal(err)
	}
	if ValidatePolicyDefaultSettingsUpdate(update, stage, PolicyModeHandlingLegacy, 0) == nil || ValidatePolicyDefaultSettingsUpdate(update, stage, PolicyModeHandlingLegacyWAF, 0) == nil || ValidatePolicyDefaultSettingsUpdate(update, stage, PolicyModeHandlingRaw, 1) == nil {
		t.Fatal("atomic binding bundle bypassed handling or policy CAS")
	}
	manifest := Manifest{Runtime: Runtime{Kind: RuntimeWASMPolicy, ABI: PolicyABIV1, HostScope: HostScopeAgent, PolicyKind: "ip"}}
	if handling, err := PolicyModeHandlingForManifest(manifest); err != nil || handling != PolicyModeHandlingLegacy {
		t.Fatal("old manifest changed semantics")
	}
	manifest.Metadata = map[string]string{PolicyModeHandlingMetadataKey: string(PolicyModeHandlingRaw)}
	if handling, err := PolicyModeHandlingForManifest(manifest); err != nil || handling != PolicyModeHandlingRaw {
		t.Fatal(err)
	}
	manifest.Metadata[PolicyModeHandlingMetadataKey] = string(PolicyModeHandlingLegacyWAF)
	if _, err := PolicyModeHandlingForManifest(manifest); err == nil {
		t.Fatal("IP impersonated legacy WAF bridge")
	}
	manifest.Metadata[PolicyModeHandlingMetadataKey] = "fail-open"
	if _, err := PolicyModeHandlingForManifest(manifest); err == nil {
		t.Fatal("static failure policy impersonated mode handling")
	}
	manifest.Metadata[PolicyModeHandlingMetadataKey] = ""
	if _, err := PolicyModeHandlingForManifest(manifest); err == nil {
		t.Fatal("explicitly empty declaration became legacy")
	}
}
