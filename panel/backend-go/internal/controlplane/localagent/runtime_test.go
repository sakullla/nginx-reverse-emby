package localagent

import (
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

func TestEmbeddedConfigProjectsLocalCapabilityAuditExactly(t *testing.T) {
	cfg := config.Default()
	cfg.LocalAgentPluginCapabilityAudit.Enabled = true
	cfg.LocalAgentPluginCapabilityAudit.QueueSize = 17
	cfg.LocalAgentPluginCapabilityAudit.BatchSize = 3
	cfg.LocalAgentPluginCapabilityAudit.FlushInterval = 75 * time.Millisecond
	cfg.LocalAgentPluginCapabilityAudit.Retention = 9 * time.Hour
	cfg.LocalAgentPluginCapabilityAudit.MaxBytes = 3 << 20
	cfg.LocalAgentPluginCapabilityAudit.MinFreeBytes = 5 << 20
	cfg.LocalAgentPluginCapabilityAudit.CloseTimeout = 900 * time.Millisecond
	if got := embeddedConfig(cfg).CapabilityAudit; got != cfg.LocalAgentPluginCapabilityAudit {
		t.Fatalf("embedded capability audit = %+v want=%+v", got, cfg.LocalAgentPluginCapabilityAudit)
	}
}
