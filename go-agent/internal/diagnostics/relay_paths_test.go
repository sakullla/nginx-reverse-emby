package diagnostics

import (
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
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
