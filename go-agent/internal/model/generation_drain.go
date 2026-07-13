package model

import "time"

const (
	GenerationDrainStateApplied          = "applied"
	GenerationDrainStateDraining         = "draining"
	GenerationDrainStateDrained          = "drained"
	GenerationDrainStateForced           = "forced"
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
	AppliedAt          time.Time `json:"applied_at"`
	DrainStartedAt     time.Time `json:"drain_started_at,omitempty"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
}

type GenerationDrainSnapshot struct {
	ActiveGenerationID string                  `json:"active_generation_id"`
	Generations        []GenerationDrainStatus `json:"generations"`
}

func (s GenerationDrainStatus) RevisionReport(lease RevisionLease) RevisionReport {
	return RevisionReport{AgentID: lease.AgentID, Revision: s.Revision, RetryCycle: lease.RetryCycle, Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: s.GenerationID, Status: s.State, Forced: s.State == GenerationDrainStateForced, ForceReason: s.ForceReason}
}
