package wasmreference

import (
	"bytes"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestWAFGuestIsDeterministicAndCanonicalPolicyV1(t *testing.T) {
	options := WAFOptions{ScanRounds: 1, MemoryMinPages: 1, MemoryMaxPages: 1}
	first, second := WAFGuest(options), WAFGuest(options)
	if !bytes.Equal(first, second) {
		t.Fatal("reference WAF guest generation is not deterministic")
	}
	if err := pluginsdk.ValidatePolicyV1WASM(first, int64(pluginsdk.WASMPageSizeBytes)); err != nil {
		t.Fatalf("reference WAF guest violates canonical policy v1: %v", err)
	}
}
