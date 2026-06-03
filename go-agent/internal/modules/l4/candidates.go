package l4

import (
	"context"
	"errors"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"net"
	"strconv"
	"strings"
	"time"
)

type l4Candidate struct {
	address               string
	directUDPPath         bool
	backoffKey            string
	markBackoffOnFailure  bool
	backendObservationKey string
}

func l4Candidates(ctx context.Context, cache *model.Cache, rule model.L4Rule) ([]l4Candidate, error) {
	if cache == nil {
		return nil, fmt.Errorf("backend cache is required")
	}

	rawBackends := rule.Backends
	if len(rawBackends) == 0 {
		return nil, fmt.Errorf("at least one backend is required for %s:%d", rule.ListenHost, rule.ListenPort)
	}

	placeholders := make([]model.Candidate, 0, len(rawBackends))
	indexesByID := make(map[string][]int, len(rawBackends))
	duplicateCounts := make(map[string]int, len(rawBackends))
	for i := range rawBackends {
		id := model.StableBackendID(net.JoinHostPort(rawBackends[i].Host, strconv.Itoa(rawBackends[i].Port)))
		placeholders = append(placeholders, model.Candidate{Address: id})
		indexesByID[id] = append(indexesByID[id], i)
		duplicateCounts[id]++
	}

	scope := strings.ToLower(rule.Protocol) + ":" + net.JoinHostPort(rule.ListenHost, strconv.Itoa(rule.ListenPort))
	orderedBackends := cache.OrderLatencyOnly(scope, rule.LoadBalancing.Strategy, placeholders)
	out := make([]l4Candidate, 0, len(rawBackends))
	for _, ordered := range orderedBackends {
		indexes := indexesByID[ordered.Address]
		if len(indexes) == 0 {
			continue
		}
		backendIndex := indexes[0]
		indexesByID[ordered.Address] = indexes[1:]
		backend := rawBackends[backendIndex]
		backendID := model.StableBackendID(net.JoinHostPort(backend.Host, strconv.Itoa(backend.Port)))
		if ruleUsesRelay(rule) {
			// Preserve the configured host for relay chains so the final hop resolves DNS.
			dialAddress := net.JoinHostPort(backend.Host, strconv.Itoa(backend.Port))
			bk := model.RelayBackoffKeyForLayers(nil, rule.RelayLayers, dialAddress)
			if cache.IsInBackoff(bk) {
				continue
			}
			out = append(out, l4Candidate{
				address:               dialAddress,
				directUDPPath:         false,
				backoffKey:            bk,
				markBackoffOnFailure:  len(rule.RelayLayers) == 0,
				backendObservationKey: l4ObservationKey(scope, backendID, backendIndex, duplicateCounts[backendID]),
			})
			continue
		}
		endpoint := model.Endpoint{
			Host: backend.Host,
			Port: backend.Port,
		}
		resolved, err := cache.Resolve(ctx, endpoint)
		if err != nil {
			if ctx != nil {
				if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
					return nil, ctxErr
				}
			}
			continue
		}
		resolved = cache.PreferResolvedCandidatesLatencyOnly(resolved)
		for _, candidate := range resolved {
			if cache.IsInBackoff(candidate.Address) {
				continue
			}
			out = append(out, l4Candidate{
				address:               candidate.Address,
				directUDPPath:         strings.ToLower(rule.Protocol) == "udp" && !ruleUsesRelay(rule),
				markBackoffOnFailure:  true,
				backendObservationKey: l4ObservationKey(scope, backendID, backendIndex, duplicateCounts[backendID]),
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no healthy backend candidates for %s:%d", rule.ListenHost, rule.ListenPort)
	}
	return out, nil
}

func l4ObservationKey(scope string, backendID string, backendIndex int, duplicateCount int) string {
	if duplicateCount <= 1 {
		return model.BackendObservationKey(scope, backendID)
	}
	return model.BackendObservationKey(scope, fmt.Sprintf("%s#%d", backendID, backendIndex+1))
}

func l4CandidateBackoffAddr(candidate l4Candidate) string {
	if candidate.backoffKey != "" {
		return candidate.backoffKey
	}
	return candidate.address
}

func (s *Server) observeCandidateFailure(candidate l4Candidate) {
	if s == nil || s.cache == nil {
		return
	}
	if candidate.backendObservationKey != "" {
		s.cache.ObserveBackendFailure(candidate.backendObservationKey)
	}
	if addr := l4CandidateBackoffAddr(candidate); addr != "" && candidate.markBackoffOnFailure {
		s.cache.MarkFailure(addr)
	}
}

func (s *Server) observeCandidateSuccess(candidate l4Candidate, headerLatency time.Duration) {
	if s == nil || s.cache == nil || candidate.address == "" {
		return
	}
	if candidate.backendObservationKey != "" {
		s.cache.ObserveBackendSuccess(candidate.backendObservationKey, headerLatency, 0, 0)
	}
	s.cache.ObserveSuccess(l4CandidateBackoffAddr(candidate), headerLatency)
}
