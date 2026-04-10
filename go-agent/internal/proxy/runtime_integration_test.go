package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestHTTPRuntimeAppliesHostHeadersProxyRedirectAndRoundRobin(t *testing.T) {
	type backendObservation struct {
		ForwardedHost  string
		ForwardedPort  string
		ForwardedProto string
	}

	var (
		mu           sync.Mutex
		observations = map[string][]backendObservation{
			"one": {},
			"two": {},
		}
	)

	newBackend := func(name string) *httptest.Server {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			observations[name] = append(observations[name], backendObservation{
				ForwardedHost:  r.Header.Get("X-Forwarded-Host"),
				ForwardedPort:  r.Header.Get("X-Forwarded-Port"),
				ForwardedProto: r.Header.Get("X-Forwarded-Proto"),
			})
			mu.Unlock()

			w.Header().Set("Location", server.URL+"/redirected/"+name)
			w.WriteHeader(http.StatusFound)
		}))
		return server
	}

	backendOne := newBackend("one")
	defer backendOne.Close()
	backendTwo := newBackend("two")
	defer backendTwo.Close()

	server := NewServer(model.HTTPListener{
		Rules: []model.HTTPRule{{
			FrontendURL: "http://Panel.Example.Test",
			BackendURL:  backendOne.URL,
			Backends: []model.HTTPBackend{
				{URL: backendOne.URL},
				{URL: backendTwo.URL},
			},
			PassProxyHeaders: true,
			ProxyRedirect:    true,
			LoadBalancing: model.LoadBalancing{
				Strategy: "round_robin",
			},
		}},
	})
	proxy := httptest.NewServer(server)
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("failed to parse proxy URL: %v", err)
	}
	frontendPort := proxyURL.Port()
	if frontendPort == "" {
		t.Fatalf("proxy URL does not include a port: %q", proxy.URL)
	}

	if _, err := strconv.Atoi(frontendPort); err != nil {
		t.Fatalf("proxy URL port is invalid: %q", frontendPort)
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	send := func() *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, proxy.URL+"/entry", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		req.Host = "PANEL.EXAMPLE.TEST"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("runtime request failed: %v", err)
		}
		return resp
	}

	respOne := send()
	defer respOne.Body.Close()
	respTwo := send()
	defer respTwo.Body.Close()

	if respOne.StatusCode != http.StatusFound || respTwo.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 responses, got first=%d second=%d", respOne.StatusCode, respTwo.StatusCode)
	}

	checkLocation := func(rawLocation string) string {
		t.Helper()
		parsed, err := url.Parse(rawLocation)
		if err != nil {
			t.Fatalf("failed to parse rewritten location %q: %v", rawLocation, err)
		}
		if normalizeHost(parsed.Host) != "panel.example.test" {
			t.Fatalf("expected frontend host in rewritten location, got %q", parsed.Host)
		}
		if parsed.Port() != "" {
			t.Fatalf("expected rewritten location without explicit port, got %q", parsed.Port())
		}
		return parsed.Path
	}

	pathOne := checkLocation(respOne.Header.Get("Location"))
	pathTwo := checkLocation(respTwo.Header.Get("Location"))
	if pathOne == pathTwo {
		t.Fatalf("expected round-robin backend redirects to differ, got same path %q", pathOne)
	}

	mu.Lock()
	oneCalls := len(observations["one"])
	twoCalls := len(observations["two"])
	var headers backendObservation
	if oneCalls == 1 {
		headers = observations["one"][0]
	} else if twoCalls == 1 {
		headers = observations["two"][0]
	}
	mu.Unlock()

	if oneCalls != 1 || twoCalls != 1 {
		t.Fatalf("expected one request per backend via round robin, got backendOne=%d backendTwo=%d", oneCalls, twoCalls)
	}
	if headers.ForwardedHost != "PANEL.EXAMPLE.TEST" {
		t.Fatalf("expected X-Forwarded-Host to preserve incoming host, got %q", headers.ForwardedHost)
	}
	if headers.ForwardedProto != "http" {
		t.Fatalf("expected X-Forwarded-Proto=http, got %q", headers.ForwardedProto)
	}
	if headers.ForwardedPort != frontendPort {
		t.Fatalf("expected X-Forwarded-Port=%s, got %q", frontendPort, headers.ForwardedPort)
	}
}
