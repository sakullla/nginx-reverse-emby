package storage

import "time"

const (
	PKISettingsSingletonID         = 1
	PKILeaseSingletonID            = 1
	PKISecuritySnapshotSingletonID = 1

	PKIIdentityKindAgent    = "agent"
	PKIIdentityKindListener = "listener"

	PKIIdentityStateEnrollmentRequired = "enrollment_required"
	PKIIdentityStateActive             = "active"
	PKIIdentityStateRevoked            = "revoked"

	PKICertificatePurposeClient = "client_auth"
	PKICertificatePurposeServer = "server_auth"

	PKICertificateStatusPending    = "pending"
	PKICertificateStatusActive     = "active"
	PKICertificateStatusSuperseded = "superseded"
	PKICertificateStatusRevoked    = "revoked"
	PKICertificateStatusExpired    = "expired"

	PKILifecycleJobStatePending   = "pending"
	PKILifecycleJobStateRunning   = "running"
	PKILifecycleJobStateSucceeded = "succeeded"
	PKILifecycleJobStateFailed    = "failed"
	PKILifecycleJobStateCancelled = "cancelled"

	PKIUpgradeStateTunnelMTLSOnly = "tunnel_mtls_only"
)

// PKISettingsRow is the canonical singleton for the internal PKI security
// domain. Durations are stored as seconds/days so the representation is
// portable across every supported database driver.
type PKISettingsRow struct {
	ID                      int       `gorm:"column:id;primaryKey;autoIncrement:false;check:pki_settings_singleton,id = 1"`
	PKIDomainID             string    `gorm:"column:pki_domain_id;not null;uniqueIndex:idx_pki_settings_domain"`
	CALifetimeSeconds       int64     `gorm:"column:ca_lifetime_seconds;not null"`
	EndpointLifetimeSeconds int64     `gorm:"column:endpoint_lifetime_seconds;not null"`
	AuditRetentionDays      int       `gorm:"column:audit_retention_days;not null"`
	SecurityRevision        int64     `gorm:"column:security_revision;not null;default:0"`
	PKIEpoch                int64     `gorm:"column:pki_epoch;not null;default:0"`
	UpgradeState            string    `gorm:"column:upgrade_state;not null;default:''"`
	CreatedAt               time.Time `gorm:"column:created_at;not null"`
	UpdatedAt               time.Time `gorm:"column:updated_at;not null"`
}

type PKIAuthorityRow struct {
	ID                    string     `gorm:"column:id;primaryKey"`
	PKIDomainID           string     `gorm:"column:pki_domain_id;not null;uniqueIndex:idx_pki_authorities_domain_generation,priority:1"`
	Generation            int64      `gorm:"column:generation;not null;uniqueIndex:idx_pki_authorities_domain_generation,priority:2;check:pki_authority_generation,generation > 0"`
	Status                string     `gorm:"column:status;not null;index:idx_pki_authorities_status"`
	CertificatePEM        string     `gorm:"column:certificate_pem;type:text;not null"`
	EncryptedKeyRef       *string    `gorm:"column:encrypted_key_ref;uniqueIndex:idx_pki_authorities_key_ref"`
	FingerprintSHA256     string     `gorm:"column:fingerprint_sha256;not null;uniqueIndex:idx_pki_authorities_fingerprint"`
	NotBefore             time.Time  `gorm:"column:not_before;not null"`
	NotAfter              time.Time  `gorm:"column:not_after;not null"`
	RetireDeadline        *time.Time `gorm:"column:retire_deadline"`
	CreatedReason         string     `gorm:"column:created_reason;not null;default:''"`
	RetiredReason         string     `gorm:"column:retired_reason;not null;default:''"`
	PrivateKeyDestroyedAt *time.Time `gorm:"column:private_key_destroyed_at"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;not null"`
}

type PKIIdentityRow struct {
	ID                   string     `gorm:"column:id;primaryKey"`
	PKIDomainID          string     `gorm:"column:pki_domain_id;not null;uniqueIndex:idx_pki_identity_owner,priority:1"`
	Kind                 string     `gorm:"column:kind;not null;uniqueIndex:idx_pki_identity_owner,priority:2"`
	AgentID              string     `gorm:"column:agent_id;not null;uniqueIndex:idx_pki_identity_owner,priority:3"`
	ListenerID           string     `gorm:"column:listener_id;not null;default:'';uniqueIndex:idx_pki_identity_owner,priority:4"`
	State                string     `gorm:"column:state;not null;index:idx_pki_identities_state"`
	CurrentCertificateID *string    `gorm:"column:current_certificate_id;uniqueIndex:idx_pki_identity_current_certificate"`
	RevokedAt            *time.Time `gorm:"column:revoked_at"`
	RevokedReason        string     `gorm:"column:revoked_reason;not null;default:''"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null"`
}

type PKICertificateRow struct {
	ID                       string     `gorm:"column:id;primaryKey"`
	SerialHex                string     `gorm:"column:serial_hex;not null;uniqueIndex:idx_pki_certificates_serial"`
	IdentityID               string     `gorm:"column:identity_id;not null;index:idx_pki_certificates_identity"`
	Purpose                  string     `gorm:"column:purpose;not null;index:idx_pki_certificates_identity_purpose,priority:2"`
	AuthorityID              string     `gorm:"column:authority_id;not null;index:idx_pki_certificates_authority"`
	CAGeneration             int64      `gorm:"column:ca_generation;not null;index:idx_pki_certificates_ca_generation"`
	CertificatePEM           string     `gorm:"column:certificate_pem;type:text;not null"`
	PublicKeyFingerprint     string     `gorm:"column:public_key_fingerprint_sha256;not null"`
	NotBefore                time.Time  `gorm:"column:not_before;not null"`
	NotAfter                 time.Time  `gorm:"column:not_after;not null"`
	Status                   string     `gorm:"column:status;not null;index:idx_pki_certificates_status"`
	ActiveIdentityPurposeKey *string    `gorm:"column:active_identity_purpose_key;uniqueIndex:idx_pki_certificates_one_active"`
	RevokedAt                *time.Time `gorm:"column:revoked_at"`
	RevokedReason            string     `gorm:"column:revoked_reason;not null;default:''"`
	SupersededByID           *string    `gorm:"column:superseded_by_id;index:idx_pki_certificates_superseded_by"`
	CreatedAt                time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt                time.Time  `gorm:"column:updated_at;not null"`
}

type PKIEnrollmentTokenRow struct {
	ID                string     `gorm:"column:id;primaryKey"`
	TokenDigestSHA256 string     `gorm:"column:token_digest_sha256;not null;uniqueIndex:idx_pki_enrollment_tokens_digest"`
	Scope             string     `gorm:"column:scope;not null"`
	BoundAgentID      string     `gorm:"column:bound_agent_id;not null;default:'';index:idx_pki_enrollment_tokens_bound_agent"`
	ExpiresAt         time.Time  `gorm:"column:expires_at;not null;index:idx_pki_enrollment_tokens_expires"`
	ConsumedAt        *time.Time `gorm:"column:consumed_at"`
	CreatedBy         string     `gorm:"column:created_by;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
}

// PKIEnrollmentReplayRow makes certificate enrollment retry-safe across an
// HTTP response loss. RequestKey is scoped by the service (one-time token
// digest for registration, agent/request_id for authenticated control sync),
// while RequestFingerprint prevents the same key from being reused with a
// different CSR or identity binding.
type PKIEnrollmentReplayRow struct {
	ID                 string    `gorm:"column:id;primaryKey"`
	PKIDomainID        string    `gorm:"column:pki_domain_id;not null;index:idx_pki_enrollment_replays_domain"`
	RequestKey         string    `gorm:"column:request_key;not null;uniqueIndex:idx_pki_enrollment_replays_request"`
	RequestFingerprint string    `gorm:"column:request_fingerprint_sha256;not null"`
	ResultJSON         string    `gorm:"column:result_json;type:text;not null"`
	CreatedAt          time.Time `gorm:"column:created_at;not null"`
}

// PKIConfirmationNonceRow is a server-issued, short-lived, one-use approval
// capability bound to an operator, action and target.
type PKIConfirmationNonceRow struct {
	ID           string     `gorm:"column:id;primaryKey"`
	PKIDomainID  string     `gorm:"column:pki_domain_id;not null;index:idx_pki_confirmation_nonces_domain"`
	DigestSHA256 string     `gorm:"column:digest_sha256;not null;uniqueIndex:idx_pki_confirmation_nonces_digest"`
	OperatorID   string     `gorm:"column:operator_id;not null"`
	Action       string     `gorm:"column:action;not null"`
	TargetID     string     `gorm:"column:target_id;not null;default:''"`
	ExpiresAt    time.Time  `gorm:"column:expires_at;not null;index:idx_pki_confirmation_nonces_expires"`
	ConsumedAt   *time.Time `gorm:"column:consumed_at"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
}

// PKISecuritySnapshotRow is the latest canonical signed snapshot. Revision
// responses read this row so configuration never outruns the trust/revocation
// state needed to apply it.
type PKISecuritySnapshotRow struct {
	ID               int       `gorm:"column:id;primaryKey;autoIncrement:false;check:pki_security_snapshot_singleton,id = 1"`
	PKIDomainID      string    `gorm:"column:pki_domain_id;not null"`
	PKIEpoch         int64     `gorm:"column:pki_epoch;not null"`
	SecurityRevision int64     `gorm:"column:security_revision;not null"`
	SnapshotJSON     string    `gorm:"column:snapshot_json;type:text;not null"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null"`
}

type PKILifecycleJobRow struct {
	ID              string     `gorm:"column:id;primaryKey"`
	PKIDomainID     string     `gorm:"column:pki_domain_id;not null;index:idx_pki_lifecycle_jobs_domain"`
	TargetType      string     `gorm:"column:target_type;not null;index:idx_pki_lifecycle_jobs_target,priority:1"`
	TargetID        string     `gorm:"column:target_id;not null;index:idx_pki_lifecycle_jobs_target,priority:2"`
	Kind            string     `gorm:"column:kind;not null;index:idx_pki_lifecycle_jobs_target,priority:3"`
	Phase           string     `gorm:"column:phase;not null"`
	State           string     `gorm:"column:state;not null;index:idx_pki_lifecycle_jobs_state"`
	Attempt         int        `gorm:"column:attempt;not null;default:0"`
	NextAttemptAt   *time.Time `gorm:"column:next_attempt_at;index:idx_pki_lifecycle_jobs_next_attempt"`
	Deadline        *time.Time `gorm:"column:deadline"`
	LastError       string     `gorm:"column:last_error;type:text;not null;default:''"`
	OperationID     string     `gorm:"column:operation_id;not null;default:'';index:idx_pki_lifecycle_jobs_operation"`
	IdempotencyKey  string     `gorm:"column:idempotency_key;not null;uniqueIndex:idx_pki_lifecycle_jobs_idempotency"`
	ActiveTargetKey *string    `gorm:"column:active_target_key;uniqueIndex:idx_pki_lifecycle_jobs_one_active"`
	LeaseOwner      string     `gorm:"column:lease_owner;not null;default:''"`
	LeaseDeadline   *time.Time `gorm:"column:lease_deadline"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null"`
}

type PKIEventRow struct {
	ID               string    `gorm:"column:id;primaryKey"`
	PKIDomainID      string    `gorm:"column:pki_domain_id;not null;index:idx_pki_events_domain_time,priority:1"`
	Type             string    `gorm:"column:type;not null;index:idx_pki_events_type"`
	OccurredAt       time.Time `gorm:"column:occurred_at;not null;index:idx_pki_events_domain_time,priority:2"`
	Source           string    `gorm:"column:source;not null"`
	OperatorID       string    `gorm:"column:operator_id;not null;default:''"`
	ObjectType       string    `gorm:"column:object_type;not null"`
	ObjectID         string    `gorm:"column:object_id;not null"`
	CertificateID    *string   `gorm:"column:certificate_id;index:idx_pki_events_certificate"`
	CAGeneration     *int64    `gorm:"column:ca_generation;index:idx_pki_events_ca_generation"`
	Result           string    `gorm:"column:result;not null"`
	Reason           string    `gorm:"column:reason;type:text;not null;default:''"`
	SecurityRevision int64     `gorm:"column:security_revision;not null"`
	DetailsJSON      string    `gorm:"column:details_json;type:text;not null;default:'{}'"`
}

type PKIInstanceLeaseRow struct {
	ID            int       `gorm:"column:id;primaryKey;autoIncrement:false;check:pki_instance_lease_singleton,id = 1"`
	PKIDomainID   string    `gorm:"column:pki_domain_id;not null;uniqueIndex:idx_pki_instance_lease_domain"`
	InstanceID    string    `gorm:"column:instance_id;not null"`
	LeaseTerm     string    `gorm:"column:lease_term;not null;default:''"`
	LeaseDeadline time.Time `gorm:"column:lease_deadline;not null"`
	PKIEpoch      int64     `gorm:"column:pki_epoch;not null"`
	State         string    `gorm:"column:state;not null"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null"`
}

func (PKISettingsRow) TableName() string          { return "pki_settings" }
func (PKIAuthorityRow) TableName() string         { return "pki_authorities" }
func (PKIIdentityRow) TableName() string          { return "pki_identities" }
func (PKICertificateRow) TableName() string       { return "pki_certificates" }
func (PKIEnrollmentTokenRow) TableName() string   { return "pki_enrollment_tokens" }
func (PKIEnrollmentReplayRow) TableName() string  { return "pki_enrollment_replays" }
func (PKIConfirmationNonceRow) TableName() string { return "pki_confirmation_nonces" }
func (PKISecuritySnapshotRow) TableName() string  { return "pki_security_snapshot" }
func (PKILifecycleJobRow) TableName() string      { return "pki_lifecycle_jobs" }
func (PKIEventRow) TableName() string             { return "pki_events" }
func (PKIInstanceLeaseRow) TableName() string     { return "pki_instance_lease" }
