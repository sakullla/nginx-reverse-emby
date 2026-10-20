//go:build !integration

package diagnostics

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func TestProbeBackendReachabilityRecordsHTTPStatusWithoutBusinessSuccess(t *testing.T) {
	statuses := []int{
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusMethodNotAllowed,
		http.StatusOK,
		http.StatusBadGateway,
	}
	for _, status := range statuses {
		status := status
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/emby" {
					t.Errorf("path = %s", r.URL.Path)
				}
				w.WriteHeader(status)
			}))
			t.Cleanup(server.Close)

			target := server.URL + "/emby"
			snapshot := model.Snapshot{
				Revision: 4,
				Rules: []model.HTTPRule{{
					ID:          7,
					FrontendURL: "http://edge.example/in",
					Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:1/other"}},
				}},
			}
			handler := newProbeHandler(t, "edge-1", snapshot)
			result, err := handler.HandleTask(context.Background(), control.TaskMessage{
				TaskType:   control.TaskTypeProbeBackendReachability,
				RawPayload: map[string]any{"target_url": target},
			})
			if err != nil {
				t.Fatalf("HandleTask() error = %v", err)
			}
			if result[control.ProbeResultStatus] != control.ProbeStatusHTTPStatus {
				t.Fatalf("status = %#v, want http_status", result[control.ProbeResultStatus])
			}
			if result[control.ProbeResultHTTPStatusCode] != status {
				t.Fatalf("http status = %#v, want %d", result[control.ProbeResultHTTPStatusCode], status)
			}
			assertReachabilityIdentity(t, result, "edge-1", target)
			if _, ok := result["succeeded"]; ok {
				t.Fatal("http status was recorded as business success")
			}
			if _, ok := result[control.ProbeResultRelayListenerID]; ok {
				t.Fatal("direct probe recorded a relay listener")
			}
			assertSnapshotUnchanged(t, handler, snapshot)
		})
	}
}

func TestProbeBackendReachabilityNetworkErrorStaysDistinctFromConnected(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	target := "http://" + address + "/emby"
	handler := newProbeHandler(t, "edge-1", model.Snapshot{})
	result, err := handler.HandleTask(context.Background(), control.TaskMessage{
		TaskType:   control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{"target_url": "  " + target + "  "},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}
	if result[control.ProbeResultStatus] != control.ProbeStatusNetworkError {
		t.Fatalf("status = %#v, want network_error", result[control.ProbeResultStatus])
	}
	if result[control.ProbeResultTarget] != target {
		t.Fatalf("target = %#v", result[control.ProbeResultTarget])
	}
	if _, ok := result[control.ProbeResultHTTPStatusCode]; ok {
		t.Fatal("network error included an HTTP status")
	}
	if detail, _ := result[control.ProbeResultError].(string); detail == "" {
		t.Fatal("network error omitted dial evidence")
	}
}

func TestClassifyLiteralDialDoesNotPromotePartialTCPToConnected(t *testing.T) {
	status, code := classifyLiteralDial(literalDialObservation{statusCode: http.StatusUnauthorized, tcpConnected: true})
	if status != control.ProbeStatusHTTPStatus || code != http.StatusUnauthorized {
		t.Fatalf("401 classification = %s %d", status, code)
	}
	status, _ = classifyLiteralDial(literalDialObservation{tcpConnected: true})
	if status != control.ProbeStatusUntested {
		t.Fatalf("TCP without HTTP status = %s, want untested", status)
	}
	status, _ = classifyLiteralDial(literalDialObservation{err: errors.New("eof"), tcpConnected: true})
	if status != control.ProbeStatusNetworkError {
		t.Fatalf("TCP then protocol error = %s, want network_error", status)
	}
}

func TestProbeBackendReachabilityKeepsInsufficientTargetsUntested(t *testing.T) {
	handler := newProbeHandler(t, "edge-1", model.Snapshot{})
	handler.probeLiteral = func(context.Context, *url.URL, time.Duration) literalDialObservation {
		t.Fatal("invalid target was dialed")
		return literalDialObservation{}
	}
	result, err := handler.HandleTask(context.Background(), control.TaskMessage{
		TaskType:   control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{"target_url": "ftp://files.example/library"},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}
	if result[control.ProbeResultStatus] != control.ProbeStatusUntested {
		t.Fatalf("status = %#v, want untested", result[control.ProbeResultStatus])
	}
	if _, err := handler.HandleTask(context.Background(), control.TaskMessage{
		TaskType:   control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{},
	}); err == nil {
		t.Fatal("missing target_url returned a result")
	}
}

func TestProbeBackendReachabilityProbesOnlyExistingHop(t *testing.T) {
	listener := reachableProbeListener(12)
	other := reachableProbeListener(13)
	snapshot := model.Snapshot{
		Revision:       5,
		Rules:          []model.HTTPRule{{ID: 9, FrontendURL: "http://edge.example"}},
		RelayListeners: []model.RelayListener{listener, other},
	}
	handler := newProbeHandler(t, "edge-1", snapshot)
	handler.httpProber = NewHTTPProber(HTTPProberConfig{RelayProvider: newDiagnosticTLSMaterialProvider()})
	literalCalls := 0
	handler.probeLiteral = func(context.Context, *url.URL, time.Duration) literalDialObservation {
		literalCalls++
		return literalDialObservation{statusCode: http.StatusOK, tcpConnected: true}
	}
	hopCalls := 0
	handler.probeHop = func(_ context.Context, hop relay.Hop, _ relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
		hopCalls++
		if hop.Listener.ID != listener.ID {
			t.Fatalf("probed listener %d", hop.Listener.ID)
		}
		if hop.Address != "192.0.2.12:12202" {
			t.Fatalf("hop address = %s", hop.Address)
		}
		if hop.Listener.TLSMode != listener.TLSMode || len(hop.Listener.PinSet) != 1 {
			t.Fatalf("hop trust changed: %+v", hop.Listener)
		}
		return []relay.ProbeTiming{{ToListenerID: listener.ID, LatencyMS: 1.2}}, nil
	}

	target := "http://10.0.0.8:8096/emby"
	result, err := handler.HandleTask(context.Background(), control.TaskMessage{
		TaskType: control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{
			"target_url":        target,
			"relay_listener_id": float64(listener.ID),
		},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}
	if literalCalls != 0 || hopCalls != 1 {
		t.Fatalf("literal calls = %d, hop calls = %d", literalCalls, hopCalls)
	}
	if result[control.ProbeResultStatus] != control.ProbeStatusConnected {
		t.Fatalf("status = %#v, want connected", result[control.ProbeResultStatus])
	}
	if result[control.ProbeResultRelayListenerID] != listener.ID {
		t.Fatalf("relay listener = %#v", result[control.ProbeResultRelayListenerID])
	}
	if _, ok := result[control.ProbeResultHTTPStatusCode]; ok {
		t.Fatal("hop probe recorded an HTTP status")
	}
	assertReachabilityIdentity(t, result, "edge-1", target)
	assertSnapshotUnchanged(t, handler, snapshot)
}

func TestProbeBackendReachabilityDoesNotPromoteHopWithoutDialEvidence(t *testing.T) {
	listener := reachableProbeListener(12)
	handler := newProbeHandler(t, "edge-1", model.Snapshot{RelayListeners: []model.RelayListener{listener}})
	handler.httpProber = NewHTTPProber(HTTPProberConfig{RelayProvider: newDiagnosticTLSMaterialProvider()})
	handler.probeHop = func(context.Context, relay.Hop, relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
		return []relay.ProbeTiming{{To: "10.0.0.8:8096", LatencyMS: 4}}, nil
	}
	result, err := handler.HandleTask(context.Background(), control.TaskMessage{
		TaskType: control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{
			"target_url":        "http://10.0.0.8:8096/emby",
			"relay_listener_id": "12",
		},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}
	if result[control.ProbeResultStatus] != control.ProbeStatusUntested {
		t.Fatalf("status = %#v, want untested", result[control.ProbeResultStatus])
	}
	if result[control.ProbeResultError] != relayProbeNoDialEvidence {
		t.Fatalf("missing dial detail = %#v", result[control.ProbeResultError])
	}

	const trustFailure = "bind relay tunnel security: trust root is inactive"
	handler.probeHop = func(context.Context, relay.Hop, relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
		return nil, errors.New(trustFailure)
	}
	result, err = handler.HandleTask(context.Background(), control.TaskMessage{
		TaskType: control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{
			"target_url":        "http://10.0.0.8:8096/emby",
			"relay_listener_id": 12,
		},
	})
	if err != nil {
		t.Fatalf("HandleTask(trust) error = %v", err)
	}
	if result[control.ProbeResultStatus] != control.ProbeStatusUntested {
		t.Fatalf("trust failure status = %#v, want untested", result[control.ProbeResultStatus])
	}
	if result[control.ProbeResultError] != trustFailure {
		t.Fatalf("trust failure detail = %#v", result[control.ProbeResultError])
	}

	handler.probeHop = func(context.Context, relay.Hop, relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	}
	result, err = handler.HandleTask(context.Background(), control.TaskMessage{
		TaskType: control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{
			"target_url":        "http://10.0.0.8:8096/emby",
			"relay_listener_id": 12,
		},
	})
	if err != nil {
		t.Fatalf("HandleTask(dial) error = %v", err)
	}
	if result[control.ProbeResultStatus] != control.ProbeStatusNetworkError {
		t.Fatalf("dial failure status = %#v, want network_error", result[control.ProbeResultStatus])
	}
}

func TestProbeBackendReachabilityDoesNotCreateOrRewriteListener(t *testing.T) {
	listener := reachableProbeListener(12)
	listener.Enabled = false
	invalid := reachableProbeListener(14)
	invalid.TLSMode = "pin_only"
	invalid.PinSet = nil
	snapshot := model.Snapshot{RelayListeners: []model.RelayListener{listener, invalid}}
	handler := newProbeHandler(t, "edge-1", snapshot)
	calls := 0
	handler.probeHop = func(context.Context, relay.Hop, relay.TLSMaterialProvider) ([]relay.ProbeTiming, error) {
		calls++
		return []relay.ProbeTiming{{ToListenerID: 14, LatencyMS: 1}}, nil
	}
	for _, id := range []int{12, 14, 99} {
		result, err := handler.HandleTask(context.Background(), control.TaskMessage{
			TaskType: control.TaskTypeProbeBackendReachability,
			RawPayload: map[string]any{
				"target_url":        "http://10.0.0.8:8096/emby",
				"relay_listener_id": id,
			},
		})
		if err != nil {
			t.Fatalf("HandleTask(%d) error = %v", id, err)
		}
		if result[control.ProbeResultStatus] != control.ProbeStatusUntested {
			t.Fatalf("listener %d status = %#v, want untested", id, result[control.ProbeResultStatus])
		}
	}
	if calls != 0 {
		t.Fatalf("probe calls = %d, want 0", calls)
	}
	assertSnapshotUnchanged(t, handler, snapshot)
}

func TestRelayHopProbeRequestStaysOnOneHop(t *testing.T) {
	hop := relay.Hop{Address: "192.0.2.12:12202", Listener: model.RelayListener{ID: 12}}
	network, target, chain := relayHopProbeRequest(hop)
	if network != "tcp" || target != "" || len(chain) != 1 || chain[0].Listener.ID != 12 {
		t.Fatalf("probe request = %s %q %+v", network, target, chain)
	}
}

func TestClassifyRelayHopDialKeepsTrustFailureText(t *testing.T) {
	cases := []string{
		"bind relay tunnel security: tunnel security state has no usable trust root",
		"tls material provider is required",
		"tunnel credential provider is required for pki_mtls",
	}
	for _, message := range cases {
		status, detail := classifyRelayHopDial(12, nil, errors.New(message))
		if status != control.ProbeStatusUntested || detail != message {
			t.Fatalf("classify(%q) = %s %q", message, status, detail)
		}
		if detail == relayProbeNoDialEvidence {
			t.Fatalf("trust failure collapsed to missing dial evidence: %q", message)
		}
	}

	status, detail := classifyRelayHopDial(12, []relay.ProbeTiming{{To: "10.0.0.8:8096", LatencyMS: 4}}, nil)
	if status != control.ProbeStatusUntested || detail != relayProbeNoDialEvidence {
		t.Fatalf("missing timing = %s %q", status, detail)
	}
	status, detail = classifyRelayHopDial(12, nil, errors.New("relay chain is required"))
	if status != control.ProbeStatusUntested || detail != relayProbeNoDialEvidence {
		t.Fatalf("setup failure = %s %q", status, detail)
	}
}

func TestProbeLiteralHTTPIgnoresEnvironmentProxy(t *testing.T) {
	proxyHits := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(proxy.Close)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(target.Close)

	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	handler := newProbeHandler(t, "edge-1", model.Snapshot{})
	result, err := handler.HandleTask(context.Background(), control.TaskMessage{
		TaskType:   control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{"target_url": target.URL + "/emby"},
	})
	if err != nil {
		t.Fatalf("HandleTask(loopback) error = %v", err)
	}
	if result[control.ProbeResultStatus] != control.ProbeStatusHTTPStatus || result[control.ProbeResultHTTPStatusCode] != http.StatusForbidden {
		t.Fatalf("loopback result = %#v", result)
	}

	handler.probeTimeout = 500 * time.Millisecond
	privateURL := "http://192.0.2.12:9/emby"
	result, err = handler.HandleTask(context.Background(), control.TaskMessage{
		TaskType:   control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{"target_url": privateURL},
	})
	if err != nil {
		t.Fatalf("HandleTask(private) error = %v", err)
	}
	if proxyHits != 0 {
		t.Fatalf("environment proxy handled the literal target, hits=%d result=%#v", proxyHits, result)
	}
	if result[control.ProbeResultStatus] == control.ProbeStatusHTTPStatus {
		t.Fatalf("private target recorded an HTTP status without a direct response: %#v", result)
	}
	if result[control.ProbeResultTarget] != privateURL {
		t.Fatalf("target = %#v", result[control.ProbeResultTarget])
	}
}

func TestModuleProbeUsesConfiguredExecutingNode(t *testing.T) {
	mod := NewModule()
	mod.SetNodeID(" edge-9 ")
	if err := mod.Apply(context.Background(), module.ApplyRequest{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	probed := false
	handler, ok := mod.Handler().(*DiagnosticHandler)
	if !ok {
		t.Fatal("handler type mismatch")
	}
	handler.probeLiteral = func(context.Context, *url.URL, time.Duration) literalDialObservation {
		probed = true
		return literalDialObservation{statusCode: http.StatusForbidden, tcpConnected: true}
	}
	result, err := mod.HandleTask(context.Background(), control.TaskMessage{
		TaskType:   control.TaskTypeProbeBackendReachability,
		RawPayload: map[string]any{"target_url": "https://media.example/library"},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}
	if !probed || result[control.ProbeResultAgentID] != "edge-9" || result[control.ProbeResultStatus] != control.ProbeStatusHTTPStatus {
		t.Fatalf("result = %#v, probed = %v", result, probed)
	}
}

func newProbeHandler(t *testing.T, nodeID string, snapshot model.Snapshot) *DiagnosticHandler {
	t.Helper()
	store := core.NewInMemory()
	if err := store.SaveAppliedSnapshot(snapshot); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}
	if err := store.SaveDesiredSnapshot(snapshot); err != nil {
		t.Fatalf("SaveDesiredSnapshot() error = %v", err)
	}
	handler := NewDiagnosticHandler(store, nil, nil)
	handler.SetNodeID(nodeID)
	handler.now = func() time.Time {
		return time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	}
	return handler
}

func reachableProbeListener(id int) model.RelayListener {
	return model.RelayListener{
		ID:                      id,
		AgentID:                 "service-1",
		Name:                    "service-hop",
		ListenHost:              "127.0.0.1",
		ListenPort:              12202,
		PublicHost:              "192.0.2." + strconv.Itoa(id),
		PublicPort:              12202,
		Enabled:                 true,
		TLSMode:                 "pin_and_ca",
		PinSet:                  []model.RelayPin{{Type: "spki_sha256", Value: "pin-value"}},
		TrustedCACertificateIDs: []int{9},
	}
}

func assertReachabilityIdentity(t *testing.T, result map[string]any, nodeID string, target string) {
	t.Helper()
	if result[control.ProbeResultAgentID] != nodeID {
		t.Fatalf("agent_id = %#v", result[control.ProbeResultAgentID])
	}
	if result[control.ProbeResultTarget] != target {
		t.Fatalf("target = %#v", result[control.ProbeResultTarget])
	}
	checkedAt, _ := result[control.ProbeResultCheckedAt].(string)
	parsed, err := time.Parse(time.RFC3339Nano, checkedAt)
	if err != nil || !parsed.Equal(time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("checked_at = %#v", result[control.ProbeResultCheckedAt])
	}
}

func assertSnapshotUnchanged(t *testing.T, handler *DiagnosticHandler, want model.Snapshot) {
	t.Helper()
	applied, err := handler.store.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("LoadAppliedSnapshot() error = %v", err)
	}
	desired, err := handler.store.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("LoadDesiredSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(applied, want) || !reflect.DeepEqual(desired, want) {
		t.Fatalf("snapshot changed\napplied=%#v\ndesired=%#v", applied, desired)
	}
}
