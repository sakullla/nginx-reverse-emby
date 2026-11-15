//go:build !fast

package storage

import (
	"strings"
	"testing"
)

func TestListPublicCertificateProjectionsOmitsInternalPKIAndPrivateMaterial(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	const privateKey = "SUPER-PRIVATE-KEY-MATERIAL"
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{
		{ID: 2, Domain: "one.example", Enabled: true, Usage: "https", CertificateType: "uploaded", TargetAgentIDs: `["edge-a"]`},
		{ID: 1, Domain: "two.example", Enabled: false, Usage: "https", CertificateType: "acme", TargetAgentIDs: `["edge-a"]`},
		{ID: 3, Domain: "other.example", Enabled: true, Usage: "https", CertificateType: "acme", TargetAgentIDs: `["other-agent"]`},
		{ID: 4, Domain: "internal.example", Enabled: true, Usage: "https", CertificateType: "internal_ca", TargetAgentIDs: `["edge-a"]`},
		{ID: 8, Domain: "rule.example", Enabled: true, Usage: "https", CertificateType: "acme", TargetAgentIDs: `[]`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHTTPRules(ctx, "edge-a", []HTTPRuleRow{{ID: 11, Enabled: true, FrontendURL: "https://rule.example/"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveManagedCertificateMaterial(ctx, "one.example", ManagedCertificateBundle{Domain: "one.example", CertPEM: "CERTIFICATE-MATERIAL", KeyPEM: privateKey}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListPublicCertificateProjections(ctx, "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 8 {
		t.Fatalf("projections = %+v", got)
	}
	if got[0].Enabled || got[0].MaterialAvailable || got[1].MaterialAvailable != true || !got[1].Enabled || got[2].MaterialAvailable {
		t.Fatalf("projection flags = %+v", got)
	}
	encoded := mustJSON(t, got)
	if strings.Contains(encoded, privateKey) || strings.Contains(encoded, "CERTIFICATE-MATERIAL") || strings.Contains(encoded, "BEGIN ") {
		t.Fatalf("projection leaked material: %s", encoded)
	}
}
