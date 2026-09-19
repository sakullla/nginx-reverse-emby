package pluginsdk

import (
	"strings"
	"testing"
)

func TestValidatePolicyIdentityCanonicalBoundary(t *testing.T) {
	if err := ValidatePolicyIdentity(strings.Repeat("a", PolicyIdentityMaxBytes)); err != nil {
		t.Fatalf("exact identity boundary rejected: %v", err)
	}
	for name, value := range map[string]string{
		"empty": "", "leading whitespace": " id", "trailing whitespace": "id ",
		"newline": "id\nsecret", "nul": "id\x00secret", "oversized": strings.Repeat("a", PolicyIdentityMaxBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidatePolicyIdentity(value); err == nil {
				t.Fatal("ValidatePolicyIdentity accepted invalid value")
			}
		})
	}
}

func TestPolicyV1EvaluateRequestFrameBytesCountsCompleteFrame(t *testing.T) {
	if PolicyRequestIDMaxBytes != 256 {
		t.Fatalf("PolicyRequestIDMaxBytes=%d, want stable v1 bound 256", PolicyRequestIDMaxBytes)
	}
	base, err := PolicyV1EvaluateRequestFrameBytes("http.request", "request-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	withInput, err := PolicyV1EvaluateRequestFrameBytes("http.request", "request-1", []byte("overlay"))
	if err != nil {
		t.Fatal(err)
	}
	if base <= len("http.request")+len("request-1") || withInput-base <= len("overlay") {
		t.Fatalf("frame sizes base=%d with_input=%d do not include protobuf tags/lengths", base, withInput)
	}
}
