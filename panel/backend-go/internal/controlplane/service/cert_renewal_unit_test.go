package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

func TestIssuanceLockContextHonorsCancellation(t *testing.T) {
	unlock := issuanceLock(910001)
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := issuanceLockContext(ctx, 910001)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("issuance lock error = %v, want context.Canceled", err)
	}
}

func TestManagedCertificateRenewalTargetIncludesLocalDistribution(t *testing.T) {
	service := &certificateService{cfg: config.Config{LocalAgentID: "local"}}
	base := ManagedCertificate{
		Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns", CertificateType: "acme",
	}
	for _, test := range []struct {
		name    string
		targets []string
		want    bool
	}{
		{name: "local only", targets: []string{"local"}, want: true},
		{name: "local and remote", targets: []string{"local", "edge"}, want: true},
		{name: "remote only", targets: []string{"edge"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cert := base
			cert.TargetAgentIDs = test.targets
			if got := service.isManagedCertificateRenewalTarget(cert); got != test.want {
				t.Fatalf("renewal target = %t, want %t", got, test.want)
			}
		})
	}
}
