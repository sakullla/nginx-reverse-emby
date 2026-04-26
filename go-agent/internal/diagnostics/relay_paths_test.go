package diagnostics

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
)

func TestRelayPathHopReportsMarksOnlyMatchedFailedRelayHop(t *testing.T) {
	hops := []relay.Hop{
		{
			Address: "relay-a.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   1,
				Name: "Relay A",
			},
		},
		{
			Address: "relay-b.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   2,
				Name: "nnc-mk-relay",
			},
		},
	}

	reports := relayPathHopReports(
		hops,
		"emby.example.com:443",
		false,
		40.5,
		errors.New("dial tcp relay-a.example.invalid:12202: connect: connection refused"),
		nil,
	)

	if len(reports) != 3 {
		t.Fatalf("reports = %+v", reports)
	}
	if reports[0].State != relayHopStateFailed || reports[0].Success {
		t.Fatalf("first hop = %+v, want failed", reports[0])
	}
	if reports[1].State != relayHopStateUntested || reports[1].Success {
		t.Fatalf("second hop = %+v, want untested", reports[1])
	}
	if reports[2].State != relayHopStateUntested || reports[2].Success {
		t.Fatalf("final hop = %+v, want untested", reports[2])
	}
}

func TestProbeDiagnosticRelayPathsFallsBackWhenFirstPathTimesOut(t *testing.T) {
	provider := newDiagnosticTLSMaterialProvider()
	paths := []relayplan.Path{
		{
			IDs: []int{601},
			Hops: []relay.Hop{{
				Address:  "relay-a.example.invalid:12202",
				Listener: model.RelayListener{ID: 601, Name: "Relay A"},
			}},
			Key: relayplan.PathKey("relay_path", []int{601}, "emby.example.com:443"),
		},
		{
			IDs: []int{602},
			Hops: []relay.Hop{{
				Address:  "relay-b.example.invalid:12202",
				Listener: model.RelayListener{ID: 602, Name: "Relay B"},
			}},
			Key: relayplan.PathKey("relay_path", []int{602}, "emby.example.com:443"),
		},
	}

	previousDialWithResult := diagnosticRelayDialWithResult
	previousProbePath := diagnosticRelayProbePath
	t.Cleanup(func() {
		diagnosticRelayDialWithResult = previousDialWithResult
		diagnosticRelayProbePath = previousProbePath
	})
	diagnosticRelayDialWithResult = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider, opts ...relay.DialOptions) (net.Conn, relay.DialResult, error) {
		if err := ctx.Err(); err != nil {
			return nil, relay.DialResult{}, err
		}
		if len(chain) > 0 && chain[0].Listener.ID == 601 {
			<-ctx.Done()
			return nil, relay.DialResult{}, ctx.Err()
		}
		client, server := net.Pipe()
		_ = server.Close()
		return client, relay.DialResult{SelectedAddress: target}, nil
	}
	diagnosticRelayProbePath = func(ctx context.Context, network, target string, chain []relay.Hop, provider relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
		return []relay.ProbeTiming{{ToListenerID: 602, LatencyMS: 1.2}, {LatencyMS: 2.4}}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	reports, selectedPath, err := probeDiagnosticRelayPaths(ctx, "tcp", "emby.example.com:443", paths, provider, nil, nil)
	if err != nil {
		t.Fatalf("probeDiagnosticRelayPaths() error = %v", err)
	}
	if len(selectedPath) != 1 || selectedPath[0] != 602 {
		t.Fatalf("selectedPath = %+v, want [602]", selectedPath)
	}
	if len(reports) != 2 || reports[0].Selected || reports[0].Success || !reports[1].Selected || !reports[1].Success {
		t.Fatalf("reports = %+v", reports)
	}
}

func TestRelayPathHopReportsMarksFinalHopFailedWhenTargetConnectFails(t *testing.T) {
	hops := []relay.Hop{
		{
			Address: "relay-a.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   1,
				Name: "Relay A",
			},
		},
		{
			Address: "relay-b.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   2,
				Name: "nnc-mk-relay",
			},
		},
	}

	reports := relayPathHopReports(
		hops,
		"emby.example.com:443",
		false,
		40.5,
		errors.New("relay connection failed: dial tcp emby.example.com:443: connect: connection refused"),
		nil,
	)

	if len(reports) != 3 {
		t.Fatalf("reports = %+v", reports)
	}
	if reports[0].State != relayHopStateSuccess || !reports[0].Success {
		t.Fatalf("first hop = %+v, want success", reports[0])
	}
	if reports[1].State != relayHopStateSuccess || !reports[1].Success {
		t.Fatalf("second hop = %+v, want success", reports[1])
	}
	if reports[2].State != relayHopStateFailed || reports[2].Success {
		t.Fatalf("final hop = %+v, want failed", reports[2])
	}
}

func TestRelayPathHopReportsDoesNotMatchEndpointSubstrings(t *testing.T) {
	hops := []relay.Hop{
		{
			Address: "example.com:443",
			Listener: model.RelayListener{
				ID:   1,
				Name: "Relay A",
			},
		},
		{
			Address: "relay-b.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   2,
				Name: "Relay B",
			},
		},
	}

	reports := relayPathHopReports(
		hops,
		"media.example.com:443",
		false,
		40.5,
		errors.New("relay connection failed: dial tcp media.example.com:443: connect: connection refused"),
		nil,
	)

	if len(reports) != 3 {
		t.Fatalf("reports = %+v", reports)
	}
	if reports[0].State != relayHopStateSuccess || !reports[0].Success {
		t.Fatalf("first hop = %+v, want success", reports[0])
	}
	if reports[1].State != relayHopStateSuccess || !reports[1].Success {
		t.Fatalf("second hop = %+v, want success", reports[1])
	}
	if reports[2].State != relayHopStateFailed || reports[2].Success {
		t.Fatalf("final hop = %+v, want failed", reports[2])
	}
}

func TestRelayPathHopReportsMarksFinalHopFailedWhenResolvedBackendAddressFails(t *testing.T) {
	hops := []relay.Hop{
		{
			Address: "relay-a.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   1,
				Name: "Relay A",
			},
		},
		{
			Address: "relay-b.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   2,
				Name: "Relay B",
			},
		},
	}

	reports := relayPathHopReports(
		hops,
		"203.0.113.10:443",
		false,
		40.5,
		errors.New("relay connection failed: dial tcp 203.0.113.10:443: connect: connection refused"),
		nil,
	)

	if len(reports) != 3 {
		t.Fatalf("reports = %+v", reports)
	}
	if reports[0].State != relayHopStateSuccess || !reports[0].Success {
		t.Fatalf("first hop = %+v, want success", reports[0])
	}
	if reports[1].State != relayHopStateSuccess || !reports[1].Success {
		t.Fatalf("second hop = %+v, want success", reports[1])
	}
	if reports[2].State != relayHopStateFailed || reports[2].Success {
		t.Fatalf("final hop = %+v, want failed", reports[2])
	}
}

func TestRelayPathHopReportsDoesNotInferFinalHopFromMatchingPort(t *testing.T) {
	hops := []relay.Hop{
		{
			Address: "relay-a.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   1,
				Name: "Relay A",
			},
		},
		{
			Address: "relay-b.example.invalid:443",
			Listener: model.RelayListener{
				ID:   2,
				Name: "Relay B",
			},
		},
	}

	reports := relayPathHopReports(
		hops,
		"emby.example.com:443",
		false,
		40.5,
		errors.New("relay connection failed: dial tcp 203.0.113.10:443: connect: connection refused"),
		nil,
	)

	if len(reports) != 3 {
		t.Fatalf("reports = %+v", reports)
	}
	for _, report := range reports {
		if report.State != relayHopStateUntested || report.Success {
			t.Fatalf("matching-port report = %+v, want untested", reports)
		}
	}
}

func TestRelayPathHopReportsUsesPortAwareRelayMatchingForSharedHost(t *testing.T) {
	hops := []relay.Hop{
		{
			Address: "relay-shared.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   1,
				Name: "Relay A",
			},
		},
		{
			Address: "relay-shared.example.invalid:12203",
			Listener: model.RelayListener{
				ID:   2,
				Name: "Relay B",
			},
		},
	}

	reports := relayPathHopReports(
		hops,
		"emby.example.com:443",
		false,
		40.5,
		errors.New("dial tcp relay-shared.example.invalid:12203: connect: connection refused"),
		nil,
	)

	if len(reports) != 3 {
		t.Fatalf("reports = %+v", reports)
	}
	if reports[0].State != relayHopStateSuccess || !reports[0].Success {
		t.Fatalf("first hop = %+v, want success", reports[0])
	}
	if reports[1].State != relayHopStateFailed || reports[1].Success {
		t.Fatalf("second hop = %+v, want failed", reports[1])
	}
	if reports[2].State != relayHopStateUntested || reports[2].Success {
		t.Fatalf("final hop = %+v, want untested", reports[2])
	}
}

func TestRelayPathHopReportsMatchesDownstreamRelayDNSFailureByHost(t *testing.T) {
	hops := []relay.Hop{
		{
			Address: "relay-a.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   1,
				Name: "Relay A",
			},
		},
		{
			Address: "relay-b.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   2,
				Name: "Relay B",
			},
		},
	}

	reports := relayPathHopReports(
		hops,
		"emby.example.com:443",
		false,
		40.5,
		errors.New("lookup relay-b.example.invalid: no such host"),
		nil,
	)

	if len(reports) != 3 {
		t.Fatalf("reports = %+v", reports)
	}
	if reports[0].State != relayHopStateSuccess || !reports[0].Success {
		t.Fatalf("first hop = %+v, want success", reports[0])
	}
	if reports[1].State != relayHopStateFailed || reports[1].Success {
		t.Fatalf("second hop = %+v, want failed", reports[1])
	}
	if reports[2].State != relayHopStateUntested || reports[2].Success {
		t.Fatalf("final hop = %+v, want untested", reports[2])
	}
}

func TestRelayPathHopReportsDoesNotChooseRelayForAmbiguousLookupHost(t *testing.T) {
	hops := []relay.Hop{
		{
			Address: "relay-shared.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   1,
				Name: "Relay A",
			},
		},
		{
			Address: "relay-shared.example.invalid:12203",
			Listener: model.RelayListener{
				ID:   2,
				Name: "Relay B",
			},
		},
	}

	reports := relayPathHopReports(
		hops,
		"emby.example.com:443",
		false,
		40.5,
		errors.New("lookup relay-shared.example.invalid: no such host"),
		nil,
	)

	if len(reports) != 3 {
		t.Fatalf("reports = %+v", reports)
	}
	for _, report := range reports {
		if report.State != relayHopStateUntested || report.Success {
			t.Fatalf("ambiguous lookup report = %+v, want untested", reports)
		}
	}
}

func TestRelayPathHopReportsMatchesFinalHopDNSFailureByHost(t *testing.T) {
	hops := []relay.Hop{
		{
			Address: "relay-a.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   1,
				Name: "Relay A",
			},
		},
		{
			Address: "relay-b.example.invalid:12202",
			Listener: model.RelayListener{
				ID:   2,
				Name: "Relay B",
			},
		},
	}

	reports := relayPathHopReports(
		hops,
		"emby.example.com:443",
		false,
		40.5,
		errors.New("lookup emby.example.com: no such host"),
		nil,
	)

	if len(reports) != 3 {
		t.Fatalf("reports = %+v", reports)
	}
	if reports[0].State != relayHopStateSuccess || !reports[0].Success {
		t.Fatalf("first hop = %+v, want success", reports[0])
	}
	if reports[1].State != relayHopStateSuccess || !reports[1].Success {
		t.Fatalf("second hop = %+v, want success", reports[1])
	}
	if reports[2].State != relayHopStateFailed || reports[2].Success {
		t.Fatalf("final hop = %+v, want failed", reports[2])
	}
}
