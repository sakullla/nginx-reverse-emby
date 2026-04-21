package diagnostics

import (
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
)

func diagnosticAddressKey(relayChain []int, address string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}
	if len(relayChain) == 0 {
		return trimmed
	}
	return backends.RelayBackoffKey(relayChain, trimmed)
}

func markDiagnosticAddressFailure(cache *backends.Cache, relayChain []int, address string) {
	if cache == nil {
		return
	}
	cache.MarkFailure(diagnosticAddressKey(relayChain, address))
}

func observeDiagnosticAddressSuccess(cache *backends.Cache, relayChain []int, address string, latency time.Duration, totalDuration time.Duration, bytesTransferred int64) {
	if cache == nil {
		return
	}
	cache.ObserveTransferSuccess(diagnosticAddressKey(relayChain, address), latency, totalDuration, bytesTransferred)
}

func markDiagnosticAddressSuccess(cache *backends.Cache, relayChain []int, address string) {
	if cache == nil {
		return
	}
	cache.MarkSuccess(diagnosticAddressKey(relayChain, address))
}
