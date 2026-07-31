package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKIBootstrapSeparatesTunnelPKIFromManagedCertificates(t *testing.T) {
	root := t.TempDir()
	store := newControlPKIStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatalf("OpenPKIVault() error = %v", err)
	}
	t.Cleanup(vault.Close)

	result, err := BootstrapInternalPKI(t.Context(), InternalPKIBootstrapOptions{Store: store, Vault: vault})
	if err != nil {
		t.Fatalf("BootstrapInternalPKI() error = %v", err)
	}
	if result.UpgradeState != PKIUpgradeStateTunnelMTLSOnly || result.PKIDomainID == "" || result.PKIEpoch != 1 {
		t.Fatalf("bootstrap result = %+v", result)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil || state.Settings == nil || len(state.Authorities) != 1 {
		t.Fatalf("canonical state = %+v, error = %v", state, err)
	}
	if state.Authorities[0].EncryptedKeyRef == nil || strings.Contains(state.Authorities[0].CertificatePEM, "PRIVATE KEY") {
		t.Fatalf("authority leaked or omitted encrypted key reference: %+v", state.Authorities[0])
	}

	relay := NewRelayListenerService(config.Config{DataDir: root, LocalAgentID: "local"}, store)
	if err := relay.Bootstrap(t.Context()); err != nil {
		t.Fatalf("relay Bootstrap() error = %v", err)
	}
	managed, err := store.ListManagedCertificates(t.Context())
	if err != nil || len(managed) != 0 {
		t.Fatalf("managed certificates after PKI bootstrap = %+v, error = %v", managed, err)
	}
}

func TestPKIPublicACMECertificateDomainRemainsAvailable(t *testing.T) {
	root := t.TempDir()
	store := newControlPKIStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	if _, err := BootstrapInternalPKI(t.Context(), InternalPKIBootstrapOptions{Store: store, Vault: vault}); err != nil {
		t.Fatal(err)
	}
	certificates := NewCertificateService(config.Config{DataDir: root, LocalAgentID: "local"}, store)
	if err := certificates.rejectCanonicalPKICertificateMutation(t.Context(), ManagedCertificate{
		Domain: "public.example.test", CertificateType: "acme", Usage: "https",
	}); err != nil {
		t.Fatalf("public ACME certificate was rejected: %v", err)
	}
	if err := certificates.rejectCanonicalPKICertificateMutation(t.Context(), ManagedCertificate{
		Domain: "relay.internal", CertificateType: "internal_ca", Usage: "relay_tunnel",
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("generic internal certificate error = %v", err)
	}
}

func TestTunnelMTLSUpgradePreservesControlAgentAndListenerAssociations(t *testing.T) {
	root := t.TempDir()
	store := newControlPKIStore(t, root)
	agent := storage.AgentRow{
		ID: "agent-a", Name: "edge A", AgentToken: "control-token-a", TagsJSON: `["edge"]`, CapabilitiesJSON: `["relay_quic"]`,
	}
	if err := store.SaveAgent(t.Context(), agent); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), agent.ID, []storage.RelayListenerRow{{
		ID: 42, AgentID: agent.ID, Name: "relay A", ListenHost: "0.0.0.0", ListenPort: 9443,
		PublicHost: "relay.example.test", PublicPort: 9443, Enabled: true, CertificateID: intPointer(7),
		TLSMode: "pin_only", PinSetJSON: `[{"type":"spki_sha256","value":"legacy"}]`, TagsJSON: `["critical"]`,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 7, Domain: "__relay-ca.internal", CertificateType: "internal_ca", Usage: "relay_ca", Status: "active",
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatalf("OpenPKIVault() error = %v", err)
	}
	t.Cleanup(vault.Close)
	result, err := BootstrapInternalPKI(t.Context(), InternalPKIBootstrapOptions{Store: store, Vault: vault})
	if err != nil {
		t.Fatalf("BootstrapInternalPKI() error = %v", err)
	}
	if result.UpgradeState != PKIUpgradeStateMigrationRequired {
		t.Fatalf("upgrade state = %q", result.UpgradeState)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Identities) != 2 {
		t.Fatalf("migration identities = %+v, want agent and listener", state.Identities)
	}

	relay := NewRelayListenerService(config.Config{DataDir: root, LocalAgentID: "local"}, store)
	leaseRepository, err := NewGormPKILeaseRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := NewPKILeaseService(PKILeaseServiceOptions{Repository: leaseRepository, InstanceID: "migration-control"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	pki := &InternalPKIService{store: store, lease: lease, activation: relay, clock: time.Now, random: rand.Reader}
	operation, err := pki.Activate(t.Context(), PKIActionRequest{Reason: "maintenance complete", ConfirmationNonce: "confirmed"})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if operation.State != storage.PKILifecycleJobStateSucceeded || operation.Phase != "completed" {
		t.Fatalf("activation operation = %+v", operation)
	}
	agents, _ := store.ListAgents(t.Context())
	if len(agents) != 1 || agents[0].ID != agent.ID || agents[0].AgentToken != agent.AgentToken || agents[0].Name != agent.Name || agents[0].TagsJSON != agent.TagsJSON {
		t.Fatalf("agent/control association changed: %+v", agents)
	}
	listeners, _ := store.ListRelayListeners(t.Context(), agent.ID)
	if len(listeners) != 1 || listeners[0].ID != 42 || listeners[0].Name != "relay A" || listeners[0].TagsJSON != `["critical"]` ||
		listeners[0].CertificateID != nil || listeners[0].TLSMode != "pki_mtls" || listeners[0].PinSetJSON != "[]" {
		t.Fatalf("listener migration result = %+v", listeners)
	}
	managed, _ := store.ListManagedCertificates(t.Context())
	if len(managed) != 0 {
		t.Fatalf("legacy internal managed certificates remain: %+v", managed)
	}
	state, _ = store.LoadPKICanonicalState(t.Context())
	if state.Settings == nil || state.Settings.UpgradeState != PKIUpgradeStateTunnelMTLSOnly || len(state.LifecycleJobs) != 1 {
		t.Fatalf("activation canonical state = %+v", state)
	}
}

func TestRelayPKICreateUsesCanonicalIdentityWithoutGenericCertificate(t *testing.T) {
	root := t.TempDir()
	store := newControlPKIStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	if _, err := BootstrapInternalPKI(t.Context(), InternalPKIBootstrapOptions{Store: store, Vault: vault}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "agent-a", Name: "edge A", AgentToken: "control-token"}); err != nil {
		t.Fatal(err)
	}
	relay := NewRelayListenerService(config.Config{DataDir: root, LocalAgentID: "local"}, store)
	name, host, publicHost, port := "relay A", "0.0.0.0", "relay.example.test", 9443
	listener, err := relay.createLegacy(t.Context(), "agent-a", RelayListenerInput{
		Name: &name, ListenHost: &host, ListenPort: &port, PublicHost: &publicHost, PublicPort: &port,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if listener.CertificateID != nil || listener.TLSMode != "pki_mtls" || listener.AllowSelfSigned || len(listener.PinSet) != 0 {
		t.Fatalf("PKI relay listener = %+v", listener)
	}
	managed, _ := store.ListManagedCertificates(t.Context())
	if len(managed) != 0 {
		t.Fatalf("relay create generated generic certificate: %+v", managed)
	}
	state, _ := store.LoadPKICanonicalState(t.Context())
	found := false
	for _, identity := range state.Identities {
		if identity.Kind == storage.PKIIdentityKindListener && identity.AgentID == "agent-a" && identity.ListenerID == "1" && identity.State == storage.PKIIdentityStateEnrollmentRequired {
			found = true
		}
	}
	if !found {
		t.Fatalf("listener PKI identity not created: %+v", state.Identities)
	}
}

func TestPKIEnrollAndBindAgentRollsBackStableControlRowWithToken(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	tokens := newPKIEnrollmentTokenService(t, fixture, sequencePKIID("bind-token"))
	issued, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeNewAgent, CreatedBy: "panel"})
	if err != nil {
		t.Fatal(err)
	}
	request := PKIEnrollRequest{
		Token: issued.Token, Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentAnonymousCSR(t, mustPKIEnrollmentKey(t)),
	}
	agent := storage.AgentRow{Name: "new edge", AgentToken: "new-control-token", TagsJSON: `["new"]`}
	failing := newPKIEnrollmentServiceForTest(t, fixture, &pkiEnrollmentTestAuthoritySigner{err: errors.New("sign failed")}, incrementingPKIID("failed-bind"))
	if _, err := failing.EnrollAndBindAgent(t.Context(), request, agent); err == nil {
		t.Fatal("EnrollAndBindAgent(sign failure) error = nil")
	}
	afterFailure := loadPKIEnrollmentState(t, fixture.store)
	if len(afterFailure.EnrollmentTokens) != 1 || afterFailure.EnrollmentTokens[0].ConsumedAt != nil {
		t.Fatalf("failed bind consumed token: %+v", afterFailure.EnrollmentTokens)
	}
	agents, _ := fixture.store.ListAgents(t.Context())
	if len(agents) != 1 || agents[0].ID != "agent-a" {
		t.Fatalf("failed bind persisted stable agent: %+v", agents)
	}

	success := newPKIEnrollmentServiceForTest(t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("successful-bind"))
	result, err := success.EnrollAndBindAgent(t.Context(), request, agent)
	if err != nil {
		t.Fatalf("EnrollAndBindAgent(retry) error = %v", err)
	}
	agents, _ = fixture.store.ListAgents(t.Context())
	found := false
	for _, row := range agents {
		if row.ID == result.AgentID && row.Name == agent.Name && row.AgentToken == agent.AgentToken {
			found = true
		}
	}
	if !found {
		t.Fatalf("successful atomic bind missing stable agent: result=%+v agents=%+v", result, agents)
	}
}

func TestPKIBoundReenrollmentPreservesExistingControlToken(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	if err := fixture.store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "agent-a", Name: "stable agent A", AgentToken: "existing-control-token", TagsJSON: `[]`,
	}); err != nil {
		t.Fatal(err)
	}
	tokens := newPKIEnrollmentTokenService(t, fixture, sequencePKIID("bound-token"))
	issued, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{
		Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: "agent-a", CreatedBy: "panel",
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := newPKIIdentityBinding(
		"domain-1", storage.PKIIdentityKindAgent, "agent-a", "", storage.PKICertificatePurposeClient, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := newPKIEnrollmentServiceForTest(
		t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("bound-reenroll"),
	)
	result, err := enrollment.EnrollAndBindAgent(t.Context(), PKIEnrollRequest{
		Token: issued.Token, AgentID: "agent-a", Kind: storage.PKIIdentityKindAgent,
		Purpose: storage.PKICertificatePurposeClient,
		CSRPEM:  mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
	}, storage.AgentRow{ID: "agent-a", Name: "updated agent A", AgentToken: "replacement-control-token", TagsJSON: `[]`})
	if err != nil {
		t.Fatalf("EnrollAndBindAgent() error = %v", err)
	}
	if result.AgentControlToken != "existing-control-token" {
		t.Fatalf("returned control token = %q", result.AgentControlToken)
	}
	agents, err := fixture.store.ListAgents(t.Context())
	if err != nil || len(agents) != 1 || agents[0].AgentToken != "existing-control-token" || agents[0].Name != "updated agent A" {
		t.Fatalf("stable agent after re-enrollment = %+v, error = %v", agents, err)
	}
}

func TestPKIRegistrationSnapshotFailureLeavesEnrollmentTokenUnconsumed(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	tokens := newPKIEnrollmentTokenService(t, fixture, sequencePKIID("snapshot-failure-token"))
	issued, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{Scope: PKIEnrollmentTokenScopeNewAgent, CreatedBy: "panel"})
	if err != nil {
		t.Fatal(err)
	}
	enrollment := newPKIEnrollmentServiceForTest(
		t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("snapshot-failure"),
	)
	pki := &InternalPKIService{
		store: fixture.store, enrollment: enrollment,
		snapshotSigner: pkiControlSnapshotErrorSigner{err: ErrPKILeaseNotHeld},
		clock:          func() time.Time { return fixture.now },
	}
	_, err = pki.RegisterAgent(t.Context(), RegisterRequest{
		Name: "new edge", RegisterToken: issued.Token,
		TunnelCSRPEM: mustPKIEnrollmentAnonymousCSR(t, mustPKIEnrollmentKey(t)),
	}, storage.AgentRow{Name: "new edge", AgentToken: "new-control-token", TagsJSON: `[]`})
	if !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	state := loadPKIEnrollmentState(t, fixture.store)
	if len(state.EnrollmentTokens) != 1 || state.EnrollmentTokens[0].ConsumedAt != nil || len(state.Identities) != 0 || len(state.Certificates) != 0 {
		t.Fatalf("failed registration committed PKI state: %+v", state)
	}
	agents, listErr := fixture.store.ListAgents(t.Context())
	if listErr != nil || len(agents) != 1 || agents[0].ID != "agent-a" {
		t.Fatalf("failed registration committed stable agent: %+v, error = %v", agents, listErr)
	}
}

func TestPKIControlSyncKeepsPlainHeartbeatAvailableWithoutSigner(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	pki := &InternalPKIService{
		store: fixture.store, snapshotSigner: pkiControlSnapshotErrorSigner{err: ErrPKILeaseNotHeld},
		clock: func() time.Time { return fixture.now },
	}
	snapshot, credentials, err := pki.ControlSync(t.Context(), "agent-a", nil, nil)
	if err != nil || snapshot.PKIDomainID != "" || len(credentials) != 0 {
		t.Fatalf("ControlSync() snapshot=%+v credentials=%+v error=%v", snapshot, credentials, err)
	}
}

func TestPKISecuritySnapshotSignatureBindsTrustRootMetadata(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	signer, err := NewPKIVaultSecuritySnapshotSigner(PKIVaultSecuritySnapshotSignerOptions{
		StateSource: fixture.store,
		Signer:      &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.SignPKISecuritySnapshot(t.Context(), PKIUnsignedSecuritySnapshot{
		PKIDomainID: "domain-1",
		Version: PKISecuritySnapshotVersion{
			Version: PKISecurityVersion{PKIEpoch: 1, SecurityRevision: 0}, Full: true,
		},
		IssuedAt: fixture.now, TrustGenerations: []int64{1},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := marshalPKIUnsignedSecuritySnapshot(signed.PKIUnsignedSecuritySnapshot)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(&fixture.authorityKey.PublicKey, digest[:], signed.Signature) {
		t.Fatal("canonical snapshot signature did not verify")
	}
	tampered := signed.PKIUnsignedSecuritySnapshot
	tampered.TrustRoots = slices.Clone(tampered.TrustRoots)
	tampered.TrustRoots[0].FingerprintSHA256 = strings.Repeat("b", 64)
	tamperedPayload, err := marshalPKIUnsignedSecuritySnapshot(tampered)
	if err != nil {
		t.Fatal(err)
	}
	tamperedDigest := sha256.Sum256(tamperedPayload)
	if ecdsa.VerifyASN1(&fixture.authorityKey.PublicKey, tamperedDigest[:], signed.Signature) {
		t.Fatal("snapshot signature accepted tampered trust-root fingerprint")
	}
}

func TestPKIBootstrapDoesNotDecryptAuthorityBeforeLease(t *testing.T) {
	root := t.TempDir()
	store := newControlPKIStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	if _, err := BootstrapInternalPKI(t.Context(), InternalPKIBootstrapOptions{Store: store, Vault: vault}); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil || len(state.Authorities) != 1 || state.Authorities[0].EncryptedKeyRef == nil {
		t.Fatalf("canonical authority = %+v, error = %v", state.Authorities, err)
	}
	keyPath := filepath.Join(vault.vaultDir, *state.Authorities[0].EncryptedKeyRef)
	if err := os.WriteFile(keyPath, []byte("corrupt-encrypted-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapInternalPKI(t.Context(), InternalPKIBootstrapOptions{Store: store, Vault: vault}); err != nil {
		t.Fatalf("public bootstrap validation decrypted CA key: %v", err)
	}
	signer, err := NewPKIVaultAuthoritySigner(vault)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.LoadSigner(t.Context(), state.Authorities[0]); err == nil {
		t.Fatal("LoadSigner() error = nil for corrupt encrypted CA key")
	}
}

func TestAgentAuthenticatedControlSyncEnrollsListenerCSR(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	if err := fixture.store.SaveAgent(t.Context(), storage.AgentRow{ID: "agent-a", Name: "stable agent A", AgentToken: "control-token-a"}); err != nil {
		t.Fatal(err)
	}
	binding, err := newPKIIdentityBinding(
		"domain-1", storage.PKIIdentityKindListener, "agent-a", "42", storage.PKICertificatePurposeServer,
		[]string{"relay.example.test"}, []string{"192.0.2.42"},
	)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := newPKIEnrollmentServiceForTest(t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("control-sync"))
	snapshotSigner, err := NewPKIVaultSecuritySnapshotSigner(PKIVaultSecuritySnapshotSignerOptions{
		StateSource: fixture.store,
		Signer:      &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	pki := &InternalPKIService{
		store: fixture.store, enrollment: enrollment, snapshotSigner: snapshotSigner,
		clock: func() time.Time { return fixture.now },
	}
	snapshot, credentials, err := pki.ControlSync(t.Context(), "agent-a", nil, []PKIControlEnrollmentRequest{{
		RequestID: "listener-42-generation-1", Kind: storage.PKIIdentityKindListener, ListenerID: "42",
		Purpose:  storage.PKICertificatePurposeServer,
		CSRPEM:   mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
		DNSNames: []string{"relay.example.test"}, IPAddresses: []string{"192.0.2.42"},
	}})
	if err != nil {
		t.Fatalf("ControlSync() error = %v", err)
	}
	if snapshot.PKIDomainID != "domain-1" || len(credentials) != 1 || credentials[0].RequestID != "listener-42-generation-1" ||
		credentials[0].Credential.Purpose != storage.PKICertificatePurposeServer || strings.Contains(credentials[0].Credential.CertificatePEM, "PRIVATE KEY") {
		t.Fatalf("control sync result: snapshot=%+v credentials=%+v", snapshot, credentials)
	}
	state := loadPKIEnrollmentState(t, fixture.store)
	if len(state.Identities) != 1 || state.Identities[0].AgentID != "agent-a" || state.Identities[0].ListenerID != "42" {
		t.Fatalf("listener identity = %+v", state.Identities)
	}
}

func TestPKIProductionRevocationRepositoryFencesAndCommitsControlDisable(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	if err := fixture.store.SaveAgent(t.Context(), storage.AgentRow{ID: "agent-a", Name: "stable agent A", AgentToken: "control-token-a"}); err != nil {
		t.Fatal(err)
	}
	tokens := newPKIEnrollmentTokenService(t, fixture, sequencePKIID("revoke-token"))
	issued, err := tokens.Create(t.Context(), PKIEnrollmentTokenRequest{
		Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: "agent-a", CreatedBy: "panel",
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := newPKIIdentityBinding("domain-1", storage.PKIIdentityKindAgent, "agent-a", "", storage.PKICertificatePurposeClient, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := newPKIEnrollmentServiceForTest(t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("revoke-enroll"))
	enrolled, err := enrollment.Enroll(t.Context(), PKIEnrollRequest{
		Token: issued.Token, AgentID: "agent-a", Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	leaseRepository, err := NewGormPKILeaseRepository(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := NewPKILeaseService(PKILeaseServiceOptions{
		Repository: leaseRepository, InstanceID: "control-a", Clock: func() time.Time { return fixture.now }, Random: rand.Reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := lease.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewGormPKIRevocationRepository(GormPKIRevocationRepositoryOptions{Store: fixture.store, Clock: func() time.Time { return fixture.now }})
	if err != nil {
		t.Fatal(err)
	}
	badGrant := grant
	badGrant.LeaseTerm = strings.Repeat("f", 64)
	if _, err := repository.RevokePKIIdentityAtomically(t.Context(), PKIRevocationMutation{
		Request: PKIRevocationRequest{IdentityID: enrolled.IdentityID, Reason: "fenced", Source: "test"}, Lease: badGrant,
	}, func(_ context.Context, facts PKIRevocationFacts) (PKISignedSecuritySnapshot, error) {
		return pkiRevocationTestSigner{}.SignPKISecuritySnapshot(t.Context(), PKIUnsignedSecuritySnapshot{PKIDomainID: facts.PKIDomainID})
	}); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("stale fence error = %v", err)
	}
	before := loadPKIEnrollmentState(t, fixture.store)
	if before.Settings.SecurityRevision != 0 || before.Identities[0].State == storage.PKIIdentityStateRevoked {
		t.Fatalf("stale fence mutated state: %+v", before)
	}

	publisher := &pkiRevocationTestPublisher{}
	closer := &pkiRevocationTestCloser{}
	revocation, err := NewPKIRevocationService(PKIRevocationServiceOptions{
		Repository: repository, Signer: pkiRevocationTestSigner{}, Publisher: publisher, Closer: closer,
		Lease: lease, Clock: func() time.Time { return fixture.now }, Convergence: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := revocation.Revoke(t.Context(), PKIRevocationRequest{IdentityID: enrolled.IdentityID, Reason: "compromised", Source: "panel", OperatorID: "admin"})
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if !commit.ControlTokenDisabled || commit.Facts.SecurityRevision != 1 || !publisher.called || !closer.called {
		t.Fatalf("revocation commit = %+v publisher=%v closer=%v", commit, publisher.called, closer.called)
	}
	after := loadPKIEnrollmentState(t, fixture.store)
	if after.Settings.SecurityRevision != 1 || after.Identities[0].State != storage.PKIIdentityStateRevoked ||
		after.Certificates[0].Status != storage.PKICertificateStatusRevoked || len(after.LifecycleJobs) != 1 {
		t.Fatalf("revoked canonical state = %+v", after)
	}
	agents, _ := fixture.store.ListAgents(t.Context())
	if len(agents) != 1 || agents[0].AgentToken != "" {
		t.Fatalf("revoked agent token remained enabled: %+v", agents)
	}
}

func newControlPKIStore(t *testing.T, root string) *storage.GormStore {
	t.Helper()
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DataRoot: root, DSN: filepath.Join(root, "panel.db"), LocalAgentID: "local",
	})
	if err != nil {
		t.Fatalf("storage.NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func intPointer(value int) *int { return &value }

type pkiControlSnapshotErrorSigner struct{ err error }

func (s pkiControlSnapshotErrorSigner) SignPKISecuritySnapshot(context.Context, PKIUnsignedSecuritySnapshot) (PKISignedSecuritySnapshot, error) {
	return PKISignedSecuritySnapshot{}, s.err
}
