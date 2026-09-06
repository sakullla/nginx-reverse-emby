package model

import (
	"strings"
	"testing"
	"time"
)

func capabilityAuditRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NRE_MASTER_URL", "https://panel.example")
	t.Setenv("NRE_AGENT_TOKEN", "agent-token")
}

func TestCapabilityAuditConfigDefaultsOffAndStrictEnvironment(t *testing.T) {
	capabilityAuditRequiredEnv(t)
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultCapabilityAuditConfig()
	if cfg.CapabilityAudit != want || cfg.CapabilityAudit.Enabled {
		t.Fatalf("default capability audit = %+v want=%+v", cfg.CapabilityAudit, want)
	}
	t.Setenv("NRE_PLUGIN_CAPABILITY_AUDIT_ENABLED", "true")
	t.Setenv("NRE_PLUGIN_CAPABILITY_AUDIT_QUEUE_SIZE", "64")
	t.Setenv("NRE_PLUGIN_CAPABILITY_AUDIT_BATCH_SIZE", "8")
	t.Setenv("NRE_PLUGIN_CAPABILITY_AUDIT_FLUSH_INTERVAL", "100ms")
	t.Setenv("NRE_PLUGIN_CAPABILITY_AUDIT_RETENTION", "12h")
	t.Setenv("NRE_PLUGIN_CAPABILITY_AUDIT_MAX_BYTES", "1048576")
	t.Setenv("NRE_PLUGIN_CAPABILITY_AUDIT_MIN_FREE_BYTES", "2097152")
	t.Setenv("NRE_PLUGIN_CAPABILITY_AUDIT_CLOSE_TIMEOUT", "500ms")
	cfg, err = LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CapabilityAudit.Enabled || cfg.CapabilityAudit.QueueSize != 64 || cfg.CapabilityAudit.BatchSize != 8 ||
		cfg.CapabilityAudit.FlushInterval != 100*time.Millisecond || cfg.CapabilityAudit.Retention != 12*time.Hour ||
		cfg.CapabilityAudit.MaxBytes != 1<<20 || cfg.CapabilityAudit.MinFreeBytes != 2<<20 || cfg.CapabilityAudit.CloseTimeout != 500*time.Millisecond {
		t.Fatalf("configured capability audit = %+v", cfg.CapabilityAudit)
	}
}

func TestCapabilityAuditConfigRejectsInvalidEnvironment(t *testing.T) {
	for _, test := range []struct {
		name, value string
	}{
		{"ENABLED", "1"},
		{"QUEUE_SIZE", "0"},
		{"BATCH_SIZE", "-1"},
		{"FLUSH_INTERVAL", "0s"},
		{"RETENTION", "721h"},
		{"MAX_BYTES", "0"},
		{"MIN_FREE_BYTES", "-1"},
		{"CLOSE_TIMEOUT", "31s"},
	} {
		t.Run(test.name, func(t *testing.T) {
			capabilityAuditRequiredEnv(t)
			t.Setenv("NRE_PLUGIN_CAPABILITY_AUDIT_"+test.name, test.value)
			if _, err := LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "CAPABILITY_AUDIT") {
				t.Fatalf("invalid %s=%q error=%v", test.name, test.value, err)
			}
		})
	}
}
