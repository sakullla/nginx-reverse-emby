package pluginsdk

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestRPCResourceCallRejectsRemovedDockerOperation(t *testing.T) {
	call := RPCResourceCall{RequestID: "request-1", ResourceHandle: "handle-1", Operation: RPCResourceDNSApply}
	if err := call.Validate(); err != nil {
		t.Fatal(err)
	}
	call.Operation = 5
	if err := call.Validate(); err == nil {
		t.Fatal("removed Docker resource operation was accepted")
	}
}

func TestRuntimeABIConstantsAndErrorsAreStable(t *testing.T) {
	if PolicyABIV1 != "nre:policy/v1" || PolicyABIMajorVersion != 1 || RPCABIV1 != "nre:rpc/v1" {
		t.Fatal("runtime ABI identifiers changed")
	}
	if (&RuntimeError{Code: ErrorIncompatibleABI, Message: "mismatch"}).Error() != "incompatible_abi: mismatch" {
		t.Fatal("runtime error wire semantics changed")
	}
	if ErrorUnspecified.Valid() || ErrorCode(99).Valid() || !ErrorInternal.Valid() {
		t.Fatal("runtime error enum validation no longer fails closed")
	}
	if err := (PolicyEvaluateResponse{Success: &PolicyOutput{Action: PolicyActionAllow}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (LifecycleResponse{Success: &LifecycleSuccess{Ready: true}}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWireResultFramesRejectConflictMissingAndUnknownEnums(t *testing.T) {
	policySuccess := appendVarintField(nil, 1, 1)
	policySuccessFrame := appendBytesField(nil, 1, policySuccess)
	policyError := appendVarintField(nil, 1, uint64(ErrorUnavailable))
	policyErrorFrame := appendBytesField(nil, 2, policyError)
	overflowError := appendVarintField(nil, 1, uint64(1)<<32|uint64(ErrorInvalidArgument))
	rpcSuccessFrame := appendBytesField(nil, 1, appendVarintField(nil, 1, 1))
	rpcErrorFrame := appendBytesField(nil, 2, policyError)
	for name, test := range map[string]struct {
		validate func([]byte) error
		frame    []byte
		valid    bool
	}{
		"policy success":  {ValidatePolicyEvaluateResponseFrame, policySuccessFrame, true},
		"policy error":    {ValidatePolicyEvaluateResponseFrame, policyErrorFrame, true},
		"policy conflict": {ValidatePolicyEvaluateResponseFrame, append(append([]byte(nil), policySuccessFrame...), policyErrorFrame...), false},
		"policy missing":  {ValidatePolicyEvaluateResponseFrame, nil, false},
		"policy unknown":  {ValidatePolicyEvaluateResponseFrame, appendBytesField(nil, 2, appendVarintField(nil, 1, 99)), false},
		"policy overflow": {ValidatePolicyEvaluateResponseFrame, appendBytesField(nil, 2, overflowError), false},
		"RPC success":     {ValidateRPCLifecycleResponseFrame, rpcSuccessFrame, true},
		"RPC error":       {ValidateRPCLifecycleResponseFrame, rpcErrorFrame, true},
		"RPC conflict":    {ValidateRPCLifecycleResponseFrame, append(append([]byte(nil), rpcSuccessFrame...), rpcErrorFrame...), false},
		"RPC missing":     {ValidateRPCLifecycleResponseFrame, nil, false},
		"RPC unknown":     {ValidateRPCLifecycleResponseFrame, appendBytesField(nil, 2, appendVarintField(nil, 1, 99)), false},
		"RPC overflow":    {ValidateRPCLifecycleResponseFrame, appendBytesField(nil, 2, overflowError), false},
	} {
		t.Run(name, func(t *testing.T) {
			err := test.validate(test.frame)
			if test.valid && err != nil {
				t.Fatal(err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid result frame was accepted")
			}
		})
	}
}

func appendVarintField(target []byte, number protowire.Number, value uint64) []byte {
	target = protowire.AppendTag(target, number, protowire.VarintType)
	return protowire.AppendVarint(target, value)
}

func appendBytesField(target []byte, number protowire.Number, value []byte) []byte {
	target = protowire.AppendTag(target, number, protowire.BytesType)
	return protowire.AppendBytes(target, value)
}

func TestPolicyV1WASMABICallingConventionIsStable(t *testing.T) {
	guest := PolicyV1GuestFunctions()
	host := PolicyV1HostFunctions()
	requiredHost := PolicyV1RequiredHostFunctions()
	if len(guest) != 6 || len(host) != 9 || len(requiredHost) != 6 || PolicyHostModule != PolicyABIV1 {
		t.Fatalf("unexpected policy ABI surface: guest=%d host=%d module=%q", len(guest), len(host), PolicyHostModule)
	}
	if _, ok := requiredHost[PolicyHostReadNormalizedHTTP]; ok {
		t.Fatal("additive normalized HTTP import became mandatory for legacy policy/v1 guests")
	}
	if got := guest[PolicyExportEvaluate]; !sameWASMSignature(got, WASMFunctionSignature{Parameters: []WASMValueType{WASMI32, WASMI32}, Results: []WASMValueType{WASMI64}}) {
		t.Fatalf("evaluate signature changed: %+v", got)
	}
	for name, signature := range host {
		want := WASMFunctionSignature{Parameters: []WASMValueType{WASMI32, WASMI32, WASMI32, WASMI32}, Results: []WASMValueType{WASMI64}}
		if !sameWASMSignature(signature, want) {
			t.Fatalf("host import %s signature changed: %+v", name, signature)
		}
	}
	pointer, length := UnpackPolicyBuffer(PackPolicyBuffer(0x10203040, 0x50607080))
	if pointer != 0x10203040 || length != 0x50607080 {
		t.Fatalf("buffer frame changed: pointer=%x length=%x", pointer, length)
	}
	status, length := UnpackPolicyHostResult(PackPolicyHostResult(PolicyStatusResourceExhausted, 4096))
	if status != PolicyStatusResourceExhausted || length != 4096 {
		t.Fatalf("host result frame changed: status=%d length=%d", status, length)
	}
	if status.ErrorCode() != ErrorResourceExhausted || PolicyStatus(99).ErrorCode() != ErrorInternal {
		t.Fatal("numeric policy status no longer maps to stable RuntimeError codes")
	}
}

func TestPolicyV1HostFunctionSignaturesHaveIndependentBackingArrays(t *testing.T) {
	host := PolicyV1HostFunctions()
	readField := host[PolicyHostReadField]
	readField.Parameters[0] = WASMI64
	readField.Results[0] = WASMI32

	stateGet := host[PolicyHostStateGet]
	if stateGet.Parameters[0] != WASMI32 || stateGet.Results[0] != WASMI64 {
		t.Fatal("mutating one host function signature changed another map entry")
	}
	fresh := PolicyV1HostFunctions()[PolicyHostReadField]
	if fresh.Parameters[0] != WASMI32 || fresh.Results[0] != WASMI64 {
		t.Fatal("mutating a returned signature changed a later call")
	}
}

func TestConfigSchemaHostInjectedKeywordAndSemanticsAreStable(t *testing.T) {
	if ConfigSchemaKeywordHostInjected != "hostInjected" || ConfigSchemaKeywordReadOnly != "readOnly" || ConfigSchemaKeywordWriteOnly != "writeOnly" {
		t.Fatal("config schema annotation keywords changed")
	}

	injected, err := ConfigSchemaHostInjected(map[string]any{"type": "string", ConfigSchemaKeywordHostInjected: true})
	if err != nil || !injected {
		t.Fatalf("hostInjected named property is no longer host-written: injected=%t err=%v", injected, err)
	}
	unmarked, err := ConfigSchemaHostInjected(map[string]any{"type": "string"})
	if err != nil || unmarked {
		t.Fatalf("unmarked schema is no longer treated as fill-form: injected=%t err=%v", unmarked, err)
	}
	explicitFalse, err := ConfigSchemaHostInjected(map[string]any{ConfigSchemaKeywordHostInjected: false})
	if err != nil || explicitFalse {
		t.Fatalf("explicit false hostInjected changed: injected=%t err=%v", explicitFalse, err)
	}
	if _, err := ConfigSchemaHostInjected(map[string]any{ConfigSchemaKeywordHostInjected: "yes"}); err == nil {
		t.Fatal("non-boolean hostInjected was accepted")
	}

	named := map[string]any{"type": "string", ConfigSchemaKeywordHostInjected: true}
	if err := ValidateConfigSchemaHostInjected(named, false, true); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigSchemaHostInjected(map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}, true, false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigSchemaHostInjected(map[string]any{"type": "string", ConfigSchemaKeywordWriteOnly: true}, false, true); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigSchemaHostInjected(named, true, false); err == nil {
		t.Fatal("root hostInjected was accepted")
	}
	if err := ValidateConfigSchemaHostInjected(named, false, false); err == nil {
		t.Fatal("hostInjected on a non-named property was accepted")
	}
	if err := ValidateConfigSchemaHostInjected(map[string]any{ConfigSchemaKeywordHostInjected: true, ConfigSchemaKeywordWriteOnly: true}, false, true); err == nil {
		t.Fatal("hostInjected and writeOnly both true was accepted")
	}
	if err := ValidateConfigSchemaHostInjected(map[string]any{ConfigSchemaKeywordHostInjected: true, ConfigSchemaKeywordReadOnly: true}, false, true); err != nil {
		t.Fatal(err)
	}
}

func sameWASMSignature(left, right WASMFunctionSignature) bool {
	if len(left.Parameters) != len(right.Parameters) || len(left.Results) != len(right.Results) {
		return false
	}
	for index := range left.Parameters {
		if left.Parameters[index] != right.Parameters[index] {
			return false
		}
	}
	for index := range left.Results {
		if left.Results[index] != right.Results[index] {
			return false
		}
	}
	return true
}
