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
)

type PluginPackageCandidate struct {
	Package   plugins.ValidatedPackage
	CachePath string
}

type PluginInstallRequest struct {
	Package              PluginPackageCandidate
	SourceKind           string
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
	SourceKind           string
}

type PluginUninstallRequest struct {
	PluginID string
	ActorID  string
	Drained  bool
}

type pluginLifecycleStore interface {
	InstallPlugin(context.Context, storage.PluginInstallTransaction) error
	ApplyPluginMutation(context.Context, storage.PluginMutation) error
	RecordPluginOperation(context.Context, storage.PluginOperationRow, storage.AuditEventRow) error
	GetInstalledPlugin(context.Context, string) (storage.InstalledPluginRow, bool, error)
	GetPluginPackage(context.Context, string) (storage.PluginPackageRow, bool, error)
	ListPluginGrants(context.Context, string) ([]storage.PluginGrantRow, error)
	GetPluginInstance(context.Context, string) (storage.PluginInstanceRow, bool, error)
	ListPluginOperations(context.Context, string) ([]storage.PluginOperationRow, error)
}

type PluginService struct {
	store pluginLifecycleStore
	now   func() time.Time
}

func NewPluginService(store pluginLifecycleStore) *PluginService {
	return &PluginService{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *PluginService) Status(ctx context.Context, pluginID string) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !ok {
		return storage.InstalledPluginRow{}, ErrPluginNotInstalled
	}
	return installed, nil
}

func (s *PluginService) Operations(ctx context.Context, pluginID string) ([]storage.PluginOperationRow, error) {
	return s.store.ListPluginOperations(ctx, pluginID)
}

func (s *PluginService) Install(ctx context.Context, request PluginInstallRequest) (storage.InstalledPluginRow, error) {
	manifest := request.Package.Package.Manifest
	operation := s.operation(manifest.ID, "install", request.Package.Package.Digest, request.ActorID)
	if err := validatePackageCandidate(request.Package); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := confirmSourceRisk(request.SourceKind, request.RiskAccepted); err != nil {
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
	installed := storage.InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: strings.ToLower(request.Package.Package.Digest), DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: string(cleanupJSON), LastOperationID: operation.ID, InstalledAt: now, UpdatedAt: now}
	packageRow := storage.PluginPackageRow{Digest: installed.ActivePackageDigest, PluginID: manifest.ID, Version: manifest.Version, CachePath: request.Package.CachePath, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: string(schemaJSON), VerifiedAt: now}
	grants := grantRows(manifest.ID, installed.ActivePackageDigest, manifest.Permissions, request.ActorID, now)
	transaction := storage.PluginInstallTransaction{Package: packageRow, Installed: installed, Grants: grants, Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "success", "", now)}
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
	operation := s.operation(pluginID, kind, installed.ActivePackageDigest, actorID)
	if !ok {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, ErrPluginNotInstalled)
	}
	if installed.DesiredLifecycle == desired && (installed.CurrentLifecycle == current || installed.CurrentLifecycle == "applying") {
		return installed, nil
	}
	now := s.now()
	installed.DesiredLifecycle, installed.CurrentLifecycle, installed.UpdatedAt = desired, "applying", now
	installed.LastOperationID = operation.ID
	operation.Status = "applying"
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: pluginID, ExpectedActive: installed.ActivePackageDigest, Installed: &installed, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, "accepted", "", now)})
	return installed, err
}

// CompleteLifecycleApply records the Agent/revision result separately from the
// desired-state mutation. A failed apply never changes the active package.
func (s *PluginService) CompleteLifecycleApply(ctx context.Context, pluginID, actorID string, applied bool, agentResults any) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(pluginID, "lifecycle_complete", installed.ActivePackageDigest, actorID)
	if !ok {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, ErrPluginNotInstalled)
	}
	encodedResults, err := json.Marshal(agentResults)
	if err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	now := s.now()
	operation.AgentResultsJSON, operation.CompletedAt = string(encodedResults), &now
	if applied {
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
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	result := "success"
	if !applied {
		result = "failure"
	}
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: pluginID, ExpectedActive: installed.ActivePackageDigest, Installed: &installed, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, result, operation.ErrorClass, now)})
	return installed, err
}

func (s *PluginService) Configure(ctx context.Context, request PluginConfigureRequest) (storage.PluginInstanceRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	operation := s.operation(request.PluginID, "configure", installed.ActivePackageDigest, request.ActorID)
	if !ok {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	packageRow, ok, err := s.store.GetPluginPackage(ctx, installed.ActivePackageDigest)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if !ok {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, errors.New("active plugin package is unavailable"))
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(packageRow.ConfigSchemaJSON), &schema); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := plugins.ValidateConfig(schema, request.Config); err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if strings.TrimSpace(request.InstanceID) == "" {
		request.InstanceID = lifecycleID("instance")
	}
	instance, exists, err := s.store.GetPluginInstance(ctx, request.InstanceID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if exists && instance.PluginID != request.PluginID {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, errors.New("plugin instance identity mismatch"))
	}
	targetJSON, err := json.Marshal(request.Targets)
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	now := s.now()
	version := instance.ConfigVersion + 1
	if instance.PendingVersion >= version {
		version = instance.PendingVersion + 1
	}
	if !exists {
		instance = storage.PluginInstanceRow{ID: request.InstanceID, PluginID: request.PluginID, ConfigJSON: "{}", StatusSummaryJSON: "{}"}
	}
	instance.ResourceGroupID = request.ResourceGroupID
	instance.TargetJSON = string(targetJSON)
	instance.PendingConfigJSON = string(request.Config)
	instance.PendingVersion = version
	instance.DesiredEnabled = installed.DesiredLifecycle == "enabled"
	instance.CurrentState = "applying"
	instance.UpdatedAt = now
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	operation.Status = "applying"
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: request.PluginID, ExpectedActive: installed.ActivePackageDigest, Installed: &installed, ReplaceInstance: &instance, Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "accepted", "", now)})
	return instance, err
}

func (s *PluginService) CompleteConfigure(ctx context.Context, pluginID, instanceID, actorID string, applied bool, agentResults any) (storage.PluginInstanceRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	operation := s.operation(pluginID, "configure_complete", installed.ActivePackageDigest, actorID)
	if !ok {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, actorID, ErrPluginNotInstalled)
	}
	instance, exists, err := s.store.GetPluginInstance(ctx, instanceID)
	if err != nil {
		return storage.PluginInstanceRow{}, err
	}
	if !exists || instance.PluginID != pluginID || instance.PendingVersion == 0 {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, actorID, errors.New("pending plugin configuration is unavailable"))
	}
	encodedResults, err := json.Marshal(agentResults)
	if err != nil {
		return storage.PluginInstanceRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	now := s.now()
	operation.AgentResultsJSON, operation.CompletedAt = string(encodedResults), &now
	if applied {
		instance.ConfigJSON, instance.ConfigVersion = instance.PendingConfigJSON, instance.PendingVersion
		instance.PendingConfigJSON, instance.PendingVersion = "", 0
		if installed.CurrentLifecycle == "active" {
			instance.CurrentState = "active"
		} else {
			instance.CurrentState = "disabled"
		}
		operation.Status = "succeeded"
	} else {
		instance.PendingConfigJSON, instance.PendingVersion = "", 0
		instance.CurrentState = "degraded"
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply plugin configuration"
	}
	instance.StatusSummaryJSON = string(encodedResults)
	instance.UpdatedAt = now
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	result := "success"
	if !applied {
		result = "failure"
	}
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: pluginID, ExpectedActive: installed.ActivePackageDigest, Installed: &installed, ReplaceInstance: &instance, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, result, operation.ErrorClass, now)})
	return instance, err
}

func (s *PluginService) Upgrade(ctx context.Context, request PluginUpgradeRequest) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, request.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(request.PluginID, "upgrade", request.Package.Package.Digest, request.ActorID)
	if !ok {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	if err := validatePackageCandidate(request.Package); err != nil || request.Package.Package.Manifest.ID != request.PluginID {
		if err == nil {
			err = errors.New("upgrade package identity mismatch")
		}
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	if err := confirmSourceRisk(request.SourceKind, request.RiskAccepted); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
	}
	grants, err := s.store.ListPluginGrants(ctx, request.PluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if permissionsAdded(grants, request.Package.Package.Manifest.Permissions) {
		if err := confirmPermissions(request.Package.Package.Manifest.Permissions, request.ConfirmedPermissions, true); err != nil {
			return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, err)
		}
	}
	if strings.EqualFold(installed.ActivePackageDigest, request.Package.Package.Digest) {
		return installed, nil
	}
	if installed.StagedPackageDigest != "" {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, request.ActorID, errors.New("another plugin upgrade is already staged"))
	}
	now := s.now()
	manifestJSON, _ := json.Marshal(request.Package.Package.Manifest)
	schemaJSON, _ := json.Marshal(request.Package.Package.ConfigSchema)
	oldDigest := installed.ActivePackageDigest
	installed.StagedPackageDigest = strings.ToLower(request.Package.Package.Digest)
	installed.CurrentLifecycle = "upgrading"
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	operation.Status = "staged"
	packageRow := storage.PluginPackageRow{Digest: installed.StagedPackageDigest, PluginID: request.PluginID, Version: request.Package.Package.Manifest.Version, CachePath: request.Package.CachePath, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: string(schemaJSON), VerifiedAt: now}
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: request.PluginID, ExpectedActive: oldDigest, Installed: &installed, Package: &packageRow, Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "accepted", "", now)})
	return installed, err
}

func (s *PluginService) CompleteUpgrade(ctx context.Context, pluginID, actorID string, applied bool, agentResults any) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(pluginID, "upgrade_complete", installed.StagedPackageDigest, actorID)
	if !ok || installed.StagedPackageDigest == "" || installed.CurrentLifecycle != "upgrading" {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("staged upgrade is unavailable"))
	}
	packageRow, exists, err := s.store.GetPluginPackage(ctx, installed.StagedPackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("staged package cache record is unavailable"))
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	encodedResults, err := json.Marshal(agentResults)
	if err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	now, oldDigest := s.now(), installed.ActivePackageDigest
	operation.AgentResultsJSON, operation.CompletedAt = string(encodedResults), &now
	var grants []storage.PluginGrantRow
	if applied {
		installed.ActivePackageDigest, installed.RollbackPackageDigest = installed.StagedPackageDigest, oldDigest
		installed.StagedPackageDigest = ""
		cleanupJSON, _ := json.Marshal(manifest.Cleanup)
		installed.CleanupPolicyJSON = string(cleanupJSON)
		if installed.DesiredLifecycle == "enabled" {
			installed.CurrentLifecycle = "active"
		} else {
			installed.CurrentLifecycle = "disabled"
		}
		operation.Status = "succeeded"
		grants = grantRows(pluginID, installed.ActivePackageDigest, manifest.Permissions, actorID, now)
	} else {
		installed.StagedPackageDigest = ""
		installed.CurrentLifecycle = "degraded"
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply staged plugin package"
	}
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	result := "success"
	if !applied {
		result = "failure"
	}
	mutation := storage.PluginMutation{PluginID: pluginID, ExpectedActive: oldDigest, Installed: &installed, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, result, operation.ErrorClass, now)}
	if applied {
		mutation.ReplaceGrants = grants
	}
	err = s.store.ApplyPluginMutation(ctx, mutation)
	return installed, err
}

func (s *PluginService) Rollback(ctx context.Context, pluginID, actorID string) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(pluginID, "rollback", installed.RollbackPackageDigest, actorID)
	if !ok || installed.RollbackPackageDigest == "" {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("rollback package is unavailable"))
	}
	_, exists, err := s.store.GetPluginPackage(ctx, installed.RollbackPackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("rollback package cache record is unavailable"))
	}
	if installed.StagedPackageDigest != "" {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("another plugin package transition is already staged"))
	}
	now, oldDigest := s.now(), installed.ActivePackageDigest
	installed.StagedPackageDigest = installed.RollbackPackageDigest
	installed.CurrentLifecycle = "rolling_back"
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	operation.Status = "staged"
	err = s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: pluginID, ExpectedActive: oldDigest, Installed: &installed, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, "accepted", "", now)})
	return installed, err
}

func (s *PluginService) CompleteRollback(ctx context.Context, pluginID, actorID string, applied bool, agentResults any) (storage.InstalledPluginRow, error) {
	installed, ok, err := s.store.GetInstalledPlugin(ctx, pluginID)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	operation := s.operation(pluginID, "rollback_complete", installed.StagedPackageDigest, actorID)
	if !ok || installed.StagedPackageDigest == "" || installed.CurrentLifecycle != "rolling_back" {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("staged rollback is unavailable"))
	}
	packageRow, exists, err := s.store.GetPluginPackage(ctx, installed.StagedPackageDigest)
	if err != nil {
		return storage.InstalledPluginRow{}, err
	}
	if !exists {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, errors.New("rollback package cache record is unavailable"))
	}
	var manifest plugins.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	encodedResults, err := json.Marshal(agentResults)
	if err != nil {
		return storage.InstalledPluginRow{}, s.recordFailure(ctx, operation, actorID, err)
	}
	now, oldDigest := s.now(), installed.ActivePackageDigest
	operation.AgentResultsJSON, operation.CompletedAt = string(encodedResults), &now
	var grants []storage.PluginGrantRow
	if applied {
		installed.ActivePackageDigest, installed.RollbackPackageDigest = installed.StagedPackageDigest, oldDigest
		installed.StagedPackageDigest = ""
		cleanupJSON, _ := json.Marshal(manifest.Cleanup)
		installed.CleanupPolicyJSON = string(cleanupJSON)
		if installed.DesiredLifecycle == "enabled" {
			installed.CurrentLifecycle = "active"
		} else {
			installed.CurrentLifecycle = "disabled"
		}
		operation.Status = "succeeded"
		grants = grantRows(pluginID, installed.ActivePackageDigest, manifest.Permissions, actorID, now)
	} else {
		installed.StagedPackageDigest = ""
		installed.CurrentLifecycle = "degraded"
		operation.Status, operation.ErrorClass, operation.Error = "failed", "agent_apply", "one or more target agents failed to apply rollback package"
	}
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	result := "success"
	if !applied {
		result = "failure"
	}
	mutation := storage.PluginMutation{PluginID: pluginID, ExpectedActive: oldDigest, Installed: &installed, Operation: operation, Audit: pluginLifecycleAudit(operation, actorID, result, operation.ErrorClass, now)}
	if applied {
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
	operation := s.operation(request.PluginID, "uninstall", installed.ActivePackageDigest, request.ActorID)
	if !ok {
		return s.recordFailure(ctx, operation, request.ActorID, ErrPluginNotInstalled)
	}
	if installed.CurrentLifecycle != "disabled" || !request.Drained {
		return s.recordFailure(ctx, operation, request.ActorID, ErrPluginUninstallBlocked)
	}
	var cleanup plugins.CleanupPolicy
	if err := json.Unmarshal([]byte(installed.CleanupPolicyJSON), &cleanup); err != nil {
		return s.recordFailure(ctx, operation, request.ActorID, err)
	}
	now := s.now()
	operation.Status, operation.CompletedAt = "succeeded", &now
	return s.store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: request.PluginID, ExpectedActive: installed.ActivePackageDigest, DeletePlugin: true, DeleteInstances: cleanup.Instances == "delete", DeleteGrants: cleanup.Grants == "delete", Operation: operation, Audit: pluginLifecycleAudit(operation, request.ActorID, "success", "", now)})
}

func (s *PluginService) operation(pluginID, kind, digest, actorID string) storage.PluginOperationRow {
	return storage.PluginOperationRow{ID: lifecycleID("pluginop"), PluginID: pluginID, Kind: kind, Status: "running", TargetPackageDigest: strings.ToLower(digest), AgentResultsJSON: "{}", ActorID: actorID, CreatedAt: s.now()}
}

func (s *PluginService) recordFailure(ctx context.Context, operation storage.PluginOperationRow, actorID string, cause error) error {
	now := s.now()
	operation.Status, operation.ErrorClass, operation.Error, operation.CompletedAt = "failed", pluginErrorClass(cause), cause.Error(), &now
	if err := s.store.RecordPluginOperation(ctx, operation, pluginLifecycleAudit(operation, actorID, "failure", operation.ErrorClass, now)); err != nil {
		return fmt.Errorf("%w (persist operation failure: %v)", cause, err)
	}
	return cause
}

func validatePackageCandidate(candidate PluginPackageCandidate) error {
	digest := strings.ToLower(strings.TrimSpace(candidate.Package.Digest))
	if candidate.Package.Manifest.ID == "" || candidate.Package.Manifest.Version == "" || !isHexDigest(digest) || candidate.CachePath == "" {
		return errors.New("validated digest-addressed package candidate is required")
	}
	if !strings.EqualFold(filepath.Base(filepath.Clean(candidate.CachePath)), digest) {
		return errors.New("verified cache path is not addressed by package digest")
	}
	computed, err := plugins.ComputePackageDigest(candidate.CachePath)
	if err != nil || !strings.EqualFold(computed, digest) {
		return errors.New("verified cache contents do not match package digest")
	}
	revalidated, err := plugins.NewValidator(plugins.ValidatorOptions{}).ValidatePackage(candidate.CachePath, plugins.PackageExpectation{ID: candidate.Package.Manifest.ID, Version: candidate.Package.Manifest.Version, SHA256: digest})
	if err != nil {
		return fmt.Errorf("revalidate cached package: %w", err)
	}
	if !reflect.DeepEqual(revalidated.Manifest, candidate.Package.Manifest) || !reflect.DeepEqual(revalidated.ConfigSchema, candidate.Package.ConfigSchema) {
		return errors.New("package candidate projection differs from verified cache")
	}
	return nil
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

func permissionsAdded(grants []storage.PluginGrantRow, permissions []plugins.Permission) bool {
	existing := map[string]struct{}{}
	for _, grant := range grants {
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

func pluginLifecycleAudit(operation storage.PluginOperationRow, actorID, result, errorClass string, now time.Time) storage.AuditEventRow {
	metadata, _ := json.Marshal(map[string]any{"operation_id": operation.ID, "operation_kind": operation.Kind, "package_digest": operation.TargetPackageDigest})
	return storage.AuditEventRow{ID: lifecycleID("audit"), ActorID: actorID, Action: "plugin." + operation.Kind, TargetKind: "plugin", TargetID: operation.PluginID, CorrelationID: operation.ID, Result: result, ErrorClass: errorClass, MetadataJSON: string(metadata), CreatedAt: now}
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
