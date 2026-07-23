package coordinator

import (
	"context"
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
	LoadCoordinatorRuntimeSnapshot(context.Context, string, int64) (storage.CoordinatorRuntimeSnapshot, bool, error)
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

type DependencyAgentClaimResult struct {
	Plan       dependency.Plan
	Evaluation dependency.Evaluation
	Node       dependency.Node
	Claim      ClaimResult
	Eligible   bool
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
	rebuilt, normalized, err := c.rebuildDependencyPlan(ctx, operationID, plan.Action, plan.Nodes)
	if err != nil {
		return dependency.Plan{}, err
	}
	if normalized || dependencyDerivedEdgesDiffer(plan, rebuilt) {
		return rebuilt, nil
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

// ClaimDependencyAgent claims only the authenticated caller's frontier node.
// Remote pulls must not reserve leases for other independently ready agents.
func (c *Coordinator) ClaimDependencyAgent(ctx context.Context, operationID, agentID string) (DependencyAgentClaimResult, error) {
	plan, err := c.LoadDependencyPlan(ctx, operationID)
	if err != nil {
		return DependencyAgentClaimResult{}, err
	}
	evaluation, err := c.EvaluateDependencyPlan(ctx, plan)
	result := DependencyAgentClaimResult{Plan: plan, Evaluation: evaluation}
	if err != nil {
		return result, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return result, fmt.Errorf("agent id is required")
	}
	for _, node := range plan.Nodes {
		if node.AgentID == agentID {
			result.Node = node
			break
		}
	}
	if result.Node.AgentID == "" {
		return result, fmt.Errorf("%w: operation %q has no revision for agent %q", dependency.ErrMissingDependency, plan.OperationID, agentID)
	}
	for _, node := range evaluation.Frontier {
		if node.AgentID != agentID {
			continue
		}
		result.Eligible = true
		result.Claim, err = c.claimDependencyNode(ctx, plan.OperationID, node)
		return result, err
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
	plan, _, err := c.rebuildDependencyPlan(ctx, operationID, action, nodes)
	return plan, err
}

func (c *Coordinator) rebuildDependencyPlan(ctx context.Context, operationID string, action dependency.Action, nodes []dependency.Node) (dependency.Plan, bool, error) {
	repository, ok := c.repository.(dependencyRepository)
	if !ok {
		return dependency.Plan{}, false, fmt.Errorf("dependency repository is unavailable")
	}
	skeleton, err := dependency.NewPlan(strings.TrimSpace(operationID), action, nodes, nil)
	if err != nil {
		return dependency.Plan{}, false, err
	}
	operationID = skeleton.OperationID
	revisions := make([]dependency.SnapshotRevision, 0, len(skeleton.Nodes))
	normalized := false
	for _, node := range skeleton.Nodes {
		row, found, err := repository.GetCoordinatorRevision(ctx, node.AgentID, node.Revision)
		if err != nil {
			return dependency.Plan{}, false, err
		}
		if !found {
			return dependency.Plan{}, false, fmt.Errorf("%w: revision %s/%d", dependency.ErrMissingDependency, node.AgentID, node.Revision)
		}
		if row.OperationID != operationID {
			return dependency.Plan{}, false, fmt.Errorf("%w: revision %s/%d belongs to operation %q", dependency.ErrInvalidPlan, node.AgentID, node.Revision, row.OperationID)
		}
		snapshotRow := row
		if action == dependency.ActionDelete {
			rows, err := repository.ListAgentRevisions(ctx, node.AgentID)
			if err != nil {
				return dependency.Plan{}, false, err
			}
			snapshotRow, found = previousDependencyRevision(rows, node.Revision)
			if !found {
				return dependency.Plan{}, false, fmt.Errorf("%w: prior revision for %s/%d", dependency.ErrMissingDependency, node.AgentID, node.Revision)
			}
		}
		runtimeSnapshot, found, err := repository.LoadCoordinatorRuntimeSnapshot(ctx, node.AgentID, snapshotRow.Revision)
		if err != nil {
			return dependency.Plan{}, false, err
		}
		if !found {
			return dependency.Plan{}, false, fmt.Errorf("%w: revision %s/%d", dependency.ErrMissingDependency, node.AgentID, snapshotRow.Revision)
		}
		normalized = normalized || runtimeSnapshot.Normalized
		revisions = append(revisions, dependency.SnapshotRevision{
			AgentID: node.AgentID, Revision: node.Revision, Snapshot: runtimeSnapshot.Snapshot,
		})
	}
	plan, err := dependency.BuildPlan(operationID, action, revisions)
	return plan, normalized, err
}

func dependencyDerivedEdgesDiffer(persisted, rebuilt dependency.Plan) bool {
	hasDerivedEdge := false
	for _, edge := range persisted.Edges {
		if strings.HasPrefix(edge.Resource, "http_rule:") || strings.HasPrefix(edge.Resource, "l4_rule:") {
			hasDerivedEdge = true
			break
		}
	}
	if !hasDerivedEdge || len(persisted.Edges) != len(rebuilt.Edges) {
		return hasDerivedEdge
	}
	for i := range persisted.Edges {
		if persisted.Edges[i] != rebuilt.Edges[i] {
			return true
		}
	}
	return false
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
		switch row.State {
		case storage.AgentRevisionStateApplied:
			states[node.AgentID] = dependency.StateSucceeded
			continue
		case storage.AgentRevisionStateFailed:
			states[node.AgentID] = dependency.StateFailed
			continue
		case storage.AgentRevisionStateSuperseded:
			states[node.AgentID] = dependency.StateSuperseded
			continue
		case storage.AgentRevisionStatePending, storage.AgentRevisionStateApplying:
			// Active revisions still require their live pointer for fencing.
		default:
			return dependency.Evaluation{}, fmt.Errorf("%w: revision %s/%d has unsupported state %q", dependency.ErrInvalidPlan, node.AgentID, node.Revision, row.State)
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
		}
	}
	return plan.Evaluate(states), nil
}
