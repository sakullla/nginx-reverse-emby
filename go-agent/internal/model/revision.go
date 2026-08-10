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

func (s Snapshot) HasFullRevisionPayload() bool {
	return s.HasAgentConfig() &&
		s.Rules != nil &&
		s.L4Rules != nil &&
		s.RelayListeners != nil &&
		s.EgressProfiles != nil &&
		s.Certificates != nil &&
		s.CertificatePolicies != nil &&
		s.PluginGenerations != nil &&
		s.PluginPolicies != nil
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
	AgentID        string                `json:"agent_id"`
	Revision       int64                 `json:"revision"`
	RetryCycle     int                   `json:"retry_cycle"`
	Attempt        int                   `json:"attempt"`
	LeaseID        string                `json:"lease_id"`
	GenerationID   string                `json:"generation_id"`
	Status         string                `json:"status"`
	ErrorCode      string                `json:"error_code,omitempty"`
	ErrorMessage   string                `json:"error_message,omitempty"`
	Forced         bool                  `json:"forced,omitempty"`
	ForceReason    string                `json:"force_reason,omitempty"`
	PluginStatuses []PluginRuntimeStatus `json:"plugin_statuses,omitempty"`
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
	GenerationID        string        `json:"generation_id"`
	RuntimeGenerationID string        `json:"runtime_generation_id,omitempty"`
	RuntimeSnapshotHash string        `json:"runtime_snapshot_hash,omitempty"`
	Revision            int64         `json:"revision"`
	SnapshotDigest      string        `json:"snapshot_digest"`
	Phase               string        `json:"phase"`
	Lease               RevisionLease `json:"lease"`
	Acknowledged        bool          `json:"acknowledged"`
	// AppliedReportRejected records that the coordinator terminally rejected
	// this locally active generation's applied report because its lease is no
	// longer current. The report must not be retried or sent as a predecessor.
	AppliedReportRejected bool      `json:"applied_report_rejected,omitempty"`
	ErrorCode             string    `json:"error_code,omitempty"`
	ErrorMessage          string    `json:"error_message,omitempty"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type GenerationJournal struct {
	Version       int               `json:"version"`
	AgentID       string            `json:"agent_id"`
	Active        *GenerationRecord `json:"active,omitempty"`
	Candidate     *GenerationRecord `json:"candidate,omitempty"`
	LastKnownGood *GenerationRecord `json:"last_known_good,omitempty"`
	// Draining retains the protocol generation identities of predecessors until
	// their runtime resources are terminal and the control plane acknowledges
	// the drain report. It is optional for journal v1 backward compatibility.
	Draining []GenerationRecord `json:"draining,omitempty"`
}
