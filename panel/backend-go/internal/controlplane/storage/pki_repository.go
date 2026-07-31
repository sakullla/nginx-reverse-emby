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
	"strings"

	"gorm.io/gorm"
)

var (
	ErrPKIInvariant = errors.New("invalid PKI canonical record")
)

type PKITransaction struct {
	db *gorm.DB
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
	if strings.TrimSpace(row.PKIDomainID) == "" || strings.TrimSpace(row.InstanceID) == "" || row.LeaseDeadline.IsZero() || row.PKIEpoch < 0 || strings.TrimSpace(row.State) == "" || row.UpdatedAt.IsZero() {
		return pkiInvariant("instance lease fields are incomplete")
	}
	return tx.db.WithContext(ctx).Create(&row).Error
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
		{&state.LifecycleJobs, "created_at ASC, id ASC"},
		{&state.Events, "occurred_at ASC, id ASC"},
	}
	for _, query := range queries {
		if err := db.WithContext(ctx).Order(query.order).Find(query.value).Error; err != nil {
			return PKICanonicalState{}, err
		}
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
	hasFacts := len(state.Authorities)+len(state.Identities)+len(state.Certificates)+len(state.EnrollmentTokens)+len(state.LifecycleJobs)+len(state.Events) > 0 || state.InstanceLease != nil
	if state.Settings == nil {
		if hasFacts {
			return pkiInvariant("PKI settings are required before canonical facts")
		}
		return nil
	}
	domainID := strings.TrimSpace(state.Settings.PKIDomainID)
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
		identities[identity.ID] = identity
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
		if state.InstanceLease.PKIDomainID != domainID || state.InstanceLease.PKIEpoch != state.Settings.PKIEpoch {
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

func validatePKISupersessionGraph(certificates map[string]PKICertificateRow) error {
	for _, certificate := range certificates {
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
