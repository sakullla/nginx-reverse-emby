package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	HostRuntimeDatasetQuery   = "dataset.query"
	HostRuntimeDatasetControl = "dataset.control"
	HostRuntimeDatasetStatus  = "dataset.status"
	HostRuntimeDatasetCatalog = "dataset.catalog"

	DatasetMaxQueryClassifications       = 64
	DatasetMaxQueryDurationMicros        = 2000
	DatasetMaxQueryResponseBytes         = 32 << 10
	DatasetMaxCatalogPage                = 128
	DatasetMaxClassifications            = 10000
	DatasetMaxAttributes                 = 32
	DatasetMaxDownloadBytes        int64 = 128 << 20
	DatasetMaxExpandedBytes        int64 = 256 << 20
	DatasetDefaultIndexBudgetBytes int64 = 512 << 20
	DatasetMaxEntries                    = 2000000
	DatasetMaxDependencyDepth            = 64
)

type DatasetFormat string

const (
	DatasetFormatGeoIP     DatasetFormat = "geoip"
	DatasetFormatGeoSite   DatasetFormat = "geosite"
	DatasetFormatCommunity DatasetFormat = "community"
	DatasetFormatCIDR      DatasetFormat = "cidr"
	DatasetFormatGeoMMDB   DatasetFormat = "geo-mmdb"
)

func (format DatasetFormat) Validate() error {
	switch format {
	case DatasetFormatGeoIP, DatasetFormatGeoSite, DatasetFormatCommunity, DatasetFormatCIDR, DatasetFormatGeoMMDB:
		return nil
	default:
		return errors.New("unsupported dataset format")
	}
}

type DatasetClassificationKind string

const (
	DatasetClassificationCountry DatasetClassificationKind = "country"
	DatasetClassificationRegion  DatasetClassificationKind = "region"
	DatasetClassificationCIDR    DatasetClassificationKind = "cidr"
	DatasetClassificationDomain  DatasetClassificationKind = "domain"
)

func (kind DatasetClassificationKind) Validate() error {
	switch kind {
	case DatasetClassificationCountry, DatasetClassificationRegion, DatasetClassificationCIDR, DatasetClassificationDomain:
		return nil
	default:
		return errors.New("unsupported dataset classification kind")
	}
}

var (
	datasetNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.!@+-]{0,127}$`)
	datasetDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	datasetHandlePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,256}$`)
)

// ValidateDatasetClassificationName accepts source classification names, including
// category-ai-!cn. These are data keys, not policy IDs, file paths, or expressions.
// Attribute selection is represented separately, without interpreting '@' here.
func ValidateDatasetClassificationName(name string) error {
	if !datasetNamePattern.MatchString(name) || name == "." || name == ".." {
		return errors.New("invalid dataset classification name")
	}
	return nil
}

func validateDatasetDigest(digest string) error {
	if !datasetDigestPattern.MatchString(digest) {
		return errors.New("dataset digest must be canonical SHA-256")
	}
	return nil
}

func validateDatasetText(value string, max int) error {
	if value == "" || len(value) > max || !utf8.ValidString(value) || value != strings.TrimSpace(value) {
		return errors.New("invalid bounded dataset text")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return errors.New("dataset text contains a control character")
		}
	}
	return nil
}

// DatasetSource describes a Host-managed source. URL validation checks syntax
// only: Host fetch must independently authorize destinations, redirects, DNS,
// credentials and size/time budgets. Private sources require an explicit grant.
// LicenseURL records metadata, not proof that the underlying data is licensed.
// An empty URL denotes an upload-only source and requires refresh to be disabled.
type DatasetSource struct {
	ID                     string        `json:"id"`
	Name                   string        `json:"name"`
	URL                    string        `json:"url,omitempty"`
	Format                 DatasetFormat `json:"format"`
	LicenseURL             string        `json:"license_url,omitempty"`
	AttributionText        string        `json:"attribution_text,omitempty"`
	AttributionURL         string        `json:"attribution_url,omitempty"`
	RefreshIntervalSeconds int64         `json:"refresh_interval_seconds"`
}

func validateDatasetURL(raw string) error {
	if err := validateDatasetText(raw, 2048); err != nil {
		return err
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("dataset URL must be HTTP(S), without credentials or fragment")
	}
	return nil
}

func (source DatasetSource) Validate() error {
	if err := ValidatePolicyIdentity(source.ID); err != nil {
		return err
	}
	if err := validateDatasetText(source.Name, 128); err != nil {
		return err
	}
	if source.URL != "" {
		if err := validateDatasetURL(source.URL); err != nil {
			return err
		}
	} else if source.RefreshIntervalSeconds != 0 {
		return errors.New("upload-only dataset source cannot schedule remote refresh")
	}
	if err := source.Format.Validate(); err != nil {
		return err
	}
	if source.LicenseURL != "" {
		if err := validateDatasetURL(source.LicenseURL); err != nil {
			return err
		}
	}
	if err := validateDatasetAttribution(source.AttributionText, source.AttributionURL); err != nil {
		return err
	}
	if source.RefreshIntervalSeconds != 0 && (source.RefreshIntervalSeconds < 3600 || source.RefreshIntervalSeconds > 366*24*3600) {
		return errors.New("dataset refresh interval must be disabled or between one hour and 366 days")
	}
	return nil
}

// Attribution is plain text and a separately validated HTTP(S) link. Render
// text escaped, never as HTML; do not infer licensing authority from metadata.
func validateDatasetAttribution(text, link string) error {
	if text != "" {
		if err := validateDatasetText(text, 512); err != nil {
			return err
		}
	}
	if link != "" {
		return validateDatasetURL(link)
	}
	return nil
}

type DatasetFamilyCoverage string

const (
	DatasetFamilyComplete DatasetFamilyCoverage = "complete"
	DatasetFamilyPartial  DatasetFamilyCoverage = "partial"
	DatasetFamilyNone     DatasetFamilyCoverage = "none"
)

type DatasetAddressCoverage struct {
	IPv4 DatasetFamilyCoverage `json:"ipv4"`
	IPv6 DatasetFamilyCoverage `json:"ipv6"`
}

func (coverage DatasetAddressCoverage) Validate() error {
	for _, family := range []DatasetFamilyCoverage{coverage.IPv4, coverage.IPv6} {
		switch family {
		case DatasetFamilyComplete, DatasetFamilyPartial, DatasetFamilyNone:
		default:
			return errors.New("invalid dataset address coverage")
		}
	}
	return nil
}

// DatasetVersion is immutable provenance and index metadata. Digest identifies
// the complete version manifest; RawDigest and IndexDigest identify its separate
// data artifacts. Classifications are retrieved in bounded catalog pages.
type DatasetVersion struct {
	SourceID            string                 `json:"source_id"`
	Digest              string                 `json:"digest"`
	SourceURL           string                 `json:"source_url,omitempty"`
	LicenseURL          string                 `json:"license_url,omitempty"`
	AttributionText     string                 `json:"attribution_text,omitempty"`
	AttributionURL      string                 `json:"attribution_url,omitempty"`
	Revision            string                 `json:"revision"`
	FetchedAt           string                 `json:"fetched_at"`
	RawDigest           string                 `json:"raw_digest"`
	IndexDigest         string                 `json:"index_digest"`
	Format              DatasetFormat          `json:"format"`
	SemanticVersion     string                 `json:"semantic_version"`
	ClassificationCount int                    `json:"classification_count"`
	EntryCount          int                    `json:"entry_count"`
	IndexBytes          int64                  `json:"index_bytes"`
	Coverage            DatasetAddressCoverage `json:"coverage"`
}

func (version DatasetVersion) Validate() error {
	if err := ValidatePolicyIdentity(version.SourceID); err != nil {
		return err
	}
	for _, digest := range []string{version.Digest, version.RawDigest, version.IndexDigest} {
		if err := validateDatasetDigest(digest); err != nil {
			return err
		}
	}
	if version.SourceURL != "" {
		if err := validateDatasetURL(version.SourceURL); err != nil {
			return err
		}
	}
	if version.LicenseURL != "" {
		if err := validateDatasetURL(version.LicenseURL); err != nil {
			return err
		}
	}
	if err := validateDatasetAttribution(version.AttributionText, version.AttributionURL); err != nil {
		return err
	}
	if err := validateDatasetText(version.Revision, 256); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, version.FetchedAt); err != nil || len(version.FetchedAt) > 64 {
		return errors.New("invalid dataset fetch timestamp")
	}
	if err := version.Format.Validate(); err != nil {
		return err
	}
	if err := ValidatePolicyIdentity(version.SemanticVersion); err != nil {
		return err
	}
	if version.ClassificationCount < 1 || version.ClassificationCount > DatasetMaxClassifications || version.EntryCount < 1 || version.EntryCount > DatasetMaxEntries || version.IndexBytes < 1 {
		return errors.New("dataset version counts exceed import bounds")
	}
	return version.Coverage.Validate()
}

// DatasetReference is an opaque Host-issued grant for one immutable snapshot.
// ValidateFor rejects mismatched binding metadata, but is NOT authentication.
// Hosts must resolve Handle in their grant registry, compare all fields against
// the stored grant, and verify caller capability, revocation and quota on every
// use. Plugins cannot mint references by filling in this structure.
type DatasetReference struct {
	Handle        string `json:"handle"`
	InstanceID    string `json:"instance_id"`
	Generation    string `json:"generation"`
	SourceID      string `json:"source_id"`
	VersionDigest string `json:"version_digest"`
}

func (ref DatasetReference) Validate() error {
	if !datasetHandlePattern.MatchString(ref.Handle) {
		return errors.New("invalid opaque dataset handle")
	}
	for _, id := range []string{ref.InstanceID, ref.Generation, ref.SourceID} {
		if err := ValidatePolicyIdentity(id); err != nil {
			return err
		}
	}
	return validateDatasetDigest(ref.VersionDigest)
}

func (ref DatasetReference) ValidateFor(instanceID, generation string) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if ref.InstanceID != instanceID || ref.Generation != generation {
		return errors.New("dataset reference belongs to another instance or generation")
	}
	return nil
}

// DatasetAttribute preserves typed GeoSite boolean/integer attributes. Exactly
// one value is present. Negate selects the complement of the predicate within
// the named classification, not outside that classification.
type DatasetAttribute struct {
	Name    string `json:"name"`
	Boolean *bool  `json:"boolean,omitempty"`
	Integer *int64 `json:"integer,omitempty"`
	Negate  bool   `json:"negate,omitempty"`
}

func (attribute DatasetAttribute) Validate() error {
	if err := ValidateDatasetClassificationName(attribute.Name); err != nil {
		return err
	}
	if (attribute.Boolean == nil) == (attribute.Integer == nil) {
		return errors.New("dataset attribute requires exactly one typed value")
	}
	return nil
}

type DatasetClassification struct {
	Name string                    `json:"name"`
	Kind DatasetClassificationKind `json:"kind"`
	// Attributes are conjunctive predicates; their order has no significance.
	Attributes []DatasetAttribute `json:"attributes,omitempty"`
}

func (classification DatasetClassification) Validate() error {
	if err := ValidateDatasetClassificationName(classification.Name); err != nil {
		return err
	}
	if err := classification.Kind.Validate(); err != nil {
		return err
	}
	if len(classification.Attributes) > DatasetMaxAttributes || (classification.Kind != DatasetClassificationDomain && len(classification.Attributes) != 0) {
		return errors.New("dataset attributes exceed the bound or require domain classifications")
	}
	names := make(map[string]bool, len(classification.Attributes))
	for _, attribute := range classification.Attributes {
		if err := attribute.Validate(); err != nil {
			return err
		}
		if names[attribute.Name] {
			return errors.New("duplicate dataset attribute")
		}
		names[attribute.Name] = true
	}
	return nil
}

type DatasetQueryBudget struct {
	MaxDurationMicros int `json:"max_duration_micros"`
	MaxResponseBytes  int `json:"max_response_bytes"`
}

func (budget DatasetQueryBudget) Validate() error {
	if budget.MaxDurationMicros < 1 || budget.MaxDurationMicros > DatasetMaxQueryDurationMicros || budget.MaxResponseBytes < 1 || budget.MaxResponseBytes > DatasetMaxQueryResponseBytes {
		return errors.New("dataset query requires explicit bounded time and response budgets")
	}
	return nil
}

// DatasetQueryRequest queries only the local, already prepared snapshot. The
// caller provides one address or domain; it carries no trusted-source assertion.
// Admission queries must obtain their address from Host-authenticated connection
// context, never from this caller input. Host query time counts against the
// enclosing admission budget, including any policy/v1 2 ms execution ceiling.
type DatasetQueryRequest struct {
	Reference       DatasetReference        `json:"reference"`
	Classifications []DatasetClassification `json:"classifications"`
	Address         string                  `json:"address,omitempty"`
	Domain          string                  `json:"domain,omitempty"`
	Budget          DatasetQueryBudget      `json:"budget"`
}

func (request DatasetQueryRequest) Validate() error {
	if err := request.Reference.Validate(); err != nil {
		return err
	}
	if err := request.Budget.Validate(); err != nil {
		return err
	}
	if (request.Address == "") == (request.Domain == "") {
		return errors.New("dataset query requires exactly one address or domain")
	}
	if request.Address != "" {
		address, err := netip.ParseAddr(request.Address)
		if err != nil || address.Zone() != "" || address.String() != request.Address {
			return errors.New("dataset query address must be a canonical unscoped IP")
		}
	} else if err := validateDatasetDomain(request.Domain); err != nil {
		return err
	}
	if len(request.Classifications) < 1 || len(request.Classifications) > DatasetMaxQueryClassifications {
		return errors.New("dataset query classification count exceeds the bound")
	}
	seen := make(map[string]bool, len(request.Classifications))
	for _, classification := range request.Classifications {
		if err := classification.Validate(); err != nil {
			return err
		}
		if (classification.Kind == DatasetClassificationDomain) != (request.Domain != "") {
			return errors.New("dataset query input does not support the classification kind")
		}
		key := datasetClassificationKey(classification)
		if seen[key] {
			return errors.New("duplicate dataset query classification")
		}
		seen[key] = true
	}
	return nil
}

func (request DatasetQueryRequest) ValidateFor(instanceID, generation string) error {
	if err := request.Validate(); err != nil {
		return err
	}
	return request.Reference.ValidateFor(instanceID, generation)
}

func validateDatasetDomain(domain string) error {
	if domain == "" || len(domain) > 253 || strings.ToLower(domain) != domain {
		return errors.New("dataset domain must be canonical lower-case ASCII without a trailing dot")
	}
	if _, err := netip.ParseAddr(domain); err == nil {
		return errors.New("dataset domain must not be an IP address")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("invalid dataset domain label")
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return errors.New("dataset domain requires ASCII DNS labels (IDNA must be normalized by caller)")
			}
		}
	}
	return nil
}

func datasetClassificationKey(classification DatasetClassification) string {
	// Attribute predicates are a conjunction; changing their order cannot make
	// a duplicate selector consume another query slot. Do not mutate caller data.
	classification.Attributes = append([]DatasetAttribute(nil), classification.Attributes...)
	sort.Slice(classification.Attributes, func(i, j int) bool { return classification.Attributes[i].Name < classification.Attributes[j].Name })
	encoded, _ := json.Marshal(classification)
	return string(encoded)
}

type DatasetQueryStatus string

const (
	DatasetQueryOK                    DatasetQueryStatus = "ok"
	DatasetQueryUnavailable           DatasetQueryStatus = "unavailable"
	DatasetQueryMissingClassification DatasetQueryStatus = "missing-classification"
	DatasetQueryBudgetExceeded        DatasetQueryStatus = "budget-exceeded"
	DatasetQueryUnauthorized          DatasetQueryStatus = "unauthorized"
	DatasetQueryStaleReference        DatasetQueryStatus = "stale-reference"
	DatasetQueryInvalidData           DatasetQueryStatus = "invalid-data"
)

type DatasetMatchCoverage string

const (
	DatasetCovered           DatasetMatchCoverage = "covered"
	DatasetUnknown           DatasetMatchCoverage = "unknown"
	DatasetUnsupportedFamily DatasetMatchCoverage = "unsupported-family"
)

// DatasetMatch corresponds to the classification at the same request index.
// Covered + false is an ordinary non-match. Unknown or unsupported-family may
// never be represented as a positive match (for example, a province whitelist).
type DatasetMatch struct {
	Index    int                  `json:"index"`
	Matched  bool                 `json:"matched"`
	Coverage DatasetMatchCoverage `json:"coverage"`
}

func (match DatasetMatch) Validate() error {
	if match.Index < 0 || match.Index >= DatasetMaxQueryClassifications {
		return errors.New("invalid dataset match index")
	}
	switch match.Coverage {
	case DatasetCovered:
	case DatasetUnknown, DatasetUnsupportedFamily:
		if match.Matched {
			return errors.New("unknown dataset coverage cannot claim a match")
		}
	default:
		return errors.New("invalid dataset match coverage")
	}
	return nil
}

// DatasetQueryResponse never returns raw data or an unbounded category list.
// Failed queries contain no partial successes. Status is distinct from a
// successful lookup that found no match or has unknown address coverage.
type DatasetQueryResponse struct {
	Reference DatasetReference   `json:"reference"`
	Status    DatasetQueryStatus `json:"status"`
	Matches   []DatasetMatch     `json:"matches,omitempty"`
}

func (response DatasetQueryResponse) Validate() error {
	if err := response.Reference.Validate(); err != nil {
		return err
	}
	switch response.Status {
	case DatasetQueryOK:
		if len(response.Matches) < 1 || len(response.Matches) > DatasetMaxQueryClassifications {
			return errors.New("dataset query success requires bounded matches")
		}
	case DatasetQueryUnavailable, DatasetQueryMissingClassification, DatasetQueryBudgetExceeded,
		DatasetQueryUnauthorized, DatasetQueryStaleReference, DatasetQueryInvalidData:
		if len(response.Matches) != 0 {
			return errors.New("failed dataset query must not return partial matches")
		}
	default:
		return errors.New("invalid dataset query status")
	}
	for index, match := range response.Matches {
		if err := match.Validate(); err != nil {
			return err
		}
		if match.Index != index {
			return errors.New("dataset matches must be complete and in request order")
		}
	}
	return validateDatasetFrame(response, DatasetMaxQueryResponseBytes)
}

func (response DatasetQueryResponse) ValidateFor(request DatasetQueryRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := response.Validate(); err != nil {
		return err
	}
	if response.Reference != request.Reference {
		return errors.New("dataset response snapshot differs from request")
	}
	if response.Status == DatasetQueryOK && len(response.Matches) != len(request.Classifications) {
		return errors.New("dataset response omitted requested classifications")
	}
	for _, match := range response.Matches {
		if request.Domain != "" && match.Coverage != DatasetCovered {
			return errors.New("domain classification results must be covered or report a query failure")
		}
	}
	return validateDatasetFrame(response, request.Budget.MaxResponseBytes)
}

func validateDatasetFrame(value any, maxBytes int) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > maxBytes {
		return errors.New("dataset payload exceeds the frame budget")
	}
	return nil
}

// QueryDatasets validates both sides of the public HostRuntime contract. Host
// authorization is mandatory even if the client's local validation succeeds.
func (client *HostRuntimeClient) QueryDatasets(ctx context.Context, request DatasetQueryRequest) (DatasetQueryResponse, error) {
	var response DatasetQueryResponse
	if err := request.Validate(); err != nil {
		return response, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return response, err
	}
	if err := client.Call(ctx, HostRuntimeCall{Operation: HostRuntimeDatasetQuery, Payload: payload}, &response); err != nil {
		return DatasetQueryResponse{}, err
	}
	if err := response.ValidateFor(request); err != nil {
		return DatasetQueryResponse{}, fmt.Errorf("dataset query response: %w", err)
	}
	return response, nil
}
