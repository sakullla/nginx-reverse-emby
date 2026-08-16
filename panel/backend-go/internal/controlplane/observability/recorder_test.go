//go:build !integration

package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRecorderCorrelatesLogsWithoutSecretsAndBoundsMetricLabels(t *testing.T) {
	var logs bytes.Buffer
	recorder := NewRecorder(slog.New(slog.NewJSONHandler(&logs, nil)))
	recorder.Observe(context.Background(), Event{
		Name: RevisionApply, Outcome: "applied", OperationID: "operation-7", AgentID: "agent-1",
		Revision: 7, GenerationID: "generation-7", Attempt: 2, Duration: 1250 * time.Millisecond,
	})

	logText := logs.String()
	for _, want := range []string{"operation-7", "agent-1", "generation-7", `"revision":7`, `"attempt":2`} {
		if !strings.Contains(logText, want) {
			t.Fatalf("structured log missing %q: %s", want, logText)
		}
	}

	var metrics bytes.Buffer
	if err := recorder.WritePrometheus(&metrics); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	metricText := metrics.String()
	if !strings.Contains(metricText, `nre_revision_apply_total{outcome="applied"} 1`) {
		t.Fatalf("metrics = %s", metricText)
	}
	for _, forbidden := range []string{"revision=", "operation_id=", "agent_id=", "generation_id=", "attempt="} {
		if strings.Contains(metricText, forbidden) {
			t.Fatalf("metrics contain unbounded label %q: %s", forbidden, metricText)
		}
	}

	attrs := SafeAttrs(map[string]any{
		"operation_id": "operation-7", "register_token": "secret-token", "lease_id": "lease-secret", "private_material": "pem",
	})
	joined := strings.TrimSpace(strings.Join([]string{attrs[0].(string), attrs[1].(string)}, "="))
	if joined != "operation_id=operation-7" {
		t.Fatalf("SafeAttrs() = %#v", attrs)
	}
}

func TestObserveContainsObserverFailure(t *testing.T) {
	ctx := WithObserver(context.Background(), ObserverFunc(func(context.Context, Event) {
		panic("telemetry unavailable")
	}))
	Observe(ctx, Event{Name: RevisionApply, Outcome: "failed"})
}
