package l4

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestRelayPrefetchRequiresOneDestination(t *testing.T) {
	rule := model.L4Rule{Backends: []model.L4Backend{{Host: "backend.example", Port: 443}}, RelayLayers: [][]int{{1}, {2}}}
	server := &Server{relayListenersByID: map[int]model.RelayListener{1: {TransportMode: "tls_tcp"}, 2: {TransportMode: "tls_tcp"}}}
	if !server.canPrefetchRelayPayload(rule) {
		t.Fatal("single relay path must carry queued bytes")
	}
	for _, mutate := range []func(*model.L4Rule){
		func(r *model.L4Rule) {
			r.Backends = append(r.Backends, model.L4Backend{Host: "other.example", Port: 443})
		},
		func(r *model.L4Rule) { r.RelayLayers = [][]int{{1, 2}} },
		func(r *model.L4Rule) { r.RelayLayers = nil },
		func(r *model.L4Rule) { r.Tuning.ProxyProtocol.Send = true },
	} {
		candidate := rule
		mutate(&candidate)
		if server.canPrefetchRelayPayload(candidate) {
			t.Fatal("must not send application bytes before selecting a unique destination or before its PROXY header")
		}
	}
	server.relayListenersByID[2] = model.RelayListener{TransportMode: "quic", AllowTransportFallback: true}
	if server.canPrefetchRelayPayload(rule) {
		t.Fatal("transport fallback must not replay application bytes")
	}
}
