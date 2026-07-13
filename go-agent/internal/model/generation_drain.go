package model

import "time"

const (
	GenerationDrainStateApplied          = "applied"
	GenerationDrainStateDraining         = "draining"
	GenerationDrainStateDrained          = "drained"
	GenerationDrainStateForced           = "forced"
	GenerationDrainStateCleanupFailed    = "cleanup_failed"
	GenerationForceReasonTimeout         = "timeout"
	GenerationForceReasonGenerationLimit = "generation_limit"
	GenerationForceReasonEntityDeleted   = "entity_deleted"
	GenerationForceReasonEntityDisabled  = "entity_disabled"
)

type GenerationDrainStatus struct {
	GenerationID       string    `json:"generation_id"`
	Revision           int64     `json:"revision"`
	State              string    `json:"state"`
	SessionCount       int       `json:"session_count"`
	ForcedSessionCount int       `json:"forced_session_count,omitempty"`
	ForceReason        string    `json:"force_reason,omitempty"`
	CleanupError       string    `json:"cleanup_error,omitempty"`
	AppliedAt          time.Time `json:"applied_at"`
	DrainStartedAt     time.Time `json:"drain_started_at,omitempty"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
}

type GenerationDrainSnapshot struct {
	ActiveGenerationID string                  `json:"active_generation_id"`
	Generations        []GenerationDrainStatus `json:"generations"`
}

func (s GenerationDrainStatus) RevisionReport(lease RevisionLease) RevisionReport {
	report := RevisionReport{AgentID: lease.AgentID, Revision: s.Revision, RetryCycle: lease.RetryCycle, Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: s.GenerationID, Status: s.State, Forced: s.State == GenerationDrainStateForced || s.ForceReason != "", ForceReason: s.ForceReason}
	if s.CleanupError != "" {
		report.ErrorCode = "generation_cleanup_failed"
		report.ErrorMessage = s.CleanupError
	}
	return report
}
