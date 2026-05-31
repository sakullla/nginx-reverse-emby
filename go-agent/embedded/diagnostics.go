package embedded

import (
	"context"
	"os"
	"strings"

	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
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

	certManager, err := modulecerts.NewManager(dataDir, modulecerts.WithLocalAgent(true), modulecerts.WithNodeRole("master"))
	if err != nil {
		return nil, err
	}
	defer certManager.Close()
	if err := certManager.Apply(ctx, snapshot.Certificates, snapshot.CertificatePolicies); err != nil {
		return nil, err
	}

	mod := modulediagnostics.NewModule()
	if err := mod.Apply(ctx, agentmodule.ApplyRequest{
		Next: snapshot,
		Providers: diagnosticProviderResolver{
			agentmodule.ProviderDiagnosticsRelaySource: certManager,
			agentmodule.ProviderTLSMaterial:            certManager,
		},
	}); err != nil {
		return nil, err
	}
	return mod.HandleTask(ctx, task.TaskMessage{
		TaskType:   req.TaskType,
		RawPayload: map[string]any{"rule_id": req.RuleID},
	})
}

type diagnosticProviderResolver map[agentmodule.ProviderRef]any

func (r diagnosticProviderResolver) Resolve(ref agentmodule.ProviderRef) (any, bool) {
	provider, ok := r[ref]
	return provider, ok
}
