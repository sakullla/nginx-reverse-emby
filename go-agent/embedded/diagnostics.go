package embedded

import (
	"context"
	"os"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/certs"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/diagnostics"
	agentstore "github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/task"
)

type DiagnosticRequest struct {
	TaskType string
	RuleID   int
}

func DiagnoseSnapshot(ctx context.Context, dataDir string, snapshot Snapshot, req DiagnosticRequest) (map[string]any, error) {
	dataDir = strings.TrimSpace(dataDir)
	cleanup := func() {}
	if dataDir == "" {
		tmpDir, err := os.MkdirTemp("", "nre-embedded-diagnostics-*")
		if err != nil {
			return nil, err
		}
		dataDir = tmpDir
		cleanup = func() { _ = os.RemoveAll(tmpDir) }
	}
	defer cleanup()

	mem := agentstore.NewInMemory()
	if err := mem.SaveAppliedSnapshot(snapshot); err != nil {
		return nil, err
	}

	certManager, err := certs.NewManager(dataDir, certs.WithLocalAgent(true), certs.WithNodeRole("master"))
	if err != nil {
		return nil, err
	}
	defer certManager.Close()
	if err := certManager.Apply(ctx, snapshot.Certificates, snapshot.CertificatePolicies); err != nil {
		return nil, err
	}

	handler := task.NewDiagnosticHandler(
		mem,
		diagnostics.NewHTTPProber(diagnostics.HTTPProberConfig{Attempts: 5, RelayProvider: certManager}),
		diagnostics.NewTCPProber(diagnostics.TCPProberConfig{Attempts: 5, RelayProvider: certManager}),
	)
	return handler.HandleTask(ctx, task.TaskMessage{
		TaskType:   req.TaskType,
		RawPayload: map[string]any{"rule_id": req.RuleID},
	})
}
