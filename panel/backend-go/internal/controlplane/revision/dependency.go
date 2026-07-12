package revision

import (
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type DependencyAction = dependency.Action

const (
	DependencyActionApply  = dependency.ActionApply
	DependencyActionDelete = dependency.ActionDelete
)

func validateDependencyAction(action DependencyAction) error {
	switch action {
	case "", DependencyActionApply, DependencyActionDelete:
		return nil
	default:
		return wrapError(ErrorCodeInvalidRequest, "unsupported dependency action %q", action)
	}
}

func appendDependencyPlan(
	ledger *storage.RevisionLedgerWrite,
	operationID string,
	action DependencyAction,
	targets []Target,
	allocated map[string]int64,
	before map[string]storage.Snapshot,
	after map[string]storage.Snapshot,
	now time.Time,
) error {
	if action == "" || len(ledger.Revisions) == 0 {
		return nil
	}

	revisions := make([]dependency.SnapshotRevision, 0, len(targets))
	for _, target := range targets {
		revisionNumber := allocated[target.AgentID]
		snapshot := after[target.AgentID]
		if action == DependencyActionDelete {
			snapshot = before[target.AgentID]
		} else {
			snapshot.Revision = revisionNumber
		}
		revisions = append(revisions, dependency.SnapshotRevision{
			AgentID: target.AgentID, Revision: revisionNumber, Snapshot: snapshot,
		})
	}

	plan, err := dependency.BuildPlan(operationID, action, revisions)
	if err != nil {
		return NewError(ErrorCodeUnprocessable, "dependency plan is invalid", err)
	}
	payload, err := plan.Marshal()
	if err != nil {
		return NewError(ErrorCodeUnprocessable, "dependency plan is invalid", err)
	}
	digest := plan.Digest()
	if strings.TrimSpace(digest) == "" {
		return NewError(ErrorCodeInternal, "dependency plan digest could not be computed", nil)
	}
	artifactID := "dependency-plan-" + digest
	ledger.Artifacts = append(ledger.Artifacts, storage.GenerationArtifactRow{
		ID: artifactID, Kind: storage.GenerationArtifactKindDependencyPlan,
		SHA256: digest, Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now,
	})
	for _, revision := range ledger.Revisions {
		ledger.ArtifactRefs = append(ledger.ArtifactRefs, storage.AgentRevisionArtifactRow{
			AgentID: revision.AgentID, Revision: revision.Revision,
			ArtifactID: artifactID, Role: storage.RevisionArtifactRoleDependencyPlan,
			CreatedAt: now,
		})
	}
	return nil
}
