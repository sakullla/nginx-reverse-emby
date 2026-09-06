package localagent

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
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

func TestEmbeddedPolicyRefProjectsCompositionMetadata(t *testing.T) {
	mode := sdk.PolicyModeObserve
	want := &storage.PolicyRef{
		ID:             "effective-policy",
		Overlay:        json.RawMessage(`{"schema":"nre.policy-overlay/v1","stages":[]}`),
		OverlayFormat:  sdk.PolicyOverlayFormatEnvelopeV1,
		LegacyPolicyID: "legacy-owner",
		StageModes: []storage.PolicyModeBinding{{
			Stage:    sdk.PolicyStageIdentity{Kind: sdk.PolicyOverlayStageIP, PolicyID: "ip-policy"},
			Snapshot: sdk.PolicySettingsSnapshot{Version: sdk.PolicySettingsVersion{Revision: 2, InstanceVersion: 3}, Settings: sdk.PolicyModeSettings{Handling: sdk.PolicyModeHandlingRaw, DefaultMode: &mode}},
		}},
	}
	got := toEmbeddedPolicyRef(want)
	encodedWant, _ := json.Marshal(want)
	encodedGot, _ := json.Marshal(got)
	var wantValue, gotValue any
	_ = json.Unmarshal(encodedWant, &wantValue)
	_ = json.Unmarshal(encodedGot, &gotValue)
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("embedded policy ref lost composition metadata: got=%s want=%s", encodedGot, encodedWant)
	}
	want.Overlay[0] = '['
	if got.Overlay[0] != '{' {
		t.Fatal("embedded policy overlay aliases storage snapshot")
	}
}
