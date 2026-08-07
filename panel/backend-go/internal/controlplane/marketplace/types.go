package marketplace

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

const (
	SourceKindOfficial = "official"
	SourceKindCustom   = "custom"
	OfficialSourceID   = "official"
	OfficialSourceURL  = "https://github.com/sakullla/sakullla-plugins.git"
	UntrustedRiskLabel = "UNOFFICIAL_SOURCE_SUPPLY_CHAIN_RISK"
)

var sourceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

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
}

func OfficialSource() Source {
	return Source{ID: OfficialSourceID, Kind: SourceKindOfficial, Name: "Sakullla Official", URL: OfficialSourceURL, Reference: "main"}
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
	if source.Kind == SourceKindOfficial {
		official := OfficialSource()
		if source.ID != official.ID || source.URL != official.URL || source.Name != official.Name || source.CredentialRef != "" {
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
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "ssh") || parsed.Host == "" || parsed.User != nil {
		return errors.New("custom source URL must be an https or ssh URL without embedded credentials")
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
	ID         string
	SourceID   string
	Commit     string
	Status     string
	ErrorClass string
	Error      string
	DiffJSON   string
	StartedAt  time.Time
	FinishedAt *time.Time
}

type Repository interface {
	SaveRefreshOperation(context.Context, RefreshOperation) error
	PromoteSnapshot(context.Context, Source, Snapshot) error
	CurrentSnapshot(context.Context, string) (Snapshot, bool, error)
}

type PackageReferenceChecker interface {
	PackageReferenced(context.Context, string) (bool, error)
}

type Fetcher interface {
	Fetch(context.Context, Source, string) (string, error)
}
