package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var (
	ErrPluginNotInstalled           = errors.New("plugin is not installed")
	ErrPluginPermissionConfirmation = errors.New("plugin permissions require exact administrator confirmation")
	ErrPluginRiskConfirmation       = errors.New("unofficial plugin source risk requires administrator confirmation")
	ErrPluginUninstallBlocked       = errors.New("plugin runtime must be disabled and drained before uninstall")
	ErrPluginResourceAuthorization  = errors.New("plugin target resource authorization denied")
	ErrPluginReadProjection         = errors.New("plugin read projection is invalid")
	ErrPluginConflict               = storage.ErrPluginConflict
)

type PluginPackageCandidate struct {
	Package            plugins.ValidatedPackage
	CachePath          string
	sourceID           string
	sourceKind         string
	sourceRiskLabel    string
	requireAcquisition bool
}

type PluginInstallRequest struct {
	Package              PluginPackageCandidate
	ActorID              string
	ConfirmedPermissions []string
	RiskAccepted         bool
}

type PluginConfigureRequest struct {
	PluginID        string
	InstanceID      string
	ResourceGroupID string
	Targets         any
	Config          json.RawMessage
	ActorID         string
}

type PluginUpgradeRequest struct {
	PluginID             string
	Package              PluginPackageCandidate
	ActorID              string
	ConfirmedPermissions []string
	RiskAccepted         bool
}

type PluginRollbackRequest struct {
	PluginID             string
	ActorID              string
	ConfirmedPermissions []string
}

type PluginUninstallRequest struct {
	PluginID string
	ActorID  string
	Drained  bool
}

type PluginPermissionDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

type PluginPackageDetail struct {
	Digest         string               `json:"digest"`
	Version        string               `json:"version"`
	Manifest       plugins.Manifest     `json:"manifest"`
	ConfigSchema   map[string]any       `json:"config_schema"`
	Permissions    []string             `json:"permissions"`
	PermissionDiff PluginPermissionDiff `json:"permission_diff"`
}

type PluginAgentStatus struct {
	InstanceID        string          `json:"instance_id"`
	AgentID           string          `json:"agent_id"`
	TargetScope       string          `json:"target_scope"`
	Available         bool            `json:"available"`
	CurrentState      string          `json:"current_state"`
	StatusSummary     json.RawMessage `json:"status_summary"`
	OperationID       string          `json:"operation_id,omitempty"`
	OperationKind     string          `json:"operation_kind,omitempty"`
	OperationStatus   string          `json:"operation_status,omitempty"`
	TargetRevision    int64           `json:"target_revision,omitempty"`
	DesiredRevision   int             `json:"desired_revision,omitempty"`
	CurrentRevision   int             `json:"current_revision,omitempty"`
	LastApplyRevision int             `json:"last_apply_revision,omitempty"`
	LastApplyStatus   string          `json:"last_apply_status,omitempty"`
	LastApplyMessage  string          `json:"last_apply_message,omitempty"`
}

type PluginSummary struct {
	PluginID                string    `json:"plugin_id"`
	ActivePackageDigest     string    `json:"active_package_digest"`
	ActiveSourceID          string    `json:"active_source_id,omitempty"`
	ActiveSourceKind        string    `json:"active_source_kind,omitempty"`
	ActiveSourceRiskLabel   string    `json:"active_source_risk_label,omitempty"`
	StagedPackageDigest     string    `json:"staged_package_digest,omitempty"`
	StagedSourceID          string    `json:"staged_source_id,omitempty"`
	StagedSourceKind        string    `json:"staged_source_kind,omitempty"`
	StagedSourceRiskLabel   string    `json:"staged_source_risk_label,omitempty"`
	RollbackPackageDigest   string    `json:"rollback_package_digest,omitempty"`
	RollbackSourceID        string    `json:"rollback_source_id,omitempty"`
	RollbackSourceKind      string    `json:"rollback_source_kind,omitempty"`
	RollbackSourceRiskLabel string    `json:"rollback_source_risk_label,omitempty"`
	DesiredLifecycle        string    `json:"desired_lifecycle"`
	CurrentLifecycle        string    `json:"current_lifecycle"`
	LastOperationID         string    `json:"last_operation_id"`
	StateVersion            uint64    `json:"state_version"`
	PendingOperationID      string    `json:"pending_operation_id,omitempty"`
	PendingKind             string    `json:"pending_kind,omitempty"`
	PendingTargetDigest     string    `json:"pending_target_digest,omitempty"`
	PendingRevision         int64     `json:"pending_revision,omitempty"`
	InstalledAt             time.Time `json:"installed_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type PluginInstanceDetail struct {
	ID                     string          `json:"id"`
	PluginID               string          `json:"plugin_id"`
	ResourceGroupID        string          `json:"resource_group_id"`
	Targets                []string        `json:"targets"`
	Config                 json.RawMessage `json:"config"`
	ConfigVersion          uint64          `json:"config_version"`
	PendingConfig          json.RawMessage `json:"pending_config,omitempty"`
	PendingVersion         uint64          `json:"pending_version,omitempty"`
	PendingOperationID     string          `json:"pending_operation_id,omitempty"`
	PendingResourceGroupID string          `json:"pending_resource_group_id,omitempty"`
	PendingTargets         []string        `json:"pending_targets,omitempty"`
	DesiredEnabled         bool            `json:"desired_enabled"`
	CurrentState           string          `json:"current_state"`
	StatusSummary          json.RawMessage `json:"status_summary"`
	StateVersion           uint64          `json:"state_version"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

type PluginGrantDetail struct {
	PackageDigest    string    `json:"package_digest"`
	Permission       string    `json:"permission"`
	ResourceSelector string    `json:"resource_selector,omitempty"`
	GrantedBy        string    `json:"granted_by"`
	GrantedAt        time.Time `json:"granted_at"`
}

type PluginOperationDetail struct {
	ID                  string          `json:"id"`
	PluginID            string          `json:"plugin_id"`
	Kind                string          `json:"kind"`
	Status              string          `json:"status"`
	TargetPackageDigest string          `json:"target_package_digest,omitempty"`
	TargetRevision      int64           `json:"target_revision,omitempty"`
	AgentResults        json.RawMessage `json:"agent_results"`
	ErrorClass          string          `json:"error_class,omitempty"`
	Error               string          `json:"error,omitempty"`
	ActorID             string          `json:"actor_id"`
	SessionID           string          `json:"session_id,omitempty"`
	CorrelationID       string          `json:"correlation_id,omitempty"`
	SourceID            string          `json:"source_id,omitempty"`
	SourceKind          string          `json:"source_kind,omitempty"`
	SourceRiskLabel     string          `json:"source_risk_label,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	CompletedAt         *time.Time      `json:"completed_at,omitempty"`
}

type PluginDetail struct {
	Plugin        PluginSummary          `json:"plugin"`
	Package       PluginPackageDetail    `json:"package"`
	Instances     []PluginInstanceDetail `json:"instances"`
	Grants        []PluginGrantDetail    `json:"grants"`
	AgentStatuses []PluginAgentStatus    `json:"agent_statuses"`
}

// PluginApplyResult is supplied only by the trusted revision/Agent reporting
// path. Every identity field must match the pending durable operation.
type PluginApplyResult struct {
	PluginID       string
	InstanceID     string
	OperationID    string
	TargetRevision int64
	TargetDigest   string
	ConfigVersion  uint64
	ActorID        string
	Applied        bool
	AgentResults   any
}

type pluginLifecycleStore interface {
	InstallPlugin(context.Context, storage.PluginInstallTransaction) error
	ApplyPluginMutation(context.Context, storage.PluginMutation) error
	RecordPluginOperation(context.Context, storage.PluginOperationRow, storage.AuditEventRow) error
	GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error)
	ListInstalledPlugins(context.Context) ([]storage.InstalledPluginRow, error)
	GetPluginPackage(context.Context, string) (storage.PluginPackageRow, bool, error)
	ListPluginGrants(context.Context, string) ([]storage.PluginGrantRow, error)
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	ListPluginInstances(context.Context, string) ([]storage.PluginInstanceRow, error)
	ListPluginOperations(context.Context, string) ([]storage.PluginOperationRow, error)
	ListAgents(context.Context) ([]storage.AgentRow, error)
}

type PluginService struct {
	store     pluginLifecycleStore
	validator *plugins.Validator
	now       func() time.Time
}

func NewPluginService(store pluginLifecycleStore) *PluginService {
	return NewPluginServiceWithValidator(store, plugins.NewValidator(plugins.ValidatorOptions{HostVersion: "0.0.0-dev"}))

}

func NewPluginServiceWithValidator(store pluginLifecycleStore, validator *plugins.Validator) *PluginService {
	return &PluginService{store: store, validator: validator, now: func() time.Time { return time.Now().UTC() }}
}

func (s *PluginService) installedPlugin(ctx context.Context, pluginID string) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, ErrPluginNotInstalled
	}
	return installed, nil
}

func (s *PluginService) List(ctx context.Context) ([]PluginSummary, error) {
	rows, err := s.store.ListInstalledPlugins(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PluginSummary, 0, len(rows))
	for _, row := range rows {
		result = append(result, pluginSummary(row))
	}
	return result, nil
}

func (s *PluginService) Detail(ctx context.Context, pluginID string) (PluginDetail, error) {
	installed, err := s.installedPlugin(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	packageRow, ok, err := s.store.GetPluginPackage(ctx, installed.ActivePackageDigest)
	if err != nil {
		return PluginDetail{}, err
	}
	if !ok {
		return PluginDetail{}, errors.New("active plugin package is unavailable")
	}
	if err := s.validateStoredPackageIntegrity(packageRow); err != nil {
		return PluginDetail{}, err
	}
	instances, err := s.store.ListPluginInstances(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	grants, err := s.store.ListPluginGrants(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	packageDetail, err := pluginPackageDetail(packageRow, grants, installed.ActivePackageDigest)
	if err != nil {
		return PluginDetail{}, err
	}
	instanceDetails, err := pluginInstanceDetails(instances)
	if err != nil {
		return PluginDetail{}, err
	}
	grantDetails := pluginGrantDetails(grants)
	operations, err := s.store.ListPluginOperations(ctx, pluginID)
	if err != nil {
		return PluginDetail{}, err
	}
	agentStatuses, err := s.pluginAgentStatuses(ctx, installed, instances, operations)
	if err != nil {
		return PluginDetail{}, err
	}
	return PluginDetail{Plugin: pluginSummary(installed), Package: packageDetail, Instances: instanceDetails, Grants: grantDetails, AgentStatuses: agentStatuses}, nil
}

func (s *PluginService) PackageDetail(ctx context.Context, candidate PluginPackageCandidate, pluginID string) (PluginPackageDetail, error) {
	if err := s.validatePackageCandidate(candidate); err != nil {
		return PluginPackageDetail{}, err
	}
	if pluginID != "" && pluginID != candidate.Package.Manifest.ID {
		return PluginPackageDetail{}, fmt.Errorf("%w: package plugin identity does not match requested plugin", ErrInvalidArgument)
	}
	grants, activeDigest := []storage.PluginGrantRow(nil), ""
	if strings.TrimSpace(pluginID) != "" {
		installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
		if err != nil {
			return PluginPackageDetail{}, err
		}
		if ok {
			activeDigest = installed.ActivePackageDigest
			grants, err = s.store.ListPluginGrants(ctx, pluginID)
			if err != nil {
				return PluginPackageDetail{}, err
			}
		}
	}
	manifestJSON, err := json.Marshal(candidate.Package.Manifest)
	if err != nil {
		return PluginPackageDetail{}, err
	}
	schemaJSON, err := json.Marshal(candidate.Package.ConfigSchema)
	if err != nil {
		return PluginPackageDetail{}, err
	}
	return pluginPackageDetail(storage.PluginPackageRow{Digest: candidate.Package.Digest, Version: candidate.Package.Manifest.Version, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: string(schemaJSON)}, grants, activeDigest)
}

func (s *PluginService) Operations(ctx context.Context, pluginID string) ([]PluginOperationDetail, error) {
	rows, err := s.store.ListPluginOperations(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	result := make([]PluginOperationDetail, 0, len(rows))
	for _, row := range rows {
		agentResults, err := pluginReadJSONObject(row.AgentResultsJSON)
		if err != nil {
			return nil, err
		}
		result = append(result, PluginOperationDetail{ID: row.ID, PluginID: row.PluginID, Kind: row.Kind, Status: row.Status, TargetPackageDigest: row.TargetPackageDigest, TargetRevision: row.TargetRevision, AgentResults: agentResults, ErrorClass: row.ErrorClass, Error: row.Error, ActorID: row.ActorID, SessionID: row.SessionID, CorrelationID: row.CorrelationID, SourceID: row.SourceID, SourceKind: row.SourceKind, SourceRiskLabel: row.SourceRiskLabel, CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt})
	}
	return result, nil
}

func (s *PluginService) Install(ctx context.Context, request PluginInstallRequest) (storage.InstalledPluginRow, error) {
	manifest := request.Package.Package.Manifest
	operation := s.operation(ctx, manifest.ID, "install", request.Package.Package.Digest, request.ActorID)
	bindOperationSource(&operation, request.Package)
	if err := s.validatePackageCandidate(request.Package); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := confirmCandidateSource(request.Package, request.RiskAccepted); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := confirmPermissions(manifest.Permissions, request.ConfirmedPermissions, true); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	now := s.now()
	operation.Status = "succeeded"
	operation.CompletedAt = &now
	manifestJSON, _ := json.Marshal(manifest)
	schemaJSON, _ := json.Marshal(request.Package.Package.ConfigSchema)
	cleanupJSON, _ := json.Marshal(manifest.Cleanup)
	installed := storage.InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: strings.ToLower(request.Package.Package.Digest), ActiveSourceID: request.Package.sourceID, ActiveSourceKind: request.Package.sourceKind, ActiveSourceRiskLabel: request.Package.sourceRiskLabel, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: string(cleanupJSON), LastOperationID: operation.ID, StateVersion: 1, InstalledAt: now, UpdatedAt: now}
	packageRow := pluginPackageRow(request.Package, manifestJSON, schemaJSON, now)
	grants := grantRows(manifest.ID, installed.ActivePackageDigest, manifest.Permissions, operation.ActorID, now)
	transaction := storage.PluginInstallTransaction{Package: packageRow, Installed: installed, Grants: grants, Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "success", "", now)}
	transaction.RequireAcquisition, transaction.AcquisitionSourceID, transaction.AcquisitionDigest = request.Package.requireAcquisition, request.Package.sourceID, request.Package.Package.Digest
	if err := s.store.InstallPlugin(ctx, transaction); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	return installed, nil
}

func (s *PluginService) Enable(ctx context.Context, pluginID, actorID string) (storage.InstalledPluginRow, error) {
	return s.setLifecycle(ctx, pluginID, actorID, "enable", "enabled", "active")
}

func (s *PluginService) Disable(ctx context.Context, pluginID, actorID string) (storage.InstalledPluginRow, error) {
	return s.setLifecycle(ctx, pluginID, actorID, "disable", "disabled", "disabled")
}

func (s *PluginService) setLifecycle(ctx context.Context, pluginID, actorID, kind, desired, current string) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(ctx, pluginID, kind, installed.ActivePackageDigest, actorID)
	if !ok {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, ErrPluginNotInstalled)
	}
	if kind != "disable" {
		if err := s.revalidateInstalledPackage(ctx, installed.ActivePackageDigest); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
		}
	}
	if kind == "enable" {
		packageRow, exists, packageErr := s.store.GetPluginPackage(ctx, installed.ActivePackageDigest)
		if packageErr != nil {
			return storage.InstalledPluginRow{}, packageErr
		}
		if !exists {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("active plugin package is unavailable"))
		}
		var manifest plugins.Manifest
		if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
		}
		instances, err := s.store.ListPluginInstances(ctx, pluginID)
		if err != nil {
			return storage.InstalledPluginRow{}, err
		}
		for _, instance := range instances {
			if err := s.validateAgentTargets(ctx, manifest.Compatibility.Agent, json.RawMessage(instance.TargetJSON)); err != nil {
				return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
			}
		}
	}
	if installed.DesiredLifecycle == desired && (installed.CurrentLifecycle == current || installed.CurrentLifecycle == "applying") {
		return installed, nil
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	now := s.now()
	installed.DesiredLifecycle, installed.CurrentLifecycle, installed.UpdatedAt = desired, "applying", now
	installed.LastOperationID = operation.ID
	operation.TargetRevision = int64(installed.StateVersion + 1)
	setPendingOperation(&installed, operation)
	operation.Status = "applying"
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: pluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, "accepted", "", now)})
	return installed, err
}

// CompleteLifecycleApply records the Agent/revision result separately from the
// desired-state mutation. A failed apply never changes the active package.
func (s *PluginService) CompleteLifecycleApply(ctx context.Context, applyResult PluginApplyResult) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, ErrPluginNotInstalled
	}
	operation, err := s.pendingOperation(ctx, installed, applyResult, "enable", "disable")
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	encodedResults, err := json.Marshal(applyResult.AgentResults)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	now := s.now()
	operation.AgentResultsJSON, operation.CompletedAt = string(encodedResults), &now
	if applyResult.Applied {
		operation.Status = "succeeded"
		if installed.DesiredLifecycle == "enabled" {
			installed.CurrentLifecycle = "active"
		} else {
			installed.CurrentLifecycle = "disabled"
		}
	} else {
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply plugin lifecycle"
		installed.CurrentLifecycle = "degraded"
	}
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	auditResult := "success"
	if !applyResult.Applied {
		auditResult = "failure"
	}
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, applyResult.ActorID, auditResult, operation.ErrorClass, now)})
	return installed, err
}

func (s *PluginService) Configure(ctx context.Context, request PluginConfigureRequest) (storage.PluginInstanceRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	operation := s.operation(ctx, request.PluginID, "configure", installed.ActivePackageDigest, request.ActorID)
	if !ok {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	if err := s.revalidateInstalledPackage(ctx, installed.ActivePackageDigest); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	packageRow, ok, err := s.store.GetPluginPackage(ctx, installed.ActivePackageDigest)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if !ok {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, errors.New("active plugin package is unavailable"))
	}
	schema, err := plugins.DecodeConfigSchema([]byte(packageRow.ConfigSchemaJSON))
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := plugins.ValidateConfig(schema, request.Config); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	targetJSON, err := json.Marshal(request.Targets)
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: invalid plugin targets", ErrInvalidArgument))
	}
	if strings.TrimSpace(request.ResourceGroupID) == "" {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: plugin instance resource group is required", ErrInvalidArgument))
	}
	targetIDs, err := pluginTargetIDs(targetJSON)
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: %v", ErrInvalidArgument, err))
	}
	if err := s.validateAgentTargets(ctx, manifest.Compatibility.Agent, targetJSON); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	for _, targetID := range targetIDs {
		if err := authorizeReferencedResource(ctx, s.store, "agent", targetID); err != nil {
			return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: %v", ErrPluginResourceAuthorization, err))
		}
	}
	if strings.TrimSpace(request.InstanceID) == "" {
		request.InstanceID = lifecycleID("instance")
	}
	instance, exists, err := s.store.GetPluginInstance(ctx, request.InstanceID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if exists && instance.PluginID != request.PluginID {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("%w: plugin instance identity mismatch", ErrInvalidArgument))
	}
	now := s.now()
	version := instance.ConfigVersion + 1
	if instance.PendingVersion >= version {
		version = instance.PendingVersion + 1
	}
	if !exists {
		instance = storage.PluginInstanceRow{ID: request.InstanceID, PluginID: request.PluginID, ResourceGroupID: request.ResourceGroupID, TargetJSON: string(targetJSON), ConfigJSON: "{}", StatusSummaryJSON: "{}"}
	}
	instance.PendingConfigJSON = string(request.Config)
	instance.PendingResourceGroupID = request.ResourceGroupID
	instance.PendingTargetJSON = string(targetJSON)
	instance.PendingVersion = version
	instance.PendingOperationID = operation.ID
	instance.DesiredEnabled = installed.DesiredLifecycle == "enabled"
	instance.CurrentState = "applying"
	instance.UpdatedAt = now
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	operation.TargetRevision = int64(installed.StateVersion + 1)
	setPendingOperation(&installed, operation)
	operation.Status = "applying"
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: request.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, ReplaceInstance: &instance, ValidateInstanceScope: true, Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "accepted", "", now)})
	return instance, err
}

func (s *PluginService) CompleteConfigure(ctx context.Context, applyResult PluginApplyResult) (storage.PluginInstanceRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, applyResult.PluginID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if !ok {
		return storage.PluginInstanceRow{}, ErrPluginNotInstalled
	}
	operation, err := s.pendingOperation(ctx, installed, applyResult, "configure")
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	instance, exists, err := s.store.GetPluginInstance(ctx, applyResult.InstanceID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if !exists || instance.PluginID != applyResult.PluginID || instance.PendingVersion == 0 || instance.PendingOperationID != operation.ID || instance.PendingVersion != applyResult.ConfigVersion {
		return storage.PluginInstanceRow{}, errors.New("pending plugin configuration identity is stale or unavailable")
	}
	encodedResults, err := json.Marshal(applyResult.AgentResults)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	now := s.now()
	operation.AgentResultsJSON, operation.CompletedAt = string(encodedResults), &now
	if applyResult.Applied {
		instance.ConfigJSON, instance.ConfigVersion = instance.PendingConfigJSON, instance.PendingVersion
		instance.ResourceGroupID, instance.TargetJSON = instance.PendingResourceGroupID, instance.PendingTargetJSON
		clearPendingInstance(&instance)
		if installed.CurrentLifecycle == "active" {
			instance.CurrentState = "active"
		} else {
			instance.CurrentState = "disabled"
		}
		operation.Status = "succeeded"
	} else {
		clearPendingInstance(&instance)
		instance.CurrentState = "degraded"
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply plugin configuration"
	}
	instance.StatusSummaryJSON = string(encodedResults)
	instance.UpdatedAt = now
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	auditResult := "success"
	if !applyResult.Applied {
		auditResult = "failure"
	}
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: applyResult.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, ReplaceInstance: &instance, ValidateInstanceScope: applyResult.Applied, PromoteInstanceBinding: applyResult.Applied, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, applyResult.ActorID, auditResult, operation.ErrorClass, now)})
	return instance, err
}

func (s *PluginService) Upgrade(ctx context.Context, request PluginUpgradeRequest) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(ctx, request.PluginID, "upgrade", request.Package.Package.Digest, request.ActorID)
	bindOperationSource(&operation, request.Package)
	if !ok {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	if err := s.validatePackageCandidate(request.Package); err != nil || request.Package.Package.Manifest.ID != request.PluginID {
		if err == nil {
			err = errors.New("upgrade package identity mismatch")
		}
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := confirmCandidateSource(request.Package, request.RiskAccepted); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	grants, err := s.store.ListPluginGrants(ctx, request.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if permissionsAdded(grants, installed.ActivePackageDigest, request.Package.Package.Manifest.Permissions) {
		if err := confirmPermissions(request.Package.Package.Manifest.Permissions, request.ConfirmedPermissions, true); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
		}
	}
	if strings.EqualFold(installed.ActivePackageDigest, request.Package.Package.Digest) {
		return installed, nil
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	activePackage, exists, err := s.store.GetPluginPackage(ctx, installed.ActivePackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, errors.New("active plugin package is unavailable"))
	}
	if err := s.validateStoredPackage(activePackage); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	var activeManifest plugins.Manifest
	if err := json.Unmarshal([]byte(activePackage.ManifestJSON), &activeManifest); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	instances, err := s.store.ListPluginInstances(ctx, request.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	for _, instance := range instances {
		if err := s.validateAgentTargets(ctx, request.Package.Package.Manifest.Compatibility.Agent, json.RawMessage(instance.TargetJSON)); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
		}
	}
	operation.TargetRevision = int64(installed.StateVersion + 1)
	for index := range instances {
		staged := json.RawMessage(instances[index].ConfigJSON)
		if len(request.Package.Package.Manifest.Migrations) > 0 {
			staged, err = plugins.ApplyMigrationChain(request.Package.CachePath, request.Package.Package.Manifest, activeManifest.Version, staged)
			if err != nil {
				return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
			}
		}
		if err := plugins.ValidateConfig(request.Package.Package.ConfigSchema, staged); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, fmt.Errorf("candidate config for instance %s is incompatible after migration: %w", instances[index].ID, err))
		}
		instances[index].PendingConfigJSON = string(staged)
		instances[index].PendingVersion = instances[index].ConfigVersion + 1
		instances[index].PendingOperationID = operation.ID
		instances[index].CurrentState = "applying"
	}
	now := s.now()
	manifestJSON, _ := json.Marshal(request.Package.Package.Manifest)
	schemaJSON, _ := json.Marshal(request.Package.Package.ConfigSchema)
	oldDigest := installed.ActivePackageDigest
	installed.StagedPackageDigest = strings.ToLower(request.Package.Package.Digest)
	installed.StagedSourceID, installed.StagedSourceKind, installed.StagedSourceRiskLabel = request.Package.sourceID, request.Package.sourceKind, request.Package.sourceRiskLabel
	installed.CurrentLifecycle = "upgrading"
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	setPendingOperation(&installed, operation)
	operation.Status = "staged"
	packageRow := pluginPackageRow(request.Package, manifestJSON, schemaJSON, now)
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: request.PluginID, ExpectedActive: oldDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, Package: &packageRow, ReplaceInstances: instances, Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "accepted", "", now), RequireAcquisition: request.Package.requireAcquisition, AcquisitionSourceID: request.Package.sourceID, AcquisitionDigest: request.Package.Package.Digest})
	return installed, err
}

func (s *PluginService) CompleteUpgrade(ctx context.Context, applyResult PluginApplyResult) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, ErrPluginNotInstalled
	}
	operation, err := s.pendingOperation(ctx, installed, applyResult, "upgrade")
	if err != nil || installed.StagedPackageDigest == "" || installed.CurrentLifecycle != "upgrading" {
		if err != nil {
			return storage.InstalledPluginRow{}, err
		}
		return storage.InstalledPluginRow{}, errors.New("staged upgrade is unavailable")
	}
	packageRow, exists, err := s.store.GetPluginPackage(ctx, installed.StagedPackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, errors.New("staged package cache record is unavailable")
	}
	if err := s.validateStoredPackage(packageRow); err != nil {
		return installed, s.failPendingPackagePromotion(ctx, installed, operation, err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	instances, err := s.store.ListPluginInstances(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	for _, instance := range instances {
		if instance.PendingOperationID != operation.ID || instance.PendingVersion == 0 {
			return storage.InstalledPluginRow{}, errors.New("staged instance migration identity is unavailable")
		}
	}
	encodedResults, err := json.Marshal(applyResult.AgentResults)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	now, oldDigest := s.now(), installed.ActivePackageDigest
	operation.AgentResultsJSON, operation.CompletedAt = string(encodedResults), &now
	var grants []storage.PluginGrantRow
	if applyResult.Applied {
		installed.ActivePackageDigest, installed.RollbackPackageDigest = installed.StagedPackageDigest, oldDigest
		installed.ActiveSourceID, installed.RollbackSourceID = installed.StagedSourceID, installed.ActiveSourceID
		installed.ActiveSourceKind, installed.RollbackSourceKind = installed.StagedSourceKind, installed.ActiveSourceKind
		installed.ActiveSourceRiskLabel, installed.RollbackSourceRiskLabel = installed.StagedSourceRiskLabel, installed.ActiveSourceRiskLabel
		clearStagedSource(&installed)
		cleanupJSON, _ := json.Marshal(manifest.Cleanup)
		installed.CleanupPolicyJSON = string(cleanupJSON)
		if installed.DesiredLifecycle == "enabled" {
			installed.CurrentLifecycle = "active"
		} else {
			installed.CurrentLifecycle = "disabled"
		}
		operation.Status = "succeeded"
		grants = grantRows(applyResult.PluginID, installed.ActivePackageDigest, manifest.Permissions, operation.ActorID, now)
		for index := range instances {
			instances[index].RollbackConfigJSON, instances[index].RollbackVersion = instances[index].ConfigJSON, instances[index].ConfigVersion
			instances[index].ConfigJSON, instances[index].ConfigVersion = instances[index].PendingConfigJSON, instances[index].PendingVersion
			instances[index].PendingConfigJSON, instances[index].PendingVersion, instances[index].PendingOperationID = "", 0, ""
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	} else {
		clearStagedSource(&installed)
		installed.CurrentLifecycle = lifecycleCurrentState(installed.DesiredLifecycle)
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply staged plugin package"
		for index := range instances {
			instances[index].PendingConfigJSON, instances[index].PendingVersion, instances[index].PendingOperationID = "", 0, ""
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	}
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	auditResult := "success"
	if !applyResult.Applied {
		auditResult = "failure"
	}
	mutation := storage.PluginMutation{PluginID: applyResult.PluginID, ExpectedActive: oldDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, ReplaceInstances: instances, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, applyResult.ActorID, auditResult, operation.ErrorClass, now)}
	if applyResult.Applied {
		mutation.ReplaceGrants = grants
	}
	err = s.store.ApplyPluginMutation(ctx, mutation)
	return installed, err
}

func (s *PluginService) Rollback(ctx context.Context, request PluginRollbackRequest) (storage.InstalledPluginRow, error) {
	pluginID, actorID := request.PluginID, request.ActorID
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(ctx, pluginID, "rollback", installed.RollbackPackageDigest, actorID)
	if !ok || installed.RollbackPackageDigest == "" {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("rollback package is unavailable"))
	}
	rollbackPackage, exists, err := s.store.GetPluginPackage(ctx, installed.RollbackPackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("rollback package cache record is unavailable"))
	}
	operation.SourceID = installed.RollbackSourceID
	operation.SourceKind = installed.RollbackSourceKind
	operation.SourceRiskLabel = installed.RollbackSourceRiskLabel
	if err := s.validateStoredPackage(rollbackPackage); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	rollbackSchema, err := plugins.DecodeConfigSchema([]byte(rollbackPackage.ConfigSchemaJSON))
	if err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	var rollbackManifest plugins.Manifest
	if err := json.Unmarshal([]byte(rollbackPackage.ManifestJSON), &rollbackManifest); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	grants, err := s.store.ListPluginGrants(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if permissionsAdded(grants, installed.ActivePackageDigest, rollbackManifest.Permissions) {
		if err := confirmPermissions(rollbackManifest.Permissions, request.ConfirmedPermissions, true); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
		}
	}
	instances, err := s.store.ListPluginInstances(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	for _, instance := range instances {
		if err := s.validateAgentTargets(ctx, rollbackManifest.Compatibility.Agent, json.RawMessage(instance.TargetJSON)); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
		}
	}
	operation.TargetRevision = int64(installed.StateVersion + 1)
	for index := range instances {
		if instances[index].RollbackVersion == 0 || plugins.ValidateConfig(rollbackSchema, json.RawMessage(instances[index].RollbackConfigJSON)) != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, fmt.Errorf("rollback config for instance %s is unavailable or invalid", instances[index].ID))
		}
		instances[index].PendingConfigJSON = instances[index].RollbackConfigJSON
		instances[index].PendingVersion = instances[index].RollbackVersion
		instances[index].PendingOperationID = operation.ID
		instances[index].CurrentState = "applying"
	}
	now, oldDigest := s.now(), installed.ActivePackageDigest
	installed.StagedPackageDigest = installed.RollbackPackageDigest
	installed.StagedSourceID, installed.StagedSourceKind, installed.StagedSourceRiskLabel = installed.RollbackSourceID, installed.RollbackSourceKind, installed.RollbackSourceRiskLabel
	installed.CurrentLifecycle = "rolling_back"
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	setPendingOperation(&installed, operation)
	operation.Status = "staged"
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: pluginID, ExpectedActive: oldDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, ReplaceInstances: instances, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, "accepted", "", now)})
	return installed, err
}

func (s *PluginService) CompleteRollback(ctx context.Context, applyResult PluginApplyResult) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, ErrPluginNotInstalled
	}
	operation, err := s.pendingOperation(ctx, installed, applyResult, "rollback")
	if err != nil || installed.StagedPackageDigest == "" || installed.CurrentLifecycle != "rolling_back" {
		if err != nil {
			return storage.InstalledPluginRow{}, err
		}
		return storage.InstalledPluginRow{}, errors.New("staged rollback is unavailable")
	}
	packageRow, exists, err := s.store.GetPluginPackage(ctx, installed.StagedPackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, errors.New("rollback package cache record is unavailable")
	}
	if err := s.validateStoredPackage(packageRow); err != nil {
		return installed, s.failPendingPackagePromotion(ctx, installed, operation, err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return storage.InstalledPluginRow{}, err
	}
	instances, err := s.store.ListPluginInstances(ctx, applyResult.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	for _, instance := range instances {
		if instance.PendingOperationID != operation.ID || instance.PendingVersion == 0 {
			return storage.InstalledPluginRow{}, errors.New("staged rollback config identity is unavailable")
		}
	}
	encodedResults, err := json.Marshal(applyResult.AgentResults)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	now, oldDigest := s.now(), installed.ActivePackageDigest
	operation.AgentResultsJSON, operation.CompletedAt = string(encodedResults), &now
	var grants []storage.PluginGrantRow
	if applyResult.Applied {
		installed.ActivePackageDigest, installed.RollbackPackageDigest = installed.StagedPackageDigest, oldDigest
		installed.ActiveSourceID, installed.RollbackSourceID = installed.StagedSourceID, installed.ActiveSourceID
		installed.ActiveSourceKind, installed.RollbackSourceKind = installed.StagedSourceKind, installed.ActiveSourceKind
		installed.ActiveSourceRiskLabel, installed.RollbackSourceRiskLabel = installed.StagedSourceRiskLabel, installed.ActiveSourceRiskLabel
		clearStagedSource(&installed)
		cleanupJSON, _ := json.Marshal(manifest.Cleanup)
		installed.CleanupPolicyJSON = string(cleanupJSON)
		if installed.DesiredLifecycle == "enabled" {
			installed.CurrentLifecycle = "active"
		} else {
			installed.CurrentLifecycle = "disabled"
		}
		operation.Status = "succeeded"
		grants = grantRows(applyResult.PluginID, installed.ActivePackageDigest, manifest.Permissions, operation.ActorID, now)
		for index := range instances {
			instances[index].RollbackConfigJSON, instances[index].RollbackVersion = instances[index].ConfigJSON, instances[index].ConfigVersion
			instances[index].ConfigJSON, instances[index].ConfigVersion = instances[index].PendingConfigJSON, instances[index].PendingVersion
			instances[index].PendingConfigJSON, instances[index].PendingVersion, instances[index].PendingOperationID = "", 0, ""
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	} else {
		clearStagedSource(&installed)
		installed.CurrentLifecycle = lifecycleCurrentState(installed.DesiredLifecycle)
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply rollback package"
		for index := range instances {
			instances[index].PendingConfigJSON, instances[index].PendingVersion, instances[index].PendingOperationID = "", 0, ""
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	}
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	auditResult := "success"
	if !applyResult.Applied {
		auditResult = "failure"
	}
	mutation := storage.PluginMutation{PluginID: applyResult.PluginID, ExpectedActive: oldDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, ReplaceInstances: instances, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, applyResult.ActorID, auditResult, operation.ErrorClass, now)}
	if applyResult.Applied {
		mutation.ReplaceGrants = grants
	}
	err = s.store.ApplyPluginMutation(ctx, mutation)
	return installed, err
}

func (s *PluginService) Uninstall(ctx context.Context, request PluginUninstallRequest) error {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return err
	}
	operation := s.operation(ctx, request.PluginID, "uninstall", installed.ActivePackageDigest, request.ActorID)
	if !ok {
		return s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	if installed.CurrentLifecycle != "disabled" || !request.Drained {
		return s.recordFailure(ctx, operation, request.ActorID, ErrPluginUninstallBlocked)
	}
	if err := ensureNoPendingOperation(installed); err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, err)
	}
	packageRow, exists, err := s.store.GetPluginPackage(ctx, installed.ActivePackageDigest)
	if err != nil {
		return err
	}
	if !exists {
		return s.recordFailure(ctx, operation, request.ActorID, errors.New("active plugin package is unavailable"))
	}
	if err := s.validateStoredPackageIntegrity(packageRow); err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, err)
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, err)
	}
	var projectedCleanup plugins.CleanupPolicy
	if err := json.Unmarshal([]byte(installed.CleanupPolicyJSON), &projectedCleanup); err != nil || projectedCleanup != manifest.Cleanup {
		return s.recordFailure(ctx, operation, request.ActorID, errors.New("persisted plugin cleanup policy differs from verified package"))
	}
	cleanup := manifest.Cleanup
	now := s.now()
	operation.Status, operation.CompletedAt = "succeeded", &now
	return s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: request.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, DeletePlugin: true, DeleteInstances: cleanup.Instances == "delete", DeleteGrants: cleanup.Grants == "delete", Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "success", "", now)})
}

func (s *PluginService) operation(ctx context.Context, pluginID, kind, digest, fallbackActorID string) storage.PluginOperationRow {
	id := lifecycleID("pluginop")
	actorID, sessionID, correlationID := strings.TrimSpace(fallbackActorID), "", id
	if actor, ok := storage.QuotaActorFromContext(ctx); ok {
		actorID, sessionID = actor.UserID, actor.SessionID
		if strings.TrimSpace(actor.CorrelationID) != "" {
			correlationID = actor.CorrelationID
		}
	}
	return storage.PluginOperationRow{ID: id, PluginID: pluginID, Kind: kind, Status: "running", TargetPackageDigest: strings.ToLower(digest), AgentResultsJSON: "{}", ActorID: actorID, SessionID: sessionID, CorrelationID: correlationID, CreatedAt: s.now()}
}

func ensureNoPendingOperation(installed storage.InstalledPluginRow) error {
	if installed.PendingOperationID != "" || installed.StagedPackageDigest != "" {
		return fmt.Errorf("%w: another plugin operation is already pending", ErrPluginConflict)
	}
	return nil
}

func setPendingOperation(installed *storage.InstalledPluginRow, operation storage.PluginOperationRow) {
	installed.PendingOperationID = operation.ID
	installed.PendingKind = operation.Kind
	installed.PendingTargetDigest = operation.TargetPackageDigest
	installed.PendingRevision = operation.TargetRevision
}

func clearPendingOperation(installed *storage.InstalledPluginRow) {
	installed.PendingOperationID = ""
	installed.PendingKind = ""
	installed.PendingTargetDigest = ""
	installed.PendingRevision = 0
}

func clearStagedSource(installed *storage.InstalledPluginRow) {
	installed.StagedPackageDigest = ""
	installed.StagedSourceID = ""
	installed.StagedSourceKind = ""
	installed.StagedSourceRiskLabel = ""
}

func lifecycleCurrentState(desired string) string {
	if desired == "enabled" {
		return "active"
	}
	return "disabled"
}

func (s *PluginService) pendingOperation(ctx context.Context, installed storage.InstalledPluginRow, result PluginApplyResult, kinds ...string) (storage.PluginOperationRow, error) {
	if result.OperationID == "" || result.OperationID != installed.PendingOperationID || result.TargetRevision != installed.PendingRevision || !strings.EqualFold(result.TargetDigest, installed.PendingTargetDigest) {
		return storage.PluginOperationRow{}, fmt.Errorf("%w: plugin apply result is stale, replayed, or out of order", ErrPluginConflict)
	}
	allowed := false
	for _, kind := range kinds {
		if installed.PendingKind == kind {
			allowed = true
			break
		}
	}
	if !allowed {
		return storage.PluginOperationRow{}, errors.New("plugin apply result kind does not match pending operation")
	}
	operations, err := s.store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil {
		return storage.PluginOperationRow{}, err
	}
	for _, operation := range operations {
		if operation.ID == result.OperationID {
			if operation.CompletedAt != nil || operation.Kind != installed.PendingKind || operation.TargetRevision != result.TargetRevision || !strings.EqualFold(operation.TargetPackageDigest, result.TargetDigest) {
				return storage.PluginOperationRow{}, errors.New("plugin apply result does not match durable pending operation")
			}
			return operation, nil
		}
	}
	return storage.PluginOperationRow{}, errors.New("durable pending plugin operation is unavailable")
}

func (s *PluginService) recordFailure(ctx context.Context, operation storage.PluginOperationRow, actorID string, cause error) error {
	now := s.now()
	operation.Status, operation.ErrorClass, operation.Error, operation.CompletedAt = "failed", pluginErrorClass(cause), cause.Error(), &now
	if err := s.store.RecordPluginOperation(ctx, operation, pluginLifecycleAudit(operation, actorID, "failure", operation.ErrorClass, now)); err != nil {
		return fmt.Errorf("%w (persist operation failure: %v)", cause, err)
	}
	return cause
}

func (s *PluginService) validatePackageCandidate(candidate PluginPackageCandidate) error {
	digest := strings.ToLower(strings.TrimSpace(candidate.Package.Digest))
	if candidate.Package.Manifest.ID == "" || candidate.Package.Manifest.Version == "" || !isHexDigest(digest) || candidate.CachePath == "" || len(candidate.CachePath) > plugins.MaxPackagePathBytes {
		return errors.New("validated digest-addressed package candidate is required")
	}
	if !strings.EqualFold(filepath.Base(filepath.Clean(candidate.CachePath)), digest) {
		return errors.New("verified cache path is not addressed by package digest")
	}
	computed, err := plugins.ComputePackageDigest(candidate.CachePath)
	if err != nil || !strings.EqualFold(computed, digest) {
		return errors.New("verified cache contents do not match package digest")
	}
	if s.validator == nil {
		return errors.New("plugin compatibility validator is unavailable")
	}
	revalidated, err := s.validator.ValidatePackage(candidate.CachePath, plugins.PackageExpectation{ID: candidate.Package.Manifest.ID, Version: candidate.Package.Manifest.Version, SHA256: digest})
	if err != nil {
		return fmt.Errorf("revalidate cached package: %w", err)
	}
	if !reflect.DeepEqual(revalidated.Manifest, candidate.Package.Manifest) || !reflect.DeepEqual(revalidated.ConfigSchema, candidate.Package.ConfigSchema) {
		return errors.New("package candidate projection differs from verified cache")
	}
	return nil
}

func (s *PluginService) revalidateInstalledPackage(ctx context.Context, digest string) error {
	row, ok, err := s.store.GetPluginPackage(ctx, digest)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("installed plugin package is unavailable")
	}
	return s.validateStoredPackage(row)
}

func (s *PluginService) validateStoredPackage(row storage.PluginPackageRow) error {
	if s.validator == nil {
		return errors.New("plugin compatibility validator is unavailable")
	}
	validated, err := s.validator.ValidatePackage(row.CachePath, plugins.PackageExpectation{ID: row.PluginID, Version: row.Version, SHA256: row.Digest})
	if err != nil {
		return fmt.Errorf("revalidate installed package: %w", err)
	}
	return validateStoredPackageProjection(row, validated)
}

func (s *PluginService) validateStoredPackageIntegrity(row storage.PluginPackageRow) error {
	if s.validator == nil {
		return errors.New("plugin compatibility validator is unavailable")
	}
	validated, err := s.validator.ValidatePackageIntegrity(row.CachePath, plugins.PackageExpectation{ID: row.PluginID, Version: row.Version, SHA256: row.Digest})
	if err != nil {
		return fmt.Errorf("revalidate installed package integrity: %w", err)
	}
	return validateStoredPackageProjection(row, validated)
}

func validateStoredPackageProjection(row storage.PluginPackageRow, validated plugins.ValidatedPackage) error {
	var projectedManifest plugins.Manifest
	if err := json.Unmarshal([]byte(row.ManifestJSON), &projectedManifest); err != nil || !reflect.DeepEqual(projectedManifest, validated.Manifest) {
		return errors.New("persisted plugin manifest differs from verified cache")
	}
	projectedSchema, err := plugins.DecodeConfigSchema([]byte(row.ConfigSchemaJSON))
	if err != nil || !reflect.DeepEqual(projectedSchema, validated.ConfigSchema) {
		return errors.New("persisted plugin config schema differs from verified cache")
	}
	return nil
}

func bindOperationSource(operation *storage.PluginOperationRow, candidate PluginPackageCandidate) {
	operation.SourceID = candidate.sourceID
	operation.SourceKind = candidate.sourceKind
	operation.SourceRiskLabel = candidate.sourceRiskLabel
}

func pluginPackageRow(candidate PluginPackageCandidate, manifestJSON, schemaJSON []byte, now time.Time) storage.PluginPackageRow {
	return storage.PluginPackageRow{Digest: strings.ToLower(candidate.Package.Digest), PluginID: candidate.Package.Manifest.ID, Version: candidate.Package.Manifest.Version, CachePath: candidate.CachePath, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: string(schemaJSON), VerifiedAt: now}
}

func confirmCandidateSource(candidate PluginPackageCandidate, accepted bool) error {
	if strings.TrimSpace(candidate.sourceID) == "" {
		return errors.New("verified package source provenance is required")
	}
	switch candidate.sourceKind {
	case marketplace.SourceKindOfficial:
		if candidate.sourceID != marketplace.OfficialSourceID || candidate.sourceRiskLabel != "" {
			return errors.New("official package source provenance is invalid")
		}
	case marketplace.SourceKindCustom:
		if candidate.sourceID == marketplace.OfficialSourceID || candidate.sourceRiskLabel != marketplace.UntrustedRiskLabel {
			return errors.New("custom package source provenance is invalid")
		}
	default:
		return errors.New("unknown marketplace source kind")
	}
	return confirmSourceRisk(candidate.sourceKind, accepted)
}

func clearPendingInstance(instance *storage.PluginInstanceRow) {
	instance.PendingConfigJSON = ""
	instance.PendingResourceGroupID = ""
	instance.PendingTargetJSON = ""
	instance.PendingVersion = 0
	instance.PendingOperationID = ""
}

func (s *PluginService) failPendingPackagePromotion(ctx context.Context, installed storage.InstalledPluginRow, operation storage.PluginOperationRow, cause error) error {
	instances, err := s.store.ListPluginInstances(ctx, installed.PluginID)
	if err != nil {
		return errors.Join(cause, err)
	}
	now := s.now()
	for index := range instances {
		if instances[index].PendingOperationID == operation.ID {
			clearPendingInstance(&instances[index])
			instances[index].CurrentState = lifecycleCurrentState(installed.DesiredLifecycle)
			instances[index].UpdatedAt = now
		}
	}
	clearStagedSource(&installed)
	installed.CurrentLifecycle = lifecycleCurrentState(installed.DesiredLifecycle)
	clearPendingOperation(&installed)
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	operation.Status, operation.ErrorClass, operation.Error, operation.CompletedAt = "failed", "package_integrity", cause.Error(), &now
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, ExpectedPendingOperationID: operation.ID, Installed: &installed, ReplaceInstances: instances, Operation: operation, CompleteOperation: true, Audit: pluginLifecycleAudit(operation, operation.ActorID, "failure", operation.ErrorClass, now)})
	if err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (s *PluginService) validateAgentTargets(ctx context.Context, constraint string, raw json.RawMessage) error {
	targetIDs, err := pluginTargetIDs(raw)
	if err != nil {
		return err
	}
	byID, err := s.authoritativePluginAgents(ctx)
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, targetID := range targetIDs {
		targetID = strings.TrimSpace(targetID)
		if targetID == "" {
			return errors.New("plugin target agent ID is required")
		}
		if _, duplicate := seen[targetID]; duplicate {
			continue
		}
		seen[targetID] = struct{}{}
		agent, ok := byID[targetID]
		if !ok {
			return fmt.Errorf("plugin target agent %s is unavailable", targetID)
		}
		if err := plugins.CheckAgentCompatibility(agent.Version, constraint); err != nil {
			return fmt.Errorf("plugin target agent %s: %w", targetID, err)
		}
		var capabilities []string
		if err := json.Unmarshal([]byte(agent.CapabilitiesJSON), &capabilities); err != nil {
			return fmt.Errorf("plugin target agent %s capabilities are unavailable", targetID)
		}
		supported := false
		for _, capability := range capabilities {
			if capability == "package_manifest_v1" {
				supported = true
				break
			}
		}
		if !supported {
			return fmt.Errorf("plugin target agent %s lacks package_manifest_v1 capability", targetID)
		}
	}
	return nil
}

func (s *PluginService) authoritativePluginAgents(ctx context.Context) (map[string]storage.AgentRow, error) {
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]storage.AgentRow, len(agents)+1)
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	provider, ok := s.store.(interface {
		LocalAgentBuild(context.Context) (string, string, bool, error)
	})
	if !ok {
		return byID, nil
	}
	localID, version, present, err := provider.LocalAgentBuild(ctx)
	if err != nil {
		return nil, err
	}
	localID = strings.TrimSpace(localID)
	if localID == "" {
		return byID, nil
	}
	if !present {
		delete(byID, localID)
		return byID, nil
	}
	local := byID[localID]
	local.ID = localID
	if buildVersion := strings.TrimSpace(version); buildVersion != "" {
		local.Version = buildVersion
	}
	local.CapabilitiesJSON = marshalStringArray(defaultLocalCapabilities)
	local.IsLocal = true
	local.Mode = "local"
	if stateProvider, ok := s.store.(interface {
		LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	}); ok {
		state, err := stateProvider.LoadLocalAgentState(ctx)
		if err != nil {
			return nil, err
		}
		local.DesiredVersion = state.DesiredVersion
		local.DesiredRevision = state.DesiredRevision
		local.CurrentRevision = state.CurrentRevision
		local.LastApplyRevision = state.LastApplyRevision
		local.LastApplyStatus = state.LastApplyStatus
		local.LastApplyMessage = state.LastApplyMessage
		if local.Version == "" {
			local.Version = strings.TrimSpace(state.Version)
		}
	}
	byID[localID] = local
	return byID, nil
}

func pluginTargetIDs(raw json.RawMessage) ([]string, error) {
	var targetIDs []string
	trimmed := strings.TrimSpace(string(raw))
	if trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(raw, &targetIDs); err != nil {
			return nil, errors.New("plugin targets must be an array of agent IDs")
		}
	}
	if len(targetIDs) == 0 {
		targetIDs = []string{"local"}
	}
	return targetIDs, nil
}

func confirmSourceRisk(kind string, accepted bool) error {
	switch kind {
	case marketplace.SourceKindOfficial:
		return nil
	case marketplace.SourceKindCustom:
		if !accepted {
			return ErrPluginRiskConfirmation
		}
		return nil
	default:
		return errors.New("unknown marketplace source kind")
	}
}

func isHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func confirmPermissions(permissions []plugins.Permission, confirmed []string, required bool) error {
	expected := normalizedPermissions(permissions)
	actual := append([]string(nil), confirmed...)
	for index := range actual {
		actual[index] = strings.TrimSpace(actual[index])
	}
	sort.Strings(actual)
	if !required && len(actual) == 0 {
		return nil
	}
	if len(expected) != len(actual) {
		return ErrPluginPermissionConfirmation
	}
	for index := range expected {
		if expected[index] != actual[index] {
			return ErrPluginPermissionConfirmation
		}
	}
	return nil
}

func normalizedPermissions(permissions []plugins.Permission) []string {
	result := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		value := strings.TrimSpace(permission.Name)
		if resource := strings.TrimSpace(permission.Resource); resource != "" {
			value += ":" + resource
		}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func pluginSummary(row storage.InstalledPluginRow) PluginSummary {
	return PluginSummary{
		PluginID: row.PluginID, ActivePackageDigest: row.ActivePackageDigest,
		ActiveSourceID: row.ActiveSourceID, ActiveSourceKind: row.ActiveSourceKind, ActiveSourceRiskLabel: row.ActiveSourceRiskLabel,
		StagedPackageDigest: row.StagedPackageDigest, StagedSourceID: row.StagedSourceID, StagedSourceKind: row.StagedSourceKind, StagedSourceRiskLabel: row.StagedSourceRiskLabel,
		RollbackPackageDigest: row.RollbackPackageDigest, RollbackSourceID: row.RollbackSourceID, RollbackSourceKind: row.RollbackSourceKind, RollbackSourceRiskLabel: row.RollbackSourceRiskLabel,
		DesiredLifecycle: row.DesiredLifecycle, CurrentLifecycle: row.CurrentLifecycle, LastOperationID: row.LastOperationID, StateVersion: row.StateVersion,
		PendingOperationID: row.PendingOperationID, PendingKind: row.PendingKind, PendingTargetDigest: row.PendingTargetDigest, PendingRevision: row.PendingRevision,
		InstalledAt: row.InstalledAt, UpdatedAt: row.UpdatedAt,
	}
}

func pluginInstanceDetails(rows []storage.PluginInstanceRow) ([]PluginInstanceDetail, error) {
	result := make([]PluginInstanceDetail, 0, len(rows))
	for _, row := range rows {
		targets, err := pluginTargetIDs(json.RawMessage(row.TargetJSON))
		if err != nil {
			return nil, ErrPluginReadProjection
		}
		config, err := pluginReadJSONObject(row.ConfigJSON)
		if err != nil {
			return nil, err
		}
		statusSummary, err := pluginReadJSONObject(row.StatusSummaryJSON)
		if err != nil {
			return nil, err
		}
		detail := PluginInstanceDetail{ID: row.ID, PluginID: row.PluginID, ResourceGroupID: row.ResourceGroupID, Targets: targets, Config: config, ConfigVersion: row.ConfigVersion, PendingVersion: row.PendingVersion, PendingOperationID: row.PendingOperationID, PendingResourceGroupID: row.PendingResourceGroupID, DesiredEnabled: row.DesiredEnabled, CurrentState: row.CurrentState, StatusSummary: statusSummary, StateVersion: row.StateVersion, UpdatedAt: row.UpdatedAt}
		if strings.TrimSpace(row.PendingConfigJSON) != "" {
			detail.PendingConfig, err = pluginReadJSONObject(row.PendingConfigJSON)
			if err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(row.PendingTargetJSON) != "" || strings.TrimSpace(row.PendingResourceGroupID) != "" {
			detail.PendingTargets, err = pluginTargetIDs(json.RawMessage(row.PendingTargetJSON))
			if err != nil {
				return nil, ErrPluginReadProjection
			}
		}
		result = append(result, detail)
	}
	return result, nil
}

func pluginGrantDetails(rows []storage.PluginGrantRow) []PluginGrantDetail {
	result := make([]PluginGrantDetail, 0, len(rows))
	for _, row := range rows {
		result = append(result, PluginGrantDetail{PackageDigest: row.PackageDigest, Permission: row.Permission, ResourceSelector: row.ResourceSelector, GrantedBy: row.GrantedBy, GrantedAt: row.GrantedAt})
	}
	return result
}

func pluginReadJSONObject(raw string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		return nil, ErrPluginReadProjection
	}
	return json.RawMessage(raw), nil
}

func pluginPackageDetail(row storage.PluginPackageRow, grants []storage.PluginGrantRow, activeDigest string) (PluginPackageDetail, error) {
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(row.ManifestJSON), &manifest); err != nil {
		return PluginPackageDetail{}, err
	}
	schema, err := plugins.DecodeConfigSchema([]byte(row.ConfigSchemaJSON))
	if err != nil {
		return PluginPackageDetail{}, err
	}
	permissions := normalizedPermissions(manifest.Permissions)
	current := make([]string, 0, len(grants))
	for _, grant := range grants {
		if activeDigest != "" && !strings.EqualFold(grant.PackageDigest, activeDigest) {
			continue
		}
		value := strings.TrimSpace(grant.Permission)
		if selector := strings.TrimSpace(grant.ResourceSelector); selector != "" {
			value += ":" + selector
		}
		current = append(current, value)
	}
	sort.Strings(current)
	return PluginPackageDetail{Digest: strings.ToLower(row.Digest), Version: row.Version, Manifest: manifest, ConfigSchema: schema, Permissions: permissions, PermissionDiff: permissionDiff(current, permissions)}, nil
}

func permissionDiff(current, desired []string) PluginPermissionDiff {
	currentSet, desiredSet := map[string]struct{}{}, map[string]struct{}{}
	for _, permission := range current {
		currentSet[permission] = struct{}{}
	}
	for _, permission := range desired {
		desiredSet[permission] = struct{}{}
	}
	diff := PluginPermissionDiff{Added: []string{}, Removed: []string{}}
	for _, permission := range desired {
		if _, ok := currentSet[permission]; !ok {
			diff.Added = append(diff.Added, permission)
		}
	}
	for _, permission := range current {
		if _, ok := desiredSet[permission]; !ok {
			diff.Removed = append(diff.Removed, permission)
		}
	}
	return diff
}

func (s *PluginService) pluginAgentStatuses(ctx context.Context, installed storage.InstalledPluginRow, instances []storage.PluginInstanceRow, operations []storage.PluginOperationRow) ([]PluginAgentStatus, error) {
	byID, err := s.authoritativePluginAgents(ctx)
	if err != nil {
		return nil, err
	}
	operationsByID := make(map[string]storage.PluginOperationRow, len(operations))
	for _, operation := range operations {
		operationsByID[operation.ID] = operation
	}
	statuses := make([]PluginAgentStatus, 0)
	for _, instance := range instances {
		summary, err := pluginReadJSONObject(instance.StatusSummaryJSON)
		if err != nil {
			return nil, err
		}
		activeTargets, err := pluginTargetIDs(json.RawMessage(instance.TargetJSON))
		if err != nil {
			return nil, ErrPluginReadProjection
		}
		pendingScope := instance.PendingOperationID != "" && (strings.TrimSpace(instance.PendingTargetJSON) != "" || strings.TrimSpace(instance.PendingResourceGroupID) != "")
		activeOperationID := ""
		if instance.PendingOperationID != "" && !pendingScope {
			activeOperationID = instance.PendingOperationID
		}
		statuses = appendPluginAgentStatuses(statuses, byID, operationsByID, installed, instance, activeTargets, "active", activeOperationID, summary)
		if pendingScope {
			pendingTargets, err := pluginTargetIDs(json.RawMessage(instance.PendingTargetJSON))
			if err != nil {
				return nil, ErrPluginReadProjection
			}
			statuses = appendPluginAgentStatuses(statuses, byID, operationsByID, installed, instance, pendingTargets, "pending", instance.PendingOperationID, summary)
		}
	}
	sort.Slice(statuses, func(i, j int) bool {
		if statuses[i].InstanceID == statuses[j].InstanceID {
			if statuses[i].TargetScope == statuses[j].TargetScope {
				return statuses[i].AgentID < statuses[j].AgentID
			}
			return statuses[i].TargetScope < statuses[j].TargetScope
		}
		return statuses[i].InstanceID < statuses[j].InstanceID
	})
	return statuses, nil
}

func appendPluginAgentStatuses(result []PluginAgentStatus, agents map[string]storage.AgentRow, operations map[string]storage.PluginOperationRow, installed storage.InstalledPluginRow, instance storage.PluginInstanceRow, targets []string, scope, operationID string, summary json.RawMessage) []PluginAgentStatus {
	operation := operations[operationID]
	targetRevision := operation.TargetRevision
	if targetRevision == 0 && operationID != "" && operationID == installed.PendingOperationID {
		targetRevision = installed.PendingRevision
	}
	for _, target := range targets {
		target = strings.TrimSpace(target)
		agent, available := agents[target]
		result = append(result, PluginAgentStatus{InstanceID: instance.ID, AgentID: target, TargetScope: scope, Available: available, CurrentState: instance.CurrentState, StatusSummary: summary, OperationID: operationID, OperationKind: operation.Kind, OperationStatus: operation.Status, TargetRevision: targetRevision, DesiredRevision: agent.DesiredRevision, CurrentRevision: agent.CurrentRevision, LastApplyRevision: agent.LastApplyRevision, LastApplyStatus: agent.LastApplyStatus, LastApplyMessage: agent.LastApplyMessage})
	}
	return result
}

func permissionsAdded(grants []storage.PluginGrantRow, activeDigest string, permissions []plugins.Permission) bool {
	existing := map[string]struct{}{}
	for _, grant := range grants {
		if !strings.EqualFold(grant.PackageDigest, activeDigest) {
			continue
		}
		value := grant.Permission
		if grant.ResourceSelector != "" {
			value += ":" + grant.ResourceSelector
		}
		existing[value] = struct{}{}
	}
	for _, permission := range normalizedPermissions(permissions) {
		if _, ok := existing[permission]; !ok {
			return true
		}
	}
	return false
}

func grantRows(pluginID, digest string, permissions []plugins.Permission, actorID string, now time.Time) []storage.PluginGrantRow {
	rows := make([]storage.PluginGrantRow, 0, len(permissions))
	for _, permission := range permissions {
		rows = append(rows, storage.PluginGrantRow{ID: lifecycleID("grant"), PluginID: pluginID, PackageDigest: digest, Permission: strings.TrimSpace(permission.Name), ResourceSelector: strings.TrimSpace(permission.Resource), GrantedBy: actorID, GrantedAt: now})
	}
	return rows
}

func pluginLifecycleAudit(operation storage.PluginOperationRow, _ string, result, errorClass string, now time.Time) storage.AuditEventRow {
	metadata, _ := json.Marshal(map[string]any{"operation_id": operation.ID, "operation_kind": operation.Kind, "package_digest": operation.TargetPackageDigest, "source_id": operation.SourceID, "source_kind": operation.SourceKind, "source_risk_label": operation.SourceRiskLabel})
	return storage.AuditEventRow{ID: lifecycleID("audit"), ActorID: operation.ActorID, SessionID: operation.SessionID, Action: "plugin." + operation.Kind, TargetKind: "plugin", TargetID: operation.PluginID, CorrelationID: operation.CorrelationID, Result: result, ErrorClass: errorClass, MetadataJSON: string(metadata), CreatedAt: now}
}

func pluginErrorClass(err error) string {
	switch {
	case errors.Is(err, ErrPluginPermissionConfirmation):
		return "permission_confirmation"
	case errors.Is(err, ErrPluginRiskConfirmation):
		return "source_risk_confirmation"
	case errors.Is(err, ErrPluginUninstallBlocked):
		return "runtime_not_drained"
	case errors.Is(err, ErrPluginNotInstalled):
		return "not_installed"
	default:
		return "validation"
	}
}

func lifecycleID(prefix string) string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(value)
}
