package service

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKITokenCreatesDigestOnlyWithDefaultTTL(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	randomBytes := make([]byte, pkiEnrollmentTokenBytes)
	for index := range randomBytes {
		randomBytes[index] = byte(index + 1)
	}
	tokens, err := NewPKITokenService(PKITokenServiceOptions{
		Store: fixture.store, Clock: func() time.Time { return fixture.now }, Random: &singleReadReader{value: randomBytes},
		NewID: sequencePKIID("token-1"),
	})
	if err != nil {
		t.Fatalf("NewPKITokenService() error = %v", err)
	}
	issued, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeNewAgent, CreatedBy: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	decoded, err := hex.DecodeString(issued.Token)
	if err != nil || len(decoded) != pkiEnrollmentTokenBytes {
		t.Fatalf("plaintext token = %q, decode error = %v", issued.Token, err)
	}
	if issued.ExpiresAt.Sub(fixture.now) != defaultPKIEnrollmentTokenTTL {
		t.Fatalf("token TTL = %v, want %v", issued.ExpiresAt.Sub(fixture.now), defaultPKIEnrollmentTokenTTL)
	}
	state := loadPKIEnrollmentState(t, fixture.store)
	if len(state.EnrollmentTokens) != 1 {
		t.Fatalf("enrollment token rows = %d, want 1", len(state.EnrollmentTokens))
	}
	digest := sha256.Sum256(decoded)
	row := state.EnrollmentTokens[0]
	if row.TokenDigestSHA256 != hex.EncodeToString(digest[:]) || row.TokenDigestSHA256 == issued.Token {
		t.Fatalf("persisted token digest = %q, want SHA-256 digest only", row.TokenDigestSHA256)
	}
	if row.ConsumedAt != nil || row.BoundAgentID != "" || row.Scope != PKIEnrollmentTokenScopeNewAgent {
		t.Fatalf("persisted token metadata = %+v", row)
	}
}

func TestPKITokenConcurrentConsumptionAllowsExactlyOneWinner(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	tokens := newPKIEnrollmentTokenService(t, fixture, sequencePKIID("token-1"))
	issued, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: "agent-a", CreatedBy: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	digest, err := digestPKIEnrollmentToken(issued.Token)
	if err != nil {
		t.Fatalf("digestPKIEnrollmentToken() error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errorsSeen := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			var consumed bool
			err := fixture.store.WithPKITransaction(context.Background(), func(tx *storage.PKITransaction) error {
				_, consumedNow, err := tx.ConsumePKIEnrollmentToken(context.Background(), digest, fixture.now)
				consumed = consumedNow
				return err
			})
			if err != nil {
				errorsSeen <- err
				return
			}
			results <- consumed
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Errorf("ConsumePKIEnrollmentToken() error = %v", err)
	}
	winners := 0
	for consumed := range results {
		if consumed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent token winners = %d, want 1", winners)
	}
}

func TestPKIIdentityValidatesCSRAlgorithmPurposeAndOwner(t *testing.T) {
	key := mustPKIEnrollmentKey(t)
	binding, err := newPKIIdentityBinding("domain-1", storage.PKIIdentityKindAgent, "agent-a", "", storage.PKICertificatePurposeClient, nil, nil)
	if err != nil {
		t.Fatalf("newPKIIdentityBinding() error = %v", err)
	}
	valid := mustPKIEnrollmentCSR(t, key, binding, false)
	parsed, err := parsePKIEnrollmentCSR(valid)
	if err != nil {
		t.Fatalf("parsePKIEnrollmentCSR() error = %v", err)
	}
	if err := validatePKIEnrollmentCSRBinding(parsed, binding, false); err != nil {
		t.Fatalf("validatePKIEnrollmentCSRBinding() error = %v", err)
	}

	wrongBinding, err := newPKIIdentityBinding("domain-1", storage.PKIIdentityKindAgent, "agent-b", "", storage.PKICertificatePurposeClient, nil, nil)
	if err != nil {
		t.Fatalf("newPKIIdentityBinding(wrong owner) error = %v", err)
	}
	if err := validatePKIEnrollmentCSRBinding(parsed, wrongBinding, false); !errors.Is(err, ErrPKIEnrollmentOwnerMismatch) {
		t.Fatalf("wrong-owner CSR error = %v, want ErrPKIEnrollmentOwnerMismatch", err)
	}
	if _, err := newPKIIdentityBinding("domain-1", storage.PKIIdentityKindAgent, "agent-a", "", storage.PKICertificatePurposeServer, nil, nil); !errors.Is(err, ErrPKIEnrollmentRequest) {
		t.Fatalf("wrong-purpose binding error = %v, want ErrPKIEnrollmentRequest", err)
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	rsaDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, rsaKey)
	if err != nil {
		t.Fatalf("CreateCertificateRequest(RSA) error = %v", err)
	}
	rsaPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: rsaDER}))
	if _, err := parsePKIEnrollmentCSR(rsaPEM); !errors.Is(err, ErrPKIEnrollmentCSR) {
		t.Fatalf("RSA CSR error = %v, want ErrPKIEnrollmentCSR", err)
	}
	boundCSR := mustPKIEnrollmentCSR(t, key, binding, false)
	boundParsed, err := parsePKIEnrollmentCSR(boundCSR)
	if err != nil {
		t.Fatalf("parse bound CSR: %v", err)
	}
	if err := validatePKIEnrollmentCSRBinding(boundParsed, binding, true); !errors.Is(err, ErrPKIEnrollmentOwnerMismatch) {
		t.Fatalf("non-anonymous new-agent CSR error = %v, want ErrPKIEnrollmentOwnerMismatch", err)
	}
}

func TestPKIEnrollmentNewAgentAndBoundReenrollmentAreAtomic(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	tokens := newPKIEnrollmentTokenService(t, fixture, sequencePKIID("token-new", "token-reenroll", "token-reuse", "token-owner", "token-signer"))
	signer := &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}
	enrollment := newPKIEnrollmentServiceForTest(t, fixture, signer, sequencePKIID(
		"agent-generated", "certificate-1", "identity-1", "event-1",
		"certificate-2", "event-2",
	))

	newToken, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeNewAgent, CreatedBy: "admin-new"})
	if err != nil {
		t.Fatalf("Create(new token) error = %v", err)
	}
	firstKey := mustPKIEnrollmentKey(t)
	first, err := enrollment.Enroll(t.Context(), PKIEnrollRequest{
		Token: newToken.Token, Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentAnonymousCSR(t, firstKey),
	})
	if err != nil {
		t.Fatalf("Enroll(new agent) error = %v", err)
	}
	if first.AgentID != "agent-generated" || first.IdentityID != "identity-1" || first.CertificateID != "certificate-1" {
		t.Fatalf("new enrollment result = %+v", first)
	}
	assertPKIEnrollmentCertificateProfile(t, first.CertificatePEM, fixture.authorityCertificate, "spiffe://domain-1/agent/agent-generated", x509.ExtKeyUsageClientAuth, nil, nil)

	reenrollToken, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: first.AgentID, CreatedBy: "admin-reenroll"})
	if err != nil {
		t.Fatalf("Create(re-enrollment token) error = %v", err)
	}
	binding, err := newPKIIdentityBinding("domain-1", storage.PKIIdentityKindAgent, first.AgentID, "", storage.PKICertificatePurposeClient, nil, nil)
	if err != nil {
		t.Fatalf("newPKIIdentityBinding() error = %v", err)
	}
	secondKey := mustPKIEnrollmentKey(t)
	second, err := enrollment.Enroll(t.Context(), PKIEnrollRequest{
		Token: reenrollToken.Token, AgentID: first.AgentID, Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, secondKey, binding, false),
	})
	if err != nil {
		t.Fatalf("Enroll(bound re-enrollment) error = %v", err)
	}
	if second.AgentID != first.AgentID || second.IdentityID != first.IdentityID || second.CertificateID == first.CertificateID {
		t.Fatalf("re-enrollment did not preserve stable owner: first=%+v second=%+v", first, second)
	}
	state := loadPKIEnrollmentState(t, fixture.store)
	if len(state.Identities) != 1 || len(state.Certificates) != 2 || len(state.Events) != 2 {
		t.Fatalf("canonical enrollment counts = identities:%d certificates:%d events:%d", len(state.Identities), len(state.Certificates), len(state.Events))
	}
	identity := state.Identities[0]
	if identity.AgentID != first.AgentID || identity.CurrentCertificateID == nil || *identity.CurrentCertificateID != second.CertificateID || identity.State != storage.PKIIdentityStateActive {
		t.Fatalf("stable identity after re-enrollment = %+v", identity)
	}
	oldCertificate := findPKIEnrollmentCertificate(t, state, first.CertificateID)
	if oldCertificate.Status != storage.PKICertificateStatusSuperseded || oldCertificate.SupersededByID == nil || *oldCertificate.SupersededByID != second.CertificateID {
		t.Fatalf("old certificate after re-enrollment = %+v", oldCertificate)
	}
	newCertificate := findPKIEnrollmentCertificate(t, state, second.CertificateID)
	if newCertificate.Status != storage.PKICertificateStatusActive {
		t.Fatalf("new certificate status = %q, want active", newCertificate.Status)
	}

	reuseToken, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: first.AgentID, CreatedBy: "admin"})
	if err != nil {
		t.Fatalf("Create(reuse token) error = %v", err)
	}
	_, err = enrollment.Enroll(t.Context(), PKIEnrollRequest{
		Token: reuseToken.Token, AgentID: first.AgentID, Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, secondKey, binding, false),
	})
	if !errors.Is(err, ErrPKIEnrollmentPublicKeyReuse) {
		t.Fatalf("reused public key error = %v, want ErrPKIEnrollmentPublicKeyReuse", err)
	}
	assertPKIEnrollmentTokenUnconsumed(t, fixture.store, reuseToken.Token)
	assertPKIEnrollmentFactCounts(t, fixture.store, 1, 2, 2)

	ownerToken, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: first.AgentID, CreatedBy: "admin"})
	if err != nil {
		t.Fatalf("Create(owner token) error = %v", err)
	}
	thirdKey := mustPKIEnrollmentKey(t)
	_, err = enrollment.Enroll(t.Context(), PKIEnrollRequest{
		Token: ownerToken.Token, AgentID: "attacker", Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, thirdKey, binding, false),
	})
	if !errors.Is(err, ErrPKIEnrollmentOwnerMismatch) {
		t.Fatalf("wrong request owner error = %v, want ErrPKIEnrollmentOwnerMismatch", err)
	}
	assertPKIEnrollmentTokenUnconsumed(t, fixture.store, ownerToken.Token)
	assertPKIEnrollmentFactCounts(t, fixture.store, 1, 2, 2)

	signingToken, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: first.AgentID, CreatedBy: "admin"})
	if err != nil {
		t.Fatalf("Create(signing token) error = %v", err)
	}
	signer.err = errors.New("vault unavailable")
	_, err = enrollment.Enroll(t.Context(), PKIEnrollRequest{
		Token: signingToken.Token, AgentID: first.AgentID, Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, thirdKey, binding, false),
	})
	if !errors.Is(err, ErrPKIEnrollmentAuthorityUnavailable) {
		t.Fatalf("signing failure error = %v, want ErrPKIEnrollmentAuthorityUnavailable", err)
	}
	assertPKIEnrollmentTokenUnconsumed(t, fixture.store, signingToken.Token)
	assertPKIEnrollmentFactCounts(t, fixture.store, 1, 2, 2)
}

func TestPKIEnrollmentConcurrentSameTokenHasOneCompleteWinner(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	tokens := newPKIEnrollmentTokenService(t, fixture, sequencePKIID("token-1"))
	issued, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeNewAgent, CreatedBy: "admin"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	enrollment := newPKIEnrollmentServiceForTest(t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("enrollment"))
	requests := []PKIEnrollRequest{
		{Token: issued.Token, Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient, CSRPEM: mustPKIEnrollmentAnonymousCSR(t, mustPKIEnrollmentKey(t))},
		{Token: issued.Token, Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient, CSRPEM: mustPKIEnrollmentAnonymousCSR(t, mustPKIEnrollmentKey(t))},
	}
	start := make(chan struct{})
	results := make(chan error, len(requests))
	var workers sync.WaitGroup
	for _, request := range requests {
		request := request
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, enrollErr := enrollment.Enroll(context.Background(), request)
			results <- enrollErr
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	rejections := 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrPKIEnrollmentTokenRejected):
			rejections++
		default:
			t.Errorf("concurrent Enroll() error = %v", err)
		}
	}
	if winners != 1 || rejections != 1 {
		t.Fatalf("concurrent enrollment winners=%d rejections=%d, want 1/1", winners, rejections)
	}
	assertPKIEnrollmentFactCounts(t, fixture.store, 1, 1, 1)
}

func TestPKIEnrollmentLocalAndListenerProfiles(t *testing.T) {
	t.Run("embedded local agent is bound without token", func(t *testing.T) {
		fixture := newPKIEnrollmentFixture(t)
		enrollment := newPKIEnrollmentServiceForTest(t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("local"))
		binding, err := newPKIIdentityBinding("domain-1", storage.PKIIdentityKindAgent, "local-agent", "", storage.PKICertificatePurposeClient, nil, nil)
		if err != nil {
			t.Fatalf("newPKIIdentityBinding() error = %v", err)
		}
		result, err := enrollment.EnrollLocal(t.Context(), PKILocalEnrollRequest{
			Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
			CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
		})
		if err != nil {
			t.Fatalf("EnrollLocal() error = %v", err)
		}
		if result.AgentID != "local-agent" {
			t.Fatalf("local enrollment agent ID = %q", result.AgentID)
		}
		state := loadPKIEnrollmentState(t, fixture.store)
		if len(state.EnrollmentTokens) != 0 || len(state.Identities) != 1 || state.Identities[0].AgentID != "local-agent" {
			t.Fatalf("local enrollment canonical state = %+v", state)
		}
	})

	t.Run("listener gets exact server identity", func(t *testing.T) {
		fixture := newPKIEnrollmentFixture(t)
		tokens := newPKIEnrollmentTokenService(t, fixture, sequencePKIID("listener-token"))
		issued, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: "agent-a", CreatedBy: "admin"})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		dnsNames := []string{"relay.example.test"}
		ipAddresses := []string{"192.0.2.10"}
		binding, err := newPKIIdentityBinding("domain-1", storage.PKIIdentityKindListener, "agent-a", "42", storage.PKICertificatePurposeServer, dnsNames, ipAddresses)
		if err != nil {
			t.Fatalf("newPKIIdentityBinding() error = %v", err)
		}
		enrollment := newPKIEnrollmentServiceForTest(t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("listener"))
		result, err := enrollment.Enroll(t.Context(), PKIEnrollRequest{
			Token: issued.Token, AgentID: "agent-a", Kind: storage.PKIIdentityKindListener, ListenerID: "42", Purpose: storage.PKICertificatePurposeServer,
			DNSNames: dnsNames, IPAddresses: ipAddresses, CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
		})
		if err != nil {
			t.Fatalf("Enroll(listener) error = %v", err)
		}
		assertPKIEnrollmentCertificateProfile(t, result.CertificatePEM, fixture.authorityCertificate, "spiffe://domain-1/agent/agent-a/listener/42", x509.ExtKeyUsageServerAuth, dnsNames, []net.IP{net.ParseIP(ipAddresses[0])})
	})
}

type pkiEnrollmentFixture struct {
	store                *storage.GormStore
	now                  time.Time
	authorityKey         *ecdsa.PrivateKey
	authorityCertificate *x509.Certificate
}

func newPKIEnrollmentFixture(t *testing.T) pkiEnrollmentFixture {
	t.Helper()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DataRoot: t.TempDir(), DSN: filepath.Join(t.TempDir(), "pki-enrollment.db"), LocalAgentID: "local-agent",
	})
	if err != nil {
		t.Fatalf("storage.NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close() error = %v", err)
		}
	})
	authorityKey := mustPKIEnrollmentKey(t)
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&authorityKey.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey() error = %v", err)
	}
	subjectKeyID := sha256.Sum256(publicKeyDER)
	serial := new(big.Int).SetBytes(append([]byte{0x80}, make([]byte, 15)...))
	authorityTemplate := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "NRE test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(2 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true, SubjectKeyId: append([]byte(nil), subjectKeyID[:20]...),
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	authorityDER, err := x509.CreateCertificate(rand.Reader, authorityTemplate, authorityTemplate, &authorityKey.PublicKey, authorityKey)
	if err != nil {
		t.Fatalf("CreateCertificate(authority) error = %v", err)
	}
	authorityCertificate, err := x509.ParseCertificate(authorityDER)
	if err != nil {
		t.Fatalf("ParseCertificate(authority) error = %v", err)
	}
	authorityFingerprint := sha256.Sum256(authorityCertificate.Raw)
	keyRef := "test-authority-key"
	err = store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		if err := tx.CreatePKISettings(t.Context(), storage.PKISettingsRow{
			PKIDomainID: "domain-1", CALifetimeSeconds: int64((10 * 365 * 24 * time.Hour) / time.Second),
			EndpointLifetimeSeconds: int64((90 * 24 * time.Hour) / time.Second), AuditRetentionDays: 365,
			PKIEpoch: 1, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.CreatePKIAuthority(t.Context(), storage.PKIAuthorityRow{
			ID: "authority-1", PKIDomainID: "domain-1", Generation: 1, Status: "active",
			CertificatePEM:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: authorityDER})),
			EncryptedKeyRef: &keyRef, FingerprintSHA256: hex.EncodeToString(authorityFingerprint[:]),
			NotBefore: authorityCertificate.NotBefore, NotAfter: authorityCertificate.NotAfter, CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		t.Fatalf("initialize PKI fixture error = %v", err)
	}
	return pkiEnrollmentFixture{store: store, now: now, authorityKey: authorityKey, authorityCertificate: authorityCertificate}
}

type pkiEnrollmentTestAuthoritySigner struct {
	key *ecdsa.PrivateKey
	err error
}

func (s *pkiEnrollmentTestAuthoritySigner) LoadSigner(context.Context, storage.PKIAuthorityRow) (crypto.Signer, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.key, nil
}

type singleReadReader struct {
	value []byte
	used  bool
}

func (r *singleReadReader) Read(target []byte) (int, error) {
	if r.used {
		return 0, errors.New("test random source exhausted")
	}
	r.used = true
	return copy(target, r.value), nil
}

func newPKIEnrollmentTokenService(t *testing.T, fixture pkiEnrollmentFixture, ids PKIIDGenerator) *PKITokenService {
	t.Helper()
	service, err := NewPKITokenService(PKITokenServiceOptions{
		Store: fixture.store, Clock: func() time.Time { return fixture.now }, Random: rand.Reader, NewID: ids,
	})
	if err != nil {
		t.Fatalf("NewPKITokenService() error = %v", err)
	}
	return service
}

func newPKIEnrollmentServiceForTest(t *testing.T, fixture pkiEnrollmentFixture, signer PKIEnrollmentAuthoritySigner, ids PKIIDGenerator) *PKIEnrollmentService {
	t.Helper()
	service, err := NewPKIEnrollmentService(PKIEnrollmentServiceOptions{
		Store: fixture.store, AuthoritySigner: signer, LocalAgentID: "local-agent",
		Clock: func() time.Time { return fixture.now }, Random: rand.Reader, NewID: ids,
	})
	if err != nil {
		t.Fatalf("NewPKIEnrollmentService() error = %v", err)
	}
	return service
}

func sequencePKIID(values ...string) PKIIDGenerator {
	var mutex sync.Mutex
	index := 0
	return func() (string, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if index >= len(values) {
			return "", errors.New("test ID sequence exhausted")
		}
		value := values[index]
		index++
		return value, nil
	}
}

func incrementingPKIID(prefix string) PKIIDGenerator {
	var mutex sync.Mutex
	value := 0
	return func() (string, error) {
		mutex.Lock()
		defer mutex.Unlock()
		value++
		return fmt.Sprintf("%s-%d", prefix, value), nil
	}
}

func mustPKIEnrollmentKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey() error = %v", err)
	}
	return key
}

func mustPKIEnrollmentAnonymousCSR(t *testing.T, key *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{SignatureAlgorithm: x509.ECDSAWithSHA256}, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest(anonymous) error = %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func mustPKIEnrollmentCSR(t *testing.T, key *ecdsa.PrivateKey, binding pkiIdentityBinding, anonymous bool) string {
	t.Helper()
	if anonymous {
		return mustPKIEnrollmentAnonymousCSR(t, key)
	}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: binding.URI.String()}, SignatureAlgorithm: x509.ECDSAWithSHA256,
		URIs: []*url.URL{binding.URI}, DNSNames: append([]string(nil), binding.DNSNames...), IPAddresses: clonePKIIPs(binding.IPAddresses),
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest() error = %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func loadPKIEnrollmentState(t *testing.T, store *storage.GormStore) storage.PKICanonicalState {
	t.Helper()
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatalf("LoadPKICanonicalState() error = %v", err)
	}
	return state
}

func findPKIEnrollmentCertificate(t *testing.T, state storage.PKICanonicalState, id string) storage.PKICertificateRow {
	t.Helper()
	for _, certificate := range state.Certificates {
		if certificate.ID == id {
			return certificate
		}
	}
	t.Fatalf("certificate %q not found", id)
	return storage.PKICertificateRow{}
}

func assertPKIEnrollmentTokenUnconsumed(t *testing.T, store *storage.GormStore, plaintext string) {
	t.Helper()
	digest, err := digestPKIEnrollmentToken(plaintext)
	if err != nil {
		t.Fatalf("digestPKIEnrollmentToken() error = %v", err)
	}
	state := loadPKIEnrollmentState(t, store)
	for _, token := range state.EnrollmentTokens {
		if token.TokenDigestSHA256 == digest {
			if token.ConsumedAt != nil {
				t.Fatalf("failed enrollment consumed token: %+v", token)
			}
			return
		}
	}
	t.Fatalf("enrollment token digest not found")
}

func assertPKIEnrollmentFactCounts(t *testing.T, store *storage.GormStore, identities, certificates, events int) {
	t.Helper()
	state := loadPKIEnrollmentState(t, store)
	if len(state.Identities) != identities || len(state.Certificates) != certificates || len(state.Events) != events {
		t.Fatalf("PKI fact counts = identities:%d certificates:%d events:%d, want %d/%d/%d", len(state.Identities), len(state.Certificates), len(state.Events), identities, certificates, events)
	}
}

func assertPKIEnrollmentCertificateProfile(t *testing.T, certificatePEM string, authority *x509.Certificate, identityURI string, usage x509.ExtKeyUsage, dnsNames []string, ipAddresses []net.IP) {
	t.Helper()
	block, rest := pem.Decode([]byte(certificatePEM))
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatalf("certificate PEM is malformed")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	if err := certificate.CheckSignatureFrom(authority); err != nil {
		t.Fatalf("certificate authority signature error = %v", err)
	}
	if certificate.Subject.CommonName != identityURI || len(certificate.URIs) != 1 || certificate.URIs[0].String() != identityURI {
		t.Fatalf("certificate identity = subject:%q URIs:%v", certificate.Subject.CommonName, certificate.URIs)
	}
	if len(certificate.ExtKeyUsage) != 1 || certificate.ExtKeyUsage[0] != usage || len(certificate.UnknownExtKeyUsage) != 0 {
		t.Fatalf("certificate EKU = %v unknown=%v", certificate.ExtKeyUsage, certificate.UnknownExtKeyUsage)
	}
	if !equalPKIStrings(certificate.DNSNames, dnsNames) || !equalPKIIPs(certificate.IPAddresses, ipAddresses) {
		t.Fatalf("certificate server SANs = DNS:%v IP:%v", certificate.DNSNames, certificate.IPAddresses)
	}
}
