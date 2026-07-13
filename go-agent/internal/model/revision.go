package model

import "time"

type RevisionLease struct {
	AgentID             string    `json:"agent_id"`
	Revision            int64     `json:"revision"`
	RetryCycle          int       `json:"retry_cycle"`
	Attempt             int       `json:"attempt"`
	LeaseID             string    `json:"lease_id"`
	SnapshotDigest      string    `json:"snapshot_digest"`
	DesiredVersion      string    `json:"desired_version"`
	ApplyTimeoutSeconds int       `json:"apply_timeout_seconds"`
	DrainTimeoutSeconds int       `json:"drain_timeout_seconds"`
	DeadlineAt          time.Time `json:"deadline_at"`
}

type RevisionPull struct {
	HasUpdate              bool           `json:"has_update"`
	DesiredRevision        int64          `json:"desired_revision"`
	Lease                  *RevisionLease `json:"lease,omitempty"`
	Snapshot               *Snapshot      `json:"snapshot,omitempty"`
	VerifiedSnapshotDigest string         `json:"-"`
}

type RevisionStart struct {
	AgentID      string `json:"agent_id"`
	Revision     int64  `json:"revision"`
	RetryCycle   int    `json:"retry_cycle"`
	Attempt      int    `json:"attempt"`
	LeaseID      string `json:"lease_id"`
	GenerationID string `json:"generation_id"`
}

type RevisionReport struct {
	AgentID      string `json:"agent_id"`
	Revision     int64  `json:"revision"`
	RetryCycle   int    `json:"retry_cycle"`
	Attempt      int    `json:"attempt"`
	LeaseID      string `json:"lease_id"`
	GenerationID string `json:"generation_id"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	Forced       bool   `json:"forced,omitempty"`
	ForceReason  string `json:"force_reason,omitempty"`
}

const (
	GenerationPhasePrepared = "prepared"
	GenerationPhaseStarting = "starting"
	GenerationPhaseStarted  = "started"
	GenerationPhaseCutover  = "cutover"
	GenerationPhaseActive   = "active"
	GenerationPhaseFailed   = "failed"
)

type GenerationRecord struct {
	GenerationID   string        `json:"generation_id"`
	Revision       int64         `json:"revision"`
	SnapshotDigest string        `json:"snapshot_digest"`
	Phase          string        `json:"phase"`
	Lease          RevisionLease `json:"lease"`
	Acknowledged   bool          `json:"acknowledged"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type GenerationJournal struct {
	Version       int               `json:"version"`
	AgentID       string            `json:"agent_id"`
	Active        *GenerationRecord `json:"active,omitempty"`
	Candidate     *GenerationRecord `json:"candidate,omitempty"`
	LastKnownGood *GenerationRecord `json:"last_known_good,omitempty"`
}
