// Package datasets compiles complete data candidates into immutable local
// classification indices. It owns parsing and matching, never plugin actions,
// network fetch authorization, or runtime grant issuance.
package datasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type Limits struct {
	MaxDownloadBytes     int64
	MaxExpandedBytes     int64
	MaxIndexBytes        int64
	MaxMemoryBytes       int64
	MaxEntries           int
	MaxClassifications   int
	MaxDependencyDepth   int
	MaxRegexBytes        int
	MaxRegexInstructions int
	MaxScanRecords       int
	MaxImportDuration    time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		MaxDownloadBytes: sdk.DatasetMaxDownloadBytes, MaxExpandedBytes: sdk.DatasetMaxExpandedBytes,
		MaxIndexBytes: sdk.DatasetDefaultIndexBudgetBytes, MaxMemoryBytes: sdk.DatasetDefaultIndexBudgetBytes,
		MaxEntries: sdk.DatasetMaxEntries, MaxClassifications: sdk.DatasetMaxClassifications, MaxDependencyDepth: sdk.DatasetMaxDependencyDepth,
		MaxRegexBytes: 4096, MaxRegexInstructions: 8192, MaxScanRecords: 20000000, MaxImportDuration: 120 * time.Second,
	}
}

func (limits Limits) normalized() (Limits, error) {
	if limits == (Limits{}) {
		return DefaultLimits(), nil
	}
	defaults := DefaultLimits()
	if limits.MaxDownloadBytes <= 0 || limits.MaxDownloadBytes > defaults.MaxDownloadBytes || limits.MaxExpandedBytes <= 0 || limits.MaxExpandedBytes > defaults.MaxExpandedBytes || limits.MaxIndexBytes <= 0 || limits.MaxIndexBytes > defaults.MaxIndexBytes || limits.MaxMemoryBytes <= 0 || limits.MaxMemoryBytes > defaults.MaxMemoryBytes || limits.MaxEntries <= 0 || limits.MaxEntries > defaults.MaxEntries || limits.MaxClassifications <= 0 || limits.MaxClassifications > defaults.MaxClassifications || limits.MaxDependencyDepth <= 0 || limits.MaxDependencyDepth > defaults.MaxDependencyDepth || limits.MaxRegexBytes <= 0 || limits.MaxRegexBytes > defaults.MaxRegexBytes || limits.MaxRegexInstructions <= 0 || limits.MaxRegexInstructions > defaults.MaxRegexInstructions || limits.MaxScanRecords <= 0 || limits.MaxScanRecords > defaults.MaxScanRecords || limits.MaxImportDuration <= 0 || limits.MaxImportDuration > defaults.MaxImportDuration {
		return Limits{}, errors.New("dataset limits must be positive and cannot exceed canonical ceilings")
	}
	return limits, nil
}

type Input struct {
	Source         sdk.DatasetSource
	Revision       string
	FetchedAt      string
	ExpectedDigest string
	Data           []byte
	// Files is a complete community file collection at Revision, keyed by category.
	// Its digest is the canonical sorted name/length/content stream from FilesDigest.
	// Data alternatively contains a complete repository tar.gz/zip, never both.
	Files map[string][]byte
}

type Stats struct {
	EntryCount           int
	ClassificationCount  int
	IndexBytes           int64 // canonical encoded artifact length
	EstimatedMemoryBytes int64 // conservative retained index estimate, not process RSS
	ScannedRecords       int
	IPv4Prefixes         int
	IPv6Prefixes         int
}

type Error struct {
	Code   sdk.DatasetFailureCode
	Detail string
}

func (err *Error) Error() string { return string(err.Code) + ": " + err.Detail }
func invalid(format string, args ...any) error {
	return &Error{Code: sdk.DatasetFailureInvalidData, Detail: fmt.Sprintf(format, args...)}
}
func exhausted(detail string) error { return &Error{Code: sdk.DatasetFailureBudget, Detail: detail} }
func checkContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return exhausted("import or query deadline/cancellation reached")
	}
	return nil
}
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type domainRule struct {
	Type       string                 `json:"type"`
	Value      string                 `json:"value"`
	Attributes []sdk.DatasetAttribute `json:"attributes,omitempty"`
}
type groupWire struct {
	Name        string                        `json:"name"`
	Kind        sdk.DatasetClassificationKind `json:"kind"`
	DisplayName string                        `json:"display_name"`
	Inverse     bool                          `json:"inverse,omitempty"`
	Prefixes    []string                      `json:"prefixes,omitempty"`
	Domains     []domainRule                  `json:"domains,omitempty"`
	// Region membership is known only inside the union of region prefixes.
	Coverage sdk.DatasetAddressCoverage `json:"coverage"`
}
type provenance struct {
	Source    sdk.DatasetSource `json:"source"`
	Revision  string            `json:"revision"`
	FetchedAt string            `json:"fetched_at"`
	RawDigest string            `json:"raw_digest"`
}
type indexWire struct {
	Schema     string      `json:"schema"`
	Provenance provenance  `json:"provenance"`
	Groups     []groupWire `json:"groups"`
}

const indexSchema = "nre.dataset-index/v1"

// CIDRDocument is the native portable country/region/CIDR input format. Country
// and region are separate dimensions. Prefixes are canonical masked CIDRs.
type CIDRDocument struct {
	Schema          string               `json:"schema"`
	Classifications []CIDRClassification `json:"classifications"`
}
type CIDRClassification struct {
	Name        string                        `json:"name"`
	Kind        sdk.DatasetClassificationKind `json:"kind"`
	DisplayName string                        `json:"display_name"`
	CIDRs       []string                      `json:"cidrs"`
}

const CIDRSchema = "nre.cidr-dataset/v1"
