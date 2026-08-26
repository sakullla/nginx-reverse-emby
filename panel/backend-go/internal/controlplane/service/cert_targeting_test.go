//go:build exhaustive && !integration

package service

import (
	"runtime"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestMasterCFDNSAllowsRemoteDistributionTargets(t *testing.T) {
	t.Parallel()
	cert := ManagedCertificate{
		Domain: "*.example.com", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		CertificateType: "acme", TargetAgentIDs: []string{"local", "edge-capable"},
	}
	if err := assertManagedCertificateTargetingAllowed(config.Config{LocalAgentID: "local"}, cert); err != nil {
		t.Fatalf("master-issued certificate rejected remote distribution target: %v", err)
	}
}

func TestMasterCFDNSDistributionRequiresCertificateInstallCapability(t *testing.T) {
	store := newServiceOwnerStore(t)
	for _, agent := range []storage.AgentRow{
		{ID: "edge-capable", Name: "edge-capable", Version: "1", Platform: runtime.GOOS + "-" + runtime.GOARCH, CapabilitiesJSON: `["cert_install"]`},
		{ID: "edge-incapable", Name: "edge-incapable", Version: "1", Platform: runtime.GOOS + "-" + runtime.GOARCH, CapabilitiesJSON: `[]`},
	} {
		if err := store.SaveAgent(t.Context(), agent); err != nil {
			t.Fatal(err)
		}
	}
	service := NewCertificateService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	cert := ManagedCertificate{Domain: "*.example.com", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns", CertificateType: "acme"}

	cert.TargetAgentIDs = []string{"edge-capable"}
	if err := service.assertCertificateDistributionTargetsAllowed(t.Context(), cert); err != nil {
		t.Fatalf("capable remote distribution target rejected: %v", err)
	}
	cert.TargetAgentIDs = []string{"edge-incapable"}
	if err := service.assertCertificateDistributionTargetsAllowed(t.Context(), cert); err == nil || !strings.Contains(err.Error(), "does not support certificate install") {
		t.Fatalf("incapable remote distribution target error = %v", err)
	}
}

func TestMasterCFDNSTargetChangeRedistributesWithoutReissue(t *testing.T) {
	t.Parallel()
	previous := ManagedCertificate{
		Domain: "*.example.com", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		CertificateType: "acme", TargetAgentIDs: []string{"local"},
	}
	current := previous
	current.TargetAgentIDs = []string{"local", "edge-capable"}
	if managedCertificateMutationNeedsManagedDNSIssue(&previous, current) {
		t.Fatal("target-only change requested a new DNS-01 certificate")
	}
	current.Domain = "*.changed.example.com"
	if !managedCertificateMutationNeedsManagedDNSIssue(&previous, current) {
		t.Fatal("domain change did not request a new DNS-01 certificate")
	}
}
