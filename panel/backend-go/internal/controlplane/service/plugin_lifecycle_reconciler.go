package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type PluginLifecycleReconcileStore interface {
	RecordPluginAgentRuntimeReport(context.Context, storage.PluginGenerationReport) (storage.PluginAgentRuntimeStatusRow, bool, error)
	ListPluginAgentRuntimeStatuses(context.Context, string) ([]storage.PluginAgentRuntimeStatusRow, error)
	GetPluginOperation(context.Context, string) (storage.PluginOperationRow, bool, error)
}

type PluginLifecycleReconciler struct {
	store   PluginLifecycleReconcileStore
	plugins PluginLifecycleCompletion
	runtime PluginControlPlaneRuntime
}

type PluginControlPlaneRuntime interface {
	ActivateBatch(context.Context, []pluginhost.Candidate) ([]*pluginhost.Instance, error)
	ActiveGeneration(string) (string, bool)
	Stop(context.Context, string) error
}

type PluginLifecycleCompletion interface {
	CompleteLifecycleApply(context.Context, PluginApplyResult) (storage.InstalledPluginRow, error)
	CompleteConfigure(context.Context, PluginApplyResult) (storage.PluginInstanceRow, error)
	CompleteUpgrade(context.Context, PluginApplyResult) (storage.InstalledPluginRow, error)
	CompleteRollback(context.Context, PluginApplyResult) (storage.InstalledPluginRow, error)
	CompleteTrustedRevisionOperation(context.Context, storage.PluginOperationRow, bool, any) error
}

type PluginLifecycleReconcileResult struct {
	OperationID string                                `json:"operation_id"`
	Pending     bool                                  `json:"pending"`
	Completed   bool                                  `json:"completed"`
	Applied     bool                                  `json:"applied"`
	Replayed    bool                                  `json:"replayed"`
	Statuses    []storage.PluginAgentRuntimeStatusRow `json:"statuses"`
}

func NewPluginLifecycleReconciler(store PluginLifecycleReconcileStore, plugins PluginLifecycleCompletion) (*PluginLifecycleReconciler, error) {
	if store == nil || plugins == nil {
		return nil, errors.New("plugin lifecycle reconcile store and service are required")
	}
	return &PluginLifecycleReconciler{store: store, plugins: plugins}, nil
}

func (r *PluginLifecycleReconciler) SetControlPlaneRuntime(runtime PluginControlPlaneRuntime) {
	if r != nil {
		r.runtime = runtime
	}
}

func (r *PluginLifecycleReconciler) RecoverSupersededOperations(ctx context.Context) error {
	if r == nil || r.plugins == nil {
		return nil
	}
	pending, ok := r.plugins.(interface {
		RecoverPendingPluginOperations(context.Context) error
	})
	if ok {
		return pending.RecoverPendingPluginOperations(ctx)
	}
	recoverer, ok := r.plugins.(interface {
		RecoverSupersededConfigures(context.Context) error
	})
	if !ok {
		return nil
	}
	return recoverer.RecoverSupersededConfigures(ctx)
}

func (r *PluginLifecycleReconciler) completeTrustedRevisionOperation(ctx context.Context, operation storage.PluginOperationRow, applied bool, agentResults any) error {
	if applied {
		if planner, ok := r.plugins.(interface {
			controlPlaneRuntimePlan(context.Context, storage.PluginOperationRow) (controlPlanePluginRuntimePlan, error)
		}); ok {
			plan, err := planner.controlPlaneRuntimePlan(ctx, operation)
			if err != nil {
				return err
			}
			if plan.Controlled {
				if r.runtime == nil {
					return errors.New("control-plane plugin runtime is unavailable")
				}
				for _, instanceID := range plan.StopInstanceIDs {
					if err := r.runtime.Stop(ctx, instanceID); err != nil {
						return err
					}
				}
				pending := make([]pluginhost.Candidate, 0, len(plan.Candidates))
				for _, candidate := range plan.Candidates {
					if active, ok := r.runtime.ActiveGeneration(candidate.InstanceID); ok && active == candidate.Identity.Generation {
						continue
					}
					pending = append(pending, candidate)
				}
				if len(pending) > 0 {
					if _, err := r.runtime.ActivateBatch(ctx, pending); err != nil {
						failureResults := controlPlaneRuntimeFailureResults(agentResults, err)
						if completeErr := r.plugins.CompleteTrustedRevisionOperation(ctx, operation, false, failureResults); completeErr != nil {
							return errors.Join(err, completeErr)
						}
						return nil
					}
				}
			}
		}
	}
	return r.plugins.CompleteTrustedRevisionOperation(ctx, operation, applied, agentResults)
}

func controlPlaneRuntimeFailureResults(agentResults any, cause error) map[string]any {
	results := make(map[string]any)
	if existing, ok := agentResults.(map[string]any); ok {
		for key, value := range existing {
			results[key] = value
		}
	}
	message := "control-plane plugin activation failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = cause.Error()
	}
	results["control-plane-runtime"] = map[string]any{"state": "failed", "safe_detail": message}
	return results
}

// Reconcile records a report from the already-authenticated Agent control
// channel. Only the exact staged operation/revision/generation/digests can
// advance the durable row.
func (r *PluginLifecycleReconciler) Reconcile(ctx context.Context, report storage.PluginGenerationReport, actorID string) (PluginLifecycleReconcileResult, error) {
	if ctx == nil || strings.TrimSpace(actorID) == "" {
		return PluginLifecycleReconcileResult{}, errors.New("trusted plugin lifecycle report context and actor are required")
	}
	_, replayed, err := r.store.RecordPluginAgentRuntimeReport(ctx, report)
	if err != nil {
		return PluginLifecycleReconcileResult{}, err
	}
	operation, found, err := r.store.GetPluginOperation(ctx, report.OperationID)
	if err != nil {
		return PluginLifecycleReconcileResult{}, err
	}
	if !found || operation.PluginID != report.PluginID {
		return PluginLifecycleReconcileResult{}, storage.ErrPluginGenerationStale
	}
	statuses, err := r.store.ListPluginAgentRuntimeStatuses(ctx, report.OperationID)
	if err != nil {
		return PluginLifecycleReconcileResult{}, err
	}
	result := PluginLifecycleReconcileResult{OperationID: report.OperationID, Replayed: replayed, Statuses: statuses}
	if operation.Status == "succeeded" || operation.Status == "failed" {
		result.Completed, result.Applied = true, operation.Status == "succeeded"
		return result, nil
	}
	if (operation.Status != "applying" && operation.Status != "staged") || operation.TargetRevision <= 0 || len(statuses) == 0 {
		return PluginLifecycleReconcileResult{}, storage.ErrPluginGenerationStale
	}
	applied := true
	for _, status := range statuses {
		if status.OperationID != operation.ID || status.PluginID != operation.PluginID || status.Revision <= 0 {
			return PluginLifecycleReconcileResult{}, storage.ErrPluginGenerationConflict
		}
		switch status.State {
		case "active", "drained":
		case "degraded", "failed":
			applied = false
		default:
			result.Pending = true
			return result, nil
		}
	}
	agentResults, err := pluginRuntimeAgentResults(statuses)
	if err != nil {
		return PluginLifecycleReconcileResult{}, err
	}
	if strings.TrimSpace(operation.ActorID) == "" {
		return PluginLifecycleReconcileResult{}, storage.ErrPluginGenerationConflict
	}
	applyResult := PluginApplyResult{PluginID: operation.PluginID, InstanceID: report.InstanceID, OperationID: operation.ID, TargetRevision: operation.TargetRevision, TargetDigest: operation.TargetPackageDigest, ActorID: operation.ActorID, Applied: applied, AgentResults: agentResults}
	for _, status := range statuses {
		if status.InstanceID == report.InstanceID {
			applyResult.ConfigVersion = status.ConfigVersion
			break
		}
	}
	switch operation.Kind {
	case "enable", "disable":
		_, err = r.plugins.CompleteLifecycleApply(ctx, applyResult)
	case "configure":
		_, err = r.plugins.CompleteConfigure(ctx, applyResult)
	case "upgrade":
		_, err = r.plugins.CompleteUpgrade(ctx, applyResult)
	case "rollback":
		_, err = r.plugins.CompleteRollback(ctx, applyResult)
	default:
		err = fmt.Errorf("unsupported plugin lifecycle reconcile operation %q", operation.Kind)
	}
	if err != nil {
		return PluginLifecycleReconcileResult{}, err
	}
	result.Completed, result.Applied = true, applied
	return result, nil
}

func pluginRuntimeAgentResults(statuses []storage.PluginAgentRuntimeStatusRow) (map[string]any, error) {
	result := make(map[string]any, len(statuses))
	for _, status := range statuses {
		var details any
		if err := json.Unmarshal([]byte(status.DetailsJSON), &details); err != nil {
			return nil, err
		}
		result[status.AgentID+"/"+status.InstanceID] = map[string]any{
			"state": status.State, "generation_id": status.GenerationID,
			"package_digest": status.PackageDigest, "artifact_digest": status.ArtifactDigest,
			"error_code": status.ErrorCode, "details": details,
		}
	}
	return result, nil
}
