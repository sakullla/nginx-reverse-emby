package http

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestHTTPEffectiveInputsIgnoresUnreferencedRelayListenerChanges(t *testing.T) {
	rules := []model.HTTPRule{{
		ID:          1,
		FrontendURL: "http://edge.example.test:8080",
		Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
		RelayLayers: [][]int{{101}, {102, 103}},
		Enabled:     true,
	}}
	previous := model.Snapshot{
		Rules: rules,
		RelayListeners: []model.RelayListener{
			testHTTPRelayListener(101, "relay-a.example.test"),
			testHTTPRelayListener(102, "relay-b.example.test"),
			testHTTPRelayListener(103, "relay-c.example.test"),
			testHTTPRelayListener(999, "unrelated-old.example.test"),
		},
	}
	next := model.Snapshot{
		Rules: rules,
		RelayListeners: []model.RelayListener{
			testHTTPRelayListener(101, "relay-a.example.test"),
			testHTTPRelayListener(102, "relay-b.example.test"),
			testHTTPRelayListener(103, "relay-c.example.test"),
			testHTTPRelayListener(999, "unrelated-new.example.test"),
		},
	}

	if !httpEffectiveInputsEqual(previous, next) {
		t.Fatal("httpEffectiveInputsEqual() = false, want true for unreferenced relay listener change")
	}
}

func TestHTTPEffectiveInputsDetectsReferencedRelayListenerChanges(t *testing.T) {
	rules := []model.HTTPRule{{
		ID:          1,
		FrontendURL: "http://edge.example.test:8080",
		Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
		RelayLayers: [][]int{{101}, {102}},
		Enabled:     true,
	}}
	previous := model.Snapshot{
		Rules: rules,
		RelayListeners: []model.RelayListener{
			testHTTPRelayListener(101, "relay-a.example.test"),
			testHTTPRelayListener(102, "relay-b.example.test"),
			testHTTPRelayListener(999, "unrelated.example.test"),
		},
	}
	next := model.Snapshot{
		Rules: rules,
		RelayListeners: []model.RelayListener{
			testHTTPRelayListener(101, "relay-a.example.test"),
			testHTTPRelayListener(102, "relay-b-new.example.test"),
			testHTTPRelayListener(999, "unrelated.example.test"),
		},
	}

	if httpEffectiveInputsEqual(previous, next) {
		t.Fatal("httpEffectiveInputsEqual() = true, want false for referenced relay listener change")
	}
}

func testHTTPRelayListener(id int, host string) model.RelayListener {
	return model.RelayListener{
		ID:            id,
		Name:          host,
		ListenHost:    "127.0.0.1",
		ListenPort:    9000 + id,
		PublicHost:    host,
		PublicPort:    9443,
		Enabled:       true,
		TransportMode: "tls",
	}
}
