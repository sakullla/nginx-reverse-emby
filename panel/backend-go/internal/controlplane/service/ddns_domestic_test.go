//go:build !integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestDDNSDomesticMappingUsesPluginAndNotCloudflare(t *testing.T) {
	raw, _ := json.Marshal(storage.DDNSConfig{
		Enabled: true, Domain: "edge.example.cn, other.example.cn",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	})
	cf := &domesticCFClient{}
	records := &scriptedDNSRecords{byDomain: map[string]DNSCredential{
		"edge.example.cn":  {Provider: pluginhost.DNSProviderAliyun, Mapped: true},
		"other.example.cn": {Provider: pluginhost.DNSProviderDNSPodCN, Mapped: true},
	}}
	records.ensureErr = errors.New("permission denied token=super-secret-dns-token " + strings.Repeat("x", 600))
	store := &domesticDDNSStore{rows: map[string]storage.AgentRow{
		"a1": {ID: "a1", DdnsConfigJSON: string(raw), LastSeenIPv4: "203.0.113.10"},
	}}
	now := time.Unix(1_700_000_000, 0)
	svc := NewDDNSService(config.Config{DDNS: config.DDNSRuntimeConfig{TTL: 120}}, store, cf, func() time.Time { return now })
	svc.SetRecordProvider(records)
	svc.reconcileAgent(context.Background(), "a1")
	if cf.calls != 0 {
		t.Fatalf("Cloudflare calls = %d", cf.calls)
	}
	if len(records.ensured) != 2 {
		t.Fatalf("plugin ensure calls = %+v, want both domains", records.ensured)
	}
	status := store.status("a1")
	if status.Status != "error" || strings.Contains(status.LastError, "super-secret-dns-token") || len(status.LastError) > 500 || !strings.Contains(status.LastError, "permission denied") {
		t.Fatalf("status = %+v", status)
	}
	records.ensureErr = nil
	records.ensured = nil
	now = now.Add(time.Hour)
	svc.reconcileAgent(context.Background(), "a1")
	if store.status("a1").Status != "ok" || store.status("a1").LastResolvedIPv4 != "203.0.113.10" {
		t.Fatalf("recovered status = %+v", store.status("a1"))
	}
}

func TestDDNSDomesticGuardrailsDoNotCallPlugin(t *testing.T) {
	raw, _ := json.Marshal(storage.DDNSConfig{
		Enabled: false, Domain: "edge.example.cn",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	})
	conflictRaw, _ := json.Marshal(storage.DDNSConfig{
		Enabled: true, Domain: "edge.example.cn",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	})
	records := &scriptedDNSRecords{byDomain: map[string]DNSCredential{
		"edge.example.cn": {Provider: pluginhost.DNSProviderTencentDNS, Mapped: true},
	}}
	store := &domesticDDNSStore{rows: map[string]storage.AgentRow{
		"a1": {ID: "a1", DdnsConfigJSON: string(raw), LastSeenIPv4: "203.0.113.10", DdnsStatusJSON: `{"last_resolved_ipv4":"203.0.113.9","last_success_at_unix":10}`},
	}}
	svc := NewDDNSService(config.Config{}, store, &domesticCFClient{}, time.Now)
	svc.SetRecordProvider(records)
	svc.reconcileAgent(context.Background(), "a1")
	if len(records.ensured) != 0 || store.status("a1").Status != "disabled" || store.status("a1").LastResolvedIPv4 != "203.0.113.9" {
		t.Fatalf("disabled status=%+v ensured=%d", store.status("a1"), len(records.ensured))
	}

	store.rows["a1"] = storage.AgentRow{ID: "a1", DdnsConfigJSON: string(conflictRaw), LastSeenIPv4: ""}
	svc.reconcileAgent(context.Background(), "a1")
	if len(records.ensured) != 0 || store.status("a1").Status != "idle" {
		t.Fatalf("empty address status=%+v", store.status("a1"))
	}

	store.rows["a1"] = storage.AgentRow{ID: "a1", DdnsConfigJSON: string(conflictRaw), LastSeenIPv4: "203.0.113.10"}
	store.rows["a2"] = storage.AgentRow{ID: "a2", DdnsConfigJSON: string(conflictRaw), LastSeenIPv4: "203.0.113.11"}
	svc.reconcileAgent(context.Background(), "a1")
	if len(records.ensured) != 0 || !strings.Contains(store.status("a1").LastError, "ddns ownership conflict") {
		t.Fatalf("conflict status=%+v ensured=%d", store.status("a1"), len(records.ensured))
	}
	svc.ReconcileAfterHeartbeat(context.Background(), "a1")
	if len(records.ensured) != 0 {
		t.Fatal("heartbeat reconcile called the plugin")
	}
}

func TestDDNSCloudflareMappingStillUsesCloudflareClient(t *testing.T) {
	raw, _ := json.Marshal(storage.DDNSConfig{
		Enabled: true, Domain: "www.example.com",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	})
	cf := &domesticCFClient{}
	records := &scriptedDNSRecords{byDomain: map[string]DNSCredential{
		"www.example.com": {Provider: pluginhost.DNSProviderCloudflare, Token: "mapped-token", Mapped: true},
	}}
	store := &domesticDDNSStore{rows: map[string]storage.AgentRow{
		"a1": {ID: "a1", DdnsConfigJSON: string(raw), LastSeenIPv4: "203.0.113.10"},
	}}
	svc := NewDDNSService(config.Config{DDNS: config.DDNSRuntimeConfig{Token: "env-token", TTL: 120}}, store, cf, time.Now)
	svc.SetRecordProvider(records)
	svc.reconcileAgent(context.Background(), "a1")
	if len(records.ensured) != 0 || len(cf.recorded) != 1 || cf.recorded[0].token != "mapped-token" {
		t.Fatalf("cf=%+v ensured=%d", cf.recorded, len(records.ensured))
	}
}

type domesticDDNSStore struct {
	rows map[string]storage.AgentRow
}

func (f *domesticDDNSStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	out := make([]storage.AgentRow, 0, len(f.rows))
	for _, row := range f.rows {
		out = append(out, row)
	}
	return out, nil
}

func (f *domesticDDNSStore) UpdateDdnsStatusColumn(_ context.Context, agentID, statusJSON string) error {
	row := f.rows[agentID]
	row.DdnsStatusJSON = statusJSON
	f.rows[agentID] = row
	return nil
}

func (f *domesticDDNSStore) status(id string) storage.DdnsStatus {
	var status storage.DdnsStatus
	_ = json.Unmarshal([]byte(f.rows[id].DdnsStatusJSON), &status)
	return status
}

type domesticCFCall struct {
	token string
	fqdn  string
}

type domesticCFClient struct {
	calls    int
	recorded []domesticCFCall
}

func (c *domesticCFClient) EnsureRecord(_ context.Context, token, fqdn, _, _ string, _ int) (cloudflareRecordOutcome, error) {
	c.calls++
	c.recorded = append(c.recorded, domesticCFCall{token: token, fqdn: fqdn})
	return cloudflareRecordOutcome{}, nil
}
