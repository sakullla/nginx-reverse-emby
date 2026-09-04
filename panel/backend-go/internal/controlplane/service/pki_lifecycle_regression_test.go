package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKIEndpointRotationRejectsCandidateExpiredDuringStaging(t *testing.T) {
	startedAt := time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(2 * time.Hour)
	currentTime := startedAt
	active := PKIEndpointCertificateState{
		IdentityID: "identity-staging", CertificateID: "cert-active", Generation: 1,
		CertificateFingerprintSHA256: testPKISHA256("a"), PublicKeyFingerprintSHA256: testPKISHA256("d"),
		NotBefore: startedAt.Add(-89 * 24 * time.Hour), NotAfter: startedAt.Add(3 * time.Hour),
	}
	repository := newRegressionPKIEndpointRepository(active)
	rotator := &advancingPKIRotator{
		candidate: PKIEndpointRotationCandidate{
			CertificateID: "cert-expired-during-staging", Generation: 2,
			CertificateFingerprintSHA256: testPKISHA256("b"),
			PublicKeyFingerprintSHA256:   testPKISHA256("c"),
			NotBefore:                    startedAt.Add(-time.Minute), NotAfter: startedAt.Add(time.Hour), Verified: true,
		},
		afterStage: func() { currentTime = completedAt },
	}
	service, err := NewPKILifecycleService(PKILifecycleServiceOptions{
		Policy: DefaultInternalPKIPolicy(), Repository: repository, Rotator: rotator,
		Lease: regressionPKILeaseGate{}, Clock: func() time.Time { return currentTime },
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.RunEndpointRotation(t.Context(), active.IdentityID, true)
	if !errors.Is(err, ErrPKILifecycleInvalid) {
		t.Fatalf("RunEndpointRotation() error = %v, want ErrPKILifecycleInvalid", err)
	}
	if result.Activated || repository.activations != 0 {
		t.Fatalf("expired staged candidate was activated: result=%+v activations=%d", result, repository.activations)
	}
	if len(repository.failures) != 1 || !repository.failures[0].Event.OccurredAt.Equal(completedAt) {
		t.Fatalf("rotation failure did not use completion time: %+v", repository.failures)
	}
}

func TestProtectedRestoreEmbedsConfiguredMasterKeyInsidePKIRoot(t *testing.T) {
	dataRoot := t.TempDir()
	masterKey := filepath.Join(dataRoot, "pki", "keys", "master.key")
	target, err := NewProductionPKIBackupRestoreTarget(PKIBackupRestoreTargetOptions{
		Store: &storage.GormStore{}, Vault: &PKIVault{}, DataRoot: dataRoot, MasterKeyFile: masterKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	vaultStageRoot := filepath.Join(t.TempDir(), "vault-stage")
	stagedMasterKey, cleanupRoot, err := target.preparePKIRestoreMasterKeyStage(vaultStageRoot)
	if err != nil {
		t.Fatal(err)
	}
	wantMasterKey := filepath.Join(vaultStageRoot, "pki", "keys", "master.key")
	if stagedMasterKey != wantMasterKey || cleanupRoot != "" {
		t.Fatalf("master-key stage = (%q, %q), want (%q, embedded)", stagedMasterKey, cleanupRoot, wantMasterKey)
	}
	swaps := target.pkiRestorePathSwaps(vaultStageRoot, stagedMasterKey)
	if len(swaps) != 1 {
		t.Fatalf("embedded master key created overlapping restore swaps: %+v", swaps)
	}
	if swaps[0].ActivePath != filepath.Join(dataRoot, "pki") || swaps[0].StagedPath != filepath.Join(vaultStageRoot, "pki") {
		t.Fatalf("PKI restore swap = %+v", swaps[0])
	}
}

func testPKISHA256(marker string) string {
	return strings.Repeat(marker, 64)[:64]
}

type advancingPKIRotator struct {
	candidate  PKIEndpointRotationCandidate
	afterStage func()
}

func (r *advancingPKIRotator) StageAndVerifyPKIEndpoint(context.Context, PKIEndpointCertificateState, bool) (PKIEndpointRotationCandidate, error) {
	if r.afterStage != nil {
		r.afterStage()
	}
	return r.candidate, nil
}

type regressionPKILeaseGate struct{}

func (regressionPKILeaseGate) RequirePKILease(context.Context) (PKILeaseGrant, error) {
	return PKILeaseGrant{
		PKIDomainID: "domain-1", PKIEpoch: 1, InstanceID: "instance-1",
		LeaseTerm: strings.Repeat("e", 64), LeaseDeadline: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}

type regressionPKIEndpointRepository struct {
	states      map[string]PKIEndpointCertificateState
	failures    []PKIEndpointRotationFailure
	activations int
}

func newRegressionPKIEndpointRepository(states ...PKIEndpointCertificateState) *regressionPKIEndpointRepository {
	repository := &regressionPKIEndpointRepository{states: make(map[string]PKIEndpointCertificateState, len(states))}
	for _, state := range states {
		repository.states[state.IdentityID] = state
	}
	return repository
}

func (r *regressionPKIEndpointRepository) LoadPKIEndpointCertificate(_ context.Context, identityID string) (PKIEndpointCertificateState, error) {
	state, found := r.states[identityID]
	if !found {
		return PKIEndpointCertificateState{}, errors.New("identity not found")
	}
	return state, nil
}

func (r *regressionPKIEndpointRepository) RecordPKIEndpointRotationFailure(_ context.Context, failure PKIEndpointRotationFailure) error {
	r.failures = append(r.failures, failure)
	return nil
}

func (r *regressionPKIEndpointRepository) ActivatePKIEndpointCandidate(_ context.Context, activation PKIEndpointActivation) error {
	r.activations++
	return nil
}
