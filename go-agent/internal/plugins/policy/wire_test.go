package policy

import (
	"bytes"
	"testing"
)

func TestPolicyReadFieldValueBoundFitsCompleteBytesResponse(t *testing.T) {
	exact, err := policyBytesResponseFrameBytes(bytes.Repeat([]byte("x"), MaxPolicyReadFieldValueBytes), true)
	if err != nil {
		t.Fatal(err)
	}
	if exact != int(MaxPolicyOutputBytes) {
		t.Fatalf("exact BytesResponse frame = %d, want %d", exact, MaxPolicyOutputBytes)
	}
	overflow, err := policyBytesResponseFrameBytes(bytes.Repeat([]byte("x"), MaxPolicyReadFieldValueBytes+1), true)
	if err != nil {
		t.Fatal(err)
	}
	if overflow != int(MaxPolicyOutputBytes)+1 {
		t.Fatalf("overflow BytesResponse frame = %d, want %d", overflow, MaxPolicyOutputBytes+1)
	}
}
