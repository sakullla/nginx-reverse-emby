package pki

import (
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
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestPrepareEnrollmentPersistsReplaySafeKeyAndCSR(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
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
		t.Fatalf("anonymous CSR is not server-bound and empty: %+v", request)
	}
	for _, name := range []string{pendingKeyName, pendingCSRName, pendingJournalName} {
		path := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, name)
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat %s: %v", name, statErr)
		}
		if got := info.Mode().Perm(); runtime.GOOS != "windows" && got&0o077 != 0 {
			t.Fatalf("%s permissions = %o, want no group/other access", name, got)
		}
	}

	_, err = store.PrepareEnrollment(context.Background(), EnrollmentSpec{
		StorageIdentity: "agent", DomainID: "other-domain", AgentID: "agent-1",
		Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient,
	})
	if !errors.Is(err, ErrPendingConflict) {
		t.Fatalf("conflicting PrepareEnrollment() error = %v, want ErrPendingConflict", err)
	}
}

func TestSecuritySnapshotRejectsDowngradeAndRequiresEpochZeroFullRecovery(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)

	initial := authority.snapshot(t, "domain-1", 7, 3, true, nil, nil, now)
	state, err := store.ApplySecuritySnapshot(initial)
	if err != nil {
		t.Fatalf("ApplySecuritySnapshot() error = %v", err)
	}
	if state.Snapshot.PKIEpoch != 7 || state.Snapshot.SecurityRevision != 3 {
		t.Fatalf("active security version = (%d,%d)", state.Snapshot.PKIEpoch, state.Snapshot.SecurityRevision)
	}
	if _, err := store.ApplySecuritySnapshot(initial); err != nil {
		t.Fatalf("identical snapshot replay error = %v", err)
	}
	delta := authority.snapshot(t, "domain-1", 7, 4, false, nil, nil, now.Add(time.Second))
	if _, err := store.ApplySecuritySnapshot(delta); err != nil {
		t.Fatalf("same-epoch delta activation error = %v", err)
	}
	deltaReopened, err := NewStore(store.dataRoot, WithClock(func() time.Time { return now.Add(2 * time.Second) }))
	if err != nil {
		t.Fatalf("reopen delta store: %v", err)
	}
	if loadedDelta, err := deltaReopened.LoadSecuritySnapshot(); err != nil || loadedDelta.Snapshot.Full {
		t.Fatalf("reloaded delta = %+v, error = %v", loadedDelta.Snapshot, err)
	}
	downgrade := authority.snapshot(t, "domain-1", 7, 2, true, nil, nil, now.Add(time.Second))
	if _, err := store.ApplySecuritySnapshot(downgrade); !errors.Is(err, ErrSecurityDowngrade) {
		t.Fatalf("downgrade error = %v, want ErrSecurityDowngrade", err)
	}
	invalidEpoch := authority.snapshot(t, "domain-1", 8, 1, true, nil, nil, now.Add(2*time.Second))
	if _, err := store.ApplySecuritySnapshot(invalidEpoch); !errors.Is(err, ErrSecurityDowngrade) {
		t.Fatalf("higher nonzero epoch revision error = %v, want ErrSecurityDowngrade", err)
	}
	nextEpoch := authority.snapshot(t, "domain-1", 8, 0, true, []string{"identity-revoked"}, []string{"ABCD"}, now.Add(3*time.Second))
	state, err = store.ApplySecuritySnapshot(nextEpoch)
	if err != nil {
		t.Fatalf("higher epoch revision zero error = %v", err)
	}
	if !slices.Equal(state.Snapshot.RevokedSerials, []string{"abcd"}) {
		t.Fatalf("normalized revoked serials = %v", state.Snapshot.RevokedSerials)
	}

	reopened, err := NewStore(store.dataRoot, WithClock(func() time.Time { return now.Add(4 * time.Second) }))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	loaded, err := reopened.LoadSecuritySnapshot()
	if err != nil {
		t.Fatalf("LoadSecuritySnapshot() error = %v", err)
	}
	if loaded.Hash != state.Hash || loaded.Snapshot.PKIEpoch != 8 {
		t.Fatalf("reopened security state = %+v, want hash %s epoch 8", loaded, state.Hash)
	}
	ack, err := reopened.SecurityAcknowledgement("certificate-1")
	if err != nil {
		t.Fatalf("SecurityAcknowledgement() error = %v", err)
	}
	if ack.CertificateID != "certificate-1" || !slices.Equal(ack.TrustGenerations, []int64{1}) {
		t.Fatalf("ack = %+v", ack)
	}
}

func TestActivateCredentialPublishesCompleteGenerationAndKeepsOldOnFailure(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	snapshot := authority.snapshot(t, "domain-1", 1, 0, true, nil, nil, now)
	expectation := CredentialExpectation{
		DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent,
		Purpose: model.PKICertificatePurposeClient, Now: now,
	}

	pending := prepareKnownAgent(t, store, expectation)
	credential := authority.issueCredential(t, pending, expectation, "identity-1", "certificate-1", now)
	active, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID,
		Credential: credential, Security: snapshot, Expectation: expectation,
	})
	if err != nil {
		t.Fatalf("ActivateCredential() error = %v", err)
	}
	if active.Manifest.Credential.CertificateID != "certificate-1" || active.TLSCertificate.PrivateKey == nil {
		t.Fatalf("active credential = %+v", active.Manifest)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed pending directory still exists: %v", err)
	}
	ackData, err := os.ReadFile(filepath.Join(store.Root(), securityDirName, "ack.json"))
	if err != nil || !strings.Contains(string(ackData), `"certificate_id":"certificate-1"`) {
		t.Fatalf("durable security acknowledgement = %q, error = %v", ackData, err)
	}
	loaded, err := store.LoadActiveCredential("agent")
	if err != nil {
		t.Fatalf("LoadActiveCredential() error = %v", err)
	}
	if loaded.Manifest.Generation != active.Manifest.Generation || loaded.Leaf == nil {
		t.Fatalf("loaded active generation = %+v", loaded.Manifest)
	}

	badPending := prepareKnownAgent(t, store, expectation)
	badCredential := authority.issueCredential(t, badPending, expectation, "identity-1", "certificate-2", now)
	badCredential.PublicKeyFingerprint = strings.Repeat("0", 64)
	if _, err := store.ActivateCredential(context.Background(), ActivateRequest{
		StorageIdentity: "agent", RequestID: badPending.Request.RequestID,
		Credential: badCredential, Security: snapshot, Expectation: expectation,
	}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("invalid credential activation error = %v, want ErrCredentialInvalid", err)
	}
	stillActive, err := store.LoadActiveCredential("agent")
	if err != nil {
		t.Fatalf("LoadActiveCredential() after failed candidate error = %v", err)
	}
	if stillActive.Manifest.Generation != active.Manifest.Generation {
		t.Fatalf("failed candidate changed active generation from %s to %s", active.Manifest.Generation, stillActive.Manifest.Generation)
	}
}

func TestActivateStagedRegistrationConsumesSanitizedJoinResponse(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	pending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{
		StorageIdentity: "agent", Kind: model.PKIIdentityKindAgent, Purpose: model.PKICertificatePurposeClient,
	})
	if err != nil {
		t.Fatalf("PrepareEnrollment() error = %v", err)
	}
	expectation := CredentialExpectation{
		DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindAgent,
		Purpose: model.PKICertificatePurposeClient, Now: now,
	}
	staged := StagedRegistration{
		AgentID:          "agent-1",
		TunnelCredential: authority.issueCredential(t, pending, expectation, "identity-1", "certificate-staged", now),
		SecuritySnapshot: authority.snapshot(t, "domain-1", 0, 0, true, nil, nil, now),
	}
	responsePath := filepath.Join(store.Root(), identitiesDirName, "agent", pendingDirName, "response.json")
	if _, err := writePrivateJSON(responsePath, staged); err != nil {
		t.Fatalf("write staged response: %v", err)
	}
	active, err := store.ActivateStagedRegistration(context.Background(), "agent")
	if err != nil {
		t.Fatalf("ActivateStagedRegistration() error = %v", err)
	}
	if active.Manifest.Credential.CertificateID != "certificate-staged" {
		t.Fatalf("active staged certificate = %s", active.Manifest.Credential.CertificateID)
	}
	encoded, err := json.Marshal(staged)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "control-secret") || strings.Contains(string(encoded), "register-secret") {
		t.Fatal("staged registration unexpectedly contains a raw control/register token")
	}
}

func TestSecuritySnapshotRejectsForgedSignature(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t, now)
	authority := newTestAuthority(t, now, "authority-1", 1)
	snapshot := authority.snapshot(t, "domain-1", 0, 0, true, nil, nil, now)
	snapshot.Signature[0] ^= 0xff
	if _, err := store.ApplySecuritySnapshot(snapshot); !errors.Is(err, ErrSecurityInvalid) {
		t.Fatalf("forged signature error = %v, want ErrSecurityInvalid", err)
	}
}

func newTestStore(t *testing.T, now time.Time) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir(), WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func prepareKnownAgent(t *testing.T, store *Store, expectation CredentialExpectation) PendingEnrollment {
	t.Helper()
	pending, err := store.PrepareEnrollment(context.Background(), EnrollmentSpec{
		StorageIdentity: "agent", DomainID: expectation.DomainID, AgentID: expectation.AgentID,
		Kind: expectation.Kind, Purpose: expectation.Purpose,
	})
	if err != nil {
		t.Fatalf("PrepareEnrollment() error = %v", err)
	}
	return pending
}

type testAuthority struct {
	key         *ecdsa.PrivateKey
	certificate *x509.Certificate
	root        model.PKITrustRoot
}

func newTestAuthority(t *testing.T, now time.Time, authorityID string, generation int64) testAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(generation), Subject: pkix.Name{CommonName: authorityID},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(der)
	return testAuthority{
		key: key, certificate: certificate,
		root: model.PKITrustRoot{
			AuthorityID: authorityID, Generation: generation, Status: "active",
			CertificatePEM:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			FingerprintSHA256: hex.EncodeToString(fingerprint[:]), NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
		},
	}
}

func (authority testAuthority) snapshot(t *testing.T, domain string, epoch, revision int64, full bool, revokedIdentities, revokedSerials []string, issuedAt time.Time) model.PKISecuritySnapshot {
	t.Helper()
	identities := slices.Clone(revokedIdentities)
	serials := slices.Clone(revokedSerials)
	slices.Sort(identities)
	for index := range serials {
		serials[index] = strings.ToLower(serials[index])
	}
	slices.Sort(serials)
	descriptor := securityTrustDescriptor{
		AuthorityID: authority.root.AuthorityID, Generation: authority.root.Generation, Status: authority.root.Status,
		FingerprintSHA256: authority.root.FingerprintSHA256, NotBefore: authority.root.NotBefore.UTC(), NotAfter: authority.root.NotAfter.UTC(),
	}
	payload, err := json.Marshal(securitySignaturePayload{
		PKIDomainID: domain,
		Version:     securitySnapshotVersion{Version: securityVersion{PKIEpoch: epoch, SecurityRevision: revision}, Full: full},
		IssuedAt:    issuedAt.UTC(), TrustGenerations: []int64{authority.root.Generation}, TrustRoots: []securityTrustDescriptor{descriptor},
		RevokedIdentityIDs: identities, RevokedSerials: serials,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, authority.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return model.PKISecuritySnapshot{
		PKIDomainID: domain, PKIEpoch: epoch, SecurityRevision: revision, Full: full, IssuedAt: issuedAt,
		TrustRoots: []model.PKITrustRoot{authority.root}, RevokedIdentityIDs: identities, RevokedSerials: serials,
		SignerGeneration: authority.root.Generation, Signature: signature,
	}
}

func (authority testAuthority) issueCredential(t *testing.T, pending PendingEnrollment, expectation CredentialExpectation, identityID, certificateID string, now time.Time) model.PKITunnelCredential {
	t.Helper()
	csrBlock, _ := pem.Decode([]byte(pending.Request.CSRPEM))
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	identityURI, err := url.Parse("spiffe://" + expectation.DomainID + "/agent/" + expectation.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if expectation.Kind == model.PKIIdentityKindListener {
		identityURI.Path += "/listener/" + expectation.ListenerID
	}
	usage := x509.ExtKeyUsageClientAuth
	if expectation.Purpose == model.PKICertificatePurposeServer {
		usage = x509.ExtKeyUsageServerAuth
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: identityURI.String()},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		URIs: []*url.URL{identityURI}, DNSNames: slices.Clone(expectation.DNSNames),
	}
	for _, value := range expectation.IPAddresses {
		template.IPAddresses = append(template.IPAddresses, []byte(value))
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, csr.PublicKey, authority.key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
	return model.PKITunnelCredential{
		IdentityID: identityID, CertificateID: certificateID, Purpose: expectation.Purpose,
		CertificatePEM:       string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		PublicKeyFingerprint: hex.EncodeToString(fingerprint[:]), AuthorityID: authority.root.AuthorityID,
		CAGeneration: authority.root.Generation, NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
	}
}
