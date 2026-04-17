package task

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/diagnostics"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
)

func TestDiagnosticHandlerExecutesHTTPRuleProbeFromAppliedSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(model.Snapshot{
		Rules: []model.HTTPRule{{
			ID:          7,
			FrontendURL: "https://edge.example.test/emby",
			BackendURL:  server.URL + "/healthz",
		}},
	}); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}

	handler := NewDiagnosticHandler(mem, diagnostics.NewHTTPProber(diagnostics.HTTPProberConfig{
		Attempts:   2,
		Timeout:    time.Second,
		HTTPClient: server.Client(),
	}), diagnostics.NewTCPProber(diagnostics.TCPProberConfig{}))

	result, err := handler.HandleTask(context.Background(), TaskMessage{
		TaskID:     "task-1",
		TaskType:   TaskTypeDiagnoseHTTPRule,
		RawPayload: map[string]any{"rule_id": 7},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %#v", result["summary"])
	}
	if summary["succeeded"] != 2 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestDiagnosticHandlerReturnsPerBackendResults(t *testing.T) {
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backendB.Close()

	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(model.Snapshot{
		Rules: []model.HTTPRule{{
			ID:          17,
			FrontendURL: "https://edge.example.test/emby",
			Backends: []model.HTTPBackend{
				{URL: backendA.URL + "/healthz"},
				{URL: backendB.URL + "/healthz"},
			},
			LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
		}},
	}); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}

	handler := NewDiagnosticHandler(mem, diagnostics.NewHTTPProber(diagnostics.HTTPProberConfig{
		Attempts:   5,
		Timeout:    time.Second,
		HTTPClient: backendA.Client(),
	}), diagnostics.NewTCPProber(diagnostics.TCPProberConfig{}))

	result, err := handler.HandleTask(context.Background(), TaskMessage{
		TaskID:     "task-17",
		TaskType:   TaskTypeDiagnoseHTTPRule,
		RawPayload: map[string]any{"rule_id": 17},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	backends, ok := result["backends"].([]map[string]any)
	if !ok {
		t.Fatalf("backends = %#v", result["backends"])
	}
	if len(backends) != 2 {
		t.Fatalf("backends = %#v", backends)
	}
}

func TestDiagnosticHandlerSerializesAdaptiveBackendFactors(t *testing.T) {
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backendB.Close()

	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(model.Snapshot{
		Rules: []model.HTTPRule{{
			ID:          18,
			FrontendURL: "https://edge.example.test/emby",
			Backends: []model.HTTPBackend{
				{URL: backendA.URL + "/healthz"},
				{URL: backendB.URL + "/healthz"},
			},
			LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		}},
	}); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}

	handler := NewDiagnosticHandler(mem, diagnostics.NewHTTPProber(diagnostics.HTTPProberConfig{
		Attempts:   1,
		Timeout:    time.Second,
		HTTPClient: backendA.Client(),
	}), diagnostics.NewTCPProber(diagnostics.TCPProberConfig{}))

	result, err := handler.HandleTask(context.Background(), TaskMessage{
		TaskID:     "task-18",
		TaskType:   TaskTypeDiagnoseHTTPRule,
		RawPayload: map[string]any{"rule_id": 18},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	backends, ok := result["backends"].([]map[string]any)
	if !ok || len(backends) == 0 {
		t.Fatalf("backends = %#v", result["backends"])
	}
	if _, ok := backends[0]["adaptive"].(map[string]any); !ok {
		t.Fatalf("adaptive = %#v", backends[0]["adaptive"])
	}
}

func TestDiagnosticHandlerUsesFiveHTTPSamplesByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(model.Snapshot{
		Rules: []model.HTTPRule{{
			ID:          27,
			FrontendURL: "https://edge.example.test/emby",
			BackendURL:  server.URL + "/healthz",
		}},
	}); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}

	handler := NewDiagnosticHandler(
		mem,
		diagnostics.NewHTTPProber(diagnostics.HTTPProberConfig{
			Timeout:    time.Second,
			HTTPClient: server.Client(),
		}),
		diagnostics.NewTCPProber(diagnostics.TCPProberConfig{}),
	)

	result, err := handler.HandleTask(context.Background(), TaskMessage{
		TaskID:     "task-27",
		TaskType:   TaskTypeDiagnoseHTTPRule,
		RawPayload: map[string]any{"rule_id": 27},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %#v", result["summary"])
	}
	if summary["sent"] != 5 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestDiagnosticHandlerExecutesTCPL4ProbeFromDesiredSnapshot(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	mem := store.NewInMemory()
	addr := ln.Addr().(*net.TCPAddr)
	if err := mem.SaveDesiredSnapshot(model.Snapshot{
		L4Rules: []model.L4Rule{{
			ID:           9,
			Protocol:     "tcp",
			ListenHost:   "0.0.0.0",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: addr.Port,
		}},
	}); err != nil {
		t.Fatalf("SaveDesiredSnapshot() error = %v", err)
	}

	handler := NewDiagnosticHandler(mem, diagnostics.NewHTTPProber(diagnostics.HTTPProberConfig{}), diagnostics.NewTCPProber(diagnostics.TCPProberConfig{
		Attempts: 1,
		Timeout:  time.Second,
	}))

	result, err := handler.HandleTask(context.Background(), TaskMessage{
		TaskID:     "task-2",
		TaskType:   TaskTypeDiagnoseL4TCPRule,
		RawPayload: map[string]any{"rule_id": 9},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %#v", result["summary"])
	}
	if summary["succeeded"] != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	<-done
}

func TestDiagnosticHandlerReturnsPerBackendResultsForL4Rules(t *testing.T) {
	lnA, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer lnA.Close()
	lnB, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer lnB.Close()

	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		for i := 0; i < 5; i++ {
			conn, err := lnA.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	doneB := make(chan struct{})
	go func() {
		defer close(doneB)
		for i := 0; i < 5; i++ {
			conn, err := lnB.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	addrA := lnA.Addr().(*net.TCPAddr)
	addrB := lnB.Addr().(*net.TCPAddr)

	mem := store.NewInMemory()
	if err := mem.SaveDesiredSnapshot(model.Snapshot{
		L4Rules: []model.L4Rule{{
			ID:         19,
			Protocol:   "tcp",
			ListenHost: "0.0.0.0",
			ListenPort: 9400,
			Backends: []model.L4Backend{
				{Host: "127.0.0.1", Port: addrA.Port},
				{Host: "127.0.0.1", Port: addrB.Port},
			},
			LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
		}},
	}); err != nil {
		t.Fatalf("SaveDesiredSnapshot() error = %v", err)
	}

	handler := NewDiagnosticHandler(mem, diagnostics.NewHTTPProber(diagnostics.HTTPProberConfig{}), diagnostics.NewTCPProber(diagnostics.TCPProberConfig{
		Attempts: 5,
		Timeout:  time.Second,
	}))

	result, err := handler.HandleTask(context.Background(), TaskMessage{
		TaskID:     "task-19",
		TaskType:   TaskTypeDiagnoseL4TCPRule,
		RawPayload: map[string]any{"rule_id": 19},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	backends, ok := result["backends"].([]map[string]any)
	if !ok {
		t.Fatalf("backends = %#v", result["backends"])
	}
	if len(backends) != 2 {
		t.Fatalf("backends = %#v", backends)
	}

	<-doneA
	<-doneB
}

func TestReportToMapIncludesAdaptiveRecoveryFields(t *testing.T) {
	report := diagnostics.Report{
		Kind:   "http",
		RuleID: 29,
		Summary: diagnostics.Summary{
			Sent:      1,
			Succeeded: 1,
			Quality:   "极佳",
		},
		Backends: []diagnostics.BackendReport{{
			Backend: "http://backend.example.test/healthz",
			Summary: diagnostics.Summary{
				Sent:      1,
				Succeeded: 1,
				Quality:   "极佳",
			},
			Adaptive: &diagnostics.AdaptiveSummary{
				Preferred:        true,
				Reason:           "performance_higher",
				State:            "recovering",
				SampleConfidence: 0.55,
				SlowStartActive:  true,
				Outlier:          true,
				TrafficShareHint: "recovery",
			},
			Children: []diagnostics.BackendReport{{
				Backend: "http://backend.example.test/healthz [10.0.0.10:443]",
				Summary: diagnostics.Summary{
					Sent:      1,
					Succeeded: 1,
					Quality:   "极佳",
				},
				Adaptive: &diagnostics.AdaptiveSummary{
					State:            "warm",
					SampleConfidence: 1,
					SlowStartActive:  false,
					Outlier:          false,
					TrafficShareHint: "normal",
				},
			}},
		}},
	}

	payload := reportToMap(report)
	backends, ok := payload["backends"].([]map[string]any)
	if !ok || len(backends) != 1 {
		t.Fatalf("backends = %#v", payload["backends"])
	}

	adaptive, ok := backends[0]["adaptive"].(map[string]any)
	if !ok {
		t.Fatalf("adaptive = %#v", backends[0]["adaptive"])
	}
	if adaptive["state"] != "recovering" {
		t.Fatalf("state = %#v", adaptive["state"])
	}
	if adaptive["sample_confidence"] != 0.55 {
		t.Fatalf("sample_confidence = %#v", adaptive["sample_confidence"])
	}
	if adaptive["slow_start_active"] != true {
		t.Fatalf("slow_start_active = %#v", adaptive["slow_start_active"])
	}
	if adaptive["outlier"] != true {
		t.Fatalf("outlier = %#v", adaptive["outlier"])
	}
	if adaptive["traffic_share_hint"] != "recovery" {
		t.Fatalf("traffic_share_hint = %#v", adaptive["traffic_share_hint"])
	}

	children, ok := backends[0]["children"].([]map[string]any)
	if !ok || len(children) != 1 {
		t.Fatalf("children = %#v", backends[0]["children"])
	}
	childAdaptive, ok := children[0]["adaptive"].(map[string]any)
	if !ok {
		t.Fatalf("child adaptive = %#v", children[0]["adaptive"])
	}
	if childAdaptive["state"] != "warm" {
		t.Fatalf("child state = %#v", childAdaptive["state"])
	}
	if childAdaptive["sample_confidence"] != 1.0 {
		t.Fatalf("child sample_confidence = %#v", childAdaptive["sample_confidence"])
	}
	if childAdaptive["slow_start_active"] != false {
		t.Fatalf("child slow_start_active = %#v", childAdaptive["slow_start_active"])
	}
	if childAdaptive["outlier"] != false {
		t.Fatalf("child outlier = %#v", childAdaptive["outlier"])
	}
	if childAdaptive["traffic_share_hint"] != "normal" {
		t.Fatalf("child traffic_share_hint = %#v", childAdaptive["traffic_share_hint"])
	}
}

func TestReportToMapOmitsEstimatedBandwidthWhenAdaptiveSummaryHasNoThroughput(t *testing.T) {
	report := diagnostics.Report{
		Kind: "l4_tcp",
		Backends: []diagnostics.BackendReport{{
			Backend: "127.0.0.1:9001",
			Adaptive: &diagnostics.AdaptiveSummary{
				LatencyMS:        12,
				PerformanceScore: 0.8,
			},
		}},
	}

	payload := reportToMap(report)
	backends, ok := payload["backends"].([]map[string]any)
	if !ok || len(backends) != 1 {
		t.Fatalf("backends = %#v", payload["backends"])
	}
	adaptive, ok := backends[0]["adaptive"].(map[string]any)
	if !ok {
		t.Fatalf("adaptive = %#v", backends[0]["adaptive"])
	}
	if _, exists := adaptive["estimated_bandwidth_bps"]; exists {
		t.Fatalf("l4 payload must omit estimated_bandwidth_bps: %#v", adaptive)
	}
}
