package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestCertificateServiceCreateUploadedPersistsValidatedMaterialAndHash(t *testing.T) {
	ca := mustCreateSelfSignedCA(t, "Upload Test CA")
	leaf := mustCreateLeafSignedByCA(t, "uploaded.example.com", ca)

	store := &relayCertStore{}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	created, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("uploaded.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("uploaded"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
		CertificatePEM:  stringPtr(strings.TrimSpace(leaf.CertPEM)),
		PrivateKeyPEM:   stringPtr(strings.TrimSpace(leaf.KeyPEM)),
		CAPEM:           stringPtr(strings.TrimSpace(ca.CertPEM)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	material, ok := store.materialsByHost["uploaded.example.com"]
	if !ok {
		t.Fatalf("missing persisted material: %+v", store.materialsByHost)
	}
	expectedCertPEM := fmt.Sprintf("%s\n%s", strings.TrimSpace(leaf.CertPEM), strings.TrimSpace(ca.CertPEM))
	if strings.TrimSpace(material.CertPEM) != strings.TrimSpace(expectedCertPEM) {
		t.Fatalf("persisted cert chain mismatch")
	}
	if strings.TrimSpace(material.KeyPEM) != strings.TrimSpace(leaf.KeyPEM) {
		t.Fatalf("persisted key mismatch")
	}
	expectedHash := hashManagedCertificateMaterial(strings.TrimSpace(expectedCertPEM), strings.TrimSpace(leaf.KeyPEM))
	if created.MaterialHash != expectedHash {
		t.Fatalf("created.MaterialHash = %q, want %q", created.MaterialHash, expectedHash)
	}
	if created.Status != "pending" {
		t.Fatalf("created.Status = %q", created.Status)
	}
	if created.LastIssueAt != "" {
		t.Fatalf("created.LastIssueAt = %q", created.LastIssueAt)
	}
}

func TestCertificateServiceUpdateUploadedPreservesMaterialWhenPEMFieldsOmitted(t *testing.T) {
	ca := mustCreateSelfSignedCA(t, "Upload Preserve CA")
	leaf := mustCreateLeafSignedByCA(t, "preserve.example.com", ca)
	persistedCert := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM)
	persistedKey := strings.TrimSpace(leaf.KeyPEM)
	persistedHash := hashManagedCertificateMaterial(persistedCert, persistedKey)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              31,
			Domain:          "preserve.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			MaterialHash:    persistedHash,
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        4,
		}},
		materialsByHost: map[string]relayMaterial{
			"preserve.example.com": {CertPEM: persistedCert, KeyPEM: persistedKey},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "local", 31, ManagedCertificateInput{
		Tags: &[]string{"rotated"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	material := store.materialsByHost["preserve.example.com"]
	if material.CertPEM != persistedCert || material.KeyPEM != persistedKey {
		t.Fatalf("updated material changed unexpectedly: %+v", material)
	}
	if updated.MaterialHash != persistedHash {
		t.Fatalf("updated.MaterialHash = %q, want %q", updated.MaterialHash, persistedHash)
	}
	if updated.Status != "pending" {
		t.Fatalf("updated.Status = %q", updated.Status)
	}
	if updated.LastIssueAt != "" {
		t.Fatalf("updated.LastIssueAt = %q", updated.LastIssueAt)
	}
}

func TestCertificateServiceUpdateUploadedMergesOmittedPEMFieldsFromPreviousMaterial(t *testing.T) {
	caA := mustCreateSelfSignedCA(t, "Upload Merge CA A")
	caB := mustCreateSelfSignedCA(t, "Upload Merge CA B")
	leaf := mustCreateLeafSignedByCA(t, "merge.example.com", caA)
	previousCert := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(caA.CertPEM)
	previousKey := strings.TrimSpace(leaf.KeyPEM)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              32,
			Domain:          "merge.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        5,
		}},
		materialsByHost: map[string]relayMaterial{
			"merge.example.com": {CertPEM: previousCert, KeyPEM: previousKey},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "local", 32, ManagedCertificateInput{
		CAPEM: stringPtr(strings.TrimSpace(caB.CertPEM)),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	material := store.materialsByHost["merge.example.com"]
	expectedCert := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(caB.CertPEM)
	if strings.TrimSpace(material.CertPEM) != expectedCert {
		t.Fatalf("material.CertPEM mismatch after CA-only merge")
	}
	if strings.TrimSpace(material.KeyPEM) != previousKey {
		t.Fatalf("material.KeyPEM mismatch after CA-only merge")
	}
	if updated.MaterialHash != hashManagedCertificateMaterial(expectedCert, previousKey) {
		t.Fatalf("updated.MaterialHash = %q", updated.MaterialHash)
	}
}

func TestCertificateServiceUpdateUploadedOmittedFieldsPreserveRawBytesAndHash(t *testing.T) {
	ca := mustCreateSelfSignedCA(t, "Upload Raw Preserve CA")
	leaf := mustCreateLeafSignedByCA(t, "raw-preserve.example.com", ca)
	leafPEM := strings.TrimSpace(leaf.CertPEM)
	caPEM := strings.TrimSpace(ca.CertPEM)
	preservedCert := leafPEM + "\n\n\n" + caPEM + "\n"
	preservedKey := strings.TrimSpace(leaf.KeyPEM)
	preservedHash := hashManagedCertificateMaterial(preservedCert, preservedKey)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              33,
			Domain:          "raw-preserve.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    preservedHash,
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        6,
		}},
		materialsByHost: map[string]relayMaterial{
			"raw-preserve.example.com": {CertPEM: preservedCert, KeyPEM: preservedKey},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "local", 33, ManagedCertificateInput{
		PrivateKeyPEM: stringPtr(preservedKey),
		Tags:          &[]string{"metadata-only"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	material := store.materialsByHost["raw-preserve.example.com"]
	if material.CertPEM != preservedCert {
		t.Fatalf("material.CertPEM changed unexpectedly")
	}
	if material.KeyPEM != preservedKey {
		t.Fatalf("material.KeyPEM changed unexpectedly")
	}
	if updated.MaterialHash != preservedHash {
		t.Fatalf("updated.MaterialHash = %q, want %q", updated.MaterialHash, preservedHash)
	}
}

func TestCertificateServiceUpdateUploadedSameDomainRestoreMaterialOnPersistenceFailure(t *testing.T) {
	oldCA := mustCreateSelfSignedCA(t, "Upload Rollback CA old")
	oldLeaf := mustCreateLeafSignedByCA(t, "rollback.example.com", oldCA)
	oldCert := strings.TrimSpace(oldLeaf.CertPEM) + "\n" + strings.TrimSpace(oldCA.CertPEM)
	oldKey := strings.TrimSpace(oldLeaf.KeyPEM)
	oldHash := hashManagedCertificateMaterial(oldCert, oldKey)

	newCA := mustCreateSelfSignedCA(t, "Upload Rollback CA new")
	newLeaf := mustCreateLeafSignedByCA(t, "rollback.example.com", newCA)
	newCert := strings.TrimSpace(newLeaf.CertPEM) + "\n" + strings.TrimSpace(newCA.CertPEM)
	newKey := strings.TrimSpace(newLeaf.KeyPEM)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              34,
			Domain:          "rollback.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    oldHash,
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        4,
		}},
		materialsByHost: map[string]relayMaterial{
			"rollback.example.com": {CertPEM: oldCert, KeyPEM: oldKey},
		},
		saveMaterialErrs: []error{
			errors.New("disk write failed"),
			nil,
		},
		saveMaterialPartialWriteOnError: true,
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 34, ManagedCertificateInput{
		CertificatePEM: stringPtr(strings.TrimSpace(newLeaf.CertPEM)),
		PrivateKeyPEM:  stringPtr(newKey),
		CAPEM:          stringPtr(strings.TrimSpace(newCA.CertPEM)),
	})
	if err == nil {
		t.Fatal("expected Update() error")
	}

	row := managedCertificateFromRow(store.managedCerts[0])
	if row.MaterialHash != oldHash || row.Revision != 4 {
		t.Fatalf("row not rolled back: %+v", row)
	}
	material := store.materialsByHost["rollback.example.com"]
	if strings.TrimSpace(material.CertPEM) != oldCert || strings.TrimSpace(material.KeyPEM) != oldKey {
		t.Fatalf("material not restored: %+v", material)
	}
	if strings.TrimSpace(material.CertPEM) == newCert {
		t.Fatalf("material incorrectly kept failed write payload")
	}
}

func TestCertificateServiceUpdateRejectsUploadedToNonUploadedTransition(t *testing.T) {
	ca := mustCreateSelfSignedCA(t, "Upload Transition CA")
	leaf := mustCreateLeafSignedByCA(t, "transition.example.com", ca)
	cert := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM)
	key := strings.TrimSpace(leaf.KeyPEM)
	hash := hashManagedCertificateMaterial(cert, key)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              35,
			Domain:          "transition.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    hash,
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        8,
		}},
		materialsByHost: map[string]relayMaterial{
			"transition.example.com": {CertPEM: cert, KeyPEM: key},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 35, ManagedCertificateInput{
		CertificateType: stringPtr("acme"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}
	row := managedCertificateFromRow(store.managedCerts[0])
	if row.CertificateType != "uploaded" || row.MaterialHash != hash {
		t.Fatalf("row changed unexpectedly: %+v", row)
	}
	material := store.materialsByHost["transition.example.com"]
	if strings.TrimSpace(material.CertPEM) != cert || strings.TrimSpace(material.KeyPEM) != key {
		t.Fatalf("material changed unexpectedly: %+v", material)
	}
}

func TestCertificateServiceUploadedCreateRejectsMissingOrInvalidMaterial(t *testing.T) {
	store := &relayCertStore{}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("missing.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("uploaded"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() missing material error = %v", err)
	}

	_, err = svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("invalid.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("uploaded"),
		Usage:           stringPtr("https"),
		Enabled:         boolPtr(true),
		CertificatePEM:  stringPtr("not-a-cert"),
		PrivateKeyPEM:   stringPtr("not-a-key"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() invalid PEM error = %v", err)
	}
}

func TestCertificateServiceUploadedUpdateRejectsMissingOrInvalidMaterial(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              39,
			Domain:          "update-missing.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        2,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 39, ManagedCertificateInput{
		Tags: &[]string{"keep-existing"},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() missing material error = %v", err)
	}

	_, err = svc.Update(context.Background(), "local", 39, ManagedCertificateInput{
		CertificatePEM: stringPtr("not-a-cert"),
		PrivateKeyPEM:  stringPtr("not-a-key"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() invalid PEM error = %v", err)
	}
}

func TestCertificateServiceUploadedIssueRejectsMissingMaterialAndSucceedsWhenPresent(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              41,
			Domain:          "issue.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "uploaded",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Issue(context.Background(), "local", 41); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() without material error = %v", err)
	}

	ca := mustCreateSelfSignedCA(t, "Issue CA")
	leaf := mustCreateLeafSignedByCA(t, "issue.example.com", ca)
	joined := strings.TrimSpace(leaf.CertPEM) + "\n" + strings.TrimSpace(ca.CertPEM)
	store.materialsByHost = map[string]relayMaterial{
		"issue.example.com": {CertPEM: joined, KeyPEM: strings.TrimSpace(leaf.KeyPEM)},
	}

	issued, err := svc.Issue(context.Background(), "local", 41)
	if err != nil {
		t.Fatalf("Issue() with material error = %v", err)
	}
	if issued.Status != "pending" {
		t.Fatalf("issued.Status = %q", issued.Status)
	}
	if issued.MaterialHash == "" {
		t.Fatalf("issued.MaterialHash is empty")
	}
	if issued.LastIssueAt != "" {
		t.Fatalf("issued.LastIssueAt = %q", issued.LastIssueAt)
	}
}

func TestCertificateServiceIssueMasterCFDNSSuccessPersistsMaterialAndUpdatesState(t *testing.T) {
	issuedMaterial := mustCreateSelfSignedCA(t, "master-issue-success.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              60,
			Domain:          "master-issue-success.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			LastError:       "old error",
			MaterialHash:    "old-hash",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        9,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			60: {
				Changed:     true,
				LastIssueAt: "2026-04-11T10:11:12Z",
				ACMEInfo: ManagedCertificateACMEInfo{
					MainDomain: "master-issue-success.example.com",
					CA:         "LetsEncrypt",
					Renew:      "2026-07-10T00:00:00Z",
				},
				Material: storage.ManagedCertificateBundle{
					Domain:  "master-issue-success.example.com",
					CertPEM: issuedMaterial.CertPEM,
					KeyPEM:  issuedMaterial.KeyPEM,
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)

	issued, err := svc.Issue(context.Background(), "local", 60)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Status != "active" {
		t.Fatalf("issued.Status = %q", issued.Status)
	}
	if issued.LastIssueAt != "2026-04-11T10:11:12Z" {
		t.Fatalf("issued.LastIssueAt = %q", issued.LastIssueAt)
	}
	if issued.LastError != "" {
		t.Fatalf("issued.LastError = %q", issued.LastError)
	}
	if issued.ACMEInfo.CA != "LetsEncrypt" || issued.ACMEInfo.Renew != "2026-07-10T00:00:00Z" {
		t.Fatalf("issued.ACMEInfo = %+v", issued.ACMEInfo)
	}
	expectedHash := hashManagedCertificateMaterial(strings.TrimSpace(issuedMaterial.CertPEM), strings.TrimSpace(issuedMaterial.KeyPEM))
	if issued.MaterialHash != expectedHash {
		t.Fatalf("issued.MaterialHash = %q, want %q", issued.MaterialHash, expectedHash)
	}
	if issued.Revision != 10 {
		t.Fatalf("issued.Revision = %d", issued.Revision)
	}
	persisted := store.materialsByHost["master-issue-success.example.com"]
	if persisted.CertPEM != strings.TrimSpace(issuedMaterial.CertPEM) || persisted.KeyPEM != strings.TrimSpace(issuedMaterial.KeyPEM) {
		t.Fatalf("persisted material mismatch: %+v", persisted)
	}
}

func TestCertificateServiceIssueMasterCFDNSSucceedsWhenRenewIsInFuture(t *testing.T) {
	issuedMaterial := mustCreateSelfSignedCA(t, "master-issue-future-renew.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              66,
			Domain:          "master-issue-future-renew.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			CertificateType: "acme",
			Usage:           "https",
			ACMEInfo:        `{"Main_Domain":"master-issue-future-renew.example.com","Renew":"2026-07-10T00:00:00Z"}`,
			Revision:        11,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			66: {
				Changed:     true,
				LastIssueAt: "2026-04-11T15:16:17Z",
				Material: storage.ManagedCertificateBundle{
					Domain:  "master-issue-future-renew.example.com",
					CertPEM: issuedMaterial.CertPEM,
					KeyPEM:  issuedMaterial.KeyPEM,
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)

	issued, err := svc.Issue(context.Background(), "local", 66)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Status != "active" {
		t.Fatalf("issued.Status = %q", issued.Status)
	}
	if issued.LastIssueAt != "2026-04-11T15:16:17Z" {
		t.Fatalf("issued.LastIssueAt = %q", issued.LastIssueAt)
	}
	if issued.Revision != 12 {
		t.Fatalf("issued.Revision = %d", issued.Revision)
	}
}

func TestCertificateServiceIssueMasterCFDNSIssuerFailureRecordsErrorState(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              61,
			Domain:          "master-issue-failure.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        7,
		}},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		errs: map[int]error{
			61: errors.New("cloudflare issue failed"),
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)

	_, err := svc.Issue(context.Background(), "local", 61)
	if err == nil {
		t.Fatal("expected Issue() error")
	}

	failed := managedCertificateFromRow(store.managedCerts[0])
	if failed.Status != "error" {
		t.Fatalf("failed.Status = %q", failed.Status)
	}
	if failed.LastError != "cloudflare issue failed" {
		t.Fatalf("failed.LastError = %q", failed.LastError)
	}
	if failed.Revision != 8 {
		t.Fatalf("failed.Revision = %d", failed.Revision)
	}
}

func TestCertificateServiceIssueMasterCFDNSMaterialPersistenceFailureRestoresState(t *testing.T) {
	previous := mustCreateSelfSignedCA(t, "master-issue-previous.example.com")
	issued := mustCreateSelfSignedCA(t, "master-issue-new.example.com")
	previousHash := hashManagedCertificateMaterial(previous.CertPEM, previous.KeyPEM)

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              62,
			Domain:          "master-issue-material-failure.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			LastError:       "old",
			MaterialHash:    previousHash,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        4,
		}},
		materialsByHost: map[string]relayMaterial{
			"master-issue-material-failure.example.com": {
				CertPEM: previous.CertPEM,
				KeyPEM:  previous.KeyPEM,
			},
		},
		saveMaterialErrs: []error{
			errors.New("persist failed"),
			nil,
		},
		saveMaterialPartialWriteOnError: true,
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			62: {
				Changed:     true,
				LastIssueAt: "2026-04-11T11:12:13Z",
				Material: storage.ManagedCertificateBundle{
					Domain:  "master-issue-material-failure.example.com",
					CertPEM: issued.CertPEM,
					KeyPEM:  issued.KeyPEM,
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)

	_, err := svc.Issue(context.Background(), "local", 62)
	if err == nil {
		t.Fatal("expected Issue() error")
	}

	failed := managedCertificateFromRow(store.managedCerts[0])
	if failed.Status != "error" {
		t.Fatalf("failed.Status = %q", failed.Status)
	}
	if failed.Revision != 5 {
		t.Fatalf("failed.Revision = %d", failed.Revision)
	}
	if failed.MaterialHash != previousHash {
		t.Fatalf("failed.MaterialHash = %q, want %q", failed.MaterialHash, previousHash)
	}
	persisted := store.materialsByHost["master-issue-material-failure.example.com"]
	if persisted.CertPEM != previous.CertPEM || persisted.KeyPEM != previous.KeyPEM {
		t.Fatalf("material was not restored: %+v", persisted)
	}
}

func TestCertificateServiceIssueMasterCFDNSFirstIssueMaterialPersistenceFailureWithCleanupFailure(t *testing.T) {
	issued := mustCreateSelfSignedCA(t, "master-issue-first-no-previous.example.com")
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              72,
			Domain:          "master-issue-first-no-previous.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			LastError:       "",
			MaterialHash:    "",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        6,
		}},
		saveMaterialErrs: []error{
			errors.New("persist failed"),
		},
		saveMaterialPartialWriteOnError: true,
		cleanupErrs:                     []error{errors.New("cleanup failed")},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{
		results: map[int]managedCertificateRenewalResult{
			72: {
				Changed:     true,
				LastIssueAt: "2026-04-11T18:19:20Z",
				Material: storage.ManagedCertificateBundle{
					Domain:  "master-issue-first-no-previous.example.com",
					CertPEM: issued.CertPEM,
					KeyPEM:  issued.KeyPEM,
				},
			},
		},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)

	_, err := svc.Issue(context.Background(), "local", 72)
	if err == nil {
		t.Fatal("expected Issue() error")
	}
	if !strings.Contains(err.Error(), "restore failed: cleanup failed") {
		t.Fatalf("Issue() error = %v", err)
	}

	row := managedCertificateFromRow(store.managedCerts[0])
	if row.Status != "pending" || row.Revision != 6 || row.LastError != "" {
		t.Fatalf("row changed unexpectedly after restore failure: %+v", row)
	}
	if store.saveManagedCall != 0 {
		t.Fatalf("saveManagedCall = %d, want 0", store.saveManagedCall)
	}
}

func TestCertificateServiceIssueMasterCFDNSRejectsIneligibleCertificates(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              63,
				Domain:          "disabled.example.com",
				Enabled:         false,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				CertificateType: "acme",
				Usage:           "https",
				Revision:        2,
			},
			{
				ID:              64,
				Domain:          "wrong-type.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				CertificateType: "uploaded",
				Usage:           "https",
				Revision:        3,
			},
			{
				ID:              65,
				Domain:          "ip-scope.example.com",
				Enabled:         true,
				Scope:           "ip",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				CertificateType: "acme",
				Usage:           "https",
				Revision:        4,
			},
			{
				ID:              67,
				Domain:          "wrong-target.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "master_cf_dns",
				TargetAgentIDs:  `["local","edge-1"]`,
				Status:          "pending",
				CertificateType: "acme",
				Usage:           "https",
				Revision:        5,
			},
			{
				ID:              68,
				Domain:          "wrong-issuer.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "pending",
				CertificateType: "acme",
				Usage:           "https",
				Revision:        6,
			},
		},
	}
	issuer := &fakeManagedCertificateRenewalIssuer{}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, issuer)

	if _, err := svc.Issue(context.Background(), "local", 63); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() disabled cert error = %v", err)
	}
	if _, err := svc.Issue(context.Background(), "local", 64); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() wrong type cert error = %v", err)
	}
	if _, err := svc.Issue(context.Background(), "local", 65); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() wrong scope cert error = %v", err)
	}
	if _, err := svc.Issue(context.Background(), "local", 67); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() wrong target cert error = %v", err)
	}
}

func TestCertificateServiceIssueLocalHTTP01ACMERejectsMultiTargetGenericIssue(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              69,
			Domain:          "local-http01-acme.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local","edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, &fakeManagedCertificateRenewalIssuer{})

	_, err := svc.Issue(context.Background(), "", 69)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: local_http01 certificates must be issued from the per-agent endpoint" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestCertificateServiceIssueLocalHTTP01ACMEPerAgentMarksOnlyRequestedAgentPendingAndBumpsRevision(t *testing.T) {
	now := time.Date(2026, 4, 11, 12, 13, 14, 0, time.UTC)
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          1,
				AgentID:     "edge-1",
				FrontendURL: "https://media.example.com",
				Enabled:     true,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              70,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1","edge-2"]`,
			Status:          "active",
			LastIssueAt:     "2026-04-01T00:00:00Z",
			LastError:       "stale error",
			MaterialHash:    "global-hash",
			AgentReports:    `{"edge-1":{"status":"active","last_issue_at":"2026-04-10T10:11:12Z","last_error":"edge error","material_hash":"edge-hash","acme_info":{"Main_Domain":"media.example.com","Profile":"default"},"updated_at":"2026-04-10T10:11:12Z"},"edge-2":{"status":"active","last_issue_at":"2026-04-09T09:08:07Z","last_error":"","material_hash":"edge-2-hash","acme_info":{"Main_Domain":"media.example.com","Profile":"other"},"updated_at":"2026-04-09T09:08:07Z"}}`,
			ACMEInfo:        `{"Main_Domain":"media.example.com","Profile":"global"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        8,
		}},
	}
	svc := newCertificateServiceWithRenewal(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store, &fakeManagedCertificateRenewalIssuer{})
	svc.now = func() time.Time { return now }

	issued, err := svc.Issue(context.Background(), "edge-1", 70)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Status != "pending" {
		t.Fatalf("issued.Status = %q", issued.Status)
	}
	if issued.LastError != "" {
		t.Fatalf("issued.LastError = %q", issued.LastError)
	}
	if issued.LastIssueAt != "2026-04-01T00:00:00Z" {
		t.Fatalf("issued.LastIssueAt = %q", issued.LastIssueAt)
	}
	if issued.Revision != 9 {
		t.Fatalf("issued.Revision = %d", issued.Revision)
	}

	edge1 := issued.AgentReports["edge-1"]
	if edge1.Status != "pending" {
		t.Fatalf("edge-1 status = %q", edge1.Status)
	}
	if edge1.LastIssueAt != "2026-04-10T10:11:12Z" {
		t.Fatalf("edge-1 last_issue_at = %q", edge1.LastIssueAt)
	}
	if edge1.LastError != "" {
		t.Fatalf("edge-1 last_error = %q", edge1.LastError)
	}
	if edge1.MaterialHash != "" {
		t.Fatalf("edge-1 material_hash = %q", edge1.MaterialHash)
	}
	if edge1.ACMEInfo != (ManagedCertificateACMEInfo{}) {
		t.Fatalf("edge-1 acme_info = %+v", edge1.ACMEInfo)
	}
	if edge1.UpdatedAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("edge-1 updated_at = %q", edge1.UpdatedAt)
	}

	edge2 := issued.AgentReports["edge-2"]
	if edge2.Status != "active" {
		t.Fatalf("edge-2 status = %q", edge2.Status)
	}
	if edge2.MaterialHash != "edge-2-hash" {
		t.Fatalf("edge-2 material_hash = %q", edge2.MaterialHash)
	}

	persisted := managedCertificateFromRow(store.managedCerts[0])
	if persisted.Status != "pending" {
		t.Fatalf("persisted.Status = %q", persisted.Status)
	}
	if persisted.LastError != "" {
		t.Fatalf("persisted.LastError = %q", persisted.LastError)
	}
	if persisted.Revision != 9 {
		t.Fatalf("persisted.Revision = %d", persisted.Revision)
	}
	if persisted.AgentReports["edge-1"].Status != "pending" {
		t.Fatalf("persisted edge-1 status = %q", persisted.AgentReports["edge-1"].Status)
	}
	if persisted.AgentReports["edge-2"].Status != "active" {
		t.Fatalf("persisted edge-2 status = %q", persisted.AgentReports["edge-2"].Status)
	}
}

func TestCertificateServiceIssueLocalHTTP01ACMERejectsTargetWithoutLocalACME(t *testing.T) {
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          1,
				AgentID:     "edge-1",
				FrontendURL: "https://media.example.com",
				Enabled:     true,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              71,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Issue(context.Background(), "edge-1", 71)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: target agent does not support local ACME issuance: Edge 1" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestCertificateServiceIssueLocalHTTP01ACMERejectsTargetWithoutMatchingHTTPSRule(t *testing.T) {
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          1,
				AgentID:     "edge-1",
				FrontendURL: "http://media.example.com",
				Enabled:     true,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              72,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Issue(context.Background(), "edge-1", 72)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: no enabled HTTPS HTTP rule found for media.example.com on agent Edge 1" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestCertificateServiceIssueLocalHTTP01ACMERejectsRequestedAgentNotAssigned(t *testing.T) {
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-2",
			Name:             "Edge 2",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              73,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Issue(context.Background(), "edge-2", 73)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: certificate is not assigned to the requested agent" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestCertificateServiceIssueLocalHTTP01ACMEReturnsInvalidArgumentWhenSelectedTargetAgentMissing(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              74,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			CertificateType: "acme",
			Usage:           "https",
			Revision:        3,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Issue(context.Background(), "edge-1", 74)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Issue() error = %v", err)
	}
	if err.Error() != "invalid argument: target agent not found: edge-1" {
		t.Fatalf("Issue() error = %v", err)
	}
}

func TestCertificateServiceUploadedLocalHTTP01RequiresCertInstallCapableTargets(t *testing.T) {
	ca := mustCreateSelfSignedCA(t, "Capabilities CA")
	leaf := mustCreateLeafSignedByCA(t, "targets.example.com", ca)

	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "edge-1",
			CapabilitiesJSON: `["http_rules"]`,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", ManagedCertificateInput{
		Domain:          stringPtr("targets.example.com"),
		Scope:           stringPtr("domain"),
		IssuerMode:      stringPtr("local_http01"),
		CertificateType: stringPtr("uploaded"),
		Usage:           stringPtr("https"),
		TargetAgentIDs:  &[]string{"edge-1"},
		CertificatePEM:  stringPtr(strings.TrimSpace(leaf.CertPEM)),
		PrivateKeyPEM:   stringPtr(strings.TrimSpace(leaf.KeyPEM)),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() missing cert_install capability error = %v", err)
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

func TestCertificateServiceGlobalListReturnsFullManagedCertificateSetWithoutOverlay(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:             91,
				Domain:         "shared.example.com",
				Enabled:        true,
				Scope:          "domain",
				IssuerMode:     "local_http01",
				TargetAgentIDs: `["local","edge-1"]`,
				Status:         "pending",
				AgentReports:   `{"local":{"status":"active","last_issue_at":"2026-04-10T12:00:00Z","last_error":"","material_hash":"local-hash"}}`,
				Usage:          "https",
				Revision:       3,
			},
			{
				ID:             92,
				Domain:         "edge-only.example.com",
				Enabled:        true,
				Scope:          "domain",
				IssuerMode:     "local_http01",
				TargetAgentIDs: `["edge-1"]`,
				Status:         "active",
				Usage:          "https",
				Revision:       4,
			},
		},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	certs, err := svc.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List(global) error = %v", err)
	}
	if len(certs) != 2 {
		t.Fatalf("len(certs) = %d", len(certs))
	}
	if certs[0].ID != 91 || certs[0].Status != "pending" {
		t.Fatalf("certs[0] = %+v", certs[0])
	}
	if certs[1].ID != 92 {
		t.Fatalf("certs[1] = %+v", certs[1])
	}
}

func TestCertificateServiceGlobalUpdateCanMutateCertificateNotAssignedToLocalAgent(t *testing.T) {
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install"]`,
		}},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              93,
			Domain:          "edge-only.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "pending",
			Usage:           "https",
			CertificateType: "acme",
			Revision:        5,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	updated, err := svc.Update(context.Background(), "", 93, ManagedCertificateInput{
		Domain: stringPtr("edge-only-updated.example.com"),
	})
	if err != nil {
		t.Fatalf("Update(global) error = %v", err)
	}
	if updated.Domain != "edge-only-updated.example.com" {
		t.Fatalf("updated.Domain = %q", updated.Domain)
	}
	row := managedCertificateFromRow(store.managedCerts[0])
	if row.Domain != "edge-only-updated.example.com" {
		t.Fatalf("row.Domain = %q", row.Domain)
	}
}

func TestCertificateServiceGlobalDeleteRemovesSharedCertificateCompletely(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              94,
			Domain:          "shared-delete.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local","edge-1"]`,
			Status:          "active",
			Usage:           "https",
			CertificateType: "acme",
			Revision:        7,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "", 94)
	if err != nil {
		t.Fatalf("Delete(global) error = %v", err)
	}
	if deleted.ID != 94 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed cert rows should be fully deleted: %+v", store.managedCerts)
	}
}

func TestCertificateServiceDeleteRejectsReferencedAutoRelayListenerCertificate(t *testing.T) {
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:            4,
				AgentID:       "local",
				Name:          "relay-auto",
				CertificateID: intPtrStorage(80),
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              80,
			Domain:          "relay-auto.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "relay_tunnel",
			CertificateType: "internal_ca",
			TagsJSON:        `["relay","auto","listener:4"]`,
			Revision:        5,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Delete(context.Background(), "local", 80)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete() error = %v", err)
	}
	if err.Error() != "invalid argument: certificate 80 is referenced by relay listener 4 on agent local" {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("managed certs mutated unexpectedly: %+v", store.managedCerts)
	}
}

func TestCertificateServiceDeleteRejectsReferencedSharedAutoRelayListenerCertificateDetach(t *testing.T) {
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:            5,
				AgentID:       "local",
				Name:          "relay-shared",
				CertificateID: intPtrStorage(81),
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              81,
			Domain:          "relay-shared.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local","edge-1"]`,
			Status:          "active",
			Usage:           "relay_tunnel",
			CertificateType: "internal_ca",
			TagsJSON:        `["auto","auto:relay-listener","listener:5","agent:local"]`,
			Revision:        6,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Delete(context.Background(), "local", 81)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete() error = %v", err)
	}
	if err.Error() != "invalid argument: certificate 81 is referenced by relay listener 5 on agent local" {
		t.Fatalf("Delete() error = %v", err)
	}
	remaining := managedCertificateFromRow(store.managedCerts[0])
	if len(remaining.TargetAgentIDs) != 2 {
		t.Fatalf("remaining.TargetAgentIDs = %+v", remaining.TargetAgentIDs)
	}
}

func TestCertificateServiceDeleteAllowsReferencedNonAutoCertificate(t *testing.T) {
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:            6,
				AgentID:       "local",
				Name:          "relay-manual",
				CertificateID: intPtrStorage(82),
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              82,
			Domain:          "manual.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "https",
			CertificateType: "uploaded",
			Revision:        7,
		}},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "local", 82)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 82 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed cert rows should be deleted: %+v", store.managedCerts)
	}
}

func TestCertificateServiceDeleteSucceedsWhenCleanupFailsPostCommit(t *testing.T) {
	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              33,
			Domain:          "cleanup-failure.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			Usage:           "https",
			CertificateType: "acme",
			Revision:        7,
		}},
		cleanupErrs: []error{errors.New("cleanup failed")},
	}
	svc := NewCertificateService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "local", 33)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 33 {
		t.Fatalf("Delete() id = %d", deleted.ID)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed cert rows should stay committed after cleanup failure: %+v", store.managedCerts)
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

func TestNewCertificateServiceDoesNotAutoWireManagedDNSRenewalIssuer(t *testing.T) {
	t.Setenv("CF_TOKEN", "token")

	svc := NewCertificateService(config.Config{
		EnableLocalAgent:              true,
		LocalAgentID:                  "local",
		ManagedDNSCertificatesEnabled: true,
	}, &relayCertStore{})

	if svc.renewalIssuer != nil {
		t.Fatal("renewalIssuer should not be auto-wired by default constructor")
	}
}

type fakeManagedCertificateRenewalIssuer struct {
	calls   []int
	results map[int]managedCertificateRenewalResult
	errs    map[int]error
}

func (f *fakeManagedCertificateRenewalIssuer) Issue(_ context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	f.calls = append(f.calls, cert.ID)
	if err := f.errs[cert.ID]; err != nil {
		return managedCertificateRenewalResult{}, err
	}
	return f.results[cert.ID], nil
}

func (f *fakeManagedCertificateRenewalIssuer) Renew(_ context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	f.calls = append(f.calls, cert.ID)
	if err := f.errs[cert.ID]; err != nil {
		return managedCertificateRenewalResult{}, err
	}
	return f.results[cert.ID], nil
}
