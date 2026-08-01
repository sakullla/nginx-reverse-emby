package storage

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrPKIInvariant                 = errors.New("invalid PKI canonical record")
	ErrPKILeaseFence                = errors.New("PKI mutation lease fence rejected")
	ErrPKIAgentIdentityNotRevoked   = errors.New("agent has a non-revoked PKI identity")
	ErrAgentRelayListenerReferenced = errors.New("agent relay listener is still referenced")
	ErrAgentControlTokenChanged     = errors.New("agent control token changed")
)

type PKITransaction struct {
	db *gorm.DB
}

// PKILeaseFence is the storage-level representation of a service lease grant.
// Mutations compare every field, including the deadline, against the canonical
// row and use database time for the expiry decision inside the same transaction.
type PKILeaseFence struct {
	PKIDomainID   string
	PKIEpoch      int64
	InstanceID    string
	LeaseTerm     string
	LeaseDeadline time.Time
}

// RequirePKILeaseFence locks and validates the canonical lease for a mutation.
// A concurrent renewal or owner/epoch change rejects the mutation instead of
// degrading to a service-layer preflight check.
func (tx *PKITransaction) RequirePKILeaseFence(ctx context.Context, fence PKILeaseFence) error {
	fence.PKIDomainID = strings.TrimSpace(fence.PKIDomainID)
	fence.InstanceID = strings.TrimSpace(fence.InstanceID)
	fence.LeaseTerm = strings.TrimSpace(fence.LeaseTerm)
	if fence.PKIDomainID == "" || fence.PKIEpoch < 0 || fence.InstanceID == "" || fence.LeaseTerm == "" || fence.LeaseDeadline.IsZero() {
		return pkiInvariant("PKI lease fence fields are incomplete")
	}

	settings, found, err := tx.GetPKISettingsForUpdate(ctx)
	if err != nil {
		return err
	}
	if !found || settings.PKIDomainID != fence.PKIDomainID || settings.PKIEpoch != fence.PKIEpoch {
		return ErrPKILeaseFence
	}

	var row PKIInstanceLeaseRow
	err = tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(
			"id = ? AND pki_domain_id = ? AND pki_epoch = ? AND instance_id = ? AND lease_term = ? AND state = ? AND lease_deadline = ? AND lease_deadline > CURRENT_TIMESTAMP",
			PKILeaseSingletonID,
			fence.PKIDomainID,
			fence.PKIEpoch,
			fence.InstanceID,
			fence.LeaseTerm,
			PKIInstanceLeaseStateHeld,
			fence.LeaseDeadline,
		).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrPKILeaseFence
	}
	return err
}

// WithPKITransaction is the only supported mutation boundary for canonical PKI
// facts. The complete relation graph is checked before its transaction can
// commit, so insertion order may use forward references without exposing them.
func (s *GormStore) WithPKITransaction(ctx context.Context, mutate func(*PKITransaction) error) error {
	if mutate == nil {
		return fmt.Errorf("PKI mutation callback is required")
	}
	run := func(db *gorm.DB) error {
		if err := mutate(&PKITransaction{db: db}); err != nil {
			return err
		}
		return validatePKICanonicalRelationships(ctx, db)
	}
	if s.transactionScoped {
		// Use a nested savepoint so a caller cannot swallow validation failure
		// and accidentally commit invalid PKI rows in its outer transaction.
		return s.db.WithContext(ctx).Transaction(run)
	}
	return s.writeTransaction(ctx, run)
}

func (tx *PKITransaction) CreatePKISettings(ctx context.Context, row PKISettingsRow) error {
	row.ID = PKISettingsSingletonID
	if strings.TrimSpace(row.PKIDomainID) == "" || row.CALifetimeSeconds <= 0 || row.EndpointLifetimeSeconds <= 0 || row.AuditRetentionDays <= 0 || row.SecurityRevision < 0 || row.PKIEpoch < 0 || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
		return pkiInvariant("settings fields are incomplete")
	}
	return tx.db.WithContext(ctx).Create(&row).Error
}

func (tx *PKITransaction) CreatePKIAuthority(ctx context.Context, row PKIAuthorityRow) error {
	if strings.TrimSpace(row.ID) == "" || strings.TrimSpace(row.PKIDomainID) == "" || row.Generation <= 0 || strings.TrimSpace(row.Status) == "" || row.NotBefore.IsZero() || !row.NotAfter.After(row.NotBefore) || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
		return pkiInvariant("authority fields are incomplete")
	}
	if row.PrivateKeyDestroyedAt == nil && (row.EncryptedKeyRef == nil || strings.TrimSpace(*row.EncryptedKeyRef) == "") {
		return pkiInvariant("authority encrypted key reference is required until key destruction")
	}
	if _, err := validatePKIAuthorityCertificate(row); err != nil {
		return err
	}
	row.FingerprintSHA256 = strings.ToLower(strings.TrimSpace(row.FingerprintSHA256))
	return tx.db.WithContext(ctx).Create(&row).Error
}

func (tx *PKITransaction) CreatePKIIdentity(ctx context.Context, row PKIIdentityRow) error {
	row.Kind = strings.TrimSpace(row.Kind)
	row.AgentID = strings.TrimSpace(row.AgentID)
	row.ListenerID = strings.TrimSpace(row.ListenerID)
	if strings.TrimSpace(row.ID) == "" || strings.TrimSpace(row.PKIDomainID) == "" || row.AgentID == "" || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
		return pkiInvariant("identity fields are incomplete")
	}
	if row.Kind != PKIIdentityKindAgent && row.Kind != PKIIdentityKindListener {
		return pkiInvariant("identity kind must be agent or listener")
	}
	if row.Kind == PKIIdentityKindAgent && row.ListenerID != "" {
		return pkiInvariant("agent identity cannot carry a listener reference")
	}
	if row.Kind == PKIIdentityKindListener && row.ListenerID == "" {
		return pkiInvariant("listener identity requires a listener reference")
	}
	switch row.State {
	case PKIIdentityStateEnrollmentRequired, PKIIdentityStateActive, PKIIdentityStateRevoked:
	default:
		return pkiInvariant("identity state is invalid")
	}
	return tx.db.WithContext(ctx).Create(&row).Error
}

func (tx *PKITransaction) CreatePKICertificate(ctx context.Context, row PKICertificateRow) error {
	row.SerialHex = strings.ToLower(strings.TrimSpace(row.SerialHex))
	if strings.TrimSpace(row.ID) == "" || !validHexAtLeast(row.SerialHex, 16) || strings.TrimSpace(row.IdentityID) == "" || strings.TrimSpace(row.AuthorityID) == "" || row.CAGeneration <= 0 || !validHexBytes(row.PublicKeyFingerprint, 32) || row.NotBefore.IsZero() || !row.NotAfter.After(row.NotBefore) || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
		return pkiInvariant("certificate fields are incomplete")
	}
	if row.Purpose != PKICertificatePurposeClient && row.Purpose != PKICertificatePurposeServer {
		return pkiInvariant("certificate purpose is invalid")
	}
	switch row.Status {
	case PKICertificateStatusPending, PKICertificateStatusActive, PKICertificateStatusSuperseded, PKICertificateStatusRevoked, PKICertificateStatusExpired:
	default:
		return pkiInvariant("certificate status is invalid")
	}
	if _, err := validatePKILeafCertificate(row); err != nil {
		return err
	}
	row.PublicKeyFingerprint = strings.ToLower(strings.TrimSpace(row.PublicKeyFingerprint))
	row.ActiveIdentityPurposeKey = nil
	if row.Status == PKICertificateStatusActive {
		key := pkiUniqueSlot(row.IdentityID, row.Purpose)
		row.ActiveIdentityPurposeKey = &key
	}
	return tx.db.WithContext(ctx).Create(&row).Error
}

func (tx *PKITransaction) CreatePKIEnrollmentToken(ctx context.Context, row PKIEnrollmentTokenRow) error {
	row.TokenDigestSHA256 = strings.ToLower(strings.TrimSpace(row.TokenDigestSHA256))
	if strings.TrimSpace(row.ID) == "" || !validHexBytes(row.TokenDigestSHA256, 32) || strings.TrimSpace(row.Scope) == "" || row.ExpiresAt.IsZero() || strings.TrimSpace(row.CreatedBy) == "" || row.CreatedAt.IsZero() {
		return pkiInvariant("enrollment token fields are incomplete")
	}
	return tx.db.WithContext(ctx).Create(&row).Error
}

// ConsumePKIEnrollmentToken conditionally consumes one live token. Callers get
// no distinction between a missing, expired, or already-consumed credential so
// the service layer can expose a single rejection result without a token-state
// oracle.
func (tx *PKITransaction) ConsumePKIEnrollmentToken(ctx context.Context, digest string, consumedAt time.Time) (PKIEnrollmentTokenRow, bool, error) {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !validHexBytes(digest, 32) || consumedAt.IsZero() {
		return PKIEnrollmentTokenRow{}, false, pkiInvariant("enrollment token consumption fields are incomplete")
	}
	var row PKIEnrollmentTokenRow
	err := tx.db.WithContext(ctx).
		Where("token_digest_sha256 = ?", digest).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKIEnrollmentTokenRow{}, false, nil
	}
	if err != nil {
		return PKIEnrollmentTokenRow{}, false, err
	}
	result := tx.db.WithContext(ctx).
		Model(&PKIEnrollmentTokenRow{}).
		Where("id = ? AND consumed_at IS NULL AND expires_at > ?", row.ID, consumedAt).
		Update("consumed_at", consumedAt)
	if result.Error != nil {
		return PKIEnrollmentTokenRow{}, false, result.Error
	}
	if result.RowsAffected != 1 {
		return PKIEnrollmentTokenRow{}, false, nil
	}
	row.ConsumedAt = &consumedAt
	return row, true, nil
}

func (tx *PKITransaction) FindPKIEnrollmentReplayForUpdate(ctx context.Context, requestKey string) (PKIEnrollmentReplayRow, bool, error) {
	requestKey = strings.TrimSpace(requestKey)
	if requestKey == "" {
		return PKIEnrollmentReplayRow{}, false, pkiInvariant("enrollment replay request key is required")
	}
	var row PKIEnrollmentReplayRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("request_key = ?", requestKey).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKIEnrollmentReplayRow{}, false, nil
	}
	return row, err == nil, err
}

func (tx *PKITransaction) CreatePKIEnrollmentReplay(ctx context.Context, row PKIEnrollmentReplayRow) error {
	row.PKIDomainID = strings.TrimSpace(row.PKIDomainID)
	row.RequestKey = strings.TrimSpace(row.RequestKey)
	row.RequestFingerprint = strings.ToLower(strings.TrimSpace(row.RequestFingerprint))
	row.ResultJSON = strings.TrimSpace(row.ResultJSON)
	if strings.TrimSpace(row.ID) == "" || row.PKIDomainID == "" || row.RequestKey == "" ||
		!validHexBytes(row.RequestFingerprint, 32) || !json.Valid([]byte(row.ResultJSON)) || row.CreatedAt.IsZero() ||
		!row.ExpiresAt.After(row.CreatedAt) {
		return pkiInvariant("enrollment replay fields are incomplete")
	}
	return tx.db.WithContext(ctx).Create(&row).Error
}

func (tx *PKITransaction) CreatePKIConfirmationNonce(ctx context.Context, row PKIConfirmationNonceRow) error {
	row.PKIDomainID = strings.TrimSpace(row.PKIDomainID)
	row.DigestSHA256 = strings.ToLower(strings.TrimSpace(row.DigestSHA256))
	row.OperatorID = strings.TrimSpace(row.OperatorID)
	row.Action = strings.TrimSpace(row.Action)
	row.TargetID = strings.TrimSpace(row.TargetID)
	if strings.TrimSpace(row.ID) == "" || row.PKIDomainID == "" || !validHexBytes(row.DigestSHA256, 32) ||
		row.OperatorID == "" || row.Action == "" || row.ExpiresAt.IsZero() || row.CreatedAt.IsZero() ||
		!row.ExpiresAt.After(row.CreatedAt) {
		return pkiInvariant("confirmation nonce fields are incomplete")
	}
	return tx.db.WithContext(ctx).Create(&row).Error
}

// ConsumePKIConfirmationNonce atomically validates the complete approval
// binding and consumes it. Missing, expired, reused, or mismatched values all
// return the same false result so callers do not expose an approval oracle.
func (tx *PKITransaction) ConsumePKIConfirmationNonce(
	ctx context.Context,
	domainID, digest, operatorID, action, targetID string,
	consumedAt time.Time,
) (bool, error) {
	domainID = strings.TrimSpace(domainID)
	digest = strings.ToLower(strings.TrimSpace(digest))
	operatorID = strings.TrimSpace(operatorID)
	action = strings.TrimSpace(action)
	targetID = strings.TrimSpace(targetID)
	if domainID == "" || !validHexBytes(digest, 32) || operatorID == "" || action == "" || consumedAt.IsZero() {
		return false, pkiInvariant("confirmation nonce consumption fields are incomplete")
	}
	result := tx.db.WithContext(ctx).
		Model(&PKIConfirmationNonceRow{}).
		Where("pki_domain_id = ? AND digest_sha256 = ? AND operator_id = ? AND action = ? AND target_id = ? AND consumed_at IS NULL AND expires_at > CURRENT_TIMESTAMP",
			domainID, digest, operatorID, action, targetID).
		Update("consumed_at", gorm.Expr("CURRENT_TIMESTAMP"))
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (tx *PKITransaction) GetPKISecuritySnapshotForUpdate(ctx context.Context) (PKISecuritySnapshotRow, bool, error) {
	var row PKISecuritySnapshotRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, PKISecuritySnapshotSingletonID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKISecuritySnapshotRow{}, false, nil
	}
	return row, err == nil, err
}

// SavePKISecuritySnapshot persists the canonical signed snapshot monotonically.
// Replaying identical bytes for the same version is harmless; substituting a
// different signature/body at an already-published version is rejected.
func (tx *PKITransaction) SavePKISecuritySnapshot(ctx context.Context, row PKISecuritySnapshotRow) error {
	row.ID = PKISecuritySnapshotSingletonID
	row.PKIDomainID = strings.TrimSpace(row.PKIDomainID)
	row.SnapshotJSON = strings.TrimSpace(row.SnapshotJSON)
	if row.PKIDomainID == "" || row.PKIEpoch < 0 || row.SecurityRevision < 0 ||
		!json.Valid([]byte(row.SnapshotJSON)) || row.UpdatedAt.IsZero() {
		return pkiInvariant("security snapshot fields are incomplete")
	}
	current, found, err := tx.GetPKISecuritySnapshotForUpdate(ctx)
	if err != nil {
		return err
	}
	if !found {
		return tx.db.WithContext(ctx).Create(&row).Error
	}
	if current.PKIDomainID != row.PKIDomainID || row.PKIEpoch < current.PKIEpoch ||
		(row.PKIEpoch == current.PKIEpoch && row.SecurityRevision < current.SecurityRevision) {
		return pkiInvariant("security snapshot version regressed")
	}
	if row.PKIEpoch == current.PKIEpoch && row.SecurityRevision == current.SecurityRevision {
		if current.SnapshotJSON != row.SnapshotJSON {
			return pkiInvariant("security snapshot bytes changed at the same version")
		}
		return nil
	}
	result := tx.db.WithContext(ctx).
		Model(&PKISecuritySnapshotRow{}).
		Where("id = ? AND pki_epoch = ? AND security_revision = ?", current.ID, current.PKIEpoch, current.SecurityRevision).
		Updates(map[string]any{
			"pki_domain_id": row.PKIDomainID, "pki_epoch": row.PKIEpoch,
			"security_revision": row.SecurityRevision, "snapshot_json": row.SnapshotJSON,
			"updated_at": row.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return pkiInvariant("security snapshot changed concurrently")
	}
	return nil
}

func (tx *PKITransaction) GetRelayListenerForUpdate(ctx context.Context, agentID string, listenerID int) (RelayListenerRow, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || listenerID <= 0 {
		return RelayListenerRow{}, false, pkiInvariant("relay listener owner is incomplete")
	}
	var row RelayListenerRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND agent_id = ?", listenerID, agentID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RelayListenerRow{}, false, nil
	}
	return row, err == nil, err
}

// PKIStableAgentExistsForUpdate validates that a bound enrollment owner is a
// live control-plane agent in the same transaction as token creation or
// consumption. Row locking prevents a concurrent delete from racing a bound
// enrollment on databases that support SELECT FOR UPDATE; SQLite writes are
// serialized by GormStore.
func (tx *PKITransaction) PKIStableAgentExistsForUpdate(ctx context.Context, agentID string) (bool, error) {
	_, found, err := tx.GetPKIStableAgentForUpdate(ctx, agentID)
	return found, err
}

func (tx *PKITransaction) GetPKIStableAgentForUpdate(ctx context.Context, agentID string) (AgentRow, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return AgentRow{}, false, pkiInvariant("stable agent identifier is required")
	}
	var row AgentRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", agentID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentRow{}, false, nil
	}
	return row, err == nil, err
}

// UpsertPKIStableAgent binds the control-plane agent row to enrollment inside
// the same transaction that consumes the one-time token and issues the tunnel
// certificate. allowCreate is true only for a new-agent token.
func (tx *PKITransaction) UpsertPKIStableAgent(ctx context.Context, row AgentRow, allowCreate bool) (AgentRow, error) {
	row.ID = strings.TrimSpace(row.ID)
	row.Name = strings.TrimSpace(row.Name)
	row.AgentToken = strings.TrimSpace(row.AgentToken)
	row.AgentURL = strings.TrimSpace(row.AgentURL)
	row.Mode = strings.TrimSpace(row.Mode)
	if row.ID == "" || row.Name == "" || row.IsLocal {
		return AgentRow{}, pkiInvariant("remote PKI agent binding is incomplete")
	}
	if row.Mode == "" {
		row.Mode = "pull"
	}
	if row.LastApplyStatus == "" {
		row.LastApplyStatus = "success"
	}

	var current AgentRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", row.ID).
		First(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if !allowCreate {
			return AgentRow{}, pkiInvariant("bound PKI agent owner does not exist")
		}
		if row.AgentToken == "" {
			return AgentRow{}, pkiInvariant("new PKI agent control token is required")
		}
		if err := tx.db.WithContext(ctx).Create(&row).Error; err != nil {
			return AgentRow{}, err
		}
		return row, nil
	}
	if err != nil {
		return AgentRow{}, err
	}
	if allowCreate {
		return AgentRow{}, pkiInvariant("new PKI agent owner already exists")
	}
	if current.IsLocal {
		return AgentRow{}, pkiInvariant("embedded local agent cannot use remote enrollment")
	}
	// A bound tunnel re-enrollment must not rotate a still-valid control-plane
	// token. If revocation already cleared it, the supplied fresh token restores
	// control access for the same stable agent ID.
	if strings.TrimSpace(current.AgentToken) != "" {
		row.AgentToken = current.AgentToken
	}
	if row.AgentToken == "" {
		return AgentRow{}, pkiInvariant("bound PKI agent control token is unavailable")
	}
	result := tx.db.WithContext(ctx).Model(&AgentRow{}).Where("id = ?", row.ID).Updates(map[string]any{
		"name": row.Name, "agent_url": row.AgentURL, "agent_token": row.AgentToken,
		"version": row.Version, "platform": row.Platform, "tags": row.TagsJSON,
		"capabilities": row.CapabilitiesJSON, "mode": row.Mode, "last_apply_status": row.LastApplyStatus,
	})
	if result.Error != nil {
		return AgentRow{}, result.Error
	}
	if result.RowsAffected != 1 {
		return AgentRow{}, pkiInvariant("bound PKI agent owner changed concurrently")
	}
	return row, nil
}

func (tx *PKITransaction) DisablePKIStableAgentToken(ctx context.Context, agentID string) (bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false, pkiInvariant("PKI agent owner is required")
	}
	var agent AgentRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", agentID).
		First(&agent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, pkiInvariant("PKI agent owner is missing")
	}
	if err != nil {
		return false, err
	}
	if agent.IsLocal || strings.TrimSpace(agent.AgentToken) == "" {
		return true, nil
	}
	result := tx.db.WithContext(ctx).
		Model(&AgentRow{}).
		Where("id = ? AND is_local = ? AND agent_token = ?", agentID, false, agent.AgentToken).
		Update("agent_token", "")
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (tx *PKITransaction) SavePKISecurityAcknowledgement(ctx context.Context, agentID, acknowledgementJSON string, acknowledgedAt time.Time) error {
	agentID = strings.TrimSpace(agentID)
	acknowledgementJSON = strings.TrimSpace(acknowledgementJSON)
	if agentID == "" || acknowledgementJSON == "" || acknowledgedAt.IsZero() || !json.Valid([]byte(acknowledgementJSON)) {
		return pkiInvariant("PKI security acknowledgement is invalid")
	}
	result := tx.db.WithContext(ctx).
		Model(&AgentRow{}).
		Where("id = ?", agentID).
		Updates(map[string]any{"pki_security_ack": acknowledgementJSON, "pki_security_ack_at": acknowledgedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return pkiInvariant("PKI security acknowledgement owner is missing")
	}
	return nil
}

func (tx *PKITransaction) GetPKISettings(ctx context.Context) (PKISettingsRow, bool, error) {
	var row PKISettingsRow
	err := tx.db.WithContext(ctx).First(&row, PKISettingsSingletonID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKISettingsRow{}, false, nil
	}
	return row, err == nil, err
}

func (tx *PKITransaction) GetPKISettingsForUpdate(ctx context.Context) (PKISettingsRow, bool, error) {
	var row PKISettingsRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&row, PKISettingsSingletonID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKISettingsRow{}, false, nil
	}
	return row, err == nil, err
}

// SetPKISecurityRevision performs a compare-and-swap update while the caller's
// transaction holds the settings row. It is shared by revoke and emergency
// authority mutations so a security revision can never be published twice.
func (tx *PKITransaction) SetPKISecurityRevision(ctx context.Context, previous, next int64, updatedAt time.Time) error {
	if previous < 0 || next != previous+1 || updatedAt.IsZero() {
		return pkiInvariant("PKI security revision transition is invalid")
	}
	result := tx.db.WithContext(ctx).
		Model(&PKISettingsRow{}).
		Where("id = ? AND security_revision = ?", PKISettingsSingletonID, previous).
		Updates(map[string]any{"security_revision": next, "updated_at": updatedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return pkiInvariant("PKI security revision changed concurrently")
	}
	return nil
}

func (tx *PKITransaction) SetPKIUpgradeState(ctx context.Context, current, next string, updatedAt time.Time) error {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" || next == "" || updatedAt.IsZero() {
		return pkiInvariant("PKI upgrade state transition is incomplete")
	}
	result := tx.db.WithContext(ctx).
		Model(&PKISettingsRow{}).
		Where("id = ? AND upgrade_state = ?", PKISettingsSingletonID, current).
		Updates(map[string]any{"upgrade_state": next, "updated_at": updatedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return pkiInvariant("PKI upgrade state changed concurrently")
	}
	return nil
}

func (tx *PKITransaction) GetPKIAuthority(ctx context.Context, id string) (PKIAuthorityRow, bool, error) {
	var row PKIAuthorityRow
	err := tx.db.WithContext(ctx).Where("id = ?", strings.TrimSpace(id)).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKIAuthorityRow{}, false, nil
	}
	return row, err == nil, err
}

func (tx *PKITransaction) GetActivePKIAuthorityForUpdate(ctx context.Context, domainID string) (PKIAuthorityRow, bool, error) {
	var row PKIAuthorityRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("pki_domain_id = ? AND status = ?", strings.TrimSpace(domainID), "active").
		Order("generation DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKIAuthorityRow{}, false, nil
	}
	return row, err == nil, err
}

// FindPKIIdentityForUpdate locks the stable owner slot where the database
// supports row locks. SQLite writers are already serialized by GormStore.
func (tx *PKITransaction) FindPKIIdentityForUpdate(ctx context.Context, domainID, kind, agentID, listenerID string) (PKIIdentityRow, bool, error) {
	var row PKIIdentityRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("pki_domain_id = ? AND kind = ? AND agent_id = ? AND listener_id = ?",
			strings.TrimSpace(domainID), strings.TrimSpace(kind), strings.TrimSpace(agentID), strings.TrimSpace(listenerID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKIIdentityRow{}, false, nil
	}
	return row, err == nil, err
}

func (tx *PKITransaction) GetPKIIdentityForUpdate(ctx context.Context, identityID string) (PKIIdentityRow, bool, error) {
	var row PKIIdentityRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", strings.TrimSpace(identityID)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKIIdentityRow{}, false, nil
	}
	return row, err == nil, err
}

func (tx *PKITransaction) ListPKIIdentityCertificatesForUpdate(ctx context.Context, identityID string) ([]PKICertificateRow, error) {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return nil, pkiInvariant("PKI certificate owner is required")
	}
	var rows []PKICertificateRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("identity_id = ?", identityID).
		Order("created_at ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

// RevokePKIIdentityCertificates updates the identity and every credential that
// could still authenticate it. Historical expired credentials stay immutable,
// while active, pending and superseded serials become permanently revoked.
func (tx *PKITransaction) RevokePKIIdentityCertificates(ctx context.Context, identityID, reason string, revokedAt time.Time) (PKIIdentityRow, []PKICertificateRow, error) {
	identityID = strings.TrimSpace(identityID)
	reason = strings.TrimSpace(reason)
	if identityID == "" || reason == "" || revokedAt.IsZero() {
		return PKIIdentityRow{}, nil, pkiInvariant("PKI revocation fields are incomplete")
	}
	identity, found, err := tx.GetPKIIdentityForUpdate(ctx, identityID)
	if err != nil {
		return PKIIdentityRow{}, nil, err
	}
	if !found || identity.State == PKIIdentityStateRevoked {
		return PKIIdentityRow{}, nil, pkiInvariant("PKI identity is missing or already revoked")
	}
	certificates, err := tx.ListPKIIdentityCertificatesForUpdate(ctx, identityID)
	if err != nil {
		return PKIIdentityRow{}, nil, err
	}
	revoked := make([]PKICertificateRow, 0, len(certificates))
	for _, certificate := range certificates {
		switch certificate.Status {
		case PKICertificateStatusActive, PKICertificateStatusPending, PKICertificateStatusSuperseded:
			revoked = append(revoked, certificate)
		}
	}
	result := tx.db.WithContext(ctx).
		Model(&PKICertificateRow{}).
		Where("identity_id = ? AND status IN ?", identityID, []string{PKICertificateStatusActive, PKICertificateStatusPending, PKICertificateStatusSuperseded}).
		Updates(map[string]any{
			"status": PKICertificateStatusRevoked, "active_identity_purpose_key": nil,
			"superseded_by_id": nil,
			"revoked_at":       revokedAt, "revoked_reason": reason, "updated_at": revokedAt,
		})
	if result.Error != nil {
		return PKIIdentityRow{}, nil, result.Error
	}
	if result.RowsAffected != int64(len(revoked)) {
		return PKIIdentityRow{}, nil, pkiInvariant("PKI certificate revocation changed concurrently")
	}
	for index := range revoked {
		revoked[index].Status = PKICertificateStatusRevoked
		revoked[index].ActiveIdentityPurposeKey = nil
		revoked[index].SupersededByID = nil
		revoked[index].RevokedAt = &revokedAt
		revoked[index].RevokedReason = reason
		revoked[index].UpdatedAt = revokedAt
	}
	result = tx.db.WithContext(ctx).
		Model(&PKIIdentityRow{}).
		Where("id = ? AND state <> ?", identityID, PKIIdentityStateRevoked).
		Updates(map[string]any{
			"state": PKIIdentityStateRevoked, "current_certificate_id": nil,
			"revoked_at": revokedAt, "revoked_reason": reason, "updated_at": revokedAt,
		})
	if result.Error != nil {
		return PKIIdentityRow{}, nil, result.Error
	}
	if result.RowsAffected != 1 {
		return PKIIdentityRow{}, nil, pkiInvariant("PKI identity revocation changed concurrently")
	}
	identity.State = PKIIdentityStateRevoked
	identity.CurrentCertificateID = nil
	identity.RevokedAt = &revokedAt
	identity.RevokedReason = reason
	return identity, revoked, nil
}

func (tx *PKITransaction) ListTrustedPKIAuthoritiesForUpdate(ctx context.Context, domainID string) ([]PKIAuthorityRow, error) {
	domainID = strings.TrimSpace(domainID)
	if domainID == "" {
		return nil, pkiInvariant("PKI authority domain is required")
	}
	var rows []PKIAuthorityRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("pki_domain_id = ? AND status IN ?", domainID, []string{"active", "prepared", "retiring"}).
		Order("generation ASC").
		Find(&rows).Error
	return rows, err
}

func (tx *PKITransaction) GetPKICertificateForUpdate(ctx context.Context, id string) (PKICertificateRow, bool, error) {
	var row PKICertificateRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", strings.TrimSpace(id)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKICertificateRow{}, false, nil
	}
	return row, err == nil, err
}

func (tx *PKITransaction) SetPKIIdentityCurrentCertificate(ctx context.Context, identityID, certificateID string, updatedAt time.Time) error {
	identityID = strings.TrimSpace(identityID)
	certificateID = strings.TrimSpace(certificateID)
	if identityID == "" || certificateID == "" || updatedAt.IsZero() {
		return pkiInvariant("identity certificate update fields are incomplete")
	}
	result := tx.db.WithContext(ctx).
		Model(&PKIIdentityRow{}).
		Where("id = ?", identityID).
		Updates(map[string]any{
			"state":                  PKIIdentityStateActive,
			"current_certificate_id": certificateID,
			"revoked_at":             nil,
			"revoked_reason":         "",
			"updated_at":             updatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return pkiInvariant("identity certificate update target is missing")
	}
	return nil
}

func (tx *PKITransaction) SupersedePKICertificate(ctx context.Context, certificateID, replacementID string, updatedAt time.Time) (bool, error) {
	certificateID = strings.TrimSpace(certificateID)
	replacementID = strings.TrimSpace(replacementID)
	if certificateID == "" || replacementID == "" || certificateID == replacementID || updatedAt.IsZero() {
		return false, pkiInvariant("certificate supersession fields are incomplete")
	}
	result := tx.db.WithContext(ctx).
		Model(&PKICertificateRow{}).
		Where("id = ? AND status = ?", certificateID, PKICertificateStatusActive).
		Updates(map[string]any{
			"status":                      PKICertificateStatusSuperseded,
			"active_identity_purpose_key": nil,
			"superseded_by_id":            replacementID,
			"updated_at":                  updatedAt,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func (tx *PKITransaction) CreatePKILifecycleJob(ctx context.Context, row PKILifecycleJobRow) error {
	if strings.TrimSpace(row.ID) == "" || strings.TrimSpace(row.PKIDomainID) == "" || strings.TrimSpace(row.TargetType) == "" || strings.TrimSpace(row.TargetID) == "" || strings.TrimSpace(row.Kind) == "" || strings.TrimSpace(row.Phase) == "" || strings.TrimSpace(row.IdempotencyKey) == "" || row.Attempt < 0 || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
		return pkiInvariant("lifecycle job fields are incomplete")
	}
	row.ActiveTargetKey = nil
	switch row.State {
	case PKILifecycleJobStatePending, PKILifecycleJobStateRunning:
		key := pkiUniqueSlot(row.PKIDomainID, row.TargetType, row.TargetID, row.Kind)
		row.ActiveTargetKey = &key
	case PKILifecycleJobStateSucceeded, PKILifecycleJobStateFailed, PKILifecycleJobStateCancelled:
	default:
		return pkiInvariant("lifecycle job state is invalid")
	}
	return tx.db.WithContext(ctx).Create(&row).Error
}

func (tx *PKITransaction) FindPKILifecycleJobByIdempotencyForUpdate(ctx context.Context, key string) (PKILifecycleJobRow, bool, error) {
	var row PKILifecycleJobRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("idempotency_key = ?", strings.TrimSpace(key)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKILifecycleJobRow{}, false, nil
	}
	return row, err == nil, err
}

func (tx *PKITransaction) FindActivePKILifecycleJobForTargetForUpdate(ctx context.Context, domainID, targetType, targetID, kind string) (PKILifecycleJobRow, bool, error) {
	activeKey := pkiUniqueSlot(strings.TrimSpace(domainID), strings.TrimSpace(targetType), strings.TrimSpace(targetID), strings.TrimSpace(kind))
	var row PKILifecycleJobRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("active_target_key = ?", activeKey).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKILifecycleJobRow{}, false, nil
	}
	return row, err == nil, err
}

func (tx *PKITransaction) GetPKILifecycleJobForUpdate(ctx context.Context, id string) (PKILifecycleJobRow, bool, error) {
	var row PKILifecycleJobRow
	err := tx.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? OR operation_id = ?", strings.TrimSpace(id), strings.TrimSpace(id)).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKILifecycleJobRow{}, false, nil
	}
	return row, err == nil, err
}

func (tx *PKITransaction) UpdatePKILifecycleJob(ctx context.Context, previous PKILifecycleJobRow, next PKILifecycleJobRow) error {
	if strings.TrimSpace(previous.ID) == "" || next.ID != previous.ID || next.PKIDomainID != previous.PKIDomainID ||
		next.TargetType != previous.TargetType || next.TargetID != previous.TargetID || next.Kind != previous.Kind ||
		next.IdempotencyKey != previous.IdempotencyKey || next.Attempt < previous.Attempt || next.UpdatedAt.IsZero() {
		return pkiInvariant("lifecycle job transition is invalid")
	}
	var activeTargetKey *string
	switch next.State {
	case PKILifecycleJobStatePending, PKILifecycleJobStateRunning:
		key := pkiUniqueSlot(next.PKIDomainID, next.TargetType, next.TargetID, next.Kind)
		activeTargetKey = &key
	case PKILifecycleJobStateSucceeded, PKILifecycleJobStateFailed, PKILifecycleJobStateCancelled:
	default:
		return pkiInvariant("lifecycle job state is invalid")
	}
	result := tx.db.WithContext(ctx).
		Model(&PKILifecycleJobRow{}).
		Where("id = ? AND phase = ? AND state = ? AND attempt = ? AND updated_at = ?", previous.ID, previous.Phase, previous.State, previous.Attempt, previous.UpdatedAt).
		Updates(map[string]any{
			"phase": next.Phase, "state": next.State, "attempt": next.Attempt,
			"next_attempt_at": next.NextAttemptAt, "deadline": next.Deadline,
			"last_error": next.LastError, "operation_id": next.OperationID,
			"active_target_key": activeTargetKey, "lease_owner": next.LeaseOwner,
			"lease_deadline": next.LeaseDeadline, "updated_at": next.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return pkiInvariant("lifecycle job changed concurrently")
	}
	return nil
}

func (tx *PKITransaction) AppendPKIEvent(ctx context.Context, row PKIEventRow) error {
	if strings.TrimSpace(row.ID) == "" || strings.TrimSpace(row.PKIDomainID) == "" || strings.TrimSpace(row.Type) == "" || row.OccurredAt.IsZero() || strings.TrimSpace(row.Source) == "" || strings.TrimSpace(row.ObjectType) == "" || strings.TrimSpace(row.ObjectID) == "" || strings.TrimSpace(row.Result) == "" || row.SecurityRevision < 0 {
		return pkiInvariant("event fields are incomplete")
	}
	if strings.TrimSpace(row.DetailsJSON) == "" {
		row.DetailsJSON = "{}"
	}
	if !json.Valid([]byte(row.DetailsJSON)) {
		return pkiInvariant("event details must be valid JSON")
	}
	return tx.db.WithContext(ctx).Create(&row).Error
}

func (tx *PKITransaction) CreatePKIInstanceLease(ctx context.Context, row PKIInstanceLeaseRow) error {
	row.ID = PKILeaseSingletonID
	if strings.TrimSpace(row.PKIDomainID) == "" || strings.TrimSpace(row.InstanceID) == "" || strings.TrimSpace(row.LeaseTerm) == "" || row.LeaseDeadline.IsZero() || row.PKIEpoch < 0 || strings.TrimSpace(row.State) == "" || row.UpdatedAt.IsZero() {
		return pkiInvariant("instance lease fields are incomplete")
	}
	return tx.db.WithContext(ctx).Create(&row).Error
}

type PKICanonicalState struct {
	Settings           *PKISettingsRow
	Authorities        []PKIAuthorityRow
	Identities         []PKIIdentityRow
	Certificates       []PKICertificateRow
	EnrollmentTokens   []PKIEnrollmentTokenRow
	EnrollmentReplays  []PKIEnrollmentReplayRow
	ConfirmationNonces []PKIConfirmationNonceRow
	SecuritySnapshot   *PKISecuritySnapshotRow
	LifecycleJobs      []PKILifecycleJobRow
	Events             []PKIEventRow
	InstanceLease      *PKIInstanceLeaseRow
}

func (tx *PKITransaction) LoadPKICanonicalState(ctx context.Context) (PKICanonicalState, error) {
	return loadPKICanonicalStateFromDB(ctx, tx.db)
}

// HasPKICanonicalSchema lets optional PKI consumers distinguish an
// uninitialised/legacy store from a corrupt canonical PKI store. Once the
// settings table exists, failures while loading canonical state remain fatal.
func (s *GormStore) HasPKICanonicalSchema(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return s.db.WithContext(ctx).Migrator().HasTable(&PKISettingsRow{}), nil
}

// LoadLatestPKISecuritySnapshot returns the canonical signed snapshot that
// must accompany ordinary revision payloads. Once tunnel mTLS is activated,
// absence or corruption is a hard safety error rather than an omitted field.
func (s *GormStore) LoadLatestPKISecuritySnapshot(ctx context.Context) (*PKISecuritySnapshot, error) {
	present, err := s.HasPKICanonicalSchema(ctx)
	if err != nil || !present {
		return nil, err
	}
	state, err := s.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	if state.Settings == nil {
		return nil, nil
	}
	settings := *state.Settings
	if state.SecuritySnapshot == nil {
		if settings.UpgradeState == PKIUpgradeStateTunnelMTLSOnly {
			return nil, pkiInvariant("activated tunnel mTLS has no signed security snapshot")
		}
		return nil, nil
	}
	snapshot, err := ValidateCanonicalPKISecuritySnapshot(state)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// ValidateCanonicalPKISecuritySnapshot verifies the complete persisted
// security envelope against the same canonical authority, trust and
// revocation graph that produced it. Callers use this before bootstrap,
// migration, backup export and restore so a structurally plausible but stale
// or forged snapshot can never become the advertised security state.
func ValidateCanonicalPKISecuritySnapshot(state PKICanonicalState) (PKISecuritySnapshot, error) {
	if state.Settings == nil || state.SecuritySnapshot == nil {
		return PKISecuritySnapshot{}, pkiInvariant("canonical signed security snapshot is missing")
	}
	settings := *state.Settings
	row := *state.SecuritySnapshot
	if row.PKIDomainID != settings.PKIDomainID || row.PKIEpoch != settings.PKIEpoch || row.SecurityRevision != settings.SecurityRevision {
		return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot row does not match canonical settings")
	}
	decoder := json.NewDecoder(strings.NewReader(row.SnapshotJSON))
	decoder.DisallowUnknownFields()
	var snapshot PKISecuritySnapshot
	if !json.Valid([]byte(row.SnapshotJSON)) || decoder.Decode(&snapshot) != nil {
		return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot JSON is invalid")
	}
	if snapshot.PKIDomainID != settings.PKIDomainID || snapshot.PKIEpoch != settings.PKIEpoch ||
		snapshot.SecurityRevision != settings.SecurityRevision || !snapshot.Full || snapshot.IssuedAt.IsZero() ||
		snapshot.SignerGeneration <= 0 || len(snapshot.Signature) == 0 {
		return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot version is not canonical")
	}

	trusted := make([]PKIAuthorityRow, 0)
	for _, authority := range state.Authorities {
		switch authority.Status {
		case "active", "prepared", "retiring":
			trusted = append(trusted, authority)
		}
	}
	sort.Slice(trusted, func(left, right int) bool { return trusted[left].Generation < trusted[right].Generation })
	if len(trusted) == 0 || len(snapshot.TrustRoots) != len(trusted) {
		return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot trust set is incomplete")
	}
	trustGenerations := make([]int64, len(trusted))
	trustDescriptors := make([]canonicalPKISnapshotTrustRoot, len(trusted))
	var signerCertificate *x509.Certificate
	for index, authority := range trusted {
		root := snapshot.TrustRoots[index]
		if root.AuthorityID != authority.ID || root.Generation != authority.Generation || root.Status != authority.Status ||
			root.CertificatePEM != authority.CertificatePEM || !strings.EqualFold(root.FingerprintSHA256, authority.FingerprintSHA256) ||
			!root.NotBefore.Equal(authority.NotBefore) || !root.NotAfter.Equal(authority.NotAfter) {
			return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot trust root does not match canonical authority")
		}
		parsed, err := validatePKIAuthorityCertificate(authority)
		if err != nil {
			return PKISecuritySnapshot{}, err
		}
		trustGenerations[index] = authority.Generation
		trustDescriptors[index] = canonicalPKISnapshotTrustRoot{
			AuthorityID: authority.ID, Generation: authority.Generation, Status: authority.Status,
			FingerprintSHA256: strings.ToLower(authority.FingerprintSHA256), NotBefore: authority.NotBefore.UTC(), NotAfter: authority.NotAfter.UTC(),
		}
		if authority.Generation == snapshot.SignerGeneration {
			if authority.Status != "active" {
				return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot signer is not active")
			}
			signerCertificate = parsed
		}
	}
	if signerCertificate == nil {
		return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot signer generation is not trusted")
	}

	revokedIdentities := make([]string, 0)
	for _, identity := range state.Identities {
		if identity.State == PKIIdentityStateRevoked {
			revokedIdentities = append(revokedIdentities, identity.ID)
		}
	}
	revokedSerials := make([]string, 0)
	for _, certificate := range state.Certificates {
		if certificate.Status == PKICertificateStatusRevoked {
			revokedSerials = append(revokedSerials, certificate.SerialHex)
		}
	}
	sort.Strings(revokedIdentities)
	sort.Strings(revokedSerials)
	if !equalCanonicalPKIStringsExact(snapshot.RevokedIdentityIDs, revokedIdentities) ||
		!equalCanonicalPKIStringsExact(snapshot.RevokedSerials, revokedSerials) {
		return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot revocations do not match canonical state")
	}
	payload, err := json.Marshal(canonicalPKISnapshotPayload{
		PKIDomainID: snapshot.PKIDomainID,
		Version: canonicalPKISnapshotVersion{
			Version: canonicalPKISecurityVersion{PKIEpoch: snapshot.PKIEpoch, SecurityRevision: snapshot.SecurityRevision},
			Full:    snapshot.Full,
		},
		IssuedAt: snapshot.IssuedAt.UTC(), TrustGenerations: trustGenerations, TrustRoots: trustDescriptors,
		RevokedIdentityIDs: cloneCanonicalPKIStrings(snapshot.RevokedIdentityIDs),
		RevokedSerials:     cloneCanonicalPKIStrings(snapshot.RevokedSerials),
	})
	if err != nil {
		return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot canonical payload cannot be encoded")
	}
	publicKey, ok := signerCertificate.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot signer key is invalid")
	}
	digest := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(publicKey, digest[:], snapshot.Signature) {
		return PKISecuritySnapshot{}, pkiInvariant("signed security snapshot signature is invalid")
	}
	return snapshot, nil
}

type canonicalPKISecurityVersion struct {
	PKIEpoch         int64 `json:"pki_epoch"`
	SecurityRevision int64 `json:"security_revision"`
}

type canonicalPKISnapshotVersion struct {
	Version canonicalPKISecurityVersion `json:"version"`
	Full    bool                        `json:"full"`
}

type canonicalPKISnapshotTrustRoot struct {
	AuthorityID       string    `json:"authority_id"`
	Generation        int64     `json:"generation"`
	Status            string    `json:"status"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
}

type canonicalPKISnapshotPayload struct {
	PKIDomainID        string                          `json:"pki_domain_id"`
	Version            canonicalPKISnapshotVersion     `json:"version"`
	IssuedAt           time.Time                       `json:"issued_at"`
	TrustGenerations   []int64                         `json:"trust_generations"`
	TrustRoots         []canonicalPKISnapshotTrustRoot `json:"trust_roots"`
	RevokedIdentityIDs []string                        `json:"revoked_identity_ids"`
	RevokedSerials     []string                        `json:"revoked_serials"`
}

func equalCanonicalPKIStringsExact(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneCanonicalPKIStrings(values []string) []string {
	result := make([]string, len(values))
	copy(result, values)
	return result
}

func (s *GormStore) LoadPKICanonicalState(ctx context.Context) (PKICanonicalState, error) {
	if s.transactionScoped {
		return loadPKICanonicalStateFromDB(ctx, s.db)
	}
	var state PKICanonicalState
	var loadErr error
	options := &sql.TxOptions{ReadOnly: true}
	if s.driver == "postgres" || s.driver == "mysql" {
		options.Isolation = sql.LevelRepeatableRead
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, loadErr = loadPKICanonicalStateFromDB(ctx, tx)
		return loadErr
	}, options)
	if err != nil {
		return PKICanonicalState{}, err
	}
	return state, nil
}

func loadPKICanonicalStateFromDB(ctx context.Context, db *gorm.DB) (PKICanonicalState, error) {
	state := PKICanonicalState{}
	var settings PKISettingsRow
	if err := db.WithContext(ctx).First(&settings, PKISettingsSingletonID).Error; err == nil {
		state.Settings = &settings
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return PKICanonicalState{}, err
	}
	queries := []struct {
		value any
		order string
	}{
		{&state.Authorities, "generation ASC"},
		{&state.Identities, "id ASC"},
		{&state.Certificates, "created_at ASC, id ASC"},
		{&state.EnrollmentTokens, "created_at ASC, id ASC"},
		{&state.EnrollmentReplays, "created_at ASC, id ASC"},
		{&state.ConfirmationNonces, "created_at ASC, id ASC"},
		{&state.LifecycleJobs, "created_at ASC, id ASC"},
		{&state.Events, "occurred_at ASC, id ASC"},
	}
	for _, query := range queries {
		if err := db.WithContext(ctx).Order(query.order).Find(query.value).Error; err != nil {
			return PKICanonicalState{}, err
		}
	}
	var securitySnapshot PKISecuritySnapshotRow
	if err := db.WithContext(ctx).First(&securitySnapshot, PKISecuritySnapshotSingletonID).Error; err == nil {
		state.SecuritySnapshot = &securitySnapshot
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return PKICanonicalState{}, err
	}
	var lease PKIInstanceLeaseRow
	if err := db.WithContext(ctx).First(&lease, PKILeaseSingletonID).Error; err == nil {
		state.InstanceLease = &lease
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return PKICanonicalState{}, err
	}
	return state, nil
}

func validatePKICanonicalRelationships(ctx context.Context, db *gorm.DB) error {
	state, err := loadPKICanonicalStateFromDB(ctx, db)
	if err != nil {
		return err
	}
	hasFacts := len(state.Authorities)+len(state.Identities)+len(state.Certificates)+len(state.EnrollmentTokens)+
		len(state.EnrollmentReplays)+len(state.ConfirmationNonces)+len(state.LifecycleJobs)+len(state.Events) > 0 ||
		state.SecuritySnapshot != nil || state.InstanceLease != nil
	if state.Settings == nil {
		if hasFacts {
			return pkiInvariant("PKI settings are required before canonical facts")
		}
		return nil
	}
	domainID := strings.TrimSpace(state.Settings.PKIDomainID)
	var agentRows []AgentRow
	if err := db.WithContext(ctx).Select("id").Find(&agentRows).Error; err != nil {
		return err
	}
	agents := make(map[string]struct{}, len(agentRows))
	for _, agent := range agentRows {
		agents[agent.ID] = struct{}{}
	}
	var listenerRows []RelayListenerRow
	if err := db.WithContext(ctx).Find(&listenerRows).Error; err != nil {
		return err
	}
	listeners := make(map[string]RelayListenerRow, len(listenerRows))
	for _, listener := range listenerRows {
		listeners[pkiUniqueSlot(listener.AgentID, fmt.Sprint(listener.ID))] = listener
	}
	authorities := make(map[string]PKIAuthorityRow, len(state.Authorities))
	authoritiesByGeneration := make(map[int64]PKIAuthorityRow, len(state.Authorities))
	parsedAuthorities := make(map[string]*x509.Certificate, len(state.Authorities))
	for _, authority := range state.Authorities {
		if authority.PKIDomainID != domainID {
			return pkiInvariant(fmt.Sprintf("authority %q belongs to a different PKI domain", authority.ID))
		}
		parsed, parseErr := validatePKIAuthorityCertificate(authority)
		if parseErr != nil {
			return parseErr
		}
		authorities[authority.ID] = authority
		authoritiesByGeneration[authority.Generation] = authority
		parsedAuthorities[authority.ID] = parsed
	}
	identities := make(map[string]PKIIdentityRow, len(state.Identities))
	for _, identity := range state.Identities {
		if identity.PKIDomainID != domainID {
			return pkiInvariant(fmt.Sprintf("identity %q belongs to a different PKI domain", identity.ID))
		}
		if _, found := agents[identity.AgentID]; !found && identity.State != PKIIdentityStateRevoked {
			return pkiInvariant(fmt.Sprintf("identity %q references missing agent %q", identity.ID, identity.AgentID))
		}
		if identity.Kind == PKIIdentityKindListener && identity.State != PKIIdentityStateRevoked {
			if _, found := listeners[pkiUniqueSlot(identity.AgentID, identity.ListenerID)]; !found {
				return pkiInvariant(fmt.Sprintf("identity %q references missing relay listener %q", identity.ID, identity.ListenerID))
			}
		}
		identities[identity.ID] = identity
	}
	for _, replay := range state.EnrollmentReplays {
		if replay.PKIDomainID != domainID || !validHexBytes(strings.TrimSpace(replay.RequestFingerprint), 32) ||
			!json.Valid([]byte(replay.ResultJSON)) || !replay.ExpiresAt.After(replay.CreatedAt) {
			return pkiInvariant(fmt.Sprintf("enrollment replay %q is invalid", replay.ID))
		}
	}
	for _, nonce := range state.ConfirmationNonces {
		if nonce.PKIDomainID != domainID || !validHexBytes(strings.TrimSpace(nonce.DigestSHA256), 32) || !nonce.ExpiresAt.After(nonce.CreatedAt) {
			return pkiInvariant(fmt.Sprintf("confirmation nonce %q is invalid", nonce.ID))
		}
	}
	if state.SecuritySnapshot != nil {
		if _, err := ValidateCanonicalPKISecuritySnapshot(state); err != nil {
			return err
		}
	} else if state.Settings.SecurityRevision > 0 {
		return pkiInvariant("non-initial PKI security revision has no signed snapshot")
	}
	certificates := make(map[string]PKICertificateRow, len(state.Certificates))
	for _, certificate := range state.Certificates {
		identity, identityFound := identities[certificate.IdentityID]
		if !identityFound {
			return pkiInvariant(fmt.Sprintf("certificate %q references missing identity %q", certificate.ID, certificate.IdentityID))
		}
		authority, authorityFound := authorities[certificate.AuthorityID]
		if !authorityFound {
			return pkiInvariant(fmt.Sprintf("certificate %q references missing authority %q", certificate.ID, certificate.AuthorityID))
		}
		if identity.PKIDomainID != authority.PKIDomainID || authority.PKIDomainID != domainID {
			return pkiInvariant(fmt.Sprintf("certificate %q crosses PKI domains", certificate.ID))
		}
		if certificate.CAGeneration != authority.Generation {
			return pkiInvariant(fmt.Sprintf("certificate %q CA generation does not match authority %q", certificate.ID, authority.ID))
		}
		if (identity.Kind == PKIIdentityKindAgent && certificate.Purpose != PKICertificatePurposeClient) || (identity.Kind == PKIIdentityKindListener && certificate.Purpose != PKICertificatePurposeServer) {
			return pkiInvariant(fmt.Sprintf("certificate %q purpose does not match identity kind", certificate.ID))
		}
		parsed, parseErr := validatePKILeafCertificate(certificate)
		if parseErr != nil {
			return parseErr
		}
		if issuerErr := validatePKILeafIssuer(parsed, parsedAuthorities[authority.ID], certificate.Purpose); issuerErr != nil {
			return pkiInvariant(fmt.Sprintf("certificate %q does not verify against authority %q: %v", certificate.ID, authority.ID, issuerErr))
		}
		var listener *RelayListenerRow
		if identity.Kind == PKIIdentityKindListener {
			if current, found := listeners[pkiUniqueSlot(identity.AgentID, identity.ListenerID)]; found {
				listener = &current
			}
		}
		if ownerErr := validatePKILeafOwner(parsed, identity, listener, domainID); ownerErr != nil {
			return pkiInvariant(fmt.Sprintf("certificate %q owner binding is invalid: %v", certificate.ID, ownerErr))
		}
		certificates[certificate.ID] = certificate
	}
	for _, identity := range state.Identities {
		if identity.CurrentCertificateID == nil {
			if identity.State == PKIIdentityStateActive {
				return pkiInvariant(fmt.Sprintf("active identity %q has no current certificate", identity.ID))
			}
			continue
		}
		certificate, found := certificates[*identity.CurrentCertificateID]
		if !found {
			return pkiInvariant(fmt.Sprintf("identity %q references missing current certificate %q", identity.ID, *identity.CurrentCertificateID))
		}
		if certificate.IdentityID != identity.ID {
			return pkiInvariant(fmt.Sprintf("identity %q current certificate belongs to another identity", identity.ID))
		}
		if identity.State == PKIIdentityStateActive && certificate.Status != PKICertificateStatusActive {
			return pkiInvariant(fmt.Sprintf("active identity %q current certificate is not active", identity.ID))
		}
		if identity.State == PKIIdentityStateEnrollmentRequired {
			return pkiInvariant(fmt.Sprintf("enrollment-required identity %q has a current certificate", identity.ID))
		}
		if identity.State == PKIIdentityStateRevoked && certificate.Status != PKICertificateStatusRevoked {
			return pkiInvariant(fmt.Sprintf("revoked identity %q current certificate is not revoked", identity.ID))
		}
	}
	for _, certificate := range state.Certificates {
		if certificate.Status == PKICertificateStatusActive {
			identity := identities[certificate.IdentityID]
			if identity.State != PKIIdentityStateActive || identity.CurrentCertificateID == nil || *identity.CurrentCertificateID != certificate.ID {
				return pkiInvariant(fmt.Sprintf("active certificate %q is not its identity's current certificate", certificate.ID))
			}
		}
	}
	if err := validatePKISupersessionGraph(certificates); err != nil {
		return err
	}
	for _, event := range state.Events {
		if event.PKIDomainID != domainID {
			return pkiInvariant(fmt.Sprintf("event %q belongs to a different PKI domain", event.ID))
		}
		if event.CertificateID != nil {
			if _, found := certificates[*event.CertificateID]; !found {
				return pkiInvariant(fmt.Sprintf("event %q references missing certificate", event.ID))
			}
		}
		if event.CAGeneration != nil {
			if _, found := authoritiesByGeneration[*event.CAGeneration]; !found {
				return pkiInvariant(fmt.Sprintf("event %q references missing CA generation", event.ID))
			}
		}
	}
	for _, job := range state.LifecycleJobs {
		if job.PKIDomainID != domainID {
			return pkiInvariant(fmt.Sprintf("lifecycle job %q belongs to a different PKI domain", job.ID))
		}
		switch job.TargetType {
		case "identity":
			if _, found := identities[job.TargetID]; !found {
				return pkiInvariant(fmt.Sprintf("lifecycle job %q references missing identity", job.ID))
			}
		case "certificate":
			if _, found := certificates[job.TargetID]; !found {
				return pkiInvariant(fmt.Sprintf("lifecycle job %q references missing certificate", job.ID))
			}
		case "authority":
			if _, found := authorities[job.TargetID]; !found {
				return pkiInvariant(fmt.Sprintf("lifecycle job %q references missing authority", job.ID))
			}
		}
	}
	if state.InstanceLease != nil {
		if state.InstanceLease.PKIDomainID != domainID || state.InstanceLease.PKIEpoch != state.Settings.PKIEpoch || strings.TrimSpace(state.InstanceLease.LeaseTerm) == "" {
			return pkiInvariant("instance lease domain or epoch does not match PKI settings")
		}
	}
	return nil
}

type LegacyPKIMigrationSources struct {
	ManagedCertificates []ManagedCertificateRow
	RelayListeners      []RelayListenerRow
}

// InspectLegacyPKIMigrationSources is deliberately read-only. It identifies
// generic internal CA/relay facts that a maintenance migration may consume,
// without copying them into or mutating the public managed-certificate domain.
func (s *GormStore) InspectLegacyPKIMigrationSources(ctx context.Context) (LegacyPKIMigrationSources, error) {
	var certificates []ManagedCertificateRow
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&certificates).Error; err != nil {
		return LegacyPKIMigrationSources{}, err
	}
	internal := make([]ManagedCertificateRow, 0)
	for _, row := range certificates {
		certificateType := strings.ToLower(strings.TrimSpace(row.CertificateType))
		usage := strings.ToLower(strings.TrimSpace(row.Usage))
		if certificateType == "internal_ca" || usage == "relay_ca" || usage == "relay_tunnel" || strings.EqualFold(strings.TrimSpace(row.Domain), "__relay-ca.internal") {
			internal = append(internal, row)
		}
	}
	var listeners []RelayListenerRow
	if err := s.db.WithContext(ctx).Order("agent_id ASC, id ASC").Find(&listeners).Error; err != nil {
		return LegacyPKIMigrationSources{}, err
	}
	return LegacyPKIMigrationSources{ManagedCertificates: internal, RelayListeners: listeners}, nil
}

func pkiInvariant(message string) error {
	return fmt.Errorf("%w: %s", ErrPKIInvariant, message)
}

func pkiUniqueSlot(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func validatePKIAuthorityCertificate(row PKIAuthorityRow) (*x509.Certificate, error) {
	certificate, err := parseSinglePKICertificatePEM(row.CertificatePEM)
	if err != nil {
		return nil, err
	}
	if err := validatePKICertificateCryptoProfile(certificate); err != nil {
		return nil, err
	}
	if !certificate.BasicConstraintsValid || !certificate.IsCA || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, pkiInvariant("authority certificate is not a certificate-signing CA")
	}
	if err := certificate.CheckSignatureFrom(certificate); err != nil {
		return nil, pkiInvariant("authority certificate must be self-signed")
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	if !strings.EqualFold(strings.TrimSpace(row.FingerprintSHA256), hex.EncodeToString(fingerprint[:])) {
		return nil, pkiInvariant("authority certificate fingerprint does not match canonical metadata")
	}
	if !row.NotBefore.Equal(certificate.NotBefore) || !row.NotAfter.Equal(certificate.NotAfter) {
		return nil, pkiInvariant("authority certificate validity does not match canonical metadata")
	}
	return certificate, nil
}

func validatePKILeafCertificate(row PKICertificateRow) (*x509.Certificate, error) {
	certificate, err := parseSinglePKICertificatePEM(row.CertificatePEM)
	if err != nil {
		return nil, err
	}
	if err := validatePKICertificateCryptoProfile(certificate); err != nil {
		return nil, err
	}
	if !certificate.BasicConstraintsValid || certificate.IsCA || certificate.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return nil, pkiInvariant("endpoint certificate has invalid CA or key-usage attributes")
	}
	if normalizePKISerialHex(row.SerialHex) != normalizePKISerialHex(certificate.SerialNumber.Text(16)) {
		return nil, pkiInvariant("certificate serial does not match canonical metadata")
	}
	fingerprint := sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
	if !strings.EqualFold(strings.TrimSpace(row.PublicKeyFingerprint), hex.EncodeToString(fingerprint[:])) {
		return nil, pkiInvariant("certificate public-key fingerprint does not match canonical metadata")
	}
	if !row.NotBefore.Equal(certificate.NotBefore) || !row.NotAfter.Equal(certificate.NotAfter) {
		return nil, pkiInvariant("certificate validity does not match canonical metadata")
	}
	requiredUsage := x509.ExtKeyUsageClientAuth
	if row.Purpose == PKICertificatePurposeServer {
		requiredUsage = x509.ExtKeyUsageServerAuth
	}
	if len(certificate.ExtKeyUsage) != 1 || certificate.ExtKeyUsage[0] != requiredUsage || len(certificate.UnknownExtKeyUsage) != 0 {
		return nil, pkiInvariant("certificate extended key usage does not match its purpose")
	}
	return certificate, nil
}

func validatePKILeafIssuer(certificate, authority *x509.Certificate, purpose string) error {
	if certificate == nil || authority == nil {
		return errors.New("certificate or authority is missing")
	}
	if !bytes.Equal(certificate.RawIssuer, authority.RawSubject) {
		return errors.New("issuer distinguished name does not match authority subject")
	}
	if len(certificate.AuthorityKeyId) == 0 || len(authority.SubjectKeyId) == 0 || !bytes.Equal(certificate.AuthorityKeyId, authority.SubjectKeyId) {
		return errors.New("authority key identifier does not match authority subject key identifier")
	}
	usage := x509.ExtKeyUsageClientAuth
	if purpose == PKICertificatePurposeServer {
		usage = x509.ExtKeyUsageServerAuth
	}
	roots := x509.NewCertPool()
	roots.AddCert(authority)
	verifyAt := certificate.NotBefore.Add(certificate.NotAfter.Sub(certificate.NotBefore) / 2)
	if _, err := certificate.Verify(x509.VerifyOptions{
		Roots:       roots,
		KeyUsages:   []x509.ExtKeyUsage{usage},
		CurrentTime: verifyAt,
	}); err != nil {
		return fmt.Errorf("X.509 chain verification failed: %w", err)
	}
	return nil
}

func validatePKILeafOwner(certificate *x509.Certificate, identity PKIIdentityRow, listener *RelayListenerRow, domainID string) error {
	if certificate == nil {
		return errors.New("certificate is missing")
	}
	path := "/agent/" + identity.AgentID
	if identity.Kind == PKIIdentityKindListener {
		path += "/listener/" + identity.ListenerID
	}
	expectedURI := (&url.URL{Scheme: "spiffe", Host: domainID, Path: path}).String()
	if certificate.Subject.CommonName != expectedURI || len(certificate.Subject.Country)+len(certificate.Subject.Organization)+
		len(certificate.Subject.OrganizationalUnit)+len(certificate.Subject.Locality)+len(certificate.Subject.Province)+
		len(certificate.Subject.StreetAddress)+len(certificate.Subject.PostalCode)+len(certificate.Subject.ExtraNames) != 0 ||
		certificate.Subject.SerialNumber != "" || len(certificate.Subject.Names) != 1 {
		return errors.New("subject is not bound to the canonical identity URI")
	}
	if len(certificate.URIs) != 1 || certificate.URIs[0] == nil || certificate.URIs[0].String() != expectedURI || len(certificate.EmailAddresses) != 0 {
		return errors.New("subject alternative identity URI is not canonical")
	}
	if identity.Kind == PKIIdentityKindAgent {
		if len(certificate.DNSNames) != 0 || len(certificate.IPAddresses) != 0 {
			return errors.New("agent certificate carries listener SANs")
		}
		return nil
	}
	if identity.Kind != PKIIdentityKindListener {
		return errors.New("identity kind is unsupported")
	}
	if listener == nil {
		if identity.State == PKIIdentityStateRevoked {
			return nil
		}
		return errors.New("canonical relay listener is missing")
	}
	dnsNames, ipAddresses, err := canonicalStoragePKIListenerSANs(*listener)
	if err != nil {
		return err
	}
	if !equalCanonicalPKIStrings(certificate.DNSNames, dnsNames) || !equalCanonicalPKIIPs(certificate.IPAddresses, ipAddresses) {
		return errors.New("listener SANs do not match canonical endpoints")
	}
	return nil
}

func canonicalStoragePKIListenerSANs(listener RelayListenerRow) ([]string, []net.IP, error) {
	hosts := []string{listener.PublicHost, listener.ListenHost}
	var bindHosts []string
	if value := strings.TrimSpace(listener.BindHostsJSON); value != "" {
		if err := json.Unmarshal([]byte(value), &bindHosts); err != nil {
			return nil, nil, errors.New("canonical listener bind hosts are invalid")
		}
	}
	hosts = append(hosts, bindHosts...)
	dnsSet := make(map[string]struct{})
	ipSet := make(map[string]net.IP)
	for _, host := range hosts {
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if host == "" {
			continue
		}
		if parsed := net.ParseIP(host); parsed != nil {
			if !parsed.IsUnspecified() {
				ipSet[parsed.String()] = parsed
			}
			continue
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if host == "" || strings.ContainsAny(host, " /\\:@?#[]") {
			return nil, nil, errors.New("canonical listener DNS name is invalid")
		}
		dnsSet[host] = struct{}{}
	}
	dnsNames := make([]string, 0, len(dnsSet))
	for name := range dnsSet {
		dnsNames = append(dnsNames, name)
	}
	sort.Strings(dnsNames)
	ipNames := make([]string, 0, len(ipSet))
	for name := range ipSet {
		ipNames = append(ipNames, name)
	}
	sort.Strings(ipNames)
	ipAddresses := make([]net.IP, 0, len(ipNames))
	for _, name := range ipNames {
		ipAddresses = append(ipAddresses, ipSet[name])
	}
	if len(dnsNames) == 0 && len(ipAddresses) == 0 {
		return nil, nil, errors.New("canonical listener has no certificate endpoint")
	}
	return dnsNames, ipAddresses, nil
}

func equalCanonicalPKIStrings(left, right []string) bool {
	leftValues := append([]string(nil), left...)
	rightValues := append([]string(nil), right...)
	for index := range leftValues {
		leftValues[index] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(leftValues[index]), "."))
	}
	for index := range rightValues {
		rightValues[index] = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rightValues[index]), "."))
	}
	sort.Strings(leftValues)
	sort.Strings(rightValues)
	if len(leftValues) != len(rightValues) {
		return false
	}
	for index := range leftValues {
		if leftValues[index] != rightValues[index] {
			return false
		}
	}
	return true
}

func equalCanonicalPKIIPs(left, right []net.IP) bool {
	leftValues := make([]string, len(left))
	rightValues := make([]string, len(right))
	for index := range left {
		leftValues[index] = left[index].String()
	}
	for index := range right {
		rightValues[index] = right[index].String()
	}
	return equalCanonicalPKIStrings(leftValues, rightValues)
}

func validatePKISupersessionGraph(certificates map[string]PKICertificateRow) error {
	for _, certificate := range certificates {
		if certificate.Status == PKICertificateStatusSuperseded && certificate.SupersededByID == nil {
			return pkiInvariant(fmt.Sprintf("superseded certificate %q has no superseding certificate", certificate.ID))
		}
		if certificate.SupersededByID == nil {
			continue
		}
		if certificate.Status != PKICertificateStatusSuperseded {
			return pkiInvariant(fmt.Sprintf("certificate %q has a superseding reference while not superseded", certificate.ID))
		}
		if *certificate.SupersededByID == certificate.ID {
			return pkiInvariant(fmt.Sprintf("certificate %q supersedes itself", certificate.ID))
		}
		replacement, found := certificates[*certificate.SupersededByID]
		if !found || replacement.IdentityID != certificate.IdentityID || replacement.Purpose != certificate.Purpose {
			return pkiInvariant(fmt.Sprintf("certificate %q has an invalid superseding certificate", certificate.ID))
		}
	}

	const (
		pkiSupersessionUnvisited = iota
		pkiSupersessionVisiting
		pkiSupersessionVisited
	)
	visits := make(map[string]int, len(certificates))
	var visit func(string) error
	visit = func(id string) error {
		switch visits[id] {
		case pkiSupersessionVisiting:
			return pkiInvariant(fmt.Sprintf("certificate supersession graph contains a cycle at %q", id))
		case pkiSupersessionVisited:
			return nil
		}
		visits[id] = pkiSupersessionVisiting
		if replacementID := certificates[id].SupersededByID; replacementID != nil {
			if err := visit(*replacementID); err != nil {
				return err
			}
		}
		visits[id] = pkiSupersessionVisited
		return nil
	}
	for id := range certificates {
		if err := visit(id); err != nil {
			return err
		}
	}

	for _, certificate := range certificates {
		if certificate.SupersededByID == nil {
			continue
		}
		replacement := certificates[*certificate.SupersededByID]
		if !replacement.CreatedAt.After(certificate.CreatedAt) {
			return pkiInvariant(fmt.Sprintf("certificate %q superseding certificate was not created later", certificate.ID))
		}
	}
	return nil
}

func parseSinglePKICertificatePEM(value string) (*x509.Certificate, error) {
	encoded := bytes.TrimSpace([]byte(value))
	if !bytes.HasPrefix(encoded, []byte("-----BEGIN CERTIFICATE-----")) {
		return nil, pkiInvariant("certificate PEM must begin with a certificate block")
	}
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, pkiInvariant("certificate PEM must contain exactly one certificate block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, pkiInvariant("certificate PEM is not a parseable X.509 certificate")
	}
	return certificate, nil
}

func validatePKICertificateCryptoProfile(certificate *x509.Certificate) error {
	if certificate == nil || certificate.SerialNumber == nil || certificate.SerialNumber.Sign() <= 0 || certificate.SerialNumber.BitLen() < 128 {
		return pkiInvariant("certificate serial must be a positive value of at least 128 bits")
	}
	publicKey, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve == nil || publicKey.Curve.Params().Name != elliptic.P256().Params().Name {
		return pkiInvariant("certificate public key must use ECDSA P-256")
	}
	if certificate.SignatureAlgorithm != x509.ECDSAWithSHA256 {
		return pkiInvariant("certificate signature must use ECDSA with SHA-256")
	}
	return nil
}

func normalizePKISerialHex(value string) string {
	normalized := strings.TrimLeft(strings.ToLower(strings.TrimSpace(value)), "0")
	if normalized == "" {
		return "0"
	}
	return normalized
}

func validHexBytes(value string, bytes int) bool {
	value = strings.TrimSpace(value)
	if len(value) != bytes*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validHexAtLeast(value string, bytes int) bool {
	value = strings.TrimSpace(value)
	if len(value) < bytes*2 || len(value)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
