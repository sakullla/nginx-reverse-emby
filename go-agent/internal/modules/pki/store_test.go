package pki

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestPrepareEnrollmentPersistsReplaySafeKeyAndCSR(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	spec := EnrollmentSpec{StorageIdentity: "agent", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient}
	first, err := store.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("PrepareEnrollment() error = %v", err)
	}
	second, err := store.PrepareEnrollment(context.Background(), spec)
	if err != nil {
		t.Fatalf("replayed PrepareEnrollment() error = %v", err)
	}
	if first.Request.RequestID != second.Request.RequestID || first.Request.CSRPEM != second.Request.CSRPEM || first.RequestFingerprint != second.RequestFingerprint {
		t.Fatal("pending enrollment did not replay the identical request")
	}
	request, err := parseCSRPEM([]byte(first.Request.CSRPEM))
	if err != nil {
		t.Fatalf("parse generated CSR: %v", err)
	}
	if request.Subject.String() != "" || len(request.Extensions) != 0 || len(request.URIs) != 0 || len(request.DNSNames) != 0 || len(request.IPAddresses) != 0 {
		t.Fatalf("anonymous CSR is not empty: %+v", request)
	}
	for _, name := range []string{pendingKeyName, pendingCSRName, pendingJournalName} {
		path := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, name)
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat %s: %v", name, statErr)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s permissions = %o", name, info.Mode().Perm())
		}
	}
	_, err = store.PrepareEnrollment(context.Background(), EnrollmentSpec{
		StorageIdentity: "agent", DomainID: "other-domain", AgentID: "agent-1",
		Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient,
	})
	if !errors.Is(err, ErrPendingConflict) {
		t.Fatalf("conflicting PrepareEnrollment() error = %v", err)
	}
}
