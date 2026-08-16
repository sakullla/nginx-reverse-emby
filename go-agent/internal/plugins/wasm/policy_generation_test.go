//go:build !integration

package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
)

func TestPolicyGenerationPreparesMultipleStages(t *testing.T) {
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	artifactPath, digest := writePolicyFixture(t)
	first := fixturePolicyStage("instance-ip", model.PolicyKindIP, artifactPath, digest)
	second := fixturePolicyStage("instance-waf", model.PolicyKindWAF, artifactPath, digest)
	generation, err := PreparePolicyGeneration(ctx, runtime, "compat-generation-1", []model.PolicyStage{first, second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = generation.Close(context.Background()) })
	if err := generation.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []model.PolicyStage{first, second} {
		var response policy.ModuleResponse
		var err error
		for attempt := 0; attempt < 20; attempt++ {
			response, err = generation.Evaluate(ctx, policy.ModuleRequest{
				GenerationID: "compat-generation-1", PolicyID: stage.PolicyID, PolicyKind: stage.Kind,
				InstanceID: stage.InstanceID, ExtensionPoint: policy.ExtensionHTTP, RequestID: "request-1",
				Payload: []byte("input"), Budget: stage.ResourceBudget, Host: &testPolicyHost{},
			})
			if err == nil {
				break
			}
			var evaluationError *policy.EvaluationError
			if !errors.As(err, &evaluationError) || evaluationError.Kind != policy.FailureBudget || evaluationError.Code != string(ErrorDeadline) {
				break
			}
			goruntime.Gosched()
		}
		if err != nil {
			t.Fatal(err)
		}
		if response.Action != policy.ActionAllow || string(response.Payload) != "guest-ok" {
			t.Fatalf("stage %s response=%+v", stage.InstanceID, response)
		}
	}
}

func TestPolicyGenerationPrepareFailureRollsBackAtomically(t *testing.T) {
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	artifactPath, digest := writePolicyFixture(t)
	first := fixturePolicyStage("instance-ready", model.PolicyKindIP, artifactPath, digest)
	invalid := fixturePolicyStage("instance-invalid-init", model.PolicyKindWAF, artifactPath, digest)
	invalid.Config = []byte(`{"mode":"not-compatible"}`)
	if _, err := PreparePolicyGeneration(ctx, runtime, "compat-generation-1", []model.PolicyStage{first, invalid}, nil); err == nil {
		t.Fatal("prepared a generation with an invalid second-stage init request")
	}
	runtime.mu.Lock()
	remaining := len(runtime.generations)
	runtime.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("failed preparation retained %d compiled generations", remaining)
	}
}

func TestPolicyGenerationFactoryIsolatesOptionalStageFailure(t *testing.T) {
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	artifactPath, digest := writePolicyFixture(t)
	requiredStage := fixturePolicyStage("required-instance", model.PolicyKindIP, artifactPath, digest)
	requiredStage.PolicyID = "required-policy"
	optionalStage := fixturePolicyStage("optional-instance", model.PolicyKindWAF, artifactPath, digest)
	optionalStage.PolicyID = "optional-policy"
	optionalStage.Config = []byte(`{"mode":"invalid"}`)
	events := make(chan Event, 2)
	factory := GenerationFactory{Runtime: runtime, Observer: ObserverFunc(func(event Event) { events <- event })}
	prepared, err := factory.PrepareGeneration(ctx, policy.GenerationSpec{
		ID: "compat-generation-1", RequiredPolicyIDs: []string{"required-policy"},
		Policies: []model.PluginPolicy{
			{ID: "required-policy", Revision: 1, Stages: []model.PolicyStage{requiredStage}},
			{ID: "optional-policy", Revision: 1, Stages: []model.PolicyStage{optionalStage}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	generation := prepared.(*PolicyGeneration)
	t.Cleanup(func() { _ = generation.Close(context.Background()) })
	if len(generation.stages) != 1 {
		t.Fatalf("prepared stages=%d, want only required healthy stage", len(generation.stages))
	}
	select {
	case event := <-events:
		if event.Code != ErrorOptionalDegraded {
			t.Fatalf("optional failure event=%+v", event)
		}
	default:
		t.Fatal("optional stage failure was not observed")
	}
}

func TestPolicyGenerationFactoryRequiredFailureRollsBackOptionalStages(t *testing.T) {
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	artifactPath, digest := writePolicyFixture(t)
	optionalStage := fixturePolicyStage("optional-ready", model.PolicyKindIP, artifactPath, digest)
	optionalStage.PolicyID = "optional-policy"
	requiredStage := fixturePolicyStage("required-invalid", model.PolicyKindWAF, artifactPath, digest)
	requiredStage.PolicyID = "required-policy"
	requiredStage.Config = []byte(`{"mode":"invalid"}`)
	factory := GenerationFactory{Runtime: runtime}
	if _, err := factory.PrepareGeneration(ctx, policy.GenerationSpec{
		ID: "compat-generation-1", RequiredPolicyIDs: []string{"required-policy"},
		Policies: []model.PluginPolicy{
			{ID: "optional-policy", Revision: 1, Stages: []model.PolicyStage{optionalStage}},
			{ID: "required-policy", Revision: 1, Stages: []model.PolicyStage{requiredStage}},
		},
	}); err == nil {
		t.Fatal("required stage failure did not reject the candidate generation")
	}
	runtime.mu.Lock()
	remaining := len(runtime.generations)
	runtime.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("required failure retained %d candidate generations", remaining)
	}
}

func TestPolicyGenerationRejectsOverflowAndConcurrencyBudgets(t *testing.T) {
	stage := model.PolicyStage{
		InstanceID: "budget-instance", PolicyID: "budget-policy", ABI: model.PolicyABIV1,
		SignatureVerified: true, SignerKeyID: "key", SignerFingerprint: "fingerprint",
		ArtifactPath: "artifact.wasm", ArtifactDigest: string(make([]byte, 64)),
		ResourceBudget: model.PolicyResourceBudget{
			TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096,
		},
	}
	overflow := stage
	overflow.ResourceBudget.TimeoutMS = math.MaxInt64
	if err := validatePolicyStageEvidence(overflow); err == nil {
		t.Fatal("accepted overflowing timeout milliseconds")
	}
	overConcurrency := stage
	overConcurrency.ResourceBudget.Concurrency = policy.MaxPolicyConcurrency + 1
	if err := validatePolicyStageEvidence(overConcurrency); err == nil {
		t.Fatal("accepted concurrency above the policy ceiling")
	}
}

func writePolicyFixture(t *testing.T) (string, string) {
	t.Helper()
	wasmBytes := compatfixture.PolicyV1GuestWASM()
	path := filepath.Join(t.TempDir(), "policy.wasm")
	if err := os.WriteFile(path, wasmBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(wasmBytes)
	return path, hex.EncodeToString(digest[:])
}

func fixturePolicyStage(instanceID string, kind model.PolicyKind, artifactPath, digest string) model.PolicyStage {
	return model.PolicyStage{
		Kind: kind, PolicyID: "policy-reference", PluginID: "official.reference", PluginVersion: "1.0.0",
		InstanceID: instanceID, PackageDigest: digest, ArtifactPath: artifactPath, ArtifactDigest: digest,
		SignatureVerified: true, SignerKeyID: "test-key", SignerFingerprint: "test-fingerprint",
		ABI: model.PolicyABIV1, ExtensionPoints: []string{policy.ExtensionHTTP},
		GrantedScopes: []string{"http.inspect", "state.read"}, Config: []byte(`{"mode":"compat"}`),
		ResourceBudget: model.PolicyResourceBudget{TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096},
		FailurePolicy:  model.PolicyFailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
	}
}
