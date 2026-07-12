package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrDependencyClaimRequired = storage.ErrCoordinatorDependencyClaimRequired

type dependencyRepository interface {
	GetCoordinatorRevision(context.Context, string, int64) (storage.AgentRevisionRow, bool, error)
	GetAgentRevisionPointer(context.Context, string) (storage.AgentRevisionPointerRow, bool, error)
	GetGenerationArtifact(context.Context, string) (storage.GenerationArtifactRow, bool, error)
	ListAgentRevisions(context.Context, string) ([]storage.AgentRevisionRow, error)
}

type persistedDependencyRepository interface {
	dependencyRepository
	GetOperationDependencyArtifact(context.Context, string) (storage.GenerationArtifactRow, bool, error)
	ListOperationRevisions(context.Context, string) ([]storage.AgentRevisionRow, error)
}

type DependencyNodeClaim struct {
	Node   dependency.Node
	Result ClaimResult
}

type DependencyFrontierClaimResult struct {
	Plan       dependency.Plan
	Evaluation dependency.Evaluation
	Claims     []DependencyNodeClaim
}

func (c *Coordinator) LoadDependencyPlan(ctx context.Context, operationID string) (dependency.Plan, error) {
	repository, ok := c.repository.(persistedDependencyRepository)
	if !ok {
		return dependency.Plan{}, fmt.Errorf("dependency repository is unavailable")
	}
	operationID = strings.TrimSpace(operationID)
	artifact, found, err := repository.GetOperationDependencyArtifact(ctx, operationID)
	if err != nil {
		return dependency.Plan{}, err
	}
	if !found {
		return dependency.Plan{}, fmt.Errorf("%w: operation %q has no dependency plan", dependency.ErrMissingDependency, operationID)
	}
	plan, err := dependency.ParsePlan(artifact.Payload)
	if err != nil {
		return dependency.Plan{}, err
	}
	if plan.OperationID != operationID {
		return dependency.Plan{}, fmt.Errorf("%w: dependency plan belongs to operation %q", dependency.ErrInvalidPlan, plan.OperationID)
	}
	revisions, err := repository.ListOperationRevisions(ctx, operationID)
	if err != nil {
		return dependency.Plan{}, err
	}
	if len(revisions) != len(plan.Nodes) {
		return dependency.Plan{}, fmt.Errorf("%w: dependency plan node count does not match operation revisions", dependency.ErrInvalidPlan)
	}
	for i := range plan.Nodes {
		if plan.Nodes[i].AgentID != revisions[i].AgentID || plan.Nodes[i].Revision != revisions[i].Revision {
			return dependency.Plan{}, fmt.Errorf("%w: dependency plan nodes do not match operation revisions", dependency.ErrInvalidPlan)
		}
	}
	return plan, nil
}

func (c *Coordinator) EvaluateDependencyOperation(ctx context.Context, operationID string) (dependency.Evaluation, error) {
	plan, err := c.LoadDependencyPlan(ctx, operationID)
	if err != nil {
		return dependency.Evaluation{}, err
	}
	return c.EvaluateDependencyPlan(ctx, plan)
}

func (c *Coordinator) ClaimDependencyFrontier(ctx context.Context, operationID string) (DependencyFrontierClaimResult, error) {
	plan, err := c.LoadDependencyPlan(ctx, operationID)
	if err != nil {
		return DependencyFrontierClaimResult{}, err
	}
	evaluation, err := c.EvaluateDependencyPlan(ctx, plan)
	result := DependencyFrontierClaimResult{Plan: plan, Evaluation: evaluation}
	if err != nil {
		return result, err
	}
	result.Claims = make([]DependencyNodeClaim, 0, len(evaluation.Frontier))
	for _, node := range evaluation.Frontier {
		claim, claimErr := c.claimDependencyNode(ctx, plan.OperationID, node)
		result.Claims = append(result.Claims, DependencyNodeClaim{Node: node, Result: claim})
		if claimErr != nil {
			return result, claimErr
		}
	}
	return result, nil
}

func (c *Coordinator) claimDependencyNode(ctx context.Context, operationID string, node dependency.Node) (ClaimResult, error) {
	for attempt := 0; attempt < 2; attempt++ {
		leaseID, err := c.newID("lease")
		if err != nil {
			return ClaimResult{}, fmt.Errorf("generate lease id: %w", err)
		}
		result, err := c.repository.ClaimLatestAgentRevision(ctx, storage.CoordinatorClaimRequest{
			AgentID: node.AgentID, LeaseID: leaseID,
			ExpectedOperationID: operationID, ExpectedRevision: node.Revision,
			Now: c.now(), DefaultApplyTimeoutSeconds: durationSeconds(c.applyTimeout),
			DefaultDrainTimeoutSeconds: durationSeconds(c.drainTimeout),
		})
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, storage.ErrCoordinatorReconcileNeeded) {
			return ClaimResult{}, err
		}
		if _, reconcileErr := c.Reconcile(ctx, node.AgentID); reconcileErr != nil {
			return ClaimResult{}, reconcileErr
		}
	}
	return ClaimResult{}, fmt.Errorf("%w: agent %q remained unreconciled", ErrStateConflict, node.AgentID)
}

func (c *Coordinator) RebuildDependencyPlan(ctx context.Context, operationID string, action dependency.Action, nodes []dependency.Node) (dependency.Plan, error) {
	repository, ok := c.repository.(dependencyRepository)
	if !ok {
		return dependency.Plan{}, fmt.Errorf("dependency repository is unavailable")
	}
	skeleton, err := dependency.NewPlan(strings.TrimSpace(operationID), action, nodes, nil)
	if err != nil {
		return dependency.Plan{}, err
	}
	operationID = skeleton.OperationID
	revisions := make([]dependency.SnapshotRevision, 0, len(skeleton.Nodes))
	for _, node := range skeleton.Nodes {
		row, found, err := repository.GetCoordinatorRevision(ctx, node.AgentID, node.Revision)
		if err != nil {
			return dependency.Plan{}, err
		}
		if !found {
			return dependency.Plan{}, fmt.Errorf("%w: revision %s/%d", dependency.ErrMissingDependency, node.AgentID, node.Revision)
		}
		if row.OperationID != operationID {
			return dependency.Plan{}, fmt.Errorf("%w: revision %s/%d belongs to operation %q", dependency.ErrInvalidPlan, node.AgentID, node.Revision, row.OperationID)
		}
		snapshotRow := row
		if action == dependency.ActionDelete {
			rows, err := repository.ListAgentRevisions(ctx, node.AgentID)
			if err != nil {
				return dependency.Plan{}, err
			}
			snapshotRow, found = previousDependencyRevision(rows, node.Revision)
			if !found {
				return dependency.Plan{}, fmt.Errorf("%w: prior revision for %s/%d", dependency.ErrMissingDependency, node.AgentID, node.Revision)
			}
		}
		artifact, found, err := repository.GetGenerationArtifact(ctx, snapshotRow.SnapshotArtifactID)
		if err != nil {
			return dependency.Plan{}, err
		}
		if !found {
			return dependency.Plan{}, fmt.Errorf("%w: snapshot artifact %q", dependency.ErrMissingDependency, snapshotRow.SnapshotArtifactID)
		}
		digest := sha256.Sum256(artifact.Payload)
		digestText := hex.EncodeToString(digest[:])
		if !strings.EqualFold(digestText, artifact.SHA256) || !strings.EqualFold(digestText, snapshotRow.SnapshotDigest) {
			return dependency.Plan{}, fmt.Errorf("%w: revision %s/%d snapshot digest is inconsistent", dependency.ErrInvalidPlan, node.AgentID, snapshotRow.Revision)
		}
		var snapshot storage.Snapshot
		if err := json.Unmarshal(artifact.Payload, &snapshot); err != nil {
			return dependency.Plan{}, fmt.Errorf("%w: decode revision %s/%d snapshot: %v", dependency.ErrInvalidPlan, node.AgentID, snapshotRow.Revision, err)
		}
		if snapshot.Revision != snapshotRow.Revision {
			return dependency.Plan{}, fmt.Errorf("%w: revision %s/%d snapshot identity is inconsistent", dependency.ErrInvalidPlan, node.AgentID, snapshotRow.Revision)
		}
		revisions = append(revisions, dependency.SnapshotRevision{
			AgentID: node.AgentID, Revision: node.Revision, Snapshot: snapshot,
		})
	}
	return dependency.BuildPlan(operationID, action, revisions)
}

func previousDependencyRevision(rows []storage.AgentRevisionRow, targetRevision int64) (storage.AgentRevisionRow, bool) {
	var previous storage.AgentRevisionRow
	found := false
	for _, row := range rows {
		if row.Revision >= 0 && row.Revision < targetRevision && (!found || row.Revision > previous.Revision) {
			previous = row
			found = true
		}
	}
	return previous, found
}

func (c *Coordinator) EvaluateDependencyPlan(ctx context.Context, plan dependency.Plan) (dependency.Evaluation, error) {
	repository, ok := c.repository.(dependencyRepository)
	if !ok {
		return dependency.Evaluation{}, fmt.Errorf("dependency repository is unavailable")
	}
	if _, err := plan.Marshal(); err != nil {
		return dependency.Evaluation{}, err
	}
	states := make(map[string]dependency.State, len(plan.Nodes))
	for _, node := range plan.Nodes {
		row, found, err := repository.GetCoordinatorRevision(ctx, node.AgentID, node.Revision)
		if err != nil {
			return dependency.Evaluation{}, err
		}
		if !found || row.OperationID != plan.OperationID {
			return dependency.Evaluation{}, fmt.Errorf("%w: revision %s/%d", dependency.ErrMissingDependency, node.AgentID, node.Revision)
		}
		pointer, found, err := repository.GetAgentRevisionPointer(ctx, node.AgentID)
		if err != nil {
			return dependency.Evaluation{}, err
		}
		if !found {
			return dependency.Evaluation{}, fmt.Errorf("%w: pointer for agent %q", dependency.ErrMissingDependency, node.AgentID)
		}
		if pointer.DesiredRevision < node.Revision {
			return dependency.Evaluation{}, fmt.Errorf("%w: pointer for agent %q precedes revision %d", dependency.ErrInvalidPlan, node.AgentID, node.Revision)
		}
		switch row.State {
		case storage.AgentRevisionStatePending:
			if pointer.DesiredRevision > node.Revision {
				states[node.AgentID] = dependency.StateSuperseded
				break
			}
			if pointer.DesiredRevision == node.Revision && pointer.AppliedRevision >= node.Revision {
				states[node.AgentID] = dependency.StateSucceeded
				break
			}
			states[node.AgentID] = dependency.StateWaiting
		case storage.AgentRevisionStateApplying:
			if pointer.DesiredRevision > node.Revision {
				states[node.AgentID] = dependency.StateSuperseded
				break
			}
			if pointer.DesiredRevision == node.Revision && pointer.AppliedRevision >= node.Revision {
				states[node.AgentID] = dependency.StateSucceeded
				break
			}
			states[node.AgentID] = dependency.StateRunning
		case storage.AgentRevisionStateApplied:
			states[node.AgentID] = dependency.StateSucceeded
		case storage.AgentRevisionStateFailed:
			states[node.AgentID] = dependency.StateFailed
		case storage.AgentRevisionStateSuperseded:
			states[node.AgentID] = dependency.StateSuperseded
		default:
			return dependency.Evaluation{}, fmt.Errorf("%w: revision %s/%d has unsupported state %q", dependency.ErrInvalidPlan, node.AgentID, node.Revision, row.State)
		}
	}
	return plan.Evaluate(states), nil
}
