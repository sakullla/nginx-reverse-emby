package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestVerificationL4RoutesUseDistinctDestinationsAndRelayLayers(t *testing.T) {
	rules := verificationL4Rules(config{backendURL: "http://backend.test:9002", relayLayers: [][]int{{1}}, relay2Layers: [][]int{{1}, {2}}})
	if len(rules) != 9 {
		t.Fatalf("rules=%d", len(rules))
	}
	ports := make(map[int]bool)
	for _, rule := range rules {
		if ports[rule.ListenPort] || len(rule.Backends) != 1 || rule.Backends[0].Host != "backend.test" {
			t.Fatalf("invalid rule: %+v", rule)
		}
		ports[rule.ListenPort] = true
		if rule.ListenPort%100 == 3 && !reflect.DeepEqual(rule.RelayLayers, [][]int{{1}, {2}}) {
			t.Fatalf("missing two-hop path: %+v", rule)
		}
		if rule.Protocol == "udp" && rule.Backends[0].Port != 9003 {
			t.Fatalf("UDP target: %+v", rule)
		}
	}
}

func TestVerifyHTTPRejectsStatusCorruptionAndTruncation(t *testing.T) {
	for _, mode := range []string{"ok", "status", "corrupt", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "status":
					http.Error(w, "failed", 502)
				case "corrupt":
					_, _ = w.Write(make([]byte, 1<<20))
				case "truncated":
					w.Header().Set("Content-Length", "1048576")
					_, _ = w.Write([]byte("short"))
				default:
					if !serveVerificationHTTP(w, r) {
						http.NotFound(w, r)
					}
				}
			}))
			defer server.Close()
			err := verifyHTTP(server.URL)
			if (err == nil) != (mode == "ok") {
				t.Fatalf("verification error=%v", err)
			}
		})
	}
}
