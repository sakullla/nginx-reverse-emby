package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type pkiLocalEmbeddedSource struct{}

func (pkiLocalEmbeddedSource) Sync(context.Context, goagentembedded.SyncRequest) (goagentembedded.Snapshot, error) {
	return goagentembedded.Snapshot{}, nil
}

type pkiLocalEmbeddedSink struct{}

func (pkiLocalEmbeddedSink) Save(context.Context, goagentembedded.RuntimeState) error { return nil }

func TestInternalPKILocalEnrollmentReplaysProductionEmbeddedCSR(t *testing.T) {
	t.Parallel()
	fixture := newPKIEnrollmentFixtureAt(t, time.Now().UTC().Truncate(time.Second))
	dataRoot := t.TempDir()
	runtime, err := goagentembedded.New(goagentembedded.Config{
		AgentID: "local-agent", AgentName: "embedded local agent", DataDir: dataRoot,
	}, pkiLocalEmbeddedSource{}, pkiLocalEmbeddedSink{})
	if err != nil {
		t.Fatalf("embedded.New() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	pending, err := runtime.TunnelCredentialStore().PrepareEnrollment(t.Context(), goagentembedded.PKIEnrollmentSpec{
		StorageIdentity: "agent", DomainID: "domain-1", AgentID: "local-agent",
		Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
	})
	if err != nil {
		t.Fatalf("PrepareEnrollment() error = %v", err)
	}
	enrollment := newPKIEnrollmentServiceForTest(
		t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("local-contract"),
	)
	snapshotSigner, err := NewPKIVaultSecuritySnapshotSigner(PKIVaultSecuritySnapshotSignerOptions{
		StateSource: fixture.store,
		Signer:      &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey},
	})
	if err != nil {
		t.Fatalf("NewPKIVaultSecuritySnapshotSigner() error = %v", err)
	}
	pki := &InternalPKIService{
		store: fixture.store, lease: pkiStaticLeaseGate{}, enrollment: enrollment,
		snapshotSigner: snapshotSigner, clock: func() time.Time { return fixture.now },
	}
	request := PKILocalEnrollRequest{
		RequestID: pending.Request.RequestID, Kind: pending.Request.Kind,
		ListenerID: pending.Request.ListenerID, Purpose: pending.Request.Purpose,
		CSRPEM: pending.Request.CSRPEM, DNSNames: pending.Request.DNSNames,
		IPAddresses: pending.Request.IPAddresses,
	}
	first, err := pki.EnrollLocal(t.Context(), request)
	if err != nil {
		t.Fatalf("EnrollLocal() error = %v", err)
	}
	replayed, err := pki.EnrollLocal(t.Context(), request)
	if err != nil {
		t.Fatalf("EnrollLocal(replay) error = %v", err)
	}
	if replayed.TunnelCredential.CertificateID != first.TunnelCredential.CertificateID ||
		replayed.TunnelCredential.CertificatePEM != first.TunnelCredential.CertificatePEM ||
		first.TunnelCredential.Purpose != storage.PKICertificatePurposeClient ||
		first.SecuritySnapshot.PKIDomainID != "domain-1" {
		t.Fatalf("local enrollment replay changed credential: first=%+v replay=%+v", first, replayed)
	}
	embeddedSecurity := pkiLocalEmbeddedSnapshot(first.SecuritySnapshot)
	backendPayload, agentPayload := pkiLocalContractSignaturePayloads(t, first.SecuritySnapshot, embeddedSecurity)
	if !bytes.Equal(backendPayload, agentPayload) {
		t.Fatalf("backend/agent security signature payload drift:\nbackend=%s\nagent=%s", backendPayload, agentPayload)
	}
	digest := sha256.Sum256(agentPayload)
	if !ecdsa.VerifyASN1(&fixture.authorityKey.PublicKey, digest[:], first.SecuritySnapshot.Signature) {
		t.Fatal("production local security snapshot signature does not verify against its canonical payload")
	}
	activated, err := runtime.TunnelCredentialStore().ActivateRegistrationCredential(t.Context(), goagentembedded.PKIActivateRequest{
		StorageIdentity: "agent", RequestID: pending.Request.RequestID,
		Credential: pkiLocalEmbeddedCredential(first.TunnelCredential),
		Security:   embeddedSecurity,
		Expectation: goagentembedded.PKICredentialExpectation{
			DomainID: "domain-1", AgentID: "local-agent",
			Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		},
	})
	if err != nil {
		t.Fatalf("ActivateRegistrationCredential() error = %v", err)
	}
	if activated.Manifest.Credential.CertificateID != first.TunnelCredential.CertificateID {
		t.Fatalf("activated embedded credential = %+v", activated)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("embedded Close() error = %v", err)
	}
	restarted, err := goagentembedded.New(goagentembedded.Config{
		AgentID: "local-agent", AgentName: "embedded local agent", DataDir: dataRoot,
	}, pkiLocalEmbeddedSource{}, pkiLocalEmbeddedSink{})
	if err != nil {
		t.Fatalf("embedded.New(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	reloaded, err := restarted.TunnelCredentialStore().LoadActiveCredential("agent")
	if err != nil {
		t.Fatalf("LoadActiveCredential(restart) error = %v", err)
	}
	pendingAfterRestart, err := restarted.TunnelCredentialStore().PendingEnrollments()
	if err != nil {
		t.Fatalf("PendingEnrollments(restart) error = %v", err)
	}
	if reloaded.Manifest.Credential.CertificateID != first.TunnelCredential.CertificateID || len(pendingAfterRestart) != 0 {
		t.Fatalf("restarted embedded credential=%+v pending=%+v", reloaded, pendingAfterRestart)
	}
	state := loadPKIEnrollmentState(t, fixture.store)
	if len(state.Identities) != 1 || len(state.Certificates) != 1 || len(state.EnrollmentReplays) != 1 {
		t.Fatalf("local enrollment replay duplicated canonical state: %+v", state)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "PRIVATE KEY") {
		t.Fatalf("local enrollment reply leaked private key: %s", encoded)
	}
}

func pkiLocalContractSignaturePayloads(t *testing.T, backend storage.PKISecuritySnapshot, agent goagentembedded.PKISecuritySnapshot) ([]byte, []byte) {
	t.Helper()
	trustGenerations := make([]int64, 0, len(backend.TrustRoots))
	descriptors := make([]PKISecurityTrustRootDescriptor, 0, len(backend.TrustRoots))
	for _, root := range backend.TrustRoots {
		trustGenerations = append(trustGenerations, root.Generation)
		descriptors = append(descriptors, PKISecurityTrustRootDescriptor{
			AuthorityID: root.AuthorityID, Generation: root.Generation, Status: root.Status,
			FingerprintSHA256: root.FingerprintSHA256, NotBefore: root.NotBefore, NotAfter: root.NotAfter,
		})
	}
	backendPayload, err := marshalPKIUnsignedSecuritySnapshot(PKIUnsignedSecuritySnapshot{
		PKIDomainID: backend.PKIDomainID,
		Version: PKISecuritySnapshotVersion{
			Version: PKISecurityVersion{PKIEpoch: backend.PKIEpoch, SecurityRevision: backend.SecurityRevision},
			Full:    backend.Full,
		},
		IssuedAt: backend.IssuedAt, TrustGenerations: trustGenerations, TrustRoots: descriptors,
		RevokedIdentityIDs: backend.RevokedIdentityIDs, RevokedSerials: backend.RevokedSerials,
	})
	if err != nil {
		t.Fatal(err)
	}
	type agentVersion struct {
		PKIEpoch         int64 `json:"pki_epoch"`
		SecurityRevision int64 `json:"security_revision"`
	}
	type agentSnapshotVersion struct {
		Version agentVersion `json:"version"`
		Full    bool         `json:"full"`
	}
	type agentTrustDescriptor struct {
		AuthorityID       string    `json:"authority_id"`
		Generation        int64     `json:"generation"`
		Status            string    `json:"status"`
		FingerprintSHA256 string    `json:"fingerprint_sha256"`
		NotBefore         time.Time `json:"not_before"`
		NotAfter          time.Time `json:"not_after"`
	}
	agentRoots := make([]agentTrustDescriptor, 0, len(agent.TrustRoots))
	agentGenerations := make([]int64, 0, len(agent.TrustRoots))
	for _, root := range agent.TrustRoots {
		agentGenerations = append(agentGenerations, root.Generation)
		agentRoots = append(agentRoots, agentTrustDescriptor{
			AuthorityID: root.AuthorityID, Generation: root.Generation, Status: root.Status,
			FingerprintSHA256: strings.ToLower(strings.TrimSpace(root.FingerprintSHA256)),
			NotBefore:         root.NotBefore.UTC(), NotAfter: root.NotAfter.UTC(),
		})
	}
	sort.Slice(agentRoots, func(i, j int) bool { return agentRoots[i].Generation < agentRoots[j].Generation })
	sort.Slice(agentGenerations, func(i, j int) bool { return agentGenerations[i] < agentGenerations[j] })
	agentPayload, err := json.Marshal(struct {
		PKIDomainID        string                 `json:"pki_domain_id"`
		Version            agentSnapshotVersion   `json:"version"`
		IssuedAt           time.Time              `json:"issued_at"`
		TrustGenerations   []int64                `json:"trust_generations"`
		TrustRoots         []agentTrustDescriptor `json:"trust_roots"`
		RevokedIdentityIDs []string               `json:"revoked_identity_ids"`
		RevokedSerials     []string               `json:"revoked_serials"`
	}{
		PKIDomainID: agent.PKIDomainID,
		Version: agentSnapshotVersion{
			Version: agentVersion{PKIEpoch: agent.PKIEpoch, SecurityRevision: agent.SecurityRevision},
			Full:    agent.Full,
		},
		IssuedAt: agent.IssuedAt.UTC(), TrustGenerations: agentGenerations, TrustRoots: agentRoots,
		RevokedIdentityIDs: agent.RevokedIdentityIDs, RevokedSerials: agent.RevokedSerials,
	})
	if err != nil {
		t.Fatal(err)
	}
	return backendPayload, agentPayload
}

func pkiLocalEmbeddedCredential(value storage.PKITunnelCredential) goagentembedded.PKITunnelCredential {
	return goagentembedded.PKITunnelCredential{
		IdentityID: value.IdentityID, CertificateID: value.CertificateID, Purpose: value.Purpose,
		CertificatePEM: value.CertificatePEM, PublicKeyFingerprint: value.PublicKeyFingerprint,
		AuthorityID: value.AuthorityID, CAGeneration: value.CAGeneration,
		NotBefore: value.NotBefore, NotAfter: value.NotAfter,
	}
}

func pkiLocalEmbeddedSnapshot(value storage.PKISecuritySnapshot) goagentembedded.PKISecuritySnapshot {
	roots := make([]goagentembedded.PKITrustRoot, 0, len(value.TrustRoots))
	for _, root := range value.TrustRoots {
		roots = append(roots, goagentembedded.PKITrustRoot{
			AuthorityID: root.AuthorityID, Generation: root.Generation, Status: root.Status,
			CertificatePEM: root.CertificatePEM, FingerprintSHA256: root.FingerprintSHA256,
			NotBefore: root.NotBefore, NotAfter: root.NotAfter,
		})
	}
	return goagentembedded.PKISecuritySnapshot{
		PKIDomainID: value.PKIDomainID, PKIEpoch: value.PKIEpoch,
		SecurityRevision: value.SecurityRevision, Full: value.Full, IssuedAt: value.IssuedAt,
		TrustRoots:         roots,
		RevokedIdentityIDs: slices.Clone(value.RevokedIdentityIDs),
		RevokedSerials:     slices.Clone(value.RevokedSerials),
		SignerGeneration:   value.SignerGeneration,
		Signature:          append([]byte(nil), value.Signature...),
	}
}
