package storage

import (
	"context"
	"fmt"
	"strings"
)

const (
	GenerationArtifactKindDependencyPlan = "dependency_plan"
	RevisionArtifactRoleDependencyPlan   = "dependency_plan"
)

func (s *GormStore) ListOperationRevisions(ctx context.Context, operationID string) ([]AgentRevisionRow, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return nil, fmt.Errorf("operation id is required")
	}
	var rows []AgentRevisionRow
	if err := s.db.WithContext(ctx).
		Where("operation_id = ?", operationID).
		Order("agent_id, revision").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *GormStore) GetOperationDependencyArtifact(ctx context.Context, operationID string) (GenerationArtifactRow, bool, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return GenerationArtifactRow{}, false, fmt.Errorf("operation id is required")
	}
	revisions, err := s.ListOperationRevisions(ctx, operationID)
	if err != nil {
		return GenerationArtifactRow{}, false, err
	}
	if len(revisions) == 0 {
		return GenerationArtifactRow{}, false, nil
	}

	var refs []AgentRevisionArtifactRow
	if err := s.db.WithContext(ctx).
		Table("agent_revision_artifacts AS refs").
		Select("refs.agent_id, refs.revision, refs.artifact_id, refs.role, refs.created_at").
		Joins("JOIN agent_revisions AS revisions ON revisions.agent_id = refs.agent_id AND revisions.revision = refs.revision").
		Where("revisions.operation_id = ? AND refs.role = ?", operationID, RevisionArtifactRoleDependencyPlan).
		Order("refs.agent_id, refs.revision, refs.artifact_id").
		Scan(&refs).Error; err != nil {
		return GenerationArtifactRow{}, false, err
	}
	if len(refs) == 0 {
		return GenerationArtifactRow{}, false, nil
	}
	if len(refs) != len(revisions) {
		return GenerationArtifactRow{}, false, fmt.Errorf("operation %q dependency plan references are incomplete", operationID)
	}

	artifactID := refs[0].ArtifactID
	for i := range revisions {
		if refs[i].AgentID != revisions[i].AgentID || refs[i].Revision != revisions[i].Revision {
			return GenerationArtifactRow{}, false, fmt.Errorf("operation %q dependency plan references do not match revisions", operationID)
		}
		if refs[i].ArtifactID != artifactID {
			return GenerationArtifactRow{}, false, fmt.Errorf("operation %q references multiple dependency plans", operationID)
		}
	}
	artifact, found, err := s.GetGenerationArtifact(ctx, artifactID)
	if err != nil {
		return GenerationArtifactRow{}, false, err
	}
	if !found {
		return GenerationArtifactRow{}, false, fmt.Errorf("operation %q dependency plan artifact %q does not exist", operationID, artifactID)
	}
	if artifact.Kind != GenerationArtifactKindDependencyPlan {
		return GenerationArtifactRow{}, false, fmt.Errorf("operation %q artifact %q has kind %q, want %q", operationID, artifactID, artifact.Kind, GenerationArtifactKindDependencyPlan)
	}
	if err := validateGenerationArtifact(artifact); err != nil {
		return GenerationArtifactRow{}, false, err
	}
	if artifact.ID != "dependency-plan-"+artifact.SHA256 {
		return GenerationArtifactRow{}, false, fmt.Errorf("operation %q dependency plan artifact is not content-addressed", operationID)
	}
	return artifact, true, nil
}
