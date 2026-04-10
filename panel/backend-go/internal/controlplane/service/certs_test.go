package service

import (
	"context"
	"errors"
	"testing"

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
