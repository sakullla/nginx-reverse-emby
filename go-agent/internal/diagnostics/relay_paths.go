package diagnostics

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
)

const (
	relayHopStateSuccess  = "success"
	relayHopStateFailed   = "failed"
	relayHopStateUntested = "untested"
)

func resolveDiagnosticRelayPaths(ruleLabel string, chain []int, layers [][]int, relayListeners []model.RelayListener, target string) ([]relayplan.Path, error) {
	normalizedLayers := relayplan.NormalizeLayers(chain, layers)
	pathIDs, err := relayplan.ExpandPaths(normalizedLayers, 32)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ruleLabel, err)
	}
	if len(pathIDs) == 0 {
		return nil, nil
	}

	relayListenersByID := make(map[int]model.RelayListener, len(relayListeners))
	for _, listener := range relayListeners {
		relayListenersByID[listener.ID] = listener
	}

	paths := make([]relayplan.Path, 0, len(pathIDs))
	for _, ids := range pathIDs {
		hops := make([]relay.Hop, 0, len(ids))
		for _, listenerID := range ids {
			listener, ok := relayListenersByID[listenerID]
			if !ok {
				return nil, fmt.Errorf("%s: relay listener %d not found", ruleLabel, listenerID)
			}
			if !listener.Enabled {
				return nil, fmt.Errorf("%s: relay listener %d is disabled", ruleLabel, listenerID)
			}
			if err := relay.ValidateListener(listener); err != nil {
				return nil, fmt.Errorf("%s: relay listener %d: %w", ruleLabel, listenerID, err)
			}
			host, port := relayHopDialEndpoint(listener)
			hops = append(hops, relay.Hop{
				Address:    net.JoinHostPort(host, strconv.Itoa(port)),
				ServerName: host,
				Listener:   listener,
			})
		}
		paths = append(paths, relayplan.Path{
			IDs:  append([]int(nil), ids...),
			Hops: hops,
			Key:  relayplan.PathKey("relay_path", ids, target),
		})
	}
	return paths, nil
}

func probeDiagnosticRelayPaths(ctx context.Context, network string, target string, paths []relayplan.Path, provider relay.TLSMaterialProvider, cache *backends.Cache, preferenceCache *backends.Cache) ([]RelayPathReport, []int, error) {
	if len(paths) == 0 {
		return nil, nil, nil
	}
	if provider == nil {
		return nil, nil, fmt.Errorf("relay provider is required")
	}

	reports := make([]RelayPathReport, 0, len(paths))
	reportsByPath := make(map[string]int, len(paths))
	for _, path := range paths {
		startedAt := time.Now()
		conn, dialResult, err := diagnosticRelayDialWithResult(ctx, network, target, path.Hops, provider)
		if conn != nil {
			_ = conn.Close()
		}
		latencyMS := roundMetric(float64(time.Since(startedAt)) / float64(time.Millisecond))
		success := err == nil
		if success && latencyMS <= 0 {
			latencyMS = 0.1
		}
		reportTarget := resolveProbeAddress(target, dialResult.SelectedAddress)
		var probeTimings []relay.ProbeTiming
		if success {
			if timings, probeErr := diagnosticRelayProbePath(ctx, network, reportTarget, path.Hops, provider); probeErr == nil {
				probeTimings = timings
			}
		}
		report := RelayPathReport{
			Path:      append([]int(nil), path.IDs...),
			Success:   success,
			LatencyMS: latencyMS,
			Hops:      relayPathHopReports(path.Hops, reportTarget, success, latencyMS, err, probeTimings),
		}
		if err != nil {
			report.Error = err.Error()
		}
		reportsByPath[relayPathReportKey(path.IDs)] = len(reports)
		reports = append(reports, report)
	}

	selectedIndex := preferredRelayPathIndex(target, paths, preferenceCache, reportsByPath, reports)
	if selectedIndex < 0 {
		selectedIndex = diagnosticSelectedRelayPathIndex(ctx, network, target, paths, provider, cache, reportsByPath, reports)
	}
	if selectedIndex < 0 {
		return reports, nil, nil
	}
	reports[selectedIndex].Selected = true
	return reports, append([]int(nil), reports[selectedIndex].Path...), nil
}

func preferredRelayPathIndex(target string, paths []relayplan.Path, cache *backends.Cache, reportsByPath map[string]int, reports []RelayPathReport) int {
	if cache == nil || len(paths) == 0 {
		return -1
	}
	candidates := make([]backends.Candidate, 0, len(paths))
	pathsByKey := make(map[string]relayplan.Path, len(paths))
	observed := false
	for _, path := range paths {
		key := path.Key
		if strings.TrimSpace(key) == "" {
			key = relayplan.PathKey("relay_path", path.IDs, target)
		}
		summary := cache.Summary(backends.BackendObservationKey(relayplan.RelayPathScope(target), key))
		if observationSummaryHasHistory(summary) {
			observed = true
		}
		pathsByKey[key] = path
		candidates = append(candidates, backends.Candidate{Address: key})
	}
	if !observed {
		return -1
	}
	ordered := cache.Order(relayplan.RelayPathScope(target), backends.StrategyAdaptive, candidates)
	for _, candidate := range ordered {
		path, ok := pathsByKey[candidate.Address]
		if !ok {
			continue
		}
		index, ok := reportsByPath[relayPathReportKey(path.IDs)]
		if ok && reports[index].Success {
			return index
		}
	}
	return -1
}

func diagnosticSelectedRelayPathIndex(ctx context.Context, network string, target string, paths []relayplan.Path, provider relay.TLSMaterialProvider, cache *backends.Cache, reportsByPath map[string]int, reports []RelayPathReport) int {
	racer := relayplan.Racer{Dialer: diagnosticRelayReportPathDialer{provider: provider}, Cache: cache, Concurrency: 3, MaxPaths: 32}
	result, err := racer.Race(ctx, relayplan.Request{
		Network: network,
		Target:  target,
		Paths:   paths,
	})
	if result.Conn != nil {
		_ = result.Conn.Close()
	}
	if err != nil {
		return firstSuccessfulRelayPathIndex(paths, reportsByPath, reports)
	}
	index, ok := reportsByPath[relayPathReportKey(result.Selected.IDs)]
	if !ok {
		return -1
	}
	return index
}

type diagnosticRelayReportPathDialer struct {
	provider relay.TLSMaterialProvider
}

func (d diagnosticRelayReportPathDialer) DialPath(ctx context.Context, req relayplan.Request, path relayplan.Path) (net.Conn, relay.DialResult, error) {
	return diagnosticRelayDialWithResult(ctx, req.Network, req.Target, path.Hops, d.provider, req.Options...)
}

func firstSuccessfulRelayPathIndex(paths []relayplan.Path, reportsByPath map[string]int, reports []RelayPathReport) int {
	for _, path := range paths {
		index, ok := reportsByPath[relayPathReportKey(path.IDs)]
		if !ok || !reports[index].Success {
			continue
		}
		return index
	}
	return -1
}

func relayPathReportKey(path []int) string {
	return relayplan.PathKey("relay_path_report", path, "")
}

func relayPathHopReports(hops []relay.Hop, target string, success bool, latencyMS float64, err error, timings []relay.ProbeTiming) []RelayHopReport {
	timingByListenerID, finalLatencyMS := relayHopTimingLookup(timings)
	failedIndex := relayPathFailedHopIndex(hops, target, err)
	reports := make([]RelayHopReport, 0, len(hops)+1)
	for i, hop := range hops {
		state := relayHopStateForIndex(i, len(hops), success, failedIndex)
		report := RelayHopReport{
			Success:        success,
			State:          state,
			ToListenerID:   hop.Listener.ID,
			ToListenerName: hop.Listener.Name,
			ToAgentName:    relayListenerNodeName(hop.Listener),
		}
		report.Success = state == relayHopStateSuccess
		if i == 0 {
			report.From = "client"
		} else {
			previous := hops[i-1].Listener
			report.FromListenerID = previous.ID
			report.FromListenerName = previous.Name
			report.FromAgentName = relayListenerNodeName(previous)
		}
		if state == relayHopStateFailed && err != nil {
			report.Error = err.Error()
		}
		if report.Success {
			report.LatencyMS = timingByListenerID[hop.Listener.ID]
		}
		reports = append(reports, report)
	}

	finalHopLatencyMS := finalLatencyMS
	if len(hops) == 0 && success && finalHopLatencyMS <= 0 {
		finalHopLatencyMS = latencyMS
	}
	finalState := relayHopStateForIndex(len(hops), len(hops), success, failedIndex)
	final := RelayHopReport{
		To:        target,
		Success:   finalState == relayHopStateSuccess,
		State:     finalState,
		LatencyMS: finalHopLatencyMS,
	}
	if len(hops) == 0 {
		final.From = "client"
	} else {
		previous := hops[len(hops)-1].Listener
		final.FromListenerID = previous.ID
		final.FromListenerName = previous.Name
		final.FromAgentName = relayListenerNodeName(previous)
	}
	if final.State == relayHopStateFailed && err != nil {
		final.Error = err.Error()
	}
	reports = append(reports, final)
	return reports
}

func relayHopStateForIndex(index int, finalIndex int, pathSuccess bool, failedIndex int) string {
	if pathSuccess {
		return relayHopStateSuccess
	}
	if failedIndex < 0 {
		if finalIndex == 0 && index == 0 {
			return relayHopStateFailed
		}
		return relayHopStateUntested
	}
	switch {
	case index < failedIndex:
		return relayHopStateSuccess
	case index == failedIndex:
		return relayHopStateFailed
	default:
		return relayHopStateUntested
	}
}

func relayPathFailedHopIndex(hops []relay.Hop, target string, err error) int {
	if err == nil {
		return -1
	}
	message := err.Error()
	for i, hop := range hops {
		if endpointHostPortAppearsInError(message, hop.Address) {
			return i
		}
	}
	if endpointHostPortAppearsInError(message, target) {
		return len(hops)
	}
	if lookupHost := lookupHostFromError(message); lookupHost != "" {
		return relayPathFailedLookupHostIndex(hops, target, lookupHost)
	}
	return -1
}

func endpointHostPortAppearsInError(message string, endpoint string) bool {
	message = strings.TrimSpace(message)
	endpoint = strings.TrimSpace(endpoint)
	if message == "" || endpoint == "" {
		return false
	}
	if endpointTokenAppearsInError(message, endpoint) {
		return true
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return false
	}
	host = strings.Trim(host, "[]")
	if endpointTokenAppearsInError(message, net.JoinHostPort(host, port)) {
		return true
	}
	if !strings.Contains(host, ":") && endpointTokenAppearsInError(message, host+":"+port) {
		return true
	}
	return false
}

func endpointTokenAppearsInError(message string, endpoint string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	endpoint = strings.ToLower(strings.TrimSpace(endpoint))
	if message == "" || endpoint == "" {
		return false
	}
	offset := 0
	for {
		index := strings.Index(message[offset:], endpoint)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(endpoint)
		if endpointTokenBoundaryBefore(message, start) && endpointTokenBoundaryAfter(message, end) {
			return true
		}
		offset = start + 1
	}
}

func endpointTokenBoundaryBefore(message string, start int) bool {
	if start <= 0 {
		return true
	}
	return !isEndpointTokenChar(message[start-1])
}

func endpointTokenBoundaryAfter(message string, end int) bool {
	if end >= len(message) {
		return true
	}
	next := message[end]
	if next == ':' {
		return true
	}
	return !isEndpointTokenChar(next)
}

func isEndpointTokenChar(ch byte) bool {
	return ch == '.' || ch == '-' || ch == '_' || ch == '[' || ch == ']' || ch == '%' ||
		(ch >= '0' && ch <= '9') ||
		(ch >= 'a' && ch <= 'z')
}

func lookupHostFromError(message string) string {
	const marker = "lookup "
	index := strings.Index(message, marker)
	if index < 0 {
		return ""
	}
	remainder := strings.TrimSpace(message[index+len(marker):])
	if remainder == "" {
		return ""
	}
	host := strings.Fields(remainder)[0]
	return strings.Trim(strings.TrimSuffix(host, ":"), "[]")
}

func relayPathFailedLookupHostIndex(hops []relay.Hop, target string, lookupHost string) int {
	lookupHost = strings.Trim(lookupHost, "[]")
	matchedIndex := -1
	matches := 0
	for i, hop := range hops {
		if endpointHostEquals(hop.Address, lookupHost) {
			matchedIndex = i
			matches++
		}
	}
	if endpointHostEquals(target, lookupHost) {
		matchedIndex = len(hops)
		matches++
	}
	if matches != 1 {
		return -1
	}
	return matchedIndex
}

func endpointHostEquals(endpoint string, host string) bool {
	endpointHost, _, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.Trim(endpointHost, "[]"), strings.Trim(host, "[]"))
}

func relayListenerNodeName(listener relay.Listener) string {
	if name := strings.TrimSpace(listener.AgentName); name != "" {
		return name
	}
	return strings.TrimSpace(listener.AgentID)
}

func relayHopTimingLookup(timings []relay.ProbeTiming) (map[int]float64, float64) {
	byListenerID := make(map[int]float64, len(timings))
	finalLatencyMS := 0.0
	for _, timing := range timings {
		if timing.LatencyMS <= 0 {
			continue
		}
		if timing.ToListenerID > 0 {
			byListenerID[timing.ToListenerID] = timing.LatencyMS
			continue
		}
		finalLatencyMS = timing.LatencyMS
	}
	return byListenerID, finalLatencyMS
}
