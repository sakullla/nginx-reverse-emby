//go:build integration

package embedded

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestProductionWrapperMetricsReachEmbeddedExposition(t *testing.T) {
	_, _ = core.NewGenerationManager(nil).Apply(context.Background(), model.Snapshot{}, model.Snapshot{Revision: 91})
	var output bytes.Buffer
	if err := WriteObservabilityMetrics(&output); err != nil {
		t.Fatalf("WriteObservabilityMetrics() error = %v", err)
	}
	if !strings.Contains(output.String(), `nre_agent_generation_cutover_total{outcome="failed"}`) {
		t.Fatalf("metrics = %s", output.String())
	}
}
