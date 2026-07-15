package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/observability"
)

func TestObservabilityMetricsRequirePanelTokenAndExposeOnlyBoundedLabels(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.PanelToken = "panel-secret"
	observability.Default().Observe(context.Background(), observability.Event{
		Name: observability.RevisionApply, Outcome: "applied", AgentID: "agent-cardinality",
		OperationID: "operation-cardinality", Revision: 999, GenerationID: "generation-cardinality", Attempt: 42,
	})
	router, err := NewRouter(Dependencies{Config: cfg})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	if closer, ok := router.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/panel-api/metrics", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized metrics status = %d, want 401", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/panel-api/metrics", nil)
	request.Header.Set("X-Panel-Token", cfg.PanelToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `nre_revision_apply_total{outcome="applied"}`) {
		t.Fatalf("metrics body = %s", body)
	}
	for _, forbidden := range []string{"agent-cardinality", "operation-cardinality", "generation-cardinality", `revision="999"`, `attempt="42"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics leaked unbounded label/value %q: %s", forbidden, body)
		}
	}
}
