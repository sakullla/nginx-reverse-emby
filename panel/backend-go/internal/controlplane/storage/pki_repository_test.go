package storage

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestPKICanonicalRepositoryTransactionAndConstraints(t *testing.T) {
	store := newPKIFocusedTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	material := newPKITestMaterial(t, now, "domain-1", "ca-1", 1, "identity-1", "cert-1", "a0000000000000000000000000000001", PKICertificatePurposeClient)

	rollbackErr := errors.New("rollback")
	if err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
		if err := tx.CreatePKISettings(ctx, pkiTestSettings(now)); err != nil {
			return err
		}
		if err := tx.AppendPKIEvent(ctx, pkiTestEvent("event-rollback", now)); err != nil {
			return err
		}
		return rollbackErr
	}); !errors.Is(err, rollbackErr) {
		t.Fatalf("WithPKITransaction(rollback) error = %v", err)
	}
	assertPKIStateEmpty(t, store, ctx)

	if err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
		certificateID := material.certificate.ID
		rows := []func() error{
			func() error { return tx.CreatePKISettings(ctx, pkiTestSettings(now)) },
			func() error { return tx.CreatePKIAuthority(ctx, material.authority) },
			func() error {
				return tx.CreatePKIIdentity(ctx, PKIIdentityRow{
					ID: "identity-1", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent, AgentID: "agent-1",
					State: PKIIdentityStateActive, CurrentCertificateID: &certificateID, CreatedAt: now, UpdatedAt: now,
				})
			},
			func() error { return tx.CreatePKICertificate(ctx, material.certificate) },
			func() error {
				return tx.CreatePKIEnrollmentToken(ctx, PKIEnrollmentTokenRow{
					ID: "token-1", TokenDigestSHA256: strings.Repeat("b", 64), Scope: "new_agent",
					ExpiresAt: now.Add(time.Hour), CreatedBy: "admin", CreatedAt: now,
				})
			},
			func() error {
				return tx.CreatePKILifecycleJob(ctx, PKILifecycleJobRow{
					ID: "job-1", PKIDomainID: "domain-1", TargetType: "identity", TargetID: "identity-1",
					Kind: "renew", Phase: "queued", State: PKILifecycleJobStatePending, IdempotencyKey: "renew-identity-1", CreatedAt: now, UpdatedAt: now,
				})
			},
			func() error { return tx.AppendPKIEvent(ctx, pkiTestEvent("event-1", now)) },
			func() error {
				return tx.CreatePKIInstanceLease(ctx, PKIInstanceLeaseRow{
					PKIDomainID: "domain-1", InstanceID: "instance-1", LeaseTerm: "term-1", LeaseDeadline: now.Add(30 * time.Second), PKIEpoch: 1, State: "held", UpdatedAt: now,
				})
			},
		}
		for _, create := range rows {
			if err := create(); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("WithPKITransaction(create canonical state) error = %v", err)
	}
	state, err := store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatalf("LoadPKICanonicalState() error = %v", err)
	}
	if state.Settings == nil || state.InstanceLease == nil || len(state.Authorities) != 1 || len(state.Identities) != 1 || len(state.Certificates) != 1 || len(state.EnrollmentTokens) != 1 || len(state.LifecycleJobs) != 1 || len(state.Events) != 1 {
		t.Fatalf("canonical state counts are incomplete: %+v", state)
	}

	duplicate := material.issueCertificate(t, "cert-2", "b0000000000000000000000000000002", "identity-1", PKICertificatePurposeClient)
	if err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
		return tx.CreatePKICertificate(ctx, duplicate)
	}); err == nil {
		t.Fatal("second active identity/purpose certificate unexpectedly succeeded")
	}
	if err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
		row := material.issueCertificate(t, "cert-private", "c0000000000000000000000000000003", "identity-1", PKICertificatePurposeClient)
		row.Status = PKICertificateStatusPending
		row.CertificatePEM += "-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----\n"
		return tx.CreatePKICertificate(ctx, row)
	}); !errors.Is(err, ErrPKIInvariant) {
		t.Fatalf("certificate containing an extra private-key block error = %v, want ErrPKIInvariant", err)
	}
}

func TestPKICanonicalRelationshipsAcceptConfiguredEmbeddedAgentOwner(t *testing.T) {
	store := openPKIFocusedTestStore(t, t.TempDir(), false)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	err := store.WithPKITransaction(t.Context(), func(tx *PKITransaction) error {
		if err := tx.CreatePKISettings(t.Context(), pkiTestSettings(now)); err != nil {
			return err
		}
		if err := tx.CreatePKIIdentity(t.Context(), PKIIdentityRow{
			ID: "embedded-identity", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent,
			AgentID: store.LocalAgentID(), State: PKIIdentityStateEnrollmentRequired,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.SavePKISecurityAcknowledgement(t.Context(), store.LocalAgentID(), `{"pki_domain_id":"domain-1"}`, now)
	})
	if err != nil {
		t.Fatalf("create configured embedded identity and acknowledgement without remote AgentRow: %v", err)
	}
	localState, err := store.LoadLocalAgentState(t.Context())
	if err != nil {
		t.Fatalf("load embedded acknowledgement: %v", err)
	}
	if localState.PKISecurityAckJSON != `{"pki_domain_id":"domain-1"}` || localState.PKISecurityAckAt == nil || !localState.PKISecurityAckAt.Equal(now) {
		t.Fatalf("embedded acknowledgement was not persisted: %+v", localState)
	}

	err = store.WithPKITransaction(t.Context(), func(tx *PKITransaction) error {
		return tx.CreatePKIIdentity(t.Context(), PKIIdentityRow{
			ID: "missing-remote-identity", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent,
			AgentID: "missing-remote", State: PKIIdentityStateEnrollmentRequired,
			CreatedAt: now, UpdatedAt: now,
		})
	})
	if !errors.Is(err, ErrPKIInvariant) {
		t.Fatalf("create identity for missing remote agent error = %v, want ErrPKIInvariant", err)
	}
}

func TestPKICanonicalRelationshipsRecoverEmbeddedOwnerFromDurableAcknowledgement(t *testing.T) {
	store := openPKIFocusedTestStore(t, t.TempDir(), false)
	now := time.Date(2026, 8, 3, 13, 0, 0, 0, time.UTC)
	material := newPKITestMaterial(t, now, "domain-1", "ca-1", 1, "embedded-identity", "embedded-certificate", "a0000000000000000000000000000001", PKICertificatePurposeClient)
	material.agentID = store.LocalAgentID()
	material.certificate = material.issueCertificate(t, "embedded-certificate", "a0000000000000000000000000000001", "embedded-identity", PKICertificatePurposeClient)
	certificateID := material.certificate.ID

	err := store.WithPKITransaction(t.Context(), func(tx *PKITransaction) error {
		if err := tx.CreatePKISettings(t.Context(), pkiTestSettings(now)); err != nil {
			return err
		}
		if err := tx.CreatePKIAuthority(t.Context(), material.authority); err != nil {
			return err
		}
		if err := tx.CreatePKIIdentity(t.Context(), PKIIdentityRow{
			ID: "embedded-identity", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent,
			AgentID: store.LocalAgentID(), State: PKIIdentityStateActive,
			CurrentCertificateID: &certificateID, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.CreatePKICertificate(t.Context(), material.certificate); err != nil {
			return err
		}
		acknowledgement, err := json.Marshal(PKISecurityAcknowledgement{
			PKIDomainID: "domain-1", PKIEpoch: 1, Full: true, CertificateID: certificateID,
		})
		if err != nil {
			return err
		}
		return tx.SavePKISecurityAcknowledgement(t.Context(), store.LocalAgentID(), string(acknowledgement), now)
	})
	if err != nil {
		t.Fatalf("create embedded PKI graph: %v", err)
	}
	if err := validatePKICanonicalRelationships(t.Context(), store.db, "pki-backup-stage"); err != nil {
		t.Fatalf("isolated backup validation rejected durable embedded owner: %v", err)
	}
	if err := store.db.WithContext(t.Context()).Model(&LocalAgentStateRow{}).Where("id = ?", 1).
		Updates(map[string]any{"pki_security_ack": "", "pki_security_ack_at": nil}).Error; err != nil {
		t.Fatalf("clear embedded acknowledgement: %v", err)
	}
	if err := validatePKICanonicalRelationships(t.Context(), store.db, "pki-backup-stage"); !errors.Is(err, ErrPKIInvariant) {
		t.Fatalf("isolated validation without durable owner error = %v, want ErrPKIInvariant", err)
	}
}

func TestPKIConfirmationNonceConsumptionUsesDatabaseTime(t *testing.T) {
	store := newPKIFocusedTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	futureDigest := strings.Repeat("c", 64)
	expiredDigest := strings.Repeat("d", 64)
	if err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
		if err := tx.CreatePKISettings(ctx, pkiTestSettings(now)); err != nil {
			return err
		}
		if err := tx.CreatePKIConfirmationNonce(ctx, PKIConfirmationNonceRow{
			ID: "nonce-future", PKIDomainID: "domain-1", DigestSHA256: futureDigest,
			OperatorID: "panel", Action: "activate", ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
		}); err != nil {
			return err
		}
		return tx.CreatePKIConfirmationNonce(ctx, PKIConfirmationNonceRow{
			ID: "nonce-expired", PKIDomainID: "domain-1", DigestSHA256: expiredDigest,
			OperatorID: "panel", Action: "activate", ExpiresAt: now.Add(-10 * time.Minute), CreatedAt: now.Add(-20 * time.Minute),
		})
	}); err != nil {
		t.Fatalf("create confirmation nonces: %v", err)
	}

	if err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
		consumed, err := tx.ConsumePKIConfirmationNonce(ctx, "domain-1", futureDigest, "panel", "activate", "", now.Add(24*time.Hour))
		if err != nil {
			return err
		}
		if !consumed {
			t.Fatal("unexpired nonce was rejected using the caller's future clock")
		}
		return nil
	}); err != nil {
		t.Fatalf("consume unexpired confirmation nonce: %v", err)
	}
	if err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
		consumed, err := tx.ConsumePKIConfirmationNonce(ctx, "domain-1", expiredDigest, "panel", "activate", "", now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if consumed {
			t.Fatal("expired nonce was accepted using the caller's past clock")
		}
		return nil
	}); err != nil {
		t.Fatalf("reject expired confirmation nonce: %v", err)
	}

	state, err := store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatalf("load canonical state: %v", err)
	}
	consumedByID := make(map[string]bool, len(state.ConfirmationNonces))
	for _, row := range state.ConfirmationNonces {
		consumedByID[row.ID] = row.ConsumedAt != nil
	}
	if !consumedByID["nonce-future"] || consumedByID["nonce-expired"] {
		t.Fatalf("confirmation nonce consumption state = %+v", consumedByID)
	}
}

func TestPKIRepositoryRejectsBrokenRelationshipsAndRollsBack(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*testing.T, context.Context, *PKITransaction, pkiTestMaterial) error
	}{
		{
			name: "missing current certificate",
			mutate: func(t *testing.T, ctx context.Context, tx *PKITransaction, material pkiTestMaterial) error {
				missing := "missing-cert"
				return tx.CreatePKIIdentity(ctx, PKIIdentityRow{
					ID: "identity-1", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent, AgentID: "agent-1",
					State: PKIIdentityStateActive, CurrentCertificateID: &missing, CreatedAt: now, UpdatedAt: now,
				})
			},
		},
		{
			name: "missing identity",
			mutate: func(t *testing.T, ctx context.Context, tx *PKITransaction, material pkiTestMaterial) error {
				if err := tx.CreatePKIAuthority(ctx, material.authority); err != nil {
					return err
				}
				certificate := material.certificate
				certificate.IdentityID = "missing-identity"
				certificate.Status = PKICertificateStatusPending
				return tx.CreatePKICertificate(ctx, certificate)
			},
		},
		{
			name: "missing authority",
			mutate: func(t *testing.T, ctx context.Context, tx *PKITransaction, material pkiTestMaterial) error {
				if err := createPKITestPendingIdentity(ctx, tx, now); err != nil {
					return err
				}
				certificate := material.certificate
				certificate.AuthorityID = "missing-authority"
				certificate.Status = PKICertificateStatusPending
				return tx.CreatePKICertificate(ctx, certificate)
			},
		},
		{
			name: "cross domain",
			mutate: func(t *testing.T, ctx context.Context, tx *PKITransaction, material pkiTestMaterial) error {
				material.authority.PKIDomainID = "domain-2"
				if err := tx.CreatePKIAuthority(ctx, material.authority); err != nil {
					return err
				}
				if err := createPKITestPendingIdentity(ctx, tx, now); err != nil {
					return err
				}
				certificate := material.certificate
				certificate.Status = PKICertificateStatusPending
				return tx.CreatePKICertificate(ctx, certificate)
			},
		},
		{
			name: "wrong CA generation",
			mutate: func(t *testing.T, ctx context.Context, tx *PKITransaction, material pkiTestMaterial) error {
				if err := tx.CreatePKIAuthority(ctx, material.authority); err != nil {
					return err
				}
				if err := createPKITestPendingIdentity(ctx, tx, now); err != nil {
					return err
				}
				certificate := material.certificate
				certificate.CAGeneration = 2
				certificate.Status = PKICertificateStatusPending
				return tx.CreatePKICertificate(ctx, certificate)
			},
		},
		{
			name: "wrong CA signer",
			mutate: func(t *testing.T, ctx context.Context, tx *PKITransaction, material pkiTestMaterial) error {
				other := newPKITestMaterial(t, now, "domain-1", "ca-other", 1, "identity-1", "cert-other", "e0000000000000000000000000000005", PKICertificatePurposeClient)
				if err := tx.CreatePKIAuthority(ctx, material.authority); err != nil {
					return err
				}
				if err := createPKITestPendingIdentity(ctx, tx, now); err != nil {
					return err
				}
				certificate := other.certificate
				certificate.AuthorityID = material.authority.ID
				certificate.CAGeneration = material.authority.Generation
				certificate.Status = PKICertificateStatusPending
				return tx.CreatePKICertificate(ctx, certificate)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newPKIFocusedTestStore(t)
			ctx := context.Background()
			material := newPKITestMaterial(t, now, "domain-1", "ca-1", 1, "identity-1", "cert-1", "a0000000000000000000000000000001", PKICertificatePurposeClient)
			err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
				if err := tx.CreatePKISettings(ctx, pkiTestSettings(now)); err != nil {
					return err
				}
				return test.mutate(t, ctx, tx, material)
			})
			if !errors.Is(err, ErrPKIInvariant) {
				t.Fatalf("WithPKITransaction() error = %v, want ErrPKIInvariant", err)
			}
			assertPKIStateEmpty(t, store, ctx)
		})
	}
}

func TestPKICertificateParsingRejectsMalformedAndMismatchedMetadata(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*PKIAuthorityRow, *PKICertificateRow)
		create func(context.Context, *PKITransaction, PKIAuthorityRow, PKICertificateRow) error
	}{
		{"malformed authority", func(authority *PKIAuthorityRow, _ *PKICertificateRow) {
			authority.CertificatePEM = "-----BEGIN CERTIFICATE-----\nnot-der\n-----END CERTIFICATE-----"
		}, func(ctx context.Context, tx *PKITransaction, authority PKIAuthorityRow, _ PKICertificateRow) error {
			return tx.CreatePKIAuthority(ctx, authority)
		}},
		{"authority garbage suffix", func(authority *PKIAuthorityRow, _ *PKICertificateRow) { authority.CertificatePEM += "garbage" }, func(ctx context.Context, tx *PKITransaction, authority PKIAuthorityRow, _ PKICertificateRow) error {
			return tx.CreatePKIAuthority(ctx, authority)
		}},
		{"authority fingerprint mismatch", func(authority *PKIAuthorityRow, _ *PKICertificateRow) {
			authority.FingerprintSHA256 = strings.Repeat("f", 64)
		}, func(ctx context.Context, tx *PKITransaction, authority PKIAuthorityRow, _ PKICertificateRow) error {
			return tx.CreatePKIAuthority(ctx, authority)
		}},
		{"certificate serial mismatch", func(_ *PKIAuthorityRow, certificate *PKICertificateRow) {
			certificate.SerialHex = "f000000000000000000000000000000f"
		}, func(ctx context.Context, tx *PKITransaction, _ PKIAuthorityRow, certificate PKICertificateRow) error {
			return tx.CreatePKICertificate(ctx, certificate)
		}},
		{"certificate fingerprint mismatch", func(_ *PKIAuthorityRow, certificate *PKICertificateRow) {
			certificate.PublicKeyFingerprint = strings.Repeat("f", 64)
		}, func(ctx context.Context, tx *PKITransaction, _ PKIAuthorityRow, certificate PKICertificateRow) error {
			return tx.CreatePKICertificate(ctx, certificate)
		}},
		{"certificate extra block", func(_ *PKIAuthorityRow, certificate *PKICertificateRow) {
			certificate.CertificatePEM += certificate.CertificatePEM
		}, func(ctx context.Context, tx *PKITransaction, _ PKIAuthorityRow, certificate PKICertificateRow) error {
			return tx.CreatePKICertificate(ctx, certificate)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newPKIFocusedTestStore(t)
			ctx := context.Background()
			material := newPKITestMaterial(t, now, "domain-1", "ca-1", 1, "identity-1", "cert-1", "a0000000000000000000000000000001", PKICertificatePurposeClient)
			authority, certificate := material.authority, material.certificate
			test.mutate(&authority, &certificate)
			err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
				if err := tx.CreatePKISettings(ctx, pkiTestSettings(now)); err != nil {
					return err
				}
				return test.create(ctx, tx, authority, certificate)
			})
			if !errors.Is(err, ErrPKIInvariant) {
				t.Fatalf("create malformed certificate error = %v, want ErrPKIInvariant", err)
			}
			assertPKIStateEmpty(t, store, ctx)
		})
	}
}

func TestPKIRepositoryRejectsIssuerAndPurposeEscalation(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*x509.Certificate, *x509.Certificate)
	}{
		{
			name: "wrong issuer DN",
			mutate: func(_ *x509.Certificate, parent *x509.Certificate) {
				parent.Subject = pkix.Name{CommonName: "wrong-issuer"}
				parent.RawSubject = nil
			},
		},
		{
			name: "wrong authority key identifier",
			mutate: func(_ *x509.Certificate, parent *x509.Certificate) {
				parent.SubjectKeyId = bytes.Repeat([]byte{0xff}, 20)
			},
		},
		{
			name: "dual client and server EKU",
			mutate: func(template *x509.Certificate, _ *x509.Certificate) {
				template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
			},
		},
		{
			name: "opposite server EKU",
			mutate: func(template *x509.Certificate, _ *x509.Certificate) {
				template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			},
		},
		{
			name: "any EKU",
			mutate: func(template *x509.Certificate, _ *x509.Certificate) {
				template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageAny}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newPKIFocusedTestStore(t)
			ctx := context.Background()
			material := newPKITestMaterial(t, now, "domain-1", "ca-1", 1, "identity-1", "cert-1", "a0000000000000000000000000000001", PKICertificatePurposeClient)
			certificate := material.issueCertificateWithOptions(t, "cert-invalid", "b0000000000000000000000000000002", "identity-1", PKICertificatePurposeClient, now, test.mutate)
			certificate.Status = PKICertificateStatusPending
			err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
				if err := tx.CreatePKISettings(ctx, pkiTestSettings(now)); err != nil {
					return err
				}
				if err := tx.CreatePKIAuthority(ctx, material.authority); err != nil {
					return err
				}
				if err := createPKITestPendingIdentity(ctx, tx, now); err != nil {
					return err
				}
				return tx.CreatePKICertificate(ctx, certificate)
			})
			if !errors.Is(err, ErrPKIInvariant) {
				t.Fatalf("WithPKITransaction() error = %v, want ErrPKIInvariant", err)
			}
			assertPKIStateEmpty(t, store, ctx)
		})
	}
}

func TestPKIRepositoryBindsLeafOwnersAndListenerSANsToCanonicalRows(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	t.Run("agent owner URI", func(t *testing.T) {
		store := newPKIFocusedTestStore(t)
		material := newPKITestMaterial(t, now, "domain-1", "ca-1", 1, "identity-1", "cert-base", "a0000000000000000000000000000001", PKICertificatePurposeClient)
		certificate := material.issueCertificateWithOptions(
			t, "cert-wrong-owner", "b0000000000000000000000000000002", "identity-1", PKICertificatePurposeClient, now,
			func(template *x509.Certificate, _ *x509.Certificate) {
				wrong, err := url.Parse("spiffe://domain-1/agent/another-agent")
				if err != nil {
					t.Fatal(err)
				}
				template.Subject = pkix.Name{CommonName: wrong.String()}
				template.URIs = []*url.URL{wrong}
			},
		)
		currentID := certificate.ID
		err := store.WithPKITransaction(t.Context(), func(tx *PKITransaction) error {
			if err := tx.CreatePKISettings(t.Context(), pkiTestSettings(now)); err != nil {
				return err
			}
			if err := tx.CreatePKIAuthority(t.Context(), material.authority); err != nil {
				return err
			}
			if err := tx.CreatePKIIdentity(t.Context(), pkiTestIdentity(now, PKIIdentityStateActive, &currentID)); err != nil {
				return err
			}
			return tx.CreatePKICertificate(t.Context(), certificate)
		})
		if !errors.Is(err, ErrPKIInvariant) {
			t.Fatalf("wrong agent owner URI error = %v, want ErrPKIInvariant", err)
		}
	})

	t.Run("agent certificate with listener SAN", func(t *testing.T) {
		store := newPKIFocusedTestStore(t)
		material := newPKITestMaterial(t, now, "domain-1", "ca-1", 1, "identity-1", "cert-base", "a0000000000000000000000000000001", PKICertificatePurposeClient)
		certificate := material.issueCertificateWithOptions(
			t, "cert-agent-san", "b0000000000000000000000000000002", "identity-1", PKICertificatePurposeClient, now,
			func(template *x509.Certificate, _ *x509.Certificate) {
				template.DNSNames = []string{"relay.example.test"}
			},
		)
		currentID := certificate.ID
		err := store.WithPKITransaction(t.Context(), func(tx *PKITransaction) error {
			if err := tx.CreatePKISettings(t.Context(), pkiTestSettings(now)); err != nil {
				return err
			}
			if err := tx.CreatePKIAuthority(t.Context(), material.authority); err != nil {
				return err
			}
			if err := tx.CreatePKIIdentity(t.Context(), pkiTestIdentity(now, PKIIdentityStateActive, &currentID)); err != nil {
				return err
			}
			return tx.CreatePKICertificate(t.Context(), certificate)
		})
		if !errors.Is(err, ErrPKIInvariant) {
			t.Fatalf("agent listener SAN error = %v, want ErrPKIInvariant", err)
		}
	})

	t.Run("listener SAN differs from canonical endpoint", func(t *testing.T) {
		store := newPKIFocusedTestStore(t)
		if err := store.SaveRelayListeners(t.Context(), "agent-1", []RelayListenerRow{{
			ID: 42, AgentID: "agent-1", Name: "relay", ListenHost: "0.0.0.0", ListenPort: 7443,
			PublicHost: "relay.example.test", PublicPort: 7443, Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
		}}); err != nil {
			t.Fatal(err)
		}
		material := newPKITestMaterial(t, now, "domain-1", "ca-1", 1, "listener-identity", "cert-base", "a0000000000000000000000000000001", PKICertificatePurposeServer)
		certificate := material.issueCertificateWithOptions(
			t, "cert-listener-san", "b0000000000000000000000000000002", "listener-identity", PKICertificatePurposeServer, now,
			func(template *x509.Certificate, _ *x509.Certificate) {
				owner, err := url.Parse("spiffe://domain-1/agent/agent-1/listener/42")
				if err != nil {
					t.Fatal(err)
				}
				template.Subject = pkix.Name{CommonName: owner.String()}
				template.URIs = []*url.URL{owner}
				template.DNSNames = []string{"different.example.test"}
			},
		)
		currentID := certificate.ID
		err := store.WithPKITransaction(t.Context(), func(tx *PKITransaction) error {
			if err := tx.CreatePKISettings(t.Context(), pkiTestSettings(now)); err != nil {
				return err
			}
			if err := tx.CreatePKIAuthority(t.Context(), material.authority); err != nil {
				return err
			}
			if err := tx.CreatePKIIdentity(t.Context(), PKIIdentityRow{
				ID: "listener-identity", PKIDomainID: "domain-1", Kind: PKIIdentityKindListener, AgentID: "agent-1", ListenerID: "42",
				State: PKIIdentityStateActive, CurrentCertificateID: &currentID, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
			return tx.CreatePKICertificate(t.Context(), certificate)
		})
		if !errors.Is(err, ErrPKIInvariant) {
			t.Fatalf("listener SAN mismatch error = %v, want ErrPKIInvariant", err)
		}
	})
}

func TestPKISupersessionGraphRejectsInvalidLineageAndRollsBack(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		wantError string
		build     func(*testing.T, pkiTestMaterial) (PKIIdentityRow, []PKICertificateRow)
	}{
		{
			name:      "superseded without replacement",
			wantError: "has no superseding certificate",
			build: func(_ *testing.T, material pkiTestMaterial) (PKIIdentityRow, []PKICertificateRow) {
				certificate := material.certificate
				certificate.Status = PKICertificateStatusSuperseded
				return pkiTestIdentity(now, PKIIdentityStateEnrollmentRequired, nil), []PKICertificateRow{certificate}
			},
		},
		{
			name:      "self reference",
			wantError: "supersedes itself",
			build: func(_ *testing.T, material pkiTestMaterial) (PKIIdentityRow, []PKICertificateRow) {
				certificate := material.certificate
				certificate.Status = PKICertificateStatusSuperseded
				replacementID := certificate.ID
				certificate.SupersededByID = &replacementID
				return pkiTestIdentity(now, PKIIdentityStateEnrollmentRequired, nil), []PKICertificateRow{certificate}
			},
		},
		{
			name:      "multi-certificate cycle",
			wantError: "contains a cycle",
			build: func(t *testing.T, material pkiTestMaterial) (PKIIdentityRow, []PKICertificateRow) {
				first := material.certificate
				first.Status = PKICertificateStatusSuperseded
				second := material.issueCertificateWithOptions(t, "cert-2", "b0000000000000000000000000000002", "identity-1", PKICertificatePurposeClient, now.Add(time.Minute), nil)
				second.Status = PKICertificateStatusSuperseded
				firstReplacement, secondReplacement := second.ID, first.ID
				first.SupersededByID = &firstReplacement
				second.SupersededByID = &secondReplacement
				return pkiTestIdentity(now, PKIIdentityStateEnrollmentRequired, nil), []PKICertificateRow{first, second}
			},
		},
		{
			name:      "active certificate reference",
			wantError: "while not superseded",
			build: func(t *testing.T, material pkiTestMaterial) (PKIIdentityRow, []PKICertificateRow) {
				active := material.certificate
				replacement := material.issueCertificateWithOptions(t, "cert-2", "b0000000000000000000000000000002", "identity-1", PKICertificatePurposeClient, now.Add(time.Minute), nil)
				replacement.Status = PKICertificateStatusPending
				replacementID := replacement.ID
				active.SupersededByID = &replacementID
				currentID := active.ID
				return pkiTestIdentity(now, PKIIdentityStateActive, &currentID), []PKICertificateRow{active, replacement}
			},
		},
		{
			name:      "replacement not created later",
			wantError: "was not created later",
			build: func(t *testing.T, material pkiTestMaterial) (PKIIdentityRow, []PKICertificateRow) {
				first := material.certificate
				first.Status = PKICertificateStatusSuperseded
				second := material.issueCertificateWithOptions(t, "cert-2", "b0000000000000000000000000000002", "identity-1", PKICertificatePurposeClient, now, nil)
				second.Status = PKICertificateStatusPending
				replacementID := second.ID
				first.SupersededByID = &replacementID
				return pkiTestIdentity(now, PKIIdentityStateEnrollmentRequired, nil), []PKICertificateRow{first, second}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newPKIFocusedTestStore(t)
			ctx := context.Background()
			material := newPKITestMaterial(t, now, "domain-1", "ca-1", 1, "identity-1", "cert-1", "a0000000000000000000000000000001", PKICertificatePurposeClient)
			identity, certificates := test.build(t, material)
			err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
				if err := tx.CreatePKISettings(ctx, pkiTestSettings(now)); err != nil {
					return err
				}
				if err := tx.CreatePKIAuthority(ctx, material.authority); err != nil {
					return err
				}
				if err := tx.CreatePKIIdentity(ctx, identity); err != nil {
					return err
				}
				for _, certificate := range certificates {
					if err := tx.CreatePKICertificate(ctx, certificate); err != nil {
						return err
					}
				}
				return nil
			})
			if !errors.Is(err, ErrPKIInvariant) || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("WithPKITransaction() error = %v, want ErrPKIInvariant containing %q", err, test.wantError)
			}
			assertPKIStateEmpty(t, store, ctx)
		})
	}
}

func TestPKICanonicalStateUsesOneReadSnapshot(t *testing.T) {
	root := t.TempDir()
	store := openPKIFocusedTestStore(t, root, false)
	writer := openPKIFocusedTestStore(t, root, true)
	ctx := context.Background()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	if err := store.WithPKITransaction(ctx, func(tx *PKITransaction) error {
		if err := tx.CreatePKISettings(ctx, pkiTestSettings(now)); err != nil {
			return err
		}
		return tx.AppendPKIEvent(ctx, pkiTestEvent("event-1", now))
	}); err != nil {
		t.Fatalf("initialize PKI state: %v", err)
	}

	settingsRead := make(chan struct{})
	continueRead := make(chan struct{})
	var pause sync.Once
	const callbackName = "test:pki_snapshot_pause"
	if err := store.db.Callback().Query().After("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement.Table == (PKISettingsRow{}).TableName() {
			pause.Do(func() {
				close(settingsRead)
				<-continueRead
			})
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callbackName) })

	type loadResult struct {
		state PKICanonicalState
		err   error
	}
	loaded := make(chan loadResult, 1)
	go func() {
		state, err := store.LoadPKICanonicalState(ctx)
		loaded <- loadResult{state: state, err: err}
	}()
	select {
	case <-settingsRead:
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatal("timed out waiting for the snapshot settings query")
	}
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- writer.WithPKITransaction(ctx, func(tx *PKITransaction) error {
			return tx.AppendPKIEvent(ctx, pkiTestEvent("event-2", now.Add(time.Second)))
		})
	}()
	select {
	case err := <-writerDone:
		if err != nil {
			close(continueRead)
			t.Fatalf("commit concurrent PKI event: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatal("timed out committing the concurrent PKI event")
	}
	close(continueRead)
	var result loadResult
	select {
	case result = <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out completing the PKI snapshot read")
	}
	if result.err != nil {
		t.Fatalf("LoadPKICanonicalState() error = %v", result.err)
	}
	if len(result.state.Events) != 1 || result.state.Events[0].ID != "event-1" {
		t.Fatalf("snapshot mixed concurrent commit: %+v", result.state.Events)
	}
	current, err := store.LoadPKICanonicalState(ctx)
	if err != nil || len(current.Events) != 2 {
		t.Fatalf("current state events = %+v, error = %v", current.Events, err)
	}
}

func TestValidateCanonicalPKISecuritySnapshotRejectsStaleForgedAndIncompleteState(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	settings := pkiTestSettings(now)
	material := newPKITestMaterial(t, now, settings.PKIDomainID, "ca-1", 1, "identity-1", "cert-1", "a0000000000000000000000000000001", PKICertificatePurposeClient)
	currentCertificateID := material.certificate.ID
	snapshotRow := pkiTestSignedSecuritySnapshot(t, settings, material, now)
	baseState := func() PKICanonicalState {
		settingsCopy := settings
		snapshotCopy := snapshotRow
		return PKICanonicalState{
			Settings:    &settingsCopy,
			Authorities: []PKIAuthorityRow{material.authority},
			Identities: []PKIIdentityRow{{
				ID: "identity-1", PKIDomainID: settings.PKIDomainID, Kind: PKIIdentityKindAgent, AgentID: "agent-1",
				State: PKIIdentityStateActive, CurrentCertificateID: &currentCertificateID, CreatedAt: now, UpdatedAt: now,
			}},
			Certificates:     []PKICertificateRow{material.certificate},
			SecuritySnapshot: &snapshotCopy,
		}
	}
	if snapshot, err := ValidateCanonicalPKISecuritySnapshot(baseState()); err != nil || snapshot.PKIDomainID != settings.PKIDomainID {
		t.Fatalf("valid signed snapshot = %+v, error = %v", snapshot, err)
	}

	tests := []struct {
		name   string
		mutate func(*PKICanonicalState)
	}{
		{
			name: "stale row revision",
			mutate: func(state *PKICanonicalState) {
				state.SecuritySnapshot.SecurityRevision++
			},
		},
		{
			name: "forged signature",
			mutate: func(state *PKICanonicalState) {
				var snapshot PKISecuritySnapshot
				if err := json.Unmarshal([]byte(state.SecuritySnapshot.SnapshotJSON), &snapshot); err != nil {
					t.Fatal(err)
				}
				snapshot.Signature[0] ^= 0xff
				encoded, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				state.SecuritySnapshot.SnapshotJSON = string(encoded)
			},
		},
		{
			name: "unknown signed field",
			mutate: func(state *PKICanonicalState) {
				state.SecuritySnapshot.SnapshotJSON = strings.TrimSuffix(state.SecuritySnapshot.SnapshotJSON, "}") + `,"unexpected":true}`
			},
		},
		{
			name: "incomplete trust set",
			mutate: func(state *PKICanonicalState) {
				state.Authorities = append(state.Authorities, PKIAuthorityRow{
					ID: "ca-2", PKIDomainID: settings.PKIDomainID, Generation: 2, Status: "prepared",
				})
			},
		},
		{
			name: "stale revocation set",
			mutate: func(state *PKICanonicalState) {
				state.Certificates[0].Status = PKICertificateStatusRevoked
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := baseState()
			test.mutate(&state)
			if _, err := ValidateCanonicalPKISecuritySnapshot(state); !errors.Is(err, ErrPKIInvariant) {
				t.Fatalf("ValidateCanonicalPKISecuritySnapshot() error = %v, want ErrPKIInvariant", err)
			}
		})
	}
}

func TestPKILegacyMigrationSourcesStaySeparate(t *testing.T) {
	store := newPKIFocusedTestStore(t)
	ctx := context.Background()
	rows := []ManagedCertificateRow{
		{ID: 1, Domain: "public.example", CertificateType: "acme", Usage: "https"},
		{ID: 2, Domain: "__relay-ca.internal", CertificateType: "internal_ca", Usage: "relay_ca"},
	}
	if err := store.db.WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatalf("create legacy certificates: %v", err)
	}
	certificateID := 2
	if err := store.db.WithContext(ctx).Create(&RelayListenerRow{ID: 7, AgentID: "edge-1", Name: "relay", CertificateID: &certificateID}).Error; err != nil {
		t.Fatalf("create legacy relay listener: %v", err)
	}
	sources, err := store.InspectLegacyPKIMigrationSources(ctx)
	if err != nil {
		t.Fatalf("InspectLegacyPKIMigrationSources() error = %v", err)
	}
	if len(sources.ManagedCertificates) != 1 || sources.ManagedCertificates[0].ID != 2 || len(sources.RelayListeners) != 1 {
		t.Fatalf("migration sources = %+v", sources)
	}
	var managedCount int64
	if err := store.db.WithContext(ctx).Model(&ManagedCertificateRow{}).Count(&managedCount).Error; err != nil {
		t.Fatalf("count managed certificates: %v", err)
	}
	if managedCount != 2 {
		t.Fatalf("managed certificate rows changed during inspection: %d", managedCount)
	}
	if store.db.Migrator().HasColumn(&ManagedCertificateRow{}, "pki_domain_id") || store.db.Migrator().HasColumn(&ManagedCertificateRow{}, "encrypted_key_ref") {
		t.Fatal("public managed certificate schema was polluted with internal PKI columns")
	}
}

type pkiTestMaterial struct {
	authority            PKIAuthorityRow
	authorityCertificate *x509.Certificate
	authorityKey         *ecdsa.PrivateKey
	certificate          PKICertificateRow
	now                  time.Time
	domainID             string
	agentID              string
}

func newPKITestMaterial(t *testing.T, now time.Time, domainID, authorityID string, generation int64, identityID, certificateID, serialHex, purpose string) pkiTestMaterial {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caSerial := mustPKITestSerial(t, "d0000000000000000000000000000001")
	caSubjectKeyID := sha256.Sum256([]byte("pki-test-authority:" + authorityID))
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial, Subject: pkix.Name{CommonName: authorityID},
		NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour),
		BasicConstraintsValid: true, IsCA: true,
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		SubjectKeyId:       caSubjectKeyID[:20],
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	caFingerprint := sha256.Sum256(caDER)
	keyRef := "ca-" + authorityID + ".vault"
	material := pkiTestMaterial{
		authority: PKIAuthorityRow{
			ID: authorityID, PKIDomainID: domainID, Generation: generation, Status: "active",
			CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})), EncryptedKeyRef: &keyRef,
			FingerprintSHA256: hex.EncodeToString(caFingerprint[:]), NotBefore: caCertificate.NotBefore, NotAfter: caCertificate.NotAfter,
			CreatedAt: now, UpdatedAt: now,
		},
		authorityCertificate: caCertificate,
		authorityKey:         key,
		now:                  now,
		domainID:             domainID,
		agentID:              "agent-1",
	}
	material.certificate = material.issueCertificate(t, certificateID, serialHex, identityID, purpose)
	return material
}

func (material pkiTestMaterial) issueCertificate(t *testing.T, certificateID, serialHex, identityID, purpose string) PKICertificateRow {
	t.Helper()
	return material.issueCertificateWithOptions(t, certificateID, serialHex, identityID, purpose, material.now, nil)
}

func pkiTestSignedSecuritySnapshot(t *testing.T, settings PKISettingsRow, material pkiTestMaterial, issuedAt time.Time) PKISecuritySnapshotRow {
	t.Helper()
	trustGenerations := []int64{material.authority.Generation}
	trustDescriptors := []canonicalPKISnapshotTrustRoot{{
		AuthorityID:       material.authority.ID,
		Generation:        material.authority.Generation,
		Status:            material.authority.Status,
		FingerprintSHA256: strings.ToLower(material.authority.FingerprintSHA256),
		NotBefore:         material.authority.NotBefore.UTC(),
		NotAfter:          material.authority.NotAfter.UTC(),
	}}
	payload, err := json.Marshal(canonicalPKISnapshotPayload{
		PKIDomainID: settings.PKIDomainID,
		Version: canonicalPKISnapshotVersion{
			Version: canonicalPKISecurityVersion{PKIEpoch: settings.PKIEpoch, SecurityRevision: settings.SecurityRevision},
			Full:    true,
		},
		IssuedAt: issuedAt.UTC(), TrustGenerations: trustGenerations, TrustRoots: trustDescriptors,
		RevokedIdentityIDs: []string{}, RevokedSerials: []string{},
	})
	if err != nil {
		t.Fatalf("marshal signed PKI snapshot payload: %v", err)
	}
	digest := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, material.authorityKey, digest[:])
	if err != nil {
		t.Fatalf("sign PKI snapshot payload: %v", err)
	}
	snapshotJSON, err := json.Marshal(PKISecuritySnapshot{
		PKIDomainID: settings.PKIDomainID, PKIEpoch: settings.PKIEpoch, SecurityRevision: settings.SecurityRevision,
		Full: true, IssuedAt: issuedAt.UTC(),
		TrustRoots: []PKITrustRoot{{
			AuthorityID: material.authority.ID, Generation: material.authority.Generation, Status: material.authority.Status,
			CertificatePEM: material.authority.CertificatePEM, FingerprintSHA256: material.authority.FingerprintSHA256,
			NotBefore: material.authority.NotBefore, NotAfter: material.authority.NotAfter,
		}},
		RevokedIdentityIDs: []string{}, RevokedSerials: []string{},
		SignerGeneration: material.authority.Generation, Signature: signature,
	})
	if err != nil {
		t.Fatalf("marshal signed PKI snapshot: %v", err)
	}
	return PKISecuritySnapshotRow{
		PKIDomainID: settings.PKIDomainID, PKIEpoch: settings.PKIEpoch, SecurityRevision: settings.SecurityRevision,
		SnapshotJSON: string(snapshotJSON), UpdatedAt: issuedAt,
	}
}

func (material pkiTestMaterial) issueCertificateWithOptions(t *testing.T, certificateID, serialHex, identityID, purpose string, issuedAt time.Time, mutate func(*x509.Certificate, *x509.Certificate)) PKICertificateRow {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate endpoint key: %v", err)
	}
	usage := x509.ExtKeyUsageClientAuth
	if purpose == PKICertificatePurposeServer {
		usage = x509.ExtKeyUsageServerAuth
	}
	identityURI := (&url.URL{Scheme: "spiffe", Host: material.domainID, Path: "/agent/" + material.agentID}).String()
	template := &x509.Certificate{
		SerialNumber: mustPKITestSerial(t, serialHex), Subject: pkix.Name{CommonName: identityURI},
		NotBefore: issuedAt, NotAfter: issuedAt.Add(90 * 24 * time.Hour),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{usage}, SignatureAlgorithm: x509.ECDSAWithSHA256,
		URIs: []*url.URL{{Scheme: "spiffe", Host: material.domainID, Path: "/agent/" + material.agentID}},
	}
	parent := *material.authorityCertificate
	if mutate != nil {
		mutate(template, &parent)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, &parent, &key.PublicKey, material.authorityKey)
	if err != nil {
		t.Fatalf("create endpoint certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse endpoint certificate: %v", err)
	}
	publicKeyFingerprint := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
	return PKICertificateRow{
		ID: certificateID, SerialHex: serialHex, IdentityID: identityID, Purpose: purpose,
		AuthorityID: material.authority.ID, CAGeneration: material.authority.Generation,
		CertificatePEM:       string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		PublicKeyFingerprint: hex.EncodeToString(publicKeyFingerprint[:]), NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
		Status: PKICertificateStatusActive, CreatedAt: issuedAt, UpdatedAt: issuedAt,
	}
}

func pkiTestIdentity(now time.Time, state string, currentCertificateID *string) PKIIdentityRow {
	return PKIIdentityRow{
		ID: "identity-1", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent, AgentID: "agent-1",
		State: state, CurrentCertificateID: currentCertificateID, CreatedAt: now, UpdatedAt: now,
	}
}

func mustPKITestSerial(t *testing.T, value string) *big.Int {
	t.Helper()
	serial, ok := new(big.Int).SetString(value, 16)
	if !ok || serial.BitLen() < 128 {
		t.Fatalf("invalid PKI test serial %q", value)
	}
	return serial
}

func createPKITestPendingIdentity(ctx context.Context, tx *PKITransaction, now time.Time) error {
	return tx.CreatePKIIdentity(ctx, PKIIdentityRow{
		ID: "identity-1", PKIDomainID: "domain-1", Kind: PKIIdentityKindAgent, AgentID: "agent-1",
		State: PKIIdentityStateEnrollmentRequired, CreatedAt: now, UpdatedAt: now,
	})
}

func assertPKIStateEmpty(t *testing.T, store *GormStore, ctx context.Context) {
	t.Helper()
	state, err := store.LoadPKICanonicalState(ctx)
	if err != nil {
		t.Fatalf("LoadPKICanonicalState() error = %v", err)
	}
	if state.Settings != nil || state.InstanceLease != nil || len(state.Authorities)+len(state.Identities)+len(state.Certificates)+len(state.EnrollmentTokens)+len(state.LifecycleJobs)+len(state.Events) != 0 {
		t.Fatalf("rolled-back PKI state persisted: %+v", state)
	}
}

func newPKIFocusedTestStore(t *testing.T) *GormStore {
	t.Helper()
	store := openPKIFocusedTestStore(t, t.TempDir(), false)
	if err := store.SaveAgent(t.Context(), AgentRow{ID: "agent-1", Name: "PKI test agent"}); err != nil {
		t.Fatalf("SaveAgent(agent-1) error = %v", err)
	}
	return store
}

func openPKIFocusedTestStore(t *testing.T, root string, skipBootstrap bool) *GormStore {
	t.Helper()
	store, err := NewStore(StoreConfig{
		Driver: "sqlite", DataRoot: root, DSN: filepath.Join(root, "panel.db"), LocalAgentID: "local", SkipBootstrapSchema: skipBootstrap,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func pkiTestSettings(now time.Time) PKISettingsRow {
	return PKISettingsRow{
		PKIDomainID: "domain-1", CALifetimeSeconds: int64((10 * 365 * 24 * time.Hour) / time.Second),
		EndpointLifetimeSeconds: int64((90 * 24 * time.Hour) / time.Second), AuditRetentionDays: 365,
		SecurityRevision: 0, PKIEpoch: 1, UpgradeState: "migration_required", CreatedAt: now, UpdatedAt: now,
	}
}

func pkiTestEvent(id string, now time.Time) PKIEventRow {
	return PKIEventRow{
		ID: id, PKIDomainID: "domain-1", Type: "pki.initialized", OccurredAt: now,
		Source: "control_plane", ObjectType: "pki_domain", ObjectID: "domain-1", Result: "success", SecurityRevision: 0, DetailsJSON: "{}",
	}
}
