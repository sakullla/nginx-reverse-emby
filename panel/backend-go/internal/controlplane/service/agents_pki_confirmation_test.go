package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestInternalPKIRotationConfirmationsAreBoundAndConsumed(t *testing.T) {
	root := t.TempDir()
	store := newPKIAuthorityRuntimeTestStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapPKIAuthorityRuntimeTest(t, store, vault)
	now := time.Now().UTC().Truncate(time.Second)
	clock := func() time.Time { return now }
	authority := newPKIAuthorityRuntimeForTest(t, store, vault, bootstrap, clock, nil)
	pki := &InternalPKIService{
		store: store, lease: bootstrap.lease, authority: authority,
		clock: clock, random: rand.Reader,
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "agent-a", Name: "agent A", AgentToken: "control-token"}); err != nil {
		t.Fatal(err)
	}
	grant, err := bootstrap.lease.RequirePKILease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.CreatePKIIdentity(t.Context(), storage.PKIIdentityRow{
			ID: "identity-a", PKIDomainID: grant.PKIDomainID, Kind: storage.PKIIdentityKindAgent,
			AgentID: "agent-a", State: storage.PKIIdentityStateEnrollmentRequired,
			CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := pki.ForceRotate(t.Context(), PKIActionRequest{TargetID: "identity-a", Reason: "missing approval"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ForceRotate(without confirmation) error = %v", err)
	}
	forceConfirmation, err := pki.IssueConfirmationNonce(t.Context(), PKIConfirmationRequest{
		Action: "force_rotate", TargetID: "identity-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if forceConfirmation.Action != "force_rotate" || forceConfirmation.TargetID != "identity-a" {
		t.Fatalf("force confirmation binding = %+v", forceConfirmation)
	}
	if _, err := pki.RotateCA(t.Context(), PKIActionRequest{
		Reason: "wrong action", ConfirmationNonce: forceConfirmation.Nonce,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("RotateCA(force-rotate confirmation) error = %v", err)
	}
	if _, err := pki.ForceRotate(t.Context(), PKIActionRequest{
		TargetID: "identity-b", Reason: "wrong target", ConfirmationNonce: forceConfirmation.Nonce,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ForceRotate(wrong-target confirmation) error = %v", err)
	}
	assertPKIConfirmationConsumed(t, store, forceConfirmation.Nonce, false)
	operation, err := pki.ForceRotate(t.Context(), PKIActionRequest{
		TargetID: "identity-a", Reason: "scheduled endpoint renewal", ConfirmationNonce: forceConfirmation.Nonce,
	})
	if err != nil {
		t.Fatalf("ForceRotate() error = %v", err)
	}
	if operation.Kind != "force_rotate" || operation.State != storage.PKILifecycleJobStateFailed {
		t.Fatalf("ForceRotate() operation = %+v", operation)
	}
	assertPKIConfirmationConsumed(t, store, forceConfirmation.Nonce, true)
	if _, err := pki.ForceRotate(t.Context(), PKIActionRequest{
		TargetID: "identity-a", Reason: "replay", ConfirmationNonce: forceConfirmation.Nonce,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ForceRotate(reused confirmation) error = %v", err)
	}

	if _, err := pki.IssueConfirmationNonce(t.Context(), PKIConfirmationRequest{Action: "ca_rotate", TargetID: "another-domain"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("IssueConfirmationNonce(ca wrong target) error = %v", err)
	}
	caConfirmation, err := pki.IssueConfirmationNonce(t.Context(), PKIConfirmationRequest{Action: "ca_rotate"})
	if err != nil {
		t.Fatal(err)
	}
	if caConfirmation.Action != "ca_rotate" || caConfirmation.TargetID != "domain" {
		t.Fatalf("CA confirmation binding = %+v", caConfirmation)
	}
	operation, err = pki.RotateCA(t.Context(), PKIActionRequest{
		Reason: "scheduled authority maintenance", ConfirmationNonce: caConfirmation.Nonce,
	})
	if err != nil {
		t.Fatalf("RotateCA() error = %v", err)
	}
	if operation.Kind != "ca_rotate" {
		t.Fatalf("RotateCA() operation = %+v", operation)
	}
	assertPKIConfirmationConsumed(t, store, caConfirmation.Nonce, true)
	if pkiLifecycleTerminal(operation.State) {
		t.Fatalf("RotateCA() did not leave an active operation for replay coverage: %+v", operation)
	}
	if _, err := pki.RotateCA(t.Context(), PKIActionRequest{
		Reason: "forged replay", ConfirmationNonce: strings.Repeat("0", 64),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("RotateCA(forged confirmation with active job) error = %v", err)
	}
	if _, err := pki.RotateCA(t.Context(), PKIActionRequest{
		Reason: "replay active job", ConfirmationNonce: caConfirmation.Nonce,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("RotateCA(reused confirmation with active job) error = %v", err)
	}
}

func assertPKIConfirmationConsumed(t *testing.T, store *storage.GormStore, nonce string, want bool) {
	t.Helper()
	nonceBytes, err := hex.DecodeString(nonce)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(nonceBytes)
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range state.ConfirmationNonces {
		if row.DigestSHA256 == hex.EncodeToString(digest[:]) {
			if (row.ConsumedAt != nil) != want {
				t.Fatalf("confirmation consumed = %t, want %t: %+v", row.ConsumedAt != nil, want, row)
			}
			return
		}
	}
	t.Fatal("confirmation nonce row not found")
}
