//go:build !integration

package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/wasm/testfixture"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// This observer only records the production runtime's result. It does not
// implement any Host import, authorization decision or dataset lookup.
type consumptionFactory struct {
	GenerationFactory
	responses map[string]policy.ModuleResponse
	errors    map[string]error
}

func (f *consumptionFactory) PrepareGeneration(ctx context.Context, spec policy.GenerationSpec) (policy.GenerationRuntime, error) {
	runtime, err := f.GenerationFactory.PrepareGeneration(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &consumptionRuntime{GenerationRuntime: runtime, factory: f}, nil
}

type consumptionRuntime struct {
	policy.GenerationRuntime
	factory *consumptionFactory
}

func (r *consumptionRuntime) Evaluate(ctx context.Context, request policy.ModuleRequest) (policy.ModuleResponse, error) {
	response, err := r.GenerationRuntime.Evaluate(ctx, request)
	r.factory.responses[request.GenerationID] = response
	r.factory.errors[request.GenerationID] = err
	return response, err
}

func TestWASMConsumesGenerationDatasetAndAuthenticatedSource(t *testing.T) {
	runtime, err := NewRuntime(t.Context(), RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	factory := &consumptionFactory{GenerationFactory: GenerationFactory{Runtime: runtime}, responses: map[string]policy.ModuleResponse{}, errors: map[string]error{}}
	registry := module.NewRegistry()
	if err := registry.Register(policy.NewModule(factory, nil)); err != nil {
		t.Fatal(err)
	}
	prepare := func(previous, next model.Snapshot) (*module.GenerationView, error) {
		generation, err := module.NewGenerationContext(previous, next)
		if err != nil {
			return nil, err
		}
		candidate, err := registry.PrepareGeneration(t.Context(), generation)
		if err != nil {
			return nil, err
		}
		if err := candidate.Ready(t.Context()); err != nil {
			_ = candidate.Destroy(t.Context())
			return nil, err
		}
		view, _ := candidate.Publish()
		t.Cleanup(func() { _ = view.Destroy(context.Background()) })
		return view, nil
	}
	evaluate := func(view *module.GenerationView, entry string) policy.Decision {
		metadata, err := policy.NewDirectMetadata(&net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 12345})
		if err != nil {
			t.Fatal(err)
		}
		body, err := policy.NewBodyWindow(nil, true, policy.BodyNotSkipped)
		if err != nil {
			t.Fatal(err)
		}
		input, err := policy.NewInput(policy.ExtensionL4, "actual-entry-call", metadata, nil, body)
		if err != nil {
			t.Fatal(err)
		}
		provider, ok := view.Resolve(policy.ProviderEvaluator)
		if !ok {
			t.Fatal("generation did not publish its policy evaluator")
		}
		return provider.(policy.Evaluator).Evaluate(t.Context(), &model.PolicyRef{ID: "shared"}, input.WithEntryID(entry))
	}
	first := consumptionSnapshot(t, 1, "192.0.2.0/24", consumptionGuest(t, "regions", "cn-44", nil, 512, false))
	firstView, err := prepare(model.Snapshot{}, first)
	if err != nil {
		t.Fatal(err)
	}
	assertResult := func(view *module.GenerationView, snapshot model.Snapshot, matched bool) sdk.DatasetReference {
		t.Helper()
		decision := evaluate(view, "tcp-entry-7")
		// Like the multi-stage generation fixture, retry only scheduling
		// deadlines. A successful real guest result is still required below.
		for attempt := 1; goruntime.GOOS == "windows" && attempt < 20; attempt++ {
			var failure *policy.EvaluationError
			if !errors.As(factory.errors[view.ID()], &failure) || failure.Kind != policy.FailureBudget || failure.Code != string(ErrorDeadline) || decision.Action != policy.ActionDeny || !decision.Degraded {
				break
			}
			goruntime.Gosched()
			decision = evaluate(view, "tcp-entry-7")
		}
		if decision.Action != policy.ActionAllow || decision.Degraded {
			t.Fatalf("real guest evaluation = %+v", decision)
		}
		payload := factory.responses[view.ID()].Payload
		resolveStatus, resolveFrame := consumptionSlot(t, payload, 0)
		resolve, err := sdk.UnmarshalPolicyDatasetResolveResponse(resolveFrame, consumptionResolveRequest("regions"))
		if resolveStatus != sdk.PolicyStatusOK || err != nil || resolve.Reference == nil {
			t.Fatalf("guest init resolve: status=%v response=%+v error=%v", resolveStatus, resolve, err)
		}
		ref := *resolve.Reference
		if ref.Generation != view.ID() || ref.InstanceID != "ip-instance" || ref.VersionDigest != snapshot.Datasets[0].Version.Digest {
			t.Fatalf("init resolved outside its immutable generation: %+v", ref)
		}
		queryStatus, queryFrame := consumptionSlot(t, payload, 1)
		query, err := sdk.UnmarshalPolicyDatasetQueryResponse(queryFrame, consumptionQuery(ref, "cn-44"))
		if queryStatus != sdk.PolicyStatusOK || err != nil || query.Status != sdk.DatasetQueryOK || len(query.Matches) != 1 || query.Matches[0].Matched != matched {
			t.Fatalf("guest source query: status=%v response=%+v error=%v", queryStatus, query, err)
		}
		sourceStatus, sourceFrame := consumptionSlot(t, payload, 2)
		source, err := sdk.UnmarshalPolicyTrustedSourceResponse(sourceFrame, 4096)
		if sourceStatus != sdk.PolicyStatusOK || err != nil || source.Source == nil {
			t.Fatalf("guest source read: status=%v response=%+v error=%v", sourceStatus, source, err)
		}
		if err := source.Source.ValidateFor("ip-instance", view.ID(), "tcp-entry-7"); err != nil || source.Source.SourceAddress.String() != "192.0.2.1" || source.Source.PeerAddress != source.Source.SourceAddress || source.Source.Authority != sdk.PolicySourceSocket {
			t.Fatalf("guest received an unbound or fabricated source: %+v error=%v", source.Source, err)
		}
		return ref
	}
	oldRef := assertResult(firstView, first, true)
	second := consumptionSnapshot(t, 2, "198.51.100.0/24", consumptionGuest(t, "regions", "cn-44", nil, 512, false))
	secondView, err := prepare(first, second)
	if err != nil {
		t.Fatal(err)
	}
	newRef := assertResult(secondView, second, false)
	if oldRef.Handle == newRef.Handle || oldRef.VersionDigest == newRef.VersionDigest {
		t.Fatal("different generation indices collapsed to a shared/latest reference")
	}
	// Existing sessions still use the old instance and its init-time handle.
	if again := assertResult(firstView, first, true); again != oldRef {
		t.Fatal("reused old guest resolved a new reference after publication")
	}
	watchdogDenied := func(t *testing.T, view *module.GenerationView, decision policy.Decision) bool {
		t.Helper()
		// Windows scheduling can let the production 2 ms watchdog win the
		// race with a rejected import. Require the explicit budget failure;
		// other platforms still require the precise guest import statuses.
		if goruntime.GOOS != "windows" || len(factory.responses[view.ID()].Payload) != 0 {
			return false
		}
		var failure *policy.EvaluationError
		if !errors.As(factory.errors[view.ID()], &failure) || failure.Kind != policy.FailureBudget || decision.Action != policy.ActionDeny || !decision.Degraded {
			t.Fatalf("call disappeared without a visible budget denial: decision=%+v error=%v", decision, factory.errors[view.ID()])
		}
		t.Log("production watchdog rejected the call before the guest returned import statuses")
		return true
	}
	t.Run("missing connection entry", func(t *testing.T) {
		decision := evaluate(secondView, "")
		payload := factory.responses[secondView.ID()].Payload
		if watchdogDenied(t, secondView, decision) {
			return
		}
		queryStatus, _ := consumptionSlot(t, payload, 1)
		sourceStatus, _ := consumptionSlot(t, payload, 2)
		if queryStatus == sdk.PolicyStatusOK || sourceStatus == sdk.PolicyStatusOK || decision.Action != policy.ActionDeny || !decision.Degraded {
			t.Fatalf("entry-less source consumption succeeded: query=%v source=%v decision=%+v", queryStatus, sourceStatus, decision)
		}
	})
	for _, test := range []struct {
		name         string
		source       string
		missingGrant string
		initSource   bool
		missingClass bool
		wantError    string
	}{
		{name: "unknown source", source: "unbound", wantError: "initialize artifact"},
		{name: "missing resolve grant", source: "regions", missingGrant: string(sdk.CapabilityDatasetResolve), wantError: "policy import admission"},
		{name: "missing query grant", source: "regions", missingGrant: string(sdk.CapabilityDatasetQuery), wantError: "policy import admission"},
		{name: "missing source grant", source: "regions", missingGrant: string(sdk.CapabilityPolicyTrustedSource), wantError: "policy import admission"},
		{name: "source read during initialization", source: "regions", initSource: true, wantError: "initialize artifact"},
		{name: "selected class unavailable", source: "regions", missingClass: true, wantError: "classification"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := consumptionSnapshot(t, 3, "198.51.100.0/24", consumptionGuest(t, test.source, "cn-44", nil, 512, test.initSource))
			if test.missingClass {
				candidate.Datasets[0].Bindings[0].Classifications[0].Name = "cn-11"
			}
			if test.missingGrant != "" {
				stage := &candidate.PluginPolicies[0].Stages[0]
				grants := []string{}
				for _, grant := range stage.GrantedScopes {
					if grant != test.missingGrant {
						grants = append(grants, grant)
					}
				}
				stage.GrantedScopes = grants
			}
			if _, err := prepare(second, candidate); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("required guest failed outside the expected admission boundary %q: %v", test.wantError, err)
			}
			if registry.ActiveGeneration() != secondView {
				t.Fatal("failed guest initialization replaced the active generation")
			}
		})
	}
	staleFrame, err := sdk.MarshalPolicyDatasetQueryRequest(consumptionQuery(oldRef, "cn-44"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, class string
		override    []byte
		capacity    int
		wantStatus  sdk.PolicyStatus
	}{
		{name: "ungranted classification", class: "cn-11", capacity: 512, wantStatus: sdk.PolicyStatusOK},
		{name: "old generation reference", class: "cn-44", override: staleFrame, capacity: 512, wantStatus: sdk.PolicyStatusInvalidArgument},
		{name: "host output budget", class: "cn-44", capacity: 4097, wantStatus: sdk.PolicyStatusResourceExhausted},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := consumptionSnapshot(t, 4, "198.51.100.0/24", consumptionGuest(t, "regions", test.class, test.override, test.capacity, false))
			view, err := prepare(second, candidate)
			if err != nil {
				t.Fatal(err)
			}
			decision := evaluate(view, "tcp-entry-7")
			payload := factory.responses[view.ID()].Payload
			if watchdogDenied(t, view, decision) {
				return
			}
			status, frame := consumptionSlot(t, payload, 1)
			if status != test.wantStatus {
				t.Fatalf("guest query status=%v, want %v", status, test.wantStatus)
			}
			if status != sdk.PolicyStatusOK {
				if decision.Action != policy.ActionDeny || !decision.Degraded {
					t.Fatalf("ignored Host failure escaped enforcement: %+v", decision)
				}
				return
			}
			_, resolveFrame := consumptionSlot(t, payload, 0)
			resolve, err := sdk.UnmarshalPolicyDatasetResolveResponse(resolveFrame, consumptionResolveRequest("regions"))
			if err != nil || resolve.Reference == nil {
				t.Fatalf("resolve=%+v err=%v", resolve, err)
			}
			response, err := sdk.UnmarshalPolicyDatasetQueryResponse(frame, consumptionQuery(*resolve.Reference, test.class))
			if err != nil || response.Status != sdk.DatasetQueryUnauthorized || len(response.Matches) != 0 {
				t.Fatalf("unbound class became a normal miss: %+v err=%v", response, err)
			}
			if decision.Action != policy.ActionDeny || !decision.Degraded {
				t.Fatalf("non-OK dataset result with transport OK escaped typed enforcement: %+v", decision)
			}
		})
	}
}

func consumptionResolveRequest(source string) sdk.PolicyDatasetResolveRequest {
	return testfixture.ResolveRequest(source)
}

func consumptionQuery(ref sdk.DatasetReference, class string) sdk.PolicyDatasetQueryRequest {
	return testfixture.QueryRequest(ref, class)
}

func consumptionSlot(t *testing.T, payload []byte, index int) (sdk.PolicyStatus, []byte) {
	t.Helper()
	if len(payload) != 3*520 {
		t.Fatalf("guest did not return all three executed import records: %d bytes", len(payload))
	}
	slot := payload[index*520 : (index+1)*520]
	result := binary.LittleEndian.Uint64(slot)
	length := uint32(result)
	if length > 512 {
		t.Fatalf("guest import returned oversized result: %d", length)
	}
	return sdk.PolicyStatus(result >> 32), slot[8 : 8+length]
}

func consumptionSnapshot(t *testing.T, revision int64, prefix string, wasmBytes []byte) model.Snapshot {
	t.Helper()
	data, err := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: []datasets.CIDRClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, DisplayName: "广东省", CIDRs: []string{prefix}}}})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	index, err := datasets.Compile(t.Context(), datasets.Input{Source: sdk.DatasetSource{ID: "regions", Name: "Region fixture", Format: sdk.DatasetFormatCIDR}, Revision: prefix, FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + hex.EncodeToString(sum[:]), Data: data}, datasets.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := index.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	indexSum, wasmSum := sha256.Sum256(encoded), sha256.Sum256(wasmBytes)
	indexPath, wasmPath := filepath.Join(t.TempDir(), "index.nredataset"), filepath.Join(t.TempDir(), "policy.wasm")
	if err := os.WriteFile(indexPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wasmPath, wasmBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	stage := fixturePolicyStage("ip-instance", model.PolicyKindIP, wasmPath, hex.EncodeToString(wasmSum[:]))
	stage.ExtensionPoints = []string{policy.ExtensionL4}
	stage.ResourceGroupID = "default"
	stage.DeclaredScopes = []string{string(sdk.CapabilityDatasetResolve), string(sdk.CapabilityDatasetQuery), string(sdk.CapabilityPolicyTrustedSource)}
	stage.GrantedScopes = append([]string(nil), stage.DeclaredScopes...)
	mode := sdk.PolicyModeEnforce
	stage.PolicySettings = &sdk.PolicySettingsSnapshot{Version: sdk.PolicySettingsVersion{Revision: 1, InstanceVersion: 1}, Settings: sdk.PolicyModeSettings{Handling: sdk.PolicyModeHandlingRaw, DefaultMode: &mode}}
	return model.Snapshot{Revision: revision, L4Rules: []model.L4Rule{{ID: 7, Enabled: true, PolicyRef: &model.PolicyRef{ID: "shared"}}},
		PluginPolicies: []model.PluginPolicy{{ID: "shared", Revision: revision, Stages: []model.PolicyStage{stage}}},
		Datasets:       []model.DatasetSnapshot{{Version: index.Version(), Artifact: model.DatasetArtifact{ID: "dataset-" + hex.EncodeToString(indexSum[:]), Kind: model.DatasetArtifactKind, SHA256: hex.EncodeToString(indexSum[:]), SizeBytes: int64(len(encoded)), LocalPath: indexPath}, Bindings: []model.DatasetInstanceBinding{{InstanceID: "ip-instance", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}}}},
	}
}

func consumptionGuest(t *testing.T, source, class string, queryOverride []byte, queryCapacity int, sourceDuringInit bool) []byte {
	t.Helper()
	guest, err := testfixture.ConsumptionGuest(source, class, queryOverride, queryCapacity, sourceDuringInit)
	if err != nil {
		t.Fatal(err)
	}
	return guest
}
