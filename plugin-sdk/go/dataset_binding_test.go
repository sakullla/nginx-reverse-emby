package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type datasetBindingHostFunc func(context.Context, DatasetBindingAuthorization, DatasetBindingRequest) (DatasetBindingResponse, error)

func (fn datasetBindingHostFunc) ManageDatasetBinding(ctx context.Context, auth DatasetBindingAuthorization, request DatasetBindingRequest) (DatasetBindingResponse, error) {
	return fn(ctx, auth, request)
}
func bindingFixture() (DatasetBindingRequest, DatasetBindingAuthorization) {
	version := datasetTestVersion()
	spec := DatasetBindingSpec{VersionDigest: version.Digest, Classifications: datasetTestQuery().Classifications}
	request := DatasetBindingRequest{Action: DatasetBindingBind, OperationID: "operation", InstanceID: "execution", SourceID: version.SourceID, Targets: ExecutionTargetSelection{Mode: ExecutionTargetsEffective}, Spec: &spec}
	auth := DatasetBindingAuthorization{CallerPluginID: "plugin", CallerInstanceID: "management", CallerGeneration: "management-generation", ResourceGroupID: "group", TargetPluginID: "plugin", TargetInstanceID: "execution", TargetResourceGroupID: "group", DeclaredScopes: []string{string(CapabilityDatasetBind)}, GrantedScopes: []string{string(CapabilityDatasetBind)}, EffectiveAgentIDs: []string{"local", "remote"}, GrantedAgentIDs: []string{"local", "remote"}, SourceIDs: []string{version.SourceID}, Version: &version, Catalog: spec.Classifications}
	return request, auth
}
func bindingCall(request DatasetBindingRequest) HostRuntimeCall {
	payload, _ := json.Marshal(request)
	return HostRuntimeCall{Operation: HostRuntimeDatasetBinding, OperationID: request.OperationID, Payload: payload}
}
func bindingAck(auth DatasetBindingAuthorization, request DatasetBindingRequest) DatasetBindingResponse {
	response := DatasetBindingResponse{OperationID: request.OperationID, InstanceID: request.InstanceID, SourceID: request.SourceID, Revision: auth.Revision + 1, Targets: []DatasetBindingTargetStatus{}}
	if request.Spec != nil {
		response.Desired = &DatasetBindingRecord{InstanceID: request.InstanceID, SourceID: request.SourceID, Revision: response.Revision, Targets: request.Targets, Spec: *request.Spec}
	}
	targets, _ := ResolveExecutionTargets(request.Targets, auth.EffectiveAgentIDs, auth.GrantedAgentIDs)
	for _, id := range targets {
		status := DatasetBindingTargetStatus{AgentID: id, State: "pending", Desired: request.Spec}
		if auth.Current != nil {
			status.Applied = &auth.Current.Spec
			status.LastGood = &auth.Current.Spec
			status.Generation = "actual-old-generation"
			status.ConfigRevision = 7
		}
		response.Targets = append(response.Targets, status)
	}
	if request.InstanceUpdate != nil {
		response.InstanceRevision = auth.InstanceRevision + 1
		if request.InstanceUpdate.PolicyDefaults != nil {
			response.PolicyRevision = auth.PolicyRevision + 1
		}
	}
	return response
}
func TestDatasetBindingClientHostRoundtripAndDurableReplay(t *testing.T) {
	request, auth := bindingFixture()
	calls := 0
	host := datasetBindingHostFunc(func(ctx context.Context, a DatasetBindingAuthorization, r DatasetBindingRequest) (DatasetBindingResponse, error) {
		calls++
		return bindingAck(a, r), nil
	})
	client := managedTestClient(t, func(httpRequest *http.Request, call HostRuntimeCall) HostRuntimeResponse {
		response, err := CallDatasetBindingHost(httpRequest.Context(), host, auth, call)
		if err != nil {
			t.Error(err)
			return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorInvalidArgument, Message: "binding denied"}}
		}
		encoded, _ := json.Marshal(response)
		return HostRuntimeResponse{Payload: encoded}
	})
	response, err := client.ManageDatasetBinding(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || response.Targets[0].AgentID != "local" || response.Targets[0].Applied != nil || response.Targets[0].State != "pending" {
		t.Fatal("desired binding was confused with node application")
	}
	digest, _ := DatasetBindingRequestDigest(request)
	persisted, _ := json.Marshal(DatasetBindingReplay{RequestDigest: digest, Response: response, ResolvedAgentIDs: append([]string(nil), auth.EffectiveAgentIDs...)})
	var replay DatasetBindingReplay
	if err := json.Unmarshal(persisted, &replay); err != nil {
		t.Fatal(err)
	}
	auth.Current = response.Desired
	auth.Revision = response.Revision
	auth.Replay = &replay
	second, err := client.ManageDatasetBinding(t.Context(), request)
	if err != nil || second.OperationID != request.OperationID || calls != 1 {
		t.Fatal("restart replay repeated mutation", err)
	}
	changed := request
	changed.Targets = ExecutionTargetSelection{Mode: ExecutionTargetsSubset, AgentIDs: []string{"local"}}
	if _, err := CallDatasetBindingHost(t.Context(), host, auth, bindingCall(changed)); err == nil {
		t.Fatal("operation ID accepted changed binding")
	}
	auth.Replay = nil
	request.Action = DatasetBindingUnbind
	request.ExpectedRevision = auth.Revision
	request.OperationID = "unbind"
	request.Spec = nil
	removed, err := CallDatasetBindingHost(t.Context(), host, auth, bindingCall(request))
	if err != nil {
		t.Fatal(err)
	}
	if removed.Desired != nil || removed.Targets[0].Applied == nil || removed.Targets[0].State != "pending" {
		t.Fatal("removal claimed applied before node ACK")
	}
}
func TestDatasetBindingScopeCatalogAndCASRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(*DatasetBindingRequest, *DatasetBindingAuthorization)
	}{
		{"grant", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) { a.GrantedScopes = nil }},
		{"declaration", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) { a.DeclaredScopes = nil }},
		{"plugin", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) { a.TargetPluginID = "foreign" }},
		{"instance", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) { r.InstanceID = "foreign" }},
		{"group", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) { a.TargetResourceGroupID = "foreign" }},
		{"target", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) {
			r.Targets = ExecutionTargetSelection{Mode: ExecutionTargetsSubset, AgentIDs: []string{"foreign"}}
		}},
		{"source", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) { a.SourceIDs = nil }},
		{"classification", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) { a.Catalog = nil }},
		{"version", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) { a.Version = nil }},
		{"CAS", func(r *DatasetBindingRequest, a *DatasetBindingAuthorization) {
			r.Action = DatasetBindingReplace
			r.ExpectedRevision = 3
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request, auth := bindingFixture()
			test.change(&request, &auth)
			called := false
			host := datasetBindingHostFunc(func(context.Context, DatasetBindingAuthorization, DatasetBindingRequest) (DatasetBindingResponse, error) {
				called = true
				return DatasetBindingResponse{}, nil
			})
			if _, err := CallDatasetBindingHost(t.Context(), host, auth, bindingCall(request)); err == nil || called {
				t.Fatal("invalid authority reached mutation")
			}
		})
	}
	request, auth := bindingFixture()
	call := bindingCall(request)
	call.OperationID = "different"
	if _, err := CallDatasetBindingHost(t.Context(), nil, auth, call); err == nil {
		t.Fatal("outer operation identity mismatch accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CallDatasetBindingHost(ctx, nil, auth, bindingCall(request)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	for _, payload := range []string{`{"action":"inspect","action":"bind"}`, `{} {}`, `{"unknown":true}`, strings.Repeat(" ", DatasetBindingMaxFrameBytes+1)} {
		if _, err := DecodeDatasetBindingRequest([]byte(payload)); err == nil {
			t.Fatal("invalid or oversized frame accepted")
		}
	}
}
func TestDatasetBindingRejectsDishonestOrForeignResponses(t *testing.T) {
	request, auth := bindingFixture()
	for _, change := range []func(*DatasetBindingResponse){func(r *DatasetBindingResponse) { r.OperationID = "foreign" }, func(r *DatasetBindingResponse) { r.Targets[0].AgentID = "foreign" }, func(r *DatasetBindingResponse) { r.Targets[0].State = "applied" }, func(r *DatasetBindingResponse) { r.Targets[0].State = "failed" }, func(r *DatasetBindingResponse) { r.Desired.SourceID = "foreign" }} {
		host := datasetBindingHostFunc(func(ctx context.Context, a DatasetBindingAuthorization, r DatasetBindingRequest) (DatasetBindingResponse, error) {
			response := bindingAck(a, r)
			change(&response)
			return response, nil
		})
		if _, err := CallDatasetBindingHost(t.Context(), host, auth, bindingCall(request)); err == nil {
			t.Fatal("dishonest binding response accepted")
		}
	}
}
func TestDatasetBindingAtomicConfigAndPolicyDefaults(t *testing.T) {
	request, auth := bindingFixture()
	old := *request.Spec
	old.VersionDigest = "sha256:" + strings.Repeat("3", 64)
	old.Classifications = []DatasetClassification{{Kind: DatasetClassificationDomain, Name: "old-only"}}
	request.Action = DatasetBindingReplace
	request.ExpectedRevision = 5
	auth.Revision = 5
	auth.Current = &DatasetBindingRecord{InstanceID: request.InstanceID, SourceID: request.SourceID, Revision: 5, Targets: request.Targets, Spec: old}
	stage := PolicyStageIdentity{Kind: PolicyOverlayStageIP, PolicyID: "ip-policy"}
	auth.PolicyStage = &stage
	auth.PolicyModeHandling = PolicyModeHandlingRaw
	auth.PolicyRevision = 2
	auth.InstanceRevision = 7
	auth.DeclaredScopes = append(auth.DeclaredScopes, "storage.write", string(CapabilityPolicyControl))
	auth.GrantedScopes = append([]string(nil), auth.DeclaredScopes...)
	request.InstanceUpdate = &DatasetBindingInstanceUpdate{ExpectedRevision: 7, Config: json.RawMessage(`{"classification":"new-only"}`), PolicyDefaults: &PolicyDefaultSettingsUpdate{Stage: stage, Mode: PolicyModeObserve, ExpectedRevision: 2}}
	called := 0
	host := datasetBindingHostFunc(func(ctx context.Context, a DatasetBindingAuthorization, r DatasetBindingRequest) (DatasetBindingResponse, error) {
		called++
		if r.Spec.VersionDigest == a.Current.Spec.VersionDigest || r.InstanceUpdate.Config == nil || r.InstanceUpdate.PolicyDefaults == nil {
			t.Fatal("atomic update split into intermediate mutation")
		}
		return bindingAck(a, r), nil
	})
	for _, mutate := range []func(*DatasetBindingAuthorization){func(a *DatasetBindingAuthorization) { a.InstanceRevision++ }, func(a *DatasetBindingAuthorization) { a.PolicyRevision++ }, func(a *DatasetBindingAuthorization) { a.GrantedScopes = []string{string(CapabilityDatasetBind)} }, func(a *DatasetBindingAuthorization) { a.PolicyModeHandling = PolicyModeHandlingLegacy }} {
		bad := auth
		mutate(&bad)
		if _, err := CallDatasetBindingHost(t.Context(), host, bad, bindingCall(request)); err == nil || called != 0 {
			t.Fatal("failed bundled authority/CAS reached mutation")
		}
	}
	response, err := CallDatasetBindingHost(t.Context(), host, auth, bindingCall(request))
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || response.InstanceRevision != 8 || response.PolicyRevision != 3 || response.Revision != 6 {
		t.Fatal("atomic versions not acknowledged")
	}
	digest, _ := DatasetBindingRequestDigest(request)
	auth.Replay = &DatasetBindingReplay{RequestDigest: digest, Response: response, ResolvedAgentIDs: append([]string(nil), auth.EffectiveAgentIDs...)}
	auth.Current = response.Desired
	auth.Revision = 6
	auth.InstanceRevision = 8
	auth.PolicyRevision = 3
	if _, err := CallDatasetBindingHost(t.Context(), host, auth, bindingCall(request)); err != nil || called != 1 {
		t.Fatal("bundle replay repeated mutation or compared obsolete CAS", err)
	}
}
func TestDatasetBindingStrictResponseAndFailedStatus(t *testing.T) {
	request, auth := bindingFixture()
	response := bindingAck(auth, request)
	response.Targets[0].State = "failed"
	response.Targets[0].Error = &RuntimeError{Code: ErrorUnavailable, Message: "dataset preparation failed"}
	raw, _ := json.Marshal(response)
	if _, err := DecodeDatasetBindingResponse(request, raw); err != nil {
		t.Fatal("truthful failed status rejected", err)
	}
	for _, payload := range []string{strings.Replace(string(raw), `"instance_id"`, `"Instance_ID"`, 1), strings.Replace(string(raw), `"revision":1`, `"revision":1,"revision":2`, 1), strings.Replace(string(raw), `"state":"failed"`, `"state":"failed","unknown":true`, 1)} {
		if _, err := DecodeDatasetBindingResponse(request, []byte(payload)); err == nil {
			t.Fatal("noncanonical response accepted")
		}
	}
	request.Spec.Classifications = append(request.Spec.Classifications, request.Spec.Classifications[0])
	if request.Validate() == nil {
		t.Fatal("duplicate binding selector accepted")
	}
}
func TestDatasetBindingAndPolicyControlCapabilityNegotiation(t *testing.T) {
	for _, capability := range []HostCapability{CapabilityDatasetBind, CapabilityPolicyControl} {
		manifest := Manifest{Runtime: Runtime{Kind: RuntimeRPCService, HostScope: HostScopeControlPlane}, Permissions: []Permission{{Name: string(capability)}}}
		if ValidateManifestManagedCapabilities(manifest, nil) == nil {
			t.Fatal("old Host admitted unsupported consumption capability")
		}
		if err := ValidateManifestManagedCapabilities(manifest, []HostCapability{capability}); err != nil {
			t.Fatal(err)
		}
		features := RequiredRPCFeatures([]string{string(capability)})
		if len(features) != 1 || ValidateRPCFeatures(features, nil) == nil {
			t.Fatal("missing feature acknowledgement accepted")
		}
		manifest.Runtime.Kind = RuntimeWASMPolicy
		if ValidateManifestManagedCapabilities(manifest, []HostCapability{capability}) == nil {
			t.Fatal("WASM guest gained management capability")
		}
	}
}
func TestDatasetBindingHistoricalReplayDoesNotRequireDeletedVersion(t *testing.T) {
	request, auth := bindingFixture()
	response := bindingAck(auth, request)
	digest, _ := DatasetBindingRequestDigest(request)
	auth.Replay = &DatasetBindingReplay{RequestDigest: digest, Response: response, ResolvedAgentIDs: []string{"local", "remote"}}
	// The instance later removed this binding, moved targets and deleted the
	// unused old version. This retry may only return its original durable ACK.
	auth.Revision = 9
	auth.Current = nil
	auth.Version = nil
	auth.Catalog = nil
	auth.EffectiveAgentIDs = []string{"new-node"}
	auth.GrantedAgentIDs = []string{"local", "remote", "new-node"}
	replay, err := CallDatasetBindingHost(t.Context(), nil, auth, bindingCall(request))
	if err != nil || replay.Revision != response.Revision || len(replay.Targets) != 2 || replay.Targets[0].AgentID != "local" {
		t.Fatal("historical replay required deleted candidate data or changed targets", err)
	}
	auth.GrantedScopes = nil
	if _, err := CallDatasetBindingHost(t.Context(), nil, auth, bindingCall(request)); err == nil {
		t.Fatal("historical replay bypassed current capability revocation")
	}
}

func TestDatasetBindingCatalogIdentityPreservesAttributePredicatesRoundtrip(t *testing.T) {
	yes, no, rank := true, false, int64(7)
	const name = "category-ai-!cn"
	cases := []struct {
		name            string
		selector        DatasetClassification
		wantError       bool
		invalidSelector bool
	}{
		{name: "literal negated key boolean", selector: DatasetClassification{Name: name, Kind: DatasetClassificationDomain, Attributes: []DatasetAttribute{{Name: "!cn", Boolean: &yes}}}},
		{name: "false boolean", selector: DatasetClassification{Name: name, Kind: DatasetClassificationDomain, Attributes: []DatasetAttribute{{Name: "cn", Boolean: &no}}}},
		{name: "integer", selector: DatasetClassification{Name: name, Kind: DatasetClassificationDomain, Attributes: []DatasetAttribute{{Name: "rank", Integer: &rank}}}},
		{name: "negated boolean", selector: DatasetClassification{Name: name, Kind: DatasetClassificationDomain, Attributes: []DatasetAttribute{{Name: "!cn", Boolean: &yes, Negate: true}}}},
		{name: "negated integer conjunction", selector: DatasetClassification{Name: name, Kind: DatasetClassificationDomain, Attributes: []DatasetAttribute{{Name: "!cn", Boolean: &yes}, {Name: "rank", Integer: &rank, Negate: true}}}},
		{name: "unknown name", selector: DatasetClassification{Name: "missing", Kind: DatasetClassificationDomain}, wantError: true},
		{name: "different kind", selector: DatasetClassification{Name: name, Kind: DatasetClassificationCountry}, wantError: true},
		{name: "invalid typed predicate", selector: DatasetClassification{Name: name, Kind: DatasetClassificationDomain, Attributes: []DatasetAttribute{{Name: "rank", Boolean: &yes, Integer: &rank}}}, wantError: true, invalidSelector: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request, auth := bindingFixture()
			request.Spec.Classifications = []DatasetClassification{test.selector}
			// Index.Catalog enumerates classification identity only. Attributes are
			// query predicates, not part of this immutable catalog's identity rows.
			auth.Catalog = []DatasetClassification{{Name: name, Kind: DatasetClassificationDomain}}
			mutations, transportCalls := 0, 0
			host := datasetBindingHostFunc(func(ctx context.Context, a DatasetBindingAuthorization, r DatasetBindingRequest) (DatasetBindingResponse, error) {
				mutations++
				if !reflect.DeepEqual(r.Spec.Classifications, request.Spec.Classifications) {
					t.Error("Host received altered typed predicates")
				}
				return bindingAck(a, r), nil
			})
			client := managedTestClient(t, func(httpRequest *http.Request, call HostRuntimeCall) HostRuntimeResponse {
				transportCalls++
				response, err := CallDatasetBindingHost(httpRequest.Context(), host, auth, call)
				if err != nil {
					return HostRuntimeResponse{Error: &RuntimeError{Code: ErrorInvalidArgument, Message: "binding rejected"}}
				}
				encoded, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				return HostRuntimeResponse{Payload: encoded}
			})
			response, err := client.ManageDatasetBinding(t.Context(), request)
			if test.wantError {
				if err == nil || mutations != 0 {
					t.Fatal("invalid selector reached binding mutation")
				}
			} else {
				if err != nil {
					t.Fatal("catalog identity rejected valid attribute predicates:", err)
				}
				if mutations != 1 || response.Desired == nil || !reflect.DeepEqual(response.Desired.Spec.Classifications, request.Spec.Classifications) {
					t.Fatal("binding acknowledgement lost typed predicates")
				}
			}
			expectedTransport := 1
			if test.invalidSelector {
				expectedTransport = 0
			}
			if transportCalls != expectedTransport {
				t.Fatalf("transport calls = %d, want %d", transportCalls, expectedTransport)
			}
		})
	}
}
