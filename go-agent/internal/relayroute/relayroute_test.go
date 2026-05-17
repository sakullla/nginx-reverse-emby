package relayroute

import (
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
)

func testListener(id int) model.RelayListener {
	return model.RelayListener{
		ID:         id,
		ListenHost: "127.0.0.1",
		ListenPort: 8000 + id,
		PublicHost: "relay.example.com",
		PublicPort: 9000 + id,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: "abc",
		}},
	}
}

func TestUsesRelayDetectsCanonicalLayersOnly(t *testing.T) {
	if UsesRelay(nil, nil) {
		t.Fatal("UsesRelay(nil, nil) = true, want false")
	}
	if UsesRelay([]int{1}, nil) {
		t.Fatal("UsesRelay(chain, nil) = true, want false")
	}
	if UsesRelay(nil, [][]int{{}}) {
		t.Fatal("UsesRelay(nil, empty layer) = true, want false")
	}
	if !UsesRelay(nil, [][]int{{1, 2}}) {
		t.Fatal("UsesRelay(nil, layers) = false, want true")
	}
}

func TestResolvePathsBuildsHopsAndKeys(t *testing.T) {
	paths, err := ResolvePaths("http rule \"https://app.example\"", nil, [][]int{{1}}, []model.RelayListener{testListener(1)}, "backend.example:443")
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if len(paths) != 1 || len(paths[0].Hops) != 1 {
		t.Fatalf("paths = %+v, want one path with one hop", paths)
	}
	hop := paths[0].Hops[0]
	if hop.Address != "relay.example.com:9001" || hop.ServerName != "relay.example.com" {
		t.Fatalf("hop = %+v, want public endpoint", hop)
	}
	wantKey := relayplan.PathKey("relay_path", []int{1}, "backend.example:443")
	if paths[0].Key != wantKey {
		t.Fatalf("path key = %q, want %q", paths[0].Key, wantKey)
	}
}

func TestResolvePathsUsesWireGuardListenerAddressInsideTunnel(t *testing.T) {
	profileID := 7
	listener := testListener(1)
	listener.ListenHost = "10.71.0.1"
	listener.BindHosts = []string{"10.71.0.1"}
	listener.ListenPort = 7443
	listener.PublicHost = "relay.example.com"
	listener.PublicPort = 51820
	listener.TransportMode = relay.ListenerTransportModeWireGuard
	listener.WireGuardProfileID = &profileID
	listener.TLSMode = ""
	listener.PinSet = nil

	paths, err := ResolvePaths("l4 rule 0.0.0.0:9443", nil, [][]int{{1}}, []model.RelayListener{listener}, "backend.example:443")
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if len(paths) != 1 || len(paths[0].Hops) != 1 {
		t.Fatalf("paths = %+v, want one path with one hop", paths)
	}
	hop := paths[0].Hops[0]
	if hop.Address != "10.71.0.1:7443" {
		t.Fatalf("hop address = %q, want WireGuard tunnel listener address", hop.Address)
	}
	if hop.ServerName != "relay.example.com" {
		t.Fatalf("hop server name = %q, want public host for identity", hop.ServerName)
	}
}

func TestResolvePathsDoesNotAliasInputLayers(t *testing.T) {
	layers := [][]int{{1}}
	paths, err := ResolvePaths("rule", nil, layers, []model.RelayListener{testListener(1)}, "backend.example:443")
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	layers[0][0] = 99
	if paths[0].IDs[0] != 1 {
		t.Fatalf("ResolvePaths() IDs aliased input layers: paths=%+v layers=%+v", paths, layers)
	}
}

func TestResolvePathsAllocations(t *testing.T) {
	listeners := benchmarkRelayListeners(12)
	layers := [][]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}
	allocs := testing.AllocsPerRun(1000, func() {
		paths, err := ResolvePaths("benchmark rule", nil, layers, listeners, "backend.example:443")
		if err != nil {
			t.Fatalf("ResolvePaths() error = %v", err)
		}
		if len(paths) != 27 {
			t.Fatalf("ResolvePaths() paths = %d, want 27", len(paths))
		}
	})
	if allocs > 515 {
		t.Fatalf("ResolvePaths() allocations = %.2f, want <= 515", allocs)
	}
}

func TestResolvePathsWrapsMissingListenerWithLabel(t *testing.T) {
	_, err := ResolvePaths("l4 rule 127.0.0.1:8443", nil, [][]int{{2}}, []model.RelayListener{testListener(1)}, "")
	if err == nil || !strings.Contains(err.Error(), "l4 rule 127.0.0.1:8443: relay listener 2 not found") {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
}

func TestResolvePathsIgnoresRelayChainOnly(t *testing.T) {
	paths, err := ResolvePaths("http rule \"https://app.example\"", []int{1}, nil, []model.RelayListener{testListener(1)}, "backend.example:443")
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("ResolvePaths() = %+v, want no paths", paths)
	}
}

func TestClonePathsWithTargetDoesNotAliasSlices(t *testing.T) {
	paths, err := ResolvePaths("rule", nil, [][]int{{1}}, []model.RelayListener{testListener(1)}, "")
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	cloned := ClonePathsWithTarget(paths, "backend.example:443")
	cloned[0].IDs[0] = 99
	cloned[0].Hops[0].Address = "changed"
	if paths[0].IDs[0] != 1 || paths[0].Hops[0].Address == "changed" {
		t.Fatalf("ClonePathsWithTarget aliases original path: original=%+v cloned=%+v", paths, cloned)
	}
	if cloned[0].Key != relayplan.PathKey("relay_path", []int{1}, "backend.example:443") {
		t.Fatalf("cloned key = %q", cloned[0].Key)
	}
}
