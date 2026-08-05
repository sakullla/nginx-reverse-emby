package service

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPKIAuditValidatesQueriesAndRetainsProtectedEvents(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)
	repository := &pkiAuditTestRepository{}
	service, err := NewPKIAuditService(repository, 365*pkiDay, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPKIAuditService() error = %v", err)
	}
	event := NewPKIAuditEvent("certificate_issued", "scheduler", "identity-1", "succeeded", "", now)
	event.CertificateID = "certificate-1"
	event.SecurityRevision = 7
	event.ID = stablePKIAuditEventID(event)
	if err := service.Append(t.Context(), event); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	invalid := event
	invalid.ObjectID = ""
	if err := service.Append(t.Context(), invalid); err == nil {
		t.Fatal("Append(invalid event) returned nil error")
	}
	older := NewPKIAuditEvent("certificate_requested", "agent", "identity-1", "succeeded", "", now.Add(-time.Hour))
	newer := NewPKIAuditEvent("certificate_rotated", "scheduler", "identity-1", "succeeded", "", now.Add(time.Hour))
	repository.query = []PKIAuditEvent{older, newer}
	events, err := service.Query(t.Context(), PKIAuditFilter{ObjectID: "identity-1", Limit: 10})
	if err != nil || len(events) != 2 || events[0].ID != newer.ID || events[1].ID != older.ID {
		t.Fatalf("Query() = (%+v, %v)", events, err)
	}
	protected := []string{event.ID, "incident-event"}
	deleted, err := service.EnforceRetention(t.Context(), protected)
	wantProtected := []string{"incident-event", event.ID}
	if err != nil || deleted != 3 || !repository.cutoff.Equal(now.Add(-365*pkiDay)) || !reflect.DeepEqual(repository.protected, wantProtected) {
		t.Fatalf("EnforceRetention() = (%d, %v), cutoff %v, protected %v", deleted, err, repository.cutoff, repository.protected)
	}
}

func TestPKIAuditSerialFilterMatchesStructuredExactValuesOnly(t *testing.T) {
	t.Parallel()
	if pkiAuditDetailsMatchSerial(`{"message":"certificate abc123 failed","serial_hex":"abc1230"}`, "abc123") {
		t.Fatal("serial filter matched a message substring or a longer serial")
	}
	if !pkiAuditDetailsMatchSerial(`{"revocations":{"all_revoked_serials":["0x00ABC123","def456"]}}`, "abc123") {
		t.Fatal("serial filter rejected an exact structured serial")
	}
	if pkiAuditDetailsMatchSerial(`{"serial_hex":"not-hex"}`, "abc123") {
		t.Fatal("serial filter accepted malformed serial metadata")
	}
}

func TestPKIAlertDerivationIsStableAndRanksFailedClosedFirst(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 8, 0, 0, 0, time.UTC)
	state := PKIEndpointCertificateState{
		IdentityID: "identity-1", CertificateID: "certificate-1", Generation: 1,
		CertificateFingerprintSHA256: strings.Repeat("a", 64), PublicKeyFingerprintSHA256: strings.Repeat("b", 64),
		NotBefore: now.Add(-25 * time.Hour), NotAfter: now.Add(-time.Minute), FailureCount: 2,
	}
	decision, err := EvaluatePKIEndpointSchedule(DefaultInternalPKIPolicy(), state, now, false)
	if err != nil {
		t.Fatalf("EvaluatePKIEndpointSchedule() error = %v", err)
	}
	fact, ok := PKIEndpointAlertFact(state, decision, now, "renewal failed")
	if !ok || fact.Kind != PKIAlertKindFailedClosed || fact.Level != PKIAlertFailedClosed {
		t.Fatalf("endpoint alert fact = (%+v, %v)", fact, ok)
	}
	facts := []PKIAlertFact{
		{Kind: PKIAlertKindIdentity, ObjectType: "pki_identity", ObjectID: "identity-2", Level: PKIAlertCritical, FirstSeen: now, LastSeen: now, Reason: "owner mismatch"},
		fact,
	}
	alerts, err := DerivePKIAlerts(facts)
	if err != nil || len(alerts) != 2 || alerts[0].Level != PKIAlertFailedClosed {
		t.Fatalf("DerivePKIAlerts() = (%+v, %v)", alerts, err)
	}
	again, err := DerivePKIAlerts(facts)
	if err != nil || alerts[0].ID != again[0].ID || alerts[1].ID != again[1].ID {
		t.Fatalf("stable alert IDs = %v and %v, error %v", alerts, again, err)
	}
}

type pkiAuditTestRepository struct {
	appended  []PKIAuditEvent
	query     []PKIAuditEvent
	cutoff    time.Time
	protected []string
}

func (r *pkiAuditTestRepository) AppendPKIAuditEvent(_ context.Context, event PKIAuditEvent) error {
	r.appended = append(r.appended, event)
	return nil
}

func (r *pkiAuditTestRepository) QueryPKIAuditEvents(context.Context, PKIAuditFilter) ([]PKIAuditEvent, error) {
	return append([]PKIAuditEvent(nil), r.query...), nil
}

func (r *pkiAuditTestRepository) DeleteExpiredPKIAuditEvents(_ context.Context, cutoff time.Time, protected []string) (int64, error) {
	r.cutoff = cutoff
	r.protected = append([]string(nil), protected...)
	return 3, nil
}
