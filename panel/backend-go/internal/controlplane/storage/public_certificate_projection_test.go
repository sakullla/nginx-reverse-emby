package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSelectPublicCertificateProjectionsKeepsAgentPublicSet(t *testing.T) {
	rows := []ManagedCertificateRow{
		{ID: 2, Domain: "two.example", Enabled: true, Usage: "https", CertificateType: "uploaded", TargetAgentIDs: `["edge-a"]`},
		{ID: 1, Domain: "one.example", Enabled: false, Usage: "https", CertificateType: "acme", TargetAgentIDs: `["edge-a"]`},
		{ID: 3, Domain: "other.example", Enabled: true, Usage: "https", CertificateType: "acme", TargetAgentIDs: `["edge-b"]`},
		{ID: 4, Domain: "internal.example", Enabled: true, Usage: "https", CertificateType: "internal_ca", TargetAgentIDs: `["edge-a"]`},
		{ID: 5, Domain: "relay.example", Enabled: true, Usage: "relay_tunnel", CertificateType: "acme", TargetAgentIDs: `["edge-a"]`},
		{ID: 6, Domain: "__relay-ca.internal", Enabled: true, Usage: "https", CertificateType: "uploaded", TargetAgentIDs: `["edge-a"]`},
		{ID: 7, Domain: "rule.example", Enabled: true, Usage: "https", CertificateType: "acme", TargetAgentIDs: `[]`},
	}
	httpRows := []HTTPRuleRow{{ID: 1, AgentID: "edge-a", Enabled: true, FrontendURL: "https://rule.example/"}}
	filtered := filterManagedCertificatesForAgent(rows, "edge-a", httpRows, nil)
	got, err := selectPublicCertificateProjections(filtered, func(row ManagedCertificateRow) (bool, error) {
		return row.ID == 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 7 {
		t.Fatalf("projections = %+v", got)
	}
	if got[0].Enabled || got[0].MaterialAvailable || got[0].Domain != "one.example" {
		t.Fatalf("disabled projection = %+v", got[0])
	}
	if !got[1].Enabled || !got[1].MaterialAvailable || got[1].Domain != "two.example" {
		t.Fatalf("available projection = %+v", got[1])
	}
	if !got[2].Enabled || got[2].MaterialAvailable || got[2].Domain != "rule.example" {
		t.Fatalf("rule projection = %+v", got[2])
	}
	encoded := mustJSON(t, got)
	for _, hidden := range []string{"cert_pem", "key_pem", "PRIVATE", "BEGIN "} {
		if strings.Contains(encoded, hidden) {
			t.Fatalf("projection json leaked %s: %s", hidden, encoded)
		}
	}

	unsafe := []ManagedCertificateRow{{ID: 8, Domain: "bad\n.example", Enabled: true, Usage: "https", CertificateType: "acme", TargetAgentIDs: `["edge-a"]`}}
	filtered = filterManagedCertificatesForAgent(unsafe, "edge-a", nil, nil)
	_, err = selectPublicCertificateProjections(filtered, func(ManagedCertificateRow) (bool, error) { return false, nil })
	if err == nil || strings.Contains(err.Error(), "bad") {
		t.Fatalf("unsafe domain error = %v", err)
	}
	_, err = selectPublicCertificateProjections([]ManagedCertificateRow{{ID: 2, Domain: "two.example", Enabled: true, Usage: "https", CertificateType: "uploaded"}}, func(ManagedCertificateRow) (bool, error) {
		return false, errors.New("SUPER-PRIVATE-KEY-MATERIAL")
	})
	if err == nil || strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "SUPER") {
		t.Fatalf("material error leaked: %v", err)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
