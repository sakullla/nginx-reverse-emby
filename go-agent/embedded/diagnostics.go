package embedded

import (
	"context"
	"os"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
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

	certManager, err := modulecerts.NewManager(dataDir, modulecerts.WithLocalAgent(true), modulecerts.WithNodeRole("master"))
	if err != nil {
		return nil, err
	}
	defer certManager.Close()

	registry := agentmodule.NewRegistry()
	certModule := modulecerts.NewModule(certManager)
	mod := modulediagnostics.NewModule()
	if err := registry.Register(certModule); err != nil {
		return nil, err
	}
	if err := registry.Register(mod); err != nil {
		return nil, err
	}
	if err := registry.Apply(ctx, Snapshot{}, snapshot); err != nil {
		return nil, err
	}
	return mod.HandleTask(ctx, control.TaskMessage{
		TaskType:   req.TaskType,
		RawPayload: map[string]any{"rule_id": req.RuleID},
	})
}
