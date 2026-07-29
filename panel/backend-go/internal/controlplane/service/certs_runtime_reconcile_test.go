package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type localRuntimeManagedCertificateStoreStub struct {
	managedCerts []storage.ManagedCertificateRow
	rulesByAgent map[string][]storage.HTTPRuleRow
	saveCalled   bool
}

func (s *localRuntimeManagedCertificateStoreStub) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return append([]storage.ManagedCertificateRow(nil), s.managedCerts...), nil
}

func (s *localRuntimeManagedCertificateStoreStub) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), s.rulesByAgent[agentID]...), nil
}

func (s *localRuntimeManagedCertificateStoreStub) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	s.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
	s.saveCalled = true
	return nil
}

func (s *localRuntimeManagedCertificateStoreStub) UpdateManagedCertificates(_ context.Context, update func([]storage.ManagedCertificateRow) ([]storage.ManagedCertificateRow, bool, error)) error {
	next, changed, err := update(append([]storage.ManagedCertificateRow(nil), s.managedCerts...))
	if err != nil || !changed {
		return err
	}
	return s.SaveManagedCertificates(context.Background(), next)
}

func TestReconcileManagedCertificatesFromLocalRuntimeStateUsesMetadataDrivenErrorOutcome(t *testing.T) {
	t.Parallel()
	store := &localRuntimeManagedCertificateStoreStub{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    "hash-21",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"sync.example.com"}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          9,
				AgentID:     "local",
				FrontendURL: "https://sync.example.com",
				Enabled:     true,
				Revision:    4,
			}},
		},
	}

	err := ReconcileManagedCertificatesFromLocalRuntimeState(context.Background(), store, "local", storage.RuntimeState{
		CurrentRevision:   4,
		LastApplyRevision: 2,
		LastApplyStatus:   "success",
		Status:            "active",
		Metadata: map[string]string{
			"last_sync_error":     "apply failed",
			"last_apply_revision": "4",
			"last_apply_status":   "error",
			"last_apply_message":  "apply failed",
		},
	}, time.Date(2026, time.April, 11, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ReconcileManagedCertificatesFromLocalRuntimeState() error = %v", err)
	}
	if !store.saveCalled {
		t.Fatal("SaveManagedCertificates() was not called")
	}

	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Status != "error" || cert.LastError != "apply failed" {
		t.Fatalf("unexpected reconciled cert = %+v", cert)
	}
	report := cert.AgentReports["local"]
	if report.Status != "error" || report.LastError != "apply failed" {
		t.Fatalf("unexpected reconciled report = %+v", report)
	}
}

func TestReconcileManagedCertificatesFromLocalRuntimeStateKeepsExplicitReportsAuthoritativeOnError(t *testing.T) {
	t.Parallel()
	store := &localRuntimeManagedCertificateStoreStub{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              22,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    "hash-22",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"sync.example.com"}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          9,
				AgentID:     "local",
				FrontendURL: "https://sync.example.com",
				Enabled:     true,
				Revision:    4,
			}},
		},
	}

	err := ReconcileManagedCertificatesFromLocalRuntimeState(context.Background(), store, "local", storage.RuntimeState{
		CurrentRevision: 4,
		Status:          "active",
		Metadata: map[string]string{
			"last_sync_error":   "apply failed",
			"last_apply_status": "error",
		},
		ManagedCertificateReports: []storage.ManagedCertificateReport{{
			ID:           22,
			Domain:       "SYNC.EXAMPLE.COM",
			Status:       "active",
			LastIssueAt:  "2026-04-11T13:00:00Z",
			MaterialHash: "hash-22-new",
			ACMEInfo: storage.ManagedCertificateACMEInfo{
				MainDomain: "sync.example.com",
			},
		}},
	}, time.Date(2026, time.April, 11, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ReconcileManagedCertificatesFromLocalRuntimeState() error = %v", err)
	}
	if !store.saveCalled {
		t.Fatal("SaveManagedCertificates() was not called")
	}

	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Status != "active" || cert.MaterialHash != "hash-22-new" {
		t.Fatalf("explicit report should remain authoritative even when apply status is error: %+v", cert)
	}
	report := cert.AgentReports["local"]
	if report.Status != "active" || report.MaterialHash != "hash-22-new" {
		t.Fatalf("unexpected explicit report overlay = %+v", report)
	}
}

func TestManagedCertificateHeartbeatReportsFromRuntimeState(t *testing.T) {
	t.Parallel()
	converted := managedCertificateHeartbeatReportsFromRuntimeState([]storage.ManagedCertificateReport{{
		ID:           99,
		Domain:       "a.example.com",
		Status:       "active",
		LastIssueAt:  "2026-04-11T13:00:00Z",
		LastError:    "",
		MaterialHash: "hash-99",
		NotAfter:     "2026-07-10T13:00:00Z",
		ACMEInfo: storage.ManagedCertificateACMEInfo{
			MainDomain: "a.example.com",
			KeyLength:  "ec256",
		},
		UpdatedAt: "2026-04-11T13:30:00Z",
	}})
	if len(converted) != 1 {
		t.Fatalf("converted reports = %+v", converted)
	}
	if converted[0].NotAfter != "2026-07-10T13:00:00Z" {
		t.Fatalf("converted NotAfter = %q", converted[0].NotAfter)
	}
	raw, err := json.Marshal(converted[0].ACMEInfo)
	if err != nil {
		t.Fatalf("json.Marshal(ACMEInfo) error = %v", err)
	}
	if string(raw) == "{}" {
		t.Fatalf("ACMEInfo unexpectedly empty after conversion: %s", raw)
	}
}

type concurrentLocalRuntimeManagedCertificateStore struct {
	mu               sync.Mutex
	managedCerts     []storage.ManagedCertificateRow
	operationStarted chan struct{}
	resumeOperation  chan struct{}
	startOnce        sync.Once
}

func (s *concurrentLocalRuntimeManagedCertificateStore) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	s.mu.Lock()
	rows := append([]storage.ManagedCertificateRow(nil), s.managedCerts...)
	s.mu.Unlock()
	s.startOnce.Do(func() { close(s.operationStarted) })
	<-s.resumeOperation
	return rows, nil
}

func (*concurrentLocalRuntimeManagedCertificateStore) ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error) {
	return nil, nil
}

func (s *concurrentLocalRuntimeManagedCertificateStore) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	currentPointers := make(map[int][2]string, len(s.managedCerts))
	for _, row := range s.managedCerts {
		currentPointers[row.ID] = [2]string{row.ActiveGenerationID, row.PendingGenerationID}
	}
	for index := range rows {
		if pointers, ok := currentPointers[rows[index].ID]; ok {
			rows[index].ActiveGenerationID = pointers[0]
			rows[index].PendingGenerationID = pointers[1]
		}
	}
	s.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
	return nil
}

func (s *concurrentLocalRuntimeManagedCertificateStore) UpdateManagedCertificates(_ context.Context, update func([]storage.ManagedCertificateRow) ([]storage.ManagedCertificateRow, bool, error)) error {
	s.startOnce.Do(func() { close(s.operationStarted) })
	<-s.resumeOperation
	s.mu.Lock()
	defer s.mu.Unlock()
	current := append([]storage.ManagedCertificateRow(nil), s.managedCerts...)
	next, changed, err := update(current)
	if err != nil || !changed {
		return err
	}
	currentPointers := make(map[int][2]string, len(current))
	for _, row := range current {
		currentPointers[row.ID] = [2]string{row.ActiveGenerationID, row.PendingGenerationID}
	}
	for index := range next {
		if pointers, ok := currentPointers[next[index].ID]; ok {
			next[index].ActiveGenerationID = pointers[0]
			next[index].PendingGenerationID = pointers[1]
		}
	}
	s.managedCerts = append([]storage.ManagedCertificateRow(nil), next...)
	return nil
}

func (s *concurrentLocalRuntimeManagedCertificateStore) promoteRemotely(row storage.ManagedCertificateRow) {
	s.mu.Lock()
	s.managedCerts = []storage.ManagedCertificateRow{row}
	s.mu.Unlock()
}

func (s *concurrentLocalRuntimeManagedCertificateStore) snapshot() storage.ManagedCertificateRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.managedCerts[0]
}

func TestReconcileManagedCertificatesFromLocalRuntimeStateDoesNotOverwriteConcurrentRemotePromotion(t *testing.T) {
	initial := storage.ManagedCertificateRow{
		ID: 31, Domain: "shared.example.com", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["local","remote"]`, Status: "pending", MaterialHash: "old-hash", AgentReports: `{}`,
		ACMEInfo: `{}`, Usage: "https", CertificateType: "acme", Revision: 7, NotAfter: "2026-08-01T00:00:00Z",
		ActiveGenerationID: "generation-old", PendingGenerationID: "generation-new",
	}
	store := &concurrentLocalRuntimeManagedCertificateStore{
		managedCerts:     []storage.ManagedCertificateRow{initial},
		operationStarted: make(chan struct{}),
		resumeOperation:  make(chan struct{}),
	}
	reconcileDone := make(chan error, 1)
	go func() {
		reconcileDone <- ReconcileManagedCertificatesFromLocalRuntimeState(context.Background(), store, "local", storage.RuntimeState{
			ManagedCertificateReports: []storage.ManagedCertificateReport{{
				ID: 31, Domain: "shared.example.com", Status: "active", MaterialHash: "new-hash", UpdatedAt: "2026-07-26T09:00:00Z",
			}},
		}, time.Date(2026, time.July, 26, 9, 0, 0, 0, time.UTC))
	}()

	<-store.operationStarted
	promoted := initial
	promoted.Status = "active"
	promoted.MaterialHash = "new-hash"
	promoted.NotAfter = "2026-10-24T00:00:00Z"
	promoted.AgentReports = `{"remote":{"status":"active","material_hash":"new-hash","updated_at":"2026-07-26T08:59:59Z"}}`
	promoted.ActiveGenerationID = "generation-new"
	promoted.PendingGenerationID = ""
	store.promoteRemotely(promoted)
	close(store.resumeOperation)
	if err := <-reconcileDone; err != nil {
		t.Fatalf("ReconcileManagedCertificatesFromLocalRuntimeState() error = %v", err)
	}

	row := store.snapshot()
	if row.Status != promoted.Status || row.MaterialHash != promoted.MaterialHash || row.NotAfter != promoted.NotAfter {
		t.Fatalf("concurrent promotion metadata was overwritten: got %+v, want status/hash/not_after from %+v", row, promoted)
	}
	if row.ActiveGenerationID != "generation-new" || row.PendingGenerationID != "" {
		t.Fatalf("generation pointers after reconciliation = active %q pending %q", row.ActiveGenerationID, row.PendingGenerationID)
	}
	cert := managedCertificateFromRow(row)
	if _, ok := cert.AgentReports["remote"]; !ok {
		t.Fatalf("remote promotion report was lost: %+v", cert.AgentReports)
	}
	if _, ok := cert.AgentReports["local"]; !ok {
		t.Fatalf("local runtime report was not merged: %+v", cert.AgentReports)
	}
}
