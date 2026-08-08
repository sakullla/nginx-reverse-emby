package marketplace

import (
	"context"
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
	MaxSourceNameBytes      = 190
	MaxSourceURLBytes       = 2048
	MaxSourceReferenceBytes = 512
	MaxCredentialRefBytes   = 190
	OfficialRefreshInterval = 6 * time.Hour
)

var sourceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

var (
	ErrRefreshLeaseHeld = errors.New("marketplace source refresh lease is already held")
	ErrInvalidSource    = errors.New("invalid marketplace source")
)

type Source struct {
	ID              string        `json:"id"`
	Kind            string        `json:"kind"`
	Name            string        `json:"name"`
	URL             string        `json:"url"`
	Reference       string        `json:"reference"`
	CredentialRef   string        `json:"credential_ref,omitempty"`
	RefreshInterval time.Duration `json:"refresh_interval_ns"`
	RiskLabel       string        `json:"risk_label,omitempty"`
	CurrentSnapshot string        `json:"current_snapshot,omitempty"`
	LastResult      string        `json:"last_result,omitempty"`
	LastError       string        `json:"last_error,omitempty"`
	UpdatedAt       time.Time     `json:"updated_at"`
	LastCompletedAt time.Time     `json:"last_completed_at,omitempty"`
	LeaseExpiresAt  time.Time     `json:"-"`
	Deleting        bool          `json:"-"`
}

func OfficialSource() Source {
	return Source{ID: OfficialSourceID, Kind: SourceKindOfficial, Name: "Sakullla Official", URL: OfficialSourceURL, Reference: "main", RefreshInterval: OfficialRefreshInterval}
}

func EffectiveRefreshInterval(source Source) time.Duration {
	if source.Kind == SourceKindOfficial && source.RefreshInterval == 0 {
		return OfficialRefreshInterval
	}
	return source.RefreshInterval
}

func NewCustomSource(id, name, remoteURL, reference, credentialRef string, refreshInterval time.Duration) (Source, error) {
	source := Source{
		ID: strings.ToLower(strings.TrimSpace(id)), Kind: SourceKindCustom, Name: strings.TrimSpace(name), URL: strings.TrimSpace(remoteURL),
		Reference: strings.TrimSpace(reference), CredentialRef: strings.TrimSpace(credentialRef), RefreshInterval: refreshInterval, RiskLabel: UntrustedRiskLabel,
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
	if len(source.Name) > MaxSourceNameBytes || len(source.URL) > MaxSourceURLBytes || len(source.Reference) > MaxSourceReferenceBytes || len(source.CredentialRef) > MaxCredentialRefBytes {
		return errors.New("marketplace source field exceeds persistence limit")
	}
	if source.Kind == SourceKindOfficial {
		official := OfficialSource()
		if source.ID != official.ID || source.URL != official.URL || source.Name != official.Name || source.Reference != official.Reference || source.CredentialRef != "" {
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
	if source.Reference == "" {
		return errors.New("custom source reference is required")
	}
	if source.RiskLabel != UntrustedRiskLabel {
		return errors.New("custom sources must retain the untrusted supply-chain risk label")
	}
	return nil
}

type Snapshot struct {
	ID          string                `json:"id"`
	SourceID    string                `json:"source_id"`
	Commit      string                `json:"commit"`
	Path        string                `json:"-"`
	ValidatedAt time.Time             `json:"validated_at"`
	Entries     []plugins.MarketEntry `json:"entries"`
}

type RefreshOperation struct {
	ID             string
	SourceID       string
	Commit         string
	Status         string
	ErrorClass     string
	Error          string
	DiffJSON       string
	StartedAt      time.Time
	FinishedAt     *time.Time
	Actor          OperationActor
	LeaseToken     string
	LeaseExpiresAt time.Time
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
	RecordRefreshRejection(context.Context, string, OperationActor, string) error
	StagePackageAcquisition(context.Context, string, string, string) error
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
	SourceID string
	Digest   string
}

type PackageGCClaim struct {
	SourceID       string
	Digest         string
	Token          string
	QuarantinePath string
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
