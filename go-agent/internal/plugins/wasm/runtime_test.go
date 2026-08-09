package wasm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
)

func TestWASMVerifiedBoundaryAndGenerationReuse(t *testing.T) {
	wasmBytes := compatfixture.PolicyV1GuestWASM()
	digest := sha256.Sum256(wasmBytes)
	if _, err := AcceptVerifiedArtifact(wasmBytes, hex.EncodeToString(digest[:]), false); err == nil {
		t.Fatal("accepted an artifact without a verified signature")
	}
	if _, err := AcceptVerifiedArtifact(wasmBytes, string(make([]byte, 64)), true); err == nil {
		t.Fatal("accepted a mismatched artifact digest")
	}
	artifact, err := AcceptVerifiedArtifact(wasmBytes, hex.EncodeToString(digest[:]), true)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	generation, err := runtime.CompileGeneration(ctx, artifact, GenerationConfig{
		ID:          "generation-1",
		InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget:      Budget{MaxMemoryPages: 16, MaxConcurrency: 1, Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := &testPolicyHost{}
	for index := 0; index < 2; index++ {
		response, err := generation.Evaluate(ctx, host, compatfixture.CanonicalPolicyV1EvaluateRequest())
		if err != nil {
			t.Fatal(err)
		}
		if err := pluginsdk.ValidatePolicyEvaluateResponseFrame(response); err != nil {
			t.Fatal(err)
		}
	}
	generation.mu.Lock()
	created := generation.created
	idle := len(generation.idle)
	generation.mu.Unlock()
	if created != 1 || idle != 1 {
		t.Fatalf("generation pool created=%d idle=%d, want one reused instance", created, idle)
	}
	if host.readFieldCalls != 4 {
		t.Fatalf("host read calls=%d, want two bounded retries per evaluation", host.readFieldCalls)
	}
}

func TestWASMGenerationConcurrencyBudgetAndDrain(t *testing.T) {
	artifact := verifiedFixture(t)
	events := make(chan Event, 8)
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16, Observer: ObserverFunc(func(event Event) { events <- event })})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	generation, err := runtime.CompileGeneration(ctx, artifact, GenerationConfig{
		ID:          "bounded-generation",
		InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget:      Budget{MaxMemoryPages: 16, MaxConcurrency: 1, Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := &testPolicyHost{entered: make(chan struct{}), release: make(chan struct{})}
	firstDone := make(chan error, 1)
	go func() {
		_, evaluateErr := generation.Evaluate(ctx, host, compatfixture.CanonicalPolicyV1EvaluateRequest())
		firstDone <- evaluateErr
	}()
	<-host.entered
	if _, err := generation.Evaluate(ctx, &testPolicyHost{}, compatfixture.CanonicalPolicyV1EvaluateRequest()); !IsCode(err, ErrorConcurrencyBudget) {
		t.Fatalf("second evaluation error=%v, want concurrency budget", err)
	}
	close(host.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := generation.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := generation.Evaluate(ctx, &testPolicyHost{}, compatfixture.CanonicalPolicyV1EvaluateRequest()); !IsCode(err, ErrorGenerationDraining) {
		t.Fatalf("post-drain evaluation error=%v, want draining", err)
	}
	generation.mu.Lock()
	created := generation.created
	generation.mu.Unlock()
	if created != 0 {
		t.Fatalf("drained generation retains %d instances", created)
	}
	select {
	case event := <-events:
		if event.Code != ErrorConcurrencyBudget {
			t.Fatalf("first observed event=%+v, want concurrency budget", event)
		}
	default:
		t.Fatal("budget failure was not observable")
	}
}

func TestWASMBudgetsAreObservable(t *testing.T) {
	ctx := context.Background()
	events := make(chan Event, 4)
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16, Observer: ObserverFunc(func(event Event) { events <- event })})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	_, err = runtime.CompileGeneration(ctx, verifiedFixture(t), GenerationConfig{
		ID:          "budget-generation",
		InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget:      Budget{MaxInputBytes: 8, MaxOutputBytes: 8, MaxMemoryPages: 16, MaxConcurrency: 1, Timeout: time.Second},
	})
	if err == nil || !IsCode(err, ErrorInputBudget) {
		t.Fatalf("compile error=%v, want init input budget", err)
	}
	if event := <-events; event.Code != ErrorInputBudget {
		t.Fatalf("event=%+v, want input budget", event)
	}
}

func TestWASMReadyColdRuntimeWithTwoMillisecondRequestBudget(t *testing.T) {
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	generation, err := runtime.CompileGeneration(ctx, verifiedFixture(t), GenerationConfig{
		ID:          "cold-ready-generation",
		InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget:      Budget{MaxMemoryPages: 16, MaxConcurrency: 1, Timeout: 2 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := generation.Ready(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWASMRejectsForbiddenCapabilitiesAndMemory(t *testing.T) {
	fixture := compatfixture.PolicyV1GuestWASM()
	withStart := append(append([]byte(nil), fixture...), 8, 1, 0)
	if err := validateBinaryEnvelope(withStart); err == nil {
		t.Fatal("accepted a WebAssembly start section")
	}
	importedMemory := append(append([]byte(nil), wasmV1Header...), 2, 8, 1, 1, 'x', 1, 'm', 2, 0, 1)
	if err := validateBinaryEnvelope(importedMemory); err == nil {
		t.Fatal("accepted an imported memory")
	}

	forbiddenImport := bytes.ReplaceAll(fixture, []byte(pluginsdk.PolicyHostModule), []byte("wasi_snapshot"))
	if bytes.Equal(forbiddenImport, fixture) {
		t.Fatal("canonical fixture host module was not found")
	}
	digest := sha256.Sum256(forbiddenImport)
	artifact, err := AcceptVerifiedArtifact(forbiddenImport, hex.EncodeToString(digest[:]), true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if _, err := runtime.CompileGeneration(ctx, artifact, GenerationConfig{
		ID: "wasi-generation", InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget: Budget{MaxMemoryPages: 16},
	}); !IsCode(err, ErrorIncompatibleABI) {
		t.Fatalf("forbidden import error=%v, want incompatible ABI", err)
	}
	if _, err := runtime.CompileGeneration(ctx, verifiedFixture(t), GenerationConfig{
		ID: "memory-generation", InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget: Budget{MaxMemoryPages: 8},
	}); !IsCode(err, ErrorIncompatibleABI) {
		t.Fatalf("memory maximum error=%v, want incompatible ABI", err)
	}
}

func TestWASMOutputBudgetIsObservable(t *testing.T) {
	ctx := context.Background()
	events := make(chan Event, 4)
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16, Observer: ObserverFunc(func(event Event) { events <- event })})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	generation, err := runtime.CompileGeneration(ctx, verifiedFixture(t), GenerationConfig{
		ID: "output-generation", InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget: Budget{MaxInputBytes: 4096, MaxOutputBytes: 8, MaxMemoryPages: 16, MaxConcurrency: 1, Timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generation.Evaluate(ctx, &testPolicyHost{}, compatfixture.CanonicalPolicyV1EvaluateRequest()); !IsCode(err, ErrorOutputBudget) {
		t.Fatalf("evaluate error=%v, want output budget", err)
	}
	found := false
	for len(events) > 0 {
		if (<-events).Code == ErrorOutputBudget {
			found = true
		}
	}
	if !found {
		t.Fatal("output budget failure was not observable")
	}
}

func verifiedFixture(t *testing.T) VerifiedArtifact {
	t.Helper()
	wasmBytes := compatfixture.PolicyV1GuestWASM()
	digest := sha256.Sum256(wasmBytes)
	artifact, err := AcceptVerifiedArtifact(wasmBytes, hex.EncodeToString(digest[:]), true)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

type testPolicyHost struct {
	mu             sync.Mutex
	readFieldCalls int
	entered        chan struct{}
	release        chan struct{}
	once           sync.Once
}

func (host *testPolicyHost) ReadField(context.Context, string) ([]byte, error) {
	host.mu.Lock()
	host.readFieldCalls++
	host.mu.Unlock()
	if host.entered != nil {
		host.once.Do(func() { close(host.entered) })
		<-host.release
	}
	return []byte("GET"), nil
}

func (*testPolicyHost) ReadBodyWindow(context.Context, uint32, uint32) ([]byte, error) {
	return nil, errors.New("unavailable")
}
func (*testPolicyHost) StateGet(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("unavailable")
}
func (*testPolicyHost) StatePut(context.Context, string, []byte) error {
	return errors.New("unavailable")
}
func (*testPolicyHost) EmitEvent(context.Context, string, []byte) error {
	return errors.New("unavailable")
}
func (*testPolicyHost) AddMetric(context.Context, string, int64) error {
	return errors.New("unavailable")
}
