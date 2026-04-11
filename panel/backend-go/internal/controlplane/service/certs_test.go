package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestCertificateServiceListOverlaysAgentReportFields(t *testing.T) {
	store := &relayCertStore{
		agents: []storage.AgentRow{{ID: "edge-1"}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:             21,
			Domain:         "shared.example.com",
			Enabled:        true,
			Scope:          "domain",
			IssuerMode:     "local_http01",
			TargetAgentIDs: `["edge-1","edge-2"]`,
			Status:         "pending",
			LastIssueAt:    "2026-04-01T00:00:00Z",
			LastError:      "old error",
			MaterialHash:   "global-hash",
			AgentReports:   `{"edge-1":{"status":"active","last_issue_at":"2026-04-10T12:00:00Z","last_error":"","material_hash":"agent-hash","acme_info":{"Main_Domain":"shared.example.com","Profile":"default"}}}`,
			ACMEInfo:       `{"Main_Domain":"global.example.com","Profile":"global"}`,
			Usage:          "https",
			Revision:       4,
		}},
	}
	svc := NewCertificateService(config.Config{
		LocalAgentID: "local",
	}, store)

	certs, err := svc.List(context.Background(), "edge-1")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("len(certs) = %d", len(certs))
	}

	cert := certs[0]
	if cert.Status != "active" {
		t.Fatalf("cert.Status = %q", cert.Status)
	}
	if cert.LastIssueAt != "2026-04-10T12:00:00Z" {
		t.Fatalf("cert.LastIssueAt = %q", cert.LastIssueAt)
	}
	if cert.LastError != "" {
		t.Fatalf("cert.LastError = %q", cert.LastError)
	}
	if cert.MaterialHash != "agent-hash" {
		t.Fatalf("cert.MaterialHash = %q", cert.MaterialHash)
	}
	if cert.ACMEInfo.MainDomain != "shared.example.com" || cert.ACMEInfo.Profile != "default" {
		t.Fatalf("cert.ACMEInfo = %+v", cert.ACMEInfo)
	}
}

func TestCertificateServiceRejectsSystemRelayCAMutations(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              10,
			Domain:          "__relay-ca.internal",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			TagsJSON:        `["system:relay-ca","system"]`,
			Revision:        2,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain: stringPtr("new-relay-ca.internal"),
		Usage:  stringPtr("relay_ca"),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain: stringPtr("__relay-ca.internal"),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() with reserved domain error = %v", err)
	}

	if _, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain: stringPtr("tagged.example.com"),
		Tags:   &[]string{"system:relay-ca", "system"},
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() with reserved tags error = %v", err)
	}

	if _, err := svc.Update(context.Background(), "local", 10, ManagedCertificateInput{
		Enabled: boolPtr(false),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}

	store.managedCerts = append(store.managedCerts, storage.ManagedCertificateRow{
		ID:              11,
		Domain:          "ordinary.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["local"]`,
		Status:          "active",
		Usage:           "https",
		CertificateType: "uploaded",
		Revision:        3,
	})
	if _, err := svc.Update(context.Background(), "local", 11, ManagedCertificateInput{
		Domain: stringPtr("__relay-ca.internal"),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() with reserved domain error = %v", err)
	}

	if _, err := svc.Delete(context.Background(), "local", 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestCertificateServiceRejectsInvalidMasterCFDNSTargeting(t *testing.T) {
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install"]`,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "edge-1", ManagedCertificateInput{
		Domain:          stringPtr("remote.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("master_cf_dns"),
		TargetAgentIDs:  &[]string{"edge-1"},
		Usage:           stringPtr("https"),
		CertificateType: stringPtr("acme"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if err.Error() != "invalid argument: master_cf_dns certificates must target only the local master agent" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestCertificateServiceRejectsNonACMEMasterCFDNSCertificate(t *testing.T) {
	store := &relayCertStore{}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("local.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("master_cf_dns"),
		TargetAgentIDs:  &[]string{"local"},
		Usage:           stringPtr("https"),
		CertificateType: stringPtr("uploaded"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if err.Error() != "invalid argument: master_cf_dns certificates must use certificate_type=acme" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestCertificateServiceUpdateRejectsMasterCFDNSTargetExpansion(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              14,
			Domain:          "local.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "https",
			CertificateType: "acme",
			Revision:        2,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 14, ManagedCertificateInput{
		TargetAgentIDs: &[]string{"local", "edge-1"},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}
	if err.Error() != "invalid argument: master_cf_dns certificates must target only the local master agent" {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestCertificateServiceDeleteDetachesSingleAgentFromSharedCertificate(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:             30,
			Domain:         "shared.example.com",
			Enabled:        true,
			Scope:          "domain",
			IssuerMode:     "local_http01",
			TargetAgentIDs: `["local","edge-1"]`,
			Status:         "active",
			Usage:          "https",
			Revision:       5,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "local", 30)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(deleted.TargetAgentIDs) != 1 || deleted.TargetAgentIDs[0] != "local" {
		t.Fatalf("deleted.TargetAgentIDs = %+v", deleted.TargetAgentIDs)
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("len(store.managedCerts) = %d", len(store.managedCerts))
	}
	remaining := managedCertificateFromRow(store.managedCerts[0])
	if len(remaining.TargetAgentIDs) != 1 || remaining.TargetAgentIDs[0] != "edge-1" {
		t.Fatalf("remaining.TargetAgentIDs = %+v", remaining.TargetAgentIDs)
	}
}

func TestCertificateServiceRunRenewalPassRenewsEligibleCloudflareCertificate(t *testing.T) {
	now := time.Date(2026, 4, 11, 1, 2, 3, 0, time.UTC)
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              40,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			LastError:       "previous failure",
			MaterialHash:    "old-hash",
			ACMEInfo:        `{"Main_Domain":"media.example.com","Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			40: {
				Changed:      true,
				LastIssueAt:  "2026-04-11T01:02:03Z",
				MaterialHash: "new-hash",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "media.example.com",
					CA:         "LetsEncrypt",
					Renew:      "2026-07-10T00:00:00Z",
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return now }

	if err := svc.RunRenewalPass(context.Background()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	if len(issuer.calls) != 1 || issuer.calls[0] != 40 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}

	renewed := managedCertificateFromRow(store.managedCerts[0])
	if renewed.Status != "active" {
		t.Fatalf("renewed.Status = %q", renewed.Status)
	}
	if renewed.LastError != "" {
		t.Fatalf("renewed.LastError = %q", renewed.LastError)
	}
	if renewed.LastIssueAt != "2026-04-11T01:02:03Z" {
		t.Fatalf("renewed.LastIssueAt = %q", renewed.LastIssueAt)
	}
	if renewed.MaterialHash != "new-hash" {
		t.Fatalf("renewed.MaterialHash = %q", renewed.MaterialHash)
	}
	if renewed.ACMEInfo.CA != "LetsEncrypt" || renewed.ACMEInfo.Renew != "2026-07-10T00:00:00Z" {
		t.Fatalf("renewed.ACMEInfo = %+v", renewed.ACMEInfo)
	}
	if renewed.Revision != 4 {
		t.Fatalf("renewed.Revision = %d", renewed.Revision)
	}
}

func TestCertificateServiceRunRenewalPassSkipsIneligibleCertificates(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              41,
				Domain:          "disabled.example.com",
				Enabled:         false,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        2,
			},
			{
				ID:              42,
				Domain:          "local-http.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        3,
			},
			{
				ID:              43,
				Domain:          "future.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				ACMEInfo:        `{"Renew":"2026-05-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        4,
			},
		},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC) }

	if err := svc.RunRenewalPass(context.Background()); err != nil {
		t.Fatalf("RunRenewalPass() error = %v", err)
	}
	if len(issuer.calls) != 0 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}
	if store.saveManagedCall != 0 {
		t.Fatalf("expected no persistence for skipped certificates, saveManagedCall = %d", store.saveManagedCall)
	}
}

func TestCertificateServiceRunRenewalPassRecordsIssuerFailure(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              44,
			Domain:          "broken.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        7,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		errs: map[int]error{
			44: errors.New("cloudflare renewal failed"),
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC) }

	err := svc.RunRenewalPass(context.Background())
	if err == nil {
		t.Fatal("expected RunRenewalPass() to return error")
	}

	failed := managedCertificateFromRow(store.managedCerts[0])
	if failed.Status != "error" {
		t.Fatalf("failed.Status = %q", failed.Status)
	}
	if failed.LastError != "cloudflare renewal failed" {
		t.Fatalf("failed.LastError = %q", failed.LastError)
	}
	if failed.Revision != 7 {
		t.Fatalf("failed.Revision = %d", failed.Revision)
	}
}

func TestCertificateServiceRunRenewalPassStopsAfterIssuerFailure(t *testing.T) {
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              45,
				Domain:          "first.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        8,
			},
			{
				ID:              46,
				Domain:          "second.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				MaterialHash:    "before",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        9,
			},
			{
				ID:              47,
				Domain:          "skip.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["remote"]`,
				Status:          "pending",
				ACMEInfo:        `{"Renew":"2026-04-10T00:00:00Z"}`,
				CertificateType: "acme",
				Usage:           "https",
				Revision:        10,
			},
		},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		errs: map[int]error{
			45: errors.New("first renew failed"),
		},
		results: map[int]managedCertificateRenewalResult{
			46: {
				Changed:      true,
				LastIssueAt:  "2026-04-11T00:00:00Z",
				MaterialHash: "after",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "second.example.com",
					Renew:      "2026-07-10T00:00:00Z",
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc.now = func() time.Time { return now }

	err := svc.RunRenewalPass(context.Background())
	if err == nil {
		t.Fatal("expected RunRenewalPass() to return first renewal error")
	}
	if len(issuer.calls) != 1 || issuer.calls[0] != 45 {
		t.Fatalf("issuer calls = %+v", issuer.calls)
	}

	first := managedCertificateFromRow(store.managedCerts[0])
	if first.Status != "error" || first.LastError != "first renew failed" {
		t.Fatalf("first = %+v", first)
	}

	second := managedCertificateFromRow(store.managedCerts[1])
	if second.Status != "pending" || second.MaterialHash != "before" || second.Revision != 9 {
		t.Fatalf("second = %+v", second)
	}

	skipped := managedCertificateFromRow(store.managedCerts[2])
	if skipped.Status != "pending" || skipped.Revision != 10 {
		t.Fatalf("skipped = %+v", skipped)
	}
}

type fakeManagedCertificateRenewalIssuer struct {
	calls   []int
	results map[int]managedCertificateRenewalResult
	errs    map[int]error
}

func (f *fakeManagedCertificateRenewalIssuer) Renew(_ context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	f.calls = append(f.calls, cert.ID)
	if err := f.errs[cert.ID]; err != nil {
		return managedCertificateRenewalResult{}, err
	}
	return f.results[cert.ID], nil
}
