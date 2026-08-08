package pluginsdk

import "testing"

func TestRuntimeABIConstantsAndErrorsAreStable(t *testing.T) {
	if PolicyABIV1 != "nre:policy/v1" || RPCABIV1 != "nre:rpc/v1" {
		t.Fatal("runtime ABI identifiers changed")
	}
	if (&RuntimeError{Code: ErrorIncompatibleABI, Message: "mismatch"}).Error() != "incompatible_abi: mismatch" {
		t.Fatal("runtime error wire semantics changed")
	}
}
