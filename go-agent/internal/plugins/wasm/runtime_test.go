//go:build !integration

package wasm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var testWASMHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// This is the old v1 free-text wire shape with real request-derived values
// in rule_id and summary. The new enum schema treats both length-delimited
// fields as unknown, leaving code/action unspecified and rejecting dispatch.

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

func TestWASMEvaluateDeadlineCoversNonTerminatingReset(t *testing.T) {
	events := make(chan Event, 4)
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{
		MaxMemoryPages: 16,
		Observer:       ObserverFunc(func(event Event) { events <- event }),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	generation, err := runtime.CompileGeneration(ctx, verifiedArtifactFromBytes(t, policyFixtureWithResetBody(t,
		[]byte{0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x41, 0x00, 0x0b},
	)), GenerationConfig{
		ID:          "non-terminating-reset",
		InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget:      Budget{MaxMemoryPages: 16, MaxConcurrency: 1, Timeout: 10 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("compile non-terminating reset fixture: %v: %v", err, errors.Unwrap(err))
	}

	started := time.Now()
	if _, err := generation.Evaluate(ctx, &testPolicyHost{}, compatfixture.CanonicalPolicyV1EvaluateRequest()); !IsCode(err, ErrorDeadline) {
		t.Fatalf("evaluate error=%v, want deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("non-terminating reset held the request for %s", elapsed)
	}
	generation.mu.Lock()
	created, idle := generation.created, len(generation.idle)
	generation.mu.Unlock()
	if created != 0 || idle != 0 {
		t.Fatalf("timed-out reset retained created=%d idle=%d instances", created, idle)
	}
	found := false
	for len(events) > 0 {
		if event := <-events; event.Operation == "evaluate" && event.Code == ErrorDeadline {
			found = true
		}
	}
	if !found {
		t.Fatal("reset timeout was not observed as an evaluate deadline")
	}
}

func TestWASMRejectsForbiddenCapabilitiesAndMemory(t *testing.T) {
	fixture := compatfixture.PolicyV1GuestWASM()
	withStart := append(append([]byte(nil), fixture...), 8, 1, 0)
	if err := pluginsdk.ValidatePolicyV1WASM(withStart, 16*int64(pluginsdk.WASMPageSizeBytes)); err == nil {
		t.Fatal("accepted a WebAssembly start section")
	}
	importedMemory := append(append([]byte(nil), testWASMHeader...), 2, 8, 1, 1, 'x', 1, 'm', 2, 0, 1)
	if err := pluginsdk.ValidatePolicyV1WASM(importedMemory, 16*int64(pluginsdk.WASMPageSizeBytes)); err == nil {
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

func TestWASMHostReadFieldEncodesPresentEmptySeparatelyFromMissing(t *testing.T) {
	ctx := context.Background()
	runtime, err := NewRuntime(ctx, RuntimeOptions{MaxMemoryPages: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	generation, err := runtime.CompileGeneration(ctx, verifiedFixture(t), GenerationConfig{
		ID: "host-read-field-presence", InitRequest: compatfixture.CanonicalPolicyV1InitRequest(),
		Budget: Budget{MaxMemoryPages: 16, MaxConcurrency: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	guest, err := generation.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer generation.release(guest, true)
	request := marshalHostTestMessage(t, "ReadFieldRequest", func(message protoreflect.Message) {
		message.Set(policyField(message.Interface(), "name"), protoreflect.ValueOfString("request.query"))
	})
	budget := Budget{MaxInputBytes: uint32(len(request)), MaxOutputBytes: uint32(pluginsdk.PolicyV1MaxOutputFrameBytes)}

	for name, test := range map[string]struct {
		value []byte
		found bool
	}{
		"present empty": {value: make([]byte, 0), found: true},
		"missing":       {value: nil, found: false},
	} {
		t.Run(name, func(t *testing.T) {
			packed := callHostTestFrame(t, runtime, guest, &readFieldPresenceHost{value: test.value}, pluginsdk.PolicyHostReadField, request, budget.MaxOutputBytes, budget)
			status, written := pluginsdk.UnpackPolicyHostResult(packed)
			if status != pluginsdk.PolicyStatusOK {
				t.Fatalf("ReadField status = %d", status)
			}
			response, ok := guest.module.Memory().Read(32<<10, written)
			if !ok {
				t.Fatal("read BytesResponse")
			}
			message, err := newPolicyMessage("BytesResponse")
			if err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(response, message); err != nil {
				t.Fatalf("decode BytesResponse: %v", err)
			}
			found := message.ProtoReflect().Get(policyField(message, "found")).Bool()
			if found != test.found {
				t.Fatalf("BytesResponse found = %v, want %v", found, test.found)
			}
		})
	}
}

func marshalHostTestMessage(t *testing.T, name protoreflect.Name, populate func(protoreflect.Message)) []byte {
	t.Helper()
	message, err := newPolicyMessage(name)
	if err != nil {
		t.Fatal(err)
	}
	populate(message.ProtoReflect())
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func callHostTestFrame(t *testing.T, runtime *Runtime, guest *instance, host pluginsdk.PolicyHost, name string, request []byte, responseCapacity uint32, budget Budget) uint64 {
	t.Helper()
	const requestPointer = uint32(16 << 10)
	const responsePointer = uint32(32 << 10)
	if !guest.module.Memory().Write(requestPointer, request) {
		t.Fatal("write host test request")
	}
	stack := []uint64{uint64(requestPointer), uint64(len(request)), uint64(responsePointer), uint64(responseCapacity)}
	return runtime.callHost(contextWithHost(context.Background(), "host-budget-dimensions", host, budget), guest.module, name, stack)
}

type dimensionPolicyHost struct {
	testPolicyHost
	statePutError error
}

type readFieldBoundaryHost struct {
	testPolicyHost
	value []byte
}

type readFieldPresenceHost struct {
	testPolicyHost
	value []byte
}

func (host *readFieldPresenceHost) ReadField(context.Context, string) ([]byte, error) {
	return host.value, nil
}

func (host *readFieldBoundaryHost) ReadField(context.Context, string) ([]byte, error) {
	return append([]byte(nil), host.value...), nil
}

func (host *dimensionPolicyHost) StatePut(context.Context, string, []byte) error {
	return host.statePutError
}

type testBudgetDimensionError struct {
	dimension pluginsdk.BudgetDimension
	cause     error
}

func (err *testBudgetDimensionError) Error() string { return "resource exhausted" }
func (err *testBudgetDimensionError) Unwrap() error { return err.cause }
func (err *testBudgetDimensionError) BudgetDimension() pluginsdk.BudgetDimension {
	return err.dimension
}

func verifiedFixture(t *testing.T) VerifiedArtifact {
	t.Helper()
	return verifiedArtifactFromBytes(t, compatfixture.PolicyV1GuestWASM())
}

func verifiedArtifactFromBytes(t *testing.T, wasmBytes []byte) VerifiedArtifact {
	t.Helper()
	digest := sha256.Sum256(wasmBytes)
	artifact, err := AcceptVerifiedArtifact(wasmBytes, hex.EncodeToString(digest[:]), true)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func policyFixtureWithResetBody(t *testing.T, resetBody []byte) []byte {
	t.Helper()
	module := compatfixture.PolicyV1GuestWASM()
	result := append([]byte(nil), module[:len(testWASMHeader)]...)
	remaining := module[len(testWASMHeader):]
	for len(remaining) > 0 {
		sectionID := remaining[0]
		sectionLength, consumed, ok := consumeTestULEB32(remaining[1:])
		if !ok || int(sectionLength) > len(remaining)-1-consumed {
			t.Fatal("malformed compatibility fixture section")
		}
		section := append([]byte(nil), remaining[1+consumed:1+consumed+int(sectionLength)]...)
		remaining = remaining[1+consumed+int(sectionLength):]
		if sectionID == 10 {
			section = replaceLastCodeBody(t, section, resetBody)
		}
		result = append(result, sectionID)
		result = appendTestULEB32(result, uint32(len(section)))
		result = append(result, section...)
	}
	return result
}

func replaceLastCodeBody(t *testing.T, section, replacement []byte) []byte {
	t.Helper()
	count, consumed, ok := consumeTestULEB32(section)
	if !ok || count == 0 {
		t.Fatal("compatibility fixture code vector is missing")
	}
	result := append([]byte(nil), section[:consumed]...)
	remaining := section[consumed:]
	for index := uint32(0); index < count; index++ {
		bodyLength, bodyPrefix, valid := consumeTestULEB32(remaining)
		if !valid || int(bodyLength) > len(remaining)-bodyPrefix {
			t.Fatal("malformed compatibility fixture function body")
		}
		body := remaining[bodyPrefix : bodyPrefix+int(bodyLength)]
		remaining = remaining[bodyPrefix+int(bodyLength):]
		if index == count-1 {
			body = replacement
		}
		result = appendTestULEB32(result, uint32(len(body)))
		result = append(result, body...)
	}
	if len(remaining) != 0 {
		t.Fatal("trailing compatibility fixture code data")
	}
	return result
}

func consumeTestULEB32(encoded []byte) (uint32, int, bool) {
	var result uint32
	for index := 0; index < len(encoded) && index < 5; index++ {
		current := encoded[index]
		if index == 4 && current&0xf0 != 0 {
			return 0, 0, false
		}
		result |= uint32(current&0x7f) << (7 * index)
		if current&0x80 == 0 {
			return result, index + 1, true
		}
	}
	return 0, 0, false
}

func appendTestULEB32(target []byte, value uint32) []byte {
	var encoded [binary.MaxVarintLen32]byte
	length := binary.PutUvarint(encoded[:], uint64(value))
	return append(target, encoded[:length]...)
}

type testPolicyHost struct {
	mu             sync.Mutex
	readFieldCalls int
	entered        chan struct{}
	release        chan struct{}
	once           sync.Once
}

type eventCapturePolicyHost struct {
	testPolicyHost
	event pluginsdk.PolicySecurityEvent
	calls int
}

func (host *eventCapturePolicyHost) EmitEvent(_ context.Context, event pluginsdk.PolicySecurityEvent) error {
	host.event = event
	host.calls++
	return nil
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
func (*testPolicyHost) EmitEvent(context.Context, pluginsdk.PolicySecurityEvent) error {
	return errors.New("unavailable")
}
func (*testPolicyHost) AddMetric(context.Context, string, int64) error {
	return errors.New("unavailable")
}
