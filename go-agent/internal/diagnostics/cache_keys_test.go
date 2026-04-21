package diagnostics

import (
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
)

func TestMarkDiagnosticAddressFailureUsesRelayScopedKey(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	chain := []int{41}
	address := "relay-target.example:9443"
	scopedKey := backends.RelayBackoffKey(chain, address)

	markDiagnosticAddressFailure(cache, chain, address)

	if !cache.IsInBackoff(scopedKey) {
		t.Fatalf("expected scoped key %q to enter backoff", scopedKey)
	}
	if cache.IsInBackoff(address) {
		t.Fatalf("expected plain address %q to stay out of backoff", address)
	}
}

func TestObserveDiagnosticAddressSuccessUsesRelayScopedKey(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	chain := []int{51}
	address := "relay-target.example:9001"
	scopedKey := backends.RelayBackoffKey(chain, address)

	markDiagnosticAddressFailure(cache, chain, address)
	observeDiagnosticAddressSuccess(cache, chain, address, 25*time.Millisecond, 60*time.Millisecond, 128*1024)

	if cache.IsInBackoff(scopedKey) {
		t.Fatalf("expected scoped key %q backoff to clear after success", scopedKey)
	}
	summary := cache.Summary(scopedKey)
	if summary.RecentSucceeded != 1 {
		t.Fatalf("expected scoped key success summary, got %+v", summary)
	}
	if plain := cache.Summary(address); plain.RecentSucceeded != 0 || plain.RecentFailed != 0 {
		t.Fatalf("expected plain address summary to remain untouched, got %+v", plain)
	}
}

func TestMarkDiagnosticAddressSuccessUsesRelayScopedKey(t *testing.T) {
	cache := backends.NewCache(backends.Config{})
	chain := []int{61}
	address := "relay-target.example:9002"
	scopedKey := backends.RelayBackoffKey(chain, address)

	markDiagnosticAddressFailure(cache, chain, address)
	markDiagnosticAddressSuccess(cache, chain, address)

	if cache.IsInBackoff(scopedKey) {
		t.Fatalf("expected scoped key %q backoff to clear after success", scopedKey)
	}
	if cache.IsInBackoff(address) {
		t.Fatalf("expected plain address %q to stay out of backoff", address)
	}
}
