//go:build integration

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

	bootstrap := bootstrapInternalPKIForControlTest(t, store, vault)
	result := bootstrap.result
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
	snapshot, err := store.LoadAgentSnapshot(t.Context(), "local", storage.AgentSnapshotInput{})
	if err != nil || snapshot.PKISecurity == nil || snapshot.PKISecurity.PKIDomainID != result.PKIDomainID ||
		snapshot.PKISecurity.PKIEpoch != 1 || !snapshot.PKISecurity.Full {
		t.Fatalf("initial revision snapshot PKI security = %+v, error=%v", snapshot.PKISecurity, err)
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
	bootstrapInternalPKIForControlTest(t, store, vault)
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

func TestManagedCertificateBackgroundCompletionRechecksCanonicalPKIOwnership(t *testing.T) {
	root := t.TempDir()
	store := newControlPKIStore(t, root)
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrapInternalPKIForControlTest(t, store, vault)

	const domain = "legacy-relay-order.example.test"
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 71, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["local"]`, Status: "issuing", CertificateType: "acme", Usage: "relay_tunnel", Revision: 3,
	}}); err != nil {
		t.Fatal(err)
	}
	issued := mustCreateSelfSignedCA(t, domain)
	material := storage.ManagedCertificateBundle{Domain: domain, CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM}
	service := NewCertificateService(config.Config{DataDir: root, EnableLocalAgent: true, LocalAgentID: "local"}, store)
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current, index, found := findManagedCertificateByID(rows, 71)
	if !found {
		t.Fatal("managed certificate 71 not found")
	}
	_, err = service.persistManagedCertificateIssueSuccess(t.Context(), rows, index, current, managedCertificateRenewalResult{
		Changed: true, LastIssueAt: time.Now().UTC().Format(time.RFC3339),
		MaterialHash: hashManagedCertificateMaterial(material.CertPEM, material.KeyPEM),
	}, material)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("persistManagedCertificateIssueSuccess(after activation) error = %v", err)
	}
	rows, err = store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if persisted := managedCertificateFromRow(rows[0]); persisted.Status != "issuing" || persisted.MaterialHash != "" {
		t.Fatalf("legacy relay certificate was finalized after activation: %+v", persisted)
	}
	if _, found, err := store.LoadManagedCertificateMaterial(t.Context(), domain); err != nil || found {
		t.Fatalf("legacy relay material found=%t error=%v", found, err)
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
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{
		{ID: 7, Domain: "__relay-ca.internal", CertificateType: "internal_ca", Usage: "relay_ca", Status: "active"},
		{ID: 8, Domain: "private-app.example.test", CertificateType: "internal_ca", Usage: "https", Status: "active"},
	}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatalf("OpenPKIVault() error = %v", err)
	}
	t.Cleanup(vault.Close)
	bootstrap := bootstrapInternalPKIForControlTest(t, store, vault)
	result := bootstrap.result
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
	beforeActivation, err := store.ListRelayListeners(t.Context(), agent.ID)
	if err != nil || len(beforeActivation) != 1 || beforeActivation[0].TLSMode != "pin_only" || beforeActivation[0].CertificateID == nil || *beforeActivation[0].CertificateID != 7 {
		t.Fatalf("listener switched authentication before activation: %+v, error=%v", beforeActivation, err)
	}
	migrationSnapshot, err := store.LoadAgentSnapshot(t.Context(), agent.ID, storage.AgentSnapshotInput{})
	if err != nil {
		t.Fatal(err)
	}
	migrationProjection, err := (&InternalPKIService{store: store}).PrepareRelayListeners(t.Context(), agent.ID, migrationSnapshot.RelayListeners)
	if err != nil || len(migrationProjection) != 1 || migrationProjection[0].TLSMode != "pin_only" || migrationProjection[0].CertificateID == nil {
		t.Fatalf("migration control snapshot switched relay authentication: %+v, error=%v", migrationProjection, err)
	}

	relay := NewRelayListenerService(config.Config{DataDir: root, LocalAgentID: "local"}, store)
	activationFailure := errors.New("injected activation commit failure")
	if err := relay.FinalizeTunnelMTLSUpgrade(t.Context(), "activation-rollback", func(context.Context, *storage.GormStore) error {
		return activationFailure
	}, nil); !errors.Is(err, activationFailure) {
		t.Fatalf("FinalizeTunnelMTLSUpgrade(rollback) error = %v", err)
	}
	afterRollback, err := store.ListRelayListeners(t.Context(), agent.ID)
	if err != nil || len(afterRollback) != 1 || afterRollback[0].TLSMode != "pin_only" || afterRollback[0].CertificateID == nil || *afterRollback[0].CertificateID != 7 {
		t.Fatalf("failed activation partially changed listener: %+v, error=%v", afterRollback, err)
	}
	certificatesAfterRollback, err := store.ListManagedCertificates(t.Context())
	if err != nil || len(certificatesAfterRollback) != 2 {
		t.Fatalf("failed activation partially removed certificates: %+v, error=%v", certificatesAfterRollback, err)
	}
	tokenService, err := NewPKITokenService(PKITokenServiceOptions{Store: store, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	enrollmentService, err := NewPKIEnrollmentService(PKIEnrollmentServiceOptions{
		Store: store, Lease: bootstrap.lease, AuthoritySigner: bootstrap.authoritySigner, LocalAgentID: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	boundToken, err := tokenService.Create(t.Context(), PKIEnrollmentTokenRequest{
		Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: agent.ID, CreatedBy: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	agentBinding, err := newPKIIdentityBinding(
		result.PKIDomainID, storage.PKIIdentityKindAgent, agent.ID, "", storage.PKICertificatePurposeClient, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	agentCredential, err := enrollmentService.EnrollAndBindAgent(t.Context(), PKIEnrollRequest{
		RequestID: "activation-agent", Token: boundToken.Token, AgentID: agent.ID,
		Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), agentBinding, false),
	}, agent)
	if err != nil {
		t.Fatalf("enroll activation agent: %v", err)
	}
	listenerDNSNames := []string{"relay.example.test"}
	listenerBinding, err := newPKIIdentityBinding(
		result.PKIDomainID, storage.PKIIdentityKindListener, agent.ID, "42", storage.PKICertificatePurposeServer,
		listenerDNSNames, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enrollmentService.EnrollAuthenticated(t.Context(), agent.ID, agent.AgentToken, PKIEnrollRequest{
		RequestID: "activation-listener", Kind: storage.PKIIdentityKindListener, ListenerID: "42",
		Purpose: storage.PKICertificatePurposeServer, DNSNames: listenerDNSNames,
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), listenerBinding, false),
	}); err != nil {
		t.Fatalf("enroll activation listener: %v", err)
	}
	pki := &InternalPKIService{
		store: store, lease: bootstrap.lease, activation: relay, snapshotSigner: bootstrap.snapshotSigner,
		clock: time.Now, random: rand.Reader,
	}
	if _, err := pki.SecuritySnapshot(t.Context(), agent.ID, &storage.PKISecurityAcknowledgement{
		PKIDomainID: result.PKIDomainID, PKIEpoch: result.PKIEpoch, SecurityRevision: 0,
		Full: true, CertificateID: agentCredential.CertificateID, TrustGenerations: []int64{1},
	}); err != nil {
		t.Fatalf("acknowledge activation security snapshot: %v", err)
	}
	confirmation, err := pki.IssueConfirmationNonce(t.Context(), PKIConfirmationRequest{Action: "activate"})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := pki.Activate(t.Context(), PKIActionRequest{Reason: "maintenance complete", ConfirmationNonce: confirmation.Nonce})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if operation.State != storage.PKILifecycleJobStateRunning || operation.Phase != "awaiting_revision_apply" {
		t.Fatalf("activation operation = %+v", operation)
	}
	revisions, err := store.ListAgentRevisions(t.Context(), agent.ID)
	if err != nil || len(revisions) == 0 || revisions[len(revisions)-1].OperationID != "activate-1" || revisions[len(revisions)-1].State != "pending" {
		t.Fatalf("activation revisions = %+v, error = %v", revisions, err)
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
	if len(managed) != 1 || managed[0].ID != 8 || managed[0].Usage != "https" {
		t.Fatalf("activation removed non-relay internal certificate or retained relay material: %+v", managed)
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
	bootstrapInternalPKIForControlTest(t, store, vault)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "agent-a", Name: "edge A", AgentToken: "control-token"}); err != nil {
		t.Fatal(err)
	}
	relay := NewRelayListenerService(config.Config{DataDir: root, LocalAgentID: "local"}, store)
	invalidName, wildcardHost, invalidPort := "invalid relay", "0.0.0.0", 9442
	if _, err := relay.createLegacy(t.Context(), "agent-a", RelayListenerInput{
		Name: &invalidName, ListenHost: &wildcardHost, ListenPort: &invalidPort,
	}); !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "concrete public_host or bind_host") {
		t.Fatalf("Create(wildcard without certificate endpoint) error = %v", err)
	}
	if rows, err := store.ListRelayListeners(t.Context(), "agent-a"); err != nil || len(rows) != 0 {
		t.Fatalf("invalid PKI relay persisted rows = %+v, error=%v", rows, err)
	}
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
	unspecifiedPublicHost := "0.0.0.0"
	wildcardBindHosts := []string{"0.0.0.0"}
	if _, err := relay.updateLegacy(t.Context(), "agent-a", listener.ID, RelayListenerInput{
		BindHosts: &wildcardBindHosts, ListenHost: &host, PublicHost: &unspecifiedPublicHost,
	}); !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "concrete public_host or bind_host") {
		t.Fatalf("Update(wildcard without certificate endpoint) error = %v", err)
	}
	rows, err := store.ListRelayListeners(t.Context(), "agent-a")
	if err != nil || len(rows) != 1 || rows[0].PublicHost != publicHost {
		t.Fatalf("invalid PKI relay update changed persisted row = %+v, error=%v", rows, err)
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

func TestTunnelMTLSActivationRejectsLegacyListenerWithoutCertificateEndpoint(t *testing.T) {
	root := t.TempDir()
	store := newControlPKIStore(t, root)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "agent-a", Name: "edge A", AgentToken: "control-token"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRelayListeners(t.Context(), "agent-a", []storage.RelayListenerRow{{
		ID: 42, AgentID: "agent-a", Name: "legacy wildcard relay", BindHostsJSON: `["0.0.0.0"]`,
		ListenHost: "0.0.0.0", ListenPort: 9443, PublicPort: 9443, Enabled: true, TLSMode: "pin_only",
	}}); err != nil {
		t.Fatal(err)
	}
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vault.Close)
	bootstrapInternalPKIForControlTest(t, store, vault)

	err = store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		settings, found, err := tx.GetPKISettings(t.Context())
		if err != nil {
			return err
		}
		if !found {
			return errors.New("PKI settings not found")
		}
		_, err = validateTunnelMTLSActivationGate(t.Context(), store, tx, settings, time.Now().UTC())
		return err
	})
	if !errors.Is(err, ErrPKILifecycleConflict) || !strings.Contains(err.Error(), "no concrete certificate endpoint") {
		t.Fatalf("activation gate error = %v", err)
	}
}

func TestRelayPKICreateRejectsReusingRevokedListenerID(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	store := fixture.store
	if err := store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.CreatePKIIdentity(t.Context(), storage.PKIIdentityRow{
			ID: "retired-listener-identity", PKIDomainID: "domain-1",
			Kind: storage.PKIIdentityKindListener, AgentID: "agent-a", ListenerID: "1",
			State: storage.PKIIdentityStateRevoked, CreatedAt: fixture.now, UpdatedAt: fixture.now,
		})
	}); err != nil {
		t.Fatal(err)
	}

	relay := NewRelayListenerService(config.Config{DataDir: t.TempDir(), LocalAgentID: "local-agent"}, store)
	name, host, publicHost, port := "replacement", "0.0.0.0", "relay.example.test", 9443
	enabled := false
	preferredID := 1
	if _, err := relay.Create(t.Context(), "agent-a", RelayListenerInput{
		ID: &preferredID, Name: &name, ListenHost: &host, ListenPort: &port,
		PublicHost: &publicHost, PublicPort: &port, Enabled: &enabled,
	}); !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "cannot be reused") {
		t.Fatalf("Create(revoked listener ID) error = %v", err)
	}
	listeners, err := store.ListRelayListeners(t.Context(), "agent-a")
	if err != nil || len(listeners) != 0 {
		t.Fatalf("revoked listener ID create persisted rows = %+v, error = %v", listeners, err)
	}
	automaticName, automaticHost, automaticPublicHost, automaticPort := "automatic replacement", "0.0.0.0", "automatic.example.test", 9444
	automatic, err := relay.Create(t.Context(), "agent-a", RelayListenerInput{
		Name: &automaticName, ListenHost: &automaticHost, ListenPort: &automaticPort,
		PublicHost: &automaticPublicHost, PublicPort: &automaticPort, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("Create(automatic replacement) error = %v", err)
	}
	if automatic.ID == preferredID {
		t.Fatalf("automatic replacement reused revoked listener ID %d", automatic.ID)
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
		RequestID: "bind-agent", Token: issued.Token, Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
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
	if len(agents) != 2 {
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
		RequestID: "bound-reenroll", Token: issued.Token, AgentID: "agent-a", Kind: storage.PKIIdentityKindAgent,
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
	if err != nil || len(agents) != 2 || agents[0].AgentToken != "existing-control-token" || agents[0].Name != "updated agent A" {
		t.Fatalf("stable agent after re-enrollment = %+v, error = %v", agents, err)
	}
}

func TestPKIRegistrationSnapshotFailureReplaysCommittedEnrollment(t *testing.T) {
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
		store: fixture.store, lease: pkiStaticLeaseGate{}, enrollment: enrollment,
		snapshotSigner: pkiControlSnapshotErrorSigner{err: ErrPKILeaseNotHeld},
		clock:          func() time.Time { return fixture.now },
	}
	request := RegisterRequest{
		Name: "new edge", RegisterToken: issued.Token,
		PKIEnrollmentRequestID: "registration-response-replay-1",
		TunnelCSRPEM:           mustPKIEnrollmentAnonymousCSR(t, mustPKIEnrollmentKey(t)),
	}
	agent := storage.AgentRow{Name: "new edge", AgentToken: "new-control-token", TagsJSON: `[]`}
	_, err = pki.RegisterAgent(t.Context(), request, agent)
	if !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	state := loadPKIEnrollmentState(t, fixture.store)
	if len(state.EnrollmentTokens) != 1 || state.EnrollmentTokens[0].ConsumedAt == nil || len(state.Identities) != 1 ||
		len(state.Certificates) != 1 || len(state.EnrollmentReplays) != 1 {
		t.Fatalf("response-loss-safe enrollment state = %+v", state)
	}
	agents, listErr := fixture.store.ListAgents(t.Context())
	if listErr != nil || len(agents) != 3 {
		t.Fatalf("committed stable agents = %+v, error = %v", agents, listErr)
	}
	validSigner, signerErr := NewPKIVaultSecuritySnapshotSigner(PKIVaultSecuritySnapshotSignerOptions{
		StateSource: fixture.store, Signer: &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey},
	})
	if signerErr != nil {
		t.Fatal(signerErr)
	}
	pki.snapshotSigner = validSigner
	replayed, err := pki.RegisterAgent(t.Context(), request, agent)
	if err != nil {
		t.Fatalf("RegisterAgent(replay) error = %v", err)
	}
	if replayed.AgentToken != "new-control-token" || replayed.TunnelCredential.CertificateID != state.Certificates[0].ID ||
		replayed.SecuritySnapshot.PKIDomainID != "domain-1" {
		t.Fatalf("replayed registration = %+v", replayed)
	}
	afterReplay := loadPKIEnrollmentState(t, fixture.store)
	if len(afterReplay.Identities) != 1 || len(afterReplay.Certificates) != 1 || len(afterReplay.EnrollmentReplays) != 1 {
		t.Fatalf("registration replay duplicated canonical facts: %+v", afterReplay)
	}
}

func TestPKIControlSyncReturnsSnapshotSignerFailureForHeartbeatIsolation(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	pki := &InternalPKIService{
		store: fixture.store, lease: pkiStaticLeaseGate{}, snapshotSigner: pkiControlSnapshotErrorSigner{err: ErrPKILeaseNotHeld},
		clock: func() time.Time { return fixture.now },
	}
	snapshot, credentials, err := pki.ControlSync(t.Context(), "agent-a", nil, nil)
	if !errors.Is(err, ErrPKILeaseNotHeld) || snapshot.PKIDomainID != "" || len(credentials) != 0 {
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
	bootstrap := bootstrapInternalPKIForControlTest(t, store, vault)
	state, err := store.LoadPKICanonicalState(t.Context())
	if err != nil || len(state.Authorities) != 1 || state.Authorities[0].EncryptedKeyRef == nil {
		t.Fatalf("canonical authority = %+v, error = %v", state.Authorities, err)
	}
	keyPath := filepath.Join(vault.vaultDir, *state.Authorities[0].EncryptedKeyRef)
	if err := os.WriteFile(keyPath, []byte("corrupt-encrypted-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BootstrapInternalPKI(t.Context(), InternalPKIBootstrapOptions{
		Store: store, Vault: vault, Lease: bootstrap.lease, SnapshotSigner: bootstrap.snapshotSigner,
	}); err != nil {
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
	if err := fixture.store.SaveRelayListeners(t.Context(), "agent-a", []storage.RelayListenerRow{{
		ID: 42, AgentID: "agent-a", Name: "relay 42", BindHostsJSON: `["192.0.2.42"]`,
		ListenHost: "0.0.0.0", ListenPort: 7443, PublicHost: "relay.example.test", PublicPort: 7443,
		Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.CreatePKIIdentity(t.Context(), storage.PKIIdentityRow{
			ID: "listener-identity-42", PKIDomainID: "domain-1", Kind: storage.PKIIdentityKindListener,
			AgentID: "agent-a", ListenerID: "42", State: storage.PKIIdentityStateEnrollmentRequired,
			CreatedAt: fixture.now, UpdatedAt: fixture.now,
		})
	}); err != nil {
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
		store: fixture.store, lease: pkiStaticLeaseGate{}, enrollment: enrollment, snapshotSigner: snapshotSigner,
		clock: func() time.Time { return fixture.now },
	}
	validRequest := PKIControlEnrollmentRequest{
		RequestID: "listener-42-generation-1", Kind: storage.PKIIdentityKindListener, ListenerID: "42",
		Purpose:  storage.PKICertificatePurposeServer,
		CSRPEM:   mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
		DNSNames: []string{"relay.example.test"}, IPAddresses: []string{"192.0.2.42"}, controlToken: "control-token-a",
	}
	snapshot, credentials, err := pki.ControlSync(t.Context(), "agent-a", nil, []PKIControlEnrollmentRequest{
		validRequest,
		{RequestID: "missing-listener", Kind: storage.PKIIdentityKindListener, ListenerID: "99", Purpose: storage.PKICertificatePurposeServer, CSRPEM: validRequest.CSRPEM, controlToken: "control-token-a"},
	})
	if err != nil {
		t.Fatalf("ControlSync() error = %v", err)
	}
	if snapshot.PKIDomainID != "domain-1" || len(credentials) != 2 || credentials[0].RequestID != "listener-42-generation-1" ||
		credentials[0].Credential.Purpose != storage.PKICertificatePurposeServer || strings.Contains(credentials[0].Credential.CertificatePEM, "PRIVATE KEY") ||
		credentials[1].RequestID != "missing-listener" || credentials[1].Error == "" {
		t.Fatalf("control sync result: snapshot=%+v credentials=%+v", snapshot, credentials)
	}
	replayedSnapshot, replayedCredentials, err := pki.ControlSync(t.Context(), "agent-a", nil, []PKIControlEnrollmentRequest{validRequest})
	if err != nil || replayedSnapshot.PKIDomainID != snapshot.PKIDomainID || len(replayedCredentials) != 1 ||
		replayedCredentials[0].Credential.CertificateID != credentials[0].Credential.CertificateID {
		t.Fatalf("control sync replay: snapshot=%+v credentials=%+v error=%v", replayedSnapshot, replayedCredentials, err)
	}
	state := loadPKIEnrollmentState(t, fixture.store)
	if len(state.Identities) != 1 || state.Identities[0].AgentID != "agent-a" || state.Identities[0].ListenerID != "42" ||
		state.Identities[0].State != storage.PKIIdentityStateActive {
		t.Fatalf("listener identity = %+v", state.Identities)
	}
	repository, err := NewGormPKIRevocationRepository(GormPKIRevocationRepositoryOptions{
		Store: fixture.store, Clock: func() time.Time { return fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	publisher := &pkiRevocationTestPublisher{}
	closer := &pkiRevocationTestCloser{}
	revocation, err := NewPKIRevocationService(PKIRevocationServiceOptions{
		Repository: repository, Signer: snapshotSigner, Publisher: publisher, Closer: closer,
		Lease: pkiStaticLeaseGate{}, Clock: func() time.Time { return fixture.now }, Convergence: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	pki.revocation = revocation
	relay := NewRelayListenerService(config.Config{DataDir: t.TempDir(), LocalAgentID: "local-agent"}, fixture.store)
	relay.SetPKIListenerRevoker(pki.RevokeListenerForDeletion)
	if _, err := relay.Delete(t.Context(), "agent-a", 42); err != nil {
		t.Fatalf("Delete(listener with active PKI credential) error = %v", err)
	}
	state = loadPKIEnrollmentState(t, fixture.store)
	if len(state.Identities) != 1 || state.Identities[0].State != storage.PKIIdentityStateRevoked ||
		len(state.Certificates) != 1 || state.Certificates[0].Status != storage.PKICertificateStatusRevoked ||
		state.Settings.SecurityRevision != 1 || !publisher.called || !closer.called {
		t.Fatalf("listener deletion did not converge revocation: %+v publisher=%v closer=%v", state, publisher.called, closer.called)
	}
	preferredID, replacementName, replacementHost, replacementPublicHost, replacementPort := 42, "replacement", "0.0.0.0", "replacement.example.test", 7443
	if _, err := relay.Create(t.Context(), "agent-a", RelayListenerInput{
		ID: &preferredID, Name: &replacementName, ListenHost: &replacementHost, ListenPort: &replacementPort,
		PublicHost: &replacementPublicHost, PublicPort: &replacementPort,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create(revoked listener ID) error = %v", err)
	}
}

func TestAgentAuthenticatedControlSyncReturnsInfrastructureFailures(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	if err := fixture.store.SaveAgent(t.Context(), storage.AgentRow{ID: "agent-a", Name: "stable agent A", AgentToken: "control-token-a"}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.CreatePKIIdentity(t.Context(), storage.PKIIdentityRow{
			ID: "agent-identity-a", PKIDomainID: "domain-1", Kind: storage.PKIIdentityKindAgent,
			AgentID: "agent-a", State: storage.PKIIdentityStateEnrollmentRequired,
			CreatedAt: fixture.now, UpdatedAt: fixture.now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := newPKIIdentityBinding(
		"domain-1", storage.PKIIdentityKindAgent, "agent-a", "", storage.PKICertificatePurposeClient, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := newPKIEnrollmentServiceForTest(
		t, fixture, &pkiEnrollmentTestAuthoritySigner{err: ErrPKILeaseNotHeld}, incrementingPKIID("control-sync-infra"),
	)
	pki := &InternalPKIService{
		store: fixture.store, lease: pkiStaticLeaseGate{}, enrollment: enrollment,
		clock: func() time.Time { return fixture.now },
	}
	request := PKIControlEnrollmentRequest{
		RequestID: "agent-a-generation-1", Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false), controlToken: "control-token-a",
	}
	snapshot, credentials, err := pki.ControlSync(t.Context(), "agent-a", nil, []PKIControlEnrollmentRequest{request})
	if !errors.Is(err, ErrPKILeaseNotHeld) || snapshot.PKIDomainID != "" || len(credentials) != 0 {
		t.Fatalf("ControlSync(infrastructure failure) snapshot=%+v credentials=%+v error=%v", snapshot, credentials, err)
	}
}

func TestAgentAuthenticatedControlSyncReturnsCorruptReplayAsInfrastructureFailure(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	if err := fixture.store.SaveAgent(t.Context(), storage.AgentRow{ID: "agent-a", Name: "stable agent A", AgentToken: "control-token-a"}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.CreatePKIIdentity(t.Context(), storage.PKIIdentityRow{
			ID: "agent-identity-a", PKIDomainID: "domain-1", Kind: storage.PKIIdentityKindAgent,
			AgentID: "agent-a", State: storage.PKIIdentityStateEnrollmentRequired,
			CreatedAt: fixture.now, UpdatedAt: fixture.now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := newPKIIdentityBinding(
		"domain-1", storage.PKIIdentityKindAgent, "agent-a", "", storage.PKICertificatePurposeClient, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	enrollRequest := PKIEnrollRequest{
		RequestID: "agent-a-corrupt-replay", AgentID: "agent-a", Kind: storage.PKIIdentityKindAgent,
		Purpose: storage.PKICertificatePurposeClient,
		CSRPEM:  mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
	}
	replayKey, fingerprint, err := pkiEnrollmentReplayIdentity(enrollRequest, pkiEnrollmentCredential{
		authenticated: true, controlToken: "control-token-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.CreatePKIEnrollmentReplay(t.Context(), storage.PKIEnrollmentReplayRow{
			ID: "corrupt-replay", PKIDomainID: "domain-1", RequestKey: replayKey, RequestFingerprint: fingerprint,
			ResultJSON: `{}`, ExpiresAt: fixture.now.Add(time.Minute), CreatedAt: fixture.now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	enrollment := newPKIEnrollmentServiceForTest(
		t, fixture, &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey}, incrementingPKIID("control-sync-corrupt-replay"),
	)
	pki := &InternalPKIService{
		store: fixture.store, lease: pkiStaticLeaseGate{}, enrollment: enrollment,
		clock: func() time.Time { return fixture.now },
	}
	snapshot, credentials, err := pki.ControlSync(t.Context(), "agent-a", nil, []PKIControlEnrollmentRequest{{
		RequestID: enrollRequest.RequestID, Kind: enrollRequest.Kind, Purpose: enrollRequest.Purpose,
		CSRPEM: enrollRequest.CSRPEM, controlToken: "control-token-a",
	}})
	if !errors.Is(err, ErrPKIEnrollmentRequest) || errors.Is(err, errPKIEnrollmentClientRequest) ||
		snapshot.PKIDomainID != "" || len(credentials) != 0 {
		t.Fatalf("ControlSync(corrupt replay) snapshot=%+v credentials=%+v error=%v", snapshot, credentials, err)
	}
}

func TestCanonicalRelayDeletionFailsClosedWithoutPKIRevoker(t *testing.T) {
	fixture := newPKIEnrollmentFixture(t)
	if err := fixture.store.SaveRelayListeners(t.Context(), "agent-a", []storage.RelayListenerRow{{
		ID: 42, AgentID: "agent-a", Name: "canonical relay", ListenHost: "0.0.0.0", ListenPort: 7443,
		PublicHost: "relay.example.test", PublicPort: 7443, Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
	}}); err != nil {
		t.Fatal(err)
	}
	relay := NewRelayListenerService(config.Config{DataDir: t.TempDir(), LocalAgentID: "local-agent"}, fixture.store)
	if _, err := relay.Delete(t.Context(), "agent-a", 42); !errors.Is(err, ErrPKIEnrollmentAuthorityUnavailable) {
		t.Fatalf("Delete(canonical listener without revoker) error = %v", err)
	}
	listeners, err := relay.List(t.Context(), "agent-a")
	if err != nil || len(listeners) != 1 || listeners[0].ID != 42 {
		t.Fatalf("listener after fenced deletion = %+v, error = %v", listeners, err)
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
		RequestID: "revoked-agent", Token: issued.Token, AgentID: "agent-a", Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if relinquished, err := fixture.store.RelinquishPKIInstanceLease(
		t.Context(), "instance-1", strings.Repeat("a", 64), 1, fixture.now,
	); err != nil || !relinquished {
		t.Fatalf("relinquish fixture lease = %v, error = %v", relinquished, err)
	}
	leaseRepository, err := NewGormPKILeaseRepository(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	leaseNow := time.Now().UTC()
	lease, err := NewPKILeaseService(PKILeaseServiceOptions{
		Repository: leaseRepository, InstanceID: "control-a", Clock: func() time.Time { return leaseNow }, Random: rand.Reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := lease.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewGormPKIRevocationRepository(GormPKIRevocationRepositoryOptions{Store: fixture.store, Clock: func() time.Time { return leaseNow }})
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
	agentService := NewAgentService(config.Config{LocalAgentID: "local-agent"}, fixture.store)
	if _, err := agentService.Delete(t.Context(), "agent-a"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete(agent with active PKI identity) error = %v", err)
	}

	publisher := &pkiRevocationTestPublisher{}
	closer := &pkiRevocationTestCloser{}
	snapshotSigner, err := NewPKIVaultSecuritySnapshotSigner(PKIVaultSecuritySnapshotSignerOptions{
		StateSource: fixture.store,
		Signer:      &pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	revocation, err := NewPKIRevocationService(PKIRevocationServiceOptions{
		Repository: repository, Signer: snapshotSigner, Publisher: publisher, Closer: closer,
		Lease: lease, Clock: func() time.Time { return leaseNow }, Convergence: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	pki := &InternalPKIService{
		store: fixture.store, lease: lease, revocation: revocation,
		clock: func() time.Time { return leaseNow }, random: rand.Reader,
	}
	if _, err := pki.Revoke(t.Context(), PKIActionRequest{
		TargetID: enrolled.IdentityID, Reason: "forged", ConfirmationNonce: strings.Repeat("0", 64),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Revoke(forged nonce) error = %v", err)
	}
	wrongTarget, err := pki.IssueConfirmationNonce(t.Context(), PKIConfirmationRequest{Action: "revoke", TargetID: "another-identity"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pki.Revoke(t.Context(), PKIActionRequest{
		TargetID: enrolled.IdentityID, Reason: "wrong target", ConfirmationNonce: wrongTarget.Nonce,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Revoke(wrong-target nonce) error = %v", err)
	}
	confirmation, err := pki.IssueConfirmationNonce(t.Context(), PKIConfirmationRequest{Action: "revoke", TargetID: enrolled.IdentityID})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := pki.Revoke(t.Context(), PKIActionRequest{
		TargetID: enrolled.IdentityID, Reason: "compromised", ConfirmationNonce: confirmation.Nonce,
	})
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if operation.State != storage.PKILifecycleJobStateSucceeded || !publisher.called || !closer.called {
		t.Fatalf("revocation operation = %+v publisher=%v closer=%v", operation, publisher.called, closer.called)
	}
	if _, err := pki.Revoke(t.Context(), PKIActionRequest{
		TargetID: enrolled.IdentityID, Reason: "replay", ConfirmationNonce: confirmation.Nonce,
	}); err == nil {
		t.Fatal("Revoke(reused nonce) error = nil")
	}
	after := loadPKIEnrollmentState(t, fixture.store)
	if after.Settings.SecurityRevision != 1 || after.Identities[0].State != storage.PKIIdentityStateRevoked ||
		after.Certificates[0].Status != storage.PKICertificateStatusRevoked || len(after.LifecycleJobs) != 1 {
		t.Fatalf("revoked canonical state = %+v", after)
	}
	revisionSnapshot, err := fixture.store.LoadAgentSnapshot(t.Context(), "agent-a", storage.AgentSnapshotInput{})
	if err != nil || revisionSnapshot.PKISecurity == nil || revisionSnapshot.PKISecurity.SecurityRevision != 1 ||
		!slices.Contains(revisionSnapshot.PKISecurity.RevokedIdentityIDs, enrolled.IdentityID) {
		t.Fatalf("post-revoke revision snapshot PKI security = %+v, error=%v", revisionSnapshot.PKISecurity, err)
	}
	agents, _ := fixture.store.ListAgents(t.Context())
	if len(agents) != 2 || agents[0].AgentToken != "" {
		t.Fatalf("revoked agent token remained enabled: %+v", agents)
	}

	leaseNow = leaseNow.Add(time.Second)
	replacementTokens, err := NewPKITokenService(PKITokenServiceOptions{
		Store: fixture.store, LocalAgentID: "local-agent", Clock: func() time.Time { return leaseNow },
		Random: rand.Reader, NewID: sequencePKIID("revoked-replacement-token"),
	})
	if err != nil {
		t.Fatal(err)
	}
	replacementToken, err := replacementTokens.Create(t.Context(), PKIEnrollmentTokenRequest{
		Scope: PKIEnrollmentTokenScopeBoundReenrollment, BoundAgentID: "agent-a", CreatedBy: "panel",
	})
	if err != nil {
		t.Fatal(err)
	}
	replacementRequest := PKIEnrollRequest{
		RequestID: "revoked-agent-replacement", Token: replacementToken.Token, AgentID: "agent-a",
		Kind: storage.PKIIdentityKindAgent, Purpose: storage.PKICertificatePurposeClient,
		CSRPEM: mustPKIEnrollmentCSR(t, mustPKIEnrollmentKey(t), binding, false),
	}
	replacementAgent := storage.AgentRow{
		ID: "agent-a", Name: "recovered agent A", AgentToken: "replacement-control-token", TagsJSON: `[]`,
	}
	newLeaseEnrollment := func(signer PKIEnrollmentAuthoritySigner, ids PKIIDGenerator) *PKIEnrollmentService {
		t.Helper()
		service, err := NewPKIEnrollmentService(PKIEnrollmentServiceOptions{
			Store: fixture.store, Lease: lease, AuthoritySigner: signer, LocalAgentID: "local-agent",
			Clock: func() time.Time { return leaseNow }, Random: rand.Reader, NewID: ids,
		})
		if err != nil {
			t.Fatalf("NewPKIEnrollmentService() error = %v", err)
		}
		return service
	}
	failingReplacement := newLeaseEnrollment(
		&pkiEnrollmentTestAuthoritySigner{err: errors.New("replacement signing failed")},
		incrementingPKIID("failed-revoked-replacement"),
	)
	if _, err := failingReplacement.EnrollAndBindAgent(t.Context(), replacementRequest, replacementAgent); err == nil {
		t.Fatal("EnrollAndBindAgent(revoked replacement signing failure) error = nil")
	}
	afterReplacementFailure := loadPKIEnrollmentState(t, fixture.store)
	if len(afterReplacementFailure.Identities) != 1 || afterReplacementFailure.Identities[0].ID != enrolled.IdentityID ||
		afterReplacementFailure.Identities[0].State != storage.PKIIdentityStateRevoked ||
		len(afterReplacementFailure.EnrollmentTokens) != 2 || afterReplacementFailure.EnrollmentTokens[1].ConsumedAt != nil {
		t.Fatalf("failed replacement changed durable PKI state: %+v", afterReplacementFailure)
	}
	agents, _ = fixture.store.ListAgents(t.Context())
	if len(agents) != 2 || agents[0].AgentToken != "" {
		t.Fatalf("failed replacement committed proposed control token: %+v", agents)
	}

	successfulReplacement := newLeaseEnrollment(
		&pkiEnrollmentTestAuthoritySigner{key: fixture.authorityKey},
		incrementingPKIID("successful-revoked-replacement"),
	)
	replacement, err := successfulReplacement.EnrollAndBindAgent(t.Context(), replacementRequest, replacementAgent)
	if err != nil {
		t.Fatalf("EnrollAndBindAgent(revoked replacement retry) error = %v", err)
	}
	if replacement.IdentityID == enrolled.IdentityID || replacement.AgentControlToken != replacementAgent.AgentToken {
		t.Fatalf("replacement enrollment = %+v", replacement)
	}
	afterReplacement := loadPKIEnrollmentState(t, fixture.store)
	if len(afterReplacement.Identities) != 2 || len(afterReplacement.Certificates) != 2 {
		t.Fatalf("replacement identity history = %+v", afterReplacement)
	}
	var oldIdentity, activeReplacement storage.PKIIdentityRow
	var oldCertificate, activeCertificate storage.PKICertificateRow
	for _, identity := range afterReplacement.Identities {
		switch identity.ID {
		case enrolled.IdentityID:
			oldIdentity = identity
		case replacement.IdentityID:
			activeReplacement = identity
		}
	}
	for _, certificate := range afterReplacement.Certificates {
		switch certificate.ID {
		case enrolled.CertificateID:
			oldCertificate = certificate
		case replacement.CertificateID:
			activeCertificate = certificate
		}
	}
	if oldIdentity.State != storage.PKIIdentityStateRevoked || oldCertificate.Status != storage.PKICertificateStatusRevoked ||
		activeReplacement.State != storage.PKIIdentityStateActive || activeCertificate.Status != storage.PKICertificateStatusActive {
		t.Fatalf("replacement state old_identity=%+v old_certificate=%+v replacement_identity=%+v replacement_certificate=%+v",
			oldIdentity, oldCertificate, activeReplacement, activeCertificate)
	}
	replacementSnapshot, err := fixture.store.LoadAgentSnapshot(t.Context(), "agent-a", storage.AgentSnapshotInput{})
	if err != nil || replacementSnapshot.PKISecurity == nil ||
		!slices.Contains(replacementSnapshot.PKISecurity.RevokedIdentityIDs, enrolled.IdentityID) ||
		!slices.Contains(replacementSnapshot.PKISecurity.RevokedSerials, oldCertificate.SerialHex) {
		t.Fatalf("replacement snapshot lost monotonic revocations: %+v, error=%v", replacementSnapshot.PKISecurity, err)
	}
	agents, _ = fixture.store.ListAgents(t.Context())
	if len(agents) != 2 || agents[0].AgentToken != replacementAgent.AgentToken {
		t.Fatalf("replacement control token was not committed atomically: %+v", agents)
	}

	leaseNow = leaseNow.Add(time.Second)
	replacementConfirmation, err := pki.IssueConfirmationNonce(t.Context(), PKIConfirmationRequest{
		Action: "revoke", TargetID: replacement.IdentityID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pki.Revoke(t.Context(), PKIActionRequest{
		TargetID: replacement.IdentityID, Reason: "replacement retired", ConfirmationNonce: replacementConfirmation.Nonce,
	}); err != nil {
		t.Fatalf("Revoke(replacement) error = %v", err)
	}
	if _, err := agentService.Delete(t.Context(), "agent-a"); err != nil {
		t.Fatalf("Delete(agent after PKI revocation) error = %v", err)
	}
	afterDelete := loadPKIEnrollmentState(t, fixture.store)
	if len(afterDelete.Identities) != 2 || len(afterDelete.Certificates) != 2 {
		t.Fatalf("agent deletion discarded PKI revocation tombstones: %+v", afterDelete)
	}
	for _, identity := range afterDelete.Identities {
		if identity.State != storage.PKIIdentityStateRevoked {
			t.Fatalf("agent deletion retained non-revoked identity: %+v", afterDelete.Identities)
		}
	}
	for _, certificate := range afterDelete.Certificates {
		if certificate.Status != storage.PKICertificateStatusRevoked {
			t.Fatalf("agent deletion retained non-revoked certificate: %+v", afterDelete.Certificates)
		}
	}
}

type controlPKIBootstrap struct {
	result          InternalPKIBootstrapResult
	lease           *PKILeaseService
	authoritySigner PKIEnrollmentAuthoritySigner
	snapshotSigner  PKISecuritySnapshotSigner
}

func bootstrapInternalPKIForControlTest(t *testing.T, store *storage.GormStore, vault *PKIVault) controlPKIBootstrap {
	t.Helper()
	repository, err := NewGormPKILeaseRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := NewPKILeaseService(PKILeaseServiceOptions{
		Repository: repository, InstanceID: "integration-bootstrap-" + strings.ReplaceAll(t.Name(), "/", "-"),
	})
	if err != nil {
		t.Fatal(err)
	}
	vaultSigner, err := NewPKIVaultAuthoritySigner(vault)
	if err != nil {
		t.Fatal(err)
	}
	leaseSigner, err := NewPKILeaseAuthoritySigner(lease, vaultSigner)
	if err != nil {
		t.Fatal(err)
	}
	snapshotSigner, err := NewPKIVaultSecuritySnapshotSigner(PKIVaultSecuritySnapshotSignerOptions{
		StateSource: store, Signer: leaseSigner,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := BootstrapInternalPKI(t.Context(), InternalPKIBootstrapOptions{
		Store: store, Vault: vault, Lease: lease, SnapshotSigner: snapshotSigner,
	})
	if err != nil {
		t.Fatalf("BootstrapInternalPKI() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = lease.Relinquish(ctx)
	})
	return controlPKIBootstrap{
		result: result, lease: lease, authoritySigner: leaseSigner, snapshotSigner: snapshotSigner,
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
