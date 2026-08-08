package pluginsdk

import "testing"

func TestRuntimeABIConstantsAndErrorsAreStable(t *testing.T) {
	if PolicyABIV1 != "nre:policy/v1" || PolicyABIMajorVersion != 1 || RPCABIV1 != "nre:rpc/v1" {
		t.Fatal("runtime ABI identifiers changed")
	}
	if (&RuntimeError{Code: ErrorIncompatibleABI, Message: "mismatch"}).Error() != "incompatible_abi: mismatch" {
		t.Fatal("runtime error wire semantics changed")
	}
}

func TestPolicyV1WASMABICallingConventionIsStable(t *testing.T) {
	guest := PolicyV1GuestFunctions()
	host := PolicyV1HostFunctions()
	if len(guest) != 6 || len(host) != 6 || PolicyHostModule != PolicyABIV1 {
		t.Fatalf("unexpected policy ABI surface: guest=%d host=%d module=%q", len(guest), len(host), PolicyHostModule)
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
