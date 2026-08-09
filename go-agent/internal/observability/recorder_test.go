package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRecorderKeepsCorrelationOutOfMetricLabelsAndRedactsSensitiveFields(t *testing.T) {
	var logs bytes.Buffer
	recorder := NewRecorder(slog.New(slog.NewJSONHandler(&logs, nil)))
	recorder.Observe(context.Background(), Event{
		Name: GenerationCutover, Outcome: "succeeded", OperationID: "operation-9", AgentID: "agent-1",
		Revision: 9, GenerationID: "generation-9", Attempt: 3, Duration: 25 * time.Millisecond,
	})
	if logText := logs.String(); !strings.Contains(logText, "operation-9") || !strings.Contains(logText, "generation-9") {
		t.Fatalf("structured log = %s", logText)
	}
	var metrics bytes.Buffer
	if err := recorder.WritePrometheus(&metrics); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	metricText := metrics.String()
	if !strings.Contains(metricText, `nre_agent_generation_cutover_total{outcome="succeeded"} 1`) {
		t.Fatalf("metrics = %s", metricText)
	}
	for _, forbidden := range []string{"revision=", "operation_id=", "agent_id=", "generation_id=", "attempt="} {
		if strings.Contains(metricText, forbidden) {
			t.Fatalf("metrics contain unbounded label %q: %s", forbidden, metricText)
		}
	}
	attrs := SafeAttrs(map[string]any{"generation_id": "generation-9", "api_token": "token", "snapshot_material": "payload"})
	if len(attrs) != 2 || attrs[0] != "generation_id" || attrs[1] != "generation-9" {
		t.Fatalf("SafeAttrs() = %#v", attrs)
	}
}

func TestObserveContainsObserverFailure(t *testing.T) {
	ctx := WithObserver(context.Background(), ObserverFunc(func(context.Context, Event) {
		panic("telemetry unavailable")
	}))
	Observe(ctx, Event{Name: GenerationCutover, Outcome: "failed"})
}

func TestPolicyObservabilityKeepsIdentityOutOfMetricLabels(t *testing.T) {
	var logs bytes.Buffer
	recorder := NewRecorder(slog.New(slog.NewJSONHandler(&logs, nil)))
	recorder.Observe(context.Background(), Event{
		Name: PolicyBudget, Outcome: "exhausted", GenerationID: "generation-policy",
		PluginID: "official.waf", InstanceID: "waf-1", PolicyID: "site-policy",
		PolicyStage: "waf", Reason: "deadline", Duration: 2 * time.Millisecond,
	})
	if logText := logs.String(); !strings.Contains(logText, "official.waf") || !strings.Contains(logText, "deadline") {
		t.Fatalf("policy log = %s", logText)
	}
	var metrics bytes.Buffer
	if err := recorder.WritePrometheus(&metrics); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	metricText := metrics.String()
	if !strings.Contains(metricText, `nre_agent_policy_budget_total{outcome="exhausted"} 1`) {
		t.Fatalf("policy metrics = %s", metricText)
	}
	for _, forbidden := range []string{"plugin_id=", "instance_id=", "policy_id=", "generation_id=", "request_id=", "source=", "reason="} {
		if strings.Contains(metricText, forbidden) {
			t.Fatalf("policy metrics contain unbounded label %q: %s", forbidden, metricText)
		}
	}
}

func TestPolicyGuestMetricsApplySignedDeltasFromBoundedCatalog(t *testing.T) {
	recorder := NewRecorder(nil)
	for _, delta := range []int64{5, -2, 4} {
		recorder.Observe(context.Background(), Event{
			Name: PolicyHostMetric, Outcome: "observed", GuestMetric: "rate.allowed", MetricDelta: delta,
		})
	}
	var metrics bytes.Buffer
	if err := recorder.WritePrometheus(&metrics); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	if metricText := metrics.String(); !strings.Contains(metricText, "nre_agent_policy_guest_rate_allowed 7\n") {
		t.Fatalf("guest metrics = %s", metricText)
	}

	recorder = NewRecorder(nil)
	recorder.Observe(context.Background(), Event{Name: PolicyHostMetric, Outcome: "observed", GuestMetric: "policy.allowed", MetricDelta: int64(^uint64(0) >> 1)})
	recorder.Observe(context.Background(), Event{Name: PolicyHostMetric, Outcome: "observed", GuestMetric: "policy.allowed", MetricDelta: 1})
	metrics.Reset()
	if err := recorder.WritePrometheus(&metrics); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	if metricText := metrics.String(); !strings.Contains(metricText, "nre_agent_policy_guest_allowed 9223372036854775807\n") {
		t.Fatalf("overflow changed guest metric: %s", metricText)
	}
}
