package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

var (
	ErrPKITransactionRequired = errors.New("PKI mutation requires a PKI transaction")
	ErrPKIInvariant           = errors.New("invalid PKI canonical record")
)

// WithPKITransaction is the only supported mutation boundary for canonical PKI
// facts. It reuses an enclosing store transaction when one already exists.
func (s *GormStore) WithPKITransaction(ctx context.Context, mutate func(*GormStore) error) error {
	if mutate == nil {
		return fmt.Errorf("PKI mutation callback is required")
	}
	if s.transactionScoped {
		return mutate(s)
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return mutate(&GormStore{
			db:                tx,
			dataRoot:          s.dataRoot,
			localAgentID:      s.localAgentID,
			driver:            s.driver,
			transactionScoped: true,
		})
	})
}

func (s *GormStore) CreatePKISettings(ctx context.Context, row PKISettingsRow) error {
	if err := s.requirePKITransaction(); err != nil {
		return err
	}
	row.ID = PKISettingsSingletonID
	if strings.TrimSpace(row.PKIDomainID) == "" || row.CALifetimeSeconds <= 0 || row.EndpointLifetimeSeconds <= 0 || row.AuditRetentionDays <= 0 || row.SecurityRevision < 0 || row.PKIEpoch < 0 || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
		return pkiInvariant("settings fields are incomplete")
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *GormStore) CreatePKIAuthority(ctx context.Context, row PKIAuthorityRow) error {
	if err := s.requirePKITransaction(); err != nil {
		return err
	}
	if strings.TrimSpace(row.ID) == "" || strings.TrimSpace(row.PKIDomainID) == "" || row.Generation <= 0 || strings.TrimSpace(row.Status) == "" || !validCertificateOnlyPEM(row.CertificatePEM) || !validHexBytes(row.FingerprintSHA256, 32) || row.NotBefore.IsZero() || !row.NotAfter.After(row.NotBefore) || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
		return pkiInvariant("authority fields are incomplete")
	}
	if row.PrivateKeyDestroyedAt == nil && (row.EncryptedKeyRef == nil || strings.TrimSpace(*row.EncryptedKeyRef) == "") {
		return pkiInvariant("authority encrypted key reference is required until key destruction")
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *GormStore) CreatePKIIdentity(ctx context.Context, row PKIIdentityRow) error {
	if err := s.requirePKITransaction(); err != nil {
		return err
	}
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
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *GormStore) CreatePKICertificate(ctx context.Context, row PKICertificateRow) error {
	if err := s.requirePKITransaction(); err != nil {
		return err
	}
	row.SerialHex = strings.ToLower(strings.TrimSpace(row.SerialHex))
	if strings.TrimSpace(row.ID) == "" || !validHexAtLeast(row.SerialHex, 16) || strings.TrimSpace(row.IdentityID) == "" || strings.TrimSpace(row.AuthorityID) == "" || row.CAGeneration <= 0 || !validCertificateOnlyPEM(row.CertificatePEM) || !validHexBytes(row.PublicKeyFingerprint, 32) || row.NotBefore.IsZero() || !row.NotAfter.After(row.NotBefore) || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
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
	row.ActiveIdentityPurposeKey = nil
	if row.Status == PKICertificateStatusActive {
		key := pkiUniqueSlot(row.IdentityID, row.Purpose)
		row.ActiveIdentityPurposeKey = &key
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *GormStore) CreatePKIEnrollmentToken(ctx context.Context, row PKIEnrollmentTokenRow) error {
	if err := s.requirePKITransaction(); err != nil {
		return err
	}
	row.TokenDigestSHA256 = strings.ToLower(strings.TrimSpace(row.TokenDigestSHA256))
	if strings.TrimSpace(row.ID) == "" || !validHexBytes(row.TokenDigestSHA256, 32) || strings.TrimSpace(row.Scope) == "" || row.ExpiresAt.IsZero() || strings.TrimSpace(row.CreatedBy) == "" || row.CreatedAt.IsZero() {
		return pkiInvariant("enrollment token fields are incomplete")
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *GormStore) CreatePKILifecycleJob(ctx context.Context, row PKILifecycleJobRow) error {
	if err := s.requirePKITransaction(); err != nil {
		return err
	}
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
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *GormStore) AppendPKIEvent(ctx context.Context, row PKIEventRow) error {
	if err := s.requirePKITransaction(); err != nil {
		return err
	}
	if strings.TrimSpace(row.ID) == "" || strings.TrimSpace(row.PKIDomainID) == "" || strings.TrimSpace(row.Type) == "" || row.OccurredAt.IsZero() || strings.TrimSpace(row.Source) == "" || strings.TrimSpace(row.ObjectType) == "" || strings.TrimSpace(row.ObjectID) == "" || strings.TrimSpace(row.Result) == "" || row.SecurityRevision < 0 {
		return pkiInvariant("event fields are incomplete")
	}
	if strings.TrimSpace(row.DetailsJSON) == "" {
		row.DetailsJSON = "{}"
	}
	if !json.Valid([]byte(row.DetailsJSON)) {
		return pkiInvariant("event details must be valid JSON")
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

func (s *GormStore) CreatePKIInstanceLease(ctx context.Context, row PKIInstanceLeaseRow) error {
	if err := s.requirePKITransaction(); err != nil {
		return err
	}
	row.ID = PKILeaseSingletonID
	if strings.TrimSpace(row.PKIDomainID) == "" || strings.TrimSpace(row.InstanceID) == "" || row.LeaseDeadline.IsZero() || row.PKIEpoch < 0 || strings.TrimSpace(row.State) == "" || row.UpdatedAt.IsZero() {
		return pkiInvariant("instance lease fields are incomplete")
	}
	return s.db.WithContext(ctx).Create(&row).Error
}

type PKICanonicalState struct {
	Settings         *PKISettingsRow
	Authorities      []PKIAuthorityRow
	Identities       []PKIIdentityRow
	Certificates     []PKICertificateRow
	EnrollmentTokens []PKIEnrollmentTokenRow
	LifecycleJobs    []PKILifecycleJobRow
	Events           []PKIEventRow
	InstanceLease    *PKIInstanceLeaseRow
}

func (s *GormStore) LoadPKICanonicalState(ctx context.Context) (PKICanonicalState, error) {
	state := PKICanonicalState{}
	var settings PKISettingsRow
	if err := s.db.WithContext(ctx).First(&settings, PKISettingsSingletonID).Error; err == nil {
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
		{&state.LifecycleJobs, "created_at ASC, id ASC"},
		{&state.Events, "occurred_at ASC, id ASC"},
	}
	for _, query := range queries {
		if err := s.db.WithContext(ctx).Order(query.order).Find(query.value).Error; err != nil {
			return PKICanonicalState{}, err
		}
	}
	var lease PKIInstanceLeaseRow
	if err := s.db.WithContext(ctx).First(&lease, PKILeaseSingletonID).Error; err == nil {
		state.InstanceLease = &lease
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return PKICanonicalState{}, err
	}
	return state, nil
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

func (s *GormStore) requirePKITransaction() error {
	if s == nil || s.db == nil || !s.transactionScoped {
		return ErrPKITransactionRequired
	}
	return nil
}

func pkiInvariant(message string) error {
	return fmt.Errorf("%w: %s", ErrPKIInvariant, message)
}

func pkiUniqueSlot(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func validCertificateOnlyPEM(value string) bool {
	upper := strings.ToUpper(value)
	return strings.Contains(upper, "-----BEGIN CERTIFICATE-----") && !strings.Contains(upper, "PRIVATE KEY")
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
