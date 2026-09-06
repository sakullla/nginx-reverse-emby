package storage

import (
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const DatasetArtifactKind = "dataset-index-v1"
const revisionDatasetArtifactRolePrefix = "dataset_artifact:"

// These projections mirror the Agent snapshot and intentionally contain no
// raw source blob, executable identity, remote retrieval credential or grant.
type DatasetSnapshot struct {
	Version  pluginsdk.DatasetVersion `json:"version"`
	Artifact DatasetArtifact          `json:"artifact"`
	Bindings []DatasetInstanceBinding `json:"bindings"`
}
type DatasetArtifact struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	LocalPath string `json:"local_path,omitempty"`
}
type DatasetInstanceBinding struct {
	InstanceID      string                            `json:"instance_id"`
	Classifications []pluginsdk.DatasetClassification `json:"classifications"`
}

type DatasetSourceRow struct {
	ID              string `gorm:"primaryKey"`
	ResourceGroupID string `gorm:"not null;index"`
	SourceJSON      string `gorm:"not null"`
	RetrievalJSON   string `gorm:"not null"`
	CurrentDigest   string
	LastFailure     string
	LastRefreshAt   *time.Time
	NextRefreshAt   *time.Time `gorm:"index"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (DatasetSourceRow) TableName() string { return "dataset_sources" }

type DatasetVersionRow struct {
	SourceID          string    `gorm:"primaryKey"`
	Digest            string    `gorm:"primaryKey"`
	VersionJSON       string    `gorm:"not null"`
	VerificationJSON  string    `gorm:"not null;default:''"`
	ArtifactID        string    `gorm:"not null;index"`
	ArtifactSHA256    string    `gorm:"not null"`
	ArtifactSizeBytes int64     `gorm:"not null"`
	ArtifactPath      string    `gorm:"not null"`
	CreatedAt         time.Time `gorm:"index"`
}

func (DatasetVersionRow) TableName() string { return "dataset_versions" }

// DatasetVersionVerification captures the sidecar evidence for immutable bytes.
// It does not follow subsequent changes to the source retrieval configuration.
type DatasetVersionVerification struct {
	Mode           string `json:"mode"`
	ChecksumURL    string `json:"checksum_url"`
	ChecksumDigest string `json:"checksum_digest"`
	ExpectedDigest string `json:"expected_digest"`
	ResolvedAt     string `json:"resolved_at"`
}

type DatasetBindingRow struct {
	AgentID             string `gorm:"primaryKey"`
	InstanceID          string `gorm:"primaryKey"`
	SourceID            string `gorm:"primaryKey"`
	VersionDigest       string `gorm:"not null;index"`
	ClassificationsJSON string `gorm:"not null"`
	Revision            int64  `gorm:"not null"`
}

func (DatasetBindingRow) TableName() string { return "dataset_bindings" }

type DatasetUploadRow struct {
	SourceID   string `gorm:"primaryKey"`
	Digest     string `gorm:"primaryKey"`
	ArtifactID string `gorm:"not null;index"`
	CreatedAt  time.Time
}

func (DatasetUploadRow) TableName() string { return "dataset_uploads" }
