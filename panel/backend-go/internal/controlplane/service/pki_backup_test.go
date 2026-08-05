package service

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
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPKIBackupProtectedRoundTripSanitizesTokensAndMatchesKeys(t *testing.T) {
	fixture := newPKIBackupFixture(t)
	gate := &pkiBackupTestLeaseGate{grant: fixture.grant}
	target := &pkiBackupTestRestoreTarget{current: fixture.targetState(
		PKISecurityVersion{PKIEpoch: fixture.grant.PKIEpoch, SecurityRevision: fixture.securityRevision},
	)}
	service := newPKIBackupServiceForTest(t, fixture, gate, target)
	passphrase := []byte("correct horse battery staple")

	exported, err := service.ExportProtected(t.Context(), passphrase)
	if err != nil {
		t.Fatalf("ExportProtected() error = %v", err)
	}
	if exported.Manifest.EnrollmentTokens != 0 || !exported.Manifest.Full {
		t.Fatalf("manifest = %+v, want full snapshot with zero tokens", exported.Manifest)
	}
	if bytes.Contains(exported.Envelope, []byte(fixture.tokenDigest)) || bytes.Contains(exported.Envelope, fixture.authorityPKCS8) || bytes.Contains(exported.Envelope, passphrase) {
		t.Fatal("protected envelope contains token, authority key, or passphrase plaintext")
	}
	var envelope PKIProtectedBackupEnvelope
	if err := json.Unmarshal(exported.Envelope, &envelope); err != nil {
		t.Fatalf("Unmarshal(envelope) error = %v", err)
	}
	if envelope.KDF.MemoryKiB != 64*1024 || envelope.KDF.Iterations != 3 || envelope.KDF.Parallelism != 1 || envelope.KDF.KeyBytes != 32 || envelope.Cipher.Algorithm != "aes-256-gcm" {
		t.Fatalf("envelope crypto metadata = %+v / %+v", envelope.KDF, envelope.Cipher)
	}

	result, err := service.RestoreProtected(t.Context(), exported.Envelope, passphrase, PKIBackupRestoreOptions{})
	if err != nil {
		t.Fatalf("RestoreProtected() error = %v", err)
	}
	if result.Version != target.current.Version || result.Forced {
		t.Fatalf("restore result = %+v, target = %+v", result, target.current)
	}
	request, ok := target.lastRequest()
	if !ok || len(request.AuthorityKeys) != 1 || request.AuthorityKeys[0].AuthorityID != fixture.authority.ID {
		t.Fatalf("activation request = %+v", request)
	}
	if !bytes.Equal(request.AuthorityKeys[0].PKCS8, fixture.authorityPKCS8) {
		t.Fatal("restored authority key differs from exported PKCS#8 key")
	}
	stage, err := stagePKIBackupSQLite(t.Context(), request.SQLiteSnapshot, pkiBackupStageOptions{})
	if err != nil {
		t.Fatalf("validate activated SQLite snapshot: %v", err)
	}
	defer clear(stage.Snapshot)
	if stage.EnrollmentTokens != 0 || stage.State.InstanceLease != nil {
		t.Fatalf("activated snapshot retained ephemeral state: tokens=%d lease=%+v", stage.EnrollmentTokens, stage.State.InstanceLease)
	}
}

func TestPKIBackupCommittedCleanupIsASuccessfulActivation(t *testing.T) {
	fixture := newPKIBackupFixture(t)
	exportTarget := &pkiBackupTestRestoreTarget{current: fixture.targetState(
		PKISecurityVersion{PKIEpoch: fixture.grant.PKIEpoch, SecurityRevision: fixture.securityRevision},
	)}
	gate := &pkiBackupTestLeaseGate{grant: fixture.grant}
	exporter := newPKIBackupServiceForTest(t, fixture, gate, exportTarget)
	exported, err := exporter.ExportProtected(t.Context(), []byte("backup-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	target := &pkiBackupTestRestoreTarget{
		current:       exportTarget.current,
		activationErr: errors.Join(storage.ErrPKIRestoreCleanupPending, errors.New("injected tombstone cleanup failure")),
	}
	service := newPKIBackupServiceForTest(t, fixture, gate, target)
	result, err := service.RestoreProtected(t.Context(), exported.Envelope, []byte("backup-passphrase"), PKIBackupRestoreOptions{})
	if err != nil {
		t.Fatalf("RestoreProtected(committed cleanup) error = %v", err)
	}
	if !result.CleanupPending || target.activationCount() != 1 {
		t.Fatalf("committed cleanup result = %+v, activations = %d", result, target.activationCount())
	}
}

func TestPKIBackupWrongPassphraseAndTamperLeaveTargetUnchanged(t *testing.T) {
	fixture := newPKIBackupFixture(t)
	target := &pkiBackupTestRestoreTarget{current: fixture.targetState(
		PKISecurityVersion{PKIEpoch: fixture.grant.PKIEpoch, SecurityRevision: fixture.securityRevision},
	)}
	service := newPKIBackupServiceForTest(t, fixture, &pkiBackupTestLeaseGate{grant: fixture.grant}, target)
	exported, err := service.ExportProtected(t.Context(), []byte("backup-passphrase"))
	if err != nil {
		t.Fatalf("ExportProtected() error = %v", err)
	}
	wantState := target.current

	if _, err := service.RestoreProtected(t.Context(), exported.Envelope, []byte("wrong-passphrase"), PKIBackupRestoreOptions{}); !errors.Is(err, ErrPKIBackupAuthentication) {
		t.Fatalf("wrong-passphrase error = %v, want ErrPKIBackupAuthentication", err)
	}
	if target.current != wantState || target.activationCount() != 0 {
		t.Fatalf("wrong passphrase changed target: state=%+v activations=%d", target.current, target.activationCount())
	}

	var envelope PKIProtectedBackupEnvelope
	if err := json.Unmarshal(exported.Envelope, &envelope); err != nil {
		t.Fatalf("Unmarshal(envelope) error = %v", err)
	}
	envelope.Manifest.Version.SecurityRevision++
	tampered, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal(tampered envelope) error = %v", err)
	}
	if _, err := service.RestoreProtected(t.Context(), tampered, []byte("backup-passphrase"), PKIBackupRestoreOptions{}); !errors.Is(err, ErrPKIBackupAuthentication) {
		t.Fatalf("tampered-manifest error = %v, want ErrPKIBackupAuthentication", err)
	}
	if target.current != wantState || target.activationCount() != 0 {
		t.Fatalf("tampering changed target: state=%+v activations=%d", target.current, target.activationCount())
	}

	target.current.SQLiteSchemaSHA256 = hex.EncodeToString(bytes.Repeat([]byte{0xff}, sha256.Size))
	mismatchedSchemaState := target.current
	if _, err := service.RestoreProtected(t.Context(), exported.Envelope, []byte("backup-passphrase"), PKIBackupRestoreOptions{}); !errors.Is(err, ErrPKIBackupSchema) {
		t.Fatalf("target-schema mismatch error = %v, want ErrPKIBackupSchema", err)
	}
	if target.current != mismatchedSchemaState || target.activationCount() != 0 {
		t.Fatalf("schema mismatch changed target: state=%+v activations=%d", target.current, target.activationCount())
	}
}

func TestPKIBackupTargetSchemaBaselineRequiredBeforeInitialization(t *testing.T) {
	valid := PKIBackupTargetState{
		SQLiteSchemaVersion: 0,
		SQLiteSchemaSHA256:  hex.EncodeToString(make([]byte, sha256.Size)),
	}
	if err := validatePKIBackupTargetState(valid); err != nil {
		t.Fatalf("validate uninitialized target with trusted schema error = %v", err)
	}
	valid.SQLiteSchemaSHA256 = ""
	if err := validatePKIBackupTargetState(valid); !errors.Is(err, ErrPKIBackupSchema) {
		t.Fatalf("validate uninitialized target without schema error = %v, want ErrPKIBackupSchema", err)
	}
}

func TestPKIBackupLeaseLossFailsClosed(t *testing.T) {
	fixture := newPKIBackupFixture(t)
	target := &pkiBackupTestRestoreTarget{current: fixture.targetState(
		PKISecurityVersion{PKIEpoch: fixture.grant.PKIEpoch, SecurityRevision: fixture.securityRevision},
	)}
	gate := &pkiBackupTestLeaseGate{grant: fixture.grant, failAt: 3}
	service := newPKIBackupServiceForTest(t, fixture, gate, target)
	if result, err := service.ExportProtected(t.Context(), []byte("backup-passphrase")); !errors.Is(err, ErrPKILeaseNotHeld) || len(result.Envelope) != 0 {
		t.Fatalf("lost-lease export = (%d bytes, %v), want empty ErrPKILeaseNotHeld", len(result.Envelope), err)
	}

	gate = &pkiBackupTestLeaseGate{grant: fixture.grant, failAt: 1}
	service = newPKIBackupServiceForTest(t, fixture, gate, target)
	if _, err := service.RestoreProtected(t.Context(), []byte("not-an-envelope"), []byte("backup-passphrase"), PKIBackupRestoreOptions{}); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("lost-lease restore error = %v, want ErrPKILeaseNotHeld", err)
	}
	if target.activationCount() != 0 {
		t.Fatalf("activation count = %d, want zero", target.activationCount())
	}
}

func TestPKIBackupForceActivationUsesHigherEpochAndIsAtomicOnFailure(t *testing.T) {
	fixture := newPKIBackupFixture(t)
	exportTarget := &pkiBackupTestRestoreTarget{current: fixture.targetState(
		PKISecurityVersion{PKIEpoch: fixture.grant.PKIEpoch, SecurityRevision: fixture.securityRevision},
	)}
	exportService := newPKIBackupServiceForTest(t, fixture, &pkiBackupTestLeaseGate{grant: fixture.grant}, exportTarget)
	exported, err := exportService.ExportProtected(t.Context(), []byte("backup-passphrase"))
	if err != nil {
		t.Fatalf("ExportProtected() error = %v", err)
	}

	current := fixture.targetState(PKISecurityVersion{PKIEpoch: 5, SecurityRevision: 91})
	failingTarget := &pkiBackupTestRestoreTarget{current: current, activationErr: errors.New("injected atomic swap failure")}
	service := newPKIBackupServiceForTest(t, fixture, &pkiBackupTestLeaseGate{grant: fixture.grant}, failingTarget)
	if _, err := service.RestoreProtected(t.Context(), exported.Envelope, []byte("backup-passphrase"), PKIBackupRestoreOptions{Force: true}); !errors.Is(err, ErrPKIBackupActivation) {
		t.Fatalf("forced activation failure error = %v, want ErrPKIBackupActivation", err)
	}
	if failingTarget.current != current {
		t.Fatalf("failed activation changed target: got %+v want %+v", failingTarget.current, current)
	}
	request, ok := failingTarget.lastRequest()
	if !ok || request.Version != (PKISecurityVersion{PKIEpoch: 6, SecurityRevision: 0}) || !request.Forced || !request.Full {
		t.Fatalf("forced request = %+v", request)
	}
	stage, err := stagePKIBackupSQLite(t.Context(), request.SQLiteSnapshot, pkiBackupStageOptions{ForceVersion: &request.Version})
	if err != nil {
		t.Fatalf("validate forced staged snapshot: %v", err)
	}
	defer clear(stage.Snapshot)
	if stage.State.Settings == nil || stage.State.Settings.PKIEpoch != 6 || stage.State.Settings.SecurityRevision != 0 {
		t.Fatalf("forced staged settings = %+v", stage.State.Settings)
	}
}

func TestPKIBackupStagingRejectsTokensSchemaHashAndKeyMismatch(t *testing.T) {
	fixture := newPKIBackupFixture(t)
	if _, err := stagePKIBackupSQLite(t.Context(), fixture.snapshot, pkiBackupStageOptions{}); !errors.Is(err, ErrPKIBackupIntegrity) {
		t.Fatalf("unsanitized snapshot error = %v, want ErrPKIBackupIntegrity", err)
	}
	staged, err := stagePKIBackupSQLite(t.Context(), fixture.snapshot, pkiBackupStageOptions{Sanitize: true})
	if err != nil {
		t.Fatalf("sanitized staging error = %v", err)
	}
	defer clear(staged.Snapshot)
	if bytes.Contains(staged.Snapshot, []byte(fixture.tokenDigest)) {
		t.Fatal("sanitized SQLite bytes retain the original enrollment token digest")
	}
	if freelist := readPKIBackupFreelistCount(t, staged.Snapshot); freelist != 0 {
		t.Fatalf("sanitized SQLite freelist_count = %d, want zero", freelist)
	}
	keys := []PKIBackupAuthorityKey{{AuthorityID: fixture.authority.ID, Generation: fixture.authority.Generation, PKCS8: append([]byte(nil), fixture.authorityPKCS8...)}}
	defer clearPKIBackupAuthorityKeys(keys)
	manifest := buildPKIBackupManifest(staged, keys, time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC))
	manifest.SQLiteSHA256 = hex.EncodeToString(make([]byte, sha256.Size))
	if err := validatePKIBackupManifest(manifest, staged, keys); !errors.Is(err, ErrPKIBackupIntegrity) {
		t.Fatalf("wrong snapshot hash error = %v, want ErrPKIBackupIntegrity", err)
	}

	wrongKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey(wrong) error = %v", err)
	}
	wrongPKCS8, err := x509.MarshalPKCS8PrivateKey(wrongKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey(wrong) error = %v", err)
	}
	defer clear(wrongPKCS8)
	if err := validatePKIBackupAuthorityKeys(staged.State, []PKIBackupAuthorityKey{{AuthorityID: fixture.authority.ID, Generation: 1, PKCS8: wrongPKCS8}}); !errors.Is(err, ErrPKIBackupIntegrity) {
		t.Fatalf("mismatched authority key error = %v, want ErrPKIBackupIntegrity", err)
	}

	broken := dropPKIBackupTable(t, staged.Snapshot, "pki_events")
	defer clear(broken)
	if _, err := stagePKIBackupSQLite(t.Context(), broken, pkiBackupStageOptions{}); !errors.Is(err, ErrPKIBackupSchema) {
		t.Fatalf("missing-schema error = %v, want ErrPKIBackupSchema", err)
	}
}

type pkiBackupFixture struct {
	snapshot         []byte
	authority        storage.PKIAuthorityRow
	authorityPKCS8   []byte
	tokenDigest      string
	grant            PKILeaseGrant
	securityRevision int64
	schemaVersion    int
	schemaSHA256     string
}

func newPKIBackupFixture(t *testing.T) pkiBackupFixture {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "panel.db")
	dsn := path + "?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open fixture SQLite: %v", err)
	}
	if err := db.AutoMigrate(
		&storage.AgentRow{}, &storage.RelayListenerRow{},
		&storage.PKISettingsRow{}, &storage.PKIAuthorityRow{}, &storage.PKIIdentityRow{},
		&storage.PKICertificateRow{}, &storage.PKIEnrollmentTokenRow{}, &storage.PKIEnrollmentReplayRow{},
		&storage.PKIConfirmationNonceRow{}, &storage.PKISecuritySnapshotRow{}, &storage.PKILifecycleJobRow{},
		&storage.PKIEventRow{}, &storage.PKIInstanceLeaseRow{},
	); err != nil {
		t.Fatalf("migrate fixture PKI schema: %v", err)
	}
	schemaVersion, schemaSHA256, err := inspectPKIBackupSchema(t.Context(), db)
	if err != nil {
		t.Fatalf("inspect fixture schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("fixture db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close fixture schema handle: %v", err)
	}

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	leaseTerm := hex.EncodeToString(make([]byte, pkiLeaseTermBytes))
	key, authority := newPKIBackupAuthority(t, now)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey(authority) error = %v", err)
	}
	tokenDigestBytes := sha256.Sum256([]byte("one-time-enrollment-secret"))
	tokenDigest := hex.EncodeToString(tokenDigestBytes[:])
	unsignedSnapshot := PKIUnsignedSecuritySnapshot{
		PKIDomainID: "domain-1",
		Version: PKISecuritySnapshotVersion{
			Version: PKISecurityVersion{PKIEpoch: 1, SecurityRevision: 7}, Full: true,
		},
		IssuedAt:         now,
		TrustGenerations: []int64{authority.Generation},
		TrustRoots: []PKISecurityTrustRootDescriptor{{
			AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
			FingerprintSHA256: authority.FingerprintSHA256, NotBefore: authority.NotBefore, NotAfter: authority.NotAfter,
		}},
		RevokedIdentityIDs: []string{}, RevokedSerials: []string{},
	}
	unsignedPayload, err := marshalPKIUnsignedSecuritySnapshot(unsignedSnapshot)
	if err != nil {
		t.Fatalf("marshal fixture security snapshot: %v", err)
	}
	unsignedDigest := sha256.Sum256(unsignedPayload)
	signature, err := ecdsa.SignASN1(rand.Reader, key, unsignedDigest[:])
	if err != nil {
		t.Fatalf("sign fixture security snapshot: %v", err)
	}
	securitySnapshot := storage.PKISecuritySnapshot{
		PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 7, Full: true, IssuedAt: now,
		TrustRoots: []storage.PKITrustRoot{{
			AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
			CertificatePEM: authority.CertificatePEM, FingerprintSHA256: authority.FingerprintSHA256,
			NotBefore: authority.NotBefore, NotAfter: authority.NotAfter,
		}},
		RevokedIdentityIDs: []string{}, RevokedSerials: []string{},
		SignerGeneration: authority.Generation, Signature: signature,
	}
	encodedSecuritySnapshot, err := json.Marshal(securitySnapshot)
	if err != nil {
		t.Fatalf("marshal fixture persisted security snapshot: %v", err)
	}
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "backup-fixture", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("open fixture canonical store: %v", err)
	}
	err = store.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		if err := tx.CreatePKISettings(t.Context(), storage.PKISettingsRow{
			PKIDomainID: "domain-1", CALifetimeSeconds: 315360000, EndpointLifetimeSeconds: 7776000,
			AuditRetentionDays: 365, SecurityRevision: 7, PKIEpoch: 1, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.CreatePKIAuthority(t.Context(), authority); err != nil {
			return err
		}
		if err := tx.SavePKISecuritySnapshot(t.Context(), storage.PKISecuritySnapshotRow{
			PKIDomainID: "domain-1", PKIEpoch: 1, SecurityRevision: 7,
			SnapshotJSON: string(encodedSecuritySnapshot), UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.CreatePKIEnrollmentToken(t.Context(), storage.PKIEnrollmentTokenRow{
			ID: "token-1", TokenDigestSHA256: tokenDigest, Scope: "agent_enrollment",
			ExpiresAt: now.Add(time.Hour), CreatedBy: "operator", CreatedAt: now,
		}); err != nil {
			return err
		}
		return tx.CreatePKIInstanceLease(t.Context(), storage.PKIInstanceLeaseRow{
			PKIDomainID: "domain-1", InstanceID: "instance-a", LeaseTerm: leaseTerm, LeaseDeadline: now.Add(30 * time.Second),
			PKIEpoch: 1, State: PKILeaseStateHeld, UpdatedAt: now,
		})
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("seed fixture canonical state: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close fixture canonical store: %v", err)
	}
	snapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture snapshot: %v", err)
	}
	grant := PKILeaseGrant{
		PKIDomainID: "domain-1", PKIEpoch: 1, InstanceID: "instance-a",
		LeaseTerm: leaseTerm, LeaseDeadline: now.Add(30 * time.Second),
	}
	return pkiBackupFixture{
		snapshot: snapshot, authority: authority, authorityPKCS8: pkcs8,
		tokenDigest: tokenDigest, grant: grant, securityRevision: 7,
		schemaVersion: schemaVersion, schemaSHA256: schemaSHA256,
	}
}

func (f pkiBackupFixture) targetState(version PKISecurityVersion) PKIBackupTargetState {
	return PKIBackupTargetState{
		Initialized: true, PKIDomainID: f.grant.PKIDomainID, Version: version,
		SQLiteSchemaVersion: f.schemaVersion, SQLiteSchemaSHA256: f.schemaSHA256,
	}
}

func newPKIBackupAuthority(t *testing.T, now time.Time) (*ecdsa.PrivateKey, storage.PKIAuthorityRow) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey(authority) error = %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey(authority) error = %v", err)
	}
	subjectKeyID := sha256.Sum256(publicDER)
	template := &x509.Certificate{
		SerialNumber: new(big.Int).SetBytes(append([]byte{0x80}, make([]byte, 15)...)),
		Subject:      pkix.Name{CommonName: "NRE backup test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true, SubjectKeyId: append([]byte(nil), subjectKeyID[:20]...), SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate(authority) error = %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate(authority) error = %v", err)
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	keyRef := "ca-1-test.vault"
	return key, storage.PKIAuthorityRow{
		ID: "authority-1", PKIDomainID: "domain-1", Generation: 1, Status: "active",
		CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), EncryptedKeyRef: &keyRef,
		FingerprintSHA256: hex.EncodeToString(fingerprint[:]), NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
		CreatedReason: "bootstrap", CreatedAt: now, UpdatedAt: now,
	}
}

func newPKIBackupServiceForTest(t *testing.T, fixture pkiBackupFixture, gate PKILeaseGate, target PKIBackupRestoreTarget) *PKIBackupService {
	t.Helper()
	service, err := NewPKIBackupService(PKIBackupServiceOptions{
		LeaseGate:          gate,
		SnapshotSource:     pkiBackupTestSnapshotSource{snapshot: fixture.snapshot},
		AuthorityKeySource: pkiBackupTestKeySource{keys: map[string][]byte{fixture.authority.ID: append([]byte(nil), fixture.authorityPKCS8...)}},
		RestoreTarget:      target, Clock: func() time.Time { return time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC) }, Random: rand.Reader,
	})
	if err != nil {
		t.Fatalf("NewPKIBackupService() error = %v", err)
	}
	return service
}

type pkiBackupTestSnapshotSource struct {
	snapshot []byte
}

func (s pkiBackupTestSnapshotSource) CaptureConsistentPKISQLite(context.Context) ([]byte, error) {
	return append([]byte(nil), s.snapshot...), nil
}

type pkiBackupTestKeySource struct {
	keys map[string][]byte
}

func (s pkiBackupTestKeySource) ExportPKIAuthorityKey(_ context.Context, authority storage.PKIAuthorityRow) ([]byte, error) {
	key, found := s.keys[authority.ID]
	if !found {
		return nil, errors.New("test authority key missing")
	}
	return append([]byte(nil), key...), nil
}

type pkiBackupTestLeaseGate struct {
	mutex  sync.Mutex
	grant  PKILeaseGrant
	failAt int
	calls  int
}

func (g *pkiBackupTestLeaseGate) RequirePKILease(context.Context) (PKILeaseGrant, error) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.calls++
	if g.failAt > 0 && g.calls >= g.failAt {
		return PKILeaseGrant{}, ErrPKILeaseNotHeld
	}
	return g.grant, nil
}

type pkiBackupTestRestoreTarget struct {
	mutex         sync.Mutex
	current       PKIBackupTargetState
	requests      []PKIBackupActivationRequest
	activationErr error
}

func (t *pkiBackupTestRestoreTarget) CurrentPKIBackupTarget(context.Context) (PKIBackupTargetState, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.current, nil
}

func (t *pkiBackupTestRestoreTarget) ActivateProtectedPKIBackup(_ context.Context, request PKIBackupActivationRequest) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	copyRequest := request
	copyRequest.SQLiteSnapshot = append([]byte(nil), request.SQLiteSnapshot...)
	copyRequest.AuthorityKeys = make([]PKIBackupAuthorityKey, len(request.AuthorityKeys))
	for index, key := range request.AuthorityKeys {
		copyRequest.AuthorityKeys[index] = key
		copyRequest.AuthorityKeys[index].PKCS8 = append([]byte(nil), key.PKCS8...)
	}
	t.requests = append(t.requests, copyRequest)
	if t.activationErr != nil {
		return t.activationErr
	}
	if request.ExpectedTarget != t.current {
		return errors.New("test target compare-and-swap conflict")
	}
	t.current = PKIBackupTargetState{
		Initialized: true, PKIDomainID: request.PKIDomainID, Version: request.Version,
		SQLiteSchemaVersion: t.current.SQLiteSchemaVersion, SQLiteSchemaSHA256: t.current.SQLiteSchemaSHA256,
	}
	return nil
}

func (t *pkiBackupTestRestoreTarget) activationCount() int {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return len(t.requests)
}

func (t *pkiBackupTestRestoreTarget) lastRequest() (PKIBackupActivationRequest, bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if len(t.requests) == 0 {
		return PKIBackupActivationRequest{}, false
	}
	return t.requests[len(t.requests)-1], true
}

func dropPKIBackupTable(t *testing.T, snapshot []byte, table string) []byte {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "broken.db")
	if err := os.WriteFile(path, snapshot, 0o600); err != nil {
		t.Fatalf("write broken fixture: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=journal_mode(DELETE)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open broken fixture: %v", err)
	}
	if err := db.Exec("DROP TABLE " + table).Error; err != nil {
		t.Fatalf("drop fixture table: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("broken fixture db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close broken fixture: %v", err)
	}
	result, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read broken fixture: %v", err)
	}
	return result
}

func readPKIBackupFreelistCount(t *testing.T, snapshot []byte) int64 {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "freelist.db")
	if err := os.WriteFile(path, snapshot, 0o600); err != nil {
		t.Fatalf("write freelist fixture: %v", err)
	}
	db, closeDB, err := openPKIBackupSQLite(path)
	if err != nil {
		t.Fatalf("open freelist fixture: %v", err)
	}
	var count int64
	if err := db.WithContext(t.Context()).Raw("PRAGMA freelist_count").Scan(&count).Error; err != nil {
		_ = closeDB()
		t.Fatalf("read freelist_count: %v", err)
	}
	if err := closeDB(); err != nil {
		t.Fatalf("close freelist fixture: %v", err)
	}
	return count
}
