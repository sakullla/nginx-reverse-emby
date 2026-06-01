package diagnostics

import (
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayplan"
)

func diagnosticAddressKey(relayChain []int, address string) string {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return ""
	}
	if len(relayChain) == 0 {
		return trimmed
	}
	return model.RelayBackoffKey(relayChain, trimmed)
}

func diagnosticRelayChainForObservation(configured []int, candidate []int, selected []int) []int {
	if len(selected) > 0 {
		return append([]int(nil), selected...)
	}
	if len(candidate) > 0 {
		return append([]int(nil), candidate...)
	}
	return append([]int(nil), configured...)
}

func markDiagnosticAddressFailure(cache *model.Cache, relayChain []int, address string) {
	markDiagnosticAddressFailureAll(relayChain, address, cache)
}

func markDiagnosticAddressFailureAll(relayChain []int, address string, caches ...*model.Cache) {
	key := diagnosticAddressKey(relayChain, address)
	for _, cache := range uniqueDiagnosticCaches(caches...) {
		cache.MarkFailure(key)
	}
}

func uniqueDiagnosticCaches(caches ...*model.Cache) []*model.Cache {
	seen := make(map[*model.Cache]struct{}, len(caches))
	out := make([]*model.Cache, 0, len(caches))
	for _, cache := range caches {
		if cache == nil {
			continue
		}
		if _, ok := seen[cache]; ok {
			continue
		}
		seen[cache] = struct{}{}
		out = append(out, cache)
	}
	return out
}

func observeDiagnosticAddressSuccess(cache *model.Cache, relayChain []int, address string, latency time.Duration, totalDuration time.Duration, bytesTransferred int64) {
	observeDiagnosticAddressSuccessAll(relayChain, address, latency, totalDuration, bytesTransferred, cache)
}

func observeDiagnosticAddressSuccessAll(relayChain []int, address string, latency time.Duration, totalDuration time.Duration, bytesTransferred int64, caches ...*model.Cache) {
	key := diagnosticAddressKey(relayChain, address)
	for _, cache := range uniqueDiagnosticCaches(caches...) {
		cache.ObserveTransferSuccess(key, latency, totalDuration, bytesTransferred)
	}
}

func markDiagnosticAddressSuccess(cache *model.Cache, relayChain []int, address string) {
	markDiagnosticAddressSuccessAll(relayChain, address, cache)
}

func markDiagnosticAddressSuccessAll(relayChain []int, address string, caches ...*model.Cache) {
	key := diagnosticAddressKey(relayChain, address)
	for _, cache := range uniqueDiagnosticCaches(caches...) {
		cache.MarkSuccess(key)
	}
}

func persistentDiagnosticAddressCaches(runCache *model.Cache, sharedCache *model.Cache, relayChain []int) []*model.Cache {
	if len(relayChain) == 0 {
		return uniqueDiagnosticCaches(runCache)
	}
	return uniqueDiagnosticCaches(runCache, sharedCache)
}

func relayResolvedAddressBackedOffForAllPaths(cache *model.Cache, fallbackChain []int, paths []relayplan.Path, address string) bool {
	if cache == nil {
		return false
	}
	if len(paths) == 0 {
		return cache.IsInBackoff(diagnosticAddressKey(fallbackChain, address))
	}
	return len(relayPathsAvailableForAddress(cache, fallbackChain, paths, address)) == 0
}

func relayPathsAvailableForAddress(cache *model.Cache, fallbackChain []int, paths []relayplan.Path, address string) []relayplan.Path {
	if cache == nil {
		return append([]relayplan.Path(nil), paths...)
	}
	if len(paths) == 0 {
		if cache.IsInBackoff(diagnosticAddressKey(fallbackChain, address)) {
			return nil
		}
		return append([]relayplan.Path(nil), paths...)
	}
	available := make([]relayplan.Path, 0, len(paths))
	for _, path := range paths {
		if cache.IsInBackoff(diagnosticAddressKey(path.IDs, address)) {
			continue
		}
		available = append(available, path)
	}
	return available
}
