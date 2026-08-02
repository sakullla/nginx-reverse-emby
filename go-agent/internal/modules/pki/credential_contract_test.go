package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

var errCredentialContractInjected = errors.New("injected credential contract persistence failure")

func TestCredentialActivationRejectsNonCanonicalLeafAndPreservesReplay(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*x509.Certificate)
	}{
		{name: "missing basic constraints", mutate: func(certificate *x509.Certificate) {
			certificate.BasicConstraintsValid = false
		}},
		{name: "extra subject RDN", mutate: func(certificate *x509.Certificate) {
			certificate.Subject.Organization = []string{"unexpected"}
		}},
		{name: "email SAN", mutate: func(certificate *x509.Certificate) {
			certificate.EmailAddresses = []string{"unexpected@example.test"}
		}},
		{name: "unsupported registered ID SAN", mutate: func(certificate *x509.Certificate) {
			encoded, err := asn1.Marshal([]asn1.RawValue{
				{Class: asn1.ClassContextSpecific, Tag: 6, Bytes: []byte(certificate.URIs[0].String())},
				{Class: asn1.ClassContextSpecific, Tag: 8, Bytes: []byte{42, 3, 4}},
			})
			if err != nil {
				panic(err)
			}
			certificate.ExtraExtensions = append(certificate.ExtraExtensions, pkix.Extension{Id: subjectAlternativeNameOID, Value: encoded})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t, now)
			authority := newTestAuthority(t, now, "authority-1", 1)
			expectation := credentialContractAgentExpectation(now)
			initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)

			baselinePending := prepareKnownAgent(t, store, expectation)
			baselineCredential := authority.issueCredential(t, baselinePending, expectation, "identity-old", "certificate-old", now)
			baseline, err := store.ActivateCredential(context.Background(), ActivateRequest{
				StorageIdentity: "agent", RequestID: baselinePending.Request.RequestID,
				Credential: baselineCredential, Security: initial, Expectation: expectation,
			})
			if err != nil {
				t.Fatalf("activate baseline credential: %v", err)
			}

			pending := prepareKnownAgent(t, store, expectation)
			invalid := authority.issueCredentialWithMutator(t, pending, expectation, "identity-new", "certificate-invalid", now, test.mutate)
			if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
				StorageIdentity: "agent", RequestID: pending.Request.RequestID,
				Credential: invalid, Security: initial, Expectation: expectation,
			}); !errors.Is(err, ErrCredentialInvalid) {
				t.Fatalf("invalid ActivateCredential() error = %v, want ErrCredentialInvalid", err)
			}
			active, err := store.LoadActiveCredential("agent")
			if err != nil || active.Manifest.Generation != baseline.Manifest.Generation {
				t.Fatalf("invalid credential changed active generation: %+v, error = %v", active.Manifest, err)
			}
			replay, err := store.LoadPending("agent")
			if err != nil || replay.Request.RequestID != pending.Request.RequestID || replay.Request.CSRPEM != pending.Request.CSRPEM ||
				replay.RequestFingerprint != pending.RequestFingerprint {
				t.Fatalf("invalid credential damaged pending replay: %+v, error = %v", replay, err)
			}

			corrected := authority.issueCredential(t, pending, expectation, "identity-new", "certificate-corrected", now)
			activated, err := store.ActivateCredential(context.Background(), ActivateRequest{
				StorageIdentity: "agent", RequestID: pending.Request.RequestID,
				Credential: corrected, Security: initial, Expectation: expectation,
			})
			if err != nil || activated.Manifest.Credential.CertificateID != "certificate-corrected" {
				t.Fatalf("corrected ActivateCredential() = %+v, error = %v", activated, err)
			}
		})
	}
}

func TestCredentialChainRejectionIsNotMaskedByAuthorityMetadata(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	trusted := newTestAuthority(t, now, "authority-1", 1)
	untrusted := newTestAuthority(t, now, "authority-2", 2)
	expectation := credentialContractAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := untrusted.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	credential.AuthorityID = trusted.root.AuthorityID
	credential.CAGeneration = trusted.root.Generation
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential,
		Security: trusted.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: expectation,
	}); !errors.Is(err, ErrCredentialInvalid) || !strings.Contains(err.Error(), "chain verification") {
		t.Fatalf("untrusted chain error = %v, want chain-specific ErrCredentialInvalid", err)
	}
	if replay, err := store.LoadPending("agent"); err != nil || replay.Request.RequestID != pending.Request.RequestID {
		t.Fatalf("chain rejection damaged pending replay: %+v, error = %v", replay, err)
	}
}

func TestCredentialCrashBoundariesRecoverAfterStoreReopen(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		point     string
		committed bool
	}{
		{point: "credential.after_generation_publish"},
		{point: "credential.after_pointer_publish", committed: true},
		{point: "credential.after_ack_publish", committed: true},
		{point: "credential.after_pending_remove", committed: true},
	} {
		t.Run(test.point, func(t *testing.T) {
			dataRoot := t.TempDir()
			failed := false
			store, err := NewStore(dataRoot, WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
				if point == test.point && !failed {
					failed = true
					return errCredentialContractInjected
				}
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			authority := newTestAuthority(t, now, "authority-1", 1)
			expectation := credentialContractAgentExpectation(now)
			pending := prepareKnownAgent(t, store, expectation)
			credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
			request := ActivateRequest{
				StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential,
				Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: expectation,
			}
			metadata, activateErr := store.ActivateCredential(context.Background(), request)
			if test.committed {
				if !errors.Is(activateErr, ErrActivationCommitted) || metadata.Manifest.Credential.CertificateID != "certificate-1" {
					t.Fatalf("committed ActivateCredential() = %+v, error = %v", metadata, activateErr)
				}
			} else if !errors.Is(activateErr, errCredentialContractInjected) {
				t.Fatalf("uncommitted ActivateCredential() error = %v", activateErr)
			}

			reopened, err := NewStore(dataRoot, WithClock(func() time.Time { return now.Add(2 * time.Minute) }))
			if err != nil {
				t.Fatal(err)
			}
			if !test.committed {
				higher := authority.snapshot(t, "domain-1", 1, 1, false, nil, nil, now.Add(time.Minute))
				if _, err := reopened.ApplySecuritySnapshot(higher); err != nil {
					t.Fatalf("ApplySecuritySnapshot(higher) error = %v", err)
				}
				request.Security = higher
			} else if active, err := reopened.LoadActiveCredential("agent"); err != nil || active.Manifest.Credential.CertificateID != "certificate-1" {
				t.Fatalf("reopened active credential = %+v, error = %v", active, err)
			}
			if _, err := reopened.ActivateCredential(context.Background(), request); err != nil {
				t.Fatalf("reopened reconciliation error = %v", err)
			}
			if _, err := reopened.LoadPending("agent"); !errors.Is(err, ErrPendingNotFound) {
				t.Fatalf("pending after reconciliation error = %v, want ErrPendingNotFound", err)
			}
			acknowledgement, err := reopened.SecurityAcknowledgement("agent")
			if err != nil || acknowledgement.CertificateID != "certificate-1" {
				t.Fatalf("reopened acknowledgement = %+v, error = %v", acknowledgement, err)
			}
		})
	}
}

func TestConcurrentRevocationWaitsForCredentialCutoverBarrier(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	selected := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now }), withPersistenceCheckpoint(func(point string) error {
		if point == "credential.security_selected" {
			once.Do(func() { close(selected) })
			<-release
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := credentialContractAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	initial := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	revoked := authority.snapshot(t, "domain-1", 1, 1, false, []string{"identity-1"}, nil, now.Add(time.Minute))

	activationDone := make(chan error, 1)
	go func() {
		_, err := store.ActivateCredential(context.Background(), ActivateRequest{
			StorageIdentity: "agent", RequestID: pending.Request.RequestID,
			Credential: credential, Security: initial, Expectation: expectation,
		})
		activationDone <- err
	}()
	<-selected
	revocationAttempted := make(chan struct{})
	revocationDone := make(chan error, 1)
	go func() {
		close(revocationAttempted)
		_, err := store.ApplySecuritySnapshot(revoked)
		revocationDone <- err
	}()
	<-revocationAttempted
	if store.mu.TryLock() {
		store.mu.Unlock()
		close(release)
		<-activationDone
		<-revocationDone
		t.Fatal("credential cutover did not hold the authoritative Store lock at its security barrier")
	}
	close(release)
	if err := <-activationDone; err != nil {
		t.Fatalf("ActivateCredential() error = %v", err)
	}
	if err := <-revocationDone; err != nil {
		t.Fatalf("ApplySecuritySnapshot(revocation) error = %v", err)
	}
	if _, err := store.LoadActiveCredential("agent"); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("revoked active credential error = %v, want ErrCredentialInvalid", err)
	}
}

func TestActiveCredentialSerializationIsStrictlyOpaque(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	expectation := credentialContractAgentExpectation(now)
	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID, Credential: credential,
		Security: authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now), Expectation: expectation,
	}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	active, err := store.loadActiveCredentialLocked("agent")
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(active)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "{}" {
		t.Fatalf("active credential JSON = %s, want strictly opaque {}", encoded)
	}
	privateKey, ok := active.tlsCertificate.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("active private key type = %T", active.tlsCertificate.PrivateKey)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, secretEncoding := range []string{
		base64.StdEncoding.EncodeToString(privateDER),
		hex.EncodeToString(privateDER),
		privateKey.D.Text(16),
	} {
		if strings.Contains(string(encoded), secretEncoding) {
			t.Fatalf("active credential JSON contains private-key encoding %q", secretEncoding)
		}
	}
}

func credentialContractAgentExpectation(now time.Time) CredentialExpectation {
	return CredentialExpectation{
		DomainID: "domain-1", AgentID: "agent-1", Kind: "agent",
		Purpose: "client", Now: now,
	}
}
