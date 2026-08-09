package marketplace

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

const (
	SourceKindOfficial      = "official"
	SourceKindCustom        = "custom"
	OfficialSourceID        = "official"
	OfficialSourceURL       = "https://github.com/sakullla/sakullla-plugins.git"
	UntrustedRiskLabel      = "UNOFFICIAL_SOURCE_SUPPLY_CHAIN_RISK"
	CredentialPurpose       = "git.marketplace"
	SignerSecretPurpose     = "plugin.marketplace.signer"
	MaxSourceNameBytes      = 190
	MaxSourceURLBytes       = 2048
	MaxSourceRefNameBytes   = 512
	MaxCredentialRefBytes   = 190
	MaxSignerKeyIDBytes     = 190
	MaxSignerSecretRefBytes = 190
	OfficialRefreshInterval = 6 * time.Hour
)

const (
	SourcePurposeMarket = "market"
	SourcePurposePlugin = "plugin"
	GitRefKindBranch    = "branch"
	GitRefKindTag       = "tag"
)

var sourceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var fullCommitOIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

var (
	ErrRefreshLeaseHeld        = errors.New("marketplace source refresh lease is already held")
	ErrSourceGenerationChanged = errors.New("marketplace source generation changed")
	ErrInvalidSource           = errors.New("invalid marketplace source")
)

type Source struct {
	ID                   string        `json:"id"`
	Kind                 string        `json:"kind"`
	Purpose              string        `json:"purpose"`
	Name                 string        `json:"name"`
	URL                  string        `json:"url"`
	RefKind              string        `json:"ref_kind"`
	RefName              string        `json:"ref_name"`
	CredentialRef        string        `json:"-"`
	CredentialConfigured bool          `json:"credential_configured"`
	SignerKeyID          string        `json:"signer_key_id,omitempty"`
	SignerSecretRef      string        `json:"-"`
	SignerPublicKey      string        `json:"-"`
	SignerFingerprint    string        `json:"signer_fingerprint,omitempty"`
	RefreshInterval      time.Duration `json:"refresh_interval_ns"`
	RiskLabel            string        `json:"risk_label,omitempty"`
	ConfigRevision       uint64        `json:"config_revision"`
	CurrentResolvedOID   string        `json:"current_resolved_oid,omitempty"`
	CurrentSnapshot      string        `json:"current_snapshot,omitempty"`
	LastResult           string        `json:"last_result,omitempty"`
	LastError            string        `json:"last_error,omitempty"`
	UpdatedAt            time.Time     `json:"updated_at"`
	LastCompletedAt      time.Time     `json:"last_completed_at,omitempty"`
	LeaseExpiresAt       time.Time     `json:"-"`
	Deleting             bool          `json:"-"`
}

func (source Source) MarshalJSON() ([]byte, error) {
	type sourceJSON Source
	copy := sourceJSON(source)
	copy.CredentialConfigured = source.CredentialRef != ""
	return json.Marshal(copy)
}

type SourceSigner struct {
	KeyID     string
	SecretRef string
	PublicKey string
}

// SignatureTrust is the immutable, non-secret verification identity captured
// when a package enters a source catalog. It is safe to retain after the
// source or its vault metadata has been removed.
type SignatureTrust struct {
	SourceID    string
	SourceKind  string
	KeyID       string
	PublicKey   string
	Fingerprint string
}

func OfficialSource() Source {
	return Source{ID: OfficialSourceID, Kind: SourceKindOfficial, Purpose: SourcePurposeMarket, Name: "Sakullla Official", URL: OfficialSourceURL, RefKind: GitRefKindBranch, RefName: "main", ConfigRevision: 1, RefreshInterval: OfficialRefreshInterval}
}

func EffectiveRefreshInterval(source Source) time.Duration {
	if source.Kind == SourceKindOfficial && source.RefreshInterval == 0 {
		return OfficialRefreshInterval
	}
	return source.RefreshInterval
}

func NewCustomSource(id, name, remoteURL, branchName, credentialRef string, refreshInterval time.Duration) (Source, error) {
	return NewGitRepositorySource(id, name, remoteURL, SourcePurposeMarket, GitRefKindBranch, branchName, credentialRef, refreshInterval)
}

func NewGitRepositorySource(id, name, remoteURL, purpose, refKind, refName, credentialRef string, refreshInterval time.Duration) (Source, error) {
	source := Source{
		ID: strings.ToLower(strings.TrimSpace(id)), Kind: SourceKindCustom, Name: strings.TrimSpace(name), URL: strings.TrimSpace(remoteURL),
		Purpose: strings.TrimSpace(purpose), RefKind: strings.TrimSpace(refKind), RefName: strings.TrimSpace(refName), CredentialRef: strings.TrimSpace(credentialRef),
		CredentialConfigured: strings.TrimSpace(credentialRef) != "", ConfigRevision: 1, RefreshInterval: refreshInterval, RiskLabel: UntrustedRiskLabel,
	}
	if err := ValidateSource(source); err != nil {
		return Source{}, err
	}
	return source, nil
}

// NewSignedCustomSource binds one administrator-imported Ed25519 public key to
// a custom source. The public key is safe to persist; SecretRef records the
// authorized vault object from which it was imported without retaining secret
// plaintext.
func NewSignedCustomSource(id, name, remoteURL, branchName, credentialRef string, refreshInterval time.Duration, signer SourceSigner) (Source, error) {
	return NewSignedGitRepositorySource(id, name, remoteURL, SourcePurposeMarket, GitRefKindBranch, branchName, credentialRef, refreshInterval, signer)
}

func NewSignedGitRepositorySource(id, name, remoteURL, purpose, refKind, refName, credentialRef string, refreshInterval time.Duration, signer SourceSigner) (Source, error) {
	source, err := NewGitRepositorySource(id, name, remoteURL, purpose, refKind, refName, credentialRef, refreshInterval)
	if err != nil {
		return Source{}, err
	}
	source.SignerKeyID = signer.KeyID
	source.SignerSecretRef = signer.SecretRef
	source.SignerPublicKey = signer.PublicKey
	if key, decodeErr := decodeSourceSignerPublicKey(source.SignerPublicKey); decodeErr == nil {
		source.SignerFingerprint = signerFingerprint(key)
	}
	if err := ValidateSource(source); err != nil {
		return Source{}, err
	}
	return source, nil
}

func ValidateSource(source Source) error {
	if err := validateSource(source); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	return nil
}

func validateSource(source Source) error {
	if source.RefreshInterval < 0 {
		return errors.New("marketplace refresh interval cannot be negative")
	}
	if len(source.Name) > MaxSourceNameBytes || len(source.URL) > MaxSourceURLBytes || len(source.RefName) > MaxSourceRefNameBytes || len(source.CredentialRef) > MaxCredentialRefBytes || len(source.SignerKeyID) > MaxSignerKeyIDBytes || len(source.SignerSecretRef) > MaxSignerSecretRefBytes || len(source.SignerFingerprint) > 64 {
		return errors.New("marketplace source field exceeds persistence limit")
	}
	if source.Kind == SourceKindOfficial {
		official := OfficialSource()
		if source.ID != official.ID || source.URL != official.URL || source.Name != official.Name || source.Purpose != official.Purpose || source.RefKind != official.RefKind || source.RefName != official.RefName || source.ConfigRevision != official.ConfigRevision || source.CredentialRef != "" || source.SignerKeyID != "" || source.SignerSecretRef != "" || source.SignerPublicKey != "" || source.SignerFingerprint != "" {
			return errors.New("official source identity is built in and immutable")
		}
		return nil
	}
	if source.Kind != SourceKindCustom {
		return fmt.Errorf("unsupported source kind %q", source.Kind)
	}
	lowerName := strings.ToLower(strings.TrimSpace(source.Name))
	if !sourceIDPattern.MatchString(source.ID) || source.ID == OfficialSourceID || lowerName == "official" || strings.Contains(lowerName, "sakullla official") || strings.Contains(source.Name, "官方") {
		return errors.New("custom source identity may not impersonate the official source")
	}
	parsed, err := url.Parse(source.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return errors.New("custom source URL must be an https URL without embedded credentials, query, or fragment")
	}
	if source.Purpose != SourcePurposeMarket && source.Purpose != SourcePurposePlugin {
		return errors.New("custom source purpose must be market or plugin")
	}
	if source.RefKind != GitRefKindBranch && source.RefKind != GitRefKindTag {
		return errors.New("custom source ref_kind must be branch or tag")
	}
	if source.RefName == "" || strings.HasPrefix(source.RefName, "refs/") || strings.ContainsAny(source.RefName, "\x00\r\n") || strings.Contains(source.RefName, "..") || strings.HasSuffix(source.RefName, ".lock") {
		return errors.New("custom source ref_name is invalid")
	}
	if source.ConfigRevision == 0 {
		return errors.New("custom source config revision is required")
	}
	if source.CurrentResolvedOID != "" && !IsFullCommitOID(source.CurrentResolvedOID) {
		return errors.New("custom source resolved OID must be a full lowercase commit OID")
	}
	if source.RiskLabel != UntrustedRiskLabel {
		return errors.New("custom sources must retain the untrusted supply-chain risk label")
	}
	hasSigner := source.SignerKeyID != "" || source.SignerSecretRef != "" || source.SignerPublicKey != "" || source.SignerFingerprint != ""
	if hasSigner {
		if source.SignerSecretRef != strings.TrimSpace(source.SignerSecretRef) || source.SignerPublicKey != strings.TrimSpace(source.SignerPublicKey) {
			return errors.New("custom source signer fields must use canonical whitespace")
		}
		if source.SignerKeyID == "" || source.SignerSecretRef == "" || source.SignerPublicKey == "" || source.SignerFingerprint == "" {
			return errors.New("custom source signer identity, vault reference, public key, and fingerprint must be configured together")
		}
		if source.SignerKeyID == plugins.OfficialSignatureKeyID || plugins.ValidateSignerKeyID(source.SignerKeyID) != nil {
			return errors.New("custom source signer identity is invalid")
		}
		key, err := decodeSourceSignerPublicKey(source.SignerPublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return errors.New("custom source signer public key is invalid")
		}
		if source.SignerFingerprint != signerFingerprint(key) {
			return errors.New("custom source signer fingerprint does not match its public key")
		}
	}
	return nil
}

func IsFullCommitOID(value string) bool { return fullCommitOIDPattern.MatchString(value) }

func signerFingerprint(key ed25519.PublicKey) string {
	digest := sha256.Sum256(key)
	return hex.EncodeToString(digest[:])
}

func SourceSignerFingerprint(publicKey string) (string, error) {
	key, err := decodeSourceSignerPublicKey(publicKey)
	if err != nil {
		return "", err
	}
	return signerFingerprint(key), nil
}

// SignatureTrust returns the exact signer allowed for this source. Custom
// sources never inherit the built-in official root through this binding.
func (source Source) SignatureTrust() (SignatureTrust, error) {
	if err := ValidateSource(source); err != nil {
		return SignatureTrust{}, err
	}
	if source.Kind == SourceKindCustom {
		trust := SignatureTrust{SourceID: source.ID, SourceKind: source.Kind, KeyID: source.SignerKeyID, PublicKey: source.SignerPublicKey, Fingerprint: source.SignerFingerprint}
		if err := ValidateSignatureTrust(trust); err != nil {
			return SignatureTrust{}, err
		}
		return trust, nil
	}
	key := plugins.DefaultTrustedSigners()[plugins.OfficialSignatureKeyID]
	return SignatureTrust{SourceID: source.ID, SourceKind: source.Kind, KeyID: plugins.OfficialSignatureKeyID, PublicKey: base64.StdEncoding.EncodeToString(key), Fingerprint: signerFingerprint(key)}, nil
}

func ValidateSignatureTrust(trust SignatureTrust) error {
	if strings.TrimSpace(trust.SourceID) == "" || (trust.SourceKind != SourceKindOfficial && trust.SourceKind != SourceKindCustom) || strings.TrimSpace(trust.KeyID) == "" {
		return errors.New("package signature source binding is incomplete")
	}
	if err := plugins.ValidateSignerKeyID(trust.KeyID); err != nil {
		return errors.New("package signature key binding is invalid")
	}
	key, err := decodeSourceSignerPublicKey(trust.PublicKey)
	if err != nil || trust.Fingerprint != signerFingerprint(key) {
		return errors.New("package signature key binding is invalid")
	}
	if trust.SourceKind == SourceKindOfficial {
		if trust.SourceID != OfficialSourceID || trust.KeyID != plugins.OfficialSignatureKeyID || !key.Equal(plugins.DefaultTrustedSigners()[plugins.OfficialSignatureKeyID]) {
			return errors.New("official package signature binding is invalid")
		}
	} else if trust.SourceID == OfficialSourceID || trust.KeyID == plugins.OfficialSignatureKeyID {
		return errors.New("custom package signature binding is invalid")
	}
	return nil
}

func ValidatorForSignatureTrust(trust SignatureTrust) (*plugins.Validator, error) {
	return ValidatorForSignatureTrustWithBase(plugins.NewValidator(plugins.ValidatorOptions{}), trust)
}

// ValidatorForSignatureTrustWithBase preserves the base validator's current
// host/agent/platform compatibility configuration while replacing its trust
// roots with the package's immutable acquisition signer.
func ValidatorForSignatureTrustWithBase(base *plugins.Validator, trust SignatureTrust) (*plugins.Validator, error) {
	if err := ValidateSignatureTrust(trust); err != nil {
		return nil, err
	}
	if base == nil {
		return nil, errors.New("plugin compatibility validator is unavailable")
	}
	key, _ := decodeSourceSignerPublicKey(trust.PublicKey)
	return base.WithTrustedSigners(map[string]ed25519.PublicKey{trust.KeyID: key}, plugins.TrustedSignerPolicyExact), nil
}

func decodeSourceSignerPublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != value || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("invalid Ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}

type SourceValidatorFactory func(Source) (*plugins.Validator, error)

// NewSourceValidatorFactory constructs a fresh verifier per refresh/resolve.
// A custom source receives exactly its persisted signer and can never inherit
// another custom source's trust root.
func NewSourceValidatorFactory(options plugins.ValidatorOptions) SourceValidatorFactory {
	return func(source Source) (*plugins.Validator, error) {
		if source.Kind == SourceKindOfficial {
			return plugins.NewValidator(options), nil
		}
		if err := ValidateSource(source); err != nil {
			return nil, err
		}
		if source.SignerKeyID == "" || source.SignerPublicKey == "" {
			return nil, errors.New("custom marketplace source has no bound signer")
		}
		key, err := decodeSourceSignerPublicKey(source.SignerPublicKey)
		if err != nil {
			return nil, err
		}
		return plugins.NewValidator(options).WithTrustedSigners(map[string]ed25519.PublicKey{source.SignerKeyID: key}, plugins.TrustedSignerPolicyExact), nil
	}
}

type Snapshot struct {
	ID             string                        `json:"id"`
	SourceID       string                        `json:"source_id"`
	Commit         string                        `json:"commit"`
	SourceRevision uint64                        `json:"source_revision"`
	RefKind        string                        `json:"ref_kind"`
	RefName        string                        `json:"ref_name"`
	Path           string                        `json:"-"`
	ValidatedAt    time.Time                     `json:"validated_at"`
	Entries        []plugins.MarketEntry         `json:"entries"`
	DirectPlugin   *plugins.DirectPluginSnapshot `json:"direct_plugin,omitempty"`
}

type RefreshOperation struct {
	ID                string
	SourceID          string
	Commit            string
	SourceRevision    uint64
	RefKind           string
	RefName           string
	SignerSourceKind  string
	SignerKeyID       string
	SignerPublicKey   string
	SignerFingerprint string
	Status            string
	ErrorClass        string
	Error             string
	DiffJSON          string
	StartedAt         time.Time
	FinishedAt        *time.Time
	Actor             OperationActor
	LeaseToken        string
	LeaseExpiresAt    time.Time
}

type RefreshIdentity struct {
	OperationID string
	LeaseToken  string
}

type RefreshIdentityCapture struct {
	mu       sync.RWMutex
	identity RefreshIdentity
}

func WithRefreshIdentityCapture(ctx context.Context) (context.Context, *RefreshIdentityCapture) {
	capture := &RefreshIdentityCapture{}
	return context.WithValue(ctx, refreshIdentityCaptureKey{}, capture), capture
}

func (c *RefreshIdentityCapture) Store(operation RefreshOperation) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.identity = RefreshIdentity{OperationID: operation.ID, LeaseToken: operation.LeaseToken}
	c.mu.Unlock()
}

func (c *RefreshIdentityCapture) Load() RefreshIdentity {
	if c == nil {
		return RefreshIdentity{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.identity
}

type refreshIdentityCaptureKey struct{}

func storeRefreshIdentity(ctx context.Context, operation RefreshOperation) {
	if capture, ok := ctx.Value(refreshIdentityCaptureKey{}).(*RefreshIdentityCapture); ok {
		capture.Store(operation)
	}
}

// OperationActor is trusted request provenance. Credential material is never
// carried in operation metadata or audit events.
type OperationActor struct {
	ActorID       string
	SessionID     string
	CorrelationID string
}

type CredentialAuthorization struct {
	SecretID        string
	ResourceGroupID string
	Actor           OperationActor
}

type credentialAuthorizationKey struct{}

func WithCredentialAuthorization(ctx context.Context, authorization CredentialAuthorization) context.Context {
	return context.WithValue(ctx, credentialAuthorizationKey{}, authorization)
}

func CredentialAuthorizationFromContext(ctx context.Context, secretID string) (CredentialAuthorization, bool) {
	authorization, ok := ctx.Value(credentialAuthorizationKey{}).(CredentialAuthorization)
	return authorization, ok && authorization.SecretID == secretID && authorization.ResourceGroupID != "" && authorization.Actor.ActorID != ""
}

type Repository interface {
	AcquireRefreshLease(context.Context, RefreshOperation) error
	RenewRefreshLease(context.Context, RefreshOperation) error
	RecordRefreshRejection(context.Context, RefreshOperation, string) error
	StagePackageAcquisition(context.Context, string, string, string, SignatureTrust) error
	PublishPackageAcquisition(context.Context, string, string, string, SignatureTrust, func() error) error
	CompletePackageAcquisitions(context.Context, string, string, bool) error
	SaveRefreshOperation(context.Context, RefreshOperation) error
	PromoteSnapshotAndCompleteRefresh(context.Context, Source, Snapshot, RefreshOperation) error
	CurrentSnapshot(context.Context, string) (Snapshot, bool, error)
}

type PackageReferenceChecker interface {
	PackageReferenced(context.Context, string) (bool, error)
}

type DirectoryCleanupRepository interface {
	RegisterMarketplaceDirectoryCleanup(context.Context, string, string, []string) error
}

type SourceDeletion struct {
	SnapshotPaths []string
	CacheDigests  []string
}

type PackageGCIntent struct {
	SourceID          string
	Digest            string
	SignerFingerprint string
}

type PackageAcquisition struct {
	SourceID       string
	Digest         string
	SnapshotID     string
	SourceRevision uint64
	RefKind        string
	RefName        string
	ResolvedOID    string
	Trust          SignatureTrust
}

type PackageGCClaim struct {
	SourceID          string
	Digest            string
	SignerFingerprint string
	Token             string
	QuarantineID      string
	QuarantinePath    string
	Trust             SignatureTrust
	ObjectsPrepared   bool
	Objects           []PackageGCObject
}

const (
	PackageGCLayoutSigner = "signer"
	PackageGCLayoutLegacy = "legacy"
)

// PackageGCObject is one exact cache object owned by a durable signer-variant
// claim. Paths are managed-root-relative and remain stable across lease-token
// takeover so a replacement worker can reconcile an interrupted quarantine.
type PackageGCObject struct {
	Layout            string `json:"layout"`
	Path              string `json:"path"`
	QuarantinePath    string `json:"quarantine_path"`
	SignerFingerprint string `json:"signer_fingerprint"`
}

type DirectoryCleanupWork struct {
	ID          string
	SourceID    string
	OperationID string
	Path        string
	ClaimToken  string
}

type Fetcher interface {
	Fetch(context.Context, Source, string) (string, error)
}
