package diagnostics

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
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

func probeDiagnosticRelayPaths(ctx context.Context, network string, target string, paths []relayplan.Path, provider relay.TLSMaterialProvider, cache *backends.Cache) ([]RelayPathReport, []int, error) {
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
		report := RelayPathReport{
			Path:      append([]int(nil), path.IDs...),
			Success:   success,
			LatencyMS: latencyMS,
			Hops:      relayPathHopReports(path.Hops, reportTarget, success, latencyMS, err, dialResult.HopTimings),
		}
		if err != nil {
			report.Error = err.Error()
		}
		reportsByPath[relayPathReportKey(path.IDs)] = len(reports)
		reports = append(reports, report)
	}

	selectedIndex := diagnosticSelectedRelayPathIndex(ctx, network, target, paths, provider, cache, reportsByPath, reports)
	if selectedIndex < 0 {
		return reports, nil, nil
	}
	reports[selectedIndex].Selected = true
	return reports, append([]int(nil), reports[selectedIndex].Path...), nil
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

func relayPathHopReports(hops []relay.Hop, target string, success bool, latencyMS float64, err error, timings []relay.HopTiming) []RelayHopReport {
	timingByListenerID, finalLatencyMS := relayHopTimingLookup(timings)
	reports := make([]RelayHopReport, 0, len(hops)+1)
	for i, hop := range hops {
		report := RelayHopReport{
			Success:        success,
			ToListenerID:   hop.Listener.ID,
			ToListenerName: hop.Listener.Name,
		}
		if i == 0 {
			report.From = "client"
		} else {
			previous := hops[i-1].Listener
			report.FromListenerID = previous.ID
			report.FromListenerName = previous.Name
		}
		if err != nil {
			report.Error = err.Error()
		}
		if success {
			report.LatencyMS = timingByListenerID[hop.Listener.ID]
		}
		reports = append(reports, report)
	}

	if success && finalLatencyMS > 0 {
		latencyMS = finalLatencyMS
	}
	final := RelayHopReport{
		To:        target,
		Success:   success,
		LatencyMS: latencyMS,
	}
	if len(hops) == 0 {
		final.From = "client"
	} else {
		previous := hops[len(hops)-1].Listener
		final.FromListenerID = previous.ID
		final.FromListenerName = previous.Name
	}
	if err != nil {
		final.Error = err.Error()
	}
	reports = append(reports, final)
	return reports
}

func relayHopTimingLookup(timings []relay.HopTiming) (map[int]float64, float64) {
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
